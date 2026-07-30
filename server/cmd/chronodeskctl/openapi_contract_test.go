package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChronoDeskctlMatchesCanonicalOpenAPI(t *testing.T) {
	contractPath := filepath.FromSlash("../../internal/openapi/openapi.yaml")
	content, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	contract := string(content)
	for description, required := range map[string]string{
		"OpenAPI version":                    "openapi: 3.2.0",
		"project server":                     "https://chronodesk.example/api/v2/projects/{projectKey}",
		"capabilities OAuth scope":           "oauth2: ['tickets:read']",
		"OAuth endpoint":                     "  /oauth/token:",
		"OAuth project and audience binding": "required: [grant_type, project_key, resource]",
		"inbound integration endpoint":       "/integrations/inbound/{connectionId}/mappings/{mappingId}/messages:",
		"Webhook timestamp header":           "X-ChronoDesk-Timestamp",
		"inbound signature routing binding":  "v1\\n{timestamp}\\n{projectKey}\\n{connectionId}\\n{mappingId}\\n",
		"inbound signature body binding":     "{normalizedContentType}\\n{exact_raw_body}",
		"outbound signature input":           "timestamp + \".\" + exact_raw_body",
		"Webhook signature format":           "v1=<lowercase hex HMAC-SHA256",
	} {
		if !strings.Contains(contract, required) {
			t.Errorf("%s missing canonical fragment %q", description, required)
		}
	}
}

func TestChronoDeskctlAudienceResourcesMatchOpenAPI(t *testing.T) {
	base, err := url.Parse("https://desk.example")
	if err != nil {
		t.Fatal(err)
	}
	for audience, expected := range map[string]string{
		"api": "https://desk.example/api/v2",
		"mcp": "https://desk.example/mcp",
		"a2a": "https://desk.example/a2a/v1",
	} {
		got, err := audienceResource(base, audience)
		if err != nil {
			t.Fatalf("audienceResource(%q): %v", audience, err)
		}
		if got != expected {
			t.Errorf("audienceResource(%q) = %q, want %q", audience, got, expected)
		}
	}
}
