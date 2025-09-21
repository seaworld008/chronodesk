package models

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStringListUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  []string
		expectNil bool
		expectErr bool
	}{
		{
			name:     "json array",
			input:    ` ["alpha","beta","gamma"] `,
			expected: []string{"alpha", "beta", "gamma"},
		},
		{
			name:     "json encoded string array",
			input:    `"[\"alpha\", \"beta\", \"gamma\"]"`,
			expected: []string{"alpha", "beta", "gamma"},
		},
		{
			name:     "comma separated",
			input:    `"alpha, beta , , gamma"`,
			expected: []string{"alpha", "beta", "gamma"},
		},
		{
			name:     "single value",
			input:    `"solo"`,
			expected: []string{"solo"},
		},
		{
			name:     "empty string",
			input:    `"   "`,
			expected: []string{},
		},
		{
			name:      "null",
			input:     `null`,
			expectNil: true,
		},
		{
			name:      "invalid object",
			input:     `{ "invalid": true }`,
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var list StringList
			err := json.Unmarshal([]byte(tc.input), &list)

			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.expectNil {
				if list != nil {
					t.Fatalf("expected nil list, got %v", list)
				}
				return
			}

			if !reflect.DeepEqual([]string(list), tc.expected) {
				t.Fatalf("expected %v, got %v", tc.expected, list)
			}
		})
	}
}

func TestNormalizeStringListTrimsValues(t *testing.T) {
	input := []string{" alpha ", "", "beta", "   gamma"}
	expected := []string{"alpha", "beta", "gamma"}

	output := normalizeStringList(input)
	if !reflect.DeepEqual(output, expected) {
		t.Fatalf("expected %v, got %v", expected, output)
	}
}
