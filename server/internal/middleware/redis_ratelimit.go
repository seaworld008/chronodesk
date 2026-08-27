package middleware

import (
	"bytes"
	"context"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/safeconv"
)

const redisHTTPRateLimitKeyPrefix = "chronodesk:http-rate:v1"

// RedisRateLimitScriptExecutor is implemented by both ChronoDesk Redis
// transports: go-redis over RESP and Upstash over HTTPS REST.
type RedisRateLimitScriptExecutor interface {
	Eval(
		ctx context.Context,
		script string,
		keys []string,
		args ...interface{},
	) (interface{}, error)
}

type redisRateLimitSnapshot struct {
	remaining int
	reset     time.Time
	touchedAt time.Time
}

// RedisSlidingWindow is a cross-process sliding-window limiter. Redis server
// time and one Lua script make admission atomic, while HMACed keys keep client
// IPs, user IDs and route values out of the Redis namespace.
type RedisSlidingWindow struct {
	client    RedisRateLimitScriptExecutor
	keyPepper []byte
	limit     int
	window    time.Duration
	timeout   time.Duration

	mu        sync.Mutex
	snapshots map[string]redisRateLimitSnapshot
}

func NewRedisSlidingWindow(
	client RedisRateLimitScriptExecutor,
	keyPepper []byte,
	limit int,
	window time.Duration,
	timeout time.Duration,
) (*RedisSlidingWindow, error) {
	if client == nil {
		return nil, errors.New("redis rate limiter requires a client")
	}
	if len(keyPepper) < 16 {
		return nil, errors.New("redis rate limiter key pepper must be at least 16 bytes")
	}
	if limit <= 0 || window <= 0 {
		return nil, errors.New("redis rate limiter requires positive limit and window")
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &RedisSlidingWindow{
		client:    client,
		keyPepper: append([]byte(nil), keyPepper...),
		limit:     limit,
		window:    window,
		timeout:   timeout,
		snapshots: make(map[string]redisRateLimitSnapshot),
	}, nil
}

const redisSlidingWindowScript = `
local server_time = redis.call("TIME")
local now_ms = (tonumber(server_time[1]) * 1000) + math.floor(tonumber(server_time[2]) / 1000)
local limit = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local token = ARGV[4]

redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", now_ms - window_ms)
local count = redis.call("ZCARD", KEYS[1])
local allowed = 1
if count + cost > limit then
  allowed = 0
else
  for index = 1, cost do
    redis.call("ZADD", KEYS[1], now_ms, token .. ":" .. index)
  end
  count = count + cost
  redis.call("PEXPIRE", KEYS[1], window_ms + 1000)
end

local reset_after_ms = 0
local earliest = redis.call("ZRANGE", KEYS[1], 0, 0, "WITHSCORES")
if #earliest == 2 then
  reset_after_ms = math.max(0, tonumber(earliest[2]) + window_ms - now_ms)
end
return {allowed, math.max(0, limit - count), reset_after_ms}
`

func (limiter *RedisSlidingWindow) Allow(key string) bool {
	return limiter.AllowN(key, 1)
}

func (limiter *RedisSlidingWindow) AllowN(key string, n int) bool {
	if strings.TrimSpace(key) == "" || n <= 0 {
		return false
	}
	opaqueKey := limiter.opaqueKey(key)
	token, err := randomRateLimitToken()
	if err != nil {
		limiter.recordFailure(opaqueKey)
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), limiter.timeout)
	defer cancel()
	result, err := limiter.client.Eval(
		ctx,
		redisSlidingWindowScript,
		[]string{redisHTTPRateLimitKeyPrefix + ":{" + opaqueKey + "}"},
		limiter.limit,
		limiter.window.Milliseconds(),
		n,
		token,
	)
	if err != nil {
		// Authentication and other write-route limiters fail closed when the
		// shared authority is unavailable.
		limiter.recordFailure(opaqueKey)
		return false
	}
	values, err := redisRateLimitIntegers(result, 3)
	if err != nil ||
		(values[0] != 0 && values[0] != 1) ||
		values[1] < 0 ||
		values[1] > limiter.limit ||
		values[2] < 0 ||
		int64(values[2]) > limiter.window.Milliseconds() {
		limiter.recordFailure(opaqueKey)
		return false
	}
	now := time.Now()
	limiter.mu.Lock()
	limiter.snapshots[opaqueKey] = redisRateLimitSnapshot{
		remaining: values[1],
		reset:     now.Add(time.Duration(values[2]) * time.Millisecond),
		touchedAt: now,
	}
	limiter.mu.Unlock()
	return values[0] == 1
}

// Limit returns the maximum number of requests in one Redis window.
func (limiter *RedisSlidingWindow) Limit() int {
	return limiter.limit
}

func (limiter *RedisSlidingWindow) Remaining(key string) int {
	snapshot, ok := limiter.snapshot(key)
	if !ok {
		return limiter.limit
	}
	return snapshot.remaining
}

func (limiter *RedisSlidingWindow) Reset(key string) time.Time {
	snapshot, ok := limiter.snapshot(key)
	if !ok {
		return time.Now()
	}
	return snapshot.reset
}

func (limiter *RedisSlidingWindow) snapshot(key string) (redisRateLimitSnapshot, bool) {
	opaqueKey := limiter.opaqueKey(key)
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	snapshot, ok := limiter.snapshots[opaqueKey]
	return snapshot, ok
}

func (limiter *RedisSlidingWindow) recordFailure(opaqueKey string) {
	now := time.Now()
	limiter.mu.Lock()
	limiter.snapshots[opaqueKey] = redisRateLimitSnapshot{
		remaining: 0,
		reset:     now.Add(limiter.window),
		touchedAt: now,
	}
	limiter.mu.Unlock()
}

func (limiter *RedisSlidingWindow) opaqueKey(raw string) string {
	mac := hmac.New(sha256.New, limiter.keyPepper)
	_, _ = mac.Write([]byte(raw))
	return hex.EncodeToString(mac.Sum(nil))
}

func randomRateLimitToken() (string, error) {
	var value [16]byte
	if _, err := cryptorand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func redisRateLimitIntegers(value interface{}, count int) ([]int, error) {
	var raw []interface{}
	switch typed := value.(type) {
	case []interface{}:
		raw = typed
	case []int64:
		raw = make([]interface{}, len(typed))
		for index := range typed {
			raw[index] = typed[index]
		}
	default:
		return nil, fmt.Errorf("unexpected Redis rate-limit response %T", value)
	}
	if len(raw) != count {
		return nil, fmt.Errorf("unexpected Redis rate-limit response length %d", len(raw))
	}
	result := make([]int, len(raw))
	for index, item := range raw {
		value, err := rateLimitInteger(item)
		if err != nil {
			return nil, err
		}
		result[index] = value
	}
	return result, nil
}

func rateLimitInteger(value interface{}) (int, error) {
	switch typed := value.(type) {
	case int:
		return typed, nil
	case int64:
		return safeconv.Int(typed)
	case uint:
		if typed <= uint(math.MaxInt) {
			return int(typed), nil
		}
		return 0, errors.New("redis rate-limit response exceeds the native int range")
	case float64:
		if typed < float64(math.MinInt) ||
			typed >= math.Ldexp(1, strconv.IntSize-1) ||
			math.Trunc(typed) != typed {
			return 0, errors.New("redis rate-limit response is not an integer")
		}
		return int(typed), nil
	case string:
		parsed, err := strconv.ParseInt(typed, 10, strconv.IntSize)
		if err != nil {
			return 0, err
		}
		if parsed < int64(math.MinInt) || parsed > int64(math.MaxInt) {
			return 0, errors.New("redis rate-limit response exceeds the native int range")
		}
		return int(parsed), nil
	default:
		return 0, fmt.Errorf("unexpected redis rate-limit integer %T", value)
	}
}

// InfrastructureRouteRateLimitSkip prevents shared generic limits from
// degrading liveness/readiness probes, protocol discovery, API contracts,
// browser preflights or immutable static assets. Business and protocol
// endpoints remain subject to their route-specific controls.
func InfrastructureRouteRateLimitSkip(c HTTPContext) bool {
	if getMethod(c) == "OPTIONS" {
		return true
	}
	path := getPath(c)
	switch path {
	case "/health", "/healthz", "/live", "/livez", "/ready", "/readyz",
		"/metrics", "/favicon.ico", "/robots.txt", "/openapi.json",
		"/openapi.yaml", "/.well-known":
		return true
	}
	for _, prefix := range []string{
		"/.well-known/",
		"/assets/",
		"/static/",
		"/uploads/avatars/",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

const anonymousCredentialBodyLimit = 64 << 10

const humanRefreshCookieName = "chronodesk_refresh_token"

// AnonymousIPRouteKeyFunc is the coarse anti-abuse layer for unauthenticated
// credential endpoints. It limits the trusted client IP and matched route,
// regardless of which account or credential an attacker targets.
func AnonymousIPRouteKeyFunc(c HTTPContext) string {
	return RouteKeyFunc(c)
}

// AnonymousCredentialKeyFunc is the account/credential layer for
// unauthenticated writes. It isolates employees sharing one enterprise NAT,
// while a separate IP-route limiter still prevents attackers from evading the
// coarse limit by rotating account identifiers.
func AnonymousCredentialKeyFunc(c HTTPContext) string {
	route := getRoutePattern(c)
	subject := anonymousCredentialSubject(c)
	if subject == "" {
		return IPKeyFunc(c) + "|" + route + "|unidentified"
	}
	sum := sha256.Sum256([]byte(subject))
	return route + "|subject_" + hex.EncodeToString(sum[:16])
}

func anonymousCredentialSubject(c HTTPContext) string {
	ginContext, ok := c.(*GinHTTPContext)
	if !ok || ginContext.Context.Request == nil {
		return ""
	}
	request := ginContext.Context.Request
	switch getRoutePattern(c) {
	case "/api/auth/refresh", "/api/auth/logout":
		var credential string
		count := 0
		for _, cookie := range request.Cookies() {
			if cookie.Name != humanRefreshCookieName {
				continue
			}
			count++
			credential = cookie.Value
		}
		if count == 1 &&
			credential != "" &&
			strings.TrimSpace(credential) == credential {
			return "refresh_cookie:" + credential
		}
		return ""
	}
	if request.Body == nil {
		return ""
	}
	if request.ContentLength > anonymousCredentialBodyLimit {
		return ""
	}

	original := request.Body
	body, err := io.ReadAll(io.LimitReader(original, anonymousCredentialBodyLimit+1))
	if err != nil {
		request.Body = &restoredRequestBody{
			Reader: io.MultiReader(bytes.NewReader(body), original),
			Closer: original,
		}
		return ""
	}
	if len(body) > anonymousCredentialBodyLimit {
		request.Body = &restoredRequestBody{
			Reader: io.MultiReader(bytes.NewReader(body), original),
			Closer: original,
		}
		return ""
	}
	if err := original.Close(); err != nil {
		request.Body = io.NopCloser(bytes.NewReader(body))
		return ""
	}
	request.Body = io.NopCloser(bytes.NewReader(body))

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	for _, field := range []string{"email", "refresh_token", "token"} {
		raw, exists := payload[field]
		if !exists {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}
		value = strings.TrimSpace(value)
		if field == "email" {
			value = strings.ToLower(value)
		}
		if value != "" {
			return field + ":" + value
		}
	}
	return ""
}

type restoredRequestBody struct {
	io.Reader
	io.Closer
}

// AuthenticatedUserRouteKeyFunc isolates a logged-in human and route even
// when many employees share one enterprise proxy address.
func AuthenticatedUserRouteKeyFunc(c HTTPContext) string {
	return UserRouteKeyFunc(c)
}
