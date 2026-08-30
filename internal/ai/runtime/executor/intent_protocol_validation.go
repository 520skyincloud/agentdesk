package executor

import (
	"fmt"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/replyintent"
)

type runtimeIntentProtocolRepairContext struct {
	AdjacentAIReply      string
	AdjacentServiceReply string
	PreviousCustomerText string
}

func repairRuntimeIntentDetectProtocol(parsed *runtimeIntentDetectJSON, currentText string, context runtimeIntentProtocolRepairContext, enforce bool) {
	// IntentDetect owns semantic task boundaries and context resolution. Local
	// code deliberately does not rewrite text/resolvedText or infer a competing
	// task count from punctuation and keywords. The parameters remain in the
	// signature for compatibility with legacy profiles and focused tests.
	if parsed != nil {
		parsed.IntentTasks = collapseExactRuntimeIntentTasks(parsed.IntentTasks)
	}
	_ = currentText
	_ = context
	_ = enforce
}

func collapseExactRuntimeIntentTasks(tasks runtimeIntentTaskList) runtimeIntentTaskList {
	if len(tasks) < 2 {
		return tasks
	}
	ret := make(runtimeIntentTaskList, 0, len(tasks))
	indexByKey := make(map[string]int, len(tasks))
	for _, task := range tasks {
		key := runtimeIntentProtocolExactTaskKey(task)
		if index, exists := indexByKey[key]; exists {
			ret[index].SourceRefs = mergeRuntimeIntentProtocolSourceRefs(ret[index].SourceRefs, task.SourceRefs)
			continue
		}
		indexByKey[key] = len(ret)
		ret = append(ret, task)
	}
	return ret
}

func mergeRuntimeIntentProtocolSourceRefs(current runtimeIntentSourceRefList, incoming runtimeIntentSourceRefList) runtimeIntentSourceRefList {
	ret := append(runtimeIntentSourceRefList(nil), current...)
	seen := make(map[string]struct{}, len(ret)+len(incoming))
	for _, ref := range ret {
		if normalized := strings.ToUpper(strings.TrimSpace(ref)); normalized != "" {
			seen[normalized] = struct{}{}
		}
	}
	for _, ref := range incoming {
		normalized := strings.ToUpper(strings.TrimSpace(ref))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		ret = append(ret, normalized)
	}
	return ret
}

func repairRuntimeIntentProtocolCurrentTurnReferences(tasks []runtimeIntentTaskJSON, sourceTexts []string) int {
	if len(tasks) == 0 || len(sourceTexts) <= 1 {
		return 0
	}
	repairedCount := 0
	for taskIndex := range tasks {
		task := &tasks[taskIndex]
		candidate, contextTexts, nearestContext, ok := runtimeIntentProtocolCurrentTurnReferenceInputs(*task, sourceTexts)
		if !ok || normalizeRuntimeIntentProtocolAtomicText(task.Text) == normalizeRuntimeIntentProtocolAtomicText(candidate) {
			continue
		}
		repaired := *task
		repaired.Text = candidate
		if cleaned, cleanedOK := runtimeIntentProtocolResolvedCurrentClause(repaired, candidate, nearestContext); cleanedOK {
			repaired.ResolvedText = cleaned
		}
		contextText := strings.Join(contextTexts, " ")
		if !runtimeIntentProtocolResolvedReferenceGroundedInText(repaired, candidate, contextText) ||
			validateRuntimeIntentProtocolResolvedEntities(repaired, candidate, contextText) != nil ||
			validateRuntimeIntentProtocolExplicitReplacement(repaired, candidate, nearestContext) != nil {
			continue
		}
		*task = repaired
		repairedCount++
	}
	return repairedCount
}

func runtimeIntentProtocolCurrentTurnReferenceInputs(task runtimeIntentTaskJSON, sourceTexts []string) (string, []string, string, bool) {
	if semanticGateNormalizeResolution(task.ResolutionState) != runtimeIntentResolutionResolvedFromContext ||
		semanticGateNormalizeRelation(task.RelationToPrevious) != "independent" || len(task.SourceRefs) < 2 {
		return "", nil, "", false
	}
	primarySource := runtimeIntentSourceRefIndex(task.SourceRefs[0])
	if primarySource <= 0 || primarySource >= len(sourceTexts) {
		return "", nil, "", false
	}
	candidates := currentTurnTaskCandidates(sourceTexts[primarySource])
	if len(candidates) != 1 || !runtimeIntentAtomicCandidateRequiresContext(candidates[0]) {
		return "", nil, "", false
	}
	contextTexts := make([]string, 0, len(task.SourceRefs)-1)
	nearestContext := ""
	nearestContextSource := -1
	seen := map[int]bool{}
	for _, ref := range task.SourceRefs[1:] {
		contextSource := runtimeIntentSourceRefIndex(ref)
		if contextSource < 0 || contextSource >= primarySource || contextSource >= len(sourceTexts) || seen[contextSource] {
			continue
		}
		seen[contextSource] = true
		contextText := strings.TrimSpace(sourceTexts[contextSource])
		if contextText == "" {
			continue
		}
		if contextSource > nearestContextSource {
			nearestContext = contextText
			nearestContextSource = contextSource
		}
		contextTexts = append(contextTexts, contextText)
	}
	if len(contextTexts) == 0 {
		return "", nil, "", false
	}
	return strings.TrimSpace(candidates[0]), contextTexts, nearestContext, true
}

func repairRuntimeIntentProtocolCurrentReference(tasks []runtimeIntentTaskJSON, candidates []string, context runtimeIntentProtocolRepairContext) bool {
	if len(tasks) != 1 || len(candidates) != 1 || strings.TrimSpace(context.AdjacentAIReply) == "" ||
		strings.TrimSpace(context.PreviousCustomerText) == "" {
		return false
	}
	candidate := strings.TrimSpace(candidates[0])
	if !runtimeIntentAtomicCandidateRequiresContext(candidate) {
		return false
	}
	task := &tasks[0]
	if !runtimeIntentProtocolTaskHasExecutableBusiness(*task) || task.NeedsTool || task.NeedsHumanRoute ||
		!semanticGateRelationUsesPrevious(task.RelationToPrevious) ||
		semanticGateNormalizeResolution(task.ResolutionState) != runtimeIntentResolutionResolvedFromContext {
		return false
	}
	resolvedText := strings.TrimSpace(task.ResolvedText)
	if resolvedText == "" || runtimeIntentAtomicCandidateRequiresContext(resolvedText) {
		return false
	}

	repaired := *task
	repaired.Text = candidate
	if cleaned, ok := runtimeIntentProtocolResolvedCurrentClause(repaired, candidate, context.PreviousCustomerText); ok {
		repaired.ResolvedText = cleaned
	}
	if err := validateRuntimeIntentResolvedReferenceTaskContext(repaired, candidate, context); err != nil {
		return false
	}
	changed := normalizeRuntimeIntentProtocolAtomicText(task.Text) != normalizeRuntimeIntentProtocolAtomicText(repaired.Text) ||
		normalizeRuntimeIntentProtocolAtomicText(task.ResolvedText) != normalizeRuntimeIntentProtocolAtomicText(repaired.ResolvedText)
	if changed {
		*task = repaired
	}
	return changed
}

func runtimeIntentProtocolResolvedCurrentClause(task runtimeIntentTaskJSON, candidate string, previousCustomerText string) (string, bool) {
	previousCandidates := currentTurnTaskCandidates(previousCustomerText)
	resolvedCandidates := currentTurnTaskCandidates(task.ResolvedText)
	if len(previousCandidates) != 1 || len(resolvedCandidates) != 2 {
		return "", false
	}
	previous := normalizeRuntimeIntentProtocolAtomicText(previousCandidates[0])
	if previous == "" {
		return "", false
	}
	currentAnchors := runtimeIntentProtocolCurrentEntityAnchors(task, candidate, previousCustomerText)
	if len(currentAnchors) == 0 {
		return "", false
	}
	remaining := ""
	removedPrevious := false
	for _, resolvedCandidate := range resolvedCandidates {
		if normalizeRuntimeIntentProtocolAtomicText(resolvedCandidate) == previous {
			if removedPrevious {
				return "", false
			}
			removedPrevious = true
			continue
		}
		if remaining != "" || !runtimeIntentProtocolTextContainsAnyAnchor(resolvedCandidate, currentAnchors) {
			return "", false
		}
		remaining = strings.TrimSpace(resolvedCandidate)
	}
	if !removedPrevious || remaining == "" || runtimeIntentAtomicCandidateRequiresContext(remaining) {
		return "", false
	}
	return remaining, true
}

func runtimeIntentProtocolCurrentEntityAnchors(task runtimeIntentTaskJSON, candidate string, previousCustomerText string) []string {
	current := normalizeRuntimeKnowledgeQuery(candidate)
	previous := normalizeRuntimeKnowledgeQuery(previousCustomerText)
	ret := make([]string, 0, len(task.Entities))
	for _, entity := range task.Entities {
		anchor := runtimeIntentProtocolEntityAnchor(entity)
		if len([]rune(anchor)) < 2 || !strings.Contains(current, anchor) || strings.Contains(previous, anchor) {
			continue
		}
		ret = append(ret, anchor)
	}
	return ret
}

func runtimeIntentProtocolTextContainsAnyAnchor(text string, anchors []string) bool {
	compact := normalizeRuntimeKnowledgeQuery(text)
	for _, anchor := range anchors {
		if strings.Contains(compact, anchor) {
			return true
		}
	}
	return false
}

func validateRuntimeIntentResolvedReferenceContext(parsed runtimeIntentDetectJSON, currentText string, context runtimeIntentProtocolRepairContext, enforce bool) error {
	if !enforce {
		return nil
	}
	sourceTexts := currentTurnIntentSourceTexts(currentText)
	for taskIndex, task := range parsed.IntentTasks {
		if semanticGateNormalizeResolution(task.ResolutionState) != runtimeIntentResolutionResolvedFromContext {
			continue
		}
		contextParts := make([]string, 0, len(task.SourceRefs)+2)
		relation := semanticGateNormalizeRelation(task.RelationToPrevious)
		earlierSources, hasEarlierSources := runtimeIntentProtocolEarlierCurrentTurnSources(task, sourceTexts)
		adjacentReply := ""
		switch {
		case hasEarlierSources:
			contextParts = append(contextParts, earlierSources...)
		case semanticGateRelationUsesPrevious(relation):
			adjacentReply = runtimeIntentProtocolAdjacentServiceReply(context)
			if strings.TrimSpace(context.PreviousCustomerText) == "" || adjacentReply == "" {
				return fmt.Errorf("intentTasks[%d] resolved_from_context requires an adjacent customer and service reply pair", taskIndex)
			}
			contextParts = append(contextParts, context.PreviousCustomerText, adjacentReply)
		default:
			return fmt.Errorf("intentTasks[%d] resolved_from_context has no authorized context relation", taskIndex)
		}
		candidate := strings.TrimSpace(task.Text)
		if candidate == "" && len(task.SourceRefs) > 0 {
			primaryIndex := runtimeIntentSourceRefIndex(task.SourceRefs[0])
			if primaryIndex >= 0 && primaryIndex < len(sourceTexts) {
				candidate = sourceTexts[primaryIndex]
			}
		}
		contextText := strings.Join(contextParts, " ")
		grounded := runtimeIntentProtocolResolvedReferenceGroundedInText(task, candidate, contextText)
		if !hasEarlierSources {
			groundingContext := context
			groundingContext.AdjacentAIReply = adjacentReply
			grounded = runtimeIntentProtocolResolvedReferenceGrounded(task, candidate, groundingContext)
		}
		if !grounded {
			return fmt.Errorf("intentTasks[%d] resolvedText is not grounded in its authorized context", taskIndex)
		}
		if err := validateRuntimeIntentProtocolResolvedEntities(task, candidate, contextText); err != nil {
			return fmt.Errorf("intentTasks[%d] %w", taskIndex, err)
		}
	}
	return nil
}

func runtimeIntentProtocolEarlierCurrentTurnSources(task runtimeIntentTaskJSON, sourceTexts []string) ([]string, bool) {
	if len(task.SourceRefs) < 2 || len(sourceTexts) < 2 {
		return nil, false
	}
	primaryIndex := runtimeIntentSourceRefIndex(task.SourceRefs[0])
	if primaryIndex <= 0 || primaryIndex >= len(sourceTexts) {
		return nil, false
	}
	ret := make([]string, 0, len(task.SourceRefs)-1)
	seen := make(map[int]struct{}, len(task.SourceRefs)-1)
	for _, ref := range task.SourceRefs[1:] {
		index := runtimeIntentSourceRefIndex(ref)
		if index < 0 || index >= primaryIndex || index >= len(sourceTexts) {
			continue
		}
		if _, exists := seen[index]; exists {
			continue
		}
		text := strings.TrimSpace(sourceTexts[index])
		if text == "" {
			continue
		}
		seen[index] = struct{}{}
		ret = append(ret, text)
	}
	return ret, len(ret) > 0
}

func runtimeIntentProtocolAdjacentServiceReply(context runtimeIntentProtocolRepairContext) string {
	if reply := strings.TrimSpace(context.AdjacentServiceReply); reply != "" {
		return reply
	}
	return strings.TrimSpace(context.AdjacentAIReply)
}

func validateRuntimeIntentCurrentTurnReferenceContexts(parsed runtimeIntentDetectJSON, currentText string) error {
	sourceTexts := currentTurnIntentSourceTexts(currentText)
	if len(sourceTexts) <= 1 {
		return nil
	}
	for taskIndex, task := range parsed.IntentTasks {
		if len(task.SourceRefs) == 0 {
			continue
		}
		primarySource := runtimeIntentSourceRefIndex(task.SourceRefs[0])
		if primarySource <= 0 || primarySource >= len(sourceTexts) {
			continue
		}
		candidates := currentTurnTaskCandidates(sourceTexts[primarySource])
		if len(candidates) != 1 || !runtimeIntentAtomicCandidateRequiresContext(candidates[0]) ||
			semanticGateNormalizeResolution(task.ResolutionState) != runtimeIntentResolutionResolvedFromContext {
			continue
		}
		if semanticGateNormalizeRelation(task.RelationToPrevious) != "independent" {
			return fmt.Errorf("intentTasks[%d] current-turn source reference must keep relationToPrevious independent", taskIndex)
		}
		candidate, contextTexts, nearestContext, ok := runtimeIntentProtocolCurrentTurnReferenceInputs(task, sourceTexts)
		if !ok {
			return fmt.Errorf("intentTasks[%d] resolved current-turn reference requires an earlier sourceRef", taskIndex)
		}
		contextText := strings.Join(contextTexts, " ")
		if !runtimeIntentProtocolResolvedReferenceGroundedInText(task, candidate, contextText) {
			return fmt.Errorf("intentTasks[%d] resolvedText is not grounded in its referenced current-turn sources", taskIndex)
		}
		if err := validateRuntimeIntentProtocolResolvedEntities(task, candidate, contextText); err != nil {
			return fmt.Errorf("intentTasks[%d] %w", taskIndex, err)
		}
		if err := validateRuntimeIntentProtocolExplicitReplacement(task, candidate, nearestContext); err != nil {
			return fmt.Errorf("intentTasks[%d] %w", taskIndex, err)
		}
		previous := normalizeRuntimeIntentProtocolAtomicText(nearestContext)
		resolved := normalizeRuntimeIntentProtocolAtomicText(task.ResolvedText)
		if previous != "" && resolved != previous && strings.Contains(resolved, previous) {
			return fmt.Errorf("intentTasks[%d].resolvedText retains an earlier current-turn question as a second answer target", taskIndex)
		}
	}
	return nil
}

func validateRuntimeIntentResolvedReferenceTaskContext(task runtimeIntentTaskJSON, candidate string, context runtimeIntentProtocolRepairContext) error {
	if strings.TrimSpace(context.PreviousCustomerText) == "" || strings.TrimSpace(context.AdjacentAIReply) == "" {
		return fmt.Errorf("resolved reference requires an adjacent customer question and AI reply")
	}
	if !runtimeIntentProtocolResolvedReferenceGrounded(task, candidate, context) {
		return fmt.Errorf("resolved reference is not grounded in the adjacent customer question or AI reply")
	}
	if err := validateRuntimeIntentProtocolResolvedEntities(task, candidate, context.PreviousCustomerText+" "+context.AdjacentAIReply); err != nil {
		return err
	}
	if err := validateRuntimeIntentProtocolExplicitReplacement(task, candidate, context.PreviousCustomerText); err != nil {
		return err
	}
	previousCandidates := currentTurnTaskCandidates(context.PreviousCustomerText)
	resolvedCandidates := currentTurnTaskCandidates(task.ResolvedText)
	if len(previousCandidates) != 1 {
		return nil
	}
	previous := normalizeRuntimeIntentProtocolAtomicText(previousCandidates[0])
	currentAnchors := runtimeIntentProtocolCurrentEntityAnchors(task, candidate, context.PreviousCustomerText)
	if previous != "" && len(currentAnchors) > 0 &&
		strings.Contains(normalizeRuntimeIntentProtocolAtomicText(task.ResolvedText), previous) {
		return fmt.Errorf("intentTasks[0].resolvedText retains the previous customer question as a second answer target")
	}
	if len(resolvedCandidates) <= 1 {
		return nil
	}
	for _, resolvedCandidate := range resolvedCandidates {
		if normalizeRuntimeIntentProtocolAtomicText(resolvedCandidate) == previous {
			return fmt.Errorf("intentTasks[0].resolvedText retains the previous customer question as a second answer target")
		}
	}
	return nil
}

func runtimeIntentProtocolResolvedReferenceGrounded(task runtimeIntentTaskJSON, candidate string, context runtimeIntentProtocolRepairContext) bool {
	return runtimeIntentProtocolResolvedReferenceGroundedWithAspectContext(
		task,
		candidate,
		context.PreviousCustomerText+" "+context.AdjacentAIReply,
		context.PreviousCustomerText,
	)
}

func runtimeIntentProtocolResolvedReferenceGroundedInText(task runtimeIntentTaskJSON, candidate string, contextText string) bool {
	return runtimeIntentProtocolResolvedReferenceGroundedWithAspectContext(task, candidate, contextText, contextText)
}

func runtimeIntentProtocolResolvedReferenceGroundedWithAspectContext(task runtimeIntentTaskJSON, candidate string, contextText string, aspectContextText string) bool {
	resolved := normalizeRuntimeKnowledgeQuery(task.ResolvedText)
	adjacentContext := normalizeRuntimeKnowledgeQuery(contextText)
	if resolved == "" || adjacentContext == "" {
		return false
	}
	currentAnchors := runtimeIntentProtocolCurrentEntityAnchors(task, candidate, contextText)
	if runtimeIntentProtocolExplicitReplacementSubject(candidate) != "" && len(currentAnchors) > 0 {
		previousAspect := runtimeIntentProtocolReferenceAspect(aspectContextText)
		if previousAspect != "" && previousAspect == runtimeIntentProtocolReferenceAspect(task.ResolvedText) {
			return true
		}
	}
	for _, entity := range task.Entities {
		anchor := runtimeIntentProtocolEntityAnchor(entity)
		if !runtimeIntentConcreteEntityText(anchor) || !strings.Contains(resolved, anchor) ||
			runtimeIntentProtocolStringSliceContains(currentAnchors, anchor) {
			continue
		}
		if strings.Contains(adjacentContext, anchor) {
			return true
		}
	}
	semanticCore := runtimeIntentProtocolReferenceSemanticCore(task.ResolvedText, currentAnchors)
	if len([]rune(semanticCore)) < 2 {
		return false
	}
	for size := 4; size >= 2; size-- {
		for _, anchor := range runtimeIntentProtocolRuneWindows(semanticCore, size) {
			if runtimeIntentProtocolGenericReferenceAnchor(anchor) ||
				runtimeIntentProtocolTextContainsAnyAnchor(anchor, currentAnchors) {
				continue
			}
			if strings.Contains(adjacentContext, anchor) {
				return true
			}
		}
	}
	return false
}

func validateRuntimeIntentProtocolResolvedEntities(task runtimeIntentTaskJSON, candidate string, contextText string) error {
	resolved := normalizeRuntimeKnowledgeQuery(task.ResolvedText)
	contextCompact := normalizeRuntimeKnowledgeQuery(contextText)
	currentAnchors := runtimeIntentProtocolCurrentEntityAnchors(task, candidate, contextText)
	for _, entity := range task.Entities {
		anchor := runtimeIntentProtocolEntityAnchor(entity)
		if !runtimeIntentConcreteEntityText(anchor) || runtimeIntentProtocolAspectOnlyEntity(anchor) ||
			!strings.Contains(resolved, anchor) || runtimeIntentProtocolStringSliceContains(currentAnchors, anchor) {
			continue
		}
		if !strings.Contains(contextCompact, anchor) {
			return fmt.Errorf("resolvedText adds entity %q that is absent from its referenced context", strings.TrimSpace(entity.Text))
		}
	}
	return nil
}

func validateRuntimeIntentProtocolExplicitReplacement(task runtimeIntentTaskJSON, candidate string, previousCustomerText string) error {
	replacement := runtimeIntentProtocolExplicitReplacementSubject(candidate)
	if replacement == "" {
		return nil
	}
	currentAnchors := runtimeIntentProtocolCurrentEntityAnchors(task, candidate, previousCustomerText)
	if len(currentAnchors) == 0 {
		return fmt.Errorf("resolved reference with an explicit replacement subject requires a declared current entity")
	}
	resolved := normalizeRuntimeKnowledgeQuery(task.ResolvedText)
	for _, anchor := range currentAnchors {
		if !strings.Contains(replacement, anchor) && !strings.Contains(anchor, replacement) {
			continue
		}
		if !strings.Contains(resolved, anchor) {
			return fmt.Errorf("resolved reference does not contain the current replacement entity")
		}
		previousAnchors := runtimeIntentProtocolPreviousSameTypeAnchors(task, anchor, previousCustomerText)
		for _, previousAnchor := range previousAnchors {
			if previousAnchor != anchor && strings.Contains(resolved, previousAnchor) {
				return fmt.Errorf("intentTasks[0].resolvedText retains the previous customer question as a previous answer target")
			}
		}
		predicateCore := runtimeIntentProtocolReplacementPredicateCore(task.ResolvedText, append(currentAnchors, previousAnchors...))
		if predicateCore != "" && !strings.Contains(normalizeRuntimeKnowledgeQuery(previousCustomerText), predicateCore) {
			return fmt.Errorf("resolved reference adds unsupported subject or predicate %q", predicateCore)
		}
		return nil
	}
	return fmt.Errorf("resolved reference replacement subject does not match the declared current entity")
}

func runtimeIntentProtocolEntityAnchor(entity runtimeIntentEntityJSON) string {
	anchor := normalizeRuntimeKnowledgeQuery(entity.Text)
	if strings.EqualFold(strings.TrimSpace(entity.Type), "room_type") {
		anchor = strings.TrimSuffix(strings.TrimSuffix(anchor, "房型"), "客房")
	}
	return strings.TrimSpace(anchor)
}

func runtimeIntentProtocolExplicitReplacementSubject(candidate string) string {
	compact := normalizeRuntimeKnowledgeQuery(candidate)
	for _, pronoun := range []string{"这个", "那个", "这些", "那些", "这种", "那种"} {
		if strings.HasPrefix(compact, pronoun) {
			return ""
		}
	}
	prefix := ""
	for _, item := range []string{"同样", "那么", "那", "这"} {
		if strings.HasPrefix(compact, item) {
			prefix = item
			break
		}
	}
	if prefix == "" {
		return ""
	}
	compact = strings.TrimPrefix(compact, prefix)
	for _, suffix := range []string{"怎么样呢", "怎么样", "如何呢", "如何", "可以吗", "有吗", "呢", "吗", "呀", "啊", "吧"} {
		compact = strings.TrimSuffix(compact, suffix)
	}
	compact = strings.TrimSpace(compact)
	if len([]rune(compact)) < 2 || runtimeIntentProtocolAspectOnlyEntity(compact) ||
		runtimeIntentProtocolDependentAspectCandidate(compact) {
		return ""
	}
	return compact
}

func runtimeIntentProtocolPreviousSameTypeAnchors(task runtimeIntentTaskJSON, currentAnchor string, previousCustomerText string) []string {
	ret := []string{}
	for _, entity := range task.Entities {
		if runtimeIntentProtocolEntityAnchor(entity) != currentAnchor {
			continue
		}
		entityType := strings.ToLower(strings.TrimSpace(entity.Type))
		if entityType == "room_type" {
			previous := normalizeRuntimeIntentProtocolAtomicText(previousCustomerText)
			for _, marker := range []string{"房型", "客房"} {
				searchFrom := 0
				for searchFrom < len(previous) {
					index := strings.Index(previous[searchFrom:], marker)
					if index < 0 {
						break
					}
					prefix := previous[:searchFrom+index]
					prefix = runtimeIntentProtocolReferenceSemanticCore(prefix, nil)
					prefixRunes := []rune(prefix)
					if len(prefixRunes) > 8 {
						prefixRunes = prefixRunes[len(prefixRunes)-8:]
					}
					if anchor := runtimeIntentProtocolBoundedReferenceAnchor(string(prefixRunes)); anchor != "" {
						ret = appendIfMissing(ret, anchor)
					}
					searchFrom += index + len(marker)
				}
			}
		}
		if anchor := runtimeIntentProtocolLeadingSubjectAnchor(previousCustomerText); anchor != "" {
			ret = appendIfMissing(ret, anchor)
		}
		if entityType != "room_type" {
			if anchor := runtimeIntentProtocolAvailabilityObjectAnchor(previousCustomerText); anchor != "" {
				ret = appendIfMissing(ret, anchor)
			}
		}
	}
	return ret
}

func runtimeIntentProtocolLeadingSubjectAnchor(text string) string {
	compact := normalizeRuntimeIntentProtocolAtomicText(text)
	bestIndex := -1
	for _, marker := range []string{
		"有没有", "是否有", "是不是", "可不可以", "能不能", "多少钱", "什么时候", "在哪里", "哪儿", "哪里",
		"都有", "同时有", "步行", "走路", "驾车", "开车", "打车", "有", "是", "价格", "收费", "几点", "多久", "怎么", "如何",
	} {
		if index := strings.Index(compact, marker); index > 0 && (bestIndex < 0 || index < bestIndex) {
			bestIndex = index
		}
	}
	if bestIndex <= 0 {
		return ""
	}
	return runtimeIntentProtocolBoundedReferenceAnchor(runtimeIntentProtocolReferenceSemanticCore(compact[:bestIndex], nil))
}

func runtimeIntentProtocolAvailabilityObjectAnchor(text string) string {
	compact := normalizeRuntimeIntentProtocolAtomicText(text)
	for _, prefix := range []string{"有没有", "是否有", "有"} {
		if !strings.HasPrefix(compact, prefix) {
			continue
		}
		anchor := strings.TrimPrefix(compact, prefix)
		anchor = strings.Trim(anchor, "的了呢啊呀哈吧吗么哦啦这那和与都也")
		return runtimeIntentProtocolBoundedReferenceAnchor(anchor)
	}
	return ""
}

func runtimeIntentProtocolBoundedReferenceAnchor(anchor string) string {
	anchor = strings.TrimSpace(anchor)
	anchor = strings.Trim(anchor, "的了呢啊呀哈吧吗么哦啦这那和与都也")
	if len([]rune(anchor)) < 2 || len([]rune(anchor)) > 8 || runtimeIntentProtocolGenericReferenceAnchor(anchor) {
		return ""
	}
	return anchor
}

func runtimeIntentProtocolReplacementPredicateCore(text string, removals []string) string {
	core := runtimeIntentProtocolReferenceSemanticCore(text, removals)
	for _, phrase := range []string{"同时具备", "同时配备", "同时提供", "配备", "配有", "具备", "包含", "支持", "供应", "都有", "同时有"} {
		core = strings.ReplaceAll(core, phrase, "")
	}
	for _, character := range []string{"和", "与", "都", "也", "有", "是", "要", "会", "能", "可"} {
		core = strings.ReplaceAll(core, character, "")
	}
	return strings.TrimSpace(core)
}

func runtimeIntentProtocolReferenceSemanticCore(text string, removals []string) string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	for _, removal := range removals {
		compact = strings.ReplaceAll(compact, normalizeRuntimeKnowledgeQuery(removal), "")
	}
	for _, phrase := range []string{
		"可以不可以", "可不可以", "有没有", "是不是", "是否有", "能不能", "什么时候", "为什么", "怎么办", "咋办",
		"怎么", "如何", "多少", "几个", "几瓶", "几点", "多久", "哪里", "哪儿", "在哪", "哪个", "什么",
		"酒店", "门店", "房间", "客房", "房型", "当前", "相关", "问题", "服务", "提供", "是否",
		"同样", "刚才", "刚刚", "前面", "上面", "之前", "这个", "那个", "那么", "请问", "麻烦",
	} {
		compact = strings.ReplaceAll(compact, phrase, "")
	}
	return strings.Trim(compact, "的了呢啊呀哈吧吗么哦啦这那")
}

func runtimeIntentProtocolReferenceAspect(text string) string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	switch {
	case containsAny(compact, []string{"收费", "免费", "价格", "多少钱", "费用"}):
		return "price"
	case containsAny(compact, []string{"几点", "时间", "多久", "什么时候"}):
		return "time"
	case containsAny(compact, []string{"哪里", "哪儿", "在哪", "地址", "位置"}):
		return "location"
	case containsAny(compact, []string{"怎么", "如何", "办法", "方式", "流程"}):
		return "method"
	case containsAny(compact, []string{"有没有", "是否有", "能不能", "可不可以", "可以吗", "是不是"}):
		return "availability"
	case strings.Contains(compact, "有") && strings.HasSuffix(strings.Trim(compact, "呢啊呀哈吧"), "吗"):
		return "availability"
	default:
		return ""
	}
}

func runtimeIntentProtocolRuneWindows(text string, size int) []string {
	runes := []rune(text)
	if size <= 0 || len(runes) < size {
		return nil
	}
	ret := make([]string, 0, len(runes)-size+1)
	for index := 0; index+size <= len(runes); index++ {
		ret = append(ret, string(runes[index:index+size]))
	}
	return ret
}

func runtimeIntentProtocolGenericReferenceAnchor(anchor string) bool {
	switch normalizeRuntimeKnowledgeQuery(anchor) {
	case "酒店", "门店", "房间", "客房", "房型", "当前", "相关", "问题", "服务", "可以", "需要", "提供",
		"怎么", "如何", "几点", "哪里", "在哪", "时间", "有没", "没有", "是否", "是不", "能不", "不能",
		"什么", "这个", "那个", "一样", "同样":
		return true
	default:
		return false
	}
}

func runtimeIntentProtocolStringSliceContains(items []string, expected string) bool {
	for _, item := range items {
		if item == expected {
			return true
		}
	}
	return false
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
	}
	if requireSourceRefs && len(sourceTexts) > 0 {
		if err := validateRuntimeIntentProtocolModelOwnedSources(tasks, sourceTexts); err != nil {
			return err
		}
	}
	return nil
}

// validateRuntimeIntentProtocolModelOwnedSources verifies only provenance and
// order. IntentDetect alone decides how many tasks exist and where their
// semantic boundaries are; local code must not infer a competing task count.
func validateRuntimeIntentProtocolModelOwnedSources(tasks []runtimeIntentTaskJSON, sourceTexts []string) error {
	lastPrimarySource := -1
	referencedSources := make([]bool, len(sourceTexts))

	for taskIndex, task := range tasks {
		primarySource := runtimeIntentSourceRefIndex(task.SourceRefs[0])
		if primarySource < lastPrimarySource {
			return fmt.Errorf("intentTasks[%d] is out of current-turn source order", taskIndex)
		}
		lastPrimarySource = primarySource
		for _, ref := range task.SourceRefs {
			index := runtimeIntentSourceRefIndex(ref)
			if index >= 0 && index < len(referencedSources) {
				referencedSources[index] = true
			}
		}
	}
	for sourceIndex, referenced := range referencedSources {
		if !referenced {
			return fmt.Errorf("current-turn source U%d is not referenced by any intent task", sourceIndex+1)
		}
	}
	return nil
}

func runtimeIntentProtocolExactTaskKey(task runtimeIntentTaskJSON) string {
	entities := make([]string, 0, len(task.Entities))
	for _, entity := range task.Entities {
		entities = append(entities, strings.ToLower(strings.TrimSpace(entity.Type))+":"+normalizeRuntimeKnowledgeQuery(entity.Text))
	}
	parts := []string{
		canonicalIntentCode(task.Intent),
		strings.ToLower(strings.TrimSpace(task.SubIntent)),
		semanticGateNormalizeObjective(task.Objective),
		semanticGateNormalizeRelation(task.RelationToPrevious),
		semanticGateNormalizeResolution(task.ResolutionState),
		normalizeRuntimeIntentProtocolAtomicText(task.Text),
		normalizeRuntimeIntentProtocolAtomicText(task.ResolvedText),
		strings.Join(entities, ","),
		fmt.Sprintf("%t|%t|%t|%t", task.NeedsKnowledge, task.NeedsResource, task.NeedsTool, task.NeedsHumanRoute),
		strings.TrimSpace(task.ResourceAction),
	}
	return strings.Join(parts, "|")
}

// validateRuntimeIntentProtocolAtomicCoverageBySource is a conservative
// completeness check. IntentDetect still owns task boundaries; local code only
// rejects obvious punctuated omissions and never creates or rewrites a task.
func validateRuntimeIntentProtocolAtomicCoverageBySource(tasks []runtimeIntentTaskJSON, sourceTexts []string) error {
	for sourceIndex, sourceText := range sourceTexts {
		candidates := currentTurnTaskCandidates(sourceText)
		if len(candidates) == 0 {
			continue
		}

		sourceTasks := make([]runtimeIntentTaskJSON, 0, len(tasks))
		for _, task := range tasks {
			if !runtimeIntentProtocolTaskHasExecutableBusiness(task) || len(task.SourceRefs) == 0 ||
				runtimeIntentSourceRefIndex(task.SourceRefs[0]) != sourceIndex {
				continue
			}
			sourceTasks = append(sourceTasks, task)
		}
		if len(candidates) == 1 {
			if len(sourceTasks) == 0 {
				return fmt.Errorf("current-turn source U%d does not cover atomic question 1 of 1: %s", sourceIndex+1, strings.TrimSpace(candidates[0]))
			}
			continue
		}

		covered := make([]bool, len(candidates))
		usedExactTasks := make([]bool, len(sourceTasks))
		for candidateIndex, candidate := range candidates {
			candidateText := normalizeRuntimeIntentProtocolAtomicText(candidate)
			for taskIndex, task := range sourceTasks {
				if usedExactTasks[taskIndex] || normalizeRuntimeIntentProtocolAtomicText(task.Text) != candidateText {
					continue
				}
				covered[candidateIndex] = true
				usedExactTasks[taskIndex] = true
				break
			}
		}

		for _, task := range sourceTasks {
			if semanticGateNormalizeObjective(task.Objective) != "compound_information" {
				continue
			}
			candidateIndexes := runtimeIntentProtocolCompoundCoveredCandidateIndexes(task, candidates)
			if len(candidateIndexes) == 0 || !runtimeIntentProtocolCompoundCandidatesAreRelated(task, sourceText, candidates, candidateIndexes) {
				continue
			}
			for _, candidateIndex := range candidateIndexes {
				covered[candidateIndex] = true
			}
		}

		for candidateIndex, isCovered := range covered {
			if !isCovered {
				return fmt.Errorf("current-turn source U%d does not cover atomic question %d of %d: %s", sourceIndex+1, candidateIndex+1, len(candidates), strings.TrimSpace(candidates[candidateIndex]))
			}
		}
	}
	return nil
}

func runtimeIntentProtocolCompoundCoveredCandidateIndexes(task runtimeIntentTaskJSON, candidates []string) []int {
	taskText := normalizeRuntimeIntentProtocolAtomicText(task.Text)
	if taskText == "" {
		return nil
	}
	ret := make([]int, 0, len(candidates))
	for candidateIndex, candidate := range candidates {
		candidateText := normalizeRuntimeIntentProtocolAtomicText(candidate)
		if candidateText != "" && strings.Contains(taskText, candidateText) {
			ret = append(ret, candidateIndex)
		}
	}
	return ret
}

func runtimeIntentProtocolCompoundCandidatesAreRelated(task runtimeIntentTaskJSON, sourceText string, candidates []string, candidateIndexes []int) bool {
	if len(candidateIndexes) <= 1 {
		return true
	}
	if runtimeIntentProtocolCompoundCandidatesShareEntity(task, candidates, candidateIndexes) {
		return true
	}
	if normalizeRuntimeIntentProtocolAtomicText(task.Text) != normalizeRuntimeIntentProtocolAtomicText(sourceText) {
		return false
	}
	subject := runtimeIntentProtocolCompoundSingleConcreteSubject(task, candidates, candidateIndexes)
	if subject == "" {
		return false
	}
	hasAnchoredCandidate := false
	for _, candidateIndex := range candidateIndexes {
		if candidateIndex < 0 || candidateIndex >= len(candidates) {
			return false
		}
		candidateText := normalizeRuntimeKnowledgeQuery(candidates[candidateIndex])
		if strings.Contains(candidateText, subject) {
			hasAnchoredCandidate = true
			continue
		}
		if !runtimeIntentProtocolDependentAspectCandidate(candidateText) {
			return false
		}
	}
	return hasAnchoredCandidate
}

func runtimeIntentProtocolCompoundCandidatesShareEntity(task runtimeIntentTaskJSON, candidates []string, candidateIndexes []int) bool {
	for _, entity := range task.Entities {
		entityText := normalizeRuntimeKnowledgeQuery(entity.Text)
		if !runtimeIntentConcreteEntityText(entityText) {
			continue
		}
		shared := true
		for _, candidateIndex := range candidateIndexes {
			if candidateIndex < 0 || candidateIndex >= len(candidates) ||
				!strings.Contains(normalizeRuntimeKnowledgeQuery(candidates[candidateIndex]), entityText) {
				shared = false
				break
			}
		}
		if shared {
			return true
		}
	}
	return false
}

func runtimeIntentProtocolCompoundSingleConcreteSubject(task runtimeIntentTaskJSON, candidates []string, candidateIndexes []int) string {
	subject := ""
	for _, entity := range task.Entities {
		entityText := normalizeRuntimeKnowledgeQuery(entity.Text)
		if !runtimeIntentConcreteEntityText(entityText) || runtimeIntentProtocolAspectOnlyEntity(entityText) {
			continue
		}
		appears := false
		for _, candidateIndex := range candidateIndexes {
			if candidateIndex >= 0 && candidateIndex < len(candidates) &&
				strings.Contains(normalizeRuntimeKnowledgeQuery(candidates[candidateIndex]), entityText) {
				appears = true
				break
			}
		}
		if !appears {
			continue
		}
		if subject != "" && subject != entityText {
			return ""
		}
		subject = entityText
	}
	return subject
}

func runtimeIntentProtocolAspectOnlyEntity(text string) bool {
	switch normalizeRuntimeKnowledgeQuery(text) {
	case "位置", "地址", "收费", "费用", "价格", "免费", "数量", "时间", "方式", "流程", "范围", "条件", "要求", "情况":
		return true
	default:
		return false
	}
}

func runtimeIntentProtocolDependentAspectCandidate(text string) bool {
	text = strings.Trim(normalizeRuntimeKnowledgeQuery(text), "啊呀呢吧哈啦哦嘛么的了")
	return containsAnyPrefix(text, []string{
		"位置", "地址", "收费", "费用", "价格", "免费", "数量", "几瓶", "几个", "多少", "时间", "几点", "多久", "什么时候",
		"哪里", "在哪", "怎么", "如何", "方式", "流程", "范围", "条件", "有什么要求", "需要什么",
	})
}

func runtimeIntentProtocolCurrentTurnContextAuthorizesResolution(task runtimeIntentTaskJSON, originalText string, sourceTexts []string) bool {
	relation := semanticGateNormalizeRelation(task.RelationToPrevious)
	if relation != "independent" || len(task.SourceRefs) < 2 ||
		!runtimeIntentProtocolTaskOwnsOriginalSource(task, originalText, sourceTexts) {
		return false
	}
	resolvedText := strings.TrimSpace(task.ResolvedText)
	return resolvedText != "" &&
		normalizeRuntimeIntentProtocolAtomicText(resolvedText) != normalizeRuntimeIntentProtocolAtomicText(originalText) &&
		!runtimeIntentAtomicCandidateRequiresContext(resolvedText)
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
