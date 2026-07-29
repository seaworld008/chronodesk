package services

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/gorm"
)

func TestDomainContractHumanUpdateKeepsAssignmentActorProjectionAtomic(t *testing.T) {
	fixture := newDurableNotificationFixture(t, false)
	assigneeID := fixture.assignee.ID

	updated, err := fixture.service.UpdateTicketExpectedVersion(
		context.Background(),
		fixture.ticket.ID,
		&models.TicketUpdateRequest{AssignedToID: &assigneeID},
		fixture.actor.ID,
		fixture.ticket.Version,
	)
	if err != nil {
		t.Fatalf("human update assignment failed: %v", err)
	}

	wantActor := models.HumanActor(assigneeID)
	var gotAssignedToID uint
	if updated.AssignedToID != nil {
		gotAssignedToID = *updated.AssignedToID
	}
	if updated.AssignedToID == nil ||
		*updated.AssignedToID != assigneeID ||
		updated.AssignedToActorType != wantActor.Type ||
		updated.AssignedToActorID != wantActor.ID ||
		updated.AssignedToServicePrincipalID != nil {
		t.Fatalf(
			"Human PUT assignment committed divergent projections: assigned_to_id=%d actor=(%q,%q) service_principal=%v; want human ActorRef %+v",
			gotAssignedToID,
			updated.AssignedToActorType,
			updated.AssignedToActorID,
			updated.AssignedToServicePrincipalID,
			wantActor,
		)
	}
}

func TestDomainContractHumanBulkCommandsCommitNotificationOutbox(t *testing.T) {
	t.Run("bulk assignment", func(t *testing.T) {
		fixture := newDurableNotificationFixture(t, false)
		assigneeID := fixture.assignee.ID
		if _, err := fixture.service.BulkUpdateTickets(
			context.Background(),
			&BulkUpdateRequest{
				Tickets: []TicketVersionPrecondition{{
					ID:      fixture.ticket.ID,
					Version: fixture.ticket.Version,
				}},
				AssignedToID: &assigneeID,
			},
			fixture.actor.ID,
		); err != nil {
			t.Fatalf("human bulk assignment failed: %v", err)
		}
		assertDomainNotificationOutbox(
			t,
			fixture.db,
			eventcontract.TicketAssignedEventType,
			fixture.ticket.ID,
			[]string{fmt.Sprintf(
				"%s:%d",
				models.NotificationTypeTicketAssigned,
				fixture.assignee.ID,
			)},
		)
	})

	t.Run("bulk status transition", func(t *testing.T) {
		fixture := newDurableNotificationFixture(t, true)
		status := string(models.TicketStatusInProgress)
		if _, err := fixture.service.BulkUpdateTickets(
			context.Background(),
			&BulkUpdateRequest{
				Tickets: []TicketVersionPrecondition{{
					ID:      fixture.ticket.ID,
					Version: fixture.ticket.Version,
				}},
				Status: &status,
			},
			fixture.actor.ID,
		); err != nil {
			t.Fatalf("human bulk status transition failed: %v", err)
		}
		assertDomainNotificationOutbox(
			t,
			fixture.db,
			eventcontract.TicketTransitionedEventType,
			fixture.ticket.ID,
			[]string{
				fmt.Sprintf(
					"%s:%d",
					models.NotificationTypeTicketStatusChanged,
					fixture.assignee.ID,
				),
				fmt.Sprintf(
					"%s:%d",
					models.NotificationTypeTicketStatusChanged,
					fixture.creator.ID,
				),
			},
		)
	})
}

func assertDomainNotificationOutbox(
	t *testing.T,
	db *gorm.DB,
	eventType string,
	ticketID uint,
	wantDestinations []string,
) {
	t.Helper()
	var event models.DomainEvent
	if err := db.
		Where("type = ? AND subject = ?", eventType, fmt.Sprintf("ticket/%d", ticketID)).
		Order("created_at DESC").
		First(&event).Error; err != nil {
		t.Fatalf("load %s event for ticket %d: %v", eventType, ticketID, err)
	}
	var deliveries []models.OutboxDelivery
	if err := db.
		Where(
			"event_id = ? AND destination_type = ?",
			event.ID,
			NotificationOutboxDestination,
		).
		Order("destination_id ASC").
		Find(&deliveries).Error; err != nil {
		t.Fatalf("load notification Outbox for event %s: %v", event.ID, err)
	}
	gotDestinations := make([]string, 0, len(deliveries))
	for _, delivery := range deliveries {
		if delivery.Status != models.OutboxDeliveryPending {
			t.Fatalf(
				"notification Outbox %s committed in state %q, want pending",
				delivery.ID,
				delivery.Status,
			)
		}
		gotDestinations = append(gotDestinations, delivery.DestinationID)
	}
	sort.Strings(gotDestinations)
	sort.Strings(wantDestinations)
	if fmt.Sprint(gotDestinations) != fmt.Sprint(wantDestinations) {
		t.Fatalf(
			"ticket %d notification Outbox destinations = %v, want %v",
			ticketID,
			gotDestinations,
			wantDestinations,
		)
	}
}
