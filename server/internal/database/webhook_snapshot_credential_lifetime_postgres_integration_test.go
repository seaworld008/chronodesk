package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestWebhookCredentialLifetimeMigrationUnderForceRLSPostgres(
	t *testing.T,
) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL webhook credential migration evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse PostgreSQL integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal(
				"PostgreSQL webhook credential migration test requires a loopback target",
			)
		}
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	schemaName := "chronodesk_webhook_lifetime_" + suffix
	ownerRole := "chronodesk_webhook_owner_" + suffix
	runtimeRole := "chronodesk_webhook_runtime_" + suffix
	ownerPassword := "WebhookOwner" + suffix + "!"
	runtimePassword := "WebhookRuntime" + suffix + "!"
	quotedSchema := quotePostgresRLSTestIdentifier(schemaName)
	quotedOwner := quotePostgresRLSTestIdentifier(ownerRole)
	quotedRuntime := quotePostgresRLSTestIdentifier(runtimeRole)
	silentConfig := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	admin, err := gorm.Open(postgres.Open(rawDSN), silentConfig)
	if err != nil {
		t.Fatalf("open PostgreSQL integration administrator: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	var ownerSQL, runtimeSQL *sql.DB
	schemaCreated := false
	ownerCreated := false
	runtimeCreated := false
	t.Cleanup(func() {
		if runtimeSQL != nil {
			_ = runtimeSQL.Close()
		}
		if ownerSQL != nil {
			_ = ownerSQL.Close()
		}
		if schemaCreated {
			_ = admin.Exec(
				"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
			).Error
		}
		if runtimeCreated {
			_ = admin.Exec("DROP ROLE IF EXISTS " + quotedRuntime).Error
		}
		if ownerCreated {
			_ = admin.Exec("DROP ROLE IF EXISTS " + quotedOwner).Error
		}
		_ = adminSQL.Close()
	})
	if err := admin.Exec(
		"CREATE ROLE " + quotedOwner +
			" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD " +
			quotePostgresRLSTestLiteral(ownerPassword),
	).Error; err != nil {
		t.Fatalf("create PostgreSQL migration owner: %v", err)
	}
	ownerCreated = true
	if err := admin.Exec(
		"CREATE ROLE " + quotedRuntime +
			" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD " +
			quotePostgresRLSTestLiteral(runtimePassword),
	).Error; err != nil {
		t.Fatalf("create PostgreSQL runtime role: %v", err)
	}
	runtimeCreated = true
	if err := admin.Exec(
		"CREATE SCHEMA " + quotedSchema + " AUTHORIZATION " + quotedOwner,
	).Error; err != nil {
		t.Fatalf("create PostgreSQL webhook credential schema: %v", err)
	}
	schemaCreated = true

	ownerURL := *parsed
	ownerURL.User = url.UserPassword(ownerRole, ownerPassword)
	ownerQuery := ownerURL.Query()
	ownerQuery.Set("search_path", schemaName)
	ownerURL.RawQuery = ownerQuery.Encode()
	owner, err := gorm.Open(
		postgres.Open(ownerURL.String()),
		silentConfig,
	)
	if err != nil {
		t.Fatalf("open PostgreSQL NOBYPASSRLS migration owner: %v", err)
	}
	ownerSQL, err = owner.DB()
	if err != nil {
		t.Fatal(err)
	}
	legacyStatements := []string{
		`CREATE TABLE projects (
			id BIGINT PRIMARY KEY,
			organization_id BIGINT NOT NULL,
			status VARCHAR(20) NOT NULL
		)`,
		`CREATE TABLE domain_events (
			id VARCHAR(36) PRIMARY KEY,
			organization_id BIGINT NOT NULL,
			project_id BIGINT NOT NULL
		)`,
		`CREATE TABLE webhook_delivery_snapshots (
			id VARCHAR(36) PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL,
			organization_id BIGINT NOT NULL,
			project_id BIGINT NOT NULL,
			config_id BIGINT NOT NULL,
			event_id VARCHAR(36) NOT NULL,
			secret VARCHAR(2048) NOT NULL DEFAULT '',
			previous_secret VARCHAR(2048) NOT NULL DEFAULT '',
			access_token VARCHAR(2048) NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE outbox_deliveries (
			id VARCHAR(36) PRIMARY KEY,
			organization_id BIGINT NOT NULL,
			project_id BIGINT NOT NULL,
			event_id VARCHAR(36) NOT NULL,
			destination_type VARCHAR(50) NOT NULL,
			destination_id VARCHAR(128) NOT NULL,
			status VARCHAR(20) NOT NULL
		)`,
	}
	for _, statement := range legacyStatements {
		if err := owner.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := owner.AutoMigrate(
		&models.SchemaMigrationCheckpoint{},
	); err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(`
		INSERT INTO projects (id, organization_id, status)
		VALUES
			(22, 11, 'active'),
			(23, 11, 'archived')
	`).Error; err != nil {
		t.Fatal(err)
	}
	seedLegacyWebhookCredentialPair(t, owner)
	for _, table := range []string{
		"domain_events",
		"outbox_deliveries",
		"webhook_delivery_snapshots",
	} {
		quotedTable := quotePostgresRLSTestIdentifier(table)
		if err := owner.Exec(
			"ALTER TABLE " + quotedTable + " ENABLE ROW LEVEL SECURITY",
		).Error; err != nil {
			t.Fatal(err)
		}
		if err := owner.Exec(
			"ALTER TABLE " + quotedTable + " FORCE ROW LEVEL SECURITY",
		).Error; err != nil {
			t.Fatal(err)
		}
		predicate := `(organization_id = NULLIF(current_setting(` +
			`'chronodesk.organization_id', true), '')::bigint AND ` +
			`project_id = NULLIF(current_setting(` +
			`'chronodesk.project_id', true), '')::bigint)`
		if err := owner.Exec(
			"CREATE POLICY chronodesk_project_scope ON " + quotedTable +
				" FOR ALL TO PUBLIC USING " + predicate +
				" WITH CHECK " + predicate,
		).Error; err != nil {
			t.Fatal(err)
		}
	}
	var ownerState struct {
		Superuser bool
		BypassRLS bool
		Owner     bool
	}
	if err := owner.Raw(`
		SELECT
			role.rolsuper AS superuser,
			role.rolbypassrls AS bypass_rls,
			table_state.relowner = role.oid AS owner
		FROM pg_roles AS role
		JOIN pg_class AS table_state
		  ON table_state.relname = 'webhook_delivery_snapshots'
		JOIN pg_namespace AS namespace
		  ON namespace.oid = table_state.relnamespace
		 AND namespace.nspname = CURRENT_SCHEMA()
		WHERE role.rolname = CURRENT_USER
	`).Scan(&ownerState).Error; err != nil {
		t.Fatal(err)
	}
	if ownerState.Superuser || ownerState.BypassRLS || !ownerState.Owner {
		t.Fatalf("unexpected migration owner state: %+v", ownerState)
	}
	var invisibleRows int64
	if err := owner.Table("webhook_delivery_snapshots").
		Count(&invisibleRows).Error; err != nil {
		t.Fatal(err)
	}
	if invisibleRows != 0 {
		t.Fatalf(
			"FORCE RLS migration owner unexpectedly saw %d unscoped rows",
			invisibleRows,
		)
	}

	const callbackName = "test:postgres_webhook_checkpoint_failure"
	if err := owner.Callback().Create().Before("gorm:create").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement != nil &&
				tx.Statement.Table == "schema_migration_checkpoints" {
				_ = tx.AddError(errors.New("injected PostgreSQL checkpoint failure"))
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	firstCutover := time.Date(
		2026,
		8,
		10,
		1,
		2,
		3,
		987654321,
		time.UTC,
	)
	err = migrateWebhookSnapshotCredentialLifetimeContractAt(
		owner,
		firstCutover,
	)
	if err == nil ||
		!strings.Contains(err.Error(), "injected PostgreSQL checkpoint failure") {
		t.Fatalf(
			"PostgreSQL migration error = %v, want injected checkpoint failure",
			err,
		)
	}
	if err := owner.Callback().Create().Remove(callbackName); err != nil {
		t.Fatal(err)
	}
	assertPostgresWebhookCredentialForceRLS(t, owner)
	var checkpointCount int64
	if err := owner.Model(&models.SchemaMigrationCheckpoint{}).
		Where("key = ?", webhookSnapshotCredentialLifetimeCheckpointKey).
		Count(&checkpointCount).Error; err != nil {
		t.Fatal(err)
	}
	if checkpointCount != 0 {
		t.Fatalf("failed PostgreSQL migration committed checkpoint=%d", checkpointCount)
	}
	for _, required := range []struct {
		table  string
		column string
	}{
		{"webhook_delivery_snapshots", "credential_expires_at"},
		{"outbox_deliveries", "expires_at"},
	} {
		present, columnErr := hasExactDatabaseColumn(
			owner,
			required.table,
			required.column,
		)
		if columnErr != nil {
			t.Fatal(columnErr)
		}
		if present {
			t.Fatalf(
				"failed PostgreSQL migration retained half-applied column %s.%s",
				required.table,
				required.column,
			)
		}
	}

	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		owner,
		firstCutover,
	); err != nil {
		t.Fatalf("migrate FORCE-RLS legacy webhook credentials: %v", err)
	}
	assertPostgresWebhookCredentialForceRLS(t, owner)
	var checkpoint models.SchemaMigrationCheckpoint
	if err := owner.Where(
		"key = ?",
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Take(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	persistedDeadline := checkpoint.CompletedAt.Add(
		models.WebhookDeliveryCredentialLifetime,
	)
	assertPostgresWebhookCredentialDeadlines(
		t,
		owner,
		models.ProjectScope{OrganizationID: 11, ProjectID: 22},
		persistedDeadline,
	)
	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		owner,
		firstCutover.Add(30*24*time.Hour),
	); err != nil {
		t.Fatalf("rerun FORCE-RLS webhook credential migration: %v", err)
	}
	assertPostgresWebhookCredentialDeadlines(
		t,
		owner,
		models.ProjectScope{OrganizationID: 11, ProjectID: 22},
		persistedDeadline,
	)
	if err := MigrateWebhookSnapshotCredentialLifetimeContract(owner); err != nil {
		t.Fatalf("finalize PostgreSQL webhook credential contract: %v", err)
	}
	if err := ValidateWebhookSnapshotCredentialLifetimeContract(owner); err != nil {
		t.Fatalf("validate PostgreSQL webhook credential contract: %v", err)
	}
	assertPostgresWebhookCredentialForceRLS(t, owner)

	if err := admin.Exec(
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRuntime,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(
		"GRANT SELECT ON projects, domain_events, outbox_deliveries, " +
			"webhook_delivery_snapshots, schema_migration_checkpoints TO " +
			quotedRuntime,
	).Error; err != nil {
		t.Fatal(err)
	}
	runtimeURL := ownerURL
	runtimeURL.User = url.UserPassword(runtimeRole, runtimePassword)
	runtime, err := gorm.Open(
		postgres.Open(runtimeURL.String()),
		silentConfig,
	)
	if err != nil {
		t.Fatalf("open PostgreSQL webhook runtime role: %v", err)
	}
	runtimeSQL, err = runtime.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWebhookCredentialLifetimeCatalog(runtime); err != nil {
		t.Fatalf("validate runtime PostgreSQL webhook credential catalog: %v", err)
	}
	if err := ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatalf("validate scoped runtime webhook credential data: %v", err)
	}

	secondEventID := "00000000-0000-4000-8000-000000000911"
	secondSnapshotID := "00000000-0000-7000-8000-000000000912"
	secondDeliveryID := "00000000-0000-4000-8000-000000000913"
	secondDeadline := persistedDeadline.Add(time.Hour)
	if err := WithProjectScopeTransaction(
		context.Background(),
		owner,
		models.ProjectScope{OrganizationID: 11, ProjectID: 23},
		func(tx *gorm.DB) error {
			if err := tx.Exec(`
				INSERT INTO domain_events (id, organization_id, project_id)
				VALUES (?, 11, 23)
			`, secondEventID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`
				INSERT INTO webhook_delivery_snapshots (
					id, created_at, organization_id, project_id, config_id,
					event_id, credential_expires_at
				) VALUES (?, ?, 11, 23, 78, ?, ?)
			`,
				secondSnapshotID,
				firstCutover,
				secondEventID,
				secondDeadline,
			).Error; err != nil {
				return err
			}
			return tx.Exec(`
				INSERT INTO outbox_deliveries (
					id, organization_id, project_id, event_id,
					destination_type, destination_id, status, expires_at
				) VALUES (?, 11, 23, ?, 'webhook', ?, 'pending', ?)
			`,
				secondDeliveryID,
				secondEventID,
				"snapshot:"+secondSnapshotID,
				secondDeadline.Add(time.Second),
			).Error
		},
	); err != nil {
		t.Fatal(err)
	}
	err = ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
		context.Background(),
		runtime,
	)
	if err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "deadline") ||
		!strings.Contains(err.Error(), "23") {
		t.Fatalf(
			"runtime scoped validation error = %v, want archived project deadline mismatch",
			err,
		)
	}
	if err := owner.Exec(`
		ALTER TABLE outbox_deliveries
		DROP CONSTRAINT chk_outbox_delivery_status
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(`
		ALTER TABLE outbox_deliveries
		ADD CONSTRAINT chk_outbox_delivery_status CHECK (status IS NOT NULL)
	`).Error; err != nil {
		t.Fatal(err)
	}
	err = validateWebhookCredentialLifetimeCatalog(owner)
	if err == nil ||
		!strings.Contains(err.Error(), "chk_outbox_delivery_status") ||
		!strings.Contains(err.Error(), "definition") {
		t.Fatalf(
			"weakened same-name PostgreSQL constraint error = %v",
			err,
		)
	}
}

func assertPostgresWebhookCredentialForceRLS(t *testing.T, db *gorm.DB) {
	t.Helper()
	var rows []struct {
		Table    string `gorm:"column:table_name"`
		Enabled  bool   `gorm:"column:enabled"`
		Forced   bool   `gorm:"column:forced"`
		Policies int64  `gorm:"column:policies"`
	}
	if err := db.Raw(`
		SELECT
			table_state.relname AS table_name,
			table_state.relrowsecurity AS enabled,
			table_state.relforcerowsecurity AS forced,
			(
				SELECT COUNT(*)
				FROM pg_policies
				WHERE schemaname = CURRENT_SCHEMA()
				  AND tablename = table_state.relname
				  AND policyname = 'chronodesk_project_scope'
			) AS policies
		FROM pg_class AS table_state
		JOIN pg_namespace AS namespace
		  ON namespace.oid = table_state.relnamespace
		WHERE namespace.nspname = CURRENT_SCHEMA()
		  AND table_state.relname IN (
			'domain_events',
			'outbox_deliveries',
			'webhook_delivery_snapshots'
		  )
		ORDER BY table_state.relname ASC
	`).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("PostgreSQL webhook RLS rows=%d, want 3", len(rows))
	}
	for _, row := range rows {
		if !row.Enabled || !row.Forced || row.Policies != 1 {
			t.Fatalf("invalid PostgreSQL webhook RLS state: %+v", row)
		}
	}
}

func assertPostgresWebhookCredentialDeadlines(
	t *testing.T,
	db *gorm.DB,
	scope models.ProjectScope,
	want time.Time,
) {
	t.Helper()
	if err := WithProjectScopeTransaction(
		context.Background(),
		db,
		scope,
		func(tx *gorm.DB) error {
			var snapshot struct {
				Deadline time.Time `gorm:"column:credential_expires_at"`
			}
			if err := tx.Table("webhook_delivery_snapshots").
				Select("credential_expires_at").
				Take(&snapshot).Error; err != nil {
				return err
			}
			var delivery struct {
				Deadline time.Time `gorm:"column:expires_at"`
			}
			if err := tx.Table("outbox_deliveries").
				Select("expires_at").
				Take(&delivery).Error; err != nil {
				return err
			}
			if !snapshot.Deadline.Equal(want) ||
				!delivery.Deadline.Equal(want) {
				return fmt.Errorf(
					"deadlines snapshot=%s delivery=%s want=%s",
					snapshot.Deadline,
					delivery.Deadline,
					want,
				)
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
}
