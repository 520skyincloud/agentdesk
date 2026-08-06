package factory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/modelconfig"
	"agent-desk/internal/pkg/usagex"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type ChatModelFactory struct{}

func NewChatModelFactory() *ChatModelFactory {
	return &ChatModelFactory{}
}

func (f *ChatModelFactory) Build(ctx context.Context, aiConfig modelconfig.Config) (model.ToolCallingChatModel, error) {
	if strings.EqualFold(strings.TrimSpace(aiConfig.APIMode), "responses") {
		return newRetryingToolCallingChatModel(newResponsesChatModel(aiConfig), aiConfig.MaxRetryCount), nil
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
	chatModel, err := openai.NewChatModel(ctx, conf)
	if err != nil {
		return nil, err
	}
	return newRetryingToolCallingChatModel(chatModel, aiConfig.MaxRetryCount), nil
}

type retryingToolCallingChatModel struct {
	delegate      model.ToolCallingChatModel
	maxRetryCount int
}

func newRetryingToolCallingChatModel(delegate model.ToolCallingChatModel, maxRetryCount int) model.ToolCallingChatModel {
	if maxRetryCount < 0 {
		maxRetryCount = 0
	}
	if maxRetryCount > 10 {
		maxRetryCount = 10
	}
	return &retryingToolCallingChatModel{delegate: delegate, maxRetryCount: maxRetryCount}
}

func (m *retryingToolCallingChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if m == nil || m.delegate == nil {
		return nil, fmt.Errorf("chat model unavailable")
	}
	var lastErr error
	for attempt := 0; attempt <= m.maxRetryCount; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		message, err := m.delegate.Generate(ctx, input, opts...)
		if err == nil && isUsableModelMessage(message) {
			return message, nil
		}
		if err == nil {
			err = fmt.Errorf("model returned empty output")
		}
		lastErr = err
		if attempt < m.maxRetryCount && !sleepModelRetry(ctx, time.Duration(attempt+1)*100*time.Millisecond) {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func (m *retryingToolCallingChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if m == nil || m.delegate == nil {
		return nil, fmt.Errorf("chat model unavailable")
	}
	var lastErr error
	for attempt := 0; attempt <= m.maxRetryCount; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reader, err := m.delegate.Stream(ctx, input, opts...)
		if err == nil && reader != nil {
			return reader, nil
		}
		if err == nil {
			err = fmt.Errorf("model returned empty stream")
		}
		lastErr = err
		if attempt < m.maxRetryCount && !sleepModelRetry(ctx, time.Duration(attempt+1)*100*time.Millisecond) {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func (m *retryingToolCallingChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	if m == nil || m.delegate == nil {
		return nil, fmt.Errorf("chat model unavailable")
	}
	delegate, err := m.delegate.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return newRetryingToolCallingChatModel(delegate, m.maxRetryCount), nil
}

func isUsableModelMessage(message *schema.Message) bool {
	return message != nil && (strings.TrimSpace(message.Content) != "" || len(message.ToolCalls) > 0)
}

func sleepModelRetry(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

var _ model.ToolCallingChatModel = (*retryingToolCallingChatModel)(nil)

func isAzureOpenAIBaseURL(baseURL string) bool {
	baseURL = strings.ToLower(strings.TrimSpace(baseURL))
	return strings.Contains(baseURL, ".openai.azure.com")
}

func providerExtraFields(aiConfig modelconfig.Config) map[string]any {
	extraFields := map[string]any{}
	if isDashScopeQwenThinkingModel(aiConfig) {
		extraFields["enable_thinking"] = false
	}
	if isDeepSeekV4ThinkingModel(aiConfig) {
		extraFields["enable_thinking"] = false
		extraFields["thinking"] = map[string]any{"type": "disabled"}
	}
	if len(extraFields) == 0 {
		return nil
	}
	return extraFields
}

func isDashScopeQwenThinkingModel(aiConfig modelconfig.Config) bool {
	baseURL := strings.ToLower(strings.TrimSpace(aiConfig.BaseURL))
	modelName := strings.ToLower(strings.TrimSpace(aiConfig.ModelName))
	return strings.Contains(baseURL, "dashscope.aliyuncs.com") && strings.HasPrefix(modelName, "qwen3")
}

func isDeepSeekV4ThinkingModel(aiConfig modelconfig.Config) bool {
	return modelconfig.IsDeepSeekV4Model(aiConfig.ModelName)
}
