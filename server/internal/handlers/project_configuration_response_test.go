package handlers

import (
	"encoding/json"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
)

func TestProjectIntakeResponseExcludesScopeActorAndControlFields(t *testing.T) {
	configuration := &services.ProjectIntakeConfiguration{
		ReleaseID:      "0198f148-19f8-7f7a-88ac-66d17c662733",
		ReleaseVersion: 3,
		RequestTypes: []models.RequestTypeVersion{{
			ID:             "0198f148-19f8-7f7a-88ac-66d17c662734",
			OrganizationID: 91,
			ProjectID:      92,
			Key:            "incident",
			Version:        1,
			Status:         models.ConfigurationStatusPublished,
			Name:           "故障",
			WorkClass:      models.WorkClassIncident,
			JSONSchema:     datatypes.JSON(`{"type":"object"}`),
			UISchema:       datatypes.JSON(`{}`),
			ContentHash:    "must-not-leak",
			CreatedByType:  models.ActorTypeHuman,
			CreatedByID:    "must-not-leak",
		}},
		Workflows: []models.WorkflowVersion{{
			ID:             "0198f148-19f8-7f7a-88ac-66d17c662735",
			OrganizationID: 91,
			ProjectID:      92,
			Key:            "incident",
			Version:        1,
			Status:         models.ConfigurationStatusPublished,
			Name:           "故障流程",
			States:         datatypes.JSON(`[]`),
			Transitions:    datatypes.JSON(`[]`),
			ContentHash:    "must-not-leak",
			CreatedByType:  models.ActorTypeHuman,
			CreatedByID:    "must-not-leak",
		}},
	}
	encoded, err := json.Marshal(projectIntakeConfigurationDTO(configuration))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"request_types", "workflows"} {
		items, ok := payload[key].([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("%s = %#v", key, payload[key])
		}
		item := items[0].(map[string]any)
		for _, forbidden := range []string{
			"organization_id",
			"project_id",
			"created_by_type",
			"created_by_id",
			"content_hash",
		} {
			if _, exposed := item[forbidden]; exposed {
				t.Errorf("%s response exposes %q: %s", key, forbidden, encoded)
			}
		}
	}
}
