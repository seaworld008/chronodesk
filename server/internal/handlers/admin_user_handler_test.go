package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"gongdan-system/internal/models"
	"gongdan-system/internal/services"

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
}
