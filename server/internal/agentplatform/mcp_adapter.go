package agentplatform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/mcp"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	mcpSourceProtocol          = "mcp"
	mcpDefaultLimit            = 20
	mcpMaximumLimit            = 100
	mcpMaximumCandidateBudget  = 500
	mcpCandidateBudgetMultiple = 5
)

var mcpQueuePattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// MCPAdapter is the protocol-to-domain bridge shared by mcp.Backend,
// mcp.Authenticator, and mcp.Authorizer. It keeps transport concerns out of
// AgentNativeService while reusing that service for every state transition.
type MCPAdapter struct {
	db       *gorm.DB
	service  *services.AgentNativeService
	projects *services.ProjectService
	tokens   *agentauth.Manager
	events   chan mcp.ResourceEvent
}

type mcpPublication struct {
	principal mcp.Principal
	ticketID  uint
	oldQueue  string
	newQueue  string
}

type mcpPublicationBuffer struct {
	items []mcpPublication
}

type mcpPublicationBufferContextKey struct{}

var (
	_ mcp.Backend       = (*MCPAdapter)(nil)
	_ mcp.Authenticator = (*MCPAdapter)(nil)
	_ mcp.Authorizer    = (*MCPAdapter)(nil)
	_ mcp.EventBackend  = (*MCPAdapter)(nil)
)

func NewMCPAdapter(
	db *gorm.DB,
	service *services.AgentNativeService,
	tokens *agentauth.Manager,
) (*MCPAdapter, error) {
	if db == nil {
		return nil, errors.New("MCP adapter database is required")
	}
	if service == nil {
		return nil, errors.New("MCP adapter AgentNativeService is required")
	}
	if tokens == nil {
		return nil, errors.New("MCP adapter token manager is required")
	}
	projects, err := services.NewProjectService(db)
	if err != nil {
		return nil, fmt.Errorf("create MCP project service: %w", err)
	}
	return &MCPAdapter{
		db:       db,
		service:  service,
		projects: projects,
		tokens:   tokens,
		events:   make(chan mcp.ResourceEvent, 256),
	}, nil
}

func (a *MCPAdapter) Events() <-chan mcp.ResourceEvent {
	return a.events
}

func (a *MCPAdapter) Authenticate(ctx context.Context, bearerToken string) (mcp.Principal, error) {
	return a.authenticate(ctx, bearerToken, true)
}

// Revalidate performs the same credential checks as Authenticate without
// turning every subscription heartbeat into two last_used_at writes.
func (a *MCPAdapter) Revalidate(ctx context.Context, bearerToken string) (mcp.Principal, error) {
	return a.authenticate(ctx, bearerToken, false)
}

func (a *MCPAdapter) authenticate(
	ctx context.Context,
	bearerToken string,
	touchLastUsed bool,
) (mcp.Principal, error) {
	access, err := a.tokens.Verify(strings.TrimSpace(bearerToken))
	if err != nil || access == nil || access.PrincipalID == "" || access.CredentialID == "" {
		return mcp.Principal{}, errors.New("invalid agent access token")
	}

	now := time.Now().UTC()
	var principal models.ServicePrincipal
	if err := a.db.WithContext(ctx).First(&principal, "id = ?", access.PrincipalID).Error; err != nil {
		return mcp.Principal{}, errors.New("invalid agent access token")
	}
	if principal.Status != models.ServicePrincipalStatusActive ||
		principal.EmergencyDisabled ||
		(principal.ExpiresAt != nil && !principal.ExpiresAt.After(now)) {
		return mcp.Principal{}, errors.New("invalid agent access token")
	}

	var credential models.AgentCredential
	if err := a.db.WithContext(ctx).
		Where("id = ? AND service_principal_id = ?", access.CredentialID, principal.ID).
		First(&credential).Error; err != nil {
		return mcp.Principal{}, errors.New("invalid agent access token")
	}
	if credential.Status != models.AgentCredentialStatusActive ||
		credential.RevokedAt != nil ||
		!credential.ExpiresAt.After(now) {
		return mcp.Principal{}, errors.New("invalid agent access token")
	}

	projectAccess, err := a.projects.ResolvePrincipalProject(
		ctx,
		access.ProjectKey,
		principal.ID,
	)
	if err != nil || projectAccess == nil {
		return mcp.Principal{}, errors.New("invalid agent access token")
	}
	scopes := intersectScopes(
		intersectScopes(access.Scopes, principal.ScopeList()),
		projectAccess.Scopes,
	)
	operationContext, err := services.WithOperationContext(ctx, services.OperationContext{
		Scope:        projectAccess.Scope,
		Actor:        models.ServicePrincipalActor(principal.ID),
		Source:       services.SourceProtocolMCP,
		CredentialID: credential.ID,
	})
	if err != nil {
		return mcp.Principal{}, errors.New("invalid agent access token")
	}
	if touchLastUsed {
		usedAt := now
		_ = a.db.WithContext(operationContext).Model(&models.AgentCredential{}).
			Where("id = ?", credential.ID).
			Update("last_used_at", usedAt).Error
		_ = a.db.WithContext(operationContext).Model(&models.ServicePrincipal{}).
			Where("id = ?", principal.ID).
			Update("last_used_at", usedAt).Error
	}

	return mcp.Principal{
		Type:         string(models.ActorTypeServicePrincipal),
		ID:           principal.ID,
		CredentialID: credential.ID,
		Scopes:       scopes,
		Attributes: map[string]any{
			"name":            access.Name,
			"client_id":       access.ClientID,
			"token_id":        access.JTI,
			"expires_at":      access.ExpiresAt.Format(time.RFC3339Nano),
			"project_key":     access.ProjectKey,
			"organization_id": projectAccess.Scope.OrganizationID,
			"project_id":      projectAccess.Scope.ProjectID,
		},
	}, nil
}

// runMCPProjectOperation is the database security boundary for one MCP
// authorization, tool call, resource read, or subscription revalidation. The
// callback's domain result is deliberately separated from the outer
// transaction result: denied PolicyDecision rows and failed idempotency
// reservations remain auditable, while each domain service keeps its own
// savepoint-backed rollback semantics through scopeddb.TransactionForContext.
func runMCPProjectOperation[T any](
	adapter *MCPAdapter,
	ctx context.Context,
	principal mcp.Principal,
	run func(
		context.Context,
		string,
		models.ProjectScope,
	) (T, error),
) (T, error) {
	var zero T
	if adapter == nil || adapter.db == nil || run == nil {
		return zero, errors.New("MCP project operation is unavailable")
	}
	operationContext, projectKey, scope, err :=
		mcpOperationContext(ctx, principal)
	if err != nil {
		return zero, err
	}
	publications := &mcpPublicationBuffer{}
	operationContext = context.WithValue(
		operationContext,
		mcpPublicationBufferContextKey{},
		publications,
	)
	var (
		result       T
		operationErr error
	)
	transactionErr := scopeddb.WithProjectScopeContextTransaction(
		operationContext,
		adapter.db,
		scope,
		func(scopedContext context.Context) error {
			currentAccess, revalidateErr :=
				adapter.service.RevalidatePrincipalProjectOperation(
					scopedContext,
					principal.Scopes...,
				)
			if revalidateErr != nil {
				return revalidateErr
			}
			if currentAccess.Project.Key != models.ProjectKey(projectKey) {
				return services.ErrProjectAccessDenied
			}
			result, operationErr = run(
				scopedContext,
				projectKey,
				scope,
			)
			// Domain errors are part of the operation outcome. In particular,
			// denied policy decisions and failed idempotency records must
			// remain durable. Infrastructure errors from COMMIT are returned
			// by the outer transaction itself.
			return nil
		},
	)
	if transactionErr != nil {
		return zero, transactionErr
	}
	if operationErr == nil {
		for _, publication := range publications.items {
			adapter.publishTicketResourcesNow(
				publication.principal,
				publication.ticketID,
				publication.oldQueue,
				publication.newQueue,
			)
		}
	}
	return result, operationErr
}

// runMCPExternalProjectOperation performs only the first short authorization
// transaction, then executes an operation-specific two-phase service outside
// it. The service must revalidate the same Grant/credential snapshot in its
// final project transaction before returning or persisting a result.
func runMCPExternalProjectOperation[T any](
	adapter *MCPAdapter,
	ctx context.Context,
	principal mcp.Principal,
	run func(
		context.Context,
		string,
		models.ProjectScope,
	) (T, error),
) (T, error) {
	var zero T
	if adapter == nil || adapter.db == nil || run == nil {
		return zero, errors.New(
			"MCP external project operation is unavailable",
		)
	}
	operationContext, projectKey, scope, err :=
		mcpOperationContext(ctx, principal)
	if err != nil {
		return zero, err
	}
	publications := &mcpPublicationBuffer{}
	operationContext = context.WithValue(
		operationContext,
		mcpPublicationBufferContextKey{},
		publications,
	)
	err = scopeddb.WithProjectScopeContextTransaction(
		operationContext,
		adapter.db,
		scope,
		func(scopedContext context.Context) error {
			currentAccess, revalidateErr :=
				adapter.service.
					RevalidatePrincipalProjectOperation(
						scopedContext,
						principal.Scopes...,
					)
			if revalidateErr != nil {
				return revalidateErr
			}
			if currentAccess.Project.Key !=
				models.ProjectKey(projectKey) {
				return services.ErrProjectAccessDenied
			}
			return nil
		},
	)
	if err != nil {
		return zero, err
	}
	result, operationErr := run(
		operationContext,
		projectKey,
		scope,
	)
	if operationErr == nil {
		for _, publication := range publications.items {
			adapter.publishTicketResourcesNow(
				publication.principal,
				publication.ticketID,
				publication.oldQueue,
				publication.newQueue,
			)
		}
	}
	return result, operationErr
}

func (a *MCPAdapter) Authorize(
	ctx context.Context,
	principal mcp.Principal,
	request mcp.AuthorizationRequest,
) error {
	if err := validateMCPPrincipal(principal); err != nil {
		return err
	}
	if err := validateMCPPolicyPreauthorizationInput(
		request.Action,
		request.Arguments,
	); err != nil {
		return err
	}
	projectOperation := runMCPProjectOperation[struct{}]
	if policySpecForMCPAction(request.Action).write {
		projectOperation = runMCPExternalProjectOperation[struct{}]
	}
	_, err := projectOperation(
		a,
		ctx,
		principal,
		func(
			scopedContext context.Context,
			projectKey string,
			_ models.ProjectScope,
		) (struct{}, error) {
			return struct{}{}, a.authorizeScoped(
				scopedContext,
				principal,
				request,
				projectKey,
			)
		},
	)
	return err
}

func (a *MCPAdapter) authorizeScoped(
	ctx context.Context,
	principal mcp.Principal,
	request mcp.AuthorizationRequest,
	projectKey string,
) error {
	if !principal.HasScopes(request.RequiredScopes...) {
		return errors.New("insufficient scope")
	}
	if mcpTicketTool(request.Action) {
		if err := validateMCPProjectArgument(request.Arguments, projectKey); err != nil {
			return err
		}
	}
	var resourceReference mcp.ProjectResourceReference
	if request.ResourceURI != "" {
		parsedReference, parseErr :=
			mcp.ParseProjectResourceURI(request.ResourceURI)
		if parseErr != nil || parsedReference.ProjectKey != projectKey {
			return errors.New("MCP resource project does not match access token")
		}
		resourceReference = parsedReference
	}

	// ticket_list performs its list- and object-level checks against one
	// request-scoped policy snapshot in listTickets. Running CheckAction here
	// would persist a second PolicyDecision for the same list request.
	if request.Action == "ticket_list" || request.Action == "action_check" {
		return nil
	}

	spec := policySpecForMCPAction(request.Action)
	if spec.action == "" {
		return errors.New("unsupported MCP policy action")
	}
	if request.Action == "resource:read" {
		if resourceReference.Kind == mcp.ProjectResourceQueue {
			// Queue reads are filtered by listTickets using one bounded
			// request-scoped policy batch.
			return nil
		}
		if resourceReference.Kind == mcp.ProjectResourceHistory {
			spec.action = "ticket.history.read"
		}
	}
	resourceID, policyContext := mcpAuthorizationTarget(request)
	for _, scope := range request.RequiredScopes {
		domainAction := spec.action
		if domainAction == "" {
			domainAction = request.Action
		}
		check := a.service.CheckAction
		if spec.write {
			check = a.service.CheckActionInShortProjectTransactions
		}
		decision, err := check(ctx, services.PolicyCheckInput{
			ServicePrincipalID: principal.ID,
			CredentialID:       principal.CredentialID,
			Scope:              scope,
			Action:             domainAction,
			ResourceType:       spec.resourceType,
			ResourceID:         resourceID,
			IsWrite:            spec.write,
			IsRisky:            spec.risky,
			RequestDigest:      requestDigest(mcpAuthorizationDigestInput(request)),
			SourceProtocol:     mcpSourceProtocol,
			Context:            policyContext,
		})
		if err != nil {
			if decision != nil {
				return &mcp.PolicyError{DecisionID: decision.ID, ReasonCode: decision.ReasonCode}
			}
			return err
		}
	}
	return nil
}

func mcpAuthorizationTarget(request mcp.AuthorizationRequest) (string, map[string]any) {
	arguments := request.Arguments
	resourceID := ""
	if raw, ok := arguments["ticket_id"]; ok {
		if ticketID, err := numericUint(raw); err == nil && ticketID > 0 {
			resourceID = strconv.FormatUint(uint64(ticketID), 10)
		}
	}

	policyContext := make(map[string]any)
	if request.ResourceURI != "" {
		reference, err := mcp.ParseProjectResourceURI(request.ResourceURI)
		if err == nil {
			policyContext["project_key"] = reference.ProjectKey
			switch reference.Kind {
			case mcp.ProjectResourceTicket, mcp.ProjectResourceHistory:
				resourceID = strconv.FormatUint(uint64(reference.TicketID), 10)
			case mcp.ProjectResourceQueue:
				resourceID = "*"
				policyContext["queue"] = reference.Queue
			}
		}
	}

	switch request.Action {
	case "ticket_update":
		if patch, ok := arguments["patch"].(map[string]any); ok {
			fields := make([]string, 0, len(patch))
			for field := range patch {
				if field == "queue" {
					field = "custom_fields"
				}
				fields = append(fields, field)
			}
			sort.Strings(fields)
			policyContext["changed_fields"] = fields
		}
	case "ticket_assign":
		if assignee, ok := arguments["assignee"].(map[string]any); ok {
			policyContext["assignee_type"] = argumentString(assignee, "type")
			policyContext["assignee_id"] = argumentString(assignee, "id")
		}
	case "ticket_transition":
		policyContext["target_status"] = argumentString(arguments, "status")
	case "ticket_add_comment":
		policyContext["visibility"] = argumentString(arguments, "visibility")
	case "ticket_attach_file":
		policyContext["file_name"] = argumentString(arguments, "file_name")
		policyContext["content_type"] = argumentString(arguments, "content_type")
	case "ticket_create":
		if queue := argumentString(arguments, "queue"); queue != "" {
			policyContext["queue"] = queue
		}
	}
	if len(policyContext) == 0 {
		return resourceID, nil
	}
	return resourceID, policyContext
}

func mcpAuthorizationDigestInput(request mcp.AuthorizationRequest) map[string]any {
	if len(request.Arguments) > 0 {
		return request.Arguments
	}
	if request.ResourceURI != "" {
		return map[string]any{"resource_uri": request.ResourceURI}
	}
	return map[string]any{"action": request.Action}
}

func (a *MCPAdapter) CallTool(
	ctx context.Context,
	principal mcp.Principal,
	name string,
	arguments map[string]any,
) (map[string]any, error) {
	if err := validateMCPPrincipal(principal); err != nil {
		return nil, backendError(err)
	}
	if err := validateMCPToolPreauthorizationInput(
		name,
		arguments,
	); err != nil {
		return nil, err
	}
	command, commandReady, err :=
		mcpNativeCommandAuthorizationInput(
			principal,
			name,
			arguments,
		)
	if err != nil {
		return nil, err
	}
	if commandReady {
		operationContext, projectKey, _, contextErr :=
			mcpOperationContext(ctx, principal)
		if contextErr != nil {
			return nil, backendError(contextErr)
		}
		if contextErr = validateMCPProjectArgument(
			arguments,
			projectKey,
		); contextErr != nil {
			return nil, contextErr
		}
		ctx, err = a.service.
			AuthorizeNativeCommandInShortProjectTransactions(
				operationContext,
				command,
			)
		if err != nil {
			return nil, backendError(err)
		}
	}
	leaseContext, release, err :=
		a.service.AcquireAgentExecutionContext(
			ctx,
			principal.ID,
		)
	if err != nil {
		return nil, backendError(err)
	}
	defer release()
	ctx = leaseContext

	projectOperation := runMCPProjectOperation[map[string]any]
	if name == "ticket_attach_file" || name == "ticket_create" {
		projectOperation =
			runMCPExternalProjectOperation[map[string]any]
	}
	result, err := projectOperation(
		a,
		ctx,
		principal,
		func(
			scopedContext context.Context,
			projectKey string,
			_ models.ProjectScope,
		) (map[string]any, error) {
			return a.callToolScoped(
				scopedContext,
				principal,
				projectKey,
				name,
				arguments,
			)
		},
	)
	if err != nil {
		return nil, backendError(err)
	}
	return result, nil
}

func (a *MCPAdapter) callToolScoped(
	ctx context.Context,
	principal mcp.Principal,
	projectKey string,
	name string,
	arguments map[string]any,
) (map[string]any, error) {
	if !mcpTicketTool(name) {
		return nil, &mcp.BackendError{Code: "unknown_tool", Message: "unknown MCP tool"}
	}
	if err := validateMCPProjectArgument(arguments, projectKey); err != nil {
		return nil, err
	}
	if err := validateMCPToolPreauthorizationInput(
		name,
		arguments,
	); err != nil {
		return nil, err
	}

	switch name {
	case "ticket_list":
		return a.listTickets(ctx, principal, arguments)
	case "ticket_get":
		return a.getTicket(ctx, principal, arguments)
	case "ticket_history":
		return a.ticketHistory(ctx, principal, arguments)
	case "action_check":
		return a.actionCheck(ctx, principal, arguments)
	case "ticket_create":
		return a.createTicket(ctx, principal, arguments)
	case "ticket_update":
		return a.updateTicket(ctx, principal, arguments)
	case "ticket_claim":
		return a.claimTicket(ctx, principal, arguments)
	case "ticket_heartbeat":
		return a.heartbeatTicket(ctx, principal, arguments)
	case "ticket_release":
		return a.releaseTicket(ctx, principal, arguments)
	case "ticket_assign":
		return a.assignTicket(ctx, principal, arguments)
	case "ticket_transition":
		return a.transitionTicket(ctx, principal, arguments)
	case "ticket_add_comment":
		return a.addComment(ctx, principal, arguments)
	case "ticket_attach_file":
		return a.attachFile(ctx, principal, arguments)
	default:
		return nil, &mcp.BackendError{Code: "unknown_tool", Message: "unknown MCP tool"}
	}
}

func mcpTicketTool(name string) bool {
	switch name {
	case "ticket_list",
		"ticket_get",
		"ticket_history",
		"action_check",
		"ticket_create",
		"ticket_update",
		"ticket_claim",
		"ticket_heartbeat",
		"ticket_release",
		"ticket_assign",
		"ticket_transition",
		"ticket_add_comment",
		"ticket_attach_file":
		return true
	default:
		return false
	}
}

func mcpToolRequiresLease(name string) bool {
	switch name {
	case "ticket_update",
		"ticket_assign",
		"ticket_transition",
		"ticket_add_comment",
		"ticket_attach_file":
		return true
	default:
		return false
	}
}

func validateMCPToolPreauthorizationInput(
	name string,
	arguments map[string]any,
) error {
	if !mcpToolRequiresLease(name) ||
		argumentString(arguments, "lease_id") != "" {
		return nil
	}
	failure := backendError(services.ErrLeaseConflict)
	failure.Details = map[string]any{"field": "lease_id"}
	return failure
}

func validateMCPPolicyPreauthorizationInput(
	name string,
	arguments map[string]any,
) error {
	switch name {
	case "ticket_add_comment", "ticket_attach_file":
		return validateMCPToolPreauthorizationInput(name, arguments)
	default:
		return nil
	}
}

func mcpNativeCommandAuthorizationInput(
	principal mcp.Principal,
	name string,
	arguments map[string]any,
) (services.NativeCommandAuthorizationInput, bool, error) {
	command := services.NativeCommandAuthorizationInput{
		Actor:          principalActor(principal),
		CredentialID:   principal.CredentialID,
		TokenScopes:    append([]string(nil), principal.Scopes...),
		RequestDigest:  requestDigest(arguments),
		SourceProtocol: mcpSourceProtocol,
	}
	switch name {
	case "ticket_update":
		command.Kind = services.NativeCommandTicketUpdate
	case "ticket_assign":
		command.Kind = services.NativeCommandTicketAssign
		rawAssignee, ok := arguments["assignee"].(map[string]any)
		if !ok {
			return command, false, invalidArgument(
				"assignee is required",
			)
		}
		assignee := models.ActorRef{
			Type: models.ActorType(
				argumentString(rawAssignee, "type"),
			),
			ID: argumentString(rawAssignee, "id"),
		}
		command.Assignee = &assignee
	case "ticket_transition":
		command.Kind = services.NativeCommandTicketTransit
	case "ticket_add_comment":
		command.Kind = services.NativeCommandCommentCreate
	case "ticket_claim":
		command.Kind = services.NativeCommandTicketClaim
	case "ticket_heartbeat":
		command.Kind = services.NativeCommandLeaseHeartbeat
		command.LeaseID = argumentString(arguments, "lease_id")
	case "ticket_release":
		command.Kind = services.NativeCommandLeaseRelease
		command.LeaseID = argumentString(arguments, "lease_id")
	default:
		return command, false, nil
	}
	ticketID, err := argumentUint(arguments, "ticket_id")
	if err != nil {
		return command, false, err
	}
	command.TicketID = ticketID
	return command, true, nil
}

func (a *MCPAdapter) createTicket(
	ctx context.Context,
	principal mcp.Principal,
	arguments map[string]any,
) (map[string]any, error) {
	requestTypeVersionID, ok := normalizeMachineConfigurationVersionID(
		argumentString(arguments, "request_type_version_id"),
	)
	if !ok {
		return nil, invalidArgument(
			"request_type_version_id must be a canonical UUID",
		)
	}
	workflowVersionID, ok := normalizeMachineConfigurationVersionID(
		argumentString(arguments, "workflow_version_id"),
	)
	if !ok {
		return nil, invalidArgument(
			"workflow_version_id must be a canonical UUID",
		)
	}
	request := models.TicketCreateRequest{
		Title:                argumentString(arguments, "title"),
		Description:          argumentString(arguments, "description"),
		Type:                 models.TicketType(argumentString(arguments, "type")),
		Priority:             models.TicketPriority(argumentString(arguments, "priority")),
		Source:               models.TicketSourceAgent,
		RequestTypeVersionID: requestTypeVersionID,
		WorkflowVersionID:    workflowVersionID,
		Tags:                 models.StringList(argumentStrings(arguments, "tags")),
	}
	if rawContext, ok := arguments["agent_context"].(map[string]any); ok {
		encoded, _ := json.Marshal(rawContext)
		var agentContext models.AgentContext
		if err := json.Unmarshal(encoded, &agentContext); err != nil {
			return nil, invalidArgument("invalid agent_context")
		}
		request.AgentContext = &agentContext
	}
	queue := argumentString(arguments, "queue")
	if queue != "" {
		if !mcpQueuePattern.MatchString(queue) {
			return nil, invalidArgument("invalid queue")
		}
		customFields := models.JSONMap{"queue": queue}
		request.CustomFields = &customFields
	}

	reservation, err := a.reserveIdempotency(ctx, principal, "ticket.create", arguments)
	if err != nil {
		return nil, err
	}
	digest := requestDigest(arguments)
	authorization := services.NativeCommandAuthorizationInput{
		Kind:           services.NativeCommandTicketCreate,
		Actor:          principalActor(principal),
		CredentialID:   principal.CredentialID,
		TokenScopes:    append([]string(nil), principal.Scopes...),
		RequestDigest:  digest,
		SourceProtocol: mcpSourceProtocol,
	}
	if reservation.Replayed {
		if err := a.service.
			AuthorizeNativeCommandReplayInShortProjectTransactions(
				ctx,
				authorization,
			); err != nil {
			return nil, backendError(err)
		}
		return replayReceipt(reservation.Record)
	}

	authorizedContext, err :=
		a.service.AuthorizeNativeCommandInShortProjectTransactions(
			ctx,
			authorization,
		)
	if err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	result, err := runMachineTicketCreateDatabaseCommand(
		authorizedContext,
		a.db,
		a.service,
		services.NativeTicketCreateInput{
			Request:             request,
			Actor:               principalActor(principal),
			CredentialID:        principal.CredentialID,
			SourceProtocol:      mcpSourceProtocol,
			RequestDigest:       digest,
			TrustLevel:          models.TicketTrustLevelUntrusted,
			IdempotencyRecordID: reservation.Record.ID,
		},
	)
	if err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	a.publishTicketResources(
		ctx,
		principal,
		result.Ticket.ID,
		"",
		ticketQueue(result.Ticket),
	)
	return receiptMap(result.Receipt), nil
}

func (a *MCPAdapter) updateTicket(
	ctx context.Context,
	principal mcp.Principal,
	arguments map[string]any,
) (map[string]any, error) {
	ticketID, expectedVersion, leaseID, err := commandTicketVersion(arguments)
	if err != nil {
		return nil, err
	}
	patch, ok := arguments["patch"].(map[string]any)
	if !ok || len(patch) == 0 {
		return nil, invalidArgument("patch is required")
	}
	for field, value := range patch {
		switch field {
		case "title", "description", "type", "priority":
		case "tags":
		case "agent_context":
		case "queue":
			queue, _ := value.(string)
			queue = strings.TrimSpace(queue)
			if !mcpQueuePattern.MatchString(queue) {
				return nil, invalidArgument("invalid queue")
			}
		default:
			return nil, invalidArgument("unsupported ticket patch field")
		}
	}
	scope, err := services.RequireProjectScope(ctx)
	if err != nil {
		return nil, backendError(err)
	}
	var current models.Ticket
	if err := a.db.WithContext(ctx).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			ticketID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&current).Error; err != nil {
		return nil, backendError(err)
	}
	oldQueue := ticketQueue(&current)
	newQueue := oldQueue
	changes := make(map[string]any, len(patch))
	for field, value := range patch {
		switch field {
		case "title", "description", "type", "priority":
			changes[field] = value
		case "tags":
			encoded, _ := json.Marshal(argumentStringSliceValue(value))
			changes[field] = datatypes.JSON(encoded)
		case "agent_context":
			changes[field] = value
		case "queue":
			newQueue = strings.TrimSpace(value.(string))
			customFields := cloneStringAnyMap(current.CustomFields.Data())
			customFields["queue"] = newQueue
			encoded, _ := json.Marshal(customFields)
			changes["custom_fields"] = datatypes.JSON(encoded)
		}
	}
	reservation, err := a.reserveIdempotency(ctx, principal, "ticket.update", arguments)
	if err != nil {
		return nil, err
	}
	if reservation.Replayed {
		return replayReceipt(reservation.Record)
	}
	result, err := a.service.UpdateTicketVersion(ctx, services.VersionedTicketUpdateInput{
		TicketID:            ticketID,
		ExpectedVersion:     expectedVersion,
		LeaseID:             leaseID,
		Actor:               principalActor(principal),
		CredentialID:        principal.CredentialID,
		RequiredScope:       models.ScopeTicketsUpdate,
		Action:              "ticket.update",
		SourceProtocol:      mcpSourceProtocol,
		RequestDigest:       requestDigest(arguments),
		Changes:             changes,
		IdempotencyRecordID: reservation.Record.ID,
		EventData: map[string]any{
			"ticket_id":      ticketID,
			"changed_fields": sortedMapKeys(changes),
			"reason":         argumentString(arguments, "reason"),
			"old_queue":      oldQueue,
			"new_queue":      newQueue,
		},
	})
	if err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	a.publishTicketResources(
		ctx,
		principal,
		ticketID,
		oldQueue,
		ticketQueue(result.Ticket),
	)
	return receiptMap(result.Receipt), nil
}

func (a *MCPAdapter) assignTicket(
	ctx context.Context,
	principal mcp.Principal,
	arguments map[string]any,
) (map[string]any, error) {
	ticketID, expectedVersion, leaseID, err := commandTicketVersion(arguments)
	if err != nil {
		return nil, err
	}
	assigneeValue, ok := arguments["assignee"].(map[string]any)
	if !ok {
		return nil, invalidArgument("assignee is required")
	}
	assignee := models.ActorRef{
		Type: models.ActorType(argumentString(assigneeValue, "type")),
		ID:   argumentString(assigneeValue, "id"),
	}
	// Validate the target before reserving an idempotency record so malformed
	// Assignment input keeps its stable input/not-found/policy error contract.
	// AssignTicket resolves it again inside the write command to close the
	// validation/write race.
	if _, err := a.service.ResolveTicketAssignmentChanges(ctx, &assignee); err != nil {
		return nil, backendError(err)
	}
	reservation, err := a.reserveIdempotency(ctx, principal, "ticket.assign", arguments)
	if err != nil {
		return nil, err
	}
	if reservation.Replayed {
		return replayReceipt(reservation.Record)
	}
	result, err := a.service.AssignTicket(ctx, services.AssignTicketCommand{
		TicketID:            ticketID,
		ExpectedVersion:     expectedVersion,
		LeaseID:             leaseID,
		Actor:               principalActor(principal),
		Assignee:            &assignee,
		CredentialID:        principal.CredentialID,
		SourceProtocol:      mcpSourceProtocol,
		RequestDigest:       requestDigest(arguments),
		Reason:              argumentString(arguments, "reason"),
		IdempotencyRecordID: reservation.Record.ID,
	})
	if err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	a.publishTicketResources(
		ctx,
		principal,
		ticketID,
		"",
		ticketQueue(result.Ticket),
	)
	return receiptMap(result.Receipt), nil
}

func (a *MCPAdapter) transitionTicket(
	ctx context.Context,
	principal mcp.Principal,
	arguments map[string]any,
) (map[string]any, error) {
	ticketID, expectedVersion, leaseID, err := commandTicketVersion(arguments)
	if err != nil {
		return nil, err
	}
	status := models.TicketStatus(argumentString(arguments, "status"))
	if !status.IsValid() {
		return nil, invalidArgument("invalid status")
	}
	reservation, err := a.reserveIdempotency(ctx, principal, "ticket.transition", arguments)
	if err != nil {
		return nil, err
	}
	if reservation.Replayed {
		return replayReceipt(reservation.Record)
	}
	result, err := a.service.TransitionTicket(ctx, services.TransitionTicketCommand{
		TicketID:            ticketID,
		ExpectedVersion:     expectedVersion,
		LeaseID:             leaseID,
		Actor:               principalActor(principal),
		Status:              status,
		CredentialID:        principal.CredentialID,
		SourceProtocol:      mcpSourceProtocol,
		RequestDigest:       requestDigest(arguments),
		Reason:              argumentString(arguments, "reason"),
		IdempotencyRecordID: reservation.Record.ID,
	})
	if err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	a.publishTicketResources(
		ctx,
		principal,
		ticketID,
		"",
		ticketQueue(result.Ticket),
	)
	return receiptMap(result.Receipt), nil
}

func (a *MCPAdapter) addComment(
	ctx context.Context,
	principal mcp.Principal,
	arguments map[string]any,
) (map[string]any, error) {
	ticketID, expectedVersion, leaseID, err := commandTicketVersionWithRequiredLease(arguments)
	if err != nil {
		return nil, err
	}
	commentType := models.CommentType(argumentString(arguments, "visibility"))
	reservation, err := a.reserveIdempotency(ctx, principal, "ticket.comment.create", arguments)
	if err != nil {
		return nil, err
	}
	if reservation.Replayed {
		return a.replayComment(ctx, reservation.Record)
	}
	result, err := a.service.CreateComment(ctx, services.NativeCommentInput{
		TicketID:            ticketID,
		ExpectedVersion:     expectedVersion,
		LeaseID:             leaseID,
		Actor:               principalActor(principal),
		CredentialID:        principal.CredentialID,
		SourceProtocol:      mcpSourceProtocol,
		RequestDigest:       requestDigest(arguments),
		Content:             argumentString(arguments, "content"),
		ContentType:         argumentString(arguments, "content_type"),
		Type:                commentType,
		Reason:              argumentString(arguments, "reason"),
		IdempotencyRecordID: reservation.Record.ID,
	})
	if err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	a.publishTicketResources(ctx, principal, ticketID, "", "")
	return map[string]any{
		"receipt": receiptMap(result.Receipt),
		"comment": commentMap(result.Comment),
	}, nil
}

func (a *MCPAdapter) attachFile(
	ctx context.Context,
	principal mcp.Principal,
	arguments map[string]any,
) (map[string]any, error) {
	ticketID, expectedVersion, leaseID, err := commandTicketVersionWithRequiredLease(arguments)
	if err != nil {
		return nil, err
	}
	content, err := decodeBase64Attachment(
		argumentString(arguments, "content_base64"),
		argumentString(arguments, "sha256"),
	)
	if err != nil {
		return nil, err
	}
	attachmentInput := services.NativeAttachmentInput{
		TicketID:        ticketID,
		ExpectedVersion: expectedVersion,
		LeaseID:         leaseID,
		Actor:           principalActor(principal),
		CredentialID:    principal.CredentialID,
		SourceProtocol:  mcpSourceProtocol,
		RequestDigest:   requestDigest(arguments),
		OriginalName: argumentString(
			arguments,
			"file_name",
		),
		ContentType: argumentString(
			arguments,
			"content_type",
		),
		IsPublic: argumentString(
			arguments,
			"visibility",
		) == "public",
	}
	if err := a.service.PrepareAttachmentUploadAuthorization(
		ctx,
		&attachmentInput,
	); err != nil {
		return nil, err
	}
	reservation, err := a.reserveIdempotency(ctx, principal, "ticket.attachment.create", arguments)
	if err != nil {
		return nil, err
	}
	if reservation.Replayed {
		return a.replayAttachment(ctx, reservation.Record)
	}
	attachmentInput.Reader = bytes.NewReader(content)
	attachmentInput.IdempotencyRecordID =
		reservation.Record.ID
	result, err := a.service.StoreAttachment(
		ctx,
		attachmentInput,
	)
	if err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	a.publishTicketResources(ctx, principal, ticketID, "", "")
	return map[string]any{
		"receipt":    receiptMap(result.Receipt),
		"attachment": attachmentMap(result.Attachment),
	}, nil
}

func (a *MCPAdapter) claimTicket(
	ctx context.Context,
	principal mcp.Principal,
	arguments map[string]any,
) (map[string]any, error) {
	ticketID, err := argumentUint(arguments, "ticket_id")
	if err != nil {
		return nil, err
	}
	expectedVersion, err := argumentUint64(arguments, "expected_version")
	if err != nil {
		return nil, err
	}
	leaseSeconds, err := argumentInt(arguments, "lease_seconds", 120, 10, 900)
	if err != nil {
		return nil, err
	}
	reservation, err := a.reserveIdempotency(ctx, principal, "ticket.lease.claim", arguments)
	if err != nil {
		return nil, err
	}
	if reservation.Replayed {
		return replayLeaseResult(reservation.Record)
	}
	result, err := a.service.ClaimTicketLeaseCommand(ctx, services.ClaimTicketLeaseCommandInput{
		TicketID:            ticketID,
		Actor:               principalActor(principal),
		ExpectedVersion:     expectedVersion,
		TTL:                 time.Duration(leaseSeconds) * time.Second,
		CredentialID:        principal.CredentialID,
		SourceProtocol:      mcpSourceProtocol,
		RequestDigest:       requestDigest(arguments),
		IdempotencyRecordID: reservation.Record.ID,
	})
	if err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	a.publishTicketResources(ctx, principal, ticketID, "", "")
	return leaseResultMap(result), nil
}

func (a *MCPAdapter) heartbeatTicket(
	ctx context.Context,
	principal mcp.Principal,
	arguments map[string]any,
) (map[string]any, error) {
	ticketID, err := argumentUint(arguments, "ticket_id")
	if err != nil {
		return nil, err
	}
	leaseID := argumentString(arguments, "lease_id")
	leaseSeconds, err := argumentInt(arguments, "lease_seconds", 120, 10, 900)
	if err != nil {
		return nil, err
	}
	reservation, err := a.reserveIdempotency(ctx, principal, "ticket.lease.heartbeat", arguments)
	if err != nil {
		return nil, err
	}
	if reservation.Replayed {
		return replayLeaseResult(reservation.Record)
	}
	scope, err := services.RequireProjectScope(ctx)
	if err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	var existing models.TicketLease
	if err := a.db.WithContext(ctx).
		Where(
			"id = ? AND ticket_id = ? AND organization_id = ? AND project_id = ?",
			leaseID,
			ticketID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&existing).Error; err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	result, err := a.service.HeartbeatTicketLeaseCommand(ctx, services.HeartbeatTicketLeaseCommandInput{
		LeaseID:             leaseID,
		Actor:               principalActor(principal),
		ExpectedVersion:     existing.TicketVersion,
		TTL:                 time.Duration(leaseSeconds) * time.Second,
		CredentialID:        principal.CredentialID,
		SourceProtocol:      mcpSourceProtocol,
		RequestDigest:       requestDigest(arguments),
		IdempotencyRecordID: reservation.Record.ID,
	})
	if err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	a.publishTicketResources(ctx, principal, ticketID, "", "")
	return leaseResultMap(result), nil
}

func (a *MCPAdapter) releaseTicket(
	ctx context.Context,
	principal mcp.Principal,
	arguments map[string]any,
) (map[string]any, error) {
	ticketID, err := argumentUint(arguments, "ticket_id")
	if err != nil {
		return nil, err
	}
	leaseID := argumentString(arguments, "lease_id")
	reservation, err := a.reserveIdempotency(ctx, principal, "ticket.lease.release", arguments)
	if err != nil {
		return nil, err
	}
	if reservation.Replayed {
		return replayReceipt(reservation.Record)
	}
	scope, err := services.RequireProjectScope(ctx)
	if err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	var lease models.TicketLease
	if err := a.db.WithContext(ctx).
		Where(
			"id = ? AND ticket_id = ? AND organization_id = ? AND project_id = ?",
			leaseID,
			ticketID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&lease).Error; err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	result, err := a.service.ReleaseTicketLeaseCommand(ctx, services.ReleaseTicketLeaseCommandInput{
		LeaseID:             lease.ID,
		Actor:               principalActor(principal),
		Reason:              "released by MCP agent",
		CredentialID:        principal.CredentialID,
		SourceProtocol:      mcpSourceProtocol,
		RequestDigest:       requestDigest(arguments),
		IdempotencyRecordID: reservation.Record.ID,
	})
	if err != nil {
		a.failReservation(ctx, reservation, err)
		return nil, backendError(err)
	}
	a.publishTicketResources(ctx, principal, ticketID, "", "")
	return receiptMap(result.Receipt), nil
}

func (a *MCPAdapter) ReadResource(
	ctx context.Context,
	principal mcp.Principal,
	resourceURI string,
) (mcp.ResourceContent, error) {
	leaseContext, release, err :=
		a.service.AcquireAgentExecutionContext(
			ctx,
			principal.ID,
		)
	if err != nil {
		return mcp.ResourceContent{}, backendError(err)
	}
	defer release()
	ctx = leaseContext

	result, err := runMCPProjectOperation(
		a,
		ctx,
		principal,
		func(
			scopedContext context.Context,
			projectKey string,
			_ models.ProjectScope,
		) (mcp.ResourceContent, error) {
			return a.readResourceScoped(
				scopedContext,
				principal,
				projectKey,
				resourceURI,
			)
		},
	)
	if err != nil {
		return mcp.ResourceContent{}, backendError(err)
	}
	return result, nil
}

func (a *MCPAdapter) readResourceScoped(
	ctx context.Context,
	principal mcp.Principal,
	projectKey string,
	resourceURI string,
) (mcp.ResourceContent, error) {
	reference, err := mcp.ParseProjectResourceURI(resourceURI)
	if err != nil || reference.ProjectKey != projectKey {
		return mcp.ResourceContent{}, invalidArgument("resource project does not match access token")
	}

	var payload any
	switch reference.Kind {
	case mcp.ProjectResourceTicket:
		payload, err = a.getTicketByID(ctx, principal, reference.TicketID)
	case mcp.ProjectResourceHistory:
		payload, err = a.historyByTicket(ctx, principal, reference.TicketID, "", mcpMaximumLimit)
	case mcp.ProjectResourceQueue:
		payload, err = a.listTickets(ctx, principal, map[string]any{
			"project_key": projectKey,
			"queue":       reference.Queue,
			"limit":       int64(mcpMaximumLimit),
		})
	default:
		err = invalidArgument("unsupported resource URI")
	}
	if err != nil {
		return mcp.ResourceContent{}, err
	}
	text, err := json.Marshal(payload)
	if err != nil {
		return mcp.ResourceContent{}, backendError(err)
	}
	return mcp.ResourceContent{
		URI:      resourceURI,
		MIMEType: "application/json",
		Text:     string(text),
	}, nil
}

// ValidateSubscription is the non-auditing delivery-time authorization path.
// Establishing a subscription remains fully audited; repeated cache
// invalidations must not create PolicyDecision rows or consume execution
// quotas.
func (a *MCPAdapter) ValidateSubscription(
	ctx context.Context,
	principal mcp.Principal,
	resourceURI string,
) (bool, error) {
	if _, _, _, err := mcpOperationContext(ctx, principal); err != nil {
		return false, nil
	}
	allowed, err := runMCPProjectOperation(
		a,
		ctx,
		principal,
		func(
			scopedContext context.Context,
			projectKey string,
			scope models.ProjectScope,
		) (bool, error) {
			return a.validateSubscriptionScoped(
				scopedContext,
				principal,
				projectKey,
				scope,
				resourceURI,
			)
		},
	)
	if err != nil && machineAuthorizationRevoked(err) {
		return false, nil
	}
	return allowed, err
}

func (a *MCPAdapter) validateSubscriptionScoped(
	ctx context.Context,
	principal mcp.Principal,
	projectKey string,
	scope models.ProjectScope,
	resourceURI string,
) (bool, error) {
	reference, err := mcp.ParseProjectResourceURI(resourceURI)
	if err != nil || reference.ProjectKey != projectKey {
		return false, nil
	}
	authorization := mcp.AuthorizationRequest{
		Action:         "resource:subscribe",
		RequiredScopes: []string{models.ScopeTicketsRead, models.ScopeEventsSubscribe},
		ResourceURI:    resourceURI,
	}
	resourceID, policyContext := mcpAuthorizationTarget(authorization)
	if resourceID == "" {
		return false, nil
	}
	for _, scope := range authorization.RequiredScopes {
		allowed, err := a.service.EvaluateReadAction(ctx, services.PolicyCheckInput{
			ServicePrincipalID: principal.ID,
			CredentialID:       principal.CredentialID,
			Scope:              scope,
			Action:             "ticket.subscribe",
			ResourceType:       "ticket",
			ResourceID:         resourceID,
			SourceProtocol:     mcpSourceProtocol,
			Context:            policyContext,
		})
		if err != nil || !allowed {
			return allowed, err
		}
	}

	switch reference.Kind {
	case mcp.ProjectResourceTicket, mcp.ProjectResourceHistory:
		readAction := "ticket.read"
		if reference.Kind == mcp.ProjectResourceHistory {
			readAction = "ticket.history.read"
		}
		allowed, evaluateErr := a.service.EvaluateReadAction(ctx, services.PolicyCheckInput{
			ServicePrincipalID: principal.ID,
			CredentialID:       principal.CredentialID,
			Scope:              models.ScopeTicketsRead,
			Action:             readAction,
			ResourceType:       "ticket",
			ResourceID:         strconv.FormatUint(uint64(reference.TicketID), 10),
			SourceProtocol:     mcpSourceProtocol,
		})
		if evaluateErr != nil || !allowed {
			return allowed, evaluateErr
		}
		var exists int64
		queryErr := a.db.WithContext(ctx).Model(&models.Ticket{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				reference.TicketID,
				scope.OrganizationID,
				scope.ProjectID,
			).
			Count(&exists).Error
		if queryErr != nil {
			return false, queryErr
		}
		return exists == 1, nil
	case mcp.ProjectResourceQueue:
		return true, nil
	default:
		return false, nil
	}
}

func (a *MCPAdapter) listTickets(
	ctx context.Context,
	principal mcp.Principal,
	arguments map[string]any,
) (map[string]any, error) {
	if !principal.HasScopes(models.ScopeTicketsRead) {
		return nil, &mcp.BackendError{
			Code:    "insufficient_scope",
			Message: "tickets:read scope is required",
		}
	}
	limit, err := argumentInt(arguments, "limit", mcpDefaultLimit, 1, mcpMaximumLimit)
	if err != nil {
		return nil, err
	}
	cursor, err := decodeMCPQueryCursor(argumentString(arguments, "cursor"))
	if err != nil {
		return nil, err
	}
	queue := argumentString(arguments, "queue")
	if queue != "" && !mcpQueuePattern.MatchString(queue) {
		return nil, invalidArgument("invalid queue")
	}
	scope, err := services.RequireProjectScope(ctx)
	if err != nil {
		return nil, backendError(err)
	}

	policyBatch, err := a.service.PrepareReadPolicyBatch(ctx, services.PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		CredentialID:       principal.CredentialID,
		Scope:              models.ScopeTicketsRead,
		Action:             "ticket.list",
		ResourceType:       "ticket",
		ResourceID:         "*",
		RequestDigest:      requestDigest(arguments),
		SourceProtocol:     mcpSourceProtocol,
	})
	if err != nil {
		return nil, backendError(err)
	}

	query := a.db.WithContext(ctx).
		Model(&models.Ticket{}).
		Where(
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		)
	if statuses := argumentStrings(arguments, "status"); len(statuses) > 0 {
		query = query.Where("status IN ?", statuses)
	}
	if priorities := argumentStrings(arguments, "priority"); len(priorities) > 0 {
		query = query.Where("priority IN ?", priorities)
	}
	if assigned := argumentString(arguments, "assigned_to"); assigned != "" {
		query = query.Where("assigned_to_actor_id = ?", assigned)
	}
	if search := strings.TrimSpace(argumentString(arguments, "search")); search != "" {
		pattern := "%" + escapeLike(search) + "%"
		query = query.Where(
			"(title LIKE ? ESCAPE '\\' OR description LIKE ? ESCAPE '\\' OR ticket_number LIKE ? ESCAPE '\\')",
			pattern,
			pattern,
			pattern,
		)
	}
	if !cursor.CreatedAt.IsZero() {
		cursorID, parseErr := strconv.ParseUint(cursor.ID, 10, 64)
		if parseErr != nil || cursorID == 0 {
			return nil, invalidArgument("invalid cursor")
		}
		query = query.Where(
			"(created_at < ?) OR (created_at = ? AND id < ?)",
			cursor.CreatedAt,
			cursor.CreatedAt,
			cursorID,
		)
	}

	candidateBudget := boundedMCPListCandidateBudget(limit)
	var candidates []models.Ticket
	if err := query.
		Order("created_at DESC, id DESC").
		Limit(candidateBudget + 1).
		Find(&candidates).Error; err != nil {
		return nil, backendError(err)
	}

	items := make([]map[string]any, 0, limit)
	scanned := 0
	for scanned < len(candidates) && scanned < candidateBudget && len(items) < limit {
		ticket := &candidates[scanned]
		scanned++
		if queue != "" && ticketQueue(ticket) != queue {
			continue
		}
		allowed, checkErr := policyBatch.Allows(services.PolicyCheckInput{
			ServicePrincipalID: principal.ID,
			CredentialID:       principal.CredentialID,
			Scope:              models.ScopeTicketsRead,
			Action:             "ticket.read",
			ResourceType:       "ticket",
			ResourceID:         strconv.FormatUint(uint64(ticket.ID), 10),
			SourceProtocol:     mcpSourceProtocol,
		})
		if checkErr != nil {
			return nil, backendError(checkErr)
		}
		if allowed {
			summary, summaryErr := ticketSummary(ticket)
			if summaryErr != nil {
				return nil, summaryErr
			}
			items = append(items, summary)
		}
	}

	hasMore := scanned < len(candidates)
	result := map[string]any{"items": items}
	if hasMore && scanned > 0 {
		last := candidates[scanned-1]
		result["next_cursor"] = EncodeCursor(Cursor{
			CreatedAt: last.CreatedAt,
			ID:        strconv.FormatUint(uint64(last.ID), 10),
		})
	}
	if _, err := policyBatch.RecordSummary(ctx, map[string]any{
		"candidate_budget":   candidateBudget,
		"candidates_scanned": scanned,
		"items_returned":     len(items),
		"items_filtered":     scanned - len(items),
		"has_more":           hasMore,
		"cursor_semantics":   "last_examined_candidate",
	}); err != nil {
		return nil, backendError(err)
	}
	return result, nil
}

func (a *MCPAdapter) getTicket(
	ctx context.Context,
	principal mcp.Principal,
	arguments map[string]any,
) (map[string]any, error) {
	ticketID, err := argumentUint(arguments, "ticket_id")
	if err != nil {
		return nil, err
	}
	ticket, err := a.getTicketByID(ctx, principal, ticketID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ticket": ticket}, nil
}

func (a *MCPAdapter) getTicketByID(
	ctx context.Context,
	principal mcp.Principal,
	ticketID uint,
) (map[string]any, error) {
	if _, err := a.checkPolicy(ctx, principal, policyRequest{
		scope:        models.ScopeTicketsRead,
		action:       "ticket.read",
		resourceType: "ticket",
		resourceID:   strconv.FormatUint(uint64(ticketID), 10),
	}); err != nil {
		return nil, err
	}
	scope, err := services.RequireProjectScope(ctx)
	if err != nil {
		return nil, backendError(err)
	}
	var ticket models.Ticket
	if err := a.db.WithContext(ctx).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			ticketID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&ticket).Error; err != nil {
		return nil, backendError(err)
	}
	return ticketDetail(&ticket)
}

func (a *MCPAdapter) ticketHistory(
	ctx context.Context,
	principal mcp.Principal,
	arguments map[string]any,
) (map[string]any, error) {
	ticketID, err := argumentUint(arguments, "ticket_id")
	if err != nil {
		return nil, err
	}
	limit, err := argumentInt(arguments, "limit", mcpDefaultLimit, 1, mcpMaximumLimit)
	if err != nil {
		return nil, err
	}
	return a.historyByTicket(ctx, principal, ticketID, argumentString(arguments, "cursor"), limit)
}

func (a *MCPAdapter) historyByTicket(
	ctx context.Context,
	principal mcp.Principal,
	ticketID uint,
	cursorValue string,
	limit int,
) (map[string]any, error) {
	if _, err := a.checkPolicy(ctx, principal, policyRequest{
		scope:        models.ScopeTicketsRead,
		action:       "ticket.history.read",
		resourceType: "ticket",
		resourceID:   strconv.FormatUint(uint64(ticketID), 10),
	}); err != nil {
		return nil, err
	}
	scope, err := services.RequireProjectScope(ctx)
	if err != nil {
		return nil, backendError(err)
	}
	var ticket models.Ticket
	if err := a.db.WithContext(ctx).
		Select("id").
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			ticketID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&ticket).Error; err != nil {
		return nil, backendError(err)
	}
	cursor, err := decodeMCPQueryCursor(cursorValue)
	if err != nil {
		return nil, err
	}
	query := a.db.WithContext(ctx).
		Preload("Event").
		Where(
			"ticket_id = ? AND organization_id = ? AND project_id = ?",
			ticketID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		Order("id DESC")
	if !cursor.CreatedAt.IsZero() {
		cursorID, parseErr := strconv.ParseUint(cursor.ID, 10, 64)
		if parseErr != nil || cursorID == 0 {
			return nil, invalidArgument("invalid cursor")
		}
		query = query.Where("id < ?", cursorID)
	}
	var histories []models.TicketHistory
	if err := query.Limit(limit + 1).Find(&histories).Error; err != nil {
		return nil, backendError(err)
	}
	hasMore := len(histories) > limit
	if hasMore {
		histories = histories[:limit]
	}

	items := make([]map[string]any, 0, len(histories))
	for i := range histories {
		item, err := historyItem(&histories[i])
		if err != nil {
			return nil, backendError(err)
		}
		items = append(items, item)
	}
	result := map[string]any{"items": items}
	if hasMore && len(histories) > 0 {
		last := histories[len(histories)-1]
		result["next_cursor"] = EncodeCursor(Cursor{
			CreatedAt: last.CreatedAt,
			ID:        strconv.FormatUint(uint64(last.ID), 10),
		})
	}
	return result, nil
}

func (a *MCPAdapter) actionCheck(
	ctx context.Context,
	principal mcp.Principal,
	arguments map[string]any,
) (map[string]any, error) {
	action := argumentString(arguments, "action")
	spec := policySpecForMCPAction(action)
	if spec.scope == "" {
		return nil, invalidArgument("unsupported action")
	}
	resourceID := ""
	if raw, ok := arguments["ticket_id"]; ok {
		ticketID, err := numericUint(raw)
		if err != nil || ticketID == 0 {
			return nil, invalidArgument("invalid ticket id")
		}
		resourceID = strconv.FormatUint(uint64(ticketID), 10)
	}
	contextValue, _ := arguments["context"].(map[string]any)
	decision, err := a.service.CheckAction(ctx, services.PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		CredentialID:       principal.CredentialID,
		Scope:              spec.scope,
		Action:             spec.action,
		ResourceType:       spec.resourceType,
		ResourceID:         resourceID,
		IsWrite:            spec.write,
		IsRisky:            spec.risky,
		RequestDigest:      requestDigest(arguments),
		SourceProtocol:     mcpSourceProtocol,
		Context:            contextValue,
	})
	if decision == nil {
		return nil, backendError(err)
	}
	if err != nil && !isServicePolicyError(err) {
		return nil, backendError(err)
	}
	return map[string]any{
		"allowed":         decision.Allowed,
		"decision_id":     decision.ID,
		"reason_code":     decision.ReasonCode,
		"required_scopes": []string{spec.scope},
	}, nil
}

func (a *MCPAdapter) reserveIdempotency(
	ctx context.Context,
	principal mcp.Principal,
	operation string,
	arguments map[string]any,
) (*services.IdempotencyReservation, error) {
	key := argumentString(arguments, "idempotency_key")
	if key == "" {
		return nil, invalidArgument("idempotency_key is required")
	}
	body, err := json.Marshal(arguments)
	if err != nil {
		return nil, invalidArgument("arguments are not JSON serializable")
	}
	reservation, err := a.service.ReserveIdempotency(
		ctx,
		principalActor(principal),
		operation,
		key,
		body,
		24*time.Hour,
	)
	if err != nil {
		return nil, backendError(err)
	}
	return reservation, nil
}

func (a *MCPAdapter) failReservation(
	ctx context.Context,
	reservation *services.IdempotencyReservation,
	err error,
) {
	if reservation == nil || reservation.Record == nil || reservation.Replayed {
		return
	}
	_ = a.service.FailIdempotency(ctx, reservation.Record.ID, services.AgentNativeErrorCode(err))
}

func (a *MCPAdapter) replayComment(
	ctx context.Context,
	record *models.IdempotencyRecord,
) (map[string]any, error) {
	receipt, err := decodeReplayReceipt(record)
	if err != nil {
		return nil, err
	}
	commentID, parseErr := safeconv.ParsePositiveUint(receipt.ResourceID)
	if parseErr != nil {
		return nil, backendError(errors.New("invalid replayed comment id"))
	}
	var comment models.TicketComment
	if len(record.ResourceSnapshot) > 0 {
		if err := json.Unmarshal(record.ResourceSnapshot, &comment); err != nil {
			return nil, backendError(errors.New("invalid replayed comment snapshot"))
		}
	} else {
		scope, scopeErr := services.RequireProjectScope(ctx)
		if scopeErr != nil {
			return nil, backendError(scopeErr)
		}
		if err := a.db.WithContext(ctx).
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				commentID,
				scope.OrganizationID,
				scope.ProjectID,
			).
			First(&comment).Error; err != nil {
			return nil, backendError(err)
		}
	}
	return map[string]any{
		"receipt": receiptMap(receipt),
		"comment": commentMap(&comment),
	}, nil
}

func (a *MCPAdapter) replayAttachment(
	ctx context.Context,
	record *models.IdempotencyRecord,
) (map[string]any, error) {
	receipt, err := decodeReplayReceipt(record)
	if err != nil {
		return nil, err
	}
	attachmentID, parseErr := safeconv.ParsePositiveUint(receipt.ResourceID)
	if parseErr != nil {
		return nil, backendError(errors.New("invalid replayed attachment id"))
	}
	var attachment models.TicketAttachment
	if len(record.ResourceSnapshot) > 0 {
		if err := json.Unmarshal(record.ResourceSnapshot, &attachment); err != nil {
			return nil, backendError(errors.New("invalid replayed attachment snapshot"))
		}
	} else {
		scope, scopeErr := services.RequireProjectScope(ctx)
		if scopeErr != nil {
			return nil, backendError(scopeErr)
		}
		if err := a.db.WithContext(ctx).
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				attachmentID,
				scope.OrganizationID,
				scope.ProjectID,
			).
			First(&attachment).Error; err != nil {
			return nil, backendError(err)
		}
	}
	return map[string]any{
		"receipt":    receiptMap(receipt),
		"attachment": attachmentMap(&attachment),
	}, nil
}

func replayReceipt(record *models.IdempotencyRecord) (map[string]any, error) {
	receipt, err := decodeReplayReceipt(record)
	if err != nil {
		return nil, err
	}
	return receiptMap(receipt), nil
}

func decodeReplayReceipt(record *models.IdempotencyRecord) (services.OperationReceipt, error) {
	if record == nil || len(record.ResponseBody) == 0 {
		return services.OperationReceipt{}, backendError(errors.New("missing idempotent response"))
	}
	var receipt services.OperationReceipt
	if err := json.Unmarshal(record.ResponseBody, &receipt); err != nil ||
		receipt.OperationID == "" ||
		receipt.ResourceID == "" ||
		receipt.EventID == "" ||
		receipt.PolicyDecisionID == "" {
		return services.OperationReceipt{}, backendError(errors.New("invalid idempotent response"))
	}
	return receipt, nil
}

func replayLeaseResult(record *models.IdempotencyRecord) (map[string]any, error) {
	receipt, err := decodeReplayReceipt(record)
	if err != nil {
		return nil, err
	}
	if len(record.ResourceSnapshot) == 0 {
		return nil, backendError(errors.New("missing idempotent lease snapshot"))
	}
	var snapshot map[string]any
	if err := json.Unmarshal(record.ResourceSnapshot, &snapshot); err != nil ||
		snapshot["lease_id"] == nil ||
		snapshot["expires_at"] == nil {
		return nil, backendError(errors.New("invalid idempotent lease snapshot"))
	}
	return map[string]any{
		"receipt":        receiptMap(receipt),
		"lease_id":       snapshot["lease_id"],
		"expires_at":     snapshot["expires_at"],
		"ticket_version": receipt.ResourceVersion,
	}, nil
}

func leaseResultMap(result *services.TicketLeaseCommandResult) map[string]any {
	return map[string]any{
		"receipt":        receiptMap(result.Receipt),
		"lease_id":       result.Lease.ID,
		"expires_at":     result.Lease.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"ticket_version": result.Lease.TicketVersion,
	}
}

func receiptMap(receipt services.OperationReceipt) map[string]any {
	changedFields := receipt.ChangedFields
	if changedFields == nil {
		changedFields = []string{}
	}
	return map[string]any{
		"operation_id":       receipt.OperationID,
		"resource_id":        receipt.ResourceID,
		"resource_version":   receipt.ResourceVersion,
		"event_id":           receipt.EventID,
		"changed_fields":     changedFields,
		"policy_decision_id": receipt.PolicyDecisionID,
	}
}

func commentMap(comment *models.TicketComment) map[string]any {
	return map[string]any{
		"id":           comment.ID,
		"ticket_id":    comment.TicketID,
		"actor":        actorMap(comment.Actor()),
		"visibility":   string(comment.Type),
		"content":      comment.Content,
		"content_type": comment.ContentType,
		"created_at":   comment.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func attachmentMap(attachment *models.TicketAttachment) map[string]any {
	visibility := "internal"
	if attachment.IsPublic {
		visibility = "public"
	}
	return map[string]any{
		"id":           attachment.ID,
		"ticket_id":    attachment.TicketID,
		"file_name":    attachment.OriginalName,
		"content_type": attachment.MimeType,
		"size":         attachment.FileSize,
		"sha256":       attachment.Hash,
		"virus_scan":   string(attachment.VirusScan),
		"visibility":   visibility,
		"created_at":   attachment.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func commandTicketVersion(arguments map[string]any) (uint, uint64, string, error) {
	ticketID, err := argumentUint(arguments, "ticket_id")
	if err != nil {
		return 0, 0, "", err
	}
	expectedVersion, err := argumentUint64(arguments, "expected_version")
	if err != nil {
		return 0, 0, "", err
	}
	return ticketID, expectedVersion, argumentString(arguments, "lease_id"), nil
}

func commandTicketVersionWithRequiredLease(arguments map[string]any) (uint, uint64, string, error) {
	ticketID, expectedVersion, leaseID, err := commandTicketVersion(arguments)
	if err != nil {
		return 0, 0, "", err
	}
	if leaseID == "" {
		return 0, 0, "", invalidParams("lease_id is required", "lease_id")
	}
	return ticketID, expectedVersion, leaseID, nil
}

func boundedMCPListCandidateBudget(limit int) int {
	budget := limit * mcpCandidateBudgetMultiple
	if budget < limit {
		budget = limit
	}
	if budget > mcpMaximumCandidateBudget {
		budget = mcpMaximumCandidateBudget
	}
	return budget
}

func principalActor(principal mcp.Principal) models.ActorRef {
	return models.ServicePrincipalActor(principal.ID)
}

func argumentStringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return uniqueStrings(typed)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return uniqueStrings(result)
	default:
		return []string{}
	}
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input)+1)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func sortedMapKeys(input map[string]any) []string {
	result := make([]string, 0, len(input))
	for key := range input {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func (a *MCPAdapter) publishTicketResources(
	ctx context.Context,
	principal mcp.Principal,
	ticketID uint,
	oldQueue,
	newQueue string,
) {
	if buffer, ok := ctx.Value(
		mcpPublicationBufferContextKey{},
	).(*mcpPublicationBuffer); ok && buffer != nil {
		buffer.items = append(buffer.items, mcpPublication{
			principal: principal,
			ticketID:  ticketID,
			oldQueue:  oldQueue,
			newQueue:  newQueue,
		})
		return
	}
	a.publishTicketResourcesNow(principal, ticketID, oldQueue, newQueue)
}

func (a *MCPAdapter) publishTicketResourcesNow(
	principal mcp.Principal,
	ticketID uint,
	oldQueue,
	newQueue string,
) {
	projectKey, err := mcpPrincipalProjectKey(principal)
	if err != nil {
		return
	}
	baseURI := "ticket://projects/" + projectKey
	uris := []string{
		fmt.Sprintf("%s/tickets/%d", baseURI, ticketID),
		fmt.Sprintf("%s/tickets/%d/history", baseURI, ticketID),
	}
	for _, queue := range []string{"default", oldQueue, newQueue} {
		if mcpQueuePattern.MatchString(queue) {
			uris = append(uris, baseURI+"/queues/"+queue)
		}
	}
	for _, uri := range uniqueStrings(uris) {
		event := mcp.ResourceEvent{URI: uri}
		select {
		case a.events <- event:
		default:
			select {
			case <-a.events:
			default:
			}
			select {
			case a.events <- event:
			default:
			}
		}
	}
}

type policySpec struct {
	scope        string
	action       string
	resourceType string
	write        bool
	risky        bool
}

type policyRequest struct {
	scope        string
	action       string
	resourceType string
	resourceID   string
	write        bool
	risky        bool
	context      map[string]any
	digest       string
}

func (a *MCPAdapter) checkPolicy(
	ctx context.Context,
	principal mcp.Principal,
	request policyRequest,
) (*models.PolicyDecision, error) {
	decision, err := a.service.CheckAction(ctx, services.PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		CredentialID:       principal.CredentialID,
		Scope:              request.scope,
		Action:             request.action,
		ResourceType:       request.resourceType,
		ResourceID:         request.resourceID,
		IsWrite:            request.write,
		IsRisky:            request.risky,
		RequestDigest:      request.digest,
		SourceProtocol:     mcpSourceProtocol,
		Context:            request.context,
	})
	if err != nil {
		return decision, backendPolicyError(err, decision)
	}
	return decision, nil
}

func policySpecForMCPAction(action string) policySpec {
	specs := map[string]policySpec{
		"ticket_list":        {models.ScopeTicketsRead, "ticket.list", "ticket", false, false},
		"ticket_get":         {models.ScopeTicketsRead, "ticket.read", "ticket", false, false},
		"ticket_create":      {models.ScopeTicketsCreate, "ticket.create", "ticket", true, false},
		"ticket_update":      {models.ScopeTicketsUpdate, "ticket.update", "ticket", true, false},
		"ticket_claim":       {models.ScopeTasksManage, "ticket.claim", "ticket", true, false},
		"ticket_heartbeat":   {models.ScopeTasksManage, "ticket.lease.heartbeat", "ticket", true, false},
		"ticket_release":     {models.ScopeTasksManage, "ticket.lease.release", "ticket", true, false},
		"ticket_assign":      {models.ScopeTicketsAssign, "ticket.assign", "ticket", true, true},
		"ticket_transition":  {models.ScopeTicketsTransition, "ticket.transition", "ticket", true, true},
		"ticket_add_comment": {models.ScopeCommentsWrite, "ticket.comment.create", "ticket", true, false},
		"ticket_attach_file": {models.ScopeAttachmentsWrite, "ticket.attachment.create", "ticket", true, false},
		"ticket_history":     {models.ScopeTicketsRead, "ticket.history.read", "ticket", false, false},
		"action_check":       {models.ScopeTicketsRead, "policy.check", "ticket", false, false},
		"resource:read":      {models.ScopeTicketsRead, "ticket.read", "ticket", false, false},
		"resource:subscribe": {models.ScopeEventsSubscribe, "ticket.subscribe", "ticket", false, false},
		"resource:stream":    {models.ScopeEventsSubscribe, "event.stream", "event", false, false},
	}
	return specs[action]
}

func ticketSummary(ticket *models.Ticket) (map[string]any, error) {
	result := map[string]any{
		"id":            ticket.ID,
		"version":       ticket.Version,
		"ticket_number": ticket.TicketNumber,
		"title":         ticket.Title,
		"type":          string(ticket.Type),
		"priority":      string(ticket.Priority),
		"status":        string(ticket.Status),
		"queue":         ticketQueue(ticket),
		"created_at":    ticket.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at":    ticket.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	assigned, err := ticketAssignedActor(ticket)
	if err != nil {
		return nil, err
	}
	if assigned != nil {
		result["assigned_to"] = actorMap(*assigned)
	}
	return result, nil
}

func ticketDetail(ticket *models.Ticket) (map[string]any, error) {
	result, err := ticketSummary(ticket)
	if err != nil {
		return nil, err
	}
	result["description"] = ticket.Description
	result["source"] = string(ticket.Source)
	result["created_by"] = actorMap(ticketCreatorActor(ticket))
	tags := []string(ticket.Tags)
	if tags == nil {
		tags = []string{}
	}
	result["tags"] = tags
	result["sla_breached"] = ticket.SLABreached
	contextValue := ticket.AgentContext.Data()
	if contextValue.Goal != "" ||
		len(contextValue.Constraints) > 0 ||
		len(contextValue.AcceptanceCriteria) > 0 ||
		len(contextValue.MissingInformation) > 0 ||
		len(contextValue.RelatedResources) > 0 {
		result["agent_context"] = contextValue
	}
	if customFields := ticket.CustomFields.Data(); customFields != nil {
		result["custom_fields"] = customFields
	}
	if ticket.DueDate != nil {
		result["due_at"] = ticket.DueDate.UTC().Format(time.RFC3339Nano)
	}
	return result, nil
}

func ticketCreatorActor(ticket *models.Ticket) models.ActorRef {
	return models.ActorRef{Type: ticket.CreatedByActorType, ID: ticket.CreatedByActorID}
}

func ticketAssignedActor(ticket *models.Ticket) (*models.ActorRef, error) {
	actorType := strings.TrimSpace(string(ticket.AssignedToActorType))
	actorID := strings.TrimSpace(ticket.AssignedToActorID)
	hasActorType := actorType != ""
	hasActorID := actorID != ""
	hasHumanProjection := ticket.AssignedToID != nil
	hasServicePrincipalProjection := ticket.AssignedToServicePrincipalID != nil

	if !hasActorType && !hasActorID {
		if hasHumanProjection || hasServicePrincipalProjection {
			return nil, ticketAssignmentIntegrityError(ticket.ID, "missing_actor")
		}
		return nil, nil
	}
	if !hasActorType || !hasActorID {
		return nil, ticketAssignmentIntegrityError(ticket.ID, "incomplete_actor")
	}
	if actorType != string(ticket.AssignedToActorType) || actorID != ticket.AssignedToActorID {
		return nil, ticketAssignmentIntegrityError(ticket.ID, "invalid_actor")
	}

	actor := models.ActorRef{Type: ticket.AssignedToActorType, ID: actorID}
	if err := actor.Validate(); err != nil {
		return nil, ticketAssignmentIntegrityError(ticket.ID, "invalid_actor")
	}
	switch actor.Type {
	case models.ActorTypeHuman:
		if _, err := safeconv.ParsePositiveUint(actor.ID); err != nil ||
			hasServicePrincipalProjection {
			return nil, ticketAssignmentIntegrityError(ticket.ID, "projection_mismatch")
		}
		if hasHumanProjection &&
			strconv.FormatUint(uint64(*ticket.AssignedToID), 10) != actor.ID {
			return nil, ticketAssignmentIntegrityError(ticket.ID, "projection_mismatch")
		}
	case models.ActorTypeServicePrincipal:
		if hasHumanProjection {
			return nil, ticketAssignmentIntegrityError(ticket.ID, "projection_mismatch")
		}
		if hasServicePrincipalProjection &&
			strings.TrimSpace(*ticket.AssignedToServicePrincipalID) != actor.ID {
			return nil, ticketAssignmentIntegrityError(ticket.ID, "projection_mismatch")
		}
	case models.ActorTypeSystem:
		if hasHumanProjection || hasServicePrincipalProjection {
			return nil, ticketAssignmentIntegrityError(ticket.ID, "projection_mismatch")
		}
	}
	return &actor, nil
}

func actorMap(actor models.ActorRef) map[string]any {
	return map[string]any{"type": string(actor.Type), "id": actor.ID}
}

func ticketQueue(ticket *models.Ticket) string {
	if fields := ticket.CustomFields.Data(); fields != nil {
		if queue, ok := fields["queue"].(string); ok && mcpQueuePattern.MatchString(queue) {
			return queue
		}
	}
	return "default"
}

func historyItem(history *models.TicketHistory) (map[string]any, error) {
	var eventID any
	actor := history.Actor()
	switch history.Provenance {
	case models.TicketHistoryProvenanceDomainEvent:
		if history.EventID == nil ||
			strings.TrimSpace(*history.EventID) == "" ||
			history.ResourceVersion == 0 ||
			history.Event == nil ||
			history.Event.ID != *history.EventID ||
			history.Event.ResourceVersion != history.ResourceVersion ||
			history.Event.Subject != fmt.Sprintf("ticket/%d", history.TicketID) ||
			history.Event.ActorType != actor.Type ||
			history.Event.ActorID != actor.ID {
			return nil, fmt.Errorf("history %d has an invalid domain event link", history.ID)
		}
		eventID = *history.EventID
	case models.TicketHistoryProvenancePreEvent, models.TicketHistoryProvenanceImported:
		if history.EventID != nil || history.ResourceVersion != 0 {
			return nil, fmt.Errorf("history %d has inconsistent unlinked provenance", history.ID)
		}
		eventID = nil
	default:
		return nil, fmt.Errorf("history %d has unknown provenance %q", history.ID, history.Provenance)
	}
	changedFields := []string{}
	if history.FieldName != "" {
		changedFields = append(changedFields, history.FieldName)
	}
	var details map[string]any
	if json.Unmarshal([]byte(history.Details), &details) == nil {
		if raw, ok := details["changed_fields"].([]any); ok {
			for _, field := range raw {
				if text, ok := field.(string); ok && text != "" {
					changedFields = append(changedFields, text)
				}
			}
		}
	}
	changedFields = uniqueStrings(changedFields)
	return map[string]any{
		"id":               history.ID,
		"ticket_id":        history.TicketID,
		"actor":            actorMap(actor),
		"action":           string(history.Action),
		"changed_fields":   changedFields,
		"reason":           history.Description,
		"event_id":         eventID,
		"resource_version": history.ResourceVersion,
		"provenance":       string(history.Provenance),
		"created_at":       history.CreatedAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

func validateMCPPrincipal(principal mcp.Principal) error {
	if principal.Type != string(models.ActorTypeServicePrincipal) ||
		strings.TrimSpace(principal.ID) == "" ||
		strings.TrimSpace(principal.CredentialID) == "" {
		return services.ErrInvalidActor
	}
	return nil
}

func mcpPrincipalProjectKey(principal mcp.Principal) (string, error) {
	if err := validateMCPPrincipal(principal); err != nil {
		return "", err
	}
	rawProjectKey, ok := principal.Attributes["project_key"].(string)
	if !ok || rawProjectKey != strings.TrimSpace(rawProjectKey) ||
		models.ValidateProjectKey(rawProjectKey) != nil {
		return "", errors.New("trusted MCP project context is required")
	}
	return rawProjectKey, nil
}

func mcpOperationContext(
	ctx context.Context,
	principal mcp.Principal,
) (context.Context, string, models.ProjectScope, error) {
	projectKey, err := mcpPrincipalProjectKey(principal)
	if err != nil {
		return nil, "", models.ProjectScope{}, err
	}
	organizationID, err := numericUint(principal.Attributes["organization_id"])
	if err != nil || organizationID == 0 {
		return nil, "", models.ProjectScope{}, errors.New("trusted MCP organization context is required")
	}
	projectID, err := numericUint(principal.Attributes["project_id"])
	if err != nil || projectID == 0 {
		return nil, "", models.ProjectScope{}, errors.New("trusted MCP project context is required")
	}
	scope := models.ProjectScope{
		OrganizationID: organizationID,
		ProjectID:      projectID,
	}
	operationContext, err := services.WithOperationContext(ctx, services.OperationContext{
		Scope:        scope,
		Actor:        principalActor(principal),
		Source:       services.SourceProtocolMCP,
		CredentialID: principal.CredentialID,
	})
	if err != nil {
		return nil, "", models.ProjectScope{}, err
	}
	return operationContext, projectKey, scope, nil
}

func validateMCPProjectArgument(arguments map[string]any, projectKey string) error {
	rawProjectKey, ok := arguments["project_key"].(string)
	if !ok || rawProjectKey == "" {
		return invalidParams("project_key is required", "project_key")
	}
	if rawProjectKey != strings.TrimSpace(rawProjectKey) ||
		models.ValidateProjectKey(rawProjectKey) != nil {
		return invalidParams("project_key must be canonical", "project_key")
	}
	if rawProjectKey != projectKey {
		return &mcp.BackendError{
			Code:    "project_scope_mismatch",
			Message: "project_key does not match the authenticated access token",
			Details: map[string]any{
				"field": "project_key",
			},
		}
	}
	return nil
}

func decodeMCPQueryCursor(value string) (Cursor, error) {
	cursor, err := DecodeCursor(value)
	if err != nil {
		return Cursor{}, invalidArgument("invalid cursor")
	}
	return cursor, nil
}

func argumentString(arguments map[string]any, key string) string {
	value, _ := arguments[key].(string)
	return strings.TrimSpace(value)
}

func argumentStrings(arguments map[string]any, key string) []string {
	raw, ok := arguments[key].([]any)
	if !ok {
		if typed, ok := arguments[key].([]string); ok {
			return uniqueStrings(typed)
		}
		return nil
	}
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			values = append(values, strings.TrimSpace(text))
		}
	}
	return uniqueStrings(values)
}

func argumentUint(arguments map[string]any, key string) (uint, error) {
	value, ok := arguments[key]
	if !ok {
		return 0, invalidArgument(key + " is required")
	}
	result, err := numericUint(value)
	if err != nil || result == 0 {
		return 0, invalidArgument("invalid " + key)
	}
	return result, nil
}

func argumentUint64(arguments map[string]any, key string) (uint64, error) {
	value, ok := arguments[key]
	if !ok {
		return 0, invalidArgument(key + " is required")
	}
	result, err := numericUint64(value)
	if err != nil || result == 0 {
		return 0, invalidArgument("invalid " + key)
	}
	return result, nil
}

func argumentInt(
	arguments map[string]any,
	key string,
	defaultValue, minimum, maximum int,
) (int, error) {
	value, ok := arguments[key]
	if !ok {
		return defaultValue, nil
	}
	number, err := numericPositiveInt(value)
	if err != nil || number < minimum || number > maximum {
		return 0, invalidArgument(fmt.Sprintf("%s must be between %d and %d", key, minimum, maximum))
	}
	return number, nil
}

func numericUint(value any) (uint, error) {
	number, err := numericUint64(value)
	if err != nil {
		return 0, err
	}
	return safeconv.PositiveUint(number)
}

func numericUint64(value any) (uint64, error) {
	switch typed := value.(type) {
	case uint:
		return uint64(typed), nil
	case uint64:
		return typed, nil
	case uint32:
		return uint64(typed), nil
	case int:
		if typed > 0 {
			return uint64(typed), nil
		}
	case int64:
		if typed > 0 {
			return uint64(typed), nil
		}
	case int32:
		if typed > 0 {
			return uint64(typed), nil
		}
	case float64:
		if typed > 0 && typed < math.Ldexp(1, 64) && math.Trunc(typed) == typed {
			return uint64(typed), nil
		}
	case json.Number:
		return strconv.ParseUint(string(typed), 10, 64)
	}
	return 0, errors.New("not a positive integer")
}

func numericPositiveInt(value any) (int, error) {
	switch typed := value.(type) {
	case int:
		if typed > 0 {
			return typed, nil
		}
	case int64:
		if typed > 0 {
			return safeconv.Int(typed)
		}
	case int32:
		if typed > 0 {
			return int(typed), nil
		}
	case uint:
		if uint64(typed) <= uint64(math.MaxInt) {
			return int(typed), nil
		}
	case uint64:
		if typed <= uint64(math.MaxInt) {
			return int(typed), nil
		}
	case uint32:
		if uint64(typed) <= uint64(math.MaxInt) {
			return int(typed), nil
		}
	case float64:
		if typed > 0 && typed < math.Ldexp(1, strconv.IntSize-1) && math.Trunc(typed) == typed {
			return int(typed), nil
		}
	case json.Number:
		number, err := strconv.ParseInt(string(typed), 10, strconv.IntSize)
		if err == nil && number > 0 && number <= int64(math.MaxInt) {
			return int(number), nil
		}
	}
	return 0, errors.New("not a positive integer")
}

func parsePositiveUintString(value, label string) (uint, error) {
	number, err := safeconv.ParsePositiveUint(value)
	if err != nil {
		return 0, invalidArgument("invalid " + label)
	}
	return number, nil
}

func requestDigest(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func intersectScopes(tokenScopes, principalScopes []string) []string {
	allowed := make(map[string]struct{}, len(principalScopes))
	for _, scope := range principalScopes {
		allowed[scope] = struct{}{}
	}
	result := make([]string, 0, len(tokenScopes))
	for _, scope := range tokenScopes {
		if _, ok := allowed[scope]; ok {
			result = append(result, scope)
		}
	}
	return uniqueStrings(result)
}

func invalidArgument(message string) *mcp.BackendError {
	return &mcp.BackendError{Code: "invalid_argument", Message: message}
}

func invalidParams(message, field string) *mcp.BackendError {
	return &mcp.BackendError{
		Code:    "invalid_params",
		Message: message,
		Details: map[string]any{"field": field},
	}
}

func ticketAssignmentIntegrityError(ticketID uint, reasonCode string) *mcp.BackendError {
	return &mcp.BackendError{
		Code:    "data_integrity_error",
		Message: "ticket assignment data is inconsistent",
		Details: map[string]any{
			"resource_type": "ticket",
			"resource_id":   strconv.FormatUint(uint64(ticketID), 10),
			"field":         "assigned_to_actor",
			"reason_code":   reasonCode,
		},
	}
}

func backendPolicyError(err error, decision *models.PolicyDecision) *mcp.BackendError {
	failure := backendError(err)
	if decision != nil {
		if failure.Details == nil {
			failure.Details = map[string]any{}
		}
		failure.Details["policy_decision_id"] = decision.ID
		failure.Details["reason_code"] = decision.ReasonCode
	}
	return failure
}

func backendError(err error) *mcp.BackendError {
	if err == nil {
		return &mcp.BackendError{Code: "internal_error", Message: "operation failed"}
	}
	var safeBackendError *mcp.BackendError
	if errors.As(err, &safeBackendError) && safeBackendError != nil {
		return safeBackendError
	}
	code := services.AgentNativeErrorCode(err)
	retryable := false
	message := "operation failed"
	switch {
	case errors.Is(err, services.ErrInvalidAssignee):
		code, message = "invalid_argument", "assignee is invalid"
	case errors.Is(err, services.ErrAssigneeNotFound):
		code, message = "not_found", "assignee not found"
	case errors.Is(err, services.ErrAssigneePolicyDenied):
		code, message = "policy_denied", "assignee is unavailable"
	case errors.Is(err, gorm.ErrRecordNotFound):
		code, message = "not_found", "resource not found"
	case errors.Is(err, services.ErrVersionConflict):
		code, message, retryable = "version_conflict", "ticket version changed", true
	case errors.Is(err, services.ErrLeaseConflict),
		errors.Is(err, services.ErrLeaseExpired),
		errors.Is(err, services.ErrLeaseNotOwned):
		code, message, retryable = services.AgentNativeErrorCode(err), "ticket lease conflict", true
	case errors.Is(err, services.ErrPolicyDenied),
		errors.Is(err, services.ErrProjectAccessDenied),
		errors.Is(err, services.ErrGlobalEmergencyStop),
		errors.Is(err, services.ErrReadOnlyMode),
		errors.Is(err, services.ErrPrincipalDisabled),
		errors.Is(err, services.ErrPrincipalExpired):
		code, message = "policy_denied", "action denied by policy"
	case errors.Is(err, services.ErrRateLimited),
		errors.Is(err, services.ErrConcurrencyLimit),
		errors.Is(err, services.ErrAutomationLoop),
		errors.Is(err, services.ErrIdempotencyInProgress):
		code, message, retryable = services.AgentNativeErrorCode(err), "operation is temporarily unavailable", true
	case errors.Is(err, services.ErrExecutionGuardUnavailable):
		code, message, retryable = "service_unavailable", "execution protection is temporarily unavailable", true
	case errors.Is(err, services.ErrIdempotencyConflict):
		code, message = "idempotency_conflict", "idempotency key conflicts with another request"
	case errors.Is(err, services.ErrAttachmentTooLarge),
		errors.Is(err, services.ErrAttachmentNotClean),
		errors.Is(err, services.ErrInvalidAttachment),
		errors.Is(err, services.ErrInvalidAttachmentName),
		errors.Is(err, services.ErrAttachmentStorageMissing):
		code, message = "attachment_rejected", "attachment was rejected"
	case errors.Is(err, services.ErrInvalidActor),
		errors.Is(err, services.ErrInvalidScope),
		errors.Is(err, services.ErrInvalidTicketTransition):
		code, message = "invalid_argument", "request is invalid"
	}
	if code == "" {
		code = "internal_error"
	}
	return &mcp.BackendError{Code: code, Message: message, Retryable: retryable}
}

func isServicePolicyError(err error) bool {
	return errors.Is(err, services.ErrPolicyDenied) ||
		errors.Is(err, services.ErrProjectAccessDenied) ||
		errors.Is(err, services.ErrGlobalEmergencyStop) ||
		errors.Is(err, services.ErrReadOnlyMode) ||
		errors.Is(err, services.ErrPrincipalDisabled) ||
		errors.Is(err, services.ErrPrincipalExpired)
}

func decodeBase64Attachment(encoded, expectedSHA string) ([]byte, error) {
	content, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, invalidArgument("attachment content is not valid base64")
	}
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != strings.ToLower(expectedSHA) {
		return nil, invalidArgument("attachment sha256 does not match content")
	}
	return content, nil
}
