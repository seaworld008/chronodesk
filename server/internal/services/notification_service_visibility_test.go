package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
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
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	secondUser := models.User{
		Username:     "notification-other",
		Email:        "notification-other@example.com",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
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
			OrganizationID: 1, ProjectID: 1,
		},
		{
			Type: models.NotificationTypeSystemAlert, Title: "本人已读",
			Content: "owner read", RecipientID: firstUser.ID, IsRead: true,
			OrganizationID: 1, ProjectID: 1,
		},
		{
			Type: models.NotificationTypeSystemAlert, Title: "他人未读",
			Content: "other unread", RecipientID: secondUser.ID, IsRead: false,
			OrganizationID: 1, ProjectID: 1,
		},
	}
	for index := range notifications {
		if err := db.Create(&notifications[index]).Error; err != nil {
			t.Fatal(err)
		}
	}

	unread := false
	service := NewNotificationServiceWithProtector(db, nil)
	ctx := notificationTestOperationContext(
		t,
		models.ProjectScope{OrganizationID: 1, ProjectID: 1},
		models.HumanActor(firstUser.ID),
	)
	items, total, err := service.GetNotifications(
		ctx,
		&models.NotificationFilter{
			RecipientID: &firstUser.ID,
			IsRead:      &unread,
			Limit:       10,
			OrderBy:     "created_at",
			OrderDir:    "desc",
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
	if _, _, err := service.GetNotifications(
		ctx,
		&models.NotificationFilter{
			RecipientID: &firstUser.ID,
			Limit:       10,
			OrderBy:     "created_at; DROP TABLE notifications",
			OrderDir:    "desc; DROP TABLE users",
		},
	); !errors.Is(err, ErrInvalidNotificationListQuery) {
		t.Fatalf("invalid notification sort error = %v", err)
	}

	items, total, err = service.GetNotifications(
		ctx,
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

func TestGetNotificationsBatchPreloadsOnlyScopedTicketSummaryAndOwnership(
	t *testing.T,
) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.Notification{},
	); err != nil {
		t.Fatal(err)
	}
	recipient := models.User{
		Username:     "summary-recipient",
		Email:        "summary-recipient@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&recipient).Error; err != nil {
		t.Fatal(err)
	}
	ownerID := recipient.ID
	otherOwnerID := ownerID + 1
	tickets := []models.Ticket{
		{
			OrganizationID: 1,
			ProjectID:      1,
			TicketNumber:   "SUM-1",
			Title:          "first summary",
			Description:    "description-sentinel-1",
			CustomerEmail:  "pii-sentinel-1@example.test",
			CustomerPhone:  "phone-sentinel-1",
			CustomerName:   "name-sentinel-1",
			CreatedByID:    &ownerID,
			AgentContext: datatypes.NewJSONType(models.AgentContext{
				Goal: "agent-context-sentinel-1",
			}),
		},
		{
			OrganizationID: 1,
			ProjectID:      1,
			TicketNumber:   "SUM-2",
			Title:          "second summary",
			Description:    "description-sentinel-2",
			CustomerEmail:  "pii-sentinel-2@example.test",
			CreatedByID:    &otherOwnerID,
		},
		{
			OrganizationID: 1,
			ProjectID:      2,
			TicketNumber:   "OTHER-1",
			Title:          "cross-project sentinel",
			Description:    "cross-project-description-sentinel",
			CustomerEmail:  "cross-project-pii@example.test",
			CreatedByID:    &ownerID,
		},
	}
	for index := range tickets {
		if err := db.Create(&tickets[index]).Error; err != nil {
			t.Fatalf("create ticket %d: %v", index, err)
		}
	}
	notifications := []models.Notification{
		{
			OrganizationID:  1,
			ProjectID:       1,
			RecipientID:     recipient.ID,
			Type:            models.NotificationTypeTicketCreated,
			Title:           "first",
			Content:         "first",
			RelatedTicketID: &tickets[0].ID,
		},
		{
			OrganizationID:  1,
			ProjectID:       1,
			RecipientID:     recipient.ID,
			Type:            models.NotificationTypeTicketCreated,
			Title:           "second",
			Content:         "second",
			RelatedTicketID: &tickets[1].ID,
		},
		{
			OrganizationID:  1,
			ProjectID:       1,
			RecipientID:     recipient.ID,
			Type:            models.NotificationTypeTicketCreated,
			Title:           "cross project",
			Content:         "cross project",
			RelatedTicketID: &tickets[2].ID,
		},
	}
	for index := range notifications {
		if err := db.Create(&notifications[index]).Error; err != nil {
			t.Fatalf("create notification %d: %v", index, err)
		}
	}

	var ticketQueries atomic.Int64
	const callbackName = "test:count-notification-ticket-summary-preload"
	if err := db.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(query *gorm.DB) {
			if query.Statement.Table == (models.Ticket{}).TableName() {
				ticketQueries.Add(1)
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(callbackName)
	})

	service := NewNotificationServiceWithProtector(db, nil)
	ctx := notificationTestOperationContext(
		t,
		models.ProjectScope{OrganizationID: 1, ProjectID: 1},
		models.HumanActor(recipient.ID),
	)
	items, total, err := service.GetNotifications(
		ctx,
		&models.NotificationFilter{
			RecipientID: &recipient.ID,
			Limit:       10,
			OrderBy:     "id",
			OrderDir:    "asc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("notifications len=%d total=%d, want 3/3", len(items), total)
	}
	if ticketQueries.Load() != 1 {
		t.Fatalf(
			"related ticket preload queries=%d, want one batched query",
			ticketQueries.Load(),
		)
	}
	for index := 0; index < 2; index++ {
		ticket := items[index].RelatedTicket
		if ticket == nil {
			t.Fatalf("items[%d] missing related ticket summary", index)
		}
		if ticket.ID != tickets[index].ID ||
			ticket.TicketNumber != tickets[index].TicketNumber ||
			ticket.Title != tickets[index].Title ||
			ticket.CreatedByID == nil ||
			*ticket.CreatedByID != *tickets[index].CreatedByID {
			t.Fatalf("items[%d] summary/ownership=%+v", index, ticket)
		}
		if ticket.OrganizationID != 0 ||
			ticket.ProjectID != 0 ||
			ticket.Description != "" ||
			ticket.CustomerEmail != "" ||
			ticket.CustomerPhone != "" ||
			ticket.CustomerName != "" ||
			ticket.AgentContext.Data().Goal != "" {
			t.Errorf("items[%d] preloaded full ticket fields: %+v", index, ticket)
		}
	}
	if items[2].RelatedTicket != nil {
		t.Fatalf(
			"cross-project related ticket was preloaded: %+v",
			items[2].RelatedTicket,
		)
	}
}

func TestGetNotificationsUsesIDTieBreakerInRequestedDirection(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.Notification{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "notification-stable-owner",
		Email:        "notification-stable-owner@example.com",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC().Truncate(time.Second)
	notifications := make([]models.Notification, 30)
	for index := range notifications {
		notifications[index] = models.Notification{
			CreatedAt:      createdAt,
			OrganizationID: 1,
			ProjectID:      1,
			Type:           models.NotificationTypeSystemAlert,
			Title:          fmt.Sprintf("stable-%02d", index),
			Content:        "stable",
			RecipientID:    user.ID,
		}
	}
	if err := db.Create(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	service := NewNotificationServiceWithProtector(db, nil)
	ctx := notificationTestOperationContext(
		t,
		models.ProjectScope{OrganizationID: 1, ProjectID: 1},
		models.HumanActor(user.ID),
	)
	for _, direction := range []string{"asc", "desc"} {
		items, total, err := service.GetNotifications(
			ctx,
			&models.NotificationFilter{
				RecipientID: &user.ID,
				Limit:       10,
				Offset:      10,
				OrderBy:     "created_at",
				OrderDir:    direction,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if total != 30 || len(items) != 10 {
			t.Fatalf("%s page len=%d total=%d", direction, len(items), total)
		}
		for index := range items {
			want := notifications[index+10].ID
			if direction == "desc" {
				want = notifications[19-index].ID
			}
			if items[index].ID != want {
				t.Fatalf(
					"%s stable order at %d: id=%d want=%d",
					direction,
					index,
					items[index].ID,
					want,
				)
			}
		}
	}
}

func TestDeliverTicketNotificationOutboxKeepsSnapshotAfterTicketDeletion(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.Notification{},
		&models.NotificationPreference{},
	); err != nil {
		t.Fatal(err)
	}
	actor := models.User{
		Username: "deleted-ticket-actor", Email: "deleted-ticket-actor@example.com",
		PasswordHash: "hash", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	recipient := models.User{
		Username: "deleted-ticket-recipient", Email: "deleted-ticket-recipient@example.com",
		PasswordHash: "hash", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
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
		CreatedByID:  &recipient.ID,
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
	service := NewNotificationServiceWithProtector(db, nil)
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
