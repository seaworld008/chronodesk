package services

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSchedulerRedisEntry struct {
	token     string
	expiresAt time.Time
}

type fakeSchedulerRedis struct {
	mu             sync.Mutex
	entries        map[string]fakeSchedulerRedisEntry
	failAll        bool
	failRenewAfter int
	renewCount     int
	releaseCount   int
}

func newFakeSchedulerRedis() *fakeSchedulerRedis {
	return &fakeSchedulerRedis{entries: make(map[string]fakeSchedulerRedisEntry)}
}

func (redis *fakeSchedulerRedis) Eval(
	ctx context.Context,
	script string,
	keys []string,
	args ...interface{},
) (interface{}, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	redis.mu.Lock()
	defer redis.mu.Unlock()
	if redis.failAll {
		return nil, errors.New("Redis unavailable")
	}
	if len(keys) != 1 {
		return nil, fmt.Errorf("unexpected key count %d", len(keys))
	}
	now := time.Now()
	key := keys[0]
	entry, exists := redis.entries[key]
	if exists && !entry.expiresAt.After(now) {
		delete(redis.entries, key)
		exists = false
	}

	switch script {
	case redisAcquireSchedulerLeaseScript:
		if exists {
			return int64(0), nil
		}
		ttl, err := fakeSchedulerRedisInt64(args[1])
		if err != nil {
			return nil, err
		}
		redis.entries[key] = fakeSchedulerRedisEntry{
			token:     fmt.Sprint(args[0]),
			expiresAt: now.Add(time.Duration(ttl) * time.Millisecond),
		}
		return int64(1), nil
	case redisRenewSchedulerLeaseScript:
		redis.renewCount++
		if redis.failRenewAfter > 0 && redis.renewCount >= redis.failRenewAfter {
			return nil, errors.New("renew failed")
		}
		if !exists || entry.token != fmt.Sprint(args[0]) {
			return int64(0), nil
		}
		ttl, err := fakeSchedulerRedisInt64(args[1])
		if err != nil {
			return nil, err
		}
		entry.expiresAt = now.Add(time.Duration(ttl) * time.Millisecond)
		redis.entries[key] = entry
		return int64(1), nil
	case redisReleaseSchedulerLeaseScript:
		redis.releaseCount++
		if !exists || entry.token != fmt.Sprint(args[0]) {
			return int64(0), nil
		}
		delete(redis.entries, key)
		return int64(1), nil
	default:
		return nil, errors.New("unexpected Redis script")
	}
}

func fakeSchedulerRedisInt64(value interface{}) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return strconv.ParseInt(fmt.Sprint(value), 10, 64)
	}
}

func (redis *fakeSchedulerRedis) replaceOwner(key, token string, ttl time.Duration) {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	redis.entries[key] = fakeSchedulerRedisEntry{
		token:     token,
		expiresAt: time.Now().Add(ttl),
	}
}

func (redis *fakeSchedulerRedis) counts() (renewals, releases int) {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	return redis.renewCount, redis.releaseCount
}

func newSchedulerForTest(
	t *testing.T,
	redis SchedulerRedisExecutor,
	leaseTTL time.Duration,
	redisTimeout time.Duration,
) *SchedulerService {
	t.Helper()
	scheduler, err := newSchedulerService(
		openTestDB(t),
		redis,
		schedulerServiceOptions{
			leaseTTL:              leaseTTL,
			redisOperationTimeout: redisTimeout,
		},
	)
	if err != nil {
		t.Fatalf("newSchedulerService() error = %v", err)
	}
	return scheduler
}

func addSchedulerTestJob(
	t *testing.T,
	scheduler *SchedulerService,
	id string,
	handler func(context.Context) error,
) {
	t.Helper()
	if err := scheduler.AddJob(&ScheduledJob{
		ID:       id,
		Name:     id,
		CronExpr: "0 0 0 1 1 *",
		Handler:  handler,
		IsActive: true,
		Timeout:  time.Second,
	}); err != nil {
		t.Fatalf("AddJob() error = %v", err)
	}
}

func TestNewSchedulerServiceRequiresDistributedRedis(t *testing.T) {
	_, err := NewSchedulerService(openTestDB(t), nil)
	if err == nil {
		t.Fatal("NewSchedulerService() accepted a nil Redis client")
	}
}

func TestSchedulerRejectsInvalidConfiguration(t *testing.T) {
	db := openTestDB(t)
	redis := newFakeSchedulerRedis()
	tests := []struct {
		name    string
		dbNil   bool
		redis   SchedulerRedisExecutor
		options schedulerServiceOptions
	}{
		{
			name:    "missing database",
			dbNil:   true,
			redis:   redis,
			options: schedulerServiceOptions{leaseTTL: time.Second, redisOperationTimeout: time.Millisecond},
		},
		{
			name:    "missing Redis",
			redis:   nil,
			options: schedulerServiceOptions{leaseTTL: time.Second, redisOperationTimeout: time.Millisecond},
		},
		{
			name:    "zero lease TTL",
			redis:   redis,
			options: schedulerServiceOptions{redisOperationTimeout: time.Millisecond},
		},
		{
			name:    "zero Redis timeout",
			redis:   redis,
			options: schedulerServiceOptions{leaseTTL: time.Second},
		},
		{
			name:    "unsafe renewal window",
			redis:   redis,
			options: schedulerServiceOptions{leaseTTL: 20 * time.Millisecond, redisOperationTimeout: 10 * time.Millisecond},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testDB := db
			if test.dbNil {
				testDB = nil
			}
			if _, err := newSchedulerService(testDB, test.redis, test.options); err == nil {
				t.Fatal("newSchedulerService() accepted invalid configuration")
			}
		})
	}
}

func TestSchedulerUsesStrictSixFieldCronParser(t *testing.T) {
	scheduler := newSchedulerForTest(
		t,
		newFakeSchedulerRedis(),
		120*time.Millisecond,
		20*time.Millisecond,
	)
	if err := scheduler.AddJob(&ScheduledJob{
		ID:       "full-expression",
		Name:     "full-expression",
		CronExpr: "17 3 4 5 6 *",
		Handler:  func(context.Context) error { return nil },
		IsActive: true,
	}); err != nil {
		t.Fatalf("valid cron expression rejected: %v", err)
	}
	if err := scheduler.AddJob(&ScheduledJob{
		ID:       "invalid-expression",
		Name:     "invalid-expression",
		CronExpr: "not-a-cron",
		Handler:  func(context.Context) error { return nil },
		IsActive: true,
	}); err == nil {
		t.Fatal("invalid cron expression was silently accepted")
	}
}

func TestSchedulerPreventsSameProcessOverlap(t *testing.T) {
	redis := newFakeSchedulerRedis()
	scheduler := newSchedulerForTest(t, redis, 120*time.Millisecond, 20*time.Millisecond)
	entered := make(chan struct{})
	release := make(chan struct{})
	addSchedulerTestJob(t, scheduler, "local-overlap", func(context.Context) error {
		close(entered)
		<-release
		return nil
	})

	var executions sync.WaitGroup
	executions.Add(1)
	go func() {
		defer executions.Done()
		scheduler.executeJob("local-overlap")
	}()
	<-entered
	scheduler.executeJob("local-overlap")

	status := scheduler.GetJobStatus()["local-overlap"]
	if status.SkipCount != 1 || !status.IsRunning {
		t.Fatalf("overlap status = %+v", status)
	}
	close(release)
	executions.Wait()
	status = scheduler.GetJobStatus()["local-overlap"]
	if status.RunCount != 1 || status.ErrorCount != 0 || status.IsRunning {
		t.Fatalf("final status = %+v", status)
	}
}

func TestSchedulerLeasePreventsCrossInstanceOverlap(t *testing.T) {
	redis := newFakeSchedulerRedis()
	first := newSchedulerForTest(t, redis, 120*time.Millisecond, 20*time.Millisecond)
	second := newSchedulerForTest(t, redis, 120*time.Millisecond, 20*time.Millisecond)
	entered := make(chan struct{})
	release := make(chan struct{})
	var runCount atomic.Int64
	handler := func(context.Context) error {
		runCount.Add(1)
		close(entered)
		<-release
		return nil
	}
	addSchedulerTestJob(t, first, "distributed-overlap", handler)
	addSchedulerTestJob(t, second, "distributed-overlap", handler)

	var firstExecution sync.WaitGroup
	firstExecution.Add(1)
	go func() {
		defer firstExecution.Done()
		first.executeJob("distributed-overlap")
	}()
	<-entered
	second.executeJob("distributed-overlap")
	if got := runCount.Load(); got != 1 {
		t.Fatalf("handler executions = %d, want 1", got)
	}
	if status := second.GetJobStatus()["distributed-overlap"]; status.SkipCount != 1 {
		t.Fatalf("second scheduler status = %+v", status)
	}
	close(release)
	firstExecution.Wait()
}

func TestSchedulerRenewsLeaseForLongRunningJob(t *testing.T) {
	redis := newFakeSchedulerRedis()
	const leaseTTL = 120 * time.Millisecond
	first := newSchedulerForTest(t, redis, leaseTTL, 20*time.Millisecond)
	second := newSchedulerForTest(t, redis, leaseTTL, 20*time.Millisecond)
	entered := make(chan struct{})
	release := make(chan struct{})
	var runCount atomic.Int64
	handler := func(context.Context) error {
		runCount.Add(1)
		close(entered)
		<-release
		return nil
	}
	addSchedulerTestJob(t, first, "renewed-lease", handler)
	addSchedulerTestJob(t, second, "renewed-lease", handler)

	var firstExecution sync.WaitGroup
	firstExecution.Add(1)
	go func() {
		defer firstExecution.Done()
		first.executeJob("renewed-lease")
	}()
	<-entered
	time.Sleep(leaseTTL * 2)
	second.executeJob("renewed-lease")
	close(release)
	firstExecution.Wait()

	renewals, _ := redis.counts()
	if renewals < 2 {
		t.Fatalf("lease renewals = %d, want at least 2", renewals)
	}
	if got := runCount.Load(); got != 1 {
		t.Fatalf("handler executions = %d, want 1", got)
	}
}

func TestSchedulerCancelsJobWhenLeaseRenewalFails(t *testing.T) {
	redis := newFakeSchedulerRedis()
	redis.failRenewAfter = 1
	scheduler := newSchedulerForTest(t, redis, 120*time.Millisecond, 20*time.Millisecond)
	addSchedulerTestJob(t, scheduler, "renewal-failure", func(ctx context.Context) error {
		<-ctx.Done()
		return context.Cause(ctx)
	})

	scheduler.executeJob("renewal-failure")
	status := scheduler.GetJobStatus()["renewal-failure"]
	if status.RunCount != 1 || status.ErrorCount != 1 || status.LeaseErrorCount != 1 {
		t.Fatalf("renewal failure status = %+v", status)
	}
	if status.LastError == "" {
		t.Fatal("renewal failure did not record an error")
	}
}

func TestSchedulerFailsClosedWhenRedisUnavailable(t *testing.T) {
	redis := newFakeSchedulerRedis()
	redis.failAll = true
	scheduler := newSchedulerForTest(t, redis, 120*time.Millisecond, 20*time.Millisecond)
	var handlerCalled atomic.Bool
	addSchedulerTestJob(t, scheduler, "redis-failure", func(context.Context) error {
		handlerCalled.Store(true)
		return nil
	})

	scheduler.executeJob("redis-failure")
	if handlerCalled.Load() {
		t.Fatal("handler ran without Redis coordination")
	}
	status := scheduler.GetJobStatus()["redis-failure"]
	if status.RunCount != 0 || status.ErrorCount != 1 || status.LeaseErrorCount != 1 {
		t.Fatalf("Redis failure status = %+v", status)
	}
}

func TestSchedulerRecoversPanicAndReleasesLease(t *testing.T) {
	redis := newFakeSchedulerRedis()
	scheduler := newSchedulerForTest(t, redis, 120*time.Millisecond, 20*time.Millisecond)
	addSchedulerTestJob(t, scheduler, "panic", func(context.Context) error {
		panic("test panic")
	})

	scheduler.executeJob("panic")
	status := scheduler.GetJobStatus()["panic"]
	if status.RunCount != 1 || status.ErrorCount != 1 || status.IsRunning {
		t.Fatalf("panic status = %+v", status)
	}
	_, releases := redis.counts()
	if releases != 1 {
		t.Fatalf("lease releases = %d, want 1", releases)
	}
	redis.mu.Lock()
	remaining := len(redis.entries)
	redis.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("leases remaining after panic = %d", remaining)
	}
}

func TestSchedulerLeaseReleaseChecksOwnerToken(t *testing.T) {
	redis := newFakeSchedulerRedis()
	manager, err := newSchedulerLeaseManager(redis, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.acquire(context.Background(), "owner-check", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	redis.replaceOwner(lease.key, "different-owner", time.Second)
	if err := manager.release(lease); !errors.Is(err, ErrSchedulerLeaseLost) {
		t.Fatalf("release() error = %v, want ErrSchedulerLeaseLost", err)
	}
	redis.mu.Lock()
	owner := redis.entries[lease.key].token
	redis.mu.Unlock()
	if owner != "different-owner" {
		t.Fatalf("foreign lease owner was deleted: %q", owner)
	}
}

func TestSchedulerStopCancelsAndWaitsForRunningJobs(t *testing.T) {
	redis := newFakeSchedulerRedis()
	scheduler := newSchedulerForTest(t, redis, 120*time.Millisecond, 20*time.Millisecond)
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
	entered := make(chan struct{})
	if err := scheduler.AddJob(&ScheduledJob{
		ID:       "graceful-stop",
		Name:     "graceful-stop",
		CronExpr: "@every 1s",
		Handler: func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			return context.Cause(ctx)
		},
		IsActive: true,
		Timeout:  5 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled job did not start")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := scheduler.Stop(shutdownCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if scheduler.IsRunning() {
		t.Fatal("scheduler still reports running after Stop")
	}
	if err := scheduler.Start(); !errors.Is(err, ErrSchedulerStopped) {
		t.Fatalf("restart error = %v, want ErrSchedulerStopped", err)
	}
	_, releases := redis.counts()
	if releases == 0 {
		t.Fatal("running job lease was not released during shutdown")
	}
}
