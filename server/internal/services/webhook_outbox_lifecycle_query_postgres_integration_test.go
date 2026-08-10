package services

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

func TestWebhookOutboxLifecycleProductionQueriesPostgresExplainScale(
	t *testing.T,
) {
	fixture := newWebhookOutboxLifecyclePostgresFixture(t)
	fixture.clearRows(t)
	if err := fixture.adminScoped.Exec(`
		CREATE INDEX idx_outbox_lifecycle_claim
		ON outbox_deliveries (
			organization_id,
			project_id,
			next_attempt_at,
			created_at,
			id
		)
		WHERE destination_type <> 'webhook'
		  AND status IN ('pending', 'failed');
		CREATE INDEX idx_outbox_lifecycle_stale_claim
		ON outbox_deliveries (
			organization_id,
			project_id,
			locked_at,
			created_at,
			id
		)
		WHERE destination_type <> 'webhook'
		  AND status = 'processing';
		CREATE INDEX idx_outbox_webhook_retry_claim
		ON outbox_deliveries (
			organization_id,
			project_id,
			destination_type,
			status,
			next_attempt_at,
			created_at,
			id
		)
		WHERE destination_type = 'webhook'
		  AND status IN ('pending', 'failed')
		  AND expires_at IS NOT NULL;
		CREATE INDEX idx_outbox_webhook_stale_claim
		ON outbox_deliveries (
			organization_id,
			project_id,
			destination_type,
			status,
			locked_at,
			created_at,
			id
		)
		WHERE destination_type = 'webhook'
		  AND status = 'processing'
		  AND expires_at IS NOT NULL;
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
		WHERE destination_type = 'webhook'
		  AND status IN ('dead', 'failed', 'pending', 'processing')
		  AND expires_at IS NOT NULL;
		CREATE INDEX idx_outbox_webhook_legacy_cleanup
		ON outbox_deliveries (
			organization_id,
			project_id,
			destination_type,
			status,
			destination_id,
			id
		)
		WHERE destination_type = 'webhook'
		  AND status = 'succeeded';
		CREATE INDEX idx_webhook_snapshot_overlap_cleanup
		ON webhook_delivery_snapshots (
			organization_id,
			project_id,
			previous_secret_expires_at,
			id,
			credential_shredded_at
		)
		WHERE credential_shredded_at IS NULL
		  AND previous_secret_expires_at IS NOT NULL
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.adminScoped.Exec(`
		INSERT INTO domain_events (
			id,
			created_at,
			organization_id,
			project_id,
			spec_version,
			source,
			type,
			subject,
			time,
			data_content_type,
			data,
			actor_type,
			actor_id,
			resource_version
		)
		SELECT
			'20000000-0000-7000-8000-' ||
				lpad(series::text, 12, '0'),
			TIMESTAMPTZ '2026-01-01 00:00:00+00',
			11,
			CASE WHEN series % 10 = 0 THEN 101 ELSE 102 END,
			'1.0',
			'urn:chronodesk:test:lifecycle-index',
			'io.chronodesk.test.lifecycle-index.v1',
			'lifecycle-index/' || series::text,
			TIMESTAMPTZ '2026-01-01 00:00:00+00',
			'application/json',
			'{}'::jsonb,
			'system',
			'lifecycle-index-test',
			1
		FROM generate_series(1, 500000) AS series;

		INSERT INTO webhook_delivery_snapshots (
			id,
			created_at,
			organization_id,
			project_id,
			config_id,
			event_id,
			config_updated_at,
			provider,
			webhook_url,
			secret,
			previous_secret,
			access_token,
			credential_expires_at,
			previous_secret_expires_at,
			credential_shredded_at,
			credential_shred_reason,
			enabled_events,
			message_template,
			message_format,
			filter_rules,
			retry_count,
			retry_interval,
			timeout_seconds,
			rate_limit,
			rate_limit_window
		)
		SELECT
			CASE
				WHEN series % 10 = 0
					THEN '90000000-0000-7000-8000-'
				ELSE '10000000-0000-7000-8000-'
			END ||
				lpad(series::text, 12, '0'),
			TIMESTAMPTZ '2026-01-01 00:00:00+00',
			11,
			CASE WHEN series % 10 = 0 THEN 101 ELSE 102 END,
			series,
			'20000000-0000-7000-8000-' ||
				lpad(series::text, 12, '0'),
			TIMESTAMPTZ '2026-01-01 00:00:00+00',
			'custom',
			'https://lifecycle.invalid.example/events',
			CASE
				WHEN (series / 10) % 10 >= 3 THEN ''
				ELSE 'sealed-current-envelope'
			END,
			CASE
				WHEN (series / 10) % 10 >= 3 THEN ''
				ELSE 'sealed-previous-envelope'
			END,
			CASE
				WHEN (series / 10) % 10 >= 3 THEN ''
				ELSE 'sealed-access-envelope'
			END,
			CASE
				WHEN (series / 10) % 10 < 5
					THEN TIMESTAMPTZ '2027-01-01 00:00:00+00'
				ELSE TIMESTAMPTZ '2025-01-01 00:00:00+00'
			END,
			CASE
				WHEN (series / 10) % 10 >= 3 THEN NULL
				WHEN (series / 10) % 3 = 0
					THEN TIMESTAMPTZ '2025-01-01 00:00:00+00'
				ELSE TIMESTAMPTZ '2027-01-01 00:00:00+00'
			END,
			CASE
				WHEN (series / 10) % 10 >= 3
					THEN TIMESTAMPTZ '2026-01-01 00:00:00+00'
				ELSE NULL
			END,
			CASE
				WHEN (series / 10) % 10 >= 3 THEN 'succeeded'
				ELSE NULL
			END,
			'[]',
			'',
			'',
			'',
			8,
			60,
			30,
			60,
			60
		FROM generate_series(1, 500000) AS series;

		INSERT INTO outbox_deliveries (
			id,
			created_at,
			updated_at,
			organization_id,
			project_id,
			event_id,
			destination_type,
			destination_id,
			status,
			attempts,
			max_attempts,
			next_attempt_at,
			locked_at,
			locked_by,
			lock_token,
			expires_at,
			delivered_at
		)
		SELECT
			'40000000-0000-7000-8000-' ||
				lpad(series::text, 12, '0'),
			TIMESTAMPTZ '2026-01-01 00:00:00+00' +
				series * INTERVAL '1 microsecond',
			TIMESTAMPTZ '2026-01-01 00:00:00+00',
			11,
			CASE WHEN series % 10 = 0 THEN 101 ELSE 102 END,
			'20000000-0000-7000-8000-' ||
				lpad(series::text, 12, '0'),
			'webhook',
			'snapshot:' ||
				CASE
					WHEN series % 10 = 0
						THEN '90000000-0000-7000-8000-'
					ELSE '10000000-0000-7000-8000-'
				END ||
				lpad(series::text, 12, '0'),
			CASE
				WHEN (series / 10) % 10 = 0 THEN 'processing'
				WHEN (series / 10) % 10 = 1 THEN 'failed'
				WHEN (series / 10) % 10 = 2 THEN 'pending'
				ELSE 'succeeded'
			END,
			CASE
				WHEN (series / 10) % 10 = 0 THEN 2
				ELSE 1
			END,
			8,
			CASE
				WHEN (series / 10) % 7 = 0
					THEN TIMESTAMPTZ '2027-01-01 00:00:00+00'
				ELSE TIMESTAMPTZ '2025-01-01 00:00:00+00'
			END,
			CASE
				WHEN (series / 10) % 10 = 0
					THEN TIMESTAMPTZ '2025-01-01 00:00:00+00'
				ELSE NULL
			END,
			CASE
				WHEN (series / 10) % 10 = 0 THEN 'scale-worker'
				ELSE ''
			END,
			CASE
				WHEN (series / 10) % 10 = 0
					THEN '50000000-0000-7000-8000-' ||
						lpad(series::text, 12, '0')
				ELSE NULL
			END,
			CASE
				WHEN (series / 10) % 10 < 5
					THEN TIMESTAMPTZ '2027-01-01 00:00:00+00'
				ELSE TIMESTAMPTZ '2025-01-01 00:00:00+00'
			END,
			CASE
				WHEN (series / 10) % 10 >= 3
					THEN TIMESTAMPTZ '2026-01-01 00:00:00+00'
				ELSE NULL
			END
		FROM generate_series(1, 500000) AS series;

		ANALYZE outbox_deliveries;
		ANALYZE webhook_delivery_snapshots
	`).Error; err != nil {
		t.Fatal(err)
	}

	scope := fixture.projectA.Scope()
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	batchCreatedBefore := time.Date(
		2030,
		time.January,
		1,
		0,
		0,
		0,
		0,
		time.UTC,
	)
	dryRun := fixture.runtimeA.Session(&gorm.Session{DryRun: true})

	var targetRows int64
	if err := fixture.adminScoped.Model(&models.OutboxDelivery{}).
		Where(
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		).
		Count(&targetRows).Error; err != nil {
		t.Fatal(err)
	}
	if targetRows != 50000 {
		t.Fatalf("target project rows = %d, want 50000", targetRows)
	}
	var dueExpiryRows int64
	if err := fixture.adminScoped.Model(&models.OutboxDelivery{}).
		Where(
			"organization_id = ? AND project_id = ? "+
				"AND expires_at <= ?",
			scope.OrganizationID,
			scope.ProjectID,
			now,
		).
		Count(&dueExpiryRows).Error; err != nil {
		t.Fatal(err)
	}
	if dueExpiryRows == 0 || dueExpiryRows == targetRows {
		t.Fatalf(
			"expiry selectivity is not mixed: due=%d total=%d",
			dueExpiryRows,
			targetRows,
		)
	}

	for _, status := range []models.OutboxDeliveryStatus{
		models.OutboxDeliveryPending,
		models.OutboxDeliveryFailed,
	} {
		var claimRows []models.OutboxDelivery
		claim := buildOutboxWebhookRetryClaimCandidateQuery(
			dryRun.Model(&models.OutboxDelivery{}),
			scope,
			status,
			now,
			batchCreatedBefore,
			outboxClaimScanCursor{},
			50,
		).Find(&claimRows)
		if err := scopeddb.WithProjectScopeTransaction(
			context.Background(),
			fixture.runtimeA,
			scope,
			func(runtimeTx *gorm.DB) error {
				assertPostgresLifecycleJSONPlanUsesIndex(
					t,
					runtimeTx,
					fixture.runtimeRole,
					claim.Statement.SQL.String(),
					claim.Statement.Vars,
					"outbox_deliveries",
					"idx_outbox_webhook_retry_claim",
					1,
					50,
				)
				return nil
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	var staleRows []models.OutboxDelivery
	stale := buildOutboxWebhookStaleClaimCandidateQuery(
		dryRun.Model(&models.OutboxDelivery{}),
		scope,
		now.Add(-time.Minute),
		batchCreatedBefore,
		outboxClaimScanCursor{},
		50,
	).Find(&staleRows)
	if err := scopeddb.WithProjectScopeTransaction(
		context.Background(),
		fixture.runtimeA,
		scope,
		func(runtimeTx *gorm.DB) error {
			assertPostgresLifecycleJSONPlanUsesIndex(
				t,
				runtimeTx,
				fixture.runtimeRole,
				stale.Statement.SQL.String(),
				stale.Statement.Vars,
				"outbox_deliveries",
				"idx_outbox_webhook_stale_claim",
				1,
				50,
			)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}

	var legacyRows []webhookLegacySucceededScanRow
	legacy := buildWebhookLegacySucceededCandidateQuery(
		dryRun.Model(&models.OutboxDelivery{}),
		scope,
		webhookOutboxCleanupCursor{
			destinationID: "snapshot:00000000-0000-7000-8000-000000000000",
			stableID:      "00000000-0000-7000-8000-000000000000",
		},
		50,
	).Find(&legacyRows)
	if err := scopeddb.WithProjectScopeTransaction(
		context.Background(),
		fixture.runtimeA,
		scope,
		func(runtimeTx *gorm.DB) error {
			assertPostgresLifecycleJSONPlanUsesIndex(
				t,
				runtimeTx,
				fixture.runtimeRole,
				legacy.Statement.SQL.String(),
				legacy.Statement.Vars,
				"outbox_deliveries",
				"idx_outbox_webhook_legacy_cleanup",
				1,
				50,
			)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}

	var expiryRows []models.OutboxDelivery
	expiry := buildWebhookExpiryCandidateQuery(
		dryRun.Model(&models.OutboxDelivery{}),
		scope,
		now,
		now.Add(-time.Minute),
		webhookOutboxCleanupCursor{
			destinationID: "snapshot:00000000-0000-7000-8000-000000000000",
			stableID:      "00000000-0000-7000-8000-000000000000",
			sortAt: time.Date(
				2024,
				time.January,
				1,
				0,
				0,
				0,
				0,
				time.UTC,
			),
			status: models.OutboxDeliveryDead,
		},
		50,
	).Find(&expiryRows)
	if err := scopeddb.WithProjectScopeTransaction(
		context.Background(),
		fixture.runtimeA,
		scope,
		func(runtimeTx *gorm.DB) error {
			assertPostgresLifecycleJSONPlanUsesIndex(
				t,
				runtimeTx,
				fixture.runtimeRole,
				expiry.Statement.SQL.String(),
				expiry.Statement.Vars,
				"outbox_deliveries",
				"idx_outbox_webhook_lifecycle_cleanup",
				0,
				0,
			)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}

	var overlapRows []models.WebhookDeliverySnapshot
	overlap := buildWebhookOverlapCandidateQuery(
		dryRun.Model(&models.WebhookDeliverySnapshot{}),
		scope,
		now,
		webhookOutboxCleanupCursor{
			stableID: "00000000-0000-7000-8000-000000000000",
			sortAt: time.Date(
				2024,
				time.January,
				1,
				0,
				0,
				0,
				0,
				time.UTC,
			),
		},
		50,
	).Find(&overlapRows)
	if err := scopeddb.WithProjectScopeTransaction(
		context.Background(),
		fixture.runtimeA,
		scope,
		func(runtimeTx *gorm.DB) error {
			assertPostgresLifecycleJSONPlanUsesIndex(
				t,
				runtimeTx,
				fixture.runtimeRole,
				overlap.Statement.SQL.String(),
				overlap.Statement.Vars,
				"webhook_delivery_snapshots",
				"idx_webhook_snapshot_overlap_cleanup",
				1,
				50,
			)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}

	assertWebhookRetryRawPlan := func(
		status models.OutboxDeliveryStatus,
		cursor outboxClaimScanCursor,
		wantMinRows float64,
		wantMaxRows float64,
	) {
		t.Helper()
		var rows []models.OutboxDelivery
		query := buildOutboxWebhookRetryClaimCandidateQuery(
			dryRun.Model(&models.OutboxDelivery{}),
			scope,
			status,
			now,
			batchCreatedBefore,
			cursor,
			50,
		).Find(&rows)
		assertPostgresLifecycleDryRunPlan(
			t,
			fixture.runtimeA,
			fixture.runtimeRole,
			scope,
			query.Statement,
			"outbox_deliveries",
			"idx_outbox_webhook_retry_claim",
			wantMinRows,
			wantMaxRows,
		)
	}
	assertWebhookStaleRawPlan := func(
		cursor outboxClaimScanCursor,
		wantMinRows float64,
		wantMaxRows float64,
	) {
		t.Helper()
		var rows []models.OutboxDelivery
		query := buildOutboxWebhookStaleClaimCandidateQuery(
			dryRun.Model(&models.OutboxDelivery{}),
			scope,
			now.Add(-time.Minute),
			batchCreatedBefore,
			cursor,
			50,
		).Find(&rows)
		assertPostgresLifecycleDryRunPlan(
			t,
			fixture.runtimeA,
			fixture.runtimeRole,
			scope,
			query.Statement,
			"outbox_deliveries",
			"idx_outbox_webhook_stale_claim",
			wantMinRows,
			wantMaxRows,
		)
	}
	assertNonWebhookRetryRawPlan := func(
		cursor outboxClaimScanCursor,
		wantMinRows float64,
		wantMaxRows float64,
	) {
		t.Helper()
		var rows []models.OutboxDelivery
		query := buildOutboxNonWebhookRetryClaimCandidateQuery(
			dryRun.Model(&models.OutboxDelivery{}),
			scope,
			now,
			batchCreatedBefore,
			cursor,
			50,
		).Find(&rows)
		assertPostgresLifecycleDryRunPlan(
			t,
			fixture.runtimeA,
			fixture.runtimeRole,
			scope,
			query.Statement,
			"outbox_deliveries",
			"idx_outbox_lifecycle_claim",
			wantMinRows,
			wantMaxRows,
		)
	}
	assertNonWebhookStaleRawPlan := func(
		cursor outboxClaimScanCursor,
		wantMinRows float64,
		wantMaxRows float64,
	) {
		t.Helper()
		var rows []models.OutboxDelivery
		query := buildOutboxNonWebhookStaleClaimCandidateQuery(
			dryRun.Model(&models.OutboxDelivery{}),
			scope,
			now.Add(-time.Minute),
			batchCreatedBefore,
			cursor,
			50,
		).Find(&rows)
		assertPostgresLifecycleDryRunPlan(
			t,
			fixture.runtimeA,
			fixture.runtimeRole,
			scope,
			query.Statement,
			"outbox_deliveries",
			"idx_outbox_lifecycle_stale_claim",
			wantMinRows,
			wantMaxRows,
		)
	}

	if err := fixture.adminScoped.Exec(`
		UPDATE outbox_deliveries
		SET next_attempt_at = TIMESTAMPTZ '2027-01-01 00:00:00+00'
		WHERE organization_id = ?
		  AND project_id = ?
		  AND destination_type = 'webhook'
		  AND status IN ('pending', 'failed')
	`, scope.OrganizationID, scope.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.adminScoped.Exec(`
		UPDATE outbox_deliveries
		SET locked_at = TIMESTAMPTZ '2027-01-01 00:00:00+00'
		WHERE organization_id = ?
		  AND project_id = ?
		  AND destination_type = 'webhook'
		  AND status = 'processing'
	`, scope.OrganizationID, scope.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.adminScoped.Exec(
		"ANALYZE outbox_deliveries",
	).Error; err != nil {
		t.Fatal(err)
	}
	assertWebhookRetryRawPlan(
		models.OutboxDeliveryPending,
		outboxClaimScanCursor{},
		0,
		0,
	)
	assertWebhookRetryRawPlan(
		models.OutboxDeliveryFailed,
		outboxClaimScanCursor{},
		0,
		0,
	)
	assertWebhookStaleRawPlan(outboxClaimScanCursor{}, 0, 0)

	if err := fixture.adminScoped.Exec(`
		UPDATE outbox_deliveries
		SET next_attempt_at = TIMESTAMPTZ '2025-01-01 00:00:00+00'
		WHERE id IN (
			'40000000-0000-7000-8000-000000000010',
			'40000000-0000-7000-8000-000000000020'
		);
		UPDATE outbox_deliveries
		SET locked_at = TIMESTAMPTZ '2025-01-01 00:00:00+00'
		WHERE id = '40000000-0000-7000-8000-000000000100';
		ANALYZE outbox_deliveries
	`).Error; err != nil {
		t.Fatal(err)
	}
	assertWebhookRetryRawPlan(
		models.OutboxDeliveryPending,
		outboxClaimScanCursor{},
		1,
		1,
	)
	assertWebhookRetryRawPlan(
		models.OutboxDeliveryFailed,
		outboxClaimScanCursor{},
		1,
		1,
	)
	assertWebhookStaleRawPlan(outboxClaimScanCursor{}, 1, 1)

	if err := fixture.adminScoped.Exec(`
		UPDATE outbox_deliveries
		SET next_attempt_at = TIMESTAMPTZ '2025-01-01 00:00:00+00'
		WHERE organization_id = ?
		  AND project_id = ?
		  AND destination_type = 'webhook'
		  AND status IN ('pending', 'failed')
	`, scope.OrganizationID, scope.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.adminScoped.Exec(`
		UPDATE outbox_deliveries
		SET locked_at = TIMESTAMPTZ '2025-01-01 00:00:00+00'
		WHERE organization_id = ?
		  AND project_id = ?
		  AND destination_type = 'webhook'
		  AND status = 'processing'
	`, scope.OrganizationID, scope.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.adminScoped.Exec(
		"ANALYZE outbox_deliveries",
	).Error; err != nil {
		t.Fatal(err)
	}
	deepCursor := outboxClaimScanCursor{
		sortAt: time.Date(
			2025,
			time.January,
			1,
			0,
			0,
			0,
			0,
			time.UTC,
		),
		createdAt: time.Date(
			2026,
			time.January,
			1,
			0,
			0,
			0,
			0,
			time.UTC,
		).Add(400000 * time.Microsecond),
		stableID: "40000000-0000-7000-8000-000000400000",
	}
	assertWebhookRetryRawPlan(
		models.OutboxDeliveryPending,
		deepCursor,
		1,
		50,
	)
	assertWebhookRetryRawPlan(
		models.OutboxDeliveryFailed,
		deepCursor,
		1,
		50,
	)
	assertWebhookStaleRawPlan(deepCursor, 1, 50)

	var deepLegacyRows []webhookLegacySucceededScanRow
	deepLegacy := buildWebhookLegacySucceededCandidateQuery(
		dryRun.Model(&models.OutboxDelivery{}),
		scope,
		webhookOutboxCleanupCursor{
			destinationID: "snapshot:90000000-0000-7000-8000-000000400000",
			stableID:      "40000000-0000-7000-8000-000000400000",
		},
		50,
	).Find(&deepLegacyRows)
	assertPostgresLifecycleDryRunPlan(
		t,
		fixture.runtimeA,
		fixture.runtimeRole,
		scope,
		deepLegacy.Statement,
		"outbox_deliveries",
		"idx_outbox_webhook_legacy_cleanup",
		1,
		50,
	)
	var deepOverlapRows []models.WebhookDeliverySnapshot
	deepOverlap := buildWebhookOverlapCandidateQuery(
		dryRun.Model(&models.WebhookDeliverySnapshot{}),
		scope,
		now,
		webhookOutboxCleanupCursor{
			stableID: "90000000-0000-7000-8000-000000400000",
			sortAt: time.Date(
				2025,
				time.January,
				1,
				0,
				0,
				0,
				0,
				time.UTC,
			),
		},
		50,
	).Find(&deepOverlapRows)
	assertPostgresLifecycleDryRunPlan(
		t,
		fixture.runtimeA,
		fixture.runtimeRole,
		scope,
		deepOverlap.Statement,
		"webhook_delivery_snapshots",
		"idx_webhook_snapshot_overlap_cleanup",
		1,
		50,
	)
	if err := fixture.adminScoped.Exec(`
		UPDATE outbox_deliveries
		SET expires_at = TIMESTAMPTZ '2025-01-01 00:00:00+00'
		WHERE organization_id = ?
		  AND project_id = ?
		  AND destination_type = 'webhook'
		  AND status IN ('failed', 'processing')
	`, scope.OrganizationID, scope.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.adminScoped.Exec(`
		UPDATE outbox_deliveries
		SET locked_at = TIMESTAMPTZ '2027-01-01 00:00:00+00'
		WHERE organization_id = ?
		  AND project_id = ?
		  AND destination_type = 'webhook'
		  AND status = 'processing'
	`, scope.OrganizationID, scope.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.adminScoped.Exec(
		"ANALYZE outbox_deliveries",
	).Error; err != nil {
		t.Fatal(err)
	}
	var deepExpiryRows []models.OutboxDelivery
	deepExpiry := buildWebhookExpiryCandidateQuery(
		dryRun.Model(&models.OutboxDelivery{}),
		scope,
		now,
		now.Add(-time.Minute),
		webhookOutboxCleanupCursor{
			destinationID: "snapshot:90000000-0000-7000-8000-000000400000",
			stableID:      "40000000-0000-7000-8000-000000400000",
			sortAt: time.Date(
				2025,
				time.January,
				1,
				0,
				0,
				0,
				0,
				time.UTC,
			),
			status: models.OutboxDeliveryFailed,
		},
		50,
	).Find(&deepExpiryRows)
	assertPostgresLifecycleDryRunPlan(
		t,
		fixture.runtimeA,
		fixture.runtimeRole,
		scope,
		deepExpiry.Statement,
		"outbox_deliveries",
		"idx_outbox_webhook_lifecycle_cleanup",
		1,
		50,
	)
	var validLockRaw []models.OutboxDelivery
	if err := buildWebhookExpiryCandidateQuery(
		fixture.adminScoped.Model(&models.OutboxDelivery{}),
		scope,
		now,
		now.Add(-time.Minute),
		webhookOutboxCleanupCursor{
			destinationID: "snapshot:00000000-0000-7000-8000-000000000000",
			stableID:      "00000000-0000-7000-8000-000000000000",
			sortAt: time.Date(
				2024,
				time.January,
				1,
				0,
				0,
				0,
				0,
				time.UTC,
			),
			status: models.OutboxDeliveryProcessing,
		},
		50,
	).Find(&validLockRaw).Error; err != nil {
		t.Fatal(err)
	}
	if len(validLockRaw) != 50 {
		t.Fatalf("valid-lock raw page rows = %d, want 50", len(validLockRaw))
	}
	validLockIDs := make([]string, 0, len(validLockRaw))
	for index := range validLockRaw {
		validLockIDs = append(validLockIDs, validLockRaw[index].ID)
	}
	var validLockEligible []models.OutboxDelivery
	validLockSecondary := buildWebhookExpiryEligiblePageQuery(
		dryRun.Model(&models.OutboxDelivery{}),
		scope,
		now,
		now.Add(-time.Minute),
		validLockIDs,
	).Find(&validLockEligible)
	assertPostgresLifecycleDryRunPlan(
		t,
		fixture.runtimeA,
		fixture.runtimeRole,
		scope,
		validLockSecondary.Statement,
		"outbox_deliveries",
		"outbox_deliveries_pkey",
		0,
		0,
	)

	if err := fixture.adminScoped.Exec(`
		UPDATE outbox_deliveries
		SET destination_type = 'event_stream',
		    delivered_at = NULL,
		    status = CASE
			    WHEN ((right(id, 12))::bigint / 10) % 10
			         IN (3, 4, 5)
				    THEN 'pending'
			    ELSE 'processing'
		    END,
		    next_attempt_at = TIMESTAMPTZ '2027-01-01 00:00:00+00',
		    locked_at = CASE
			    WHEN ((right(id, 12))::bigint / 10) % 10
			         IN (3, 4, 5)
				    THEN NULL
			    ELSE TIMESTAMPTZ '2027-01-01 00:00:00+00'
		    END,
		    locked_by = CASE
			    WHEN ((right(id, 12))::bigint / 10) % 10
			         IN (3, 4, 5)
				    THEN ''
			    ELSE 'non-webhook-scale-worker'
		    END,
		    lock_token = CASE
			    WHEN ((right(id, 12))::bigint / 10) % 10
			         IN (3, 4, 5)
				    THEN NULL
			    ELSE '60000000-0000-7000-8000-' || right(id, 12)
		    END
		WHERE organization_id = ?
		  AND project_id = ?
		  AND destination_type = 'webhook'
		  AND status = 'succeeded'
	`, scope.OrganizationID, scope.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.adminScoped.Exec(
		"ANALYZE outbox_deliveries",
	).Error; err != nil {
		t.Fatal(err)
	}
	assertNonWebhookRetryRawPlan(outboxClaimScanCursor{}, 0, 0)
	assertNonWebhookStaleRawPlan(outboxClaimScanCursor{}, 0, 0)

	if err := fixture.adminScoped.Exec(`
		UPDATE outbox_deliveries
		SET next_attempt_at = TIMESTAMPTZ '2025-01-01 00:00:00+00'
		WHERE id = '40000000-0000-7000-8000-000000000030';
		UPDATE outbox_deliveries
		SET locked_at = TIMESTAMPTZ '2025-01-01 00:00:00+00'
		WHERE id = '40000000-0000-7000-8000-000000000060';
		ANALYZE outbox_deliveries
	`).Error; err != nil {
		t.Fatal(err)
	}
	assertNonWebhookRetryRawPlan(outboxClaimScanCursor{}, 1, 1)
	assertNonWebhookStaleRawPlan(outboxClaimScanCursor{}, 1, 1)

	if err := fixture.adminScoped.Exec(`
		UPDATE outbox_deliveries
		SET next_attempt_at = TIMESTAMPTZ '2025-01-01 00:00:00+00'
		WHERE organization_id = ?
		  AND project_id = ?
		  AND destination_type <> 'webhook'
		  AND status IN ('pending', 'failed')
	`, scope.OrganizationID, scope.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.adminScoped.Exec(`
		UPDATE outbox_deliveries
		SET locked_at = TIMESTAMPTZ '2025-01-01 00:00:00+00'
		WHERE organization_id = ?
		  AND project_id = ?
		  AND destination_type <> 'webhook'
		  AND status = 'processing'
	`, scope.OrganizationID, scope.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.adminScoped.Exec(
		"ANALYZE outbox_deliveries",
	).Error; err != nil {
		t.Fatal(err)
	}
	assertNonWebhookRetryRawPlan(deepCursor, 1, 50)
	assertNonWebhookStaleRawPlan(deepCursor, 1, 50)
}

func assertPostgresLifecycleDryRunPlan(
	t *testing.T,
	runtimeDB *gorm.DB,
	runtimeRole string,
	scope models.ProjectScope,
	statement *gorm.Statement,
	relationName string,
	indexName string,
	wantMinRows float64,
	wantMaxRows float64,
) {
	t.Helper()
	if err := scopeddb.WithProjectScopeTransaction(
		context.Background(),
		runtimeDB,
		scope,
		func(runtimeTx *gorm.DB) error {
			assertPostgresLifecycleJSONPlanUsesIndex(
				t,
				runtimeTx,
				runtimeRole,
				statement.SQL.String(),
				statement.Vars,
				relationName,
				indexName,
				wantMinRows,
				wantMaxRows,
			)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
}

type postgresLifecycleExplainNode struct {
	NodeType                  string                         `json:"Node Type"`
	RelationName              string                         `json:"Relation Name"`
	IndexName                 string                         `json:"Index Name"`
	ActualRows                float64                        `json:"Actual Rows"`
	ActualLoops               float64                        `json:"Actual Loops"`
	RowsRemovedByFilter       float64                        `json:"Rows Removed by Filter"`
	RowsRemovedByJoinFilter   float64                        `json:"Rows Removed by Join Filter"`
	RowsRemovedByIndexRecheck float64                        `json:"Rows Removed by Index Recheck"`
	SharedHitBlocks           float64                        `json:"Shared Hit Blocks"`
	SharedReadBlocks          float64                        `json:"Shared Read Blocks"`
	Plans                     []postgresLifecycleExplainNode `json:"Plans"`
}

func assertPostgresLifecycleJSONPlanUsesIndex(
	t *testing.T,
	db *gorm.DB,
	runtimeRole string,
	query string,
	args []any,
	relationName string,
	indexName string,
	wantMinRows float64,
	wantMaxRows float64,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var currentUser string
	if err := db.WithContext(ctx).
		Raw("SELECT current_user").
		Scan(&currentUser).Error; err != nil {
		t.Fatal(err)
	}
	if currentUser != runtimeRole {
		t.Fatalf(
			"production lifecycle plan ran as %q, want runtime %q",
			currentUser,
			runtimeRole,
		)
	}
	if err := db.WithContext(ctx).
		Exec("SET LOCAL plan_cache_mode = force_generic_plan").Error; err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := db.WithContext(ctx).Raw(
		"EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+query,
		args...,
	).Row().Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plans []struct {
		Plan postgresLifecycleExplainNode `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &plans); err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("production lifecycle plan count = %d", len(plans))
	}
	if plans[0].Plan.ActualRows < wantMinRows ||
		plans[0].Plan.ActualRows > wantMaxRows {
		t.Fatalf(
			"production lifecycle plan result rows = %.0f, want %.0f..%.0f",
			plans[0].Plan.ActualRows,
			wantMinRows,
			wantMaxRows,
		)
	}
	summary := postgresLifecyclePlanSummary(
		plans[0].Plan,
		relationName,
		indexName,
	)
	if summary.exactIndex == nil ||
		!summary.hasRelation ||
		summary.hasSeqScan ||
		summary.hasSort {
		t.Fatalf(
			"production lifecycle plan must use exact %s on %s without SeqScan or Sort: %+v",
			indexName,
			relationName,
			plans[0].Plan,
		)
	}
	indexNode := summary.exactIndex
	examined := postgresLifecyclePlanNodeExamined(*indexNode)
	if examined > 1000 {
		t.Fatalf(
			"production lifecycle index examined %.0f rows, want <= 1000",
			examined,
		)
	}
	if blocks := indexNode.SharedHitBlocks +
		indexNode.SharedReadBlocks; blocks > 32768 {
		t.Fatalf(
			"production lifecycle index touched %.0f shared blocks, want <= 32768",
			blocks,
		)
	}
	if summary.examined > 5000 {
		t.Fatalf(
			"production lifecycle plan examined %.0f rows across the plan tree, want <= 5000: %+v",
			summary.examined,
			plans[0].Plan,
		)
	}
	if blocks := plans[0].Plan.SharedHitBlocks +
		plans[0].Plan.SharedReadBlocks; blocks > 32768 {
		t.Fatalf(
			"production lifecycle plan touched %.0f shared blocks, want <= 32768",
			blocks,
		)
	}
}

type postgresLifecycleExplainSummary struct {
	exactIndex  *postgresLifecycleExplainNode
	hasRelation bool
	hasSeqScan  bool
	hasSort     bool
	examined    float64
}

func postgresLifecyclePlanSummary(
	node postgresLifecycleExplainNode,
	relationName string,
	indexName string,
) postgresLifecycleExplainSummary {
	summary := postgresLifecycleExplainSummary{
		hasRelation: node.RelationName == relationName,
		hasSeqScan: node.RelationName == relationName &&
			node.NodeType == "Seq Scan",
		hasSort: node.NodeType == "Sort" ||
			node.NodeType == "Incremental Sort",
		examined: postgresLifecyclePlanNodeExamined(node),
	}
	if node.IndexName == indexName &&
		((node.RelationName == relationName &&
			(node.NodeType == "Index Scan" ||
				node.NodeType == "Index Only Scan")) ||
			node.NodeType == "Bitmap Index Scan") {
		indexNode := node
		summary.exactIndex = &indexNode
	}
	for _, child := range node.Plans {
		childSummary := postgresLifecyclePlanSummary(
			child,
			relationName,
			indexName,
		)
		summary.hasRelation =
			summary.hasRelation || childSummary.hasRelation
		summary.hasSeqScan =
			summary.hasSeqScan || childSummary.hasSeqScan
		summary.hasSort = summary.hasSort || childSummary.hasSort
		summary.examined += childSummary.examined
		if summary.exactIndex == nil &&
			childSummary.exactIndex != nil {
			summary.exactIndex = childSummary.exactIndex
		}
	}
	return summary
}

func TestPostgresLifecyclePlanSummaryAcceptsExactBitmapAndFindsUnsafeNodes(
	t *testing.T,
) {
	const (
		relationName = "outbox_deliveries"
		indexName    = "outbox_deliveries_pkey"
	)
	bitmap := postgresLifecyclePlanSummary(
		postgresLifecycleExplainNode{
			NodeType:     "Bitmap Heap Scan",
			RelationName: relationName,
			Plans: []postgresLifecycleExplainNode{{
				NodeType:  "Bitmap Index Scan",
				IndexName: indexName,
			}},
		},
		relationName,
		indexName,
	)
	if bitmap.exactIndex == nil ||
		!bitmap.hasRelation ||
		bitmap.hasSeqScan ||
		bitmap.hasSort {
		t.Fatalf("exact bitmap plan summary = %+v", bitmap)
	}
	unsafe := postgresLifecyclePlanSummary(
		postgresLifecycleExplainNode{
			NodeType: "Append",
			Plans: []postgresLifecycleExplainNode{
				{
					NodeType: "Sort",
					Plans: []postgresLifecycleExplainNode{{
						NodeType:     "Seq Scan",
						RelationName: relationName,
					}},
				},
				{
					NodeType:     "Bitmap Heap Scan",
					RelationName: relationName,
					Plans: []postgresLifecycleExplainNode{{
						NodeType:  "Bitmap Index Scan",
						IndexName: indexName,
					}},
				},
			},
		},
		relationName,
		indexName,
	)
	if unsafe.exactIndex == nil ||
		!unsafe.hasRelation ||
		!unsafe.hasSeqScan ||
		!unsafe.hasSort {
		t.Fatalf("unsafe sibling plan summary = %+v", unsafe)
	}
}

func postgresLifecyclePlanNodeExamined(
	node postgresLifecycleExplainNode,
) float64 {
	return (node.ActualRows +
		node.RowsRemovedByFilter +
		node.RowsRemovedByJoinFilter +
		node.RowsRemovedByIndexRecheck) *
		node.ActualLoops
}
