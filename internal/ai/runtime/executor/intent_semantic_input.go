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
	compact := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(strings.ToLower(strings.TrimSpace(text)))
	switch compact {
	case "免费吗", "收费吗", "多少钱", "多久", "几点", "在哪里", "在哪", "怎么用", "怎么弄", "可以吗", "行吗", "那呢", "呢":
		return true
	default:
		return false
	}
}

func isIntentTaskLeadOnly(text string) bool {
	compact := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(strings.ToLower(strings.TrimSpace(text)))
	switch compact {
	case "请问", "想问", "想问一下", "我想问", "我想问一下", "麻烦问一下", "咨询一下", "还有个问题", "还有一个问题", "另外", "还有", "顺便":
		return true
	default:
		return false
	}
}
