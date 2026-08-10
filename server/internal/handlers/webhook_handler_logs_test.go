package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/agentplatform"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type webhookTestRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip webhookTestRoundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

func TestWebhookHandlerGetWebhookLogsReturnsScopedCursorRows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "webhook.db")),
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
		Username:     "webhook-log-admin",
		Email:        "webhook-log-admin@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}
	config := models.WebhookConfig{
		OrganizationID: 1,
		ProjectID:      10,
		Name:           "log query webhook",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://hooks.example.test/events",
		Status:         models.WebhookStatusActive,
		CreatedBy:      1,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	testLog := models.WebhookLog{
		OrganizationID: 1,
		ProjectID:      10,
		ConfigID:       config.ID,
		EventType:      models.WebhookEventSystemAlert,
		Status:         "failed",
		RequestMethod:  http.MethodPost,
	}
	if err := db.Create(&testLog).Error; err != nil {
		t.Fatal(err)
	}
	decoyLogs := []models.WebhookLog{
		{
			OrganizationID: 1,
			ProjectID:      10,
			ConfigID:       config.ID,
			EventType:      models.WebhookEventSystemAlert,
			Status:         "success",
			RequestMethod:  http.MethodPost,
		},
		{
			OrganizationID: 1,
			ProjectID:      11,
			ConfigID:       config.ID,
			EventType:      models.WebhookEventSystemAlert,
			Status:         "failed",
			RequestMethod:  http.MethodPost,
		},
	}
	if err := db.Create(&decoyLogs).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewWebhookHandlerWithProtector(db, nil)
	if err := handler.ConfigureListCursor(
		[]byte("webhook-handler-log-cursor-test-key"),
	); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/webhooks/:id/logs", func(c *gin.Context) {
		bindWebhookProjectTestContext(t, c)
		handler.GetWebhookLogs(c)
	})
	configPath := "/webhooks/" + strconv.FormatUint(uint64(config.ID), 10)

	var listStatement *gorm.Statement
	const captureCallback = "test:capture_webhook_log_query_statements"
	if err := db.Callback().Query().Before("gorm:query").Register(
		captureCallback,
		func(tx *gorm.DB) {
			if tx.Statement == nil || tx.Statement.Table != "webhook_logs" {
				return
			}
			switch tx.Statement.Dest.(type) {
			case *[]models.WebhookLog:
				listStatement = tx.Statement
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Query().Remove(captureCallback); err != nil {
			t.Errorf("remove query capture callback: %v", err)
		}
	})

	request := httptest.NewRequest(
		http.MethodGet,
		configPath+"/logs?status=failed&limit=10",
		nil,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	var payload struct {
		Code int `json:"code"`
		Data struct {
			Items      []models.WebhookLog `json:"items"`
			NextCursor string              `json:"next_cursor"`
			HasMore    bool                `json:"has_more"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != 0 {
		t.Fatalf("code=%d body=%s", payload.Code, response.Body.String())
	}
	if len(payload.Data.Items) != 1 {
		t.Fatalf(
			"items=%d, want 1 counted row; body=%s",
			len(payload.Data.Items),
			response.Body.String(),
		)
	}
	if payload.Data.Items[0].ID != testLog.ID {
		t.Fatalf(
			"item id=%d, want scoped failed test log %d; body=%s",
			payload.Data.Items[0].ID,
			testLog.ID,
			response.Body.String(),
		)
	}
	if listStatement == nil {
		t.Fatal("did not capture scoped webhook log list statement")
	}
	if payload.Data.HasMore || payload.Data.NextCursor != "" {
		t.Fatalf("unexpected continuation: %+v", payload.Data)
	}
}

func TestWebhookHandlerQueuesThenWorkerExpiresUncertainDelivery(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	projectService, project, user, db := projectHandlerTestService(t)
	if err := db.AutoMigrate(
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.WebhookLog{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.ProjectMembership{}).
		Where("project_id = ? AND user_id = ?", project.ID, user.ID).
		Update("role", models.ProjectRoleManager).Error; err != nil {
		t.Fatal(err)
	}

	config := models.WebhookConfig{
		OrganizationID: project.OrganizationID,
		ProjectID:      project.ID,
		Name:           "failed delivery transaction",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://webhook.example.test/callback",
		Status:         models.WebhookStatusActive,
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventSystemAlert,
		},
		RetryCount: 1,
		CreatedBy:  user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewKeyring(
		"webhook-handler-test",
		map[string][]byte{
			"webhook-handler-test": bytes.Repeat([]byte{0x5a}, 32),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	protectedSecret, err := security.ProtectOptional(
		protector,
		"webhook-handler-test-secret",
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
		UpdateColumn("secret", protectedSecret).Error; err != nil {
		t.Fatal(err)
	}

	native := services.NewAgentNativeService(db)
	var httpAttempts atomic.Int32
	deliveredTargets := make(chan string, 1)
	notificationService := services.NewNotificationServiceWithClientFactory(
		db,
		protector,
		services.WebhookClientFactoryFunc(func(
			_ context.Context,
			target *url.URL,
			_ time.Duration,
		) (*http.Client, error) {
			deliveredTargets <- target.String()
			return &http.Client{
				Transport: webhookTestRoundTripFunc(
					func(*http.Request) (*http.Response, error) {
						httpAttempts.Add(1)
						return nil, errors.New(
							"target connection failed",
						)
					},
				),
			}, nil
		}),
	)
	notificationService.ConfigureWebhookTestCommands(projectService, native)
	handler := NewWebhookHandlerWithProtector(
		db,
		protector,
		notificationService,
	)

	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("platform_role", models.PlatformRoleMember)
		c.Next()
	})
	group.Use(ProjectCommandScopeMiddleware(projectService))
	group.POST("/webhooks/:id/test", handler.TestWebhook)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/OPS/webhooks/"+
			strconv.FormatUint(uint64(config.ID), 10)+
			"/test",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf(
			"status=%d, want queued result 202; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	var payload struct {
		Code int               `json:"code"`
		Msg  string            `json:"msg"`
		Data TestWebhookResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != 0 ||
		payload.Data.Status != "queued" ||
		!payload.Data.Queued ||
		payload.Data.Delivered ||
		payload.Data.EventID == "" ||
		payload.Data.DeliveryID == "" ||
		payload.Data.SnapshotID == "" {
		t.Fatalf("unexpected queued delivery result: %s", response.Body.String())
	}
	if payload.Msg != "Webhook 测试已入队" {
		t.Fatalf("unstable queued UI message: %q", payload.Msg)
	}
	if httpAttempts.Load() != 0 || len(deliveredTargets) != 0 {
		t.Fatal("Webhook handler performed synchronous external HTTP")
	}

	var logs []models.WebhookLog
	if err := db.Where(
		"organization_id = ? AND project_id = ? AND config_id = ?",
		project.OrganizationID,
		project.ID,
		config.ID,
	).Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if len(logs) != 0 {
		t.Fatalf(
			"queued command committed %d delivery logs before worker, want 0; response=%s",
			len(logs),
			response.Body.String(),
		)
	}

	const changedURL = "https://changed.example.test/ignored"
	if err := db.Model(&models.WebhookConfig{}).
		Where("id = ?", config.ID).
		Updates(map[string]any{
			"webhook_url": changedURL,
			"status":      models.WebhookStatusDisabled,
		}).Error; err != nil {
		t.Fatal(err)
	}
	deliverer, err := agentplatform.NewNativeOutboxDeliverer(
		agentplatform.NativeOutboxDelivererOptions{
			DB:            db,
			Notifications: notificationService,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := native.ProcessOutboxBatch(
		context.Background(),
		"webhook-handler-test-worker",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatalf("process committed webhook test delivery: %v", err)
	}
	if result.Claimed != 1 ||
		result.Delivered != 0 ||
		result.Failed != 0 ||
		result.Expired != 1 ||
		httpAttempts.Load() != 1 {
		t.Fatalf(
			"worker result=%+v HTTP attempts=%d",
			result,
			httpAttempts.Load(),
		)
	}
	notificationService.WaitForWebhookAttemptAudits()
	select {
	case target := <-deliveredTargets:
		if target != config.WebhookURL || target == changedURL {
			t.Fatalf(
				"worker used mutable target %q instead of snapshot %q",
				target,
				config.WebhookURL,
			)
		}
	default:
		t.Fatal("worker did not resolve the frozen webhook target")
	}

	if err := db.Where(
		"organization_id = ? AND project_id = ? AND config_id = ?",
		project.OrganizationID,
		project.ID,
		config.ID,
	).Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 ||
		logs[0].Status != "failed" ||
		logs[0].EventType != models.WebhookEventSystemAlert ||
		logs[0].ErrorMessage != "webhook请求发送失败" {
		t.Fatalf("unexpected worker delivery log: %+v", logs)
	}
	var delivery models.OutboxDelivery
	if err := db.Where("id = ?", payload.Data.DeliveryID).
		Take(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.Status != models.OutboxDeliveryExpired ||
		delivery.DeliveredAt != nil ||
		delivery.ExpiredAt == nil ||
		delivery.Attempts != 1 {
		t.Fatalf("worker did not finalize Outbox delivery: %+v", delivery)
	}
	var snapshot models.WebhookDeliverySnapshot
	if err := db.Where("id = ?", payload.Data.SnapshotID).
		Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot.Secret != "" ||
		snapshot.PreviousSecret != "" ||
		snapshot.PreviousSecretExpiresAt != nil ||
		snapshot.AccessToken != "" ||
		snapshot.CredentialShreddedAt == nil ||
		snapshot.CredentialShredReason == nil ||
		*snapshot.CredentialShredReason !=
			models.WebhookCredentialShredReasonExpired {
		t.Fatalf("uncertain delivery snapshot was not shredded: %+v", snapshot)
	}
	var event models.DomainEvent
	if err := db.Where("id = ?", payload.Data.EventID).
		Take(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.PublishedAt != nil {
		t.Fatalf("expired delivery event was published at %v", event.PublishedAt)
	}
}

func TestWebhookHandlerRejectsRevocationAfterPreflightWithoutHTTP(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	projectService, project, user, db := projectHandlerTestService(t)
	if err := db.AutoMigrate(
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.ProjectMembership{}).
		Where("project_id = ? AND user_id = ?", project.ID, user.ID).
		Update("role", models.ProjectRoleManager).Error; err != nil {
		t.Fatal(err)
	}
	config := models.WebhookConfig{
		OrganizationID: project.OrganizationID,
		ProjectID:      project.ID,
		Name:           "revoked after preflight",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://revoked.example.test/callback",
		Status:         models.WebhookStatusActive,
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventSystemAlert,
		},
		CreatedBy: user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}

	native := services.NewAgentNativeService(db)
	var httpAttempts atomic.Int32
	notificationService := services.NewNotificationServiceWithClientFactory(
		db,
		nil,
		services.WebhookClientFactoryFunc(func(
			context.Context,
			*url.URL,
			time.Duration,
		) (*http.Client, error) {
			httpAttempts.Add(1)
			return nil, nil
		}),
	)
	notificationService.ConfigureWebhookTestCommands(projectService, native)
	handler := NewWebhookHandlerWithProtector(
		db,
		nil,
		notificationService,
	)

	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("platform_role", models.PlatformRoleMember)
		c.Next()
	})
	group.Use(ProjectCommandScopeMiddleware(projectService))
	group.Use(func(c *gin.Context) {
		if err := db.Model(&models.ProjectMembership{}).
			Where(
				"project_id = ? AND user_id = ?",
				project.ID,
				user.ID,
			).
			Update("is_active", false).Error; err != nil {
			t.Fatal(err)
		}
		c.Next()
	})
	group.POST("/webhooks/:id/test", handler.TestWebhook)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/OPS/webhooks/"+
			strconv.FormatUint(uint64(config.ID), 10)+
			"/test",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf(
			"revoked status=%d, want 403; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	if httpAttempts.Load() != 0 {
		t.Fatalf(
			"revoked handler performed %d HTTP attempts",
			httpAttempts.Load(),
		)
	}
	var deliveries int64
	if err := db.Model(&models.OutboxDelivery{}).
		Count(&deliveries).Error; err != nil {
		t.Fatal(err)
	}
	if deliveries != 0 {
		t.Fatalf("revoked handler committed %d Outbox deliveries", deliveries)
	}
}
