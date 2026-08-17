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
	TaskKey     string
	Intent      string
	SubIntent   string
	Query       string
	RequestMode string
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
		result.ClaimType = InferClaimType(input.Task.Query, input.Candidate.Title, input.Candidate.Content)
		result.FactScope = InferFactScope(input.Task.Query, result.ClaimType)
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
	query := normalizeText(task.Query)
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
	if queryType != titleType {
		return false
	}
	if queryType == "recommendation" {
		queryCategory := recommendationCategory(normalizedQuery)
		titleCategory := recommendationCategory(title)
		if queryCategory != "" && titleCategory != "" && queryCategory != titleCategory {
			return false
		}
		return InferFactScope(normalizedQuery, queryType) == InferFactScope(candidate.Title, titleType) &&
			bigramJaccard(normalizedQuery, title) >= 0.08
	}
	return candidate.Score >= 0.64 && businessTopicMatch(normalizedQuery, title)
}

func businessTopicMatch(query, title string) bool {
	queryCore := businessTopicCore(query)
	titleCore := businessTopicCore(title)
	if queryCore == "" || titleCore == "" {
		return false
	}
	if strings.Contains(queryCore, titleCore) || strings.Contains(titleCore, queryCore) {
		return true
	}
	return bigramJaccard(queryCore, titleCore) >= 0.22
}

func businessTopicCore(value string) string {
	value = normalizeText(value)
	replacer := strings.NewReplacer(
		"可不可以", "", "可以不可以", "", "是否提供", "", "有没有", "", "是否有", "",
		"使用方法", "", "怎么办", "", "咋开", "", "咋弄", "", "咋办", "", "怎么样", "", "怎样", "",
		"在哪里", "", "在哪儿", "", "什么时间", "", "如何", "", "怎么", "", "能不能", "",
		"开具", "", "申请", "", "办理", "", "操作", "", "流程", "", "步骤", "",
		"是否", "", "提供", "", "位置", "", "时间", "", "几点", "", "在哪", "",
		"有吗", "", "可以吗", "", "吗", "", "呢", "", "呀", "", "啊", "",
	)
	return strings.TrimSpace(replacer.Replace(value))
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
