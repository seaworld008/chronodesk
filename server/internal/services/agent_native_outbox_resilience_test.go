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

func TestProcessOutboxBatchTimesOutBlackHoleAndCanRetry(t *testing.T) {
	db := openAgentNativeTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close black-hole Outbox test database: %v", err)
		}
	})
	service := NewAgentNativeService(db, AgentNativeOptions{
		OutboxLockTTL:             time.Second,
		OutboxDeliveryTimeout:     60 * time.Millisecond,
		OutboxDeliveryConcurrency: 1,
	})
	createOutboxResilienceEvent(t, service, 1)

	release := make(chan struct{})
	started := make(chan struct{})
	var returned atomic.Bool
	deliverer := OutboxDeliverFunc(func(
		context.Context,
		*models.OutboxDelivery,
		CloudEventEnvelope,
	) error {
		close(started)
		<-release
		returned.Store(true)
		return nil
	})

	start := time.Now()
	result, err := service.ProcessOutboxBatch(
		context.Background(),
		"timeout-worker",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("黑洞投递阻塞批次过久: %s", elapsed)
	}
	if result.Failed != 1 || result.Delivered != 0 {
		t.Fatalf("超时批次结果 = %+v", result)
	}
	<-started
	var delivery models.OutboxDelivery
	if err := db.First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.Status != models.OutboxDeliveryFailed ||
		delivery.LockedAt != nil ||
		!strings.Contains(delivery.LastError, "timed out") {
		t.Fatalf("超时投递没有解除锁并记录失败: %+v", delivery)
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for (!returned.Load() || len(service.outboxDeliverySlots) != 0) &&
		time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !returned.Load() || len(service.outboxDeliverySlots) != 0 {
		t.Fatal("释放后投递适配器没有返回并释放并发槽")
	}
	if err := db.Model(&models.OutboxDelivery{}).
		Where("id = ?", delivery.ID).
		Update(
			"next_attempt_at",
			time.Now().UTC().Add(-time.Second),
		).Error; err != nil {
		t.Fatal(err)
	}
	retried, err := service.ProcessOutboxBatch(
		context.Background(),
		"retry-worker",
		10,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Delivered != 1 {
		t.Fatalf("解除黑洞后重试结果 = %+v", retried)
	}
}

func TestProcessOutboxBatchRecoversDelivererPanic(t *testing.T) {
	db := openAgentNativeTestDB(t)
	closeAgentNativeOutboxTestDB(t, db)
	service := NewAgentNativeService(db, AgentNativeOptions{
		OutboxLockTTL:         time.Second,
		OutboxDeliveryTimeout: 200 * time.Millisecond,
	})
	createOutboxResilienceEvent(t, service, 1)

	result, err := service.ProcessOutboxBatch(
		context.Background(),
		"panic-worker",
		10,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			panic("sensitive panic payload")
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 {
		t.Fatalf("panic 批次结果 = %+v", result)
	}
	var delivery models.OutboxDelivery
	if err := db.First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.LastError != "outbox deliverer panicked" ||
		strings.Contains(delivery.LastError, "sensitive") {
		t.Fatalf("panic 错误没有安全归一化: %q", delivery.LastError)
	}
}

func TestProcessOutboxBatchUsesBoundedConcurrency(t *testing.T) {
	db := openAgentNativeTestDB(t)
	closeAgentNativeOutboxTestDB(t, db)
	service := NewAgentNativeService(db, AgentNativeOptions{
		OutboxLockTTL:             time.Second,
		OutboxDeliveryTimeout:     500 * time.Millisecond,
		OutboxDeliveryConcurrency: 4,
	})
	createOutboxResilienceEvent(t, service, 2)

	var current atomic.Int32
	var maximum atomic.Int32
	deliverer := OutboxDeliverFunc(func(
		context.Context,
		*models.OutboxDelivery,
		CloudEventEnvelope,
	) error {
		active := current.Add(1)
		defer current.Add(-1)
		for {
			observed := maximum.Load()
			if active <= observed || maximum.CompareAndSwap(observed, active) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
		return nil
	})
	result, err := service.ProcessOutboxBatch(
		context.Background(),
		"bounded-worker",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Delivered != 2 {
		t.Fatalf("受控并发批次结果 = %+v", result)
	}
	if maximum.Load() < 2 || maximum.Load() > 2 {
		t.Fatalf("最大并发数 = %d，期望 2", maximum.Load())
	}
}

func TestProcessOutboxBatchPersistsCancellationWithoutLockingDelivery(t *testing.T) {
	db := openAgentNativeTestDB(t)
	closeAgentNativeOutboxTestDB(t, db)
	service := NewAgentNativeService(db, AgentNativeOptions{
		OutboxLockTTL:         time.Second,
		OutboxDeliveryTimeout: 500 * time.Millisecond,
	})
	createOutboxResilienceEvent(t, service, 1)

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan struct {
		result OutboxBatchResult
		err    error
	}, 1)
	go func() {
		result, err := service.ProcessOutboxBatch(
			ctx,
			"cancel-worker",
			10,
			OutboxDeliverFunc(func(
				attemptCtx context.Context,
				_ *models.OutboxDelivery,
				_ CloudEventEnvelope,
			) error {
				close(started)
				<-attemptCtx.Done()
				return attemptCtx.Err()
			}),
		)
		done <- struct {
			result OutboxBatchResult
			err    error
		}{result: result, err: err}
	}()
	<-started
	cancel()
	outcome := <-done
	if outcome.err != nil && !errors.Is(outcome.err, context.Canceled) {
		t.Fatal(outcome.err)
	}
	if outcome.result.Failed != 1 {
		t.Fatalf("取消批次结果 = %+v", outcome.result)
	}
	var delivery models.OutboxDelivery
	if err := db.First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.LockedAt != nil || delivery.Status != models.OutboxDeliveryFailed {
		t.Fatalf("取消后投递仍被锁定: %+v", delivery)
	}
}

func TestProcessOutboxBatchIsolatesBlackHoleLaneAndReportsSaturation(
	t *testing.T,
) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	fixture.service.outboxDeliveryTimeout = 30 * time.Millisecond
	fixture.service.configureOutboxDeliveryBulkheads(8)
	for index := 0; index < 3; index++ {
		fixture.createIntent(t, fmt.Sprintf("bulkhead-black-hole-%d", index))
	}
	healthyProject := seedLifecycleWorkerProject(t, fixture, 91)
	seedLifecycleNonWebhookDelivery(t, fixture, healthyProject, 91001)

	release := make(chan struct{})
	var (
		activeWebhook   atomic.Int32
		maximumWebhook  atomic.Int32
		returnedWebhook atomic.Int32
		healthyCalls    atomic.Int32
	)
	deliverer := OutboxDeliverFunc(func(
		_ context.Context,
		delivery *models.OutboxDelivery,
		_ CloudEventEnvelope,
	) error {
		if delivery.DestinationType != "webhook" {
			healthyCalls.Add(1)
			return nil
		}
		active := activeWebhook.Add(1)
		for {
			observed := maximumWebhook.Load()
			if active <= observed ||
				maximumWebhook.CompareAndSwap(observed, active) {
				break
			}
		}
		<-release
		activeWebhook.Add(-1)
		returnedWebhook.Add(1)
		return nil
	})

	first, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"bulkhead-black-hole-worker",
		8,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Claimed != 5 ||
		first.Delivered != 1 ||
		first.Expired != 4 ||
		healthyCalls.Load() != 1 {
		t.Fatalf(
			"black-hole isolation batch = %+v healthy=%d",
			first,
			healthyCalls.Load(),
		)
	}
	if maximumWebhook.Load() != 4 {
		t.Fatalf(
			"webhook lane maximum concurrency = %d, want 4",
			maximumWebhook.Load(),
		)
	}
	if len(fixture.service.outboxDeliverySlots) != 4 ||
		len(fixture.service.outboxDeliveryLaneSlots[OutboxDeliveryLaneWebhook]) != 4 {
		t.Fatalf(
			"black-hole permits global=%d webhook=%d, want 4/4",
			len(fixture.service.outboxDeliverySlots),
			len(fixture.service.outboxDeliveryLaneSlots[OutboxDeliveryLaneWebhook]),
		)
	}

	pendingWebhook, _, _ := fixture.createIntent(
		t,
		"bulkhead-pending-while-saturated",
	)
	seedLifecycleNonWebhookDelivery(t, fixture, healthyProject, 91002)
	second, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"bulkhead-healthy-worker",
		8,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Claimed != 1 ||
		second.Delivered != 1 ||
		second.Status != OutboxBatchStatusPartialSaturation ||
		!outboxBatchHasSaturatedLane(
			second,
			OutboxDeliveryLaneWebhook,
		) {
		t.Fatalf("healthy lane during saturation batch = %+v", second)
	}
	var pending models.OutboxDelivery
	if err := fixture.db.First(
		&pending,
		"id = ?",
		pendingWebhook.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if pending.Status != models.OutboxDeliveryPending ||
		pending.Attempts != 0 ||
		pending.LockedAt != nil {
		t.Fatalf(
			"saturated webhook was claimed or attempted: %+v",
			pending,
		)
	}

	saturated, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"bulkhead-saturated-worker",
		8,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if saturated.Claimed != 0 ||
		saturated.Status != OutboxBatchStatusSaturated ||
		!outboxBatchHasSaturatedLane(
			saturated,
			OutboxDeliveryLaneWebhook,
		) {
		t.Fatalf("zero-work saturation batch = %+v", saturated)
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for (returnedWebhook.Load() != 4 ||
		len(fixture.service.outboxDeliverySlots) != 0 ||
		len(fixture.service.outboxDeliveryLaneSlots[OutboxDeliveryLaneWebhook]) != 0) &&
		time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if returnedWebhook.Load() != 4 ||
		len(fixture.service.outboxDeliverySlots) != 0 ||
		len(fixture.service.outboxDeliveryLaneSlots[OutboxDeliveryLaneWebhook]) != 0 {
		t.Fatalf(
			"bulkhead did not recover returned=%d global=%d webhook=%d",
			returnedWebhook.Load(),
			len(fixture.service.outboxDeliverySlots),
			len(fixture.service.outboxDeliveryLaneSlots[OutboxDeliveryLaneWebhook]),
		)
	}

	recovered, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"bulkhead-recovered-worker",
		8,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Delivered != 1 ||
		recovered.Status != OutboxBatchStatusProcessed {
		t.Fatalf("recovered webhook lane batch = %+v", recovered)
	}
	var processing int64
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("status = ?", models.OutboxDeliveryProcessing).
		Count(&processing).Error; err != nil {
		t.Fatal(err)
	}
	if processing != 0 {
		t.Fatalf("bulkhead recovery left %d processing locks", processing)
	}
}

func TestProcessOutboxBatchCallbackBacklogDoesNotStarveHealthySameProjectLanes(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	closeAgentNativeOutboxTestDB(t, db)
	service := NewAgentNativeService(db, AgentNativeOptions{
		OutboxLockTTL:             time.Second,
		OutboxDeliveryTimeout:     30 * time.Millisecond,
		OutboxDeliveryConcurrency: 8,
	})
	targets := make([]OutboxTarget, 0, 14)
	for index := 0; index < 12; index++ {
		targets = append(targets, OutboxTarget{
			Type:        EmailOutboxDestination,
			ID:          fmt.Sprintf("callback-black-hole-%d", index),
			MaxAttempts: 3,
		})
	}
	targets = append(
		targets,
		OutboxTarget{
			Type:        KnowledgeIndexRebuildOutboxDestination,
			ID:          "healthy-storage",
			MaxAttempts: 3,
		},
		OutboxTarget{
			Type:        "event_stream",
			ID:          "healthy-internal",
			MaxAttempts: 3,
		},
	)
	event, err := service.createDomainEvent(
		t,
		context.Background(),
		DomainEventInput{
			Type: "io.chronodesk.outbox.callback-bulkhead." +
				"test.v1",
			Subject:         "test/outbox/callback-bulkhead",
			Actor:           models.SystemActor("outbox-callback-test"),
			ResourceVersion: 1,
			Data:            map[string]any{"test": true},
		},
		targets,
	)
	if err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	var (
		activeCallback   atomic.Int32
		maximumCallback  atomic.Int32
		returnedCallback atomic.Int32
		healthyStorage   atomic.Int32
		healthyInternal  atomic.Int32
	)
	deliverer := OutboxDeliverFunc(func(
		_ context.Context,
		delivery *models.OutboxDelivery,
		_ CloudEventEnvelope,
	) error {
		switch delivery.DestinationType {
		case EmailOutboxDestination:
			current := activeCallback.Add(1)
			for {
				observed := maximumCallback.Load()
				if current <= observed ||
					maximumCallback.CompareAndSwap(observed, current) {
					break
				}
			}
			<-release
			activeCallback.Add(-1)
			returnedCallback.Add(1)
		case KnowledgeIndexRebuildOutboxDestination:
			healthyStorage.Add(1)
		case "event_stream":
			healthyInternal.Add(1)
		}
		return nil
	})

	result, err := service.ProcessOutboxBatch(
		context.Background(),
		"callback-bulkhead-worker",
		8,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Claimed != 6 ||
		result.Delivered != 2 ||
		result.Failed != 4 ||
		result.Status != OutboxBatchStatusPartialSaturation ||
		!outboxBatchHasSaturatedLane(
			result,
			OutboxDeliveryLaneCallback,
		) {
		t.Fatalf("callback bulkhead batch = %+v", result)
	}
	if maximumCallback.Load() != 4 ||
		healthyStorage.Load() != 1 ||
		healthyInternal.Load() != 1 {
		t.Fatalf(
			"callback maximum=%d healthy storage=%d internal=%d",
			maximumCallback.Load(),
			healthyStorage.Load(),
			healthyInternal.Load(),
		)
	}
	if len(service.outboxDeliverySlots) != 4 ||
		len(service.outboxDeliveryLaneSlots[OutboxDeliveryLaneCallback]) != 4 ||
		len(service.outboxDeliveryLaneSlots[OutboxDeliveryLaneStorage]) != 0 ||
		len(service.outboxDeliveryLaneSlots[OutboxDeliveryLaneInternal]) != 0 {
		t.Fatalf(
			"callback permits global=%d callback=%d storage=%d internal=%d",
			len(service.outboxDeliverySlots),
			len(service.outboxDeliveryLaneSlots[OutboxDeliveryLaneCallback]),
			len(service.outboxDeliveryLaneSlots[OutboxDeliveryLaneStorage]),
			len(service.outboxDeliveryLaneSlots[OutboxDeliveryLaneInternal]),
		)
	}
	var untouchedCallbacks int64
	if err := db.Model(&models.OutboxDelivery{}).
		Where(
			"event_id = ? AND destination_type = ? AND status = ? "+
				"AND attempts = 0 AND locked_at IS NULL",
			event.ID,
			EmailOutboxDestination,
			models.OutboxDeliveryPending,
		).
		Count(&untouchedCallbacks).Error; err != nil {
		t.Fatal(err)
	}
	if untouchedCallbacks != 8 {
		t.Fatalf(
			"untouched callback backlog = %d, want 8",
			untouchedCallbacks,
		)
	}
	var processing int64
	if err := db.Model(&models.OutboxDelivery{}).
		Where("status = ?", models.OutboxDeliveryProcessing).
		Count(&processing).Error; err != nil {
		t.Fatal(err)
	}
	if processing != 0 {
		t.Fatalf("callback bulkhead left %d processing locks", processing)
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for (returnedCallback.Load() != 4 ||
		len(service.outboxDeliverySlots) != 0 ||
		len(service.outboxDeliveryLaneSlots[OutboxDeliveryLaneCallback]) != 0) &&
		time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if returnedCallback.Load() != 4 ||
		len(service.outboxDeliverySlots) != 0 ||
		len(service.outboxDeliveryLaneSlots[OutboxDeliveryLaneCallback]) != 0 {
		t.Fatalf(
			"callback permits did not recover returned=%d global=%d callback=%d",
			returnedCallback.Load(),
			len(service.outboxDeliverySlots),
			len(service.outboxDeliveryLaneSlots[OutboxDeliveryLaneCallback]),
		)
	}
}

func TestProcessOutboxBatchBoundsUnknownDestinationLane(t *testing.T) {
	db := openAgentNativeTestDB(t)
	closeAgentNativeOutboxTestDB(t, db)
	service := NewAgentNativeService(db, AgentNativeOptions{
		OutboxLockTTL:             time.Second,
		OutboxDeliveryTimeout:     30 * time.Millisecond,
		OutboxDeliveryConcurrency: 8,
	})
	createOutboxResilienceEvent(t, service, 6)

	release := make(chan struct{})
	var (
		active   atomic.Int32
		maximum  atomic.Int32
		returned atomic.Int32
	)
	deliverer := OutboxDeliverFunc(func(
		context.Context,
		*models.OutboxDelivery,
		CloudEventEnvelope,
	) error {
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		<-release
		active.Add(-1)
		returned.Add(1)
		return nil
	})
	first, err := service.ProcessOutboxBatch(
		context.Background(),
		"unknown-lane-worker",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Claimed != 4 ||
		first.Failed != 4 ||
		maximum.Load() != 4 ||
		!outboxBatchHasSaturatedLane(
			first,
			OutboxDeliveryLaneOther,
		) {
		t.Fatalf(
			"unknown destination bulkhead batch=%+v maximum=%d",
			first,
			maximum.Load(),
		)
	}
	var untouched int64
	if err := db.Model(&models.OutboxDelivery{}).
		Where(
			"status = ? AND attempts = 0 AND locked_at IS NULL",
			models.OutboxDeliveryPending,
		).
		Count(&untouched).Error; err != nil {
		t.Fatal(err)
	}
	if untouched != 2 {
		t.Fatalf("unknown saturated lane untouched rows = %d, want 2", untouched)
	}
	second, err := service.ProcessOutboxBatch(
		context.Background(),
		"unknown-lane-saturated-worker",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Claimed != 0 ||
		second.Status != OutboxBatchStatusSaturated ||
		!outboxBatchHasSaturatedLane(
			second,
			OutboxDeliveryLaneOther,
		) {
		t.Fatalf("unknown zero-work saturation batch = %+v", second)
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for (returned.Load() != 4 ||
		len(service.outboxDeliverySlots) != 0 ||
		len(service.outboxDeliveryLaneSlots[OutboxDeliveryLaneOther]) != 0) &&
		time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if returned.Load() != 4 ||
		len(service.outboxDeliverySlots) != 0 ||
		len(service.outboxDeliveryLaneSlots[OutboxDeliveryLaneOther]) != 0 {
		t.Fatalf(
			"unknown lane did not recover returned=%d global=%d other=%d",
			returned.Load(),
			len(service.outboxDeliverySlots),
			len(service.outboxDeliveryLaneSlots[OutboxDeliveryLaneOther]),
		)
	}
	recovered, err := service.ProcessOutboxBatch(
		context.Background(),
		"unknown-lane-recovered-worker",
		10,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Delivered != 2 {
		t.Fatalf("unknown lane recovery batch = %+v", recovered)
	}
}

func TestProcessOutboxBatchRevisitsCapacitySkippedRowBeforeNewerTraffic(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	closeAgentNativeOutboxTestDB(t, db)
	service := NewAgentNativeService(db, AgentNativeOptions{
		OutboxLockTTL:             time.Second,
		OutboxDeliveryTimeout:     100 * time.Millisecond,
		OutboxDeliveryConcurrency: 2,
	})
	createOutboxResilienceEvent(t, service, 1)

	var skipped models.OutboxDelivery
	if err := db.First(&skipped).Error; err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-time.Minute)
	if err := db.Model(&models.OutboxDelivery{}).
		Where("id = ?", skipped.ID).
		Updates(map[string]any{
			"created_at":      base,
			"next_attempt_at": base,
		}).Error; err != nil {
		t.Fatal(err)
	}
	cursorClass := "non-webhook:" +
		string(OutboxDeliveryLaneOther) +
		":retry"
	fixtureScope := models.ProjectScope{
		OrganizationID: skipped.OrganizationID,
		ProjectID:      skipped.ProjectID,
	}
	service.compareAndSetOutboxClaimScanCursor(
		fixtureScope,
		cursorClass,
		outboxClaimScanCursor{},
		outboxClaimScanCursor{
			sortAt:    base.Add(time.Second),
			createdAt: base.Add(time.Second),
			stableID:  "ffffffff-ffff-7fff-bfff-ffffffffffff",
		},
	)

	blockingPermit, blocked := service.tryAcquireOutboxDeliveryPermit(
		OutboxDeliveryLaneOther,
	)
	if blockingPermit == nil || blocked != outboxPermitAvailable {
		t.Fatalf("reserve blocking permit = %v, blocked=%d", blockingPermit, blocked)
	}
	defer blockingPermit.release()

	saturated, err := service.ProcessOutboxBatch(
		context.Background(),
		"capacity-skipped-worker",
		1,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			return errors.New("capacity-skipped row reached adapter")
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if saturated.Claimed != 0 ||
		saturated.Status != OutboxBatchStatusSaturated ||
		!outboxBatchHasSaturatedLane(
			saturated,
			OutboxDeliveryLaneOther,
		) {
		t.Fatalf("capacity-skipped batch = %+v", saturated)
	}
	blockingPermit.release()

	createOutboxResilienceEvent(t, service, 1)
	var successor models.OutboxDelivery
	if err := db.Where("id <> ?", skipped.ID).First(&successor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxDelivery{}).
		Where("id = ?", successor.ID).
		Updates(map[string]any{
			"created_at":      base.Add(2 * time.Second),
			"next_attempt_at": base.Add(2 * time.Second),
		}).Error; err != nil {
		t.Fatal(err)
	}

	var deliveredID string
	recovered, err := service.ProcessOutboxBatch(
		context.Background(),
		"capacity-recovered-worker",
		1,
		OutboxDeliverFunc(func(
			_ context.Context,
			delivery *models.OutboxDelivery,
			_ CloudEventEnvelope,
		) error {
			deliveredID = delivery.ID
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Delivered != 1 || deliveredID != skipped.ID {
		t.Fatalf(
			"capacity recovery delivered=%s batch=%+v, want skipped=%s before successor=%s",
			deliveredID,
			recovered,
			skipped.ID,
			successor.ID,
		)
	}
	var remaining models.OutboxDelivery
	if err := db.First(&remaining, "id = ?", successor.ID).Error; err != nil {
		t.Fatal(err)
	}
	if remaining.Status != models.OutboxDeliveryPending ||
		remaining.Attempts != 0 ||
		remaining.LockedAt != nil {
		t.Fatalf("newer successor was claimed before skipped row: %+v", remaining)
	}
}

func TestProcessOutboxBatchReportsGlobalSaturationWithoutClaiming(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	closeAgentNativeOutboxTestDB(t, db)
	service := NewAgentNativeService(db, AgentNativeOptions{
		OutboxLockTTL:             time.Second,
		OutboxDeliveryTimeout:     30 * time.Millisecond,
		OutboxDeliveryConcurrency: 8,
	})
	targets := make([]OutboxTarget, 0, 8)
	for index := 0; index < 4; index++ {
		targets = append(targets, OutboxTarget{
			Type:        "future-callback",
			ID:          fmt.Sprintf("other-%d", index),
			MaxAttempts: 3,
		})
	}
	for index := 0; index < 4; index++ {
		targets = append(targets, OutboxTarget{
			Type:        "event_stream",
			ID:          fmt.Sprintf("internal-%d", index),
			MaxAttempts: 3,
		})
	}
	if _, err := service.createDomainEvent(
		t,
		context.Background(),
		DomainEventInput{
			Type:            "io.chronodesk.outbox.global-saturation.test.v1",
			Subject:         "test/outbox/global-saturation",
			Actor:           models.SystemActor("outbox-global-test"),
			ResourceVersion: 1,
			Data:            map[string]any{"test": true},
		},
		targets,
	); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	var (
		active   atomic.Int32
		maximum  atomic.Int32
		returned atomic.Int32
	)
	deliverer := OutboxDeliverFunc(func(
		context.Context,
		*models.OutboxDelivery,
		CloudEventEnvelope,
	) error {
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		<-release
		active.Add(-1)
		returned.Add(1)
		return nil
	})
	first, err := service.ProcessOutboxBatch(
		context.Background(),
		"global-fill-worker",
		8,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Claimed != 8 ||
		first.Failed != 8 ||
		maximum.Load() != 8 ||
		len(service.outboxDeliverySlots) != 8 {
		t.Fatalf(
			"global fill batch=%+v maximum=%d permits=%d",
			first,
			maximum.Load(),
			len(service.outboxDeliverySlots),
		)
	}

	var firstProject models.Project
	if err := db.Order("id ASC").First(&firstProject).Error; err != nil {
		t.Fatal(err)
	}
	pendingProject := firstProject
	pendingProject.ID = 0
	pendingProject.PublicID = ""
	pendingProject.CreatedAt = time.Time{}
	pendingProject.UpdatedAt = time.Time{}
	pendingProject.Key = models.ProjectKey("GLOB2")
	pendingProject.Name = "global saturation later project"
	if err := db.Create(&pendingProject).Error; err != nil {
		t.Fatal(err)
	}
	pendingActor := models.SystemActor("outbox-global-test")
	pendingEvent, err := service.createDomainEvent(
		t,
		contextWithProjectScope(t, pendingProject.Scope(), pendingActor),
		DomainEventInput{
			Type:            "io.chronodesk.outbox.global-pending.test.v1",
			Subject:         "test/outbox/global-pending",
			Actor:           pendingActor,
			ResourceVersion: 1,
			Scope:           pendingProject.Scope(),
			Data:            map[string]any{"test": true},
		},
		[]OutboxTarget{{
			Type:        EmailOutboxDestination,
			ID:          "global-pending",
			MaxAttempts: 3,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Start at the earlier project, whose timed-out rows are not retryable yet.
	// The pending delivery exists only in the later project. Full global
	// capacity must produce a typed result without probing either project.
	service.outboxProjectCursor.Store(0)
	var candidateQueries atomic.Int32
	const queryCallback = "test:global_saturation_skips_claim_queries"
	if err := db.Callback().Query().After("gorm:query").
		Register(queryCallback, func(tx *gorm.DB) {
			if strings.Contains(
				strings.ToLower(tx.Statement.SQL.String()),
				"from `outbox_deliveries`",
			) {
				candidateQueries.Add(1)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(queryCallback)
	})
	saturated, err := service.ProcessOutboxBatch(
		context.Background(),
		"global-saturated-worker",
		8,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if saturated.Claimed != 0 ||
		saturated.Status != OutboxBatchStatusSaturated ||
		!saturated.GlobalSaturated {
		t.Fatalf("global saturation batch = %+v", saturated)
	}
	if candidateQueries.Load() != 0 {
		t.Fatalf(
			"global saturation made %d Outbox candidate queries, want 0",
			candidateQueries.Load(),
		)
	}
	var pending models.OutboxDelivery
	if err := db.Where("event_id = ?", pendingEvent.ID).
		Take(&pending).Error; err != nil {
		t.Fatal(err)
	}
	if pending.Status != models.OutboxDeliveryPending ||
		pending.ProjectID != pendingProject.ID ||
		pending.Attempts != 0 ||
		pending.LockedAt != nil {
		t.Fatalf("global saturation claimed pending row: %+v", pending)
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for (returned.Load() != 8 ||
		len(service.outboxDeliverySlots) != 0) &&
		time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if returned.Load() != 8 ||
		len(service.outboxDeliverySlots) != 0 {
		t.Fatalf(
			"global permits did not recover returned=%d permits=%d",
			returned.Load(),
			len(service.outboxDeliverySlots),
		)
	}
	recovered, err := service.ProcessOutboxBatch(
		context.Background(),
		"global-recovered-worker",
		8,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Delivered != 1 {
		t.Fatalf("global recovery batch = %+v", recovered)
	}
}

func TestAgentNativeOutboxBulkheadsUseFiniteLanesAndHardGlobalLimit(
	t *testing.T,
) {
	service := NewAgentNativeService(
		openAgentNativeTestDB(t),
		AgentNativeOptions{OutboxDeliveryConcurrency: 64},
	)
	closeAgentNativeOutboxTestDB(t, service.db)
	if cap(service.outboxDeliverySlots) != 8 {
		t.Fatalf(
			"global Outbox concurrency = %d, want hard limit 8",
			cap(service.outboxDeliverySlots),
		)
	}
	if len(service.outboxDeliveryLaneSlots) != 5 {
		t.Fatalf(
			"Outbox lane cardinality = %d, want 5",
			len(service.outboxDeliveryLaneSlots),
		)
	}
	for _, lane := range outboxDeliveryLaneOrder {
		if cap(service.outboxDeliveryLaneSlots[lane]) != 4 {
			t.Fatalf(
				"Outbox lane %s capacity = %d, want 4",
				lane,
				cap(service.outboxDeliveryLaneSlots[lane]),
			)
		}
	}
	tests := map[string]OutboxDeliveryLane{
		"webhook":                    OutboxDeliveryLaneWebhook,
		"a2a_push":                   OutboxDeliveryLaneCallback,
		"email":                      OutboxDeliveryLaneCallback,
		"attachment_upload":          OutboxDeliveryLaneStorage,
		"attachment_cleanup":         OutboxDeliveryLaneStorage,
		"attachment_staging_cleanup": OutboxDeliveryLaneStorage,
		"knowledge_index_rebuild":    OutboxDeliveryLaneStorage,
		"event_stream":               OutboxDeliveryLaneInternal,
		"automation":                 OutboxDeliveryLaneInternal,
		"notification":               OutboxDeliveryLaneInternal,
		"sla_escalation":             OutboxDeliveryLaneInternal,
		"private-future-destination": OutboxDeliveryLaneOther,
	}
	for destination, want := range tests {
		if got := outboxDeliveryLaneForDestination(destination); got != want {
			t.Fatalf(
				"destination %q lane = %s, want %s",
				destination,
				got,
				want,
			)
		}
	}
}

func TestProcessOutboxBatchReportsIdleSeparatelyFromSaturation(t *testing.T) {
	db := openAgentNativeTestDB(t)
	closeAgentNativeOutboxTestDB(t, db)
	testProjectOperationContext(
		t,
		db,
		models.SystemActor("outbox-idle-test"),
	)
	service := NewAgentNativeService(db)
	result, err := service.ProcessOutboxBatch(
		context.Background(),
		"idle-worker",
		10,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != OutboxBatchStatusIdle ||
		len(result.SaturatedLanes) != 0 ||
		result.GlobalSaturated {
		t.Fatalf("empty Outbox status = %+v", result)
	}
}

func closeAgentNativeOutboxTestDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close Outbox test database: %v", err)
		}
	})
}

func outboxBatchHasSaturatedLane(
	result OutboxBatchResult,
	lane OutboxDeliveryLane,
) bool {
	for _, saturated := range result.SaturatedLanes {
		if saturated == lane {
			return true
		}
	}
	return false
}

func createOutboxResilienceEvent(
	t *testing.T,
	service *AgentNativeService,
	targetCount int,
) {
	t.Helper()
	targets := make([]OutboxTarget, 0, targetCount)
	for index := range targetCount {
		targets = append(targets, OutboxTarget{
			Type:        "test",
			ID:          fmt.Sprintf("destination-%d", index),
			MaxAttempts: 3,
		})
	}
	if _, err := service.createDomainEvent(
		t,
		context.Background(),
		DomainEventInput{
			Type:            "io.chronodesk.outbox.resilience.test.v1",
			Subject:         "test/outbox",
			Actor:           models.SystemActor("outbox-resilience-test"),
			ResourceVersion: 1,
			Data:            map[string]any{"test": true},
		},
		targets,
	); err != nil {
		t.Fatal(err)
	}
}
