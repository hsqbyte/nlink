// Package client — backend pool for a single proxy: LB + health-check + PROXY protocol.
package client

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	modelConfig "github.com/hsqbyte/nlink/src/models/config"
)

// Backend represents a single upstream address and its health state.
type Backend struct {
	Addr        string
	Healthy     atomic.Bool
	ActiveConns atomic.Int64
	LastErr     atomic.Value

	consecOK   int
	consecFail int
}

// BackendPool holds the backends for one proxy and implements LB + HC.
type BackendPool struct {
	name     string
	backends []*Backend
	strategy string // roundrobin | random | leastconn
	rr       atomic.Uint64
	hc       *modelConfig.HealthCheckConfig
	stopOnce sync.Once
	stop     chan struct{}
}

// NewBackendPool builds a pool from a ProxyConfig.
// If LocalBackends is empty, falls back to LocalIP:LocalPort.
func NewBackendPool(p *modelConfig.ProxyConfig) *BackendPool {
	bp := &BackendPool{
		name:     p.Name,
		strategy: normalizeStrategy(p.LBStrategy),
		hc:       p.HealthCheck,
		stop:     make(chan struct{}),
	}
	addrs := p.LocalBackends
	if len(addrs) == 0 {
		addrs = []string{net.JoinHostPort(p.LocalIP, fmt.Sprintf("%d", p.LocalPort))}
	}
	for _, a := range addrs {
		b := &Backend{Addr: a}
		b.Healthy.Store(true)
		bp.backends = append(bp.backends, b)
	}
	if bp.hc != nil && bp.hc.Enabled {
		go bp.runHealthCheck()
	}
	return bp
}

func normalizeStrategy(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "random", "leastconn":
		return s
	default:
		return "roundrobin"
	}
}

// Pick chooses a healthy backend; falls back to any backend if all are unhealthy.
func (bp *BackendPool) Pick() *Backend {
	if bp == nil || len(bp.backends) == 0 {
		return nil
	}
	healthy := make([]*Backend, 0, len(bp.backends))
	for _, b := range bp.backends {
		if b.Healthy.Load() {
			healthy = append(healthy, b)
		}
	}
	if len(healthy) == 0 {
		healthy = bp.backends
	}
	switch bp.strategy {
	case "random":
		return healthy[rand.Intn(len(healthy))]
	case "leastconn":
		pick := healthy[0]
		min := pick.ActiveConns.Load()
		for _, b := range healthy[1:] {
			if n := b.ActiveConns.Load(); n < min {
				min = n
				pick = b
			}
		}
		return pick
	default:
		i := bp.rr.Add(1) - 1
		return healthy[int(i)%len(healthy)]
	}
}

// DialBackend picks a backend and dials it. The caller must decrement
// backend.ActiveConns when done.
func (bp *BackendPool) DialBackend(timeout time.Duration) (net.Conn, *Backend, error) {
	b := bp.Pick()
	if b == nil {
		return nil, nil, fmt.Errorf("no backend available for proxy %s", bp.name)
	}
	c, err := net.DialTimeout("tcp", b.Addr, timeout)
	if err != nil {
		b.LastErr.Store(err.Error())
		b.Healthy.Store(false)
		return nil, b, err
	}
	b.ActiveConns.Add(1)
	return c, b, nil
}

// Close stops the health-check goroutine.
func (bp *BackendPool) Close() {
	if bp == nil {
		return
	}
	bp.stopOnce.Do(func() { close(bp.stop) })
}

func (bp *BackendPool) runHealthCheck() {
	interval := time.Duration(bp.hc.IntervalMs) * time.Millisecond
	if interval < 500*time.Millisecond {
		interval = 5 * time.Second
	}
	timeout := time.Duration(bp.hc.TimeoutMs) * time.Millisecond
	if timeout < 100*time.Millisecond {
		timeout = 2 * time.Second
	}
	rise := bp.hc.Rise
	if rise < 1 {
		rise = 2
	}
	fall := bp.hc.Fall
	if fall < 1 {
		fall = 3
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-bp.stop:
			return
		case <-t.C:
			for _, b := range bp.backends {
				ok := probeBackend(bp.hc.Type, b.Addr, bp.hc.Path, timeout)
				if ok {
					b.consecFail = 0
					b.consecOK++
					if b.consecOK >= rise {
						b.Healthy.Store(true)
					}
				} else {
					b.consecOK = 0
					b.consecFail++
					if b.consecFail >= fall {
						b.Healthy.Store(false)
					}
				}
			}
		}
	}
}

func probeBackend(kind, addr, path string, timeout time.Duration) bool {
	if strings.EqualFold(kind, "http") {
		client := &http.Client{Timeout: timeout}
		if path == "" {
			path = "/health"
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		url := fmt.Sprintf("http://%s%s", addr, path)
		resp, err := client.Get(url)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode >= 200 && resp.StatusCode < 400
	}
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// ---- PROXY protocol helpers ----

// proxyProtocolHeader returns a v1/v2 header or nil if mode is empty.
func proxyProtocolHeader(mode string, client, server net.Addr) []byte {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "v1":
		return proxyV1(client, server)
	case "v2":
		return proxyV2(client, server)
	}
	return nil
}

func proxyV1(client, server net.Addr) []byte {
	ch, cp, err1 := splitHostPort(client)
	sh, sp, err2 := splitHostPort(server)
	if err1 != nil || err2 != nil {
		return []byte("PROXY UNKNOWN\r\n")
	}
	family := "TCP4"
	if strings.Contains(ch, ":") || strings.Contains(sh, ":") {
		family = "TCP6"
	}
	return []byte(fmt.Sprintf("PROXY %s %s %s %d %d\r\n", family, ch, sh, cp, sp))
}

func proxyV2(client, server net.Addr) []byte {
	ch, cp, err1 := splitHostPort(client)
	sh, sp, err2 := splitHostPort(server)
	if err1 != nil || err2 != nil {
		return nil
	}
	cIP := net.ParseIP(ch)
	sIP := net.ParseIP(sh)
	if cIP == nil || sIP == nil {
		return nil
	}
	isV6 := cIP.To4() == nil || sIP.To4() == nil
	sig := []byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A}
	verCmd := byte(0x21)
	famProto := byte(0x11) // TCP over IPv4
	var addrBuf []byte
	if isV6 {
		famProto = 0x21 // TCP over IPv6
		addrBuf = make([]byte, 0, 16+16+2+2)
		addrBuf = append(addrBuf, cIP.To16()...)
		addrBuf = append(addrBuf, sIP.To16()...)
	} else {
		addrBuf = make([]byte, 0, 4+4+2+2)
		addrBuf = append(addrBuf, cIP.To4()...)
		addrBuf = append(addrBuf, sIP.To4()...)
	}
	var portBuf [4]byte
	binary.BigEndian.PutUint16(portBuf[0:2], uint16(cp))
	binary.BigEndian.PutUint16(portBuf[2:4], uint16(sp))
	addrBuf = append(addrBuf, portBuf[:]...)

	hdr := make([]byte, 0, len(sig)+4+len(addrBuf))
	hdr = append(hdr, sig...)
	hdr = append(hdr, verCmd, famProto)
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(addrBuf)))
	hdr = append(hdr, lenBuf[:]...)
	hdr = append(hdr, addrBuf...)
	return hdr
}

func splitHostPort(a net.Addr) (string, int, error) {
	h, p, err := net.SplitHostPort(a.String())
	if err != nil {
		return "", 0, err
	}
	port := 0
	fmt.Sscanf(p, "%d", &port)
	return h, port, nil
}
