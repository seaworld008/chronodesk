package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// OpenProjectMigrationDatabase opens the privileged, short-lived connection
// used only for schema migrations and ENABLE/FORCE RLS. The caller must close
// the returned pool before accepting application traffic.
func OpenProjectMigrationDatabase(
	cfg *config.Config,
) (*gorm.DB, func() error, error) {
	if cfg == nil {
		return nil, nil, errors.New("configuration is required")
	}
	dsn := strings.TrimSpace(cfg.Database.MigrationURL)
	if dsn == "" {
		return nil, nil, errors.New(
			"DATABASE_MIGRATION_URL is required when AUTO_MIGRATE=true",
		)
	}
	db, closeDatabase, err := openProjectRolePostgres(
		cfg,
		dsn,
		"migration",
	)
	if err != nil {
		return nil, nil, err
	}
	return db, closeDatabase, nil
}

// NewProjectRuntime opens the only PostgreSQL identity retained by the
// application process. It validates ENABLE+FORCE RLS and rejects LOGIN roles
// that are SUPERUSER, BYPASSRLS, table owners, or members of an owner role
// before Redis is initialized or HTTP traffic is accepted.
func NewProjectRuntime(cfg *config.Config) (*Database, error) {
	if cfg == nil {
		return nil, errors.New("configuration is required")
	}
	dsn := strings.TrimSpace(cfg.Database.RuntimeURL)
	if dsn == "" {
		return nil, errors.New("DATABASE_RUNTIME_URL is required")
	}
	db, closeDatabase, err := openProjectRolePostgres(
		cfg,
		dsn,
		"runtime",
	)
	if err != nil {
		return nil, err
	}
	closeOnError := func(openErr error) (*Database, error) {
		if closeErr := closeDatabase(); closeErr != nil {
			return nil, errors.Join(openErr, closeErr)
		}
		return nil, openErr
	}
	if err := ValidateProjectRLSRuntime(db); err != nil {
		return closeOnError(fmt.Errorf(
			"PostgreSQL project RLS runtime validation failed: %w",
			err,
		))
	}
	if err := ValidateProjectRuntimeRole(db); err != nil {
		return closeOnError(fmt.Errorf(
			"PostgreSQL runtime role validation failed: %w",
			err,
		))
	}
	if err := InstallProjectScopeTransactionRouting(db); err != nil {
		return closeOnError(err)
	}

	redis, err := connectRedis(cfg)
	if err != nil {
		return closeOnError(fmt.Errorf(
			"failed to connect to required Redis: %w",
			err,
		))
	}
	return &Database{DB: db, Redis: redis}, nil
}

func openProjectRolePostgres(
	cfg *config.Config,
	dsn string,
	purpose string,
) (*gorm.DB, func() error, error) {
	if err := ValidatePostgresTransport(
		dsn,
		os.Getenv("POSTGRES_ALLOW_INSECURE") == "true",
	); err != nil {
		return nil, nil, fmt.Errorf(
			"%s PostgreSQL transport validation failed: %w",
			purpose,
			err,
		)
	}
	logLevel := logger.Info
	if cfg.Server.Environment == "production" {
		logLevel = logger.Error
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger:         logger.Default.LogMode(logLevel),
		TranslateError: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf(
			"open %s PostgreSQL connection: %w",
			purpose,
			err,
		)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, fmt.Errorf(
			"open %s PostgreSQL pool: %w",
			purpose,
			err,
		)
	}
	sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)

	pingContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingContext); err != nil {
		_ = sqlDB.Close()
		return nil, nil, fmt.Errorf(
			"ping %s PostgreSQL connection: %w",
			purpose,
			err,
		)
	}
	return db, sqlDB.Close, nil
}
