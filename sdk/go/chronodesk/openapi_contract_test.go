package chronodesk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSDKContractMatchesCanonicalOpenAPI(t *testing.T) {
	path := filepath.FromSlash("../../../server/internal/openapi/openapi.yaml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contract := string(content)
	for description, required := range map[string]string{
		"OpenAPI version":                    "openapi: 3.2.0",
		"project server":                     "https://chronodesk.example/api/v2/projects/{projectKey}",
		"capabilities OAuth scope":           "oauth2: ['tickets:read']",
		"OAuth project and audience binding": "required: [grant_type, project_key, resource]",
		"API audience":                       "${APP_URL}/api/v2",
		"MCP audience":                       "${APP_URL}/mcp",
		"A2A audience":                       "${APP_URL}/a2a/v1",
	} {
		if !strings.Contains(contract, required) {
			t.Errorf("%s missing canonical fragment %q", description, required)
		}
	}
}
