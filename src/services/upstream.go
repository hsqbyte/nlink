package services

import (
	"fmt"
)

// ---- 上游连接管理 ----

// UpstreamPeer 本节点主动连接的上游对端信息
type UpstreamPeer struct {
	Addr      string   `json:"addr"`
	Port      int      `json:"port"`
	Name      string   `json:"name"`
	Connected bool     `json:"connected"`
	Proxies   []string `json:"proxies"`
	Latency   int64    `json:"latency"` // 延迟(ms)
}

// RegisterUpstreamPeer 注册上游连接
func (ts *TunnelService) RegisterUpstreamPeer(addr string, port int, name string, proxies []string) {
	ts.upstreamMu.Lock()
	defer ts.upstreamMu.Unlock()
	key := fmt.Sprintf("%s:%d", addr, port)
	ts.upstreamPeers[key] = &UpstreamPeer{
		Addr: addr, Port: port, Name: name, Connected: true, Proxies: proxies,
	}
}

// UpdateUpstreamPeerStatus 更新上游连接状态
func (ts *TunnelService) UpdateUpstreamPeerStatus(addr string, port int, connected bool) {
	ts.upstreamMu.Lock()
	defer ts.upstreamMu.Unlock()
	key := fmt.Sprintf("%s:%d", addr, port)
	if p, ok := ts.upstreamPeers[key]; ok {
		p.Connected = connected
	}
}

// UpdateUpstreamPeerLatency 更新上游连接延迟
func (ts *TunnelService) UpdateUpstreamPeerLatency(addr string, port int, latencyMs int64) {
	ts.upstreamMu.Lock()
	defer ts.upstreamMu.Unlock()
	key := fmt.Sprintf("%s:%d", addr, port)
	if p, ok := ts.upstreamPeers[key]; ok {
		p.Latency = latencyMs
	}
}

// ListUpstreamPeers 列出所有上游连接
func (ts *TunnelService) ListUpstreamPeers() []UpstreamPeer {
	ts.upstreamMu.RLock()
	defer ts.upstreamMu.RUnlock()
	result := make([]UpstreamPeer, 0, len(ts.upstreamPeers))
	for _, p := range ts.upstreamPeers {
		result = append(result, *p)
	}
	return result
}
