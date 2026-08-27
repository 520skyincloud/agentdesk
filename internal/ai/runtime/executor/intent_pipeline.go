package executor

import (
	"context"
	"strconv"
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
	currentText := currentRuntimeIntentSemanticText(req)
	intent, promptPack, configured := detectRuntimeIntentWithModel(ctx, req, history, detector)
	if !configured {
		intent = intentDetectUnavailableIntent("IntentDetect model unavailable; entering interaction")
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
		Prompt: prompt,
	}
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
			instructions = append(instructions, "客户在表达不满但没有明确要求人工或投诉升级；先自然道歉一句，然后回到当前可解决的问题，不主动触发转人工。")
		} else if intent.SubIntent == "clarify" || intent.NeedsClarification {
			instructions = append(instructions, "当前表达不明确时，只追问一个关键点；不要乱查知识、乱取变量或乱转人工。")
		} else {
			instructions = append(instructions, "所有闲聊、玩笑、感谢、确认、表情和非业务互动都归本类；自然短句接住当前话题，别只回哈哈，语气不要淡。")
		}
	default:
		instructions = append(instructions, "未匹配到启用意图分类时，只围绕当前问题短答或追问一个关键点，不调用知识、资源或人工路由。")
	}
	return appendSpatialFactInstruction(callbacks.IntentPromptTraceData{PackName: name, Instructions: instructions}, intent)
}

func appendSpatialFactInstruction(prompt callbacks.IntentPromptTraceData, intent callbacks.IntentTraceData) callbacks.IntentPromptTraceData {
	instruction := buildSpatialFactInstruction(intent)
	if instruction == "" {
		return prompt
	}
	for _, existing := range prompt.Instructions {
		if strings.TrimSpace(existing) == instruction {
			return prompt
		}
	}
	prompt.Instructions = append(prompt.Instructions, instruction)
	return prompt
}

func buildSpatialFactInstruction(intent callbacks.IntentTraceData) string {
	if !hasSpatialFactTask(intent) {
		return ""
	}
	return "【仅适用于本轮周边/位置任务】空间事实必须按独立维度使用：地点是否存在、地点名称、具体地址、距离、交通方式、预计时间、完整路线。知识只支持其中一个维度时，只能回答该维度，不能跨维度推断；知道地点名称或存在，不代表很近、可以步行、需要开车或几分钟可到；知道酒店地址，不代表知道最近地铁站、线路、出口、换乘或步行时间。距离、步行/驾车方式、分钟数、公里数、站点、线路、出口和换乘都必须有知识片段直接支持，没有直接证据就不估算、不编造，也不能用地点名称代替客户真正询问的距离或路线。"
}

func hasSpatialFactTask(intent callbacks.IntentTraceData) bool {
	if intent.PrimaryIntent == "hotel_info" && isSpatialFactSubIntent(intent.SubIntent) {
		return true
	}
	for _, task := range intent.IntentTasks {
		if task.Intent == "hotel_info" && isSpatialFactSubIntent(task.SubIntent) {
			return true
		}
	}
	return false
}

func isSpatialFactSubIntent(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "surrounding_facilities", "location_info":
		return true
	default:
		return false
	}
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
		CurrentTurn:             preview(currentRuntimeIntentSemanticText(req), 240),
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
	replyRequiredTaskCount := countReplyRequiredTasks(taskPlans)
	if replyRequiredTaskCount > 1 {
		goal = "按 IntentDetect 子任务顺序分别处理当前轮每个任务"
		style = "自然微信口吻；多任务可以分句或分行逐项回复，不强压成一句"
		doNot = append(doNot, "不要只答主意图或最后一个问题")
	}
	return callbacks.ReplyPlanTraceData{
		Intent:                 intent.PrimaryIntent,
		AnswerGoal:             goal,
		UseContext:             useContext,
		DoNot:                  doNot,
		Style:                  style,
		ActiveTaskCount:        len(taskPlans),
		ReplyRequiredTaskCount: replyRequiredTaskCount,
		TaskPlans:              taskPlans,
	}
}

func buildIntentStagePrompt(prompt callbacks.IntentPromptTraceData, plan callbacks.ReplyPlanTraceData) string {
	if len(prompt.Instructions) == 0 {
		return ""
	}
	structuredOutput := replyPlanRequiresStructuredOutput(plan, false)
	var b strings.Builder
	b.WriteString("【当前意图专项规则】\n")
	for _, item := range prompt.Instructions {
		item = strings.TrimSpace(item)
		if structuredOutput {
			item = normalizeStructuredReplyPromptText(item)
		}
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
			if replyTaskRequiresText(task) {
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
			if strings.TrimSpace(task.TaskID) != "" {
				b.WriteString(task.TaskID)
			} else {
				b.WriteString(strconv.Itoa(i + 1))
			}
			b.WriteString("：")
			if strings.TrimSpace(task.ResolvedText) != "" {
				b.WriteString(task.ResolvedText)
			} else if strings.TrimSpace(task.Text) != "" {
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
			b.WriteString("多个文本任务必须逐题完整回答；具体输出格式以后续任务输出契约为准，不得自行合并、遗漏或增加任务。\n")
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
	style := plan.Style
	if structuredOutput {
		style = normalizeStructuredReplyPromptText(style)
	}
	b.WriteString("风格：" + style)
	result := strings.TrimSpace(b.String())
	if structuredOutput {
		result = normalizeStructuredReplyPromptText(result)
	}
	return result
}

func buildReplyTaskPlans(intent callbacks.IntentTraceData) []callbacks.ReplyTaskPlanTraceData {
	tasks := make([]callbacks.ReplyTaskPlanTraceData, 0, len(intent.IntentTasks)+len(intent.ResourceActions)+1)
	add := func(task callbacks.ReplyTaskPlanTraceData) {
		task.Intent = strings.TrimSpace(task.Intent)
		task.SubIntent = strings.TrimSpace(task.SubIntent)
		task.Objective = semanticGateNormalizeObjective(task.Objective)
		task.RelationToPrevious = semanticGateNormalizeRelation(task.RelationToPrevious)
		task.ResolutionState = semanticGateNormalizeResolution(task.ResolutionState)
		task.OriginalText = strings.TrimSpace(task.OriginalText)
		if task.OriginalText == "" {
			task.OriginalText = strings.TrimSpace(task.Text)
		}
		task.ResolvedText = strings.TrimSpace(task.ResolvedText)
		if task.ResolvedText == "" {
			task.ResolvedText = task.OriginalText
		}
		task.Text = task.ResolvedText
		task.SourceRefs = normalizeRuntimeIntentSourceRefs(task.SourceRefs)
		task.Output = strings.TrimSpace(task.Output)
		task.ResourceAction = strings.TrimSpace(task.ResourceAction)
		if task.OutputKind == "" {
			task.OutputKind = replyTaskOutputKind(task)
		}
		task.ReplyRequired = task.OutputKind == "text"
		if task.Intent == "" && task.Output == "" {
			return
		}
		for index, existing := range tasks {
			if existing.Intent == task.Intent && existing.SubIntent == task.SubIntent && existing.Objective == task.Objective && existing.RelationToPrevious == task.RelationToPrevious && existing.ResolutionState == task.ResolutionState && existing.OriginalText == task.OriginalText && existing.ResolvedText == task.ResolvedText && existing.Output == task.Output && existing.ResourceAction == task.ResourceAction {
				tasks[index].SourceRefs = mergeReplyTaskSourceRefs(existing.SourceRefs, task.SourceRefs)
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
	return finalizeReplyTaskPlans(tasks)
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
		Intent:             task.Intent,
		SubIntent:          task.SubIntent,
		Objective:          task.Objective,
		RelationToPrevious: task.RelationToPrevious,
		ResolutionState:    task.ResolutionState,
		Text:               task.ResolvedText,
		OriginalText:       task.Text,
		ResolvedText:       task.ResolvedText,
		SourceRefs:         append([]string(nil), task.SourceRefs...),
		Output:             output,
		ResourceAction:     task.ResourceAction,
	}
}

func finalizeReplyTaskPlans(tasks []callbacks.ReplyTaskPlanTraceData) []callbacks.ReplyTaskPlanTraceData {
	hasBusinessTask := false
	for _, task := range tasks {
		if task.Intent != "interaction" {
			hasBusinessTask = true
			break
		}
	}
	if hasBusinessTask {
		for index := range tasks {
			target := -1
			if shouldCollapseInteractionTaskIntoContext(tasks[index]) {
				target = nearestBusinessReplyTaskIndex(tasks, index)
			} else if shouldCollapseDuplicateClarifyTask(tasks[index]) {
				target = matchingBusinessReplyTaskIndex(tasks, index)
			}
			if target < 0 {
				continue
			}
			tasks[index].Output = "context_only"
			tasks[index].OutputKind = "context_only"
			tasks[index].ReplyRequired = false
			tasks[target].SourceRefs = mergeReplyTaskSourceRefs(tasks[target].SourceRefs, tasks[index].SourceRefs)
		}
	}
	taskIndex := 0
	contextIndex := 0
	for index := range tasks {
		if tasks[index].OutputKind == "context_only" {
			contextIndex++
			tasks[index].TaskID = "context-" + strconv.Itoa(contextIndex)
			continue
		}
		taskIndex++
		tasks[index].TaskID = "task-" + strconv.Itoa(taskIndex)
	}
	return tasks
}

func shouldCollapseInteractionTaskIntoContext(task callbacks.ReplyTaskPlanTraceData) bool {
	if task.Intent != "interaction" {
		return false
	}
	if task.SubIntent == "clarify" || task.ResolutionState == runtimeIntentResolutionAmbiguous || task.ResolutionState == runtimeIntentResolutionUnresolved {
		return false
	}
	if strings.TrimSpace(task.ResourceAction) != "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(task.Objective), "identity") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(task.SubIntent)) {
	case "correction", "social", "thanks", "thank_you", "greeting", "farewell", "acknowledgement", "acknowledgment", "frustration", "insult_complaint":
		return true
	default:
		return strings.EqualFold(strings.TrimSpace(task.Objective), "social")
	}
}

func shouldCollapseDuplicateClarifyTask(task callbacks.ReplyTaskPlanTraceData) bool {
	return task.Intent == "interaction" &&
		task.SubIntent == "clarify" &&
		task.ResolutionState != runtimeIntentResolutionAmbiguous &&
		task.ResolutionState != runtimeIntentResolutionUnresolved
}

func matchingBusinessReplyTaskIndex(tasks []callbacks.ReplyTaskPlanTraceData, sourceIndex int) int {
	if sourceIndex < 0 || sourceIndex >= len(tasks) {
		return -1
	}
	source := tasks[sourceIndex]
	match := -1
	for index, task := range tasks {
		if index == sourceIndex || task.Intent == "interaction" || !replyTaskSourceRefsOverlap(source.SourceRefs, task.SourceRefs) || !replyTaskTextsOverlap(source, task) {
			continue
		}
		if match >= 0 {
			return -1
		}
		match = index
	}
	return match
}

func replyTaskSourceRefsOverlap(left []string, right []string) bool {
	for _, leftRef := range normalizeRuntimeIntentSourceRefs(left) {
		for _, rightRef := range normalizeRuntimeIntentSourceRefs(right) {
			if leftRef == rightRef {
				return true
			}
		}
	}
	return false
}

func replyTaskTextsOverlap(left callbacks.ReplyTaskPlanTraceData, right callbacks.ReplyTaskPlanTraceData) bool {
	leftTexts := normalizedReplyTaskTexts(left)
	rightTexts := normalizedReplyTaskTexts(right)
	for text := range leftTexts {
		if rightTexts[text] {
			return true
		}
	}
	return false
}

func normalizedReplyTaskTexts(task callbacks.ReplyTaskPlanTraceData) map[string]bool {
	ret := map[string]bool{}
	for _, text := range []string{task.OriginalText, task.ResolvedText} {
		if normalized := normalizeRuntimeKnowledgeQuery(text); normalized != "" {
			ret[normalized] = true
		}
	}
	return ret
}

func nearestBusinessReplyTaskIndex(tasks []callbacks.ReplyTaskPlanTraceData, sourceIndex int) int {
	for distance := 1; distance < len(tasks); distance++ {
		if right := sourceIndex + distance; right < len(tasks) && tasks[right].Intent != "interaction" {
			return right
		}
		if left := sourceIndex - distance; left >= 0 && tasks[left].Intent != "interaction" {
			return left
		}
	}
	return -1
}

func mergeReplyTaskSourceRefs(primary []string, context []string) []string {
	ret := append([]string(nil), primary...)
	for _, ref := range context {
		ret = appendIfMissing(ret, strings.TrimSpace(ref))
	}
	return normalizeRuntimeIntentSourceRefs(ret)
}

func replyTaskOutputKind(task callbacks.ReplyTaskPlanTraceData) string {
	if task.Output == "structured_resource_commit" || task.Intent == "hotel_variable" || strings.TrimSpace(task.ResourceAction) != "" {
		return "resource"
	}
	if task.Output == "human_route_confirmation_or_dispatch" || task.Intent == "human_complaint_risk" {
		return "handoff"
	}
	return "text"
}

func countReplyRequiredTasks(tasks []callbacks.ReplyTaskPlanTraceData) int {
	count := 0
	for _, task := range tasks {
		if replyTaskRequiresText(task) {
			count++
		}
	}
	return count
}

func replyTaskRequiresText(task callbacks.ReplyTaskPlanTraceData) bool {
	switch strings.TrimSpace(task.OutputKind) {
	case "text":
		return true
	case "resource", "handoff", "context_only":
		return false
	}
	if task.ReplyRequired {
		return true
	}
	if task.Output == "structured_resource_commit" || task.Output == "human_route_confirmation_or_dispatch" {
		return false
	}
	return task.Output == "text_reply" || task.Output == "knowledge_text_reply" || task.Intent == "hotel_info" || task.Intent == "interaction"
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
		ret = append(ret, "direct handoff policy")
	}
	return ret
}

func isMediaOnlyWithoutActionableIntent(message models.Message) bool {
	if !isRuntimeMediaMessage(message.MessageType) {
		return false
	}
	mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
	text := preferredMediaUnderstandingText(mediaText, mediaSummary)
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
	return replyengine.MediaUnderstandingHasActionableIntent(preferredMediaUnderstandingText(mediaText, mediaSummary))
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
	if content == "" || !utils.IsRuntimeCustomerBurstEnvelope(content) {
		return content
	}
	items := utils.RuntimeCustomerBurstItems(content)
	if len(items) == 0 {
		return content
	}
	return utils.RuntimeCustomerBurstItemText(items[len(items)-1])
}

func currentTurnDisplayText(content string) string {
	content = strings.TrimSpace(content)
	if content == "" || !utils.IsRuntimeCustomerBurstEnvelope(content) {
		return content
	}
	return utils.RuntimeCustomerBurstDisplayText(content)
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
