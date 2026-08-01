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
	"unicode"
	"unicode/utf8"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/listcursor"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// AutomationService 自动化服务
type AutomationService struct {
	db             *gorm.DB
	native         *AgentNativeService
	sla            *SLAService
	logCursorCodec *listcursor.Codec
}

// NewAutomationService 创建自动化服务实例
func NewAutomationService(db *gorm.DB) *AutomationService {
	return &AutomationService{
		db:     db,
		native: NewAgentNativeService(db),
		sla:    NewSLAService(db),
	}
}

// NewAutomationServiceWithAgentNative creates the production automation
// engine. Rule actions reuse the same versioned, event-producing domain
// commands as REST, MCP and A2A instead of mutating ticket tables directly.
func NewAutomationServiceWithAgentNative(db *gorm.DB, native *AgentNativeService) *AutomationService {
	return &AutomationService{db: db, native: native, sla: NewSLAService(db)}
}

// ConfigureListCursor derives an Automation-log-only signing key from the
// deployment-owned root. Execution-log reads remain unavailable until this is
// configured explicitly.
func (s *AutomationService) ConfigureListCursor(root []byte) error {
	if s == nil || s.db == nil || len(root) == 0 {
		return ErrAutomationListCursorKey
	}
	codec, err := listcursor.NewCodec(root, "automation-execution-logs.v1")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAutomationListCursorKey, err)
	}
	s.logCursorCodec = codec
	return nil
}

func (s *AutomationService) slaDomainService() *SLAService {
	if s.sla == nil {
		s.sla = NewSLAService(s.db)
	}
	return s.sla
}

const (
	automationActorID                 = "automation-rule-engine"
	automationActionOperation         = "automation.rule.action"
	automationRuleExecutionOperation  = "automation.rule.execution"
	automationTriggerOperation        = "automation.trigger.enqueue"
	maxAutomationCausalDepth          = 16
	automationReservationTTL          = 2 * time.Minute
	automationFailureRetryDelay       = 2 * time.Second
	automationCompletedRetentionTTL   = 365 * 24 * time.Hour
	DefaultAutomationListSize         = 25
	MaxAutomationListSize             = 100
	MaxAutomationCategoryFilterLength = 50
	MaxAutomationKeywordFilterLength  = 200
	automationLogCursorVersion        = 1
	automationLogSortVersion          = "executed_at_desc_id_desc.v1"
)

var (
	ErrInvalidAutomationTriggerType = errors.New("invalid automation trigger event type")
	ErrInvalidAutomationRuleType    = errors.New("invalid automation rule type")
	ErrInvalidAutomationListQuery   = errors.New("automation list query is invalid")
	ErrInvalidAutomationListCursor  = errors.New("automation list cursor is invalid")
	ErrAutomationListCursorKey      = errors.New("automation list cursor signing key is unavailable")
	ErrQuickReplyNotFound           = errors.New("quick reply not found")
	ErrInvalidQuickReplyTags        = errors.New("quick reply tags are invalid")
)

type AutomationExecutionLogQuery struct {
	Cursor   string
	Limit    int
	RuleID   *uint
	TicketID *uint
	Success  *bool
}

type AutomationExecutionLogPage struct {
	Items      []*models.AutomationLog
	NextCursor string
	HasMore    bool
}

type automationExecutionLogCursor struct {
	Version      int    `json:"v"`
	Organization uint   `json:"organization_id"`
	Project      uint   `json:"project_id"`
	Limit        int    `json:"limit"`
	FilterHash   string `json:"filter_hash"`
	SortVersion  string `json:"sort_version"`
	ExecutedAt   string `json:"executed_at"`
	ID           uint   `json:"id"`
}

func automationProjectScope(ctx context.Context) (models.ProjectScope, error) {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return models.ProjectScope{}, fmt.Errorf(
			"trusted automation project scope is required: %w",
			err,
		)
	}
	return scope, nil
}

func scopedAutomationQuery(
	db *gorm.DB,
	scope models.ProjectScope,
) *gorm.DB {
	return db.Where(
		"organization_id = ? AND project_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
	)
}

// AutomationRuleService 自动化规则相关方法

// CreateRule 创建自动化规则
func (s *AutomationService) CreateRule(ctx context.Context, req *models.AutomationRuleRequest, userID uint) (*models.AutomationRule, error) {
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || !validAutomationRuleType(req.RuleType) {
		return nil, ErrInvalidAutomationRuleType
	}
	triggerEvent, err := normalizeAutomationRuleTriggerEvent(req.TriggerEvent)
	if err != nil {
		return nil, err
	}
	rule := &models.AutomationRule{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           req.Name,
		Description:    req.Description,
		RuleType:       req.RuleType,
		IsActive:       false,
		Priority:       1,
		TriggerEvent:   triggerEvent,
		CreatedBy:      userID,
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
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return nil, 0, err
	}
	if page < 1 || pageSize < 1 || pageSize > MaxAutomationListSize {
		return nil, 0, ErrInvalidAutomationListQuery
	}
	if ruleType != "" && !validAutomationRuleType(ruleType) {
		return nil, 0, ErrInvalidAutomationRuleType
	}
	query := scopedAutomationQuery(
		s.db.WithContext(ctx).Model(&models.AutomationRule{}),
		scope,
	).Preload("CreatedUser").Preload("UpdatedUser")

	if ruleType != "" {
		query = query.Where("rule_type = ?", ruleType)
	}
	if triggerEvent != "" {
		normalized, err := normalizeAutomationRuleTriggerEvent(triggerEvent)
		if err != nil {
			return nil, 0, err
		}
		query = query.Where("trigger_event = ?", normalized)
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
	if err := query.
		Order("priority ASC").
		Order("created_at DESC").
		Order("id DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&rules).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get rules: %w", err)
	}

	return rules, total, nil
}

// GetRuleByID 根据ID获取规则
func (s *AutomationService) GetRuleByID(ctx context.Context, ruleID uint) (*models.AutomationRule, error) {
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	var rule models.AutomationRule
	if err := scopedAutomationQuery(
		s.db.WithContext(ctx),
		scope,
	).Preload("CreatedUser").
		Preload("UpdatedUser").
		Where("id = ?", ruleID).
		First(&rule).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("rule not found")
		}
		return nil, fmt.Errorf("failed to get rule: %w", err)
	}
	return &rule, nil
}

// UpdateRule 更新规则
func (s *AutomationService) UpdateRule(ctx context.Context, ruleID uint, req *models.AutomationRuleRequest, userID uint) error {
	if req == nil || !validAutomationRuleType(req.RuleType) {
		return ErrInvalidAutomationRuleType
	}
	rule, err := s.GetRuleByID(ctx, ruleID)
	if err != nil {
		return err
	}
	triggerEvent, err := normalizeAutomationRuleTriggerEvent(req.TriggerEvent)
	if err != nil {
		return err
	}

	updates := map[string]interface{}{
		"name":          req.Name,
		"description":   req.Description,
		"rule_type":     req.RuleType,
		"trigger_event": triggerEvent,
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

	scope, err := automationProjectScope(ctx)
	if err != nil {
		return err
	}
	result := scopedAutomationQuery(
		s.db.WithContext(ctx).Model(&models.AutomationRule{}),
		scope,
	).Where("id = ?", rule.ID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("rule not found")
	}
	return nil
}

func validAutomationRuleType(ruleType string) bool {
	switch ruleType {
	case "assignment", "classification", "escalation", "sla":
		return true
	default:
		return false
	}
}

// DeleteRule 删除规则
func (s *AutomationService) DeleteRule(ctx context.Context, ruleID uint) error {
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return err
	}
	result := scopedAutomationQuery(
		s.db.WithContext(ctx),
		scope,
	).Where("id = ?", ruleID).Delete(&models.AutomationRule{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete rule: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("rule not found")
	}
	return nil
}

// EnqueueScheduledCheck durably emits the scheduler's exact CloudEvent type.
func (s *AutomationService) EnqueueScheduledCheck(ctx context.Context, ticket *models.Ticket) error {
	if s == nil || s.native == nil {
		return errors.New("agent-native automation service is unavailable")
	}
	if ticket == nil || ticket.ID == 0 || ticket.Version == 0 {
		return errors.New("versioned ticket is required")
	}
	var err error
	ctx, err = EnsureSystemProjectOperationContext(
		ctx,
		models.ProjectScope{
			OrganizationID: ticket.OrganizationID,
			ProjectID:      ticket.ProjectID,
		},
		models.SystemActor("scheduler"),
		"",
		"",
	)
	if err != nil {
		return err
	}
	return s.enqueueNativeTrigger(
		ctx,
		eventcontract.AutomationScheduledCheckEventType,
		ticket,
	)
}

// HasActiveRules reports whether a trigger has at least one rule that can
// consume it. Scheduled scans call this before loading tickets, and
// enqueueNativeTrigger repeats the guard so direct callers cannot create an
// event/Outbox storm when no rule is enabled.
func (s *AutomationService) HasActiveRules(ctx context.Context, triggerEvent string) (bool, error) {
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return false, err
	}
	normalized, err := normalizeAutomationRuleTriggerEvent(triggerEvent)
	if err != nil {
		return false, err
	}
	var count int64
	if err := scopedAutomationQuery(
		s.db.WithContext(ctx).Model(&models.AutomationRule{}),
		scope,
	).
		Where("is_active = ? AND trigger_event = ?", true, normalized).
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
	normalized, err := normalizeAutomationRuleTriggerEvent(triggerEvent)
	if err != nil {
		return err
	}
	hasActiveRules, err := s.HasActiveRules(ctx, normalized)
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
		normalized,
		ticket.ID,
		ticket.Version,
		bucket,
	)
	eventID := stableAutomationEventID(occurrenceKey)
	eventData := map[string]any{
		"ticket_id":      ticket.ID,
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
	err = transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		event, appendErr := s.native.AppendDomainEventTx(ctx, tx, DomainEventInput{
			ID:              eventID,
			Type:            normalized,
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
// AutomationRule.TriggerEvent matches event.Type exactly. Events emitted by
// this engine (or causally descended from one) are acknowledged without
// re-entering rules, which provides a hard loop boundary in addition to
// per-action idempotency.
func (s *AutomationService) ExecuteDomainEvent(ctx context.Context, event CloudEventEnvelope) error {
	if s == nil || s.native == nil {
		return errors.New("agent-native automation service is unavailable")
	}
	triggerEvent := strings.TrimSpace(event.Type)
	if !eventcontract.IsAutomationRuleTriggerEventType(triggerEvent) {
		return nil
	}
	if strings.TrimSpace(event.ID) == "" {
		return errors.New("automation event id is required")
	}
	scope := models.ProjectScope{
		OrganizationID: event.OrganizationID,
		ProjectID:      event.ProjectID,
	}
	var err error
	ctx, err = EnsureSystemProjectOperationContext(
		ctx,
		scope,
		models.SystemActor(automationActorID),
		event.TraceID,
		event.CorrelationID,
	)
	if err != nil {
		return err
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
	if err := s.db.WithContext(ctx).Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		ticketID,
		scope.OrganizationID,
		scope.ProjectID,
	).First(&ticket).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return fmt.Errorf("load automation ticket: %w", err)
	}
	if err := s.executeNativeRules(
		ctx,
		triggerEvent,
		event,
		&ticket,
		lineageRules,
		rootEventID,
	); err != nil {
		return fmt.Errorf("automation trigger %s: %w", triggerEvent, err)
	}
	return nil
}

func normalizeAutomationRuleTriggerEvent(value string) (string, error) {
	normalized := strings.TrimSpace(value)
	if !eventcontract.IsAutomationRuleTriggerEventType(normalized) {
		return "", fmt.Errorf("%w: %q", ErrInvalidAutomationTriggerType, normalized)
	}
	return normalized, nil
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
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, "", false, err
	}
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
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				causationID,
				scope.OrganizationID,
				scope.ProjectID,
			).
			First(&cause).Error
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
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return err
	}
	var rules []models.AutomationRule
	if err := scopedAutomationQuery(
		s.db.WithContext(ctx),
		scope,
	).
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
		if err := s.db.WithContext(ctx).Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			ticket.ID,
			scope.OrganizationID,
			scope.ProjectID,
		).First(&current).Error; err != nil {
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
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return err
	}
	if rule == nil ||
		rule.OrganizationID != scope.OrganizationID ||
		rule.ProjectID != scope.ProjectID ||
		ticket == nil ||
		ticket.OrganizationID != scope.OrganizationID ||
		ticket.ProjectID != scope.ProjectID {
		return errors.New("automation rule and ticket must match trusted project scope")
	}
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
	ID             uint      `json:"id"`
	OrganizationID uint      `json:"organization_id"`
	ProjectID      uint      `json:"project_id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	RuleType       string    `json:"rule_type"`
	IsActive       bool      `json:"is_active"`
	Priority       int       `json:"priority"`
	TriggerEvent   string    `json:"trigger_event"`
	Conditions     string    `json:"conditions"`
	Actions        string    `json:"actions"`
	CreatedBy      uint      `json:"created_by"`
	UpdatedBy      *uint     `json:"updated_by,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type automationRuleExecutionSnapshot struct {
	RootEventID        string                 `json:"root_event_id"`
	Rule               automationRuleSnapshot `json:"rule"`
	ConditionEvaluated bool                   `json:"condition_evaluated"`
	Matched            bool                   `json:"matched"`
}

func newAutomationRuleSnapshot(rule *models.AutomationRule) (automationRuleSnapshot, error) {
	if rule == nil ||
		rule.ID == 0 ||
		rule.OrganizationID == 0 ||
		rule.ProjectID == 0 {
		return automationRuleSnapshot{}, errors.New("automation rule is required")
	}
	return automationRuleSnapshot{
		ID:             rule.ID,
		OrganizationID: rule.OrganizationID,
		ProjectID:      rule.ProjectID,
		Name:           rule.Name,
		Description:    rule.Description,
		RuleType:       rule.RuleType,
		IsActive:       rule.IsActive,
		Priority:       rule.Priority,
		TriggerEvent:   rule.TriggerEvent,
		Conditions:     rule.Conditions,
		Actions:        rule.Actions,
		CreatedBy:      rule.CreatedBy,
		UpdatedBy:      rule.UpdatedBy,
		UpdatedAt:      rule.UpdatedAt,
	}, nil
}

func (snapshot automationRuleSnapshot) rule() (*models.AutomationRule, error) {
	if snapshot.ID == 0 ||
		snapshot.OrganizationID == 0 ||
		snapshot.ProjectID == 0 ||
		strings.TrimSpace(snapshot.TriggerEvent) == "" {
		return nil, errors.New("automation rule snapshot is invalid")
	}
	return &models.AutomationRule{
		ID:             snapshot.ID,
		OrganizationID: snapshot.OrganizationID,
		ProjectID:      snapshot.ProjectID,
		Name:           snapshot.Name,
		Description:    snapshot.Description,
		RuleType:       snapshot.RuleType,
		IsActive:       snapshot.IsActive,
		Priority:       snapshot.Priority,
		TriggerEvent:   snapshot.TriggerEvent,
		Conditions:     snapshot.Conditions,
		Actions:        snapshot.Actions,
		CreatedBy:      snapshot.CreatedBy,
		UpdatedBy:      snapshot.UpdatedBy,
		UpdatedAt:      snapshot.UpdatedAt,
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
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	rootEventID = strings.TrimSpace(rootEventID)
	keyPrefix := automationRootKey(rootEventID) + ":"
	var records []models.IdempotencyRecord
	if err := s.db.WithContext(ctx).
		Select("key", "resource_snapshot").
		Where(
			"organization_id = ? AND project_id = ? AND actor_type = ? AND actor_id = ? AND operation = ? AND state IN ? AND key LIKE ?",
			scope.OrganizationID,
			scope.ProjectID,
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
		if rule.OrganizationID != scope.OrganizationID ||
			rule.ProjectID != scope.ProjectID {
			return nil, errors.New(
				"frozen automation rule does not match trusted project scope",
			)
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
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	rootEventID = strings.TrimSpace(rootEventID)
	if rootEventID == "" || rule == nil || rule.ID == 0 {
		return nil, errors.New("automation causal root and rule are required")
	}
	if rule.OrganizationID != scope.OrganizationID ||
		rule.ProjectID != scope.ProjectID {
		return nil, errors.New(
			"automation rule does not match trusted project scope",
		)
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
		OrganizationID:   scope.OrganizationID,
		ProjectID:        scope.ProjectID,
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
			"organization_id = ? AND project_id = ? AND actor_type = ? AND actor_id = ? AND operation = ? AND key = ?",
			scope.OrganizationID,
			scope.ProjectID,
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
			"id = ? AND organization_id = ? AND project_id = ? AND state IN ? AND expires_at <= ?",
			existing.ID,
			scope.OrganizationID,
			scope.ProjectID,
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
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return err
	}
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
			"id = ? AND organization_id = ? AND project_id = ? AND state = ? AND resource_id = ? AND expires_at > ?",
			claim.RecordID,
			scope.OrganizationID,
			scope.ProjectID,
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
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return err
	}
	if claim == nil || claim.RecordID == "" || claim.Token == "" {
		return errors.New("automation rule execution claim is required")
	}
	now := s.native.now()
	result := s.db.WithContext(ctx).Model(&models.IdempotencyRecord{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND state = ? AND resource_id = ? AND expires_at > ?",
			claim.RecordID,
			scope.OrganizationID,
			scope.ProjectID,
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
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return err
	}
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
	return transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		result := tx.Model(&models.IdempotencyRecord{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND state = ? AND resource_id = ? AND expires_at > ?",
				claim.RecordID,
				scope.OrganizationID,
				scope.ProjectID,
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
			scope,
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
			OrganizationID:  scope.OrganizationID,
			ProjectID:       scope.ProjectID,
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
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return err
	}
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
	return transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		result := tx.Model(&models.IdempotencyRecord{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND state = ? AND resource_id = ? AND expires_at > ?",
				claim.RecordID,
				scope.OrganizationID,
				scope.ProjectID,
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
			scope,
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
			OrganizationID:  scope.OrganizationID,
			ProjectID:       scope.ProjectID,
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
	scope models.ProjectScope,
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
	result := scopedAutomationQuery(
		tx.Model(&models.AutomationRule{}),
		scope,
	).
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
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return automationRuleCheckpoint{}, err
	}
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
			"organization_id = ? AND project_id = ? AND actor_type = ? AND actor_id = ? AND operation = ? AND key IN ?",
			scope.OrganizationID,
			scope.ProjectID,
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
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return err
	}
	var ticket models.Ticket
	ticketID, err := automationTicketID(event)
	if err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		ticketID,
		scope.OrganizationID,
		scope.ProjectID,
	).First(&ticket).Error; err != nil {
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
		}, eventcontract.TicketAssignedEventType, models.ScopeTicketsAssign, "ticket.assign",
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
			eventcontract.TicketUpdatedEventType,
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
			eventcontract.TicketTransitionedEventType,
			models.ScopeTicketsTransition,
			"ticket.transition",
			"status",
		)
	case "add_comment":
		raw, ok := action.Params["content"]
		if !ok || strings.TrimSpace(fmt.Sprint(raw)) == "" {
			return errors.New("content parameter required")
		}
		_, err := s.native.CreateComment(ctx, NativeCommentInput{
			TicketID:                 ticket.ID,
			ExpectedVersion:          ticket.Version,
			Actor:                    models.SystemActor(automationActorID),
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
		}, eventcontract.TicketEscalatedEventType, "", "automation.ticket.escalate",
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
	return transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
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
	return transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		event, err := s.native.AppendDomainEventTx(ctx, tx, DomainEventInput{
			Type:            eventcontract.AutomationNotificationRequestedEventType,
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
		if ticket.CreatedByID != nil {
			return *ticket.CreatedByID
		}
		return nil
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

// ListExecutionLogs returns a stable, project-bound execution timeline.
func (s *AutomationService) ListExecutionLogs(
	ctx context.Context,
	query AutomationExecutionLogQuery,
) (*AutomationExecutionLogPage, error) {
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	if s == nil || s.db == nil || s.logCursorCodec == nil {
		return nil, ErrAutomationListCursorKey
	}
	if query.Limit < 1 || query.Limit > MaxAutomationListSize ||
		(query.RuleID != nil && *query.RuleID == 0) ||
		(query.TicketID != nil && *query.TicketID == 0) {
		return nil, ErrInvalidAutomationListQuery
	}
	filterHash := automationExecutionLogFilterHash(query)
	cursor, err := s.decodeExecutionLogCursor(
		query.Cursor,
		scope,
		query.Limit,
		filterHash,
	)
	if err != nil {
		return nil, err
	}

	logsQuery := scopedAutomationQuery(
		s.db.WithContext(ctx).Model(&models.AutomationLog{}),
		scope,
	).
		Preload(
			"Rule",
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		).
		Preload(
			"Ticket",
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		)
	if query.RuleID != nil {
		logsQuery = logsQuery.Where("rule_id = ?", *query.RuleID)
	}
	if query.TicketID != nil {
		logsQuery = logsQuery.Where("ticket_id = ?", *query.TicketID)
	}
	if query.Success != nil {
		logsQuery = logsQuery.Where("success = ?", *query.Success)
	}
	if cursor != nil {
		logsQuery = logsQuery.Where(
			"executed_at < ? OR (executed_at = ? AND id < ?)",
			cursor.ExecutedAt,
			cursor.ExecutedAt,
			cursor.ID,
		)
	}

	var logs []*models.AutomationLog
	if err := logsQuery.
		Order("executed_at DESC").
		Order("id DESC").
		Limit(query.Limit + 1).
		Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("list automation execution logs: %w", err)
	}
	hasMore := len(logs) > query.Limit
	if hasMore {
		logs = logs[:query.Limit]
	}
	nextCursor := ""
	if hasMore && len(logs) > 0 {
		last := logs[len(logs)-1]
		nextCursor, err = s.logCursorCodec.Encode(
			automationExecutionLogCursor{
				Version:      automationLogCursorVersion,
				Organization: scope.OrganizationID,
				Project:      scope.ProjectID,
				Limit:        query.Limit,
				FilterHash:   filterHash,
				SortVersion:  automationLogSortVersion,
				ExecutedAt:   last.ExecutedAt.UTC().Format(time.RFC3339Nano),
				ID:           last.ID,
			},
		)
		if err != nil {
			return nil, ErrAutomationListCursorKey
		}
	}
	if logs == nil {
		logs = []*models.AutomationLog{}
	}
	return &AutomationExecutionLogPage{
		Items:      logs,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func automationExecutionLogFilterHash(
	query AutomationExecutionLogQuery,
) string {
	raw, _ := json.Marshal(struct {
		RuleID   *uint `json:"rule_id"`
		TicketID *uint `json:"ticket_id"`
		Success  *bool `json:"success"`
	}{
		RuleID:   query.RuleID,
		TicketID: query.TicketID,
		Success:  query.Success,
	})
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:])
}

func (s *AutomationService) decodeExecutionLogCursor(
	raw string,
	scope models.ProjectScope,
	limit int,
	filterHash string,
) (*struct {
	ExecutedAt time.Time
	ID         uint
}, error) {
	if raw == "" {
		return nil, nil
	}
	var cursor automationExecutionLogCursor
	if err := s.logCursorCodec.Decode(raw, &cursor); err != nil {
		return nil, ErrInvalidAutomationListCursor
	}
	executedAt, err := time.Parse(time.RFC3339Nano, cursor.ExecutedAt)
	if err != nil || executedAt.IsZero() || cursor.ID == 0 ||
		cursor.Version != automationLogCursorVersion ||
		cursor.Organization != scope.OrganizationID ||
		cursor.Project != scope.ProjectID ||
		cursor.Limit != limit ||
		cursor.FilterHash != filterHash ||
		cursor.SortVersion != automationLogSortVersion ||
		strings.TrimSpace(cursor.ExecutedAt) != cursor.ExecutedAt {
		return nil, ErrInvalidAutomationListCursor
	}
	return &struct {
		ExecutedAt time.Time
		ID         uint
	}{
		ExecutedAt: executedAt.UTC(),
		ID:         cursor.ID,
	}, nil
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
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	config := &models.SLAConfig{
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
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

	if err := transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		// 如果设置为默认配置，仅取消当前项目的其他默认配置。
		if config.IsDefault {
			if err := scopedAutomationQuery(
				tx.Model(&models.SLAConfig{}),
				scope,
			).Where("is_default = ?", true).
				Update("is_default", false).Error; err != nil {
				return fmt.Errorf("failed to update existing default config: %w", err)
			}
		}
		if err := tx.Create(config).Error; err != nil {
			return fmt.Errorf("failed to create SLA config: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return config, nil
}

// GetSLAConfigs 获取SLA配置列表
func (s *AutomationService) GetSLAConfigs(ctx context.Context, isActive *bool, page, pageSize int) ([]*models.SLAConfig, int64, error) {
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return nil, 0, err
	}
	offset, err := validateAutomationConfigListQuery(page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	query := scopedAutomationQuery(
		s.db.WithContext(ctx).Model(&models.SLAConfig{}),
		scope,
	)

	if isActive != nil {
		query = query.Where("is_active = ?", *isActive)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count SLA configs: %w", err)
	}

	var configs []*models.SLAConfig
	if err := query.
		Order("is_default DESC").
		Order("created_at DESC").
		Order("id DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&configs).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get SLA configs: %w", err)
	}

	return configs, total, nil
}

// GetSLAConfigForTicket 为工单获取适用的SLA配置
func (s *AutomationService) GetSLAConfigForTicket(ctx context.Context, ticket *models.Ticket) (*models.SLAConfig, error) {
	return s.slaDomainService().GetConfigForTicket(ctx, ticket)
}

// CalculateSLADeadlines 计算SLA截止时间
func (s *AutomationService) CalculateSLADeadlines(ctx context.Context, ticket *models.Ticket, config *models.SLAConfig) (responseDeadline, resolutionDeadline time.Time, err error) {
	return s.slaDomainService().CalculateDeadlines(ctx, ticket, config)
}

// addWorkingTime 添加工作时间（考虑工作时间、周末、节假日）
func (s *AutomationService) addWorkingTime(
	startTime time.Time,
	duration time.Duration,
	workingHours *models.WorkingHours,
	excludeWeekends, excludeHolidays bool,
) (time.Time, error) {
	return s.slaDomainService().addWorkingTime(
		startTime,
		duration,
		workingHours,
		excludeWeekends,
		excludeHolidays,
	)
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
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	template := &models.TicketTemplate{
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
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
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return nil, 0, err
	}
	offset, err := validateAutomationConfigListQuery(page, pageSize)
	if err != nil ||
		!validAutomationConfigFilter(
			category,
			MaxAutomationCategoryFilterLength,
		) {
		return nil, 0, ErrInvalidAutomationListQuery
	}
	query := scopedAutomationQuery(
		s.db.WithContext(ctx).Model(&models.TicketTemplate{}),
		scope,
	).Preload("CreatedUser").Preload("AssignToUser")

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
	if err := query.
		Order("created_at DESC").
		Order("id DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&templates).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get templates: %w", err)
	}

	return templates, total, nil
}

// GetTemplateByID 根据ID获取模板
func (s *AutomationService) GetTemplateByID(ctx context.Context, templateID uint) (*models.TicketTemplate, error) {
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	var template models.TicketTemplate
	if err := scopedAutomationQuery(
		s.db.WithContext(ctx),
		scope,
	).Preload("CreatedUser").
		Preload("AssignToUser").
		Where("id = ?", templateID).
		First(&template).Error; err != nil {
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
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || userID == 0 ||
		!automationHumanActorMatches(ctx, userID) {
		return nil, ErrInvalidQuickReplyTags
	}
	tags, err := normalizeQuickReplyTags(req.Tags)
	if err != nil {
		return nil, err
	}
	reply := &models.QuickReply{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           req.Name,
		Category:       req.Category,
		Content:        req.Content,
		Tags:           tags,
		IsPublic:       false,
		CreatedBy:      userID,
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
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return nil, 0, err
	}
	offset, err := validateAutomationConfigListQuery(page, pageSize)
	if err != nil ||
		userID == 0 ||
		!automationHumanActorMatches(ctx, userID) ||
		!validAutomationConfigFilter(
			category,
			MaxAutomationCategoryFilterLength,
		) ||
		!validAutomationConfigFilter(
			keyword,
			MaxAutomationKeywordFilterLength,
		) {
		return nil, 0, ErrInvalidAutomationListQuery
	}
	query := scopedAutomationQuery(
		s.db.WithContext(ctx).Model(&models.QuickReply{}),
		scope,
	).Preload("CreatedUser")

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
		like := "%" + escapeAutomationLike(strings.ToLower(keyword)) + "%"
		query = query.Where(
			"(lower(name) LIKE ? ESCAPE '\\' OR lower(content) LIKE ? ESCAPE '\\' OR lower(tags) LIKE ? ESCAPE '\\')",
			like,
			like,
			like,
		)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count quick replies: %w", err)
	}

	var replies []*models.QuickReply
	if err := query.
		Order("created_at DESC").
		Order("id DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&replies).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get quick replies: %w", err)
	}

	return replies, total, nil
}

// UseQuickReply 使用快速回复（增加使用计数）
func (s *AutomationService) UseQuickReply(
	ctx context.Context,
	replyID uint,
	userID uint,
) error {
	scope, err := automationProjectScope(ctx)
	if err != nil {
		return err
	}
	if replyID == 0 || userID == 0 {
		return ErrQuickReplyNotFound
	}
	if !automationHumanActorMatches(ctx, userID) {
		return ErrQuickReplyNotFound
	}
	result := scopedAutomationQuery(
		s.db.WithContext(ctx).Model(&models.QuickReply{}),
		scope,
	).Where(
		"id = ? AND (created_by = ? OR is_public = ?)",
		replyID,
		userID,
		true,
	).
		UpdateColumn("usage_count", gorm.Expr("usage_count + ?", 1))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrQuickReplyNotFound
	}
	return nil
}

func validateAutomationConfigListQuery(page, pageSize int) (int, error) {
	if page < 1 ||
		pageSize < 1 ||
		pageSize > MaxAutomationListSize ||
		page > math.MaxInt/pageSize {
		return 0, ErrInvalidAutomationListQuery
	}
	return (page - 1) * pageSize, nil
}

func validAutomationConfigFilter(value string, maximum int) bool {
	if value == "" {
		return true
	}
	if maximum < 1 ||
		!utf8.ValidString(value) ||
		strings.TrimSpace(value) != value ||
		len([]rune(value)) > maximum {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func escapeAutomationLike(value string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	).Replace(value)
}

func automationHumanActorMatches(ctx context.Context, userID uint) bool {
	operation, err := OperationContextFromContext(ctx)
	return err == nil &&
		operation.Source == SourceProtocolHumanREST &&
		operation.Actor == models.HumanActor(userID)
}

func normalizeQuickReplyTags(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	const (
		maxTags       = 20
		maxTagRunes   = 50
		maxStoredSize = 200
	)
	seen := make(map[string]struct{})
	tags := make([]string, 0, maxTags)
	for _, raw := range strings.Split(value, ",") {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		if !validAutomationConfigFilter(tag, maxTagRunes) {
			return "", ErrInvalidQuickReplyTags
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		tags = append(tags, tag)
		if len(tags) > maxTags {
			return "", ErrInvalidQuickReplyTags
		}
	}
	normalized := strings.Join(tags, ",")
	if len([]rune(normalized)) > maxStoredSize {
		return "", ErrInvalidQuickReplyTags
	}
	return normalized, nil
}

// ClassifyTicket 工单自动分类
func (s *AutomationService) ClassifyTicket(ctx context.Context, ticket *models.Ticket) error {
	if ticket == nil || ticket.ID == 0 {
		return errors.New("ticket is required")
	}
	var err error
	ctx, err = EnsureSystemProjectOperationContext(
		ctx,
		models.ProjectScope{
			OrganizationID: ticket.OrganizationID,
			ProjectID:      ticket.ProjectID,
		},
		models.SystemActor(automationActorID),
		"",
		"",
	)
	if err != nil {
		return err
	}
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return err
	}
	var current models.Ticket
	if err := s.db.WithContext(ctx).Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		ticket.ID,
		scope.OrganizationID,
		scope.ProjectID,
	).First(&current).Error; err != nil {
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
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return err
	}
	var ticket models.Ticket
	if err := s.db.WithContext(ctx).Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		ticketID,
		scope.OrganizationID,
		scope.ProjectID,
	).First(&ticket).Error; err != nil {
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
		return eventcontract.TicketAssignedEventType
	case "ticket.transition":
		return eventcontract.TicketTransitionedEventType
	default:
		return eventcontract.TicketUpdatedEventType
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
			// These authoritative actor projection fields are coupled to
			// assigned_to_id below.
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
