package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGormUserRepositoryRejectsRolesOutsideClosedHumanEnum(t *testing.T) {
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

	for _, role := range []UserRole{UserRole("user"), UserRole("superuser"), UserRole("unknown")} {
		user := &User{
			ID:           1,
			Username:     "invalid-role",
			Email:        "invalid-role@example.test",
			PasswordHash: "hash",
			Role:         role,
			Status:       StatusActive,
		}
		if err := repository.Create(context.Background(), user); err == nil ||
			!strings.Contains(err.Error(), "invalid human role") {
			t.Errorf("Create role %q error = %v, want invalid human role", role, err)
		}
		if err := repository.Update(context.Background(), user); err == nil ||
			!strings.Contains(err.Error(), "invalid human role") {
			t.Errorf("Update role %q error = %v, want invalid human role", role, err)
		}
	}
}
