package modelconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"agent-desk/internal/pkg/strictjson"
)

type PreparedResponsesJSONSchema struct {
	Schema      json.RawMessage
	Fingerprint string
}

// PrepareResponsesJSONSchema validates the exact strict contract sent to a
// Responses provider. The pre-normalize pass accepts implicit primitive types
// on const/enum nodes; the post-normalize pass requires them explicitly.
func PrepareResponsesJSONSchema(raw []byte) (PreparedResponsesJSONSchema, error) {
	if err := strictjson.ValidateSchemaDefinition(raw); err != nil {
		return PreparedResponsesJSONSchema{}, fmt.Errorf("compile Responses JSON Schema: %w", err)
	}
	if err := validateResponsesStrictSchema(raw, false); err != nil {
		return PreparedResponsesJSONSchema{}, err
	}
	normalized, err := NormalizeResponsesJSONSchema(raw)
	if err != nil {
		return PreparedResponsesJSONSchema{}, err
	}
	if err := strictjson.ValidateSchemaDefinition(normalized); err != nil {
		return PreparedResponsesJSONSchema{}, fmt.Errorf("compile normalized Responses JSON Schema: %w", err)
	}
	if err := validateResponsesStrictSchema(normalized, true); err != nil {
		return PreparedResponsesJSONSchema{}, err
	}
	sum := sha256.Sum256(normalized)
	return PreparedResponsesJSONSchema{
		Schema:      normalized,
		Fingerprint: hex.EncodeToString(sum[:]),
	}, nil
}

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

func validateResponsesStrictSchema(raw []byte, requireExplicitPrimitiveType bool) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var schema any
	if err := decoder.Decode(&schema); err != nil {
		return fmt.Errorf("decode strict Responses JSON Schema: %w", err)
	}
	return validateResponsesStrictSchemaNode(schema, "$", requireExplicitPrimitiveType)
}

func validateResponsesStrictSchemaNode(value any, path string, requireExplicitPrimitiveType bool) error {
	switch current := value.(type) {
	case map[string]any:
		typeName, _ := current["type"].(string)
		properties, hasProperties := current["properties"].(map[string]any)
		if typeName == "object" || hasProperties {
			additional, ok := current["additionalProperties"].(bool)
			if !ok || additional {
				return fmt.Errorf("strict Responses JSON Schema %s must set additionalProperties=false", path)
			}
			if !hasProperties {
				return fmt.Errorf("strict Responses JSON Schema %s object must define properties", path)
			}
			required, err := responsesRequiredPropertyNames(current["required"])
			if err != nil {
				return fmt.Errorf("strict Responses JSON Schema %s: %w", path, err)
			}
			propertyNames := make([]string, 0, len(properties))
			for name := range properties {
				propertyNames = append(propertyNames, name)
			}
			sort.Strings(propertyNames)
			if strings.Join(required, "\x00") != strings.Join(propertyNames, "\x00") {
				return fmt.Errorf("strict Responses JSON Schema %s required must exactly match properties", path)
			}
		}
		if typeName == "array" {
			if current["items"] == nil {
				return fmt.Errorf("strict Responses JSON Schema %s array must define items", path)
			}
		}
		if err := validateResponsesPrimitiveConstraint(current, path, requireExplicitPrimitiveType); err != nil {
			return err
		}
		for key, child := range current {
			if err := validateResponsesStrictSchemaNode(child, path+"."+key, requireExplicitPrimitiveType); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range current {
			if err := validateResponsesStrictSchemaNode(child, fmt.Sprintf("%s[%d]", path, index), requireExplicitPrimitiveType); err != nil {
				return err
			}
		}
	}
	return nil
}

func responsesRequiredPropertyNames(value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("required must be an array")
	}
	ret := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		name, ok := item.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("required must contain non-empty property names")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("required contains duplicate property %q", name)
		}
		seen[name] = struct{}{}
		ret = append(ret, name)
	}
	sort.Strings(ret)
	return ret, nil
}

func validateResponsesPrimitiveConstraint(schema map[string]any, path string, requireExplicitPrimitiveType bool) error {
	typeName, _ := schema["type"].(string)
	if value, ok := schema["const"]; ok {
		inferred := responsesJSONType(value)
		if inferred == "" {
			return fmt.Errorf("strict Responses JSON Schema %s const has unsupported type", path)
		}
		if requireExplicitPrimitiveType && typeName == "" {
			return fmt.Errorf("strict Responses JSON Schema %s const must declare type after normalization", path)
		}
		if typeName != "" && typeName != inferred {
			return fmt.Errorf("strict Responses JSON Schema %s const type %q conflicts with %q", path, inferred, typeName)
		}
	}
	values, ok := schema["enum"].([]any)
	if !ok || len(values) == 0 {
		return nil
	}
	inferred := responsesJSONType(values[0])
	if inferred == "" {
		return fmt.Errorf("strict Responses JSON Schema %s enum has unsupported type", path)
	}
	for _, value := range values[1:] {
		if responsesJSONType(value) != inferred {
			return fmt.Errorf("strict Responses JSON Schema %s enum mixes primitive types", path)
		}
	}
	if requireExplicitPrimitiveType && typeName == "" {
		return fmt.Errorf("strict Responses JSON Schema %s enum must declare type after normalization", path)
	}
	if typeName != "" && typeName != inferred {
		return fmt.Errorf("strict Responses JSON Schema %s enum type %q conflicts with %q", path, inferred, typeName)
	}
	return nil
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
