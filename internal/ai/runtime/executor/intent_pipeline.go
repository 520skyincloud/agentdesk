package executor

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"agent-desk/internal/ai/replyengine"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"
)

type runtimePipelinePlan struct {
	Normalize           callbacks.NormalizeTraceData
	Intent              callbacks.IntentTraceData
	PromptSelect        callbacks.IntentPromptTraceData
	Context             callbacks.ContextBuildTraceData
	ReplyPlan           callbacks.ReplyPlanTraceData
	ToolKnowledge       callbacks.ToolKnowledgeTraceData
	Generate            callbacks.GenerateTraceData
	Validate            callbacks.ValidateTraceData
	Prompt              string
	PrefetchedKnowledge *retrievers.KnowledgeRetrieveResult
}

func buildRuntimePipelinePlanStrict(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, detector runtimeIntentModelDetector) (runtimePipelinePlan, error) {
	currentText := strings.TrimSpace(req.UserMessage.Content)
	intent, promptPack, configured, err := detectRuntimeIntentWithModelStrict(ctx, req, history, detector)
	if err != nil {
		return runtimePipelinePlan{}, err
	}
	if !configured {
		return runtimePipelinePlan{}, services.NewAIReplyExecutionError(
			services.AIReplyExecutionErrorIntentDetectFailed,
			fmt.Errorf("intent model unavailable"),
		)
	}
	prefetchedKnowledge, err := probeClarifyKnowledge(ctx, req, intent)
	if err != nil {
		return runtimePipelinePlan{}, err
	}
	if prefetchedKnowledge != nil && len(prefetchedKnowledge.Hits) > 0 && strings.TrimSpace(prefetchedKnowledge.ContextText) != "" {
		intent.PrimaryIntent = "hotel_info"
		intent.MatchedIntentCode = "hotel_info"
		intent.DetectedIntent = "hotel_info"
		intent.SubIntent = "store_knowledge"
		intent.NeedsClarification = false
		intent.NeedsKnowledge = true
		intent.ShouldReply = true
		intent.MatchMode = "knowledge_probe"
		intent.Reason = appendIntentReason(intent.Reason, "clarify knowledge probe matched current store knowledge")
		intent.IntentTasks = []callbacks.IntentTaskTraceData{{
			Intent: "hotel_info", SubIntent: "store_knowledge", Text: currentText,
			NeedsKnowledge: true, Reason: "clarify knowledge probe matched",
		}}
		promptPack = selectIntentPromptPack(intent)
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
		Prompt:              prompt,
		PrefetchedKnowledge: prefetchedKnowledge,
	}, nil
}

func probeClarifyKnowledge(ctx context.Context, req RunInput, intent callbacks.IntentTraceData) (*retrievers.KnowledgeRetrieveResult, error) {
	if intent.PrimaryIntent != "interaction" || (strings.TrimSpace(intent.SubIntent) != "clarify" && !intent.NeedsClarification) {
		return nil, nil
	}
	if len(utils.SplitInt64s(req.AIAgent.KnowledgeIDs)) == 0 {
		return nil, nil
	}
	query := strings.TrimSpace(currentTurnDisplayText(req.UserMessage.Content))
	if query == "" {
		return nil, nil
	}
	retriever := retrievers.NewKnowledgeRetriever(req.AIAgent)
	if len(retriever.KnowledgeBaseIDs()) == 0 {
		return nil, nil
	}
	options := retrievers.DefaultKnowledgeRetrieveOptions()
	options.QueryPreview = preview(query, 120)
	result, err := retriever.RetrieveContextByOptions(ctx, options, query)
	if err != nil {
		return nil, services.NewAIReplyExecutionError(services.AIReplyExecutionErrorKnowledgeUnavailable, err)
	}
	if result == nil || len(result.Hits) == 0 || strings.TrimSpace(result.ContextText) == "" {
		return nil, nil
	}
	return result, nil
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

func selectIntentPromptPack(intent callbacks.IntentTraceData) callbacks.IntentPromptTraceData {
	name := intent.PrimaryIntent
	instructions := []string{
		"先按当前用户问题回答，历史只作辅助。",
		"房号只代表短期当前会话上下文；如果房号来自旧消息、压缩记忆或不确定是否仍在当前房间，必须重新确认，不能直接断言。",
		"不要假承诺；没有真实工具、资源提交或接待路由结果时，也不要承诺内部核实、通知转告、登记安排、现场查看、后续跟进或已完成状态。",
		"如果知识库内容写的是让客户联系前台/管家/门店工作人员，只能如实引导客户联系，不能改写成系统已经替客户执行了真实动作。",
		"知识库已经给出答案时必须直接回答，不要说稍后再查、内部确认或回头答复。",
		"最终回复只输出给客人的话，不输出思考过程、规则复述、内部判断依据或知识库治理备注。",
		"回复像微信真人，通常 1-3 句。",
	}
	switch intent.PrimaryIntent {
	case "hotel_info":
		instructions = append(instructions, "酒店规则、设施、WiFi、发票、用品、停车、早餐等都属于酒店信息大分类。", "必须使用当前门店知识库或门店资料，不编造门店规则。")
		if intent.SubIntent == "checkin_process" || intent.SubIntent == "check_in" {
			instructions = append(instructions, "入住流程问题要优先说明小程序/证件/订单核验等知识库写明的办理步骤；不要只反问到店了吗，也不要把它当作小程序变量发送请求。")
		}
	case "hotel_variable":
		instructions = append(instructions, "门店账号变量只由 Commit 阶段读取和发送，不编造变量值。", "只提交本轮 resourceActions 明确需要的变量；Generate 阶段不要描述变量发送状态。")
		if intent.NeedsKnowledge {
			instructions = append(instructions, "本轮同时包含酒店信息问题时，Generate 阶段只回答知识问题；变量消息由系统按 resourceActions 另行提交。")
		}
	case "service_request":
		instructions = append(instructions, "服务请求先看知识库是否有自助路径。", "无法解决时追问一个必要字段或按人工意图/接待路由处理；没有工具或路由结果时，不能表达动作已执行、已转告、现场查看或后续有人处理。", "同轮包含早餐、停车、发票等知识问题时必须直接回答知识结果。")
	case "human_complaint_risk":
		if intent.SubIntent == "emergency_safety" {
			instructions = append(instructions, "突发安全/受伤风险必须按接待路由转人工。", "先安抚、提醒不要移动；如停不下来或流血严重，提示先拨打 120/报警。", "缺房号/位置时追问当前位置，但不要因此阻断人工路由。")
		} else {
			instructions = append(instructions, "按当前门店托管模式和排班处理人工、投诉和风险。", "不要口头假装已经通知或处理完成。", "普通设施/设备问题若知识库命中，知识库优先，不要反复诱导转人工。")
		}
	case "interaction":
		if intent.SubIntent == "media_context_follow_up" {
			instructions = append(instructions, "当前问题是在追问最近图片/文件解析文本；直接结合上下文回答用户问法，不机械复述解析全文，不说系统识别。语音仍按既有语转文文本链路处理。")
		} else if isSocialCorrectionSubIntent(intent.SubIntent) {
			instructions = append(instructions, "当前问题是在纠正或澄清上一轮误会；只接住当前纠正，轻声道歉或确认即可，不要继续补答历史里的电视、早餐、停车、语音等旧主题。")
		} else if intent.SubIntent == "frustration" {
			instructions = append(instructions, "客户在表达不满但没有明确要求人工或投诉升级；先自然道歉一句，然后回到当前可解决的问题，不主动触发转人工确认。")
		} else if intent.SubIntent == "clarify" || intent.NeedsClarification {
			instructions = append(instructions, "当前表达不明确时，只追问一个关键点；不要乱查知识、乱取变量或乱转人工。")
		} else {
			instructions = append(instructions, "所有闲聊、玩笑、感谢、确认、表情和非业务互动都归本类；自然短句接住当前话题，别只回哈哈，语气不要淡。")
		}
	default:
		instructions = append(instructions, "未匹配到启用意图分类时，只围绕当前问题短答或追问一个关键点，不调用知识、资源或人工路由。")
	}
	return callbacks.IntentPromptTraceData{PackName: name, Instructions: instructions}
}

func hasCheckinProcessTask(intent callbacks.IntentTraceData) bool {
	for _, task := range intent.IntentTasks {
		if task.Intent == "hotel_info" && isCheckinProcessSubIntent(task.SubIntent) {
			return true
		}
	}
	return false
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
	useContext := []string{"currentTurn", "recentMessages", "compressedMemory", "mediaContext", "intentResources"}
	taskPlans := buildReplyTaskPlans(intent)
	switch intent.PrimaryIntent {
	case "interaction":
		if intent.SubIntent == "media_context_follow_up" {
			goal = "结合最近图片/文件解析文本回答用户追问"
			doNot = append(doNot, "不要复述 OCR", "不要只描述图片不回答问题")
		} else if isSocialCorrectionSubIntent(intent.SubIntent) {
			goal = "接住客户对上一轮误会的纠正"
			useContext = []string{"currentTurn", "immediatelyPreviousAssistantMessage"}
			doNot = append(doNot, "不要补答旧主题", "不要解释系统内部原因", "不要追问旧业务问题")
		} else if intent.SubIntent == "weather_query" || intent.ResourceAction == "get_weather" {
			goal = "调用天气工具回答闲聊型天气查询"
			doNot = append(doNot, "不要说查不到", "不要让用户自己看手机天气")
		} else if intent.SubIntent == "frustration" {
			goal = "接住客户不满，回到当前问题继续解决"
			doNot = append(doNot, "不要主动转人工", "不要反复确认转人工")
		}
	case "hotel_info":
		goal = "基于当前门店知识库回答酒店信息问题"
	case "hotel_variable":
		if intent.NeedsKnowledge {
			goal = "回答当前轮酒店信息问题，并按当前门店账号变量满足资源请求"
		} else {
			goal = "按当前门店账号变量满足用户请求"
		}
	case "service_request":
		goal = "给出自助路径或按策略引导人工，不承诺执行"
	case "human_complaint_risk":
		if intent.SubIntent == "emergency_safety" {
			goal = "处理突发安全/受伤风险并进入接待路由"
		} else {
			goal = "按托管模式处理人工、投诉或风险诉求"
		}
	}
	style := "自然微信口吻，1-3句"
	if len(taskPlans) > 1 {
		goal = "按 IntentDetect 子任务顺序分别处理当前轮每个任务"
		style = "自然微信口吻；多任务可以分句或分行逐项回复，不强压成一句"
		doNot = append(doNot, "不要只答主意图或最后一个问题")
	}
	return callbacks.ReplyPlanTraceData{Intent: intent.PrimaryIntent, AnswerGoal: goal, UseContext: useContext, DoNot: doNot, Style: style, TaskPlans: taskPlans}
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
	if len(plan.TaskPlans) > 1 {
		generateTasks := make([]callbacks.ReplyTaskPlanTraceData, 0, len(plan.TaskPlans))
		hiddenCommitTaskCount := 0
		for _, task := range plan.TaskPlans {
			if task.Output == "knowledge_text_reply" || task.Intent == "hotel_info" {
				generateTasks = append(generateTasks, task)
				continue
			}
			if task.Output == "structured_resource_commit" || task.Intent == "hotel_variable" || strings.TrimSpace(task.ResourceAction) != "" {
				hiddenCommitTaskCount++
			}
		}
		b.WriteString("【多任务回复计划】\n")
		for i, task := range generateTasks {
			b.WriteString("- 任务")
			b.WriteString(strconv.Itoa(i + 1))
			b.WriteString("：")
			if strings.TrimSpace(task.Text) != "" {
				b.WriteString(task.Text)
				b.WriteString("；")
			}
			b.WriteString("分类=")
			b.WriteString(task.Intent)
			if strings.TrimSpace(task.SubIntent) != "" {
				b.WriteString("/")
				b.WriteString(task.SubIntent)
			}
			if strings.TrimSpace(task.ResourceAction) != "" {
				b.WriteString("；动作=")
				b.WriteString(task.ResourceAction)
			}
			if strings.TrimSpace(task.Output) != "" {
				b.WriteString("；输出=")
				b.WriteString(task.Output)
			}
			b.WriteString("\n")
		}
		if len(generateTasks) == 0 {
			b.WriteString("- 本阶段没有需要生成文本的知识任务。\n")
		}
		if len(generateTasks) > 1 {
			b.WriteString("多个文本任务可以拆成多条客户消息；不同文本消息之间必须用 <<NEXT_MESSAGE>> 单独一行分隔，Commit 阶段会按该标记分别发送。\n")
		}
		if hiddenCommitTaskCount > 0 {
			b.WriteString("另有结构化变量任务已登记给 Commit 阶段，本阶段不要写变量名称、发送状态或后续动作。\n")
		}
		b.WriteString("必须按上面列出的知识/信息任务生成文本回答；结构化变量任务只由 Commit 阶段发送，文本里不要承诺“发你/已经发/后续发”。\n")
	}
	if plan.Intent == "interaction" && strings.Contains(plan.AnswerGoal, "图片/文件") {
		b.WriteString("图片/文件上下文追问：如果当前问题是‘这是啥/这是干嘛的/什么意思/怎么样/你看’这类短指代，默认衔接最近一条已解析的图片或文件文本；直接结合上下文回答用户问法，不要把它当成无上下文问题。语音仍按既有语转文文本链路处理。\n")
	}
	if plan.Intent == "interaction" && strings.Contains(plan.AnswerGoal, "纠正") {
		b.WriteString("纠错输出范围：完整上下文继续保留，但本次生成只用当前纠正和紧邻的上一条客服消息识别误会；只输出一句完整回应，不续答、更换或追问任何旧业务主题。\n")
	}
	if plan.Intent == "hotel_variable" {
		b.WriteString("酒店变量发送：结构化变量只由 Commit 阶段发送。若本轮还有停车、早餐、发票等知识问题，文本回复只回答这些知识问题，不要写变量名称、发送状态或后续动作。\n")
	}
	b.WriteString("房号时效：房号只能来自当前连续会话的近期原文；压缩记忆或旧消息里的房号不能直接当作当前房间，必须重新确认。\n")
	if len(plan.DoNot) > 0 {
		b.WriteString("禁止：" + strings.Join(plan.DoNot, "；") + "\n")
	}
	b.WriteString("风格：" + plan.Style)
	return strings.TrimSpace(b.String())
}

func buildReplyTaskPlans(intent callbacks.IntentTraceData) []callbacks.ReplyTaskPlanTraceData {
	tasks := make([]callbacks.ReplyTaskPlanTraceData, 0, len(intent.IntentTasks)+len(intent.ResourceActions)+1)
	add := func(task callbacks.ReplyTaskPlanTraceData) {
		task.Intent = strings.TrimSpace(task.Intent)
		task.SubIntent = strings.TrimSpace(task.SubIntent)
		task.Text = strings.TrimSpace(task.Text)
		task.Output = strings.TrimSpace(task.Output)
		task.ResourceAction = strings.TrimSpace(task.ResourceAction)
		if task.Intent == "" && task.Output == "" {
			return
		}
		for _, existing := range tasks {
			if existing.Intent == task.Intent && existing.SubIntent == task.SubIntent && existing.Text == task.Text && existing.Output == task.Output && existing.ResourceAction == task.ResourceAction {
				return
			}
		}
		tasks = append(tasks, task)
	}
	for _, item := range intent.IntentTasks {
		add(replyTaskPlanFromIntentTask(item))
	}
	if len(tasks) == 0 {
		for _, action := range intent.ResourceActions {
			add(callbacks.ReplyTaskPlanTraceData{
				Intent:         "hotel_variable",
				SubIntent:      hotelVariableResourceTypeFromAction(action),
				Output:         "structured_resource_commit",
				ResourceAction: action,
			})
		}
	}
	if len(tasks) == 0 && strings.TrimSpace(intent.ResourceAction) != "" {
		add(callbacks.ReplyTaskPlanTraceData{
			Intent:         "hotel_variable",
			SubIntent:      hotelVariableResourceTypeFromAction(intent.ResourceAction),
			Output:         "structured_resource_commit",
			ResourceAction: intent.ResourceAction,
		})
	}
	if len(tasks) == 0 {
		output := "text_reply"
		if intent.NeedsKnowledge {
			output = "knowledge_text_reply"
		}
		if intent.NeedsResource {
			output = "structured_resource_commit"
		}
		if intent.NeedsHumanRoute {
			output = "human_route_confirmation_or_dispatch"
		}
		add(callbacks.ReplyTaskPlanTraceData{
			Intent:         intent.PrimaryIntent,
			SubIntent:      intent.SubIntent,
			Output:         output,
			ResourceAction: intent.ResourceAction,
		})
	}
	return tasks
}

func replyTaskPlanFromIntentTask(task callbacks.IntentTaskTraceData) callbacks.ReplyTaskPlanTraceData {
	output := "text_reply"
	if task.NeedsKnowledge || task.Intent == "hotel_info" {
		output = "knowledge_text_reply"
	}
	if task.NeedsResource || task.Intent == "hotel_variable" || strings.TrimSpace(task.ResourceAction) != "" {
		output = "structured_resource_commit"
	}
	if task.Intent == "human_complaint_risk" {
		output = "human_route_confirmation_or_dispatch"
	}
	return callbacks.ReplyTaskPlanTraceData{
		Intent:         task.Intent,
		SubIntent:      task.SubIntent,
		Text:           task.Text,
		Output:         output,
		ResourceAction: task.ResourceAction,
	}
}

func expectedIntentResources(intent callbacks.IntentTraceData) []string {
	ret := make([]string, 0)
	if intent.NeedsKnowledge {
		ret = append(ret, "knowledge")
	}
	if intent.NeedsResource {
		ret = append(ret, "store/account resource")
	}
	for _, action := range intent.ResourceActions {
		if strings.TrimSpace(action) != "" {
			ret = append(ret, "resource action:"+strings.TrimSpace(action))
		}
	}
	if intent.ResourceAction != "" {
		ret = appendIfMissing(ret, "resource action:"+intent.ResourceAction)
	}
	for _, task := range intent.IntentTasks {
		if strings.TrimSpace(task.Intent) == "" {
			continue
		}
		label := "intent task:" + strings.TrimSpace(task.Intent)
		if strings.TrimSpace(task.SubIntent) != "" {
			label += "/" + strings.TrimSpace(task.SubIntent)
		}
		if strings.TrimSpace(task.ResourceAction) != "" {
			label += ":" + strings.TrimSpace(task.ResourceAction)
		}
		if strings.TrimSpace(task.Text) != "" {
			label += "=" + preview(task.Text, 40)
		}
		ret = append(ret, label)
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

func currentTurnDisplayText(content string) string {
	content = strings.TrimSpace(content)
	if content == "" || !strings.Contains(content, "客人刚才连续发了几条消息") {
		return content
	}
	lines := strings.Split(content, "\n")
	body := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "客人刚才连续发了几条消息") {
			continue
		}
		body = append(body, line)
	}
	if len(body) == 0 {
		return content
	}
	return "本轮客户连续消息（按时间顺序）：\n" + strings.Join(body, "\n")
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
