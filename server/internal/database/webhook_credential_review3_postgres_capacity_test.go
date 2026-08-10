package database

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const (
	task9aQualificationProjectCeiling                 = 1_000
	task9aQualificationWebhookPairCeiling             = 100_000
	task9aQualificationNonWebhookCeiling              = 1_000_000
	task9aQualificationAEXAcquireBudget               = 5 * time.Second
	task9aQualificationAEXHoldBudget                  = 15 * time.Second
	task9aQualificationCutoverBudget                  = 20 * time.Second
	task9aQualificationRuntimeBudget                  = 10 * time.Second
	task9aQualificationStatementBudget                = 8 * time.Second
	task9aQualificationStatementTotalBudget           = 12 * time.Second
	task9aQualificationPeakRSSBudgetInBytes           = uint64(1 << 30)
	task9aQualificationLockObservationBudget          = 5 * time.Second
	task9aQualificationRuntimeBarrierWatchdogBudget   = task9aQualificationRuntimeBudget + 5*time.Second
	task9aQualificationRuntimeValidationContextBudget = 30 * time.Second
)

func TestTask9aQualificationRuntimeBarrierBudgetsIncludeSchedulerMargin(
	t *testing.T,
) {
	if task9aQualificationRuntimeBarrierWatchdogBudget <=
		task9aQualificationRuntimeBudget {
		t.Fatalf(
			"runtime barrier watchdog %s must exceed runtime budget %s",
			task9aQualificationRuntimeBarrierWatchdogBudget,
			task9aQualificationRuntimeBudget,
		)
	}
	if task9aQualificationRuntimeValidationContextBudget <=
		task9aQualificationRuntimeBarrierWatchdogBudget {
		t.Fatalf(
			"runtime validation context %s must exceed watchdog %s",
			task9aQualificationRuntimeValidationContextBudget,
			task9aQualificationRuntimeBarrierWatchdogBudget,
		)
	}
}

func qualifyPostgresWebhookProductionStatements(
	t *testing.T,
	owner *gorm.DB,
	hotScope models.ProjectScope,
	cutoverAt time.Time,
) {
	t.Helper()
	type qualification struct {
		name     string
		duration time.Duration
		plan     string
	}
	qualifications := make([]qualification, 0, 4)
	explain := func(
		tx *gorm.DB,
		name string,
		statement webhookCredentialSQLStatement,
	) error {
		var rows []struct {
			Plan string `gorm:"column:QUERY PLAN"`
		}
		started := time.Now()
		if err := tx.Raw(
			"EXPLAIN (ANALYZE, BUFFERS, WAL, TIMING OFF, FORMAT JSON) "+
				statement.query,
			statement.args...,
		).Scan(&rows).Error; err != nil {
			return err
		}
		duration := time.Since(started)
		planParts := make([]string, 0, len(rows))
		for _, row := range rows {
			planParts = append(planParts, row.Plan)
		}
		qualifications = append(qualifications, qualification{
			name:     name,
			duration: duration,
			plan:     strings.Join(planParts, " "),
		})
		return nil
	}
	rollback := errors.New("rollback capacity qualification fixture")
	deadline := cutoverAt.Add(models.WebhookDeliveryCredentialLifetime)
	err := withWebhookCredentialOwnerAccess(owner, func(tx *gorm.DB) error {
		for _, statement := range []string{
			`ALTER TABLE webhook_delivery_snapshots
			 ALTER COLUMN credential_expires_at DROP NOT NULL`,
			`ALTER TABLE outbox_deliveries
			 DROP CONSTRAINT chk_outbox_webhook_expires_at`,
			`UPDATE outbox_deliveries
			 SET expires_at = NULL
			 WHERE destination_type = 'webhook'`,
			`UPDATE webhook_delivery_snapshots
			 SET credential_expires_at = NULL`,
		} {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		if err := explain(
			tx,
			"owner_legacy_violation",
			buildWebhookCredentialViolationStatement(
				"postgres",
				nil,
				false,
				false,
			),
		); err != nil {
			return err
		}
		if err := explain(
			tx,
			"delivery_backfill",
			buildWebhookDeliveryDeadlineBackfillStatement(deadline),
		); err != nil {
			return err
		}
		if err := explain(
			tx,
			"snapshot_backfill",
			buildWebhookSnapshotDeadlineBackfillStatement(deadline),
		); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("qualify owner/backfill production statements: %v", err)
	}
	if err := WithProjectScopeTransaction(
		context.Background(),
		owner,
		hotScope,
		func(tx *gorm.DB) error {
			return explain(
				tx,
				"runtime_scoped_violation",
				buildWebhookCredentialViolationStatement(
					"postgres",
					&hotScope,
					true,
					false,
				),
			)
		},
	); err != nil {
		t.Fatal(err)
	}
	var total time.Duration
	for _, result := range qualifications {
		total += result.duration
		if result.duration > task9aQualificationStatementBudget {
			t.Fatalf(
				"production statement %s took %s, budget %s",
				result.name,
				result.duration,
				task9aQualificationStatementBudget,
			)
		}
		plan := result.plan
		if len(plan) > 4_000 {
			plan = plan[:4_000] + " …[truncated]"
		}
		t.Logf(
			"capacity production EXPLAIN %s duration=%s plan=%s",
			result.name,
			result.duration,
			plan,
		)
	}
	if len(qualifications) != 4 {
		t.Fatalf(
			"qualified %d production statements, want 4",
			len(qualifications),
		)
	}
	if total > task9aQualificationStatementTotalBudget {
		t.Fatalf(
			"production statements total %s exceeded %s",
			total,
			task9aQualificationStatementTotalBudget,
		)
	}
	var counts struct {
		Projects    int64 `gorm:"column:projects"`
		Webhooks    int64 `gorm:"column:webhooks"`
		NonWebhooks int64 `gorm:"column:non_webhooks"`
	}
	if err := withWebhookCredentialOwnerAccess(owner, func(tx *gorm.DB) error {
		return tx.Raw(`
			SELECT
				(
					SELECT COUNT(*) FROM projects
					WHERE organization_id = 11
					  AND id >= 100 AND id < 1100
				) AS projects,
				(
					SELECT COUNT(*) FROM outbox_deliveries
					WHERE organization_id = 11
					  AND project_id >= 100 AND project_id < 1100
					  AND destination_type = 'webhook'
				) AS webhooks,
				(
					SELECT COUNT(*) FROM outbox_deliveries
					WHERE destination_type = 'test_delivery'
				) AS non_webhooks
		`).Scan(&counts).Error
	}); err != nil {
		t.Fatal(err)
	}
	if counts.Projects != task9aQualificationProjectCeiling ||
		counts.Webhooks != task9aQualificationWebhookPairCeiling ||
		counts.NonWebhooks != task9aQualificationNonWebhookCeiling {
		t.Fatalf(
			"capacity support ceiling counts=%+v, want %d/%d/%d",
			counts,
			task9aQualificationProjectCeiling,
			task9aQualificationWebhookPairCeiling,
			task9aQualificationNonWebhookCeiling,
		)
	}
	peakRSS := postgresQualificationPeakRSSBytes(t)
	if peakRSS > task9aQualificationPeakRSSBudgetInBytes {
		t.Fatalf(
			"capacity peak RSS %d exceeded %d bytes",
			peakRSS,
			task9aQualificationPeakRSSBudgetInBytes,
		)
	}
	var (
		postgresVersion string
		waitingLocks    int64
	)
	if err := owner.Raw("SHOW server_version").
		Scan(&postgresVersion).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Raw(`
		SELECT COUNT(*)
		FROM pg_locks
		WHERE NOT granted
		  AND relation IN (
			'domain_events'::regclass,
			'outbox_deliveries'::regclass,
			'webhook_delivery_snapshots'::regclass
		  )
	`).Scan(&waitingLocks).Error; err != nil {
		t.Fatal(err)
	}
	if waitingLocks != 0 {
		t.Fatalf("capacity tail retained %d waiting locks", waitingLocks)
	}
	t.Logf(
		"capacity qualification PostgreSQL=%s support=%d/%d/%d "+
			"statement_total=%s peak_rss_bytes=%d waiting_locks=%d",
		postgresVersion,
		counts.Projects,
		counts.Webhooks,
		counts.NonWebhooks,
		total,
		peakRSS,
		waitingLocks,
	)
}

func postgresQualificationPeakRSSBytes(t *testing.T) uint64 {
	t.Helper()
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	peak := uint64(usage.Maxrss)
	if runtime.GOOS == "linux" {
		peak *= 1024
	}
	return peak
}
