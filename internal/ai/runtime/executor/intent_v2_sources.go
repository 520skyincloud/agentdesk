package executor

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"agent-desk/internal/ai/replyengine"
	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/strictjson"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

type intentV2SourceScope struct {
	Envelope     contextcompiler.TurnInputEnvelope
	Messages     []models.Message
	RequiredRefs map[string]struct{}
}

func buildIntentV2SourceScope(req RunInput, historyMessages []models.Message) intentV2SourceScope {
	messages := intentV2TurnMessages(req, historyMessages)
	requiredIDs := make(map[int64]struct{}, len(messages))
	if turn, turnMessages, ok := runtimeTaskScope(req); ok && len(turnMessages) > 0 {
		messages = turnMessages
		represented := representedTaskSourceMessageIDs(turn)
		for _, message := range messages {
			if _, covered := represented[message.ID]; !covered {
				requiredIDs[message.ID] = struct{}{}
			}
		}
		if len(requiredIDs) > 0 {
			messages = withIntentV2AdjacentContext(messages, requiredIDs, 2)
		}
	}
	if len(requiredIDs) == 0 {
		for _, message := range messages {
			requiredIDs[message.ID] = struct{}{}
		}
	}
	scope := contextcompiler.EnvelopeScope{
		TenantID: req.Conversation.TenantID, StoreID: req.Conversation.StoreID,
		ConversationID: req.Conversation.ID, SessionNo: req.UserMessage.SessionNo,
		TurnID: req.UserMessage.AIReplyTurnID, TurnVersion: req.UserMessage.AIReplyTurnVersion,
	}
	envelope := contextcompiler.BuildTurnInputEnvelope(scope, messages)
	observationByRef := intentV2ObservationByRef(envelope)
	requiredRefs := make(map[string]struct{}, len(requiredIDs))
	for _, utterance := range envelope.Utterances {
		if _, required := requiredIDs[utterance.MessageID]; required && intentV2UtteranceUsable(utterance, observationByRef) {
			requiredRefs[utterance.Ref] = struct{}{}
		}
	}
	return intentV2SourceScope{Envelope: envelope, Messages: messages, RequiredRefs: requiredRefs}
}

func intentV2TurnMessages(req RunInput, historyMessages []models.Message) []models.Message {
	messages := make([]models.Message, 0, 6)
	for index := len(historyMessages) - 1; index >= 0; index-- {
		item := historyMessages[index]
		if item.ConversationID != req.Conversation.ID || item.SessionNo != req.UserMessage.SessionNo || item.SenderType != enums.IMSenderTypeCustomer {
			break
		}
		messages = append([]models.Message{item}, messages...)
		if len(messages) >= 11 {
			break
		}
	}
	seen := make(map[int64]struct{}, len(messages)+1)
	deduped := make([]models.Message, 0, len(messages)+1)
	for _, message := range append(messages, req.UserMessage) {
		if message.ID > 0 {
			if _, exists := seen[message.ID]; exists {
				continue
			}
			seen[message.ID] = struct{}{}
		}
		deduped = append(deduped, message)
	}
	return deduped
}

func representedTaskSourceMessageIDs(turn *models.AIReplyTurn) map[int64]struct{} {
	represented := make(map[int64]struct{})
	if turn == nil || turn.ID <= 0 || turn.TenantID <= 0 {
		return represented
	}
	tasks := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(sqls.DB(), turn.TenantID, turn.ID)
	for _, task := range tasks {
		if task.Status == enums.AIReplyTurnTaskStatusFailed || task.Status == enums.AIReplyTurnTaskStatusSuperseded {
			continue
		}
		for _, messageID := range taskBoundMessageIDs(task) {
			represented[messageID] = struct{}{}
		}
	}
	return represented
}

func withIntentV2AdjacentContext(messages []models.Message, required map[int64]struct{}, contextLimit int) []models.Message {
	include := make(map[int64]struct{}, len(required)+contextLimit)
	for id := range required {
		include[id] = struct{}{}
	}
	remaining := contextLimit
	for index := len(messages) - 1; index >= 0 && remaining > 0; index-- {
		if _, requiredMessage := required[messages[index].ID]; !requiredMessage {
			continue
		}
		for previous := index - 1; previous >= 0 && remaining > 0; previous-- {
			id := messages[previous].ID
			if _, exists := include[id]; exists {
				continue
			}
			include[id] = struct{}{}
			remaining--
		}
	}
	ret := make([]models.Message, 0, len(include))
	for _, message := range messages {
		if _, ok := include[message.ID]; ok {
			ret = append(ret, message)
		}
	}
	return ret
}

func renderIntentV2SourceMessage(req RunInput, scope intentV2SourceScope) models.Message {
	message := req.UserMessage
	message.MessageType = enums.IMMessageTypeText
	message.Payload = ""
	message.Content = scope.Envelope.RenderEnvelopeJSON() + "\n必须覆盖的当前输入：" + strings.Join(sortedIntentV2Refs(scope.RequiredRefs), ",")
	return message
}

func filterIntentV2History(items []models.Message, current []models.Message) []models.Message {
	ids := make(map[int64]struct{}, len(current))
	for _, message := range current {
		ids[message.ID] = struct{}{}
	}
	ret := make([]models.Message, 0, len(items))
	for _, item := range items {
		if _, currentMessage := ids[item.ID]; !currentMessage {
			ret = append(ret, item)
		}
	}
	return ret
}

func resolveIntentV2TaskSources(parsed *contracts.IntentTasksV2, scope intentV2SourceScope) error {
	if parsed == nil {
		return fmt.Errorf("intent tasks are required")
	}
	utteranceByRef := make(map[string]contextcompiler.EnvelopeUtterance, len(scope.Envelope.Utterances))
	utteranceOrder := make(map[string]int, len(scope.Envelope.Utterances))
	for _, utterance := range scope.Envelope.Utterances {
		utteranceByRef[utterance.Ref] = utterance
		utteranceOrder[utterance.Ref] = len(utteranceOrder)
	}
	observationByRef := intentV2ObservationByRef(scope.Envelope)
	coveredRequired := make(map[string]struct{}, len(scope.RequiredRefs))
	requiredRefs := sortedIntentV2Refs(scope.RequiredRefs)
	for index := range parsed.Tasks {
		task := &parsed.Tasks[index]
		task.SourceRefs = uniqueIntentV2Refs(task.SourceRefs)
		if len(task.SourceRefs) == 0 && len(requiredRefs) == 1 {
			task.SourceRefs = []string{requiredRefs[0]}
		}
		if len(task.SourceRefs) == 0 {
			return intentV2SourceProtocolError(index, "source_refs_missing", "multiple current messages require explicit sourceRefs")
		}
		task.SourceRefs = intentV2PrimaryRefFirst(task.Text, task.SourceRefs, utteranceByRef)
		task.SourceMessageIDs = task.SourceMessageIDs[:0]
		for _, ref := range task.SourceRefs {
			utterance, ok := utteranceByRef[ref]
			if !ok || utterance.MessageID <= 0 {
				return intentV2SourceProtocolError(index, "source_ref_unknown", "sourceRefs contains an unknown current-turn ref")
			}
			task.SourceMessageIDs = append(task.SourceMessageIDs, utterance.MessageID)
			if _, required := scope.RequiredRefs[ref]; required {
				coveredRequired[ref] = struct{}{}
			}
		}
		if (parsed.DialogueAct == "correction" || parsed.DialogueAct == "cancellation") &&
			!intentV2HasUsablePriorContext(task.SourceRefs, utteranceByRef, utteranceOrder, observationByRef) {
			return intentV2SourceProtocolError(index, "source_context_ref_missing", "correction or cancellation requires a usable prior context URef after the primary source")
		}
	}
	normalizeIntentV2ReadyMediaFollowUps(parsed, utteranceByRef, observationByRef)
	missing := make([]string, 0)
	for ref := range scope.RequiredRefs {
		if _, covered := coveredRequired[ref]; !covered {
			missing = append(missing, ref)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return &strictjson.ProtocolError{
			Code: strictjson.ErrorJSONBusinessInvariant, Path: "$.tasks[*].sourceRefs",
			Message: "required current inputs are not covered: " + strings.Join(missing, ","),
		}
	}
	return nil
}

func normalizeIntentV2ReadyMediaFollowUps(
	parsed *contracts.IntentTasksV2,
	utteranceByRef map[string]contextcompiler.EnvelopeUtterance,
	observationByRef map[string]contextcompiler.EnvelopeObservation,
) {
	if parsed == nil {
		return
	}
	for index := range parsed.Tasks {
		task := &parsed.Tasks[index]
		if strings.TrimSpace(task.Intent) != "interaction" ||
			!intentV2TaskLooksLikeMediaFollowUp(*task, utteranceByRef) ||
			!intentV2TaskHasReadyMediaObservation(task.SourceRefs, utteranceByRef, observationByRef) {
			continue
		}
		task.SubIntent = "media_context_follow_up"
		task.RequestMode = "answer"
		if parsed.DialogueAct == "unknown" || parsed.DialogueAct == "new_topic" {
			parsed.DialogueAct = "follow_up"
		}
	}
}

func intentV2TaskLooksLikeMediaFollowUp(task contracts.IntentTaskV2, utteranceByRef map[string]contextcompiler.EnvelopeUtterance) bool {
	if replyengine.LooksLikeMediaFollowUp(task.Text) {
		return true
	}
	if len(task.SourceRefs) == 0 {
		return false
	}
	return replyengine.LooksLikeMediaFollowUp(utteranceByRef[task.SourceRefs[0]].Text)
}

func intentV2TaskHasReadyMediaObservation(
	refs []string,
	utteranceByRef map[string]contextcompiler.EnvelopeUtterance,
	observationByRef map[string]contextcompiler.EnvelopeObservation,
) bool {
	for _, ref := range refs {
		utterance, ok := utteranceByRef[ref]
		if !ok {
			continue
		}
		for _, observationRef := range utterance.ObservationRefs {
			observation, ok := observationByRef[observationRef]
			if !ok || observation.Status != "ready" || strings.TrimSpace(observation.Text) == "" {
				continue
			}
			switch observation.SourceType {
			case "image", "video", "gif", "attachment":
				return true
			}
		}
	}
	return false
}

func intentV2ObservationByRef(envelope contextcompiler.TurnInputEnvelope) map[string]contextcompiler.EnvelopeObservation {
	ret := make(map[string]contextcompiler.EnvelopeObservation, len(envelope.Observations))
	for _, observation := range envelope.Observations {
		ret[observation.Ref] = observation
	}
	return ret
}

func intentV2UtteranceUsable(utterance contextcompiler.EnvelopeUtterance, observationByRef map[string]contextcompiler.EnvelopeObservation) bool {
	if strings.TrimSpace(utterance.Text) != "" {
		return true
	}
	for _, ref := range utterance.ObservationRefs {
		observation, ok := observationByRef[ref]
		if ok && observation.Status == "ready" && strings.TrimSpace(observation.Text) != "" {
			return true
		}
	}
	return false
}

func intentV2HasUsablePriorContext(
	refs []string,
	utteranceByRef map[string]contextcompiler.EnvelopeUtterance,
	utteranceOrder map[string]int,
	observationByRef map[string]contextcompiler.EnvelopeObservation,
) bool {
	if len(refs) < 2 {
		return false
	}
	primaryOrder, ok := utteranceOrder[refs[0]]
	if !ok {
		return false
	}
	for _, ref := range refs[1:] {
		order, ok := utteranceOrder[ref]
		if !ok || order >= primaryOrder {
			continue
		}
		if intentV2UtteranceUsable(utteranceByRef[ref], observationByRef) {
			return true
		}
	}
	return false
}

func intentV2PrimaryRefFirst(taskText string, refs []string, utteranceByRef map[string]contextcompiler.EnvelopeUtterance) []string {
	if len(refs) < 2 || normalizeRuntimeTaskText(taskText) == "" {
		return refs
	}
	bestIndex, bestScore := 0, 0
	for index, ref := range refs {
		utterance, ok := utteranceByRef[ref]
		if !ok || strings.TrimSpace(utterance.Text) == "" {
			continue
		}
		score := 0
		if normalizeRuntimeTaskText(taskText) == normalizeRuntimeTaskText(utterance.Text) {
			score = 2
		} else if _, _, precise := runtimeTaskSpanWithinSource(taskText, utterance.Text); precise {
			score = 1
		}
		if score > bestScore {
			bestIndex, bestScore = index, score
		}
	}
	if bestIndex == 0 || bestScore == 0 {
		return refs
	}
	ret := make([]string, 0, len(refs))
	ret = append(ret, refs[bestIndex])
	ret = append(ret, refs[:bestIndex]...)
	ret = append(ret, refs[bestIndex+1:]...)
	return ret
}

func intentV2SourceProtocolError(index int, code, message string) error {
	err := fmt.Errorf("%s: %s", code, message)
	return &strictjson.ProtocolError{
		Code: strictjson.ErrorJSONBusinessInvariant,
		Path: fmt.Sprintf("$.tasks[%d].sourceRefs", index), Message: err.Error(), Err: err,
	}
}

func uniqueIntentV2Refs(values []string) []string {
	ret := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	return ret
}

func sortedIntentV2Refs(values map[string]struct{}) []string {
	ret := make([]string, 0, len(values))
	for value := range values {
		ret = append(ret, value)
	}
	sort.Strings(ret)
	return ret
}

func taskBoundMessageIDs(task models.AIReplyTurnTask) []int64 {
	ret := make([]int64, 0, 4)
	seen := make(map[int64]struct{}, 4)
	appendID := func(id int64) {
		if id <= 0 {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		ret = append(ret, id)
	}
	appendID(task.SourceMessageID)
	if strings.TrimSpace(task.SourceBindingsJSON) == "" {
		return ret
	}
	var bindings contracts.TaskSourceBindingsV1
	if err := json.Unmarshal([]byte(task.SourceBindingsJSON), &bindings); err != nil {
		return ret
	}
	for _, binding := range bindings.Bindings {
		appendID(binding.MessageID)
		for _, observationID := range binding.ObservationMessageIDs {
			appendID(observationID)
		}
	}
	return ret
}
