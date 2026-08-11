package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestWebhookEmergencyRevokeSerializesClaimPostgres(t *testing.T) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL emergency revoke race evidence",
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
		t.Fatal("parse PostgreSQL emergency revoke integration DSN")
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("PostgreSQL emergency revoke target must be loopback")
		}
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	schemaName := "chronodesk_webhook_revoke_" + suffix
	quotedSchema := quoteWebhookPostgresIdentifier(schemaName)
	config := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	admin, err := gorm.Open(postgres.Open(rawDSN), config)
	if err != nil {
		t.Fatal("open PostgreSQL emergency revoke administrator")
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	var pools []*sql.DB
	schemaCreated := false
	t.Cleanup(func() {
		for _, pool := range pools {
			_ = pool.Close()
		}
		if schemaCreated {
			if dropErr := admin.Exec(
				"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
			).Error; dropErr != nil {
				t.Errorf("drop PostgreSQL emergency revoke schema: %v", dropErr)
			}
		}
		_ = adminSQL.Close()
	})
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatal(err)
	}
	schemaCreated = true

	openSchemaDB := func(applicationName string) *gorm.DB {
		t.Helper()
		databaseURL := *parsed
		query := databaseURL.Query()
		query.Set("search_path", schemaName)
		query.Set("application_name", applicationName)
		databaseURL.RawQuery = query.Encode()
		db, openErr := gorm.Open(postgres.Open(databaseURL.String()), config)
		if openErr != nil {
			t.Fatalf("open PostgreSQL %s connection", applicationName)
		}
		pool, poolErr := db.DB()
		if poolErr != nil {
			t.Fatal(poolErr)
		}
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		pools = append(pools, pool)
		return db
	}

	setupDB := openSchemaDB("webhook_revoke_setup_" + suffix)
	claimDB := openSchemaDB("webhook_revoke_claim_" + suffix)
	revokeDB := openSchemaDB("webhook_revoke_command_" + suffix)
	blockerDB := openSchemaDB("webhook_revoke_blocker_" + suffix)
	observerDB := openSchemaDB("webhook_revoke_observer_" + suffix)
	for _, db := range []*gorm.DB{claimDB, revokeDB, observerDB} {
		if err := scopeddb.Install(db); err != nil {
			t.Fatal(err)
		}
	}
	tableOnly := setupDB.Session(&gorm.Session{NewDB: true})
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

	now := time.Now().UTC().Truncate(time.Microsecond)
	publishedAt := now.Add(-2 * time.Minute)
	project := models.Project{
		ID:             201,
		PublicID:       "00000000-0000-7000-8000-000000000201",
		CreatedAt:      now,
		UpdatedAt:      now,
		OrganizationID: 21,
		BusinessUnitID: 1,
		Key:            "REVOKE",
		Name:           "Emergency Revoke",
		Status:         models.ProjectStatusActive,
	}
	user := models.User{
		ID:           27,
		CreatedAt:    now,
		UpdatedAt:    now,
		Username:     "webhook-revoke-admin",
		Email:        "webhook-revoke-admin@example.test",
		PasswordHash: "test-only-password-hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	membership := models.ProjectMembership{
		ID:        2701,
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
		ProjectID: project.ID,
		UserID:    user.ID,
		Role:      models.ProjectRoleAdmin,
		IsActive:  true,
	}
	webhook := models.WebhookConfig{
		ID:             2901,
		CreatedAt:      now,
		UpdatedAt:      now,
		OrganizationID: project.OrganizationID,
		ProjectID:      project.ID,
		Name:           "PostgreSQL emergency revoke",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://loopback-only.invalid/events",
		Status:         models.WebhookStatusActive,
		Secret:         "sealed-current-secret",
		PreviousSecret: "sealed-previous-secret",
		AccessToken:    "sealed-access-token",
		CreatedBy:      user.ID,
	}
	event := models.DomainEvent{
		ID:              uuid.NewString(),
		CreatedAt:       now,
		OrganizationID:  project.OrganizationID,
		ProjectID:       project.ID,
		SpecVersion:     "1.0",
		Source:          "urn:chronodesk:test:webhook-revoke",
		Type:            string(models.WebhookEventSystemAlert),
		Subject:         "system/webhook-revoke",
		Time:            now,
		DataContentType: "application/json",
		Data:            []byte(`{"kind":"test"}`),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "webhook-revoke-test",
		ResourceVersion: 1,
		PublishedAt:     &publishedAt,
	}
	snapshotUUID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	destinationID, err := models.WebhookDeliverySnapshotDestinationID(
		snapshotUUID.String(),
	)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(models.WebhookDeliveryCredentialLifetime)
	delivery := models.OutboxDelivery{
		ID:              uuid.NewString(),
		CreatedAt:       now,
		UpdatedAt:       now,
		OrganizationID:  project.OrganizationID,
		ProjectID:       project.ID,
		EventID:         event.ID,
		DestinationType: "webhook",
		DestinationID:   destinationID,
		Status:          models.OutboxDeliveryPending,
		MaxAttempts:     8,
		NextAttemptAt:   now.Add(-time.Minute),
		ExpiresAt:       &expiresAt,
	}
	snapshot := models.WebhookDeliverySnapshot{
		ID:                  snapshotUUID.String(),
		CreatedAt:           now,
		OrganizationID:      project.OrganizationID,
		ProjectID:           project.ID,
		ConfigID:            webhook.ID,
		EventID:             event.ID,
		ConfigUpdatedAt:     webhook.UpdatedAt,
		Provider:            webhook.Provider,
		WebhookURL:          webhook.WebhookURL,
		Secret:              webhook.Secret,
		PreviousSecret:      webhook.PreviousSecret,
		AccessToken:         webhook.AccessToken,
		CredentialExpiresAt: expiresAt,
		EnabledEvents:       `["io.chronodesk.system.alert.v1"]`,
		MessageFormat:       "markdown",
		RetryCount:          3,
		RetryInterval:       60,
		TimeoutSeconds:      30,
		RateLimit:           60,
		RateLimitWindow:     60,
	}
	if err := setupDB.Transaction(func(tx *gorm.DB) error {
		for _, row := range []any{
			&project,
			&user,
			&membership,
			&webhook,
			&event,
			&delivery,
			&snapshot,
		} {
			if createErr := tx.Create(row).Error; createErr != nil {
				return createErr
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	blocker := blockerDB.Begin()
	if blocker.Error != nil {
		t.Fatal(blocker.Error)
	}
	blockerOpen := true
	t.Cleanup(func() {
		if blockerOpen {
			_ = blocker.Rollback().Error
		}
	})
	var lockedDeliveryID string
	if err := blocker.Raw(
		`SELECT id FROM outbox_deliveries WHERE id = ? FOR UPDATE`,
		delivery.ID,
	).Scan(&lockedDeliveryID).Error; err != nil {
		t.Fatal(err)
	}
	if lockedDeliveryID != delivery.ID {
		t.Fatal("failed to acquire PostgreSQL delivery race gate")
	}
	claimConfigLocked := make(chan struct{})
	var claimConfigLockOnce sync.Once
	claimConfigCallback :=
		"test:webhook_emergency_revoke_config_lock_" + suffix
	if err := claimDB.Callback().Query().After("gorm:query").Register(
		claimConfigCallback,
		func(tx *gorm.DB) {
			if tx.Statement != nil &&
				tx.Statement.Table == "webhook_configs" {
				claimConfigLockOnce.Do(func() {
					close(claimConfigLocked)
				})
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = claimDB.Callback().Query().Remove(claimConfigCallback)
	})

	raceCtx, cancel := context.WithTimeout(
		context.Background(),
		8*time.Second,
	)
	defer cancel()
	workerCtx, err := EnsureSystemProjectOperationContext(
		raceCtx,
		project.Scope(),
		models.SystemActor(outboxSystemActorID),
		"postgres-webhook-revoke-worker",
		"postgres-webhook-revoke-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	humanCtx, err := WithOperationContext(
		raceCtx,
		OperationContext{
			Scope:  project.Scope(),
			Actor:  models.HumanActor(user.ID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	claimService := NewAgentNativeService(claimDB, AgentNativeOptions{
		Now: func() time.Time { return now },
	})
	revokeService := NewAgentNativeService(revokeDB, AgentNativeOptions{
		Now: func() time.Time { return now.Add(time.Second) },
	})
	claimDone := make(chan struct {
		rows []*models.OutboxDelivery
		err  error
	}, 1)
	go func() {
		rows, claimErr := claimService.ClaimPendingOutbox(
			workerCtx,
			"postgres-webhook-revoke-worker",
			1,
			time.Minute,
		)
		claimDone <- struct {
			rows []*models.OutboxDelivery
			err  error
		}{rows: rows, err: claimErr}
	}()
	select {
	case <-claimConfigLocked:
	case claimResult := <-claimDone:
		t.Fatalf(
			"PostgreSQL claim ended before config lock rows=%d err=%v",
			len(claimResult.rows),
			claimResult.err,
		)
	case <-raceCtx.Done():
		t.Fatal("PostgreSQL claim did not acquire its config SHARE lock")
	}
	waitForWebhookPostgresLock(
		t,
		admin,
		"webhook_revoke_claim_"+suffix,
		"outbox_deliveries",
	)

	revokeDone := make(chan struct {
		result WebhookEmergencyRevokeResult
		err    error
	}, 1)
	go func() {
		result, revokeErr := revokeService.EmergencyRevokeWebhook(
			humanCtx,
			webhook.ID,
		)
		revokeDone <- struct {
			result WebhookEmergencyRevokeResult
			err    error
		}{result: result, err: revokeErr}
	}()
	waitForWebhookPostgresLock(
		t,
		admin,
		"webhook_revoke_command_"+suffix,
		"webhook_configs",
	)

	if err := blocker.Commit().Error; err != nil {
		t.Fatal(err)
	}
	blockerOpen = false
	var claimResult struct {
		rows []*models.OutboxDelivery
		err  error
	}
	select {
	case claimResult = <-claimDone:
	case <-raceCtx.Done():
		t.Fatal("PostgreSQL claim did not complete after race gate release")
	}
	var revokeResult struct {
		result WebhookEmergencyRevokeResult
		err    error
	}
	select {
	case revokeResult = <-revokeDone:
	case <-raceCtx.Done():
		t.Fatal("PostgreSQL revoke did not complete after claim")
	}
	if claimResult.err != nil || len(claimResult.rows) != 1 {
		t.Fatalf(
			"PostgreSQL claim result rows=%d err=%v",
			len(claimResult.rows),
			claimResult.err,
		)
	}
	if revokeResult.err != nil ||
		revokeResult.result.ExpiredDeliveries != 0 ||
		revokeResult.result.InFlightDeliveries != 1 ||
		revokeResult.result.ShreddedSnapshots != 1 {
		t.Fatalf(
			"PostgreSQL revoke result=%+v err=%v",
			revokeResult.result,
			revokeResult.err,
		)
	}

	var currentWebhook models.WebhookConfig
	if err := observerDB.Unscoped().First(
		&currentWebhook,
		webhook.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	var currentDelivery models.OutboxDelivery
	if err := observerDB.Where(
		"id = ?",
		delivery.ID,
	).Take(&currentDelivery).Error; err != nil {
		t.Fatal(err)
	}
	var currentSnapshot models.WebhookDeliverySnapshot
	if err := observerDB.Where(
		"id = ?",
		snapshot.ID,
	).Take(&currentSnapshot).Error; err != nil {
		t.Fatal(err)
	}
	var currentEvent models.DomainEvent
	if err := observerDB.Where(
		"id = ?",
		event.ID,
	).Take(&currentEvent).Error; err != nil {
		t.Fatal(err)
	}
	if currentWebhook.Status != models.WebhookStatusDisabled ||
		currentDelivery.Status != models.OutboxDeliveryProcessing ||
		currentSnapshot.CredentialShreddedAt == nil ||
		currentSnapshot.CredentialShredReason == nil ||
		*currentSnapshot.CredentialShredReason !=
			models.WebhookCredentialShredReasonRevoked ||
		currentSnapshot.Secret != "" ||
		currentSnapshot.PreviousSecret != "" ||
		currentSnapshot.AccessToken != "" ||
		currentEvent.PublishedAt == nil ||
		!currentEvent.PublishedAt.Equal(publishedAt) {
		t.Fatalf(
			"PostgreSQL race state config=%s delivery=%s snapshot_reason=%v published_at=%v",
			currentWebhook.Status,
			currentDelivery.Status,
			currentSnapshot.CredentialShredReason,
			currentEvent.PublishedAt,
		)
	}

	var clientCreations atomic.Int32
	notifications := NewNotificationServiceWithClientFactory(
		observerDB,
		nil,
		WebhookClientFactoryFunc(func(
			context.Context,
			*url.URL,
			time.Duration,
		) (*http.Client, error) {
			clientCreations.Add(1)
			return nil, errors.New("HTTP client creation is forbidden after revoke")
		}),
	)
	claimRef, err := OutboxClaimRefFromDelivery(&currentDelivery)
	if err != nil {
		t.Fatal(err)
	}
	callerEvent := CloudEventFromModel(&currentEvent)
	httpResult := notifications.SendWebhookSnapshotOutboxAttemptResult(
		workerCtx,
		WebhookOutboxAttemptClaim{
			DeliveryID:          currentDelivery.ID,
			EventID:             currentDelivery.EventID,
			Scope:               project.Scope(),
			WorkerID:            claimRef.WorkerID,
			LockToken:           claimRef.LockToken,
			LockedAt:            claimRef.LockedAt,
			AttemptGeneration:   claimRef.Attempts,
			SnapshotDestination: currentDelivery.DestinationID,
			EffectiveDeadline:   now.Add(time.Minute),
			CredentialExpiresAt: expiresAt,
		},
		&callerEvent,
	)
	if httpResult.Kind != OutboxAttemptKnownFailure ||
		!errors.Is(httpResult.Err, ErrWebhookOutboxAttemptRejected) ||
		clientCreations.Load() != 0 {
		t.Fatalf(
			"PostgreSQL revoked HTTP gate result=%+v clients=%d",
			httpResult,
			clientCreations.Load(),
		)
	}
}

func waitForWebhookPostgresLock(
	t *testing.T,
	admin *gorm.DB,
	applicationName string,
	tableName string,
) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int64
		if err := admin.Raw(`
			SELECT COUNT(*)
			FROM pg_stat_activity
			WHERE application_name = ?
			  AND wait_event_type = 'Lock'
			  AND POSITION(? IN query) > 0
		`, applicationName, tableName).Scan(&waiting).Error; err != nil {
			t.Fatal(err)
		}
		if waiting == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf(
		"PostgreSQL application %s did not wait on %s",
		applicationName,
		tableName,
	)
}
