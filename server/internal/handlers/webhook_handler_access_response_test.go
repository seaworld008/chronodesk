package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestWebhookHandlersRejectNonManagerProjectRoles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &WebhookHandler{}
	endpoints := []struct {
		name   string
		method string
		path   string
		invoke func(*gin.Context)
	}{
		{
			name:   "list",
			method: http.MethodGet,
			path:   "/api/projects/OPS/webhooks",
			invoke: handler.ListWebhooks,
		},
		{
			name:   "create",
			method: http.MethodPost,
			path:   "/api/projects/OPS/webhooks",
			invoke: handler.CreateWebhook,
		},
		{
			name:   "get",
			method: http.MethodGet,
			path:   "/api/projects/OPS/webhooks/1",
			invoke: handler.GetWebhook,
		},
		{
			name:   "update",
			method: http.MethodPut,
			path:   "/api/projects/OPS/webhooks/1",
			invoke: handler.UpdateWebhook,
		},
		{
			name:   "delete",
			method: http.MethodDelete,
			path:   "/api/projects/OPS/webhooks/1",
			invoke: handler.DeleteWebhook,
		},
		{
			name:   "test",
			method: http.MethodPost,
			path:   "/api/projects/OPS/webhooks/1/test",
			invoke: handler.TestWebhook,
		},
		{
			name:   "logs",
			method: http.MethodGet,
			path:   "/api/projects/OPS/webhooks/1/logs",
			invoke: handler.GetWebhookLogs,
		},
		{
			name:   "stats",
			method: http.MethodGet,
			path:   "/api/projects/OPS/webhooks/1/stats",
			invoke: handler.GetWebhookStats,
		},
	}
	for _, role := range []models.ProjectRole{
		models.ProjectRoleAgent,
		models.ProjectRoleRequester,
		models.ProjectRoleObserver,
	} {
		for _, endpoint := range endpoints {
			t.Run(string(role)+"/"+endpoint.name, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				context, _ := gin.CreateTestContext(recorder)
				context.Request = httptest.NewRequest(
					endpoint.method,
					endpoint.path,
					strings.NewReader(`{}`),
				)
				context.Params = []gin.Param{{Key: "id", Value: "1"}}
				bindWebhookProjectRoleTestContext(t, context, role)

				endpoint.invoke(context)

				if recorder.Code != http.StatusForbidden {
					t.Fatalf(
						"status=%d, want %d; body=%s",
						recorder.Code,
						http.StatusForbidden,
						recorder.Body.String(),
					)
				}
			})
		}
	}
}

func TestRequireWebhookManagerAccessAllowsExactRoleSet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []models.ProjectRole{
		models.ProjectRoleAdmin,
		models.ProjectRoleManager,
	} {
		t.Run(string(role), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(
				http.MethodGet,
				"/api/projects/OPS/webhooks",
				nil,
			)
			bindWebhookProjectRoleTestContext(t, context, role)

			operation, ok := requireWebhookManagerAccess(context)

			if !ok {
				t.Fatalf("role %q denied: body=%s", role, recorder.Body.String())
			}
			if operation.Scope.ProjectID != 10 {
				t.Fatalf("scope=%+v, want project 10", operation.Scope)
			}
		})
	}
}

func TestWebhookConfigResponseUsesExactFieldAllowlist(t *testing.T) {
	now := time.Now().UTC()
	updatedBy := uint(9)
	response := newWebhookConfigResponse(models.WebhookConfig{
		ID:                      7,
		CreatedAt:               now,
		UpdatedAt:               now,
		OrganizationID:          1,
		ProjectID:               10,
		Name:                    "alerts",
		Description:             "operator alerts",
		Provider:                models.WebhookProviderCustom,
		WebhookURL:              "https://example.invalid/webhook",
		Status:                  models.WebhookStatusActive,
		PreviousSecretExpiresAt: &now,
		EnabledEvents:           `["io.chronodesk.system.alert.v1"]`,
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventSystemAlert,
		},
		MessageTemplate: "event",
		MessageFormat:   "text",
		FilterRules:     `{"transition_statuses":["resolved"]}`,
		FilterRulesObj: &models.WebhookFilterRules{
			TransitionStatuses: []models.TicketStatus{
				models.TicketStatusResolved,
			},
		},
		RetryCount:      3,
		RetryInterval:   60,
		TimeoutSeconds:  30,
		IsAsync:         true,
		RateLimit:       60,
		RateLimitWindow: 60,
		LastTriggeredAt: &now,
		LastSuccessAt:   &now,
		LastErrorAt:     &now,
		LastError:       "redacted delivery error",
		TotalSent:       12,
		TotalSuccess:    10,
		TotalFailed:     2,
		CreatedBy:       8,
		UpdatedBy:       &updatedBy,
		Creator: &models.User{
			PlatformRole: models.PlatformRolePlatformAdmin,
			Permissions:  `["sensitive"]`,
			LastLoginIP:  "192.0.2.10",
		},
		Updater: &models.User{
			PlatformRole: models.PlatformRoleSecurityAuditor,
			Permissions:  `["sensitive"]`,
			LastLoginIP:  "192.0.2.11",
		},
	})

	assertWebhookJSONFieldAllowlist(t, response, []string{
		"id",
		"created_at",
		"updated_at",
		"organization_id",
		"project_id",
		"name",
		"description",
		"provider",
		"webhook_url",
		"status",
		"previous_secret_expires_at",
		"enabled_events",
		"enabled_events_list",
		"message_template",
		"message_format",
		"filter_rules",
		"filter_rules_obj",
		"retry_count",
		"retry_interval",
		"timeout_seconds",
		"is_async",
		"rate_limit",
		"rate_limit_window",
		"last_triggered_at",
		"last_success_at",
		"last_error_at",
		"last_error",
		"total_sent",
		"total_success",
		"total_failed",
		"created_by",
		"updated_by",
	})
}

func TestWebhookLogResponseUsesExactFieldAllowlist(t *testing.T) {
	now := time.Now().UTC()
	response := newWebhookLogResponse(models.WebhookLog{
		ID:              11,
		CreatedAt:       now,
		ConfigID:        7,
		Config:          &models.WebhookConfig{Creator: &models.User{}},
		EventType:       models.WebhookEventSystemAlert,
		EventData:       `{"untrusted":"payload"}`,
		RequestURL:      "https://example.invalid/webhook",
		RequestHeaders:  `{"Authorization":"redacted"}`,
		RequestBody:     `{"untrusted":"payload"}`,
		ResponseStatus:  http.StatusBadGateway,
		ResponseHeaders: `{"Set-Cookie":"redacted"}`,
		ResponseBody:    "upstream body",
		ResponseTime:    125,
		Status:          "failed",
		ErrorMessage:    "delivery failed",
		SourceIP:        "192.0.2.20",
	})

	assertWebhookJSONFieldAllowlist(t, response, []string{
		"id",
		"created_at",
		"config_id",
		"event_type",
		"status",
		"response_status",
		"response_time",
		"error_message",
	})
}

func TestWebhookStatsResponseUsesNestedFieldAllowlists(t *testing.T) {
	response := WebhookStatsResponse{
		Summary: WebhookStatsSummaryResponse{
			TotalSent:    8,
			TotalSuccess: 7,
			TotalFailed:  1,
		},
		DailyStats: []WebhookDailyStatsResponse{
			{
				Date:    WebhookDateOnly("2026-07-31"),
				Sent:    3,
				Success: 2,
				Failed:  1,
			},
		},
		Period: "最近7天",
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	assertWebhookMapFieldAllowlist(
		t,
		object,
		[]string{"summary", "daily_stats", "period"},
	)
	summary, ok := object["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary=%T, want object", object["summary"])
	}
	assertWebhookMapFieldAllowlist(
		t,
		summary,
		[]string{"total_sent", "total_success", "total_failed"},
	)
	dailyStats, ok := object["daily_stats"].([]any)
	if !ok || len(dailyStats) != 1 {
		t.Fatalf("daily_stats=%T %v, want one item", object["daily_stats"], object["daily_stats"])
	}
	daily, ok := dailyStats[0].(map[string]any)
	if !ok {
		t.Fatalf("daily_stats[0]=%T, want object", dailyStats[0])
	}
	assertWebhookMapFieldAllowlist(
		t,
		daily,
		[]string{"date", "sent", "success", "failed"},
	)
}

func TestWebhookDateOnlyScanNormalizesSupportedDatabaseValues(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  WebhookDateOnly
	}{
		{
			name: "time",
			value: time.Date(
				2026,
				time.July,
				31,
				23,
				59,
				59,
				0,
				time.FixedZone("database", 8*60*60),
			),
			want: "2026-07-31",
		},
		{
			name:  "string",
			value: "2026-07-31",
			want:  "2026-07-31",
		},
		{
			name:  "bytes",
			value: []byte("2026-07-31"),
			want:  "2026-07-31",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var date WebhookDateOnly
			if err := date.Scan(test.value); err != nil {
				t.Fatalf("Scan(%T): %v", test.value, err)
			}
			if date != test.want {
				t.Fatalf("date=%q, want %q", date, test.want)
			}
		})
	}
}

func TestWebhookDateOnlyScanRejectsNonCanonicalValues(t *testing.T) {
	for _, value := range []any{
		nil,
		"31/07/2026",
		"2026-7-31",
		[]byte("2026-02-30"),
		20260731,
	} {
		date := WebhookDateOnly("2026-07-30")
		if err := date.Scan(value); err == nil {
			t.Fatalf("Scan(%T %v) succeeded", value, value)
		}
		if date != "2026-07-30" {
			t.Fatalf("failed Scan mutated date to %q", date)
		}
	}
	var date *WebhookDateOnly
	if err := date.Scan("2026-07-31"); err == nil {
		t.Fatal("nil WebhookDateOnly receiver accepted a value")
	}
}

func TestWebhookHandlerStatsMatchPublishedNestedContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "webhook-stats.db")),
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
		Username:     "webhook-stats-admin",
		Email:        "webhook-stats-admin@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}
	config := models.WebhookConfig{
		OrganizationID: 1,
		ProjectID:      10,
		Name:           "stats webhook",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://example.invalid/webhook",
		Status:         models.WebhookStatusActive,
		TotalSent:      12,
		TotalSuccess:   9,
		TotalFailed:    3,
		CreatedBy:      1,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
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
			CreatedAt:      createdAt,
			OrganizationID: 1,
			ProjectID:      10,
			ConfigID:       config.ID,
			EventType:      models.WebhookEventSystemAlert,
			Status:         "failed",
		},
		{
			CreatedAt:      createdAt,
			OrganizationID: 1,
			ProjectID:      10,
			ConfigID:       config.ID,
			EventType:      models.WebhookEventSystemAlert,
			Status:         "pending",
		},
	}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewWebhookHandlerWithProtector(db, nil)
	router := gin.New()
	router.GET("/webhooks/:id/stats", func(context *gin.Context) {
		bindWebhookProjectTestContext(t, context)
		handler.GetWebhookStats(context)
	})
	request := httptest.NewRequest(
		http.MethodGet,
		"/webhooks/"+strconv.FormatUint(uint64(config.ID), 10)+"/stats?days=7",
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
		t.Fatal(err)
	}
	if payload.Code != 0 {
		t.Fatalf("code=%d body=%s", payload.Code, recorder.Body.String())
	}
	if payload.Data.Summary != (WebhookStatsSummaryResponse{
		TotalSent:    12,
		TotalSuccess: 9,
		TotalFailed:  3,
	}) {
		t.Fatalf("summary=%+v", payload.Data.Summary)
	}
	if len(payload.Data.DailyStats) != 1 {
		t.Fatalf("daily_stats=%+v, want one day", payload.Data.DailyStats)
	}
	if date := string(payload.Data.DailyStats[0].Date); date != wantDate {
		t.Fatalf("daily_stats[0].date=%q, want %q", date, wantDate)
	}
	if daily := payload.Data.DailyStats[0]; daily.Sent != 3 ||
		daily.Success != 1 || daily.Failed != 1 {
		t.Fatalf("daily_stats[0]=%+v", daily)
	}

	var responseObject map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &responseObject); err != nil {
		t.Fatal(err)
	}
	data, ok := responseObject["data"].(map[string]any)
	if !ok {
		t.Fatalf("data=%T, want object", responseObject["data"])
	}
	assertWebhookMapFieldAllowlist(
		t,
		data,
		[]string{"summary", "daily_stats", "period"},
	)
}

func bindWebhookProjectRoleTestContext(
	t *testing.T,
	context *gin.Context,
	role models.ProjectRole,
) {
	t.Helper()
	scope := models.ProjectScope{OrganizationID: 1, ProjectID: 10}
	requestContext, err := services.WithOperationContext(
		context.Request.Context(),
		services.OperationContext{
			Scope:  scope,
			Actor:  models.HumanActor(1),
			Source: services.SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	context.Set("user_id", uint(1))
	context.Set(projectAccessContextKey, services.ProjectAccess{
		Scope: scope,
		Role:  role,
	})
	context.Set(projectRoleContextKey, string(role))
	context.Request = context.Request.WithContext(requestContext)
}

func assertWebhookJSONFieldAllowlist(
	t *testing.T,
	value any,
	want []string,
) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	assertWebhookMapFieldAllowlist(t, object, want)
}

func assertWebhookMapFieldAllowlist(
	t *testing.T,
	object map[string]any,
	want []string,
) {
	t.Helper()
	if len(object) != len(want) {
		t.Fatalf("fields=%v, want=%v", object, want)
	}
	for _, name := range want {
		if _, exists := object[name]; !exists {
			t.Errorf("field %q is missing from %v", name, object)
		}
	}
}
