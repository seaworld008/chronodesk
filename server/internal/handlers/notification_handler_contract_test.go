package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestNotificationListAcceptsOnlyCurrentQueryContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name        string
		query       string
		unsupported string
	}{
		{
			name:  "current react admin contract",
			query: "page=1&page_size=20&sort=%5B%22created_at%22%2C%22DESC%22%5D&filter=%7B%22is_read%22%3Afalse%7D",
		},
		{name: "old limit", query: "limit=20", unsupported: "limit"},
		{name: "old offset", query: "offset=0", unsupported: "offset"},
		{name: "old direct read filter", query: "is_read=false", unsupported: "is_read"},
		{name: "old direct type filter", query: "type=system_alert", unsupported: "type"},
		{name: "old direct priority filter", query: "priority=normal", unsupported: "priority"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(
				http.MethodGet,
				"/notifications?"+test.query,
				nil,
			)
			if got := unsupportedNotificationListQuery(context); got != test.unsupported {
				t.Fatalf("unsupported query = %q, want %q", got, test.unsupported)
			}
		})
	}
}

func TestNotificationPreferenceUpdateRejectsUntrustedPersistenceFields(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.NotificationPreference{}); err != nil {
		t.Fatal(err)
	}
	const userID = uint(7)
	existing := models.NotificationPreference{
		UserID:           userID,
		NotificationType: models.NotificationTypeTicketAssigned,
		EmailEnabled:     true,
		InAppEnabled:     true,
		WebhookEnabled:   false,
		MaxDailyCount:    50,
		BatchDelivery:    false,
		BatchInterval:    60,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewNotificationHandler(
		services.NewNotificationServiceWithProtector(db, nil),
	)
	router := gin.New()
	router.PUT("/preferences", func(c *gin.Context) {
		c.Set("user_id", userID)
		handler.UpdateNotificationPreferences(c)
	})

	validItem := `"notification_type":"ticket_assigned","email_enabled":false,"in_app_enabled":true,"webhook_enabled":false,"max_daily_count":25,"batch_delivery":false,"batch_interval":30`
	tests := []struct {
		name string
		body string
	}{
		{name: "null", body: `null`},
		{name: "array", body: `[]`},
		{name: "legacy array item", body: `[{}]`},
		{name: "unknown top level", body: `{"preferences":[],"ghost":true}`},
		{
			name: "timestamp smuggle",
			body: `{"preferences":[{` + validItem + `,"created_at":"2026-07-31T00:00:00Z"}]}`,
		},
		{
			name: "identity smuggle",
			body: `{"preferences":[{` + validItem + `,"id":99,"user_id":42}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPut,
				"/preferences",
				bytes.NewBufferString(test.body),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want 400; body=%s",
					response.Code,
					response.Body.String(),
				)
			}
			var persisted models.NotificationPreference
			if err := db.First(&persisted, existing.ID).Error; err != nil {
				t.Fatal(err)
			}
			if persisted.UserID != userID ||
				persisted.MaxDailyCount != 50 ||
				!persisted.EmailEnabled {
				t.Fatalf("invalid request mutated preference: %+v", persisted)
			}
		})
	}

	request := httptest.NewRequest(
		http.MethodPut,
		"/preferences",
		bytes.NewBufferString(
			`{"preferences":[{`+validItem+`},`+
				`{"notification_type":"ticket_status_changed",`+
				`"email_enabled":false,"in_app_enabled":false,`+
				`"webhook_enabled":false,"max_daily_count":0,`+
				`"batch_delivery":false,"batch_interval":1}]}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("canonical status = %d, body=%s", response.Code, response.Body.String())
	}
	var persisted models.NotificationPreference
	if err := db.Where(
		"user_id = ? AND notification_type = ?",
		userID,
		models.NotificationTypeTicketAssigned,
	).First(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.UserID != userID ||
		persisted.MaxDailyCount != 25 ||
		persisted.EmailEnabled {
		t.Fatalf("canonical persisted preference = %+v", persisted)
	}
	var allDisabled models.NotificationPreference
	if err := db.Where(
		"user_id = ? AND notification_type = ?",
		userID,
		models.NotificationTypeTicketStatusChanged,
	).First(&allDisabled).Error; err != nil {
		t.Fatal(err)
	}
	if allDisabled.EmailEnabled ||
		allDisabled.InAppEnabled ||
		allDisabled.WebhookEnabled ||
		allDisabled.MaxDailyCount != 0 ||
		allDisabled.BatchInterval != 1 {
		t.Fatalf("all-disabled persisted preference = %+v", allDisabled)
	}
}

func TestNotificationListRejectsRemovedDirectFilterParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	handler := NewNotificationHandler(services.NewNotificationServiceWithProtector(db, nil))
	router := gin.New()
	router.GET("/notifications", func(c *gin.Context) {
		c.Set("user_id", uint(7))
		handler.GetNotifications(c)
	})

	for _, query := range []string{
		"limit=10",
		"offset=0",
		"is_read=false",
		"type=system_alert",
		"priority=normal",
	} {
		request := httptest.NewRequest(
			http.MethodGet,
			"/notifications?"+query,
			nil,
		)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400; body=%s", query, response.Code, response.Body.String())
		}
		var body struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s response is not JSON: %v", query, err)
		}
		if body.Code != 1 || body.Msg == "" {
			t.Fatalf("%s response = %+v, want stable Chinese error", query, body)
		}
	}
}

func TestNotificationListUsesStrictBoundedPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.Notification{}); err != nil {
		t.Fatal(err)
	}
	handler := NewNotificationHandler(services.NewNotificationServiceWithProtector(db, nil))
	router := gin.New()
	router.GET("/notifications", func(c *gin.Context) {
		c.Set("user_id", uint(7))
		handler.GetNotifications(c)
	})

	for _, query := range []string{
		"page=0",
		"page=-1",
		"page_size=0",
		"page_size=-1",
		"page_size=101",
		"page_size=unbounded",
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "/notifications?"+query, nil),
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", query, response.Code, response.Body.String())
		}
	}
}
