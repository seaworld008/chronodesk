package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestAdminUserServiceRejectsRolesOutsideClosedPlatformEnum(t *testing.T) {
	service := NewAdminUserService(openTestDB(t))
	invalidRole := models.PlatformRole("admin")

	if _, err := service.GetUserList(context.Background(), &UserListRequest{
		PlatformRole: &invalidRole,
	}); err == nil || !strings.Contains(err.Error(), "invalid platform role") {
		t.Errorf("GetUserList invalid role error = %v", err)
	}
	if _, err := service.CreateUser(context.Background(), &models.UserCreateRequest{
		PlatformRole: invalidRole,
	}); err == nil || !strings.Contains(err.Error(), "invalid platform role") {
		t.Errorf("CreateUser invalid role error = %v", err)
	}
	if _, err := service.UpdateUser(context.Background(), models.HumanActor(1), 1, &models.UserUpdateRequest{
		PlatformRole: &invalidRole,
	}); err == nil || !strings.Contains(err.Error(), "invalid platform role") {
		t.Errorf("UpdateUser invalid role error = %v", err)
	}
}

func TestAdminUserServiceUpdatePreservesLastActiveAdmin(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatalf("migrate users: %v", err)
	}
	admin := models.User{
		Username:     "last-admin",
		Email:        "last-admin@example.com",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}

	suspended := models.UserStatusSuspended
	service := NewAdminUserService(db)
	if _, err := service.UpdateUser(
		context.Background(),
		models.HumanActor(admin.ID),
		admin.ID,
		&models.UserUpdateRequest{Status: &suspended},
	); !errors.Is(err, ErrLastActivePlatformAdministrator) {
		t.Fatalf("last admin update error = %v", err)
	}

	var persisted models.User
	if err := db.First(&persisted, admin.ID).Error; err != nil {
		t.Fatalf("reload admin: %v", err)
	}
	if persisted.Status != models.UserStatusActive {
		t.Fatalf("last admin status = %q, want active", persisted.Status)
	}
}

func TestAdminUserServiceTreatsSoftDeletedIdentityAsConflict(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatalf("migrate users: %v", err)
	}
	deleted := models.User{
		Username:     "retained-identity",
		Email:        "retained-identity@example.com",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusDeleted,
	}
	if err := db.Create(&deleted).Error; err != nil {
		t.Fatalf("create retained user: %v", err)
	}
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatalf("soft delete retained user: %v", err)
	}

	service := NewAdminUserService(db)
	_, err := service.CreateUser(context.Background(), &models.UserCreateRequest{
		Username:     "new-identity",
		Email:        deleted.Email,
		Password:     "StrongPassword123!",
		PlatformRole: models.PlatformRoleMember,
	})
	if !errors.Is(err, ErrAdminUserIdentityConflict) {
		t.Fatalf("CreateUser error = %v, want identity conflict", err)
	}

	active := models.User{
		Username:     "active-identity",
		Email:        "active-identity@example.com",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&active).Error; err != nil {
		t.Fatalf("create active user: %v", err)
	}
	_, err = service.UpdateUser(
		context.Background(),
		models.HumanActor(active.ID),
		active.ID,
		&models.UserUpdateRequest{Email: &deleted.Email},
	)
	if !errors.Is(err, ErrAdminUserIdentityConflict) {
		t.Fatalf("UpdateUser error = %v, want identity conflict", err)
	}
}
