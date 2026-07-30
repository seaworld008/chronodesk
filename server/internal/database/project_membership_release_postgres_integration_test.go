package database

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const projectMembershipUpsertedEventType = "io.chronodesk.project.membership.upserted.v1"

var postgresReleaseTestIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func TestPostgresFreshSeedMembershipSurvivesForceRLS(t *testing.T) {
	db, roleName, suffix := openPostgresMembershipReleaseTestDB(t, "fresh")

	assertPostgresReleaseTestRole(t, db, roleName)
	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("run fresh PostgreSQL migration: %v", err)
	}
	var defaultProject models.Project
	if err := db.Where("key = ?", DefaultProjectKey).
		First(&defaultProject).Error; err != nil {
		t.Fatalf("load fresh default project: %v", err)
	}
	if err := db.Model(&models.Project{}).
		Where("id = ?", defaultProject.ID).
		UpdateColumn("ticket_sequence", 41).Error; err != nil {
		t.Fatalf("set default project sequence before RLS rerun: %v", err)
	}
	if err := EnableProjectRLS(db); err != nil {
		t.Fatalf("enable FORCE RLS for fresh database: %v", err)
	}
	assertPostgresReleaseTestOwnershipAndRLS(t, db, roleName)
	liveTicket := createPostgresReleaseScopedTicket(
		t,
		db,
		defaultProject.Scope(),
		"DEFAULT-41",
	)
	lateUser := models.User{
		Username:     "other-only-" + suffix,
		Email:        "other-only-" + suffix + "@example.test",
		PasswordHash: "test-only-hash",
		Role:         models.RoleAgent,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&lateUser).Error; err != nil {
		t.Fatalf("create post-cutover Human: %v", err)
	}
	latePrincipal := models.ServicePrincipal{
		ID:     fmt.Sprintf("00000000-0000-4000-8000-%012s", suffix[len(suffix)-12:]),
		Name:   "other-only-" + suffix,
		Status: models.ServicePrincipalStatusActive,
		Scopes: datatypes.JSON(`["tickets:read"]`),
	}
	if err := db.Create(&latePrincipal).Error; err != nil {
		t.Fatalf("create post-cutover Service Principal: %v", err)
	}
	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("rerun full migration after FORCE RLS: %v", err)
	}
	if err := validatePostgresProjectOwnedNotNullContract(db); err != nil {
		t.Fatalf("project NOT NULL contract after migration rerun: %v", err)
	}
	assertPostgresProjectCutoverRerunIsNoop(
		t,
		db,
		defaultProject.Scope(),
		lateUser.ID,
		latePrincipal.ID,
		41,
	)
	assertPostgresReleaseScopedTicketUnchanged(
		t,
		db,
		defaultProject.Scope(),
		liveTicket,
	)

	t.Setenv("ADMIN_EMAIL", "fresh-release-"+suffix+"@example.test")
	t.Setenv("ADMIN_PASSWORD", "ChronoDesk-Release-Test-2026!")
	t.Setenv("ENVIRONMENT", "development")
	seedOptions := SeedOptions{
		EnsureInitialAdministratorMembership: services.
			EnsureBootstrapProjectAdministratorMembership,
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := SeedData(db, seedOptions); err != nil {
			t.Fatalf("seed fresh FORCE RLS database attempt %d: %v", attempt, err)
		}
	}

	assertSingleAuditedPostgresMembership(
		t,
		db,
		"chronodesk-bootstrap",
	)

	if err := db.Exec(`
		ALTER TABLE idempotency_records
			ALTER COLUMN organization_id DROP NOT NULL
	`).Error; err != nil {
		t.Fatalf("weaken idempotency scope for runtime gate test: %v", err)
	}
	if err := ValidateRuntimeSchema(db); err == nil || !strings.Contains(
		err.Error(),
		"idempotency_records.organization_id (nullable)",
	) {
		t.Fatalf("runtime nullable project scope error = %v", err)
	}
	if err := MigrateProjectScope(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("repair nullable project scope after checkpoint: %v", err)
	}
	if err := validatePostgresProjectOwnedNotNullContract(db); err != nil {
		t.Fatalf("repaired project NOT NULL contract: %v", err)
	}
}

func TestPostgresLegacyAdministratorMigrationOwnsSingleAuditedMembership(
	t *testing.T,
) {
	db, roleName, suffix := openPostgresMembershipReleaseTestDB(t, "legacy")

	assertPostgresReleaseTestRole(t, db, roleName)
	if err := db.Exec(`
		CREATE TABLE users (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ,
			updated_at TIMESTAMPTZ,
			deleted_at TIMESTAMPTZ,
			username VARCHAR(50) NOT NULL,
			email VARCHAR(100) NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			role VARCHAR(20) NOT NULL DEFAULT 'customer',
			status VARCHAR(20) NOT NULL DEFAULT 'inactive'
		)
	`).Error; err != nil {
		t.Fatalf("create legacy users table: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO users (
			username,
			email,
			password_hash,
			role,
			status
		) VALUES (?, ?, ?, ?, ?)
	`,
		"legacy-admin",
		"legacy-release-"+suffix+"@example.test",
		"legacy-password-hash",
		models.RoleAdmin,
		models.UserStatusActive,
	).Error; err != nil {
		t.Fatalf("insert legacy administrator: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE tickets (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ,
			updated_at TIMESTAMPTZ,
			ticket_number VARCHAR(64) NOT NULL,
			title VARCHAR(255) NOT NULL,
			description TEXT NOT NULL,
			type VARCHAR(20) NOT NULL DEFAULT 'request',
			priority VARCHAR(20) NOT NULL DEFAULT 'normal',
			status VARCHAR(20) NOT NULL DEFAULT 'open',
			source VARCHAR(20) NOT NULL DEFAULT 'web',
			created_by_id BIGINT,
			CONSTRAINT uni_tickets_ticket_number UNIQUE (ticket_number)
		)
	`).Error; err != nil {
		t.Fatalf("create legacy tickets table: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO tickets (
			ticket_number,
			title,
			description,
			created_by_id
		) VALUES (?, ?, ?, ?)
	`, "LEGACY-1", "legacy scoped ticket", "legacy ticket body", 1).Error; err != nil {
		t.Fatalf("insert legacy ticket: %v", err)
	}

	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("upgrade legacy PostgreSQL database: %v", err)
	}
	var migratedTicket models.Ticket
	if err := db.Where("title = ?", "legacy scoped ticket").
		First(&migratedTicket).Error; err != nil {
		t.Fatalf("load migrated legacy ticket: %v", err)
	}
	if migratedTicket.OrganizationID == 0 ||
		migratedTicket.ProjectID == 0 ||
		migratedTicket.QueueID == 0 ||
		strings.TrimSpace(migratedTicket.PublicID) == "" ||
		strings.TrimSpace(migratedTicket.RequestTypeVersionID) == "" ||
		strings.TrimSpace(migratedTicket.WorkflowVersionID) == "" {
		t.Fatalf("legacy ticket project contract incomplete: %+v", migratedTicket)
	}
	publicID, err := uuid.Parse(migratedTicket.PublicID)
	if err != nil || publicID.Version() != 7 {
		t.Fatalf(
			"legacy ticket public ID = %q (%v), want UUIDv7",
			migratedTicket.PublicID,
			err,
		)
	}
	if err := EnableProjectRLS(db); err != nil {
		t.Fatalf("enable FORCE RLS for upgraded database: %v", err)
	}
	assertPostgresReleaseTestOwnershipAndRLS(t, db, roleName)
	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("rerun upgraded migration after FORCE RLS: %v", err)
	}

	t.Setenv("ADMIN_EMAIL", "unused-release-"+suffix+"@example.test")
	t.Setenv("ADMIN_PASSWORD", "ChronoDesk-Release-Test-2026!")
	t.Setenv("ENVIRONMENT", "development")
	seedOptions := SeedOptions{
		EnsureInitialAdministratorMembership: services.
			EnsureBootstrapProjectAdministratorMembership,
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := SeedData(db, seedOptions); err != nil {
			t.Fatalf("seed upgraded FORCE RLS database attempt %d: %v", attempt, err)
		}
	}

	assertSingleAuditedPostgresMembership(
		t,
		db,
		"chronodesk-project-scope-migration",
	)
}

func TestPostgresPartialProjectScopeMigrationRetry(t *testing.T) {
	db, _, suffix := openPostgresMembershipReleaseTestDB(t, "partial_retry")
	if err := db.Exec(`
		CREATE TABLE users (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ,
			updated_at TIMESTAMPTZ,
			deleted_at TIMESTAMPTZ,
			username VARCHAR(50) NOT NULL,
			email VARCHAR(100) NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			role VARCHAR(20) NOT NULL DEFAULT 'customer',
			status VARCHAR(20) NOT NULL DEFAULT 'inactive'
		)
	`).Error; err != nil {
		t.Fatalf("create partial-retry users table: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO users (
			username,
			email,
			password_hash,
			role,
			status
		) VALUES (?, ?, ?, ?, ?)
	`,
		"partial-retry-admin",
		"partial-retry-"+suffix+"@example.test",
		"partial-retry-password-hash",
		models.RoleAdmin,
		models.UserStatusActive,
	).Error; err != nil {
		t.Fatalf("insert partial-retry administrator: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE tickets (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ,
			updated_at TIMESTAMPTZ,
			ticket_number VARCHAR(64) NOT NULL,
			title VARCHAR(255) NOT NULL,
			description TEXT NOT NULL,
			type VARCHAR(20) NOT NULL DEFAULT 'request',
			priority VARCHAR(20) NOT NULL DEFAULT 'normal',
			status VARCHAR(20) NOT NULL DEFAULT 'open',
			source VARCHAR(20) NOT NULL DEFAULT 'web',
			created_by_id BIGINT,
			public_id VARCHAR(36),
			organization_id BIGINT,
			project_id BIGINT,
			queue_id BIGINT,
			request_type_version_id VARCHAR(36),
			workflow_version_id VARCHAR(36),
			CONSTRAINT uni_tickets_ticket_number UNIQUE (ticket_number)
		)
	`).Error; err != nil {
		t.Fatalf("create partial-retry tickets table: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO tickets (
			ticket_number,
			title,
			description,
			created_by_id
		) VALUES (?, ?, ?, ?)
	`,
		"PARTIAL-1",
		"partial retry ticket",
		"partial retry body",
		1,
	).Error; err != nil {
		t.Fatalf("insert partial-retry ticket: %v", err)
	}

	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("retry partial PostgreSQL project migration: %v", err)
	}
	var ticket models.Ticket
	if err := db.Where("title = ?", "partial retry ticket").
		First(&ticket).Error; err != nil {
		t.Fatalf("load partial-retry migrated Ticket: %v", err)
	}
	publicID, err := uuid.Parse(ticket.PublicID)
	if err != nil ||
		publicID.Version() != 7 ||
		ticket.OrganizationID == 0 ||
		ticket.ProjectID == 0 ||
		ticket.QueueID == 0 ||
		ticket.RequestTypeVersionID == "" ||
		ticket.WorkflowVersionID == "" {
		t.Fatalf(
			"partial-retry Ticket contract = %+v, public ID error = %v",
			ticket,
			err,
		)
	}
}

func TestPostgresProjectScopeCutoverRejectsUncheckpointedProjectState(
	t *testing.T,
) {
	t.Run("project control data", func(t *testing.T) {
		db, _, _ := openPostgresMembershipReleaseTestDB(
			t,
			"noctl",
		)
		if err := db.Exec(`
			CREATE TABLE projects (
				id BIGINT PRIMARY KEY
			)
		`).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO projects (id) VALUES (1)`).Error; err != nil {
			t.Fatal(err)
		}
		err := RunMigrations(db)
		if err == nil || !strings.Contains(
			err.Error(),
			"projects contains 1 project control row",
		) {
			t.Fatalf("uncheckpointed project control error = %v", err)
		}
	})

	t.Run("pre-scoped business data", func(t *testing.T) {
		db, _, _ := openPostgresMembershipReleaseTestDB(
			t,
			"noscope",
		)
		if err := db.Exec(`
			CREATE TABLE tickets (
				id BIGINT PRIMARY KEY,
				organization_id BIGINT,
				project_id BIGINT
			)
		`).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`
			INSERT INTO tickets (id, organization_id, project_id)
			VALUES (1, 10, 20)
		`).Error; err != nil {
			t.Fatal(err)
		}
		err := RunMigrations(db)
		if err == nil || !strings.Contains(
			err.Error(),
			"tickets contains 1 pre-scoped row",
		) {
			t.Fatalf("uncheckpointed business scope error = %v", err)
		}
	})
}

func TestPostgresProjectScopeCutoverRejectsMissingCheckpointAfterRLS(
	t *testing.T,
) {
	db, _, _ := openPostgresMembershipReleaseTestDB(t, "missing_checkpoint")
	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("run initial PostgreSQL migration: %v", err)
	}
	if err := EnableProjectRLS(db); err != nil {
		t.Fatalf("enable FORCE RLS: %v", err)
	}
	if err := db.Where(
		"key = ?",
		projectScopeCutoverCheckpointKey,
	).Delete(&models.SchemaMigrationCheckpoint{}).Error; err != nil {
		t.Fatalf("remove project cutover checkpoint: %v", err)
	}
	err := MigrateProjectScope(
		db,
		services.EnsureProjectScopeMigrationMembership,
	)
	if err == nil || !strings.Contains(
		err.Error(),
		"checkpoint is missing after project RLS was enabled",
	) {
		t.Fatalf("missing post-RLS checkpoint error = %v", err)
	}
}

func assertPostgresProjectCutoverRerunIsNoop(
	t *testing.T,
	db *gorm.DB,
	scope models.ProjectScope,
	lateUserID uint,
	latePrincipalID string,
	wantSequence uint64,
) {
	t.Helper()
	var checkpointCount int64
	if err := db.Model(&models.SchemaMigrationCheckpoint{}).
		Where("key = ?", projectScopeCutoverCheckpointKey).
		Count(&checkpointCount).Error; err != nil {
		t.Fatalf("count project cutover checkpoint: %v", err)
	}
	if checkpointCount != 1 {
		t.Fatalf("project cutover checkpoint count = %d, want 1", checkpointCount)
	}
	var project models.Project
	if err := db.First(&project, scope.ProjectID).Error; err != nil {
		t.Fatalf("reload default project after migration rerun: %v", err)
	}
	if project.TicketSequence != wantSequence {
		t.Fatalf(
			"default project sequence = %d after migration rerun, want %d",
			project.TicketSequence,
			wantSequence,
		)
	}
	err := WithProjectScopeTransaction(
		context.Background(),
		db,
		scope,
		func(scoped *gorm.DB) error {
			var memberships int64
			if err := scoped.Model(&models.ProjectMembership{}).
				Where(
					"project_id = ? AND user_id = ?",
					scope.ProjectID,
					lateUserID,
				).
				Count(&memberships).Error; err != nil {
				return err
			}
			var grants int64
			if err := scoped.Model(&models.ProjectPrincipalGrant{}).
				Where(
					"project_id = ? AND service_principal_id = ?",
					scope.ProjectID,
					latePrincipalID,
				).
				Count(&grants).Error; err != nil {
				return err
			}
			if memberships != 0 || grants != 0 {
				return fmt.Errorf(
					"migration rerun granted DEFAULT access: memberships=%d grants=%d",
					memberships,
					grants,
				)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("verify post-cutover identities remain ungranted: %v", err)
	}
}

func createPostgresReleaseScopedTicket(
	t *testing.T,
	db *gorm.DB,
	scope models.ProjectScope,
	ticketNumber string,
) models.Ticket {
	t.Helper()
	var created models.Ticket
	err := WithProjectScopeTransaction(
		context.Background(),
		db,
		scope,
		func(scoped *gorm.DB) error {
			var queue models.Queue
			if err := scoped.Where(
				"project_id = ? AND is_default = ?",
				scope.ProjectID,
				true,
			).First(&queue).Error; err != nil {
				return err
			}
			created = models.Ticket{
				OrganizationID:       scope.OrganizationID,
				ProjectID:            scope.ProjectID,
				QueueID:              queue.ID,
				RequestTypeVersionID: bootstrapRequestRequest,
				WorkflowVersionID:    bootstrapWorkflow,
				TicketNumber:         ticketNumber,
				Title:                "FORCE RLS migration rerun",
				Description:          "live scoped Ticket must remain immutable",
				Type:                 models.TicketTypeRequest,
				Priority:             models.TicketPriorityHigh,
				Status:               models.TicketStatusOpen,
				Source:               models.TicketSourceAPI,
				Version:              5,
				CreatedByActorType:   models.ActorTypeSystem,
				CreatedByActorID:     "release-regression",
			}
			return scoped.Create(&created).Error
		},
	)
	if err != nil {
		t.Fatalf("create live FORCE RLS Ticket: %v", err)
	}
	return created
}

func assertPostgresReleaseScopedTicketUnchanged(
	t *testing.T,
	db *gorm.DB,
	scope models.ProjectScope,
	want models.Ticket,
) {
	t.Helper()
	err := WithProjectScopeTransaction(
		context.Background(),
		db,
		scope,
		func(scoped *gorm.DB) error {
			var got models.Ticket
			if err := scoped.Unscoped().First(&got, want.ID).Error; err != nil {
				return err
			}
			if got.PublicID != want.PublicID ||
				got.OrganizationID != want.OrganizationID ||
				got.ProjectID != want.ProjectID ||
				got.QueueID != want.QueueID ||
				got.RequestTypeVersionID != want.RequestTypeVersionID ||
				got.WorkflowVersionID != want.WorkflowVersionID ||
				got.TicketNumber != want.TicketNumber ||
				got.Version != want.Version ||
				got.CreatedByActorType != want.CreatedByActorType ||
				got.CreatedByActorID != want.CreatedByActorID {
				return fmt.Errorf("migration rerun rewrote live Ticket: %+v", got)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("verify live FORCE RLS Ticket after migration rerun: %v", err)
	}
}

func openPostgresMembershipReleaseTestDB(
	t *testing.T,
	fixture string,
) (*gorm.DB, string, string) {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for the isolated PostgreSQL release regression tests",
		)
	}
	rawDSN := strings.TrimSpace(os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"))
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatal("parse PostgreSQL integration DSN")
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("release regression tests require a loopback PostgreSQL target")
		}
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	schemaName := "chronodesk_release_" + fixture + "_" + suffix
	roleName := "chronodesk_release_owner_" + fixture + "_" + suffix
	quotedSchema := quotePostgresReleaseTestIdentifier(t, schemaName)
	quotedRole := quotePostgresReleaseTestIdentifier(t, roleName)
	rolePassword := "ChronoDeskRelease" + suffix + "!"

	silentConfig := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	adminDB, err := gorm.Open(postgres.Open(rawDSN), silentConfig)
	if err != nil {
		t.Fatal("open PostgreSQL integration administrator connection")
	}
	adminSQLDB, err := adminDB.DB()
	if err != nil {
		t.Fatal("open PostgreSQL integration administrator pool")
	}
	var runtimeSQLDB *sql.DB
	roleCreated := false
	schemaCreated := false
	t.Cleanup(func() {
		if runtimeSQLDB != nil {
			if closeErr := runtimeSQLDB.Close(); closeErr != nil {
				t.Errorf("close isolated PostgreSQL runtime pool: %v", closeErr)
			}
		}
		if schemaCreated {
			if cleanupErr := adminDB.Exec(
				"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
			).Error; cleanupErr != nil {
				t.Errorf("drop isolated PostgreSQL schema: %v", cleanupErr)
			}
		}
		if roleCreated {
			if cleanupErr := adminDB.Exec(
				"DROP ROLE IF EXISTS " + quotedRole,
			).Error; cleanupErr != nil {
				t.Errorf("drop isolated PostgreSQL role: %v", cleanupErr)
			}
		}
		if closeErr := adminSQLDB.Close(); closeErr != nil {
			t.Errorf("close PostgreSQL integration administrator pool: %v", closeErr)
		}
	})

	if err := adminDB.Exec(
		"CREATE ROLE " + quotedRole +
			" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD " +
			quotePostgresReleaseTestLiteral(rolePassword),
	).Error; err != nil {
		t.Fatal("create isolated least-privilege PostgreSQL owner role")
	}
	roleCreated = true
	if err := adminDB.Exec(
		"CREATE SCHEMA " + quotedSchema + " AUTHORIZATION " + quotedRole,
	).Error; err != nil {
		t.Fatal("create isolated PostgreSQL schema owned by runtime role")
	}
	schemaCreated = true

	runtimeParsed := *parsed
	runtimeParsed.User = url.UserPassword(roleName, rolePassword)
	query := runtimeParsed.Query()
	query.Set("search_path", schemaName)
	runtimeParsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(runtimeParsed.String()), silentConfig)
	if err != nil {
		t.Fatal("open isolated PostgreSQL owner connection")
	}
	runtimeSQLDB, err = db.DB()
	if err != nil {
		t.Fatal("open isolated PostgreSQL owner pool")
	}
	if err := runtimeSQLDB.Ping(); err != nil {
		t.Fatal("ping isolated PostgreSQL owner connection")
	}
	return db, roleName, suffix
}

func assertPostgresReleaseTestRole(
	t *testing.T,
	db *gorm.DB,
	wantRole string,
) {
	t.Helper()
	var state struct {
		RoleName  string `gorm:"column:role_name"`
		IsSuper   bool   `gorm:"column:is_super"`
		BypassRLS bool   `gorm:"column:bypass_rls"`
		SchemaOwn string `gorm:"column:schema_owner"`
	}
	if err := db.Raw(`
		SELECT
			CURRENT_USER AS role_name,
			role.rolsuper AS is_super,
			role.rolbypassrls AS bypass_rls,
			pg_get_userbyid(namespace.nspowner) AS schema_owner
		FROM pg_roles AS role
		JOIN pg_namespace AS namespace
		  ON namespace.nspname = CURRENT_SCHEMA()
		WHERE role.rolname = CURRENT_USER
	`).Scan(&state).Error; err != nil {
		t.Fatalf("inspect isolated PostgreSQL owner role: %v", err)
	}
	if state.RoleName != wantRole ||
		state.IsSuper ||
		state.BypassRLS ||
		state.SchemaOwn != wantRole {
		t.Fatalf(
			"isolated PostgreSQL role = %+v, want non-superuser NOBYPASSRLS owner %q",
			state,
			wantRole,
		)
	}
}

func assertPostgresReleaseTestOwnershipAndRLS(
	t *testing.T,
	db *gorm.DB,
	wantRole string,
) {
	t.Helper()
	if err := ValidateProjectRLSRuntime(db); err != nil {
		t.Fatalf("validate FORCE RLS runtime state: %v", err)
	}

	var foreignOwnedTables int64
	if err := db.Raw(`
		SELECT COUNT(*)
		FROM pg_class AS relation
		JOIN pg_namespace AS namespace
		  ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = CURRENT_SCHEMA()
		  AND relation.relkind IN ('r', 'p')
		  AND pg_get_userbyid(relation.relowner) <> ?
	`, wantRole).Scan(&foreignOwnedTables).Error; err != nil {
		t.Fatalf("inspect isolated PostgreSQL table ownership: %v", err)
	}
	if foreignOwnedTables != 0 {
		t.Fatalf(
			"isolated schema contains %d table(s) not owned by %q",
			foreignOwnedTables,
			wantRole,
		)
	}

	var eventsWithoutScope int64
	if err := db.Model(&models.DomainEvent{}).
		Count(&eventsWithoutScope).Error; err != nil {
		t.Fatalf("query FORCE RLS event table without scope: %v", err)
	}
	if eventsWithoutScope != 0 {
		t.Fatalf(
			"FORCE RLS exposed %d domain event(s) to the unscoped table owner",
			eventsWithoutScope,
		)
	}
}

func assertSingleAuditedPostgresMembership(
	t *testing.T,
	db *gorm.DB,
	wantActorID string,
) {
	t.Helper()
	var organization models.Organization
	if err := db.Where("slug = ?", DefaultOrganizationSlug).
		First(&organization).Error; err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	var project models.Project
	if err := db.Where(
		"organization_id = ? AND key = ?",
		organization.ID,
		DefaultProjectKey,
	).First(&project).Error; err != nil {
		t.Fatalf("load default project: %v", err)
	}
	var administrator models.User
	if err := db.Where("role = ?", models.RoleAdmin).
		First(&administrator).Error; err != nil {
		t.Fatalf("load initial administrator: %v", err)
	}

	err := WithProjectScopeTransaction(
		context.Background(),
		db,
		project.Scope(),
		func(scoped *gorm.DB) error {
			var memberships []models.ProjectMembership
			if err := scoped.Where(
				"project_id = ? AND user_id = ?",
				project.ID,
				administrator.ID,
			).Find(&memberships).Error; err != nil {
				return fmt.Errorf("load administrator membership: %w", err)
			}
			if len(memberships) != 1 ||
				memberships[0].Role != models.ProjectRoleAdmin ||
				!memberships[0].IsActive {
				return fmt.Errorf(
					"membership = %+v, want one active project administrator",
					memberships,
				)
			}

			var events []models.DomainEvent
			if err := scoped.Find(&events).Error; err != nil {
				return fmt.Errorf("load scoped domain events: %w", err)
			}
			if len(events) != 1 ||
				events[0].Type != projectMembershipUpsertedEventType ||
				events[0].OrganizationID != organization.ID ||
				events[0].ProjectID != project.ID ||
				events[0].ActorType != models.ActorTypeSystem ||
				events[0].ActorID != wantActorID {
				return fmt.Errorf(
					"domain events = %+v, want one membership event by %q",
					events,
					wantActorID,
				)
			}

			var deliveries []models.OutboxDelivery
			if err := scoped.Find(&deliveries).Error; err != nil {
				return fmt.Errorf("load scoped Outbox deliveries: %w", err)
			}
			if len(deliveries) != 1 ||
				deliveries[0].EventID != events[0].ID ||
				deliveries[0].DestinationType != "event_stream" {
				return fmt.Errorf(
					"Outbox deliveries = %+v, want one delivery for event %q",
					deliveries,
					events[0].ID,
				)
			}

			var auditEntries []models.AuditLedgerEntry
			if err := scoped.Find(&auditEntries).Error; err != nil {
				return fmt.Errorf("load scoped audit entries: %w", err)
			}
			if len(auditEntries) != 1 ||
				auditEntries[0].DomainEventID != events[0].ID ||
				auditEntries[0].ActorType != models.ActorTypeSystem ||
				auditEntries[0].ActorID != wantActorID {
				return fmt.Errorf(
					"audit entries = %+v, want one entry by %q",
					auditEntries,
					wantActorID,
				)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("verify audited administrator membership: %v", err)
	}
}

func quotePostgresReleaseTestIdentifier(t *testing.T, identifier string) string {
	t.Helper()
	if len(identifier) > 63 ||
		!postgresReleaseTestIdentifierPattern.MatchString(identifier) {
		t.Fatalf("unsafe generated PostgreSQL test identifier %q", identifier)
	}
	return `"` + identifier + `"`
}

func quotePostgresReleaseTestLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
