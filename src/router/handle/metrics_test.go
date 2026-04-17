package handle

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hsqbyte/nlink/src/services"
)

func TestPrometheusMetricsOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 初始化最小 TunnelService（不注入 TCP 服务器，只用其统计 API）
	services.InitTunnelService(nil, 30)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/metrics", nil)

	PrometheusMetrics(c)

	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d", w.Code)
	}
	body := w.Body.String()
	must := []string{
		"nlink_up",
		"nlink_uptime_seconds",
		"nlink_peers",
		"nlink_proxies",
		"nlink_connections_total",
		"nlink_active_connections",
		"nlink_bytes_in_total",
		"nlink_bytes_out_total",
		"# TYPE",
		"# HELP",
	}
	for _, m := range must {
		if !strings.Contains(body, m) {
			t.Errorf("metrics 输出缺少 %q", m)
		}
	}

	// Content-Type 正确
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type 错误: %s", ct)
	}
}
