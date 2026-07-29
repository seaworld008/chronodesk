package agentplatform

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/a2a"
	"github.com/seaworld008/chronodesk/server/internal/mcp"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestAssignmentTargetErrorsMapConsistentlyAcrossMCPAndA2A(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	backend, err := NewA2ABackend(
		fixture.db,
		fixture.service,
		StaticA2AIdentityResolver{Identity: A2AExecutionIdentity{
			Actor:        models.ServicePrincipalActor(fixture.principal.ID),
			CredentialID: fixture.credential.ID,
		}},
	)
	if err != nil {
		t.Fatalf("create A2A backend: %v", err)
	}

	inactive := createAssignmentTargetPrincipal(t, fixture, "inactive")
	if _, err := fixture.service.SetServicePrincipalControls(
		context.Background(),
		inactive.ID,
		models.ServicePrincipalStatusInactive,
		false,
		false,
	); err != nil {
		t.Fatalf("disable target principal: %v", err)
	}
	emergency := createAssignmentTargetPrincipal(t, fixture, "emergency")
	if _, err := fixture.service.SetServicePrincipalControls(
		context.Background(),
		emergency.ID,
		models.ServicePrincipalStatusActive,
		false,
		true,
	); err != nil {
		t.Fatalf("emergency-disable target principal: %v", err)
	}

	tests := []struct {
		name     string
		assignee models.ActorRef
		mcpCode  string
		a2aState a2a.TaskState
	}{
		{
			name:     "invalid human id",
			assignee: models.ActorRef{Type: models.ActorTypeHuman, ID: "invalid"},
			mcpCode:  "invalid_argument",
			a2aState: a2a.TaskStateInputRequired,
		},
		{
			name:     "missing human",
			assignee: models.HumanActor(fixture.user.ID + 100000),
			mcpCode:  "not_found",
			a2aState: a2a.TaskStateFailed,
		},
		{
			name:     "missing service principal",
			assignee: models.ServicePrincipalActor("missing-target-principal"),
			mcpCode:  "not_found",
			a2aState: a2a.TaskStateFailed,
		},
		{
			name:     "inactive service principal",
			assignee: models.ServicePrincipalActor(inactive.ID),
			mcpCode:  "policy_denied",
			a2aState: a2a.TaskStateRejected,
		},
		{
			name:     "emergency-disabled service principal",
			assignee: models.ServicePrincipalActor(emergency.ID),
			mcpCode:  "policy_denied",
			a2aState: a2a.TaskStateRejected,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, mcpErr := fixture.adapter.assignTicket(
				context.Background(),
				fixture.actor,
				map[string]any{
					"ticket_id":        int64(999999),
					"expected_version": int64(1),
					"lease_id":         "unused-assignment-lease",
					"assignee": map[string]any{
						"type": string(test.assignee.Type),
						"id":   test.assignee.ID,
					},
				},
			)
			var protocolErr *mcp.BackendError
			if !errors.As(mcpErr, &protocolErr) || protocolErr.Code != test.mcpCode {
				t.Fatalf("MCP error = %v, want code %q", mcpErr, test.mcpCode)
			}

			reporter := &recordingA2AReporter{}
			message := structuredA2AMessage(t, "ticket-work", map[string]any{
				"operation":        "assign",
				"ticket_id":        999999,
				"expected_version": 1,
				"lease_id":         "unused-assignment-lease",
				"assignee":         test.assignee,
			})
			message.MessageID = fmt.Sprintf("assignment-error-%d", index)
			if err := backend.Process(
				context.Background(),
				a2a.Task{
					ID:        fmt.Sprintf("assignment-error-task-%d", index),
					ContextID: "assignment-errors",
				},
				message,
				reporter,
			); err != nil {
				t.Fatalf("A2A assignment mapping returned transport error: %v", err)
			}
			if got := reporter.lastState(); got != test.a2aState {
				t.Fatalf("A2A state = %s, want %s", got, test.a2aState)
			}
			if test.assignee.ID == "missing-target-principal" &&
				reporter.lastState() == a2a.TaskStateAuthRequired {
				t.Fatal("missing target principal was misclassified as caller authentication failure")
			}
		})
	}
}

func TestMCPAndA2AAssignmentPersistIdenticalCanonicalValues(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	ctx := context.Background()
	backend, err := NewA2ABackend(
		fixture.db,
		fixture.service,
		StaticA2AIdentityResolver{Identity: A2AExecutionIdentity{
			Actor:        models.ServicePrincipalActor(fixture.principal.ID),
			CredentialID: fixture.credential.ID,
		}},
	)
	if err != nil {
		t.Fatalf("create A2A backend: %v", err)
	}
	if _, err := fixture.service.CreateAgentPolicy(ctx, services.CreateAgentPolicyInput{
		ServicePrincipalID: fixture.principal.ID,
		Name:               "allow cross-protocol assignment",
		Effect:             models.AgentPolicyEffectAllow,
		Scope:              models.ScopeTicketsAssign,
		Action:             "ticket.assign",
		ResourceType:       "ticket",
		Priority:           100,
	}); err != nil {
		t.Fatalf("create assignment policy: %v", err)
	}
	target := createAssignmentTargetPrincipal(t, fixture, "active")

	tests := []struct {
		name     string
		assignee models.ActorRef
	}{
		{
			name:     "active service principal",
			assignee: models.ServicePrincipalActor(target.ID),
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mcpTicket := fixture.seedTicket(
				t,
				fmt.Sprintf("MCP-ASSIGN-CONSISTENCY-%d", index),
				"",
			)
			a2aTicket := fixture.seedTicket(
				t,
				fmt.Sprintf("A2A-ASSIGN-CONSISTENCY-%d", index),
				"",
			)
			mcpLease := seedAssignmentLease(t, fixture, mcpTicket)
			a2aLease := seedAssignmentLease(t, fixture, a2aTicket)

			if _, err := fixture.adapter.CallTool(ctx, fixture.actor, "ticket_assign", map[string]any{
				"ticket_id":        int64(mcpTicket.ID),
				"expected_version": int64(1),
				"lease_id":         mcpLease.ID,
				"assignee": map[string]any{
					"type": string(test.assignee.Type),
					"id":   test.assignee.ID,
				},
				"idempotency_key": fmt.Sprintf("mcp-assignment-consistency-%d", index),
			}); err != nil {
				t.Fatalf("MCP assignment: %v", err)
			}

			reporter := &recordingA2AReporter{}
			message := structuredA2AMessage(t, "ticket-work", map[string]any{
				"operation":        "assign",
				"ticket_id":        a2aTicket.ID,
				"expected_version": 1,
				"lease_id":         a2aLease.ID,
				"assignee":         test.assignee,
			})
			message.MessageID = fmt.Sprintf("a2a-assignment-consistency-%d", index)
			if err := backend.Process(
				ctx,
				a2a.Task{
					ID:        fmt.Sprintf("a2a-assignment-consistency-task-%d", index),
					ContextID: "assignment-consistency",
				},
				message,
				reporter,
			); err != nil {
				t.Fatalf("A2A assignment: %v", err)
			}
			if len(reporter.artifacts) != 1 {
				t.Fatalf("A2A assignment artifacts = %d, want 1", len(reporter.artifacts))
			}

			if err := fixture.db.First(&mcpTicket, mcpTicket.ID).Error; err != nil {
				t.Fatalf("reload MCP ticket: %v", err)
			}
			if err := fixture.db.First(&a2aTicket, a2aTicket.ID).Error; err != nil {
				t.Fatalf("reload A2A ticket: %v", err)
			}
			mcpValues := ticketAssignmentValues(mcpTicket)
			a2aValues := ticketAssignmentValues(a2aTicket)
			if !reflect.DeepEqual(mcpValues, a2aValues) {
				t.Fatalf("assignment values diverged: MCP=%#v A2A=%#v", mcpValues, a2aValues)
			}
			if mcpValues.ActorType != test.assignee.Type || mcpValues.ActorID != test.assignee.ID {
				t.Fatalf("canonical actor was not persisted: %#v", mcpValues)
			}
		})
	}
}

func createAssignmentTargetPrincipal(
	t *testing.T,
	fixture *mcpAdapterFixture,
	suffix string,
) *models.ServicePrincipal {
	t.Helper()
	principal, err := fixture.service.CreateServicePrincipal(
		context.Background(),
		services.CreateServicePrincipalInput{
			Name: "assignment-target-" + suffix,
		},
	)
	if err != nil {
		t.Fatalf("create assignment target principal: %v", err)
	}
	return principal
}

func seedAssignmentLease(
	t *testing.T,
	fixture *mcpAdapterFixture,
	ticket models.Ticket,
) models.TicketLease {
	t.Helper()
	now := time.Now().UTC()
	lease := models.TicketLease{
		ID:              fmt.Sprintf("assignment-lease-%d", ticket.ID),
		TicketID:        ticket.ID,
		HolderActorType: models.ActorTypeServicePrincipal,
		HolderActorID:   fixture.principal.ID,
		TicketVersion:   ticket.Version,
		ExpiresAt:       now.Add(time.Minute),
		LastHeartbeatAt: now,
	}
	if err := fixture.db.Create(&lease).Error; err != nil {
		t.Fatalf("seed assignment lease: %v", err)
	}
	return lease
}

type assignmentValues struct {
	ActorType          models.ActorType
	ActorID            string
	HumanUserID        *uint
	ServicePrincipalID *string
}

func ticketAssignmentValues(ticket models.Ticket) assignmentValues {
	return assignmentValues{
		ActorType:          ticket.AssignedToActorType,
		ActorID:            ticket.AssignedToActorID,
		HumanUserID:        ticket.AssignedToID,
		ServicePrincipalID: ticket.AssignedToServicePrincipalID,
	}
}
