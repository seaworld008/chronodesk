package services

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
)

func TestResolveTicketAssignmentChangesEnforcesCanonicalDomainRules(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "assignment")
	service := NewAgentNativeService(db)

	active := createNativePrincipal(t, service, user.ID, "assignment-active")
	inactive := createNativePrincipal(t, service, user.ID, "assignment-inactive")
	if _, err := service.SetServicePrincipalControls(
		context.Background(),
		inactive.ID,
		models.ServicePrincipalStatusInactive,
		false,
		false,
	); err != nil {
		t.Fatalf("disable assignment principal: %v", err)
	}
	emergency := createNativePrincipal(t, service, user.ID, "assignment-emergency")
	if _, err := service.SetServicePrincipalControls(
		context.Background(),
		emergency.ID,
		models.ServicePrincipalStatusActive,
		false,
		true,
	); err != nil {
		t.Fatalf("emergency-disable assignment principal: %v", err)
	}
	ctx := testProjectOperationContext(t, db, models.HumanActor(user.ID))
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectMembership{
		ProjectID: operation.Scope.ProjectID,
		UserID:    user.ID,
		Role:      models.ProjectRoleAgent,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, principal := range []*models.ServicePrincipal{active, inactive, emergency} {
		if err := db.Create(&models.ProjectPrincipalGrant{
			ProjectID:          operation.Scope.ProjectID,
			ServicePrincipalID: principal.ID,
			Role:               models.ProjectRoleAgent,
			Scopes:             datatypes.JSON(`["tickets:assign"]`),
			IsActive:           true,
		}).Error; err != nil {
			t.Fatalf("grant assignment principal %s: %v", principal.ID, err)
		}
	}
	requester := seedActorUser(t, db, "assignment-requester")
	if err := db.Create(&models.ProjectMembership{
		ProjectID: operation.Scope.ProjectID,
		UserID:    requester.ID,
		Role:      models.ProjectRoleRequester,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	crossProjectUser := seedActorUser(t, db, "assignment-other-project")
	var currentProject models.Project
	if err := db.First(&currentProject, operation.Scope.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	otherProject := models.Project{
		OrganizationID: currentProject.OrganizationID,
		BusinessUnitID: currentProject.BusinessUnitID,
		Key:            models.ProjectKey("OTHER"),
		Name:           "Other",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&otherProject).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectMembership{
		ProjectID: otherProject.ID,
		UserID:    crossProjectUser.ID,
		Role:      models.ProjectRoleAgent,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	integrationOnly := createNativePrincipal(
		t,
		service,
		user.ID,
		"assignment-integration-only",
		models.ScopeTicketsRead,
	)
	if err := db.Create(&models.ProjectPrincipalGrant{
		ProjectID:          operation.Scope.ProjectID,
		ServicePrincipalID: integrationOnly.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             datatypes.JSON(`["tickets:read"]`),
		IsActive:           true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	expiredGrantPrincipal := createNativePrincipal(
		t,
		service,
		user.ID,
		"assignment-expired-grant",
		models.ScopeTicketsAssign,
	)
	expiredAt := time.Now().Add(-time.Minute)
	if err := db.Create(&models.ProjectPrincipalGrant{
		ProjectID:          operation.Scope.ProjectID,
		ServicePrincipalID: expiredGrantPrincipal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             datatypes.JSON(`["tickets:assign"]`),
		IsActive:           true,
		ExpiresAt:          &expiredAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		assignee models.ActorRef
		wantErr  error
	}{
		{
			name:     "invalid human id",
			assignee: models.ActorRef{Type: models.ActorTypeHuman, ID: "not-a-user-id"},
			wantErr:  ErrInvalidAssignee,
		},
		{
			name: "overflowing human id",
			assignee: models.ActorRef{
				Type: models.ActorTypeHuman,
				ID:   "18446744073709551616",
			},
			wantErr: ErrInvalidAssignee,
		},
		{
			name:     "missing human",
			assignee: models.HumanActor(user.ID + 99999),
			wantErr:  ErrAssigneeNotFound,
		},
		{
			name:     "missing service principal",
			assignee: models.ServicePrincipalActor("missing-assignment-principal"),
			wantErr:  ErrAssigneeNotFound,
		},
		{
			name:     "inactive service principal",
			assignee: models.ServicePrincipalActor(inactive.ID),
			wantErr:  ErrAssigneePolicyDenied,
		},
		{
			name:     "emergency-disabled service principal",
			assignee: models.ServicePrincipalActor(emergency.ID),
			wantErr:  ErrAssigneePolicyDenied,
		},
		{
			name:     "requester is not assignable",
			assignee: models.HumanActor(requester.ID),
			wantErr:  ErrAssigneePolicyDenied,
		},
		{
			name:     "other project human is not assignable",
			assignee: models.HumanActor(crossProjectUser.ID),
			wantErr:  ErrAssigneePolicyDenied,
		},
		{
			name:     "integration-only principal is not assignable",
			assignee: models.ServicePrincipalActor(integrationOnly.ID),
			wantErr:  ErrAssigneePolicyDenied,
		},
		{
			name:     "expired project grant is not assignable",
			assignee: models.ServicePrincipalActor(expiredGrantPrincipal.ID),
			wantErr:  ErrAssigneePolicyDenied,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changes, err := service.ResolveTicketAssignmentChanges(ctx, &test.assignee)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ResolveTicketAssignmentChanges() error = %v, want %v", err, test.wantErr)
			}
			if changes != nil {
				t.Fatalf("failed assignment returned changes: %#v", changes)
			}
		})
	}

	t.Run("human", func(t *testing.T) {
		assignee := models.HumanActor(user.ID)
		got, err := service.ResolveTicketAssignmentChanges(ctx, &assignee)
		if err != nil {
			t.Fatalf("resolve human assignment: %v", err)
		}
		want := map[string]any{
			"assigned_to_actor_type":           models.ActorTypeHuman,
			"assigned_to_actor_id":             models.HumanActor(user.ID).ID,
			"assigned_to_id":                   user.ID,
			"assigned_to_service_principal_id": nil,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("human changes = %#v, want %#v", got, want)
		}
	})

	t.Run("active service principal", func(t *testing.T) {
		assignee := models.ServicePrincipalActor(active.ID)
		got, err := service.ResolveTicketAssignmentChanges(ctx, &assignee)
		if err != nil {
			t.Fatalf("resolve principal assignment: %v", err)
		}
		want := map[string]any{
			"assigned_to_actor_type":           models.ActorTypeServicePrincipal,
			"assigned_to_actor_id":             active.ID,
			"assigned_to_id":                   nil,
			"assigned_to_service_principal_id": active.ID,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("principal changes = %#v, want %#v", got, want)
		}
	})

	t.Run("release", func(t *testing.T) {
		got, err := service.ResolveTicketAssignmentChanges(
			ctx,
			nil,
		)
		if err != nil {
			t.Fatalf("resolve released assignment: %v", err)
		}
		want := map[string]any{
			"assigned_to_actor_type":           "",
			"assigned_to_actor_id":             "",
			"assigned_to_id":                   nil,
			"assigned_to_service_principal_id": nil,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("release changes = %#v, want %#v", got, want)
		}
	})

	t.Run("system is not an assignment target", func(t *testing.T) {
		assignee := models.SystemActor("scheduler")
		got, err := service.ResolveTicketAssignmentChanges(ctx, &assignee)
		if !errors.Is(err, ErrInvalidAssignee) {
			t.Fatalf("ResolveTicketAssignmentChanges() error = %v, want %v", err, ErrInvalidAssignee)
		}
		if got != nil {
			t.Fatalf("system assignment returned changes: %#v", got)
		}
	})
}

func TestAgentNativeAssignmentErrorCodesAreStable(t *testing.T) {
	tests := []struct {
		err  error
		code string
	}{
		{ErrInvalidAssignee, "invalid_assignee"},
		{ErrAssigneeNotFound, "assignee_not_found"},
		{ErrAssigneePolicyDenied, "assignee_policy_denied"},
	}
	for _, test := range tests {
		if got := AgentNativeErrorCode(test.err); got != test.code {
			t.Fatalf("AgentNativeErrorCode(%v) = %q, want %q", test.err, got, test.code)
		}
	}
}
