package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mlogclub/simple/common/strs"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/usagex"
)

type ChatCompletionResult struct {
	Content          string
	ModelName        string
	PromptTokens     int
	CompletionTokens int
}

type llm struct{}

var LLM = &llm{}

func (s *llm) Chat(ctx context.Context, systemPrompt string, userPrompt string) (*ChatCompletionResult, error) {
	config, err := GetAIConfigForContext(ctx, enums.AIModelTypeLLM)
	if err != nil {
		return nil, err
	}
	return s.ChatWithConfig(ctx, *config, systemPrompt, userPrompt)
}

func (s *llm) ChatWithConfig(ctx context.Context, config models.AIConfig, systemPrompt string, userPrompt string) (*ChatCompletionResult, error) {
	if strings.EqualFold(strings.TrimSpace(config.APIMode), "responses") {
		return s.chatWithResponses(ctx, config, systemPrompt, userPrompt)
	}
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, 2)
	if strs.IsNotBlank(systemPrompt) {
		messages = append(messages, openai.ChatCompletionMessageParamUnion{
			OfSystem: &openai.ChatCompletionSystemMessageParam{
				Content: openai.ChatCompletionSystemMessageParamContentUnion{
					OfString: openai.String(systemPrompt),
				},
			},
		})
	}
	messages = append(messages, openai.ChatCompletionMessageParamUnion{
		OfUser: &openai.ChatCompletionUserMessageParam{
			Content: openai.ChatCompletionUserMessageParamContentUnion{
				OfString: openai.String(userPrompt),
			},
		},
	})

	params := openai.ChatCompletionNewParams{
		Messages: messages,
		Model:    shared.ChatModel(config.ModelName),
	}
	if config.MaxOutputTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(config.MaxOutputTokens))
	}
	applyProviderSpecificChatParams(&params, config)

	client := newOpenAIClient(config)
	chatResp, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to call llm api (model=%s provider=%s system_chars=%d user_chars=%d max_output_tokens=%d): %w",
			config.ModelName, config.Provider, utf8.RuneCountInString(systemPrompt), utf8.RuneCountInString(userPrompt), config.MaxOutputTokens, err)
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no llm choices in response")
	}

	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	return &ChatCompletionResult{
		Content:          content,
		ModelName:        config.ModelName,
		PromptTokens:     int(chatResp.Usage.PromptTokens),
		CompletionTokens: int(chatResp.Usage.CompletionTokens),
	}, nil
}

type llmResponsesRequest struct {
	Model           string         `json:"model"`
	Instructions    string         `json:"instructions,omitempty"`
	Input           string         `json:"input"`
	MaxOutputTokens int            `json:"max_output_tokens,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type llmResponsesResponse struct {
	OutputText string `json:"output_text"`
	Status     string `json:"status"`
	Error      *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
	Output []struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (s *llm) chatWithResponses(ctx context.Context, config models.AIConfig, systemPrompt string, userPrompt string) (*ChatCompletionResult, error) {
	body := llmResponsesRequest{
		Model:        strings.TrimSpace(config.ModelName),
		Instructions: strings.TrimSpace(systemPrompt),
		Input:        strings.TrimSpace(userPrompt),
		Metadata: map[string]any{
			"agentdesk_api_mode": "responses",
			"agentdesk_call":     "llm_chat_with_config",
		},
	}
	if config.MaxOutputTokens > 0 {
		body.MaxOutputTokens = config.MaxOutputTokens
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/") + "/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(config.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	timeout := 60 * time.Second
	if config.TimeoutMS > 0 {
		timeout = time.Duration(config.TimeoutMS) * time.Millisecond
	}
	client := usagex.NewHTTPClient(timeout)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call responses api (model=%s provider=%s system_chars=%d user_chars=%d max_output_tokens=%d): %w",
			config.ModelName, config.Provider, utf8.RuneCountInString(systemPrompt), utf8.RuneCountInString(userPrompt), config.MaxOutputTokens, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("responses api status %d: %s", resp.StatusCode, compactLLMResponseError(raw))
	}
	parsed := llmResponsesResponse{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse responses api response: %w; raw=%s", err, compactLLMResponseError(raw))
	}
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return nil, fmt.Errorf("responses api error: %s", parsed.Error.Message)
	}
	content := strings.TrimSpace(parsed.OutputText)
	if content == "" {
		content = strings.TrimSpace(extractLLMResponsesOutputText(parsed))
	}
	if content == "" {
		return nil, fmt.Errorf("no responses api output text")
	}
	return &ChatCompletionResult{
		Content:          content,
		ModelName:        config.ModelName,
		PromptTokens:     parsed.Usage.InputTokens,
		CompletionTokens: parsed.Usage.OutputTokens,
	}, nil
}

func extractLLMResponsesOutputText(resp llmResponsesResponse) string {
	parts := make([]string, 0)
	for _, output := range resp.Output {
		for _, content := range output.Content {
			if strings.TrimSpace(content.Text) != "" {
				parts = append(parts, strings.TrimSpace(content.Text))
			}
		}
	}
	return strings.Join(parts, "\n")
}

func compactLLMResponseError(raw []byte) string {
	text := strings.Join(strings.Fields(string(raw)), " ")
	runes := []rune(text)
	if len(runes) <= 500 {
		return text
	}
	return string(runes[:500]) + "..."
}

func applyProviderSpecificChatParams(params *openai.ChatCompletionNewParams, config models.AIConfig) {
	if params == nil {
		return
	}
	extraFields := map[string]any{}
	if isDashScopeQwenThinkingModel(config) {
		extraFields["enable_thinking"] = false
	}
	if isDeepSeekV4ThinkingModel(config) {
		extraFields["thinking"] = map[string]any{"type": "disabled"}
	}
	if len(extraFields) > 0 {
		params.SetExtraFields(extraFields)
	}
}

func isDashScopeQwenThinkingModel(config models.AIConfig) bool {
	baseURL := strings.ToLower(strings.TrimSpace(config.BaseURL))
	modelName := strings.ToLower(strings.TrimSpace(config.ModelName))
	return strings.Contains(baseURL, "dashscope.aliyuncs.com") && strings.HasPrefix(modelName, "qwen3")
}

func isDeepSeekV4ThinkingModel(config models.AIConfig) bool {
	baseURL := strings.ToLower(strings.TrimSpace(config.BaseURL))
	modelName := strings.ToLower(strings.TrimSpace(config.ModelName))
	return strings.Contains(baseURL, "api.deepseek.com") && strings.HasPrefix(modelName, "deepseek-v4")
}
