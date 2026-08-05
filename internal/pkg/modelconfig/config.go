package modelconfig

import (
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
}

func IsDeepSeekV4Model(modelName string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelName)), "deepseek-v4")
}
