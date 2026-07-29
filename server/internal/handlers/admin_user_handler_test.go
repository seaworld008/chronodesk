package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
)

func TestAdminUserUpdateAllowsClearingPhoneAndEmailVerification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatalf("migrate users: %v", err)
	}
	user := models.User{
		Username: "update-user", Email: "update-user@example.com",
		Phone: "+8613800138000", PasswordHash: "hashed",
		Role: models.RoleAgent, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	handler := NewAdminUserHandler(services.NewAdminUserService(db))
	router := gin.New()
	router.PUT("/users/:id", handler.UpdateUser)

	body, _ := json.Marshal(map[string]any{
		"phone":          "",
		"display_name":   "Updated Agent",
		"email_verified": true,
	})
	request := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/users/%d", user.ID),
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", response.Code, response.Body.String())
	}

	var persisted models.User
	if err := db.First(&persisted, user.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if persisted.Phone != "" {
		t.Fatalf("phone = %q, want empty", persisted.Phone)
	}
	if persisted.DisplayName != "Updated Agent" {
		t.Fatalf("display_name = %q", persisted.DisplayName)
	}
	if !persisted.EmailVerified {
		t.Fatal("email_verified was not updated")
	}

	invalidRequest := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/users/%d", user.ID),
		bytes.NewBufferString(`{"phone":"13800138000"}`),
	)
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	router.ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid phone status = %d, want %d", invalidResponse.Code, http.StatusBadRequest)
	}

	for _, historicalRole := range []string{"user", "superuser"} {
		roleRequest := httptest.NewRequest(
			http.MethodPut,
			fmt.Sprintf("/users/%d", user.ID),
			bytes.NewBufferString(fmt.Sprintf(`{"role":%q}`, historicalRole)),
		)
		roleRequest.Header.Set("Content-Type", "application/json")
		roleResponse := httptest.NewRecorder()
		router.ServeHTTP(roleResponse, roleRequest)
		if roleResponse.Code != http.StatusBadRequest {
			t.Errorf(
				"historical role %q status = %d, want %d; body=%s",
				historicalRole,
				roleResponse.Code,
				http.StatusBadRequest,
				roleResponse.Body.String(),
			)
		}
	}
}

func TestAdminUserCreateMapsRetainedIdentityConflictToChinese409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatalf("migrate users: %v", err)
	}
	deleted := models.User{
		Username:     "retained-handler-user",
		Email:        "retained-handler-user@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAgent,
		Status:       models.UserStatusDeleted,
	}
	if err := db.Create(&deleted).Error; err != nil {
		t.Fatalf("create retained user: %v", err)
	}
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatalf("soft delete retained user: %v", err)
	}

	handler := NewAdminUserHandler(services.NewAdminUserService(db))
	router := gin.New()
	router.POST("/users", handler.CreateUser)

	body, _ := json.Marshal(map[string]any{
		"username": "new-handler-user",
		"email":    deleted.Email,
		"password": "StrongPassword123!",
		"role":     "agent",
	})
	request := httptest.NewRequest(http.MethodPost, "/users", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", response.Code, response.Body.String())
	}
	bodyText := response.Body.String()
	if !strings.Contains(bodyText, "用户名或邮箱已被使用") {
		t.Fatalf("response is not the stable Chinese conflict: %s", bodyText)
	}
	if strings.Contains(bodyText, "SQLSTATE") || strings.Contains(bodyText, "unique constraint") {
		t.Fatalf("response leaked database details: %s", bodyText)
	}
}
