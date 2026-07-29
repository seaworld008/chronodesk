package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"github.com/gin-gonic/gin"
)

func TestAssigneeHandlerReturnsOnlyActiveAssignableUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatalf("migrate users: %v", err)
	}

	users := []models.User{
		{
			Username: "active-agent", Email: "active-agent@example.com",
			PasswordHash: "hashed", Role: models.RoleAgent, Status: models.UserStatusActive,
		},
		{
			Username: "active-customer", Email: "active-customer@example.com",
			PasswordHash: "hashed", Role: models.RoleCustomer, Status: models.UserStatusActive,
		},
		{
			Username: "inactive-agent", Email: "inactive-agent@example.com",
			PasswordHash: "hashed", Role: models.RoleAgent, Status: models.UserStatusInactive,
		},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create users: %v", err)
	}

	handler := NewAssigneeHandler(db)
	router := gin.New()
	router.GET("/assignees", func(c *gin.Context) {
		c.Set("user_role", c.GetHeader("X-Test-Role"))
		handler.List(c)
	})
	router.GET("/assignees/:id", func(c *gin.Context) {
		c.Set("user_role", c.GetHeader("X-Test-Role"))
		handler.Get(c)
	})

	adminRequest := httptest.NewRequest(http.MethodGet, "/assignees", nil)
	adminRequest.Header.Set("X-Test-Role", string(models.RoleAdmin))
	adminResponse := httptest.NewRecorder()
	router.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK {
		t.Fatalf("admin status = %d, body=%s", adminResponse.Code, adminResponse.Body.String())
	}
	var payload struct {
		Data struct {
			Items []map[string]any `json:"items"`
			Total int64            `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(adminResponse.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode admin response: %v", err)
	}
	if payload.Data.Total != 1 || len(payload.Data.Items) != 1 {
		t.Fatalf("assignable users = total %d items %d", payload.Data.Total, len(payload.Data.Items))
	}
	if payload.Data.Items[0]["username"] != "active-agent" {
		t.Fatalf("assignable user = %v", payload.Data.Items[0]["username"])
	}
	if _, exposed := payload.Data.Items[0]["email"]; exposed {
		t.Fatal("assignee directory must not expose email")
	}

	customerRequest := httptest.NewRequest(http.MethodGet, "/assignees", nil)
	customerRequest.Header.Set("X-Test-Role", string(models.RoleCustomer))
	customerResponse := httptest.NewRecorder()
	router.ServeHTTP(customerResponse, customerRequest)
	if customerResponse.Code != http.StatusOK {
		t.Fatalf("customer status = %d", customerResponse.Code)
	}
	var customerPayload struct {
		Data struct {
			Items []assigneeResponse `json:"items"`
			Total int64              `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(customerResponse.Body.Bytes(), &customerPayload); err != nil {
		t.Fatalf("decode customer response: %v", err)
	}
	if customerPayload.Data.Total != 0 || len(customerPayload.Data.Items) != 0 {
		t.Fatalf("customer assignees = total %d items %d", customerPayload.Data.Total, len(customerPayload.Data.Items))
	}

	invalidFilterRequest := httptest.NewRequest(http.MethodGet, "/assignees?filter=%7B", nil)
	invalidFilterRequest.Header.Set("X-Test-Role", string(models.RoleAdmin))
	invalidFilterResponse := httptest.NewRecorder()
	router.ServeHTTP(invalidFilterResponse, invalidFilterRequest)
	if invalidFilterResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid assignee filter status = %d, body=%s", invalidFilterResponse.Code, invalidFilterResponse.Body.String())
	}
	var invalidFilterPayload struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(invalidFilterResponse.Body.Bytes(), &invalidFilterPayload); err != nil {
		t.Fatalf("decode invalid assignee filter response: %v", err)
	}
	if invalidFilterPayload.Msg != "处理人筛选条件无效" {
		t.Fatalf("invalid assignee filter message = %q", invalidFilterPayload.Msg)
	}

	invalidIDRequest := httptest.NewRequest(http.MethodGet, "/assignees/not-a-number", nil)
	invalidIDRequest.Header.Set("X-Test-Role", string(models.RoleAdmin))
	invalidIDResponse := httptest.NewRecorder()
	router.ServeHTTP(invalidIDResponse, invalidIDRequest)
	if invalidIDResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid assignee id status = %d, body=%s", invalidIDResponse.Code, invalidIDResponse.Body.String())
	}
	var invalidIDPayload struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(invalidIDResponse.Body.Bytes(), &invalidIDPayload); err != nil {
		t.Fatalf("decode invalid assignee id response: %v", err)
	}
	if invalidIDPayload.Msg != "处理人 ID 无效" {
		t.Fatalf("invalid assignee id message = %q", invalidIDPayload.Msg)
	}
}
