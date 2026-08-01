package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type capturingNotificationListService struct {
	services.NotificationServiceInterface
	filter *models.NotificationFilter
}

func (service *capturingNotificationListService) GetNotifications(
	_ context.Context,
	filter *models.NotificationFilter,
) ([]*models.Notification, int64, error) {
	copy := *filter
	service.filter = &copy
	return []*models.Notification{}, 0, nil
}

func TestNotificationListAcceptsOnlyCurrentQueryContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name  string
		query string
		valid bool
	}{
		{
			name:  "current react admin contract",
			query: "page=1&page_size=20&sort=%5B%22created_at%22%2C%22DESC%22%5D&filter=%7B%22is_read%22%3Afalse%7D",
			valid: true,
		},
		{
			name:  "recipient column sort",
			query: "page=1&page_size=25&sort=%5B%22recipient_id%22%2C%22ASC%22%5D",
			valid: true,
		},
		{
			name:  "sender column sort",
			query: "page=1&page_size=25&sort=%5B%22sender_id%22%2C%22DESC%22%5D",
			valid: true,
		},
		{name: "old limit", query: "limit=20"},
		{name: "old offset", query: "offset=0"},
		{name: "old direct read filter", query: "is_read=false"},
		{name: "old direct type filter", query: "type=system_alert"},
		{name: "old direct priority filter", query: "priority=normal"},
		{name: "duplicate page", query: "page=1&page=2"},
		{name: "empty sort", query: "sort="},
		{name: "invalid encoding", query: "filter=%ZZ"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := strictNotificationListQueryValues(test.query)
			if test.valid && err != nil {
				t.Fatalf("valid query rejected: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("invalid query was accepted")
			}
		})
	}
}

func TestNotificationListRejectsInvalidSortAndFilterValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &capturingNotificationListService{}
	handler := NewNotificationHandler(service)
	for _, query := range []string{
		"sort=%5B%22organization_id%22%2C%22ASC%22%5D",
		"sort=%5B%22created_at%22%2C%22down%22%5D",
		"sort=%7B%22field%22%3A%22created_at%22%7D",
		"filter=%7B%22unknown%22%3Atrue%7D",
		"filter=%7B%22type%22%3A%22unknown%22%7D",
		"filter=%7B%22recipient_id%22%3A8%7D",
		"filter=%7B%22is_read%22%3A1%7D",
	} {
		t.Run(query, func(t *testing.T) {
			router := gin.New()
			router.GET("/notifications", func(c *gin.Context) {
				c.Set("user_id", uint(7))
				handler.GetNotifications(c)
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(
				response,
				httptest.NewRequest(
					http.MethodGet,
					"/notifications?"+query,
					nil,
				),
			)
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status=%d body=%s",
					response.Code,
					response.Body,
				)
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

func TestNotificationListDefaultsToTwentyFiveAndAllowsOneHundred(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &capturingNotificationListService{}
	handler := NewNotificationHandler(service)
	router := gin.New()
	router.GET("/notifications", func(c *gin.Context) {
		c.Set("user_id", uint(7))
		handler.GetNotifications(c)
	})

	for _, test := range []struct {
		query      string
		wantLimit  int
		wantOffset int
		wantPage   string
		wantSize   string
	}{
		{wantLimit: 25, wantPage: `"page":1`, wantSize: `"page_size":25`},
		{
			query:      "page=2&page_size=100",
			wantLimit:  100,
			wantOffset: 100,
			wantPage:   `"page":2`,
			wantSize:   `"page_size":100`,
		},
	} {
		response := httptest.NewRecorder()
		path := "/notifications"
		if test.query != "" {
			path += "?" + test.query
		}
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		if service.filter == nil ||
			service.filter.Limit != test.wantLimit ||
			service.filter.Offset != test.wantOffset {
			t.Fatalf("%s filter=%+v", path, service.filter)
		}
		if !strings.Contains(response.Body.String(), test.wantPage) ||
			!strings.Contains(response.Body.String(), test.wantSize) {
			t.Fatalf("%s body=%s", path, response.Body.String())
		}
	}
}
