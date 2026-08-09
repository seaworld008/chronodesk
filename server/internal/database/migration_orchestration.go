package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const (
	chronodeskMigrationAdvisoryLockKey  = "chronodesk_schema_migration_v1"
	chronodeskMigrationLockTimeout      = "5s"
	chronodeskMigrationStatementTimeout = "4min"
	chronodeskMigrationCleanupTimeout   = 5 * time.Second
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
	sqlConnection, ok := db.Statement.ConnPool.(*sql.Conn)
	if !ok || sqlConnection == nil {
		return errors.New(
			"PostgreSQL migration session lock requires a pinned physical connection",
		)
	}
	var original struct {
		BackendPID       int    `gorm:"column:backend_pid"`
		LockTimeout      string `gorm:"column:lock_timeout"`
		StatementTimeout string `gorm:"column:statement_timeout"`
	}
	if err := db.Raw(`
		SELECT
			pg_backend_pid() AS backend_pid,
			current_setting('lock_timeout') AS lock_timeout,
			current_setting('statement_timeout') AS statement_timeout
	`).Scan(&original).Error; err != nil {
		return fmt.Errorf("read PostgreSQL migration timeouts: %w", err)
	}
	lockHeld := false
	cleanup := func() error {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(),
			chronodeskMigrationCleanupTimeout,
		)
		defer cancel()
		cleanupDB := db.Session(&gorm.Session{
			NewDB:   true,
			Context: cleanupCtx,
		})
		var cleanupErrs []error
		if lockHeld {
			var unlocked bool
			if err := cleanupDB.Raw(
				`SELECT pg_advisory_unlock(hashtextextended(?, 0))`,
				chronodeskMigrationAdvisoryLockKey,
			).Scan(&unlocked).Error; err != nil {
				cleanupErrs = append(
					cleanupErrs,
					fmt.Errorf(
						"release PostgreSQL migration session lock: %w",
						err,
					),
				)
			} else if !unlocked {
				cleanupErrs = append(
					cleanupErrs,
					errors.New(
						"PostgreSQL migration advisory lock was not held at release",
					),
				)
			}
		}
		if err := cleanupDB.Exec(
			"SELECT pg_advisory_unlock_all()",
		).Error; err != nil {
			cleanupErrs = append(
				cleanupErrs,
				fmt.Errorf(
					"release all PostgreSQL migration session locks: %w",
					err,
				),
			)
		}
		if err := cleanupDB.Exec(`
			SELECT
				set_config('lock_timeout', ?, false),
				set_config('statement_timeout', ?, false)
		`,
			original.LockTimeout,
			original.StatementTimeout,
		).Error; err != nil {
			cleanupErrs = append(
				cleanupErrs,
				fmt.Errorf(
					"restore PostgreSQL migration timeouts: %w",
					err,
				),
			)
		}
		var restored struct {
			LockTimeout      string `gorm:"column:lock_timeout"`
			StatementTimeout string `gorm:"column:statement_timeout"`
			AdvisoryLocks    int64  `gorm:"column:advisory_locks"`
		}
		if err := cleanupDB.Raw(`
			SELECT
				current_setting('lock_timeout') AS lock_timeout,
				current_setting('statement_timeout') AS statement_timeout,
				(
					SELECT COUNT(*)
					FROM pg_locks
					WHERE pid = pg_backend_pid()
					  AND locktype = 'advisory'
				) AS advisory_locks
		`).Scan(&restored).Error; err != nil {
			cleanupErrs = append(
				cleanupErrs,
				fmt.Errorf(
					"read back PostgreSQL migration session cleanup: %w",
					err,
				),
			)
		} else {
			if restored.LockTimeout != original.LockTimeout ||
				restored.StatementTimeout != original.StatementTimeout {
				cleanupErrs = append(
					cleanupErrs,
					fmt.Errorf(
						"PostgreSQL migration timeouts restored as %q/%q, want %q/%q",
						restored.LockTimeout,
						restored.StatementTimeout,
						original.LockTimeout,
						original.StatementTimeout,
					),
				)
			}
			if restored.AdvisoryLocks != 0 {
				cleanupErrs = append(
					cleanupErrs,
					fmt.Errorf(
						"PostgreSQL migration backend %d retained %d advisory locks",
						original.BackendPID,
						restored.AdvisoryLocks,
					),
				)
			}
		}
		cleanupErr := errors.Join(cleanupErrs...)
		if cleanupErr == nil {
			return nil
		}
		discardErr := sqlConnection.Raw(func(any) error {
			return driver.ErrBadConn
		})
		if discardErr != nil &&
			!errors.Is(discardErr, driver.ErrBadConn) &&
			!errors.Is(discardErr, sql.ErrConnDone) {
			cleanupErr = errors.Join(
				cleanupErr,
				fmt.Errorf(
					"discard PostgreSQL migration physical connection: %w",
					discardErr,
				),
			)
		}
		if waitErr := waitForPostgresBackendExit(
			cleanupCtx,
			db,
			original.BackendPID,
		); waitErr != nil {
			cleanupErr = errors.Join(cleanupErr, waitErr)
		}
		return cleanupErr
	}
	if err := db.Exec(`
		SELECT
			set_config('lock_timeout', ?, false),
			set_config('statement_timeout', ?, false)
	`,
		chronodeskMigrationLockTimeout,
		chronodeskMigrationStatementTimeout,
	).Error; err != nil {
		return errors.Join(
			fmt.Errorf("set PostgreSQL migration timeouts: %w", err),
			cleanup(),
		)
	}
	if err := db.Exec(
		`SELECT pg_advisory_lock(hashtextextended(?, 0))`,
		chronodeskMigrationAdvisoryLockKey,
	).Error; err != nil {
		return errors.Join(
			fmt.Errorf("acquire PostgreSQL migration session lock: %w", err),
			cleanup(),
		)
	}
	lockHeld = true
	runErr := run(db)
	return errors.Join(runErr, cleanup())
}

func waitForPostgresBackendExit(
	ctx context.Context,
	db *gorm.DB,
	backendPID int,
) error {
	sqlDB, ok := db.Config.ConnPool.(*sql.DB)
	if !ok || sqlDB == nil {
		return errors.New(
			"verify discarded PostgreSQL migration backend requires the root pool",
		)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var active int64
		if err := sqlDB.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM pg_stat_activity WHERE pid = $1`,
			backendPID,
		).Scan(&active); err != nil {
			return fmt.Errorf(
				"verify discarded PostgreSQL migration backend %d: %w",
				backendPID,
				err,
			)
		}
		if active == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf(
				"discarded PostgreSQL migration backend %d remained active: %w",
				backendPID,
				ctx.Err(),
			)
		case <-ticker.C:
		}
	}
}
