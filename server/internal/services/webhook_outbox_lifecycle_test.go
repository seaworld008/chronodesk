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

func (fixture *webhookOutboxLifecycleFixture) startDispatch(
	t *testing.T,
	deliveryID string,
) time.Time {
	t.Helper()
	var delivery models.OutboxDelivery
	if err := fixture.db.Where(
		"id = ?",
		deliveryID,
	).Take(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.LockedAt == nil {
		t.Fatalf("dispatch start delivery has no claim lock: %+v", delivery)
	}
	startedAt := webhookDispatchStartedAt(
		fixture.service.now().UTC().Add(time.Millisecond),
		delivery.LockedAt.UTC(),
	)
	result := fixture.db.Model(&models.OutboxDelivery{}).
		Where(
			"id = ? AND dispatch_started_at = locked_at",
			deliveryID,
		).
		Update("dispatch_started_at", startedAt)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if result.RowsAffected != 1 {
		t.Fatalf(
			"dispatch start rows = %d for %s",
			result.RowsAffected,
			deliveryID,
		)
	}
	return startedAt
}

func TestWebhookFinalizeNotStartedRequiresPreparedDispatch(
	t *testing.T,
) {
	for _, test := range []struct {
		name    string
		marker  func(*time.Time) *time.Time
		wantErr bool
	}{
		{
			name: "prepared",
			marker: func(lockedAt *time.Time) *time.Time {
				value := lockedAt.UTC()
				return &value
			},
		},
		{
			name: "legacy_unknown",
			marker: func(*time.Time) *time.Time {
				return nil
			},
			wantErr: true,
		},
		{
			name: "started",
			marker: func(lockedAt *time.Time) *time.Time {
				value := lockedAt.UTC().Add(time.Millisecond)
				return &value
			},
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Millisecond)
			fixture := newWebhookOutboxLifecycleFixture(t, now)
			claimed, claim := fixture.claim(t, "not-started-"+test.name)
			marker := test.marker(claimed.LockedAt)
			if err := fixture.db.Model(&models.OutboxDelivery{}).
				Where("id = ?", claimed.ID).
				Update("dispatch_started_at", marker).Error; err != nil {
				t.Fatal(err)
			}
			result, err := fixture.service.FinalizeOutboxAttempt(
				fixture.worker,
				claim,
				outboxAttemptNotStarted(
					errors.New("transport was not entered"),
				),
			)
			if test.wantErr {
				if !errors.Is(
					err,
					ErrWebhookOutboxLifecycleInvariant,
				) {
					t.Fatalf("not-started error = %v", err)
				}
			} else if err != nil ||
				result.Status != models.OutboxDeliveryFailed {
				t.Fatalf(
					"prepared not-started result=%+v err=%v",
					result,
					err,
				)
			}
			var current models.OutboxDelivery
			if err := fixture.db.First(
				&current,
				"id = ?",
				claimed.ID,
			).Error; err != nil {
				t.Fatal(err)
			}
			if test.wantErr {
				if current.Status != models.OutboxDeliveryProcessing ||
					current.Attempts != 1 {
					t.Fatalf(
						"rejected not-started mutated delivery: %+v",
						current,
					)
				}
				if marker == nil {
					if current.DispatchStartedAt != nil {
						t.Fatalf(
							"legacy marker changed: %+v",
							current,
						)
					}
				} else if current.DispatchStartedAt == nil ||
					!current.DispatchStartedAt.Equal(
						marker.UTC(),
					) {
					t.Fatalf(
						"dispatch marker changed: %+v",
						current,
					)
				}
				return
			}
			if current.Status != models.OutboxDeliveryFailed ||
				current.Attempts != 0 ||
				current.DispatchStartedAt != nil ||
				current.LockedAt != nil ||
				current.LockToken != nil {
				t.Fatalf(
					"prepared not-started was not safely released: %+v",
					current,
				)
			}
		})
	}
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

func setWebhookLifecyclePublicationHistory(
	t *testing.T,
	fixture *webhookOutboxLifecycleFixture,
	publishedAt *time.Time,
) {
	t.Helper()
	if publishedAt != nil {
		deliveredAt := publishedAt.UTC()
		mixed := models.OutboxDelivery{
			ID: "00000000-0000-7000-8001-" +
				fixture.event.ID[len(fixture.event.ID)-12:],
			OrganizationID:  fixture.scope.OrganizationID,
			ProjectID:       fixture.scope.ProjectID,
			EventID:         fixture.event.ID,
			DestinationType: "event_stream",
			DestinationID:   "immutable-publication-history",
			Status:          models.OutboxDeliverySucceeded,
			MaxAttempts:     1,
			NextAttemptAt:   deliveredAt,
			DeliveredAt:     &deliveredAt,
		}
		if err := fixture.db.Create(&mixed).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.db.Model(&models.DomainEvent{}).
		Where("id = ?", fixture.event.ID).
		Update("published_at", publishedAt).Error; err != nil {
		t.Fatal(err)
	}
}

func assertWebhookLifecyclePublicationHistory(
	t *testing.T,
	fixture *webhookOutboxLifecycleFixture,
	want *time.Time,
) {
	t.Helper()
	var event models.DomainEvent
	if err := fixture.db.First(
		&event,
		"id = ?",
		fixture.event.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if want == nil {
		if event.PublishedAt != nil {
			t.Fatalf("unpublished event gained published_at %v", event.PublishedAt)
		}
		return
	}
	if event.PublishedAt == nil || !event.PublishedAt.Equal(want.UTC()) {
		t.Fatalf(
			"published event history = %v, want %v",
			event.PublishedAt,
			want.UTC(),
		)
	}
}

func TestPostRevokeProcessingFinalizePreservesDomainEventPublicationHistory(
	t *testing.T,
) {
	for _, published := range []bool{false, true} {
		t.Run(fmt.Sprintf("published_%t", published), func(t *testing.T) {
			now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
			fixture := newWebhookOutboxLifecycleFixture(t, now)
			claimed, claim := fixture.claim(t, "post-revoke-finalize")
			fixture.startDispatch(t, claimed.ID)
			var publishedAt *time.Time
			if published {
				value := now.Add(-time.Hour)
				publishedAt = &value
			}
			setWebhookLifecyclePublicationHistory(t, fixture, publishedAt)
			if _, err := fixture.service.EmergencyRevokeWebhook(
				webhookEmergencyAdminContext(
					t,
					fixture,
					models.ProjectRoleAdmin,
				),
				fixture.config.ID,
			); err != nil {
				t.Fatal(err)
			}
			result, err := fixture.service.FinalizeOutboxAttempt(
				fixture.worker,
				claim,
				OutboxKnownFailure(errors.New("safe post-revoke failure")),
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != models.OutboxDeliveryExpired {
				t.Fatalf("post-revoke finalize status = %s", result.Status)
			}
			assertWebhookLifecyclePublicationHistory(
				t,
				fixture,
				publishedAt,
			)
		})
	}
}

func TestPostRevokeProcessingCleanupPreservesDomainEventPublicationHistory(
	t *testing.T,
) {
	for _, published := range []bool{false, true} {
		t.Run(fmt.Sprintf("published_%t", published), func(t *testing.T) {
			now := time.Date(2026, time.August, 11, 13, 0, 0, 0, time.UTC)
			fixture := newWebhookOutboxLifecycleFixture(t, now)
			claimed, _ := fixture.claim(t, "post-revoke-cleanup")
			fixture.startDispatch(t, claimed.ID)
			var publishedAt *time.Time
			if published {
				value := now.Add(-time.Hour)
				publishedAt = &value
			}
			setWebhookLifecyclePublicationHistory(t, fixture, publishedAt)
			if _, err := fixture.service.EmergencyRevokeWebhook(
				webhookEmergencyAdminContext(
					t,
					fixture,
					models.ProjectRoleAdmin,
				),
				fixture.config.ID,
			); err != nil {
				t.Fatal(err)
			}
			fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(time.Second))
			result, err := fixture.service.ExpireWebhookDeliveriesBatch(
				context.Background(),
				10,
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Expired != 1 {
				t.Fatalf("post-revoke cleanup = %+v, want one expiry", result)
			}
			assertWebhookLifecyclePublicationHistory(
				t,
				fixture,
				publishedAt,
			)
		})
	}
}

func TestWebhookReplayExpirationPreservesDomainEventPublicationHistory(
	t *testing.T,
) {
	for _, published := range []bool{false, true} {
		t.Run(fmt.Sprintf("published_%t", published), func(t *testing.T) {
			now := time.Date(2026, time.August, 11, 14, 0, 0, 0, time.UTC)
			fixture := newWebhookOutboxLifecycleFixture(t, now)
			if err := fixture.db.Model(&models.OutboxDelivery{}).
				Where("id = ?", fixture.delivery.ID).
				Update("status", models.OutboxDeliveryFailed).Error; err != nil {
				t.Fatal(err)
			}
			var publishedAt *time.Time
			if published {
				value := now.Add(-time.Hour)
				publishedAt = &value
			}
			setWebhookLifecyclePublicationHistory(t, fixture, publishedAt)
			fixture.setNow(fixture.snapshot.CredentialExpiresAt.Add(time.Second))
			result, err := fixture.service.ReplayOutboxCommand(
				webhookEmergencyAdminContext(
					t,
					fixture,
					models.ProjectRoleAdmin,
				),
				fixture.delivery.ID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Disposition != OutboxReplayExpired ||
				!result.Materialized {
				t.Fatalf("expired replay result = %+v", result)
			}
			assertWebhookLifecyclePublicationHistory(
				t,
				fixture,
				publishedAt,
			)
		})
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

func TestWebhookStaleClaimReclaimsOnlyPreparedDispatch(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	prepared := fixture.delivery
	legacy, _, _ := fixture.createIntent(t, "stale-legacy-unknown")
	started, _, _ := fixture.createIntent(t, "stale-started")
	lockedAt := now.Add(-2 * time.Minute)
	startedAt := now.Add(-90 * time.Second)
	for index, row := range []struct {
		delivery models.OutboxDelivery
		marker   *time.Time
	}{
		{
			delivery: prepared,
			marker: func() *time.Time {
				value := lockedAt
				return &value
			}(),
		},
		{delivery: legacy},
		{delivery: started, marker: &startedAt},
	} {
		token := fmt.Sprintf(
			"00000000-0000-7000-8000-%012d",
			index+1,
		)
		if err := fixture.db.Model(&models.OutboxDelivery{}).
			Where("id = ?", row.delivery.ID).
			Updates(map[string]any{
				"status":              models.OutboxDeliveryProcessing,
				"attempts":            1,
				"locked_at":           lockedAt,
				"locked_by":           "stale-worker",
				"lock_token":          token,
				"dispatch_started_at": row.marker,
			}).Error; err != nil {
			t.Fatal(err)
		}
	}
	fixture.setNow(now.Add(10 * time.Minute))
	claimed, err := fixture.service.ClaimPendingOutbox(
		fixture.worker,
		"replacement-worker",
		10,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 ||
		claimed[0].ID != prepared.ID ||
		claimed[0].Attempts != 2 ||
		!isWebhookDispatchPrepared(
			claimed[0].DispatchStartedAt,
			claimed[0].LockedAt,
		) {
		t.Fatalf("stale dispatch claims = %+v", claimed)
	}
	for _, protected := range []struct {
		delivery models.OutboxDelivery
		marker   *time.Time
	}{
		{delivery: legacy},
		{delivery: started, marker: &startedAt},
	} {
		var current models.OutboxDelivery
		if err := fixture.db.First(
			&current,
			"id = ?",
			protected.delivery.ID,
		).Error; err != nil {
			t.Fatal(err)
		}
		if current.Status != models.OutboxDeliveryProcessing ||
			current.Attempts != 1 ||
			current.LockedBy != "stale-worker" {
			t.Fatalf(
				"unknown/started dispatch was reclaimed: %+v",
				current,
			)
		}
		if protected.marker == nil {
			if current.DispatchStartedAt != nil {
				t.Fatalf("legacy marker changed: %+v", current)
			}
		} else if !isWebhookDispatchStarted(
			current.DispatchStartedAt,
			current.LockedAt,
		) {
			t.Fatalf("started marker changed: %+v", current)
		}
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
	claimed, claim := fixture.claim(t, "atomic-success-worker")
	fixture.startDispatch(t, claimed.ID)
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
		!isWebhookDispatchStarted(
			delivery.DispatchStartedAt,
			delivery.LockedAt,
		) ||
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
	claimed, claim := fixture.claim(t, "timely-success-worker")
	fixture.startDispatch(t, claimed.ID)
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
		!isWebhookDispatchStarted(
			delivery.DispatchStartedAt,
			delivery.LockedAt,
		) ||
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
			claimed, claim := fixture.claim(t, "unsafe-success-worker")
			fixture.startDispatch(t, claimed.ID)
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
				delivery.DeliveredAt != nil ||
				!isWebhookDispatchStarted(
					delivery.DispatchStartedAt,
					delivery.LockedAt,
				) {
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

func TestWebhookOutboxKnownFailureClearsPreparedGenerationForRetry(
	t *testing.T,
) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	claimed, claim := fixture.claim(t, "known-failure-worker")
	result, err := fixture.service.FinalizeOutboxAttempt(
		fixture.worker,
		claim,
		OutboxKnownFailure(errors.New("destination rejected safely")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != models.OutboxDeliveryFailed {
		t.Fatalf("known failure result = %+v", result)
	}
	current, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		claimed.ID,
		fixture.snapshot.ID,
	)
	if current.DispatchStartedAt != nil ||
		current.Status != models.OutboxDeliveryFailed ||
		current.LockedAt != nil ||
		current.LockToken != nil ||
		snapshot.CredentialShreddedAt != nil {
		t.Fatalf(
			"known failure did not release retry generation: delivery=%+v snapshot=%+v",
			current,
			snapshot,
		)
	}
}

func TestWebhookPreparedDispatchRejectsSuccessAndUncertainFinalize(
	t *testing.T,
) {
	for _, test := range []struct {
		name    string
		attempt func(time.Time) OutboxAttemptResult
	}{
		{
			name: "success",
			attempt: func(now time.Time) OutboxAttemptResult {
				return OutboxKnownSuccess(now)
			},
		},
		{
			name: "uncertain",
			attempt: func(time.Time) OutboxAttemptResult {
				return OutboxUncertain(context.DeadlineExceeded)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Millisecond)
			fixture := newWebhookOutboxLifecycleFixture(t, now)
			claimed, claim := fixture.claim(
				t,
				"prepared-finalize-"+test.name,
			)
			_, err := fixture.service.FinalizeOutboxAttempt(
				fixture.worker,
				claim,
				test.attempt(now.Add(time.Millisecond)),
			)
			if !errors.Is(err, ErrWebhookOutboxLifecycleInvariant) {
				t.Fatalf("prepared %s finalize error = %v", test.name, err)
			}
			var current models.OutboxDelivery
			if err := fixture.db.First(
				&current,
				"id = ?",
				claimed.ID,
			).Error; err != nil {
				t.Fatal(err)
			}
			if current.Status != models.OutboxDeliveryProcessing ||
				!isWebhookDispatchPrepared(
					current.DispatchStartedAt,
					current.LockedAt,
				) {
				t.Fatalf(
					"rejected prepared finalize mutated delivery: %+v",
					current,
				)
			}
		})
	}
}

func TestWebhookOutboxUncertainPreservesStartedDispatch(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	claimed, claim := fixture.claim(t, "uncertain-worker")
	startedAt := fixture.startDispatch(t, claimed.ID)
	result, err := fixture.service.FinalizeOutboxAttempt(
		fixture.worker,
		claim,
		OutboxUncertain(context.DeadlineExceeded),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != models.OutboxDeliveryExpired {
		t.Fatalf("uncertain result = %+v", result)
	}
	current, snapshot := loadWebhookLifecycleRows(
		t,
		fixture,
		claimed.ID,
		fixture.snapshot.ID,
	)
	if current.Status != models.OutboxDeliveryExpired ||
		current.DispatchStartedAt == nil ||
		!current.DispatchStartedAt.Equal(startedAt) {
		t.Fatalf("uncertain dispatch history changed: %+v", current)
	}
	assertSnapshotShredded(
		t,
		snapshot,
		models.WebhookCredentialShredReasonExpired,
	)
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
	claimed, claim := fixture.claim(t, "deadline-failure-worker")
	fixture.startDispatch(t, claimed.ID)

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
	claimed, claim := fixture.claim(t, "final-attempt-worker")
	fixture.startDispatch(t, claimed.ID)

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
			claimed, claim := fixture.claim(
				t,
				"post-claim-mismatch-"+test.name,
			)
			fixture.startDispatch(t, claimed.ID)
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
			"locked_at":           newLock,
			"lock_token":          "019fee69-720c-7023-ae63-fcaf437562ab",
			"dispatch_started_at": newLock,
			"attempts":            gorm.Expr("attempts + 1"),
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
			claimed, claim := fixture.claim(t, "claim-window-worker")
			fixture.startDispatch(t, claimed.ID)
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
