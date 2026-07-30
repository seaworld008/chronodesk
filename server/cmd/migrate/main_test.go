package main

import "testing"

func TestMigrationDSNFromEnvironmentUsesDedicatedMigrationRole(t *testing.T) {
	t.Setenv("DATABASE_MIGRATION_URL", "postgres://migration-role/database")
	t.Setenv("DATABASE_URL_UNPOOLED", "postgres://unpooled-role/database")
	t.Setenv("POSTGRES_URL_NON_POOLING", "postgres://non-pooling-role/database")
	t.Setenv("DATABASE_URL", "postgres://legacy-role/database")

	if got := migrationDSNFromEnvironment(); got != "postgres://migration-role/database" {
		t.Fatalf("migration DSN = %q, want dedicated migration role", got)
	}
}

func TestMigrationDSNFromEnvironmentFallsBackInDocumentedOrder(t *testing.T) {
	t.Setenv("DATABASE_MIGRATION_URL", "")
	t.Setenv("DATABASE_URL_UNPOOLED", "")
	t.Setenv("POSTGRES_URL_NON_POOLING", "postgres://non-pooling-role/database")
	t.Setenv("DATABASE_URL", "postgres://legacy-role/database")

	if got := migrationDSNFromEnvironment(); got != "postgres://non-pooling-role/database" {
		t.Fatalf("migration DSN = %q, want non-pooling fallback", got)
	}
}

func TestMigrationDSNFromEnvironmentRejectsMissingConfiguration(t *testing.T) {
	t.Setenv("DATABASE_MIGRATION_URL", "")
	t.Setenv("DATABASE_URL_UNPOOLED", "")
	t.Setenv("POSTGRES_URL_NON_POOLING", "")
	t.Setenv("DATABASE_URL", "")

	if got := migrationDSNFromEnvironment(); got != "" {
		t.Fatalf("migration DSN = %q, want empty value", got)
	}
}
