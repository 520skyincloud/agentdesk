package executor

import (
	"fmt"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/replyintent"
)

type runtimeIntentProtocolRepairContext struct {
	AdjacentAIReply      string
	PreviousCustomerText string
}

func repairRuntimeIntentDetectProtocol(parsed *runtimeIntentDetectJSON, currentText string, context runtimeIntentProtocolRepairContext, enforce bool) {
	if parsed == nil || !enforce || len(parsed.IntentTasks) == 0 {
		return
	}
	candidates := currentTurnTaskCandidates(currentText)
	if len(candidates) == 0 {
		return
	}
	tasks := []runtimeIntentTaskJSON(parsed.IntentTasks)
	repairRuntimeIntentProtocolRepeatReference(tasks, candidates, context)
}

func repairRuntimeIntentProtocolRepeatReference(tasks []runtimeIntentTaskJSON, candidates []string, context runtimeIntentProtocolRepairContext) bool {
	if len(tasks) != 1 || len(candidates) != 1 || strings.TrimSpace(context.AdjacentAIReply) == "" ||
		strings.TrimSpace(context.PreviousCustomerText) == "" {
		return false
	}
	candidate := strings.TrimSpace(candidates[0])
	if !runtimeIntentProtocolExplicitRepeatReference(candidate) {
		return false
	}
	previousCandidates := currentTurnTaskCandidates(context.PreviousCustomerText)
	if len(previousCandidates) != 1 || runtimeIntentAtomicCandidateRequiresContext(previousCandidates[0]) {
		return false
	}
	task := &tasks[0]
	intent := canonicalIntentCode(task.Intent)
	if (intent != "hotel_info" && intent != "hotel_variable") || !runtimeIntentProtocolTaskHasExecutableBusiness(*task) ||
		task.NeedsTool || task.NeedsHumanRoute {
		return false
	}
	currentScore := runtimeIntentProtocolAtomicTextMatchScore(task.Text, candidate)
	previousTextScore := runtimeIntentProtocolAtomicTextMatchScore(task.Text, previousCandidates[0])
	if currentScore == 0 && previousTextScore == 0 {
		return false
	}
	if !runtimeIntentProtocolRepeatReferenceMatchesContext(candidate, previousCandidates[0], context.AdjacentAIReply) {
		return false
	}
	resolvedText := strings.TrimSpace(task.ResolvedText)
	if resolvedText != "" && !runtimeIntentAtomicCandidateRequiresContext(resolvedText) &&
		runtimeIntentProtocolAtomicTextMatchScore(resolvedText, previousCandidates[0]) == 0 {
		return false
	}
	task.Text = candidate
	task.ResolvedText = strings.TrimSpace(previousCandidates[0])
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	return true
}

func runtimeIntentProtocolRepeatReferenceMatchesContext(candidate string, previousCustomerText string, adjacentAIReply string) bool {
	anchor := runtimeIntentProtocolRepeatReferenceAnchor(candidate)
	if anchor == "" {
		return true
	}
	contextText := runtimeIntentProtocolRepeatReferenceComparableText(previousCustomerText + " " + adjacentAIReply)
	if contextText != "" && strings.Contains(contextText, anchor) {
		return true
	}
	return false
}

func runtimeIntentProtocolRepeatReferenceAnchor(text string) string {
	anchor := runtimeIntentProtocolRepeatReferenceComparableText(text)
	for _, phrase := range []string{
		"再说一遍", "再说一次", "再讲一遍", "再讲一次", "再复述", "重新说", "重说一遍", "重复一遍", "复述一下",
		"只需要", "只要", "仅", "麻烦", "请", "正确的", "正确", "准确的", "准确", "完整的", "完整", "简单地", "简单", "直接",
		"刚才那个", "刚才的", "刚才", "刚刚那个", "刚刚的", "刚刚", "前面那个", "前面的", "前面", "上面那个", "上面的", "上面",
		"之前那个", "之前的", "之前", "这个", "那个", "内容", "答案", "一下", "一遍", "一次", "就行", "即可", "吧", "呢", "哈", "啊", "呀",
	} {
		anchor = strings.ReplaceAll(anchor, phrase, "")
	}
	return strings.TrimSpace(anchor)
}

func runtimeIntentProtocolRepeatReferenceComparableText(text string) string {
	compact := strings.ToLower(normalizeRuntimeKnowledgeQuery(text))
	return strings.NewReplacer(
		"wi-fi", "wifi",
		"无线网络", "wifi",
		"无线网", "wifi",
	).Replace(compact)
}

func runtimeIntentProtocolExplicitRepeatReference(text string) bool {
	compact := normalizeRuntimeKnowledgeQuery(text)
	return containsAny(compact, []string{
		"再说一遍", "再说一次", "再讲一遍", "再讲一次", "再复述", "重新说", "重说一遍", "重复一遍", "复述一下",
	})
}

func repairRuntimeIntentProtocolDuplicateOwnership(tasks []runtimeIntentTaskJSON, candidates []string, sourceTexts []string) bool {
	if len(tasks) < 2 || len(tasks) != len(candidates) {
		return false
	}
	owners := make([]int, len(candidates))
	assignments := make([]int, len(tasks))
	for index := range owners {
		owners[index] = -1
	}
	for index := range assignments {
		assignments[index] = -1
	}
	duplicateTask := -1
	duplicateCandidate := -1
	for taskIndex, task := range tasks {
		if !runtimeIntentProtocolTaskHasExecutableBusiness(task) {
			return false
		}
		matchedCandidate := -1
		taskText := normalizeRuntimeIntentProtocolAtomicText(task.Text)
		for candidateIndex, candidate := range candidates {
			if taskText != normalizeRuntimeIntentProtocolAtomicText(candidate) {
				continue
			}
			if matchedCandidate >= 0 {
				return false
			}
			matchedCandidate = candidateIndex
		}
		if matchedCandidate < 0 {
			return false
		}
		assignments[taskIndex] = matchedCandidate
		if owners[matchedCandidate] >= 0 {
			if duplicateTask >= 0 {
				return false
			}
			duplicateTask = taskIndex
			duplicateCandidate = matchedCandidate
			continue
		}
		owners[matchedCandidate] = taskIndex
	}
	missingCandidate := -1
	for candidateIndex, owner := range owners {
		if owner >= 0 {
			continue
		}
		if missingCandidate >= 0 {
			return false
		}
		missingCandidate = candidateIndex
	}
	if duplicateTask < 0 || duplicateCandidate < 0 || missingCandidate < 0 ||
		(len(sourceTexts) > 0 && !runtimeIntentProtocolCandidateMatchesTaskSource(candidates[missingCandidate], tasks[duplicateTask].SourceRefs, sourceTexts)) ||
		!runtimeIntentProtocolDuplicateTaskSupportsCandidate(&tasks[duplicateTask], candidates[missingCandidate], candidates[duplicateCandidate]) {
		return false
	}
	assignments[duplicateTask] = missingCandidate
	previousCandidate := -1
	for _, candidateIndex := range assignments {
		if candidateIndex <= previousCandidate {
			return false
		}
		previousCandidate = candidateIndex
	}
	tasks[duplicateTask].Text = strings.TrimSpace(candidates[missingCandidate])
	return true
}

func runtimeIntentProtocolDuplicateTaskSupportsCandidate(task *runtimeIntentTaskJSON, missingCandidate string, duplicateCandidate string) bool {
	if task == nil {
		return false
	}
	missingResolvedScore := runtimeIntentProtocolAtomicTextMatchScore(task.ResolvedText, missingCandidate)
	duplicateResolvedScore := runtimeIntentProtocolAtomicTextMatchScore(task.ResolvedText, duplicateCandidate)
	if missingResolvedScore >= 2 && missingResolvedScore > duplicateResolvedScore {
		return true
	}
	if duplicateResolvedScore > 0 {
		return false
	}
	resolvedText := strings.TrimSpace(task.ResolvedText)
	if resolvedText != "" && !runtimeIntentAtomicCandidateRequiresContext(resolvedText) {
		return false
	}
	missingEntityMatches := 0
	duplicateEntityMatches := 0
	for _, entity := range task.Entities {
		entityText := normalizeRuntimeKnowledgeQuery(entity.Text)
		if !runtimeIntentConcreteEntityText(entityText) {
			continue
		}
		inMissing := strings.Contains(normalizeRuntimeKnowledgeQuery(missingCandidate), entityText)
		inDuplicate := strings.Contains(normalizeRuntimeKnowledgeQuery(duplicateCandidate), entityText)
		if inMissing && !inDuplicate {
			missingEntityMatches++
		}
		if inDuplicate && !inMissing {
			duplicateEntityMatches++
		}
	}
	if missingEntityMatches == 0 || duplicateEntityMatches > 0 {
		return false
	}
	expectedObjective := runtimeIntentAtomicKnowledgeObjective(missingCandidate)
	actualObjective := semanticGateNormalizeObjective(task.Objective)
	if expectedObjective != "general_guidance" && actualObjective != expectedObjective {
		return false
	}
	task.ResolvedText = strings.TrimSpace(missingCandidate)
	return true
}

func runtimeIntentProtocolAtomicTextMatchScore(text string, candidate string) int {
	text = normalizeRuntimeIntentProtocolAtomicText(text)
	candidate = normalizeRuntimeIntentProtocolAtomicText(candidate)
	if text == "" || candidate == "" {
		return 0
	}
	switch {
	case text == candidate:
		return 3
	case len([]rune(candidate)) >= 3 && strings.Contains(text, candidate):
		return 2
	case len([]rune(text)) >= 4 && strings.Contains(candidate, text):
		return 1
	default:
		return 0
	}
}

func repairRuntimeIntentProtocolIntersectionObjective(tasks []runtimeIntentTaskJSON, candidates []string) int {
	repaired := 0
	for taskIndex := range tasks {
		task := &tasks[taskIndex]
		if canonicalIntentCode(task.Intent) != "hotel_info" || !runtimeIntentProtocolTaskHasExecutableBusiness(*task) ||
			task.NeedsResource || task.NeedsTool || task.NeedsHumanRoute || strings.TrimSpace(task.ResourceAction) != "" {
			continue
		}
		matchedCandidate := ""
		for _, candidate := range candidates {
			if normalizeRuntimeIntentProtocolAtomicText(task.Text) != normalizeRuntimeIntentProtocolAtomicText(candidate) {
				continue
			}
			if matchedCandidate != "" {
				matchedCandidate = ""
				break
			}
			matchedCandidate = candidate
		}
		if matchedCandidate == "" || !runtimeIntentProtocolRoomFeatureIntersectionQuestion(matchedCandidate, task.Entities) {
			continue
		}
		task.Objective = "compound_information"
		repaired++
	}
	return repaired
}

func runtimeIntentProtocolRoomFeatureIntersectionQuestion(text string, entities runtimeIntentEntityList) bool {
	compact := strings.ToLower(normalizeRuntimeKnowledgeQuery(text))
	if !runtimeIntentClauseHasSharedPredicate(compact) ||
		!containsAny(compact, []string{"哪些房型", "哪几种房型", "什么房型", "哪些房间", "哪几种房间"}) ||
		containsAny(compact, []string{"推荐", "哪个好", "哪种好", "怎么选", "选哪个", "适合", "建议", "值得", "优先"}) {
		return false
	}
	facilities := make(map[string]struct{}, len(entities))
	for _, entity := range entities {
		if strings.ToLower(strings.TrimSpace(entity.Type)) != "facility" {
			continue
		}
		entityText := normalizeRuntimeKnowledgeQuery(entity.Text)
		if entityText == "" || !strings.Contains(compact, strings.ToLower(entityText)) {
			continue
		}
		facilities[entityText] = struct{}{}
	}
	return len(facilities) >= 2
}

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

	var candidates []string
	if !requireSourceRefs || len(sourceTexts) == 0 {
		candidates = currentTurnTaskCandidates(currentText)
	}
	taskCandidates := make([]int, len(tasks))
	for index := range taskCandidates {
		taskCandidates[index] = -1
	}
	if requireSourceRefs && len(sourceTexts) > 0 {
		if err := validateRuntimeIntentProtocolModelOwnedSources(tasks, sourceTexts); err != nil {
			return err
		}
	} else if len(candidates) > 0 && (requireSemantics || len(candidates) > 1) {
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
		candidateRequiresContext := runtimeIntentAtomicCandidateRequiresContext(candidateText) ||
			runtimeIntentProtocolRelationAuthorizesContextResolution(task, candidateText, sourceTexts)
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

// validateRuntimeIntentProtocolModelOwnedSources verifies only provenance and
// order. IntentDetect alone decides how many tasks exist and where their
// semantic boundaries are; local code must not infer a competing task count.
func validateRuntimeIntentProtocolModelOwnedSources(tasks []runtimeIntentTaskJSON, sourceTexts []string) error {
	referencedSources := make([]bool, len(sourceTexts))
	lastPrimarySource := -1
	nextSourcePosition := make([]int, len(sourceTexts))

	for taskIndex, task := range tasks {
		for _, ref := range task.SourceRefs {
			refIndex := runtimeIntentSourceRefIndex(ref)
			if refIndex >= 0 && refIndex < len(referencedSources) {
				referencedSources[refIndex] = true
			}
		}
		primarySource := runtimeIntentSourceRefIndex(task.SourceRefs[0])
		if primarySource < lastPrimarySource {
			return fmt.Errorf("intentTasks[%d] is out of current-turn source order", taskIndex)
		}
		lastPrimarySource = primarySource

		sourceText := normalizeRuntimeIntentProtocolAtomicText(sourceTexts[primarySource])
		taskText := normalizeRuntimeIntentProtocolAtomicText(task.Text)
		if sourceText == "" || taskText == "" {
			return fmt.Errorf("intentTasks[%d].text cannot be grounded in its primary source", taskIndex)
		}
		searchFrom := nextSourcePosition[primarySource]
		relativeIndex := strings.Index(sourceText[searchFrom:], taskText)
		if relativeIndex < 0 {
			if strings.Contains(sourceText, taskText) {
				return fmt.Errorf("intentTasks[%d].text repeats or overlaps an earlier task in the same source", taskIndex)
			}
			return fmt.Errorf("intentTasks[%d].text is not a literal span of its primary source", taskIndex)
		}
		start := searchFrom + relativeIndex
		nextSourcePosition[primarySource] = start + len(taskText)
	}

	for sourceIndex, referenced := range referencedSources {
		if !referenced {
			return fmt.Errorf("current-turn source U%d is not covered by any intentTask sourceRefs", sourceIndex+1)
		}
	}
	return nil
}

func runtimeIntentProtocolRelationAuthorizesContextResolution(task runtimeIntentTaskJSON, originalText string, sourceTexts []string) bool {
	if !semanticGateRelationUsesPrevious(task.RelationToPrevious) ||
		!runtimeIntentProtocolTaskOwnsOriginalSource(task, originalText, sourceTexts) ||
		runtimeIntentProtocolOriginalIsIndependentQuestion(originalText) {
		return false
	}
	resolvedText := strings.TrimSpace(task.ResolvedText)
	return resolvedText != "" &&
		normalizeRuntimeIntentProtocolAtomicText(resolvedText) != normalizeRuntimeIntentProtocolAtomicText(originalText) &&
		!runtimeIntentAtomicCandidateRequiresContext(resolvedText)
}

func runtimeIntentProtocolTaskOwnsOriginalSource(task runtimeIntentTaskJSON, originalText string, sourceTexts []string) bool {
	if normalizeRuntimeIntentProtocolAtomicText(task.Text) != normalizeRuntimeIntentProtocolAtomicText(originalText) {
		return false
	}
	return runtimeIntentProtocolCandidateMatchesTaskSource(originalText, task.SourceRefs, sourceTexts)
}

func runtimeIntentProtocolOriginalIsIndependentQuestion(text string) bool {
	compact := compactRuntimeIntentClause(text)
	if compact == "" || isDependentIntentTaskClause(text) || runtimeIntentAtomicCandidateRequiresContext(text) ||
		!runtimeIntentClauseHasSelfContainedQuestion(compact) {
		return false
	}
	for _, marker := range []string{
		"可不可以", "有没有", "是不是", "什么时候", "为什么", "是否", "能不能",
		"怎么", "如何", "多少", "几个", "几瓶", "几点", "多久", "哪里", "在哪", "谁", "什么", "吗",
	} {
		compact = strings.ReplaceAll(compact, marker, "")
	}
	compact = strings.Trim(compact, "的了呢啊呀哈吧真")
	return len([]rune(compact)) >= 2
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
