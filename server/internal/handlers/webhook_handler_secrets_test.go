package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestWebhookHandlerEncryptsCredentialsAtRest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:webhook-secret-handler?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.WebhookConfig{}, &models.WebhookLog{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		ID: 1, Username: "webhook-admin", Email: "webhook-admin@example.test",
		PasswordHash: "hash", Role: models.RoleAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	ring, err := security.NewKeyring("webhook-test", map[string][]byte{
		"webhook-test": bytes.Repeat([]byte{0x28}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewWebhookHandlerWithProtector(db, ring)
	router := gin.New()
	router.POST("/webhooks", func(c *gin.Context) {
		c.Set("user_id", uint(1))
		handler.CreateWebhook(c)
	})
	payload := map[string]any{
		"name":              "secure webhook",
		"provider":          "custom",
		"webhook_url":       "https://hooks.example.test/events",
		"secret":            "signature-secret",
		"access_token":      "external-access-token",
		"enabled_events":    []string{"ticket.created"},
		"message_format":    "markdown",
		"retry_count":       1,
		"retry_interval":    60,
		"timeout_seconds":   30,
		"is_async":          true,
		"rate_limit":        60,
		"rate_limit_window": 60,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/webhooks", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "signature-secret") ||
		strings.Contains(recorder.Body.String(), "external-access-token") {
		t.Fatal("webhook response leaked credentials")
	}
	var stored models.WebhookConfig
	if err := db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if !security.IsEnvelope(stored.Secret) || !security.IsEnvelope(stored.AccessToken) ||
		strings.Contains(stored.Secret, "signature-secret") ||
		strings.Contains(stored.AccessToken, "external-access-token") {
		t.Fatalf("webhook credentials were not encrypted: secret=%q token=%q", stored.Secret, stored.AccessToken)
	}
	service := services.NewNotificationServiceWithProtector(db, ring)
	targets, err := service.ListWebhookOutboxTargets(
		request.Context(),
		models.WebhookEventTicketCreated,
	)
	if err != nil || len(targets) != 1 {
		t.Fatalf("encrypted webhook reload targets=%+v err=%v", targets, err)
	}
}

func TestWebhookHandlerFailsClosedWithoutKeyring(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:webhook-no-key?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.WebhookConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.User{
		ID: 1, Username: "admin-no-key", Email: "admin-no-key@example.test",
		PasswordHash: "hash",
	}).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewWebhookHandlerWithProtector(db, nil)
	router := gin.New()
	router.POST("/webhooks", func(c *gin.Context) {
		c.Set("user_id", uint(1))
		handler.CreateWebhook(c)
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/webhooks",
		strings.NewReader(`{
			"name":"must rollback",
			"provider":"custom",
			"webhook_url":"https://hooks.example.test/events",
			"secret":"never-plaintext"
		}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var count int64
	if err := db.Model(&models.WebhookConfig{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed encrypted write left %d webhook rows", count)
	}
}

func TestWebhookHandlerRejectsSSRFTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:webhook-ssrf?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.WebhookConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.User{
		ID: 1, Username: "admin-ssrf", Email: "admin-ssrf@example.test",
		PasswordHash: "hash",
	}).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewWebhookHandlerWithProtector(db, nil)
	router := gin.New()
	router.POST("/webhooks", func(c *gin.Context) {
		c.Set("user_id", uint(1))
		handler.CreateWebhook(c)
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/webhooks",
		strings.NewReader(`{
			"name":"metadata probe",
			"provider":"custom",
			"webhook_url":"http://169.254.169.254/latest/meta-data"
		}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var count int64
	if err := db.Model(&models.WebhookConfig{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unsafe webhook persisted %d rows", count)
	}
}
