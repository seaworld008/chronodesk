package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gongdan-system/internal/models"
	"gongdan-system/internal/services"
)

// AutomationHandler 自动化处理器
type AutomationHandler struct {
	automationService *services.AutomationService
	schedulerService  *services.SchedulerService
}

// NewAutomationHandler 创建自动化处理器
func NewAutomationHandler(db *gorm.DB, schedulerService *services.SchedulerService) *AutomationHandler {
	return &AutomationHandler{
		automationService: services.NewAutomationService(db),
		schedulerService:  schedulerService,
	}
}

// AutomationRule 相关接口

// CreateRule 创建自动化规则
// @Summary 创建自动化规则
// @Description 创建工单自动化规则，支持分配、分类、升级等
// @Tags 自动化
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param rule body models.AutomationRuleRequest true "规则信息"
// @Success 201 {object} map[string]interface{} "成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/rules [post]
func (h *AutomationHandler) CreateRule(c *gin.Context) {
	var req models.AutomationRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   err.Error(),
		})
		return
	}

	userID, _ := c.Get("user_id")
	rule, err := h.automationService.CreateRule(c.Request.Context(), &req, userID.(uint))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "创建规则失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "创建规则成功",
		"data":    rule,
	})
}

// GetRules 获取自动化规则列表
// @Summary 获取自动化规则列表
// @Description 获取自动化规则列表，支持筛选和分页
// @Tags 自动化
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param rule_type query string false "规则类型"
// @Param is_active query boolean false "是否激活"
// @Param page query int false "页码" default(1)
// @Param page_size query int false "页大小" default(20)
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/rules [get]
func (h *AutomationHandler) GetRules(c *gin.Context) {
	ruleType := strings.TrimSpace(c.Query("rule_type"))
	triggerEvent := strings.TrimSpace(c.Query("trigger_event"))
	search := strings.TrimSpace(c.Query("search"))
	var isActive *bool
	if activeStr := c.Query("is_active"); activeStr != "" {
		active := activeStr == "true"
		isActive = &active
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	rules, total, err := h.automationService.GetRules(c.Request.Context(), ruleType, triggerEvent, isActive, search, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取规则列表失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取规则列表成功",
		"data": gin.H{
			"rules":       rules,
			"total":       total,
			"page":        page,
			"page_size":   pageSize,
			"total_pages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// GetRule 获取规则详情
// @Summary 获取规则详情
// @Description 根据ID获取自动化规则详情
// @Tags 自动化
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param id path int true "规则ID"
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 404 {object} map[string]interface{} "规则不存在"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/rules/{id} [get]
func (h *AutomationHandler) GetRule(c *gin.Context) {
	ruleID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的规则ID",
		})
		return
	}

	rule, err := h.automationService.GetRuleByID(c.Request.Context(), uint(ruleID))
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": "获取规则详情失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取规则详情成功",
		"data":    rule,
	})
}

// UpdateRule 更新规则
// @Summary 更新自动化规则
// @Description 更新指定的自动化规则
// @Tags 自动化
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param id path int true "规则ID"
// @Param rule body models.AutomationRuleRequest true "规则信息"
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 404 {object} map[string]interface{} "规则不存在"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/rules/{id} [put]
func (h *AutomationHandler) UpdateRule(c *gin.Context) {
	ruleID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的规则ID",
		})
		return
	}

	var req models.AutomationRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   err.Error(),
		})
		return
	}

	userID, _ := c.Get("user_id")
	err = h.automationService.UpdateRule(c.Request.Context(), uint(ruleID), &req, userID.(uint))
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": "更新规则失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "更新规则成功",
	})
}

// DeleteRule 删除规则
// @Summary 删除自动化规则
// @Description 删除指定的自动化规则
// @Tags 自动化
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param id path int true "规则ID"
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 404 {object} map[string]interface{} "规则不存在"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/rules/{id} [delete]
func (h *AutomationHandler) DeleteRule(c *gin.Context) {
	ruleID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的规则ID",
		})
		return
	}

	err = h.automationService.DeleteRule(c.Request.Context(), uint(ruleID))
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": "删除规则失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "删除规则成功",
	})
}

// GetRuleStats 获取规则统计
// @Summary 获取规则统计
// @Description 获取自动化规则的执行统计信息
// @Tags 自动化
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param id path int true "规则ID"
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 404 {object} map[string]interface{} "规则不存在"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/rules/{id}/stats [get]
func (h *AutomationHandler) GetRuleStats(c *gin.Context) {
	ruleID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的规则ID",
		})
		return
	}

	stats, err := h.automationService.GetRuleStats(c.Request.Context(), uint(ruleID))
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": "获取规则统计失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取规则统计成功",
		"data":    stats,
	})
}

// GetExecutionLogs 获取执行日志
// @Summary 获取自动化执行日志
// @Description 获取自动化规则的执行日志
// @Tags 自动化
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param rule_id query int false "规则ID"
// @Param ticket_id query int false "工单ID"
// @Param success query boolean false "是否成功"
// @Param page query int false "页码" default(1)
// @Param page_size query int false "页大小" default(20)
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/logs [get]
func (h *AutomationHandler) GetExecutionLogs(c *gin.Context) {
	var ruleID, ticketID *uint
	var success *bool

	if ruleIDStr := c.Query("rule_id"); ruleIDStr != "" {
		if id, err := strconv.ParseUint(ruleIDStr, 10, 32); err == nil {
			ruleIDUint := uint(id)
			ruleID = &ruleIDUint
		}
	}

	if ticketIDStr := c.Query("ticket_id"); ticketIDStr != "" {
		if id, err := strconv.ParseUint(ticketIDStr, 10, 32); err == nil {
			ticketIDUint := uint(id)
			ticketID = &ticketIDUint
		}
	}

	if successStr := c.Query("success"); successStr != "" {
		successBool := successStr == "true"
		success = &successBool
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	logs, total, err := h.automationService.GetExecutionLogs(c.Request.Context(), ruleID, ticketID, success, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取执行日志失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取执行日志成功",
		"data": gin.H{
			"logs":        logs,
			"total":       total,
			"page":        page,
			"page_size":   pageSize,
			"total_pages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// SLA配置相关接口

// CreateSLAConfig 创建SLA配置
// @Summary 创建SLA配置
// @Description 创建服务级别协议配置
// @Tags SLA管理
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param config body models.SLAConfigRequest true "SLA配置信息"
// @Success 201 {object} map[string]interface{} "成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/sla [post]
func (h *AutomationHandler) CreateSLAConfig(c *gin.Context) {
	var req models.SLAConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   err.Error(),
		})
		return
	}

	config, err := h.automationService.CreateSLAConfig(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "创建SLA配置失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "创建SLA配置成功",
		"data":    config,
	})
}

// GetSLAConfigs 获取SLA配置列表
// @Summary 获取SLA配置列表
// @Description 获取SLA配置列表
// @Tags SLA管理
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param is_active query boolean false "是否激活"
// @Param page query int false "页码" default(1)
// @Param page_size query int false "页大小" default(20)
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/sla [get]
func (h *AutomationHandler) GetSLAConfigs(c *gin.Context) {
	var isActive *bool
	if activeStr := c.Query("is_active"); activeStr != "" {
		active := activeStr == "true"
		isActive = &active
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	configs, total, err := h.automationService.GetSLAConfigs(c.Request.Context(), isActive, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取SLA配置列表失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取SLA配置列表成功",
		"data": gin.H{
			"configs":     configs,
			"total":       total,
			"page":        page,
			"page_size":   pageSize,
			"total_pages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// Template相关接口

// CreateTemplate 创建工单模板
// @Summary 创建工单模板
// @Description 创建工单模板
// @Tags 模板管理
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param template body models.TicketTemplateRequest true "模板信息"
// @Success 201 {object} map[string]interface{} "成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/templates [post]
func (h *AutomationHandler) CreateTemplate(c *gin.Context) {
	var req models.TicketTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   err.Error(),
		})
		return
	}

	userID, _ := c.Get("user_id")
	template, err := h.automationService.CreateTemplate(c.Request.Context(), &req, userID.(uint))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "创建模板失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "创建模板成功",
		"data":    template,
	})
}

// GetTemplates 获取模板列表
// @Summary 获取工单模板列表
// @Description 获取工单模板列表
// @Tags 模板管理
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param category query string false "分类"
// @Param is_active query boolean false "是否激活"
// @Param page query int false "页码" default(1)
// @Param page_size query int false "页大小" default(20)
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/templates [get]
func (h *AutomationHandler) GetTemplates(c *gin.Context) {
	category := c.Query("category")
	var isActive *bool
	if activeStr := c.Query("is_active"); activeStr != "" {
		active := activeStr == "true"
		isActive = &active
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	templates, total, err := h.automationService.GetTemplates(c.Request.Context(), category, isActive, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取模板列表失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取模板列表成功",
		"data": gin.H{
			"templates":   templates,
			"total":       total,
			"page":        page,
			"page_size":   pageSize,
			"total_pages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// GetTemplate 获取模板详情
// @Summary 获取工单模板详情
// @Description 根据ID获取工单模板详情
// @Tags 模板管理
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param id path int true "模板ID"
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 404 {object} map[string]interface{} "模板不存在"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/templates/{id} [get]
func (h *AutomationHandler) GetTemplate(c *gin.Context) {
	templateID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的模板ID",
		})
		return
	}

	template, err := h.automationService.GetTemplateByID(c.Request.Context(), uint(templateID))
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": "获取模板详情失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取模板详情成功",
		"data":    template,
	})
}

// QuickReply相关接口

// CreateQuickReply 创建快速回复
// @Summary 创建快速回复
// @Description 创建快速回复模板
// @Tags 快速回复
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param reply body models.QuickReplyRequest true "快速回复信息"
// @Success 201 {object} map[string]interface{} "成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/quick-replies [post]
func (h *AutomationHandler) CreateQuickReply(c *gin.Context) {
	var req models.QuickReplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   err.Error(),
		})
		return
	}

	userID, _ := c.Get("user_id")
	reply, err := h.automationService.CreateQuickReply(c.Request.Context(), &req, userID.(uint))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "创建快速回复失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "创建快速回复成功",
		"data":    reply,
	})
}

// GetQuickReplies 获取快速回复列表
// @Summary 获取快速回复列表
// @Description 获取快速回复列表
// @Tags 快速回复
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param category query string false "分类"
// @Param keyword query string false "关键词搜索"
// @Param is_public query boolean false "是否公开"
// @Param page query int false "页码" default(1)
// @Param page_size query int false "页大小" default(20)
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/quick-replies [get]
func (h *AutomationHandler) GetQuickReplies(c *gin.Context) {
	category := c.Query("category")
	keyword := c.Query("keyword")
	var isPublic *bool
	if publicStr := c.Query("is_public"); publicStr != "" {
		public := publicStr == "true"
		isPublic = &public
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	userID, _ := c.Get("user_id")
	replies, total, err := h.automationService.GetQuickReplies(c.Request.Context(), category, keyword, isPublic, userID.(uint), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取快速回复列表失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取快速回复列表成功",
		"data": gin.H{
			"replies":     replies,
			"total":       total,
			"page":        page,
			"page_size":   pageSize,
			"total_pages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// UseQuickReply 使用快速回复
// @Summary 使用快速回复
// @Description 使用快速回复（增加使用计数）
// @Tags 快速回复
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param id path int true "快速回复ID"
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 404 {object} map[string]interface{} "快速回复不存在"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/quick-replies/{id}/use [post]
func (h *AutomationHandler) UseQuickReply(c *gin.Context) {
	replyID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的回复ID",
		})
		return
	}

	err = h.automationService.UseQuickReply(c.Request.Context(), uint(replyID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "使用快速回复失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "使用快速回复成功",
	})
}

// BatchOperations 批量操作相关接口

// BatchUpdateTickets 批量更新工单
// @Summary 批量更新工单
// @Description 批量更新多个工单的状态、优先级等
// @Tags 批量操作
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param request body map[string]interface{} true "批量更新请求"
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/batch/update [post]
func (h *AutomationHandler) BatchUpdateTickets(c *gin.Context) {
	var req struct {
		TicketIDs []uint                 `json:"ticket_ids" binding:"required"`
		Updates   map[string]interface{} `json:"updates" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   err.Error(),
		})
		return
	}

	err := h.automationService.BatchUpdateTickets(c.Request.Context(), req.TicketIDs, req.Updates)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "批量更新工单失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "批量更新工单成功",
	})
}

// BatchAssignTickets 批量分配工单
// @Summary 批量分配工单
// @Description 批量分配工单给指定用户
// @Tags 批量操作
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param request body map[string]interface{} true "批量分配请求"
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/admin/automation/batch/assign [post]
func (h *AutomationHandler) BatchAssignTickets(c *gin.Context) {
	var req struct {
		TicketIDs []uint `json:"ticket_ids" binding:"required"`
		UserID    uint   `json:"user_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   err.Error(),
		})
		return
	}

	err := h.automationService.BatchAssignTickets(c.Request.Context(), req.TicketIDs, req.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "批量分配工单失败",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "批量分配工单成功",
	})
}
