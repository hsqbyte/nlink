package router

import (
	"github.com/gin-gonic/gin"
	"github.com/hsqbyte/nlink/src/core/tcp"
	"github.com/hsqbyte/nlink/src/router/middleware"
)

var (
	// HTTP
	Engine    *gin.Engine
	APIRouter *gin.RouterGroup

	// TCP
	TCPRouter *tcp.Router
)

func init() {
	Engine = gin.New()
	Engine.Use(gin.Logger(), middleware.RecoveryMiddleware(), middleware.SecurityHeadersMiddleware(), middleware.CORSMiddleware())

	// 健康检查（不需要认证）
	Engine.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// 登录/登出（不需要认证）
	Engine.POST("/api/v1/login", middleware.HandleLogin)
	Engine.POST("/api/v1/logout", middleware.HandleLogout)

	APIRouter = Engine.Group("/api/v1")
	APIRouter.Use(middleware.AuthMiddleware())

	// TCP 路由
	TCPRouter = tcp.NewRouter()
	TCPRouter.Use(tcp.Recovery(), tcp.Logger())
}
