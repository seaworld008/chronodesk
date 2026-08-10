package models

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gorm.io/datatypes"
)

func TestNotificationRelatedTicketSerializesClosedThreeFieldSummary(
	t *testing.T,
) {
	creatorID := uint(71)
	assigneeID := uint(72)
	notification := &Notification{
		ID: 1,
		RelatedTicket: &Ticket{
			ID:            91,
			TicketNumber:  "NOTIFY-91",
			Title:         "summary title",
			Description:   "description-sentinel",
			CustomerEmail: "pii-sentinel@example.test",
			CustomerPhone: "phone-sentinel",
			CustomerName:  "name-sentinel",
			CreatedByID:   &creatorID,
			CreatedBy: &User{
				ID:       creatorID,
				Username: "creator-sentinel",
			},
			AssignedToID: &assigneeID,
			AssignedTo: &User{
				ID:       assigneeID,
				Username: "assignee-sentinel",
			},
			CreatedByActorType:  ActorTypeHuman,
			CreatedByActorID:    "creator-actor-sentinel",
			AssignedToActorType: ActorTypeServicePrincipal,
			AssignedToActorID:   "assignee-actor-sentinel",
			AgentContext: datatypes.NewJSONType(AgentContext{
				Goal: "agent-context-sentinel",
			}),
		},
	}

	raw, err := json.Marshal(notification.ToResponse())
	if err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range []string{
		"description-sentinel",
		"pii-sentinel@example.test",
		"phone-sentinel",
		"name-sentinel",
		"creator-sentinel",
		"assignee-sentinel",
		"creator-actor-sentinel",
		"assignee-actor-sentinel",
		"agent-context-sentinel",
	} {
		if strings.Contains(string(raw), sentinel) {
			t.Errorf("notification response leaked %q: %s", sentinel, raw)
		}
	}

	var response struct {
		RelatedTicket map[string]json.RawMessage `json:"related_ticket"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	gotKeys := make([]string, 0, len(response.RelatedTicket))
	for key := range response.RelatedTicket {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	wantKeys := []string{"id", "ticket_number", "title"}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("related_ticket keys = %v, want %v; response=%s", gotKeys, wantKeys, raw)
	}
}

func TestNotificationWithoutRelatedTicketSerializesExplicitNull(t *testing.T) {
	raw, err := json.Marshal((&Notification{ID: 1}).ToResponse())
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	value, present := response["related_ticket"]
	if !present || string(value) != "null" {
		t.Fatalf("related_ticket = %s present=%v, want explicit null; response=%s", value, present, raw)
	}
}
