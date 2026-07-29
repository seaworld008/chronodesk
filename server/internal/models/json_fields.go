package models

import (
	"encoding/json"
	"strings"
)

// decodeJSONField keeps text-backed JSON storage from leaking into API
// responses. Write paths persist valid JSON; blank or corrupt values degrade
// to an explicit empty collection so machine clients receive a stable schema
// instead of null.
func decodeJSONField[T any](raw string, empty T) T {
	if strings.TrimSpace(raw) == "" {
		return empty
	}

	var value T
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return empty
	}
	return value
}

func decodeJSONMap(raw string) map[string]interface{} {
	return decodeJSONField(raw, map[string]interface{}{})
}

func decodeJSONStringSlice(raw string) []string {
	return decodeJSONField(raw, []string{})
}
