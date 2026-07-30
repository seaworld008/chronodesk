package services

import (
	"context"
	"fmt"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestAnalyticsUserStatsCoversClosedPlatformRoleSet(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.LoginHistory{}); err != nil {
		t.Fatal(err)
	}
	roles := []models.PlatformRole{
		models.PlatformRolePlatformAdmin,
		models.PlatformRoleSecurityAuditor,
		models.PlatformRoleEmergencyOperator,
		models.PlatformRoleMember,
	}
	for index, role := range roles {
		user := models.User{
			Username:     fmt.Sprintf("analytics-role-%d", index),
			Email:        fmt.Sprintf("analytics-role-%d@example.test", index),
			PasswordHash: "hash",
			PlatformRole: role,
			Status:       models.UserStatusActive,
		}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
	}

	stats, err := NewAnalyticsService(db).getPlatformUserStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 4 ||
		stats.PlatformAdmins != 1 ||
		stats.SecurityAuditors != 1 ||
		stats.EmergencyOperators != 1 ||
		stats.Members != 1 {
		t.Fatalf("unexpected user role stats: %+v", stats)
	}
}
