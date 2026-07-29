package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
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
	if err := db.Exec(`
		CREATE TABLE IF NOT EXISTS refresh_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			session_id TEXT NOT NULL,
			revoked BOOLEAN NOT NULL DEFAULT FALSE,
			revoked_at DATETIME
		)
	`).Error; err != nil {
		t.Fatalf("failed to create refresh token session table: %v", err)
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

func TestGetLoginHistory_InvalidOrderByFallsBackToDefault(t *testing.T) {
	db := setupUserServiceLoginHistoryDB(t)
	svc := NewUserService(db)

	userID := seedUserForLoginHistory(t, db, "sort@example.com")
	now := time.Now()
	oldLogin := now.Add(-3 * time.Hour)
	newLogin := now.Add(-1 * time.Hour)

	fixtures := []models.LoginHistory{
		{
			UserID:      userID,
			Username:    "sort-user",
			Email:       "sort@example.com",
			IPAddress:   "10.0.0.11",
			LoginTime:   oldLogin,
			LoginStatus: models.LoginStatusSuccess,
			DeviceType:  "desktop",
			LoginMethod: "password",
			SessionID:   "sort-sess-old",
			IsActive:    true,
		},
		{
			UserID:      userID,
			Username:    "sort-user",
			Email:       "sort@example.com",
			IPAddress:   "10.0.0.12",
			LoginTime:   newLogin,
			LoginStatus: models.LoginStatusSuccess,
			DeviceType:  "desktop",
			LoginMethod: "password",
			SessionID:   "sort-sess-new",
			IsActive:    true,
		},
	}
	if err := db.Create(&fixtures).Error; err != nil {
		t.Fatalf("failed to seed login history: %v", err)
	}

	req := &models.LoginHistoryRequest{
		Page:     1,
		PageSize: 10,
		OrderBy:  "login_time; DROP TABLE login_histories;--",
		Order:    "ASC INVALID",
	}
	records, _, err := svc.GetLoginHistory(context.Background(), userID, req)
	if err != nil {
		t.Fatalf("GetLoginHistory returned error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].SessionID != "sort-sess-new" {
		t.Fatalf("expected fallback default order by latest login_time first, got %s", records[0].SessionID)
	}
}

func TestDeleteLoginSession(t *testing.T) {
	db := setupUserServiceLoginHistoryDB(t)
	svc := NewUserService(db)

	ownerID := seedUserForLoginHistory(t, db, "owner@example.com")
	otherID := seedUserForLoginHistory(t, db, "other-owner@example.com")

	loginTime := time.Now().Add(-30 * time.Minute)
	session := &models.LoginHistory{
		UserID:      ownerID,
		Username:    "owner",
		Email:       "owner@example.com",
		IPAddress:   "127.0.0.1",
		LoginTime:   loginTime,
		LoginStatus: models.LoginStatusSuccess,
		DeviceType:  "desktop",
		LoginMethod: "password",
		SessionID:   "owner-session",
		IsActive:    true,
	}
	if err := db.Create(session).Error; err != nil {
		t.Fatalf("failed to create login history: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO refresh_tokens (user_id, session_id, revoked) VALUES (?, ?, ?)",
		ownerID,
		session.SessionID,
		false,
	).Error; err != nil {
		t.Fatalf("failed to create refresh token session: %v", err)
	}

	if err := svc.DeleteLoginSession(context.Background(), ownerID, session.ID); err != nil {
		t.Fatalf("DeleteLoginSession returned error: %v", err)
	}

	var refreshed models.LoginHistory
	if err := db.First(&refreshed, session.ID).Error; err != nil {
		t.Fatalf("failed to fetch login history: %v", err)
	}
	if refreshed.IsActive {
		t.Fatalf("expected session to be inactive after delete")
	}
	if refreshed.LogoutTime == nil {
		t.Fatalf("expected logout_time to be set")
	}
	var activeTokens int64
	if err := db.Table("refresh_tokens").
		Where("user_id = ? AND session_id = ? AND revoked = ?", ownerID, session.SessionID, false).
		Count(&activeTokens).Error; err != nil {
		t.Fatalf("failed to count active refresh tokens: %v", err)
	}
	if activeTokens != 0 {
		t.Fatalf("expected session refresh tokens to be revoked, got %d active", activeTokens)
	}

	err := svc.DeleteLoginSession(context.Background(), otherID, session.ID)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected gorm.ErrRecordNotFound for cross-user delete, got %v", err)
	}
}
