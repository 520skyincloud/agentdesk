package modelconfig

import (
	"encoding/json"
	"testing"
)

func TestNormalizeResponsesJSONSchemaAddsImplicitPrimitiveTypes(t *testing.T) {
	raw := []byte(`{
  "type":"object",
  "properties":{
    "version":{"const":"v1"},
    "mode":{"enum":["a","b"]},
    "enabled":{"const":true},
    "count":{"const":1},
    "mixed":{"enum":["a",1]},
    "explicit":{"type":"string","enum":["x"]}
  }
}`)
	normalized, err := NormalizeResponsesJSONSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(normalized, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	for key, want := range map[string]string{
		"version": "string", "mode": "string", "enabled": "boolean", "count": "integer", "explicit": "string",
	} {
		if got := properties[key].(map[string]any)["type"]; got != want {
			t.Fatalf("property %s type=%v want=%s", key, got, want)
		}
	}
	if _, exists := properties["mixed"].(map[string]any)["type"]; exists {
		t.Fatal("mixed enum must not receive an incorrect single type")
	}
}

func TestNormalizeResponsesJSONSchemaRejectsInvalidJSON(t *testing.T) {
	if _, err := NormalizeResponsesJSONSchema([]byte(`{"type":`)); err == nil {
		t.Fatal("expected invalid JSON Schema to fail")
	}
}
