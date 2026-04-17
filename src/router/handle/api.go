package handle

import (
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/hsqbyte/nlink/src/core/config"
	"github.com/hsqbyte/nlink/src/core/tcp"
	"github.com/hsqbyte/nlink/src/core/vpn"
	"github.com/hsqbyte/nlink/src/router"
	"github.com/hsqbyte/nlink/src/services"
)

// 代理名 / 节点名允许字符：字母/数字/下划线/连字符，长度 1-64
var validIdentifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// validateIdentifier 校验路径参数中的标识符（代理名、节点名等），
// 不合法时返回 400 并写响应，返回 false 告知调用方终止处理。
func validateIdentifier(c *gin.Context, value, field string) bool {
	if !validIdentifier.MatchString(value) {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "非法的 " + field + " 格式（仅允许字母/数字/下划线/连字符，长度 1-64）",
		})
		return false
	}
	return true
}

func init() {
	router.APIRouter.GET("/proxies", listProxies)
	router.APIRouter.DELETE("/proxies/:name", removeProxy)
	router.APIRouter.GET("/peers", listPeers)
	router.APIRouter.DELETE("/peers/:name", kickPeer)
	router.APIRouter.GET("/status", serverStatus)
	router.APIRouter.GET("/stats", serverStats)
	router.APIRouter.GET("/node/config", getNodeConfig)
	router.APIRouter.PUT("/node/config", updateNodeConfig)

	// 远程管理对端
	router.APIRouter.GET("/peers/:name/config", getPeerConfig)
	router.APIRouter.POST("/peers/:name/proxies", addPeerProxy)
	router.APIRouter.DELETE("/peers/:name/proxies/:proxyName", removePeerProxy)
	router.APIRouter.PUT("/peers/:name/pool", updatePeerPool)
	router.APIRouter.GET("/peers/:name/peers", getPeerPeers)
	router.APIRouter.POST("/peers/:name/forward", forwardPeerCmd)
}

// resolvePeer 根据名称解析对端 connID
func resolvePeer(c *gin.Context) (string, bool) {
	name := c.Param("name")
	if !validateIdentifier(c, name, "节点名") {
		return "", false
	}
	ts := services.GetTunnelService()
	connID, ok := ts.GetConnIDByName(name)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "节点不存在: " + name})
		return "", false
	}
	return connID, true
}

// listProxies 列出所有代理
func listProxies(c *gin.Context) {
	ts := services.GetTunnelService()
	proxies := ts.ListProxies()
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data":    proxies,
	})
}

// removeProxy 移除指定代理
func removeProxy(c *gin.Context) {
	name := c.Param("name")
	if !validateIdentifier(c, name, "代理名") {
		return
	}
	ts := services.GetTunnelService()
	if ts.RemoveProxy(name) {
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "代理已移除"})
	} else {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "代理不存在"})
	}
}

// listPeers 列出所有在线对端
func listPeers(c *gin.Context) {
	ts := services.GetTunnelService()
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data":    ts.ListPeers(),
	})
}

// kickPeer 踢出指定对端
func kickPeer(c *gin.Context) {
	connID, ok := resolvePeer(c)
	if !ok {
		return
	}
	ts := services.GetTunnelService()
	if ts.KickPeer(connID) {
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "节点已踢出"})
	} else {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "节点不存在"})
	}
}

// serverStatus 节点状态
func serverStatus(c *gin.Context) {
	ts := services.GetTunnelService()
	proxies := ts.ListProxies()
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data": gin.H{
			"proxy_count": len(proxies),
			"proxies":     proxies,
		},
	})
}

// serverStats Dashboard 数据接口
func serverStats(c *gin.Context) {
	ts := services.GetTunnelService()
	data := gin.H{
		"node_name": config.GlobalConfig.Node.Name,
		"server":    ts.ServerStats(),
		"proxies":   ts.ListProxies(),
		"peers":     ts.ListPeers(),
		"upstream":  ts.ListUpstreamPeers(),
	}
	// VPN 信息
	vpnCfg := config.GlobalConfig.Node.VPN
	if vpnCfg != nil && vpnCfg.IsEnabled() {
		vpnData := gin.H{
			"enabled":     true,
			"virtual_ip":  vpnCfg.VirtualIP,
			"listen_port": vpnCfg.ListenPort,
			"mtu":         vpnCfg.MTU,
		}
		// 获取公网地址和在线对端
		if engine := vpn.GetGlobalEngine(); engine != nil {
			if addr := engine.CachedPublicAddr(); addr != "" {
				vpnData["public_addr"] = addr
			}
			peers := engine.Transport().ListPeers()
			if len(peers) > 0 {
				peerList := make([]gin.H, 0, len(peers))
				for _, p := range peers {
					peerList = append(peerList, gin.H{
						"virtual_ip": p.VirtualIP.String(),
						"endpoint":   p.Endpoint.String(),
						"last_seen":  p.LastSeen.Format("2006-01-02 15:04:05"),
					})
				}
				vpnData["peers"] = peerList
			}
		}
		data["vpn"] = vpnData
	}
	// 对端 VPN 信息（从配置中读取）
	if len(config.GlobalConfig.Peers) > 0 {
		peerVPN := make(map[string]gin.H)
		for _, p := range config.GlobalConfig.Peers {
			if p.VirtualIP != "" {
				key := p.Addr
				peerVPN[key] = gin.H{
					"virtual_ip": p.VirtualIP,
					"vpn_port":   p.VPNPort,
				}
			}
		}
		if len(peerVPN) > 0 {
			data["peer_vpn"] = peerVPN
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data":    data,
	})
}

// getNodeConfig 获取节点配置
func getNodeConfig(c *gin.Context) {
	cfg := config.GlobalConfig
	data := gin.H{
		"node_name": cfg.Node.Name,
		"token":     cfg.Node.Token,
	}
	if cfg.Node.Listen != nil {
		lc := cfg.Node.Listen
		data["listen_port"] = lc.Port
		data["heartbeat_timeout"] = lc.HeartbeatTimeout
		data["max_proxies_per_peer"] = lc.MaxProxiesPerPeer
		data["work_conn_timeout"] = lc.WorkConnTimeout
		data["max_message_size"] = lc.MaxMessageSize
		data["pool_count"] = lc.PoolCount
	}
	if cfg.Node.Dashboard != nil {
		data["dashboard_port"] = cfg.Node.Dashboard.Port
		data["shutdown_timeout"] = cfg.Node.Dashboard.ShutdownTimeout
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data":    data,
	})
}

// updateNodeConfig 更新节点运行时配置 (部分字段可热更新)
func updateNodeConfig(c *gin.Context) {
	var req struct {
		Token             *string `json:"token"`
		HeartbeatTimeout  *int    `json:"heartbeat_timeout"`
		MaxProxiesPerPeer *int    `json:"max_proxies_per_peer"`
		WorkConnTimeout   *int    `json:"work_conn_timeout"`
		PoolCount         *int    `json:"pool_count"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "参数错误"})
		return
	}

	cfg := config.GlobalConfig
	if req.Token != nil {
		cfg.Node.Token = *req.Token
	}
	if cfg.Node.Listen != nil {
		lc := cfg.Node.Listen
		if req.HeartbeatTimeout != nil {
			lc.HeartbeatTimeout = *req.HeartbeatTimeout
		}
		if req.MaxProxiesPerPeer != nil {
			lc.MaxProxiesPerPeer = *req.MaxProxiesPerPeer
		}
		if req.WorkConnTimeout != nil {
			lc.WorkConnTimeout = *req.WorkConnTimeout
		}

		// 如果 pool_count 变更，广播给所有在线对端
		if req.PoolCount != nil && *req.PoolCount != lc.PoolCount {
			lc.PoolCount = *req.PoolCount
			ts := services.GetTunnelService()
			for _, p := range ts.ListPeers() {
				go ts.UpdatePeerPool(p.ConnID, *req.PoolCount)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "配置已更新"})
}

// ---- 远程管理对端 ----

// getPeerConfig 获取对端配置
func getPeerConfig(c *gin.Context) {
	connID, ok := resolvePeer(c)
	if !ok {
		return
	}
	ts := services.GetTunnelService()
	cfg, err := ts.GetPeerConfig(connID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "success", "data": cfg})
}

// addPeerProxy 远程添加对端代理
func addPeerProxy(c *gin.Context) {
	connID, ok := resolvePeer(c)
	if !ok {
		return
	}
	var req tcp.AddProxyData
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "参数错误"})
		return
	}
	if req.Name == "" || req.RemotePort <= 0 || req.LocalPort <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "缺少必要参数: name, remote_port, local_port"})
		return
	}
	if !validIdentifier.MatchString(req.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "非法的代理名格式（仅允许字母/数字/下划线/连字符，长度 1-64）"})
		return
	}
	if req.RemotePort < 1 || req.RemotePort > 65535 || req.LocalPort < 1 || req.LocalPort > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "端口范围非法 (1-65535)"})
		return
	}
	if req.LocalIP == "" {
		req.LocalIP = "127.0.0.1"
	}
	if req.Type == "" {
		req.Type = "tcp"
	}

	ts := services.GetTunnelService()
	if err := ts.AddPeerProxy(connID, &req); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "代理已添加"})
}

// removePeerProxy 远程删除对端代理
func removePeerProxy(c *gin.Context) {
	connID, ok := resolvePeer(c)
	if !ok {
		return
	}
	proxyName := c.Param("proxyName")
	if !validateIdentifier(c, proxyName, "代理名") {
		return
	}
	ts := services.GetTunnelService()
	if err := ts.RemovePeerProxy(connID, proxyName); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "代理已删除"})
}

// updatePeerPool 远程修改对端连接池
func updatePeerPool(c *gin.Context) {
	connID, ok := resolvePeer(c)
	if !ok {
		return
	}
	var req struct {
		PoolCount int `json:"pool_count"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "参数错误"})
		return
	}
	ts := services.GetTunnelService()
	if err := ts.UpdatePeerPool(connID, req.PoolCount); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "连接池已更新"})
}

// getPeerPeers 获取对端的下游节点列表
func getPeerPeers(c *gin.Context) {
	connID, ok := resolvePeer(c)
	if !ok {
		return
	}
	ts := services.GetTunnelService()
	peers, err := ts.GetPeerPeers(connID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "success", "data": peers})
}

// forwardPeerCmd 转发命令给对端的下游节点
func forwardPeerCmd(c *gin.Context) {
	connID, ok := resolvePeer(c)
	if !ok {
		return
	}
	var req tcp.ForwardCmdData
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "参数错误"})
		return
	}
	if req.Cmd == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "缺少 cmd 参数"})
		return
	}
	ts := services.GetTunnelService()
	resp, err := ts.ForwardPeerCmd(connID, &req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": resp.Code, "message": resp.Message, "data": resp.Data})
}
