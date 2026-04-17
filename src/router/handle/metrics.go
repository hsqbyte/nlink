package handle

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hsqbyte/nlink/src/router"
	"github.com/hsqbyte/nlink/src/services"
)

func init() {
	// /metrics 不走认证，方便 Prometheus 抓取；部署时通过网络策略限制访问源
	router.Engine.GET("/metrics", PrometheusMetrics)
}

// PrometheusMetrics 输出 Prometheus 纯文本格式指标
//
// 不依赖 prometheus/client_golang，自己生成 text/plain;version=0.0.4。
// 满足大多数场景；需要 histogram/summary 时再引入客户端库。
func PrometheusMetrics(c *gin.Context) {
	ts := services.GetTunnelService()
	if ts == nil {
		c.String(http.StatusServiceUnavailable, "tunnel service not ready")
		return
	}

	stats := ts.ServerStats()
	proxies := ts.ListProxies()
	peers := ts.ListPeers()

	var b strings.Builder
	b.Grow(2048)

	writeGauge(&b, "nlink_up", "Whether the nlink node is up (always 1)", 1)
	writeGauge(&b, "nlink_uptime_seconds", "Uptime in seconds since start", float64(stats.Uptime))
	writeGauge(&b, "nlink_peers", "Number of connected peers", float64(stats.PeerCount))
	writeGauge(&b, "nlink_proxies", "Number of registered proxies", float64(stats.ProxyCount))
	writeCounter(&b, "nlink_connections_total", "Total accepted connections across all proxies", float64(stats.TotalConns))
	writeGauge(&b, "nlink_active_connections", "Currently active connections across all proxies", float64(stats.ActiveConns))
	writeCounter(&b, "nlink_bytes_in_total", "Bytes received by proxies", float64(stats.BytesIn))
	writeCounter(&b, "nlink_bytes_out_total", "Bytes sent by proxies", float64(stats.BytesOut))

	// per-proxy metrics
	b.WriteString("# HELP nlink_proxy_connections_total Total connections per proxy\n")
	b.WriteString("# TYPE nlink_proxy_connections_total counter\n")
	for _, p := range proxies {
		fmt.Fprintf(&b, "nlink_proxy_connections_total{proxy=%q,peer=%q} %d\n",
			p.Name, p.PeerName, p.TotalConns)
	}
	b.WriteString("# HELP nlink_proxy_active_connections Active connections per proxy\n")
	b.WriteString("# TYPE nlink_proxy_active_connections gauge\n")
	for _, p := range proxies {
		fmt.Fprintf(&b, "nlink_proxy_active_connections{proxy=%q,peer=%q} %d\n",
			p.Name, p.PeerName, p.ActiveConns)
	}
	b.WriteString("# HELP nlink_proxy_bytes_in_total Bytes received per proxy\n")
	b.WriteString("# TYPE nlink_proxy_bytes_in_total counter\n")
	for _, p := range proxies {
		fmt.Fprintf(&b, "nlink_proxy_bytes_in_total{proxy=%q,peer=%q} %d\n",
			p.Name, p.PeerName, p.BytesIn)
	}
	b.WriteString("# HELP nlink_proxy_bytes_out_total Bytes sent per proxy\n")
	b.WriteString("# TYPE nlink_proxy_bytes_out_total counter\n")
	for _, p := range proxies {
		fmt.Fprintf(&b, "nlink_proxy_bytes_out_total{proxy=%q,peer=%q} %d\n",
			p.Name, p.PeerName, p.BytesOut)
	}
	b.WriteString("# HELP nlink_proxy_pool_hits_total Pool hits per proxy\n")
	b.WriteString("# TYPE nlink_proxy_pool_hits_total counter\n")
	for _, p := range proxies {
		fmt.Fprintf(&b, "nlink_proxy_pool_hits_total{proxy=%q,peer=%q} %d\n",
			p.Name, p.PeerName, p.PoolHits)
	}
	b.WriteString("# HELP nlink_proxy_ondemand_hits_total On-demand work conn hits per proxy\n")
	b.WriteString("# TYPE nlink_proxy_ondemand_hits_total counter\n")
	for _, p := range proxies {
		fmt.Fprintf(&b, "nlink_proxy_ondemand_hits_total{proxy=%q,peer=%q} %d\n",
			p.Name, p.PeerName, p.OnDemandHits)
	}

	// per-peer latency
	b.WriteString("# HELP nlink_peer_latency_ms Latency (ms) to each connected peer\n")
	b.WriteString("# TYPE nlink_peer_latency_ms gauge\n")
	for _, p := range peers {
		fmt.Fprintf(&b, "nlink_peer_latency_ms{peer=%q,conn_id=%q} %d\n",
			p.Name, p.ConnID, p.Latency)
	}

	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	c.String(http.StatusOK, b.String())
}

func writeGauge(b *strings.Builder, name, help string, v float64) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s gauge\n%s %g\n", name, help, name, name, v)
}

func writeCounter(b *strings.Builder, name, help string, v float64) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s counter\n%s %g\n", name, help, name, name, v)
}
