package models

import "testing"

func TestParseStringSliceFromJSON(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected []string
	}{
		{"empty string", "", []string{}},
		{"invalid json", "not-json", []string{}},
		{"json array", `["alpha", "beta", "gamma"]`, []string{"alpha", "beta", "gamma"}},
		{"with whitespace", `[" alpha ", "", "beta"]`, []string{"alpha", "beta"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseStringSliceFromJSON(tc.input)
			if len(got) != len(tc.expected) {
				t.Fatalf("expected length %d, got %d", len(tc.expected), len(got))
			}
			for i, expected := range tc.expected {
				if got[i] != expected {
					t.Fatalf("expected %q at index %d, got %q", expected, i, got[i])
				}
			}
		})
	}
}

func TestParseCustomFieldsFromJSON(t *testing.T) {
	input := `{"foo":"bar","count":10}`
	result := parseCustomFieldsFromJSON(input)

	if result["foo"] != "bar" {
		t.Fatalf("expected foo=bar, got %v", result["foo"])
	}

	if result["count"].(float64) != 10 {
		t.Fatalf("expected count=10, got %v", result["count"])
	}
}

func TestParseCustomFieldsHandlesInvalidJSON(t *testing.T) {
	result := parseCustomFieldsFromJSON("not-json")
	if len(result) != 0 {
		t.Fatalf("expected empty map, got %v", result)
	}
}
