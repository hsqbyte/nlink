package services

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fastgox/utils/logger"
)

// UDP 转发实现
//
// 设计：
//  1. 每个 UDP 客户端地址(cliAddr) 对应一条 workConn (简单 NAT 会话)
//  2. workConn 上用 [len:2][payload] 分帧承载 UDP 包
//  3. 空闲超时（60s）自动回收 workConn
//
// 这个方案比独立一条控制消息简单，也不改变 TCP 控制通道协议。

const (
	udpSessionIdleTimeout = 60 * time.Second
	udpMaxPacketSize      = 65507 // IPv4 UDP 理论上限
)

// UDPProxy 管理一个 UDP 代理端口
type UDPProxy struct {
	Name       string
	RemotePort int
	PeerConnID string
	Conn       *net.UDPConn
	WorkConnCh chan net.Conn
	tunnel     *TunnelService

	acl          *ACL
	rateLimitBps int64

	// 统计
	TotalSessions atomic.Int64
	ActiveSess    atomic.Int64
	BytesIn       atomic.Int64
	BytesOut      atomic.Int64
	RejectedACL   atomic.Int64

	mu       sync.Mutex
	sessions map[string]*udpSession // key = clientAddr.String()
	closed   bool
}

type udpSession struct {
	workConn net.Conn
	lastSeen atomic.Int64 // unix nano
	cliAddr  *net.UDPAddr
}

// NewUDPProxy 监听指定端口
func NewUDPProxy(name string, remotePort int, peerConnID string, tunnel *TunnelService, opts *ProxyOptions) (*UDPProxy, error) {
	addr := net.UDPAddr{Port: remotePort}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		return nil, err
	}
	p := &UDPProxy{
		Name:       name,
		RemotePort: remotePort,
		PeerConnID: peerConnID,
		Conn:       conn,
		WorkConnCh: make(chan net.Conn, 20),
		tunnel:     tunnel,
		sessions:   make(map[string]*udpSession),
	}
	if opts != nil {
		if len(opts.AllowCIDR) > 0 || len(opts.DenyCIDR) > 0 {
			acl, err := ParseACL(opts.AllowCIDR, opts.DenyCIDR)
			if err != nil {
				conn.Close()
				return nil, err
			}
			p.acl = acl
		}
		p.rateLimitBps = opts.RateLimit
	}
	return p, nil
}

// Run 读包循环
func (p *UDPProxy) Run() {
	logger.Info("[UDPProxy:%s] 监听 UDP :%d", p.Name, p.RemotePort)
	go p.reapLoop()
	buf := make([]byte, udpMaxPacketSize)
	for {
		n, cliAddr, err := p.Conn.ReadFromUDP(buf)
		if err != nil {
			p.mu.Lock()
			closed := p.closed
			p.mu.Unlock()
			if closed {
				return
			}
			logger.Error("[UDPProxy:%s] read: %v", p.Name, err)
			continue
		}
		if p.acl != nil && !p.acl.Allow(cliAddr.String()) {
			p.RejectedACL.Add(1)
			continue
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		go p.handlePacket(cliAddr, pkt)
	}
}

func (p *UDPProxy) handlePacket(cli *net.UDPAddr, pkt []byte) {
	key := cli.String()
	p.mu.Lock()
	sess, ok := p.sessions[key]
	if !ok {
		// 新会话：请求 workConn
		p.tunnel.RequestWorkConn(p.Name, p.PeerConnID)
		wc, err := p.waitForWorkConn()
		if err != nil {
			p.mu.Unlock()
			logger.Warn("[UDPProxy:%s] 获取 workConn 失败: %v", p.Name, err)
			return
		}
		if err := sendActivation(wc, p.Name); err != nil {
			p.mu.Unlock()
			wc.Close()
			logger.Warn("[UDPProxy:%s] 激活失败: %v", p.Name, err)
			return
		}
		sess = &udpSession{workConn: wc, cliAddr: cli}
		sess.lastSeen.Store(time.Now().UnixNano())
		p.sessions[key] = sess
		p.TotalSessions.Add(1)
		p.ActiveSess.Add(1)
		go p.recvFromWork(sess)
	}
	p.mu.Unlock()

	// 发送分帧数据到 workConn
	if err := writeUDPFrame(sess.workConn, pkt); err != nil {
		logger.Debug("[UDPProxy:%s] 写 workConn 失败: %v", p.Name, err)
		p.closeSession(key)
		return
	}
	sess.lastSeen.Store(time.Now().UnixNano())
	p.BytesIn.Add(int64(len(pkt)))
}

func (p *UDPProxy) waitForWorkConn() (net.Conn, error) {
	select {
	case wc, ok := <-p.WorkConnCh:
		if !ok {
			return nil, fmt.Errorf("workConn 通道已关闭")
		}
		return wc, nil
	case <-time.After(p.tunnel.workConnTimeout):
		return nil, fmt.Errorf("等待 workConn 超时")
	}
}

func (p *UDPProxy) recvFromWork(sess *udpSession) {
	for {
		payload, err := readUDPFrame(sess.workConn)
		if err != nil {
			if err != io.EOF {
				logger.Debug("[UDPProxy:%s] 读 workConn 结束: %v", p.Name, err)
			}
			p.closeSession(sess.cliAddr.String())
			return
		}
		if _, err := p.Conn.WriteToUDP(payload, sess.cliAddr); err != nil {
			logger.Debug("[UDPProxy:%s] 写回客户端失败: %v", p.Name, err)
			p.closeSession(sess.cliAddr.String())
			return
		}
		sess.lastSeen.Store(time.Now().UnixNano())
		p.BytesOut.Add(int64(len(payload)))
	}
}

func (p *UDPProxy) closeSession(key string) {
	p.mu.Lock()
	sess, ok := p.sessions[key]
	if ok {
		delete(p.sessions, key)
	}
	p.mu.Unlock()
	if ok {
		sess.workConn.Close()
		p.ActiveSess.Add(-1)
	}
}

func (p *UDPProxy) reapLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		<-ticker.C
		p.mu.Lock()
		closed := p.closed
		if closed {
			p.mu.Unlock()
			return
		}
		now := time.Now().UnixNano()
		var expired []string
		for k, s := range p.sessions {
			if time.Duration(now-s.lastSeen.Load()) > udpSessionIdleTimeout {
				expired = append(expired, k)
			}
		}
		p.mu.Unlock()
		for _, k := range expired {
			p.closeSession(k)
		}
	}
}

// Close 幂等关闭
func (p *UDPProxy) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.Conn.Close()
	close(p.WorkConnCh)
	sessions := p.sessions
	p.sessions = nil
	p.mu.Unlock()
	for _, s := range sessions {
		s.workConn.Close()
	}
}

// ---- 帧格式：[len:uint16 BE][payload] ----

func writeUDPFrame(w io.Writer, payload []byte) error {
	if len(payload) > 0xFFFF {
		return fmt.Errorf("packet too large: %d", len(payload))
	}
	hdr := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(hdr[:2], uint16(len(payload)))
	copy(hdr[2:], payload)
	_, err := w.Write(hdr)
	return err
}

func readUDPFrame(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(hdr[:])
	if n == 0 {
		return []byte{}, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
