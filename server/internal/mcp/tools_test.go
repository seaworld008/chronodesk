package mcp

import (
	"slices"
	"testing"
	"time"
)

func TestToolSchemasMatchAuthoritativeRuntimeLimits(t *testing.T) {
	tools := make(map[string]ToolDefinition)
	for _, tool := range toolDefinitions() {
		tools[tool.Name] = tool
	}

	createProperties := tools["ticket_create"].InputSchema["properties"].(map[string]any)
	if _, exists := createProperties["source"]; exists {
		t.Fatal("ticket_create must derive its trusted source from MCP transport, not caller input")
	}
	for _, required := range tools["ticket_create"].InputSchema["required"].([]string) {
		if required == "source" {
			t.Fatal("ticket_create still requires caller-controlled source")
		}
	}
	for _, field := range []string{
		"request_type_version_id",
		"workflow_version_id",
	} {
		versionID, ok := createProperties[field].(schema)
		if !ok ||
			versionID["format"] != "uuid" ||
			versionID["minLength"] != float64(36) ||
			versionID["maxLength"] != float64(36) {
			t.Fatalf("ticket_create %s schema = %#v", field, versionID)
		}
		required := tools["ticket_create"].InputSchema["required"].([]string)
		if !slices.Contains(required, field) {
			t.Fatalf("ticket_create does not require %s: %#v", field, required)
		}
	}

	for _, name := range []string{"ticket_claim", "ticket_heartbeat"} {
		properties := tools[name].InputSchema["properties"].(map[string]any)
		lease := properties["lease_seconds"].(schema)
		if lease["minimum"] != float64(10) || lease["maximum"] != float64(900) {
			t.Fatalf("%s lease bounds = %#v, want 10..900 seconds", name, lease)
		}
	}

	commentProperties := tools["ticket_add_comment"].InputSchema["properties"].(map[string]any)
	comment := commentProperties["content"].(schema)
	if comment["maxLength"] != float64(10000) {
		t.Fatalf("comment maxLength = %v, want 10000", comment["maxLength"])
	}
}

func TestEveryTicketToolRequiresCanonicalProjectKey(t *testing.T) {
	for _, tool := range toolDefinitions() {
		properties := tool.InputSchema["properties"].(map[string]any)
		projectKey, ok := properties["project_key"].(schema)
		if !ok {
			t.Fatalf("%s has no project_key schema", tool.Name)
		}
		if projectKey["pattern"] != `^[A-Za-z0-9._:-]+$` ||
			projectKey["maxLength"] != float64(32) {
			t.Fatalf("%s project_key schema = %#v", tool.Name, projectKey)
		}
		required, _ := tool.InputSchema["required"].([]string)
		if !slices.Contains(required, "project_key") {
			t.Fatalf("%s does not require project_key: %#v", tool.Name, required)
		}
	}
}

func TestHistoryEntrySchemaRepresentsUnlinkedProvenanceWithoutSyntheticIdentity(t *testing.T) {
	entry := map[string]any{
		"id":               uint64(1),
		"ticket_id":        uint64(2),
		"actor":            map[string]any{"type": "human", "id": "7"},
		"action":           "create",
		"changed_fields":   []any{},
		"reason":           "legacy import",
		"event_id":         nil,
		"resource_version": uint64(0),
		"provenance":       "pre_event",
		"created_at":       time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := validateSchema(entry, historyEntrySchema(), "$.history"); err != nil {
		t.Fatalf("history output schema rejected honest unlinked provenance: %v", err)
	}
}
