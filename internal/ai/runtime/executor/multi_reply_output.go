package executor

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
)

type generatedReplyPartsEnvelope struct {
	ReplyParts []generatedReplyPart `json:"replyParts"`
}

type generatedReplyPart struct {
	TaskID         string   `json:"taskId"`
	Content        string   `json:"content"`
	CoveredFactIDs []string `json:"coveredFactIds,omitempty"`
}

type textReplyTaskGroup struct {
	TaskID             string
	Texts              []string
	Facts              []replyFactRequirement
	StructuredRequired bool
}

type replyFactRequirement struct {
	FactID         string
	Aspect         string
	Statement      string
	CriticalValues []string
}

// ErrGeneratedReplyProtocol marks generated output that failed the internal
// replyParts contract and is safe to retry without exposing the raw payload.
var ErrGeneratedReplyProtocol = errors.New("generated reply protocol validation failed")

var errGeneratedReplyProtocol = ErrGeneratedReplyProtocol

const generatedReplySingleTaskMaxOutputTokens = 512

func IsGeneratedReplyProtocolError(err error) bool {
	return errors.Is(err, ErrGeneratedReplyProtocol)
}

func replyPlanRequiresStructuredOutput(plan callbacks.ReplyPlanTraceData, explicitlyRequired bool) bool {
	groups := buildTextReplyTaskGroups(plan)
	return len(groups) > 0 && requiresStructuredReplyParts(groups, explicitlyRequired)
}

func generatedReplyAIConfigForPlan(config models.AIConfig, plan callbacks.ReplyPlanTraceData) models.AIConfig {
	if len(buildTextReplyTaskGroups(plan)) > 1 {
		return config
	}
	if config.MaxOutputTokens <= 0 || config.MaxOutputTokens > generatedReplySingleTaskMaxOutputTokens {
		config.MaxOutputTokens = generatedReplySingleTaskMaxOutputTokens
	}
	return config
}

func normalizeStructuredReplyPromptText(text string) string {
	text = strings.ReplaceAll(text, "回复像微信真人，通常 1-3 句。", "每个 replyParts.content 只回答对应任务，普通问题用 1-2 句，流程问题可用 2-3 个简短步骤。")
	text = strings.ReplaceAll(text, "回复像微信真人，通常1-3句。", "每个 replyParts.content 只回答对应任务，普通问题用 1-2 句，流程问题可用 2-3 个简短步骤。")
	text = strings.ReplaceAll(text, "自然微信口吻，1-3句", "每个 replyParts.content 只回答对应任务，普通问题用 1-2 句，流程问题可用 2-3 个简短步骤")
	text = strings.ReplaceAll(text, "最终回复只输出给客人的话", "replyParts 的 content 只写给客人的话")
	text = strings.ReplaceAll(text, "最终文本只输出给客人的话", "replyParts 的 content 只写给客人的话")
	return text
}

func buildMultiReplyOutputInstruction(plan callbacks.ReplyPlanTraceData, requireStructured bool) string {
	groups := buildTextReplyTaskGroups(plan)
	if len(groups) == 0 || (len(groups) <= 1 && !requiresStructuredReplyParts(groups, requireStructured)) {
		return ""
	}
	var b strings.Builder
	b.WriteString("【任务输出契约】本轮只允许回答下面列出的文本任务。只输出一个 JSON 对象，不要输出 Markdown 代码块或 JSON 之外的文字。格式为：")
	example := generatedReplyPartsEnvelope{ReplyParts: make([]generatedReplyPart, 0, len(groups))}
	for _, group := range groups {
		part := generatedReplyPart{TaskID: group.TaskID, Content: "给客户的自然回复"}
		for _, fact := range group.Facts {
			part.CoveredFactIDs = append(part.CoveredFactIDs, fact.FactID)
		}
		example.ReplyParts = append(example.ReplyParts, part)
	}
	exampleJSON, _ := json.Marshal(example)
	b.Write(exampleJSON)
	b.WriteString("。JSON 外层是内部协议；只有 content 是客户可见回复。replyParts 必须按以下任务顺序输出，每个文本任务恰好一项，不得遗漏、合并、重复或增加 taskId。每个 content 只回答对应任务，只使用该任务列出的事实，普通问题用 1-2 句，流程问题可用 2-3 个简短步骤。不要写 <<NEXT_MESSAGE>>，也不要把结构化变量动作写进 content。coveredFactIds 只能填写该任务下列出的事实 ID；存在必答事实时必须全部覆盖。同一句事实对应多个事实 ID 时，coveredFactIds 必须全部列出，但 content 只自然表达一次。严格遵守事实维度：existence 只证明存在或不存在，不能扩写为配送范围、使用方法、地点、时间或已执行的服务承诺。程序会按任务顺序合并为最多三条客户消息。\n")
	for _, group := range groups {
		b.WriteString("- ")
		b.WriteString(group.TaskID)
		b.WriteString("：")
		b.WriteString(strings.Join(group.Texts, "；"))
		b.WriteString("\n")
		for _, fact := range groupReplyFactRequirementsForInstruction(group.Facts) {
			b.WriteString("  - 必答事实 ")
			b.WriteString(strings.Join(fact.FactIDs, "、"))
			if len(fact.FactIDs) > 1 {
				b.WriteString("（同一事实，content 只表达一次）")
			}
			b.WriteString("：")
			b.WriteString(fact.Statement)
			if len(fact.Aspects) > 0 {
				b.WriteString("；事实维度：")
				b.WriteString(strings.Join(fact.Aspects, "、"))
			}
			if len(fact.CriticalValues) > 0 {
				b.WriteString("；回复中必须原样包含：")
				b.WriteString(strings.Join(fact.CriticalValues, "、"))
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

type replyFactInstructionGroup struct {
	FactIDs        []string
	Aspects        []string
	Statement      string
	CriticalValues []string
}

func groupReplyFactRequirementsForInstruction(facts []replyFactRequirement) []replyFactInstructionGroup {
	ret := make([]replyFactInstructionGroup, 0, len(facts))
	byStatement := make(map[string]int, len(facts))
	for _, fact := range facts {
		statement := strings.TrimSpace(fact.Statement)
		key := normalizeRuntimeKnowledgeQuery(statement)
		if key == "" {
			key = "fact-id:" + strings.TrimSpace(fact.FactID)
		}
		if index, ok := byStatement[key]; ok {
			if factID := strings.TrimSpace(fact.FactID); factID != "" {
				ret[index].FactIDs = appendIfMissing(ret[index].FactIDs, factID)
			}
			if aspect := strings.TrimSpace(fact.Aspect); aspect != "" {
				ret[index].Aspects = appendIfMissing(ret[index].Aspects, aspect)
			}
			ret[index].CriticalValues = appendKnowledgeEvidenceCriticalValues(ret[index].CriticalValues, fact.CriticalValues)
			continue
		}
		byStatement[key] = len(ret)
		item := replyFactInstructionGroup{
			FactIDs:        []string{strings.TrimSpace(fact.FactID)},
			Statement:      statement,
			CriticalValues: append([]string(nil), fact.CriticalValues...),
		}
		if aspect := strings.TrimSpace(fact.Aspect); aspect != "" {
			item.Aspects = []string{aspect}
		}
		ret = append(ret, item)
	}
	for {
		merged := false
		for containedIndex := 0; containedIndex < len(ret) && !merged; containedIndex++ {
			for containerIndex := 0; containerIndex < len(ret); containerIndex++ {
				if containedIndex == containerIndex || !replyFactInstructionGroupContains(ret[containerIndex], ret[containedIndex]) {
					continue
				}
				container := ret[containerIndex]
				contained := ret[containedIndex]
				if containedIndex < containerIndex {
					container.FactIDs = appendUniqueStrings(contained.FactIDs, container.FactIDs)
					container.Aspects = appendUniqueStrings(contained.Aspects, container.Aspects)
					container.CriticalValues = appendKnowledgeEvidenceCriticalValues(contained.CriticalValues, container.CriticalValues)
					ret[containedIndex] = container
					ret = append(ret[:containerIndex], ret[containerIndex+1:]...)
				} else {
					container.FactIDs = appendUniqueStrings(container.FactIDs, contained.FactIDs)
					container.Aspects = appendUniqueStrings(container.Aspects, contained.Aspects)
					container.CriticalValues = appendKnowledgeEvidenceCriticalValues(container.CriticalValues, contained.CriticalValues)
					ret[containerIndex] = container
					ret = append(ret[:containedIndex], ret[containedIndex+1:]...)
				}
				merged = true
				break
			}
		}
		if !merged {
			break
		}
	}
	return ret
}

func replyFactInstructionGroupContains(container replyFactInstructionGroup, contained replyFactInstructionGroup) bool {
	return generatedReplyFallbackFactContains(
		replyFactRequirement{Aspect: strings.Join(container.Aspects, ","), Statement: container.Statement, CriticalValues: container.CriticalValues},
		replyFactRequirement{Aspect: strings.Join(contained.Aspects, ","), Statement: contained.Statement, CriticalValues: contained.CriticalValues},
	)
}

func appendUniqueStrings(existing []string, values []string) []string {
	ret := append([]string(nil), existing...)
	for _, value := range values {
		ret = appendIfMissing(ret, value)
	}
	return ret
}

func normalizeGeneratedReplyParts(text string, plan callbacks.ReplyPlanTraceData, requireStructured bool) string {
	normalized, _ := normalizeGeneratedReplyPartsResult(text, plan, requireStructured)
	return normalized
}

func normalizeGeneratedReplyPartsResult(text string, plan callbacks.ReplyPlanTraceData, requireStructured bool) (string, error) {
	groups := buildTextReplyTaskGroups(plan)
	if len(groups) == 0 {
		return "", nil
	}
	raw := strings.TrimSpace(text)
	envelope, parsed := parseGeneratedReplyParts(raw)
	if !parsed {
		if looksLikeGeneratedReplyPartsProtocol(raw) {
			return "", fmt.Errorf("%w: malformed replyParts payload", errGeneratedReplyProtocol)
		}
		if requiresStructuredReplyParts(groups, requireStructured) {
			return "", fmt.Errorf("%w: structured replyParts payload required", errGeneratedReplyProtocol)
		}
		return raw, nil
	}
	expectedTaskIDs := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		expectedTaskIDs[group.TaskID] = struct{}{}
	}
	contentByTaskID := make(map[string]string, len(envelope.ReplyParts))
	for _, part := range envelope.ReplyParts {
		taskID := strings.TrimSpace(part.TaskID)
		content := strings.TrimSpace(part.Content)
		if taskID == "" {
			return "", fmt.Errorf("%w: reply part is missing taskId", errGeneratedReplyProtocol)
		}
		if _, expected := expectedTaskIDs[taskID]; !expected {
			return "", fmt.Errorf("%w: unknown taskId %s", errGeneratedReplyProtocol, taskID)
		}
		if _, exists := contentByTaskID[taskID]; exists {
			return "", fmt.Errorf("%w: duplicate taskId %s", errGeneratedReplyProtocol, taskID)
		}
		if content == "" {
			return "", fmt.Errorf("%w: missing content for %s", errGeneratedReplyProtocol, taskID)
		}
		if containsReplyMessageMarker(content) {
			return "", fmt.Errorf("%w: content for %s contains an internal message marker", errGeneratedReplyProtocol, taskID)
		}
		group := groupByTaskID(groups, taskID)
		if err := validateCoveredFacts(part, group); err != nil {
			return "", err
		}
		content = compactGeneratedReplyContent(content)
		if content == "" {
			return "", fmt.Errorf("%w: content for %s became empty after duplicate removal", errGeneratedReplyProtocol, taskID)
		}
		contentByTaskID[taskID] = content
	}
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		content := strings.TrimSpace(contentByTaskID[group.TaskID])
		if content == "" {
			return "", fmt.Errorf("%w: missing content for %s", errGeneratedReplyProtocol, group.TaskID)
		}
		parts = append(parts, content)
	}
	return composeGeneratedReplyContents(parts, 3), nil
}

func requiresStructuredReplyParts(groups []textReplyTaskGroup, explicitlyRequired bool) bool {
	if explicitlyRequired || len(groups) > 1 {
		return true
	}
	for _, group := range groups {
		if group.StructuredRequired || len(group.Facts) > 0 {
			return true
		}
	}
	return false
}

func groupByTaskID(groups []textReplyTaskGroup, taskID string) textReplyTaskGroup {
	for _, group := range groups {
		if group.TaskID == taskID {
			return group
		}
	}
	return textReplyTaskGroup{}
}

func validateCoveredFacts(part generatedReplyPart, group textReplyTaskGroup) error {
	expected := make(map[string]replyFactRequirement, len(group.Facts))
	for _, fact := range group.Facts {
		expected[fact.FactID] = fact
	}
	covered := make(map[string]struct{}, len(part.CoveredFactIDs))
	for _, rawFactID := range part.CoveredFactIDs {
		rawFactID = strings.TrimSpace(rawFactID)
		if rawFactID == "" {
			return fmt.Errorf("%w: empty coveredFactId for %s", errGeneratedReplyProtocol, group.TaskID)
		}
		factID, err := normalizeCoveredFactID(rawFactID, group)
		if err != nil {
			return err
		}
		if _, exists := covered[factID]; exists {
			return fmt.Errorf("%w: duplicate coveredFactId %s for %s", errGeneratedReplyProtocol, rawFactID, group.TaskID)
		}
		covered[factID] = struct{}{}
	}
	for _, fact := range group.Facts {
		if _, ok := covered[fact.FactID]; !ok {
			return fmt.Errorf("%w: missing coveredFactId %s for %s", errGeneratedReplyProtocol, fact.FactID, group.TaskID)
		}
		for _, criticalValue := range sanitizeKnowledgeEvidenceCriticalValuesForStatement(fact.CriticalValues, fact.Statement) {
			if !containsCriticalValue(part.Content, criticalValue) {
				return fmt.Errorf("%w: content for %s is missing critical value %s", errGeneratedReplyProtocol, group.TaskID, criticalValue)
			}
		}
	}
	return nil
}

func validateGeneratedReplyFactAspectBoundaries(content string, facts []replyFactRequirement) error {
	if len(facts) == 0 {
		return nil
	}
	evidence := make([]string, 0, len(facts))
	for _, fact := range facts {
		if statement := strings.TrimSpace(fact.Statement); statement != "" {
			evidence = append(evidence, statement)
		}
	}
	if len(evidence) == 0 {
		return nil
	}
	for _, clause := range splitGeneratedReplyFactClauses(content) {
		if generatedReplyClauseIsPureUncertaintyBoundary(clause) || generatedReplyClauseIsBareAffirmation(clause) || generatedReplyClauseIsServiceOffer(clause) {
			continue
		}
		for _, boundary := range generatedReplyFactAspectBoundaries() {
			if !generatedReplyTextHasBoundaryClaim(clause, boundary) {
				continue
			}
			if generatedReplyBoundaryClaimGroundedByFacts(clause, evidence, boundary) {
				continue
			}
			return fmt.Errorf("content adds unsupported %s claim", boundary.Aspect)
		}
		if generatedReplyTimeClaimPattern.MatchString(normalizeCriticalValueText(clause)) && !generatedReplyTimeClaimGroundedByFacts(clause, evidence) {
			return fmt.Errorf("content adds unsupported time claim")
		}
		if !generatedReplyLiteralClaimsGroundedByFacts(clause, evidence, generatedReplyQuantityClaimPattern) {
			return fmt.Errorf("content adds unsupported quantity claim")
		}
		if !generatedReplyLiteralClaimsGroundedByFacts(clause, evidence, generatedReplyPriceClaimPattern) {
			return fmt.Errorf("content adds unsupported price claim")
		}
	}
	return nil
}

type generatedReplyFactAspectBoundary struct {
	Aspect       string
	Markers      []string
	ClaimPattern *regexp.Regexp
}

func generatedReplyFactAspectBoundaries() []generatedReplyFactAspectBoundary {
	return []generatedReplyFactAspectBoundary{
		{Aspect: "scope", Markers: []string{"送到", "送至", "送上", "送进", "送达", "配送", "送房", "房门口", "所有房间", "全部房间", "每个房间", "任何房间", "都能", "都可以", "均可"}},
		{Aspect: "method", Markers: []string{"通过", "使用", "扫码", "点击", "输入", "操作", "办理", "填写", "可以写", "联系", "自动送", "帮您送", "帮你送", "给您送", "给你送", "已安排", "已经安排", "马上送", "稍后送"}},
		{Aspect: "location", Markers: []string{"位于", "地址是", "地址为", "一楼", "前台", "在房间里", "在房间内", "门口", "楼层", "旁边", "对面"}},
		{Aspect: "existence", Markers: []string{"不提供", "未提供", "没有", "未配备", "不配备", "不含", "设有", "配备", "提供", "包含"}, ClaimPattern: generatedReplyExistenceClaimPattern},
		{Aspect: "capability", Markers: []string{"不支持", "不允许", "不可以", "不能够", "不能", "可以", "能够", "支持", "允许"}, ClaimPattern: generatedReplyCapabilityClaimPattern},
		{Aspect: "policy", Markers: []string{"不需要", "必须", "需要", "仅限", "只能", "不得", "禁止", "要求", "规定", "应当"}, ClaimPattern: generatedReplyPolicyClaimPattern},
	}
}

func splitGeneratedReplyFactClauses(content string) []string {
	parts := strings.FieldsFunc(content, func(r rune) bool {
		switch r {
		case '\n', '\r', '。', '！', '!', '？', '?', '；', ';', '，', ',':
			return true
		default:
			return false
		}
	})
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			ret = append(ret, part)
		}
	}
	return ret
}

func generatedReplyClauseIsPureUncertaintyBoundary(clause string) bool {
	normalized := normalizeCriticalValueText(clause)
	normalized = strings.TrimSpace(strings.NewReplacer(
		"不好意思", "",
		"抱歉", "",
		"这个问题", "",
		"这个", "",
		"这点", "",
		"我", "",
		"目前", "",
		"暂时", "",
	).Replace(normalized))
	for _, marker := range []string{
		"不清楚", "不确定", "无法确认", "没法确认", "没法准确回答", "不能准确回答", "暂时不能确认", "资料没写", "资料未写", "没有写明", "未说明", "需要确认", "要问门店", "以门店实际为准",
	} {
		if strings.HasPrefix(normalized, marker) {
			return true
		}
	}
	return false
}

func generatedReplyClauseIsBareAffirmation(clause string) bool {
	switch normalizeRuntimeKnowledgeQuery(clause) {
	case "有", "有的", "是", "是的", "对", "对的", "没错", "可以", "可以的", "能", "能的", "没问题":
		return true
	default:
		return false
	}
}

func generatedReplyClauseIsServiceOffer(clause string) bool {
	return containsAny(normalizeCriticalValueText(clause), []string{
		"有需要随时说", "有需要可以说", "有问题随时说", "有问题可以说", "需要的话随时说", "还有需要的话", "其他需要的话",
	})
}

func generatedReplyTextHasBoundaryClaim(text string, boundary generatedReplyFactAspectBoundary) bool {
	normalized := normalizeCriticalValueText(text)
	if boundary.ClaimPattern != nil && boundary.ClaimPattern.MatchString(normalized) {
		return true
	}
	for _, marker := range boundary.Markers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func generatedReplyBoundaryClaimGroundedByFacts(clause string, evidence []string, boundary generatedReplyFactAspectBoundary) bool {
	normalizedClause := normalizeRuntimeKnowledgeQuery(clause)
	if normalizedClause == "" {
		return false
	}
	clauseNegative := knowledgeEvidenceTextHasNegativeBoundary(normalizedClause)
	comparableClause := generatedReplyBoundaryComparableText(normalizedClause, boundary.Markers)
	for _, fact := range evidence {
		for _, factClause := range splitGeneratedReplyFactClauses(fact) {
			normalizedFact := normalizeRuntimeKnowledgeQuery(factClause)
			if normalizedFact == "" || !generatedReplyTextHasBoundaryClaim(normalizedFact, boundary) || clauseNegative != knowledgeEvidenceTextHasNegativeBoundary(normalizedFact) {
				continue
			}
			if strings.Contains(normalizedFact, normalizedClause) || strings.Contains(normalizedClause, normalizedFact) || knowledgeEvidenceTextNGramSimilarity(normalizedClause, normalizedFact) >= 0.72 {
				return true
			}
			comparableFact := generatedReplyBoundaryComparableText(normalizedFact, boundary.Markers)
			if comparableClause != "" && comparableFact != "" &&
				(strings.Contains(comparableFact, comparableClause) || strings.Contains(comparableClause, comparableFact) || knowledgeEvidenceTextNGramSimilarity(comparableClause, comparableFact) >= 0.72) {
				return true
			}
		}
	}
	return false
}

func generatedReplyBoundaryComparableText(text string, markers []string) string {
	text = strings.ReplaceAll(normalizeRuntimeKnowledgeQuery(normalizeCriticalValueText(text)), "所有", "全部")
	for _, marker := range markers {
		text = strings.ReplaceAll(text, marker, "")
	}
	return strings.NewReplacer(
		"我们酒店", "",
		"本酒店", "",
		"本门店", "",
		"酒店", "",
		"门店", "",
		"这边", "",
		"目前", "",
		"另外", "",
		"房费", "",
		"含", "",
		"有", "",
		"不可", "",
		"可", "",
		"要", "",
		"得", "",
		"应", "",
		"办理入住", "入住",
		"携带身份证", "身份证",
		"带身份证", "身份证",
		"出示身份证", "身份证",
		"携带宠物", "宠物入住",
		"带宠物", "宠物入住",
		"有的", "",
		"是的", "",
		"可以的", "",
	).Replace(text)
}

var generatedReplyExistenceClaimPattern = regexp.MustCompile(`(^|酒店|门店|本店|店里|这里|客房|房间|房内|房费|套餐|价格)(有|没有|无|含|不含)([^需问]|$)`)
var generatedReplyCapabilityClaimPattern = regexp.MustCompile(`(^|宠物|客人|房间|客房|酒店|门店|机器人|停车场|小程序|发票|订单|会员).{0,8}(可|不可)(入住|使用|办理|携带|进入|送|开门|退房|支付|投屏|充电|停车)`)
var generatedReplyPolicyClaimPattern = regexp.MustCompile(`(^|入住|退房|登记|开门|发票|停车|早餐|宠物|儿童).{0,8}(要|得|应)(带|提供|出示|支付|办理|登记|遵守|收费|购买)`)

func generatedReplyTimeClaimGroundedByFacts(clause string, evidence []string) bool {
	claims := generatedReplyTimeClaimPattern.FindAllString(normalizeCriticalValueText(clause), -1)
	if len(claims) == 0 {
		return false
	}
	for _, claim := range claims {
		grounded := false
		for _, fact := range evidence {
			if strings.Contains(normalizeCriticalValueText(fact), claim) {
				grounded = true
				break
			}
		}
		if !grounded {
			return false
		}
	}
	return true
}

var generatedReplyTimeClaimPattern = regexp.MustCompile(`(?:[0-9]+(?:\.[0-9]+)?|[一二三四五六七八九十两]+)(?:分钟|小时|天|工作日)|[0-9]{1,2}[:：][0-9]{2}`)
var generatedReplyQuantityClaimPattern = regexp.MustCompile(`(?:[0-9]+(?:\.[0-9]+)?|[一二三四五六七八九十百两]+)(?:瓶|个|份|间|张|次|台|套)`)
var generatedReplyPriceClaimPattern = regexp.MustCompile(`(?:免费|不收费|免收费用|收费|[0-9]+(?:\.[0-9]+)?元)`)

func generatedReplyLiteralClaimsGroundedByFacts(clause string, evidence []string, pattern *regexp.Regexp) bool {
	claims := pattern.FindAllString(normalizeCriticalValueText(clause), -1)
	for _, claim := range claims {
		grounded := false
		for _, fact := range evidence {
			if strings.Contains(normalizeCriticalValueText(fact), claim) {
				grounded = true
				break
			}
		}
		if !grounded {
			return false
		}
	}
	return true
}

func normalizeCoveredFactID(rawFactID string, group textReplyTaskGroup) (string, error) {
	rawFactID = strings.TrimSpace(rawFactID)
	for _, fact := range group.Facts {
		if strings.TrimSpace(fact.FactID) == rawFactID {
			return rawFactID, nil
		}
	}
	rawScope, rawSuffix := splitCoveredFactID(rawFactID)
	if rawSuffix == "" {
		return "", fmt.Errorf("%w: unknown coveredFactId %s for %s", errGeneratedReplyProtocol, rawFactID, group.TaskID)
	}
	if rawScope != "" && !equivalentCoveredFactTaskScope(rawScope, group.TaskID) {
		return "", fmt.Errorf("%w: unknown coveredFactId %s for %s", errGeneratedReplyProtocol, rawFactID, group.TaskID)
	}
	matches := make([]string, 0, 1)
	for _, fact := range group.Facts {
		expectedFactID := strings.TrimSpace(fact.FactID)
		_, expectedSuffix := splitCoveredFactID(expectedFactID)
		if expectedSuffix == "" || expectedSuffix != rawSuffix {
			continue
		}
		matches = append(matches, expectedFactID)
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("%w: unknown coveredFactId %s for %s", errGeneratedReplyProtocol, rawFactID, group.TaskID)
	default:
		return "", fmt.Errorf("%w: ambiguous coveredFactId %s for %s", errGeneratedReplyProtocol, rawFactID, group.TaskID)
	}
}

func splitCoveredFactID(value string) (string, string) {
	value = strings.TrimSpace(value)
	if len(value) < 2 {
		return "", ""
	}
	digitStart := len(value)
	for digitStart > 0 && value[digitStart-1] >= '0' && value[digitStart-1] <= '9' {
		digitStart--
	}
	if digitStart == len(value) || digitStart == 0 || value[digitStart-1] != 'F' {
		return "", ""
	}
	for _, character := range value[:digitStart-1] {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' && character != '_' {
			return "", ""
		}
	}
	return value[:digitStart-1], value[digitStart-1:]
}

func equivalentCoveredFactTaskScope(left string, right string) bool {
	leftIndex, leftOK := coveredFactTaskIndex(left)
	rightIndex, rightOK := coveredFactTaskIndex(right)
	return leftOK && rightOK && leftIndex == rightIndex
}

func coveredFactTaskIndex(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"task-", "task_", "task", "t"} {
		if strings.HasPrefix(value, prefix) {
			value = strings.TrimPrefix(value, prefix)
			break
		}
	}
	if value == "" {
		return "", false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return "", false
		}
	}
	return strings.TrimLeft(value, "0"), true
}

func containsCriticalValue(content string, criticalValue string) bool {
	return strings.Contains(normalizeCriticalValueText(content), normalizeCriticalValueText(criticalValue))
}

func criticalValuePolarityMatches(content string, statement string, criticalValue string) bool {
	content = normalizeCriticalValueText(content)
	statement = normalizeCriticalValueText(statement)
	criticalValue = normalizeCriticalValueText(criticalValue)
	if content == "" || criticalValue == "" {
		return false
	}
	expectedNegative := criticalValueOccurrenceIsNegated(statement, strings.Index(statement, criticalValue))
	for offset := 0; offset < len(content); {
		index := strings.Index(content[offset:], criticalValue)
		if index < 0 {
			break
		}
		index += offset
		if criticalValueOccurrenceIsNegated(content, index) == expectedNegative {
			return true
		}
		offset = index + len(criticalValue)
	}
	return false
}

func criticalValueOccurrenceIsNegated(text string, byteIndex int) bool {
	if byteIndex < 0 {
		return false
	}
	prefixRunes := []rune(text[:byteIndex])
	if len(prefixRunes) > 5 {
		prefixRunes = prefixRunes[len(prefixRunes)-5:]
	}
	prefix := string(prefixRunes)
	for _, marker := range []string{"并不是", "并非", "没有", "不是", "不能", "不会", "无法", "不可", "不含", "不提供", "不", "没", "无", "未", "非"} {
		if strings.HasSuffix(prefix, marker) {
			return true
		}
	}
	return false
}

func normalizeCriticalValueText(text string) string {
	fullWidthNormalized := strings.Map(func(r rune) rune {
		if r >= '\uff01' && r <= '\uff5e' {
			return r - 0xfee0
		}
		switch r {
		case ' ', '\t', '\r', '\n', '\u3000':
			return -1
		default:
			return r
		}
	}, strings.TrimSpace(text))
	runes := []rune(fullWidthNormalized)
	for index := 1; index+1 < len(runes); index++ {
		if !isCriticalValueRangeSeparator(runes[index]) {
			continue
		}
		if isCriticalValueRangeEndpoint(runes[index-1]) && isCriticalValueRangeEndpoint(runes[index+1]) {
			runes[index] = '-'
		}
	}
	return strings.NewReplacer(
		"扫人脸", "人脸",
		"刷人脸", "人脸",
		"扫脸", "人脸",
		"刷脸", "人脸",
		"酒店名称", "酒店名",
		"门店名称", "酒店名",
		"门店名", "酒店名",
		"对应楼层", "楼层",
		"所在楼层", "楼层",
		"房间号", "房号",
		"门牌号", "房号",
	).Replace(string(runes))
}

func isCriticalValueRangeSeparator(r rune) bool {
	switch r {
	case '-', '~', '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2212', '\u301c', '\uff5e', '至', '到':
		return true
	default:
		return false
	}
}

func isCriticalValueRangeEndpoint(r rune) bool {
	if unicode.IsDigit(r) {
		return true
	}
	return strings.ContainsRune("零〇一二三四五六七八九十百千万两", r)
}

func containsReplyMessageMarker(text string) bool {
	for _, marker := range []string{"<<NEXT_MESSAGE>>", "<NEXT_MESSAGE>", "[[NEXT_MESSAGE]]"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func composeGeneratedReplyContents(parts []string, limit int) string {
	return strings.Join(balanceGeneratedReplyContents(parts, limit), "\n<<NEXT_MESSAGE>>\n")
}

func balanceGeneratedReplyContents(parts []string, limit int) []string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if limit <= 0 || len(cleaned) <= limit {
		return cleaned
	}
	groupCount := limit
	baseSize := len(cleaned) / groupCount
	extra := len(cleaned) % groupCount
	balanced := make([]string, 0, groupCount)
	start := 0
	for index := 0; index < groupCount; index++ {
		size := baseSize
		if index < extra {
			size++
		}
		end := start + size
		balanced = append(balanced, strings.Join(cleaned[start:end], "\n\n"))
		start = end
	}
	return balanced
}

func parseGeneratedReplyParts(text string) (generatedReplyPartsEnvelope, bool) {
	return parseGeneratedReplyPartsPayload(strings.TrimSpace(text), 0)
}

func parseGeneratedReplyPartsPayload(raw string, depth int) (generatedReplyPartsEnvelope, bool) {
	if depth > 4 {
		return generatedReplyPartsEnvelope{}, false
	}
	raw = unwrapGeneratedReplyMarkdownFence(strings.TrimSpace(raw))
	if raw == "" {
		return generatedReplyPartsEnvelope{}, false
	}

	envelope := generatedReplyPartsEnvelope{}
	if err := json.Unmarshal([]byte(raw), &envelope); err == nil && len(envelope.ReplyParts) > 0 {
		return envelope, true
	}

	var quoted string
	if err := json.Unmarshal([]byte(raw), &quoted); err == nil && strings.TrimSpace(quoted) != raw {
		return parseGeneratedReplyPartsPayload(quoted, depth+1)
	}

	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &wrapper); err == nil {
		for _, key := range []string{"output", "result", "content", "text", "answer", "data"} {
			wrapped, ok := wrapper[key]
			if !ok || len(wrapped) == 0 {
				continue
			}
			if parsed, ok := parseGeneratedReplyPartsPayload(string(wrapped), depth+1); ok {
				return parsed, true
			}
		}
	}
	return generatedReplyPartsEnvelope{}, false
}

func unwrapGeneratedReplyMarkdownFence(raw string) string {
	raw = strings.TrimSpace(raw)
	start := strings.Index(raw, "```")
	if start < 0 {
		return raw
	}
	afterFence := raw[start+3:]
	lineEnd := strings.IndexByte(afterFence, '\n')
	if lineEnd < 0 {
		return raw
	}
	language := strings.TrimSpace(afterFence[:lineEnd])
	if language != "" && !strings.EqualFold(language, "json") {
		return raw
	}
	body := afterFence[lineEnd+1:]
	end := strings.LastIndex(body, "```")
	if end < 0 {
		return raw
	}
	return strings.TrimSpace(body[:end])
}

func looksLikeGeneratedReplyPartsProtocol(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if strings.Contains(normalized, "replyparts") || strings.Contains(normalized, "coveredfactids") {
		return true
	}
	return strings.Contains(normalized, "taskid") && strings.Contains(normalized, "content")
}

func buildTextReplyTaskGroups(plan callbacks.ReplyPlanTraceData) []textReplyTaskGroup {
	groups := make([]textReplyTaskGroup, 0, len(plan.TaskPlans))
	usedTaskIDs := make(map[string]struct{}, len(plan.TaskPlans))
	for _, task := range plan.TaskPlans {
		if !isReplyRequiredTextTask(task) {
			continue
		}
		fallbackTaskID := fmt.Sprintf("task-%d", len(groups)+1)
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			taskID = fallbackTaskID
		}
		if _, exists := usedTaskIDs[taskID]; exists {
			taskID = fallbackTaskID
		}
		usedTaskIDs[taskID] = struct{}{}
		text := firstNonEmptyReplyTaskText(task.ResolvedText, task.Text, task.SubIntent, task.Intent)
		groups = append(groups, textReplyTaskGroup{
			TaskID:             taskID,
			Texts:              []string{text},
			Facts:              replyFactRequirements(task.SupportedFacts),
			StructuredRequired: task.ReplyRequired || strings.TrimSpace(task.TaskID) != "" || len(task.SupportedFacts) > 0,
		})
	}
	return groups
}

func isReplyRequiredTextTask(task callbacks.ReplyTaskPlanTraceData) bool {
	switch strings.TrimSpace(task.OutputKind) {
	case "resource", "handoff", "context_only":
		return false
	case "text":
		return task.ReplyRequired
	}
	output := strings.TrimSpace(task.Output)
	intent := strings.TrimSpace(task.Intent)
	if output == "structured_resource_commit" || output == "human_route_confirmation_or_dispatch" || intent == "hotel_variable" {
		return false
	}
	return output != "" || intent != ""
}

func replyFactRequirements(facts []callbacks.KnowledgeEvidenceFactTraceData) []replyFactRequirement {
	ret := make([]replyFactRequirement, 0, len(facts))
	seen := make(map[string]struct{}, len(facts))
	for index, fact := range facts {
		factID := strings.TrimSpace(fact.FactID)
		if factID == "" {
			factID = fmt.Sprintf("F%d", index+1)
		}
		if _, exists := seen[factID]; exists {
			continue
		}
		seen[factID] = struct{}{}
		ret = append(ret, replyFactRequirement{
			FactID:         factID,
			Aspect:         strings.TrimSpace(fact.Aspect),
			Statement:      strings.TrimSpace(fact.Statement),
			CriticalValues: sanitizeKnowledgeEvidenceCriticalValuesForStatement(fact.CriticalValues, fact.Statement),
		})
	}
	return ret
}

func compactGeneratedReplyContent(content string) string {
	sentences := splitGeneratedReplyDisplaySentences(content)
	if len(sentences) < 2 {
		return strings.TrimSpace(content)
	}
	seen := make(map[string]struct{}, len(sentences))
	var b strings.Builder
	lastSentence := ""
	for _, sentence := range sentences {
		sentence = strings.TrimSpace(sentence)
		if _, exists := seen[sentence]; exists {
			continue
		}
		seen[sentence] = struct{}{}
		if b.Len() > 0 && !generatedReplySentenceHasTerminalPunctuation(lastSentence) {
			b.WriteString("\n")
		}
		b.WriteString(sentence)
		lastSentence = sentence
	}
	return strings.TrimSpace(b.String())
}

func generatedReplySentenceHasTerminalPunctuation(sentence string) bool {
	sentence = strings.TrimSpace(sentence)
	if sentence == "" {
		return false
	}
	runes := []rune(sentence)
	return strings.ContainsRune("。！!？?；;", runes[len(runes)-1])
}

func splitGeneratedReplyDisplaySentences(content string) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	ret := make([]string, 0, 4)
	start := 0
	for index, character := range content {
		if !strings.ContainsRune("。！!？?；;\n\r", character) {
			continue
		}
		end := index + len(string(character))
		if sentence := strings.TrimSpace(content[start:end]); sentence != "" {
			ret = append(ret, sentence)
		}
		start = end
	}
	if start < len(content) {
		if sentence := strings.TrimSpace(content[start:]); sentence != "" {
			ret = append(ret, sentence)
		}
	}
	return ret
}

func generatedReplyDisplaySentenceContains(container string, contained string) bool {
	containerText := normalizeGeneratedReplyPresentationClause(container)
	containedText := normalizeGeneratedReplyPresentationClause(contained)
	if containerText == "" || containedText == "" || len([]rune(containerText)) < len([]rune(containedText)) {
		return false
	}
	if containerText == containedText {
		return true
	}
	return generatedReplyFallbackFactContains(
		replyFactRequirement{Statement: container},
		replyFactRequirement{Statement: contained},
	)
}

func normalizeGeneratedReplyPresentationClause(text string) string {
	normalized := normalizeRuntimeKnowledgeQuery(text)
	for {
		trimmed := normalized
		for _, prefix := range []string{"另外", "此外", "同时", "然后", "以及", "并且", "还有", "再有", "我们的", "咱们的", "你们的", "您的", "你的", "我们", "咱们", "你们", "您", "你"} {
			if strings.HasPrefix(trimmed, prefix) {
				trimmed = strings.TrimPrefix(trimmed, prefix)
				break
			}
		}
		if trimmed == normalized {
			break
		}
		normalized = trimmed
	}
	normalized = strings.ReplaceAll(normalized, "完成登记后", "完成登记")
	for {
		trimmed := normalized
		for _, suffix := range []string{"啦", "呀", "啊", "哦"} {
			if strings.HasSuffix(trimmed, suffix) {
				trimmed = strings.TrimSuffix(trimmed, suffix)
				break
			}
		}
		if trimmed == normalized {
			break
		}
		normalized = trimmed
	}
	return normalized
}

func firstNonEmptyReplyTaskText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
