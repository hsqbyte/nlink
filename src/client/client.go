package client

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsqbyte/nlink/src/core/tcp"
	"github.com/hsqbyte/nlink/src/core/vpn"
	modelConfig "github.com/hsqbyte/nlink/src/models/config"
	"github.com/hsqbyte/nlink/src/services"
)

const (
	reconnectBaseDelay = 3 * time.Second
	reconnectMaxDelay  = 60 * time.Second
)

type Client struct {
	nodeName      string // 本节点名称
	remoteName    string // 对端节点名称
	cfg           *modelConfig.PeerConfig
	conn          net.Conn
	mu            sync.Mutex
	seqID         int
	done          chan struct{}
	authenticated bool                    // 是否曾认证成功
	poolCount     int                     // 全局预建连接数
	connID        string                  // 对端分配的连接ID
	crypto        *tcp.Crypto             // 控制通道加密
	pingStart     atomic.Int64            // 心跳发送时间 (UnixMilli)
	backendPools  map[string]*BackendPool // proxyName -> 后端池 (含 LB + HC)
}

func Run(nodeName string, cfg *modelConfig.PeerConfig) error {
	delay := reconnectBaseDelay
	for {
		c := &Client{nodeName: nodeName, cfg: cfg, done: make(chan struct{})}
		// 初始化控制通道加密
		if cfg.Token != "" {
			cr, err := tcp.NewCrypto(cfg.Token)
			if err != nil {
				return fmt.Errorf("加密初始化失败: %w", err)
			}
			c.crypto = cr
		}
		err := c.run()
		if err == nil {
			return nil
		}
		fmt.Fprintf(os.Stderr, "[Node:%s] 连接断开: %v\n", nodeName, err)

		// 更新上游连接状态为断开
		if ts := services.GetTunnelService(); ts != nil {
			ts.UpdateUpstreamPeerStatus(cfg.Addr, cfg.Port, false)
		}

		// 如果曾经成功连接过（认证通过），重置退避
		if c.authenticated {
			delay = reconnectBaseDelay
		}

		fmt.Fprintf(os.Stderr, "[Node:%s] %v 后重连...\n", nodeName, delay)
		time.Sleep(delay)

		delay = delay * 2
		if delay > reconnectMaxDelay {
			delay = reconnectMaxDelay
		}
	}
}

func (c *Client) run() error {
	// 确定连接池大小
	c.poolCount = c.cfg.PoolCount
	if c.poolCount < 0 {
		c.poolCount = 0
	}

	addr := net.JoinHostPort(c.cfg.Addr, fmt.Sprintf("%d", c.cfg.Port))
	fmt.Printf("[Node:%s] 连接对端: %s\n", c.nodeName, addr)

	conn, err := c.dialPeer(addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("连接失败: %w", err)
	}
	c.conn = conn
	fmt.Printf("[Node:%s] 已连接: %s\n", c.nodeName, conn.RemoteAddr())

	// 确保退出时关闭连接和通知心跳
	defer func() {
		close(c.done)
		conn.Close()
	}()

	// 展开端口范围代理 (F5) —— 在注册前绑定到本地 cfg
	expandProxyConfig(c.cfg)

	// 构造后端池 (F3/F4)。openWorkConn/openPoolConn 会使用这里的池 dial。
	c.backendPools = make(map[string]*BackendPool, len(c.cfg.Proxies))
	for i := range c.cfg.Proxies {
		p := &c.cfg.Proxies[i]
		c.backendPools[p.Name] = NewBackendPool(p)
	}
	defer func() {
		for _, bp := range c.backendPools {
			bp.Close()
		}
	}()

	if err := c.auth(); err != nil {
		return err
	}
	c.authenticated = true
	fmt.Printf("[Node:%s] 认证成功\n", c.nodeName)

	// 注册上游连接到 TunnelService（供 Dashboard 展示）
	proxyNames := make([]string, 0, len(c.cfg.Proxies))
	for _, p := range c.cfg.Proxies {
		proxyNames = append(proxyNames, p.Name)
	}
	upName := c.remoteName
	if upName == "" {
		upName = c.cfg.Addr
	}
	if ts := services.GetTunnelService(); ts != nil {
		ts.RegisterUpstreamPeer(c.cfg.Addr, c.cfg.Port, upName, proxyNames)
	}

	for _, p := range c.cfg.Proxies {
		if err := c.registerProxy(p); err != nil {
			fmt.Fprintf(os.Stderr, "[Node:%s] 注册代理失败 %s: %v\n", c.nodeName, p.Name, err)
			continue
		}
		fmt.Printf("[Node:%s] 代理注册成功: %s -> :%d\n", c.nodeName, p.Name, p.RemotePort)
	}

	// 预建连接池
	if c.poolCount > 0 {
		c.fillPool()
	}

	// VPN 打洞：交换端点信息并尝试建立直连
	go c.vpnHolePunch()

	go c.heartbeat()

	// readLoop 返回意味着连接断开
	return c.readLoop()
}

// fillPool 预建 poolCount 个全局工作连接
func (c *Client) fillPool() {
	for i := 0; i < c.poolCount; i++ {
		go c.openPoolConn()
	}
	fmt.Printf("[Node:%s] 连接池: 预建 %d 个全局工作连接\n", c.nodeName, c.poolCount)
}

// openPoolConn 预建一个全局工作连接，等待服务端激活后连接本地服务并转发
func (c *Client) openPoolConn() {
	if c.poolCount <= 0 || c.connID == "" {
		return
	}

	select {
	case <-c.done:
		return
	default:
	}

	workAddr := net.JoinHostPort(c.cfg.Addr, fmt.Sprintf("%d", c.cfg.Port+1))
	workConn, err := c.dialPeer(workAddr, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Node:%s] 连接池-工作连接失败: %v\n", c.nodeName, err)
		return
	}

	regMsg := tcp.Message{
		Cmd:  "new_work_conn",
		Data: mustMarshal(tcp.NewWorkConnData{ConnID: c.connID}),
	}
	buf := tcp.EncodeMessage(&regMsg)
	if _, err := workConn.Write(buf); err != nil {
		fmt.Fprintf(os.Stderr, "[Node:%s] 连接池-注册失败: %v\n", c.nodeName, err)
		workConn.Close()
		return
	}

	// 阻塞等待服务端激活信号（携带代理名）
	proxyName, err := readActivation(workConn)
	if err != nil {
		workConn.Close()
		return
	}

	var proxy *modelConfig.ProxyConfig
	for i := range c.cfg.Proxies {
		if c.cfg.Proxies[i].Name == proxyName {
			proxy = &c.cfg.Proxies[i]
			break
		}
	}
	if proxy == nil {
		fmt.Fprintf(os.Stderr, "[Node:%s] 连接池-未知代理: %s\n", c.nodeName, proxyName)
		workConn.Close()
		return
	}

	localAddr := net.JoinHostPort(proxy.LocalIP, fmt.Sprintf("%d", proxy.LocalPort))
	if proxy.Type == "udp" {
		fmt.Printf("[Node:%s] 连接池 UDP 转发: %s <-> %s\n", c.nodeName, proxyName, localAddr)
		relayUDP(workConn, proxy.LocalIP, proxy.LocalPort)
		go c.openPoolConn()
		return
	}
	bp := c.backendPools[proxyName]
	var (
		localConn net.Conn
		backend   *Backend
		dialErr   error
	)
	if bp != nil {
		localConn, backend, dialErr = bp.DialBackend(5 * time.Second)
	} else {
		localConn, dialErr = net.DialTimeout("tcp", localAddr, 5*time.Second)
	}
	if dialErr != nil {
		fmt.Fprintf(os.Stderr, "[Node:%s] 连接池-本地连接失败 %s: %v\n", c.nodeName, localAddr, dialErr)
		workConn.Close()
		return
	}
	if hdr := proxyProtocolHeader(proxy.ProxyProtocol, workConn.RemoteAddr(), localConn.RemoteAddr()); hdr != nil {
		if _, werr := localConn.Write(hdr); werr != nil {
			fmt.Fprintf(os.Stderr, "[Node:%s] 连接池-PROXY 头写入失败: %v\n", c.nodeName, werr)
			localConn.Close()
			workConn.Close()
			if backend != nil {
				backend.ActiveConns.Add(-1)
			}
			return
		}
	}
	backendAddr := localAddr
	if backend != nil {
		backendAddr = backend.Addr
	}
	fmt.Printf("[Node:%s] 连接池转发: %s <-> %s\n", c.nodeName, proxyName, backendAddr)
	relay(workConn, localConn)
	if backend != nil {
		backend.ActiveConns.Add(-1)
	}

	// 转发结束后补充池连接
	go c.openPoolConn()
}

// readActivation 读取服务端激活信号（携带代理名）
func readActivation(conn net.Conn) (string, error) {
	header := make([]byte, 3)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", err
	}
	if header[0] != 0x01 {
		return "", fmt.Errorf("invalid activation signal: %x", header[0])
	}
	nameLen := binary.BigEndian.Uint16(header[1:3])
	if nameLen == 0 || nameLen > 256 {
		return "", fmt.Errorf("invalid proxy name length: %d", nameLen)
	}
	nameBuf := make([]byte, nameLen)
	if _, err := io.ReadFull(conn, nameBuf); err != nil {
		return "", err
	}
	return string(nameBuf), nil
}

func (c *Client) auth() error {
	if err := c.sendMsg("auth", tcp.AuthData{Token: c.cfg.Token, Name: c.nodeName}); err != nil {
		return err
	}
	resp, err := c.readMsg()
	if err != nil {
		return err
	}
	if resp.Cmd != "auth" {
		return fmt.Errorf("期望命令 auth, 收到 %s", resp.Cmd)
	}
	if resp.Code != 0 && resp.Code != 200 {
		return fmt.Errorf("认证失败: code=%d msg=%s", resp.Code, resp.Message)
	}
	if dataMap, ok := resp.Data.(map[string]interface{}); ok {
		if connID, ok := dataMap["conn_id"].(string); ok {
			c.connID = connID
		}
		if nodeName, ok := dataMap["node_name"].(string); ok {
			c.remoteName = nodeName
		}
	}
	return nil
}

func (c *Client) registerProxy(p modelConfig.ProxyConfig) error {
	return c.sendAndExpect("new_proxy", tcp.NewProxyData{
		Name:          p.Name,
		Type:          p.Type,
		RemotePort:    p.RemotePort,
		LocalIP:       p.LocalIP,
		LocalPort:     p.LocalPort,
		CustomDomains: p.CustomDomains,
		HostRewrite:   p.HostRewrite,
		AllowCIDR:     p.AllowCIDR,
		DenyCIDR:      p.DenyCIDR,
		RateLimit:     p.RateLimit,
	}, "new_proxy")
}

func (c *Client) sendAndExpect(cmd string, data interface{}, expectCmd string) error {
	if err := c.sendMsg(cmd, data); err != nil {
		return err
	}
	resp, err := c.readMsg()
	if err != nil {
		return err
	}
	if resp.Cmd != expectCmd {
		return fmt.Errorf("期望命令 %s, 收到 %s", expectCmd, resp.Cmd)
	}

	var baseResp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	raw, _ := json.Marshal(resp)
	json.Unmarshal(raw, &baseResp)
	if baseResp.Code != 0 && baseResp.Code != 200 {
		return fmt.Errorf("服务器返回错误: code=%d msg=%s", baseResp.Code, baseResp.Message)
	}
	return nil
}

func (c *Client) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			c.mu.Lock()
			c.pingStart.Store(time.Now().UnixMilli())
			err := c.sendMsgLocked("ping", nil)
			c.mu.Unlock()
			if err != nil {
				fmt.Fprintf(os.Stderr, "[Node:%s] 心跳失败: %v\n", c.nodeName, err)
				return
			}
		}
	}
}

// vpnHolePunch VPN 打洞：探测公网地址 → 交换端点 → 尝试打洞
func (c *Client) vpnHolePunch() {
	engine := vpn.GetGlobalEngine()
	if engine == nil {
		return // VPN 未启用
	}

	// 等待一会儿让 VPN engine 完全启动
	time.Sleep(2 * time.Second)

	// STUN 探测本机公网地址
	result, err := engine.DiscoverPublicAddr()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Node:%s] VPN STUN 探测失败: %v (使用静态配置)\n", c.nodeName, err)
		return
	}
	fmt.Printf("[Node:%s] VPN 公网地址: %s\n", c.nodeName, result.PublicAddr)

	// 发送本机端点给服务端
	c.mu.Lock()
	err = c.sendMsgLocked("vpn_endpoint", tcp.VPNEndpointData{
		VirtualIP:  engine.VirtualIP(),
		PublicAddr: result.PublicAddr.String(),
		ListenPort: engine.Config().ListenPort,
	})
	c.mu.Unlock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Node:%s] VPN 端点交换失败: %v\n", c.nodeName, err)
		return
	}

	// 响应在 readLoop 中处理 (vpn_endpoint 命令)
}

func (c *Client) readLoop() error {
	for {
		msg, err := c.readMsg()
		if err != nil {
			return fmt.Errorf("读取消息失败: %w", err)
		}

		switch msg.Cmd {
		case "pong":
			// 计算心跳延迟
			if c.pingStart.Load() > 0 {
				latency := time.Now().UnixMilli() - c.pingStart.Load()
				c.pingStart.Store(0)
				if ts := services.GetTunnelService(); ts != nil {
					ts.UpdateUpstreamPeerLatency(c.cfg.Addr, c.cfg.Port, latency)
				}
			}
		case "ping_latency":
			c.replyCmd(msg.Seq, "ping_latency", 200, "pong", nil)
		case "start_work_conn":
			var data tcp.StartWorkConnData
			raw, _ := json.Marshal(msg.Data)
			json.Unmarshal(raw, &data)
			go c.openWorkConn(data.ProxyName)
		case "get_config":
			c.handleGetConfig(msg.Seq)
		case "add_proxy":
			var data tcp.AddProxyData
			raw, _ := json.Marshal(msg.Data)
			json.Unmarshal(raw, &data)
			c.handleAddProxy(msg.Seq, &data)
		case "remove_proxy":
			var data tcp.RemoveProxyData
			raw, _ := json.Marshal(msg.Data)
			json.Unmarshal(raw, &data)
			c.handleRemoveProxy(msg.Seq, &data)
		case "update_pool":
			var data tcp.UpdatePoolData
			raw, _ := json.Marshal(msg.Data)
			json.Unmarshal(raw, &data)
			c.handleUpdatePool(msg.Seq, &data)
		case "get_peers":
			c.handleGetPeers(msg.Seq)
		case "forward_cmd":
			var data tcp.ForwardCmdData
			raw, _ := json.Marshal(msg.Data)
			json.Unmarshal(raw, &data)
			go c.handleForwardCmd(msg.Seq, &data)
		case "vpn_endpoint":
			go c.handleVPNEndpointResp(msg)
		default:
			fmt.Printf("[Node:%s] 收到: cmd=%s\n", c.nodeName, msg.Cmd)
		}
	}
}

func (c *Client) openWorkConn(proxyName string) {
	var proxy *modelConfig.ProxyConfig
	for i := range c.cfg.Proxies {
		if c.cfg.Proxies[i].Name == proxyName {
			proxy = &c.cfg.Proxies[i]
			break
		}
	}
	if proxy == nil {
		fmt.Fprintf(os.Stderr, "[Node:%s] 未知代理: %s\n", c.nodeName, proxyName)
		return
	}

	workAddr := net.JoinHostPort(c.cfg.Addr, fmt.Sprintf("%d", c.cfg.Port+1))
	workConn, err := c.dialPeer(workAddr, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Node:%s] 工作连接失败: %v\n", c.nodeName, err)
		return
	}

	regMsg := tcp.Message{
		Cmd:  "new_work_conn",
		Data: mustMarshal(tcp.NewWorkConnData{ProxyName: proxyName}),
	}
	buf := tcp.EncodeMessage(&regMsg)
	if _, err := workConn.Write(buf); err != nil {
		fmt.Fprintf(os.Stderr, "[Node:%s] 工作连接注册失败: %v\n", c.nodeName, err)
		workConn.Close()
		return
	}

	localAddr := net.JoinHostPort(proxy.LocalIP, fmt.Sprintf("%d", proxy.LocalPort))
	if proxy.Type == "udp" {
		fmt.Printf("[Node:%s] 开始 UDP 转发: %s <-> %s\n", c.nodeName, proxyName, localAddr)
		relayUDP(workConn, proxy.LocalIP, proxy.LocalPort)
		go c.openPoolConn()
		return
	}
	bp := c.backendPools[proxyName]
	var (
		localConn net.Conn
		backend   *Backend
		dialErr   error
	)
	if bp != nil {
		localConn, backend, dialErr = bp.DialBackend(5 * time.Second)
	} else {
		localConn, dialErr = net.DialTimeout("tcp", localAddr, 5*time.Second)
	}
	if dialErr != nil {
		fmt.Fprintf(os.Stderr, "[Node:%s] 本地连接失败 %s: %v\n", c.nodeName, localAddr, dialErr)
		workConn.Close()
		return
	}
	if hdr := proxyProtocolHeader(proxy.ProxyProtocol, workConn.RemoteAddr(), localConn.RemoteAddr()); hdr != nil {
		if _, werr := localConn.Write(hdr); werr != nil {
			fmt.Fprintf(os.Stderr, "[Node:%s] PROXY 头写入失败: %v\n", c.nodeName, werr)
			localConn.Close()
			workConn.Close()
			if backend != nil {
				backend.ActiveConns.Add(-1)
			}
			return
		}
	}
	backendAddr := localAddr
	if backend != nil {
		backendAddr = backend.Addr
	}
	fmt.Printf("[Node:%s] 开始转发: %s <-> %s\n", c.nodeName, proxyName, backendAddr)
	relay(workConn, localConn)
	if backend != nil {
		backend.ActiveConns.Add(-1)
	}

	// 按需连接用完后，补充全局池连接
	go c.openPoolConn()
}

// ---- 远程管理指令处理 ----

// handleGetConfig 返回本节点当前配置
func (c *Client) handleGetConfig(seq string) {
	proxies := make([]tcp.ProxyDetail, len(c.cfg.Proxies))
	for i, p := range c.cfg.Proxies {
		proxies[i] = tcp.ProxyDetail{
			Name: p.Name, Type: p.Type,
			LocalIP: p.LocalIP, LocalPort: p.LocalPort,
			RemotePort: p.RemotePort,
		}
	}
	c.replyCmd(seq, "get_config", 200, "success", tcp.PeerConfigData{
		PeerAddr:  c.cfg.Addr,
		PeerPort:  c.cfg.Port,
		PoolCount: c.poolCount,
		Proxies:   proxies,
	})
}

// handleAddProxy 服务端远程添加代理
func (c *Client) handleAddProxy(seq string, data *tcp.AddProxyData) {
	// 检查是否已存在
	for _, p := range c.cfg.Proxies {
		if p.Name == data.Name {
			c.replyCmd(seq, "add_proxy", 409, "代理已存在: "+data.Name, nil)
			return
		}
	}

	newProxy := modelConfig.ProxyConfig{
		Name:          data.Name,
		Type:          data.Type,
		LocalIP:       data.LocalIP,
		LocalPort:     data.LocalPort,
		RemotePort:    data.RemotePort,
		RemotePortEnd: data.RemotePortEnd,
		LocalBackends: data.LocalBackends,
		LBStrategy:    data.LBStrategy,
		ProxyProtocol: data.ProxyProtocol,
		CustomDomains: data.CustomDomains,
		HostRewrite:   data.HostRewrite,
		AllowCIDR:     data.AllowCIDR,
		DenyCIDR:      data.DenyCIDR,
		RateLimit:     data.RateLimit,
	}

	c.cfg.Proxies = append(c.cfg.Proxies, newProxy)
	// 运行时新建后端池 (LB/HC)，允许后续 openWorkConn/openPoolConn 命中
	if c.backendPools == nil {
		c.backendPools = make(map[string]*BackendPool)
	}
	c.backendPools[newProxy.Name] = NewBackendPool(&newProxy)
	fmt.Printf("[Node:%s] 远程添加代理: %s -> :%d (本地 %s:%d)\n", c.nodeName, data.Name, data.RemotePort, data.LocalIP, data.LocalPort)

	c.replyCmd(seq, "add_proxy", 200, "代理已添加", nil)
}

// handleRemoveProxy 服务端远程删除代理
func (c *Client) handleRemoveProxy(seq string, data *tcp.RemoveProxyData) {
	found := false
	for i, p := range c.cfg.Proxies {
		if p.Name == data.Name {
			c.cfg.Proxies = append(c.cfg.Proxies[:i], c.cfg.Proxies[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		c.replyCmd(seq, "remove_proxy", 404, "代理不存在: "+data.Name, nil)
		return
	}
	fmt.Printf("[Node:%s] 远程删除代理: %s\n", c.nodeName, data.Name)
	c.replyCmd(seq, "remove_proxy", 200, "代理已删除", nil)
}

// handleUpdatePool 服务端远程修改连接池配置
func (c *Client) handleUpdatePool(seq string, data *tcp.UpdatePoolData) {
	old := c.poolCount
	c.poolCount = data.PoolCount
	c.cfg.PoolCount = data.PoolCount
	fmt.Printf("[Node:%s] 远程修改连接池: %d -> %d\n", c.nodeName, old, data.PoolCount)

	// 如果新值 > 旧值，补充全局池连接
	if data.PoolCount > old {
		diff := data.PoolCount - old
		for i := 0; i < diff; i++ {
			go c.openPoolConn()
		}
	}

	c.replyCmd(seq, "update_pool", 200, fmt.Sprintf("连接池已更新: %d -> %d", old, data.PoolCount), nil)
}

// handleGetPeers 返回本节点作为监听端时的在线对端列表
func (c *Client) handleGetPeers(seq string) {
	ts := services.GetTunnelService()
	if ts == nil {
		c.replyCmd(seq, "get_peers", 200, "success", []tcp.DownstreamPeer{})
		return
	}
	peers := ts.ListPeers()
	result := make([]tcp.DownstreamPeer, 0, len(peers))
	for _, p := range peers {
		if p.ConnID == c.connID {
			continue
		}
		result = append(result, tcp.DownstreamPeer{
			ConnID:  p.ConnID,
			Name:    p.Name,
			Proxies: p.Proxies,
		})
	}
	c.replyCmd(seq, "get_peers", 200, "success", result)
}

// handleForwardCmd 转发命令给本节点的下游对端
func (c *Client) handleForwardCmd(seq string, data *tcp.ForwardCmdData) {
	ts := services.GetTunnelService()
	if ts == nil {
		c.replyCmd(seq, "forward_cmd", 503, "本节点未运行监听", nil)
		return
	}

	if len(data.Path) > 0 {
		// 多跳: 剥离第一个节点，继续转发
		nextHop := data.Path[0]
		fwd := &tcp.ForwardCmdData{
			TargetID: data.TargetID,
			Path:     data.Path[1:],
			Cmd:      data.Cmd,
			Data:     data.Data,
		}
		resp, err := ts.ForwardPeerCmd(nextHop, fwd)
		if err != nil {
			c.replyCmd(seq, "forward_cmd", 500, err.Error(), nil)
			return
		}
		c.replyCmd(seq, "forward_cmd", resp.Code, resp.Message, json.RawMessage(resp.Data))
		return
	}

	// 直接转发给 TargetID
	switch data.Cmd {
	case "forward_cmd":
		// forward_cmd 需要嵌套转发
		fwd := &tcp.ForwardCmdData{
			TargetID: data.TargetID,
			Cmd:      data.Cmd,
			Data:     data.Data,
		}
		resp, err := ts.ForwardPeerCmd(data.TargetID, fwd)
		if err != nil {
			c.replyCmd(seq, "forward_cmd", 500, err.Error(), nil)
			return
		}
		c.replyCmd(seq, "forward_cmd", resp.Code, resp.Message, json.RawMessage(resp.Data))
	default:
		// 所有标准命令（get_config, add_proxy, remove_proxy, update_pool, get_peers）直接发给目标
		resp, err := ts.SendCommandToPeer(data.TargetID, data.Cmd, data.Data)
		if err != nil {
			c.replyCmd(seq, "forward_cmd", 500, err.Error(), nil)
			return
		}
		raw, _ := json.Marshal(resp.Data)
		c.replyCmd(seq, "forward_cmd", resp.Code, resp.Message, json.RawMessage(raw))
	}
}

// handleVPNEndpointResp 处理服务端回复的 VPN 端点信息，尝试打洞
func (c *Client) handleVPNEndpointResp(msg *tcp.Response) {
	engine := vpn.GetGlobalEngine()
	if engine == nil {
		return
	}

	// 解析服务端的 VPN 端点
	raw, _ := json.Marshal(msg.Data)
	var resp struct {
		VirtualIP  string `json:"virtual_ip"`
		PublicAddr string `json:"public_addr"`
		ListenPort int    `json:"listen_port"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "[Node:%s] VPN 端点解析失败: %v\n", c.nodeName, err)
		return
	}

	if resp.VirtualIP == "" {
		return
	}

	// 提取纯 IP（去掉 CIDR 后缀）
	peerVIP := resp.VirtualIP
	if ip, _, err := net.ParseCIDR(resp.VirtualIP); err == nil {
		peerVIP = ip.String()
	}

	if resp.PublicAddr != "" {
		// 有公网地址，尝试打洞
		fmt.Printf("[Node:%s] VPN 打洞: 对端 %s 公网 %s\n", c.nodeName, peerVIP, resp.PublicAddr)
		result, err := engine.PunchPeer(resp.PublicAddr, peerVIP)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Node:%s] VPN 打洞失败: %v\n", c.nodeName, err)
		} else if result.Success {
			fmt.Printf("[Node:%s] VPN 打洞成功: %s -> %s\n", c.nodeName, peerVIP, result.PeerAddr)
			return
		}
	}

	// 打洞失败或无公网地址，回退到静态配置
	if c.cfg.VPNPort > 0 && c.cfg.VirtualIP != "" {
		endpoint := fmt.Sprintf("%s:%d", c.cfg.Addr, c.cfg.VPNPort)
		if err := engine.AddPeerWithPolicy(c.cfg.VirtualIP, endpoint, c.cfg.VPNRoutes, c.cfg.VPNAllowCIDR, c.cfg.VPNDenyCIDR); err != nil {
			fmt.Fprintf(os.Stderr, "[Node:%s] VPN 静态对端添加失败: %v\n", c.nodeName, err)
		} else {
			fmt.Printf("[Node:%s] VPN 回退静态配置: %s -> %s (routes=%v)\n", c.nodeName, c.cfg.VirtualIP, endpoint, c.cfg.VPNRoutes)
		}
	}
}

// replyCmd 回复服务端指令（通过 Message.Data 包装 code/message/data）
func (c *Client) replyCmd(seq, cmd string, code int, message string, data interface{}) {
	payload := map[string]interface{}{
		"code":    code,
		"message": message,
	}
	if data != nil {
		payload["data"] = data
	}
	msg := tcp.Message{
		Cmd:  cmd,
		Seq:  seq,
		Data: mustMarshal(payload),
	}
	msgBytes, _ := json.Marshal(msg)

	// 加密（如果启用）
	if c.crypto != nil {
		encrypted, err := c.crypto.Encrypt(msgBytes)
		if err == nil {
			msgBytes = encrypted
		}
	}

	buf := make([]byte, 4+len(msgBytes))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(msgBytes)))
	copy(buf[4:], msgBytes)
	c.mu.Lock()
	c.conn.Write(buf)
	c.mu.Unlock()
}

func (c *Client) sendMsg(cmd string, data interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sendMsgLocked(cmd, data)
}

func (c *Client) sendMsgLocked(cmd string, data interface{}) error {
	c.seqID++
	msg := tcp.Message{
		Cmd: cmd,
		Seq: fmt.Sprintf("%d", c.seqID),
	}
	if data != nil {
		msg.Data = mustMarshal(data)
	}
	payload, _ := json.Marshal(msg)

	// 加密（如果启用）
	if c.crypto != nil {
		encrypted, err := c.crypto.Encrypt(payload)
		if err != nil {
			return fmt.Errorf("加密失败: %w", err)
		}
		payload = encrypted
	}

	buf := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(payload)))
	copy(buf[4:], payload)
	_, err := c.conn.Write(buf)
	return err
}

func (c *Client) readMsg() (*tcp.Response, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(c.conn, header); err != nil {
		return nil, err
	}
	msgLen := int(binary.BigEndian.Uint32(header))
	if msgLen <= 0 || msgLen > 65536 {
		return nil, fmt.Errorf("消息长度无效: %d", msgLen)
	}
	body := make([]byte, msgLen)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return nil, err
	}

	// 解密（如果启用）
	if c.crypto != nil {
		decrypted, err := c.crypto.Decrypt(body)
		if err != nil {
			return nil, fmt.Errorf("解密失败: %w", err)
		}
		body = decrypted
	}

	var resp tcp.Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func mustMarshal(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func relay(c1, c2 net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
	}
	go cp(c1, c2)
	go cp(c2, c1)
	wg.Wait()
	c1.Close()
	c2.Close()
}

// relayUDP 在 workConn(TCP) 与本地 UDP 服务之间做分帧转发。
// workConn 上的格式: [len:uint16 BE][payload]
// 转发结束后 workConn 已关闭。
func relayUDP(workConn net.Conn, localIP string, localPort int) {
	defer workConn.Close()

	localAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(localIP, fmt.Sprintf("%d", localPort)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[UDP] 解析本地地址失败: %v\n", err)
		return
	}
	udpConn, err := net.DialUDP("udp", nil, localAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[UDP] 连接本地失败 %s: %v\n", localAddr, err)
		return
	}
	defer udpConn.Close()

	done := make(chan struct{}, 2)

	// workConn -> local UDP
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			var hdr [2]byte
			if _, err := io.ReadFull(workConn, hdr[:]); err != nil {
				return
			}
			n := binary.BigEndian.Uint16(hdr[:])
			if n == 0 {
				continue
			}
			buf := make([]byte, n)
			if _, err := io.ReadFull(workConn, buf); err != nil {
				return
			}
			if _, err := udpConn.Write(buf); err != nil {
				return
			}
		}
	}()

	// local UDP -> workConn
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 65507)
		for {
			udpConn.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := udpConn.Read(buf)
			if err != nil {
				return
			}
			if n > 0xFFFF {
				continue
			}
			frame := make([]byte, 2+n)
			binary.BigEndian.PutUint16(frame[:2], uint16(n))
			copy(frame[2:], buf[:n])
			if _, err := workConn.Write(frame); err != nil {
				return
			}
		}
	}()

	<-done
}
