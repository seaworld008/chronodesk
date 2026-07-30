package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestAutomationLogResponsePublishesBoundedRuleAndTicketDTOs(t *testing.T) {
	now := time.Date(2026, time.July, 31, 8, 0, 0, 0, time.UTC)
	responses := automationLogResponses([]*models.AutomationLog{
		{
			ID:              11,
			CreatedAt:       now,
			OrganizationID:  2,
			ProjectID:       3,
			RuleID:          4,
			TicketID:        5,
			TriggerEvent:    "io.chronodesk.ticket.created.v1",
			ExecutedAt:      now,
			Success:         true,
			ExecutionTime:   7,
			ActionsExecuted: "[]",
			Changes:         "{}",
			Rule: &models.AutomationRule{
				ID:             4,
				Name:           "自动分派",
				Description:    "项目内分派规则",
				RuleType:       "assignment",
				TriggerEvent:   "io.chronodesk.ticket.created.v1",
				Priority:       10,
				IsActive:       true,
				SuccessCount:   8,
				FailureCount:   1,
				ExecutionCount: 9,
				CreatedAt:      now,
				UpdatedAt:      now,
				Conditions:     `[{"field":"title","value":"untrusted"}]`,
				Actions:        `[{"type":"assign"}]`,
			},
			Ticket: &models.Ticket{
				ID:            5,
				TicketNumber:  "OPS-5",
				Title:         "示例工单",
				Status:        models.TicketStatusOpen,
				InternalNotes: "must-not-leak",
				CustomerEmail: "must-not-leak@example.invalid",
			},
		},
	})
	payload, err := json.Marshal(responses)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 {
		t.Fatalf("responses = %v", decoded)
	}
	rule, ok := decoded[0]["rule"].(map[string]any)
	if !ok {
		t.Fatalf("rule = %T", decoded[0]["rule"])
	}
	assertJSONKeys(t, rule, []string{
		"id",
		"name",
		"description",
		"rule_type",
		"trigger_event",
		"priority",
		"is_active",
		"success_count",
		"failure_count",
		"execution_count",
		"created_at",
		"updated_at",
	})
	ticket, ok := decoded[0]["ticket"].(map[string]any)
	if !ok {
		t.Fatalf("ticket = %T", decoded[0]["ticket"])
	}
	assertJSONKeys(t, ticket, []string{
		"id",
		"ticket_number",
		"title",
		"status",
	})
	for _, forbidden := range []string{
		"internal_notes",
		"customer_email",
		"conditions",
		"actions",
	} {
		if _, leaked := rule[forbidden]; leaked {
			t.Errorf("rule leaked %q", forbidden)
		}
		if _, leaked := ticket[forbidden]; leaked {
			t.Errorf("ticket leaked %q", forbidden)
		}
	}
}

func assertJSONKeys(
	t *testing.T,
	value map[string]any,
	expected []string,
) {
	t.Helper()
	if len(value) != len(expected) {
		t.Fatalf("keys = %v, want %v", value, expected)
	}
	for _, key := range expected {
		if _, ok := value[key]; !ok {
			t.Errorf("missing JSON field %q", key)
		}
	}
}
