package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agent-desk/internal/pkg/modelconfig"
	"agent-desk/internal/pkg/usagex"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type responsesChatModel struct {
	config modelconfig.Config
	client *http.Client
	tools  []*schema.ToolInfo
}

type responsesRequest struct {
	Model           string              `json:"model"`
	Instructions    string              `json:"instructions,omitempty"`
	Input           any                 `json:"input"`
	MaxOutputTokens int                 `json:"max_output_tokens,omitempty"`
	Text            *responsesText      `json:"text,omitempty"`
	Reasoning       *responsesReasoning `json:"reasoning,omitempty"`
	Tools           []responsesTool     `json:"tools,omitempty"`
	ToolChoice      string              `json:"tool_choice,omitempty"`
	Metadata        map[string]any      `json:"metadata,omitempty"`
}

type responsesText struct {
	Format responsesTextFormat `json:"format"`
}

type responsesTextFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type responsesReasoning struct {
	Effort string `json:"effort"`
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type responsesInputItem struct {
	Type      string `json:"type"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
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
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []struct {
		Type      string `json:"type"`
		Role      string `json:"role"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Content   []struct {
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

func newResponsesChatModel(aiConfig modelconfig.Config) *responsesChatModel {
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
	requestInput := any(prompt)
	if responsesMessagesNeedStructuredInput(input) {
		requestInput = responsesInputFromMessages(input)
	}
	body := responsesRequest{
		Model:        strings.TrimSpace(m.config.ModelName),
		Instructions: instructions,
		Input:        requestInput,
		Metadata: map[string]any{
			"agentdesk_api_mode": "responses",
		},
	}
	if structured := m.config.StructuredOutput; structured != nil {
		if strings.TrimSpace(structured.Name) == "" || !json.Valid(structured.JSONSchema) {
			return nil, fmt.Errorf("responses structured output contract is invalid")
		}
		normalizedSchema, err := modelconfig.NormalizeResponsesJSONSchema(structured.JSONSchema)
		if err != nil {
			return nil, err
		}
		body.Text = &responsesText{Format: responsesTextFormat{
			Type: "json_schema", Name: structured.Name, Strict: structured.Strict,
			Schema: normalizedSchema,
		}}
	}
	if modelconfig.IsDeepSeekV4Model(m.config.ModelName) {
		body.Reasoning = &responsesReasoning{Effort: "none"}
	}
	if len(m.tools) > 0 {
		responseTools, err := buildResponsesTools(m.tools)
		if err != nil {
			return nil, err
		}
		body.Tools = responseTools
		body.ToolChoice = "auto"
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
		class := modelconfig.InvocationErrorNetwork
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			class = modelconfig.InvocationErrorTimeout
		}
		return nil, modelconfig.NewInvocationError(class, 0, true)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, modelconfig.NewInvocationError(modelconfig.InvocationErrorInvalidResponse, resp.StatusCode, true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		invocationErr := responsesInvocationError(resp.StatusCode, raw, m.config.StructuredOutput != nil)
		annotateResponsesReceipt(req.Context(), invocationErr)
		return nil, invocationErr
	}
	parsed := responsesResponse{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		invocationErr := modelconfig.NewInvocationErrorWithMetadata(modelconfig.InvocationErrorInvalidResponse, resp.StatusCode, "invalid_json", "", true)
		annotateResponsesReceipt(req.Context(), invocationErr)
		return nil, invocationErr
	}
	if responsesStatusFailed(parsed) {
		invocationErr := responsesInvocationError(resp.StatusCode, raw, m.config.StructuredOutput != nil)
		annotateResponsesReceipt(req.Context(), invocationErr)
		return nil, invocationErr
	}
	content := strings.TrimSpace(parsed.OutputText)
	if content == "" {
		content = strings.TrimSpace(extractResponsesOutputText(parsed))
	}
	message := &schema.Message{
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
	}
	message.ToolCalls = extractResponsesToolCalls(parsed)
	return message, nil
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

func responsesMessagesNeedStructuredInput(messages []*schema.Message) bool {
	for _, message := range messages {
		if message != nil && (message.Role == schema.Tool || len(message.ToolCalls) > 0) {
			return true
		}
	}
	return false
}

func responsesInputFromMessages(messages []*schema.Message) []responsesInputItem {
	items := make([]responsesInputItem, 0, len(messages))
	for _, message := range messages {
		if message == nil || message.Role == schema.System {
			continue
		}
		content := strings.TrimSpace(message.Content)
		if content == "" && len(message.UserInputMultiContent) > 0 {
			content = textFromInputParts(message.UserInputMultiContent)
		}
		switch message.Role {
		case schema.Tool:
			if callID := strings.TrimSpace(message.ToolCallID); callID != "" {
				items = append(items, responsesInputItem{Type: "function_call_output", CallID: callID, Output: content})
			}
		case schema.Assistant:
			if content != "" {
				items = append(items, responsesInputItem{Type: "message", Role: "assistant", Content: content})
			}
			for _, toolCall := range message.ToolCalls {
				callID := strings.TrimSpace(toolCall.ID)
				if callID == "" {
					continue
				}
				items = append(items, responsesInputItem{
					Type: "function_call", CallID: callID,
					Name: strings.TrimSpace(toolCall.Function.Name), Arguments: strings.TrimSpace(toolCall.Function.Arguments),
				})
			}
		default:
			if content != "" {
				items = append(items, responsesInputItem{Type: "message", Role: "user", Content: content})
			}
		}
	}
	return items
}

func buildResponsesTools(infos []*schema.ToolInfo) ([]responsesTool, error) {
	tools := make([]responsesTool, 0, len(infos))
	for _, info := range infos {
		if info == nil || strings.TrimSpace(info.Name) == "" {
			continue
		}
		parameters := json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
		if info.ParamsOneOf != nil {
			definition, err := info.ParamsOneOf.ToJSONSchema()
			if err != nil {
				return nil, fmt.Errorf("build responses tool %q schema: %w", info.Name, err)
			}
			if definition != nil {
				raw, err := json.Marshal(definition)
				if err != nil {
					return nil, fmt.Errorf("marshal responses tool %q schema: %w", info.Name, err)
				}
				parameters = raw
			}
		}
		tools = append(tools, responsesTool{
			Type: "function", Name: strings.TrimSpace(info.Name),
			Description: strings.TrimSpace(info.Desc), Parameters: parameters,
		})
	}
	return tools, nil
}

func extractResponsesToolCalls(resp responsesResponse) []schema.ToolCall {
	toolCalls := make([]schema.ToolCall, 0)
	for _, output := range resp.Output {
		if output.Type != "function_call" || strings.TrimSpace(output.CallID) == "" || strings.TrimSpace(output.Name) == "" {
			continue
		}
		toolCalls = append(toolCalls, schema.ToolCall{
			ID: output.CallID, Type: "function",
			Function: schema.FunctionCall{Name: output.Name, Arguments: output.Arguments},
		})
	}
	return toolCalls
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

func responsesInvocationError(statusCode int, raw []byte, structured bool) error {
	failure := parseResponsesFailure(statusCode, raw)
	class := modelconfig.InvocationErrorUpstream
	retryable := failure.retryable
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		class = modelconfig.InvocationErrorCredentialRejected
		retryable = false
	case http.StatusNotFound:
		class = modelconfig.InvocationErrorEndpointNotFound
		retryable = false
	case http.StatusBadRequest:
		class = modelconfig.InvocationErrorPayloadRejected
		retryable = false
	case http.StatusTooManyRequests:
		class = modelconfig.InvocationErrorRateLimited
	}
	schemaRejected := structured && responseErrorMentionsSchema(raw)
	if schemaRejected {
		class = modelconfig.InvocationErrorStructuredOutputSchemaRejected
		retryable = false
	}
	if statusCode >= 200 && statusCode < 300 && !schemaRejected {
		class = failure.class
	}
	return modelconfig.NewInvocationErrorWithMetadata(class, statusCode, failure.responseStatus, failure.providerCode, retryable)
}

type responsesFailure struct {
	class          string
	responseStatus string
	providerCode   string
	retryable      bool
}

func parseResponsesFailure(statusCode int, raw []byte) responsesFailure {
	ret := responsesFailure{
		class:     modelconfig.InvocationErrorUpstream,
		retryable: statusCode == http.StatusTooManyRequests || statusCode >= 500,
	}
	var payload struct {
		Status string `json:"status"`
		Error  *struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return ret
	}
	ret.responseStatus = strings.TrimSpace(payload.Status)
	values := make([]string, 0, 3)
	if payload.Error != nil {
		values = append(values, payload.Error.Code, payload.Error.Type)
		ret.providerCode = firstNonEmptyResponseValue(payload.Error.Code, payload.Error.Type)
	}
	if payload.IncompleteDetails != nil {
		values = append(values, payload.IncompleteDetails.Reason)
		if ret.providerCode == "" {
			ret.providerCode = strings.TrimSpace(payload.IncompleteDetails.Reason)
		}
	}
	normalized := strings.ToLower(strings.Join(values, " "))
	switch {
	case containsResponsesErrorCode(normalized,
		"rate_limit", "too_many_requests", "server_error", "service_unavailable", "provider_unavailable",
		"temporarily_unavailable", "overloaded", "upstream_error", "timeout", "request_timeout"):
		ret.retryable = true
		if strings.Contains(normalized, "rate_limit") || strings.Contains(normalized, "too_many_requests") {
			ret.class = modelconfig.InvocationErrorRateLimited
		}
	case containsResponsesErrorCode(normalized,
		"authentication", "invalid_api_key", "permission_denied", "forbidden", "invalid_request",
		"unsupported_model", "model_not_found", "schema", "json_schema", "max_output_tokens"):
		ret.retryable = false
		ret.class = modelconfig.InvocationErrorPayloadRejected
	}
	return ret
}

func responsesStatusFailed(parsed responsesResponse) bool {
	if parsed.Error != nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Status)) {
	case "failed", "cancelled", "incomplete":
		return true
	default:
		return false
	}
}

func annotateResponsesReceipt(ctx context.Context, err error) {
	details, ok := modelconfig.InvocationErrorDetails(err)
	if !ok {
		return
	}
	usagex.AnnotateLatestReceipt(ctx, details.Class, details.ResponseStatus, details.ProviderCode)
}

func containsResponsesErrorCode(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func firstNonEmptyResponseValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func responseErrorMentionsSchema(raw []byte) bool {
	var payload struct {
		Error *struct {
			Message string `json:"message"`
			Param   string `json:"param"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Error == nil {
		return false
	}
	value := strings.ToLower(strings.Join([]string{payload.Error.Message, payload.Error.Param, payload.Error.Code}, " "))
	return strings.Contains(value, "schema") || strings.Contains(value, "json_schema")
}
