package services

import (
	"fmt"
	"net"

	"github.com/fastgox/utils/logger"
	"github.com/hsqbyte/nlink/src/core/tcp"
)

// ---- 代理管理 ----

// RegisterProxy 注册代理
func (ts *TunnelService) RegisterProxy(connID string, name string, remotePort int, opts *ProxyOptions) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if _, exists := ts.proxies[name]; exists {
		return fmt.Errorf("代理已存在: %s", name)
	}
	if _, exists := ts.udpProxies[name]; exists {
		return fmt.Errorf("代理已存在: %s", name)
	}

	proxy, err := NewTCPProxy(name, remotePort, connID, ts, opts)
	if err != nil {
		return err
	}

	ts.proxies[name] = proxy
	ts.peerProxies[connID] = append(ts.peerProxies[connID], name)

	// 创建对端全局连接池（如果不存在）
	if _, ok := ts.peerPools[connID]; !ok {
		ts.peerPools[connID] = newPeerPool(50)
	}

	go proxy.Run()
	peerName := ts.peerNames[connID]
	logger.Info("[Tunnel] 代理注册成功: name=%s port=%d peer=%s(%s)", name, remotePort, peerName, connID)
	return nil
}

// RegisterUDPProxy 注册 UDP 代理
func (ts *TunnelService) RegisterUDPProxy(connID string, name string, remotePort int, opts *ProxyOptions) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if _, exists := ts.proxies[name]; exists {
		return fmt.Errorf("代理已存在: %s", name)
	}
	if _, exists := ts.udpProxies[name]; exists {
		return fmt.Errorf("代理已存在: %s", name)
	}

	proxy, err := NewUDPProxy(name, remotePort, connID, ts, opts)
	if err != nil {
		return err
	}

	ts.udpProxies[name] = proxy
	ts.peerProxies[connID] = append(ts.peerProxies[connID], name)

	go proxy.Run()
	peerName := ts.peerNames[connID]
	logger.Info("[Tunnel] UDP 代理注册成功: name=%s port=%d peer=%s(%s)", name, remotePort, peerName, connID)
	return nil
}

// RegisterHTTPProxy 注册 HTTP 虚拟主机代理（type=http）
func (ts *TunnelService) RegisterHTTPProxy(connID string, name string, customDomains []string, hostRewrite string, opts *ProxyOptions) error {
	vhost := GetHTTPVhost()
	if vhost == nil {
		return fmt.Errorf("未启用 vhost_http_port，无法注册 type=http 代理")
	}
	ts.mu.Lock()
	// 名称全局唯一
	if _, exists := ts.proxies[name]; exists {
		ts.mu.Unlock()
		return fmt.Errorf("代理已存在: %s", name)
	}
	if _, exists := ts.udpProxies[name]; exists {
		ts.mu.Unlock()
		return fmt.Errorf("代理已存在: %s", name)
	}
	ts.mu.Unlock()

	hp := &HTTPProxy{
		Name:          name,
		CustomDomains: customDomains,
		HostRewrite:   hostRewrite,
		PeerConnID:    connID,
		WorkConnCh:    make(chan net.Conn, 20),
		tunnel:        ts,
	}
	if opts != nil {
		if len(opts.AllowCIDR) > 0 || len(opts.DenyCIDR) > 0 {
			acl, err := ParseACL(opts.AllowCIDR, opts.DenyCIDR)
			if err != nil {
				return err
			}
			hp.acl = acl
		}
		hp.rateLimitBps = opts.RateLimit
	}
	if err := vhost.Register(hp); err != nil {
		return err
	}

	ts.mu.Lock()
	ts.peerProxies[connID] = append(ts.peerProxies[connID], name)
	if _, ok := ts.peerPools[connID]; !ok {
		ts.peerPools[connID] = newPeerPool(50)
	}
	ts.mu.Unlock()

	peerName := ts.peerNames[connID]
	logger.Info("[Tunnel] HTTP 代理注册成功: name=%s domains=%v peer=%s(%s)", name, customDomains, peerName, connID)
	return nil
}

// RequestWorkConn 通过TCP控制通道向对端请求工作连接
func (ts *TunnelService) RequestWorkConn(proxyName string, peerConnID string) {
	if err := ts.tcpServer.ConnManager().SendTo(peerConnID, ts.tcpServer.Codec(), &tcp.Response{
		Cmd:     "start_work_conn",
		Code:    200,
		Message: "success",
		Data:    tcp.StartWorkConnData{ProxyName: proxyName},
	}); err != nil {
		logger.Warn("[Tunnel] 请求工作连接失败: proxy=%s peer=%s err=%v", proxyName, peerConnID, err)
	}
}

// DeliverWorkConn 将工作连接投递给对应代理（按需连接）
func (ts *TunnelService) DeliverWorkConn(proxyName string, conn net.Conn) bool {
	ts.mu.RLock()
	proxy, ok := ts.proxies[proxyName]
	udpProxy, uok := ts.udpProxies[proxyName]
	ts.mu.RUnlock()
	if ok {
		select {
		case proxy.WorkConnCh <- conn:
			return true
		default:
			return false
		}
	}
	if uok {
		select {
		case udpProxy.WorkConnCh <- conn:
			return true
		default:
			return false
		}
	}
	// 尝试投递给 HTTP vhost
	if vhost := GetHTTPVhost(); vhost != nil {
		if vhost.Deliver(proxyName, conn) {
			return true
		}
	}
	return false
}

// DeliverPoolConn 将池连接投递到对端全局连接池
func (ts *TunnelService) DeliverPoolConn(connID string, conn net.Conn) bool {
	ts.mu.RLock()
	pool, ok := ts.peerPools[connID]
	ts.mu.RUnlock()
	if !ok {
		return false
	}
	return pool.TryPut(conn)
}

// getPeerPool 获取对端全局连接池（同包内部使用）
func (ts *TunnelService) getPeerPool(connID string) *peerPool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.peerPools[connID]
}

// closePeerPool 从 map 中移除并同步关闭连接池（必须在持有 ts.mu.Lock() 时调用）
func (ts *TunnelService) closePeerPoolLocked(connID string) {
	if pool, ok := ts.peerPools[connID]; ok {
		delete(ts.peerPools, connID)
		pool.Close()
	}
}

// RemovePeerProxies 移除对端所有代理（断连清理）
func (ts *TunnelService) RemovePeerProxies(connID string) {
	ts.mu.Lock()
	names, ok := ts.peerProxies[connID]
	if ok {
		// 记录断开的对端（供 Dashboard 展示）
		if peerName, hasName := ts.peerNames[connID]; hasName {
			ts.disconnectedPeers[peerName] = &DisconnectedPeer{
				Name:    peerName,
				Proxies: names,
			}
		}

		for _, name := range names {
			if proxy, exists := ts.proxies[name]; exists {
				proxy.Close()
				delete(ts.proxies, name)
				logger.Info("[Tunnel] 代理已移除: %s", name)
			}
			if uproxy, exists := ts.udpProxies[name]; exists {
				uproxy.Close()
				delete(ts.udpProxies, name)
				logger.Info("[Tunnel] UDP 代理已移除: %s", name)
			}
			if vhost := GetHTTPVhost(); vhost != nil {
				vhost.Unregister(name)
			}
		}
		delete(ts.peerProxies, connID)
	}

	// 清理名称映射
	if name, ok := ts.peerNames[connID]; ok {
		delete(ts.nameToConnID, name)
		delete(ts.peerNames, connID)
	}

	// 清理全局连接池
	ts.closePeerPoolLocked(connID)

	// 清理延迟记录
	delete(ts.peerLatencies, connID)
	ts.mu.Unlock()

	// 主动唤醒所有发往该 connID 的未完成请求，避免等待 10s 超时
	ts.failPendingForConn(connID, "对端已断开")
}

// ListProxies 列出所有代理（含统计）
func (ts *TunnelService) ListProxies() []ProxyInfo {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	result := make([]ProxyInfo, 0, len(ts.proxies)+len(ts.udpProxies))
	for _, p := range ts.proxies {
		result = append(result, ProxyInfo{
			Name:         p.Name,
			Type:         "tcp",
			RemotePort:   p.RemotePort,
			PeerConnID:   p.PeerConnID,
			PeerName:     ts.peerNames[p.PeerConnID],
			TotalConns:   p.TotalConns.Load(),
			ActiveConns:  p.ActiveConns.Load(),
			BytesIn:      p.BytesIn.Load(),
			BytesOut:     p.BytesOut.Load(),
			PoolHits:     p.PoolHits.Load(),
			OnDemandHits: p.OnDemandHits.Load(),
			RejectedACL:  p.RejectedACL.Load(),
		})
	}
	for _, p := range ts.udpProxies {
		result = append(result, ProxyInfo{
			Name:        p.Name,
			Type:        "udp",
			RemotePort:  p.RemotePort,
			PeerConnID:  p.PeerConnID,
			PeerName:    ts.peerNames[p.PeerConnID],
			TotalConns:  p.TotalSessions.Load(),
			ActiveConns: p.ActiveSess.Load(),
			BytesIn:     p.BytesIn.Load(),
			BytesOut:    p.BytesOut.Load(),
			RejectedACL: p.RejectedACL.Load(),
		})
	}
	if vhost := GetHTTPVhost(); vhost != nil {
		for _, p := range vhost.List() {
			result = append(result, ProxyInfo{
				Name:        p.Name,
				Type:        "http",
				RemotePort:  vhost.port,
				PeerConnID:  p.PeerConnID,
				PeerName:    ts.peerNames[p.PeerConnID],
				TotalConns:  p.TotalConns.Load(),
				ActiveConns: p.ActiveConns.Load(),
				BytesIn:     p.BytesIn.Load(),
				BytesOut:    p.BytesOut.Load(),
				RejectedACL: p.RejectedACL.Load(),
			})
		}
	}
	return result
}

// ServerStats 返回节点全局统计
func (ts *TunnelService) ServerStats() ServerStatsInfo {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	var totalConns, activeConns, bytesIn, bytesOut int64
	for _, p := range ts.proxies {
		totalConns += p.TotalConns.Load()
		activeConns += p.ActiveConns.Load()
		bytesIn += p.BytesIn.Load()
		bytesOut += p.BytesOut.Load()
	}
	for _, p := range ts.udpProxies {
		totalConns += p.TotalSessions.Load()
		activeConns += p.ActiveSess.Load()
		bytesIn += p.BytesIn.Load()
		bytesOut += p.BytesOut.Load()
	}

	return ServerStatsInfo{
		Uptime:      int64(ts.uptime().Seconds()),
		PeerCount:   len(ts.peerProxies),
		ProxyCount:  len(ts.proxies) + len(ts.udpProxies),
		TotalConns:  totalConns,
		ActiveConns: activeConns,
		BytesIn:     bytesIn,
		BytesOut:    bytesOut,
	}
}

// CloseAll 关闭所有代理
func (ts *TunnelService) CloseAll() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for name, proxy := range ts.proxies {
		proxy.Close()
		delete(ts.proxies, name)
	}
	for name, proxy := range ts.udpProxies {
		proxy.Close()
		delete(ts.udpProxies, name)
	}
	// 清理所有连接池
	for connID, pool := range ts.peerPools {
		delete(ts.peerPools, connID)
		pool.Close()
	}
	ts.peerProxies = make(map[string][]string)
	ts.peerNames = make(map[string]string)
	ts.nameToConnID = make(map[string]string)
}

// RemoveProxy 移除指定代理
func (ts *TunnelService) RemoveProxy(name string) bool {
	ts.mu.Lock()
	removed := false
	if proxy, ok := ts.proxies[name]; ok {
		proxy.Close()
		delete(ts.proxies, name)
		removed = true
	} else if uproxy, ok := ts.udpProxies[name]; ok {
		uproxy.Close()
		delete(ts.udpProxies, name)
		removed = true
	}
	if removed {
		for connID, names := range ts.peerProxies {
			for i, n := range names {
				if n == name {
					ts.peerProxies[connID] = append(names[:i], names[i+1:]...)
					break
				}
			}
		}
	}
	ts.mu.Unlock()
	if !removed {
		if vhost := GetHTTPVhost(); vhost != nil {
			if vhost.Unregister(name) {
				ts.mu.Lock()
				for connID, names := range ts.peerProxies {
					for i, n := range names {
						if n == name {
							ts.peerProxies[connID] = append(names[:i], names[i+1:]...)
							break
						}
					}
				}
				ts.mu.Unlock()
				removed = true
			}
		}
	}
	if removed {
		logger.Info("[Tunnel] 代理已手动移除: %s", name)
	}
	return removed
}

// KickPeer 踢出指定对端
func (ts *TunnelService) KickPeer(connID string) bool {
	ts.mu.RLock()
	_, ok := ts.peerProxies[connID]
	ts.mu.RUnlock()
	if !ok {
		return false
	}
	ts.RemovePeerProxies(connID)
	ts.tcpServer.ConnManager().Kick(connID)
	logger.Info("[Tunnel] 对端已踢出: %s", connID)
	return true
}

// ProxyInfo 代理信息（含统计）
type ProxyInfo struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	RemotePort   int    `json:"remote_port"`
	PeerConnID   string `json:"peer_conn_id"`
	PeerName     string `json:"peer_name"`
	TotalConns   int64  `json:"total_conns"`
	ActiveConns  int64  `json:"active_conns"`
	BytesIn      int64  `json:"bytes_in"`
	BytesOut     int64  `json:"bytes_out"`
	PoolHits     int64  `json:"pool_hits"`
	OnDemandHits int64  `json:"on_demand_hits"`
	RejectedACL  int64  `json:"rejected_acl"`
}

// ServerStatsInfo 节点全局统计
type ServerStatsInfo struct {
	Uptime      int64 `json:"uptime"`
	PeerCount   int   `json:"peer_count"`
	ProxyCount  int   `json:"proxy_count"`
	TotalConns  int64 `json:"total_conns"`
	ActiveConns int64 `json:"active_conns"`
	BytesIn     int64 `json:"bytes_in"`
	BytesOut    int64 `json:"bytes_out"`
}
