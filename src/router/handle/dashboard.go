package handle

import (
	"html/template"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hsqbyte/nlink/src/core/config"
	"github.com/hsqbyte/nlink/src/router"
	"github.com/hsqbyte/nlink/src/router/middleware"
	"github.com/hsqbyte/nlink/web"
)

var dashboardTmpl *template.Template
var loginTmpl *template.Template

func init() {
	dashboardTmpl = template.Must(template.ParseFS(web.TemplateFS(), "dashboard.html"))
	loginTmpl = template.Must(template.ParseFS(web.TemplateFS(), "login.html"))

	router.Engine.StaticFS("/static", http.FS(web.StaticFS()))
	router.Engine.GET("/login", loginPage)
	router.Engine.GET("/dashboard", middleware.AuthMiddleware(), dashboardPage)
	router.Engine.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/dashboard")
	})
}

func loginPage(c *gin.Context) {
	cfg := config.GlobalConfig.Node.Dashboard
	// 如果不需要认证，直接跳转 dashboard
	if cfg == nil || !cfg.AuthRequired() {
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}
	// 已登录则跳转 dashboard
	token, err := c.Cookie("nlink_token")
	if err == nil && token != "" {
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := loginTmpl.Execute(c.Writer, nil); err != nil {
		c.String(http.StatusInternalServerError, "模板渲染失败: %v", err)
	}
}

func dashboardPage(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := dashboardTmpl.Execute(c.Writer, nil); err != nil {
		c.String(http.StatusInternalServerError, "模板渲染失败: %v", err)
	}
}
