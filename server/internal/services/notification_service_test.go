package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"gorm.io/gorm"
)

const testCustomWebhookSecret = "chronodesk-custom-webhook-test-secret"

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

func TestCustomWebhookDualKeyRotationSignsSameRawBody(t *testing.T) {
	service := NewNotificationServiceWithProtector(openTestDB(t), nil)
	body := []byte(`{"specversion":"1.0","id":"dual-key"}`)
	request := httptest.NewRequest(
		http.MethodPost,
		"https://example.test/hook",
		bytes.NewReader(body),
	)
	expiresAt := time.Now().UTC().Add(time.Hour)
	config := &models.WebhookConfig{
		Provider:                models.WebhookProviderCustom,
		Secret:                  "current-secret-with-at-least-32-bytes",
		PreviousSecret:          "previous-secret-with-at-least-32-bytes",
		PreviousSecretExpiresAt: &expiresAt,
	}
	service.setRequestHeaders(request, config, body)
	timestamp := request.Header.Get("X-ChronoDesk-Timestamp")
	current := request.Header.Get("X-ChronoDesk-Signature")
	previous := request.Header.Get("X-ChronoDesk-Signature-Previous")
	if timestamp == "" ||
		current != service.generateCustomWebhookSign(
			timestamp,
			body,
			config.Secret,
		) ||
		previous != service.generateCustomWebhookSign(
			timestamp,
			body,
			config.PreviousSecret,
		) {
		t.Fatalf(
			"unexpected dual signature headers: timestamp=%q current=%q previous=%q",
			timestamp,
			current,
			previous,
		)
	}
	expiredAt := time.Now().UTC().Add(-time.Second)
	config.PreviousSecretExpiresAt = &expiredAt
	expiredRequest := httptest.NewRequest(
		http.MethodPost,
		"https://example.test/hook",
		bytes.NewReader(body),
	)
	service.setRequestHeaders(expiredRequest, config, body)
	if got := expiredRequest.Header.Get(
		"X-ChronoDesk-Signature-Previous",
	); got != "" {
		t.Fatalf("expired previous signature was sent: %q", got)
	}
}

func TestWebhookNon2xxAlwaysPropagatesToOutboxCaller(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporary failure", http.StatusBadGateway)
	}))
	defer endpoint.Close()

	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.WebhookLog{},
	); err != nil {
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
		OrganizationID: 1,
		ProjectID:      1,
		Name:           "failing", Provider: models.WebhookProviderCustom,
		WebhookURL: endpoint.URL, Status: models.WebhookStatusActive,
		RetryCount: 1, RetryInterval: 1, CreatedBy: user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	config.Secret = testCustomWebhookSecret
	service := NewNotificationServiceWithProtector(db, nil)
	useTestWebhookClient(service, endpoint.Client())
	event, _ := newTestCustomWebhookEvent(
		t,
		"event-non-2xx",
		models.WebhookEventTicketCreated,
	)
	err := service.sendWebhookAttempt(context.Background(), &config, event)
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
	if err := db.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.WebhookLog{},
	); err != nil {
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
		OrganizationID:   1,
		ProjectID:        10,
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
	protector := storeTestCustomWebhookSecret(
		t,
		db,
		&config,
		testCustomWebhookSecret,
	)
	service := NewNotificationServiceWithProtector(db, protector)
	useTestWebhookClient(service, endpoint.Client())
	service.outboxWebhookTimeout = 50 * time.Millisecond
	started := time.Now()
	event, _ := newTestCustomWebhookEvent(
		t,
		"event-hard-timeout",
		models.WebhookEventTicketCreated,
	)
	snapshot, err := models.NewWebhookDeliverySnapshot(
		config,
		event.Metadata["event_id"],
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(snapshot).Error; err != nil {
		t.Fatal(err)
	}
	err = service.SendWebhookSnapshotOutboxAttempt(
		context.Background(),
		models.ProjectScope{OrganizationID: 1, ProjectID: 10},
		snapshot.ID,
		event,
	)
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

func TestCustomWebhookSendsExactSignedStructuredCloudEvent(t *testing.T) {
	type receivedRequest struct {
		body          []byte
		contentType   string
		timestamp     string
		signature     string
		cloudEventID  string
		idempotencyID string
		deliveryID    string
	}
	received := make(chan receivedRequest, 1)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read receiver body: %v", err)
		}
		received <- receivedRequest{
			body:          body,
			contentType:   request.Header.Get("Content-Type"),
			timestamp:     request.Header.Get("X-ChronoDesk-Timestamp"),
			signature:     request.Header.Get("X-ChronoDesk-Signature"),
			cloudEventID:  request.Header.Get("X-CloudEvents-ID"),
			idempotencyID: request.Header.Get("Idempotency-Key"),
			deliveryID:    request.Header.Get("X-ChronoDesk-Delivery-ID"),
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer endpoint.Close()

	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.WebhookLog{},
	); err != nil {
		t.Fatal(err)
	}
	owner := models.User{
		Username:     "signed-webhook-owner",
		Email:        "signed-webhook-owner@example.test",
		PasswordHash: "hash",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	config := models.WebhookConfig{
		OrganizationID: 1,
		ProjectID:      1,
		Name:           "signed-cloud-event",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     endpoint.URL,
		Status:         models.WebhookStatusActive,
		CreatedBy:      owner.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	config.Secret = testCustomWebhookSecret
	service := NewNotificationServiceWithProtector(db, nil)
	useTestWebhookClient(service, endpoint.Client())
	event, cloudEvent := newTestCustomWebhookEvent(
		t,
		"event-signed-structured",
		models.WebhookEventTicketCreated,
	)
	before := time.Now().UTC()
	if err := service.sendWebhookAttempt(context.Background(), &config, event); err != nil {
		t.Fatalf("send signed custom Webhook: %v", err)
	}
	after := time.Now().UTC()
	request := <-received

	wantBody, err := json.Marshal(cloudEvent)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(request.body, wantBody) {
		t.Fatalf(
			"receiver body does not equal the exact structured CloudEvent\n got: %s\nwant: %s",
			request.body,
			wantBody,
		)
	}
	if request.contentType != "application/cloudevents+json" {
		t.Fatalf("Content-Type = %q", request.contentType)
	}
	if request.cloudEventID != cloudEvent.ID {
		t.Fatalf("X-CloudEvents-ID = %q, want %q", request.cloudEventID, cloudEvent.ID)
	}
	if request.idempotencyID != event.Metadata["delivery_id"] ||
		request.deliveryID != event.Metadata["delivery_id"] {
		t.Fatalf(
			"delivery IDs = (%q, %q), want %q",
			request.idempotencyID,
			request.deliveryID,
			event.Metadata["delivery_id"],
		)
	}
	unixSeconds, err := strconv.ParseInt(request.timestamp, 10, 64)
	if err != nil {
		t.Fatalf("X-ChronoDesk-Timestamp = %q: %v", request.timestamp, err)
	}
	if unixSeconds < before.Add(-time.Second).Unix() ||
		unixSeconds > after.Add(time.Second).Unix() {
		t.Fatalf(
			"signed timestamp %d is outside receiver replay window [%d,%d]",
			unixSeconds,
			before.Add(-time.Second).Unix(),
			after.Add(time.Second).Unix(),
		)
	}
	mac := hmac.New(sha256.New, []byte(testCustomWebhookSecret))
	_, _ = mac.Write([]byte(request.timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(request.body)
	wantSignature := "v1=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(request.signature), []byte(wantSignature)) {
		t.Fatalf("X-ChronoDesk-Signature = %q, want %q", request.signature, wantSignature)
	}

	var log models.WebhookLog
	if err := db.Order("id DESC").First(&log).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.RequestHeaders, "X-Chronodesk-Timestamp") ||
		strings.Contains(log.RequestHeaders, "Signature") {
		t.Fatalf("unexpected signed Webhook audit headers: %s", log.RequestHeaders)
	}
}

func TestCustomWebhookWithoutSecretFailsClosed(t *testing.T) {
	var attempts atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()

	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.WebhookLog{},
	); err != nil {
		t.Fatal(err)
	}
	owner := models.User{
		Username:     "unsigned-webhook-owner",
		Email:        "unsigned-webhook-owner@example.test",
		PasswordHash: "hash",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	config := models.WebhookConfig{
		OrganizationID:   1,
		ProjectID:        20,
		Name:             "unsigned-custom-webhook",
		Provider:         models.WebhookProviderCustom,
		WebhookURL:       endpoint.URL,
		Status:           models.WebhookStatusActive,
		EnabledEventsObj: []models.WebhookEventType{models.WebhookEventTicketCreated},
		CreatedBy:        owner.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	service := NewNotificationServiceWithProtector(db, nil)
	useTestWebhookClient(service, endpoint.Client())
	event, _ := newTestCustomWebhookEvent(
		t,
		"event-unsigned-rejected",
		models.WebhookEventTicketCreated,
	)
	snapshot, err := models.NewWebhookDeliverySnapshot(
		config,
		event.Metadata["event_id"],
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(snapshot).Error; err != nil {
		t.Fatal(err)
	}
	err = service.SendWebhookSnapshotOutboxAttempt(
		context.Background(),
		models.ProjectScope{OrganizationID: 1, ProjectID: 20},
		snapshot.ID,
		event,
	)
	if err == nil || !strings.Contains(err.Error(), "缺少签名密钥") {
		t.Fatalf("unsigned custom Webhook error = %v", err)
	}
	if attempts.Load() != 0 {
		t.Fatalf("unsigned custom Webhook made %d HTTP attempts", attempts.Load())
	}
	var log models.WebhookLog
	if err := db.Order("id DESC").First(&log).Error; err != nil {
		t.Fatal(err)
	}
	if log.Status != "failed" ||
		!strings.Contains(log.ErrorMessage, "缺少签名密钥") ||
		log.RequestURL != "" ||
		log.RequestBody != "" {
		t.Fatalf("unsigned custom Webhook log = %+v", log)
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
			OrganizationID: 1,
			ProjectID:      30,
			Name:           "resolved only",
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     "https://resolved.example.test/events",
			Status:         models.WebhookStatusActive,
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
			OrganizationID: 1,
			ProjectID:      30,
			Name:           "closed only",
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     "https://closed.example.test/events",
			Status:         models.WebhookStatusActive,
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
			OrganizationID: 1,
			ProjectID:      30,
			Name:           "all transitions",
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     "https://all.example.test/events",
			Status:         models.WebhookStatusActive,
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
				models.ProjectScope{OrganizationID: 1, ProjectID: 30},
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

func newTestCustomWebhookEvent(
	t testing.TB,
	eventID string,
	eventType models.WebhookEventType,
) (*NotificationEvent, CloudEventEnvelope) {
	t.Helper()
	actor := models.SystemActor("webhook-test")
	eventTime := time.Date(2026, time.July, 30, 9, 8, 7, 654321000, time.UTC)
	data, err := json.Marshal(map[string]any{
		"actor":          actor,
		"changed_fields": []string{"status"},
		"status":         models.TicketStatusInProgress,
		"ticket_id":      42,
	})
	if err != nil {
		t.Fatal(err)
	}
	cloudEvent := CloudEventEnvelope{
		SpecVersion:     "1.0",
		ID:              eventID,
		Source:          "urn:chronodesk:ticket-system",
		Type:            string(eventType),
		Subject:         "ticket/42",
		Time:            eventTime,
		DataContentType: "application/json",
		DataSchema:      "urn:chronodesk:schema:domain-event-data:v1",
		TraceID:         "trace-webhook-test",
		CorrelationID:   "correlation-webhook-test",
		CausationID:     "cause-webhook-test",
		ActorType:       actor.Type,
		ActorID:         actor.ID,
		ResourceVersion: 8,
		Data:            data,
	}
	event := &NotificationEvent{
		Type:         eventType,
		ResourceID:   42,
		ResourceType: "ticket",
		Title:        "created",
		Description:  string(eventType),
		Data: map[string]any{
			"cloud_event":   cloudEvent,
			"ticket_id":     42,
			"ticket_number": "WEBHOOK-42",
		},
		Metadata: map[string]string{
			"delivery_id": "delivery:" + eventID,
			"event_id":    eventID,
		},
		Timestamp: eventTime,
	}
	return event, cloudEvent
}

func storeTestCustomWebhookSecret(
	t testing.TB,
	db *gorm.DB,
	config *models.WebhookConfig,
	secret string,
) security.Protector {
	t.Helper()
	key := bytes.Repeat([]byte{0x6a}, 32)
	protector, err := security.NewKeyring(
		"custom-webhook-test",
		map[string][]byte{"custom-webhook-test": key},
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := security.ProtectOptional(
		protector,
		secret,
		security.FieldAAD(
			"webhook_configs",
			strconv.FormatUint(uint64(config.ID), 10),
			"secret",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.WebhookConfig{}).
		Where("id = ?", config.ID).
		UpdateColumn("secret", envelope).Error; err != nil {
		t.Fatal(err)
	}
	config.Secret = envelope
	return protector
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
