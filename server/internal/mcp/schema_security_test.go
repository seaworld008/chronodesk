package mcp

import (
	"strings"
	"testing"
)

func TestValidateSchemaAllowsOnlyServerOwnedAnchoredPatterns(t *testing.T) {
	supported := schema{
		"type":    "string",
		"pattern": `^[a-f0-9]{64}$`,
	}
	if err := validateSchema(strings.Repeat("a", 64), supported, "$.sha256"); err != nil {
		t.Fatalf("validateSchema() rejected supported pattern: %v", err)
	}
	if err := validateSchema("not-a-digest", supported, "$.sha256"); err == nil {
		t.Fatal("validateSchema() accepted a value that does not match the pattern")
	}

	untrusted := schema{
		"type":    "string",
		"pattern": `json-schema.org`,
	}
	if err := validateSchema("https://evil-json-schema.org", untrusted, "$.url"); err == nil {
		t.Fatal("validateSchema() executed a pattern outside the server allowlist")
	}
}
