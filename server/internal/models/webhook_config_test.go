package models

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestValidateWebhookSubscriptionsRequiresCanonicalTypes(t *testing.T) {
	tests := []struct {
		name    string
		events  []WebhookEventType
		filters *WebhookFilterRules
		wantErr string
	}{
		{
			name:    "canonical type",
			events:  []WebhookEventType{WebhookEventTicketCreated},
			filters: nil,
		},
		{
			name:    "legacy alias",
			events:  []WebhookEventType{"ticket.created"},
			wantErr: "unsupported Webhook event type",
		},
		{
			name: "duplicate canonical type",
			events: []WebhookEventType{
				WebhookEventTicketCreated,
				WebhookEventTicketCreated,
			},
			wantErr: "duplicate Webhook event type",
		},
		{
			name:   "predicate without transitioned subscription",
			events: []WebhookEventType{WebhookEventTicketUpdated},
			filters: &WebhookFilterRules{
				TransitionStatuses: []TicketStatus{TicketStatusResolved},
			},
			wantErr: "require the ticket.transitioned CloudEvent",
		},
		{
			name:   "unknown status",
			events: []WebhookEventType{WebhookEventTicketTransitioned},
			filters: &WebhookFilterRules{
				TransitionStatuses: []TicketStatus{"awaiting_robot"},
			},
			wantErr: "unsupported transition status",
		},
		{
			name:   "duplicate status",
			events: []WebhookEventType{WebhookEventTicketTransitioned},
			filters: &WebhookFilterRules{
				TransitionStatuses: []TicketStatus{
					TicketStatusClosed,
					TicketStatusClosed,
				},
			},
			wantErr: "duplicate transition status",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateWebhookSubscriptions(test.events, test.filters, true)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateWebhookSubscriptions() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf(
					"ValidateWebhookSubscriptions() error = %v, want %q",
					err,
					test.wantErr,
				)
			}
		})
	}
}

func TestWebhookConfigMatchesTransitionPredicate(t *testing.T) {
	config := WebhookConfig{
		EnabledEventsObj: []WebhookEventType{
			WebhookEventTicketTransitioned,
			WebhookEventTicketComment,
		},
		FilterRulesObj: &WebhookFilterRules{
			TransitionStatuses: []TicketStatus{
				TicketStatusResolved,
			},
		},
	}

	if !config.MatchesEvent(
		WebhookEventTicketTransitioned,
		TicketStatusResolved,
	) {
		t.Fatal("resolved transition did not match explicit predicate")
	}
	if config.MatchesEvent(
		WebhookEventTicketTransitioned,
		TicketStatusClosed,
	) {
		t.Fatal("closed transition bypassed explicit resolved predicate")
	}
	if !config.MatchesEvent(WebhookEventTicketComment, "") {
		t.Fatal("predicate for transitioned events affected another event type")
	}
	if config.MatchesEvent(WebhookEventTicketUpdated, "") {
		t.Fatal("event not present in the subscription matched")
	}
}

func TestDecodeWebhookFilterRulesRejectsOpenEndedJSON(t *testing.T) {
	tests := []string{
		`{"transition_statuses":["resolved"],"typo":true}`,
		`{"transition_statuses":["resolved"]} {"transition_statuses":[]}`,
	}
	for _, input := range tests {
		if _, err := DecodeWebhookFilterRules(input); err == nil {
			t.Fatalf("DecodeWebhookFilterRules(%q) accepted invalid JSON contract", input)
		}
	}
}

func TestWebhookDeletionRetainsDeliveryAuditLog(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf(
			"file:%s?mode=memory&cache=shared&_foreign_keys=1",
			t.Name(),
		)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&User{}, &WebhookConfig{}, &WebhookLog{}); err != nil {
		t.Fatal(err)
	}
	user := User{
		Username: "webhook-delete-admin", Email: "webhook-delete@example.com",
		PasswordHash: "hash", PlatformRole: PlatformRolePlatformAdmin, Status: UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	config := WebhookConfig{
		Name:       "audit-retained",
		Provider:   WebhookProviderCustom,
		WebhookURL: "https://webhook.example.test/chronodesk",
		Status:     WebhookStatusInactive,
		CreatedBy:  user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	log := WebhookLog{
		CreatedAt:    time.Now(),
		ConfigID:     config.ID,
		EventType:    WebhookEventTicketCreated,
		Status:       "failed",
		RequestURL:   config.WebhookURL,
		ErrorMessage: "expected test failure",
	}
	if err := db.Create(&log).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Delete(&config).Error; err != nil {
		t.Fatalf("soft-delete Webhook with audit log: %v", err)
	}
	var visible int64
	if err := db.Model(&WebhookConfig{}).
		Where("id = ?", config.ID).
		Count(&visible).Error; err != nil {
		t.Fatal(err)
	}
	if visible != 0 {
		t.Fatalf("deleted Webhook remains visible: count=%d", visible)
	}
	var retainedConfig WebhookConfig
	if err := db.Unscoped().First(&retainedConfig, config.ID).Error; err != nil {
		t.Fatalf("Webhook audit anchor is missing: %v", err)
	}
	if !retainedConfig.DeletedAt.Valid {
		t.Fatal("Webhook audit anchor is not marked deleted")
	}
	var logCount int64
	if err := db.Model(&WebhookLog{}).
		Where("id = ? AND config_id = ?", log.ID, config.ID).
		Count(&logCount).Error; err != nil {
		t.Fatal(err)
	}
	if logCount != 1 {
		t.Fatalf("Webhook deletion retained %d logs, want 1", logCount)
	}
}

func TestWebhookDeliverySnapshotIsAppendOnly(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf(
			"file:%s?mode=memory&cache=shared",
			t.Name(),
		)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&User{},
		&WebhookConfig{},
		&WebhookDeliverySnapshot{},
	); err != nil {
		t.Fatal(err)
	}
	user := User{
		Username:     "webhook-snapshot-owner",
		Email:        "webhook-snapshot-owner@example.test",
		PasswordHash: "hash",
		PlatformRole: PlatformRolePlatformAdmin,
		Status:       UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	config := WebhookConfig{
		OrganizationID: 11,
		ProjectID:      22,
		Name:           "append-only",
		Provider:       WebhookProviderCustom,
		WebhookURL:     "https://old.example.test/events",
		Status:         WebhookStatusActive,
		EnabledEventsObj: []WebhookEventType{
			WebhookEventTicketCreated,
		},
		RetryCount: 2,
		CreatedBy:  user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewWebhookDeliverySnapshot(config, "event-append-only")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(snapshot).Error; err != nil {
		t.Fatal(err)
	}
	parsed, err := uuid.Parse(snapshot.ID)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("snapshot id %q is not UUIDv7: %v", snapshot.ID, err)
	}

	if err := db.Model(snapshot).
		Update("webhook_url", "https://changed.example.test/events").Error; err == nil ||
		!strings.Contains(err.Error(), "immutable") {
		t.Fatalf("snapshot update error = %v, want immutable rejection", err)
	}
	if err := db.Delete(snapshot).Error; err == nil ||
		!strings.Contains(err.Error(), "immutable") {
		t.Fatalf("snapshot delete error = %v, want immutable rejection", err)
	}

	var retained WebhookDeliverySnapshot
	if err := db.First(&retained, "id = ?", snapshot.ID).Error; err != nil {
		t.Fatal(err)
	}
	if retained.WebhookURL != config.WebhookURL ||
		retained.EventID != "event-append-only" {
		t.Fatalf("immutable snapshot changed: %+v", retained)
	}
}
