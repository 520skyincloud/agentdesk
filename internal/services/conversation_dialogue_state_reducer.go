package services

import (
	"sort"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

type DialogueStateEventKind string

const (
	DialogueStateEventCustomerCommitted  DialogueStateEventKind = "customer_committed"
	DialogueStateEventIntentCompleted    DialogueStateEventKind = "intent_completed"
	DialogueStateEventTasksChanged       DialogueStateEventKind = "tasks_changed"
	DialogueStateEventAssistantCommitted DialogueStateEventKind = "assistant_committed"
	DialogueStateEventRouteChanged       DialogueStateEventKind = "route_changed"
	DialogueStateEventResumeChanged      DialogueStateEventKind = "resume_changed"
)

type DialogueStateEvent struct {
	Kind             DialogueStateEventKind
	MessageID        int64
	TurnID           int64
	TurnVersion      int
	DialogueAct      string
	Topic            string
	ActiveTaskKeys   []string
	Tasks            []models.AIReplyTurnTask
	Actions          []models.AIReplyTurnAction
	ResolvedTaskKeys []string
	AssistantMessage *models.Message
	ConversationMode string
	SessionFacts     []contracts.DialogueStateSessionFact
	Now              time.Time
}

func ReduceDialogueState(current contracts.DialogueStateSnapshotV1, event DialogueStateEvent) contracts.DialogueStateSnapshotV1 {
	if dialogueStateEventIsStale(current, event) {
		return current
	}
	lateForFocus := dialogueStateEventIsLateForFocus(current, event)
	startsNewTurn := dialogueStateEventStartsNewTurn(current, event)
	if event.Now.IsZero() {
		event.Now = time.Now().UTC()
	}
	if current.UpdatedAt.After(event.Now) {
		event.Now = current.UpdatedAt
	}
	if event.MessageID > current.BasedOnMessageID {
		current.BasedOnMessageID = event.MessageID
	}
	if event.Kind != DialogueStateEventCustomerCommitted && !lateForFocus {
		if startsNewTurn {
			// Open tasks belong to one turn. Rebuild them from the authoritative
			// task ledger carried by the new turn event instead of leaking stale
			// tasks into the next customer question.
			// 文档 §6.8：集合字段必须输出空数组，不输出 null。
			current.OpenTasks = []contracts.DialogueStateOpenTask{}
			current.Focus.ActiveTaskKeys = []string{}
		}
		advanceDialogueTurnEvidence(&current, event)
	}
	if event.Kind == DialogueStateEventCustomerCommitted {
		boundDialogueState(&current, event.Now)
		current.SchemaVersion = contracts.DialogueStateSnapshotV1SchemaVersion
		current.UpdatedAt = event.Now.UTC()
		normalizeDialogueStateArrays(&current)
		return current
	}
	if !lateForFocus {
		if mode := normalizeDialogueConversationMode(event.ConversationMode); mode != "" {
			current.ConversationMode = mode
		}
		if relation := normalizeDialogueStateRelation(event.DialogueAct); relation != "" {
			current.Focus.RelationToPrior = relation
		}
		if topic := boundedDialogueText(event.Topic, 120); topic != "" {
			current.Focus.Topic = topic
		}
	}
	if len(event.Tasks) > 0 {
		applyDialogueTasks(&current, event.Tasks, event.Now)
	}
	if len(event.Actions) > 0 {
		applyDialogueActions(&current, event.Actions)
	}
	if len(event.ResolvedTaskKeys) > 0 {
		tasksByKey := make(map[string]models.AIReplyTurnTask, len(event.Tasks))
		for _, task := range event.Tasks {
			if task.TaskKey != "" {
				tasksByKey[task.TaskKey] = task
			}
		}
		for _, taskKey := range event.ResolvedTaskKeys {
			if task, ok := tasksByKey[taskKey]; ok {
				if outcome, terminal := dialogueTaskOutcome(task); terminal {
					resolveDialogueTask(&current, task.TaskKey, outcome, assistantMessageID(event.AssistantMessage), event.Now)
				}
				continue
			}
			// Preserve the legacy event-only path when no task ledger is
			// available. A task ledger, when present, is authoritative.
			resolveDialogueTask(&current, taskKey, string(enums.AIReplyTurnTaskBusinessOutcomeAnswered), assistantMessageID(event.AssistantMessage), event.Now)
		}
	}
	if !lateForFocus && event.AssistantMessage != nil && event.AssistantMessage.ID > 0 {
		senderType := "ai"
		if event.AssistantMessage.SenderType == enums.IMSenderTypeAgent {
			senderType = "agent"
		}
		current.LastAssistant = &contracts.DialogueStateLastAssistant{
			MessageID: event.AssistantMessage.ID, SenderType: senderType,
			TaskKeys: uniqueBoundedStrings(event.ResolvedTaskKeys, 12, 128),
		}
	}
	if !lateForFocus && len(event.SessionFacts) > 0 {
		current.SessionFacts = mergeDialogueFacts(current.SessionFacts, event.SessionFacts, event.Now)
	}
	boundDialogueState(&current, event.Now)
	current.SchemaVersion = contracts.DialogueStateSnapshotV1SchemaVersion
	current.UpdatedAt = event.Now.UTC()
	return current
}

func dialogueStateEventStartsNewTurn(current contracts.DialogueStateSnapshotV1, event DialogueStateEvent) bool {
	if event.Kind == DialogueStateEventCustomerCommitted || event.TurnID <= 0 {
		return false
	}
	if current.BasedOnTurnID <= 0 {
		return true
	}
	return event.TurnID > current.BasedOnTurnID
}

func dialogueStateEventIsStale(current contracts.DialogueStateSnapshotV1, event DialogueStateEvent) bool {
	if event.Kind == DialogueStateEventCustomerCommitted {
		return event.MessageID > 0 && current.BasedOnMessageID > 0 && event.MessageID <= current.BasedOnMessageID
	}
	// Task and assistant terminal events are commutative ledger updates. They
	// must still close old open tasks after a newer customer message arrives.
	// Only semantic intent events are dropped wholesale when their focus is old.
	return event.Kind == DialogueStateEventIntentCompleted && dialogueStateEventIsLateForFocus(current, event)
}

func dialogueStateEventIsLateForFocus(current contracts.DialogueStateSnapshotV1, event DialogueStateEvent) bool {
	if event.MessageID > 0 && current.BasedOnMessageID > 0 && event.MessageID < current.BasedOnMessageID {
		return true
	}
	if event.TurnID <= 0 || current.BasedOnTurnID <= 0 {
		return false
	}
	if event.TurnID != current.BasedOnTurnID {
		return event.TurnID < current.BasedOnTurnID
	}
	return event.TurnVersion > 0 && current.BasedOnTurnVersion > 0 && event.TurnVersion < current.BasedOnTurnVersion
}

func advanceDialogueTurnEvidence(state *contracts.DialogueStateSnapshotV1, event DialogueStateEvent) {
	if state == nil || event.TurnID <= 0 {
		return
	}
	if event.TurnID > state.BasedOnTurnID {
		state.BasedOnTurnID = event.TurnID
		state.BasedOnTurnVersion = max(event.TurnVersion, 0)
		return
	}
	if event.TurnID == state.BasedOnTurnID && event.TurnVersion > state.BasedOnTurnVersion {
		state.BasedOnTurnVersion = event.TurnVersion
	}
}

func applyDialogueTasks(state *contracts.DialogueStateSnapshotV1, tasks []models.AIReplyTurnTask, now time.Time) {
	for _, task := range tasks {
		if task.TaskKey == "" {
			continue
		}
		if outcome, terminal := dialogueTaskOutcome(task); terminal {
			resolveDialogueTask(state, task.TaskKey, outcome, task.CommittedMessageID, now)
			continue
		}
		open := contracts.DialogueStateOpenTask{
			TaskKey: task.TaskKey, Intent: boundedDialogueText(task.Intent, 80), SubIntent: boundedDialogueText(task.SubIntent, 120),
			State: dialogueOpenTaskState(task), MissingField: nil,
		}
		upsertDialogueOpenTask(state, open)
	}
}

func applyDialogueActions(state *contracts.DialogueStateSnapshotV1, actions []models.AIReplyTurnAction) {
	for _, action := range actions {
		if action.TaskKey == "" {
			continue
		}
		for i := range state.OpenTasks {
			if state.OpenTasks[i].TaskKey != action.TaskKey {
				continue
			}
			switch action.Status {
			case "requested", "prepared", "committed", "delivery_failed":
				state.OpenTasks[i].State = "awaiting_action"
			case "delivered":
				state.OpenTasks[i].State = "ready_to_generate"
			}
		}
	}
}

func resolveDialogueTask(state *contracts.DialogueStateSnapshotV1, taskKey, outcome string, answerMessageID int64, now time.Time) {
	taskKey = strings.TrimSpace(taskKey)
	if taskKey == "" {
		return
	}
	filtered := state.OpenTasks[:0]
	for _, open := range state.OpenTasks {
		if open.TaskKey != taskKey {
			filtered = append(filtered, open)
		}
	}
	state.OpenTasks = filtered
	active := state.Focus.ActiveTaskKeys[:0]
	for _, activeTaskKey := range state.Focus.ActiveTaskKeys {
		if activeTaskKey != taskKey {
			active = append(active, activeTaskKey)
		}
	}
	state.Focus.ActiveTaskKeys = active
	resolved := contracts.DialogueStateResolvedTask{TaskKey: taskKey, Outcome: outcome, AnswerMessageID: max(answerMessageID, 0), ResolvedAt: now.UTC()}
	for i := range state.ResolvedTasks {
		if state.ResolvedTasks[i].TaskKey == taskKey {
			state.ResolvedTasks[i] = resolved
			return
		}
	}
	state.ResolvedTasks = append(state.ResolvedTasks, resolved)
}

func upsertDialogueOpenTask(state *contracts.DialogueStateSnapshotV1, task contracts.DialogueStateOpenTask) {
	for i := range state.OpenTasks {
		if state.OpenTasks[i].TaskKey == task.TaskKey {
			state.OpenTasks[i] = task
			return
		}
	}
	state.OpenTasks = append(state.OpenTasks, task)
}

func dialogueTaskOutcome(task models.AIReplyTurnTask) (string, bool) {
	outcome := enums.ClassifyAIReplyTurnTaskOutcome(task.Status, task.TaskType, task.KnowledgeStatus, task.ResultCode)
	if !outcome.Terminal || outcome.Business == "" {
		return "", false
	}
	return string(outcome.Business), true
}

func dialogueOpenTaskState(task models.AIReplyTurnTask) string {
	switch task.Status {
	case enums.AIReplyTurnTaskStatusHandoffPending:
		return "awaiting_human"
	case enums.AIReplyTurnTaskStatusWaitingCoverage:
		return "awaiting_action"
	}
	switch task.KnowledgeStatus {
	case enums.AIReplyTurnTaskKnowledgeStatusPending:
		return "awaiting_knowledge"
	case enums.AIReplyTurnTaskKnowledgeStatusNoHit, enums.AIReplyTurnTaskKnowledgeStatusNoContext:
		return "awaiting_customer"
	}
	if task.TaskType == enums.AIReplyTurnTaskTypeResource || task.TaskType == enums.AIReplyTurnTaskTypeHuman {
		return "awaiting_action"
	}
	return "ready_to_generate"
}

func mergeDialogueFacts(existing, incoming []contracts.DialogueStateSessionFact, now time.Time) []contracts.DialogueStateSessionFact {
	byKey := make(map[string]contracts.DialogueStateSessionFact)
	order := make([]string, 0, len(existing)+len(incoming))
	appendFact := func(fact contracts.DialogueStateSessionFact) {
		fact.Key = boundedDialogueText(fact.Key, 80)
		fact.Value = boundedDialogueText(fact.Value, 300)
		if fact.Key == "" || fact.Value == "" || fact.ExpiresAt != nil && fact.ExpiresAt.Before(now) {
			return
		}
		if _, ok := byKey[fact.Key]; !ok {
			order = append(order, fact.Key)
		}
		byKey[fact.Key] = fact
	}
	for _, fact := range existing {
		appendFact(fact)
	}
	for _, fact := range incoming {
		appendFact(fact)
	}
	ret := make([]contracts.DialogueStateSessionFact, 0, len(order))
	for _, key := range order {
		ret = append(ret, byKey[key])
	}
	if len(ret) > 24 {
		ret = ret[len(ret)-24:]
	}
	return ret
}

func boundDialogueState(state *contracts.DialogueStateSnapshotV1, now time.Time) {
	state.Focus.Topic = boundedDialogueText(state.Focus.Topic, 120)
	state.Focus.RelationToPrior = normalizeDialogueStateRelation(state.Focus.RelationToPrior)
	if state.Focus.RelationToPrior == "" {
		state.Focus.RelationToPrior = "unknown"
	}
	if len(state.OpenTasks) > 12 {
		state.OpenTasks = state.OpenTasks[len(state.OpenTasks)-12:]
	}
	activeTaskKeys := make([]string, 0, len(state.OpenTasks))
	for _, task := range state.OpenTasks {
		activeTaskKeys = append(activeTaskKeys, task.TaskKey)
	}
	state.Focus.ActiveTaskKeys = uniqueBoundedStrings(activeTaskKeys, 12, 128)
	sort.SliceStable(state.ResolvedTasks, func(i, j int) bool {
		return state.ResolvedTasks[i].ResolvedAt.Before(state.ResolvedTasks[j].ResolvedAt)
	})
	if len(state.ResolvedTasks) > 20 {
		state.ResolvedTasks = state.ResolvedTasks[len(state.ResolvedTasks)-20:]
	}
	state.SessionFacts = mergeDialogueFacts(nil, state.SessionFacts, now)
}

func normalizeDialogueConversationMode(value string) string {
	switch strings.TrimSpace(value) {
	case "ai_serving", "human_pending", "human_serving", "resume_pending", "closed":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func normalizeDialogueStateRelation(value string) string {
	switch strings.TrimSpace(value) {
	case "new_topic", "follow_up", "repeat", "correction", "confirmation", "cancellation", "unknown":
		return strings.TrimSpace(value)
	case "refinement":
		return "follow_up"
	default:
		return ""
	}

}

func uniqueBoundedStrings(items []string, limit, runeLimit int) []string {
	ret := make([]string, 0, min(len(items), limit))
	seen := make(map[string]struct{})
	for _, item := range items {
		item = boundedDialogueText(item, runeLimit)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		ret = append(ret, item)
		if len(ret) >= limit {
			break
		}
	}
	return ret
}

func boundedDialogueText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if limit > 0 && len(runes) > limit {
		runes = runes[:limit]
	}
	return strings.TrimSpace(string(runes))
}

func assistantMessageID(message *models.Message) int64 {
	if message == nil {
		return 0
	}
	return message.ID
}

// normalizeDialogueStateArrays 文档 §6.8：序列化前归一化所有集合为空数组。
func normalizeDialogueStateArrays(state *contracts.DialogueStateSnapshotV1) {
	if state.OpenTasks == nil {
		state.OpenTasks = []contracts.DialogueStateOpenTask{}
	}
	if state.ResolvedTasks == nil {
		state.ResolvedTasks = []contracts.DialogueStateResolvedTask{}
	}
	if state.SessionFacts == nil {
		state.SessionFacts = []contracts.DialogueStateSessionFact{}
	}
	if state.Focus.ActiveTaskKeys == nil {
		state.Focus.ActiveTaskKeys = []string{}
	}
}
