package modelconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// NormalizeResponsesJSONSchema keeps the local contract semantics unchanged
// while making implicit const/enum primitive types explicit for Responses
// providers that require every schema node to declare type, anyOf, or $ref.
func NormalizeResponsesJSONSchema(raw []byte) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var schema any
	if err := decoder.Decode(&schema); err != nil {
		return nil, fmt.Errorf("decode Responses JSON Schema: %w", err)
	}
	normalizeResponsesSchemaNode(schema)
	normalized, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode Responses JSON Schema: %w", err)
	}
	return normalized, nil
}

func normalizeResponsesSchemaNode(value any) {
	switch current := value.(type) {
	case map[string]any:
		if _, hasType := current["type"]; !hasType && current["anyOf"] == nil && current["$ref"] == nil {
			if inferred := inferResponsesSchemaType(current); inferred != "" {
				current["type"] = inferred
			}
		}
		for _, child := range current {
			normalizeResponsesSchemaNode(child)
		}
	case []any:
		for _, child := range current {
			normalizeResponsesSchemaNode(child)
		}
	}
}

func inferResponsesSchemaType(schema map[string]any) string {
	if value, ok := schema["const"]; ok {
		return responsesJSONType(value)
	}
	values, ok := schema["enum"].([]any)
	if !ok || len(values) == 0 {
		return ""
	}
	inferred := responsesJSONType(values[0])
	if inferred == "" {
		return ""
	}
	for _, value := range values[1:] {
		if responsesJSONType(value) != inferred {
			return ""
		}
	}
	return inferred
}

func responsesJSONType(value any) string {
	switch typed := value.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case nil:
		return "null"
	case json.Number:
		if !strings.ContainsAny(typed.String(), ".eE") {
			return "integer"
		}
		return "number"
	case float64:
		return "number"
	default:
		return ""
	}
}
