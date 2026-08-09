package handlers

import (
	"encoding/json"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
)

func TestTicketResponseForRoleProjectsAllFiveHumanRoles(t *testing.T) {
	assigneeID := uint(22)
	creatorID := uint(11)
	ticket := &models.Ticket{
		ID:                  1,
		TicketNumber:        "SEC-1",
		Title:               "projection sentinel",
		CustomerEmail:       "pii-email@example.test",
		CustomerPhone:       "+8613800000000",
		CustomerName:        "PII Customer",
		CreatedByID:         &creatorID,
		CreatedBy:           &models.User{ID: creatorID, Username: "creator-sentinel"},
		AssignedToID:        &assigneeID,
		AssignedTo:          &models.User{ID: assigneeID, Username: "assignee-sentinel"},
		CreatedByActorType:  models.ActorTypeHuman,
		CreatedByActorID:    "creator-actor-sentinel",
		AssignedToActorType: models.ActorTypeServicePrincipal,
		AssignedToActorID:   "assignee-actor-sentinel",
		AgentContext: datatypes.NewJSONType(models.AgentContext{
			Goal: "agent-context-sentinel",
		}),
	}

	for _, test := range []struct {
		role                models.ProjectRole
		wantFull            bool
		wantRequesterFields bool
	}{
		{role: models.ProjectRoleAdmin, wantFull: true},
		{role: models.ProjectRoleManager, wantFull: true},
		{role: models.ProjectRoleAgent, wantFull: true},
		{role: models.ProjectRoleRequester, wantRequesterFields: true},
		{role: models.ProjectRoleObserver},
	} {
		t.Run(string(test.role), func(t *testing.T) {
			raw, err := json.Marshal(ticketResponseForRole(ticket, string(test.role)))
			if err != nil {
				t.Fatal(err)
			}
			var response map[string]json.RawMessage
			if err := json.Unmarshal(raw, &response); err != nil {
				t.Fatal(err)
			}
			if string(response["ticket_number"]) != `"SEC-1"` {
				t.Fatalf("public ticket fields missing: %s", raw)
			}

			for _, key := range []string{
				"customer_email",
				"customer_phone",
				"customer_name",
				"created_by_id",
				"created_by",
				"created_by_actor",
				"assigned_to_id",
				"assigned_to",
				"assigned_to_actor",
				"agent_context",
			} {
				_, present := response[key]
				if test.wantFull && !present {
					t.Errorf("full role response omitted %q: %s", key, raw)
				}
				if test.role == models.ProjectRoleObserver && present {
					t.Errorf("observer response leaked %q: %s", key, raw)
				}
			}

			if test.wantRequesterFields {
				if _, present := response["created_by_id"]; !present {
					t.Errorf("requester lost own created_by_id: %s", raw)
				}
				for _, key := range []string{
					"created_by",
					"assigned_to_id",
					"assigned_to",
					"assigned_to_actor",
				} {
					if _, present := response[key]; present {
						t.Errorf("requester response leaked %q: %s", key, raw)
					}
				}
			}
		})
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
