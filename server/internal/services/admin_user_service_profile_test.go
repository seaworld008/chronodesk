package services

import (
	"context"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestAdminUserCreatePersistsInitialProfileProjection(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.UserProfile{}); err != nil {
		t.Fatal(err)
	}
	service := NewAdminUserService(db)
	user, err := service.CreateUser(context.Background(), &models.UserCreateRequest{
		Username:     "profile-first-save",
		Email:        "profile-first-save@example.test",
		Phone:        "+8613800138000",
		Password:     "StrongPassword123!",
		FirstName:    "首次",
		LastName:     "保存",
		PlatformRole: models.PlatformRoleMember,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	var profile models.UserProfile
	if err := db.Where("user_id = ?", user.ID).First(&profile).Error; err != nil {
		t.Fatalf("load initial profile: %v", err)
	}
	if profile.Phone != user.Phone ||
		profile.Avatar != user.Avatar ||
		profile.Timezone != user.Timezone ||
		profile.Language != user.Language {
		t.Fatalf("initial profile mismatch: user=%+v profile=%+v", user, profile)
	}
}
