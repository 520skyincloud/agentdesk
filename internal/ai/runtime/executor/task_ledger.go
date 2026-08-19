package executor

import (
	"agent-desk/internal/ai/runtime/contracts"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

type runtimeTaskBatchState struct {
	Enabled            bool
	TurnID             int64
	TurnVersion        int
	HasMore            bool
	FailedTaskKeys     []string
	HumanTaskKeys      []string
	SelectedTaskKeys   []string
	CommittedTaskCount int
	CoveredByTaskID    int64
	// TaskIDByTaskKey 绑定 Task 持久 ID（契约 4.17 审计链）。
	TaskIDByTaskKey map[string]int64
}

func loadPersistedRuntimeTaskBatch(req RunInput) (callbacks.IntentTraceData, callbacks.ReplyPlanTraceData, runtimeTaskBatchState, bool, error) {
	state := runtimeTaskBatchState{}
	turn, sourceMessages, ok := runtimeTaskScope(req)
	if !ok {
		return callbacks.IntentTraceData{}, callbacks.ReplyPlanTraceData{}, state, false, nil
	}
	allTasks := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(sqls.DB(), turn.TenantID, turn.ID)
	if len(allTasks) > 0 {
		state.TaskIDByTaskKey = make(map[string]int64, len(allTasks))
		for index := range allTasks {
			state.TaskIDByTaskKey[allTasks[index].TaskKey] = allTasks[index].ID
		}
	}
	if len(allTasks) == 0 || !runtimeTaskSourcesCovered(sourceMessages, allTasks) {
		return callbacks.IntentTraceData{}, callbacks.ReplyPlanTraceData{}, state, false, nil
	}
	var (
		batch   []models.AIReplyTurnTask
		hasMore bool
	)
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedTurn, lockErr := repositories.AIReplyTurnRepository.GetForUpdateInTenant(ctx.Tx, turn.ID, turn.TenantID)
		if lockErr != nil {
			return lockErr
		}
		if lockedTurn == nil || lockedTurn.ConversationID != turn.ConversationID || lockedTurn.SessionNo != turn.SessionNo {
			return services.ErrAIReplyTurnStale
		}
		batch, hasMore, lockErr = services.AIReplyTurnTaskService.ClaimBatchDB(ctx.Tx, lockedTurn, req.JobID)
		return lockErr
	})
	if err != nil {
		return callbacks.IntentTraceData{}, callbacks.ReplyPlanTraceData{}, state, false, err
	}
	if len(batch) == 0 {
		state = runtimeTaskBatchState{
			Enabled: true, TurnID: turn.ID, TurnVersion: turn.Version,
			HasMore: services.AIReplyTurnTaskService.HasRunnable(turn.TenantID, turn.ID),
		}
		covered := false
		for index := range allTasks {
			task := allTasks[index]
			if task.SourceMessageID == req.UserMessage.ID && aiReplyTurnTaskLedgerTerminal(task.Status) {
				covered = true
				if task.CoveredByTaskID > 0 {
					state.CoveredByTaskID = task.CoveredByTaskID
				} else {
					state.CoveredByTaskID = task.ID
				}
			}
			if task.Status == enums.AIReplyTurnTaskStatusHandoffPending || task.Status == enums.AIReplyTurnTaskStatusFailed {
				state.FailedTaskKeys = appendUniqueStrings(state.FailedTaskKeys, task.TaskKey)
			}
		}
		// 生产回归 2026-08-18：生成失败后的重试进入空批次恢复分支，跳过意图/知识，
		// 把已命中的知识丢掉，模型自由发挥"资料没写明"。当前消息没有任何已提交回复时
		// 必须回到完整管线重跑（任务已终态，Ensure/Claim 幂等，checkpoint 防重复检索）。
		if !covered {
			return callbacks.IntentTraceData{}, callbacks.ReplyPlanTraceData{}, state, false, nil
		}
		return callbacks.IntentTraceData{}, callbacks.ReplyPlanTraceData{}, state, true, nil
	}
	plans := replyTaskPlansFromLedger(batch, sourceMessages)
	intent := intentFromReplyTaskPlans(plans, "restored from AI reply turn task ledger")
	promptPack := selectIntentPromptPack(intent)
	replyPlan := buildReplyPlan(intent, promptPack)
	replyPlan.TaskPlans = plans
	state = runtimeTaskState(turn, batch, hasMore)
	return intent, replyPlan, state, true, nil
}

func persistAndSelectRuntimeTaskBatch(req RunInput, intent callbacks.IntentTraceData, replyPlan callbacks.ReplyPlanTraceData) (callbacks.IntentTraceData, callbacks.ReplyPlanTraceData, runtimeTaskBatchState, error) {
	state := runtimeTaskBatchState{}
	turn, sourceMessages, ok := runtimeTaskScope(req)
	if !ok {
		return intent, assignEphemeralTaskKeys(replyPlan), state, nil
	}
	inputs, plannedByKey, err := buildRuntimeTaskInputs(replyPlan.TaskPlans, req.UserMessage.ID, sourceMessages, turn.TenantID, turn.ID)
	if err != nil {
		return callbacks.IntentTraceData{}, callbacks.ReplyPlanTraceData{}, state, err
	}
	var (
		ensured []models.AIReplyTurnTask
		batch   []models.AIReplyTurnTask
		hasMore bool
	)
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedTurn, lockErr := repositories.AIReplyTurnRepository.GetForUpdateInTenant(ctx.Tx, turn.ID, turn.TenantID)
		if lockErr != nil {
			return lockErr
		}
		if lockedTurn == nil || lockedTurn.Version != turn.Version || lockedTurn.ConversationID != turn.ConversationID ||
			lockedTurn.SessionNo != turn.SessionNo {
			return services.ErrAIReplyTurnStale
		}
		ensured, lockErr = services.AIReplyTurnTaskService.EnsureTasksDB(ctx.Tx, lockedTurn, inputs)
		if lockErr != nil {
			return lockErr
		}
		// 契约 10.7：覆盖标签与 Task 创建同事务持久化。
		if coverErr := services.AIReplyTurnTaskService.RecordResolvedCoverageDB(ctx.Tx, req.ToJobRef(), lockedTurn, ensured, time.Now()); coverErr != nil {
			return coverErr
		}
		batch, hasMore, lockErr = services.AIReplyTurnTaskService.ClaimBatchDB(ctx.Tx, lockedTurn, req.JobID)
		return lockErr
	})
	if err != nil {
		return callbacks.IntentTraceData{}, callbacks.ReplyPlanTraceData{}, state, err
	}

	selectedPlans := make([]callbacks.ReplyTaskPlanTraceData, 0, len(batch))
	for _, task := range batch {
		if planned, exists := plannedByKey[task.TaskKey]; exists {
			planned.TaskKey = task.TaskKey
			selectedPlans = append(selectedPlans, planned)
			continue
		}
		selectedPlans = append(selectedPlans, replyTaskPlanFromLedgerTask(task, sourceMessages))
	}
	if len(selectedPlans) == 0 && len(ensured) > 0 {
		return intent, callbacks.ReplyPlanTraceData{}, runtimeTaskState(turn, batch, hasMore), nil
	}
	filteredIntent := filterIntentForReplyTaskPlans(intent, selectedPlans)
	filteredPlan := buildReplyPlan(filteredIntent, selectIntentPromptPack(filteredIntent))
	filteredPlan.TaskPlans = selectedPlans
	return filteredIntent, filteredPlan, runtimeTaskState(turn, batch, hasMore), nil
}

func runtimeTaskScope(req RunInput) (*models.AIReplyTurn, []models.Message, bool) {
	if req.UserMessage.AIReplyTurnID <= 0 || req.UserMessage.AIReplyTurnVersion <= 0 ||
		req.JobID <= 0 || !services.AIReplyTurnService.EnabledFor(&req.Conversation) || !services.AIReplyTurnTaskService.Enabled() {
		return nil, nil, false
	}
	turn := repositories.AIReplyTurnRepository.GetInTenant(sqls.DB(), req.UserMessage.AIReplyTurnID, req.Conversation.TenantID)
	if turn == nil || turn.ConversationID != req.Conversation.ID || turn.SessionNo != req.UserMessage.SessionNo ||
		req.UserMessage.AIReplyTurnVersion != turn.Version || turn.StoreID != req.Conversation.StoreID ||
		turn.StoreStaffBindingID != req.Conversation.StoreStaffBindingID {
		return nil, nil, false
	}
	messages := repositories.MessageRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", turn.TenantID).
		Eq("conversation_id", turn.ConversationID).
		Eq("session_no", turn.SessionNo).
		Eq("ai_reply_turn_id", turn.ID).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		Where("recalled_at IS NULL AND send_status NOT IN (?, ?)", enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Asc("ai_reply_turn_version").
		Asc("id"))
	return turn, messages, true
}

func runtimeTaskSourcesCovered(messages []models.Message, tasks []models.AIReplyTurnTask) bool {
	if len(messages) == 0 || len(tasks) == 0 {
		return false
	}
	represented := make(map[int64]struct{}, len(tasks))
	for _, task := range tasks {
		represented[task.SourceMessageID] = struct{}{}
	}
	for _, message := range messages {
		if _, ok := represented[message.ID]; !ok {
			return false
		}
	}
	return true
}

func buildRuntimeTaskInputs(plans []callbacks.ReplyTaskPlanTraceData, fallbackMessageID int64, sourceMessages []models.Message, tenantID, turnID int64) ([]services.AIReplyTurnTaskInput, map[string]callbacks.ReplyTaskPlanTraceData, error) {
	inputs := make([]services.AIReplyTurnTaskInput, 0, len(plans))
	plannedByKey := make(map[string]callbacks.ReplyTaskPlanTraceData, len(plans))
	usedSourceMessageIDs := make(map[int64]struct{}, len(sourceMessages))
	plannedBySourceMessageID := make(map[int64][]callbacks.ReplyTaskPlanTraceData, len(sourceMessages))
	for index, plan := range plans {
		// 纯标点/空白消息：归一化后无实质文本，不创建无意义任务，直接跳过。
		if normalizeRuntimeTaskText(plan.Text) == "" {
			continue
		}
		sourceMessageID, spanStart, spanEnd := matchRuntimeTaskSourceMessageWithSpan(plan, fallbackMessageID, sourceMessages, usedSourceMessageIDs)
		if sourceMessageID <= 0 {
			return nil, nil, fmt.Errorf("AI reply task source message unavailable")
		}
		usedSourceMessageIDs[sourceMessageID] = struct{}{}
		input := services.AIReplyTurnTaskInput{
			TenantID:        tenantID,
			TurnID:          turnID,
			SourceMessageID: sourceMessageID,
			SequenceNo:      index + 1,
			TaskType:        runtimeTaskTypeForPlan(plan),
			Intent:          plan.Intent,
			SubIntent:       plan.SubIntent,
			RequestMode:     plan.RequestMode,
			RelationType:    plan.RelationType,
			ResourceAction:  plan.ResourceAction,
			QuestionText:    plan.Text,
			// 契约 10.2：来源片段确定性绑定，防止正文与意图跨消息串线。
			SourceSpanStart: spanStart,
			SourceSpanEnd:   spanEnd,
		}
		// 契约 10.8：把 Intent 建议的答案义务固化为 answer_requirement_set.v1。
		if requirementsJSON := buildAnswerRequirementsJSON(plan, spanStart, spanEnd); requirementsJSON != "" {
			input.AnswerRequirementsJSON = requirementsJSON
		}
		taskKey := services.AIReplyTurnTaskService.StableTaskKey(input)
		plan.TaskKey = taskKey
		inputs = append(inputs, input)
		plannedByKey[taskKey] = plan
		plannedBySourceMessageID[sourceMessageID] = append(plannedBySourceMessageID[sourceMessageID], plan)
	}
	for _, source := range sourceMessages {
		if _, represented := usedSourceMessageIDs[source.ID]; represented {
			continue
		}
		sourceText := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(source.MessageType, source.Content, source.Payload))
		sourceFingerprint := normalizeRuntimeTaskText(sourceText)
		if sourceFingerprint == "" {
			continue
		}
		var duplicatePlans []callbacks.ReplyTaskPlanTraceData
		for _, candidate := range sourceMessages {
			if candidate.ID == source.ID || normalizeRuntimeTaskText(utils.BuildRuntimeMessageTextWithPayload(candidate.MessageType, candidate.Content, candidate.Payload)) != sourceFingerprint {
				continue
			}
			if planned := plannedBySourceMessageID[candidate.ID]; len(planned) > 0 {
				duplicatePlans = append([]callbacks.ReplyTaskPlanTraceData(nil), planned...)
				break
			}
		}
		if len(duplicatePlans) == 0 {
			continue
		}
		duplicatedForSource := make([]callbacks.ReplyTaskPlanTraceData, 0, len(duplicatePlans))
		for _, duplicatePlan := range duplicatePlans {
			duplicatePlan.TaskKey = ""
			questionText := strings.TrimSpace(duplicatePlan.Text)
			if questionText == "" {
				questionText = sourceText
			}
			spanStart, spanEnd := runtimeTaskSpanWithinSource(questionText, sourceText)
			input := services.AIReplyTurnTaskInput{
				TenantID:        tenantID,
				TurnID:          turnID,
				SourceMessageID: source.ID,
				SequenceNo:      len(inputs) + 1,
				TaskType:        runtimeTaskTypeForPlan(duplicatePlan),
				Intent:          duplicatePlan.Intent,
				SubIntent:       duplicatePlan.SubIntent,
				RequestMode:     duplicatePlan.RequestMode,
				RelationType:    duplicatePlan.RelationType,
				ResourceAction:  duplicatePlan.ResourceAction,
				QuestionText:    questionText,
				SourceSpanStart: spanStart,
				SourceSpanEnd:   spanEnd,
			}
			if requirementsJSON := buildAnswerRequirementsJSON(duplicatePlan, spanStart, spanEnd); requirementsJSON != "" {
				input.AnswerRequirementsJSON = requirementsJSON
			}
			taskKey := services.AIReplyTurnTaskService.StableTaskKey(input)
			duplicatePlan.TaskKey = taskKey
			inputs = append(inputs, input)
			plannedByKey[taskKey] = duplicatePlan
			duplicatedForSource = append(duplicatedForSource, duplicatePlan)
		}
		usedSourceMessageIDs[source.ID] = struct{}{}
		plannedBySourceMessageID[source.ID] = duplicatedForSource
	}
	return inputs, plannedByKey, nil
}

func runtimeTaskSpanWithinSource(questionText, sourceText string) (int, int) {
	if normalizeRuntimeTaskText(questionText) == "" || !strings.Contains(normalizeRuntimeTaskText(sourceText), normalizeRuntimeTaskText(questionText)) {
		return 0, 0
	}
	return 0, len([]rune(sourceText))
}

// matchRuntimeTaskSourceMessageWithSpan 契约 10.2/4.14：来源绑定按
// 1) 归一化全文相等；2) 归一化包含（正文片段真实存在于该消息，返回 rune span）；
// 3) sequence/最后消息兜底（span 为 0，视为无证明）。
// 包含式绑定取代纯 sequence 优先级，修复 U1 正文配 U2 意图的串线。
func matchRuntimeTaskSourceMessageWithSpan(plan callbacks.ReplyTaskPlanTraceData, fallbackMessageID int64, messages []models.Message, used map[int64]struct{}) (int64, int, int) {
	needle := normalizeRuntimeTaskText(plan.Text)
	if needle != "" {
		for _, message := range messages {
			raw := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
			candidate := normalizeRuntimeTaskText(raw)
			if _, alreadyUsed := used[message.ID]; alreadyUsed || candidate == "" {
				continue
			}
			if candidate == needle {
				return message.ID, 0, len([]rune(raw))
			}
		}
		// 一条客户消息可以拆成多个独立任务；没有未使用的精确来源时，
		// 允许后续任务继续绑定同一条消息，而不是错误落到下一条消息。
		for _, message := range messages {
			raw := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
			if normalizeRuntimeTaskText(raw) == needle {
				return message.ID, 0, len([]rune(raw))
			}
		}
		// 包含式：fragment 必须真实存在于该消息原文（去运输包装后）。
		for _, message := range messages {
			raw := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
			candidate := normalizeRuntimeTaskText(raw)
			if _, alreadyUsed := used[message.ID]; alreadyUsed || candidate == "" || len(candidate) < len(needle) {
				continue
			}
			if strings.Contains(candidate, needle) {
				runes := []rune(raw)
				return message.ID, 0, len(runes)
			}
		}
		for _, message := range messages {
			raw := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
			candidate := normalizeRuntimeTaskText(raw)
			if candidate != "" && len(candidate) >= len(needle) && strings.Contains(candidate, needle) {
				return message.ID, 0, len([]rune(raw))
			}
		}
	}
	messageID := matchRuntimeTaskSourceMessage(plan, fallbackMessageID, messages, used)
	return messageID, 0, 0
}

func matchRuntimeTaskSourceMessage(plan callbacks.ReplyTaskPlanTraceData, fallbackMessageID int64, messages []models.Message, used map[int64]struct{}) int64 {
	// 严格文本哈希优先。模型改写后的 task.text 没有原文证据时，必须绑定
	// 当前触发消息；sequence 是任务顺序，不是 Turn 内消息序号，不能拿它猜来源。
	needle := normalizeRuntimeTaskText(plan.Text)
	if needle != "" {
		for _, message := range messages {
			candidate := normalizeRuntimeTaskText(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
			if _, alreadyUsed := used[message.ID]; !alreadyUsed && candidate != "" && candidate == needle {
				return message.ID
			}
		}
		for _, message := range messages {
			candidate := normalizeRuntimeTaskText(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
			if candidate != "" && candidate == needle {
				return message.ID
			}
		}
	}
	for _, message := range messages {
		if message.ID == fallbackMessageID {
			return message.ID
		}
	}
	if len(messages) > 0 {
		return messages[len(messages)-1].ID
	}
	return fallbackMessageID
}

func normalizeRuntimeTaskText(value string) string {
	value = strings.ToLower(strings.TrimSpace(currentTurnDisplayText(value)))
	replacer := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "", "，", "", "。", "", "！", "", "!", "", "？", "", "?", "", "：", "", ":", "", "；", "", ";", "")
	return replacer.Replace(value)
}

func runtimeTaskTypeForPlan(plan callbacks.ReplyTaskPlanTraceData) enums.AIReplyTurnTaskType {
	if plan.Output == "structured_resource_commit" || plan.Intent == "hotel_variable" || strings.TrimSpace(plan.ResourceAction) != "" {
		return enums.AIReplyTurnTaskTypeResource
	}
	if plan.Output == "human_route_confirmation_or_dispatch" || plan.Intent == "human_complaint_risk" {
		return enums.AIReplyTurnTaskTypeHuman
	}
	if plan.Output == "knowledge_text_reply" || plan.Intent == "hotel_info" {
		return enums.AIReplyTurnTaskTypeKnowledge
	}
	return enums.AIReplyTurnTaskTypeText
}

func runtimeTaskState(turn *models.AIReplyTurn, tasks []models.AIReplyTurnTask, hasMore bool) runtimeTaskBatchState {
	state := runtimeTaskBatchState{Enabled: turn != nil, HasMore: hasMore}
	if turn != nil {
		state.TurnID = turn.ID
		state.TurnVersion = turn.Version
	}
	for _, task := range tasks {
		state.SelectedTaskKeys = append(state.SelectedTaskKeys, task.TaskKey)
		switch {
		case task.Status == enums.AIReplyTurnTaskStatusFailed || task.Status == enums.AIReplyTurnTaskStatusHandoffPending:
			state.FailedTaskKeys = append(state.FailedTaskKeys, task.TaskKey)
		case task.TaskType == enums.AIReplyTurnTaskTypeHuman:
			state.HumanTaskKeys = append(state.HumanTaskKeys, task.TaskKey)
		case task.Status == enums.AIReplyTurnTaskStatusCommitted:
			state.CommittedTaskCount++
		}
	}
	return state
}

func aiReplyTurnTaskLedgerTerminal(status enums.AIReplyTurnTaskStatus) bool {
	switch status {
	case enums.AIReplyTurnTaskStatusDelivered, enums.AIReplyTurnTaskStatusCovered,
		enums.AIReplyTurnTaskStatusHandoff, enums.AIReplyTurnTaskStatusSkipped,
		enums.AIReplyTurnTaskStatusSuperseded, enums.AIReplyTurnTaskStatusFailed,
		enums.AIReplyTurnTaskStatusCommitted:
		return true
	default:
		return false
	}
}

func replyTaskPlansFromLedger(tasks []models.AIReplyTurnTask, messages []models.Message) []callbacks.ReplyTaskPlanTraceData {
	ret := make([]callbacks.ReplyTaskPlanTraceData, 0, len(tasks))
	for _, task := range tasks {
		ret = append(ret, replyTaskPlanFromLedgerTask(task, messages))
	}
	return ret
}

func replyTaskPlanFromLedgerTask(task models.AIReplyTurnTask, messages []models.Message) callbacks.ReplyTaskPlanTraceData {
	text := ""
	for _, message := range messages {
		if message.ID == task.SourceMessageID {
			text = strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
			break
		}
	}
	output := "text_reply"
	switch task.TaskType {
	case enums.AIReplyTurnTaskTypeKnowledge:
		output = "knowledge_text_reply"
	case enums.AIReplyTurnTaskTypeResource:
		output = "structured_resource_commit"
	case enums.AIReplyTurnTaskTypeHuman:
		output = "human_route_confirmation_or_dispatch"
	}
	return callbacks.ReplyTaskPlanTraceData{
		TaskKey: task.TaskKey, Sequence: task.SequenceNo, Intent: task.Intent, SubIntent: task.SubIntent, Text: text,
		RelationType: task.RelationType,
		Output:       output, ResourceAction: task.ResourceAction,
	}
}

func intentFromReplyTaskPlans(plans []callbacks.ReplyTaskPlanTraceData, reason string) callbacks.IntentTraceData {
	intent := callbacks.IntentTraceData{ShouldReply: len(plans) > 0, MatchMode: "task_ledger", Reason: reason}
	for _, plan := range plans {
		item := callbacks.IntentTaskTraceData{
			Sequence: plan.Sequence, Intent: plan.Intent, SubIntent: plan.SubIntent, Text: plan.Text,
			RequestMode: plan.RequestMode, ResourceAction: plan.ResourceAction,
		}
		switch runtimeTaskTypeForPlan(plan) {
		case enums.AIReplyTurnTaskTypeKnowledge:
			item.NeedsKnowledge = true
			intent.NeedsKnowledge = true
		case enums.AIReplyTurnTaskTypeResource:
			item.NeedsResource = true
			intent.NeedsResource = true
			if action := strings.TrimSpace(plan.ResourceAction); action != "" {
				intent.ResourceActions = appendIfMissing(intent.ResourceActions, action)
			}
		case enums.AIReplyTurnTaskTypeHuman:
			item.NeedsHumanRoute = true
			intent.NeedsHumanRoute = true
		}
		intent.IntentTasks = append(intent.IntentTasks, item)
	}
	intent = deriveModelIntentFromTasks(intent)
	intent.DetectedIntent = intent.PrimaryIntent
	if len(intent.ResourceActions) > 0 {
		intent.ResourceAction = intent.ResourceActions[0]
		intent.ResourceType = hotelVariableResourceTypeFromAction(intent.ResourceAction)
	}
	return intent
}

func filterIntentForReplyTaskPlans(original callbacks.IntentTraceData, plans []callbacks.ReplyTaskPlanTraceData) callbacks.IntentTraceData {
	filtered := intentFromReplyTaskPlans(plans, original.Reason)
	filtered.IntentConfidence = original.IntentConfidence
	filtered.MatchedConfigID = original.MatchedConfigID
	filtered.MatchedConfig = original.MatchedConfig
	filtered.MatchMode = original.MatchMode
	filtered.ToolCodes = append([]string(nil), original.ToolCodes...)
	filtered.HumanRoutePolicy = original.HumanRoutePolicy
	filtered.NeedsTool = original.NeedsTool
	filtered.NeedsClarification = original.NeedsClarification
	filtered.DialogueAct = original.DialogueAct
	if filtered.ResourceType == "" {
		filtered.ResourceType = original.ResourceType
	}
	return filtered
}

func assignEphemeralTaskKeys(plan callbacks.ReplyPlanTraceData) callbacks.ReplyPlanTraceData {
	for index := range plan.TaskPlans {
		if strings.TrimSpace(plan.TaskPlans[index].TaskKey) == "" {
			plan.TaskPlans[index].TaskKey = fmt.Sprintf("task-%d", index+1)
		}
	}
	return plan
}

func sortReplyTaskPlansByKeyOrder(plans []callbacks.ReplyTaskPlanTraceData, keys []string) []callbacks.ReplyTaskPlanTraceData {
	order := make(map[string]int, len(keys))
	for index, key := range keys {
		order[key] = index
	}
	sort.SliceStable(plans, func(i, j int) bool {
		return order[plans[i].TaskKey] < order[plans[j].TaskKey]
	})
	return plans
}

// ToJobRef 把 RunInput 投影为 Job 引用（供覆盖标签写入）。
func (r RunInput) ToJobRef() *models.JobRef {
	if r.JobID <= 0 {
		return nil
	}
	return &models.JobRef{ID: r.JobID, TenantID: r.Conversation.TenantID}
}

// buildAnswerRequirementsJSON 由 trace Requirements（"kind|required"）构造
// 服务端分配 Key 的义务集合；subject span 由 Task 主来源字段承载。
func buildAnswerRequirementsJSON(plan callbacks.ReplyTaskPlanTraceData, spanStart, spanEnd int) string {
	if len(plan.Requirements) == 0 {
		return ""
	}
	set := contracts.AnswerRequirementSetV1{
		SchemaVersion: contracts.AnswerRequirementSetV1SchemaVersion,
	}
	for index, encoded := range plan.Requirements {
		kind, required := decodeRequirementSeed(encoded)
		set.Requirements = append(set.Requirements, contracts.AnswerRequirementItemV1{
			Key: fmt.Sprintf("R%d", index+1), Kind: kind, Required: required,
			Sequence: index + 1,
		})
	}
	raw, err := json.Marshal(set)
	if err != nil {
		return ""
	}
	return string(raw)
}

func decodeRequirementSeed(encoded string) (string, bool) {
	parts := strings.SplitN(encoded, "|", 2)
	kind := strings.TrimSpace(parts[0])
	required := len(parts) == 2 && parts[1] == "true"
	if kind == "" {
		kind = "other"
	}
	return kind, required
}
