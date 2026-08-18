package knowledgepolicy

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/models"
)

type Task struct {
	TaskKey   string
	Intent    string
	SubIntent string
	Query     string
	// EvidenceQuery is the current task's own source-bound question. Query may
	// include a bounded parent-topic hint for retrieval, but evidence approval
	// must never be granted merely because that inherited hint matches a FAQ.
	EvidenceQuery string
	RequestMode   string
}

type EvidenceJudgeInput struct {
	TenantID  int64
	StoreID   int64
	Task      Task
	Candidate rag.RetrieveResult
	Metadata  *models.KnowledgeEvidenceMetadata
}

type EvidenceJudgeResult struct {
	SourceClass        string
	FactScope          string
	ClaimType          string
	TrustLevel         string
	Freshness          string
	ReviewStatus       string
	TopicLabels        []string
	ResourcePurpose    string
	AutoAttachResource bool
	TopicMatch         string
	Answerability      string
	AllowedUses        []string
	BlockedReasons     []string
}

func Judge(input EvidenceJudgeInput) EvidenceJudgeResult {
	result := metadataProjection(input)
	if LooksLikeMetaContent(input.Candidate.Title, input.Candidate.Content) {
		result.SourceClass = "derived_qa"
		result.ClaimType = "meta"
		result.TrustLevel = "weak"
	}
	result.TopicMatch = topicMatch(input.Task, input.Candidate, result.TopicLabels)
	block := func(reason string) { result.BlockedReasons = appendUnique(result.BlockedReasons, reason) }
	if isExceptionEvidence(input.Candidate.Title, input.Candidate.Content) && !taskRequestsException(input.Task) {
		// An exception FAQ can be semantically close to a normal procedure
		// question, but it is not permission to answer the normal case with an
		// escalation path (for example, "another room cannot check in").
		block("exception_evidence_for_normal_question")
	}

	if input.Metadata != nil && (input.Metadata.TenantID != input.TenantID || input.Metadata.StoreID != input.StoreID || input.Metadata.KnowledgeBaseID != input.Candidate.KnowledgeBaseID) {
		block("metadata_scope_mismatch")
	}
	if result.ClaimType == "meta" || result.SourceClass == "derived_qa" {
		block("meta_content")
	}
	if result.SourceClass == "customer_content" {
		block("customer_content_not_fact")
	}
	if result.ReviewStatus == "rejected" {
		block("review_rejected")
	}
	if result.TrustLevel == "blocked" {
		block("trust_blocked")
	}
	if result.Freshness == "stale" {
		block("evidence_stale")
	}
	if result.TopicMatch == "mismatch" {
		block("topic_mismatch")
	}

	if len(result.BlockedReasons) > 0 {
		result.Answerability = "blocked"
		return result
	}
	if result.TrustLevel == "weak" || result.SourceClass == "customer_content" {
		result.Answerability = "context_only"
		result.BlockedReasons = appendUnique(result.BlockedReasons, "evidence_not_strong_enough")
		result.AllowedUses = []string{"resolve_reference"}
		return result
	}
	// Structured FAQ answers are atomic question/answer records. A merely related
	// question may be useful for reference resolution, but must not become answer
	// evidence for a different business topic.
	if looksLikeDirectQA(input.Candidate.Title, input.Candidate.Content) && result.TopicMatch != "exact" {
		result.Answerability = "context_only"
		result.BlockedReasons = appendUnique(result.BlockedReasons, "direct_qa_topic_not_exact")
		result.AllowedUses = []string{"resolve_reference"}
		return result
	}

	switch result.ClaimType {
	case "recommendation":
		if result.TopicMatch != "exact" {
			result.Answerability = "context_only"
			result.BlockedReasons = appendUnique(result.BlockedReasons, "recommendation_topic_not_exact")
			result.AllowedUses = []string{"resolve_reference"}
			return result
		}
		if result.FactScope != "store" && result.FactScope != "nearby" {
			result.Answerability = "blocked"
			result.BlockedReasons = appendUnique(result.BlockedReasons, "recommendation_scope_invalid")
			return result
		}
		if result.SourceClass != "store_authored" && result.SourceClass != "imported_faq" && result.ReviewStatus != "approved" {
			result.Answerability = "context_only"
			result.BlockedReasons = appendUnique(result.BlockedReasons, "recommendation_source_unreviewed")
			result.AllowedUses = []string{"resolve_reference"}
			return result
		}
		result.Answerability = "supporting"
		result.AllowedUses = []string{"answer_text", "recommend"}
	case "procedure", "policy":
		if result.TopicMatch != "exact" {
			result.Answerability = "context_only"
			result.BlockedReasons = appendUnique(result.BlockedReasons, "procedure_or_policy_topic_not_exact")
			result.AllowedUses = []string{"resolve_reference"}
			return result
		}
		if result.SourceClass == "unknown" && result.ReviewStatus != "approved" {
			result.Answerability = "context_only"
			result.BlockedReasons = appendUnique(result.BlockedReasons, "procedure_or_policy_source_unreviewed")
			result.AllowedUses = []string{"resolve_reference"}
			return result
		}
		result.Answerability = "supporting"
		result.AllowedUses = []string{"answer_text"}
	default:
		result.Answerability = "supporting"
		result.AllowedUses = []string{"answer_text"}
	}
	if result.AutoAttachResource && result.ResourcePurpose != "unknown" {
		result.AllowedUses = appendUnique(result.AllowedUses, "prepare_resource")
	}
	return result
}

// IsNonBypassableBoundary identifies the small set of evidence boundaries that
// must remain effective even when the evidence-quality gate is excluded for a
// tenant/store during a rollout. Gate exclusions may relax metadata/trust
// checks, but they must never turn an exception FAQ or a cross-topic hit into
// answer evidence.
func IsNonBypassableBoundary(result EvidenceJudgeResult) bool {
	for _, reason := range result.BlockedReasons {
		switch strings.TrimSpace(reason) {
		case "exception_evidence_for_normal_question", "topic_mismatch",
			"direct_qa_topic_not_exact", "recommendation_topic_not_exact",
			"procedure_or_policy_topic_not_exact":
			return true
		}
	}
	return false
}

func metadataProjection(input EvidenceJudgeInput) EvidenceJudgeResult {
	result := EvidenceJudgeResult{
		SourceClass: "unknown", FactScope: "store", ClaimType: "fact", TrustLevel: "supported",
		Freshness: "unknown", ReviewStatus: "pending", ResourcePurpose: "unknown",
		TopicLabels: []string{}, AllowedUses: []string{}, BlockedReasons: []string{},
	}
	if input.Metadata != nil {
		meta := input.Metadata
		result.SourceClass = fallback(meta.SourceClass, result.SourceClass)
		result.FactScope = fallback(meta.FactScope, result.FactScope)
		result.ClaimType = fallback(meta.ClaimType, result.ClaimType)
		result.TrustLevel = fallback(meta.TrustLevel, result.TrustLevel)
		result.Freshness = fallback(meta.Freshness, result.Freshness)
		result.ReviewStatus = fallback(meta.ReviewStatus, result.ReviewStatus)
		result.ResourcePurpose = fallback(meta.ResourcePurpose, result.ResourcePurpose)
		result.AutoAttachResource = meta.AutoAttachResource
		_ = json.Unmarshal([]byte(meta.TopicLabels), &result.TopicLabels)
	}
	if result.SourceClass == "unknown" && looksLikeDirectQA(input.Candidate.Title, input.Candidate.Content) {
		result.SourceClass = "imported_faq"
	}
	if input.Metadata == nil || (result.SourceClass == "unknown" && result.ReviewStatus == "pending") {
		// Query may contain a bounded parent-topic retrieval hint for an
		// elliptical follow-up. Classification must remain bound to the current
		// source question, otherwise the parent can turn a new question into the
		// wrong claim type or fact scope.
		query := strings.TrimSpace(input.Task.EvidenceQuery)
		if query == "" {
			query = input.Task.Query
		}
		result.ClaimType = InferClaimType(query, input.Candidate.Title, input.Candidate.Content)
		result.FactScope = InferFactScope(query, result.ClaimType)
	}
	return result
}

func InferClaimType(query, title, content string) string {
	if LooksLikeMetaContent(title, content) {
		return "meta"
	}
	text := normalizeText(strings.Join([]string{query, title}, " "))
	if containsAny(text, "推荐", "攻略", "好玩", "去哪", "哪里玩", "周边", "附近有什么", "吃什么", "有哪些景点") {
		return "recommendation"
	}
	if containsAny(text, "怎么", "如何", "咋", "怎样", "怎么办", "咋办", "咋弄", "流程", "步骤", "办理", "操作", "使用方法") {
		return "procedure"
	}
	if containsAny(text, "政策", "规定", "收费", "退款", "取消", "押金", "最晚", "最早", "能不能", "可以吗") {
		return "policy"
	}
	return "fact"
}

func InferFactScope(query, claimType string) string {
	text := normalizeText(query)
	if claimType == "recommendation" && containsAny(text, "附近", "周边", "景点", "商圈", "去哪") {
		return "nearby"
	}
	return "store"
}

func LooksLikeMetaContent(title, content string) bool {
	text := normalizeText(strings.Join([]string{title, content}, " "))
	contentText := normalizeText(content)
	if containsAny(text,
		"contextblock", "theinputcontext", "replicatethecontext", "copythecontext",
		"okayiwill", "tokenlimits", "outputshouldstart", "instructionexplicitly",
		"standardbehavior", "systemprompt", "userprompt", "assistantmessage",
		"上下文块", "复制上下文", "输出应该", "指令要求", "令牌限制") {
		return true
	}
	if normalizeText(title) == "问题" && strings.HasPrefix(contentText, "问题问题答案答案") {
		return true
	}
	if containsAny(text, "用户可能通过哪些", "用户可能怎么问", "用户会如何询问", "不同的方式向助手询问") {
		return true
	}
	if containsAny(text, "分为哪两个类别", "分为哪几类", "缺少哪些关键信息", "如果填充真实数据") {
		return true
	}
	sourceTerms := containsAny(text, "这段文本", "本文", "本段", "原文", "上文", "材料中", "表格", "prompt", "文中", "这个问题列表")
	examTerms := containsAny(text, "主要提供了哪", "提到了哪", "介绍了哪", "缺少了什么", "是否包含", "分为哪", "第一个", "第二个", "首先介绍", "答案是什么")
	if sourceTerms && examTerms {
		return true
	}
	if containsAny(text, "推荐部分首先", "第二个推荐", "第一个推荐", "问题可以怎么问") {
		return true
	}
	// 数据集垃圾类：LLM 对 Markdown 表格/Prompt 模板生成的技术元问答
	// （LaTeX/Completion/微调/损坏的文件/渲染异常等）。这些是技术域标记，
	// 不会命中酒店业务 FAQ。
	if containsAny(text, "latex", "completion", "fine-tuning", "微调",
		"损坏的文件", "渲染异常", "提取出具体", "该文本", "该表格", "模型生成") {
		return true
	}
	return false
}

func topicMatch(task Task, candidate rag.RetrieveResult, labels []string) string {
	query := normalizeText(task.EvidenceQuery)
	if query == "" {
		query = normalizeText(task.Query)
	}
	source := normalizeText(strings.Join([]string{candidate.Title, candidate.DocumentTitle, candidate.Content}, " "))
	if query == "" || source == "" {
		return "related"
	}
	title := normalizeText(candidate.Title)
	if strings.Contains(source, query) || (title != "" && strings.Contains(query, title)) {
		return "exact"
	}
	for _, label := range labels {
		label = normalizeText(label)
		if label != "" && strings.Contains(query, label) {
			return "exact"
		}
	}
	// 向量分数只证明语义相关，不能单独升级为 exact。只有原文、标签或
	// 可验证的结构化问答主题能够证明题目精确匹配。
	if looksLikeDirectQA(candidate.Title, candidate.Content) && structuredQATopicMatch(task, query, candidate) {
		return "exact"
	}
	if bigramJaccard(query, source) >= 0.22 || candidate.Score >= 0.64 {
		return "related"
	}
	return "mismatch"
}

func taskRequestsException(task Task) bool {
	// EvidenceQuery is the source-bound current question. Query may carry a
	// parent-topic retrieval hint for an elliptical follow-up; including it here
	// lets an old failure topic authorize an exception answer for a new topic.
	text := normalizeText(firstNonEmpty(task.EvidenceQuery, task.Query))
	text = normalizeText(strings.Join([]string{text, task.SubIntent}, " "))
	if containsAny(text,
		"办不了", "无法办理", "不能办理", "入住失败", "退房失败", "办理失败", "打不开",
		"不制冷", "坏了", "故障", "出问题", "异常", "报错", "报错了", "没法", "无法",
		"失败", "另一间房", "手机不能用", "收不到", "进不去", "卡住", "故障",
	) {
		return true
	}
	value := strings.ToLower(strings.TrimSpace(task.SubIntent))
	for _, marker := range []string{"failure", "failed", "exception", "error", "issue", "trouble", "repair", "maintenance"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func isExceptionEvidence(title, content string) bool {
	titleText := normalizeText(title)
	contentText := normalizeText(content)
	strongMarkers := []string{
		"办不了", "无法办理", "不能办理", "入住失败", "退房失败", "办理失败", "打不开",
		"不制冷", "坏了", "故障", "报错", "失败", "另一间房", "手机不能用",
		"收不到", "进不去", "卡住",
	}
	if containsAny(titleText, strongMarkers...) {
		return true
	}
	if !containsAny(contentText, strongMarkers...) {
		return false
	}
	// Normal procedure FAQs often include a conditional fallback such as
	// “如无法办理请联系客服”. That clause must not downgrade the whole normal
	// FAQ into an exception record; require an actual failure statement instead.
	if containsAny(contentText, "如无法", "如果无法", "若无法", "如遇故障", "如有问题请", "遇到问题请") &&
		!containsAny(contentText, "当前无法", "一直无法", "已经失败", "提示失败", "实际故障") {
		return false
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func structuredQATopicMatch(task Task, normalizedQuery string, candidate rag.RetrieveResult) bool {
	title := normalizeText(candidate.Title)
	if normalizedQuery == "" || title == "" {
		return false
	}
	queryType := InferClaimType(normalizedQuery, "", "")
	titleType := InferClaimType(candidate.Title, candidate.Title, candidate.Content)
	if queryType != titleType && titleType == "recommendation" && structuredTaskRequestsRecommendation(task, normalizedQuery) {
		queryType = "recommendation"
	}
	if queryType == "recommendation" || titleType == "recommendation" {
		if queryType != titleType {
			return false
		}
		queryCategory := recommendationCategory(normalizedQuery)
		titleCategory := recommendationCategory(title)
		if queryCategory != "" && titleCategory != "" && queryCategory != titleCategory {
			return false
		}
		return InferFactScope(normalizedQuery, queryType) == InferFactScope(candidate.Title, titleType) &&
			bigramJaccard(normalizedQuery, title) >= 0.08
	}
	// FAQ 标题的语法形式不是答案能力。相同业务主题可以用 policy/fact
	// 回答 procedure 式口语问法，例如“外卖怎么点”可以使用“酒店可以点
	// 外卖吗”的规则答案。向量分数仍不能单独授权，必须同时存在可验证的
	// 业务主题片段，避免高分但跨主题的结果进入 Generate。
	return candidate.Score >= 0.64 && businessTopicMatch(normalizedQuery, strings.Join([]string{candidate.Title, candidate.Content}, " "))
}

func businessTopicMatch(query, source string) bool {
	queryCore := businessTopicCore(query)
	sourceCore := businessTopicCore(source)
	if queryCore == "" || sourceCore == "" {
		return false
	}
	if strings.Contains(queryCore, sourceCore) || strings.Contains(sourceCore, queryCore) {
		return true
	}
	if bigramJaccard(queryCore, sourceCore) >= 0.22 {
		return true
	}
	common := longestCommonTopicRunes(queryCore, sourceCore)
	return common >= 2 && float64(common)/float64(len([]rune(queryCore))) >= 0.5
}

func businessTopicCore(value string) string {
	value = normalizeText(value)
	replacer := strings.NewReplacer(
		"酒店", "", "房间", "", "客人", "", "住客", "", "请问", "", "麻烦", "",
		"帮我", "", "给我", "", "我要", "", "想要", "", "具体", "", "一下", "",
		// Remove interrogative scaffolding as phrases, not individual domain
		// characters. This keeps short colloquial questions such as
		// “入住都不会？” and “干嘛吃的？” bound to the business anchor.
		"都不会", "", "会不会", "", "不会", "", "干什么", "", "干嘛", "",
		"什么时候", "", "什么", "", "啥", "",
		"可不可以", "", "可以不可以", "", "是否提供", "", "有没有", "", "是否有", "",
		"使用方法", "", "怎么办", "", "咋开", "", "咋弄", "", "咋办", "", "怎么样", "", "怎样", "",
		"在哪里", "", "在哪儿", "", "什么时间", "", "如何", "", "怎么", "", "能不能", "",
		"开具", "", "申请", "", "办理", "", "操作", "", "流程", "", "步骤", "",
		"是否", "", "提供", "", "位置", "", "时间", "", "几点", "", "在哪里", "", "哪里", "", "在哪", "",
		"有吗", "", "可以吗", "", "吗", "", "呢", "", "呀", "", "啊", "",
	)
	return strings.TrimSpace(replacer.Replace(value))
}

func longestCommonTopicRunes(a, b string) int {
	left := []rune(a)
	right := []rune(b)
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	previous := make([]int, len(right)+1)
	best := 0
	for _, leftRune := range left {
		current := make([]int, len(right)+1)
		for index, rightRune := range right {
			if leftRune != rightRune {
				continue
			}
			current[index+1] = previous[index] + 1
			if current[index+1] > best {
				best = current[index+1]
			}
		}
		previous = current
	}
	return best
}

func structuredTaskRequestsRecommendation(task Task, normalizedQuery string) bool {
	subIntent := normalizeText(task.SubIntent)
	structuredNearbyScope := strings.Contains(subIntent, "surrounding") || strings.Contains(subIntent, "nearby")
	if !structuredNearbyScope {
		return false
	}
	return recommendationCategory(normalizedQuery) != "" || containsAny(normalizedQuery, "附近", "周边", "去哪")
}

func recommendationCategory(text string) string {
	text = normalizeText(text)
	switch {
	case containsAny(text, "吃", "餐饮", "美食", "小吃", "饭店", "餐厅", "外卖"):
		return "food"
	case containsAny(text, "玩", "游玩", "景点", "公园", "逛", "娱乐"):
		return "play"
	case containsAny(text, "购物", "商场", "商圈", "买东西"):
		return "shopping"
	default:
		return ""
	}
}

func looksLikeDirectQA(title, content string) bool {
	return strings.TrimSpace(title) != "" && (strings.Contains(content, "答案：") || strings.Contains(content, "答案:"))
}

func normalizeText(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Han, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func bigramJaccard(a, b string) float64 {
	aSet, bSet := bigrams(a), bigrams(b)
	if len(aSet) == 0 || len(bSet) == 0 {
		return 0
	}
	intersection := 0
	for value := range aSet {
		if _, ok := bSet[value]; ok {
			intersection++
		}
	}
	return float64(intersection) / float64(len(aSet)+len(bSet)-intersection)
}

func bigrams(value string) map[string]struct{} {
	runes := []rune(value)
	ret := make(map[string]struct{}, len(runes))
	for i := 0; i+1 < len(runes); i++ {
		ret[string(runes[i:i+2])] = struct{}{}
	}
	return ret
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, normalizeText(value)) {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func fallback(value, defaultValue string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return defaultValue
}

func SortedUnique(values []string) []string {
	ret := make([]string, 0, len(values))
	for _, value := range values {
		ret = appendUnique(ret, value)
	}
	sort.Strings(ret)
	return ret
}
