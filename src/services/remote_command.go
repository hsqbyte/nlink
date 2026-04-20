package services

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hsqbyte/nlink/src/core/tcp"
)

// ---- 远程指令 (RPC over TCP) ----

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
		marshalled, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("序列化指令数据失败: %w", err)
		}
		rawData = marshalled
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

// failPendingForConn 对端断开时，立即给所有属于该 connID 的未完成请求返回错误响应，
// 避免调用方白白等待 SendCommandToPeer 的 10s 超时。
func (ts *TunnelService) failPendingForConn(connID string, reason string) {
	prefix := connID + ":"
	ts.pendingMu.Lock()
	defer ts.pendingMu.Unlock()
	for key, ch := range ts.pendingRequests {
		if len(key) < len(prefix) || key[:len(prefix)] != prefix {
			continue
		}
		select {
		case ch <- &tcp.Response{Code: 599, Message: reason}:
		default:
		}
	}
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
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("序列化对端配置数据失败: %w", err)
	}
	var cfg tcp.PeerConfigData
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	return &cfg, nil
}

// AddPeerProxy 远程添加对端代理
func (ts *TunnelService) AddPeerProxy(connID string, data *tcp.AddProxyData) error {
	// 先在本节点注册代理端口
	opts := &ProxyOptions{
		AllowCIDR: data.AllowCIDR,
		DenyCIDR:  data.DenyCIDR,
		RateLimit: data.RateLimit,
	}
	var err error
	if data.Type == "udp" {
		err = ts.RegisterUDPProxy(connID, data.Name, data.RemotePort, opts)
	} else if data.Type == "http" {
		err = ts.RegisterHTTPProxy(connID, data.Name, data.CustomDomains, data.HostRewrite, opts)
	} else if data.Type == "socks5" {
		err = ts.RegisterSOCKS5Proxy(connID, data.Name, data.RemotePort, opts)
	} else {
		err = ts.RegisterProxy(connID, data.Name, data.RemotePort, opts)
	}
	if err != nil {
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

// UpdatePeerProxy 远程更新对端代理：先 remove 再 add，失败不做自动回滚（返回错误给调用方）
func (ts *TunnelService) UpdatePeerProxy(connID string, data *tcp.AddProxyData) error {
	if err := ts.RemovePeerProxy(connID, data.Name); err != nil {
		return fmt.Errorf("移除旧代理失败: %w", err)
	}
	if err := ts.AddPeerProxy(connID, data); err != nil {
		return fmt.Errorf("添加新代理失败: %w", err)
	}
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
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("序列化对端下游列表失败: %w", err)
	}
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
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("序列化转发结果失败: %w", err)
	}
	return &tcp.ForwardCmdResp{Code: resp.Code, Message: resp.Message, Data: raw}, nil
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

	// 并发度限制：避免 peer 数量激增时一次性创建过多 goroutine
	const maxConcurrent = 32
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	for _, connID := range connIDs {
		wg.Add(1)
		sem <- struct{}{}
		go func(cid string) {
			defer wg.Done()
			defer func() { <-sem }()
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
	wg.Wait()
}
