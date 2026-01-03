package models

import (
	"testing"

	"gorm.io/datatypes"
)

func TestTicketToResponseTags(t *testing.T) {
	ticket := &Ticket{
		Tags: datatypes.JSONSlice[string]{"alpha", "beta", "gamma"},
	}

	response := ticket.ToResponse()
	if len(response.Tags) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(response.Tags))
	}
	if response.Tags[0] != "alpha" || response.Tags[1] != "beta" || response.Tags[2] != "gamma" {
		t.Fatalf("unexpected tags: %v", response.Tags)
	}
}

func TestTicketToResponseCustomFields(t *testing.T) {
	ticket := &Ticket{
		CustomFields: datatypes.NewJSONType(map[string]any{
			"foo":   "bar",
			"count": float64(10),
		}),
	}

	response := ticket.ToResponse()
	if response.CustomFields["foo"] != "bar" {
		t.Fatalf("expected foo=bar, got %v", response.CustomFields["foo"])
	}
	if response.CustomFields["count"].(float64) != 10 {
		t.Fatalf("expected count=10, got %v", response.CustomFields["count"])
	}
}

func TestTicketToResponseEmptyFields(t *testing.T) {
	ticket := &Ticket{}
	response := ticket.ToResponse()

	if response.Tags != nil && len(response.Tags) != 0 {
		t.Fatalf("expected empty tags, got %v", response.Tags)
	}
	if response.CustomFields == nil {
		// This is expected behavior - empty JSONType returns nil map
	}
}
