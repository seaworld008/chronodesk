package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestSendWebhookLogEnvironment(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")

	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.WebhookConfig{}, &models.WebhookLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	creator := models.User{
		Username:     "webhook-env",
		Email:        "webhook-env@example.com",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&creator).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	config := models.WebhookConfig{
		OrganizationID: 1,
		ProjectID:      1,
		Name:           "env-test",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     server.URL,
		Status:         models.WebhookStatusActive,
		RetryCount:     0,
		RetryInterval:  1,
		CreatedBy:      creator.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatalf("create webhook config: %v", err)
	}
	config.Secret = testCustomWebhookSecret

	service := NewNotificationServiceWithProtector(db, nil)
	useTestWebhookClient(service, server.Client())
	event, _ := newTestCustomWebhookEvent(
		t,
		"event-environment-log",
		models.WebhookEventSystemAlert,
	)

	if err := service.sendWebhookAttempt(context.Background(), &config, event); err != nil {
		t.Fatalf("sendWebhookAttempt returned error: %v", err)
	}

	var log models.WebhookLog
	if err := db.Order("id desc").First(&log).Error; err != nil {
		t.Fatalf("fetch webhook log: %v", err)
	}

	if log.Environment != "production" {
		t.Fatalf("expected environment production, got %q", log.Environment)
	}
}
