package modelconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"agent-desk/internal/pkg/enums"
)

func TestConfigDoesNotSerializeRuntimeModelOrCredentialMaterial(t *testing.T) {
	data, err := json.Marshal(Config{
		Provider:  enums.AIProviderOpenAI,
		BaseURL:   "https://newapi.internal.example/v1",
		APIKey:    "secret-api-key",
		ModelName: "private-model-name",
	})
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
