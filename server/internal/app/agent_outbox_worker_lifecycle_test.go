package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type blockingAgentOutboxLifecycleRuntime struct {
	cleanupStarted chan struct{}
	releaseCleanup chan struct{}
	deliveryCalled chan struct{}
	cleanupOnce    sync.Once
	deliveryOnce   sync.Once
}

func (runtime *blockingAgentOutboxLifecycleRuntime) ExpireWebhookDeliveriesBatch(
	ctx context.Context,
	_ int,
) (services.WebhookOutboxCleanupResult, error) {
	runtime.cleanupOnce.Do(func() { close(runtime.cleanupStarted) })
	select {
	case <-ctx.Done():
		return services.WebhookOutboxCleanupResult{}, ctx.Err()
	case <-runtime.releaseCleanup:
		return services.WebhookOutboxCleanupResult{
			Expired: 1,
		}, errors.New("credential-shaped cleanup detail")
	}
}

func (runtime *blockingAgentOutboxLifecycleRuntime) ProcessOutboxBatch(
	context.Context,
	string,
	int,
	services.OutboxDeliverer,
) (services.OutboxBatchResult, error) {
	runtime.deliveryOnce.Do(func() { close(runtime.deliveryCalled) })
	return services.OutboxBatchResult{}, nil
}

func TestAgentOutboxCleanupCannotBlockNormalDeliveryLoop(t *testing.T) {
	runtime := &blockingAgentOutboxLifecycleRuntime{
		cleanupStarted: make(chan struct{}),
		releaseCleanup: make(chan struct{}),
		deliveryCalled: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var (
		releaseOnce sync.Once
		stopOnce    sync.Once
	)
	stop := func() {
		stopOnce.Do(func() {
			cancel()
			releaseOnce.Do(func() { close(runtime.releaseCleanup) })
		})
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("worker loops did not join on shutdown")
		}
	}
	t.Cleanup(stop)
	go func() {
		runAgentOutboxWorkerLoops(
			ctx,
			runtime,
			services.OutboxDeliverFunc(func(
				context.Context,
				*models.OutboxDelivery,
				services.CloudEventEnvelope,
			) error {
				return nil
			}),
			time.Hour,
			time.Hour,
		)
		close(done)
	}()
	select {
	case <-runtime.cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("cleanup loop did not start")
	}
	select {
	case <-runtime.deliveryCalled:
	case <-time.After(time.Second):
		t.Fatal("normal delivery waited for blocked cleanup")
	}
	stop()
}

func TestAgentOutboxCleanupMetricTextContainsOnlyFixedIntegers(t *testing.T) {
	message := agentOutboxCleanupMetricText(
		services.WebhookOutboxCleanupResult{
			Attempted:               5,
			Expired:                 1,
			OverlapCleared:          2,
			LegacySucceededShredded: 3,
			Malformed:               4,
		},
	)
	for _, value := range []string{
		"attempted=5",
		"expired=1",
		"malformed=4",
		"overlap_cleared=2",
		"legacy_succeeded_shredded=3",
	} {
		if !strings.Contains(message, value) {
			t.Fatalf("metric text %q is missing %q", message, value)
		}
	}
	if strings.Contains(message, "credential") ||
		strings.Contains(message, "http") {
		t.Fatalf("metric text contains dynamic detail: %q", message)
	}
}

func TestAgentOutboxSaturationReporterUsesFixedRateLimitedSignal(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 26, 9, 0, 0, 0, time.UTC)
	reporter := newAgentOutboxSaturationReporter(
		time.Minute,
		func() time.Time { return now },
	)
	result := services.OutboxBatchResult{
		Claimed:   1,
		Delivered: 1,
		Status:    services.OutboxBatchStatusPartialSaturation,
		SaturatedLanes: []services.OutboxDeliveryLane{
			services.OutboxDeliveryLaneWebhook,
			services.OutboxDeliveryLane("private-destination-id"),
		},
	}
	message, ok := reporter.observe(result)
	if !ok {
		t.Fatal("first saturation signal was suppressed")
	}
	for _, value := range []string{
		"status=partial_saturation",
		"global_saturated=false",
		"lanes=webhook,other",
		"claimed=1",
		"delivered=1",
		"failed=0",
	} {
		if !strings.Contains(message, value) {
			t.Fatalf("saturation metric %q is missing %q", message, value)
		}
	}
	if strings.Contains(message, "private-destination-id") {
		t.Fatalf("saturation metric leaked dynamic label: %q", message)
	}
	if _, ok := reporter.observe(result); ok {
		t.Fatal("stable saturation signal was not rate limited")
	}
	now = now.Add(time.Minute)
	if _, ok := reporter.observe(result); !ok {
		t.Fatal("saturation signal did not recover after rate limit interval")
	}
	alternate := services.OutboxBatchResult{
		Status:          services.OutboxBatchStatusSaturated,
		GlobalSaturated: true,
	}
	if _, ok := reporter.observe(alternate); !ok {
		t.Fatal("distinct fixed saturation signature was suppressed")
	}
	if _, ok := reporter.observe(services.OutboxBatchResult{
		Status: services.OutboxBatchStatusIdle,
	}); ok {
		t.Fatal("idle Outbox emitted a saturation signal")
	}
	if _, ok := reporter.observe(result); ok {
		t.Fatal("idle transition reset the persistent saturation rate limit")
	}
	if _, ok := reporter.observe(alternate); ok {
		t.Fatal("rate limit was not retained independently by signature")
	}
	now = now.Add(time.Minute)
	if _, ok := reporter.observe(result); !ok {
		t.Fatal("persistent saturation signal did not recover after interval")
	}
}
