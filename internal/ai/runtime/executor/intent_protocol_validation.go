package executor

import (
	"fmt"
	"regexp"
	"sort"
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
	if err := validateRuntimeIntentInteractionAgainstAdjacentContext(parsed, currentText, context); err != nil {
		return err
	}
	sourceTexts := currentTurnIntentSourceTexts(currentText)
	for taskIndex, task := range parsed.IntentTasks {
		if semanticGateNormalizeResolution(task.ResolutionState) != runtimeIntentResolutionResolvedFromContext {
			continue
		}
		if runtimeIntentProtocolIsConversationRecapTask(task) {
			// A recap task resolves to an operation over the bounded conversation
			// history, not to an entity copied from the adjacent exchange. Source
			// provenance is validated by the base protocol; lexical grounding here
			// would reject the exact contract requested by the Intent prompt.
			continue
		}
		candidate := strings.TrimSpace(task.Text)
		if candidate == "" && len(task.SourceRefs) > 0 {
			primaryIndex := runtimeIntentSourceRefIndex(task.SourceRefs[0])
			if primaryIndex >= 0 && primaryIndex < len(sourceTexts) {
				candidate = sourceTexts[primaryIndex]
			}
		}
		if len(task.SourceRefs) <= 1 && !runtimeIntentAtomicCandidateRequiresContext(candidate) {
			// relationToPrevious and resolutionState select bounded context; they
			// are not a local semantic veto. A self-contained current question can
			// be answered without an adjacent pair even when the model conservatively
			// marked it as context-resolved. The resolved form must still be grounded
			// in the current question so this tolerance cannot introduce a new topic.
			if !runtimeIntentProtocolResolvedReferenceGroundedInText(task, candidate, candidate) {
				return fmt.Errorf("intentTasks[%d] resolvedText is not grounded in its current self-contained question", taskIndex)
			}
			if err := validateRuntimeIntentProtocolResolvedEntities(task, candidate, candidate); err != nil {
				return fmt.Errorf("intentTasks[%d] %w", taskIndex, err)
			}
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

func validateRuntimeIntentInteractionAgainstAdjacentContext(parsed runtimeIntentDetectJSON, currentText string, context runtimeIntentProtocolRepairContext) error {
	previousCustomerText := strings.TrimSpace(context.PreviousCustomerText)
	adjacentServiceReply := runtimeIntentProtocolAdjacentServiceReply(context)
	if previousCustomerText == "" || adjacentServiceReply == "" {
		return nil
	}
	sourceTexts := currentTurnIntentSourceTexts(currentText)
	for taskIndex, task := range parsed.IntentTasks {
		text := strings.TrimSpace(task.Text)
		if text == "" && len(task.SourceRefs) > 0 {
			primaryIndex := runtimeIntentSourceRefIndex(task.SourceRefs[0])
			if primaryIndex >= 0 && primaryIndex < len(sourceTexts) {
				text = sourceTexts[primaryIndex]
			}
		}
		answerRejectedIntent := canonicalIntentCode(task.Intent) == "human_complaint_risk" && strings.TrimSpace(task.SubIntent) == "answer_rejected"
		answerRejectedRelation := semanticGateNormalizeRelation(task.RelationToPrevious) == "answer_rejected"
		if answerRejectedIntent || answerRejectedRelation {
			if !answerRejectedIntent || !answerRejectedRelation {
				return fmt.Errorf("intentTasks[%d] answer_rejected classification and relation must agree", taskIndex)
			}
			if !runtimeIntentProtocolAnswerRejectionGrounded(task, text, context) {
				return fmt.Errorf("intentTasks[%d] answer_rejected is not grounded in the adjacent AI answer", taskIndex)
			}
			continue
		}
		if canonicalIntentCode(task.Intent) != "interaction" || runtimeIntentProtocolIsConversationRecapTask(task) {
			continue
		}
		if runtimeIntentProtocolExplicitlyRejectsAdjacentAIAnswer(task, text, context) {
			return fmt.Errorf("intentTasks[%d] explicit rejection of the adjacent AI answer cannot remain interaction", taskIndex)
		}
		if runtimeIntentProtocolExplicitRepeatReference(text) &&
			runtimeIntentProtocolPreviousTurnHasHotelBusinessTarget(previousCustomerText) &&
			runtimeIntentProtocolPreviousTurnCanOwnRepeat(text, previousCustomerText) {
			return fmt.Errorf("intentTasks[%d] completed-answer repeat cannot remain interaction", taskIndex)
		}
		if runtimeIntentProtocolCancellationNeedsPreviousRelation(task, text) {
			return fmt.Errorf("intentTasks[%d] cancellation of the adjacent task must use cancel_previous", taskIndex)
		}
		if !runtimeIntentProtocolAdjacentServiceReplyAsksForSlot(adjacentServiceReply) ||
			runtimeIntentTaskHasSelfContainedBusinessRequest(task) {
			continue
		}
		if runtimeIntentProtocolIsExplicitPureInteraction(task, text) {
			continue
		}
		if runtimeIntentProtocolInteractionAnswersAdjacentSlot(task, text, adjacentServiceReply) {
			return fmt.Errorf("intentTasks[%d] answer to adjacent service clarification cannot remain interaction", taskIndex)
		}
	}
	return nil
}

func runtimeIntentProtocolExplicitlyRejectsAdjacentAIAnswer(task runtimeIntentTaskJSON, text string, context runtimeIntentProtocolRepairContext) bool {
	return runtimeIntentProtocolHasAnswerRejectionSignal(strings.Join([]string{text, task.ResolvedText}, " ")) &&
		runtimeIntentProtocolAnswerRejectionGrounded(task, text, context)
}

func runtimeIntentProtocolAnswerRejectionGrounded(task runtimeIntentTaskJSON, text string, context runtimeIntentProtocolRepairContext) bool {
	if strings.TrimSpace(context.PreviousCustomerText) == "" || strings.TrimSpace(context.AdjacentAIReply) == "" {
		return false
	}
	current := strings.Join([]string{text, task.ResolvedText}, " ")
	compact := strings.Trim(normalizeRuntimeKnowledgeQuery(current), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	if compact == "" || !runtimeIntentProtocolHasAnswerRejectionSignal(compact) {
		return false
	}
	if runtimeIntentProtocolDirectlyReferencesAdjacentAnswer(compact) {
		return true
	}
	return runtimeIntentProtocolAnswerRejectionSharesTopic(
		compact,
		context.PreviousCustomerText+" "+context.AdjacentAIReply,
	)
}

func runtimeIntentProtocolHasAnswerRejectionSignal(text string) bool {
	compact := strings.Trim(normalizeRuntimeKnowledgeQuery(text), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	if compact == "" {
		return false
	}
	if containsAny(compact, []string{
		"答非所问", "没有回答我的问题", "没回答我的问题", "没有回答问题", "没回答问题",
		"你回答错了", "你回复错了", "你答错了", "你说错了", "回答不对", "回复不对", "说的不对",
		"前后矛盾", "说法矛盾", "你刚才不是说", "你前面不是说", "你不是说",
		"我问的是", "我说的是", "不是我问的", "不是这个问题",
		"还是没解决", "仍然没解决", "根本没解决", "问题还没解决", "问题没有解决",
	}) {
		return true
	}
	return containsAny(compact, []string{
		"客服说", "前台说", "人工客服说", "真人客服说", "酒店同事说", "门店同事说", "管家说",
		"客服告诉我", "前台告诉我", "工作人员告诉我", "现场明明", "实际明明",
	})
}

func runtimeIntentProtocolDirectlyReferencesAdjacentAnswer(compact string) bool {
	return containsAny(compact, []string{
		"答非所问", "没有回答我的问题", "没回答我的问题", "没有回答问题", "没回答问题",
		"你回答错了", "你回复错了", "你答错了", "你说错了", "回答不对", "回复不对", "说的不对",
		"前后矛盾", "说法矛盾", "还是没解决", "仍然没解决", "根本没解决", "问题还没解决", "问题没有解决",
	})
}

func runtimeIntentProtocolAnswerRejectionSharesTopic(current string, context string) bool {
	current = normalizeRuntimeKnowledgeQuery(current)
	context = normalizeRuntimeKnowledgeQuery(context)
	if current == "" || context == "" {
		return false
	}
	if runtimeIntentProtocolAnswerRejectionSharesAspect(current, context) {
		return true
	}
	for _, phrase := range []string{
		"你刚才不是说", "你前面不是说", "你不是说", "我问的是", "我说的是", "不是我问的", "不是这个问题",
		"客服告诉我", "前台告诉我", "工作人员告诉我", "人工客服说", "真人客服说", "酒店同事说", "门店同事说",
		"客服说", "前台说", "管家说", "现场明明", "实际明明", "刚才", "前面", "之前",
	} {
		current = strings.ReplaceAll(current, normalizeRuntimeKnowledgeQuery(phrase), "")
	}
	current = strings.Trim(current, "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	for size := 4; size >= 2; size-- {
		for _, anchor := range runtimeIntentProtocolRuneWindows(current, size) {
			if runtimeIntentProtocolGenericAnswerRejectionAnchor(anchor) || !strings.Contains(context, anchor) {
				continue
			}
			return true
		}
	}
	return false
}

func runtimeIntentProtocolAnswerRejectionSharesAspect(current string, context string) bool {
	aspectGroups := [][]string{
		{"步行", "走路", "开车", "驾车", "打车", "骑行", "公交", "地铁", "换乘", "路线", "怎么走", "多远", "公里", "分钟"},
		{"微信转账", "支付宝", "银行卡", "现金", "刷卡", "付款", "支付", "转账"},
	}
	for _, group := range aspectGroups {
		if containsAny(current, group) && containsAny(context, group) {
			return true
		}
	}
	return false
}

func runtimeIntentProtocolGenericAnswerRejectionAnchor(anchor string) bool {
	anchor = normalizeRuntimeKnowledgeQuery(anchor)
	if anchor == "" || runtimeIntentProtocolGenericReferenceAnchor(anchor) {
		return true
	}
	return containsAny(anchor, []string{
		"客服", "前台", "同事", "人工", "真人", "现场", "实际", "明明", "刚才", "前面", "之前",
		"回答", "回复", "问题", "你说", "我问", "我说", "可以", "不可以", "不能", "不是", "就是", "有没", "没有", "是否",
	})
}

func runtimeIntentProtocolCancellationNeedsPreviousRelation(task runtimeIntentTaskJSON, text string) bool {
	subIntent := strings.ToLower(strings.TrimSpace(task.SubIntent))
	if subIntent != "cancel" && subIntent != "cancellation" {
		return false
	}
	compact := strings.Trim(normalizeRuntimeKnowledgeQuery(text), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	if compact == "" || !runtimeIntentProtocolIsExplicitCancellationInteraction(task, compact) {
		return false
	}
	return semanticGateNormalizeRelation(task.RelationToPrevious) != "cancel_previous"
}

func runtimeIntentProtocolInteractionAnswersAdjacentSlot(task runtimeIntentTaskJSON, text string, reply string) bool {
	relation := semanticGateNormalizeRelation(task.RelationToPrevious)
	if relation == "clarification_answer" ||
		(semanticGateNormalizeResolution(task.ResolutionState) == runtimeIntentResolutionResolvedFromContext && semanticGateRelationUsesPrevious(relation)) {
		return true
	}
	compactText := normalizeRuntimeKnowledgeQuery(text)
	compactReply := normalizeRuntimeKnowledgeQuery(reply)
	if compactText == "" || compactReply == "" {
		return false
	}
	if containsAny(compactReply, []string{"房间", "房号", "门牌号", "几号房"}) &&
		runtimeIntentProtocolRoomNumberSlotValuePattern.MatchString(compactText) {
		return true
	}
	if containsAny(compactReply, []string{"是否", "有没有", "有无", "可以吗", "能吗", "确认", "取消"}) &&
		containsAny(compactText, []string{"是的", "对的", "没错", "不是", "不对", "有的", "没有", "可以", "不可以", "能", "不能", "确认", "取消"}) {
		return true
	}
	if containsAny(compactReply, []string{"姓名", "名字", "怎么称呼"}) {
		return runtimeIntentProtocolNameSlotValuePattern.MatchString(compactText) || runtimeIntentProtocolTaskHasEntityType(task, "name", "guest_name", "person")
	}
	if containsAny(compactReply, []string{"几点", "时间", "什么时候"}) {
		return runtimeIntentProtocolTimeSlotValuePattern.MatchString(compactText) || runtimeIntentProtocolTaskHasEntityType(task, "time", "datetime")
	}
	if containsAny(compactReply, []string{"什么房型", "哪个房型", "哪种房型", "哪间房", "哪个房间"}) {
		return (runtimeIntentProtocolTaskDeclaresClarification(task) && !runtimeIntentProtocolOriginalIsIndependentQuestion(text) && runtimeIntentProtocolNameSlotValuePattern.MatchString(strings.TrimSuffix(compactText, "的"))) ||
			runtimeIntentProtocolTaskHasEntityType(task, "room_type", "room")
	}
	if containsAny(compactReply, []string{"口味", "偏好", "想吃什么", "喜欢什么"}) {
		return containsAny(compactText, []string{"口味", "偏好", "想吃", "喜欢"}) ||
			runtimeIntentProtocolTaskHasEntityType(task, "preference", "flavor", "taste")
	}
	if containsAny(compactReply, []string{"哪个门店", "哪家店", "什么位置", "哪个位置", "地址是", "在哪里", "在哪"}) {
		return runtimeIntentProtocolTaskHasEntityType(task, "location", "store", "address")
	}
	return false
}

func runtimeIntentProtocolTaskHasEntityType(task runtimeIntentTaskJSON, expected ...string) bool {
	for _, entity := range task.Entities {
		entityType := strings.ToLower(strings.TrimSpace(entity.Type))
		for _, candidate := range expected {
			if entityType == candidate {
				return true
			}
		}
	}
	return false
}

func runtimeIntentProtocolTaskDeclaresClarification(task runtimeIntentTaskJSON) bool {
	switch strings.ToLower(strings.TrimSpace(task.SubIntent)) {
	case "clarify", "clarification", "clarification_answer", "missing_detail", "slot_answer", "provide_detail":
		return true
	default:
		return false
	}
}

func runtimeIntentProtocolPreviousTurnCanOwnRepeat(repeatText string, previousCustomerText string) bool {
	previousCandidates := currentTurnTaskCandidates(previousCustomerText)
	if len(previousCandidates) == 0 {
		return false
	}
	if len(previousCandidates) == 1 {
		previous := strings.TrimSpace(previousCandidates[0])
		if previous == "" || runtimeIntentAtomicCandidateRequiresContext(previous) {
			return false
		}
		anchor := runtimeIntentProtocolRepeatReferenceAnchor(repeatText)
		if anchor == "" {
			return true
		}
		previousText := runtimeIntentProtocolRepeatReferenceComparableText(previous)
		return strings.Contains(previousText, anchor) || strings.Contains(anchor, previousText)
	}

	anchor := runtimeIntentProtocolRepeatReferenceAnchor(repeatText)
	if len([]rune(anchor)) < 2 {
		return false
	}
	matches := 0
	for _, candidate := range previousCandidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || runtimeIntentAtomicCandidateRequiresContext(candidate) {
			continue
		}
		candidateText := runtimeIntentProtocolRepeatReferenceComparableText(candidate)
		if strings.Contains(candidateText, anchor) || strings.Contains(anchor, candidateText) {
			matches++
		}
	}
	return matches == 1
}

func runtimeIntentProtocolPreviousTurnHasHotelBusinessTarget(previousCustomerText string) bool {
	for _, candidate := range currentTurnTaskCandidates(previousCustomerText) {
		compact := normalizeRuntimeKnowledgeQuery(candidate)
		if compact != "" && runtimeIntentProtocolTextHasHotelBusinessAnchor(compact) {
			return true
		}
	}
	return false
}

func runtimeIntentProtocolIsExplicitPureInteraction(task runtimeIntentTaskJSON, text string) bool {
	if task.NeedsKnowledge || task.NeedsResource || task.NeedsTool || task.NeedsHumanRoute || strings.TrimSpace(task.ResourceAction) != "" {
		return false
	}
	compact := strings.Trim(normalizeRuntimeKnowledgeQuery(text), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	if compact == "" {
		return false
	}
	if runtimeIntentProtocolIsExplicitCancellationInteraction(task, compact) {
		return true
	}
	if runtimeIntentProtocolOriginalIsIndependentQuestion(text) || runtimeIntentStandaloneTaskLabel(text) {
		return false
	}
	if containsAny(compact, []string{"是的", "对的", "没错", "不是", "不对"}) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(task.SubIntent)) {
	case "thanks", "thank_you":
		return runtimeIntentProtocolContainsOnlyInteractionPhrases(compact, []string{
			"谢谢您的帮助", "谢谢你的帮助", "非常感谢", "太感谢了", "感谢您", "感谢你", "谢谢您", "谢谢你", "辛苦了", "多谢", "谢谢", "感谢", "辛苦",
		})
	case "acknowledgement", "acknowledgment":
		return runtimeIntentProtocolContainsOnlyInteractionPhrases(compact, []string{
			"已经知道了", "已经明白了", "已经了解了", "可以了", "收到了", "知道了", "明白了", "了解了", "好的", "好", "行",
		})
	case "greeting":
		return runtimeIntentProtocolContainsOnlyInteractionPhrases(compact, []string{"早上好", "下午好", "晚上好", "你好", "您好"})
	case "farewell":
		return runtimeIntentProtocolContainsOnlyInteractionPhrases(compact, []string{"再见", "拜拜", "晚安"})
	case "frustration", "insult_complaint":
		return runtimeIntentProtocolContainsOnlyInteractionPhrases(compact, []string{
			"你烦不烦", "烦不烦", "烦死了", "太烦了", "真烦", "太慢了", "怎么这么慢", "回复太慢", "服务太差", "态度太差", "听不懂人话", "答非所问", "什么玩意", "真无语", "无语", "垃圾", "傻逼",
		})
	}
	if !strings.EqualFold(strings.TrimSpace(task.Objective), "social") {
		return false
	}
	return runtimeIntentProtocolContainsOnlyInteractionPhrases(compact, []string{
		"谢谢您的帮助", "谢谢你的帮助", "非常感谢", "太感谢了", "已经解决了", "问题解决了", "不需要了", "不用了", "收到了", "知道了", "明白了", "了解了", "可以了",
		"早上好", "下午好", "晚上好", "感谢您", "感谢你", "谢谢您", "谢谢你", "辛苦了", "取消吧", "算了吧", "没事了",
		"谢谢", "感谢", "辛苦", "你好", "您好", "再见", "拜拜", "晚安", "不用", "不需要", "算了", "取消", "没事", "解决了", "收到", "知道", "明白", "了解", "好的", "好", "行",
	})
}

func runtimeIntentProtocolIsExplicitCancellationInteraction(task runtimeIntentTaskJSON, compact string) bool {
	subIntent := strings.ToLower(strings.TrimSpace(task.SubIntent))
	if subIntent != "cancel" && subIntent != "cancellation" &&
		semanticGateNormalizeRelation(task.RelationToPrevious) != "cancel_previous" {
		return false
	}
	return runtimeIntentProtocolContainsOnlyInteractionPhrases(compact, []string{
		"已经解决了", "问题解决了", "已经解决", "问题解决", "不需要了", "不用了", "没事了", "取消吧", "算了吧", "不需要", "不用", "算了", "取消", "没事", "解决了", "解决",
		"是的", "对的", "没错", "不是", "不对",
	})
}

func runtimeIntentProtocolContainsOnlyInteractionPhrases(compact string, phrases []string) bool {
	remaining := strings.TrimSpace(compact)
	for _, phrase := range phrases {
		remaining = strings.ReplaceAll(remaining, normalizeRuntimeKnowledgeQuery(phrase), "")
	}
	remaining = strings.Trim(remaining, "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了哟哦诶额")
	return remaining == ""
}

func runtimeIntentProtocolAdjacentServiceReplyAsksForSlot(reply string) bool {
	compact := normalizeRuntimeKnowledgeQuery(reply)
	if compact == "" || runtimeIntentProtocolIsGenericOpenHelpQuestion(compact) {
		return false
	}
	if strings.Contains(compact, "吗") && containsAny(compact, []string{"是问", "想问", "指的是", "意思是", "说的是", "确认一下"}) {
		return true
	}
	if !strings.ContainsAny(reply, "?？") && !containsAny(compact, []string{"请问", "方便说", "告诉我", "说下", "提供一下", "回复"}) {
		return false
	}
	return containsAny(compact, []string{
		"哪个", "哪种", "什么", "多少", "几号", "几点", "哪里", "在哪", "房间", "房号", "姓名", "名字",
		"口味", "偏好", "条件", "范围", "选项", "是否", "有没有", "有无", "可以吗", "能吗", "确认", "取消",
	})
}

func runtimeIntentProtocolIsGenericOpenHelpQuestion(compact string) bool {
	for _, prefix := range []string{"您好", "你好", "请问"} {
		compact = strings.TrimPrefix(compact, prefix)
	}
	compact = strings.TrimRight(compact, "吗呢啊呀")
	switch compact {
	case "有什么可以帮您", "有什么能帮您", "需要什么帮助",
		"您想了解什么", "你想了解什么", "您想问什么", "你想问什么", "今天想聊点什么", "想聊点什么",
		"您还有什么想了解的", "你还有什么想了解的", "还有什么想了解的", "有什么想了解的",
		"您还有什么想咨询的", "你还有什么想咨询的", "还有什么想咨询的", "有什么想咨询的":
		return true
	}
	if containsAny(compact, []string{"房型", "房间", "房号", "姓名", "名字", "口味", "偏好", "条件", "范围", "选项", "几点", "时间", "哪里", "在哪"}) {
		return false
	}
	hasOpenQuestion := containsAny(compact, []string{"还有什么", "有什么", "还需要", "是否还需要"})
	hasHelpTopic := containsAny(compact, []string{"需要", "帮助", "了解", "咨询", "想问", "想聊", "问题"})
	return hasOpenQuestion && hasHelpTopic
}

var (
	runtimeIntentProtocolRoomNumberSlotValuePattern = regexp.MustCompile(`(?:^|[^0-9])[0-9]{3,6}(?:房|房间|号)?(?:[^0-9]|$)`)
	runtimeIntentProtocolNameSlotValuePattern       = regexp.MustCompile(`^[\p{Han}A-Za-z·]{2,20}$`)
	runtimeIntentProtocolTimeSlotValuePattern       = regexp.MustCompile(`(?:[0-9]{1,2}[:：][0-9]{2}|(?:凌晨|早上|上午|中午|下午|傍晚|晚上|夜里)?(?:[0-9]{1,2}|[零〇一二三四五六七八九十两]{1,3})点(?:半|[0-9]{1,2}分)?)`)
)

func runtimeIntentProtocolIsConversationRecapTask(task runtimeIntentTaskJSON) bool {
	if canonicalIntentCode(task.Intent) != "interaction" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(task.SubIntent)) {
	case "conversation_recap", "conversation_summary", "conversation_history", "recap":
		return true
	default:
		return false
	}
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
		relation := semanticGateNormalizeRelation(task.RelationToPrevious)
		if !semanticGateValidRelation(relation) {
			return fmt.Errorf("intentTasks[%d].relationToPrevious is missing or invalid", index)
		}
		resolution := semanticGateNormalizeResolution(task.ResolutionState)
		if !semanticGateValidResolution(resolution) {
			return fmt.Errorf("intentTasks[%d].resolutionState is missing or invalid", index)
		}
		if canonicalIntentCode(task.Intent) == "interaction" && !runtimeIntentProtocolIsConversationRecapTask(task) &&
			runtimeIntentInteractionContradictsBusinessProtocol(task, resolution) {
			return fmt.Errorf("intentTasks[%d] interaction carries a clear business information target", index)
		}
	}
	if requireSourceRefs && len(sourceTexts) > 0 {
		if err := validateRuntimeIntentProtocolModelOwnedSources(tasks, sourceTexts); err != nil {
			return err
		}
		if err := validateRuntimeIntentProtocolAtomicCoverageBySource(tasks, sourceTexts); err != nil {
			return err
		}
	}
	return nil
}

// validateRuntimeIntentProtocolModelOwnedSources verifies provenance, source
// ownership and order. IntentDetect alone decides how many tasks exist and
// where their semantic boundaries are; local code only requires every physical
// current-turn source to be explicitly owned by at least one model task.
func validateRuntimeIntentProtocolModelOwnedSources(tasks []runtimeIntentTaskJSON, sourceTexts []string) error {
	lastPrimarySource := -1
	referencedSources := make([]bool, len(sourceTexts))
	lastUniqueOffsetBySource := make(map[int]int, len(sourceTexts))
	executableTasksBySource := make(map[int]int, len(sourceTexts))
	for _, task := range tasks {
		if len(task.SourceRefs) == 0 || !runtimeIntentProtocolTaskHasExecutableBusiness(task) {
			continue
		}
		primarySource := runtimeIntentSourceRefIndex(task.SourceRefs[0])
		if primarySource >= 0 && primarySource < len(sourceTexts) {
			executableTasksBySource[primarySource]++
		}
	}

	for taskIndex, task := range tasks {
		primarySource := runtimeIntentSourceRefIndex(task.SourceRefs[0])
		if primarySource < lastPrimarySource {
			return fmt.Errorf("intentTasks[%d] is out of current-turn source order", taskIndex)
		}
		lastPrimarySource = primarySource
		if primarySource >= 0 && primarySource < len(sourceTexts) {
			offset, unique := runtimeIntentProtocolUniqueTaskOffset(sourceTexts[primarySource], task.Text)
			if executableTasksBySource[primarySource] > 1 && runtimeIntentProtocolTaskHasExecutableBusiness(task) && !unique {
				return fmt.Errorf("intentTasks[%d].text cannot be uniquely located in its multi-task current source", taskIndex)
			}
			if unique {
				if previousOffset, exists := lastUniqueOffsetBySource[primarySource]; exists && offset < previousOffset {
					return fmt.Errorf("intentTasks[%d] is out of current-turn source text order", taskIndex)
				}
				lastUniqueOffsetBySource[primarySource] = offset
			}
		}
		for _, ref := range task.SourceRefs {
			sourceIndex := runtimeIntentSourceRefIndex(ref)
			if sourceIndex >= 0 && sourceIndex < len(referencedSources) {
				referencedSources[sourceIndex] = true
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

func runtimeIntentProtocolUniqueTaskOffset(sourceText string, taskText string) (int, bool) {
	source := normalizeRuntimeKnowledgeQuery(sourceText)
	task := normalizeRuntimeIntentProtocolAtomicText(taskText)
	if source == "" || task == "" {
		return 0, false
	}
	matchedOffset := -1
	matchedCandidates := 0
	searchFrom := 0
	for _, candidate := range currentTurnTaskCandidates(sourceText) {
		candidateText := normalizeRuntimeIntentProtocolAtomicText(candidate)
		if candidateText == "" {
			continue
		}
		relative := strings.Index(source[searchFrom:], candidateText)
		if relative < 0 {
			continue
		}
		offset := searchFrom + relative
		if candidateText == task {
			matchedOffset = offset
			matchedCandidates++
		}
		searchFrom = offset + len(candidateText)
	}
	if matchedCandidates == 1 {
		return matchedOffset, true
	}
	first := strings.Index(source, task)
	if first < 0 || strings.Index(source[first+len(task):], task) >= 0 {
		return 0, false
	}
	return first, true
}

func runtimeIntentProtocolExactTaskKey(task runtimeIntentTaskJSON) string {
	entities := make([]string, 0, len(task.Entities))
	for _, entity := range task.Entities {
		entities = append(entities, strings.ToLower(strings.TrimSpace(entity.Type))+":"+normalizeRuntimeKnowledgeQuery(entity.Text))
	}
	parts := []string{
		canonicalIntentCode(task.Intent),
		strings.ToLower(strings.TrimSpace(task.SubIntent)),
		semanticGateNormalizeObjectiveMetadata(task.Objective),
		semanticGateNormalizeRelation(task.RelationToPrevious),
		semanticGateNormalizeResolution(task.ResolutionState),
		normalizeRuntimeIntentProtocolAtomicText(task.Text),
		normalizeRuntimeIntentProtocolAtomicText(task.ResolvedText),
		strings.Join(entities, ","),
		fmt.Sprintf("%t|%t|%t|%t", task.NeedsKnowledge, task.NeedsResource, task.NeedsTool, task.NeedsHumanRoute),
		strings.TrimSpace(task.ResourceAction),
		runtimeIntentProtocolSourceOwnershipKey(task),
	}
	return strings.Join(parts, "|")
}

func runtimeIntentProtocolSourceOwnershipKey(task runtimeIntentTaskJSON) string {
	if len(task.SourceRefs) == 0 {
		return ""
	}
	primary := strings.ToUpper(strings.TrimSpace(task.SourceRefs[0]))
	if semanticGateNormalizeResolution(task.ResolutionState) != runtimeIntentResolutionResolvedFromContext || len(task.SourceRefs) == 1 {
		return primary
	}
	contextRefs := make([]string, 0, len(task.SourceRefs)-1)
	seen := make(map[string]struct{}, len(task.SourceRefs)-1)
	for _, ref := range task.SourceRefs[1:] {
		normalized := strings.ToUpper(strings.TrimSpace(ref))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		contextRefs = append(contextRefs, normalized)
	}
	if len(contextRefs) == 0 {
		return primary
	}
	sort.Strings(contextRefs)
	return primary + "<-" + strings.Join(contextRefs, ",")
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
			if len(task.SourceRefs) == 0 || runtimeIntentSourceRefIndex(task.SourceRefs[0]) != sourceIndex {
				continue
			}
			sourceTasks = append(sourceTasks, task)
		}
		if len(candidates) == 1 {
			if len(sourceTasks) == 0 {
				return fmt.Errorf("current-turn source U%d does not cover atomic question 1 of 1: %s", sourceIndex+1, strings.TrimSpace(candidates[0]))
			}
			ownerKeys := make(map[string]struct{}, len(sourceTasks))
			disjointModelSpans := len(sourceTasks) > 1 && runtimeIntentProtocolTasksHaveDisjointSourceSpans(sourceText, sourceTasks)
			for taskIndex, task := range sourceTasks {
				groundingCandidate := candidates[0]
				if disjointModelSpans {
					groundingCandidate = task.Text
				}
				if !runtimeIntentProtocolSingleSourceTaskGroundedInCandidate(task, groundingCandidate, sourceText) {
					return fmt.Errorf("current-turn source U%d task %d is not grounded in atomic question 1 of 1: %s", sourceIndex+1, taskIndex+1, strings.TrimSpace(candidates[0]))
				}
				if taskIndex == 0 {
					ownerKeys[runtimeIntentProtocolTaskCandidateOwnershipKey(task, candidates[0])] = struct{}{}
					continue
				}
				if !disjointModelSpans {
					return fmt.Errorf("current-turn source U%d atomic question 1 of 1 has duplicate task ownership", sourceIndex+1)
				}
				key := runtimeIntentProtocolTaskCandidateOwnershipKey(task, candidates[0])
				if _, exists := ownerKeys[key]; exists {
					return fmt.Errorf("current-turn source U%d atomic question 1 of 1 has duplicate task ownership", sourceIndex+1)
				}
				ownerKeys[key] = struct{}{}
			}
			continue
		}

		covered := make([]bool, len(candidates))
		usedTasks := make([]bool, len(sourceTasks))
		ownerKeys := make([]map[string]struct{}, len(candidates))
		ownerCounts := make([]int, len(candidates))
		ownerTasks := make([][]runtimeIntentTaskJSON, len(candidates))

		for taskIndex, task := range sourceTasks {
			if semanticGateNormalizeObjective(task.Objective) != "compound_information" {
				continue
			}
			candidateIndexes := runtimeIntentProtocolCompoundCoveredCandidateIndexes(task, candidates)
			if len(candidateIndexes) == 0 || !runtimeIntentProtocolCompoundCandidatesAreRelated(task, sourceText, candidates, candidateIndexes) {
				continue
			}
			if len(candidateIndexes) > 1 && runtimeIntentProtocolTaskRequestsSeparateAnswers(task) {
				continue
			}
			for _, candidateIndex := range candidateIndexes {
				covered[candidateIndex] = true
				ownerCounts[candidateIndex]++
				ownerTasks[candidateIndex] = append(ownerTasks[candidateIndex], task)
				if ownerKeys[candidateIndex] == nil {
					ownerKeys[candidateIndex] = make(map[string]struct{}, 1)
				}
				ownerKeys[candidateIndex][runtimeIntentProtocolTaskCandidateOwnershipKey(task, candidates[candidateIndex])] = struct{}{}
			}
			usedTasks[taskIndex] = true
		}

		lastCandidateIndex := 0
		for taskIndex, task := range sourceTasks {
			if usedTasks[taskIndex] {
				continue
			}
			matchedCandidate := -1
			bestScore := 0
			ambiguousBest := false
			for candidateIndex := lastCandidateIndex; candidateIndex < len(candidates); candidateIndex++ {
				score := runtimeIntentProtocolTaskCandidateGroundingScore(task, candidates[candidateIndex])
				if score <= 0 || score < bestScore {
					continue
				}
				if score == bestScore {
					ambiguousBest = true
					continue
				}
				matchedCandidate = candidateIndex
				bestScore = score
				ambiguousBest = false
			}
			if ambiguousBest {
				return fmt.Errorf("current-turn source U%d task %d cannot be uniquely grounded in atomic questions", sourceIndex+1, taskIndex+1)
			}
			if matchedCandidate < 0 {
				return fmt.Errorf("current-turn source U%d has an extra executable business task %d", sourceIndex+1, taskIndex+1)
			}
			key := runtimeIntentProtocolTaskCandidateOwnershipKey(task, candidates[matchedCandidate])
			if ownerCounts[matchedCandidate] > 0 {
				owners := append([]runtimeIntentTaskJSON(nil), ownerTasks[matchedCandidate]...)
				owners = append(owners, task)
				if !runtimeIntentProtocolTasksHaveDisjointSourceSpans(candidates[matchedCandidate], owners) {
					return fmt.Errorf("current-turn source U%d atomic question %d of %d has duplicate task ownership", sourceIndex+1, matchedCandidate+1, len(candidates))
				}
				if _, exists := ownerKeys[matchedCandidate][key]; exists {
					return fmt.Errorf("current-turn source U%d atomic question %d of %d has duplicate task ownership", sourceIndex+1, matchedCandidate+1, len(candidates))
				}
			}
			if ownerKeys[matchedCandidate] == nil {
				ownerKeys[matchedCandidate] = make(map[string]struct{}, 1)
			}
			ownerKeys[matchedCandidate][key] = struct{}{}
			ownerCounts[matchedCandidate]++
			ownerTasks[matchedCandidate] = append(ownerTasks[matchedCandidate], task)
			covered[matchedCandidate] = true
			usedTasks[taskIndex] = true
			lastCandidateIndex = matchedCandidate
		}

		for candidateIndex, isCovered := range covered {
			if !isCovered {
				return fmt.Errorf("current-turn source U%d does not cover atomic question %d of %d: %s", sourceIndex+1, candidateIndex+1, len(candidates), strings.TrimSpace(candidates[candidateIndex]))
			}
		}
		for taskIndex, used := range usedTasks {
			if !used {
				return fmt.Errorf("current-turn source U%d has an extra executable business task %d", sourceIndex+1, taskIndex+1)
			}
		}
	}
	return nil
}

func runtimeIntentProtocolTaskRequestsSeparateAnswers(task runtimeIntentTaskJSON) bool {
	return containsAny(normalizeRuntimeKnowledgeQuery(task.Text+" "+task.ResolvedText), []string{"分别", "各自", "逐项"})
}

func runtimeIntentProtocolTasksHaveDisjointSourceSpans(sourceText string, tasks []runtimeIntentTaskJSON) bool {
	source := normalizeRuntimeKnowledgeQuery(sourceText)
	if source == "" || len(tasks) <= 1 {
		return false
	}
	previousEnd := -1
	for _, task := range tasks {
		text := normalizeRuntimeIntentProtocolAtomicText(task.Text)
		if text == "" || isRuntimeIntentOutputConstraintClause(task.Text) {
			return false
		}
		start := strings.Index(source, text)
		if start < 0 || strings.Index(source[start+len(text):], text) >= 0 || start < previousEnd {
			return false
		}
		previousEnd = start + len(text)
	}
	for index := 1; index < len(tasks); index++ {
		left := tasks[index-1].Text
		right := tasks[index].Text
		if !runtimeIntentProtocolTaskHasExplicitRequestWording(tasks[index-1]) &&
			runtimeIntentStandaloneTaskLabel(left) &&
			!runtimeIntentClauseHasSelfContainedQuestion(compactRuntimeIntentClause(left)) &&
			(len([]rune(runtimeIntentProtocolAtomicTopicCore(right))) < 2 || runtimeIntentProtocolDependentAspectCandidate(right)) {
			return false
		}
	}
	return true
}

func runtimeIntentProtocolTaskHasExplicitRequestWording(task runtimeIntentTaskJSON) bool {
	compact := normalizeRuntimeKnowledgeQuery(task.Text)
	return (semanticGateNormalizeObjective(task.Objective) == "action_request" && runtimeIntentActionRequestHasTarget(compact)) ||
		runtimeIntentTaskHasExplicitRequestWording(compact)
}

func runtimeIntentProtocolSingleSourceTaskGroundedInCandidate(task runtimeIntentTaskJSON, candidate string, _ string) bool {
	return runtimeIntentProtocolTaskCandidateGroundingScore(task, candidate) > 0
}

func runtimeIntentProtocolTaskCandidateGroundingScore(task runtimeIntentTaskJSON, candidate string) int {
	expectedObjective := runtimeIntentAtomicKnowledgeObjective(candidate)
	actualObjective := semanticGateNormalizeObjective(task.Objective)
	taskText := normalizeRuntimeIntentProtocolAtomicText(task.Text)
	candidateText := normalizeRuntimeIntentProtocolAtomicText(candidate)
	if taskText != "" && candidateText != "" && strings.Contains(candidateText, taskText) {
		expectedObjective = runtimeIntentAtomicKnowledgeObjective(task.Text)
	}
	literalExact := taskText != "" && taskText == candidateText
	if !(canonicalIntentCode(task.Intent) == "interaction" && literalExact) &&
		!runtimeIntentProtocolAtomicObjectivesCompatible(expectedObjective, actualObjective) {
		return 0
	}
	if !runtimeIntentProtocolTaskResolvedTextGroundedInCandidate(task, candidate) {
		return 0
	}
	if taskText != "" && candidateText != "" {
		if taskText == candidateText {
			return 10000
		}
		if strings.Contains(candidateText, taskText) {
			return 9000 + len([]rune(taskText))
		}
	}
	bestScore := 0
	for _, entity := range task.Entities {
		anchor := runtimeIntentProtocolEntityAnchor(entity)
		if runtimeIntentConcreteEntityText(anchor) && strings.Contains(normalizeRuntimeKnowledgeQuery(candidate), anchor) {
			score := 7000 + len([]rune(anchor))
			if score > bestScore {
				bestScore = score
			}
		}
	}
	candidateCore := runtimeIntentProtocolAtomicTopicCore(candidate)
	if len([]rune(candidateCore)) < 2 {
		return bestScore
	}
	for _, text := range runtimeIntentProtocolTaskTextVariants(task) {
		taskCore := runtimeIntentProtocolAtomicTopicCore(text)
		if len([]rune(taskCore)) < 2 {
			continue
		}
		if strings.Contains(taskCore, candidateCore) || strings.Contains(candidateCore, taskCore) {
			score := 6000 + min(len([]rune(taskCore)), len([]rune(candidateCore)))
			if score > bestScore {
				bestScore = score
			}
		}
		for size := 4; size >= 2; size-- {
			for _, anchor := range runtimeIntentProtocolRuneWindows(candidateCore, size) {
				if runtimeIntentProtocolGenericSingleTaskTopicAnchor(anchor) || !strings.Contains(taskCore, anchor) {
					continue
				}
				score := 4000 + size
				if score > bestScore {
					bestScore = score
				}
			}
		}
	}
	return bestScore
}

func runtimeIntentProtocolTaskResolvedTextGroundedInCandidate(task runtimeIntentTaskJSON, candidate string) bool {
	if semanticGateNormalizeResolution(task.ResolutionState) == runtimeIntentResolutionResolvedFromContext ||
		runtimeIntentProtocolIsConversationRecapTask(task) {
		return true
	}
	resolved := normalizeRuntimeIntentProtocolAtomicText(task.ResolvedText)
	if resolved == "" {
		resolved = normalizeRuntimeIntentProtocolAtomicText(task.Text)
	}
	taskText := normalizeRuntimeIntentProtocolAtomicText(task.Text)
	candidateText := normalizeRuntimeIntentProtocolAtomicText(candidate)
	if resolved == "" || candidateText == "" {
		return false
	}
	if !runtimeIntentProtocolTaskEntitiesGroundedInCandidateText(task, candidate) {
		return false
	}
	if resolved == taskText || resolved == candidateText {
		return true
	}
	if runtimeIntentProtocolImpliedChargingQuestion(candidate, task.ResolvedText) {
		return true
	}
	aspectText := candidate
	if taskText != "" && candidateText != "" && strings.Contains(candidateText, taskText) {
		aspectText = task.Text
	}
	candidateAspect := runtimeIntentProtocolReferenceAspect(aspectText)
	resolvedAspect := runtimeIntentProtocolReferenceAspect(task.ResolvedText)
	if candidateAspect != "" && resolvedAspect != "" && candidateAspect != resolvedAspect {
		return false
	}
	candidateCore := runtimeIntentProtocolAtomicTopicCore(candidate)
	resolvedCore := runtimeIntentProtocolAtomicTopicCore(task.ResolvedText)
	if runtimeIntentProtocolTopicCoresOverlap(candidateCore, resolvedCore) {
		return true
	}
	taskCore := runtimeIntentProtocolAtomicTopicCore(task.Text)
	return runtimeIntentProtocolTopicCoresOverlap(taskCore, resolvedCore) &&
		runtimeIntentProtocolTopicCoresOverlap(candidateCore, taskCore)
}

func runtimeIntentProtocolImpliedChargingQuestion(candidate string, resolvedText string) bool {
	candidateText := normalizeRuntimeKnowledgeQuery(candidate)
	resolved := normalizeRuntimeKnowledgeQuery(resolvedText)
	return containsAny(candidateText, []string{"电车", "电动车", "新能源车"}) &&
		containsAny(candidateText, []string{"懂我意思", "明白我意思"}) &&
		containsAny(resolved, []string{"充电桩", "充电"})
}

func runtimeIntentProtocolTaskEntitiesGroundedInCandidateText(task runtimeIntentTaskJSON, candidate string) bool {
	candidateText := normalizeRuntimeKnowledgeQuery(candidate)
	taskText := normalizeRuntimeKnowledgeQuery(task.Text)
	resolvedText := normalizeRuntimeKnowledgeQuery(task.ResolvedText)
	for _, entity := range task.Entities {
		anchor := runtimeIntentProtocolEntityAnchor(entity)
		if !runtimeIntentConcreteEntityText(anchor) || runtimeIntentProtocolAspectOnlyEntity(anchor) ||
			!strings.Contains(resolvedText, anchor) {
			continue
		}
		if !strings.Contains(candidateText, anchor) && !strings.Contains(taskText, anchor) {
			return false
		}
	}
	return true
}

func runtimeIntentProtocolTopicCoresOverlap(left string, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if len([]rune(left)) < 2 || len([]rune(right)) < 2 {
		return false
	}
	if strings.Contains(left, right) || strings.Contains(right, left) {
		return true
	}
	for size := 4; size >= 2; size-- {
		for _, anchor := range runtimeIntentProtocolRuneWindows(left, size) {
			if runtimeIntentProtocolGenericSingleTaskTopicAnchor(anchor) || !strings.Contains(right, anchor) {
				continue
			}
			return true
		}
	}
	return false
}

func runtimeIntentProtocolTaskCandidateOwnershipKey(task runtimeIntentTaskJSON, candidate string) string {
	aspect := strings.ToLower(strings.TrimSpace(task.Objective))
	if aspect == "" {
		aspect = "unknown"
	}
	subject := runtimeIntentProtocolAtomicTopicCore(task.Text)
	if len([]rune(subject)) < 2 {
		subject = runtimeIntentProtocolAtomicTopicCore(task.ResolvedText)
	}
	if len([]rune(subject)) < 2 {
		subject = runtimeIntentProtocolAtomicTopicCore(candidate)
	}
	return aspect + "|" + subject
}

func runtimeIntentProtocolAtomicObjectivesCompatible(expected string, actual string) bool {
	if actual == "" || actual == "unknown" || !semanticGateValidObjective(actual) ||
		expected == "general_guidance" || actual == expected || actual == "compound_information" {
		return true
	}
	if expected != "compound_information" {
		return false
	}
	switch actual {
	case "availability", "quantity", "price", "time", "location", "method", "policy", "scope", "condition", "explanation", "general_guidance":
		return true
	default:
		return false
	}
}

func runtimeIntentProtocolAtomicTopicCore(text string) string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	for from, to := range map[string]string{
		"早饭": "早餐", "无线网络": "wifi", "无线网": "wifi",
		"房门怎么打开": "开门", "怎么打开房门": "开门", "打开房门": "开门", "房门打开": "开门", "开房门": "开门",
	} {
		compact = strings.ReplaceAll(compact, from, to)
	}
	for _, phrase := range []string{
		"可以不可以", "可不可以", "有没有", "是不是", "是否有", "能不能", "什么时候", "为什么", "怎么办", "咋办",
		"怎么", "如何", "多少", "几个", "几瓶", "几点", "多久", "哪里", "哪儿", "在哪", "哪个", "什么",
		"酒店", "门店", "房间", "客房", "房型", "当前", "相关", "问题", "服务", "是否",
		"麻烦问下", "麻烦问一下", "麻烦请问", "请问一下", "请问", "麻烦", "告诉我", "说一下", "说下",
		"供应时间", "开放时间", "营业时间", "开始时间", "结束时间", "时间", "价格", "费用", "收费情况", "收费", "免费",
		"供应", "开放", "营业", "开始", "结束", "情况", "方法", "方式", "流程", "办理", "操作", "提供",
	} {
		compact = strings.ReplaceAll(compact, phrase, "")
	}
	return strings.Trim(compact, "的了呢啊呀哈吧吗么哦啦这那和与都也")
}

func runtimeIntentProtocolGenericSingleTaskTopicAnchor(anchor string) bool {
	anchor = normalizeRuntimeKnowledgeQuery(anchor)
	return anchor == "" || containsAny(anchor, []string{
		"可以", "需要", "有没", "没有", "是否", "是不", "能不", "不能", "知道", "了解", "一下", "告诉", "说下",
	})
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
	for _, candidateIndex := range candidateIndexes {
		if candidateIndex < 0 || candidateIndex >= len(candidates) ||
			!runtimeIntentProtocolCompoundCandidateIsKnowledgeOnly(candidates[candidateIndex]) {
			return false
		}
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
			if runtimeIntentProtocolCompoundResidualIsAspectOnly(candidateText, subject) {
				continue
			}
			return false
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
			if candidateIndex < 0 || candidateIndex >= len(candidates) {
				shared = false
				break
			}
			candidateText := normalizeRuntimeKnowledgeQuery(candidates[candidateIndex])
			if !strings.Contains(candidateText, entityText) ||
				!runtimeIntentProtocolCompoundResidualIsAspectOnly(candidateText, entityText) {
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

func runtimeIntentProtocolCompoundResidualIsAspectOnly(candidate string, subject string) bool {
	residual := normalizeRuntimeKnowledgeQuery(candidate)
	residual = strings.ReplaceAll(residual, normalizeRuntimeKnowledgeQuery(subject), "")
	core := runtimeIntentProtocolAtomicTopicCore(residual)
	core = strings.Trim(core, "有是要会能可的了呢啊呀哈吧吗么哦啦这那和与都也")
	return len([]rune(core)) < 2 || runtimeIntentProtocolAspectOnlyEntity(core)
}

func runtimeIntentProtocolCompoundCandidateIsKnowledgeOnly(candidate string) bool {
	compact := normalizeRuntimeKnowledgeQuery(candidate)
	if containsAny(compact, []string{"转人工", "找人工", "找客服", "接客服", "接同事", "联系同事"}) {
		return false
	}
	actionVerb := containsAny(compact, []string{"送来", "送上来", "送到", "拿来", "拿上来", "换一个", "换新的", "修一下", "来看看", "过来处理", "叫人", "派人", "安排人"})
	actionRequest := containsAny(compact, []string{"帮我", "给我", "麻烦", "请人", "让人"})
	return !actionVerb || !actionRequest
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
		case "facility", "supply", "room_type", "room", "service", "location", "order", "person", "company":
			return true
		}
	}
	return false
}

func runtimeIntentClarifyHasStructuredAlternativeTargets(task runtimeIntentTaskJSON) bool {
	text := normalizeRuntimeKnowledgeQuery(task.Text)
	byType := make(map[string]map[string]struct{}, len(task.Entities))
	for _, entity := range task.Entities {
		entityType := strings.TrimSpace(entity.Type)
		entityText := normalizeRuntimeKnowledgeQuery(entity.Text)
		if entityType == "" || entityText == "" || strings.Contains(text, entityText) {
			continue
		}
		if byType[entityType] == nil {
			byType[entityType] = make(map[string]struct{}, 2)
		}
		byType[entityType][entityText] = struct{}{}
	}
	for _, values := range byType {
		if len(values) >= 2 {
			return true
		}
	}
	return false
}

func runtimeIntentTaskHasSelfContainedBusinessRequest(task runtimeIntentTaskJSON) bool {
	objective := semanticGateNormalizeObjective(task.Objective)
	switch objective {
	case "availability", "quantity", "location", "price", "time", "policy", "method", "explanation", "recommendation", "identity", "general_guidance", "compound_information", "status", "confirm", "action_request":
	default:
		return false
	}
	text := strings.TrimSpace(task.Text)
	if text == "" || isRuntimeIntentOutputConstraintClause(text) ||
		runtimeIntentAtomicCandidateRequiresContext(text) || isDependentIntentTaskClause(text) ||
		runtimeIntentClarifyHasStructuredAlternativeTargets(task) {
		return false
	}
	compact := normalizeRuntimeKnowledgeQuery(text)
	if objective == "action_request" {
		return runtimeIntentActionRequestHasTarget(compact)
	}
	return len([]rune(compact)) >= 2 && (runtimeIntentStandaloneTaskLabel(text) || runtimeIntentTaskHasExplicitRequestWording(compact))
}

func runtimeIntentTaskHasExplicitRequestWording(compact string) bool {
	return containsAny(compact, []string{
		"告诉我", "跟我说", "说一下", "说下", "讲一下", "讲下", "说清楚",
		"发给我", "发我", "给我发", "给我看", "给我查", "帮我查", "查一下", "查下",
		"介绍一下", "介绍下", "推荐一下", "推荐下", "列一下", "列出", "说明一下", "说明下",
	})
}

func runtimeIntentActionRequestHasTarget(text string) bool {
	compact := strings.Trim(normalizeRuntimeKnowledgeQuery(text), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	if compact == "" {
		return false
	}

	for {
		before := compact
		for _, prefix := range []string{
			"麻烦帮我一下", "麻烦帮我", "麻烦给我", "麻烦请", "麻烦",
			"可以帮我", "能不能帮我", "能否帮我", "可不可以帮我",
			"请帮我一下", "请帮我", "请给我", "请",
			"帮我一下", "帮我", "帮忙", "给我", "我想要", "我需要", "我想", "我要", "需要", "想要",
		} {
			if !strings.HasPrefix(compact, prefix) {
				continue
			}
			compact = strings.TrimLeft(strings.TrimSpace(strings.TrimPrefix(compact, prefix)), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
			break
		}
		if compact == before {
			break
		}
	}
	compact = strings.Trim(compact, "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	if compact == "" {
		return false
	}

	for _, suffix := range []string{"发给我", "给我发", "发我", "送给我", "给我送", "送来", "送上来", "拿给我", "给我拿"} {
		if !strings.HasSuffix(compact, suffix) {
			continue
		}
		return runtimeIntentActionRequestHasTargetAfterActionPrefix(strings.TrimSuffix(compact, suffix))
	}
	for _, prefix := range []string{
		"派人过来", "叫人过来", "让人过来", "派人来", "叫人来", "让人来",
		"安排", "派人", "叫人", "让人",
	} {
		if !strings.HasPrefix(compact, prefix) {
			continue
		}
		return runtimeIntentActionRequestHasTarget(strings.TrimPrefix(compact, prefix))
	}
	for _, prefix := range []string{
		"联系", "通知", "转接", "发送", "提供", "确认", "查看", "查询", "获取",
		"处理", "取消", "删除", "添加", "帮助", "帮忙", "告诉", "说说", "讲讲", "看看", "查查", "说", "讲", "看", "查",
		"发", "送", "拿", "换", "修", "开", "关",
	} {
		if !strings.HasPrefix(compact, prefix) {
			continue
		}
		return runtimeIntentActionRequestHasTargetAfterActionPrefix(strings.TrimPrefix(compact, prefix))
	}
	return runtimeIntentActionRequestHasConcreteTarget(compact)
}

func runtimeIntentActionRequestHasTargetAfterActionPrefix(text string) bool {
	target := runtimeIntentActionRequestTargetCore(text)
	return target != "" &&
		!runtimeIntentActionRequestTargetIsQuantityOnly(target) &&
		!runtimeIntentActionRequestIsBarePredicateWithQuantity(target)
}

func runtimeIntentActionRequestHasConcreteTarget(text string) bool {
	target := runtimeIntentActionRequestTargetCore(text)
	return target != "" &&
		!runtimeIntentActionRequestIsBarePredicateSequence(target) &&
		!runtimeIntentActionRequestTargetIsQuantityOnly(target) &&
		!runtimeIntentActionRequestIsBarePredicateWithQuantity(target)
}

func runtimeIntentActionRequestIsBarePredicateSequence(text string) bool {
	compact := strings.Trim(normalizeRuntimeKnowledgeQuery(text), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了一下")
	if compact == "" {
		return false
	}
	if runtimeIntentActionRequestIsBarePredicate(compact) {
		return true
	}
	runes := []rune(compact)
	for index := 1; index < len(runes); index++ {
		if runtimeIntentActionRequestIsBarePredicate(string(runes[:index])) &&
			runtimeIntentActionRequestIsBarePredicateSequence(string(runes[index:])) {
			return true
		}
	}
	return false
}

func runtimeIntentActionRequestIsBarePredicate(text string) bool {
	compact := strings.Trim(normalizeRuntimeKnowledgeQuery(text), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了一下")
	switch compact {
	case "帮", "帮助", "帮忙", "说", "说说", "讲", "讲讲", "告诉", "确认", "看看", "看", "查看", "查查", "查", "查询", "获取", "提供",
		"处理", "安排", "弄", "搞", "做", "执行", "操作", "发", "发送", "送", "拿", "换", "修", "联系", "通知", "转接", "取消", "删除", "添加", "开", "关",
		"预约", "预订", "申请", "补充", "配送", "办理", "领取", "叫", "订", "租", "借", "过来", "来", "点":
		return true
	default:
		return false
	}
}

var runtimeIntentActionRequestQuantityOnlyPattern = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]+)?|[零〇一二三四五六七八九十百千万两半几]+|若干|多少)(?:个|份|瓶|件|张|套|间|些|点|位|次|条|双|把|包|盒|袋|支|只|杯|桶|卷|晚|天|小时|分钟)?$`)

func runtimeIntentActionRequestTargetIsQuantityOnly(text string) bool {
	compact := strings.Trim(normalizeRuntimeKnowledgeQuery(text), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	return compact != "" && runtimeIntentActionRequestQuantityOnlyPattern.MatchString(compact)
}

func runtimeIntentActionRequestIsBarePredicateWithQuantity(text string) bool {
	compact := strings.Trim(normalizeRuntimeKnowledgeQuery(text), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	for index := range compact {
		if index == 0 || !runtimeIntentActionRequestIsBarePredicate(compact[:index]) {
			continue
		}
		if runtimeIntentActionRequestTargetIsQuantityOnly(compact[index:]) {
			return true
		}
	}
	return false
}

func runtimeIntentActionRequestTargetCore(text string) string {
	target := strings.Trim(normalizeRuntimeKnowledgeQuery(text), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	for {
		before := target
		for _, prefix := range []string{"给我一下", "给我", "一下", "下"} {
			target = strings.TrimPrefix(target, prefix)
		}
		for _, suffix := range []string{"给我一下", "给我", "一下", "下", "我"} {
			target = strings.TrimSuffix(target, suffix)
		}
		target = strings.Trim(target, "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
		if target == before {
			break
		}
	}
	switch target {
	case "", "事", "事情", "东西", "帮助", "忙", "这个", "那个", "它", "这", "那", "问题", "服务", "操作":
		return ""
	default:
		return target
	}
}

func runtimeIntentInteractionContradictsBusinessProtocol(task runtimeIntentTaskJSON, resolution string) bool {
	subIntent := strings.ToLower(strings.TrimSpace(task.SubIntent))
	if task.NeedsKnowledge || task.NeedsHumanRoute {
		return true
	}
	if task.NeedsTool {
		resourceAction := strings.ToLower(strings.TrimSpace(task.ResourceAction))
		return subIntent != "weather_query" || task.NeedsResource ||
			(resourceAction != "" && resourceAction != "get_weather") ||
			!runtimeIntentProtocolIsExplicitWeatherQuestion(task)
	}
	// Resource delivery remains a valid model-owned interaction task. The
	// executor normalizes the concrete resource action later; rejecting it here
	// would turn a legitimate mini-program or media task into an Intent retry.
	if task.NeedsResource || strings.TrimSpace(task.ResourceAction) != "" {
		return subIntent == "clarify"
	}
	// Weather must use the real tool path. A weather label without the tool is
	// not a valid interaction result and must be repaired by IntentDetect.
	if subIntent == "weather_query" {
		return true
	}
	// IntentDetect owns non-business interaction categories such as AI identity,
	// social questions and frustration. Preserve those categories, but do not let
	// a clear hotel request bypass retrieval merely because the model chose a
	// non-clarify interaction subtype.
	if subIntent != "clarify" {
		if runtimeIntentProtocolIsExplicitAIIdentityQuestion(task) ||
			runtimeIntentProtocolIsExplicitSocialQuestion(task) ||
			runtimeIntentProtocolIsExplicitPureInteraction(task, task.Text) {
			return false
		}
		return runtimeIntentProtocolInteractionHasClearHotelBusinessTarget(task, resolution)
	}
	if resolution == runtimeIntentResolutionResolvedFromContext {
		return true
	}
	if semanticGateNormalizeObjective(task.Objective) == "action_request" && !runtimeIntentActionRequestHasTarget(task.Text) {
		return false
	}
	if runtimeIntentProtocolOriginalIsIndependentQuestion(task.Text) ||
		runtimeIntentProtocolOriginalIsIndependentQuestion(task.ResolvedText) {
		return true
	}
	if runtimeIntentTaskHasSelfContainedBusinessRequest(task) {
		return true
	}
	return resolution == runtimeIntentResolutionClear && runtimeIntentTaskHasExplicitBusinessInformationTarget(task)
}

func runtimeIntentProtocolIsExplicitWeatherQuestion(task runtimeIntentTaskJSON) bool {
	if semanticGateNormalizeObjective(task.Objective) != "general_guidance" {
		return false
	}
	text := normalizeRuntimeKnowledgeQuery(strings.Join([]string{task.Text, task.ResolvedText}, " "))
	if runtimeIntentProtocolHasOperationalHotelTarget(task, text) {
		return false
	}
	if containsAny(text, []string{
		"天气", "气温", "温度", "下雨", "降雨", "雨雪", "晴天", "阴天", "多云", "台风", "刮风", "风力", "空气质量",
	}) {
		return true
	}
	indirectWeather := containsAny(text, []string{"热不热", "冷不冷", "热吗", "冷吗", "带伞", "雨伞"})
	timeBound := containsAny(text, []string{"今天", "今日", "今晚", "明天", "明日", "后天", "这两天", "最近几天", "周末"})
	return indirectWeather && (timeBound || runtimeIntentProtocolTaskHasEntityType(task, "location", "city", "place"))
}

func runtimeIntentProtocolIsExplicitAIIdentityQuestion(task runtimeIntentTaskJSON) bool {
	if strings.ToLower(strings.TrimSpace(task.SubIntent)) != "ai_identity" ||
		semanticGateNormalizeObjective(task.Objective) != "identity" {
		return false
	}
	combined := strings.Trim(normalizeRuntimeKnowledgeQuery(strings.Join(runtimeIntentProtocolTaskTextVariants(task), " ")), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	return containsAny(combined, []string{
		"你是谁", "你叫什么", "你的名字", "你是机器人", "你是ai", "你是人工智能", "你是真人", "你是人工客服", "你是客服", "你能做什么", "你会什么",
	}) && !containsAny(combined, []string{"酒店老板", "门店老板", "老板是谁", "董事长", "负责人是谁"})
}

func runtimeIntentProtocolIsExplicitSocialQuestion(task runtimeIntentTaskJSON) bool {
	if strings.ToLower(strings.TrimSpace(task.SubIntent)) != "social" ||
		!strings.EqualFold(strings.TrimSpace(task.Objective), "social") {
		return false
	}
	if runtimeIntentTaskHasExplicitBusinessInformationTarget(task) {
		return false
	}
	for _, text := range runtimeIntentProtocolTaskTextVariants(task) {
		compact := strings.Trim(normalizeRuntimeKnowledgeQuery(text), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
		switch compact {
		case "你好吗", "你最近好吗", "你怎么样", "你最近怎么样", "你在干嘛", "你在做什么", "你忙吗":
			return true
		}
	}
	return false
}

func runtimeIntentProtocolTaskTextVariants(task runtimeIntentTaskJSON) []string {
	ret := make([]string, 0, 2)
	for _, text := range []string{task.Text, task.ResolvedText} {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		normalized := normalizeRuntimeKnowledgeQuery(text)
		duplicate := false
		for _, existing := range ret {
			if normalizeRuntimeKnowledgeQuery(existing) == normalized {
				duplicate = true
				break
			}
		}
		if !duplicate {
			ret = append(ret, text)
		}
	}
	return ret
}

func runtimeIntentProtocolHasOperationalHotelTarget(task runtimeIntentTaskJSON, text string) bool {
	for _, entity := range task.Entities {
		switch strings.ToLower(strings.TrimSpace(entity.Type)) {
		case "facility", "supply", "room_type", "room", "service", "order", "person", "company":
			return true
		}
	}
	return containsAny(text, []string{
		"房间", "房型", "客房", "入住", "退房", "续住", "开门", "房门", "房号", "门锁", "密码",
		"早餐", "停车", "充电桩", "发票", "wifi", "无线网", "空调", "电视", "投屏", "遥控器", "拖鞋", "牙刷", "浴巾", "毛巾", "纸巾", "矿泉水",
		"洗衣", "外卖", "机器人", "小程序", "老板", "董事长", "负责人", "前台", "同住人", "行李", "遗失", "噪音",
	})
}

func runtimeIntentProtocolTextHasHotelBusinessAnchor(text string) bool {
	text = normalizeRuntimeKnowledgeQuery(text)
	return containsAny(text, []string{
		"酒店", "门店", "房间", "房型", "客房", "入住", "退房", "续住", "开门", "房门", "房号", "门锁", "密码",
		"早餐", "停车", "充电桩", "发票", "wifi", "无线网", "空调", "电视", "投屏", "遥控器", "拖鞋", "牙刷", "浴巾", "毛巾", "纸巾", "矿泉水",
		"洗衣", "外卖", "机器人", "小程序", "老板", "董事长", "负责人", "前台", "同住人", "行李", "遗失", "噪音",
		"附近餐饮", "附近小吃", "附近好玩", "周边餐饮", "周边小吃", "周边景点", "地铁站", "公交站", "高铁站", "机场",
	})
}

func runtimeIntentProtocolInteractionHasClearHotelBusinessTarget(task runtimeIntentTaskJSON, resolution string) bool {
	if resolution == runtimeIntentResolutionResolvedFromContext ||
		runtimeIntentTaskHasExplicitBusinessInformationTarget(task) ||
		runtimeIntentTaskHasSelfContainedBusinessRequest(task) {
		return true
	}
	text := normalizeRuntimeKnowledgeQuery(strings.Join([]string{task.Text, task.ResolvedText}, " "))
	if text == "" || !runtimeIntentProtocolTextHasHotelBusinessAnchor(text) {
		return false
	}
	return runtimeIntentProtocolOriginalIsIndependentQuestion(task.Text) ||
		runtimeIntentProtocolOriginalIsIndependentQuestion(task.ResolvedText) ||
		runtimeIntentStandaloneTaskLabel(task.Text) ||
		containsAny(text, []string{"我问的是", "我说的是", "帮我", "麻烦", "请问", "告诉我", "查一下", "查下"})
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
