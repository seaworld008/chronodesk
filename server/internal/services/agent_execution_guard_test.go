package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptedRedisExecution struct {
	mu        sync.Mutex
	responses []interface{}
	errors    []error
	calls     []redisExecutionCall
}

type redisExecutionCall struct {
	script string
	keys   []string
	args   []interface{}
}

func (executor *scriptedRedisExecution) Eval(
	_ context.Context,
	script string,
	keys []string,
	args ...interface{},
) (interface{}, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.calls = append(executor.calls, redisExecutionCall{
		script: script,
		keys:   append([]string(nil), keys...),
		args:   append([]interface{}(nil), args...),
	})
	index := len(executor.calls) - 1
	var result interface{} = int64(0)
	if index < len(executor.responses) {
		result = executor.responses[index]
	}
	if index < len(executor.errors) && executor.errors[index] != nil {
		return nil, executor.errors[index]
	}
	return result, nil
}

func TestRedisAgentExecutionGuardUsesOpaqueClusterSlotAndAtomicRelease(t *testing.T) {
	executor := &scriptedRedisExecution{
		responses: []interface{}{float64(0), float64(1)},
	}
	guard, err := NewRedisAgentExecutionGuard(
		executor,
		[]byte("0123456789abcdef-test-pepper"),
	)
	if err != nil {
		t.Fatal(err)
	}
	principalID := "principal-sensitive-identity"
	permit, err := guard.Acquire(context.Background(), AgentExecutionGuardRequest{
		PrincipalID:      principalID,
		RateLimit:        20,
		ConcurrencyLimit: 3,
		ConcurrencyTTL:   90 * time.Second,
	})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := guard.Release(context.Background(), permit); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	executor.mu.Lock()
	defer executor.mu.Unlock()
	if len(executor.calls) != 2 {
		t.Fatalf("Redis calls = %d, want 2", len(executor.calls))
	}
	acquire := executor.calls[0]
	if len(acquire.keys) != 2 {
		t.Fatalf("acquire keys = %#v", acquire.keys)
	}
	if strings.Contains(strings.Join(acquire.keys, "|"), principalID) {
		t.Fatalf("Redis keys expose principal: %#v", acquire.keys)
	}
	firstTag := redisHashTag(acquire.keys[0])
	secondTag := redisHashTag(acquire.keys[1])
	if firstTag == "" || firstTag != secondTag {
		t.Fatalf("rate/concurrency keys do not share one Redis slot: %#v", acquire.keys)
	}
	if !strings.Contains(acquire.script, `redis.call("TIME")`) ||
		!strings.Contains(acquire.script, "ZREMRANGEBYSCORE") ||
		!strings.Contains(acquire.script, "PEXPIRE") {
		t.Fatal("acquire script is missing atomic Redis-time/TTL controls")
	}
	if got := acquire.args[3]; got != int64((90 * time.Second).Milliseconds()) {
		t.Fatalf("concurrency TTL argument = %#v", got)
	}
	release := executor.calls[1]
	if len(release.keys) != 1 || release.keys[0] != acquire.keys[1] {
		t.Fatalf("release key = %#v, want concurrency key", release.keys)
	}
	if !strings.Contains(release.script, "ZREM") {
		t.Fatal("release must remove only its opaque lease token atomically")
	}
}

func TestRedisAgentExecutionGuardMapsLimitsAndFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		result interface{}
		err    error
		want   error
	}{
		{name: "rate", result: int64(1), want: ErrRateLimited},
		{name: "concurrency", result: int64(2), want: ErrConcurrencyLimit},
		{name: "outage", err: errors.New("network down"), want: ErrExecutionGuardUnavailable},
		{name: "malformed", result: "not-a-number", want: ErrExecutionGuardUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &scriptedRedisExecution{
				responses: []interface{}{test.result},
				errors:    []error{test.err},
			}
			guard, err := NewRedisAgentExecutionGuard(
				executor,
				[]byte("0123456789abcdef-test-pepper"),
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = guard.Acquire(context.Background(), AgentExecutionGuardRequest{
				PrincipalID:      "principal",
				RateLimit:        10,
				ConcurrencyLimit: 1,
				ConcurrencyTTL:   time.Minute,
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("Acquire() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRedisAgentLoopWindowIsSharedAcrossGuardInstances(t *testing.T) {
	executor := &scriptedRedisExecution{
		responses: []interface{}{int64(0), int64(0), int64(1)},
	}
	first, err := NewRedisAgentExecutionGuard(
		executor,
		[]byte("0123456789abcdef-test-pepper"),
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRedisAgentExecutionGuard(
		executor,
		[]byte("0123456789abcdef-test-pepper"),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := AgentLoopGuardRequest{
		Fingerprint: "opaque-policy-command-fingerprint",
		Threshold:   2,
		Window:      time.Minute,
	}
	for attempt, guard := range []*RedisAgentExecutionGuard{first, second} {
		detected, err := guard.RecordLoop(context.Background(), request)
		if err != nil || detected {
			t.Fatalf("attempt %d: detected=%v err=%v", attempt+1, detected, err)
		}
	}
	detected, err := first.RecordLoop(context.Background(), request)
	if err != nil || !detected {
		t.Fatalf("shared third attempt: detected=%v err=%v", detected, err)
	}

	executor.mu.Lock()
	defer executor.mu.Unlock()
	if len(executor.calls) != 3 {
		t.Fatalf("Redis loop calls = %d", len(executor.calls))
	}
	key := executor.calls[0].keys[0]
	for _, call := range executor.calls {
		if len(call.keys) != 1 || call.keys[0] != key {
			t.Fatalf("loop guards did not share one authority key: %#v", executor.calls)
		}
		if strings.Contains(call.keys[0], request.Fingerprint) {
			t.Fatalf("loop key exposes request fingerprint: %q", call.keys[0])
		}
	}
}

func TestRequiredDistributedExecutionGuardRejectsLocalFallback(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedCompatibilityUser(t, db, "distributed-required")
	service := NewAgentNativeService(db, AgentNativeOptions{
		ExecutionGuard:                   NewInMemoryAgentExecutionGuardForTesting(),
		RequireDistributedExecutionGuard: true,
	})
	principal := createNativePrincipal(
		t,
		service,
		user.ID,
		"distributed-required-agent",
		modelsScopeTicketsReadForGuardTest,
	)
	if err := service.ValidateExecutionGuardConfiguration(); !errors.Is(err, ErrExecutionGuardUnavailable) {
		t.Fatalf("startup validation error = %v", err)
	}
	if _, err := service.AcquireAgentExecution(context.Background(), principal.ID); !errors.Is(err, ErrExecutionGuardUnavailable) {
		t.Fatalf("AcquireAgentExecution() error = %v", err)
	}
}

func TestPolicyWriteFailsClosedAndAuditsSharedGuardOutage(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedCompatibilityUser(t, db, "guard-outage")
	executor := &scriptedRedisExecution{errors: []error{errors.New("Redis unavailable")}}
	guard, err := NewRedisAgentExecutionGuard(
		executor,
		[]byte("0123456789abcdef-test-pepper"),
	)
	if err != nil {
		t.Fatal(err)
	}
	service := NewAgentNativeService(db, AgentNativeOptions{
		ExecutionGuard:                   guard,
		RequireDistributedExecutionGuard: true,
	})
	principal := createNativePrincipal(
		t,
		service,
		user.ID,
		"guard-outage-agent",
		modelsScopeTicketsReadForGuardTest,
	)
	decision, err := service.CheckAction(context.Background(), PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		Scope:              modelsScopeTicketsReadForGuardTest,
		Action:             "ticket.read-side-effect-test",
		ResourceType:       "ticket",
		ResourceID:         "42",
		IsWrite:            true,
		RequestDigest:      "request-digest",
		SourceProtocol:     "test",
	})
	if !errors.Is(err, ErrExecutionGuardUnavailable) {
		t.Fatalf("CheckAction() error = %v", err)
	}
	if decision == nil || decision.Allowed ||
		decision.ReasonCode != "execution_guard_unavailable" {
		t.Fatalf("outage decision = %+v", decision)
	}
	if AgentNativeErrorCode(err) != "execution_guard_unavailable" {
		t.Fatalf("AgentNativeErrorCode() = %q", AgentNativeErrorCode(err))
	}
}

func TestInMemoryAgentExecutionLeaseExpiresAfterCrashWindow(t *testing.T) {
	guard := NewInMemoryAgentExecutionGuardForTesting()
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	request := AgentExecutionGuardRequest{
		PrincipalID:       "principal",
		RateLimit:         100,
		ConcurrencyLimit:  1,
		ConcurrencyTTL:    time.Minute,
		ObservedAtForTest: now,
	}
	if _, err := guard.Acquire(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Acquire(context.Background(), request); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("second Acquire() error = %v", err)
	}
	request.ObservedAtForTest = now.Add(time.Minute + time.Millisecond)
	if _, err := guard.Acquire(context.Background(), request); err != nil {
		t.Fatalf("expired crash lease should be reclaimed: %v", err)
	}
}

func redisHashTag(key string) string {
	start := strings.IndexByte(key, '{')
	end := strings.IndexByte(key, '}')
	if start < 0 || end <= start {
		return ""
	}
	return key[start+1 : end]
}

// Keep the test independent from accidental scope-string spelling changes.
const modelsScopeTicketsReadForGuardTest = "tickets:read"
