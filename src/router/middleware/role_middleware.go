package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequireAdmin 仅 admin 可执行的写操作 (POST/PUT/DELETE/PATCH)
// viewer 角色对写操作返回 403；GET / HEAD 一律放行。
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		m := c.Request.Method
		if m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions {
			c.Next()
			return
		}
		role, _ := c.Get("role")
		// 未启用认证 / 未登录场景下 role 为空，按现有 AuthMiddleware 行为放行
		if role == nil || role == "" {
			c.Next()
			return
		}
		if r, ok := role.(string); ok && r == "admin" {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code":    403,
			"message": "权限不足：仅 admin 可执行此操作",
		})
	}
}
