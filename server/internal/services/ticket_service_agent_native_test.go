package services

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestHumanTicketCASCannotOverwriteConcurrentAgentVersion(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.Ticket{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "cas-human", Email: "cas-human@example.com",
		PasswordHash: "hash", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
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
		PasswordHash: "hash", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	native := NewAgentNativeService(db)
	service := newTicketServiceWithDependenciesForTest(t, db, native, nil, 0)
	ctx := testProjectOperationContext(t, db, models.HumanActor(user.ID))
	grantHumanTicketCreateMembership(
		t,
		db,
		ctx,
		user.ID,
		models.ProjectRoleAgent,
	)
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

func TestHumanTicketDueDateOmittedLeavesValueAndVersionUnchanged(t *testing.T) {
	fixture, initial := newHumanTicketDueDateFixture(t)

	updated, err := fixture.service.UpdateTicketExpectedVersion(
		fixture.ctx,
		fixture.ticket.ID,
		&models.TicketUpdateRequest{},
		fixture.actor.ID,
		fixture.ticket.Version,
	)
	if err != nil {
		t.Fatalf("update ticket: %v", err)
	}
	assertTicketDueDate(t, updated, &initial)
	if updated.Version != fixture.ticket.Version {
		t.Fatalf("version = %d, want unchanged %d", updated.Version, fixture.ticket.Version)
	}
	assertHumanTicketDueDateAuditCount(t, fixture, 0, false)
}

func TestHumanTicketDueDateExplicitNullOnEmptyValueIsCompleteNoOp(t *testing.T) {
	fixture := newHumanTicketWithoutDueDateFixture(t)
	var before models.Ticket
	if err := fixture.db.First(&before, fixture.ticket.ID).Error; err != nil {
		t.Fatalf("load ticket before update: %v", err)
	}
	request := &models.TicketUpdateRequest{
		DueDate: models.NewOptionalTime(nil),
	}
	changes, histories, _, err := NewAgentNativeService(fixture.db).buildHumanTicketUpdate(
		fixture.ctx,
		&before,
		request,
	)
	if err != nil {
		t.Fatalf("build empty due date update: %v", err)
	}
	if len(changes) != 0 || len(histories) != 0 {
		t.Fatalf("explicit null for an empty due date produced changes=%#v histories=%#v", changes, histories)
	}

	updated, err := fixture.service.UpdateTicketExpectedVersion(
		fixture.ctx,
		fixture.ticket.ID,
		request,
		fixture.actor.ID,
		fixture.ticket.Version,
	)
	if err != nil {
		t.Fatalf("clear empty due date: %v", err)
	}
	assertTicketDueDate(t, updated, nil)
	if updated.Version != fixture.ticket.Version {
		t.Fatalf("version = %d, want unchanged %d", updated.Version, fixture.ticket.Version)
	}

	var after models.Ticket
	if err := fixture.db.First(&after, fixture.ticket.ID).Error; err != nil {
		t.Fatalf("load ticket after update: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("ticket changed after clearing an empty due date: before=%#v after=%#v", before, after)
	}
	assertHumanTicketDueDateAuditCount(t, fixture, 0, false)
}

func TestHumanTicketDueDateValueUpdatesWithAuditEventAndVersion(t *testing.T) {
	fixture, _ := newHumanTicketDueDateFixture(t)
	replacement := time.Date(2026, time.August, 5, 15, 45, 0, 0, time.UTC)

	updated, err := fixture.service.UpdateTicketExpectedVersion(
		fixture.ctx,
		fixture.ticket.ID,
		&models.TicketUpdateRequest{
			DueDate: models.NewOptionalTime(&replacement),
		},
		fixture.actor.ID,
		fixture.ticket.Version,
	)
	if err != nil {
		t.Fatalf("update ticket: %v", err)
	}
	assertTicketDueDate(t, updated, &replacement)
	if updated.Version != fixture.ticket.Version+1 {
		t.Fatalf("version = %d, want %d", updated.Version, fixture.ticket.Version+1)
	}
	assertHumanTicketDueDateAuditCount(t, fixture, 1, true)
}

func TestHumanTicketDueDateExplicitNullClearsWithAuditEventAndVersion(t *testing.T) {
	fixture, _ := newHumanTicketDueDateFixture(t)

	updated, err := fixture.service.UpdateTicketExpectedVersion(
		fixture.ctx,
		fixture.ticket.ID,
		&models.TicketUpdateRequest{
			DueDate: models.NewOptionalTime(nil),
		},
		fixture.actor.ID,
		fixture.ticket.Version,
	)
	if err != nil {
		t.Fatalf("update ticket: %v", err)
	}
	assertTicketDueDate(t, updated, nil)
	if updated.Version != fixture.ticket.Version+1 {
		t.Fatalf("version = %d, want %d", updated.Version, fixture.ticket.Version+1)
	}

	var nullCount int64
	if err := fixture.db.Model(&models.Ticket{}).
		Where("id = ? AND due_date IS NULL", fixture.ticket.ID).
		Count(&nullCount).Error; err != nil {
		t.Fatalf("query cleared due_date: %v", err)
	}
	if nullCount != 1 {
		t.Fatalf("database due_date is not NULL")
	}
	assertHumanTicketDueDateAuditCount(t, fixture, 1, false)
}

func newHumanTicketDueDateFixture(t *testing.T) (durableNotificationFixture, time.Time) {
	t.Helper()
	fixture := newDurableNotificationFixture(t, false)
	initial := time.Date(2026, time.August, 4, 10, 0, 0, 0, time.UTC)
	if err := fixture.db.Model(&models.Ticket{}).
		Where("id = ?", fixture.ticket.ID).
		Update("due_date", initial).Error; err != nil {
		t.Fatalf("seed due_date: %v", err)
	}
	fixture.ticket.DueDate = &initial
	return fixture, initial
}

func newHumanTicketWithoutDueDateFixture(t *testing.T) durableNotificationFixture {
	t.Helper()
	return newDurableNotificationFixture(t, false)
}

func assertTicketDueDate(t *testing.T, ticket *models.Ticket, want *time.Time) {
	t.Helper()
	if want == nil {
		if ticket.DueDate != nil {
			t.Fatalf("due_date = %v, want nil", ticket.DueDate)
		}
		return
	}
	if ticket.DueDate == nil || !ticket.DueDate.Equal(*want) {
		t.Fatalf("due_date = %v, want %v", ticket.DueDate, want)
	}
}

func assertHumanTicketDueDateAuditCount(
	t *testing.T,
	fixture durableNotificationFixture,
	want int,
	wantNewValue bool,
) {
	t.Helper()
	var histories []models.TicketHistory
	if err := fixture.db.
		Where("ticket_id = ? AND field_name = ?", fixture.ticket.ID, "due_date").
		Find(&histories).Error; err != nil {
		t.Fatalf("query due_date history: %v", err)
	}
	if len(histories) != want {
		t.Fatalf("due_date history count = %d, want %d", len(histories), want)
	}

	var events []models.DomainEvent
	if err := fixture.db.
		Where("subject = ? AND type = ?", fmt.Sprintf("ticket/%d", fixture.ticket.ID), "io.chronodesk.ticket.updated.v1").
		Find(&events).Error; err != nil {
		t.Fatalf("query due_date event: %v", err)
	}
	if len(events) != want {
		t.Fatalf("ticket.updated event count = %d, want %d", len(events), want)
	}
	if want == 0 {
		return
	}
	history := &histories[0]
	event := &events[0]
	if history.OldValue == "" {
		t.Fatal("due_date history must retain the old value")
	}
	if (history.NewValue != "") != wantNewValue {
		t.Fatalf("due_date history new value = %q, want value present %v", history.NewValue, wantNewValue)
	}
	if event.ResourceVersion != fixture.ticket.Version+1 {
		t.Fatalf("event resource version = %d, want %d", event.ResourceVersion, fixture.ticket.Version+1)
	}
	assertTicketHistoryEventLink(t, history, event)
}
