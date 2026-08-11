package services

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"gorm.io/gorm"
)

func TestWebhookDispatchStartLinearizesEmergencyRevokePostgres(
	t *testing.T,
) {
	t.Run("revoke commits before dispatch start", func(t *testing.T) {
		fixture := newWebhookOutboxLifecyclePostgresFixture(t)
		attempt := newPostgresWebhookDispatchAttempt(t, fixture)
		beforeStart := make(chan struct{})
		resume := make(chan struct{})
		var resumeOnce sync.Once
		release := func() {
			resumeOnce.Do(func() { close(resume) })
		}
		t.Cleanup(release)
		attempt.notifications.beforeWebhookDispatchStart = func(
			context.Context,
			WebhookOutboxAttemptClaim,
		) {
			close(beforeStart)
			<-resume
		}

		result := make(chan OutboxAttemptResult, 1)
		go func() {
			result <- attempt.send()
		}()
		awaitWebhookDispatchSignalOrResult(
			t,
			beforeStart,
			result,
			"second gate",
		)

		revoke, err := fixture.service(
			fixture.runtimeB,
			time.Now().UTC(),
		).EmergencyRevokeWebhook(
			attempt.humanContext,
			postgresLifecycleConfigAActiveID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if revoke.ExpiredDeliveries != 1 ||
			revoke.InFlightDeliveries != 0 ||
			revoke.ShreddedSnapshots != 1 {
			t.Fatalf("revoke-first result = %+v", revoke)
		}
		if attempt.transportCalls.Load() != 0 {
			t.Fatal("HTTP transport started before dispatch boundary resumed")
		}
		release()
		sendResult := awaitWebhookDispatchResult(t, result)
		if sendResult.Kind != OutboxAttemptKnownFailure ||
			!errors.Is(
				sendResult.Err,
				ErrWebhookOutboxAttemptRejected,
			) ||
			attempt.transportCalls.Load() != 0 {
			t.Fatalf(
				"revoke-first send=%+v transports=%d",
				sendResult,
				attempt.transportCalls.Load(),
			)
		}
		delivery := fixture.loadDelivery(t, attempt.pair.delivery.ID)
		if delivery.Status != models.OutboxDeliveryExpired ||
			delivery.DispatchStartedAt != nil {
			t.Fatalf("revoke-first delivery = %+v", delivery)
		}
	})

	t.Run("dispatch start commits before revoke", func(t *testing.T) {
		fixture := newWebhookOutboxLifecyclePostgresFixture(t)
		attempt := newPostgresWebhookDispatchAttempt(t, fixture)
		afterStart := make(chan struct{})
		resume := make(chan struct{})
		var resumeOnce sync.Once
		release := func() {
			resumeOnce.Do(func() { close(resume) })
		}
		t.Cleanup(release)
		attempt.notifications.afterWebhookDispatchStart = func(
			context.Context,
			WebhookOutboxAttemptClaim,
		) {
			close(afterStart)
			<-resume
		}

		result := make(chan OutboxAttemptResult, 1)
		go func() {
			result <- attempt.send()
		}()
		awaitWebhookDispatchSignalOrResult(
			t,
			afterStart,
			result,
			"committed dispatch start",
		)
		if attempt.transportCalls.Load() != 0 {
			t.Fatal("transport began before committed-boundary hook released")
		}

		revoke, err := fixture.service(
			fixture.runtimeB,
			time.Now().UTC(),
		).EmergencyRevokeWebhook(
			attempt.humanContext,
			postgresLifecycleConfigAActiveID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if revoke.ExpiredDeliveries != 0 ||
			revoke.InFlightDeliveries != 1 ||
			revoke.ShreddedSnapshots != 1 {
			t.Fatalf("dispatch-first revoke result = %+v", revoke)
		}
		delivery := fixture.loadDelivery(t, attempt.pair.delivery.ID)
		if delivery.Status != models.OutboxDeliveryProcessing ||
			!isWebhookDispatchStarted(
				delivery.DispatchStartedAt,
				delivery.LockedAt,
			) {
			t.Fatalf("dispatch-first delivery = %+v", delivery)
		}
		release()
		sendResult := awaitWebhookDispatchResult(t, result)
		if sendResult.Kind != OutboxAttemptKnownSuccess ||
			sendResult.Err != nil ||
			attempt.transportCalls.Load() != 1 {
			t.Fatalf(
				"dispatch-first send=%+v transports=%d",
				sendResult,
				attempt.transportCalls.Load(),
			)
		}
	})
}

func TestWebhookDispatchStateMixedVersionPostgres(t *testing.T) {
	t.Run("legacy null processing is conservatively in flight", func(
		t *testing.T,
	) {
		fixture := newWebhookOutboxLifecyclePostgresFixture(t)
		fixture.clearRows(t)
		lockedAt := time.Now().UTC().Add(-2 * time.Minute).
			Truncate(time.Microsecond)
		pair := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryPending,
			lockedAt.Add(time.Hour),
			"",
			nil,
			0,
		)
		if err := fixture.adminScoped.Model(&models.OutboxDelivery{}).
			Where("id = ?", pair.delivery.ID).
			Updates(map[string]any{
				"status":     models.OutboxDeliveryProcessing,
				"attempts":   1,
				"locked_at":  lockedAt,
				"locked_by":  "legacy-binary-worker",
				"lock_token": uuid.Must(uuid.NewV7()).String(),
			}).Error; err != nil {
			t.Fatal(err)
		}
		pair.delivery.DispatchStartedAt = nil
		service := fixture.service(
			fixture.runtimeA,
			lockedAt.Add(10*time.Minute),
		)
		claimed, err := service.ClaimPendingOutbox(
			fixture.workerContext(
				t,
				context.Background(),
				fixture.projectA,
			),
			"new-binary-worker",
			1,
			time.Minute,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(claimed) != 0 {
			t.Fatalf("new worker reclaimed legacy processing: %+v", claimed)
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
		revoke, err := service.EmergencyRevokeWebhook(
			humanContext,
			postgresLifecycleConfigAActiveID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if revoke.ExpiredDeliveries != 0 ||
			revoke.InFlightDeliveries != 1 ||
			revoke.ShreddedSnapshots != 1 {
			t.Fatalf("legacy mixed-version revoke = %+v", revoke)
		}
		current := fixture.loadDelivery(t, pair.delivery.ID)
		if current.Status != models.OutboxDeliveryProcessing ||
			current.DispatchStartedAt != nil ||
			current.LockedBy != "legacy-binary-worker" {
			t.Fatalf("legacy processing was recalled: %+v", current)
		}
	})

	t.Run("new prepared processing is recallable", func(t *testing.T) {
		fixture := newWebhookOutboxLifecyclePostgresFixture(t)
		fixture.clearRows(t)
		pair := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryPending,
			time.Now().UTC().Add(time.Hour),
			"",
			nil,
			0,
		)
		service := fixture.service(fixture.runtimeA, time.Now().UTC())
		workerContext := fixture.workerContext(
			t,
			context.Background(),
			fixture.projectA,
		)
		claimed, err := service.ClaimPendingOutbox(
			workerContext,
			"new-prepared-worker",
			1,
			time.Minute,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(claimed) != 1 ||
			claimed[0].ID != pair.delivery.ID ||
			!isWebhookDispatchPrepared(
				claimed[0].DispatchStartedAt,
				claimed[0].LockedAt,
			) {
			t.Fatalf("new prepared claim = %+v", claimed)
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
		revoke, err := service.EmergencyRevokeWebhook(
			humanContext,
			postgresLifecycleConfigAActiveID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if revoke.ExpiredDeliveries != 1 ||
			revoke.InFlightDeliveries != 0 ||
			revoke.ShreddedSnapshots != 1 {
			t.Fatalf("prepared revoke = %+v", revoke)
		}
		current := fixture.loadDelivery(t, pair.delivery.ID)
		if current.Status != models.OutboxDeliveryExpired ||
			current.DispatchStartedAt != nil {
			t.Fatalf("prepared processing was not recalled: %+v", current)
		}
	})
}

func TestWebhookDispatchGenerationFenceRejectsLegacyReclaimPostgres(
	t *testing.T,
) {
	fixture := newWebhookOutboxLifecyclePostgresFixture(t)
	fixture.clearRows(t)
	claimNow := time.Now().UTC().Add(2 * time.Second).Truncate(
		time.Microsecond,
	)
	pair := fixture.seedPair(
		t,
		fixture.projectA,
		models.OutboxDeliveryPending,
		claimNow.Add(time.Hour),
		"",
		nil,
		0,
	)
	workerContext := fixture.workerContext(
		t,
		context.Background(),
		fixture.projectA,
	)
	claimed, err := fixture.service(
		fixture.runtimeA,
		claimNow,
	).ClaimPendingOutbox(
		workerContext,
		"new-prepared-worker",
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 ||
		claimed[0].ID != pair.delivery.ID ||
		!isWebhookDispatchPrepared(
			claimed[0].DispatchStartedAt,
			claimed[0].LockedAt,
		) {
		t.Fatalf("new prepared claim = %+v", claimed)
	}
	original := fixture.loadDelivery(t, claimed[0].ID)
	legacyLockedAt := claimed[0].LockedAt.UTC().Add(2 * time.Minute)
	runtimeSQL, err := fixture.runtimeA.DB()
	if err != nil {
		t.Fatal(err)
	}
	rawTransaction, err := runtimeSQL.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawTransaction.Rollback() })
	if _, err := rawTransaction.ExecContext(
		context.Background(),
		`SELECT
			set_config('chronodesk.organization_id', $1, true),
			set_config('chronodesk.project_id', $2, true),
			set_config('chronodesk.project_ids', '', true)`,
		strconv.FormatUint(
			uint64(fixture.projectA.OrganizationID),
			10,
		),
		strconv.FormatUint(uint64(fixture.projectA.ID), 10),
	); err != nil {
		t.Fatal(err)
	}
	_, legacyErr := rawTransaction.ExecContext(
		context.Background(),
		`UPDATE outbox_deliveries
		 SET attempts = $1,
		     locked_at = $2,
		     locked_by = $3,
		     lock_token = $4,
		     updated_at = $2
		 WHERE id = $5
		   AND organization_id = $6
		   AND project_id = $7`,
		claimed[0].Attempts+1,
		legacyLockedAt,
		"legacy-reclaim-worker",
		"019fee69-720c-7023-ae63-fcaf437562aa",
		claimed[0].ID,
		fixture.projectA.OrganizationID,
		fixture.projectA.ID,
	)
	if err := rawTransaction.Rollback(); err != nil &&
		!errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	var postgresError *pgconn.PgError
	if !errors.As(legacyErr, &postgresError) ||
		postgresError.Code != "23514" {
		t.Fatalf(
			"legacy reclaim error = %v, want SQLSTATE 23514",
			legacyErr,
		)
	}
	unchanged := fixture.loadDelivery(t, claimed[0].ID)
	if unchanged.Attempts != original.Attempts ||
		unchanged.LockedAt == nil ||
		!unchanged.LockedAt.Equal(original.LockedAt.UTC()) ||
		unchanged.LockedBy != original.LockedBy ||
		outboxLockTokenValue(unchanged.LockToken) !=
			outboxLockTokenValue(original.LockToken) ||
		unchanged.DispatchStartedAt == nil ||
		!unchanged.DispatchStartedAt.Equal(
			original.DispatchStartedAt.UTC(),
		) {
		t.Fatalf("rejected legacy reclaim mutated row: %+v", unchanged)
	}

	replacementNow := original.LockedAt.UTC().Add(10 * time.Minute)
	replacement, err := fixture.service(
		fixture.runtimeA,
		replacementNow,
	).ClaimPendingOutbox(
		workerContext,
		"new-replacement-worker",
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(replacement) != 1 ||
		replacement[0].ID != original.ID ||
		replacement[0].Attempts != original.Attempts+1 ||
		!isWebhookDispatchPrepared(
			replacement[0].DispatchStartedAt,
			replacement[0].LockedAt,
		) ||
		replacement[0].DispatchStartedAt.Equal(
			original.DispatchStartedAt.UTC(),
		) {
		t.Fatalf("generation-bound replacement claim = %+v", replacement)
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
	revoke, err := fixture.service(
		fixture.runtimeB,
		replacementNow,
	).EmergencyRevokeWebhook(
		humanContext,
		postgresLifecycleConfigAActiveID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if revoke.ExpiredDeliveries != 1 ||
		revoke.InFlightDeliveries != 0 ||
		revoke.ShreddedSnapshots != 1 {
		t.Fatalf("generation-bound prepared revoke = %+v", revoke)
	}
}

func TestWebhookDispatchGenerationFenceRejectsUnknownAndStartedReclaimsPostgres(
	t *testing.T,
) {
	t.Run("U legacy unknown", func(t *testing.T) {
		fixture := newWebhookOutboxLifecyclePostgresFixture(t)
		fixture.clearRows(t)
		lockedAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(
			time.Microsecond,
		)
		pair := fixture.seedPair(
			t,
			fixture.projectA,
			models.OutboxDeliveryPending,
			lockedAt.Add(time.Hour),
			"",
			nil,
			0,
		)
		if err := fixture.adminScoped.Model(&models.OutboxDelivery{}).
			Where("id = ?", pair.delivery.ID).
			Updates(map[string]any{
				"status":     models.OutboxDeliveryProcessing,
				"attempts":   1,
				"locked_at":  lockedAt,
				"locked_by":  "legacy-unknown-worker",
				"lock_token": uuid.Must(uuid.NewV7()).String(),
			}).Error; err != nil {
			t.Fatal(err)
		}
		delivery := fixture.loadDelivery(t, pair.delivery.ID)
		if delivery.DispatchStartedAt != nil {
			t.Fatalf("legacy unknown fixture = %+v", delivery)
		}
		assertLegacyReclaimRejectedPostgres(
			t,
			fixture,
			delivery,
			lockedAt.Add(10*time.Minute),
		)
	})

	t.Run("S dispatch started", func(t *testing.T) {
		fixture := newWebhookOutboxLifecyclePostgresFixture(t)
		attempt := newPostgresWebhookDispatchAttempt(t, fixture)
		if err := attempt.notifications.beginWebhookOutboxDispatch(
			attempt.workerContext,
			attempt.claim,
		); err != nil {
			t.Fatal(err)
		}
		delivery := fixture.loadDelivery(t, attempt.pair.delivery.ID)
		if !isWebhookDispatchStarted(
			delivery.DispatchStartedAt,
			delivery.LockedAt,
		) {
			t.Fatalf("started fixture = %+v", delivery)
		}
		assertLegacyReclaimRejectedPostgres(
			t,
			fixture,
			delivery,
			delivery.LockedAt.UTC().Add(10*time.Minute),
		)
		if attempt.transportCalls.Load() != 0 {
			t.Fatalf(
				"generation fence test entered transport %d times",
				attempt.transportCalls.Load(),
			)
		}
	})
}

func assertLegacyReclaimRejectedPostgres(
	t *testing.T,
	fixture *webhookOutboxLifecyclePostgresFixture,
	original models.OutboxDelivery,
	nextLockedAt time.Time,
) {
	t.Helper()
	runtimeSQL, err := fixture.runtimeA.DB()
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := runtimeSQL.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transaction.Rollback() })
	if _, err := transaction.ExecContext(
		context.Background(),
		`SELECT
			set_config('chronodesk.organization_id', $1, true),
			set_config('chronodesk.project_id', $2, true),
			set_config('chronodesk.project_ids', '', true)`,
		strconv.FormatUint(
			uint64(fixture.projectA.OrganizationID),
			10,
		),
		strconv.FormatUint(uint64(fixture.projectA.ID), 10),
	); err != nil {
		t.Fatal(err)
	}
	_, mutationErr := transaction.ExecContext(
		context.Background(),
		`UPDATE outbox_deliveries
		 SET attempts = $1,
		     locked_at = $2,
		     locked_by = $3,
		     lock_token = $4,
		     updated_at = $2
		 WHERE id = $5
		   AND organization_id = $6
		   AND project_id = $7`,
		original.Attempts+1,
		nextLockedAt.UTC(),
		"legacy-reclaim-worker",
		"019fee69-720c-7023-ae63-fcaf437562ad",
		original.ID,
		fixture.projectA.OrganizationID,
		fixture.projectA.ID,
	)
	if err := transaction.Rollback(); err != nil &&
		!errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	var postgresError *pgconn.PgError
	if !errors.As(mutationErr, &postgresError) ||
		postgresError.Code != "23514" {
		t.Fatalf(
			"legacy mutation error = %v, want SQLSTATE 23514",
			mutationErr,
		)
	}
	current := fixture.loadDelivery(t, original.ID)
	if current.Attempts != original.Attempts ||
		current.LockedAt == nil ||
		original.LockedAt == nil ||
		!current.LockedAt.Equal(original.LockedAt.UTC()) ||
		current.LockedBy != original.LockedBy ||
		outboxLockTokenValue(current.LockToken) !=
			outboxLockTokenValue(original.LockToken) ||
		!sameOptionalTime(
			current.DispatchStartedAt,
			original.DispatchStartedAt,
		) {
		t.Fatalf("rejected legacy mutation changed row: %+v", current)
	}
}

func sameOptionalTime(left *time.Time, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.UTC().Equal(right.UTC())
}

func TestWebhookLegacyUnknownSecondGateCountsInFlightPostgres(
	t *testing.T,
) {
	fixture := newWebhookOutboxLifecyclePostgresFixture(t)
	fixture.clearRows(t)
	lockedAt := time.Now().UTC().Add(-time.Second).Truncate(
		time.Microsecond,
	)
	pair := fixture.seedPair(
		t,
		fixture.projectA,
		models.OutboxDeliveryPending,
		lockedAt.Add(time.Hour),
		"",
		nil,
		0,
	)
	if err := fixture.adminScoped.Model(&models.OutboxDelivery{}).
		Where("id = ?", pair.delivery.ID).
		Updates(map[string]any{
			"status":     models.OutboxDeliveryProcessing,
			"attempts":   1,
			"locked_at":  lockedAt,
			"locked_by":  "legacy-pre-do-worker",
			"lock_token": uuid.Must(uuid.NewV7()).String(),
		}).Error; err != nil {
		t.Fatal(err)
	}
	service := fixture.service(fixture.runtimeA, time.Now().UTC())
	workerContext := fixture.workerContext(
		t,
		context.Background(),
		fixture.projectA,
	)
	legacyClaimed := fixture.loadDelivery(t, pair.delivery.ID)
	if legacyClaimed.DispatchStartedAt != nil {
		t.Fatalf("legacy fixture unexpectedly has marker: %+v", legacyClaimed)
	}

	// Reproduce the old worker's second gate after it loaded the frozen
	// credential but immediately before client.Do.
	gateReturned := make(chan struct{})
	resumeTransport := make(chan struct{})
	var resumeOnce sync.Once
	release := func() {
		resumeOnce.Do(func() { close(resumeTransport) })
	}
	t.Cleanup(release)
	transportResult := make(chan error, 1)
	var transportCalls atomic.Int32
	go func() {
		claim := OutboxClaimRef{
			DeliveryID: legacyClaimed.ID,
			WorkerID:   legacyClaimed.LockedBy,
			LockToken:  outboxLockTokenValue(legacyClaimed.LockToken),
			LockedAt:   legacyClaimed.LockedAt.UTC(),
			Attempts:   legacyClaimed.Attempts,
		}
		var frozenSecret string
		gateErr := runProjectOperation(
			workerContext,
			fixture.runtimeA,
			func(projectCtx context.Context) error {
				return transactionForContext(
					projectCtx,
					fixture.runtimeA,
					func(tx *gorm.DB) error {
						if err := lockWebhookLifecycleProject(
							tx,
							fixture.projectA.Scope(),
						); err != nil {
							return err
						}
						if _, err := lockWebhookConfigForDestination(
							tx,
							fixture.projectA.Scope(),
							legacyClaimed.DestinationID,
						); err != nil {
							return err
						}
						delivery, err := lockClaimedOutboxDelivery(
							tx,
							fixture.projectA.Scope(),
							claim,
						)
						if err != nil {
							return err
						}
						snapshot, err := lockWebhookSnapshotForDelivery(
							tx,
							delivery,
						)
						if err != nil {
							return err
						}
						frozenSecret = snapshot.Secret
						return nil
					},
				)
			},
		)
		if gateErr != nil {
			transportResult <- gateErr
			return
		}
		if frozenSecret == "" {
			transportResult <- errors.New(
				"legacy gate did not load frozen credential",
			)
			return
		}
		close(gateReturned)
		<-resumeTransport
		client := &http.Client{
			Transport: webhookAttemptRoundTripper(func(
				*http.Request,
			) (*http.Response, error) {
				transportCalls.Add(1)
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Header:     make(http.Header),
					Body:       io.NopCloser(bytes.NewReader(nil)),
				}, nil
			}),
		}
		request, requestErr := http.NewRequestWithContext(
			context.Background(),
			http.MethodPost,
			"https://loopback.invalid/webhook",
			nil,
		)
		if requestErr != nil {
			transportResult <- requestErr
			return
		}
		response, requestErr := client.Do(request)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		transportResult <- requestErr
	}()
	select {
	case <-gateReturned:
	case err := <-transportResult:
		t.Fatalf("legacy second gate returned early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for legacy second gate")
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
	revoke, err := service.EmergencyRevokeWebhook(
		humanContext,
		postgresLifecycleConfigAActiveID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if revoke.ExpiredDeliveries != 0 ||
		revoke.InFlightDeliveries != 1 ||
		revoke.ShreddedSnapshots != 1 {
		t.Fatalf("legacy reclaim revoke result = %+v", revoke)
	}
	current := fixture.loadDelivery(t, legacyClaimed.ID)
	if current.Status != models.OutboxDeliveryProcessing ||
		current.DispatchStartedAt != nil {
		t.Fatalf("legacy in-flight delivery was falsely recalled: %+v", current)
	}
	var snapshot models.WebhookDeliverySnapshot
	if err := fixture.adminScoped.Where(
		"id = ?",
		pair.snapshot.ID,
	).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot.CredentialShreddedAt == nil ||
		snapshot.Secret != "" ||
		snapshot.PreviousSecret != "" ||
		snapshot.PreviousSecretExpiresAt != nil ||
		snapshot.AccessToken != "" {
		t.Fatalf("legacy in-flight snapshot was not shredded: %+v", snapshot)
	}
	if transportCalls.Load() != 0 {
		t.Fatal("legacy transport began before the explicit release")
	}
	release()
	select {
	case transportErr := <-transportResult:
		if transportErr != nil {
			t.Fatal(transportErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for legacy transport")
	}
	if transportCalls.Load() != 1 {
		t.Fatalf("legacy transport calls = %d, want 1", transportCalls.Load())
	}
}

type postgresWebhookDispatchAttempt struct {
	fixture        *webhookOutboxLifecyclePostgresFixture
	pair           postgresLifecyclePair
	claim          WebhookOutboxAttemptClaim
	workerContext  context.Context
	humanContext   context.Context
	notifications  *NotificationService
	transportCalls atomic.Int32
}

func newPostgresWebhookDispatchAttempt(
	t *testing.T,
	fixture *webhookOutboxLifecyclePostgresFixture,
) *postgresWebhookDispatchAttempt {
	t.Helper()
	fixture.clearRows(t)
	claimNow := time.Now().UTC().Add(2 * time.Second).Truncate(
		time.Microsecond,
	)
	expiresAt := claimNow.Add(time.Hour)
	pair := fixture.seedPair(
		t,
		fixture.projectA,
		models.OutboxDeliveryPending,
		expiresAt,
		"",
		nil,
		0,
	)
	workerContext := fixture.workerContext(
		t,
		context.Background(),
		fixture.projectA,
	)
	claimed, err := fixture.service(
		fixture.runtimeA,
		claimNow,
	).ClaimPendingOutbox(
		workerContext,
		"postgres-dispatch-worker",
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != pair.delivery.ID {
		t.Fatalf("prepared PostgreSQL claim = %+v", claimed)
	}
	pair.delivery = *claimed[0]
	persisted := fixture.loadDelivery(t, pair.delivery.ID)
	if !isWebhookDispatchPrepared(
		persisted.DispatchStartedAt,
		persisted.LockedAt,
	) {
		t.Fatalf("prepared marker did not persist: %+v", persisted)
	}
	pair.event.Type = string(models.WebhookEventSystemAlert)
	pair.event.DataSchema = "urn:chronodesk:schema:domain-event-data:v1"
	if err := fixture.adminScoped.Model(&models.DomainEvent{}).
		Where("id = ?", pair.event.ID).
		Updates(map[string]any{
			"type":        pair.event.Type,
			"data_schema": pair.event.DataSchema,
		}).Error; err != nil {
		t.Fatal(err)
	}
	pair.event = fixture.loadEvent(t, pair.event.ID)
	protector, err := security.NewKeyring(
		"postgres-dispatch",
		map[string][]byte{
			"postgres-dispatch": bytes.Repeat([]byte{0x71}, 32),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := protector.Seal(
		[]byte(testCustomWebhookSecret),
		security.FieldAAD(
			"webhook_configs",
			strconv.FormatUint(
				uint64(postgresLifecycleConfigAActiveID),
				10,
			),
			"secret",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.adminScoped.Exec(
		`UPDATE webhook_delivery_snapshots
		 SET secret = ?,
		     previous_secret = '',
		     previous_secret_expires_at = NULL,
		     access_token = ''
		 WHERE id = ?`,
		secret,
		pair.snapshot.ID,
	).Error; err != nil {
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
	claimRef := mustPostgresClaimRef(t, pair.delivery)
	attempt := &postgresWebhookDispatchAttempt{
		fixture:       fixture,
		pair:          pair,
		workerContext: workerContext,
		humanContext:  humanContext,
		claim: WebhookOutboxAttemptClaim{
			DeliveryID:          pair.delivery.ID,
			EventID:             pair.delivery.EventID,
			Scope:               fixture.projectA.Scope(),
			WorkerID:            claimRef.WorkerID,
			LockToken:           claimRef.LockToken,
			LockedAt:            claimRef.LockedAt,
			AttemptGeneration:   claimRef.Attempts,
			SnapshotDestination: pair.delivery.DestinationID,
			EffectiveDeadline:   expiresAt,
			CredentialExpiresAt: expiresAt,
		},
	}
	attempt.notifications = NewNotificationServiceWithClientFactory(
		fixture.runtimeA,
		protector,
		WebhookClientFactoryFunc(func(
			context.Context,
			*url.URL,
			time.Duration,
		) (*http.Client, error) {
			return &http.Client{
				Transport: webhookAttemptRoundTripper(func(
					*http.Request,
				) (*http.Response, error) {
					attempt.transportCalls.Add(1)
					return &http.Response{
						StatusCode: http.StatusNoContent,
						Header:     make(http.Header),
						Body: io.NopCloser(
							bytes.NewReader(nil),
						),
					}, nil
				}),
			}, nil
		}),
	)
	state, gateErr := attempt.notifications.validateWebhookOutboxAttemptGate(
		workerContext,
		attempt.claim,
	)
	if gateErr != nil {
		t.Fatalf("initial dispatch gate: %v", gateErr)
	}
	persistedEvent := CloudEventFromModel(&state.event)
	callerEvent := CloudEventFromModel(&pair.event)
	if !webhookCallerEventMatchesPersisted(
		&callerEvent,
		&persistedEvent,
	) {
		t.Fatal("PostgreSQL dispatch caller event did not match persistence")
	}
	snapshotConfig, configErr := state.snapshot.WebhookConfig()
	if configErr != nil {
		t.Fatalf("PostgreSQL dispatch snapshot config: %v", configErr)
	}
	if revealErr := attempt.notifications.revealWebhookSecrets(
		&snapshotConfig,
	); revealErr != nil {
		t.Fatalf("PostgreSQL dispatch secret reveal: %v", revealErr)
	}
	attempt.notifications.CloseWebhookAttemptAuditsAndWait()
	return attempt
}

func (attempt *postgresWebhookDispatchAttempt) send() OutboxAttemptResult {
	caller := CloudEventFromModel(&attempt.pair.event)
	return attempt.notifications.SendWebhookSnapshotOutboxAttemptResult(
		attempt.workerContext,
		attempt.claim,
		&caller,
	)
}

func awaitWebhookDispatchSignal(
	t *testing.T,
	signal <-chan struct{},
	name string,
) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func awaitWebhookDispatchSignalOrResult(
	t *testing.T,
	signal <-chan struct{},
	result <-chan OutboxAttemptResult,
	name string,
) {
	t.Helper()
	select {
	case <-signal:
	case early := <-result:
		t.Fatalf(
			"Webhook attempt returned before %s: %+v",
			name,
			early,
		)
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func awaitWebhookDispatchResult(
	t *testing.T,
	result <-chan OutboxAttemptResult,
) OutboxAttemptResult {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Webhook dispatch result")
		return OutboxAttemptResult{}
	}
}
