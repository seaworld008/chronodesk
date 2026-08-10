package database

import (
	"strings"
	"testing"
)

func TestWebhookOutboxLifecycleIndexesPostgresCatalogExact(t *testing.T) {
	db, _, _ := openPostgresMembershipReleaseTestDB(
		t,
		"lifecycle_idx",
	)
	if err := db.Exec(`
		CREATE TABLE outbox_deliveries (
			id text PRIMARY KEY,
			created_at timestamptz NOT NULL,
			organization_id bigint NOT NULL,
			project_id bigint NOT NULL,
			status varchar(20) NOT NULL,
			next_attempt_at timestamptz NOT NULL,
			expires_at timestamptz,
			locked_at timestamptz,
			destination_type varchar(50) NOT NULL,
			destination_id varchar(128) NOT NULL
		);
		CREATE TABLE webhook_delivery_snapshots (
			id text PRIMARY KEY,
			organization_id bigint NOT NULL,
			project_id bigint NOT NULL,
			credential_shredded_at timestamptz,
			previous_secret_expires_at timestamptz
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO outbox_deliveries (
			id,
			created_at,
			organization_id,
			project_id,
			status,
			next_attempt_at,
			destination_type,
			destination_id
		)
		VALUES
			(
				'legacy-processing',
				TIMESTAMPTZ '2026-01-01 00:00:00+00',
				11,
				21,
				'processing',
				TIMESTAMPTZ '2026-01-01 00:00:00+00',
				'test',
				'legacy-processing'
			),
			(
				'legacy-pending',
				TIMESTAMPTZ '2026-01-01 00:00:00+00',
				11,
				21,
				'pending',
				TIMESTAMPTZ '2026-01-01 00:00:00+00',
				'test',
				'legacy-pending'
			)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateWebhookOutboxLifecycleFence(db); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWebhookOutboxLifecycleFence(db); err != nil {
		t.Fatal(err)
	}
	var firstToken string
	if err := db.Raw(`
		SELECT lock_token
		FROM outbox_deliveries
		WHERE id = 'legacy-processing'
	`).Scan(&firstToken).Error; err != nil {
		t.Fatal(err)
	}
	if !webhookOutboxLifecycleTokenIsUUIDv7(firstToken) {
		t.Fatalf("PostgreSQL legacy token is not canonical UUIDv7: %q", firstToken)
	}
	if err := MigrateWebhookOutboxLifecycleFence(db); err != nil {
		t.Fatal(err)
	}
	var secondToken string
	if err := db.Raw(`
		SELECT lock_token
		FROM outbox_deliveries
		WHERE id = 'legacy-processing'
	`).Scan(&secondToken).Error; err != nil {
		t.Fatal(err)
	}
	if secondToken != firstToken {
		t.Fatal("idempotent PostgreSQL fence migration rotated a valid token")
	}
	if err := db.Exec(
		`UPDATE outbox_deliveries
		 SET lock_token = ?
		 WHERE id = 'legacy-processing'`,
		strings.ReplaceAll(firstToken, "-", ""),
	).Error; err == nil {
		t.Fatal("PostgreSQL lifecycle fence accepted compact UUIDv7")
	}
	if err := db.Exec(
		`UPDATE outbox_deliveries
		 SET lock_token = ?
		 WHERE id = 'legacy-pending'`,
		firstToken,
	).Error; err == nil {
		t.Fatal("PostgreSQL lifecycle fence accepted non-processing token")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := MigrateWebhookOutboxLifecycleIndexes(db); err != nil {
			t.Fatalf("lifecycle index migration attempt %d: %v", attempt+1, err)
		}
	}
	if err := ValidateWebhookOutboxLifecycleIndexes(db); err != nil {
		t.Fatal(err)
	}

	for name, ddl := range map[string]string{
		"opclass": `
			CREATE INDEX idx_outbox_webhook_lifecycle_cleanup
			ON outbox_deliveries (
				organization_id,
				project_id,
				destination_type,
				status,
				expires_at,
				destination_id varchar_pattern_ops,
				id
			)
			WHERE destination_type = 'webhook'
			  AND status IN ('dead', 'failed', 'pending', 'processing')
			  AND expires_at IS NOT NULL
		`,
		"collation": `
			CREATE INDEX idx_outbox_webhook_lifecycle_cleanup
			ON outbox_deliveries (
				organization_id,
				project_id,
				destination_type,
				status,
				expires_at,
				destination_id COLLATE "C",
				id
			)
			WHERE destination_type = 'webhook'
			  AND status IN ('dead', 'failed', 'pending', 'processing')
			  AND expires_at IS NOT NULL
		`,
		"nulls_first": `
			CREATE INDEX idx_outbox_webhook_lifecycle_cleanup
			ON outbox_deliveries (
				organization_id,
				project_id,
				destination_type,
				status,
				expires_at,
				destination_id NULLS FIRST,
				id
			)
			WHERE destination_type = 'webhook'
			  AND status IN ('dead', 'failed', 'pending', 'processing')
			  AND expires_at IS NOT NULL
		`,
		"include": `
			CREATE INDEX idx_outbox_webhook_lifecycle_cleanup
			ON outbox_deliveries (
				organization_id,
				project_id,
				destination_type,
				status,
				expires_at,
				destination_id,
				id
			)
			INCLUDE (locked_at)
			WHERE destination_type = 'webhook'
			  AND status IN ('dead', 'failed', 'pending', 'processing')
			  AND expires_at IS NOT NULL
		`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := db.Exec(
				"DROP INDEX idx_outbox_webhook_lifecycle_cleanup",
			).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(ddl).Error; err != nil {
				t.Fatal(err)
			}
			err := ValidateWebhookOutboxLifecycleIndexes(db)
			if err == nil ||
				!strings.Contains(
					err.Error(),
					webhookOutboxLifecycleCleanupIndex,
				) {
				t.Fatalf(
					"PostgreSQL lifecycle %s drift error = %v",
					name,
					err,
				)
			}
			if err := MigrateWebhookOutboxLifecycleIndexes(db); err != nil {
				t.Fatal(err)
			}
			if err := ValidateWebhookOutboxLifecycleIndexes(db); err != nil {
				t.Fatal(err)
			}
		})
	}

	if err := db.Exec(`
		DROP INDEX idx_outbox_webhook_lifecycle_cleanup;
		CREATE INDEX idx_outbox_webhook_lifecycle_cleanup
		ON outbox_deliveries (
			organization_id,
			project_id,
			destination_type,
			status,
			expires_at,
			id,
			destination_id
		)
		WHERE destination_type = 'webhook'
		  AND status IN ('dead', 'failed', 'pending', 'processing')
		  AND expires_at IS NOT NULL
	`).Error; err != nil {
		t.Fatal(err)
	}
	err := ValidateWebhookOutboxLifecycleIndexes(db)
	if err == nil ||
		!strings.Contains(
			err.Error(),
			webhookOutboxLifecycleCleanupIndex,
		) {
		t.Fatalf("PostgreSQL lifecycle index drift error = %v", err)
	}
	if err := MigrateWebhookOutboxLifecycleIndexes(db); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWebhookOutboxLifecycleIndexes(db); err != nil {
		t.Fatal(err)
	}
}
