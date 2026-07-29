package models

import (
	"testing"

	"gorm.io/datatypes"
)

func TestActorRefValidationAndLegacyFallbacks(t *testing.T) {
	human := HumanActor(42)
	if err := human.Validate(); err != nil {
		t.Fatalf("human actor should be valid: %v", err)
	}
	if human.Type != ActorTypeHuman || human.ID != "42" {
		t.Fatalf("unexpected human actor: %+v", human)
	}
	if err := (ActorRef{Type: ActorTypeServicePrincipal}).Validate(); err == nil {
		t.Fatal("actor without id must be rejected")
	}
	if err := (ActorRef{Type: "unknown", ID: "x"}).Validate(); err == nil {
		t.Fatal("unknown actor type must be rejected")
	}

	comment := TicketComment{UserID: 42}
	if comment.Actor() != human {
		t.Fatalf("legacy comment should fall back to human actor: %+v", comment.Actor())
	}
	userID := uint(42)
	history := TicketHistory{UserID: &userID}
	if history.Actor() != human {
		t.Fatalf("legacy history should fall back to human actor: %+v", history.Actor())
	}
	systemHistory := TicketHistory{}
	if systemHistory.Actor().Type != ActorTypeSystem {
		t.Fatalf("history without actor or user should fall back to system: %+v", systemHistory.Actor())
	}
}

func TestTicketSourceAgentAndAgentContextResponse(t *testing.T) {
	if !TicketSourceAgent.IsValid() {
		t.Fatal("agent ticket source must be valid")
	}
	if TicketSource("remote_url").IsValid() {
		t.Fatal("unknown ticket source must be invalid")
	}
	ticket := Ticket{
		ID:          1,
		CreatedByID: 7,
		Version:     3,
		TrustLevel:  TicketTrustLevelUntrusted,
		AgentContext: datatypes.NewJSONType(AgentContext{
			Goal: "resolve ticket",
		}),
		TicketNumber: "AI-TEST-1",
		Title:        "Test",
		Description:  "untrusted",
		Type:         TicketTypeRequest,
		Priority:     TicketPriorityNormal,
		Status:       TicketStatusOpen,
		Source:       TicketSourceAgent,
	}
	response := ticket.ToResponse()
	if response.Version != 3 || response.CreatedByActor != HumanActor(7) {
		t.Fatalf("unexpected Agent-native response: %+v", response)
	}
	if response.AgentContext.Goal != "resolve ticket" {
		t.Fatalf("agent context missing from response: %+v", response.AgentContext)
	}
}
