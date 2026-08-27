package executor

import (
	"fmt"
	"strconv"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

// repairRuntimeIntentAtomicKnowledgeTasks narrows a model-produced compound
// hotel-information task back to independent questions already present in the
// current customer turn. It never classifies a new action or invents a task.
func repairRuntimeIntentAtomicKnowledgeTasks(tasks []callbacks.IntentTaskTraceData, currentText string, sourceTexts []string, enforce bool) ([]callbacks.IntentTaskTraceData, int) {
	if !enforce || len(tasks) == 0 {
		return tasks, 0
	}
	candidates := currentTurnTaskCandidates(currentText)
	if len(candidates) <= 1 {
		return tasks, 0
	}

	assignments := make([][]int, len(tasks))
	owners := make([]int, len(candidates))
	for index := range owners {
		owners[index] = -1
	}
	for candidateIndex, candidate := range candidates {
		bestTask := -1
		bestScore := 0
		bestLength := 0
		for taskIndex, task := range tasks {
			score, matchedLength := runtimeIntentAtomicTaskMatch(task, candidate)
			if score > bestScore || (score == bestScore && score > 0 && (bestLength == 0 || matchedLength < bestLength)) {
				bestTask = taskIndex
				bestScore = score
				bestLength = matchedLength
			}
		}
		if bestTask < 0 {
			return tasks, 0
		}
		owners[candidateIndex] = bestTask
		assignments[bestTask] = append(assignments[bestTask], candidateIndex)
	}

	repairable := make([]bool, len(tasks))
	for taskIndex, task := range tasks {
		assigned := assignments[taskIndex]
		if !runtimeIntentSafeCompoundKnowledgeTask(task) || len(assigned) == 0 || !runtimeIntentAtomicTaskCanBeNarrowed(task, candidates, assigned) {
			continue
		}
		rawMatchCount := 0
		for _, candidate := range candidates {
			if score, _ := runtimeIntentAtomicTaskMatch(task, candidate); score > 0 {
				rawMatchCount++
			}
		}
		if len(assigned) > 1 || rawMatchCount > 1 {
			repairable[taskIndex] = true
		}
	}

	repaired := make([]callbacks.IntentTaskTraceData, 0, len(tasks)+len(candidates))
	emitted := make([]bool, len(tasks))
	repairCount := 0
	for candidateIndex, candidate := range candidates {
		taskIndex := owners[candidateIndex]
		if taskIndex < 0 || taskIndex >= len(tasks) {
			return tasks, 0
		}
		if !repairable[taskIndex] {
			if !emitted[taskIndex] {
				repaired = append(repaired, tasks[taskIndex])
				emitted[taskIndex] = true
			}
			continue
		}
		item := tasks[taskIndex]
		candidate = strings.TrimSpace(candidate)
		item.Text = candidate
		item.ResolvedText = candidate
		item.Objective = runtimeIntentAtomicKnowledgeObjective(candidate)
		item.SubIntent = runtimeIntentAtomicKnowledgeSubIntent(item.SubIntent, item.Objective)
		item.Entities = runtimeIntentAtomicEntitiesForCandidate(item.Entities, candidate)
		item.SourceRefs = runtimeIntentAtomicSourceRefsForCandidate(item.SourceRefs, candidate, sourceTexts)
		item.Reason = appendIntentReason(item.Reason, "local atomic repair from compound current-turn task")
		repaired = append(repaired, item)
		emitted[taskIndex] = true
		repairCount++
	}
	for taskIndex, task := range tasks {
		if !emitted[taskIndex] {
			repaired = append(repaired, task)
		}
	}
	if repairCount == 0 {
		return tasks, 0
	}
	return repaired, repairCount
}

func runtimeIntentAtomicTaskCanBeNarrowed(task callbacks.IntentTaskTraceData, candidates []string, assigned []int) bool {
	if normalizeRuntimeKnowledgeQuery(task.Text) != normalizeRuntimeKnowledgeQuery(task.ResolvedText) {
		return false
	}
	for _, candidateIndex := range assigned {
		if candidateIndex < 0 || candidateIndex >= len(candidates) || runtimeIntentAtomicCandidateRequiresContext(candidates[candidateIndex]) {
			return false
		}
	}
	return true
}

func runtimeIntentAtomicCandidateRequiresContext(candidate string) bool {
	compact := normalizeRuntimeKnowledgeQuery(candidate)
	if compact == "" || isDependentIntentTaskClause(candidate) {
		return true
	}
	if len([]rune(compact)) <= 6 && (strings.HasPrefix(compact, "这") || strings.HasPrefix(compact, "那")) {
		return true
	}
	return containsAny(compact, []string{"再说一遍", "重新说", "上一个", "刚才那个", "前面那个", "同样呢"})
}

func runtimeIntentSafeCompoundKnowledgeTask(task callbacks.IntentTaskTraceData) bool {
	if canonicalIntentCode(task.Intent) != "hotel_info" || !task.NeedsKnowledge || task.NeedsResource || task.NeedsTool || task.NeedsHumanRoute || strings.TrimSpace(task.ResourceAction) != "" {
		return false
	}
	return semanticGateNormalizeObjective(task.Objective) == "compound_information" || strings.Contains(strings.ToLower(strings.TrimSpace(task.SubIntent)), "compound")
}

func runtimeIntentAtomicTaskMatch(task callbacks.IntentTaskTraceData, candidate string) (int, int) {
	candidate = normalizeRuntimeKnowledgeQuery(candidate)
	if candidate == "" {
		return 0, 0
	}
	bestScore := 0
	bestLength := 0
	for _, rawText := range []string{task.Text, task.ResolvedText} {
		text := normalizeRuntimeKnowledgeQuery(rawText)
		if text == "" {
			continue
		}
		score := 0
		switch {
		case text == candidate:
			score = 4
		case len([]rune(candidate)) >= 3 && strings.Contains(text, candidate):
			score = 3
		case len([]rune(text)) >= 4 && strings.Contains(candidate, text):
			score = 2
		}
		if score > bestScore || (score == bestScore && score > 0 && (bestLength == 0 || len([]rune(text)) < bestLength)) {
			bestScore = score
			bestLength = len([]rune(text))
		}
	}
	return bestScore, bestLength
}

func runtimeIntentAtomicKnowledgeObjective(text string) string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	switch {
	case strings.Contains(compact, "价格"), strings.Contains(compact, "收费"), strings.Contains(compact, "免费"), strings.Contains(compact, "多少钱"):
		return "price"
	case strings.Contains(compact, "地址"), strings.Contains(compact, "位置"), strings.Contains(compact, "在哪里"), strings.Contains(compact, "在哪"), strings.Contains(compact, "哪里"):
		return "location"
	case strings.Contains(compact, "几瓶"), strings.Contains(compact, "几个"), strings.Contains(compact, "多少瓶"), strings.Contains(compact, "数量"):
		return "quantity"
	case strings.Contains(compact, "几点"), strings.Contains(compact, "多久"), strings.Contains(compact, "什么时候"), strings.Contains(compact, "时间"):
		return "time"
	case strings.Contains(compact, "有没有"), strings.Contains(compact, "是否有"):
		return "availability"
	case strings.Contains(compact, "怎么"), strings.Contains(compact, "如何"):
		return "method"
	case strings.Contains(compact, "是不是"), strings.Contains(compact, "是否"), strings.Contains(compact, "规则"):
		return "policy"
	default:
		return "general_guidance"
	}
}

func runtimeIntentAtomicKnowledgeSubIntent(current string, objective string) string {
	if objective == "location" {
		return "location_info"
	}
	return current
}

func runtimeIntentAtomicEntitiesForCandidate(entities []callbacks.IntentEntityTraceData, candidate string) []callbacks.IntentEntityTraceData {
	compact := normalizeRuntimeKnowledgeQuery(candidate)
	ret := make([]callbacks.IntentEntityTraceData, 0, len(entities))
	for _, entity := range entities {
		entityText := normalizeRuntimeKnowledgeQuery(entity.Text)
		if entityText != "" && strings.Contains(compact, entityText) {
			ret = append(ret, entity)
		}
	}
	return ret
}

func runtimeIntentAtomicSourceRefsForCandidate(existing []string, candidate string, sourceTexts []string) []string {
	if len(sourceTexts) <= 1 {
		return append([]string(nil), existing...)
	}
	candidateText := normalizeRuntimeKnowledgeQuery(candidate)
	matchedRef := ""
	matchCount := 0
	for index, sourceText := range sourceTexts {
		source := normalizeRuntimeKnowledgeQuery(sourceText)
		if source == "" || candidateText == "" {
			continue
		}
		if source == candidateText || strings.Contains(source, candidateText) || strings.Contains(candidateText, source) {
			matchedRef = fmt.Sprintf("U%d", index+1)
			matchCount++
		}
	}
	if matchCount != 1 {
		return append([]string(nil), existing...)
	}
	ret := []string{matchedRef}
	for _, ref := range existing {
		if ref == matchedRef {
			continue
		}
		index := runtimeIntentSourceRefIndex(ref)
		if index >= 0 && index < len(sourceTexts) && !runtimeBurstLineLooksLikeTask(sourceTexts[index]) {
			ret = appendIfMissing(ret, ref)
		}
	}
	return ret
}

func runtimeIntentSourceRefIndex(ref string) int {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "U") {
		return -1
	}
	value, err := strconv.Atoi(strings.TrimPrefix(ref, "U"))
	if err != nil || value <= 0 {
		return -1
	}
	return value - 1
}
