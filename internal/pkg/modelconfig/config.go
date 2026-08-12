package modelconfig

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-desk/internal/pkg/enums"
)

// Config is the transient model-call shape used by AI runtimes. It is not a
// database entity and must never be serialized into API responses or logs.
type Config struct {
	Provider         enums.AIProvider  `json:"-"`
	BaseURL          string            `json:"-"`
	APIKey           string            `json:"-"`
	APIMode          string            `json:"-"`
	ModelType        enums.AIModelType `json:"-"`
	ModelName        string            `json:"-"`
	Dimension        int               `json:"-"`
	MaxContextTokens int               `json:"-"`
	MaxOutputTokens  int               `json:"-"`
	TimeoutMS        int               `json:"-"`
	MaxRetryCount    int               `json:"-"`
	Temperature      float64           `json:"-"`
	StructuredOutput *StructuredOutput `json:"-"`
}

// StructuredOutput is an invocation-scoped output contract. It is attached by
// the caller that owns the protocol, so other calls reusing the same model slot
// are not accidentally forced into an unrelated JSON shape.
type StructuredOutput struct {
	Name       string          `json:"-"`
	JSONSchema json.RawMessage `json:"-"`
	Strict     bool            `json:"-"`
}

func (c Config) WithJSONSchema(name string, schema []byte) (Config, error) {
	name = normalizeStructuredOutputName(name)
	if name == "" {
		return Config{}, fmt.Errorf("structured output name is required")
	}
	if !json.Valid(schema) {
		return Config{}, fmt.Errorf("structured output JSON Schema is invalid")
	}
	c.StructuredOutput = &StructuredOutput{
		Name:       name,
		JSONSchema: append(json.RawMessage(nil), schema...),
		Strict:     true,
	}
	return c, nil
}

func normalizeStructuredOutputName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

func IsDeepSeekV4Model(modelName string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelName)), "deepseek-v4")
}

func IsDeepSeekV4FlashModel(modelName string) bool {
	return strings.EqualFold(strings.TrimSpace(modelName), "deepseek-v4-flash")
}
