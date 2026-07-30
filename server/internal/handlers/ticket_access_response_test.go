package handlers

import (
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestTicketResponseForRoleRedactsCustomerAssigneeID(t *testing.T) {
	assigneeID := uint(22)
	creatorID := uint(11)
	ticket := &models.Ticket{
		ID:           1,
		CreatedByID:  &creatorID,
		AssignedToID: &assigneeID,
	}

	adminResponse := ticketResponseForRole(ticket, string(models.ProjectRoleAdmin))
	if adminResponse.AssignedToID == nil || *adminResponse.AssignedToID != assigneeID {
		t.Fatalf("admin assigned_to_id = %v, want %d", adminResponse.AssignedToID, assigneeID)
	}

	customerResponse := ticketResponseForRole(ticket, string(models.ProjectRoleRequester))
	if customerResponse.AssignedToID != nil {
		t.Fatalf("customer assigned_to_id = %v, want nil", customerResponse.AssignedToID)
	}
	if customerResponse.CreatedByID != ticket.CreatedByID {
		t.Fatalf("customer created_by_id = %d, want own id %d", customerResponse.CreatedByID, ticket.CreatedByID)
	}
}

func TestTicketHistoryResponseKeepsEventLineageInternal(t *testing.T) {
	eventID := "019b1d8a-4ff1-7ac0-9b12-4e78fa7ae8bb"
	history := &models.TicketHistory{
		ID:              1,
		TicketID:        2,
		ActorType:       models.ActorTypeHuman,
		ActorID:         "3",
		EventID:         &eventID,
		ResourceVersion: 4,
		Provenance:      models.TicketHistoryProvenanceDomainEvent,
		Action:          models.HistoryActionUpdate,
		Description:     "updated",
		IsVisible:       true,
	}

	internal := ticketHistoryResponses([]*models.TicketHistory{history}, false)
	if len(internal) != 1 ||
		internal[0].EventID == nil ||
		*internal[0].EventID != eventID ||
		internal[0].ResourceVersion != 4 ||
		internal[0].Provenance != models.TicketHistoryProvenanceDomainEvent {
		t.Fatalf("internal history lineage = %#v", internal)
	}

	customer := ticketHistoryResponses([]*models.TicketHistory{history}, true)
	if len(customer) != 1 ||
		customer[0].EventID != nil ||
		customer[0].ResourceVersion != 0 ||
		customer[0].Provenance != "" {
		t.Fatalf("customer history leaked internal lineage = %#v", customer)
	}
}
