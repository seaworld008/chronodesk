package database

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigrationSessionCleanupSurvivesCallerCancellationPostgres(
	t *testing.T,
) {
	rawDSN := loopbackPostgresIntegrationDSN(t)
	config := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	}
	db, err := gorm.Open(postgres.Open(rawDSN), config)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := gorm.Open(postgres.Open(rawDSN), config)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	peerSQL, err := peer.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(),
			3*time.Second,
		)
		defer cancel()
		_ = db.WithContext(cleanupCtx).Exec(
			"SELECT pg_advisory_unlock_all()",
		).Error
		_ = peer.WithContext(cleanupCtx).Exec(
			"SELECT pg_advisory_unlock_all()",
		).Error
		_ = sqlDB.Close()
		_ = peerSQL.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	var backendPID int
	runErr := db.WithContext(ctx).Connection(func(pinned *gorm.DB) error {
		if err := pinned.Exec(`
			SELECT
				set_config('lock_timeout', '17s', false),
				set_config('statement_timeout', '23s', false)
		`).Error; err != nil {
			return err
		}
		if err := pinned.Raw(
			"SELECT pg_backend_pid()",
		).Scan(&backendPID).Error; err != nil {
			return err
		}
		return withMigrationSessionAdvisoryLock(
			pinned,
			func(*gorm.DB) error {
				cancel()
				return ctx.Err()
			},
		)
	})
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("migration callback error = %v, want context canceled", runErr)
	}

	var acquired bool
	if err := peer.Raw(
		`SELECT pg_try_advisory_lock(hashtextextended(?, 0))`,
		chronodeskMigrationAdvisoryLockKey,
	).Scan(&acquired).Error; err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal(
			"another PostgreSQL session could not immediately acquire the migration lock",
		)
	}
	var released bool
	if err := peer.Raw(
		`SELECT pg_advisory_unlock(hashtextextended(?, 0))`,
		chronodeskMigrationAdvisoryLockKey,
	).Scan(&released).Error; err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("peer PostgreSQL session did not release its migration lock")
	}

	var lockCount int64
	if err := peer.Raw(`
		SELECT COUNT(*)
		FROM pg_locks
		WHERE pid = ?
		  AND locktype = 'advisory'
	`, backendPID).Scan(&lockCount).Error; err != nil {
		t.Fatal(err)
	}
	if lockCount != 0 {
		t.Fatalf(
			"canceled migration backend %d retained %d advisory locks",
			backendPID,
			lockCount,
		)
	}

	var restored struct {
		BackendPID       int    `gorm:"column:backend_pid"`
		LockTimeout      string `gorm:"column:lock_timeout"`
		StatementTimeout string `gorm:"column:statement_timeout"`
	}
	if err := db.Raw(`
		SELECT
			pg_backend_pid() AS backend_pid,
			current_setting('lock_timeout') AS lock_timeout,
			current_setting('statement_timeout') AS statement_timeout
	`).Scan(&restored).Error; err != nil {
		t.Fatal(err)
	}
	if restored.BackendPID != backendPID {
		t.Fatalf(
			"healthy canceled callback discarded backend %d and returned %d",
			backendPID,
			restored.BackendPID,
		)
	}
	if restored.LockTimeout != "17s" ||
		restored.StatementTimeout != "23s" {
		t.Fatalf(
			"reused PostgreSQL connection timeouts = %q/%q, want 17s/23s",
			restored.LockTimeout,
			restored.StatementTimeout,
		)
	}
}

func TestMigrationSessionCleanupFailureDiscardsPhysicalConnectionPostgres(
	t *testing.T,
) {
	rawDSN := loopbackPostgresIntegrationDSN(t)
	config := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	}
	db, err := gorm.Open(postgres.Open(rawDSN), config)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := gorm.Open(postgres.Open(rawDSN), config)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	peerSQL, err := peer.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(),
			3*time.Second,
		)
		defer cancel()
		_ = peer.WithContext(cleanupCtx).Exec(
			"SELECT pg_advisory_unlock_all()",
		).Error
		_ = sqlDB.Close()
		_ = peerSQL.Close()
	})

	var poisonedPID int
	runErr := db.Connection(func(pinned *gorm.DB) error {
		if err := pinned.Raw(
			"SELECT pg_backend_pid()",
		).Scan(&poisonedPID).Error; err != nil {
			return err
		}
		return withMigrationSessionAdvisoryLock(
			pinned,
			func(locked *gorm.DB) error {
				if err := locked.Exec("BEGIN").Error; err != nil {
					return err
				}
				return locked.Exec("SELECT 1 / 0").Error
			},
		)
	})
	if runErr == nil {
		t.Fatal("SQL-aborted migration callback unexpectedly succeeded")
	}

	var acquired bool
	if err := peer.Raw(
		`SELECT pg_try_advisory_lock(hashtextextended(?, 0))`,
		chronodeskMigrationAdvisoryLockKey,
	).Scan(&acquired).Error; err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal(
			"cleanup-failed PostgreSQL session retained the migration lock",
		)
	}
	var released bool
	if err := peer.Raw(
		`SELECT pg_advisory_unlock(hashtextextended(?, 0))`,
		chronodeskMigrationAdvisoryLockKey,
	).Scan(&released).Error; err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("peer PostgreSQL session did not release its migration lock")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var active int64
		if err := peer.Raw(`
			SELECT COUNT(*)
			FROM pg_stat_activity
			WHERE pid = ?
		`, poisonedPID).Scan(&active).Error; err != nil {
			t.Fatal(err)
		}
		if active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"cleanup-failed PostgreSQL backend %d was returned to the pool",
				poisonedPID,
			)
		}
		time.Sleep(10 * time.Millisecond)
	}

	var replacementPID int
	if err := db.Raw(
		"SELECT pg_backend_pid()",
	).Scan(&replacementPID).Error; err != nil {
		t.Fatal(err)
	}
	if replacementPID == poisonedPID {
		t.Fatalf(
			"cleanup-failed PostgreSQL backend %d was reused",
			poisonedPID,
		)
	}
}

func TestMigrationSessionCleanupAfterTimeoutPathsPostgres(t *testing.T) {
	rawDSN := loopbackPostgresIntegrationDSN(t)
	config := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	}
	db, err := gorm.Open(postgres.Open(rawDSN), config)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := gorm.Open(postgres.Open(rawDSN), config)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	peerSQL, err := peer.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() {
		_ = sqlDB.Close()
		_ = peerSQL.Close()
	})

	assertReusable := func(t *testing.T) {
		t.Helper()
		var acquired bool
		if err := peer.Raw(
			`SELECT pg_try_advisory_lock(hashtextextended(?, 0))`,
			chronodeskMigrationAdvisoryLockKey,
		).Scan(&acquired).Error; err != nil {
			t.Fatal(err)
		}
		if !acquired {
			t.Fatal("peer could not acquire migration lock after timeout")
		}
		var released bool
		if err := peer.Raw(
			`SELECT pg_advisory_unlock(hashtextextended(?, 0))`,
			chronodeskMigrationAdvisoryLockKey,
		).Scan(&released).Error; err != nil {
			t.Fatal(err)
		}
		if !released {
			t.Fatal("peer did not release migration lock after timeout")
		}
		if err := db.Connection(func(pinned *gorm.DB) error {
			return withMigrationSessionAdvisoryLock(
				pinned,
				func(*gorm.DB) error { return nil },
			)
		}); err != nil {
			t.Fatalf("retry migration session after timeout: %v", err)
		}
		var settings struct {
			LockTimeout      string `gorm:"column:lock_timeout"`
			StatementTimeout string `gorm:"column:statement_timeout"`
		}
		if err := db.Raw(`
			SELECT
				current_setting('lock_timeout') AS lock_timeout,
				current_setting('statement_timeout') AS statement_timeout
		`).Scan(&settings).Error; err != nil {
			t.Fatal(err)
		}
		if settings.LockTimeout != "0" ||
			settings.StatementTimeout != "0" {
			t.Fatalf(
				"timeout cleanup restored %q/%q, want 0/0",
				settings.LockTimeout,
				settings.StatementTimeout,
			)
		}
	}

	t.Run("statement timeout", func(t *testing.T) {
		err := db.Connection(func(pinned *gorm.DB) error {
			return withMigrationSessionAdvisoryLock(
				pinned,
				func(locked *gorm.DB) error {
					if err := locked.Exec(
						"SET statement_timeout = '50ms'",
					).Error; err != nil {
						return err
					}
					return locked.Exec("SELECT pg_sleep(0.2)").Error
				},
			)
		})
		if err == nil ||
			!strings.Contains(err.Error(), "SQLSTATE 57014") {
			t.Fatalf("statement timeout error = %v", err)
		}
		assertReusable(t)
	})

	t.Run("lock timeout", func(t *testing.T) {
		table := fmt.Sprintf(
			`"chronodesk_task9a_lock_%d"`,
			time.Now().UnixNano(),
		)
		if err := peer.Exec(
			"CREATE TABLE " + table + " (id BIGINT PRIMARY KEY)",
		).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = peer.Exec("DROP TABLE IF EXISTS " + table).Error
		})
		blocker := peer.Begin()
		if blocker.Error != nil {
			t.Fatal(blocker.Error)
		}
		if err := blocker.Exec(
			"LOCK TABLE " + table + " IN ACCESS EXCLUSIVE MODE",
		).Error; err != nil {
			_ = blocker.Rollback().Error
			t.Fatal(err)
		}
		err := db.Connection(func(pinned *gorm.DB) error {
			return withMigrationSessionAdvisoryLock(
				pinned,
				func(locked *gorm.DB) error {
					if err := locked.Exec(
						"SET lock_timeout = '50ms'",
					).Error; err != nil {
						return err
					}
					return locked.Transaction(func(tx *gorm.DB) error {
						return tx.Exec(
							"LOCK TABLE " + table +
								" IN ACCESS SHARE MODE",
						).Error
					})
				},
			)
		})
		if rollbackErr := blocker.Rollback().Error; rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		if err == nil || !strings.Contains(err.Error(), "SQLSTATE 55P03") {
			t.Fatalf("lock timeout error = %v", err)
		}
		assertReusable(t)
	})

	t.Run("migration advisory lock timeout and retry", func(t *testing.T) {
		if err := peer.Exec(
			`SELECT pg_advisory_lock(hashtextextended(?, 0))`,
			chronodeskMigrationAdvisoryLockKey,
		).Error; err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		err := db.Connection(func(pinned *gorm.DB) error {
			return withMigrationSessionAdvisoryLock(
				pinned,
				func(*gorm.DB) error {
					return errors.New(
						"migration callback unexpectedly ran while peer held lock",
					)
				},
			)
		})
		elapsed := time.Since(started)
		var released bool
		if releaseErr := peer.Raw(
			`SELECT pg_advisory_unlock(hashtextextended(?, 0))`,
			chronodeskMigrationAdvisoryLockKey,
		).Scan(&released).Error; releaseErr != nil {
			t.Fatal(releaseErr)
		}
		if !released {
			t.Fatal("peer did not release migration advisory lock")
		}
		if err == nil || !strings.Contains(err.Error(), "SQLSTATE 55P03") {
			t.Fatalf("migration advisory lock timeout error = %v", err)
		}
		if elapsed < 4*time.Second || elapsed > 8*time.Second {
			t.Fatalf(
				"migration advisory lock timeout elapsed = %s, want bounded near 5s",
				elapsed,
			)
		}
		assertReusable(t)
	})

	t.Run("caller deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			75*time.Millisecond,
		)
		defer cancel()
		err := db.WithContext(ctx).Connection(func(pinned *gorm.DB) error {
			return withMigrationSessionAdvisoryLock(
				pinned,
				func(locked *gorm.DB) error {
					return locked.Exec("SELECT pg_sleep(0.3)").Error
				},
			)
		})
		if err == nil ||
			(!errors.Is(err, context.DeadlineExceeded) &&
				!strings.Contains(
					strings.ToLower(err.Error()),
					"deadline",
				)) {
			t.Fatalf("caller deadline error = %v", err)
		}
		assertReusable(t)
	})
}

func loopbackPostgresIntegrationDSN(t *testing.T) string {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL migration session evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse PostgreSQL integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("PostgreSQL migration session test requires loopback")
		}
	}
	return rawDSN
}
