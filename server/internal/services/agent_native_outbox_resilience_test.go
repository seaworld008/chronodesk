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
	service := NewAgentNativeService(db, AgentNativeOptions{
		OutboxLockTTL:             time.Second,
		OutboxDeliveryTimeout:     500 * time.Millisecond,
		OutboxDeliveryConcurrency: 2,
	})
	createOutboxResilienceEvent(t, service, 6)

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
	if result.Delivered != 6 {
		t.Fatalf("受控并发批次结果 = %+v", result)
	}
	if maximum.Load() < 2 || maximum.Load() > 2 {
		t.Fatalf("最大并发数 = %d，期望 2", maximum.Load())
	}
}

func TestProcessOutboxBatchPersistsCancellationWithoutLockingDelivery(t *testing.T) {
	db := openAgentNativeTestDB(t)
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
