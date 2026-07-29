package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/gorm"
)

// EscalationService 升级服务
type EscalationService struct {
	db                *gorm.DB
	automationService *AutomationService
	agentNative       *AgentNativeService
}

const (
	slaMonitorActorID              = "sla-monitor"
	slaReservationTTL              = 5 * time.Minute
	slaCompletedRetentionTTL       = 10 * 365 * 24 * time.Hour
	SLABreachEventType             = "io.chronodesk.ticket.sla.breached.v1"
	SLAEscalationOutboxDestination = "sla_escalation"
	slaEscalationDestinationID     = "breach"
)

type slaExecutionContext struct {
	OccurrenceID  string
	TraceID       string
	CorrelationID string
	CausationID   string
}

type slaEscalationEventData struct {
	TicketID                 uint                    `json:"ticket_id"`
	SLAConfigID              uint                    `json:"sla_config_id"`
	SLAOccurrenceID          string                  `json:"sla_occurrence_id"`
	ResponseDeadline         time.Time               `json:"response_deadline"`
	ResolutionDeadline       time.Time               `json:"resolution_deadline"`
	IsResponseOverdue        bool                    `json:"response_overdue"`
	IsResolutionOverdue      bool                    `json:"resolution_overdue"`
	ResponseOverdueMinutes   int64                   `json:"response_overdue_minutes"`
	ResolutionOverdueMinutes int64                   `json:"resolution_overdue_minutes"`
	EscalationRules          []models.EscalationRule `json:"escalation_rules"`
	ChangedFields            []string                `json:"changed_fields"`
}

func (s *EscalationService) SetAgentNativeService(native *AgentNativeService) {
	s.agentNative = native
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
	TicketID                 uint              `json:"ticket_id"`
	ResponseDeadline         time.Time         `json:"response_deadline"`
	ResolutionDeadline       time.Time         `json:"resolution_deadline"`
	IsResponseOverdue        bool              `json:"is_response_overdue"`
	IsResolutionOverdue      bool              `json:"is_resolution_overdue"`
	ResponseOverdueMinutes   int64             `json:"response_overdue_minutes"`
	ResolutionOverdueMinutes int64             `json:"resolution_overdue_minutes"`
	SLAConfig                *models.SLAConfig `json:"sla_config,omitempty"`
}

// CheckSLAViolations 检查SLA违规
func (s *EscalationService) CheckSLAViolations(ctx context.Context) error {
	log.Println("开始检查SLA违规...")
	if s == nil || s.agentNative == nil {
		return errors.New("agent-native SLA escalation service is unavailable")
	}

	// 获取所有未关闭的工单
	var tickets []models.Ticket
	if err := s.db.WithContext(ctx).Where("status IN ?", []string{"open", "in_progress"}).Find(&tickets).Error; err != nil {
		return fmt.Errorf("failed to get open tickets: %w", err)
	}

	violationCount := 0
	var scanErrors []error
	for _, ticket := range tickets {
		status, err := s.CheckTicketSLA(ctx, &ticket)
		if err != nil {
			log.Printf("Failed to check SLA for ticket %d: %v", ticket.ID, err)
			scanErrors = append(scanErrors, fmt.Errorf("check ticket %d SLA: %w", ticket.ID, err))
			continue
		}

		// 处理违规情况
		if status.IsResponseOverdue || status.IsResolutionOverdue {
			violationCount++
			if err := s.HandleSLAViolation(ctx, &ticket, status); err != nil {
				log.Printf("Failed to handle SLA violation for ticket %d: %v", ticket.ID, err)
				scanErrors = append(scanErrors, fmt.Errorf("handle ticket %d SLA violation: %w", ticket.ID, err))
			}
		}
	}

	log.Printf("SLA检查完成，发现 %d 个违规工单", violationCount)
	return errors.Join(scanErrors...)
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
		SLAConfig:          slaConfig,
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
		Where("ticket_id = ? AND type != ?", ticketID, models.CommentTypeSystem).
		Count(&count)
	return count > 0
}

// HandleSLAViolation 处理SLA违规
func (s *EscalationService) HandleSLAViolation(ctx context.Context, ticket *models.Ticket, status *TicketSLAStatus) error {
	if s == nil || s.agentNative == nil {
		return errors.New("agent-native SLA escalation service is unavailable")
	}
	if ticket == nil || ticket.ID == 0 || status == nil || status.SLAConfig == nil {
		return errors.New("ticket and SLA status are required")
	}
	log.Printf("处理工单 %d 的SLA违规", ticket.ID)

	execution, err := newSLAExecutionContext(ticket, status)
	if err != nil {
		return err
	}
	escalationRules, err := status.SLAConfig.GetEscalationRules()
	if err != nil {
		return fmt.Errorf("failed to get escalation rules: %w", err)
	}
	snapshot := newSLAEscalationEventData(ticket.ID, status, escalationRules, execution)
	breachEvent, err := s.markSLABreach(ctx, ticket, snapshot, execution)
	if err != nil {
		return fmt.Errorf("failed to mark SLA breach: %w", err)
	}
	if breachEvent == nil {
		return errors.New("SLA breach event is required")
	}
	execution.CausationID = breachEvent.ID

	var executionErrors []error
	if err := s.executeSLAEscalationSnapshot(
		ctx,
		snapshot,
		execution,
		breachEvent.ID,
	); err != nil {
		executionErrors = append(executionErrors, err)
	}

	if err := s.db.WithContext(ctx).First(ticket, ticket.ID).Error; err != nil {
		executionErrors = append(executionErrors, fmt.Errorf("reload SLA ticket: %w", err))
	}

	// 记录违规日志
	if err := s.recordSLAViolation(ctx, ticket, status); err != nil {
		executionErrors = append(executionErrors, err)
	}
	return errors.Join(executionErrors...)
}

// ExecuteDomainEvent resumes SLA statistics and escalation rules from the
// immutable breach-event snapshot. It deliberately does not recalculate the
// ticket SLA or reload rule configuration: the ticket may have been closed and
// the configuration may have changed after the breach transaction committed.
func (s *EscalationService) ExecuteDomainEvent(ctx context.Context, event CloudEventEnvelope) error {
	if s == nil || s.agentNative == nil {
		return errors.New("agent-native SLA escalation service is unavailable")
	}
	if event.Type != SLABreachEventType {
		return fmt.Errorf("unsupported SLA escalation event type %q", event.Type)
	}
	if strings.TrimSpace(event.ID) == "" {
		return errors.New("SLA breach event id is required")
	}
	if event.ActorType != models.ActorTypeSystem || event.ActorID != slaMonitorActorID {
		return fmt.Errorf("SLA breach event has invalid actor %s/%s", event.ActorType, event.ActorID)
	}
	var snapshot slaEscalationEventData
	if err := json.Unmarshal(event.Data, &snapshot); err != nil {
		return fmt.Errorf("decode SLA breach event: %w", err)
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	traceID := strings.TrimSpace(event.TraceID)
	if traceID == "" {
		traceID = snapshot.SLAOccurrenceID
	}
	correlationID := strings.TrimSpace(event.CorrelationID)
	if correlationID == "" {
		correlationID = traceID
	}
	execution := &slaExecutionContext{
		OccurrenceID:  snapshot.SLAOccurrenceID,
		TraceID:       traceID,
		CorrelationID: correlationID,
		CausationID:   event.ID,
	}
	return s.executeSLAEscalationSnapshot(ctx, &snapshot, execution, event.ID)
}

func (s *EscalationService) executeSLAEscalationSnapshot(
	ctx context.Context,
	snapshot *slaEscalationEventData,
	execution *slaExecutionContext,
	breachEventID string,
) error {
	var executionErrors []error
	if err := s.updateSLAStatsOnce(
		ctx,
		snapshot.SLAConfigID,
		execution,
		breachEventID,
	); err != nil {
		executionErrors = append(executionErrors, fmt.Errorf("update SLA stats: %w", err))
	}
	overdueMinutes := maxInt64(
		snapshot.ResponseOverdueMinutes,
		snapshot.ResolutionOverdueMinutes,
	)
	for index := range snapshot.EscalationRules {
		rule := &snapshot.EscalationRules[index]
		if overdueMinutes < int64(rule.TriggerMinutes) {
			continue
		}
		if err := s.executeEscalationRuleNative(
			ctx,
			snapshot.TicketID,
			rule,
			overdueMinutes,
			execution,
		); err != nil {
			executionErrors = append(
				executionErrors,
				fmt.Errorf("execute SLA rule %s: %w", slaRuleIdentity(rule), err),
			)
		}
	}
	return errors.Join(executionErrors...)
}

// executeEscalationRule 执行升级规则
func (s *EscalationService) executeEscalationRule(ctx context.Context, ticket *models.Ticket, rule *models.EscalationRule, overdueMinutes int64) error {
	if ticket == nil {
		return errors.New("ticket is required")
	}
	execution := legacySLAExecutionContext(ticket)
	return s.executeEscalationRuleNative(ctx, ticket.ID, rule, overdueMinutes, execution)
}

func (s *EscalationService) executeEscalationRuleNative(
	ctx context.Context,
	ticketID uint,
	rule *models.EscalationRule,
	overdueMinutes int64,
	execution *slaExecutionContext,
) error {
	if s == nil || s.agentNative == nil {
		return errors.New("agent-native SLA escalation service is unavailable")
	}
	if rule == nil || execution == nil {
		return errors.New("SLA escalation rule and execution context are required")
	}
	switch rule.Action {
	case "escalate_to_manager":
		return s.escalateToManagerNative(ctx, ticketID, rule, overdueMinutes, execution)
	case "notify_admin":
		return s.notifyAdminNative(ctx, ticketID, rule, execution)
	case "change_priority":
		return s.increasePriorityNative(ctx, ticketID, rule, execution)
	default:
		return fmt.Errorf("unknown escalation action: %s", rule.Action)
	}
}

// escalateToManager 升级给管理员
func (s *EscalationService) escalateToManager(ctx context.Context, ticket *models.Ticket, managerID *uint, overdueMinutes int64) error {
	if ticket == nil {
		return errors.New("ticket is required")
	}
	rule := &models.EscalationRule{
		Action:       "escalate_to_manager",
		TargetUserID: managerID,
	}
	return s.escalateToManagerNative(
		ctx,
		ticket.ID,
		rule,
		overdueMinutes,
		legacySLAExecutionContext(ticket),
	)
}

func (s *EscalationService) escalateToManagerNative(
	ctx context.Context,
	ticketID uint,
	rule *models.EscalationRule,
	overdueMinutes int64,
	execution *slaExecutionContext,
) error {
	managerID, err := s.resolveSLAManager(ctx, ticketID, rule.TargetUserID)
	if err != nil {
		return err
	}
	var manager models.User
	if err := s.db.WithContext(ctx).Select("id").First(&manager, managerID).Error; err != nil {
		return fmt.Errorf("SLA manager %d not found: %w", managerID, err)
	}
	ruleID := slaRuleIdentity(rule)
	eventID, _, err := s.executeSLAUpdate(
		ctx,
		ticketID,
		execution,
		ruleID,
		"manager-update",
		"io.chronodesk.ticket.escalated.v1",
		map[string]any{
			"sla_rule_action":    rule.Action,
			"sla_rule_threshold": rule.TriggerMinutes,
			"overdue_minutes":    overdueMinutes,
			"manager_id":         managerID,
		},
		func(current *models.Ticket) (map[string]any, error) {
			changes := make(map[string]any)
			if current.AssignedToID == nil || *current.AssignedToID != managerID ||
				current.AssignedToActorType != models.ActorTypeHuman ||
				current.AssignedToActorID != strconv.FormatUint(uint64(managerID), 10) {
				changes["assigned_to_id"] = managerID
				changes["assigned_to_actor_type"] = models.ActorTypeHuman
				changes["assigned_to_actor_id"] = strconv.FormatUint(uint64(managerID), 10)
				changes["assigned_to_service_principal_id"] = nil
			}
			if current.Priority == models.TicketPriorityLow ||
				current.Priority == models.TicketPriorityNormal {
				changes["priority"] = models.TicketPriorityHigh
			}
			if !current.IsEscalated {
				changes["is_escalated"] = true
			}
			return changes, nil
		},
	)
	if err != nil {
		return err
	}
	causationID := eventID
	if causationID == "" {
		causationID = execution.CausationID
	}
	return s.executeSLAComment(
		ctx,
		ticketID,
		execution,
		ruleID,
		"manager-comment",
		fmt.Sprintf(
			"工单因 SLA 违规自动升级给管理员 %d（规则阈值 %d 分钟）",
			managerID,
			rule.TriggerMinutes,
		),
		causationID,
	)
}

// notifyAdmin 通知管理员
func (s *EscalationService) notifyAdmin(ctx context.Context, ticket *models.Ticket, notifyUsers []uint, overdueMinutes int64) error {
	if ticket == nil {
		return errors.New("ticket is required")
	}
	rule := &models.EscalationRule{
		Action:      "notify_admin",
		NotifyUsers: append([]uint(nil), notifyUsers...),
	}
	return s.notifyAdminNative(ctx, ticket.ID, rule, legacySLAExecutionContext(ticket))
}

func (s *EscalationService) notifyAdminNative(
	ctx context.Context,
	ticketID uint,
	rule *models.EscalationRule,
	execution *slaExecutionContext,
) error {
	log.Printf("通知管理员：工单 %d 达到 SLA 阈值 %d 分钟", ticketID, rule.TriggerMinutes)
	return s.executeSLAComment(
		ctx,
		ticketID,
		execution,
		slaRuleIdentity(rule),
		"notify-comment",
		fmt.Sprintf("工单因 SLA 违规已通知管理员（规则阈值 %d 分钟）", rule.TriggerMinutes),
		execution.CausationID,
	)
}

// increasePriority 提升优先级
func (s *EscalationService) increasePriority(ctx context.Context, ticket *models.Ticket) error {
	if ticket == nil {
		return errors.New("ticket is required")
	}
	rule := &models.EscalationRule{Action: "change_priority"}
	return s.increasePriorityNative(ctx, ticket.ID, rule, legacySLAExecutionContext(ticket))
}

func (s *EscalationService) increasePriorityNative(
	ctx context.Context,
	ticketID uint,
	rule *models.EscalationRule,
	execution *slaExecutionContext,
) error {
	ruleID := slaRuleIdentity(rule)
	eventID, changed, err := s.executeSLAUpdate(
		ctx,
		ticketID,
		execution,
		ruleID,
		"priority-update",
		"io.chronodesk.ticket.updated.v1",
		map[string]any{
			"sla_rule_action":    rule.Action,
			"sla_rule_threshold": rule.TriggerMinutes,
		},
		func(current *models.Ticket) (map[string]any, error) {
			next, ok := nextSLAPriority(current.Priority)
			if !ok {
				return nil, nil
			}
			return map[string]any{"priority": next}, nil
		},
	)
	if err != nil {
		return err
	}
	if !changed && eventID == "" {
		return nil
	}
	causationID := eventID
	if causationID == "" {
		causationID = execution.CausationID
	}
	return s.executeSLAComment(
		ctx,
		ticketID,
		execution,
		ruleID,
		"priority-comment",
		fmt.Sprintf("工单因 SLA 违规自动执行优先级提升（规则阈值 %d 分钟）", rule.TriggerMinutes),
		causationID,
	)
}

func (s *EscalationService) executeSLAUpdate(
	ctx context.Context,
	ticketID uint,
	execution *slaExecutionContext,
	ruleID string,
	step string,
	eventType string,
	eventData map[string]any,
	buildChanges func(*models.Ticket) (map[string]any, error),
) (string, bool, error) {
	reservation, err := s.reserveSLAOperation(ctx, "sla.rule."+step, execution, ruleID, step)
	if err != nil {
		return "", false, err
	}
	if reservation.Replayed {
		return reservation.Record.EventID, reservation.Record.EventID != "", nil
	}
	var current models.Ticket
	if err := s.db.WithContext(ctx).First(&current, ticketID).Error; err != nil {
		return "", false, s.failSLAReservation(ctx, reservation.Record.ID, err)
	}
	changes, err := buildChanges(&current)
	if err != nil {
		return "", false, s.failSLAReservation(ctx, reservation.Record.ID, err)
	}
	if len(changes) == 0 {
		if err := s.completeSLAIdempotencyNoop(ctx, reservation.Record.ID, &current, ""); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	data := make(map[string]any, len(eventData)+3)
	for key, value := range eventData {
		data[key] = value
	}
	data["ticket_id"] = ticketID
	data["sla_occurrence_id"] = execution.OccurrenceID
	data["changed_fields"] = sortedMapKeys(changes)
	result, err := s.agentNative.UpdateTicketVersion(ctx, VersionedTicketUpdateInput{
		TicketID:                 ticketID,
		ExpectedVersion:          current.Version,
		Actor:                    models.SystemActor(slaMonitorActorID),
		SourceProtocol:           "scheduler",
		Changes:                  changes,
		EventType:                eventType,
		EventData:                data,
		TraceID:                  execution.TraceID,
		CorrelationID:            execution.CorrelationID,
		CausationID:              execution.CausationID,
		IdempotencyRecordID:      reservation.Record.ID,
		IdempotencyCompletionTTL: slaCompletedRetentionTTL,
	})
	if err != nil {
		return "", false, s.failSLAReservation(ctx, reservation.Record.ID, err)
	}
	return result.Event.ID, true, nil
}

func (s *EscalationService) executeSLAComment(
	ctx context.Context,
	ticketID uint,
	execution *slaExecutionContext,
	ruleID string,
	step string,
	content string,
	causationID string,
) error {
	reservation, err := s.reserveSLAOperation(ctx, "sla.rule."+step, execution, ruleID, step)
	if err != nil {
		return err
	}
	if reservation.Replayed {
		return nil
	}
	var current models.Ticket
	if err := s.db.WithContext(ctx).First(&current, ticketID).Error; err != nil {
		return s.failSLAReservation(ctx, reservation.Record.ID, err)
	}
	compatibilityUserID := uint(0)
	if s.agentNative.systemCompatibilityUserID == 0 {
		compatibilityUserID = current.CreatedByID
	}
	_, err = s.agentNative.CreateComment(ctx, NativeCommentInput{
		TicketID:                 ticketID,
		ExpectedVersion:          current.Version,
		Actor:                    models.SystemActor(slaMonitorActorID),
		CompatibilityUserID:      compatibilityUserID,
		SourceProtocol:           "scheduler",
		Content:                  content,
		ContentType:              "text",
		Type:                     models.CommentTypeSystem,
		Reason:                   "SLA escalation rule " + ruleID,
		TraceID:                  execution.TraceID,
		CorrelationID:            execution.CorrelationID,
		CausationID:              causationID,
		IdempotencyRecordID:      reservation.Record.ID,
		IdempotencyCompletionTTL: slaCompletedRetentionTTL,
	})
	if err != nil {
		return s.failSLAReservation(ctx, reservation.Record.ID, err)
	}
	return nil
}

func (s *EscalationService) resolveSLAManager(
	ctx context.Context,
	ticketID uint,
	configured *uint,
) (uint, error) {
	if configured != nil && *configured > 0 {
		return *configured, nil
	}
	var current models.Ticket
	if err := s.db.WithContext(ctx).
		Select("id", "assigned_to_id", "assigned_to_actor_type", "is_escalated").
		First(&current, ticketID).Error; err != nil {
		return 0, err
	}
	if current.IsEscalated &&
		current.AssignedToID != nil &&
		current.AssignedToActorType == models.ActorTypeHuman {
		return *current.AssignedToID, nil
	}
	var manager models.User
	if err := s.db.WithContext(ctx).
		Where("role = ? AND status = ?", models.RoleAdmin, models.UserStatusActive).
		Order("id ASC").
		First(&manager).Error; err != nil {
		return 0, fmt.Errorf("no active manager found for SLA escalation: %w", err)
	}
	return manager.ID, nil
}

func nextSLAPriority(priority models.TicketPriority) (models.TicketPriority, bool) {
	switch priority {
	case models.TicketPriorityLow:
		return models.TicketPriorityNormal, true
	case models.TicketPriorityNormal:
		return models.TicketPriorityHigh, true
	case models.TicketPriorityHigh, models.TicketPriorityUrgent:
		return models.TicketPriorityCritical, true
	default:
		return "", false
	}
}

func newSLAEscalationEventData(
	ticketID uint,
	status *TicketSLAStatus,
	rules []models.EscalationRule,
	execution *slaExecutionContext,
) *slaEscalationEventData {
	ruleSnapshot := make([]models.EscalationRule, len(rules))
	copy(ruleSnapshot, rules)
	return &slaEscalationEventData{
		TicketID:                 ticketID,
		SLAConfigID:              status.SLAConfig.ID,
		SLAOccurrenceID:          execution.OccurrenceID,
		ResponseDeadline:         status.ResponseDeadline.UTC(),
		ResolutionDeadline:       status.ResolutionDeadline.UTC(),
		IsResponseOverdue:        status.IsResponseOverdue,
		IsResolutionOverdue:      status.IsResolutionOverdue,
		ResponseOverdueMinutes:   status.ResponseOverdueMinutes,
		ResolutionOverdueMinutes: status.ResolutionOverdueMinutes,
		EscalationRules:          ruleSnapshot,
	}
}

func (snapshot *slaEscalationEventData) Validate() error {
	if snapshot == nil ||
		snapshot.TicketID == 0 ||
		snapshot.SLAConfigID == 0 ||
		strings.TrimSpace(snapshot.SLAOccurrenceID) == "" {
		return errors.New("SLA breach event identity is incomplete")
	}
	if snapshot.ResponseDeadline.IsZero() || snapshot.ResolutionDeadline.IsZero() {
		return errors.New("SLA breach event deadlines are required")
	}
	if !snapshot.IsResponseOverdue && !snapshot.IsResolutionOverdue {
		return errors.New("SLA breach event does not contain a violation")
	}
	if snapshot.ResponseOverdueMinutes < 0 || snapshot.ResolutionOverdueMinutes < 0 {
		return errors.New("SLA breach event overdue minutes cannot be negative")
	}
	return nil
}

func newSLAExecutionContext(
	ticket *models.Ticket,
	status *TicketSLAStatus,
) (*slaExecutionContext, error) {
	if ticket == nil || status == nil || status.SLAConfig == nil {
		return nil, errors.New("ticket and SLA status are required")
	}
	if status.ResponseDeadline.IsZero() || status.ResolutionDeadline.IsZero() {
		return nil, errors.New("SLA deadlines are required")
	}
	identity := map[string]any{
		"ticket_id":           ticket.ID,
		"sla_config_id":       status.SLAConfig.ID,
		"response_deadline":   status.ResponseDeadline.UTC().Format(time.RFC3339Nano),
		"resolution_deadline": status.ResolutionDeadline.UTC().Format(time.RFC3339Nano),
	}
	digest := stableSLAHash(identity)
	occurrenceID := fmt.Sprintf("sla:%d:%s", ticket.ID, digest[:24])
	return &slaExecutionContext{
		OccurrenceID:  occurrenceID,
		TraceID:       occurrenceID,
		CorrelationID: occurrenceID,
		CausationID:   "",
	}, nil
}

func legacySLAExecutionContext(ticket *models.Ticket) *slaExecutionContext {
	deadline := ""
	if ticket != nil && ticket.SLADueDate != nil {
		deadline = ticket.SLADueDate.UTC().Format(time.RFC3339Nano)
	}
	ticketID := uint(0)
	if ticket != nil {
		ticketID = ticket.ID
	}
	digest := stableSLAHash(map[string]any{
		"ticket_id": ticketID,
		"deadline":  deadline,
	})
	occurrenceID := fmt.Sprintf("sla:%d:%s", ticketID, digest[:24])
	return &slaExecutionContext{
		OccurrenceID:  occurrenceID,
		TraceID:       occurrenceID,
		CorrelationID: occurrenceID,
		CausationID:   "",
	}
}

func (s *EscalationService) markSLABreach(
	ctx context.Context,
	ticket *models.Ticket,
	snapshot *slaEscalationEventData,
	execution *slaExecutionContext,
) (*models.DomainEvent, error) {
	if snapshot == nil {
		return nil, errors.New("SLA breach event snapshot is required")
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	reservation, err := s.reserveSLAOperation(
		ctx,
		"sla.breach.mark",
		execution,
		"",
		"breach",
	)
	if err != nil {
		return nil, err
	}
	if reservation.Replayed {
		if strings.TrimSpace(reservation.Record.EventID) == "" {
			return nil, errors.New("replayed SLA breach has no domain event")
		}
		var event models.DomainEvent
		if err := s.db.WithContext(ctx).
			First(&event, "id = ?", reservation.Record.EventID).Error; err != nil {
			return nil, fmt.Errorf("load replayed SLA breach event: %w", err)
		}
		return &event, nil
	}
	var current models.Ticket
	if err := s.db.WithContext(ctx).First(&current, ticket.ID).Error; err != nil {
		return nil, s.failSLAReservation(ctx, reservation.Record.ID, err)
	}
	changes := make(map[string]any, 2)
	if !current.SLABreached {
		changes["sla_breached"] = true
	}
	if current.SLADueDate == nil || !current.SLADueDate.Equal(snapshot.ResolutionDeadline) {
		changes["sla_due_date"] = snapshot.ResolutionDeadline
	}
	snapshot.ChangedFields = sortedMapKeys(changes)
	if len(changes) == 0 {
		event, err := s.appendNoopSLABreachEvent(
			ctx,
			reservation.Record.ID,
			&current,
			snapshot,
			execution,
		)
		if err != nil {
			return nil, s.failSLAReservation(ctx, reservation.Record.ID, err)
		}
		*ticket = current
		return event, nil
	}
	result, err := s.agentNative.UpdateTicketVersion(ctx, VersionedTicketUpdateInput{
		TicketID:                 current.ID,
		ExpectedVersion:          current.Version,
		Actor:                    models.SystemActor(slaMonitorActorID),
		SourceProtocol:           "scheduler",
		Changes:                  changes,
		EventType:                SLABreachEventType,
		EventData:                snapshot,
		TraceID:                  execution.TraceID,
		CorrelationID:            execution.CorrelationID,
		CausationID:              execution.CausationID,
		IdempotencyRecordID:      reservation.Record.ID,
		IdempotencyCompletionTTL: slaCompletedRetentionTTL,
		OutboxTargets:            s.slaBreachOutboxTargets(),
	})
	if err != nil {
		return nil, s.failSLAReservation(ctx, reservation.Record.ID, err)
	}
	*ticket = *result.Ticket
	return result.Event, nil
}

func (s *EscalationService) appendNoopSLABreachEvent(
	ctx context.Context,
	idempotencyRecordID string,
	ticket *models.Ticket,
	snapshot *slaEscalationEventData,
	execution *slaExecutionContext,
) (*models.DomainEvent, error) {
	var event *models.DomainEvent
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var appendErr error
		event, appendErr = s.agentNative.AppendDomainEventTx(
			ctx,
			tx,
			DomainEventInput{
				Type:            SLABreachEventType,
				Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
				Actor:           models.SystemActor(slaMonitorActorID),
				ResourceVersion: ticket.Version,
				TraceID:         execution.TraceID,
				CorrelationID:   execution.CorrelationID,
				CausationID:     execution.CausationID,
				Data:            snapshot,
			},
			s.slaBreachOutboxTargets(),
		)
		if appendErr != nil {
			return appendErr
		}
		return s.completeSLAIdempotencyNoopTx(
			ctx,
			tx,
			idempotencyRecordID,
			ticket,
			event.ID,
		)
	})
	if err != nil {
		return nil, err
	}
	return event, nil
}

func (s *EscalationService) slaBreachOutboxTargets() []OutboxTarget {
	targets := append([]OutboxTarget(nil), s.agentNative.defaultOutboxTargets...)
	for _, target := range targets {
		if target.Type == SLAEscalationOutboxDestination &&
			target.ID == slaEscalationDestinationID {
			return targets
		}
	}
	return append(targets, OutboxTarget{
		Type:        SLAEscalationOutboxDestination,
		ID:          slaEscalationDestinationID,
		MaxAttempts: 8,
	})
}

func (s *EscalationService) reserveSLAOperation(
	ctx context.Context,
	operation string,
	execution *slaExecutionContext,
	ruleID string,
	step string,
) (*IdempotencyReservation, error) {
	if execution == nil {
		return nil, errors.New("SLA execution context is required")
	}
	request, err := json.Marshal(map[string]any{
		"sla_occurrence_id": execution.OccurrenceID,
		"rule_id":           ruleID,
		"step":              step,
	})
	if err != nil {
		return nil, err
	}
	key := strings.Join([]string{execution.OccurrenceID, ruleID, step}, ":")
	if len(key) > 255 {
		key = "sla:" + stableSLAHash(key)
	}
	return s.agentNative.ReserveIdempotency(
		ctx,
		models.SystemActor(slaMonitorActorID),
		operation,
		key,
		request,
		slaReservationTTL,
	)
}

func (s *EscalationService) completeSLAIdempotencyNoop(
	ctx context.Context,
	recordID string,
	ticket *models.Ticket,
	eventID string,
) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.completeSLAIdempotencyNoopTx(ctx, tx, recordID, ticket, eventID)
	})
}

func (s *EscalationService) completeSLAIdempotencyNoopTx(
	ctx context.Context,
	tx *gorm.DB,
	recordID string,
	ticket *models.Ticket,
	eventID string,
) error {
	receipt := OperationReceipt{
		OperationID:     newNativeID(),
		ResourceID:      strconv.FormatUint(uint64(ticket.ID), 10),
		ResourceVersion: ticket.Version,
		EventID:         eventID,
		ChangedFields:   []string{},
	}
	if err := s.agentNative.CompleteIdempotencyTxWithTTL(
		ctx,
		tx,
		recordID,
		http.StatusOK,
		receipt,
		receipt.ResourceID,
		eventID,
		slaCompletedRetentionTTL,
	); err != nil {
		return err
	}
	return s.agentNative.storeIdempotencySnapshotTx(
		ctx,
		tx,
		recordID,
		ticket.ToResponse(),
	)
}

func (s *EscalationService) failSLAReservation(
	ctx context.Context,
	recordID string,
	operationErr error,
) error {
	failErr := s.agentNative.FailIdempotency(
		ctx,
		recordID,
		AgentNativeErrorCode(operationErr),
	)
	return errors.Join(operationErr, failErr)
}

func (s *EscalationService) updateSLAStatsOnce(
	ctx context.Context,
	slaConfigID uint,
	execution *slaExecutionContext,
	breachEventID string,
) error {
	reservation, err := s.reserveSLAOperation(ctx, "sla.stats.record", execution, "", "stats")
	if err != nil {
		return err
	}
	if reservation.Replayed {
		return nil
	}
	var config models.SLAConfig
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.SLAConfig{}).
			Where("id = ?", slaConfigID).
			Updates(map[string]any{
				"applied_count":   gorm.Expr("applied_count + ?", 1),
				"violation_count": gorm.Expr("violation_count + ?", 1),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.First(&config, slaConfigID).Error; err != nil {
			return err
		}
		complianceRate := 0.0
		if config.AppliedCount > 0 {
			complianceRate = float64(config.AppliedCount-config.ViolationCount) /
				float64(config.AppliedCount) * 100
		}
		if err := tx.Model(&config).Update("compliance_rate", complianceRate).Error; err != nil {
			return err
		}
		config.ComplianceRate = complianceRate
		receipt := OperationReceipt{
			OperationID:   newNativeID(),
			ResourceID:    strconv.FormatUint(uint64(config.ID), 10),
			EventID:       breachEventID,
			ChangedFields: []string{"applied_count", "violation_count", "compliance_rate"},
		}
		if err := s.agentNative.CompleteIdempotencyTxWithTTL(
			ctx,
			tx,
			reservation.Record.ID,
			http.StatusOK,
			receipt,
			receipt.ResourceID,
			breachEventID,
			slaCompletedRetentionTTL,
		); err != nil {
			return err
		}
		return s.agentNative.storeIdempotencySnapshotTx(
			ctx,
			tx,
			reservation.Record.ID,
			&config,
		)
	})
	if err != nil {
		return s.failSLAReservation(ctx, reservation.Record.ID, err)
	}
	return nil
}

func slaRuleIdentity(rule *models.EscalationRule) string {
	if rule == nil {
		return "rule-none"
	}
	return "rule-" + stableSLAHash(rule)[:24]
}

func stableSLAHash(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
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
			"name":            config.Name,
			"applied_count":   config.AppliedCount,
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

	for range ticker.C {
		ctx := context.Background()
		if err := s.CheckSLAViolations(ctx); err != nil {
			log.Printf("SLA检查失败: %v", err)
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
