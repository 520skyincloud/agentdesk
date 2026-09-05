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
	HasBoundedHistory    bool
}

func repairRuntimeIntentDetectProtocol(parsed *runtimeIntentDetectJSON, currentText string, context runtimeIntentProtocolRepairContext, enforce bool) {
	// IntentDetect owns task boundaries, context resolution, and intent semantics.
	// Local repair is intentionally limited to exact duplicate removal.
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
		relation := semanticGateNormalizeRelation(task.RelationToPrevious)
		hasEarlierCurrentSource, err := runtimeIntentProtocolHasEarlierCurrentTurnContext(task, sourceTexts)
		if err != nil {
			return fmt.Errorf("intentTasks[%d] %w", taskIndex, err)
		}
		if hasEarlierCurrentSource {
			if relation != "independent" {
				return fmt.Errorf("intentTasks[%d] same-turn context sourceRefs require relationToPrevious independent", taskIndex)
			}
			continue
		}
		if !semanticGateRelationUsesPrevious(relation) {
			return fmt.Errorf("intentTasks[%d] resolved_from_context requires an earlier current-turn source or previous-turn relation", taskIndex)
		}
		if canonicalIntentCode(task.Intent) == "interaction" && strings.TrimSpace(task.SubIntent) == "conversation_recap" {
			if !context.HasBoundedHistory {
				return fmt.Errorf("intentTasks[%d] conversation_recap requires bounded conversation history", taskIndex)
			}
			continue
		}
		if !context.HasBoundedHistory && (strings.TrimSpace(context.PreviousCustomerText) == "" || runtimeIntentProtocolAdjacentServiceReply(context) == "") {
			return fmt.Errorf("intentTasks[%d] resolved_from_context requires bounded conversation history", taskIndex)
		}
	}
	return nil
}

func runtimeIntentProtocolHasEarlierCurrentTurnContext(task runtimeIntentTaskJSON, sourceTexts []string) (bool, error) {
	if len(task.SourceRefs) <= 1 || len(sourceTexts) == 0 {
		return false, nil
	}
	primarySource := runtimeIntentSourceRefIndex(task.SourceRefs[0])
	if primarySource < 0 || primarySource >= len(sourceTexts) {
		return false, nil
	}
	hasEarlierSource := false
	for _, ref := range task.SourceRefs[1:] {
		contextSource := runtimeIntentSourceRefIndex(ref)
		if contextSource < 0 || contextSource >= len(sourceTexts) {
			continue
		}
		if contextSource >= primarySource {
			return false, fmt.Errorf("resolved_from_context sourceRef %q must refer to an earlier current-turn source", ref)
		}
		hasEarlierSource = true
	}
	return hasEarlierSource, nil
}

func validateRuntimeIntentInteractionAgainstAdjacentContext(parsed runtimeIntentDetectJSON, currentText string, context runtimeIntentProtocolRepairContext) error {
	for taskIndex, task := range parsed.IntentTasks {
		answerRejectedIntent := canonicalIntentCode(task.Intent) == "human_complaint_risk" && strings.TrimSpace(task.SubIntent) == "answer_rejected"
		answerRejectedRelation := semanticGateNormalizeRelation(task.RelationToPrevious) == "answer_rejected"
		if !answerRejectedIntent && !answerRejectedRelation {
			continue
		}
		if !answerRejectedIntent || !answerRejectedRelation {
			return fmt.Errorf("intentTasks[%d] answer_rejected classification and relation must agree", taskIndex)
		}
		if strings.TrimSpace(context.AdjacentAIReply) == "" {
			return fmt.Errorf("intentTasks[%d] answer_rejected requires an immediately previous AI reply", taskIndex)
		}
	}
	_ = currentText
	return nil
}

var (
	runtimeIntentProtocolRoomNumberSlotValuePattern = regexp.MustCompile(`(?:^|[^0-9])[0-9]{3,6}(?:房|房间|号)?(?:[^0-9]|$)`)
	runtimeIntentProtocolNameSlotValuePattern       = regexp.MustCompile(`^[\p{Han}A-Za-z·]{2,20}$`)
	runtimeIntentProtocolTimeSlotValuePattern       = regexp.MustCompile(`(?:[0-9]{1,2}[:：][0-9]{2}|(?:凌晨|早上|上午|中午|下午|傍晚|晚上|夜里)?(?:[0-9]{1,2}|[零〇一二三四五六七八九十两]{1,3})点(?:半|[0-9]{1,2}分)?)`)
)

func runtimeIntentProtocolAdjacentServiceReply(context runtimeIntentProtocolRepairContext) string {
	if reply := strings.TrimSpace(context.AdjacentServiceReply); reply != "" {
		return reply
	}
	return strings.TrimSpace(context.AdjacentAIReply)
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
		if intent := canonicalIntentCode(task.Intent); intent == "" || !isRuntimeTopLevelIntent(intent) {
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
		for entityIndex, entity := range task.Entities {
			if strings.TrimSpace(entity.Text) == "" {
				return fmt.Errorf("intentTasks[%d].entities[%d].text is missing", index, entityIndex)
			}
			if !isRuntimeIntentEntityType(semanticGateNormalizeValue(entity.Type)) {
				return fmt.Errorf("intentTasks[%d].entities[%d].type is missing or invalid", index, entityIndex)
			}
		}
	}
	if requireSourceRefs && len(sourceTexts) > 0 {
		if err := validateRuntimeIntentProtocolModelOwnedSources(tasks, sourceTexts); err != nil {
			return err
		}
	}
	return nil
}

// validateRuntimeIntentProtocolModelOwnedSources verifies only source order and
// original-text provenance. IntentDetect alone owns task count and boundaries.
func validateRuntimeIntentProtocolModelOwnedSources(tasks []runtimeIntentTaskJSON, sourceTexts []string) error {
	lastPrimarySource := -1
	for taskIndex, task := range tasks {
		primarySource := runtimeIntentSourceRefIndex(task.SourceRefs[0])
		if primarySource < lastPrimarySource {
			return fmt.Errorf("intentTasks[%d] is out of current-turn source order", taskIndex)
		}
		lastPrimarySource = primarySource
		if primarySource >= 0 && primarySource < len(sourceTexts) &&
			!runtimeIntentProtocolCandidateMatchesTaskSource(task.Text, runtimeIntentSourceRefList{task.SourceRefs[0]}, sourceTexts) {
			return fmt.Errorf("intentTasks[%d].text is not traceable to primary source %s", taskIndex, strings.ToUpper(strings.TrimSpace(task.SourceRefs[0])))
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

var runtimeIntentActionRequestQuantityOnlyPattern = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]+)?|[零〇一二三四五六七八九十百千万两半几]+|若干|多少)(?:个|份|瓶|件|张|套|间|些|点|位|次|条|双|把|包|盒|袋|支|只|杯|桶|卷|晚|天|小时|分钟)?$`)

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
