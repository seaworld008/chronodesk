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

func TestPostgresA2AIdentifierMigrationIntegration(t *testing.T) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set CHRONODESK_POSTGRES_INTEGRATION=1 for the isolated PostgreSQL migration test")
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
			t.Fatal("A2A migration integration test requires a loopback PostgreSQL target")
		}
	}

	admin, err := gorm.Open(postgres.Open(rawDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open integration PostgreSQL: %v", err)
	}
	schema := fmt.Sprintf("chronodesk_a2a_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error; cleanupErr != nil {
			t.Errorf("drop isolated schema: %v", cleanupErr)
		}
	})

	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}

	legacyDDL := []string{
		`CREATE TABLE agent_tasks (
			id VARCHAR(64) PRIMARY KEY,
			context_id VARCHAR(64) NOT NULL,
			execution_message_id VARCHAR(64) NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX idx_agent_tasks_context ON agent_tasks(context_id)`,
		`CREATE TABLE agent_messages (
			id VARCHAR(64) PRIMARY KEY,
			task_id VARCHAR(64) NOT NULL,
			context_id VARCHAR(64) NOT NULL
		)`,
		`CREATE INDEX idx_agent_messages_context ON agent_messages(context_id)`,
		`CREATE TABLE agent_task_events (
			id BIGSERIAL PRIMARY KEY,
			task_id VARCHAR(64) NOT NULL,
			context_id VARCHAR(64) NOT NULL
		)`,
		`CREATE INDEX idx_agent_task_events_context ON agent_task_events(context_id)`,
		`CREATE TABLE domain_events (
			id VARCHAR(64) PRIMARY KEY,
			correlation_id VARCHAR(128) NOT NULL DEFAULT '',
			causation_id VARCHAR(128) NOT NULL DEFAULT ''
		)`,
	}
	for _, statement := range legacyDDL {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create legacy A2A schema: %v", err)
		}
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := MigrateA2AIdentifierContract(db); err != nil {
			t.Fatalf("PostgreSQL migration attempt %d: %v", attempt, err)
		}
	}
	if err := validatePostgresA2AIdentifierContract(db); err != nil {
		t.Fatalf("validate migrated identifier contract: %v", err)
	}

	longID := strings.Repeat("智", a2aExternalIdentifierMaxLength)
	if err := db.Exec(
		`INSERT INTO agent_tasks (id, context_id, execution_message_id)
		 VALUES ('task-1', ?, ?)`,
		longID,
		longID,
	).Error; err != nil {
		t.Fatalf("insert max-length task identifiers: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO agent_messages (id, task_id, context_id)
		 VALUES (?, 'task-1', ?)`,
		longID,
		longID,
	).Error; err != nil {
		t.Fatalf("insert max-length message identifiers: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO agent_task_events (task_id, context_id)
		 VALUES ('task-1', ?)`,
		longID,
	).Error; err != nil {
		t.Fatalf("insert max-length task event context: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO domain_events (id, correlation_id, causation_id)
		 VALUES ('event-1', ?, ?)`,
		longID,
		longID,
	).Error; err != nil {
		t.Fatalf("insert max-length domain event identifiers: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO agent_messages (id, task_id, context_id)
		 VALUES (?, 'task-1', 'duplicate')`,
		longID,
	).Error; err == nil {
		t.Fatal("agent_messages primary key was lost during widening")
	}

	var indexCount int64
	if err := db.Raw(
		`SELECT COUNT(*) FROM pg_indexes
		 WHERE schemaname = CURRENT_SCHEMA()
		   AND indexname IN (
		     'idx_agent_tasks_context',
		     'idx_agent_messages_context',
		     'idx_agent_task_events_context'
		   )`,
	).Scan(&indexCount).Error; err != nil {
		t.Fatalf("inspect migrated indexes: %v", err)
	}
	if indexCount != 3 {
		t.Fatalf("identifier widening preserved %d/3 indexes", indexCount)
	}

	if err := db.Exec("DELETE FROM agent_task_events").Error; err != nil {
		t.Fatalf("clear boundary event: %v", err)
	}
	if err := db.Exec(
		"ALTER TABLE agent_task_events ALTER COLUMN context_id TYPE VARCHAR(64)",
	).Error; err != nil {
		t.Fatalf("restore one legacy width: %v", err)
	}
	if err := validatePostgresA2AIdentifierContract(db); err == nil {
		t.Fatal("runtime contract accepted a legacy 64-character column")
	}
	if err := MigrateA2AIdentifierContract(db); err != nil {
		t.Fatalf("repair legacy runtime width: %v", err)
	}
}
