package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type AdminAuditExplorerService interface {
	Explore(
		context.Context,
		*services.AdminAuditFilter,
	) (*services.AdminAuditPage, error)
	GetDetail(context.Context, uint) (*services.AdminAuditDetail, error)
}

// AdminAuditHandler 管理员审计日志处理器
type AdminAuditHandler struct {
	auditService  AdminAuditExplorerService
	exportService AdminAuditExporterService
}

type AdminAuditExporterService interface {
	Create(
		context.Context,
		uint,
		models.PlatformRole,
		*services.AdminAuditFilter,
		uint,
	) (*services.AdminAuditExportView, error)
	Get(
		context.Context,
		uint,
		string,
	) (*services.AdminAuditExportView, error)
	Open(
		context.Context,
		uint,
		string,
	) (*services.AdminAuditExportDownload, error)
}

// NewAdminAuditHandler 创建新的审计探索器处理器。
func NewAdminAuditHandler(auditService AdminAuditExplorerService) *AdminAuditHandler {
	return &AdminAuditHandler{auditService: auditService}
}

func (h *AdminAuditHandler) SetExportService(
	service AdminAuditExporterService,
) {
	if h != nil {
		h.exportService = service
	}
}

// GetAuditLogs 获取管理员操作审计日志。
func (h *AdminAuditHandler) GetAuditLogs(c *gin.Context) {
	if h.auditService == nil {
		adminAuditError(c, http.StatusServiceUnavailable, "审计日志服务未初始化")
		return
	}
	filter, err := parseAdminAuditFilter(c)
	if err != nil {
		adminAuditError(c, http.StatusBadRequest, "审计查询参数错误")
		return
	}
	page, err := h.auditService.Explore(c.Request.Context(), filter)
	if err != nil {
		if errors.Is(err, services.ErrInvalidAdminAuditCursor) {
			adminAuditError(c, http.StatusBadRequest, "审计游标无效或已被篡改")
			return
		}
		if errors.Is(err, services.ErrInvalidAdminAuditLimit) {
			adminAuditError(c, http.StatusBadRequest, "审计分页大小必须在 1 到 100 之间")
			return
		}
		logHandlerFailure(c, "admin_audit.list", err)
		adminAuditError(c, http.StatusInternalServerError, "获取审计日志失败")
		return
	}
	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "获取审计日志成功",
		Data: page,
	})
}

func (h *AdminAuditHandler) GetAuditLog(c *gin.Context) {
	if h.auditService == nil {
		adminAuditError(c, http.StatusServiceUnavailable, "审计日志服务未初始化")
		return
	}
	id, err := safeconv.ParsePositiveUint(c.Param("id"))
	if err != nil {
		adminAuditError(c, http.StatusBadRequest, "审计记录 ID 无效")
		return
	}
	detail, err := h.auditService.GetDetail(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrAdminAuditNotFound) {
			adminAuditError(c, http.StatusNotFound, "审计记录不存在")
			return
		}
		logHandlerFailure(c, "admin_audit.detail", err)
		adminAuditError(c, http.StatusInternalServerError, "获取审计详情失败")
		return
	}
	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "获取审计详情成功",
		Data: detail,
	})
}

func (h *AdminAuditHandler) CreateAuditExport(c *gin.Context) {
	if h == nil || h.exportService == nil {
		adminAuditError(c, http.StatusServiceUnavailable, "审计导出服务未初始化")
		return
	}
	filter, err := parseAdminAuditExportFilter(c)
	if err != nil {
		adminAuditError(c, http.StatusBadRequest, "审计导出筛选参数错误")
		return
	}
	userID, userOK := middleware.GetCurrentUserID(c)
	role, roleOK := middleware.GetCurrentPlatformRole(c)
	anchorID, anchorOK := middleware.GetCurrentAdminAuditRecordID(c)
	if !userOK || !roleOK {
		adminAuditError(c, http.StatusForbidden, "无权创建审计导出")
		return
	}
	if !anchorOK {
		adminAuditError(c, http.StatusServiceUnavailable, "审计导出锚点不可用")
		return
	}
	view, err := h.exportService.Create(
		c.Request.Context(),
		userID,
		role,
		filter,
		anchorID,
	)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrAdminAuditExportInvalidRange):
			adminAuditError(c, http.StatusBadRequest, "导出时间范围必须在 30 天以内")
		case errors.Is(err, services.ErrAdminAuditExportUnauthorized):
			adminAuditError(c, http.StatusForbidden, "无权创建审计导出")
		case errors.Is(err, services.ErrAdminAuditExportInvalidAnchor):
			adminAuditError(c, http.StatusServiceUnavailable, "审计导出锚点不可用")
		default:
			logHandlerFailure(c, "admin_audit_export.create", err)
			adminAuditError(c, http.StatusInternalServerError, "创建审计导出失败")
		}
		return
	}
	c.JSON(http.StatusAccepted, ApiResponse{
		Code: 0,
		Msg:  "审计导出任务已创建",
		Data: view,
	})
}

func (h *AdminAuditHandler) GetAuditExport(c *gin.Context) {
	if h == nil || h.exportService == nil {
		adminAuditError(c, http.StatusServiceUnavailable, "审计导出服务未初始化")
		return
	}
	if err := requireNoAdminAuditQuery(c); err != nil {
		adminAuditError(c, http.StatusBadRequest, "审计导出查询参数错误")
		return
	}
	userID, ok := middleware.GetCurrentUserID(c)
	if !ok {
		adminAuditError(c, http.StatusForbidden, "无权查看审计导出")
		return
	}
	view, err := h.exportService.Get(
		c.Request.Context(),
		userID,
		c.Param("publicID"),
	)
	if err != nil {
		if errors.Is(err, services.ErrAdminAuditExportNotFound) {
			adminAuditError(c, http.StatusNotFound, "审计导出任务不存在")
			return
		}
		logHandlerFailure(c, "admin_audit_export.status", err)
		adminAuditError(c, http.StatusInternalServerError, "获取审计导出状态失败")
		return
	}
	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "获取审计导出状态成功",
		Data: view,
	})
}

func (h *AdminAuditHandler) DownloadAuditExport(c *gin.Context) {
	if h == nil || h.exportService == nil {
		adminAuditError(c, http.StatusServiceUnavailable, "审计导出服务未初始化")
		return
	}
	if err := requireNoAdminAuditQuery(c); err != nil {
		adminAuditError(c, http.StatusBadRequest, "审计导出查询参数错误")
		return
	}
	userID, ok := middleware.GetCurrentUserID(c)
	if !ok {
		adminAuditError(c, http.StatusForbidden, "无权下载审计导出")
		return
	}
	download, err := h.exportService.Open(
		c.Request.Context(),
		userID,
		c.Param("publicID"),
	)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrAdminAuditExportNotFound):
			adminAuditError(c, http.StatusNotFound, "审计导出任务不存在")
		case errors.Is(err, services.ErrAdminAuditExportPending):
			adminAuditError(c, http.StatusConflict, "审计导出尚未完成")
		case errors.Is(err, services.ErrAdminAuditExportFailed):
			adminAuditError(c, http.StatusConflict, "审计导出生成失败")
		case errors.Is(err, services.ErrAdminAuditExportExpired):
			adminAuditError(c, http.StatusGone, "审计导出已过期")
		default:
			logHandlerFailure(c, "admin_audit_export.download", err)
			adminAuditError(c, http.StatusInternalServerError, "下载审计导出失败")
		}
		return
	}
	defer download.Reader.Close()
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header(
		"Content-Disposition",
		`attachment; filename="`+download.Filename+`"`,
	)
	c.Header("Content-Length", strconv.FormatInt(download.Size, 10))
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Content-SHA256", download.SHA256)
	c.Status(http.StatusOK)
	written, copyErr := io.Copy(c.Writer, download.Reader)
	if copyErr != nil || written != download.Size {
		middleware.SetAdminAuditOperationOutcome(
			c,
			"error",
			"审计导出下载流未完整发送",
		)
		return
	}
	middleware.SetAdminAuditOperationOutcome(c, "success", "")
}

func parseAdminAuditExportFilter(
	c *gin.Context,
) (*services.AdminAuditFilter, error) {
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return nil, errors.New("审计导出查询字符串无效")
	}
	allowed := map[string]struct{}{
		"user_id": {}, "actor": {}, "platform_role": {}, "action": {},
		"method": {}, "path": {}, "path_prefix": {}, "status": {},
		"result": {}, "keyword": {}, "start_time": {}, "end_time": {},
	}
	for key, entries := range values {
		if _, ok := allowed[key]; !ok {
			return nil, errors.New("包含未知的审计导出筛选参数")
		}
		if len(entries) != 1 || strings.TrimSpace(entries[0]) == "" {
			return nil, errors.New("审计导出筛选参数不能为空或重复")
		}
	}
	startRaw, startExists := values["start_time"]
	endRaw, endExists := values["end_time"]
	if !startExists || !endExists {
		return nil, errors.New("审计导出必须指定开始和结束时间")
	}
	filter := &services.AdminAuditFilter{}
	if raw := values.Get("user_id"); raw != "" {
		id, parseErr := safeconv.ParsePositiveUint(raw)
		if parseErr != nil {
			return nil, errors.New("用户 ID 筛选值无效")
		}
		filter.UserID = &id
	}
	if filter.Actor, err = auditTextFilter(values.Get("actor"), 100); err != nil {
		return nil, errors.New("操作人筛选值无效")
	}
	if rawRole := values.Get("platform_role"); rawRole != "" {
		filter.PlatformRole = models.PlatformRole(rawRole)
		if !filter.PlatformRole.IsValid() {
			return nil, errors.New("平台角色筛选值无效")
		}
	}
	if filter.Action, err = auditTextFilter(values.Get("action"), 255); err != nil {
		return nil, errors.New("操作筛选值无效")
	}
	filter.Method = strings.ToUpper(strings.TrimSpace(values.Get("method")))
	if filter.Method != "" && !validAuditMethod(filter.Method) {
		return nil, errors.New("请求方法筛选值无效")
	}
	legacyPath := values.Get("path")
	pathPrefix := values.Get("path_prefix")
	if legacyPath != "" && pathPrefix != "" {
		return nil, errors.New("资源路径筛选参数不能重复表达")
	}
	if pathPrefix == "" {
		pathPrefix = legacyPath
	}
	if filter.Path, err = auditTextFilter(pathPrefix, 255); err != nil ||
		(filter.Path != "" && !strings.HasPrefix(filter.Path, "/")) {
		return nil, errors.New("资源路径前缀筛选值无效")
	}
	if raw := values.Get("status"); raw != "" {
		status, parseErr := strconv.Atoi(raw)
		if parseErr != nil || status < 100 || status > 599 {
			return nil, errors.New("HTTP 状态码筛选值无效")
		}
		filter.Status = &status
	}
	filter.Result = strings.TrimSpace(values.Get("result"))
	if filter.Result != "" &&
		filter.Result != "pending" &&
		filter.Result != "success" &&
		filter.Result != "error" {
		return nil, errors.New("操作结果筛选值无效")
	}
	if filter.Keyword, err = auditTextFilter(values.Get("keyword"), 200); err != nil {
		return nil, errors.New("关键词筛选值无效")
	}
	start, err := parseAdminAuditTime(startRaw[0], false)
	if err != nil {
		return nil, errors.New("开始时间筛选值无效")
	}
	end, err := parseAdminAuditTime(endRaw[0], true)
	if err != nil {
		return nil, errors.New("结束时间筛选值无效")
	}
	if start.After(end) || end.Sub(start) > services.MaxAdminAuditExportRange {
		return nil, services.ErrAdminAuditExportInvalidRange
	}
	filter.StartTime = &start
	filter.EndTime = &end
	return filter, nil
}

func requireNoAdminAuditQuery(c *gin.Context) error {
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil || len(values) != 0 {
		return errors.New("审计导出状态和下载不接受查询参数")
	}
	return nil
}

func parseAdminAuditFilter(c *gin.Context) (*services.AdminAuditFilter, error) {
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return nil, errors.New("审计查询字符串无效")
	}
	allowed := map[string]struct{}{
		"user_id": {}, "actor": {}, "platform_role": {}, "action": {},
		"method": {}, "path": {}, "path_prefix": {}, "status": {},
		"result": {}, "keyword": {}, "time_preset": {}, "start_time": {},
		"end_time": {}, "limit": {}, "cursor": {},
	}
	for key, entries := range values {
		if _, ok := allowed[key]; !ok {
			return nil, errors.New("包含未知的审计筛选参数")
		}
		if len(entries) != 1 ||
			strings.TrimSpace(entries[0]) == "" ||
			entries[0] != strings.TrimSpace(entries[0]) {
			return nil, errors.New(
				"审计筛选参数不能为空、重复或包含首尾空白",
			)
		}
	}

	filter := &services.AdminAuditFilter{
		Limit: services.DefaultAdminAuditLimit,
	}
	if raw := values.Get("user_id"); raw != "" {
		id, parseErr := safeconv.ParsePositiveUint(raw)
		if parseErr != nil {
			return nil, errors.New("用户 ID 筛选值无效")
		}
		filter.UserID = &id
	}
	if filter.Actor, err = auditTextFilter(values.Get("actor"), 100); err != nil {
		return nil, errors.New("操作人筛选值无效")
	}
	rawRole := values.Get("platform_role")
	if rawRole != "" {
		filter.PlatformRole = models.PlatformRole(rawRole)
		if !filter.PlatformRole.IsValid() {
			return nil, errors.New("平台角色筛选值无效")
		}
	}
	if filter.Action, err = auditTextFilter(values.Get("action"), 255); err != nil {
		return nil, errors.New("操作筛选值无效")
	}

	filter.Method = strings.ToUpper(strings.TrimSpace(values.Get("method")))
	if filter.Method != "" && !validAuditMethod(filter.Method) {
		return nil, errors.New("请求方法筛选值无效")
	}
	legacyPath := values.Get("path")
	pathPrefix := values.Get("path_prefix")
	if legacyPath != "" && pathPrefix != "" {
		return nil, errors.New("资源路径筛选参数不能重复表达")
	}
	if pathPrefix == "" {
		pathPrefix = legacyPath
	}
	if filter.Path, err = auditTextFilter(pathPrefix, 255); err != nil ||
		(filter.Path != "" && !strings.HasPrefix(filter.Path, "/")) {
		return nil, errors.New("资源路径前缀筛选值无效")
	}

	if raw := values.Get("status"); raw != "" {
		status, parseErr := strconv.Atoi(raw)
		if parseErr != nil || status < 100 || status > 599 {
			return nil, errors.New("HTTP 状态码筛选值无效")
		}
		filter.Status = &status
	}
	filter.Result = strings.TrimSpace(values.Get("result"))
	if filter.Result != "" &&
		filter.Result != "pending" &&
		filter.Result != "success" &&
		filter.Result != "error" {
		return nil, errors.New("操作结果筛选值无效")
	}
	if filter.Keyword, err = auditTextFilter(values.Get("keyword"), 200); err != nil {
		return nil, errors.New("关键词筛选值无效")
	}

	preset := strings.TrimSpace(values.Get("time_preset"))
	startRaw := strings.TrimSpace(values.Get("start_time"))
	endRaw := strings.TrimSpace(values.Get("end_time"))
	if preset != "" && (startRaw != "" || endRaw != "") {
		return nil, errors.New("时间预设与自定义时间范围不能同时使用")
	}
	if preset != "" {
		duration, ok := map[string]time.Duration{
			"1h":  time.Hour,
			"24h": 24 * time.Hour,
			"7d":  7 * 24 * time.Hour,
			"30d": 30 * 24 * time.Hour,
		}[preset]
		if !ok {
			return nil, errors.New("时间预设筛选值无效")
		}
		end := time.Now().UTC()
		start := end.Add(-duration)
		filter.StartTime = &start
		filter.EndTime = &end
		filter.TimePreset = preset
	} else {
		if startRaw != "" {
			start, parseErr := parseAdminAuditTime(startRaw, false)
			if parseErr != nil {
				return nil, errors.New("开始时间筛选值无效")
			}
			filter.StartTime = &start
		}
		if endRaw != "" {
			end, parseErr := parseAdminAuditTime(endRaw, true)
			if parseErr != nil {
				return nil, errors.New("结束时间筛选值无效")
			}
			filter.EndTime = &end
		}
	}
	if filter.StartTime != nil && filter.EndTime != nil &&
		filter.StartTime.After(*filter.EndTime) {
		return nil, errors.New("开始时间不能晚于结束时间")
	}

	if raw := values.Get("limit"); raw != "" {
		limit, parseErr := strconv.Atoi(raw)
		if parseErr != nil || limit < 1 || limit > services.MaxAdminAuditLimit {
			return nil, errors.New("每页数量必须在 1 到 100 之间")
		}
		filter.Limit = limit
	}
	filter.Cursor = strings.TrimSpace(values.Get("cursor"))
	if filter.Cursor != "" {
		if len(filter.Cursor) > 2048 ||
			strings.IndexFunc(filter.Cursor, unicode.IsSpace) >= 0 {
			return nil, errors.New("审计游标格式无效")
		}
	}
	return filter, nil
}

func parseAdminAuditTime(value string, endOfDate bool) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, err
	}
	if endOfDate {
		parsed = parsed.Add(24*time.Hour - time.Nanosecond)
	}
	return parsed, nil
}

func auditTextFilter(value string, maxRunes int) (string, error) {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > maxRunes ||
		strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("invalid audit text filter")
	}
	return value, nil
}

func validAuditMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}

func adminAuditError(c *gin.Context, status int, message string) {
	c.JSON(status, ApiResponse{Code: 1, Msg: message, Data: nil})
}
