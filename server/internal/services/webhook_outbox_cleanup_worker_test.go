package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestWebhookOutboxCleanupContinuesAfterMalformedCandidate(t *testing.T) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	firstDelivery := fixture.delivery
	firstSnapshot := fixture.snapshot
	secondDelivery, secondSnapshot, _ := fixture.createIntent(t, "valid-after-bad")
	badSnapshot := firstSnapshot
	badDelivery := firstDelivery
	if secondSnapshot.ID < badSnapshot.ID {
		badSnapshot, secondSnapshot = secondSnapshot, badSnapshot
		badDelivery, secondDelivery = secondDelivery, badDelivery
	}
	if err := fixture.db.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(
		"UPDATE webhook_delivery_snapshots SET event_id = ? WHERE id = ?",
		"00000000-0000-7000-8000-000000000099",
		badSnapshot.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	fixture.setNow(badSnapshot.CredentialExpiresAt.Add(time.Second))

	result, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		2,
	)
	if !errors.Is(err, ErrWebhookOutboxLifecycleInvariant) {
		t.Fatalf("cleanup error = %v, want fixed invariant", err)
	}
	if result.Attempted != 2 ||
		result.Malformed != 1 ||
		result.Expired != 1 {
		t.Fatalf("cleanup did not continue after malformed row: %+v", result)
	}
	var valid models.OutboxDelivery
	if err := fixture.db.First(
		&valid,
		"id = ?",
		secondDelivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if valid.Status != models.OutboxDeliveryExpired {
		t.Fatalf("valid later candidate status = %s", valid.Status)
	}
	var malformed models.OutboxDelivery
	if err := fixture.db.First(
		&malformed,
		"id = ?",
		badDelivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if malformed.Status == models.OutboxDeliveryExpired {
		t.Fatal("malformed pair was silently terminalized")
	}
}

func TestWebhookOutboxCleanupCursorAdvancesPastPersistentMalformedLimitOne(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	firstDelivery := fixture.delivery
	secondDelivery, _, _ := fixture.createIntent(t, "cursor-valid")
	if secondDelivery.DestinationID < firstDelivery.DestinationID ||
		(secondDelivery.DestinationID == firstDelivery.DestinationID &&
			secondDelivery.ID < firstDelivery.ID) {
		firstDelivery, secondDelivery = secondDelivery, firstDelivery
	}
	if err := fixture.db.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(
		`UPDATE webhook_delivery_snapshots
		 SET event_id = ?
		 WHERE id = (
			SELECT substr(destination_id, length(?) + 1)
			FROM outbox_deliveries
			WHERE id = ?
		 )`,
		"00000000-0000-7000-8000-000000000099",
		models.WebhookDeliverySnapshotDestinationPrefix,
		firstDelivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(time.Second))

	first, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		1,
	)
	if !errors.Is(err, ErrWebhookOutboxLifecycleInvariant) ||
		first.Attempted != 1 ||
		first.Malformed != 1 {
		t.Fatalf("first malformed cleanup = %+v err=%v", first, err)
	}
	reachedValid := false
	var observed []WebhookOutboxCleanupResult
	for attempt := 0; attempt < 3; attempt++ {
		next, nextErr := fixture.service.ExpireWebhookDeliveriesBatch(
			context.Background(),
			1,
		)
		if nextErr != nil &&
			!errors.Is(nextErr, ErrWebhookOutboxLifecycleInvariant) {
			t.Fatal(nextErr)
		}
		observed = append(observed, next)
		reachedValid = reachedValid || next.Expired == 1
	}
	if !reachedValid {
		t.Fatalf("cursor did not advance to valid row: %+v", observed)
	}
	var valid models.OutboxDelivery
	if err := fixture.db.First(
		&valid,
		"id = ?",
		secondDelivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if valid.Status != models.OutboxDeliveryExpired {
		t.Fatalf("valid cursor successor status = %s", valid.Status)
	}
}

func TestWebhookOutboxLegacyRepairCursorAdvancesPastMalformedLimitOne(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	firstDelivery := fixture.delivery
	secondDelivery, _, _ := fixture.createIntent(t, "legacy-cursor-valid")
	if secondDelivery.DestinationID < firstDelivery.DestinationID ||
		(secondDelivery.DestinationID == firstDelivery.DestinationID &&
			secondDelivery.ID < firstDelivery.ID) {
		firstDelivery, secondDelivery = secondDelivery, firstDelivery
	}
	deliveredAt := now.Add(-time.Second)
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id IN ?", []string{firstDelivery.ID, secondDelivery.ID}).
		Updates(map[string]any{
			"status":       models.OutboxDeliverySucceeded,
			"delivered_at": deliveredAt,
		}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(
		`UPDATE webhook_delivery_snapshots
		 SET event_id = ?
		 WHERE id = (
			SELECT substr(destination_id, length(?) + 1)
			FROM outbox_deliveries
			WHERE id = ?
		 )`,
		"00000000-0000-7000-8000-000000000098",
		models.WebhookDeliverySnapshotDestinationPrefix,
		firstDelivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}

	first, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		1,
	)
	if !errors.Is(err, ErrWebhookOutboxLifecycleInvariant) ||
		first.Attempted != 1 ||
		first.Malformed != 1 ||
		first.LegacySucceededShredded != 0 {
		t.Fatalf("first malformed legacy repair = %+v err=%v", first, err)
	}
	second, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Attempted != 1 ||
		second.LegacySucceededShredded != 1 {
		t.Fatalf("legacy cursor did not reach valid row: %+v", second)
	}
}

func TestWebhookOutboxLegacyRawScanBudgetIsGlobalAcrossProjects(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	for projectIndex := 0; projectIndex < 101; projectIndex++ {
		project := seedLifecycleWorkerProject(
			t,
			fixture,
			100+projectIndex,
		)
		pairs := seedLifecycleLegacySucceededPairs(
			t,
			fixture,
			project,
			10_000+projectIndex*2,
			2,
		)
		for index := range pairs {
			shredLifecycleLegacySnapshotSucceeded(
				t,
				fixture,
				pairs[index].snapshot.ID,
			)
		}
	}
	var rawRows atomic.Int64
	const callbackName = "test:legacy_raw_scan_global_budget"
	if err := fixture.db.Callback().Query().After("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			sql := strings.ToLower(tx.Statement.SQL.String())
			if strings.Contains(sql, "snapshot_shredded") &&
				strings.Contains(sql, "status = 'succeeded'") {
				rawRows.Add(tx.RowsAffected)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Query().Remove(callbackName)
	})

	result, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		200,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempted != 0 {
		t.Fatalf("canonical legacy rows became candidates: %+v", result)
	}
	if got := rawRows.Load(); got > webhookOutboxLegacyScanPageSize {
		t.Fatalf(
			"legacy raw scan rows = %d, global budget = %d",
			got,
			webhookOutboxLegacyScanPageSize,
		)
	}
}

func TestWebhookOutboxLegacyRepairIsReachablePastCanonicalRawPage(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	var project models.Project
	if err := fixture.db.First(
		&project,
		"id = ? AND organization_id = ?",
		fixture.scope.ProjectID,
		fixture.scope.OrganizationID,
	).Error; err != nil {
		t.Fatal(err)
	}
	pairs := seedLifecycleLegacySucceededPairs(
		t,
		fixture,
		project,
		20_000,
		webhookOutboxLegacyScanPageSize+1,
	)
	sort.Slice(pairs, func(left, right int) bool {
		if pairs[left].delivery.DestinationID !=
			pairs[right].delivery.DestinationID {
			return pairs[left].delivery.DestinationID <
				pairs[right].delivery.DestinationID
		}
		return pairs[left].delivery.ID < pairs[right].delivery.ID
	})
	for index := 0; index < webhookOutboxLegacyScanPageSize; index++ {
		shredLifecycleLegacySnapshotSucceeded(
			t,
			fixture,
			pairs[index].snapshot.ID,
		)
	}
	var rawRows atomic.Int64
	const callbackName = "test:legacy_raw_scan_repair_reachable"
	if err := fixture.db.Callback().Query().After("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			sql := strings.ToLower(tx.Statement.SQL.String())
			if strings.Contains(sql, "snapshot_shredded") &&
				strings.Contains(sql, "status = 'succeeded'") {
				rawRows.Add(tx.RowsAffected)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Query().Remove(callbackName)
	})

	first, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstRawRows := rawRows.Load()
	if first.Attempted != 0 ||
		first.LegacySucceededShredded != 0 ||
		firstRawRows != webhookOutboxLegacyScanPageSize {
		t.Fatalf(
			"first legacy page result=%+v raw=%d, want 200 canonical rows",
			first,
			firstRawRows,
		)
	}
	second, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondRawRows := rawRows.Load() - firstRawRows
	if second.Attempted != 1 ||
		second.LegacySucceededShredded != 1 ||
		secondRawRows != 1 {
		t.Fatalf(
			"second legacy page result=%+v raw=%d, want one repair",
			second,
			secondRawRows,
		)
	}
	var repaired models.WebhookDeliverySnapshot
	if err := fixture.db.First(
		&repaired,
		"id = ?",
		pairs[len(pairs)-1].snapshot.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if repaired.CredentialShreddedAt == nil ||
		repaired.CredentialShredReason == nil ||
		*repaired.CredentialShredReason !=
			models.WebhookCredentialShredReasonSucceeded {
		t.Fatalf("tail legacy snapshot was not repaired: %+v", repaired)
	}
}

func TestWebhookOutboxLegacyCanonicalShredWithMalformedDestinationIsReported(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	claimed, claim := fixture.claim(t, "legacy-prefix-success-worker")
	fixture.startDispatch(t, claimed.ID)
	claim.EffectiveDeadline = claim.LockedAt.Add(time.Minute)
	if _, err := fixture.service.FinalizeOutboxAttempt(
		fixture.worker,
		claim,
		OutboxKnownSuccess(claim.LockedAt.Add(time.Second)),
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", fixture.delivery.ID).
		Update(
			"destination_id",
			"bad-shot:"+fixture.snapshot.ID,
		).Error; err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		1,
	)
	if !errors.Is(err, ErrWebhookOutboxLifecycleInvariant) ||
		result.Attempted != 1 ||
		result.Malformed != 1 ||
		result.LegacySucceededShredded != 0 {
		t.Fatalf(
			"malformed canonical legacy result=%+v err=%v",
			result,
			err,
		)
	}
}

func TestWebhookOutboxLegacyWrongShredReasonIsReported(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(time.Second))
	if result, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		1,
	); err != nil || result.Expired != 1 {
		t.Fatalf("seed expired result=%+v err=%v", result, err)
	}
	deliveredAt := fixture.snapshot.CredentialExpiresAt.Add(-time.Second)
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", fixture.delivery.ID).
		Updates(map[string]any{
			"status":       models.OutboxDeliverySucceeded,
			"expired_at":   nil,
			"delivered_at": deliveredAt,
		}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		1,
	)
	if !errors.Is(err, ErrWebhookOutboxLifecycleInvariant) ||
		result.Attempted != 1 ||
		result.Malformed != 1 ||
		result.LegacySucceededShredded != 0 {
		t.Fatalf(
			"wrong legacy shred reason result=%+v err=%v",
			result,
			err,
		)
	}
}

func TestWebhookOutboxCleanupCategoriesRemainFairWithLimitOne(
	t *testing.T,
) {
	t.Run("new expiry reaches persistent overlap backlog", func(t *testing.T) {
		now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
		fixture := newWebhookOutboxLifecycleFixture(t, now)
		for index := 0; index < 6; index++ {
			fixture.createIntent(
				t,
				fmt.Sprintf("overlap-backlog-%d", index),
			)
		}
		cleanupAt := now.Add(2 * time.Hour)
		fixture.setNow(cleanupAt)
		first, err := fixture.service.ExpireWebhookDeliveriesBatch(
			context.Background(),
			1,
		)
		if err != nil || first.OverlapCleared != 1 {
			t.Fatalf("seed overlap cleanup = %+v err=%v", first, err)
		}

		dueDelivery, dueSnapshot, _ :=
			fixture.createIntent(t, "new-expiry")
		dueAt := cleanupAt.Add(-time.Second)
		if err := fixture.db.Exec(
			"UPDATE outbox_deliveries SET expires_at = ? WHERE id = ?",
			dueAt,
			dueDelivery.ID,
		).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.db.Exec(
			"UPDATE webhook_delivery_snapshots "+
				"SET credential_expires_at = ? WHERE id = ?",
			dueAt,
			dueSnapshot.ID,
		).Error; err != nil {
			t.Fatal(err)
		}

		reachedExpiry := false
		for attempt := 0; attempt < 3; attempt++ {
			result, cleanupErr :=
				fixture.service.ExpireWebhookDeliveriesBatch(
					context.Background(),
					1,
				)
			if cleanupErr != nil {
				t.Fatal(cleanupErr)
			}
			reachedExpiry = reachedExpiry || result.Expired == 1
		}
		if !reachedExpiry {
			t.Fatal(
				"new expiry was starved behind persistent overlap backlog",
			)
		}
	})

	t.Run("new overlap reaches persistent expiry backlog", func(t *testing.T) {
		now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
		fixture := newWebhookOutboxLifecycleFixture(t, now)
		pairs := [][2]string{{
			fixture.delivery.ID,
			fixture.snapshot.ID,
		}}
		for index := 0; index < 6; index++ {
			delivery, snapshot, _ := fixture.createIntent(
				t,
				fmt.Sprintf("expiry-backlog-%d", index),
			)
			pairs = append(
				pairs,
				[2]string{delivery.ID, snapshot.ID},
			)
		}
		_, overlapSnapshot, _ :=
			fixture.createIntent(t, "new-overlap")
		cleanupAt := now.Add(2 * time.Hour)
		dueAt := cleanupAt.Add(-time.Second)
		for _, pair := range pairs {
			if err := fixture.db.Exec(
				"UPDATE outbox_deliveries SET expires_at = ? WHERE id = ?",
				dueAt,
				pair[0],
			).Error; err != nil {
				t.Fatal(err)
			}
			if err := fixture.db.Exec(
				"UPDATE webhook_delivery_snapshots "+
					"SET credential_expires_at = ?, "+
					"previous_secret = '', "+
					"previous_secret_expires_at = NULL "+
					"WHERE id = ?",
				dueAt,
				pair[1],
			).Error; err != nil {
				t.Fatal(err)
			}
		}
		fixture.setNow(cleanupAt)
		first, err := fixture.service.ExpireWebhookDeliveriesBatch(
			context.Background(),
			1,
		)
		if err != nil || first.Expired != 1 {
			t.Fatalf("seed expiry cleanup = %+v err=%v", first, err)
		}

		reachedOverlap := false
		var observed []WebhookOutboxCleanupResult
		for attempt := 0; attempt < 3; attempt++ {
			result, cleanupErr :=
				fixture.service.ExpireWebhookDeliveriesBatch(
					context.Background(),
					1,
				)
			if cleanupErr != nil {
				t.Fatal(cleanupErr)
			}
			reachedOverlap =
				reachedOverlap || result.OverlapCleared == 1
			observed = append(observed, result)
		}
		if !reachedOverlap {
			t.Fatalf(
				"new overlap was starved behind persistent expiry backlog: %+v",
				observed,
			)
		}
		var stored models.WebhookDeliverySnapshot
		if err := fixture.db.First(
			&stored,
			"id = ?",
			overlapSnapshot.ID,
		).Error; err != nil {
			t.Fatal(err)
		}
		if stored.PreviousSecretExpiresAt != nil {
			t.Fatal("fair overlap candidate was not cleaned")
		}
	})
}

func TestWebhookOutboxCleanupBoundsIdleProjectScanByGlobalLimit(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	var template models.Project
	if err := fixture.db.First(
		&template,
		"id = ?",
		fixture.scope.ProjectID,
	).Error; err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 12; index++ {
		project := template
		project.ID = 0
		project.PublicID = ""
		project.CreatedAt = time.Time{}
		project.UpdatedAt = time.Time{}
		project.Key = models.ProjectKey(fmt.Sprintf("IDLE%d", index))
		project.Name = fmt.Sprintf("idle cleanup project %d", index)
		if err := fixture.db.Create(&project).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.db.Exec(
		"DELETE FROM outbox_deliveries",
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(
		"DELETE FROM webhook_delivery_snapshots",
	).Error; err != nil {
		t.Fatal(err)
	}

	var candidateQueries atomic.Int32
	const callbackName = "test:webhook_cleanup_project_budget"
	if err := fixture.db.Callback().Query().After("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			sql := strings.ToLower(tx.Statement.SQL.String())
			if strings.Contains(sql, "from `outbox_deliveries`") ||
				strings.Contains(
					sql,
					"from `webhook_delivery_snapshots`",
				) {
				candidateQueries.Add(1)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Query().Remove(callbackName)
	})

	result, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempted != 0 {
		t.Fatalf("idle cleanup attempted candidates: %+v", result)
	}
	if got := candidateQueries.Load(); got != 3 {
		t.Fatalf(
			"idle cleanup candidate queries = %d, want one project's 3 bounded queries",
			got,
		)
	}
}

func TestWebhookOutboxClaimBoundsIdleProjectScanByGlobalLimit(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	var template models.Project
	if err := fixture.db.First(
		&template,
		"id = ?",
		fixture.scope.ProjectID,
	).Error; err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 12; index++ {
		project := template
		project.ID = 0
		project.PublicID = ""
		project.CreatedAt = time.Time{}
		project.UpdatedAt = time.Time{}
		project.Key = models.ProjectKey(fmt.Sprintf("CLAIM%d", index))
		project.Name = fmt.Sprintf("idle claim project %d", index)
		if err := fixture.db.Create(&project).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.db.Exec(
		"DELETE FROM outbox_deliveries",
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(
		"DELETE FROM webhook_delivery_snapshots",
	).Error; err != nil {
		t.Fatal(err)
	}

	var (
		candidateQueries atomic.Int32
		adapterCalls     atomic.Int32
	)
	const callbackName = "test:webhook_claim_project_budget"
	if err := fixture.db.Callback().Query().After("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			sql := strings.ToLower(tx.Statement.SQL.String())
			if strings.Contains(sql, "from `outbox_deliveries`") {
				candidateQueries.Add(1)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Query().Remove(callbackName)
	})

	result, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"idle-project-budget-worker",
		1,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			adapterCalls.Add(1)
			return errors.New("idle claim unexpectedly reached adapter")
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Claimed != 0 {
		t.Fatalf("idle claim returned deliveries: %+v", result)
	}
	if adapterCalls.Load() != 0 {
		t.Fatalf("idle claim made %d adapter calls", adapterCalls.Load())
	}
	if got := candidateQueries.Load(); got != 5 {
		t.Fatalf(
			"idle claim candidate queries = %d, want one project's 5 bounded class scans",
			got,
		)
	}
}
