package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const jsonSchema202012 = "https://json-schema.org/draft/2020-12/schema"

type schema map[string]any

var supportedSchemaPatterns = map[string]*regexp.Regexp{
	`^[A-Za-z0-9._:-]+$`:     regexp.MustCompile(`^[A-Za-z0-9._:-]+$`),
	`^[A-Za-z0-9+/]*={0,2}$`: regexp.MustCompile(`^[A-Za-z0-9+/]*={0,2}$`),
	`^[a-f0-9]{64}$`:         regexp.MustCompile(`^[a-f0-9]{64}$`),
}

func objectSchema(properties map[string]any, required ...string) schema {
	result := schema{
		"$schema":              jsonSchema202012,
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func integerSchema(description string, minimum float64) schema {
	return schema{"type": "integer", "minimum": minimum, "description": description}
}

func enumSchema(description string, values ...string) schema {
	enumValues := make([]any, len(values))
	for i, value := range values {
		enumValues[i] = value
	}
	return schema{"type": "string", "description": description, "enum": enumValues}
}

func arraySchema(description string, items any) schema {
	return schema{"type": "array", "description": description, "items": items}
}

func toolOutputSchema(dataSchema schema) schema {
	errorSchema := schema{
		"type": "object",
		"properties": map[string]any{
			"code":      schema{"type": "string", "minLength": float64(1)},
			"message":   schema{"type": "string", "minLength": float64(1)},
			"retryable": schema{"type": "boolean"},
			"details":   schema{"type": "object"},
		},
		"required":             []string{"code", "message", "retryable"},
		"additionalProperties": false,
	}
	return schema{
		"$schema": jsonSchema202012,
		"type":    "object",
		"properties": map[string]any{
			"ok":    schema{"type": "boolean"},
			"data":  dataSchema,
			"error": errorSchema,
		},
		"required":             []string{"ok"},
		"additionalProperties": false,
		"allOf": []any{
			schema{
				"if":   schema{"properties": map[string]any{"ok": schema{"const": true}}},
				"then": schema{"required": []string{"data"}, "not": schema{"required": []string{"error"}}},
			},
			schema{
				"if":   schema{"properties": map[string]any{"ok": schema{"const": false}}},
				"then": schema{"required": []string{"error"}, "not": schema{"required": []string{"data"}}},
			},
		},
	}
}

// validateSchema validates the strict subset of JSON Schema used by this
// package. The schemas themselves are full draft 2020-12 documents for MCP
// clients; this validator enforces boundary safety without another dependency.
func validateSchema(value any, definition schema, path string) error {
	if path == "" {
		path = "$"
	}

	if constValue, ok := definition["const"]; ok && !jsonValuesEqual(value, constValue) {
		return fmt.Errorf("%s must equal %v", path, constValue)
	}

	types, hasType := schemaTypes(definition["type"])
	if hasType && !matchesAnyType(value, types) {
		return fmt.Errorf("%s must be %s", path, strings.Join(types, " or "))
	}

	if values, ok := definition["enum"].([]any); ok {
		matched := false
		for _, candidate := range values {
			if jsonValuesEqual(value, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s has an unsupported value", path)
		}
	}

	switch typed := value.(type) {
	case map[string]any:
		if err := validateObject(typed, definition, path); err != nil {
			return err
		}
	case []any:
		if minItems, ok := asFloat(definition["minItems"]); ok && float64(len(typed)) < minItems {
			return fmt.Errorf("%s must contain at least %d item(s)", path, int(minItems))
		}
		if maxItems, ok := asFloat(definition["maxItems"]); ok && float64(len(typed)) > maxItems {
			return fmt.Errorf("%s must contain no more than %d item(s)", path, int(maxItems))
		}
		if itemDefinition, ok := definition["items"].(schema); ok {
			for i, item := range typed {
				if err := validateSchema(item, itemDefinition, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		} else if rawDefinition, ok := definition["items"].(map[string]any); ok {
			for i, item := range typed {
				if err := validateSchema(item, schema(rawDefinition), fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case string:
		if minLength, ok := asFloat(definition["minLength"]); ok && float64(len([]rune(typed))) < minLength {
			return fmt.Errorf("%s is too short", path)
		}
		if maxLength, ok := asFloat(definition["maxLength"]); ok && float64(len([]rune(typed))) > maxLength {
			return fmt.Errorf("%s is too long", path)
		}
		if pattern, ok := definition["pattern"].(string); ok {
			expression, supported := supportedSchemaPatterns[pattern]
			if !supported {
				return fmt.Errorf("invalid server schema at %s", path)
			}
			if !expression.MatchString(typed) {
				return fmt.Errorf("%s has an invalid format", path)
			}
		}
		if format, ok := definition["format"].(string); ok {
			switch format {
			case "date-time":
				if _, err := time.Parse(time.RFC3339, typed); err != nil {
					return fmt.Errorf("%s must be an RFC 3339 timestamp", path)
				}
			case "uuid":
				parsed, err := uuid.Parse(typed)
				if err != nil ||
					len(typed) != len(parsed.String()) ||
					!strings.EqualFold(typed, parsed.String()) {
					return fmt.Errorf("%s must be a canonical UUID", path)
				}
			}
		}
	case float64:
		if minimum, ok := asFloat(definition["minimum"]); ok && typed < minimum {
			return fmt.Errorf("%s must be at least %v", path, minimum)
		}
		if maximum, ok := asFloat(definition["maximum"]); ok && typed > maximum {
			return fmt.Errorf("%s must be no more than %v", path, maximum)
		}
	case float32:
		number := float64(typed)
		if minimum, ok := asFloat(definition["minimum"]); ok && number < minimum {
			return fmt.Errorf("%s must be at least %v", path, minimum)
		}
		if maximum, ok := asFloat(definition["maximum"]); ok && number > maximum {
			return fmt.Errorf("%s must be no more than %v", path, maximum)
		}
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		number, _ := numberAsFloat(typed)
		if minimum, ok := asFloat(definition["minimum"]); ok && number < minimum {
			return fmt.Errorf("%s must be at least %v", path, minimum)
		}
		if maximum, ok := asFloat(definition["maximum"]); ok && number > maximum {
			return fmt.Errorf("%s must be no more than %v", path, maximum)
		}
	}
	return nil
}

func validateObject(value map[string]any, definition schema, path string) error {
	if minProperties, ok := asFloat(definition["minProperties"]); ok && float64(len(value)) < minProperties {
		return fmt.Errorf("%s must contain at least %d propertie(s)", path, int(minProperties))
	}
	if maxProperties, ok := asFloat(definition["maxProperties"]); ok && float64(len(value)) > maxProperties {
		return fmt.Errorf("%s must contain no more than %d propertie(s)", path, int(maxProperties))
	}
	required, _ := definition["required"].([]string)
	if required == nil {
		if values, ok := definition["required"].([]any); ok {
			for _, item := range values {
				if text, ok := item.(string); ok {
					required = append(required, text)
				}
			}
		}
	}
	for _, key := range required {
		if _, ok := value[key]; !ok {
			return fmt.Errorf("%s.%s is required", path, key)
		}
	}

	properties := make(map[string]any)
	switch raw := definition["properties"].(type) {
	case map[string]any:
		properties = raw
	case schema:
		properties = raw
	}

	if additional, ok := definition["additionalProperties"].(bool); ok && !additional {
		for key := range value {
			if _, ok := properties[key]; !ok {
				return fmt.Errorf("%s.%s is not allowed", path, key)
			}
		}
	}

	for key, propertyDefinition := range properties {
		propertyValue, ok := value[key]
		if !ok {
			continue
		}
		var child schema
		switch typed := propertyDefinition.(type) {
		case schema:
			child = typed
		case map[string]any:
			child = schema(typed)
		default:
			continue
		}
		if err := validateSchema(propertyValue, child, path+"."+key); err != nil {
			return err
		}
	}
	return nil
}

func schemaTypes(raw any) ([]string, bool) {
	switch typed := raw.(type) {
	case string:
		return []string{typed}, true
	case []string:
		return typed, true
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := item.(string); ok {
				result = append(result, value)
			}
		}
		return result, len(result) > 0
	default:
		return nil, false
	}
}

func matchesAnyType(value any, types []string) bool {
	for _, expected := range types {
		switch expected {
		case "null":
			if value == nil {
				return true
			}
		case "object":
			if _, ok := value.(map[string]any); ok {
				return true
			}
		case "array":
			if _, ok := value.([]any); ok {
				return true
			}
		case "string":
			if _, ok := value.(string); ok {
				return true
			}
		case "boolean":
			if _, ok := value.(bool); ok {
				return true
			}
		case "number":
			if _, ok := numberAsFloat(value); ok {
				return true
			}
		case "integer":
			if number, ok := numberAsFloat(value); ok && math.Trunc(number) == number {
				return true
			}
		}
	}
	return false
}

func numberAsFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func asFloat(value any) (float64, bool) {
	return numberAsFloat(value)
}

func jsonValuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}
