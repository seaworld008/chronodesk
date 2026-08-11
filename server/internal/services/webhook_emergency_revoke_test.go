package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestWebhookEmergencyRevokeTransitionsOnlyRevocableDeliveries(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 11, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	adminContext := webhookEmergencyAdminContext(
		t,
		fixture,
		models.ProjectRoleAdmin,
	)

	type lifecycleRow struct {
		delivery models.OutboxDelivery
		snapshot models.WebhookDeliverySnapshot
		event    models.DomainEvent
	}
	rows := map[models.OutboxDeliveryStatus]lifecycleRow{
		models.OutboxDeliveryPending: {
			delivery: fixture.delivery,
			snapshot: fixture.snapshot,
			event:    fixture.event,
		},
	}
	for _, status := range []models.OutboxDeliveryStatus{
		models.OutboxDeliveryFailed,
		models.OutboxDeliveryDead,
		models.OutboxDeliveryProcessing,
		models.OutboxDeliverySucceeded,
		models.OutboxDeliveryExpired,
	} {
		delivery, snapshot, event := fixture.createIntent(
			t,
			string(status),
		)
		updates := map[string]any{"status": status}
		switch status {
		case models.OutboxDeliveryProcessing:
			lockToken := "019fee69-720c-7023-ae63-fcaf437561b5"
			updates["attempts"] = 1
			updates["locked_at"] = now
			updates["locked_by"] = "already-sending"
			updates["lock_token"] = lockToken
		case models.OutboxDeliverySucceeded:
			deliveredAt := now.Add(-time.Minute)
			updates["delivered_at"] = deliveredAt
		case models.OutboxDeliveryExpired:
			expiredAt := now.Add(-time.Minute)
			updates["expired_at"] = expiredAt
		}
		if err := fixture.db.Model(&models.OutboxDelivery{}).
			Where("id = ?", delivery.ID).
			Updates(updates).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.db.First(
			&delivery,
			"id = ?",
			delivery.ID,
		).Error; err != nil {
			t.Fatal(err)
		}
		rows[status] = lifecycleRow{
			delivery: delivery,
			snapshot: snapshot,
			event:    event,
		}
	}

	// A revoke transition must never rewrite the parent publication history.
	publishedAt := now.Add(-2 * time.Hour)
	if err := fixture.db.Model(&models.DomainEvent{}).
		Where("id = ?", rows[models.OutboxDeliveryFailed].event.ID).
		Update("published_at", publishedAt).Error; err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.EmergencyRevokeWebhook(
		adminContext,
		fixture.config.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfigID != fixture.config.ID ||
		result.Status != models.WebhookStatusDisabled ||
		result.ExpiredDeliveries != 3 ||
		result.InFlightDeliveries != 1 ||
		result.ShreddedSnapshots != len(rows) {
		t.Fatalf("emergency revoke result = %+v", result)
	}

	var config models.WebhookConfig
	if err := fixture.db.Unscoped().First(
		&config,
		fixture.config.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if config.Status != models.WebhookStatusDisabled ||
		config.UpdatedBy == nil ||
		*config.UpdatedBy != fixture.config.CreatedBy {
		t.Fatalf("webhook config was not disabled by the Human actor: %+v", config)
	}

	for originalStatus, row := range rows {
		var delivery models.OutboxDelivery
		if err := fixture.db.First(
			&delivery,
			"id = ?",
			row.delivery.ID,
		).Error; err != nil {
			t.Fatal(err)
		}
		wantStatus := originalStatus
		switch originalStatus {
		case models.OutboxDeliveryPending,
			models.OutboxDeliveryFailed,
			models.OutboxDeliveryDead:
			wantStatus = models.OutboxDeliveryExpired
			if delivery.ExpiredAt == nil ||
				delivery.DeliveredAt != nil ||
				delivery.LockedAt != nil ||
				delivery.LockedBy != "" ||
				delivery.LockToken != nil {
				t.Fatalf(
					"revoked delivery %s retained mutable attempt state: %+v",
					originalStatus,
					delivery,
				)
			}
		case models.OutboxDeliveryProcessing:
			if delivery.LockedAt == nil ||
				delivery.LockedBy != "already-sending" ||
				delivery.LockToken == nil {
				t.Fatalf("in-flight delivery was recalled: %+v", delivery)
			}
		}
		if delivery.Status != wantStatus {
			t.Fatalf(
				"delivery %s status = %s, want %s",
				originalStatus,
				delivery.Status,
				wantStatus,
			)
		}

		var snapshot models.WebhookDeliverySnapshot
		if err := fixture.db.First(
			&snapshot,
			"id = ?",
			row.snapshot.ID,
		).Error; err != nil {
			t.Fatal(err)
		}
		assertSnapshotShredded(
			t,
			snapshot,
			models.WebhookCredentialShredReasonRevoked,
		)
	}

	var unpublished models.DomainEvent
	if err := fixture.db.First(
		&unpublished,
		"id = ?",
		rows[models.OutboxDeliveryPending].event.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if unpublished.PublishedAt != nil {
		t.Fatalf(
			"expired delivery published its parent event at %v",
			unpublished.PublishedAt,
		)
	}
	var published models.DomainEvent
	if err := fixture.db.First(
		&published,
		"id = ?",
		rows[models.OutboxDeliveryFailed].event.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if published.PublishedAt == nil ||
		!published.PublishedAt.Equal(publishedAt) {
		t.Fatalf(
			"revoke rewrote immutable parent publication history: %v",
			published.PublishedAt,
		)
	}

	repeated, err := fixture.service.EmergencyRevokeWebhook(
		adminContext,
		fixture.config.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.ConfigID != fixture.config.ID ||
		repeated.Status != models.WebhookStatusDisabled ||
		repeated.ExpiredDeliveries != 0 ||
		repeated.InFlightDeliveries != 1 ||
		repeated.ShreddedSnapshots != 0 {
		t.Fatalf("repeated emergency revoke = %+v", repeated)
	}

	claimed, err := fixture.service.ClaimPendingOutbox(
		fixture.worker,
		"post-revoke-claim",
		20,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, delivery := range claimed {
		if delivery.DestinationType == "webhook" {
			t.Fatalf("revoked Webhook delivery was claimed: %+v", delivery)
		}
	}
}

func TestWebhookEmergencyRevokeRequiresExactProjectAdminAndScope(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 11, 10, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	managerContext := webhookEmergencyAdminContext(
		t,
		fixture,
		models.ProjectRoleManager,
	)
	var actor models.User
	if err := fixture.db.First(
		&actor,
		fixture.config.CreatedBy,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&actor).Update(
		"platform_role",
		models.PlatformRolePlatformAdmin,
	).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.EmergencyRevokeWebhook(
		managerContext,
		fixture.config.ID,
	); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf(
			"platform administrator with manager membership error = %v",
			err,
		)
	}

	foreignContext := webhookEmergencyForeignProjectContext(
		t,
		fixture,
		fixture.config.CreatedBy,
	)
	_, crossProjectErr := fixture.service.EmergencyRevokeWebhook(
		foreignContext,
		fixture.config.ID,
	)
	_, missingErr := fixture.service.EmergencyRevokeWebhook(
		foreignContext,
		fixture.config.ID+100000,
	)
	if !errors.Is(crossProjectErr, ErrWebhookConfigNotFound) ||
		!errors.Is(missingErr, ErrWebhookConfigNotFound) ||
		crossProjectErr.Error() != missingErr.Error() {
		t.Fatalf(
			"cross-project=%v missing=%v, want identical non-leaking errors",
			crossProjectErr,
			missingErr,
		)
	}

	var config models.WebhookConfig
	if err := fixture.db.First(&config, fixture.config.ID).Error; err != nil {
		t.Fatal(err)
	}
	if config.Status != models.WebhookStatusActive {
		t.Fatalf("denied revoke changed config status to %s", config.Status)
	}
	var snapshot models.WebhookDeliverySnapshot
	if err := fixture.db.First(
		&snapshot,
		"id = ?",
		fixture.snapshot.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot.CredentialShreddedAt != nil {
		t.Fatal("denied revoke shredded snapshot credentials")
	}
}

func TestWebhookEmergencyRevokeRollsBackEveryMutation(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 11, 11, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	adminContext := webhookEmergencyAdminContext(
		t,
		fixture,
		models.ProjectRoleAdmin,
	)
	injected := errors.New("injected emergency shred failure")
	const callbackName = "test:webhook_emergency_revoke_shred_failure"
	if err := fixture.db.Callback().Update().
		Before("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement != nil &&
				tx.Statement.Table ==
					(models.WebhookDeliverySnapshot{}).TableName() {
				_ = tx.AddError(injected)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Update().Remove(callbackName)
	})

	_, err := fixture.service.EmergencyRevokeWebhook(
		adminContext,
		fixture.config.ID,
	)
	if !errors.Is(err, injected) {
		t.Fatalf("EmergencyRevokeWebhook() error = %v, want injected", err)
	}

	var config models.WebhookConfig
	if err := fixture.db.First(&config, fixture.config.ID).Error; err != nil {
		t.Fatal(err)
	}
	var delivery models.OutboxDelivery
	if err := fixture.db.First(
		&delivery,
		"id = ?",
		fixture.delivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot models.WebhookDeliverySnapshot
	if err := fixture.db.First(
		&snapshot,
		"id = ?",
		fixture.snapshot.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if config.Status != models.WebhookStatusActive ||
		delivery.Status != models.OutboxDeliveryPending ||
		delivery.ExpiredAt != nil ||
		snapshot.CredentialShreddedAt != nil ||
		snapshot.Secret == "" ||
		snapshot.PreviousSecret == "" ||
		snapshot.AccessToken == "" {
		t.Fatalf(
			"failed revoke partially committed config=%+v delivery=%+v snapshot=%+v",
			config,
			delivery,
			snapshot,
		)
	}
}

func webhookEmergencyAdminContext(
	t *testing.T,
	fixture *webhookOutboxLifecycleFixture,
	role models.ProjectRole,
) context.Context {
	t.Helper()
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  fixture.scope,
			Actor:  models.HumanActor(fixture.config.CreatedBy),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ensureTestHumanProjectRole(
		t,
		fixture.db,
		ctx,
		fixture.config.CreatedBy,
		role,
	)
	return ctx
}

func webhookEmergencyForeignProjectContext(
	t *testing.T,
	fixture *webhookOutboxLifecycleFixture,
	userID uint,
) context.Context {
	t.Helper()
	var project models.Project
	if err := fixture.db.First(
		&project,
		fixture.scope.ProjectID,
	).Error; err != nil {
		t.Fatal(err)
	}
	foreign := models.Project{
		OrganizationID: project.OrganizationID,
		BusinessUnitID: project.BusinessUnitID,
		Key:            models.ProjectKey("OTHER"),
		Name:           "Other",
		Status:         models.ProjectStatusActive,
	}
	if err := fixture.db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  foreign.Scope(),
			Actor:  models.HumanActor(userID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ensureTestHumanProjectRole(
		t,
		fixture.db,
		ctx,
		userID,
		models.ProjectRoleAdmin,
	)
	return ctx
}

func assertWebhookEmergencyResultSecretFree(
	t *testing.T,
	value string,
) {
	t.Helper()
	for _, forbidden := range []string{
		"sealed-current-envelope",
		"sealed-previous-envelope",
		"sealed-access-envelope",
		"https://",
	} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("emergency revoke output leaked %q", forbidden)
		}
	}
}
