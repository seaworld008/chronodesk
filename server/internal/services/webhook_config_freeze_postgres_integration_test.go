package services

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestConfiguredWebhookFreezeSerializesOrdinaryMutationPostgres(
	t *testing.T,
) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL webhook config barrier evidence",
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
			t.Fatal("PostgreSQL webhook barrier test requires a loopback target")
		}
	}
	schemaName := fmt.Sprintf(
		"chronodesk_webhook_barrier_%d",
		time.Now().UnixNano(),
	)
	quotedSchema := quoteWebhookPostgresIdentifier(schemaName)
	silentConfig := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	admin, err := gorm.Open(postgres.Open(rawDSN), silentConfig)
	if err != nil {
		t.Fatalf("open PostgreSQL barrier administrator: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	schemaCreated := false
	t.Cleanup(func() {
		if schemaCreated {
			_ = admin.Exec(
				"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
			).Error
		}
		_ = adminSQL.Close()
	})
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatal(err)
	}
	schemaCreated = true
	scopedURL := *parsed
	query := scopedURL.Query()
	query.Set("search_path", schemaName)
	scopedURL.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(scopedURL.String()), silentConfig)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = sqlDB.Close() })
	tableOnly := db.Session(&gorm.Session{NewDB: true})
	tableOnly.Config.IgnoreRelationshipsWhenMigrating = true
	if err := tableOnly.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.WebhookDeliverySnapshot{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		ID:           7,
		Username:     "webhook-barrier-owner",
		Email:        "webhook-barrier-owner@example.test",
		PasswordHash: "test-only",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	scope := models.ProjectScope{OrganizationID: 11, ProjectID: 22}
	oldURL := "https://old-barrier.example.test/events"
	newURL := "https://new-barrier.example.test/events"
	config := models.WebhookConfig{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "barrier",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     oldURL,
		Status:         models.WebhookStatusActive,
		Secret:         "sealed-old-secret",
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventTicketCreated,
		},
		CreatedBy: user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	txNow := time.Date(
		2026,
		8,
		10,
		15,
		16,
		17,
		987654000,
		time.UTC,
	)
	service := NewAgentNativeService(db, AgentNativeOptions{
		DefaultOutboxTargets: []OutboxTarget{{
			Type: "webhook",
			ID:   "configured",
		}},
		Now: func() time.Time { return txNow },
	})
	actor := models.SystemActor("webhook-barrier")
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:  scope,
		Actor:  actor,
		Source: SourceProtocolWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	configLocked := make(chan struct{})
	releaseFreeze := make(chan struct{})
	const callbackName = "test:webhook_config_for_share_barrier"
	if err := db.Callback().Query().After("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement == nil ||
				tx.Statement.Table != "webhook_configs" {
				return
			}
			select {
			case <-configLocked:
			default:
				close(configLocked)
			}
			<-releaseFreeze
		},
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(callbackName)
	})
	type appendResult struct {
		event *models.DomainEvent
		err   error
	}
	appendDone := make(chan appendResult, 1)
	go func() {
		event, appendErr := service.createDomainEvent(
			t,
			ctx,
			DomainEventInput{
				Type:            "io.chronodesk.ticket.created.v1",
				Subject:         "ticket/1",
				Actor:           actor,
				ResourceVersion: 1,
				Data:            map[string]any{"ticket_id": 1},
			},
			nil,
		)
		appendDone <- appendResult{event: event, err: appendErr}
	}()
	select {
	case <-configLocked:
	case <-time.After(2 * time.Second):
		t.Fatal("configured fan-out did not acquire the config barrier")
	}
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- db.Model(&models.WebhookConfig{}).
			Where("id = ?", config.ID).
			UpdateColumns(map[string]any{
				"webhook_url": newURL,
				"secret":      "sealed-new-secret",
				"status":      models.WebhookStatusDisabled,
			}).Error
	}()
	select {
	case updateErr := <-updateDone:
		t.Fatalf(
			"ordinary mutation bypassed config FOR SHARE barrier: %v",
			updateErr,
		)
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseFreeze)
	var appended appendResult
	select {
	case appended = <-appendDone:
	case <-time.After(2 * time.Second):
		t.Fatal("configured fan-out did not finish after barrier release")
	}
	if appended.err != nil || appended.event == nil {
		t.Fatalf(
			"configured fan-out event=%+v err=%v",
			appended.event,
			appended.err,
		)
	}
	select {
	case updateErr := <-updateDone:
		if updateErr != nil {
			t.Fatalf("ordinary mutation after fan-out: %v", updateErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary mutation remained blocked after fan-out commit")
	}
	var snapshot models.WebhookDeliverySnapshot
	if err := db.Where(
		"event_id = ? AND config_id = ?",
		appended.event.ID,
		config.ID,
	).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	var delivery models.OutboxDelivery
	if err := db.Where(
		"event_id = ? AND destination_type = ?",
		appended.event.ID,
		"webhook",
	).Take(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot.WebhookURL != oldURL ||
		snapshot.Secret != "sealed-old-secret" ||
		delivery.ExpiresAt == nil ||
		!delivery.ExpiresAt.Equal(snapshot.CredentialExpiresAt) {
		t.Fatalf(
			"fan-out captured mixed config or deadline: snapshot=%+v delivery=%+v",
			snapshot,
			delivery,
		)
	}
	if err := db.Delete(&models.WebhookConfig{}, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	var retained models.WebhookDeliverySnapshot
	if err := db.Where("id = ?", snapshot.ID).Take(&retained).Error; err != nil {
		t.Fatal(err)
	}
	if retained.WebhookURL != oldURL ||
		retained.Secret != "sealed-old-secret" ||
		!retained.CredentialExpiresAt.Equal(
			txNow.Add(models.WebhookDeliveryCredentialLifetime),
		) {
		t.Fatalf(
			"ordinary disable/delete changed committed snapshot: %+v",
			retained,
		)
	}
}
