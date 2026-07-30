package services

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type recordingNotificationEmailAttempt struct {
	mu    sync.Mutex
	calls int
}

func (s *recordingNotificationEmailAttempt) SendEmailNotificationOutboxAttempt(
	_ context.Context,
	_ models.ProjectScope,
	_ *models.Notification,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return nil
}

func (s *recordingNotificationEmailAttempt) GetEmailTemplate(
	models.NotificationType,
) (*EmailTemplate, error) {
	return nil, nil
}

func (s *recordingNotificationEmailAttempt) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func newNotificationEmailOutboxTestService(
	t *testing.T,
) (*gorm.DB, *NotificationService, *recordingNotificationEmailAttempt, models.User, context.Context) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(
			"file:"+strings.ReplaceAll(t.Name(), "/", "-")+
				"?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Notification{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "notification-email-user",
		Email:        "notification-email@example.test",
		PasswordHash: "test-password-hash",
		Role:         models.RoleCustomer,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	scope := seedNotificationProjectMembership(t, db, user.ID)
	attempt := &recordingNotificationEmailAttempt{}
	service := NewNotificationServiceWithProtector(db, nil)
	service.SetEmailNotificationService(attempt)
	return db, service, attempt, user, notificationTestOperationContext(
		t,
		scope,
		models.SystemActor("notification-service"),
	)
}

func TestCreateEmailNotificationCommitsOnlyDurableOutboxIntent(t *testing.T) {
	db, service, attempt, user, ctx := newNotificationEmailOutboxTestService(t)
	notification, err := service.CreateNotification(
		ctx,
		&models.NotificationCreateRequest{
			Type:        models.NotificationTypeSystemAlert,
			Title:       "需要持久投递",
			Content:     "邮件正文留在通知记录中",
			Channel:     models.NotificationChannelEmail,
			RecipientID: user.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.callCount() != 0 {
		t.Fatal("notification creation performed an SMTP attempt")
	}

	var event models.DomainEvent
	if err := db.First(&event).Error; err != nil {
		t.Fatal(err)
	}
	var delivery models.OutboxDelivery
	if err := db.First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.EventID != event.ID ||
		delivery.EventID != NotificationEmailEventID(notification.ID) ||
		delivery.DestinationType != EmailOutboxDestination ||
		delivery.DestinationID != NotificationEmailDestinationPrefix+
			strconv.FormatUint(uint64(notification.ID), 10) {
		t.Fatalf("unexpected email notification Outbox: %+v", delivery)
	}
	if strings.Contains(string(event.Data), notification.Content) ||
		strings.Contains(string(event.Data), user.Email) {
		t.Fatal("email notification event copied recipient or message content")
	}
}

func TestCreateEmailNotificationRollsBackWhenOutboxInsertFails(t *testing.T) {
	db, service, attempt, user, ctx := newNotificationEmailOutboxTestService(t)
	const callbackName = "fail-notification-email-outbox"
	if err := db.Callback().Create().
		Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement != nil &&
				tx.Statement.Schema != nil &&
				tx.Statement.Schema.Table == "outbox_deliveries" {
				tx.AddError(errors.New("injected Outbox failure"))
			}
		}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Callback().Create().Remove(callbackName)
	}()

	if _, err := service.CreateNotification(
		ctx,
		&models.NotificationCreateRequest{
			Type:        models.NotificationTypeSystemAlert,
			Title:       "必须回滚",
			Content:     "不得留下孤立通知",
			Channel:     models.NotificationChannelEmail,
			RecipientID: user.ID,
		},
	); err == nil {
		t.Fatal("expected Outbox insert failure")
	}
	if attempt.callCount() != 0 {
		t.Fatal("failed transaction performed an SMTP attempt")
	}
	for name, model := range map[string]any{
		"notifications":     &models.Notification{},
		"domain_events":     &models.DomainEvent{},
		"outbox_deliveries": &models.OutboxDelivery{},
	} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s count after rollback = %d, want 0", name, count)
		}
	}
}
