package services

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// TestActivationRoundTrip 确保 0x01 与 0x02 两种激活报文可正确互读
func TestActivationRoundTrip(t *testing.T) {
	// 使用 net.Pipe 做同步传输
	runCase := func(t *testing.T, proxyName, target string) {
		t.Helper()
		a, b := net.Pipe()
		defer a.Close()
		defer b.Close()

		errCh := make(chan error, 1)
		go func() {
			if target == "" {
				errCh <- sendActivation(a, proxyName)
			} else {
				errCh <- sendActivationWithTarget(a, proxyName, target)
			}
		}()

		b.SetReadDeadline(time.Now().Add(time.Second))

		gotName, gotTarget, err := readActivationTest(b)
		if err != nil {
			t.Fatalf("read error: %v", err)
		}
		if gotName != proxyName {
			t.Errorf("name mismatch: got=%s want=%s", gotName, proxyName)
		}
		if gotTarget != target {
			t.Errorf("target mismatch: got=%s want=%s", gotTarget, target)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("send error: %v", err)
		}
	}

	runCase(t, "web", "")
	runCase(t, "socks", "example.com:443")
}

// readActivationTest 复制自 client 包的 readActivation (以避免跨包依赖)
func readActivationTest(conn net.Conn) (string, string, error) {
	header := make([]byte, 3)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", "", err
	}
	flag := header[0]
	nameLen := binary.BigEndian.Uint16(header[1:3])
	nameBuf := make([]byte, nameLen)
	if _, err := io.ReadFull(conn, nameBuf); err != nil {
		return "", "", err
	}
	if flag == 0x01 {
		return string(nameBuf), "", nil
	}
	tl := make([]byte, 2)
	if _, err := io.ReadFull(conn, tl); err != nil {
		return "", "", err
	}
	tlen := binary.BigEndian.Uint16(tl)
	tBuf := make([]byte, tlen)
	if _, err := io.ReadFull(conn, tBuf); err != nil {
		return "", "", err
	}
	return string(nameBuf), string(tBuf), nil
}

// TestSOCKS5Handshake_IPv4 覆盖握手与 CONNECT 解析
func TestSOCKS5Handshake_IPv4(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	// 客户端脚本 (a 端)
	go func() {
		// VER=5 NMETHODS=1 METHOD=0 (no auth)
		a.Write([]byte{0x05, 0x01, 0x00})
		// 读取方法响应 (2 字节)
		resp := make([]byte, 2)
		io.ReadFull(a, resp)
		// VER=5 CMD=1(CONNECT) RSV=0 ATYP=1(IPv4) 1.2.3.4:80
		a.Write([]byte{0x05, 0x01, 0x00, 0x01, 1, 2, 3, 4, 0x00, 0x50})
		// 读取 CONNECT 响应 (10 字节)
		connResp := make([]byte, 10)
		io.ReadFull(a, connResp)
		a.Close()
	}()

	b.SetDeadline(time.Now().Add(time.Second))
	target, err := socks5Handshake(b)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if target != "1.2.3.4:80" {
		t.Errorf("target=%q want 1.2.3.4:80", target)
	}
}

// TestSOCKS5Handshake_Domain 域名 ATYP
func TestSOCKS5Handshake_Domain(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	go func() {
		a.Write([]byte{0x05, 0x01, 0x00})
		resp := make([]byte, 2)
		io.ReadFull(a, resp)
		dom := []byte("example.com")
		req := bytes.Buffer{}
		req.Write([]byte{0x05, 0x01, 0x00, 0x03, byte(len(dom))})
		req.Write(dom)
		req.Write([]byte{0x01, 0xBB}) // 443
		a.Write(req.Bytes())
		connResp := make([]byte, 10)
		io.ReadFull(a, connResp)
		a.Close()
	}()

	b.SetDeadline(time.Now().Add(time.Second))
	target, err := socks5Handshake(b)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if target != "example.com:443" {
		t.Errorf("target=%q want example.com:443", target)
	}
}
