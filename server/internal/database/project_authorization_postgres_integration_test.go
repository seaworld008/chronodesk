package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const postgresAuthorizationBarrierTimeout = 3 * time.Second

func TestPostgresHumanAuthorizationRevalidationLocksMembershipUntilTransactionCompletes(
	t *testing.T,
) {
	db, revocationConnection, project, suffix := openPostgresAuthorizationBarrierFixture(
		t,
		"authz_human",
	)
	user := models.User{
		Username:     "authz-human-" + suffix,
		Email:        "authz-human-" + suffix + "@example.test",
		PasswordHash: "test-only-password-hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create active Human: %v", err)
	}
	membership := models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    user.ID,
		Role:      models.ProjectRoleManager,
		IsActive:  true,
	}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatalf("create active project membership: %v", err)
	}
	projectService, err := services.NewProjectService(db)
	if err != nil {
		t.Fatalf("create Project service: %v", err)
	}

	var revocationResult <-chan postgresAuthorizationRevocationResult
	err = WithProjectScopeContextTransaction(
		context.Background(),
		db,
		project.Scope(),
		func(ctx context.Context) error {
			access, revalidateErr := projectService.RevalidateHumanProjectAccess(
				ctx,
				project.Scope(),
				user.ID,
			)
			if revalidateErr != nil {
				return revalidateErr
			}
			if access == nil ||
				access.Scope != project.Scope() ||
				access.Role != models.ProjectRoleManager {
				return errors.New("active Human access was not revalidated")
			}

			revocationResult = beginPostgresAuthorizationRevocation(
				t,
				db,
				revocationConnection,
				`UPDATE project_memberships
				 SET is_active = FALSE
				 WHERE id = $1`,
				membership.ID,
			)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("complete active Human project transaction: %v", err)
	}

	assertPostgresAuthorizationRevocationSucceeds(
		t,
		revocationResult,
	)

	businessExecuted := false
	err = WithProjectScopeContextTransaction(
		context.Background(),
		db,
		project.Scope(),
		func(ctx context.Context) error {
			if _, revalidateErr := projectService.RevalidateHumanProjectAccess(
				ctx,
				project.Scope(),
				user.ID,
			); revalidateErr != nil {
				return revalidateErr
			}
			businessExecuted = true
			return nil
		},
	)
	if !errors.Is(err, services.ErrProjectAccessDenied) {
		t.Fatalf(
			"revoked Human revalidation error = %v, want project access denied",
			err,
		)
	}
	if businessExecuted {
		t.Fatal("revoked Human authorization executed the business callback")
	}
}

func TestPostgresMachineAuthorizationRevalidationLinearizesEveryRevocableRow(
	t *testing.T,
) {
	db, revocationConnection, project, suffix := openPostgresAuthorizationBarrierFixture(
		t,
		"authz_machine",
	)
	testCases := []struct {
		name       string
		statement  func(postgresMachineAuthorizationFixture) string
		arguments  func(postgresMachineAuthorizationFixture) []any
		wantDenied error
	}{
		{
			name: "Grant",
			statement: func(postgresMachineAuthorizationFixture) string {
				return `UPDATE project_principal_grants
					SET is_active = FALSE
					WHERE id = $1`
			},
			arguments: func(fixture postgresMachineAuthorizationFixture) []any {
				return []any{fixture.grant.ID}
			},
			wantDenied: services.ErrProjectAccessDenied,
		},
		{
			name: "Credential",
			statement: func(postgresMachineAuthorizationFixture) string {
				return `UPDATE agent_credentials
					SET status = $2, revoked_at = CURRENT_TIMESTAMP
					WHERE id = $1`
			},
			arguments: func(fixture postgresMachineAuthorizationFixture) []any {
				return []any{
					fixture.credential.ID,
					models.AgentCredentialStatusRevoked,
				}
			},
			wantDenied: services.ErrInvalidCredential,
		},
		{
			name: "Principal_status",
			statement: func(postgresMachineAuthorizationFixture) string {
				return `UPDATE service_principals
					SET status = $2
					WHERE id = $1`
			},
			arguments: func(fixture postgresMachineAuthorizationFixture) []any {
				return []any{
					fixture.principal.ID,
					models.ServicePrincipalStatusRevoked,
				}
			},
			wantDenied: services.ErrPrincipalDisabled,
		},
		{
			name: "Project_status",
			statement: func(postgresMachineAuthorizationFixture) string {
				return `UPDATE projects
					SET status = $2
					WHERE id = $1`
			},
			arguments: func(fixture postgresMachineAuthorizationFixture) []any {
				return []any{
					fixture.project.ID,
					models.ProjectStatusArchived,
				}
			},
			wantDenied: services.ErrProjectAccessDenied,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := seedPostgresMachineAuthorizationFixture(
				t,
				db,
				project,
				strings.ToLower(testCase.name)+"-"+suffix,
			)
			native := services.NewAgentNativeService(db)
			operationContext := fixture.operationContext(t)

			var revocationResult <-chan postgresAuthorizationRevocationResult
			err := WithProjectScopeContextTransaction(
				operationContext,
				db,
				project.Scope(),
				func(ctx context.Context) error {
					access, revalidateErr :=
						native.RevalidatePrincipalProjectOperation(
							ctx,
							models.ScopeTicketsRead,
						)
					if revalidateErr != nil {
						return revalidateErr
					}
					if access == nil ||
						access.Scope != project.Scope() ||
						access.Role != models.ProjectRoleAgent {
						return errors.New(
							"active Service Principal access was not revalidated",
						)
					}

					revocationResult =
						beginPostgresAuthorizationRevocation(
							t,
							db,
							revocationConnection,
							testCase.statement(fixture),
							testCase.arguments(fixture)...,
						)
					return nil
				},
			)
			if err != nil {
				t.Fatalf(
					"complete active Principal project transaction: %v",
					err,
				)
			}
			assertPostgresAuthorizationRevocationSucceeds(
				t,
				revocationResult,
			)

			businessExecuted := false
			err = WithProjectScopeContextTransaction(
				operationContext,
				db,
				project.Scope(),
				func(ctx context.Context) error {
					if _, revalidateErr :=
						native.RevalidatePrincipalProjectOperation(
							ctx,
							models.ScopeTicketsRead,
						); revalidateErr != nil {
						return revalidateErr
					}
					businessExecuted = true
					return nil
				},
			)
			if !errors.Is(err, testCase.wantDenied) {
				t.Fatalf(
					"revoked %s revalidation error = %v, want %v",
					testCase.name,
					err,
					testCase.wantDenied,
				)
			}
			if businessExecuted {
				t.Fatalf(
					"revoked %s authorization executed the business callback",
					testCase.name,
				)
			}
		})
	}
}

type postgresMachineAuthorizationFixture struct {
	project    models.Project
	principal  models.ServicePrincipal
	grant      models.ProjectPrincipalGrant
	credential models.AgentCredential
}

func seedPostgresMachineAuthorizationFixture(
	t *testing.T,
	db *gorm.DB,
	project models.Project,
	name string,
) postgresMachineAuthorizationFixture {
	t.Helper()
	principal := models.ServicePrincipal{
		ID:     uuid.NewString(),
		Name:   "authz-principal-" + name,
		Status: models.ServicePrincipalStatusActive,
		Scopes: datatypes.JSON(
			`["tickets:read","tickets:create"]`,
		),
	}
	if err := db.Create(&principal).Error; err != nil {
		t.Fatalf("create active Service Principal: %v", err)
	}
	credential := models.AgentCredential{
		ID:                 uuid.NewString(),
		ServicePrincipalID: principal.ID,
		Name:               "authz-credential-" + name,
		SecretHash:         strings.Repeat("a", 64),
		Status:             models.AgentCredentialStatusActive,
		ExpiresAt:          time.Now().UTC().Add(time.Hour),
	}
	if err := db.Create(&credential).Error; err != nil {
		t.Fatalf("create active Agent credential: %v", err)
	}
	grant := models.ProjectPrincipalGrant{
		ProjectID:          project.ID,
		ServicePrincipalID: principal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes: datatypes.JSON(
			`["tickets:read","tickets:create"]`,
		),
		IsActive: true,
	}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatalf("create active Principal Grant: %v", err)
	}
	return postgresMachineAuthorizationFixture{
		project:    project,
		principal:  principal,
		grant:      grant,
		credential: credential,
	}
}

func (fixture postgresMachineAuthorizationFixture) operationContext(
	t *testing.T,
) context.Context {
	return fixture.operationContextForSource(
		t,
		services.SourceProtocolAgentREST,
	)
}

func (fixture postgresMachineAuthorizationFixture) operationContextForSource(
	t *testing.T,
	source services.SourceProtocol,
) context.Context {
	t.Helper()
	ctx, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:        fixture.project.Scope(),
			Actor:        models.ServicePrincipalActor(fixture.principal.ID),
			Source:       source,
			CredentialID: fixture.credential.ID,
		},
	)
	if err != nil {
		t.Fatalf("bind Principal operation context: %v", err)
	}
	return ctx
}

func TestPostgresMachineAuthorizationSamplesExpiryAfterFinalCredentialLock(
	t *testing.T,
) {
	db, blocker, project, suffix := openPostgresAuthorizationBarrierFixture(
		t,
		"authz_expiry",
	)
	testCases := []struct {
		name       string
		expire     func(*testing.T, *gorm.DB, postgresMachineAuthorizationFixture, time.Time)
		wantDenied error
	}{
		{
			name: "Principal",
			expire: func(
				t *testing.T,
				db *gorm.DB,
				fixture postgresMachineAuthorizationFixture,
				expiresAt time.Time,
			) {
				t.Helper()
				if err := db.Model(&models.ServicePrincipal{}).
					Where("id = ?", fixture.principal.ID).
					Update("expires_at", expiresAt).Error; err != nil {
					t.Fatalf("set Principal expiry: %v", err)
				}
			},
			wantDenied: services.ErrPrincipalExpired,
		},
		{
			name: "Grant",
			expire: func(
				t *testing.T,
				db *gorm.DB,
				fixture postgresMachineAuthorizationFixture,
				expiresAt time.Time,
			) {
				t.Helper()
				if err := db.Model(&models.ProjectPrincipalGrant{}).
					Where("id = ?", fixture.grant.ID).
					Update("expires_at", expiresAt).Error; err != nil {
					t.Fatalf("set Grant expiry: %v", err)
				}
			},
			wantDenied: services.ErrProjectAccessDenied,
		},
		{
			name: "Credential",
			expire: func(
				t *testing.T,
				db *gorm.DB,
				fixture postgresMachineAuthorizationFixture,
				expiresAt time.Time,
			) {
				t.Helper()
				if err := db.Model(&models.AgentCredential{}).
					Where("id = ?", fixture.credential.ID).
					Update("expires_at", expiresAt).Error; err != nil {
					t.Fatalf("set credential expiry: %v", err)
				}
			},
			wantDenied: services.ErrCredentialExpired,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := seedPostgresMachineAuthorizationFixture(
				t,
				db,
				project,
				"expiry-"+strings.ToLower(testCase.name)+"-"+suffix,
			)
			expiresAt := time.Now().UTC().Add(2 * time.Second)
			testCase.expire(t, db, fixture, expiresAt)

			blockerContext, blockerCancel := context.WithTimeout(
				context.Background(),
				10*time.Second,
			)
			defer blockerCancel()
			if _, err := blocker.ExecContext(blockerContext, "BEGIN"); err != nil {
				t.Fatalf("begin credential barrier transaction: %v", err)
			}
			blockerCommitted := false
			defer func() {
				if !blockerCommitted {
					_, _ = blocker.ExecContext(
						context.Background(),
						"ROLLBACK",
					)
				}
			}()
			var lockedCredentialID string
			if err := blocker.QueryRowContext(
				blockerContext,
				`SELECT id
				 FROM agent_credentials
				 WHERE id = $1
				 FOR UPDATE`,
				fixture.credential.ID,
			).Scan(&lockedCredentialID); err != nil {
				t.Fatalf("lock final credential authorization row: %v", err)
			}
			if lockedCredentialID != fixture.credential.ID {
				t.Fatalf(
					"locked credential = %q, want %q",
					lockedCredentialID,
					fixture.credential.ID,
				)
			}

			type revalidationResult struct {
				callbackExecuted bool
				err              error
			}
			pid := make(chan int, 1)
			completed := make(chan struct{})
			result := make(chan revalidationResult, 1)
			native := services.NewAgentNativeService(db)
			operationContext := fixture.operationContext(t)
			go func() {
				defer close(completed)
				callbackExecuted := false
				err := WithProjectScopeContextTransaction(
					operationContext,
					db,
					project.Scope(),
					func(ctx context.Context) error {
						var backendPID int
						if pidErr := db.WithContext(ctx).
							Raw("SELECT pg_backend_pid()").
							Scan(&backendPID).Error; pidErr != nil {
							return pidErr
						}
						pid <- backendPID
						if _, revalidateErr :=
							native.RevalidatePrincipalProjectOperation(
								ctx,
								models.ScopeTicketsRead,
							); revalidateErr != nil {
							return revalidateErr
						}
						callbackExecuted = true
						return nil
					},
				)
				result <- revalidationResult{
					callbackExecuted: callbackExecuted,
					err:              err,
				}
			}()

			var backendPID int
			select {
			case backendPID = <-pid:
			case early := <-result:
				t.Fatalf(
					"revalidation completed before credential barrier: %+v",
					early,
				)
			case <-time.After(5 * time.Second):
				t.Fatal("revalidation did not enter its PostgreSQL transaction")
			}
			waitForPostgresBackendLock(
				t,
				db,
				backendPID,
				completed,
			)
			if !time.Now().Before(expiresAt) {
				t.Fatal(
					"credential lock barrier was reached only after expiry",
				)
			}
			untilExpired := time.Until(expiresAt.Add(150 * time.Millisecond))
			timer := time.NewTimer(untilExpired)
			select {
			case <-completed:
				timer.Stop()
				t.Fatal(
					"revalidation completed while the final credential lock was held",
				)
			case <-timer.C:
			}
			if _, err := blocker.ExecContext(
				blockerContext,
				"COMMIT",
			); err != nil {
				t.Fatalf("release credential barrier transaction: %v", err)
			}
			blockerCommitted = true

			select {
			case completedResult := <-result:
				if !errors.Is(completedResult.err, testCase.wantDenied) {
					t.Fatalf(
						"%s expiry revalidation error = %v, want %v",
						testCase.name,
						completedResult.err,
						testCase.wantDenied,
					)
				}
				if completedResult.callbackExecuted {
					t.Fatalf(
						"%s expiry executed the business callback",
						testCase.name,
					)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf(
					"%s expiry revalidation did not finish after lock release",
					testCase.name,
				)
			}
		})
	}
}

type postgresAuthorizationEventAppender struct{}

func (postgresAuthorizationEventAppender) AppendDomainEventTx(
	context.Context,
	*gorm.DB,
	services.DomainEventInput,
	[]services.OutboxTarget,
) (*models.DomainEvent, error) {
	return &models.DomainEvent{ID: uuid.NewString()}, nil
}

type postgresHumanMembershipCommand func(
	*services.ProjectService,
	context.Context,
	models.ProjectScope,
	uint,
) error

func TestPostgresHumanPreflightToMembershipCommandsCannotDeadlock(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		fixtureName string
		command     postgresHumanMembershipCommand
	}{
		{
			name:        "Deactivate",
			fixtureName: "ma_d",
			command: func(
				service *services.ProjectService,
				ctx context.Context,
				scope models.ProjectScope,
				targetID uint,
			) error {
				_, err := service.DeactivateHumanMembership(
					ctx,
					scope,
					targetID,
				)
				return err
			},
		},
		{
			name:        "Upsert",
			fixtureName: "ma_u",
			command: func(
				service *services.ProjectService,
				ctx context.Context,
				scope models.ProjectScope,
				targetID uint,
			) error {
				_, err := service.UpsertHumanMembership(
					ctx,
					scope,
					services.UpsertProjectMembershipInput{
						UserID: targetID,
						Role:   models.ProjectRoleManager,
					},
				)
				return err
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runPostgresCrossAdministratorMembershipCommand(
				t,
				testCase.fixtureName,
				testCase.command,
			)
		})
	}
}

func runPostgresCrossAdministratorMembershipCommand(
	t *testing.T,
	fixtureName string,
	command postgresHumanMembershipCommand,
) {
	t.Helper()
	db, blocker, project, suffix := openPostgresAuthorizationBarrierFixture(
		t,
		fixtureName,
	)
	administrators := make([]models.User, 2)
	for index := range administrators {
		administrators[index] = models.User{
			Username: fmt.Sprintf("authz-admin-%d-%s", index, suffix),
			Email: fmt.Sprintf(
				"authz-admin-%d-%s@example.test",
				index,
				suffix,
			),
			PasswordHash: "test-only-password-hash",
			PlatformRole: models.PlatformRoleMember,
			Status:       models.UserStatusActive,
		}
		if err := db.Create(&administrators[index]).Error; err != nil {
			t.Fatalf("create project administrator %d: %v", index, err)
		}
		membership := models.ProjectMembership{
			ProjectID: project.ID,
			UserID:    administrators[index].ID,
			Role:      models.ProjectRoleAdmin,
			IsActive:  true,
			Version:   1,
		}
		if err := db.Create(&membership).Error; err != nil {
			t.Fatalf("create project administrator membership %d: %v", index, err)
		}
	}
	_, commandDatabases, backendPIDs :=
		openPostgresAuthorizationCommandDatabases(
			t,
			db,
			len(administrators),
		)
	commandServices := make([]*services.ProjectService, len(administrators))
	for index := range administrators {
		commandService, serviceErr := services.NewProjectService(
			commandDatabases[index],
			postgresAuthorizationEventAppender{},
		)
		if serviceErr != nil {
			t.Fatalf(
				"create membership command service %d: %v",
				index,
				serviceErr,
			)
		}
		commandServices[index] = commandService
	}

	blockerContext, blockerCancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer blockerCancel()
	if _, err := blocker.ExecContext(blockerContext, "BEGIN"); err != nil {
		t.Fatalf("begin Project lock barrier transaction: %v", err)
	}
	blockerCommitted := false
	defer func() {
		if !blockerCommitted {
			_, _ = blocker.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	var lockedProjectID uint
	if err := blocker.QueryRowContext(
		blockerContext,
		`SELECT id
		 FROM projects
		 WHERE id = $1
		 FOR UPDATE`,
		project.ID,
	).Scan(&lockedProjectID); err != nil {
		t.Fatalf("lock membership administration Project: %v", err)
	}

	type administrationResult struct {
		requesterID uint
		err         error
	}
	completed := make([]chan struct{}, len(administrators))
	results := make(chan administrationResult, 2)
	for index := range administrators {
		requester := administrators[index]
		target := administrators[1-index]
		commandService := commandServices[index]
		completed[index] = make(chan struct{})
		commandCompleted := completed[index]
		operationContext, contextErr := services.WithOperationContext(
			context.Background(),
			services.OperationContext{
				Scope:  project.Scope(),
				Actor:  models.HumanActor(requester.ID),
				Source: services.SourceProtocolHumanREST,
			},
		)
		if contextErr != nil {
			t.Fatalf("bind administrator operation context: %v", contextErr)
		}
		go func() {
			defer close(commandCompleted)
			preflight, err := commandService.ResolveHumanProject(
				operationContext,
				string(project.Key),
				requester.ID,
			)
			if err == nil &&
				(preflight == nil ||
					preflight.Scope != project.Scope() ||
					preflight.Role != models.ProjectRoleAdmin) {
				err = errors.New(
					"Human preflight did not resolve project administrator access",
				)
			}
			if err == nil {
				err = command(
					commandService,
					operationContext,
					project.Scope(),
					target.ID,
				)
			}
			results <- administrationResult{
				requesterID: requester.ID,
				err:         err,
			}
		}()
	}

	for index, backendPID := range backendPIDs {
		waitForPostgresBackendLock(
			t,
			db,
			backendPID,
			completed[index],
		)
	}
	if _, err := blocker.ExecContext(blockerContext, "COMMIT"); err != nil {
		t.Fatalf("release Project lock barrier transaction: %v", err)
	}
	blockerCommitted = true

	successes := 0
	denials := 0
	for range administrators {
		select {
		case result := <-results:
			switch {
			case result.err == nil:
				successes++
			case errors.Is(result.err, services.ErrProjectAccessDenied):
				denials++
			default:
				t.Fatalf(
					"administrator %d cross-revocation error = %v",
					result.requesterID,
					result.err,
				)
			}
		case <-time.After(5 * time.Second):
			t.Fatal(
				"cross-administrator revocations did not finish without deadlock",
			)
		}
	}
	if successes != 1 || denials != 1 {
		t.Fatalf(
			"cross-revocation results: successes=%d denials=%d, want 1/1",
			successes,
			denials,
		)
	}
	var activeAdministrators int64
	if err := db.Model(&models.ProjectMembership{}).
		Where(
			"project_id = ? AND role = ? AND is_active = ?",
			project.ID,
			models.ProjectRoleAdmin,
			true,
		).
		Count(&activeAdministrators).Error; err != nil {
		t.Fatalf("count active project administrators: %v", err)
	}
	if activeAdministrators != 1 {
		t.Fatalf(
			"active project administrators = %d, want 1",
			activeAdministrators,
		)
	}
}

func TestPostgresHumanPreflightToTicketCreateDatabaseCommandCannotDeadlock(
	t *testing.T,
) {
	db, blocker, project, suffix := openPostgresAuthorizationBarrierFixture(
		t,
		"tc_h",
	)
	bootstrapPostgresTicketConfiguration(t, db)
	user := models.User{
		Username:     "authz-ticket-requester-" + suffix,
		Email:        "authz-ticket-requester-" + suffix + "@example.test",
		PasswordHash: "test-only-password-hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create ticket requester: %v", err)
	}
	membership := models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    user.ID,
		Role:      models.ProjectRoleRequester,
		IsActive:  true,
		Version:   1,
	}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatalf("create ticket requester Membership: %v", err)
	}
	_, commandDatabases, backendPIDs :=
		openPostgresAuthorizationCommandDatabases(t, db, 2)
	commandServices := make([]*services.ProjectService, 2)
	nativeServices := make([]*services.AgentNativeService, 2)
	for index := range commandServices {
		service, err := services.NewProjectService(commandDatabases[index])
		if err != nil {
			t.Fatalf("create ticket command Project service %d: %v", index, err)
		}
		commandServices[index] = service
		ledger, err := services.NewAuditLedgerService(commandDatabases[index])
		if err != nil {
			t.Fatalf("create ticket command Audit service %d: %v", index, err)
		}
		nativeServices[index] = services.NewAgentNativeService(
			commandDatabases[index],
			services.AgentNativeOptions{AuditLedger: ledger},
		)
	}

	blockerContext, blockerCancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer blockerCancel()
	if _, err := blocker.ExecContext(blockerContext, "BEGIN"); err != nil {
		t.Fatalf("begin ticket command Project barrier: %v", err)
	}
	blockerCommitted := false
	defer func() {
		if !blockerCommitted {
			_, _ = blocker.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	var lockedProjectID uint
	if err := blocker.QueryRowContext(
		blockerContext,
		`SELECT id
		 FROM projects
		 WHERE id = $1
		 FOR UPDATE`,
		project.ID,
	).Scan(&lockedProjectID); err != nil {
		t.Fatalf("lock ticket command Project: %v", err)
	}

	type ticketCommandResult struct {
		ticketNumber string
		err          error
	}
	completed := []chan struct{}{make(chan struct{}), make(chan struct{})}
	results := make(chan ticketCommandResult, len(commandServices))
	for index, commandService := range commandServices {
		nativeService := nativeServices[index]
		commandIndex := index
		operationContext, err := services.WithOperationContext(
			context.Background(),
			services.OperationContext{
				Scope:  project.Scope(),
				Actor:  models.HumanActor(user.ID),
				Source: services.SourceProtocolHumanREST,
			},
		)
		if err != nil {
			t.Fatalf("bind ticket requester operation context: %v", err)
		}
		commandCompleted := completed[index]
		go func() {
			defer close(commandCompleted)
			preflight, commandErr := commandService.ResolveHumanProject(
				operationContext,
				string(project.Key),
				user.ID,
			)
			if commandErr == nil &&
				(preflight == nil ||
					preflight.Scope != project.Scope() ||
					preflight.Role != models.ProjectRoleRequester) {
				commandErr = errors.New(
					"Human preflight did not resolve ticket requester access",
				)
			}
			ticketNumber := ""
			if commandErr == nil {
				_, commandErr =
					commandService.RunHumanTicketCreateDatabaseCommand(
						operationContext,
						project.Scope(),
						user.ID,
						func(
							ctx context.Context,
							_ *gorm.DB,
							_ *services.ProjectAccess,
						) error {
							created, createErr :=
								nativeService.CreateNativeTicket(
									ctx,
									services.NativeTicketCreateInput{
										Request: models.TicketCreateRequest{
											Title: fmt.Sprintf(
												"Concurrent Human ticket %d",
												commandIndex,
											),
											Description: "Human command boundary",
											Type:        models.TicketTypeRequest,
											Priority: models.
												TicketPriorityNormal,
											Source: models.TicketSourceWeb,
										},
										Actor:          models.HumanActor(user.ID),
										SourceProtocol: "rest-human",
										TrustLevel: models.
											TicketTrustLevelUntrusted,
									},
								)
							if createErr != nil {
								return createErr
							}
							ticketNumber =
								created.Ticket.TicketNumber
							return nil
						},
					)
			}
			results <- ticketCommandResult{
				ticketNumber: ticketNumber,
				err:          commandErr,
			}
		}()
	}
	for index, backendPID := range backendPIDs {
		waitForPostgresBackendLock(
			t,
			db,
			backendPID,
			completed[index],
		)
	}
	if _, err := blocker.ExecContext(blockerContext, "COMMIT"); err != nil {
		t.Fatalf("release ticket command Project barrier: %v", err)
	}
	blockerCommitted = true

	gotNumbers := make(map[string]struct{}, len(commandServices))
	for range commandServices {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("ticket create database command error: %v", result.err)
			}
			gotNumbers[result.ticketNumber] = struct{}{}
		case <-time.After(5 * time.Second):
			t.Fatal(
				"ticket create database commands did not finish without deadlock",
			)
		}
	}
	for sequence := project.TicketSequence + 1; sequence <= project.TicketSequence+2; sequence++ {
		want := fmt.Sprintf("%s-%d", project.Key, sequence)
		if _, exists := gotNumbers[want]; !exists {
			t.Fatalf(
				"ticket numbers = %+v, missing %q",
				gotNumbers,
				want,
			)
		}
	}
	var persisted models.Project
	if err := db.First(&persisted, project.ID).Error; err != nil {
		t.Fatalf("reload ticket command Project: %v", err)
	}
	if persisted.TicketSequence != project.TicketSequence+2 {
		t.Fatalf(
			"ticket sequence = %d, want %d",
			persisted.TicketSequence,
			project.TicketSequence+2,
		)
	}
	assertPostgresTicketCreateDurability(
		t,
		db,
		project,
		models.ActorTypeHuman,
		models.HumanActor(user.ID).ID,
		2,
	)
}

func TestPostgresMachineProtocolTicketCreateCommandsCannotDeadlock(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		fixture string
		source  services.SourceProtocol
	}{
		{
			name:    "Agent_REST",
			fixture: "tc_ar",
			source:  services.SourceProtocolAgentREST,
		},
		{name: "MCP", fixture: "tc_m", source: services.SourceProtocolMCP},
		{name: "A2A", fixture: "tc_a", source: services.SourceProtocolA2A},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db, blocker, project, suffix :=
				openPostgresAuthorizationBarrierFixture(
					t,
					testCase.fixture,
				)
			bootstrapPostgresTicketConfiguration(t, db)
			fixture := seedPostgresMachineAuthorizationFixture(
				t,
				db,
				project,
				"create-"+strings.ToLower(testCase.name)+"-"+suffix,
			)
			_, commandDatabases, backendPIDs :=
				openPostgresAuthorizationCommandDatabases(t, db, 2)
			projectServices := make([]*services.ProjectService, 2)
			nativeServices := make([]*services.AgentNativeService, 2)
			for index := range projectServices {
				projectService, err := services.NewProjectService(
					commandDatabases[index],
				)
				if err != nil {
					t.Fatalf(
						"create machine Project service %d: %v",
						index,
						err,
					)
				}
				projectServices[index] = projectService
				ledger, err := services.NewAuditLedgerService(
					commandDatabases[index],
				)
				if err != nil {
					t.Fatalf(
						"create machine Audit service %d: %v",
						index,
						err,
					)
				}
				nativeServices[index] =
					services.NewAgentNativeService(
						commandDatabases[index],
						services.AgentNativeOptions{
							AuditLedger: ledger,
						},
					)
			}

			blockerContext, blockerCancel := context.WithTimeout(
				context.Background(),
				10*time.Second,
			)
			defer blockerCancel()
			if _, err := blocker.ExecContext(
				blockerContext,
				"BEGIN",
			); err != nil {
				t.Fatalf("begin machine ticket Project barrier: %v", err)
			}
			blockerCommitted := false
			defer func() {
				if !blockerCommitted {
					_, _ = blocker.ExecContext(
						context.Background(),
						"ROLLBACK",
					)
				}
			}()
			var lockedProjectID uint
			if err := blocker.QueryRowContext(
				blockerContext,
				`SELECT id
				 FROM projects
				 WHERE id = $1
				 FOR UPDATE`,
				project.ID,
			).Scan(&lockedProjectID); err != nil {
				t.Fatalf("lock machine ticket Project: %v", err)
			}

			type machineTicketResult struct {
				ticketNumber string
				err          error
			}
			completed := []chan struct{}{
				make(chan struct{}),
				make(chan struct{}),
			}
			results := make(chan machineTicketResult, 2)
			for index := range projectServices {
				projectService := projectServices[index]
				nativeService := nativeServices[index]
				commandDB := commandDatabases[index]
				commandIndex := index
				commandCompleted := completed[index]
				operationContext :=
					fixture.operationContextForSource(
						t,
						testCase.source,
					)
				go func() {
					defer close(commandCompleted)
					preflight, commandErr :=
						projectService.ResolvePrincipalProject(
							operationContext,
							string(project.Key),
							fixture.principal.ID,
							models.ScopeTicketsCreate,
						)
					if commandErr == nil &&
						(preflight == nil ||
							preflight.Scope != project.Scope() ||
							preflight.Role != models.ProjectRoleAgent) {
						commandErr = errors.New(
							"machine preflight did not resolve Agent access",
						)
					}
					ticketNumber := ""
					if commandErr == nil {
						_, commandErr =
							services.RunTicketCreateDatabaseCommand(
								operationContext,
								commandDB,
								nativeService,
								func(
									ctx context.Context,
									_ *gorm.DB,
									_ *services.ProjectAccess,
								) error {
									created, createErr :=
										nativeService.CreateNativeTicket(
											ctx,
											services.NativeTicketCreateInput{
												Request: models.TicketCreateRequest{
													Title: fmt.Sprintf(
														"Concurrent %s ticket %d",
														testCase.name,
														commandIndex,
													),
													Description: "Machine command boundary",
													Type: models.
														TicketTypeRequest,
													Priority: models.
														TicketPriorityNormal,
													Source: models.
														TicketSourceAgent,
												},
												Actor: models.
													ServicePrincipalActor(
														fixture.principal.ID,
													),
												CredentialID: fixture.
													credential.ID,
												SourceProtocol: string(
													testCase.source,
												),
												RequestDigest: fmt.Sprintf(
													"%s-%d",
													testCase.name,
													commandIndex,
												),
												TrustLevel: models.
													TicketTrustLevelUntrusted,
											},
										)
									if createErr != nil {
										return createErr
									}
									ticketNumber =
										created.Ticket.TicketNumber
									return nil
								},
							)
					}
					results <- machineTicketResult{
						ticketNumber: ticketNumber,
						err:          commandErr,
					}
				}()
			}
			for index, backendPID := range backendPIDs {
				waitForPostgresBackendLock(
					t,
					db,
					backendPID,
					completed[index],
				)
			}
			if _, err := blocker.ExecContext(
				blockerContext,
				"COMMIT",
			); err != nil {
				t.Fatalf("release machine ticket Project barrier: %v", err)
			}
			blockerCommitted = true

			gotNumbers := make(map[string]struct{}, 2)
			for range 2 {
				select {
				case result := <-results:
					if result.err != nil {
						t.Fatalf(
							"%s ticket command error: %v",
							testCase.name,
							result.err,
						)
					}
					gotNumbers[result.ticketNumber] = struct{}{}
				case <-time.After(5 * time.Second):
					t.Fatalf(
						"%s ticket commands did not finish",
						testCase.name,
					)
				}
			}
			for sequence := project.TicketSequence + 1; sequence <=
				project.TicketSequence+2; sequence++ {
				want := fmt.Sprintf("%s-%d", project.Key, sequence)
				if _, exists := gotNumbers[want]; !exists {
					t.Fatalf(
						"%s ticket numbers = %+v, missing %q",
						testCase.name,
						gotNumbers,
						want,
					)
				}
			}
			assertPostgresTicketCreateDurability(
				t,
				db,
				project,
				models.ActorTypeServicePrincipal,
				fixture.principal.ID,
				2,
			)
		})
	}
}

func TestPostgresLeaseConsumingCommandWaitsForReleaseAndThenRejects(
	t *testing.T,
) {
	db, _, project, suffix := openPostgresAuthorizationBarrierFixture(t, "lr")
	bootstrapPostgresTicketConfiguration(t, db)
	user, ticket, lease := seedPostgresTicketLeaseFixture(
		t,
		db,
		project,
		suffix,
		time.Now().UTC().Add(time.Hour),
	)
	_, commandDatabases, backendPIDs :=
		openPostgresAuthorizationCommandDatabases(t, db, 2)
	releaseService := services.NewAgentNativeService(commandDatabases[0])
	commandService := services.NewAgentNativeService(commandDatabases[1])
	ctx := postgresHumanLeaseOperationContext(t, project, user.ID)

	releaseHasBothLocks := make(chan struct{})
	continueRelease := make(chan struct{})
	var signalOnce sync.Once
	const callbackName = "test:pause_release_after_lease_lock"
	if err := commandDatabases[0].Callback().Query().
		After("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement == nil ||
				tx.Statement.Table != (models.TicketLease{}).TableName() {
				return
			}
			if _, locked := tx.Statement.Clauses["FOR"]; !locked {
				return
			}
			signalOnce.Do(func() {
				close(releaseHasBothLocks)
				<-continueRelease
			})
		}); err != nil {
		t.Fatalf("register release Lease lock barrier: %v", err)
	}
	t.Cleanup(func() {
		_ = commandDatabases[0].Callback().Query().Remove(callbackName)
	})

	type releaseResult struct {
		result *services.TicketLeaseCommandResult
		err    error
	}
	releaseFinished := make(chan struct{})
	releaseResults := make(chan releaseResult, 1)
	go func() {
		defer close(releaseFinished)
		result, err := releaseService.ReleaseTicketLeaseCommand(
			ctx,
			services.ReleaseTicketLeaseCommandInput{
				LeaseID: lease.ID,
				Actor:   models.HumanActor(user.ID),
				Reason:  "concurrent release",
			},
		)
		releaseResults <- releaseResult{result: result, err: err}
	}()
	select {
	case <-releaseHasBothLocks:
	case <-time.After(5 * time.Second):
		close(continueRelease)
		t.Fatal("release command did not lock Ticket then Lease")
	}

	commandFinished := make(chan struct{})
	commandResults := make(chan error, 1)
	go func() {
		defer close(commandFinished)
		_, err := commandService.UpdateTicketVersion(
			ctx,
			services.VersionedTicketUpdateInput{
				TicketID:        ticket.ID,
				ExpectedVersion: ticket.Version,
				LeaseID:         lease.ID,
				Actor:           models.HumanActor(user.ID),
				Changes:         map[string]any{"title": "must not commit"},
			},
		)
		commandResults <- err
	}()
	waitForPostgresBackendLock(
		t,
		db,
		backendPIDs[1],
		commandFinished,
	)
	close(continueRelease)

	select {
	case result := <-releaseResults:
		if result.err != nil {
			t.Fatalf("release Ticket Lease command: %v", result.err)
		}
		if result.result == nil ||
			result.result.Lease == nil ||
			result.result.Lease.ReleasedAt == nil {
			t.Fatalf("release command result is incomplete: %+v", result.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("release Ticket Lease command did not finish")
	}
	select {
	case err := <-commandResults:
		if !errors.Is(err, services.ErrLeaseExpired) {
			t.Fatalf(
				"lease-consuming command after committed release error = %v, want ErrLeaseExpired",
				err,
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lease-consuming command did not finish after release")
	}

	var persistedTicket models.Ticket
	if err := db.First(&persistedTicket, ticket.ID).Error; err != nil {
		t.Fatalf("reload Ticket after concurrent Lease release: %v", err)
	}
	if persistedTicket.Version != ticket.Version ||
		persistedTicket.Title != ticket.Title {
		t.Fatalf(
			"released Lease allowed Ticket mutation: before=%+v after=%+v",
			ticket,
			persistedTicket,
		)
	}
	var updateEvents int64
	if err := db.Model(&models.DomainEvent{}).
		Where(
			"organization_id = ? AND project_id = ? AND type = ? AND subject = ?",
			project.OrganizationID,
			project.ID,
			eventcontract.TicketUpdatedEventType,
			fmt.Sprintf("ticket/%d", ticket.ID),
		).
		Count(&updateEvents).Error; err != nil {
		t.Fatalf("count rejected Ticket update events: %v", err)
	}
	if updateEvents != 0 {
		t.Fatalf("rejected Ticket command persisted %d update events", updateEvents)
	}
}

func TestPostgresTicketLeaseHeartbeatSamplesExpiryAfterFinalLock(
	t *testing.T,
) {
	db, blocker, project, suffix :=
		openPostgresAuthorizationBarrierFixture(t, "lh")
	bootstrapPostgresTicketConfiguration(t, db)
	expiresAt := time.Now().UTC().Add(750 * time.Millisecond)
	user, ticket, lease := seedPostgresTicketLeaseFixture(
		t,
		db,
		project,
		suffix,
		expiresAt,
	)
	_, commandDatabases, backendPIDs :=
		openPostgresAuthorizationCommandDatabases(t, db, 1)
	service := services.NewAgentNativeService(commandDatabases[0])
	ctx := postgresHumanLeaseOperationContext(t, project, user.ID)

	blockerContext, blockerCancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer blockerCancel()
	if _, err := blocker.ExecContext(blockerContext, "BEGIN"); err != nil {
		t.Fatalf("begin Ticket Lease expiry barrier: %v", err)
	}
	blockerCommitted := false
	defer func() {
		if !blockerCommitted {
			_, _ = blocker.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	var lockedTicketID uint
	if err := blocker.QueryRowContext(
		blockerContext,
		`SELECT id
		 FROM tickets
		 WHERE id = $1
		 FOR UPDATE`,
		ticket.ID,
	).Scan(&lockedTicketID); err != nil {
		t.Fatalf("lock Ticket before Lease expiry barrier: %v", err)
	}
	var lockedLeaseID string
	if err := blocker.QueryRowContext(
		blockerContext,
		`SELECT id
		 FROM ticket_leases
		 WHERE id = $1
		 FOR UPDATE`,
		lease.ID,
	).Scan(&lockedLeaseID); err != nil {
		t.Fatalf("lock final Ticket Lease expiry row: %v", err)
	}

	type heartbeatResult struct {
		result *services.TicketLeaseCommandResult
		err    error
	}
	completed := make(chan struct{})
	results := make(chan heartbeatResult, 1)
	go func() {
		defer close(completed)
		result, err := service.HeartbeatTicketLeaseCommand(
			ctx,
			services.HeartbeatTicketLeaseCommandInput{
				LeaseID:         lease.ID,
				Actor:           models.HumanActor(user.ID),
				ExpectedVersion: ticket.Version,
				TTL:             time.Minute,
			},
		)
		results <- heartbeatResult{result: result, err: err}
	}()
	waitForPostgresBackendLock(t, db, backendPIDs[0], completed)
	if remaining := time.Until(expiresAt.Add(150 * time.Millisecond)); remaining > 0 {
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		<-timer.C
	}
	if _, err := blocker.ExecContext(blockerContext, "COMMIT"); err != nil {
		t.Fatalf("release expired Ticket Lease barrier: %v", err)
	}
	blockerCommitted = true

	select {
	case result := <-results:
		if !errors.Is(result.err, services.ErrLeaseExpired) {
			t.Fatalf(
				"heartbeat after final-lock expiry error = %v, want ErrLeaseExpired",
				result.err,
			)
		}
		if result.result != nil {
			t.Fatalf("expired heartbeat returned a result: %+v", result.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("expired Ticket Lease heartbeat did not finish")
	}
	var persisted models.TicketLease
	if err := db.First(&persisted, "id = ?", lease.ID).Error; err != nil {
		t.Fatalf("reload expired Ticket Lease: %v", err)
	}
	if !persisted.ExpiresAt.Equal(lease.ExpiresAt) ||
		!persisted.LastHeartbeatAt.Equal(lease.LastHeartbeatAt) {
		t.Fatalf(
			"expired heartbeat revived Lease: before=%+v after=%+v",
			lease,
			persisted,
		)
	}
	var heartbeatEvents int64
	if err := db.Model(&models.DomainEvent{}).
		Where(
			"organization_id = ? AND project_id = ? AND type = ? AND subject = ?",
			project.OrganizationID,
			project.ID,
			"io.chronodesk.ticket.lease.heartbeat.v1",
			fmt.Sprintf("ticket/%d", ticket.ID),
		).
		Count(&heartbeatEvents).Error; err != nil {
		t.Fatalf("count rejected Lease heartbeat events: %v", err)
	}
	if heartbeatEvents != 0 {
		t.Fatalf("expired heartbeat persisted %d domain events", heartbeatEvents)
	}
}

func seedPostgresTicketLeaseFixture(
	t *testing.T,
	db *gorm.DB,
	project models.Project,
	suffix string,
	expiresAt time.Time,
) (models.User, models.Ticket, models.TicketLease) {
	t.Helper()
	user := models.User{
		Username:     "lease-" + suffix,
		Email:        "lease-" + suffix + "@example.test",
		PasswordHash: "test-only-password-hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create Ticket Lease Human: %v", err)
	}
	if err := db.Create(&models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    user.ID,
		Role:      models.ProjectRoleManager,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatalf("create Ticket Lease Human membership: %v", err)
	}
	var queue models.Queue
	if err := db.Where(
		"project_id = ? AND is_default = ?",
		project.ID,
		true,
	).First(&queue).Error; err != nil {
		t.Fatalf("load Ticket Lease default Queue: %v", err)
	}
	var requestType models.RequestTypeVersion
	if err := db.Where(
		"project_id = ? AND key = ? AND status = ?",
		project.ID,
		"request",
		models.ConfigurationStatusPublished,
	).First(&requestType).Error; err != nil {
		t.Fatalf("load Ticket Lease Request Type: %v", err)
	}
	var workflow models.WorkflowVersion
	if err := db.Where(
		"project_id = ? AND key = ? AND status = ?",
		project.ID,
		"default",
		models.ConfigurationStatusPublished,
	).First(&workflow).Error; err != nil {
		t.Fatalf("load Ticket Lease Workflow: %v", err)
	}
	ticket := models.Ticket{
		OrganizationID:       project.OrganizationID,
		ProjectID:            project.ID,
		QueueID:              queue.ID,
		RequestTypeVersionID: requestType.ID,
		WorkflowVersionID:    workflow.ID,
		TicketNumber:         "LEASE-" + suffix,
		Title:                "Lease concurrency barrier",
		Description:          "Lease state must fence concurrent commands",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityNormal,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceWeb,
		Version:              1,
		TrustLevel:           models.TicketTrustLevelUntrusted,
		CreatedByID:          &user.ID,
		CreatedByActorType:   models.ActorTypeHuman,
		CreatedByActorID:     models.HumanActor(user.ID).ID,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("create Ticket Lease Ticket: %v", err)
	}
	heartbeatAt := time.Now().UTC()
	lease := models.TicketLease{
		ID:              uuid.NewString(),
		OrganizationID:  project.OrganizationID,
		ProjectID:       project.ID,
		TicketID:        ticket.ID,
		HolderActorType: models.ActorTypeHuman,
		HolderActorID:   models.HumanActor(user.ID).ID,
		TicketVersion:   ticket.Version,
		ExpiresAt:       expiresAt,
		LastHeartbeatAt: heartbeatAt,
	}
	if err := db.Create(&lease).Error; err != nil {
		t.Fatalf("create Ticket Lease: %v", err)
	}
	return user, ticket, lease
}

func postgresHumanLeaseOperationContext(
	t *testing.T,
	project models.Project,
	userID uint,
) context.Context {
	t.Helper()
	ctx, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:  project.Scope(),
			Actor:  models.HumanActor(userID),
			Source: services.SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatalf("bind Ticket Lease Human operation context: %v", err)
	}
	return ctx
}

func bootstrapPostgresTicketConfiguration(t *testing.T, db *gorm.DB) {
	t.Helper()
	service, err := services.NewProjectConfigurationService(db)
	if err != nil {
		t.Fatalf("create PostgreSQL Project configuration service: %v", err)
	}
	if err := service.BootstrapActiveProjects(context.Background()); err != nil {
		t.Fatalf("bootstrap PostgreSQL ticket configuration: %v", err)
	}
}

func assertPostgresTicketCreateDurability(
	t *testing.T,
	db *gorm.DB,
	project models.Project,
	actorType models.ActorType,
	actorID string,
	want int,
) {
	t.Helper()
	var tickets int64
	if err := db.Model(&models.Ticket{}).
		Where(
			"organization_id = ? AND project_id = ? AND created_by_actor_type = ? AND created_by_actor_id = ?",
			project.OrganizationID,
			project.ID,
			actorType,
			actorID,
		).
		Count(&tickets).Error; err != nil {
		t.Fatalf("count durable created Tickets: %v", err)
	}
	if tickets != int64(want) {
		t.Fatalf("durable created Tickets = %d, want %d", tickets, want)
	}

	var events []models.DomainEvent
	if err := db.Where(
		"organization_id = ? AND project_id = ? AND type = ? AND actor_type = ? AND actor_id = ?",
		project.OrganizationID,
		project.ID,
		eventcontract.TicketCreatedEventType,
		actorType,
		actorID,
	).Find(&events).Error; err != nil {
		t.Fatalf("load durable Ticket Created events: %v", err)
	}
	if len(events) != want {
		t.Fatalf("durable Ticket Created events = %d, want %d", len(events), want)
	}
	eventIDs := make([]string, 0, len(events))
	for _, event := range events {
		eventIDs = append(eventIDs, event.ID)
	}
	var outbox int64
	if err := db.Model(&models.OutboxDelivery{}).
		Where("event_id IN ?", eventIDs).
		Count(&outbox).Error; err != nil {
		t.Fatalf("count durable Ticket Created Outbox rows: %v", err)
	}
	if outbox < int64(want) {
		t.Fatalf("durable Ticket Created Outbox rows = %d, want >= %d", outbox, want)
	}
	var audit int64
	if err := db.Model(&models.AuditLedgerEntry{}).
		Where(
			"domain_event_id IN ? AND event_type = ? AND actor_type = ? AND actor_id = ?",
			eventIDs,
			eventcontract.TicketCreatedEventType,
			actorType,
			actorID,
		).
		Count(&audit).Error; err != nil {
		t.Fatalf("count durable Ticket Created Audit rows: %v", err)
	}
	if audit != int64(want) {
		t.Fatalf("durable Ticket Created Audit rows = %d, want %d", audit, want)
	}
}

func openPostgresAuthorizationCommandDatabases(
	t *testing.T,
	db *gorm.DB,
	count int,
) ([]*sql.Conn, []*gorm.DB, []int) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open PostgreSQL authorization pool: %v", err)
	}
	connections := make([]*sql.Conn, count)
	commandDatabases := make([]*gorm.DB, count)
	backendPIDs := make([]int, count)
	t.Cleanup(func() {
		for index, connection := range connections {
			if connection == nil {
				continue
			}
			if closeErr := connection.Close(); closeErr != nil {
				t.Errorf(
					"close authorization command connection %d: %v",
					index,
					closeErr,
				)
			}
		}
	})
	for index := range count {
		connection, connectionErr := sqlDB.Conn(context.Background())
		if connectionErr != nil {
			t.Fatalf(
				"reserve authorization command connection %d: %v",
				index,
				connectionErr,
			)
		}
		connections[index] = connection
		commandDB, openErr := gorm.Open(
			postgres.New(postgres.Config{Conn: connection}),
			&gorm.Config{DisableAutomaticPing: true, TranslateError: true},
		)
		if openErr != nil {
			t.Fatalf(
				"bind authorization command GORM connection %d: %v",
				index,
				openErr,
			)
		}
		commandDatabases[index] = commandDB
		if pidErr := connection.QueryRowContext(
			context.Background(),
			"SELECT pg_backend_pid()",
		).Scan(&backendPIDs[index]); pidErr != nil {
			t.Fatalf(
				"read authorization command backend PID %d: %v",
				index,
				pidErr,
			)
		}
	}
	return connections, commandDatabases, backendPIDs
}

func openPostgresAuthorizationBarrierFixture(
	t *testing.T,
	fixture string,
) (*gorm.DB, *sql.Conn, models.Project, string) {
	t.Helper()
	db, _, suffix := openPostgresMembershipReleaseTestDB(t, fixture)
	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("run PostgreSQL authorization barrier migrations: %v", err)
	}

	var project models.Project
	if err := db.Where("key = ?", DefaultProjectKey).
		Take(&project).Error; err != nil {
		t.Fatalf("load default project scope: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open PostgreSQL authorization barrier pool: %v", err)
	}
	sqlDB.SetMaxOpenConns(4)
	revocationConnection, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatalf("reserve independent PostgreSQL revocation connection: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := revocationConnection.Close(); closeErr != nil {
			t.Errorf(
				"close independent PostgreSQL revocation connection: %v",
				closeErr,
			)
		}
	})
	if err := revocationConnection.PingContext(context.Background()); err != nil {
		t.Fatalf("ping independent PostgreSQL revocation connection: %v", err)
	}

	return db, revocationConnection, project, suffix
}

type postgresAuthorizationRevocationResult struct {
	affected int64
	err      error
}

func beginPostgresAuthorizationRevocation(
	t *testing.T,
	observer *gorm.DB,
	connection *sql.Conn,
	statement string,
	arguments ...any,
) <-chan postgresAuthorizationRevocationResult {
	t.Helper()
	var backendPID int
	if err := connection.QueryRowContext(
		context.Background(),
		"SELECT pg_backend_pid()",
	).Scan(&backendPID); err != nil {
		t.Fatalf("read revocation connection backend PID: %v", err)
	}
	started := make(chan struct{})
	completed := make(chan struct{})
	result := make(chan postgresAuthorizationRevocationResult, 1)
	go func() {
		defer close(completed)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		close(started)
		execResult, err := connection.ExecContext(
			ctx,
			statement,
			arguments...,
		)
		if err != nil {
			result <- postgresAuthorizationRevocationResult{err: err}
			return
		}
		affected, err := execResult.RowsAffected()
		result <- postgresAuthorizationRevocationResult{
			affected: affected,
			err:      err,
		}
	}()
	<-started
	waitForPostgresBackendLock(t, observer, backendPID, completed)
	return result
}

func waitForPostgresBackendLock(
	t *testing.T,
	observer *gorm.DB,
	backendPID int,
	completed <-chan struct{},
) {
	t.Helper()
	deadline := time.Now().Add(postgresAuthorizationBarrierTimeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-completed:
			t.Fatalf(
				"PostgreSQL backend %d completed before reaching the lock barrier",
				backendPID,
			)
		default:
		}

		var activity struct {
			Sessions int64 `gorm:"column:sessions"`
			Waiting  bool  `gorm:"column:waiting"`
		}
		if err := observer.Raw(
			`SELECT
				COUNT(*) AS sessions,
				COALESCE(
					BOOL_OR(wait_event_type = 'Lock'),
					FALSE
				) AS waiting
			 FROM pg_stat_activity
			 WHERE pid = ?`,
			backendPID,
		).Scan(&activity).Error; err != nil {
			t.Fatalf(
				"inspect PostgreSQL backend %d lock wait: %v",
				backendPID,
				err,
			)
		}
		if activity.Sessions == 1 && activity.Waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"PostgreSQL backend %d did not reach a lock wait: %+v",
				backendPID,
				activity,
			)
		}
		<-ticker.C
	}
}

func assertPostgresAuthorizationRevocationSucceeds(
	t *testing.T,
	result <-chan postgresAuthorizationRevocationResult,
) {
	t.Helper()
	if result == nil {
		t.Fatal("authorization revocation barrier was not started")
	}
	select {
	case completed := <-result:
		if completed.err != nil {
			t.Fatalf(
				"authorization revocation did not succeed after transaction release: %v",
				completed.err,
			)
		}
		if completed.affected != 1 {
			t.Fatalf(
				"authorization revocation affected %d rows after transaction release, want 1",
				completed.affected,
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal(
			"authorization revocation remained blocked after revalidation transaction committed",
		)
	}
}
