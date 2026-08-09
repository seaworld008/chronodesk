package database

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const (
	legacyWebhookEventID    = "00000000-0000-7000-8000-000000000901"
	legacyWebhookSnapshotID = "00000000-0000-7000-8000-000000000902"
	legacyWebhookDeliveryID = "00000000-0000-7000-8000-000000000903"
)

func TestWebhookCredentialLifetimeMigrationAnchorsLegacyGraceOnce(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "checkpoint")
	seedLegacyWebhookCredentialPair(t, db)
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
	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		firstCutover,
	); err != nil {
		t.Fatalf("first credential lifetime migration: %v", err)
	}

	var checkpoint models.SchemaMigrationCheckpoint
	if err := db.Where(
		"key = ?",
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Take(&checkpoint).Error; err != nil {
		t.Fatalf("load credential lifetime checkpoint: %v", err)
	}
	if !checkpoint.CompletedAt.Equal(firstCutover) {
		t.Fatalf(
			"checkpoint = %s, want %s",
			checkpoint.CompletedAt,
			firstCutover,
		)
	}
	wantDeadline := checkpoint.CompletedAt.Add(
		models.WebhookDeliveryCredentialLifetime,
	)
	assertLegacyWebhookCredentialDeadline(t, db, wantDeadline)
	mustExecWebhookCredentialTest(
		t,
		db,
		`CREATE TRIGGER reject_snapshot_deadline_rewrite
		 BEFORE UPDATE OF credential_expires_at
		 ON webhook_delivery_snapshots
		 BEGIN
		   SELECT RAISE(ABORT, 'snapshot deadline rewrite');
		 END`,
	)
	mustExecWebhookCredentialTest(
		t,
		db,
		`CREATE TRIGGER reject_delivery_deadline_rewrite
		 BEFORE UPDATE OF expires_at
		 ON outbox_deliveries
		 BEGIN
		   SELECT RAISE(ABORT, 'delivery deadline rewrite');
		 END`,
	)

	laterClock := firstCutover.Add(30 * 24 * time.Hour)
	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		laterClock,
	); err != nil {
		t.Fatalf("rerun credential lifetime migration: %v", err)
	}
	assertLegacyWebhookCredentialDeadline(t, db, wantDeadline)
	if err := db.Where(
		"key = ?",
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Take(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	if !checkpoint.CompletedAt.Equal(firstCutover) {
		t.Fatalf(
			"migration rerun extended checkpoint to %s",
			checkpoint.CompletedAt,
		)
	}
}

func TestWebhookCredentialLifetimeMigrationRejectsMalformedLegacyPairs(
	t *testing.T,
) {
	tests := []struct {
		name    string
		mutate  func(*testing.T, *gorm.DB)
		wantErr string
	}{
		{
			name: "missing delivery",
			mutate: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				if err := db.Exec(
					"DELETE FROM outbox_deliveries WHERE id = ?",
					legacyWebhookDeliveryID,
				).Error; err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "missing",
		},
		{
			name: "delivery references missing snapshot",
			mutate: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				if err := db.Exec(`
					UPDATE outbox_deliveries
					SET destination_id = ?
					WHERE id = ?
				`,
					"snapshot:00000000-0000-7000-8000-000000000999",
					legacyWebhookDeliveryID,
				).Error; err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "missing snapshot",
		},
		{
			name: "cross scope",
			mutate: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				if err := db.Exec(`
					UPDATE outbox_deliveries
					SET project_id = project_id + 1
					WHERE id = ?
				`, legacyWebhookDeliveryID).Error; err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "scope",
		},
		{
			name: "cross event",
			mutate: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				otherEventID := "00000000-0000-7000-8000-000000000904"
				if err := db.Exec(`
					INSERT INTO domain_events (id, organization_id, project_id)
					VALUES (?, 11, 22)
				`, otherEventID).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Exec(`
					UPDATE outbox_deliveries
					SET event_id = ?
					WHERE id = ?
				`, otherEventID, legacyWebhookDeliveryID).Error; err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "event",
		},
		{
			name: "duplicate delivery",
			mutate: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				if err := db.Exec(`
					INSERT INTO outbox_deliveries (
						id, organization_id, project_id, event_id,
						destination_type, destination_id, status
					) VALUES (?, 11, 22, ?, 'webhook', ?, 'pending')
				`,
					"00000000-0000-7000-8000-000000000905",
					legacyWebhookEventID,
					"snapshot:"+legacyWebhookSnapshotID,
				).Error; err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "duplicate",
		},
		{
			name: "malformed destination",
			mutate: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				if err := db.Exec(`
					UPDATE outbox_deliveries
					SET destination_id = 'configured'
					WHERE id = ?
				`, legacyWebhookDeliveryID).Error; err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "malformed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openLegacyWebhookCredentialMigrationDB(t, test.name)
			seedLegacyWebhookCredentialPair(t, db)
			test.mutate(t, db)
			err := migrateWebhookSnapshotCredentialLifetimeContractAt(
				db,
				time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
			)
			if err == nil ||
				!strings.Contains(
					strings.ToLower(err.Error()),
					test.wantErr,
				) {
				t.Fatalf(
					"migration error = %v, want %q",
					err,
					test.wantErr,
				)
			}
		})
	}
}

func TestWebhookCredentialLifetimeMigrationRollsBackBackfillWithCheckpoint(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "checkpoint-rollback")
	seedLegacyWebhookCredentialPair(t, db)
	const callbackName = "test:fail_webhook_lifetime_checkpoint"
	if err := db.Callback().Create().Before("gorm:create").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement != nil &&
				tx.Statement.Table == "schema_migration_checkpoints" {
				_ = tx.AddError(errors.New("injected checkpoint failure"))
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	firstCutover := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		firstCutover,
	)
	if err == nil ||
		!strings.Contains(err.Error(), "injected checkpoint failure") {
		t.Fatalf("migration error = %v, want injected checkpoint failure", err)
	}
	if err := db.Callback().Create().Remove(callbackName); err != nil {
		t.Fatal(err)
	}
	var checkpointCount int64
	if err := db.Model(&models.SchemaMigrationCheckpoint{}).
		Where("key = ?", webhookSnapshotCredentialLifetimeCheckpointKey).
		Count(&checkpointCount).Error; err != nil {
		t.Fatal(err)
	}
	if checkpointCount != 0 {
		t.Fatalf("failed migration committed %d checkpoints", checkpointCount)
	}
	for _, required := range []struct {
		table  string
		column string
	}{
		{"webhook_delivery_snapshots", "credential_expires_at"},
		{"outbox_deliveries", "expires_at"},
	} {
		present, columnErr := hasExactDatabaseColumn(
			db,
			required.table,
			required.column,
		)
		if columnErr != nil {
			t.Fatal(columnErr)
		}
		if present {
			t.Fatalf(
				"failed migration retained half-applied column %s.%s",
				required.table,
				required.column,
			)
		}
	}

	laterCutover := firstCutover.Add(48 * time.Hour)
	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		laterCutover,
	); err != nil {
		t.Fatalf("retry migration after rollback: %v", err)
	}
	assertLegacyWebhookCredentialDeadline(
		t,
		db,
		laterCutover.Add(models.WebhookDeliveryCredentialLifetime),
	)
}

func TestRunMigrationsRecoversFromFreshSQLitePostCutoverFailure(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open(
			"file:"+strings.ReplaceAll(t.Name(), "/", "-")+
				"?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	const callbackName = "test:fail_fresh_sqlite_post_cutover"
	if err := db.Callback().Create().Before("gorm:create").Register(
		callbackName,
		func(tx *gorm.DB) {
			checkpoint, ok := tx.Statement.Dest.(*models.SchemaMigrationCheckpoint)
			if ok &&
				checkpoint.Key ==
					webhookSnapshotCredentialLifetimeCheckpointKey {
				_ = tx.AddError(
					errors.New("injected fresh SQLite post-cutover failure"),
				)
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	err = RunMigrations(db)
	if err == nil ||
		!strings.Contains(
			err.Error(),
			"injected fresh SQLite post-cutover failure",
		) {
		t.Fatalf("fresh SQLite post-cutover error = %v", err)
	}
	if err := ValidateRuntimeSchema(db); err == nil {
		t.Fatalf(
			"runtime schema accepted failed fresh post-cutover: %v",
			err,
		)
	}
	if err := db.Callback().Create().Remove(callbackName); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("recover fresh SQLite post-cutover: %v", err)
	}
	var checkpoint models.SchemaMigrationCheckpoint
	if err := db.Where(
		"key = ?",
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Take(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("rerun recovered fresh SQLite migration: %v", err)
	}
	var rerun models.SchemaMigrationCheckpoint
	if err := db.Where(
		"key = ?",
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Take(&rerun).Error; err != nil {
		t.Fatal(err)
	}
	if !rerun.CompletedAt.Equal(checkpoint.CompletedAt) {
		t.Fatalf(
			"fresh SQLite rerun changed checkpoint from %s to %s",
			checkpoint.CompletedAt,
			rerun.CompletedAt,
		)
	}
}

func TestWebhookCredentialLifetimeMigrationRejectsExistingDeadlineMismatch(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "mismatch")
	seedLegacyWebhookCredentialPair(t, db)
	if err := prepareWebhookSnapshotCredentialLifetimeColumns(db); err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 8, 17, 1, 2, 3, 0, time.UTC)
	if err := db.Exec(`
		UPDATE webhook_delivery_snapshots
		SET credential_expires_at = ?
		WHERE id = ?
	`, first, legacyWebhookSnapshotID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		UPDATE outbox_deliveries
		SET expires_at = ?
		WHERE id = ?
	`, first.Add(time.Second), legacyWebhookDeliveryID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.SchemaMigrationCheckpoint{
		Key:         webhookSnapshotCredentialLifetimeCheckpointKey,
		Version:     webhookSnapshotCredentialLifetimeCheckpointVersion,
		Checksum:    webhookSnapshotCredentialLifetimeCheckpointChecksum,
		CompletedAt: time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	}).Error; err != nil {
		t.Fatal(err)
	}
	err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "deadline") {
		t.Fatalf("migration error = %v, want deadline mismatch", err)
	}
}

func TestWebhookCredentialLifetimeMigrationRejectsLifetimeDataWithoutCheckpoint(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "data-without-checkpoint")
	seedLegacyWebhookCredentialPair(t, db)
	if err := prepareWebhookSnapshotCredentialLifetimeColumns(db); err != nil {
		t.Fatal(err)
	}
	existingDeadline := time.Date(2026, 8, 17, 1, 2, 3, 0, time.UTC)
	mustExecWebhookCredentialTest(
		t,
		db,
		`UPDATE webhook_delivery_snapshots
		 SET credential_expires_at = ?
		 WHERE id = ?`,
		existingDeadline,
		legacyWebhookSnapshotID,
	)
	mustExecWebhookCredentialTest(
		t,
		db,
		`UPDATE outbox_deliveries SET expires_at = ? WHERE id = ?`,
		existingDeadline,
		legacyWebhookDeliveryID,
	)
	err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	)
	if err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "checkpoint") {
		t.Fatalf(
			"migration error = %v, want pre-checkpoint lifetime-data rejection",
			err,
		)
	}
}

func TestWebhookCredentialLifetimeMigrationDoesNotRepairNullAfterCheckpoint(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "null-after-checkpoint")
	seedLegacyWebhookCredentialPair(t, db)
	if err := prepareWebhookSnapshotCredentialLifetimeColumns(db); err != nil {
		t.Fatal(err)
	}
	firstCutover := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	if err := db.Create(&models.SchemaMigrationCheckpoint{
		Key:         webhookSnapshotCredentialLifetimeCheckpointKey,
		Version:     webhookSnapshotCredentialLifetimeCheckpointVersion,
		Checksum:    webhookSnapshotCredentialLifetimeCheckpointChecksum,
		CompletedAt: firstCutover,
	}).Error; err != nil {
		t.Fatal(err)
	}
	err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		firstCutover.Add(30*24*time.Hour),
	)
	if err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "deadline") {
		t.Fatalf(
			"migration error = %v, want null deadline fail-closed",
			err,
		)
	}
	var row struct {
		Deadline *time.Time `gorm:"column:credential_expires_at"`
	}
	if err := db.Table("webhook_delivery_snapshots").
		Select("credential_expires_at").
		Where("id = ?", legacyWebhookSnapshotID).
		Scan(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Deadline != nil {
		t.Fatalf(
			"checkpoint rerun silently repaired deadline to %s",
			row.Deadline,
		)
	}
}

func TestValidateWebhookCredentialLifetimeContractFailsClosedOnBadData(
	t *testing.T,
) {
	tests := []struct {
		name    string
		mutate  func(*testing.T, *gorm.DB)
		wantErr string
	}{
		{
			name: "invalid status",
			mutate: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				mustExecWebhookCredentialTest(
					t,
					db,
					`UPDATE outbox_deliveries SET status = 'mystery' WHERE id = ?`,
					legacyWebhookDeliveryID,
				)
			},
			wantErr: "status",
		},
		{
			name: "invalid shred reason",
			mutate: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				mustExecWebhookCredentialTest(
					t,
					db,
					`UPDATE webhook_delivery_snapshots
					 SET credential_shredded_at = ?, credential_shred_reason = 'mystery'
					 WHERE id = ?`,
					time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC),
					legacyWebhookSnapshotID,
				)
			},
			wantErr: "shred reason",
		},
		{
			name: "shredded secret remains",
			mutate: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				mustExecWebhookCredentialTest(
					t,
					db,
					`UPDATE webhook_delivery_snapshots
					 SET credential_shredded_at = ?,
					     credential_shred_reason = 'succeeded',
					     secret = 'sealed-secret'
					 WHERE id = ?`,
					time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC),
					legacyWebhookSnapshotID,
				)
			},
			wantErr: "credential envelope",
		},
		{
			name: "deadline mismatch",
			mutate: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				mustExecWebhookCredentialTest(
					t,
					db,
					`UPDATE outbox_deliveries
					 SET expires_at = ?
					 WHERE id = ?`,
					time.Date(2026, 8, 18, 1, 2, 3, 0, time.UTC),
					legacyWebhookDeliveryID,
				)
			},
			wantErr: "deadline",
		},
		{
			name: "missing webhook deadline",
			mutate: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				mustExecWebhookCredentialTest(
					t,
					db,
					`UPDATE outbox_deliveries SET expires_at = NULL WHERE id = ?`,
					legacyWebhookDeliveryID,
				)
			},
			wantErr: "deadline",
		},
		{
			name: "cross scope",
			mutate: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				mustExecWebhookCredentialTest(
					t,
					db,
					`UPDATE outbox_deliveries SET project_id = 23 WHERE id = ?`,
					legacyWebhookDeliveryID,
				)
			},
			wantErr: "scope",
		},
		{
			name: "cross event",
			mutate: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				otherEventID := "00000000-0000-7000-8000-000000000906"
				mustExecWebhookCredentialTest(
					t,
					db,
					`INSERT INTO domain_events (id, organization_id, project_id)
					 VALUES (?, 11, 22)`,
					otherEventID,
				)
				mustExecWebhookCredentialTest(
					t,
					db,
					`UPDATE outbox_deliveries SET event_id = ? WHERE id = ?`,
					otherEventID,
					legacyWebhookDeliveryID,
				)
			},
			wantErr: "event",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openLegacyWebhookCredentialMigrationDB(t, "validate-"+test.name)
			seedLegacyWebhookCredentialPair(t, db)
			if err := prepareWebhookSnapshotCredentialLifetimeColumns(db); err != nil {
				t.Fatalf("prepare validator fixture: %v", err)
			}
			deadline := time.Date(
				2026,
				8,
				17,
				1,
				2,
				3,
				0,
				time.UTC,
			)
			mustExecWebhookCredentialTest(
				t,
				db,
				"UPDATE webhook_delivery_snapshots SET credential_expires_at = ?",
				deadline,
			)
			mustExecWebhookCredentialTest(
				t,
				db,
				"UPDATE outbox_deliveries SET expires_at = ?",
				deadline,
			)
			test.mutate(t, db)
			_, err := loadAndValidateWebhookCredentialPairs(
				db,
				true,
				nil,
			)
			if err == nil ||
				!strings.Contains(
					strings.ToLower(err.Error()),
					test.wantErr,
				) {
				t.Fatalf(
					"validation error = %v, want %q",
					err,
					test.wantErr,
				)
			}
		})
	}
}

func TestValidateWebhookCredentialLifetimeContractRejectsMissingSchema(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "missing-schema")
	seedLegacyWebhookCredentialPair(t, db)
	err := ValidateWebhookSnapshotCredentialLifetimeContract(db)
	if err == nil ||
		!strings.Contains(err.Error(), "credential_expires_at") {
		t.Fatalf(
			"validation error = %v, want missing credential_expires_at",
			err,
		)
	}
}

func TestRuntimeWebhookCredentialValidationRejectsUnknownProjectStatus(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "runtime-status")
	seedLegacyWebhookCredentialPair(t, db)
	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	); err != nil {
		t.Fatal(err)
	}
	mustExecWebhookCredentialTest(
		t,
		db,
		"PRAGMA ignore_check_constraints = ON",
	)
	mustExecWebhookCredentialTest(
		t,
		db,
		"UPDATE projects SET status = 'mystery' WHERE id = 22",
	)
	mustExecWebhookCredentialTest(
		t,
		db,
		"PRAGMA ignore_check_constraints = OFF",
	)
	err := ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
		context.Background(),
		db,
	)
	if err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "status") {
		t.Fatalf(
			"runtime validation error = %v, want unknown Project status",
			err,
		)
	}
}

func TestWebhookCredentialProjectScopeForeignKeysRejectDirectoryOrphans(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "project-scope-fk")
	seedLegacyWebhookCredentialPair(t, db)
	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM projects WHERE id = 22").Error; err == nil {
		t.Fatal("Project hard delete bypassed child scope foreign keys")
	}
}

func TestRuntimeWebhookCredentialValidationDoesNotScanNonWebhookHistory(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "runtime-bounded")
	seedLegacyWebhookCredentialPair(t, db)
	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	); err != nil {
		t.Fatal(err)
	}
	mustExecWebhookCredentialTest(
		t,
		db,
		`INSERT INTO outbox_deliveries (
			id, organization_id, project_id, event_id,
			destination_type, destination_id, status
		 ) VALUES (
			'legacy-non-webhook-id', 11, 22, ?,
			'test_delivery', 'large-history', 'pending'
		 )`,
		legacyWebhookEventID,
	)
	if err := ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
		context.Background(),
		db,
	); err != nil {
		t.Fatalf(
			"runtime validator scanned non-webhook history: %v",
			err,
		)
	}
}

func TestCanonicalWebhookConstraintDefinitionPreservesLogicalGrouping(
	t *testing.T,
) {
	leftGrouped, err := canonicalWebhookConstraintDefinition(
		`CHECK ((a = 'x' AND b = 'y') OR (c = 'z' AND d = 'w'))`,
	)
	if err != nil {
		t.Fatal(err)
	}
	rightGrouped, err := canonicalWebhookConstraintDefinition(
		`CHECK (a = 'x' AND (b = 'y' OR c = 'z') AND d = 'w')`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if leftGrouped == rightGrouped {
		t.Fatalf(
			"logically different CHECK groupings collapsed to %q",
			leftGrouped,
		)
	}
}

func TestWebhookCredentialColumnContractRejectsWrongExistingSQLiteColumns(
	t *testing.T,
) {
	tests := []struct {
		name        string
		snapshotDDL string
		outboxDDL   string
		wantColumn  string
	}{
		{
			name: "snapshot deadline text",
			snapshotDDL: `CREATE TABLE webhook_delivery_snapshots (
				credential_expires_at TEXT NOT NULL,
				credential_shredded_at DATETIME,
				credential_shred_reason VARCHAR(20)
			)`,
			outboxDDL: `CREATE TABLE outbox_deliveries (
				expires_at DATETIME,
				expired_at DATETIME
			)`,
			wantColumn: "credential_expires_at",
		},
		{
			name: "snapshot shredded timestamp not null",
			snapshotDDL: `CREATE TABLE webhook_delivery_snapshots (
				credential_expires_at DATETIME NOT NULL,
				credential_shredded_at DATETIME NOT NULL,
				credential_shred_reason VARCHAR(20)
			)`,
			outboxDDL: `CREATE TABLE outbox_deliveries (
				expires_at DATETIME,
				expired_at DATETIME
			)`,
			wantColumn: "credential_shredded_at",
		},
		{
			name: "shred reason length",
			snapshotDDL: `CREATE TABLE webhook_delivery_snapshots (
				credential_expires_at DATETIME NOT NULL,
				credential_shredded_at DATETIME,
				credential_shred_reason VARCHAR(21)
			)`,
			outboxDDL: `CREATE TABLE outbox_deliveries (
				expires_at DATETIME,
				expired_at DATETIME
			)`,
			wantColumn: "credential_shred_reason",
		},
		{
			name: "outbox expiry not null with default",
			snapshotDDL: `CREATE TABLE webhook_delivery_snapshots (
				credential_expires_at DATETIME NOT NULL,
				credential_shredded_at DATETIME,
				credential_shred_reason VARCHAR(20)
			)`,
			outboxDDL: `CREATE TABLE outbox_deliveries (
				expires_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				expired_at DATETIME
			)`,
			wantColumn: "expires_at",
		},
		{
			name: "outbox expired timestamp text default",
			snapshotDDL: `CREATE TABLE webhook_delivery_snapshots (
				credential_expires_at DATETIME NOT NULL,
				credential_shredded_at DATETIME,
				credential_shred_reason VARCHAR(20)
			)`,
			outboxDDL: `CREATE TABLE outbox_deliveries (
				expires_at DATETIME,
				expired_at TEXT DEFAULT 'never'
			)`,
			wantColumn: "expired_at",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, err := gorm.Open(
				sqlite.Open(
					"file:"+strings.ReplaceAll(t.Name(), "/", "-")+
						"?mode=memory&cache=shared",
				),
				&gorm.Config{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(test.snapshotDDL).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(test.outboxDDL).Error; err != nil {
				t.Fatal(err)
			}
			err = validateWebhookCredentialColumnContract(db)
			if err == nil ||
				!strings.Contains(err.Error(), test.wantColumn) {
				t.Fatalf(
					"column contract error = %v, want %s rejection",
					err,
					test.wantColumn,
				)
			}
		})
	}
}

func TestWebhookCredentialClosedVocabularyMatchesCanonicalModelValues(
	t *testing.T,
) {
	statusExpression := closedVocabularyConstraintExpression(
		"status",
		models.OutboxDeliveryStatusValues(),
	)
	if got := webhookCredentialConstraintDefinitions["chk_outbox_delivery_status"].expression; got != statusExpression {
		t.Fatalf(
			"Outbox status CHECK = %q, want canonical %q",
			got,
			statusExpression,
		)
	}
	reasonExpression := nullableClosedVocabularyConstraintExpression(
		"credential_shred_reason",
		models.WebhookCredentialShredReasonValues(),
	)
	if got := webhookCredentialConstraintDefinitions["chk_webhook_snapshot_shred_reason"].expression; got != reasonExpression {
		t.Fatalf(
			"shred reason CHECK = %q, want canonical %q",
			got,
			reasonExpression,
		)
	}

	statusField, ok := reflect.TypeOf(models.OutboxDelivery{}).
		FieldByName("Status")
	if !ok {
		t.Fatal("OutboxDelivery.Status field is missing")
	}
	assertGORMCheckUsesCanonicalExpression(
		t,
		statusField.Tag.Get("gorm"),
		"chk_outbox_delivery_status",
		statusExpression,
	)
	reasonField, ok := reflect.TypeOf(models.WebhookDeliverySnapshot{}).
		FieldByName("CredentialShredReason")
	if !ok {
		t.Fatal("WebhookDeliverySnapshot.CredentialShredReason field is missing")
	}
	assertGORMCheckUsesCanonicalExpression(
		t,
		reasonField.Tag.Get("gorm"),
		"chk_webhook_snapshot_shred_reason",
		reasonExpression,
	)
}

func TestSQLiteWebhookCredentialConstraintsRejectInvalidDirectSQL(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open(
			"file:"+strings.ReplaceAll(t.Name(), "/", "-")+
				"?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.Project{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.WebhookDeliverySnapshot{},
	); err != nil {
		t.Fatal(err)
	}
	if err := validateSQLiteWebhookCredentialCatalog(db); err != nil {
		t.Fatalf("validate exact SQLite CHECK definitions: %v", err)
	}
	now := time.Date(2026, 8, 10, 1, 2, 3, 987654321, time.UTC)
	event := models.DomainEvent{
		ID:              uuid.NewString(),
		OrganizationID:  11,
		ProjectID:       22,
		SpecVersion:     "1.0",
		Source:          "urn:chronodesk:test",
		Type:            "io.chronodesk.ticket.created.v1",
		Subject:         "ticket/1",
		Time:            now,
		DataContentType: "application/json",
		Data:            datatypes.JSON(`{}`),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "sqlite-constraint-test",
		ResourceVersion: 1,
	}
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	config := models.WebhookConfig{
		ID:             77,
		OrganizationID: 11,
		ProjectID:      22,
		UpdatedAt:      now,
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://sqlite-constraint.example.test/events",
		Status:         models.WebhookStatusActive,
		Secret:         "sealed-secret",
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventTicketCreated,
		},
	}
	deadline := now.Add(models.WebhookDeliveryCredentialLifetime)
	snapshot, err := models.NewWebhookDeliverySnapshot(
		config,
		event.ID,
		deadline,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(snapshot).Error; err != nil {
		t.Fatal(err)
	}
	destinationID, err :=
		models.WebhookDeliverySnapshotDestinationID(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	delivery := models.OutboxDelivery{
		ID:              uuid.NewString(),
		OrganizationID:  11,
		ProjectID:       22,
		EventID:         event.ID,
		DestinationType: "webhook",
		DestinationID:   destinationID,
		Status:          models.OutboxDeliveryPending,
		MaxAttempts:     1,
		NextAttemptAt:   now,
		ExpiresAt:       &deadline,
	}
	if err := db.Create(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "unknown outbox status",
			query: "UPDATE outbox_deliveries SET status = 'mystery' WHERE id = ?",
			args:  []any{delivery.ID},
		},
		{
			name:  "expired status without timestamp",
			query: "UPDATE outbox_deliveries SET status = 'expired' WHERE id = ?",
			args:  []any{delivery.ID},
		},
		{
			name:  "webhook deadline removed",
			query: "UPDATE outbox_deliveries SET expires_at = NULL WHERE id = ?",
			args:  []any{delivery.ID},
		},
		{
			name: "unknown shred reason",
			query: `UPDATE webhook_delivery_snapshots
				SET credential_shredded_at = ?,
				    credential_shred_reason = 'mystery',
				    secret = ''
				WHERE id = ?`,
			args: []any{now, snapshot.ID},
		},
		{
			name: "shredded credential remains",
			query: `UPDATE webhook_delivery_snapshots
				SET credential_shredded_at = ?,
				    credential_shred_reason = 'succeeded'
				WHERE id = ?`,
			args: []any{now, snapshot.ID},
		},
		{
			name: "shredded secret null",
			query: `UPDATE webhook_delivery_snapshots
				SET credential_shredded_at = ?,
				    credential_shred_reason = 'succeeded',
				    secret = NULL,
				    previous_secret = '',
				    access_token = ''
				WHERE id = ?`,
			args: []any{now, snapshot.ID},
		},
		{
			name: "shredded previous secret null",
			query: `UPDATE webhook_delivery_snapshots
				SET credential_shredded_at = ?,
				    credential_shred_reason = 'succeeded',
				    secret = '',
				    previous_secret = NULL,
				    access_token = ''
				WHERE id = ?`,
			args: []any{now, snapshot.ID},
		},
		{
			name: "shredded access token null",
			query: `UPDATE webhook_delivery_snapshots
				SET credential_shredded_at = ?,
				    credential_shred_reason = 'succeeded',
				    secret = '',
				    previous_secret = '',
				    access_token = NULL
				WHERE id = ?`,
			args: []any{now, snapshot.ID},
		},
		{
			name:  "snapshot deadline removed",
			query: "UPDATE webhook_delivery_snapshots SET credential_expires_at = NULL WHERE id = ?",
			args:  []any{snapshot.ID},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := db.Exec(test.query, test.args...).Error; err == nil {
				t.Fatalf("invalid direct SQL passed constraint: %s", test.query)
			}
		})
	}
}

func assertGORMCheckUsesCanonicalExpression(
	t *testing.T,
	tag string,
	name string,
	wantExpression string,
) {
	t.Helper()
	marker := "check:" + name + ","
	start := strings.Index(tag, marker)
	if start < 0 {
		t.Fatalf("GORM tag %q is missing %s", tag, name)
	}
	got := tag[start+len(marker):]
	if separator := strings.Index(got, ";"); separator >= 0 {
		got = got[:separator]
	}
	gotCanonical, err := canonicalWebhookConstraintDefinition(got)
	if err != nil {
		t.Fatalf("parse GORM CHECK %s: %v", name, err)
	}
	wantCanonical, err := canonicalWebhookConstraintDefinition(
		wantExpression,
	)
	if err != nil {
		t.Fatalf("parse canonical CHECK %s: %v", name, err)
	}
	if gotCanonical != wantCanonical {
		t.Fatalf(
			"GORM CHECK %s = %q, want %q",
			name,
			got,
			wantExpression,
		)
	}
}

func openLegacyWebhookCredentialMigrationDB(
	t *testing.T,
	suffix string,
) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s-%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "-"),
		strings.ReplaceAll(suffix, " ", "-"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SchemaMigrationCheckpoint{}); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE projects (
			id INTEGER NOT NULL,
			organization_id INTEGER NOT NULL,
			status VARCHAR(20) NOT NULL,
			PRIMARY KEY (id),
			CONSTRAINT chk_projects_status CHECK (
				status IN ('active', 'archived')
			)
		)`,
		`CREATE UNIQUE INDEX idx_projects_scope_id
		 ON projects(organization_id, id)`,
		`CREATE TABLE domain_events (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL
		)`,
		`CREATE TABLE webhook_delivery_snapshots (
			id TEXT PRIMARY KEY,
			created_at DATETIME NOT NULL,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			config_id INTEGER NOT NULL,
			event_id TEXT NOT NULL,
			secret TEXT NOT NULL DEFAULT '',
			previous_secret TEXT NOT NULL DEFAULT '',
			access_token TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE outbox_deliveries (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			event_id TEXT NOT NULL,
			destination_type TEXT NOT NULL,
			destination_id TEXT NOT NULL,
			status TEXT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func seedLegacyWebhookCredentialPair(t *testing.T, db *gorm.DB) {
	t.Helper()
	createdAt := time.Date(2026, 8, 1, 1, 2, 3, 0, time.UTC)
	projectInsert := `INSERT OR IGNORE INTO projects (
		id, organization_id, status
	 ) VALUES
		(22, 11, 'active'),
		(23, 11, 'archived')`
	if db.Dialector.Name() == "postgres" {
		projectInsert = `INSERT INTO projects (
			id, organization_id, status
		 ) VALUES
			(22, 11, 'active'),
			(23, 11, 'archived')
		 ON CONFLICT (id) DO NOTHING`
	}
	mustExecWebhookCredentialTest(t, db, projectInsert)
	mustExecWebhookCredentialTest(
		t,
		db,
		`INSERT INTO domain_events (id, organization_id, project_id)
		 VALUES (?, 11, 22)`,
		legacyWebhookEventID,
	)
	mustExecWebhookCredentialTest(
		t,
		db,
		`INSERT INTO webhook_delivery_snapshots (
			id, created_at, organization_id, project_id, config_id, event_id
		 ) VALUES (?, ?, 11, 22, 77, ?)`,
		legacyWebhookSnapshotID,
		createdAt,
		legacyWebhookEventID,
	)
	mustExecWebhookCredentialTest(
		t,
		db,
		`INSERT INTO outbox_deliveries (
			id, organization_id, project_id, event_id,
			destination_type, destination_id, status
		 ) VALUES (?, 11, 22, ?, 'webhook', ?, 'pending')`,
		legacyWebhookDeliveryID,
		legacyWebhookEventID,
		"snapshot:"+legacyWebhookSnapshotID,
	)
}

func mustExecWebhookCredentialTest(
	t *testing.T,
	db *gorm.DB,
	query string,
	args ...any,
) {
	t.Helper()
	if err := db.Exec(query, args...).Error; err != nil {
		t.Fatal(err)
	}
}

func assertLegacyWebhookCredentialDeadline(
	t *testing.T,
	db *gorm.DB,
	want time.Time,
) {
	t.Helper()
	var snapshot struct {
		CredentialExpiresAt *time.Time `gorm:"column:credential_expires_at"`
	}
	if err := db.Table("webhook_delivery_snapshots").
		Where("id = ?", legacyWebhookSnapshotID).
		Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	var delivery struct {
		ExpiresAt *time.Time `gorm:"column:expires_at"`
	}
	if err := db.Table("outbox_deliveries").
		Where("id = ?", legacyWebhookDeliveryID).
		Take(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot.CredentialExpiresAt == nil ||
		delivery.ExpiresAt == nil ||
		!snapshot.CredentialExpiresAt.Equal(want) ||
		!delivery.ExpiresAt.Equal(want) {
		t.Fatalf(
			"legacy deadlines snapshot=%v delivery=%v want=%s",
			snapshot.CredentialExpiresAt,
			delivery.ExpiresAt,
			want,
		)
	}
}
