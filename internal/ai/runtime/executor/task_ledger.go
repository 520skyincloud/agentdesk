package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
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
	coverage := resolvedCoverageForJob(req, turn)
	if len(allTasks) == 0 || !runtimeTaskSourcesCovered(sourceMessages, allTasks, coverage) {
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
			HasMore:         services.AIReplyTurnTaskService.HasRunnable(turn.TenantID, turn.ID),
			TaskIDByTaskKey: make(map[string]int64, len(allTasks)),
		}
		for index := range allTasks {
			task := allTasks[index]
			state.TaskIDByTaskKey[task.TaskKey] = task.ID
			if task.SourceMessageID == req.UserMessage.ID &&
				(aiReplyTurnTaskLedgerTerminal(task.Status) || task.Status == enums.AIReplyTurnTaskStatusWaitingCoverage) {
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
	plans, err := replyTaskPlansFromLedger(batch, sourceMessages)
	if err != nil {
		return callbacks.IntentTraceData{}, callbacks.ReplyPlanTraceData{}, state, false, err
	}
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
	inputs, plannedByKey, err := buildRuntimeTaskInputs(
		replyPlan.TaskPlans, req.UserMessage.ID, sourceMessages, turn.TenantID, turn.ID,
	)
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
		if coverErr := services.AIReplyTurnTaskService.RecordResolvedCoverageDB(
			ctx.Tx, req.ToJobRef(), lockedTurn, ensured, resolvedCoverageItemsFromIntent(intent), time.Now(),
		); coverErr != nil {
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
		restored, restoreErr := replyTaskPlanFromLedgerTask(task, sourceMessages)
		if restoreErr != nil {
			return callbacks.IntentTraceData{}, callbacks.ReplyPlanTraceData{}, state, restoreErr
		}
		selectedPlans = append(selectedPlans, restored)
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

func runtimeTaskSourcesCovered(messages []models.Message, tasks []models.AIReplyTurnTask, coverage []contracts.ResolvedCoverageItemV1) bool {
	if len(messages) == 0 || len(tasks) == 0 {
		return len(messages) > 0 && runtimeCoverageRepresentsAllMessages(messages, coverage)
	}
	represented := make(map[int64]struct{}, len(tasks)+len(coverage))
	for _, task := range tasks {
		represented[task.SourceMessageID] = struct{}{}
	}
	for _, item := range coverage {
		if item.MessageID <= 0 || !runtimeCoverageStatusResolved(item.Status) {
			continue
		}
		represented[item.MessageID] = struct{}{}
	}
	for _, message := range messages {
		if _, ok := represented[message.ID]; !ok {
			return false
		}
	}
	return true
}

func resolvedCoverageForJob(req RunInput, turn *models.AIReplyTurn) []contracts.ResolvedCoverageItemV1 {
	if req.JobID <= 0 || turn == nil {
		return nil
	}
	job := repositories.AIReplyJobRepository.GetInTenant(sqls.DB(), req.JobID, turn.TenantID)
	if job == nil || job.TurnID != turn.ID || strings.TrimSpace(job.ResolvedCoverageJSON) == "" ||
		len(strings.TrimSpace(job.ResolvedCoverageFingerprint)) != sha256.Size*2 {
		return nil
	}
	raw := []byte(job.ResolvedCoverageJSON)
	sum := sha256.Sum256(raw)
	if !strings.EqualFold(strings.TrimSpace(job.ResolvedCoverageFingerprint), hex.EncodeToString(sum[:])) {
		slog.Warn("AI reply resolved coverage fingerprint mismatch",
			"tenant_id", job.TenantID, "job_id", job.ID, "turn_id", job.TurnID)
		return nil
	}
	resolved, err := contracts.DecodeResolvedTurnCoverageV1(raw)
	if err != nil || resolved.TurnID != turn.ID || resolved.TurnVersion > turn.Version {
		slog.Warn("AI reply resolved coverage ignored",
			"tenant_id", job.TenantID, "job_id", job.ID, "turn_id", job.TurnID, "error", err)
		return nil
	}
	return resolved.Items
}

func resolvedCoverageItemsFromIntent(intent callbacks.IntentTraceData) []contracts.ResolvedCoverageItemV1 {
	ret := make([]contracts.ResolvedCoverageItemV1, 0, len(intent.UtteranceCoverage))
	for _, item := range intent.UtteranceCoverage {
		if item.MessageID <= 0 {
			continue
		}
		status := "scheduled"
		if item.Status == "ignored" {
			status = "ignored"
		}
		ret = append(ret, contracts.ResolvedCoverageItemV1{
			MessageID: item.MessageID, Status: status, ReasonCode: strings.TrimSpace(item.ReasonCode),
		})
	}
	return ret
}

func runtimeCoverageRepresentsAllMessages(messages []models.Message, coverage []contracts.ResolvedCoverageItemV1) bool {
	represented := make(map[int64]struct{}, len(coverage))
	for _, item := range coverage {
		if item.MessageID > 0 && runtimeCoverageStatusResolved(item.Status) {
			represented[item.MessageID] = struct{}{}
		}
	}
	for _, message := range messages {
		if _, exists := represented[message.ID]; !exists {
			return false
		}
	}
	return len(messages) > 0
}

func runtimeCoverageStatusResolved(status string) bool {
	switch strings.TrimSpace(status) {
	case "covered", "routed", "ignored", "failed", "skipped", "superseded":
		return true
	default:
		return false
	}
}

func buildRuntimeTaskInputs(
	plans []callbacks.ReplyTaskPlanTraceData,
	fallbackMessageID int64,
	sourceMessages []models.Message,
	tenantID, turnID int64,
) ([]services.AIReplyTurnTaskInput, map[string]callbacks.ReplyTaskPlanTraceData, error) {
	inputs := make([]services.AIReplyTurnTaskInput, 0, len(plans))
	plannedByKey := make(map[string]callbacks.ReplyTaskPlanTraceData, len(plans))
	parentKeyByTaskKey := make(map[string]string, len(plans))
	taskKeyByQuestionUnitKey := make(map[string]string, len(plans))
	representedSourceMessageIDs := make(map[int64]struct{}, len(sourceMessages))
	plannedBySourceMessageID := make(map[int64]callbacks.ReplyTaskPlanTraceData, len(sourceMessages))
	for index, plan := range plans {
		// Whitespace-only inputs are covered by the persisted utterance coverage
		// ledger. A question mark is meaningful and is intentionally preserved by
		// normalizeRuntimeTaskText as a clarification task.
		if normalizeRuntimeTaskText(plan.Text) == "" {
			continue
		}
		strictSource := runtimeTaskPlanHasAuthoritativeSource(plan)
		sourceMessageID, spanStart, spanEnd := int64(0), 0, 0
		if strictSource {
			var exactText string
			var strictErr error
			sourceMessageID, spanStart, spanEnd, exactText, strictErr = strictRuntimeTaskSource(plan, sourceMessages)
			if strictErr != nil {
				return nil, nil, strictErr
			}
			plan.Text = exactText
		} else {
			sourceMessageID, spanStart, spanEnd = directRuntimeTaskSource(plan, sourceMessages)
			if sourceMessageID <= 0 {
				sourceMessageID, spanStart, spanEnd = matchRuntimeTaskSourceMessageWithSpan(plan, fallbackMessageID, sourceMessages)
			}
		}
		if sourceMessageID <= 0 {
			return nil, nil, fmt.Errorf("AI reply task source message unavailable")
		}
		representedSourceMessageIDs[sourceMessageID] = struct{}{}
		bindings, bindingsJSON, sourceSetFingerprint := normalizeTaskSourceBindings(plan.SourceBindings, sourceMessageID, spanStart, spanEnd)
		observationBindings, observationBindingsJSON := normalizeTaskObservationBindings(plan.ObservationBindings)
		plan.SourceMessageID = sourceMessageID
		plan.SourceSpanStart = spanStart
		plan.SourceSpanEnd = spanEnd
		plan.SourceBindings = bindings
		plan.ObservationBindings = observationBindings
		plan.SourceSetFingerprint = firstNonEmpty(plan.SourceSetFingerprint, sourceSetFingerprint)
		input := services.AIReplyTurnTaskInput{
			TenantID:                tenantID,
			TurnID:                  turnID,
			SourceMessageID:         sourceMessageID,
			SequenceNo:              index + 1,
			TaskType:                runtimeTaskTypeForPlan(plan),
			Intent:                  plan.Intent,
			SubIntent:               plan.SubIntent,
			RequestMode:             plan.RequestMode,
			RelationType:            plan.RelationType,
			ResourceAction:          plan.ResourceAction,
			QuestionText:            plan.Text,
			QuestionUnitKey:         plan.QuestionUnitKey,
			AnalysisRevision:        plan.AnalysisRevision,
			SourceBindingsJSON:      bindingsJSON,
			ObservationBindingsJSON: observationBindingsJSON,
			SourceSetFingerprint:    plan.SourceSetFingerprint,
			CanonicalQuestionHash:   plan.CanonicalQuestionHash,
			// 契约 10.2：来源片段确定性绑定，防止正文与意图跨消息串线。
			SourceSpanStart: spanStart,
			SourceSpanEnd:   spanEnd,
		}
		taskKey := services.AIReplyTurnTaskService.StableTaskKey(input)
		plan.TaskKey = taskKey
		if questionUnitKey := strings.TrimSpace(plan.QuestionUnitKey); questionUnitKey != "" {
			taskKeyByQuestionUnitKey[questionUnitKey] = taskKey
		}
		parentKeyByTaskKey[taskKey] = strings.TrimSpace(plan.ParentTaskKey)
		// 契约 10.8：TaskKey 与来源身份确定后再固化答案义务，避免空 taskKey/sourceMessageId。
		requirementsJSON, requirementsErr := buildAnswerRequirementsJSON(plan, taskKey, sourceMessageID, spanStart, spanEnd)
		if requirementsErr != nil {
			return nil, nil, requirementsErr
		}
		if requirementsJSON != "" {
			input.AnswerRequirementsJSON = requirementsJSON
		}
		inputs = append(inputs, input)
		plannedByKey[taskKey] = plan
		if _, exists := plannedBySourceMessageID[sourceMessageID]; !exists {
			plannedBySourceMessageID[sourceMessageID] = plan
		}
	}
	for _, source := range sourceMessages {
		if _, represented := representedSourceMessageIDs[source.ID]; represented {
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
		bindings, bindingsJSON, sourceSetFingerprint := normalizeTaskSourceBindings(
			nil, source.ID, 0, len([]rune(sourceText)),
		)
		duplicatePlan.SourceMessageID = source.ID
		duplicatePlan.SourceSpanStart = 0
		duplicatePlan.SourceSpanEnd = len([]rune(sourceText))
		duplicatePlan.SourceBindings = bindings
		// An equivalent text message is a separate source event. Reusing another
		// message's media dependency would attach the wrong image to this task.
		duplicatePlan.ObservationBindings = nil
		duplicatePlan.SourceSetFingerprint = sourceSetFingerprint
		input := services.AIReplyTurnTaskInput{
			TenantID:             tenantID,
			TurnID:               turnID,
			SourceMessageID:      source.ID,
			SequenceNo:           len(inputs) + 1,
			TaskType:             runtimeTaskTypeForPlan(duplicatePlan),
			Intent:               duplicatePlan.Intent,
			SubIntent:            duplicatePlan.SubIntent,
			RequestMode:          duplicatePlan.RequestMode,
			RelationType:         duplicatePlan.RelationType,
			ResourceAction:       duplicatePlan.ResourceAction,
			QuestionText:         sourceText,
			SourceSpanStart:      0,
			SourceSpanEnd:        len([]rune(sourceText)),
			SourceBindingsJSON:   bindingsJSON,
			SourceSetFingerprint: sourceSetFingerprint,
		}
		taskKey := services.AIReplyTurnTaskService.StableTaskKey(input)
		duplicatePlan.TaskKey = taskKey
		parentKeyByTaskKey[taskKey] = strings.TrimSpace(duplicatePlan.ParentTaskKey)
		requirementsJSON, requirementsErr := buildAnswerRequirementsJSON(duplicatePlan, taskKey, source.ID, 0, len([]rune(sourceText)))
		if requirementsErr != nil {
			return nil, nil, requirementsErr
		}
		if requirementsJSON != "" {
			input.AnswerRequirementsJSON = requirementsJSON
		}
		inputs = append(inputs, input)
		plannedByKey[taskKey] = duplicatePlan
		representedSourceMessageIDs[source.ID] = struct{}{}
		plannedBySourceMessageID[source.ID] = duplicatePlan
	}
	// QuestionUnit parent references are temporary within the current envelope
	// (Q1/Q2) until stable TaskKeys are known. Resolve them before the service
	// opens its transaction; durable parent TaskKeys pass through unchanged.
	for index := range inputs {
		taskKey := services.AIReplyTurnTaskService.StableTaskKey(inputs[index])
		parentKey := strings.TrimSpace(parentKeyByTaskKey[taskKey])
		if mapped := strings.TrimSpace(taskKeyByQuestionUnitKey[parentKey]); mapped != "" {
			parentKey = mapped
		}
		if parentKey == taskKey {
			parentKey = ""
		}
		inputs[index].RelatedTaskKey = parentKey
	}
	return inputs, plannedByKey, nil
}

// matchRuntimeTaskSourceMessageWithSpan 契约 10.2/4.14：来源绑定按
// 1) 归一化全文相等；2) 归一化包含（正文片段真实存在于该消息，返回 rune span）；
// 3) sequence/最后消息兜底（span 为 0，视为无证明）。
// 包含式绑定取代纯 sequence 优先级，修复 U1 正文配 U2 意图的串线。
func matchRuntimeTaskSourceMessageWithSpan(plan callbacks.ReplyTaskPlanTraceData, fallbackMessageID int64, messages []models.Message) (int64, int, int) {
	needle := normalizeRuntimeTaskText(plan.Text)
	if needle != "" {
		for _, message := range messages {
			raw := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
			candidate := normalizeRuntimeTaskText(raw)
			if candidate == "" {
				continue
			}
			if candidate == needle {
				return message.ID, 0, len([]rune(raw))
			}
		}
		// 包含式：fragment 必须真实存在于该消息原文（去运输包装后）。
		for _, message := range messages {
			raw := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
			candidate := normalizeRuntimeTaskText(raw)
			if candidate == "" || len(candidate) < len(needle) {
				continue
			}
			if strings.Contains(candidate, needle) {
				runes := []rune(raw)
				return message.ID, 0, len(runes)
			}
		}
	}
	messageID := matchRuntimeTaskSourceMessage(plan, fallbackMessageID, messages)
	return messageID, 0, 0
}

func runtimeTaskPlanHasAuthoritativeSource(plan callbacks.ReplyTaskPlanTraceData) bool {
	return strings.TrimSpace(plan.QuestionUnitKey) != "" || len(plan.SourceBindings) > 0 || strings.TrimSpace(plan.SourceSetFingerprint) != ""
}

func strictRuntimeTaskSource(plan callbacks.ReplyTaskPlanTraceData, messages []models.Message) (int64, int, int, string, error) {
	if plan.SourceMessageID <= 0 || len(plan.SourceBindings) == 0 {
		return 0, 0, 0, "", fmt.Errorf("AI reply v3 task source binding is incomplete")
	}
	messageByID := make(map[int64]models.Message, len(messages))
	for _, message := range messages {
		messageByID[message.ID] = message
	}
	bindings := append([]callbacks.TaskSourceBindingTraceData(nil), plan.SourceBindings...)
	sort.SliceStable(bindings, func(i, j int) bool {
		if bindings[i].MessageID != bindings[j].MessageID {
			return bindings[i].MessageID < bindings[j].MessageID
		}
		if bindings[i].SpanStart != bindings[j].SpanStart {
			return bindings[i].SpanStart < bindings[j].SpanStart
		}
		return bindings[i].SpanEnd < bindings[j].SpanEnd
	})
	parts := make([]string, 0, len(bindings))
	primaryStart, primaryEnd := -1, -1
	for _, binding := range bindings {
		message, exists := messageByID[binding.MessageID]
		if !exists {
			return 0, 0, 0, "", fmt.Errorf("AI reply v3 task source message %d is outside the current turn", binding.MessageID)
		}
		text := runtimeTaskSourceText(message)
		runes := []rune(text)
		if binding.SpanStart < 0 || binding.SpanEnd <= binding.SpanStart || binding.SpanEnd > len(runes) {
			return 0, 0, 0, "", fmt.Errorf("AI reply v3 task source span [%d,%d) is invalid for message %d", binding.SpanStart, binding.SpanEnd, binding.MessageID)
		}
		part := strings.TrimSpace(string(runes[binding.SpanStart:binding.SpanEnd]))
		if part == "" {
			return 0, 0, 0, "", fmt.Errorf("AI reply v3 task source span is empty for message %d", binding.MessageID)
		}
		parts = append(parts, part)
		if binding.MessageID == plan.SourceMessageID && primaryStart < 0 {
			primaryStart, primaryEnd = binding.SpanStart, binding.SpanEnd
		}
	}
	if primaryStart < 0 {
		return 0, 0, 0, "", fmt.Errorf("AI reply v3 primary source message is not present in source bindings")
	}
	if plan.SourceSpanStart != primaryStart || plan.SourceSpanEnd != primaryEnd {
		return 0, 0, 0, "", fmt.Errorf("AI reply v3 primary source span does not match source bindings")
	}
	return plan.SourceMessageID, primaryStart, primaryEnd, strings.Join(parts, " "), nil
}

func matchRuntimeTaskSourceMessage(plan callbacks.ReplyTaskPlanTraceData, fallbackMessageID int64, messages []models.Message) int64 {
	// 按文档绑定顺序，禁止字符串包含作为主匹配：
	// 1. 严格文本哈希（归一化后完全相等，不是 contains）优先。
	// 2. sequence 兜底：合法时对应当前 Turn 客户消息顺序。
	// 3. 仍无法唯一匹配时不猜测，回退到 fallbackMessageID / 最后一条消息。
	needle := normalizeRuntimeTaskText(plan.Text)
	if needle != "" {
		for _, message := range messages {
			candidate := normalizeRuntimeTaskText(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
			if candidate != "" && candidate == needle {
				return message.ID
			}
		}
	}
	if plan.Sequence >= 1 && plan.Sequence <= len(messages) {
		return messages[plan.Sequence-1].ID
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

func directRuntimeTaskSource(plan callbacks.ReplyTaskPlanTraceData, messages []models.Message) (int64, int, int) {
	if plan.SourceMessageID <= 0 {
		return 0, 0, 0
	}
	for _, message := range messages {
		if message.ID != plan.SourceMessageID {
			continue
		}
		runeCount := len([]rune(strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))))
		if plan.SourceSpanStart < 0 || plan.SourceSpanEnd <= plan.SourceSpanStart || plan.SourceSpanEnd > runeCount {
			return message.ID, 0, runeCount
		}
		return message.ID, plan.SourceSpanStart, plan.SourceSpanEnd
	}
	return 0, 0, 0
}

func normalizeTaskSourceBindings(bindings []callbacks.TaskSourceBindingTraceData, sourceMessageID int64, spanStart, spanEnd int) ([]callbacks.TaskSourceBindingTraceData, string, string) {
	if len(bindings) == 0 {
		bindings = []callbacks.TaskSourceBindingTraceData{{MessageID: sourceMessageID, SpanStart: spanStart, SpanEnd: spanEnd}}
	} else {
		bindings = append([]callbacks.TaskSourceBindingTraceData(nil), bindings...)
	}
	sort.SliceStable(bindings, func(i, j int) bool {
		if bindings[i].MessageID != bindings[j].MessageID {
			return bindings[i].MessageID < bindings[j].MessageID
		}
		if bindings[i].SpanStart != bindings[j].SpanStart {
			return bindings[i].SpanStart < bindings[j].SpanStart
		}
		return bindings[i].SpanEnd < bindings[j].SpanEnd
	})
	raw, _ := json.Marshal(bindings)
	sum := sha256.Sum256(raw)
	return bindings, string(raw), hex.EncodeToString(sum[:])
}

func normalizeTaskObservationBindings(bindings []callbacks.TaskObservationBindingTraceData) ([]callbacks.TaskObservationBindingTraceData, string) {
	if len(bindings) == 0 {
		return []callbacks.TaskObservationBindingTraceData{}, ""
	}
	seen := make(map[string]struct{}, len(bindings))
	normalized := make([]callbacks.TaskObservationBindingTraceData, 0, len(bindings))
	for _, binding := range bindings {
		if binding.MessageID <= 0 || binding.SourceRevision <= 0 {
			continue
		}
		key := fmt.Sprintf("%d/%d", binding.MessageID, binding.SourceRevision)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, binding)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].MessageID == normalized[j].MessageID {
			return normalized[i].SourceRevision < normalized[j].SourceRevision
		}
		return normalized[i].MessageID < normalized[j].MessageID
	})
	if len(normalized) == 0 {
		return normalized, ""
	}
	raw, _ := json.Marshal(normalized)
	return normalized, string(raw)
}

func normalizeRuntimeTaskText(value string) string {
	value = strings.ToLower(strings.TrimSpace(currentTurnDisplayText(value)))
	original := value
	replacer := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "", "，", "", "。", "", "！", "", "!", "", "？", "", "?", "", "：", "", ":", "", "；", "", ";", "")
	value = replacer.Replace(value)
	if value != "" {
		return value
	}
	if strings.ContainsAny(original, "？?") {
		return "__question_mark__"
	}
	if strings.ContainsAny(original, "！!") {
		return "__exclamation_mark__"
	}
	return ""
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
	state := runtimeTaskBatchState{
		Enabled: turn != nil, HasMore: hasMore,
		TaskIDByTaskKey: make(map[string]int64, len(tasks)),
	}
	if turn != nil {
		state.TurnID = turn.ID
		state.TurnVersion = turn.Version
	}
	for _, task := range tasks {
		state.TaskIDByTaskKey[task.TaskKey] = task.ID
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

func replyTaskPlansFromLedger(tasks []models.AIReplyTurnTask, messages []models.Message) ([]callbacks.ReplyTaskPlanTraceData, error) {
	ret := make([]callbacks.ReplyTaskPlanTraceData, 0, len(tasks))
	parentByID := make(map[int64]ledgerParentContext, len(tasks))
	for _, task := range tasks {
		if task.ID > 0 && strings.TrimSpace(task.TaskKey) != "" {
			parentByID[task.ID] = ledgerParentContext{TaskKey: task.TaskKey, Topic: ledgerTaskContextText(task, messages)}
		}
	}
	for _, task := range tasks {
		plan, err := replyTaskPlanFromLedgerTaskWithParents(task, messages, parentByID)
		if err != nil {
			return nil, err
		}
		ret = append(ret, plan)
	}
	return ret, nil
}

func replyTaskPlanFromLedgerTask(task models.AIReplyTurnTask, messages []models.Message) (callbacks.ReplyTaskPlanTraceData, error) {
	parentByID := map[int64]ledgerParentContext{}
	if task.RelatedTaskID > 0 && sqls.DB() != nil {
		if parent, err := repositories.AIReplyTurnTaskRepository.GetForUpdateInTenant(sqls.DB(), task.RelatedTaskID, task.TenantID); err == nil && parent != nil {
			parentByID[parent.ID] = ledgerParentContext{TaskKey: parent.TaskKey, Topic: ledgerTaskContextText(*parent, messages)}
		}
	}
	return replyTaskPlanFromLedgerTaskWithParents(task, messages, parentByID)
}

type ledgerParentContext struct {
	TaskKey string
	Topic   string
}

func ledgerTaskContextText(task models.AIReplyTurnTask, messages []models.Message) string {
	for _, message := range messages {
		if message.ID == task.SourceMessageID {
			return runtimeTaskSourceSpanText(runtimeTaskSourceText(message), task.SourceSpanStart, task.SourceSpanEnd)
		}
	}
	return ""
}

func replyTaskPlanFromLedgerTaskWithParents(task models.AIReplyTurnTask, messages []models.Message, parentByID map[int64]ledgerParentContext) (callbacks.ReplyTaskPlanTraceData, error) {
	text := ""
	for _, message := range messages {
		if message.ID == task.SourceMessageID {
			text = runtimeTaskSourceText(message)
			text = runtimeTaskSourceSpanText(text, task.SourceSpanStart, task.SourceSpanEnd)
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
	plan := callbacks.ReplyTaskPlanTraceData{
		TaskKey: task.TaskKey, Sequence: task.SequenceNo, Intent: task.Intent, SubIntent: task.SubIntent, Text: text,
		RequestMode: task.RequestMode, RelationType: task.RelationType,
		QuestionUnitKey: task.QuestionUnitKey, SourceMessageID: task.SourceMessageID,
		AnalysisRevision: task.AnalysisRevision, SourceSpanStart: task.SourceSpanStart,
		SourceSpanEnd: task.SourceSpanEnd, SourceSetFingerprint: task.SourceSetFingerprint,
		CanonicalQuestionHash: task.CanonicalQuestionHash,
		Output:                output, ResourceAction: task.ResourceAction,
	}
	if parent, ok := parentByID[task.RelatedTaskID]; ok && strings.TrimSpace(parent.TaskKey) != "" {
		plan.ParentTaskKey = strings.TrimSpace(parent.TaskKey)
		plan.ResolvedTopic = firstNonEmptyQuestionTopic(parent.Topic, task.SubIntent, task.Intent)
	}
	if strings.TrimSpace(task.SourceBindingsJSON) != "" {
		_ = json.Unmarshal([]byte(task.SourceBindingsJSON), &plan.SourceBindings)
	}
	if strings.TrimSpace(task.ObservationBindingsJSON) != "" {
		_ = json.Unmarshal([]byte(task.ObservationBindingsJSON), &plan.ObservationBindings)
	}
	if strings.TrimSpace(task.AnswerRequirementsJSON) != "" {
		set, err := contracts.DecodeAnswerRequirementSetV1([]byte(task.AnswerRequirementsJSON))
		if err != nil {
			return callbacks.ReplyTaskPlanTraceData{}, fmt.Errorf("AI reply task %s answer requirements are invalid: %w", task.TaskKey, err)
		}
		if err := contracts.ValidateAnswerRequirementBindingV1(
			set, task.TaskKey, task.SourceMessageID, task.SourceSpanStart, task.SourceSpanEnd,
		); err != nil {
			return callbacks.ReplyTaskPlanTraceData{}, fmt.Errorf("AI reply task %s answer requirements do not match task: %w", task.TaskKey, err)
		}
		for _, requirement := range set.Requirements {
			plan.Requirements = append(plan.Requirements, fmt.Sprintf("%s|%t", requirement.Kind, requirement.Required))
		}
	}
	return plan, nil
}

func runtimeTaskSourceText(message models.Message) string {
	switch message.MessageType {
	case enums.IMMessageTypeText, enums.IMMessageTypeHTML:
		return strings.TrimSpace(message.Content)
	case enums.IMMessageTypeVoice, enums.IMMessageTypeImage, enums.IMMessageTypeAttachment,
		enums.IMMessageTypeVideo, enums.IMMessageTypeGIF:
		mediaText, _, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
		if strings.TrimSpace(status) == "understood" && strings.TrimSpace(mediaText) != "" {
			return strings.TrimSpace(mediaText)
		}
	}
	return strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
}

func runtimeTaskSourceSpanText(text string, start, end int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if start < 0 || end <= start || end > len(runes) {
		return text
	}
	return strings.TrimSpace(string(runes[start:end]))
}

func intentFromReplyTaskPlans(plans []callbacks.ReplyTaskPlanTraceData, reason string) callbacks.IntentTraceData {
	intent := callbacks.IntentTraceData{ShouldReply: len(plans) > 0, MatchMode: "task_ledger", Reason: reason}
	hasHuman := false
	hasAnswerableBusiness := false
	for _, plan := range plans {
		item := callbacks.IntentTaskTraceData{
			Sequence: plan.Sequence, Intent: plan.Intent, SubIntent: plan.SubIntent, Text: plan.Text,
			RequestMode: plan.RequestMode, ResourceAction: plan.ResourceAction,
			QuestionUnitKey: plan.QuestionUnitKey, SourceMessageID: plan.SourceMessageID,
			AnalysisRevision: plan.AnalysisRevision, SourceSpanStart: plan.SourceSpanStart,
			SourceSpanEnd: plan.SourceSpanEnd, SourceBindings: append([]callbacks.TaskSourceBindingTraceData(nil), plan.SourceBindings...),
			ObservationBindings:  append([]callbacks.TaskObservationBindingTraceData(nil), plan.ObservationBindings...),
			SourceSetFingerprint: plan.SourceSetFingerprint, CanonicalQuestionHash: plan.CanonicalQuestionHash,
			RelationType: plan.RelationType, ParentTaskKey: plan.ParentTaskKey,
			ResolvedTopic: plan.ResolvedTopic, InheritedRequirements: append([]string(nil), plan.InheritedRequirements...),
			Requirements: append([]string(nil), plan.Requirements...),
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
			hasHuman = true
		}
		if item.NeedsKnowledge || item.NeedsResource || item.Intent == "hotel_info" || item.Intent == "hotel_variable" || item.Intent == "service_request" {
			hasAnswerableBusiness = true
		}
		intent.IntentTasks = append(intent.IntentTasks, item)
	}
	if len(plans) > 0 {
		intent.PrimaryIntent = primaryRuntimeIntentFromReplyPlans(plans)
		for _, plan := range plans {
			if plan.Intent == intent.PrimaryIntent {
				intent.SubIntent = plan.SubIntent
				break
			}
		}
		intent.DetectedIntent = intent.PrimaryIntent
		intent.MatchedIntentCode = intent.PrimaryIntent
	}
	if len(intent.ResourceActions) > 0 {
		intent.ResourceAction = intent.ResourceActions[0]
		intent.ResourceType = hotelVariableResourceTypeFromAction(intent.ResourceAction)
	}
	intent.NeedsHumanRoute = hasHuman && !hasAnswerableBusiness
	if !intent.NeedsHumanRoute {
		intent.HumanRoutePolicy = ""
	}
	return intent
}

func primaryRuntimeIntentFromReplyPlans(plans []callbacks.ReplyTaskPlanTraceData) string {
	for _, plan := range plans {
		if plan.Intent == "hotel_info" && isCheckinProcessSubIntent(plan.SubIntent) {
			return "hotel_info"
		}
	}
	for _, plan := range plans {
		if plan.Intent == "hotel_variable" {
			return "hotel_variable"
		}
	}
	for _, plan := range plans {
		if plan.Intent == "hotel_info" {
			return "hotel_info"
		}
	}
	for _, plan := range plans {
		if plan.Intent != "interaction" && plan.Intent != "human_complaint_risk" {
			return plan.Intent
		}
	}
	for _, plan := range plans {
		if plan.Intent != "human_complaint_risk" {
			return plan.Intent
		}
	}
	return strings.TrimSpace(plans[0].Intent)
}

func filterIntentForReplyTaskPlans(original callbacks.IntentTraceData, plans []callbacks.ReplyTaskPlanTraceData) callbacks.IntentTraceData {
	filtered := intentFromReplyTaskPlans(plans, original.Reason)
	filtered.IntentConfidence = original.IntentConfidence
	filtered.MatchedConfigID = original.MatchedConfigID
	filtered.MatchedConfig = original.MatchedConfig
	filtered.MatchMode = original.MatchMode
	filtered.ToolCodes = append([]string(nil), original.ToolCodes...)
	if filtered.NeedsHumanRoute {
		filtered.HumanRoutePolicy = original.HumanRoutePolicy
	} else {
		filtered.HumanRoutePolicy = ""
	}
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
func buildAnswerRequirementsJSON(plan callbacks.ReplyTaskPlanTraceData, taskKey string, sourceMessageID int64, spanStart, spanEnd int) (string, error) {
	if len(plan.Requirements) == 0 {
		return "", nil
	}
	set := contracts.AnswerRequirementSetV1{
		SchemaVersion: contracts.AnswerRequirementSetV1SchemaVersion,
		TaskKey:       strings.TrimSpace(taskKey),
	}
	for index, encoded := range plan.Requirements {
		kind, required := decodeRequirementSeed(encoded)
		set.Requirements = append(set.Requirements, contracts.AnswerRequirementItemV1{
			Key: fmt.Sprintf("R%d", index+1), Kind: kind,
			SourceMsgID: sourceMessageID, SpanStart: spanStart, SpanEnd: spanEnd,
			Required: required, Sequence: index + 1,
		})
	}
	raw, err := contracts.MarshalAnswerRequirementSetV1(set)
	if err != nil {
		return "", fmt.Errorf("encode answer_requirement_set.v1: %w", err)
	}
	return string(raw), nil
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
