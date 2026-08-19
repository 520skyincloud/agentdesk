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
		source, spanStart, spanEnd, preciseSpan := matchRuntimeTaskSourceWithSpan(plan, fallbackMessageID, sources, usedSourceMessageIDs)
		if source.Message.ID <= 0 || strings.TrimSpace(source.Text) == "" {
			return nil, nil, fmt.Errorf("AI reply task source message unavailable")
		}
		sourceEvidence, err := buildRuntimeTaskSourceEvidence(plan, source, spanStart, spanEnd, preciseSpan)
		if err != nil {
			return nil, nil, err
		}
		usedSourceMessageIDs[source.Message.ID] = struct{}{}
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
		}
		// 契约 10.8：把 Intent 建议的答案义务固化为 answer_requirement_set.v1。
		if requirementsJSON := buildAnswerRequirementsJSON(plan, spanStart, spanEnd); requirementsJSON != "" {
			input.AnswerRequirementsJSON = requirementsJSON
		}
		taskKey := services.AIReplyTurnTaskService.StableTaskKey(input)
		plan.TaskKey = taskKey
		inputs = append(inputs, input)
		plannedByKey[taskKey] = plan
		plannedBySourceMessageID[source.Message.ID] = append(plannedBySourceMessageID[source.Message.ID], plan)
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
			sourceEvidence, err := buildRuntimeTaskSourceEvidence(duplicatePlan, source, spanStart, spanEnd, preciseSpan)
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
}

func buildRuntimeTaskSourceEvidence(plan callbacks.ReplyTaskPlanTraceData, source runtimeTaskSource, spanStart, spanEnd int, preciseSpan bool) (runtimeTaskSourceEvidence, error) {
	ret := runtimeTaskSourceEvidence{}
	sourceRunes := []rune(source.Text)
	if len(sourceRunes) == 0 {
		return ret, fmt.Errorf("AI reply task source text unavailable")
	}
	if spanStart < 0 || spanEnd <= spanStart || spanEnd > len(sourceRunes) {
		spanStart, spanEnd, preciseSpan = 0, len(sourceRunes), false
	}
	bindings := contracts.TaskSourceBindingsV1{
		SchemaVersion:    contracts.TaskSourceBindingsV1SchemaVersion,
		PrimaryMessageID: source.Message.ID,
		Bindings: []contracts.TaskSourceBindingItemV1{{
			MessageID:             source.Message.ID,
			AnalysisRevision:      source.AnalysisRevision,
			Start:                 spanStart,
			End:                   spanEnd,
			ObservationMessageIDs: []int64{},
		}},
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
		// Stable V2 only returns task.text, not model-provided source refs. When a
		// rewrite cannot be located exactly, the whole authoritative ASR remains
		// the evidence boundary and the normalized task text only disambiguates
		// multiple questions from the same long utterance inside the hash.
		canonicalQuotes = append(canonicalQuotes, strings.TrimSpace(plan.Text))
	}
	ret.BindingsJSON = string(raw)
	ret.SourceSetFingerprint = hex.EncodeToString(sourceSetSum[:])
	ret.CanonicalQuestionHash = CanonicalQuestionHash(plan.Intent, plan.SubIntent, canonicalQuotes, plan.RequestMode)
	return ret, nil
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
