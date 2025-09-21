package models

import (
	"encoding/json"
	"fmt"
	"strings"
)

// JSONMap supports flexible JSON map inputs from clients.
type JSONMap map[string]interface{}

// UnmarshalJSON accepts objects, JSON-encoded strings, or empty values.
func (m *JSONMap) UnmarshalJSON(data []byte) error {
	if len(strings.TrimSpace(string(data))) == 0 || string(data) == "null" {
		*m = nil
		return nil
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err == nil {
		*m = obj
		return nil
	}

	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		trimmed := strings.TrimSpace(str)
		if trimmed == "" {
			*m = JSONMap{}
			return nil
		}
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
			*m = obj
			return nil
		}
		return fmt.Errorf("invalid json map string")
	}

	return fmt.Errorf("invalid json map payload")
}

// ToMap returns a standard map for downstream usage.
func (m JSONMap) ToMap() map[string]interface{} {
	if m == nil {
		return nil
	}
	return map[string]interface{}(m)
}
