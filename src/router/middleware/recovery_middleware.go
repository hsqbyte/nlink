package middleware

import (
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/fastgox/utils/logger"
	"github.com/gin-gonic/gin"
)

// RecoveryMiddleware 自定义panic恢复中间件，返回统一JSON格式
func RecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// 检查是否为断开的连接（客户端主动断开，无需报错）
				var brokenPipe bool
				if ne, ok := err.(*net.OpError); ok {
					if se, ok := ne.Err.(*os.SyscallError); ok {
						if strings.Contains(strings.ToLower(se.Error()), "broken pipe") ||
							strings.Contains(strings.ToLower(se.Error()), "connection reset by peer") {
							brokenPipe = true
						}
					}
				}

				if brokenPipe {
					logger.Error("[Recovery] broken pipe: %s %s", c.Request.Method, c.Request.URL.Path)
					c.Abort()
					return
				}

				logger.Error("[Recovery] panic recovered: %v | %s %s", err, c.Request.Method, c.Request.URL.Path)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"code":    500,
					"message": "服务器内部错误",
				})
			}
		}()
		c.Next()
	}
}
