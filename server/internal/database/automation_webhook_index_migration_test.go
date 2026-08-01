package database

import (
	"context"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateAutomationWebhookPaginationIndexesSQLiteIsIdempotent(
	t *testing.T,
) {
	db := openSQLiteAutomationWebhookIndexTestDatabase(t, "idempotent")
	createSQLiteAutomationWebhookIndexTestTables(t, db)

	for attempt := 1; attempt <= 2; attempt++ {
		if err := MigrateAutomationWebhookPaginationIndexes(db); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt, err)
		}
	}
	if err := ValidateAutomationWebhookPaginationIndexes(db); err != nil {
		t.Fatalf("validate migrated pagination indexes: %v", err)
	}
	for _, definition := range automationWebhookPaginationIndexes {
		if !db.Migrator().HasIndex(definition.table, definition.name) {
			t.Errorf("pagination index %s is missing", definition.name)
		}
	}
}

func TestMigrateAutomationWebhookPaginationIndexesSQLiteRepairsDrift(
	t *testing.T,
) {
	tests := []struct {
		name       string
		indexSQL   string
		wantDetail string
	}{
		{
			name: "wrong direction",
			indexSQL: `
				CREATE INDEX idx_automation_rules_directory
				ON automation_rules (
					organization_id,
					project_id,
					deleted_at,
					priority,
					created_at,
					id DESC
				)
			`,
			wantDetail: "created_at DESC",
		},
		{
			name: "partial",
			indexSQL: `
				CREATE INDEX idx_automation_rules_directory
				ON automation_rules (
					organization_id,
					project_id,
					deleted_at,
					priority,
					created_at DESC,
					id DESC
				)
				WHERE deleted_at IS NULL
			`,
			wantDetail: "non-partial",
		},
		{
			name: "expression",
			indexSQL: `
				CREATE INDEX idx_automation_rules_directory
				ON automation_rules (
					(organization_id + 0),
					project_id,
					deleted_at,
					priority,
					created_at DESC,
					id DESC
				)
			`,
			wantDetail: "non-expression",
		},
		{
			name: "wrong order",
			indexSQL: `
				CREATE INDEX idx_automation_rules_directory
				ON automation_rules (
					project_id,
					organization_id,
					deleted_at,
					priority,
					created_at DESC,
					id DESC
				)
			`,
			wantDetail: "exact order",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openSQLiteAutomationWebhookIndexTestDatabase(
				t,
				"drift-"+strings.ReplaceAll(test.name, " ", "-"),
			)
			createSQLiteAutomationWebhookIndexTestTables(t, db)
			if err := db.Exec(test.indexSQL).Error; err != nil {
				t.Fatalf("create incompatible pagination index: %v", err)
			}

			err := ValidateAutomationWebhookPaginationIndexes(db)
			if err == nil ||
				!strings.Contains(err.Error(), "idx_automation_rules_directory") ||
				!strings.Contains(err.Error(), test.wantDetail) {
				t.Fatalf(
					"runtime validation error = %v, want index and %q",
					err,
					test.wantDetail,
				)
			}

			if err := MigrateAutomationWebhookPaginationIndexes(db); err != nil {
				t.Fatalf("repair incompatible pagination index: %v", err)
			}
			if err := ValidateAutomationWebhookPaginationIndexes(db); err != nil {
				t.Fatalf("validate repaired pagination indexes: %v", err)
			}
		})
	}
}

func TestRunMigrationsFromLastModelCreatesAutomationWebhookIndexes(
	t *testing.T,
) {
	db := openSQLiteAutomationWebhookIndexTestDatabase(t, "resume-last-model")
	if err := RunMigrations(db); err != nil {
		t.Fatalf("create baseline schema: %v", err)
	}

	dropAutomationWebhookPaginationIndexes(t, db)
	if err := db.Exec(`
		CREATE INDEX idx_automation_rules_directory
		ON automation_rules (
			project_id,
			organization_id,
			deleted_at,
			priority,
			created_at DESC,
			id DESC
		)
	`).Error; err != nil {
		t.Fatalf("create wrong-order resume fixture: %v", err)
	}
	if err := ValidateAutomationWebhookPaginationIndexes(db); err == nil {
		t.Fatal("runtime gate accepted the incompatible resume fixture")
	}

	if err := RunMigrationsFromModel(
		context.Background(),
		db,
		len(schemaMigrationModels())+1,
	); err != nil {
		t.Fatalf("resume migration after the last model: %v", err)
	}
	if err := ValidateAutomationWebhookPaginationIndexes(db); err != nil {
		t.Fatalf("validate resume-created pagination indexes: %v", err)
	}

	dropAutomationWebhookPaginationIndexes(t, db)
	if err := CreateIndexes(db); err != nil {
		t.Fatalf("create indexes independently of the model scan: %v", err)
	}
	if err := ValidateAutomationWebhookPaginationIndexes(db); err != nil {
		t.Fatalf("validate CreateIndexes pagination indexes: %v", err)
	}
}

func TestMigrateAutomationWebhookPaginationIndexesRejectsIncompleteSchema(
	t *testing.T,
) {
	db := openSQLiteAutomationWebhookIndexTestDatabase(t, "incomplete")
	if err := db.Exec(`
		CREATE TABLE automation_rules (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL
		)
	`).Error; err != nil {
		t.Fatalf("create incomplete schema: %v", err)
	}

	err := MigrateAutomationWebhookPaginationIndexes(db)
	if err == nil ||
		!strings.Contains(err.Error(), "automation_rules.project_id") {
		t.Fatalf("migration error = %v, want missing project_id", err)
	}
}

func openSQLiteAutomationWebhookIndexTestDatabase(
	t *testing.T,
	suffix string,
) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(
			"file:automation-webhook-index-"+suffix+"?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("open SQLite automation/Webhook index database: %v", err)
	}
	return db
}

func createSQLiteAutomationWebhookIndexTestTables(
	t *testing.T,
	db *gorm.DB,
) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE automation_rules (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			deleted_at DATETIME,
			priority INTEGER NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE automation_logs (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			rule_id INTEGER NOT NULL,
			ticket_id INTEGER NOT NULL,
			success NUMERIC NOT NULL,
			executed_at DATETIME NOT NULL
		)`,
		`CREATE TABLE webhook_configs (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			deleted_at DATETIME,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE webhook_logs (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			config_id INTEGER NOT NULL,
			status TEXT NOT NULL,
			event_type TEXT NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE sla_configs (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			is_default NUMERIC NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE ticket_templates (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE quick_replies (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE action_proposals (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE approval_tasks (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE handoffs (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE knowledge_articles (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE knowledge_article_versions (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			article_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			status TEXT NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE knowledge_ingestion_tasks (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create automation/Webhook index test table: %v", err)
		}
	}
}

func dropAutomationWebhookPaginationIndexes(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, definition := range automationWebhookPaginationIndexes {
		if err := db.Exec(
			"DROP INDEX IF EXISTS " +
				quoteAutomationWebhookSQLiteIdentifier(definition.name),
		).Error; err != nil {
			t.Fatalf("drop pagination index %s: %v", definition.name, err)
		}
	}
}
