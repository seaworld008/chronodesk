package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestAgentContextBoundary(t *testing.T) {
	valid := &models.AgentContext{
		Goal:               strings.Repeat("目", maxAgentContextGoalRunes),
		Constraints:        []string{"do not restart"},
		AcceptanceCriteria: []string{"health is green"},
		MissingInformation: []string{"deployment region"},
		RelatedResources:   []string{"urn:chronodesk:ticket:OPS-1"},
	}
	if err := validateAgentContext(valid); err != nil {
		t.Fatalf("valid Agent context error = %v", err)
	}

	for _, test := range []struct {
		name    string
		context *models.AgentContext
	}{
		{
			name: "goal length",
			context: &models.AgentContext{
				Goal: strings.Repeat("界", maxAgentContextGoalRunes+1),
			},
		},
		{
			name: "item count",
			context: &models.AgentContext{
				Constraints: make([]string, maxAgentContextItems+1),
			},
		},
		{
			name: "fact length",
			context: &models.AgentContext{
				AcceptanceCriteria: []string{
					strings.Repeat("界", maxAgentContextItemRunes+1),
				},
			},
		},
		{
			name: "resource length",
			context: &models.AgentContext{
				RelatedResources: []string{
					strings.Repeat("r", maxAgentContextResourceRunes+1),
				},
			},
		},
		{
			name: "serialized size",
			context: &models.AgentContext{
				Constraints:        repeatedAgentContextItems(),
				AcceptanceCriteria: repeatedAgentContextItems(),
				MissingInformation: repeatedAgentContextItems(),
				RelatedResources:   repeatedAgentContextItems(),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateAgentContext(test.context); !errors.Is(
				err,
				ErrInvalidAgentContext,
			) {
				t.Fatalf("error = %v, want invalid Agent context", err)
			}
		})
	}
}

func repeatedAgentContextItems() []string {
	items := make([]string, maxAgentContextItems)
	for index := range items {
		items[index] = strings.Repeat("x", maxAgentContextItemRunes)
	}
	return items
}

func TestAgentContextBoundaryIsUsedByCreateAndUpdateCommands(t *testing.T) {
	actor := models.HumanActor(7)
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  models.ProjectScope{OrganizationID: 1, ProjectID: 2},
			Actor:  actor,
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	invalid := &models.AgentContext{
		Constraints: make([]string, maxAgentContextItems+1),
	}
	service := &AgentNativeService{}
	if _, createErr := service.CreateNativeTicket(
		ctx,
		NativeTicketCreateInput{
			Request: models.TicketCreateRequest{
				Title:        "Bounded context",
				Description:  "Bounded context",
				Type:         models.TicketTypeRequest,
				Priority:     models.TicketPriorityNormal,
				Source:       models.TicketSourceWeb,
				AgentContext: invalid,
			},
			Actor: actor,
		},
	); !errors.Is(createErr, ErrInvalidAgentContext) {
		t.Fatalf("create error = %v, want invalid Agent context", createErr)
	}

	if _, _, _, updateErr := service.buildHumanTicketUpdate(
		ctx,
		&models.Ticket{},
		&models.TicketUpdateRequest{AgentContext: invalid},
	); !errors.Is(updateErr, ErrInvalidAgentContext) {
		t.Fatalf("update error = %v, want invalid Agent context", updateErr)
	}
}

func TestMachineTicketUpdateUsesSharedAgentContextBoundary(t *testing.T) {
	actor := models.ServicePrincipalActor("agent-context-boundary")
	invalid := map[string]any{
		"constraints": make([]string, maxAgentContextItems+1),
	}

	for _, source := range []SourceProtocol{
		SourceProtocolAgentREST,
		SourceProtocolMCP,
		SourceProtocolA2A,
	} {
		t.Run(string(source), func(t *testing.T) {
			ctx, err := WithOperationContext(
				context.Background(),
				OperationContext{
					Scope:        models.ProjectScope{OrganizationID: 1, ProjectID: 2},
					Actor:        actor,
					Source:       source,
					CredentialID: "credential-agent-context-boundary",
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			_, updateErr := (&AgentNativeService{}).UpdateTicketVersion(
				ctx,
				VersionedTicketUpdateInput{
					TicketID:        1,
					ExpectedVersion: 1,
					Actor:           actor,
					CredentialID:    "credential-agent-context-boundary",
					SourceProtocol:  string(source),
					Changes: map[string]any{
						"agent_context": invalid,
					},
				},
			)
			if !errors.Is(updateErr, ErrInvalidAgentContext) {
				t.Fatalf(
					"machine update error = %v, want invalid Agent context",
					updateErr,
				)
			}
			if code := AgentNativeErrorCode(updateErr); code != "invalid_request" {
				t.Fatalf("machine update code = %q, want invalid_request", code)
			}
		})
	}
}

func TestMachineTicketUpdateRejectsMalformedAgentContextWithClosedError(t *testing.T) {
	actor := models.ServicePrincipalActor("agent-context-shape")
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:        models.ProjectScope{OrganizationID: 1, ProjectID: 2},
			Actor:        actor,
			Source:       SourceProtocolAgentREST,
			CredentialID: "credential-agent-context-shape",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, updateErr := (&AgentNativeService{}).UpdateTicketVersion(
		ctx,
		VersionedTicketUpdateInput{
			TicketID:        1,
			ExpectedVersion: 1,
			Actor:           actor,
			CredentialID:    "credential-agent-context-shape",
			SourceProtocol:  string(SourceProtocolAgentREST),
			Changes: map[string]any{
				"agent_context": map[string]any{
					"constraints": "must be a list",
				},
			},
		},
	)
	if !errors.Is(updateErr, ErrInvalidAgentContext) {
		t.Fatalf("malformed machine context error = %v", updateErr)
	}
	if code := AgentNativeErrorCode(updateErr); code != "invalid_request" {
		t.Fatalf("malformed machine context code = %q", code)
	}
}
