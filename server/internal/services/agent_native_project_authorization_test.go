package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type agentProjectAuthorizationFixture struct {
	db         *gorm.DB
	project    models.Project
	human      models.User
	membership models.ProjectMembership
	principal  models.ServicePrincipal
	grant      models.ProjectPrincipalGrant
	credential models.AgentCredential
}

func newAgentProjectAuthorizationFixture(
	t *testing.T,
) agentProjectAuthorizationFixture {
	t.Helper()
	db := newProjectServiceTestDB(t)
	if err := db.AutoMigrate(&models.AgentCredential{}); err != nil {
		t.Fatal(err)
	}
	_, _, project, human := seedProjectAccessFixture(t, db)
	membership := models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    human.ID,
		Role:      models.ProjectRoleManager,
		IsActive:  true,
		Version:   3,
	}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatal(err)
	}
	principal := models.ServicePrincipal{
		ID:     "00000000-0000-7000-8000-000000000095",
		Name:   "machine-project-authorization",
		Status: models.ServicePrincipalStatusActive,
		Scopes: datatypes.JSON(`["tickets:read","tasks:manage"]`),
	}
	if err := db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	grant := models.ProjectPrincipalGrant{
		ProjectID:          project.ID,
		ServicePrincipalID: principal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             datatypes.JSON(`["tickets:read","tasks:manage"]`),
		IsActive:           true,
	}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	credential := models.AgentCredential{
		ID:                 "00000000-0000-7000-8000-000000000096",
		ServicePrincipalID: principal.ID,
		Name:               "machine-project-authorization",
		SecretHash:         "test-only-secret-hash",
		Status:             models.AgentCredentialStatusActive,
		ExpiresAt:          time.Now().UTC().Add(time.Hour),
	}
	if err := db.Create(&credential).Error; err != nil {
		t.Fatal(err)
	}
	return agentProjectAuthorizationFixture{
		db:         db,
		project:    project,
		human:      human,
		membership: membership,
		principal:  principal,
		grant:      grant,
		credential: credential,
	}
}

func (fixture agentProjectAuthorizationFixture) machineContext(
	t *testing.T,
) context.Context {
	t.Helper()
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:        fixture.project.Scope(),
		Actor:        models.ServicePrincipalActor(fixture.principal.ID),
		Source:       SourceProtocolAgentREST,
		CredentialID: fixture.credential.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestAgentNativePrincipalRevalidationSamplesNowAfterCredentialLock(
	t *testing.T,
) {
	fixture := newAgentProjectAuthorizationFixture(t)
	queried := map[string]bool{}
	if err := fixture.db.Callback().Query().After("gorm:query").Register(
		"test:machine-clock-after-locks",
		func(query *gorm.DB) {
			queried[query.Statement.Table] = true
		},
	); err != nil {
		t.Fatal(err)
	}
	nowCalls := 0
	native := NewAgentNativeService(
		fixture.db,
		AgentNativeOptions{
			Now: func() time.Time {
				nowCalls++
				for _, table := range []string{
					"projects",
					"service_principals",
					"project_principal_grants",
					"agent_credentials",
				} {
					if !queried[table] {
						t.Fatalf(
							"clock sampled before %s lock query completed",
							table,
						)
					}
				}
				return time.Now().UTC()
			},
		},
	)

	var access *ProjectAccess
	err := scopeddb.WithProjectScopeContextTransaction(
		fixture.machineContext(t),
		fixture.db,
		fixture.project.Scope(),
		func(ctx context.Context) error {
			var revalidateErr error
			access, revalidateErr = native.RevalidatePrincipalProjectOperation(
				ctx,
				models.ScopeTicketsRead,
			)
			return revalidateErr
		},
	)
	if err != nil {
		t.Fatalf("revalidate machine operation: %v", err)
	}
	if nowCalls != 1 {
		t.Fatalf("authorization clock sampled %d times, want 1", nowCalls)
	}
	if access == nil {
		t.Fatal("machine revalidation returned no access")
	}
	snapshot := access.AuthorizationSnapshot
	if snapshot.Scope != fixture.project.Scope() ||
		snapshot.ActorType != models.ActorTypeServicePrincipal ||
		snapshot.ProjectUpdatedAt.IsZero() ||
		snapshot.PrincipalID != fixture.principal.ID ||
		snapshot.PrincipalUpdatedAt.IsZero() ||
		snapshot.GrantID != fixture.grant.ID ||
		snapshot.GrantUpdatedAt.IsZero() ||
		snapshot.GrantRole != models.ProjectRoleAgent ||
		len(snapshot.GrantScopes) != 2 ||
		snapshot.CredentialID != fixture.credential.ID ||
		snapshot.CredentialUpdatedAt.IsZero() ||
		!snapshot.Matches(snapshot) {
		t.Fatalf("machine authorization snapshot = %+v", snapshot)
	}
}

func TestRevalidateOperationProjectAuthorizationDispatchesCanonicalRules(
	t *testing.T,
) {
	fixture := newAgentProjectAuthorizationFixture(t)
	native := NewAgentNativeService(fixture.db)

	humanContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  fixture.project.Scope(),
			Actor:  models.HumanActor(fixture.human.ID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var humanAccess *ProjectAccess
	err = scopeddb.WithProjectScopeContextTransaction(
		humanContext,
		fixture.db,
		fixture.project.Scope(),
		func(ctx context.Context) error {
			var revalidateErr error
			humanAccess, revalidateErr =
				RevalidateOperationProjectAuthorization(
					ctx,
					fixture.db,
					native,
					models.ScopeTicketsRead,
				)
			return revalidateErr
		},
	)
	if err != nil {
		t.Fatalf("dispatch Human authorization: %v", err)
	}
	if humanAccess == nil ||
		humanAccess.Role != models.ProjectRoleManager ||
		humanAccess.AuthorizationSnapshot.MembershipID !=
			fixture.membership.ID {
		t.Fatalf("dispatched Human access = %+v", humanAccess)
	}

	var machineAccess *ProjectAccess
	err = scopeddb.WithProjectScopeContextTransaction(
		fixture.machineContext(t),
		fixture.db,
		fixture.project.Scope(),
		func(ctx context.Context) error {
			var revalidateErr error
			machineAccess, revalidateErr =
				RevalidateOperationProjectAuthorization(
					ctx,
					fixture.db,
					native,
					models.ScopeTicketsRead,
				)
			return revalidateErr
		},
	)
	if err != nil {
		t.Fatalf("dispatch machine authorization: %v", err)
	}
	if machineAccess == nil ||
		machineAccess.Role != models.ProjectRoleAgent ||
		machineAccess.AuthorizationSnapshot.CredentialID !=
			fixture.credential.ID {
		t.Fatalf("dispatched machine access = %+v", machineAccess)
	}

	systemContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  fixture.project.Scope(),
			Actor:  models.SystemActor("authorization-test"),
			Source: SourceProtocolWorker,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	err = scopeddb.WithProjectScopeContextTransaction(
		systemContext,
		fixture.db,
		fixture.project.Scope(),
		func(ctx context.Context) error {
			_, revalidateErr := RevalidateOperationProjectAuthorization(
				ctx,
				fixture.db,
				native,
			)
			return revalidateErr
		},
	)
	if !errors.Is(err, ErrInvalidActor) {
		t.Fatalf("system authorization dispatch error = %v", err)
	}
}
