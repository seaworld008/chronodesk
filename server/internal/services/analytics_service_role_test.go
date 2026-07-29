package services

import (
	"context"
	"fmt"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestAnalyticsUserStatsCoversClosedHumanRoleSet(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.LoginHistory{}); err != nil {
		t.Fatal(err)
	}
	roles := []models.UserRole{
		models.RoleAdmin,
		models.RoleSupervisor,
		models.RoleAgent,
		models.RoleCustomer,
	}
	for index, role := range roles {
		user := models.User{
			Username:     fmt.Sprintf("analytics-role-%d", index),
			Email:        fmt.Sprintf("analytics-role-%d@example.test", index),
			PasswordHash: "hash",
			Role:         role,
			Status:       models.UserStatusActive,
		}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
	}

	stats, err := NewAnalyticsService(db).getUserStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 4 ||
		stats.Admins != 1 ||
		stats.Supervisors != 1 ||
		stats.Agents != 1 ||
		stats.Customers != 1 {
		t.Fatalf("unexpected user role stats: %+v", stats)
	}
}
