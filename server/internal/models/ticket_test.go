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

func TestTicketStatusTransitions(t *testing.T) {
	tests := []struct {
		name string
		from TicketStatus
		to   TicketStatus
		want bool
	}{
		{name: "open to in progress", from: TicketStatusOpen, to: TicketStatusInProgress, want: true},
		{name: "open can resolve directly", from: TicketStatusOpen, to: TicketStatusResolved, want: true},
		{name: "in progress to resolved", from: TicketStatusInProgress, to: TicketStatusResolved, want: true},
		{name: "resolved to closed", from: TicketStatusResolved, to: TicketStatusClosed, want: true},
		{name: "resolved can reopen", from: TicketStatusResolved, to: TicketStatusOpen, want: true},
		{name: "cancelled can reopen", from: TicketStatusCancelled, to: TicketStatusOpen, want: true},
		{name: "open cannot close directly", from: TicketStatusOpen, to: TicketStatusClosed, want: false},
		{name: "closed is terminal", from: TicketStatusClosed, to: TicketStatusOpen, want: false},
		{name: "same status is allowed", from: TicketStatusPending, to: TicketStatusPending, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.from.CanTransitionTo(tt.to); got != tt.want {
				t.Fatalf("%s.CanTransitionTo(%s) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
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
