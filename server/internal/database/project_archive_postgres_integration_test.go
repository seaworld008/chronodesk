package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostgresProjectArchiveRevocationOutboxRunsAfterForceRLS(
	t *testing.T,
) {
	db, roleName, suffix := openPostgresMembershipReleaseTestDB(
		t,
		"pr_archive",
	)
	assertPostgresReleaseTestRole(t, db, roleName)
	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("migrate project archive fixture: %v", err)
	}
	project := createArchivablePostgresProject(t, db)
	administrator := models.User{
		Username:     "archive-admin-" + suffix,
		Email:        "archive-admin-" + suffix + "@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&administrator).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnableProjectRLS(db); err != nil {
		t.Fatalf("enable FORCE RLS for archive fixture: %v", err)
	}
	ledger, err := services.NewAuditLedgerService(db)
	if err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(
		db,
		services.AgentNativeOptions{AuditLedger: ledger},
	)
	projectService, err := services.NewProjectService(db, native)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := projectService.ArchiveProject(
		context.Background(),
		project.PublicID,
		models.HumanActor(administrator.ID),
	)
	if err != nil {
		t.Fatalf("archive project under FORCE RLS: %v", err)
	}
	if archived.Status != models.ProjectStatusArchived {
		t.Fatalf("archived project = %+v", archived)
	}

	var deliveredType string
	batch, err := native.ProcessOutboxBatch(
		context.Background(),
		"postgres-project-archive-worker",
		10,
		services.OutboxDeliverFunc(func(
			_ context.Context,
			_ *models.OutboxDelivery,
			event services.CloudEventEnvelope,
		) error {
			deliveredType = event.Type
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("deliver archived revocation under FORCE RLS: %v", err)
	}
	if batch.Claimed != 1 ||
		batch.Delivered != 1 ||
		batch.Failed != 0 ||
		deliveredType != services.ProjectAccessRevokedEventType {
		t.Fatalf(
			"archived revocation batch=%+v type=%q",
			batch,
			deliveredType,
		)
	}
	var event models.DomainEvent
	var delivery models.OutboxDelivery
	var audit models.AuditLedgerEntry
	if err := WithProjectScopeTransaction(
		context.Background(),
		db,
		project.Scope(),
		func(scoped *gorm.DB) error {
			if err := scoped.Where(
				"type = ? AND project_id = ?",
				services.ProjectAccessRevokedEventType,
				project.ID,
			).Take(&event).Error; err != nil {
				return err
			}
			if err := scoped.Where("event_id = ?", event.ID).
				Take(&delivery).Error; err != nil {
				return err
			}
			return scoped.Where("domain_event_id = ?", event.ID).
				Take(&audit).Error
		},
	); err != nil {
		t.Fatal(err)
	}
	if delivery.Status != models.OutboxDeliverySucceeded {
		t.Fatalf("archived revocation delivery = %+v", delivery)
	}
	if audit.EventType != services.ProjectAccessRevokedEventType ||
		audit.Actor() != models.HumanActor(administrator.ID) {
		t.Fatalf("archived revocation audit = %+v", audit)
	}
}

func TestPostgresOutboxClaimWaitsForArchiveAndAppliesArchivedAllowlist(
	t *testing.T,
) {
	ownerDB, roleName, suffix := openPostgresMembershipReleaseTestDB(
		t,
		"pr_archive_claim",
	)
	assertPostgresReleaseTestRole(t, ownerDB, roleName)
	if err := RunMigrations(
		ownerDB,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("migrate archived Outbox claim fixture: %v", err)
	}
	project := createArchivablePostgresProject(t, ownerDB)
	administrator := models.User{
		Username:     "archive-claim-admin-" + suffix,
		Email:        "archive-claim-admin-" + suffix + "@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := ownerDB.Create(&administrator).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnableProjectRLS(ownerDB); err != nil {
		t.Fatalf("enable FORCE RLS for archived Outbox claim: %v", err)
	}

	eventWriter := services.NewAgentNativeService(ownerDB)
	eventActor := models.HumanActor(administrator.ID)
	eventContext, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:         project.Scope(),
			Actor:         eventActor,
			Source:        services.SourceProtocolHumanREST,
			TraceID:       "postgres-archive-claim-fixture",
			CorrelationID: "postgres-archive-claim-fixture",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var genericEvent *models.DomainEvent
	var revocationEvent *models.DomainEvent
	if err := WithProjectScopeTransaction(
		eventContext,
		ownerDB,
		project.Scope(),
		func(scoped *gorm.DB) error {
			targets := []services.OutboxTarget{{
				Type:        "event_stream",
				ID:          "default",
				MaxAttempts: 3,
			}}
			genericEvent, err = eventWriter.AppendDomainEventTx(
				eventContext,
				scoped,
				services.DomainEventInput{
					Type:    "io.chronodesk.ticket.pre-archive-canary.v1",
					Subject: "ticket/canary",
					Actor:   eventActor,
					Scope:   project.Scope(),
					Data:    map[string]any{"canary": true},
				},
				targets,
			)
			if err != nil {
				return err
			}
			revocationEvent, err = eventWriter.AppendDomainEventTx(
				eventContext,
				scoped,
				services.DomainEventInput{
					Type:    services.ProjectAccessRevokedEventType,
					Subject: "project/" + project.PublicID,
					Actor:   eventActor,
					Scope:   project.Scope(),
					Data:    map[string]any{"project_id": project.PublicID},
				},
				targets,
			)
			return err
		},
	); err != nil {
		t.Fatalf("seed archived Outbox claim events under FORCE RLS: %v", err)
	}

	runtimeDB, observerDB, runtimeRole := openPostgresArchiveRuntimeDB(
		t,
		ownerDB,
		suffix,
	)
	assertPostgresArchiveRuntimeRole(
		t,
		runtimeDB,
		runtimeRole,
		roleName,
	)
	var unscopedDeliveries int64
	if err := runtimeDB.Model(&models.OutboxDelivery{}).
		Count(&unscopedDeliveries).Error; err != nil {
		t.Fatalf("count unscoped runtime Outbox deliveries: %v", err)
	}
	if unscopedDeliveries != 0 {
		t.Fatalf(
			"FORCE RLS exposed %d Outbox deliveries without scope",
			unscopedDeliveries,
		)
	}

	connections, commandDBs, backendPIDs :=
		openPostgresAuthorizationCommandDatabases(t, runtimeDB, 2)
	archiveConnection := connections[0]
	claimDB := commandDBs[1]
	archivePID := backendPIDs[0]
	claimPID := backendPIDs[1]
	archiveContext, archiveCancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer archiveCancel()
	if _, err := archiveConnection.ExecContext(
		archiveContext,
		"BEGIN",
	); err != nil {
		t.Fatalf("begin project archive barrier transaction: %v", err)
	}
	archiveCommitted := false
	defer func() {
		if !archiveCommitted {
			_, _ = archiveConnection.ExecContext(
				context.Background(),
				"ROLLBACK",
			)
		}
	}()
	if _, err := archiveConnection.ExecContext(
		archiveContext,
		`SELECT
			set_config('chronodesk.organization_id', $1, true),
			set_config('chronodesk.project_id', $2, true),
			set_config('chronodesk.project_ids', '', true)`,
		fmt.Sprint(project.OrganizationID),
		fmt.Sprint(project.ID),
	); err != nil {
		t.Fatalf("set archive barrier project scope: %v", err)
	}
	updateResult, err := archiveConnection.ExecContext(
		archiveContext,
		`UPDATE projects
		 SET status = $1, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $2
		   AND organization_id = $3
		   AND status = $4`,
		models.ProjectStatusArchived,
		project.ID,
		project.OrganizationID,
		models.ProjectStatusActive,
	)
	if err != nil {
		t.Fatalf("hold uncommitted project archive: %v", err)
	}
	affected, err := updateResult.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf(
			"hold uncommitted project archive: rows=%d err=%v",
			affected,
			err,
		)
	}

	const workerID = "postgres-archive-claim-barrier-worker"
	workerActor := models.SystemActor("outbox-delivery-worker")
	claimBaseContext, claimCancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer claimCancel()
	claimContext, err := services.EnsureSystemProjectOperationContext(
		claimBaseContext,
		project.Scope(),
		workerActor,
		"postgres-archive-claim-barrier",
		"postgres-archive-claim-barrier",
	)
	if err != nil {
		t.Fatal(err)
	}
	type claimResult struct {
		deliveries []*models.OutboxDelivery
		err        error
	}
	claimCompleted := make(chan struct{})
	claimResults := make(chan claimResult, 1)
	claimService := services.NewAgentNativeService(claimDB)
	go func() {
		defer close(claimCompleted)
		deliveries, claimErr := claimService.ClaimPendingOutbox(
			claimContext,
			workerID,
			10,
			2*time.Minute,
		)
		claimResults <- claimResult{
			deliveries: deliveries,
			err:        claimErr,
		}
	}()
	waitForPostgresBackendLock(
		t,
		observerDB,
		claimPID,
		claimCompleted,
	)
	var blockedByArchive bool
	if err := observerDB.Raw(
		`SELECT CAST(? AS integer) = ANY(pg_blocking_pids(?)) AS blocked`,
		archivePID,
		claimPID,
	).Scan(&blockedByArchive).Error; err != nil {
		t.Fatalf("inspect archived Outbox claim blocker: %v", err)
	}
	if !blockedByArchive {
		t.Fatalf(
			"claim backend %d was not blocked by archive backend %d",
			claimPID,
			archivePID,
		)
	}
	if _, err := archiveConnection.ExecContext(
		archiveContext,
		"COMMIT",
	); err != nil {
		t.Fatalf("commit project archive barrier transaction: %v", err)
	}
	archiveCommitted = true

	var claimed claimResult
	select {
	case claimed = <-claimResults:
	case <-time.After(10 * time.Second):
		t.Fatal("Outbox claim did not finish after project archive committed")
	}
	if claimed.err != nil {
		t.Fatalf("claim Outbox after archive commit: %v", claimed.err)
	}
	if len(claimed.deliveries) != 1 ||
		claimed.deliveries[0].Event == nil ||
		claimed.deliveries[0].Event.ID != revocationEvent.ID ||
		claimed.deliveries[0].Event.Type !=
			services.ProjectAccessRevokedEventType {
		t.Fatalf(
			"archived claim leaked generic work or lost revocation: %+v",
			claimed.deliveries,
		)
	}
	if err := claimService.MarkOutboxDelivered(
		claimContext,
		claimed.deliveries[0].ID,
		workerID,
	); err != nil {
		t.Fatalf("finalize allowed archived Outbox delivery: %v", err)
	}

	if err := WithProjectScopeTransaction(
		context.Background(),
		runtimeDB,
		project.Scope(),
		func(scoped *gorm.DB) error {
			var genericDelivery models.OutboxDelivery
			if err := scoped.Where(
				"event_id = ?",
				genericEvent.ID,
			).Take(&genericDelivery).Error; err != nil {
				return err
			}
			if genericDelivery.Status != models.OutboxDeliveryPending ||
				genericDelivery.Attempts != 0 ||
				genericDelivery.LockedAt != nil ||
				genericDelivery.LockedBy != "" {
				return fmt.Errorf(
					"generic delivery changed after archive: %+v",
					genericDelivery,
				)
			}
			var revocationDelivery models.OutboxDelivery
			if err := scoped.Where(
				"event_id = ?",
				revocationEvent.ID,
			).Take(&revocationDelivery).Error; err != nil {
				return err
			}
			if revocationDelivery.Status !=
				models.OutboxDeliverySucceeded ||
				revocationDelivery.Attempts != 1 ||
				revocationDelivery.LockedAt != nil ||
				revocationDelivery.LockedBy != "" {
				return fmt.Errorf(
					"allowed revocation delivery not finalized: %+v",
					revocationDelivery,
				)
			}
			return nil
		},
	); err != nil {
		t.Fatalf("verify archived Outbox claim under FORCE RLS: %v", err)
	}
}

func createArchivablePostgresProject(
	t *testing.T,
	db *gorm.DB,
) models.Project {
	t.Helper()
	var defaultProject models.Project
	if err := db.Where(
		"key = ? AND status = ?",
		DefaultProjectKey,
		models.ProjectStatusActive,
	).Take(&defaultProject).Error; err != nil {
		t.Fatalf("load default project hierarchy: %v", err)
	}
	project := models.Project{
		OrganizationID: defaultProject.OrganizationID,
		BusinessUnitID: defaultProject.BusinessUnitID,
		Key:            models.ProjectKey("ARCHIVE"),
		Name:           "Archivable Project",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatalf("create non-default archive fixture: %v", err)
	}
	return project
}

func openPostgresArchiveRuntimeDB(
	t *testing.T,
	ownerDB *gorm.DB,
	suffix string,
) (*gorm.DB, *gorm.DB, string) {
	t.Helper()
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	var schemaName string
	if err := ownerDB.Raw("SELECT CURRENT_SCHEMA()").
		Scan(&schemaName).Error; err != nil {
		t.Fatalf("read archived Outbox fixture schema: %v", err)
	}
	quotedSchema := quotePostgresReleaseTestIdentifier(t, schemaName)
	runtimeRole := "chronodesk_archive_runtime_" + suffix
	quotedRuntimeRole := quotePostgresReleaseTestIdentifier(t, runtimeRole)
	runtimePassword := "ChronoDeskArchiveRuntime" + suffix + "!"
	silentConfig := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	observerDB, err := gorm.Open(postgres.Open(rawDSN), silentConfig)
	if err != nil {
		t.Fatal("open PostgreSQL archived Outbox observer")
	}
	observerSQL, err := observerDB.DB()
	if err != nil {
		t.Fatal("open PostgreSQL archived Outbox observer pool")
	}
	roleCreated := false
	var runtimeSQL *sql.DB
	t.Cleanup(func() {
		if runtimeSQL != nil {
			if closeErr := runtimeSQL.Close(); closeErr != nil {
				t.Errorf(
					"close archived Outbox runtime pool: %v",
					closeErr,
				)
			}
		}
		if roleCreated {
			if cleanupErr := observerDB.Exec(
				"DROP OWNED BY " + quotedRuntimeRole,
			).Error; cleanupErr != nil {
				t.Errorf(
					"drop archived Outbox runtime privileges: %v",
					cleanupErr,
				)
			}
			if cleanupErr := observerDB.Exec(
				"DROP ROLE IF EXISTS " + quotedRuntimeRole,
			).Error; cleanupErr != nil {
				t.Errorf(
					"drop archived Outbox runtime role: %v",
					cleanupErr,
				)
			}
		}
		if closeErr := observerSQL.Close(); closeErr != nil {
			t.Errorf(
				"close archived Outbox observer pool: %v",
				closeErr,
			)
		}
	})
	if err := observerDB.Exec(
		"CREATE ROLE " + quotedRuntimeRole +
			" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE " +
			"NOBYPASSRLS PASSWORD " +
			quotePostgresReleaseTestLiteral(runtimePassword),
	).Error; err != nil {
		t.Fatalf("create archived Outbox runtime role: %v", err)
	}
	roleCreated = true
	for _, grant := range []string{
		"GRANT USAGE ON SCHEMA " + quotedSchema +
			" TO " + quotedRuntimeRole,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " +
			quotedSchema + " TO " + quotedRuntimeRole,
		"GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA " +
			quotedSchema + " TO " + quotedRuntimeRole,
	} {
		if err := ownerDB.Exec(grant).Error; err != nil {
			t.Fatalf("grant archived Outbox runtime privilege: %v", err)
		}
	}

	runtimeURL, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatal("parse archived Outbox PostgreSQL DSN")
	}
	runtimeURL.User = url.UserPassword(runtimeRole, runtimePassword)
	query := runtimeURL.Query()
	query.Set("search_path", schemaName)
	runtimeURL.RawQuery = query.Encode()
	runtimeDB, err := gorm.Open(
		postgres.Open(runtimeURL.String()),
		silentConfig,
	)
	if err != nil {
		t.Fatal("open non-owner archived Outbox runtime")
	}
	runtimeSQL, err = runtimeDB.DB()
	if err != nil {
		t.Fatal("open non-owner archived Outbox runtime pool")
	}
	runtimeSQL.SetMaxOpenConns(6)
	runtimeSQL.SetMaxIdleConns(6)
	if err := runtimeSQL.Ping(); err != nil {
		t.Fatal("ping non-owner archived Outbox runtime")
	}
	return runtimeDB, observerDB, runtimeRole
}

func assertPostgresArchiveRuntimeRole(
	t *testing.T,
	runtimeDB *gorm.DB,
	wantRole string,
	ownerRole string,
) {
	t.Helper()
	var state struct {
		RoleName     string `gorm:"column:role_name"`
		IsSuper      bool   `gorm:"column:is_super"`
		BypassRLS    bool   `gorm:"column:bypass_rls"`
		ProjectOwner string `gorm:"column:project_owner"`
	}
	if err := runtimeDB.Raw(`
		SELECT
			CURRENT_USER AS role_name,
			role.rolsuper AS is_super,
			role.rolbypassrls AS bypass_rls,
			pg_get_userbyid(relation.relowner) AS project_owner
		FROM pg_roles AS role
		JOIN pg_class AS relation
		  ON relation.relname = 'projects'
		JOIN pg_namespace AS namespace
		  ON namespace.oid = relation.relnamespace
		 AND namespace.nspname = CURRENT_SCHEMA()
		WHERE role.rolname = CURRENT_USER
	`).Scan(&state).Error; err != nil {
		t.Fatalf("inspect archived Outbox runtime role: %v", err)
	}
	if state.RoleName != wantRole ||
		state.IsSuper ||
		state.BypassRLS ||
		state.ProjectOwner != ownerRole ||
		state.ProjectOwner == state.RoleName {
		t.Fatalf(
			"archived Outbox runtime role is not least privilege: %+v",
			state,
		)
	}
}
