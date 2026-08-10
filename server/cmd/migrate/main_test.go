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

func TestDestructiveDropIncludesWebhookDeliverySnapshotsBeforeConfigs(
	t *testing.T,
) {
	tables := dropChronoDeskTableNames()
	positions := make(map[string]int, len(tables))
	for index, table := range tables {
		positions[table] = index
	}
	snapshot, hasSnapshot := positions["webhook_delivery_snapshots"]
	outbox, hasOutbox := positions["outbox_deliveries"]
	event, hasEvent := positions["domain_events"]
	config, hasConfig := positions["webhook_configs"]
	if !hasSnapshot {
		t.Fatal("destructive drop inventory omits webhook_delivery_snapshots")
	}
	if !hasConfig {
		t.Fatal("destructive drop inventory omits webhook_configs")
	}
	if !hasOutbox || !hasEvent {
		t.Fatal("destructive drop inventory omits Outbox or DomainEvent tables")
	}
	if snapshot >= outbox || outbox >= event || snapshot >= config {
		t.Fatalf(
			"drop order snapshot=%d outbox=%d event=%d config=%d is unsafe",
			snapshot,
			outbox,
			event,
			config,
		)
	}
}
