package services

import (
	"context"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestAdminUserListUsesAllowlistedOrderColumns(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}
	older := models.User{
		Username:     "older-user",
		Email:        "older@example.com",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
		CreatedAt:    time.Now().Add(-time.Hour),
	}
	newer := models.User{
		Username:     "newer-user",
		Email:        "newer@example.com",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
		CreatedAt:    time.Now(),
	}
	if err := db.Create(&older).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&newer).Error; err != nil {
		t.Fatal(err)
	}

	result, err := NewAdminUserService(db).GetUserList(
		context.Background(),
		&UserListRequest{
			Page:     1,
			PageSize: 20,
			OrderBy:  "created_at; DROP TABLE users",
			Order:    "asc; DROP TABLE users",
		},
	)
	if err != nil {
		t.Fatalf("GetUserList() error = %v", err)
	}
	if len(result.Items) != 2 || result.Items[0].ID != newer.ID {
		t.Fatalf("fallback order IDs = %#v, want newest first", result.Items)
	}

	var count int64
	if err := db.Model(&models.User{}).Count(&count).Error; err != nil {
		t.Fatalf("users table was damaged by order input: %v", err)
	}
	if count != 2 {
		t.Fatalf("user count = %d, want 2", count)
	}
}
