package services_test

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/database"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

// This test is opt-in because it exercises the configured shared Redis. It is
// used by the release gate with CHRONODESK_REDIS_INTEGRATION=1.
func TestRedisAgentExecutionGuardIntegration(t *testing.T) {
	if os.Getenv("CHRONODESK_REDIS_INTEGRATION") != "1" {
		t.Skip("set CHRONODESK_REDIS_INTEGRATION=1 to test the configured Redis")
	}
	// go test executes this package from internal/services.
	_ = godotenv.Load("../../.env")
	client := configuredIntegrationRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Redis ping failed: %v", err)
	}

	guard, err := services.NewRedisAgentExecutionGuard(
		client,
		[]byte("chronodesk-release-gate-key-pepper"),
	)
	if err != nil {
		t.Fatal(err)
	}
	secondProcessGuard, err := services.NewRedisAgentExecutionGuard(
		client,
		[]byte("chronodesk-release-gate-key-pepper"),
	)
	if err != nil {
		t.Fatal(err)
	}
	randomID := integrationRandomID(t)
	principalID := "release-gate-" + randomID
	loopFingerprint := "loop-" + randomID
	t.Cleanup(func() {
		_ = client.Close()
	})

	renewRequest := services.AgentExecutionGuardRequest{
		SubjectID:        "release-gate-renew-" + randomID,
		RateLimit:        100,
		ConcurrencyLimit: 1,
		ConcurrencyTTL:   2 * time.Second,
	}
	renewed, err := guard.Acquire(ctx, renewRequest)
	if err != nil {
		t.Fatalf("renew probe acquire failed: %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if err := guard.Renew(
		ctx,
		services.AgentExecutionRenewRequest{
			Permit:         renewed,
			ConcurrencyTTL: 2 * time.Second,
		},
	); err != nil {
		t.Fatalf("renew probe failed: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := secondProcessGuard.Acquire(
		ctx,
		renewRequest,
	); !errors.Is(err, services.ErrConcurrencyLimit) {
		t.Fatalf(
			"renewed permit was not shared across guard instances: %v",
			err,
		)
	}
	if err := guard.Release(ctx, renewed); err != nil {
		t.Fatalf("renewed permit release failed: %v", err)
	}
	afterRenewRelease, err := secondProcessGuard.Acquire(
		ctx,
		renewRequest,
	)
	if err != nil {
		t.Fatalf(
			"acquire after renewed permit release failed: %v",
			err,
		)
	}
	if err := secondProcessGuard.Release(
		ctx,
		afterRenewRelease,
	); err != nil {
		t.Fatalf("post-renew release failed: %v", err)
	}

	mixedSubject := "release-gate-mixed-ttl-" + randomID
	longPermit, err := guard.Acquire(
		ctx,
		services.AgentExecutionGuardRequest{
			SubjectID:        mixedSubject,
			RateLimit:        100,
			ConcurrencyLimit: 2,
			ConcurrencyTTL:   10 * time.Second,
		},
	)
	if err != nil {
		t.Fatalf("mixed TTL long acquire failed: %v", err)
	}
	shortPermit, err := secondProcessGuard.Acquire(
		ctx,
		services.AgentExecutionGuardRequest{
			SubjectID:        mixedSubject,
			RateLimit:        100,
			ConcurrencyLimit: 2,
			ConcurrencyTTL:   time.Second,
		},
	)
	if err != nil {
		t.Fatalf("mixed TTL short acquire failed: %v", err)
	}
	time.Sleep(2100 * time.Millisecond)
	if _, err := secondProcessGuard.Acquire(
		ctx,
		services.AgentExecutionGuardRequest{
			SubjectID:        mixedSubject,
			RateLimit:        100,
			ConcurrencyLimit: 1,
			ConcurrencyTTL:   time.Second,
		},
	); !errors.Is(err, services.ErrConcurrencyLimit) {
		t.Fatalf(
			"short permit truncated long permit key TTL: %v",
			err,
		)
	}
	if err := guard.Release(ctx, longPermit); err != nil {
		t.Fatalf("mixed TTL long release failed: %v", err)
	}
	if err := secondProcessGuard.Release(
		ctx,
		shortPermit,
	); err != nil {
		t.Fatalf("mixed TTL short release failed: %v", err)
	}

	request := services.AgentExecutionGuardRequest{
		SubjectID:        principalID,
		RateLimit:        2,
		ConcurrencyLimit: 1,
		ConcurrencyTTL:   5 * time.Second,
	}
	first, err := guard.Acquire(ctx, request)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if _, err := secondProcessGuard.Acquire(ctx, request); !errors.Is(err, services.ErrConcurrencyLimit) {
		t.Fatalf("concurrent acquire error = %v", err)
	}
	if err := guard.Release(ctx, first); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	second, err := secondProcessGuard.Acquire(ctx, request)
	if err != nil {
		t.Fatalf("second admitted acquire failed: %v", err)
	}
	if err := guard.Release(ctx, second); err != nil {
		t.Fatalf("second release failed: %v", err)
	}
	if _, err := guard.Acquire(ctx, request); !errors.Is(err, services.ErrRateLimited) {
		t.Fatalf("rate-limited acquire error = %v", err)
	}

	loopRequest := services.AgentLoopGuardRequest{
		Fingerprint: loopFingerprint,
		Threshold:   2,
		Window:      time.Minute,
	}
	for attempt, processGuard := range []*services.RedisAgentExecutionGuard{guard, secondProcessGuard} {
		detected, err := processGuard.RecordLoop(ctx, loopRequest)
		if err != nil || detected {
			t.Fatalf("loop attempt %d: detected=%v err=%v", attempt+1, detected, err)
		}
	}
	detected, err := guard.RecordLoop(ctx, loopRequest)
	if err != nil || !detected {
		t.Fatalf("third loop attempt: detected=%v err=%v", detected, err)
	}
}

func integrationRandomID(t *testing.T) string {
	t.Helper()
	var value [16]byte
	if _, err := cryptorand.Read(value[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value[:])
}

func configuredIntegrationRedis(t *testing.T) database.RedisInterface {
	t.Helper()
	if os.Getenv("KV_REST_API_URL") != "" {
		client, err := database.NewHTTPRedisClient()
		if err != nil {
			t.Fatalf("create REST Redis client: %v", err)
		}
		return client
	}
	options, err := redis.ParseURL(os.Getenv("REDIS_URL"))
	if err != nil {
		t.Fatalf("parse REDIS_URL: %v", err)
	}
	return database.NewTCPRedisClient(redis.NewClient(options))
}
