package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
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

// testSlidingWindow is a deterministic in-process RateLimiter used only to
// exercise middleware header and concurrency behavior. Production composition
// uses RedisSlidingWindow exclusively.
type testSlidingWindow struct {
	mu      sync.Mutex
	windows map[string][]time.Time
	limit   int
	window  time.Duration
}

func newTestSlidingWindow(limit int, window time.Duration) *testSlidingWindow {
	if limit <= 0 {
		panic("test sliding window limit must be positive")
	}
	if window <= 0 {
		panic("test sliding window duration must be positive")
	}
	return &testSlidingWindow{
		windows: make(map[string][]time.Time),
		limit:   limit,
		window:  window,
	}
}

func (limiter *testSlidingWindow) Allow(key string) bool {
	return limiter.AllowN(key, 1)
}

func (limiter *testSlidingWindow) AllowN(key string, count int) bool {
	if count <= 0 {
		return false
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := time.Now()
	limiter.removeExpired(key, now)
	if len(limiter.windows[key])+count > limiter.limit {
		return false
	}
	for range count {
		limiter.windows[key] = append(limiter.windows[key], now)
	}
	return true
}

func (limiter *testSlidingWindow) Limit() int {
	return limiter.limit
}

func (limiter *testSlidingWindow) Remaining(key string) int {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.removeExpired(key, time.Now())
	return limiter.limit - len(limiter.windows[key])
}

func (limiter *testSlidingWindow) Reset(key string) time.Time {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := time.Now()
	limiter.removeExpired(key, now)
	if len(limiter.windows[key]) == 0 {
		return now
	}
	return limiter.windows[key][0].Add(limiter.window)
}

func (limiter *testSlidingWindow) removeExpired(key string, now time.Time) {
	cutoff := now.Add(-limiter.window)
	requests := limiter.windows[key]
	valid := requests[:0]
	for _, requestTime := range requests {
		if requestTime.After(cutoff) {
			valid = append(valid, requestTime)
		}
	}
	if len(valid) == 0 {
		delete(limiter.windows, key)
		return
	}
	limiter.windows[key] = valid
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

func TestAnonymousCredentialKeySeparatesIdentityAndPreservesRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if err := engine.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}

	var identityKeys, ipKeys, emails []string
	engine.POST("/api/auth/login", func(c *gin.Context) {
		context := NewGinHTTPContext(c)
		identityKeys = append(identityKeys, AnonymousCredentialKeyFunc(context))
		ipKeys = append(ipKeys, AnonymousIPRouteKeyFunc(context))
		var payload struct {
			Email string `json:"email"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			t.Fatalf("request body was not preserved: %v", err)
		}
		emails = append(emails, payload.Email)
		c.Status(204)
	})

	for _, email := range []string{
		" Employee@Example.COM ",
		"employee@example.com",
		"other@example.com",
	} {
		request := httptest.NewRequest(
			"POST",
			"/api/auth/login",
			strings.NewReader(`{"email":`+strconv.Quote(email)+`,"password":"secret"}`),
		)
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "192.0.2.10:4321"
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != 204 {
			t.Fatalf("login key probe status = %d", response.Code)
		}
	}

	if len(identityKeys) != 3 ||
		identityKeys[0] != identityKeys[1] ||
		identityKeys[0] == identityKeys[2] {
		t.Fatalf("identity keys = %#v", identityKeys)
	}
	if len(ipKeys) != 3 || ipKeys[0] != ipKeys[1] || ipKeys[1] != ipKeys[2] {
		t.Fatalf("IP route keys = %#v", ipKeys)
	}
	if len(emails) != 3 || emails[0] != " Employee@Example.COM " {
		t.Fatalf("preserved emails = %#v", emails)
	}
}

func TestSlidingWindowConcurrentInspectionIsRaceSafe(t *testing.T) {
	limiter := newTestSlidingWindow(5000, time.Minute)
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
		{name: "sliding limit", run: func() { newTestSlidingWindow(0, time.Minute) }},
		{name: "sliding window", run: func() { newTestSlidingWindow(1, 0) }},
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

func TestRateLimitEmitsOnlyRFC9333Headers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(WrapGinMiddleware(RateLimit(&RateLimitConfig{
		Limiter: newTestSlidingWindow(2, time.Minute),
		KeyFunc: func(HTTPContext) string { return "shared" },
		Headers: true,
	})))
	engine.GET("/tickets", func(c *gin.Context) {
		c.Status(204)
	})

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest("GET", "/tickets", nil))

	if got := response.Header().Get("RateLimit-Limit"); got != "2" {
		t.Fatalf("RateLimit-Limit = %q, want 2", got)
	}
	if got := response.Header().Get("RateLimit-Remaining"); got != "1" {
		t.Fatalf("RateLimit-Remaining = %q, want 1", got)
	}
	reset, err := strconv.ParseInt(response.Header().Get("RateLimit-Reset"), 10, 64)
	if err != nil || reset <= 0 {
		t.Fatalf("RateLimit-Reset must be a positive delta in seconds: value=%q err=%v",
			response.Header().Get("RateLimit-Reset"), err)
	}
	for _, removed := range []string{
		"X-RateLimit-Remaining",
		"X-RateLimit-Reset",
		"X-RateLimit-Reset-After",
	} {
		if got := response.Header().Get(removed); got != "" {
			t.Fatalf("removed compatibility header %s is still emitted: %q", removed, got)
		}
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
