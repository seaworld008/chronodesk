package database

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostgresIdempotencyScopeIndexMigrationIntegration(t *testing.T) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set CHRONODESK_POSTGRES_INTEGRATION=1 for the isolated PostgreSQL idempotency index test")
	}
	rawDSN := strings.TrimSpace(os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"))
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("idempotency index integration test requires a loopback PostgreSQL target")
		}
	}

	admin, err := gorm.Open(postgres.Open(rawDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open integration PostgreSQL: %v", err)
	}
	schemaName := fmt.Sprintf("chronodesk_idempotency_index_%d", time.Now().UnixNano())
	quotedSchema := `"` + schemaName + `"`
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := admin.Exec(
			"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
		).Error; cleanupErr != nil {
			t.Errorf("drop isolated schema: %v", cleanupErr)
		}
	})

	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open isolated PostgreSQL pool: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Errorf("close isolated PostgreSQL pool: %v", closeErr)
		}
	})

	legacyDDL := []string{
		`CREATE TABLE idempotency_records (
			id VARCHAR(36) PRIMARY KEY,
			organization_id BIGINT NOT NULL,
			project_id BIGINT NOT NULL,
			actor_type VARCHAR(32) NOT NULL,
			actor_id VARCHAR(128) NOT NULL,
			operation VARCHAR(128) NOT NULL,
			key VARCHAR(255) NOT NULL
		)`,
		`CREATE UNIQUE INDEX idx_idempotency_actor_operation_key
			ON idempotency_records (actor_type, actor_id, operation, key)`,
		`INSERT INTO idempotency_records (
			id, organization_id, project_id, actor_type, actor_id, operation, key
		) VALUES (
			'legacy-record', 11, 22, 'service_principal', 'agent-1',
			'ticket.create', 'same-key'
		)`,
	}
	for _, statement := range legacyDDL {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create legacy idempotency schema: %v", err)
		}
	}

	scopedInsert := `
		INSERT INTO idempotency_records (
			id, organization_id, project_id, actor_type, actor_id, operation, key
		) VALUES (
			?, 11, ?, 'service_principal', 'agent-1',
			'ticket.create', 'same-key'
		)
		ON CONFLICT (
			organization_id, project_id, actor_type, actor_id, operation, key
		) DO NOTHING
	`
	if err := db.Exec(scopedInsert, "before-migration", 23).Error; err == nil {
		t.Fatal("legacy four-column index unexpectedly satisfied the scoped ON CONFLICT target")
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := MigrateIdempotencyScopeIndex(db); err != nil {
			t.Fatalf("PostgreSQL migration attempt %d: %v", attempt, err)
		}
	}
	if err := ValidateIdempotencyScopeIndex(db); err != nil {
		t.Fatalf("validate migrated PostgreSQL idempotency index: %v", err)
	}

	if result := db.Exec(scopedInsert, "project-23", 23); result.Error != nil ||
		result.RowsAffected != 1 {
		t.Fatalf(
			"reuse caller key in another project: rows=%d err=%v",
			result.RowsAffected,
			result.Error,
		)
	}
	if result := db.Exec(scopedInsert, "project-23-replay", 23); result.Error != nil ||
		result.RowsAffected != 0 {
		t.Fatalf(
			"replay caller key in the same project: rows=%d err=%v",
			result.RowsAffected,
			result.Error,
		)
	}

	var records int64
	if err := db.Table("idempotency_records").Count(&records).Error; err != nil {
		t.Fatalf("count idempotency records: %v", err)
	}
	if records != 2 {
		t.Fatalf("idempotency row count = %d, want 2 project-scoped rows", records)
	}
}
