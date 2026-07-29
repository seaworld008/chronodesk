package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestWebhookNon2xxAlwaysPropagatesToOutboxCaller(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporary failure", http.StatusBadGateway)
	}))
	defer endpoint.Close()

	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.WebhookConfig{}, &models.WebhookLog{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "webhook-owner", Email: "webhook-owner@example.com",
		PasswordHash: "hash", Role: models.RoleAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	config := models.WebhookConfig{
		Name: "failing", Provider: models.WebhookProviderCustom,
		WebhookURL: endpoint.URL, Status: models.WebhookStatusActive,
		RetryCount: 1, RetryInterval: 1, CreatedBy: user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	service := NewNotificationService(db)
	useTestWebhookClient(service, endpoint.Client())
	err := service.sendWebhookAttempt(context.Background(), &config, &NotificationEvent{
		Type: models.WebhookEventTicketCreated, ResourceID: 1,
		ResourceType: "ticket", Title: "created", Timestamp: time.Now().UTC(),
		Data: map[string]any{"ticket_number": "WEBHOOK-1"},
	})
	if err == nil {
		t.Fatal("non-2xx webhook response must remain an error even when a retry is configured")
	}
	var log models.WebhookLog
	if err := db.Order("id DESC").First(&log).Error; err != nil {
		t.Fatal(err)
	}
	if log.Status != "failed" ||
		log.NextRetryAt != nil ||
		log.MaxRetries != 0 ||
		log.ResponseStatus != http.StatusBadGateway {
		t.Fatalf("unexpected webhook log: %+v", log)
	}
}

func TestWebhookOutboxAttemptHasHardTimeoutAndNoLegacyRetry(t *testing.T) {
	var attempts atomic.Int32
	releaseHandler := make(chan struct{})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		<-releaseHandler
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer endpoint.Close()

	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.WebhookConfig{}, &models.WebhookLog{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "webhook-timeout", Email: "webhook-timeout@example.com",
		PasswordHash: "hash", Role: models.RoleAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	config := models.WebhookConfig{
		Name:             "timeout",
		Provider:         models.WebhookProviderCustom,
		WebhookURL:       endpoint.URL,
		Status:           models.WebhookStatusActive,
		EnabledEventsObj: []models.WebhookEventType{models.WebhookEventTicketCreated},
		RetryCount:       10,
		RetryInterval:    3600,
		TimeoutSeconds:   300,
		CreatedBy:        user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	service := NewNotificationService(db)
	useTestWebhookClient(service, endpoint.Client())
	service.outboxWebhookTimeout = 50 * time.Millisecond
	started := time.Now()
	err := service.SendWebhookOutboxAttempt(context.Background(), config.ID, &NotificationEvent{
		Type: models.WebhookEventTicketCreated, ResourceID: 1,
		ResourceType: "ticket", Title: "created", Timestamp: time.Now().UTC(),
	})
	elapsed := time.Since(started)
	close(releaseHandler)
	if err == nil {
		t.Fatal("timed out Outbox webhook attempt must fail")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Outbox webhook timeout was not enforced: %s", elapsed)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("Outbox webhook made %d attempts, want 1", got)
	}
	var log models.WebhookLog
	if err := db.Order("id DESC").First(&log).Error; err != nil {
		t.Fatal(err)
	}
	if log.Status != "failed" || log.NextRetryAt != nil {
		t.Fatalf("Outbox attempt scheduled legacy retry state: %+v", log)
	}
}

func useTestWebhookClient(service *NotificationService, client *http.Client) {
	service.webhookClients = WebhookClientFactoryFunc(func(
		context.Context,
		*url.URL,
		time.Duration,
	) (*http.Client, error) {
		return client, nil
	})
}
