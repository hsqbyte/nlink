package services

// ---- 对端名称/延迟/断开记录 ----

// RegisterPeerName 注册对端名称映射
func (ts *TunnelService) RegisterPeerName(connID, name string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.peerNames[connID] = name
	ts.nameToConnID[name] = connID
	// 清除断开记录（对端重连了）
	delete(ts.disconnectedPeers, name)
}

// IsPeerNameTaken 检查对端名称是否已被占用
func (ts *TunnelService) IsPeerNameTaken(name string) bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	_, ok := ts.nameToConnID[name]
	return ok
}

// GetConnIDByName 根据名称查找连接ID
func (ts *TunnelService) GetConnIDByName(name string) (string, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	id, ok := ts.nameToConnID[name]
	return id, ok
}

// GetPeerName 根据连接ID查找名称
func (ts *TunnelService) GetPeerName(connID string) string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.peerNames[connID]
}

// PeerProxyCount 返回指定对端已注册的代理数
func (ts *TunnelService) PeerProxyCount(connID string) int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return len(ts.peerProxies[connID])
}

// ListPeers 列出所有对端（含在线和已断开）
func (ts *TunnelService) ListPeers() []PeerInfo {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	result := make([]PeerInfo, 0, len(ts.peerProxies)+len(ts.disconnectedPeers))
	for connID, names := range ts.peerProxies {
		result = append(result, PeerInfo{
			ConnID:    connID,
			Name:      ts.peerNames[connID],
			Proxies:   names,
			Connected: true,
			Latency:   ts.peerLatencies[connID],
		})
	}
	for _, dp := range ts.disconnectedPeers {
		result = append(result, PeerInfo{
			Name:      dp.Name,
			Proxies:   dp.Proxies,
			Connected: false,
		})
	}
	return result
}

// UpdatePeerLatency 更新下游对端延迟
func (ts *TunnelService) UpdatePeerLatency(connID string, latencyMs int64) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.peerLatencies[connID] = latencyMs
}

// PeerInfo 对端信息
type PeerInfo struct {
	ConnID    string   `json:"conn_id"`
	Name      string   `json:"name"`
	Proxies   []string `json:"proxies"`
	Connected bool     `json:"connected"`
	Latency   int64    `json:"latency"` // 延迟(ms)，0=未检测
}

// DisconnectedPeer 已断开的对端记录
type DisconnectedPeer struct {
	Name    string
	Proxies []string
}
