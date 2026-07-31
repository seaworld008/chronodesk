package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTrustedDeviceHandlerStrictPageEnvelopeAndAuthFirst(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.OTPTrustedDevice{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "trusted-handler",
		Email:        "trusted-handler@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	devices := make([]models.OTPTrustedDevice, 0, 30)
	for index := 0; index < 30; index++ {
		devices = append(devices, models.OTPTrustedDevice{
			UserID:          user.ID,
			DeviceTokenHash: fmt.Sprintf("handler-hash-%d", index),
			DeviceName:      fmt.Sprintf("Device %d", index),
			LastUsedAt:      now.Add(time.Duration(index) * time.Minute),
			ExpiresAt:       now.Add(24 * time.Hour),
		})
	}
	if err := db.Create(&devices).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewUserHandler(nil, services.NewTrustedDeviceService(db))
	router := gin.New()
	router.GET("/trusted-devices", func(c *gin.Context) {
		if c.GetHeader("X-Test-Authenticated") == "true" {
			c.Set("user_id", user.ID)
		}
		handler.GetTrustedDevices(c)
	})

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(
		unauthorized,
		httptest.NewRequest(
			http.MethodGet,
			"/trusted-devices?unknown=value",
			nil,
		),
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf(
			"authorization-before-pagination status = %d, body=%s",
			unauthorized.Code,
			unauthorized.Body.String(),
		)
	}

	invalidRequest := httptest.NewRequest(
		http.MethodGet,
		"/trusted-devices?page_size=101",
		nil,
	)
	invalidRequest.Header.Set("X-Test-Authenticated", "true")
	invalid := httptest.NewRecorder()
	router.ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d, body=%s", invalid.Code, invalid.Body)
	}

	validRequest := httptest.NewRequest(
		http.MethodGet,
		"/trusted-devices?page=2&page_size=25&sort_by=last_used_at&sort_order=asc",
		nil,
	)
	validRequest.Header.Set("X-Test-Authenticated", "true")
	valid := httptest.NewRecorder()
	router.ServeHTTP(valid, validRequest)
	if valid.Code != http.StatusOK {
		t.Fatalf("valid status = %d, body=%s", valid.Code, valid.Body)
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Items      []TrustedDeviceResponse `json:"items"`
			Total      int64                   `json:"total"`
			Page       int                     `json:"page"`
			PageSize   int                     `json:"page_size"`
			TotalPages int                     `json:"total_pages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(valid.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 0 || body.Data.Total != 30 ||
		body.Data.Page != 2 || body.Data.PageSize != 25 ||
		body.Data.TotalPages != 2 || len(body.Data.Items) != 5 {
		t.Fatalf("unexpected response: %+v", body)
	}
}
