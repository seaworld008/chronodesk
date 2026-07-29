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
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changes, err := service.ResolveTicketAssignmentChanges(context.Background(), &test.assignee)
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
		got, err := service.ResolveTicketAssignmentChanges(context.Background(), &assignee)
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
		got, err := service.ResolveTicketAssignmentChanges(context.Background(), &assignee)
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
			context.Background(),
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
		got, err := service.ResolveTicketAssignmentChanges(context.Background(), &assignee)
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
