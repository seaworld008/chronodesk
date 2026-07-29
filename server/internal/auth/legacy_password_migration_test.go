package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"gongdan-system/internal/models"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUpgradeVerifiedLegacySHA256Password(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:legacy-password-upgrade?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&RefreshToken{},
		&models.LoginHistory{},
	); err != nil {
		t.Fatal(err)
	}

	legacyPassword := "KnownLegacy!2026"
	digest := sha256.Sum256([]byte(legacyPassword))
	user := models.User{
		Username:     "legacy-admin",
		Email:        "legacy-admin@example.com",
		PasswordHash: hex.EncodeToString(digest[:]),
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	token := RefreshToken{
		UserID:    user.ID,
		Token:     "legacy-refresh-token",
		SessionID: "legacy-session",
		Revoked:   false,
	}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	history := models.LoginHistory{
		UserID:    user.ID,
		SessionID: "legacy-session",
		IsActive:  true,
	}
	if err := db.Create(&history).Error; err != nil {
		t.Fatal(err)
	}

	upgraded, err := UpgradeVerifiedLegacySHA256Password(
		context.Background(),
		db,
		user.Email,
		"wrong-password",
		"Replacement!2026",
	)
	if !errors.Is(err, ErrLegacyPasswordProofInvalid) || upgraded {
		t.Fatalf("wrong proof result = (%v, %v)", upgraded, err)
	}

	upgraded, err = UpgradeVerifiedLegacySHA256Password(
		context.Background(),
		db,
		user.Email,
		legacyPassword,
		"Replacement!2026",
	)
	if err != nil || !upgraded {
		t.Fatalf("upgrade result = (%v, %v)", upgraded, err)
	}
	if err := db.First(&user, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := NewSimplePasswordService(8, "").VerifyPassword(
		user.PasswordHash,
		"Replacement!2026",
	); err != nil {
		t.Fatalf("verify replacement password: %v", err)
	}
	if err := db.First(&token, token.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !token.Revoked {
		t.Fatal("expected refresh token to be revoked")
	}
	if err := db.First(&history, history.ID).Error; err != nil {
		t.Fatal(err)
	}
	if history.IsActive || history.LogoutTime == nil {
		t.Fatal("expected login history to be closed")
	}

	upgraded, err = UpgradeVerifiedLegacySHA256Password(
		context.Background(),
		db,
		user.Email,
		legacyPassword,
		"Replacement!2026",
	)
	if err != nil || upgraded {
		t.Fatalf("idempotent upgrade result = (%v, %v)", upgraded, err)
	}
}

func TestQuarantineUnsupportedPasswordHashes(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:legacy-password-quarantine?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&RefreshToken{},
		&models.LoginHistory{},
	); err != nil {
		t.Fatal(err)
	}
	active := models.User{
		Username:     "legacy-active",
		Email:        "legacy-active@example.com",
		PasswordHash: "unsupported-digest",
		Role:         models.RoleCustomer,
		Status:       models.UserStatusActive,
	}
	inactive := models.User{
		Username:     "legacy-inactive",
		Email:        "legacy-inactive@example.com",
		PasswordHash: "another-unsupported-digest",
		Role:         models.RoleCustomer,
		Status:       models.UserStatusInactive,
	}
	if err := db.Create(&active).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&inactive).Error; err != nil {
		t.Fatal(err)
	}

	report, err := QuarantineUnsupportedPasswordHashes(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if report.Quarantined != 2 ||
		report.ActiveSuspended != 1 ||
		report.InactiveSanitized != 1 {
		t.Fatalf("quarantine report = %+v", report)
	}
	for _, userID := range []uint{active.ID, inactive.ID} {
		var user models.User
		if err := db.Unscoped().First(&user, userID).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := bcrypt.Cost([]byte(user.PasswordHash)); err != nil {
			t.Fatalf("user %d password is not bcrypt: %v", userID, err)
		}
		if user.PasswordResetAt == nil {
			t.Fatalf("user %d missing password reset timestamp", userID)
		}
		if userID == active.ID && user.Status != models.UserStatusSuspended {
			t.Fatalf("active legacy account status = %s", user.Status)
		}
	}

	second, err := QuarantineUnsupportedPasswordHashes(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if second.Quarantined != 0 {
		t.Fatalf("second quarantine report = %+v", second)
	}
}
