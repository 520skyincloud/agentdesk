package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/replyengine"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/factory"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"

	"github.com/cloudwego/eino/schema"
	"github.com/mlogclub/simple/sqls"
)

type runtimeIntentModelDetector interface {
	DetectRuntimeIntent(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, error)
}

func deterministicWeatherIntent(text string) (callbacks.IntentTraceData, bool) {
	text = strings.TrimSpace(text)
	if text == "" || !looksLikeWeatherQuery(text) {
		return callbacks.IntentTraceData{}, false
	}
	return callbacks.IntentTraceData{
		DetectedIntent:    "天气查询",
		MatchedIntentCode: "social_confirm",
		PrimaryIntent:     "social_confirm",
		SubIntent:         "weather_query",
		IntentConfidence:  0.92,
		ShouldReply:       true,
		NeedsTool:         true,
		ResourceAction:    "get_weather",
		ToolCodes:         []string{toolx.BuiltinWeather.Code},
		Reason:            "deterministic weather query under social_confirm: use get_weather tool",
	}, true
}

func looksLikeWeatherQuery(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	weatherWords := []string{"天气", "气温", "温度", "下雨", "有雨", "雨吗", "冷不冷", "热不热", "会不会冷", "会不会热", "适合出门", "穿什么"}
	for _, word := range weatherWords {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

type llmRuntimeIntentDetector struct{}

type runtimeIntentDetectJSON struct {
	PrimaryIntent    string   `json:"primaryIntent"`
	SubIntent        string   `json:"subIntent"`
	Confidence       float64  `json:"confidence"`
	NeedsKnowledge   bool     `json:"needsKnowledge"`
	NeedsTool        bool     `json:"needsTool"`
	NeedsResource    bool     `json:"needsResource"`
	NeedsHumanRoute  bool     `json:"needsHumanRoute"`
	ResourceAction   string   `json:"resourceAction"`
	SecondaryIntents []string `json:"secondaryIntents"`
	Reason           string   `json:"reason"`
}

func detectRuntimeIntentWithModel(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, detector runtimeIntentModelDetector) (callbacks.IntentTraceData, callbacks.IntentPromptTraceData, bool) {
	if isMediaOnlyWithoutActionableIntent(req.UserMessage) && !hasAdjacentTextMediaQuestion(req, history) {
		intent := callbacks.IntentTraceData{DetectedIntent: "普通媒体无明确诉求", MatchedIntentCode: "context_media_gate", PrimaryIntent: "context_media", SubIntent: "media_only_no_question", IntentConfidence: 0.9, ShouldReply: false, Reason: "media context gate: media-only message has no actionable intent"}
		return intent, selectIntentPromptPack(intent), true
	}
	configs := loadEnabledIntentConfigs(resolveRuntimeIntentScope(req))
	if detector == nil {
		detector = llmRuntimeIntentDetector{}
	}
	intent, err := detector.DetectRuntimeIntent(ctx, req, history, configs)
	if err != nil {
		if safetyIntent, ok := deterministicRuntimeIntentOverride(req.UserMessage.Content); ok {
			safetyIntent.Reason = strings.TrimSpace(safetyIntent.Reason + "; model intent detect failed: " + err.Error())
			return safetyIntent, selectIntentPromptPack(safetyIntent), true
		}
		return callbacks.IntentTraceData{}, callbacks.IntentPromptTraceData{}, false
	}
	intent = normalizeModelIntentTrace(intent, req, configs)
	if intent.PrimaryIntent == "" {
		return callbacks.IntentTraceData{}, callbacks.IntentPromptTraceData{}, false
	}
	prompt := promptForModelDetectedIntent(intent, configs)
	return intent, prompt, true
}

func (llmRuntimeIntentDetector) DetectRuntimeIntent(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, error) {
	intentConfig := resolveRuntimeIntentDetectAIConfig(req)
	if strings.TrimSpace(intentConfig.ModelName) == "" || strings.TrimSpace(string(intentConfig.Provider)) == "" {
		return callbacks.IntentTraceData{}, fmt.Errorf("ai config unavailable")
	}
	intentCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	chatModel, err := factory.NewChatModelFactory().Build(intentCtx, intentConfig)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	result, err := chatModel.Generate(intentCtx, []*schema.Message{
		schema.SystemMessage(runtimeIntentDetectSystemPrompt()),
		schema.UserMessage(buildRuntimeIntentDetectUserPrompt(req, history, configs)),
	})
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	parsed, err := parseRuntimeIntentDetectJSON(result.Content)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	return callbacks.IntentTraceData{
		DetectedIntent:       parsed.PrimaryIntent,
		MatchedIntentCode:    parsed.PrimaryIntent,
		PrimaryIntent:        parsed.PrimaryIntent,
		SubIntent:            parsed.SubIntent,
		SecondaryIntents:     parsed.SecondaryIntents,
		SecondaryIntentCodes: parsed.SecondaryIntents,
		IntentConfidence:     parsed.Confidence,
		ShouldReply:          true,
		NeedsKnowledge:       parsed.NeedsKnowledge,
		NeedsTool:            parsed.NeedsTool,
		NeedsResource:        parsed.NeedsResource,
		NeedsHumanRoute:      parsed.NeedsHumanRoute,
		ResourceAction:       parsed.ResourceAction,
		HumanRoutePolicy:     parsed.SubIntent,
		Reason:               strings.TrimSpace("model IntentDetect JSON: " + parsed.Reason),
	}, nil
}

func resolveRuntimeIntentDetectAIConfig(req RunInput) models.AIConfig {
	if sqls.DB() != nil && req.Conversation.ID > 0 {
		if resolved, err := services.StoreAIModelSettingService.ResolveForConversation(req.Conversation.ID, services.StoreAIModelUsageIntentDetectLLM, 0); err == nil && resolved != nil {
			return resolved.Config
		}
	}
	if sqls.DB() != nil {
		if resolved, err := services.StoreAIModelSettingService.Resolve(0, services.StoreAIModelUsageIntentDetectLLM); err == nil && resolved != nil {
			return resolved.Config
		}
	}
	return req.AIConfig
}

func resolveRuntimeAIConfigByStoreUsage(req RunInput, usageCode string, legacyAgentConfigID int64) models.AIConfig {
	if sqls.DB() == nil {
		return req.AIConfig
	}
	if resolved, err := services.StoreAIModelSettingService.ResolveForConversation(req.Conversation.ID, usageCode, legacyAgentConfigID); err == nil && resolved != nil {
		return resolved.Config
	}
	return req.AIConfig
}

func runtimeIntentDetectSystemPrompt() string {
	return strings.TrimSpace(`你是酒店无人化客服系统的 IntentDetect 阶段，只输出 JSON，不回复客户。
你的任务是判断当前用户消息属于业务分类之一：hotel_info、service_request、human_complaint_risk、social_confirm、unknown_clarify、hotel_variable。
分类规则：
1. 用户问酒店规则、设施、设备、用品、流程、价格、服务说明、可不可用、怎么操作等信息类问题，归 hotel_info，needsKnowledge=true。
2. 用户要当前门店电话、定位/地址/导航、入住小程序、门店群等账号变量，归 hotel_variable，needsResource=true，不查知识库。
3. 摔倒、受伤、流血、报警、安全事故等突发安全风险，归 human_complaint_risk，subIntent=emergency_safety，needsHumanRoute=true。
4. 明确要人工、强投诉、赔偿、严重风险，归 human_complaint_risk。
5. 送物、维修、叫醒、行李等执行类诉求归 service_request；但如果本质是在问规则、位置、自助方式、能不能、怎么处理，归 hotel_info。service_request 只要不是突发安全/明确人工，也必须先设置 needsKnowledge=true，让当前门店知识库先给自助路径、处理边界或升级依据。
6. 图片、文件、截图等媒体解析结果只是上下文文本，不是业务意图分类；如果当前消息是“这是啥/这是干嘛的/什么意思/怎么样/你看”等短指代疑问，且上下文里最近有已解析的图片或文件内容，就按基于上下文的普通问答处理：不查酒店知识库、不取门店变量、不转人工，通常归 social_confirm 或 unknown_clarify，并在 reason 说明是基于最近媒体/文件上下文回答。
7. 天气、气温、下雨、冷不冷、热不热、适不适合出门等闲聊型天气查询，不属于酒店信息和酒店变量；归 social_confirm，但必须设置 subIntent=weather_query、needsTool=true、resourceAction=get_weather。用户给出城市/地点时不要说查不到，后续阶段会调用天气工具。
8. 感谢、好的、确认、表情、轻互动归 social_confirm。低置信度归 unknown_clarify。
只返回 JSON，字段固定：primaryIntent, subIntent, confidence, needsKnowledge, needsTool, needsResource, needsHumanRoute, resourceAction, secondaryIntents, reason。`)
}

func buildRuntimeIntentDetectUserPrompt(req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) string {
	var b strings.Builder
	currentText := strings.TrimSpace(req.UserMessage.Content)
	b.WriteString("必须分类的当前消息:\n")
	b.WriteString(currentText)
	b.WriteString("\n\n当前消息类型: ")
	b.WriteString(string(req.UserMessage.MessageType))
	b.WriteString("\n\n判别纪律：只给“当前消息”分类；最近原始消息、媒体理解和长期记忆只用于解释“这个/刚才/还/继续/那”等指代。")
	b.WriteString("如果当前消息已经有独立的新主题，禁止沿用上一轮早餐、停车、投诉、安全、转人工等历史主题。")
	mediaContext := currentAndRecentMediaText(req, history)
	if mediaContext != "" {
		b.WriteString("\n\n上下文中的媒体理解:\n")
		b.WriteString(preview(mediaContext, 1200))
		if replyengine.LooksLikeMediaFollowUp(req.UserMessage.Content) {
			b.WriteString("\n注意：当前消息像是在追问最近图片/文件解析文本；媒体解析结果只是上下文，不是意图分类，不要输出 media_understanding。")
		}
	}
	if len(history.RawItems) > 0 {
		b.WriteString("\n\n最近原始消息(低于当前消息优先级):\n")
		start := len(history.RawItems) - 8
		if start < 0 {
			start = 0
		}
		for _, item := range history.RawItems[start:] {
			text := strings.TrimSpace(item.Content)
			if text == "" && item.Payload != "" {
				mediaText, mediaSummary, _ := utils.RuntimeMediaUnderstandingFromPayload(item.Payload)
				text = strings.TrimSpace(strings.Join([]string{mediaText, mediaSummary}, " "))
			}
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
				b.WriteString(preview(desc, 120))
			}
			b.WriteString("\n")
		}
	}
	if currentText != "" {
		b.WriteString("\n再次强调，最终 JSON 必须只分类这条当前消息：\n")
		b.WriteString(currentText)
		b.WriteString("\n")
	}
	b.WriteString("\n请输出严格 JSON。")
	return b.String()
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

func normalizeModelIntentTrace(intent callbacks.IntentTraceData, req RunInput, configs []models.ReplyIntentConfig) callbacks.IntentTraceData {
	if weatherIntent, ok := deterministicWeatherIntent(req.UserMessage.Content); ok {
		return weatherIntent
	}
	intent.PrimaryIntent = canonicalIntentCode(intent.PrimaryIntent)
	if intent.PrimaryIntent == "" {
		intent.PrimaryIntent = canonicalIntentCode(intent.MatchedIntentCode)
	}
	if intent.PrimaryIntent == "" || !isRuntimeTopLevelIntent(intent.PrimaryIntent) {
		intent.PrimaryIntent = "unknown_clarify"
		intent.MatchedIntentCode = "unknown_clarify"
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
		intent.PrimaryIntent = "unknown_clarify"
		intent.MatchedIntentCode = "unknown_clarify"
		intent.NeedsClarification = true
		intent.NeedsKnowledge = false
		intent.NeedsResource = false
		intent.NeedsHumanRoute = false
	}
	if safetyIntent, ok := deterministicRuntimeIntentOverride(req.UserMessage.Content); ok && shouldCurrentTurnOverrideConfiguredIntent(safetyIntent, intent) {
		return safetyIntent
	}
	switch intent.PrimaryIntent {
	case "hotel_info":
		intent.NeedsKnowledge = true
		intent.NeedsResource = false
		intent.NeedsHumanRoute = false
		currentSubIntent := detectRuntimeSubIntent("hotel_info", req.UserMessage.Content)
		if currentSubIntent != "" && currentSubIntent != "store_knowledge" && currentSubIntent != intent.SubIntent {
			intent.SubIntent = currentSubIntent
			intent.Reason = appendIntentReason(intent.Reason, "current-turn subIntent override")
		} else if intent.SubIntent == "" {
			intent.SubIntent = currentSubIntent
		}
	case "hotel_variable":
		intent.NeedsKnowledge = false
		intent.NeedsResource = true
		intent.NeedsHumanRoute = false
		resourceType, resourceAction := normalizeHotelVariableResourceAction(intent.ResourceAction, req.UserMessage.Content)
		intent.ResourceType = resourceType
		intent.ResourceAction = resourceAction
		if resourceType != "" && resourceType != "store_variable" {
			intent.SubIntent = resourceType
			intent.Reason = appendIntentReason(intent.Reason, "current-turn resource subIntent override")
		} else if intent.SubIntent == "" {
			intent.SubIntent = resourceType
		}
		intent = applyHotelVariableMixedKnowledge(intent, req.UserMessage.Content)
	case "service_request":
		intent.NeedsKnowledge = true
		intent.NeedsResource = false
		intent = enforceHumanRouteFlagByIntentCategory(intent)
	case "social_confirm":
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
			if isExplicitHandoffSubIntent(intent.SubIntent) || looksLikeExplicitHandoffRequest(req.UserMessage.Content) {
				intent.SubIntent = "explicit_handoff"
			}
			intent.HumanRoutePolicy = "managed_mode"
		}
	case "unknown_clarify":
		intent.NeedsClarification = true
		intent.NeedsKnowledge = false
		intent.NeedsResource = false
		intent.NeedsHumanRoute = false
	}
	intent = enforceHumanRouteFlagByIntentCategory(intent)
	intent = applyCurrentTurnIntentGuard(intent, req, configs)
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

func applyCurrentTurnIntentGuard(intent callbacks.IntentTraceData, req RunInput, configs []models.ReplyIntentConfig) callbacks.IntentTraceData {
	currentText := strings.TrimSpace(req.UserMessage.Content)
	if currentText == "" || isRuntimeMediaMessage(req.UserMessage.MessageType) {
		return intent
	}
	if replyengine.LooksLikeMediaFollowUp(currentText) {
		return intent
	}
	currentOnly, ok := classifyIntentFromCurrentTurnOnly(currentText, configs)
	if ok && currentOnly.PrimaryIntent != "" {
		if shouldCurrentOnlyIntentOverrideModel(currentOnly, intent) {
			currentOnly.Reason = appendIntentReason(currentOnly.Reason, "current-turn guard replaced model intent: "+intent.PrimaryIntent)
			return currentOnly
		}
		if currentOnly.PrimaryIntent == intent.PrimaryIntent {
			intent = mergeCurrentOnlyIntentHints(intent, currentOnly)
		}
		return intent
	}
	if shouldDowngradeLikelyStaleModelIntent(currentText, intent) {
		return callbacks.IntentTraceData{
			DetectedIntent:     "当前消息独立且未命中业务分类",
			MatchedIntentCode:  "unknown_clarify",
			PrimaryIntent:      "unknown_clarify",
			IntentConfidence:   0.55,
			ShouldReply:        true,
			NeedsClarification: true,
			Reason:             appendIntentReason(intent.Reason, "current-turn guard downgraded likely stale context classification"),
		}
	}
	return intent
}

func classifyIntentFromCurrentTurnOnly(currentText string, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, bool) {
	text := strings.TrimSpace(currentText)
	if text == "" {
		return callbacks.IntentTraceData{}, false
	}
	if weatherIntent, ok := deterministicWeatherIntent(text); ok {
		return weatherIntent, true
	}
	if safetyIntent, ok := deterministicRuntimeIntentOverride(text); ok {
		return safetyIntent, true
	}
	if variableIntent, ok := deterministicHotelVariableIntentOverride(text); ok {
		return variableIntent, true
	}
	var best *intentConfigMatch
	for _, cfg := range collapseIntentConfigsByScope(configs) {
		score, reason := scoreIntentConfig(cfg, text, text)
		if score <= 0 {
			continue
		}
		match := &intentConfigMatch{Config: cfg, Confidence: score, Reason: reason, ScopeRank: intentScopeRank(cfg)}
		if best == nil || betterIntentConfigMatch(match, best) {
			best = match
		}
	}
	if best == nil {
		if isSimpleSocialConfirmText(text) {
			return callbacks.IntentTraceData{
				DetectedIntent:    "轻互动/确认",
				MatchedIntentCode: "social_confirm",
				PrimaryIntent:     "social_confirm",
				SubIntent:         "social",
				IntentConfidence:  0.82,
				ShouldReply:       true,
				Reason:            "current-turn social confirm guard",
			}, true
		}
		return callbacks.IntentTraceData{}, false
	}
	intent := intentTraceFromConfig(best.Config, best.Confidence, "current-turn only "+best.Reason, text)
	intent = refineRuntimeIntentByCurrentText(intent, text)
	intent = enforceHumanRouteFlagByIntentCategory(intent)
	return intent, true
}

func shouldCurrentOnlyIntentOverrideModel(currentOnly callbacks.IntentTraceData, modelIntent callbacks.IntentTraceData) bool {
	if currentOnly.PrimaryIntent == "" || modelIntent.PrimaryIntent == "" || currentOnly.PrimaryIntent == modelIntent.PrimaryIntent {
		return false
	}
	if currentOnly.PrimaryIntent == "human_complaint_risk" && currentOnly.SubIntent == "emergency_safety" {
		return true
	}
	if modelIntent.PrimaryIntent == "human_complaint_risk" && !currentOnly.NeedsHumanRoute {
		return true
	}
	if modelIntent.NeedsHumanRoute && !currentOnly.NeedsHumanRoute {
		return true
	}
	if modelIntent.PrimaryIntent == "hotel_info" && currentOnly.PrimaryIntent == "hotel_variable" {
		return true
	}
	if modelIntent.PrimaryIntent == "hotel_info" && currentOnly.PrimaryIntent == "social_confirm" {
		return true
	}
	if modelIntent.PrimaryIntent == "human_complaint_risk" && currentOnly.PrimaryIntent == "hotel_info" {
		return true
	}
	return false
}

func mergeCurrentOnlyIntentHints(intent callbacks.IntentTraceData, currentOnly callbacks.IntentTraceData) callbacks.IntentTraceData {
	if currentOnly.SubIntent != "" && currentOnly.SubIntent != intent.SubIntent {
		intent.SubIntent = currentOnly.SubIntent
		intent.Reason = appendIntentReason(intent.Reason, "current-turn guard adjusted subIntent")
	}
	if currentOnly.ResourceType != "" {
		intent.ResourceType = currentOnly.ResourceType
	}
	if currentOnly.ResourceAction != "" {
		intent.ResourceAction = currentOnly.ResourceAction
	}
	if currentOnly.NeedsKnowledge {
		intent.NeedsKnowledge = true
	}
	if currentOnly.NeedsResource {
		intent.NeedsResource = true
	}
	if currentOnly.NeedsTool {
		intent.NeedsTool = true
		intent.ToolCodes = appendIfMissing(intent.ToolCodes, currentOnly.ToolCodes...)
	}
	return intent
}

func shouldDowngradeLikelyStaleModelIntent(currentText string, intent callbacks.IntentTraceData) bool {
	if isAmbiguousContinuationText(currentText) {
		return false
	}
	switch intent.PrimaryIntent {
	case "human_complaint_risk":
		return !hasCurrentHumanRiskSignal(currentText)
	case "hotel_info", "hotel_variable", "service_request":
		return !hasCurrentBusinessSignal(currentText)
	default:
		return false
	}
}

func isAmbiguousContinuationText(text string) bool {
	compact := normalizeConfiguredIntentText(text)
	if compact == "" {
		return false
	}
	if len([]rune(compact)) <= 4 {
		return true
	}
	return containsAnyNormalized(compact, []string{"这个", "那个", "刚才", "上面", "前面", "继续", "还是", "还没", "还不", "那怎么办", "这样呢"})
}

func hasCurrentHumanRiskSignal(text string) bool {
	compact := normalizeConfiguredIntentText(text)
	if compact == "" {
		return false
	}
	return isEmergencySafetyText(compact) || containsAnyNormalized(compact, []string{"转人工", "人工", "真人", "投诉", "赔偿", "退款", "退钱", "差评", "报警", "隐私", "身份证", "订单异常", "房型不对", "价格不一样", "贵了", "便宜"})
}

func hasCurrentBusinessSignal(text string) bool {
	compact := normalizeConfiguredIntentText(text)
	if compact == "" {
		return false
	}
	return hasCurrentHumanRiskSignal(compact) || detectHotelVariableResourceType(compact) != "" || containsAnyNormalized(compact, []string{
		"wifi", "wi-fi", "无线", "网络", "发票", "专票", "普票", "停车", "早餐", "早饭", "退房", "入住", "小程序", "定位", "地址", "电话",
		"电视", "投屏", "小爱", "空调", "热水", "洗衣", "用品", "拖鞋", "牙刷", "纸巾", "浴巾", "浴帽", "矿泉水", "送水", "打扫", "保洁", "维修", "行李", "叫醒",
	})
}

func isSimpleSocialConfirmText(text string) bool {
	compact := normalizeConfiguredIntentText(text)
	if compact == "" {
		return false
	}
	return containsAnyNormalized(compact, []string{"谢谢", "感谢", "好的", "好", "嗯", "可以", "不用了", "先不用", "没事", "你好", "你也好"})
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

func looksLikeExplicitHandoffRequest(text string) bool {
	text = normalizeConfiguredIntentText(text)
	return containsAnyNormalized(text, []string{"转人工", "找人工", "人工客服", "真人客服", "真人", "客服", "找个人", "找你们人", "人工处理"})
}

func isExplicitHandoffSubIntent(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "explicit_handoff", "request_human", "human_request", "manual_service", "customer_service", "handoff_request":
		return true
	default:
		return false
	}
}

func promptForModelDetectedIntent(intent callbacks.IntentTraceData, configs []models.ReplyIntentConfig) callbacks.IntentPromptTraceData {
	if config, ok := findIntentConfigByCode(configs, intent.PrimaryIntent); ok {
		return promptTraceFromConfig(config, intent)
	}
	return selectIntentPromptPack(intent)
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
	case "hotel_info", "service_request", "human_complaint_risk", "social_confirm", "unknown_clarify", "hotel_variable":
		return true
	default:
		return false
	}
}

func normalizeHotelVariableResourceAction(action string, currentText string) (string, string) {
	action = strings.TrimSpace(action)
	switch action {
	case "provide_phone":
		return "phone", action
	case "provide_location":
		return "location", action
	case "send_miniprogram":
		return "mini_program", action
	case "provide_store_group":
		return "store_group", action
	}
	resourceType := detectHotelVariableResourceType(currentText)
	switch resourceType {
	case "phone":
		return resourceType, "provide_phone"
	case "location":
		return resourceType, "provide_location"
	case "mini_program":
		return resourceType, "send_miniprogram"
	default:
		return "store_variable", "provide_store_variable"
	}
}
