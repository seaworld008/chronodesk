package auth

import (
	"context"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestQuarantineUnsupportedPasswordHashes(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:unsupported-password-quarantine?mode=memory&cache=shared"),
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
		Username:     "unsupported-active",
		Email:        "unsupported-active@example.com",
		PasswordHash: "unsupported-digest",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	inactive := models.User{
		Username:     "unsupported-inactive",
		Email:        "unsupported-inactive@example.com",
		PasswordHash: "another-unsupported-digest",
		PlatformRole: models.PlatformRoleMember,
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
			t.Fatalf("active unsupported account status = %s", user.Status)
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
