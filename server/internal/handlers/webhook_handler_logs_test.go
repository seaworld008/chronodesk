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
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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

func TestWebhookHandlerGetWebhookLogsReturnsCountedRows(t *testing.T) {
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
		Role:         models.RoleAdmin,
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

	handler := NewWebhookHandlerWithProtector(db, nil)
	router := gin.New()
	router.POST("/webhooks/:id/test", func(c *gin.Context) {
		bindWebhookProjectTestContext(t, c)
		handler.TestWebhook(c)
	})
	router.GET("/webhooks/:id/logs", func(c *gin.Context) {
		bindWebhookProjectTestContext(t, c)
		handler.GetWebhookLogs(c)
	})
	configPath := "/webhooks/" + strconv.FormatUint(uint64(config.ID), 10)
	testRequest := httptest.NewRequest(
		http.MethodPost,
		configPath+"/test",
		nil,
	)
	testResponse := httptest.NewRecorder()
	router.ServeHTTP(testResponse, testRequest)
	if testResponse.Code != http.StatusOK {
		t.Fatalf(
			"test webhook status=%d body=%s",
			testResponse.Code,
			testResponse.Body.String(),
		)
	}
	var testPayload struct {
		Code int               `json:"code"`
		Data TestWebhookResult `json:"data"`
	}
	if err := json.Unmarshal(testResponse.Body.Bytes(), &testPayload); err != nil {
		t.Fatal(err)
	}
	if testPayload.Code != 1 ||
		testPayload.Data.Status != "failed" ||
		testPayload.Data.Delivered {
		t.Fatalf("unexpected failed test result: %s", testResponse.Body.String())
	}

	var testLog models.WebhookLog
	if err := db.Where(
		"organization_id = ? AND project_id = ? AND config_id = ?",
		1,
		10,
		config.ID,
	).First(&testLog).Error; err != nil {
		t.Fatalf("load log written by test webhook request: %v", err)
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

	var countStatement *gorm.Statement
	var listStatement *gorm.Statement
	const captureCallback = "test:capture_webhook_log_query_statements"
	if err := db.Callback().Query().Before("gorm:query").Register(
		captureCallback,
		func(tx *gorm.DB) {
			if tx.Statement == nil || tx.Statement.Table != "webhook_logs" {
				return
			}
			switch tx.Statement.Dest.(type) {
			case *int64:
				countStatement = tx.Statement
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
		configPath+"/logs?status=failed&page=1&page_size=10",
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
			Items []models.WebhookLog `json:"items"`
			Total int64               `json:"total"`
			Page  int                 `json:"page"`
			Size  int                 `json:"size"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != 0 {
		t.Fatalf("code=%d body=%s", payload.Code, response.Body.String())
	}
	if payload.Data.Total != 1 {
		t.Fatalf("total=%d, want 1; body=%s", payload.Data.Total, response.Body.String())
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
	if countStatement == nil || listStatement == nil {
		t.Fatalf(
			"did not capture count and list statements: count=%p list=%p",
			countStatement,
			listStatement,
		)
	}
	if countStatement == listStatement {
		t.Fatal("webhook log count and list reused one mutable GORM statement")
	}
}

func TestWebhookHandlerFailedDeliveryCommitsLogThroughProjectMiddleware(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	projectService, project, user, db := projectHandlerTestService(t)
	if err := db.AutoMigrate(
		&models.WebhookConfig{},
		&models.WebhookLog{},
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
		CreatedBy:      user.ID,
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

	handler := NewWebhookHandlerWithProtector(db, protector)
	handler.notificationService = services.NewNotificationServiceWithClientFactory(
		db,
		protector,
		services.WebhookClientFactoryFunc(func(
			context.Context,
			*url.URL,
			time.Duration,
		) (*http.Client, error) {
			return &http.Client{
				Transport: webhookTestRoundTripFunc(
					func(*http.Request) (*http.Response, error) {
						return nil, errors.New("target connection failed")
					},
				),
			}, nil
		}),
	)

	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("user_role", string(models.RoleAgent))
		c.Next()
	})
	group.Use(ProjectScopeMiddleware(projectService, db))
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
	if response.Code != http.StatusOK {
		t.Fatalf(
			"status=%d, want command result 200; body=%s",
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
	if payload.Code != 1 ||
		payload.Data.Status != "failed" ||
		payload.Data.Delivered {
		t.Fatalf("unexpected failed delivery result: %s", response.Body.String())
	}
	if payload.Msg != "Webhook 测试失败，请检查配置和目标服务状态" {
		t.Fatalf("unstable UI failure message: %q", payload.Msg)
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
	if len(logs) != 1 {
		t.Fatalf(
			"failed delivery committed %d logs, want 1; response=%s",
			len(logs),
			response.Body.String(),
		)
	}
	if logs[0].Status != "failed" ||
		logs[0].EventType != models.WebhookEventSystemAlert ||
		logs[0].ErrorMessage != "webhook请求发送失败" {
		t.Fatalf("unexpected committed failed delivery log: %+v", logs[0])
	}
}
