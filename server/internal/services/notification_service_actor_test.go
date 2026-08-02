package services

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestTicketNotificationOutboxSupportsEveryAuthoritativeActorType(t *testing.T) {
	tests := []struct {
		name       string
		actor      models.ActorRef
		wantSender bool
	}{
		{
			name:       "human",
			actor:      models.HumanActor(1),
			wantSender: true,
		},
		{
			name:  "service principal",
			actor: models.ServicePrincipalActor("11111111-1111-4111-8111-111111111111"),
		},
		{
			name:  "system",
			actor: models.SystemActor("automation"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openTestDB(t)
			if err := db.AutoMigrate(
				&models.User{},
				&models.Ticket{},
				&models.Notification{},
				&models.NotificationPreference{},
			); err != nil {
				t.Fatalf("migrate notification actor fixture: %v", err)
			}
			actorUser := models.User{
				Username:     "notification-actor",
				Email:        "notification-actor@example.com",
				PasswordHash: "hash",
				PlatformRole: models.PlatformRoleMember,
				Status:       models.UserStatusActive,
			}
			recipient := models.User{
				Username:     "notification-recipient",
				Email:        "notification-recipient@example.com",
				PasswordHash: "hash",
				PlatformRole: models.PlatformRoleMember,
				Status:       models.UserStatusActive,
			}
			if err := db.Create(&actorUser).Error; err != nil {
				t.Fatalf("create actor user: %v", err)
			}
			if err := db.Create(&recipient).Error; err != nil {
				t.Fatalf("create recipient: %v", err)
			}
			if test.actor.Type == models.ActorTypeHuman {
				test.actor = models.HumanActor(actorUser.ID)
			}
			ticket := models.Ticket{
				TicketNumber:       "NOTIFICATION-ACTOR-" + fmt.Sprint(recipient.ID),
				Title:              "统一 Actor 通知",
				Description:        "notification actor contract",
				Type:               models.TicketTypeRequest,
				Priority:           models.TicketPriorityNormal,
				Status:             models.TicketStatusOpen,
				Source:             models.TicketSourceAgent,
				Version:            2,
				CreatedByActorType: test.actor.Type,
				CreatedByActorID:   test.actor.ID,
			}
			if err := db.Create(&ticket).Error; err != nil {
				t.Fatalf("create ticket: %v", err)
			}
			data, err := json.Marshal(ticketNotificationEventData{
				TicketID:       ticket.ID,
				TicketNumber:   ticket.TicketNumber,
				TicketTitle:    ticket.Title,
				TicketPriority: ticket.Priority,
				AssignedToID:   recipient.ID,
			})
			if err != nil {
				t.Fatalf("encode event data: %v", err)
			}
			service := NewNotificationServiceWithProtector(db, nil)
			notification, created, err := service.DeliverTicketNotificationOutbox(
				context.Background(),
				CloudEventEnvelope{
					ID:              newNativeID(),
					Type:            eventcontract.TicketAssignedEventType,
					ActorType:       test.actor.Type,
					ActorID:         test.actor.ID,
					ResourceVersion: ticket.Version,
					Data:            data,
				},
				fmt.Sprintf(
					"%s:%d",
					models.NotificationTypeTicketAssigned,
					recipient.ID,
				),
			)
			if err != nil {
				t.Fatalf("deliver %s notification: %v", test.actor.Type, err)
			}
			if !created || notification == nil {
				t.Fatal("notification was not created")
			}
			if test.wantSender {
				if notification.SenderID == nil || *notification.SenderID != actorUser.ID {
					t.Fatalf("human sender projection = %v, want %d", notification.SenderID, actorUser.ID)
				}
			} else if notification.SenderID != nil {
				t.Fatalf("non-human actor projected to sender user %d", *notification.SenderID)
			}
			var metadata map[string]any
			if err := json.Unmarshal([]byte(notification.Metadata), &metadata); err != nil {
				t.Fatalf("decode notification metadata: %v", err)
			}
			actor, ok := metadata["actor"].(map[string]any)
			if !ok ||
				actor["type"] != string(test.actor.Type) ||
				actor["id"] != test.actor.ID {
				t.Fatalf("notification metadata actor = %#v, want %+v", metadata["actor"], test.actor)
			}
		})
	}
}
