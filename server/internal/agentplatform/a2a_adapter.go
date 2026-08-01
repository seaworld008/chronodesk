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

	"github.com/seaworld008/chronodesk/server/internal/a2a"
	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm/clause"
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
	Actor        models.ActorRef
	CredentialID string
	ProjectKey   string
	Scope        models.ProjectScope
	TokenScopes  []string
}

type a2aIdentityContextKey struct{}

func WithA2AExecutionIdentity(ctx context.Context, identity A2AExecutionIdentity) context.Context {
	return context.WithValue(
		ctx,
		a2aIdentityContextKey{},
		cloneA2AExecutionIdentity(identity),
	)
}

func A2AExecutionIdentityFromContext(ctx context.Context) (A2AExecutionIdentity, bool) {
	identity, ok := ctx.Value(a2aIdentityContextKey{}).(A2AExecutionIdentity)
	if !ok {
		return A2AExecutionIdentity{}, false
	}
	return cloneA2AExecutionIdentity(identity), true
}

func cloneA2AExecutionIdentity(
	identity A2AExecutionIdentity,
) A2AExecutionIdentity {
	identity.TokenScopes = append([]string(nil), identity.TokenScopes...)
	return identity
}

// BindA2AIdentity is retained as a fail-closed compatibility shim while the
// application composition root migrates to BindA2AIdentityWithProject.
func BindA2AIdentity() gin.HandlerFunc {
	return BindA2AIdentityWithProject(nil)
}

// BindA2AIdentityWithProject resolves the OAuth project_key through the live
// service-principal grant and installs one trusted A2A OperationContext.
// Message metadata and the A2A tenant compatibility field never construct or
// override this scope. Mount it after agentauth.Middleware.
func BindA2AIdentityWithProject(
	projectService *services.ProjectService,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if projectService == nil {
			WriteProblem(c, http.StatusServiceUnavailable, ProblemInternal, "A2A project service is unavailable", true)
			c.Abort()
			return
		}
		principalID := strings.TrimSpace(c.GetString(agentauth.ContextPrincipalID))
		credentialID := strings.TrimSpace(c.GetString(agentauth.ContextCredentialID))
		projectKey := strings.TrimSpace(c.GetString(agentauth.ContextProjectKey))
		if principalID == "" || credentialID == "" || projectKey == "" {
			WriteProblem(c, 401, ProblemUnauthorized, "Verified A2A principal is missing", false)
			c.Abort()
			return
		}
		tokenScopes, err := verifiedA2ATokenScopes(c)
		if err != nil {
			WriteProblem(c, http.StatusUnauthorized, ProblemUnauthorized, "Verified A2A token scopes are invalid", false)
			c.Abort()
			return
		}
		projectAccess, err := projectService.ResolvePrincipalProject(
			c.Request.Context(),
			projectKey,
			principalID,
			models.ScopeTasksManage,
		)
		if err != nil {
			WriteProblem(c, http.StatusForbidden, ProblemPolicyDenied, "Project access is denied", false)
			c.Abort()
			return
		}
		identity := A2AExecutionIdentity{
			Actor:        models.ServicePrincipalActor(principalID),
			CredentialID: credentialID,
			ProjectKey:   projectKey,
			Scope:        projectAccess.Scope,
			TokenScopes:  tokenScopes,
		}
		ctx, err := services.WithOperationContext(
			c.Request.Context(),
			services.OperationContext{
				Scope:        identity.Scope,
				Actor:        identity.Actor,
				Source:       services.SourceProtocolA2A,
				CredentialID: identity.CredentialID,
			},
		)
		if err != nil {
			WriteProblem(c, http.StatusUnauthorized, ProblemUnauthorized, "Verified A2A identity is invalid", false)
			c.Abort()
			return
		}
		ctx, err = a2a.WithProjectBinding(ctx, a2a.ProjectBinding{
			ProjectKey: identity.ProjectKey,
			Scope:      identity.Scope,
		})
		if err != nil {
			WriteProblem(c, http.StatusUnauthorized, ProblemUnauthorized, "Verified A2A project is invalid", false)
			c.Abort()
			return
		}
		ctx = WithA2AExecutionIdentity(ctx, identity)
		ctx = a2a.WithTaskOwner(ctx, a2a.TaskOwner{
			ActorType:    string(identity.Actor.Type),
			ActorID:      identity.Actor.ID,
			CredentialID: identity.CredentialID,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func verifiedA2ATokenScopes(c *gin.Context) ([]string, error) {
	if c == nil {
		return nil, errors.New("verified A2A request context is unavailable")
	}
	value, exists := c.Get(agentauth.ContextScopes)
	scopes, ok := value.([]string)
	if !exists || !ok {
		return nil, errors.New("verified A2A token scopes are unavailable")
	}
	snapshot := append([]string(nil), scopes...)
	if err := validateA2ATokenScopes(snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// A2AStreamLimiter adapts the protocol-neutral Redis execution guard to A2A
// long-lived responses. Identity comes only from the verified OAuth request
// context; message metadata and remote IP addresses are never quota keys.
type A2AStreamLimiter struct {
	native      *services.AgentNativeService
	globalLimit int
}

func NewA2AStreamLimiter(
	native *services.AgentNativeService,
	globalLimit int,
) (*A2AStreamLimiter, error) {
	if native == nil || globalLimit <= 0 {
		return nil, errors.New("A2A stream limiter requires Agent service and a positive global limit")
	}
	return &A2AStreamLimiter{native: native, globalLimit: globalLimit}, nil
}

func (l *A2AStreamLimiter) Acquire(ctx context.Context) (func(), error) {
	identity, ok := A2AExecutionIdentityFromContext(ctx)
	if !ok || validateA2AExecutionIdentity(ctx, identity) != nil {
		return nil, a2a.ErrStreamControlUnavailable
	}
	release, err := l.native.AcquireAgentStream(
		ctx,
		a2aSourceProtocol,
		identity.Actor.ID,
		identity.CredentialID,
		l.globalLimit,
	)
	switch {
	case err == nil:
		return release, nil
	case errors.Is(err, services.ErrConcurrencyLimit),
		errors.Is(err, services.ErrRateLimited):
		return nil, a2a.ErrStreamQuotaExceeded
	case errors.Is(err, services.ErrExecutionGuardUnavailable):
		return nil, a2a.ErrStreamControlUnavailable
	default:
		return nil, err
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
		principalID := strings.TrimSpace(
			c.GetString(agentauth.ContextPrincipalID),
		)
		credentialID := strings.TrimSpace(
			c.GetString(agentauth.ContextCredentialID),
		)
		if err := validateA2APolicyOperation(
			c.Request.Context(),
			principalID,
			credentialID,
		); err != nil {
			writeNativeProblem(c, err)
			c.Abort()
			return
		}
		for _, policy := range policies {
			if _, err := native.CheckActionInShortProjectTransactions(
				c.Request.Context(),
				services.PolicyCheckInput{
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
				},
			); err != nil {
				writeNativeProblem(c, err)
				c.Abort()
				return
			}
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Next()
	}
}

func validateA2APolicyOperation(
	ctx context.Context,
	principalID string,
	credentialID string,
) error {
	operation, err := services.OperationContextFromContext(ctx)
	if err != nil {
		return fmt.Errorf("A2A policy requires trusted operation context: %w", err)
	}
	identity, ok := A2AExecutionIdentityFromContext(ctx)
	if !ok {
		return errors.New("trusted A2A policy identity is unavailable")
	}
	if err := validateA2AExecutionIdentity(ctx, identity); err != nil {
		return fmt.Errorf("trusted A2A policy identity is invalid: %w", err)
	}
	if identity.Actor != operation.Actor ||
		identity.Scope != operation.Scope ||
		identity.CredentialID != operation.CredentialID ||
		identity.Actor != models.ServicePrincipalActor(principalID) ||
		identity.CredentialID != credentialID ||
		operation.Source != services.SourceProtocolA2A {
		return errors.New(
			"A2A policy identity does not match trusted operation context",
		)
	}
	return nil
}

type A2AIdentityResolver interface {
	ResolveA2AIdentity(ctx context.Context, task a2a.Task, message a2a.Message) (A2AExecutionIdentity, error)
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
	if err := validateA2AExecutionIdentity(ctx, identity); err != nil {
		return A2AExecutionIdentity{}, err
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
	return cloneA2AExecutionIdentity(r.Identity), nil
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

type deferredA2AReport struct {
	status      *a2a.TaskState
	message     *a2a.Message
	artifact    *a2a.Artifact
	appendParts bool
	lastChunk   bool
	metadata    map[string]any
}

// deferredA2AReporter keeps live task updates behind the Ticket transaction
// boundary. A2A streaming clients can therefore never observe a success
// artifact for a command whose PostgreSQL commit later fails.
type deferredA2AReporter struct {
	postCommit []func(context.Context) error
	reports    []deferredA2AReport
}

func (reporter *deferredA2AReporter) SetStatus(
	_ context.Context,
	state a2a.TaskState,
	message *a2a.Message,
	metadata map[string]any,
) error {
	stateCopy := state
	reporter.reports = append(reporter.reports, deferredA2AReport{
		status:   &stateCopy,
		message:  message,
		metadata: cloneA2AMap(metadata),
	})
	return nil
}

func (reporter *deferredA2AReporter) AddArtifact(
	_ context.Context,
	artifact a2a.Artifact,
	appendParts bool,
	lastChunk bool,
	metadata map[string]any,
) error {
	artifactCopy := artifact
	reporter.reports = append(reporter.reports, deferredA2AReport{
		artifact:    &artifactCopy,
		appendParts: appendParts,
		lastChunk:   lastChunk,
		metadata:    cloneA2AMap(metadata),
	})
	return nil
}

func (reporter *deferredA2AReporter) DeferPostCommit(
	run func(context.Context) error,
) error {
	if reporter == nil || run == nil {
		return errors.New("A2A post-commit callback is required")
	}
	reporter.postCommit = append(reporter.postCommit, run)
	return nil
}

func (reporter *deferredA2AReporter) Discard() {
	if reporter == nil {
		return
	}
	reporter.postCommit = nil
	reporter.reports = nil
}

func (reporter *deferredA2AReporter) Flush(
	ctx context.Context,
	target a2a.Reporter,
) error {
	if reporter == nil || target == nil {
		return errors.New("A2A reporter is unavailable")
	}
	for _, run := range reporter.postCommit {
		if err := run(ctx); err != nil {
			return err
		}
	}
	for _, report := range reporter.reports {
		var err error
		switch {
		case report.status != nil:
			err = target.SetStatus(
				ctx,
				*report.status,
				report.message,
				report.metadata,
			)
		case report.artifact != nil:
			err = target.AddArtifact(
				ctx,
				*report.artifact,
				report.appendParts,
				report.lastChunk,
				report.metadata,
			)
		default:
			err = errors.New("deferred A2A report is invalid")
		}
		if err != nil {
			return err
		}
	}
	return nil
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
	if scopeddb.HasTransaction(ctx) {
		return errors.New(
			"A2A backend requires a context outside a project transaction",
		)
	}
	identity, err := b.identity.ResolveA2AIdentity(ctx, task, message)
	if err != nil || identity.Actor.Validate() != nil {
		return reportA2AState(ctx, reporter, a2a.TaskStateAuthRequired, "authentication_required", nil)
	}
	ctx, err = bindA2AOperationIdentity(ctx, identity)
	if err != nil {
		return reportA2AState(ctx, reporter, a2a.TaskStateAuthRequired, "project_scope_mismatch", nil)
	}

	if identity.Actor.Type == models.ActorTypeServicePrincipal {
		leaseContext, release, acquireErr :=
			b.native.AcquireAgentExecutionContext(
				ctx,
				identity.Actor.ID,
			)
		if acquireErr != nil {
			return b.reportDomainError(ctx, reporter, acquireErr)
		}
		defer release()
		ctx = leaseContext
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
	deferredReporter := &deferredA2AReporter{}
	outcomeErr := b.processA2ACommand(
		ctx,
		task,
		message,
		identity,
		skill,
		payload,
		deferredReporter,
	)
	if err := deferredReporter.Flush(ctx, reporter); err != nil {
		return err
	}
	return outcomeErr
}

func (b *A2ABackend) processA2ACommand(
	ctx context.Context,
	task a2a.Task,
	message a2a.Message,
	identity A2AExecutionIdentity,
	skill string,
	payload map[string]any,
	reporter a2a.Reporter,
) error {
	if kind, ok := a2aNativeCommandKind(skill, payload); ok {
		if err := services.ValidateNativeCommandTokenScopes(
			kind,
			identity.TokenScopes,
		); err != nil {
			return b.reportDomainError(ctx, reporter, err)
		}
	}
	var (
		reservation a2aCommandReservation
		replayed    *models.IdempotencyRecord
	)
	reserveErr := b.native.RunProjectOperation(
		ctx,
		func(scopedContext context.Context) error {
			if err := b.revalidateA2AExecution(
				scopedContext,
				identity,
			); err != nil {
				return err
			}
			var err error
			reservation, replayed, err = b.reserveA2ACommand(
				scopedContext,
				task,
				message,
				identity,
				skill,
				payload,
			)
			return err
		},
	)
	if reserveErr != nil {
		if errors.Is(reserveErr, services.ErrIdempotencyInProgress) {
			return fmt.Errorf("%w: domain command reservation is still active", a2a.ErrExecutionDeferred)
		}
		return b.reportDomainError(ctx, reporter, reserveErr)
	}
	if replayed != nil {
		if err := b.authorizeA2AReplay(
			ctx,
			task,
			identity,
			skill,
			payload,
			replayed,
		); err != nil {
			return b.reportDomainError(ctx, reporter, err)
		}
		return b.reportA2AIdempotentReplay(
			ctx,
			reporter,
			task,
			identity,
			skill,
			payload,
			replayed,
		)
	}
	if reservation.ID != "" {
		ctx = context.WithValue(ctx, a2aCommandReservationContextKey{}, reservation)
	}

	authorizedContext, authorizeErr := b.authorizeA2ACommand(
		ctx,
		task,
		identity,
		skill,
		payload,
		reservation,
	)
	if authorizeErr != nil {
		b.failA2ACommand(ctx, reservation, authorizeErr)
		return b.reportDomainError(ctx, reporter, authorizeErr)
	}
	if skill == "ticket-intake" {
		commandErr := b.ticketIntake(
			authorizedContext,
			task,
			message,
			identity,
			payload,
			reporter,
		)
		if commandErr == nil {
			return nil
		}
		if deferred, ok := reporter.(*deferredA2AReporter); ok {
			deferred.Discard()
		}
		b.failA2ACommand(ctx, reservation, commandErr)
		var invalid *a2aCommandInputError
		if errors.As(commandErr, &invalid) {
			return reportA2AState(
				ctx,
				reporter,
				a2a.TaskStateInputRequired,
				"structured_input_required",
				invalid.required,
			)
		}
		return b.reportDomainError(ctx, reporter, commandErr)
	}

	var (
		outcomeErr error
		commandErr error
	)
	transactionErr := b.native.RunProjectOperation(
		authorizedContext,
		func(scopedContext context.Context) error {
			if err := b.revalidateA2AExecution(
				scopedContext,
				identity,
			); err != nil {
				return err
			}
			outcomeErr, commandErr = b.executeA2ACommandScoped(
				scopedContext,
				task,
				message,
				identity,
				skill,
				payload,
				reporter,
			)
			if commandErr != nil && reservation.ID != "" {
				_ = b.native.FailIdempotency(
					scopedContext,
					reservation.ID,
					services.AgentNativeErrorCode(commandErr),
				)
			}
			// Denied PolicyDecision rows and failed idempotency records are
			// durable business outcomes. Domain services use savepoints for
			// command-level rollback.
			return nil
		},
	)
	if transactionErr != nil {
		if deferred, ok := reporter.(*deferredA2AReporter); ok {
			deferred.Discard()
		}
		b.failA2ACommand(ctx, reservation, transactionErr)
		return b.reportDomainError(ctx, reporter, transactionErr)
	}
	return outcomeErr
}

func (b *A2ABackend) executeA2ACommandScoped(
	ctx context.Context,
	task a2a.Task,
	message a2a.Message,
	identity A2AExecutionIdentity,
	skill string,
	payload map[string]any,
	reporter a2a.Reporter,
) (outcomeErr error, commandErr error) {
	var err error
	switch skill {
	case "ticket-query":
		err = b.ticketQuery(ctx, task, message, identity, payload, reporter)
	case "ticket-work":
		err = b.ticketWork(ctx, task, message, identity, payload, reporter)
	case "ticket-comment":
		err = b.ticketComment(ctx, task, message, identity, payload, reporter)
	case "ticket-escalation":
		err = b.ticketEscalation(ctx, task, message, identity, payload, reporter)
	default:
		return reportA2AState(
			ctx,
			reporter,
			a2a.TaskStateRejected,
			"unsupported_skill",
			nil,
		), nil
	}
	if err == nil {
		return nil, nil
	}
	var invalid *a2aCommandInputError
	if errors.As(err, &invalid) {
		return reportA2AState(
			ctx,
			reporter,
			a2a.TaskStateInputRequired,
			"structured_input_required",
			invalid.required,
		), err
	}
	return b.reportDomainError(ctx, reporter, err), err
}

func (b *A2ABackend) authorizeA2ACommand(
	ctx context.Context,
	task a2a.Task,
	identity A2AExecutionIdentity,
	skill string,
	payload map[string]any,
	reservation a2aCommandReservation,
) (context.Context, error) {
	if identity.Actor.Type != models.ActorTypeServicePrincipal {
		return ctx, nil
	}
	command, ok := a2aNativeCommandAuthorizationInput(
		task,
		identity,
		skill,
		payload,
		reservation,
	)
	if !ok {
		return b.native.RequirePolicyDecisionAuthorizations(ctx)
	}
	return b.native.AuthorizeNativeCommandInShortProjectTransactions(
		ctx,
		command,
	)
}

func a2aNativeCommandAuthorizationInput(
	task a2a.Task,
	identity A2AExecutionIdentity,
	skill string,
	payload map[string]any,
	reservation a2aCommandReservation,
) (services.NativeCommandAuthorizationInput, bool) {
	if !a2aCommandReadyForAuthorization(skill, payload) {
		return services.NativeCommandAuthorizationInput{}, false
	}
	kind, ok := a2aNativeCommandKind(skill, payload)
	if !ok {
		return services.NativeCommandAuthorizationInput{}, false
	}
	ticketID, _ := a2aTicketIDValue(payload["ticket_id"])
	command := services.NativeCommandAuthorizationInput{
		Kind:           kind,
		Actor:          identity.Actor,
		CredentialID:   identity.CredentialID,
		TokenScopes:    append([]string(nil), identity.TokenScopes...),
		TicketID:       ticketID,
		RequestDigest:  reservation.RequestDigest,
		SourceProtocol: a2aSourceProtocol,
	}
	switch skill {
	case "ticket-query":
		command.DecisionContext = map[string]any{
			"a2a_task_id":    task.ID,
			"a2a_context_id": task.ContextID,
		}
	case "ticket-work":
		var work ticketWorkCommand
		if decodeA2ACommand(payload, &work) != nil {
			return services.NativeCommandAuthorizationInput{}, false
		}
		command.LeaseID = work.LeaseID
		command.Assignee = work.Assignee
	}
	return command, true
}

func a2aNativeCommandKind(
	skill string,
	payload map[string]any,
) (services.NativeCommandAuthorizationKind, bool) {
	switch skill {
	case "ticket-intake":
		return services.NativeCommandTicketCreate, true
	case "ticket-query":
		return services.NativeCommandTicketQuery, true
	case "ticket-comment":
		return services.NativeCommandCommentCreate, true
	case "ticket-escalation":
		return services.NativeCommandTicketEscalate, true
	case "ticket-work":
		operation := strings.ToLower(
			strings.TrimSpace(fmt.Sprint(payload["operation"])),
		)
		switch operation {
		case "claim":
			return services.NativeCommandTicketClaim, true
		case "release":
			return services.NativeCommandLeaseRelease, true
		case "update":
			return services.NativeCommandTicketUpdate, true
		case "transition":
			return services.NativeCommandTicketTransit, true
		case "assign":
			return services.NativeCommandTicketAssign, true
		default:
			return "", false
		}
	default:
		return "", false
	}
}

func a2aCommandReadyForAuthorization(
	skill string,
	payload map[string]any,
) bool {
	switch skill {
	case "ticket-intake":
		var command ticketIntakeCommand
		if decodeA2ACommand(payload, &command) != nil ||
			strings.TrimSpace(command.Title) == "" ||
			strings.TrimSpace(command.Description) == "" ||
			!command.Type.IsValid() ||
			!command.Priority.IsValid() {
			return false
		}
		_, requestTypeValid :=
			normalizeMachineConfigurationVersionID(
				command.RequestTypeVersionID,
			)
		_, workflowValid :=
			normalizeMachineConfigurationVersionID(
				command.WorkflowVersionID,
			)
		return requestTypeValid && workflowValid
	case "ticket-query":
		var command ticketQueryCommand
		return decodeA2ACommand(payload, &command) == nil &&
			command.TicketID != 0
	case "ticket-comment":
		var command ticketCommentCommand
		return decodeA2ACommand(payload, &command) == nil &&
			command.TicketID != 0 &&
			command.ExpectedVersion != 0 &&
			strings.TrimSpace(command.LeaseID) != "" &&
			strings.TrimSpace(command.Content) != ""
	case "ticket-escalation":
		var command ticketEscalationCommand
		return decodeA2ACommand(payload, &command) == nil &&
			command.TicketID != 0 &&
			command.ExpectedVersion != 0 &&
			strings.TrimSpace(command.LeaseID) != "" &&
			strings.TrimSpace(command.Reason) != "" &&
			(command.Priority == "" || command.Priority.IsValid())
	case "ticket-work":
		var command ticketWorkCommand
		if decodeA2ACommand(payload, &command) != nil ||
			command.TicketID == 0 {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(command.Operation)) {
		case "claim":
			return command.ExpectedVersion != 0
		case "release":
			return strings.TrimSpace(command.LeaseID) != ""
		case "update":
			return command.ExpectedVersion != 0 &&
				strings.TrimSpace(command.LeaseID) != "" &&
				len(command.Changes) != 0
		case "transition":
			return command.ExpectedVersion != 0 &&
				strings.TrimSpace(command.LeaseID) != "" &&
				command.Status.IsValid()
		case "assign":
			return command.ExpectedVersion != 0 &&
				strings.TrimSpace(command.LeaseID) != "" &&
				command.Assignee != nil
		default:
			return false
		}
	default:
		return false
	}
}

func (b *A2ABackend) failA2ACommand(
	ctx context.Context,
	reservation a2aCommandReservation,
	outcomeErr error,
) {
	if reservation.ID == "" {
		return
	}
	operation, err := services.OperationContextFromContext(ctx)
	if err != nil ||
		operation.Actor.Type != models.ActorTypeServicePrincipal {
		return
	}
	_ = b.native.FinalizeRevokedActorIdempotency(
		ctx,
		operation.Scope,
		operation.Actor,
		reservation.ID,
		services.AgentNativeErrorCode(outcomeErr),
	)
}

func (b *A2ABackend) revalidateA2AExecution(
	ctx context.Context,
	identity A2AExecutionIdentity,
) error {
	if b == nil || b.native == nil {
		return errors.New("A2A project authorization is unavailable")
	}
	if identity.Actor.Type != models.ActorTypeServicePrincipal {
		return services.ErrInvalidActor
	}
	access, err := b.native.RevalidatePrincipalProjectOperation(
		ctx,
		models.ScopeTasksManage,
	)
	if err != nil {
		return err
	}
	if access.Scope != identity.Scope ||
		access.Project.Key != models.ProjectKey(identity.ProjectKey) {
		return services.ErrProjectAccessDenied
	}
	return nil
}

func validateA2AExecutionIdentity(
	ctx context.Context,
	identity A2AExecutionIdentity,
) error {
	if err := identity.Actor.Validate(); err != nil {
		return err
	}
	if identity.Actor.Type != models.ActorTypeServicePrincipal ||
		strings.TrimSpace(identity.CredentialID) == "" ||
		models.ValidateProjectKey(identity.ProjectKey) != nil ||
		identity.Scope.Validate() != nil {
		return errors.New("trusted A2A identity is incomplete")
	}
	if err := validateA2ATokenScopes(identity.TokenScopes); err != nil {
		return fmt.Errorf("trusted A2A token scopes are invalid: %w", err)
	}
	operation, err := services.OperationContextFromContext(ctx)
	if err != nil {
		return err
	}
	if operation.Actor != identity.Actor ||
		operation.CredentialID != identity.CredentialID ||
		operation.Source != services.SourceProtocolA2A ||
		operation.Scope != identity.Scope {
		return errors.New("trusted A2A operation context does not match identity")
	}
	binding, ok := a2a.ProjectBindingFromContext(ctx)
	if !ok ||
		binding.ProjectKey != identity.ProjectKey ||
		binding.Scope != identity.Scope {
		return errors.New("trusted A2A project binding does not match identity")
	}
	return nil
}

func bindA2AOperationIdentity(
	ctx context.Context,
	identity A2AExecutionIdentity,
) (context.Context, error) {
	if err := identity.Actor.Validate(); err != nil {
		return nil, err
	}
	if identity.Actor.Type != models.ActorTypeServicePrincipal ||
		strings.TrimSpace(identity.CredentialID) == "" ||
		models.ValidateProjectKey(identity.ProjectKey) != nil ||
		identity.Scope.Validate() != nil {
		return nil, errors.New("trusted A2A identity is incomplete")
	}
	if err := validateA2ATokenScopes(identity.TokenScopes); err != nil {
		return nil, fmt.Errorf("trusted A2A token scopes are invalid: %w", err)
	}
	if operation, err := services.OperationContextFromContext(ctx); err == nil {
		if operation.Actor != identity.Actor ||
			operation.CredentialID != identity.CredentialID ||
			operation.Source != services.SourceProtocolA2A ||
			operation.Scope != identity.Scope {
			return nil, errors.New("trusted A2A operation context does not match identity")
		}
	} else {
		var bindErr error
		ctx, bindErr = services.WithOperationContext(ctx, services.OperationContext{
			Scope:        identity.Scope,
			Actor:        identity.Actor,
			Source:       services.SourceProtocolA2A,
			CredentialID: identity.CredentialID,
		})
		if bindErr != nil {
			return nil, bindErr
		}
	}
	if binding, ok := a2a.ProjectBindingFromContext(ctx); ok {
		if binding.ProjectKey != identity.ProjectKey ||
			binding.Scope != identity.Scope {
			return nil, errors.New("existing A2A project binding does not match identity")
		}
	} else {
		var err error
		ctx, err = a2a.WithProjectBinding(ctx, a2a.ProjectBinding{
			ProjectKey: identity.ProjectKey,
			Scope:      identity.Scope,
		})
		if err != nil {
			return nil, err
		}
	}
	ctx = WithA2AExecutionIdentity(ctx, identity)
	ctx = a2a.WithTaskOwner(ctx, a2a.TaskOwner{
		ActorType:    string(identity.Actor.Type),
		ActorID:      identity.Actor.ID,
		CredentialID: identity.CredentialID,
	})
	return ctx, nil
}

func validateA2ATokenScopes(scopes []string) error {
	if len(scopes) == 0 {
		return errors.New("A2A token scopes are missing")
	}
	supported := make(map[string]struct{}, len(models.SupportedAgentScopes))
	for _, scope := range models.SupportedAgentScopes {
		supported[scope] = struct{}{}
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if scope == "" || scope != strings.TrimSpace(scope) {
			return errors.New("A2A token scope is malformed")
		}
		if _, ok := supported[scope]; !ok {
			return fmt.Errorf("A2A token scope %q is unsupported", scope)
		}
		if _, ok := seen[scope]; ok {
			return fmt.Errorf("A2A token scope %q is duplicated", scope)
		}
		seen[scope] = struct{}{}
	}
	if _, ok := seen[models.ScopeTasksManage]; !ok {
		return errors.New("A2A token does not grant tasks:manage")
	}
	return nil
}

func a2aTokenHasScopes(
	identity A2AExecutionIdentity,
	required ...string,
) bool {
	return a2aTokenScopeSnapshotHasScopes(identity.TokenScopes, required...)
}

func a2aTokenScopeSnapshotHasScopes(
	tokenScopes []string,
	required ...string,
) bool {
	if validateA2ATokenScopes(tokenScopes) != nil {
		return false
	}
	granted := make(map[string]struct{}, len(tokenScopes))
	for _, scope := range tokenScopes {
		granted[scope] = struct{}{}
	}
	for _, scope := range required {
		if _, ok := granted[scope]; !ok {
			return false
		}
	}
	return true
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
	Title                string                `json:"title"`
	Description          string                `json:"description"`
	Type                 models.TicketType     `json:"type"`
	Priority             models.TicketPriority `json:"priority"`
	RequestTypeVersionID string                `json:"request_type_version_id"`
	WorkflowVersionID    string                `json:"workflow_version_id"`
	CategoryID           *uint                 `json:"category_id,omitempty"`
	SubcategoryID        *uint                 `json:"subcategory_id,omitempty"`
	Tags                 []string              `json:"tags,omitempty"`
	DueDate              *time.Time            `json:"due_date,omitempty"`
	CustomerEmail        string                `json:"customer_email,omitempty"`
	CustomerPhone        string                `json:"customer_phone,omitempty"`
	CustomerName         string                `json:"customer_name,omitempty"`
	CustomFields         map[string]any        `json:"custom_fields,omitempty"`
	AgentContext         *models.AgentContext  `json:"agent_context,omitempty"`
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
		return requireA2AFields(
			"title",
			"description",
			"type",
			"priority",
			"request_type_version_id",
			"workflow_version_id",
		)
	}
	requestTypeVersionID, validRequestTypeVersion :=
		normalizeMachineConfigurationVersionID(command.RequestTypeVersionID)
	workflowVersionID, validWorkflowVersion :=
		normalizeMachineConfigurationVersionID(command.WorkflowVersionID)
	if !validRequestTypeVersion || !validWorkflowVersion {
		return requireA2AFields(
			"request_type_version_id (canonical UUID)",
			"workflow_version_id (canonical UUID)",
		)
	}
	var customFields *models.JSONMap
	if command.CustomFields != nil {
		value := models.JSONMap(command.CustomFields)
		customFields = &value
	}
	request := models.TicketCreateRequest{
		Title:                command.Title,
		Description:          command.Description,
		Type:                 command.Type,
		Priority:             command.Priority,
		Source:               models.TicketSourceAgent,
		RequestTypeVersionID: requestTypeVersionID,
		WorkflowVersionID:    workflowVersionID,
		CategoryID:           command.CategoryID,
		SubcategoryID:        command.SubcategoryID,
		Tags:                 models.StringList(command.Tags),
		DueDate:              command.DueDate,
		CustomerEmail:        command.CustomerEmail,
		CustomerPhone:        command.CustomerPhone,
		CustomerName:         command.CustomerName,
		CustomFields:         customFields,
		AgentContext:         command.AgentContext,
	}
	reservation := a2aReservationFromContext(ctx)
	result, err := runMachineTicketCreateDatabaseCommand(
		ctx,
		b.db,
		b.native,
		services.NativeTicketCreateInput{
			Request:             request,
			Actor:               identity.Actor,
			CredentialID:        identity.CredentialID,
			SourceProtocol:      a2aSourceProtocol,
			RequestDigest:       reservation.RequestDigest,
			TrustLevel:          models.TicketTrustLevelUntrusted,
			TraceID:             task.ID,
			CorrelationID:       task.ContextID,
			IdempotencyRecordID: reservation.ID,
		},
	)
	if err != nil {
		return err
	}
	return b.deferA2ATicketResult(
		task,
		identity,
		reporter,
		"ticket-intake",
		map[string]any{"receipt": result.Receipt},
		result.Ticket,
	)
}

func (b *A2ABackend) deferA2ATicketResult(
	task a2a.Task,
	identity A2AExecutionIdentity,
	reporter a2a.Reporter,
	skill string,
	response map[string]any,
	ticket *models.Ticket,
) error {
	if ticket == nil {
		return errors.New("A2A Ticket result is unavailable")
	}
	deferred, ok := reporter.(*deferredA2AReporter)
	if !ok {
		return errors.New(
			"A2A Ticket result requires a post-commit reporter",
		)
	}
	baseResponse := cloneA2AMap(response)
	snapshot := ticket.ToResponse()
	ticketID := ticket.ID
	return deferred.DeferPostCommit(func(postCommitContext context.Context) error {
		response := cloneA2AMap(baseResponse)
		if b.mayReturnA2ATicketSnapshot(
			postCommitContext,
			task,
			identity,
			ticketID,
		) {
			response["ticket"] = snapshot
		}
		return reportA2AResult(
			postCommitContext,
			deferred,
			task,
			skill,
			response,
		)
	})
}

func (b *A2ABackend) mayReturnA2ATicketSnapshot(
	ctx context.Context,
	task a2a.Task,
	identity A2AExecutionIdentity,
	ticketID uint,
) bool {
	if identity.Actor.Type != models.ActorTypeServicePrincipal {
		return true
	}
	if ticketID == 0 || !a2aTokenHasScopes(
		identity,
		models.ScopeTasksManage,
		models.ScopeTicketsRead,
	) {
		return false
	}
	_, err := b.native.CheckActionInShortProjectTransactions(
		ctx,
		services.PolicyCheckInput{
			ServicePrincipalID: identity.Actor.ID,
			CredentialID:       identity.CredentialID,
			Scope:              models.ScopeTicketsRead,
			Action:             "ticket.read",
			ResourceType:       "ticket",
			ResourceID:         strconv.FormatUint(uint64(ticketID), 10),
			SourceProtocol:     a2aSourceProtocol,
			Context: map[string]any{
				"a2a_task_id":      task.ID,
				"a2a_context_id":   task.ContextID,
				"response_payload": true,
			},
		},
	)
	// The originating command already committed. A denied or unavailable read
	// policy returns only the operation receipt, never the protected snapshot.
	return err == nil
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
		if err := b.native.ValidateNativeCommandAuthorization(
			ctx,
			services.NativeCommandAuthorizationInput{
				Kind:           services.NativeCommandTicketQuery,
				Actor:          identity.Actor,
				CredentialID:   identity.CredentialID,
				TokenScopes:    append([]string(nil), identity.TokenScopes...),
				TicketID:       command.TicketID,
				SourceProtocol: a2aSourceProtocol,
				DecisionContext: map[string]any{
					"a2a_task_id":    task.ID,
					"a2a_context_id": task.ContextID,
				},
			},
		); err != nil {
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
			"ticket_id": result.Lease.TicketID,
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
		reservation := a2aReservationFromContext(ctx)
		result, err := b.native.TransitionTicket(ctx, services.TransitionTicketCommand{
			TicketID:            command.TicketID,
			ExpectedVersion:     command.ExpectedVersion,
			LeaseID:             command.LeaseID,
			Actor:               identity.Actor,
			Status:              command.Status,
			CredentialID:        identity.CredentialID,
			SourceProtocol:      a2aSourceProtocol,
			RequestDigest:       reservation.RequestDigest,
			Reason:              command.Reason,
			TraceID:             task.ID,
			CorrelationID:       task.ContextID,
			CausationID:         message.MessageID,
			IdempotencyRecordID: reservation.ID,
		})
		if err != nil {
			return err
		}
		return b.deferA2ATicketResult(
			task,
			identity,
			reporter,
			"ticket-work",
			map[string]any{
				"operation": "transition",
				"receipt":   result.Receipt,
			},
			result.Ticket,
		)
	case "assign":
		if command.ExpectedVersion == 0 || command.LeaseID == "" || command.Assignee == nil {
			return requireA2AFields("expected_version", "lease_id", "assignee")
		}
		reservation := a2aReservationFromContext(ctx)
		result, err := b.native.AssignTicket(ctx, services.AssignTicketCommand{
			TicketID:            command.TicketID,
			ExpectedVersion:     command.ExpectedVersion,
			LeaseID:             command.LeaseID,
			Actor:               identity.Actor,
			Assignee:            command.Assignee,
			CredentialID:        identity.CredentialID,
			SourceProtocol:      a2aSourceProtocol,
			RequestDigest:       reservation.RequestDigest,
			Reason:              command.Reason,
			TraceID:             task.ID,
			CorrelationID:       task.ContextID,
			CausationID:         message.MessageID,
			IdempotencyRecordID: reservation.ID,
		})
		if err != nil {
			return err
		}
		return b.deferA2ATicketResult(
			task,
			identity,
			reporter,
			"ticket-work",
			map[string]any{
				"operation": "assign",
				"receipt":   result.Receipt,
			},
			result.Ticket,
		)
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
	return b.deferA2ATicketResult(
		task,
		identity,
		reporter,
		"ticket-work",
		map[string]any{
			"operation": operation,
			"receipt":   result.Receipt,
		},
		result.Ticket,
	)
}

type ticketCommentCommand struct {
	TicketID        uint               `json:"ticket_id"`
	ExpectedVersion uint64             `json:"expected_version"`
	LeaseID         string             `json:"lease_id,omitempty"`
	Content         string             `json:"content"`
	ContentType     string             `json:"content_type,omitempty"`
	Type            models.CommentType `json:"type,omitempty"`
	ParentID        *uint              `json:"parent_id,omitempty"`
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
		CredentialID:        identity.CredentialID,
		SourceProtocol:      a2aSourceProtocol,
		RequestDigest:       reservation.RequestDigest,
		Content:             command.Content,
		ContentType:         command.ContentType,
		Type:                command.Type,
		ParentID:            command.ParentID,
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
	var priority *models.TicketPriority
	if command.Priority != "" {
		if !command.Priority.IsValid() {
			return requireA2AFields("valid priority")
		}
		priority = &command.Priority
	}
	reservation := a2aReservationFromContext(ctx)
	result, err := b.native.EscalateTicket(ctx, services.EscalateTicketCommand{
		TicketID:            command.TicketID,
		ExpectedVersion:     command.ExpectedVersion,
		LeaseID:             command.LeaseID,
		Actor:               identity.Actor,
		Priority:            priority,
		CredentialID:        identity.CredentialID,
		SourceProtocol:      a2aSourceProtocol,
		RequestDigest:       reservation.RequestDigest,
		Reason:              command.Reason,
		TraceID:             task.ID,
		CorrelationID:       task.ContextID,
		CausationID:         message.MessageID,
		IdempotencyRecordID: reservation.ID,
	})
	if err != nil {
		return err
	}
	return b.deferA2ATicketResult(
		task,
		identity,
		reporter,
		"ticket-escalation",
		map[string]any{"receipt": result.Receipt},
		result.Ticket,
	)
}

func (b *A2ABackend) authorizeA2AReplay(
	ctx context.Context,
	task a2a.Task,
	identity A2AExecutionIdentity,
	skill string,
	payload map[string]any,
	record *models.IdempotencyRecord,
) error {
	if identity.Actor.Type != models.ActorTypeServicePrincipal {
		return nil
	}
	command, ok := a2aNativeCommandAuthorizationInput(
		task,
		identity,
		skill,
		payload,
		a2aCommandReservation{},
	)
	if !ok {
		return errors.New("replayed A2A command is no longer structurally valid")
	}
	if command.Kind == services.NativeCommandLeaseRelease {
		ticketID, valid := a2aTicketIDValue(record.ResourceID)
		if !valid {
			return errors.New(
				"replayed lease release is missing its trusted Ticket resource",
			)
		}
		command.TicketID = ticketID
	}
	command.DecisionContext = map[string]any{
		"a2a_task_id":       task.ID,
		"a2a_context_id":    task.ContextID,
		"idempotent_replay": true,
	}
	return b.native.AuthorizeNativeCommandReplayInShortProjectTransactions(
		ctx,
		command,
	)
}

func (b *A2ABackend) reportA2AIdempotentReplay(
	ctx context.Context,
	reporter a2a.Reporter,
	task a2a.Task,
	identity A2AExecutionIdentity,
	skill string,
	payload map[string]any,
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
	var linkedTicketID uint
	if len(record.ResourceSnapshot) > 0 {
		mayReturn, ticketID := b.mayReturnA2AReplaySnapshot(
			ctx,
			task,
			identity,
			skill,
			payload,
			receipt,
		)
		if !mayReturn {
			return reportA2AResult(ctx, reporter, task, skill, result)
		}
		var snapshot any
		if err := json.Unmarshal(record.ResourceSnapshot, &snapshot); err != nil {
			return err
		}
		result["resource"] = snapshot
		if ticketID != 0 {
			if a2aReplaySnapshotIsTicket(skill, payload) {
				result["resourceType"] = "ticket"
				result["receipt"] = a2aReplayReceipt{
					OperationReceipt: receipt,
					ResourceType:     "ticket",
				}
			}
			linkedTicketID = ticketID
		}
	}
	return reportA2AResultWithTicketLink(
		ctx,
		reporter,
		task,
		skill,
		result,
		linkedTicketID,
	)
}

func (b *A2ABackend) mayReturnA2AReplaySnapshot(
	ctx context.Context,
	task a2a.Task,
	identity A2AExecutionIdentity,
	skill string,
	payload map[string]any,
	receipt services.OperationReceipt,
) (bool, uint) {
	ticketID, ok := a2aReplayTicketID(skill, payload, receipt)
	if identity.Actor.Type != models.ActorTypeServicePrincipal {
		return true, ticketID
	}
	if !ok {
		return false, 0
	}
	return b.mayReturnA2ATicketSnapshot(ctx, task, identity, ticketID), ticketID
}

func a2aReplayTicketID(
	skill string,
	payload map[string]any,
	receipt services.OperationReceipt,
) (uint, bool) {
	if skill == "ticket-intake" {
		return a2aTicketIDValue(receipt.ResourceID)
	}
	if skill == "ticket-work" {
		operation, _ := a2aSkillOperation(skill, payload)
		if operation == "release" {
			return a2aTicketIDValue(receipt.ResourceID)
		}
	}
	return a2aTicketIDValue(payload["ticket_id"])
}

type a2aReplayReceipt struct {
	services.OperationReceipt
	ResourceType string `json:"resource_type,omitempty"`
}

func a2aReplaySnapshotIsTicket(skill string, payload map[string]any) bool {
	switch skill {
	case "ticket-intake", "ticket-escalation":
		return true
	case "ticket-work":
		operation := strings.ToLower(strings.TrimSpace(fmt.Sprint(payload["operation"])))
		return operation == "update" || operation == "transition" || operation == "assign"
	default:
		return false
	}
}

func (b *A2ABackend) reportDomainError(ctx context.Context, reporter a2a.Reporter, err error) error {
	switch {
	case errors.Is(err, services.ErrInvalidAssignee):
		return reportA2AState(
			ctx,
			reporter,
			a2a.TaskStateInputRequired,
			services.AgentNativeErrorCode(err),
			[]string{"valid assignee.type and assignee.id"},
		)
	case errors.Is(err, services.ErrAssigneeNotFound):
		return reportA2AState(
			ctx,
			reporter,
			a2a.TaskStateFailed,
			services.AgentNativeErrorCode(err),
			nil,
		)
	case errors.Is(err, services.ErrAssigneePolicyDenied):
		return reportA2AState(
			ctx,
			reporter,
			a2a.TaskStateRejected,
			services.AgentNativeErrorCode(err),
			nil,
		)
	case errors.Is(err, services.ErrInvalidCredential),
		errors.Is(err, services.ErrCredentialExpired),
		errors.Is(err, services.ErrPrincipalNotFound),
		errors.Is(err, services.ErrPrincipalDisabled),
		errors.Is(err, services.ErrPrincipalExpired):
		return reportA2AState(ctx, reporter, a2a.TaskStateAuthRequired, "authentication_required", nil)
	case errors.Is(err, services.ErrPolicyDenied),
		errors.Is(err, services.ErrProjectAccessDenied),
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
	case errors.Is(err, services.ErrInvalidTicketTags):
		return reportA2AState(ctx, reporter, a2a.TaskStateInputRequired, "invalid_request", []string{"at most 20 unique tags of at most 50 characters"})
	case errors.Is(err, services.ErrInvalidAgentContext):
		return reportA2AState(
			ctx,
			reporter,
			a2a.TaskStateInputRequired,
			"invalid_request",
			[]string{"agent_context within documented limits"},
		)
	case errors.Is(err, services.ErrTicketCategoryScope),
		errors.Is(err, services.ErrInvalidTicketCategorySelection):
		return reportA2AState(
			ctx,
			reporter,
			a2a.TaskStateInputRequired,
			"invalid_request",
			[]string{"category and direct subcategory from the authorized project"},
		)
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
	return reportA2AResultWithTicketLink(ctx, reporter, task, skill, result, 0)
}

func reportA2AResultWithTicketLink(
	ctx context.Context,
	reporter a2a.Reporter,
	task a2a.Task,
	skill string,
	result any,
	ticketID uint,
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
	partMetadata := map[string]any{"untrustedContent": true}
	artifactMetadata := map[string]any{
		"a2aTaskId":        task.ID,
		"a2aContextId":     task.ContextID,
		"untrustedContent": true,
	}
	reportMetadata := map[string]any{"skill": skill}
	if ticketID != 0 {
		resource := map[string]any{
			"type": "ticket",
			"id":   ticketID,
		}
		partMetadata["resource"] = resource
		artifactMetadata["resource"] = resource
		reportMetadata["resource"] = resource
	}
	return reporter.AddArtifact(ctx, a2a.Artifact{
		ArtifactID: skill + "-" + task.ID,
		Name:       skill + " result",
		Parts: []a2a.Part{{
			Data:      json.RawMessage(data),
			MediaType: "application/json",
			Metadata:  partMetadata,
		}},
		Metadata: artifactMetadata,
	}, false, true, reportMetadata)
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
	protector   security.Protector
	actor       models.ActorRef
	maxAttempts int
}

type A2AOutboxPushDispatcherOptions struct {
	DB              *gorm.DB
	Native          *services.AgentNativeService
	SecretProtector security.Protector
	MaxAttempts     int
}

func NewA2AOutboxPushDispatcher(
	options A2AOutboxPushDispatcherOptions,
) (*A2AOutboxPushDispatcher, error) {
	if options.DB == nil ||
		options.Native == nil ||
		options.SecretProtector == nil {
		return nil, errors.New(
			"A2A push dispatcher requires database, AgentNativeService and secret protector",
		)
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = 8
	}
	return &A2AOutboxPushDispatcher{
		db:          options.DB,
		native:      options.Native,
		protector:   options.SecretProtector,
		actor:       models.SystemActor("a2a-push-dispatcher"),
		maxAttempts: options.MaxAttempts,
	}, nil
}

func (d *A2AOutboxPushDispatcher) Enqueue(
	ctx context.Context,
	config a2a.PushNotificationConfig,
	event a2a.StoredEvent,
) error {
	operation, err := services.OperationContextFromContext(ctx)
	if err != nil {
		return err
	}
	workerContext, err := d.workerOperationContext(ctx, operation.Scope, event)
	if err != nil {
		return err
	}
	return scopeddb.WithProjectScopeTransaction(
		workerContext,
		d.db,
		operation.Scope,
		func(tx *gorm.DB) error {
			return d.EnqueueTx(workerContext, tx, config, event)
		},
	)
}

func (d *A2AOutboxPushDispatcher) workerOperationContext(
	ctx context.Context,
	scope models.ProjectScope,
	event a2a.StoredEvent,
) (context.Context, error) {
	return services.WithOperationContext(ctx, services.OperationContext{
		Scope:         scope,
		Actor:         d.actor,
		Source:        services.SourceProtocolWorker,
		TraceID:       event.TaskID,
		CorrelationID: event.ContextID,
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
	operation, err := services.OperationContextFromContext(ctx)
	if err != nil {
		return err
	}
	workerContext, err := d.workerOperationContext(ctx, operation.Scope, event)
	if err != nil {
		return err
	}
	scope := operation.Scope
	eventID := stableA2APushEventID(event.TaskID, event.Cursor, config.ID)
	var existing int64
	if err := tx.WithContext(workerContext).
		Model(&models.DomainEvent{}).
		Where(
			"organization_id = ? AND project_id = ? AND id = ?",
			scope.OrganizationID,
			scope.ProjectID,
			eventID,
		).
		Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	var source models.AgentPushNotificationConfig
	if err := tx.WithContext(workerContext).
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where(
			"id = ? AND task_id = ? AND organization_id = ? AND project_id = ?",
			config.ID,
			event.TaskID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		Take(&source).Error; err != nil {
		return fmt.Errorf(
			"load A2A push configuration for snapshot: %w",
			err,
		)
	}
	requestBody, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("encode A2A push request snapshot: %w", err)
	}
	snapshot, err := models.NewA2APushDeliverySnapshot(
		scope,
		eventID,
		event.TaskID,
		source.ID,
		source.UpdatedAt,
		source.URL,
		requestBody,
		"application/a2a+json",
		a2a.ProtocolVersion,
	)
	if err != nil {
		return fmt.Errorf(
			"create A2A push delivery snapshot: %w",
			err,
		)
	}
	snapshot.TokenCiphertext, err = rewrapA2APushSnapshotSecret(
		d.protector,
		source.Token,
		security.FieldAAD(
			"agent_push_notification_configs",
			source.ID,
			"token",
		),
		a2aPushSnapshotSecretAAD(*snapshot, "token"),
		false,
	)
	if err != nil {
		return fmt.Errorf("freeze A2A push token: %w", err)
	}
	var authenticationEnvelope string
	if len(source.Authentication) > 0 &&
		string(source.Authentication) != "null" {
		if err := json.Unmarshal(
			source.Authentication,
			&authenticationEnvelope,
		); err != nil {
			return fmt.Errorf(
				"freeze A2A push authentication: %w",
				security.ErrPlaintextSecret,
			)
		}
	}
	snapshot.AuthenticationCiphertext, err =
		rewrapA2APushSnapshotSecret(
			d.protector,
			authenticationEnvelope,
			security.FieldAAD(
				"agent_push_notification_configs",
				source.ID,
				"authentication",
			),
			a2aPushSnapshotSecretAAD(
				*snapshot,
				"authentication",
			),
			true,
		)
	if err != nil {
		return fmt.Errorf(
			"freeze A2A push authentication: %w",
			err,
		)
	}
	if err := tx.WithContext(workerContext).
		Create(snapshot).Error; err != nil {
		return fmt.Errorf(
			"persist A2A push delivery snapshot: %w",
			err,
		)
	}
	resourceVersion := event.ResourceVersion
	if resourceVersion == 0 && event.Payload.Task != nil && event.Payload.Task.Version > 0 {
		resourceVersion = event.Payload.Task.Version
	}
	if resourceVersion == 0 {
		resourceVersion = 1
	}
	_, err = d.native.AppendDomainEventTx(
		workerContext,
		tx.WithContext(workerContext),
		services.DomainEventInput{
			ID:              eventID,
			Type:            "io.chronodesk.a2a.task.updated.v1",
			Subject:         "a2a/task/" + event.TaskID,
			Actor:           d.actor,
			Scope:           scope,
			ResourceVersion: resourceVersion,
			TraceID:         event.TaskID,
			CorrelationID:   event.ContextID,
			CausationID:     event.Cursor,
			Data: map[string]any{
				"a2a_task_id":          event.TaskID,
				"a2a_context_id":       event.ContextID,
				"a2a_event_cursor":     event.Cursor,
				"push_config_id":       source.ID,
				"push_snapshot_id":     snapshot.ID,
				"stream_response":      event.Payload,
				"contains_secrets":     false,
				"destination_snapshot": true,
			},
		},
		[]services.OutboxTarget{{
			Type: "a2a_push",
			ID: a2aPushSnapshotDestinationPrefix +
				snapshot.ID,
			MaxAttempts: d.maxAttempts,
		}},
	)
	return err
}

const a2aPushSnapshotDestinationPrefix = "snapshot:"

func a2aPushSnapshotSecretAAD(
	snapshot models.A2APushDeliverySnapshot,
	field string,
) []byte {
	compositeID := fmt.Sprintf(
		"organization=%d;project=%d;config_length=%d;config=%s;snapshot=%s",
		snapshot.OrganizationID,
		snapshot.ProjectID,
		len(snapshot.PushConfigID),
		snapshot.PushConfigID,
		snapshot.ID,
	)
	return security.FieldAAD(
		snapshot.TableName(),
		compositeID,
		field,
	)
}

func rewrapA2APushSnapshotSecret(
	protector security.Protector,
	sourceEnvelope string,
	sourceAAD []byte,
	snapshotAAD []byte,
	authentication bool,
) (string, error) {
	if sourceEnvelope == "" {
		return "", nil
	}
	if protector == nil {
		return "", security.ErrKeyringUnavailable
	}
	plaintext, err := protector.Open(sourceEnvelope, sourceAAD)
	if err != nil {
		return "", err
	}
	defer clear(plaintext)
	if authentication {
		var value a2a.AuthenticationInfo
		if err := json.Unmarshal(plaintext, &value); err != nil {
			return "", err
		}
		if strings.ContainsAny(
			value.Scheme+value.Credentials,
			"\r\n",
		) {
			return "", errors.New(
				"A2A push authentication contains invalid characters",
			)
		}
	} else if bytes.ContainsAny(plaintext, "\r\n") {
		return "", errors.New(
			"A2A push token contains invalid characters",
		)
	}
	return protector.Seal(plaintext, snapshotAAD)
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
