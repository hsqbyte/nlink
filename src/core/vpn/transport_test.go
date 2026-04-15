package vpn

import (
	"net"
	"testing"
	"time"
)

// TestUDPTransportRoundTrip 测试 UDP 传输层的加密/解密和双向通信
// 不需要 TUN 设备或 root 权限
func TestUDPTransportRoundTrip(t *testing.T) {
	token := "test-secret-token"
	localIP1 := net.ParseIP("10.0.0.1").To4()
	localIP2 := net.ParseIP("10.0.0.2").To4()

	// 创建两个 transport（模拟两个节点）
	t1, err := NewUDPTransport(0, token, localIP1) // 端口 0 = 自动分配
	if err != nil {
		t.Fatalf("创建 transport1 失败: %v", err)
	}
	defer t1.Close()

	t2, err := NewUDPTransport(0, token, localIP2)
	if err != nil {
		t.Fatalf("创建 transport2 失败: %v", err)
	}
	defer t2.Close()

	// 获取实际分配的端口
	addr1 := t1.conn.LocalAddr().(*net.UDPAddr)
	addr2 := t2.conn.LocalAddr().(*net.UDPAddr)
	t.Logf("Transport1 监听: %s", addr1.String())
	t.Logf("Transport2 监听: %s", addr2.String())

	// 互相注册对端
	t1.AddPeer(localIP2, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: addr2.Port})
	t2.AddPeer(localIP1, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: addr1.Port})

	// 构造一个模拟的 IPv4 包（最小 20 字节头 + 4 字节数据）
	// src=10.0.0.1, dst=10.0.0.2
	fakePacket := make([]byte, 24)
	fakePacket[0] = 0x45 // IPv4, header len=20
	fakePacket[2] = 0x00
	fakePacket[3] = 24 // total length
	copy(fakePacket[12:16], localIP1) // src IP
	copy(fakePacket[16:20], localIP2) // dst IP
	fakePacket[20] = 'T'
	fakePacket[21] = 'E'
	fakePacket[22] = 'S'
	fakePacket[23] = 'T'

	// t1 发送给 t2
	if err := t1.SendTo(fakePacket, localIP2); err != nil {
		t.Fatalf("t1 -> t2 发送失败: %v", err)
	}

	// t2 接收
	recvBuf := make([]byte, 1500)
	done := make(chan struct{})
	var recvN int
	var recvErr error

	go func() {
		recvN, _, recvErr = t2.RecvFrom(recvBuf)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("接收超时")
	}

	if recvErr != nil {
		t.Fatalf("t2 接收失败: %v", recvErr)
	}

	if recvN != 24 {
		t.Fatalf("接收长度不匹配: got %d, want 24", recvN)
	}

	// 验证数据完整性
	if string(recvBuf[20:24]) != "TEST" {
		t.Fatalf("数据不匹配: got %q, want %q", recvBuf[20:24], "TEST")
	}

	t.Logf("t1 -> t2 成功: %d bytes, payload=%q", recvN, recvBuf[20:24])

	// 反向: t2 -> t1
	fakePacket2 := make([]byte, 24)
	fakePacket2[0] = 0x45
	fakePacket2[3] = 24
	copy(fakePacket2[12:16], localIP2)
	copy(fakePacket2[16:20], localIP1)
	fakePacket2[20] = 'B'
	fakePacket2[21] = 'A'
	fakePacket2[22] = 'C'
	fakePacket2[23] = 'K'

	if err := t2.SendTo(fakePacket2, localIP1); err != nil {
		t.Fatalf("t2 -> t1 发送失败: %v", err)
	}

	done2 := make(chan struct{})
	go func() {
		recvN, _, recvErr = t1.RecvFrom(recvBuf)
		close(done2)
	}()

	select {
	case <-done2:
	case <-time.After(3 * time.Second):
		t.Fatal("反向接收超时")
	}

	if recvErr != nil {
		t.Fatalf("t1 接收失败: %v", recvErr)
	}

	if string(recvBuf[20:24]) != "BACK" {
		t.Fatalf("反向数据不匹配: got %q, want %q", recvBuf[20:24], "BACK")
	}

	t.Logf("t2 -> t1 成功: %d bytes, payload=%q", recvN, recvBuf[20:24])
}

// TestUDPTransportAutoDiscover 测试从未知对端接收时自动发现
func TestUDPTransportAutoDiscover(t *testing.T) {
	token := "test-secret-token"
	localIP1 := net.ParseIP("10.0.0.1").To4()
	localIP2 := net.ParseIP("10.0.0.2").To4()

	t1, err := NewUDPTransport(0, token, localIP1)
	if err != nil {
		t.Fatalf("创建 transport1 失败: %v", err)
	}
	defer t1.Close()

	t2, err := NewUDPTransport(0, token, localIP2)
	if err != nil {
		t.Fatalf("创建 transport2 失败: %v", err)
	}
	defer t2.Close()

	addr1 := t1.conn.LocalAddr().(*net.UDPAddr)
	addr2 := t2.conn.LocalAddr().(*net.UDPAddr)

	// 只有 t2 知道 t1 的地址，t1 不知道 t2
	t2.AddPeer(localIP1, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: addr1.Port})

	// t2 发送给 t1
	fakePacket := make([]byte, 24)
	fakePacket[0] = 0x45
	fakePacket[3] = 24
	copy(fakePacket[12:16], localIP2) // src = t2
	copy(fakePacket[16:20], localIP1) // dst = t1
	copy(fakePacket[20:24], []byte("DISC"))

	if err := t2.SendTo(fakePacket, localIP1); err != nil {
		t.Fatalf("t2 -> t1 发送失败: %v", err)
	}

	// t1 接收 — 应该自动发现 t2
	recvBuf := make([]byte, 1500)
	done := make(chan struct{})
	go func() {
		_, _, _ = t1.RecvFrom(recvBuf)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("接收超时")
	}

	// 验证 t1 现在知道 t2
	peer, ok := t1.GetPeer(localIP2)
	if !ok {
		t.Fatal("自动发现失败: t1 不知道 10.0.0.2")
	}

	t.Logf("自动发现成功: 10.0.0.2 -> %s", peer.Endpoint.String())

	// 现在 t1 应该能回发给 t2
	replyPacket := make([]byte, 24)
	replyPacket[0] = 0x45
	replyPacket[3] = 24
	copy(replyPacket[12:16], localIP1)
	copy(replyPacket[16:20], localIP2)
	copy(replyPacket[20:24], []byte("RPLY"))

	if err := t1.SendTo(replyPacket, localIP2); err != nil {
		t.Fatalf("t1 -> t2 回复失败: %v", err)
	}

	done2 := make(chan struct{})
	go func() {
		_, _, _ = t2.RecvFrom(recvBuf)
		close(done2)
	}()

	select {
	case <-done2:
	case <-time.After(3 * time.Second):
		t.Fatal("回复接收超时")
	}

	// 确认 t2 的端口被正确使用
	_ = addr2
	if string(recvBuf[20:24]) != "RPLY" {
		t.Fatalf("回复数据不匹配: got %q", recvBuf[20:24])
	}

	t.Log("自动发现 + 回复测试通过")
}
