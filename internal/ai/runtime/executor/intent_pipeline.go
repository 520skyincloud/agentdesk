package executor

import (
	"context"
	"strings"

	"agent-desk/internal/ai/replyengine"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
)

type runtimePipelinePlan struct {
	Normalize     callbacks.NormalizeTraceData
	Intent        callbacks.IntentTraceData
	PromptSelect  callbacks.IntentPromptTraceData
	Context       callbacks.ContextBuildTraceData
	ReplyPlan     callbacks.ReplyPlanTraceData
	ToolKnowledge callbacks.ToolKnowledgeTraceData
	Generate      callbacks.GenerateTraceData
	Validate      callbacks.ValidateTraceData
	Prompt        string
}

func buildRuntimePipelinePlan(req RunInput, history adapter.HistoryBuildResult) runtimePipelinePlan {
	return buildRuntimePipelinePlanWithModel(context.Background(), req, history, nil)
}

func buildRuntimePipelinePlanWithModel(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, detector runtimeIntentModelDetector) runtimePipelinePlan {
	currentText := strings.TrimSpace(req.UserMessage.Content)
	intent, promptPack, configured := detectRuntimeIntentWithModel(ctx, req, history, detector)
	if !configured {
		intent, promptPack, configured = matchConfiguredRuntimeIntent(req, history)
	}
	if !configured {
		intent = detectRuntimeIntent(req, history)
		promptPack = selectIntentPromptPack(intent)
	}
	if mediaIntent, ok := recentMediaFollowUpIntent(req, history); ok && shouldMediaFollowUpOverrideDetectedIntent(intent, req.UserMessage.Content) {
		intent = mediaIntent
		promptPack = selectIntentPromptPack(intent)
		configured = true
	}
	contextTrace := buildContextTrace(req, history, intent)
	toolKnowledge := buildToolKnowledgeTrace(intent)
	replyPlan := buildReplyPlan(intent, promptPack)
	prompt := buildIntentStagePrompt(promptPack, replyPlan)
	return runtimePipelinePlan{
		Normalize: callbacks.NormalizeTraceData{
			CurrentUserText:      preview(currentText, 240),
			CurrentMessageType:   string(req.UserMessage.MessageType),
			RecentMessageCount:   len(history.Messages),
			CompressedMemory:     history.MemorySource,
			MediaContextDetected: contextTrace.MediaContextCount > 0,
		},
		Intent:        intent,
		PromptSelect:  promptPack,
		Context:       contextTrace,
		ToolKnowledge: toolKnowledge,
		ReplyPlan:     replyPlan,
		Generate: callbacks.GenerateTraceData{
			Policy: "只负责自然表达 ReplyPlan，不重新决定业务流程、资源发送或人工路由。",
			Status: "pending",
		},
		Validate: callbacks.ValidateTraceData{
			Rules: []string{"answer_current_question", "no_fake_commitment", "no_ocr_dump", "no_unasked_media_reply", "natural_short_reply"},
		},
		Prompt: prompt,
	}
}

func shouldMediaFollowUpOverrideDetectedIntent(intent callbacks.IntentTraceData, currentText string) bool {
	if !intent.ShouldReply {
		return false
	}
	latestText := latestBurstMessageText(currentText)
	if latestText == "" {
		latestText = currentText
	}
	if intent.PrimaryIntent == "human_complaint_risk" {
		return !hasCurrentHumanRiskSignal(latestText)
	}
	if intent.PrimaryIntent == "hotel_variable" {
		return false
	}
	if intent.NeedsResource || intent.NeedsHumanRoute {
		return false
	}
	return true
}

func buildToolKnowledgeTrace(intent callbacks.IntentTraceData) callbacks.ToolKnowledgeTraceData {
	policy := "当前意图无需额外工具或知识检索；继续携带完整上下文。"
	if intent.NeedsKnowledge || intent.NeedsTool || intent.NeedsResource || intent.NeedsHumanRoute {
		policy = "只按当前意图调用必要知识、门店变量、工具或人工路由；没有结果时不能编造或假装已执行。"
	}
	return callbacks.ToolKnowledgeTraceData{
		Policy:             policy,
		ExpectedResources:  expectedIntentResources(intent),
		KnowledgeTriggered: intent.NeedsKnowledge,
		ToolTriggered:      intent.NeedsTool || intent.NeedsResource || intent.NeedsHumanRoute,
	}
}

func detectRuntimeIntent(req RunInput, history adapter.HistoryBuildResult) callbacks.IntentTraceData {
	if isMediaOnlyWithoutActionableIntent(req.UserMessage) && !hasAdjacentTextMediaQuestion(req, history) {
		return callbacks.IntentTraceData{DetectedIntent: "普通媒体无明确诉求", MatchedIntentCode: "context_media_gate", PrimaryIntent: "context_media", SubIntent: "media_only_no_question", IntentConfidence: 0.9, ShouldReply: false, Reason: "media context gate: media-only message has no actionable intent"}
	}
	if isActionableMediaMessage(req.UserMessage) {
		return callbacks.IntentTraceData{DetectedIntent: "媒体上下文包含可行动问题", MatchedIntentCode: "unknown_clarify", PrimaryIntent: "unknown_clarify", SubIntent: "actionable_media_context", IntentConfidence: 0.55, ShouldReply: true, NeedsClarification: true, Reason: "parsed image/file context contains actionable question or error but no business intent matched"}
	}
	if hasRecentMediaFollowUpContext(req, history) {
		return callbacks.IntentTraceData{DetectedIntent: "上下文后续追问", MatchedIntentCode: "social_confirm", PrimaryIntent: "social_confirm", SubIntent: "media_context_follow_up", IntentConfidence: 0.7, ShouldReply: true, Reason: "recent parsed image/file context follow-up but no configured classification matched"}
	}
	return callbacks.IntentTraceData{
		DetectedIntent:     "未能确定用户意图",
		MatchedIntentCode:  "unknown_clarify",
		PrimaryIntent:      "unknown_clarify",
		IntentConfidence:   0.35,
		ShouldReply:        true,
		NeedsClarification: true,
		Reason:             "no enabled intent classification matched",
	}
}

func selectIntentPromptPack(intent callbacks.IntentTraceData) callbacks.IntentPromptTraceData {
	name := intent.PrimaryIntent
	instructions := []string{
		"先按当前用户问题回答，历史只作辅助。",
		"房号只代表短期当前会话上下文；如果房号来自旧消息、压缩记忆或不确定是否仍在当前房间，必须重新确认，不能直接断言。",
		"不要假承诺已安排、已通知、已处理。",
		"回复像微信真人，通常 1-3 句。",
	}
	switch intent.PrimaryIntent {
	case "context_media":
		instructions = append(instructions, "图片/文件本身无明确诉求时只记录为上下文资产，不主动回复。")
	case "hotel_info":
		instructions = append(instructions, "酒店规则、设施、WiFi、发票、用品、停车、早餐等都属于酒店信息大分类。", "必须使用当前门店知识库或门店资料，不编造门店规则。")
	case "hotel_variable":
		instructions = append(instructions, "电话、定位、小程序属于当前门店账号变量。", "只读取当前企微员工号绑定门店的变量，不查询知识库，不编造号码、定位或链接。")
	case "service_request":
		instructions = append(instructions, "服务请求先看知识库是否有自助路径。", "无法解决时按托管模式和排班进入人工路由，不承诺已安排。")
	case "human_complaint_risk":
		if intent.SubIntent == "emergency_safety" {
			instructions = append(instructions, "突发安全/受伤风险必须按接待路由转人工。", "先安抚、提醒不要移动；如停不下来或流血严重，提示先拨打 120/报警。", "缺房号/位置时追问当前位置，但不要因此阻断人工路由。")
		} else {
			instructions = append(instructions, "按当前门店托管模式和排班处理人工、投诉和风险。", "不要口头假装已经通知或处理完成。", "普通设施/设备问题若知识库命中，知识库优先，不要反复诱导转人工。")
		}
	case "social_confirm":
		if intent.SubIntent == "media_context_follow_up" {
			instructions = append(instructions, "当前问题是在追问最近图片/文件解析文本；直接结合上下文回答用户问法，不机械复述解析全文，不说系统识别。语音仍按既有语转文文本链路处理。")
		} else {
			instructions = append(instructions, "自然短句回应，别只回哈哈，语气不要淡。")
		}
	case "unknown_clarify":
		instructions = append(instructions, "低置信度时只追问一个关键点，不乱查知识、不乱转人工。")
	default:
		instructions = append(instructions, "未匹配到启用意图分类时，只围绕当前问题短答或追问一个关键点，不调用知识、资源或人工路由。")
	}
	return callbacks.IntentPromptTraceData{PackName: name, Instructions: instructions}
}

func buildContextTrace(req RunInput, history adapter.HistoryBuildResult, intent callbacks.IntentTraceData) callbacks.ContextBuildTraceData {
	mediaCount := 0
	for _, item := range history.RawItems {
		if isRuntimeMediaMessage(item.MessageType) {
			mediaCount++
		}
	}
	if isRuntimeMediaMessage(req.UserMessage.MessageType) {
		mediaCount++
	}
	return callbacks.ContextBuildTraceData{
		CurrentTurn:             preview(req.UserMessage.Content, 240),
		RecentRawMessageCount:   len(history.Messages),
		CompressedMemorySource:  history.MemorySource,
		CompressedMemoryCount:   history.MemoryItemCount,
		MediaContextCount:       mediaCount,
		Priority:                []string{"currentTurn", "recentRawMessages", "mediaContext", "compressedMemory", "intentResources", "knowledge"},
		IntentResourcesExpected: expectedIntentResources(intent),
	}
}

func buildReplyPlan(intent callbacks.IntentTraceData, prompt callbacks.IntentPromptTraceData) callbacks.ReplyPlanTraceData {
	goal := "回答当前用户问题"
	doNot := []string{"不要假承诺", "不要答非所问", "不要长篇模板化"}
	switch intent.PrimaryIntent {
	case "context_media":
		goal = "不回复，只记录图片/文件上下文资产"
	case "social_confirm":
		if intent.SubIntent == "media_context_follow_up" {
			goal = "结合最近图片/文件解析文本回答用户追问"
			doNot = append(doNot, "不要复述 OCR", "不要只描述图片不回答问题")
		} else if intent.SubIntent == "weather_query" || intent.ResourceAction == "get_weather" {
			goal = "调用天气工具回答闲聊型天气查询"
			doNot = append(doNot, "不要说查不到", "不要让用户自己看手机天气")
		}
	case "hotel_info":
		goal = "基于当前门店知识库回答酒店信息问题"
	case "hotel_variable":
		goal = "按当前门店账号变量满足用户请求"
	case "service_request":
		goal = "给出自助路径或按策略引导人工，不承诺执行"
	case "human_complaint_risk":
		if intent.SubIntent == "emergency_safety" {
			goal = "处理突发安全/受伤风险并进入接待路由"
		} else {
			goal = "按托管模式处理人工、投诉或风险诉求"
		}
	}
	return callbacks.ReplyPlanTraceData{Intent: intent.PrimaryIntent, AnswerGoal: goal, UseContext: []string{"currentTurn", "recentMessages", "compressedMemory", "mediaContext", "intentResources"}, DoNot: doNot, Style: "自然微信口吻，1-3句"}
}

func buildIntentStagePrompt(prompt callbacks.IntentPromptTraceData, plan callbacks.ReplyPlanTraceData) string {
	if len(prompt.Instructions) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("【当前意图专项规则】\n")
	for _, item := range prompt.Instructions {
		item = strings.TrimSpace(item)
		if item != "" {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
	}
	b.WriteString("【回复计划】\n")
	b.WriteString("目标：" + plan.AnswerGoal + "\n")
	b.WriteString("上下文优先级：当前问题 > 最近原始上下文 > 媒体理解 > 压缩记忆 > 当前意图资源/知识库。必须持续参考上下文，但不要让无关旧信息盖过当前问题。\n")
	if plan.Intent == "social_confirm" && strings.Contains(plan.AnswerGoal, "图片/文件") {
		b.WriteString("图片/文件上下文追问：如果当前问题是‘这是啥/这是干嘛的/什么意思/怎么样/你看’这类短指代，默认衔接最近一条已解析的图片或文件文本；直接结合上下文回答用户问法，不要把它当成无上下文问题。语音仍按既有语转文文本链路处理。\n")
	}
	b.WriteString("房号时效：房号只能来自当前连续会话的近期原文；压缩记忆或旧消息里的房号不能直接当作当前房间，必须重新确认。\n")
	if len(plan.DoNot) > 0 {
		b.WriteString("禁止：" + strings.Join(plan.DoNot, "；") + "\n")
	}
	b.WriteString("风格：" + plan.Style)
	return strings.TrimSpace(b.String())
}

func expectedIntentResources(intent callbacks.IntentTraceData) []string {
	ret := make([]string, 0)
	if intent.NeedsKnowledge {
		ret = append(ret, "knowledge")
	}
	if intent.NeedsResource {
		ret = append(ret, "store/account resource")
	}
	if intent.ResourceAction != "" {
		ret = append(ret, "resource action:"+intent.ResourceAction)
	}
	if intent.NeedsHumanRoute {
		ret = append(ret, "handoff confirmation policy")
	}
	return ret
}

func isMediaOnlyWithoutActionableIntent(message models.Message) bool {
	if !isRuntimeMediaMessage(message.MessageType) {
		return false
	}
	mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
	text := strings.TrimSpace(strings.Join([]string{mediaText, mediaSummary}, " "))
	if status != "understood" {
		return false
	}
	if text == "" {
		return false
	}
	if replyengine.MediaUnderstandingExplicitlyNoIntent(text) {
		return true
	}
	return !replyengine.MediaUnderstandingHasActionableIntent(text)
}

func isActionableMediaMessage(message models.Message) bool {
	if !isRuntimeMediaMessage(message.MessageType) {
		return false
	}
	mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
	if status != "understood" {
		return false
	}
	return replyengine.MediaUnderstandingHasActionableIntent(strings.Join([]string{mediaText, mediaSummary}, " "))
}

func hasRecentMediaContext(history adapter.HistoryBuildResult) bool {
	for i := len(history.RawItems) - 1; i >= 0; i-- {
		item := history.RawItems[i]
		if isRuntimeMediaMessage(item.MessageType) {
			_, _, status := utils.RuntimeMediaUnderstandingFromPayload(item.Payload)
			return status == "understood" || strings.TrimSpace(item.Payload) != ""
		}
	}
	return false
}

func hasRecentMediaFollowUpContext(req RunInput, history adapter.HistoryBuildResult) bool {
	if isRuntimeMediaMessage(req.UserMessage.MessageType) {
		return false
	}
	followUpText := latestBurstMessageText(req.UserMessage.Content)
	if followUpText == "" {
		followUpText = req.UserMessage.Content
	}
	if !replyengine.LooksLikeMediaFollowUp(followUpText) {
		return false
	}
	if isClearlyIndependentMediaFollowUpText(replyengine.NormalizeIntentText(followUpText)) {
		return false
	}
	return hasRecentUsableMediaContext(history)
}

func recentMediaFollowUpIntent(req RunInput, history adapter.HistoryBuildResult) (callbacks.IntentTraceData, bool) {
	if !hasRecentMediaFollowUpContext(req, history) {
		return callbacks.IntentTraceData{}, false
	}
	return callbacks.IntentTraceData{
		DetectedIntent:    "上下文后续追问",
		MatchedIntentCode: "social_confirm",
		PrimaryIntent:     "social_confirm",
		SubIntent:         "media_context_follow_up",
		IntentConfidence:  0.86,
		ShouldReply:       true,
		NeedsKnowledge:    false,
		NeedsResource:     false,
		NeedsHumanRoute:   false,
		Reason:            "current short/deictic question should attach to recent understood media context",
	}, true
}

func isClearlyIndependentMediaFollowUpText(compact string) bool {
	if compact == "" {
		return false
	}
	return replyengine.ContainsAny(compact,
		"早餐", "停车", "发票", "押金", "退房", "入住时间", "会员", "wifi", "wi-fi", "无线网", "洗衣", "健身房", "餐厅",
		"发定位", "酒店定位", "门店定位", "导航", "怎么去", "酒店地址", "我要办入住", "办理入住", "办入住", "小程序", "安心宿",
		"送水", "拖鞋", "牙刷", "纸巾", "维修", "打扫", "保洁", "投诉", "转人工", "人工客服",
	)
}

func latestBurstMessageText(content string) string {
	content = strings.TrimSpace(content)
	if content == "" || !strings.Contains(content, "客人刚才连续发了几条消息") {
		return content
	}
	lines := strings.Split(content, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if idx := strings.Index(line, "]"); idx >= 0 && idx+1 < len(line) {
			line = strings.TrimSpace(line[idx+1:])
		}
		if line != "" {
			return line
		}
	}
	return content
}

func hasRecentUsableMediaContext(history adapter.HistoryBuildResult) bool {
	for i := len(history.RawItems) - 1; i >= 0; i-- {
		item := history.RawItems[i]
		if item.SenderType != "" && item.SenderType != enums.IMSenderTypeCustomer {
			continue
		}
		if !isRuntimeMediaMessage(item.MessageType) {
			continue
		}
		mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(item.Payload)
		if strings.TrimSpace(status) == "understood" && (strings.TrimSpace(mediaText) != "" || strings.TrimSpace(mediaSummary) != "") {
			return true
		}
	}
	return false
}

func hasAdjacentTextMediaQuestion(req RunInput, history adapter.HistoryBuildResult) bool {
	if !isRuntimeMediaMessage(req.UserMessage.MessageType) {
		return false
	}
	for i := len(history.RawItems) - 1; i >= 0; i-- {
		item := history.RawItems[i]
		if item.SenderType != enums.IMSenderTypeCustomer {
			continue
		}
		if item.MessageType != enums.IMMessageTypeText && item.MessageType != enums.IMMessageTypeHTML {
			continue
		}
		text := strings.TrimSpace(item.Content)
		if text == "" {
			continue
		}
		return replyengine.LooksLikeMediaFollowUp(text)
	}
	return false
}

func isRuntimeMediaMessage(messageType enums.IMMessageType) bool {
	switch messageType {
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeVideo, enums.IMMessageTypeAttachment, enums.IMMessageTypeGIF:
		return true
	default:
		return false
	}
}

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
