package middleware

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/hsqbyte/nlink/src/core/config"
)

var (
	sessions   = make(map[string]bool)
	sessionsMu sync.RWMutex
)

// AuthMiddleware 控制面板认证中间件
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := config.GlobalConfig.Node.Dashboard
		if cfg == nil || !cfg.AuthRequired() {
			c.Next()
			return
		}

		token, err := c.Cookie("nlink_token")
		if err != nil || !isValidSession(token) {
			path := c.Request.URL.Path
			// API 请求返回 401
			if len(path) > 4 && path[:5] == "/api/" {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "未登录"})
				return
			}
			// 页面请求重定向到登录页
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}
		c.Next()
	}
}

// HandleLogin 处理登录请求
func HandleLogin(c *gin.Context) {
	cfg := config.GlobalConfig.Node.Dashboard
	if cfg == nil || !cfg.AuthRequired() {
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}

	var req struct {
		Username string `json:"username" form:"username"`
		Password string `json:"password" form:"password"`
	}
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "参数错误"})
		return
	}

	usernameMatch := subtle.ConstantTimeCompare([]byte(req.Username), []byte(cfg.Username)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(req.Password), []byte(cfg.Password)) == 1

	if !usernameMatch || !passwordMatch {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "用户名或密码错误"})
		return
	}

	token := generateSessionToken()
	addSession(token)

	c.SetCookie("nlink_token", token, 86400, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "登录成功"})
}

// HandleLogout 处理登出请求
func HandleLogout(c *gin.Context) {
	token, _ := c.Cookie("nlink_token")
	if token != "" {
		removeSession(token)
	}
	c.SetCookie("nlink_token", "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "已登出"})
}

func generateSessionToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// fallback - should never happen
		return hex.EncodeToString(sha256.New().Sum(b))
	}
	return hex.EncodeToString(b)
}

func isValidSession(token string) bool {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	return sessions[token]
}

func addSession(token string) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	sessions[token] = true
}

func removeSession(token string) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	delete(sessions, token)
}
