package executor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"agent-desk/internal/ai/replyengine"
	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"

	"github.com/cloudwego/eino/schema"
)

func buildRunMessages(ctx context.Context, req RunInput, summary *RunResult, collector *callbacks.RuntimeTraceCollector, gate *KnowledgeAnswerabilityGate) []*schema.Message {
	messages, _ := buildRunMessagesStrict(ctx, req, summary, collector, gate)
	return messages
}

func buildRunMessagesStrict(ctx context.Context, req RunInput, summary *RunResult, collector *callbacks.RuntimeTraceCollector, gate *KnowledgeAnswerabilityGate) ([]*schema.Message, error) {
	modes := resolveRuntimeFeatureModes(req)
	if err := validateRuntimeFeatureModes(modes); err != nil {
		return nil, err
	}
	history := adapter.BuildHistoryMessages(req.Conversation.ID, req.UserMessage.ID, req.Conversation.TenantID, 0)
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
		collector.Data.Input.CurrentUserMessagePreview = preview(runtimeUserMessageText(req.UserMessage), 120)
		collector.Data.Pipeline.ContextBuild.Mode = modes.ContextCompiler
	}
	plan, err := buildRuntimePipelinePlanStrict(ctx, req, history, nil)
	if err != nil {
		return nil, err
	}
	if summary != nil && plan.TaskState.Enabled {
		summary.TaskLedgerEnabled = true
		summary.TaskKeys = append([]string(nil), plan.TaskState.SelectedTaskKeys...)
		summary.FailedTaskKeys = append([]string(nil), plan.TaskState.FailedTaskKeys...)
		summary.HumanTaskKeys = append([]string(nil), plan.TaskState.HumanTaskKeys...)
		summary.HasRemainingTasks = plan.TaskState.HasMore
		summary.NeedsHumanDispatch = len(plan.TaskState.HumanTaskKeys) > 0
		summary.CoveredByTaskID = plan.TaskState.CoveredByTaskID
	}
	if collector != nil {
		collector.SetPipeline(plan.Normalize, plan.Intent, plan.PromptSelect, plan.Context, plan.ToolKnowledge, plan.ReplyPlan, plan.Generate, plan.Validate)
		collector.SetActionLedger(buildInitialActionLedger(plan.Intent))
	}
	if plan.PrefetchedKnowledge != nil {
		if summary != nil {
			summary.RetrieverCount = len(plan.PrefetchedKnowledge.Hits)
		}
		if collector != nil {
			collector.SetRetrieverSummary(plan.PrefetchedKnowledge.TraceSummary)
			collector.AddRetrieverItems(plan.PrefetchedKnowledge.TraceItems)
			collector.SetKnowledgeResources(resolveRuntimeKnowledgeResources(req, plan.PrefetchedKnowledge))
		}
	}
	if skip, taskErr := validateRuntimeTaskPlan(plan); taskErr != nil {
		return nil, taskErr
	} else if skip {
		if summary != nil {
			summary.SkipReply = true
			summary.SkipGeneration = true
		}
		return nil, nil
	}
	if !plan.Intent.ShouldReply {
		if summary != nil {
			summary.ReplyText = ""
			summary.SkipReply = true
			summary.SkipGeneration = true
		}
		return nil, nil
	}
	preparedActions := []contracts.PreparedActionV1(nil)
	if modes.ActionLedger == runtimeActionLedgerAuthoritative {
		actionLedger := plan.ActionLedgerV2
		preparedActions, actionLedger, err = prepareRuntimeActions(req, plan.TaskState, plan.ReplyPlanV2, plan.Evidence, actionLedger)
		if err != nil {
			return nil, err
		}
		plan.ActionLedgerV2 = actionLedger
	}
	if summary != nil {
		summary.ReplyPlanV2 = &plan.ReplyPlanV2
		summary.EvidenceBundle = &plan.Evidence
		summary.ActionLedgerV2 = &plan.ActionLedgerV2
		summary.PreparedActions = preparedActions
		summary.UseRuntimeV2Generate = modes.ReplyContract == runtimeReplyContractV2
		summary.UseRuntimeV2DirectGenerate = summary.UseRuntimeV2Generate && !plan.Intent.NeedsTool
		summary.RuntimeValidatorMode = modes.Validator
		summary.ActionLedgerAuthoritative = modes.ActionLedger == runtimeActionLedgerAuthoritative
	}
	if modes.ContextCompiler == runtimeContextCompilerLegacy {
		return buildLegacyGenerateMessagesStrict(ctx, req, history, plan, summary, collector, gate)
	}
	if modes.ContextCompiler == runtimeContextCompilerShadow {
		legacyMessages, legacyErr := buildLegacyGenerateMessagesStrict(ctx, req, history, plan, summary, collector, gate)
		if legacyErr != nil {
			return nil, legacyErr
		}
		if plan.ReplyPlanV2.ShouldGenerate {
			if _, shadowErr := compileRuntimeGenerateMessages(ctx, req, history, plan, nil, collector, modes, false); shadowErr != nil {
				slog.Warn("AI runtime context compiler shadow failed", "conversation_id", req.Conversation.ID, "error", shadowErr)
				if collector != nil {
					collector.Data.Pipeline.ContextBuild.ShadowStatus = "failed"
					collector.Data.Pipeline.ContextBuild.ShadowError = "compile_failed"
				}
			}
		}
		return legacyMessages, nil
	}
	if !plan.ReplyPlanV2.ShouldGenerate {
		if summary != nil {
			summary.SkipGeneration = true
		}
		return nil, nil
	}
	messages, err := compileRuntimeGenerateMessages(ctx, req, history, plan, summary, collector, modes, true)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func buildLegacyGenerateMessagesStrict(
	ctx context.Context,
	req RunInput,
	history adapter.HistoryBuildResult,
	plan runtimePipelinePlan,
	summary *RunResult,
	collector *callbacks.RuntimeTraceCollector,
	gate *KnowledgeAnswerabilityGate,
) ([]*schema.Message, error) {
	messages := make([]*schema.Message, 0, len(history.Messages)+12)
	if history.MemoryMessage != nil {
		messages = append(messages, history.MemoryMessage)
	}
	messages = append(messages, history.Messages...)
	for _, instruction := range []string{
		buildRecentMediaContextInstruction(req, history, plan.Intent),
		buildCurrentTurnBoundaryInstruction(req, history, plan.Intent),
		buildRecentAnsweredTurnInstruction(req, history),
		generationGuardInstruction(ctx),
		plan.Prompt,
		buildMultiReplyOutputInstruction(plan.ReplyPlan),
		buildNoHitTaskInstruction(plan.ReplyPlan, plan.NoHitTaskKeys),
		buildAutoHandoffDisabledInstruction(req, plan.Intent),
	} {
		if strings.TrimSpace(instruction) != "" {
			messages = append(messages, schema.SystemMessage(instruction))
		}
	}
	retrievedContext, err := appendRetrievedContextStrict(ctx, req, plan.Intent, plan.PrefetchedKnowledge, summary, collector, gate, &messages)
	if err != nil {
		return nil, err
	}
	appendReplyTagContext(req, plan.Intent, plan.ReplyPlan, retrievedContext.AnswerabilityStatus, collector, &messages)
	if instruction := buildGenerationScopeInstruction(plan.Intent); strings.TrimSpace(instruction) != "" {
		messages = append(messages, schema.SystemMessage(instruction))
	}
	messages = append(messages, schema.UserMessage(buildGenerationUserMessageText(runtimeUserMessageText(req.UserMessage), plan.Intent)))
	return messages, nil
}

func compileRuntimeGenerateMessages(
	ctx context.Context,
	req RunInput,
	history adapter.HistoryBuildResult,
	plan runtimePipelinePlan,
	summary *RunResult,
	collector *callbacks.RuntimeTraceCollector,
	modes runtimeFeatureModes,
	activate bool,
) ([]*schema.Message, error) {
	resolved, err := services.ModelCallResolverService.ResolveForConversation(req.Conversation.ID, enums.ModelUsageSlotReplyLLM)
	if err != nil {
		return nil, err
	}
	instance, err := resolveRuntimeCompilerInstance(req, resolved)
	if err != nil {
		return nil, err
	}
	dialogueState, err := services.ConversationDialogueStateService.Load(req.Conversation.TenantID, req.Conversation.ID, runtimeSessionNo(req))
	if err != nil {
		return nil, err
	}
	currentMessages := []models.Message{req.UserMessage}
	if _, sourceMessages, ok := runtimeTaskScope(req); ok && len(sourceMessages) > 0 {
		currentMessages = sourceMessages
	}
	answerabilityStatus := plan.Evidence.RetrievalStatus
	if answerabilityStatus == "has_context" {
		answerabilityStatus = answerabilityStatusHasContext
	}
	tagMessages := make([]*schema.Message, 0, 1)
	appendReplyTagContext(req, plan.Intent, plan.ReplyPlan, answerabilityStatus, collector, &tagMessages)
	tagText := ""
	if len(tagMessages) > 0 && tagMessages[len(tagMessages)-1] != nil {
		tagText = strings.TrimSpace(tagMessages[len(tagMessages)-1].Content)
	}
	preparedActions := make([]contracts.ActionLedgerItemV1, 0)
	for _, action := range plan.ActionLedgerV2.Actions {
		if action.Status == "prepared" {
			preparedActions = append(preparedActions, action)
		}
	}
	recentHistory := runtimeGenerateRelevantHistory(history.RawItems, plan.ReplyPlan.TaskPlans)
	compileInput := contextcompiler.CompileInput{
		Stage: contextcompiler.CompileStageGenerate,
		Scope: runtimeCompilerScope(req, resolved, instance), Model: *resolved, Instance: *instance, Agent: req.AIAgent,
		CurrentMessages: currentMessages, RecentHistory: recentHistory, Memory: history.Memory, DialogueState: dialogueState,
		ReplyPlan: &plan.ReplyPlanV2, Evidence: &plan.Evidence, PreparedActions: preparedActions,
		ReplyTagText: tagText, IntentProfileRevision: runtimeIntentProfileRevision(req),
		GenerationInstruction:            buildCompiledGenerationInstruction(ctx, req, history, plan, modes),
		ReplyContract:                    contextcompiler.ReplyContract(modes.ReplyContract),
		ExpectedEvidenceScopeFingerprint: plan.Evidence.ScopeFingerprint,
	}
	compiled, err := contextcompiler.New(nil).Compile(ctx, compileInput)
	if err != nil {
		return nil, err
	}
	if activate && summary != nil {
		summary.CompiledContext = &compiled
		summary.GenerateCompileInput = &compileInput
	}
	if collector != nil {
		if activate {
			collector.Data.Pipeline.ContextBuild.ContextLimit = compiled.ContextLimit
			collector.Data.Pipeline.ContextBuild.ReservedOutput = compiled.ReservedOutput
			collector.Data.Pipeline.ContextBuild.SafetyMargin = compiled.SafetyMargin
			collector.Data.Pipeline.ContextBuild.AvailableInput = compiled.AvailableInput
			collector.Data.Pipeline.ContextBuild.EstimatedInput = compiled.EstimatedInput
			collector.Data.Pipeline.ContextBuild.Estimator = compiled.Estimator
			collector.Data.Pipeline.ContextBuild.Fingerprint = compiled.Fingerprint
		} else {
			collector.Data.Pipeline.ContextBuild.ShadowStatus = "compiled"
			collector.Data.Pipeline.ContextBuild.ShadowEstimatedInput = compiled.EstimatedInput
			collector.Data.Pipeline.ContextBuild.ShadowFingerprint = compiled.Fingerprint
			collector.Data.Pipeline.ContextBuild.ShadowPrunedCount = len(compiled.PrunedItems)
		}
	}
	return compiled.Messages, nil
}

// runtimeGenerateRelevantHistory 把“理解上下文”和“生成事实”分开。IntentDetect
// 继续读取近期历史来解析指代；Generate 只有在当前 Task 明确是追问、纠正、重复、
// 媒体追问或包含紧邻指代时才携带历史。独立新问题和闲聊不再被旧酒店答案污染。
func runtimeGenerateRelevantHistory(history []models.Message, plans []callbacks.ReplyTaskPlanTraceData) []models.Message {
	if len(history) == 0 || len(plans) == 0 {
		return nil
	}
	for _, plan := range plans {
		switch strings.TrimSpace(plan.RelationType) {
		case "follow_up", "correction", "repeat", "continuation":
			return history
		}
		switch strings.TrimSpace(plan.SubIntent) {
		case "media_context_follow_up", "correction", "clarify_previous":
			return history
		}
		compact := compactRuntimeProtocolText(plan.Text)
		if containsAny(compact, []string{"这个", "那个", "刚才", "上面", "前面", "继续", "还是", "它", "那里", "那边", "这里说的"}) {
			return history
		}
	}
	return nil
}

func buildCompiledGenerationInstruction(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, plan runtimePipelinePlan, modes runtimeFeatureModes) string {
	parts := []string{
		generationGuardInstruction(ctx),
		plan.Prompt,
		buildAutoHandoffDisabledInstruction(req, plan.Intent),
		buildDisabledActionInstruction(),
	}
	if modes.ReplyContract == runtimeReplyContractLegacy {
		parts = append(parts,
			buildRecentMediaContextInstruction(req, history, plan.Intent),
			buildMultiReplyOutputInstruction(plan.ReplyPlan),
			buildNoHitTaskInstruction(plan.ReplyPlan, plan.NoHitTaskKeys),
		)
	}
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			ret = append(ret, part)
		}
	}
	return strings.Join(ret, "\n\n")
}

func runtimeIntentProfileRevision(req RunInput) int64 {
	profile := resolveRuntimeIntentProfile(resolveRuntimeIntentScope(req))
	if profile == nil {
		return 0
	}
	return profile.Revision
}

func validateRuntimeTaskPlan(plan runtimePipelinePlan) (bool, error) {
	if !plan.TaskState.Enabled || len(plan.ReplyPlan.TaskPlans) > 0 {
		return false, nil
	}
	if len(plan.TaskState.FailedTaskKeys) > 0 || plan.TaskState.HasMore {
		return false, services.NewAIReplyExecutionError(
			services.AIReplyExecutionErrorKnowledgeUnavailable,
			fmt.Errorf("AI reply turn has no runnable task and still requires handoff or continuation"),
		)
	}
	return true, nil
}

func buildNoHitTaskInstruction(plan callbacks.ReplyPlanTraceData, taskKeys []string) string {
	if len(taskKeys) == 0 {
		return ""
	}
	noHit := make(map[string]struct{}, len(taskKeys))
	for _, taskKey := range taskKeys {
		if taskKey = strings.TrimSpace(taskKey); taskKey != "" {
			noHit[taskKey] = struct{}{}
		}
	}
	items := make([]string, 0, len(noHit))
	for _, task := range plan.TaskPlans {
		if _, ok := noHit[strings.TrimSpace(task.TaskKey)]; !ok {
			continue
		}
		label := strings.TrimSpace(currentTurnDisplayText(task.Text))
		if label == "" {
			label = runtimeTaskDisplayLabel(task.SubIntent)
		}
		items = append(items, fmt.Sprintf("%s（%s）", task.TaskKey, label))
	}
	if len(items) == 0 {
		return ""
	}
	return "【逐题知识边界】以下任务已完成当前门店知识检索，但没有命中可用资料：" + strings.Join(items, "；") + "。只能说明当前资料未写明；需要补充条件时，每题最多追问一个关键点。禁止凭常识、历史酒店答案或模型猜测补写事实。"
}

func buildAutoHandoffDisabledInstruction(req RunInput, intent callbacks.IntentTraceData) string {
	if !intent.NeedsHumanRoute || !isHandoffIntentCategory(intent) {
		return ""
	}
	if services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabledForConversation(req.Conversation.ID) {
		return ""
	}
	return "【当前会话接待边界】当前客户在此企微员工号下不允许自动转人工。不要提及任何内部设置，不要发起转人工确认、通知、派发或承诺同事处理。继续直接回答当前问题；若涉及紧急安全情况，优先给出可立即执行的自我保护和 120/110 建议。"
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

func buildGenerationScopeInstruction(intent callbacks.IntentTraceData) string {
	if !intent.NeedsKnowledge || (!intent.NeedsResource && len(intent.ResourceActions) == 0) {
		return ""
	}
	taskPlans := buildReplyTaskPlans(intent)
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
	parts = append(parts,
		"变量任务由 Commit 阶段单独处理，本阶段完全不要写变量任务、变量名称、发送状态或后续动作。",
		"最终文本只回答上面列出的酒店信息任务，不能包含“发你/发给你/给你发/已经发/后续发/点开就能/我这边发/我这边按入口发”。",
	)
	return strings.Join(parts, "\n")
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
	currentText := runtimeUserMessageText(req.UserMessage)
	if currentText == "" {
		return ""
	}
	boundaryText := currentText
	if intent.NeedsKnowledge && (intent.NeedsResource || len(intent.ResourceActions) > 0) {
		if generationText := strings.TrimSpace(buildGenerationUserMessageText(currentText, intent)); generationText != "" {
			boundaryText = generationText
		}
	}
	parts := []string{
		"当前轮回复边界：最终回复只回答最后这条当前用户消息。",
		"历史消息已用[历史消息][说话人][时间]标注；必须分清客户、AI客服、人工客服分别说了什么，不能把 AI 或人工客服说过的话当成客户新诉求。",
		"最近原始消息、媒体理解和长期记忆只用于解释指代、补足背景或避免重复询问；如果当前消息是新主题，禁止补答上一轮早餐、停车、投诉、安全、转人工等旧主题。",
		"知识库或变量结果只在命中当前问题时使用；检索结果里出现了当前问题没问的早餐、停车、电视、发票、定位、小程序等内容时，禁止拼进回复。",
		"最终回复只输出给客人的话，不得输出思考过程、规则复述、判断依据或“我先看看/从历史来看/按当前规则”这类内部分析，也不得输出“店助补充/若不确定请先问同事”等内部知识治理备注。",
		"动作安全：没有工具、资源提交、接待路由或明确系统执行结果时，不能承诺或暗示任何真实动作、内部核实、通知转告、登记安排、现场查看、后续跟进或已完成状态。知识库没写明时，只能说当前资料没写明并追问一个关键点；真要人工时必须进入人工意图/接待路由，不能口头假装后续有人处理。",
	}
	currentLine := "当前客户消息"
	if timeLabel := adapter.RuntimeMessageTimeLabel(&req.UserMessage); timeLabel != "" {
		currentLine += "[" + timeLabel + "]"
	}
	parts = append(parts, currentLine+"："+preview(boundaryText, 240))
	if intent.PrimaryIntent == "hotel_info" || intent.PrimaryIntent == "service_request" {
		parts = append(parts, "酒店信息/服务请求：只围绕当前问题使用知识库结果，不要把同一会话里的其他酒店问题一起回答。知识库已经给出答案时必须直接回答，不能说正在查、稍后查、内部确认或后续处理。如果当前问题里某一项知识库没有明确写明，只能说“当前资料没写明”并追问一个关键点。")
	}
	if len(intent.IntentTasks) > 1 {
		parts = append(parts, "当前轮包含连续多问：必须按客户消息顺序逐项覆盖当前轮每个问题；不要只回答主意图或最后一个问题。已检索到的知识必须直接答，缺资料时逐项说明“当前资料没写明”，不能说“帮你查/我查一下”。")
	}
	if intent.PrimaryIntent == "service_request" {
		parts = append(parts, "服务请求回复结构：先看知识库有没有自助路径或处理边界；没有明确答案时只说明当前资料未写明，并追问一个必要字段，不得自行转人工。没有路由或工具结果时，禁止表达动作已执行、内部确认或后续有人处理。")
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
	return strings.Join(parts, "\n")
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
	return trimmed != "" && (strings.Count(trimmed, "\n") >= 1 || strings.Contains(trimmed, "[消息"))
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

func buildRecentMediaContextInstruction(req RunInput, history adapter.HistoryBuildResult, intent callbacks.IntentTraceData) string {
	if strings.TrimSpace(intent.SubIntent) != "media_context_follow_up" {
		return ""
	}
	if isRuntimeMediaMessage(req.UserMessage.MessageType) {
		return ""
	}
	if !replyengine.LooksLikeMediaFollowUp(runtimeUserMessageText(req.UserMessage)) {
		return ""
	}
	mediaText := recentUsableMediaTextFromHistory(history)
	if mediaText == "" {
		if recent := findRecentUsableMediaUnderstanding(req); recent != nil {
			mediaText = strings.TrimSpace(strings.Join([]string{recent.MediaText, recent.MediaSummary}, "\n"))
		}
	}
	if mediaText == "" {
		return ""
	}
	return "本轮图片/文件上下文：当前用户问题是在追问最近一条已解析的图片或文件文本。请直接结合下面内容回答当前问法，不要把图片/文件当成无关历史，不要机械复述整段内容，不要说系统识别。语音仍按既有语转文文本链路处理。\n" +
		preview(mediaText, 1200)
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
		text := strings.TrimSpace(strings.Join([]string{mediaText, mediaSummary}, "\n"))
		if text != "" {
			return text
		}
	}
	return ""
}

type retrievedContextOutcome struct {
	Decision            knowledgeGuardDecision
	AnswerabilityStatus string
}

func appendRetrievedContextStrict(ctx context.Context, req RunInput, intent callbacks.IntentTraceData, prefetched *retrievers.KnowledgeRetrieveResult, summary *RunResult, collector *callbacks.RuntimeTraceCollector, gate *KnowledgeAnswerabilityGate, messages *[]*schema.Message) (retrievedContextOutcome, error) {
	if messages == nil {
		return retrievedContextOutcome{AnswerabilityStatus: answerabilityStatusUnanswerable},
			services.NewAIReplyExecutionError(services.AIReplyExecutionErrorKnowledgeUnavailable, nil)
	}
	if gate == nil {
		gate = NewKnowledgeAnswerabilityGate()
	}
	state, err := gate.Evaluate(ctx, answerabilityGateInput{
		Request:             req,
		Summary:             summary,
		Collector:           collector,
		Messages:            append([]*schema.Message(nil), (*messages)...),
		Intent:              intent,
		PrefetchedKnowledge: prefetched,
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
		return retrievedContextOutcome{AnswerabilityStatus: answerabilityStatusUnanswerable},
			services.NewAIReplyExecutionError(services.AIReplyExecutionErrorKnowledgeUnavailable, err)
	}
	*messages = append((*messages)[:0], state.Input.Messages...)
	if state.SkipGate {
		return retrievedContextOutcome{AnswerabilityStatus: state.AnswerabilityStatus}, nil
	}
	return retrievedContextOutcome{Decision: state.Decision, AnswerabilityStatus: state.AnswerabilityStatus}, nil
}
