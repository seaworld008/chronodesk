package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

// AnalyticsHandler 分析统计处理器
type AnalyticsHandler struct {
	analyticsService *services.AnalyticsService
	projectService   *services.ProjectService
	response         *middleware.ResponseHelper
}

// NewAnalyticsHandler 创建分析处理器
func NewAnalyticsHandler(
	db *gorm.DB,
	projectServices ...*services.ProjectService,
) *AnalyticsHandler {
	var projectService *services.ProjectService
	if len(projectServices) == 1 {
		projectService = projectServices[0]
	}
	return &AnalyticsHandler{
		analyticsService: services.NewAnalyticsService(db),
		projectService:   projectService,
		response:         middleware.NewResponseHelper(),
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
// @Router /api/platform/analytics/system [get]
func (h *AnalyticsHandler) GetSystemStats(c *gin.Context) {
	stats, err := h.analyticsService.GetSystemStats()
	if err != nil {
		logHandlerFailure(c, "analytics.get_system_stats", err)
		h.response.InternalServerError(c, "获取系统统计失败")
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
// @Router /api/platform/analytics/business [get]
func (h *AnalyticsHandler) GetBusinessStats(c *gin.Context) {
	authorized, ok := h.resolveAuthorizedProjectSet(c)
	if !ok {
		return
	}
	stats, err := h.analyticsService.GetBusinessStats(
		c.Request.Context(),
		authorized,
	)
	if err != nil {
		h.writeBusinessAnalyticsError(
			c,
			"analytics.get_business_stats",
			err,
			"获取业务统计失败",
		)
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
// @Router /api/platform/analytics/dashboard [get]
func (h *AnalyticsHandler) GetDashboardStats(c *gin.Context) {
	authorized, ok := h.resolveAuthorizedProjectSet(c)
	if !ok {
		return
	}
	platformStats, err := h.analyticsService.GetPlatformStats(
		c.Request.Context(),
	)
	if err != nil {
		logHandlerFailure(c, "analytics.dashboard_platform_stats", err)
		h.response.InternalServerError(c, "获取平台统计失败")
		return
	}

	businessStats, err := h.analyticsService.GetBusinessStats(
		c.Request.Context(),
		authorized,
	)
	if err != nil {
		h.writeBusinessAnalyticsError(
			c,
			"analytics.dashboard_business_stats",
			err,
			"获取业务统计失败",
		)
		return
	}

	// 获取最近7天的趋势数据
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -7)

	timeRangeStats, err := h.analyticsService.GetTimeRangeStats(
		c.Request.Context(),
		authorized,
		startDate,
		endDate,
	)
	if err != nil {
		h.writeBusinessAnalyticsError(
			c,
			"analytics.dashboard_trend_stats",
			err,
			"获取趋势数据失败",
		)
		return
	}

	dashboardData := gin.H{
		"platform_stats":   platformStats,
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
// @Router /api/platform/analytics/timerange [get]
func (h *AnalyticsHandler) GetTimeRangeStats(c *gin.Context) {
	authorized, ok := h.resolveAuthorizedProjectSet(c)
	if !ok {
		return
	}
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

	stats, err := h.analyticsService.GetTimeRangeStats(
		c.Request.Context(),
		authorized,
		startDate,
		endDate,
	)
	if err != nil {
		h.writeBusinessAnalyticsError(
			c,
			"analytics.get_time_range_stats",
			err,
			"获取时间范围统计失败",
		)
		return
	}

	h.response.Success(c, stats, "获取时间范围统计成功")
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
// @Router /api/platform/analytics/export [get]
func (h *AnalyticsHandler) ExportStats(c *gin.Context) {
	authorized, ok := h.resolveAuthorizedProjectSet(c)
	if !ok {
		return
	}
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

	data, err := h.analyticsService.ExportStats(
		c.Request.Context(),
		authorized,
		format,
		startDate,
		endDate,
	)
	if err != nil {
		h.writeBusinessAnalyticsError(
			c,
			"analytics.export_stats",
			err,
			"导出统计数据失败",
		)
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
// @Router /api/platform/analytics/realtime [get]
func (h *AnalyticsHandler) GetRealtimeMetrics(c *gin.Context) {
	systemStats, err := h.analyticsService.GetSystemStats()
	if err != nil {
		logHandlerFailure(c, "analytics.get_realtime_metrics", err)
		h.response.InternalServerError(c, "获取实时指标失败")
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

func (h *AnalyticsHandler) resolveAuthorizedProjectSet(
	c *gin.Context,
) (services.AnalyticsAuthorizedProjectSet, bool) {
	if h == nil || h.projectService == nil {
		h.response.Forbidden(c, "未配置业务统计项目授权")
		return services.AnalyticsAuthorizedProjectSet{}, false
	}
	userID := c.GetUint("user_id")
	if userID == 0 {
		h.response.Forbidden(c, "无权访问项目业务统计")
		return services.AnalyticsAuthorizedProjectSet{}, false
	}
	projects, err := h.projectService.ListHumanProjects(
		c.Request.Context(),
		userID,
	)
	if err != nil {
		if errors.Is(err, services.ErrProjectAccessDenied) {
			h.response.Forbidden(c, "无权访问项目业务统计")
			return services.AnalyticsAuthorizedProjectSet{}, false
		}
		logHandlerFailure(c, "analytics.resolve_authorized_projects", err)
		h.response.InternalServerError(c, "解析业务统计项目授权失败")
		return services.AnalyticsAuthorizedProjectSet{}, false
	}
	if len(projects) == 0 {
		h.response.Forbidden(c, "无权访问项目业务统计")
		return services.AnalyticsAuthorizedProjectSet{}, false
	}
	organizationID := projects[0].Scope.OrganizationID
	projectIDs := make([]uint, 0, len(projects))
	seen := make(map[uint]struct{}, len(projects))
	for _, access := range projects {
		if access.Scope.OrganizationID != organizationID ||
			access.Scope.ProjectID == 0 {
			h.response.Forbidden(c, "业务统计项目授权范围不一致")
			return services.AnalyticsAuthorizedProjectSet{}, false
		}
		if _, duplicate := seen[access.Scope.ProjectID]; duplicate {
			h.response.Forbidden(c, "业务统计项目授权范围重复")
			return services.AnalyticsAuthorizedProjectSet{}, false
		}
		seen[access.Scope.ProjectID] = struct{}{}
		projectIDs = append(projectIDs, access.Scope.ProjectID)
	}
	authorized, err := services.NewHumanAnalyticsAuthorizedProjectSet(
		userID,
		organizationID,
		projectIDs,
	)
	if err != nil {
		h.response.Forbidden(c, "业务统计项目授权范围无效")
		return services.AnalyticsAuthorizedProjectSet{}, false
	}
	return authorized, true
}

func (h *AnalyticsHandler) writeBusinessAnalyticsError(
	c *gin.Context,
	operation string,
	err error,
	message string,
) {
	if errors.Is(err, services.ErrProjectAccessDenied) ||
		errors.Is(
			err,
			services.ErrAnalyticsAuthorizedProjectSetRequired,
		) {
		h.response.Forbidden(c, "业务统计项目授权已失效")
		return
	}
	if errors.Is(err, services.ErrAnalyticsInvalidTimeRange) {
		h.response.BadRequest(c, "统计时间范围无效")
		return
	}
	logHandlerFailure(c, operation, err)
	h.response.InternalServerError(c, message)
}
