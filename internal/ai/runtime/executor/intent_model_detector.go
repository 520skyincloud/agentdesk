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
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/replyintent"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/services"

	"github.com/cloudwego/eino/schema"
	"github.com/mlogclub/simple/sqls"
)

type runtimeIntentModelDetector interface {
	DetectRuntimeIntent(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, error)
}

type llmRuntimeIntentDetector struct{}

const runtimeIntentDetectTimeout = 60 * time.Second

type runtimeIntentDetectJSON struct {
	PrimaryIntent      string                  `json:"primaryIntent"`
	SubIntent          string                  `json:"subIntent"`
	Confidence         float64                 `json:"confidence"`
	NeedsKnowledge     bool                    `json:"needsKnowledge"`
	NeedsTool          bool                    `json:"needsTool"`
	NeedsResource      bool                    `json:"needsResource"`
	NeedsHumanRoute    bool                    `json:"needsHumanRoute"`
	NeedsClarification bool                    `json:"needsClarification"`
	ResourceType       string                  `json:"resourceType"`
	ResourceAction     string                  `json:"resourceAction"`
	ResourceActions    runtimeIntentStringList `json:"resourceActions"`
	SecondaryIntents   runtimeIntentStringList `json:"secondaryIntents"`
	MixedSubTasks      runtimeIntentStringList `json:"mixedSubTasks"`
	IntentTasks        runtimeIntentTaskList   `json:"intentTasks"`
	Reason             string                  `json:"reason"`
}

type runtimeIntentTaskJSON struct {
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

type runtimeIntentStringList []string

func (list *runtimeIntentStringList) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" || trimmed == "false" || trimmed == "true" {
		*list = nil
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		if strings.TrimSpace(single) == "" {
			*list = nil
			return nil
		}
		*list = []string{strings.TrimSpace(single)}
		return nil
	}
	var rawItems []any
	if err := json.Unmarshal(data, &rawItems); err != nil {
		return err
	}
	items := make([]string, 0, len(rawItems))
	for _, item := range rawItems {
		text, ok := item.(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		if text != "" {
			items = append(items, text)
		}
	}
	*list = items
	return nil
}

type runtimeIntentTaskList []runtimeIntentTaskJSON

func (list *runtimeIntentTaskList) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" || trimmed == "false" || trimmed == "true" {
		*list = nil
		return nil
	}
	var single runtimeIntentTaskJSON
	if err := json.Unmarshal(data, &single); err == nil && strings.TrimSpace(single.Intent) != "" {
		*list = []runtimeIntentTaskJSON{single}
		return nil
	}
	var items []runtimeIntentTaskJSON
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	*list = items
	return nil
}

func detectRuntimeIntentWithModel(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, detector runtimeIntentModelDetector) (callbacks.IntentTraceData, callbacks.IntentPromptTraceData, bool) {
	if isMediaOnlyWithoutActionableIntent(req.UserMessage) && !hasAdjacentTextMediaQuestion(req, history) {
		intent := callbacks.IntentTraceData{DetectedIntent: "media_gate", MatchedIntentCode: "media_gate", SubIntent: "media_only_no_question", IntentConfidence: 0.9, ShouldReply: false, Reason: "media gate: media-only message has no actionable intent"}
		return intent, selectIntentPromptPack(intent), true
	}
	configs := loadEnabledIntentConfigs(resolveRuntimeIntentScope(req))
	if detector == nil {
		detector = llmRuntimeIntentDetector{}
	}
	intent, err := detector.DetectRuntimeIntent(ctx, req, history, configs)
	if err != nil {
		intent := intentDetectUnavailableIntent("IntentDetect model failed: " + err.Error())
		return intent, selectIntentPromptPack(intent), true
	}
	intent = normalizeModelIntentTrace(intent, req, history, configs)
	if intent.PrimaryIntent == "" {
		return callbacks.IntentTraceData{}, callbacks.IntentPromptTraceData{}, false
	}
	prompt := promptForModelDetectedIntent(intent, configs)
	return intent, prompt, true
}

func (llmRuntimeIntentDetector) DetectRuntimeIntent(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, error) {
	intentConfig, credentialRevision, resolveErr := resolveRuntimeIntentDetectAIConfigWithRevision(req)
	if resolveErr != nil {
		return callbacks.IntentTraceData{}, resolveErr
	}
	if strings.TrimSpace(intentConfig.ModelName) == "" || strings.TrimSpace(string(intentConfig.Provider)) == "" {
		return callbacks.IntentTraceData{}, fmt.Errorf("ai config unavailable")
	}
	intentCtx, cancel := context.WithTimeout(ctx, runtimeIntentDetectTimeout)
	defer cancel()
	intentCtx, usageCapture := usagex.WithCapture(intentCtx)
	chatModel, err := factory.NewChatModelFactory().Build(intentCtx, intentConfig)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	scope := resolveRuntimeIntentScope(req)
	profile := resolveRuntimeIntentProfile(scope)
	messages := []*schema.Message{
		schema.SystemMessage(runtimeIntentDetectSystemPromptForProfile(profile)),
		schema.UserMessage(buildRuntimeIntentDetectUserPrompt(req, history, configs)),
	}
	firstStartedAt := time.Now()
	firstReceiptOffset := len(usageCapture.Receipts())
	result, err := chatModel.Generate(intentCtx, messages)
	if err != nil {
		recordIntentModelUsage(req, intentConfig, credentialRevision, nil, gatewayReceiptSince(usageCapture, firstReceiptOffset), 1, time.Since(firstStartedAt).Milliseconds(), err)
		return callbacks.IntentTraceData{}, err
	}
	recordIntentModelUsage(req, intentConfig, credentialRevision, result, gatewayReceiptSince(usageCapture, firstReceiptOffset), 1, time.Since(firstStartedAt).Milliseconds(), nil)
	parsed, err := parseRuntimeIntentDetectJSON(result.Content)
	if err != nil {
		retryStartedAt := time.Now()
		retryReceiptOffset := len(usageCapture.Receipts())
		retry, retryErr := chatModel.Generate(intentCtx, append(messages, schema.SystemMessage("上一版 IntentDetect 输出不是合法 JSON。请重新输出严格 JSON。intentTasks 必须是数组，且是唯一事实来源；顶层 primaryIntent/needsKnowledge/needsResource/resourceActions 只能汇总 intentTasks。不要输出 Markdown、解释、注释或多余文本。")))
		if retryErr != nil {
			recordIntentModelUsage(req, intentConfig, credentialRevision, nil, gatewayReceiptSince(usageCapture, retryReceiptOffset), 2, time.Since(retryStartedAt).Milliseconds(), retryErr)
			return callbacks.IntentTraceData{}, fmt.Errorf("%w; retry failed: %v", err, retryErr)
		}
		recordIntentModelUsage(req, intentConfig, credentialRevision, retry, gatewayReceiptSince(usageCapture, retryReceiptOffset), 2, time.Since(retryStartedAt).Milliseconds(), nil)
		parsed, err = parseRuntimeIntentDetectJSON(retry.Content)
		if err != nil {
			return callbacks.IntentTraceData{}, err
		}
	}
	return callbacks.IntentTraceData{
		DetectedIntent:       parsed.PrimaryIntent,
		MatchedIntentCode:    parsed.PrimaryIntent,
		PrimaryIntent:        parsed.PrimaryIntent,
		SubIntent:            parsed.SubIntent,
		SecondaryIntents:     []string(parsed.SecondaryIntents),
		SecondaryIntentCodes: []string(parsed.SecondaryIntents),
		IntentConfidence:     parsed.Confidence,
		ShouldReply:          true,
		NeedsKnowledge:       parsed.NeedsKnowledge,
		NeedsTool:            parsed.NeedsTool,
		NeedsResource:        parsed.NeedsResource,
		NeedsHumanRoute:      parsed.NeedsHumanRoute,
		NeedsClarification:   parsed.NeedsClarification,
		ResourceType:         parsed.ResourceType,
		ResourceAction:       parsed.ResourceAction,
		ResourceActions:      []string(parsed.ResourceActions),
		IntentTasks:          convertRuntimeIntentTasks([]runtimeIntentTaskJSON(parsed.IntentTasks)),
		HumanRoutePolicy:     parsed.SubIntent,
		Reason:               strings.TrimSpace("model IntentDetect JSON: " + parsed.Reason),
	}, nil
}

func recordIntentModelUsage(req RunInput, aiConfig models.AIConfig, credentialRevision int64, message *schema.Message, receipt *usagex.Receipt, attempt int, latencyMS int64, callErr error) {
	requestID := strings.TrimSpace(req.UserMessage.RequestID)
	if requestID == "" {
		return
	}
	status := "completed"
	errorMessage := ""
	if callErr != nil {
		status = "failed"
		errorMessage = "model_call_failed"
	}
	event := models.AIUsageEvent{
		EventKey:       fmt.Sprintf("%s:intent_detect:%d", requestID, attempt),
		ConversationID: req.Conversation.ID, MessageID: req.UserMessage.ID, RequestID: requestID,
		Stage: "intent_detect", Provider: string(aiConfig.Provider), Model: aiConfig.ModelName,
		AIConfigID: aiConfig.ID, ModelSource: "intent_model_resolver", CredentialRevision: credentialRevision,
		MetricSource: services.AIUsageMetricSourceProviderOperation,
		LatencyMS:    latencyMS, Status: status, ErrorMessage: errorMessage,
	}
	if message != nil && message.ResponseMeta != nil && message.ResponseMeta.Usage != nil {
		usage := message.ResponseMeta.Usage
		event.PromptTokens = int64(usage.PromptTokens)
		event.CompletionTokens = int64(usage.CompletionTokens)
		event.CachedPromptTokens = int64(usage.PromptTokenDetails.CachedTokens)
		event.ReasoningTokens = int64(usage.CompletionTokensDetails.ReasoningTokens)
		event.MetricSource = services.AIUsageMetricSourceUpstreamActual
	}
	applyGatewayReceiptToUsageEvent(&event, receipt)
	_ = services.AIUsageEventService.Record(event)
}

func gatewayReceiptSince(capture *usagex.Capture, offset int) *usagex.Receipt {
	receipts := capture.Receipts()
	if offset < 0 || offset >= len(receipts) {
		return nil
	}
	receipt := receipts[len(receipts)-1]
	return &receipt
}

func applyGatewayReceiptToUsageEvent(event *models.AIUsageEvent, receipt *usagex.Receipt) {
	if event == nil || receipt == nil {
		return
	}
	event.Gateway = receipt.Gateway
	event.GatewayRequestID = receipt.RequestID
	event.GatewayUpstreamID = receipt.UpstreamRequestID
	event.CallStartedAt = &receipt.StartedAt
	event.CallFinishedAt = &receipt.FinishedAt
	if receipt.LatencyMS() > 0 {
		event.LatencyMS = receipt.LatencyMS()
	}
}

func convertRuntimeIntentTasks(tasks []runtimeIntentTaskJSON) []callbacks.IntentTaskTraceData {
	ret := make([]callbacks.IntentTaskTraceData, 0, len(tasks))
	for _, task := range tasks {
		intent := canonicalIntentCode(task.Intent)
		if intent == "" {
			continue
		}
		ret = append(ret, callbacks.IntentTaskTraceData{
			Intent:          intent,
			SubIntent:       strings.TrimSpace(task.SubIntent),
			Text:            strings.TrimSpace(task.Text),
			NeedsKnowledge:  task.NeedsKnowledge || intent == "hotel_info",
			NeedsResource:   task.NeedsResource || intent == "hotel_variable",
			NeedsTool:       task.NeedsTool,
			NeedsHumanRoute: task.NeedsHumanRoute || intent == "human_complaint_risk",
			ResourceAction:  strings.TrimSpace(task.ResourceAction),
			Reason:          strings.TrimSpace(task.Reason),
		})
	}
	return ret
}

func resolveRuntimeIntentDetectAIConfig(req RunInput) models.AIConfig {
	config, _, err := resolveRuntimeIntentDetectAIConfigWithRevision(req)
	if err != nil {
		return models.AIConfig{}
	}
	return config
}

func resolveRuntimeIntentDetectAIConfigWithRevision(req RunInput) (models.AIConfig, int64, error) {
	if sqls.DB() == nil {
		return models.AIConfig{}, 0, fmt.Errorf("database unavailable")
	}
	if req.Conversation.ID <= 0 {
		return models.AIConfig{}, 0, fmt.Errorf("conversation is required for intent model")
	}
	resolved, err := services.StoreAIModelSettingService.ResolveForConversation(req.Conversation.ID, services.StoreAIModelUsageIntentDetectLLM, 0)
	if err != nil {
		return models.AIConfig{}, 0, err
	}
	if resolved == nil {
		return models.AIConfig{}, 0, fmt.Errorf("intent model unavailable")
	}
	return resolved.Config, resolved.CredentialRevision, nil
}

func runtimeIntentDetectSystemPrompt() string {
	return runtimeIntentDetectSystemPromptForProfile(nil)
}

func runtimeIntentDetectSystemPromptForProfile(profile *models.ReplyIntentProfile) string {
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

func buildRuntimeIntentDetectUserPrompt(req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) string {
	var b strings.Builder
	currentText := strings.TrimSpace(req.UserMessage.Content)
	currentDisplayText := currentTurnDisplayText(currentText)
	b.WriteString("必须分类的当前消息:\n")
	b.WriteString(currentDisplayText)
	b.WriteString("\n\n当前消息类型: ")
	b.WriteString(string(req.UserMessage.MessageType))
	if timeLabel := adapter.RuntimeMessageTimeLabel(&req.UserMessage); timeLabel != "" {
		b.WriteString("\n当前消息时间: ")
		b.WriteString(timeLabel)
	}
	b.WriteString("\n\n判别纪律：只给“当前消息”分类；最近原始消息、媒体理解和长期记忆只用于解释“这个/刚才/还/继续/那”等指代。")
	b.WriteString("如果当前消息已经有独立的新主题，禁止沿用上一轮早餐、停车、投诉、安全、转人工等历史主题。")
	b.WriteString("但若紧邻的上一条 AI 客服消息正在追问一个业务问题的偏好、条件、范围或选项，当前短回答属于该业务的连续补充：必须继承该业务 intent/subIntent，并将 intentTasks[].text 写成包含上一轮业务主题和当前补充条件的完整检索问题。")
	b.WriteString("例如 AI 问附近餐饮口味、客户答‘麻辣口味的’，应输出 hotel_info/surrounding_facilities 且 needsKnowledge=true，任务文本可写‘附近餐饮推荐，偏好麻辣口味’。没有紧邻业务追问时，独立短语不得从更早历史强行继承旧主题。")
	b.WriteString("历史消息使用[历史消息][说话人][时间]格式，必须分清客户、AI客服、人工客服分别说了什么。")
	if instruction := buildAdjacentAIReplyRelationInstruction(history); instruction != "" {
		b.WriteString("\n\n")
		b.WriteString(instruction)
	}
	mediaContext := currentAndRecentMediaText(req, history)
	if mediaContext != "" {
		b.WriteString("\n\n上下文中的媒体理解:\n")
		b.WriteString(preview(mediaContext, 1200))
		b.WriteString("\n媒体解析结果只是上下文，不要输出单独的媒体类意图；是否使用它解释当前问题，由你根据当前消息语义判断。")
	}
	if len(history.RawItems) > 0 {
		b.WriteString("\n\n最近原始消息(低于当前消息优先级):\n")
		start := len(history.RawItems) - 8
		if start < 0 {
			start = 0
		}
		for _, item := range history.RawItems[start:] {
			text := adapter.RuntimeHistoryMessageContent(&item)
			if text != "" {
				b.WriteString("- ")
				b.WriteString(preview(text, 160))
				b.WriteString("\n")
			}
		}
	}
	if strings.TrimSpace(history.MemorySource) != "" {
		b.WriteString("\n长期记忆摘要(最低优先级，房号等一次性入住事实不能当当前事实):\n")
		b.WriteString(preview(history.MemorySource, 800))
	}
	if len(configs) > 0 {
		b.WriteString("\n\n启用的分类配置(用于理解分类含义，不要按关键词机械匹配):\n")
		for _, cfg := range collapseIntentConfigsByScope(configs) {
			b.WriteString("- code=")
			b.WriteString(canonicalIntentCode(cfg.Code))
			b.WriteString(" name=")
			b.WriteString(strings.TrimSpace(cfg.Name))
			if desc := strings.TrimSpace(cfg.Description); desc != "" {
				b.WriteString(" desc=")
				b.WriteString(preview(desc, 80))
			}
			b.WriteString("\n")
		}
	}
	if currentText != "" {
		b.WriteString("\n再次强调，最终 JSON 必须只分类这条当前消息：\n")
		b.WriteString(currentDisplayText)
		b.WriteString("\n")
	}
	b.WriteString("\n请输出严格 JSON。")
	return b.String()
}

func buildAdjacentAIReplyRelationInstruction(history adapter.HistoryBuildResult) string {
	aiReply, ok := immediatelyPreviousAIReply(history)
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString("【上一答复关系判断（仅本轮启用）】\n")
	b.WriteString("紧邻上一条历史消息确为 AI 客服答复。必须结合此前客户原问题、这条紧邻 AI 答复和当前客户消息，判断客户是在继续业务，还是明确拒绝了上一答复；不能按‘不是、为什么、真的吗’等单个词机械匹配。\n")
	if customerQuestion := customerMessageBeforeAdjacentAIReply(history); customerQuestion != "" {
		b.WriteString("此前客户原问题：")
		b.WriteString(preview(customerQuestion, 240))
		b.WriteString("\n")
	}
	b.WriteString("紧邻 AI 客服答复：")
	b.WriteString(preview(aiReply, 240))
	b.WriteString("\n")
	b.WriteString("关系标签只用于内部判断，不增加任何 JSON 字段：new_topic、normal_follow_up、clarification_answer、accepted、not_understood、answer_rejected、answer_contradicted、answer_unresolved。\n")
	b.WriteString("只有以下语义关系输出 human_complaint_risk + answer_rejected，且 needsHumanRoute=true：客户明确否定上一答复；指出 AI 前后矛盾；指出答非所问并重申同一个问题；同一问题再次追问且上一答复仍未解决；拒绝 AI 给出的能力边界方案并要求无法满足的例外；引用真人客服说法或现场事实反驳上一答复。\n")
	b.WriteString("以下不得输出 answer_rejected：提出独立新问题；正常补充收费、时间、支付等细节；正常回答 AI 刚才追问的房号、偏好、条件或选项；孤立的‘真的吗/为什么’但没有明确否定或矛盾；与上一业务答复无关的不满、吐槽或闲聊。此时按当前真实业务意图继续分类。")
	return b.String()
}

func hasImmediatelyPreviousAIReply(history adapter.HistoryBuildResult) bool {
	_, ok := immediatelyPreviousAIReply(history)
	return ok
}

func immediatelyPreviousAIReply(history adapter.HistoryBuildResult) (string, bool) {
	if history.LatestRawItem != nil {
		item := *history.LatestRawItem
		if item.SenderType != enums.IMSenderTypeAI {
			return "", false
		}
		content := strings.TrimSpace(adapter.RuntimeHistoryMessageContent(&item))
		if content == "" {
			return "", false
		}
		return content, true
	}
	if len(history.RawItems) == 0 {
		return "", false
	}
	item := history.RawItems[len(history.RawItems)-1]
	if item.SenderType != enums.IMSenderTypeAI {
		return "", false
	}
	content := strings.TrimSpace(adapter.RuntimeHistoryMessageContent(&item))
	if content == "" {
		return "", false
	}
	return content, true
}

func customerMessageBeforeAdjacentAIReply(history adapter.HistoryBuildResult) string {
	if !hasImmediatelyPreviousAIReply(history) {
		return ""
	}
	for i := len(history.RawItems) - 2; i >= 0; i-- {
		item := history.RawItems[i]
		if item.SenderType != enums.IMSenderTypeCustomer {
			continue
		}
		if content := strings.TrimSpace(adapter.RuntimeHistoryMessageContent(&item)); content != "" {
			return content
		}
	}
	return ""
}

func parseRuntimeIntentDetectJSON(content string) (runtimeIntentDetectJSON, error) {
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
	var parsed runtimeIntentDetectJSON
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return parsed, err
	}
	return parsed, nil
}

func normalizeModelIntentTrace(intent callbacks.IntentTraceData, req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) callbacks.IntentTraceData {
	intent.PrimaryIntent = canonicalIntentCode(intent.PrimaryIntent)
	if intent.PrimaryIntent == "" {
		intent.PrimaryIntent = canonicalIntentCode(intent.MatchedIntentCode)
	}
	if intent.PrimaryIntent == "" || !isRuntimeTopLevelIntent(intent.PrimaryIntent) {
		intent.PrimaryIntent = "interaction"
		intent.MatchedIntentCode = "interaction"
		intent.SubIntent = "clarify"
		intent.NeedsClarification = true
		intent.NeedsKnowledge = false
		intent.NeedsResource = false
		intent.NeedsHumanRoute = false
	}
	intent.MatchedIntentCode = intent.PrimaryIntent
	intent.ShouldReply = true
	if intent.IntentConfidence <= 0 || intent.IntentConfidence > 1 {
		intent.IntentConfidence = 0.65
	}
	if intent.IntentConfidence < 0.45 && intent.PrimaryIntent != "human_complaint_risk" {
		intent.PrimaryIntent = "interaction"
		intent.MatchedIntentCode = "interaction"
		intent.SubIntent = "clarify"
		intent.NeedsClarification = true
		intent.NeedsKnowledge = false
		intent.NeedsResource = false
		intent.NeedsHumanRoute = false
	}
	intent.IntentTasks = normalizeRuntimeIntentTasks(intent.IntentTasks)
	intent = enforceAnswerRejectedAdjacency(intent, history)
	intent = deriveModelIntentFromTasks(intent)
	if intentHasHotelVariableTask(intent) {
		intent.ResourceActions = normalizeHotelVariableResourceActions(intent.ResourceActions, intent.ResourceAction, intent.ResourceType, intent.SubIntent, intent.IntentTasks)
	}
	switch intent.PrimaryIntent {
	case "hotel_info":
		intent.NeedsKnowledge = true
		intent.NeedsResource = len(intent.ResourceActions) > 0
		intent.NeedsHumanRoute = false
		if strings.TrimSpace(intent.SubIntent) == "" {
			intent.SubIntent = "store_knowledge"
		}
	case "hotel_variable":
		intent.NeedsResource = true
		intent.NeedsHumanRoute = false
		intent.ResourceActions = normalizeHotelVariableResourceActions(intent.ResourceActions, intent.ResourceAction, intent.ResourceType, intent.SubIntent, intent.IntentTasks)
		resourceAction := ""
		if len(intent.ResourceActions) > 0 {
			resourceAction = intent.ResourceActions[0]
		}
		resourceType, resourceAction := normalizeHotelVariableResourceAction(resourceAction, intent.ResourceType, intent.SubIntent)
		intent.ResourceType = resourceType
		intent.ResourceAction = resourceAction
		if len(intent.ResourceActions) == 0 && resourceAction != "" {
			intent.ResourceActions = []string{resourceAction}
		}
		if resourceType != "" && resourceType != "store_variable" {
			intent.SubIntent = resourceType
		} else if intent.SubIntent == "" {
			intent.SubIntent = resourceType
		}
		if intentHasMixedHotelInfoTask(intent) {
			intent.NeedsKnowledge = true
		} else {
			intent.NeedsKnowledge = false
		}
	case "service_request":
		intent.NeedsKnowledge = true
		intent.NeedsResource = len(intent.ResourceActions) > 0
		intent = enforceHumanRouteFlagByIntentCategory(intent)
	case "interaction":
		intent.NeedsKnowledge = false
		intent.NeedsResource = len(intent.ResourceActions) > 0
		intent.NeedsHumanRoute = false
		if strings.TrimSpace(intent.SubIntent) == "" {
			intent.SubIntent = "chat"
		}
		if intent.NeedsClarification && strings.TrimSpace(intent.SubIntent) == "chat" {
			intent.SubIntent = "clarify"
		}
		if strings.TrimSpace(intent.SubIntent) == "weather_query" || strings.TrimSpace(intent.ResourceAction) == "get_weather" {
			intent.NeedsClarification = false
			intent.NeedsKnowledge = false
			intent.NeedsTool = true
			intent.NeedsResource = false
			intent.NeedsHumanRoute = false
			intent.ResourceAction = "get_weather"
			intent.ToolCodes = appendIfMissing(intent.ToolCodes, toolx.BuiltinWeather.Code)
		}
	case "human_complaint_risk":
		intent.NeedsKnowledge = false
		intent.NeedsResource = false
		intent.NeedsHumanRoute = true
		if intent.SubIntent == "emergency_safety" {
			intent.HumanRoutePolicy = "emergency_safety"
		} else {
			if strings.TrimSpace(intent.SubIntent) == "" {
				intent.SubIntent = "explicit_handoff"
			}
			intent.HumanRoutePolicy = "managed_mode"
		}
	}
	if shouldAttachCheckinMiniProgramTask(intent) {
		intent = ensureCheckinProcessMiniProgramTask(intent, req)
	}
	if len(intent.ResourceActions) > 0 && intent.PrimaryIntent != "human_complaint_risk" {
		intent.NeedsResource = true
		if strings.TrimSpace(intent.ResourceAction) == "" {
			intent.ResourceAction = intent.ResourceActions[0]
		}
		resourceType, resourceAction := normalizeHotelVariableResourceAction(intent.ResourceAction, intent.ResourceType, intent.SubIntent)
		intent.ResourceType = resourceType
		intent.ResourceAction = resourceAction
		if intent.PrimaryIntent == "hotel_variable" && resourceType != "" && resourceType != "store_variable" {
			intent.SubIntent = resourceType
		}
	}
	if intentHasMixedHotelInfoTask(intent) && intent.PrimaryIntent != "human_complaint_risk" {
		intent.NeedsKnowledge = true
	}
	intent = enforceHumanRouteFlagByIntentCategory(intent)
	if intent.DetectedIntent == "" {
		intent.DetectedIntent = intent.PrimaryIntent
	}
	if config, ok := findIntentConfigByCode(configs, intent.PrimaryIntent); ok {
		intent.MatchedConfigID = config.ID
		intent.MatchedConfig = strings.TrimSpace(config.Name)
		intent.MatchMode = "model"
	}
	return intent
}

func enforceAnswerRejectedAdjacency(intent callbacks.IntentTraceData, history adapter.HistoryBuildResult) callbacks.IntentTraceData {
	if hasImmediatelyPreviousAIReply(history) {
		return intent
	}
	changedTask := false
	for i := range intent.IntentTasks {
		task := &intent.IntentTasks[i]
		if task.Intent != "human_complaint_risk" || strings.TrimSpace(task.SubIntent) != "answer_rejected" {
			continue
		}
		task.Intent = "interaction"
		task.SubIntent = "frustration"
		task.NeedsHumanRoute = false
		task.NeedsKnowledge = false
		task.NeedsResource = false
		task.NeedsTool = false
		task.ResourceAction = ""
		changedTask = true
	}
	if canonicalIntentCode(intent.PrimaryIntent) != "human_complaint_risk" || strings.TrimSpace(intent.SubIntent) != "answer_rejected" {
		if changedTask {
			intent.Reason = appendIntentReason(intent.Reason, "answer_rejected ignored: immediately previous message is not an AI reply")
		}
		return intent
	}
	intent.PrimaryIntent = "interaction"
	intent.MatchedIntentCode = "interaction"
	if len(intent.IntentTasks) > 0 {
		intent.SubIntent = ""
	} else {
		intent.SubIntent = "frustration"
	}
	intent.NeedsHumanRoute = false
	intent.HumanRoutePolicy = ""
	intent.NeedsKnowledge = false
	intent.NeedsResource = false
	intent.NeedsTool = false
	intent.ResourceAction = ""
	intent.ResourceActions = nil
	intent.Reason = appendIntentReason(intent.Reason, "answer_rejected ignored: immediately previous message is not an AI reply")
	return intent
}

func deriveModelIntentFromTasks(intent callbacks.IntentTraceData) callbacks.IntentTraceData {
	if len(intent.IntentTasks) == 0 {
		return intent
	}
	primary := ""
	hasHuman := false
	hasVariable := false
	hasCheckinKnowledge := false
	hasKnowledge := false
	hasResource := false
	resourceActions := make([]string, 0)
	for i := range intent.IntentTasks {
		task := &intent.IntentTasks[i]
		task.Intent = canonicalIntentCode(task.Intent)
		if task.Intent == "" || !isRuntimeTopLevelIntent(task.Intent) {
			task.Intent = "interaction"
		}
		switch task.Intent {
		case "human_complaint_risk":
			hasHuman = true
			task.NeedsHumanRoute = true
		case "hotel_variable":
			hasVariable = true
			hasResource = true
			task.NeedsResource = true
			_, task.ResourceAction = normalizeHotelVariableResourceAction(task.ResourceAction, "", task.SubIntent)
			if task.ResourceAction != "" && task.ResourceAction != "provide_store_variable" {
				resourceActions = appendIfMissing(resourceActions, task.ResourceAction)
			}
		case "hotel_info":
			hasKnowledge = true
			task.NeedsKnowledge = true
			if isCheckinProcessSubIntent(task.SubIntent) {
				hasCheckinKnowledge = true
			}
		}
		if task.NeedsKnowledge {
			hasKnowledge = true
		}
		if task.NeedsResource {
			hasResource = true
		}
	}
	if hasHuman {
		primary = "human_complaint_risk"
	} else if hasCheckinKnowledge {
		primary = "hotel_info"
	} else if hasVariable {
		primary = "hotel_variable"
	} else {
		for _, task := range intent.IntentTasks {
			if task.Intent != "interaction" {
				primary = task.Intent
				break
			}
		}
	}
	if primary == "" && len(intent.IntentTasks) > 0 {
		primary = intent.IntentTasks[0].Intent
	}
	if primary == "" {
		primary = "interaction"
	}
	secondary := make([]string, 0)
	for _, task := range intent.IntentTasks {
		if task.Intent != primary {
			secondary = appendIfMissing(secondary, task.Intent)
		}
	}
	if intent.PrimaryIntent != "" && intent.PrimaryIntent != primary {
		intent.Reason = appendIntentReason(intent.Reason, "primaryIntent derived from intentTasks")
	}
	intent.PrimaryIntent = primary
	intent.MatchedIntentCode = primary
	intent.SecondaryIntents = mergeStringLists(secondary, intent.SecondaryIntents)
	intent.SecondaryIntentCodes = mergeStringLists(secondary, intent.SecondaryIntentCodes)
	intent.NeedsKnowledge = hasKnowledge
	intent.NeedsResource = hasResource
	intent.NeedsHumanRoute = hasHuman
	if len(resourceActions) > 0 {
		intent.ResourceActions = resourceActions
		intent.ResourceAction = resourceActions[0]
		intent.ResourceType, intent.ResourceAction = normalizeHotelVariableResourceAction(intent.ResourceAction, intent.ResourceType, intent.SubIntent)
		if intent.ResourceType != "" && intent.PrimaryIntent == "hotel_variable" {
			intent.SubIntent = intent.ResourceType
		}
	}
	if strings.TrimSpace(intent.SubIntent) == "" || intent.PrimaryIntent == "hotel_variable" && intent.SubIntent == "store_variable" {
		if subIntent := firstTaskSubIntentForPrimary(intent.IntentTasks, intent.PrimaryIntent); subIntent != "" {
			intent.SubIntent = subIntent
		}
	}
	return intent
}

func firstTaskSubIntentForPrimary(tasks []callbacks.IntentTaskTraceData, primary string) string {
	for _, task := range tasks {
		if task.Intent == primary && strings.TrimSpace(task.SubIntent) != "" {
			return strings.TrimSpace(task.SubIntent)
		}
	}
	for _, task := range tasks {
		if strings.TrimSpace(task.SubIntent) != "" {
			return strings.TrimSpace(task.SubIntent)
		}
	}
	return ""
}

func mergeStringLists(first []string, second []string) []string {
	ret := make([]string, 0, len(first)+len(second))
	for _, item := range first {
		ret = appendIfMissing(ret, strings.TrimSpace(item))
	}
	for _, item := range second {
		ret = appendIfMissing(ret, strings.TrimSpace(item))
	}
	return ret
}

func intentDetectUnavailableIntent(reason string) callbacks.IntentTraceData {
	return callbacks.IntentTraceData{
		DetectedIntent:     "intent_detect_unavailable",
		IntentConfidence:   0.35,
		ShouldReply:        true,
		NeedsClarification: false,
		NeedsKnowledge:     false,
		NeedsResource:      false,
		NeedsHumanRoute:    false,
		Reason:             strings.TrimSpace(reason),
	}
}

func enforceHumanRouteFlagByIntentCategory(intent callbacks.IntentTraceData) callbacks.IntentTraceData {
	if isHandoffIntentCategory(intent) {
		return intent
	}
	if intent.NeedsHumanRoute || strings.TrimSpace(intent.HumanRoutePolicy) != "" {
		intent.Reason = appendIntentReason(intent.Reason, "human route ignored: handoff confirmation only belongs to human_complaint_risk intent category")
	}
	intent.NeedsHumanRoute = false
	intent.HumanRoutePolicy = ""
	return intent
}

func appendIntentReason(current string, addition string) string {
	current = strings.TrimSpace(current)
	addition = strings.TrimSpace(addition)
	if current == "" {
		return addition
	}
	if addition == "" || strings.Contains(current, addition) {
		return current
	}
	return current + "; " + addition
}

func intentHasMixedHotelInfoTask(intent callbacks.IntentTraceData) bool {
	for _, task := range intent.IntentTasks {
		if task.Intent == "hotel_info" || task.NeedsKnowledge {
			return true
		}
	}
	return false
}

func shouldAttachCheckinMiniProgramTask(intent callbacks.IntentTraceData) bool {
	if intent.PrimaryIntent != "hotel_info" {
		return false
	}
	if isCheckinProcessSubIntent(intent.SubIntent) {
		return true
	}
	for _, task := range intent.IntentTasks {
		if task.Intent == "hotel_info" && isCheckinProcessSubIntent(task.SubIntent) {
			return true
		}
	}
	return false
}

func isCheckinProcessSubIntent(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "checkin_process", "check_in", "checkin", "check_in_process", "checkin_steps", "check_in_steps", "checkin_guide":
		return true
	default:
		return false
	}
}

func ensureCheckinProcessMiniProgramTask(intent callbacks.IntentTraceData, req RunInput) callbacks.IntentTraceData {
	intent.NeedsKnowledge = true
	intent.NeedsResource = true
	intent.ResourceActions = normalizeHotelVariableResourceActions(append(intent.ResourceActions, "provide_mini_program"), intent.ResourceAction, intent.ResourceType, intent.SubIntent, intent.IntentTasks)
	if strings.TrimSpace(intent.ResourceAction) == "" {
		intent.ResourceAction = "provide_mini_program"
	}
	if strings.TrimSpace(intent.SubIntent) == "" || intent.SubIntent == "check_in" || intent.SubIntent == "checkin" {
		intent.SubIntent = "checkin_process"
	}
	currentText := strings.TrimSpace(currentTurnDisplayText(req.UserMessage.Content))
	if currentText == "" {
		currentText = "办理入住"
	}
	hasKnowledgeTask := false
	hasMiniProgramTask := false
	for i := range intent.IntentTasks {
		if intent.IntentTasks[i].Intent == "hotel_info" && isCheckinProcessSubIntent(intent.IntentTasks[i].SubIntent) {
			intent.IntentTasks[i].SubIntent = "checkin_process"
			intent.IntentTasks[i].NeedsKnowledge = true
			if strings.TrimSpace(intent.IntentTasks[i].Text) == "" {
				intent.IntentTasks[i].Text = currentText
			}
			hasKnowledgeTask = true
		}
		if intent.IntentTasks[i].Intent == "hotel_variable" && strings.TrimSpace(intent.IntentTasks[i].ResourceAction) == "provide_mini_program" {
			intent.IntentTasks[i].SubIntent = "mini_program"
			intent.IntentTasks[i].NeedsResource = true
			if strings.TrimSpace(intent.IntentTasks[i].Text) == "" {
				intent.IntentTasks[i].Text = "发送入住小程序入口"
			}
			hasMiniProgramTask = true
		}
	}
	if !hasKnowledgeTask {
		intent.IntentTasks = append([]callbacks.IntentTaskTraceData{{
			Intent:         "hotel_info",
			SubIntent:      "checkin_process",
			Text:           currentText,
			NeedsKnowledge: true,
			Reason:         "checkin process needs knowledge tutorial",
		}}, intent.IntentTasks...)
	}
	if !hasMiniProgramTask {
		intent.IntentTasks = append(intent.IntentTasks, callbacks.IntentTaskTraceData{
			Intent:         "hotel_variable",
			SubIntent:      "mini_program",
			Text:           "发送入住小程序入口",
			NeedsResource:  true,
			ResourceAction: "provide_mini_program",
			Reason:         "checkin process should also provide configured mini program entry",
		})
	}
	intent.Reason = appendIntentReason(intent.Reason, "checkin_process attached mini program resource action")
	return intent
}

func promptForModelDetectedIntent(intent callbacks.IntentTraceData, configs []models.ReplyIntentConfig) callbacks.IntentPromptTraceData {
	if config, ok := findIntentConfigByCode(configs, intent.PrimaryIntent); ok {
		return appendSpatialFactInstruction(promptTraceFromConfig(config, intent), intent)
	}
	return appendSpatialFactInstruction(selectIntentPromptPack(intent), intent)
}

func findIntentConfigByCode(configs []models.ReplyIntentConfig, code string) (models.ReplyIntentConfig, bool) {
	code = canonicalIntentCode(code)
	for _, item := range collapseIntentConfigsByScope(configs) {
		if canonicalIntentCode(item.Code) == code {
			return item, true
		}
	}
	return models.ReplyIntentConfig{}, false
}

func isRuntimeTopLevelIntent(code string) bool {
	switch code {
	case "hotel_info", "service_request", "human_complaint_risk", "interaction", "hotel_variable":
		return true
	default:
		return false
	}
}

func normalizeHotelVariableResourceAction(action string, resourceType string, subIntent string) (string, string) {
	action = strings.TrimSpace(action)
	switch action {
	case "provide_phone":
		return "phone", action
	case "provide_location":
		return "location", action
	case "send_miniprogram", "provide_mini_program":
		return "mini_program", "provide_mini_program"
	case "provide_store_group":
		return "store_group", action
	}
	resourceType = normalizeHotelVariableResourceType(resourceType)
	if resourceType == "" {
		resourceType = normalizeHotelVariableResourceType(subIntent)
	}
	switch resourceType {
	case "phone":
		return resourceType, "provide_phone"
	case "location":
		return resourceType, "provide_location"
	case "mini_program":
		return resourceType, "provide_mini_program"
	default:
		return "store_variable", "provide_store_variable"
	}
}

func normalizeHotelVariableResourceActions(actions []string, action string, resourceType string, subIntent string, tasks []callbacks.IntentTaskTraceData) []string {
	ret := make([]string, 0, len(actions)+1)
	add := func(value string) {
		_, normalized := normalizeHotelVariableResourceAction(value, "", "")
		if normalized == "provide_store_variable" {
			return
		}
		ret = appendIfMissing(ret, normalized)
	}
	for _, item := range actions {
		add(item)
	}
	for _, task := range tasks {
		if task.Intent == "hotel_variable" || task.NeedsResource {
			add(task.ResourceAction)
		}
	}
	add(action)
	if len(ret) == 0 {
		_, normalized := normalizeHotelVariableResourceAction("", resourceType, subIntent)
		if normalized != "provide_store_variable" {
			ret = appendIfMissing(ret, normalized)
		}
	}
	return ret
}

func normalizeRuntimeIntentTasks(tasks []callbacks.IntentTaskTraceData) []callbacks.IntentTaskTraceData {
	ret := make([]callbacks.IntentTaskTraceData, 0, len(tasks))
	for _, task := range tasks {
		task.Intent = canonicalIntentCode(task.Intent)
		task.SubIntent = strings.TrimSpace(task.SubIntent)
		task.Text = strings.TrimSpace(task.Text)
		task.ResourceAction = strings.TrimSpace(task.ResourceAction)
		task.Reason = strings.TrimSpace(task.Reason)
		if task.Intent == "" {
			continue
		}
		if task.Intent == "hotel_info" {
			task.NeedsKnowledge = true
		}
		if task.Intent == "hotel_variable" {
			task.NeedsResource = true
			_, task.ResourceAction = normalizeHotelVariableResourceAction(task.ResourceAction, "", task.SubIntent)
		}
		if task.Intent == "human_complaint_risk" {
			task.NeedsHumanRoute = true
		}
		ret = append(ret, task)
	}
	return ret
}

func intentHasHotelVariableTask(intent callbacks.IntentTraceData) bool {
	for _, task := range intent.IntentTasks {
		if task.Intent == "hotel_variable" || task.NeedsResource {
			return true
		}
	}
	return len(intent.ResourceActions) > 0 || strings.TrimSpace(intent.ResourceAction) != "" || normalizeHotelVariableResourceType(intent.ResourceType) != ""
}

func normalizeHotelVariableResourceType(resourceType string) string {
	switch strings.TrimSpace(resourceType) {
	case "phone", "contact_phone", "store_phone":
		return "phone"
	case "location", "address", "navigation", "store_location":
		return "location"
	case "mini_program", "miniprogram", "miniProgram", "checkin_miniprogram", "send_miniprogram":
		return "mini_program"
	case "store_group", "room_group":
		return "store_group"
	default:
		return strings.TrimSpace(resourceType)
	}
}
