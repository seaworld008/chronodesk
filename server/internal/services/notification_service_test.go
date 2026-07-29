package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestWebhookProtocolHeadersAndDingTalkTitleUseChronoDeskBrand(t *testing.T) {
	service := NewNotificationServiceWithProtector(openTestDB(t), nil)
	request := httptest.NewRequest(http.MethodPost, "https://example.test/hook", nil)
	config := &models.WebhookConfig{
		Provider: models.WebhookProviderLark,
		Secret:   "test-secret",
	}
	service.setRequestHeaders(request, config, nil)
	if got := request.Header.Get("User-Agent"); got != "ChronoDesk-Webhook/1.0" {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := request.Header.Get("X-Lark-Request-Nonce"); got != "chronodesk" {
		t.Fatalf("Lark nonce = %q", got)
	}

	body, err := service.buildDingTalkBody("通知正文")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Markdown struct {
			Title string `json:"title"`
		} `json:"markdown"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Markdown.Title != "ChronoDesk 通知" {
		t.Fatalf("DingTalk title = %q", payload.Markdown.Title)
	}
}

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
	service := NewNotificationServiceWithProtector(db, nil)
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
	service := NewNotificationServiceWithProtector(db, nil)
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

func TestListWebhookOutboxTargetsAppliesTransitionPredicate(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.WebhookConfig{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "webhook-filter-owner",
		Email:        "webhook-filter-owner@example.com",
		PasswordHash: "hash",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	configs := []models.WebhookConfig{
		{
			Name:       "resolved only",
			Provider:   models.WebhookProviderCustom,
			WebhookURL: "https://resolved.example.test/events",
			Status:     models.WebhookStatusActive,
			EnabledEventsObj: []models.WebhookEventType{
				models.WebhookEventTicketTransitioned,
			},
			FilterRulesObj: &models.WebhookFilterRules{
				TransitionStatuses: []models.TicketStatus{
					models.TicketStatusResolved,
				},
			},
			CreatedBy: user.ID,
		},
		{
			Name:       "closed only",
			Provider:   models.WebhookProviderCustom,
			WebhookURL: "https://closed.example.test/events",
			Status:     models.WebhookStatusActive,
			EnabledEventsObj: []models.WebhookEventType{
				models.WebhookEventTicketTransitioned,
			},
			FilterRulesObj: &models.WebhookFilterRules{
				TransitionStatuses: []models.TicketStatus{
					models.TicketStatusClosed,
				},
			},
			CreatedBy: user.ID,
		},
		{
			Name:       "all transitions",
			Provider:   models.WebhookProviderCustom,
			WebhookURL: "https://all.example.test/events",
			Status:     models.WebhookStatusActive,
			EnabledEventsObj: []models.WebhookEventType{
				models.WebhookEventTicketTransitioned,
			},
			CreatedBy: user.ID,
		},
	}
	for index := range configs {
		if err := db.Create(&configs[index]).Error; err != nil {
			t.Fatal(err)
		}
	}

	service := NewNotificationServiceWithProtector(db, nil)
	tests := []struct {
		name   string
		status models.TicketStatus
		want   []uint
	}{
		{
			name:   "resolved",
			status: models.TicketStatusResolved,
			want:   []uint{configs[0].ID, configs[2].ID},
		},
		{
			name:   "closed",
			status: models.TicketStatusClosed,
			want:   []uint{configs[1].ID, configs[2].ID},
		},
		{
			name: "missing status fails closed for filtered subscriptions",
			want: []uint{configs[2].ID},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			targets, err := service.ListWebhookOutboxTargets(
				context.Background(),
				models.WebhookEventTicketTransitioned,
				test.status,
			)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]uint, 0, len(targets))
			for _, target := range targets {
				got = append(got, target.ConfigID)
			}
			if len(got) != len(test.want) {
				t.Fatalf("target IDs = %v, want %v", got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("target IDs = %v, want %v", got, test.want)
				}
			}
		})
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
