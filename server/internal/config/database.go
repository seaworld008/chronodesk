package config

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/go-redis/redis/v8"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Database 数据库连接管理器接口
type DatabaseInterface interface {
	Close() error
	HealthCheck(ctx context.Context) error
}

// Database 数据库连接管理器
type Database struct {
	DB    *gorm.DB
	Redis *redis.Client
}

// NewDatabase 创建数据库连接
func NewDatabase(cfg *Config) (*Database, error) {
	log.Printf("Connecting to database: %s", cfg.Database.Name)

	// 连接PostgreSQL
	db, err := connectPostgreSQL(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	// 连接Redis
	rdb, err := connectRedis(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	log.Printf("Database connections established successfully")

	return &Database{
		DB:    db,
		Redis: rdb,
	}, nil
}

// connectPostgreSQL 连接PostgreSQL数据库
func connectPostgreSQL(cfg *Config) (*gorm.DB, error) {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=%s TimeZone=Asia/Shanghai",
		cfg.Database.Host,
		cfg.Database.User,
		cfg.Database.Password,
		cfg.Database.Name,
		cfg.Database.Port,
		cfg.Database.SSLMode,
	)

	// 配置GORM日志
	gormLogger := logger.Default.LogMode(logger.Info)
	if cfg.Server.Environment == "production" {
		gormLogger = logger.Default.LogMode(logger.Error)
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// 配置连接池
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

	return db, nil
}

// connectRedis 连接Redis
func connectRedis(cfg *Config) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.GetRedisAddr(),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return rdb, nil
}

// Close 关闭数据库连接
func (d *Database) Close() error {
	if d.DB != nil {
		sqlDB, err := d.DB.DB()
		if err != nil {
			log.Printf("Error getting underlying sql.DB: %v", err)
		} else {
			if err := sqlDB.Close(); err != nil {
				log.Printf("Error closing PostgreSQL connection: %v", err)
			}
		}
	}

	if d.Redis != nil {
		if err := d.Redis.Close(); err != nil {
			log.Printf("Error closing Redis connection: %v", err)
		}
	}

	log.Println("Database connections closed")
	return nil
}

// HealthCheck 健康检查
func (d *Database) HealthCheck(ctx context.Context) error {
	// 检查PostgreSQL连接
	if d.DB != nil {
		sqlDB, err := d.DB.DB()
		if err != nil {
			return fmt.Errorf("failed to get underlying sql.DB: %w", err)
		}
		if err := sqlDB.PingContext(ctx); err != nil {
			return fmt.Errorf("PostgreSQL health check failed: %w", err)
		}
	}

	// 检查Redis连接
	if d.Redis != nil {
		if _, err := d.Redis.Ping(ctx).Result(); err != nil {
			return fmt.Errorf("Redis health check failed: %w", err)
		}
	}

	log.Println("Database health check passed")
	return nil
}
