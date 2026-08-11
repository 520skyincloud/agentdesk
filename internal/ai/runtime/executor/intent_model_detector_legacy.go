package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/factory"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/replyintent"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/services"

	"github.com/cloudwego/eino/schema"
)

type legacyRuntimeIntentDetectJSON struct {
	PrimaryIntent      string                        `json:"primaryIntent"`
	SubIntent          string                        `json:"subIntent"`
	Confidence         float64                       `json:"confidence"`
	NeedsKnowledge     bool                          `json:"needsKnowledge"`
	NeedsTool          bool                          `json:"needsTool"`
	NeedsResource      bool                          `json:"needsResource"`
	NeedsHumanRoute    bool                          `json:"needsHumanRoute"`
	NeedsClarification bool                          `json:"needsClarification"`
	ResourceType       string                        `json:"resourceType"`
	ResourceAction     string                        `json:"resourceAction"`
	ResourceActions    legacyRuntimeIntentStringList `json:"resourceActions"`
	SecondaryIntents   legacyRuntimeIntentStringList `json:"secondaryIntents"`
	MixedSubTasks      legacyRuntimeIntentStringList `json:"mixedSubTasks"`
	IntentTasks        legacyRuntimeIntentTaskList   `json:"intentTasks"`
	Reason             string                        `json:"reason"`
}

type legacyRuntimeIntentTaskJSON struct {
	Intent          string `json:"intent"`
	SubIntent       string `json:"subIntent"`
	Text            string `json:"text"`
	NeedsKnowledge  bool   `json:"needsKnowledge"`
	NeedsResource   bool   `json:"needsResource"`
	NeedsTool       bool   `json:"needsTool"`
	NeedsHumanRoute bool   `json:"needsHumanRoute"`
	ResourceAction  string `json:"resourceAction"`
	Reason          string `json:"reason"`
}

type legacyRuntimeIntentStringList []string

func (list *legacyRuntimeIntentStringList) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" || trimmed == "false" || trimmed == "true" {
		*list = nil
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		if single = strings.TrimSpace(single); single != "" {
			*list = []string{single}
		} else {
			*list = nil
		}
		return nil
	}
	var rawItems []any
	if err := json.Unmarshal(data, &rawItems); err != nil {
		return err
	}
	items := make([]string, 0, len(rawItems))
	for _, item := range rawItems {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			items = append(items, strings.TrimSpace(text))
		}
	}
	*list = items
	return nil
}

type legacyRuntimeIntentTaskList []legacyRuntimeIntentTaskJSON

func (list *legacyRuntimeIntentTaskList) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" || trimmed == "false" || trimmed == "true" {
		*list = nil
		return nil
	}
	var single legacyRuntimeIntentTaskJSON
	if err := json.Unmarshal(data, &single); err == nil && strings.TrimSpace(single.Intent) != "" {
		*list = []legacyRuntimeIntentTaskJSON{single}
		return nil
	}
	var items []legacyRuntimeIntentTaskJSON
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	*list = items
	return nil
}

func detectRuntimeIntentLegacy(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, error) {
	resolved, err := resolveRuntimeIntentDetectModelCall(req)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	intentConfig := resolved.RuntimeConfig()
	if strings.TrimSpace(intentConfig.ModelName) == "" || strings.TrimSpace(string(intentConfig.Provider)) == "" {
		return callbacks.IntentTraceData{}, fmt.Errorf("intent model unavailable")
	}
	intentCtx, cancel := context.WithTimeout(ctx, runtimeIntentDetectTimeout(intentConfig.TimeoutMS))
	defer cancel()
	intentCtx, usageCapture := usagex.WithCapture(intentCtx)
	intentCtx = usagex.WithScope(intentCtx, services.ModelCallUsageScope(resolved, req.Conversation.ID, req.UserMessage.ID, req.UserMessage.RequestID))
	chatModel, err := factory.NewChatModelFactory().Build(intentCtx, intentConfig)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	profile := resolveRuntimeIntentProfile(resolveRuntimeIntentScope(req))
	if profile == nil {
		return callbacks.IntentTraceData{}, fmt.Errorf("published tenant industry profile unavailable")
	}
	messages := []*schema.Message{
		schema.SystemMessage(legacyRuntimeIntentDetectSystemPromptForProfile(profile)),
		schema.UserMessage(buildRuntimeIntentDetectUserPrompt(req, history, configs)),
	}
	startedAt := time.Now()
	offset := len(usageCapture.Receipts())
	result, err := chatModel.Generate(intentCtx, messages)
	if err != nil {
		recordIntentModelUsage(req, intentConfig, resolved, nil, gatewayReceiptSince(usageCapture, offset), 1, time.Since(startedAt).Milliseconds(), err)
		return callbacks.IntentTraceData{}, err
	}
	recordIntentModelUsage(req, intentConfig, resolved, result, gatewayReceiptSince(usageCapture, offset), 1, time.Since(startedAt).Milliseconds(), nil)
	parsed, err := parseRuntimeIntentDetectLegacyJSON(result.Content)
	if err != nil {
		startedAt = time.Now()
		offset = len(usageCapture.Receipts())
		retry, retryErr := chatModel.Generate(intentCtx, append(messages, schema.SystemMessage(
			"上一版 IntentDetect 输出不是合法 JSON。请重新输出严格 JSON；不要输出 Markdown、解释、注释或多余文本。",
		)))
		if retryErr != nil {
			recordIntentModelUsage(req, intentConfig, resolved, nil, gatewayReceiptSince(usageCapture, offset), 2, time.Since(startedAt).Milliseconds(), retryErr)
			return callbacks.IntentTraceData{}, fmt.Errorf("%w; retry failed: %v", err, retryErr)
		}
		recordIntentModelUsage(req, intentConfig, resolved, retry, gatewayReceiptSince(usageCapture, offset), 2, time.Since(startedAt).Milliseconds(), nil)
		parsed, err = parseRuntimeIntentDetectLegacyJSON(retry.Content)
		if err != nil {
			return callbacks.IntentTraceData{}, err
		}
	}
	return callbacks.IntentTraceData{
		DetectedIntent: parsed.PrimaryIntent, MatchedIntentCode: parsed.PrimaryIntent, PrimaryIntent: parsed.PrimaryIntent,
		SubIntent: parsed.SubIntent, SecondaryIntents: []string(parsed.SecondaryIntents), SecondaryIntentCodes: []string(parsed.SecondaryIntents),
		IntentConfidence: parsed.Confidence, ShouldReply: true,
		NeedsKnowledge: parsed.NeedsKnowledge, NeedsTool: parsed.NeedsTool, NeedsResource: parsed.NeedsResource,
		NeedsHumanRoute: parsed.NeedsHumanRoute, NeedsClarification: parsed.NeedsClarification,
		ResourceType: parsed.ResourceType, ResourceAction: parsed.ResourceAction, ResourceActions: []string(parsed.ResourceActions),
		IntentTasks:      convertLegacyRuntimeIntentTasks([]legacyRuntimeIntentTaskJSON(parsed.IntentTasks)),
		HumanRoutePolicy: parsed.SubIntent, Reason: strings.TrimSpace("model IntentDetect JSON: " + parsed.Reason),
	}, nil
}

func parseRuntimeIntentDetectLegacyJSON(content string) (legacyRuntimeIntentDetectJSON, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end >= start {
		content = content[start : end+1]
	}
	var parsed legacyRuntimeIntentDetectJSON
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return parsed, err
	}
	return parsed, nil
}

func convertLegacyRuntimeIntentTasks(tasks []legacyRuntimeIntentTaskJSON) []callbacks.IntentTaskTraceData {
	ret := make([]callbacks.IntentTaskTraceData, 0, len(tasks))
	for index, task := range tasks {
		intent := canonicalIntentCode(task.Intent)
		if intent == "" {
			continue
		}
		ret = append(ret, callbacks.IntentTaskTraceData{
			Sequence: index + 1, Intent: intent, SubIntent: strings.TrimSpace(task.SubIntent), Text: strings.TrimSpace(task.Text),
			RequestMode: "answer", Confidence: 0.65,
			NeedsKnowledge: task.NeedsKnowledge || intent == "hotel_info", NeedsResource: task.NeedsResource || intent == "hotel_variable",
			NeedsTool: task.NeedsTool, NeedsHumanRoute: task.NeedsHumanRoute || intent == "human_complaint_risk",
			ResourceAction: strings.TrimSpace(task.ResourceAction), Reason: strings.TrimSpace(task.Reason),
		})
	}
	return ret
}

func legacyRuntimeIntentDetectSystemPromptForProfile(profile *models.ReplyIntentProfile) string {
	if profile == nil {
		return replyintent.DefaultHotelIntentDetectSystemPrompt()
	}
	prompt := strings.TrimSpace(profile.IntentDetectPrompt)
	schemaText := strings.TrimSpace(profile.IntentJSONSchema)
	if prompt == "" {
		prompt = replyintent.DefaultHotelIntentDetectPrompt()
	}
	if schemaText == "" {
		schemaText = replyintent.DefaultHotelIntentJSONSchema()
	}
	return strings.TrimSpace(prompt + "\n\n" + schemaText)
}
