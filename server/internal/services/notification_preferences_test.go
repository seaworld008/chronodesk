package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNotificationPreferenceOwnerLockEmitsPostgresForUpdate(t *testing.T) {
	db, err := gorm.Open(
		postgres.Open(
			"host=127.0.0.1 user=contract dbname=contract sslmode=disable",
		),
		&gorm.Config{
			DryRun:               true,
			DisableAutomaticPing: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var user struct {
		ID uint
	}
	statement := notificationPreferenceUserLockQuery(
		db,
		42,
	).Take(&user).Statement
	sql := strings.Join(strings.Fields(statement.SQL.String()), " ")
	if !strings.Contains(sql, `FROM "users"`) ||
		!strings.Contains(sql, `WHERE id = $1`) ||
		!strings.HasSuffix(sql, "FOR UPDATE") {
		t.Fatalf("PostgreSQL notification preference lock SQL = %q", sql)
	}
}

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
	for index, notificationType := range allowed {
		if result[index].NotificationType != notificationType {
			t.Fatalf(
				"preference %d type = %q, want canonical %q",
				index,
				result[index].NotificationType,
				notificationType,
			)
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

	now := time.Now().UTC()
	invalidPreferences := []models.NotificationPreference{
		{
			NotificationType: models.NotificationTypeTicketAssigned,
			MaxDailyCount:    -1,
			BatchInterval:    60,
		},
		{
			NotificationType: models.NotificationTypeTicketAssigned,
			MaxDailyCount:    50,
			BatchInterval:    60,
			WebhookEnabled:   true,
		},
		{
			NotificationType: models.NotificationTypeTicketAssigned,
			MaxDailyCount:    50,
			BatchInterval:    60,
			BatchDelivery:    true,
		},
		{
			NotificationType: models.NotificationTypeTicketAssigned,
			MaxDailyCount:    50,
			BatchInterval:    15,
		},
		{
			NotificationType:  models.NotificationTypeTicketAssigned,
			MaxDailyCount:     50,
			BatchInterval:     60,
			DoNotDisturbStart: &now,
		},
		{
			NotificationType:  models.NotificationTypeTicketAssigned,
			MaxDailyCount:     50,
			BatchInterval:     60,
			DoNotDisturbStart: timePointer(now.Add(time.Hour)),
			DoNotDisturbEnd:   timePointer(now),
		},
	}
	for index, preference := range invalidPreferences {
		if err := service.UpdateNotificationPreferences(
			context.Background(),
			user.ID,
			[]models.NotificationPreference{preference},
		); !errors.Is(err, ErrInvalidNotificationPreferences) {
			t.Fatalf(
				"invalid preference %d error = %v",
				index,
				err,
			)
		}
	}
}

func TestNotificationPreferencesProjectCanonicalDefaultsWithoutMutatingStorage(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open(
			"file:notification_preference_defaults?mode=memory&cache=shared",
		),
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
		Username:     "preference-default-owner",
		Email:        "preference-default-owner@example.test",
		PasswordHash: "test-only",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.NotificationPreference{}).Create(
		map[string]any{
			"user_id":           user.ID,
			"notification_type": models.NotificationTypeTicketAssigned,
			"email_enabled":     true,
			"in_app_enabled":    true,
			"webhook_enabled":   true,
			"max_daily_count":   25,
			"batch_delivery":    true,
			"batch_interval":    15,
		},
	).Error; err != nil {
		t.Fatal(err)
	}

	service := NewNotificationServiceWithProtector(db, nil)
	preferences, err := service.GetNotificationPreferences(
		context.Background(),
		user.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(preferences) != len(models.NotificationTypes()) {
		t.Fatalf(
			"materialized preferences = %d, want %d",
			len(preferences),
			len(models.NotificationTypes()),
		)
	}
	for _, preference := range preferences {
		if preference.WebhookEnabled ||
			preference.BatchDelivery ||
			preference.BatchInterval != 60 {
			t.Fatalf("non-canonical preference = %+v", preference)
		}
		if preference.NotificationType ==
			models.NotificationTypeTicketAssigned {
			if preference.MaxDailyCount != 25 {
				t.Fatalf("legacy supported setting changed: %+v", preference)
			}
			continue
		}
		if !preference.EmailEnabled ||
			!preference.InAppEnabled ||
			preference.MaxDailyCount != 50 {
			t.Fatalf("default preference = %+v", preference)
		}
	}

	var persisted models.NotificationPreference
	if err := db.Where(
		"user_id = ? AND notification_type = ?",
		user.ID,
		models.NotificationTypeTicketAssigned,
	).First(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if !persisted.WebhookEnabled ||
		!persisted.BatchDelivery ||
		persisted.BatchInterval != 15 {
		t.Fatalf("safe GET mutated legacy storage: %+v", persisted)
	}
	var persistedCount int64
	if err := db.Model(&models.NotificationPreference{}).
		Where("user_id = ?", user.ID).
		Count(&persistedCount).Error; err != nil {
		t.Fatal(err)
	}
	if persistedCount != 1 {
		t.Fatalf(
			"safe GET changed preference row count = %d, want 1",
			persistedCount,
		)
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}
