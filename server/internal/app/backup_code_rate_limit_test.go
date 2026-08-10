package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/config"
)

type backupCodeRateLimitRedis struct {
	mu     sync.Mutex
	counts map[string]int64
}

func (redis *backupCodeRateLimitRedis) Eval(
	_ context.Context,
	_ string,
	keys []string,
	args ...interface{},
) (interface{}, error) {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	if redis.counts == nil {
		redis.counts = make(map[string]int64)
	}
	limit, _ := strconv.ParseInt(fmt.Sprint(args[0]), 10, 64)
	windowMilliseconds, _ := strconv.ParseInt(fmt.Sprint(args[1]), 10, 64)
	key := keys[0]
	if redis.counts[key] >= limit {
		return []interface{}{int64(0), int64(0), windowMilliseconds}, nil
	}
	redis.counts[key]++
	return []interface{}{
		int64(1),
		limit - redis.counts[key],
		windowMilliseconds,
	}, nil
}

func TestBackupCodeRegenerationRateLimitReturns429AndIsolatesUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redis := &backupCodeRateLimitRedis{}
	rateLimit, err := newBackupCodeRegenerationRateLimit(
		redis,
		[]byte("0123456789abcdef-test-pepper"),
		config.RateLimitConfig{
			BackupCodeRequests: 2,
			BackupCodeWindow:   15 * time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		rawUserID, err := strconv.ParseUint(c.GetHeader("X-Test-User-ID"), 10, 64)
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set("user_id", uint(rawUserID))
		c.Next()
	})
	router.POST(
		"/api/auth/otp/backup-codes",
		rateLimit,
		func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		},
	)

	untrustedRouter := gin.New()
	untrustedRouter.POST(
		"/api/auth/otp/backup-codes",
		rateLimit,
		func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		},
	)
	untrustedResponse := httptest.NewRecorder()
	untrustedRouter.ServeHTTP(
		untrustedResponse,
		httptest.NewRequest(
			http.MethodPost,
			"/api/auth/otp/backup-codes",
			nil,
		),
	)
	if untrustedResponse.Code != http.StatusTooManyRequests {
		t.Fatalf(
			"missing authenticated user limiter status = %d, want fail-closed 429",
			untrustedResponse.Code,
		)
	}

	request := func(userID string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		httpRequest := httptest.NewRequest(
			http.MethodPost,
			"/api/auth/otp/backup-codes",
			nil,
		)
		httpRequest.Header.Set("X-Test-User-ID", userID)
		router.ServeHTTP(recorder, httpRequest)
		return recorder
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if response := request("42"); response.Code != http.StatusNoContent {
			t.Fatalf("user 42 attempt %d status = %d", attempt, response.Code)
		}
	}
	limited := request("42")
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("user 42 limited status = %d, want 429", limited.Code)
	}
	if limited.Header().Get("Retry-After") == "" {
		t.Fatal("dedicated limiter omitted Retry-After")
	}
	if response := request("84"); response.Code != http.StatusNoContent {
		t.Fatalf("different user status = %d, want isolation", response.Code)
	}
}

func TestBackupCodeRegenerationRateLimitUsesDedicatedBucket(t *testing.T) {
	first := backupCodeRegenerationRateLimitKey("user_42|/api/auth/otp/backup-codes")
	second := backupCodeRegenerationRateLimitKey("user_42|/api/auth/otp/backup-codes")
	if first == "" || first != second {
		t.Fatalf("dedicated key is not stable: %q %q", first, second)
	}
	if first == "user_42|/api/auth/otp/backup-codes" {
		t.Fatal("dedicated limiter collides with the authenticated route bucket")
	}
}
