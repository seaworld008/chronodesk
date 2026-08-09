package services

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestWebhookTestCommandCommitsSnapshotEventAndOutboxWithoutHTTP(
	t *testing.T,
) {
	fixture := newNotificationWebhookTestCommandFixture(
		t,
		models.ProjectRoleManager,
	)

	receipt, err := fixture.service.TestWebhook(
		fixture.ctx,
		fixture.scope,
		fixture.config.ID,
	)
	if err != nil {
		t.Fatalf("queue webhook test delivery: %v", err)
	}
	if fixture.httpAttempts.Load() != 0 {
		t.Fatalf(
			"Human command performed %d synchronous HTTP attempts",
			fixture.httpAttempts.Load(),
		)
	}
	if receipt == nil ||
		receipt.Status != "queued" ||
		!receipt.Queued ||
		receipt.Delivered ||
		receipt.ConfigID != fixture.config.ID ||
		receipt.OperationID == "" ||
		receipt.EventID == "" ||
		receipt.DeliveryID == "" ||
		receipt.SnapshotID == "" ||
		receipt.ConfigurationVersion == "" {
		t.Fatalf("invalid queued receipt: %+v", receipt)
	}

	var snapshot models.WebhookDeliverySnapshot
	if err := fixture.db.Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		receipt.SnapshotID,
		fixture.scope.OrganizationID,
		fixture.scope.ProjectID,
	).Take(&snapshot).Error; err != nil {
		t.Fatalf("load committed webhook snapshot: %v", err)
	}
	if snapshot.EventID != receipt.EventID ||
		snapshot.ConfigID != fixture.config.ID ||
		snapshot.WebhookURL != fixture.config.WebhookURL ||
		snapshot.Secret != fixture.config.Secret ||
		!snapshot.ConfigUpdatedAt.Equal(fixture.config.UpdatedAt.UTC()) {
		t.Fatalf("snapshot did not freeze the locked configuration: %+v", snapshot)
	}

	var event models.DomainEvent
	if err := fixture.db.Where("id = ?", receipt.EventID).
		Take(&event).Error; err != nil {
		t.Fatalf("load committed webhook test event: %v", err)
	}
	if event.Type != eventcontract.SystemAlertEventType ||
		event.ActorType != models.ActorTypeHuman ||
		event.ActorID != models.HumanActor(fixture.user.ID).ID ||
		event.ConfigurationVersion != receipt.ConfigurationVersion ||
		event.OrganizationID != fixture.scope.OrganizationID ||
		event.ProjectID != fixture.scope.ProjectID {
		t.Fatalf("unexpected webhook test DomainEvent: %+v", event)
	}
	if strings.Contains(string(event.Data), fixture.config.Secret) ||
		strings.Contains(string(event.Data), fixture.config.WebhookURL) {
		t.Fatalf("DomainEvent leaked webhook credentials or destination: %s", event.Data)
	}
	var ledgerEntry models.AuditLedgerEntry
	if err := fixture.db.Where(
		"domain_event_id = ? AND organization_id = ? AND project_id = ?",
		event.ID,
		fixture.scope.OrganizationID,
		fixture.scope.ProjectID,
	).Take(&ledgerEntry).Error; err != nil {
		t.Fatalf("load committed webhook test audit ledger entry: %v", err)
	}
	if ledgerEntry.EventType != event.Type ||
		ledgerEntry.ActorType != event.ActorType ||
		ledgerEntry.ActorID != event.ActorID ||
		ledgerEntry.ConfigurationVersion != event.ConfigurationVersion {
		t.Fatalf(
			"audit ledger did not retain webhook test provenance: %+v",
			ledgerEntry,
		)
	}

	var delivery models.OutboxDelivery
	if err := fixture.db.Where("id = ?", receipt.DeliveryID).
		Take(&delivery).Error; err != nil {
		t.Fatalf("load committed webhook Outbox delivery: %v", err)
	}
	if delivery.EventID != event.ID ||
		delivery.DestinationType != "webhook" ||
		delivery.DestinationID != webhookSnapshotDestinationPrefix+snapshot.ID ||
		delivery.Status != models.OutboxDeliveryPending ||
		delivery.MaxAttempts != fixture.config.RetryCount+1 {
		t.Fatalf("unexpected webhook Outbox delivery: %+v", delivery)
	}
	if delivery.ExpiresAt == nil ||
		!delivery.ExpiresAt.Equal(snapshot.CredentialExpiresAt) ||
		!snapshot.CredentialExpiresAt.Equal(
			event.Time.Add(models.WebhookDeliveryCredentialLifetime),
		) {
		t.Fatalf(
			"Human webhook test deadline mismatch: event=%s snapshot=%s delivery=%v",
			event.Time,
			snapshot.CredentialExpiresAt,
			delivery.ExpiresAt,
		)
	}
	if fixture.clockCalls.Load() != 1 {
		t.Fatalf(
			"Human webhook test transaction clock called %d times, want 1",
			fixture.clockCalls.Load(),
		)
	}
	if !event.Time.Equal(fixture.txNow) {
		t.Fatalf(
			"Human webhook test event time=%s, want %s",
			event.Time,
			fixture.txNow,
		)
	}
}

func TestWebhookTestCommandAllowsInactiveWithoutEnablingOrdinaryDelivery(
	t *testing.T,
) {
	fixture := newNotificationWebhookTestCommandFixture(
		t,
		models.ProjectRoleManager,
	)
	if err := fixture.db.Model(&models.WebhookConfig{}).
		Where("id = ?", fixture.config.ID).
		Update("status", models.WebhookStatusInactive).Error; err != nil {
		t.Fatal(err)
	}
	fixture.config.Status = models.WebhookStatusInactive

	targets, err := fixture.service.ListWebhookOutboxTargets(
		fixture.ctx,
		fixture.scope,
		models.WebhookEventSystemAlert,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 {
		t.Fatalf("inactive webhook became ordinary delivery target: %+v", targets)
	}
	if _, err := models.NewWebhookDeliverySnapshot(
		fixture.config,
		"ordinary-event",
		time.Now().UTC().Add(models.WebhookDeliveryCredentialLifetime),
	); err == nil {
		t.Fatal("ordinary snapshot accepted inactive webhook")
	}

	receipt, err := fixture.service.TestWebhook(
		fixture.ctx,
		fixture.scope,
		fixture.config.ID,
	)
	if err != nil {
		t.Fatalf("queue inactive webhook test delivery: %v", err)
	}
	if receipt == nil || !receipt.Queued || receipt.ConfigID != fixture.config.ID {
		t.Fatalf("inactive webhook test receipt=%+v", receipt)
	}
}

func TestWebhookTestCommandRollsBackSnapshotWhenEventAppendFails(t *testing.T) {
	fixture := newNotificationWebhookTestCommandFixture(
		t,
		models.ProjectRoleAdmin,
	)
	const callbackName = "test:fail_webhook_test_domain_event"
	if err := fixture.db.Callback().Create().Before("gorm:create").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Table == "domain_events" {
				_ = tx.AddError(errors.New("injected domain event failure"))
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Create().Remove(callbackName)
	})

	receipt, err := fixture.service.TestWebhook(
		fixture.ctx,
		fixture.scope,
		fixture.config.ID,
	)
	if err == nil || receipt != nil {
		t.Fatalf("event append failure returned receipt=%+v err=%v", receipt, err)
	}
	if fixture.httpAttempts.Load() != 0 {
		t.Fatalf(
			"failed command performed %d HTTP attempts",
			fixture.httpAttempts.Load(),
		)
	}
	for name, model := range map[string]any{
		"snapshots":  &models.WebhookDeliverySnapshot{},
		"events":     &models.DomainEvent{},
		"deliveries": &models.OutboxDelivery{},
	} {
		var count int64
		if err := fixture.db.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("failed command committed %d %s", count, name)
		}
	}
}

func TestWebhookTestCommandRevalidatesRevokedHumanBeforeIntent(t *testing.T) {
	fixture := newNotificationWebhookTestCommandFixture(
		t,
		models.ProjectRoleManager,
	)
	if err := fixture.db.Model(&models.ProjectMembership{}).
		Where(
			"project_id = ? AND user_id = ?",
			fixture.scope.ProjectID,
			fixture.user.ID,
		).
		Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}

	receipt, err := fixture.service.TestWebhook(
		fixture.ctx,
		fixture.scope,
		fixture.config.ID,
	)
	if !errors.Is(err, ErrWebhookTestAccessDenied) ||
		!errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("revoked Human error = %v, want access denied", err)
	}
	if receipt != nil {
		t.Fatalf("revoked Human received receipt: %+v", receipt)
	}
	if fixture.httpAttempts.Load() != 0 {
		t.Fatalf(
			"revoked Human performed %d HTTP attempts",
			fixture.httpAttempts.Load(),
		)
	}
	var deliveries int64
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Count(&deliveries).Error; err != nil {
		t.Fatal(err)
	}
	if deliveries != 0 {
		t.Fatalf("revoked Human committed %d Outbox deliveries", deliveries)
	}
}

type notificationWebhookTestCommandFixture struct {
	db           *gorm.DB
	service      *NotificationService
	native       *AgentNativeService
	scope        models.ProjectScope
	user         models.User
	config       models.WebhookConfig
	ctx          context.Context
	httpAttempts *atomic.Int32
	clockCalls   *atomic.Int32
	txNow        time.Time
}

func newNotificationWebhookTestCommandFixture(
	t *testing.T,
	role models.ProjectRole,
) notificationWebhookTestCommandFixture {
	t.Helper()
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, strings.ToLower(string(role)))
	scope := seedNotificationProjectMembership(t, db, user.ID)
	ensureTestHumanProjectRole(
		t,
		db,
		notificationTestOperationContext(t, scope, models.HumanActor(user.ID)),
		user.ID,
		role,
	)
	projects, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	auditLedger, err := NewAuditLedgerService(db)
	if err != nil {
		t.Fatal(err)
	}
	txNow := time.Date(
		2026,
		8,
		10,
		14,
		15,
		16,
		987654321,
		time.UTC,
	)
	clockCalls := &atomic.Int32{}
	native := NewAgentNativeService(db, AgentNativeOptions{
		AuditLedger: auditLedger,
		Now: func() time.Time {
			clockCalls.Add(1)
			return txNow
		},
	})
	httpAttempts := &atomic.Int32{}
	service := NewNotificationServiceWithClientFactory(
		db,
		nil,
		WebhookClientFactoryFunc(func(
			context.Context,
			*url.URL,
			time.Duration,
		) (*http.Client, error) {
			httpAttempts.Add(1)
			return nil, errors.New("HTTP is forbidden in the Human command")
		}),
	)
	service.ConfigureWebhookTestCommands(projects, native)
	config := models.WebhookConfig{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "queued test delivery",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://original.example.test/chronodesk",
		Status:         models.WebhookStatusActive,
		Secret:         "sealed-test-envelope",
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventSystemAlert,
		},
		RetryCount: 2,
		CreatedBy:  user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	return notificationWebhookTestCommandFixture{
		db:      db,
		service: service,
		native:  native,
		scope:   scope,
		user:    user,
		config:  config,
		ctx: notificationTestOperationContext(
			t,
			scope,
			models.HumanActor(user.ID),
		),
		httpAttempts: httpAttempts,
		clockCalls:   clockCalls,
		txNow:        txNow,
	}
}
