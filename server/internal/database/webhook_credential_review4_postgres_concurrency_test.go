package database

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

type postgresRuntimeIsolationBarrierResult struct {
	SnapshotID   string
	Deadline     time.Time
	Validation   error
	WallDuration time.Duration
}

func runPostgresRuntimeIsolationBarrier(
	t *testing.T,
	owner *gorm.DB,
	runtimeDB *gorm.DB,
	scope models.ProjectScope,
	isolation sql.IsolationLevel,
	callbackName string,
) postgresRuntimeIsolationBarrierResult {
	t.Helper()
	validationContext, cancelValidation := context.WithTimeout(
		context.Background(),
		task9aQualificationRuntimeValidationContextBudget,
	)
	defer cancelValidation()

	var (
		inventoryOnce     sync.Once
		inventoryObserved bool
		writerErr         error
		snapshot          struct {
			ID       string    `gorm:"column:id"`
			Deadline time.Time `gorm:"column:credential_expires_at"`
		}
	)
	if err := runtimeDB.Callback().Query().After("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement == nil || tx.Statement.Table != "projects" {
				return
			}
			inventoryOnce.Do(func() {
				inventoryObserved = true
				writerContext, cancelWriter := context.WithTimeout(
					context.Background(),
					task9aQualificationRuntimeBarrierWatchdogBudget,
				)
				defer cancelWriter()
				writerErr = WithProjectScopeTransaction(
					writerContext,
					owner,
					scope,
					func(tx *gorm.DB) error {
						if err := tx.Table(
							"webhook_delivery_snapshots",
						).
							Select("id", "credential_expires_at").
							Order("id ASC").
							Take(&snapshot).Error; err != nil {
							return err
						}
						return tx.Exec(
							`UPDATE webhook_delivery_snapshots
							 SET credential_expires_at = ?
							 WHERE id = ?`,
							snapshot.Deadline.Add(2*time.Hour),
							snapshot.ID,
						).Error
					},
				)
			})
		},
	); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := runtimeDB.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove runtime isolation barrier callback: %v", err)
		}
	}()

	watchdogFired := make(chan struct{})
	watchdog := time.AfterFunc(
		task9aQualificationRuntimeBarrierWatchdogBudget,
		func() {
			close(watchdogFired)
			cancelValidation()
		},
	)
	validationStarted := time.Now()
	validationErr := validateWebhookCredentialRuntimeSnapshotWithOptions(
		validationContext,
		runtimeDB,
		&sql.TxOptions{
			Isolation: isolation,
			ReadOnly:  true,
		},
	)
	wallDuration := time.Since(validationStarted)
	watchdogStopped := watchdog.Stop()
	cancelValidation()
	if !watchdogStopped {
		<-watchdogFired
		t.Fatalf(
			"runtime isolation barrier exceeded watchdog %s and "+
				"returned synchronously with %v",
			task9aQualificationRuntimeBarrierWatchdogBudget,
			validationErr,
		)
	}
	if !inventoryObserved {
		t.Fatalf(
			"runtime validator ended before its Project inventory "+
				"barrier: %v",
			validationErr,
		)
	}
	if writerErr != nil {
		t.Fatalf("commit runtime isolation barrier mutation: %v", writerErr)
	}
	if snapshot.ID == "" || snapshot.Deadline.IsZero() {
		t.Fatal("runtime isolation barrier did not capture a snapshot")
	}
	return postgresRuntimeIsolationBarrierResult{
		SnapshotID:   snapshot.ID,
		Deadline:     snapshot.Deadline,
		Validation:   validationErr,
		WallDuration: wallDuration,
	}
}

func restorePostgresRuntimeIsolationBarrierSnapshot(
	t *testing.T,
	owner *gorm.DB,
	scope models.ProjectScope,
	result postgresRuntimeIsolationBarrierResult,
) {
	t.Helper()
	restoreContext, cancelRestore := context.WithTimeout(
		context.Background(),
		task9aQualificationRuntimeBarrierWatchdogBudget,
	)
	defer cancelRestore()
	if err := WithProjectScopeTransaction(
		restoreContext,
		owner,
		scope,
		func(tx *gorm.DB) error {
			update := tx.Exec(
				`UPDATE webhook_delivery_snapshots
				 SET credential_expires_at = ?
				 WHERE id = ?`,
				result.Deadline,
				result.SnapshotID,
			)
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return fmt.Errorf(
					"restored %d runtime barrier snapshots, want 1",
					update.RowsAffected,
				)
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
}
