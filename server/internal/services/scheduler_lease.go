package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const schedulerRedisKeyPrefix = "chronodesk:scheduler:v1"

var (
	ErrSchedulerLeaseHeld        = errors.New("scheduler job lease is held by another instance")
	ErrSchedulerLeaseLost        = errors.New("scheduler job lease ownership was lost")
	ErrSchedulerRedisUnavailable = errors.New("scheduler Redis coordination is unavailable")
)

const redisAcquireSchedulerLeaseScript = `
local acquired = redis.call("SET", KEYS[1], ARGV[1], "NX", "PX", ARGV[2])
if acquired then
  return 1
end
return 0
`

const redisRenewSchedulerLeaseScript = `
if redis.call("GET", KEYS[1]) ~= ARGV[1] then
  return 0
end
redis.call("PEXPIRE", KEYS[1], ARGV[2])
return 1
`

const redisReleaseSchedulerLeaseScript = `
if redis.call("GET", KEYS[1]) ~= ARGV[1] then
  return 0
end
redis.call("DEL", KEYS[1])
return 1
`

type schedulerLease struct {
	key   string
	token string
}

// SchedulerRedisExecutor is satisfied by both the TCP and Upstash REST Redis
// clients. Scheduler coordination intentionally depends only on atomic EVAL.
type SchedulerRedisExecutor interface {
	Eval(
		ctx context.Context,
		script string,
		keys []string,
		args ...interface{},
	) (interface{}, error)
}

type schedulerLeaseManager struct {
	redis            SchedulerRedisExecutor
	operationTimeout time.Duration
}

func newSchedulerLeaseManager(
	redis SchedulerRedisExecutor,
	operationTimeout time.Duration,
) (*schedulerLeaseManager, error) {
	if redis == nil {
		return nil, errors.New("scheduler requires Redis coordination")
	}
	if operationTimeout <= 0 {
		return nil, errors.New("scheduler Redis operation timeout must be positive")
	}
	return &schedulerLeaseManager{
		redis:            redis,
		operationTimeout: operationTimeout,
	}, nil
}

func (manager *schedulerLeaseManager) acquire(
	ctx context.Context,
	jobID string,
	ttl time.Duration,
) (*schedulerLease, error) {
	if jobID == "" {
		return nil, errors.New("scheduler job ID is required")
	}
	if ttl <= 0 {
		return nil, errors.New("scheduler lease TTL must be positive")
	}
	token, err := newSchedulerLeaseToken()
	if err != nil {
		return nil, fmt.Errorf("generate scheduler lease token: %w", err)
	}
	lease := &schedulerLease{
		key:   schedulerLeaseKey(jobID),
		token: token,
	}
	operationCtx, cancel := context.WithTimeout(ctx, manager.operationTimeout)
	defer cancel()
	result, err := manager.redis.Eval(
		operationCtx,
		redisAcquireSchedulerLeaseScript,
		[]string{lease.key},
		lease.token,
		ttl.Milliseconds(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: acquire lease: %v", ErrSchedulerRedisUnavailable, err)
	}
	acquired, err := schedulerRedisInteger(result)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid acquire response", ErrSchedulerRedisUnavailable)
	}
	switch acquired {
	case 1:
		return lease, nil
	case 0:
		return nil, ErrSchedulerLeaseHeld
	default:
		return nil, fmt.Errorf("%w: unknown acquire response", ErrSchedulerRedisUnavailable)
	}
}

func (manager *schedulerLeaseManager) renew(
	ctx context.Context,
	lease *schedulerLease,
	ttl time.Duration,
) error {
	if lease == nil || lease.key == "" || lease.token == "" {
		return errors.New("scheduler lease is required")
	}
	operationCtx, cancel := context.WithTimeout(ctx, manager.operationTimeout)
	defer cancel()
	result, err := manager.redis.Eval(
		operationCtx,
		redisRenewSchedulerLeaseScript,
		[]string{lease.key},
		lease.token,
		ttl.Milliseconds(),
	)
	if err != nil {
		return fmt.Errorf("%w: renew lease: %v", ErrSchedulerRedisUnavailable, err)
	}
	renewed, err := schedulerRedisInteger(result)
	if err != nil {
		return fmt.Errorf("%w: invalid renew response", ErrSchedulerRedisUnavailable)
	}
	if renewed != 1 {
		return ErrSchedulerLeaseLost
	}
	return nil
}

func (manager *schedulerLeaseManager) release(lease *schedulerLease) error {
	if lease == nil || lease.key == "" || lease.token == "" {
		return nil
	}
	operationCtx, cancel := context.WithTimeout(context.Background(), manager.operationTimeout)
	defer cancel()
	result, err := manager.redis.Eval(
		operationCtx,
		redisReleaseSchedulerLeaseScript,
		[]string{lease.key},
		lease.token,
	)
	if err != nil {
		return fmt.Errorf("%w: release lease: %v", ErrSchedulerRedisUnavailable, err)
	}
	released, err := schedulerRedisInteger(result)
	if err != nil {
		return fmt.Errorf("%w: invalid release response", ErrSchedulerRedisUnavailable)
	}
	if released != 1 {
		return ErrSchedulerLeaseLost
	}
	return nil
}

func schedulerLeaseKey(jobID string) string {
	sum := sha256.Sum256([]byte(jobID))
	return schedulerRedisKeyPrefix + ":job:{" + hex.EncodeToString(sum[:]) + "}:lease"
}

func newSchedulerLeaseToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func schedulerRedisInteger(value interface{}) (int64, error) {
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
		const maxInt64 = uint64(1<<63 - 1)
		if typed > maxInt64 {
			return 0, errors.New("Redis integer overflows int64")
		}
		return int64(typed), nil
	case float64:
		if typed != float64(int64(typed)) {
			return 0, errors.New("Redis number is not an integer")
		}
		return int64(typed), nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, fmt.Errorf("unsupported Redis integer type %T", value)
	}
}
