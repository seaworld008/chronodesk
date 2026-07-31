package agentplatform

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/seaworld008/chronodesk/server/internal/a2a"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestA2ATaskSnapshotDecisionUsesShortProjectTransaction(t *testing.T) {
	fixture := newA2AAdapterFixture(t)
	const callbackName = "test:require_snapshot_policy_project_transaction"
	decisionCreates := 0
	if err := fixture.db.Callback().Create().
		Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			decision, ok := tx.Statement.Dest.(*models.PolicyDecision)
			if !ok {
				return
			}
			decisionCreates++
			if !scopeddb.HasTransaction(tx.Statement.Context) {
				_ = tx.AddError(errors.New(
					"snapshot decision is outside a project transaction",
				))
				return
			}
			if decision.OrganizationID != fixture.organization.ID ||
				decision.ProjectID != fixture.project.ID ||
				!strings.Contains(
					string(decision.Context),
					`"stored_snapshot":true`,
				) {
				_ = tx.AddError(errors.New(
					"snapshot decision is missing trusted project provenance",
				))
			}
		}); err != nil {
		t.Fatalf("register snapshot decision assertion: %v", err)
	}

	ticketID := uint(731)
	task := a2a.Task{
		ID:             "task-completed-snapshot-rls",
		ContextID:      "context-completed-snapshot-rls",
		LinkedTicketID: &ticketID,
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
		},
	}
	ctx := context.WithoutCancel(a2aFixtureContext(t, fixture))
	authorizer := NewA2ATaskListAuthorizer(fixture.native)
	allowed, err := authorizer.AuthorizeTaskSnapshot(ctx, task)
	if err != nil || !allowed {
		t.Fatalf("allowed snapshot authorization = %v, %v", allowed, err)
	}
	if _, err := fixture.native.CreateAgentPolicy(
		context.Background(),
		services.CreateAgentPolicyInput{
			ServicePrincipalID: fixture.principal.ID,
			Name:               "deny completed snapshot",
			Effect:             models.AgentPolicyEffectDeny,
			Scope:              models.ScopeTicketsRead,
			Action:             "ticket.read",
			ResourceType:       "ticket",
			ResourceID:         strconvUint(ticketID),
			Priority:           100,
		},
	); err != nil {
		t.Fatalf("create snapshot deny policy: %v", err)
	}
	allowed, err = authorizer.AuthorizeTaskSnapshot(ctx, task)
	if err != nil || allowed {
		t.Fatalf("denied snapshot authorization = %v, %v", allowed, err)
	}
	if decisionCreates != 2 {
		t.Fatalf(
			"snapshot authorization persisted %d decisions, want 2",
			decisionCreates,
		)
	}
	var denied int64
	if err := fixture.db.Model(&models.PolicyDecision{}).
		Where(
			"service_principal_id = ? AND action = ? AND allowed = ?",
			fixture.principal.ID,
			"ticket.read",
			false,
		).
		Count(&denied).Error; err != nil {
		t.Fatalf("count denied snapshot decisions: %v", err)
	}
	if denied != 1 {
		t.Fatalf("persisted denied snapshot decisions=%d, want 1", denied)
	}
}

func TestA2ATaskListDecisionsUseShortProjectTransactions(t *testing.T) {
	t.Run("allowed summary", func(t *testing.T) {
		fixture := newA2AAdapterFixture(t)
		decisionCreates := installA2AListDecisionScopeAssertion(t, fixture)
		ctx := a2aFixtureContext(t, fixture)
		batch, err := NewA2ATaskListAuthorizer(fixture.native).
			PrepareTaskList(ctx, a2a.ListTasksParams{PageSize: 20})
		if err != nil {
			t.Fatalf("prepare allowed Task list: %v", err)
		}
		if err := batch.RecordSummary(
			context.WithoutCancel(ctx),
			a2a.TaskListAuthorizationSummary{
				CandidateBudget:   100,
				CandidatesScanned: 1,
				ItemsReturned:     1,
			},
		); err != nil {
			t.Fatalf("record allowed Task list summary: %v", err)
		}
		if *decisionCreates != 1 {
			t.Fatalf(
				"allowed Task list persisted %d decisions, want 1",
				*decisionCreates,
			)
		}
	})

	t.Run("denied prepare", func(t *testing.T) {
		fixture := newA2AAdapterFixture(t)
		if _, err := fixture.native.CreateAgentPolicy(
			context.Background(),
			services.CreateAgentPolicyInput{
				ServicePrincipalID: fixture.principal.ID,
				Name:               "deny Task list",
				Effect:             models.AgentPolicyEffectDeny,
				Scope:              models.ScopeTasksManage,
				Action:             "a2a.ListTasks",
				ResourceType:       "a2a_task",
				ResourceID:         "*",
				Priority:           100,
			},
		); err != nil {
			t.Fatalf("create Task list deny policy: %v", err)
		}
		decisionCreates := installA2AListDecisionScopeAssertion(t, fixture)
		_, err := NewA2ATaskListAuthorizer(fixture.native).
			PrepareTaskList(
				context.WithoutCancel(a2aFixtureContext(t, fixture)),
				a2a.ListTasksParams{PageSize: 20},
			)
		if !errors.Is(err, services.ErrPolicyDenied) {
			t.Fatalf("denied Task list error=%v, want policy denied", err)
		}
		if *decisionCreates != 1 {
			t.Fatalf(
				"denied Task list persisted %d decisions, want 1",
				*decisionCreates,
			)
		}
		var denied int64
		if err := fixture.db.Model(&models.PolicyDecision{}).
			Where(
				"service_principal_id = ? AND action = ? AND allowed = ?",
				fixture.principal.ID,
				"a2a.ListTasks",
				false,
			).
			Count(&denied).Error; err != nil {
			t.Fatalf("count denied Task list decisions: %v", err)
		}
		if denied != 1 {
			t.Fatalf("persisted denied Task list decisions=%d, want 1", denied)
		}
	})
}

func TestA2ATaskSnapshotsRequireTokenTicketReadScope(t *testing.T) {
	fixture := newA2AAdapterFixture(t)
	ctx := a2aFixtureContextWithTokenScopes(
		t,
		fixture,
		[]string{models.ScopeTasksManage},
	)
	ticketID := uint(884)
	linked := a2a.Task{
		ID:             "task-token-snapshot-linked",
		ContextID:      "context-token-snapshot",
		LinkedTicketID: &ticketID,
	}
	authorizer := NewA2ATaskListAuthorizer(fixture.native)
	allowed, err := authorizer.AuthorizeTaskSnapshot(ctx, linked)
	if err != nil || allowed {
		t.Fatalf(
			"narrow token snapshot authorization = %v, %v",
			allowed,
			err,
		)
	}
	var decisions int64
	if err := fixture.db.Model(&models.PolicyDecision{}).
		Count(&decisions).Error; err != nil {
		t.Fatal(err)
	}
	if decisions != 0 {
		t.Fatalf(
			"token-denied snapshot reached principal policy: %d",
			decisions,
		)
	}

	batch, err := authorizer.PrepareTaskList(
		ctx,
		a2a.ListTasksParams{PageSize: 20},
	)
	if err != nil {
		t.Fatalf("prepare narrow-token Task list: %v", err)
	}
	if allowed, err := batch.Allows(linked); err != nil || allowed {
		t.Fatalf(
			"narrow token list exposed linked Task = %v, %v",
			allowed,
			err,
		)
	}
	if allowed, err := batch.Allows(a2a.Task{
		ID:        "task-token-snapshot-unlinked",
		ContextID: "context-token-snapshot",
	}); err != nil || !allowed {
		t.Fatalf(
			"tasks:manage token could not list unlinked Task = %v, %v",
			allowed,
			err,
		)
	}
}

func TestA2AUnlinkedTaskSnapshotRevalidatesLiveProjectGrant(
	t *testing.T,
) {
	fixture := newA2AAdapterFixture(t)
	ctx := context.WithoutCancel(a2aFixtureContext(t, fixture))
	task := a2a.Task{
		ID:        "task-unlinked-live-grant",
		ContextID: "context-unlinked-live-grant",
	}
	authorizer := NewA2ATaskListAuthorizer(fixture.native)
	allowed, err := authorizer.AuthorizeTaskSnapshot(ctx, task)
	if err != nil || !allowed {
		t.Fatalf(
			"initial unlinked snapshot allowed=%t err=%v",
			allowed,
			err,
		)
	}
	if err := fixture.db.Model(
		&models.ProjectPrincipalGrant{},
	).Where(
		"project_id = ? AND service_principal_id = ?",
		fixture.project.ID,
		fixture.principal.ID,
	).Update("is_active", false).Error; err != nil {
		t.Fatalf("revoke A2A project grant: %v", err)
	}
	allowed, err = authorizer.AuthorizeTaskSnapshot(ctx, task)
	if err != nil || allowed {
		t.Fatalf(
			"revoked unlinked snapshot allowed=%t err=%v, want false/nil",
			allowed,
			err,
		)
	}
}

func TestTrustedA2AIdentityRejectsOperationScopeMismatch(t *testing.T) {
	fixture := newA2AAdapterFixture(t)
	ctx := a2aFixtureContext(t, fixture)
	ctx = WithA2AExecutionIdentity(ctx, A2AExecutionIdentity{
		Actor:        models.ServicePrincipalActor(fixture.principal.ID),
		CredentialID: fixture.credential.ID,
		ProjectKey:   string(fixture.project.Key),
		TokenScopes:  fixture.principal.ScopeList(),
		Scope: models.ProjectScope{
			OrganizationID: fixture.organization.ID,
			ProjectID:      fixture.project.ID + 1,
		},
	})
	allowed, err := NewA2ATaskListAuthorizer(fixture.native).
		AuthorizeTaskSnapshot(ctx, a2a.Task{
			ID:        "task-mismatched-operation-scope",
			ContextID: "context-mismatched-operation-scope",
		})
	if err == nil || allowed {
		t.Fatalf(
			"mismatched A2A identity authorization = %v, %v",
			allowed,
			err,
		)
	}
	var decisions int64
	if err := fixture.db.Model(&models.PolicyDecision{}).
		Count(&decisions).Error; err != nil {
		t.Fatalf("count scope-mismatch decisions: %v", err)
	}
	if decisions != 0 {
		t.Fatalf("scope-mismatch authorization persisted %d decisions", decisions)
	}
}

func installA2AListDecisionScopeAssertion(
	t *testing.T,
	fixture a2aAdapterFixture,
) *int {
	t.Helper()
	decisionCreates := new(int)
	callbackName := "test:require_list_policy_project_transaction:" +
		fixture.principal.ID
	if err := fixture.db.Callback().Create().
		Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			decision, ok := tx.Statement.Dest.(*models.PolicyDecision)
			if !ok {
				return
			}
			*decisionCreates++
			if !scopeddb.HasTransaction(tx.Statement.Context) {
				_ = tx.AddError(errors.New(
					"Task list decision is outside a project transaction",
				))
				return
			}
			if decision.OrganizationID != fixture.organization.ID ||
				decision.ProjectID != fixture.project.ID ||
				decision.Action != "a2a.ListTasks" {
				_ = tx.AddError(errors.New(
					"Task list decision is missing trusted project provenance",
				))
			}
		}); err != nil {
		t.Fatalf("register Task list decision assertion: %v", err)
	}
	return decisionCreates
}
