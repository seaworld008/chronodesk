package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gongdan-system/internal/models"
)

func TestWebhookAuditLogNeverPersistsCredentialsOrSignatures(t *testing.T) {
	const (
		signingSecret = "webhook-signing-secret-value"
		accessToken   = "webhook-access-token-value"
	)
	var receivedAuthorization string
	var receivedSignature string
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthorization = r.Header.Get("Authorization")
		receivedSignature = r.Header.Get("X-Lark-Signature")
		w.Header().Set("X-Echo-Authorization", receivedAuthorization)
		w.Header().Set("X-Echo-Signature", receivedSignature)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(
			`{"authorization":"` + receivedAuthorization +
				`","signature":"` + receivedSignature + `"}`,
		))
	}))
	defer endpoint.Close()

	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.WebhookConfig{}, &models.WebhookLog{}); err != nil {
		t.Fatal(err)
	}
	owner := models.User{
		Username: "webhook-log-owner", Email: "webhook-log-owner@example.test",
		PasswordHash: "hash", Role: models.RoleAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	config := models.WebhookConfig{
		Name: "sensitive-log-test", Provider: models.WebhookProviderLark,
		WebhookURL: endpoint.URL + "/callback/" + accessToken + "?secret=" + signingSecret,
		Secret:     signingSecret, AccessToken: accessToken,
		Status: models.WebhookStatusActive, CreatedBy: owner.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	service := NewNotificationService(db)
	useTestWebhookClient(service, endpoint.Client())
	err := service.sendWebhookAttempt(context.Background(), &config, &NotificationEvent{
		Type: models.WebhookEventSystemAlert, ResourceID: 7,
		ResourceType: "system", Title: "安全日志测试",
		Description: signingSecret + " " + accessToken,
		Data:        map[string]any{"ticket_number": "SEC-7"},
		Metadata:    map[string]string{},
		Timestamp:   time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("expected upstream failure")
	}
	if receivedAuthorization != "Bearer "+accessToken || receivedSignature == "" {
		t.Fatalf(
			"delivery did not receive credentials: authorization=%q signature=%q",
			receivedAuthorization,
			receivedSignature,
		)
	}
	for _, sensitive := range []string{signingSecret, accessToken, receivedSignature} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("returned error leaked %q: %v", sensitive, err)
		}
	}

	var audit models.WebhookLog
	if err := db.Order("id DESC").First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	persisted := strings.Join([]string{
		audit.EventData,
		audit.RequestURL,
		audit.RequestHeaders,
		audit.RequestBody,
		audit.ResponseHeaders,
		audit.ResponseBody,
		audit.ErrorMessage,
	}, "\n")
	for _, sensitive := range []string{
		signingSecret,
		accessToken,
		receivedSignature,
		"Authorization",
		"Bearer ",
		"X-Lark-Signature",
	} {
		if strings.Contains(persisted, sensitive) {
			t.Fatalf("webhook audit log leaked %q: %s", sensitive, persisted)
		}
	}
	if audit.ResponseBody != "" {
		t.Fatalf("untrusted response body was persisted: %q", audit.ResponseBody)
	}
	if audit.RequestURL != endpoint.URL {
		t.Fatalf("logged endpoint=%q, want origin=%q", audit.RequestURL, endpoint.URL)
	}
}
