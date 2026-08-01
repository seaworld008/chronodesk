package services

import (
	"context"
	"errors"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNotificationPreferencesAreClosedAndBounded(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:notification_preferences?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.NotificationPreference{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "preference-owner",
		Email:        "preference-owner@example.test",
		Status:       models.UserStatusActive,
		PlatformRole: models.PlatformRoleMember,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	service := NewNotificationServiceWithProtector(db, nil)
	allowed := models.NotificationTypes()
	preferences := make([]models.NotificationPreference, 0, len(allowed))
	for _, notificationType := range allowed {
		preferences = append(preferences, models.NotificationPreference{
			NotificationType: notificationType,
			InAppEnabled:     true,
			MaxDailyCount:    50,
			BatchInterval:    60,
		})
	}
	if err := service.UpdateNotificationPreferences(
		context.Background(),
		user.ID,
		preferences,
	); err != nil {
		t.Fatal(err)
	}
	result, err := service.GetNotificationPreferences(
		context.Background(),
		user.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != len(allowed) {
		t.Fatalf("preference count = %d, want %d", len(result), len(allowed))
	}
	for index := 1; index < len(result); index++ {
		if result[index-1].NotificationType > result[index].NotificationType {
			t.Fatalf("preferences are not stably ordered: %+v", result)
		}
	}

	tooMany := append(
		append([]models.NotificationPreference(nil), preferences...),
		models.NotificationPreference{
			NotificationType: models.NotificationType("future_type"),
		},
	)
	if err := service.UpdateNotificationPreferences(
		context.Background(),
		user.ID,
		tooMany,
	); !errors.Is(err, ErrInvalidNotificationPreferences) {
		t.Fatalf("oversized preference error = %v", err)
	}
	duplicate := []models.NotificationPreference{
		{NotificationType: models.NotificationTypeTicketAssigned},
		{NotificationType: models.NotificationTypeTicketAssigned},
	}
	if err := service.UpdateNotificationPreferences(
		context.Background(),
		user.ID,
		duplicate,
	); !errors.Is(err, ErrInvalidNotificationPreferences) {
		t.Fatalf("duplicate preference error = %v", err)
	}
}
