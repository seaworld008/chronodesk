package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

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

// GetBusinessStats redirects the protected legacy business endpoint.
// @Summary 兼容旧业务统计入口
// @Description 永久重定向到有界、按成员身份授权的跨项目工作台
// @Tags 系统监控
// @Security ApiKeyAuth
// @Deprecated
// @Success 307 "发布周期内的临时重定向"
// @Header 307 {string} Location "/api/workbench/dashboard"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Router /api/platform/analytics/business [get]
func (h *AnalyticsHandler) GetBusinessStats(c *gin.Context) {
	c.Redirect(http.StatusTemporaryRedirect, "/api/workbench/dashboard")
}

// GetDashboardStats redirects the protected legacy dashboard endpoint.
// @Summary 兼容旧仪表板入口
// @Description 永久重定向到有界、按成员身份授权的跨项目工作台
// @Tags 系统监控
// @Security ApiKeyAuth
// @Deprecated
// @Success 307 "发布周期内的临时重定向"
// @Header 307 {string} Location "/api/workbench/dashboard"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Router /api/platform/analytics/dashboard [get]
func (h *AnalyticsHandler) GetDashboardStats(c *gin.Context) {
	c.Redirect(http.StatusTemporaryRedirect, "/api/workbench/dashboard")
}

// GetTimeRangeStats 获取指定时间范围统计
// @Summary 获取指定时间范围统计
// @Description 获取最多90个自然日的趋势统计数据
// @Tags 系统监控
// @Security ApiKeyAuth
// @Param start_date query string true "开始日期 (YYYY-MM-DD)"
// @Param end_date query string true "结束日期 (YYYY-MM-DD)"
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 413 {object} map[string]interface{} "统计结果超过大小限制"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/platform/analytics/timerange [get]
func (h *AnalyticsHandler) GetTimeRangeStats(c *gin.Context) {
	query, err := parseAnalyticsQuery(c.Request.URL.RawQuery, false)
	if err != nil {
		h.response.BadRequest(c, "统计查询参数无效")
		return
	}
	authorized, ok := h.resolveAuthorizedProjectSet(c)
	if !ok {
		return
	}

	stats, err := h.analyticsService.GetTimeRangeStats(
		c.Request.Context(),
		authorized,
		*query.startDate,
		*query.endDate,
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
// @Description 同步导出最多90个自然日且不超过1MiB的系统和业务统计数据
// @Tags 系统监控
// @Security ApiKeyAuth
// @Param format query string false "导出格式" Enums(json) default(json)
// @Param start_date query string false "开始日期 (YYYY-MM-DD)"
// @Param end_date query string false "结束日期 (YYYY-MM-DD)"
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 413 {object} map[string]interface{} "统计结果超过大小限制"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/platform/analytics/export [get]
func (h *AnalyticsHandler) ExportStats(c *gin.Context) {
	query, err := parseAnalyticsQuery(c.Request.URL.RawQuery, true)
	if err != nil {
		h.response.BadRequest(c, "统计导出查询参数无效")
		return
	}
	authorized, ok := h.resolveAuthorizedProjectSet(c)
	if !ok {
		return
	}

	data, err := h.analyticsService.ExportStats(
		c.Request.Context(),
		authorized,
		query.format,
		query.startDate,
		query.endDate,
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
	filename := "system_analytics_" + time.Now().Format("20060102_150405") + "." + query.format
	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename="+filename)

	c.Data(http.StatusOK, "application/json", data)
}

var errInvalidAnalyticsQuery = errors.New("invalid analytics query")

type analyticsQuery struct {
	format    string
	startDate *time.Time
	endDate   *time.Time
}

func parseAnalyticsQuery(rawQuery string, export bool) (analyticsQuery, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return analyticsQuery{}, errInvalidAnalyticsQuery
	}
	allowed := map[string]struct{}{
		"start_date": {},
		"end_date":   {},
	}
	if export {
		allowed["format"] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := allowed[key]; !ok || len(entries) != 1 ||
			!utf8.ValidString(key) ||
			!utf8.ValidString(entries[0]) ||
			containsDirectoryQueryControl(key) ||
			containsDirectoryQueryControl(entries[0]) ||
			entries[0] == "" ||
			strings.TrimSpace(entries[0]) != entries[0] {
			return analyticsQuery{}, errInvalidAnalyticsQuery
		}
	}

	result := analyticsQuery{format: "json"}
	if raw, exists := values["format"]; exists {
		if !export || raw[0] != "json" {
			return analyticsQuery{}, errInvalidAnalyticsQuery
		}
		result.format = raw[0]
	}

	startRaw, hasStart := values["start_date"]
	endRaw, hasEnd := values["end_date"]
	if hasStart != hasEnd || (!export && !hasStart) {
		return analyticsQuery{}, errInvalidAnalyticsQuery
	}
	if !hasStart {
		return result, nil
	}
	start, err := time.Parse("2006-01-02", startRaw[0])
	if err != nil {
		return analyticsQuery{}, errInvalidAnalyticsQuery
	}
	end, err := time.Parse("2006-01-02", endRaw[0])
	if err != nil {
		return analyticsQuery{}, errInvalidAnalyticsQuery
	}
	end = end.AddDate(0, 0, 1).Add(-time.Nanosecond)
	if err := services.ValidateAnalyticsTimeRange(start, end); err != nil {
		return analyticsQuery{}, errInvalidAnalyticsQuery
	}
	result.startDate = &start
	result.endDate = &end
	return result, nil
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
	projects, err := h.listAuthorizedAnalyticsProjects(c, userID)
	if err != nil {
		if errors.Is(err, services.ErrAnalyticsProjectLimit) {
			h.response.Error(
				c,
				http.StatusRequestEntityTooLarge,
				"授权项目超过统计范围上限",
			)
			return services.AnalyticsAuthorizedProjectSet{}, false
		}
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

func (h *AnalyticsHandler) listAuthorizedAnalyticsProjects(
	c *gin.Context,
	userID uint,
) ([]services.ProjectAccess, error) {
	const pageSize = 100
	projects := make([]services.ProjectAccess, 0, pageSize)
	totalPages := 1
	var expectedTotal int64 = -1
	for page := 1; page <= totalPages; page++ {
		directory, err := h.projectService.ListHumanProjectPage(
			c.Request.Context(),
			userID,
			services.HumanProjectListRequest{
				Page:      page,
				PageSize:  pageSize,
				SortBy:    "name",
				SortOrder: "asc",
			},
		)
		if err != nil {
			return nil, err
		}
		if page == 1 {
			expectedTotal = directory.Total
			totalPages = directory.TotalPages
			if expectedTotal > services.AnalyticsMaxProjects {
				return nil, services.ErrAnalyticsProjectLimit
			}
		} else if directory.Total != expectedTotal ||
			directory.TotalPages != totalPages {
			return nil, services.ErrProjectAccessDenied
		}
		projects = append(projects, directory.Items...)
		if len(projects) > services.AnalyticsMaxProjects {
			return nil, services.ErrAnalyticsProjectLimit
		}
	}
	if int64(len(projects)) != expectedTotal {
		return nil, services.ErrProjectAccessDenied
	}
	return projects, nil
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
	if errors.Is(err, services.ErrAnalyticsResultTooLarge) ||
		errors.Is(err, services.ErrAnalyticsExportTooLarge) ||
		errors.Is(err, services.ErrAnalyticsProjectLimit) {
		h.response.Error(c, http.StatusRequestEntityTooLarge, "统计结果超过大小限制")
		return
	}
	logHandlerFailure(c, operation, err)
	h.response.InternalServerError(c, message)
}
