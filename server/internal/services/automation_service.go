package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gongdan-system/internal/models"
	"gorm.io/gorm"
)

// AutomationService 自动化服务
type AutomationService struct {
	db *gorm.DB
}

// NewAutomationService 创建自动化服务实例
func NewAutomationService(db *gorm.DB) *AutomationService {
	return &AutomationService{db: db}
}

// AutomationRuleService 自动化规则相关方法

// CreateRule 创建自动化规则
func (s *AutomationService) CreateRule(ctx context.Context, req *models.AutomationRuleRequest, userID uint) (*models.AutomationRule, error) {
	rule := &models.AutomationRule{
		Name:         req.Name,
		Description:  req.Description,
		RuleType:     req.RuleType,
		IsActive:     true,
		Priority:     1,
		TriggerEvent: req.TriggerEvent,
		CreatedBy:    userID,
	}

	if req.IsActive != nil {
		rule.IsActive = *req.IsActive
	}
	if req.Priority != nil {
		rule.Priority = *req.Priority
	}

	// 设置条件和动作
	if err := rule.SetConditions(req.Conditions); err != nil {
		return nil, fmt.Errorf("invalid conditions: %w", err)
	}
	if err := rule.SetActions(req.Actions); err != nil {
		return nil, fmt.Errorf("invalid actions: %w", err)
	}

	if err := s.db.WithContext(ctx).Create(rule).Error; err != nil {
		return nil, fmt.Errorf("failed to create rule: %w", err)
	}

	return rule, nil
}

// GetRules 获取自动化规则列表
func (s *AutomationService) GetRules(ctx context.Context, ruleType string, triggerEvent string, isActive *bool, search string, page, pageSize int) ([]*models.AutomationRule, int64, error) {
	query := s.db.WithContext(ctx).Model(&models.AutomationRule{}).Preload("CreatedUser").Preload("UpdatedUser")

	if ruleType != "" {
		query = query.Where("rule_type = ?", ruleType)
	}
	if triggerEvent != "" {
		query = query.Where("trigger_event = ?", triggerEvent)
	}
	if isActive != nil {
		query = query.Where("is_active = ?", *isActive)
	}
	if search = strings.TrimSpace(search); search != "" {
		like := fmt.Sprintf("%%%s%%", strings.ToLower(search))
		query = query.Where("lower(name) LIKE ? OR lower(description) LIKE ?", like, like)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count rules: %w", err)
	}

	var rules []*models.AutomationRule
	offset := (page - 1) * pageSize
	if err := query.Order("priority ASC, created_at DESC").Offset(offset).Limit(pageSize).Find(&rules).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get rules: %w", err)
	}

	return rules, total, nil
}

// GetRuleByID 根据ID获取规则
func (s *AutomationService) GetRuleByID(ctx context.Context, ruleID uint) (*models.AutomationRule, error) {
	var rule models.AutomationRule
	if err := s.db.WithContext(ctx).Preload("CreatedUser").Preload("UpdatedUser").First(&rule, ruleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("rule not found")
		}
		return nil, fmt.Errorf("failed to get rule: %w", err)
	}
	return &rule, nil
}

// UpdateRule 更新规则
func (s *AutomationService) UpdateRule(ctx context.Context, ruleID uint, req *models.AutomationRuleRequest, userID uint) error {
	rule, err := s.GetRuleByID(ctx, ruleID)
	if err != nil {
		return err
	}

	updates := map[string]interface{}{
		"name":          req.Name,
		"description":   req.Description,
		"rule_type":     req.RuleType,
		"trigger_event": req.TriggerEvent,
		"updated_by":    userID,
	}

	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}
	if req.Priority != nil {
		updates["priority"] = *req.Priority
	}

	// 更新条件和动作
	if err := rule.SetConditions(req.Conditions); err != nil {
		return fmt.Errorf("invalid conditions: %w", err)
	}
	if err := rule.SetActions(req.Actions); err != nil {
		return fmt.Errorf("invalid actions: %w", err)
	}

	updates["conditions"] = rule.Conditions
	updates["actions"] = rule.Actions

	return s.db.WithContext(ctx).Model(rule).Updates(updates).Error
}

// DeleteRule 删除规则
func (s *AutomationService) DeleteRule(ctx context.Context, ruleID uint) error {
	result := s.db.WithContext(ctx).Delete(&models.AutomationRule{}, ruleID)
	if result.Error != nil {
		return fmt.Errorf("failed to delete rule: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("rule not found")
	}
	return nil
}

// ExecuteRules 执行自动化规则
func (s *AutomationService) ExecuteRules(ctx context.Context, triggerEvent string, ticket *models.Ticket) error {
	// 获取激活的规则
	var rules []models.AutomationRule
	if err := s.db.WithContext(ctx).Where("is_active = ? AND trigger_event = ?", true, triggerEvent).
		Order("priority ASC").Find(&rules).Error; err != nil {
		return fmt.Errorf("failed to get rules: %w", err)
	}

	for _, rule := range rules {
		if err := s.executeRule(ctx, &rule, ticket); err != nil {
			log.Printf("Failed to execute rule %d: %v", rule.ID, err)
			// 继续执行其他规则
		}
	}

	return nil
}

// executeRule 执行单个规则
func (s *AutomationService) executeRule(ctx context.Context, rule *models.AutomationRule, ticket *models.Ticket) error {
	startTime := time.Now()
	success := false
	var errorMsg string

	defer func() {
		// 更新规则执行统计
		execTime := time.Since(startTime)
		rule.UpdateExecutionStats(success, execTime)
		s.db.WithContext(ctx).Model(rule).Updates(map[string]interface{}{
			"execution_count":   rule.ExecutionCount,
			"last_executed_at":  rule.LastExecutedAt,
			"success_count":     rule.SuccessCount,
			"failure_count":     rule.FailureCount,
			"average_exec_time": rule.AverageExecTime,
		})

		// 记录执行日志
		s.logExecution(ctx, rule, ticket, success, errorMsg, execTime)
	}()

	// 检查条件
	conditions, err := rule.GetConditions()
	if err != nil {
		errorMsg = fmt.Sprintf("Failed to parse conditions: %v", err)
		return errors.New(errorMsg)
	}

	if !s.evaluateConditions(conditions, ticket) {
		return nil // 条件不匹配，跳过执行
	}

	// 执行动作
	actions, err := rule.GetActions()
	if err != nil {
		errorMsg = fmt.Sprintf("Failed to parse actions: %v", err)
		return errors.New(errorMsg)
	}

	for _, action := range actions {
		if err := s.executeAction(ctx, &action, ticket); err != nil {
			errorMsg = fmt.Sprintf("Failed to execute action %s: %v", action.Type, err)
			return errors.New(errorMsg)
		}
	}

	success = true
	return nil
}

// evaluateConditions 评估条件
func (s *AutomationService) evaluateConditions(conditions []models.RuleCondition, ticket *models.Ticket) bool {
	if len(conditions) == 0 {
		return true // 无条件则总是匹配
	}

	result := true
	for i, condition := range conditions {
		conditionResult := s.evaluateCondition(&condition, ticket)

		if i == 0 {
			result = conditionResult
		} else {
			// 应用逻辑操作符
			prevCondition := conditions[i-1]
			if prevCondition.LogicOp == "or" {
				result = result || conditionResult
			} else { // 默认是and
				result = result && conditionResult
			}
		}
	}

	return result
}

// evaluateCondition 评估单个条件
func (s *AutomationService) evaluateCondition(condition *models.RuleCondition, ticket *models.Ticket) bool {
	fieldValue := s.getTicketFieldValue(condition.Field, ticket)
	conditionValue := condition.Value

	switch condition.Operator {
	case "eq":
		return s.compareValues(fieldValue, conditionValue) == 0
	case "ne":
		return s.compareValues(fieldValue, conditionValue) != 0
	case "contains":
		return strings.Contains(strings.ToLower(fmt.Sprintf("%v", fieldValue)),
			strings.ToLower(fmt.Sprintf("%v", conditionValue)))
	case "starts_with":
		return strings.HasPrefix(strings.ToLower(fmt.Sprintf("%v", fieldValue)),
			strings.ToLower(fmt.Sprintf("%v", conditionValue)))
	case "ends_with":
		return strings.HasSuffix(strings.ToLower(fmt.Sprintf("%v", fieldValue)),
			strings.ToLower(fmt.Sprintf("%v", conditionValue)))
	case "regex":
		regex, err := regexp.Compile(fmt.Sprintf("%v", conditionValue))
		if err != nil {
			return false
		}
		return regex.MatchString(fmt.Sprintf("%v", fieldValue))
	case "in":
		if values, ok := conditionValue.([]interface{}); ok {
			fieldStr := fmt.Sprintf("%v", fieldValue)
			for _, v := range values {
				if fmt.Sprintf("%v", v) == fieldStr {
					return true
				}
			}
		}
		return false
	case "not_in":
		if values, ok := conditionValue.([]interface{}); ok {
			fieldStr := fmt.Sprintf("%v", fieldValue)
			for _, v := range values {
				if fmt.Sprintf("%v", v) == fieldStr {
					return false
				}
			}
			return true
		}
		return false
	case "gt", "gte", "lt", "lte":
		return s.compareNumeric(fieldValue, conditionValue, condition.Operator)
	default:
		return false
	}
}

// getTicketFieldValue 获取工单字段值
func (s *AutomationService) getTicketFieldValue(field string, ticket *models.Ticket) interface{} {
	switch field {
	case "title":
		return ticket.Title
	case "content":
		return ticket.Description
	case "type":
		return ticket.Type
	case "priority":
		return ticket.Priority
	case "status":
		return ticket.Status
	case "assigned_user_id":
		if ticket.AssignedToID != nil {
			return *ticket.AssignedToID
		}
		return nil
	case "creator_id":
		return ticket.CreatedByID
	case "created_at":
		return ticket.CreatedAt.Format(time.RFC3339)
	case "updated_at":
		return ticket.UpdatedAt.Format(time.RFC3339)
	default:
		return nil
	}
}

// compareValues 比较值
func (s *AutomationService) compareValues(a, b interface{}) int {
	aStr := fmt.Sprintf("%v", a)
	bStr := fmt.Sprintf("%v", b)

	if aStr == bStr {
		return 0
	} else if aStr < bStr {
		return -1
	}
	return 1
}

// compareNumeric 数值比较
func (s *AutomationService) compareNumeric(fieldValue, conditionValue interface{}, operator string) bool {
	fVal, fErr := s.toFloat64(fieldValue)
	cVal, cErr := s.toFloat64(conditionValue)

	if fErr != nil || cErr != nil {
		return false
	}

	switch operator {
	case "gt":
		return fVal > cVal
	case "gte":
		return fVal >= cVal
	case "lt":
		return fVal < cVal
	case "lte":
		return fVal <= cVal
	default:
		return false
	}
}

// toFloat64 转换为浮点数
func (s *AutomationService) toFloat64(value interface{}) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	default:
		return 0, fmt.Errorf("cannot convert to float64")
	}
}

// executeAction 执行动作
func (s *AutomationService) executeAction(ctx context.Context, action *models.RuleAction, ticket *models.Ticket) error {
	switch action.Type {
	case "assign":
		return s.executeAssignAction(ctx, action, ticket)
	case "set_priority":
		return s.executeSetPriorityAction(ctx, action, ticket)
	case "set_status":
		return s.executeSetStatusAction(ctx, action, ticket)
	case "add_comment":
		return s.executeAddCommentAction(ctx, action, ticket)
	case "notify":
		return s.executeNotifyAction(ctx, action, ticket)
	case "escalate":
		return s.executeEscalateAction(ctx, action, ticket)
	default:
		return fmt.Errorf("unknown action type: %s", action.Type)
	}
}

// executeAssignAction 执行分配动作
func (s *AutomationService) executeAssignAction(ctx context.Context, action *models.RuleAction, ticket *models.Ticket) error {
	userIDParam, ok := action.Params["user_id"]
	if !ok {
		return fmt.Errorf("user_id parameter required for assign action")
	}

	userID, err := s.toUint(userIDParam)
	if err != nil {
		return fmt.Errorf("invalid user_id: %w", err)
	}

	// 验证用户存在
	var user models.User
	if err := s.db.WithContext(ctx).First(&user, userID).Error; err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	// 更新工单分配
	updates := map[string]interface{}{
		"assigned_user_id": userID,
		"updated_at":       time.Now(),
	}

	return s.db.WithContext(ctx).Model(ticket).Updates(updates).Error
}

// executeSetPriorityAction 执行设置优先级动作
func (s *AutomationService) executeSetPriorityAction(ctx context.Context, action *models.RuleAction, ticket *models.Ticket) error {
	priorityParam, ok := action.Params["priority"]
	if !ok {
		return fmt.Errorf("priority parameter required")
	}

	priority := fmt.Sprintf("%v", priorityParam)
	validPriorities := []string{"low", "normal", "high", "critical"}

	found := false
	for _, p := range validPriorities {
		if p == priority {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("invalid priority: %s", priority)
	}

	updates := map[string]interface{}{
		"priority":   priority,
		"updated_at": time.Now(),
	}

	return s.db.WithContext(ctx).Model(ticket).Updates(updates).Error
}

// executeSetStatusAction 执行设置状态动作
func (s *AutomationService) executeSetStatusAction(ctx context.Context, action *models.RuleAction, ticket *models.Ticket) error {
	statusParam, ok := action.Params["status"]
	if !ok {
		return fmt.Errorf("status parameter required")
	}

	status := fmt.Sprintf("%v", statusParam)
	validStatuses := []string{"open", "in_progress", "resolved", "closed"}

	found := false
	for _, s := range validStatuses {
		if s == status {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("invalid status: %s", status)
	}

	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}

	return s.db.WithContext(ctx).Model(ticket).Updates(updates).Error
}

// executeAddCommentAction 执行添加评论动作
func (s *AutomationService) executeAddCommentAction(ctx context.Context, action *models.RuleAction, ticket *models.Ticket) error {
	contentParam, ok := action.Params["content"]
	if !ok {
		return fmt.Errorf("content parameter required")
	}

	content := fmt.Sprintf("%v", contentParam)

	// 系统用户ID，可以配置
	systemUserID := uint(1)

	comment := &models.TicketComment{
		TicketID: ticket.ID,
		UserID:   systemUserID,
		Content:  content,
		Type:     models.CommentTypeSystem,
	}

	return s.db.WithContext(ctx).Create(comment).Error
}

// executeNotifyAction 执行通知动作
func (s *AutomationService) executeNotifyAction(ctx context.Context, action *models.RuleAction, ticket *models.Ticket) error {
	// 这里可以集成通知服务
	// 暂时只记录日志
	log.Printf("Notification action executed for ticket %d", ticket.ID)
	return nil
}

// executeEscalateAction 执行升级动作
func (s *AutomationService) executeEscalateAction(ctx context.Context, action *models.RuleAction, ticket *models.Ticket) error {
	// 升级逻辑，比如分配给管理员
	managerIDParam, ok := action.Params["manager_id"]
	if !ok {
		return fmt.Errorf("manager_id parameter required for escalate action")
	}

	managerID, err := s.toUint(managerIDParam)
	if err != nil {
		return fmt.Errorf("invalid manager_id: %w", err)
	}

	updates := map[string]interface{}{
		"assigned_user_id": managerID,
		"priority":         "high", // 升级时提高优先级
		"updated_at":       time.Now(),
	}

	return s.db.WithContext(ctx).Model(ticket).Updates(updates).Error
}

// toUint 转换为uint
func (s *AutomationService) toUint(value interface{}) (uint, error) {
	switch v := value.(type) {
	case float64:
		return uint(v), nil
	case int:
		return uint(v), nil
	case int64:
		return uint(v), nil
	case uint:
		return v, nil
	case string:
		i, err := strconv.ParseUint(v, 10, 32)
		return uint(i), err
	default:
		return 0, fmt.Errorf("cannot convert to uint")
	}
}

// logExecution 记录执行日志
func (s *AutomationService) logExecution(ctx context.Context, rule *models.AutomationRule, ticket *models.Ticket, success bool, errorMsg string, execTime time.Duration) {
	logEntry := &models.AutomationLog{
		RuleID:        rule.ID,
		TicketID:      ticket.ID,
		TriggerEvent:  rule.TriggerEvent,
		ExecutedAt:    time.Now(),
		Success:       success,
		ErrorMessage:  errorMsg,
		ExecutionTime: execTime.Milliseconds(),
	}

	if err := s.db.WithContext(ctx).Create(logEntry).Error; err != nil {
		log.Printf("Failed to create automation log: %v", err)
	}
}

// GetExecutionLogs 获取执行日志
func (s *AutomationService) GetExecutionLogs(ctx context.Context, ruleID, ticketID *uint, success *bool, page, pageSize int) ([]*models.AutomationLog, int64, error) {
	query := s.db.WithContext(ctx).Model(&models.AutomationLog{}).
		Preload("Rule").Preload("Ticket")

	if ruleID != nil {
		query = query.Where("rule_id = ?", *ruleID)
	}
	if ticketID != nil {
		query = query.Where("ticket_id = ?", *ticketID)
	}
	if success != nil {
		query = query.Where("success = ?", *success)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count logs: %w", err)
	}

	var logs []*models.AutomationLog
	offset := (page - 1) * pageSize
	if err := query.Order("executed_at DESC").Offset(offset).Limit(pageSize).Find(&logs).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get logs: %w", err)
	}

	return logs, total, nil
}

// GetRuleStats 获取规则统计
func (s *AutomationService) GetRuleStats(ctx context.Context, ruleID uint) (map[string]interface{}, error) {
	rule, err := s.GetRuleByID(ctx, ruleID)
	if err != nil {
		return nil, err
	}

	stats := map[string]interface{}{
		"rule_id":           rule.ID,
		"execution_count":   rule.ExecutionCount,
		"success_count":     rule.SuccessCount,
		"failure_count":     rule.FailureCount,
		"success_rate":      0.0,
		"average_exec_time": rule.AverageExecTime,
		"last_executed_at":  rule.LastExecutedAt,
	}

	if rule.ExecutionCount > 0 {
		stats["success_rate"] = float64(rule.SuccessCount) / float64(rule.ExecutionCount) * 100
	}

	return stats, nil
}

// SLA相关方法

// CreateSLAConfig 创建SLA配置
func (s *AutomationService) CreateSLAConfig(ctx context.Context, req *models.SLAConfigRequest) (*models.SLAConfig, error) {
	config := &models.SLAConfig{
		Name:            req.Name,
		Description:     req.Description,
		IsActive:        true,
		IsDefault:       false,
		TicketType:      req.TicketType,
		Priority:        req.Priority,
		Category:        req.Category,
		AssignedUserID:  req.AssignedUserID,
		ResponseTime:    req.ResponseTime,
		ResolutionTime:  req.ResolutionTime,
		ExcludeWeekends: true,
		ExcludeHolidays: true,
	}

	if req.IsActive != nil {
		config.IsActive = *req.IsActive
	}
	if req.IsDefault != nil {
		config.IsDefault = *req.IsDefault
	}
	if req.ExcludeWeekends != nil {
		config.ExcludeWeekends = *req.ExcludeWeekends
	}
	if req.ExcludeHolidays != nil {
		config.ExcludeHolidays = *req.ExcludeHolidays
	}

	// 设置工作时间
	if req.WorkingHours != nil {
		workingHoursJSON, err := json.Marshal(req.WorkingHours)
		if err != nil {
			return nil, fmt.Errorf("invalid working hours: %w", err)
		}
		config.WorkingHours = string(workingHoursJSON)
	}

	// 设置升级规则
	if len(req.EscalationRules) > 0 {
		escalationJSON, err := json.Marshal(req.EscalationRules)
		if err != nil {
			return nil, fmt.Errorf("invalid escalation rules: %w", err)
		}
		config.EscalationRules = string(escalationJSON)
	}

	// 如果设置为默认配置，需要取消其他默认配置
	if config.IsDefault {
		if err := s.db.WithContext(ctx).Model(&models.SLAConfig{}).
			Where("is_default = ?", true).
			Update("is_default", false).Error; err != nil {
			return nil, fmt.Errorf("failed to update existing default config: %w", err)
		}
	}

	if err := s.db.WithContext(ctx).Create(config).Error; err != nil {
		return nil, fmt.Errorf("failed to create SLA config: %w", err)
	}

	return config, nil
}

// GetSLAConfigs 获取SLA配置列表
func (s *AutomationService) GetSLAConfigs(ctx context.Context, isActive *bool, page, pageSize int) ([]*models.SLAConfig, int64, error) {
	query := s.db.WithContext(ctx).Model(&models.SLAConfig{})

	if isActive != nil {
		query = query.Where("is_active = ?", *isActive)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count SLA configs: %w", err)
	}

	var configs []*models.SLAConfig
	offset := (page - 1) * pageSize
	if err := query.Order("is_default DESC, created_at DESC").Offset(offset).Limit(pageSize).Find(&configs).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get SLA configs: %w", err)
	}

	return configs, total, nil
}

// GetSLAConfigForTicket 为工单获取适用的SLA配置
func (s *AutomationService) GetSLAConfigForTicket(ctx context.Context, ticket *models.Ticket) (*models.SLAConfig, error) {
	query := s.db.WithContext(ctx).Where("is_active = ?", true)

	// 按优先级查找最匹配的配置
	conditions := []string{}
	params := []interface{}{}

	if ticket.Type != "" {
		conditions = append(conditions, "ticket_type = ? OR ticket_type IS NULL")
		params = append(params, ticket.Type)
	} else {
		conditions = append(conditions, "ticket_type IS NULL")
	}

	if ticket.Priority != "" {
		conditions = append(conditions, "priority = ? OR priority IS NULL")
		params = append(params, ticket.Priority)
	} else {
		conditions = append(conditions, "priority IS NULL")
	}

	if ticket.AssignedToID != nil {
		conditions = append(conditions, "assigned_user_id = ? OR assigned_user_id IS NULL")
		params = append(params, *ticket.AssignedToID)
	} else {
		conditions = append(conditions, "assigned_user_id IS NULL")
	}

	whereClause := "(" + strings.Join(conditions, ") AND (") + ")"
	query = query.Where(whereClause, params...)

	var config models.SLAConfig
	// 首先尝试找到最匹配的配置
	if err := query.Order("(ticket_type IS NOT NULL) DESC, (priority IS NOT NULL) DESC, (assigned_user_id IS NOT NULL) DESC").
		First(&config).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 如果没有匹配的，使用默认配置
			if err := s.db.WithContext(ctx).Where("is_default = ? AND is_active = ?", true, true).
				First(&config).Error; err != nil {
				return nil, fmt.Errorf("no suitable SLA config found")
			}
		} else {
			return nil, fmt.Errorf("failed to get SLA config: %w", err)
		}
	}

	return &config, nil
}

// CalculateSLADeadlines 计算SLA截止时间
func (s *AutomationService) CalculateSLADeadlines(ctx context.Context, ticket *models.Ticket, config *models.SLAConfig) (responseDeadline, resolutionDeadline time.Time, err error) {
	startTime := ticket.CreatedAt

	workingHours, err := config.GetWorkingHours()
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to get working hours: %w", err)
	}

	// 计算响应截止时间
	responseDeadline = s.addWorkingTime(startTime, time.Duration(config.ResponseTime)*time.Minute, workingHours, config.ExcludeWeekends, config.ExcludeHolidays)

	// 计算解决截止时间
	resolutionDeadline = s.addWorkingTime(startTime, time.Duration(config.ResolutionTime)*time.Minute, workingHours, config.ExcludeWeekends, config.ExcludeHolidays)

	return responseDeadline, resolutionDeadline, nil
}

// addWorkingTime 添加工作时间（考虑工作时间、周末、节假日）
func (s *AutomationService) addWorkingTime(startTime time.Time, duration time.Duration, workingHours *models.WorkingHours, excludeWeekends, excludeHolidays bool) time.Time {
	// 简化实现：如果不排除周末和节假日，直接添加时间
	if !excludeWeekends && !excludeHolidays {
		return startTime.Add(duration)
	}

	// TODO: 实现复杂的工作时间计算逻辑
	// 这里可以根据工作时间配置进行精确计算

	// 暂时简化处理：按工作日计算（周一到周五）
	current := startTime
	remaining := duration

	for remaining > 0 {
		// 检查是否是工作日
		if excludeWeekends && (current.Weekday() == time.Saturday || current.Weekday() == time.Sunday) {
			current = current.Add(24 * time.Hour)
			continue
		}

		// 假设每个工作日工作8小时
		dayDuration := 8 * time.Hour
		if remaining <= dayDuration {
			current = current.Add(remaining)
			remaining = 0
		} else {
			current = current.Add(dayDuration)
			remaining -= dayDuration
			// 跳到下一天
			current = time.Date(current.Year(), current.Month(), current.Day()+1, 9, 0, 0, 0, current.Location())
		}
	}

	return current
}

// Template相关方法

// CreateTemplate 创建工单模板
func (s *AutomationService) CreateTemplate(ctx context.Context, req *models.TicketTemplateRequest, userID uint) (*models.TicketTemplate, error) {
	template := &models.TicketTemplate{
		Name:            req.Name,
		Description:     req.Description,
		Category:        req.Category,
		IsActive:        true,
		TitleTemplate:   req.TitleTemplate,
		ContentTemplate: req.ContentTemplate,
		DefaultType:     req.DefaultType,
		DefaultPriority: req.DefaultPriority,
		DefaultStatus:   req.DefaultStatus,
		AssignToUserID:  req.AssignToUserID,
		CreatedBy:       userID,
	}

	if req.IsActive != nil {
		template.IsActive = *req.IsActive
	}

	// 设置自定义字段
	if len(req.CustomFields) > 0 {
		customFieldsJSON, err := json.Marshal(req.CustomFields)
		if err != nil {
			return nil, fmt.Errorf("invalid custom fields: %w", err)
		}
		template.CustomFields = string(customFieldsJSON)
	}

	if err := s.db.WithContext(ctx).Create(template).Error; err != nil {
		return nil, fmt.Errorf("failed to create template: %w", err)
	}

	return template, nil
}

// GetTemplates 获取模板列表
func (s *AutomationService) GetTemplates(ctx context.Context, category string, isActive *bool, page, pageSize int) ([]*models.TicketTemplate, int64, error) {
	query := s.db.WithContext(ctx).Model(&models.TicketTemplate{}).Preload("CreatedUser").Preload("AssignToUser")

	if category != "" {
		query = query.Where("category = ?", category)
	}
	if isActive != nil {
		query = query.Where("is_active = ?", *isActive)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count templates: %w", err)
	}

	var templates []*models.TicketTemplate
	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&templates).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get templates: %w", err)
	}

	return templates, total, nil
}

// GetTemplateByID 根据ID获取模板
func (s *AutomationService) GetTemplateByID(ctx context.Context, templateID uint) (*models.TicketTemplate, error) {
	var template models.TicketTemplate
	if err := s.db.WithContext(ctx).Preload("CreatedUser").Preload("AssignToUser").First(&template, templateID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("template not found")
		}
		return nil, fmt.Errorf("failed to get template: %w", err)
	}
	return &template, nil
}

// QuickReply相关方法

// CreateQuickReply 创建快速回复
func (s *AutomationService) CreateQuickReply(ctx context.Context, req *models.QuickReplyRequest, userID uint) (*models.QuickReply, error) {
	reply := &models.QuickReply{
		Name:      req.Name,
		Category:  req.Category,
		Content:   req.Content,
		Tags:      req.Tags,
		IsPublic:  false,
		CreatedBy: userID,
	}

	if req.IsPublic != nil {
		reply.IsPublic = *req.IsPublic
	}

	if err := s.db.WithContext(ctx).Create(reply).Error; err != nil {
		return nil, fmt.Errorf("failed to create quick reply: %w", err)
	}

	return reply, nil
}

// GetQuickReplies 获取快速回复列表
func (s *AutomationService) GetQuickReplies(ctx context.Context, category, keyword string, isPublic *bool, userID uint, page, pageSize int) ([]*models.QuickReply, int64, error) {
	query := s.db.WithContext(ctx).Model(&models.QuickReply{}).Preload("CreatedUser")

	// 只能看到自己创建的或公开的
	if isPublic == nil || !*isPublic {
		query = query.Where("created_by = ? OR is_public = ?", userID, true)
	} else {
		query = query.Where("is_public = ?", true)
	}

	if category != "" {
		query = query.Where("category = ?", category)
	}

	if keyword != "" {
		query = query.Where("name LIKE ? OR content LIKE ? OR tags LIKE ?",
			"%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count quick replies: %w", err)
	}

	var replies []*models.QuickReply
	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&replies).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get quick replies: %w", err)
	}

	return replies, total, nil
}

// UseQuickReply 使用快速回复（增加使用计数）
func (s *AutomationService) UseQuickReply(ctx context.Context, replyID uint) error {
	return s.db.WithContext(ctx).Model(&models.QuickReply{}).
		Where("id = ?", replyID).
		UpdateColumn("usage_count", gorm.Expr("usage_count + ?", 1)).Error
}

// BatchOperations 批量操作相关方法

// BatchUpdateTickets 批量更新工单
func (s *AutomationService) BatchUpdateTickets(ctx context.Context, ticketIDs []uint, updates map[string]interface{}) error {
	if len(ticketIDs) == 0 {
		return fmt.Errorf("no tickets specified")
	}

	// 验证更新字段
	allowedFields := map[string]bool{
		"status":           true,
		"priority":         true,
		"assigned_user_id": true,
		"type":             true,
	}

	validUpdates := make(map[string]interface{})
	for key, value := range updates {
		if !allowedFields[key] {
			return fmt.Errorf("field %s is not allowed for batch update", key)
		}
		validUpdates[key] = value
	}

	validUpdates["updated_at"] = time.Now()

	return s.db.WithContext(ctx).Model(&models.Ticket{}).
		Where("id IN ?", ticketIDs).
		Updates(validUpdates).Error
}

// BatchAssignTickets 批量分配工单
func (s *AutomationService) BatchAssignTickets(ctx context.Context, ticketIDs []uint, userID uint) error {
	// 验证用户存在
	var user models.User
	if err := s.db.WithContext(ctx).First(&user, userID).Error; err != nil {
		return fmt.Errorf("assigned user not found: %w", err)
	}

	updates := map[string]interface{}{
		"assigned_user_id": userID,
		"updated_at":       time.Now(),
	}

	return s.BatchUpdateTickets(ctx, ticketIDs, updates)
}

// ClassifyTicket 工单自动分类
func (s *AutomationService) ClassifyTicket(ctx context.Context, ticket *models.Ticket) error {
	// 基于关键词的简单分类逻辑
	content := strings.ToLower(ticket.Title + " " + ticket.Description)

	// 定义分类规则
	classificationRules := map[string][]string{
		"bug":     {"bug", "error", "issue", "problem", "crash", "fail"},
		"feature": {"feature", "enhancement", "improvement", "add", "new"},
		"support": {"help", "support", "question", "how to", "guidance"},
		"urgent":  {"urgent", "critical", "emergency", "asap", "immediately"},
	}

	// 应用分类规则
	for category, keywords := range classificationRules {
		for _, keyword := range keywords {
			if strings.Contains(content, keyword) {
				// 更新工单类型或添加标签
				updates := map[string]interface{}{
					"type":       category,
					"updated_at": time.Now(),
				}

				if category == "urgent" {
					updates["priority"] = "high"
				}

				return s.db.WithContext(ctx).Model(ticket).Updates(updates).Error
			}
		}
	}

	return nil
}
