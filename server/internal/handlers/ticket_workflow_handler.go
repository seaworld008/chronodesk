package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
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
	Comment      string `json:"comment"`
}

type TransferRequest struct {
	AssignedToID   uint   `json:"assigned_to_id" binding:"required"`
	Department     string `json:"department"`
	Comment        string `json:"comment"`
	TransferReason string `json:"transfer_reason"`
}

type EscalationRequest struct {
	Reason       string `json:"reason" binding:"required"`
	EscalateToID uint   `json:"escalate_to_id" binding:"required"`
	Comment      string `json:"comment"`
}

type StatusUpdateRequest struct {
	Status          string `json:"status" binding:"required"`
	Comment         string `json:"comment"`
	ResolutionNotes string `json:"resolution_notes"`
}

type BulkAssignRequest struct {
	TicketIDs    []uint `json:"ticket_ids" binding:"required"`
	AssignedToID uint   `json:"assigned_to_id" binding:"required"`
	Comment      string `json:"comment"`
}

type BulkStatusRequest struct {
	TicketIDs []uint `json:"ticket_ids" binding:"required"`
	Status    string `json:"status" binding:"required"`
	Comment   string `json:"comment"`
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

	userID := c.GetUint("user_id")
	ticket, err := h.ticketService.AssignTicket(uint(ticketID), req.AssignedToID, userID, req.Comment)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "工单分配失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticket.ToResponse(),
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

	userID := c.GetUint("user_id")
	ticket, err := h.ticketService.TransferTicket(uint(ticketID), req.AssignedToID, userID, req.Comment, req.TransferReason)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "工单转移失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticket.ToResponse(),
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

	userID := c.GetUint("user_id")
	ticket, err := h.ticketService.EscalateTicket(uint(ticketID), req.EscalateToID, userID, req.Reason, req.Comment)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "工单升级失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticket.ToResponse(),
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

	userID := c.GetUint("user_id")
	ticket, err := h.ticketService.UpdateTicketStatus(uint(ticketID), req.Status, userID, req.Comment, req.ResolutionNotes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "状态更新失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ticket.ToResponse(),
		"message": "状态更新成功",
	})
}

func (h *TicketWorkflowHandler) GetTicketStats(c *gin.Context) {
	userID := c.GetUint("user_id")
	role := c.GetString("role")

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

	limit := 10
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	status := c.Query("status")
	priority := c.Query("priority")

	tickets, total, err := h.ticketService.GetUserTickets(userID, status, priority, limit)
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
		"data":    tickets,
		"total":   total,
	})
}

func (h *TicketWorkflowHandler) GetUnassignedTickets(c *gin.Context) {
	limit := 20
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

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
		"data":    tickets,
		"total":   total,
	})
}

func (h *TicketWorkflowHandler) GetOverdueTickets(c *gin.Context) {
	userID := c.GetUint("user_id")
	role := c.GetString("role")

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
		"data":    tickets,
		"total":   total,
	})
}

func (h *TicketWorkflowHandler) GetSLABreachedTickets(c *gin.Context) {
	userID := c.GetUint("user_id")
	role := c.GetString("role")

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
		"data":    tickets,
		"total":   total,
	})
}

func (h *TicketWorkflowHandler) BulkAssignTickets(c *gin.Context) {
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

	history, total, err := h.ticketService.GetTicketHistory(uint(ticketID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取工单历史失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    history,
		"total":   total,
	})
}
