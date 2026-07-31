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

	limit := 10
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}
	_, limit = normalizePagination(1, limit, 100)

	status := c.Query("status")
	priority := c.Query("priority")

	var (
		tickets []*models.Ticket
		total   int64
		err     error
	)
	if isRequesterRole(role) {
		tickets, total, err = h.ticketService.GetTickets(c.Request.Context(), services.TicketFilters{
			Page:      1,
			Limit:     limit,
			Status:    status,
			Priority:  priority,
			CreatorID: &userID,
			SortBy:    "created_at",
			SortOrder: "desc",
		})
	} else if isProjectAgentRole(role) || isProjectManagerRole(role) {
		tickets, total, err = h.ticketService.GetUserTickets(
			c.Request.Context(),
			userID,
			status,
			priority,
			limit,
		)
	} else {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "当前身份不能查询工单",
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

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticketListResponseForRole(tickets, role),
		"total":   total,
	})
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
	limit := 20
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}
	_, limit = normalizePagination(1, limit, 100)

	priority := c.Query("priority")
	categoryID := c.Query("category_id")

	tickets, total, err := h.ticketService.GetUnassignedTickets(
		c.Request.Context(),
		priority,
		categoryID,
		limit,
	)
	if err != nil {
		logHandlerFailure(c, "ticket_workflow.get_unassigned_tickets", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取未分配工单失败",
			"error":   "internal_error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticketListResponseForRole(tickets, role),
		"total":   total,
	})
}

func (h *TicketWorkflowHandler) GetOverdueTickets(c *gin.Context) {
	userID := c.GetUint("user_id")
	role := normalizedProjectRole(c)

	tickets, total, err := h.ticketService.GetOverdueTickets(
		c.Request.Context(),
		userID,
		role,
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

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticketListResponseForRole(tickets, role),
		"total":   total,
	})
}

func (h *TicketWorkflowHandler) GetSLABreachedTickets(c *gin.Context) {
	userID := c.GetUint("user_id")
	role := normalizedProjectRole(c)

	tickets, total, err := h.ticketService.GetSLABreachedTickets(
		c.Request.Context(),
		userID,
		role,
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

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticketListResponseForRole(tickets, role),
		"total":   total,
	})
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

	history, _, err := h.ticketService.GetTicketHistory(
		c.Request.Context(),
		uint(ticketID),
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
	customer := isRequesterRole(normalizedProjectRole(c))
	responses := ticketHistoryResponses(history, customer)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    responses,
		"total":   len(responses),
	})
}
