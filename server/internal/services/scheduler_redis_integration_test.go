package services_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSchedulerRedisLeaseIntegration(t *testing.T) {
	if !redisIntegrationEnabled() {
		t.Skip("set CHRONODESK_REDIS_INTEGRATION=1 to test the configured Redis")
	}
	_ = godotenv.Load("../../.env")
	firstClient := configuredIntegrationRedis(t)
	secondClient := configuredIntegrationRedis(t)
	t.Cleanup(func() {
		_ = firstClient.Close()
		_ = secondClient.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for index, client := range []interface {
		Ping(context.Context) error
	}{firstClient, secondClient} {
		if err := client.Ping(ctx); err != nil {
			t.Fatalf("Redis client %d ping failed: %v", index+1, err)
		}
	}

	randomID := integrationRandomID(t)
	first := newIntegrationScheduler(t, firstClient, "first-"+randomID)
	second := newIntegrationScheduler(t, secondClient, "second-"+randomID)
	jobID := "redis-integration-" + randomID
	entered := make(chan struct{}, 1)
	var executions atomic.Int64
	handler := func(ctx context.Context) error {
		executions.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return context.Cause(ctx)
	}
	for _, scheduler := range []*services.SchedulerService{first, second} {
		if err := scheduler.AddJob(&services.ScheduledJob{
			ID:       jobID,
			Name:     "Redis 分布式租约集成测试",
			CronExpr: "@every 1s",
			Handler:  handler,
			IsActive: true,
			Timeout:  10 * time.Second,
		}); err != nil {
			t.Fatal(err)
		}
		if err := scheduler.Start(); err != nil {
			t.Fatal(err)
		}
	}

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no scheduler instance acquired the real Redis lease")
	}
	time.Sleep(200 * time.Millisecond)
	if got := executions.Load(); got != 1 {
		t.Fatalf("real Redis admitted %d concurrent handlers, want 1", got)
	}

	var shutdown sync.WaitGroup
	shutdown.Add(2)
	for _, scheduler := range []*services.SchedulerService{first, second} {
		go func(scheduler *services.SchedulerService) {
			defer shutdown.Done()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer shutdownCancel()
			if err := scheduler.Stop(shutdownCtx); err != nil {
				t.Errorf("Stop() error = %v", err)
			}
		}(scheduler)
	}
	shutdown.Wait()
	if got := executions.Load(); got != 1 {
		t.Fatalf("real Redis lease allowed an overlap during shutdown: %d", got)
	}
}

func redisIntegrationEnabled() bool {
	return os.Getenv("CHRONODESK_REDIS_INTEGRATION") == "1"
}

func newIntegrationScheduler(
	t *testing.T,
	redis services.SchedulerRedisExecutor,
	databaseName string,
) *services.SchedulerService {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", databaseName)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := services.NewSchedulerService(db, redis)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"sla_check",
		"automation_rules",
		"cleanup_expired_data",
		"update_statistics",
	} {
		if err := scheduler.RemoveJob(id); err != nil {
			t.Fatal(err)
		}
	}
	return scheduler
}
