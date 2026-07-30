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

func TestValidateSchemaEnforcesCanonicalUUIDFormat(t *testing.T) {
	definition := schema{"type": "string", "format": "uuid"}
	if err := validateSchema(
		"018f0f95-9e85-7a2b-8c3d-1234567890ab",
		definition,
		"$.request_type_version_id",
	); err != nil {
		t.Fatalf("validateSchema() rejected canonical UUID: %v", err)
	}
	for _, invalid := range []string{
		"",
		"not-a-uuid",
		"018f0f959e857a2b8c3d1234567890ab",
		"urn:uuid:018f0f95-9e85-7a2b-8c3d-1234567890ab",
	} {
		if err := validateSchema(
			invalid,
			definition,
			"$.request_type_version_id",
		); err == nil {
			t.Fatalf("validateSchema() accepted non-canonical UUID %q", invalid)
		}
	}
}
