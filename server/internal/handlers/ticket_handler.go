package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
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
	ctx := c.Request.Context()

	// 解析查询参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSizeRaw := c.Query("page_size")
	if pageSizeRaw == "" {
		pageSizeRaw = c.DefaultQuery("limit", "20")
	}
	pageSize, _ := strconv.Atoi(pageSizeRaw)
	page, pageSize = normalizePagination(page, pageSize, 100)
	status := strings.TrimSpace(c.Query("status"))
	priority := strings.TrimSpace(c.Query("priority"))
	ticketType := c.Query("type")
	source := c.Query("source")
	assignedTo := c.Query("assigned_to")
	createdBy := c.Query("created_by")
	search := c.Query("search")
	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := c.DefaultQuery("sort_order", "desc")
	slaBreached := strings.TrimSpace(c.Query("sla_breached"))
	isOverdue := strings.TrimSpace(c.Query("is_overdue"))
	unassigned := strings.TrimSpace(c.Query("unassigned"))
	assignedToMe := strings.TrimSpace(c.Query("assigned_to_me"))

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
			if source == "" {
				if v, ok := filterMap["source"].(string); ok {
					source = v
				}
			}

			tagsFilter = extractFilterStrings(filterMap["tags"])
			if len(tagsFilter) == 0 {
				tagsFilter = extractFilterStrings(filterMap["tag"])
			}

			if slaBreached == "" {
				if value, ok := parseFilterBool(filterMap["sla_breached"]); ok {
					slaBreached = strconv.FormatBool(value)
				}
			}
			if isOverdue == "" {
				if value, ok := parseFilterBool(filterMap["is_overdue"]); ok {
					isOverdue = strconv.FormatBool(value)
				}
			}
			if unassigned == "" {
				if value, ok := parseFilterBool(filterMap["unassigned"]); ok {
					unassigned = strconv.FormatBool(value)
				}
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
		Source:    source,
		Search:    search,
		Tags:      tagsFilter,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	}
	if parsed, ok := parseBoolPtr(slaBreached); ok {
		filters.SLABreached = parsed
	}
	if parsed, ok := parseBoolPtr(isOverdue); ok {
		filters.IsOverdue = parsed
	}
	if parsed, ok := parseBoolPtr(unassigned); ok {
		filters.Unassigned = parsed
	}

	if assignedTo != "" {
		if assignedToID, err := strconv.ParseUint(assignedTo, 10, 32); err == nil {
			id := uint(assignedToID)
			filters.AssigneeID = &id
		}
	}
	if parsed, ok := parseBoolPtr(assignedToMe); ok && *parsed {
		if userID, exists := c.Get("user_id"); exists {
			if id, valid := userID.(uint); valid {
				filters.AssigneeID = &id
			}
		}
	}

	if createdBy != "" {
		if createdByID, err := strconv.ParseUint(createdBy, 10, 32); err == nil {
			id := uint(createdByID)
			filters.CreatorID = &id
		}
	}

	// 对象级可见性：客户只能列出自己创建的工单。显式 created_by
	// 参数不能扩大其可见范围。
	if isRequesterRole(normalizedProjectRole(c)) {
		if userID := c.GetUint("user_id"); userID > 0 {
			filters.CreatorID = &userID
		}
	}

	// 获取工单列表
	tickets, total, err := h.ticketService.GetTickets(ctx, filters)
	if err != nil {
		logHandlerFailure(c, "ticket.list", err)
		h.response.InternalServerError(c, "获取工单列表失败")
		return
	}

	responses := ticketListResponseForRole(tickets, normalizedProjectRole(c))

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

func parseBoolPtr(value string) (*bool, bool) {
	if value == "" {
		return nil, false
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return nil, false
	}
	return &parsed, true
}

func parseFilterBool(value interface{}) (bool, bool) {
	if value == nil {
		return false, false
	}
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return false, false
		}
		return parsed, true
	default:
		return false, false
	}
}

// GetTicket 获取单个工单
func (h *TicketHandler) GetTicket(c *gin.Context) {
	ctx := c.Request.Context()

	// 解析工单ID
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		h.response.BadRequest(c, "无效的工单ID")
		return
	}

	// 获取工单
	ticket, err := authorizeTicket(ctx, c, h.ticketService, uint(id), ticketAccessRead)
	if err != nil {
		if writeTicketAuthorizationError(c, err) {
			return
		}
		if err.Error() == "ticket not found" {
			h.response.NotFound(c, "工单不存在")
			return
		}
		logHandlerFailure(c, "ticket.get", err)
		h.response.InternalServerError(c, "获取工单失败")
		return
	}

	setTicketETag(c, ticket.Version)
	h.response.Success(c, ticketResponseForRole(ticket, normalizedProjectRole(c)), "获取工单成功")
}

// CreateTicket 创建工单
func (h *TicketHandler) CreateTicket(c *gin.Context) {
	ctx := c.Request.Context()

	// 解析请求体
	var req models.TicketCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.response.BadRequest(c, "请求格式错误")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Description = strings.TrimSpace(req.Description)
	switch {
	case req.Title == "":
		writeHumanTicketProblem(
			c,
			http.StatusBadRequest,
			"invalid_request",
			"工单内容无效",
			"工单标题不能为空",
			false,
		)
		return
	case req.Description == "":
		writeHumanTicketProblem(
			c,
			http.StatusBadRequest,
			"invalid_request",
			"工单内容无效",
			"工单描述不能为空",
			false,
		)
		return
	}
	if isRequesterRole(normalizedProjectRole(c)) && (req.Status != nil || req.AssignedToID != nil) {
		h.response.Forbidden(c, "客户创建工单时不能指定状态或处理人")
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
		switch {
		case errors.Is(err, services.ErrTicketCreateAccessDenied):
			writeHumanTicketProblem(
				c,
				http.StatusForbidden,
				"ticket_create_access_denied",
				"无权创建工单",
				"当前用户没有该项目的有效建单成员权限",
				false,
			)
			return
		case errors.Is(err, services.ErrTicketFormValidation):
			writeHumanTicketProblem(
				c,
				http.StatusUnprocessableEntity,
				"ticket_form_validation_failed",
				"工单表单校验失败",
				"提交内容不符合所选请求类型的已发布表单",
				false,
			)
			return
		case errors.Is(err, services.ErrTicketRequestTypeAmbiguous):
			writeHumanTicketProblem(
				c,
				http.StatusBadRequest,
				"request_type_version_required",
				"必须选择请求类型",
				"请选择当前项目已发布的请求类型版本",
				false,
			)
			return
		case errors.Is(err, services.ErrTicketConfigurationUnavailable):
			writeHumanTicketProblem(
				c,
				http.StatusConflict,
				"ticket_configuration_unavailable",
				"项目配置不可用",
				"当前项目没有完整的已发布建单配置",
				false,
			)
			return
		}
		logHandlerFailure(c, "ticket.create", err)
		h.response.InternalServerError(c, "创建工单失败")
		return
	}

	setTicketETag(c, ticket.Version)
	h.response.Created(c, ticketResponseForRole(ticket, normalizedProjectRole(c)), "工单创建成功")
}

// UpdateTicket 更新工单
func (h *TicketHandler) UpdateTicket(c *gin.Context) {
	ctx := c.Request.Context()

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
		h.response.BadRequest(c, "请求格式错误")
		return
	}
	if req.CustomerEmail != nil && *req.CustomerEmail != "" {
		address, err := mail.ParseAddress(*req.CustomerEmail)
		if err != nil || address.Address != *req.CustomerEmail {
			h.response.BadRequest(c, "客户邮箱格式错误")
			return
		}
	}

	_, err = authorizeTicket(ctx, c, h.ticketService, uint(id), ticketAccessUpdate)
	if err != nil {
		if writeTicketAuthorizationError(c, err) {
			return
		}
		if err.Error() == "ticket not found" {
			h.response.NotFound(c, "工单不存在")
			return
		}
		logHandlerFailure(c, "ticket.authorize_update", err)
		h.response.InternalServerError(c, "工单权限检查失败")
		return
	}
	if isRequesterRole(normalizedProjectRole(c)) &&
		(req.Status != nil || req.AssignedToID != nil || req.InternalNotes != nil) {
		h.response.Forbidden(c, "客户不能修改工单状态、处理人或内部备注")
		return
	}
	expectedVersion, ok := requireTicketIfMatch(c)
	if !ok {
		return
	}

	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		h.response.Unauthorized(c, "用户未认证")
		return
	}

	// 更新工单
	ticket, err := h.ticketService.UpdateTicketExpectedVersion(
		ctx,
		uint(id),
		&req,
		userID.(uint),
		expectedVersion,
	)
	if err != nil {
		if errors.Is(err, services.ErrVersionConflict) {
			writeTicketVersionConflict(c)
			return
		}
		if errors.Is(err, services.ErrInvalidTicketTransition) {
			h.response.BadRequest(c, "当前工单状态不允许执行该流转")
			return
		}
		if err.Error() == "ticket not found" {
			h.response.NotFound(c, "工单不存在")
			return
		}
		logHandlerFailure(c, "ticket.update", err)
		h.response.InternalServerError(c, "更新工单失败")
		return
	}

	setTicketETag(c, ticket.Version)
	h.response.Success(c, ticketResponseForRole(ticket, normalizedProjectRole(c)), "工单更新成功")
}

// DeleteTicket 删除工单
func (h *TicketHandler) DeleteTicket(c *gin.Context) {
	ctx := c.Request.Context()

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

	role := normalizedProjectRole(c)
	if !isProjectManagerRole(role) {
		h.response.Forbidden(c, "仅项目管理员或经理可以删除工单")
		return
	}
	expectedVersion, ok := requireTicketIfMatch(c)
	if !ok {
		return
	}

	userID, ok := userIDValue.(uint)
	if !ok {
		h.response.InternalServerError(c, "无效的用户ID")
		return
	}

	// 删除工单
	err = h.ticketService.DeleteTicketExpectedVersion(
		ctx,
		uint(id),
		userID,
		role,
		expectedVersion,
	)
	if err != nil {
		if errors.Is(err, services.ErrVersionConflict) {
			writeTicketVersionConflict(c)
			return
		}
		if err.Error() == "ticket not found" {
			h.response.NotFound(c, "工单不存在")
			return
		}
		logHandlerFailure(c, "ticket.delete", err)
		h.response.InternalServerError(c, "删除工单失败")
		return
	}

	setTicketETag(c, expectedVersion+1)
	h.response.Success(c, nil, "工单删除成功")
}

// BulkUpdateTickets 批量更新工单
func (h *TicketHandler) BulkUpdateTickets(c *gin.Context) {
	ctx := c.Request.Context()

	// 解析请求体
	var req struct {
		Tickets []services.TicketVersionPrecondition `json:"tickets" binding:"required,min=1,max=100,dive"`
		Updates models.TicketUpdateRequest           `json:"updates" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.response.Error(c, http.StatusBadRequest, "invalid_request", "请求格式无效")
		return
	}

	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		h.response.Error(c, http.StatusUnauthorized, "unauthorized", "用户未登录")
		return
	}
	if !isProjectManagerRole(normalizedProjectRole(c)) {
		h.response.Forbidden(c, "仅项目管理员或经理可以批量更新工单")
		return
	}

	// 创建批量更新请求
	bulkReq := &services.BulkUpdateRequest{
		Tickets: req.Tickets,
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
	result, err := h.ticketService.BulkUpdateTickets(ctx, bulkReq, userID.(uint))
	if err != nil {
		if errors.Is(err, services.ErrVersionConflict) {
			h.response.Error(c, http.StatusConflict, "version_conflict", "批量更新遇到并发修改，所有工单均未更新")
			return
		}
		if errors.Is(err, services.ErrInvalidTicketTransition) {
			h.response.Error(c, http.StatusBadRequest, "invalid_status_transition", "工单状态流转无效")
			return
		}
		if errors.Is(err, services.ErrInvalidBulkTicketUpdate) {
			h.response.Error(c, http.StatusBadRequest, "invalid_bulk_update", "批量更新内容无效")
			return
		}
		logHandlerFailure(c, "ticket.bulk_update", err)
		h.response.Error(c, http.StatusInternalServerError, "bulk_update_failed", "批量更新工单失败")
		return
	}

	h.response.Success(c, result, "工单批量更新成功")
}

// BulkDeleteTickets 批量删除工单
func (h *TicketHandler) BulkDeleteTickets(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		Tickets []services.TicketVersionPrecondition `json:"tickets" binding:"required,min=1,max=100,dive"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.response.Error(c, http.StatusBadRequest, "invalid_request", "请求格式无效")
		return
	}

	userIDValue, exists := c.Get("user_id")
	if !exists {
		h.response.Error(c, http.StatusUnauthorized, "unauthorized", "用户未登录")
		return
	}
	userID, ok := userIDValue.(uint)
	if !ok {
		h.response.InternalServerError(c, "无效的用户ID")
		return
	}

	projectRole := normalizedProjectRole(c)
	if !isProjectManagerRole(projectRole) {
		h.response.Forbidden(c, "仅项目管理员或经理可以批量删除工单")
		return
	}

	deletedIDs := make([]uint, 0, len(req.Tickets))
	deletedTickets := make([]services.TicketVersionReceipt, 0, len(req.Tickets))
	failedIDs := make([]uint, 0)
	failedReasons := make(map[string]string)

	for _, precondition := range req.Tickets {
		if err := h.ticketService.DeleteTicketExpectedVersion(
			ctx,
			precondition.ID,
			userID,
			projectRole,
			precondition.Version,
		); err != nil {
			failedIDs = append(failedIDs, precondition.ID)
			reason := "internal_error"
			switch {
			case errors.Is(err, services.ErrVersionConflict):
				reason = "version_conflict"
			case err.Error() == "ticket not found":
				reason = "ticket_not_found"
			default:
				logHandlerFailure(c, "ticket.bulk_delete_item", err)
			}
			failedReasons[strconv.FormatUint(uint64(precondition.ID), 10)] = reason
			continue
		}
		deletedIDs = append(deletedIDs, precondition.ID)
		deletedTickets = append(deletedTickets, services.TicketVersionReceipt{
			ID:      precondition.ID,
			Version: precondition.Version + 1,
		})
	}

	result := map[string]interface{}{
		"deleted_ids":     deletedIDs,
		"deleted_tickets": deletedTickets,
		"failed_ids":      failedIDs,
	}
	if len(failedReasons) > 0 {
		result["failed_reasons"] = failedReasons
	}

	if len(failedIDs) > 0 && len(deletedIDs) == 0 {
		h.response.Error(c, http.StatusBadRequest, "bulk_delete_failed", result)
		return
	}
	if len(failedIDs) > 0 {
		h.response.Success(c, result, "部分工单删除失败")
		return
	}

	h.response.Success(c, result, "工单批量删除成功")
}

func normalizePagination(page, pageSize, maxPageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return page, pageSize
}
