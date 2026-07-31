package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupProfileRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}

	if err := db.AutoMigrate(&models.User{}, &models.UserProfile{}); err != nil {
		t.Fatalf("failed to migrate sqlite schema: %v", err)
	}

	return db
}

func createProfileRepoTestUser(t *testing.T, db *gorm.DB) models.User {
	t.Helper()

	user := models.User{
		Username:     "profile_repo_user",
		Email:        "profile_repo_user@example.com",
		PasswordHash: "$2a$10$7EqJtq98hPqEX7fNZaFWoOPKfN6obU6fY9w7NwQDJ5D6LzA6gW6Ga",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}

	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	return user
}

func TestGormProfileRepository_CreateAndGetByUserID(t *testing.T) {
	db := setupProfileRepoTestDB(t)
	user := createProfileRepoTestUser(t, db)
	repo := NewGormProfileRepository(db)

	ctx := context.Background()
	profile := &UserProfile{
		UserID:      user.ID,
		FirstName:   "Smoke",
		LastName:    "Tester",
		DisplayName: "Smoke Tester",
		Phone:       "1234567890",
		Department:  "QA",
		Position:    "Engineer",
		Timezone:    "UTC",
		Language:    "en",
	}

	if err := repo.Create(ctx, profile); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if profile.ID == 0 {
		t.Fatalf("expected profile ID to be set after create")
	}

	got, err := repo.GetByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByUserID returned error: %v", err)
	}

	if got.FirstName != "Smoke" || got.LastName != "Tester" {
		t.Fatalf("expected synced name fields, got first=%q last=%q", got.FirstName, got.LastName)
	}
	if got.Department != "QA" || got.Position != "Engineer" {
		t.Fatalf("expected synced org fields, got department=%q position=%q", got.Department, got.Position)
	}
	if got.Timezone != "UTC" || got.Language != "en" {
		t.Fatalf("expected profile timezone/language to persist, got timezone=%q language=%q", got.Timezone, got.Language)
	}
}

func TestGormProfileRepository_UpdateSyncsUserFields(t *testing.T) {
	db := setupProfileRepoTestDB(t)
	user := createProfileRepoTestUser(t, db)
	repo := NewGormProfileRepository(db)

	ctx := context.Background()
	profile := &UserProfile{
		UserID:      user.ID,
		FirstName:   "Before",
		LastName:    "User",
		DisplayName: "Before User",
		Department:  "Support",
		Position:    "Agent",
		Timezone:    "UTC",
		Language:    "en",
	}

	if err := repo.Create(ctx, profile); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	profile.FirstName = "After"
	profile.LastName = "Editor"
	profile.DisplayName = "After Editor"
	profile.Department = "Engineering"
	profile.Position = "Senior Engineer"
	profile.Phone = "18800001111"
	profile.Timezone = "Asia/Shanghai"
	profile.Language = "zh-CN"

	if err := repo.Update(ctx, profile); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	got, err := repo.GetByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByUserID returned error: %v", err)
	}

	if got.FirstName != "After" || got.LastName != "Editor" {
		t.Fatalf("expected updated names, got first=%q last=%q", got.FirstName, got.LastName)
	}
	if got.Department != "Engineering" || got.Position != "Senior Engineer" {
		t.Fatalf("expected updated org info, got department=%q position=%q", got.Department, got.Position)
	}
	if got.Phone != "18800001111" {
		t.Fatalf("expected updated phone, got %q", got.Phone)
	}
}

func TestGormProfileRepositoryBackfillsLegacyUserWithoutDroppingFields(t *testing.T) {
	db := setupProfileRepoTestDB(t)
	user := createProfileRepoTestUser(t, db)
	if err := db.Model(&user).Updates(map[string]any{
		"first_name":     "Legacy",
		"last_name":      "User",
		"avatar":         "/uploads/avatars/1/00000000-0000-4000-8000-000000000001.png",
		"phone":          "+8613800138000",
		"timezone":       "Asia/Tokyo",
		"language":       "zh-CN",
		"phone_verified": true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	repo := NewGormProfileRepository(db)
	profile, err := repo.GetByUserID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetByUserID legacy backfill: %v", err)
	}
	if profile.FirstName != "Legacy" ||
		profile.LastName != "User" ||
		profile.Phone != "+8613800138000" ||
		profile.Timezone != "Asia/Tokyo" ||
		profile.Language != "zh-CN" {
		t.Fatalf("legacy profile projection lost fields: %+v", profile)
	}

	var count int64
	if err := db.Model(&models.UserProfile{}).
		Where("user_id = ?", user.ID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("profile backfill rows = %d, want 1", count)
	}
	if _, err := repo.GetByUserID(context.Background(), user.ID); err != nil {
		t.Fatalf("idempotent profile read: %v", err)
	}
	if err := db.Model(&models.UserProfile{}).
		Where("user_id = ?", user.ID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("idempotent profile rows = %d, want 1", count)
	}
}

func TestAuthProfileUpdateValidatesAndClearsPhoneVerification(t *testing.T) {
	db := setupProfileRepoTestDB(t)
	user := createProfileRepoTestUser(t, db)
	if err := db.Model(&user).Updates(map[string]any{
		"phone":             "+8613800138000",
		"phone_verified":    true,
		"phone_verified_at": time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewGormProfileRepository(db)
	service := &AuthService{profileRepo: repo}
	phone := "+8613900139000"
	timezone := "Asia/Tokyo"
	language := "zh-CN"
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		PhoneNumber: &phone,
		Timezone:    &timezone,
		Language:    &language,
	}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	var persisted models.User
	if err := db.First(&persisted, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Phone != phone || persisted.PhoneVerified ||
		persisted.PhoneVerifiedAt != nil {
		t.Fatalf("phone verification projection = %+v", persisted)
	}

	invalidZone := "Mars/Olympus"
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		Timezone: &invalidZone,
	}); !errors.Is(err, ErrInvalidProfileZone) {
		t.Fatalf("invalid timezone error = %v", err)
	}
	english := "en"
	firstName := "English"
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		FirstName: &firstName,
		Language:  &english,
	}); err != nil {
		t.Fatalf("existing en roundtrip: %v", err)
	}
	roundTripped, err := repo.GetByUserID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if roundTripped.Language != english || roundTripped.FirstName != firstName {
		t.Fatalf("existing en roundtrip profile = %+v", roundTripped)
	}

	unsupported := "fr"
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		Language: &unsupported,
	}); !errors.Is(err, ErrInvalidProfileLocale) {
		t.Fatalf("unsupported language error = %v", err)
	}
}

func TestAuthProfileAvatarCompatibilityCannotForgeUploadResult(t *testing.T) {
	db := setupProfileRepoTestDB(t)
	user := createProfileRepoTestUser(t, db)
	legacyAvatar := "https://legacy.example.test/avatar.png"
	if err := db.Model(&user).Update("avatar", legacyAvatar).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewGormProfileRepository(db)
	service := &AuthService{profileRepo: repo}
	firstName := "Preserved"
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		FirstName: &firstName,
		Avatar:    &legacyAvatar,
	}); err != nil {
		t.Fatalf("legacy avatar exact no-op: %v", err)
	}

	forged := fmt.Sprintf(
		"/uploads/avatars/%d/00000000-0000-4000-8000-000000000001.png",
		user.ID,
	)
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		Avatar: &forged,
	}); !errors.Is(err, ErrInvalidProfileAvatar) {
		t.Fatalf("forged uploaded path error = %v", err)
	}

	clear := ""
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		Avatar: &clear,
	}); err != nil {
		t.Fatalf("clear avatar: %v", err)
	}
	profile, err := repo.GetByUserID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Avatar != "" {
		t.Fatalf("cleared avatar = %q", profile.Avatar)
	}
}
