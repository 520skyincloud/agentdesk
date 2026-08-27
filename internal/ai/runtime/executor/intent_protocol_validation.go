package executor

import (
	"fmt"
	"strings"

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
		if canonicalIntentCode(task.Intent) == "interaction" && runtimeIntentTaskHasExplicitBusinessInformationTarget(task) {
			return fmt.Errorf("intentTasks[%d] has an explicit business information target and cannot use interaction/clarify", index)
		}
	}

	candidates := currentTurnTaskCandidates(currentText)
	taskCandidates := make([]int, len(tasks))
	for index := range taskCandidates {
		taskCandidates[index] = -1
	}
	if len(candidates) > 0 && (requireSemantics || len(candidates) > 1) {
		var err error
		taskCandidates, err = assignRuntimeIntentProtocolCandidates(tasks, candidates, sourceTexts)
		if err != nil {
			return err
		}
	}
	if !requireSemantics {
		return nil
	}
	for taskIndex, task := range tasks {
		candidateText := strings.TrimSpace(task.Text)
		if candidateIndex := taskCandidates[taskIndex]; candidateIndex >= 0 && candidateIndex < len(candidates) {
			candidateText = candidates[candidateIndex]
		}
		candidateRequiresContext := runtimeIntentAtomicCandidateRequiresContext(candidateText)
		resolution := semanticGateNormalizeResolution(task.ResolutionState)
		if resolution == runtimeIntentResolutionResolvedFromContext && !candidateRequiresContext {
			return fmt.Errorf("intentTasks[%d].resolutionState resolved_from_context requires context-dependent original text", taskIndex)
		}
		if candidateRequiresContext && runtimeIntentProtocolTaskHasExecutableBusiness(task) {
			if resolution != runtimeIntentResolutionResolvedFromContext {
				return fmt.Errorf("intentTasks[%d].resolutionState must be resolved_from_context for context-dependent original text", taskIndex)
			}
			if (canonicalIntentCode(task.Intent) == "hotel_info" || task.NeedsKnowledge) &&
				runtimeIntentAtomicCandidateRequiresContext(task.ResolvedText) {
				return fmt.Errorf("intentTasks[%d].resolvedText must be a self-contained question after context resolution", taskIndex)
			}
		}
	}
	return nil
}

func runtimeIntentProtocolTaskHasExecutableBusiness(task runtimeIntentTaskJSON) bool {
	return canonicalIntentCode(task.Intent) != "interaction" ||
		task.NeedsKnowledge || task.NeedsResource || task.NeedsTool || task.NeedsHumanRoute ||
		strings.TrimSpace(task.ResourceAction) != ""
}

func assignRuntimeIntentProtocolCandidates(tasks []runtimeIntentTaskJSON, candidates []string, sourceTexts []string) ([]int, error) {
	taskCandidates := make([]int, len(tasks))
	duplicateCandidates := make([]int, len(tasks))
	for index := range taskCandidates {
		taskCandidates[index] = -1
		duplicateCandidates[index] = -1
	}
	candidateOwners := make([]int, len(candidates))
	for index := range candidateOwners {
		candidateOwners[index] = -1
	}

	for taskIndex, task := range tasks {
		taskText := normalizeRuntimeIntentProtocolAtomicText(task.Text)
		if taskText == "" {
			continue
		}
		matchedCandidate := -1
		for candidateIndex, candidate := range candidates {
			if normalizeRuntimeIntentProtocolAtomicText(candidate) != taskText {
				continue
			}
			if matchedCandidate >= 0 {
				return nil, fmt.Errorf("intentTasks[%d].text matches multiple atomic questions", taskIndex)
			}
			matchedCandidate = candidateIndex
		}
		if matchedCandidate < 0 {
			continue
		}
		if candidateOwners[matchedCandidate] >= 0 {
			duplicateCandidates[taskIndex] = matchedCandidate
			continue
		}
		taskCandidates[taskIndex] = matchedCandidate
		candidateOwners[matchedCandidate] = taskIndex
	}

	for taskIndex, task := range tasks {
		if taskCandidates[taskIndex] >= 0 || duplicateCandidates[taskIndex] >= 0 ||
			semanticGateNormalizeResolution(task.ResolutionState) != runtimeIntentResolutionResolvedFromContext {
			continue
		}
		matchedCandidate := -1
		for candidateIndex, candidate := range candidates {
			if candidateOwners[candidateIndex] >= 0 || !runtimeIntentAtomicCandidateRequiresContext(candidate) ||
				!runtimeIntentProtocolCandidateMatchesTaskSource(candidate, task.SourceRefs, sourceTexts) {
				continue
			}
			if matchedCandidate >= 0 {
				matchedCandidate = -2
				break
			}
			matchedCandidate = candidateIndex
		}
		if matchedCandidate < 0 {
			continue
		}
		taskCandidates[taskIndex] = matchedCandidate
		candidateOwners[matchedCandidate] = taskIndex
	}

	for taskIndex, candidateIndex := range duplicateCandidates {
		if candidateIndex >= 0 {
			return nil, fmt.Errorf("intentTasks[%d] duplicates atomic question %d", taskIndex, candidateIndex+1)
		}
	}
	for candidateIndex, owner := range candidateOwners {
		if owner < 0 {
			return nil, fmt.Errorf("intentTasks does not cover atomic question %d of %d", candidateIndex+1, len(candidates))
		}
	}
	for taskIndex, task := range tasks {
		if taskCandidates[taskIndex] < 0 && runtimeIntentProtocolTaskHasExecutableBusiness(task) {
			return nil, fmt.Errorf("intentTasks[%d] is an extra executable business task", taskIndex)
		}
	}
	previousCandidate := -1
	for taskIndex, candidateIndex := range taskCandidates {
		if candidateIndex < 0 {
			continue
		}
		if candidateIndex < previousCandidate {
			return nil, fmt.Errorf("intentTasks[%d] is out of current-turn atomic question order", taskIndex)
		}
		previousCandidate = candidateIndex
		tasks[taskIndex].Text = strings.TrimSpace(candidates[candidateIndex])
	}
	return taskCandidates, nil
}

func normalizeRuntimeIntentProtocolAtomicText(text string) string {
	text = strings.TrimSpace(cleanRuntimeQuestionLine(text))
	for {
		trimmed := false
		for _, prefix := range []string{
			"麻烦请问一下", "麻烦请问", "麻烦问一下", "麻烦问下",
			"我想请问一下", "我想请问", "我想问一下", "我想问",
			"请问一下", "请问", "想问一下", "想问", "麻烦",
		} {
			if !strings.HasPrefix(text, prefix) || len(text) <= len(prefix) {
				continue
			}
			text = strings.TrimLeft(strings.TrimSpace(text[len(prefix):]), "，,：:；;。.!！?？")
			trimmed = true
			break
		}
		if !trimmed {
			break
		}
	}
	return normalizeRuntimeKnowledgeQuery(text)
}

func runtimeIntentProtocolCandidateMatchesTaskSource(candidate string, refs runtimeIntentSourceRefList, sourceTexts []string) bool {
	if len(refs) == 0 || len(sourceTexts) == 0 {
		return false
	}
	refIndex := runtimeIntentSourceRefIndex(refs[0])
	if refIndex < 0 || refIndex >= len(sourceTexts) {
		return false
	}
	candidateText := normalizeRuntimeKnowledgeQuery(candidate)
	sourceText := normalizeRuntimeKnowledgeQuery(sourceTexts[refIndex])
	return candidateText != "" && sourceText != "" && strings.Contains(sourceText, candidateText)
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
