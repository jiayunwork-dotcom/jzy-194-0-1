// Package api 提供 HTTP/JSON 接口（Gin），并把前端静态文件挂在同一路由下。
package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"essplanner/internal/core"
	"essplanner/internal/service"
)

func NewRouter(svc *service.Service, staticDir string) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	api := r.Group("/api")
	{
		api.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

		api.GET("/station", func(c *gin.Context) {
			p, err := svc.GetStation(c.Request.Context())
			respond(c, p, err)
		})
		api.PUT("/station", func(c *gin.Context) {
			var p core.StationParams
			if err := c.ShouldBindJSON(&p); err != nil {
				respond(c, nil, core.ValidationError{{Field: "body", Message: "JSON 解析失败: " + err.Error()}})
				return
			}
			respond(c, p, svc.PutStation(c.Request.Context(), &p))
		})

		api.GET("/prices/:date", func(c *gin.Context) {
			prices, err := svc.GetPrices(c.Request.Context(), c.Param("date"))
			respond(c, gin.H{"date": c.Param("date"), "prices": prices}, err)
		})
		api.PUT("/prices/:date", func(c *gin.Context) {
			var body struct {
				Prices []float64 `json:"prices"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				respond(c, nil, core.ValidationError{{Field: "prices", Message: "JSON 解析失败: " + err.Error()}})
				return
			}
			err := svc.PutPrices(c.Request.Context(), c.Param("date"), body.Prices)
			respond(c, gin.H{"date": c.Param("date"), "count": len(body.Prices)}, err)
		})

		api.POST("/plans/:date/optimize", func(c *gin.Context) {
			var body struct {
				InitialSoc float64 `json:"initial_soc"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				respond(c, nil, core.ValidationError{{Field: "initial_soc", Message: "请求体须包含 initial_soc"}})
				return
			}
			plan, err := svc.OptimizeDayAhead(c.Request.Context(), c.Param("date"), body.InitialSoc)
			respond(c, plan, err)
		})
		api.GET("/plans/:date", func(c *gin.Context) {
			plan, err := svc.GetCurrentPlan(c.Request.Context(), c.Param("date"))
			respond(c, plan, err)
		})
		api.GET("/plans/:date/versions", func(c *gin.Context) {
			metas, err := svc.ListPlanVersions(c.Request.Context(), c.Param("date"))
			respond(c, metas, err)
		})
		api.GET("/plans/:date/versions/:version", func(c *gin.Context) {
			v, _ := strconv.Atoi(c.Param("version"))
			plan, err := svc.GetPlanVersion(c.Request.Context(), c.Param("date"), v)
			respond(c, plan, err)
		})

		api.POST("/plans/:date/telemetry", func(c *gin.Context) {
			var in service.TelemetryInput
			if err := c.ShouldBindJSON(&in); err != nil {
				respond(c, nil, core.ValidationError{{Field: "body", Message: "JSON 解析失败: " + err.Error()}})
				return
			}
			res, err := svc.SubmitTelemetry(c.Request.Context(), c.Param("date"), in)
			respond(c, res, err)
		})
		api.GET("/plans/:date/telemetry", func(c *gin.Context) {
			list, err := svc.ListTelemetry(c.Request.Context(), c.Param("date"))
			respond(c, list, err)
		})
	}

	// 静态文件 + SPA 回退
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
			return
		}
		if staticDir != "" {
			fp := filepath.Join(staticDir, filepath.Clean("/"+p))
			if st, err := os.Stat(fp); err == nil && !st.IsDir() {
				c.File(fp)
				return
			}
			if _, err := os.Stat(filepath.Join(staticDir, "index.html")); err == nil {
				c.File(filepath.Join(staticDir, "index.html"))
				return
			}
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})
	return r
}

// respond 统一错误映射：校验错误 400（含字段）、未找到 404、不可行 422。
func respond(c *gin.Context, data interface{}, err error) {
	if err == nil {
		c.JSON(http.StatusOK, data)
		return
	}
	var verrs core.ValidationError
	switch {
	case errors.As(err, &verrs):
		c.JSON(http.StatusBadRequest, gin.H{"errors": verrs})
	case service.IsNotFound(err):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrInfeasible):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
