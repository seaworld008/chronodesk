package database

import (
	"context"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestPostgresAutomationWebhookPaginationIndexMigrationIntegration(
	t *testing.T,
) {
	db, _, _ := openPostgresMembershipReleaseTestDB(
		t,
		"awi",
	)
	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("create baseline PostgreSQL schema: %v", err)
	}
	if err := ValidateAutomationWebhookPaginationIndexes(db); err != nil {
		t.Fatalf("validate baseline PostgreSQL pagination indexes: %v", err)
	}

	if err := db.Exec(`
		DROP INDEX idx_automation_rules_directory;
		CREATE INDEX idx_automation_rules_directory
		ON automation_rules (
			organization_id,
			project_id,
			deleted_at,
			priority,
			created_at,
			id DESC
		)
		WHERE deleted_at IS NULL
	`).Error; err != nil {
		t.Fatalf("create partial wrong-direction PostgreSQL index: %v", err)
	}
	err := ValidateRuntimeSchema(db)
	if err == nil ||
		!strings.Contains(err.Error(), "idx_automation_rules_directory") ||
		!strings.Contains(err.Error(), "non-partial") {
		t.Fatalf(
			"PostgreSQL runtime validation error = %v, want partial index rejection",
			err,
		)
	}

	if err := RunMigrationsFromModel(
		context.Background(),
		db,
		len(schemaMigrationModels())+1,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("resume PostgreSQL migration after the last model: %v", err)
	}
	if err := ValidateRuntimeSchema(db); err != nil {
		t.Fatalf("validate resumed PostgreSQL runtime schema: %v", err)
	}

	if err := db.Exec(`
		DROP INDEX idx_webhook_logs_event_timeline;
		CREATE INDEX idx_webhook_logs_event_timeline
		ON webhook_logs (
			organization_id,
			project_id,
			config_id,
			(lower(event_type::text)),
			created_at DESC,
			id DESC
		)
	`).Error; err != nil {
		t.Fatalf("create PostgreSQL expression index: %v", err)
	}
	err = ValidateAutomationWebhookPaginationIndexes(db)
	if err == nil ||
		!strings.Contains(err.Error(), "idx_webhook_logs_event_timeline") ||
		!strings.Contains(err.Error(), "non-expression") {
		t.Fatalf(
			"PostgreSQL validation error = %v, want expression index rejection",
			err,
		)
	}
	if err := MigrateAutomationWebhookPaginationIndexes(db); err != nil {
		t.Fatalf("repair PostgreSQL expression index: %v", err)
	}
	if err := ValidateAutomationWebhookPaginationIndexes(db); err != nil {
		t.Fatalf("validate repaired PostgreSQL pagination indexes: %v", err)
	}
}
