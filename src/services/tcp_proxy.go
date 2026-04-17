package services

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fastgox/utils/logger"
)

// TCPProxy 管理一个公网端口监听
type TCPProxy struct {
	Name       string
	RemotePort int
	PeerConnID string
	Listener   net.Listener
	WorkConnCh chan net.Conn
	tunnel     *TunnelService

	// 统计
	TotalConns   atomic.Int64
	ActiveConns  atomic.Int64
	BytesIn      atomic.Int64
	BytesOut     atomic.Int64
	PoolHits     atomic.Int64
	OnDemandHits atomic.Int64

	mu     sync.Mutex
	closed bool
}

// NewTCPProxy 创建 TCP 代理并立即监听端口
func NewTCPProxy(name string, remotePort int, peerConnID string, tunnel *TunnelService) (*TCPProxy, error) {
	addr := net.TCPAddr{Port: remotePort}
	ln, err := net.ListenTCP("tcp", &addr)
	if err != nil {
		return nil, err
	}
	return &TCPProxy{
		Name:       name,
		RemotePort: remotePort,
		PeerConnID: peerConnID,
		Listener:   ln,
		WorkConnCh: make(chan net.Conn, 20),
		tunnel:     tunnel,
	}, nil
}

// Run 接受用户连接并与工作连接配对
func (p *TCPProxy) Run() {
	logger.Info("[Proxy:%s] 监听端口 :%d", p.Name, p.RemotePort)
	for {
		userConn, err := p.Listener.Accept()
		if err != nil {
			p.mu.Lock()
			closed := p.closed
			p.mu.Unlock()
			if closed {
				return
			}
			logger.Error("[Proxy:%s] accept: %v", p.Name, err)
			continue
		}

		logger.Info("[Proxy:%s] 新用户连接: %s", p.Name, userConn.RemoteAddr())

		// 优先从对端全局连接池取预建连接
		if pool := p.tunnel.getPeerPool(p.PeerConnID); pool != nil {
			if workConn, ok := pool.TryGet(); ok {
				p.PoolHits.Add(1)
				p.TotalConns.Add(1)
				p.ActiveConns.Add(1)
				logger.Info("[Proxy:%s] 连接池命中: user=%s <-> work=%s", p.Name, userConn.RemoteAddr(), workConn.RemoteAddr())
				go func(uc, wc net.Conn) {
					defer p.ActiveConns.Add(-1)
					if err := sendActivation(wc, p.Name); err != nil {
						logger.Error("[Proxy:%s] 激活池连接失败: %v", p.Name, err)
						uc.Close()
						wc.Close()
						return
					}
					p.relay(uc, wc)
				}(userConn, workConn)
				continue
			}
		}

		// 池空，走按需请求
		p.tunnel.RequestWorkConn(p.Name, p.PeerConnID)

		go func(uc net.Conn) {
			select {
			case workConn := <-p.WorkConnCh:
				p.OnDemandHits.Add(1)
				p.TotalConns.Add(1)
				p.ActiveConns.Add(1)
				logger.Info("[Proxy:%s] 开始转发: user=%s <-> work=%s", p.Name, uc.RemoteAddr(), workConn.RemoteAddr())
				defer p.ActiveConns.Add(-1)
				p.relay(uc, workConn)
			case <-time.After(p.tunnel.workConnTimeout):
				logger.Warn("[Proxy:%s] 工作连接超时", p.Name)
				uc.Close()
			}
		}(userConn)
	}
}

// Close 关闭代理（幂等）
func (p *TCPProxy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.Listener.Close()
		close(p.WorkConnCh)
	}
}

// sendActivation 发送激活信号（携带代理名）
func sendActivation(conn net.Conn, proxyName string) error {
	nameBytes := []byte(proxyName)
	buf := make([]byte, 3+len(nameBytes))
	buf[0] = 0x01
	binary.BigEndian.PutUint16(buf[1:3], uint16(len(nameBytes)))
	copy(buf[3:], nameBytes)
	_, err := conn.Write(buf)
	return err
}

// relay 带流量统计的双向数据转发
// c1=userConn, c2=workConn
//
//	user -> work = BytesOut (上行到后端)
//	work -> user = BytesIn  (下行到用户)
func (p *TCPProxy) relay(c1, c2 net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		n, err := io.Copy(c2, c1)
		p.BytesOut.Add(n)
		if err != nil {
			logger.Debug("[Proxy:%s] user->work copy 结束: %v", p.Name, err)
		}
	}()
	go func() {
		defer wg.Done()
		n, err := io.Copy(c1, c2)
		p.BytesIn.Add(n)
		if err != nil {
			logger.Debug("[Proxy:%s] work->user copy 结束: %v", p.Name, err)
		}
	}()
	wg.Wait()
	c1.Close()
	c2.Close()
}

// Relay 无统计的双向数据转发（供其它模块复用）
func Relay(c1, c2 net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	copyFn := func(dst, src net.Conn) {
		defer wg.Done()
		if _, err := io.Copy(dst, src); err != nil {
			logger.Debug("[Relay] copy 结束: %v", err)
		}
	}
	go copyFn(c1, c2)
	go copyFn(c2, c1)
	wg.Wait()
	c1.Close()
	c2.Close()
}
