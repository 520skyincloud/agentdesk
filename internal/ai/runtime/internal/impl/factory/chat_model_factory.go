package factory

import (
	"context"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/usagex"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

type ChatModelFactory struct{}

func NewChatModelFactory() *ChatModelFactory {
	return &ChatModelFactory{}
}

func (f *ChatModelFactory) Build(ctx context.Context, aiConfig models.AIConfig) (model.ToolCallingChatModel, error) {
	if strings.EqualFold(strings.TrimSpace(aiConfig.APIMode), "responses") {
		return newResponsesChatModel(aiConfig), nil
	}
	conf := &openai.ChatModelConfig{
		APIKey:  strings.TrimSpace(aiConfig.APIKey),
		BaseURL: strings.TrimSpace(aiConfig.BaseURL),
		Model:   strings.TrimSpace(aiConfig.ModelName),
	}
	timeout := 60 * time.Second
	if aiConfig.TimeoutMS > 0 {
		timeout = time.Duration(aiConfig.TimeoutMS) * time.Millisecond
	}
	conf.HTTPClient = usagex.NewHTTPClient(timeout)
	conf.Timeout = timeout
	if aiConfig.MaxOutputTokens > 0 {
		maxCompletionTokens := aiConfig.MaxOutputTokens
		conf.MaxCompletionTokens = &maxCompletionTokens
	}
	if aiConfig.Provider == enums.AIProviderOpenAI && isAzureOpenAIBaseURL(aiConfig.BaseURL) {
		conf.ByAzure = true
		conf.APIVersion = "2024-06-01"
	}
	if extraFields := providerExtraFields(aiConfig); len(extraFields) > 0 {
		conf.ExtraFields = extraFields
	}
	return openai.NewChatModel(ctx, conf)
}

func isAzureOpenAIBaseURL(baseURL string) bool {
	baseURL = strings.ToLower(strings.TrimSpace(baseURL))
	return strings.Contains(baseURL, ".openai.azure.com")
}

func providerExtraFields(aiConfig models.AIConfig) map[string]any {
	extraFields := map[string]any{}
	if isDashScopeQwenThinkingModel(aiConfig) {
		extraFields["enable_thinking"] = false
	}
	if isDeepSeekV4ThinkingModel(aiConfig) {
		extraFields["thinking"] = map[string]any{"type": "disabled"}
	}
	if len(extraFields) == 0 {
		return nil
	}
	return extraFields
}

func isDashScopeQwenThinkingModel(aiConfig models.AIConfig) bool {
	baseURL := strings.ToLower(strings.TrimSpace(aiConfig.BaseURL))
	modelName := strings.ToLower(strings.TrimSpace(aiConfig.ModelName))
	return strings.Contains(baseURL, "dashscope.aliyuncs.com") && strings.HasPrefix(modelName, "qwen3")
}

func isDeepSeekV4ThinkingModel(aiConfig models.AIConfig) bool {
	baseURL := strings.ToLower(strings.TrimSpace(aiConfig.BaseURL))
	modelName := strings.ToLower(strings.TrimSpace(aiConfig.ModelName))
	return strings.Contains(baseURL, "api.deepseek.com") && strings.HasPrefix(modelName, "deepseek-v4")
}
