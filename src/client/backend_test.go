package client

import (
	"net"
	"testing"

	modelConfig "github.com/hsqbyte/nlink/src/models/config"
)

func TestExpandProxyConfig_NoRange(t *testing.T) {
	cfg := &modelConfig.PeerConfig{Proxies: []modelConfig.ProxyConfig{
		{Name: "web", RemotePort: 9000, LocalPort: 8000},
	}}
	expandProxyConfig(cfg)
	if got := len(cfg.Proxies); got != 1 {
		t.Fatalf("expected 1 proxy, got %d", got)
	}
	if cfg.Proxies[0].Name != "web" {
		t.Fatalf("expected name web, got %s", cfg.Proxies[0].Name)
	}
}

func TestExpandProxyConfig_Range(t *testing.T) {
	cfg := &modelConfig.PeerConfig{Proxies: []modelConfig.ProxyConfig{
		{Name: "g", RemotePort: 9000, RemotePortEnd: 9003, LocalPort: 8000},
	}}
	expandProxyConfig(cfg)
	if got := len(cfg.Proxies); got != 4 {
		t.Fatalf("expected 4 proxies, got %d", got)
	}
	for i, p := range cfg.Proxies {
		wantName := ""
		switch i {
		case 0:
			wantName = "g-0"
		case 1:
			wantName = "g-1"
		case 2:
			wantName = "g-2"
		case 3:
			wantName = "g-3"
		}
		if p.Name != wantName {
			t.Errorf("[%d] expected name %s, got %s", i, wantName, p.Name)
		}
		if p.RemotePort != 9000+i {
			t.Errorf("[%d] expected RemotePort %d, got %d", i, 9000+i, p.RemotePort)
		}
		if p.LocalPort != 8000+i {
			t.Errorf("[%d] expected LocalPort %d, got %d", i, 8000+i, p.LocalPort)
		}
		if p.RemotePortEnd != 0 {
			t.Errorf("[%d] expected RemotePortEnd zeroed, got %d", i, p.RemotePortEnd)
		}
	}
}

func TestProxyProtocolV1(t *testing.T) {
	client := &net.TCPAddr{IP: net.ParseIP("192.168.1.2"), Port: 54321}
	server := &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 8000}
	hdr := proxyProtocolHeader("v1", client, server)
	if hdr == nil {
		t.Fatal("expected non-nil header")
	}
	got := string(hdr)
	want := "PROXY TCP4 192.168.1.2 10.0.0.5 54321 8000\r\n"
	if got != want {
		t.Errorf("v1 header mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestProxyProtocolV2_IPv4(t *testing.T) {
	client := &net.TCPAddr{IP: net.ParseIP("192.168.1.2"), Port: 54321}
	server := &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 8000}
	hdr := proxyProtocolHeader("v2", client, server)
	if len(hdr) != 12+2+2+(4+4+2+2) {
		t.Fatalf("unexpected v2 header length: %d", len(hdr))
	}
	if hdr[12] != 0x21 {
		t.Errorf("expected VER|CMD=0x21, got 0x%x", hdr[12])
	}
	if hdr[13] != 0x11 {
		t.Errorf("expected AF_INET|STREAM=0x11, got 0x%x", hdr[13])
	}
}

func TestProxyProtocolNone(t *testing.T) {
	a := &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 1}
	b := &net.TCPAddr{IP: net.ParseIP("5.6.7.8"), Port: 2}
	if proxyProtocolHeader("", a, b) != nil {
		t.Error("expected nil for empty mode")
	}
	if proxyProtocolHeader("unknown", a, b) != nil {
		t.Error("expected nil for unknown mode")
	}
}

func TestBackendPool_PickRoundRobin(t *testing.T) {
	p := &modelConfig.ProxyConfig{
		Name:          "rr",
		LocalBackends: []string{"a:1", "b:2", "c:3"},
		LBStrategy:    "roundrobin",
	}
	bp := NewBackendPool(p)
	defer bp.Close()
	seen := map[string]int{}
	for i := 0; i < 9; i++ {
		seen[bp.Pick().Addr]++
	}
	for _, a := range []string{"a:1", "b:2", "c:3"} {
		if seen[a] != 3 {
			t.Errorf("expected 3 hits on %s, got %d", a, seen[a])
		}
	}
}

func TestBackendPool_UnhealthyFallback(t *testing.T) {
	p := &modelConfig.ProxyConfig{
		Name:          "rr",
		LocalBackends: []string{"a:1", "b:2"},
	}
	bp := NewBackendPool(p)
	defer bp.Close()
	// Mark everything unhealthy; Pick should still return something (fallback).
	for _, b := range bp.backends {
		b.Healthy.Store(false)
	}
	if bp.Pick() == nil {
		t.Error("expected fallback backend when all unhealthy")
	}
}
