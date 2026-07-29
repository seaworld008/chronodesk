package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gongdan-system/internal/models"
	"gongdan-system/internal/services"
)

type TicketWorkflowHandler struct {
	ticketService services.TicketServiceInterface
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

type BulkAssignRequest struct {
	TicketIDs    []uint `json:"ticket_ids" binding:"required,min=1,max=100,unique,dive,gt=0"`
	AssignedToID uint   `json:"assigned_to_id" binding:"required"`
	Comment      string `json:"comment" binding:"max=2000"`
}

type BulkStatusRequest struct {
	TicketIDs []uint `json:"ticket_ids" binding:"required,min=1,max=100,unique,dive,gt=0"`
	Status    string `json:"status" binding:"required,oneof=open in_progress pending resolved closed cancelled"`
	Comment   string `json:"comment" binding:"max=2000"`
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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数无效",
			"error":   err.Error(),
		})
		return
	}
	authorizedTicket, err := authorizeTicket(c.Request.Context(), c, h.ticketService, uint(ticketID), ticketAccessWorkflow)
	if err != nil {
		if !writeTicketAuthorizationError(c, err) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "工单不存在"})
		}
		return
	}
	versionedService, ok := h.ticketService.(TicketVersionedService)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "code": "version_guard_unavailable", "message": "工单服务未启用授权版本校验"})
		return
	}

	userID := c.GetUint("user_id")
	ticket, err := versionedService.AssignTicketExpectedVersion(
		uint(ticketID),
		req.AssignedToID,
		userID,
		req.Comment,
		authorizedTicket.Version,
	)
	if err != nil {
		if errors.Is(err, services.ErrVersionConflict) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "version_conflict", "message": "工单已被并发更新"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "工单分配失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticketResponseForRole(ticket, normalizedUserRole(c)),
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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数无效",
			"error":   err.Error(),
		})
		return
	}
	authorizedTicket, err := authorizeTicket(c.Request.Context(), c, h.ticketService, uint(ticketID), ticketAccessWorkflow)
	if err != nil {
		if !writeTicketAuthorizationError(c, err) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "工单不存在"})
		}
		return
	}
	versionedService, ok := h.ticketService.(TicketVersionedService)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "code": "version_guard_unavailable", "message": "工单服务未启用授权版本校验"})
		return
	}

	userID := c.GetUint("user_id")
	ticket, err := versionedService.TransferTicketExpectedVersion(
		uint(ticketID),
		req.AssignedToID,
		userID,
		req.Comment,
		req.TransferReason,
		authorizedTicket.Version,
	)
	if err != nil {
		if errors.Is(err, services.ErrVersionConflict) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "version_conflict", "message": "工单已被并发更新"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "工单转移失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticketResponseForRole(ticket, normalizedUserRole(c)),
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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数无效",
			"error":   err.Error(),
		})
		return
	}
	authorizedTicket, err := authorizeTicket(c.Request.Context(), c, h.ticketService, uint(ticketID), ticketAccessWorkflow)
	if err != nil {
		if !writeTicketAuthorizationError(c, err) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "工单不存在"})
		}
		return
	}
	versionedService, ok := h.ticketService.(TicketVersionedService)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "code": "version_guard_unavailable", "message": "工单服务未启用授权版本校验"})
		return
	}

	userID := c.GetUint("user_id")
	ticket, err := versionedService.EscalateTicketExpectedVersion(
		uint(ticketID),
		req.EscalateToID,
		userID,
		req.Reason,
		req.Comment,
		authorizedTicket.Version,
	)
	if err != nil {
		if errors.Is(err, services.ErrVersionConflict) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "version_conflict", "message": "工单已被并发更新"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "工单升级失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticketResponseForRole(ticket, normalizedUserRole(c)),
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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数无效",
			"error":   err.Error(),
		})
		return
	}
	authorizedTicket, err := authorizeTicket(c.Request.Context(), c, h.ticketService, uint(ticketID), ticketAccessWorkflow)
	if err != nil {
		if !writeTicketAuthorizationError(c, err) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "工单不存在"})
		}
		return
	}
	versionedService, ok := h.ticketService.(TicketVersionedService)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "code": "version_guard_unavailable", "message": "工单服务未启用授权版本校验"})
		return
	}

	userID := c.GetUint("user_id")
	ticket, err := versionedService.UpdateTicketStatusExpectedVersion(
		uint(ticketID),
		req.Status,
		userID,
		req.Comment,
		req.ResolutionNotes,
		authorizedTicket.Version,
	)
	if err != nil {
		if errors.Is(err, services.ErrVersionConflict) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "version_conflict", "message": "工单已被并发更新"})
			return
		}
		if errors.Is(err, services.ErrInvalidTicketTransition) {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "无效的状态流转",
				"error":   err.Error(),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "状态更新失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticketResponseForRole(ticket, normalizedUserRole(c)),
		"message": "状态更新成功",
	})
}

func (h *TicketWorkflowHandler) GetTicketStats(c *gin.Context) {
	userID := c.GetUint("user_id")
	role := normalizedUserRole(c)

	stats, err := h.ticketService.GetTicketStatistics(userID, role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取统计数据失败",
			"error":   err.Error(),
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
	role := normalizedUserRole(c)

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
	if isCustomerRole(role) {
		tickets, total, err = h.ticketService.GetTickets(c.Request.Context(), services.TicketFilters{
			Page:      1,
			Limit:     limit,
			Status:    status,
			Priority:  priority,
			CreatorID: &userID,
			SortBy:    "created_at",
			SortOrder: "desc",
		})
	} else if role == "agent" || isPrivilegedRole(role) {
		tickets, total, err = h.ticketService.GetUserTickets(userID, status, priority, limit)
	} else {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "当前身份不能查询工单",
		})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取我的工单失败",
			"error":   err.Error(),
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
	role := normalizedUserRole(c)
	if role != "agent" && !isPrivilegedRole(role) {
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

	tickets, total, err := h.ticketService.GetUnassignedTickets(priority, categoryID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取未分配工单失败",
			"error":   err.Error(),
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
	role := normalizedUserRole(c)

	tickets, total, err := h.ticketService.GetOverdueTickets(userID, role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取逾期工单失败",
			"error":   err.Error(),
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
	role := normalizedUserRole(c)

	tickets, total, err := h.ticketService.GetSLABreachedTickets(userID, role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取SLA违约工单失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticketListResponseForRole(tickets, role),
		"total":   total,
	})
}

func (h *TicketWorkflowHandler) BulkAssignTickets(c *gin.Context) {
	if !isPrivilegedRole(normalizedUserRole(c)) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "仅管理员或主管可以批量分配"})
		return
	}
	var req BulkAssignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数无效",
			"error":   err.Error(),
		})
		return
	}

	userID := c.GetUint("user_id")
	result, err := h.ticketService.BulkAssignTickets(req.TicketIDs, req.AssignedToID, userID, req.Comment)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "批量分配失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
		"message": "批量分配完成",
	})
}

func (h *TicketWorkflowHandler) BulkUpdateStatus(c *gin.Context) {
	if !isPrivilegedRole(normalizedUserRole(c)) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "仅管理员或主管可以批量更新状态"})
		return
	}
	var req BulkStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数无效",
			"error":   err.Error(),
		})
		return
	}

	userID := c.GetUint("user_id")
	result, err := h.ticketService.BulkUpdateStatus(req.TicketIDs, req.Status, userID, req.Comment)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "批量状态更新失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
		"message": "批量状态更新完成",
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

	history, total, err := h.ticketService.GetTicketHistory(uint(ticketID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取工单历史失败",
			"error":   err.Error(),
		})
		return
	}
	customer := isCustomerRole(normalizedUserRole(c))
	responses := ticketHistoryResponses(history, customer)
	total = int64(len(responses))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    responses,
		"total":   total,
	})
}
