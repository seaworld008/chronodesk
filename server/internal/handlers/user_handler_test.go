package handlers

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
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

	for _, query := range []string{"unknown=value", "page=%ZZ"} {
		unauthorized := httptest.NewRecorder()
		router.ServeHTTP(
			unauthorized,
			httptest.NewRequest(
				http.MethodGet,
				"/trusted-devices?"+query,
				nil,
			),
		)
		if unauthorized.Code != http.StatusUnauthorized {
			t.Fatalf(
				"authorization-before-pagination query %q status = %d, body=%s",
				query,
				unauthorized.Code,
				unauthorized.Body.String(),
			)
		}
	}

	for _, query := range []string{"page_size=101", "page=%ZZ"} {
		invalidRequest := httptest.NewRequest(
			http.MethodGet,
			"/trusted-devices?"+query,
			nil,
		)
		invalidRequest.Header.Set("X-Test-Authenticated", "true")
		invalid := httptest.NewRecorder()
		router.ServeHTTP(invalid, invalidRequest)
		if invalid.Code != http.StatusBadRequest {
			t.Fatalf(
				"invalid query %q status = %d, body=%s",
				query,
				invalid.Code,
				invalid.Body,
			)
		}
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

func TestLoginHistoryQueryUsesStrictStableDirectoryContract(t *testing.T) {
	query, err := parseLoginHistoryListQuery("")
	if err != nil {
		t.Fatal(err)
	}
	if query.Page != 1 || query.PageSize != 25 ||
		query.OrderBy != "login_time" || query.Order != "desc" {
		t.Fatalf("login history defaults = %+v", query)
	}
	filtered, err := parseLoginHistoryListQuery(
		"page=2&page_size=100&sort_by=created_at&sort_order=asc&" +
			"status=success&login_method=password%2Botp&is_active=false",
	)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Page != 2 || filtered.PageSize != 100 ||
		filtered.OrderBy != "created_at" || filtered.Order != "asc" ||
		filtered.Status == nil ||
		*filtered.Status != models.LoginStatusSuccess ||
		filtered.LoginMethod != models.LoginMethodPasswordOTP ||
		filtered.IsActive == nil || *filtered.IsActive {
		t.Fatalf("login history query = %+v", filtered)
	}
	for _, raw := range []string{
		"page=0",
		"page=-1",
		"page_size=101",
		"page=",
		"page=1&page=2",
		"page=%ZZ",
		"page=" + strconv.Itoa(math.MaxInt) + "&page_size=100",
		"order_by=login_time",
		"sort_by=user_id",
		"sort_order=DESC",
		"status=unknown",
		"login_method=password%2Bunknown",
		"is_active=1",
		"start_date=yesterday",
		"start_date=2026-08-02T00%3A00%3A00Z&end_date=2026-08-01T00%3A00%3A00Z",
		"unknown=value",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := parseLoginHistoryListQuery(raw); err == nil {
				t.Fatal("invalid login history query accepted")
			}
		})
	}
}

func TestLoginHistoryResponseExcludesAccountAndSessionIdentifiers(t *testing.T) {
	record := &models.LoginHistoryResponse{
		ID:               11,
		UserID:           42,
		Username:         "must-not-leak",
		Email:            "must-not-leak@example.com",
		IPAddress:        "192.0.2.10",
		LoginTime:        time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
		SessionID:        "reusable-session-identifier",
		LoginStatus:      models.LoginStatusSuccess,
		LoginMethod:      models.LoginMethodPassword,
		Location:         "上海",
		DeviceInfo:       "Chrome",
		SessionDuration:  "10分钟",
		IsCurrentSession: true,
		IsActive:         true,
	}
	encoded, err := json.Marshal(loginHistoryDTO(record))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"user_id",
		"username",
		"email",
		"session_id",
	} {
		if _, exposed := payload[forbidden]; exposed {
			t.Errorf("login history response exposes %q: %s", forbidden, encoded)
		}
	}
}
