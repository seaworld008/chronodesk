package services

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/gorm"
)

func TestUpdateTicketVersionNormalizesStructuredEventDataForNotifications(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "structured-event-notification")
	ticket := seedNativeTicket(t, db, user.ID, "STRUCTURED-EVENT-001")
	assignee := models.HumanActor(user.ID)
	if err := db.Model(&models.Ticket{}).
		Where("id = ?", ticket.ID).
		Updates(map[string]any{
			"assigned_to_id":         user.ID,
			"assigned_to_actor_type": assignee.Type,
			"assigned_to_actor_id":   assignee.ID,
		}).Error; err != nil {
		t.Fatalf("seed canonical assignment: %v", err)
	}

	type automationSnapshot struct {
		AutomationRuleID string `json:"automation_rule_id"`
		Reason           string `json:"reason"`
	}
	service := NewAgentNativeService(db)
	result, err := service.UpdateTicketVersion(
		context.Background(),
		VersionedTicketUpdateInput{
			TicketID:        ticket.ID,
			ExpectedVersion: ticket.Version,
			Actor:           models.SystemActor("automation"),
			RequiredScope:   models.ScopeTicketsTransition,
			Action:          "ticket.transition",
			Changes: map[string]any{
				"status": models.TicketStatusInProgress,
			},
			EventType: eventcontract.TicketTransitionedEventType,
			EventData: automationSnapshot{
				AutomationRuleID: "rule-1",
				Reason:           "SLA workflow",
			},
		},
	)
	if err != nil {
		t.Fatalf("structured event update: %v", err)
	}

	var eventData map[string]any
	if err := json.Unmarshal(result.Event.Data, &eventData); err != nil {
		t.Fatalf("decode persisted event data: %v", err)
	}
	for _, field := range []string{
		"automation_rule_id",
		"reason",
		"ticket_id",
		"ticket_number",
		"ticket_title",
		"ticket_priority",
		"old_status",
		"new_status",
	} {
		if _, exists := eventData[field]; !exists {
			t.Fatalf("persisted event data is missing %q: %#v", field, eventData)
		}
	}

	var deliveries []models.OutboxDelivery
	if err := db.
		Where(
			"event_id = ? AND destination_type = ?",
			result.Event.ID,
			NotificationOutboxDestination,
		).
		Find(&deliveries).Error; err != nil {
		t.Fatalf("load notification Outbox: %v", err)
	}
	if len(deliveries) != 1 ||
		deliveries[0].DestinationID != fmt.Sprintf(
			"%s:%d",
			models.NotificationTypeTicketStatusChanged,
			user.ID,
		) {
		t.Fatalf("notification Outbox deliveries = %#v", deliveries)
	}
}

func TestAppendDomainEventAdditionalTargetsDeduplicatesDefaults(t *testing.T) {
	db := openAgentNativeTestDB(t)
	target := OutboxTarget{Type: "event_stream", ID: "tickets"}
	service := NewAgentNativeService(
		db,
		AgentNativeOptions{DefaultOutboxTargets: []OutboxTarget{target}},
	)
	var event *models.DomainEvent
	err := service.InTransaction(context.Background(), func(ctx context.Context, tx *gorm.DB) error {
		var appendErr error
		event, appendErr = service.AppendDomainEventWithAdditionalTargetsTx(
			ctx,
			tx,
			DomainEventInput{
				Type:            "io.chronodesk.ticket.tested.v1",
				Subject:         "ticket/1",
				Actor:           models.SystemActor("test"),
				ResourceVersion: 1,
				Data:            map[string]any{"ticket_id": 1},
			},
			[]OutboxTarget{
				target,
				target,
				{Type: "automation", ID: "rules"},
			},
		)
		return appendErr
	})
	if err != nil {
		t.Fatalf("append event with duplicate targets: %v", err)
	}
	var deliveries []models.OutboxDelivery
	if err := db.
		Where("event_id = ?", event.ID).
		Order("destination_type ASC, destination_id ASC").
		Find(&deliveries).Error; err != nil {
		t.Fatalf("load Outbox deliveries: %v", err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("deduplicated deliveries = %#v, want 2 unique destinations", deliveries)
	}
	if deliveries[0].DestinationType != "automation" ||
		deliveries[0].DestinationID != "rules" ||
		deliveries[1].DestinationType != target.Type ||
		deliveries[1].DestinationID != target.ID {
		t.Fatalf("unexpected deduplicated deliveries: %#v", deliveries)
	}
}
