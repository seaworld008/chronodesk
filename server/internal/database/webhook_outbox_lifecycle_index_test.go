package database

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestWebhookOutboxLifecycleIndexesAreIdempotentAndExact(t *testing.T) {
	db := openWebhookOutboxLifecycleIndexTestDB(t, "exact")
	createWebhookOutboxLifecycleIndexTestTables(t, db)

	for attempt := 0; attempt < 2; attempt++ {
		if err := MigrateWebhookOutboxLifecycleIndexes(db); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt+1, err)
		}
	}
	if err := ValidateWebhookOutboxLifecycleIndexes(db); err != nil {
		t.Fatalf("validate lifecycle indexes: %v", err)
	}
	for _, definition := range webhookOutboxLifecycleIndexDefinitions {
		if !db.Migrator().HasIndex(definition.table, definition.name) {
			t.Errorf("lifecycle index %s is missing", definition.name)
		}
	}
}

func TestWebhookOutboxLifecycleIndexGateRejectsPhysicalDrift(t *testing.T) {
	for _, test := range []struct {
		name     string
		indexSQL string
	}{
		{
			name: "wrong order",
			indexSQL: `
				CREATE INDEX idx_outbox_lifecycle_claim
				ON outbox_deliveries (
					project_id, organization_id,
					next_attempt_at, created_at, id
				)
				WHERE destination_type <> 'webhook'
				  AND status IN ('pending', 'failed')
			`,
		},
		{
			name: "wrong predicate",
			indexSQL: `
				CREATE INDEX idx_outbox_lifecycle_claim
				ON outbox_deliveries (
					organization_id, project_id,
					next_attempt_at, created_at, id
				)
				WHERE destination_type <> 'webhook'
				  AND status = 'pending'
			`,
		},
		{
			name: "expression",
			indexSQL: `
				CREATE INDEX idx_outbox_lifecycle_claim
				ON outbox_deliveries (
					organization_id, project_id,
					next_attempt_at, created_at, (id || '')
				)
				WHERE destination_type <> 'webhook'
				  AND status IN ('pending', 'failed')
			`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openWebhookOutboxLifecycleIndexTestDB(
				t,
				"drift-"+strings.ReplaceAll(test.name, " ", "-"),
			)
			createWebhookOutboxLifecycleIndexTestTables(t, db)
			if err := testDBExec(db, test.indexSQL); err != nil {
				t.Fatal(err)
			}
			err := ValidateWebhookOutboxLifecycleIndexes(db)
			if err == nil ||
				!strings.Contains(err.Error(), "idx_outbox_lifecycle_claim") {
				t.Fatalf("runtime gate error = %v, want exact claim index", err)
			}
			if err := MigrateWebhookOutboxLifecycleIndexes(db); err != nil {
				t.Fatalf("repair lifecycle index: %v", err)
			}
			if err := ValidateWebhookOutboxLifecycleIndexes(db); err != nil {
				t.Fatalf("validate repaired lifecycle indexes: %v", err)
			}
		})
	}
}

func TestRunMigrationsInstallsWebhookOutboxLifecycleIndexes(t *testing.T) {
	db := openWebhookOutboxLifecycleIndexTestDB(t, "run-migrations")
	if err := RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWebhookOutboxLifecycleIndexes(db); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWebhookOutboxLifecycleFence(db); err != nil {
		t.Fatal(err)
	}
	var before models.SchemaMigrationCheckpoint
	if err := db.First(
		&before,
		"key = ?",
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateWebhookOutboxLifecycleFence(db); err != nil {
		t.Fatal(err)
	}
	if err := MigrateWebhookOutboxLifecycleIndexes(db); err != nil {
		t.Fatal(err)
	}
	var after models.SchemaMigrationCheckpoint
	if err := db.First(
		&after,
		"key = ?",
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Error; err != nil {
		t.Fatal(err)
	}
	if before.Version != after.Version ||
		before.Checksum != after.Checksum ||
		!before.CompletedAt.Equal(after.CompletedAt) {
		t.Fatalf(
			"lifecycle migration changed foundation checkpoint before=%+v after=%+v",
			before,
			after,
		)
	}
	if err := db.Exec(
		"DROP INDEX " + webhookOutboxLifecycleClaimIndex,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := validateRuntimeSchema(db, false); err == nil ||
		!strings.Contains(err.Error(), webhookOutboxLifecycleClaimIndex) {
		t.Fatalf(
			"foundation-independent runtime schema gate error = %v, want missing claim index",
			err,
		)
	}
}

func openWebhookOutboxLifecycleIndexTestDB(
	t *testing.T,
	suffix string,
) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:webhook-outbox-lifecycle-index-%s-%d?mode=memory&cache=shared&_foreign_keys=1",
		suffix,
		time.Now().UnixNano(),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func createWebhookOutboxLifecycleIndexTestTables(
	t *testing.T,
	db *gorm.DB,
) {
	t.Helper()
	if err := testDBExec(db, `
		CREATE TABLE outbox_deliveries (
			id TEXT PRIMARY KEY,
			created_at DATETIME NOT NULL,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			status TEXT NOT NULL,
			next_attempt_at DATETIME NOT NULL,
			expires_at DATETIME,
			locked_at DATETIME,
			destination_type TEXT NOT NULL,
			destination_id TEXT NOT NULL
		);
		CREATE TABLE webhook_delivery_snapshots (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			credential_shredded_at DATETIME,
			previous_secret_expires_at DATETIME
		);
	`); err != nil {
		t.Fatal(err)
	}
}

func testDBExec(db *gorm.DB, statements string) error {
	for _, statement := range strings.Split(statements, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}
