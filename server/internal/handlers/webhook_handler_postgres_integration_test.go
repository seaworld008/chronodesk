package handlers

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostgresWebhookStatsDateAndProjectIsolation(t *testing.T) {
	db := openWebhookStatsPostgresIntegrationDB(t)
	var dateStyle string
	if err := db.Raw("SHOW DateStyle").Scan(&dateStyle).Error; err != nil {
		t.Fatalf("read PostgreSQL DateStyle: %v", err)
	}
	if !strings.HasPrefix(dateStyle, "SQL") {
		t.Fatalf("PostgreSQL DateStyle=%q, want non-ISO SQL style", dateStyle)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
	); err != nil {
		t.Fatalf("migrate isolated Webhook stats schema: %v", err)
	}
	if err := db.Create(&models.User{
		ID:           1,
		Username:     "postgres-webhook-stats-admin",
		Email:        "postgres-webhook-stats-admin@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}).Error; err != nil {
		t.Fatalf("create Webhook stats user: %v", err)
	}
	config := models.WebhookConfig{
		OrganizationID: 1,
		ProjectID:      10,
		Name:           "postgres stats webhook",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://example.invalid/webhook",
		Status:         models.WebhookStatusActive,
		TotalSent:      2,
		TotalSuccess:   1,
		TotalFailed:    1,
		CreatedBy:      1,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatalf("create Webhook config: %v", err)
	}

	referenceDate := time.Now().UTC().Add(-24 * time.Hour)
	createdAt := time.Date(
		referenceDate.Year(),
		referenceDate.Month(),
		referenceDate.Day(),
		12,
		34,
		56,
		0,
		time.UTC,
	)
	wantDate := createdAt.Format(time.DateOnly)
	logs := []models.WebhookLog{
		{
			CreatedAt:      createdAt,
			OrganizationID: 1,
			ProjectID:      10,
			ConfigID:       config.ID,
			EventType:      models.WebhookEventSystemAlert,
			Status:         "success",
		},
		{
			CreatedAt:      createdAt.Add(time.Minute),
			OrganizationID: 1,
			ProjectID:      10,
			ConfigID:       config.ID,
			EventType:      models.WebhookEventSystemAlert,
			Status:         "failed",
		},
		{
			CreatedAt:      createdAt.Add(2 * time.Minute),
			OrganizationID: 1,
			ProjectID:      11,
			ConfigID:       config.ID,
			EventType:      models.WebhookEventSystemAlert,
			Status:         "success",
		},
	}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatalf("create Webhook logs: %v", err)
	}

	gin.SetMode(gin.TestMode)
	handler := NewWebhookHandlerWithProtector(db, nil)
	router := gin.New()
	router.GET("/webhooks/:id/stats", func(context *gin.Context) {
		bindWebhookProjectTestContext(t, context)
		handler.GetWebhookStats(context)
	})
	request := httptest.NewRequest(
		http.MethodGet,
		"/webhooks/"+strconv.FormatUint(uint64(config.ID), 10)+"/stats?days=365",
		nil,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var payload struct {
		Code int                  `json:"code"`
		Data WebhookStatsResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode Webhook stats response: %v", err)
	}
	if payload.Code != 0 {
		t.Fatalf("code=%d body=%s", payload.Code, recorder.Body.String())
	}
	if payload.Data.Summary != (WebhookStatsSummaryResponse{
		TotalSent:    2,
		TotalSuccess: 1,
		TotalFailed:  1,
	}) {
		t.Fatalf("summary=%+v", payload.Data.Summary)
	}
	if len(payload.Data.DailyStats) != 1 {
		t.Fatalf("daily_stats=%+v, want one scoped date", payload.Data.DailyStats)
	}
	daily := payload.Data.DailyStats[0]
	if string(daily.Date) != wantDate {
		t.Fatalf(
			"daily_stats[0].date=%q, want exact YYYY-MM-DD %q",
			daily.Date,
			wantDate,
		)
	}
	if daily.Sent != 2 || daily.Success != 1 || daily.Failed != 1 {
		t.Fatalf(
			"daily_stats[0]=%+v, cross-project delivery leaked into stats",
			daily,
		)
	}

	var responseObject map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &responseObject); err != nil {
		t.Fatalf("decode Webhook stats field contract: %v", err)
	}
	assertWebhookMapFieldAllowlist(
		t,
		responseObject,
		[]string{"code", "msg", "data"},
	)
	data, ok := responseObject["data"].(map[string]any)
	if !ok {
		t.Fatalf("data=%T, want object", responseObject["data"])
	}
	assertWebhookMapFieldAllowlist(
		t,
		data,
		[]string{"summary", "daily_stats", "period"},
	)
	summary, ok := data["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary=%T, want object", data["summary"])
	}
	assertWebhookMapFieldAllowlist(
		t,
		summary,
		[]string{"total_sent", "total_success", "total_failed"},
	)
	dailyItems, ok := data["daily_stats"].([]any)
	if !ok || len(dailyItems) != 1 {
		t.Fatalf("daily_stats=%T %v, want one item", data["daily_stats"], data["daily_stats"])
	}
	dailyObject, ok := dailyItems[0].(map[string]any)
	if !ok {
		t.Fatalf("daily_stats[0]=%T, want object", dailyItems[0])
	}
	assertWebhookMapFieldAllowlist(
		t,
		dailyObject,
		[]string{"date", "sent", "success", "failed"},
	)
}

func openWebhookStatsPostgresIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL Webhook stats evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("Webhook stats integration test requires loopback PostgreSQL")
		}
	}

	silentConfig := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	admin, err := gorm.Open(postgres.Open(rawDSN), silentConfig)
	if err != nil {
		t.Fatalf("open integration PostgreSQL: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatalf("open integration PostgreSQL pool: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := adminSQL.Close(); cleanupErr != nil {
			t.Errorf("close integration PostgreSQL pool: %v", cleanupErr)
		}
	})

	schemaName := fmt.Sprintf(
		"chronodesk_webhook_stats_%d",
		time.Now().UnixNano(),
	)
	quotedSchema := `"` + schemaName + `"`
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create isolated Webhook stats schema: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := admin.Exec(
			"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
		).Error; cleanupErr != nil {
			t.Errorf("drop isolated Webhook stats schema: %v", cleanupErr)
		}
	})

	query := parsed.Query()
	query.Set("search_path", schemaName)
	query.Set("timezone", "UTC")
	query.Set("datestyle", "SQL, DMY")
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), silentConfig)
	if err != nil {
		t.Fatalf("open isolated Webhook stats schema: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open isolated Webhook stats pool: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := sqlDB.Close(); cleanupErr != nil {
			t.Errorf("close isolated Webhook stats pool: %v", cleanupErr)
		}
	})
	return db
}
