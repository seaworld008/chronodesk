package services

import (
	"context"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
	"gongdan-system/internal/models"
)

// EscalationService 升级服务
type EscalationService struct {
	db                *gorm.DB
	automationService *AutomationService
}

// NewEscalationService 创建升级服务实例
func NewEscalationService(db *gorm.DB) *EscalationService {
	return &EscalationService{
		db:                db,
		automationService: NewAutomationService(db),
	}
}

// TicketSLAStatus SLA状态结构
type TicketSLAStatus struct {
	TicketID              uint      `json:"ticket_id"`
	ResponseDeadline      time.Time `json:"response_deadline"`
	ResolutionDeadline    time.Time `json:"resolution_deadline"`
	IsResponseOverdue     bool      `json:"is_response_overdue"`
	IsResolutionOverdue   bool      `json:"is_resolution_overdue"`
	ResponseOverdueMinutes int64    `json:"response_overdue_minutes"`
	ResolutionOverdueMinutes int64  `json:"resolution_overdue_minutes"`
	SLAConfig             *models.SLAConfig `json:"sla_config,omitempty"`
}

// CheckSLAViolations 检查SLA违规
func (s *EscalationService) CheckSLAViolations(ctx context.Context) error {
	log.Println("开始检查SLA违规...")

	// 获取所有未关闭的工单
	var tickets []models.Ticket
	if err := s.db.WithContext(ctx).Where("status IN ?", []string{"open", "in_progress"}).Find(&tickets).Error; err != nil {
		return fmt.Errorf("failed to get open tickets: %w", err)
	}

	violationCount := 0
	for _, ticket := range tickets {
		status, err := s.CheckTicketSLA(ctx, &ticket)
		if err != nil {
			log.Printf("Failed to check SLA for ticket %d: %v", ticket.ID, err)
			continue
		}

		// 处理违规情况
		if status.IsResponseOverdue || status.IsResolutionOverdue {
			violationCount++
			if err := s.HandleSLAViolation(ctx, &ticket, status); err != nil {
				log.Printf("Failed to handle SLA violation for ticket %d: %v", ticket.ID, err)
			}
		}
	}

	log.Printf("SLA检查完成，发现 %d 个违规工单", violationCount)
	return nil
}

// CheckTicketSLA 检查单个工单的SLA状态
func (s *EscalationService) CheckTicketSLA(ctx context.Context, ticket *models.Ticket) (*TicketSLAStatus, error) {
	// 获取适用的SLA配置
	slaConfig, err := s.automationService.GetSLAConfigForTicket(ctx, ticket)
	if err != nil {
		return nil, fmt.Errorf("failed to get SLA config: %w", err)
	}

	// 计算SLA截止时间
	responseDeadline, resolutionDeadline, err := s.automationService.CalculateSLADeadlines(ctx, ticket, slaConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate SLA deadlines: %w", err)
	}

	now := time.Now()
	status := &TicketSLAStatus{
		TicketID:           ticket.ID,
		ResponseDeadline:   responseDeadline,
		ResolutionDeadline: resolutionDeadline,
		SLAConfig:         slaConfig,
	}

	// 检查响应超时
	if now.After(responseDeadline) && !s.hasFirstResponse(ctx, ticket.ID) {
		status.IsResponseOverdue = true
		status.ResponseOverdueMinutes = int64(now.Sub(responseDeadline).Minutes())
	}

	// 检查解决超时
	if now.After(resolutionDeadline) && ticket.Status != "resolved" && ticket.Status != "closed" {
		status.IsResolutionOverdue = true
		status.ResolutionOverdueMinutes = int64(now.Sub(resolutionDeadline).Minutes())
	}

	return status, nil
}

// hasFirstResponse 检查是否有首次响应
func (s *EscalationService) hasFirstResponse(ctx context.Context, ticketID uint) bool {
	var count int64
	s.db.WithContext(ctx).Model(&models.TicketComment{}).
		Where("ticket_id = ? AND is_system = ?", ticketID, false).
		Count(&count)
	return count > 0
}

// HandleSLAViolation 处理SLA违规
func (s *EscalationService) HandleSLAViolation(ctx context.Context, ticket *models.Ticket, status *TicketSLAStatus) error {
	log.Printf("处理工单 %d 的SLA违规", ticket.ID)

	// 获取升级规则
	escalationRules, err := status.SLAConfig.GetEscalationRules()
	if err != nil {
		return fmt.Errorf("failed to get escalation rules: %w", err)
	}

	// 应用升级规则
	for _, rule := range escalationRules {
		shouldTrigger := false
		var overdueMinutes int64

		if status.IsResponseOverdue {
			overdueMinutes = status.ResponseOverdueMinutes
		} else if status.IsResolutionOverdue {
			overdueMinutes = status.ResolutionOverdueMinutes
		}

		if overdueMinutes >= int64(rule.TriggerMinutes) {
			shouldTrigger = true
		}

		if shouldTrigger {
			if err := s.executeEscalationRule(ctx, ticket, &rule, overdueMinutes); err != nil {
				log.Printf("Failed to execute escalation rule for ticket %d: %v", ticket.ID, err)
			}
		}
	}

	// 更新SLA统计
	if err := s.updateSLAStats(ctx, status.SLAConfig.ID, false); err != nil {
		log.Printf("Failed to update SLA stats: %v", err)
	}

	// 记录违规日志
	return s.recordSLAViolation(ctx, ticket, status)
}

// executeEscalationRule 执行升级规则
func (s *EscalationService) executeEscalationRule(ctx context.Context, ticket *models.Ticket, rule *models.EscalationRule, overdueMinutes int64) error {
	switch rule.Action {
	case "escalate_to_manager":
		return s.escalateToManager(ctx, ticket, rule.TargetUserID, overdueMinutes)
	case "notify_admin":
		return s.notifyAdmin(ctx, ticket, rule.NotifyUsers, overdueMinutes)
	case "change_priority":
		return s.increasePriority(ctx, ticket)
	default:
		return fmt.Errorf("unknown escalation action: %s", rule.Action)
	}
}

// escalateToManager 升级给管理员
func (s *EscalationService) escalateToManager(ctx context.Context, ticket *models.Ticket, managerID *uint, overdueMinutes int64) error {
	if managerID == nil {
		// 如果没有指定管理员，查找默认管理员
		var manager models.User
		if err := s.db.WithContext(ctx).Where("role = ?", "admin").First(&manager).Error; err != nil {
			return fmt.Errorf("no manager found for escalation")
		}
		managerID = &manager.ID
	}

	updates := map[string]interface{}{
		"assigned_to_id": *managerID,
		"priority":       "high",
		"updated_at":     time.Now(),
	}

	if err := s.db.WithContext(ctx).Model(ticket).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to escalate ticket: %w", err)
	}

	// 添加系统评论
	comment := &models.TicketComment{
		TicketID: ticket.ID,
		UserID:   1, // 系统用户
		Content:  fmt.Sprintf("工单因SLA违规自动升级给管理员，超时 %d 分钟", overdueMinutes),
		Type:     models.CommentTypeSystem,
	}

	return s.db.WithContext(ctx).Create(comment).Error
}

// notifyAdmin 通知管理员
func (s *EscalationService) notifyAdmin(ctx context.Context, ticket *models.Ticket, notifyUsers []uint, overdueMinutes int64) error {
	// 这里可以集成通知服务发送邮件或其他通知
	log.Printf("通知管理员：工单 %d 超时 %d 分钟", ticket.ID, overdueMinutes)

	// 添加系统评论
	comment := &models.TicketComment{
		TicketID: ticket.ID,
		UserID:   1, // 系统用户
		Content:  fmt.Sprintf("工单因SLA违规已通知管理员，超时 %d 分钟", overdueMinutes),
		Type:     models.CommentTypeSystem,
	}

	return s.db.WithContext(ctx).Create(comment).Error
}

// increasePriority 提升优先级
func (s *EscalationService) increasePriority(ctx context.Context, ticket *models.Ticket) error {
	newPriority := ""
	switch ticket.Priority {
	case "low":
		newPriority = "normal"
	case "normal":
		newPriority = "high"
	case "high":
		newPriority = "critical"
	case "critical":
		return nil // 已经是最高优先级
	default:
		newPriority = "normal"
	}

	updates := map[string]interface{}{
		"priority":   newPriority,
		"updated_at": time.Now(),
	}

	if err := s.db.WithContext(ctx).Model(ticket).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to increase priority: %w", err)
	}

	// 添加系统评论
	comment := &models.TicketComment{
		TicketID: ticket.ID,
		UserID:   1, // 系统用户
		Content:  fmt.Sprintf("工单因SLA违规自动提升优先级从 %s 到 %s", ticket.Priority, newPriority),
		Type:     models.CommentTypeSystem,
	}

	return s.db.WithContext(ctx).Create(comment).Error
}

// updateSLAStats 更新SLA统计
func (s *EscalationService) updateSLAStats(ctx context.Context, slaConfigID uint, compliance bool) error {
	updates := map[string]interface{}{
		"applied_count": gorm.Expr("applied_count + ?", 1),
	}

	if !compliance {
		updates["violation_count"] = gorm.Expr("violation_count + ?", 1)
	}

	if err := s.db.WithContext(ctx).Model(&models.SLAConfig{}).
		Where("id = ?", slaConfigID).
		Updates(updates).Error; err != nil {
		return err
	}

	// 重新计算合规率
	var config models.SLAConfig
	if err := s.db.WithContext(ctx).First(&config, slaConfigID).Error; err != nil {
		return err
	}

	complianceRate := 0.0
	if config.AppliedCount > 0 {
		complianceRate = float64(config.AppliedCount-config.ViolationCount) / float64(config.AppliedCount) * 100
	}

	return s.db.WithContext(ctx).Model(&config).Update("compliance_rate", complianceRate).Error
}

// recordSLAViolation 记录SLA违规
func (s *EscalationService) recordSLAViolation(ctx context.Context, ticket *models.Ticket, status *TicketSLAStatus) error {
	// 这里可以记录到专门的SLA违规日志表
	log.Printf("记录SLA违规：工单 %d，响应超时: %v，解决超时: %v", 
		ticket.ID, status.IsResponseOverdue, status.IsResolutionOverdue)
	return nil
}

// GetSLADashboard 获取SLA仪表板数据
func (s *EscalationService) GetSLADashboard(ctx context.Context) (map[string]interface{}, error) {
	dashboard := make(map[string]interface{})

	// 获取所有SLA配置统计
	var configs []models.SLAConfig
	if err := s.db.WithContext(ctx).Where("is_active = ?", true).Find(&configs).Error; err != nil {
		return nil, fmt.Errorf("failed to get SLA configs: %w", err)
	}

	totalApplied := int64(0)
	totalViolations := int64(0)
	avgComplianceRate := 0.0

	configStats := make([]map[string]interface{}, 0)
	for _, config := range configs {
		totalApplied += config.AppliedCount
		totalViolations += config.ViolationCount
		avgComplianceRate += config.ComplianceRate

		configStats = append(configStats, map[string]interface{}{
			"config_id":       config.ID,
			"name":           config.Name,
			"applied_count":  config.AppliedCount,
			"violation_count": config.ViolationCount,
			"compliance_rate": config.ComplianceRate,
		})
	}

	if len(configs) > 0 {
		avgComplianceRate /= float64(len(configs))
	}

	dashboard["sla_configs"] = configStats
	dashboard["total_applied"] = totalApplied
	dashboard["total_violations"] = totalViolations
	dashboard["overall_compliance_rate"] = avgComplianceRate

	// 获取当前超时工单数量
	currentViolations, err := s.getCurrentViolationCount(ctx)
	if err != nil {
		log.Printf("Failed to get current violation count: %v", err)
	} else {
		dashboard["current_violations"] = currentViolations
	}

	return dashboard, nil
}

// getCurrentViolationCount 获取当前违规工单数量
func (s *EscalationService) getCurrentViolationCount(ctx context.Context) (int64, error) {
	var tickets []models.Ticket
	if err := s.db.WithContext(ctx).Where("status IN ?", []string{"open", "in_progress"}).Find(&tickets).Error; err != nil {
		return 0, err
	}

	violationCount := int64(0)
	for _, ticket := range tickets {
		status, err := s.CheckTicketSLA(ctx, &ticket)
		if err != nil {
			continue
		}
		if status.IsResponseOverdue || status.IsResolutionOverdue {
			violationCount++
		}
	}

	return violationCount, nil
}

// ScheduleSLACheck 定时SLA检查任务
func (s *EscalationService) ScheduleSLACheck() {
	ticker := time.NewTicker(15 * time.Minute) // 每15分钟检查一次
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx := context.Background()
			if err := s.CheckSLAViolations(ctx); err != nil {
				log.Printf("SLA检查失败: %v", err)
			}
		}
	}
}

// GetTicketSLAStatus 获取工单SLA状态
func (s *EscalationService) GetTicketSLAStatus(ctx context.Context, ticketID uint) (*TicketSLAStatus, error) {
	var ticket models.Ticket
	if err := s.db.WithContext(ctx).First(&ticket, ticketID).Error; err != nil {
		return nil, fmt.Errorf("ticket not found: %w", err)
	}

	return s.CheckTicketSLA(ctx, &ticket)
}