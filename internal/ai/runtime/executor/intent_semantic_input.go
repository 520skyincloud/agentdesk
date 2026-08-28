package executor

import (
	"strings"

	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
)

const runtimeIntentExplicitQuestionMarker = "\uE000"

// currentRuntimeIntentSemanticText returns the customer text that every Intent
// stage should classify. Voice transcripts are business input, not media
// decoration, so they follow the same path as ordinary text.
func currentRuntimeIntentSemanticText(req RunInput) string {
	content := strings.TrimSpace(req.UserMessage.Content)
	if utils.IsRuntimeCustomerBurstEnvelope(content) {
		return strings.TrimSpace(currentTurnDisplayText(content))
	}
	if req.UserMessage.MessageType == enums.IMMessageTypeVoice {
		mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(req.UserMessage.Payload)
		if strings.TrimSpace(status) != "understood" {
			return ""
		}
		if text := preferredMediaUnderstandingText(mediaText, mediaSummary); text != "" {
			return text
		}
		return ""
	}
	return strings.TrimSpace(currentTurnDisplayText(content))
}

func currentTurnTaskCandidates(text string) []string {
	display := strings.TrimSpace(currentTurnDisplayText(text))
	if display == "" {
		return nil
	}
	display = strings.NewReplacer(
		"?", runtimeIntentExplicitQuestionMarker+"?",
		"？", runtimeIntentExplicitQuestionMarker+"？",
	).Replace(display)
	coarseParts := strings.FieldsFunc(display, func(r rune) bool {
		switch r {
		case '\n', '\r', ',', '，', '.', '。', ';', '；', '?', '？', '!', '！':
			return true
		default:
			return false
		}
	})
	parts := make([]string, 0, len(coarseParts))
	for _, part := range coarseParts {
		parts = append(parts, splitRuntimeTaskCandidateClause(part)...)
	}
	candidates := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		explicitQuestion := strings.Contains(part, runtimeIntentExplicitQuestionMarker)
		part = strings.ReplaceAll(part, runtimeIntentExplicitQuestionMarker, "")
		part = cleanRuntimeQuestionLine(part)
		part = trimRuntimeTaskCandidateLead(part)
		if part == "" || isRuntimeBurstStructureLine(part) || isIntentTaskLeadOnly(part) {
			continue
		}
		dependent := isRuntimeIntentOutputConstraintClauseWithExplicitQuestion(part, explicitQuestion) || isDependentIntentTaskClause(part) || runtimeIntentAtomicCandidateRequiresContext(part)
		if dependent {
			if len(candidates) > 0 {
				candidates[len(candidates)-1] = strings.TrimSpace(candidates[len(candidates)-1] + "，" + part)
				continue
			}
		}
		if !dependent && !explicitQuestion && !runtimeBurstLineLooksLikeTask(part) && !runtimeIntentTaskLabelLooksLikeTask(part) {
			continue
		}
		normalized := normalizeRuntimeKnowledgeQuery(part)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		candidates = append(candidates, part)
	}
	return candidates
}

func trimRuntimeTaskCandidateLead(text string) string {
	text = strings.TrimSpace(text)
	for _, delimiter := range []string{"：", ":"} {
		index := strings.Index(text, delimiter)
		if index <= 0 || index >= len(text)-len(delimiter) {
			continue
		}
		prefix := normalizeRuntimeKnowledgeQuery(text[:index])
		if runtimeIntentTaskCandidateLeadIsInstruction(prefix) {
			return strings.TrimSpace(text[index+len(delimiter):])
		}
	}
	return text
}

func runtimeIntentTaskCandidateLeadIsInstruction(prefix string) bool {
	if containsAny(prefix, []string{
		"分别回答", "逐个回答", "逐项回答", "一起问",
		"几个问题", "这些问题", "以下问题", "下面问题", "下列问题",
		"请问", "想问",
	}) {
		return true
	}
	for _, lead := range []string{"我想一次问", "我一次问", "一次问", "我有", "以下", "下面", "下列"} {
		if strings.HasPrefix(prefix, lead) && runtimeIntentCountedTaskLeadSuffix(strings.TrimPrefix(prefix, lead)) {
			return true
		}
	}
	return false
}

func runtimeIntentCountedTaskLeadSuffix(text string) bool {
	runes := []rune(strings.TrimSpace(strings.ToLower(text)))
	countEnd := 0
	for countEnd < len(runes) && strings.ContainsRune("0123456789零〇一二两三四五六七八九十百千万几n", runes[countEnd]) {
		countEnd++
	}
	if countEnd == 0 {
		return false
	}
	switch string(runes[countEnd:]) {
	case "个", "个问题", "项", "项问题", "条", "条问题":
		return true
	default:
		return false
	}
}

func splitRuntimeTaskCandidateClause(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if separated := splitRuntimeExplicitSeparateTaskClause(text); len(separated) > 1 {
		return separated
	}
	if !strings.Contains(text, "、") {
		return []string{text}
	}
	parts := strings.Split(text, "、")
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		part = cleanRuntimeQuestionLine(part)
		if part == "" || (!runtimeBurstLineLooksLikeTask(part) && !runtimeIntentTaskLabelLooksLikeTask(part)) {
			return []string{text}
		}
		ret = append(ret, part)
	}
	if len(ret) == 2 && !runtimeBurstLineLooksLikeTask(ret[0]) && runtimeIntentClauseHasSharedPredicate(text) {
		return []string{text}
	}
	return ret
}

func splitRuntimeExplicitSeparateTaskClause(text string) []string {
	markerIndex := strings.Index(text, "分别")
	if markerIndex <= 0 {
		return nil
	}
	prefix := strings.TrimSpace(text[:markerIndex])
	if prefix == "" {
		return nil
	}
	parts := splitRuntimeExplicitTaskPrefix(prefix)
	if len(parts) <= 1 {
		return nil
	}
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		part = trimRuntimeTaskCandidateLead(cleanRuntimeQuestionLine(part))
		if part == "" || isIntentTaskLeadOnly(part) || (!runtimeBurstLineLooksLikeTask(part) && !runtimeIntentStandaloneTaskLabel(part)) {
			return nil
		}
		ret = append(ret, part)
	}
	return ret
}

func splitRuntimeExplicitTaskPrefix(prefix string) []string {
	runes := []rune(strings.TrimSpace(prefix))
	parts := make([]string, 0, 3)
	start := 0
	preserveNext := false
	for index, character := range runes {
		if preserveNext {
			preserveNext = false
			continue
		}
		if character != '和' && character != '与' && character != '、' {
			continue
		}
		if index == start {
			continue
		}
		parts = append(parts, strings.TrimSpace(string(runes[start:index])))
		start = index + 1
		if index+1 < len(runes) && (runes[index+1] == '和' || runes[index+1] == '与') {
			preserveNext = true
		}
	}
	parts = append(parts, strings.TrimSpace(string(runes[start:])))
	return parts
}

func runtimeIntentStandaloneTaskLabel(text string) bool {
	compact := compactRuntimeIntentClause(trimRuntimeTaskCandidateLead(text))
	switch compact {
	case "方式", "流程", "地址", "账号", "密码", "数量", "费用", "价格", "时间", "位置":
		return false
	default:
		return runtimeIntentTaskLabelLooksLikeTask(compact)
	}
}

func runtimeIntentClauseHasSharedPredicate(text string) bool {
	compact := compactRuntimeIntentClause(text)
	return containsAny(compact, []string{"都有", "同时有", "同时具备", "既有", "兼有", "都配", "都提供"})
}

func runtimeIntentTaskLabelLooksLikeTask(text string) bool {
	compact := compactRuntimeIntentClause(trimRuntimeTaskCandidateLead(text))
	if len([]rune(compact)) < 2 || len([]rune(compact)) > 24 {
		return false
	}
	return containsAny(compact, []string{
		"方式", "流程", "地址", "账号", "密码", "数量", "费用", "价格", "时间", "位置",
		"wifi", "停车", "充电桩", "发票", "矿泉水", "机器人", "早餐", "入住", "开门",
	})
}

func isDependentIntentTaskClause(text string) bool {
	compact := strings.Trim(compactRuntimeIntentClause(text), "，,。.!！?？；;：:")
	for _, prefix := range []string{"另外", "还有", "顺便", "然后", "而且"} {
		compact = strings.TrimPrefix(compact, prefix)
	}
	for _, prefix := range []string{"刚才那个", "刚才的", "刚刚那个", "刚刚的", "前面那个", "前面的", "上面那个", "上面的", "那两瓶", "这两瓶", "这几瓶", "那些", "这些", "那个", "这个", "那", "这", "它们", "它", "都", "也"} {
		compact = strings.TrimPrefix(compact, prefix)
	}
	switch compact {
	case "免费吗", "是免费的吗", "是不是免费的", "是不是都免费的", "是否免费", "收费吗", "收不收费", "要收费吗", "需要收费吗", "多少钱", "价格呢",
		"多久", "几点", "什么时候", "在哪里", "在哪", "哪里", "怎么用", "怎么弄", "如何使用", "可以吗", "行吗", "对吗", "是吗", "这样对吗", "这样是吗", "那呢", "呢":
		return true
	default:
		return runtimeIntentGenericFollowUpClause(compact)
	}
}

func isRuntimeIntentOutputConstraintClause(text string) bool {
	return isRuntimeIntentOutputConstraintClauseWithExplicitQuestion(text, strings.ContainsAny(text, "?？"))
}

func isRuntimeIntentOutputConstraintClauseWithExplicitQuestion(text string, explicitQuestion bool) bool {
	compact := compactRuntimeIntentClause(text)
	if explicitQuestion || runtimeIntentClauseHasSelfContainedQuestion(compact) {
		return false
	}
	for _, prefix := range []string{"只要", "只说", "只回复", "只需要", "仅说", "仅回复", "仅需要", "仅"} {
		if strings.HasPrefix(compact, prefix) && len(compact) > len(prefix) {
			return true
		}
	}
	return false
}

func runtimeIntentClauseHasSelfContainedQuestion(compact string) bool {
	return containsAny(compact, []string{
		"吗", "是否", "有没有", "能不能", "可不可以", "是不是",
		"怎么", "咋", "如何", "多少", "几个", "几瓶", "几点", "多久", "什么时候",
		"哪里", "在哪", "为什么", "谁", "什么", "啥",
	})
}

func runtimeIntentGenericFollowUpClause(compact string) bool {
	for _, prefix := range []string{"什么时候", "在哪里", "几点", "多久", "在哪", "哪里", "怎么", "如何"} {
		if !strings.HasPrefix(compact, prefix) {
			continue
		}
		tail := strings.Trim(strings.TrimPrefix(compact, prefix), "呀啊呢吗哈的了")
		switch tail {
		case "", "开始", "结束", "供应", "开放", "营业", "能用", "可以用", "能办", "可以办",
			"吃", "办理", "申请", "下载", "能下载", "可以下载", "才能下载", "领取", "获取",
			"拿", "取", "拿取", "使用", "操作", "收费", "付款", "支付", "到账", "到", "走":
			return true
		default:
			return false
		}
	}
	return false
}

func isIntentTaskLeadOnly(text string) bool {
	compact := compactRuntimeIntentClause(text)
	switch compact {
	case "请问", "想问", "想问一下", "我想问", "我想问一下", "麻烦问一下", "咨询一下", "还有个问题", "还有一个问题", "另外", "还有", "顺便",
		"麻烦分别告诉我", "请分别告诉我", "帮我分别说一下", "麻烦分别说一下", "请分别说一下",
		"麻烦分别回答", "请分别回答", "麻烦逐个回答", "请逐个回答", "麻烦逐项回答", "请逐项回答":
		return true
	default:
		return false
	}
}

func compactRuntimeIntentClause(text string) string {
	return strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(strings.ToLower(strings.TrimSpace(text)))
}

// runtimeIntentRetrievalQuery removes only leading conversational framing from
// a retrieval copy. The intent task's text, resolvedText and sourceRefs remain
// unchanged so source coverage and generation still see the customer's words.
func runtimeIntentRetrievalQuery(text string) string {
	original := strings.TrimSpace(text)
	if original == "" {
		return ""
	}
	original = trimRuntimeIntentRetrievalReferenceLead(original)
	original = trimRuntimeIntentRetrievalOutputConstraints(original)
	for _, prefix := range []string{
		"还有一个问题想问", "还有个问题想问",
		"顺便再问一下", "顺便再问下", "另外再问一下", "另外再问下",
		"顺便问一下", "顺便问下", "顺便问", "另外问一下", "另外问下", "另外问", "再问一下", "再问下", "再问",
		"还有一个问题", "还有个问题",
	} {
		if remainder, ok := trimRuntimeIntentRetrievalPrefix(original, prefix, true); ok {
			return trimRuntimeIntentRetrievalOutputConstraints(remainder)
		}
	}
	for _, prefix := range []string{"顺便", "另外", "还有"} {
		if remainder, ok := trimRuntimeIntentRetrievalPrefix(original, prefix, false); ok {
			return trimRuntimeIntentRetrievalOutputConstraints(remainder)
		}
	}
	return trimRuntimeIntentRetrievalOutputConstraints(original)
}

func trimRuntimeIntentRetrievalReferenceLead(text string) string {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{
		"刚刚提到的", "刚刚说的", "刚刚那个", "刚刚的",
		"刚才提到的", "刚才说的", "刚才那个", "刚才的",
		"前面提到的", "前面说的", "前面那个", "前面的",
		"上面提到的", "上面说的", "上面那个", "上面的",
		"之前提到的", "之前说的", "之前那个", "之前的",
	} {
		if strings.HasPrefix(text, prefix) && len(text) > len(prefix) {
			return strings.TrimSpace(text[len(prefix):])
		}
	}
	return text
}

func trimRuntimeIntentRetrievalOutputConstraints(text string) string {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"再完整说一下", "再完整说下", "再复述一下", "再复述下", "再说一遍", "再说一下", "再说下", "重新说一下", "重新说下"} {
		if remainder, ok := trimRuntimeIntentRetrievalPrefix(text, prefix, true); ok {
			text = remainder
			break
		}
	}
	text = trimRuntimeIntentRetrievalSuffixConstraints(text)
	for _, phrase := range []string{"分别说清楚", "分别回答清楚", "分别说明清楚", "分别说", "分别回答", "分别说明", "不要混在一起", "别混在一起"} {
		text = strings.TrimSpace(strings.ReplaceAll(text, phrase, ""))
	}
	for _, separator := range []string{"，", ",", "；", ";"} {
		for _, marker := range []string{"只要", "只说", "只回复", "只需要", "仅说", "仅回复", "仅需要"} {
			if index := strings.Index(text, separator+marker); index > 0 {
				text = strings.TrimSpace(text[:index])
			}
		}
	}
	return strings.Trim(trimRuntimeIntentRetrievalSuffixConstraints(text), " ，,：:；;。.!！?？")
}

func trimRuntimeIntentRetrievalSuffixConstraints(text string) string {
	for _, phrase := range []string{
		"再完整说一次", "再完整说一遍", "完整说一次", "完整说一遍",
		"再完整说一下", "再完整说下", "完整说一下", "完整说下",
		"再复述一次", "再复述一遍", "复述一次", "复述一遍", "再复述一下", "再复述下", "复述一下", "复述下",
		"再说一次", "再说一遍", "重新说一次", "重新说一遍", "再说一下", "再说下", "重新说一下", "重新说下",
		"再确认一下", "确认一下",
	} {
		text = strings.TrimSpace(strings.TrimSuffix(text, phrase))
	}
	return text
}

func trimRuntimeIntentRetrievalPrefix(text string, prefix string, unambiguous bool) (string, bool) {
	if !strings.HasPrefix(text, prefix) || len(text) <= len(prefix) {
		return "", false
	}
	remainder := strings.TrimSpace(text[len(prefix):])
	hadSeparator := false
	for remainder != "" {
		trimmed := strings.TrimLeft(remainder, " ，,：:；;。.!！?？")
		if trimmed == remainder {
			break
		}
		hadSeparator = true
		remainder = trimmed
	}
	if remainder == "" || len([]rune(compactRuntimeIntentClause(remainder))) < 2 || (!runtimeBurstLineLooksLikeTask(remainder) && !runtimeIntentTaskLabelLooksLikeTask(remainder)) {
		return "", false
	}
	if unambiguous || hadSeparator || prefix == "顺便" {
		return remainder, true
	}
	compact := compactRuntimeIntentClause(remainder)
	if prefix == "还有" && (containsAnyPrefix(compact, []string{"没有", "多少", "几个", "几瓶", "哪些", "多久", "多长", "剩"}) || startsWithRuntimeIntentQuantity(compact)) {
		return "", false
	}
	if prefix == "另外" && containsAnyPrefix(compact, []string{"收费", "费用", "加收", "付费", "要钱"}) {
		return "", false
	}
	return remainder, true
}

func startsWithRuntimeIntentQuantity(text string) bool {
	for _, r := range text {
		return strings.ContainsRune("0123456789零〇一二两三四五六七八九十百千万半几", r)
	}
	return false
}

func containsAnyPrefix(text string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}
