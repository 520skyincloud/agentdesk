package executor

import (
	"strings"
	"unicode"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func enforceGeneratedReplyActionLedger(summary *RunResult, collector *callbacks.RuntimeTraceCollector) {
	if summary == nil || collector == nil {
		return
	}
	original := strings.TrimSpace(summary.ReplyText)
	if original == "" {
		return
	}
	intent := collector.Data.Pipeline.Intent
	cleaned := removeStructuredResourceCommitMentions(original, intent)
	cleaned = removeUnsupportedStaffActionMentions(cleaned, intent)
	cleaned = normalizeReplyTextWhitespace(cleaned)
	if cleaned == "" || cleaned == original {
		return
	}
	summary.ReplyText = cleaned
	collector.Data.Output.ReplyText = cleaned
	collector.Data.Pipeline.Validate.Status = "passed"
	collector.Data.Pipeline.Validate.Reason = appendValidationReason(
		collector.Data.Pipeline.Validate.Reason,
		"action ledger removed generated text that claimed uncommitted resource or staff actions",
	)
}

func removeStructuredResourceCommitMentions(text string, intent callbacks.IntentTraceData) string {
	actions := requestedResourceActionsFromIntent(intent)
	if len(actions) == 0 {
		return text
	}
	return filterReplySentences(text, func(sentence string) bool {
		return sentenceMentionsRequestedResource(sentence, actions) && containsAnyReplyPhrase(sentence, generatedResourceCommitPhrases())
	})
}

func removeUnsupportedStaffActionMentions(text string, intent callbacks.IntentTraceData) string {
	if intent.NeedsHumanRoute || strings.TrimSpace(intent.HumanRoutePolicy) != "" {
		return text
	}
	return filterReplySentences(text, func(sentence string) bool {
		return containsAnyReplyPhrase(sentence, unsupportedFirstPersonStaffActionPhrases())
	})
}

func requestedResourceActionsFromIntent(intent callbacks.IntentTraceData) []string {
	actions := make([]string, 0, len(intent.ResourceActions)+1)
	for _, action := range intent.ResourceActions {
		action = strings.TrimSpace(action)
		if action != "" {
			actions = appendIfMissing(actions, action)
		}
	}
	if action := strings.TrimSpace(intent.ResourceAction); action != "" {
		actions = appendIfMissing(actions, action)
	}
	for _, task := range intent.IntentTasks {
		if task.Intent != "hotel_variable" && !task.NeedsResource {
			continue
		}
		if action := strings.TrimSpace(task.ResourceAction); action != "" {
			actions = appendIfMissing(actions, action)
		}
	}
	return actions
}

func sentenceMentionsRequestedResource(sentence string, actions []string) bool {
	for _, action := range actions {
		switch strings.TrimSpace(action) {
		case "provide_location":
			if containsAnyReplyPhrase(sentence, []string{"定位", "位置", "地址", "导航"}) {
				return true
			}
		case "provide_mini_program", "send_miniprogram":
			if containsAnyReplyPhrase(sentence, []string{"小程序", "入住码", "入住入口", "办理入口"}) {
				return true
			}
		case "provide_phone":
			if containsAnyReplyPhrase(sentence, []string{"电话", "号码", "手机号", "联系方式"}) {
				return true
			}
		}
	}
	return false
}

func generatedResourceCommitPhrases() []string {
	return []string{
		"发你", "发给你", "给你发", "这边发", "我这边发", "我这边按入口发", "按入口发",
		"已经发", "已发", "后续发", "稍后发", "马上发", "点开就能", "会发",
	}
}

func unsupportedFirstPersonStaffActionPhrases() []string {
	return []string{
		"我让同事", "我叫同事", "我喊同事", "我找同事", "我这边找同事", "我这边需要找同事",
		"我帮你转给同事", "我转给同事", "我反馈给同事", "我通知同事", "我安排同事", "我帮你转达", "我转达",
		"我帮你找人", "我再帮你找人", "找人来处理", "帮你找人来处理",
		"我让前台", "我联系前台", "我帮你转前台", "我帮你转达给前台", "我帮你转人工", "我帮你转过去", "转达给前台", "转给前台", "转过去",
		"联系前台工作人员", "前台工作人员帮你处理", "工作人员帮你处理", "帮你处理", "去前台说一下", "方便去前台",
		"转前台同事", "转达给前台同事", "前台同事来跟进", "前台同事处理", "我帮你问", "我帮你确认", "我帮你查", "我查一下", "我先查", "我先问",
		"我看看", "我看下", "帮你看看", "帮您看看", "我看看怎么", "看看怎么帮", "我这边看看",
		"同事过去", "同事过来", "同事查看", "同事处理", "同事接手", "同事上门",
		"需要同事查看", "需要同事处理", "需要同事接手", "得让同事", "要让同事", "让同事去", "让同事过来",
		"问一下同事", "问下同事", "咨询同事",
		"需要现场看", "现场看一下", "现场看看", "现场看", "现场处理",
	}
}

func filterReplySentences(text string, shouldRemove func(string) bool) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	cleanedLines := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}
		sentences := splitReplySentences(trimmedLine)
		kept := make([]string, 0, len(sentences))
		for _, sentence := range sentences {
			if strings.TrimSpace(sentence) == "" || shouldRemove(sentence) {
				continue
			}
			kept = append(kept, sentence)
		}
		if len(kept) > 0 {
			cleanedLines = append(cleanedLines, strings.TrimSpace(strings.Join(kept, "")))
		}
	}
	return strings.Join(cleanedLines, "\n")
}

func splitReplySentences(text string) []string {
	var ret []string
	var b strings.Builder
	for _, r := range text {
		b.WriteRune(r)
		if isReplySentenceDelimiter(r) {
			if sentence := strings.TrimSpace(b.String()); sentence != "" {
				ret = append(ret, sentence)
			}
			b.Reset()
		}
	}
	if sentence := strings.TrimSpace(b.String()); sentence != "" {
		ret = append(ret, sentence)
	}
	return ret
}

func isReplySentenceDelimiter(r rune) bool {
	switch r {
	case '。', '！', '？', '，', '、', '!', '?', ',', ';', '；':
		return true
	default:
		return false
	}
}

func containsAnyReplyPhrase(text string, phrases []string) bool {
	compact := compactReplyText(text)
	if compact == "" {
		return false
	}
	for _, phrase := range phrases {
		if phrase = compactReplyText(phrase); phrase != "" && strings.Contains(compact, phrase) {
			return true
		}
	}
	return false
}

func compactReplyText(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		if unicode.IsSpace(r) {
			continue
		}
		switch r {
		case '，', ',', '。', '.', '！', '!', '？', '?', '：', ':', '；', ';', '“', '”', '"', '\'', '、':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normalizeReplyTextWhitespace(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	ret := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			ret = append(ret, line)
		}
	}
	return strings.Join(ret, "\n")
}

func appendValidationReason(current string, addition string) string {
	current = strings.TrimSpace(current)
	addition = strings.TrimSpace(addition)
	if addition == "" || strings.Contains(current, addition) {
		return current
	}
	if current == "" {
		return addition
	}
	return current + "; " + addition
}
