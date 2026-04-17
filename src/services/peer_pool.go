package services

import (
	"net"
	"sync"

	"github.com/fastgox/utils/logger"
)

// peerPool 对端工作连接池（安全的发送/关闭语义）
//
// 相比直接使用 `chan net.Conn`，这里通过 mu+closed 原子化
// "检查 → 发送/取出" 过程，避免清理时的泄漏与发送已关闭 channel 引发 panic。
type peerPool struct {
	ch     chan net.Conn
	mu     sync.Mutex
	closed bool
}

func newPeerPool(size int) *peerPool {
	return &peerPool{ch: make(chan net.Conn, size)}
}

// TryPut 非阻塞放入连接，池已关闭或已满时返回 false（调用方应关闭该连接）
func (p *peerPool) TryPut(c net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	select {
	case p.ch <- c:
		return true
	default:
		return false
	}
}

// TryGet 非阻塞取出连接；无可用连接返回 nil,false
func (p *peerPool) TryGet() (net.Conn, bool) {
	select {
	case c := <-p.ch:
		return c, true
	default:
		return nil, false
	}
}

// Close 关闭池并同步回收所有残留连接，幂等
func (p *peerPool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()

	// 此后所有 TryPut 都会返回 false，可安全排空
	for {
		select {
		case c := <-p.ch:
			if c != nil {
				if err := c.Close(); err != nil {
					logger.Warn("[PeerPool] 关闭残留连接失败: %v", err)
				}
			}
		default:
			return
		}
	}
}
