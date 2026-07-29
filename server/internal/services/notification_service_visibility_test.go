package services

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestGetNotificationsPreservesRecipientAndReadFilters(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.Notification{}); err != nil {
		t.Fatal(err)
	}
	firstUser := models.User{
		Username:     "notification-owner",
		Email:        "notification-owner@example.com",
		PasswordHash: "hash",
		Role:         models.RoleCustomer,
		Status:       models.UserStatusActive,
	}
	secondUser := models.User{
		Username:     "notification-other",
		Email:        "notification-other@example.com",
		PasswordHash: "hash",
		Role:         models.RoleCustomer,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&firstUser).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&secondUser).Error; err != nil {
		t.Fatal(err)
	}

	notifications := []models.Notification{
		{
			Type: models.NotificationTypeSystemAlert, Title: "本人未读",
			Content: "owner unread", RecipientID: firstUser.ID, IsRead: false,
		},
		{
			Type: models.NotificationTypeSystemAlert, Title: "本人已读",
			Content: "owner read", RecipientID: firstUser.ID, IsRead: true,
		},
		{
			Type: models.NotificationTypeSystemAlert, Title: "他人未读",
			Content: "other unread", RecipientID: secondUser.ID, IsRead: false,
		},
	}
	for index := range notifications {
		if err := db.Create(&notifications[index]).Error; err != nil {
			t.Fatal(err)
		}
	}

	unread := false
	service := NewNotificationService(db)
	items, total, err := service.GetNotifications(
		context.Background(),
		&models.NotificationFilter{
			RecipientID: &firstUser.ID,
			IsRead:      &unread,
			Limit:       10,
			OrderBy:     "created_at; DROP TABLE notifications",
			OrderDir:    "desc; DROP TABLE users",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("filtered notifications = len %d total %d, want 1/1", len(items), total)
	}
	if items[0].ID != notifications[0].ID ||
		items[0].RecipientID != firstUser.ID ||
		items[0].IsRead {
		t.Fatalf("object-level notification filter leaked data: %+v", items[0])
	}

	items, total, err = service.GetNotifications(
		context.Background(),
		&models.NotificationFilter{
			RecipientID: &firstUser.ID,
			Limit:       1,
			OrderBy:     "id",
			OrderDir:    "asc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 1 || items[0].ID != notifications[0].ID {
		t.Fatalf("pagination changed filters: len=%d total=%d first=%v", len(items), total, items)
	}
}

func TestDeliverTicketNotificationOutboxKeepsSnapshotAfterTicketDeletion(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.Notification{},
	); err != nil {
		t.Fatal(err)
	}
	actor := models.User{
		Username: "deleted-ticket-actor", Email: "deleted-ticket-actor@example.com",
		PasswordHash: "hash", Role: models.RoleAgent, Status: models.UserStatusActive,
	}
	recipient := models.User{
		Username: "deleted-ticket-recipient", Email: "deleted-ticket-recipient@example.com",
		PasswordHash: "hash", Role: models.RoleCustomer, Status: models.UserStatusActive,
	}
	if err := db.Create(&actor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&recipient).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "DELETED-NOTIFY-1",
		Title:        "删除后的通知快照",
		Description:  "outbox is delivered after deletion",
		Type:         models.TicketTypeRequest,
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Source:       models.TicketSourceWeb,
		CreatedByID:  recipient.ID,
		Version:      2,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&ticket).Error; err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(ticketNotificationEventData{
		TicketID:       ticket.ID,
		TicketNumber:   ticket.TicketNumber,
		TicketTitle:    ticket.Title,
		TicketPriority: ticket.Priority,
		OldStatus:      models.TicketStatusOpen,
		NewStatus:      models.TicketStatusInProgress,
	})
	if err != nil {
		t.Fatal(err)
	}
	event := CloudEventEnvelope{
		ID:              "deleted-ticket-event",
		Type:            "io.chronodesk.ticket.transitioned.v1",
		ActorType:       models.ActorTypeHuman,
		ActorID:         fmt.Sprint(actor.ID),
		ResourceVersion: ticket.Version,
		Data:            data,
	}
	destination := fmt.Sprintf(
		"%s:%d",
		models.NotificationTypeTicketStatusChanged,
		recipient.ID,
	)
	service := NewNotificationService(db)
	notification, created, err := service.DeliverTicketNotificationOutbox(
		context.Background(),
		event,
		destination,
	)
	if err != nil || !created {
		t.Fatalf("deliver deleted-ticket notification = (%v, %v)", created, err)
	}
	if notification.RelatedTicketID != nil || notification.ActionURL != "" {
		t.Fatalf("deleted ticket reference was persisted: %+v", notification)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(notification.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["ticket_deleted"] != true ||
		metadata["ticket_number"] != ticket.TicketNumber {
		t.Fatalf("deleted ticket snapshot metadata = %#v", metadata)
	}

	replayed, created, err := service.DeliverTicketNotificationOutbox(
		context.Background(),
		event,
		destination,
	)
	if err != nil || created || replayed.ID != notification.ID {
		t.Fatalf("idempotent replay = (%+v, %v, %v)", replayed, created, err)
	}
}
