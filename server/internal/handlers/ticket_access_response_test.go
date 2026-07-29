package handlers

import (
	"testing"

	"gongdan-system/internal/models"
)

func TestTicketResponseForRoleRedactsCustomerAssigneeID(t *testing.T) {
	assigneeID := uint(22)
	ticket := &models.Ticket{
		ID:           1,
		CreatedByID:  11,
		AssignedToID: &assigneeID,
	}

	adminResponse := ticketResponseForRole(ticket, "admin")
	if adminResponse.AssignedToID == nil || *adminResponse.AssignedToID != assigneeID {
		t.Fatalf("admin assigned_to_id = %v, want %d", adminResponse.AssignedToID, assigneeID)
	}

	customerResponse := ticketResponseForRole(ticket, "customer")
	if customerResponse.AssignedToID != nil {
		t.Fatalf("customer assigned_to_id = %v, want nil", customerResponse.AssignedToID)
	}
	if customerResponse.CreatedByID != ticket.CreatedByID {
		t.Fatalf("customer created_by_id = %d, want own id %d", customerResponse.CreatedByID, ticket.CreatedByID)
	}
}
