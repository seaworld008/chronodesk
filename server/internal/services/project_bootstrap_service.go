package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

const bootstrapProjectAdministratorActor = "chronodesk-bootstrap"
const projectScopeMigrationMembershipActor = "chronodesk-project-scope-migration"

// EnsureBootstrapProjectAdministratorMembership is the trusted composition
// seam used by the explicit seed command. The caller supplies its open seed
// transaction so the privilege grant, CloudEvent, Outbox delivery and audit
// ledger entry either commit together with the seed or all roll back.
func EnsureBootstrapProjectAdministratorMembership(
	ctx context.Context,
	tx *gorm.DB,
	administrator models.User,
	scope models.ProjectScope,
) error {
	if administrator.Role != models.RoleAdmin {
		return errors.New("bootstrap project administrator identity is invalid")
	}
	return ensureBootstrapProjectMembership(
		ctx,
		tx,
		administrator,
		scope,
		models.ProjectRoleAdmin,
		bootstrapProjectAdministratorActor,
	)
}

// EnsureProjectScopeMigrationMembership gives every active legacy human an
// explicit default-project role through the same audited domain path used by
// normal membership administration. It is injected into the schema migration
// by the application and migration-command composition roots.
func EnsureProjectScopeMigrationMembership(
	ctx context.Context,
	tx *gorm.DB,
	user models.User,
	scope models.ProjectScope,
	role models.ProjectRole,
) error {
	return ensureBootstrapProjectMembership(
		ctx,
		tx,
		user,
		scope,
		role,
		projectScopeMigrationMembershipActor,
	)
}

func ensureBootstrapProjectMembership(
	ctx context.Context,
	tx *gorm.DB,
	user models.User,
	scope models.ProjectScope,
	role models.ProjectRole,
	actorComponent string,
) error {
	if ctx == nil {
		return errors.New("bootstrap project membership context is required")
	}
	if tx == nil || tx.Statement == nil || tx.Statement.ConnPool == nil {
		return errors.New("bootstrap project membership transaction is required")
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return errors.New("bootstrap project membership requires an active transaction")
	}
	if user.ID == 0 ||
		!user.Role.IsValid() ||
		user.Status != models.UserStatusActive ||
		!role.IsValid() {
		return errors.New("bootstrap project membership identity or role is invalid")
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("bootstrap project membership scope: %w", err)
	}
	if err := scopeddb.ConfigureProjectScopeTransaction(tx, scope); err != nil {
		return fmt.Errorf(
			"configure bootstrap project membership transaction scope: %w",
			err,
		)
	}

	actor := models.SystemActor(actorComponent)
	operationContext, err := EnsureSystemProjectOperationContext(
		ctx,
		scope,
		actor,
		"",
		"",
	)
	if err != nil {
		return err
	}
	auditLedger, err := NewAuditLedgerService(tx)
	if err != nil {
		return fmt.Errorf("initialize bootstrap audit ledger: %w", err)
	}
	eventWriter := NewAgentNativeService(tx, AgentNativeOptions{
		EventSource: "urn:chronodesk:bootstrap",
		AuditLedger: auditLedger,
		DefaultOutboxTargets: []OutboxTarget{
			{Type: "event_stream", ID: "default", MaxAttempts: 8},
		},
	})
	projectService, err := NewProjectService(tx, eventWriter)
	if err != nil {
		return fmt.Errorf("initialize bootstrap project service: %w", err)
	}
	if _, err := projectService.EnsureHumanMembership(
		operationContext,
		scope,
		UpsertProjectMembershipInput{
			UserID: user.ID,
			Role:   role,
		},
	); err != nil {
		return fmt.Errorf("ensure bootstrap project membership: %w", err)
	}
	return nil
}
