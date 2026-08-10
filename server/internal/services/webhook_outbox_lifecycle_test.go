package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

type webhookOutboxLifecycleFixture struct {
	db       *gorm.DB
	service  *AgentNativeService
	scope    models.ProjectScope
	worker   context.Context
	clock    *time.Time
	config   models.WebhookConfig
	delivery models.OutboxDelivery
	snapshot models.WebhookDeliverySnapshot
	event    models.DomainEvent
}

func newWebhookOutboxLifecycleFixture(
	t *testing.T,
	now time.Time,
) *webhookOutboxLifecycleFixture {
	t.Helper()
	db := openAgentNativeTestDB(t)
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	actor := models.SystemActor("webhook-lifecycle-producer")
	producer := testProjectOperationContext(t, db, actor)
	scope, err := RequireProjectScope(producer)
	if err != nil {
		t.Fatal(err)
	}
	user := seedActorUser(t, db, strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")))
	overlapExpiry := now.Add(90 * time.Minute)
	config := models.WebhookConfig{
		OrganizationID:          scope.OrganizationID,
		ProjectID:               scope.ProjectID,
		Name:                    "lifecycle fixture",
		Provider:                models.WebhookProviderCustom,
		WebhookURL:              "https://lifecycle.invalid.example/events",
		Status:                  models.WebhookStatusActive,
		Secret:                  "sealed-current-envelope",
		PreviousSecret:          "sealed-previous-envelope",
		PreviousSecretExpiresAt: &overlapExpiry,
		AccessToken:             "sealed-access-envelope",
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventTicketCreated,
		},
		RetryCount:    8,
		RetryInterval: 60,
		CreatedBy:     user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	clock := now.UTC()
	service := NewAgentNativeService(db, AgentNativeOptions{
		DefaultOutboxTargets: []OutboxTarget{{
			Type:        "webhook",
			ID:          "configured",
			MaxAttempts: 8,
		}},
		OutboxLockTTL: time.Minute,
		Now: func() time.Time {
			return clock
		},
	})
	worker, err := EnsureSystemProjectOperationContext(
		context.Background(),
		scope,
		models.SystemActor(outboxSystemActorID),
		"webhook-lifecycle-worker",
		"webhook-lifecycle-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &webhookOutboxLifecycleFixture{
		db:      db,
		service: service,
		scope:   scope,
		worker:  worker,
		clock:   &clock,
		config:  config,
	}
	fixture.createIntent(t, "initial")
	return fixture
}

func (fixture *webhookOutboxLifecycleFixture) createIntent(
	t *testing.T,
	suffix string,
) (models.OutboxDelivery, models.WebhookDeliverySnapshot, models.DomainEvent) {
	t.Helper()
	event, err := fixture.service.createDomainEvent(
		t,
		contextWithProjectScope(
			t,
			fixture.scope,
			models.SystemActor("webhook-lifecycle-producer"),
		),
		DomainEventInput{
			Type:            "io.chronodesk.ticket.created.v1",
			Subject:         "ticket/lifecycle-" + suffix,
			Actor:           models.SystemActor("webhook-lifecycle-producer"),
			ResourceVersion: 1,
			Data:            map[string]any{"suffix": suffix},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	var delivery models.OutboxDelivery
	if err := fixture.db.Where(
		"event_id = ? AND destination_type = ?",
		event.ID,
		"webhook",
	).Take(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot models.WebhookDeliverySnapshot
	if err := fixture.db.Where(
		"event_id = ?",
		event.ID,
	).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	fixture.delivery = delivery
	fixture.snapshot = snapshot
	fixture.event = *event
	return delivery, snapshot, *event
}

func contextWithProjectScope(
	t *testing.T,
	scope models.ProjectScope,
	actor models.ActorRef,
) context.Context {
	t.Helper()
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:  scope,
		Actor:  actor,
		Source: SourceProtocolWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func (fixture *webhookOutboxLifecycleFixture) setNow(now time.Time) {
	*fixture.clock = now.UTC()
}

func (fixture *webhookOutboxLifecycleFixture) claim(
	t *testing.T,
	workerID string,
) (*models.OutboxDelivery, OutboxClaimRef) {
	t.Helper()
	claimed, err := fixture.service.ClaimPendingOutbox(
		fixture.worker,
		workerID,
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed deliveries = %d, want 1", len(claimed))
	}
	ref, err := OutboxClaimRefFromDelivery(claimed[0])
	if err != nil {
		t.Fatal(err)
	}
	return claimed[0], ref
}

func loadWebhookLifecycleRows(
	t *testing.T,
	fixture *webhookOutboxLifecycleFixture,
	deliveryID string,
	snapshotID string,
) (models.OutboxDelivery, models.WebhookDeliverySnapshot) {
	t.Helper()
	var delivery models.OutboxDelivery
	if err := fixture.db.First(&delivery, "id = ?", deliveryID).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot models.WebhookDeliverySnapshot
	if err := fixture.db.First(&snapshot, "id = ?", snapshotID).Error; err != nil {
		t.Fatal(err)
	}
	return delivery, snapshot
}

func assertSnapshotShredded(
	t *testing.T,
	snapshot models.WebhookDeliverySnapshot,
	reason models.WebhookCredentialShredReason,
) {
	t.Helper()
	if snapshot.CredentialShreddedAt == nil ||
		snapshot.CredentialShredReason == nil ||
		*snapshot.CredentialShredReason != reason ||
		snapshot.Secret != "" ||
		snapshot.PreviousSecret != "" ||
		snapshot.PreviousSecretExpiresAt != nil ||
		snapshot.AccessToken != "" {
		t.Fatalf("snapshot was not fully shredded: %+v", snapshot)
	}
}

func TestWebhookOutboxClaimRejectsExpiredCandidateAndCAS(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(time.Nanosecond))

	claimed, err := fixture.service.ClaimPendingOutbox(
		fixture.worker,
		"expired-claim-worker",
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("expired webhook was claimed: %+v", claimed[0])
	}
	var current models.OutboxDelivery
	if err := fixture.db.First(&current, "id = ?", fixture.delivery.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != models.OutboxDeliveryPending ||
		current.Attempts != 0 ||
		current.LockedAt != nil {
		t.Fatalf("expired claim CAS mutated delivery: %+v", current)
	}
}

func TestWebhookOutboxClaimCASRejectsReplayedCandidateGeneration(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", fixture.delivery.ID).
		Updates(map[string]any{
			"status":          models.OutboxDeliveryFailed,
			"attempts":        2,
			"next_attempt_at": now,
		}).Error; err != nil {
		t.Fatal(err)
	}

	const callbackName = "test:webhook_lifecycle_replay_before_claim_cas"
	var replayed atomic.Bool
	if err := fixture.db.Callback().Update().
		Before("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table !=
				(models.OutboxDelivery{}).TableName() ||
				!replayed.CompareAndSwap(false, true) {
				return
			}
			replay := tx.Session(&gorm.Session{NewDB: true}).Exec(
				`UPDATE outbox_deliveries
				 SET status = ?, attempts = 0, next_attempt_at = ?,
				     locked_at = NULL, locked_by = ''
				 WHERE id = ?`,
				models.OutboxDeliveryPending,
				now,
				fixture.delivery.ID,
			)
			tx.AddError(replay.Error)
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Update().Remove(callbackName)
	})

	claimed, err := fixture.service.ClaimPendingOutbox(
		fixture.worker,
		"replay-generation-worker",
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Load() {
		t.Fatal("replay race callback did not run")
	}
	if len(claimed) != 0 {
		t.Fatalf("claim stole replayed candidate generation: %+v", claimed[0])
	}
	var current models.OutboxDelivery
	if err := fixture.db.First(
		&current,
		"id = ?",
		fixture.delivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != models.OutboxDeliveryPending ||
		current.Attempts != 0 ||
		current.LockedAt != nil ||
		current.LockedBy != "" {
		t.Fatalf("replayed candidate generation changed: %+v", current)
	}
}

func TestWebhookOutboxClaimRejectsNullDeadline(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	if err := fixture.db.Exec("PRAGMA ignore_check_constraints = ON").Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(
		"UPDATE outbox_deliveries SET expires_at = NULL WHERE id = ?",
		fixture.delivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec("PRAGMA ignore_check_constraints = OFF").Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := fixture.service.ClaimPendingOutbox(
		fixture.worker,
		"null-deadline-worker",
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("webhook with NULL deadline was claimed: %+v", claimed[0])
	}
}

func TestWebhookOutboxClaimCASRechecksDeadlineAfterSecondarySelection(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	var expiredAtCAS atomic.Bool
	const callbackName = "test:webhook_claim_expire_after_secondary"
	if err := fixture.db.Callback().Query().After("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement == nil ||
				tx.Statement.Table !=
					(models.OutboxDelivery{}).TableName() ||
				!strings.Contains(
					strings.ToLower(tx.Statement.SQL.String()),
					"webhook_delivery_snapshots",
				) ||
				!strings.Contains(
					strings.ToLower(tx.Statement.SQL.String()),
					"id in (",
				) ||
				!expiredAtCAS.CompareAndSwap(false, true) {
				return
			}
			fixture.setNow(fixture.snapshot.CredentialExpiresAt)
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Query().Remove(callbackName)
	})
	var adapterCalls atomic.Int32
	result, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"deadline-cas-worker",
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
	if !expiredAtCAS.Load() {
		t.Fatal("deadline mutation did not run at claim CAS")
	}
	var delivery models.OutboxDelivery
	if err := fixture.db.First(
		&delivery,
		"id = ?",
		fixture.delivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if result.Claimed != 0 ||
		delivery.Status != models.OutboxDeliveryPending ||
		delivery.Attempts != 0 ||
		delivery.LockedAt != nil ||
		delivery.LockedBy != "" ||
		delivery.LockToken != nil ||
		adapterCalls.Load() != 0 {
		t.Fatalf(
			"deadline CAS mutation batch=%+v delivery=%+v calls=%d",
			result,
			delivery,
			adapterCalls.Load(),
		)
	}
}

func TestWebhookOutboxSuccessAndShredAreAtomic(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	_, claim := fixture.claim(t, "atomic-success-worker")
	injected := errors.New("injected snapshot shred failure")
	const callbackName = "test:webhook_lifecycle_shred_failure"
	if err := fixture.db.Callback().Update().Before("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement != nil &&
				tx.Statement.Table == (models.WebhookDeliverySnapshot{}).TableName() {
				_ = tx.AddError(injected)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Update().Remove(callbackName)
	})

	_, err := fixture.service.FinalizeOutboxAttempt(
		fixture.worker,
		claim,
		OutboxKnownSuccess(now.Add(time.Second)),
	)
	if !errors.Is(err, injected) {
		t.Fatalf("FinalizeOutboxAttempt() error = %v, want injected failure", err)
	}
	delivery, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		fixture.delivery.ID,
		fixture.snapshot.ID,
	)
	if delivery.Status != models.OutboxDeliveryProcessing ||
		delivery.DeliveredAt != nil ||
		snapshot.CredentialShreddedAt != nil {
		t.Fatalf(
			"failed shred partially committed delivery=%+v snapshot=%+v",
			delivery,
			snapshot,
		)
	}
}

func TestWebhookOutboxKnownTimelySuccessUsesActualCompletionAfterDeadline(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(-time.Second))
	_, claim := fixture.claim(t, "timely-success-worker")
	completedAt := fixture.snapshot.CredentialExpiresAt.Add(-time.Millisecond)
	finalizedAt := fixture.snapshot.CredentialExpiresAt.Add(time.Minute)
	fixture.setNow(finalizedAt)

	result, err := fixture.service.FinalizeOutboxAttempt(
		fixture.worker,
		claim,
		OutboxKnownSuccess(completedAt),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != models.OutboxDeliverySucceeded {
		t.Fatalf("finalize status = %s, want succeeded", result.Status)
	}
	delivery, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		fixture.delivery.ID,
		fixture.snapshot.ID,
	)
	if delivery.DeliveredAt == nil ||
		!delivery.DeliveredAt.Equal(completedAt) ||
		!delivery.UpdatedAt.Equal(finalizedAt) {
		t.Fatalf(
			"completion/finalize timestamps drifted: delivered=%v updated=%v",
			delivery.DeliveredAt,
			delivery.UpdatedAt,
		)
	}
	assertSnapshotShredded(
		t,
		snapshot,
		models.WebhookCredentialShredReasonSucceeded,
	)
}

func TestWebhookOutboxLateOrUnknownSuccessExpires(t *testing.T) {
	for _, test := range []struct {
		name        string
		completedAt time.Time
	}{
		{name: "unknown"},
		{
			name: "late",
			completedAt: time.Date(
				2026,
				time.August,
				18,
				8,
				0,
				0,
				0,
				time.UTC,
			),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
			fixture := newWebhookOutboxLifecycleFixture(t, now)
			_, claim := fixture.claim(t, "unsafe-success-worker")
			fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(time.Minute))
			result, err := fixture.service.FinalizeOutboxAttempt(
				fixture.worker,
				claim,
				OutboxKnownSuccess(test.completedAt),
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != models.OutboxDeliveryExpired {
				t.Fatalf("finalize status = %s, want expired", result.Status)
			}
			delivery, snapshot := loadWebhookLifecycleRows(
				t,
				fixture,
				fixture.delivery.ID,
				fixture.snapshot.ID,
			)
			if delivery.Status != models.OutboxDeliveryExpired ||
				delivery.ExpiredAt == nil ||
				delivery.DeliveredAt != nil {
				t.Fatalf("unsafe success was not expired: %+v", delivery)
			}
			assertSnapshotShredded(
				t,
				snapshot,
				models.WebhookCredentialShredReasonExpired,
			)
		})
	}
}

func TestWebhookOutboxCleanupExpiresDueStatesAtomically(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	type pair struct {
		delivery models.OutboxDelivery
		snapshot models.WebhookDeliverySnapshot
		event    models.DomainEvent
	}
	pairs := []pair{{
		delivery: fixture.delivery,
		snapshot: fixture.snapshot,
		event:    fixture.event,
	}}
	for _, suffix := range []string{"failed", "dead"} {
		delivery, snapshot, event := fixture.createIntent(t, suffix)
		pairs = append(pairs, pair{
			delivery: delivery,
			snapshot: snapshot,
			event:    event,
		})
	}
	statuses := []models.OutboxDeliveryStatus{
		models.OutboxDeliveryPending,
		models.OutboxDeliveryFailed,
		models.OutboxDeliveryDead,
	}
	for index := range pairs {
		if err := fixture.db.Model(&models.OutboxDelivery{}).
			Where("id = ?", pairs[index].delivery.ID).
			Updates(map[string]any{
				"status":     statuses[index],
				"last_error": "safe fixture failure",
			}).Error; err != nil {
			t.Fatal(err)
		}
	}
	fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(time.Second))
	result, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Expired != 3 || result.Attempted != 3 {
		t.Fatalf("cleanup result = %+v, want three expirations", result)
	}
	for _, current := range pairs {
		delivery, snapshot := loadWebhookLifecycleRows(
			t,
			fixture,
			current.delivery.ID,
			current.snapshot.ID,
		)
		if delivery.Status != models.OutboxDeliveryExpired ||
			delivery.ExpiredAt == nil {
			t.Fatalf("due delivery was not expired: %+v", delivery)
		}
		assertSnapshotShredded(
			t,
			snapshot,
			models.WebhookCredentialShredReasonExpired,
		)
		var event models.DomainEvent
		if err := fixture.db.First(
			&event,
			"id = ?",
			current.event.ID,
		).Error; err != nil {
			t.Fatal(err)
		}
		if event.PublishedAt != nil {
			t.Fatalf("expired event published_at = %v, want nil", event.PublishedAt)
		}
	}
}

func TestWebhookOutboxCleanupPreservesValidProcessingLock(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(-30 * time.Second))
	claimed, _ := fixture.claim(t, "valid-lock-worker")
	fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(time.Second))

	result, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Expired != 0 {
		t.Fatalf("cleanup stole a valid processing lock: %+v", result)
	}
	var current models.OutboxDelivery
	if err := fixture.db.First(&current, "id = ?", claimed.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != models.OutboxDeliveryProcessing ||
		current.LockedBy != "valid-lock-worker" ||
		current.LockedAt == nil ||
		!current.LockedAt.Equal(*claimed.LockedAt) {
		t.Fatalf("valid processing claim changed: %+v", current)
	}
}

func TestWebhookOutboxCleanupExpiresStaleProcessingLock(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	claimed, _ := fixture.claim(t, "stale-lock-worker")
	fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(time.Second))

	result, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Expired != 1 {
		t.Fatalf("stale processing cleanup result = %+v, want one", result)
	}
	delivery, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		claimed.ID,
		fixture.snapshot.ID,
	)
	if delivery.Status != models.OutboxDeliveryExpired ||
		delivery.LockedAt != nil ||
		delivery.LockedBy != "" {
		t.Fatalf("stale processing delivery was not expired: %+v", delivery)
	}
	assertSnapshotShredded(
		t,
		snapshot,
		models.WebhookCredentialShredReasonExpired,
	)
}

func TestWebhookOutboxFailureNeverSchedulesAtOrAfterDeadline(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(-time.Second))
	_, claim := fixture.claim(t, "deadline-failure-worker")

	result, err := fixture.service.FinalizeOutboxAttempt(
		fixture.worker,
		claim,
		OutboxKnownFailure(errors.New("safe destination rejection")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != models.OutboxDeliveryExpired {
		t.Fatalf("failure finalize status = %s, want expired", result.Status)
	}
	delivery, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		fixture.delivery.ID,
		fixture.snapshot.ID,
	)
	if delivery.Status != models.OutboxDeliveryExpired ||
		!delivery.NextAttemptAt.Before(fixture.snapshot.CredentialExpiresAt) {
		t.Fatalf("failure scheduled at/after deadline: %+v", delivery)
	}
	assertSnapshotShredded(
		t,
		snapshot,
		models.WebhookCredentialShredReasonExpired,
	)
}

func TestWebhookOutboxExhaustedAttemptBecomesDeadBeforeDeadline(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", fixture.delivery.ID).
		Update("max_attempts", 1).Error; err != nil {
		t.Fatal(err)
	}
	fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(-time.Second))
	_, claim := fixture.claim(t, "final-attempt-worker")

	result, err := fixture.service.FinalizeOutboxAttempt(
		fixture.worker,
		claim,
		OutboxKnownFailure(errors.New("safe final rejection")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != models.OutboxDeliveryDead {
		t.Fatalf("exhausted attempt status = %s, want dead", result.Status)
	}
	delivery, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		fixture.delivery.ID,
		fixture.snapshot.ID,
	)
	if delivery.Status != models.OutboxDeliveryDead ||
		delivery.LockedAt != nil ||
		delivery.LockedBy != "" ||
		delivery.LockToken != nil ||
		snapshot.CredentialShreddedAt != nil {
		t.Fatalf(
			"exhausted attempt did not preserve pre-deadline dead state: delivery=%+v snapshot=%+v",
			delivery,
			snapshot,
		)
	}
}

func TestWebhookOutboxCleanupClearsExpiredOverlapOnly(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	fixture.setNow(now.Add(2 * time.Hour))

	result, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.OverlapCleared != 1 {
		t.Fatalf("overlap cleanup result = %+v, want one", result)
	}
	_, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		fixture.delivery.ID,
		fixture.snapshot.ID,
	)
	if snapshot.PreviousSecret != "" ||
		snapshot.PreviousSecretExpiresAt != nil ||
		snapshot.Secret == "" ||
		snapshot.AccessToken == "" ||
		snapshot.CredentialShreddedAt != nil {
		t.Fatalf("overlap cleanup changed the live snapshot incorrectly: %+v", snapshot)
	}
}

func TestWebhookOutboxMalformedPairNeverReachesAdapterOrFinalizer(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *webhookOutboxLifecycleFixture)
	}{
		{
			name: "cross_project",
			mutate: func(t *testing.T, fixture *webhookOutboxLifecycleFixture) {
				if err := fixture.db.Exec(
					"UPDATE webhook_delivery_snapshots SET project_id = project_id + 1000 WHERE id = ?",
					fixture.snapshot.ID,
				).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "event_mismatch",
			mutate: func(t *testing.T, fixture *webhookOutboxLifecycleFixture) {
				if err := fixture.db.Exec(
					"UPDATE webhook_delivery_snapshots SET event_id = ? WHERE id = ?",
					"00000000-0000-7000-8000-000000000099",
					fixture.snapshot.ID,
				).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "deadline_mismatch",
			mutate: func(t *testing.T, fixture *webhookOutboxLifecycleFixture) {
				if err := fixture.db.Exec(
					"UPDATE webhook_delivery_snapshots SET credential_expires_at = ? WHERE id = ?",
					fixture.snapshot.CredentialExpiresAt.Add(time.Hour),
					fixture.snapshot.ID,
				).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
			fixture := newWebhookOutboxLifecycleFixture(t, now)
			if err := fixture.db.Exec(
				"PRAGMA foreign_keys = OFF",
			).Error; err != nil {
				t.Fatal(err)
			}
			if err := fixture.db.Exec(
				"PRAGMA ignore_check_constraints = ON",
			).Error; err != nil {
				t.Fatal(err)
			}
			test.mutate(t, fixture)
			if err := fixture.db.Exec(
				"PRAGMA ignore_check_constraints = OFF",
			).Error; err != nil {
				t.Fatal(err)
			}
			var attempts atomic.Int32
			result, err := fixture.service.ProcessOutboxBatch(
				context.Background(),
				"malformed-pair-worker",
				10,
				OutboxDeliverFunc(func(
					context.Context,
					*models.OutboxDelivery,
					CloudEventEnvelope,
				) error {
					attempts.Add(1)
					return nil
				}),
			)
			if err != nil &&
				!errors.Is(err, ErrWebhookOutboxLifecycleInvariant) {
				t.Fatal(err)
			}
			if attempts.Load() != 0 || result.Delivered != 0 {
				t.Fatalf(
					"malformed pair reached adapter: attempts=%d result=%+v",
					attempts.Load(),
					result,
				)
			}
		})
	}
}

func TestWebhookOutboxFinalizerRejectsPairMutationAfterValidClaim(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *webhookOutboxLifecycleFixture)
	}{
		{
			name: "destination_snapshot",
			mutate: func(t *testing.T, fixture *webhookOutboxLifecycleFixture) {
				if err := fixture.db.Exec(
					"UPDATE outbox_deliveries SET destination_id = ? WHERE id = ?",
					models.WebhookDeliverySnapshotDestinationPrefix+
						"00000000-0000-7000-8000-000000000099",
					fixture.delivery.ID,
				).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "event",
			mutate: func(t *testing.T, fixture *webhookOutboxLifecycleFixture) {
				if err := fixture.db.Exec(
					"UPDATE webhook_delivery_snapshots SET event_id = ? WHERE id = ?",
					"00000000-0000-7000-8000-000000000099",
					fixture.snapshot.ID,
				).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "deadline",
			mutate: func(t *testing.T, fixture *webhookOutboxLifecycleFixture) {
				if err := fixture.db.Exec(
					"UPDATE webhook_delivery_snapshots SET credential_expires_at = ? WHERE id = ?",
					fixture.snapshot.CredentialExpiresAt.Add(time.Hour),
					fixture.snapshot.ID,
				).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "scope",
			mutate: func(t *testing.T, fixture *webhookOutboxLifecycleFixture) {
				if err := fixture.db.Exec(
					"UPDATE webhook_delivery_snapshots SET project_id = project_id + 1000 WHERE id = ?",
					fixture.snapshot.ID,
				).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
			fixture := newWebhookOutboxLifecycleFixture(t, now)
			_, claim := fixture.claim(t, "post-claim-mismatch-"+test.name)
			if err := fixture.db.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
				t.Fatal(err)
			}
			if err := fixture.db.Exec(
				"PRAGMA ignore_check_constraints = ON",
			).Error; err != nil {
				t.Fatal(err)
			}
			test.mutate(t, fixture)
			if err := fixture.db.Exec(
				"PRAGMA ignore_check_constraints = OFF",
			).Error; err != nil {
				t.Fatal(err)
			}
			_, err := fixture.service.FinalizeOutboxAttempt(
				fixture.worker,
				claim,
				OutboxKnownSuccess(now.Add(time.Second)),
			)
			if !errors.Is(err, ErrWebhookOutboxLifecycleInvariant) {
				t.Fatalf("post-claim mismatch error = %v", err)
			}
			var delivery models.OutboxDelivery
			if err := fixture.db.First(
				&delivery,
				"id = ?",
				fixture.delivery.ID,
			).Error; err != nil {
				t.Fatal(err)
			}
			var snapshot models.WebhookDeliverySnapshot
			if err := fixture.db.First(
				&snapshot,
				"id = ?",
				fixture.snapshot.ID,
			).Error; err != nil {
				t.Fatal(err)
			}
			var event models.DomainEvent
			if err := fixture.db.First(
				&event,
				"id = ?",
				fixture.event.ID,
			).Error; err != nil {
				t.Fatal(err)
			}
			if delivery.Status != models.OutboxDeliveryProcessing ||
				delivery.DeliveredAt != nil ||
				delivery.ExpiredAt != nil ||
				delivery.LockToken == nil ||
				*delivery.LockToken != claim.LockToken ||
				snapshot.CredentialShreddedAt != nil ||
				snapshot.Secret == "" ||
				event.PublishedAt != nil {
				t.Fatalf(
					"post-claim mismatch partially finalized delivery=%+v snapshot=%+v event=%+v",
					delivery,
					snapshot,
					event,
				)
			}
		})
	}
}

func TestWebhookOutboxCleanupHonorsRequestedAndHardBounds(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	for index := 1; index < 205; index++ {
		fixture.createIntent(t, fmt.Sprintf("bound-%03d", index))
	}
	fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(time.Second))

	first, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Attempted != 2 {
		t.Fatalf("requested cleanup bound result = %+v, want attempted=2", first)
	}
	second, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		1000,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Attempted > 200 {
		t.Fatalf("hard cleanup bound exceeded: %+v", second)
	}
}

func TestWebhookOutboxClaimRefRejectsSameWorkerABA(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	_, staleClaim := fixture.claim(t, "same-worker")
	newLock := now.Add(time.Second)
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", fixture.delivery.ID).
		Updates(map[string]any{
			"locked_at": newLock,
			"attempts":  gorm.Expr("attempts + 1"),
		}).Error; err != nil {
		t.Fatal(err)
	}

	_, err := fixture.service.FinalizeOutboxAttempt(
		fixture.worker,
		staleClaim,
		OutboxKnownSuccess(now.Add(2*time.Second)),
	)
	if !errors.Is(err, ErrOutboxLockLost) {
		t.Fatalf("same-worker ABA finalize error = %v, want lock lost", err)
	}
	var current models.OutboxDelivery
	if err := fixture.db.First(&current, "id = ?", fixture.delivery.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != models.OutboxDeliveryProcessing ||
		current.Attempts != staleClaim.Attempts+1 ||
		current.LockedAt == nil ||
		!current.LockedAt.Equal(newLock) {
		t.Fatalf("stale claim changed the winning ABA claim: %+v", current)
	}
}

func TestWebhookOutboxSuccessCompletionMustBelongToClaimWindow(t *testing.T) {
	for _, test := range []struct {
		name      string
		completed func(OutboxClaimRef) time.Time
	}{
		{
			name: "before_claim",
			completed: func(claim OutboxClaimRef) time.Time {
				return claim.LockedAt.Add(-time.Microsecond)
			},
		},
		{
			name: "after_effective_request_deadline",
			completed: func(claim OutboxClaimRef) time.Time {
				return claim.EffectiveDeadline.Add(time.Microsecond)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(
				2026,
				time.August,
				10,
				8,
				0,
				0,
				0,
				time.UTC,
			)
			fixture := newWebhookOutboxLifecycleFixture(t, now)
			_, claim := fixture.claim(t, "claim-window-worker")
			claim.EffectiveDeadline = claim.LockedAt.Add(time.Second)
			result, err := fixture.service.FinalizeOutboxAttempt(
				fixture.worker,
				claim,
				OutboxKnownSuccess(test.completed(claim)),
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != models.OutboxDeliveryExpired {
				t.Fatalf(
					"out-of-window success status = %s, want expired",
					result.Status,
				)
			}
		})
	}
}

func TestWebhookOutboxLegacySucceededSnapshotRepairsImmediately(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	deliveredAt := now.Add(time.Second)
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", fixture.delivery.ID).
		Updates(map[string]any{
			"status":       models.OutboxDeliverySucceeded,
			"delivered_at": deliveredAt,
			"updated_at":   deliveredAt,
		}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.ExpireWebhookDeliveriesBatch(
		context.Background(),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.LegacySucceededShredded != 1 || result.Attempted != 1 {
		t.Fatalf("legacy success repair result = %+v, want one", result)
	}
	_, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		fixture.delivery.ID,
		fixture.snapshot.ID,
	)
	assertSnapshotShredded(
		t,
		snapshot,
		models.WebhookCredentialShredReasonSucceeded,
	)
}
