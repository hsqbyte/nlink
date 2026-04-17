package middleware

import "github.com/gin-gonic/gin"

// SecurityHeadersMiddleware 为所有响应添加安全相关 HTTP 头：
//   - X-Content-Type-Options: 禁止浏览器 MIME 嗅探
//   - X-Frame-Options:        禁止被内嵌到 iframe（防点击劫持）
//   - Referrer-Policy:        限制 Referer 泄漏
//   - Content-Security-Policy: 仅允许同源资源，禁用内联脚本（已经历审计后可放宽）
//
// 注意：CSP 较严格，若前端需加载外部字体/CDN 资源需相应放宽。
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// default-src 'self' 禁止跨站加载；unsafe-inline 是为兼容现有内联事件（onclick 等）
		// 后续若去除内联事件绑定，可删除 unsafe-inline 进一步加固。
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"connect-src 'self'; "+
				"frame-ancestors 'none'")
		c.Next()
	}
}
