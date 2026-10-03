// Package api 组装 Gin 路由与 HTTP 处理函数。
package api

import (
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"energystorage/internal/optimize"
	"energystorage/internal/service"
	"energystorage/internal/spec"
)

// Server HTTP API。
type Server struct {
	svc  *service.Service
	root fs.FS // 前端静态文件根（web/dist 内容）
}

// NewServer 构造路由引擎。root 为嵌入的前端静态文件系统；nil 时只提供 API。
func NewServer(svc *service.Service, root fs.FS) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Next()
	})

	s := &Server{svc: svc, root: root}

	api := r.Group("/api")
	{
		api.GET("/config", s.getConfig)
		api.POST("/config", s.saveConfig)

		api.GET("/plans", s.listPlans)
		api.GET("/plans/current", s.currentPlan)
		api.GET("/plans/:version", s.getPlan)
		api.POST("/reoptimize", s.reoptimize)

		api.POST("/telemetry", s.ingestTelemetry)
		api.GET("/telemetry", s.listTelemetry)
	}

	if root != nil {
		s.serveStatic(r)
	}
	return r
}

type configRequest struct {
	Station spec.Station `json:"station" binding:"required"`
	Prices  []float64    `json:"prices" binding:"required"`
	PlanDay string       `json:"plan_day" binding:"required"`
}

func (s *Server) getConfig(c *gin.Context) {
	cfg, err := s.svc.GetConfig(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}

func (s *Server) saveConfig(c *gin.Context) {
	var req configRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, &spec.FieldError{
			Message: "请求体解析失败: " + err.Error(),
			Fields:  []string{"body"},
		})
		return
	}
	cfg, v, err := s.svc.Configure(c.Request.Context(), req.Station, req.Prices, req.PlanDay)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"config": cfg, "plan": planView(v)})
}

func (s *Server) listPlans(c *gin.Context) {
	day, ok := requireDay(c)
	if !ok {
		return
	}
	versions, current, err := s.svc.ListVersions(c.Request.Context(), day)
	if err != nil {
		writeError(c, err)
		return
	}
	views := make([]gin.H, 0, len(versions))
	for _, v := range versions {
		views = append(views, summaryView(v))
	}
	c.JSON(http.StatusOK, gin.H{"versions": views, "current_version": current})
}

func (s *Server) currentPlan(c *gin.Context) {
	day, ok := requireDay(c)
	if !ok {
		return
	}
	v, err := s.svc.CurrentPlan(c.Request.Context(), day)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, planView(v))
}

func (s *Server) getPlan(c *gin.Context) {
	day, ok := requireDay(c)
	if !ok {
		return
	}
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil {
		c.JSON(http.StatusBadRequest, &spec.FieldError{Message: "version 必须为整数", Fields: []string{"version"}})
		return
	}
	v, err := s.svc.PlanByVersion(c.Request.Context(), day, version)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, planView(v))
}

type reoptRequest struct {
	PlanDay string `json:"plan_day" binding:"required"`
	Reason  string `json:"reason"`
}

func (s *Server) reoptimize(c *gin.Context) {
	var req reoptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, &spec.FieldError{Message: err.Error(), Fields: []string{"body"}})
		return
	}
	v, err := s.svc.ManualReoptimize(c.Request.Context(), req.PlanDay, req.Reason)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, planView(v))
}

type telemetryRequest struct {
	ID          string    `json:"id" binding:"required"`
	Timestamp   time.Time `json:"timestamp" binding:"required"`
	Soc         float64   `json:"soc"`
	ChargeMW    float64   `json:"charge_mw"`
	DischargeMW float64   `json:"discharge_mw"`
}

func (s *Server) ingestTelemetry(c *gin.Context) {
	var req telemetryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, &spec.FieldError{Message: err.Error(), Fields: []string{"body"}})
		return
	}
	res, err := s.svc.IngestTelemetry(c.Request.Context(), service.Telemetry{
		ID:          req.ID,
		Timestamp:   req.Timestamp,
		Soc:         req.Soc,
		ChargeMW:    req.ChargeMW,
		DischargeMW: req.DischargeMW,
	})
	if err != nil {
		writeError(c, err)
		return
	}
	status := http.StatusOK
	if res.Warning != nil {
		status = http.StatusAccepted
	}
	c.JSON(status, res)
}

func (s *Server) listTelemetry(c *gin.Context) {
	day, ok := requireDay(c)
	if !ok {
		return
	}
	tels, err := s.svc.ListTelemetry(c.Request.Context(), day)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"telemetry": tels})
}

func requireDay(c *gin.Context) (string, bool) {
	day := c.Query("day")
	if _, err := time.Parse("2006-01-02", day); err != nil {
		c.JSON(http.StatusBadRequest, &spec.FieldError{
			Message: "缺少或非法的 day 查询参数（格式 YYYY-MM-DD）",
			Fields:  []string{"day"},
		})
		return "", false
	}
	return day, true
}

func planView(v *service.PlanVersion) gin.H {
	return gin.H{
		"id":                v.ID,
		"plan_day":          v.PlanDay,
		"version":           v.Version,
		"is_current":        v.IsCurrent,
		"trigger_reason":    v.TriggerReason,
		"start_period":      v.StartPeriod,
		"start_energy_mwh":  v.StartEnergyMWh,
		"charge_mw":         v.FullCharge(),
		"discharge_mw":      v.FullDischarge(),
		"soc":               v.FullSoc(),
		"tail_charge_mw":    v.ChargeMW,
		"tail_discharge_mw": v.DischargeMW,
		"tail_soc":          v.Soc,
		"profit_tail_yuan":  v.ProfitYuan,
		"grid_points":       v.GridPoints,
		"grid_gap_mwh":      v.GridGapMWh,
		"error_bound_yuan":  v.ErrorBoundYuan,
		"created_at":        v.CreatedAt,
	}
}

func summaryView(v *service.PlanVersion) gin.H {
	return gin.H{
		"id":               v.ID,
		"version":          v.Version,
		"is_current":       v.IsCurrent,
		"trigger_reason":   v.TriggerReason,
		"start_period":     v.StartPeriod,
		"profit_tail_yuan": v.ProfitYuan,
		"error_bound_yuan": v.ErrorBoundYuan,
		"created_at":       v.CreatedAt,
	}
}

func writeError(c *gin.Context, err error) {
	var fe *spec.FieldError
	if errors.As(err, &fe) {
		c.JSON(http.StatusBadRequest, fe)
		return
	}
	var nf *service.ErrNotFound
	if errors.As(err, &nf) {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	var cf *service.ErrConflict
	if errors.As(err, &cf) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	if errors.Is(err, optimize.ErrInfeasible) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
