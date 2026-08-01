package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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
		PasswordHash: "hash", PlatformRole: models.PlatformRolePlatformAdmin, Status: models.UserStatusActive,
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
		bindWebhookProjectTestContext(t, c)
		handler.CreateWebhook(c)
	})
	router.PUT("/webhooks/:id", func(c *gin.Context) {
		bindWebhookProjectTestContext(t, c)
		handler.UpdateWebhook(c)
	})
	payload := map[string]any{
		"name":              "secure webhook",
		"provider":          "custom",
		"webhook_url":       "https://hooks.example.test/events",
		"secret":            "signature-secret",
		"access_token":      "external-access-token",
		"enabled_events":    []string{"io.chronodesk.ticket.created.v1"},
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
		models.ProjectScope{OrganizationID: 1, ProjectID: 10},
		models.WebhookEventTicketCreated,
		"",
	)
	if err != nil || len(targets) != 1 {
		t.Fatalf("encrypted webhook reload targets=%+v err=%v", targets, err)
	}

	rotationRequest := httptest.NewRequest(
		http.MethodPut,
		"/webhooks/"+strconv.FormatUint(uint64(stored.ID), 10),
		strings.NewReader(`{
			"secret":"rotated-signature-secret",
			"secret_overlap_seconds":3600
		}`),
	)
	rotationRequest.Header.Set("Content-Type", "application/json")
	rotationResponse := httptest.NewRecorder()
	router.ServeHTTP(rotationResponse, rotationRequest)
	if rotationResponse.Code != http.StatusOK {
		t.Fatalf(
			"rotate status=%d body=%s",
			rotationResponse.Code,
			rotationResponse.Body.String(),
		)
	}
	if strings.Contains(rotationResponse.Body.String(), "signature-secret") {
		t.Fatal("webhook rotation response leaked a signing secret")
	}
	if strings.Contains(
		rotationResponse.Body.String(),
		"https://hooks.example.test/events",
	) || strings.Contains(rotationResponse.Body.String(), `"webhook_url"`) {
		t.Fatalf(
			"webhook rotation response leaked raw URL: %s",
			rotationResponse.Body.String(),
		)
	}
	var rotationPayload struct {
		Data WebhookConfigResponse `json:"data"`
	}
	if err := json.Unmarshal(rotationResponse.Body.Bytes(), &rotationPayload); err != nil {
		t.Fatal(err)
	}
	if !rotationPayload.Data.HasWebhookURL ||
		rotationPayload.Data.WebhookURLMasked !=
			"https://hooks.example.test/…" {
		t.Fatalf(
			"masked URL response = %+v",
			rotationPayload.Data,
		)
	}
	var rotated models.WebhookConfig
	if err := db.First(&rotated, stored.ID).Error; err != nil {
		t.Fatal(err)
	}
	if rotated.WebhookURL != stored.WebhookURL {
		t.Fatalf(
			"omitted edit URL changed from %q to %q",
			stored.WebhookURL,
			rotated.WebhookURL,
		)
	}
	if !security.IsEnvelope(rotated.Secret) ||
		!security.IsEnvelope(rotated.PreviousSecret) ||
		rotated.PreviousSecretExpiresAt == nil ||
		!rotated.PreviousSecretExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("webhook dual-key rotation was not persisted: %+v", rotated)
	}
	current, err := ring.Open(
		rotated.Secret,
		security.FieldAAD(
			"webhook_configs",
			strconv.FormatUint(uint64(rotated.ID), 10),
			"secret",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := ring.Open(
		rotated.PreviousSecret,
		security.FieldAAD(
			"webhook_configs",
			strconv.FormatUint(uint64(rotated.ID), 10),
			"previous_secret",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "rotated-signature-secret" ||
		string(previous) != "signature-secret" {
		t.Fatal("webhook dual-key rotation stored incorrect key material")
	}
	clear(current)
	clear(previous)
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
		bindWebhookProjectTestContext(t, c)
		handler.CreateWebhook(c)
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/webhooks",
		strings.NewReader(`{
			"name":"must rollback",
			"provider":"custom",
			"webhook_url":"https://hooks.example.test/events",
			"secret":"never-plaintext",
			"enabled_events":["io.chronodesk.ticket.created.v1"]
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
		bindWebhookProjectTestContext(t, c)
		handler.CreateWebhook(c)
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/webhooks",
		strings.NewReader(`{
			"name":"metadata probe",
			"provider":"custom",
			"webhook_url":"http://169.254.169.254/latest/meta-data",
			"enabled_events":["io.chronodesk.ticket.created.v1"]
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

func TestWebhookHandlerEnforcesCanonicalEventsAndTransitionPredicates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(
		sqlite.Open("file:webhook-event-contract?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.WebhookConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.User{
		ID:           1,
		Username:     "admin-contract",
		Email:        "admin-contract@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewWebhookHandlerWithProtector(db, nil)
	router := gin.New()
	router.POST("/webhooks", func(c *gin.Context) {
		bindWebhookProjectTestContext(t, c)
		handler.CreateWebhook(c)
	})
	router.PUT("/webhooks/:id", func(c *gin.Context) {
		bindWebhookProjectTestContext(t, c)
		handler.UpdateWebhook(c)
	})

	legacy := httptest.NewRequest(
		http.MethodPost,
		"/webhooks",
		strings.NewReader(`{
			"name":"legacy alias",
			"provider":"custom",
			"webhook_url":"https://hooks.example.test/events",
			"enabled_events":["ticket.resolved"]
		}`),
	)
	legacy.Header.Set("Content-Type", "application/json")
	legacyResponse := httptest.NewRecorder()
	router.ServeHTTP(legacyResponse, legacy)
	if legacyResponse.Code != http.StatusBadRequest {
		t.Fatalf(
			"legacy event status=%d body=%s",
			legacyResponse.Code,
			legacyResponse.Body.String(),
		)
	}

	create := httptest.NewRequest(
		http.MethodPost,
		"/webhooks",
		strings.NewReader(`{
			"name":"resolved transitions",
			"provider":"custom",
			"webhook_url":"https://hooks.example.test/events",
			"enabled_events":["io.chronodesk.ticket.transitioned.v1"],
			"filter_rules":{"transition_statuses":["resolved"]}
		}`),
	)
	create.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusOK {
		t.Fatalf(
			"canonical event status=%d body=%s",
			createResponse.Code,
			createResponse.Body.String(),
		)
	}

	var stored models.WebhookConfig
	if err := db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.MatchesEvent(
		models.WebhookEventTicketTransitioned,
		models.TicketStatusResolved,
	) || stored.MatchesEvent(
		models.WebhookEventTicketTransitioned,
		models.TicketStatusClosed,
	) {
		t.Fatalf("transition predicate was not persisted: %+v", stored.FilterRulesObj)
	}

	removeTransition := httptest.NewRequest(
		http.MethodPut,
		"/webhooks/"+strconv.FormatUint(uint64(stored.ID), 10),
		strings.NewReader(`{
			"enabled_events":["io.chronodesk.ticket.created.v1"]
		}`),
	)
	removeTransition.Header.Set("Content-Type", "application/json")
	removeResponse := httptest.NewRecorder()
	router.ServeHTTP(removeResponse, removeTransition)
	if removeResponse.Code != http.StatusBadRequest {
		t.Fatalf(
			"orphaned predicate status=%d body=%s",
			removeResponse.Code,
			removeResponse.Body.String(),
		)
	}
}

func TestWebhookHandlerDoesNotExposeAnotherProject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(
		sqlite.Open("file:webhook-project-isolation?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.WebhookConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.User{
		ID: 1, Username: "project-webhook-admin",
		Email:        "project-webhook-admin@example.test",
		PasswordHash: "hash",
	}).Error; err != nil {
		t.Fatal(err)
	}
	configs := []models.WebhookConfig{
		{
			OrganizationID: 1,
			ProjectID:      10,
			Name:           "visible",
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     "https://visible.example.test",
			Status:         models.WebhookStatusActive,
			CreatedBy:      1,
		},
		{
			OrganizationID: 1,
			ProjectID:      11,
			Name:           "hidden",
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     "https://hidden.example.test",
			Status:         models.WebhookStatusActive,
			CreatedBy:      1,
		},
	}
	if err := db.Create(&configs).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewWebhookHandlerWithProtector(db, nil)
	router := gin.New()
	router.GET("/webhooks", func(c *gin.Context) {
		bindWebhookProjectTestContext(t, c)
		handler.ListWebhooks(c)
	})
	router.GET("/webhooks/:id", func(c *gin.Context) {
		bindWebhookProjectTestContext(t, c)
		handler.GetWebhook(c)
	})
	listResponse := httptest.NewRecorder()
	router.ServeHTTP(
		listResponse,
		httptest.NewRequest(http.MethodGet, "/webhooks", nil),
	)
	if listResponse.Code != http.StatusOK ||
		!strings.Contains(listResponse.Body.String(), `"visible"`) ||
		strings.Contains(listResponse.Body.String(), `"hidden"`) {
		t.Fatalf(
			"project webhook list status=%d body=%s",
			listResponse.Code,
			listResponse.Body.String(),
		)
	}
	hiddenResponse := httptest.NewRecorder()
	router.ServeHTTP(
		hiddenResponse,
		httptest.NewRequest(
			http.MethodGet,
			"/webhooks/"+strconv.FormatUint(uint64(configs[1].ID), 10),
			nil,
		),
	)
	if hiddenResponse.Code != http.StatusNotFound {
		t.Fatalf(
			"cross-project webhook status=%d body=%s",
			hiddenResponse.Code,
			hiddenResponse.Body.String(),
		)
	}
}

func bindWebhookProjectTestContext(t *testing.T, c *gin.Context) {
	t.Helper()
	scope := models.ProjectScope{OrganizationID: 1, ProjectID: 10}
	operationContext, err := services.WithOperationContext(
		c.Request.Context(),
		services.OperationContext{
			Scope:  scope,
			Actor:  models.HumanActor(1),
			Source: services.SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	c.Set("user_id", uint(1))
	c.Set(projectAccessContextKey, services.ProjectAccess{
		Scope: scope,
		Role:  models.ProjectRoleAdmin,
	})
	c.Set(projectRoleContextKey, string(models.ProjectRoleAdmin))
	c.Request = c.Request.WithContext(operationContext)
}
