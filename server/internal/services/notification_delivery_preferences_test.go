package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestTicketNotificationDeliveryEnforcesApplicationPreferences(t *testing.T) {
	tests := []struct {
		name            string
		preference      func(time.Time) models.NotificationPreference
		seedVisible     bool
		seedFuture      bool
		wantCreated     bool
		wantSuppression string
	}{
		{
			name: "application channel disabled",
			preference: func(time.Time) models.NotificationPreference {
				return models.NotificationPreference{
					InAppEnabled:  false,
					MaxDailyCount: 50,
					BatchInterval: 60,
				}
			},
			wantSuppression: "in_app_disabled",
		},
		{
			name: "absolute quiet interval active",
			preference: func(now time.Time) models.NotificationPreference {
				return models.NotificationPreference{
					InAppEnabled: true,
					DoNotDisturbStart: notificationTimePointer(
						now.Add(-time.Hour),
					),
					DoNotDisturbEnd: notificationTimePointer(
						now.Add(time.Hour),
					),
					MaxDailyCount: 50,
					BatchInterval: 60,
				}
			},
			wantSuppression: "do_not_disturb",
		},
		{
			name: "legacy invalid quiet interval fails closed without retry",
			preference: func(now time.Time) models.NotificationPreference {
				return models.NotificationPreference{
					InAppEnabled: true,
					DoNotDisturbStart: notificationTimePointer(
						now.Add(time.Hour),
					),
					DoNotDisturbEnd: notificationTimePointer(
						now.Add(-time.Hour),
					),
					MaxDailyCount: 50,
					BatchInterval: 60,
				}
			},
			wantSuppression: "invalid_preference",
		},
		{
			name: "UTC daily limit reached",
			preference: func(time.Time) models.NotificationPreference {
				return models.NotificationPreference{
					InAppEnabled:  true,
					MaxDailyCount: 1,
					BatchInterval: 60,
				}
			},
			seedVisible:     true,
			wantSuppression: "daily_limit",
		},
		{
			name: "next UTC day row does not consume current limit",
			preference: func(time.Time) models.NotificationPreference {
				return models.NotificationPreference{
					InAppEnabled:  true,
					MaxDailyCount: 1,
					BatchInterval: 60,
				}
			},
			seedFuture:  true,
			wantCreated: true,
		},
		{
			name: "legacy batch flag does not suppress delivery",
			preference: func(time.Time) models.NotificationPreference {
				return models.NotificationPreference{
					InAppEnabled:  true,
					MaxDailyCount: 50,
					BatchDelivery: true,
					BatchInterval: 15,
				}
			},
			wantCreated: true,
		},
		{
			name: "enabled below daily limit",
			preference: func(time.Time) models.NotificationPreference {
				return models.NotificationPreference{
					InAppEnabled:  true,
					MaxDailyCount: 1,
					BatchInterval: 60,
				}
			},
			wantCreated: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, service, recipient, ticket := newNotificationPreferenceFixture(t)
			now := time.Now().UTC()
			preference := test.preference(now)
			preference.UserID = recipient.ID
			preference.NotificationType =
				models.NotificationTypeTicketAssigned
			if err := db.Model(&models.NotificationPreference{}).Create(
				map[string]any{
					"user_id":              preference.UserID,
					"notification_type":    preference.NotificationType,
					"email_enabled":        preference.EmailEnabled,
					"in_app_enabled":       preference.InAppEnabled,
					"webhook_enabled":      preference.WebhookEnabled,
					"do_not_disturb_start": preference.DoNotDisturbStart,
					"do_not_disturb_end":   preference.DoNotDisturbEnd,
					"max_daily_count":      preference.MaxDailyCount,
					"batch_delivery":       preference.BatchDelivery,
					"batch_interval":       preference.BatchInterval,
				},
			).Error; err != nil {
				t.Fatal(err)
			}
			if test.seedVisible || test.seedFuture {
				createdAt := now.Add(-time.Minute)
				if test.seedFuture {
					dayStart := time.Date(
						now.Year(),
						now.Month(),
						now.Day(),
						0,
						0,
						0,
						0,
						time.UTC,
					)
					createdAt = dayStart.AddDate(0, 0, 1).Add(time.Hour)
				}
				if err := db.Create(&models.Notification{
					OrganizationID: ticket.OrganizationID,
					ProjectID:      ticket.ProjectID,
					Type:           models.NotificationTypeTicketAssigned,
					Title:          "already visible",
					Content:        "daily limit fixture",
					Priority:       models.NotificationPriorityNormal,
					Channel:        models.NotificationChannelInApp,
					RecipientID:    recipient.ID,
					CreatedAt:      createdAt,
				}).Error; err != nil {
					t.Fatal(err)
				}
			}

			event := notificationAssignmentEvent(
				t,
				"preference-"+test.name,
				recipient.ID,
				ticket,
			)
			destination := fmt.Sprintf(
				"%s:%d",
				models.NotificationTypeTicketAssigned,
				recipient.ID,
			)
			notification, created, err :=
				service.DeliverTicketNotificationOutbox(
					context.Background(),
					event,
					destination,
				)
			if err != nil {
				t.Fatal(err)
			}
			if created != test.wantCreated ||
				notification == nil ||
				notification.ID == 0 {
				t.Fatalf(
					"delivery = notification:%+v created:%v, want created:%v",
					notification,
					created,
					test.wantCreated,
				)
			}
			if test.wantCreated {
				if notification.DeliveryStatus != "" ||
					notification.IsRead {
					t.Fatalf(
						"visible notification was suppressed: %+v",
						notification,
					)
				}
				return
			}
			if notification.DeliveryStatus !=
				NotificationDeliveryStatusSuppressedByPreference ||
				!notification.IsRead ||
				notification.ReadAt == nil {
				t.Fatalf("suppressed notification state = %+v", notification)
			}
			var metadata map[string]any
			if err := json.Unmarshal(
				[]byte(notification.Metadata),
				&metadata,
			); err != nil {
				t.Fatal(err)
			}
			if metadata["preference_suppression"] != test.wantSuppression {
				t.Fatalf("suppression metadata = %#v", metadata)
			}

			replayed, replayCreated, err :=
				service.DeliverTicketNotificationOutbox(
					context.Background(),
					event,
					destination,
				)
			if err != nil ||
				replayCreated ||
				replayed == nil ||
				replayed.ID != notification.ID {
				t.Fatalf(
					"suppressed replay = notification:%+v created:%v error:%v",
					replayed,
					replayCreated,
					err,
				)
			}
		})
	}
}

func TestMissingPreferenceUsesTheCanonicalPerProjectDailyLimit(t *testing.T) {
	db, service, recipient, ticket := newNotificationPreferenceFixture(t)
	now := time.Now().UTC()
	existing := make([]models.Notification, 0, 50)
	for index := 0; index < 50; index++ {
		existing = append(existing, models.Notification{
			OrganizationID: ticket.OrganizationID,
			ProjectID:      ticket.ProjectID,
			Type:           models.NotificationTypeTicketAssigned,
			Title:          fmt.Sprintf("visible %d", index),
			Content:        "canonical default limit fixture",
			Priority:       models.NotificationPriorityNormal,
			Channel:        models.NotificationChannelInApp,
			RecipientID:    recipient.ID,
			CreatedAt:      now.Add(-time.Minute),
		})
	}
	if err := db.CreateInBatches(&existing, 25).Error; err != nil {
		t.Fatal(err)
	}

	event := notificationAssignmentEvent(
		t,
		"canonical-default-limit",
		recipient.ID,
		ticket,
	)
	notification, created, err := service.DeliverTicketNotificationOutbox(
		context.Background(),
		event,
		fmt.Sprintf(
			"%s:%d",
			models.NotificationTypeTicketAssigned,
			recipient.ID,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if created ||
		notification == nil ||
		notification.DeliveryStatus !=
			NotificationDeliveryStatusSuppressedByPreference {
		t.Fatalf(
			"missing-preference delivery = notification:%+v created:%v",
			notification,
			created,
		)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(notification.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["preference_suppression"] != "daily_limit" {
		t.Fatalf("missing-preference suppression metadata = %#v", metadata)
	}
}

func TestManualInAppNotificationUsesTheSamePreferenceGate(t *testing.T) {
	db, service, _, recipient, ctx :=
		newNotificationEmailOutboxTestService(t)
	if err := db.AutoMigrate(&models.NotificationPreference{}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateNotificationPreferences(
		context.Background(),
		recipient.ID,
		[]models.NotificationPreference{{
			NotificationType: models.NotificationTypeSystemAlert,
			EmailEnabled:     true,
			InAppEnabled:     false,
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
			Title:       "管理员通知",
			Content:     "必须服从接收者应用内偏好",
			Channel:     models.NotificationChannelInApp,
			RecipientID: recipient.ID,
			Metadata:    map[string]any{"source": "manual"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if notification.DeliveryStatus !=
		NotificationDeliveryStatusSuppressedByPreference ||
		!notification.IsRead ||
		notification.ReadAt == nil {
		t.Fatalf("manual suppressed notification = %+v", notification)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(notification.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["source"] != "manual" ||
		metadata["preference_suppression"] != "in_app_disabled" {
		t.Fatalf("manual suppression metadata = %#v", metadata)
	}

	filter := &models.NotificationFilter{
		RecipientID: &recipient.ID,
		Limit:       25,
		OrderBy:     "created_at",
		OrderDir:    "desc",
	}
	items, total, err := service.GetNotifications(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf(
			"suppressed manual notification leaked into list: total=%d items=%+v",
			total,
			items,
		)
	}
}

func TestManualNotificationRejectsChannelsWithoutDeliveryEngines(t *testing.T) {
	db, service, _, recipient, ctx :=
		newNotificationEmailOutboxTestService(t)
	for _, channel := range []models.NotificationChannel{
		models.NotificationChannelWebhook,
		models.NotificationChannelWebSocket,
	} {
		t.Run(string(channel), func(t *testing.T) {
			notification, err := service.CreateNotification(
				ctx,
				&models.NotificationCreateRequest{
					Type:        models.NotificationTypeSystemAlert,
					Title:       "unsupported channel",
					Content:     "must not persist",
					Channel:     channel,
					RecipientID: recipient.ID,
				},
			)
			if !errors.Is(err, ErrUnsupportedNotificationChannel) ||
				notification != nil {
				t.Fatalf(
					"unsupported channel result = notification:%+v error:%v",
					notification,
					err,
				)
			}
		})
	}
	var count int64
	if err := db.Model(&models.Notification{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unsupported channel notification count = %d", count)
	}
}

func newNotificationPreferenceFixture(
	t *testing.T,
) (
	*gorm.DB,
	*NotificationService,
	models.User,
	models.Ticket,
) {
	t.Helper()
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.Notification{},
		&models.NotificationPreference{},
	); err != nil {
		t.Fatal(err)
	}
	recipient := models.User{
		Username:     "preference-recipient",
		Email:        "preference-recipient@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&recipient).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		OrganizationID:     1,
		ProjectID:          10,
		TicketNumber:       "PREF-42",
		Title:              "偏好投递测试",
		Description:        "notification preference fixture",
		Type:               models.TicketTypeIncident,
		Priority:           models.TicketPriorityHigh,
		Status:             models.TicketStatusOpen,
		Source:             models.TicketSourceWeb,
		Version:            1,
		CreatedByActorType: models.ActorTypeSystem,
		CreatedByActorID:   "notification-test",
		AssignedToID:       &recipient.ID,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	return db, NewNotificationServiceWithProtector(db, nil), recipient, ticket
}

func notificationAssignmentEvent(
	t *testing.T,
	eventID string,
	recipientID uint,
	ticket models.Ticket,
) CloudEventEnvelope {
	t.Helper()
	data, err := json.Marshal(ticketNotificationEventData{
		TicketID:       ticket.ID,
		TicketNumber:   ticket.TicketNumber,
		TicketTitle:    ticket.Title,
		TicketPriority: ticket.Priority,
		AssignedToID:   recipientID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return CloudEventEnvelope{
		ID:              eventID,
		OrganizationID:  ticket.OrganizationID,
		ProjectID:       ticket.ProjectID,
		Type:            eventcontract.TicketAssignedEventType,
		ActorType:       models.ActorTypeSystem,
		ActorID:         "preference-worker",
		ResourceVersion: ticket.Version,
		Data:            data,
	}
}

func notificationTimePointer(value time.Time) *time.Time {
	return &value
}
