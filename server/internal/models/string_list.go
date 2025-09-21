package models

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// StringList supports parsing JSON arrays, JSON-encoded arrays, and comma-separated strings.
type StringList []string

// UnmarshalJSON allows flexible tag input from clients.
func (s *StringList) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*s = nil
		return nil
	}

	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*s = normalizeStringList(arr)
		return nil
	}

	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		return s.parseFromString(single)
	}

	return fmt.Errorf("invalid string list payload")
}

func (s *StringList) parseFromString(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		*s = []string{}
		return nil
	}

	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		var arr []string
		if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
			*s = normalizeStringList(arr)
			return nil
		}
		// fall through on parse error and treat as plain text
	}

	parts := strings.Split(trimmed, ",")
	*s = normalizeStringList(parts)
	return nil
}

func normalizeStringList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, item := range values {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
