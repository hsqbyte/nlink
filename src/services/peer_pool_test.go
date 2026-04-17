package services

import (
	"net"
	"sync"
	"testing"
	"time"
)

// fakeConn 实现 net.Conn 用于测试 Close 调用
type fakeConn struct {
	closed bool
	mu     sync.Mutex
}

func (f *fakeConn) Read(b []byte) (int, error)         { return 0, nil }
func (f *fakeConn) Write(b []byte) (int, error)        { return len(b), nil }
func (f *fakeConn) Close() error                       { f.mu.Lock(); f.closed = true; f.mu.Unlock(); return nil }
func (f *fakeConn) LocalAddr() net.Addr                { return &net.IPAddr{} }
func (f *fakeConn) RemoteAddr() net.Addr               { return &net.IPAddr{} }
func (f *fakeConn) SetDeadline(t time.Time) error      { return nil }
func (f *fakeConn) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakeConn) SetWriteDeadline(t time.Time) error { return nil }
func (f *fakeConn) isClosed() bool                     { f.mu.Lock(); defer f.mu.Unlock(); return f.closed }

func TestPeerPoolBasic(t *testing.T) {
	p := newPeerPool(3)
	c1, c2 := &fakeConn{}, &fakeConn{}
	if !p.TryPut(c1) {
		t.Fatal("TryPut c1 应成功")
	}
	if !p.TryPut(c2) {
		t.Fatal("TryPut c2 应成功")
	}
	got, ok := p.TryGet()
	if !ok || got != c1 {
		t.Fatalf("TryGet 应返回 c1，got=%v ok=%v", got, ok)
	}
}

func TestPeerPoolCloseDrainsAndReturnsFalse(t *testing.T) {
	p := newPeerPool(4)
	conns := []*fakeConn{{}, {}, {}}
	for _, c := range conns {
		if !p.TryPut(c) {
			t.Fatalf("TryPut 失败")
		}
	}
	p.Close()
	for _, c := range conns {
		if !c.isClosed() {
			t.Fatalf("Close 未关闭残留连接")
		}
	}
	// 关闭后 TryPut 返回 false
	late := &fakeConn{}
	if p.TryPut(late) {
		t.Fatal("Close 后 TryPut 应返回 false")
	}
	// 多次 Close 幂等
	p.Close()
}

// TestPeerPoolConcurrentPutClose 保证在并发 Put / Close 下不 panic、不泄漏
func TestPeerPoolConcurrentPutClose(t *testing.T) {
	p := newPeerPool(50)
	var wg sync.WaitGroup
	const N = 200
	leaked := make([]*fakeConn, 0, N)
	var leakMu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		// 并发 close (延迟一点让部分 put 先发生)
		time.Sleep(1 * time.Millisecond)
		p.Close()
	}()

	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			c := &fakeConn{}
			if !p.TryPut(c) {
				// 被拒 → 调用方理应关闭该连接；测试这种逻辑
				_ = c.Close()
				return
			}
			leakMu.Lock()
			leaked = append(leaked, c)
			leakMu.Unlock()
		}()
	}
	wg.Wait()

	// 所有 TryPut 成功的 conn，应由 Close() 全部关闭
	for _, c := range leaked {
		if !c.isClosed() {
			t.Fatal("已入池的连接应被 Close 回收")
		}
	}
}
