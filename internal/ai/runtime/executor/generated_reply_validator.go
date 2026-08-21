package executor

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

type generatedReplyValidationOutcome struct {
	RequestHandoffConfirmation bool
	HandoffReason              string
}

func enforceGeneratedReplyActionLedger(summary *RunResult, collector *callbacks.RuntimeTraceCollector) generatedReplyValidationOutcome {
	outcome := generatedReplyValidationOutcome{}
	if summary == nil || collector == nil {
		return outcome
	}
	original := strings.TrimSpace(summary.ReplyText)
	if original == "" {
		return outcome
	}
	intent := collector.Data.Pipeline.Intent
	scoped := limitCorrectionReplyToCurrentTurn(original, intent)
	cleaned := removeStructuredResourceCommitMentions(scoped, intent)
	humanRouteCommitted := actionLedgerContainsAction(collector.Data.ActionLedger.CommittedActions, "human_route")
	cleaned = removeUnsupportedStaffActionMentions(cleaned, humanRouteCommitted)
	cleaned = normalizeReplyTextWhitespace(cleaned)
	cleaned = normalizeIncompleteReplyEnding(cleaned)
	if !humanRouteCommitted && containsUnsupportedHandoffPromise(original) && isHandoffFillerOnly(cleaned) {
		cleaned = ""
	}
	if cleaned == "" {
		if !humanRouteCommitted && containsUnsupportedHandoffPromise(original) {
			summary.ReplyText = ""
			collector.Data.Output.ReplyText = ""
			collector.Data.Pipeline.Validate.Status = "passed"
			collector.Data.Pipeline.Validate.Reason = appendValidationReason(
				collector.Data.Pipeline.Validate.Reason,
				"unsupported handoff promise was replaced by persisted handoff confirmation",
			)
			outcome.RequestHandoffConfirmation = true
			outcome.HandoffReason = "生成回复要求门店同事接手，但尚未执行真实转接；客户消息需要人工确认"
			return outcome
		}
		cleaned = "这个问题我目前还没有足够准确的资料。"
	}
	if cleaned == original {
		return outcome
	}
	summary.ReplyText = cleaned
	collector.Data.Output.ReplyText = cleaned
	collector.Data.Pipeline.Validate.Status = "passed"
	if scoped != original {
		collector.Data.Pipeline.Validate.Reason = appendValidationReason(
			collector.Data.Pipeline.Validate.Reason,
			"correction reply was scoped to the current correction and did not continue an older topic",
		)
	}
	if cleaned != scoped {
		collector.Data.Pipeline.Validate.Reason = appendValidationReason(
			collector.Data.Pipeline.Validate.Reason,
			"action ledger removed unsupported actions or normalized an incomplete reply ending",
		)
	}
	return outcome
}

func limitCorrectionReplyToCurrentTurn(text string, intent callbacks.IntentTraceData) string {
	if intent.PrimaryIntent != "interaction" || !isSocialCorrectionSubIntent(intent.SubIntent) {
		return text
	}
	sentences := splitTerminalReplySentences(text)
	if len(sentences) == 0 {
		return text
	}
	return sentences[0]
}

func splitTerminalReplySentences(text string) []string {
	var ret []string
	var b strings.Builder
	for _, r := range strings.TrimSpace(text) {
		b.WriteRune(r)
		if isTerminalReplySentenceDelimiter(r) {
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

func isTerminalReplySentenceDelimiter(r rune) bool {
	switch r {
	case '。', '！', '？', '!', '?', '；', ';':
		return true
	default:
		return false
	}
}

func normalizeIncompleteReplyEnding(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || !endsWithIncompleteReplyDelimiter(text) {
		return text
	}
	text = strings.TrimRight(text, "，,：:、；; \t\r\n")
	if text == "" {
		return ""
	}
	if index := strings.LastIndexAny(text, "，,"); index >= 0 {
		lastClause := strings.TrimSpace(text[index+1:])
		if utf8.RuneCountInString(lastClause) <= 4 {
			text = strings.TrimSpace(text[:index])
		}
	}
	text = strings.TrimRight(strings.TrimSpace(text), "，,：:、；; \t\r\n")
	if text == "" {
		return ""
	}
	return text + "。"
}

func endsWithIncompleteReplyDelimiter(text string) bool {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return false
	}
	switch runes[len(runes)-1] {
	case '，', ',', '：', ':', '、', '；', ';':
		return true
	default:
		return false
	}
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

func removeUnsupportedStaffActionMentions(text string, humanRouteCommitted bool) string {
	if humanRouteCommitted {
		return text
	}
	text = removeUnsupportedActionClauses(text, unsupportedFirstPersonRecordActionPhrases())
	return filterReplySentences(text, func(sentence string) bool {
		return containsAnyReplyPhrase(sentence, unsupportedFirstPersonStaffActionPhrases())
	})
}

func removeUnsupportedActionClauses(text string, phrases []string) string {
	for {
		start := -1
		for _, phrase := range phrases {
			if index := strings.Index(text, phrase); index >= 0 && (start < 0 || index < start) {
				start = index
			}
		}
		if start < 0 {
			return text
		}
		end := len(text)
		for offset, r := range text[start:] {
			if isReplySentenceDelimiter(r) {
				end = start + offset + utf8.RuneLen(r)
				break
			}
		}
		clauseStart := 0
		for offset, r := range text[:start] {
			if isReplySentenceDelimiter(r) || r == '\n' {
				clauseStart = offset + utf8.RuneLen(r)
			}
		}
		if isUnsupportedRecordActionFillerPrefix(text[clauseStart:start]) {
			start = clauseStart
		}
		prefix := strings.TrimRightFunc(text[:start], unicode.IsSpace)
		for _, connector := range []string{"同时", "另外", "然后", "并且", "并", "也"} {
			if strings.HasSuffix(prefix, connector) {
				prefix = strings.TrimSpace(strings.TrimSuffix(prefix, connector))
				break
			}
		}
		text = prefix + strings.TrimLeftFunc(text[end:], unicode.IsSpace)
	}
}

func isUnsupportedRecordActionFillerPrefix(text string) bool {
	compact := compactReplyText(text)
	if compact == "" || utf8.RuneCountInString(compact) > 16 {
		return false
	}
	for _, suffix := range []string{"的事", "这个事", "这件事", "这个问题", "的问题", "这边"} {
		if strings.HasSuffix(compact, suffix) {
			return true
		}
	}
	return false
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
	phrases := append([]string{}, unsupportedFirstPersonRecordActionPhrases()...)
	return append(phrases,
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
		"我先帮你把信息转给人工", "我先帮您把信息转给人工", "我先把你的情况交过去", "我先把您的情况交过去",
		"稍等我帮你转一下", "稍等我帮您转一下", "我帮你转一下", "我帮您转一下",
	)
}

func unsupportedFirstPersonRecordActionPhrases() []string {
	return []string{
		"我先记下", "我记下", "我已记下", "我已经记下", "我帮你记下", "我帮您记下",
		"我先记一下", "我记一下", "我帮你记一下", "我帮您记一下",
		"我这边先记下", "我这边记下", "我这边先记一下", "我这边记一下",
		"我先记录", "我记录一下", "我已记录", "我已经记录", "我帮你记录", "我帮您记录",
		"我先登记", "我登记一下", "我已登记", "我已经登记", "我帮你登记", "我帮您登记",
		"我先受理", "我已受理", "我已经受理", "我帮你受理", "我帮您受理",
		"我先处理", "我已处理", "我已经处理", "我帮您处理",
	}
}

func containsUnsupportedHandoffPromise(text string) bool {
	compact := compactReplyText(text)
	if compact == "" {
		return false
	}
	if containsAnyReplyPhrase(compact, []string{"转人工", "转前台", "转给人工", "转给同事", "转给工作人员", "转达给前台", "转达给同事"}) {
		return true
	}
	if strings.Contains(compact, "交过去") && containsAnyReplyPhrase(compact, []string{"我先", "我帮", "你的情况", "您的情况", "信息"}) {
		return true
	}
	return strings.Contains(compact, "转一下") && containsAnyReplyPhrase(compact, []string{"我先", "我帮你", "我帮您", "稍等"})
}

func isHandoffFillerOnly(text string) bool {
	switch compactReplyText(text) {
	case "", "稍等", "请稍等", "好的稍等", "好稍等", "稍等一下", "请稍等一下":
		return true
	default:
		return false
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
