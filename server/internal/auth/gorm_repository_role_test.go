package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGormUserRepositoryRejectsRolesOutsideClosedPlatformEnum(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:auth-closed-human-roles?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}
	repository := NewGormUserRepository(db).(*GormUserRepository)

	for _, role := range []PlatformRole{
		PlatformRole("admin"),
		PlatformRole("supervisor"),
		PlatformRole("agent"),
		PlatformRole("customer"),
		PlatformRole("unknown"),
	} {
		user := &User{
			ID:           1,
			Username:     "invalid-role",
			Email:        "invalid-role@example.test",
			PasswordHash: "hash",
			PlatformRole: role,
			Status:       StatusActive,
		}
		if err := repository.Create(context.Background(), user); err == nil ||
			!strings.Contains(err.Error(), "invalid platform role") {
			t.Errorf("Create role %q error = %v, want invalid platform role", role, err)
		}
		if err := repository.Update(context.Background(), user); err == nil ||
			!strings.Contains(err.Error(), "invalid platform role") {
			t.Errorf("Update role %q error = %v, want invalid platform role", role, err)
		}
	}
}
