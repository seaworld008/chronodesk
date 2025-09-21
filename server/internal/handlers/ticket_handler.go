package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gongdan-system/internal/middleware"
	"gongdan-system/internal/models"
	"gongdan-system/internal/services"
)

// TicketHandler 工单处理器
type TicketHandler struct {
	ticketService services.TicketServiceInterface
	response      *middleware.ResponseHelper
}

// NewTicketHandler 创建工单处理器
func NewTicketHandler(ticketService services.TicketServiceInterface) *TicketHandler {
	return &TicketHandler{
		ticketService: ticketService,
		response:      middleware.NewResponseHelper(),
	}
}

// GetTickets 获取工单列表
func (h *TicketHandler) GetTickets(c *gin.Context) {
	ctx := context.Background()

	// 解析查询参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	status := strings.TrimSpace(c.Query("status"))
	priority := strings.TrimSpace(c.Query("priority"))
	ticketType := c.Query("type")
	assignedTo := c.Query("assigned_to")
	createdBy := c.Query("created_by")
	search := c.Query("search")
	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := c.DefaultQuery("sort_order", "desc")

	var tagsFilter []string

	if rawFilter := c.Query("filter"); rawFilter != "" {
		var filterMap map[string]interface{}
		if err := json.Unmarshal([]byte(rawFilter), &filterMap); err == nil {
			if search == "" {
				if qVal, ok := filterMap["q"].(string); ok {
					search = qVal
				}
			}
			if status == "" {
				if values := extractFilterStrings(filterMap["status"]); len(values) > 0 {
					status = strings.Join(values, ",")
				} else if v, ok := filterMap["status"].(string); ok {
					status = v
				}
			}
			if priority == "" {
				if values := extractFilterStrings(filterMap["priority"]); len(values) > 0 {
					priority = strings.Join(values, ",")
				} else if v, ok := filterMap["priority"].(string); ok {
					priority = v
				}
			}
			if ticketType == "" {
				if v, ok := filterMap["type"].(string); ok {
					ticketType = v
				}
			}

			tagsFilter = extractFilterStrings(filterMap["tags"])
			if len(tagsFilter) == 0 {
				tagsFilter = extractFilterStrings(filterMap["tag"])
			}
		}
	}

	// 构建过滤器
	filters := services.TicketFilters{
		Page:      page,
		Limit:     pageSize,
		Status:    status,
		Priority:  priority,
		Type:      ticketType,
		Search:    search,
		Tags:      tagsFilter,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	}

	if assignedTo != "" {
		if assignedToID, err := strconv.ParseUint(assignedTo, 10, 32); err == nil {
			id := uint(assignedToID)
			filters.AssigneeID = &id
		}
	}

	if createdBy != "" {
		if createdByID, err := strconv.ParseUint(createdBy, 10, 32); err == nil {
			id := uint(createdByID)
			filters.CreatorID = &id
		}
	}

	// 获取工单列表
	tickets, total, err := h.ticketService.GetTickets(ctx, filters)
	if err != nil {
		h.response.InternalServerError(c, "获取工单列表失败: "+err.Error())
		return
	}

	responses := make([]*models.TicketResponse, len(tickets))
	for i, ticket := range tickets {
		responses[i] = ticket.ToResponse()
	}

	h.response.List(c, responses, total, page, pageSize, "获取工单列表成功")
}

func extractFilterStrings(value interface{}) []string {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	case string:
		if v == "" {
			return nil
		}
		parts := strings.Split(v, ",")
		result := make([]string, 0, len(parts))
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				trimmed := strings.TrimSpace(str)
				if trimmed != "" {
					result = append(result, trimmed)
				}
			}
		}
		return result
	default:
		return nil
	}
}

// GetTicket 获取单个工单
func (h *TicketHandler) GetTicket(c *gin.Context) {
	ctx := context.Background()

	// 解析工单ID
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		h.response.BadRequest(c, "无效的工单ID")
		return
	}

	// 获取工单
	ticket, err := h.ticketService.GetTicket(ctx, uint(id))
	if err != nil {
		if err.Error() == "ticket not found" {
			h.response.NotFound(c, "工单不存在")
			return
		}
		h.response.InternalServerError(c, "获取工单失败")
		return
	}

	h.response.Success(c, ticket.ToResponse(), "获取工单成功")
}

// CreateTicket 创建工单
func (h *TicketHandler) CreateTicket(c *gin.Context) {
	ctx := context.Background()

	// 解析请求体
	var req models.TicketCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.response.BadRequest(c, "请求格式错误: "+err.Error())
		return
	}

	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		h.response.Unauthorized(c, "用户未认证")
		return
	}

	// 创建工单
	ticket, err := h.ticketService.CreateTicket(ctx, &req, userID.(uint))
	if err != nil {
		h.response.InternalServerError(c, "创建工单失败: "+err.Error())
		return
	}

	h.response.Created(c, ticket.ToResponse(), "工单创建成功")
}

// UpdateTicket 更新工单
func (h *TicketHandler) UpdateTicket(c *gin.Context) {
	ctx := context.Background()

	// 解析工单ID
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		h.response.BadRequest(c, "无效的工单ID")
		return
	}

	// 解析请求体
	var req models.TicketUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.response.BadRequest(c, "请求格式错误: "+err.Error())
		return
	}

	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		h.response.Unauthorized(c, "用户未认证")
		return
	}

	// 更新工单
	ticket, err := h.ticketService.UpdateTicket(ctx, uint(id), &req, userID.(uint))
	if err != nil {
		if err.Error() == "ticket not found" {
			h.response.NotFound(c, "工单不存在")
			return
		}
		h.response.InternalServerError(c, "更新工单失败: "+err.Error())
		return
	}

	h.response.Success(c, ticket.ToResponse(), "工单更新成功")
}

// DeleteTicket 删除工单
func (h *TicketHandler) DeleteTicket(c *gin.Context) {
	ctx := context.Background()

	// 解析工单ID
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		h.response.BadRequest(c, "无效的工单ID")
		return
	}

	// 获取当前用户信息
	userIDValue, exists := c.Get("user_id")
	if !exists {
		h.response.Unauthorized(c, "用户未认证")
		return
	}

	userRoleValue, roleExists := c.Get("user_role")
	if !roleExists {
		h.response.Forbidden(c, "缺少用户角色信息")
		return
	}

	role, ok := userRoleValue.(string)
	if !ok {
		h.response.InternalServerError(c, "无效的角色类型")
		return
	}

	userID, ok := userIDValue.(uint)
	if !ok {
		h.response.InternalServerError(c, "无效的用户ID")
		return
	}

	// 删除工单
	err = h.ticketService.DeleteTicket(ctx, uint(id), userID, role)
	if err != nil {
		if err.Error() == "ticket not found" {
			h.response.NotFound(c, "工单不存在")
			return
		}
		h.response.InternalServerError(c, "删除工单失败: "+err.Error())
		return
	}

	h.response.Success(c, nil, "工单删除成功")
}

// AssignTicket 分配工单
func (h *TicketHandler) AssignTicket(c *gin.Context) {
	// 解析工单ID
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		h.response.Error(c, http.StatusBadRequest, "invalid_ticket_id", "Invalid ticket ID")
		return
	}

	// 解析请求体
	var req struct {
		AssignedToID *uint `json:"assigned_to_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.response.Error(c, http.StatusBadRequest, "invalid_request", "Invalid request format: "+err.Error())
		return
	}

	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		h.response.Error(c, http.StatusUnauthorized, "unauthorized", "User not authenticated")
		return
	}

	// 验证分配用户ID
	if req.AssignedToID == nil {
		h.response.Error(c, http.StatusBadRequest, "invalid_request", "assigned_to_id is required")
		return
	}

	// 分配工单
	ticket, err := h.ticketService.AssignTicket(uint(id), *req.AssignedToID, userID.(uint), "")
	if err != nil {
		if err.Error() == "ticket not found" {
			h.response.Error(c, http.StatusNotFound, "ticket_not_found", "Ticket not found")
			return
		}
		h.response.Error(c, http.StatusInternalServerError, "assign_ticket_failed", "Failed to assign ticket: "+err.Error())
		return
	}

	h.response.Success(c, ticket, "Ticket assigned successfully")
}

// GetTicketStats 获取工单统计
func (h *TicketHandler) GetTicketStats(c *gin.Context) {
	ctx := context.Background()

	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		h.response.Unauthorized(c, "用户未认证")
		return
	}

	// 获取统计数据
	stats, err := h.ticketService.GetTicketStats(ctx, userID.(uint))
	if err != nil {
		h.response.InternalServerError(c, "获取工单统计失败")
		return
	}

	h.response.Success(c, stats, "获取统计数据成功")
}

// BulkUpdateTickets 批量更新工单
func (h *TicketHandler) BulkUpdateTickets(c *gin.Context) {
	ctx := context.Background()

	// 解析请求体
	var req struct {
		TicketIDs []uint                     `json:"ticket_ids" binding:"required"`
		Updates   models.TicketUpdateRequest `json:"updates" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.response.Error(c, http.StatusBadRequest, "invalid_request", "Invalid request format: "+err.Error())
		return
	}

	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		h.response.Error(c, http.StatusUnauthorized, "unauthorized", "User not authenticated")
		return
	}

	// 创建批量更新请求
	bulkReq := &services.BulkUpdateRequest{
		TicketIDs: req.TicketIDs,
	}

	if req.Updates.Status != nil {
		status := string(*req.Updates.Status)
		bulkReq.Status = &status
	}
	if req.Updates.Priority != nil {
		priority := string(*req.Updates.Priority)
		bulkReq.Priority = &priority
	}
	if req.Updates.AssignedToID != nil {
		bulkReq.AssignedToID = req.Updates.AssignedToID
	}
	if req.Updates.Tags != nil {
		bulkReq.Tags = req.Updates.Tags
	}
	if req.Updates.CustomFields != nil {
		bulkReq.CustomFields = req.Updates.CustomFields.ToMap()
	}

	// 批量更新工单
	err := h.ticketService.BulkUpdateTickets(ctx, bulkReq, userID.(uint))
	if err != nil {
		h.response.Error(c, http.StatusInternalServerError, "bulk_update_failed", "Failed to bulk update tickets: "+err.Error())
		return
	}

	h.response.Success(c, nil, "Tickets updated successfully")
}

// GetTicketHistory 获取工单历史记录
func (h *TicketHandler) GetTicketHistory(c *gin.Context) {
	// 获取工单ID
	ticketIDStr := c.Param("id")
	ticketID, err := strconv.Atoi(ticketIDStr)
	if err != nil {
		h.response.Error(c, http.StatusBadRequest, "invalid_ticket_id", "Invalid ticket ID format")
		return
	}

	// 解析查询参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	actionsStr := c.Query("actions")
	isVisibleStr := c.Query("is_visible")
	isSystemStr := c.Query("is_system")

	// 构建历史记录过滤器
	filters := &models.HistoryFilter{
		TicketID: uint64ToUintPtr(uint64(ticketID)),
		Limit:    pageSize,
		Offset:   (page - 1) * pageSize,
		OrderBy:  "created_at",
		OrderDir: "desc",
	}

	// 解析actions参数
	if actionsStr != "" {
		// 这里可以根据需要解析多个action
		filters.Actions = []models.HistoryAction{models.HistoryAction(actionsStr)}
	}

	// 解析布尔参数
	if isVisibleStr != "" {
		isVisible := isVisibleStr == "true"
		filters.IsVisible = &isVisible
	}
	if isSystemStr != "" {
		isSystem := isSystemStr == "true"
		filters.IsSystem = &isSystem
	}

	// 获取历史记录
	histories, _, err := h.ticketService.GetTicketHistory(uint(ticketID))
	if err != nil {
		h.response.Error(c, http.StatusInternalServerError, "get_history_failed", "Failed to get ticket history: "+err.Error())
		return
	}

	// 转换为响应格式
	responses := make([]*models.TicketHistoryResponse, len(histories))
	for i, history := range histories {
		responses[i] = history.ToResponse()
	}

	h.response.Success(c, responses, "获取工单历史记录成功")
}

// 辅助函数：将uint64转换为*uint
func uint64ToUintPtr(val uint64) *uint {
	uintVal := uint(val)
	return &uintVal
}
