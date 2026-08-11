package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/database/webhookdispatch"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestWebhookOutboxLifecyclePostgresConcurrencyMatrix(t *testing.T) {
	fixture := newWebhookOutboxLifecyclePostgresFixture(t)

	t.Run(
		"emergency command revalidates Human membership under runtime RLS and ACL",
		func(t *testing.T) {
			fixture.clearRows(t)
			deadline := fixture.now.Add(time.Hour)
			active := fixture.seedPair(
				t,
				fixture.projectA,
				models.OutboxDeliveryPending,
				deadline,
				"",
				nil,
				0,
			)
			deleted := fixture.seedSiblingPair(
				t,
				fixture.projectA,
				active.event,
				models.OutboxDeliveryPending,
				deadline,
				"",
				nil,
				0,
			)
			publishedAt := fixture.now.Add(-time.Hour)
			if err := fixture.adminScoped.Model(&models.DomainEvent{}).
				Where("id = ?", active.event.ID).
				Update("published_at", publishedAt).Error; err != nil {
				t.Fatal(err)
			}
			humanContext, err := WithOperationContext(
				context.Background(),
				OperationContext{
					Scope: fixture.projectA.Scope(),
					Actor: models.HumanActor(
						postgresLifecycleEmergencyUserID,
					),
					Source: SourceProtocolHumanREST,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			service := fixture.service(
				fixture.runtimeA,
				fixture.now.Add(time.Second),
			)

			membership := fixture.adminScoped.Model(
				&models.ProjectMembership{},
			).Where(
				"project_id = ? AND user_id = ?",
				fixture.projectA.ID,
				postgresLifecycleEmergencyUserID,
			)
			if err := membership.Update("is_active", false).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := service.EmergencyRevokeWebhook(
				humanContext,
				postgresLifecycleConfigAActiveID,
			); !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf(
					"inactive membership emergency revoke error = %v",
					err,
				)
			}
			if current := fixture.loadDelivery(
				t,
				active.delivery.ID,
			); current.Status != models.OutboxDeliveryPending {
				t.Fatalf(
					"denied Human revalidation changed delivery: %+v",
					current,
				)
			}
			if err := fixture.adminScoped.Model(
				&models.ProjectMembership{},
			).Where(
				"project_id = ? AND user_id = ?",
				fixture.projectA.ID,
				postgresLifecycleEmergencyUserID,
			).Update("is_active", true).Error; err != nil {
				t.Fatal(err)
			}

			if _, err := service.EmergencyRevokeWebhook(
				humanContext,
				postgresLifecycleConfigBActiveID,
			); !errors.Is(err, ErrWebhookConfigNotFound) {
				t.Fatalf("cross-project emergency revoke error = %v", err)
			}
			var foreign models.WebhookConfig
			if err := fixture.adminScoped.Unscoped().First(
				&foreign,
				postgresLifecycleConfigBActiveID,
			).Error; err != nil {
				t.Fatal(err)
			}
			if foreign.Status != models.WebhookStatusActive {
				t.Fatalf(
					"cross-project revoke changed foreign config: %+v",
					foreign,
				)
			}

			activeResult, err := service.EmergencyRevokeWebhook(
				humanContext,
				postgresLifecycleConfigAActiveID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if activeResult.ExpiredDeliveries != 1 ||
				activeResult.ShreddedSnapshots != 1 ||
				activeResult.InFlightDeliveries != 0 {
				t.Fatalf("active runtime revoke = %+v", activeResult)
			}
			deletedResult, err := service.EmergencyRevokeWebhook(
				humanContext,
				postgresLifecycleConfigADeletedID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if deletedResult.ExpiredDeliveries != 1 ||
				deletedResult.ShreddedSnapshots != 1 ||
				deletedResult.InFlightDeliveries != 0 {
				t.Fatalf(
					"soft-deleted runtime revoke = %+v",
					deletedResult,
				)
			}
			for _, pair := range []postgresLifecyclePair{
				active,
				deleted,
			} {
				delivery := fixture.loadDelivery(t, pair.delivery.ID)
				snapshot := fixture.loadSnapshot(t, pair.snapshot.ID)
				if delivery.Status != models.OutboxDeliveryExpired ||
					snapshot.CredentialShreddedAt == nil ||
					snapshot.CredentialShredReason == nil ||
					*snapshot.CredentialShredReason !=
						models.WebhookCredentialShredReasonRevoked ||
					snapshot.Secret != "" ||
					snapshot.PreviousSecret != "" ||
					snapshot.PreviousSecretExpiresAt != nil ||
					snapshot.AccessToken != "" {
					t.Fatalf(
						"runtime emergency envelope delivery=%+v snapshot=%+v",
						delivery,
						snapshot,
					)
				}
			}
			var activeConfig, deletedConfig models.WebhookConfig
			if err := fixture.adminScoped.Unscoped().First(
				&activeConfig,
				postgresLifecycleConfigAActiveID,
			).Error; err != nil {
				t.Fatal(err)
			}
			if err := fixture.adminScoped.Unscoped().First(
				&deletedConfig,
				postgresLifecycleConfigADeletedID,
			).Error; err != nil {
				t.Fatal(err)
			}
			if activeConfig.Status != models.WebhookStatusDisabled ||
				activeConfig.Secret != "" ||
				activeConfig.PreviousSecret != "" ||
				activeConfig.PreviousSecretExpiresAt != nil ||
				activeConfig.AccessToken != "" ||
				activeConfig.UpdatedBy == nil ||
				*activeConfig.UpdatedBy !=
					postgresLifecycleEmergencyUserID ||
				!deletedConfig.DeletedAt.Valid ||
				deletedConfig.Status != models.WebhookStatusDisabled ||
				deletedConfig.Secret != "" ||
				deletedConfig.PreviousSecret != "" ||
				deletedConfig.PreviousSecretExpiresAt != nil ||
				deletedConfig.AccessToken != "" ||
				deletedConfig.UpdatedBy == nil ||
				*deletedConfig.UpdatedBy !=
					postgresLifecycleEmergencyUserID {
				t.Fatalf(
					"runtime config state active=%+v deleted=%+v",
					activeConfig,
					deletedConfig,
				)
			}
			event := fixture.loadEvent(t, active.event.ID)
			if event.PublishedAt == nil ||
				!event.PublishedAt.Equal(publishedAt) {
				t.Fatalf(
					"runtime emergency rewrote publication history: %v",
					event.PublishedAt,
				)
			}
		},
	)

	t.Run("active and soft-deleted config lock anchors", func(t *testing.T) {
		ctx := fixture.workerContext(
			t,
			context.Background(),
			fixture.projectA,
		)
		var active, deleted *models.WebhookConfig
		if err := scopeddb.WithProjectScopeContextTransaction(
			ctx,
			fixture.runtimeA,
			fixture.projectA.Scope(),
			func(scopedContext context.Context) error {
				return transactionForContext(
					scopedContext,
					fixture.runtimeA,
					func(tx *gorm.DB) error {
						var err error
						active, err = lockWebhookConfigByID(
							tx,
							fixture.projectA.Scope(),
							postgresLifecycleConfigAActiveID,
							"SHARE",
						)
						if err != nil {
							return err
						}
						deleted, err = lockWebhookConfigByID(
							tx,
							fixture.projectA.Scope(),
							postgresLifecycleConfigADeletedID,
							"SHARE",
						)
						return err
					},
				)
			},
		); err != nil {
			t.Fatal(err)
		}
		if active == nil || active.DeletedAt.Valid ||
			deleted == nil || !deleted.DeletedAt.Valid {
			t.Fatalf(
				"config lock anchors active=%+v deleted=%+v",
				active,
				deleted,
			)
		}
	})

	t.Run("claim versus cleanup", func(t *testing.T) {
		fixture.clearRows(t)
		deadline := fixture.now.Add(time.Minute)
		pair := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryPending,
			deadline,
			"",
			nil,
			0,
		)
		race := fixture.newSnapshotRace(t, pair.snapshot.ID)
		claimService := fixture.service(
			fixture.runtimeA,
			deadline.Add(-time.Second),
		)
		cleanupService := fixture.service(
			fixture.runtimeB,
			deadline.Add(time.Second),
		)
		claimDone := make(chan struct {
			rows []*models.OutboxDelivery
			err  error
		}, 1)
		cleanupDone := make(chan struct {
			result WebhookOutboxCleanupResult
			err    error
		}, 1)
		claimContext := fixture.workerContext(
			t,
			race.ctx,
			fixture.projectA,
		)
		race.run(func(ctx context.Context) {
			result, err := cleanupService.ExpireWebhookDeliveriesBatch(
				ctx,
				1,
			)
			cleanupDone <- struct {
				result WebhookOutboxCleanupResult
				err    error
			}{result: result, err: err}
		})
		fixture.waitForRuntimeBlockedBy(
			t,
			fixture.runtimeBPID,
			fixture.blockerPID,
		)
		race.run(func(context.Context) {
			rows, err := claimService.ClaimPendingOutbox(
				claimContext,
				"postgres-claim-cleanup-worker",
				1,
				time.Minute,
			)
			claimDone <- struct {
				rows []*models.OutboxDelivery
				err  error
			}{rows: rows, err: err}
		})
		fixture.waitForRuntimeBlockedBy(
			t,
			fixture.runtimeAPID,
			fixture.runtimeBPID,
		)
		race.releaseGate(t)
		claim := receivePostgresClaimResult(t, claimDone)
		cleanup := receivePostgresCleanupResult(t, cleanupDone)
		race.join()
		if claim.err != nil || cleanup.err != nil {
			t.Fatalf("claim=%v cleanup=%v", claim.err, cleanup.err)
		}
		if len(claim.rows) != 0 ||
			cleanup.result.Attempted != 1 ||
			cleanup.result.Expired != 1 {
			t.Fatalf(
				"direct claim/cleanup chain rows=%d cleanup=%+v",
				len(claim.rows),
				cleanup.result,
			)
		}
		fixture.assertExpiredEnvelope(t, pair)
	})

	t.Run("success finalize versus cleanup", func(t *testing.T) {
		fixture.clearRows(t)
		deadline := fixture.now.Add(time.Minute)
		lockAt := deadline.Add(-time.Second)
		pair := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryProcessing,
			deadline,
			"postgres-success-worker",
			&lockAt,
			1,
		)
		race := fixture.newSnapshotRace(t, pair.snapshot.ID)
		finalizeService := fixture.service(
			fixture.runtimeA,
			deadline.Add(time.Minute),
		)
		cleanupService := fixture.service(
			fixture.runtimeB,
			deadline.Add(2*time.Minute),
		)
		finalizeDone := make(chan error, 1)
		cleanupDone := make(chan struct {
			result WebhookOutboxCleanupResult
			err    error
		}, 1)
		finalizeContext := fixture.workerContext(
			t,
			race.ctx,
			fixture.projectA,
		)
		claimRef := mustPostgresClaimRef(t, pair.delivery)
		race.run(func(context.Context) {
			_, err := finalizeService.FinalizeOutboxAttempt(
				finalizeContext,
				claimRef,
				OutboxKnownSuccess(deadline.Add(-time.Millisecond)),
			)
			finalizeDone <- err
		})
		fixture.waitForRuntimeBlockedBy(
			t,
			fixture.runtimeAPID,
			fixture.blockerPID,
		)
		race.run(func(ctx context.Context) {
			result, err := cleanupService.ExpireWebhookDeliveriesBatch(
				ctx,
				1,
			)
			cleanupDone <- struct {
				result WebhookOutboxCleanupResult
				err    error
			}{result: result, err: err}
		})
		cleanup := receivePostgresCleanupResult(t, cleanupDone)
		if cleanup.err != nil ||
			cleanup.result.Attempted != 1 ||
			cleanup.result.Expired != 0 {
			t.Fatalf(
				"success holder cleanup skip result=%+v err=%v",
				cleanup.result,
				cleanup.err,
			)
		}
		select {
		case err := <-finalizeDone:
			t.Fatalf("success finalize escaped snapshot gate: %v", err)
		default:
		}
		race.releaseGate(t)
		finalizeErr := receivePostgresError(t, finalizeDone)
		race.join()
		if finalizeErr != nil {
			t.Fatal(finalizeErr)
		}
		fixture.assertSucceededEnvelope(t, pair)
	})

	t.Run("failure finalize versus cleanup", func(t *testing.T) {
		fixture.clearRows(t)
		deadline := fixture.now.Add(time.Minute)
		lockAt := deadline.Add(-time.Minute)
		pair := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryProcessing,
			deadline,
			"postgres-failure-worker",
			&lockAt,
			1,
		)
		race := fixture.newSnapshotRace(t, pair.snapshot.ID)
		finalizeService := fixture.service(
			fixture.runtimeA,
			deadline.Add(-30*time.Second),
		)
		cleanupService := fixture.service(
			fixture.runtimeB,
			deadline.Add(2*time.Minute),
		)
		finalizeDone := make(chan error, 1)
		cleanupDone := make(chan struct {
			result WebhookOutboxCleanupResult
			err    error
		}, 1)
		finalizeContext := fixture.workerContext(
			t,
			race.ctx,
			fixture.projectA,
		)
		claimRef := mustPostgresClaimRef(t, pair.delivery)
		race.run(func(context.Context) {
			_, err := finalizeService.FinalizeOutboxAttempt(
				finalizeContext,
				claimRef,
				OutboxKnownFailure(errors.New("safe rejection")),
			)
			finalizeDone <- err
		})
		fixture.waitForRuntimeBlockedBy(
			t,
			fixture.runtimeAPID,
			fixture.blockerPID,
		)
		race.run(func(ctx context.Context) {
			result, err := cleanupService.ExpireWebhookDeliveriesBatch(
				ctx,
				1,
			)
			cleanupDone <- struct {
				result WebhookOutboxCleanupResult
				err    error
			}{result: result, err: err}
		})
		cleanup := receivePostgresCleanupResult(t, cleanupDone)
		if cleanup.err != nil ||
			cleanup.result.Attempted != 1 ||
			cleanup.result.Expired != 0 {
			t.Fatalf(
				"failure holder cleanup skip result=%+v err=%v",
				cleanup.result,
				cleanup.err,
			)
		}
		select {
		case err := <-finalizeDone:
			t.Fatalf("failure finalize escaped snapshot gate: %v", err)
		default:
		}
		race.releaseGate(t)
		finalizeErr := receivePostgresError(t, finalizeDone)
		race.join()
		if finalizeErr != nil {
			t.Fatal(finalizeErr)
		}
		fixture.assertLiveEnvelope(
			t,
			pair,
			models.OutboxDeliveryFailed,
		)
	})

	t.Run("cleanup versus cleanup", func(t *testing.T) {
		fixture.clearRows(t)
		deadline := fixture.now.Add(-time.Second)
		pair := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryPending,
			deadline,
			"",
			nil,
			0,
		)
		race := fixture.newSnapshotRace(t, pair.snapshot.ID)
		firstService := fixture.service(fixture.runtimeA, fixture.now)
		secondService := fixture.service(fixture.runtimeB, fixture.now)
		firstDone := make(chan struct {
			result WebhookOutboxCleanupResult
			err    error
		}, 1)
		secondDone := make(chan struct {
			result WebhookOutboxCleanupResult
			err    error
		}, 1)
		race.run(func(ctx context.Context) {
			result, err := firstService.ExpireWebhookDeliveriesBatch(
				ctx,
				1,
			)
			firstDone <- struct {
				result WebhookOutboxCleanupResult
				err    error
			}{result: result, err: err}
		})
		fixture.waitForRuntimeBlockedBy(
			t,
			fixture.runtimeAPID,
			fixture.blockerPID,
		)
		race.run(func(ctx context.Context) {
			result, err := secondService.ExpireWebhookDeliveriesBatch(
				ctx,
				1,
			)
			secondDone <- struct {
				result WebhookOutboxCleanupResult
				err    error
			}{result: result, err: err}
		})
		second := receivePostgresCleanupResult(t, secondDone)
		if second.err != nil ||
			second.result.Attempted != 1 ||
			second.result.Expired != 0 {
			t.Fatalf(
				"second cleanup did not skip runtime-held candidate: %+v err=%v",
				second.result,
				second.err,
			)
		}
		race.releaseGate(t)
		first := receivePostgresCleanupResult(t, firstDone)
		race.join()
		if first.err != nil || second.err != nil {
			t.Fatalf("cleanup errors first=%v second=%v", first.err, second.err)
		}
		if first.result.Attempted != 1 ||
			first.result.Expired != 1 ||
			first.result.Expired+second.result.Expired != 1 {
			t.Fatalf(
				"duplicate cleanup terminalization first=%+v second=%+v",
				first.result,
				second.result,
			)
		}
		if current := fixture.loadDelivery(
			t,
			pair.delivery.ID,
		); current.Status != models.OutboxDeliveryExpired {
			t.Fatalf("cleanup race status = %s", current.Status)
		}
		fixture.assertExpiredEnvelope(t, pair)
	})

	t.Run("skip locked prefix advances to tail and wraps", func(t *testing.T) {
		fixture.clearRows(t)
		first := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryPending,
			fixture.now.Add(-3*time.Second),
			"",
			nil,
			0,
		)
		second := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryPending,
			fixture.now.Add(-2*time.Second),
			"",
			nil,
			0,
		)
		tail := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryPending,
			fixture.now.Add(-time.Second),
			"",
			nil,
			0,
		)
		lockTx := fixture.adminScoped.Begin()
		if lockTx.Error != nil {
			t.Fatal(lockTx.Error)
		}
		released := false
		t.Cleanup(func() {
			if !released {
				_ = lockTx.Rollback().Error
			}
		})
		if err := lockTx.Exec(
			`SELECT 1
			 FROM outbox_deliveries
			 WHERE id IN ?
			 ORDER BY expires_at, id
			 FOR UPDATE`,
			[]string{first.delivery.ID, second.delivery.ID},
		).Error; err != nil {
			t.Fatal(err)
		}
		service := fixture.service(fixture.runtimeA, fixture.now)
		firstPass, err := service.ExpireWebhookDeliveriesBatch(
			context.Background(),
			4,
		)
		if err != nil {
			t.Fatal(err)
		}
		if firstPass.Attempted != 2 || firstPass.Expired != 0 {
			t.Fatalf("locked prefix first pass = %+v", firstPass)
		}
		tailPass := WebhookOutboxCleanupResult{}
		for pass := 0; pass < 3 &&
			fixture.loadDelivery(
				t,
				tail.delivery.ID,
			).Status != models.OutboxDeliveryExpired; pass++ {
			next, cleanupErr := service.ExpireWebhookDeliveriesBatch(
				context.Background(),
				4,
			)
			if cleanupErr != nil {
				t.Fatal(cleanupErr)
			}
			tailPass.Attempted += next.Attempted
			tailPass.Expired += next.Expired
			tailPass.OverlapCleared += next.OverlapCleared
			tailPass.LegacySucceededShredded +=
				next.LegacySucceededShredded
			tailPass.Malformed += next.Malformed
		}
		if tailPass.Expired != 1 ||
			fixture.loadDelivery(
				t,
				tail.delivery.ID,
			).Status != models.OutboxDeliveryExpired {
			t.Fatalf(
				"locked prefix blocked tail: pass=%+v tail=%+v",
				tailPass,
				fixture.loadDelivery(t, tail.delivery.ID),
			)
		}
		if err := lockTx.Commit().Error; err != nil {
			t.Fatal(err)
		}
		released = true
		wrapped := WebhookOutboxCleanupResult{}
		for pass := 0; pass < 3 && wrapped.Expired < 2; pass++ {
			next, cleanupErr := service.ExpireWebhookDeliveriesBatch(
				context.Background(),
				4,
			)
			if cleanupErr != nil {
				t.Fatal(cleanupErr)
			}
			wrapped.Attempted += next.Attempted
			wrapped.Expired += next.Expired
			wrapped.OverlapCleared += next.OverlapCleared
			wrapped.LegacySucceededShredded +=
				next.LegacySucceededShredded
			wrapped.Malformed += next.Malformed
		}
		if wrapped.Expired != 2 {
			t.Fatalf("cleanup did not wrap to released prefix: %+v", wrapped)
		}
		fixture.assertExpiredEnvelope(t, first)
		fixture.assertExpiredEnvelope(t, second)
		fixture.assertExpiredEnvelope(t, tail)
	})

	t.Run("cleanup candidate versus replay CAS", func(t *testing.T) {
		fixture.clearRows(t)
		deadline := fixture.now.Add(-time.Second)
		pair := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryFailed,
			deadline,
			"",
			nil,
			2,
		)
		race := fixture.newSnapshotRace(t, pair.snapshot.ID)
		replayService := fixture.service(fixture.runtimeA, fixture.now)
		cleanupService := fixture.service(fixture.runtimeB, fixture.now)
		replayDone := make(chan error, 1)
		cleanupDone := make(chan struct {
			result WebhookOutboxCleanupResult
			err    error
		}, 1)
		replayContext := fixture.workerContext(
			t,
			race.ctx,
			fixture.projectA,
		)
		race.run(func(ctx context.Context) {
			result, err := cleanupService.ExpireWebhookDeliveriesBatch(
				ctx,
				1,
			)
			cleanupDone <- struct {
				result WebhookOutboxCleanupResult
				err    error
			}{result: result, err: err}
		})
		fixture.waitForRuntimeBlockedBy(
			t,
			fixture.runtimeBPID,
			fixture.blockerPID,
		)
		race.run(func(context.Context) {
			replayDone <- scopeddb.WithProjectScopeContextTransaction(
				replayContext,
				fixture.runtimeA,
				fixture.projectA.Scope(),
				func(scopedCtx context.Context) error {
					return replayService.ReplayOutbox(
						scopedCtx,
						pair.delivery.ID,
					)
				},
			)
		})
		fixture.waitForReplayCleanupLockChain(t)
		race.releaseGate(t)
		replayErr := receivePostgresError(t, replayDone)
		cleanup := receivePostgresCleanupResult(t, cleanupDone)
		race.join()
		if !errors.Is(replayErr, ErrOutboxReplayExpired) {
			t.Fatalf("replay race error = %v, want expired", replayErr)
		}
		if cleanup.err != nil {
			t.Fatal(cleanup.err)
		}
		if cleanup.result.Attempted != 1 ||
			cleanup.result.Expired != 1 ||
			cleanup.result.Malformed != 0 {
			t.Fatalf("cleanup race result = %+v", cleanup.result)
		}
		current := fixture.loadDelivery(t, pair.delivery.ID)
		if current.Status != models.OutboxDeliveryExpired ||
			current.LockedAt != nil ||
			current.LockedBy != "" ||
			current.LockToken != nil {
			t.Fatalf(
				"replay/cleanup race resurrected delivery=%+v replay=%v cleanup=%+v",
				current,
				replayErr,
				cleanup.result,
			)
		}
		fixture.assertExpiredEnvelope(t, pair)
	})

	t.Run("double success publishes event atomically", func(t *testing.T) {
		fixture.clearRows(t)
		deadline := fixture.now.Add(time.Minute)
		lockAt := fixture.now.Add(-time.Second)
		first := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryProcessing,
			deadline,
			"postgres-double-success-a",
			&lockAt,
			1,
		)
		second := fixture.seedSiblingPair(
			t,
			fixture.projectA,
			first.event,
			models.OutboxDeliveryProcessing,
			deadline,
			"postgres-double-success-b",
			&lockAt,
			1,
		)
		race := fixture.newEventRace(t, first.event.ID)
		firstService := fixture.service(fixture.runtimeA, fixture.now)
		secondService := fixture.service(fixture.runtimeB, fixture.now)
		firstContext := fixture.workerContext(
			t,
			race.ctx,
			fixture.projectA,
		)
		secondContext := fixture.workerContext(
			t,
			race.ctx,
			fixture.projectA,
		)
		firstClaim := mustPostgresClaimRef(t, first.delivery)
		secondClaim := mustPostgresClaimRef(t, second.delivery)
		firstDone := make(chan struct {
			result OutboxFinalizeResult
			err    error
		}, 1)
		secondDone := make(chan struct {
			result OutboxFinalizeResult
			err    error
		}, 1)
		race.run(func(context.Context) {
			result, err := firstService.FinalizeOutboxAttempt(
				firstContext,
				firstClaim,
				OutboxKnownSuccess(fixture.now),
			)
			firstDone <- struct {
				result OutboxFinalizeResult
				err    error
			}{result: result, err: err}
		})
		race.run(func(context.Context) {
			result, err := secondService.FinalizeOutboxAttempt(
				secondContext,
				secondClaim,
				OutboxKnownSuccess(fixture.now),
			)
			secondDone <- struct {
				result OutboxFinalizeResult
				err    error
			}{result: result, err: err}
		})
		fixture.waitForEventRuntimeLockChain(t)
		race.releaseGate(t)
		firstResult := receivePostgresFinalizeResult(t, firstDone)
		secondResult := receivePostgresFinalizeResult(t, secondDone)
		race.join()
		if firstResult.err != nil || secondResult.err != nil ||
			firstResult.result.Status != models.OutboxDeliverySucceeded ||
			secondResult.result.Status != models.OutboxDeliverySucceeded {
			t.Fatalf(
				"double success finalize first=%+v second=%+v",
				firstResult,
				secondResult,
			)
		}
		fixture.assertSucceededEnvelope(t, first)
		fixture.assertSucceededEnvelope(t, second)
		event := fixture.loadEvent(t, first.event.ID)
		if event.PublishedAt == nil {
			t.Fatal("double-success event remained unpublished")
		}
		var counts struct {
			Total      int64 `gorm:"column:total"`
			Incomplete int64 `gorm:"column:incomplete"`
		}
		if err := fixture.adminScoped.Raw(`
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE status <> ?) AS incomplete
			FROM outbox_deliveries
			WHERE event_id = ?
			  AND organization_id = ?
			  AND project_id = ?
		`,
			models.OutboxDeliverySucceeded,
			first.event.ID,
			fixture.projectA.OrganizationID,
			fixture.projectA.ID,
		).Scan(&counts).Error; err != nil {
			t.Fatal(err)
		}
		if counts.Total != 2 || counts.Incomplete != 0 {
			t.Fatalf("double-success delivery counts = %+v", counts)
		}
	})

	t.Run("stale recovery and valid lock", func(t *testing.T) {
		fixture.clearRows(t)
		deadline := fixture.now.Add(-time.Second)
		validLock := fixture.now.Add(-30 * time.Second)
		overlapExpiresAt := fixture.now.Add(time.Hour)
		valid := fixture.seedPairWithSnapshotSetup(
			t,
			fixture.projectA,
			models.OutboxDeliveryPending,
			deadline,
			"",
			nil,
			0,
			func(snapshot *models.WebhookDeliverySnapshot) {
				snapshot.PreviousSecretExpiresAt = &overlapExpiresAt
			},
		)
		token, err := uuid.NewV7()
		if err != nil {
			t.Fatal(err)
		}
		tokenValue := token.String()
		if err := fixture.adminScoped.Model(&models.OutboxDelivery{}).
			Where("id = ?", valid.delivery.ID).
			Updates(map[string]any{
				"status":              models.OutboxDeliveryProcessing,
				"attempts":            1,
				"locked_at":           validLock,
				"locked_by":           "postgres-valid-worker",
				"lock_token":          tokenValue,
				"dispatch_started_at": validLock,
			}).Error; err != nil {
			t.Fatal(err)
		}
		service := fixture.service(fixture.runtimeA, fixture.now)
		result, err := service.ExpireWebhookDeliveriesBatch(
			context.Background(),
			1,
		)
		if err != nil {
			t.Fatal(err)
		}
		if result.Expired != 0 ||
			fixture.loadDelivery(
				t,
				valid.delivery.ID,
			).Status != models.OutboxDeliveryProcessing {
			t.Fatalf("valid lock changed: %+v", result)
		}
		fixture.assertLiveEnvelope(
			t,
			valid,
			models.OutboxDeliveryProcessing,
		)
		staleService := fixture.service(
			fixture.runtimeA,
			fixture.now.Add(2*time.Minute),
		)
		result = WebhookOutboxCleanupResult{}
		for attempt := 0; attempt < 2 && result.Expired == 0; attempt++ {
			next, cleanupErr := staleService.ExpireWebhookDeliveriesBatch(
				context.Background(),
				1,
			)
			if cleanupErr != nil {
				t.Fatal(cleanupErr)
			}
			result.Attempted += next.Attempted
			result.Expired += next.Expired
			result.OverlapCleared += next.OverlapCleared
			result.LegacySucceededShredded +=
				next.LegacySucceededShredded
			result.Malformed += next.Malformed
		}
		if result.Expired != 1 ||
			fixture.loadDelivery(
				t,
				valid.delivery.ID,
			).Status != models.OutboxDeliveryExpired {
			t.Fatalf("stale lock was not recovered: %+v", result)
		}
		fixture.assertExpiredEnvelope(t, valid)
	})

	t.Run("snapshot failure rolls back terminal state", func(t *testing.T) {
		fixture.clearRows(t)
		deadline := fixture.now.Add(time.Minute)
		lockAt := fixture.now
		pair := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryProcessing,
			deadline,
			"postgres-shred-failure-worker",
			&lockAt,
			1,
		)
		fixture.installShredFailureTrigger(t)
		service := fixture.service(fixture.runtimeA, fixture.now)
		_, err := service.FinalizeOutboxAttempt(
			fixture.workerContext(
				t,
				context.Background(),
				fixture.projectA,
			),
			mustPostgresClaimRef(t, pair.delivery),
			OutboxKnownSuccess(fixture.now),
		)
		if err == nil {
			t.Fatal("injected PostgreSQL shred failure was ignored")
		}
		current := fixture.loadDelivery(t, pair.delivery.ID)
		snapshot := fixture.loadSnapshot(t, pair.snapshot.ID)
		if current.Status != models.OutboxDeliveryProcessing ||
			current.DeliveredAt != nil ||
			snapshot.CredentialShreddedAt != nil {
			t.Fatalf(
				"shred failure partially committed delivery=%+v snapshot=%+v",
				current,
				snapshot,
			)
		}
		fixture.assertLiveEnvelope(
			t,
			pair,
			models.OutboxDeliveryProcessing,
		)
	})

	t.Run("cleanup shred failure rolls back expiry", func(t *testing.T) {
		fixture.clearRows(t)
		deadline := fixture.now.Add(-time.Second)
		pair := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryPending,
			deadline,
			"",
			nil,
			0,
		)
		fixture.installShredFailureTrigger(t)
		service := fixture.service(fixture.runtimeA, fixture.now)
		result, err := service.ExpireWebhookDeliveriesBatch(
			context.Background(),
			1,
		)
		if err == nil {
			t.Fatal("injected cleanup shred failure was ignored")
		}
		if result.Attempted != 1 ||
			result.Expired != 0 ||
			result.Malformed != 1 {
			t.Fatalf("cleanup rollback result = %+v", result)
		}
		fixture.assertLiveEnvelope(
			t,
			pair,
			models.OutboxDeliveryPending,
		)
	})

	t.Run("event publish failure rolls back success envelope", func(t *testing.T) {
		fixture.clearRows(t)
		deadline := fixture.now.Add(time.Minute)
		lockAt := fixture.now
		pair := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryProcessing,
			deadline,
			"postgres-event-publish-failure-worker",
			&lockAt,
			1,
		)
		fixture.installEventPublishFailureTrigger(t)
		service := fixture.service(fixture.runtimeA, fixture.now)
		_, err := service.FinalizeOutboxAttempt(
			fixture.workerContext(
				t,
				context.Background(),
				fixture.projectA,
			),
			mustPostgresClaimRef(t, pair.delivery),
			OutboxKnownSuccess(fixture.now),
		)
		if err == nil {
			t.Fatal("injected event publication failure was ignored")
		}
		fixture.assertLiveEnvelope(
			t,
			pair,
			models.OutboxDeliveryProcessing,
		)
	})

	t.Run("cleanup never updates prior event publication history", func(t *testing.T) {
		fixture.clearRows(t)
		deadline := fixture.now.Add(-time.Second)
		pair := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryPending,
			deadline,
			"",
			nil,
			0,
		)
		publishedAt := fixture.now.Add(-time.Minute)
		if err := fixture.adminScoped.Exec(
			"UPDATE domain_events SET published_at = ? WHERE id = ?",
			publishedAt,
			pair.event.ID,
		).Error; err != nil {
			t.Fatal(err)
		}
		fixture.installEventPublishFailureTrigger(t)
		service := fixture.service(fixture.runtimeA, fixture.now)
		result, err := service.ExpireWebhookDeliveriesBatch(
			context.Background(),
			1,
		)
		if err != nil {
			t.Fatal(err)
		}
		if result.Attempted != 1 ||
			result.Expired != 1 ||
			result.Malformed != 0 {
			t.Fatalf("publication-preserving cleanup result = %+v", result)
		}
		delivery := fixture.loadDelivery(t, pair.delivery.ID)
		snapshot := fixture.loadSnapshot(t, pair.snapshot.ID)
		event := fixture.loadEvent(t, pair.event.ID)
		if delivery.Status != models.OutboxDeliveryExpired ||
			delivery.ExpiredAt == nil ||
			delivery.LockedAt != nil ||
			delivery.LockToken != nil ||
			snapshot.CredentialShreddedAt == nil ||
			snapshot.CredentialShredReason == nil ||
			*snapshot.CredentialShredReason !=
				models.WebhookCredentialShredReasonExpired ||
			snapshot.Secret != "" ||
			snapshot.PreviousSecret != "" ||
			snapshot.AccessToken != "" ||
			event.PublishedAt == nil ||
			!event.PublishedAt.Equal(publishedAt) {
			t.Fatalf(
				"cleanup publication history drifted delivery=%+v snapshot=%+v event=%+v",
				delivery,
				snapshot,
				event,
			)
		}
	})

	t.Run("FORCE RLS rejects cross scope finalize", func(t *testing.T) {
		fixture.clearRows(t)
		deadline := fixture.now.Add(time.Minute)
		lockAt := fixture.now
		pair := fixture.seedPair(
			t,
			fixture.projectB,
			models.OutboxDeliveryProcessing,
			deadline,
			"postgres-project-b-worker",
			&lockAt,
			1,
		)
		service := fixture.service(fixture.runtimeA, fixture.now)
		_, err := service.FinalizeOutboxAttempt(
			fixture.workerContext(
				t,
				context.Background(),
				fixture.projectA,
			),
			mustPostgresClaimRef(t, pair.delivery),
			OutboxKnownSuccess(fixture.now),
		)
		if !errors.Is(err, ErrOutboxLockLost) {
			t.Fatalf("cross-scope finalize error = %v, want lock lost", err)
		}
		var crossScopeCount int64
		if err := scopeddb.WithProjectScopeTransaction(
			context.Background(),
			fixture.runtimeA,
			fixture.projectA.Scope(),
			func(tx *gorm.DB) error {
				return tx.Model(&models.OutboxDelivery{}).
					Where("id = ?", pair.delivery.ID).
					Count(&crossScopeCount).Error
			},
		); err != nil {
			t.Fatal(err)
		}
		if crossScopeCount != 0 {
			t.Fatalf("Project A saw %d Project B deliveries", crossScopeCount)
		}
		if current := fixture.loadDelivery(
			t,
			pair.delivery.ID,
		); current.Status != models.OutboxDeliveryProcessing {
			t.Fatalf("cross-scope finalize changed status %s", current.Status)
		}
		fixture.assertLiveEnvelope(
			t,
			pair,
			models.OutboxDeliveryProcessing,
		)
	})

	t.Run("runtime ACL gate rejects table update drift", func(t *testing.T) {
		quotedRole := quoteWebhookPostgresIdentifier(
			fixture.runtimeRole,
		)
		if err := fixture.adminScoped.Exec(
			"GRANT UPDATE ON domain_events TO " + quotedRole,
		).Error; err != nil {
			t.Fatal(err)
		}
		revoked := false
		t.Cleanup(func() {
			if revoked {
				return
			}
			_ = fixture.adminScoped.Exec(
				"REVOKE UPDATE ON domain_events FROM " + quotedRole,
			).Error
		})
		if err := validateLifecyclePostgresRuntimeACL(
			fixture.runtimeA,
			fixture.runtimeRole,
		); err == nil {
			t.Fatal("runtime ACL gate accepted table-level UPDATE drift")
		}
		if err := fixture.adminScoped.Exec(
			"REVOKE UPDATE ON domain_events FROM " + quotedRole + ";" +
				"GRANT UPDATE (published_at) ON domain_events TO " +
				quotedRole,
		).Error; err != nil {
			t.Fatal(err)
		}
		revoked = true
		if err := validateLifecyclePostgresRuntimeACL(
			fixture.runtimeA,
			fixture.runtimeRole,
		); err != nil {
			t.Fatalf("runtime ACL did not recover after REVOKE: %v", err)
		}
	})

	t.Run("runtime ACL gate rejects PUBLIC table drift", func(t *testing.T) {
		if err := fixture.adminScoped.Exec(
			"GRANT UPDATE ON domain_events TO PUBLIC",
		).Error; err != nil {
			t.Fatal(err)
		}
		revoked := false
		t.Cleanup(func() {
			if revoked {
				return
			}
			_ = fixture.adminScoped.Exec(
				"REVOKE UPDATE ON domain_events FROM PUBLIC",
			).Error
		})
		if err := validateLifecyclePostgresRuntimeACL(
			fixture.runtimeA,
			fixture.runtimeRole,
		); err == nil {
			t.Fatal("runtime ACL gate accepted PUBLIC UPDATE drift")
		}
		if err := fixture.adminScoped.Exec(
			"REVOKE UPDATE ON domain_events FROM PUBLIC",
		).Error; err != nil {
			t.Fatal(err)
		}
		revoked = true
		if err := validateLifecyclePostgresRuntimeACL(
			fixture.runtimeA,
			fixture.runtimeRole,
		); err != nil {
			t.Fatalf("runtime ACL did not recover after PUBLIC REVOKE: %v", err)
		}
	})

	t.Run("runtime ACL gate rejects PUBLIC schema create", func(t *testing.T) {
		quotedSchema := quoteWebhookPostgresIdentifier(
			fixture.schemaName,
		)
		if err := fixture.admin.Exec(
			"GRANT CREATE ON SCHEMA " + quotedSchema + " TO PUBLIC",
		).Error; err != nil {
			t.Fatal(err)
		}
		revoked := false
		t.Cleanup(func() {
			if revoked {
				return
			}
			_ = fixture.admin.Exec(
				"REVOKE CREATE ON SCHEMA " + quotedSchema +
					" FROM PUBLIC",
			).Error
		})
		if err := validateLifecyclePostgresRuntimeACL(
			fixture.runtimeA,
			fixture.runtimeRole,
		); err == nil {
			t.Fatal("runtime ACL gate accepted PUBLIC schema CREATE")
		}
		if err := fixture.admin.Exec(
			"REVOKE CREATE ON SCHEMA " + quotedSchema +
				" FROM PUBLIC",
		).Error; err != nil {
			t.Fatal(err)
		}
		revoked = true
		if err := validateLifecyclePostgresRuntimeACL(
			fixture.runtimeA,
			fixture.runtimeRole,
		); err != nil {
			t.Fatalf(
				"runtime ACL did not recover after schema REVOKE: %v",
				err,
			)
		}
	})
}

type postgresLifecyclePair struct {
	event    models.DomainEvent
	delivery models.OutboxDelivery
	snapshot models.WebhookDeliverySnapshot
}

const (
	postgresLifecycleConfigAActiveID  uint = 1
	postgresLifecycleConfigADeletedID uint = 2
	postgresLifecycleConfigBActiveID  uint = 3
	postgresLifecycleEmergencyUserID  uint = 701
)

type postgresLifecyclePrivilege struct {
	TableName     string `gorm:"column:table_name"`
	ColumnName    string `gorm:"column:column_name"`
	PrivilegeType string `gorm:"column:privilege_type"`
}

type postgresLifecycleRace struct {
	ctx     context.Context
	cancel  context.CancelFunc
	release func() error
	wg      sync.WaitGroup
}

type webhookOutboxLifecyclePostgresFixture struct {
	admin        *gorm.DB
	adminScoped  *gorm.DB
	runtimeA     *gorm.DB
	runtimeB     *gorm.DB
	projectA     models.Project
	projectB     models.Project
	now          time.Time
	schemaName   string
	runtimeRole  string
	runtimeAName string
	runtimeBName string
	runtimeAPID  int
	runtimeBPID  int
	blockerPID   int
}

func (fixture *webhookOutboxLifecyclePostgresFixture) newSnapshotRace(
	t *testing.T,
	snapshotID string,
) *postgresLifecycleRace {
	t.Helper()
	return fixture.newPostgresLifecycleRace(
		t,
		fixture.blockSnapshot(t, snapshotID),
	)
}

func (fixture *webhookOutboxLifecyclePostgresFixture) newEventRace(
	t *testing.T,
	eventID string,
) *postgresLifecycleRace {
	t.Helper()
	return fixture.newPostgresLifecycleRace(
		t,
		fixture.blockEvent(t, eventID),
	)
}

func (fixture *webhookOutboxLifecyclePostgresFixture) newPostgresLifecycleRace(
	t *testing.T,
	release func() error,
) *postgresLifecycleRace {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	race := &postgresLifecycleRace{
		ctx:     ctx,
		cancel:  cancel,
		release: release,
	}
	t.Cleanup(func() {
		race.cancel()
		if err := race.release(); err != nil {
			t.Errorf("release lifecycle PostgreSQL race gate: %v", err)
		}
		race.wg.Wait()
	})
	return race
}

func (race *postgresLifecycleRace) run(
	fn func(context.Context),
) {
	race.wg.Add(1)
	go func() {
		defer race.wg.Done()
		fn(race.ctx)
	}()
}

func (race *postgresLifecycleRace) releaseGate(t *testing.T) {
	t.Helper()
	if err := race.release(); err != nil {
		t.Fatal(err)
	}
}

func (race *postgresLifecycleRace) join() {
	race.cancel()
	race.wg.Wait()
}

func newWebhookOutboxLifecyclePostgresFixture(
	t *testing.T,
) *webhookOutboxLifecyclePostgresFixture {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set CHRONODESK_POSTGRES_INTEGRATION=1 for lifecycle PG evidence")
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatal("parse lifecycle PostgreSQL integration DSN")
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("lifecycle PostgreSQL target must be loopback")
		}
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	schemaName := "chronodesk_lifecycle_" + suffix
	roleName := "chronodesk_lifecycle_runtime_" + suffix
	rolePassword := "ChronoDeskLifecycle" + suffix + "!"
	runtimeAName := "task9a_lifecycle_a_" + suffix
	runtimeBName := "task9a_lifecycle_b_" + suffix
	quotedSchema := quoteWebhookPostgresIdentifier(schemaName)
	quotedRole := quoteWebhookPostgresIdentifier(roleName)
	config := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	admin, err := gorm.Open(postgres.Open(rawDSN), config)
	if err != nil {
		t.Fatal("open lifecycle PostgreSQL admin connection")
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal("get lifecycle PostgreSQL admin pool")
	}
	var pools []*sql.DB
	schemaCreated := false
	roleCreated := false
	t.Cleanup(func() {
		for index, pool := range pools {
			if err := pool.Close(); err != nil {
				t.Errorf(
					"close lifecycle PostgreSQL pool %d: %v",
					index,
					err,
				)
			}
		}
		if schemaCreated {
			if err := admin.Exec(
				"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
			).Error; err != nil {
				t.Errorf("drop lifecycle PostgreSQL schema: %v", err)
			}
		}
		if roleCreated {
			if err := admin.Exec(
				"DROP ROLE IF EXISTS " + quotedRole,
			).Error; err != nil {
				t.Errorf("drop lifecycle PostgreSQL role: %v", err)
			}
		}
		var leftovers int64
		if err := admin.Raw(
			`SELECT
				(CASE WHEN EXISTS (
					SELECT 1 FROM pg_namespace WHERE nspname = ?
				) THEN 1 ELSE 0 END) +
				(CASE WHEN EXISTS (
					SELECT 1 FROM pg_roles WHERE rolname = ?
				) THEN 1 ELSE 0 END)`,
			schemaName,
			roleName,
		).Scan(&leftovers).Error; err != nil {
			t.Errorf("check lifecycle PostgreSQL cleanup: %v", err)
		} else if leftovers != 0 {
			t.Errorf(
				"lifecycle PostgreSQL fixture left %d catalog object(s)",
				leftovers,
			)
		}
		if err := adminSQL.Close(); err != nil {
			t.Errorf("close lifecycle PostgreSQL admin pool: %v", err)
		}
	})
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatal(err)
	}
	schemaCreated = true
	scopedURL := *parsed
	query := scopedURL.Query()
	query.Set("search_path", schemaName)
	scopedURL.RawQuery = query.Encode()
	adminScoped, err := gorm.Open(postgres.Open(scopedURL.String()), config)
	if err != nil {
		t.Fatal(err)
	}
	adminScopedSQL, err := adminScoped.DB()
	if err != nil {
		t.Fatal(err)
	}
	pools = append(pools, adminScopedSQL)
	tableOnly := adminScoped.Session(&gorm.Session{NewDB: true})
	tableOnly.Config.IgnoreRelationshipsWhenMigrating = true
	if err := tableOnly.AutoMigrate(
		&models.Project{},
		&models.User{},
		&models.ProjectMembership{},
		&models.WebhookConfig{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.WebhookDeliverySnapshot{},
	); err != nil {
		t.Fatal(err)
	}
	if err := adminScoped.Exec(`
		ALTER TABLE outbox_deliveries
		ADD CONSTRAINT chk_outbox_lifecycle_lock_token
		CHECK (
			(
				status = 'processing'
				AND lock_token IS NOT NULL
				AND lock_token ~
					'^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
			)
			OR (
				status <> 'processing'
				AND lock_token IS NULL
			)
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := webhookdispatch.MigratePostgresGenerationFence(
		adminScoped,
	); err != nil {
		t.Fatalf("migrate dispatch generation fence: %v", err)
	}
	validGenerationFence, err :=
		webhookdispatch.PostgresGenerationFenceIsValid(adminScoped)
	if err != nil {
		t.Fatalf("validate dispatch generation fence: %v", err)
	}
	if !validGenerationFence {
		t.Fatal("dispatch generation fence is incompatible")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	projectA := models.Project{
		ID:             101,
		PublicID:       "00000000-0000-7000-8000-000000000101",
		OrganizationID: 11,
		BusinessUnitID: 1,
		Key:            "LIFE-A",
		Name:           "Lifecycle A",
		Status:         models.ProjectStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	projectB := models.Project{
		ID:             102,
		PublicID:       "00000000-0000-7000-8000-000000000102",
		OrganizationID: 11,
		BusinessUnitID: 1,
		Key:            "LIFE-B",
		Name:           "Lifecycle B",
		Status:         models.ProjectStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := adminScoped.Create(&[]models.Project{
		projectA,
		projectB,
	}).Error; err != nil {
		t.Fatal(err)
	}
	emergencyUser := models.User{
		ID:           postgresLifecycleEmergencyUserID,
		CreatedAt:    now,
		UpdatedAt:    now,
		Username:     "lifecycle-emergency-admin",
		Email:        "lifecycle-emergency-admin@example.test",
		PasswordHash: "test-only-password-hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	emergencyMembership := models.ProjectMembership{
		ID:        7101,
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
		ProjectID: projectA.ID,
		UserID:    emergencyUser.ID,
		Role:      models.ProjectRoleAdmin,
		IsActive:  true,
	}
	if err := adminScoped.Create(&emergencyUser).Error; err != nil {
		t.Fatal(err)
	}
	if err := adminScoped.Create(&emergencyMembership).Error; err != nil {
		t.Fatal(err)
	}
	configs := []models.WebhookConfig{
		{
			ID:             postgresLifecycleConfigAActiveID,
			CreatedAt:      now,
			UpdatedAt:      now,
			OrganizationID: projectA.OrganizationID,
			ProjectID:      projectA.ID,
			Name:           "Lifecycle A active",
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     "https://lifecycle.invalid.example/active",
			Status:         models.WebhookStatusActive,
			Secret:         "sealed-active-current",
			PreviousSecret: "sealed-active-previous",
			PreviousSecretExpiresAt: func() *time.Time {
				value := now.Add(time.Hour)
				return &value
			}(),
			AccessToken: "sealed-active-access",
			CreatedBy:   1,
		},
		{
			ID:        postgresLifecycleConfigADeletedID,
			CreatedAt: now,
			UpdatedAt: now,
			DeletedAt: gorm.DeletedAt{
				Time:  now,
				Valid: true,
			},
			OrganizationID: projectA.OrganizationID,
			ProjectID:      projectA.ID,
			Name:           "Lifecycle A soft deleted",
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     "https://lifecycle.invalid.example/deleted",
			Status:         models.WebhookStatusDisabled,
			Secret:         "sealed-deleted-current",
			PreviousSecret: "sealed-deleted-previous",
			PreviousSecretExpiresAt: func() *time.Time {
				value := now.Add(time.Hour)
				return &value
			}(),
			AccessToken: "sealed-deleted-access",
			CreatedBy:   1,
		},
		{
			ID:             postgresLifecycleConfigBActiveID,
			CreatedAt:      now,
			UpdatedAt:      now,
			OrganizationID: projectB.OrganizationID,
			ProjectID:      projectB.ID,
			Name:           "Lifecycle B active",
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     "https://lifecycle.invalid.example/project-b",
			Status:         models.WebhookStatusActive,
			CreatedBy:      1,
		},
	}
	if err := adminScoped.Unscoped().Create(&configs).Error; err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"webhook_configs",
		"domain_events",
		"outbox_deliveries",
		"webhook_delivery_snapshots",
	} {
		quotedTable := quoteWebhookPostgresIdentifier(table)
		predicate := `(organization_id = NULLIF(current_setting(` +
			`'chronodesk.organization_id', true), '')::bigint AND ` +
			`project_id = NULLIF(current_setting(` +
			`'chronodesk.project_id', true), '')::bigint)`
		for _, statement := range []string{
			"ALTER TABLE " + quotedTable + " ENABLE ROW LEVEL SECURITY",
			"ALTER TABLE " + quotedTable + " FORCE ROW LEVEL SECURITY",
			"CREATE POLICY chronodesk_project_scope ON " + quotedTable +
				" FOR ALL TO PUBLIC USING " + predicate +
				" WITH CHECK " + predicate,
		} {
			if err := adminScoped.Exec(statement).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	projectPredicate := `(organization_id = NULLIF(current_setting(` +
		`'chronodesk.organization_id', true), '')::bigint AND ` +
		`id = NULLIF(current_setting(` +
		`'chronodesk.project_id', true), '')::bigint)`
	for _, statement := range []string{
		"ALTER TABLE projects ENABLE ROW LEVEL SECURITY",
		"ALTER TABLE projects FORCE ROW LEVEL SECURITY",
		"CREATE POLICY chronodesk_project_inventory ON projects " +
			"FOR SELECT TO PUBLIC USING (true)",
		"CREATE POLICY chronodesk_project_scope_update ON projects " +
			"FOR UPDATE TO PUBLIC USING " + projectPredicate +
			" WITH CHECK " + projectPredicate,
	} {
		if err := adminScoped.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := admin.Exec(
		"CREATE ROLE " + quotedRole +
			" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD " +
			quoteWebhookPostgresLiteral(rolePassword),
	).Error; err != nil {
		t.Fatal(err)
	}
	roleCreated = true
	for _, statement := range []string{
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRole,
		"GRANT SELECT ON projects, users, project_memberships, " +
			"webhook_configs, domain_events, outbox_deliveries, " +
			"webhook_delivery_snapshots TO " + quotedRole,
		"GRANT UPDATE (id) ON projects TO " + quotedRole,
		"GRANT UPDATE (id) ON users TO " + quotedRole,
		"GRANT UPDATE (id) ON project_memberships TO " + quotedRole,
		"GRANT UPDATE (id, status, secret, previous_secret, " +
			"previous_secret_expires_at, access_token, updated_at, updated_by) " +
			"ON webhook_configs TO " + quotedRole,
		"GRANT UPDATE (published_at) ON domain_events TO " + quotedRole,
		"GRANT UPDATE (status, attempts, next_attempt_at, locked_at, " +
			"locked_by, lock_token, dispatch_started_at, last_error, " +
			"delivered_at, expired_at, " +
			"updated_at) ON outbox_deliveries TO " + quotedRole,
		"GRANT UPDATE (secret, previous_secret, " +
			"previous_secret_expires_at, access_token, " +
			"credential_shredded_at, credential_shred_reason) " +
			"ON webhook_delivery_snapshots TO " + quotedRole,
	} {
		if err := adminScoped.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	openRuntime := func(applicationName string) (*gorm.DB, int) {
		runtimeURL := scopedURL
		runtimeURL.User = url.UserPassword(roleName, rolePassword)
		runtimeQuery := runtimeURL.Query()
		runtimeQuery.Set("application_name", applicationName)
		runtimeURL.RawQuery = runtimeQuery.Encode()
		runtime, err := gorm.Open(postgres.Open(runtimeURL.String()), config)
		if err != nil {
			t.Fatal("open lifecycle PostgreSQL runtime connection")
		}
		pool, err := runtime.DB()
		if err != nil {
			t.Fatal("get lifecycle PostgreSQL runtime pool")
		}
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		pools = append(pools, pool)
		if err := scopeddb.Install(runtime); err != nil {
			t.Fatal(err)
		}
		assertLifecyclePostgresRuntimeRole(t, runtime, roleName)
		var backendPID int
		if err := runtime.Raw(
			"SELECT pg_backend_pid()",
		).Scan(&backendPID).Error; err != nil {
			t.Fatal(err)
		}
		return runtime, backendPID
	}
	runtimeA, runtimeAPID := openRuntime(runtimeAName)
	runtimeB, runtimeBPID := openRuntime(runtimeBName)
	return &webhookOutboxLifecyclePostgresFixture{
		admin:        admin,
		adminScoped:  adminScoped,
		runtimeA:     runtimeA,
		runtimeB:     runtimeB,
		projectA:     projectA,
		projectB:     projectB,
		now:          now,
		schemaName:   schemaName,
		runtimeRole:  roleName,
		runtimeAName: runtimeAName,
		runtimeBName: runtimeBName,
		runtimeAPID:  runtimeAPID,
		runtimeBPID:  runtimeBPID,
	}
}

func assertLifecyclePostgresRuntimeRole(
	t *testing.T,
	db *gorm.DB,
	roleName string,
) {
	t.Helper()
	var state struct {
		CurrentUser string `gorm:"column:current_user"`
		Superuser   bool
		BypassRLS   bool `gorm:"column:bypass_rls"`
		TableCount  int  `gorm:"column:table_count"`
		NonOwner    bool `gorm:"column:non_owner"`
		RLSEnabled  bool `gorm:"column:rls_enabled"`
		RLSForced   bool `gorm:"column:rls_forced"`
	}
	if err := db.Raw(`
		SELECT
			current_user,
			role.rolsuper AS superuser,
			role.rolbypassrls AS bypass_rls,
			COUNT(*)::int AS table_count,
			BOOL_AND(owner.rolname <> current_user) AS non_owner,
			BOOL_AND(table_state.relrowsecurity) AS rls_enabled,
			BOOL_AND(table_state.relforcerowsecurity) AS rls_forced
		FROM pg_roles AS role
		JOIN pg_class AS table_state
		  ON table_state.relname IN (
			'projects',
			'webhook_configs',
			'domain_events',
			'outbox_deliveries',
			'webhook_delivery_snapshots'
		  )
		JOIN pg_namespace AS namespace
		  ON namespace.oid = table_state.relnamespace
		 AND namespace.nspname = current_schema()
		JOIN pg_roles AS owner ON owner.oid = table_state.relowner
		WHERE role.rolname = current_user
		GROUP BY role.rolsuper, role.rolbypassrls
	`).Scan(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.CurrentUser != roleName ||
		state.Superuser ||
		state.BypassRLS ||
		state.TableCount != 5 ||
		!state.NonOwner ||
		!state.RLSEnabled ||
		!state.RLSForced {
		t.Fatalf("lifecycle runtime role is not least privilege: %+v", state)
	}
	if err := validateLifecyclePostgresRuntimeACL(db, roleName); err != nil {
		t.Fatal(err)
	}
	if err := webhookdispatch.ValidatePostgresRuntimePrivileges(db); err != nil {
		t.Fatalf(
			"production Webhook dispatch runtime privilege gate: %v",
			err,
		)
	}
	var dispatchPrivileges struct {
		MarkerUpdate    bool  `gorm:"column:marker_update"`
		SchemaCreate    bool  `gorm:"column:schema_create"`
		TableTrigger    bool  `gorm:"column:table_trigger"`
		FunctionCount   int64 `gorm:"column:function_count"`
		FunctionExecute bool  `gorm:"column:function_execute"`
	}
	if err := db.Raw(`
		WITH generation_function AS (
			SELECT routine.oid
			FROM pg_proc AS routine
			JOIN pg_namespace AS namespace
			  ON namespace.oid = routine.pronamespace
			WHERE namespace.nspname = CURRENT_SCHEMA()
			  AND routine.proname = ?
			  AND routine.pronargs = 0
			  AND routine.prorettype = 'trigger'::regtype
		)
		SELECT
			has_column_privilege(
				current_user,
				format('%I.%I', CURRENT_SCHEMA(), 'outbox_deliveries'),
				'dispatch_started_at',
				'UPDATE'
			) AS marker_update,
			has_schema_privilege(
				current_user,
				CURRENT_SCHEMA(),
				'CREATE'
			) AS schema_create,
			has_table_privilege(
				current_user,
				format('%I.%I', CURRENT_SCHEMA(), 'outbox_deliveries'),
				'TRIGGER'
			) AS table_trigger,
			(SELECT COUNT(*) FROM generation_function) AS function_count,
			COALESCE(
				(
					SELECT BOOL_OR(
						has_function_privilege(
							current_user,
							generation_function.oid,
							'EXECUTE'
						)
					)
					FROM generation_function
				),
				FALSE
			) AS function_execute
	`, webhookdispatch.GenerationFunctionName).
		Scan(&dispatchPrivileges).Error; err != nil {
		t.Fatal(err)
	}
	if !dispatchPrivileges.MarkerUpdate ||
		dispatchPrivileges.SchemaCreate ||
		dispatchPrivileges.TableTrigger ||
		dispatchPrivileges.FunctionCount != 1 ||
		dispatchPrivileges.FunctionExecute {
		t.Fatalf(
			"Webhook dispatch runtime privileges are unsafe: %+v",
			dispatchPrivileges,
		)
	}
}

func validateLifecyclePostgresRuntimeACL(
	db *gorm.DB,
	roleName string,
) error {
	var tablePrivileges []postgresLifecyclePrivilege
	if err := db.Raw(`
		SELECT
			table_name,
			''::text AS column_name,
			privilege_type
		FROM information_schema.table_privileges
		WHERE (grantee = ? OR grantee = 'PUBLIC')
		  AND table_schema = current_schema()
		  AND table_name IN (
			'projects',
			'users',
			'project_memberships',
			'webhook_configs',
			'domain_events',
			'outbox_deliveries',
			'webhook_delivery_snapshots'
		  )
		ORDER BY table_name, privilege_type
	`, roleName).Scan(&tablePrivileges).Error; err != nil {
		return fmt.Errorf("inspect lifecycle runtime table ACL: %w", err)
	}
	wantTablePrivileges := map[string]struct{}{
		"projects/SELECT":                   {},
		"users/SELECT":                      {},
		"project_memberships/SELECT":        {},
		"webhook_configs/SELECT":            {},
		"domain_events/SELECT":              {},
		"outbox_deliveries/SELECT":          {},
		"webhook_delivery_snapshots/SELECT": {},
	}
	if !lifecyclePostgresPrivilegesMatch(
		tablePrivileges,
		wantTablePrivileges,
		false,
	) {
		return errors.New("lifecycle runtime table ACL is not exact")
	}

	var columnPrivileges []postgresLifecyclePrivilege
	if err := db.Raw(`
		SELECT table_name, column_name, privilege_type
		FROM information_schema.column_privileges
		WHERE (grantee = ? OR grantee = 'PUBLIC')
		  AND table_schema = current_schema()
		  AND table_name IN (
			'projects',
			'users',
			'project_memberships',
			'webhook_configs',
			'domain_events',
			'outbox_deliveries',
			'webhook_delivery_snapshots'
		  )
		  AND privilege_type <> 'SELECT'
		ORDER BY table_name, column_name, privilege_type
	`, roleName).Scan(&columnPrivileges).Error; err != nil {
		return fmt.Errorf("inspect lifecycle runtime column ACL: %w", err)
	}
	wantColumnPrivileges := map[string]struct{}{
		"projects/id/UPDATE":                                           {},
		"users/id/UPDATE":                                              {},
		"project_memberships/id/UPDATE":                                {},
		"webhook_configs/id/UPDATE":                                    {},
		"webhook_configs/status/UPDATE":                                {},
		"webhook_configs/secret/UPDATE":                                {},
		"webhook_configs/previous_secret/UPDATE":                       {},
		"webhook_configs/previous_secret_expires_at/UPDATE":            {},
		"webhook_configs/access_token/UPDATE":                          {},
		"webhook_configs/updated_at/UPDATE":                            {},
		"webhook_configs/updated_by/UPDATE":                            {},
		"domain_events/published_at/UPDATE":                            {},
		"outbox_deliveries/status/UPDATE":                              {},
		"outbox_deliveries/attempts/UPDATE":                            {},
		"outbox_deliveries/next_attempt_at/UPDATE":                     {},
		"outbox_deliveries/locked_at/UPDATE":                           {},
		"outbox_deliveries/locked_by/UPDATE":                           {},
		"outbox_deliveries/lock_token/UPDATE":                          {},
		"outbox_deliveries/dispatch_started_at/UPDATE":                 {},
		"outbox_deliveries/last_error/UPDATE":                          {},
		"outbox_deliveries/delivered_at/UPDATE":                        {},
		"outbox_deliveries/expired_at/UPDATE":                          {},
		"outbox_deliveries/updated_at/UPDATE":                          {},
		"webhook_delivery_snapshots/secret/UPDATE":                     {},
		"webhook_delivery_snapshots/previous_secret/UPDATE":            {},
		"webhook_delivery_snapshots/previous_secret_expires_at/UPDATE": {},
		"webhook_delivery_snapshots/access_token/UPDATE":               {},
		"webhook_delivery_snapshots/credential_shredded_at/UPDATE":     {},
		"webhook_delivery_snapshots/credential_shred_reason/UPDATE":    {},
	}
	if !lifecyclePostgresPrivilegesMatch(
		columnPrivileges,
		wantColumnPrivileges,
		true,
	) {
		return errors.New("lifecycle runtime column ACL is not exact")
	}
	var schemaPrivileges struct {
		RoleUsage    bool `gorm:"column:role_usage"`
		RoleCreate   bool `gorm:"column:role_create"`
		PublicUsage  bool `gorm:"column:public_usage"`
		PublicCreate bool `gorm:"column:public_create"`
	}
	if err := db.Raw(`
		SELECT
			has_schema_privilege(?, current_schema(), 'USAGE')
				AS role_usage,
			has_schema_privilege(?, current_schema(), 'CREATE')
				AS role_create,
			COALESCE(BOOL_OR(
				acl.grantee = 0 AND acl.privilege_type = 'USAGE'
			), FALSE) AS public_usage,
			COALESCE(BOOL_OR(
				acl.grantee = 0 AND acl.privilege_type = 'CREATE'
			), FALSE) AS public_create
		FROM pg_namespace AS namespace
		CROSS JOIN LATERAL aclexplode(
			COALESCE(
				namespace.nspacl,
				acldefault('n', namespace.nspowner)
			)
		) AS acl
		WHERE namespace.nspname = current_schema()
	`, roleName, roleName).Scan(&schemaPrivileges).Error; err != nil {
		return fmt.Errorf("inspect lifecycle runtime schema ACL: %w", err)
	}
	if !schemaPrivileges.RoleUsage ||
		schemaPrivileges.RoleCreate ||
		schemaPrivileges.PublicUsage ||
		schemaPrivileges.PublicCreate {
		return errors.New("lifecycle runtime schema ACL is not exact")
	}
	return nil
}

func lifecyclePostgresPrivilegesMatch(
	privileges []postgresLifecyclePrivilege,
	want map[string]struct{},
	includeColumn bool,
) bool {
	if len(privileges) != len(want) {
		return false
	}
	for _, privilege := range privileges {
		key := privilege.TableName + "/"
		if includeColumn {
			key += privilege.ColumnName + "/"
		}
		key += privilege.PrivilegeType
		if _, exists := want[key]; !exists {
			return false
		}
	}
	return true
}

func (fixture *webhookOutboxLifecyclePostgresFixture) service(
	db *gorm.DB,
	now time.Time,
) *AgentNativeService {
	return NewAgentNativeService(db, AgentNativeOptions{
		OutboxLockTTL: time.Minute,
		Now: func() time.Time {
			return now
		},
	})
}

func (fixture *webhookOutboxLifecyclePostgresFixture) workerContext(
	t *testing.T,
	ctx context.Context,
	project models.Project,
) context.Context {
	t.Helper()
	ctx, err := EnsureSystemProjectOperationContext(
		ctx,
		project.Scope(),
		models.SystemActor(outboxSystemActorID),
		"postgres-lifecycle-worker",
		"postgres-lifecycle-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func (fixture *webhookOutboxLifecyclePostgresFixture) seedPair(
	t *testing.T,
	project models.Project,
	status models.OutboxDeliveryStatus,
	expiresAt time.Time,
	lockedBy string,
	lockedAt *time.Time,
	attempts int,
) postgresLifecyclePair {
	t.Helper()
	return fixture.seedPairWithSnapshotSetup(
		t,
		project,
		status,
		expiresAt,
		lockedBy,
		lockedAt,
		attempts,
		nil,
	)
}

func (fixture *webhookOutboxLifecyclePostgresFixture) seedPairWithSnapshotSetup(
	t *testing.T,
	project models.Project,
	status models.OutboxDeliveryStatus,
	expiresAt time.Time,
	lockedBy string,
	lockedAt *time.Time,
	attempts int,
	setup func(*models.WebhookDeliverySnapshot),
) postgresLifecyclePair {
	t.Helper()
	eventID := uuid.NewString()
	event := models.DomainEvent{
		ID:              eventID,
		OrganizationID:  project.OrganizationID,
		ProjectID:       project.ID,
		SpecVersion:     "1.0",
		Source:          "urn:chronodesk:test:lifecycle",
		Type:            "io.chronodesk.test.lifecycle.v1",
		Subject:         "lifecycle/" + eventID,
		Time:            fixture.now,
		DataContentType: "application/json",
		Data:            []byte(`{"lifecycle":true}`),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "lifecycle-test",
		ResourceVersion: 1,
	}
	pair := fixture.buildPairForEvent(
		t,
		project,
		event,
		status,
		expiresAt,
		lockedBy,
		lockedAt,
		attempts,
	)
	if setup != nil {
		setup(&pair.snapshot)
	}
	if err := fixture.adminScoped.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&pair.event).Error; err != nil {
			return err
		}
		if err := tx.Create(&pair.delivery).Error; err != nil {
			return err
		}
		return tx.Create(&pair.snapshot).Error
	}); err != nil {
		t.Fatal(err)
	}
	return pair
}

func (fixture *webhookOutboxLifecyclePostgresFixture) seedSiblingPair(
	t *testing.T,
	project models.Project,
	event models.DomainEvent,
	status models.OutboxDeliveryStatus,
	expiresAt time.Time,
	lockedBy string,
	lockedAt *time.Time,
	attempts int,
) postgresLifecyclePair {
	t.Helper()
	pair := fixture.buildPairForEvent(
		t,
		project,
		event,
		status,
		expiresAt,
		lockedBy,
		lockedAt,
		attempts,
	)
	pair.snapshot.ConfigID = postgresLifecycleConfigADeletedID
	if err := fixture.adminScoped.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&pair.delivery).Error; err != nil {
			return err
		}
		return tx.Create(&pair.snapshot).Error
	}); err != nil {
		t.Fatal(err)
	}
	return pair
}

func (fixture *webhookOutboxLifecyclePostgresFixture) buildPairForEvent(
	t *testing.T,
	project models.Project,
	event models.DomainEvent,
	status models.OutboxDeliveryStatus,
	expiresAt time.Time,
	lockedBy string,
	lockedAt *time.Time,
	attempts int,
) postgresLifecyclePair {
	t.Helper()
	snapshotUUID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	deliveryID := uuid.NewString()
	destinationID, err := models.WebhookDeliverySnapshotDestinationID(
		snapshotUUID.String(),
	)
	if err != nil {
		t.Fatal(err)
	}
	delivery := models.OutboxDelivery{
		ID:              deliveryID,
		OrganizationID:  project.OrganizationID,
		ProjectID:       project.ID,
		EventID:         event.ID,
		DestinationType: "webhook",
		DestinationID:   destinationID,
		Status:          status,
		Attempts:        attempts,
		MaxAttempts:     8,
		NextAttemptAt:   fixture.now.Add(-time.Minute),
		LockedAt:        lockedAt,
		LockedBy:        lockedBy,
		ExpiresAt:       &expiresAt,
	}
	if status == models.OutboxDeliveryProcessing {
		token, tokenErr := uuid.NewV7()
		if tokenErr != nil {
			t.Fatal(tokenErr)
		}
		value := token.String()
		delivery.LockToken = &value
		startedAt := fixture.now
		if lockedAt != nil {
			startedAt = lockedAt.UTC().Add(time.Microsecond)
		}
		delivery.DispatchStartedAt = &startedAt
	}
	snapshot := models.WebhookDeliverySnapshot{
		ID:              snapshotUUID.String(),
		OrganizationID:  project.OrganizationID,
		ProjectID:       project.ID,
		ConfigID:        fixture.configIDForProject(project),
		EventID:         event.ID,
		ConfigUpdatedAt: fixture.now,
		Provider:        models.WebhookProviderCustom,
		WebhookURL:      "https://lifecycle.invalid.example/events",
		Secret:          "sealed-current-envelope",
		PreviousSecret:  "sealed-previous-envelope",
		PreviousSecretExpiresAt: func() *time.Time {
			value := expiresAt.UTC()
			return &value
		}(),
		AccessToken:         "sealed-access-envelope",
		CredentialExpiresAt: expiresAt,
		EnabledEvents:       "[]",
		RetryCount:          8,
		RetryInterval:       60,
		TimeoutSeconds:      30,
		RateLimit:           60,
		RateLimitWindow:     60,
	}
	return postgresLifecyclePair{
		event:    event,
		delivery: delivery,
		snapshot: snapshot,
	}
}

func (fixture *webhookOutboxLifecyclePostgresFixture) clearRows(t *testing.T) {
	t.Helper()
	if err := fixture.adminScoped.Exec(
		"TRUNCATE webhook_delivery_snapshots, outbox_deliveries, domain_events",
	).Error; err != nil {
		t.Fatal(err)
	}
}

func (fixture *webhookOutboxLifecyclePostgresFixture) configIDForProject(
	project models.Project,
) uint {
	if project.ID == fixture.projectB.ID {
		return postgresLifecycleConfigBActiveID
	}
	return postgresLifecycleConfigAActiveID
}

func (fixture *webhookOutboxLifecyclePostgresFixture) blockSnapshot(
	t *testing.T,
	snapshotID string,
) func() error {
	t.Helper()
	tx := fixture.adminScoped.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			_ = tx.Rollback().Error
		}
	})
	if err := tx.Exec(
		`SELECT 1
		 FROM webhook_delivery_snapshots
		 WHERE id = ?
		 FOR UPDATE`,
		snapshotID,
	).Error; err != nil {
		_ = tx.Rollback().Error
		t.Fatal(err)
	}
	if err := tx.Raw(
		"SELECT pg_backend_pid()",
	).Scan(&fixture.blockerPID).Error; err != nil {
		_ = tx.Rollback().Error
		t.Fatal(err)
	}
	return func() error {
		if released {
			return nil
		}
		released = true
		return tx.Commit().Error
	}
}

func (fixture *webhookOutboxLifecyclePostgresFixture) blockEvent(
	t *testing.T,
	eventID string,
) func() error {
	t.Helper()
	tx := fixture.adminScoped.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			_ = tx.Rollback().Error
		}
	})
	if err := tx.Exec(
		`SELECT 1
		 FROM domain_events
		 WHERE id = ?
		 FOR UPDATE`,
		eventID,
	).Error; err != nil {
		_ = tx.Rollback().Error
		t.Fatal(err)
	}
	if err := tx.Raw(
		"SELECT pg_backend_pid()",
	).Scan(&fixture.blockerPID).Error; err != nil {
		_ = tx.Rollback().Error
		t.Fatal(err)
	}
	return func() error {
		if released {
			return nil
		}
		released = true
		return tx.Commit().Error
	}
}

func (fixture *webhookOutboxLifecyclePostgresFixture) waitForRuntimeBlockedBy(
	t *testing.T,
	waitingPID int,
	blockingPID int,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := fixture.admin.Raw(`
			SELECT
				wait_event_type = 'Lock'
				AND ? = ANY(pg_blocking_pids(pid))
			FROM pg_stat_activity
			WHERE pid = ?
		`, blockingPID, waitingPID).Scan(&blocked).Error; err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf(
		"PostgreSQL runtime pid=%d was not blocked by pid=%d",
		waitingPID,
		blockingPID,
	)
}

func (fixture *webhookOutboxLifecyclePostgresFixture) waitForReplayCleanupLockChain(
	t *testing.T,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var chainLength int64
		if err := fixture.admin.Raw(`
			SELECT COUNT(*)
			FROM pg_stat_activity
			WHERE
				(pid = ?
				 AND wait_event_type = 'Lock'
				 AND ? = ANY(pg_blocking_pids(pid)))
				OR
				(pid = ?
				 AND wait_event_type = 'Lock'
				 AND ? = ANY(pg_blocking_pids(pid)))
		`,
			fixture.runtimeBPID,
			fixture.blockerPID,
			fixture.runtimeAPID,
			fixture.runtimeBPID,
		).Scan(&chainLength).Error; err != nil {
			t.Fatal(err)
		}
		if chainLength == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("PostgreSQL replay/cleanup lock chain was not observed")
}

func (fixture *webhookOutboxLifecyclePostgresFixture) waitForEventRuntimeLockChain(
	t *testing.T,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var chainComplete bool
		if err := fixture.admin.Raw(`
			SELECT
				(
					? = ANY(pg_blocking_pids(?))
					AND ? = ANY(pg_blocking_pids(?))
				)
				OR
				(
					? = ANY(pg_blocking_pids(?))
					AND ? = ANY(pg_blocking_pids(?))
				)
		`,
			fixture.blockerPID,
			fixture.runtimeAPID,
			fixture.runtimeAPID,
			fixture.runtimeBPID,
			fixture.blockerPID,
			fixture.runtimeBPID,
			fixture.runtimeBPID,
			fixture.runtimeAPID,
		).Scan(&chainComplete).Error; err != nil {
			t.Fatal(err)
		}
		if chainComplete {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("PostgreSQL double-success event lock chain was not observed")
}

func (fixture *webhookOutboxLifecyclePostgresFixture) loadDelivery(
	t *testing.T,
	id string,
) models.OutboxDelivery {
	t.Helper()
	var delivery models.OutboxDelivery
	if err := fixture.adminScoped.First(&delivery, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return delivery
}

func (fixture *webhookOutboxLifecyclePostgresFixture) loadSnapshot(
	t *testing.T,
	id string,
) models.WebhookDeliverySnapshot {
	t.Helper()
	var snapshot models.WebhookDeliverySnapshot
	if err := fixture.adminScoped.First(&snapshot, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (fixture *webhookOutboxLifecyclePostgresFixture) loadEvent(
	t *testing.T,
	id string,
) models.DomainEvent {
	t.Helper()
	var event models.DomainEvent
	if err := fixture.adminScoped.First(&event, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return event
}

func (fixture *webhookOutboxLifecyclePostgresFixture) assertSucceededEnvelope(
	t *testing.T,
	pair postgresLifecyclePair,
) {
	t.Helper()
	delivery := fixture.loadDelivery(t, pair.delivery.ID)
	if delivery.Status != models.OutboxDeliverySucceeded ||
		delivery.DeliveredAt == nil ||
		delivery.ExpiredAt != nil ||
		delivery.LockedAt != nil ||
		delivery.LockedBy != "" ||
		delivery.LockToken != nil {
		t.Fatalf("succeeded lifecycle delivery envelope = %+v", delivery)
	}
	snapshot := fixture.loadSnapshot(t, pair.snapshot.ID)
	if snapshot.Secret != "" ||
		snapshot.PreviousSecret != "" ||
		snapshot.PreviousSecretExpiresAt != nil ||
		snapshot.AccessToken != "" ||
		snapshot.CredentialShreddedAt == nil ||
		snapshot.CredentialShredReason == nil ||
		*snapshot.CredentialShredReason !=
			models.WebhookCredentialShredReasonSucceeded {
		t.Fatalf("succeeded lifecycle snapshot envelope = %+v", snapshot)
	}
	event := fixture.loadEvent(t, pair.event.ID)
	if event.PublishedAt == nil {
		t.Fatal("succeeded lifecycle event remained unpublished")
	}
}

func (fixture *webhookOutboxLifecyclePostgresFixture) assertExpiredEnvelope(
	t *testing.T,
	pair postgresLifecyclePair,
) {
	t.Helper()
	delivery := fixture.loadDelivery(t, pair.delivery.ID)
	if delivery.Status != models.OutboxDeliveryExpired ||
		delivery.DeliveredAt != nil ||
		delivery.ExpiredAt == nil ||
		delivery.LockedAt != nil ||
		delivery.LockedBy != "" ||
		delivery.LockToken != nil {
		t.Fatalf("expired lifecycle delivery envelope = %+v", delivery)
	}
	snapshot := fixture.loadSnapshot(t, pair.snapshot.ID)
	if snapshot.Secret != "" ||
		snapshot.PreviousSecret != "" ||
		snapshot.PreviousSecretExpiresAt != nil ||
		snapshot.AccessToken != "" ||
		snapshot.CredentialShreddedAt == nil ||
		snapshot.CredentialShredReason == nil ||
		*snapshot.CredentialShredReason !=
			models.WebhookCredentialShredReasonExpired {
		t.Fatalf("expired lifecycle snapshot envelope = %+v", snapshot)
	}
	event := fixture.loadEvent(t, pair.event.ID)
	if event.PublishedAt != nil {
		t.Fatalf("expired lifecycle event published at %v", event.PublishedAt)
	}
}

func (fixture *webhookOutboxLifecyclePostgresFixture) assertLiveEnvelope(
	t *testing.T,
	pair postgresLifecyclePair,
	status models.OutboxDeliveryStatus,
) {
	t.Helper()
	delivery := fixture.loadDelivery(t, pair.delivery.ID)
	if delivery.Status != status ||
		delivery.DeliveredAt != nil ||
		delivery.ExpiredAt != nil {
		t.Fatalf("live lifecycle delivery envelope = %+v", delivery)
	}
	if status == models.OutboxDeliveryProcessing {
		if delivery.LockedAt == nil ||
			delivery.LockedBy == "" ||
			delivery.LockToken == nil {
			t.Fatalf("processing lifecycle fence envelope = %+v", delivery)
		}
	} else if delivery.LockedAt != nil ||
		delivery.LockedBy != "" ||
		delivery.LockToken != nil {
		t.Fatalf("unlocked lifecycle envelope = %+v", delivery)
	}
	snapshot := fixture.loadSnapshot(t, pair.snapshot.ID)
	if snapshot.Secret != pair.snapshot.Secret ||
		snapshot.PreviousSecret != pair.snapshot.PreviousSecret ||
		snapshot.AccessToken != pair.snapshot.AccessToken ||
		snapshot.CredentialShreddedAt != nil ||
		snapshot.CredentialShredReason != nil {
		t.Fatalf("live lifecycle snapshot envelope = %+v", snapshot)
	}
	event := fixture.loadEvent(t, pair.event.ID)
	if event.PublishedAt != nil {
		t.Fatalf("live lifecycle event unexpectedly published at %v", event.PublishedAt)
	}
}

func (fixture *webhookOutboxLifecyclePostgresFixture) installShredFailureTrigger(
	t *testing.T,
) {
	t.Helper()
	if err := fixture.adminScoped.Exec(`
		CREATE OR REPLACE FUNCTION fail_lifecycle_shred()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			IF NEW.credential_shredded_at IS NOT NULL THEN
				RAISE EXCEPTION 'injected lifecycle shred failure';
			END IF;
			RETURN NEW;
		END
		$$;
		CREATE TRIGGER fail_lifecycle_shred
		BEFORE UPDATE ON webhook_delivery_snapshots
		FOR EACH ROW EXECUTE FUNCTION fail_lifecycle_shred()
	`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fixture.adminScoped.Exec(`
			DROP TRIGGER IF EXISTS fail_lifecycle_shred
			ON webhook_delivery_snapshots;
			DROP FUNCTION IF EXISTS fail_lifecycle_shred()
		`).Error; err != nil {
			t.Errorf("remove lifecycle shred failure trigger: %v", err)
		}
	})
}

func (fixture *webhookOutboxLifecyclePostgresFixture) installEventPublishFailureTrigger(
	t *testing.T,
) {
	t.Helper()
	if err := fixture.adminScoped.Exec(`
		CREATE OR REPLACE FUNCTION fail_lifecycle_event_publish()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			RAISE EXCEPTION 'injected lifecycle event publish failure';
		END
		$$;
		CREATE TRIGGER fail_lifecycle_event_publish
		BEFORE UPDATE OF published_at ON domain_events
		FOR EACH ROW EXECUTE FUNCTION fail_lifecycle_event_publish()
	`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fixture.adminScoped.Exec(`
			DROP TRIGGER IF EXISTS fail_lifecycle_event_publish
			ON domain_events;
			DROP FUNCTION IF EXISTS fail_lifecycle_event_publish()
		`).Error; err != nil {
			t.Errorf(
				"remove lifecycle event publish failure trigger: %v",
				err,
			)
		}
	})
}

func mustPostgresClaimRef(
	t *testing.T,
	delivery models.OutboxDelivery,
) OutboxClaimRef {
	t.Helper()
	ref, err := OutboxClaimRefFromDelivery(&delivery)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func receivePostgresError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(8 * time.Second):
		t.Fatal("PostgreSQL lifecycle operation timed out")
		return nil
	}
}

func receivePostgresClaimResult(
	t *testing.T,
	result <-chan struct {
		rows []*models.OutboxDelivery
		err  error
	},
) struct {
	rows []*models.OutboxDelivery
	err  error
} {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(8 * time.Second):
		t.Fatal("PostgreSQL claim race timed out")
		return struct {
			rows []*models.OutboxDelivery
			err  error
		}{}
	}
}

func receivePostgresCleanupResult(
	t *testing.T,
	result <-chan struct {
		result WebhookOutboxCleanupResult
		err    error
	},
) struct {
	result WebhookOutboxCleanupResult
	err    error
} {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(8 * time.Second):
		t.Fatal("PostgreSQL cleanup race timed out")
		return struct {
			result WebhookOutboxCleanupResult
			err    error
		}{}
	}
}

func receivePostgresFinalizeResult(
	t *testing.T,
	result <-chan struct {
		result OutboxFinalizeResult
		err    error
	},
) struct {
	result OutboxFinalizeResult
	err    error
} {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(8 * time.Second):
		t.Fatal("PostgreSQL finalize race timed out")
		return struct {
			result OutboxFinalizeResult
			err    error
		}{}
	}
}
