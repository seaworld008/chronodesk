package models

import (
	"testing"

	"gorm.io/datatypes"
)

func TestActorRefValidationAndAuthoritativeBusinessActors(t *testing.T) {
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

	userID := uint(42)
	comment := TicketComment{
		UserID:    &userID,
		ActorType: ActorTypeHuman,
		ActorID:   "42",
	}
	if comment.Actor() != human {
		t.Fatalf("comment actor should be authoritative: %+v", comment.Actor())
	}
	history := TicketHistory{UserID: &userID, ActorType: ActorTypeHuman, ActorID: "42"}
	if history.Actor() != human {
		t.Fatalf("history actor should be authoritative: %+v", history.Actor())
	}
	systemHistory := TicketHistory{ActorType: ActorTypeSystem, ActorID: "chronodesk"}
	if systemHistory.Actor().Type != ActorTypeSystem {
		t.Fatalf("system history actor should be authoritative: %+v", systemHistory.Actor())
	}

	// Human foreign keys are projections, never an identity fallback. Rows
	// missing ActorRef must fail migration/constraints instead of silently
	// impersonating the referenced human.
	legacyComment := TicketComment{UserID: &userID}
	if actor := legacyComment.Actor(); actor != (ActorRef{}) {
		t.Fatalf("comment actor unexpectedly fell back to user projection: %+v", actor)
	}
	legacyAttachment := TicketAttachment{UploadedBy: &userID}
	if actor := legacyAttachment.Actor(); actor != (ActorRef{}) {
		t.Fatalf("attachment actor unexpectedly fell back to user projection: %+v", actor)
	}
	legacyHistory := TicketHistory{UserID: &userID}
	if actor := legacyHistory.Actor(); actor != (ActorRef{}) {
		t.Fatalf("history actor unexpectedly fell back to user projection: %+v", actor)
	}
	legacyTicket := Ticket{CreatedByID: &userID}
	if actor := legacyTicket.ToResponse().CreatedByActor; actor != (ActorRef{}) {
		t.Fatalf("ticket creator unexpectedly fell back to user projection: %+v", actor)
	}
}

func TestTicketSourceAgentAndAgentContextResponse(t *testing.T) {
	if !TicketSourceAgent.IsValid() {
		t.Fatal("agent ticket source must be valid")
	}
	if TicketSource("remote_url").IsValid() {
		t.Fatal("unknown ticket source must be invalid")
	}
	createdByID := uint(7)
	ticket := Ticket{
		ID:                 1,
		CreatedByID:        &createdByID,
		CreatedByActorType: ActorTypeHuman,
		CreatedByActorID:   "7",
		Version:            3,
		TrustLevel:         TicketTrustLevelUntrusted,
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
