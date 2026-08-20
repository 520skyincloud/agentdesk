package executor

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"agent-desk/internal/ai/runtime/contracts"
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
	cleaned, correctionScoped, actionCleaned := cleanGeneratedReplyTextForTasks(
		original,
		collector.Data.Pipeline.Intent,
		summary.ReplyPlanV2,
		nil,
	)
	if cleaned == "" || cleaned == original {
		return
	}
	summary.ReplyText = cleaned
	collector.Data.Output.ReplyText = cleaned
	collector.Data.Pipeline.Validate.Status = "passed"
	if correctionScoped {
		collector.Data.Pipeline.Validate.Reason = appendValidationReason(
			collector.Data.Pipeline.Validate.Reason,
			"correction reply was scoped to the current correction and did not continue an older topic",
		)
	}
	if actionCleaned {
		collector.Data.Pipeline.Validate.Reason = appendValidationReason(
			collector.Data.Pipeline.Validate.Reason,
			"action ledger removed unsupported actions or customer-field requests, or normalized an incomplete reply ending",
		)
	}
}

func cleanGeneratedReplyText(original string, intent callbacks.IntentTraceData) (cleaned string, correctionScoped bool, actionCleaned bool) {
	return cleanGeneratedReplyTextForTasks(original, intent, nil, nil)
}

func cleanGeneratedReplyTextForTasks(
	original string,
	intent callbacks.IntentTraceData,
	plan *contracts.ReplyPlanV2,
	taskKeys []string,
) (cleaned string, correctionScoped bool, actionCleaned bool) {
	original = strings.TrimSpace(original)
	if original == "" {
		return "", false, false
	}
	scoped := limitCorrectionReplyToCurrentTurn(original, intent)
	cleaned = removeStructuredResourceCommitMentions(scoped, intent)
	cleaned = removeUnsupportedStaffActionMentions(cleaned, intent)
	if replyTasksDisallowCustomerFieldCollection(intent, plan, taskKeys) {
		cleaned = removeUnsupportedCustomerFieldCollection(cleaned)
	}
	cleaned = normalizeReplyTextWhitespace(cleaned)
	cleaned = normalizeIncompleteReplyEnding(cleaned)
	return cleaned, scoped != original, cleaned != scoped
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

func removeUnsupportedStaffActionMentions(text string, intent callbacks.IntentTraceData) string {
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
		"我帮你联系前台", "帮你联系前台",
		"联系前台工作人员", "前台工作人员帮你处理", "工作人员帮你处理", "帮你处理", "去前台说一下", "方便去前台",
		"转前台同事", "转达给前台同事", "前台同事来跟进", "前台同事处理", "我帮你问", "我帮你确认", "我帮你查", "我查一下", "我先查", "我先问",
		"我看看", "我看下", "帮你看看", "帮您看看", "我看看怎么", "看看怎么帮", "我这边看看",
		"同事过去", "同事过来", "同事查看", "同事处理", "同事接手", "同事上门",
		"需要同事查看", "需要同事处理", "需要同事接手", "得让同事", "要让同事", "让同事去", "让同事过来",
		"问一下同事", "问下同事", "咨询同事",
		"需要现场看", "现场看一下", "现场看看", "现场看", "现场处理",
		"请联系前台", "建议联系前台", "可以联系前台", "可联系前台", "咨询前台", "询问前台",
		"前台确认", "前台会帮", "前台可以帮", "前台帮你", "前台协助", "由前台处理", "让前台处理",
		"后续有人联系", "后续会有人联系", "稍后有人联系", "稍后会有人联系",
		"工作人员会联系", "同事会联系", "客服会联系", "等待工作人员联系",
	}
}

func replyTasksDisallowCustomerFieldCollection(
	intent callbacks.IntentTraceData,
	plan *contracts.ReplyPlanV2,
	taskKeys []string,
) bool {
	if plan != nil {
		matched := false
		for _, task := range plan.Tasks {
			if len(taskKeys) > 0 && !taskKeyCovered(taskKeys, task.TaskKey) {
				continue
			}
			matched = true
			if stringInSlice("do_not_collect_customer_fields", task.Constraints) || runtimeKnowledgeBoundaryStatus(task.Knowledge.Status) {
				return true
			}
			if replyPlanTaskCanCollectCustomerFields(task, intent) {
				continue
			}
			if task.Intent == "hotel_info" || task.Intent == "service_request" || isCustomerFieldSensitiveSubIntent(task.SubIntent) {
				return true
			}
		}
		if matched {
			return false
		}
	}
	if intentHasExecutionCapability(intent) {
		return false
	}
	return intent.PrimaryIntent == "hotel_info" || intent.PrimaryIntent == "service_request" ||
		intent.NeedsKnowledge || isCustomerFieldSensitiveSubIntent(intent.SubIntent)
}

func replyPlanTaskCanCollectCustomerFields(task contracts.ReplyPlanTaskV2, intent callbacks.IntentTraceData) bool {
	if len(task.ActionRefs) > 0 {
		return true
	}
	matched := false
	for _, intentTask := range intent.IntentTasks {
		if strings.TrimSpace(intentTask.Intent) != strings.TrimSpace(task.Intent) ||
			strings.TrimSpace(intentTask.SubIntent) != strings.TrimSpace(task.SubIntent) {
			continue
		}
		matched = true
		if intentTask.NeedsTool {
			return true
		}
	}
	if matched {
		return false
	}
	return intentHasExecutionCapability(intent)
}

func intentHasExecutionCapability(intent callbacks.IntentTraceData) bool {
	return intent.NeedsTool || len(intent.ToolCodes) > 0
}

func isCustomerFieldSensitiveSubIntent(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "room_change", "change_room", "room_upgrade", "upgrade_room", "room_type_change",
		"discount", "promotion", "coupon", "member_discount", "price_discount":
		return true
	default:
		return false
	}
}

func removeUnsupportedCustomerFieldCollection(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	cleanedLines := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cleanedSentences := make([]string, 0)
		for _, sentence := range splitTerminalReplySentences(line) {
			cleaned := stripUnsupportedCustomerFieldCollection(sentence)
			if cleaned != "" {
				cleanedSentences = append(cleanedSentences, cleaned)
			}
		}
		if len(cleanedSentences) > 0 {
			cleanedLines = append(cleanedLines, strings.TrimSpace(strings.Join(cleanedSentences, "")))
		}
	}
	return strings.Join(cleanedLines, "\n")
}

func stripUnsupportedCustomerFieldCollection(sentence string) string {
	sentence = strings.TrimSpace(sentence)
	if sentence == "" || !containsCustomerFieldReference(sentence) {
		return sentence
	}
	start, collects := customerFieldCollectionStart(sentence)
	if !collects {
		return sentence
	}
	prefix := strings.TrimSpace(sentence[:start])
	prefix = strings.TrimRight(prefix, "，,：:、；; ")
	if !containsAnyReplyPhrase(prefix, directCapabilityBoundaryPhrases()) {
		return ""
	}
	return normalizeIncompleteReplyEnding(prefix + "。")
}

func customerFieldCollectionStart(sentence string) (int, bool) {
	if customerFieldReferenceIsSelfServiceInstruction(sentence) {
		return 0, false
	}
	earliest := len(sentence)
	for _, signal := range customerFieldCollectionSignals() {
		from := 0
		for from < len(sentence) {
			index := strings.Index(sentence[from:], signal)
			if index < 0 {
				break
			}
			index += from
			if !customerFieldCollectionSignalIsNegated(sentence[:index]) &&
				customerFieldCollectionSignalReferencesField(sentence, index, signal) && index < earliest {
				earliest = index
			}
			from = index + len(signal)
		}
	}
	compact := compactReplyText(sentence)
	if earliest == len(sentence) && strings.ContainsAny(sentence, "？?") && containsAnyReplyPhrase(compact, customerFieldQuestionPhrases()) {
		return 0, true
	}
	if earliest == len(sentence) {
		return 0, false
	}
	return earliest, true
}

func customerFieldCollectionSignalReferencesField(sentence string, index int, signal string) bool {
	if containsAnyReplyPhrase(signal, []string{"发我", "发给我", "告诉我", "告知我", "给我", "报一下", "说一下", "说下"}) {
		return containsCustomerFieldReference(sentence)
	}
	return containsCustomerFieldReference(sentence[index:])
}

func customerFieldReferenceIsSelfServiceInstruction(sentence string) bool {
	compact := compactReplyText(sentence)
	if !containsAny(compact, []string{"小程序", "自助机", "入住机", "页面", "表单", "系统内", "系统里", "平台内", "平台里"}) {
		return false
	}
	if containsAny(compact, []string{"发我", "发给我", "告诉我", "告知我", "给我回复", "回复我", "报给我"}) {
		return false
	}
	return containsAny(compact, []string{"输入", "填写", "填入", "录入", "提交"})
}

func customerFieldCollectionSignalIsNegated(prefix string) bool {
	compact := compactReplyText(prefix)
	for _, negation := range []string{"不", "无需", "不用", "不必", "不要", "无法通过", "不能通过"} {
		if strings.HasSuffix(compact, negation) {
			return true
		}
	}
	return false
}

func containsCustomerFieldReference(text string) bool {
	return containsAnyReplyPhrase(text, customerFieldReferencePhrases())
}

func customerFieldReferencePhrases() []string {
	return []string{
		"房号", "房间号", "入住的房间",
		"你在哪个房间", "您在哪个房间", "你现在在哪个房间", "您现在在哪个房间",
		"你住哪个房间", "您住哪个房间", "你住哪间房", "您住哪间房",
		"订单号", "预订号", "订单编号", "预订编号",
		"姓名", "名字", "入住人", "预订人",
		"手机号", "手机号码", "联系电话", "联系方式",
		"身份证号", "身份证号码", "证件号", "证件号码", "微信号",
	}
}

func customerFieldCollectionSignals() []string {
	return []string{
		"请提供", "麻烦提供", "需要你提供", "需要您提供", "需要提供", "提供一下",
		"请发", "麻烦发", "发我", "发给我", "告诉我", "告知我", "给我",
		"留一下", "报一下", "说一下", "说下", "登记一下", "请把", "麻烦把", "把你的", "把您的",
		"请问你的", "请问您的", "需要你的", "需要您的",
	}
}

func customerFieldQuestionPhrases() []string {
	return []string{
		"房号是多少", "房间号是多少", "订单号是多少", "预订号是多少", "手机号是多少",
		"你在哪个房间", "您在哪个房间", "你现在在哪个房间", "您现在在哪个房间",
		"你住哪个房间", "您住哪个房间",
		"你住哪间房", "您住哪间房", "你的姓名", "您的姓名", "你的名字", "您的名字",
		"哪个订单", "哪笔订单", "预订人是谁", "入住人是谁",
	}
}

func directCapabilityBoundaryPhrases() []string {
	return []string{
		"当前不能", "目前不能", "现在不能", "暂时不能", "暂不能",
		"当前无法", "目前无法", "现在无法", "暂时无法", "暂无法", "没法",
		"不支持", "暂不支持", "不能办理", "无法办理", "没有办理", "不能操作", "无法操作",
		"不能确认", "无法确认", "不能查询", "无法查询", "资料没写明", "资料未写明", "资料无法确认",
		"不能承诺", "无法承诺", "不能保证", "无法保证",
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
