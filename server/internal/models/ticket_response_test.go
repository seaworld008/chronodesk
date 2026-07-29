package models

import "testing"

func TestTicketToResponseIncludesReferenceIDs(t *testing.T) {
	assigneeID := uint(22)
	categoryID := uint(33)
	subcategoryID := uint(44)
	ticket := &Ticket{
		ID:            1,
		CreatedByID:   11,
		AssignedToID:  &assigneeID,
		CategoryID:    &categoryID,
		SubcategoryID: &subcategoryID,
	}

	response := ticket.ToResponse()
	if response.CreatedByID != ticket.CreatedByID {
		t.Fatalf("created_by_id = %d, want %d", response.CreatedByID, ticket.CreatedByID)
	}
	if response.AssignedToID == nil || *response.AssignedToID != assigneeID {
		t.Fatalf("assigned_to_id = %v, want %d", response.AssignedToID, assigneeID)
	}
	if response.CategoryID == nil || *response.CategoryID != categoryID {
		t.Fatalf("category_id = %v, want %d", response.CategoryID, categoryID)
	}
	if response.SubcategoryID == nil || *response.SubcategoryID != subcategoryID {
		t.Fatalf("subcategory_id = %v, want %d", response.SubcategoryID, subcategoryID)
	}
}
