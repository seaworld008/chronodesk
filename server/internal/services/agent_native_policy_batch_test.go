package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

func TestReadPolicyBatchPersistsTrustedProjectScope(t *testing.T) {
	db := openAgentNativeTestDB(t)
	service := NewAgentNativeService(db)
	principal := createNativePrincipal(
		t,
		service,
		0,
		"scoped-read-policy",
		models.ScopeTicketsRead,
	)
	credential, err := service.IssueCredential(
		context.Background(),
		principal.ID,
		"scoped-read-policy",
		time.Hour,
	)
	if err != nil {
		t.Fatalf("issue credential: %v", err)
	}
	scope := models.ProjectScope{OrganizationID: 41, ProjectID: 73}
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:        scope,
			Actor:        models.ServicePrincipalActor(principal.ID),
			Source:       SourceProtocolAgentREST,
			CredentialID: credential.Credential.ID,
		},
	)
	if err != nil {
		t.Fatalf("bind operation context: %v", err)
	}

	const callbackName = "test:require_scoped_read_policy_decision"
	if err := db.Callback().Create().
		Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			decision, ok := tx.Statement.Dest.(*models.PolicyDecision)
			if !ok {
				return
			}
			if !scopeddb.HasTransaction(tx.Statement.Context) {
				_ = tx.AddError(errors.New(
					"policy decision was created outside a project transaction",
				))
				return
			}
			if decision.OrganizationID == 0 || decision.ProjectID == 0 {
				_ = tx.AddError(errors.New(
					"policy decision is missing trusted project scope",
				))
			}
		}); err != nil {
		t.Fatalf("register policy scope assertion: %v", err)
	}

	batch, err := service.PrepareReadPolicyBatch(
		ctx,
		PolicyCheckInput{
			ServicePrincipalID: principal.ID,
			CredentialID:       credential.Credential.ID,
			Scope:              models.ScopeTicketsRead,
			Action:             "ticket.list",
			ResourceType:       "ticket",
			ResourceID:         "*",
			SourceProtocol:     "rest",
		},
	)
	if err != nil {
		t.Fatalf("prepare scoped read policy batch: %v", err)
	}
	if _, err := batch.RecordSummary(
		ctx,
		map[string]any{"items_returned": 0},
	); err != nil {
		t.Fatalf("record scoped read policy batch: %v", err)
	}

	var decision models.PolicyDecision
	if err := db.
		Where(
			"service_principal_id = ? AND action = ?",
			principal.ID,
			"ticket.list",
		).
		First(&decision).Error; err != nil {
		t.Fatalf("load read policy decision: %v", err)
	}
	if decision.OrganizationID != scope.OrganizationID ||
		decision.ProjectID != scope.ProjectID {
		t.Fatalf(
			"policy decision scope = %d/%d, want %d/%d",
			decision.OrganizationID,
			decision.ProjectID,
			scope.OrganizationID,
			scope.ProjectID,
		)
	}
}

func TestReadPolicyBatchRejectsUntrustedOrChangedScope(t *testing.T) {
	db := openAgentNativeTestDB(t)
	service := NewAgentNativeService(db)
	principal := createNativePrincipal(
		t,
		service,
		0,
		"scope-change-read-policy",
		models.ScopeTicketsRead,
	)
	credential, err := service.IssueCredential(
		context.Background(),
		principal.ID,
		"scope-change-read-policy",
		time.Hour,
	)
	if err != nil {
		t.Fatalf("issue credential: %v", err)
	}
	input := PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		CredentialID:       credential.Credential.ID,
		Scope:              models.ScopeTicketsRead,
		Action:             "ticket.list",
		ResourceType:       "ticket",
		ResourceID:         "*",
		SourceProtocol:     "rest",
	}
	if _, err := service.PrepareReadPolicyBatch(
		context.Background(),
		input,
	); err == nil {
		t.Fatal("read policy batch accepted a missing trusted operation context")
	}

	scope := models.ProjectScope{OrganizationID: 19, ProjectID: 23}
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:        scope,
			Actor:        models.ServicePrincipalActor(principal.ID),
			Source:       SourceProtocolAgentREST,
			CredentialID: credential.Credential.ID,
		},
	)
	if err != nil {
		t.Fatalf("bind operation context: %v", err)
	}
	batch, err := service.PrepareReadPolicyBatch(ctx, input)
	if err != nil {
		t.Fatalf("prepare read policy batch: %v", err)
	}
	changedContext, err := WithOperationContext(
		ctx,
		OperationContext{
			Scope: models.ProjectScope{
				OrganizationID: scope.OrganizationID,
				ProjectID:      scope.ProjectID + 1,
			},
			Actor:        models.ServicePrincipalActor(principal.ID),
			Source:       SourceProtocolAgentREST,
			CredentialID: credential.Credential.ID,
		},
	)
	if err != nil {
		t.Fatalf("bind changed operation context: %v", err)
	}
	if _, err := batch.RecordSummary(changedContext, nil); err == nil {
		t.Fatal("read policy batch accepted a changed trusted project scope")
	}

	var decisions int64
	if err := db.Model(&models.PolicyDecision{}).Count(&decisions).Error; err != nil {
		t.Fatalf("count policy decisions: %v", err)
	}
	if decisions != 0 {
		t.Fatalf("scope validation failure persisted %d decisions", decisions)
	}
}
