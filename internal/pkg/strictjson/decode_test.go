package strictjson

import (
	"strings"
	"testing"
)

type strictJSONFixture struct {
	SchemaVersion string            `json:"schemaVersion"`
	Name          string            `json:"name"`
	Nested        map[string]string `json:"nested"`
}

var strictJSONFixtureSchema = []byte(`{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "additionalProperties":false,
  "required":["schemaVersion","name","nested"],
  "properties":{
    "schemaVersion":{"const":"fixture.v1"},
    "name":{"type":"string","minLength":1},
    "nested":{"type":"object","additionalProperties":{"type":"string"}}
  }
}`)

func TestDecodeObjectStrict(t *testing.T) {
	valid := []byte(` {"schemaVersion":"fixture.v1","name":"ok","nested":{"a":"b"}} `)
	got, err := DecodeObject[strictJSONFixture](valid, DecodeOptions{MaxBytes: 1024, Schema: strictJSONFixtureSchema})
	if err != nil {
		t.Fatalf("DecodeObject() error=%v", err)
	}
	if got.Name != "ok" || got.Nested["a"] != "b" {
		t.Fatalf("DecodeObject()=%+v", got)
	}
}

func TestDecodeObjectRejectsProtocolViolations(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		max  int64
		code string
	}{
		{name: "too large", raw: []byte(`{"schemaVersion":"fixture.v1"}`), max: 4, code: ErrorJSONTooLarge},
		{name: "invalid utf8", raw: []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, max: 1024, code: ErrorJSONInvalidUTF8},
		{name: "markdown fence", raw: []byte("```json\n{}\n```"), max: 1024, code: ErrorJSONRootNotObject},
		{name: "root array", raw: []byte(`[]`), max: 1024, code: ErrorJSONRootNotObject},
		{name: "syntax", raw: []byte(`{"schemaVersion":`), max: 1024, code: ErrorJSONSyntaxInvalid},
		{name: "duplicate root", raw: []byte(`{"schemaVersion":"fixture.v1","schemaVersion":"fixture.v1","name":"ok","nested":{}}`), max: 1024, code: ErrorJSONDuplicateKey},
		{name: "duplicate nested", raw: []byte(`{"schemaVersion":"fixture.v1","name":"ok","nested":{"a":"b","a":"c"}}`), max: 1024, code: ErrorJSONDuplicateKey},
		{name: "unknown", raw: []byte(`{"schemaVersion":"fixture.v1","name":"ok","nested":{},"extra":true}`), max: 1024, code: ErrorJSONUnknownField},
		{name: "trailing", raw: []byte(`{"schemaVersion":"fixture.v1","name":"ok","nested":{}} true`), max: 1024, code: ErrorJSONTrailingContent},
		{name: "schema const", raw: []byte(`{"schemaVersion":"fixture.v2","name":"ok","nested":{}}`), max: 1024, code: ErrorJSONSchemaInvalid},
		{name: "schema type", raw: []byte(`{"schemaVersion":"fixture.v1","name":"","nested":{}}`), max: 1024, code: ErrorJSONSchemaInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeObject[strictJSONFixture](tt.raw, DecodeOptions{MaxBytes: tt.max, Schema: strictJSONFixtureSchema})
			if code, ok := CodeOf(err); !ok || code != tt.code {
				t.Fatalf("error=%v code=%q want=%q", err, code, tt.code)
			}
		})
	}
}

func TestDuplicateKeyErrorContainsNestedPath(t *testing.T) {
	err := RejectDuplicateObjectKeys([]byte(`{"outer":{"value":1,"value":2}}`))
	if err == nil || !strings.Contains(err.Error(), "$/outer/value") {
		t.Fatalf("error=%v", err)
	}
}
