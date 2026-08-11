package contextcompiler

import (
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

func buildRuntimeContextSnapshot(input CompileInput, memoryFacts []contracts.RuntimeContextFact) contracts.RuntimeContextSnapshotV1 {
	mode := "ai_serving"
	if input.Resume {
		mode = "resume"
	}
	dialogueAct := "unknown"
	focus := contracts.RuntimeContextFocus{RelationToPrior: "unknown", ResolvedTaskKeys: []string{}}
	sessionFacts := make([]contracts.RuntimeContextFact, 0, 12)
	if input.DialogueState != nil {
		if relation := normalizeDialogueRelation(input.DialogueState.Focus.RelationToPrior); relation != "" {
			dialogueAct = relation
			focus.RelationToPrior = relation
		}
		focus.Topic = boundedText(input.DialogueState.Focus.Topic, 120)
		for _, resolved := range input.DialogueState.ResolvedTasks {
			if resolved.TaskKey != "" {
				focus.ResolvedTaskKeys = appendUnique(focus.ResolvedTaskKeys, resolved.TaskKey)
			}
		}
		for _, fact := range input.DialogueState.SessionFacts {
			if len(sessionFacts) >= 12 {
				break
			}
			key := boundedText(fact.Key, 80)
			value := boundedText(fact.Value, 200)
			if key != "" && value != "" {
				sessionFacts = append(sessionFacts, contracts.RuntimeContextFact{Key: key, Value: value})
			}
		}
	}
	for _, fact := range memoryFacts {
		if len(sessionFacts) >= 12 {
			break
		}
		fact.Key = boundedText(fact.Key, 80)
		fact.Value = boundedText(fact.Value, 200)
		if fact.Key != "" && fact.Value != "" {
			sessionFacts = append(sessionFacts, fact)
		}
	}

	tasks := make([]contracts.RuntimeContextTask, 0)
	if input.ReplyPlan != nil {
		tasks = make([]contracts.RuntimeContextTask, 0, len(input.ReplyPlan.Tasks))
		for _, task := range input.ReplyPlan.Tasks {
			if task.OutputMode != "text" && task.OutputMode != "text_and_resource" && task.OutputMode != "clarification" {
				continue
			}
			tasks = append(tasks, contracts.RuntimeContextTask{
				TaskKey: task.TaskKey, Sequence: task.Sequence, Objective: task.Objective,
				OutputMode: task.OutputMode, KnowledgeStatus: normalizeRuntimeKnowledgeStatus(task.Knowledge.Status),
				EvidenceRefs: append([]string(nil), task.EvidenceRefs...), ActionRefs: append([]string(nil), task.ActionRefs...),
				Constraints: append([]string(nil), task.Constraints...),
			})
		}
	}
	prepared := make([]contracts.RuntimeContextPreparedAction, 0, len(input.PreparedActions))
	for _, action := range input.PreparedActions {
		if action.Status != "prepared" || action.ActionKey == "" || action.TaskKey == "" || !runtimeVisibleActionType(action.ActionType) {
			continue
		}
		prepared = append(prepared, contracts.RuntimeContextPreparedAction{
			ActionRef: action.ActionKey, TaskKey: action.TaskKey, Type: action.ActionType, Status: "prepared",
		})
		if len(prepared) >= 8 {
			break
		}
	}
	maxParts := 3
	if input.ReplyPlan != nil && input.ReplyPlan.GlobalConstraints.MaxReplyParts > 0 {
		maxParts = min(input.ReplyPlan.GlobalConstraints.MaxReplyParts, 3)
	}
	return contracts.RuntimeContextSnapshotV1{
		SchemaVersion:    contracts.RuntimeContextSnapshotV1SchemaVersion,
		ConversationMode: mode, DialogueAct: dialogueAct, Focus: focus,
		Tasks: tasks, SessionFacts: sessionFacts, PreparedActions: prepared,
		ResponsePolicy: contracts.RuntimeContextResponsePolicy{
			MaxParts: maxParts, Style: "concise_wechat_service",
			MustNotMentionInternalState: true, MustNotClaimUncommittedActions: true,
		},
	}
}

func buildIntentStateProjection(input CompileInput) map[string]any {
	projection := map[string]any{
		"schemaVersion":    "intent_dialogue_state.v1",
		"conversationMode": "ai_serving",
		"focus":            map[string]any{"topic": "", "relationToPrior": "unknown", "openTaskKeys": []string{}},
		"sessionFacts":     []map[string]string{},
	}
	if input.Resume {
		projection["conversationMode"] = "resume"
	}
	if input.DialogueState == nil {
		return projection
	}
	openKeys := make([]string, 0, len(input.DialogueState.OpenTasks))
	for _, task := range input.DialogueState.OpenTasks {
		if task.TaskKey != "" {
			openKeys = appendUnique(openKeys, task.TaskKey)
		}
	}
	projection["focus"] = map[string]any{
		"topic":           boundedText(input.DialogueState.Focus.Topic, 120),
		"relationToPrior": normalizeDialogueRelation(input.DialogueState.Focus.RelationToPrior),
		"openTaskKeys":    openKeys,
	}
	facts := make([]map[string]string, 0, 8)
	for _, fact := range input.DialogueState.SessionFacts {
		if len(facts) >= 8 {
			break
		}
		key := boundedText(fact.Key, 80)
		value := boundedText(fact.Value, 160)
		if key != "" && value != "" {
			facts = append(facts, map[string]string{"key": key, "value": value})
		}
	}
	projection["sessionFacts"] = facts
	return projection
}

func memoryFacts(input CompileInput) []contracts.RuntimeContextFact {
	if input.Memory == nil {
		return nil
	}
	candidates := []contracts.RuntimeContextFact{
		{Key: "compressed_stable_facts", Value: input.Memory.StableFacts},
		{Key: "compressed_open_issues", Value: input.Memory.OpenIssues},
		{Key: "compressed_customer_preferences", Value: input.Memory.CustomerPreferences},
		{Key: "compressed_media_summary", Value: input.Memory.MediaSummary},
	}
	ret := make([]contracts.RuntimeContextFact, 0, len(candidates))
	for _, fact := range candidates {
		fact.Value = strings.TrimSpace(fact.Value)
		if fact.Value != "" {
			ret = append(ret, fact)
		}
	}
	return ret
}

func normalizeDialogueRelation(value string) string {
	switch strings.TrimSpace(value) {
	case "new_topic", "follow_up", "repeat", "correction", "confirmation", "cancellation", "unknown":
		return strings.TrimSpace(value)
	default:
		return "unknown"
	}
}

func normalizeRuntimeKnowledgeStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "has_context", "no_context", "unanswerable", "unavailable":
		return strings.TrimSpace(value)
	default:
		return "not_needed"
	}
}

func runtimeVisibleActionType(value string) bool {
	switch strings.TrimSpace(value) {
	case "send_location", "send_mini_program", "send_phone", "send_knowledge_image":
		return true
	default:
		return false
	}
}

func appendUnique(items []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return items
	}
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func boundedText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return strings.TrimSpace(string(runes))
}
