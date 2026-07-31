package services

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

// NativeCommandAuthorizationKind identifies one canonical domain command.
// Protocol adapters map structured wire commands to these kinds; scope,
// action, risk and external-notification policy remain owned by services.
type NativeCommandAuthorizationKind string

const (
	NativeCommandTicketCreate   NativeCommandAuthorizationKind = "ticket.create"
	NativeCommandTicketQuery    NativeCommandAuthorizationKind = "ticket.query"
	NativeCommandTicketClaim    NativeCommandAuthorizationKind = "ticket.claim"
	NativeCommandLeaseHeartbeat NativeCommandAuthorizationKind = "ticket.lease.heartbeat"
	NativeCommandLeaseRelease   NativeCommandAuthorizationKind = "ticket.lease.release"
	NativeCommandTicketUpdate   NativeCommandAuthorizationKind = "ticket.update"
	NativeCommandTicketTransit  NativeCommandAuthorizationKind = "ticket.transition"
	NativeCommandTicketAssign   NativeCommandAuthorizationKind = "ticket.assign"
	NativeCommandCommentCreate  NativeCommandAuthorizationKind = "ticket.comment.create"
	NativeCommandTicketEscalate NativeCommandAuthorizationKind = "ticket.escalate"
)

// NativeCommandAuthorizationInput carries trusted command identity and
// resource data. It deliberately contains no caller-selected scope, action,
// risk or external-delivery flags.
type NativeCommandAuthorizationInput struct {
	Kind            NativeCommandAuthorizationKind
	Actor           models.ActorRef
	CredentialID    string
	TokenScopes     []string
	TicketID        uint
	LeaseID         string
	Assignee        *models.ActorRef
	RequestDigest   string
	SourceProtocol  string
	DecisionContext map[string]any
}

// NativeCommandRequiredScope returns the canonical OAuth and principal scope
// for one domain command. Protocol adapters must use this mapping instead of
// maintaining parallel scope switches.
func NativeCommandRequiredScope(
	kind NativeCommandAuthorizationKind,
) (string, error) {
	primary, _, err := nativeCommandPrimaryPolicyCheck(
		NativeCommandAuthorizationInput{Kind: kind},
	)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(primary.Scope) == "" {
		return "", fmt.Errorf(
			"%w: native command %q has no required scope",
			ErrInvalidScope,
			kind,
		)
	}
	return primary.Scope, nil
}

// ValidateNativeCommandTokenScopes attenuates a Service Principal's live
// authority to the scopes carried by the already verified OAuth access token.
// Missing, malformed, or unsupported token scopes fail closed.
func ValidateNativeCommandTokenScopes(
	kind NativeCommandAuthorizationKind,
	tokenScopes []string,
) error {
	required, err := NativeCommandRequiredScope(kind)
	if err != nil {
		return err
	}
	return validateAgentTokenScope(tokenScopes, required)
}

func validateAgentTokenScope(
	tokenScopes []string,
	required string,
) error {
	normalized, err := normalizeAgentScopes(tokenScopes)
	if err != nil {
		return err
	}
	if len(normalized) == 0 {
		return fmt.Errorf("%w: access token scopes are missing", ErrInvalidScope)
	}
	for _, scope := range normalized {
		if scope == required {
			return nil
		}
	}
	return fmt.Errorf(
		"%w: access token does not grant required scope %s",
		ErrPolicyDenied,
		required,
	)
}

// AuthorizeNativeCommandInShortProjectTransactions prepares the canonical
// command and external-notification decisions, invoking the distributed
// execution guard only between short project transactions. The returned
// context binds those decisions to the exact later domain checks.
func (s *AgentNativeService) AuthorizeNativeCommandInShortProjectTransactions(
	ctx context.Context,
	input NativeCommandAuthorizationInput,
) (context.Context, error) {
	if s == nil {
		return nil, errors.New("Agent service is required")
	}
	if err := input.Actor.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	if input.Actor.Type != models.ActorTypeServicePrincipal {
		return ctx, nil
	}
	if err := ValidateNativeCommandTokenScopes(
		input.Kind,
		input.TokenScopes,
	); err != nil {
		return nil, err
	}
	if err := s.prepareNativeCommandAuthorizationResource(
		ctx,
		&input,
	); err != nil {
		return nil, err
	}
	primary, external, err := nativeCommandPolicyPlan(input)
	if err != nil {
		return nil, err
	}
	primaryDecision, err := s.CheckActionInShortProjectTransactions(
		ctx,
		primary,
	)
	if err != nil {
		return nil, err
	}
	authorizations := []PolicyDecisionAuthorization{{
		Input:      primary,
		DecisionID: primaryDecision.ID,
	}}
	additional, err := nativeCommandAdditionalPolicyChecks(input)
	if err != nil {
		return nil, err
	}
	for _, check := range additional {
		if err := validateAgentTokenScope(
			input.TokenScopes,
			check.Scope,
		); err != nil {
			return nil, err
		}
		decision, checkErr :=
			s.CheckActionInShortProjectTransactions(ctx, check)
		if checkErr != nil {
			return nil, checkErr
		}
		authorizations = append(
			authorizations,
			PolicyDecisionAuthorization{
				Input:      check,
				DecisionID: decision.ID,
			},
		)
	}
	if external != nil {
		var (
			externalDecision *models.PolicyDecision
			externalErr      error
		)
		if tokenScopeErr := validateAgentTokenScope(
			input.TokenScopes,
			external.Scope,
		); tokenScopeErr == nil {
			externalDecision, externalErr =
				s.CheckActionInShortProjectTransactions(ctx, *external)
		} else if errors.Is(tokenScopeErr, ErrPolicyDenied) {
			externalDecision, externalErr =
				s.denyPolicyCheckForTokenScopeInShortProjectTransactions(
					ctx,
					*external,
				)
		} else {
			return nil, tokenScopeErr
		}
		if externalDecision != nil {
			authorizations = append(
				authorizations,
				PolicyDecisionAuthorization{
					Input:      *external,
					DecisionID: externalDecision.ID,
				},
			)
		}
		if externalErr != nil &&
			!errors.Is(externalErr, ErrPolicyDenied) &&
			!errors.Is(externalErr, ErrAutomationLoop) {
			return nil, externalErr
		}
		if externalDecision == nil {
			return nil, errors.New(
				"external notification policy decision is unavailable",
			)
		}
	}
	return s.WithPolicyDecisionAuthorizations(
		ctx,
		authorizations...,
	)
}

func nativeCommandAdditionalPolicyChecks(
	input NativeCommandAuthorizationInput,
) ([]PolicyCheckInput, error) {
	if input.Kind != NativeCommandTicketEscalate ||
		input.Assignee == nil {
		return nil, nil
	}
	if err := input.Assignee.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAssignee, err)
	}
	return []PolicyCheckInput{{
		ServicePrincipalID: input.Actor.ID,
		CredentialID:       input.CredentialID,
		Scope:              models.ScopeTicketsAssign,
		Action:             "ticket.assign",
		ResourceType:       "ticket",
		ResourceID: strconv.FormatUint(
			uint64(input.TicketID),
			10,
		),
		IsWrite:        true,
		IsRisky:        true,
		RequestDigest:  input.RequestDigest,
		SourceProtocol: input.SourceProtocol,
	}}, nil
}

// AuthorizeNativeCommandReplayInShortProjectTransactions rechecks only the
// canonical primary command policy for an idempotent replay. Replays never
// authorize or emit a second external notification.
func (s *AgentNativeService) AuthorizeNativeCommandReplayInShortProjectTransactions(
	ctx context.Context,
	input NativeCommandAuthorizationInput,
) error {
	if s == nil {
		return errors.New("Agent service is required")
	}
	if err := input.Actor.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	if input.Actor.Type != models.ActorTypeServicePrincipal {
		return nil
	}
	if err := ValidateNativeCommandTokenScopes(
		input.Kind,
		input.TokenScopes,
	); err != nil {
		return err
	}
	primary, _, err := nativeCommandPrimaryPolicyCheck(input)
	if err != nil {
		return err
	}
	if _, err = s.CheckActionInShortProjectTransactions(
		ctx,
		primary,
	); err != nil {
		return err
	}
	additional, err := nativeCommandAdditionalPolicyChecks(input)
	if err != nil {
		return err
	}
	for _, check := range additional {
		if err := validateAgentTokenScope(
			input.TokenScopes,
			check.Scope,
		); err != nil {
			return err
		}
		if _, err := s.CheckActionInShortProjectTransactions(
			ctx,
			check,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *AgentNativeService) denyPolicyCheckForTokenScopeInShortProjectTransactions(
	ctx context.Context,
	input PolicyCheckInput,
) (*models.PolicyDecision, error) {
	var prepared *preparedPolicyCheck
	if err := s.RunProjectOperation(
		ctx,
		func(scopedContext context.Context) error {
			var prepareErr error
			prepared, prepareErr = s.preparePolicyCheck(scopedContext, input)
			return prepareErr
		},
	); err != nil {
		return nil, err
	}
	prepared.decision.Allowed = false
	prepared.decision.ReasonCode = "token_scope_not_granted"
	prepared.decision.MatchedPolicyID = ""
	prepared.guardRequired = false

	var (
		decision   *models.PolicyDecision
		outcomeErr error
	)
	if err := s.RunProjectOperation(
		ctx,
		func(scopedContext context.Context) error {
			var persistErr error
			decision, persistErr = s.persistPreparedPolicyCheck(
				scopedContext,
				prepared,
			)
			if persistErr != nil {
				return persistErr
			}
			outcomeErr = policyDecisionOutcome(decision)
			return nil
		},
	); err != nil {
		return nil, err
	}
	return decision, outcomeErr
}

// ValidateNativeCommandAuthorization consumes an exact prepared decision
// inside the later business transaction.
func (s *AgentNativeService) ValidateNativeCommandAuthorization(
	ctx context.Context,
	input NativeCommandAuthorizationInput,
) error {
	primary, _, err := nativeCommandPrimaryPolicyCheck(input)
	if err != nil {
		return err
	}
	_, err = s.CheckAction(ctx, primary)
	return err
}

func (s *AgentNativeService) prepareNativeCommandAuthorizationResource(
	ctx context.Context,
	input *NativeCommandAuthorizationInput,
) error {
	if input == nil {
		return errors.New("native command authorization input is required")
	}
	switch input.Kind {
	case NativeCommandTicketAssign:
		if input.Assignee == nil {
			return nil
		}
		return s.RunProjectOperation(
			ctx,
			func(scopedContext context.Context) error {
				_, err := s.ResolveTicketAssignmentChanges(
					scopedContext,
					input.Assignee,
				)
				return err
			},
		)
	case NativeCommandLeaseHeartbeat, NativeCommandLeaseRelease:
		leaseID := strings.TrimSpace(input.LeaseID)
		if leaseID == "" {
			return errors.New("lease release authorization requires a lease id")
		}
		requestedTicketID := input.TicketID
		return s.RunProjectOperation(
			ctx,
			func(scopedContext context.Context) error {
				var lease models.TicketLease
				if err := s.dbForContext(scopedContext).
					Select("ticket_id").
					First(&lease, "id = ?", leaseID).
					Error; err != nil {
					return err
				}
				if requestedTicketID != 0 &&
					requestedTicketID != lease.TicketID {
					return fmt.Errorf(
						"%w: lease belongs to ticket %d, not %d",
						ErrCommandScopeMismatch,
						lease.TicketID,
						requestedTicketID,
					)
				}
				input.TicketID = lease.TicketID
				return nil
			},
		)
	default:
		return nil
	}
}

func nativeCommandPolicyPlan(
	input NativeCommandAuthorizationInput,
) (PolicyCheckInput, *PolicyCheckInput, error) {
	primary, needsExternalNotification, err :=
		nativeCommandPrimaryPolicyCheck(input)
	if err != nil {
		return PolicyCheckInput{}, nil, err
	}
	if !needsExternalNotification {
		return primary, nil, nil
	}
	external := externalNotificationPolicyCheck(
		input.Actor,
		input.CredentialID,
		primary.ResourceID,
		input.RequestDigest,
		input.SourceProtocol,
	)
	return primary, &external, nil
}

func nativeCommandPrimaryPolicyCheck(
	input NativeCommandAuthorizationInput,
) (PolicyCheckInput, bool, error) {
	resourceID := strconv.FormatUint(uint64(input.TicketID), 10)
	primary := PolicyCheckInput{
		ServicePrincipalID: input.Actor.ID,
		CredentialID:       input.CredentialID,
		ResourceType:       "ticket",
		ResourceID:         resourceID,
		RequestDigest:      input.RequestDigest,
		SourceProtocol:     input.SourceProtocol,
		Context:            input.DecisionContext,
	}
	needsExternalNotification := false
	switch input.Kind {
	case NativeCommandTicketCreate:
		primary.Scope = models.ScopeTicketsCreate
		primary.Action = "ticket.create"
		primary.ResourceID = ""
		primary.IsWrite = true
		needsExternalNotification = true
	case NativeCommandTicketQuery:
		primary.Scope = models.ScopeTicketsRead
		primary.Action = "ticket.query"
		primary.RequestDigest = ""
	case NativeCommandTicketClaim:
		primary.Scope = models.ScopeTasksManage
		primary.Action = "ticket.claim"
		primary.IsWrite = true
	case NativeCommandLeaseHeartbeat:
		primary.Scope = models.ScopeTasksManage
		primary.Action = "ticket.lease.heartbeat"
		primary.IsWrite = true
	case NativeCommandLeaseRelease:
		primary.Scope = models.ScopeTasksManage
		primary.Action = "ticket.lease.release"
		primary.IsWrite = true
	case NativeCommandTicketUpdate:
		primary.Scope = models.ScopeTicketsUpdate
		primary.Action = "ticket.update"
		primary.IsWrite = true
		needsExternalNotification = true
	case NativeCommandTicketTransit:
		primary.Scope = models.ScopeTicketsTransition
		primary.Action = "ticket.transition"
		primary.IsWrite = true
		primary.IsRisky = true
		needsExternalNotification = true
	case NativeCommandTicketAssign:
		primary.Scope = models.ScopeTicketsAssign
		primary.Action = "ticket.assign"
		primary.IsWrite = true
		primary.IsRisky = true
		needsExternalNotification = true
	case NativeCommandCommentCreate:
		primary.Scope = models.ScopeCommentsWrite
		primary.Action = "ticket.comment.create"
		primary.IsWrite = true
		needsExternalNotification = true
	case NativeCommandTicketEscalate:
		primary.Scope = models.ScopeTicketsTransition
		primary.Action = "ticket.escalate"
		primary.IsWrite = true
		primary.IsRisky = true
		needsExternalNotification = true
	default:
		return PolicyCheckInput{}, false, fmt.Errorf(
			"unsupported native command authorization kind %q",
			input.Kind,
		)
	}
	return primary, needsExternalNotification, nil
}

func nativeMutationCommandKind(
	scope string,
	action string,
) (NativeCommandAuthorizationKind, bool) {
	switch {
	case scope == models.ScopeTicketsUpdate && action == "ticket.update":
		return NativeCommandTicketUpdate, true
	case scope == models.ScopeTicketsTransition && action == "ticket.transition":
		return NativeCommandTicketTransit, true
	case scope == models.ScopeTicketsAssign && action == "ticket.assign":
		return NativeCommandTicketAssign, true
	case scope == models.ScopeTicketsTransition && action == "ticket.escalate":
		return NativeCommandTicketEscalate, true
	default:
		return "", false
	}
}

func nativeLeaseCommandKind(
	action string,
) (NativeCommandAuthorizationKind, bool) {
	switch action {
	case "ticket.claim":
		return NativeCommandTicketClaim, true
	case "ticket.lease.heartbeat":
		return NativeCommandLeaseHeartbeat, true
	case "ticket.lease.release":
		return NativeCommandLeaseRelease, true
	default:
		return "", false
	}
}

func externalNotificationPolicyCheck(
	actor models.ActorRef,
	credentialID string,
	resourceID string,
	requestDigest string,
	sourceProtocol string,
) PolicyCheckInput {
	return PolicyCheckInput{
		ServicePrincipalID: actor.ID,
		CredentialID:       credentialID,
		Scope:              models.ScopeEventsSubscribe,
		Action:             externalNotificationAction,
		ResourceType:       "ticket",
		ResourceID:         resourceID,
		IsWrite:            true,
		IsRisky:            true,
		RequestDigest:      requestDigest,
		SourceProtocol:     sourceProtocol,
	}
}
