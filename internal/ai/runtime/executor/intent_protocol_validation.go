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
		if objective := semanticGateNormalizeObjective(task.Objective); !semanticGateValidObjective(objective) {
			return fmt.Errorf("intentTasks[%d].objective is missing or invalid", index)
		}
		if relation := semanticGateNormalizeRelation(task.RelationToPrevious); !semanticGateValidRelation(relation) {
			return fmt.Errorf("intentTasks[%d].relationToPrevious is missing or invalid", index)
		}
		if resolution := semanticGateNormalizeResolution(task.ResolutionState); !semanticGateValidResolution(resolution) {
			return fmt.Errorf("intentTasks[%d].resolutionState is missing or invalid", index)
		}
	}

	candidates := currentTurnTaskCandidates(currentText)
	if len(candidates) <= 1 || !shouldDiscloseRuntimeIntentTaskCandidates(sourceTexts, candidates) {
		return nil
	}
	for candidateIndex, candidate := range candidates {
		covered := false
		for _, task := range tasks {
			traceTask := callbacks.IntentTaskTraceData{Text: task.Text, ResolvedText: task.ResolvedText}
			if score, _ := runtimeIntentAtomicTaskMatch(traceTask, candidate); score > 0 {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf("intentTasks does not cover atomic question %d of %d", candidateIndex+1, len(candidates))
		}
	}
	return nil
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
