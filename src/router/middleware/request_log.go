package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/fastgox/utils/logger"
	"github.com/gin-gonic/gin"
)

const RequestIDHeader = "X-Request-ID"
const RequestIDKey = "request_id"

// RequestID 为每个请求注入唯一 ID（优先使用请求头里带来的）
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader(RequestIDHeader)
		if rid == "" {
			rid = newRequestID()
		}
		c.Set(RequestIDKey, rid)
		c.Writer.Header().Set(RequestIDHeader, rid)
		c.Next()
	}
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// AccessLog 结构化访问日志：method path status latency ip req_id user
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latency := time.Since(start)

		rid, _ := c.Get(RequestIDKey)
		user, _ := c.Get("user")
		if user == nil {
			user = "-"
		}
		logger.Info("[HTTP] method=%s path=%s status=%d latency=%s ip=%s req_id=%v user=%v",
			c.Request.Method,
			c.Request.URL.Path,
			c.Writer.Status(),
			latency,
			c.ClientIP(),
			rid,
			user,
		)
	}
}
