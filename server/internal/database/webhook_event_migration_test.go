package database

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openWebhookMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.User{
		ID:           1,
		Username:     "webhook-migration-owner",
		Email:        "webhook-migration-owner@example.test",
		PasswordHash: "hash",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func insertLegacyWebhookConfig(
	t *testing.T,
	db *gorm.DB,
	id uint,
	name string,
	events string,
) {
	t.Helper()
	if err := db.Table("webhook_configs").Create(map[string]any{
		"id":              id,
		"organization_id": 1,
		"project_id":      1,
		"created_at":      time.Now().UTC(),
		"updated_at":      time.Now().UTC(),
		"name":            name,
		"provider":        models.WebhookProviderCustom,
		"webhook_url":     "https://hooks.example.test/events",
		"status":          models.WebhookStatusActive,
		"enabled_events":  events,
		"filter_rules":    "",
		"created_by":      1,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestMigrateWebhookEventTaxonomyPreservesSubscriptionSemantics(t *testing.T) {
	db := openWebhookMigrationTestDB(t)
	insertLegacyWebhookConfig(t, db, 1, "resolved", `["ticket.resolved"]`)
	insertLegacyWebhookConfig(t, db, 2, "updated", `["ticket.updated"]`)
	insertLegacyWebhookConfig(t, db, 3, "removed publisher", `["user.registered"]`)
	insertLegacyWebhookConfig(
		t,
		db,
		4,
		"system alert",
		`["system.alert","automation.notification"]`,
	)
	insertLegacyWebhookConfig(t, db, 5, "disabled publisher", `["user.registered"]`)
	if err := db.Table("webhook_configs").
		Where("id = ?", 5).
		Update("status", models.WebhookStatusDisabled).Error; err != nil {
		t.Fatal(err)
	}

	embeddedAttachment, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"cloud_event": map[string]any{
				"type": eventcontract.TicketAttachmentCreatedEventType,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	logs := []map[string]any{
		{
			"id": 1, "config_id": 1, "event_type": "ticket.updated",
			"event_data": string(embeddedAttachment), "status": "success",
		},
		{
			"id": 2, "config_id": 1, "event_type": "ticket.resolved",
			"event_data": `{}`, "status": "success",
		},
		{
			"id": 3, "config_id": 1,
			"event_type": eventcontract.TicketCreatedEventType,
			"event_data": `{}`, "status": "success",
		},
	}
	for _, row := range logs {
		row["created_at"] = time.Now().UTC()
		row["organization_id"] = 1
		row["project_id"] = 1
		if err := db.Table("webhook_logs").Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := MigrateWebhookEventTaxonomy(db); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt+1, err)
		}
	}

	var resolved models.WebhookConfig
	if err := db.First(&resolved, 1).Error; err != nil {
		t.Fatal(err)
	}
	if !resolved.MatchesEvent(
		models.WebhookEventTicketTransitioned,
		models.TicketStatusResolved,
	) || resolved.MatchesEvent(
		models.WebhookEventTicketTransitioned,
		models.TicketStatusClosed,
	) {
		t.Fatalf("resolved subscription predicate was not preserved: %+v", resolved.FilterRulesObj)
	}

	var updated models.WebhookConfig
	if err := db.First(&updated, 2).Error; err != nil {
		t.Fatal(err)
	}
	if !updated.IsEventEnabled(models.WebhookEventTicketUpdated) ||
		!updated.IsEventEnabled(models.WebhookEventTicketAttachment) ||
		!updated.MatchesEvent(
			models.WebhookEventTicketTransitioned,
			models.TicketStatusInProgress,
		) ||
		updated.MatchesEvent(
			models.WebhookEventTicketTransitioned,
			models.TicketStatusResolved,
		) {
		t.Fatalf("updated subscription expansion was not preserved: %+v", updated)
	}

	var removed models.WebhookConfig
	if err := db.First(&removed, 3).Error; err != nil {
		t.Fatal(err)
	}
	if len(removed.EnabledEventsObj) != 0 ||
		removed.Status != models.WebhookStatusInactive {
		t.Fatalf("publisher-less subscription remains active: %+v", removed)
	}
	var disabled models.WebhookConfig
	if err := db.First(&disabled, 5).Error; err != nil {
		t.Fatal(err)
	}
	if len(disabled.EnabledEventsObj) != 0 ||
		disabled.Status != models.WebhookStatusDisabled {
		t.Fatalf("migration weakened disabled configuration state: %+v", disabled)
	}

	var system models.WebhookConfig
	if err := db.First(&system, 4).Error; err != nil {
		t.Fatal(err)
	}
	for _, expected := range []models.WebhookEventType{
		models.WebhookEventTicketSLABreached,
		models.WebhookEventSystemAlert,
		models.WebhookEventAutomationNotification,
	} {
		if !system.IsEventEnabled(expected) {
			t.Errorf("system subscription is missing %q", expected)
		}
	}

	var migratedLogs []models.WebhookLog
	if err := db.Order("id ASC").Find(&migratedLogs).Error; err != nil {
		t.Fatal(err)
	}
	wantLogTypes := []models.WebhookEventType{
		models.WebhookEventTicketAttachment,
		models.WebhookEventTicketTransitioned,
		models.WebhookEventTicketCreated,
	}
	for index, want := range wantLogTypes {
		if migratedLogs[index].EventType != want {
			t.Errorf(
				"Webhook log %d event type = %q, want %q",
				migratedLogs[index].ID,
				migratedLogs[index].EventType,
				want,
			)
		}
	}
}

func TestMigrateWebhookEventTaxonomyRejectsUnknownConfigValue(t *testing.T) {
	db := openWebhookMigrationTestDB(t)
	insertLegacyWebhookConfig(t, db, 1, "unknown", `["custom.ticket.event"]`)

	err := MigrateWebhookEventTaxonomy(db)
	if err == nil || !strings.Contains(err.Error(), "custom.ticket.event") {
		t.Fatalf("migration error = %v, want unknown event", err)
	}
	var persisted string
	if err := db.Table("webhook_configs").
		Where("id = ?", 1).
		Pluck("enabled_events", &persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted != `["custom.ticket.event"]` {
		t.Fatalf("failed migration did not roll back: %q", persisted)
	}
}

func TestMigrateWebhookEventTaxonomyRejectsPublisherlessHistoricalLog(t *testing.T) {
	db := openWebhookMigrationTestDB(t)
	insertLegacyWebhookConfig(
		t,
		db,
		1,
		"current",
		`["io.chronodesk.ticket.created.v1"]`,
	)
	if err := db.Table("webhook_logs").Create(map[string]any{
		"id":              1,
		"organization_id": 1,
		"project_id":      1,
		"created_at":      time.Now().UTC(),
		"config_id":       1,
		"event_type":      "user.registered",
		"event_data":      `{}`,
		"status":          "success",
	}).Error; err != nil {
		t.Fatal(err)
	}

	err := MigrateWebhookEventTaxonomy(db)
	if err == nil || !strings.Contains(err.Error(), "has no canonical publisher") {
		t.Fatalf("migration error = %v, want publisherless log rejection", err)
	}
}

func TestRunMigrationsInvokesWebhookEventTaxonomyMigration(t *testing.T) {
	db := openWebhookMigrationTestDB(t)
	insertLegacyWebhookConfig(t, db, 1, "closed", `["ticket.closed"]`)

	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("RunMigrations(): %v", err)
	}
	var migrated models.WebhookConfig
	if err := db.First(&migrated, 1).Error; err != nil {
		t.Fatal(err)
	}
	if !migrated.MatchesEvent(
		models.WebhookEventTicketTransitioned,
		models.TicketStatusClosed,
	) || migrated.MatchesEvent(
		models.WebhookEventTicketTransitioned,
		models.TicketStatusResolved,
	) {
		t.Fatalf(
			"standard migration entry point did not preserve closed semantics: %+v",
			migrated.FilterRulesObj,
		)
	}
}
