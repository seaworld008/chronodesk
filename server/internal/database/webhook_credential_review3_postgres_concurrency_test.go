package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func waitForPostgresAdvisoryLockWait(
	t *testing.T,
	db *gorm.DB,
	applicationName string,
	lockKey string,
	result <-chan error,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-result:
			t.Fatalf(
				"peer migration completed before advisory serialization was observed: %v",
				err,
			)
		default:
		}
		var waiting int64
		if err := db.Raw(`
			WITH expected AS (
				SELECT
					(
						(hashtextextended(?, 0) >> 32) &
						4294967295::bigint
					)::oid AS class_id,
					(
						hashtextextended(?, 0) &
						4294967295::bigint
					)::oid AS object_id
			)
			SELECT COUNT(*)
			FROM pg_stat_activity AS activity
			JOIN pg_locks AS lock_state
			  ON lock_state.pid = activity.pid
			CROSS JOIN expected
			WHERE activity.application_name = ?
			  AND activity.wait_event_type = 'Lock'
			  AND activity.wait_event = 'advisory'
			  AND activity.query LIKE '%pg_advisory_lock%'
			  AND lock_state.locktype = 'advisory'
			  AND NOT lock_state.granted
			  AND lock_state.classid = expected.class_id
			  AND lock_state.objid = expected.object_id
		`, lockKey, lockKey, applicationName).Scan(&waiting).Error; err != nil {
			t.Fatal(err)
		}
		if waiting == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf(
		"peer migration was not observed waiting on advisory key %q",
		lockKey,
	)
}

func runPostgresCapacityCutoverWithBlockedWriter(
	t *testing.T,
	owner *gorm.DB,
	ownerPeer *gorm.DB,
	ownerPeerApplicationName string,
	cutoverAt time.Time,
	started time.Time,
) {
	t.Helper()
	const callbackName = "test:task9a_capacity_aex_observer"
	lockAcquired := make(chan struct{})
	releaseMigration := make(chan struct{})
	var (
		lockOnce     sync.Once
		migrationPID int
	)
	if err := owner.Callback().Raw().After("gorm:raw").Register(
		callbackName,
		func(tx *gorm.DB) {
			statement := strings.ToLower(
				strings.Join(strings.Fields(tx.Statement.SQL.String()), " "),
			)
			if !strings.HasPrefix(
				statement,
				"lock table domain_events, outbox_deliveries, "+
					"webhook_delivery_snapshots in access exclusive mode",
			) {
				return
			}
			lockOnce.Do(func() {
				sqlTx, ok := tx.Statement.ConnPool.(*sql.Tx)
				if !ok {
					_ = tx.AddError(errors.New(
						"capacity AEX observer requires PostgreSQL transaction",
					))
					close(lockAcquired)
					return
				}
				if err := sqlTx.QueryRowContext(
					context.Background(),
					"SELECT pg_backend_pid()",
				).Scan(&migrationPID); err != nil {
					_ = tx.AddError(err)
				}
				close(lockAcquired)
				<-releaseMigration
			})
		},
	); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := owner.Callback().Raw().Remove(callbackName); err != nil {
			t.Errorf("remove capacity AEX observer: %v", err)
		}
	}()
	migrationResult := make(chan error, 1)
	go func() {
		migrationResult <- migrateWebhookSnapshotCredentialLifetimeContractAt(
			owner,
			cutoverAt,
		)
	}()
	select {
	case <-lockAcquired:
	case <-time.After(task9aQualificationAEXAcquireBudget):
		close(releaseMigration)
		t.Fatalf(
			"capacity cutover did not acquire AEX within %s",
			task9aQualificationAEXAcquireBudget,
		)
	}
	acquiredAt := time.Now()
	acquireDuration := acquiredAt.Sub(started)
	released := false
	defer func() {
		if !released {
			close(releaseMigration)
		}
	}()
	if migrationPID == 0 {
		close(releaseMigration)
		released = true
		t.Fatal("capacity cutover AEX observer did not capture backend PID")
	}
	writerStarted := time.Now()
	writerResult := make(chan error, 1)
	go func() {
		writerResult <- WithProjectScopeTransaction(
			context.Background(),
			ownerPeer,
			models.ProjectScope{OrganizationID: 11, ProjectID: 100},
			func(tx *gorm.DB) error {
				return tx.Exec(`
					UPDATE outbox_deliveries
					SET status = status
					WHERE id =
						'00000000-0000-7000-8000-000000400001'
				`).Error
			},
		)
	}()
	waitForPostgresCapacityWriterLock(
		t,
		owner,
		ownerPeerApplicationName,
		migrationPID,
	)
	close(releaseMigration)
	released = true
	var migrationErr error
	select {
	case migrationErr = <-migrationResult:
	case <-time.After(task9aQualificationCutoverBudget):
		t.Fatalf(
			"capacity migration did not finish within %s after AEX observation",
			task9aQualificationCutoverBudget,
		)
	}
	migrationFinished := time.Now()
	if migrationErr != nil {
		t.Fatalf("capacity migration: %v", migrationErr)
	}
	var writerErr error
	select {
	case writerErr = <-writerResult:
	case <-time.After(task9aQualificationAEXHoldBudget):
		t.Fatalf(
			"blocked writer did not finish within %s",
			task9aQualificationAEXHoldBudget,
		)
	}
	writerFinished := time.Now()
	if writerErr != nil {
		t.Fatalf("capacity blocked writer: %v", writerErr)
	}
	aexHoldDuration := migrationFinished.Sub(acquiredAt)
	writerBlockedDuration := writerFinished.Sub(writerStarted)
	if acquireDuration > task9aQualificationAEXAcquireBudget {
		t.Fatalf(
			"AEX acquisition %s exceeded %s",
			acquireDuration,
			task9aQualificationAEXAcquireBudget,
		)
	}
	if aexHoldDuration > task9aQualificationAEXHoldBudget ||
		writerBlockedDuration > task9aQualificationAEXHoldBudget {
		t.Fatalf(
			"AEX hold=%s writer block=%s exceeded %s",
			aexHoldDuration,
			writerBlockedDuration,
			task9aQualificationAEXHoldBudget,
		)
	}
	var waiting int64
	if err := owner.Raw(`
		SELECT COUNT(*)
		FROM pg_locks
		WHERE NOT granted
		  AND relation IN (
			'domain_events'::regclass,
			'outbox_deliveries'::regclass,
			'webhook_delivery_snapshots'::regclass
		  )
	`).Scan(&waiting).Error; err != nil {
		t.Fatal(err)
	}
	if waiting != 0 {
		t.Fatalf("capacity cutover retained %d waiting relation locks", waiting)
	}
	t.Logf(
		"capacity AEX timeline acquire=%s hold=%s writer_blocked=%s pid=%d",
		acquireDuration,
		aexHoldDuration,
		writerBlockedDuration,
		migrationPID,
	)
}

func waitForPostgresCapacityWriterLock(
	t *testing.T,
	db *gorm.DB,
	writerApplicationName string,
	migrationPID int,
) {
	t.Helper()
	deadline := time.Now().Add(task9aQualificationLockObservationBudget)
	for time.Now().Before(deadline) {
		var state struct {
			AEXLocks     int64 `gorm:"column:aex_locks"`
			WaitingWrite int64 `gorm:"column:waiting_write"`
		}
		if err := db.Raw(`
			SELECT
				(
					SELECT COUNT(*)
					FROM pg_locks
					WHERE pid = ?
					  AND locktype = 'relation'
					  AND mode = 'AccessExclusiveLock'
					  AND granted
					  AND relation IN (
						'domain_events'::regclass,
						'outbox_deliveries'::regclass,
						'webhook_delivery_snapshots'::regclass
					  )
				) AS aex_locks,
				(
					SELECT COUNT(*)
					FROM pg_stat_activity AS activity
					JOIN pg_locks AS lock_state
					  ON lock_state.pid = activity.pid
					WHERE activity.application_name = ?
					  AND activity.wait_event_type = 'Lock'
					  AND activity.wait_event = 'relation'
					  AND ? = ANY(pg_blocking_pids(activity.pid))
					  AND lock_state.relation =
						'outbox_deliveries'::regclass
					  AND lock_state.mode = 'RowExclusiveLock'
					  AND NOT lock_state.granted
				) AS waiting_write
		`,
			migrationPID,
			writerApplicationName,
			migrationPID,
		).Scan(&state).Error; err != nil {
			t.Fatal(err)
		}
		if state.AEXLocks == 3 && state.WaitingWrite == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf(
		"capacity writer was not observed behind three granted AEX locks",
	)
}

func waitForPostgresTestRoleSessionsToExit(
	t *testing.T,
	admin *gorm.DB,
	roles []string,
) {
	t.Helper()
	if len(roles) != 2 {
		t.Fatalf("PostgreSQL cleanup requires two exact test roles")
	}
	countSessions := func() int64 {
		var sessions int64
		if err := admin.Raw(`
			SELECT COUNT(*)
			FROM pg_stat_activity
			WHERE usename IN (?, ?)
			  AND pid <> pg_backend_pid()
		`, roles[0], roles[1]).Scan(&sessions).Error; err != nil {
			t.Errorf("inspect PostgreSQL test-role sessions: %v", err)
			return -1
		}
		return sessions
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if countSessions() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := admin.Exec(`
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE usename IN (?, ?)
		  AND pid <> pg_backend_pid()
	`, roles[0], roles[1]).Error; err != nil {
		t.Errorf("terminate exact PostgreSQL test-role sessions: %v", err)
		return
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if countSessions() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf(
		"PostgreSQL test-role sessions remained after pool close and termination",
	)
}
