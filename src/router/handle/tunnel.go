package handle

import (
	"crypto/subtle"
	"fmt"

	"github.com/hsqbyte/nlink/src/core/config"
	"github.com/hsqbyte/nlink/src/core/tcp"
	"github.com/hsqbyte/nlink/src/router"
	"github.com/hsqbyte/nlink/src/services"
)

func init() {
	router.TCPRouter.Handle("auth", handleAuth)
	router.TCPRouter.Handle("new_proxy", handleNewProxy)

	// 对端远程指令回复 — 转发到 TunnelService
	peerResponseHandler := func(ctx *tcp.Context) error {
		ts := services.GetTunnelService()
		if ts == nil {
			return nil
		}
		var payload struct {
			Code    int         `json:"code"`
			Message string      `json:"message"`
			Data    interface{} `json:"data,omitempty"`
		}
		if err := ctx.Bind(&payload); err != nil {
			return nil
		}
		resp := &tcp.Response{
			Cmd:     ctx.Msg.Cmd,
			Seq:     ctx.Msg.Seq,
			Code:    payload.Code,
			Message: payload.Message,
			Data:    payload.Data,
		}
		ts.HandlePeerResponse(ctx.ConnID, resp)
		return nil
	}
	router.TCPRouter.Handle("get_config", peerResponseHandler)
	router.TCPRouter.Handle("add_proxy", peerResponseHandler)
	router.TCPRouter.Handle("remove_proxy", peerResponseHandler)
	router.TCPRouter.Handle("update_pool", peerResponseHandler)
	router.TCPRouter.Handle("get_clients", peerResponseHandler)
	router.TCPRouter.Handle("forward_cmd", peerResponseHandler)
}

// handleAuth 认证处理
func handleAuth(ctx *tcp.Context) error {
	var data tcp.AuthData
	if err := ctx.Bind(&data); err != nil {
		return ctx.Error(400, "参数解析失败")
	}

	expected := config.GlobalConfig.Node.Token
	if subtle.ConstantTimeCompare([]byte(data.Token), []byte(expected)) != 1 {
		return ctx.Error(401, "认证失败: token无效")
	}

	if data.Name == "" {
		return ctx.Error(400, "节点名称不能为空")
	}

	// 检查名称唯一性
	ts := services.GetTunnelService()
	if ts.IsPeerNameTaken(data.Name) {
		return ctx.Error(409, "节点名称已被占用: "+data.Name)
	}

	// 注册名称映射
	ts.RegisterPeerName(ctx.ConnID, data.Name)

	ctx.Set("authenticated", true)
	return ctx.Reply(map[string]interface{}{"status": "ok", "conn_id": ctx.ConnID, "node_name": config.GlobalConfig.Node.Name})
}

// handleNewProxy 注册代理
func handleNewProxy(ctx *tcp.Context) error {
	var data tcp.NewProxyData
	if err := ctx.Bind(&data); err != nil {
		return ctx.Error(400, "参数解析失败")
	}

	if data.Name == "" || data.RemotePort <= 0 {
		return ctx.Error(400, "缺少必要参数: name, remote_port")
	}

	// 检查每对端最大代理数限制
	cfg := config.GlobalConfig
	if cfg.Node.Listen != nil && cfg.Node.Listen.MaxProxiesPerPeer > 0 {
		ts := services.GetTunnelService()
		if ts.PeerProxyCount(ctx.ConnID) >= cfg.Node.Listen.MaxProxiesPerPeer {
			return ctx.Reply(tcp.NewProxyResp{
				Name:  data.Name,
				OK:    false,
				Error: fmt.Sprintf("已达最大代理数限制: %d", cfg.Node.Listen.MaxProxiesPerPeer),
			})
		}
	}

	ts := services.GetTunnelService()
	if err := ts.RegisterProxy(ctx.ConnID, data.Name, data.RemotePort); err != nil {
		return ctx.Reply(tcp.NewProxyResp{
			Name:  data.Name,
			OK:    false,
			Error: err.Error(),
		})
	}

	return ctx.Reply(tcp.NewProxyResp{
		Name:       data.Name,
		RemotePort: data.RemotePort,
		OK:         true,
	})
}
