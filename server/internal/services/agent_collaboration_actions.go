package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
)

const (
	ActionTypeTicketTransition    = "ticket.transition"
	ActionTypeTicketUpdate        = "ticket.update"
	ActionTypeTicketAssign        = "ticket.assign"
	ActionTypeTicketCommentCreate = "ticket.comment.create"
)

var (
	ErrUnsupportedProposalAction = errors.New("unsupported proposal action")
	ErrInvalidProposalPayload    = errors.New("invalid proposal action payload")
)

// ActionExecutorRegistry is an immutable allowlist of server-owned action
// schemas and executors. It deliberately has no Register method: request data,
// solution packages, Connectors, MCP tools and external Agents cannot install
// callbacks or scripts into the trusted execution path.
type ActionExecutorRegistry struct {
	native    *AgentNativeService
	executors map[string]proposalActionExecutor
}

type proposalActionExecutor interface {
	ActionType() string
	RequiredScope() string
	IsRisky() bool
	Decode(json.RawMessage) (any, error)
	Execute(
		context.Context,
		*models.ActionProposal,
		OperationContext,
		any,
	) (*proposalActionExecution, error)
}

type proposalActionExecution struct {
	Receipt OperationReceipt
	Event   *models.DomainEvent
}

func NewActionExecutorRegistry(
	native *AgentNativeService,
) (*ActionExecutorRegistry, error) {
	if native == nil {
		return nil, errors.New("agent-native service is required")
	}
	registered := []proposalActionExecutor{
		ticketTransitionProposalExecutor{native: native},
		ticketUpdateProposalExecutor{native: native},
		ticketAssignProposalExecutor{native: native},
		ticketCommentCreateProposalExecutor{native: native},
	}
	executors := make(
		map[string]proposalActionExecutor,
		len(registered),
	)
	for _, executor := range registered {
		actionType := executor.ActionType()
		if strings.TrimSpace(actionType) == "" || executors[actionType] != nil {
			return nil, fmt.Errorf(
				"invalid duplicate proposal action executor %q",
				actionType,
			)
		}
		executors[actionType] = executor
	}
	return &ActionExecutorRegistry{
		native:    native,
		executors: executors,
	}, nil
}

// CanonicalizePayload strictly decodes a proposal payload into its closed
// action schema and serializes it back to canonical JSON. Unknown fields,
// missing controls and unsupported action types fail before persistence.
func (registry *ActionExecutorRegistry) CanonicalizePayload(
	actionType string,
	payload any,
) ([]byte, error) {
	executor, err := registry.executor(actionType)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: encode %s payload: %v",
			ErrInvalidProposalPayload,
			executor.ActionType(),
			err,
		)
	}
	decoded, err := executor.Decode(raw)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: canonicalize %s payload: %v",
			ErrInvalidProposalPayload,
			executor.ActionType(),
			err,
		)
	}
	return canonical, nil
}

func (registry *ActionExecutorRegistry) RequiredScope(
	actionType string,
) (string, error) {
	executor, err := registry.executor(actionType)
	if err != nil {
		return "", err
	}
	return executor.RequiredScope(), nil
}

func (registry *ActionExecutorRegistry) execute(
	ctx context.Context,
	proposal *models.ActionProposal,
	operation OperationContext,
) (*proposalActionExecution, error) {
	if proposal == nil {
		return nil, fmt.Errorf(
			"%w: proposal is required",
			ErrInvalidProposalPayload,
		)
	}
	executor, err := registry.executor(proposal.ActionType)
	if err != nil {
		return nil, err
	}
	payload, err := executor.Decode(json.RawMessage(proposal.ActionPayload))
	if err != nil {
		return nil, err
	}
	return executor.Execute(ctx, proposal, operation, payload)
}

func (registry *ActionExecutorRegistry) authorizeReplay(
	ctx context.Context,
	proposal *models.ActionProposal,
	operation OperationContext,
) error {
	if proposal == nil {
		return ErrProposalNotExecutable
	}
	executor, err := registry.executor(proposal.ActionType)
	if err != nil {
		return err
	}
	if _, err := executor.Decode(json.RawMessage(proposal.ActionPayload)); err != nil {
		return err
	}
	_, err = registry.native.CheckAction(ctx, PolicyCheckInput{
		ServicePrincipalID: operation.Actor.ID,
		CredentialID:       operation.CredentialID,
		Scope:              executor.RequiredScope(),
		Action:             executor.ActionType(),
		ResourceType:       "ticket",
		ResourceID: strconv.FormatUint(
			uint64(proposal.TicketID),
			10,
		),
		IsWrite:        true,
		IsRisky:        executor.IsRisky(),
		SourceProtocol: string(operation.Source),
		Context:        map[string]any{"idempotent_replay": true},
	})
	return err
}

func (registry *ActionExecutorRegistry) executor(
	actionType string,
) (proposalActionExecutor, error) {
	if registry == nil || registry.native == nil {
		return nil, errors.New("proposal action registry is unavailable")
	}
	actionType = strings.ToLower(strings.TrimSpace(actionType))
	executor := registry.executors[actionType]
	if executor == nil {
		return nil, fmt.Errorf(
			"%w: %q",
			ErrUnsupportedProposalAction,
			actionType,
		)
	}
	return executor, nil
}

type ticketTransitionActionPayload struct {
	LeaseID string              `json:"lease_id"`
	Status  models.TicketStatus `json:"status"`
	Reason  string              `json:"reason"`
}

type ticketTransitionProposalExecutor struct {
	native *AgentNativeService
}

func (ticketTransitionProposalExecutor) ActionType() string {
	return ActionTypeTicketTransition
}

func (ticketTransitionProposalExecutor) RequiredScope() string {
	return models.ScopeTicketsTransition
}

func (ticketTransitionProposalExecutor) IsRisky() bool {
	return true
}

func (ticketTransitionProposalExecutor) Decode(
	raw json.RawMessage,
) (any, error) {
	var payload ticketTransitionActionPayload
	if err := decodeStrictProposalAction(raw, &payload); err != nil {
		return nil, proposalPayloadError(ActionTypeTicketTransition, err)
	}
	var err error
	payload.LeaseID, err = validateProposalLeaseID(payload.LeaseID)
	if err != nil {
		return nil, err
	}
	if !payload.Status.IsValid() {
		return nil, proposalPayloadError(
			ActionTypeTicketTransition,
			fmt.Errorf("status %q is invalid", payload.Status),
		)
	}
	payload.Reason, err = validateProposalReason(payload.Reason, true)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func (executor ticketTransitionProposalExecutor) Execute(
	ctx context.Context,
	proposal *models.ActionProposal,
	operation OperationContext,
	decoded any,
) (*proposalActionExecution, error) {
	payload, ok := decoded.(ticketTransitionActionPayload)
	if !ok {
		return nil, proposalPayloadError(
			executor.ActionType(),
			errors.New("decoded payload type is invalid"),
		)
	}
	result, err := executor.native.TransitionTicket(
		ctx,
		TransitionTicketCommand{
			TicketID:        proposal.TicketID,
			ExpectedVersion: proposal.TargetVersion,
			LeaseID:         payload.LeaseID,
			Actor:           operation.Actor,
			Status:          payload.Status,
			CredentialID:    operation.CredentialID,
			SourceProtocol:  string(operation.Source),
			RequestDigest:   proposal.ProposalDigest,
			Reason:          payload.Reason,
			TraceID:         operation.TraceID,
			CorrelationID:   operation.CorrelationID,
		},
	)
	if err != nil {
		return nil, err
	}
	return &proposalActionExecution{
		Receipt: result.Receipt,
		Event:   result.Event,
	}, nil
}

type ticketUpdateActionPayload struct {
	LeaseID       string                 `json:"lease_id"`
	Title         *string                `json:"title,omitempty"`
	Description   *string                `json:"description,omitempty"`
	Type          *models.TicketType     `json:"type,omitempty"`
	Priority      *models.TicketPriority `json:"priority,omitempty"`
	CustomerName  *string                `json:"customer_name,omitempty"`
	CustomerEmail *string                `json:"customer_email,omitempty"`
	CustomerPhone *string                `json:"customer_phone,omitempty"`
	InternalNotes *string                `json:"internal_notes,omitempty"`
}

func (payload ticketUpdateActionPayload) changes() map[string]any {
	changes := make(map[string]any, 8)
	if payload.Title != nil {
		changes["title"] = *payload.Title
	}
	if payload.Description != nil {
		changes["description"] = *payload.Description
	}
	if payload.Type != nil {
		changes["type"] = *payload.Type
	}
	if payload.Priority != nil {
		changes["priority"] = *payload.Priority
	}
	if payload.CustomerName != nil {
		changes["customer_name"] = *payload.CustomerName
	}
	if payload.CustomerEmail != nil {
		changes["customer_email"] = *payload.CustomerEmail
	}
	if payload.CustomerPhone != nil {
		changes["customer_phone"] = *payload.CustomerPhone
	}
	if payload.InternalNotes != nil {
		changes["internal_notes"] = *payload.InternalNotes
	}
	return changes
}

type ticketUpdateProposalExecutor struct {
	native *AgentNativeService
}

func (ticketUpdateProposalExecutor) ActionType() string {
	return ActionTypeTicketUpdate
}

func (ticketUpdateProposalExecutor) RequiredScope() string {
	return models.ScopeTicketsUpdate
}

func (ticketUpdateProposalExecutor) IsRisky() bool {
	return false
}

func (ticketUpdateProposalExecutor) Decode(
	raw json.RawMessage,
) (any, error) {
	var payload ticketUpdateActionPayload
	if err := decodeStrictProposalAction(raw, &payload); err != nil {
		return nil, proposalPayloadError(ActionTypeTicketUpdate, err)
	}
	var err error
	payload.LeaseID, err = validateProposalLeaseID(payload.LeaseID)
	if err != nil {
		return nil, err
	}
	if payload.Title != nil {
		value := strings.TrimSpace(*payload.Title)
		if utf8.RuneCountInString(value) < 1 ||
			utf8.RuneCountInString(value) > 255 {
			return nil, proposalPayloadError(
				ActionTypeTicketUpdate,
				errors.New("title must contain between 1 and 255 characters"),
			)
		}
		payload.Title = &value
	}
	if payload.Description != nil {
		if length := utf8.RuneCountInString(*payload.Description); length < 1 ||
			length > 10000 {
			return nil, proposalPayloadError(
				ActionTypeTicketUpdate,
				errors.New(
					"description must contain between 1 and 10000 characters",
				),
			)
		}
	}
	if payload.Type != nil && !payload.Type.IsValid() {
		return nil, proposalPayloadError(
			ActionTypeTicketUpdate,
			fmt.Errorf("type %q is invalid", *payload.Type),
		)
	}
	if payload.Priority != nil && !payload.Priority.IsValid() {
		return nil, proposalPayloadError(
			ActionTypeTicketUpdate,
			fmt.Errorf("priority %q is invalid", *payload.Priority),
		)
	}
	for field, value := range map[string]*string{
		"customer_name":  payload.CustomerName,
		"customer_email": payload.CustomerEmail,
		"customer_phone": payload.CustomerPhone,
		"internal_notes": payload.InternalNotes,
	} {
		if value != nil && utf8.RuneCountInString(*value) > 10000 {
			return nil, proposalPayloadError(
				ActionTypeTicketUpdate,
				fmt.Errorf("%s exceeds 10000 characters", field),
			)
		}
	}
	if len(payload.changes()) == 0 {
		return nil, proposalPayloadError(
			ActionTypeTicketUpdate,
			errors.New("at least one ticket field is required"),
		)
	}
	return payload, nil
}

func (executor ticketUpdateProposalExecutor) Execute(
	ctx context.Context,
	proposal *models.ActionProposal,
	operation OperationContext,
	decoded any,
) (*proposalActionExecution, error) {
	payload, ok := decoded.(ticketUpdateActionPayload)
	if !ok {
		return nil, proposalPayloadError(
			executor.ActionType(),
			errors.New("decoded payload type is invalid"),
		)
	}
	result, err := executor.native.UpdateTicketVersion(
		ctx,
		VersionedTicketUpdateInput{
			TicketID:        proposal.TicketID,
			ExpectedVersion: proposal.TargetVersion,
			LeaseID:         payload.LeaseID,
			Actor:           operation.Actor,
			CredentialID:    operation.CredentialID,
			RequiredScope:   models.ScopeTicketsUpdate,
			Action:          executor.ActionType(),
			SourceProtocol:  string(operation.Source),
			RequestDigest:   proposal.ProposalDigest,
			Changes:         payload.changes(),
			EventType:       eventcontract.TicketUpdatedEventType,
			TraceID:         operation.TraceID,
			CorrelationID:   operation.CorrelationID,
		},
	)
	if err != nil {
		return nil, err
	}
	return &proposalActionExecution{
		Receipt: result.Receipt,
		Event:   result.Event,
	}, nil
}

type ticketAssignActionPayload struct {
	LeaseID  string           `json:"lease_id"`
	Assignee *models.ActorRef `json:"assignee"`
	Reason   string           `json:"reason"`
}

type ticketAssignProposalExecutor struct {
	native *AgentNativeService
}

func (ticketAssignProposalExecutor) ActionType() string {
	return ActionTypeTicketAssign
}

func (ticketAssignProposalExecutor) RequiredScope() string {
	return models.ScopeTicketsAssign
}

func (ticketAssignProposalExecutor) IsRisky() bool {
	return true
}

func (ticketAssignProposalExecutor) Decode(
	raw json.RawMessage,
) (any, error) {
	var wire struct {
		LeaseID  string          `json:"lease_id"`
		Assignee json.RawMessage `json:"assignee"`
		Reason   string          `json:"reason"`
	}
	if err := decodeStrictProposalAction(raw, &wire); err != nil {
		return nil, proposalPayloadError(ActionTypeTicketAssign, err)
	}
	leaseID, err := validateProposalLeaseID(wire.LeaseID)
	if err != nil {
		return nil, err
	}
	if len(wire.Assignee) == 0 {
		return nil, proposalPayloadError(
			ActionTypeTicketAssign,
			errors.New("assignee is required and may be an ActorRef or null"),
		)
	}
	var assignee *models.ActorRef
	if !bytes.Equal(bytes.TrimSpace(wire.Assignee), []byte("null")) {
		var actor models.ActorRef
		if err := decodeStrictProposalAction(wire.Assignee, &actor); err != nil {
			return nil, proposalPayloadError(ActionTypeTicketAssign, err)
		}
		if err := actor.Validate(); err != nil {
			return nil, proposalPayloadError(ActionTypeTicketAssign, err)
		}
		if actor.Type != models.ActorTypeHuman &&
			actor.Type != models.ActorTypeServicePrincipal {
			return nil, proposalPayloadError(
				ActionTypeTicketAssign,
				errors.New(
					"assignee type must be human or service_principal",
				),
			)
		}
		if actor.ID != strings.TrimSpace(actor.ID) {
			return nil, proposalPayloadError(
				ActionTypeTicketAssign,
				errors.New("assignee id cannot contain surrounding whitespace"),
			)
		}
		assignee = &actor
	}
	reason, err := validateProposalReason(wire.Reason, true)
	if err != nil {
		return nil, err
	}
	return ticketAssignActionPayload{
		LeaseID:  leaseID,
		Assignee: assignee,
		Reason:   reason,
	}, nil
}

func (executor ticketAssignProposalExecutor) Execute(
	ctx context.Context,
	proposal *models.ActionProposal,
	operation OperationContext,
	decoded any,
) (*proposalActionExecution, error) {
	payload, ok := decoded.(ticketAssignActionPayload)
	if !ok {
		return nil, proposalPayloadError(
			executor.ActionType(),
			errors.New("decoded payload type is invalid"),
		)
	}
	result, err := executor.native.AssignTicket(
		ctx,
		AssignTicketCommand{
			TicketID:        proposal.TicketID,
			ExpectedVersion: proposal.TargetVersion,
			LeaseID:         payload.LeaseID,
			Actor:           operation.Actor,
			Assignee:        payload.Assignee,
			CredentialID:    operation.CredentialID,
			SourceProtocol:  string(operation.Source),
			RequestDigest:   proposal.ProposalDigest,
			Reason:          payload.Reason,
			TraceID:         operation.TraceID,
			CorrelationID:   operation.CorrelationID,
		},
	)
	if err != nil {
		return nil, err
	}
	return &proposalActionExecution{
		Receipt: result.Receipt,
		Event:   result.Event,
	}, nil
}

type ticketCommentCreateActionPayload struct {
	LeaseID     string             `json:"lease_id"`
	Content     string             `json:"content"`
	ContentType string             `json:"content_type"`
	Type        models.CommentType `json:"type"`
	Reason      string             `json:"reason,omitempty"`
}

type ticketCommentCreateProposalExecutor struct {
	native *AgentNativeService
}

func (ticketCommentCreateProposalExecutor) ActionType() string {
	return ActionTypeTicketCommentCreate
}

func (ticketCommentCreateProposalExecutor) RequiredScope() string {
	return models.ScopeCommentsWrite
}

func (ticketCommentCreateProposalExecutor) IsRisky() bool {
	return false
}

func (ticketCommentCreateProposalExecutor) Decode(
	raw json.RawMessage,
) (any, error) {
	var payload ticketCommentCreateActionPayload
	if err := decodeStrictProposalAction(raw, &payload); err != nil {
		return nil, proposalPayloadError(ActionTypeTicketCommentCreate, err)
	}
	var err error
	payload.LeaseID, err = validateProposalLeaseID(payload.LeaseID)
	if err != nil {
		return nil, err
	}
	payload.Content = strings.TrimSpace(payload.Content)
	if length := utf8.RuneCountInString(payload.Content); length < 1 ||
		length > 10000 {
		return nil, proposalPayloadError(
			ActionTypeTicketCommentCreate,
			errors.New("content must contain between 1 and 10000 characters"),
		)
	}
	if payload.ContentType != "text" && payload.ContentType != "markdown" {
		return nil, proposalPayloadError(
			ActionTypeTicketCommentCreate,
			errors.New("content_type must be text or markdown"),
		)
	}
	if payload.Type != models.CommentTypePublic &&
		payload.Type != models.CommentTypeInternal {
		return nil, proposalPayloadError(
			ActionTypeTicketCommentCreate,
			errors.New("type must be public or internal"),
		)
	}
	payload.Reason, err = validateProposalReason(payload.Reason, false)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func (executor ticketCommentCreateProposalExecutor) Execute(
	ctx context.Context,
	proposal *models.ActionProposal,
	operation OperationContext,
	decoded any,
) (*proposalActionExecution, error) {
	payload, ok := decoded.(ticketCommentCreateActionPayload)
	if !ok {
		return nil, proposalPayloadError(
			executor.ActionType(),
			errors.New("decoded payload type is invalid"),
		)
	}
	result, err := executor.native.CreateComment(
		ctx,
		NativeCommentInput{
			TicketID:        proposal.TicketID,
			ExpectedVersion: proposal.TargetVersion,
			LeaseID:         payload.LeaseID,
			Actor:           operation.Actor,
			CredentialID:    operation.CredentialID,
			SourceProtocol:  string(operation.Source),
			RequestDigest:   proposal.ProposalDigest,
			Content:         payload.Content,
			ContentType:     payload.ContentType,
			Type:            payload.Type,
			Reason:          payload.Reason,
			TraceID:         operation.TraceID,
			CorrelationID:   operation.CorrelationID,
		},
	)
	if err != nil {
		return nil, err
	}
	return &proposalActionExecution{
		Receipt: result.Receipt,
		Event:   result.Event,
	}, nil
}

func decodeStrictProposalAction(raw []byte, destination any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("payload must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("payload must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func validateProposalLeaseID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 1 || len(value) > 128 {
		return "", proposalPayloadError(
			"",
			errors.New("lease_id must contain between 1 and 128 characters"),
		)
	}
	return value, nil
}

func validateProposalReason(
	value string,
	required bool,
) (string, error) {
	value = strings.TrimSpace(value)
	length := utf8.RuneCountInString(value)
	if (required && length < 1) || length > 1000 {
		minimum := 0
		if required {
			minimum = 1
		}
		return "", proposalPayloadError(
			"",
			fmt.Errorf(
				"reason must contain between %d and 1000 characters",
				minimum,
			),
		)
	}
	return value, nil
}

func proposalPayloadError(actionType string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrInvalidProposalPayload) {
		return err
	}
	if strings.TrimSpace(actionType) == "" {
		return fmt.Errorf("%w: %v", ErrInvalidProposalPayload, err)
	}
	return fmt.Errorf(
		"%w: %s: %v",
		ErrInvalidProposalPayload,
		actionType,
		err,
	)
}
