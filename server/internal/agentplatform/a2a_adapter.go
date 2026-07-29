package agentplatform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gongdan-system/internal/a2a"
	"gongdan-system/internal/agentauth"
	"gongdan-system/internal/models"
	"gongdan-system/internal/services"
)

const a2aSourceProtocol = "a2a"

type a2aCommandReservation struct {
	ID            string
	RequestDigest string
}

type a2aCommandReservationContextKey struct{}

// A2AExecutionIdentity is a trusted authentication snapshot. It must come from
// middleware or server configuration, never from A2A message metadata.
type A2AExecutionIdentity struct {
	Actor               models.ActorRef
	CredentialID        string
	CompatibilityUserID uint
}

type a2aIdentityContextKey struct{}

func WithA2AExecutionIdentity(ctx context.Context, identity A2AExecutionIdentity) context.Context {
	return context.WithValue(ctx, a2aIdentityContextKey{}, identity)
}

func A2AExecutionIdentityFromContext(ctx context.Context) (A2AExecutionIdentity, bool) {
	identity, ok := ctx.Value(a2aIdentityContextKey{}).(A2AExecutionIdentity)
	return identity, ok
}

// BindA2AIdentity snapshots the service-principal identity established by
// agentauth.Middleware into request context. Mount it after authentication and
// before the A2A RPC handler.
func BindA2AIdentity() gin.HandlerFunc {
	return func(c *gin.Context) {
		principalID := strings.TrimSpace(c.GetString(agentauth.ContextPrincipalID))
		credentialID := strings.TrimSpace(c.GetString(agentauth.ContextCredentialID))
		if principalID == "" {
			WriteProblem(c, 401, ProblemUnauthorized, "Verified A2A principal is missing", false)
			c.Abort()
			return
		}
		identity := A2AExecutionIdentity{
			Actor:        models.ServicePrincipalActor(principalID),
			CredentialID: credentialID,
		}
		ctx := WithA2AExecutionIdentity(c.Request.Context(), identity)
		ctx = a2a.WithTaskOwner(ctx, a2a.TaskOwner{
			ActorType:    string(identity.Actor.Type),
			ActorID:      identity.Actor.ID,
			CredentialID: identity.CredentialID,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// A2ARequestPolicyMiddleware applies the service-principal policy engine to
// protocol-level Task operations that do not enter A2ABackend (for example
// GetTask, cancellation and Push configuration).
type A2ATaskResourceResolver interface {
	ResolveA2ATaskID(context.Context, string) (string, error)
}

type A2ATaskResourceResolverFunc func(
	context.Context,
	string,
) (string, error)

func (f A2ATaskResourceResolverFunc) ResolveA2ATaskID(
	ctx context.Context,
	messageID string,
) (string, error) {
	return f(ctx, messageID)
}

func A2ARequestPolicyMiddleware(
	native *services.AgentNativeService,
	resolver A2ATaskResourceResolver,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if native == nil || resolver == nil {
			WriteProblem(c, http.StatusServiceUnavailable, ProblemInternal, "A2A policy service is unavailable", true)
			c.Abort()
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, (2<<20)+1))
		if err != nil || len(body) > 2<<20 {
			WriteProblem(c, http.StatusRequestEntityTooLarge, ProblemInvalidRequest, "A2A request is too large", false)
			c.Abort()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		request, err := a2a.DecodeJSONRPCRequest(body)
		if err != nil || request.Validate() != nil {
			c.Next()
			return
		}
		policies, err := a2a.ClassifyRequestPolicies(request)
		if err != nil {
			// The A2A handler uses the same strict decoder and will return the
			// protocol-level method/params error without executing an action.
			c.Next()
			return
		}
		if policies[0].ResourceID == "*" && policies[0].MessageID != "" {
			taskID, resolveErr := resolver.ResolveA2ATaskID(
				c.Request.Context(),
				policies[0].MessageID,
			)
			switch {
			case resolveErr == nil:
				for index := range policies {
					policies[index].ResourceID = taskID
				}
			case errors.Is(resolveErr, a2a.ErrTaskNotFound):
				// This is a new Task rather than a replay.
			default:
				writeNativeProblem(c, resolveErr)
				c.Abort()
				return
			}
		}
		policyPayload, err := a2a.CanonicalRequestPolicyPayload(request)
		if err != nil {
			c.Next()
			return
		}
		principalID := c.GetString(agentauth.ContextPrincipalID)
		credentialID := c.GetString(agentauth.ContextCredentialID)
		for _, policy := range policies {
			if _, err := native.CheckAction(c.Request.Context(), services.PolicyCheckInput{
				ServicePrincipalID: principalID,
				CredentialID:       credentialID,
				Scope:              models.ScopeTasksManage,
				Action:             policy.Action,
				ResourceType:       "a2a_task",
				ResourceID:         policy.ResourceID,
				IsWrite:            policy.Write,
				IsRisky:            policy.Risky,
				RequestDigest:      digestBytes(policyPayload),
				SourceProtocol:     a2aSourceProtocol,
			}); err != nil {
				writeNativeProblem(c, err)
				c.Abort()
				return
			}
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Next()
	}
}

type A2AIdentityResolver interface {
	ResolveA2AIdentity(ctx context.Context, task a2a.Task, message a2a.Message) (A2AExecutionIdentity, error)
}

type A2AIdentityResolverFunc func(context.Context, a2a.Task, a2a.Message) (A2AExecutionIdentity, error)

func (f A2AIdentityResolverFunc) ResolveA2AIdentity(
	ctx context.Context,
	task a2a.Task,
	message a2a.Message,
) (A2AExecutionIdentity, error) {
	return f(ctx, task, message)
}

type ContextA2AIdentityResolver struct{}

func (ContextA2AIdentityResolver) ResolveA2AIdentity(
	ctx context.Context,
	_ a2a.Task,
	_ a2a.Message,
) (A2AExecutionIdentity, error) {
	identity, ok := A2AExecutionIdentityFromContext(ctx)
	if !ok {
		return A2AExecutionIdentity{}, errors.New("trusted A2A identity is unavailable")
	}
	return identity, nil
}

type StaticA2AIdentityResolver struct {
	Identity A2AExecutionIdentity
}

func (r StaticA2AIdentityResolver) ResolveA2AIdentity(
	context.Context,
	a2a.Task,
	a2a.Message,
) (A2AExecutionIdentity, error) {
	return r.Identity, nil
}

// A2ABackend maps explicitly structured A2A skills to AgentNative commands.
// It contains no LLM, natural-language intent inference, or Ticket/A2A state
// coupling.
type A2ABackend struct {
	db                    *gorm.DB
	native                *services.AgentNativeService
	identity              A2AIdentityResolver
	commandReservationTTL time.Duration
}

func NewA2ABackend(
	db *gorm.DB,
	native *services.AgentNativeService,
	resolver ...A2AIdentityResolver,
) (*A2ABackend, error) {
	if db == nil || native == nil {
		return nil, errors.New("A2A adapter requires database and AgentNativeService")
	}
	identityResolver := A2AIdentityResolver(ContextA2AIdentityResolver{})
	if len(resolver) > 0 {
		if resolver[0] == nil {
			return nil, errors.New("A2A identity resolver cannot be nil")
		}
		identityResolver = resolver[0]
	}
	return &A2ABackend{
		db:                    db,
		native:                native,
		identity:              identityResolver,
		commandReservationTTL: a2a.DefaultExecutionClaimTTL / 2,
	}, nil
}

func (b *A2ABackend) Process(
	ctx context.Context,
	task a2a.Task,
	message a2a.Message,
	reporter a2a.Reporter,
) error {
	identity, err := b.identity.ResolveA2AIdentity(ctx, task, message)
	if err != nil || identity.Actor.Validate() != nil {
		return reportA2AState(ctx, reporter, a2a.TaskStateAuthRequired, "authentication_required", nil)
	}

	if identity.Actor.Type == models.ActorTypeServicePrincipal {
		release, acquireErr := b.native.AcquireAgentExecution(ctx, identity.Actor.ID)
		if acquireErr != nil {
			return b.reportDomainError(ctx, reporter, acquireErr)
		}
		defer release()
	}

	skill, payload, parseErr := structuredA2ACommand(task, message)
	if parseErr != nil {
		return reportA2AState(
			ctx,
			reporter,
			a2a.TaskStateInputRequired,
			"structured_input_required",
			parseErr.required,
		)
	}
	reservation, replayed, reserveErr := b.reserveA2ACommand(
		ctx,
		task,
		message,
		identity,
		skill,
		payload,
	)
	if reserveErr != nil {
		if errors.Is(reserveErr, services.ErrIdempotencyInProgress) {
			return fmt.Errorf("%w: domain command reservation is still active", a2a.ErrExecutionDeferred)
		}
		return b.reportDomainError(ctx, reporter, reserveErr)
	}
	if replayed != nil {
		if err := b.authorizeA2AReplay(ctx, task, identity, skill, payload); err != nil {
			return b.reportDomainError(ctx, reporter, err)
		}
		return reportA2AIdempotentReplay(ctx, reporter, task, skill, replayed)
	}
	if reservation.ID != "" {
		ctx = context.WithValue(ctx, a2aCommandReservationContextKey{}, reservation)
	}

	switch skill {
	case "ticket-intake":
		err = b.ticketIntake(ctx, task, message, identity, payload, reporter)
	case "ticket-query":
		err = b.ticketQuery(ctx, task, message, identity, payload, reporter)
	case "ticket-work":
		err = b.ticketWork(ctx, task, message, identity, payload, reporter)
	case "ticket-comment":
		err = b.ticketComment(ctx, task, message, identity, payload, reporter)
	case "ticket-escalation":
		err = b.ticketEscalation(ctx, task, message, identity, payload, reporter)
	default:
		return reportA2AState(ctx, reporter, a2a.TaskStateRejected, "unsupported_skill", nil)
	}
	if err == nil {
		return nil
	}
	if reservation.ID != "" {
		_ = b.native.FailIdempotency(ctx, reservation.ID, services.AgentNativeErrorCode(err))
	}
	var invalid *a2aCommandInputError
	if errors.As(err, &invalid) {
		return reportA2AState(
			ctx,
			reporter,
			a2a.TaskStateInputRequired,
			"structured_input_required",
			invalid.required,
		)
	}
	return b.reportDomainError(ctx, reporter, err)
}

func (b *A2ABackend) reserveA2ACommand(
	ctx context.Context,
	task a2a.Task,
	message a2a.Message,
	identity A2AExecutionIdentity,
	skill string,
	payload map[string]any,
) (a2aCommandReservation, *models.IdempotencyRecord, error) {
	operation, write := a2aSkillOperation(skill, payload)
	if !write {
		return a2aCommandReservation{}, nil, nil
	}
	body, err := json.Marshal(map[string]any{
		"skill":      skill,
		"operation":  operation,
		"task_id":    task.ID,
		"context_id": task.ContextID,
		"payload":    payload,
	})
	if err != nil {
		return a2aCommandReservation{}, nil, err
	}
	reservation, err := b.native.ReserveIdempotency(
		ctx,
		identity.Actor,
		"a2a."+skill+"."+operation,
		message.MessageID,
		body,
		b.commandReservationTTL,
	)
	if err != nil {
		return a2aCommandReservation{}, nil, err
	}
	if reservation.Replayed {
		return a2aCommandReservation{}, reservation.Record, nil
	}
	return a2aCommandReservation{
		ID:            reservation.Record.ID,
		RequestDigest: digestBytes(body),
	}, nil, nil
}

func a2aSkillOperation(skill string, payload map[string]any) (string, bool) {
	switch skill {
	case "ticket-intake":
		return "create", true
	case "ticket-work":
		operation := strings.ToLower(strings.TrimSpace(fmt.Sprint(payload["operation"])))
		if operation == "" || operation == "<nil>" {
			operation = "command"
		}
		return operation, true
	case "ticket-comment":
		return "create", true
	case "ticket-escalation":
		return "escalate", true
	default:
		return "read", false
	}
}

func a2aReservationFromContext(ctx context.Context) a2aCommandReservation {
	reservation, _ := ctx.Value(a2aCommandReservationContextKey{}).(a2aCommandReservation)
	return reservation
}

// Cancel intentionally does not transition or otherwise mutate a Ticket.
func (b *A2ABackend) Cancel(context.Context, a2a.Task) error {
	return nil
}

type a2aCommandInputError struct {
	required []string
}

func (e *a2aCommandInputError) Error() string {
	return "structured A2A input is missing or invalid"
}

func requireA2AFields(fields ...string) error {
	return &a2aCommandInputError{required: append([]string(nil), fields...)}
}

func structuredA2ACommand(task a2a.Task, message a2a.Message) (string, map[string]any, *a2aCommandInputError) {
	var skill string
	for _, metadata := range []map[string]any{task.Metadata, message.Metadata} {
		candidate, _ := metadata["skill"].(string)
		candidate = normalizeA2ASkill(candidate)
		if candidate == "" {
			continue
		}
		if skill != "" && candidate != skill {
			return "", nil, &a2aCommandInputError{required: []string{"one unambiguous skill"}}
		}
		skill = candidate
	}

	var payload map[string]any
	for _, part := range message.Parts {
		if len(part.Data) == 0 {
			continue
		}
		if payload != nil {
			return "", nil, &a2aCommandInputError{required: []string{"exactly one JSON data part"}}
		}
		decoder := json.NewDecoder(bytes.NewReader(part.Data))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil || payload == nil {
			return "", nil, &a2aCommandInputError{required: []string{"JSON object data part"}}
		}
	}
	if payload == nil {
		if input, ok := message.Metadata["input"].(map[string]any); ok {
			payload = cloneA2AMap(input)
		} else if input, ok := task.Metadata["input"].(map[string]any); ok {
			payload = cloneA2AMap(input)
		}
	}
	if payload == nil {
		return "", nil, &a2aCommandInputError{required: []string{"skill", "JSON object data part"}}
	}
	if candidate, ok := payload["skill"].(string); ok {
		candidate = normalizeA2ASkill(candidate)
		if skill != "" && candidate != "" && candidate != skill {
			return "", nil, &a2aCommandInputError{required: []string{"one unambiguous skill"}}
		}
		if candidate != "" {
			skill = candidate
		}
		delete(payload, "skill")
	}
	if nested, ok := payload["input"].(map[string]any); ok {
		if len(payload) != 1 {
			return "", nil, &a2aCommandInputError{required: []string{"input object without peer command fields"}}
		}
		payload = cloneA2AMap(nested)
	}
	if skill == "" {
		return "", nil, &a2aCommandInputError{required: []string{"skill"}}
	}
	return skill, payload, nil
}

func normalizeA2ASkill(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

type ticketIntakeCommand struct {
	Title         string                `json:"title"`
	Description   string                `json:"description"`
	Type          models.TicketType     `json:"type"`
	Priority      models.TicketPriority `json:"priority"`
	CategoryID    *uint                 `json:"category_id,omitempty"`
	SubcategoryID *uint                 `json:"subcategory_id,omitempty"`
	Tags          []string              `json:"tags,omitempty"`
	DueDate       *time.Time            `json:"due_date,omitempty"`
	CustomerEmail string                `json:"customer_email,omitempty"`
	CustomerPhone string                `json:"customer_phone,omitempty"`
	CustomerName  string                `json:"customer_name,omitempty"`
	CustomFields  map[string]any        `json:"custom_fields,omitempty"`
	AgentContext  *models.AgentContext  `json:"agent_context,omitempty"`
}

func (b *A2ABackend) ticketIntake(
	ctx context.Context,
	task a2a.Task,
	message a2a.Message,
	identity A2AExecutionIdentity,
	payload map[string]any,
	reporter a2a.Reporter,
) error {
	var command ticketIntakeCommand
	if err := decodeA2ACommand(payload, &command); err != nil ||
		strings.TrimSpace(command.Title) == "" ||
		strings.TrimSpace(command.Description) == "" ||
		!command.Type.IsValid() ||
		!command.Priority.IsValid() {
		return requireA2AFields("title", "description", "type", "priority")
	}
	var customFields *models.JSONMap
	if command.CustomFields != nil {
		value := models.JSONMap(command.CustomFields)
		customFields = &value
	}
	request := models.TicketCreateRequest{
		Title:         command.Title,
		Description:   command.Description,
		Type:          command.Type,
		Priority:      command.Priority,
		Source:        models.TicketSourceAgent,
		CategoryID:    command.CategoryID,
		SubcategoryID: command.SubcategoryID,
		Tags:          models.StringList(command.Tags),
		DueDate:       command.DueDate,
		CustomerEmail: command.CustomerEmail,
		CustomerPhone: command.CustomerPhone,
		CustomerName:  command.CustomerName,
		CustomFields:  customFields,
		AgentContext:  command.AgentContext,
	}
	reservation := a2aReservationFromContext(ctx)
	result, err := b.native.CreateNativeTicket(ctx, services.NativeTicketCreateInput{
		Request:             request,
		Actor:               identity.Actor,
		CompatibilityUserID: identity.CompatibilityUserID,
		CredentialID:        identity.CredentialID,
		SourceProtocol:      a2aSourceProtocol,
		RequestDigest:       reservation.RequestDigest,
		TrustLevel:          models.TicketTrustLevelUntrusted,
		TraceID:             task.ID,
		CorrelationID:       task.ContextID,
		IdempotencyRecordID: reservation.ID,
	})
	if err != nil {
		return err
	}
	return reportA2AResult(ctx, reporter, task, "ticket-intake", map[string]any{
		"ticket":  result.Ticket.ToResponse(),
		"receipt": result.Receipt,
	})
}

type ticketQueryCommand struct {
	TicketID uint `json:"ticket_id"`
}

func (b *A2ABackend) ticketQuery(
	ctx context.Context,
	task a2a.Task,
	_ a2a.Message,
	identity A2AExecutionIdentity,
	payload map[string]any,
	reporter a2a.Reporter,
) error {
	var command ticketQueryCommand
	if err := decodeA2ACommand(payload, &command); err != nil || command.TicketID == 0 {
		return requireA2AFields("ticket_id")
	}
	if identity.Actor.Type == models.ActorTypeServicePrincipal {
		if _, err := b.native.CheckAction(ctx, services.PolicyCheckInput{
			ServicePrincipalID: identity.Actor.ID,
			CredentialID:       identity.CredentialID,
			Scope:              models.ScopeTicketsRead,
			Action:             "ticket.query",
			ResourceType:       "ticket",
			ResourceID:         strconv.FormatUint(uint64(command.TicketID), 10),
			SourceProtocol:     a2aSourceProtocol,
			Context: map[string]any{
				"a2a_task_id":    task.ID,
				"a2a_context_id": task.ContextID,
			},
		}); err != nil {
			return err
		}
	}
	var ticket models.Ticket
	if err := b.db.WithContext(ctx).
		Preload("CreatedBy").
		Preload("AssignedTo").
		Preload("Category").
		Preload("Subcategory").
		First(&ticket, command.TicketID).Error; err != nil {
		return err
	}
	return reportA2AResult(ctx, reporter, task, "ticket-query", map[string]any{
		"ticket": ticket.ToResponse(),
	})
}

type ticketWorkCommand struct {
	Operation       string              `json:"operation"`
	TicketID        uint                `json:"ticket_id"`
	ExpectedVersion uint64              `json:"expected_version,omitempty"`
	LeaseID         string              `json:"lease_id,omitempty"`
	LeaseSeconds    int                 `json:"lease_seconds,omitempty"`
	Changes         map[string]any      `json:"changes,omitempty"`
	Status          models.TicketStatus `json:"status,omitempty"`
	Assignee        *models.ActorRef    `json:"assignee,omitempty"`
	Reason          string              `json:"reason,omitempty"`
}

func (b *A2ABackend) ticketWork(
	ctx context.Context,
	task a2a.Task,
	message a2a.Message,
	identity A2AExecutionIdentity,
	payload map[string]any,
	reporter a2a.Reporter,
) error {
	var command ticketWorkCommand
	if err := decodeA2ACommand(payload, &command); err != nil ||
		command.TicketID == 0 ||
		strings.TrimSpace(command.Operation) == "" {
		return requireA2AFields("operation", "ticket_id")
	}
	command.Operation = strings.ToLower(strings.TrimSpace(command.Operation))
	switch command.Operation {
	case "claim":
		if command.ExpectedVersion == 0 {
			return requireA2AFields("expected_version")
		}
		reservation := a2aReservationFromContext(ctx)
		ttl := time.Duration(command.LeaseSeconds) * time.Second
		result, err := b.native.ClaimTicketLeaseCommand(ctx, services.ClaimTicketLeaseCommandInput{
			TicketID:            command.TicketID,
			Actor:               identity.Actor,
			ExpectedVersion:     command.ExpectedVersion,
			TTL:                 ttl,
			CredentialID:        identity.CredentialID,
			SourceProtocol:      a2aSourceProtocol,
			RequestDigest:       reservation.RequestDigest,
			IdempotencyRecordID: reservation.ID,
			TraceID:             task.ID,
			CorrelationID:       task.ContextID,
			CausationID:         message.MessageID,
		})
		if err != nil {
			return err
		}
		return reportA2AResult(ctx, reporter, task, "ticket-work", map[string]any{
			"operation":      "claim",
			"lease_id":       result.Lease.ID,
			"expires_at":     result.Lease.ExpiresAt,
			"ticket_version": result.Lease.TicketVersion,
			"receipt":        result.Receipt,
		})
	case "release":
		if command.LeaseID == "" {
			return requireA2AFields("lease_id")
		}
		reservation := a2aReservationFromContext(ctx)
		result, err := b.native.ReleaseTicketLeaseCommand(ctx, services.ReleaseTicketLeaseCommandInput{
			LeaseID:             command.LeaseID,
			Actor:               identity.Actor,
			Reason:              command.Reason,
			CredentialID:        identity.CredentialID,
			SourceProtocol:      a2aSourceProtocol,
			RequestDigest:       reservation.RequestDigest,
			IdempotencyRecordID: reservation.ID,
			TraceID:             task.ID,
			CorrelationID:       task.ContextID,
			CausationID:         message.MessageID,
		})
		if err != nil {
			return err
		}
		return reportA2AResult(ctx, reporter, task, "ticket-work", map[string]any{
			"operation": "release",
			"ticket_id": command.TicketID,
			"lease_id":  command.LeaseID,
			"receipt":   result.Receipt,
		})
	case "update":
		if command.ExpectedVersion == 0 || command.LeaseID == "" || len(command.Changes) == 0 {
			return requireA2AFields("expected_version", "lease_id", "changes")
		}
		return b.updateTicket(ctx, task, message, identity, reporter, services.VersionedTicketUpdateInput{
			TicketID:        command.TicketID,
			ExpectedVersion: command.ExpectedVersion,
			LeaseID:         command.LeaseID,
			RequiredScope:   models.ScopeTicketsUpdate,
			Action:          "ticket.update",
			Changes:         command.Changes,
			EventData: map[string]any{
				"a2a_task_id":    task.ID,
				"a2a_context_id": task.ContextID,
				"reason":         command.Reason,
			},
		}, "update")
	case "transition":
		if command.ExpectedVersion == 0 || command.LeaseID == "" || !command.Status.IsValid() {
			return requireA2AFields("expected_version", "lease_id", "status")
		}
		return b.updateTicket(ctx, task, message, identity, reporter, services.VersionedTicketUpdateInput{
			TicketID:        command.TicketID,
			ExpectedVersion: command.ExpectedVersion,
			LeaseID:         command.LeaseID,
			RequiredScope:   models.ScopeTicketsTransition,
			Action:          "ticket.transition",
			Changes:         map[string]any{"status": command.Status},
			EventType:       "io.chronodesk.ticket.transitioned.v1",
			EventData: map[string]any{
				"a2a_task_id":    task.ID,
				"a2a_context_id": task.ContextID,
				"status":         command.Status,
				"reason":         command.Reason,
			},
			IsRisky: true,
		}, "transition")
	case "assign":
		if command.ExpectedVersion == 0 || command.LeaseID == "" || command.Assignee == nil {
			return requireA2AFields("expected_version", "lease_id", "assignee")
		}
		changes, err := b.assignmentChanges(ctx, *command.Assignee)
		if err != nil {
			return err
		}
		return b.updateTicket(ctx, task, message, identity, reporter, services.VersionedTicketUpdateInput{
			TicketID:        command.TicketID,
			ExpectedVersion: command.ExpectedVersion,
			LeaseID:         command.LeaseID,
			RequiredScope:   models.ScopeTicketsAssign,
			Action:          "ticket.assign",
			Changes:         changes,
			EventType:       "io.chronodesk.ticket.assigned.v1",
			EventData: map[string]any{
				"a2a_task_id":    task.ID,
				"a2a_context_id": task.ContextID,
				"assignee":       command.Assignee,
				"reason":         command.Reason,
			},
			IsRisky: true,
		}, "assign")
	default:
		return requireA2AFields("operation: claim, release, update, transition, or assign")
	}
}

func (b *A2ABackend) updateTicket(
	ctx context.Context,
	task a2a.Task,
	message a2a.Message,
	identity A2AExecutionIdentity,
	reporter a2a.Reporter,
	input services.VersionedTicketUpdateInput,
	operation string,
) error {
	input.Actor = identity.Actor
	input.CompatibilityUserID = identity.CompatibilityUserID
	input.CredentialID = identity.CredentialID
	input.SourceProtocol = a2aSourceProtocol
	input.TraceID = task.ID
	input.CorrelationID = task.ContextID
	input.CausationID = message.MessageID
	reservation := a2aReservationFromContext(ctx)
	input.RequestDigest = reservation.RequestDigest
	input.IdempotencyRecordID = reservation.ID
	result, err := b.native.UpdateTicketVersion(ctx, input)
	if err != nil {
		return err
	}
	return reportA2AResult(ctx, reporter, task, "ticket-work", map[string]any{
		"operation": operation,
		"ticket":    result.Ticket.ToResponse(),
		"receipt":   result.Receipt,
	})
}

func (b *A2ABackend) assignmentChanges(ctx context.Context, assignee models.ActorRef) (map[string]any, error) {
	if err := assignee.Validate(); err != nil {
		return nil, requireA2AFields("assignee.type", "assignee.id")
	}
	changes := map[string]any{
		"assigned_to_actor_type": assignee.Type,
		"assigned_to_actor_id":   assignee.ID,
	}
	switch assignee.Type {
	case models.ActorTypeHuman:
		userID, err := strconv.ParseUint(assignee.ID, 10, 64)
		if err != nil || userID == 0 {
			return nil, requireA2AFields("numeric human assignee.id")
		}
		changes["assigned_to_id"] = uint(userID)
		changes["assigned_to_service_principal_id"] = nil
	case models.ActorTypeServicePrincipal:
		var principal models.ServicePrincipal
		if err := b.db.WithContext(ctx).
			Select("id", "compatibility_user_id").
			First(&principal, "id = ?", assignee.ID).Error; err != nil {
			return nil, err
		}
		if principal.CompatibilityUserID == nil || *principal.CompatibilityUserID == 0 {
			return nil, requireA2AFields("assignee compatibility user")
		}
		changes["assigned_to_id"] = *principal.CompatibilityUserID
		changes["assigned_to_service_principal_id"] = principal.ID
	default:
		return nil, requireA2AFields("human or service_principal assignee")
	}
	return changes, nil
}

type ticketCommentCommand struct {
	TicketID        uint               `json:"ticket_id"`
	ExpectedVersion uint64             `json:"expected_version"`
	LeaseID         string             `json:"lease_id,omitempty"`
	Content         string             `json:"content"`
	ContentType     string             `json:"content_type,omitempty"`
	Type            models.CommentType `json:"type,omitempty"`
	ParentID        *uint              `json:"parent_id,omitempty"`
	AttachmentIDs   []string           `json:"attachment_ids,omitempty"`
	TimeSpent       *int               `json:"time_spent,omitempty"`
	BillableTime    *int               `json:"billable_time,omitempty"`
	WorkType        string             `json:"work_type,omitempty"`
	Reason          string             `json:"reason,omitempty"`
	EvidenceRefs    []string           `json:"evidence_refs,omitempty"`
	InputSources    []string           `json:"input_sources,omitempty"`
}

func (b *A2ABackend) ticketComment(
	ctx context.Context,
	task a2a.Task,
	message a2a.Message,
	identity A2AExecutionIdentity,
	payload map[string]any,
	reporter a2a.Reporter,
) error {
	var command ticketCommentCommand
	if err := decodeA2ACommand(payload, &command); err != nil ||
		command.TicketID == 0 ||
		command.ExpectedVersion == 0 ||
		strings.TrimSpace(command.LeaseID) == "" ||
		strings.TrimSpace(command.Content) == "" {
		return requireA2AFields("ticket_id", "expected_version", "lease_id", "content")
	}
	command.LeaseID = strings.TrimSpace(command.LeaseID)
	if command.Type == "" {
		command.Type = models.CommentTypeInternal
	}
	if command.ContentType == "" {
		command.ContentType = "text"
	}
	reservation := a2aReservationFromContext(ctx)
	result, err := b.native.CreateComment(ctx, services.NativeCommentInput{
		TicketID:            command.TicketID,
		ExpectedVersion:     command.ExpectedVersion,
		LeaseID:             command.LeaseID,
		Actor:               identity.Actor,
		CompatibilityUserID: identity.CompatibilityUserID,
		CredentialID:        identity.CredentialID,
		SourceProtocol:      a2aSourceProtocol,
		RequestDigest:       reservation.RequestDigest,
		Content:             command.Content,
		ContentType:         command.ContentType,
		Type:                command.Type,
		ParentID:            command.ParentID,
		AttachmentIDs:       command.AttachmentIDs,
		TimeSpent:           command.TimeSpent,
		BillableTime:        command.BillableTime,
		WorkType:            command.WorkType,
		Reason:              command.Reason,
		EvidenceRefs:        command.EvidenceRefs,
		InputSources:        command.InputSources,
		TraceID:             task.ID,
		CorrelationID:       task.ContextID,
		IdempotencyRecordID: reservation.ID,
	})
	if err != nil {
		return err
	}
	return reportA2AResult(ctx, reporter, task, "ticket-comment", map[string]any{
		"comment":              result.Comment.ToResponse(),
		"receipt":              result.Receipt,
		"caused_by_message_id": message.MessageID,
	})
}

type ticketEscalationCommand struct {
	TicketID        uint                  `json:"ticket_id"`
	ExpectedVersion uint64                `json:"expected_version"`
	LeaseID         string                `json:"lease_id"`
	Reason          string                `json:"reason"`
	Priority        models.TicketPriority `json:"priority,omitempty"`
}

func (b *A2ABackend) ticketEscalation(
	ctx context.Context,
	task a2a.Task,
	message a2a.Message,
	identity A2AExecutionIdentity,
	payload map[string]any,
	reporter a2a.Reporter,
) error {
	var command ticketEscalationCommand
	if err := decodeA2ACommand(payload, &command); err != nil ||
		command.TicketID == 0 ||
		command.ExpectedVersion == 0 ||
		command.LeaseID == "" ||
		strings.TrimSpace(command.Reason) == "" {
		return requireA2AFields("ticket_id", "expected_version", "lease_id", "reason")
	}
	changes := map[string]any{"is_escalated": true}
	if command.Priority != "" {
		if !command.Priority.IsValid() {
			return requireA2AFields("valid priority")
		}
		changes["priority"] = command.Priority
	}
	reservation := a2aReservationFromContext(ctx)
	result, err := b.native.UpdateTicketVersion(ctx, services.VersionedTicketUpdateInput{
		TicketID:            command.TicketID,
		ExpectedVersion:     command.ExpectedVersion,
		LeaseID:             command.LeaseID,
		Actor:               identity.Actor,
		CompatibilityUserID: identity.CompatibilityUserID,
		CredentialID:        identity.CredentialID,
		RequiredScope:       models.ScopeTicketsTransition,
		Action:              "ticket.escalate",
		SourceProtocol:      a2aSourceProtocol,
		RequestDigest:       reservation.RequestDigest,
		Changes:             changes,
		EventType:           "io.chronodesk.ticket.escalated.v1",
		EventData: map[string]any{
			"a2a_task_id":    task.ID,
			"a2a_context_id": task.ContextID,
			"reason":         command.Reason,
			"priority":       command.Priority,
		},
		TraceID:             task.ID,
		CorrelationID:       task.ContextID,
		CausationID:         message.MessageID,
		IsRisky:             true,
		IdempotencyRecordID: reservation.ID,
	})
	if err != nil {
		return err
	}
	return reportA2AResult(ctx, reporter, task, "ticket-escalation", map[string]any{
		"ticket":  result.Ticket.ToResponse(),
		"receipt": result.Receipt,
	})
}

func (b *A2ABackend) checkPolicy(
	ctx context.Context,
	task a2a.Task,
	identity A2AExecutionIdentity,
	scope string,
	action string,
	ticketID uint,
	isWrite bool,
	isRisky bool,
) (string, error) {
	if identity.Actor.Type != models.ActorTypeServicePrincipal {
		return "", nil
	}
	decision, err := b.native.CheckAction(ctx, services.PolicyCheckInput{
		ServicePrincipalID: identity.Actor.ID,
		CredentialID:       identity.CredentialID,
		Scope:              scope,
		Action:             action,
		ResourceType:       "ticket",
		ResourceID:         strconv.FormatUint(uint64(ticketID), 10),
		IsWrite:            isWrite,
		IsRisky:            isRisky,
		SourceProtocol:     a2aSourceProtocol,
		Context: map[string]any{
			"a2a_task_id":    task.ID,
			"a2a_context_id": task.ContextID,
		},
	})
	if err != nil {
		return "", err
	}
	return decision.ID, nil
}

func (b *A2ABackend) authorizeA2AReplay(
	ctx context.Context,
	task a2a.Task,
	identity A2AExecutionIdentity,
	skill string,
	payload map[string]any,
) error {
	if identity.Actor.Type != models.ActorTypeServicePrincipal {
		return nil
	}
	var resource struct {
		TicketID uint `json:"ticket_id"`
	}
	encoded, _ := json.Marshal(payload)
	_ = json.Unmarshal(encoded, &resource)
	scope := models.ScopeTicketsRead
	action := "ticket.read"
	risky := false
	switch skill {
	case "ticket-intake":
		scope, action = models.ScopeTicketsCreate, "ticket.create"
	case "ticket-comment":
		scope, action = models.ScopeCommentsWrite, "ticket.comment.create"
	case "ticket-escalation":
		scope, action, risky = models.ScopeTicketsTransition, "ticket.escalate", true
	case "ticket-work":
		operation, _ := a2aSkillOperation(skill, payload)
		switch operation {
		case "claim":
			scope, action = models.ScopeTasksManage, "ticket.claim"
		case "release":
			scope, action = models.ScopeTasksManage, "ticket.lease.release"
		case "transition":
			scope, action, risky = models.ScopeTicketsTransition, "ticket.transition", true
		case "assign":
			scope, action, risky = models.ScopeTicketsAssign, "ticket.assign", true
		default:
			scope, action = models.ScopeTicketsUpdate, "ticket.update"
		}
	}
	_, err := b.native.CheckAction(ctx, services.PolicyCheckInput{
		ServicePrincipalID: identity.Actor.ID,
		CredentialID:       identity.CredentialID,
		Scope:              scope,
		Action:             action,
		ResourceType:       "ticket",
		ResourceID:         strconv.FormatUint(uint64(resource.TicketID), 10),
		IsWrite:            true,
		IsRisky:            risky,
		SourceProtocol:     a2aSourceProtocol,
		Context: map[string]any{
			"a2a_task_id":       task.ID,
			"a2a_context_id":    task.ContextID,
			"idempotent_replay": true,
		},
	})
	return err
}

func reportA2AIdempotentReplay(
	ctx context.Context,
	reporter a2a.Reporter,
	task a2a.Task,
	skill string,
	record *models.IdempotencyRecord,
) error {
	if record == nil || len(record.ResponseBody) == 0 {
		return services.ErrIdempotencyConflict
	}
	var receipt services.OperationReceipt
	if err := json.Unmarshal(record.ResponseBody, &receipt); err != nil {
		return err
	}
	result := map[string]any{
		"replayed": true,
		"receipt":  receipt,
	}
	if len(record.ResourceSnapshot) > 0 {
		var snapshot any
		if err := json.Unmarshal(record.ResourceSnapshot, &snapshot); err != nil {
			return err
		}
		result["resource"] = snapshot
	}
	return reportA2AResult(ctx, reporter, task, skill, result)
}

func (b *A2ABackend) reportDomainError(ctx context.Context, reporter a2a.Reporter, err error) error {
	switch {
	case errors.Is(err, services.ErrInvalidCredential),
		errors.Is(err, services.ErrCredentialExpired),
		errors.Is(err, services.ErrPrincipalNotFound),
		errors.Is(err, services.ErrPrincipalDisabled),
		errors.Is(err, services.ErrPrincipalExpired):
		return reportA2AState(ctx, reporter, a2a.TaskStateAuthRequired, "authentication_required", nil)
	case errors.Is(err, services.ErrPolicyDenied),
		errors.Is(err, services.ErrInvalidScope),
		errors.Is(err, services.ErrGlobalEmergencyStop),
		errors.Is(err, services.ErrReadOnlyMode):
		return reportA2AState(ctx, reporter, a2a.TaskStateRejected, services.AgentNativeErrorCode(err), nil)
	case errors.Is(err, services.ErrVersionConflict):
		return reportA2AState(ctx, reporter, a2a.TaskStateInputRequired, "version_conflict", []string{"fresh expected_version"})
	case errors.Is(err, services.ErrLeaseConflict),
		errors.Is(err, services.ErrLeaseExpired),
		errors.Is(err, services.ErrLeaseNotOwned):
		return reportA2AState(ctx, reporter, a2a.TaskStateInputRequired, services.AgentNativeErrorCode(err), []string{"valid lease_id"})
	case errors.Is(err, services.ErrInvalidTicketTransition):
		return reportA2AState(ctx, reporter, a2a.TaskStateInputRequired, "invalid_ticket_transition", []string{"valid status transition"})
	case errors.Is(err, services.ErrRateLimited),
		errors.Is(err, services.ErrConcurrencyLimit),
		errors.Is(err, services.ErrAutomationLoop):
		return reportA2AState(ctx, reporter, a2a.TaskStateFailed, services.AgentNativeErrorCode(err), nil)
	case errors.Is(err, services.ErrExecutionGuardUnavailable):
		return reportA2ARetryableFailure(ctx, reporter, "service_unavailable")
	case errors.Is(err, gorm.ErrRecordNotFound):
		return reportA2AState(ctx, reporter, a2a.TaskStateFailed, "not_found", nil)
	default:
		return err
	}
}

func reportA2ARetryableFailure(
	ctx context.Context,
	reporter a2a.Reporter,
	code string,
) error {
	data, err := json.Marshal(map[string]any{
		"code":      code,
		"retryable": true,
	})
	if err != nil {
		return err
	}
	message := &a2a.Message{
		Role: a2a.RoleAgent,
		Parts: []a2a.Part{{
			Data:      json.RawMessage(data),
			MediaType: "application/json",
			Metadata:  map[string]any{"untrustedContent": false},
		}},
	}
	return reporter.SetStatus(
		ctx,
		a2a.TaskStateFailed,
		message,
		map[string]any{"code": code, "retryable": true},
	)
}

func reportA2AState(
	ctx context.Context,
	reporter a2a.Reporter,
	state a2a.TaskState,
	code string,
	required []string,
) error {
	data, err := json.Marshal(map[string]any{
		"code":           code,
		"requiredFields": required,
	})
	if err != nil {
		return err
	}
	message := &a2a.Message{
		Role: a2a.RoleAgent,
		Parts: []a2a.Part{{
			Data:      json.RawMessage(data),
			MediaType: "application/json",
			Metadata:  map[string]any{"untrustedContent": false},
		}},
	}
	return reporter.SetStatus(ctx, state, message, map[string]any{"code": code})
}

func reportA2AResult(
	ctx context.Context,
	reporter a2a.Reporter,
	task a2a.Task,
	skill string,
	result any,
) error {
	data, err := json.Marshal(map[string]any{
		"skill":     skill,
		"taskId":    task.ID,
		"contextId": task.ContextID,
		"result":    result,
		"trust":     "untrusted-domain-content",
	})
	if err != nil {
		return err
	}
	return reporter.AddArtifact(ctx, a2a.Artifact{
		ArtifactID: skill + "-" + task.ID,
		Name:       skill + " result",
		Parts: []a2a.Part{{
			Data:      json.RawMessage(data),
			MediaType: "application/json",
			Metadata:  map[string]any{"untrustedContent": true},
		}},
		Metadata: map[string]any{
			"a2aTaskId":        task.ID,
			"a2aContextId":     task.ContextID,
			"untrustedContent": true,
		},
	}, false, true, map[string]any{"skill": skill})
}

func decodeA2ACommand(payload map[string]any, target any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func cloneA2AMap(source map[string]any) map[string]any {
	target := make(map[string]any, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

// A2AOutboxPushDispatcher persists A2A push work as a CloudEvent plus Outbox
// delivery. It never performs an HTTP request.
type A2AOutboxPushDispatcher struct {
	db          *gorm.DB
	native      *services.AgentNativeService
	actor       models.ActorRef
	maxAttempts int
}

func NewA2AOutboxPushDispatcher(
	db *gorm.DB,
	native *services.AgentNativeService,
	maxAttempts int,
) (*A2AOutboxPushDispatcher, error) {
	if db == nil || native == nil {
		return nil, errors.New("A2A push dispatcher requires database and AgentNativeService")
	}
	if maxAttempts <= 0 {
		maxAttempts = 8
	}
	return &A2AOutboxPushDispatcher{
		db:          db,
		native:      native,
		actor:       models.SystemActor("a2a-push-dispatcher"),
		maxAttempts: maxAttempts,
	}, nil
}

func (d *A2AOutboxPushDispatcher) Enqueue(
	ctx context.Context,
	config a2a.PushNotificationConfig,
	event a2a.StoredEvent,
) error {
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return d.EnqueueTx(ctx, tx, config, event)
	})
}

func (d *A2AOutboxPushDispatcher) EnqueueTx(
	ctx context.Context,
	tx *gorm.DB,
	config a2a.PushNotificationConfig,
	event a2a.StoredEvent,
) error {
	if tx == nil {
		return errors.New("A2A push transaction is required")
	}
	eventID := stableA2APushEventID(event.TaskID, event.Cursor, config.ID)
	var existing int64
	if err := tx.WithContext(ctx).
		Model(&models.DomainEvent{}).
		Where("id = ?", eventID).
		Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	resourceVersion := event.ResourceVersion
	if resourceVersion == 0 && event.Payload.Task != nil && event.Payload.Task.Version > 0 {
		resourceVersion = event.Payload.Task.Version
	}
	if resourceVersion == 0 {
		resourceVersion = 1
	}
	_, err := d.native.AppendDomainEventTx(ctx, tx, services.DomainEventInput{
		ID:              eventID,
		Type:            "io.chronodesk.a2a.task.updated.v1",
		Subject:         "a2a/task/" + event.TaskID,
		Actor:           d.actor,
		ResourceVersion: resourceVersion,
		TraceID:         event.TaskID,
		CorrelationID:   event.ContextID,
		CausationID:     event.Cursor,
		Data: map[string]any{
			"a2a_task_id":      event.TaskID,
			"a2a_context_id":   event.ContextID,
			"a2a_event_cursor": event.Cursor,
			"push_config_id":   config.ID,
			"callback_url":     config.URL,
			"stream_response":  event.Payload,
			"contains_secrets": false,
		},
	}, []services.OutboxTarget{{
		Type:        "a2a_push",
		ID:          config.ID,
		MaxAttempts: d.maxAttempts,
	}})
	return err
}

func stableA2APushEventID(taskID, cursor, pushConfigID string) string {
	sum := sha256.Sum256([]byte(taskID + "\x00" + cursor + "\x00" + pushConfigID))
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
