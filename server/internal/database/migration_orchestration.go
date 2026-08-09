package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const (
	chronodeskMigrationAdvisoryLockKey  = "chronodesk_schema_migration_v1"
	chronodeskMigrationLockTimeout      = "5s"
	chronodeskMigrationStatementTimeout = "4min"
)

func runBoundedMigrationOrchestration(
	ctx context.Context,
	db *gorm.DB,
	runMainMigration func(*gorm.DB) error,
) error {
	if ctx == nil || db == nil || runMainMigration == nil {
		return errors.New(
			"migration orchestration requires context, database, and callback",
		)
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return errors.New(
			"migration orchestration requires a top-level database handle",
		)
	}
	return db.WithContext(ctx).Connection(func(connection *gorm.DB) error {
		if connection.Dialector.Name() == "sqlite" {
			if err := connection.Exec(
				"PRAGMA foreign_keys = ON",
			).Error; err != nil {
				return fmt.Errorf(
					"enable SQLite foreign keys for migration: %w",
					err,
				)
			}
		}
		return withMigrationSessionAdvisoryLock(
			connection,
			func(locked *gorm.DB) error {
				if err := locked.AutoMigrate(
					&models.SchemaMigrationCheckpoint{},
				); err != nil {
					return fmt.Errorf(
						"schema migration checkpoint setup failed: %w",
						err,
					)
				}
				if err := MigrateWebhookSnapshotCredentialLifetimeContract(
					locked,
				); err != nil {
					return fmt.Errorf(
						"webhook credential lifetime legacy cutover failed: %w",
						err,
					)
				}
				if err := locked.Transaction(runMainMigration); err != nil {
					return err
				}
				if err := MigrateWebhookSnapshotCredentialLifetimeContract(
					locked.Session(&gorm.Session{NewDB: true}),
				); err != nil {
					return fmt.Errorf(
						"webhook credential lifetime post-migration cutover failed: %w",
						err,
					)
				}
				clean := locked.Session(&gorm.Session{NewDB: true})
				if err := validateWebhookCredentialLifetimeCatalog(
					clean,
				); err != nil {
					return fmt.Errorf(
						"validate committed webhook credential catalog: %w",
						err,
					)
				}
				if err := ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
					ctx,
					clean,
				); err != nil {
					return fmt.Errorf(
						"validate committed webhook credential data: %w",
						err,
					)
				}
				return nil
			},
		)
	})
}

func withMigrationSessionAdvisoryLock(
	db *gorm.DB,
	run func(*gorm.DB) error,
) error {
	if db == nil || run == nil {
		return errors.New(
			"migration session lock requires database and callback",
		)
	}
	db = db.Session(&gorm.Session{NewDB: true})
	if db.Dialector.Name() != "postgres" {
		return run(db)
	}
	var original struct {
		LockTimeout      string `gorm:"column:lock_timeout"`
		StatementTimeout string `gorm:"column:statement_timeout"`
	}
	if err := db.Raw(`
		SELECT
			current_setting('lock_timeout') AS lock_timeout,
			current_setting('statement_timeout') AS statement_timeout
	`).Scan(&original).Error; err != nil {
		return fmt.Errorf("read PostgreSQL migration timeouts: %w", err)
	}
	restoreTimeouts := func() error {
		return db.Exec(`
			SELECT
				set_config('lock_timeout', ?, false),
				set_config('statement_timeout', ?, false)
		`,
			original.LockTimeout,
			original.StatementTimeout,
		).Error
	}
	if err := db.Exec(`
		SELECT
			set_config('lock_timeout', ?, false),
			set_config('statement_timeout', ?, false)
	`,
		chronodeskMigrationLockTimeout,
		chronodeskMigrationStatementTimeout,
	).Error; err != nil {
		return fmt.Errorf("set PostgreSQL migration timeouts: %w", err)
	}
	if err := db.Exec(
		`SELECT pg_advisory_lock(hashtextextended(?, 0))`,
		chronodeskMigrationAdvisoryLockKey,
	).Error; err != nil {
		return errors.Join(
			fmt.Errorf("acquire PostgreSQL migration session lock: %w", err),
			restoreTimeouts(),
		)
	}
	runErr := run(db)
	var unlocked bool
	unlockErr := db.Raw(
		`SELECT pg_advisory_unlock(hashtextextended(?, 0))`,
		chronodeskMigrationAdvisoryLockKey,
	).Scan(&unlocked).Error
	if unlockErr == nil && !unlocked {
		unlockErr = errors.New(
			"PostgreSQL migration advisory lock was not held at release",
		)
	}
	if unlockErr != nil {
		unlockErr = fmt.Errorf(
			"release PostgreSQL migration session lock: %w",
			unlockErr,
		)
	}
	restoreErr := restoreTimeouts()
	if restoreErr != nil {
		restoreErr = fmt.Errorf(
			"restore PostgreSQL migration timeouts: %w",
			restoreErr,
		)
	}
	return errors.Join(runErr, unlockErr, restoreErr)
}
