package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gongdan-system/internal/services"
)

// AdminAuditHandler 管理员审计日志处理器
type AdminAuditHandler struct {
	auditService services.AdminAuditServiceInterface
}

// NewAdminAuditHandler 创建管理员审计日志处理器
func NewAdminAuditHandler(auditService services.AdminAuditServiceInterface) *AdminAuditHandler {
	return &AdminAuditHandler{auditService: auditService}
}

// GetAuditLogs 获取管理员操作审计日志
func (h *AdminAuditHandler) GetAuditLogs(c *gin.Context) {
	if h.auditService == nil {
		c.JSON(http.StatusServiceUnavailable, ApiResponse{
			Code: 1,
			Msg:  "审计日志服务未初始化",
			Data: nil,
		})
		return
	}

	query := struct {
		UserID    string `form:"user_id"`
		Role      string `form:"role"`
		Method    string `form:"method"`
		Path      string `form:"path"`
		Status    string `form:"status"`
		Keyword   string `form:"keyword"`
		StartTime string `form:"start_time"`
		EndTime   string `form:"end_time"`
		Page      int    `form:"page"`
		Limit     int    `form:"limit"`
	}{}

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "查询参数错误: " + err.Error(),
			Data: nil,
		})
		return
	}

	filter := &services.AdminAuditFilter{
		Role:    query.Role,
		Method:  query.Method,
		Path:    query.Path,
		Keyword: query.Keyword,
		Page:    query.Page,
		Limit:   query.Limit,
	}

	if query.UserID != "" {
		if id, err := strconv.ParseUint(query.UserID, 10, 64); err == nil {
			uid := uint(id)
			filter.UserID = &uid
		}
	}

	if query.Status != "" {
		if statusCode, err := strconv.Atoi(query.Status); err == nil {
			filter.Status = &statusCode
		}
	}

	parseTime := func(value string) *time.Time {
		if value == "" {
			return nil
		}
		layouts := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, value); err == nil {
				return &t
			}
		}
		return nil
	}

	filter.StartTime = parseTime(query.StartTime)
	filter.EndTime = parseTime(query.EndTime)

	logs, total, err := h.auditService.List(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "获取审计日志失败: " + err.Error(),
			Data: nil,
		})
		return
	}

	response := struct {
		Items []*services.AdminAuditListItem `json:"items"`
		Total int64                          `json:"total"`
		Page  int                            `json:"page"`
		Limit int                            `json:"limit"`
	}{}

	response.Total = total
	response.Page = filter.Page
	if response.Page < 1 {
		response.Page = 1
	}
	response.Limit = filter.Limit
	if response.Limit <= 0 {
		response.Limit = 20
	}
	response.Items = services.ConvertAuditLogs(logs)

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "获取审计日志成功",
		Data: response,
	})
}
