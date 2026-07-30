package services

import (
	"context"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestOperationContextValidation(t *testing.T) {
	t.Parallel()

	validHuman := OperationContext{
		Scope:  models.ProjectScope{OrganizationID: 1, ProjectID: 2},
		Actor:  models.HumanActor(3),
		Source: SourceProtocolHumanREST,
	}
	validAgent := OperationContext{
		Scope:        models.ProjectScope{OrganizationID: 1, ProjectID: 2},
		Actor:        models.ServicePrincipalActor("agent-1"),
		Source:       SourceProtocolA2A,
		CredentialID: "credential-1",
	}

	tests := []struct {
		name      string
		operation OperationContext
		wantError bool
	}{
		{name: "human", operation: validHuman},
		{name: "service principal", operation: validAgent},
		{
			name: "missing scope",
			operation: OperationContext{
				Actor:  models.HumanActor(3),
				Source: SourceProtocolHumanREST,
			},
			wantError: true,
		},
		{
			name: "missing actor",
			operation: OperationContext{
				Scope:  models.ProjectScope{OrganizationID: 1, ProjectID: 2},
				Source: SourceProtocolWorker,
			},
			wantError: true,
		},
		{
			name: "unknown source",
			operation: OperationContext{
				Scope:  models.ProjectScope{OrganizationID: 1, ProjectID: 2},
				Actor:  models.HumanActor(3),
				Source: SourceProtocol("body"),
			},
			wantError: true,
		},
		{
			name: "agent without credential",
			operation: OperationContext{
				Scope:  models.ProjectScope{OrganizationID: 1, ProjectID: 2},
				Actor:  models.ServicePrincipalActor("agent-1"),
				Source: SourceProtocolMCP,
			},
			wantError: true,
		},
		{
			name: "human with agent credential",
			operation: OperationContext{
				Scope:        models.ProjectScope{OrganizationID: 1, ProjectID: 2},
				Actor:        models.HumanActor(3),
				Source:       SourceProtocolHumanREST,
				CredentialID: "credential-1",
			},
			wantError: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.operation.Validate()
			if test.wantError && err == nil {
				t.Fatal("Validate() unexpectedly succeeded")
			}
			if !test.wantError && err != nil {
				t.Fatalf("Validate(): %v", err)
			}
		})
	}
}

func TestOperationContextRoundTripAndFailClosed(t *testing.T) {
	t.Parallel()

	if _, err := OperationContextFromContext(context.Background()); err == nil {
		t.Fatal("unscoped context unexpectedly succeeded")
	}
	if _, err := RequireProjectScope(context.Background()); err == nil {
		t.Fatal("unscoped repository guard unexpectedly succeeded")
	}

	operation := OperationContext{
		Scope:         models.ProjectScope{OrganizationID: 11, ProjectID: 12},
		Actor:         models.SystemActor("outbox-worker"),
		Source:        SourceProtocolWorker,
		TraceID:       "trace-1",
		CorrelationID: "correlation-1",
	}
	ctx, err := WithOperationContext(context.Background(), operation)
	if err != nil {
		t.Fatalf("WithOperationContext(): %v", err)
	}
	got, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatalf("OperationContextFromContext(): %v", err)
	}
	if got != operation {
		t.Fatalf("operation = %#v, want %#v", got, operation)
	}
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		t.Fatalf("RequireProjectScope(): %v", err)
	}
	if scope != operation.Scope {
		t.Fatalf("scope = %#v, want %#v", scope, operation.Scope)
	}
}
