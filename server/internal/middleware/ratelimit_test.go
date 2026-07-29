package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type testRedisRateExecutor struct {
	mu     sync.Mutex
	result interface{}
	err    error
	keys   []string
	args   []interface{}
	script string
}

func (executor *testRedisRateExecutor) Eval(
	_ context.Context,
	script string,
	keys []string,
	args ...interface{},
) (interface{}, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.script = script
	executor.keys = append([]string(nil), keys...)
	executor.args = append([]interface{}(nil), args...)
	return executor.result, executor.err
}

func TestRateLimitKeyUsesTrustedProxyAwareClientIPAndRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if err := engine.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}

	var ipKey, routeKey string
	engine.GET("/tickets", func(c *gin.Context) {
		ctx := NewGinHTTPContext(c)
		ipKey = IPKeyFunc(ctx)
		routeKey = RouteKeyFunc(ctx)
		c.Status(204)
	})

	request := httptest.NewRequest("GET", "/tickets", nil)
	request.RemoteAddr = "192.0.2.10:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.99")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if ipKey != "192.0.2.10" {
		t.Fatalf("IP key trusted spoofed forwarding header: %q", ipKey)
	}
	if routeKey != "192.0.2.10|/tickets" {
		t.Fatalf("unexpected route key: %q", routeKey)
	}
}

func TestUserRouteKeySeparatesUsersAndUsesRoutePattern(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var keys []string
	engine.GET("/tickets/:id", func(c *gin.Context) {
		c.Set("user_id", uint(42))
		keys = append(keys, UserRouteKeyFunc(NewGinHTTPContext(c)))
		c.Status(204)
	})
	for _, path := range []string{"/tickets/1", "/tickets/999"} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
	}
	if len(keys) != 2 || keys[0] != "user_42|/tickets/:id" || keys[1] != keys[0] {
		t.Fatalf("user route keys = %#v", keys)
	}
}

func TestSlidingWindowConcurrentInspectionIsRaceSafe(t *testing.T) {
	limiter := NewSlidingWindow(5000, time.Minute)
	const workers = 32
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 100; iteration++ {
				limiter.Allow("shared")
				_ = limiter.Remaining("shared")
				_ = limiter.Reset("shared")
			}
		}()
	}
	wait.Wait()
}

func TestInvalidRateLimiterConstructionFailsFast(t *testing.T) {
	tests := []struct {
		name string
		run  func()
	}{
		{name: "token capacity", run: func() { NewTokenBucket(0, 1, time.Minute) }},
		{name: "token refill", run: func() { NewTokenBucket(1, 0, time.Minute) }},
		{name: "sliding limit", run: func() { NewSlidingWindow(0, time.Minute) }},
		{name: "sliding window", run: func() { NewSlidingWindow(1, 0) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected invalid limiter configuration to panic")
				}
			}()
			test.run()
		})
	}
}

func TestRedisSlidingWindowUsesOpaqueAtomicKeyAndMetadata(t *testing.T) {
	executor := &testRedisRateExecutor{
		result: []interface{}{float64(1), float64(8), float64(1500)},
	}
	limiter, err := NewRedisSlidingWindow(
		executor,
		[]byte("0123456789abcdef-test-pepper"),
		10,
		time.Minute,
		time.Second,
	)
	if err != nil {
		t.Fatalf("NewRedisSlidingWindow() error = %v", err)
	}
	rawKey := "203.0.113.44|/api/auth/login"
	if !limiter.AllowN(rawKey, 2) {
		t.Fatal("expected Redis admission")
	}
	if remaining := limiter.Remaining(rawKey); remaining != 8 {
		t.Fatalf("Remaining() = %d, want 8", remaining)
	}
	resetAfter := time.Until(limiter.Reset(rawKey))
	if resetAfter < time.Second || resetAfter > 2*time.Second {
		t.Fatalf("Reset() duration = %s, want about 1.5s", resetAfter)
	}

	executor.mu.Lock()
	defer executor.mu.Unlock()
	if len(executor.keys) != 1 ||
		!strings.HasPrefix(executor.keys[0], redisHTTPRateLimitKeyPrefix+":{") {
		t.Fatalf("Redis keys = %#v", executor.keys)
	}
	if strings.Contains(executor.keys[0], "203.0.113.44") ||
		strings.Contains(executor.keys[0], "/api/auth/login") {
		t.Fatalf("Redis key exposes identity or route: %q", executor.keys[0])
	}
	if !strings.Contains(executor.script, `redis.call("TIME")`) ||
		!strings.Contains(executor.script, "ZADD") {
		t.Fatal("rate-limit admission must use one Redis-time Lua transition")
	}
}

func TestRedisSlidingWindowFailsClosedWhenSharedAuthorityFails(t *testing.T) {
	executor := &testRedisRateExecutor{err: errors.New("Redis unavailable")}
	limiter, err := NewRedisSlidingWindow(
		executor,
		[]byte("0123456789abcdef-test-pepper"),
		3,
		time.Minute,
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if limiter.Allow("198.51.100.9|/api/auth/login") {
		t.Fatal("shared limiter outage must fail closed")
	}
	if limiter.Remaining("198.51.100.9|/api/auth/login") != 0 {
		t.Fatal("failed-closed limiter must advertise zero remaining")
	}
}

func TestRedisSlidingWindowFailsClosedOnOutOfRangeMetadata(t *testing.T) {
	tests := []struct {
		name   string
		result interface{}
	}{
		{
			name:   "remaining exceeds configured limit",
			result: []interface{}{float64(1), float64(4), float64(0)},
		},
		{
			name:   "negative reset",
			result: []interface{}{float64(1), float64(2), float64(-1)},
		},
		{
			name:   "native int overflow",
			result: []interface{}{"1", "9223372036854775808", "0"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &testRedisRateExecutor{result: test.result}
			limiter, err := NewRedisSlidingWindow(
				executor,
				[]byte("0123456789abcdef-test-pepper"),
				3,
				time.Minute,
				time.Second,
			)
			if err != nil {
				t.Fatal(err)
			}
			if limiter.Allow("198.51.100.9|/api/auth/login") {
				t.Fatal("malformed Redis metadata must fail closed")
			}
			if limiter.Remaining("198.51.100.9|/api/auth/login") != 0 {
				t.Fatal("malformed Redis metadata must advertise zero remaining")
			}
		})
	}
}

func TestInfrastructureRoutesBypassOnlyGenericLimiter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var skipped = make(map[string]bool)
	engine.Any("/*path", func(c *gin.Context) {
		skipped[c.Request.URL.Path] = InfrastructureRouteRateLimitSkip(NewGinHTTPContext(c))
		c.Status(204)
	})

	tests := map[string]bool{
		"/health":                              true,
		"/.well-known/agent-card.json":         true,
		"/assets/application.js":               true,
		"/uploads/avatars/42/opaque-image.png": true,
		"/api/auth/login":                      false,
		"/api/tickets":                         false,
		"/mcp":                                 false,
		"/a2a/v1":                              false,
	}
	for path := range tests {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
	}
	for path, want := range tests {
		if skipped[path] != want {
			t.Fatalf("InfrastructureRouteRateLimitSkip(%q) = %v, want %v", path, skipped[path], want)
		}
	}

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest("OPTIONS", "/api/auth/login", nil))
	if !skipped["/api/auth/login"] {
		t.Fatal("browser preflight must bypass generic rate limiting")
	}
}
