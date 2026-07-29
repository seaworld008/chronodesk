package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/gorm"
)

type durableNotificationFixture struct {
	db       *gorm.DB
	service  TicketServiceInterface
	creator  models.User
	assignee models.User
	actor    models.User
	ticket   models.Ticket
}

func newDurableNotificationFixture(t *testing.T, assigned bool) durableNotificationFixture {
	t.Helper()
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.ServicePrincipal{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketHistory{},
		&models.Notification{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatalf("migrate durable notification schema: %v", err)
	}
	users := []models.User{
		{
			Username: "notification-creator", Email: "notification-creator@example.com",
			PasswordHash: "hash", Role: models.RoleUser, Status: models.UserStatusActive,
		},
		{
			Username: "notification-assignee", Email: "notification-assignee@example.com",
			PasswordHash: "hash", Role: models.RoleAgent, Status: models.UserStatusActive,
		},
		{
			Username: "notification-actor", Email: "notification-actor@example.com",
			PasswordHash: "hash", Role: models.RoleAgent, Status: models.UserStatusActive,
		},
	}
	for i := range users {
		if err := db.Create(&users[i]).Error; err != nil {
			t.Fatalf("create user %d: %v", i, err)
		}
	}
	ticket := models.Ticket{
		TicketNumber: "NOTIFY-1",
		Title:        "Durable notification",
		Description:  "notification must survive a process crash",
		Type:         models.TicketTypeRequest,
		Priority:     models.TicketPriorityHigh,
		Status:       models.TicketStatusOpen,
		Source:       models.TicketSourceWeb,
		CreatedByID:  users[0].ID,
		Version:      1,
	}
	if assigned {
		ticket.AssignedToID = &users[1].ID
		ticket.AssignedToActorType = models.ActorTypeHuman
		ticket.AssignedToActorID = models.HumanActor(users[1].ID).ID
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	native := NewAgentNativeService(db, AgentNativeOptions{
		DefaultOutboxTargets: []OutboxTarget{{
			Type: "event_stream", ID: "default", MaxAttempts: 8,
		}},
	})
	return durableNotificationFixture{
		db:       db,
		service:  NewTicketServiceWithAgentNative(db, nil, 0, native),
		creator:  users[0],
		assignee: users[1],
		actor:    users[2],
		ticket:   ticket,
	}
}

func TestHumanTicketWritesCommitDurableNotificationOutboxTargets(t *testing.T) {
	t.Run("update status and assignee", func(t *testing.T) {
		fixture := newDurableNotificationFixture(t, false)
		status := models.TicketStatusInProgress
		assigneeID := fixture.assignee.ID
		if _, err := fixture.service.UpdateTicket(
			context.Background(),
			fixture.ticket.ID,
			&models.TicketUpdateRequest{
				Status:       &status,
				AssignedToID: &assigneeID,
			},
			fixture.actor.ID,
		); err != nil {
			t.Fatalf("update ticket: %v", err)
		}
		assertCommittedNotificationTargets(
			t,
			fixture.db,
			"io.chronodesk.ticket.updated.v1",
			[]string{
				fmt.Sprintf("%s:%d", models.NotificationTypeTicketAssigned, fixture.assignee.ID),
				fmt.Sprintf("%s:%d", models.NotificationTypeTicketStatusChanged, fixture.assignee.ID),
				fmt.Sprintf("%s:%d", models.NotificationTypeTicketStatusChanged, fixture.creator.ID),
			},
		)
	})

	t.Run("assign", func(t *testing.T) {
		fixture := newDurableNotificationFixture(t, false)
		if _, err := fixture.service.AssignTicket(
			fixture.ticket.ID,
			fixture.assignee.ID,
			fixture.actor.ID,
			"please investigate",
		); err != nil {
			t.Fatalf("assign ticket: %v", err)
		}
		assertCommittedNotificationTargets(
			t,
			fixture.db,
			"io.chronodesk.ticket.assigned.v1",
			[]string{fmt.Sprintf(
				"%s:%d",
				models.NotificationTypeTicketAssigned,
				fixture.assignee.ID,
			)},
		)
	})

	t.Run("transition", func(t *testing.T) {
		fixture := newDurableNotificationFixture(t, true)
		if _, err := fixture.service.UpdateTicketStatus(
			fixture.ticket.ID,
			string(models.TicketStatusInProgress),
			fixture.actor.ID,
			"started",
			"",
		); err != nil {
			t.Fatalf("transition ticket: %v", err)
		}
		assertCommittedNotificationTargets(
			t,
			fixture.db,
			"io.chronodesk.ticket.transitioned.v1",
			[]string{
				fmt.Sprintf("%s:%d", models.NotificationTypeTicketStatusChanged, fixture.assignee.ID),
				fmt.Sprintf("%s:%d", models.NotificationTypeTicketStatusChanged, fixture.creator.ID),
			},
		)
	})
}

func assertCommittedNotificationTargets(
	t *testing.T,
	db *gorm.DB,
	eventType string,
	wantDestinations []string,
) {
	t.Helper()
	var event models.DomainEvent
	if err := db.Where("type = ?", eventType).First(&event).Error; err != nil {
		t.Fatalf("load domain event: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatalf("decode event snapshot: %v", err)
	}
	for _, field := range []string{"ticket_id", "ticket_number", "ticket_title", "ticket_priority"} {
		if data[field] == nil || data[field] == "" {
			t.Fatalf("event snapshot field %q is missing: %#v", field, data)
		}
	}

	var deliveries []models.OutboxDelivery
	if err := db.
		Where("event_id = ? AND destination_type = ?", event.ID, NotificationOutboxDestination).
		Order("destination_id ASC").
		Find(&deliveries).Error; err != nil {
		t.Fatalf("load notification Outbox targets: %v", err)
	}
	gotDestinations := make([]string, 0, len(deliveries))
	for i := range deliveries {
		if deliveries[i].Status != models.OutboxDeliveryPending {
			t.Fatalf("notification delivery committed in state %q", deliveries[i].Status)
		}
		gotDestinations = append(gotDestinations, deliveries[i].DestinationID)
	}
	sort.Strings(wantDestinations)
	if fmt.Sprint(gotDestinations) != fmt.Sprint(wantDestinations) {
		t.Fatalf("notification destinations = %v, want %v", gotDestinations, wantDestinations)
	}

	var notificationCount int64
	if err := db.Model(&models.Notification{}).Count(&notificationCount).Error; err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	if notificationCount != 0 {
		t.Fatalf(
			"ticket transaction created %d notifications before durable Outbox delivery",
			notificationCount,
		)
	}
}

func TestNotificationOutboxFailureRollsBackHumanTicketWrite(t *testing.T) {
	fixture := newDurableNotificationFixture(t, false)
	native := NewAgentNativeService(fixture.db, AgentNativeOptions{
		DefaultOutboxTargets: []OutboxTarget{{Type: "", ID: ""}},
	})
	service := NewTicketServiceWithAgentNative(fixture.db, nil, 0, native)
	if _, err := service.AssignTicket(
		fixture.ticket.ID,
		fixture.assignee.ID,
		fixture.actor.ID,
		"must roll back",
	); err == nil {
		t.Fatal("invalid Outbox target did not fail the ticket transaction")
	}

	var current models.Ticket
	if err := fixture.db.First(&current, fixture.ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.AssignedToID != nil || current.Version != fixture.ticket.Version {
		t.Fatalf("ticket escaped failed Outbox transaction: %+v", current)
	}
	for name, model := range map[string]any{
		"history":      &models.TicketHistory{},
		"domain event": &models.DomainEvent{},
		"delivery":     &models.OutboxDelivery{},
		"notification": &models.Notification{},
	} {
		var count int64
		if err := fixture.db.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("failed transaction committed %d %s rows", count, name)
		}
	}
}
