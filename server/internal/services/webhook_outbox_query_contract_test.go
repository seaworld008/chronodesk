package services

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestWebhookOutboxClaimHotPathDoesNotQuerySchemaCatalog(t *testing.T) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	var catalogQueries atomic.Int32
	const callbackName = "test:webhook_lifecycle_no_claim_catalog_query"
	if err := fixture.db.Callback().Raw().Before("gorm:raw").
		Register(callbackName, func(tx *gorm.DB) {
			if strings.Contains(
				strings.ToLower(tx.Statement.SQL.String()),
				"sqlite_master",
			) {
				catalogQueries.Add(1)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Raw().Remove(callbackName)
	})
	if _, err := fixture.service.ClaimPendingOutbox(
		fixture.worker,
		"no-catalog-worker",
		1,
		time.Minute,
	); err != nil {
		t.Fatal(err)
	}
	if catalogQueries.Load() != 0 {
		t.Fatalf(
			"claim hot path made %d schema catalog queries",
			catalogQueries.Load(),
		)
	}
}

func TestWebhookOutboxLifecycleProductionQueryBuildersPreserveFullContract(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	batchCreatedBefore := now.Add(time.Hour)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	scope := models.ProjectScope{
		OrganizationID: fixture.delivery.OrganizationID,
		ProjectID:      fixture.delivery.ProjectID,
	}
	dryRun := fixture.db.Session(&gorm.Session{DryRun: true})

	var claimRows []models.OutboxDelivery
	claim := buildOutboxWebhookRetryClaimCandidateQuery(
		dryRun.Model(&models.OutboxDelivery{}),
		scope,
		models.OutboxDeliveryPending,
		now,
		batchCreatedBefore,
		outboxClaimScanCursor{},
		17,
	).Find(&claimRows)
	assertLifecycleDryRunSQLContains(
		t,
		claim.Statement.SQL.String(),
		"organization_id = ? AND project_id = ?",
		"destination_type = 'webhook'",
		"status = 'pending'",
		"expires_at IS NOT NULL",
		"created_at < ?",
		"next_attempt_at <= ?",
		"ORDER BY next_attempt_at ASC, created_at ASC, id ASC",
		"LIMIT 17",
	)
	assertLifecycleDryRunSQLExcludes(
		t,
		claim.Statement.SQL.String(),
		"webhook_delivery_snapshots",
		"TRIM(locked_by)",
	)
	var eligibleClaimRows []models.OutboxDelivery
	eligibleClaim := buildOutboxWebhookRetryEligiblePageQuery(
		dryRun.Model(&models.OutboxDelivery{}),
		scope,
		models.OutboxDeliveryPending,
		now,
		batchCreatedBefore,
		[]string{"raw-a", "raw-b"},
	).Find(&eligibleClaimRows)
	assertLifecycleDryRunSQLContains(
		t,
		eligibleClaim.Statement.SQL.String(),
		"id IN (?,?)",
		"created_at < ?",
		"FROM webhook_delivery_snapshots AS lifecycle_snapshot",
		"credential_shredded_at IS NULL",
		"SUBSTR(destination_id, 1, ?) = ?",
		"LENGTH(destination_id) = ?",
	)
	var staleRows []models.OutboxDelivery
	stale := buildOutboxWebhookStaleClaimCandidateQuery(
		dryRun.Model(&models.OutboxDelivery{}),
		scope,
		now.Add(-time.Minute),
		batchCreatedBefore,
		outboxClaimScanCursor{},
		13,
	).Find(&staleRows)
	assertLifecycleDryRunSQLContains(
		t,
		stale.Statement.SQL.String(),
		"status = 'processing' AND locked_at IS NOT NULL",
		"expires_at IS NOT NULL",
		"created_at < ?",
		"locked_at < ?",
		"ORDER BY locked_at ASC, created_at ASC, id ASC",
		"LIMIT 13",
	)
	assertLifecycleDryRunSQLExcludes(
		t,
		stale.Statement.SQL.String(),
		"webhook_delivery_snapshots",
		"TRIM(locked_by)",
	)
	var eligibleStaleRows []models.OutboxDelivery
	eligibleStale := buildOutboxWebhookStaleEligiblePageQuery(
		dryRun.Model(&models.OutboxDelivery{}),
		scope,
		now,
		now.Add(-time.Minute),
		batchCreatedBefore,
		[]string{"raw-a", "raw-b"},
	).Find(&eligibleStaleRows)
	assertLifecycleDryRunSQLContains(
		t,
		eligibleStale.Statement.SQL.String(),
		"id IN (?,?)",
		"created_at < ?",
		"TRIM(locked_by) <> ''",
		"TRIM(lock_token) <> ''",
		"FROM webhook_delivery_snapshots AS lifecycle_snapshot",
		"credential_shredded_at IS NULL",
	)

	var expiryRows []models.OutboxDelivery
	expiry := buildWebhookExpiryCandidateQuery(
		dryRun.Model(&models.OutboxDelivery{}),
		scope,
		now,
		now.Add(-time.Minute),
		webhookOutboxCleanupCursor{
			destinationID: "snapshot:cursor",
			stableID:      "delivery-cursor",
			sortAt:        now.Add(-2 * time.Hour),
			status:        models.OutboxDeliveryFailed,
		},
		19,
	).Find(&expiryRows)
	assertLifecycleDryRunSQLContains(
		t,
		expiry.Statement.SQL.String(),
		"destination_type = 'webhook'",
		"expires_at IS NOT NULL",
		"status IN ('dead', 'failed', 'pending', 'processing')",
		"(status, expires_at, destination_id, id) > (?, ?, ?, ?)",
		"ORDER BY status ASC, expires_at ASC, destination_id ASC, id ASC",
		"LIMIT 19",
	)
	assertLifecycleDryRunSQLExcludes(
		t,
		expiry.Statement.SQL.String(),
		"CASE WHEN",
		"TRIM(locked_by)",
	)
	var eligibleExpiryRows []models.OutboxDelivery
	eligibleExpiry := buildWebhookExpiryEligiblePageQuery(
		dryRun.Model(&models.OutboxDelivery{}),
		scope,
		now,
		now.Add(-time.Minute),
		[]string{"raw-a", "raw-b"},
	).Find(&eligibleExpiryRows)
	assertLifecycleDryRunSQLContains(
		t,
		eligibleExpiry.Statement.SQL.String(),
		"id IN (?,?)",
		"status <> 'processing' OR (",
		"locked_at IS NULL",
		"TRIM(locked_by) = ''",
		"locked_at < ?",
	)

	var overlapRows []models.WebhookDeliverySnapshot
	overlap := buildWebhookOverlapCandidateQuery(
		dryRun.Model(&models.WebhookDeliverySnapshot{}),
		scope,
		now,
		webhookOutboxCleanupCursor{
			stableID: "snapshot-cursor",
			sortAt:   now.Add(-time.Hour),
		},
		23,
	).Find(&overlapRows)
	assertLifecycleDryRunSQLContains(
		t,
		overlap.Statement.SQL.String(),
		"credential_shredded_at IS NULL",
		"previous_secret_expires_at IS NOT NULL",
		"previous_secret_expires_at <= ?",
		"(previous_secret_expires_at, id) > (?, ?)",
		"ORDER BY previous_secret_expires_at ASC, id ASC",
		"LIMIT 23",
	)

	var legacyRows []webhookLegacySucceededScanRow
	legacy := buildWebhookLegacySucceededCandidateQuery(
		dryRun.Model(&models.OutboxDelivery{}),
		scope,
		webhookOutboxCleanupCursor{
			destinationID: "snapshot:cursor",
			stableID:      "delivery-cursor",
		},
		29,
	).Find(&legacyRows)
	assertLifecycleDryRunSQLContains(
		t,
		legacy.Statement.SQL.String(),
		"destination_type = 'webhook'",
		"status = 'succeeded'",
		"COALESCE((",
		"SELECT CASE",
		"credential_shredded_at IS NOT NULL",
		"credential_shred_reason = ?",
		"previous_secret_expires_at IS NULL",
		"(destination_id, id) > (?, ?)",
		"ORDER BY destination_id ASC, id ASC",
		"LIMIT 29",
	)
}

func TestOutboxClaimLaneFiltersUseFixedDisjointDestinationSets(
	t *testing.T,
) {
	fixture := newWebhookOutboxLifecycleFixture(
		t,
		time.Date(2026, time.August, 26, 9, 0, 0, 0, time.UTC),
	)
	dryRun := fixture.db.Session(&gorm.Session{DryRun: true})
	tests := []struct {
		name         string
		lane         OutboxDeliveryLane
		sqlFragment  string
		destinations []string
	}{
		{
			name:         "callback",
			lane:         OutboxDeliveryLaneCallback,
			sqlFragment:  "destination_type IN (?,?)",
			destinations: outboxCallbackDestinations,
		},
		{
			name:         "storage",
			lane:         OutboxDeliveryLaneStorage,
			sqlFragment:  "destination_type IN (?,?,?,?)",
			destinations: outboxStorageDestinations,
		},
		{
			name:         "internal",
			lane:         OutboxDeliveryLaneInternal,
			sqlFragment:  "destination_type IN (?,?,?,?,?)",
			destinations: outboxInternalDestinations,
		},
		{
			name: "other",
			lane: OutboxDeliveryLaneOther,
			sqlFragment: "destination_type NOT IN " +
				"(?,?,?,?,?,?,?,?,?,?,?,?)",
			destinations: outboxKnownDestinations,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var rows []models.OutboxDelivery
			query := applyOutboxClaimLane(
				dryRun.Model(&models.OutboxDelivery{}),
				test.lane,
			).Find(&rows)
			assertLifecycleDryRunSQLContains(
				t,
				query.Statement.SQL.String(),
				test.sqlFragment,
			)
			if len(query.Statement.Vars) != len(test.destinations) {
				t.Fatalf(
					"%s lane bind count = %d, want %d",
					test.lane,
					len(query.Statement.Vars),
					len(test.destinations),
				)
			}
			for index, destination := range test.destinations {
				if query.Statement.Vars[index] != destination {
					t.Fatalf(
						"%s lane bind %d = %v, want %q",
						test.lane,
						index,
						query.Statement.Vars[index],
						destination,
					)
				}
			}
		})
	}

	var rows []models.OutboxDelivery
	failClosed := applyOutboxClaimLane(
		dryRun.Model(&models.OutboxDelivery{}),
		OutboxDeliveryLaneWebhook,
	).Find(&rows)
	assertLifecycleDryRunSQLContains(
		t,
		failClosed.Statement.SQL.String(),
		"1 = 0",
	)
}

func assertLifecycleDryRunSQLContains(
	t *testing.T,
	sql string,
	fragments ...string,
) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(sql, fragment) {
			t.Fatalf(
				"production lifecycle SQL missing %q:\n%s",
				fragment,
				sql,
			)
		}
	}
}

func assertLifecycleDryRunSQLExcludes(
	t *testing.T,
	sql string,
	fragments ...string,
) {
	t.Helper()
	for _, fragment := range fragments {
		if strings.Contains(sql, fragment) {
			t.Fatalf(
				"production lifecycle SQL unexpectedly contains %q:\n%s",
				fragment,
				sql,
			)
		}
	}
}

func TestWebhookOutboxCleanupLimitIsSharedAcrossAllCandidateClasses(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	expiryDelivery := fixture.delivery
	expirySnapshot := fixture.snapshot
	successDelivery, _, _ := fixture.createIntent(t, "legacy-success")
	_, _, _ = fixture.createIntent(t, "overlap-only")
	cleanupAt := now.Add(2 * time.Hour)
	dueAt := cleanupAt.Add(-time.Second)
	if err := fixture.db.Exec(
		"UPDATE outbox_deliveries SET expires_at = ? WHERE id = ?",
		dueAt,
		expiryDelivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(
		"UPDATE webhook_delivery_snapshots "+
			"SET credential_expires_at = ? WHERE id = ?",
		dueAt,
		expirySnapshot.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", successDelivery.ID).
		Updates(map[string]any{
			"status":       models.OutboxDeliverySucceeded,
			"delivered_at": now.Add(time.Minute),
		}).Error; err != nil {
		t.Fatal(err)
	}
	fixture.setNow(cleanupAt)

	first, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstChanged := first.Expired +
		first.LegacySucceededShredded +
		first.OverlapCleared
	if first.Attempted != 2 || firstChanged != 2 {
		t.Fatalf("shared limit first batch = %+v", first)
	}
	second, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondChanged := second.Expired +
		second.LegacySucceededShredded +
		second.OverlapCleared
	if second.Attempted != 1 || secondChanged != 1 {
		t.Fatalf("shared limit second batch = %+v", second)
	}
}
