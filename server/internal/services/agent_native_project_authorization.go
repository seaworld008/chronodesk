package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RevalidateOperationProjectAuthorization is the protocol-neutral live
// authorization entry point for services that can be called by either a Human
// or a Service Principal. The caller must already be inside the exact project
// transaction. It only dispatches by the trusted OperationContext actor and
// reuses the canonical Human or machine revalidation rules below.
func RevalidateOperationProjectAuthorization(
	ctx context.Context,
	db *gorm.DB,
	native *AgentNativeService,
	requiredScopes ...string,
) (*ProjectAccess, error) {
	if db == nil {
		return nil, ErrProjectAccessDenied
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	switch operation.Actor.Type {
	case models.ActorTypeHuman:
		userID, err := humanActorUserID(operation.Actor)
		if err != nil {
			return nil, err
		}
		projects, err := NewProjectService(db)
		if err != nil {
			return nil, err
		}
		return projects.RevalidateHumanProjectAccess(
			ctx,
			operation.Scope,
			userID,
		)
	case models.ActorTypeServicePrincipal:
		if native == nil {
			return nil, errors.New(
				"Agent project authorization is unavailable",
			)
		}
		return native.RevalidatePrincipalProjectOperation(
			ctx,
			requiredScopes...,
		)
	default:
		return nil, ErrInvalidActor
	}
}

// RevalidatePrincipalProjectOperation reloads every revocable authorization
// fact for a machine operation from the currently bound project transaction.
// Protocol adapters may use an earlier resolution only to select the trusted
// RLS scope; the live Grant, principal, credential and requested scopes are
// authoritative for execution.
func (s *AgentNativeService) RevalidatePrincipalProjectOperation(
	ctx context.Context,
	requiredScopes ...string,
) (*ProjectAccess, error) {
	operation, db, err := s.machineProjectAuthorizationContext(ctx)
	if err != nil {
		return nil, err
	}
	project, err := lockActiveAuthorizationProject(db, operation.Scope)
	if err != nil {
		return nil, err
	}
	return s.revalidatePrincipalProjectOperationLocked(
		operation,
		db,
		project,
		requiredScopes...,
	)
}

func (s *AgentNativeService) revalidatePrincipalProjectOperationAfterProject(
	ctx context.Context,
	project models.Project,
	requiredScopes ...string,
) (*ProjectAccess, error) {
	operation, db, err := s.machineProjectAuthorizationContext(ctx)
	if err != nil {
		return nil, err
	}
	if project.Scope() != operation.Scope ||
		project.Status != models.ProjectStatusActive {
		return nil, ErrProjectAccessDenied
	}
	return s.revalidatePrincipalProjectOperationLocked(
		operation,
		db,
		project,
		requiredScopes...,
	)
}

func (s *AgentNativeService) machineProjectAuthorizationContext(
	ctx context.Context,
) (OperationContext, *gorm.DB, error) {
	if s == nil || s.db == nil {
		return OperationContext{}, nil, errors.New(
			"Agent project authorization is unavailable",
		)
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return OperationContext{}, nil, err
	}
	if operation.Actor.Type != models.ActorTypeServicePrincipal ||
		operation.CredentialID == "" {
		return OperationContext{}, nil, ErrInvalidActor
	}
	switch operation.Source {
	case SourceProtocolAgentREST, SourceProtocolMCP, SourceProtocolA2A:
	default:
		return OperationContext{}, nil, fmt.Errorf(
			"%w: unsupported machine protocol %q",
			ErrInvalidActor,
			operation.Source,
		)
	}

	if err := requireExactProjectAuthorizationTransaction(
		ctx,
		operation.Scope,
	); err != nil {
		return OperationContext{}, nil, err
	}
	return operation, s.dbForContext(ctx), nil
}

func (s *AgentNativeService) revalidatePrincipalProjectOperationLocked(
	operation OperationContext,
	db *gorm.DB,
	project models.Project,
	requiredScopes ...string,
) (*ProjectAccess, error) {
	var principal models.ServicePrincipal
	err := db.
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where("id = ?", operation.Actor.ID).
		Take(&principal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPrincipalNotFound
	}
	if err != nil {
		return nil, fmt.Errorf(
			"lock machine project principal: %w",
			err,
		)
	}
	var grant models.ProjectPrincipalGrant
	err = db.Clauses(clause.Locking{Strength: "SHARE"}).
		Where(
			"project_id = ? AND service_principal_id = ? AND is_active = ?",
			operation.Scope.ProjectID,
			operation.Actor.ID,
			true,
		).
		Take(&grant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectAccessDenied
	}
	if err != nil {
		return nil, fmt.Errorf("lock machine project grant: %w", err)
	}
	var credential models.AgentCredential
	err = db.Clauses(clause.Locking{Strength: "SHARE"}).
		Where(
			"id = ? AND service_principal_id = ?",
			operation.CredentialID,
			operation.Actor.ID,
		).
		Take(&credential).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvalidCredential
	}
	if err != nil {
		return nil, fmt.Errorf("lock machine project credential: %w", err)
	}
	// Expiry is evaluated only after every revocable authorization row has
	// been locked. A request that waited behind a concurrent update must not
	// reuse a timestamp captured before the wait and thereby extend an expired
	// principal, Grant, or credential.
	now := s.now().UTC()
	if principal.DeletedAt != nil ||
		principal.Status != models.ServicePrincipalStatusActive ||
		principal.EmergencyDisabled {
		return nil, ErrPrincipalDisabled
	}
	if principal.ExpiresAt != nil &&
		!principal.ExpiresAt.After(now) {
		return nil, ErrPrincipalExpired
	}
	if !grant.Role.IsValid() ||
		(grant.ExpiresAt != nil && !grant.ExpiresAt.After(now)) {
		return nil, ErrProjectAccessDenied
	}
	if credential.Status != models.AgentCredentialStatusActive ||
		credential.RevokedAt != nil {
		return nil, ErrInvalidCredential
	}
	if !credential.ExpiresAt.After(now) {
		return nil, ErrCredentialExpired
	}

	grantScopes, err := grant.ScopeList()
	if err != nil {
		return nil, ErrProjectAccessDenied
	}
	for _, required := range requiredScopes {
		if !grant.HasScope(required) ||
			!principal.HasScope(required) {
			return nil, ErrProjectAccessDenied
		}
	}
	return &ProjectAccess{
		Project: project,
		Role:    grant.Role,
		Scope:   operation.Scope,
		Scopes:  grantScopes,
		AuthorizationSnapshot: newPrincipalAuthorizationSnapshot(
			operation.Scope,
			project,
			principal,
			grant,
			grantScopes,
			&credential,
		),
	}, nil
}
