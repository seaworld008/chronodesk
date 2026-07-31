package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

// AutomationHandler 自动化处理器
type AutomationHandler struct {
	automationService *services.AutomationService
	schedulerService  *services.SchedulerService
}

type automationRuleLogSummary struct {
	ID             uint      `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	RuleType       string    `json:"rule_type"`
	TriggerEvent   string    `json:"trigger_event"`
	Priority       int       `json:"priority"`
	IsActive       bool      `json:"is_active"`
	SuccessCount   int64     `json:"success_count"`
	FailureCount   int64     `json:"failure_count"`
	ExecutionCount int64     `json:"execution_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type automationTicketLogSummary struct {
	ID           uint                `json:"id"`
	TicketNumber string              `json:"ticket_number"`
	Title        string              `json:"title"`
	Status       models.TicketStatus `json:"status"`
}

type automationLogResponse struct {
	ID            uint                        `json:"id"`
	CreatedAt     time.Time                   `json:"created_at"`
	RuleID        uint                        `json:"rule_id"`
	Rule          *automationRuleLogSummary   `json:"rule,omitempty"`
	TicketID      uint                        `json:"ticket_id"`
	Ticket        *automationTicketLogSummary `json:"ticket,omitempty"`
	TriggerEvent  string                      `json:"trigger_event"`
	ExecutedAt    time.Time                   `json:"executed_at"`
	Success       bool                        `json:"success"`
	ErrorMessage  string                      `json:"error_message,omitempty"`
	ExecutionTime int64                       `json:"execution_time"`
}

func automationLogResponses(
	logs []*models.AutomationLog,
) []automationLogResponse {
	result := make([]automationLogResponse, 0, len(logs))
	for _, log := range logs {
		if log == nil {
			continue
		}
		item := automationLogResponse{
			ID:            log.ID,
			CreatedAt:     log.CreatedAt,
			RuleID:        log.RuleID,
			TicketID:      log.TicketID,
			TriggerEvent:  log.TriggerEvent,
			ExecutedAt:    log.ExecutedAt,
			Success:       log.Success,
			ErrorMessage:  scrubAutomationDiagnostic(log.ErrorMessage),
			ExecutionTime: log.ExecutionTime,
		}
		if log.Rule != nil {
			item.Rule = &automationRuleLogSummary{
				ID:             log.Rule.ID,
				Name:           log.Rule.Name,
				Description:    log.Rule.Description,
				RuleType:       log.Rule.RuleType,
				TriggerEvent:   log.Rule.TriggerEvent,
				Priority:       log.Rule.Priority,
				IsActive:       log.Rule.IsActive,
				SuccessCount:   log.Rule.SuccessCount,
				FailureCount:   log.Rule.FailureCount,
				ExecutionCount: log.Rule.ExecutionCount,
				CreatedAt:      log.Rule.CreatedAt,
				UpdatedAt:      log.Rule.UpdatedAt,
			}
		}
		if log.Ticket != nil {
			item.Ticket = &automationTicketLogSummary{
				ID:           log.Ticket.ID,
				TicketNumber: log.Ticket.TicketNumber,
				Title:        log.Ticket.Title,
				Status:       log.Ticket.Status,
			}
		}
		result = append(result, item)
	}
	return result
}

type automationRuleListResponse struct {
	ID              uint       `json:"id"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	RuleType        string     `json:"rule_type"`
	IsActive        bool       `json:"is_active"`
	Priority        int        `json:"priority"`
	TriggerEvent    string     `json:"trigger_event"`
	ExecutionCount  int64      `json:"execution_count"`
	LastExecutedAt  *time.Time `json:"last_executed_at,omitempty"`
	SuccessCount    int64      `json:"success_count"`
	FailureCount    int64      `json:"failure_count"`
	AverageExecTime int64      `json:"average_exec_time"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func automationRuleListResponses(
	rules []*models.AutomationRule,
) []automationRuleListResponse {
	result := make([]automationRuleListResponse, 0, len(rules))
	for _, rule := range rules {
		if rule == nil {
			continue
		}
		result = append(result, automationRuleListResponse{
			ID:              rule.ID,
			Name:            rule.Name,
			Description:     rule.Description,
			RuleType:        rule.RuleType,
			IsActive:        rule.IsActive,
			Priority:        rule.Priority,
			TriggerEvent:    rule.TriggerEvent,
			ExecutionCount:  rule.ExecutionCount,
			LastExecutedAt:  rule.LastExecutedAt,
			SuccessCount:    rule.SuccessCount,
			FailureCount:    rule.FailureCount,
			AverageExecTime: rule.AverageExecTime,
			CreatedAt:       rule.CreatedAt,
			UpdatedAt:       rule.UpdatedAt,
		})
	}
	return result
}

func scrubAutomationDiagnostic(value string) string {
	value = services.ScrubOutboxFailureText(value)
	runes := []rune(value)
	if len(runes) > 500 {
		return string(runes[:500])
	}
	return value
}

// NewAutomationHandler creates the HTTP adapter over the application-owned
// automation service. The adapter never constructs a second service graph.
func NewAutomationHandler(
	automationService *services.AutomationService,
	schedulerService *services.SchedulerService,
) (*AutomationHandler, error) {
	if automationService == nil {
		return nil, errors.New("automation service is required")
	}
	if schedulerService == nil {
		return nil, errors.New("scheduler service is required")
	}
	return &AutomationHandler{
		automationService: automationService,
		schedulerService:  schedulerService,
	}, nil
}

func (h *AutomationHandler) ConfigureListCursor(root []byte) error {
	if h == nil || h.automationService == nil {
		return services.ErrAutomationListCursorKey
	}
	return h.automationService.ConfigureListCursor(root)
}

// RegisterProjectRoutes mounts automation administration below the trusted
// ProjectScopeMiddleware boundary. There is deliberately no global alias.
func (h *AutomationHandler) RegisterProjectRoutes(projectGroup *gin.RouterGroup) {
	automation := projectGroup.Group("/admin/automation")
	automation.Use(h.requireProjectManager)

	rules := automation.Group("/rules")
	rules.POST("", h.CreateRule)
	rules.GET("", h.GetRules)
	rules.GET("/:id", h.GetRule)
	rules.PUT("/:id", h.UpdateRule)
	rules.DELETE("/:id", h.DeleteRule)
	rules.GET("/:id/stats", h.GetRuleStats)

	automation.GET("/logs", h.GetExecutionLogs)

	sla := automation.Group("/sla")
	sla.POST("", h.CreateSLAConfig)
	sla.GET("", h.GetSLAConfigs)

	templates := automation.Group("/templates")
	templates.POST("", h.CreateTemplate)
	templates.GET("", h.GetTemplates)
	templates.GET("/:id", h.GetTemplate)

	quickReplies := automation.Group("/quick-replies")
	quickReplies.POST("", h.CreateQuickReply)
	quickReplies.GET("", h.GetQuickReplies)
	quickReplies.POST("/:id/use", h.UseQuickReply)
}

func (h *AutomationHandler) requireProjectManager(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "未解析可信项目范围",
			"error":   "project_scope_required",
		})
		return
	}
	operation, err := services.OperationContextFromContext(c.Request.Context())
	if err != nil ||
		operation.Scope != access.Scope ||
		operation.Source != services.SourceProtocolHumanREST ||
		operation.Actor != models.HumanActor(c.GetUint("user_id")) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "项目操作上下文无效",
			"error":   "invalid_project_context",
		})
		return
	}
	if access.Role != models.ProjectRoleAdmin &&
		access.Role != models.ProjectRoleManager {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "仅项目管理员或经理可管理自动化",
			"error":   "project_role_forbidden",
		})
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
// @Router /api/projects/{projectKey}/admin/automation/rules [post]
func (h *AutomationHandler) CreateRule(c *gin.Context) {
	var req models.AutomationRuleRequest
	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   "invalid_request",
		})
		return
	}

	userID, _ := c.Get("user_id")
	rule, err := h.automationService.CreateRule(c.Request.Context(), &req, userID.(uint))
	if err != nil {
		status := http.StatusInternalServerError
		message := "创建规则失败"
		code := "internal_error"
		if errors.Is(err, services.ErrInvalidAutomationTriggerType) {
			status = http.StatusBadRequest
			message = "触发事件类型无效"
			code = "invalid_trigger_type"
		} else {
			logHandlerFailure(c, "automation.create_rule", err)
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": message,
			"error":   code,
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
// @Param page_size query int false "页大小" default(25)
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/projects/{projectKey}/admin/automation/rules [get]
func (h *AutomationHandler) GetRules(c *gin.Context) {
	query, ok := requireAutomationRuleListQuery(c)
	if !ok {
		return
	}
	rules, total, err := h.automationService.GetRules(
		c.Request.Context(),
		query.ruleType,
		query.triggerEvent,
		query.isActive,
		query.search,
		query.page,
		query.pageSize,
	)
	if err != nil {
		status := http.StatusInternalServerError
		message := "获取规则列表失败"
		code := "internal_error"
		if errors.Is(err, services.ErrInvalidAutomationTriggerType) {
			status = http.StatusBadRequest
			message = "触发事件筛选值无效"
			code = "invalid_trigger_type"
		} else if errors.Is(err, services.ErrInvalidAutomationListQuery) {
			status = http.StatusBadRequest
			message = "列表查询参数无效"
			code = "invalid_request"
		} else {
			logHandlerFailure(c, "automation.list_rules", err)
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": message,
			"error":   code,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取规则列表成功",
		"data": gin.H{
			"items":       automationRuleListResponses(rules),
			"total":       total,
			"page":        query.page,
			"page_size":   query.pageSize,
			"total_pages": automationTotalPages(total, query.pageSize),
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
// @Router /api/projects/{projectKey}/admin/automation/rules/{id} [get]
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
		message := "获取规则详情失败"
		code := "internal_error"
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
			message = "规则不存在"
			code = "rule_not_found"
		} else {
			logHandlerFailure(c, "automation.get_rule", err)
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": message,
			"error":   code,
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
// @Router /api/projects/{projectKey}/admin/automation/rules/{id} [put]
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
	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   "invalid_request",
		})
		return
	}

	userID, _ := c.Get("user_id")
	err = h.automationService.UpdateRule(c.Request.Context(), uint(ruleID), &req, userID.(uint))
	if err != nil {
		status := http.StatusInternalServerError
		message := "更新规则失败"
		code := "internal_error"
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
			message = "规则不存在"
			code = "rule_not_found"
		} else if errors.Is(err, services.ErrInvalidAutomationTriggerType) {
			status = http.StatusBadRequest
			message = "触发事件类型无效"
			code = "invalid_trigger_type"
		} else {
			logHandlerFailure(c, "automation.update_rule", err)
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": message,
			"error":   code,
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
// @Router /api/projects/{projectKey}/admin/automation/rules/{id} [delete]
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
		message := "删除规则失败"
		code := "internal_error"
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
			message = "规则不存在"
			code = "rule_not_found"
		} else {
			logHandlerFailure(c, "automation.delete_rule", err)
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": message,
			"error":   code,
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
// @Router /api/projects/{projectKey}/admin/automation/rules/{id}/stats [get]
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
		message := "获取规则统计失败"
		code := "internal_error"
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
			message = "规则不存在"
			code = "rule_not_found"
		} else {
			logHandlerFailure(c, "automation.get_rule_stats", err)
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": message,
			"error":   code,
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
// @Description 使用不透明游标获取自动化规则执行日志
// @Tags 自动化
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param rule_id query int false "规则ID"
// @Param ticket_id query int false "工单ID"
// @Param success query boolean false "是否成功"
// @Param cursor query string false "不透明续页游标"
// @Param limit query int false "页大小" default(25)
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/projects/{projectKey}/admin/automation/logs [get]
func (h *AutomationHandler) GetExecutionLogs(c *gin.Context) {
	query, ok := requireAutomationExecutionLogQuery(c)
	if !ok {
		return
	}
	page, err := h.automationService.ListExecutionLogs(
		c.Request.Context(),
		query,
	)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrInvalidAutomationListQuery),
			errors.Is(err, services.ErrInvalidAutomationListCursor):
			writeInvalidAutomationListQuery(c)
		case errors.Is(err, services.ErrAutomationListCursorKey):
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"message": "自动化执行日志暂不可用",
				"error":   "service_unavailable",
			})
		default:
			logHandlerFailure(c, "automation.list_execution_logs", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "获取执行日志失败",
				"error":   "internal_error",
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取执行日志成功",
		"data": gin.H{
			"items":       automationLogResponses(page.Items),
			"next_cursor": page.NextCursor,
			"has_more":    page.HasMore,
		},
	})
}

type automationRuleListQuery struct {
	ruleType     string
	triggerEvent string
	search       string
	isActive     *bool
	page         int
	pageSize     int
}

func requireAutomationRuleListQuery(
	c *gin.Context,
) (automationRuleListQuery, bool) {
	values, ok := strictAutomationQueryValues(c, map[string]struct{}{
		"rule_type":     {},
		"trigger_event": {},
		"is_active":     {},
		"search":        {},
		"page":          {},
		"page_size":     {},
		"sort":          {},
	})
	if !ok {
		return automationRuleListQuery{}, false
	}
	query := automationRuleListQuery{
		page:     1,
		pageSize: services.DefaultAutomationListSize,
	}
	if raw, exists := values["page"]; exists {
		value, valid := strictAutomationPositiveInt(raw, int(^uint(0)>>1))
		if !valid {
			writeInvalidAutomationListQuery(c)
			return automationRuleListQuery{}, false
		}
		query.page = value
	}
	if raw, exists := values["page_size"]; exists {
		value, valid := strictAutomationPositiveInt(
			raw,
			services.MaxAutomationListSize,
		)
		if !valid {
			writeInvalidAutomationListQuery(c)
			return automationRuleListQuery{}, false
		}
		query.pageSize = value
	}
	if raw, exists := values["sort"]; exists &&
		raw != `["priority","ASC"]` {
		writeInvalidAutomationListQuery(c)
		return automationRuleListQuery{}, false
	}
	if raw, exists := values["rule_type"]; exists {
		if !validAutomationRuleTypeFilter(raw) {
			writeInvalidAutomationListQuery(c)
			return automationRuleListQuery{}, false
		}
		query.ruleType = raw
	}
	if raw, exists := values["trigger_event"]; exists {
		if len([]rune(raw)) > 128 {
			writeInvalidAutomationListQuery(c)
			return automationRuleListQuery{}, false
		}
		query.triggerEvent = raw
	}
	if raw, exists := values["search"]; exists {
		if len([]rune(raw)) > 200 {
			writeInvalidAutomationListQuery(c)
			return automationRuleListQuery{}, false
		}
		query.search = raw
	}
	if raw, exists := values["is_active"]; exists {
		value, valid := strictAutomationBool(raw)
		if !valid {
			writeInvalidAutomationListQuery(c)
			return automationRuleListQuery{}, false
		}
		query.isActive = &value
	}
	return query, true
}

func requireAutomationExecutionLogQuery(
	c *gin.Context,
) (services.AutomationExecutionLogQuery, bool) {
	values, ok := strictAutomationQueryValues(c, map[string]struct{}{
		"cursor":    {},
		"limit":     {},
		"rule_id":   {},
		"ticket_id": {},
		"success":   {},
	})
	if !ok {
		return services.AutomationExecutionLogQuery{}, false
	}
	query := services.AutomationExecutionLogQuery{
		Cursor: values["cursor"],
		Limit:  services.DefaultAutomationListSize,
	}
	if raw, exists := values["limit"]; exists {
		value, valid := strictAutomationPositiveInt(
			raw,
			services.MaxAutomationListSize,
		)
		if !valid {
			writeInvalidAutomationListQuery(c)
			return services.AutomationExecutionLogQuery{}, false
		}
		query.Limit = value
	}
	if raw, exists := values["rule_id"]; exists {
		value, valid := strictAutomationPositiveUint(raw)
		if !valid {
			writeInvalidAutomationListQuery(c)
			return services.AutomationExecutionLogQuery{}, false
		}
		query.RuleID = &value
	}
	if raw, exists := values["ticket_id"]; exists {
		value, valid := strictAutomationPositiveUint(raw)
		if !valid {
			writeInvalidAutomationListQuery(c)
			return services.AutomationExecutionLogQuery{}, false
		}
		query.TicketID = &value
	}
	if raw, exists := values["success"]; exists {
		value, valid := strictAutomationBool(raw)
		if !valid {
			writeInvalidAutomationListQuery(c)
			return services.AutomationExecutionLogQuery{}, false
		}
		query.Success = &value
	}
	return query, true
}

func strictAutomationQueryValues(
	c *gin.Context,
	allowed map[string]struct{},
) (map[string]string, bool) {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return nil, false
	}
	parsed, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		writeInvalidAutomationListQuery(c)
		return nil, false
	}
	result := make(map[string]string, len(parsed))
	for key, values := range parsed {
		if _, exists := allowed[key]; !exists || len(values) != 1 {
			writeInvalidAutomationListQuery(c)
			return nil, false
		}
		value := values[0]
		if value == "" || strings.TrimSpace(value) != value {
			writeInvalidAutomationListQuery(c)
			return nil, false
		}
		result[key] = value
	}
	return result, true
}

func strictAutomationPositiveInt(raw string, maximum int) (int, bool) {
	if raw == "" || maximum < 1 {
		return 0, false
	}
	for _, character := range raw {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseUint(raw, 10, 31)
	if err != nil || value == 0 || value > uint64(maximum) {
		return 0, false
	}
	return int(value), true
}

func strictAutomationPositiveUint(raw string) (uint, bool) {
	if raw == "" {
		return 0, false
	}
	for _, character := range raw {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || value == 0 {
		return 0, false
	}
	return uint(value), true
}

func strictAutomationBool(raw string) (bool, bool) {
	switch raw {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func validAutomationRuleTypeFilter(raw string) bool {
	if len(raw) == 0 || len(raw) > 50 ||
		raw[0] < 'a' || raw[0] > 'z' {
		return false
	}
	for _, character := range raw[1:] {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '_' {
			return false
		}
	}
	return true
}

func automationTotalPages(total int64, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 0
	}
	return int((total + int64(pageSize) - 1) / int64(pageSize))
}

func writeInvalidAutomationListQuery(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{
		"success": false,
		"message": "列表查询参数无效",
		"error":   "invalid_request",
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
// @Router /api/projects/{projectKey}/admin/automation/sla [post]
func (h *AutomationHandler) CreateSLAConfig(c *gin.Context) {
	var req models.SLAConfigRequest
	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   "invalid_request",
		})
		return
	}

	config, err := h.automationService.CreateSLAConfig(c.Request.Context(), &req)
	if err != nil {
		if errors.Is(err, services.ErrInvalidWorkingHours) {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "工作时间配置无效",
				"error":   "invalid_working_hours",
			})
			return
		}
		logHandlerFailure(c, "automation.create_sla_config", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "创建SLA配置失败",
			"error":   "internal_error",
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
// @Router /api/projects/{projectKey}/admin/automation/sla [get]
func (h *AutomationHandler) GetSLAConfigs(c *gin.Context) {
	var isActive *bool
	if activeStr := c.Query("is_active"); activeStr != "" {
		active := activeStr == "true"
		isActive = &active
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	page, pageSize = normalizePagination(page, pageSize, 100)

	configs, total, err := h.automationService.GetSLAConfigs(c.Request.Context(), isActive, page, pageSize)
	if err != nil {
		logHandlerFailure(c, "automation.list_sla_configs", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取SLA配置列表失败",
			"error":   "internal_error",
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
// @Router /api/projects/{projectKey}/admin/automation/templates [post]
func (h *AutomationHandler) CreateTemplate(c *gin.Context) {
	var req models.TicketTemplateRequest
	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   "invalid_request",
		})
		return
	}

	userID, _ := c.Get("user_id")
	template, err := h.automationService.CreateTemplate(c.Request.Context(), &req, userID.(uint))
	if err != nil {
		logHandlerFailure(c, "automation.create_template", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "创建模板失败",
			"error":   "internal_error",
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
// @Router /api/projects/{projectKey}/admin/automation/templates [get]
func (h *AutomationHandler) GetTemplates(c *gin.Context) {
	category := c.Query("category")
	var isActive *bool
	if activeStr := c.Query("is_active"); activeStr != "" {
		active := activeStr == "true"
		isActive = &active
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	page, pageSize = normalizePagination(page, pageSize, 100)

	templates, total, err := h.automationService.GetTemplates(c.Request.Context(), category, isActive, page, pageSize)
	if err != nil {
		logHandlerFailure(c, "automation.list_templates", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取模板列表失败",
			"error":   "internal_error",
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
// @Router /api/projects/{projectKey}/admin/automation/templates/{id} [get]
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
		message := "获取模板详情失败"
		code := "internal_error"
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
			message = "模板不存在"
			code = "template_not_found"
		} else {
			logHandlerFailure(c, "automation.get_template", err)
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": message,
			"error":   code,
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
// @Router /api/projects/{projectKey}/admin/automation/quick-replies [post]
func (h *AutomationHandler) CreateQuickReply(c *gin.Context) {
	var req models.QuickReplyRequest
	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   "invalid_request",
		})
		return
	}

	userID, _ := c.Get("user_id")
	reply, err := h.automationService.CreateQuickReply(c.Request.Context(), &req, userID.(uint))
	if err != nil {
		logHandlerFailure(c, "automation.create_quick_reply", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "创建快速回复失败",
			"error":   "internal_error",
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
// @Router /api/projects/{projectKey}/admin/automation/quick-replies [get]
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
	page, pageSize = normalizePagination(page, pageSize, 100)

	userID, _ := c.Get("user_id")
	replies, total, err := h.automationService.GetQuickReplies(c.Request.Context(), category, keyword, isPublic, userID.(uint), page, pageSize)
	if err != nil {
		logHandlerFailure(c, "automation.list_quick_replies", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取快速回复列表失败",
			"error":   "internal_error",
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
// @Router /api/projects/{projectKey}/admin/automation/quick-replies/{id}/use [post]
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
		logHandlerFailure(c, "automation.use_quick_reply", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "使用快速回复失败",
			"error":   "internal_error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "使用快速回复成功",
	})
}
