package services

import (
	"context"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

type operationContextScopeRecord struct {
	ID             uint `gorm:"primaryKey"`
	OrganizationID uint
	ProjectID      uint
	Value          string
}

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

func TestRunProjectOperationRejectsMultiProjectTransactionReuse(
	t *testing.T,
) {
	db := openTestDB(t)
	if err := db.Exec(`
		CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO projects (id, organization_id)
		VALUES (101, 9), (102, 9)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&operationContextScopeRecord{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&operationContextScopeRecord{
		OrganizationID: 9,
		ProjectID:      102,
		Value:          "project-b-secret",
	}).Error; err != nil {
		t.Fatal(err)
	}

	projectA := models.ProjectScope{OrganizationID: 9, ProjectID: 101}
	operationCtx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  projectA,
			Actor:  models.HumanActor(41),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	projectOperationCalled := false
	err = scopeddb.WithAuthorizedProjectScopeTransaction(
		operationCtx,
		db,
		projectA.OrganizationID,
		[]uint{projectA.ProjectID, 102},
		func(multiProjectCtx context.Context) error {
			var visible int64
			if queryErr := db.WithContext(multiProjectCtx).
				Model(&operationContextScopeRecord{}).
				Where(
					"organization_id = ? AND project_id = ?",
					projectA.OrganizationID,
					102,
				).
				Count(&visible).Error; queryErr != nil {
				return queryErr
			}
			if visible != 1 {
				t.Fatalf(
					"multi-project transaction did not expose project B test record",
				)
			}

			return runProjectOperation(
				multiProjectCtx,
				db,
				func(projectCtx context.Context) error {
					projectOperationCalled = true
					return db.WithContext(projectCtx).
						Where("project_id = ?", 102).
						First(&operationContextScopeRecord{}).
						Error
				},
			)
		},
	)
	if err == nil {
		t.Fatal("project A operation reused an A+B transaction")
	}
	if !strings.Contains(err.Error(), "does not exactly match") {
		t.Fatalf("unexpected scope mismatch error: %v", err)
	}
	if projectOperationCalled {
		t.Fatal("project A callback ran with widened A+B visibility")
	}
}

func TestRunProjectOperationReusesMatchingScopeAndNestedTransaction(
	t *testing.T,
) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&operationContextScopeRecord{}); err != nil {
		t.Fatal(err)
	}
	scope := models.ProjectScope{OrganizationID: 13, ProjectID: 17}
	operationCtx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  scope,
			Actor:  models.SystemActor("matching-scope-test"),
			Source: SourceProtocolWorker,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	called := false
	err = scopeddb.WithProjectScopeContextTransaction(
		operationCtx,
		db,
		scope,
		func(scopedCtx context.Context) error {
			return runProjectOperation(
				scopedCtx,
				db,
				func(projectCtx context.Context) error {
					called = true
					return transactionForContext(
						projectCtx,
						db,
						func(tx *gorm.DB) error {
							return tx.Create(&operationContextScopeRecord{
								OrganizationID: scope.OrganizationID,
								ProjectID:      scope.ProjectID,
								Value:          "nested-commit",
							}).Error
						},
					)
				},
			)
		},
	)
	if err != nil {
		t.Fatalf("matching project operation: %v", err)
	}
	if !called {
		t.Fatal("matching project operation callback was not called")
	}

	var count int64
	if err := db.Model(&operationContextScopeRecord{}).
		Where(
			"organization_id = ? AND project_id = ? AND value = ?",
			scope.OrganizationID,
			scope.ProjectID,
			"nested-commit",
		).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("nested committed record count = %d, want 1", count)
	}
}
