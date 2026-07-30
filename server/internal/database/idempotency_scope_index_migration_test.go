package database

import (
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateIdempotencyScopeIndexUpgradesSQLiteLegacyDefinition(
	t *testing.T,
) {
	db := openSQLiteIdempotencyIndexTestDatabase(t, "legacy-upgrade")
	createSQLiteIdempotencyIndexTestTable(t, db)
	if err := db.Exec(`
		CREATE UNIQUE INDEX idx_idempotency_actor_operation_key
		ON idempotency_records (actor_type, actor_id, operation, key)
	`).Error; err != nil {
		t.Fatalf("create legacy idempotency index: %v", err)
	}
	if err := ValidateIdempotencyScopeIndex(db); err == nil {
		t.Fatal("runtime validation accepted the legacy four-column index")
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := MigrateIdempotencyScopeIndex(db); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt, err)
		}
	}
	if err := ValidateIdempotencyScopeIndex(db); err != nil {
		t.Fatalf("validate migrated idempotency index: %v", err)
	}

	insert := `
		INSERT INTO idempotency_records (
			id, organization_id, project_id, actor_type, actor_id, operation, key
		) VALUES (?, ?, ?, 'service_principal', 'agent-1', 'ticket.create', 'same-key')
		ON CONFLICT (
			organization_id, project_id, actor_type, actor_id, operation, key
		) DO NOTHING
	`
	if result := db.Exec(insert, "record-1", 11, 22); result.Error != nil ||
		result.RowsAffected != 1 {
		t.Fatalf(
			"insert first project-scoped idempotency row: rows=%d err=%v",
			result.RowsAffected,
			result.Error,
		)
	}
	if result := db.Exec(insert, "record-2", 11, 23); result.Error != nil ||
		result.RowsAffected != 1 {
		t.Fatalf(
			"reuse caller key in another project: rows=%d err=%v",
			result.RowsAffected,
			result.Error,
		)
	}
	if result := db.Exec(insert, "record-3", 11, 23); result.Error != nil ||
		result.RowsAffected != 0 {
		t.Fatalf(
			"replay project-scoped idempotency row: rows=%d err=%v",
			result.RowsAffected,
			result.Error,
		)
	}
}

func TestMigrateIdempotencyScopeIndexFailsClosedOnIncompleteSchema(
	t *testing.T,
) {
	db := openSQLiteIdempotencyIndexTestDatabase(t, "missing-project")
	if err := db.Exec(`
		CREATE TABLE idempotency_records (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			actor_type TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			operation TEXT NOT NULL,
			key TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatalf("create incomplete idempotency table: %v", err)
	}

	err := MigrateIdempotencyScopeIndex(db)
	if err == nil || !strings.Contains(err.Error(), "project_id") {
		t.Fatalf("migration error = %v, want missing project_id", err)
	}
}

func TestValidateIdempotencyScopeIndexRejectsWrongColumnOrder(t *testing.T) {
	db := openSQLiteIdempotencyIndexTestDatabase(t, "wrong-order")
	createSQLiteIdempotencyIndexTestTable(t, db)
	if err := db.Exec(`
		CREATE UNIQUE INDEX idx_idempotency_actor_operation_key
		ON idempotency_records (
			project_id,
			organization_id,
			actor_type,
			actor_id,
			operation,
			key
		)
	`).Error; err != nil {
		t.Fatalf("create wrong-order idempotency index: %v", err)
	}

	err := ValidateIdempotencyScopeIndex(db)
	if err == nil || !strings.Contains(err.Error(), "exact order") {
		t.Fatalf("validation error = %v, want exact-order rejection", err)
	}
}

func openSQLiteIdempotencyIndexTestDatabase(
	t *testing.T,
	suffix string,
) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:idempotency-index-"+suffix+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("open SQLite idempotency index database: %v", err)
	}
	return db
}

func createSQLiteIdempotencyIndexTestTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(`
		CREATE TABLE idempotency_records (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			actor_type TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			operation TEXT NOT NULL,
			key TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatalf("create idempotency table: %v", err)
	}
}
