package services

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestResolveTicketAssignmentChangesEnforcesCanonicalDomainRules(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedCompatibilityUser(t, db, "assignment")
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
	withoutCompatibility, err := service.CreateServicePrincipal(
		context.Background(),
		CreateServicePrincipalInput{Name: "assignment-no-compatibility"},
	)
	if err != nil {
		t.Fatalf("create principal without compatibility user: %v", err)
	}
	danglingCompatibility := createNativePrincipal(t, service, user.ID, "assignment-dangling-compatibility")
	if err := db.Model(&models.ServicePrincipal{}).
		Where("id = ?", danglingCompatibility.ID).
		Update("compatibility_user_id", user.ID+100000).Error; err != nil {
		t.Fatalf("make compatibility user dangling: %v", err)
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
			name:     "service principal without compatibility user",
			assignee: models.ServicePrincipalActor(withoutCompatibility.ID),
			wantErr:  ErrAssigneePolicyDenied,
		},
		{
			name:     "service principal with missing compatibility user",
			assignee: models.ServicePrincipalActor(danglingCompatibility.ID),
			wantErr:  ErrAssigneePolicyDenied,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changes, err := service.ResolveTicketAssignmentChanges(context.Background(), test.assignee)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ResolveTicketAssignmentChanges() error = %v, want %v", err, test.wantErr)
			}
			if changes != nil {
				t.Fatalf("failed assignment returned changes: %#v", changes)
			}
		})
	}

	t.Run("human", func(t *testing.T) {
		got, err := service.ResolveTicketAssignmentChanges(context.Background(), models.HumanActor(user.ID))
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
		got, err := service.ResolveTicketAssignmentChanges(
			context.Background(),
			models.ServicePrincipalActor(active.ID),
		)
		if err != nil {
			t.Fatalf("resolve principal assignment: %v", err)
		}
		want := map[string]any{
			"assigned_to_actor_type":           models.ActorTypeServicePrincipal,
			"assigned_to_actor_id":             active.ID,
			"assigned_to_id":                   user.ID,
			"assigned_to_service_principal_id": active.ID,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("principal changes = %#v, want %#v", got, want)
		}
	})

	t.Run("system clears compatibility assignment", func(t *testing.T) {
		got, err := service.ResolveTicketAssignmentChanges(
			context.Background(),
			models.SystemActor("scheduler"),
		)
		if err != nil {
			t.Fatalf("resolve system assignment: %v", err)
		}
		want := map[string]any{
			"assigned_to_actor_type":           models.ActorTypeSystem,
			"assigned_to_actor_id":             "scheduler",
			"assigned_to_id":                   nil,
			"assigned_to_service_principal_id": nil,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("system changes = %#v, want %#v", got, want)
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
