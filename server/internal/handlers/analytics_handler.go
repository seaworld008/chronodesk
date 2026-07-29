package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gongdan-system/internal/middleware"
	"gongdan-system/internal/services"
)

// AnalyticsHandler 分析统计处理器
type AnalyticsHandler struct {
	analyticsService *services.AnalyticsService
	response         *middleware.ResponseHelper
	appVersion       string
}

// NewAnalyticsHandler 创建分析处理器
func NewAnalyticsHandler(db *gorm.DB, appVersion ...string) *AnalyticsHandler {
	version := "1.0.0"
	if len(appVersion) > 0 && appVersion[0] != "" {
		version = appVersion[0]
	}
	return &AnalyticsHandler{
		analyticsService: services.NewAnalyticsService(db),
		response:         middleware.NewResponseHelper(),
		appVersion:       version,
	}
}

// GetSystemStats 获取系统运行状态
// @Summary 获取系统运行状态
// @Description 获取系统运行状态，包括内存、CPU、GC等信息
// @Tags 系统监控
// @Security ApiKeyAuth
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/analytics/system [get]
func (h *AnalyticsHandler) GetSystemStats(c *gin.Context) {
	stats, err := h.analyticsService.GetSystemStats()
	if err != nil {
		h.response.InternalServerError(c, "获取系统统计失败: "+err.Error())
		return
	}

	h.response.Success(c, stats, "获取系统统计成功")
}

// GetBusinessStats 获取业务数据统计
// @Summary 获取业务数据统计
// @Description 获取工单、用户、活动等业务数据统计
// @Tags 系统监控
// @Security ApiKeyAuth
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/analytics/business [get]
func (h *AnalyticsHandler) GetBusinessStats(c *gin.Context) {
	stats, err := h.analyticsService.GetBusinessStats(c.Request.Context())
	if err != nil {
		h.response.InternalServerError(c, "获取业务统计失败: "+err.Error())
		return
	}

	h.response.Success(c, stats, "获取业务统计成功")
}

// GetDashboardStats 获取仪表板综合统计
// @Summary 获取仪表板综合统计
// @Description 获取仪表板所需的综合统计信息
// @Tags 系统监控
// @Security ApiKeyAuth
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/analytics/dashboard [get]
func (h *AnalyticsHandler) GetDashboardStats(c *gin.Context) {
	// 获取系统统计
	systemStats, err := h.analyticsService.GetSystemStats()
	if err != nil {
		h.response.InternalServerError(c, "获取系统统计失败: "+err.Error())
		return
	}

	// 获取业务统计
	businessStats, err := h.analyticsService.GetBusinessStats(c.Request.Context())
	if err != nil {
		h.response.InternalServerError(c, "获取业务统计失败: "+err.Error())
		return
	}

	// 获取最近7天的趋势数据
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -7)

	timeRangeStats, err := h.analyticsService.GetTimeRangeStats(c.Request.Context(), startDate, endDate)
	if err != nil {
		h.response.InternalServerError(c, "获取趋势数据失败: "+err.Error())
		return
	}

	dashboardData := gin.H{
		"system_stats":     systemStats,
		"business_stats":   businessStats,
		"time_range_stats": timeRangeStats,
		"generated_at":     time.Now(),
	}

	h.response.Success(c, dashboardData, "获取仪表板统计成功")
}

// GetTimeRangeStats 获取指定时间范围统计
// @Summary 获取指定时间范围统计
// @Description 获取指定时间范围内的趋势统计数据
// @Tags 系统监控
// @Security ApiKeyAuth
// @Param start_date query string true "开始日期 (YYYY-MM-DD)"
// @Param end_date query string true "结束日期 (YYYY-MM-DD)"
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/analytics/timerange [get]
func (h *AnalyticsHandler) GetTimeRangeStats(c *gin.Context) {
	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")

	if startDateStr == "" || endDateStr == "" {
		h.response.BadRequest(c, "请提供开始日期和结束日期")
		return
	}

	startDate, err := time.Parse("2006-01-02", startDateStr)
	if err != nil {
		h.response.BadRequest(c, "开始日期格式错误，应为 YYYY-MM-DD")
		return
	}

	endDate, err := time.Parse("2006-01-02", endDateStr)
	if err != nil {
		h.response.BadRequest(c, "结束日期格式错误，应为 YYYY-MM-DD")
		return
	}

	// 确保结束日期包含整天
	endDate = endDate.Add(24*time.Hour - time.Nanosecond)

	stats, err := h.analyticsService.GetTimeRangeStats(c.Request.Context(), startDate, endDate)
	if err != nil {
		h.response.InternalServerError(c, "获取时间范围统计失败: "+err.Error())
		return
	}

	h.response.Success(c, stats, "获取时间范围统计成功")
}

// GetHealthCheck 系统健康检查
// @Summary 系统健康检查
// @Description 检查系统各组件的健康状态
// @Tags 系统监控
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 503 {object} map[string]interface{} "服务不可用"
// @Router /api/health [get]
func (h *AnalyticsHandler) GetHealthCheck(c *gin.Context) {
	health := gin.H{
		"status":      "healthy",
		"timestamp":   time.Now(),
		"version":     h.appVersion,
		"environment": gin.Mode(),
	}

	// 检查数据库连接
	sqlDB, err := h.analyticsService.GetDB().DB()
	if err != nil {
		health["status"] = "unhealthy"
		health["database"] = "connection_failed"
		h.response.Error(c, http.StatusServiceUnavailable, "系统不健康", health)
		return
	}

	err = sqlDB.Ping()
	if err != nil {
		health["status"] = "unhealthy"
		health["database"] = "ping_failed"
		h.response.Error(c, http.StatusServiceUnavailable, "数据库连接失败", health)
		return
	}

	health["database"] = "connected"

	// 检查系统资源
	systemStats, err := h.analyticsService.GetSystemStats()
	if err == nil {
		health["memory_usage"] = gin.H{
			"heap_alloc_mb": float64(systemStats.MemStats.HeapAlloc) / 1024 / 1024,
			"sys_mb":        float64(systemStats.MemStats.Sys) / 1024 / 1024,
		}
		health["goroutines"] = systemStats.GoRoutines

		// 内存使用率检查
		heapUsagePercent := float64(systemStats.MemStats.HeapAlloc) / float64(systemStats.MemStats.Sys) * 100
		if heapUsagePercent > 90 {
			health["status"] = "warning"
			health["warning"] = "high_memory_usage"
		}

		// Goroutine数量检查
		if systemStats.GoRoutines > 10000 {
			health["status"] = "warning"
			health["warning"] = "high_goroutine_count"
		}
	}

	if health["status"] == "unhealthy" {
		h.response.Error(c, http.StatusServiceUnavailable, "健康检查完成", health)
		return
	}
	h.response.Success(c, health, "健康检查完成")
}

// ExportStats 导出统计数据
// @Summary 导出统计数据
// @Description 导出系统和业务统计数据
// @Tags 系统监控
// @Security ApiKeyAuth
// @Param format query string false "导出格式" Enums(json) default(json)
// @Param start_date query string false "开始日期 (YYYY-MM-DD)"
// @Param end_date query string false "结束日期 (YYYY-MM-DD)"
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/analytics/export [get]
func (h *AnalyticsHandler) ExportStats(c *gin.Context) {
	format := c.DefaultQuery("format", "json")
	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")

	var startDate, endDate *time.Time

	// 解析时间范围（可选）
	if startDateStr != "" && endDateStr != "" {
		start, err := time.Parse("2006-01-02", startDateStr)
		if err != nil {
			h.response.BadRequest(c, "开始日期格式错误，应为 YYYY-MM-DD")
			return
		}

		end, err := time.Parse("2006-01-02", endDateStr)
		if err != nil {
			h.response.BadRequest(c, "结束日期格式错误，应为 YYYY-MM-DD")
			return
		}

		// 确保结束日期包含整天
		end = end.Add(24*time.Hour - time.Nanosecond)

		startDate = &start
		endDate = &end
	}

	data, err := h.analyticsService.ExportStats(c.Request.Context(), format, startDate, endDate)
	if err != nil {
		h.response.InternalServerError(c, "导出统计数据失败: "+err.Error())
		return
	}

	// 设置响应头
	filename := "system_analytics_" + time.Now().Format("20060102_150405") + "." + format
	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename="+filename)

	c.Data(http.StatusOK, "application/json", data)
}

// GetRealtimeMetrics 获取实时指标
// @Summary 获取实时指标
// @Description 获取实时系统指标用于监控面板
// @Tags 系统监控
// @Security ApiKeyAuth
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/analytics/realtime [get]
func (h *AnalyticsHandler) GetRealtimeMetrics(c *gin.Context) {
	systemStats, err := h.analyticsService.GetSystemStats()
	if err != nil {
		h.response.InternalServerError(c, "获取实时指标失败: "+err.Error())
		return
	}

	// 构建实时指标数据
	realtimeMetrics := gin.H{
		"timestamp": time.Now(),
		"system": gin.H{
			"cpu_count":  systemStats.CPUCount,
			"goroutines": systemStats.GoRoutines,
			"cgo_calls":  systemStats.CGOCalls,
			"memory_usage": gin.H{
				"heap_alloc_mb":      float64(systemStats.MemStats.HeapAlloc) / 1024 / 1024,
				"heap_sys_mb":        float64(systemStats.MemStats.HeapSys) / 1024 / 1024,
				"heap_inuse_mb":      float64(systemStats.MemStats.HeapInuse) / 1024 / 1024,
				"heap_objects":       systemStats.MemStats.HeapObjects,
				"stack_inuse_mb":     float64(systemStats.MemStats.StackInuse) / 1024 / 1024,
				"sys_mb":             float64(systemStats.MemStats.Sys) / 1024 / 1024,
				"heap_usage_percent": float64(systemStats.MemStats.HeapAlloc) / float64(systemStats.MemStats.HeapSys) * 100,
			},
			"gc": gin.H{
				"num_gc":          systemStats.GCStats.NumGC,
				"num_forced_gc":   systemStats.GCStats.NumForcedGC,
				"gc_cpu_fraction": systemStats.GCStats.GCCPUFraction,
				"last_gc":         systemStats.GCStats.LastGC,
				"pause_total_ms":  float64(systemStats.GCStats.PauseTotal.Nanoseconds()) / 1000000,
			},
		},
	}

	h.response.Success(c, realtimeMetrics, "获取实时指标成功")
}
