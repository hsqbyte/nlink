package handle

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hsqbyte/nlink/src/core/config"
	"github.com/hsqbyte/nlink/src/router"
)

func init() {
	router.APIRouter.GET("/config", exportConfig)
	router.APIRouter.POST("/config", importConfig)
}

// exportConfig GET /api/v1/config — 返回当前配置 YAML
// 查询参数 redact=1 时屏蔽敏感字段 (token/password/metrics_token)
func exportConfig(c *gin.Context) {
	data, err := config.ExportYAML()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.Header("Content-Type", "application/x-yaml; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="nlink.yaml"`)
	c.Data(http.StatusOK, "application/x-yaml", data)
}

// importConfig POST /api/v1/config — 接受 YAML 并应用（验证 → 写文件 → ApplyReload）
// body: 纯 YAML 文本
func importConfig(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "读取请求体失败: " + err.Error()})
		return
	}
	if len(body) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "请求体为空"})
		return
	}
	newCfg, err := config.ParseAndValidate(body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "校验失败: " + err.Error()})
		return
	}
	if err := config.SaveConfigFile(body); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "写入失败: " + err.Error()})
		return
	}
	config.ApplyReload(newCfg)
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "配置已写入并热加载 (结构性字段可能需重启)",
	})
}
