package mcp

import "testing"

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
