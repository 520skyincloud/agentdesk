package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/usagex"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type responsesChatModel struct {
	config models.AIConfig
	client *http.Client
	tools  []*schema.ToolInfo
}

type responsesRequest struct {
	Model           string         `json:"model"`
	Instructions    string         `json:"instructions,omitempty"`
	Input           string         `json:"input"`
	MaxOutputTokens int            `json:"max_output_tokens,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type responsesResponse struct {
	ID         string `json:"id"`
	Object     string `json:"object"`
	Status     string `json:"status"`
	OutputText string `json:"output_text"`
	Error      *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage struct {
		InputTokens        int `json:"input_tokens"`
		OutputTokens       int `json:"output_tokens"`
		TotalTokens        int `json:"total_tokens"`
		InputTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
}

func newResponsesChatModel(aiConfig models.AIConfig) *responsesChatModel {
	timeout := 60 * time.Second
	if aiConfig.TimeoutMS > 0 {
		timeout = time.Duration(aiConfig.TimeoutMS) * time.Millisecond
	}
	return &responsesChatModel{
		config: aiConfig,
		client: usagex.NewHTTPClient(timeout),
	}
}

func (m *responsesChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	instructions, prompt := responsesPromptFromMessages(input)
	body := responsesRequest{
		Model:        strings.TrimSpace(m.config.ModelName),
		Instructions: instructions,
		Input:        prompt,
		Metadata: map[string]any{
			"agentdesk_api_mode": "responses",
		},
	}
	if m.config.MaxOutputTokens > 0 {
		body.MaxOutputTokens = m.config.MaxOutputTokens
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(strings.TrimSpace(m.config.BaseURL), "/") + "/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(m.config.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("responses api status %d: %s", resp.StatusCode, compactResponseError(raw))
	}
	parsed := responsesResponse{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse responses api response: %w; raw=%s", err, compactResponseError(raw))
	}
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return nil, fmt.Errorf("responses api error: %s", parsed.Error.Message)
	}
	content := strings.TrimSpace(parsed.OutputText)
	if content == "" {
		content = strings.TrimSpace(extractResponsesOutputText(parsed))
	}
	return &schema.Message{
		Role:    schema.Assistant,
		Content: content,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: parsed.Status,
			Usage: &schema.TokenUsage{
				PromptTokens:       parsed.Usage.InputTokens,
				CompletionTokens:   parsed.Usage.OutputTokens,
				TotalTokens:        parsed.Usage.TotalTokens,
				PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: parsed.Usage.InputTokensDetails.CachedTokens},
				CompletionTokensDetails: schema.CompletionTokensDetails{
					ReasoningTokens: parsed.Usage.OutputTokensDetails.ReasoningTokens,
				},
			},
		},
	}, nil
}

func (m *responsesChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("responses api stream is not implemented; use Generate")
}

func (m *responsesChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	clone := *m
	clone.tools = append([]*schema.ToolInfo(nil), tools...)
	return &clone, nil
}

func responsesPromptFromMessages(messages []*schema.Message) (string, string) {
	instructions := make([]string, 0, len(messages))
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		content := strings.TrimSpace(message.Content)
		if content == "" && len(message.UserInputMultiContent) > 0 {
			content = textFromInputParts(message.UserInputMultiContent)
		}
		if content == "" {
			continue
		}
		switch message.Role {
		case schema.System:
			instructions = append(instructions, content)
		case schema.Assistant:
			parts = append(parts, "已回复："+content)
		case schema.Tool:
			parts = append(parts, "工具结果："+content)
		default:
			parts = append(parts, "客人："+content)
		}
	}
	return strings.Join(instructions, "\n\n"), strings.Join(parts, "\n")
}

func textFromInputParts(parts []schema.MessageInputPart) string {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part.Text) != "" {
			texts = append(texts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(texts, "\n")
}

func extractResponsesOutputText(resp responsesResponse) string {
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

func compactResponseError(raw []byte) string {
	text := strings.Join(strings.Fields(string(raw)), " ")
	runes := []rune(text)
	if len(runes) <= 500 {
		return text
	}
	return string(runes[:500]) + "..."
}
