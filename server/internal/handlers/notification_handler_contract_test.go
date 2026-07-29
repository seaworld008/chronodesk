package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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
