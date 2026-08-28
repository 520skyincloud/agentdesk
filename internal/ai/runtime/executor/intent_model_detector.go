package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"agent-desk/internal/pkg/utils"
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
	Intent             string                     `json:"intent"`
	SubIntent          string                     `json:"subIntent"`
	Objective          string                     `json:"objective"`
	RelationToPrevious string                     `json:"relationToPrevious"`
	ResolutionState    string                     `json:"resolutionState"`
	Entities           runtimeIntentEntityList    `json:"entities"`
	Text               string                     `json:"text"`
	ResolvedText       string                     `json:"resolvedText"`
	SourceRefs         runtimeIntentSourceRefList `json:"sourceRefs"`
	NeedsKnowledge     bool                       `json:"needsKnowledge"`
	NeedsResource      bool                       `json:"needsResource"`
	NeedsTool          bool                       `json:"needsTool"`
	NeedsHumanRoute    bool                       `json:"needsHumanRoute"`
	ResourceAction     string                     `json:"resourceAction"`
	Reason             string                     `json:"reason"`
}

type runtimeIntentEntityJSON struct {
	Text string `json:"text"`
	Type string `json:"type"`
}

type runtimeIntentEntityList []runtimeIntentEntityJSON

type runtimeIntentStringList []string

type runtimeIntentSourceRefList []string

func (list *runtimeIntentEntityList) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" || trimmed == "false" || trimmed == "true" {
		*list = nil
		return nil
	}
	var rawItems []any
	if err := json.Unmarshal(data, &rawItems); err != nil {
		var single any
		if singleErr := json.Unmarshal(data, &single); singleErr != nil {
			return err
		}
		rawItems = []any{single}
	}
	entities := make([]runtimeIntentEntityJSON, 0, len(rawItems))
	for _, item := range rawItems {
		entity := runtimeIntentEntityJSON{Type: "other"}
		switch typed := item.(type) {
		case string:
			entity.Text = strings.TrimSpace(typed)
		case map[string]any:
			if text, ok := typed["text"].(string); ok {
				entity.Text = strings.TrimSpace(text)
			}
			if entityType, ok := typed["type"].(string); ok && strings.TrimSpace(entityType) != "" {
				entity.Type = strings.TrimSpace(entityType)
			}
		}
		if entity.Text != "" {
			entities = append(entities, entity)
		}
	}
	*list = entities
	return nil
}

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

func (list *runtimeIntentSourceRefList) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" || trimmed == "false" || trimmed == "true" {
		*list = nil
		return nil
	}
	var rawItems []any
	if err := json.Unmarshal(data, &rawItems); err != nil {
		var single string
		if singleErr := json.Unmarshal(data, &single); singleErr != nil {
			return err
		}
		rawItems = []any{single}
	}
	refs := make([]string, 0, len(rawItems))
	for _, item := range rawItems {
		ref := ""
		switch typed := item.(type) {
		case string:
			ref = typed
		case map[string]any:
			ref, _ = typed["ref"].(string)
		}
		ref = strings.TrimSpace(ref)
		if ref != "" {
			refs = appendIfMissing(refs, ref)
		}
	}
	*list = refs
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
	if err == nil {
		applyLegacyRuntimeIntentProtocolDefaults(&parsed, profile)
		err = validateRuntimeIntentDetectProtocol(parsed, profile, currentRuntimeIntentSemanticText(req))
	}
	if err != nil {
		retryStartedAt := time.Now()
		retryReceiptOffset := len(usageCapture.Receipts())
		repairInstruction := "上一版 IntentDetect 输出不满足当前 JSON 协议：" + preview(err.Error(), 240) + "。请严格按照当前系统消息声明的字段重新输出完整 JSON；intentTasks 必须从头到尾覆盖当前轮每个原子问题，且是唯一事实来源。每个任务都必须输出 text、resolvedText、sourceRefs、objective、relationToPrevious、resolutionState 和 entities；顶层字段只能汇总 intentTasks。不要输出 Markdown、解释、注释、未声明字段或 JSON 外文本。"
		retry, retryErr := chatModel.Generate(intentCtx, append(messages, schema.SystemMessage(repairInstruction)))
		if retryErr != nil {
			recordIntentModelUsage(req, intentConfig, credentialRevision, nil, gatewayReceiptSince(usageCapture, retryReceiptOffset), 2, time.Since(retryStartedAt).Milliseconds(), retryErr)
			return callbacks.IntentTraceData{}, fmt.Errorf("%w; retry failed: %v", err, retryErr)
		}
		recordIntentModelUsage(req, intentConfig, credentialRevision, retry, gatewayReceiptSince(usageCapture, retryReceiptOffset), 2, time.Since(retryStartedAt).Milliseconds(), nil)
		parsed, err = parseRuntimeIntentDetectJSON(retry.Content)
		if err == nil {
			applyLegacyRuntimeIntentProtocolDefaults(&parsed, profile)
			err = validateRuntimeIntentDetectProtocol(parsed, profile, currentRuntimeIntentSemanticText(req))
		}
		if err != nil {
			return callbacks.IntentTraceData{}, err
		}
	}
	sourceRefsValidated := runtimeIntentProfileExpectsSourceRefs(profile) &&
		len(currentTurnIntentSourceTexts(currentRuntimeIntentSemanticText(req))) > 0
	return callbacks.IntentTraceData{
		DetectedIntent:           parsed.PrimaryIntent,
		MatchedIntentCode:        parsed.PrimaryIntent,
		PrimaryIntent:            parsed.PrimaryIntent,
		SubIntent:                parsed.SubIntent,
		SecondaryIntents:         []string(parsed.SecondaryIntents),
		SecondaryIntentCodes:     []string(parsed.SecondaryIntents),
		IntentConfidence:         parsed.Confidence,
		ShouldReply:              true,
		NeedsKnowledge:           parsed.NeedsKnowledge,
		NeedsTool:                parsed.NeedsTool,
		NeedsResource:            parsed.NeedsResource,
		NeedsHumanRoute:          parsed.NeedsHumanRoute,
		NeedsClarification:       parsed.NeedsClarification,
		ResourceType:             parsed.ResourceType,
		ResourceAction:           parsed.ResourceAction,
		ResourceActions:          []string(parsed.ResourceActions),
		IntentTasks:              convertRuntimeIntentTasks([]runtimeIntentTaskJSON(parsed.IntentTasks)),
		SemanticContractExpected: runtimeIntentProfileExpectsTaskSemantics(profile),
		SourceRefsValidated:      sourceRefsValidated,
		HumanRoutePolicy:         parsed.SubIntent,
		Reason:                   strings.TrimSpace("model IntentDetect JSON: " + parsed.Reason),
	}, nil
}

func buildRuntimeIntentProtocolRepairContext(history adapter.HistoryBuildResult) runtimeIntentProtocolRepairContext {
	adjacentAIReply, ok := immediatelyPreviousAIReply(history)
	if !ok {
		return runtimeIntentProtocolRepairContext{}
	}
	return runtimeIntentProtocolRepairContext{
		AdjacentAIReply:      adjacentAIReply,
		PreviousCustomerText: customerMessageBeforeAdjacentAIReply(history),
	}
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
		resolvedText := strings.TrimSpace(task.ResolvedText)
		if resolvedText == "" {
			resolvedText = strings.TrimSpace(task.Text)
		}
		ret = append(ret, callbacks.IntentTaskTraceData{
			Intent:             intent,
			SubIntent:          strings.TrimSpace(task.SubIntent),
			Objective:          semanticGateNormalizeObjective(task.Objective),
			RelationToPrevious: semanticGateNormalizeRelation(task.RelationToPrevious),
			ResolutionState:    semanticGateNormalizeResolution(task.ResolutionState),
			Entities:           convertRuntimeIntentEntities(task.Entities),
			Text:               strings.TrimSpace(task.Text),
			ResolvedText:       resolvedText,
			SourceRefs:         normalizeRuntimeIntentSourceRefs([]string(task.SourceRefs)),
			NeedsKnowledge:     task.NeedsKnowledge || intent == "hotel_info",
			NeedsResource:      task.NeedsResource || intent == "hotel_variable",
			NeedsTool:          task.NeedsTool,
			NeedsHumanRoute:    task.NeedsHumanRoute || intent == "human_complaint_risk",
			ResourceAction:     strings.TrimSpace(task.ResourceAction),
			Reason:             strings.TrimSpace(task.Reason),
		})
	}
	return ret
}

func convertRuntimeIntentEntities(entities []runtimeIntentEntityJSON) []callbacks.IntentEntityTraceData {
	ret := make([]callbacks.IntentEntityTraceData, 0, len(entities))
	for _, entity := range entities {
		text := strings.TrimSpace(entity.Text)
		if text == "" {
			continue
		}
		entityType := semanticGateNormalizeValue(entity.Type)
		if !isRuntimeIntentEntityType(entityType) {
			entityType = "other"
		}
		ret = append(ret, callbacks.IntentEntityTraceData{Text: text, Type: entityType})
	}
	return ret
}

func isRuntimeIntentEntityType(entityType string) bool {
	switch strings.TrimSpace(entityType) {
	case "facility", "supply", "room_type", "room", "service", "location", "order", "resource", "person", "company", "other":
		return true
	default:
		return false
	}
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

func runtimeIntentProfileExpectsTaskSemantics(profile *models.ReplyIntentProfile) bool {
	schemaText := ""
	if profile != nil {
		schemaText = strings.TrimSpace(profile.IntentJSONSchema)
	}
	if schemaText == "" {
		schemaText = replyintent.DefaultHotelIntentJSONSchema()
	}
	for _, field := range []string{`"objective"`, `"relationToPrevious"`, `"resolutionState"`, `"entities"`} {
		if !strings.Contains(schemaText, field) {
			return false
		}
	}
	return true
}

func buildRuntimeIntentDetectUserPrompt(req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) string {
	var b strings.Builder
	currentText := currentRuntimeIntentSemanticText(req)
	currentDisplayText := currentText
	sourceTexts := currentTurnIntentSourceTexts(currentDisplayText)
	if len(sourceTexts) > 0 {
		b.WriteString("必须分类的当前消息（按来源顺序）:\n[CURRENT_TURN_SOURCE_REFS]\n")
		for index, text := range sourceTexts {
			b.WriteString(fmt.Sprintf("U%d: %s\n", index+1, text))
		}
		b.WriteString("每个 intentTasks 项都要输出 sourceRefs；sourceRefs[0] 是该任务的主要问题来源，其余是被该任务共同消化的相邻上下文。只能引用上面列出的 URef。")
	} else {
		b.WriteString("必须分类的当前消息:\n")
		b.WriteString(currentDisplayText)
	}
	b.WriteString("\n\n【当前轮逐题识别】你必须自己逐条扫描 U1 到 Un；每条消息都可能包含 0 个、1 个或多个任务，任务数量和边界只能由你根据完整语义判断，不能依赖标点、换行、空格或固定连接词，因为口语和语音转写可能完全没有标点。每个能够独立检索、回答、发送资源或执行动作的问题都建立一个 intentTask，并保持 URef 顺序以及同一 URef 内的原文顺序。不同对象、不同知识主题或需要不同答案证据的问题必须拆开；即使 subIntent 相同也不能合并不同答案目标，例如“有啥吃的推荐没，以及附近哪里好玩”必须由你输出餐饮推荐和游玩推荐两个 Task，不能因为都属于 surrounding_facilities 合成一个。只有同一对象紧密相关且需要共同回答的多个方面才可合成 compound_information。纯背景、情绪或补充条件并入相关任务，不要凭空新增业务任务。intentTasks[].text 必须保留主要 URef 中连续的客户原话；任何指代补全、语义改写只能写入 resolvedText。输出前从头到尾核对，不能只处理最后一句或最后一个问题。")
	b.WriteString("同一当前轮中，若后一个 URef 需要前一个 URef 才能补全，就把前一个 URef 加入 sourceRefs，并保持 relationToPrevious=independent；follow_up、reference_previous、clarification_answer、correction、modify_previous、cancel_previous、answer_rejected 只用于真实的上一会话轮关系。例如 U1=有没有停车场、U2=我开电车来的你懂我意思吗，充电 Task 的 text=U2原话、resolvedText=酒店停车场有没有电车充电桩、sourceRefs=[U2,U1]、relationToPrevious=independent、resolutionState=resolved_from_context。")
	b.WriteString("\n\n当前消息类型: ")
	b.WriteString(string(req.UserMessage.MessageType))
	if timeLabel := adapter.RuntimeMessageTimeLabel(&req.UserMessage); timeLabel != "" {
		b.WriteString("\n当前消息时间: ")
		b.WriteString(timeLabel)
	}
	b.WriteString("\n\n判别纪律：只给“当前消息”分类；最近原始消息、媒体理解和长期记忆只用于解释“这个/刚才/还/继续/那”等指代。")
	b.WriteString("如果当前消息已经有独立的新主题，禁止沿用上一轮早餐、停车、投诉、安全、转人工等历史主题。")
	b.WriteString("但若紧邻的上一条 AI 客服消息正在追问一个业务问题的偏好、条件、范围或选项，当前短回答属于该业务的连续补充：必须继承该业务 intent/subIntent；intentTasks[].text 保留客户当前原表达，intentTasks[].resolvedText 写成包含上一轮业务主题和当前补充条件的完整检索问题。")
	b.WriteString("例如 AI 问附近餐饮口味、客户答‘麻辣口味的’，应输出 hotel_info/surrounding_facilities 且 needsKnowledge=true，text 写‘麻辣口味的’，resolvedText 写‘附近餐饮推荐，偏好麻辣口味’。‘那麦田呢’、‘外卖地址再说一遍’等明确回指或复述请求，也必须在 resolvedText 中补全对象和所问方面；没有紧邻业务追问时，独立短语不得从更早历史强行继承旧主题。")
	b.WriteString("客户明确问‘刚刚都问了什么’‘刚才聊了什么’‘你刚才回答了哪些’等会话回顾时，只建立一个 interaction/conversation_recap 文本任务，relationToPrevious=reference_previous，resolutionState=resolved_from_context，resolvedText 写明回顾最近当前会话；不能当作新闲聊或 unresolved，也不能重新执行历史业务任务。")
	b.WriteString("历史消息使用[历史消息][说话人][时间]格式，必须分清客户、AI客服、人工客服分别说了什么。")
	if instruction := buildAdjacentAIReplyRelationInstruction(history); instruction != "" {
		b.WriteString("\n\n")
		b.WriteString(instruction)
	}
	mediaContext := currentAndRecentMediaText(req, history)
	if req.UserMessage.MessageType == enums.IMMessageTypeVoice && currentText != "" {
		if mediaContext == currentText {
			mediaContext = ""
		} else if strings.HasPrefix(mediaContext, currentText+"\n") {
			mediaContext = strings.TrimSpace(strings.TrimPrefix(mediaContext, currentText))
		}
	}
	if mediaContext != "" {
		b.WriteString("\n\n上下文中的媒体理解:\n")
		b.WriteString(preview(mediaContext, 1200))
		b.WriteString("\n媒体解析结果只是上下文，不要输出单独的媒体类意图；是否使用它解释当前问题，由你根据当前消息语义判断。")
	}
	if len(history.RawItems) > 0 {
		start := len(history.RawItems) - 8
		if start < 0 {
			start = 0
		}
		coveredCurrentTurnSources := runtimeIntentCurrentTurnSourceSet(req)
		recentHistory := make([]string, 0, len(history.RawItems)-start)
		for _, item := range history.RawItems[start:] {
			if runtimeIntentMessageCoveredByCurrentTurn(item, coveredCurrentTurnSources) {
				continue
			}
			text := adapter.RuntimeHistoryMessageContent(&item)
			if text != "" {
				recentHistory = append(recentHistory, preview(text, 160))
			}
		}
		if len(recentHistory) > 0 {
			b.WriteString("\n\n最近原始消息(低于当前消息优先级):\n- ")
			b.WriteString(strings.Join(recentHistory, "\n- "))
			b.WriteString("\n")
		}
	}
	if history.MemoryMessage != nil && strings.TrimSpace(history.MemoryMessage.Content) != "" {
		b.WriteString("\n长期记忆摘要(最低优先级，房号等一次性入住事实不能当当前事实):\n")
		b.WriteString(preview(history.MemoryMessage.Content, 800))
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
		b.WriteString("\n再次强调，最终 JSON 只能分类本轮 CURRENT_TURN_SOURCE_REFS 对应的客户消息；历史和媒体上下文只能用于补全明确指代，不能变成新任务。\n")
	}
	b.WriteString("\n请输出严格 JSON。")
	return b.String()
}

func currentTurnIntentSourceTexts(currentDisplayText string) []string {
	currentDisplayText = strings.TrimSpace(currentDisplayText)
	if currentDisplayText == "" {
		return nil
	}
	if !utils.IsRuntimeCustomerBurstEnvelope(currentDisplayText) {
		return []string{currentDisplayText}
	}
	items := utils.RuntimeCustomerBurstItems(currentDisplayText)
	ret := make([]string, 0, len(items))
	for _, item := range items {
		if text := utils.RuntimeCustomerBurstItemText(item); text != "" {
			ret = append(ret, text)
		}
	}
	return ret
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
	b.WriteString("每个相关 intentTasks 项都要在 relationToPrevious 中表达与紧邻上一轮的关系：新主题用 independent，正常承接用 follow_up，回答 AI 追问用 clarification_answer，明确回指用 reference_previous，纠正用 correction；上一答复被明确否定、被指出矛盾或仍未解决同一问题时统一用 answer_rejected。\n")
	b.WriteString("凡当前短句必须借助紧邻上下文才能理解，都必须同时正确输出 relationToPrevious、resolutionState 和 resolvedText，不能降级成新的 interaction/social 或泛化 clarify。AI 对明确业务问题作是/否追问后，客户回答‘是的、对、可以’等确认语时，继承该业务 intent/subIntent，使用 relationToPrevious=clarification_answer、resolutionState=resolved_from_context，并在 resolvedText 中写出完整业务问题和确认含义。客户只说‘不是’且没有同时给出正确目标时，relationToPrevious 使用 correction 或 clarification_answer，但 resolutionState 必须是 ambiguous 或 unresolved，resolvedText 只能写已知的否定含义，禁止虚构客户真正想问的对象。\n")
	b.WriteString("AI 追问姓名、房号或其他必要字段后，客户只回复‘吴朝伟’‘1208’等字段值时，这是上一业务任务的槽位回答：继承原业务 intent/subIntent，relationToPrevious=clarification_answer；能唯一确定字段时 resolutionState=resolved_from_context，resolvedText 必须明确写成‘客户姓名为吴朝伟’‘房号为1208’这类完整语义，不能当作重新打招呼、普通数字或新闲聊。\n")
	b.WriteString("紧邻周边话题中的省略追问也要补全真实对象。例如上一轮正在回答附近餐饮，客户接着说‘玩的呢/玩的勒’，这是询问酒店附近游玩或休闲地点，输出 hotel_info/surrounding_facilities、relationToPrevious=reference_previous、resolutionState=resolved_from_context，并补全 resolvedText；不能按普通闲聊处理。\n")
	b.WriteString("只有以下语义关系输出 human_complaint_risk + answer_rejected，且 needsHumanRoute=true：客户明确否定上一答复；指出 AI 前后矛盾；指出答非所问并重申同一个问题；同一问题再次追问且上一答复仍未解决；拒绝 AI 给出的能力边界方案并要求无法满足的例外；引用真人客服说法或现场事实反驳上一答复。\n")
	b.WriteString("明确示例（必须结合此前问题与紧邻 AI 答复判断，不能只看单个词）：AI 前一条说走路几分钟就到，客户说‘你刚才不是说要开车吗’属于前后矛盾型 answer_rejected；AI 只回答用品去哪里领取，客户说‘我问的是房间里有没有’属于答非所问型 answer_rejected；AI 说不能微信转账，客户说‘客服说可以微信转账’属于事实反驳型 answer_rejected。以上都输出 human_complaint_risk + answer_rejected。\n")
	b.WriteString("以下不得输出 answer_rejected：提出独立新问题；正常补充收费、时间、支付等细节；正常回答 AI 刚才追问的房号、偏好、条件或选项；孤立的‘真的吗/为什么’但没有明确否定或矛盾；与上一业务答复无关的不满、吐槽或闲聊。此时按当前真实业务意图继续分类。")
	return b.String()
}

func hasImmediatelyPreviousAIReply(history adapter.HistoryBuildResult) bool {
	_, ok := immediatelyPreviousAIReply(history)
	return ok
}

func hasResolvableAdjacentIntentContext(history adapter.HistoryBuildResult) bool {
	if history.LatestRawItem != nil {
		item := *history.LatestRawItem
		if !utils.IsAIServiceNoticeMessage(&item) && strings.TrimSpace(adapter.RuntimeHistoryMessageContent(&item)) != "" {
			return true
		}
	}
	for index := len(history.RawItems) - 1; index >= 0; index-- {
		item := history.RawItems[index]
		if utils.IsAIServiceNoticeMessage(&item) || strings.TrimSpace(adapter.RuntimeHistoryMessageContent(&item)) == "" {
			continue
		}
		return true
	}
	return false
}

func immediatelyPreviousAIReply(history adapter.HistoryBuildResult) (string, bool) {
	if history.LatestRawItem != nil {
		item := *history.LatestRawItem
		if !utils.IsAIServiceNoticeMessage(&item) {
			if item.SenderType != enums.IMSenderTypeAI {
				return "", false
			}
			content := strings.TrimSpace(adapter.RuntimeHistoryMessageContent(&item))
			if content == "" {
				return "", false
			}
			return content, true
		}
	}
	for index := len(history.RawItems) - 1; index >= 0; index-- {
		item := history.RawItems[index]
		if utils.IsAIServiceNoticeMessage(&item) {
			continue
		}
		if item.SenderType != enums.IMSenderTypeAI {
			return "", false
		}
		content := strings.TrimSpace(adapter.RuntimeHistoryMessageContent(&item))
		if content == "" {
			return "", false
		}
		return content, true
	}
	return "", false
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
	var parsed runtimeIntentDetectJSON
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return parsed, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("unexpected trailing JSON value")
		}
		return parsed, err
	}
	return parsed, nil
}

func applyLegacyRuntimeIntentProtocolDefaults(parsed *runtimeIntentDetectJSON, profile *models.ReplyIntentProfile) {
	if runtimeIntentProfileExpectsTaskSemantics(profile) {
		return
	}
	applyRuntimeIntentProtocolDefaults(parsed)
}

func applyRuntimeIntentProtocolDefaults(parsed *runtimeIntentDetectJSON) {
	if parsed == nil {
		return
	}
	for index := range parsed.IntentTasks {
		task := &parsed.IntentTasks[index]
		rawObjective := strings.TrimSpace(task.Objective)
		if semanticGateValidObjective(semanticGateNormalizeObjective(rawObjective)) {
			continue
		}
		switch semanticGateNormalizeValue(rawObjective) {
		case "", "resource", "resource_action":
		default:
			continue
		}

		intent := canonicalIntentCode(task.Intent)
		rawAction := strings.TrimSpace(task.ResourceAction)
		resourceType := ""
		resourceAction := ""
		if rawAction != "" {
			resourceType, resourceAction = normalizeHotelVariableResourceAction(rawAction, "", "")
			if !semanticGateAllowedResourceAction(resourceAction) {
				continue
			}
		} else {
			resourceType, resourceAction = normalizeHotelVariableResourceAction("", "", task.SubIntent)
		}
		if !semanticGateAllowedResourceAction(resourceAction) {
			continue
		}
		if intent != "hotel_variable" {
			if rawAction == "" || intent == "human_complaint_risk" || task.NeedsHumanRoute || task.NeedsTool {
				continue
			}
			task.Intent = "hotel_variable"
		}
		if resourceType == "" {
			continue
		}
		task.NeedsResource = true
		task.ResourceAction = resourceAction
		if strings.TrimSpace(task.SubIntent) == "" {
			task.SubIntent = resourceType
		}
		task.Objective = "action_request"
	}
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
	intent.IntentTasks = normalizeRuntimeIntentTasks(intent.IntentTasks)
	currentSourceTexts := currentTurnIntentSourceTexts(currentRuntimeIntentSemanticText(req))
	if intent.SemanticContractExpected && !intent.SourceRefsValidated {
		var droppedUngroundedTasks int
		intent.IntentTasks, droppedUngroundedTasks = filterRuntimeIntentTasksToCurrentTurn(
			intent.IntentTasks,
			currentSourceTexts,
			true,
		)
		if droppedUngroundedTasks > 0 {
			intent.Reason = appendIntentReason(intent.Reason, fmt.Sprintf("current-turn grounding dropped %d unbound intent task(s)", droppedUngroundedTasks))
		}
	}
	semanticGate := applyRuntimeIntentSemanticConsistencyGateFromTrace(intent, runtimeIntentSemanticGateContext{
		HasResolvableAdjacentContext: hasResolvableAdjacentIntentContext(history),
		HasAdjacentAIReply:           hasImmediatelyPreviousAIReply(history),
		RequireSemanticContract:      intent.SemanticContractExpected,
		CurrentTurnRefsValid:         intent.SourceRefsValidated && len(currentSourceTexts) > 0,
	})
	intent = semanticGate.Intent
	for _, violation := range semanticGate.Violations {
		intent.Reason = appendIntentReason(intent.Reason, "semantic gate: "+violation.Code)
	}
	if intent.IntentConfidence < 0.45 && intent.PrimaryIntent != "human_complaint_risk" && !semanticGate.SuppressLegacyConfidenceFallback {
		if semanticGate.ContractMode == runtimeIntentSemanticContractLegacy {
			intent = legacyLowConfidenceClarificationIntent(intent, req)
		} else {
			intent.PrimaryIntent = "interaction"
			intent.MatchedIntentCode = "interaction"
			intent.SubIntent = "clarify"
			intent.NeedsClarification = true
			intent.NeedsKnowledge = false
			intent.NeedsResource = false
			intent.NeedsHumanRoute = false
		}
	}
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
	if shouldAttachCheckinMiniProgramTask(intent, req) {
		intent = ensureCheckinProcessMiniProgramTask(intent, req)
	}
	// V2 sourceRefs are model-owned and have already passed strict provenance
	// validation. Only legacy profiles may use the old compatibility repair.
	if !intent.SemanticContractExpected {
		intent.IntentTasks = repairRuntimeIntentTaskSourceRefs(intent.IntentTasks, currentSourceTexts)
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
	intent = enforceRuntimeWeatherToolAction(intent)
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

func legacyLowConfidenceClarificationIntent(intent callbacks.IntentTraceData, req RunInput) callbacks.IntentTraceData {
	text := strings.TrimSpace(currentRuntimeIntentSemanticText(req))
	if text == "" {
		for _, task := range intent.IntentTasks {
			text = strings.TrimSpace(task.Text)
			if text == "" {
				text = strings.TrimSpace(task.ResolvedText)
			}
			if text != "" {
				break
			}
		}
	}
	intent.DetectedIntent = "interaction"
	intent.PrimaryIntent = "interaction"
	intent.MatchedIntentCode = "interaction"
	intent.SubIntent = "clarify"
	intent.SecondaryIntents = nil
	intent.SecondaryIntentCodes = nil
	intent.NeedsClarification = true
	intent.NeedsKnowledge = false
	intent.NeedsTool = false
	intent.NeedsResource = false
	intent.NeedsHumanRoute = false
	intent.ResourceType = ""
	intent.ResourceAction = ""
	intent.ResourceActions = nil
	intent.MixedSubTasks = nil
	intent.ToolCodes = nil
	intent.HumanRoutePolicy = ""
	intent.IntentTasks = []callbacks.IntentTaskTraceData{{
		Intent:             "interaction",
		SubIntent:          "clarify",
		Objective:          "unknown",
		RelationToPrevious: "independent",
		ResolutionState:    runtimeIntentResolutionAmbiguous,
		Text:               text,
		ResolvedText:       text,
		Reason:             "legacy low-confidence intent requires clarification",
	}}
	intent.Reason = appendIntentReason(intent.Reason, "legacy low-confidence intent collapsed to clarification")
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
		task.Objective = "social"
		task.RelationToPrevious = "correction"
		task.ResolutionState = "clear"
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
	hasTool := false
	hasClarification := false
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
		if task.NeedsTool {
			hasTool = true
		}
		if task.SubIntent == "clarify" || task.ResolutionState == runtimeIntentResolutionAmbiguous || task.ResolutionState == runtimeIntentResolutionUnresolved {
			hasClarification = true
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
	intent.SecondaryIntents = secondary
	intent.SecondaryIntentCodes = append([]string(nil), secondary...)
	intent.NeedsKnowledge = hasKnowledge
	intent.NeedsResource = hasResource
	intent.NeedsTool = hasTool
	intent.NeedsHumanRoute = hasHuman
	intent.NeedsClarification = hasClarification
	intent.ResourceActions = append([]string(nil), resourceActions...)
	intent.ResourceAction = ""
	intent.ResourceType = ""
	if len(resourceActions) > 0 {
		intent.ResourceAction = resourceActions[0]
		intent.ResourceType, intent.ResourceAction = normalizeHotelVariableResourceAction(intent.ResourceAction, "", "")
	}
	intent.SubIntent = firstTaskSubIntentForPrimary(intent.IntentTasks, intent.PrimaryIntent)
	if intent.SubIntent == "" && intent.PrimaryIntent == "hotel_variable" && intent.ResourceType != "" && intent.ResourceType != "store_variable" {
		intent.SubIntent = intent.ResourceType
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
		intent.Reason = appendIntentReason(intent.Reason, "human route ignored: direct handoff only belongs to human_complaint_risk intent category")
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

func shouldAttachCheckinMiniProgramTask(intent callbacks.IntentTraceData, req RunInput) bool {
	if intent.PrimaryIntent != "hotel_info" {
		return false
	}
	currentText := currentRuntimeIntentSemanticText(req)
	if isCheckinProcessSubIntent(intent.SubIntent) && runtimeIntentTextMentionsCheckin(currentText) {
		return true
	}
	sourceTexts := currentTurnIntentSourceTexts(currentText)
	for _, task := range intent.IntentTasks {
		if task.Intent != "hotel_info" || !isCheckinProcessSubIntent(task.SubIntent) {
			continue
		}
		if uniqueRuntimeIntentTaskSourceRef(task, sourceTexts) == "" {
			continue
		}
		if runtimeIntentTextMentionsCheckin(task.Text) {
			return true
		}
		if semanticGateRelationUsesPrevious(task.RelationToPrevious) &&
			task.ResolutionState == runtimeIntentResolutionResolvedFromContext &&
			runtimeIntentTextMentionsCheckin(task.ResolvedText) {
			return true
		}
	}
	return false
}

func runtimeIntentTextMentionsCheckin(text string) bool {
	compact := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "", "-", "", "_", "").Replace(strings.ToLower(strings.TrimSpace(text)))
	for _, marker := range []string{"入住", "住店", "checkin", "登记住宿"} {
		if strings.Contains(compact, marker) {
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
	if intent.SemanticContractExpected {
		intent.Reason = appendIntentReason(intent.Reason, "checkin_process attached mini program resource action")
		return intent
	}
	currentText := strings.TrimSpace(currentRuntimeIntentSemanticText(req))
	if currentText == "" {
		currentText = "办理入住"
	}
	hasKnowledgeTask := false
	hasMiniProgramTask := false
	checkinSourceRefs := make([]string, 0)
	for i := range intent.IntentTasks {
		if intent.IntentTasks[i].Intent == "hotel_info" && isCheckinProcessSubIntent(intent.IntentTasks[i].SubIntent) {
			intent.IntentTasks[i].SubIntent = "checkin_process"
			intent.IntentTasks[i].NeedsKnowledge = true
			if intent.IntentTasks[i].Objective == "" {
				intent.IntentTasks[i].Objective = "method"
			}
			if intent.IntentTasks[i].RelationToPrevious == "" {
				intent.IntentTasks[i].RelationToPrevious = "independent"
			}
			if intent.IntentTasks[i].ResolutionState == "" {
				intent.IntentTasks[i].ResolutionState = runtimeIntentResolutionClear
			}
			if strings.TrimSpace(intent.IntentTasks[i].Text) == "" {
				intent.IntentTasks[i].Text = currentText
			}
			if len(checkinSourceRefs) == 0 {
				checkinSourceRefs = append([]string(nil), intent.IntentTasks[i].SourceRefs...)
			}
			hasKnowledgeTask = true
		}
	}
	for i := range intent.IntentTasks {
		if intent.IntentTasks[i].Intent == "hotel_variable" && strings.TrimSpace(intent.IntentTasks[i].ResourceAction) == "provide_mini_program" {
			intent.IntentTasks[i].SubIntent = "mini_program"
			intent.IntentTasks[i].NeedsResource = true
			if intent.IntentTasks[i].Objective == "" {
				intent.IntentTasks[i].Objective = "action_request"
			}
			if intent.IntentTasks[i].RelationToPrevious == "" {
				intent.IntentTasks[i].RelationToPrevious = "independent"
			}
			if intent.IntentTasks[i].ResolutionState == "" {
				intent.IntentTasks[i].ResolutionState = runtimeIntentResolutionClear
			}
			if strings.TrimSpace(intent.IntentTasks[i].Text) == "" {
				intent.IntentTasks[i].Text = "发送入住小程序入口"
			}
			if len(intent.IntentTasks[i].SourceRefs) == 0 && len(checkinSourceRefs) > 0 {
				intent.IntentTasks[i].SourceRefs = append([]string(nil), checkinSourceRefs...)
			}
			hasMiniProgramTask = true
		}
	}
	if !hasKnowledgeTask {
		intent.IntentTasks = append([]callbacks.IntentTaskTraceData{{
			Intent:             "hotel_info",
			SubIntent:          "checkin_process",
			Objective:          "method",
			RelationToPrevious: "independent",
			ResolutionState:    runtimeIntentResolutionClear,
			Text:               currentText,
			NeedsKnowledge:     true,
			Reason:             "checkin process needs knowledge tutorial",
		}}, intent.IntentTasks...)
	}
	if !hasMiniProgramTask {
		intent.IntentTasks = append(intent.IntentTasks, callbacks.IntentTaskTraceData{
			Intent:             "hotel_variable",
			SubIntent:          "mini_program",
			Objective:          "action_request",
			RelationToPrevious: "independent",
			ResolutionState:    runtimeIntentResolutionClear,
			Text:               "发送入住小程序入口",
			SourceRefs:         append([]string(nil), checkinSourceRefs...),
			NeedsResource:      true,
			ResourceAction:     "provide_mini_program",
			Reason:             "checkin process should also provide configured mini program entry",
		})
	}
	intent.Reason = appendIntentReason(intent.Reason, "checkin_process attached mini program resource action")
	return intent
}

func enforceRuntimeWeatherToolAction(intent callbacks.IntentTraceData) callbacks.IntentTraceData {
	hasWeatherTask := false
	for index := range intent.IntentTasks {
		task := &intent.IntentTasks[index]
		if canonicalIntentCode(task.Intent) != "interaction" || strings.TrimSpace(task.SubIntent) != "weather_query" {
			continue
		}
		task.NeedsTool = true
		hasWeatherTask = true
	}
	if !hasWeatherTask && strings.TrimSpace(intent.SubIntent) != "weather_query" && strings.TrimSpace(intent.ResourceAction) != "get_weather" {
		return intent
	}
	intent.NeedsTool = true
	intent.ToolCodes = appendIfMissing(intent.ToolCodes, toolx.BuiltinWeather.Code)
	if len(intent.ResourceActions) == 0 {
		intent.ResourceAction = "get_weather"
	}
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
		task.Objective = semanticGateNormalizeObjective(task.Objective)
		task.RelationToPrevious = semanticGateNormalizeRelation(task.RelationToPrevious)
		task.ResolutionState = semanticGateNormalizeResolution(task.ResolutionState)
		task.Entities = normalizeRuntimeIntentEntities(task.Entities)
		task.Text = strings.TrimSpace(task.Text)
		if task.ResolvedText = strings.TrimSpace(task.ResolvedText); task.ResolvedText == "" {
			task.ResolvedText = task.Text
		}
		task.SourceRefs = normalizeRuntimeIntentSourceRefs(task.SourceRefs)
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

func filterRuntimeIntentTasksToCurrentTurn(tasks []callbacks.IntentTaskTraceData, sourceTexts []string, enforce bool) ([]callbacks.IntentTaskTraceData, int) {
	if !enforce || len(tasks) < 2 || len(sourceTexts) == 0 {
		return tasks, 0
	}
	grounded := make([]bool, len(tasks))
	groundedBusinessTasks := 0
	for index, task := range tasks {
		grounded[index] = runtimeIntentTaskGroundedInCurrentTurn(task, sourceTexts)
		if grounded[index] && runtimeIntentTaskHasExecutableBusiness(task) {
			groundedBusinessTasks++
		}
	}
	if groundedBusinessTasks == 0 {
		return tasks, 0
	}

	ret := make([]callbacks.IntentTaskTraceData, 0, len(tasks))
	dropped := 0
	for index, task := range tasks {
		if runtimeIntentTaskHasExecutableBusiness(task) && !grounded[index] {
			dropped++
			continue
		}
		ret = append(ret, task)
	}
	return ret, dropped
}

func runtimeIntentTaskHasExecutableBusiness(task callbacks.IntentTaskTraceData) bool {
	return canonicalIntentCode(task.Intent) != "interaction" ||
		task.NeedsKnowledge || task.NeedsResource || task.NeedsTool || task.NeedsHumanRoute ||
		strings.TrimSpace(task.ResourceAction) != ""
}

func runtimeIntentTaskGroundedInCurrentTurn(task callbacks.IntentTaskTraceData, sourceTexts []string) bool {
	taskText := normalizeRuntimeKnowledgeQuery(task.Text)
	for _, sourceText := range sourceTexts {
		source := normalizeRuntimeKnowledgeQuery(sourceText)
		if taskText != "" && source != "" && (taskText == source || strings.Contains(taskText, source) || strings.Contains(source, taskText)) {
			return true
		}
		for _, entity := range task.Entities {
			entityText := normalizeRuntimeKnowledgeQuery(entity.Text)
			if len([]rune(entityText)) >= 2 && strings.Contains(source, entityText) {
				return true
			}
		}
	}
	return false
}

func normalizeRuntimeIntentEntities(entities []callbacks.IntentEntityTraceData) []callbacks.IntentEntityTraceData {
	ret := make([]callbacks.IntentEntityTraceData, 0, len(entities))
	for _, entity := range entities {
		text := strings.TrimSpace(entity.Text)
		if text == "" {
			continue
		}
		entityType := semanticGateNormalizeValue(entity.Type)
		if !isRuntimeIntentEntityType(entityType) {
			entityType = "other"
		}
		ret = append(ret, callbacks.IntentEntityTraceData{Text: text, Type: entityType})
	}
	return ret
}

func normalizeRuntimeIntentSourceRefs(refs []string) []string {
	ret := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref != "" {
			ret = appendIfMissing(ret, ref)
		}
	}
	return ret
}

func repairRuntimeIntentTaskSourceRefs(tasks []callbacks.IntentTaskTraceData, sourceTexts []string) []callbacks.IntentTaskTraceData {
	if len(tasks) == 0 || len(sourceTexts) == 0 {
		return tasks
	}
	allowed := make(map[string]struct{}, len(sourceTexts))
	sourceIndexByRef := make(map[string]int, len(sourceTexts))
	for index := range sourceTexts {
		ref := fmt.Sprintf("U%d", index+1)
		allowed[ref] = struct{}{}
		sourceIndexByRef[ref] = index
	}
	for index := range tasks {
		valid := make([]string, 0, len(tasks[index].SourceRefs)+1)
		for _, ref := range normalizeRuntimeIntentSourceRefs(tasks[index].SourceRefs) {
			if _, ok := allowed[ref]; ok {
				valid = append(valid, ref)
			}
		}
		if matched := uniqueRuntimeIntentTaskSourceRef(tasks[index], sourceTexts); matched != "" {
			valid = moveRuntimeIntentSourceRefFirst(valid, matched)
		} else if len(sourceTexts) == 1 && len(valid) == 0 {
			valid = []string{"U1"}
		}
		tasks[index].SourceRefs = valid
	}

	covered := make(map[string]struct{}, len(sourceTexts))
	for _, task := range tasks {
		for _, ref := range task.SourceRefs {
			covered[ref] = struct{}{}
		}
	}
	for sourceIndex, sourceText := range sourceTexts {
		ref := fmt.Sprintf("U%d", sourceIndex+1)
		if _, ok := covered[ref]; ok || runtimeBurstLineLooksLikeTask(sourceText) {
			continue
		}
		if taskIndex := nearestRuntimeIntentSourceBindingTask(tasks, sourceIndex, sourceIndexByRef); taskIndex >= 0 {
			tasks[taskIndex].SourceRefs = appendIfMissing(tasks[taskIndex].SourceRefs, ref)
			covered[ref] = struct{}{}
		}
	}
	return tasks
}

func uniqueRuntimeIntentTaskSourceRef(task callbacks.IntentTaskTraceData, sourceTexts []string) string {
	taskText := normalizeRuntimeKnowledgeQuery(task.Text)
	if taskText == "" {
		return ""
	}
	bestScore := 0
	bestRef := ""
	bestCount := 0
	for index, sourceText := range sourceTexts {
		source := normalizeRuntimeKnowledgeQuery(sourceText)
		if source == "" {
			continue
		}
		score := 0
		switch {
		case source == taskText:
			score = 3
		case len([]rune(source)) >= 2 && strings.Contains(taskText, source):
			score = 2
		case len([]rune(taskText)) >= 2 && strings.Contains(source, taskText):
			score = 2
		}
		if score == 0 {
			continue
		}
		if score > bestScore {
			bestScore = score
			bestRef = fmt.Sprintf("U%d", index+1)
			bestCount = 1
		} else if score == bestScore {
			bestCount++
		}
	}
	if bestCount != 1 {
		return ""
	}
	return bestRef
}

func moveRuntimeIntentSourceRefFirst(refs []string, primary string) []string {
	ret := []string{primary}
	for _, ref := range refs {
		if ref != primary {
			ret = appendIfMissing(ret, ref)
		}
	}
	return ret
}

func nearestRuntimeIntentSourceBindingTask(tasks []callbacks.IntentTaskTraceData, sourceIndex int, sourceIndexByRef map[string]int) int {
	bestIndex := -1
	bestDistance := len(sourceIndexByRef) + len(tasks) + 1
	for requireBusiness := true; ; requireBusiness = false {
		for index, task := range tasks {
			isBusiness := task.Intent != "interaction" || task.NeedsKnowledge || task.NeedsResource || task.NeedsTool || task.NeedsHumanRoute
			if requireBusiness && !isBusiness {
				continue
			}
			distance := len(sourceIndexByRef) + index
			if len(task.SourceRefs) > 0 {
				if primaryIndex, ok := sourceIndexByRef[task.SourceRefs[0]]; ok {
					distance = primaryIndex - sourceIndex
					if distance < 0 {
						distance = -distance
					}
				}
			}
			if distance < bestDistance {
				bestIndex = index
				bestDistance = distance
			}
		}
		if bestIndex >= 0 || !requireBusiness {
			return bestIndex
		}
	}
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
