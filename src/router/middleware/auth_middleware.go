package middleware

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/fastgox/utils/logger"
	"github.com/gin-gonic/gin"
	"github.com/hsqbyte/nlink/src/core/config"
)

// sessionTTL 会话有效期（与 cookie 的 maxAge 保持一致）
const sessionTTL = 24 * time.Hour

// session 保存单个登录会话的过期时间
type session struct {
	expiresAt time.Time
	username  string
	role      string
}

// SessionUsername 返回 token 对应的用户名（过期或不存在返回空）
func SessionUsername(token string) string {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	s, ok := sessions[token]
	if !ok || time.Now().After(s.expiresAt) {
		return ""
	}
	return s.username
}

// SessionRole 返回 token 对应的角色
func SessionRole(token string) string {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	s, ok := sessions[token]
	if !ok || time.Now().After(s.expiresAt) {
		return ""
	}
	return s.role
}

var (
	sessions   = make(map[string]session)
	sessionsMu sync.RWMutex
)

func init() {
	go sessionJanitor()
}

// sessionJanitor 定期清理过期 session 防止 map 无界增长
func sessionJanitor() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cleanupExpiredSessions()
	}
}

func cleanupExpiredSessions() {
	now := time.Now()
	removed := 0
	sessionsMu.Lock()
	for k, s := range sessions {
		if now.After(s.expiresAt) {
			delete(sessions, k)
			removed++
		}
	}
	remaining := len(sessions)
	sessionsMu.Unlock()
	if removed > 0 {
		logger.Info("[Auth] 已清理 %d 个过期 session，剩余 %d", removed, remaining)
	}
}

// AuthMiddleware 控制面板认证中间件
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := config.GlobalConfig.Node.Dashboard
		if cfg == nil || !cfg.AuthRequired() {
			c.Next()
			return
		}

		token, err := c.Cookie("nlink_token")
		if err != nil || !IsValidSession(token) {
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
		if u := SessionUsername(token); u != "" {
			c.Set("user", u)
		}
		if r := SessionRole(token); r != "" {
			c.Set("role", r)
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

	ip := c.ClientIP()
	if locked, retry := loginLimiterCheck(ip); locked {
		c.Header("Retry-After", retryAfterSeconds(retry))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"code":    429,
			"message": "登录尝试过于频繁，请稍后再试",
		})
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

	role := ""
	if usernameMatch && passwordMatch {
		role = "admin"
	} else {
		// fallback: 多用户表
		role = cfg.LookupUser(req.Username, req.Password)
	}

	if role == "" {
		locked, retry := loginLimiterRecordFail(ip)
		if locked {
			logger.Warn("[Auth] IP %s 登录失败次数超限，已锁定 %s", ip, retry)
			c.Header("Retry-After", retryAfterSeconds(retry))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "登录尝试过于频繁，请稍后再试",
			})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "用户名或密码错误"})
		return
	}

	loginLimiterReset(ip)
	token := generateSessionToken()
	addSession(token, req.Username, role)

	secure := cfg.TLSEnabled()
	// SameSite=Strict 可防御大部分 CSRF 攻击：跨站发起的请求不会携带此 cookie
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie("nlink_token", token, int(sessionTTL.Seconds()), "/", "", secure, true)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "登录成功"})
}

// HandleLogout 处理登出请求
func HandleLogout(c *gin.Context) {
	token, _ := c.Cookie("nlink_token")
	if token != "" {
		removeSession(token)
	}
	c.SetSameSite(http.SameSiteStrictMode)
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

// retryAfterSeconds 将 duration 转换成 HTTP Retry-After 头部（秒，最小 1）
func retryAfterSeconds(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}

// IsValidSession 返回 token 是否对应有效且未过期的会话
func IsValidSession(token string) bool {
	if token == "" {
		return false
	}
	sessionsMu.RLock()
	s, ok := sessions[token]
	sessionsMu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(s.expiresAt) {
		// 惰性删除
		sessionsMu.Lock()
		delete(sessions, token)
		sessionsMu.Unlock()
		return false
	}
	return true
}

func addSession(token, username, role string) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	sessions[token] = session{expiresAt: time.Now().Add(sessionTTL), username: username, role: role}
}

func removeSession(token string) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	delete(sessions, token)
}
