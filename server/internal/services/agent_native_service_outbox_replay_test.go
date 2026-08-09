package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
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
	scope := models.ProjectScope{
		OrganizationID: 11,
		ProjectID:      22,
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
			"updated_at": workerLockAt,
		}).Error; err != nil {
		t.Fatalf("worker claim failed delivery: %v", err)
	}

	replayRead := make(chan struct{})
	var signalOnce sync.Once
	const callbackName = "test:observe_outbox_replay_stale_read"
	if err := db.Callback().Query().
		After("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement != nil &&
				tx.Statement.Table == (models.OutboxDelivery{}).TableName() {
				signalOnce.Do(func() { close(replayRead) })
			}
		}); err != nil {
		t.Fatalf("register replay read observer: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(callbackName)
	})

	service := NewAgentNativeService(db, AgentNativeOptions{
		Now: func() time.Time { return now.Add(2 * time.Second) },
	})
	replayResult := make(chan error, 1)
	go func() {
		replayResult <- service.ReplayOutbox(ctx, delivery.ID)
	}()

	select {
	case <-replayRead:
	case <-time.After(5 * time.Second):
		t.Fatal("ReplayOutbox did not read the pre-claim snapshot")
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

	var current models.OutboxDelivery
	if err := db.First(&current, "id = ?", delivery.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != models.OutboxDeliveryProcessing ||
		current.Attempts != delivery.Attempts+1 ||
		current.LockedAt == nil ||
		!current.LockedAt.Equal(workerLockAt) ||
		current.LockedBy != "worker-won-race" ||
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
		t.Fatalf("parse PostgreSQL integration DSN: %v", err)
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
		t.Fatalf("open PostgreSQL Outbox replay administrator: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
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
		t.Fatalf("open isolated Outbox replay schema: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
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
