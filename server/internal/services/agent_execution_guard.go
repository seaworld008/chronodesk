package services

import (
	"context"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

const (
	defaultAgentExecutionLeaseTTL = 5 * time.Minute
	agentRateWindow               = time.Minute
	agentGuardRedisKeyPrefix      = "chronodesk:agent-guard:v1"
)

// RedisScriptExecutor is deliberately satisfied by both the go-redis and
// Upstash REST clients. EVAL keeps every check-and-update transition atomic.
type RedisScriptExecutor interface {
	Eval(
		ctx context.Context,
		script string,
		keys []string,
		args ...interface{},
	) (interface{}, error)
}

type AgentExecutionGuardRequest struct {
	SubjectID         string
	RateLimit         int
	ConcurrencyLimit  int
	ConcurrencyTTL    time.Duration
	ObservedAtForTest time.Time
}

type AgentLoopGuardRequest struct {
	Fingerprint       string
	Threshold         int
	Window            time.Duration
	ObservedAtForTest time.Time
}

type AgentExecutionPermit struct {
	guardKey string
	token    string
}

// AgentExecutionGuard is the shared authority for per-principal request rate,
// in-flight concurrency and automation-loop windows.
type AgentExecutionGuard interface {
	Acquire(context.Context, AgentExecutionGuardRequest) (*AgentExecutionPermit, error)
	Release(context.Context, *AgentExecutionPermit) error
	RecordLoop(context.Context, AgentLoopGuardRequest) (bool, error)
	IsDistributed() bool
}

// RedisAgentExecutionGuard uses Redis server time and Lua scripts. This avoids
// process clock skew and makes limits consistent across every ChronoDesk
// replica. Identifiers are HMACed before becoming Redis keys.
type RedisAgentExecutionGuard struct {
	client    RedisScriptExecutor
	keyPepper []byte
}

func NewRedisAgentExecutionGuard(
	client RedisScriptExecutor,
	keyPepper []byte,
) (*RedisAgentExecutionGuard, error) {
	if client == nil {
		return nil, errors.New("redis Agent execution guard requires a client")
	}
	if len(keyPepper) < 16 {
		return nil, errors.New("redis Agent execution guard key pepper must be at least 16 bytes")
	}
	return &RedisAgentExecutionGuard{
		client:    client,
		keyPepper: append([]byte(nil), keyPepper...),
	}, nil
}

func (*RedisAgentExecutionGuard) IsDistributed() bool {
	return true
}

const redisAcquireAgentExecutionScript = `
local server_time = redis.call("TIME")
local now_ms = (tonumber(server_time[1]) * 1000) + math.floor(tonumber(server_time[2]) / 1000)
local rate_limit = tonumber(ARGV[1])
local concurrency_limit = tonumber(ARGV[2])
local rate_window_ms = tonumber(ARGV[3])
local concurrency_ttl_ms = tonumber(ARGV[4])
local token = ARGV[5]

redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", now_ms - rate_window_ms)
redis.call("ZREMRANGEBYSCORE", KEYS[2], "-inf", now_ms)

if rate_limit > 0 and redis.call("ZCARD", KEYS[1]) >= rate_limit then
  return 1
end
if concurrency_limit > 0 and redis.call("ZCARD", KEYS[2]) >= concurrency_limit then
  return 2
end

if rate_limit > 0 then
  redis.call("ZADD", KEYS[1], now_ms, token)
  redis.call("PEXPIRE", KEYS[1], rate_window_ms + 1000)
end
redis.call("ZADD", KEYS[2], now_ms + concurrency_ttl_ms, token)
redis.call("PEXPIRE", KEYS[2], concurrency_ttl_ms + 1000)
return 0
`

const redisReleaseAgentExecutionScript = `
local removed = redis.call("ZREM", KEYS[1], ARGV[1])
if redis.call("ZCARD", KEYS[1]) == 0 then
  redis.call("DEL", KEYS[1])
end
return removed
`

const redisRecordAgentLoopScript = `
local server_time = redis.call("TIME")
local now_ms = (tonumber(server_time[1]) * 1000) + math.floor(tonumber(server_time[2]) / 1000)
local threshold = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local token = ARGV[3]

redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", now_ms - window_ms)
if redis.call("ZCARD", KEYS[1]) >= threshold then
  return 1
end
redis.call("ZADD", KEYS[1], now_ms, token)
redis.call("PEXPIRE", KEYS[1], window_ms + 1000)
return 0
`

func (g *RedisAgentExecutionGuard) Acquire(
	ctx context.Context,
	request AgentExecutionGuardRequest,
) (*AgentExecutionPermit, error) {
	if err := validateAgentExecutionGuardRequest(request); err != nil {
		return nil, err
	}
	token, err := newAgentGuardToken()
	if err != nil {
		return nil, fmt.Errorf("generate Agent execution permit: %w", err)
	}
	hash := g.opaqueKey(request.SubjectID)
	// The hash tag pins both keys to one Redis Cluster slot.
	tag := "{" + hash + "}"
	rateKey := agentGuardRedisKeyPrefix + ":" + tag + ":rate"
	concurrencyKey := agentGuardRedisKeyPrefix + ":" + tag + ":concurrency"
	result, err := g.client.Eval(
		ctx,
		redisAcquireAgentExecutionScript,
		[]string{rateKey, concurrencyKey},
		request.RateLimit,
		request.ConcurrencyLimit,
		agentRateWindow.Milliseconds(),
		request.ConcurrencyTTL.Milliseconds(),
		token,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: acquire Redis permit: %v", ErrExecutionGuardUnavailable, err)
	}
	code, err := redisInteger(result)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid Redis acquire response", ErrExecutionGuardUnavailable)
	}
	switch code {
	case 0:
		return &AgentExecutionPermit{guardKey: concurrencyKey, token: token}, nil
	case 1:
		return nil, ErrRateLimited
	case 2:
		return nil, ErrConcurrencyLimit
	default:
		return nil, fmt.Errorf("%w: unknown Redis acquire response", ErrExecutionGuardUnavailable)
	}
}

func (g *RedisAgentExecutionGuard) Release(
	ctx context.Context,
	permit *AgentExecutionPermit,
) error {
	if permit == nil || permit.guardKey == "" || permit.token == "" {
		return nil
	}
	if _, err := g.client.Eval(
		ctx,
		redisReleaseAgentExecutionScript,
		[]string{permit.guardKey},
		permit.token,
	); err != nil {
		return fmt.Errorf("%w: release Redis permit: %v", ErrExecutionGuardUnavailable, err)
	}
	return nil
}

func (g *RedisAgentExecutionGuard) RecordLoop(
	ctx context.Context,
	request AgentLoopGuardRequest,
) (bool, error) {
	if request.Fingerprint == "" || request.Threshold <= 0 || request.Window <= 0 {
		return false, errors.New("invalid Agent loop guard request")
	}
	token, err := newAgentGuardToken()
	if err != nil {
		return false, fmt.Errorf("generate Agent loop token: %w", err)
	}
	key := agentGuardRedisKeyPrefix + ":loop:{" + g.opaqueKey(request.Fingerprint) + "}"
	result, err := g.client.Eval(
		ctx,
		redisRecordAgentLoopScript,
		[]string{key},
		request.Threshold,
		request.Window.Milliseconds(),
		token,
	)
	if err != nil {
		return false, fmt.Errorf("%w: record Redis loop window: %v", ErrExecutionGuardUnavailable, err)
	}
	code, err := redisInteger(result)
	if err != nil {
		return false, fmt.Errorf("%w: invalid Redis loop response", ErrExecutionGuardUnavailable)
	}
	switch code {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("%w: unknown Redis loop response", ErrExecutionGuardUnavailable)
	}
}

func (g *RedisAgentExecutionGuard) opaqueKey(raw string) string {
	mac := hmac.New(sha256.New, g.keyPepper)
	_, _ = mac.Write([]byte(raw))
	return hex.EncodeToString(mac.Sum(nil))
}

func validateAgentExecutionGuardRequest(request AgentExecutionGuardRequest) error {
	if request.SubjectID == "" {
		return errors.New("agent execution subject is required")
	}
	if request.RateLimit < 0 || request.ConcurrencyLimit <= 0 {
		return errors.New("agent execution rate limit must not be negative and concurrency limit must be positive")
	}
	if request.ConcurrencyTTL < time.Second {
		return errors.New("agent execution concurrency TTL must be at least one second")
	}
	return nil
}

func newAgentGuardToken() (string, error) {
	var random [16]byte
	if _, err := cryptorand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

func redisInteger(value interface{}) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int8:
		return int64(typed), nil
	case int16:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case int64:
		return typed, nil
	case uint:
		return int64(typed), nil
	case uint8:
		return int64(typed), nil
	case uint16:
		return int64(typed), nil
	case uint32:
		return int64(typed), nil
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return 0, errors.New("redis integer overflows int64")
		}
		return int64(typed), nil
	case float64:
		if typed != float64(int64(typed)) {
			return 0, errors.New("redis number is not an integer")
		}
		return int64(typed), nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, fmt.Errorf("unsupported redis integer type %T", value)
	}
}

// InMemoryAgentExecutionGuard is intended for deterministic unit tests and
// single-process development only. Production wiring must use
// RedisAgentExecutionGuard and set RequireDistributedExecutionGuard.
type InMemoryAgentExecutionGuard struct {
	mu          sync.Mutex
	rateWindows map[string][]time.Time
	inFlight    map[string]map[string]time.Time
	loopWindows map[string][]time.Time
}

func NewInMemoryAgentExecutionGuardForTesting() *InMemoryAgentExecutionGuard {
	return &InMemoryAgentExecutionGuard{
		rateWindows: make(map[string][]time.Time),
		inFlight:    make(map[string]map[string]time.Time),
		loopWindows: make(map[string][]time.Time),
	}
}

func (*InMemoryAgentExecutionGuard) IsDistributed() bool {
	return false
}

func (g *InMemoryAgentExecutionGuard) Acquire(
	_ context.Context,
	request AgentExecutionGuardRequest,
) (*AgentExecutionPermit, error) {
	if err := validateAgentExecutionGuardRequest(request); err != nil {
		return nil, err
	}
	now := request.ObservedAtForTest
	if now.IsZero() {
		now = time.Now()
	}
	token, err := newAgentGuardToken()
	if err != nil {
		return nil, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneExecutionLocked(now)
	rates := retainAgentGuardTimes(g.rateWindows[request.SubjectID], now.Add(-agentRateWindow), true)
	inFlight := g.inFlight[request.SubjectID]
	if inFlight == nil {
		inFlight = make(map[string]time.Time)
	}
	for leaseToken, expiresAt := range inFlight {
		if !expiresAt.After(now) {
			delete(inFlight, leaseToken)
		}
	}
	if request.RateLimit > 0 && len(rates) >= request.RateLimit {
		g.rateWindows[request.SubjectID] = rates
		g.setInFlight(request.SubjectID, inFlight)
		return nil, ErrRateLimited
	}
	if len(inFlight) >= request.ConcurrencyLimit {
		g.rateWindows[request.SubjectID] = rates
		g.setInFlight(request.SubjectID, inFlight)
		return nil, ErrConcurrencyLimit
	}
	if request.RateLimit > 0 {
		g.rateWindows[request.SubjectID] = append(rates, now)
	}
	inFlight[token] = now.Add(request.ConcurrencyTTL)
	g.inFlight[request.SubjectID] = inFlight
	g.pruneEmptyLocked()
	return &AgentExecutionPermit{guardKey: request.SubjectID, token: token}, nil
}

func (g *InMemoryAgentExecutionGuard) Release(
	_ context.Context,
	permit *AgentExecutionPermit,
) error {
	if permit == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	inFlight := g.inFlight[permit.guardKey]
	delete(inFlight, permit.token)
	g.setInFlight(permit.guardKey, inFlight)
	return nil
}

func (g *InMemoryAgentExecutionGuard) RecordLoop(
	_ context.Context,
	request AgentLoopGuardRequest,
) (bool, error) {
	if request.Fingerprint == "" || request.Threshold <= 0 || request.Window <= 0 {
		return false, errors.New("invalid Agent loop guard request")
	}
	now := request.ObservedAtForTest
	if now.IsZero() {
		now = time.Now()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	attempts := retainAgentGuardTimes(
		g.loopWindows[request.Fingerprint],
		now.Add(-request.Window),
		true,
	)
	if len(attempts) >= request.Threshold {
		g.loopWindows[request.Fingerprint] = attempts
		return true, nil
	}
	g.loopWindows[request.Fingerprint] = append(attempts, now)
	return false, nil
}

func (g *InMemoryAgentExecutionGuard) setInFlight(
	principalID string,
	inFlight map[string]time.Time,
) {
	if len(inFlight) == 0 {
		delete(g.inFlight, principalID)
		return
	}
	g.inFlight[principalID] = inFlight
}

func (g *InMemoryAgentExecutionGuard) pruneExecutionLocked(now time.Time) {
	for principalID, entries := range g.rateWindows {
		retained := retainAgentGuardTimes(entries, now.Add(-agentRateWindow), true)
		if len(retained) == 0 {
			delete(g.rateWindows, principalID)
		} else {
			g.rateWindows[principalID] = retained
		}
	}
	for principalID, entries := range g.inFlight {
		for token, expiresAt := range entries {
			if !expiresAt.After(now) {
				delete(entries, token)
			}
		}
		g.setInFlight(principalID, entries)
	}
}

func (g *InMemoryAgentExecutionGuard) pruneEmptyLocked() {
	for principalID, entries := range g.rateWindows {
		if len(entries) == 0 {
			delete(g.rateWindows, principalID)
		}
	}
	for principalID, entries := range g.inFlight {
		if len(entries) == 0 {
			delete(g.inFlight, principalID)
		}
	}
	for fingerprint, entries := range g.loopWindows {
		if len(entries) == 0 {
			delete(g.loopWindows, fingerprint)
		}
	}
}

func retainAgentGuardTimes(
	values []time.Time,
	cutoff time.Time,
	strictlyAfter bool,
) []time.Time {
	retained := values[:0]
	for _, value := range values {
		if (strictlyAfter && value.After(cutoff)) || (!strictlyAfter && !value.Before(cutoff)) {
			retained = append(retained, value)
		}
	}
	return retained
}
