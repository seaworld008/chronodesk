package services

import (
	"context"
	"testing"
	"time"

	"gongdan-system/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupUserServiceLoginHistoryDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite memory db: %v", err)
	}

	if err := db.AutoMigrate(&models.User{}, &models.LoginHistory{}); err != nil {
		t.Fatalf("failed to migrate schemas: %v", err)
	}

	return db
}

func seedUserForLoginHistory(t *testing.T, db *gorm.DB, email string) uint {
	t.Helper()

	user := models.User{
		Username:     email,
		Email:        email,
		PasswordHash: "hash",
		Role:         models.RoleCustomer,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	return user.ID
}

func TestGetLoginHistoryFilters(t *testing.T) {
	db := setupUserServiceLoginHistoryDB(t)
	svc := NewUserService(db)

	userID := seedUserForLoginHistory(t, db, "user@example.com")
	otherUserID := seedUserForLoginHistory(t, db, "other@example.com")

	now := time.Now()

	fixtures := []models.LoginHistory{
		{
			UserID:      userID,
			Username:    "user",
			Email:       "user@example.com",
			IPAddress:   "10.0.0.1",
			LoginTime:   now.Add(-2 * time.Hour),
			LoginStatus: models.LoginStatusSuccess,
			DeviceType:  "desktop",
			LoginMethod: "password+trusted",
			SessionID:   "sess-1",
			IsActive:    true,
		},
		{
			UserID:      userID,
			Username:    "user",
			Email:       "user@example.com",
			IPAddress:   "10.0.0.2",
			LoginTime:   now.Add(-1 * time.Hour),
			LoginStatus: models.LoginStatusFailed,
			DeviceType:  "mobile",
			LoginMethod: "password+otp",
			SessionID:   "sess-2",
			IsActive:    false,
		},
		{
			UserID:      otherUserID,
			Username:    "other",
			Email:       "other@example.com",
			IPAddress:   "192.168.0.5",
			LoginTime:   now,
			LoginStatus: models.LoginStatusSuccess,
			DeviceType:  "desktop",
			LoginMethod: "password",
			SessionID:   "sess-3",
			IsActive:    true,
		},
	}

	if err := db.Create(&fixtures).Error; err != nil {
		t.Fatalf("failed to seed login history: %v", err)
	}

	if err := db.Model(&models.LoginHistory{}).
		Where("session_id = ?", "sess-2").
		Updates(map[string]any{
			"is_active":   false,
			"logout_time": now.Add(-30 * time.Minute),
		}).Error; err != nil {
		t.Fatalf("failed to update seeded history: %v", err)
	}

	if testing.Verbose() {
		var rows []models.LoginHistory
		if err := db.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
			t.Fatalf("failed to inspect login history: %v", err)
		}
		t.Logf("seeded histories: %+v", rows)
	}

	req := &models.LoginHistoryRequest{
		DeviceType: "desktop",
		Page:       1,
		PageSize:   10,
	}
	records, total, err := svc.GetLoginHistory(context.Background(), userID, req)
	if err != nil {
		t.Fatalf("GetLoginHistory returned error: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected total 1, got %d", total)
	}
	if len(records) != 1 || records[0].SessionID != "sess-1" {
		t.Fatalf("expected desktop session sess-1, got %+v", records)
	}

	activeFlag := false
	req = &models.LoginHistoryRequest{
		LoginMethod: "password+otp",
		IsActive:    &activeFlag,
		Page:        1,
		PageSize:    10,
	}
	records, total, err = svc.GetLoginHistory(context.Background(), userID, req)
	if err != nil {
		t.Fatalf("GetLoginHistory returned error: %v", err)
	}
	if total != 1 || len(records) != 1 {
		t.Fatalf("expected 1 inactive otp record, got total=%d len=%d", total, len(records))
	}
	if records[0].SessionID != "sess-2" {
		t.Fatalf("expected session sess-2, got %s", records[0].SessionID)
	}

	req = &models.LoginHistoryRequest{
		SessionID: "sess-1",
		Page:      1,
		PageSize:  10,
	}
	records, total, err = svc.GetLoginHistory(context.Background(), userID, req)
	if err != nil {
		t.Fatalf("GetLoginHistory returned error: %v", err)
	}
	if total != 1 || len(records) != 1 {
		t.Fatalf("expected 1 session match, got total=%d len=%d", total, len(records))
	}
	if records[0].SessionID != "sess-1" {
		t.Fatalf("expected session sess-1, got %s", records[0].SessionID)
	}
}
