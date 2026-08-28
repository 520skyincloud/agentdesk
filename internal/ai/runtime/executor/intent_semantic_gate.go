package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

const (
	runtimeIntentSemanticContractLegacy  = "legacy"
	runtimeIntentSemanticContractActive  = "active"
	runtimeIntentSemanticContractInvalid = "invalid"
)

const (
	runtimeIntentResolutionClear               = "clear"
	runtimeIntentResolutionResolvedFromContext = "resolved_from_context"
	runtimeIntentResolutionAmbiguous           = "ambiguous"
	runtimeIntentResolutionUnresolved          = "unresolved"
)

// runtimeIntentTaskSemantics carries the lightweight fields produced by the
// same IntentDetect call. It is kept separate from the trace DTO so the gate
// can be integrated without changing persistence or external contracts.
type runtimeIntentTaskSemantics struct {
	Objective          string
	RelationToPrevious string
	ResolutionState    string
}

type runtimeIntentSemanticGateContext struct {
	// HasAdjacentContext is retained for focused legacy tests. Production uses
	// the explicit fields so ordinary references do not weaken the stricter
	// answer-rejection adjacency rule.
	HasAdjacentContext           bool
	HasResolvableAdjacentContext bool
	HasAdjacentAIReply           bool
	RequireSemanticContract      bool
}

type runtimeIntentSemanticViolation struct {
	TaskIndex int
	Code      string
	Detail    string
}

type runtimeIntentSemanticGateResult struct {
	Intent                           callbacks.IntentTraceData
	TaskSemantics                    []runtimeIntentTaskSemantics
	ContractMode                     string
	SuppressLegacyConfidenceFallback bool
	Violations                       []runtimeIntentSemanticViolation
}

// applyRuntimeIntentSemanticConsistencyGate validates one IntentDetect result
// locally. It never calls a model and only performs deterministic repairs that
// can be proven from the structured task contract.
func applyRuntimeIntentSemanticConsistencyGate(
	intent callbacks.IntentTraceData,
	semantics []runtimeIntentTaskSemantics,
	context runtimeIntentSemanticGateContext,
) runtimeIntentSemanticGateResult {
	result := runtimeIntentSemanticGateResult{
		Intent:        semanticGateCloneIntent(intent),
		TaskSemantics: normalizeRuntimeIntentTaskSemantics(semantics),
		ContractMode:  runtimeIntentSemanticContractLegacy,
	}
	mode := runtimeIntentSemanticContractMode(result.Intent.IntentTasks, result.TaskSemantics, context.RequireSemanticContract)
	result.ContractMode = mode
	if mode == runtimeIntentSemanticContractLegacy {
		if !semanticGateAllTaskSemanticsEmpty(result.TaskSemantics) {
			for index := range result.Intent.IntentTasks {
				result.Intent.IntentTasks[index].Objective = ""
				result.Intent.IntentTasks[index].RelationToPrevious = ""
				result.Intent.IntentTasks[index].ResolutionState = ""
			}
			result.TaskSemantics = nil
		}
		return result
	}
	// A V2 result has already passed strict protocol validation. Keep its task
	// count and order model-owned; the legacy cleanup remains available only for
	// older profiles that did not enforce the complete task contract.
	if !context.RequireSemanticContract {
		result.Intent.IntentTasks, result.TaskSemantics, result.Violations = semanticGateDropRedundantInvalidResourceTasks(
			result.Intent.IntentTasks,
			result.TaskSemantics,
			result.Violations,
		)
	}
	mode = runtimeIntentSemanticContractMode(result.Intent.IntentTasks, result.TaskSemantics, context.RequireSemanticContract)
	result.ContractMode = mode

	if mode == runtimeIntentSemanticContractInvalid {
		result.Violations = append(result.Violations, runtimeIntentSemanticViolation{
			TaskIndex: -1,
			Code:      "semantic_contract_incomplete",
			Detail:    "one or more intent tasks have incomplete semantic fields",
		})
		result.TaskSemantics = semanticGatePadTaskSemantics(result.TaskSemantics, len(result.Intent.IntentTasks))
	}

	allTasksClear := mode == runtimeIntentSemanticContractActive && len(result.Intent.IntentTasks) > 0
	for index := range result.Intent.IntentTasks {
		task := result.Intent.IntentTasks[index]
		semantic := result.TaskSemantics[index]
		redundantClarify := task.SubIntent == "clarify" &&
			semanticGateClarifyDuplicatesBusinessTask(result.Intent.IntentTasks, result.TaskSemantics, index)
		task.Objective = semantic.Objective
		task.RelationToPrevious = semantic.RelationToPrevious
		task.ResolutionState = semantic.ResolutionState
		if !semanticGateValidObjective(semantic.Objective) || !semanticGateValidRelation(semantic.RelationToPrevious) || !semanticGateValidResolution(semantic.ResolutionState) {
			task = semanticGateClarificationTask(task)
			allTasksClear = false
			result.Violations = append(result.Violations, runtimeIntentSemanticViolation{
				TaskIndex: index,
				Code:      "semantic_task_incomplete",
				Detail:    "only the task with incomplete semantic fields was isolated",
			})
			result.Intent.IntentTasks[index] = task
			continue
		}
		if task.SubIntent == "clarify" &&
			semantic.ResolutionState != runtimeIntentResolutionAmbiguous &&
			semantic.ResolutionState != runtimeIntentResolutionUnresolved &&
			!redundantClarify {
			semantic.ResolutionState = runtimeIntentResolutionAmbiguous
			task.ResolutionState = runtimeIntentResolutionAmbiguous
			result.TaskSemantics[index] = semantic
			allTasksClear = false
			result.Violations = append(result.Violations, runtimeIntentSemanticViolation{
				TaskIndex: index,
				Code:      "clarification_state_repaired",
				Detail:    "clarify tasks must declare an ambiguous or unresolved resolution state",
			})
		}
		resolution := semantic.ResolutionState
		if !semanticGateValidRelation(semantic.RelationToPrevious) {
			task = semanticGateClarificationTask(task)
			allTasksClear = false
			result.Violations = append(result.Violations, runtimeIntentSemanticViolation{
				TaskIndex: index,
				Code:      "invalid_previous_relation",
				Detail:    "unknown previous-turn relation cannot authorize downstream actions",
			})
			result.Intent.IntentTasks[index] = task
			continue
		}

		switch resolution {
		case runtimeIntentResolutionAmbiguous, runtimeIntentResolutionUnresolved:
			task = semanticGateClarificationTask(task)
			allTasksClear = false
			result.Violations = append(result.Violations, runtimeIntentSemanticViolation{
				TaskIndex: index,
				Code:      "task_requires_clarification",
				Detail:    "ambiguous or unresolved task cannot execute knowledge, resource, tool, or handoff actions",
			})
		case runtimeIntentResolutionResolvedFromContext:
			if !semanticGateHasResolvableAdjacentContext(context) || !semanticGateRelationUsesPrevious(semantic.RelationToPrevious) {
				task = semanticGateClarificationTask(task)
				allTasksClear = false
				result.Violations = append(result.Violations, runtimeIntentSemanticViolation{
					TaskIndex: index,
					Code:      "context_resolution_unavailable",
					Detail:    "resolved_from_context requires an adjacent context and a previous-turn relation",
				})
			}
		case runtimeIntentResolutionClear:
		default:
			task = semanticGateClarificationTask(task)
			allTasksClear = false
			result.Violations = append(result.Violations, runtimeIntentSemanticViolation{
				TaskIndex: index,
				Code:      "invalid_resolution_state",
				Detail:    "unknown resolution state cannot authorize downstream actions",
			})
		}

		answerRejectedIntent := task.Intent == "human_complaint_risk" && strings.TrimSpace(task.SubIntent) == "answer_rejected"
		answerRejectedRelation := semantic.RelationToPrevious == "answer_rejected"
		if answerRejectedIntent || answerRejectedRelation {
			switch {
			case !semanticGateHasAdjacentAIReply(context):
				task.Intent = "interaction"
				task.SubIntent = "frustration"
				task.Objective = "social"
				task.RelationToPrevious = "correction"
				task.ResolutionState = runtimeIntentResolutionClear
				task.NeedsKnowledge = false
				task.NeedsResource = false
				task.NeedsTool = false
				task.NeedsHumanRoute = false
				task.ResourceAction = ""
				result.TaskSemantics[index] = runtimeIntentTaskSemantics{
					Objective:          "social",
					RelationToPrevious: "correction",
					ResolutionState:    runtimeIntentResolutionClear,
				}
				result.Violations = append(result.Violations, runtimeIntentSemanticViolation{
					TaskIndex: index,
					Code:      "answer_rejected_without_adjacent_ai",
					Detail:    "answer rejection cannot route without an immediately previous AI reply",
				})
			case !answerRejectedIntent || !answerRejectedRelation:
				task = semanticGateClarificationTask(task)
				task.ResolutionState = runtimeIntentResolutionAmbiguous
				semantic.ResolutionState = runtimeIntentResolutionAmbiguous
				result.TaskSemantics[index] = semantic
				allTasksClear = false
				result.Violations = append(result.Violations, runtimeIntentSemanticViolation{
					TaskIndex: index,
					Code:      "answer_rejected_contract_mismatch",
					Detail:    "answer_rejected relation and handoff classification must agree",
				})
			}
		}

		if task.SubIntent != "clarify" && task.Intent == "service_request" && semanticGateInformationObjective(semantic.Objective) {
			task.Intent = "hotel_info"
			task.NeedsKnowledge = true
			task.NeedsResource = false
			task.NeedsTool = false
			task.NeedsHumanRoute = false
			task.ResourceAction = ""
			result.Violations = append(result.Violations, runtimeIntentSemanticViolation{
				TaskIndex: index,
				Code:      "information_objective_reclassified",
				Detail:    "information objectives cannot execute a real-world service request",
			})
		}

		task = semanticGateRestrictTaskActions(task)
		if task.Intent == "hotel_variable" && !semanticGateAllowedResourceAction(task.ResourceAction) {
			task = semanticGateClarificationTask(task)
			allTasksClear = false
			result.Violations = append(result.Violations, runtimeIntentSemanticViolation{
				TaskIndex: index,
				Code:      "resource_action_unresolved",
				Detail:    "hotel_variable task requires one supported structured resource action",
			})
		}
		result.Intent.IntentTasks[index] = task
		if task.SubIntent == "clarify" && !redundantClarify {
			allTasksClear = false
		}
	}

	result.Intent = semanticGateRecomputeIntent(result.Intent, result.TaskSemantics)
	result.SuppressLegacyConfidenceFallback = allTasksClear
	return result
}

func semanticGateHasResolvableAdjacentContext(context runtimeIntentSemanticGateContext) bool {
	return context.HasResolvableAdjacentContext || context.HasAdjacentContext
}

func semanticGateHasAdjacentAIReply(context runtimeIntentSemanticGateContext) bool {
	return context.HasAdjacentAIReply || context.HasAdjacentContext
}

func semanticGateClarifyDuplicatesBusinessTask(tasks []callbacks.IntentTaskTraceData, semantics []runtimeIntentTaskSemantics, sourceIndex int) bool {
	if sourceIndex < 0 || sourceIndex >= len(tasks) || len(tasks) != len(semantics) {
		return false
	}
	source := tasks[sourceIndex]
	for index, task := range tasks {
		if index == sourceIndex || !semanticGateValidBusinessTask(task, semantics[index]) {
			continue
		}
		if semanticGateTasksShareSource(source, task) && semanticGateTasksShareText(source, task) {
			return true
		}
	}
	return false
}

func semanticGateDropRedundantInvalidResourceTasks(
	tasks []callbacks.IntentTaskTraceData,
	semantics []runtimeIntentTaskSemantics,
	violations []runtimeIntentSemanticViolation,
) ([]callbacks.IntentTaskTraceData, []runtimeIntentTaskSemantics, []runtimeIntentSemanticViolation) {
	if len(tasks) < 2 || len(tasks) != len(semantics) {
		return tasks, semantics, violations
	}

	drop := make(map[int]struct{})
	for index, task := range tasks {
		semantic := semantics[index]
		if !semanticGateInvalidResourcePseudoTask(task, semantic) {
			continue
		}
		for otherIndex, otherTask := range tasks {
			if index == otherIndex || !semanticGateValidBusinessTask(otherTask, semantics[otherIndex]) {
				continue
			}
			if !semanticGateTasksShareSource(task, otherTask) || !semanticGateTasksShareText(task, otherTask) {
				continue
			}
			drop[index] = struct{}{}
			violations = append(violations, runtimeIntentSemanticViolation{
				TaskIndex: index,
				Code:      "redundant_invalid_resource_task_dropped",
				Detail:    "an invalid non-executable resource task duplicated an existing valid business task",
			})
			break
		}
	}
	if len(drop) == 0 {
		return tasks, semantics, violations
	}

	keptTasks := make([]callbacks.IntentTaskTraceData, 0, len(tasks)-len(drop))
	keptSemantics := make([]runtimeIntentTaskSemantics, 0, len(semantics)-len(drop))
	for index := range tasks {
		if _, ok := drop[index]; ok {
			continue
		}
		keptTasks = append(keptTasks, tasks[index])
		keptSemantics = append(keptSemantics, semantics[index])
	}
	return keptTasks, keptSemantics, violations
}

func semanticGateInvalidResourcePseudoTask(task callbacks.IntentTaskTraceData, semantic runtimeIntentTaskSemantics) bool {
	if canonicalIntentCode(task.Intent) != "interaction" || semanticGateNormalizeObjective(semantic.Objective) != "resource" {
		return false
	}
	if task.NeedsKnowledge || task.NeedsTool || task.NeedsHumanRoute || semanticGateAllowedResourceAction(task.ResourceAction) {
		return false
	}
	return true
}

func semanticGateValidBusinessTask(task callbacks.IntentTaskTraceData, semantic runtimeIntentTaskSemantics) bool {
	intent := canonicalIntentCode(task.Intent)
	if intent == "" || intent == "interaction" || !isRuntimeTopLevelIntent(intent) {
		return false
	}
	return semanticGateValidObjective(semantic.Objective) &&
		semanticGateValidRelation(semantic.RelationToPrevious) &&
		semanticGateValidResolution(semantic.ResolutionState)
}

func semanticGateTasksShareSource(left callbacks.IntentTaskTraceData, right callbacks.IntentTaskTraceData) bool {
	if len(left.SourceRefs) == 0 || len(right.SourceRefs) == 0 {
		return false
	}
	for _, leftRef := range left.SourceRefs {
		for _, rightRef := range right.SourceRefs {
			if strings.TrimSpace(leftRef) != "" && strings.TrimSpace(leftRef) == strings.TrimSpace(rightRef) {
				return true
			}
		}
	}
	return false
}

func semanticGateTasksShareText(left callbacks.IntentTaskTraceData, right callbacks.IntentTaskTraceData) bool {
	leftTexts := []string{left.Text, left.ResolvedText}
	rightTexts := []string{right.Text, right.ResolvedText}
	for _, leftText := range leftTexts {
		leftText = normalizeRuntimeKnowledgeQuery(leftText)
		if leftText == "" {
			continue
		}
		for _, rightText := range rightTexts {
			if leftText == normalizeRuntimeKnowledgeQuery(rightText) {
				return true
			}
		}
	}
	return false
}

func applyRuntimeIntentSemanticConsistencyGateFromTrace(
	intent callbacks.IntentTraceData,
	context runtimeIntentSemanticGateContext,
) runtimeIntentSemanticGateResult {
	return applyRuntimeIntentSemanticConsistencyGate(intent, runtimeIntentTaskSemanticsFromTrace(intent.IntentTasks), context)
}

func runtimeIntentTaskSemanticsFromTrace(tasks []callbacks.IntentTaskTraceData) []runtimeIntentTaskSemantics {
	ret := make([]runtimeIntentTaskSemantics, len(tasks))
	for index, task := range tasks {
		ret[index] = runtimeIntentTaskSemantics{
			Objective:          task.Objective,
			RelationToPrevious: task.RelationToPrevious,
			ResolutionState:    task.ResolutionState,
		}
	}
	return ret
}

func semanticGateCloneIntent(intent callbacks.IntentTraceData) callbacks.IntentTraceData {
	ret := intent
	ret.SecondaryIntents = append([]string(nil), intent.SecondaryIntents...)
	ret.SecondaryIntentCodes = append([]string(nil), intent.SecondaryIntentCodes...)
	ret.ResourceActions = append([]string(nil), intent.ResourceActions...)
	ret.MixedSubTasks = append([]string(nil), intent.MixedSubTasks...)
	ret.ToolCodes = append([]string(nil), intent.ToolCodes...)
	ret.IntentTasks = make([]callbacks.IntentTaskTraceData, len(intent.IntentTasks))
	for index, task := range intent.IntentTasks {
		ret.IntentTasks[index] = task
		ret.IntentTasks[index].SourceRefs = append([]string(nil), task.SourceRefs...)
		ret.IntentTasks[index].Entities = append([]callbacks.IntentEntityTraceData(nil), task.Entities...)
	}
	return ret
}

func runtimeIntentSemanticContractMode(tasks []callbacks.IntentTaskTraceData, semantics []runtimeIntentTaskSemantics, required bool) string {
	if len(tasks) == 0 {
		return runtimeIntentSemanticContractLegacy
	}
	if semanticGateAllTaskSemanticsEmpty(semantics) {
		if required {
			return runtimeIntentSemanticContractInvalid
		}
		return runtimeIntentSemanticContractLegacy
	}
	if !required {
		for _, semantic := range semantics {
			if semantic.Objective == "" || semantic.RelationToPrevious == "" || semantic.ResolutionState == "" ||
				!semanticGateValidObjective(semantic.Objective) || !semanticGateValidRelation(semantic.RelationToPrevious) || !semanticGateValidResolution(semantic.ResolutionState) {
				return runtimeIntentSemanticContractLegacy
			}
		}
	}
	if len(tasks) != len(semantics) {
		return runtimeIntentSemanticContractInvalid
	}
	for _, semantic := range semantics {
		if semantic.Objective == "" || semantic.RelationToPrevious == "" || semantic.ResolutionState == "" {
			return runtimeIntentSemanticContractInvalid
		}
		if !semanticGateValidObjective(semantic.Objective) || !semanticGateValidRelation(semantic.RelationToPrevious) || !semanticGateValidResolution(semantic.ResolutionState) {
			return runtimeIntentSemanticContractInvalid
		}
	}
	return runtimeIntentSemanticContractActive
}

func normalizeRuntimeIntentTaskSemantics(items []runtimeIntentTaskSemantics) []runtimeIntentTaskSemantics {
	ret := make([]runtimeIntentTaskSemantics, len(items))
	for index, item := range items {
		ret[index] = runtimeIntentTaskSemantics{
			Objective:          semanticGateNormalizeObjective(item.Objective),
			RelationToPrevious: semanticGateNormalizeRelation(item.RelationToPrevious),
			ResolutionState:    semanticGateNormalizeResolution(item.ResolutionState),
		}
	}
	return ret
}

func semanticGateNormalizeValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func semanticGateNormalizeObjective(value string) string {
	switch semanticGateNormalizeValue(value) {
	case "existence":
		return "availability"
	case "access", "scope", "condition":
		return "policy"
	case "how":
		return "method"
	case "why":
		return "explanation"
	case "recommend":
		return "recommendation"
	case "information_request":
		return "general_guidance"
	case "resource_request", "handoff":
		return "action_request"
	default:
		return semanticGateNormalizeValue(value)
	}
}

func semanticGateNormalizeRelation(value string) string {
	switch semanticGateNormalizeValue(value) {
	case "", "independent", "new_topic":
		if strings.TrimSpace(value) == "" {
			return ""
		}
		return "independent"
	case "follow_up", "normal_follow_up":
		return "follow_up"
	case "missing_detail", "clarification_answer":
		return "clarification_answer"
	case "reference_previous", "reference":
		return "reference_previous"
	case "correction":
		return "correction"
	case "modify_previous":
		return "modify_previous"
	case "cancel_previous":
		return "cancel_previous"
	case "answer_rejected", "answer_contradicted", "answer_unresolved":
		return "answer_rejected"
	case "multi_task_continuation":
		return "follow_up"
	default:
		return semanticGateNormalizeValue(value)
	}
}

func semanticGateNormalizeResolution(value string) string {
	switch semanticGateNormalizeValue(value) {
	case "clear":
		return runtimeIntentResolutionClear
	case "resolved_from_context", "context_resolvable":
		return runtimeIntentResolutionResolvedFromContext
	case "ambiguous":
		return runtimeIntentResolutionAmbiguous
	case "unresolved", "unknown":
		return runtimeIntentResolutionUnresolved
	default:
		return semanticGateNormalizeValue(value)
	}
}

func semanticGateAllTaskSemanticsEmpty(items []runtimeIntentTaskSemantics) bool {
	for _, item := range items {
		if strings.TrimSpace(item.Objective) != "" || strings.TrimSpace(item.RelationToPrevious) != "" || strings.TrimSpace(item.ResolutionState) != "" {
			return false
		}
	}
	return true
}

func semanticGatePadTaskSemantics(items []runtimeIntentTaskSemantics, size int) []runtimeIntentTaskSemantics {
	ret := make([]runtimeIntentTaskSemantics, size)
	copy(ret, items)
	return ret
}

func semanticGateInformationObjective(objective string) bool {
	switch semanticGateNormalizeObjective(objective) {
	case "availability", "quantity", "location", "price", "time", "policy", "method", "explanation", "recommendation", "identity", "general_guidance", "compound_information":
		return true
	default:
		return false
	}
}

func semanticGateRelationUsesPrevious(relation string) bool {
	switch semanticGateNormalizeRelation(relation) {
	case "follow_up", "clarification_answer", "reference_previous", "correction", "modify_previous", "cancel_previous", "answer_rejected":
		return true
	default:
		return false
	}
}

func semanticGateValidRelation(relation string) bool {
	switch semanticGateNormalizeRelation(relation) {
	case "independent", "follow_up", "clarification_answer", "reference_previous", "correction", "modify_previous", "cancel_previous", "answer_rejected":
		return true
	default:
		return false
	}
}

func semanticGateValidObjective(objective string) bool {
	switch semanticGateNormalizeObjective(objective) {
	case "availability", "quantity", "location", "price", "time", "policy", "method", "explanation", "recommendation", "identity", "general_guidance", "compound_information", "action_request", "status", "modify", "cancel", "confirm", "complaint", "social", "unknown":
		return true
	default:
		return false
	}
}

func semanticGateValidResolution(resolution string) bool {
	switch semanticGateNormalizeResolution(resolution) {
	case runtimeIntentResolutionClear, runtimeIntentResolutionResolvedFromContext, runtimeIntentResolutionAmbiguous, runtimeIntentResolutionUnresolved:
		return true
	default:
		return false
	}
}

func semanticGateClarificationTask(task callbacks.IntentTaskTraceData) callbacks.IntentTaskTraceData {
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.Text = strings.TrimSpace(task.Text)
	if task.Text == "" {
		task.Text = strings.TrimSpace(task.ResolvedText)
	}
	task.ResolvedText = task.Text
	task.NeedsKnowledge = false
	task.NeedsResource = false
	task.NeedsTool = false
	task.NeedsHumanRoute = false
	task.ResourceAction = ""
	return task
}

func semanticGateRestrictTaskActions(task callbacks.IntentTaskTraceData) callbacks.IntentTaskTraceData {
	task.Intent = canonicalIntentCode(task.Intent)
	switch task.Intent {
	case "hotel_info":
		task.NeedsKnowledge = true
		task.NeedsTool = false
	case "service_request":
		task.NeedsTool = false
	case "interaction":
		task.NeedsKnowledge = false
		if task.SubIntent != "weather_query" {
			task.NeedsTool = false
		}
	default:
		task.NeedsKnowledge = false
		task.NeedsTool = false
	}
	if task.Intent != "hotel_variable" {
		task.NeedsResource = false
		task.ResourceAction = ""
	} else if semanticGateAllowedResourceAction(task.ResourceAction) {
		task.NeedsResource = true
	} else {
		task.NeedsResource = false
	}
	if task.Intent != "human_complaint_risk" {
		task.NeedsHumanRoute = false
	} else {
		task.NeedsHumanRoute = true
	}
	return task
}

func semanticGateAllowedResourceAction(action string) bool {
	switch strings.TrimSpace(action) {
	case "provide_phone", "provide_location", "provide_mini_program":
		return true
	default:
		return false
	}
}

func semanticGateRecomputeIntent(intent callbacks.IntentTraceData, semantics []runtimeIntentTaskSemantics) callbacks.IntentTraceData {
	if len(intent.IntentTasks) == 0 {
		return intent
	}

	primary := ""
	hasHuman := false
	hasVariable := false
	hasCheckinKnowledge := false
	hasKnowledge := false
	hasResource := false
	hasTool := false
	hasClarification := false
	resourceActions := make([]string, 0)
	for index := range intent.IntentTasks {
		task := &intent.IntentTasks[index]
		task.Intent = canonicalIntentCode(task.Intent)
		if task.Intent == "" || !isRuntimeTopLevelIntent(task.Intent) {
			task.Intent = "interaction"
		}
		if task.Intent == "human_complaint_risk" {
			hasHuman = true
			task.NeedsHumanRoute = true
		}
		if task.Intent == "hotel_variable" {
			hasVariable = true
		}
		if task.Intent == "hotel_info" || task.NeedsKnowledge {
			hasKnowledge = true
		}
		if task.Intent == "hotel_info" && isCheckinProcessSubIntent(task.SubIntent) {
			hasCheckinKnowledge = true
		}
		if task.Intent == "hotel_variable" && task.NeedsResource && semanticGateAllowedResourceAction(task.ResourceAction) {
			hasResource = true
			resourceActions = appendIfMissing(resourceActions, strings.TrimSpace(task.ResourceAction))
		}
		if task.NeedsTool {
			hasTool = true
		}
		if task.SubIntent == "clarify" || index < len(semantics) && (semantics[index].ResolutionState == runtimeIntentResolutionAmbiguous || semantics[index].ResolutionState == runtimeIntentResolutionUnresolved) {
			hasClarification = true
		}
	}

	if hasHuman {
		primary = "human_complaint_risk"
	} else if hasCheckinKnowledge {
		primary = "hotel_info"
	} else if hasVariable {
		primary = "hotel_variable"
	} else {
		for _, task := range intent.IntentTasks {
			if task.Intent != "interaction" {
				primary = task.Intent
				break
			}
		}
	}
	if primary == "" {
		primary = intent.IntentTasks[0].Intent
	}
	if primary == "" {
		primary = "interaction"
	}

	secondary := make([]string, 0)
	for _, task := range intent.IntentTasks {
		if task.Intent != primary {
			secondary = appendIfMissing(secondary, task.Intent)
		}
	}

	intent.PrimaryIntent = primary
	intent.MatchedIntentCode = primary
	intent.DetectedIntent = primary
	intent.SecondaryIntents = secondary
	intent.SecondaryIntentCodes = append([]string(nil), secondary...)
	intent.NeedsKnowledge = hasKnowledge
	intent.NeedsResource = hasResource
	intent.NeedsTool = hasTool
	intent.NeedsHumanRoute = hasHuman
	intent.NeedsClarification = hasClarification
	intent.ResourceActions = resourceActions
	intent.ResourceAction = ""
	intent.ResourceType = ""
	if len(resourceActions) > 0 {
		intent.ResourceAction = resourceActions[0]
		intent.ResourceType = hotelVariableResourceTypeFromAction(intent.ResourceAction)
	}
	if !hasHuman {
		intent.HumanRoutePolicy = ""
	}
	if subIntent := semanticGatePrimarySubIntent(intent.IntentTasks, primary); subIntent != "" {
		intent.SubIntent = subIntent
	}
	intent.ShouldReply = true
	return intent
}

func semanticGatePrimarySubIntent(tasks []callbacks.IntentTaskTraceData, primary string) string {
	for _, task := range tasks {
		if task.Intent == primary && strings.TrimSpace(task.SubIntent) != "" {
			return strings.TrimSpace(task.SubIntent)
		}
	}
	return ""
}
