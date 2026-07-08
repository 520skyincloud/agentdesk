package executor

import (
	"strings"
	"unicode"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"github.com/mlogclub/simple/sqls"
)

type intentConfigMatch struct {
	Config     models.ReplyIntentConfig
	Confidence float64
	Reason     string
	ScopeRank  int
}

type runtimeIntentScope struct {
	CompanyID        int64
	StoreID          int64
	WxWorkInstanceID int64
	CustomerID       int64
}

func matchConfiguredRuntimeIntent(req RunInput, history adapter.HistoryBuildResult) (callbacks.IntentTraceData, callbacks.IntentPromptTraceData, bool) {
	scope := resolveRuntimeIntentScope(req)
	configs := loadEnabledIntentConfigs(scope)
	if len(configs) == 0 {
		return callbacks.IntentTraceData{}, callbacks.IntentPromptTraceData{}, false
	}
	text := strings.TrimSpace(req.UserMessage.Content)
	mediaText := currentAndRecentMediaText(req, history)
	matchText := strings.TrimSpace(strings.Join([]string{text, mediaText, history.MemorySource}, "\n"))
	classificationText := strings.TrimSpace(strings.Join([]string{text, mediaText}, "\n"))
	if isMediaOnlyWithoutActionableIntent(req.UserMessage) && !hasAdjacentTextMediaQuestion(req, history) {
		intent := callbacks.IntentTraceData{DetectedIntent: "普通媒体无明确诉求", MatchedIntentCode: "context_media_gate", PrimaryIntent: "context_media", SubIntent: "media_only_no_question", IntentConfidence: 0.9, ShouldReply: false, Reason: "media context gate: media-only message has no actionable intent"}
		return intent, selectIntentPromptPack(intent), true
	}
	if matchText == "" {
		return callbacks.IntentTraceData{}, callbacks.IntentPromptTraceData{}, false
	}
	currentTurnOverride, hasCurrentTurnOverride := deterministicRuntimeIntentOverride(text)
	variableOverride, hasVariableOverride := deterministicHotelVariableIntentOverride(text)
	var best *intentConfigMatch
	for _, config := range configs {
		if !intentContextMatches(config, req, history, mediaText) {
			continue
		}
		confidence, reason := scoreIntentConfig(config, text, classificationText)
		if confidence <= 0 {
			continue
		}
		candidate := &intentConfigMatch{Config: config, Confidence: confidence, Reason: reason, ScopeRank: intentScopeRank(config)}
		if best == nil || betterIntentConfigMatch(candidate, best) {
			best = candidate
		}
	}
	if best == nil {
		if hasCurrentTurnOverride {
			prompt := selectIntentPromptPack(currentTurnOverride)
			return currentTurnOverride, prompt, true
		}
		if hasVariableOverride {
			prompt := selectIntentPromptPack(variableOverride)
			return variableOverride, prompt, true
		}
		if fallback, ok := findUnknownOrClarifyConfig(configs); ok {
			intent := intentTraceFromConfig(fallback, 0.45, "no configured classification matched with enough confidence", classificationText)
			intent.ShouldReply = true
			prompt := promptTraceFromConfig(fallback, intent)
			return intent, prompt, true
		}
		return callbacks.IntentTraceData{}, callbacks.IntentPromptTraceData{}, false
	}
	intent := intentTraceFromConfig(best.Config, best.Confidence, best.Reason, classificationText)
	usedCurrentTurnOverride := false
	if hasCurrentTurnOverride && shouldCurrentTurnOverrideConfiguredIntent(currentTurnOverride, intent) {
		intent = currentTurnOverride
		usedCurrentTurnOverride = true
	} else if hasVariableOverride && intent.PrimaryIntent != "human_complaint_risk" {
		intent = variableOverride
		usedCurrentTurnOverride = true
	} else {
		intent = refineRuntimeIntentByCurrentText(intent, text)
		intent = applyHotelVariableMixedKnowledge(intent, text)
	}
	intent = enforceHumanRouteFlagByIntentCategory(intent)
	if usedCurrentTurnOverride {
		return intent, selectIntentPromptPack(intent), true
	}
	prompt := promptTraceFromConfig(best.Config, intent)
	return intent, prompt, true
}

func shouldCurrentTurnOverrideConfiguredIntent(override callbacks.IntentTraceData, configured callbacks.IntentTraceData) bool {
	if override.PrimaryIntent == "human_complaint_risk" && override.SubIntent == "emergency_safety" {
		return configured.PrimaryIntent != "hotel_variable"
	}
	return false
}

func deterministicRuntimeIntentOverride(currentText string) (callbacks.IntentTraceData, bool) {
	text := normalizeConfiguredIntentText(currentText)
	if text == "" {
		return callbacks.IntentTraceData{}, false
	}
	if isEmergencySafetyText(text) {
		return callbacks.IntentTraceData{
			DetectedIntent:    "突发安全/受伤风险",
			MatchedIntentCode: "human_complaint_risk",
			PrimaryIntent:     "human_complaint_risk",
			SubIntent:         "emergency_safety",
			IntentConfidence:  0.98,
			ShouldReply:       true,
			NeedsHumanRoute:   true,
			HumanRoutePolicy:  "emergency_safety",
			Reason:            "deterministic current-turn emergency safety signal matched",
		}, true
	}
	return callbacks.IntentTraceData{}, false
}

func deterministicHotelVariableIntentOverride(currentText string) (callbacks.IntentTraceData, bool) {
	resourceType := detectExplicitHotelVariableResourceType(currentText)
	if resourceType == "" {
		return callbacks.IntentTraceData{}, false
	}
	intent := callbacks.IntentTraceData{
		DetectedIntent:    "酒店变量",
		MatchedIntentCode: "hotel_variable",
		PrimaryIntent:     "hotel_variable",
		SubIntent:         resourceType,
		IntentConfidence:  0.9,
		ShouldReply:       true,
		NeedsResource:     true,
		ResourceType:      resourceType,
		ResourceAction:    hotelVariableResourceAction(resourceType),
		Reason:            "deterministic current-turn hotel variable request matched",
	}
	return applyHotelVariableMixedKnowledge(intent, currentText), true
}

func detectExplicitHotelVariableResourceType(text string) string {
	text = normalizeConfiguredIntentText(text)
	switch {
	case containsAnyNormalized(text, []string{"发定位", "定位发", "定位给", "酒店定位", "门店定位", "发地址", "地址发", "导航发", "导航给"}):
		return "location"
	case containsAnyNormalized(text, []string{"小程序", "安心宿", "入住码", "办理入住", "自助入住"}):
		return "mini_program"
	case containsAnyNormalized(text, []string{"电话多少", "电话发", "发电话", "号码多少", "联系方式", "联系电话"}):
		return "phone"
	default:
		return ""
	}
}

func hotelVariableResourceAction(resourceType string) string {
	switch resourceType {
	case "phone":
		return "provide_phone"
	case "location":
		return "provide_location"
	case "mini_program":
		return "send_miniprogram"
	default:
		return "provide_store_variable"
	}
}

func refineRuntimeIntentByCurrentText(intent callbacks.IntentTraceData, currentText string) callbacks.IntentTraceData {
	text := normalizeConfiguredIntentText(currentText)
	if text == "" {
		return intent
	}
	if isEmergencySafetyText(text) {
		intent.PrimaryIntent = "human_complaint_risk"
		intent.MatchedIntentCode = "human_complaint_risk"
		intent.SubIntent = "emergency_safety"
		intent.NeedsHumanRoute = true
		intent.NeedsKnowledge = false
		intent.HumanRoutePolicy = "emergency_safety"
		intent.IntentConfidence = maxFloat(intent.IntentConfidence, 0.98)
		intent.Reason = strings.TrimSpace(intent.Reason + "; current-turn emergency safety override")
		return intent
	}
	return intent
}

func applyHotelVariableMixedKnowledge(intent callbacks.IntentTraceData, currentText string) callbacks.IntentTraceData {
	if intent.PrimaryIntent != "hotel_variable" {
		return intent
	}
	hotelInfoSubIntent := detectRuntimeSubIntent("hotel_info", currentText)
	if hotelInfoSubIntent == "" || hotelInfoSubIntent == "store_knowledge" {
		return intent
	}
	intent.NeedsKnowledge = true
	intent.Reason = appendIntentReason(intent.Reason, "current-turn mixed hotel_info requires knowledge")
	return intent
}

func isEmergencySafetyText(text string) bool {
	return containsAnyNormalized(text, []string{"摔倒", "摔跤", "滑倒", "受伤", "流血", "出血", "骨折", "晕倒", "昏倒", "报警", "救护车", "120", "安全事故", "人身安全", "厕所太滑", "地太滑"})
}

func maxFloat(a float64, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func loadEnabledIntentConfigs(scope runtimeIntentScope) []models.ReplyIntentConfig {
	var list []models.ReplyIntentConfig
	db := sqls.DB()
	if db == nil {
		return list
	}
	err := db.Where("status = ?", enums.StatusOk).
		Where("(scope_type = ? OR (scope_type = ? AND company_id = ?) OR (scope_type = ? AND store_id = ?) OR (scope_type = ? AND wx_work_instance_id = ?))", "global", "company", scope.CompanyID, "store", scope.StoreID, "instance", scope.WxWorkInstanceID).
		Order("priority DESC").Order("sort_no ASC").Order("id ASC").Find(&list).Error
	if err != nil {
		return nil
	}
	return collapseIntentConfigsByScope(list)
}

func scoreIntentConfig(config models.ReplyIntentConfig, currentText string, matchText string) (float64, string) {
	if anyTermMatches(config.NegativeExamples, matchText) {
		return 0, "negative example matched"
	}
	mode := strings.TrimSpace(config.MatchMode)
	if mode == "" {
		mode = "hybrid"
	}
	keywordHit := anyTermMatches(config.Keywords, matchText)
	exampleHit := anyTermMatches(config.PositiveExamples, matchText)
	switch mode {
	case "keyword":
		if keywordHit {
			return 0.88, "configured keyword matched"
		}
	case "example":
		if exampleHit {
			return 0.82, "configured positive example matched"
		}
	case "regex":
		// 第一版不执行用户输入正则，避免错误正则拖垮 runtime；按普通关键词处理。
		if keywordHit || exampleHit {
			return 0.82, "configured regex-mode term matched as safe text"
		}
	default:
		if keywordHit && exampleHit {
			return 0.94, "configured keyword and example matched"
		}
		if keywordHit {
			return 0.88, "configured keyword matched"
		}
		if exampleHit {
			return 0.82, "configured positive example matched"
		}
	}
	if strings.TrimSpace(config.Keywords) == "" && strings.TrimSpace(config.PositiveExamples) == "" && strings.TrimSpace(currentText) != "" {
		return 0.55, "configured catch-all intent matched"
	}
	return 0, ""
}

func intentTraceFromConfig(config models.ReplyIntentConfig, confidence float64, reason string, matchText string) callbacks.IntentTraceData {
	toolCodes := splitIntentTerms(config.ToolCodes)
	primaryIntent, subIntent, resourceType, resourceAction := canonicalRuntimeIntent(config.Code, config.ResourceType, matchText, strings.Join([]string{config.Keywords, config.PositiveExamples}, "\n"))
	needsKnowledge := config.NeedsKnowledge
	needsResource := config.NeedsResource
	if primaryIntent == "hotel_variable" {
		needsKnowledge = false
		needsResource = true
	}
	needsHumanRoute := config.NeedsHumanRoute && primaryIntent == "human_complaint_risk"
	return callbacks.IntentTraceData{
		DetectedIntent:    strings.TrimSpace(config.Name),
		MatchedIntentCode: primaryIntent,
		PrimaryIntent:     primaryIntent,
		SubIntent:         subIntent,
		IntentConfidence:  confidence,
		ShouldReply:       !config.NoReplyWhenMatched,
		NeedsKnowledge:    needsKnowledge,
		NeedsTool:         config.NeedsTool,
		NeedsResource:     needsResource,
		NeedsHumanRoute:   needsHumanRoute,
		ResourceType:      resourceType,
		ResourceAction:    resourceAction,
		ToolCodes:         toolCodes,
		HumanRoutePolicy:  strings.TrimSpace(config.HumanRoutePolicy),
		MatchedConfigID:   config.ID,
		MatchedConfig:     strings.TrimSpace(config.Name),
		MatchMode:         strings.TrimSpace(config.MatchMode),
		Reason:            strings.TrimSpace("intent classification: " + reason),
	}
}

func promptTraceFromConfig(config models.ReplyIntentConfig, intent callbacks.IntentTraceData) callbacks.IntentPromptTraceData {
	instructions := []string{
		"先按当前用户问题回答，历史只作辅助。",
		"不要假承诺已安排、已通知、已处理。",
		"回复像微信真人，通常 1-3 句。",
	}
	if promptPack := strings.TrimSpace(config.PromptPack); promptPack != "" {
		instructions = append(instructions, splitIntentLines(promptPack)...)
	}
	if template := strings.TrimSpace(config.ReplyPlanTemplate); template != "" {
		instructions = append(instructions, "回复计划模板："+template)
	}
	if validation := strings.TrimSpace(config.ValidationRules); validation != "" {
		instructions = append(instructions, "发送前校验："+validation)
	}
	return callbacks.IntentPromptTraceData{PackName: intent.PrimaryIntent, Instructions: instructions}
}

func betterIntentConfigMatch(candidate *intentConfigMatch, current *intentConfigMatch) bool {
	if candidate.Confidence != current.Confidence {
		return candidate.Confidence > current.Confidence
	}
	if candidate.Config.Priority != current.Config.Priority {
		return candidate.Config.Priority > current.Config.Priority
	}
	if candidate.Config.SortNo != current.Config.SortNo {
		return candidate.Config.SortNo < current.Config.SortNo
	}
	if candidate.ScopeRank != current.ScopeRank {
		return candidate.ScopeRank > current.ScopeRank
	}
	return candidate.Config.ID < current.Config.ID
}

func collapseIntentConfigsByScope(list []models.ReplyIntentConfig) []models.ReplyIntentConfig {
	selected := make(map[string]models.ReplyIntentConfig, len(list))
	order := make([]string, 0, len(list))
	for _, item := range list {
		code := strings.TrimSpace(item.Code)
		if code == "" {
			continue
		}
		current, exists := selected[code]
		if !exists {
			selected[code] = item
			order = append(order, code)
			continue
		}
		if intentScopeRank(item) > intentScopeRank(current) {
			selected[code] = item
		}
	}
	ret := make([]models.ReplyIntentConfig, 0, len(order))
	for _, code := range order {
		ret = append(ret, selected[code])
	}
	return ret
}

func intentScopeRank(config models.ReplyIntentConfig) int {
	switch strings.TrimSpace(config.ScopeType) {
	case "instance":
		return 4
	case "store":
		return 3
	case "company":
		return 2
	default:
		return 1
	}
}

func findUnknownOrClarifyConfig(configs []models.ReplyIntentConfig) (models.ReplyIntentConfig, bool) {
	for _, config := range configs {
		if canonicalIntentCode(config.Code) == "unknown_clarify" {
			return config, true
		}
	}
	return models.ReplyIntentConfig{}, false
}

func canonicalRuntimeIntent(code string, resourceType string, currentText string, configHints string) (string, string, string, string) {
	primary := canonicalIntentCode(code)
	subIntent := detectRuntimeSubIntent(primary, currentText)
	resourceType = strings.TrimSpace(resourceType)
	resourceAction := ""
	if primary == "hotel_variable" {
		detectedResourceType := detectHotelVariableResourceType(currentText)
		if detectedResourceType == "" {
			detectedResourceType = detectHotelVariableResourceType(configHints)
		}
		if resourceType == "" || resourceType == "store_variable" {
			resourceType = detectedResourceType
		}
		switch resourceType {
		case "phone":
			resourceAction = "provide_phone"
		case "location":
			resourceAction = "provide_location"
		case "mini_program":
			resourceAction = "send_miniprogram"
		default:
			resourceAction = "provide_store_variable"
		}
		if resourceType != "" {
			subIntent = resourceType
		}
	}
	if subIntent == "" && strings.TrimSpace(currentText) == "" {
		subIntent = detectRuntimeSubIntent(primary, strings.Join([]string{code, resourceType, configHints}, "\n"))
	}
	return primary, subIntent, resourceType, resourceAction
}

func canonicalIntentCode(code string) string {
	switch strings.TrimSpace(code) {
	case "hotel_info":
		return "hotel_info"
	case "service_request":
		return "service_request"
	case "human_complaint_risk":
		return "human_complaint_risk"
	case "handoff", "complaint_or_risk":
		return "human_complaint_risk"
	case "social_confirm":
		return "social_confirm"
	case "unknown_clarify":
		return "unknown_clarify"
	case "hotel_variable":
		return "hotel_variable"
	default:
		return strings.TrimSpace(code)
	}
}

func detectRuntimeSubIntent(primary string, text string) string {
	text = normalizeConfiguredIntentText(text)
	switch primary {
	case "hotel_info":
		switch {
		case containsAnyNormalized(text, []string{"wifi", "wi-fi", "无线", "网络", "网连不上"}):
			return "network_wifi"
		case containsAnyNormalized(text, []string{"发票", "专票", "普票", "抬头", "报销"}):
			return "invoice"
		case containsAnyNormalized(text, []string{"送水", "拿水", "拖鞋", "牙刷", "纸巾", "浴巾", "毛巾", "浴帽", "用品"}):
			return "supplies_self_help"
		case containsAnyNormalized(text, []string{"停车", "车位", "停车场"}):
			return "parking"
		case containsAnyNormalized(text, []string{"早餐", "早饭"}):
			return "breakfast"
		default:
			return "store_knowledge"
		}
	case "hotel_variable":
		return detectHotelVariableResourceType(text)
	}
	return ""
}

func detectHotelVariableResourceType(text string) string {
	text = normalizeConfiguredIntentText(text)
	switch {
	case containsAnyNormalized(text, []string{"电话", "号码", "联系", "客服"}):
		return "phone"
	case containsAnyNormalized(text, []string{"定位", "地址", "导航", "在哪", "哪里", "怎么去"}):
		return "location"
	case containsAnyNormalized(text, []string{"小程序", "安心宿", "入住码", "办理入住", "自助入住"}):
		return "mini_program"
	default:
		return ""
	}
}

func containsAnyNormalized(text string, values []string) bool {
	for _, value := range values {
		if value != "" && strings.Contains(text, normalizeConfiguredIntentText(value)) {
			return true
		}
	}
	return false
}

func resolveRuntimeIntentScope(req RunInput) runtimeIntentScope {
	scope := runtimeIntentScope{CustomerID: req.Conversation.CustomerID}
	db := sqls.DB()
	if db == nil {
		return scope
	}
	if req.Conversation.ID <= 0 || !db.Migrator().HasTable(&models.ConversationRouteState{}) {
		return scope
	}
	state := repositories.ConversationRouteStateRepository.Take(db, "conversation_id = ?", req.Conversation.ID)
	if state != nil {
		scope.StoreID = state.StoreID
		scope.WxWorkInstanceID = state.WxWorkInstanceID
	}
	if scope.WxWorkInstanceID > 0 {
		instance := repositories.WxWorkProtocolInstanceRepository.Get(db, scope.WxWorkInstanceID)
		if instance != nil {
			if instance.CompanyID > 0 {
				scope.CompanyID = instance.CompanyID
			}
			if scope.StoreID <= 0 {
				scope.StoreID = instance.StoreID
			}
			if scope.StoreID > 0 && scope.CompanyID <= 0 {
				store := repositories.StoreRepository.Get(db, scope.StoreID)
				if store != nil {
					scope.CompanyID = store.CompanyID
				}
			}
		}
	}
	if scope.StoreID > 0 && scope.CompanyID <= 0 {
		store := repositories.StoreRepository.Get(db, scope.StoreID)
		if store != nil {
			scope.CompanyID = store.CompanyID
		}
	}
	return scope
}

func intentContextMatches(config models.ReplyIntentConfig, req RunInput, history adapter.HistoryBuildResult, mediaText string) bool {
	required := strings.ToLower(strings.TrimSpace(config.RequiredContext))
	if required == "" {
		return true
	}
	terms := splitIntentTerms(required)
	for _, term := range terms {
		switch strings.ToLower(term) {
		case "media", "image", "图片", "媒体":
			if !isRuntimeMediaMessage(req.UserMessage.MessageType) && !hasRecentMediaContext(history) && strings.TrimSpace(mediaText) == "" {
				return false
			}
		case "text", "文本":
			if strings.TrimSpace(req.UserMessage.Content) == "" {
				return false
			}
		case "memory", "summary", "记忆", "压缩记忆":
			if strings.TrimSpace(history.MemorySource) == "" {
				return false
			}
		}
	}
	return true
}

func currentAndRecentMediaText(req RunInput, history adapter.HistoryBuildResult) string {
	parts := make([]string, 0)
	if isRuntimeMediaMessage(req.UserMessage.MessageType) {
		mediaText, mediaSummary, _ := utilsMediaUnderstanding(req.UserMessage)
		parts = append(parts, mediaText, mediaSummary)
	}
	for i := len(history.RawItems) - 1; i >= 0 && len(parts) < 8; i-- {
		item := history.RawItems[i]
		if !isRuntimeMediaMessage(item.MessageType) {
			continue
		}
		mediaText, mediaSummary, _ := utilsMediaUnderstanding(item)
		parts = append(parts, mediaText, mediaSummary)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func utilsMediaUnderstanding(message models.Message) (string, string, string) {
	return utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
}

func anyTermMatches(patterns string, text string) bool {
	text = normalizeConfiguredIntentText(text)
	if text == "" {
		return false
	}
	for _, term := range splitIntentTerms(patterns) {
		term = normalizeConfiguredIntentText(term)
		if term == "" {
			continue
		}
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func splitIntentTerms(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == '，' || r == ';' || r == '；' || r == '、' || r == '|'
	})
}

func splitIntentLines(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == '\r' })
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			ret = append(ret, part)
		}
	}
	return ret
}

func normalizeConfiguredIntentText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
}
