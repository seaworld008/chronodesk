package services

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestNativeCommandTokenScopeValidationUsesCanonicalPolicyMapping(
	t *testing.T,
) {
	tests := []struct {
		kind  NativeCommandAuthorizationKind
		scope string
	}{
		{NativeCommandTicketCreate, models.ScopeTicketsCreate},
		{NativeCommandTicketQuery, models.ScopeTicketsRead},
		{NativeCommandTicketClaim, models.ScopeTasksManage},
		{NativeCommandLeaseHeartbeat, models.ScopeTasksManage},
		{NativeCommandLeaseRelease, models.ScopeTasksManage},
		{NativeCommandTicketUpdate, models.ScopeTicketsUpdate},
		{NativeCommandTicketTransit, models.ScopeTicketsTransition},
		{NativeCommandTicketAssign, models.ScopeTicketsAssign},
		{NativeCommandCommentCreate, models.ScopeCommentsWrite},
		{NativeCommandTicketEscalate, models.ScopeTicketsTransition},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			required, err := NativeCommandRequiredScope(test.kind)
			if err != nil || required != test.scope {
				t.Fatalf(
					"required scope = %q, %v; want %q",
					required,
					err,
					test.scope,
				)
			}
			if err := ValidateNativeCommandTokenScopes(
				test.kind,
				[]string{models.ScopeTasksManage, test.scope},
			); err != nil {
				t.Fatalf("validate required scope: %v", err)
			}
			if test.scope != models.ScopeTasksManage {
				if err := ValidateNativeCommandTokenScopes(
					test.kind,
					[]string{models.ScopeTasksManage},
				); !errors.Is(err, ErrPolicyDenied) {
					t.Fatalf(
						"narrow token error = %v, want policy denied",
						err,
					)
				}
			}
		})
	}
}

func TestNativeCommandTokenScopeValidationFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		kind   NativeCommandAuthorizationKind
		scopes []string
	}{
		{
			name: "missing",
			kind: NativeCommandTicketQuery,
		},
		{
			name:   "empty",
			kind:   NativeCommandTicketQuery,
			scopes: []string{""},
		},
		{
			name:   "unsupported",
			kind:   NativeCommandTicketQuery,
			scopes: []string{"tickets:superuser"},
		},
		{
			name:   "unsupported command",
			kind:   NativeCommandAuthorizationKind("ticket.unknown"),
			scopes: []string{models.ScopeTicketsRead},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateNativeCommandTokenScopes(
				test.kind,
				test.scopes,
			); err == nil {
				t.Fatal("invalid token scope snapshot was accepted")
			}
		})
	}
}

func TestNativeCommandEscalationWithAssigneeAddsCanonicalAssignmentPolicy(
	t *testing.T,
) {
	assignee := models.HumanActor(42)
	checks, err := nativeCommandAdditionalPolicyChecks(
		NativeCommandAuthorizationInput{
			Kind:           NativeCommandTicketEscalate,
			Actor:          models.ServicePrincipalActor("agent-1"),
			CredentialID:   "credential-1",
			TicketID:       91,
			Assignee:       &assignee,
			RequestDigest:  "digest-1",
			SourceProtocol: string(SourceProtocolAgentREST),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 {
		t.Fatalf("additional policy checks = %d, want 1", len(checks))
	}
	check := checks[0]
	if check.Scope != models.ScopeTicketsAssign ||
		check.Action != "ticket.assign" ||
		check.ResourceID != "91" ||
		!check.IsWrite ||
		!check.IsRisky ||
		len(check.Context) != 0 {
		t.Fatalf("assignment policy check = %+v", check)
	}

	withoutAssignee, err := nativeCommandAdditionalPolicyChecks(
		NativeCommandAuthorizationInput{
			Kind: NativeCommandTicketEscalate,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutAssignee) != 0 {
		t.Fatalf(
			"escalation without assignee added policy checks: %+v",
			withoutAssignee,
		)
	}
}

func TestPreparedNativeCommandRejectsPolicyEpochChangeBeforeMutation(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "policy-epoch")
	service := NewAgentNativeService(db)
	principal := createNativePrincipal(
		t,
		service,
		user.ID,
		"policy-epoch-agent",
		models.ScopeTicketsUpdate,
		models.ScopeTasksManage,
	)
	actor := models.ServicePrincipalActor(principal.ID)
	ctx := testProjectOperationContext(t, db, actor)
	ensureAttachmentTestAuthorization(t, db, ctx, actor)
	ticket := seedNativeTicket(
		t,
		db,
		user.ID,
		"POLICY-EPOCH-001",
	)
	lease, err := service.claimTicketLease(
		ctx,
		ticket.ID,
		actor,
		ticket.Version,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	authorization := NativeCommandAuthorizationInput{
		Kind:         NativeCommandTicketUpdate,
		Actor:        actor,
		CredentialID: "test-credential",
		TokenScopes: []string{
			models.ScopeTicketsUpdate,
		},
		TicketID:       ticket.ID,
		RequestDigest:  "policy-epoch-request",
		SourceProtocol: string(SourceProtocolAgentREST),
	}
	authorizedCtx, err :=
		service.AuthorizeNativeCommandInShortProjectTransactions(
			ctx,
			authorization,
		)
	if err != nil {
		t.Fatalf("prepare command policy: %v", err)
	}
	var decisionsBefore int64
	if err := db.Model(&models.PolicyDecision{}).
		Count(&decisionsBefore).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateAgentPolicy(
		ctx,
		CreateAgentPolicyInput{
			ServicePrincipalID: principal.ID,
			Name:               "deny prepared update",
			Effect:             models.AgentPolicyEffectDeny,
			Scope:              models.ScopeTicketsUpdate,
			Action:             "ticket.update",
			ResourceType:       "ticket",
			ResourceID:         "1",
			Priority:           1000,
		},
	); err != nil {
		t.Fatalf("create intervening deny policy: %v", err)
	}
	eventSubject := fmt.Sprintf("ticket/%d", ticket.ID)
	var eventsBefore, outboxBefore, idempotencyBefore int64
	if err := db.Model(&models.DomainEvent{}).
		Where("subject = ?", eventSubject).
		Count(&eventsBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxDelivery{}).
		Count(&outboxBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.IdempotencyRecord{}).
		Count(&idempotencyBefore).Error; err != nil {
		t.Fatal(err)
	}

	var mutationErr error
	err = service.RunProjectOperation(
		authorizedCtx,
		func(scopedContext context.Context) error {
			if _, revalidateErr :=
				service.RevalidatePrincipalProjectOperation(
					scopedContext,
					models.ScopeTicketsUpdate,
				); revalidateErr != nil {
				return revalidateErr
			}
			_, mutationErr = service.UpdateTicketVersion(
				scopedContext,
				VersionedTicketUpdateInput{
					TicketID:        ticket.ID,
					ExpectedVersion: ticket.Version,
					LeaseID:         lease.ID,
					Actor:           actor,
					CredentialID:    "test-credential",
					RequiredScope:   models.ScopeTicketsUpdate,
					Action:          "ticket.update",
					SourceProtocol: string(
						SourceProtocolAgentREST,
					),
					RequestDigest: "policy-epoch-request",
					Changes: map[string]any{
						"title": "must not commit",
					},
				},
			)
			return mutationErr
		},
	)
	if err == nil {
		err = mutationErr
	}
	if !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf(
			"mutation after policy epoch change error = %v",
			err,
		)
	}
	var persisted models.Ticket
	if err := db.First(&persisted, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Version != ticket.Version ||
		persisted.Title != ticket.Title {
		t.Fatalf(
			"stale decision mutated ticket: before=%+v after=%+v",
			ticket,
			persisted,
		)
	}
	var decisionsAfter int64
	if err := db.Model(&models.PolicyDecision{}).
		Count(&decisionsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if decisionsAfter != decisionsBefore {
		t.Fatalf(
			"failed final mutation persisted decisions: before=%d after=%d",
			decisionsBefore,
			decisionsAfter,
		)
	}
	var (
		businessEvents   int64
		outboxAfter      int64
		idempotencyAfter int64
	)
	if err := db.Model(&models.DomainEvent{}).
		Where("subject = ?", eventSubject).
		Count(&businessEvents).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxDelivery{}).
		Count(&outboxAfter).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.IdempotencyRecord{}).
		Count(&idempotencyAfter).Error; err != nil {
		t.Fatal(err)
	}
	if businessEvents != eventsBefore ||
		outboxAfter != outboxBefore ||
		idempotencyAfter != idempotencyBefore {
		t.Fatalf(
			"failed final mutation effects: event %d→%d outbox %d→%d idempotency %d→%d",
			eventsBefore,
			businessEvents,
			outboxBefore,
			outboxAfter,
			idempotencyBefore,
			idempotencyAfter,
		)
	}
}
