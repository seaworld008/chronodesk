package services

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestDeliverTicketNotificationOutboxRendersEscalationAndPreservesAssignmentEvents(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.Notification{},
	); err != nil {
		t.Fatalf("migrate ticket notification fixture: %v", err)
	}

	recipient := models.User{
		Username:     "escalation-recipient",
		Email:        "escalation-recipient@example.com",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&recipient).Error; err != nil {
		t.Fatalf("create notification recipient: %v", err)
	}

	ticket := models.Ticket{
		OrganizationID:      1,
		ProjectID:           10,
		TicketNumber:        "ESC-42",
		Title:               "数据库连接告警",
		Description:         "ticket notification escalation fixture",
		Type:                models.TicketTypeIncident,
		Priority:            models.TicketPriorityCritical,
		Status:              models.TicketStatusInProgress,
		Source:              models.TicketSourceWeb,
		Version:             3,
		CreatedByActorType:  models.ActorTypeSystem,
		CreatedByActorID:    "notification-test",
		AssignedToID:        &recipient.ID,
		AssignedToActorType: models.ActorTypeHuman,
		AssignedToActorID:   fmt.Sprint(recipient.ID),
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("create notification ticket: %v", err)
	}

	data, err := json.Marshal(ticketNotificationEventData{
		TicketID:       ticket.ID,
		TicketNumber:   ticket.TicketNumber,
		TicketTitle:    ticket.Title,
		TicketPriority: ticket.Priority,
		AssignedToID:   recipient.ID,
	})
	if err != nil {
		t.Fatalf("encode ticket notification event data: %v", err)
	}

	tests := []struct {
		name         string
		eventType    string
		wantTitle    string
		wantContent  string
		wantPriority models.NotificationPriority
	}{
		{
			name:         "escalated",
			eventType:    eventcontract.TicketEscalatedEventType,
			wantTitle:    "工单已升级 - 数据库连接告警",
			wantContent:  "工单 #ESC-42 已升级并分配给您，请优先处理",
			wantPriority: models.NotificationPriorityUrgent,
		},
		{
			name:         "assigned",
			eventType:    eventcontract.TicketAssignedEventType,
			wantTitle:    "新工单已分配 - 数据库连接告警",
			wantContent:  "工单 #ESC-42 已分配给您，请及时处理",
			wantPriority: models.NotificationPriorityHigh,
		},
		{
			name:         "updated",
			eventType:    eventcontract.TicketUpdatedEventType,
			wantTitle:    "新工单已分配 - 数据库连接告警",
			wantContent:  "工单 #ESC-42 已分配给您，请及时处理",
			wantPriority: models.NotificationPriorityHigh,
		},
	}

	service := NewNotificationServiceWithProtector(db, nil)
	destination := fmt.Sprintf(
		"%s:%d",
		models.NotificationTypeTicketAssigned,
		recipient.ID,
	)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			notification, created, err := service.DeliverTicketNotificationOutbox(
				context.Background(),
				CloudEventEnvelope{
					ID:              "ticket-notification-" + test.name,
					OrganizationID:  ticket.OrganizationID,
					ProjectID:       ticket.ProjectID,
					Type:            test.eventType,
					ActorType:       models.ActorTypeSystem,
					ActorID:         "escalation-worker",
					ResourceVersion: ticket.Version,
					Data:            data,
				},
				destination,
			)
			if err != nil {
				t.Fatalf("deliver %s ticket notification: %v", test.name, err)
			}
			if !created || notification == nil || notification.ID == 0 {
				t.Fatalf("notification was not created: created=%v notification=%+v", created, notification)
			}

			var persisted models.Notification
			if err := db.First(&persisted, notification.ID).Error; err != nil {
				t.Fatalf("load persisted ticket notification: %v", err)
			}
			if persisted.Title != test.wantTitle {
				t.Errorf("notification title = %q, want %q", persisted.Title, test.wantTitle)
			}
			if persisted.Content != test.wantContent {
				t.Errorf("notification content = %q, want %q", persisted.Content, test.wantContent)
			}
			if persisted.Priority != test.wantPriority {
				t.Errorf("notification priority = %q, want %q", persisted.Priority, test.wantPriority)
			}
			if persisted.RecipientID != recipient.ID {
				t.Errorf("notification recipient = %d, want %d", persisted.RecipientID, recipient.ID)
			}
			if persisted.Channel != models.NotificationChannelInApp {
				t.Errorf("notification channel = %q, want %q", persisted.Channel, models.NotificationChannelInApp)
			}
		})
	}

	t.Run("escalated rejects mismatched recipient", func(t *testing.T) {
		notification, created, err := service.DeliverTicketNotificationOutbox(
			context.Background(),
			CloudEventEnvelope{
				ID:              "ticket-notification-escalated-mismatched-recipient",
				OrganizationID:  ticket.OrganizationID,
				ProjectID:       ticket.ProjectID,
				Type:            eventcontract.TicketEscalatedEventType,
				ActorType:       models.ActorTypeSystem,
				ActorID:         "escalation-worker",
				ResourceVersion: ticket.Version,
				Data:            data,
			},
			fmt.Sprintf(
				"%s:%d",
				models.NotificationTypeTicketAssigned,
				recipient.ID+1,
			),
		)
		if err == nil ||
			err.Error() != "assignment notification recipient does not match event data" {
			t.Fatalf("mismatched escalation recipient error = %v", err)
		}
		if created || notification != nil {
			t.Fatalf(
				"mismatched escalation recipient created notification: created=%v notification=%+v",
				created,
				notification,
			)
		}
	})
}
