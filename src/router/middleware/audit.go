package middleware

import (
	"bytes"
	"io"
	"strings"

	"github.com/fastgox/utils/logger"
	"github.com/gin-gonic/gin"
)

// Audit 记录敏感操作日志（POST/PUT/DELETE）
// 记录: who (user), what (method+path), ip, req_id, status, body 前 200 字节
func Audit() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method != "POST" && method != "PUT" && method != "DELETE" && method != "PATCH" {
			c.Next()
			return
		}
		// 不记录登录请求的 body（避免泄露密码）
		isLogin := strings.HasSuffix(c.Request.URL.Path, "/login")

		var bodySnippet string
		if !isLogin && c.Request.Body != nil {
			raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1024))
			if err == nil {
				c.Request.Body = io.NopCloser(bytes.NewReader(raw))
				if len(raw) > 200 {
					bodySnippet = string(raw[:200]) + "...(" + itoa(len(raw)) + "B)"
				} else {
					bodySnippet = string(raw)
				}
			}
		}

		c.Next()

		rid, _ := c.Get(RequestIDKey)
		user, _ := c.Get("user")
		if user == nil {
			user = "anon"
		}
		logger.Info("[Audit] user=%v ip=%s method=%s path=%s status=%d req_id=%v body=%q",
			user, c.ClientIP(), method, c.Request.URL.Path, c.Writer.Status(), rid, bodySnippet)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
