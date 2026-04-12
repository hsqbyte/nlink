package handle

import (
	"html/template"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hsqbyte/nlink/src/router"
	"github.com/hsqbyte/nlink/web"
)

var dashboardTmpl *template.Template

func init() {
	dashboardTmpl = template.Must(template.ParseFS(web.TemplateFS(), "dashboard.html"))

	router.Engine.StaticFS("/static", http.FS(web.StaticFS()))
	router.Engine.GET("/dashboard", dashboardPage)
}

func dashboardPage(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := dashboardTmpl.Execute(c.Writer, nil); err != nil {
		c.String(http.StatusInternalServerError, "模板渲染失败: %v", err)
	}
}
