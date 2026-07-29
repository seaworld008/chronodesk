package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestDeleteUserSoftDeletesAndRevokesSessions(t *testing.T) {
	db := openTestDB(t)
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.LoginHistory{},
		&models.OTPTrustedDevice{},
	); err != nil {
		t.Fatalf("migrate user session schemas: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE refresh_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id),
			token TEXT NOT NULL,
			revoked BOOLEAN NOT NULL DEFAULT FALSE,
			revoked_at DATETIME
		)
	`).Error; err != nil {
		t.Fatalf("create refresh token table: %v", err)
	}

	admin := models.User{
		Username:     "remaining-admin",
		Email:        "remaining-admin@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	user := models.User{
		Username:     "deletable-agent",
		Email:        "deletable-agent@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAgent,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"INSERT INTO refresh_tokens (user_id, token, revoked) VALUES (?, ?, ?)",
		user.ID,
		"test-refresh-token",
		false,
	).Error; err != nil {
		t.Fatal(err)
	}
	history := models.LoginHistory{
		UserID:      user.ID,
		Username:    user.Username,
		Email:       user.Email,
		IPAddress:   "127.0.0.1",
		LoginTime:   time.Now(),
		LoginStatus: models.LoginStatusSuccess,
		SessionID:   "delete-session",
		IsActive:    true,
	}
	if err := db.Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	device := models.OTPTrustedDevice{
		UserID:          user.ID,
		DeviceTokenHash: "delete-device",
		DeviceName:      "test",
		LastUsedAt:      time.Now(),
		ExpiresAt:       time.Now().Add(time.Hour),
	}
	if err := db.Create(&device).Error; err != nil {
		t.Fatal(err)
	}

	if err := NewAdminUserService(db).DeleteUser(context.Background(), user.ID); err != nil {
		t.Fatalf("delete logged-in user: %v", err)
	}

	var visible models.User
	if err := db.First(&visible, user.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("soft-deleted user remains visible: %v", err)
	}
	var deleted models.User
	if err := db.Unscoped().First(&deleted, user.ID).Error; err != nil {
		t.Fatalf("load soft-deleted user: %v", err)
	}
	if !deleted.DeletedAt.Valid || deleted.Status != models.UserStatusDeleted {
		t.Fatalf("deleted user state = status %q, deleted_at=%v", deleted.Status, deleted.DeletedAt)
	}

	var refresh struct {
		Revoked   bool
		RevokedAt *time.Time
	}
	if err := db.Table("refresh_tokens").
		Select("revoked", "revoked_at").
		Where("user_id = ?", user.ID).
		Scan(&refresh).Error; err != nil {
		t.Fatal(err)
	}
	if !refresh.Revoked || refresh.RevokedAt == nil {
		t.Fatalf("refresh token was not revoked: %+v", refresh)
	}
	if err := db.First(&history, history.ID).Error; err != nil {
		t.Fatal(err)
	}
	if history.IsActive || history.LogoutTime == nil {
		t.Fatalf("login history remains active: %+v", history)
	}
	if err := db.First(&device, device.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !device.Revoked {
		t.Fatal("trusted device was not revoked")
	}
}
