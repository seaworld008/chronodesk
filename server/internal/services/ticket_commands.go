package services

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
)

// AssignTicketCommand is the protocol-neutral Assignment command. Assignee nil
// releases the Ticket; non-nil targets are resolved by the canonical
// Assignment resolver before any write is attempted.
type AssignTicketCommand struct {
	TicketID                 uint
	ExpectedVersion          uint64
	LeaseID                  string
	Actor                    models.ActorRef
	Assignee                 *models.ActorRef
	CredentialID             string
	PolicyDecisionID         string
	SourceProtocol           string
	RequestDigest            string
	Reason                   string
	TraceID                  string
	CorrelationID            string
	CausationID              string
	IdempotencyRecordID      string
	IdempotencyCompletionTTL time.Duration
	OutboxTargets            []OutboxTarget

	historyRecords []ticketHistorySpec
}

// TransitionTicketCommand is the protocol-neutral Ticket lifecycle command.
type TransitionTicketCommand struct {
	TicketID                 uint
	ExpectedVersion          uint64
	LeaseID                  string
	Actor                    models.ActorRef
	Status                   models.TicketStatus
	CredentialID             string
	PolicyDecisionID         string
	SourceProtocol           string
	RequestDigest            string
	Reason                   string
	Comment                  string
	ResolutionNotes          string
	TraceID                  string
	CorrelationID            string
	CausationID              string
	IdempotencyRecordID      string
	IdempotencyCompletionTTL time.Duration
	OutboxTargets            []OutboxTarget

	historyRecords []ticketHistorySpec
}

// EscalateTicketCommand atomically marks a Ticket escalated and may also raise
// priority and change Assignment. A Service Principal therefore needs
// tickets:transition and, when Assignee is present, tickets:assign.
type EscalateTicketCommand struct {
	TicketID                   uint
	ExpectedVersion            uint64
	LeaseID                    string
	Actor                      models.ActorRef
	Priority                   *models.TicketPriority
	Assignee                   *models.ActorRef
	CredentialID               string
	TransitionPolicyDecisionID string
	AssignmentPolicyDecisionID string
	SourceProtocol             string
	RequestDigest              string
	Reason                     string
	Comment                    string
	TraceID                    string
	CorrelationID              string
	CausationID                string
	IdempotencyRecordID        string
	IdempotencyCompletionTTL   time.Duration
	OutboxTargets              []OutboxTarget

	historyRecords []ticketHistorySpec
}

type ticketHistorySpec struct {
	Action      models.HistoryAction
	Description string
	FieldName   string
	OldValue    string
	NewValue    string
	IsVisible   bool
	IsImportant bool
	Details     string
}

func (s *AgentNativeService) buildHumanTicketUpdate(
	ctx context.Context,
	current *models.Ticket,
	request *models.TicketUpdateRequest,
) (map[string]any, []ticketHistorySpec, bool, error) {
	if current == nil || request == nil {
		return nil, nil, false, fmt.Errorf("ticket update input is required")
	}
	changes := make(map[string]any)
	histories := make([]ticketHistorySpec, 0, 8)
	add := func(
		field string,
		oldValue any,
		newValue any,
		action models.HistoryAction,
		description string,
		important bool,
	) {
		if reflect.DeepEqual(oldValue, newValue) {
			return
		}
		changes[field] = newValue
		histories = append(histories, ticketHistorySpec{
			Action:      action,
			Description: description,
			FieldName:   field,
			OldValue:    bulkAuditValue(oldValue),
			NewValue:    bulkAuditValue(newValue),
			IsVisible:   true,
			IsImportant: important,
		})
	}
	if request.Title != nil {
		add("title", current.Title, *request.Title, models.HistoryActionUpdate, "标题已更新", false)
	}
	if request.Description != nil {
		if current.Description != *request.Description {
			changes["description"] = *request.Description
			histories = append(histories, ticketHistorySpec{
				Action:      models.HistoryActionUpdate,
				Description: "描述已更新",
				FieldName:   "description",
				OldValue:    truncateString(current.Description, 50),
				NewValue:    truncateString(*request.Description, 50),
				IsVisible:   true,
			})
		}
	}
	if request.Status != nil {
		if !request.Status.IsValid() {
			return nil, nil, false, fmt.Errorf("%w: invalid status %q", ErrInvalidTicketTransition, *request.Status)
		}
		add(
			"status",
			current.Status,
			*request.Status,
			models.HistoryActionStatusChange,
			fmt.Sprintf(
				"状态从「%s」变更为「%s」",
				getStatusLabel(string(current.Status)),
				getStatusLabel(string(*request.Status)),
			),
			true,
		)
	}
	if request.Priority != nil {
		add(
			"priority",
			current.Priority,
			*request.Priority,
			models.HistoryActionPriorityChange,
			fmt.Sprintf(
				"优先级从「%s」变更为「%s」",
				getPriorityLabel(string(current.Priority)),
				getPriorityLabel(string(*request.Priority)),
			),
			true,
		)
	}
	if request.Type != nil {
		add(
			"type",
			current.Type,
			*request.Type,
			models.HistoryActionUpdate,
			fmt.Sprintf("类型从「%s」变更为「%s」", current.Type, *request.Type),
			false,
		)
	}
	if request.Source != nil {
		add(
			"source",
			current.Source,
			*request.Source,
			models.HistoryActionUpdate,
			fmt.Sprintf(
				"来源从「%s」变更为「%s」",
				getSourceLabel(string(current.Source)),
				getSourceLabel(string(*request.Source)),
			),
			false,
		)
	}

	assignmentResolved := false
	if request.AssignedToID != nil {
		assignee := models.HumanActor(*request.AssignedToID)
		if current.AssignedToActorType != assignee.Type ||
			current.AssignedToActorID != assignee.ID ||
			current.AssignedToID == nil ||
			*current.AssignedToID != *request.AssignedToID ||
			current.AssignedToServicePrincipalID != nil {
			assignmentChanges, err := s.ResolveTicketAssignmentChanges(ctx, &assignee)
			if err != nil {
				return nil, nil, false, err
			}
			for field, value := range assignmentChanges {
				changes[field] = value
			}
			histories = append(histories, ticketHistorySpec{
				Action:      models.HistoryActionAssign,
				Description: fmt.Sprintf("工单已分配给用户 ID: %d", *request.AssignedToID),
				FieldName:   "assigned_to_id",
				OldValue:    getAssigneeValue(current.AssignedToID),
				NewValue:    strconv.FormatUint(uint64(*request.AssignedToID), 10),
				IsVisible:   true,
				IsImportant: true,
			})
			assignmentResolved = true
		}
	}
	if request.CategoryID != nil {
		add("category_id", current.CategoryID, request.CategoryID, models.HistoryActionUpdate, "分类已更新", false)
	}
	if request.SubcategoryID != nil {
		add("subcategory_id", current.SubcategoryID, request.SubcategoryID, models.HistoryActionUpdate, "子分类已更新", false)
	}
	if request.Tags != nil {
		add("tags", []string(current.Tags), []string(request.Tags), models.HistoryActionUpdate, "标签已更新", false)
	}
	if request.DueDate != nil {
		add("due_date", current.DueDate, request.DueDate, models.HistoryActionUpdate, "截止时间已更新", true)
	}
	if request.CustomerEmail != nil {
		add("customer_email", current.CustomerEmail, *request.CustomerEmail, models.HistoryActionUpdate, "客户邮箱已更新", false)
	}
	if request.CustomerPhone != nil {
		add("customer_phone", current.CustomerPhone, *request.CustomerPhone, models.HistoryActionUpdate, "客户电话已更新", false)
	}
	if request.CustomerName != nil {
		add("customer_name", current.CustomerName, *request.CustomerName, models.HistoryActionUpdate, "客户名称已更新", false)
	}
	if request.InternalNotes != nil {
		add("internal_notes", current.InternalNotes, *request.InternalNotes, models.HistoryActionUpdate, "内部备注已更新", false)
	}
	if request.Rating != nil {
		add("rating", current.Rating, request.Rating, models.HistoryActionUpdate, "评分已更新", false)
	}
	if request.RatingComment != nil {
		add("rating_comment", current.RatingComment, *request.RatingComment, models.HistoryActionUpdate, "评分备注已更新", false)
	}
	if request.CustomFields != nil {
		add(
			"custom_fields",
			current.CustomFields.Data(),
			request.CustomFields.ToMap(),
			models.HistoryActionUpdate,
			"自定义字段已更新",
			false,
		)
	}
	if request.AgentContext != nil {
		add(
			"agent_context",
			current.AgentContext.Data(),
			*request.AgentContext,
			models.HistoryActionUpdate,
			"Agent 上下文已更新",
			false,
		)
	}
	return changes, histories, assignmentResolved, nil
}

func (s *AgentNativeService) AssignTicket(
	ctx context.Context,
	command AssignTicketCommand,
) (*VersionedTicketUpdateResult, error) {
	changes, err := s.ResolveTicketAssignmentChanges(ctx, command.Assignee)
	if err != nil {
		return nil, err
	}
	eventData := map[string]any{
		"ticket_id": command.TicketID,
		"assignee":  command.Assignee,
		"reason":    strings.TrimSpace(command.Reason),
	}
	if command.Assignee != nil && command.Assignee.Type == models.ActorTypeHuman {
		assigneeID, parseErr := strconv.ParseUint(command.Assignee.ID, 10, 64)
		if parseErr != nil || assigneeID == 0 {
			return nil, fmt.Errorf("%w: human assignee id must be a user id", ErrInvalidAssignee)
		}
		eventData["assigned_to_id"] = uint(assigneeID)
	}
	return s.UpdateTicketVersion(ctx, VersionedTicketUpdateInput{
		TicketID:                 command.TicketID,
		ExpectedVersion:          command.ExpectedVersion,
		LeaseID:                  command.LeaseID,
		Actor:                    command.Actor,
		CredentialID:             command.CredentialID,
		PolicyDecisionID:         command.PolicyDecisionID,
		RequiredScope:            models.ScopeTicketsAssign,
		Action:                   "ticket.assign",
		SourceProtocol:           command.SourceProtocol,
		RequestDigest:            command.RequestDigest,
		Changes:                  changes,
		EventType:                eventcontract.TicketAssignedEventType,
		EventData:                eventData,
		TraceID:                  command.TraceID,
		CorrelationID:            command.CorrelationID,
		CausationID:              command.CausationID,
		IsRisky:                  true,
		IdempotencyRecordID:      command.IdempotencyRecordID,
		IdempotencyCompletionTTL: command.IdempotencyCompletionTTL,
		OutboxTargets:            command.OutboxTargets,
		assignmentResolved:       true,
		historyRecords:           command.historyRecords,
	})
}

func (s *AgentNativeService) TransitionTicket(
	ctx context.Context,
	command TransitionTicketCommand,
) (*VersionedTicketUpdateResult, error) {
	if !command.Status.IsValid() {
		return nil, fmt.Errorf("%w: invalid target status %q", ErrInvalidTicketTransition, command.Status)
	}
	return s.UpdateTicketVersion(ctx, VersionedTicketUpdateInput{
		TicketID:         command.TicketID,
		ExpectedVersion:  command.ExpectedVersion,
		LeaseID:          command.LeaseID,
		Actor:            command.Actor,
		CredentialID:     command.CredentialID,
		PolicyDecisionID: command.PolicyDecisionID,
		RequiredScope:    models.ScopeTicketsTransition,
		Action:           "ticket.transition",
		SourceProtocol:   command.SourceProtocol,
		RequestDigest:    command.RequestDigest,
		Changes:          map[string]any{"status": command.Status},
		EventType:        eventcontract.TicketTransitionedEventType,
		EventData: map[string]any{
			"ticket_id": command.TicketID,
			"status":    command.Status,
			"reason":    strings.TrimSpace(command.Reason),
		},
		TraceID:                  command.TraceID,
		CorrelationID:            command.CorrelationID,
		CausationID:              command.CausationID,
		IsRisky:                  true,
		IdempotencyRecordID:      command.IdempotencyRecordID,
		IdempotencyCompletionTTL: command.IdempotencyCompletionTTL,
		OutboxTargets:            command.OutboxTargets,
		historyRecords:           command.historyRecords,
	})
}

func (s *AgentNativeService) EscalateTicket(
	ctx context.Context,
	command EscalateTicketCommand,
) (*VersionedTicketUpdateResult, error) {
	if strings.TrimSpace(command.Reason) == "" {
		return nil, fmt.Errorf("ticket escalation reason is required")
	}
	changes := map[string]any{"is_escalated": true}
	if command.Priority != nil {
		if !command.Priority.IsValid() {
			return nil, fmt.Errorf("invalid ticket priority %q", *command.Priority)
		}
		changes["priority"] = *command.Priority
	}
	var assignmentDecisionID string
	if command.Assignee != nil {
		assignmentChanges, err := s.ResolveTicketAssignmentChanges(ctx, command.Assignee)
		if err != nil {
			return nil, err
		}
		for field, value := range assignmentChanges {
			changes[field] = value
		}
		if command.Actor.Type == models.ActorTypeServicePrincipal {
			assignmentDecisionID, err = s.ensureTicketCommandPolicy(
				ctx,
				command.Actor,
				command.CredentialID,
				command.AssignmentPolicyDecisionID,
				models.ScopeTicketsAssign,
				"ticket.assign",
				command.TicketID,
				command.RequestDigest,
				command.SourceProtocol,
				true,
			)
			if err != nil {
				return nil, err
			}
		}
	}
	eventData := map[string]any{
		"ticket_id": command.TicketID,
		"reason":    strings.TrimSpace(command.Reason),
		"priority":  command.Priority,
		"assignee":  command.Assignee,
	}
	if assignmentDecisionID != "" {
		eventData["assignment_policy_decision_id"] = assignmentDecisionID
	}
	if command.Assignee != nil && command.Assignee.Type == models.ActorTypeHuman {
		assigneeID, parseErr := strconv.ParseUint(command.Assignee.ID, 10, 64)
		if parseErr != nil || assigneeID == 0 {
			return nil, fmt.Errorf("%w: human assignee id must be a user id", ErrInvalidAssignee)
		}
		eventData["assigned_to_id"] = uint(assigneeID)
	}
	return s.UpdateTicketVersion(ctx, VersionedTicketUpdateInput{
		TicketID:                      command.TicketID,
		ExpectedVersion:               command.ExpectedVersion,
		LeaseID:                       command.LeaseID,
		Actor:                         command.Actor,
		CredentialID:                  command.CredentialID,
		PolicyDecisionID:              command.TransitionPolicyDecisionID,
		RequiredScope:                 models.ScopeTicketsTransition,
		Action:                        "ticket.escalate",
		SourceProtocol:                command.SourceProtocol,
		RequestDigest:                 command.RequestDigest,
		Changes:                       changes,
		EventType:                     eventcontract.TicketEscalatedEventType,
		EventData:                     eventData,
		TraceID:                       command.TraceID,
		CorrelationID:                 command.CorrelationID,
		CausationID:                   command.CausationID,
		IsRisky:                       true,
		IdempotencyRecordID:           command.IdempotencyRecordID,
		IdempotencyCompletionTTL:      command.IdempotencyCompletionTTL,
		OutboxTargets:                 command.OutboxTargets,
		assignmentResolved:            command.Assignee != nil,
		authorizationContractOverride: true,
		historyRecords:                command.historyRecords,
	})
}

func (s *AgentNativeService) ensureTicketCommandPolicy(
	ctx context.Context,
	actor models.ActorRef,
	credentialID string,
	policyDecisionID string,
	scope string,
	action string,
	ticketID uint,
	requestDigest string,
	sourceProtocol string,
	risky bool,
) (string, error) {
	check := PolicyCheckInput{
		ServicePrincipalID: actor.ID,
		CredentialID:       credentialID,
		Scope:              scope,
		Action:             action,
		ResourceType:       "ticket",
		ResourceID:         strconv.FormatUint(uint64(ticketID), 10),
		IsWrite:            true,
		IsRisky:            risky,
		RequestDigest:      requestDigest,
		SourceProtocol:     sourceProtocol,
	}
	if policyDecisionID != "" {
		if err := s.validatePolicyDecision(ctx, policyDecisionID, actor, check); err != nil {
			return "", err
		}
		return policyDecisionID, nil
	}
	decision, err := s.CheckAction(ctx, check)
	if err != nil {
		return "", err
	}
	return decision.ID, nil
}

func ticketUpdateNotificationTargets(
	before *models.Ticket,
	after *models.Ticket,
	actor models.ActorRef,
	fields []string,
) []OutboxTarget {
	if before == nil || after == nil {
		return nil
	}
	actorID := uint(0)
	if actor.Type == models.ActorTypeHuman {
		if parsed, err := strconv.ParseUint(actor.ID, 10, 64); err == nil {
			actorID = uint(parsed)
		}
	}
	targets := make([]OutboxTarget, 0, 3)
	if fieldsContain(fields, "status") && before.Status != after.Status {
		targets = append(targets, TicketStatusNotificationOutboxTargets(after, actorID)...)
	}
	if assignmentChanged(before, after) {
		targets = append(targets, TicketAssignedNotificationOutboxTargets(after, actorID)...)
	}
	return targets
}

func addTicketNotificationEventSnapshot(data map[string]any, ticket *models.Ticket) {
	if data == nil || ticket == nil {
		return
	}
	data["ticket_number"] = ticket.TicketNumber
	data["ticket_title"] = ticket.Title
	data["ticket_priority"] = ticket.Priority
}

func assignmentChanged(before *models.Ticket, after *models.Ticket) bool {
	if before == nil || after == nil {
		return false
	}
	return before.AssignedToActorType != after.AssignedToActorType ||
		before.AssignedToActorID != after.AssignedToActorID ||
		!sameUintPointer(before.AssignedToID, after.AssignedToID) ||
		!sameStringPointer(
			before.AssignedToServicePrincipalID,
			after.AssignedToServicePrincipalID,
		)
}

func sameUintPointer(left *uint, right *uint) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameStringPointer(left *string, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func ticketHistoriesForUpdate(
	input VersionedTicketUpdateInput,
	before *models.Ticket,
	after *models.Ticket,
	fields []string,
	details string,
	humanUserID *uint,
	policyDecisionID string,
) []*models.TicketHistory {
	specs := input.historyRecords
	if len(specs) == 0 {
		action, description, fieldName := defaultTicketHistory(before, after, fields)
		specs = []ticketHistorySpec{{
			Action:      action,
			Description: description,
			FieldName:   fieldName,
			IsVisible:   true,
			IsImportant: true,
			Details:     details,
		}}
	}
	histories := make([]*models.TicketHistory, 0, len(specs))
	for _, spec := range specs {
		historyDetails := spec.Details
		if historyDetails == "" {
			historyDetails = details
		}
		histories = append(histories, &models.TicketHistory{
			TicketID:           after.ID,
			UserID:             humanUserID,
			ActorType:          input.Actor.Type,
			ActorID:            input.Actor.ID,
			ServicePrincipalID: actorServicePrincipalID(input.Actor),
			Action:             spec.Action,
			Description:        spec.Description,
			Details:            historyDetails,
			FieldName:          spec.FieldName,
			OldValue:           spec.OldValue,
			NewValue:           spec.NewValue,
			IsVisible:          spec.IsVisible,
			IsSystem:           input.Actor.Type == models.ActorTypeSystem,
			IsAutomated:        input.Actor.Type != models.ActorTypeHuman,
			IsImportant:        spec.IsImportant,
			Metadata:           policyMetadata(policyDecisionID),
		})
	}
	return histories
}

func defaultTicketHistory(
	before *models.Ticket,
	after *models.Ticket,
	fields []string,
) (models.HistoryAction, string, string) {
	if before != nil && after != nil && before.Status != after.Status {
		return models.HistoryActionStatusChange,
			fmt.Sprintf(
				"状态从「%s」变更为「%s」",
				getStatusLabel(string(before.Status)),
				getStatusLabel(string(after.Status)),
			),
			"status"
	}
	if assignmentChanged(before, after) {
		if after.AssignedToActorType == "" || after.AssignedToActorID == "" {
			return models.HistoryActionUnassign, "工单已取消分配", "assigned_to_actor"
		}
		return models.HistoryActionAssign,
			fmt.Sprintf(
				"工单已分配给 %s/%s",
				after.AssignedToActorType,
				after.AssignedToActorID,
			),
			"assigned_to_actor"
	}
	if fieldsContain(fields, "is_escalated") && after != nil && after.IsEscalated {
		return models.HistoryActionEscalate, "工单已升级", "escalation"
	}
	if len(fields) == 1 {
		return historyActionForChanges(fields), "工单已更新", fields[0]
	}
	return historyActionForChanges(fields), "工单已更新", ""
}

func escalatedPriority(current models.TicketPriority) models.TicketPriority {
	switch current {
	case models.TicketPriorityLow:
		return models.TicketPriorityNormal
	case models.TicketPriorityNormal:
		return models.TicketPriorityHigh
	case models.TicketPriorityHigh:
		return models.TicketPriorityUrgent
	case models.TicketPriorityUrgent:
		return models.TicketPriorityCritical
	default:
		return current
	}
}

func normalizeTicketEventDataObject(value any) (map[string]any, error) {
	if object, ok := value.(map[string]any); ok {
		clone := make(map[string]any, len(object))
		for field, item := range object {
			clone[field] = item
		}
		return clone, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode ticket event data: %w", err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil || object == nil {
		return nil, fmt.Errorf("ticket notification event data must be an object")
	}
	return object, nil
}
