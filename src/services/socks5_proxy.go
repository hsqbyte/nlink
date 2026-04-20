package services

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fastgox/utils/logger"
)

// SOCKS5Proxy SOCKS5 代理：在公网端口做 SOCKS5 握手，目标由客户端 CONNECT 指定，
// 由服务端解析后通过激活协议 0x02 动态下发给节点拨号。
type SOCKS5Proxy struct {
	Name       string
	RemotePort int
	PeerConnID string
	Listener   net.Listener
	WorkConnCh chan net.Conn
	tunnel     *TunnelService

	acl          *ACL
	rateLimitBps int64

	TotalConns   atomic.Int64
	ActiveConns  atomic.Int64
	BytesIn      atomic.Int64
	BytesOut     atomic.Int64
	PoolHits     atomic.Int64
	OnDemandHits atomic.Int64
	RejectedACL  atomic.Int64

	mu     sync.Mutex
	closed bool
}

// NewSOCKS5Proxy 创建并立即监听
func NewSOCKS5Proxy(name string, remotePort int, peerConnID string, tunnel *TunnelService, opts *ProxyOptions) (*SOCKS5Proxy, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", remotePort))
	if err != nil {
		return nil, err
	}
	p := &SOCKS5Proxy{
		Name:       name,
		RemotePort: remotePort,
		PeerConnID: peerConnID,
		Listener:   ln,
		WorkConnCh: make(chan net.Conn, 20),
		tunnel:     tunnel,
	}
	if opts != nil {
		if len(opts.AllowCIDR) > 0 || len(opts.DenyCIDR) > 0 {
			acl, err := ParseACL(opts.AllowCIDR, opts.DenyCIDR)
			if err != nil {
				ln.Close()
				return nil, err
			}
			p.acl = acl
		}
		p.rateLimitBps = opts.RateLimit
	}
	return p, nil
}

// Run 接受 SOCKS5 用户连接
func (p *SOCKS5Proxy) Run() {
	logger.Info("[SOCKS5:%s] 监听端口 :%d", p.Name, p.RemotePort)
	for {
		userConn, err := p.Listener.Accept()
		if err != nil {
			p.mu.Lock()
			closed := p.closed
			p.mu.Unlock()
			if closed {
				return
			}
			logger.Error("[SOCKS5:%s] accept: %v", p.Name, err)
			continue
		}

		if p.acl != nil && !p.acl.Allow(userConn.RemoteAddr().String()) {
			p.RejectedACL.Add(1)
			userConn.Close()
			continue
		}
		if p.rateLimitBps > 0 {
			userConn = wrapConnWithRateLimit(userConn, p.rateLimitBps)
		}

		go p.handle(userConn)
	}
}

// handle 完成 SOCKS5 握手，拿到 target 后请求 workConn 并转发
func (p *SOCKS5Proxy) handle(uc net.Conn) {
	uc.SetDeadline(time.Now().Add(10 * time.Second))
	target, err := socks5Handshake(uc)
	uc.SetDeadline(time.Time{})
	if err != nil {
		logger.Warn("[SOCKS5:%s] 握手失败: %v", p.Name, err)
		uc.Close()
		return
	}

	// 尝试连接池
	if pool := p.tunnel.getPeerPool(p.PeerConnID); pool != nil {
		if wc, ok := pool.TryGet(); ok {
			p.PoolHits.Add(1)
			p.TotalConns.Add(1)
			p.ActiveConns.Add(1)
			if err := sendActivationWithTarget(wc, p.Name, target); err != nil {
				logger.Error("[SOCKS5:%s] 激活池连接失败: %v", p.Name, err)
				uc.Close()
				wc.Close()
				p.ActiveConns.Add(-1)
				return
			}
			p.relay(uc, wc)
			return
		}
	}

	// 按需
	p.tunnel.RequestWorkConn(p.Name, p.PeerConnID)
	timeout := p.tunnel.workConnTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	select {
	case wc, ok := <-p.WorkConnCh:
		if !ok {
			uc.Close()
			return
		}
		p.OnDemandHits.Add(1)
		p.TotalConns.Add(1)
		p.ActiveConns.Add(1)
		if err := sendActivationWithTarget(wc, p.Name, target); err != nil {
			logger.Error("[SOCKS5:%s] 激活失败: %v", p.Name, err)
			uc.Close()
			wc.Close()
			p.ActiveConns.Add(-1)
			return
		}
		p.relay(uc, wc)
	case <-time.After(timeout):
		logger.Warn("[SOCKS5:%s] 工作连接超时", p.Name)
		uc.Close()
	}
}

func (p *SOCKS5Proxy) relay(uc, wc net.Conn) {
	defer p.ActiveConns.Add(-1)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(wc, uc)
		p.BytesOut.Add(n)
	}()
	go func() {
		defer wg.Done()
		n, _ := io.Copy(uc, wc)
		p.BytesIn.Add(n)
	}()
	wg.Wait()
	uc.Close()
	wc.Close()
}

// Close 幂等关闭
func (p *SOCKS5Proxy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.Listener.Close()
		close(p.WorkConnCh)
	}
}

// ---- SOCKS5 握手 (RFC 1928，支持 no-auth / CONNECT / IPv4 / IPv6 / domain) ----

// socks5Handshake 完成握手并向客户端回 "成功" 响应，返回 "host:port"
func socks5Handshake(c net.Conn) (string, error) {
	// 1. 方法协商: VER(1) NMETHODS(1) METHODS(n)
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return "", err
	}
	if hdr[0] != 0x05 {
		return "", fmt.Errorf("unsupported socks version: %d", hdr[0])
	}
	nMethods := int(hdr[1])
	if nMethods == 0 || nMethods > 255 {
		return "", fmt.Errorf("invalid nmethods: %d", nMethods)
	}
	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(c, methods); err != nil {
		return "", err
	}
	// 选择 NO-AUTH (0x00)
	hasNoAuth := false
	for _, m := range methods {
		if m == 0x00 {
			hasNoAuth = true
			break
		}
	}
	if !hasNoAuth {
		c.Write([]byte{0x05, 0xFF})
		return "", fmt.Errorf("no supported auth method")
	}
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return "", err
	}

	// 2. 请求: VER(1) CMD(1) RSV(1) ATYP(1) DST.ADDR DST.PORT(2)
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil {
		return "", err
	}
	if req[0] != 0x05 {
		return "", fmt.Errorf("bad request version")
	}
	if req[1] != 0x01 { // 仅支持 CONNECT
		socks5Reply(c, 0x07) // Command not supported
		return "", fmt.Errorf("unsupported cmd: %d", req[1])
	}
	var host string
	switch req[3] {
	case 0x01: // IPv4
		buf := make([]byte, 4)
		if _, err := io.ReadFull(c, buf); err != nil {
			return "", err
		}
		host = net.IP(buf).String()
	case 0x03: // domain
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(c, lenBuf); err != nil {
			return "", err
		}
		dl := int(lenBuf[0])
		if dl == 0 {
			return "", fmt.Errorf("empty domain")
		}
		buf := make([]byte, dl)
		if _, err := io.ReadFull(c, buf); err != nil {
			return "", err
		}
		host = string(buf)
	case 0x04: // IPv6
		buf := make([]byte, 16)
		if _, err := io.ReadFull(c, buf); err != nil {
			return "", err
		}
		host = net.IP(buf).String()
	default:
		socks5Reply(c, 0x08) // address type not supported
		return "", fmt.Errorf("unsupported atyp: %d", req[3])
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(c, portBuf); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBuf)

	// 握手成功回应（此时尚未真正拨号，但协议要求在此处回应；若后续失败由 relay 关闭）
	if err := socks5Reply(c, 0x00); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

// socks5Reply 写 SOCKS5 应答 (BND.ADDR=0.0.0.0:0)
func socks5Reply(c net.Conn, rep byte) error {
	resp := []byte{0x05, rep, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	_, err := c.Write(resp)
	return err
}
