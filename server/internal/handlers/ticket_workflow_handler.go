package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type TicketWorkflowHandler struct {
	ticketService services.TicketServiceInterface
}

type ticketWorkflowTransitionReader interface {
	AllowedTicketTransitions(
		context.Context,
		uint,
		uint,
	) ([]models.TicketStatus, error)
}

func NewTicketWorkflowHandler(ticketService services.TicketServiceInterface) *TicketWorkflowHandler {
	return &TicketWorkflowHandler{
		ticketService: ticketService,
	}
}

type AssignRequest struct {
	AssignedToID uint   `json:"assigned_to_id" binding:"required"`
	Comment      string `json:"comment" binding:"max=2000"`
}

type TransferRequest struct {
	AssignedToID   uint   `json:"assigned_to_id" binding:"required"`
	Department     string `json:"department" binding:"max=100"`
	Comment        string `json:"comment" binding:"max=2000"`
	TransferReason string `json:"transfer_reason" binding:"max=500"`
}

type EscalationRequest struct {
	Reason       string `json:"reason" binding:"required,max=500"`
	EscalateToID uint   `json:"escalate_to_id" binding:"required"`
	Comment      string `json:"comment" binding:"max=2000"`
}

type StatusUpdateRequest struct {
	Status          string `json:"status" binding:"required,oneof=open in_progress pending resolved closed cancelled"`
	Comment         string `json:"comment" binding:"max=2000"`
	ResolutionNotes string `json:"resolution_notes" binding:"max=10000"`
}

// GetAllowedTicketTransitions returns the lifecycle projections allowed by
// the immutable workflow version bound to this exact Ticket.
func (h *TicketWorkflowHandler) GetAllowedTicketTransitions(c *gin.Context) {
	ticketID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的工单ID",
		})
		return
	}
	if _, err := authorizeTicket(
		c.Request.Context(),
		c,
		h.ticketService,
		uint(ticketID),
		ticketAccessWorkflow,
	); err != nil {
		if !writeTicketAuthorizationError(c, err) {
			c.JSON(http.StatusNotFound, gin.H{
				"success": false,
				"message": "工单不存在",
			})
		}
		return
	}
	reader, ok := h.ticketService.(ticketWorkflowTransitionReader)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "工单工作流能力不可用",
		})
		return
	}
	allowed, err := reader.AllowedTicketTransitions(
		c.Request.Context(),
		uint(ticketID),
		c.GetUint("user_id"),
	)
	if err != nil {
		logHandlerFailure(c, "ticket_workflow.allowed_transitions", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取合法工单状态失败",
		})
		return
	}
	values := make([]string, len(allowed))
	for index, status := range allowed {
		values[index] = string(status)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"allowed_next_statuses": values,
		},
	})
}

func (h *TicketWorkflowHandler) AssignTicket(c *gin.Context) {
	ticketID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的工单ID",
		})
		return
	}

	var req AssignRequest
	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数无效",
			"error":   "invalid_request",
		})
		return
	}
	_, err = authorizeTicket(c.Request.Context(), c, h.ticketService, uint(ticketID), ticketAccessWorkflow)
	if err != nil {
		if !writeTicketAuthorizationError(c, err) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "工单不存在"})
		}
		return
	}
	expectedVersion, ok := requireTicketIfMatch(c)
	if !ok {
		return
	}
	userID := c.GetUint("user_id")
	ticket, err := h.ticketService.AssignTicketExpectedVersion(
		c.Request.Context(),
		uint(ticketID),
		req.AssignedToID,
		userID,
		req.Comment,
		expectedVersion,
	)
	if err != nil {
		if errors.Is(err, services.ErrVersionConflict) {
			writeTicketVersionConflict(c)
			return
		}
		logHandlerFailure(c, "ticket_workflow.assign", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "工单分配失败",
			"error":   "internal_error",
		})
		return
	}

	setTicketETag(c, ticket.Version)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticketResponseForRole(ticket, normalizedProjectRole(c)),
		"message": "工单分配成功",
	})
}

func (h *TicketWorkflowHandler) TransferTicket(c *gin.Context) {
	ticketID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的工单ID",
		})
		return
	}

	var req TransferRequest
	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数无效",
			"error":   "invalid_request",
		})
		return
	}
	_, err = authorizeTicket(c.Request.Context(), c, h.ticketService, uint(ticketID), ticketAccessWorkflow)
	if err != nil {
		if !writeTicketAuthorizationError(c, err) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "工单不存在"})
		}
		return
	}
	expectedVersion, ok := requireTicketIfMatch(c)
	if !ok {
		return
	}
	userID := c.GetUint("user_id")
	ticket, err := h.ticketService.TransferTicketExpectedVersion(
		c.Request.Context(),
		uint(ticketID),
		req.AssignedToID,
		userID,
		req.Comment,
		req.TransferReason,
		expectedVersion,
	)
	if err != nil {
		if errors.Is(err, services.ErrVersionConflict) {
			writeTicketVersionConflict(c)
			return
		}
		logHandlerFailure(c, "ticket_workflow.transfer", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "工单转移失败",
			"error":   "internal_error",
		})
		return
	}

	setTicketETag(c, ticket.Version)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticketResponseForRole(ticket, normalizedProjectRole(c)),
		"message": "工单转移成功",
	})
}

func (h *TicketWorkflowHandler) EscalateTicket(c *gin.Context) {
	ticketID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的工单ID",
		})
		return
	}

	var req EscalationRequest
	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数无效",
			"error":   "invalid_request",
		})
		return
	}
	_, err = authorizeTicket(c.Request.Context(), c, h.ticketService, uint(ticketID), ticketAccessWorkflow)
	if err != nil {
		if !writeTicketAuthorizationError(c, err) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "工单不存在"})
		}
		return
	}
	expectedVersion, ok := requireTicketIfMatch(c)
	if !ok {
		return
	}
	userID := c.GetUint("user_id")
	ticket, err := h.ticketService.EscalateTicketExpectedVersion(
		c.Request.Context(),
		uint(ticketID),
		req.EscalateToID,
		userID,
		req.Reason,
		req.Comment,
		expectedVersion,
	)
	if err != nil {
		if errors.Is(err, services.ErrVersionConflict) {
			writeTicketVersionConflict(c)
			return
		}
		logHandlerFailure(c, "ticket_workflow.escalate", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "工单升级失败",
			"error":   "internal_error",
		})
		return
	}

	setTicketETag(c, ticket.Version)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticketResponseForRole(ticket, normalizedProjectRole(c)),
		"message": "工单升级成功",
	})
}

func (h *TicketWorkflowHandler) UpdateTicketStatus(c *gin.Context) {
	ticketID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的工单ID",
		})
		return
	}

	var req StatusUpdateRequest
	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数无效",
			"error":   "invalid_request",
		})
		return
	}
	currentTicket, err := authorizeTicket(c.Request.Context(), c, h.ticketService, uint(ticketID), ticketAccessWorkflow)
	if err != nil {
		if !writeTicketAuthorizationError(c, err) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "工单不存在"})
		}
		return
	}
	expectedVersion, ok := requireTicketIfMatch(c)
	if !ok {
		return
	}
	userID := c.GetUint("user_id")
	ticket, err := h.ticketService.UpdateTicketStatusExpectedVersion(
		c.Request.Context(),
		uint(ticketID),
		req.Status,
		userID,
		req.Comment,
		req.ResolutionNotes,
		expectedVersion,
	)
	if err != nil {
		if errors.Is(err, services.ErrVersionConflict) {
			writeTicketVersionConflict(c)
			return
		}
		if errors.Is(err, services.ErrInvalidTicketTransition) {
			allowed := []models.TicketStatus{}
			if reader, supportsVersionedWorkflow :=
				h.ticketService.(ticketWorkflowTransitionReader); supportsVersionedWorkflow {
				if configured, readErr := reader.AllowedTicketTransitions(
					c.Request.Context(),
					uint(ticketID),
					userID,
				); readErr == nil {
					allowed = configured
				}
			}
			allowedValues := make([]string, len(allowed))
			for index, status := range allowed {
				allowedValues[index] = string(status)
			}
			writeHumanTicketProblem(
				c,
				http.StatusConflict,
				"invalid_status_transition",
				"工单状态流转冲突",
				"当前工单状态不允许执行该流转",
				false,
				map[string]any{
					"current_status":        currentTicket.Status,
					"requested_status":      req.Status,
					"allowed_next_statuses": allowedValues,
				},
			)
			return
		}
		logHandlerFailure(c, "ticket_workflow.update_status", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "状态更新失败",
			"error":   "internal_error",
		})
		return
	}

	setTicketETag(c, ticket.Version)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticketResponseForRole(ticket, normalizedProjectRole(c)),
		"message": "状态更新成功",
	})
}

func (h *TicketWorkflowHandler) GetTicketStats(c *gin.Context) {
	userID := c.GetUint("user_id")
	role := normalizedProjectRole(c)

	stats, err := h.ticketService.GetTicketStatistics(c.Request.Context(), userID, role)
	if err != nil {
		logHandlerFailure(c, "ticket_workflow.get_statistics", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取统计数据失败",
			"error":   "internal_error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

func (h *TicketWorkflowHandler) GetMyTickets(c *gin.Context) {
	userID := c.GetUint("user_id")
	role := normalizedProjectRole(c)
	query, ok := requireTicketPreviewListQuery(
		c,
		map[string]struct{}{
			"status":   {},
			"priority": {},
		},
	)
	if !ok {
		return
	}
	status := query.values.Get("status")
	priority := query.values.Get("priority")
	page := services.DirectoryPageRequest{
		Page:      query.Page,
		PageSize:  query.PageSize,
		SortBy:    query.SortBy,
		SortOrder: query.SortOrder,
	}

	var (
		tickets []*models.Ticket
		total   int64
		err     error
	)
	if isRequesterRole(role) {
		tickets, total, err = h.ticketService.GetTickets(c.Request.Context(), services.TicketFilters{
			Page:      query.Page,
			Limit:     query.PageSize,
			Status:    status,
			Priority:  priority,
			CreatorID: &userID,
			SortBy:    query.SortBy,
			SortOrder: query.SortOrder,
		})
	} else if isProjectAgentRole(role) || isProjectManagerRole(role) {
		tickets, total, err = h.ticketService.GetUserTickets(
			c.Request.Context(),
			userID,
			status,
			priority,
			page,
		)
	} else {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "当前身份不能查询工单",
		})
		return
	}
	if errors.Is(err, services.ErrInvalidTicketListQuery) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "我的工单查询参数无效",
			"error":   "invalid_query",
		})
		return
	}
	if err != nil {
		logHandlerFailure(c, "ticket_workflow.get_my_tickets", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取我的工单失败",
			"error":   "internal_error",
		})
		return
	}

	writePageEnvelope(
		c,
		ticketListResponseForRole(tickets, role),
		total,
		query.Page,
		query.PageSize,
	)
}

func (h *TicketWorkflowHandler) GetUnassignedTickets(c *gin.Context) {
	role := normalizedProjectRole(c)
	if !isProjectAgentRole(role) && !isProjectManagerRole(role) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "当前身份不能查看未分配队列",
		})
		return
	}
	query, ok := requireTicketPreviewListQuery(
		c,
		map[string]struct{}{
			"priority":    {},
			"category_id": {},
		},
	)
	if !ok {
		return
	}
	priority := query.values.Get("priority")
	categoryID := query.values.Get("category_id")

	tickets, total, err := h.ticketService.GetUnassignedTickets(
		c.Request.Context(),
		priority,
		categoryID,
		services.DirectoryPageRequest{
			Page:      query.Page,
			PageSize:  query.PageSize,
			SortBy:    query.SortBy,
			SortOrder: query.SortOrder,
		},
	)
	if errors.Is(err, services.ErrInvalidTicketListQuery) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "未分配工单查询参数无效",
			"error":   "invalid_query",
		})
		return
	}
	if err != nil {
		logHandlerFailure(c, "ticket_workflow.get_unassigned_tickets", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取未分配工单失败",
			"error":   "internal_error",
		})
		return
	}

	writePageEnvelope(
		c,
		ticketListResponseForRole(tickets, role),
		total,
		query.Page,
		query.PageSize,
	)
}

func requireTicketPreviewListQuery(
	c *gin.Context,
	filterFields map[string]struct{},
) (directoryListQuery, bool) {
	query, err := parseDirectoryListQuery(
		c.Request.URL.RawQuery,
		directoryListQuerySpec{
			DefaultSortBy:    "created_at",
			DefaultSortOrder: "desc",
			SortFields:       map[string]struct{}{"created_at": {}},
			FilterFields:     filterFields,
		},
	)
	if err != nil {
		writeInvalidTicketPreviewListQuery(c)
		return directoryListQuery{}, false
	}
	if status, ok := query.values["status"]; ok &&
		!validTicketListValues(status[0], validTicketStatusFilter) {
		writeInvalidTicketPreviewListQuery(c)
		return directoryListQuery{}, false
	}
	if priority, ok := query.values["priority"]; ok &&
		!validTicketListValues(priority[0], validTicketPriorityFilter) {
		writeInvalidTicketPreviewListQuery(c)
		return directoryListQuery{}, false
	}
	if categoryID, ok := query.values["category_id"]; ok {
		parsed, parseErr := strconv.ParseUint(categoryID[0], 10, 32)
		if parseErr != nil || parsed == 0 {
			writeInvalidTicketPreviewListQuery(c)
			return directoryListQuery{}, false
		}
	}
	return query, true
}

func writeInvalidTicketPreviewListQuery(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{
		"success": false,
		"message": "工单预览查询参数无效",
		"error":   "invalid_query",
	})
}

func (h *TicketWorkflowHandler) GetOverdueTickets(c *gin.Context) {
	userID := c.GetUint("user_id")
	role := normalizedProjectRole(c)
	page, pageSize, ok := parseStrictPagePagination(c, 25, 100)
	if !ok {
		return
	}

	tickets, total, err := h.ticketService.GetOverdueTickets(
		c.Request.Context(),
		userID,
		role,
		services.PageRequest{Page: page, PageSize: pageSize},
	)
	if err != nil {
		logHandlerFailure(c, "ticket_workflow.get_overdue_tickets", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取逾期工单失败",
			"error":   "internal_error",
		})
		return
	}

	writePageEnvelope(c, ticketListResponseForRole(tickets, role), total, page, pageSize)
}

func (h *TicketWorkflowHandler) GetSLABreachedTickets(c *gin.Context) {
	userID := c.GetUint("user_id")
	role := normalizedProjectRole(c)
	page, pageSize, ok := parseStrictPagePagination(c, 25, 100)
	if !ok {
		return
	}

	tickets, total, err := h.ticketService.GetSLABreachedTickets(
		c.Request.Context(),
		userID,
		role,
		services.PageRequest{Page: page, PageSize: pageSize},
	)
	if err != nil {
		logHandlerFailure(c, "ticket_workflow.get_sla_breached_tickets", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取SLA违约工单失败",
			"error":   "internal_error",
		})
		return
	}

	writePageEnvelope(c, ticketListResponseForRole(tickets, role), total, page, pageSize)
}

func (h *TicketWorkflowHandler) GetTicketHistory(c *gin.Context) {
	ticketID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的工单ID",
		})
		return
	}
	if _, err := authorizeTicket(c.Request.Context(), c, h.ticketService, uint(ticketID), ticketAccessRead); err != nil {
		if !writeTicketAuthorizationError(c, err) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "工单不存在"})
		}
		return
	}
	page, pageSize, ok := parseStrictPagePagination(c, 25, 100)
	if !ok {
		return
	}

	history, total, err := h.ticketService.GetTicketHistory(
		c.Request.Context(),
		uint(ticketID),
		services.PageRequest{
			Page:     page,
			PageSize: pageSize,
			CustomerVisible: isPublicTicketContentOnlyRole(
				normalizedProjectRole(c),
			),
		},
	)
	if err != nil {
		logHandlerFailure(c, "ticket_workflow.get_history", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取工单历史失败",
			"error":   "internal_error",
		})
		return
	}
	publicOnly := isPublicTicketContentOnlyRole(normalizedProjectRole(c))
	responses := ticketHistoryResponses(history, publicOnly)

	writePageEnvelope(c, responses, total, page, pageSize)
}
