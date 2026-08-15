package executor

import (
	"fmt"
	"sort"
	"strings"

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
		for index := range allTasks {
			task := allTasks[index]
			if task.SourceMessageID == req.UserMessage.ID && aiReplyTurnTaskLedgerTerminal(task.Status) {
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
	inputs, plannedByKey, err := buildRuntimeTaskInputs(replyPlan.TaskPlans, req.UserMessage.ID, sourceMessages)
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

func buildRuntimeTaskInputs(plans []callbacks.ReplyTaskPlanTraceData, fallbackMessageID int64, sourceMessages []models.Message) ([]services.AIReplyTurnTaskInput, map[string]callbacks.ReplyTaskPlanTraceData, error) {
	inputs := make([]services.AIReplyTurnTaskInput, 0, len(plans))
	plannedByKey := make(map[string]callbacks.ReplyTaskPlanTraceData, len(plans))
	usedSourceMessageIDs := make(map[int64]struct{}, len(sourceMessages))
	plannedBySourceMessageID := make(map[int64]callbacks.ReplyTaskPlanTraceData, len(sourceMessages))
	for index, plan := range plans {
		// 纯标点/空白消息：归一化后无实质文本，不创建无意义任务，直接跳过。
		if normalizeRuntimeTaskText(plan.Text) == "" {
			continue
		}
		sourceMessageID := matchRuntimeTaskSourceMessage(plan, fallbackMessageID, sourceMessages, usedSourceMessageIDs)
		if sourceMessageID <= 0 {
			return nil, nil, fmt.Errorf("AI reply task source message unavailable")
		}
		usedSourceMessageIDs[sourceMessageID] = struct{}{}
		input := services.AIReplyTurnTaskInput{
			SourceMessageID: sourceMessageID,
			SequenceNo:      index + 1,
			TaskType:        runtimeTaskTypeForPlan(plan),
			Intent:          plan.Intent,
			SubIntent:       plan.SubIntent,
			RequestMode:     plan.RequestMode,
			RelationType:    plan.RelationType,
			ResourceAction:  plan.ResourceAction,
			QuestionText:    plan.Text,
		}
		taskKey := services.AIReplyTurnTaskService.StableTaskKey(input)
		plan.TaskKey = taskKey
		inputs = append(inputs, input)
		plannedByKey[taskKey] = plan
		if _, exists := plannedBySourceMessageID[sourceMessageID]; !exists {
			plannedBySourceMessageID[sourceMessageID] = plan
		}
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
		var duplicatePlan callbacks.ReplyTaskPlanTraceData
		for _, candidate := range sourceMessages {
			if candidate.ID == source.ID || normalizeRuntimeTaskText(utils.BuildRuntimeMessageTextWithPayload(candidate.MessageType, candidate.Content, candidate.Payload)) != sourceFingerprint {
				continue
			}
			if planned, ok := plannedBySourceMessageID[candidate.ID]; ok {
				duplicatePlan = planned
				break
			}
		}
		if strings.TrimSpace(duplicatePlan.Intent) == "" && strings.TrimSpace(duplicatePlan.Output) == "" {
			continue
		}
		duplicatePlan.Text = sourceText
		input := services.AIReplyTurnTaskInput{
			SourceMessageID: source.ID,
			SequenceNo:      len(inputs) + 1,
			TaskType:        runtimeTaskTypeForPlan(duplicatePlan),
			Intent:          duplicatePlan.Intent,
			SubIntent:       duplicatePlan.SubIntent,
			RequestMode:     duplicatePlan.RequestMode,
			RelationType:    duplicatePlan.RelationType,
			ResourceAction:  duplicatePlan.ResourceAction,
			QuestionText:    sourceText,
		}
		taskKey := services.AIReplyTurnTaskService.StableTaskKey(input)
		duplicatePlan.TaskKey = taskKey
		inputs = append(inputs, input)
		plannedByKey[taskKey] = duplicatePlan
		usedSourceMessageIDs[source.ID] = struct{}{}
		plannedBySourceMessageID[source.ID] = duplicatePlan
	}
	return inputs, plannedByKey, nil
}

func matchRuntimeTaskSourceMessage(plan callbacks.ReplyTaskPlanTraceData, fallbackMessageID int64, messages []models.Message, used map[int64]struct{}) int64 {
	// 按文档绑定顺序，禁止字符串包含作为主匹配：
	// 1. 严格文本哈希（归一化后完全相等，不是 contains）优先。
	// 2. sequence 兜底：合法时对应当前 Turn 客户消息顺序。
	// 3. 仍无法唯一匹配时不猜测，回退到 fallbackMessageID / 最后一条消息。
	needle := normalizeRuntimeTaskText(plan.Text)
	if needle != "" {
		for _, message := range messages {
			candidate := normalizeRuntimeTaskText(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
			if _, alreadyUsed := used[message.ID]; !alreadyUsed && candidate != "" && candidate == needle {
				return message.ID
			}
		}
	}
	if plan.Sequence >= 1 && plan.Sequence <= len(messages) {
		message := messages[plan.Sequence-1]
		if _, alreadyUsed := used[message.ID]; !alreadyUsed {
			return message.ID
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
	if len(plans) > 0 {
		intent.PrimaryIntent = plans[0].Intent
		intent.SubIntent = plans[0].SubIntent
		intent.DetectedIntent = intent.PrimaryIntent
		intent.MatchedIntentCode = intent.PrimaryIntent
	}
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
