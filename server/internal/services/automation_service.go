package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// AutomationService 自动化服务
type AutomationService struct {
	db     *gorm.DB
	native *AgentNativeService
}

// NewAutomationService 创建自动化服务实例
func NewAutomationService(db *gorm.DB) *AutomationService {
	return &AutomationService{
		db:     db,
		native: NewAgentNativeService(db),
	}
}

// NewAutomationServiceWithAgentNative creates the production automation
// engine. Rule actions reuse the same versioned, event-producing domain
// commands as REST, MCP and A2A instead of mutating ticket tables directly.
func NewAutomationServiceWithAgentNative(db *gorm.DB, native *AgentNativeService) *AutomationService {
	return &AutomationService{db: db, native: native}
}

const (
	automationActorID                = "automation-rule-engine"
	automationActionOperation        = "automation.rule.action"
	automationRuleExecutionOperation = "automation.rule.execution"
	automationTriggerOperation       = "automation.trigger.enqueue"
	automationTriggerEventType       = "io.chronodesk.automation.trigger.requested.v1"
	maxAutomationCausalDepth         = 16
	automationReservationTTL         = 2 * time.Minute
	automationFailureRetryDelay      = 2 * time.Second
	automationCompletedRetentionTTL  = 365 * 24 * time.Hour
)

var ErrInvalidWorkingHours = errors.New("invalid SLA working hours")

// AutomationRuleService 自动化规则相关方法

// CreateRule 创建自动化规则
func (s *AutomationService) CreateRule(ctx context.Context, req *models.AutomationRuleRequest, userID uint) (*models.AutomationRule, error) {
	rule := &models.AutomationRule{
		Name:         req.Name,
		Description:  req.Description,
		RuleType:     req.RuleType,
		IsActive:     false,
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
	if s == nil || s.native == nil {
		return errors.New("agent-native automation service is unavailable")
	}
	return s.enqueueNativeTrigger(ctx, triggerEvent, ticket)
}

// HasActiveRules reports whether a trigger has at least one rule that can
// consume it. Scheduled scans call this before loading tickets, and
// enqueueNativeTrigger repeats the guard so direct callers cannot create an
// event/Outbox storm when no rule is enabled.
func (s *AutomationService) HasActiveRules(ctx context.Context, triggerEvent string) (bool, error) {
	triggerEvent = strings.TrimSpace(triggerEvent)
	if triggerEvent == "" {
		return false, errors.New("automation trigger event is required")
	}
	var count int64
	if err := s.db.WithContext(ctx).
		Model(&models.AutomationRule{}).
		Where("is_active = ? AND trigger_event = ?", true, triggerEvent).
		Limit(1).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("check active automation rules: %w", err)
	}
	return count > 0, nil
}

func (s *AutomationService) enqueueNativeTrigger(
	ctx context.Context,
	triggerEvent string,
	ticket *models.Ticket,
) error {
	if ticket == nil || ticket.ID == 0 || ticket.Version == 0 {
		return errors.New("versioned ticket is required")
	}
	triggerEvent = strings.TrimSpace(triggerEvent)
	if triggerEvent == "" {
		return errors.New("automation trigger event is required")
	}
	hasActiveRules, err := s.HasActiveRules(ctx, triggerEvent)
	if err != nil {
		return err
	}
	if !hasActiveRules {
		return nil
	}
	now := time.Now().UTC()
	bucket := now.Format("200601021504")
	occurrenceKey := fmt.Sprintf(
		"scheduled:%s:%d:%d:%s",
		triggerEvent,
		ticket.ID,
		ticket.Version,
		bucket,
	)
	eventID := stableAutomationEventID(occurrenceKey)
	eventData := map[string]any{
		"ticket_id":      ticket.ID,
		"trigger_event":  triggerEvent,
		"bucket":         bucket,
		"occurrence_key": occurrenceKey,
	}
	requestBody, err := json.Marshal(eventData)
	if err != nil {
		return fmt.Errorf("encode automation trigger: %w", err)
	}
	actor := models.SystemActor("scheduler")
	reservation, err := s.native.ReserveIdempotency(
		ctx,
		actor,
		automationTriggerOperation,
		occurrenceKey,
		requestBody,
		automationReservationTTL,
	)
	if err != nil {
		return err
	}
	if reservation.Replayed {
		return nil
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		event, appendErr := s.native.AppendDomainEventTx(ctx, tx, DomainEventInput{
			ID:              eventID,
			Type:            automationTriggerEventType,
			Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
			Actor:           actor,
			ResourceVersion: ticket.Version,
			TraceID:         eventID,
			CorrelationID:   eventID,
			Data:            eventData,
		}, nil)
		if appendErr != nil {
			return appendErr
		}
		receipt := OperationReceipt{
			OperationID:     newNativeID(),
			ResourceID:      strconv.FormatUint(uint64(ticket.ID), 10),
			ResourceVersion: ticket.Version,
			EventID:         event.ID,
			ChangedFields:   []string{"automation_trigger"},
		}
		if err := s.native.CompleteIdempotencyTxWithTTL(
			ctx,
			tx,
			reservation.Record.ID,
			http.StatusAccepted,
			receipt,
			receipt.ResourceID,
			event.ID,
			automationCompletedRetentionTTL,
		); err != nil {
			return err
		}
		return s.native.storeIdempotencySnapshotTx(
			ctx,
			tx,
			reservation.Record.ID,
			ticket.ToResponse(),
		)
	})
	if err != nil {
		failErr := s.native.FailIdempotency(ctx, reservation.Record.ID, AgentNativeErrorCode(err))
		return errors.Join(err, failErr)
	}
	return nil
}

// ExecuteDomainEvent consumes a committed CloudEvent from the durable Outbox.
// Only ticket lifecycle events are mapped to legacy rule trigger names. Events
// emitted by this engine (or causally descended from one) are acknowledged
// without re-entering rules, which provides a hard loop boundary in addition
// to per-action idempotency.
func (s *AutomationService) ExecuteDomainEvent(ctx context.Context, event CloudEventEnvelope) error {
	if s == nil || s.native == nil {
		return errors.New("agent-native automation service is unavailable")
	}
	if !automationEventSupported(event.Type) {
		return nil
	}
	if strings.TrimSpace(event.ID) == "" {
		return errors.New("automation event id is required")
	}
	lineageRules, rootEventID, looped, err := s.automationLineage(ctx, event)
	if err != nil {
		return err
	}
	if looped {
		return nil
	}
	ticketID, err := automationTicketID(event)
	if err != nil {
		return err
	}
	var ticket models.Ticket
	if err := s.db.WithContext(ctx).First(&ticket, ticketID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return fmt.Errorf("load automation ticket: %w", err)
	}
	var executionErrors []error
	for _, triggerEvent := range automationTriggersForDomainEvent(event, ticket.Status) {
		if err := s.executeNativeRules(
			ctx,
			triggerEvent,
			event,
			&ticket,
			lineageRules,
			rootEventID,
		); err != nil {
			executionErrors = append(
				executionErrors,
				fmt.Errorf("automation trigger %s: %w", triggerEvent, err),
			)
		}
	}
	return errors.Join(executionErrors...)
}

func automationEventSupported(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "io.chronodesk.ticket.created.v1":
		return true
	case "io.chronodesk.ticket.updated.v1",
		"io.chronodesk.ticket.assigned.v1",
		"io.chronodesk.ticket.transitioned.v1",
		"io.chronodesk.ticket.escalated.v1",
		"io.chronodesk.ticket.comment.created.v1",
		"io.chronodesk.ticket.attachment.created.v1",
		"io.chronodesk.ticket.sla.breached.v1":
		return true
	case automationTriggerEventType:
		return true
	default:
		return false
	}
}

func automationTriggersForDomainEvent(
	event CloudEventEnvelope,
	currentStatus models.TicketStatus,
) []string {
	switch strings.TrimSpace(event.Type) {
	case "io.chronodesk.ticket.created.v1":
		return []string{"ticket.created"}
	case "io.chronodesk.ticket.assigned.v1":
		return []string{"ticket.assigned", "ticket.updated"}
	case "io.chronodesk.ticket.transitioned.v1":
		status := transitionStatusFromEvent(event)
		if status == "" {
			status = currentStatus
		}
		triggers := make([]string, 0, 3)
		switch status {
		case models.TicketStatusResolved:
			triggers = append(triggers, "ticket.resolved")
		case models.TicketStatusClosed:
			triggers = append(triggers, "ticket.closed")
		}
		if eventHasChangedField(event, "assigned_to_id") {
			triggers = append(triggers, "ticket.assigned")
		}
		return append(triggers, "ticket.updated")
	case "io.chronodesk.ticket.updated.v1",
		"io.chronodesk.ticket.escalated.v1",
		"io.chronodesk.ticket.comment.created.v1",
		"io.chronodesk.ticket.attachment.created.v1",
		"io.chronodesk.ticket.sla.breached.v1":
		return []string{"ticket.updated"}
	case automationTriggerEventType:
		var data struct {
			TriggerEvent string `json:"trigger_event"`
		}
		if len(event.Data) == 0 || json.Unmarshal(event.Data, &data) != nil {
			return nil
		}
		trigger := strings.TrimSpace(data.TriggerEvent)
		if trigger == "" || len(trigger) > 100 {
			return nil
		}
		return []string{trigger}
	default:
		return nil
	}
}

func transitionStatusFromEvent(event CloudEventEnvelope) models.TicketStatus {
	var data struct {
		Status    models.TicketStatus `json:"status"`
		NewStatus models.TicketStatus `json:"new_status"`
	}
	if len(event.Data) == 0 || json.Unmarshal(event.Data, &data) != nil {
		return ""
	}
	if data.NewStatus != "" {
		return data.NewStatus
	}
	return data.Status
}

func eventHasChangedField(event CloudEventEnvelope, field string) bool {
	var data struct {
		ChangedFields []string `json:"changed_fields"`
	}
	if len(event.Data) == 0 || json.Unmarshal(event.Data, &data) != nil {
		return false
	}
	for _, changed := range data.ChangedFields {
		if changed == field {
			return true
		}
	}
	return false
}

func automationTicketID(event CloudEventEnvelope) (uint, error) {
	var data struct {
		TicketID uint `json:"ticket_id"`
	}
	if len(event.Data) > 0 && json.Unmarshal(event.Data, &data) == nil && data.TicketID > 0 {
		return data.TicketID, nil
	}
	const prefix = "ticket/"
	if strings.HasPrefix(event.Subject, prefix) {
		value, err := safeconv.ParsePositiveUint(strings.TrimPrefix(event.Subject, prefix))
		if err == nil {
			return value, nil
		}
	}
	return 0, fmt.Errorf("event %s does not identify a ticket", event.ID)
}

func (s *AutomationService) automationLineage(
	ctx context.Context,
	event CloudEventEnvelope,
) (map[uint]struct{}, string, bool, error) {
	lineageRules := make(map[uint]struct{})
	seen := make(map[string]struct{})
	currentID := strings.TrimSpace(event.ID)
	rootEventID := currentID
	causationID := strings.TrimSpace(event.CausationID)
	data := event.Data
	for depth := 0; ; depth++ {
		if currentID != "" {
			if _, exists := seen[currentID]; exists {
				return lineageRules, rootEventID, true, nil
			}
			seen[currentID] = struct{}{}
		}
		if ruleID := automationRuleIDFromEventData(data); ruleID > 0 {
			lineageRules[ruleID] = struct{}{}
		}
		if causationID == "" {
			return lineageRules, rootEventID, false, nil
		}
		if depth+1 >= maxAutomationCausalDepth {
			return lineageRules, rootEventID, true, nil
		}
		var cause models.DomainEvent
		err := s.db.WithContext(ctx).
			Select("id", "causation_id", "data").
			First(&cause, "id = ?", causationID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			rootEventID = causationID
			return lineageRules, rootEventID, false, nil
		}
		if err != nil {
			return nil, "", false, fmt.Errorf("load automation causation %s: %w", causationID, err)
		}
		currentID = cause.ID
		rootEventID = cause.ID
		causationID = strings.TrimSpace(cause.CausationID)
		data = json.RawMessage(cause.Data)
	}
}

func automationRuleIDFromEventData(data []byte) uint {
	if len(data) == 0 {
		return 0
	}
	var value struct {
		RuleID uint `json:"automation_rule_id"`
	}
	if json.Unmarshal(data, &value) != nil {
		return 0
	}
	return value.RuleID
}

func (s *AutomationService) executeNativeRules(
	ctx context.Context,
	triggerEvent string,
	event CloudEventEnvelope,
	ticket *models.Ticket,
	lineageRules map[uint]struct{},
	rootEventID string,
) error {
	var rules []models.AutomationRule
	if err := s.db.WithContext(ctx).
		Where("is_active = ? AND trigger_event = ?", true, triggerEvent).
		Order("priority ASC, id ASC").
		Find(&rules).Error; err != nil {
		return fmt.Errorf("failed to get rules: %w", err)
	}
	frozenRules, err := s.loadIncompleteNativeRuleSnapshots(ctx, rootEventID, triggerEvent)
	if err != nil {
		return err
	}
	rules = mergeNativeAutomationRules(rules, frozenRules)

	var executionErrors []error
	for index := range rules {
		var current models.Ticket
		if err := s.db.WithContext(ctx).First(&current, ticket.ID).Error; err != nil {
			executionErrors = append(
				executionErrors,
				fmt.Errorf("rule %d reload ticket: %w", rules[index].ID, err),
			)
			continue
		}
		if err := s.executeNativeRule(
			ctx,
			&rules[index],
			event,
			&current,
			lineageRules,
			rootEventID,
		); err != nil {
			executionErrors = append(executionErrors, fmt.Errorf("rule %d: %w", rules[index].ID, err))
		}
	}
	return errors.Join(executionErrors...)
}

func (s *AutomationService) executeNativeRule(
	ctx context.Context,
	rule *models.AutomationRule,
	event CloudEventEnvelope,
	ticket *models.Ticket,
	lineageRules map[uint]struct{},
	rootEventID string,
) (returnErr error) {
	if _, recursivelyTriggered := lineageRules[rule.ID]; recursivelyTriggered {
		return nil
	}

	claim, err := s.reserveNativeRuleExecution(ctx, rootEventID, rule)
	if err != nil {
		return err
	}
	if claim.Replayed {
		return nil
	}
	rule = claim.Rule
	if rule == nil {
		return errors.New("automation rule execution snapshot is unavailable")
	}

	startTime := time.Now()
	var actionsExecuted []string
	claimCompleted := false
	defer func() {
		if claimCompleted || returnErr == nil {
			return
		}
		failure := returnErr
		failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.failNativeRuleExecution(
			failureCtx,
			claim,
			rule,
			ticket,
			event,
			rootEventID,
			actionsExecuted,
			time.Since(startTime),
			failure,
		); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()

	actions, err := rule.GetActions()
	if err != nil {
		return fmt.Errorf("failed to parse actions: %w", err)
	}
	checkpoint, err := s.nativeRuleActionCheckpoint(
		ctx,
		rootEventID,
		rule.ID,
		len(actions),
	)
	if err != nil {
		return err
	}
	matched := claim.Matched
	if !claim.ConditionEvaluated && checkpoint.Any {
		// An existing action reservation proves the frozen condition evaluated
		// true before this durable decision field was introduced or persisted.
		matched = true
		if err := s.persistNativeRuleConditionDecision(ctx, claim, matched); err != nil {
			return err
		}
	} else if !claim.ConditionEvaluated {
		conditions, conditionsErr := rule.GetConditions()
		if conditionsErr != nil {
			return fmt.Errorf("failed to parse conditions: %w", conditionsErr)
		}
		matched = s.evaluateConditions(conditions, ticket)
		if err := s.persistNativeRuleConditionDecision(ctx, claim, matched); err != nil {
			return err
		}
	}
	if matched {
		for actionIndex := range actions {
			if err := s.renewNativeRuleExecution(ctx, claim); err != nil {
				return err
			}
			if err := s.executeNativeAction(
				ctx,
				event,
				rootEventID,
				rule,
				actionIndex,
				&actions[actionIndex],
			); err != nil {
				return fmt.Errorf(
					"failed to execute action %s[%d]: %w",
					actions[actionIndex].Type,
					actionIndex,
					err,
				)
			}
			actionsExecuted = append(actionsExecuted, actions[actionIndex].Type)
		}
	}

	if err := s.completeNativeRuleExecution(
		ctx,
		claim,
		rule,
		ticket,
		event,
		rootEventID,
		matched,
		actionsExecuted,
		time.Since(startTime),
	); err != nil {
		return err
	}
	claimCompleted = true
	return nil
}

type automationRuleExecutionClaim struct {
	RecordID           string
	Token              string
	Replayed           bool
	Rule               *models.AutomationRule
	RootEventID        string
	ConditionEvaluated bool
	Matched            bool
}

type automationRuleSnapshot struct {
	ID           uint      `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	RuleType     string    `json:"rule_type"`
	IsActive     bool      `json:"is_active"`
	Priority     int       `json:"priority"`
	TriggerEvent string    `json:"trigger_event"`
	Conditions   string    `json:"conditions"`
	Actions      string    `json:"actions"`
	CreatedBy    uint      `json:"created_by"`
	UpdatedBy    *uint     `json:"updated_by,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type automationRuleExecutionSnapshot struct {
	RootEventID        string                 `json:"root_event_id"`
	Rule               automationRuleSnapshot `json:"rule"`
	ConditionEvaluated bool                   `json:"condition_evaluated"`
	Matched            bool                   `json:"matched"`
}

func newAutomationRuleSnapshot(rule *models.AutomationRule) (automationRuleSnapshot, error) {
	if rule == nil || rule.ID == 0 {
		return automationRuleSnapshot{}, errors.New("automation rule is required")
	}
	return automationRuleSnapshot{
		ID:           rule.ID,
		Name:         rule.Name,
		Description:  rule.Description,
		RuleType:     rule.RuleType,
		IsActive:     rule.IsActive,
		Priority:     rule.Priority,
		TriggerEvent: rule.TriggerEvent,
		Conditions:   rule.Conditions,
		Actions:      rule.Actions,
		CreatedBy:    rule.CreatedBy,
		UpdatedBy:    rule.UpdatedBy,
		UpdatedAt:    rule.UpdatedAt,
	}, nil
}

func (snapshot automationRuleSnapshot) rule() (*models.AutomationRule, error) {
	if snapshot.ID == 0 || strings.TrimSpace(snapshot.TriggerEvent) == "" {
		return nil, errors.New("automation rule snapshot is invalid")
	}
	return &models.AutomationRule{
		ID:           snapshot.ID,
		Name:         snapshot.Name,
		Description:  snapshot.Description,
		RuleType:     snapshot.RuleType,
		IsActive:     snapshot.IsActive,
		Priority:     snapshot.Priority,
		TriggerEvent: snapshot.TriggerEvent,
		Conditions:   snapshot.Conditions,
		Actions:      snapshot.Actions,
		CreatedBy:    snapshot.CreatedBy,
		UpdatedBy:    snapshot.UpdatedBy,
		UpdatedAt:    snapshot.UpdatedAt,
	}, nil
}

func decodeAutomationRuleExecutionSnapshot(
	data []byte,
) (automationRuleExecutionSnapshot, *models.AutomationRule, error) {
	var snapshot automationRuleExecutionSnapshot
	if len(data) == 0 {
		return snapshot, nil, errors.New("automation rule snapshot is missing")
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return snapshot, nil, fmt.Errorf("decode automation rule snapshot: %w", err)
	}
	rule, err := snapshot.Rule.rule()
	if err != nil {
		return snapshot, nil, err
	}
	if strings.TrimSpace(snapshot.RootEventID) == "" {
		return snapshot, nil, errors.New("automation rule snapshot root event is missing")
	}
	return snapshot, rule, nil
}

func decodeAutomationRuleSnapshot(data []byte) (*models.AutomationRule, error) {
	_, rule, err := decodeAutomationRuleExecutionSnapshot(data)
	return rule, err
}

func automationRootKey(rootEventID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(rootEventID)))
	return fmt.Sprintf("%x", digest[:])
}

func automationRuleExecutionKey(rootEventID string, ruleID uint) string {
	return fmt.Sprintf("%s:%d", automationRootKey(rootEventID), ruleID)
}

func automationActionKey(rootEventID string, ruleID uint, actionIndex int) string {
	return fmt.Sprintf("%s:%d:%d", automationRootKey(rootEventID), ruleID, actionIndex)
}

func (s *AutomationService) loadIncompleteNativeRuleSnapshots(
	ctx context.Context,
	rootEventID string,
	triggerEvent string,
) ([]models.AutomationRule, error) {
	rootEventID = strings.TrimSpace(rootEventID)
	keyPrefix := automationRootKey(rootEventID) + ":"
	var records []models.IdempotencyRecord
	if err := s.db.WithContext(ctx).
		Select("key", "resource_snapshot").
		Where(
			"actor_type = ? AND actor_id = ? AND operation = ? AND state IN ? AND key LIKE ?",
			models.ActorTypeSystem,
			automationActorID,
			automationRuleExecutionOperation,
			[]models.IdempotencyState{
				models.IdempotencyStateProcessing,
				models.IdempotencyStateFailed,
			},
			keyPrefix+"%",
		).
		Find(&records).Error; err != nil {
		return nil, fmt.Errorf("load incomplete automation rule executions: %w", err)
	}
	rules := make([]models.AutomationRule, 0, len(records))
	for index := range records {
		if !strings.HasPrefix(records[index].Key, keyPrefix) {
			continue
		}
		snapshot, rule, err := decodeAutomationRuleExecutionSnapshot(
			records[index].ResourceSnapshot,
		)
		if err != nil {
			return nil, fmt.Errorf("load frozen rule for %s: %w", records[index].Key, err)
		}
		if snapshot.RootEventID == rootEventID && rule.TriggerEvent == triggerEvent {
			rules = append(rules, *rule)
		}
	}
	return rules, nil
}

func mergeNativeAutomationRules(
	current []models.AutomationRule,
	frozen []models.AutomationRule,
) []models.AutomationRule {
	merged := make(map[uint]models.AutomationRule, len(current)+len(frozen))
	for index := range current {
		merged[current[index].ID] = current[index]
	}
	for index := range frozen {
		// An unfinished causal-root execution always uses its immutable first
		// claim snapshot, even if the live rule was edited or disabled.
		merged[frozen[index].ID] = frozen[index]
	}
	result := make([]models.AutomationRule, 0, len(merged))
	for _, rule := range merged {
		result = append(result, rule)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority == result[j].Priority {
			return result[i].ID < result[j].ID
		}
		return result[i].Priority < result[j].Priority
	})
	return result
}

func (s *AutomationService) reserveNativeRuleExecution(
	ctx context.Context,
	rootEventID string,
	rule *models.AutomationRule,
) (*automationRuleExecutionClaim, error) {
	rootEventID = strings.TrimSpace(rootEventID)
	if rootEventID == "" || rule == nil || rule.ID == 0 {
		return nil, errors.New("automation causal root and rule are required")
	}
	ruleSnapshot, err := newAutomationRuleSnapshot(rule)
	if err != nil {
		return nil, err
	}
	executionSnapshot := automationRuleExecutionSnapshot{
		RootEventID: rootEventID,
		Rule:        ruleSnapshot,
	}
	snapshotBody, err := json.Marshal(executionSnapshot)
	if err != nil {
		return nil, fmt.Errorf("encode automation rule snapshot: %w", err)
	}
	requestBody, err := json.Marshal(map[string]any{
		"root_event_id": rootEventID,
		"rule_id":       rule.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("encode automation rule execution claim: %w", err)
	}
	key := automationRuleExecutionKey(rootEventID, rule.ID)
	requestDigest := sha256.Sum256(requestBody)
	requestHash := fmt.Sprintf("%x", requestDigest[:])
	now := s.native.now()
	token := newNativeID()
	record := &models.IdempotencyRecord{
		ID:               newNativeID(),
		ActorType:        models.ActorTypeSystem,
		ActorID:          automationActorID,
		Operation:        automationRuleExecutionOperation,
		Key:              key,
		RequestHash:      requestHash,
		State:            models.IdempotencyStateProcessing,
		ResourceSnapshot: datatypes.JSON(snapshotBody),
		ResourceID:       token,
		ExpiresAt:        now.Add(automationReservationTTL),
	}
	if err := s.db.WithContext(ctx).Create(record).Error; err == nil {
		frozenRule, snapshotErr := executionSnapshot.Rule.rule()
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		return &automationRuleExecutionClaim{
			RecordID:    record.ID,
			Token:       token,
			Rule:        frozenRule,
			RootEventID: rootEventID,
		}, nil
	} else if !isUniqueConstraintError(err) {
		return nil, fmt.Errorf("reserve automation rule execution: %w", err)
	}

	var existing models.IdempotencyRecord
	if err := s.db.WithContext(ctx).
		Where(
			"actor_type = ? AND actor_id = ? AND operation = ? AND key = ?",
			models.ActorTypeSystem,
			automationActorID,
			automationRuleExecutionOperation,
			key,
		).
		First(&existing).Error; err != nil {
		return nil, fmt.Errorf("load automation rule execution: %w", err)
	}
	if existing.RequestHash != requestHash {
		return nil, ErrIdempotencyConflict
	}
	storedSnapshot, frozenRule, err := decodeAutomationRuleExecutionSnapshot(
		existing.ResourceSnapshot,
	)
	if err != nil {
		return nil, err
	}
	if storedSnapshot.RootEventID != rootEventID {
		return nil, ErrIdempotencyConflict
	}
	if existing.State == models.IdempotencyStateCompleted {
		return &automationRuleExecutionClaim{
			RecordID:           existing.ID,
			Replayed:           true,
			Rule:               frozenRule,
			RootEventID:        storedSnapshot.RootEventID,
			ConditionEvaluated: storedSnapshot.ConditionEvaluated,
			Matched:            storedSnapshot.Matched,
		}, nil
	}
	if existing.ExpiresAt.After(now) {
		return nil, ErrIdempotencyInProgress
	}

	token = newNativeID()
	result := s.db.WithContext(ctx).Model(&models.IdempotencyRecord{}).
		Where(
			"id = ? AND state IN ? AND expires_at <= ?",
			existing.ID,
			[]models.IdempotencyState{
				models.IdempotencyStateProcessing,
				models.IdempotencyStateFailed,
			},
			now,
		).
		Updates(map[string]any{
			"state":           models.IdempotencyStateProcessing,
			"response_code":   0,
			"response_body":   nil,
			"resource_id":     token,
			"event_id":        "",
			"last_error_code": "",
			"expires_at":      now.Add(automationReservationTTL),
			"completed_at":    nil,
			"updated_at":      now,
		})
	if result.Error != nil {
		return nil, fmt.Errorf("take over automation rule execution: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return nil, ErrIdempotencyInProgress
	}
	return &automationRuleExecutionClaim{
		RecordID:           existing.ID,
		Token:              token,
		Rule:               frozenRule,
		RootEventID:        storedSnapshot.RootEventID,
		ConditionEvaluated: storedSnapshot.ConditionEvaluated,
		Matched:            storedSnapshot.Matched,
	}, nil
}

func (s *AutomationService) persistNativeRuleConditionDecision(
	ctx context.Context,
	claim *automationRuleExecutionClaim,
	matched bool,
) error {
	if claim == nil || claim.RecordID == "" || claim.Token == "" ||
		claim.Rule == nil || strings.TrimSpace(claim.RootEventID) == "" {
		return errors.New("automation rule execution claim is required")
	}
	ruleSnapshot, err := newAutomationRuleSnapshot(claim.Rule)
	if err != nil {
		return err
	}
	snapshotBody, err := json.Marshal(automationRuleExecutionSnapshot{
		RootEventID:        claim.RootEventID,
		Rule:               ruleSnapshot,
		ConditionEvaluated: true,
		Matched:            matched,
	})
	if err != nil {
		return fmt.Errorf("encode automation condition decision: %w", err)
	}
	now := s.native.now()
	result := s.db.WithContext(ctx).Model(&models.IdempotencyRecord{}).
		Where(
			"id = ? AND state = ? AND resource_id = ? AND expires_at > ?",
			claim.RecordID,
			models.IdempotencyStateProcessing,
			claim.Token,
			now,
		).
		Updates(map[string]any{
			"resource_snapshot": datatypes.JSON(snapshotBody),
			"updated_at":        now,
		})
	if result.Error != nil {
		return fmt.Errorf("persist automation condition decision: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrIdempotencyInProgress
	}
	claim.ConditionEvaluated = true
	claim.Matched = matched
	return nil
}

func (s *AutomationService) renewNativeRuleExecution(
	ctx context.Context,
	claim *automationRuleExecutionClaim,
) error {
	if claim == nil || claim.RecordID == "" || claim.Token == "" {
		return errors.New("automation rule execution claim is required")
	}
	now := s.native.now()
	result := s.db.WithContext(ctx).Model(&models.IdempotencyRecord{}).
		Where(
			"id = ? AND state = ? AND resource_id = ? AND expires_at > ?",
			claim.RecordID,
			models.IdempotencyStateProcessing,
			claim.Token,
			now,
		).
		Updates(map[string]any{
			"expires_at": now.Add(automationReservationTTL),
			"updated_at": now,
		})
	if result.Error != nil {
		return fmt.Errorf("renew automation rule execution: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrIdempotencyInProgress
	}
	return nil
}

func (s *AutomationService) completeNativeRuleExecution(
	ctx context.Context,
	claim *automationRuleExecutionClaim,
	rule *models.AutomationRule,
	ticket *models.Ticket,
	event CloudEventEnvelope,
	rootEventID string,
	matched bool,
	actionsExecuted []string,
	execTime time.Duration,
) error {
	if claim == nil || claim.RecordID == "" || claim.Token == "" {
		return errors.New("automation rule execution claim is required")
	}
	responseBody, err := json.Marshal(map[string]any{
		"root_event_id":    rootEventID,
		"rule_id":          rule.ID,
		"matched":          matched,
		"actions_executed": actionsExecuted,
	})
	if err != nil {
		return fmt.Errorf("encode automation rule execution result: %w", err)
	}
	actionsJSON, err := json.Marshal(actionsExecuted)
	if err != nil {
		return fmt.Errorf("encode automation executed actions: %w", err)
	}
	changesJSON, err := json.Marshal(map[string]any{
		"root_event_id":  rootEventID,
		"event_id":       event.ID,
		"correlation_id": event.CorrelationID,
		"causation_id":   event.CausationID,
		"matched":        matched,
	})
	if err != nil {
		return fmt.Errorf("encode automation execution changes: %w", err)
	}

	now := s.native.now()
	execMillis := execTime.Milliseconds()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.IdempotencyRecord{}).
			Where(
				"id = ? AND state = ? AND resource_id = ? AND expires_at > ?",
				claim.RecordID,
				models.IdempotencyStateProcessing,
				claim.Token,
				now,
			).
			Updates(map[string]any{
				"state":           models.IdempotencyStateCompleted,
				"response_code":   http.StatusOK,
				"response_body":   datatypes.JSON(responseBody),
				"resource_id":     strconv.FormatUint(uint64(rule.ID), 10),
				"event_id":        event.ID,
				"last_error_code": "",
				"expires_at":      now.Add(automationCompletedRetentionTTL),
				"completed_at":    now,
				"updated_at":      now,
			})
		if result.Error != nil {
			return fmt.Errorf("complete automation rule execution: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrIdempotencyInProgress
		}

		ruleExists, err := updateAutomationRuleAttemptStatisticsTx(
			tx,
			rule.ID,
			true,
			execMillis,
			now,
		)
		if err != nil {
			return err
		}
		if !ruleExists {
			// A deleted rule still finishes from its frozen snapshot. Its final
			// outcome remains durable in the idempotency record.
			return nil
		}

		entry := &models.AutomationLog{
			RuleID:          rule.ID,
			TicketID:        ticket.ID,
			TriggerEvent:    rule.TriggerEvent,
			ExecutedAt:      now,
			Success:         true,
			ExecutionTime:   execMillis,
			ActionsExecuted: string(actionsJSON),
			Changes:         string(changesJSON),
		}
		if err := tx.Create(entry).Error; err != nil {
			return fmt.Errorf("create automation execution log: %w", err)
		}
		return nil
	})
}

func (s *AutomationService) failNativeRuleExecution(
	ctx context.Context,
	claim *automationRuleExecutionClaim,
	rule *models.AutomationRule,
	ticket *models.Ticket,
	event CloudEventEnvelope,
	rootEventID string,
	actionsExecuted []string,
	execTime time.Duration,
	failure error,
) error {
	if claim == nil || claim.RecordID == "" || claim.Token == "" ||
		rule == nil || ticket == nil || failure == nil {
		return nil
	}
	errorMessage := failure.Error()
	if len(errorMessage) > 4000 {
		errorMessage = errorMessage[:4000]
	}
	actionsJSON, err := json.Marshal(actionsExecuted)
	if err != nil {
		return fmt.Errorf("encode failed automation actions: %w", err)
	}
	changesJSON, err := json.Marshal(map[string]any{
		"root_event_id":  rootEventID,
		"event_id":       event.ID,
		"correlation_id": event.CorrelationID,
		"causation_id":   event.CausationID,
	})
	if err != nil {
		return fmt.Errorf("encode failed automation changes: %w", err)
	}
	responseBody, err := json.Marshal(map[string]any{
		"root_event_id":    rootEventID,
		"rule_id":          rule.ID,
		"actions_executed": actionsExecuted,
		"error_code":       AgentNativeErrorCode(failure),
		"error":            errorMessage,
	})
	if err != nil {
		return fmt.Errorf("encode failed automation result: %w", err)
	}
	now := s.native.now()
	execMillis := execTime.Milliseconds()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.IdempotencyRecord{}).
			Where(
				"id = ? AND state = ? AND resource_id = ? AND expires_at > ?",
				claim.RecordID,
				models.IdempotencyStateProcessing,
				claim.Token,
				now,
			).
			Updates(map[string]any{
				"state":           models.IdempotencyStateFailed,
				"response_code":   http.StatusInternalServerError,
				"response_body":   datatypes.JSON(responseBody),
				"last_error_code": AgentNativeErrorCode(failure),
				"expires_at":      now.Add(automationFailureRetryDelay),
				"completed_at":    now,
				"updated_at":      now,
			})
		if result.Error != nil {
			return fmt.Errorf("fail automation rule execution: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			// A stale owner or concurrent loser never owns the attempt outcome.
			return nil
		}

		ruleExists, err := updateAutomationRuleAttemptStatisticsTx(
			tx,
			rule.ID,
			false,
			execMillis,
			now,
		)
		if err != nil {
			return err
		}
		if !ruleExists {
			return nil
		}
		entry := &models.AutomationLog{
			RuleID:          rule.ID,
			TicketID:        ticket.ID,
			TriggerEvent:    rule.TriggerEvent,
			ExecutedAt:      now,
			Success:         false,
			ErrorMessage:    errorMessage,
			ExecutionTime:   execMillis,
			ActionsExecuted: string(actionsJSON),
			Changes:         string(changesJSON),
		}
		if err := tx.Create(entry).Error; err != nil {
			return fmt.Errorf("create failed automation execution log: %w", err)
		}
		return nil
	})
}

func updateAutomationRuleAttemptStatisticsTx(
	tx *gorm.DB,
	ruleID uint,
	success bool,
	execMillis int64,
	now time.Time,
) (bool, error) {
	updates := map[string]any{
		"execution_count":  gorm.Expr("execution_count + 1"),
		"last_executed_at": now,
		"average_exec_time": gorm.Expr(
			"CASE WHEN execution_count = 0 THEN ? ELSE ((average_exec_time * execution_count) + ?) / (execution_count + 1) END",
			execMillis,
			execMillis,
		),
	}
	if success {
		updates["success_count"] = gorm.Expr("success_count + 1")
	} else {
		updates["failure_count"] = gorm.Expr("failure_count + 1")
	}
	result := tx.Model(&models.AutomationRule{}).
		Where("id = ?", ruleID).
		Updates(updates)
	if result.Error != nil {
		return false, fmt.Errorf("update automation rule statistics: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

type automationRuleCheckpoint struct {
	Any bool
}

func (s *AutomationService) nativeRuleActionCheckpoint(
	ctx context.Context,
	eventID string,
	ruleID uint,
	actionCount int,
) (automationRuleCheckpoint, error) {
	if actionCount <= 0 {
		return automationRuleCheckpoint{}, nil
	}
	keys := make([]string, 0, actionCount)
	for actionIndex := 0; actionIndex < actionCount; actionIndex++ {
		keys = append(keys, automationActionKey(eventID, ruleID, actionIndex))
	}
	var records []models.IdempotencyRecord
	if err := s.db.WithContext(ctx).
		Select("key", "state").
		Where(
			"actor_type = ? AND actor_id = ? AND operation = ? AND key IN ?",
			models.ActorTypeSystem,
			automationActorID,
			automationActionOperation,
			keys,
		).
		Find(&records).Error; err != nil {
		return automationRuleCheckpoint{}, fmt.Errorf(
			"load automation rule execution checkpoint: %w",
			err,
		)
	}
	return automationRuleCheckpoint{Any: len(records) > 0}, nil
}

func (s *AutomationService) executeNativeAction(
	ctx context.Context,
	event CloudEventEnvelope,
	rootEventID string,
	rule *models.AutomationRule,
	actionIndex int,
	action *models.RuleAction,
) error {
	requestBody, err := json.Marshal(map[string]any{
		"root_event_id": rootEventID,
		"rule_id":       rule.ID,
		"action_index":  actionIndex,
		"action":        action,
	})
	if err != nil {
		return fmt.Errorf("encode automation action: %w", err)
	}
	idempotencyKey := automationActionKey(rootEventID, rule.ID, actionIndex)
	reservation, err := s.native.ReserveIdempotency(
		ctx,
		models.SystemActor(automationActorID),
		automationActionOperation,
		idempotencyKey,
		requestBody,
		automationReservationTTL,
	)
	if err != nil {
		return err
	}
	if reservation.Replayed {
		return nil
	}
	if err := s.runNativeAction(ctx, event, rule, actionIndex, action, reservation.Record.ID); err != nil {
		failErr := s.native.FailIdempotency(ctx, reservation.Record.ID, AgentNativeErrorCode(err))
		return errors.Join(err, failErr)
	}
	return nil
}

func (s *AutomationService) runNativeAction(
	ctx context.Context,
	event CloudEventEnvelope,
	rule *models.AutomationRule,
	actionIndex int,
	action *models.RuleAction,
	idempotencyRecordID string,
) error {
	var ticket models.Ticket
	ticketID, err := automationTicketID(event)
	if err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).First(&ticket, ticketID).Error; err != nil {
		return fmt.Errorf("reload automation ticket: %w", err)
	}
	correlationID := strings.TrimSpace(event.CorrelationID)
	if correlationID == "" {
		correlationID = event.ID
	}
	eventData := func(changedFields ...string) map[string]any {
		return map[string]any{
			"ticket_id":                ticket.ID,
			"changed_fields":           changedFields,
			"automation_rule_id":       rule.ID,
			"automation_action_index":  actionIndex,
			"automation_trigger_event": event.ID,
		}
	}
	update := func(
		changes map[string]any,
		eventType string,
		requiredScope string,
		commandAction string,
		changedFields ...string,
	) error {
		data := eventData(changedFields...)
		if status, ok := changes["status"]; ok {
			data["status"] = status
			data["new_status"] = status
		}
		_, updateErr := s.native.UpdateTicketVersion(ctx, VersionedTicketUpdateInput{
			TicketID:                 ticket.ID,
			ExpectedVersion:          ticket.Version,
			Actor:                    models.SystemActor(automationActorID),
			SourceProtocol:           "automation",
			Changes:                  changes,
			EventType:                eventType,
			EventData:                data,
			TraceID:                  event.TraceID,
			CorrelationID:            correlationID,
			CausationID:              event.ID,
			RequiredScope:            requiredScope,
			Action:                   commandAction,
			IdempotencyRecordID:      idempotencyRecordID,
			IdempotencyCompletionTTL: automationCompletedRetentionTTL,
		})
		return updateErr
	}

	switch action.Type {
	case "assign":
		userID, err := automationActionUserID(action, "user_id")
		if err != nil {
			return err
		}
		if err := s.requireAutomationUser(ctx, userID); err != nil {
			return err
		}
		if ticket.AssignedToID != nil &&
			*ticket.AssignedToID == userID &&
			ticket.AssignedToActorType == models.ActorTypeHuman &&
			ticket.AssignedToActorID == strconv.FormatUint(uint64(userID), 10) {
			return s.completeAutomationNoop(ctx, idempotencyRecordID, &ticket, event.ID)
		}
		return update(map[string]any{
			"assigned_to_id":                   userID,
			"assigned_to_actor_type":           models.ActorTypeHuman,
			"assigned_to_actor_id":             strconv.FormatUint(uint64(userID), 10),
			"assigned_to_service_principal_id": nil,
		}, "io.chronodesk.ticket.assigned.v1", models.ScopeTicketsAssign, "ticket.assign",
			"assigned_to_id", "assigned_to_actor_type", "assigned_to_actor_id")
	case "set_priority":
		raw, ok := action.Params["priority"]
		if !ok {
			return errors.New("priority parameter required")
		}
		priority := models.TicketPriority(fmt.Sprint(raw))
		if !priority.IsValid() {
			return fmt.Errorf("invalid priority: %s", priority)
		}
		if ticket.Priority == priority {
			return s.completeAutomationNoop(ctx, idempotencyRecordID, &ticket, event.ID)
		}
		return update(
			map[string]any{"priority": priority},
			"io.chronodesk.ticket.updated.v1",
			models.ScopeTicketsUpdate,
			"ticket.update",
			"priority",
		)
	case "set_status":
		raw, ok := action.Params["status"]
		if !ok {
			return errors.New("status parameter required")
		}
		status := models.TicketStatus(fmt.Sprint(raw))
		if !status.IsValid() {
			return fmt.Errorf("invalid status: %s", status)
		}
		if ticket.Status == status {
			return s.completeAutomationNoop(ctx, idempotencyRecordID, &ticket, event.ID)
		}
		return update(
			map[string]any{"status": status},
			"io.chronodesk.ticket.transitioned.v1",
			models.ScopeTicketsTransition,
			"ticket.transition",
			"status",
		)
	case "add_comment":
		raw, ok := action.Params["content"]
		if !ok || strings.TrimSpace(fmt.Sprint(raw)) == "" {
			return errors.New("content parameter required")
		}
		compatibilityUserID := uint(0)
		if s.native.systemCompatibilityUserID == 0 {
			// Old constructors predate a configured system user. ActorRef remains
			// authoritative; the creator is only a non-null legacy FK fallback.
			compatibilityUserID = ticket.CreatedByID
		}
		_, err := s.native.CreateComment(ctx, NativeCommentInput{
			TicketID:                 ticket.ID,
			ExpectedVersion:          ticket.Version,
			Actor:                    models.SystemActor(automationActorID),
			CompatibilityUserID:      compatibilityUserID,
			SourceProtocol:           "automation",
			Content:                  fmt.Sprint(raw),
			ContentType:              "text",
			Type:                     models.CommentTypeSystem,
			Reason:                   fmt.Sprintf("automation rule %d", rule.ID),
			TraceID:                  event.TraceID,
			CorrelationID:            correlationID,
			CausationID:              event.ID,
			AutomationRuleID:         rule.ID,
			AutomationActionIndex:    &actionIndex,
			IdempotencyRecordID:      idempotencyRecordID,
			IdempotencyCompletionTTL: automationCompletedRetentionTTL,
		})
		return err
	case "escalate":
		managerID, err := automationActionUserID(action, "manager_id")
		if err != nil {
			return err
		}
		if err := s.requireAutomationUser(ctx, managerID); err != nil {
			return err
		}
		if ticket.AssignedToID != nil &&
			*ticket.AssignedToID == managerID &&
			ticket.Priority == models.TicketPriorityHigh &&
			ticket.IsEscalated {
			return s.completeAutomationNoop(ctx, idempotencyRecordID, &ticket, event.ID)
		}
		return update(map[string]any{
			"assigned_to_id":                   managerID,
			"assigned_to_actor_type":           models.ActorTypeHuman,
			"assigned_to_actor_id":             strconv.FormatUint(uint64(managerID), 10),
			"assigned_to_service_principal_id": nil,
			"priority":                         models.TicketPriorityHigh,
			"is_escalated":                     true,
		}, "io.chronodesk.ticket.escalated.v1", "", "automation.ticket.escalate",
			"assigned_to_id", "priority", "is_escalated")
	case "notify":
		return s.recordAutomationNotification(
			ctx,
			idempotencyRecordID,
			event,
			rule,
			actionIndex,
			action,
			&ticket,
		)
	default:
		return fmt.Errorf("unknown action type: %s", action.Type)
	}
}

func automationActionUserID(action *models.RuleAction, name string) (uint, error) {
	raw, ok := action.Params[name]
	if !ok {
		return 0, fmt.Errorf("%s parameter required", name)
	}
	service := &AutomationService{}
	value, err := service.toUint(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	return value, nil
}

func (s *AutomationService) requireAutomationUser(ctx context.Context, userID uint) error {
	var count int64
	if err := s.db.WithContext(ctx).Model(&models.User{}).
		Where("id = ?", userID).
		Count(&count).Error; err != nil {
		return fmt.Errorf("validate automation user %d: %w", userID, err)
	}
	if count != 1 {
		return fmt.Errorf("user %d not found", userID)
	}
	return nil
}

func (s *AutomationService) completeAutomationNoop(
	ctx context.Context,
	idempotencyRecordID string,
	ticket *models.Ticket,
	causationEventID string,
) error {
	receipt := OperationReceipt{
		OperationID:     newNativeID(),
		ResourceID:      strconv.FormatUint(uint64(ticket.ID), 10),
		ResourceVersion: ticket.Version,
		EventID:         causationEventID,
		ChangedFields:   []string{},
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.native.CompleteIdempotencyTxWithTTL(
			ctx,
			tx,
			idempotencyRecordID,
			http.StatusOK,
			receipt,
			receipt.ResourceID,
			receipt.EventID,
			automationCompletedRetentionTTL,
		); err != nil {
			return err
		}
		return s.native.storeIdempotencySnapshotTx(
			ctx,
			tx,
			idempotencyRecordID,
			ticket.ToResponse(),
		)
	})
}

func (s *AutomationService) recordAutomationNotification(
	ctx context.Context,
	idempotencyRecordID string,
	cause CloudEventEnvelope,
	rule *models.AutomationRule,
	actionIndex int,
	action *models.RuleAction,
	ticket *models.Ticket,
) error {
	correlationID := cause.CorrelationID
	if strings.TrimSpace(correlationID) == "" {
		correlationID = cause.ID
	}
	notification, err := automationNotificationPayload(action, rule, ticket)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		event, err := s.native.AppendDomainEventTx(ctx, tx, DomainEventInput{
			Type:            "io.chronodesk.automation.notification.requested.v1",
			Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
			Actor:           models.SystemActor(automationActorID),
			ResourceVersion: ticket.Version,
			TraceID:         cause.TraceID,
			CorrelationID:   correlationID,
			CausationID:     cause.ID,
			Data: map[string]any{
				"ticket_id":               ticket.ID,
				"automation_rule_id":      rule.ID,
				"automation_action_index": actionIndex,
				"notification":            notification,
				"content_untrusted":       true,
			},
		}, nil)
		if err != nil {
			return err
		}
		receipt := OperationReceipt{
			OperationID:     newNativeID(),
			ResourceID:      strconv.FormatUint(uint64(ticket.ID), 10),
			ResourceVersion: ticket.Version,
			EventID:         event.ID,
			ChangedFields:   []string{"notification_request"},
		}
		if err := s.native.CompleteIdempotencyTxWithTTL(
			ctx,
			tx,
			idempotencyRecordID,
			http.StatusAccepted,
			receipt,
			receipt.ResourceID,
			event.ID,
			automationCompletedRetentionTTL,
		); err != nil {
			return err
		}
		return s.native.storeIdempotencySnapshotTx(
			ctx,
			tx,
			idempotencyRecordID,
			ticket.ToResponse(),
		)
	})
}

func automationNotificationPayload(
	action *models.RuleAction,
	rule *models.AutomationRule,
	ticket *models.Ticket,
) (map[string]any, error) {
	if action == nil || rule == nil || ticket == nil {
		return nil, errors.New("automation notification context is incomplete")
	}
	channel := automationStringParam(action.Params, "channel")
	if channel == "" {
		channel = string(models.NotificationChannelWebhook)
	}
	if channel != string(models.NotificationChannelWebhook) {
		return nil, fmt.Errorf(
			"automation notification channel %q is unsupported; configure webhook",
			channel,
		)
	}
	if _, present := action.Params["recipient_ids"]; present {
		return nil, errors.New("recipient_ids are not supported for webhook automation notifications")
	}
	if _, present := action.Params["template_id"]; present {
		return nil, errors.New("template_id is not supported for webhook automation notifications")
	}
	title := automationStringParam(action.Params, "title")
	if title == "" {
		title = fmt.Sprintf("Automation rule %s matched", rule.Name)
	}
	content := automationStringParam(action.Params, "content")
	if content == "" {
		content = fmt.Sprintf("Ticket %s requires attention.", ticket.TicketNumber)
	}
	priority := automationStringParam(action.Params, "priority")
	if priority == "" {
		priority = string(models.NotificationPriorityNormal)
	}
	switch models.NotificationPriority(priority) {
	case models.NotificationPriorityLow,
		models.NotificationPriorityNormal,
		models.NotificationPriorityHigh,
		models.NotificationPriorityUrgent:
	default:
		return nil, fmt.Errorf("invalid automation notification priority %q", priority)
	}
	return map[string]any{
		"channel":  channel,
		"title":    truncateText(title, 255),
		"content":  truncateText(content, 4000),
		"priority": priority,
	}, nil
}

func automationStringParam(params map[string]interface{}, key string) string {
	value, ok := params[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
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

// executeAssignAction 执行分配动作
func (s *AutomationService) executeAssignAction(ctx context.Context, action *models.RuleAction, ticket *models.Ticket) error {
	return s.executeCompatibilityAction(ctx, action, ticket)
}

// executeSetPriorityAction 执行设置优先级动作
func (s *AutomationService) executeSetPriorityAction(ctx context.Context, action *models.RuleAction, ticket *models.Ticket) error {
	return s.executeCompatibilityAction(ctx, action, ticket)
}

// executeSetStatusAction 执行设置状态动作
func (s *AutomationService) executeSetStatusAction(ctx context.Context, action *models.RuleAction, ticket *models.Ticket) error {
	return s.executeCompatibilityAction(ctx, action, ticket)
}

// executeAddCommentAction 执行添加评论动作
func (s *AutomationService) executeAddCommentAction(ctx context.Context, action *models.RuleAction, ticket *models.Ticket) error {
	return s.executeCompatibilityAction(ctx, action, ticket)
}

// executeNotifyAction 执行通知动作
func (s *AutomationService) executeNotifyAction(ctx context.Context, action *models.RuleAction, ticket *models.Ticket) error {
	return s.executeCompatibilityAction(ctx, action, ticket)
}

// executeEscalateAction 执行升级动作
func (s *AutomationService) executeEscalateAction(ctx context.Context, action *models.RuleAction, ticket *models.Ticket) error {
	return s.executeCompatibilityAction(ctx, action, ticket)
}

// executeCompatibilityAction keeps the historical private action helpers
// usable by old callers while routing them through the same native command
// path. The synthetic event identity is deterministic for the ticket version
// and action payload, so a retry cannot duplicate the action.
func (s *AutomationService) executeCompatibilityAction(
	ctx context.Context,
	action *models.RuleAction,
	ticket *models.Ticket,
) error {
	if s == nil || s.native == nil {
		return errors.New("agent-native automation service is unavailable")
	}
	if ticket == nil || ticket.ID == 0 || ticket.Version == 0 {
		return errors.New("versioned ticket is required")
	}
	encoded, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("encode compatibility automation action: %w", err)
	}
	digest := sha256.Sum256(encoded)
	eventID := fmt.Sprintf(
		"compat:%d:%d:%x",
		ticket.ID,
		ticket.Version,
		digest[:8],
	)
	return s.executeNativeAction(
		ctx,
		CloudEventEnvelope{
			SpecVersion:     "1.0",
			ID:              eventID,
			Type:            "io.chronodesk.automation.compatibility.v1",
			Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
			Time:            time.Now().UTC(),
			CorrelationID:   eventID,
			ActorType:       models.ActorTypeSystem,
			ActorID:         "compatibility",
			ResourceVersion: ticket.Version,
		},
		eventID,
		&models.AutomationRule{},
		0,
		action,
	)
}

// toUint 转换为uint
func (s *AutomationService) toUint(value interface{}) (uint, error) {
	switch v := value.(type) {
	case float64:
		if v <= 0 || math.Trunc(v) != v || v >= math.Ldexp(1, strconv.IntSize) {
			return 0, fmt.Errorf("value must be a positive integer")
		}
		return safeconv.PositiveUint(uint64(v))
	case int:
		if v <= 0 {
			return 0, fmt.Errorf("value must be positive")
		}
		return uint(v), nil
	case int64:
		if v <= 0 {
			return 0, fmt.Errorf("value must be a positive platform-sized integer")
		}
		return safeconv.PositiveUint(uint64(v))
	case uint64:
		return safeconv.PositiveUint(v)
	case uint:
		if v == 0 {
			return 0, fmt.Errorf("value must be positive")
		}
		return v, nil
	case string:
		i, err := safeconv.ParsePositiveUint(v)
		if err != nil {
			return 0, fmt.Errorf("value must be a positive integer")
		}
		return i, nil
	default:
		return 0, fmt.Errorf("cannot convert to uint")
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
		if _, err := prepareWorkingSchedule(req.WorkingHours, config.ExcludeWeekends, config.ExcludeHolidays, time.UTC); err != nil {
			return nil, err
		}
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
	responseDeadline, err = s.addWorkingTime(startTime, time.Duration(config.ResponseTime)*time.Minute, workingHours, config.ExcludeWeekends, config.ExcludeHolidays)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	// 计算解决截止时间
	resolutionDeadline, err = s.addWorkingTime(startTime, time.Duration(config.ResolutionTime)*time.Minute, workingHours, config.ExcludeWeekends, config.ExcludeHolidays)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	return responseDeadline, resolutionDeadline, nil
}

// addWorkingTime 添加工作时间（考虑工作时间、周末、节假日）
func (s *AutomationService) addWorkingTime(
	startTime time.Time,
	duration time.Duration,
	workingHours *models.WorkingHours,
	excludeWeekends, excludeHolidays bool,
) (time.Time, error) {
	if duration < 0 {
		return time.Time{}, fmt.Errorf("%w: duration must not be negative", ErrInvalidWorkingHours)
	}
	if duration == 0 {
		return startTime, nil
	}

	schedule, err := prepareWorkingSchedule(
		workingHours,
		excludeWeekends,
		excludeHolidays,
		startTime.Location(),
	)
	if err != nil {
		return time.Time{}, err
	}

	current := startTime.In(schedule.location)
	remaining := duration
	// The validation guarantees at least one weekly interval. This upper bound
	// is a final guard against corrupt calendars causing an infinite scheduler.
	const maximumCalendarDays = 366 * 100
	for dayCount := 0; dayCount < maximumCalendarDays; dayCount++ {
		dayStart := time.Date(current.Year(), current.Month(), current.Day(), 0, 0, 0, 0, schedule.location)
		if schedule.isExcluded(dayStart) {
			current = dayStart.AddDate(0, 0, 1)
			continue
		}

		interval, ok := schedule.intervals[current.Weekday()]
		if !ok {
			current = dayStart.AddDate(0, 0, 1)
			continue
		}
		windowStart := dayStart.Add(interval.start)
		windowEnd := dayStart.Add(interval.end)
		if current.Before(windowStart) {
			current = windowStart
		}
		if !current.Before(windowEnd) {
			current = dayStart.AddDate(0, 0, 1)
			continue
		}

		available := windowEnd.Sub(current)
		if remaining <= available {
			return current.Add(remaining).In(startTime.Location()), nil
		}
		remaining -= available
		current = dayStart.AddDate(0, 0, 1)
	}

	return time.Time{}, fmt.Errorf("%w: deadline exceeds supported calendar range", ErrInvalidWorkingHours)
}

type workingInterval struct {
	start time.Duration
	end   time.Duration
}

type workingSchedule struct {
	location        *time.Location
	intervals       map[time.Weekday]workingInterval
	holidays        map[string]struct{}
	excludeWeekends bool
	excludeHolidays bool
}

func prepareWorkingSchedule(
	hours *models.WorkingHours,
	excludeWeekends, excludeHolidays bool,
	fallbackLocation *time.Location,
) (*workingSchedule, error) {
	if hours == nil {
		return nil, fmt.Errorf("%w: configuration is required", ErrInvalidWorkingHours)
	}
	location := fallbackLocation
	if location == nil {
		location = time.UTC
	}
	if strings.TrimSpace(hours.Timezone) != "" {
		var err error
		location, err = time.LoadLocation(strings.TrimSpace(hours.Timezone))
		if err != nil {
			return nil, fmt.Errorf("%w: unknown timezone %q", ErrInvalidWorkingHours, hours.Timezone)
		}
	}

	schedule := &workingSchedule{
		location:        location,
		intervals:       make(map[time.Weekday]workingInterval),
		holidays:        make(map[string]struct{}),
		excludeWeekends: excludeWeekends,
		excludeHolidays: excludeHolidays,
	}
	for day := time.Sunday; day <= time.Saturday; day++ {
		timeRange := hours.RangeFor(day)
		if strings.TrimSpace(timeRange.Start) == "" && strings.TrimSpace(timeRange.End) == "" {
			continue
		}
		start, err := parseClockOffset(timeRange.Start)
		if err != nil {
			return nil, fmt.Errorf("%w: %s start: %v", ErrInvalidWorkingHours, day, err)
		}
		end, err := parseClockOffset(timeRange.End)
		if err != nil {
			return nil, fmt.Errorf("%w: %s end: %v", ErrInvalidWorkingHours, day, err)
		}
		if start >= end {
			return nil, fmt.Errorf("%w: %s start must be before end", ErrInvalidWorkingHours, day)
		}
		if excludeWeekends && (day == time.Saturday || day == time.Sunday) {
			continue
		}
		schedule.intervals[day] = workingInterval{start: start, end: end}
	}
	if len(schedule.intervals) == 0 {
		return nil, fmt.Errorf("%w: no effective working day configured", ErrInvalidWorkingHours)
	}

	for _, holiday := range hours.Holidays {
		normalized := strings.TrimSpace(holiday)
		if normalized == "" {
			continue
		}
		if _, err := time.ParseInLocation("2006-01-02", normalized, location); err != nil {
			return nil, fmt.Errorf("%w: holiday %q must use YYYY-MM-DD", ErrInvalidWorkingHours, holiday)
		}
		schedule.holidays[normalized] = struct{}{}
	}
	return schedule, nil
}

func parseClockOffset(value string) (time.Duration, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("must use HH:MM")
	}
	return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute, nil
}

func (schedule *workingSchedule) isExcluded(day time.Time) bool {
	if schedule.excludeWeekends && (day.Weekday() == time.Saturday || day.Weekday() == time.Sunday) {
		return true
	}
	if !schedule.excludeHolidays {
		return false
	}
	_, excluded := schedule.holidays[day.Format("2006-01-02")]
	return excluded
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
func (s *AutomationService) BatchUpdateTickets(
	ctx context.Context,
	ticketIDs []uint,
	updates map[string]interface{},
	actors ...models.ActorRef,
) error {
	if len(ticketIDs) == 0 {
		return fmt.Errorf("no tickets specified")
	}
	if s == nil || s.native == nil {
		return errors.New("agent-native automation service is unavailable")
	}

	// 验证更新字段
	allowedFields := map[string]bool{
		"status":         true,
		"priority":       true,
		"assigned_to_id": true,
		"type":           true,
	}

	validUpdates := make(map[string]interface{})
	for key, value := range updates {
		if !allowedFields[key] {
			return fmt.Errorf("field %s is not allowed for batch update", key)
		}
		switch key {
		case "status":
			if !models.TicketStatus(fmt.Sprintf("%v", value)).IsValid() {
				return fmt.Errorf("invalid status: %v", value)
			}
		case "priority":
			if !models.TicketPriority(fmt.Sprintf("%v", value)).IsValid() {
				return fmt.Errorf("invalid priority: %v", value)
			}
		case "type":
			if !models.TicketType(fmt.Sprintf("%v", value)).IsValid() {
				return fmt.Errorf("invalid ticket type: %v", value)
			}
		case "assigned_to_id":
			if value == nil {
				validUpdates["assigned_to_actor_type"] = nil
				validUpdates["assigned_to_actor_id"] = nil
				validUpdates["assigned_to_service_principal_id"] = nil
				break
			}
			userID, err := s.toUint(value)
			if err != nil {
				return fmt.Errorf("invalid assigned_to_id: %w", err)
			}
			if err := s.requireAutomationUser(ctx, userID); err != nil {
				return err
			}
			value = userID
			validUpdates["assigned_to_actor_type"] = models.ActorTypeHuman
			validUpdates["assigned_to_actor_id"] = strconv.FormatUint(uint64(userID), 10)
			validUpdates["assigned_to_service_principal_id"] = nil
		}
		validUpdates[key] = value
	}
	if len(validUpdates) == 0 {
		return errors.New("no valid ticket changes specified")
	}
	actor := models.SystemActor(automationActorID)
	if len(actors) > 0 {
		if err := actors[0].Validate(); err != nil {
			return fmt.Errorf("invalid batch actor: %w", err)
		}
		actor = actors[0]
	}
	changeGroups := splitAutomationChangeGroups(validUpdates)

	var updateErrors []error
	for _, ticketID := range ticketIDs {
		if ticketID == 0 {
			updateErrors = append(updateErrors, errors.New("ticket id must be positive"))
			continue
		}
		for _, group := range changeGroups {
			if err := s.executeSystemTicketUpdate(
				ctx,
				ticketID,
				"automation.batch."+group.name,
				group.changes,
				map[string]any{
					"ticket_id": ticketID,
					"origin":    "admin_automation_batch",
					"category":  group.name,
				},
				actor,
			); err != nil {
				updateErrors = append(
					updateErrors,
					fmt.Errorf("ticket %d %s: %w", ticketID, group.name, err),
				)
				break
			}
		}
	}
	return errors.Join(updateErrors...)
}

// BatchAssignTickets 批量分配工单
func (s *AutomationService) BatchAssignTickets(
	ctx context.Context,
	ticketIDs []uint,
	userID uint,
	actors ...models.ActorRef,
) error {
	// 验证用户存在
	var user models.User
	if err := s.db.WithContext(ctx).First(&user, userID).Error; err != nil {
		return fmt.Errorf("assigned user not found: %w", err)
	}

	return s.BatchUpdateTickets(ctx, ticketIDs, map[string]interface{}{
		"assigned_to_id": userID,
	}, actors...)
}

// ClassifyTicket 工单自动分类
func (s *AutomationService) ClassifyTicket(ctx context.Context, ticket *models.Ticket) error {
	if ticket == nil || ticket.ID == 0 {
		return errors.New("ticket is required")
	}
	var current models.Ticket
	if err := s.db.WithContext(ctx).First(&current, ticket.ID).Error; err != nil {
		return fmt.Errorf("load ticket for classification: %w", err)
	}
	// 基于关键词的简单分类逻辑
	content := strings.ToLower(current.Title + " " + current.Description)

	updates := map[string]interface{}{}
	if containsAnyKeyword(content, []string{"urgent", "critical", "emergency", "asap", "immediately"}) {
		updates["priority"] = models.TicketPriorityHigh
	}

	classificationRules := []struct {
		ticketType models.TicketType
		keywords   []string
	}{
		{models.TicketTypeIncident, []string{"bug", "error", "issue", "problem", "crash", "fail"}},
		{models.TicketTypeRequest, []string{"feature", "enhancement", "improvement", "add", "new"}},
		{models.TicketTypeConsultation, []string{"help", "support", "question", "how to", "guidance"}},
	}
	for _, rule := range classificationRules {
		if containsAnyKeyword(content, rule.keywords) {
			updates["type"] = rule.ticketType
			break
		}
	}

	if len(updates) == 0 {
		return nil
	}
	return s.executeSystemTicketUpdate(
		ctx,
		current.ID,
		"automation.classify",
		updates,
		map[string]any{
			"ticket_id": current.ID,
			"origin":    "automatic_classification",
		},
		models.SystemActor(automationActorID),
	)
}

func (s *AutomationService) executeSystemTicketUpdate(
	ctx context.Context,
	ticketID uint,
	operation string,
	changes map[string]interface{},
	eventData map[string]any,
	actor models.ActorRef,
) error {
	if s == nil || s.native == nil {
		return errors.New("agent-native automation service is unavailable")
	}
	var ticket models.Ticket
	if err := s.db.WithContext(ctx).First(&ticket, ticketID).Error; err != nil {
		return fmt.Errorf("load ticket: %w", err)
	}
	changes = effectiveAutomationChanges(&ticket, changes)
	if len(changes) == 0 {
		return nil
	}
	requestBody, err := json.Marshal(map[string]any{
		"ticket_id":      ticketID,
		"ticket_version": ticket.Version,
		"operation":      operation,
		"changes":        changes,
	})
	if err != nil {
		return fmt.Errorf("encode automation update: %w", err)
	}
	digest := sha256.Sum256(requestBody)
	key := fmt.Sprintf("%s:%d:%d:%x", operation, ticketID, ticket.Version, digest[:12])
	reservation, err := s.native.ReserveIdempotency(
		ctx,
		actor,
		operation,
		key,
		requestBody,
		automationReservationTTL,
	)
	if err != nil {
		return err
	}
	if reservation.Replayed {
		return nil
	}
	if eventData == nil {
		eventData = make(map[string]any)
	}
	eventData["ticket_id"] = ticketID
	eventData["changed_fields"] = sortedMapKeys(changes)
	if status, ok := changes["status"]; ok {
		eventData["status"] = status
		eventData["new_status"] = status
	}
	requiredScope, action, err := automationChangeContract(changes)
	if err != nil {
		failErr := s.native.FailIdempotency(ctx, reservation.Record.ID, AgentNativeErrorCode(err))
		return errors.Join(err, failErr)
	}
	result, err := s.native.UpdateTicketVersion(ctx, VersionedTicketUpdateInput{
		TicketID:                 ticketID,
		ExpectedVersion:          ticket.Version,
		Actor:                    actor,
		SourceProtocol:           automationSourceProtocol(actor),
		Changes:                  changes,
		EventType:                automationEventType(action),
		EventData:                eventData,
		CorrelationID:            key,
		RequiredScope:            requiredScope,
		Action:                   action,
		IdempotencyRecordID:      reservation.Record.ID,
		IdempotencyCompletionTTL: automationCompletedRetentionTTL,
	})
	if err != nil {
		failErr := s.native.FailIdempotency(ctx, reservation.Record.ID, AgentNativeErrorCode(err))
		return errors.Join(err, failErr)
	}
	_ = result
	return nil
}

type automationChangeGroup struct {
	name    string
	changes map[string]interface{}
}

func splitAutomationChangeGroups(changes map[string]interface{}) []automationChangeGroup {
	assignment := make(map[string]interface{})
	ordinary := make(map[string]interface{})
	transition := make(map[string]interface{})
	for field, value := range changes {
		switch field {
		case "status":
			transition[field] = value
		case "assigned_to_id",
			"assigned_to_actor_type",
			"assigned_to_actor_id",
			"assigned_to_service_principal_id":
			assignment[field] = value
		default:
			ordinary[field] = value
		}
	}
	groups := make([]automationChangeGroup, 0, 3)
	if len(assignment) > 0 {
		groups = append(groups, automationChangeGroup{name: "assign", changes: assignment})
	}
	if len(ordinary) > 0 {
		groups = append(groups, automationChangeGroup{name: "update", changes: ordinary})
	}
	if len(transition) > 0 {
		groups = append(groups, automationChangeGroup{name: "transition", changes: transition})
	}
	return groups
}

func automationChangeContract(changes map[string]interface{}) (string, string, error) {
	groups := splitAutomationChangeGroups(changes)
	if len(groups) != 1 {
		return "", "", fmt.Errorf(
			"%w: automation update must contain one command category",
			ErrCommandScopeMismatch,
		)
	}
	switch groups[0].name {
	case "assign":
		return models.ScopeTicketsAssign, "ticket.assign", nil
	case "transition":
		return models.ScopeTicketsTransition, "ticket.transition", nil
	default:
		return models.ScopeTicketsUpdate, "ticket.update", nil
	}
}

func automationEventType(action string) string {
	switch action {
	case "ticket.assign":
		return "io.chronodesk.ticket.assigned.v1"
	case "ticket.transition":
		return "io.chronodesk.ticket.transitioned.v1"
	default:
		return "io.chronodesk.ticket.updated.v1"
	}
}

func effectiveAutomationChanges(
	ticket *models.Ticket,
	input map[string]interface{},
) map[string]interface{} {
	result := make(map[string]interface{}, len(input))
	for field, value := range input {
		switch field {
		case "status":
			if ticket.Status == models.TicketStatus(fmt.Sprint(value)) {
				continue
			}
		case "priority":
			if ticket.Priority == models.TicketPriority(fmt.Sprint(value)) {
				continue
			}
		case "type":
			if ticket.Type == models.TicketType(fmt.Sprint(value)) {
				continue
			}
		case "assigned_to_id":
			if value == nil && ticket.AssignedToID == nil {
				continue
			}
			if userID, err := (&AutomationService{}).toUint(value); err == nil &&
				ticket.AssignedToID != nil &&
				*ticket.AssignedToID == userID &&
				ticket.AssignedToActorType == models.ActorTypeHuman &&
				ticket.AssignedToActorID == strconv.FormatUint(uint64(userID), 10) {
				continue
			}
		case "assigned_to_actor_type", "assigned_to_actor_id", "assigned_to_service_principal_id":
			// These compatibility fields are coupled to assigned_to_id below.
		}
		result[field] = value
	}
	if _, assignmentChanged := result["assigned_to_id"]; !assignmentChanged {
		delete(result, "assigned_to_actor_type")
		delete(result, "assigned_to_actor_id")
		delete(result, "assigned_to_service_principal_id")
	}
	return result
}

func sortedMapKeys(values map[string]interface{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func automationSourceProtocol(actor models.ActorRef) string {
	if actor.Type == models.ActorTypeHuman {
		return "admin"
	}
	return "automation"
}

func stableAutomationEventID(occurrenceKey string) string {
	sum := sha256.Sum256([]byte(occurrenceKey))
	value := append([]byte(nil), sum[:16]...)
	value[6] = (value[6] & 0x0f) | 0x50
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	)
}

func containsAnyKeyword(content string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(content, keyword) {
			return true
		}
	}
	return false
}
