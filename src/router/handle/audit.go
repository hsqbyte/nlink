package handle

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/hsqbyte/nlink/src/router"
	"github.com/hsqbyte/nlink/src/services/audit"
)

func init() {
	router.APIRouter.GET("/audit", listAudit)
}

// listAudit 查询审计日志（仅 admin）
// query: date=YYYY-MM-DD user= path= method= limit= offset=
func listAudit(c *gin.Context) {
	if role, _ := c.Get("role"); role != nil && role != "" && role != "admin" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "权限不足"})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	records, total, err := audit.Query(audit.QueryFilter{
		Date:   c.Query("date"),
		User:   c.Query("user"),
		Path:   c.Query("path"),
		Method: c.Query("method"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "ok",
		"data": gin.H{
			"records": records,
			"total":   total,
		},
	})
}
