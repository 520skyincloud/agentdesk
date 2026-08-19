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
	"unicode"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/strictjson"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
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

type runtimeTaskSource struct {
	Message          models.Message
	Text             string
	AnalysisRevision int
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
			if runtimeTaskCoversMessage(task, req.UserMessage.ID) && aiReplyTurnTaskLedgerTerminal(task.Status) {
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
		inputs = resolveRuntimeTaskRelationTargetsDB(ctx.Tx, lockedTurn, sourceMessages, inputs)
		ensured, lockErr = services.AIReplyTurnTaskService.EnsureTasksDB(ctx.Tx, lockedTurn, inputs)
		if lockErr != nil {
			return lockErr
		}
		if relationErr := services.AIReplyTurnTaskService.SupersedePriorTasksForDialogueActDB(
			ctx.Tx, lockedTurn, intent.DialogueAct, ensured, time.Now(),
		); relationErr != nil {
			return relationErr
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

func runtimeTaskMessagesForPlans(req RunInput, plans []callbacks.ReplyTaskPlanTraceData) []models.Message {
	turn, messages, ok := runtimeTaskScope(req)
	if !ok || turn == nil || len(messages) == 0 || len(plans) == 0 {
		return nil
	}
	taskKeys := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		if key := strings.TrimSpace(plan.TaskKey); key != "" {
			taskKeys[key] = struct{}{}
		}
	}
	if len(taskKeys) == 0 {
		return nil
	}
	messageIDs := make(map[int64]struct{})
	for _, task := range repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(sqls.DB(), turn.TenantID, turn.ID) {
		if _, selected := taskKeys[task.TaskKey]; !selected {
			continue
		}
		for _, messageID := range taskBoundMessageIDs(task) {
			messageIDs[messageID] = struct{}{}
		}
	}
	ret := make([]models.Message, 0, len(messageIDs))
	for _, message := range messages {
		if _, selected := messageIDs[message.ID]; selected {
			ret = append(ret, message)
		}
	}
	return ret
}

func runtimeTaskSourcesCovered(messages []models.Message, tasks []models.AIReplyTurnTask) bool {
	if len(messages) == 0 || len(tasks) == 0 {
		return false
	}
	represented := make(map[int64]struct{}, len(tasks))
	for _, task := range tasks {
		for _, messageID := range taskBoundMessageIDs(task) {
			represented[messageID] = struct{}{}
		}
	}
	for _, message := range messages {
		if _, ok := represented[message.ID]; !ok {
			return false
		}
	}
	return true
}

func runtimeTaskCoversMessage(task models.AIReplyTurnTask, messageID int64) bool {
	if messageID <= 0 {
		return false
	}
	for _, boundMessageID := range taskBoundMessageIDs(task) {
		if boundMessageID == messageID {
			return true
		}
	}
	return false
}

func resolveRuntimeTaskRelationTargetsDB(
	db *gorm.DB,
	turn *models.AIReplyTurn,
	sourceMessages []models.Message,
	inputs []services.AIReplyTurnTaskInput,
) []services.AIReplyTurnTaskInput {
	if db == nil || turn == nil || len(inputs) == 0 {
		return inputs
	}
	existing := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(db, turn.TenantID, turn.ID)
	if len(existing) == 0 {
		return inputs
	}
	byID := make(map[int64]models.AIReplyTurnTask, len(existing))
	for _, task := range existing {
		byID[task.ID] = task
	}
	sources := make(map[int64]runtimeTaskSource, len(sourceMessages))
	for _, source := range buildRuntimeTaskSources(sourceMessages) {
		sources[source.Message.ID] = source
	}
	for index := range inputs {
		input := &inputs[index]
		switch strings.TrimSpace(input.RelationType) {
		case "correction", "cancellation":
		default:
			continue
		}
		contextMessageIDs := runtimeTaskInputContextMessageIDs(*input)
		if len(contextMessageIDs) == 0 {
			continue
		}
		candidates := make(map[int64]models.AIReplyTurnTask)
		for _, task := range existing {
			if task.IntroducedVersion >= turn.Version || !runtimeTaskBindsAnyMessage(task, contextMessageIDs) {
				continue
			}
			candidate := task
			if task.Status == enums.AIReplyTurnTaskStatusCovered && task.CoveredByTaskID > 0 {
				canonical, ok := byID[task.CoveredByTaskID]
				if !ok {
					continue
				}
				candidate = canonical
			}
			if !runtimeTaskRelationTargetable(candidate.Status) {
				continue
			}
			candidates[candidate.ID] = candidate
		}
		if len(candidates) == 1 {
			for taskID := range candidates {
				input.RelatedTaskID = taskID
			}
			continue
		}
		currentSource := sources[input.SourceMessageID]
		bestTaskID, bestScore, tied := int64(0), 0, false
		for taskID, candidate := range candidates {
			score := runtimeTaskRelationReferenceScore(currentSource.Text, runtimeTaskRelationSourceText(candidate, sources))
			switch {
			case score > bestScore:
				bestTaskID, bestScore, tied = taskID, score, false
			case score > 0 && score == bestScore:
				tied = true
			}
		}
		if bestScore > 0 && !tied {
			input.RelatedTaskID = bestTaskID
		}
	}
	return inputs
}

func runtimeTaskInputContextMessageIDs(input services.AIReplyTurnTaskInput) map[int64]struct{} {
	ret := make(map[int64]struct{}, 3)
	if strings.TrimSpace(input.SourceBindingsJSON) == "" {
		return ret
	}
	var bindings contracts.TaskSourceBindingsV1
	if err := json.Unmarshal([]byte(input.SourceBindingsJSON), &bindings); err != nil {
		return ret
	}
	primaryMessageID := bindings.PrimaryMessageID
	if primaryMessageID <= 0 {
		primaryMessageID = input.SourceMessageID
	}
	for _, binding := range bindings.Bindings {
		if binding.MessageID > 0 && binding.MessageID != primaryMessageID {
			ret[binding.MessageID] = struct{}{}
		}
	}
	return ret
}

func runtimeTaskBindsAnyMessage(task models.AIReplyTurnTask, messageIDs map[int64]struct{}) bool {
	for _, messageID := range taskBoundMessageIDs(task) {
		if _, ok := messageIDs[messageID]; ok {
			return true
		}
	}
	return false
}

func runtimeTaskRelationTargetable(status enums.AIReplyTurnTaskStatus) bool {
	switch status {
	case enums.AIReplyTurnTaskStatusPending, enums.AIReplyTurnTaskStatusReady,
		enums.AIReplyTurnTaskStatusRunning, enums.AIReplyTurnTaskStatusCommitted,
		enums.AIReplyTurnTaskStatusHandoffPending:
		return true
	default:
		return false
	}
}

func runtimeTaskRelationSourceText(task models.AIReplyTurnTask, sources map[int64]runtimeTaskSource) string {
	source, ok := sources[task.SourceMessageID]
	if !ok {
		return ""
	}
	runes := []rune(source.Text)
	if task.SourceSpanStart >= 0 && task.SourceSpanEnd > task.SourceSpanStart && task.SourceSpanEnd <= len(runes) {
		return string(runes[task.SourceSpanStart:task.SourceSpanEnd])
	}
	return source.Text
}

func runtimeTaskRelationReferenceScore(currentText, candidateText string) int {
	current := []rune(runtimeTaskRelationCoreText(currentText))
	candidate := []rune(runtimeTaskRelationCoreText(candidateText))
	if len(current) < 2 || len(candidate) < 2 {
		return 0
	}
	currentString, candidateString := string(current), string(candidate)
	if strings.Contains(currentString, candidateString) {
		return 100 + len(candidate)
	}
	previous := make([]int, len(candidate)+1)
	best := 0
	for _, currentRune := range current {
		next := make([]int, len(candidate)+1)
		for index, candidateRune := range candidate {
			if currentRune == candidateRune {
				next[index+1] = previous[index] + 1
				best = max(best, next[index+1])
			}
		}
		previous = next
	}
	if best < 2 {
		return 0
	}
	return best
}

func runtimeTaskRelationCoreText(text string) string {
	text = compactRuntimeProtocolText(text)
	for _, filler := range []string{
		"我想问的是", "我问的是", "问的是", "有没有", "能不能", "可不可以", "可以吗",
		"请问", "麻烦", "帮我", "一下", "怎么", "如何", "在哪里", "在哪儿", "在哪", "哪里",
		"多少", "什么", "这个", "那个", "吗", "呢", "啊", "呀", "吧", "了",
	} {
		text = strings.ReplaceAll(text, filler, "")
	}
	return text
}

func buildRuntimeTaskSources(messages []models.Message) []runtimeTaskSource {
	ret := make([]runtimeTaskSource, 0, len(messages))
	for index := range messages {
		message := messages[index]
		source := runtimeTaskSource{Message: message}
		switch message.MessageType {
		case enums.IMMessageTypeText, enums.IMMessageTypeHTML:
			source.Text = strings.TrimSpace(message.Content)
		default:
			analysis, err := services.MessageAnalysisService.ReadyForMessage(&message)
			if err != nil {
				slog.Warn("runtime task source analysis rejected",
					"tenant_id", message.TenantID,
					"conversation_id", message.ConversationID,
					"message_id", message.ID,
					"error", err,
				)
			} else if analysis != nil && analysis.Result != nil {
				source.Text = strings.TrimSpace(analysis.Result.NormalizedText)
				source.AnalysisRevision = analysis.SourceRevision
			}
			if source.Text == "" {
				mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
				if strings.TrimSpace(status) == "understood" {
					source.Text = strings.TrimSpace(mediaText)
					if source.Text == "" {
						source.Text = strings.TrimSpace(mediaSummary)
					}
				}
			}
		}
		if source.Text == "" {
			source.Text = strings.TrimSpace(currentTurnDisplayText(utils.BuildRuntimeMessageTextWithPayload(
				message.MessageType,
				message.Content,
				message.Payload,
			)))
		}
		ret = append(ret, source)
	}
	return ret
}

func buildRuntimeTaskInputs(plans []callbacks.ReplyTaskPlanTraceData, fallbackMessageID int64, sourceMessages []models.Message, tenantID, turnID int64) ([]services.AIReplyTurnTaskInput, map[string]callbacks.ReplyTaskPlanTraceData, error) {
	inputs := make([]services.AIReplyTurnTaskInput, 0, len(plans))
	plannedByKey := make(map[string]callbacks.ReplyTaskPlanTraceData, len(plans))
	sources := buildRuntimeTaskSources(sourceMessages)
	usedSourceMessageIDs := make(map[int64]struct{}, len(sources))
	plannedBySourceMessageID := make(map[int64][]callbacks.ReplyTaskPlanTraceData, len(sources))
	for index, plan := range plans {
		// 纯标点/空白消息：归一化后无实质文本，不创建无意义任务，直接跳过。
		if normalizeRuntimeTaskText(plan.Text) == "" {
			continue
		}
		source, boundSources, spanStart, spanEnd, preciseSpan := selectRuntimeTaskSources(plan, fallbackMessageID, sources, usedSourceMessageIDs)
		if source.Message.ID <= 0 || strings.TrimSpace(source.Text) == "" {
			return nil, nil, fmt.Errorf("AI reply task source message unavailable")
		}
		sourceEvidence, err := buildRuntimeTaskSourceEvidence(plan, source, boundSources, spanStart, spanEnd, preciseSpan)
		if err != nil {
			return nil, nil, err
		}
		for _, bound := range boundSources {
			usedSourceMessageIDs[bound.Message.ID] = struct{}{}
		}
		input := services.AIReplyTurnTaskInput{
			TenantID:              tenantID,
			TurnID:                turnID,
			SourceMessageID:       source.Message.ID,
			SequenceNo:            index + 1,
			TaskType:              runtimeTaskTypeForPlan(plan),
			Intent:                plan.Intent,
			SubIntent:             plan.SubIntent,
			RequestMode:           plan.RequestMode,
			RelationType:          plan.RelationType,
			ResourceAction:        plan.ResourceAction,
			QuestionText:          plan.Text,
			AnalysisRevision:      source.AnalysisRevision,
			SourceSpanStart:       spanStart,
			SourceSpanEnd:         spanEnd,
			SourceBindingsJSON:    sourceEvidence.BindingsJSON,
			SourceSetFingerprint:  sourceEvidence.SourceSetFingerprint,
			CanonicalQuestionHash: sourceEvidence.CanonicalQuestionHash,
			ReferenceFingerprint:  sourceEvidence.ReferenceFingerprint,
		}
		// 契约 10.8：把 Intent 建议的答案义务固化为 answer_requirement_set.v1。
		if requirementsJSON := buildAnswerRequirementsJSON(plan, spanStart, spanEnd); requirementsJSON != "" {
			input.AnswerRequirementsJSON = requirementsJSON
		}
		taskKey := services.AIReplyTurnTaskService.StableTaskKey(input)
		plan.TaskKey = taskKey
		inputs = append(inputs, input)
		plannedByKey[taskKey] = plan
		for _, bound := range boundSources {
			plannedBySourceMessageID[bound.Message.ID] = append(plannedBySourceMessageID[bound.Message.ID], plan)
		}
	}
	for _, source := range sources {
		if _, represented := usedSourceMessageIDs[source.Message.ID]; represented {
			continue
		}
		sourceText := strings.TrimSpace(source.Text)
		sourceFingerprint := normalizeRuntimeTaskText(sourceText)
		if sourceFingerprint == "" {
			continue
		}
		var duplicatePlans []callbacks.ReplyTaskPlanTraceData
		for _, candidate := range sources {
			if candidate.Message.ID == source.Message.ID || normalizeRuntimeTaskText(candidate.Text) != sourceFingerprint {
				continue
			}
			if planned := plannedBySourceMessageID[candidate.Message.ID]; len(planned) > 0 {
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
			spanStart, spanEnd, preciseSpan := runtimeTaskSpanWithinSource(questionText, sourceText)
			sourceEvidence, err := buildRuntimeTaskSourceEvidence(duplicatePlan, source, []runtimeTaskSource{source}, spanStart, spanEnd, preciseSpan)
			if err != nil {
				return nil, nil, err
			}
			input := services.AIReplyTurnTaskInput{
				TenantID:              tenantID,
				TurnID:                turnID,
				SourceMessageID:       source.Message.ID,
				SequenceNo:            len(inputs) + 1,
				TaskType:              runtimeTaskTypeForPlan(duplicatePlan),
				Intent:                duplicatePlan.Intent,
				SubIntent:             duplicatePlan.SubIntent,
				RequestMode:           duplicatePlan.RequestMode,
				RelationType:          duplicatePlan.RelationType,
				ResourceAction:        duplicatePlan.ResourceAction,
				QuestionText:          questionText,
				AnalysisRevision:      source.AnalysisRevision,
				SourceSpanStart:       spanStart,
				SourceSpanEnd:         spanEnd,
				SourceBindingsJSON:    sourceEvidence.BindingsJSON,
				SourceSetFingerprint:  sourceEvidence.SourceSetFingerprint,
				CanonicalQuestionHash: sourceEvidence.CanonicalQuestionHash,
				ReferenceFingerprint:  sourceEvidence.ReferenceFingerprint,
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
		usedSourceMessageIDs[source.Message.ID] = struct{}{}
		plannedBySourceMessageID[source.Message.ID] = duplicatedForSource
	}
	return inputs, plannedByKey, nil
}

type runtimeTaskSourceEvidence struct {
	BindingsJSON          string
	SourceSetFingerprint  string
	CanonicalQuestionHash string
	ReferenceFingerprint  string
}

func buildRuntimeTaskSourceEvidence(plan callbacks.ReplyTaskPlanTraceData, source runtimeTaskSource, boundSources []runtimeTaskSource, spanStart, spanEnd int, preciseSpan bool) (runtimeTaskSourceEvidence, error) {
	ret := runtimeTaskSourceEvidence{}
	sourceRunes := []rune(source.Text)
	if len(sourceRunes) == 0 {
		return ret, fmt.Errorf("AI reply task source text unavailable")
	}
	if spanStart < 0 || spanEnd <= spanStart || spanEnd > len(sourceRunes) {
		spanStart, spanEnd, preciseSpan = 0, len(sourceRunes), false
	}
	if len(boundSources) == 0 {
		boundSources = []runtimeTaskSource{source}
	}
	bindings := contracts.TaskSourceBindingsV1{SchemaVersion: contracts.TaskSourceBindingsV1SchemaVersion, PrimaryMessageID: source.Message.ID}
	seen := make(map[int64]struct{}, len(boundSources))
	for _, bound := range boundSources {
		if bound.Message.ID <= 0 {
			continue
		}
		if _, exists := seen[bound.Message.ID]; exists {
			continue
		}
		seen[bound.Message.ID] = struct{}{}
		start, end := 0, len([]rune(bound.Text))
		if bound.Message.ID == source.Message.ID {
			start, end = spanStart, spanEnd
		}
		if end <= start {
			continue
		}
		bindings.Bindings = append(bindings.Bindings, contracts.TaskSourceBindingItemV1{
			MessageID: bound.Message.ID, AnalysisRevision: bound.AnalysisRevision,
			Start: start, End: end, ObservationMessageIDs: []int64{},
		})
	}
	if len(bindings.Bindings) == 0 {
		return ret, fmt.Errorf("AI reply task source binding is empty")
	}
	raw, err := json.Marshal(bindings)
	if err != nil {
		return ret, err
	}
	if _, err := strictjson.DecodeObject[contracts.TaskSourceBindingsV1](raw, strictjson.DecodeOptions{
		MaxBytes: 8 * 1024,
		Schema:   contracts.MustSchema(contracts.SchemaTaskSourceBindingsV1),
	}); err != nil {
		return ret, fmt.Errorf("AI reply task source binding invalid: %w", err)
	}
	sourceSetSum := sha256.Sum256(raw)
	boundQuote := string(sourceRunes[spanStart:spanEnd])
	canonicalQuotes := []string{boundQuote}
	if !preciseSpan && normalizeRuntimeTaskText(plan.Text) != normalizeRuntimeTaskText(boundQuote) {
		// When a rewrite cannot be located exactly in the primary source, the whole
		// authoritative ASR remains the evidence boundary and normalized task text
		// only disambiguates multiple questions from the same long utterance.
		canonicalQuotes = append(canonicalQuotes, strings.TrimSpace(plan.Text))
	}
	ret.BindingsJSON = string(raw)
	ret.SourceSetFingerprint = hex.EncodeToString(sourceSetSum[:])
	ret.CanonicalQuestionHash = CanonicalQuestionHash(plan.Intent, plan.SubIntent, canonicalQuotes, plan.RequestMode)
	ret.ReferenceFingerprint = runtimeTaskReferenceFingerprint(plan, source, boundSources)
	return ret, nil
}

func selectRuntimeTaskSources(plan callbacks.ReplyTaskPlanTraceData, fallbackMessageID int64, sources []runtimeTaskSource, used map[int64]struct{}) (runtimeTaskSource, []runtimeTaskSource, int, int, bool) {
	if len(plan.SourceMessageIDs) > 0 {
		byID := make(map[int64]runtimeTaskSource, len(sources))
		for _, source := range sources {
			byID[source.Message.ID] = source
		}
		bound := make([]runtimeTaskSource, 0, len(plan.SourceMessageIDs))
		seen := make(map[int64]struct{}, len(plan.SourceMessageIDs))
		for _, messageID := range plan.SourceMessageIDs {
			source, ok := byID[messageID]
			if !ok || strings.TrimSpace(source.Text) == "" {
				continue
			}
			if _, exists := seen[messageID]; exists {
				continue
			}
			seen[messageID] = struct{}{}
			bound = append(bound, source)
		}
		if len(bound) > 0 {
			primary := bound[0]
			start, end, precise := runtimeTaskSpanWithinSource(plan.Text, primary.Text)
			return primary, bound, start, end, precise
		}
	}
	primary, start, end, precise := matchRuntimeTaskSourceWithSpan(plan, fallbackMessageID, sources, used)
	return primary, []runtimeTaskSource{primary}, start, end, precise
}

func runtimeTaskReferenceFingerprint(plan callbacks.ReplyTaskPlanTraceData, primary runtimeTaskSource, sources []runtimeTaskSource) string {
	anchorIDs := make([]string, 0, len(sources))
	primaryNeedsAnchor := primary.Message.MessageType == enums.IMMessageTypeImage ||
		primary.Message.MessageType == enums.IMMessageTypeVideo || primary.Message.MessageType == enums.IMMessageTypeGIF ||
		primary.Message.MessageType == enums.IMMessageTypeAttachment
	needsContextAnchor := runtimeTaskTextNeedsReference(plan.Text)
	for _, source := range sources {
		anchor := source.Message.ID == primary.Message.ID && primaryNeedsAnchor
		if source.Message.ID != primary.Message.ID && needsContextAnchor {
			anchor = true
		}
		if anchor {
			anchorIDs = append(anchorIDs, fmt.Sprintf("%d:%d", source.Message.ID, source.AnalysisRevision))
		}
	}
	if len(anchorIDs) == 0 {
		return ""
	}
	sort.Strings(anchorIDs)
	sum := sha256.Sum256([]byte(strings.Join(anchorIDs, ",")))
	return hex.EncodeToString(sum[:16])
}

func runtimeTaskTextNeedsReference(text string) bool {
	compact := compactRuntimeProtocolText(text)
	if compact == "" || len([]rune(compact)) > 32 {
		return false
	}
	return containsAny(compact, []string{"这个", "那个", "这张", "那张", "这个东西", "上面", "前面", "刚才", "它", "这里", "那里", "这是什么", "这是啥", "什么意思"})
}

func runtimeTaskSpanWithinSource(questionText, sourceText string) (int, int, bool) {
	sourceRunes := []rune(sourceText)
	if len(sourceRunes) == 0 || normalizeRuntimeTaskText(questionText) == "" {
		return 0, len(sourceRunes), false
	}
	questionRunes := []rune(strings.TrimSpace(questionText))
	if index := indexRuneSlice(sourceRunes, questionRunes); index >= 0 {
		return index, index + len(questionRunes), true
	}
	normalizedSource, sourceOffsets := normalizedRuntimeTaskRunes(sourceText)
	normalizedQuestion, _ := normalizedRuntimeTaskRunes(questionText)
	if index := indexRuneSlice(normalizedSource, normalizedQuestion); index >= 0 && len(normalizedQuestion) > 0 {
		start := sourceOffsets[index]
		end := sourceOffsets[index+len(normalizedQuestion)-1] + 1
		return start, end, true
	}
	return 0, len(sourceRunes), false
}

func matchRuntimeTaskSourceWithSpan(plan callbacks.ReplyTaskPlanTraceData, fallbackMessageID int64, sources []runtimeTaskSource, used map[int64]struct{}) (runtimeTaskSource, int, int, bool) {
	needle := normalizeRuntimeTaskText(plan.Text)
	if needle != "" {
		for _, source := range sources {
			candidate := normalizeRuntimeTaskText(source.Text)
			if _, alreadyUsed := used[source.Message.ID]; alreadyUsed || candidate == "" {
				continue
			}
			if candidate == needle {
				return source, 0, len([]rune(source.Text)), true
			}
		}
		for _, source := range sources {
			if normalizeRuntimeTaskText(source.Text) == needle {
				return source, 0, len([]rune(source.Text)), true
			}
		}
		for _, source := range sources {
			if _, alreadyUsed := used[source.Message.ID]; alreadyUsed {
				continue
			}
			if start, end, precise := runtimeTaskSpanWithinSource(plan.Text, source.Text); precise {
				return source, start, end, true
			}
		}
		for _, source := range sources {
			if start, end, precise := runtimeTaskSpanWithinSource(plan.Text, source.Text); precise {
				return source, start, end, true
			}
		}
	}
	for _, source := range sources {
		if source.Message.ID == fallbackMessageID {
			return source, 0, len([]rune(source.Text)), false
		}
	}
	if len(sources) > 0 {
		source := sources[len(sources)-1]
		return source, 0, len([]rune(source.Text)), false
	}
	return runtimeTaskSource{Message: models.Message{ID: fallbackMessageID}}, 0, 0, false
}

func matchRuntimeTaskSourceMessageWithSpan(plan callbacks.ReplyTaskPlanTraceData, fallbackMessageID int64, messages []models.Message, used map[int64]struct{}) (int64, int, int) {
	source, start, end, _ := matchRuntimeTaskSourceWithSpan(plan, fallbackMessageID, buildRuntimeTaskSources(messages), used)
	return source.Message.ID, start, end
}

func matchRuntimeTaskSourceMessage(plan callbacks.ReplyTaskPlanTraceData, fallbackMessageID int64, messages []models.Message, used map[int64]struct{}) int64 {
	source, _, _, _ := matchRuntimeTaskSourceWithSpan(plan, fallbackMessageID, buildRuntimeTaskSources(messages), used)
	return source.Message.ID
}

func normalizedRuntimeTaskRunes(value string) ([]rune, []int) {
	runes := []rune(strings.TrimSpace(currentTurnDisplayText(value)))
	normalized := make([]rune, 0, len(runes))
	offsets := make([]int, 0, len(runes))
	for index, current := range runes {
		current = unicode.ToLower(current)
		if runtimeTaskIgnoredRune(current) {
			continue
		}
		normalized = append(normalized, current)
		offsets = append(offsets, index)
	}
	return normalized, offsets
}

func runtimeTaskIgnoredRune(value rune) bool {
	switch value {
	case ' ', '\t', '\r', '\n', '，', '。', '！', '!', '？', '?', '：', ':', '；', ';':
		return true
	default:
		return false
	}
}

func indexRuneSlice(haystack, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for index := 0; index <= len(haystack)-len(needle); index++ {
		matched := true
		for offset := range needle {
			if haystack[index+offset] != needle[offset] {
				matched = false
				break
			}
		}
		if matched {
			return index
		}
	}
	return -1
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
	for _, source := range buildRuntimeTaskSources(messages) {
		if source.Message.ID == task.SourceMessageID {
			text = strings.TrimSpace(source.Text)
			runes := []rune(text)
			if task.SourceSpanStart >= 0 && task.SourceSpanEnd > task.SourceSpanStart && task.SourceSpanEnd <= len(runes) {
				text = strings.TrimSpace(string(runes[task.SourceSpanStart:task.SourceSpanEnd]))
			}
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
		SourceMessageIDs: taskBoundMessageIDs(task),
	}
}

func intentFromReplyTaskPlans(plans []callbacks.ReplyTaskPlanTraceData, reason string) callbacks.IntentTraceData {
	intent := callbacks.IntentTraceData{ShouldReply: len(plans) > 0, MatchMode: "task_ledger", Reason: reason}
	for _, plan := range plans {
		item := callbacks.IntentTaskTraceData{
			Sequence: plan.Sequence, Intent: plan.Intent, SubIntent: plan.SubIntent, Text: plan.Text,
			RequestMode: plan.RequestMode, ResourceAction: plan.ResourceAction,
			SourceRefs: append([]string(nil), plan.SourceRefs...), SourceMessageIDs: append([]int64(nil), plan.SourceMessageIDs...),
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
