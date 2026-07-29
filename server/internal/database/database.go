package database

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/seaworld008/chronodesk/server/internal/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// RedisInterface 定义Redis接口，支持不同的实现
type RedisInterface interface {
	Ping(ctx context.Context) error
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	Get(ctx context.Context, key string) (string, error)
	Del(ctx context.Context, keys ...string) error
	Exists(ctx context.Context, keys ...string) (int64, error)
	Expire(ctx context.Context, key string, expiration time.Duration) error
	TTL(ctx context.Context, key string) (time.Duration, error)
	// Eval is the atomic primitive used by distributed rate, concurrency and
	// loop guards. Implementations must pass keys separately from ARGV so the
	// same script also works with Redis Cluster hash slots.
	Eval(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error)
	Close() error
}

// TCPRedisClient TCP Redis客户端包装器
type TCPRedisClient struct {
	client *redis.Client
}

var _ RedisInterface = (*TCPRedisClient)(nil)

// NewTCPRedisClient 创建TCP Redis客户端
func NewTCPRedisClient(client *redis.Client) *TCPRedisClient {
	return &TCPRedisClient{client: client}
}

// 实现RedisInterface接口
func (c *TCPRedisClient) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *TCPRedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return c.client.Set(ctx, key, value, expiration).Err()
}

func (c *TCPRedisClient) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, key).Result()
}

func (c *TCPRedisClient) Del(ctx context.Context, keys ...string) error {
	return c.client.Del(ctx, keys...).Err()
}

func (c *TCPRedisClient) Exists(ctx context.Context, keys ...string) (int64, error) {
	return c.client.Exists(ctx, keys...).Result()
}

func (c *TCPRedisClient) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return c.client.Expire(ctx, key, expiration).Err()
}

func (c *TCPRedisClient) TTL(ctx context.Context, key string) (time.Duration, error) {
	return c.client.TTL(ctx, key).Result()
}

func (c *TCPRedisClient) Eval(
	ctx context.Context,
	script string,
	keys []string,
	args ...interface{},
) (interface{}, error) {
	return c.client.Eval(ctx, script, keys, args...).Result()
}

func (c *TCPRedisClient) Close() error {
	return c.client.Close()
}

// DatabaseInterface 定义数据库接口
type DatabaseInterface interface {
	Close() error
	HealthCheck() error
}

// Database 数据库结构体
type Database struct {
	DB    *gorm.DB
	Redis RedisInterface
}

// New 创建新的数据库连接
func New(cfg *config.Config) (*Database, error) {
	// 连接 PostgreSQL
	db, err := connectPostgreSQL(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	// Redis 是分布式限流、Agent 并发控制、异常循环检测和缓存一致性的
	// 共享权威存储。连接失败必须阻止实例接收流量，禁止静默降级为单机行为。
	rdb, err := connectRedis(cfg)
	if err != nil {
		if sqlDB, closeErr := db.DB(); closeErr == nil {
			_ = sqlDB.Close()
		}
		return nil, fmt.Errorf("failed to connect to required Redis: %w", err)
	}

	return &Database{
		DB:    db,
		Redis: rdb,
	}, nil
}

// connectPostgreSQL 连接 PostgreSQL 数据库
func connectPostgreSQL(cfg *config.Config) (*gorm.DB, error) {
	// 优先使用 DATABASE_URL 环境变量
	var dsn string
	if databaseURL := getEnv("DATABASE_URL", ""); databaseURL != "" {
		dsn = databaseURL
	} else {
		// 构建连接字符串
		dsn = fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=%s TimeZone=%s",
			cfg.Database.Host,
			cfg.Database.User,
			cfg.Database.Password,
			cfg.Database.Name,
			cfg.Database.Port,
			cfg.Database.SSLMode,
			cfg.Database.Timezone,
		)
	}

	// 配置 GORM 日志
	var logLevel logger.LogLevel
	if cfg.Server.Environment == "production" {
		logLevel = logger.Error
	} else {
		logLevel = logger.Info
	}

	// 连接数据库
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, err
	}

	// 获取底层的 sql.DB 对象
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// 设置连接池参数
	sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)

	return db, nil
}

// connectRedis 连接 Redis
func connectRedis(cfg *config.Config) (RedisInterface, error) {
	// 首先尝试HTTP REST API连接（推荐用于Upstash）
	if httpClient, err := NewHTTPRedisClient(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		pingErr := httpClient.Ping(ctx)
		cancel()

		if pingErr == nil {
			log.Print("Redis HTTP REST API 连接成功")
			return httpClient, nil
		}
		_ = httpClient.Close()
		log.Printf("Redis HTTP REST API 连接失败：%v", pingErr)
	}

	// 如果HTTP连接失败，尝试TCP连接
	if redisURL := getEnv("REDIS_URL", ""); redisURL != "" {
		opt, err := redis.ParseURL(redisURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Redis URL: %w", err)
		}

		// 应用额外配置
		opt.PoolSize = cfg.Redis.PoolSize
		opt.MinIdleConns = cfg.Redis.MinIdleConns
		opt.PoolTimeout = cfg.Redis.PoolTimeout
		opt.ConnMaxIdleTime = cfg.Redis.IdleTimeout

		rdb := redis.NewClient(opt)

		// 测试连接
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		tcpClient := NewTCPRedisClient(rdb)
		if err := tcpClient.Ping(ctx); err != nil {
			_ = tcpClient.Close()
			log.Printf("Redis TCP 连接失败：%v", err)
			return nil, fmt.Errorf("Redis ping failed: %w", err)
		}

		log.Print("Redis TCP 连接成功")
		return tcpClient, nil
	}

	// 使用传统的 host:port 配置
	rdb := redis.NewClient(&redis.Options{
		Addr:            fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Password:        cfg.Redis.Password,
		DB:              cfg.Redis.DB,
		PoolSize:        cfg.Redis.PoolSize,
		MinIdleConns:    cfg.Redis.MinIdleConns,
		PoolTimeout:     cfg.Redis.PoolTimeout,
		ConnMaxIdleTime: cfg.Redis.IdleTimeout,
	})

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tcpClient := NewTCPRedisClient(rdb)
	if err := tcpClient.Ping(ctx); err != nil {
		_ = tcpClient.Close()
		return nil, fmt.Errorf("Redis ping failed: %w", err)
	}

	return tcpClient, nil
}

// Close 关闭数据库连接
func (d *Database) Close() error {
	// 关闭 PostgreSQL 连接
	if d.DB != nil {
		sqlDB, err := d.DB.DB()
		if err != nil {
			return err
		}
		if err := sqlDB.Close(); err != nil {
			return err
		}
	}

	// 关闭 Redis 连接
	if d.Redis != nil {
		if err := d.Redis.Close(); err != nil {
			return err
		}
	}

	return nil
}

// getEnv 获取环境变量
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// HealthCheck 检查数据库连接健康状态
func (d *Database) HealthCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := d.PostgreSQLHealthCheck(ctx); err != nil {
		return err
	}
	return d.RedisHealthCheck(ctx)
}

// PostgreSQLHealthCheck validates the primary transactional store within the
// caller's deadline.
func (d *Database) PostgreSQLHealthCheck(ctx context.Context) error {
	if d == nil || d.DB == nil {
		return fmt.Errorf("PostgreSQL client is not initialized")
	}
	sqlDB, err := d.DB.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("PostgreSQL ping failed: %w", err)
	}
	return nil
}

// RedisHealthCheck validates the shared coordination store. Redis is a hard
// readiness dependency, not an optional cache.
func (d *Database) RedisHealthCheck(ctx context.Context) error {
	if d == nil {
		return fmt.Errorf("Redis client is not initialized")
	}
	if d.Redis == nil {
		return fmt.Errorf("Redis client is not initialized")
	}
	if err := d.Redis.Ping(ctx); err != nil {
		return fmt.Errorf("Redis ping failed: %w", err)
	}

	return nil
}
