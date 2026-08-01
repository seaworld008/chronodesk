package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestAutomationConfigurationResponsesExcludePersistenceScopeAndUsers(
	t *testing.T,
) {
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	user := &models.User{
		ID:           91,
		Username:     "must-not-leak",
		Email:        "must-not-leak@example.com",
		PasswordHash: "must-not-leak",
	}
	template := &models.TicketTemplate{
		ID:             12,
		CreatedAt:      now,
		UpdatedAt:      now,
		OrganizationID: 3,
		ProjectID:      7,
		Name:           "标准模板",
		CreatedUser:    user,
		AssignToUser:   user,
	}
	reply := &models.QuickReply{
		ID:             13,
		CreatedAt:      now,
		UpdatedAt:      now,
		OrganizationID: 3,
		ProjectID:      7,
		Name:           "标准回复",
		CreatedUser:    user,
	}
	sla := &models.SLAConfig{
		ID:             14,
		CreatedAt:      now,
		UpdatedAt:      now,
		OrganizationID: 3,
		ProjectID:      7,
		Name:           "标准 SLA",
	}

	for name, value := range map[string]any{
		"sla":         slaConfigDTO(sla),
		"template":    ticketTemplateDTO(template),
		"quick_reply": quickReplyDTO(reply),
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{
				"organization_id",
				"project_id",
				"created_user",
				"assign_to_user",
				"password_hash",
			} {
				if _, exposed := payload[forbidden]; exposed {
					t.Errorf("response exposes %q: %s", forbidden, encoded)
				}
			}
		})
	}
}
