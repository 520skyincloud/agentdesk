package executor

import (
	"strings"

	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
)

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
		part = cleanRuntimeQuestionLine(part)
		part = trimRuntimeTaskCandidateLead(part)
		if part == "" || isRuntimeBurstStructureLine(part) || isIntentTaskLeadOnly(part) {
			continue
		}
		if isDependentIntentTaskClause(part) && len(candidates) > 0 {
			candidates[len(candidates)-1] = strings.TrimSpace(candidates[len(candidates)-1] + "，" + part)
			continue
		}
		if !runtimeBurstLineLooksLikeTask(part) {
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
		if containsAny(prefix, []string{"分别回答", "逐个回答", "逐项回答", "一起问", "几个问题", "这些问题", "以下问题", "请问", "想问"}) {
			return strings.TrimSpace(text[index+len(delimiter):])
		}
	}
	return text
}

func splitRuntimeTaskCandidateClause(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" || !strings.Contains(text, "、") {
		return []string{text}
	}
	parts := strings.Split(text, "、")
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		part = cleanRuntimeQuestionLine(part)
		if part == "" || !runtimeBurstLineLooksLikeTask(part) {
			return []string{text}
		}
		ret = append(ret, part)
	}
	return ret
}

func isDependentIntentTaskClause(text string) bool {
	compact := compactRuntimeIntentClause(text)
	for _, prefix := range []string{"另外", "还有", "顺便", "然后", "而且"} {
		compact = strings.TrimPrefix(compact, prefix)
	}
	for _, prefix := range []string{"这两瓶", "这几瓶", "这些", "这个", "它们", "它", "都", "也"} {
		compact = strings.TrimPrefix(compact, prefix)
	}
	switch compact {
	case "免费吗", "是免费的吗", "是不是免费的", "是不是都免费的", "是否免费", "收费吗", "收不收费", "要收费吗", "需要收费吗", "多少钱", "价格呢",
		"多久", "几点", "什么时候", "在哪里", "在哪", "哪里", "怎么用", "怎么弄", "如何使用", "可以吗", "行吗", "那呢", "呢":
		return true
	default:
		return false
	}
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
	for _, prefix := range []string{
		"还有一个问题想问", "还有个问题想问",
		"顺便再问一下", "顺便再问下", "另外再问一下", "另外再问下",
		"顺便问一下", "顺便问下", "顺便问", "另外问一下", "另外问下", "另外问", "再问一下", "再问下", "再问",
		"还有一个问题", "还有个问题",
	} {
		if remainder, ok := trimRuntimeIntentRetrievalPrefix(original, prefix, true); ok {
			return remainder
		}
	}
	for _, prefix := range []string{"顺便", "另外", "还有"} {
		if remainder, ok := trimRuntimeIntentRetrievalPrefix(original, prefix, false); ok {
			return remainder
		}
	}
	return original
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
	if remainder == "" || len([]rune(compactRuntimeIntentClause(remainder))) < 2 || !runtimeBurstLineLooksLikeTask(remainder) {
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
	if len([]rune(compact)) < 5 {
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
