package database

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

func TestPostgresPlatformProjectOperationsRevalidateActiveAdministratorAfterUserLockBarrier(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		fixtureName string
		run         func(
			context.Context,
			*services.ProjectService,
			models.Project,
			models.User,
			string,
		) (bool, error)
		assertProjectUnchanged func(
			*testing.T,
			*gorm.DB,
			models.Project,
			string,
		)
	}{
		{
			name:        "ListPlatformProjects",
			fixtureName: "pp_list",
			run: func(
				ctx context.Context,
				service *services.ProjectService,
				_ models.Project,
				administrator models.User,
				_ string,
			) (bool, error) {
				projects, err := service.ListPlatformProjects(
					ctx,
					administrator.ID,
				)
				return len(projects) != 0, err
			},
			assertProjectUnchanged: func(
				t *testing.T,
				db *gorm.DB,
				want models.Project,
				_ string,
			) {
				t.Helper()
				var got models.Project
				if err := db.First(&got, want.ID).Error; err != nil {
					t.Fatalf(
						"reload Project after rejected platform list: %v",
						err,
					)
				}
				if got.PublicID != want.PublicID ||
					got.OrganizationID != want.OrganizationID ||
					got.BusinessUnitID != want.BusinessUnitID ||
					got.Key != want.Key ||
					got.Name != want.Name ||
					got.Description != want.Description ||
					got.Status != want.Status ||
					got.TicketSequence != want.TicketSequence ||
					!got.UpdatedAt.Equal(want.UpdatedAt) {
					t.Fatalf(
						"rejected ListPlatformProjects mutated Project: before=%+v after=%+v",
						want,
						got,
					)
				}
			},
		},
		{
			name:        "CreateProject",
			fixtureName: "pp_create",
			run: func(
				ctx context.Context,
				service *services.ProjectService,
				project models.Project,
				administrator models.User,
				suffix string,
			) (bool, error) {
				created, err := service.CreateProject(
					ctx,
					services.CreateProjectInput{
						ActorUserID:             administrator.ID,
						BusinessUnitPublicID:    project.BusinessUnit.PublicID,
						Key:                     "P" + suffix,
						Name:                    "Rejected concurrent project",
						Description:             "must not persist",
						InitialAdministratorIDs: []uint{administrator.ID},
					},
				)
				return created != nil, err
			},
			assertProjectUnchanged: func(
				t *testing.T,
				db *gorm.DB,
				_ models.Project,
				suffix string,
			) {
				t.Helper()
				var createdProjects int64
				if err := db.Model(&models.Project{}).
					Where("key = ?", "P"+suffix).
					Count(&createdProjects).Error; err != nil {
					t.Fatalf("count rejected Project creation: %v", err)
				}
				if createdProjects != 0 {
					t.Fatalf(
						"rejected CreateProject persisted %d Project row(s)",
						createdProjects,
					)
				}
			},
		},
		{
			name:        "ArchiveProject",
			fixtureName: "pp_archive",
			run: func(
				ctx context.Context,
				service *services.ProjectService,
				project models.Project,
				administrator models.User,
				_ string,
			) (bool, error) {
				archived, err := service.ArchiveProject(
					ctx,
					project.PublicID,
					models.HumanActor(administrator.ID),
				)
				return archived != nil, err
			},
			assertProjectUnchanged: func(
				t *testing.T,
				db *gorm.DB,
				want models.Project,
				_ string,
			) {
				t.Helper()
				var got models.Project
				if err := db.First(&got, want.ID).Error; err != nil {
					t.Fatalf("reload rejected archive Project: %v", err)
				}
				if got.PublicID != want.PublicID ||
					got.OrganizationID != want.OrganizationID ||
					got.BusinessUnitID != want.BusinessUnitID ||
					got.Key != want.Key ||
					got.Name != want.Name ||
					got.Description != want.Description ||
					got.Status != want.Status ||
					got.TicketSequence != want.TicketSequence ||
					!got.UpdatedAt.Equal(want.UpdatedAt) {
					t.Fatalf(
						"rejected ArchiveProject mutated Project: before=%+v after=%+v",
						want,
						got,
					)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db, actorBlocker, project, suffix :=
				openPostgresAuthorizationBarrierFixture(
					t,
					testCase.fixtureName,
				)
			administrator := models.User{
				Username:     "platform-command-admin-" + suffix,
				Email:        "platform-command-admin-" + suffix + "@example.test",
				PasswordHash: "test-only-password-hash",
				PlatformRole: models.PlatformRolePlatformAdmin,
				Status:       models.UserStatusActive,
			}
			if err := db.Create(&administrator).Error; err != nil {
				t.Fatalf("create active platform administrator: %v", err)
			}
			if err := EnableProjectRLS(db); err != nil {
				t.Fatalf("enable FORCE RLS for platform Project command: %v", err)
			}
			if err := ValidateProjectRLSRuntime(db); err != nil {
				t.Fatalf(
					"validate FORCE RLS for platform Project command: %v",
					err,
				)
			}

			before := snapshotPlatformProjectAuthorizationSideEffects(
				t,
				db,
				project.OrganizationID,
			)
			_, commandDatabases, backendPIDs :=
				openPostgresAuthorizationCommandDatabases(t, db, 1)
			ledger, err := services.NewAuditLedgerService(
				commandDatabases[0],
			)
			if err != nil {
				t.Fatalf("create platform Project Audit service: %v", err)
			}
			native := services.NewAgentNativeService(
				commandDatabases[0],
				services.AgentNativeOptions{AuditLedger: ledger},
			)
			projectService, err := services.NewProjectService(
				commandDatabases[0],
				native,
			)
			if err != nil {
				t.Fatalf("create platform Project service: %v", err)
			}

			blockerContext, blockerCancel := context.WithTimeout(
				context.Background(),
				10*time.Second,
			)
			defer blockerCancel()
			if _, err := actorBlocker.ExecContext(
				blockerContext,
				"BEGIN",
			); err != nil {
				t.Fatalf("begin actor demotion barrier: %v", err)
			}
			blockerCommitted := false
			defer func() {
				if !blockerCommitted {
					_, _ = actorBlocker.ExecContext(
						context.Background(),
						"ROLLBACK",
					)
				}
			}()
			var lockedActorID uint
			if err := actorBlocker.QueryRowContext(
				blockerContext,
				`SELECT id
				 FROM users
				 WHERE id = $1
				 FOR UPDATE`,
				administrator.ID,
			).Scan(&lockedActorID); err != nil {
				t.Fatalf("lock platform Project command actor: %v", err)
			}
			if lockedActorID != administrator.ID {
				t.Fatalf(
					"locked actor = %d, want %d",
					lockedActorID,
					administrator.ID,
				)
			}
			if result, err := actorBlocker.ExecContext(
				blockerContext,
				`UPDATE users
				 SET platform_role = $2
				 WHERE id = $1`,
				administrator.ID,
				models.PlatformRoleMember,
			); err != nil {
				t.Fatalf("stage platform administrator demotion: %v", err)
			} else if affected, rowsErr := result.RowsAffected(); rowsErr != nil {
				t.Fatalf("read staged actor demotion result: %v", rowsErr)
			} else if affected != 1 {
				t.Fatalf(
					"staged actor demotion affected %d rows, want 1",
					affected,
				)
			}

			type commandResult struct {
				returnedResource bool
				err              error
			}
			commandCompleted := make(chan struct{})
			commandResults := make(chan commandResult, 1)
			commandContext, commandCancel := context.WithTimeout(
				context.Background(),
				10*time.Second,
			)
			defer commandCancel()
			go func() {
				defer close(commandCompleted)
				returnedResource, commandErr := testCase.run(
					commandContext,
					projectService,
					project,
					administrator,
					suffix,
				)
				commandResults <- commandResult{
					returnedResource: returnedResource,
					err:              commandErr,
				}
			}()

			waitForPostgresBackendLock(
				t,
				db,
				backendPIDs[0],
				commandCompleted,
			)
			if _, err := actorBlocker.ExecContext(
				blockerContext,
				"COMMIT",
			); err != nil {
				t.Fatalf("commit platform administrator demotion: %v", err)
			}
			blockerCommitted = true

			select {
			case result := <-commandResults:
				if !errors.Is(result.err, services.ErrProjectAccessDenied) {
					t.Fatalf(
						"%s after committed actor demotion error = %v, want ErrProjectAccessDenied",
						testCase.name,
						result.err,
					)
				}
				if result.returnedResource {
					t.Fatalf(
						"%s returned a resource after authorization denial",
						testCase.name,
					)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf(
					"%s did not finish after actor lock release",
					testCase.name,
				)
			}

			var persistedActor models.User
			if err := db.First(
				&persistedActor,
				administrator.ID,
			).Error; err != nil {
				t.Fatalf("reload demoted platform administrator: %v", err)
			}
			if persistedActor.PlatformRole != models.PlatformRoleMember ||
				persistedActor.Status != models.UserStatusActive {
				t.Fatalf(
					"concurrent actor demotion was not retained: %+v",
					persistedActor,
				)
			}
			testCase.assertProjectUnchanged(t, db, project, suffix)
			after := snapshotPlatformProjectAuthorizationSideEffects(
				t,
				db,
				project.OrganizationID,
			)
			if after != before {
				t.Fatalf(
					"%s denial persisted Project/event/outbox/audit side effects: before=%+v after=%+v",
					testCase.name,
					before,
					after,
				)
			}
		})
	}
}

type platformProjectAuthorizationSideEffects struct {
	Projects           int64
	DomainEvents       int64
	OutboxDeliveries   int64
	AuditLedgerEntries int64
	AuditChainHeads    int64
	AdminAuditLogs     int64
}

func snapshotPlatformProjectAuthorizationSideEffects(
	t *testing.T,
	db *gorm.DB,
	organizationID uint,
) platformProjectAuthorizationSideEffects {
	t.Helper()
	var projectIDs []uint
	if err := db.Model(&models.Project{}).
		Where("organization_id = ?", organizationID).
		Order("id ASC").
		Pluck("id", &projectIDs).Error; err != nil {
		t.Fatalf("load Project IDs for side-effect snapshot: %v", err)
	}
	snapshot := platformProjectAuthorizationSideEffects{
		Projects: int64(len(projectIDs)),
	}
	if err := db.Model(&models.AdminAuditLog{}).
		Count(&snapshot.AdminAuditLogs).Error; err != nil {
		t.Fatalf("count admin audit logs: %v", err)
	}
	err := scopeddb.WithAuthorizedProjectScopeTransaction(
		context.Background(),
		db,
		organizationID,
		projectIDs,
		func(ctx context.Context) error {
			scoped := db.WithContext(ctx)
			for _, count := range []struct {
				name  string
				model any
				value *int64
			}{
				{
					name:  "Domain Events",
					model: &models.DomainEvent{},
					value: &snapshot.DomainEvents,
				},
				{
					name:  "Outbox Deliveries",
					model: &models.OutboxDelivery{},
					value: &snapshot.OutboxDeliveries,
				},
				{
					name:  "Audit Ledger Entries",
					model: &models.AuditLedgerEntry{},
					value: &snapshot.AuditLedgerEntries,
				},
				{
					name:  "Audit Chain Heads",
					model: &models.AuditChainHead{},
					value: &snapshot.AuditChainHeads,
				},
			} {
				if countErr := scoped.Model(count.model).
					Count(count.value).Error; countErr != nil {
					return fmt.Errorf(
						"count %s for side-effect snapshot: %w",
						count.name,
						countErr,
					)
				}
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("capture FORCE RLS side-effect snapshot: %v", err)
	}
	return snapshot
}
