package handle

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hsqbyte/nlink/src/core/config"
	"github.com/hsqbyte/nlink/src/core/vpn"
	"github.com/hsqbyte/nlink/src/router"
	"github.com/hsqbyte/nlink/src/services"
)

func init() {
	router.APIRouter.GET("/stream", serverStatsStream)
}

// serverStatsStream SSE 实时推送 /stats 数据，默认 2s 一帧
func serverStatsStream(c *gin.Context) {
	// SSE 标准头
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no") // nginx 禁缓冲

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.String(http.StatusInternalServerError, "streaming unsupported")
		return
	}

	ctx := c.Request.Context()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// 立即推一次，避免客户端等 2s
	if err := writeSSEStats(c, flusher); err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := writeSSEStats(c, flusher); err != nil {
				return
			}
		}
	}
}

func writeSSEStats(c *gin.Context, flusher http.Flusher) error {
	ts := services.GetTunnelService()
	if ts == nil {
		return nil
	}
	payload := gin.H{
		"node_name": config.GlobalConfig.Node.Name,
		"server":    ts.ServerStats(),
		"proxies":   ts.ListProxies(),
		"peers":     ts.ListPeers(),
	}
	vpnCfg := config.GlobalConfig.Node.VPN
	if vpnCfg != nil && vpnCfg.IsEnabled() {
		vpnData := gin.H{
			"enabled":    true,
			"virtual_ip": vpnCfg.VirtualIP,
		}
		if engine := vpn.GetGlobalEngine(); engine != nil {
			if addr := engine.CachedPublicAddr(); addr != "" {
				vpnData["public_addr"] = addr
			}
		}
		payload["vpn"] = vpnData
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := c.Writer.WriteString("data: "); err != nil {
		return err
	}
	if _, err := c.Writer.Write(buf); err != nil {
		return err
	}
	if _, err := c.Writer.WriteString("\n\n"); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
