package services

import (
	"errors"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestHumanTicketCASCannotOverwriteConcurrentAgentVersion(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.Ticket{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "cas-human", Email: "cas-human@example.com",
		PasswordHash: "hash", Role: models.RoleAgent, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	stale := models.Ticket{
		TicketNumber: "CAS-1", Title: "original", Description: "description",
		Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Source: models.TicketSourceWeb,
		CreatedByID: &user.ID, Version: 1,
	}
	if err := db.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}
	if result := db.Model(&models.Ticket{}).
		Where("id = ? AND version = ?", stale.ID, 1).
		Updates(map[string]any{"title": "agent update", "version": 2}); result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("seed concurrent Agent update: rows=%d err=%v", result.RowsAffected, result.Error)
	}
	stale.Priority = models.TicketPriorityHigh
	stale.Version = 2
	ctx := testProjectOperationContext(
		t,
		db,
		models.SystemActor("cas-test"),
	)
	_, err := NewAgentNativeService(db).UpdateTicketVersion(
		ctx,
		VersionedTicketUpdateInput{
			TicketID:        stale.ID,
			ExpectedVersion: 1,
			Actor:           models.SystemActor("cas-test"),
			Changes:         map[string]any{"priority": models.TicketPriorityHigh},
		},
	)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale human snapshot should conflict, got %v", err)
	}
	var current models.Ticket
	if err := db.First(&current, stale.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Title != "agent update" || current.Priority != models.TicketPriorityNormal || current.Version != 2 {
		t.Fatalf("concurrent Agent state was overwritten: %+v", current)
	}
}

func TestHumanTicketLifecycleUsesNativeEventOutboxTransaction(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Category{},
		&models.Ticket{},
		&models.TicketHistory{},
		&models.Notification{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatalf("migrate native lifecycle schema: %v", err)
	}
	user := models.User{
		Username: "human-native", Email: "human-native@example.com",
		PasswordHash: "hash", Role: models.RoleAgent, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	native := NewAgentNativeService(db)
	service := newTicketServiceWithDependenciesForTest(t, db, native, nil, 0)
	ctx := testProjectOperationContext(t, db, models.HumanActor(user.ID))
	ticket, err := service.CreateTicket(ctx, &models.TicketCreateRequest{
		Title: "Human lifecycle", Description: "untrusted",
		Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
		Source: models.TicketSourceWeb,
	}, user.ID)
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if ticket.Version != 1 {
		t.Fatalf("created version = %d", ticket.Version)
	}

	title := "Human lifecycle updated"
	updated, err := service.UpdateTicketExpectedVersion(
		ctx,
		ticket.ID,
		&models.TicketUpdateRequest{Title: &title},
		user.ID,
		ticket.Version,
	)
	if err != nil {
		t.Fatalf("update ticket: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("updated version = %d", updated.Version)
	}

	var events []models.DomainEvent
	if err := db.Order("created_at ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 ||
		events[0].Type != "io.chronodesk.ticket.created.v1" ||
		events[1].Type != "io.chronodesk.ticket.updated.v1" {
		t.Fatalf("events = %#v", events)
	}
	for _, event := range events {
		if event.ActorType != models.ActorTypeHuman ||
			event.ActorID != models.HumanActor(user.ID).ID ||
			event.DataSchema == "" {
			t.Fatalf("event lacks human audit or schema: %#v", event)
		}
	}
	var histories []models.TicketHistory
	if err := db.Where("ticket_id = ?", ticket.ID).Order("id ASC").Find(&histories).Error; err != nil {
		t.Fatal(err)
	}
	if len(histories) != len(events) {
		t.Fatalf("history count = %d, want %d", len(histories), len(events))
	}
	for index := range histories {
		assertTicketHistoryEventLink(t, &histories[index], &events[index])
	}
	var deliveryCount int64
	if err := db.Model(&models.OutboxDelivery{}).Count(&deliveryCount).Error; err != nil {
		t.Fatal(err)
	}
	if deliveryCount != 2 {
		t.Fatalf("outbox deliveries = %d, want 2", deliveryCount)
	}
}
