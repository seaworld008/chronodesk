package auth

import (
	"context"
	"testing"

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
		Role:         models.RoleCustomer,
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
