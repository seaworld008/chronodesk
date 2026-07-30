package services

import (
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestDeleteNotification(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.Notification{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	user := models.User{
		Username:     "e2e",
		Email:        "e2e@example.com",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	notification := models.Notification{
		OrganizationID: 1,
		ProjectID:      1,
		Type:           models.NotificationTypeSystemAlert,
		Title:          "E2E-通知删除",
		Content:        "E2E-通知删除",
		Priority:       models.NotificationPriorityNormal,
		Channel:        models.NotificationChannelInApp,
		RecipientID:    user.ID,
	}
	if err := db.Create(&notification).Error; err != nil {
		t.Fatalf("create notification: %v", err)
	}

	service := NewNotificationServiceWithProtector(db, nil)
	ctx := notificationTestOperationContext(
		t,
		models.ProjectScope{OrganizationID: 1, ProjectID: 1},
		models.HumanActor(user.ID),
	)
	if err := service.DeleteNotification(ctx, notification.ID); err != nil {
		t.Fatalf("delete notification: %v", err)
	}

	var count int64
	if err := db.Model(&models.Notification{}).Where("id = ?", notification.ID).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected notification deleted")
	}
}
