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
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
		TranslateError: false,
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
	ownerPeer, err := gorm.Open(
		postgres.Open(ownerURL.String()),
		silentConfig,
	)
	if err != nil {
		t.Fatalf("open peer PostgreSQL migration owner: %v", err)
	}
	ownerPeerSQL, err := ownerPeer.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ownerPeerSQL.Close()
	})
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
			status VARCHAR(20) NOT NULL DEFAULT 'pending'
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
	projectCount := 100
	pairCount := 10_000
	nonWebhookCount := 100_000
	capacityQualification :=
		os.Getenv("CHRONODESK_TASK9A_CAPACITY_QUALIFICATION") == "1"
	if capacityQualification {
		projectCount = 1_000
		pairCount = 100_000
		nonWebhookCount = 1_000_000
	}
	seedPostgresWebhookCredentialScaleFixture(
		t,
		owner,
		firstCutoverPlaceholder(),
		projectCount,
		pairCount,
		nonWebhookCount,
	)
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

	scaleCutoverStarted := time.Now()
	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		owner,
		firstCutover,
	); err != nil {
		t.Fatalf("migrate FORCE-RLS legacy webhook credentials: %v", err)
	}
	t.Logf(
		"representative cutover: %d Projects, %d webhook pairs, "+
			"%d non-webhook deliveries in %s",
		projectCount,
		pairCount,
		nonWebhookCount,
		time.Since(scaleCutoverStarted),
	)
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
	runtimeValidationContext, cancelRuntimeValidation := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	runtimeValidationStarted := time.Now()
	if err := ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
		runtimeValidationContext,
		runtime,
	); err != nil {
		cancelRuntimeValidation()
		t.Fatalf("validate scoped runtime webhook credential data: %v", err)
	}
	cancelRuntimeValidation()
	t.Logf(
		"representative runtime gate: %d Projects, %d webhook pairs, "+
			"%d non-webhook deliveries in %s",
		projectCount,
		pairCount,
		nonWebhookCount,
		time.Since(runtimeValidationStarted),
	)
	if capacityQualification {
		var postgresVersion string
		if err := owner.Raw(
			"SHOW server_version",
		).Scan(&postgresVersion).Error; err != nil {
			t.Fatal(err)
		}
		var planRows []struct {
			Plan string `gorm:"column:QUERY PLAN"`
		}
		if err := WithProjectScopeTransaction(
			context.Background(),
			owner,
			models.ProjectScope{
				OrganizationID: 11,
				ProjectID:      100,
			},
			func(tx *gorm.DB) error {
				return tx.Raw(`
					EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
					SELECT delivery.id
					FROM outbox_deliveries AS delivery
					JOIN webhook_delivery_snapshots AS snapshot
					  ON delivery.destination_id =
						 'snapshot:' || snapshot.id
					 AND delivery.organization_id =
						 snapshot.organization_id
					 AND delivery.project_id = snapshot.project_id
					 AND delivery.event_id = snapshot.event_id
					WHERE delivery.organization_id = 11
					  AND delivery.project_id = 100
					  AND delivery.destination_type = 'webhook'
					LIMIT 1
				`).Scan(&planRows).Error
			},
		); err != nil {
			t.Fatal(err)
		}
		var waitingLocks int64
		if err := owner.Raw(`
			SELECT COUNT(*)
			FROM pg_locks
			WHERE NOT granted
		`).Scan(&waitingLocks).Error; err != nil {
			t.Fatal(err)
		}
		plan := make([]string, 0, len(planRows))
		for _, row := range planRows {
			plan = append(plan, row.Plan)
		}
		t.Logf(
			"capacity qualification PostgreSQL=%s waiting_locks=%d plan=%s",
			postgresVersion,
			waitingLocks,
			strings.Join(plan, " | "),
		)
	}
	const runtimeBarrierCallback = "test:task9a_runtime_repeatable_read_barrier"
	inventoryRead := make(chan struct{})
	writerCommitted := make(chan struct{})
	var inventoryOnce sync.Once
	if err := runtime.Callback().Query().After("gorm:query").Register(
		runtimeBarrierCallback,
		func(tx *gorm.DB) {
			if tx.Statement == nil || tx.Statement.Table != "projects" {
				return
			}
			inventoryOnce.Do(func() {
				close(inventoryRead)
				<-writerCommitted
			})
		},
	); err != nil {
		t.Fatal(err)
	}
	validationResult := make(chan error, 1)
	go func() {
		validationResult <- ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
			context.Background(),
			runtime,
		)
	}()
	select {
	case <-inventoryRead:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime validator did not establish its Project inventory snapshot")
	}
	barrierEventID := "00000000-0000-7000-8000-000000000931"
	barrierSnapshotID := "00000000-0000-7000-8000-000000000932"
	barrierDeliveryID := "00000000-0000-7000-8000-000000000933"
	if err := owner.Exec(`
		INSERT INTO projects (id, organization_id, status)
		VALUES (24, 11, 'active')
	`).Error; err != nil {
		t.Fatal(err)
	}
	barrierDeadline := persistedDeadline.Add(2 * time.Hour)
	if err := WithProjectScopeTransaction(
		context.Background(),
		owner,
		models.ProjectScope{OrganizationID: 11, ProjectID: 24},
		func(tx *gorm.DB) error {
			if err := tx.Exec(`
				INSERT INTO domain_events (id, organization_id, project_id)
				VALUES (?, 11, 24)
			`, barrierEventID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`
				INSERT INTO webhook_delivery_snapshots (
					id, created_at, organization_id, project_id, config_id,
					event_id, credential_expires_at
				) VALUES (?, ?, 11, 24, 79, ?, ?)
			`,
				barrierSnapshotID,
				firstCutover,
				barrierEventID,
				barrierDeadline,
			).Error; err != nil {
				return err
			}
			return tx.Exec(`
				INSERT INTO outbox_deliveries (
					id, organization_id, project_id, event_id,
					destination_type, destination_id, status, expires_at
				) VALUES (?, 11, 24, ?, 'webhook', ?, 'pending', ?)
			`,
				barrierDeliveryID,
				barrierEventID,
				"snapshot:"+barrierSnapshotID,
				barrierDeadline,
			).Error
		},
	); err != nil {
		t.Fatal(err)
	}
	close(writerCommitted)
	select {
	case err := <-validationResult:
		if err != nil {
			t.Fatalf(
				"repeatable-read runtime gate mixed a concurrent atomic pair: %v",
				err,
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("repeatable-read runtime gate did not complete")
	}
	if err := runtime.Callback().Query().Remove(
		runtimeBarrierCallback,
	); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatalf(
			"runtime gate rejected the committed concurrent Project/pair: %v",
			err,
		)
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
	exerciseFullLegacyRunMigrationsPostgres(
		t,
		owner,
		ownerPeer,
		firstCutover,
	)
	if err := owner.Exec(`
		ALTER TABLE outbox_deliveries
		ALTER COLUMN status DROP NOT NULL
	`).Error; err != nil {
		t.Fatal(err)
	}
	err = validateWebhookCredentialLifetimeCatalog(owner)
	if err == nil ||
		!strings.Contains(err.Error(), "outbox_deliveries.status") {
		t.Fatalf("PostgreSQL nullable status catalog error = %v", err)
	}
	var fullLegacyProject models.Project
	if err := owner.Where("id = ?", 1001).
		Take(&fullLegacyProject).Error; err != nil {
		t.Fatal(err)
	}
	nullStatusErr := WithProjectScopeTransaction(
		context.Background(),
		owner,
		fullLegacyProject.Scope(),
		func(tx *gorm.DB) error {
			return tx.Exec(`
				UPDATE outbox_deliveries
				SET status = NULL
				WHERE id = '00000000-0000-7000-8000-000000000923'
			`).Error
		},
	)
	if nullStatusErr == nil ||
		!strings.Contains(nullStatusErr.Error(), "SQLSTATE 23514") {
		t.Fatalf(
			"PostgreSQL status CHECK accepted raw NULL after nullability drift: %v",
			nullStatusErr,
		)
	}
	if err := owner.Exec(`
		ALTER TABLE outbox_deliveries
		ALTER COLUMN status SET NOT NULL
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(`
		ALTER TABLE webhook_delivery_snapshots
		ALTER COLUMN credential_shred_reason TYPE VARCHAR(21)
	`).Error; err != nil {
		t.Fatal(err)
	}
	err = validateWebhookCredentialLifetimeCatalog(owner)
	if err == nil ||
		!strings.Contains(err.Error(), "credential_shred_reason") {
		t.Fatalf("varchar(21) credential reason catalog error = %v", err)
	}
	if err := owner.Exec(`
		ALTER TABLE webhook_delivery_snapshots
		ALTER COLUMN credential_shred_reason TYPE VARCHAR(20)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(`
		ALTER TABLE outbox_deliveries
		ALTER COLUMN expired_at SET DEFAULT CURRENT_TIMESTAMP
	`).Error; err != nil {
		t.Fatal(err)
	}
	err = validateWebhookCredentialLifetimeCatalog(owner)
	if err == nil || !strings.Contains(err.Error(), "expired_at") {
		t.Fatalf("defaulted expired_at catalog error = %v", err)
	}
	if err := owner.Exec(`
		ALTER TABLE outbox_deliveries
		ALTER COLUMN expired_at DROP DEFAULT
	`).Error; err != nil {
		t.Fatal(err)
	}

	if err := owner.Exec(`
		ALTER TABLE webhook_delivery_snapshots
		DROP CONSTRAINT chk_webhook_snapshot_shred_state
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(`
		ALTER TABLE webhook_delivery_snapshots
		ADD CONSTRAINT chk_webhook_snapshot_shred_state CHECK (
			credential_shredded_at IS NULL
			AND (
				credential_shred_reason IS NULL
				OR (
					credential_shredded_at IS NOT NULL
					AND credential_shred_reason IS NOT NULL
					AND secret IS NOT NULL AND secret = ''
					AND previous_secret IS NOT NULL
					AND previous_secret = ''
					AND access_token IS NOT NULL AND access_token = ''
				)
			)
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	err = validateWebhookCredentialLifetimeCatalog(owner)
	if err == nil ||
		!strings.Contains(err.Error(), "chk_webhook_snapshot_shred_state") ||
		!strings.Contains(err.Error(), "definition") {
		t.Fatalf("regrouped PostgreSQL CHECK catalog error = %v", err)
	}
	if err := owner.Exec(`
		ALTER TABLE webhook_delivery_snapshots
		DROP CONSTRAINT chk_webhook_snapshot_shred_state
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(
		"ALTER TABLE webhook_delivery_snapshots ADD CONSTRAINT " +
			"chk_webhook_snapshot_shred_state CHECK (" +
			webhookCredentialConstraintDefinitions["chk_webhook_snapshot_shred_state"].expression + ")",
	).Error; err != nil {
		t.Fatal(err)
	}

	if err := owner.Exec(`
		ALTER TABLE outbox_deliveries
		DROP CONSTRAINT chk_outbox_expired_webhook
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(`
		ALTER TABLE webhook_delivery_snapshots
		ADD COLUMN status VARCHAR(20),
		ADD COLUMN destination_type VARCHAR(50),
		ADD CONSTRAINT chk_outbox_expired_webhook CHECK (
			status <> 'expired' OR
			(status = 'expired' AND destination_type = 'webhook')
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	err = validateWebhookCredentialLifetimeCatalog(owner)
	if err == nil ||
		!strings.Contains(err.Error(), "chk_outbox_expired_webhook") {
		t.Fatalf("wrong-table same-name CHECK catalog error = %v", err)
	}
	if err := owner.Exec(`
		ALTER TABLE webhook_delivery_snapshots
		DROP CONSTRAINT chk_outbox_expired_webhook,
		DROP COLUMN status,
		DROP COLUMN destination_type
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(
		"ALTER TABLE outbox_deliveries ADD CONSTRAINT " +
			"chk_outbox_expired_webhook CHECK (" +
			webhookCredentialConstraintDefinitions["chk_outbox_expired_webhook"].expression + ") NOT VALID",
	).Error; err != nil {
		t.Fatal(err)
	}
	err = validateWebhookCredentialLifetimeCatalog(owner)
	if err == nil ||
		!strings.Contains(err.Error(), "chk_outbox_expired_webhook") ||
		!strings.Contains(err.Error(), "catalog state") {
		t.Fatalf("NOT VALID PostgreSQL CHECK catalog error = %v", err)
	}
	if err := owner.Exec(`
		ALTER TABLE outbox_deliveries
		VALIDATE CONSTRAINT chk_outbox_expired_webhook
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := validateWebhookCredentialLifetimeCatalog(owner); err != nil {
		t.Fatalf("restore exact PostgreSQL credential catalog: %v", err)
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
	if err := owner.Exec(`
		ALTER TABLE outbox_deliveries
		DROP CONSTRAINT chk_outbox_delivery_status
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(
		"ALTER TABLE outbox_deliveries ADD CONSTRAINT " +
			"chk_outbox_delivery_status CHECK (" +
			webhookCredentialConstraintDefinitions["chk_outbox_delivery_status"].expression +
			")",
	).Error; err != nil {
		t.Fatal(err)
	}

	if err := owner.Exec(`
		ALTER TABLE outbox_deliveries
		ADD COLUMN "Status" VARCHAR(20) NOT NULL DEFAULT 'pending',
		DROP CONSTRAINT chk_outbox_delivery_status,
		ADD CONSTRAINT chk_outbox_delivery_status CHECK (
			"Status" IS NOT NULL AND (
				"Status" = 'pending' OR
				"Status" = 'processing' OR
				"Status" = 'succeeded' OR
				"Status" = 'failed' OR
				"Status" = 'dead' OR
				"Status" = 'expired'
			)
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	err = validateWebhookCredentialLifetimeCatalog(owner)
	if err == nil ||
		!strings.Contains(err.Error(), "chk_outbox_delivery_status") {
		t.Fatalf(`quoted "Status" CHECK catalog error = %v`, err)
	}
	if err := owner.Exec(`
		ALTER TABLE outbox_deliveries
		DROP CONSTRAINT chk_outbox_delivery_status,
		DROP COLUMN "Status"
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(
		"ALTER TABLE outbox_deliveries ADD CONSTRAINT " +
			"chk_outbox_delivery_status CHECK (" +
			webhookCredentialConstraintDefinitions["chk_outbox_delivery_status"].expression +
			")",
	).Error; err != nil {
		t.Fatal(err)
	}

	if err := withWebhookCredentialOwnerAccess(
		owner,
		func(tx *gorm.DB) error {
			if err := tx.Exec(`
				ALTER TABLE domain_events
				DROP CONSTRAINT fk_domain_events_project_scope,
				ADD COLUMN "Project_ID" BIGINT
			`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`
				UPDATE domain_events SET "Project_ID" = project_id
			`).Error; err != nil {
				return err
			}
			return tx.Exec(`
				ALTER TABLE domain_events
				ALTER COLUMN "Project_ID" SET NOT NULL,
				ADD CONSTRAINT fk_domain_events_project_scope
					FOREIGN KEY (organization_id, "Project_ID")
					REFERENCES projects(organization_id, id)
					ON UPDATE RESTRICT ON DELETE RESTRICT
			`).Error
		},
	); err != nil {
		t.Fatal(err)
	}
	valid, exists, stateErr := postgresWebhookProjectScopeFKState(
		owner,
		webhookProjectScopeFKDefinitions()[0],
	)
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if !exists || valid {
		t.Fatalf(
			`quoted "Project_ID" FK state = valid:%v exists:%v, want incompatible`,
			valid,
			exists,
		)
	}
	if err := owner.Exec(`
		ALTER TABLE domain_events
		DROP CONSTRAINT fk_domain_events_project_scope,
		DROP COLUMN "Project_ID",
		ADD CONSTRAINT fk_domain_events_project_scope
			FOREIGN KEY (organization_id, project_id)
			REFERENCES projects(organization_id, id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	`).Error; err != nil {
		t.Fatal(err)
	}

	if err := owner.Exec(`
		CREATE TABLE "Projects" (
			id BIGINT PRIMARY KEY,
			organization_id BIGINT NOT NULL,
			UNIQUE (organization_id, id)
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(`
		INSERT INTO "Projects" (id, organization_id)
		SELECT id, organization_id FROM projects
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(`
		ALTER TABLE domain_events
		DROP CONSTRAINT fk_domain_events_project_scope,
		ADD CONSTRAINT fk_domain_events_project_scope
			FOREIGN KEY (organization_id, project_id)
			REFERENCES "Projects"(organization_id, id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	`).Error; err != nil {
		t.Fatal(err)
	}
	valid, exists, stateErr = postgresWebhookProjectScopeFKState(
		owner,
		webhookProjectScopeFKDefinitions()[0],
	)
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if !exists || valid {
		t.Fatalf(
			`quoted "Projects" FK state = valid:%v exists:%v, want incompatible`,
			valid,
			exists,
		)
	}
	if err := owner.Exec(`
		ALTER TABLE domain_events
		DROP CONSTRAINT fk_domain_events_project_scope,
		ADD CONSTRAINT fk_domain_events_project_scope
			FOREIGN KEY (organization_id, project_id)
			REFERENCES projects(organization_id, id)
			ON UPDATE RESTRICT ON DELETE RESTRICT;
		DROP TABLE "Projects"
	`).Error; err != nil {
		t.Fatal(err)
	}
}

func exerciseFullLegacyRunMigrationsPostgres(
	t *testing.T,
	owner *gorm.DB,
	ownerPeer *gorm.DB,
	now time.Time,
) {
	t.Helper()
	if err := withWebhookCredentialOwnerAccess(
		owner,
		func(tx *gorm.DB) error {
			for _, table := range []string{
				"outbox_deliveries",
				"webhook_delivery_snapshots",
				"domain_events",
			} {
				if err := tx.Exec("DELETE FROM " + table).Error; err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		t.Fatalf("clear FORCE-RLS scale fixture: %v", err)
	}
	if err := owner.Exec("DELETE FROM projects").Error; err != nil {
		t.Fatalf("clear minimal Project fixture: %v", err)
	}
	dropPostgresProjectScopeForeignKeys(t, owner)
	if err := owner.Exec(
		"DELETE FROM schema_migration_checkpoints WHERE key = ?",
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Clauses(clause.OnConflict{DoNothing: true}).Create(
		&models.SchemaMigrationCheckpoint{
			Key:         projectScopeCutoverCheckpointKey,
			Version:     projectScopeCutoverCheckpointVersion,
			Checksum:    projectScopeCutoverCheckpointChecksum,
			CompletedAt: now,
		},
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(owner); err != nil {
		t.Fatalf("build complete PostgreSQL baseline before legacy downgrade: %v", err)
	}

	organization := models.Organization{
		Slug:   "task9a-full-legacy",
		Name:   "Task 9a full legacy",
		Status: models.OrganizationStatusActive,
	}
	if err := owner.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "TASK9A",
		Name:           "Task 9a",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := owner.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		ID:             1001,
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            models.ProjectKey("TASK9A"),
		Name:           "Task 9a legacy",
		Status:         models.ProjectStatusActive,
	}
	if err := owner.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	scope := project.Scope()
	eventID := "00000000-0000-7000-8000-000000000921"
	snapshotID := "00000000-0000-7000-8000-000000000922"
	deliveryID := "00000000-0000-7000-8000-000000000923"
	if err := WithProjectScopeTransaction(
		context.Background(),
		owner,
		scope,
		func(tx *gorm.DB) error {
			event := models.DomainEvent{
				ID:              eventID,
				OrganizationID:  scope.OrganizationID,
				ProjectID:       scope.ProjectID,
				SpecVersion:     "1.0",
				Source:          "/task9a/full-legacy",
				Type:            "io.chronodesk.task9a.fixture.v1",
				Time:            now,
				DataContentType: "application/json",
				Data:            []byte(`{}`),
				ActorType:       models.ActorTypeSystem,
				ActorID:         "task9a",
				ResourceVersion: 1,
			}
			if err := tx.Create(&event).Error; err != nil {
				return err
			}
			deadline := now.Add(models.WebhookDeliveryCredentialLifetime)
			snapshot := models.WebhookDeliverySnapshot{
				ID:                  snapshotID,
				CreatedAt:           now,
				OrganizationID:      scope.OrganizationID,
				ProjectID:           scope.ProjectID,
				ConfigID:            99,
				EventID:             eventID,
				ConfigUpdatedAt:     now,
				Provider:            models.WebhookProviderCustom,
				WebhookURL:          "https://example.invalid/task9a",
				Secret:              "sealed",
				CredentialExpiresAt: deadline,
				EnabledEvents:       `["io.chronodesk.task9a.fixture.v1"]`,
				MessageFormat:       "text",
				RetryCount:          1,
				RetryInterval:       5,
				TimeoutSeconds:      5,
				RateLimit:           1,
				RateLimitWindow:     60,
			}
			if err := tx.Create(&snapshot).Error; err != nil {
				return err
			}
			return tx.Create(&models.OutboxDelivery{
				ID:              deliveryID,
				OrganizationID:  scope.OrganizationID,
				ProjectID:       scope.ProjectID,
				EventID:         eventID,
				DestinationType: "webhook",
				DestinationID:   "snapshot:" + snapshotID,
				Status:          models.OutboxDeliveryPending,
				MaxAttempts:     1,
				NextAttemptAt:   now,
				ExpiresAt:       &deadline,
			}).Error
		},
	); err != nil {
		t.Fatal(err)
	}

	dropPostgresProjectScopeForeignKeys(t, owner)
	for _, statement := range []string{
		"ALTER TABLE webhook_delivery_snapshots " +
			"DROP COLUMN credential_expires_at CASCADE",
		"ALTER TABLE webhook_delivery_snapshots " +
			"DROP COLUMN credential_shredded_at CASCADE",
		"ALTER TABLE webhook_delivery_snapshots " +
			"DROP COLUMN credential_shred_reason CASCADE",
		"ALTER TABLE outbox_deliveries DROP COLUMN expires_at CASCADE",
		"ALTER TABLE outbox_deliveries DROP COLUMN expired_at CASCADE",
	} {
		if err := owner.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := owner.Exec(
		"DELETE FROM schema_migration_checkpoints WHERE key = ?",
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(`
		CREATE FUNCTION fail_task9a_backfill_fn()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			RAISE EXCEPTION 'injected task9a SQL backfill failure';
		END;
		$$
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(`
		CREATE TRIGGER fail_task9a_backfill
		BEFORE UPDATE ON outbox_deliveries
		FOR EACH ROW EXECUTE FUNCTION fail_task9a_backfill_fn()
	`).Error; err != nil {
		t.Fatal(err)
	}
	err := RunMigrations(owner)
	if err == nil ||
		!strings.Contains(err.Error(), "injected task9a SQL backfill failure") ||
		!strings.Contains(err.Error(), "SQLSTATE P0001") {
		t.Fatalf("real PostgreSQL backfill SQL error = %v", err)
	}
	assertPostgresWebhookCredentialForceRLS(t, owner)
	var failedCheckpointCount int64
	if err := owner.Model(&models.SchemaMigrationCheckpoint{}).
		Where("key = ?", webhookSnapshotCredentialLifetimeCheckpointKey).
		Count(&failedCheckpointCount).Error; err != nil {
		t.Fatal(err)
	}
	if failedCheckpointCount != 0 {
		t.Fatalf(
			"SQL-aborted cutover retained %d foundation checkpoints",
			failedCheckpointCount,
		)
	}
	if owner.Migrator().HasColumn(
		&models.WebhookDeliverySnapshot{},
		"credential_expires_at",
	) {
		t.Fatal("SQL-aborted cutover retained a half-applied deadline column")
	}
	if err := owner.Exec(
		"DROP TRIGGER fail_task9a_backfill ON outbox_deliveries",
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec("DROP FUNCTION fail_task9a_backfill_fn()").Error; err != nil {
		t.Fatal(err)
	}

	if err := owner.Exec(`
		CREATE FUNCTION fail_task9a_snapshot_backfill_fn()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			RAISE EXCEPTION 'injected task9a snapshot SQL backfill failure';
		END;
		$$
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(`
		CREATE TRIGGER fail_task9a_snapshot_backfill
		BEFORE UPDATE ON webhook_delivery_snapshots
		FOR EACH ROW EXECUTE FUNCTION fail_task9a_snapshot_backfill_fn()
	`).Error; err != nil {
		t.Fatal(err)
	}
	err = RunMigrations(owner)
	if err == nil ||
		!strings.Contains(
			err.Error(),
			"injected task9a snapshot SQL backfill failure",
		) ||
		!strings.Contains(err.Error(), "SQLSTATE P0001") {
		t.Fatalf("second PostgreSQL backfill SQL error = %v", err)
	}
	assertPostgresWebhookCredentialCutoverRolledBack(t, owner)
	if err := owner.Exec(
		"DROP TRIGGER fail_task9a_snapshot_backfill " +
			"ON webhook_delivery_snapshots",
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(
		"DROP FUNCTION fail_task9a_snapshot_backfill_fn()",
	).Error; err != nil {
		t.Fatal(err)
	}

	if err := owner.Exec(`
		ALTER TABLE outbox_deliveries
		DROP CONSTRAINT IF EXISTS chk_outbox_delivery_status
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := WithProjectScopeTransaction(
		context.Background(),
		owner,
		scope,
		func(tx *gorm.DB) error {
			return tx.Exec(`
				INSERT INTO outbox_deliveries (
					id, organization_id, project_id, event_id,
					destination_type, destination_id, status,
					next_attempt_at
				) VALUES (
					'00000000-0000-7000-8000-000000000924',
					?, ?, ?, 'test_delivery', 'constraint-abort', 'mystery', ?
				)
			`,
				scope.OrganizationID,
				scope.ProjectID,
				eventID,
				now,
			).Error
		},
	); err != nil {
		t.Fatal(err)
	}
	err = RunMigrations(owner)
	if err == nil ||
		!strings.Contains(
			err.Error(),
			"validate PostgreSQL webhook credential constraint chk_outbox_delivery_status",
		) ||
		!strings.Contains(err.Error(), "SQLSTATE 23514") {
		t.Fatalf("PostgreSQL constraint validation SQL error = %v", err)
	}
	assertPostgresWebhookCredentialCutoverRolledBack(t, owner)
	if err := WithProjectScopeTransaction(
		context.Background(),
		owner,
		scope,
		func(tx *gorm.DB) error {
			return tx.Exec(`
				DELETE FROM outbox_deliveries
				WHERE id = '00000000-0000-7000-8000-000000000924'
			`).Error
		},
	); err != nil {
		t.Fatal(err)
	}

	if err := owner.Exec(`
		CREATE FUNCTION fail_task9a_checkpoint_insert_fn()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			IF NEW.key = '20260810_webhook_snapshot_credential_lifetime_v1' THEN
				RAISE EXCEPTION 'injected task9a checkpoint SQL insert failure';
			END IF;
			RETURN NEW;
		END;
		$$
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(`
		CREATE TRIGGER fail_task9a_checkpoint_insert
		BEFORE INSERT ON schema_migration_checkpoints
		FOR EACH ROW EXECUTE FUNCTION fail_task9a_checkpoint_insert_fn()
	`).Error; err != nil {
		t.Fatal(err)
	}
	err = RunMigrations(owner)
	if err == nil ||
		!strings.Contains(
			err.Error(),
			"injected task9a checkpoint SQL insert failure",
		) ||
		!strings.Contains(err.Error(), "SQLSTATE P0001") {
		t.Fatalf("PostgreSQL checkpoint INSERT SQL error = %v", err)
	}
	assertPostgresWebhookCredentialCutoverRolledBack(t, owner)
	if err := owner.Exec(`
		DROP TRIGGER fail_task9a_checkpoint_insert
		ON schema_migration_checkpoints
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(
		"DROP FUNCTION fail_task9a_checkpoint_insert_fn()",
	).Error; err != nil {
		t.Fatal(err)
	}

	const concurrentCheckpointBarrier = "test:task9a_concurrent_checkpoint_barrier"
	checkpointReached := make(chan struct{})
	releaseCheckpoint := make(chan struct{})
	var checkpointOnce sync.Once
	if err := owner.Callback().Create().Before("gorm:create").Register(
		concurrentCheckpointBarrier,
		func(tx *gorm.DB) {
			checkpoint, ok := tx.Statement.Dest.(*models.SchemaMigrationCheckpoint)
			if !ok ||
				checkpoint.Key !=
					webhookSnapshotCredentialLifetimeCheckpointKey {
				return
			}
			checkpointOnce.Do(func() {
				close(checkpointReached)
				<-releaseCheckpoint
			})
		},
	); err != nil {
		t.Fatal(err)
	}
	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)
	go func() {
		firstResult <- RunMigrations(owner)
	}()
	select {
	case <-checkpointReached:
	case <-time.After(10 * time.Second):
		t.Fatal("first migration did not reach the foundation checkpoint barrier")
	}
	go func() {
		secondResult <- RunMigrations(ownerPeer)
	}()
	select {
	case err := <-secondResult:
		t.Fatalf(
			"peer migration bypassed the session advisory lock: %v",
			err,
		)
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseCheckpoint)
	if err := <-firstResult; err != nil {
		t.Fatalf("first concurrent PostgreSQL migration: %v", err)
	}
	if err := <-secondResult; err != nil {
		t.Fatalf("serialized peer PostgreSQL migration: %v", err)
	}
	if err := owner.Callback().Create().Remove(
		concurrentCheckpointBarrier,
	); err != nil {
		t.Fatal(err)
	}
	var checkpoint models.SchemaMigrationCheckpoint
	if err := owner.Where(
		"key = ?",
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Take(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	wantDeadline := checkpoint.CompletedAt.Add(
		models.WebhookDeliveryCredentialLifetime,
	)
	assertPostgresWebhookCredentialDeadlines(t, owner, scope, wantDeadline)
	if err := owner.Exec(`
		CREATE FUNCTION reject_task9a_deadline_rewrite_fn()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			RAISE EXCEPTION 'task9a deadline rewrite';
		END;
		$$
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(`
		CREATE TRIGGER reject_task9a_deadline_rewrite
		BEFORE UPDATE OF credential_expires_at
		ON webhook_delivery_snapshots
		FOR EACH ROW EXECUTE FUNCTION reject_task9a_deadline_rewrite_fn()
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(owner); err != nil {
		t.Fatalf("rerun complete PostgreSQL migration without backfill: %v", err)
	}
	if err := owner.Exec(
		"DROP TRIGGER reject_task9a_deadline_rewrite " +
			"ON webhook_delivery_snapshots",
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(
		"DROP FUNCTION reject_task9a_deadline_rewrite_fn()",
	).Error; err != nil {
		t.Fatal(err)
	}
	assertPostgresWebhookCredentialDeadlines(t, owner, scope, wantDeadline)
}

func firstCutoverPlaceholder() time.Time {
	return time.Date(2026, 8, 1, 1, 2, 3, 0, time.UTC)
}

func seedPostgresWebhookCredentialScaleFixture(
	t *testing.T,
	db *gorm.DB,
	createdAt time.Time,
	projectCount int,
	pairCount int,
	nonWebhookCount int,
) {
	t.Helper()
	statements := []struct {
		sql  string
		args []any
	}{
		{
			sql: `
				INSERT INTO projects (id, organization_id, status)
				SELECT value, 11, 'active'
				FROM generate_series(100, ?) AS value
			`,
			args: []any{99 + projectCount},
		},
		{
			sql: `
				INSERT INTO domain_events (
					id, organization_id, project_id
				)
				SELECT
					'00000000-0000-7000-8000-' ||
						lpad((100000 + value)::text, 12, '0'),
					11,
					100 + ((value - 1) % ?)
				FROM generate_series(1, ?) AS value
			`,
			args: []any{projectCount, pairCount},
		},
		{
			sql: `
				INSERT INTO webhook_delivery_snapshots (
					id, created_at, organization_id, project_id, config_id,
					event_id, secret, previous_secret, access_token
				)
				SELECT
					'00000000-0000-7000-8000-' ||
						lpad((200000 + value)::text, 12, '0'),
					?,
					11,
					100 + ((value - 1) % ?),
					1000 + value,
					'00000000-0000-7000-8000-' ||
						lpad((100000 + value)::text, 12, '0'),
					'sealed',
					'',
					''
				FROM generate_series(1, ?) AS value
			`,
			args: []any{
				createdAt,
				projectCount,
				pairCount,
			},
		},
		{
			sql: `
				INSERT INTO outbox_deliveries (
					id, organization_id, project_id, event_id,
					destination_type, destination_id, status
				)
				SELECT
					'00000000-0000-7000-8000-' ||
						lpad((300000 + value)::text, 12, '0'),
					11,
					100 + ((value - 1) % ?),
					'00000000-0000-7000-8000-' ||
						lpad((100000 + value)::text, 12, '0'),
					'webhook',
					'snapshot:00000000-0000-7000-8000-' ||
						lpad((200000 + value)::text, 12, '0'),
					'pending'
				FROM generate_series(1, ?) AS value
			`,
			args: []any{projectCount, pairCount},
		},
		{
			sql: `
				INSERT INTO outbox_deliveries (
					id, organization_id, project_id, event_id,
					destination_type, destination_id, status
				)
				SELECT
					'00000000-0000-7000-8000-' ||
						lpad((400000 + value)::text, 12, '0'),
					11,
					100 + ((value - 1) % ?),
					'00000000-0000-7000-8000-' ||
						lpad(
							(100000 + (((value - 1) % ?) + 1))::text,
							12,
							'0'
						),
					'test_delivery',
					'scale:' || value::text,
					'pending'
				FROM generate_series(1, ?) AS value
			`,
			args: []any{
				projectCount,
				projectCount,
				nonWebhookCount,
			},
		},
	}
	for _, statement := range statements {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func dropPostgresProjectScopeForeignKeys(t *testing.T, db *gorm.DB) {
	t.Helper()
	var rows []struct {
		Table string `gorm:"column:table_name"`
		Name  string `gorm:"column:constraint_name"`
	}
	if err := db.Raw(`
		SELECT
			table_state.relname AS table_name,
			constraint_state.conname AS constraint_name
		FROM pg_constraint AS constraint_state
		JOIN pg_class AS table_state
		  ON table_state.oid = constraint_state.conrelid
		JOIN pg_namespace AS namespace
		  ON namespace.oid = table_state.relnamespace
		WHERE namespace.nspname = CURRENT_SCHEMA()
		  AND constraint_state.contype = 'f'
		  AND constraint_state.confrelid = 'projects'::regclass
		ORDER BY table_state.relname, constraint_state.conname
	`).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if err := db.Exec(
			"ALTER TABLE " + quotePostgresRLSTestIdentifier(row.Table) +
				" DROP CONSTRAINT " +
				quotePostgresRLSTestIdentifier(row.Name),
		).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func assertPostgresWebhookCredentialCutoverRolledBack(
	t *testing.T,
	db *gorm.DB,
) {
	t.Helper()
	assertPostgresWebhookCredentialForceRLS(t, db)
	var checkpointCount int64
	if err := db.Model(&models.SchemaMigrationCheckpoint{}).
		Where("key = ?", webhookSnapshotCredentialLifetimeCheckpointKey).
		Count(&checkpointCount).Error; err != nil {
		t.Fatal(err)
	}
	if checkpointCount != 0 {
		t.Fatalf(
			"SQL-aborted cutover retained %d foundation checkpoints",
			checkpointCount,
		)
	}
	for _, column := range []string{
		"credential_expires_at",
		"credential_shredded_at",
		"credential_shred_reason",
	} {
		if db.Migrator().HasColumn(
			&models.WebhookDeliverySnapshot{},
			column,
		) {
			t.Fatalf(
				"SQL-aborted cutover retained webhook snapshot column %s",
				column,
			)
		}
	}
	for _, column := range []string{"expires_at", "expired_at"} {
		if db.Migrator().HasColumn(
			&models.OutboxDelivery{},
			column,
		) {
			t.Fatalf(
				"SQL-aborted cutover retained Outbox column %s",
				column,
			)
		}
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
