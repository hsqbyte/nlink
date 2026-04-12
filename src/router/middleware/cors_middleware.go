package middleware

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// CORSMiddleware 创建CORS中间件
func CORSMiddleware() gin.HandlerFunc {
	// 创建基础CORS配置
	corsConfig := cors.Config{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders: []string{
			"Origin",
			"Content-Type",
			"Content-Length",
			"Accept",
			"Authorization",
			"X-Requested-With",
		},
		ExposeHeaders:    []string{"Content-Length", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           86400, // 24小时
	}

	return cors.New(corsConfig)
}
