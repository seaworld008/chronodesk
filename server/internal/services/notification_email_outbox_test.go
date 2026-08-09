package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type recordingNotificationEmailAttempt struct {
	mu    sync.Mutex
	calls int
}

var notificationEmailOutboxFixtureSequence atomic.Uint64

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
	dsn := fmt.Sprintf(
		"file:notification-email-%d-%d?mode=memory&cache=shared",
		time.Now().UnixNano(),
		notificationEmailOutboxFixtureSequence.Add(1),
	)
	db, err := gorm.Open(
		sqlite.Open(dsn),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.Notification{},
		&models.NotificationPreference{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "notification-email-user",
		Email:        "notification-email@example.test",
		PasswordHash: "test-password-hash",
		PlatformRole: models.PlatformRoleMember,
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

func TestCreateEmailNotificationHonorsTypeSpecificPreferenceBeforeQueuing(
	t *testing.T,
) {
	tests := []struct {
		name           string
		preference     *bool
		scheduledAt    *time.Time
		wantSuppressed bool
	}{
		{
			name:           "disabled is suppressed without delivery intent",
			preference:     notificationBoolPointer(false),
			wantSuppressed: true,
		},
		{
			name:       "missing preference queues one delivery intent",
			preference: nil,
		},
		{
			name:       "enabled preference queues one delivery intent",
			preference: notificationBoolPointer(true),
		},
		{
			name:        "enabled scheduled preference preserves availability",
			preference:  notificationBoolPointer(true),
			scheduledAt: notificationTimePointer(time.Now().UTC().Add(time.Hour)),
		},
		{
			name:           "disabled scheduled preference is immediately suppressed",
			preference:     notificationBoolPointer(false),
			scheduledAt:    notificationTimePointer(time.Now().UTC().Add(time.Hour)),
			wantSuppressed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, service, attempt, user, ctx :=
				newNotificationEmailOutboxTestService(t)
			if test.preference != nil {
				if err := service.UpdateNotificationPreferences(
					context.Background(),
					user.ID,
					[]models.NotificationPreference{{
						NotificationType: models.NotificationTypeSystemAlert,
						EmailEnabled:     *test.preference,
						InAppEnabled:     true,
						MaxDailyCount:    50,
						BatchInterval:    60,
					}},
				); err != nil {
					t.Fatal(err)
				}
			}

			beforeCreate := time.Now().UTC()
			notification, err := service.CreateNotification(
				ctx,
				&models.NotificationCreateRequest{
					Type:        models.NotificationTypeSystemAlert,
					Title:       "邮件偏好裁决",
					Content:     "必须先于 Outbox 写入生效",
					Channel:     models.NotificationChannelEmail,
					RecipientID: user.ID,
					ScheduledAt: test.scheduledAt,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			afterCreate := time.Now().UTC()
			if attempt.callCount() != 0 {
				t.Fatal("notification creation performed an SMTP attempt")
			}

			var notificationCount, eventCount, deliveryCount int64
			for name, model := range map[string]any{
				"notifications":     &models.Notification{},
				"domain_events":     &models.DomainEvent{},
				"outbox_deliveries": &models.OutboxDelivery{},
			} {
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				switch name {
				case "notifications":
					notificationCount = count
				case "domain_events":
					eventCount = count
				case "outbox_deliveries":
					deliveryCount = count
				}
			}
			if notificationCount != 1 {
				t.Fatalf("notification count = %d, want 1", notificationCount)
			}
			if test.wantSuppressed {
				if notification.DeliveryStatus !=
					NotificationDeliveryStatusSuppressedByPreference ||
					!notification.IsRead ||
					notification.ReadAt == nil ||
					notification.ExpiresAt == nil {
					t.Fatalf("suppressed email notification = %+v", notification)
				}
				var metadata map[string]any
				if err := json.Unmarshal([]byte(notification.Metadata), &metadata); err != nil {
					t.Fatal(err)
				}
				if metadata["preference_suppression"] != "email_disabled" {
					t.Fatalf("suppression metadata = %#v", metadata)
				}
				if notification.ReadAt.Location() != time.UTC ||
					notification.ExpiresAt.Location() != time.UTC ||
					!notification.ReadAt.Equal(*notification.ExpiresAt) ||
					notification.ReadAt.Before(beforeCreate) ||
					notification.ReadAt.After(afterCreate) {
					t.Fatalf(
						"suppression timestamps = read:%s expires:%s, want equal UTC values within [%s, %s]",
						notification.ReadAt,
						notification.ExpiresAt,
						beforeCreate,
						afterCreate,
					)
				}
				var persisted models.Notification
				if err := db.First(&persisted, notification.ID).Error; err != nil {
					t.Fatal(err)
				}
				if persisted.ReadAt == nil || persisted.ExpiresAt == nil ||
					persisted.ReadAt.Location() != time.UTC ||
					persisted.ExpiresAt.Location() != time.UTC ||
					!persisted.ReadAt.Equal(*persisted.ExpiresAt) ||
					persisted.ReadAt.Before(beforeCreate) ||
					persisted.ReadAt.After(afterCreate) {
					t.Fatalf(
						"persisted suppression timestamps = read:%s expires:%s, want equal UTC values within [%s, %s]",
						persisted.ReadAt,
						persisted.ExpiresAt,
						beforeCreate,
						afterCreate,
					)
				}
				if eventCount != 0 || deliveryCount != 0 {
					t.Fatalf(
						"suppressed delivery intents = events:%d outbox:%d, want 0/0",
						eventCount,
						deliveryCount,
					)
				}
				return
			}
			if notification.DeliveryStatus != "" || notification.IsRead ||
				notification.ReadAt != nil || notification.ExpiresAt != nil {
				t.Fatalf("enabled email notification was suppressed: %+v", notification)
			}
			if eventCount != 1 || deliveryCount != 1 {
				t.Fatalf(
					"email delivery intents = events:%d outbox:%d, want 1/1",
					eventCount,
					deliveryCount,
				)
			}
			if test.scheduledAt != nil {
				var delivery models.OutboxDelivery
				if err := db.First(&delivery).Error; err != nil {
					t.Fatal(err)
				}
				if !delivery.NextAttemptAt.Equal(*test.scheduledAt) {
					t.Fatalf(
						"scheduled Outbox availability = %s, want %s",
						delivery.NextAttemptAt,
						*test.scheduledAt,
					)
				}
			}
		})
	}
}

func TestCreateEmailNotificationDropsReservedPreferenceSuppressionMetadata(
	t *testing.T,
) {
	for _, test := range []struct {
		name       string
		preference *bool
	}{
		{name: "missing preference", preference: nil},
		{name: "enabled preference", preference: notificationBoolPointer(true)},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, service, _, user, ctx := newNotificationEmailOutboxTestService(t)
			if test.preference != nil {
				if err := service.UpdateNotificationPreferences(
					context.Background(),
					user.ID,
					[]models.NotificationPreference{{
						NotificationType: models.NotificationTypeSystemAlert,
						EmailEnabled:     *test.preference,
						InAppEnabled:     true,
						MaxDailyCount:    50,
						BatchInterval:    60,
					}},
				); err != nil {
					t.Fatal(err)
				}
			}

			notification, err := service.CreateNotification(
				ctx,
				&models.NotificationCreateRequest{
					Type:        models.NotificationTypeSystemAlert,
					Title:       "调用方元数据不得伪造抑制",
					Content:     "保留键只能由服务写入",
					Channel:     models.NotificationChannelEmail,
					RecipientID: user.ID,
					Metadata: map[string]any{
						"preference_suppression": "email_disabled",
						"caller_metadata":        "retained",
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if notification.DeliveryStatus != "" || notification.IsRead {
				t.Fatalf("enabled email notification was suppressed: %+v", notification)
			}
			assertNotificationMetadataDoesNotContain(
				t,
				notification.Metadata,
				"preference_suppression",
			)
			if _, found := notification.ToResponse().Metadata["preference_suppression"]; found {
				t.Fatal("response preserved caller-supplied preference suppression")
			}
			var persisted models.Notification
			if err := db.First(&persisted, notification.ID).Error; err != nil {
				t.Fatal(err)
			}
			assertNotificationMetadataDoesNotContain(
				t,
				persisted.Metadata,
				"preference_suppression",
			)
			for name, model := range map[string]any{
				"domain_events":     &models.DomainEvent{},
				"outbox_deliveries": &models.OutboxDelivery{},
			} {
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatalf("%s count = %d, want 1", name, count)
				}
			}
		})
	}
}

func TestExplicitEmailNotificationIgnoresInAppOnlyPreferences(t *testing.T) {
	for _, test := range []struct {
		name             string
		maxDailyCount    int
		seedInAppAtLimit bool
	}{
		{
			name:          "zero daily limit with active DND and batch",
			maxDailyCount: 0,
		},
		{
			name:             "daily limit already consumed with active DND and batch",
			maxDailyCount:    1,
			seedInAppAtLimit: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, service, _, user, ctx := newNotificationEmailOutboxTestService(t)
			now := time.Now().UTC()
			if err := db.Create(&models.NotificationPreference{
				UserID:            user.ID,
				NotificationType:  models.NotificationTypeSystemAlert,
				EmailEnabled:      true,
				InAppEnabled:      false,
				DoNotDisturbStart: notificationTimePointer(now.Add(-time.Hour)),
				DoNotDisturbEnd:   notificationTimePointer(now.Add(time.Hour)),
				MaxDailyCount:     test.maxDailyCount,
				BatchDelivery:     true,
				BatchInterval:     15,
				WebhookEnabled:    false,
			}).Error; err != nil {
				t.Fatal(err)
			}
			if test.seedInAppAtLimit {
				if err := db.Create(&models.Notification{
					OrganizationID: 1,
					ProjectID:      1,
					Type:           models.NotificationTypeSystemAlert,
					Title:          "应用内日限额夹具",
					Content:        "不应影响显式邮件",
					Priority:       models.NotificationPriorityNormal,
					Channel:        models.NotificationChannelInApp,
					RecipientID:    user.ID,
				}).Error; err != nil {
					t.Fatal(err)
				}
			}

			notification, err := service.CreateNotification(
				ctx,
				&models.NotificationCreateRequest{
					Type:        models.NotificationTypeSystemAlert,
					Title:       "邮件只服从邮件偏好",
					Content:     "应用内规则不可影响邮件 Outbox",
					Channel:     models.NotificationChannelEmail,
					RecipientID: user.ID,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if notification.DeliveryStatus != "" || notification.IsRead ||
				notification.ReadAt != nil || notification.ExpiresAt != nil {
				t.Fatalf("explicit email was suppressed: %+v", notification)
			}
			for name, model := range map[string]any{
				"domain_events":     &models.DomainEvent{},
				"outbox_deliveries": &models.OutboxDelivery{},
			} {
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatalf("%s count = %d, want 1", name, count)
				}
			}
		})
	}
}

func TestDisabledEmailPreferenceDoesNotSuppressInAppNotification(t *testing.T) {
	db, service, _, user, ctx := newNotificationEmailOutboxTestService(t)
	if err := service.UpdateNotificationPreferences(
		context.Background(),
		user.ID,
		[]models.NotificationPreference{{
			NotificationType: models.NotificationTypeSystemAlert,
			EmailEnabled:     false,
			InAppEnabled:     true,
			MaxDailyCount:    50,
			BatchInterval:    60,
		}},
	); err != nil {
		t.Fatal(err)
	}

	notification, err := service.CreateNotification(
		ctx,
		&models.NotificationCreateRequest{
			Type:        models.NotificationTypeSystemAlert,
			Title:       "应用内投递独立",
			Content:     "邮件禁用不能压制应用内通知",
			Channel:     models.NotificationChannelInApp,
			RecipientID: user.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if notification.DeliveryStatus != "" || notification.IsRead ||
		notification.ReadAt != nil || notification.ExpiresAt != nil {
		t.Fatalf("in-app notification was suppressed: %+v", notification)
	}
	for name, model := range map[string]any{
		"domain_events":     &models.DomainEvent{},
		"outbox_deliveries": &models.OutboxDelivery{},
	} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", name, count)
		}
	}
}

func notificationBoolPointer(value bool) *bool {
	return &value
}

func assertNotificationMetadataDoesNotContain(
	t *testing.T,
	raw string,
	key string,
) {
	t.Helper()
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		t.Fatal(err)
	}
	if _, found := metadata[key]; found {
		t.Fatalf("metadata retained reserved key %q: %#v", key, metadata)
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
