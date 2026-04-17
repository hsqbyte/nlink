package services

import (
	"io"
	"net"
	"time"

	"golang.org/x/time/rate"
)

// limitedConn 用 token bucket 给 Read/Write 限速
//
// 每个连接独立 limiter；代理级限速 = 每连接限速（避免所有连接共用一个 limiter 造成 head-of-line blocking）
type limitedConn struct {
	net.Conn
	limiter *rate.Limiter
}

// wrapConnWithRateLimit 若 bytesPerSec > 0 则包裹限速；否则返回原 conn
func wrapConnWithRateLimit(c net.Conn, bytesPerSec int64) net.Conn {
	if bytesPerSec <= 0 {
		return c
	}
	// burst 给 1 秒量，避免速率过低时每次都阻塞
	burst := bytesPerSec
	if burst < 1024 {
		burst = 1024
	}
	return &limitedConn{
		Conn:    c,
		limiter: rate.NewLimiter(rate.Limit(bytesPerSec), int(burst)),
	}
}

func (l *limitedConn) Read(p []byte) (int, error) {
	n, err := l.Conn.Read(p)
	if n > 0 {
		if werr := l.wait(n); werr != nil && err == nil {
			err = werr
		}
	}
	return n, err
}

func (l *limitedConn) Write(p []byte) (int, error) {
	if err := l.wait(len(p)); err != nil {
		return 0, err
	}
	return l.Conn.Write(p)
}

// wait 为 n 字节申请令牌；如果 n > burst，分片等待
func (l *limitedConn) wait(n int) error {
	burst := l.limiter.Burst()
	for n > 0 {
		chunk := n
		if chunk > burst {
			chunk = burst
		}
		r := l.limiter.ReserveN(time.Now(), chunk)
		if !r.OK() {
			return io.ErrShortWrite
		}
		delay := r.Delay()
		if delay > 0 {
			time.Sleep(delay)
		}
		n -= chunk
	}
	return nil
}
