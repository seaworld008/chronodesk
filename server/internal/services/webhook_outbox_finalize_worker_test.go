package services

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestWebhookOutboxWorkerConsumesClosedAttemptResultSet(t *testing.T) {
	for _, test := range []struct {
		name       string
		result     func(time.Time) OutboxAttemptResult
		wantStatus models.OutboxDeliveryStatus
	}{
		{
			name: "known_success",
			result: func(now time.Time) OutboxAttemptResult {
				return OutboxKnownSuccess(now)
			},
			wantStatus: models.OutboxDeliverySucceeded,
		},
		{
			name: "known_failure_no_side_effect",
			result: func(time.Time) OutboxAttemptResult {
				return OutboxKnownFailure(
					errors.New("destination rejected before side effect"),
				)
			},
			wantStatus: models.OutboxDeliveryFailed,
		},
		{
			name: "uncertain",
			result: func(time.Time) OutboxAttemptResult {
				return OutboxUncertain(context.DeadlineExceeded)
			},
			wantStatus: models.OutboxDeliveryExpired,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
			fixture := newWebhookOutboxLifecycleFixture(t, now)
			deliverer := &lifecycleRichDeliverer{
				result: test.result(now),
			}
			batch, err := fixture.service.ProcessOutboxBatch(
				context.Background(),
				"rich-result-worker",
				1,
				deliverer,
			)
			if err != nil {
				t.Fatal(err)
			}
			if deliverer.richCalls.Load() != 1 ||
				deliverer.legacyCalls.Load() != 0 {
				t.Fatalf(
					"deliverer calls rich=%d legacy=%d",
					deliverer.richCalls.Load(),
					deliverer.legacyCalls.Load(),
				)
			}
			var delivery models.OutboxDelivery
			if err := fixture.db.First(
				&delivery,
				"id = ?",
				fixture.delivery.ID,
			).Error; err != nil {
				t.Fatal(err)
			}
			if delivery.Status != test.wantStatus {
				t.Fatalf(
					"delivery status = %s, want %s; batch=%+v",
					delivery.Status,
					test.wantStatus,
					batch,
				)
			}
		})
	}
}

func TestWebhookOutboxWorkerPreservesSuccessAtDeadlineHandoff(
	t *testing.T,
) {
	now := time.Now().UTC()
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	deadline := now.Add(50 * time.Millisecond)
	if err := fixture.db.Exec(
		"UPDATE outbox_deliveries SET expires_at = ? WHERE id = ?",
		deadline,
		fixture.delivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(
		"UPDATE webhook_delivery_snapshots "+
			"SET credential_expires_at = ? WHERE id = ?",
		deadline,
		fixture.snapshot.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	batch, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"deadline-handoff-worker",
		1,
		lifecycleDeadlineHandoffDeliverer{
			completedAt: deadline.Add(-time.Millisecond),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var delivery models.OutboxDelivery
	if err := fixture.db.First(
		&delivery,
		"id = ?",
		fixture.delivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if batch.Delivered != 1 ||
		batch.Expired != 0 ||
		delivery.Status != models.OutboxDeliverySucceeded {
		t.Fatalf(
			"deadline handoff lost timely success: batch=%+v delivery=%+v",
			batch,
			delivery,
		)
	}
}

func TestWebhookOutboxWorkerPreservesKnownFailureAtDeadlineHandoff(
	t *testing.T,
) {
	now := time.Now().UTC()
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	fixture.service.outboxDeliveryTimeout = 40 * time.Millisecond
	batch, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"known-failure-deadline-worker",
		1,
		lifecycleDeadlineKnownFailureDeliverer{},
	)
	if err != nil {
		t.Fatal(err)
	}
	delivery, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		fixture.delivery.ID,
		fixture.snapshot.ID,
	)
	if batch.Failed != 1 ||
		batch.Expired != 0 ||
		delivery.Status != models.OutboxDeliveryFailed ||
		snapshot.CredentialShreddedAt != nil {
		t.Fatalf(
			"timely known failure lost at deadline: batch=%+v delivery=%+v snapshot=%+v",
			batch,
			delivery,
			snapshot,
		)
	}
}

func TestWebhookOutboxBatchStartsSuccessorWithFullWindowAfterCapacityFrees(
	t *testing.T,
) {
	now := time.Now().UTC()
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	second, secondSnapshot, _ := fixture.createIntent(
		t,
		"capacity-successor",
	)
	fixture.service.outboxDeliverySlots = make(chan struct{}, 1)
	fixture.service.outboxDeliveryTimeout = 40 * time.Millisecond
	var calls atomic.Int32
	batch, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"capacity-bound-worker",
		2,
		OutboxDeliverFunc(func(
			ctx context.Context,
			_ *models.OutboxDelivery,
			_ CloudEventEnvelope,
		) error {
			if calls.Add(1) > 1 {
				return nil
			}
			<-ctx.Done()
			return ctx.Err()
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Claimed != 2 ||
		batch.Delivered != 1 ||
		batch.Expired != 1 ||
		calls.Load() != 2 {
		t.Fatalf(
			"capacity-bound batch=%+v adapter calls=%d",
			batch,
			calls.Load(),
		)
	}
	delivery, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		second.ID,
		secondSnapshot.ID,
	)
	if delivery.Status != models.OutboxDeliverySucceeded ||
		delivery.Attempts != 1 ||
		delivery.LockedAt != nil ||
		delivery.LockToken != nil ||
		snapshot.CredentialShreddedAt == nil ||
		snapshot.CredentialShredReason == nil ||
		*snapshot.CredentialShredReason !=
			models.WebhookCredentialShredReasonSucceeded {
		t.Fatalf(
			"successor did not receive a full attempt window: delivery=%+v snapshot=%+v",
			delivery,
			snapshot,
		)
	}
}

func TestWebhookOutboxLegacyErrorRemainsKnownFailure(t *testing.T) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	batch, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"legacy-error-worker",
		1,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			return errors.New("ordinary safe destination rejection")
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Failed != 1 || batch.Expired != 0 {
		t.Fatalf("legacy error batch = %+v, want known failure", batch)
	}
	delivery, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		fixture.delivery.ID,
		fixture.snapshot.ID,
	)
	if delivery.Status != models.OutboxDeliveryFailed ||
		snapshot.CredentialShreddedAt != nil {
		t.Fatalf(
			"legacy error result changed semantics: delivery=%+v snapshot=%+v",
			delivery,
			snapshot,
		)
	}
}

func TestWebhookOutboxWorkerFreezesClaimBeforeAdapterMutation(t *testing.T) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	deliverer := &lifecycleRichDeliverer{
		result: OutboxKnownSuccess(now.Add(time.Millisecond)),
		mutate: func(delivery *models.OutboxDelivery) {
			delivery.LockedBy = "adapter-mutated-worker"
			delivery.LockedAt = nil
			delivery.Attempts = 999
			token := "adapter-mutated-token"
			delivery.LockToken = &token
		},
	}
	batch, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"frozen-claim-worker",
		1,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Delivered != 1 || batch.Claimed != 1 {
		t.Fatalf("adapter mutation changed frozen claim: %+v", batch)
	}
	var current models.OutboxDelivery
	if err := fixture.db.First(
		&current,
		"id = ?",
		fixture.delivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != models.OutboxDeliverySucceeded {
		t.Fatalf("adapter mutation left status %s", current.Status)
	}
}

func TestWebhookOutboxPanicIsUncertainAndExpires(t *testing.T) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	batch, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"panic-uncertain-worker",
		1,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			panic("credential-shaped panic content")
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Expired != 1 || batch.Failed != 0 {
		t.Fatalf("panic batch = %+v, want uncertain expiry", batch)
	}
	delivery, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		fixture.delivery.ID,
		fixture.snapshot.ID,
	)
	if delivery.LastError != "webhook delivery result is uncertain" {
		t.Fatalf("panic detail was not fixed: %q", delivery.LastError)
	}
	assertSnapshotShredded(
		t,
		snapshot,
		models.WebhookCredentialShredReasonExpired,
	)
}

func TestWebhookOutboxProcessingWithoutCompleteLockNeverReachesAdapter(
	t *testing.T,
) {
	for _, test := range []struct {
		name     string
		lockedAt *time.Time
		lockedBy string
	}{
		{name: "null_lock"},
		{
			name: "empty_worker",
			lockedAt: func() *time.Time {
				value := time.Date(
					2026,
					time.August,
					10,
					8,
					0,
					0,
					0,
					time.UTC,
				)
				return &value
			}(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
			fixture := newWebhookOutboxLifecycleFixture(t, now)
			if err := fixture.db.Model(&models.OutboxDelivery{}).
				Where("id = ?", fixture.delivery.ID).
				Updates(map[string]any{
					"status":    models.OutboxDeliveryProcessing,
					"attempts":  1,
					"locked_at": test.lockedAt,
					"locked_by": test.lockedBy,
				}).Error; err != nil {
				t.Fatal(err)
			}
			var attempts atomic.Int32
			batch, err := fixture.service.ProcessOutboxBatch(
				context.Background(),
				"incomplete-lock-worker",
				1,
				OutboxDeliverFunc(func(
					context.Context,
					*models.OutboxDelivery,
					CloudEventEnvelope,
				) error {
					attempts.Add(1)
					return nil
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			if attempts.Load() != 0 || batch.Claimed != 0 {
				t.Fatalf(
					"incomplete lock reached adapter: attempts=%d batch=%+v",
					attempts.Load(),
					batch,
				)
			}
			fixture.setNow(
				fixture.snapshot.CredentialExpiresAt.Add(time.Second),
			)
			cleanup, err := fixture.service.ExpireWebhookDeliveriesBatch(
				context.Background(),
				1,
			)
			if err != nil {
				t.Fatal(err)
			}
			if cleanup.Expired != 1 {
				t.Fatalf("due incomplete lock cleanup = %+v", cleanup)
			}
		})
	}
}

func TestWebhookOutboxExhaustedStaleClaimBecomesDeadWithoutAdapter(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	staleAt := now.Add(-2 * time.Minute)
	staleToken := "018f3f7e-7b22-7cc0-8000-000000000009"
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", fixture.delivery.ID).
		Updates(map[string]any{
			"status":     models.OutboxDeliveryProcessing,
			"attempts":   fixture.delivery.MaxAttempts,
			"locked_at":  staleAt,
			"locked_by":  "exhausted-stale-worker",
			"lock_token": staleToken,
		}).Error; err != nil {
		t.Fatal(err)
	}
	var adapterCalls atomic.Int32
	batch, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"must-not-redeliver-exhausted-stale",
		1,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			adapterCalls.Add(1)
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	delivery, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		fixture.delivery.ID,
		fixture.snapshot.ID,
	)
	if adapterCalls.Load() != 0 ||
		batch.Claimed != 0 ||
		batch.Dead != 1 ||
		batch.Failed != 1 ||
		delivery.Status != models.OutboxDeliveryDead ||
		delivery.Attempts != delivery.MaxAttempts ||
		delivery.LockedAt != nil ||
		delivery.LockedBy != "" ||
		delivery.LockToken != nil ||
		snapshot.CredentialShreddedAt != nil {
		t.Fatalf(
			"exhausted stale state: calls=%d batch=%+v delivery=%+v snapshot=%+v",
			adapterCalls.Load(),
			batch,
			delivery,
			snapshot,
		)
	}
}
