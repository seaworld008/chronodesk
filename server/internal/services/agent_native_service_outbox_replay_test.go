package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

func TestReplayOutboxAllowsOnlyFailedOrDeadDeliveries(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      models.OutboxDeliveryStatus
		wantAllowed bool
	}{
		{
			name:        "failed",
			status:      models.OutboxDeliveryFailed,
			wantAllowed: true,
		},
		{
			name:        "dead",
			status:      models.OutboxDeliveryDead,
			wantAllowed: true,
		},
		{
			name:   "pending",
			status: models.OutboxDeliveryPending,
		},
		{
			name:   "processing",
			status: models.OutboxDeliveryProcessing,
		},
		{
			name:   "succeeded",
			status: models.OutboxDeliverySucceeded,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, service, ctx, delivery, event :=
				newOutboxReplayTestFixture(t, test.status)

			err := service.ReplayOutbox(ctx, delivery.ID)
			if test.wantAllowed {
				if err != nil {
					t.Fatalf("ReplayOutbox() error = %v", err)
				}
				assertOutboxReplayReset(t, db, delivery, event)
				return
			}
			if !errors.Is(err, ErrOutboxReplayConflict) {
				t.Fatalf(
					"ReplayOutbox() error = %v, want %v",
					err,
					ErrOutboxReplayConflict,
				)
			}
			assertOutboxReplayRejectedUnchanged(
				t,
				db,
				delivery,
				event,
			)
		})
	}
}

func TestReplayOutboxKeepsNotFoundContract(t *testing.T) {
	db := openAgentNativeTestDB(t)
	ctx := testProjectOperationContext(
		t,
		db,
		models.SystemActor("outbox-replay-test"),
	)
	service := NewAgentNativeService(db)

	err := service.ReplayOutbox(
		ctx,
		"00000000-0000-7000-8000-000000000099",
	)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf(
			"ReplayOutbox() error = %v, want %v",
			err,
			gorm.ErrRecordNotFound,
		)
	}
}

func TestReplayWebhookOutboxRequiresLiveMatchingFiniteSnapshot(t *testing.T) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)

	t.Run("eligible replay preserves absolute deadline", func(t *testing.T) {
		fixture := newWebhookOutboxLifecycleFixture(t, now)
		setWebhookReplayCandidate(t, fixture, models.OutboxDeliveryFailed)
		before := fixture.delivery.ExpiresAt.UTC()

		if err := fixture.service.ReplayOutbox(
			contextWithProjectScope(
				t,
				fixture.scope,
				models.HumanActor(1),
			),
			fixture.delivery.ID,
		); err != nil {
			t.Fatalf("ReplayOutbox() error = %v", err)
		}

		delivery, snapshot := loadWebhookLifecycleRows(
			t,
			fixture,
			fixture.delivery.ID,
			fixture.snapshot.ID,
		)
		if delivery.Status != models.OutboxDeliveryPending ||
			delivery.ExpiresAt == nil ||
			!delivery.ExpiresAt.UTC().Equal(before) {
			t.Fatalf(
				"replay changed finite delivery generation: %+v",
				delivery,
			)
		}
		if snapshot.CredentialShreddedAt != nil ||
			!snapshot.CredentialExpiresAt.UTC().Equal(before) {
			t.Fatalf(
				"replay changed finite snapshot generation: %+v",
				snapshot,
			)
		}
	})

	t.Run("due replay commits expiry before stable error", func(t *testing.T) {
		fixture := newWebhookOutboxLifecycleFixture(t, now)
		setWebhookReplayCandidate(t, fixture, models.OutboxDeliveryDead)
		absoluteDeadline := fixture.snapshot.CredentialExpiresAt.UTC()
		fixture.setNow(fixture.snapshot.CredentialExpiresAt)

		err := fixture.service.ReplayOutbox(
			contextWithProjectScope(
				t,
				fixture.scope,
				models.HumanActor(1),
			),
			fixture.delivery.ID,
		)
		if !errors.Is(err, ErrOutboxReplayExpired) {
			t.Fatalf(
				"ReplayOutbox() error = %v, want %v",
				err,
				ErrOutboxReplayExpired,
			)
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
			delivery.ExpiresAt == nil ||
			!delivery.ExpiresAt.UTC().Equal(absoluteDeadline) ||
			!snapshot.CredentialExpiresAt.UTC().Equal(
				absoluteDeadline,
			) {
			t.Fatalf(
				"due replay did not commit terminal delivery: %+v",
				delivery,
			)
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
			fixture.event.ID,
		).Error; err != nil {
			t.Fatal(err)
		}
		if event.PublishedAt != nil {
			t.Fatalf(
				"expired replay published domain event at %v",
				event.PublishedAt,
			)
		}
	})

	t.Run("already expired uses stable error", func(t *testing.T) {
		fixture := newWebhookOutboxLifecycleFixture(t, now)
		setWebhookReplayCandidate(t, fixture, models.OutboxDeliveryFailed)
		expiredAt := now.Add(-time.Minute)
		if err := fixture.db.Model(&models.OutboxDelivery{}).
			Where("id = ?", fixture.delivery.ID).
			Updates(map[string]any{
				"status":     models.OutboxDeliveryExpired,
				"expired_at": expiredAt,
			}).Error; err != nil {
			t.Fatal(err)
		}
		if err := shredWebhookSnapshot(
			fixture.db,
			&fixture.snapshot,
			models.WebhookCredentialShredReasonExpired,
			expiredAt,
		); err != nil {
			t.Fatal(err)
		}

		err := fixture.service.ReplayOutbox(
			contextWithProjectScope(
				t,
				fixture.scope,
				models.HumanActor(1),
			),
			fixture.delivery.ID,
		)
		if !errors.Is(err, ErrOutboxReplayExpired) {
			t.Fatalf(
				"ReplayOutbox() error = %v, want %v",
				err,
				ErrOutboxReplayExpired,
			)
		}
	})

	t.Run("shredded failed candidate uses stable error", func(t *testing.T) {
		fixture := newWebhookOutboxLifecycleFixture(t, now)
		setWebhookReplayCandidate(t, fixture, models.OutboxDeliveryFailed)
		if err := shredWebhookSnapshot(
			fixture.db,
			&fixture.snapshot,
			models.WebhookCredentialShredReasonRevoked,
			now,
		); err != nil {
			t.Fatal(err)
		}

		err := fixture.service.ReplayOutbox(
			contextWithProjectScope(
				t,
				fixture.scope,
				models.HumanActor(1),
			),
			fixture.delivery.ID,
		)
		if !errors.Is(err, ErrOutboxReplayExpired) {
			t.Fatalf(
				"ReplayOutbox() error = %v, want %v",
				err,
				ErrOutboxReplayExpired,
			)
		}
	})

	t.Run("deadline mismatch uses stable error", func(t *testing.T) {
		fixture := newWebhookOutboxLifecycleFixture(t, now)
		setWebhookReplayCandidate(t, fixture, models.OutboxDeliveryDead)
		mismatch := fixture.snapshot.CredentialExpiresAt.Add(-time.Second)
		if err := fixture.db.Model(&models.OutboxDelivery{}).
			Where("id = ?", fixture.delivery.ID).
			Update("expires_at", mismatch).Error; err != nil {
			t.Fatal(err)
		}

		err := fixture.service.ReplayOutbox(
			contextWithProjectScope(
				t,
				fixture.scope,
				models.HumanActor(1),
			),
			fixture.delivery.ID,
		)
		if !errors.Is(err, ErrOutboxReplayExpired) {
			t.Fatalf(
				"ReplayOutbox() error = %v, want %v",
				err,
				ErrOutboxReplayExpired,
			)
		}
	})

	t.Run("missing snapshot pair uses stable error", func(t *testing.T) {
		fixture := newWebhookOutboxLifecycleFixture(t, now)
		setWebhookReplayCandidate(t, fixture, models.OutboxDeliveryFailed)
		if err := fixture.db.Exec(
			"DELETE FROM webhook_delivery_snapshots WHERE id = ?",
			fixture.snapshot.ID,
		).Error; err != nil {
			t.Fatal(err)
		}

		err := fixture.service.ReplayOutbox(
			contextWithProjectScope(
				t,
				fixture.scope,
				models.HumanActor(1),
			),
			fixture.delivery.ID,
		)
		if !errors.Is(err, ErrOutboxReplayExpired) {
			t.Fatalf(
				"ReplayOutbox() error = %v, want %v",
				err,
				ErrOutboxReplayExpired,
			)
		}
	})
}

func TestReplayWebhookOutboxExactGenerationCASRejectsSameStatusABA(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 10, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	setWebhookReplayCandidate(t, fixture, models.OutboxDeliveryFailed)

	const callbackName = "test:outbox_replay_same_status_aba"
	changedAt := now.Add(time.Second)
	var injected bool
	if err := fixture.db.Callback().Update().
		Before("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			if injected ||
				tx.Statement == nil ||
				tx.Statement.Table !=
					(models.OutboxDelivery{}).TableName() {
				return
			}
			injected = true
			if _, err := tx.Statement.ConnPool.ExecContext(
				tx.Statement.Context,
				"UPDATE outbox_deliveries "+
					"SET attempts = attempts + 1, updated_at = ? "+
					"WHERE id = ? AND status = ?",
				changedAt,
				fixture.delivery.ID,
				models.OutboxDeliveryFailed,
			); err != nil {
				tx.AddError(err)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Update().Remove(callbackName)
	})

	err := fixture.service.ReplayOutbox(
		contextWithProjectScope(
			t,
			fixture.scope,
			models.HumanActor(1),
		),
		fixture.delivery.ID,
	)
	if !errors.Is(err, ErrOutboxReplayConflict) {
		t.Fatalf(
			"ReplayOutbox() same-status ABA error = %v, want %v",
			err,
			ErrOutboxReplayConflict,
		)
	}
}

func setWebhookReplayCandidate(
	t *testing.T,
	fixture *webhookOutboxLifecycleFixture,
	status models.OutboxDeliveryStatus,
) {
	t.Helper()
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", fixture.delivery.ID).
		Updates(map[string]any{
			"status":   status,
			"attempts": 3,
			"next_attempt_at": fixture.snapshot.CredentialExpiresAt.Add(
				-time.Hour,
			),
			"locked_at":    nil,
			"locked_by":    "",
			"lock_token":   nil,
			"last_error":   "bounded test failure",
			"delivered_at": nil,
			"expired_at":   nil,
		}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.First(
		&fixture.delivery,
		"id = ?",
		fixture.delivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
}

func TestReplayOutboxCASDoesNotClearConcurrentWorkerClaimPostgres(
	t *testing.T,
) {
	db := openOutboxReplayPostgresIntegrationDB(t)
	if err := db.AutoMigrate(
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatalf("migrate isolated Outbox replay tables: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE projects (
			id BIGINT PRIMARY KEY,
			organization_id BIGINT NOT NULL,
			status TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatalf("create isolated Project lifecycle table: %v", err)
	}
	if err := db.Exec(`
		ALTER TABLE outbox_deliveries
		ADD CONSTRAINT chk_outbox_lifecycle_lock_token
		CHECK (
			(
				status = 'processing'
				AND lock_token IS NOT NULL
				AND lock_token ~
					'^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
			)
			OR (
				status <> 'processing'
				AND lock_token IS NULL
			)
		)
	`).Error; err != nil {
		t.Fatalf("install isolated Outbox lifecycle fence: %v", err)
	}
	scope := models.ProjectScope{
		OrganizationID: 11,
		ProjectID:      22,
	}
	if err := db.Exec(
		"INSERT INTO projects (id, organization_id, status) VALUES (?, ?, ?)",
		scope.ProjectID,
		scope.OrganizationID,
		models.ProjectStatusActive,
	).Error; err != nil {
		t.Fatalf("seed isolated Project lifecycle row: %v", err)
	}
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  scope,
			Actor:  models.SystemActor("outbox-replay-test"),
			Source: SourceProtocolWorker,
		},
	)
	if err != nil {
		t.Fatalf("build Outbox replay operation context: %v", err)
	}
	now := time.Date(2026, time.August, 2, 12, 30, 0, 0, time.UTC)
	publishedAt := now.Add(-time.Minute)
	event := models.DomainEvent{
		ID:              "00000000-0000-7000-8000-000000000011",
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
		SpecVersion:     "1.0",
		Source:          "urn:chronodesk:test:outbox-replay-race",
		Type:            "io.chronodesk.test.outbox-replay-race.v1",
		Subject:         "outbox-replay/race",
		Time:            now.Add(-2 * time.Minute),
		DataContentType: "application/json",
		Data:            datatypes.JSON(`{"test":"race"}`),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "outbox-replay-test",
		ResourceVersion: 1,
		PublishedAt:     &publishedAt,
	}
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	delivery := models.OutboxDelivery{
		ID:              "00000000-0000-7000-8000-000000000012",
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
		EventID:         event.ID,
		DestinationType: "test_delivery",
		DestinationID:   "outbox-replay-race",
		Status:          models.OutboxDeliveryFailed,
		Attempts:        3,
		MaxAttempts:     8,
		NextAttemptAt:   now,
		LastError:       "retryable upstream failure",
	}
	if err := db.Create(&delivery).Error; err != nil {
		t.Fatal(err)
	}

	workerTx := db.Begin()
	if workerTx.Error != nil {
		t.Fatal(workerTx.Error)
	}
	workerCommitted := false
	t.Cleanup(func() {
		if !workerCommitted {
			_ = workerTx.Rollback().Error
		}
	})
	var workerView models.OutboxDelivery
	if err := workerTx.Clauses(
		clause.Locking{Strength: "UPDATE"},
	).Where("id = ?", delivery.ID).
		Take(&workerView).Error; err != nil {
		t.Fatalf("worker lock failed delivery: %v", err)
	}
	workerLockAt := now.Add(time.Second)
	generatedWorkerToken, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	workerToken := generatedWorkerToken.String()
	if err := workerTx.Model(&models.OutboxDelivery{}).
		Where(
			"id = ? AND status = ?",
			delivery.ID,
			models.OutboxDeliveryFailed,
		).
		Updates(map[string]any{
			"status":     models.OutboxDeliveryProcessing,
			"attempts":   gorm.Expr("attempts + 1"),
			"locked_at":  workerLockAt,
			"locked_by":  "worker-won-race",
			"lock_token": workerToken,
			"updated_at": workerLockAt,
		}).Error; err != nil {
		t.Fatalf("worker claim failed delivery: %v", err)
	}

	service := NewAgentNativeService(db, AgentNativeOptions{
		Now: func() time.Time { return now.Add(2 * time.Second) },
	})
	replayCtx, cancelReplay := context.WithCancel(ctx)
	replayResult := make(chan error, 1)
	replayDone := make(chan struct{})
	replayStarted := make(chan struct{})
	t.Cleanup(func() {
		cancelReplay()
		if !workerCommitted {
			_ = workerTx.Rollback().Error
		}
		select {
		case <-replayDone:
		case <-time.After(5 * time.Second):
			t.Error("ReplayOutbox goroutine did not join")
		}
	})
	go func() {
		defer close(replayDone)
		close(replayStarted)
		replayResult <- service.ReplayOutbox(
			replayCtx,
			delivery.ID,
		)
	}()

	select {
	case <-replayStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("ReplayOutbox goroutine did not start")
	}
	if err := workerTx.Commit().Error; err != nil {
		t.Fatalf("commit worker claim: %v", err)
	}
	workerCommitted = true

	select {
	case replayErr := <-replayResult:
		if !errors.Is(replayErr, ErrOutboxReplayConflict) {
			t.Fatalf(
				"ReplayOutbox() race error = %v, want %v",
				replayErr,
				ErrOutboxReplayConflict,
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReplayOutbox did not finish after worker commit")
	}
	<-replayDone

	var current models.OutboxDelivery
	if err := db.First(&current, "id = ?", delivery.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != models.OutboxDeliveryProcessing ||
		current.Attempts != delivery.Attempts+1 ||
		current.LockedAt == nil ||
		!current.LockedAt.Equal(workerLockAt) ||
		current.LockedBy != "worker-won-race" ||
		current.LockToken == nil ||
		*current.LockToken != workerToken ||
		current.LastError != delivery.LastError {
		t.Fatalf("replay cleared the winning worker claim: %+v", current)
	}
	var currentEvent models.DomainEvent
	if err := db.First(&currentEvent, "id = ?", event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if currentEvent.PublishedAt == nil ||
		!currentEvent.PublishedAt.Equal(publishedAt) {
		t.Fatalf(
			"rejected race reset event publication: %v",
			currentEvent.PublishedAt,
		)
	}
}

func TestReplayWebhookOutboxExactGenerationCASPostgres(t *testing.T) {
	fixture := newWebhookOutboxLifecyclePostgresFixture(t)
	fixture.clearRows(t)
	deadline := fixture.now.Add(time.Hour)
	pair := fixture.seedPair(
		t,
		fixture.projectA,
		models.OutboxDeliveryFailed,
		deadline,
		"",
		nil,
		2,
	)
	service := fixture.service(fixture.runtimeA, fixture.now)
	const callbackName = "test:postgres_outbox_replay_same_status_aba"
	changedAt := fixture.now.Add(time.Second)
	var injected bool
	if err := fixture.runtimeA.Callback().Update().
		Before("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			if injected ||
				tx.Statement == nil ||
				tx.Statement.Table !=
					(models.OutboxDelivery{}).TableName() {
				return
			}
			injected = true
			if _, err := tx.Statement.ConnPool.ExecContext(
				tx.Statement.Context,
				"UPDATE outbox_deliveries "+
					"SET attempts = attempts + 1, updated_at = $1 "+
					"WHERE id = $2 AND status = $3",
				changedAt,
				pair.delivery.ID,
				models.OutboxDeliveryFailed,
			); err != nil {
				tx.AddError(err)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.runtimeA.Callback().Update().Remove(callbackName)
	})
	replayContext := fixture.workerContext(
		t,
		context.Background(),
		fixture.projectA,
	)
	err := scopeddb.WithProjectScopeContextTransaction(
		replayContext,
		fixture.runtimeA,
		fixture.projectA.Scope(),
		func(scopedCtx context.Context) error {
			return service.ReplayOutbox(scopedCtx, pair.delivery.ID)
		},
	)
	if !injected {
		t.Fatal("same-status PostgreSQL ABA mutation was not injected")
	}
	if !errors.Is(err, ErrOutboxReplayConflict) {
		t.Fatalf(
			"ReplayOutbox() PostgreSQL ABA error = %v, want %v",
			err,
			ErrOutboxReplayConflict,
		)
	}
	current := fixture.loadDelivery(t, pair.delivery.ID)
	if current.Status != models.OutboxDeliveryFailed ||
		current.Attempts != pair.delivery.Attempts ||
		!current.ExpiresAt.Equal(*pair.delivery.ExpiresAt) {
		t.Fatalf(
			"stale replay overwrote PostgreSQL generation: %+v",
			current,
		)
	}
}

func TestReplayWebhookOutboxLosesToConcurrentWorkerClaimPostgres(
	t *testing.T,
) {
	fixture := newWebhookOutboxLifecyclePostgresFixture(t)
	fixture.clearRows(t)
	deadline := fixture.now.Add(time.Hour)
	pair := fixture.seedPair(
		t,
		fixture.projectA,
		models.OutboxDeliveryFailed,
		deadline,
		"",
		nil,
		2,
	)
	workerContext := fixture.workerContext(
		t,
		context.Background(),
		fixture.projectA,
	)
	replayContext := fixture.workerContext(
		t,
		context.Background(),
		fixture.projectA,
	)
	workerClaimed := make(chan struct{})
	releaseWorker := make(chan struct{})
	workerDone := make(chan error, 1)
	replayDone := make(chan error, 1)
	claimAt := fixture.now.Add(time.Second)
	token, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	claimToken := token.String()
	go func() {
		workerDone <- scopeddb.WithProjectScopeContextTransaction(
			workerContext,
			fixture.runtimeB,
			fixture.projectA.Scope(),
			func(scopedCtx context.Context) error {
				db := fixture.runtimeB.WithContext(scopedCtx)
				if err := lockWebhookLifecycleProject(
					db,
					fixture.projectA.Scope(),
				); err != nil {
					return err
				}
				var delivery models.OutboxDelivery
				if err := db.Clauses(
					clause.Locking{Strength: "UPDATE"},
				).Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					pair.delivery.ID,
					fixture.projectA.OrganizationID,
					fixture.projectA.ID,
				).Take(&delivery).Error; err != nil {
					return err
				}
				result := db.Model(&models.OutboxDelivery{}).
					Where(
						"id = ? AND status = ? AND attempts = ?",
						delivery.ID,
						models.OutboxDeliveryFailed,
						delivery.Attempts,
					).
					Updates(map[string]any{
						"status":     models.OutboxDeliveryProcessing,
						"attempts":   gorm.Expr("attempts + 1"),
						"locked_at":  claimAt,
						"locked_by":  "postgres-worker-claim-winner",
						"lock_token": claimToken,
						"updated_at": claimAt,
					})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return errors.New("worker claim lost exact generation CAS")
				}
				close(workerClaimed)
				<-releaseWorker
				return nil
			},
		)
	}()
	select {
	case <-workerClaimed:
	case <-time.After(5 * time.Second):
		t.Fatal("PostgreSQL worker claim did not reach commit gate")
	}
	service := fixture.service(fixture.runtimeA, fixture.now.Add(2*time.Second))
	go func() {
		replayDone <- scopeddb.WithProjectScopeContextTransaction(
			replayContext,
			fixture.runtimeA,
			fixture.projectA.Scope(),
			func(scopedCtx context.Context) error {
				return service.ReplayOutbox(scopedCtx, pair.delivery.ID)
			},
		)
	}()
	fixture.waitForRuntimeBlockedBy(
		t,
		fixture.runtimeAPID,
		fixture.runtimeBPID,
	)
	close(releaseWorker)
	if workerErr := receivePostgresError(t, workerDone); workerErr != nil {
		t.Fatal(workerErr)
	}
	replayErr := receivePostgresError(t, replayDone)
	if !errors.Is(replayErr, ErrOutboxReplayConflict) {
		t.Fatalf(
			"replay/claim error = %v, want %v",
			replayErr,
			ErrOutboxReplayConflict,
		)
	}
	current := fixture.loadDelivery(t, pair.delivery.ID)
	if current.Status != models.OutboxDeliveryProcessing ||
		current.Attempts != pair.delivery.Attempts+1 ||
		current.LockedBy != "postgres-worker-claim-winner" ||
		current.LockToken == nil ||
		*current.LockToken != claimToken {
		t.Fatalf(
			"replay overwrote worker claim generation: %+v",
			current,
		)
	}
}

func openOutboxReplayPostgresIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL Outbox replay evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatal("parse PostgreSQL integration DSN")
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal(
				"Outbox replay integration test requires a loopback PostgreSQL target",
			)
		}
	}
	admin, err := gorm.Open(postgres.Open(rawDSN), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal("open PostgreSQL Outbox replay administrator")
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal("get PostgreSQL Outbox replay administrator pool")
	}
	t.Cleanup(func() { _ = adminSQL.Close() })

	schemaName := fmt.Sprintf(
		"chronodesk_outbox_replay_%d",
		time.Now().UnixNano(),
	)
	quotedSchema := `"` + schemaName + `"`
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create Outbox replay schema: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := admin.Exec(
			"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
		).Error; cleanupErr != nil {
			t.Errorf("drop Outbox replay schema: %v", cleanupErr)
		}
	})

	scopedURL := *parsed
	query := scopedURL.Query()
	query.Set("search_path", schemaName)
	scopedURL.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(scopedURL.String()), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal("open isolated Outbox replay schema")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("get isolated Outbox replay pool")
	}
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func newOutboxReplayTestFixture(
	t *testing.T,
	status models.OutboxDeliveryStatus,
) (
	*gorm.DB,
	*AgentNativeService,
	context.Context,
	models.OutboxDelivery,
	models.DomainEvent,
) {
	t.Helper()
	db := openAgentNativeTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("get Outbox replay SQLite pool")
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx := testProjectOperationContext(
		t,
		db,
		models.SystemActor("outbox-replay-test"),
	)
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	publishedAt := now.Add(-time.Minute)
	event := models.DomainEvent{
		ID:              "00000000-0000-7000-8000-000000000001",
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
		SpecVersion:     "1.0",
		Source:          "urn:chronodesk:test:outbox-replay",
		Type:            "io.chronodesk.test.outbox-replay.v1",
		Subject:         "outbox-replay/test",
		Time:            now.Add(-2 * time.Minute),
		DataContentType: "application/json",
		Data:            datatypes.JSON(`{"test":true}`),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "outbox-replay-test",
		ResourceVersion: 1,
		PublishedAt:     &publishedAt,
	}
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	lockTime := now.Add(-30 * time.Second)
	deliveryTime := now.Add(-time.Minute)
	delivery := models.OutboxDelivery{
		ID:              "00000000-0000-7000-8000-000000000002",
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
		EventID:         event.ID,
		DestinationType: "event_stream",
		DestinationID:   "outbox-replay-test",
		Status:          status,
		Attempts:        3,
		MaxAttempts:     8,
		NextAttemptAt:   now.Add(time.Hour),
		LockedAt:        &lockTime,
		LockedBy:        "worker-before-replay",
		LastError:       "test delivery failure",
		DeliveredAt:     &deliveryTime,
	}
	if err := db.Create(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	service := NewAgentNativeService(db, AgentNativeOptions{
		Now: func() time.Time { return now },
	})
	return db, service, ctx, delivery, event
}

func assertOutboxReplayReset(
	t *testing.T,
	db *gorm.DB,
	original models.OutboxDelivery,
	event models.DomainEvent,
) {
	t.Helper()
	var current models.OutboxDelivery
	if err := db.First(&current, "id = ?", original.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != models.OutboxDeliveryPending ||
		current.Attempts != 0 ||
		current.LockedAt != nil ||
		current.LockedBy != "" ||
		current.LastError != "" ||
		current.DeliveredAt != nil {
		t.Fatalf("replayed delivery was not reset: %+v", current)
	}
	var currentEvent models.DomainEvent
	if err := db.First(&currentEvent, "id = ?", event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if currentEvent.PublishedAt != nil {
		t.Fatalf(
			"replayed event published_at = %v, want nil",
			currentEvent.PublishedAt,
		)
	}
}

func assertOutboxReplayRejectedUnchanged(
	t *testing.T,
	db *gorm.DB,
	original models.OutboxDelivery,
	event models.DomainEvent,
) {
	t.Helper()
	var current models.OutboxDelivery
	if err := db.First(&current, "id = ?", original.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != original.Status ||
		current.Attempts != original.Attempts ||
		current.LockedAt == nil ||
		!current.LockedAt.Equal(*original.LockedAt) ||
		current.LockedBy != original.LockedBy ||
		current.LastError != original.LastError ||
		current.DeliveredAt == nil ||
		!current.DeliveredAt.Equal(*original.DeliveredAt) {
		t.Fatalf(
			"rejected replay changed delivery: before=%+v after=%+v",
			original,
			current,
		)
	}
	var currentEvent models.DomainEvent
	if err := db.First(&currentEvent, "id = ?", event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if currentEvent.PublishedAt == nil ||
		!currentEvent.PublishedAt.Equal(*event.PublishedAt) {
		t.Fatalf(
			"rejected replay changed event publication: before=%v after=%v",
			event.PublishedAt,
			currentEvent.PublishedAt,
		)
	}
}
