package modelconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"agent-desk/internal/pkg/enums"
)

func TestConfigDoesNotSerializeRuntimeModelOrCredentialMaterial(t *testing.T) {
	config, err := Config{
		Provider:  enums.AIProviderOpenAI,
		BaseURL:   "https://newapi.internal.example/v1",
		APIKey:    "secret-api-key",
		ModelName: "private-model-name",
	}.WithJSONSchema("reply_output.v2", []byte(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(data)
	for _, forbidden := range []string{"newapi", "secret-api-key", "private-model-name", "openai"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("runtime model configuration leaked through JSON: %s", serialized)
		}
	}
	if serialized != "{}" {
		t.Fatalf("runtime model configuration must not expose fields: %s", serialized)
	}
}

func TestConfigWithJSONSchemaNormalizesNameAndCopiesSchema(t *testing.T) {
	raw := []byte(`{"type":"object"}`)
	config, err := (Config{}).WithJSONSchema("reply_output.v2", raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = '['
	if config.StructuredOutput == nil || config.StructuredOutput.Name != "reply_output_v2" {
		t.Fatalf("unexpected structured output: %#v", config.StructuredOutput)
	}
	if string(config.StructuredOutput.JSONSchema) != `{"type":"object"}` || !config.StructuredOutput.Strict {
		t.Fatalf("unexpected schema contract: %#v", config.StructuredOutput)
	}
}

func TestConfigWithJSONSchemaRejectsInvalidSchema(t *testing.T) {
	if _, err := (Config{}).WithJSONSchema("reply", []byte(`{"type":`)); err == nil {
		t.Fatal("expected invalid JSON Schema document to be rejected")
	}
}

func TestIsDeepSeekV4FlashModelRequiresExactName(t *testing.T) {
	if !IsDeepSeekV4FlashModel(" DeepSeek-V4-Flash ") {
		t.Fatal("expected canonical DeepSeek V4 Flash name to match")
	}
	for _, value := range []string{"deepseek-v4-pro", "deepseek-v4-flash-preview", "deepseek-v4"} {
		if IsDeepSeekV4FlashModel(value) {
			t.Fatalf("unexpected DeepSeek V4 Flash match for %q", value)
		}
	}
}
