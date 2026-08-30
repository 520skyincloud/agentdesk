package executor

import (
	"context"
	"fmt"
	"strings"

	"agent-desk/internal/ai/replyengine"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"

	"github.com/cloudwego/eino/schema"
)

func buildRunMessages(ctx context.Context, req RunInput, summary *RunResult, collector *callbacks.RuntimeTraceCollector, gate *KnowledgeAnswerabilityGate) []*schema.Message {
	history := adapter.BuildHistoryMessages(req.Conversation.ID, req.UserMessage.ID, 0)
	if summary != nil {
		summary.HistoryMessageCount = len(history.Messages)
		summary.ContextMemorySource = history.MemorySource
		summary.ContextMemoryMessageCount = history.MemoryItemCount
	}
	if collector != nil {
		collector.Data.Input.HistoryMessageCount = len(history.Messages)
		collector.Data.Input.ContextMemorySource = history.MemorySource
		collector.Data.Input.ContextMemoryMessageCount = history.MemoryItemCount
		collector.Data.Input.KnowledgeBaseIDs = utils.SplitInt64s(req.AIAgent.KnowledgeIDs)
		collector.Data.Input.CurrentUserMessagePreview = preview(currentRuntimeIntentSemanticText(req), 120)
	}
	plan := buildRuntimePipelinePlanWithModel(ctx, req, history, nil)
	if collector != nil {
		collector.SetPipeline(plan.Normalize, plan.Intent, plan.PromptSelect, plan.Context, plan.ToolKnowledge, plan.ReplyPlan, plan.Generate, plan.Validate)
		collector.SetActionLedger(buildInitialActionLedger(plan.Intent))
	}
	messages := make([]*schema.Message, 0, len(history.Messages)+3)
	if history.MemoryMessage != nil {
		messages = append(messages, history.MemoryMessage)
	}
	messages = append(messages, history.Messages...)
	if instruction := buildRecentMediaContextInstruction(req, history, plan.Intent); strings.TrimSpace(instruction) != "" {
		messages = append(messages, schema.SystemMessage(instruction))
	}
	if instruction := buildWeatherToolInstruction(plan.Intent); strings.TrimSpace(instruction) != "" {
		messages = append(messages, schema.SystemMessage(instruction))
	}
	if instruction := buildAutoHandoffDisabledInstruction(req, plan.Intent); strings.TrimSpace(instruction) != "" {
		messages = append(messages, schema.SystemMessage(instruction))
	}
	if !plan.Intent.ShouldReply {
		if summary != nil {
			summary.ReplyText = ""
			summary.SkipReply = true
		}
		return messages
	}
	retrievedContext := appendRetrievedContext(ctx, req, plan.Intent, summary, collector, gate, &messages)
	activeReplyPlan := plan.ReplyPlan
	hasDeferredKnowledge := false
	if collector != nil {
		activeReplyPlan = collector.Data.Pipeline.ReplyPlan
		hasDeferredKnowledge = collector.Data.Pipeline.EvidenceJudge.DeferredHandoff
		if isolatedPlan, taskIDs := isolateUngroundedKnowledgeReplyTasks(activeReplyPlan); len(taskIDs) > 0 {
			activeReplyPlan = isolatedPlan
			collector.Data.Pipeline.ReplyPlan = isolatedPlan
			collector.Data.Pipeline.Validate.Reason = appendValidationReason(
				collector.Data.Pipeline.Validate.Reason,
				"isolated ungrounded knowledge task(s) while preserving independent executable tasks: "+strings.Join(taskIDs, ","),
			)
		}
	}
	if instruction := buildCurrentTurnBoundaryInstructionForReplyPlan(req, history, plan.Intent, activeReplyPlan); strings.TrimSpace(instruction) != "" {
		messages = append(messages, schema.SystemMessage(instruction))
	}
	messages = buildGenerateStageMessages(req, history, plan.Intent, activeReplyPlan, messages, retrievedContext.RawKnowledgeContextMessages)
	if prompt := buildIntentStagePrompt(plan.PromptSelect, activeReplyPlan); strings.TrimSpace(prompt) != "" {
		messages = append(messages, schema.SystemMessage(prompt))
	}
	if instruction := buildMultiReplyOutputInstruction(activeReplyPlan, hasDeferredKnowledge); strings.TrimSpace(instruction) != "" {
		messages = append(messages, schema.SystemMessage(instruction))
	}
	appendReplyTagContext(req, plan.Intent, activeReplyPlan, retrievedContext.AnswerabilityStatus, collector, &messages)
	if instruction := buildGenerationScopeInstruction(plan.Intent, activeReplyPlan); strings.TrimSpace(instruction) != "" {
		messages = append(messages, schema.SystemMessage(instruction))
	}
	messages = append(messages, schema.UserMessage(buildActiveGenerationUserMessageText(
		currentRuntimeIntentSemanticText(req),
		plan.Intent,
		activeReplyPlan,
		hasDeferredKnowledge,
	)))
	return messages
}

func buildActiveGenerationUserMessageText(currentText string, intent callbacks.IntentTraceData, plan callbacks.ReplyPlanTraceData, hasDeferredKnowledge bool) string {
	taskPlans := activeGenerationTaskPlans(intent, plan)
	activeTasks := make([]string, 0, len(taskPlans))
	for _, task := range taskPlans {
		text := activeGenerationTaskText(task)
		if text != "" {
			activeTasks = appendIfMissing(activeTasks, text)
		}
	}
	if len(activeTasks) > 0 {
		return strings.Join(activeTasks, "\n")
	}
	if hasDeferredKnowledge || len(plan.TaskPlans) > 0 {
		return "当前没有需要 Generate 输出的文本任务。"
	}
	return buildGenerationUserMessageText(currentText, intent)
}

func buildGenerateStageMessages(req RunInput, history adapter.HistoryBuildResult, intent callbacks.IntentTraceData, plan callbacks.ReplyPlanTraceData, messages []*schema.Message, rawKnowledgeContextMessages []*schema.Message) []*schema.Message {
	excluded := make(map[*schema.Message]struct{}, len(history.Messages)+1)
	if history.MemoryMessage != nil {
		excluded[history.MemoryMessage] = struct{}{}
	}
	for _, message := range history.Messages {
		if message != nil {
			excluded[message] = struct{}{}
		}
	}
	for _, message := range rawKnowledgeContextMessages {
		if message != nil {
			excluded[message] = struct{}{}
		}
	}

	ret := make([]*schema.Message, 0, len(messages)+1)
	for _, message := range messages {
		if message == nil {
			continue
		}
		if _, exists := excluded[message]; exists {
			continue
		}
		ret = append(ret, message)
	}
	if contextMessage := buildBoundedGenerationConversationContext(history, activeGenerationTaskPlans(intent, plan)); contextMessage != nil {
		ret = append(ret, contextMessage)
	}
	if contextMessage := buildActiveGenerationTaskContext(req, intent, plan); contextMessage != nil {
		ret = append(ret, contextMessage)
	}
	return ret
}

type boundedGenerationHistoryEntry struct {
	speaker string
	text    string
}

func buildBoundedGenerationConversationContext(history adapter.HistoryBuildResult, taskPlans []callbacks.ReplyTaskPlanTraceData) *schema.Message {
	adjacentTaskIDs := make([]string, 0, len(taskPlans))
	recapTaskIDs := make([]string, 0, len(taskPlans))
	for index, task := range taskPlans {
		taskMode := generationConversationContextMode(task)
		if taskMode == "" {
			continue
		}
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			taskID = fmt.Sprintf("task-%d", index+1)
		}
		if taskMode == "recap" {
			recapTaskIDs = appendIfMissing(recapTaskIDs, taskID)
		} else {
			adjacentTaskIDs = appendIfMissing(adjacentTaskIDs, taskID)
		}
	}
	if len(adjacentTaskIDs) == 0 && len(recapTaskIDs) == 0 {
		return nil
	}

	entries := boundedGenerationHistoryEntries(history)
	if len(entries) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("【当前任务所需的有界会话上下文】下列历史只供明确标注的任务使用，不会重新成为待回答任务；不得补答、复述或续写其他旧问题。\n")
	if len(adjacentTaskIDs) > 0 {
		b.WriteString("相邻上下文适用任务：")
		b.WriteString(strings.Join(adjacentTaskIDs, "、"))
		b.WriteString("。只用下面最多两条消息理解当前短答、指代、纠正或槽位答案，最终仍只回答当前活跃任务。\n")
		appendBoundedGenerationHistoryEntries(&b, adjacentGenerationHistoryEntries(entries))
	}
	if len(recapTaskIDs) > 0 {
		b.WriteString("会话回顾上下文适用任务：")
		b.WriteString(strings.Join(recapTaskIDs, "、"))
		b.WriteString("。可以按时间顺序概括下面最多八条最近消息；不要声称客户此前没有提问，也不要带出未列出的更早内容。\n")
		appendBoundedGenerationHistoryEntries(&b, lastBoundedGenerationHistoryEntries(entries, 8))
	}
	return schema.SystemMessage(strings.TrimSpace(b.String()))
}

func appendBoundedGenerationHistoryEntries(b *strings.Builder, entries []boundedGenerationHistoryEntry) {
	if b == nil {
		return
	}
	for _, entry := range entries {
		b.WriteString("- ")
		b.WriteString(entry.speaker)
		b.WriteString("：")
		b.WriteString(entry.text)
		b.WriteString("\n")
	}
}

func generationConversationContextMode(task callbacks.ReplyTaskPlanTraceData) string {
	if looksLikeConversationRecapTask(task) {
		return "recap"
	}
	relation := strings.ToLower(strings.TrimSpace(task.RelationToPrevious))
	resolution := strings.ToLower(strings.TrimSpace(task.ResolutionState))
	if resolution == runtimeIntentResolutionResolvedFromContext {
		if len(task.SourceRefs) > 1 && relation == "independent" {
			return ""
		}
		return "adjacent"
	}
	switch relation {
	case "clarification_answer", "reference_previous", "correction", "modify_previous", "cancel_previous", "answer_rejected":
		return "adjacent"
	default:
		return ""
	}
}

func looksLikeConversationRecapTask(task callbacks.ReplyTaskPlanTraceData) bool {
	switch strings.ToLower(strings.TrimSpace(task.SubIntent)) {
	case "conversation_recap", "conversation_summary", "conversation_history", "recap":
		return true
	}
	for _, text := range []string{task.OriginalText, task.Text, task.ResolvedText} {
		compact := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "，", "", ",", "", "？", "", "?", "").Replace(strings.ToLower(strings.TrimSpace(text)))
		if compact == "" {
			continue
		}
		if replyengine.ContainsAny(compact,
			"刚刚都问了什么", "刚刚都问你什么", "刚才都问了什么", "刚才都问你什么", "刚刚问了什么", "刚才问了什么",
			"刚刚聊了什么", "刚才聊了什么", "刚刚说了什么", "刚才说了什么", "刚刚回答了什么", "刚才回答了什么",
			"刚刚你回答了什么", "刚才你回答了什么", "前面聊了什么", "前面问了什么", "前面回答了什么",
		) {
			return true
		}
	}
	return false
}

func boundedGenerationHistoryEntries(history adapter.HistoryBuildResult) []boundedGenerationHistoryEntry {
	entries := make([]boundedGenerationHistoryEntry, 0, len(history.Messages))
	for _, message := range history.Messages {
		if message == nil {
			continue
		}
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		speaker := ""
		switch {
		case strings.Contains(text, "[人工客服]"):
			speaker = "人工客服"
		case strings.Contains(text, "[AI客服]"):
			speaker = "AI客服"
		case strings.Contains(text, "[客户]"):
			speaker = "客户"
		case message.Role == schema.User:
			speaker = "客户"
		case message.Role == schema.Assistant:
			speaker = "AI客服"
		default:
			continue
		}
		if strings.HasPrefix(text, "[历史消息]") {
			if end := strings.Index(text, "] "); end >= 0 {
				text = strings.TrimSpace(text[end+2:])
			}
		}
		if text == "" {
			continue
		}
		entries = append(entries, boundedGenerationHistoryEntry{speaker: speaker, text: preview(text, 1200)})
	}
	return entries
}

func adjacentGenerationHistoryEntries(entries []boundedGenerationHistoryEntry) []boundedGenerationHistoryEntry {
	if len(entries) == 0 {
		return nil
	}
	assistantIndex := len(entries) - 1
	if entries[assistantIndex].speaker != "AI客服" && entries[assistantIndex].speaker != "人工客服" {
		return lastBoundedGenerationHistoryEntries(entries, 2)
	}

	ret := make([]boundedGenerationHistoryEntry, 0, 2)
	for index := assistantIndex - 1; index >= 0; index-- {
		if entries[index].speaker == "客户" {
			ret = append(ret, entries[index])
			break
		}
	}
	ret = append(ret, entries[assistantIndex])
	return ret
}

func lastBoundedGenerationHistoryEntries(entries []boundedGenerationHistoryEntry, limit int) []boundedGenerationHistoryEntry {
	if limit <= 0 || len(entries) == 0 {
		return nil
	}
	if len(entries) <= limit {
		return append([]boundedGenerationHistoryEntry(nil), entries...)
	}
	return append([]boundedGenerationHistoryEntry(nil), entries[len(entries)-limit:]...)
}

func buildActiveGenerationTaskContext(req RunInput, intent callbacks.IntentTraceData, plan callbacks.ReplyPlanTraceData) *schema.Message {
	taskPlans := activeGenerationTaskPlans(intent, plan)
	if len(taskPlans) == 0 {
		return nil
	}

	currentSources := adapter.BuildCurrentTurnSources(req.UserMessage)
	sourceTexts := make([]string, 0, len(currentSources))
	sourceByRef := make(map[string]string, len(currentSources))
	for index, source := range currentSources {
		text := strings.TrimSpace(source.Text)
		if text != "" {
			ref := strings.TrimSpace(source.Ref)
			if ref == "" {
				ref = fmt.Sprintf("U%d", index+1)
			}
			sourceTexts = append(sourceTexts, text)
			sourceByRef[ref] = text
		}
	}

	var b strings.Builder
	b.WriteString("【当前活跃回答任务】以下内容是 Generate 可使用的当前轮来源、补全问题和 Judge 已确认事实。更早原始历史与长期记忆默认不进入 Generate；只有明确依赖上下文的任务可以使用单独提供的有界会话上下文。不得补答未列出的旧问题，也不得根据一个事实推导未确认能力。\n")
	for taskIndex, task := range taskPlans {
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			taskID = fmt.Sprintf("task-%d", taskIndex+1)
		}
		b.WriteString("\n任务 ")
		b.WriteString(taskID)
		b.WriteString("：\n")

		originalText := strings.TrimSpace(task.OriginalText)
		if originalText == "" {
			originalText = strings.TrimSpace(task.Text)
		}
		resolvedText := strings.TrimSpace(task.ResolvedText)
		if resolvedText == "" {
			resolvedText = strings.TrimSpace(task.Text)
		}
		if resolvedText == "" {
			resolvedText = originalText
		}

		primarySource, contextSources := activeGenerationTaskSources(task.SourceRefs, sourceByRef)
		if primarySource == "" {
			primarySource = originalText
		}
		if primarySource == "" && len(sourceTexts) == 1 {
			primarySource = strings.TrimSpace(sourceTexts[0])
		}
		if primarySource != "" {
			b.WriteString("- primary 来源：")
			b.WriteString(primarySource)
			b.WriteString("\n")
		}
		for _, source := range contextSources {
			b.WriteString("- context 来源：")
			b.WriteString(source)
			b.WriteString("\n")
		}
		if resolvedText != "" {
			b.WriteString("- 自包含问题：")
			b.WriteString(resolvedText)
			b.WriteString("\n")
		}
		for factIndex, fact := range task.SupportedFacts {
			statement := strings.TrimSpace(fact.Statement)
			if statement == "" {
				continue
			}
			factID := strings.TrimSpace(fact.FactID)
			if factID == "" {
				factID = fmt.Sprintf("F%d", factIndex+1)
			}
			b.WriteString("- 已确认事实 ")
			b.WriteString(factID)
			b.WriteString("：")
			b.WriteString(statement)
			if values := compactGenerationContextStrings(fact.CriticalValues); len(values) > 0 {
				b.WriteString("；必要值：")
				b.WriteString(strings.Join(values, "、"))
			}
			b.WriteString("\n")
		}
		if missing := compactGenerationContextStrings(task.MissingAspects); len(missing) > 0 {
			b.WriteString("- 尚未确认方面（禁止自行补全）：")
			b.WriteString(strings.Join(missing, "、"))
			b.WriteString("\n")
		}
	}
	return schema.SystemMessage(strings.TrimSpace(b.String()))
}

func activeGenerationTaskPlans(intent callbacks.IntentTraceData, plan callbacks.ReplyPlanTraceData) []callbacks.ReplyTaskPlanTraceData {
	taskPlans := plan.TaskPlans
	if len(taskPlans) == 0 {
		taskPlans = buildReplyTaskPlans(intent)
	}
	ret := make([]callbacks.ReplyTaskPlanTraceData, 0, len(taskPlans))
	for _, task := range taskPlans {
		if replyTaskRequiresText(task) {
			ret = append(ret, task)
		}
	}
	return ret
}

func activeGenerationTaskText(task callbacks.ReplyTaskPlanTraceData) string {
	for _, text := range []string{task.ResolvedText, task.Text, task.OriginalText} {
		if text = strings.TrimSpace(text); text != "" {
			return text
		}
	}
	return ""
}

func activeGenerationTaskSources(refs []string, sourceByRef map[string]string) (string, []string) {
	primary := ""
	contexts := make([]string, 0, len(refs))
	for _, ref := range refs {
		source := strings.TrimSpace(sourceByRef[strings.TrimSpace(ref)])
		if source == "" {
			continue
		}
		if primary == "" {
			primary = source
			continue
		}
		contexts = appendIfMissing(contexts, source)
	}
	return primary, contexts
}

func compactGenerationContextStrings(values []string) []string {
	ret := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			ret = appendIfMissing(ret, value)
		}
	}
	return ret
}

func buildAutoHandoffDisabledInstruction(req RunInput, intent callbacks.IntentTraceData) string {
	if !intent.NeedsHumanRoute || !isHandoffIntentCategory(intent) {
		return ""
	}
	if services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabledForConversation(req.Conversation.ID) {
		return ""
	}
	return "【当前会话接待边界】当前客户在此企微员工号下不允许自动转人工。不要提及任何内部设置，不要发起转人工、通知、派发或承诺同事处理。继续直接回答当前问题；若涉及紧急安全情况，优先给出可立即执行的自我保护和 120/110 建议。"
}

func buildGenerationUserMessageText(currentText string, intent callbacks.IntentTraceData) string {
	defaultText := strings.TrimSpace(currentTurnDisplayText(currentText))
	if !intent.NeedsKnowledge || (!intent.NeedsResource && len(intent.ResourceActions) == 0) {
		return defaultText
	}
	taskPlans := buildReplyTaskPlans(intent)
	knowledgeTasks := make([]string, 0, len(taskPlans))
	for _, task := range taskPlans {
		if task.Output != "knowledge_text_reply" && task.Intent != "hotel_info" {
			continue
		}
		label := strings.TrimSpace(task.Text)
		if label == "" {
			label = runtimeTaskDisplayLabel(task.SubIntent)
		}
		if label != "" {
			knowledgeTasks = appendIfMissing(knowledgeTasks, label)
		}
	}
	if len(knowledgeTasks) == 0 {
		return defaultText
	}
	return strings.Join(knowledgeTasks, "\n")
}

func buildGenerationScopeInstruction(intent callbacks.IntentTraceData, replyPlan callbacks.ReplyPlanTraceData) string {
	if !intent.NeedsKnowledge || (!intent.NeedsResource && len(intent.ResourceActions) == 0) {
		return ""
	}
	taskPlans := replyPlan.TaskPlans
	if len(taskPlans) == 0 {
		taskPlans = buildReplyTaskPlans(intent)
	}
	knowledgeTasks := make([]string, 0, len(taskPlans))
	resourceTasks := make([]string, 0, len(taskPlans))
	for _, task := range taskPlans {
		if task.Output == "knowledge_text_reply" || task.Intent == "hotel_info" {
			label := strings.TrimSpace(task.Text)
			if label == "" {
				label = runtimeTaskDisplayLabel(task.SubIntent)
			}
			if label != "" {
				knowledgeTasks = appendIfMissing(knowledgeTasks, label)
			}
		}
		if task.Output == "structured_resource_commit" || task.Intent == "hotel_variable" || strings.TrimSpace(task.ResourceAction) != "" {
			label := strings.TrimSpace(task.ResourceAction)
			if label == "" {
				label = strings.TrimSpace(task.SubIntent)
			}
			if label != "" {
				resourceTasks = appendIfMissing(resourceTasks, label)
			}
		}
	}
	if len(knowledgeTasks) == 0 {
		knowledgeTasks = append(knowledgeTasks, "当前轮酒店信息问题")
	}
	parts := []string{
		"【Generate 输出范围】当前轮同时包含酒店信息任务和酒店变量任务。",
		"本阶段只输出酒店信息任务的文本答案：" + strings.Join(knowledgeTasks, "、") + "。",
	}
	_ = resourceTasks
	textOutputSubject := "最终文本"
	if replyPlanRequiresStructuredOutput(replyPlan, false) {
		textOutputSubject = "replyParts 的各个 content"
	}
	parts = append(parts,
		"变量任务由 Commit 阶段单独处理，本阶段完全不要写变量任务、变量名称、发送状态或后续动作。",
		textOutputSubject+"只回答上面列出的酒店信息任务，不能包含“发你/发给你/给你发/已经发/后续发/点开就能/我这边发/我这边按入口发”。",
	)
	result := strings.Join(parts, "\n")
	if replyPlanRequiresStructuredOutput(replyPlan, false) {
		result = normalizeStructuredReplyPromptText(result)
	}
	return result
}

func runtimeTaskDisplayLabel(value string) string {
	switch strings.TrimSpace(value) {
	case "parking":
		return "停车"
	case "breakfast":
		return "早餐"
	case "invoice":
		return "发票"
	case "network_wifi", "wifi", "network":
		return "WiFi/网络"
	case "tvcast", "tv_cast", "tv_screen_mirror":
		return "电视投屏"
	case "checkin_process", "check_in", "checkin":
		return "入住流程"
	case "supplies_self_help", "supplies":
		return "用品"
	case "location":
		return "定位"
	case "mini_program":
		return "入住小程序"
	case "phone":
		return "电话"
	default:
		return strings.TrimSpace(value)
	}
}

func buildInitialActionLedger(intent callbacks.IntentTraceData) callbacks.ActionLedgerTraceData {
	ledger := callbacks.ActionLedgerTraceData{}
	add := func(action string, reason string) {
		action = strings.TrimSpace(action)
		if action == "" {
			return
		}
		reason = strings.TrimSpace(reason)
		resourceType := hotelVariableResourceTypeFromAction(action)
		for _, existing := range ledger.RequestedActions {
			if existing.Action == action && existing.ResourceType == resourceType && existing.Reason == reason {
				return
			}
		}
		ledger.RequestedActions = append(ledger.RequestedActions, callbacks.ActionLedgerItem{
			Action:       action,
			ResourceType: resourceType,
			Status:       "requested",
			Reason:       reason,
		})
	}
	for _, task := range buildReplyTaskPlans(intent) {
		taskReason := task.Text
		if taskReason == "" {
			taskReason = task.SubIntent
		}
		if task.Output == "structured_resource_commit" || task.Intent == "hotel_variable" || strings.TrimSpace(task.ResourceAction) != "" {
			add(task.ResourceAction, taskReason)
		}
		if task.Output == "knowledge_text_reply" || task.Intent == "hotel_info" {
			ledger.RequestedActions = appendIfMissingActionLedgerItem(ledger.RequestedActions, callbacks.ActionLedgerItem{
				Action: "knowledge_lookup",
				Status: "requested",
				Reason: taskReason,
			})
		}
		if task.Output == "human_route_confirmation_or_dispatch" || task.Intent == "human_complaint_risk" {
			ledger.RequestedActions = appendIfMissingActionLedgerItem(ledger.RequestedActions, callbacks.ActionLedgerItem{
				Action: "human_route",
				Status: "requested",
				Reason: taskReason,
			})
		}
	}
	for _, action := range intent.ResourceActions {
		add(action, "")
	}
	add(intent.ResourceAction, "")
	if intent.NeedsKnowledge && !actionLedgerContainsAction(ledger.RequestedActions, "knowledge_lookup") {
		ledger.RequestedActions = appendIfMissingActionLedgerItem(ledger.RequestedActions, callbacks.ActionLedgerItem{
			Action: "knowledge_lookup",
			Status: "requested",
		})
	}
	if intent.NeedsHumanRoute && !actionLedgerContainsAction(ledger.RequestedActions, "human_route") {
		ledger.RequestedActions = appendIfMissingActionLedgerItem(ledger.RequestedActions, callbacks.ActionLedgerItem{
			Action: "human_route",
			Status: "requested",
		})
	}
	return ledger
}

func appendIfMissingActionLedgerItem(items []callbacks.ActionLedgerItem, item callbacks.ActionLedgerItem) []callbacks.ActionLedgerItem {
	item.Action = strings.TrimSpace(item.Action)
	item.ResourceType = strings.TrimSpace(item.ResourceType)
	item.Reason = strings.TrimSpace(item.Reason)
	if item.Action == "" {
		return items
	}
	for _, existing := range items {
		if existing.Action == item.Action && existing.ResourceType == item.ResourceType && existing.Reason == item.Reason {
			return items
		}
	}
	return append(items, item)
}

func actionLedgerContainsAction(items []callbacks.ActionLedgerItem, action string) bool {
	action = strings.TrimSpace(action)
	if action == "" {
		return false
	}
	for _, item := range items {
		if item.Action == action {
			return true
		}
	}
	return false
}

func buildCurrentTurnBoundaryInstruction(req RunInput, history adapter.HistoryBuildResult, intent callbacks.IntentTraceData) string {
	return buildCurrentTurnBoundaryInstructionForReplyPlan(req, history, intent, callbacks.ReplyPlanTraceData{})
}

func buildCurrentTurnBoundaryInstructionForReplyPlan(req RunInput, history adapter.HistoryBuildResult, intent callbacks.IntentTraceData, replyPlan callbacks.ReplyPlanTraceData) string {
	currentText := currentRuntimeIntentSemanticText(req)
	if currentText == "" {
		return ""
	}
	boundaryText := currentText
	activeTaskTexts := make([]string, 0, len(replyPlan.TaskPlans))
	for _, task := range activeGenerationTaskPlans(intent, replyPlan) {
		if text := activeGenerationTaskText(task); text != "" {
			activeTaskTexts = append(activeTaskTexts, text)
		}
	}
	if len(replyPlan.TaskPlans) > 0 && len(activeTaskTexts) > 0 {
		boundaryText = strings.Join(activeTaskTexts, "\n")
	} else if intent.NeedsKnowledge && (intent.NeedsResource || len(intent.ResourceActions) > 0) {
		if generationText := strings.TrimSpace(buildGenerationUserMessageText(req.UserMessage.Content, intent)); generationText != "" {
			boundaryText = generationText
		}
	}
	parts := []string{
		"当前轮回复边界：最终回复只回答最后这条当前用户消息。",
		"历史消息已用[历史消息][说话人][时间]标注；必须分清客户、AI客服、人工客服分别说了什么，不能把 AI 或人工客服说过的话当成客户新诉求。",
		"最近原始消息、媒体理解和长期记忆只用于解释指代、补足背景或避免重复询问；如果当前消息是新主题，禁止补答上一轮早餐、停车、投诉、安全、转人工等旧主题。",
		"知识库或变量结果只在命中当前问题时使用；检索结果里出现了当前问题没问的早餐、停车、电视、发票、定位、小程序等内容时，禁止拼进回复。",
		"最终回复只输出给客人的话，不得输出思考过程、规则复述、判断依据或“我先看看/从历史来看/按当前规则”这类内部分析，也不得输出“店助补充/若不确定请先问同事”等内部知识治理备注。",
		"动作安全：没有工具、资源提交、接待路由或明确系统执行结果时，不能承诺或暗示任何真实动作、内部核实、通知转告、登记安排、现场查看、后续跟进或已完成状态。明确的酒店业务问题如果知识库没有可用答案，必须进入接待路由；确实只缺一个能推进处理的关键点时只追问一个，不能口头假装后续有人处理。",
	}
	currentLine := "当前客户消息"
	if len(replyPlan.TaskPlans) > 0 {
		currentLine = "当前活跃回答任务"
	}
	if timeLabel := adapter.RuntimeMessageTimeLabel(&req.UserMessage); timeLabel != "" {
		currentLine += "[" + timeLabel + "]"
	}
	parts = append(parts, currentLine+"："+preview(boundaryText, 240))
	if intent.PrimaryIntent == "hotel_info" || intent.PrimaryIntent == "service_request" {
		parts = append(parts, "酒店信息/服务请求：只围绕当前问题使用知识库结果，不要把同一会话里的其他酒店问题一起回答。知识库已经给出答案时必须直接回答，不能说正在查、稍后查、内部确认或后续处理。如果明确的酒店业务问题没有可用答案，进入接待路由，不要对客户说资料没写明或没查到。")
	}
	if len(activeTaskTexts) > 1 || (len(replyPlan.TaskPlans) == 0 && len(intent.IntentTasks) > 1) {
		parts = append(parts, "当前轮包含连续多问：必须按客户消息顺序逐项覆盖当前轮每个问题；不要只回答主意图或最后一个问题。已检索到的知识必须直接答；没有可用答案的酒店业务项进入接待路由，不能说“资料没写明”“帮你查/我查一下”。")
	}
	if intent.PrimaryIntent == "service_request" {
		parts = append(parts, "服务请求回复结构：先看知识库有没有自助路径或处理边界；没有明确答案时追问一个必要字段或进入人工意图/接待路由。没有路由或工具结果时，禁止表达动作已执行、内部确认或后续有人处理。")
	}
	if looksLikeReturningCustomerTurn(currentText) {
		parts = append(parts, "当前消息像是跨天/隔一段时间后重新咨询；旧消息或长期记忆里的房号、入住事实都已过期。缺少当前房号时只能重新询问，回复中不能沿用旧房号。回复必须点名当前问题本身，例如当前问电视就要提到电视，不能只欢迎回来或只问是否到房间。")
	}
	if shouldAttachRecentMediaUnderstandingToCurrentTurn(req, history, intent) {
		parts = append(parts, "当前消息是在追问最近一条客户媒体/文件/语音理解结果；必须结合该媒体理解回答。如果媒体/语音理解里已经有本轮房号，回复里保留这个房号，不要只泛称“房间号记下了”。")
	}
	if intent.PrimaryIntent == "hotel_variable" || intent.NeedsResource || len(intent.ResourceActions) > 0 {
		parts = append(parts, "酒店变量：只围绕当前请求的电话、定位、小程序等变量回复；未配置的变量要明确说未配置。定位/小程序结构化消息由 Commit 阶段发送，文本回复不要说发你、发给你、已经发、点开就能、让同事发、让同事联系、稍后处理或换成其他动作承诺。")
		if intent.NeedsKnowledge {
			parts = append(parts, "混合变量+知识：最终文本只回答停车、早餐、发票、流程等知识问题；定位、小程序等变量会由系统按 resourceActions 单独发送结构化消息。")
		}
	}
	if intent.PrimaryIntent == "interaction" && strings.TrimSpace(intent.SubIntent) != "media_context_follow_up" {
		parts = append(parts, "互动/感谢/确认：当前消息只是感谢、好的、确认、结束语或表情时，只回应这条当前消息；不要继续承诺上一条送物、维修、转人工或其他真实动作，也不要补答旧业务问题。单独表情且无明确业务上下文时，像真人值守一样轻接住即可，可自然说“我在，有事发我”。")
	}
	if intent.PrimaryIntent == "interaction" && isSocialCorrectionSubIntent(intent.SubIntent) {
		parts = append(parts, "纠错/误会：完整上下文仍然保留，但本轮只允许用当前纠正和紧邻的上一条客服消息识别误会；自然承认看错、听错或理解错，只输出一句完整短句。不要辩解，不要补答或追问任何旧业务主题。")
	}
	result := strings.Join(parts, "\n")
	if replyPlanRequiresStructuredOutput(replyPlan, false) {
		result = normalizeStructuredReplyPromptText(result)
	}
	return result
}

func isSocialCorrectionSubIntent(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "correction", "clarification", "misunderstanding", "deny_voice", "voice_correction":
		return true
	default:
		return false
	}
}

func isMultiQuestionCurrentTurn(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	return utils.IsRuntimeCustomerBurstEnvelope(trimmed) && len(utils.RuntimeCustomerBurstItems(trimmed)) > 1
}

func shouldAttachRecentMediaUnderstandingToCurrentTurn(req RunInput, history adapter.HistoryBuildResult, intent callbacks.IntentTraceData) bool {
	if isRuntimeMediaMessage(req.UserMessage.MessageType) || strings.TrimSpace(intent.SubIntent) != "media_context_follow_up" {
		return false
	}
	return recentUsableMediaTextFromHistory(history) != ""
}

func looksLikeReturningCustomerTurn(text string) bool {
	compact := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "").Replace(strings.ToLower(strings.TrimSpace(text)))
	if compact == "" {
		return false
	}
	return replyengine.ContainsAny(compact, "三天后", "两天后", "几天后", "隔天", "过几天", "又来了", "我又来了", "回来继续", "再次咨询")
}

func buildWeatherToolInstruction(intent callbacks.IntentTraceData) string {
	if strings.TrimSpace(intent.SubIntent) != "weather_query" && strings.TrimSpace(intent.ResourceAction) != "get_weather" && !runtimeIntentHasWeatherTask(intent) {
		return ""
	}
	return "当前阶段已识别为天气查询。你必须调用 get_weather 工具获取真实天气后再回复；不要直接说你查不到、以手机天气为准。若当前消息没有明确城市或地点，先简短追问城市；若有城市或地点，直接把它作为 location 调用工具。"
}

func runtimeIntentHasWeatherTask(intent callbacks.IntentTraceData) bool {
	for _, task := range intent.IntentTasks {
		if canonicalIntentCode(task.Intent) == "interaction" && strings.TrimSpace(task.SubIntent) == "weather_query" {
			return true
		}
	}
	return false
}

func buildRecentMediaContextInstruction(req RunInput, history adapter.HistoryBuildResult, intent callbacks.IntentTraceData) string {
	if strings.TrimSpace(intent.SubIntent) != "media_context_follow_up" {
		return ""
	}
	if isRuntimeMediaMessage(req.UserMessage.MessageType) {
		return ""
	}
	if !replyengine.LooksLikeMediaFollowUp(req.UserMessage.Content) {
		return ""
	}
	mediaText := recentUsableMediaTextFromHistory(history)
	if mediaText == "" {
		if recent := findRecentUsableMediaUnderstanding(req); recent != nil {
			mediaText = preferredGenerateMediaUnderstandingText(recent.MediaText, recent.MediaSummary)
		}
	}
	if mediaText == "" {
		return ""
	}
	return "本轮图片/文件上下文：当前用户问题是在追问最近一条已解析的图片或文件文本。请直接结合下面内容回答当前问法，不要把图片/文件当成无关历史，不要机械复述整段内容，不要说系统识别。语音仍按既有语转文文本链路处理。\n" + preview(mediaText, 1200)
}

func recentUsableMediaTextFromHistory(history adapter.HistoryBuildResult) string {
	for i := len(history.RawItems) - 1; i >= 0; i-- {
		item := history.RawItems[i]
		if item.SenderType != "" && item.SenderType != enums.IMSenderTypeCustomer {
			continue
		}
		if !isRuntimeMediaMessage(item.MessageType) {
			continue
		}
		mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(item.Payload)
		if strings.TrimSpace(status) != "understood" {
			continue
		}
		text := preferredGenerateMediaUnderstandingText(mediaText, mediaSummary)
		if text != "" {
			return text
		}
	}
	return ""
}

func preferredGenerateMediaUnderstandingText(mediaText string, mediaSummary string) string {
	if text := strings.TrimSpace(mediaText); text != "" {
		return text
	}
	return strings.TrimSpace(mediaSummary)
}

type retrievedContextOutcome struct {
	Decision                    knowledgeGuardDecision
	AnswerabilityStatus         string
	RawKnowledgeContextMessages []*schema.Message
}

func appendRetrievedContext(ctx context.Context, req RunInput, intent callbacks.IntentTraceData, summary *RunResult, collector *callbacks.RuntimeTraceCollector, gate *KnowledgeAnswerabilityGate, messages *[]*schema.Message) retrievedContextOutcome {
	if messages == nil {
		return retrievedContextOutcome{AnswerabilityStatus: answerabilityStatusUnanswerable}
	}
	if gate == nil {
		gate = NewKnowledgeAnswerabilityGate()
	}
	state, err := gate.Evaluate(ctx, answerabilityGateInput{
		Request:   req,
		Summary:   summary,
		Collector: collector,
		Messages:  append([]*schema.Message(nil), (*messages)...),
		Intent:    intent,
	})
	if err != nil || state == nil {
		errorMessage := ""
		if err != nil {
			errorMessage = err.Error()
		} else {
			errorMessage = "answerability gate returned nil state"
		}
		if collector != nil {
			collector.SetAnswerability(callbacks.AnswerabilityTraceData{
				Status:       answerabilityStatusUnanswerable,
				Reason:       "answerability gate failed",
				ErrorMessage: errorMessage,
			})
		}
		return retrievedContextOutcome{
			Decision:            buildKnowledgeUnavailableDecision(req.AIAgent, utils.SplitInt64s(req.AIAgent.KnowledgeIDs)),
			AnswerabilityStatus: answerabilityStatusUnanswerable,
		}
	}
	rawKnowledgeContextMessages := findRawKnowledgeContextMessages(state)
	*messages = append((*messages)[:0], state.Input.Messages...)
	if state.SkipGate {
		return retrievedContextOutcome{
			AnswerabilityStatus:         state.AnswerabilityStatus,
			RawKnowledgeContextMessages: rawKnowledgeContextMessages,
		}
	}
	return retrievedContextOutcome{
		Decision:                    state.Decision,
		AnswerabilityStatus:         state.AnswerabilityStatus,
		RawKnowledgeContextMessages: rawKnowledgeContextMessages,
	}
}

func findRawKnowledgeContextMessages(state *answerabilityGateState) []*schema.Message {
	if state == nil || state.RetrieveResult == nil {
		return nil
	}
	contextText := strings.TrimSpace(state.RetrieveResult.ContextText)
	if contextText == "" {
		return nil
	}
	ret := make([]*schema.Message, 0, 1)
	for _, message := range state.Input.Messages {
		if message != nil && strings.TrimSpace(message.Content) == contextText {
			ret = append(ret, message)
		}
	}
	return ret
}
