package services

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fastgox/utils/logger"
	"github.com/hsqbyte/nlink/src/core/tcp"
)

// TunnelService 隧道服务，管理所有代理和工作连接
type TunnelService struct {
	mu      sync.RWMutex
	proxies map[string]*TCPProxy

	// 对端连接映射: connID -> 代理名列表
	peerProxies map[string][]string

	// 对端名称映射
	peerNames    map[string]string // connID -> peerName
	nameToConnID map[string]string // peerName -> connID

	// 对端全局连接池: connID -> pool channel
	peerPools map[string]chan net.Conn

	// 已断开的对端（保留供 Dashboard 显示）
	disconnectedPeers map[string]*DisconnectedPeer // key: peerName

	// 对端延迟 (ms)
	peerLatencies map[string]int64 // connID -> latency ms

	// 上游连接（本节点主动连接的对端）
	upstreamMu    sync.RWMutex
	upstreamPeers map[string]*UpstreamPeer // key: addr:port

	tcpServer       *tcp.Server
	workConnTimeout time.Duration
	startTime       time.Time

	// 远程指令: 等待对端回复
	pendingMu       sync.Mutex
	pendingRequests map[string]chan *tcp.Response // key: connID:seq
	seqCounter      atomic.Uint64
}

var tunnelSvc *TunnelService

// InitTunnelService 初始化隧道服务
func InitTunnelService(tcpServer *tcp.Server, workConnTimeout int) {
	tunnelSvc = &TunnelService{
		proxies:           make(map[string]*TCPProxy),
		peerProxies:       make(map[string][]string),
		peerNames:         make(map[string]string),
		nameToConnID:      make(map[string]string),
		peerPools:         make(map[string]chan net.Conn),
		disconnectedPeers: make(map[string]*DisconnectedPeer),
		peerLatencies:     make(map[string]int64),
		upstreamPeers:     make(map[string]*UpstreamPeer),
		tcpServer:         tcpServer,
		workConnTimeout:   time.Duration(workConnTimeout) * time.Second,
		startTime:         time.Now(),
		pendingRequests:   make(map[string]chan *tcp.Response),
	}
	tunnelSvc.StartLatencyProbe()
}

// GetTunnelService 获取隧道服务单例
func GetTunnelService() *TunnelService {
	return tunnelSvc
}

// EnsureTunnelService 确保隧道服务已初始化（Dashboard-only 场景）
func EnsureTunnelService() {
	if tunnelSvc != nil {
		return
	}
	tunnelSvc = &TunnelService{
		proxies:           make(map[string]*TCPProxy),
		peerProxies:       make(map[string][]string),
		peerNames:         make(map[string]string),
		nameToConnID:      make(map[string]string),
		peerPools:         make(map[string]chan net.Conn),
		disconnectedPeers: make(map[string]*DisconnectedPeer),
		peerLatencies:     make(map[string]int64),
		upstreamPeers:     make(map[string]*UpstreamPeer),
		startTime:         time.Now(),
		pendingRequests:   make(map[string]chan *tcp.Response),
	}
}

// ---- 对端名称管理 ----

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

// ---- 代理管理 ----

// RegisterProxy 注册代理
func (ts *TunnelService) RegisterProxy(connID string, name string, remotePort int) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if _, exists := ts.proxies[name]; exists {
		return fmt.Errorf("代理已存在: %s", name)
	}

	proxy, err := NewTCPProxy(name, remotePort, connID, ts)
	if err != nil {
		return err
	}

	ts.proxies[name] = proxy
	ts.peerProxies[connID] = append(ts.peerProxies[connID], name)

	// 创建对端全局连接池（如果不存在）
	if _, ok := ts.peerPools[connID]; !ok {
		ts.peerPools[connID] = make(chan net.Conn, 50)
	}

	go proxy.Run()
	peerName := ts.peerNames[connID]
	logger.Info("[Tunnel] 代理注册成功: name=%s port=%d peer=%s(%s)", name, remotePort, peerName, connID)
	return nil
}

// RequestWorkConn 通过TCP控制通道向对端请求工作连接
func (ts *TunnelService) RequestWorkConn(proxyName string, peerConnID string) {
	ts.tcpServer.ConnManager().SendTo(peerConnID, ts.tcpServer.Codec(), &tcp.Response{
		Cmd:     "start_work_conn",
		Code:    200,
		Message: "success",
		Data:    tcp.StartWorkConnData{ProxyName: proxyName},
	})
}

// DeliverWorkConn 将工作连接投递给对应代理（按需连接）
func (ts *TunnelService) DeliverWorkConn(proxyName string, conn net.Conn) bool {
	ts.mu.RLock()
	proxy, ok := ts.proxies[proxyName]
	ts.mu.RUnlock()
	if !ok {
		return false
	}
	select {
	case proxy.WorkConnCh <- conn:
		return true
	default:
		return false
	}
}

// DeliverPoolConn 将池连接投递到对端全局连接池
func (ts *TunnelService) DeliverPoolConn(connID string, conn net.Conn) bool {
	ts.mu.RLock()
	pool, ok := ts.peerPools[connID]
	ts.mu.RUnlock()
	if !ok {
		return false
	}
	select {
	case pool <- conn:
		return true
	default:
		return false
	}
}

// GetPeerPool 获取对端全局连接池
func (ts *TunnelService) GetPeerPool(connID string) chan net.Conn {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.peerPools[connID]
}

// RemovePeerProxies 移除对端所有代理（断连清理）
func (ts *TunnelService) RemovePeerProxies(connID string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

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
		}
		delete(ts.peerProxies, connID)
	}

	// 清理名称映射
	if name, ok := ts.peerNames[connID]; ok {
		delete(ts.nameToConnID, name)
		delete(ts.peerNames, connID)
	}

	// 清理全局连接池
	if pool, ok := ts.peerPools[connID]; ok {
		delete(ts.peerPools, connID)
		go func() {
			for {
				select {
				case conn := <-pool:
					if conn != nil {
						conn.Close()
					}
				default:
					return
				}
			}
		}()
	}

	// 清理延迟记录
	delete(ts.peerLatencies, connID)
}

// ListProxies 列出所有代理（含统计）
func (ts *TunnelService) ListProxies() []ProxyInfo {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	result := make([]ProxyInfo, 0, len(ts.proxies))
	for _, p := range ts.proxies {
		result = append(result, ProxyInfo{
			Name:         p.Name,
			RemotePort:   p.RemotePort,
			PeerConnID:   p.PeerConnID,
			PeerName:     ts.peerNames[p.PeerConnID],
			TotalConns:   p.TotalConns.Load(),
			ActiveConns:  p.ActiveConns.Load(),
			BytesIn:      p.BytesIn.Load(),
			BytesOut:     p.BytesOut.Load(),
			PoolHits:     p.PoolHits.Load(),
			OnDemandHits: p.OnDemandHits.Load(),
		})
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

	peerCount := len(ts.peerProxies)
	proxyCount := len(ts.proxies)

	return ServerStatsInfo{
		Uptime:      int64(time.Since(ts.startTime).Seconds()),
		PeerCount:   peerCount,
		ProxyCount:  proxyCount,
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
	// 清理所有连接池
	for connID, pool := range ts.peerPools {
		delete(ts.peerPools, connID)
		go func(p chan net.Conn) {
			for {
				select {
				case conn := <-p:
					if conn != nil {
						conn.Close()
					}
				default:
					return
				}
			}
		}(pool)
	}
	ts.peerProxies = make(map[string][]string)
	ts.peerNames = make(map[string]string)
	ts.nameToConnID = make(map[string]string)
}

// RemoveProxy 移除指定代理
func (ts *TunnelService) RemoveProxy(name string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	proxy, ok := ts.proxies[name]
	if !ok {
		return false
	}
	proxy.Close()
	delete(ts.proxies, name)
	// 从 peerProxies 中移除
	for connID, names := range ts.peerProxies {
		for i, n := range names {
			if n == name {
				ts.peerProxies[connID] = append(names[:i], names[i+1:]...)
				break
			}
		}
	}
	logger.Info("[Tunnel] 代理已手动移除: %s", name)
	return true
}

// KickPeer 踢出指定对端
func (ts *TunnelService) KickPeer(connID string) bool {
	ts.mu.RLock()
	_, ok := ts.peerProxies[connID]
	ts.mu.RUnlock()
	if !ok {
		return false
	}
	// 先移除代理
	ts.RemovePeerProxies(connID)
	// 再关闭控制连接
	ts.tcpServer.ConnManager().Kick(connID)
	logger.Info("[Tunnel] 对端已踢出: %s", connID)
	return true
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

// StartLatencyProbe 启动周期性延迟检测（服务端→下游对端）
func (ts *TunnelService) StartLatencyProbe() {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			ts.probeAllPeers()
		}
	}()
}

func (ts *TunnelService) probeAllPeers() {
	ts.mu.RLock()
	connIDs := make([]string, 0, len(ts.peerProxies))
	for connID := range ts.peerProxies {
		connIDs = append(connIDs, connID)
	}
	ts.mu.RUnlock()

	for _, connID := range connIDs {
		go func(cid string) {
			start := time.Now()
			resp, err := ts.SendCommandToPeer(cid, "ping_latency", nil)
			if err != nil {
				return
			}
			if resp.Code == 200 {
				latency := time.Since(start).Milliseconds()
				ts.UpdatePeerLatency(cid, latency)
			}
		}(connID)
	}
}

// ---- 远程管理对端 ----

// SendCommandToPeer 向对端发送指令并等待回复
func (ts *TunnelService) SendCommandToPeer(connID, cmd string, data interface{}) (*tcp.Response, error) {
	seq := fmt.Sprintf("s%d", ts.seqCounter.Add(1))
	key := connID + ":" + seq

	ch := make(chan *tcp.Response, 1)
	ts.pendingMu.Lock()
	ts.pendingRequests[key] = ch
	ts.pendingMu.Unlock()

	defer func() {
		ts.pendingMu.Lock()
		delete(ts.pendingRequests, key)
		ts.pendingMu.Unlock()
	}()

	var rawData json.RawMessage
	if data != nil {
		rawData, _ = json.Marshal(data)
	}

	err := ts.tcpServer.ConnManager().SendTo(connID, ts.tcpServer.Codec(), &tcp.Response{
		Cmd:  cmd,
		Seq:  seq,
		Code: 200,
		Data: rawData,
	})
	if err != nil {
		return nil, fmt.Errorf("发送指令失败: %w", err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("等待对端响应超时")
	}
}

// HandlePeerResponse 分发对端回复到等待的请求
func (ts *TunnelService) HandlePeerResponse(connID string, resp *tcp.Response) bool {
	if resp.Seq == "" {
		return false
	}
	key := connID + ":" + resp.Seq
	ts.pendingMu.Lock()
	ch, ok := ts.pendingRequests[key]
	ts.pendingMu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- resp:
	default:
	}
	return true
}

// GetPeerConfig 获取对端配置
func (ts *TunnelService) GetPeerConfig(connID string) (*tcp.PeerConfigData, error) {
	resp, err := ts.SendCommandToPeer(connID, "get_config", nil)
	if err != nil {
		return nil, err
	}
	if resp.Code != 200 {
		return nil, fmt.Errorf("对端返回错误: %s", resp.Message)
	}
	raw, _ := json.Marshal(resp.Data)
	var cfg tcp.PeerConfigData
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	return &cfg, nil
}

// AddPeerProxy 远程添加对端代理
func (ts *TunnelService) AddPeerProxy(connID string, data *tcp.AddProxyData) error {
	// 先在本节点注册代理端口
	if err := ts.RegisterProxy(connID, data.Name, data.RemotePort); err != nil {
		return fmt.Errorf("注册代理失败: %w", err)
	}

	// 通知对端添加代理配置
	resp, err := ts.SendCommandToPeer(connID, "add_proxy", data)
	if err != nil {
		// 对端没响应，回滚代理
		ts.RemoveProxy(data.Name)
		return err
	}
	if resp.Code != 200 {
		ts.RemoveProxy(data.Name)
		return fmt.Errorf("%s", resp.Message)
	}
	return nil
}

// RemovePeerProxy 远程删除对端代理
func (ts *TunnelService) RemovePeerProxy(connID string, name string) error {
	// 通知对端删除代理
	resp, err := ts.SendCommandToPeer(connID, "remove_proxy", &tcp.RemoveProxyData{Name: name})
	if err != nil {
		return err
	}
	if resp.Code != 200 {
		return fmt.Errorf("%s", resp.Message)
	}
	// 从本节点移除代理
	ts.RemoveProxy(name)
	return nil
}

// UpdatePeerPool 远程修改对端连接池
func (ts *TunnelService) UpdatePeerPool(connID string, poolCount int) error {
	resp, err := ts.SendCommandToPeer(connID, "update_pool", &tcp.UpdatePoolData{PoolCount: poolCount})
	if err != nil {
		return err
	}
	if resp.Code != 200 {
		return fmt.Errorf("%s", resp.Message)
	}
	return nil
}

// GetPeerPeers 获取对端的下游节点列表
func (ts *TunnelService) GetPeerPeers(connID string) ([]tcp.DownstreamPeer, error) {
	resp, err := ts.SendCommandToPeer(connID, "get_clients", nil)
	if err != nil {
		return nil, err
	}
	if resp.Code != 200 {
		return nil, fmt.Errorf("%s", resp.Message)
	}
	raw, _ := json.Marshal(resp.Data)
	var peers []tcp.DownstreamPeer
	if err := json.Unmarshal(raw, &peers); err != nil {
		return nil, fmt.Errorf("解析下游节点失败: %w", err)
	}
	return peers, nil
}

// ForwardPeerCmd 转发命令给对端的下游节点
func (ts *TunnelService) ForwardPeerCmd(connID string, fwd *tcp.ForwardCmdData) (*tcp.ForwardCmdResp, error) {
	resp, err := ts.SendCommandToPeer(connID, "forward_cmd", fwd)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(resp.Data)
	return &tcp.ForwardCmdResp{Code: resp.Code, Message: resp.Message, Data: raw}, nil
}

// ProxyInfo 代理信息（含统计）
type ProxyInfo struct {
	Name         string `json:"name"`
	RemotePort   int    `json:"remote_port"`
	PeerConnID   string `json:"peer_conn_id"`
	PeerName     string `json:"peer_name"`
	TotalConns   int64  `json:"total_conns"`
	ActiveConns  int64  `json:"active_conns"`
	BytesIn      int64  `json:"bytes_in"`
	BytesOut     int64  `json:"bytes_out"`
	PoolHits     int64  `json:"pool_hits"`
	OnDemandHits int64  `json:"on_demand_hits"`
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

// ---- TCP 代理 ----

// TCPProxy 管理一个公网端口监听
type TCPProxy struct {
	Name       string
	RemotePort int
	PeerConnID string
	Listener   net.Listener
	WorkConnCh chan net.Conn
	tunnel     *TunnelService

	// 统计
	TotalConns   atomic.Int64
	ActiveConns  atomic.Int64
	BytesIn      atomic.Int64
	BytesOut     atomic.Int64
	PoolHits     atomic.Int64
	OnDemandHits atomic.Int64

	mu     sync.Mutex
	closed bool
}

func NewTCPProxy(name string, remotePort int, peerConnID string, tunnel *TunnelService) (*TCPProxy, error) {
	addr := net.TCPAddr{Port: remotePort}
	ln, err := net.ListenTCP("tcp", &addr)
	if err != nil {
		return nil, err
	}
	return &TCPProxy{
		Name:       name,
		RemotePort: remotePort,
		PeerConnID: peerConnID,
		Listener:   ln,
		WorkConnCh: make(chan net.Conn, 20),
		tunnel:     tunnel,
	}, nil
}

// Run 接受用户连接并与工作连接配对
func (p *TCPProxy) Run() {
	logger.Info("[Proxy:%s] 监听端口 :%d", p.Name, p.RemotePort)
	for {
		userConn, err := p.Listener.Accept()
		if err != nil {
			p.mu.Lock()
			closed := p.closed
			p.mu.Unlock()
			if closed {
				return
			}
			logger.Error("[Proxy:%s] accept: %v", p.Name, err)
			continue
		}

		logger.Info("[Proxy:%s] 新用户连接: %s", p.Name, userConn.RemoteAddr())

		// 优先从对端全局连接池取预建连接
		if peerPool := p.tunnel.GetPeerPool(p.PeerConnID); peerPool != nil {
			select {
			case workConn := <-peerPool:
				p.PoolHits.Add(1)
				p.TotalConns.Add(1)
				p.ActiveConns.Add(1)
				logger.Info("[Proxy:%s] 连接池命中: user=%s <-> work=%s", p.Name, userConn.RemoteAddr(), workConn.RemoteAddr())
				go func(uc, wc net.Conn) {
					defer p.ActiveConns.Add(-1)
					if err := sendActivation(wc, p.Name); err != nil {
						logger.Error("[Proxy:%s] 激活池连接失败: %v", p.Name, err)
						uc.Close()
						wc.Close()
						return
					}
					p.relay(uc, wc)
				}(userConn, workConn)
				continue
			default:
			}
		}

		// 池空，走按需请求
		p.tunnel.RequestWorkConn(p.Name, p.PeerConnID)

		go func(uc net.Conn) {
			select {
			case workConn := <-p.WorkConnCh:
				p.OnDemandHits.Add(1)
				p.TotalConns.Add(1)
				p.ActiveConns.Add(1)
				logger.Info("[Proxy:%s] 开始转发: user=%s <-> work=%s", p.Name, uc.RemoteAddr(), workConn.RemoteAddr())
				defer p.ActiveConns.Add(-1)
				p.relay(uc, workConn)
			case <-time.After(p.tunnel.workConnTimeout):
				logger.Warn("[Proxy:%s] 工作连接超时", p.Name)
				uc.Close()
			}
		}(userConn)
	}
}

// sendActivation 发送激活信号（携带代理名）
func sendActivation(conn net.Conn, proxyName string) error {
	nameBytes := []byte(proxyName)
	buf := make([]byte, 3+len(nameBytes))
	buf[0] = 0x01
	binary.BigEndian.PutUint16(buf[1:3], uint16(len(nameBytes)))
	copy(buf[3:], nameBytes)
	_, err := conn.Write(buf)
	return err
}

func (p *TCPProxy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.Listener.Close()
		close(p.WorkConnCh)
	}
}

// ---- 数据转发 ----

// relay 带流量统计的双向数据转发
func (p *TCPProxy) relay(c1, c2 net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	// c1=userConn, c2=workConn
	// user -> work = BytesOut (上行到后端)
	// work -> user = BytesIn  (下行到用户)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(c2, c1)
		p.BytesOut.Add(n)
	}()
	go func() {
		defer wg.Done()
		n, _ := io.Copy(c1, c2)
		p.BytesIn.Add(n)
	}()
	wg.Wait()
	c1.Close()
	c2.Close()
}

// Relay 无统计的双向数据转发 (兼容)
func Relay(c1, c2 net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	copyFn := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
	}
	go copyFn(c1, c2)
	go copyFn(c2, c1)
	wg.Wait()
	c1.Close()
	c2.Close()
}
