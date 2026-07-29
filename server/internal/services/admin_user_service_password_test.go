package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gongdan-system/internal/models"
)

func TestResetUserPasswordAtomicallyRevokesSessions(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.LoginHistory{}); err != nil {
		t.Fatalf("migrate password reset schemas: %v", err)
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

	lockedUntil := time.Now().Add(time.Hour)
	user := models.User{
		Username:      "password-reset-agent",
		Email:         "password-reset-agent@example.com",
		PasswordHash:  "old-password-hash",
		Role:          models.RoleAgent,
		Status:        models.UserStatusActive,
		LoginAttempts: 4,
		LockedUntil:   &lockedUntil,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"INSERT INTO refresh_tokens (user_id, token, revoked) VALUES (?, ?, ?)",
		user.ID,
		"password-reset-refresh",
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
		SessionID:   "password-reset-session",
		IsActive:    true,
	}
	if err := db.Create(&history).Error; err != nil {
		t.Fatal(err)
	}

	const newPassword = "New-Password-42!"
	if err := NewAdminUserService(db).ResetUserPassword(
		context.Background(),
		user.ID,
		newPassword,
	); err != nil {
		t.Fatalf("reset user password: %v", err)
	}

	var updatedUser models.User
	if err := db.First(&updatedUser, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updatedUser.PasswordResetAt == nil {
		t.Fatal("password reset timestamp was not recorded")
	}
	if updatedUser.LoginAttempts != 0 || updatedUser.LockedUntil != nil {
		t.Fatalf("login lock state was not cleared: attempts=%d locked_until=%v", updatedUser.LoginAttempts, updatedUser.LockedUntil)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(updatedUser.PasswordHash), []byte(newPassword)); err != nil {
		t.Fatalf("new password hash does not verify: %v", err)
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
	if history.IsActive || history.LogoutTime == nil ||
		history.LoginStatus != models.LoginStatusExpired ||
		history.FailureReason != "password_reset" {
		t.Fatalf("login session was not closed: %+v", history)
	}
}

func TestResetUserPasswordRollsBackWhenSessionRevocationFails(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.LoginHistory{}); err != nil {
		t.Fatalf("migrate password reset schemas: %v", err)
	}

	user := models.User{
		Username:     "password-reset-rollback",
		Email:        "password-reset-rollback@example.com",
		PasswordHash: "original-password-hash",
		Role:         models.RoleAgent,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	err := NewAdminUserService(db).ResetUserPassword(
		context.Background(),
		user.ID,
		"New-Password-42!",
	)
	if err == nil || !strings.Contains(err.Error(), "failed to revoke user refresh tokens") {
		t.Fatalf("reset without refresh token table error = %v", err)
	}

	var unchangedUser models.User
	if err := db.First(&unchangedUser, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unchangedUser.PasswordHash != "original-password-hash" || unchangedUser.PasswordResetAt != nil {
		t.Fatalf("password update was not rolled back: hash=%q reset_at=%v", unchangedUser.PasswordHash, unchangedUser.PasswordResetAt)
	}
}
