package executor

import (
	"fmt"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/replyintent"
)

func validateRuntimeIntentDetectProtocol(parsed runtimeIntentDetectJSON, profile *models.ReplyIntentProfile, currentText string) error {
	tasks := []runtimeIntentTaskJSON(parsed.IntentTasks)
	if len(tasks) == 0 {
		return fmt.Errorf("intentTasks is empty")
	}
	requireSemantics := runtimeIntentProfileExpectsTaskSemantics(profile)
	requireResolvedText := runtimeIntentProfileExpectsResolvedText(profile)
	requireSourceRefs := runtimeIntentProfileExpectsSourceRefs(profile)
	sourceTexts := currentTurnIntentSourceTexts(currentText)
	for index, task := range tasks {
		if canonicalIntentCode(task.Intent) == "" {
			return fmt.Errorf("intentTasks[%d].intent is missing or invalid", index)
		}
		if strings.TrimSpace(task.Text) == "" {
			return fmt.Errorf("intentTasks[%d].text is missing", index)
		}
		if requireResolvedText && strings.TrimSpace(task.ResolvedText) == "" {
			return fmt.Errorf("intentTasks[%d].resolvedText is missing", index)
		}
		if requireSourceRefs && len(sourceTexts) > 0 {
			if len(task.SourceRefs) == 0 {
				return fmt.Errorf("intentTasks[%d].sourceRefs is missing", index)
			}
			for _, ref := range task.SourceRefs {
				refIndex := runtimeIntentSourceRefIndex(ref)
				if refIndex < 0 || refIndex >= len(sourceTexts) {
					return fmt.Errorf("intentTasks[%d].sourceRefs contains invalid ref %q", index, ref)
				}
			}
		}
		if !requireSemantics {
			continue
		}
		objective := semanticGateNormalizeObjective(task.Objective)
		if !semanticGateValidObjective(objective) {
			return fmt.Errorf("intentTasks[%d].objective is missing or invalid", index)
		}
		relation := semanticGateNormalizeRelation(task.RelationToPrevious)
		if !semanticGateValidRelation(relation) {
			return fmt.Errorf("intentTasks[%d].relationToPrevious is missing or invalid", index)
		}
		resolution := semanticGateNormalizeResolution(task.ResolutionState)
		if !semanticGateValidResolution(resolution) {
			return fmt.Errorf("intentTasks[%d].resolutionState is missing or invalid", index)
		}
		textRequiresContext := runtimeIntentAtomicCandidateRequiresContext(task.Text)
		if resolution == runtimeIntentResolutionResolvedFromContext && !textRequiresContext {
			return fmt.Errorf("intentTasks[%d].resolutionState resolved_from_context requires dependent original text", index)
		}
		if (canonicalIntentCode(task.Intent) == "hotel_info" || task.NeedsKnowledge) &&
			textRequiresContext && runtimeIntentAtomicCandidateRequiresContext(task.ResolvedText) {
			return fmt.Errorf("intentTasks[%d].resolvedText must be a self-contained question after context resolution", index)
		}
		if canonicalIntentCode(task.Intent) == "interaction" && runtimeIntentTaskHasExplicitBusinessInformationTarget(task) {
			return fmt.Errorf("intentTasks[%d] has an explicit business information target and cannot use interaction/clarify", index)
		}
	}

	candidates := currentTurnTaskCandidates(currentText)
	if len(candidates) <= 1 || !shouldDiscloseRuntimeIntentTaskCandidates(sourceTexts, candidates) {
		return nil
	}
	matchedTasks := make([]int, len(tasks))
	for index := range matchedTasks {
		matchedTasks[index] = -1
	}
	for candidateIndex := range candidates {
		seenTasks := make([]bool, len(tasks))
		if !matchRuntimeIntentCandidateToDistinctTask(candidateIndex, candidates, tasks, seenTasks, matchedTasks) {
			return fmt.Errorf("intentTasks does not cover atomic question %d of %d", candidateIndex+1, len(candidates))
		}
	}
	return nil
}

func matchRuntimeIntentCandidateToDistinctTask(candidateIndex int, candidates []string, tasks []runtimeIntentTaskJSON, seenTasks []bool, matchedTasks []int) bool {
	if candidateIndex < 0 || candidateIndex >= len(candidates) {
		return false
	}
	for taskIndex, task := range tasks {
		if seenTasks[taskIndex] {
			continue
		}
		traceTask := callbacks.IntentTaskTraceData{Text: task.Text, ResolvedText: task.ResolvedText}
		if score, _ := runtimeIntentAtomicTaskMatch(traceTask, candidates[candidateIndex]); score <= 0 {
			continue
		}
		seenTasks[taskIndex] = true
		if matchedTasks[taskIndex] < 0 || matchRuntimeIntentCandidateToDistinctTask(matchedTasks[taskIndex], candidates, tasks, seenTasks, matchedTasks) {
			matchedTasks[taskIndex] = candidateIndex
			return true
		}
	}
	return false
}

func runtimeIntentTaskHasExplicitBusinessInformationTarget(task runtimeIntentTaskJSON) bool {
	objective := semanticGateNormalizeObjective(task.Objective)
	switch objective {
	case "availability", "quantity", "location", "price", "time", "policy", "method", "explanation", "recommendation", "identity", "general_guidance", "compound_information", "status", "confirm":
	default:
		return false
	}
	for _, entity := range task.Entities {
		if strings.TrimSpace(entity.Text) == "" {
			continue
		}
		switch strings.TrimSpace(entity.Type) {
		case "facility", "supply", "room_type", "room", "service", "location", "order", "company":
			return true
		}
	}
	return false
}

func runtimeIntentProfileExpectsResolvedText(profile *models.ReplyIntentProfile) bool {
	schemaText := ""
	if profile != nil {
		schemaText = strings.TrimSpace(profile.IntentJSONSchema)
	}
	if schemaText == "" {
		schemaText = replyintent.DefaultHotelIntentJSONSchema()
	}
	return strings.Contains(schemaText, `"resolvedText"`)
}

func runtimeIntentProfileExpectsSourceRefs(profile *models.ReplyIntentProfile) bool {
	schemaText := ""
	if profile != nil {
		schemaText = strings.TrimSpace(profile.IntentJSONSchema)
	}
	if schemaText == "" {
		schemaText = replyintent.DefaultHotelIntentJSONSchema()
	}
	return strings.Contains(schemaText, `"sourceRefs"`)
}
