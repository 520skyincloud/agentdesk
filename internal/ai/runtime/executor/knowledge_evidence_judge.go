package executor

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

type runtimeKnowledgeEvidenceFilterStats struct {
	droppedMeta     int
	droppedScope    int
	droppedPolicy   int
	droppedAction   int
	droppedMismatch int
	droppedWeak     int
}

type runtimeKnowledgeEvidenceDecision struct {
	Keep   bool
	Reason string
}

// judgeRuntimeTaskKnowledgeEvidence 在知识结果进入 EvidenceBundle、资源绑定和
// AnswerGroup 之前完成一次统一裁决。这样被判定为错题的正文、图片和分组键都会
// 一起退出链路，不会出现“文字过滤了但洗衣房图片仍跟着地址任务发出”的半过滤。
func judgeRuntimeTaskKnowledgeEvidence(req RunInput, item *runtimeTaskKnowledgeItem) {
	if item == nil || item.Result == nil || item.Status == enums.AIReplyTurnTaskKnowledgeStatusFailed || !gateEnabled(gateEvidenceQuality, req) {
		return
	}
	combined := append([]rag.RetrieveResult(nil), item.Result.ContextResults...)
	combined = appendUniqueRuntimeRetrieveResults(combined, item.Result.Hits, map[string]bool{})
	if len(combined) == 0 {
		return
	}
	kept, stats, decisions := filterKnowledgeEvidenceForTaskWithDecisions(req, *item, combined)
	applyRuntimeKnowledgeEvidenceDecisions(item.Result, decisions)
	if len(kept) == len(combined) {
		return
	}
	allowed := make(map[string]struct{}, len(kept))
	for _, result := range kept {
		allowed[runtimeEvidenceResultKey(result)] = struct{}{}
	}
	item.Result.Hits = retainJudgedKnowledgeResults(item.Result.Hits, allowed)
	item.Result.ContextResults = retainJudgedKnowledgeResults(item.Result.ContextResults, allowed)
	if len(item.Result.ContextResults) == 0 && len(item.Result.Hits) > 0 {
		limit := item.Result.Options.MaxContextItems
		if limit <= 0 || limit > len(item.Result.Hits) {
			limit = len(item.Result.Hits)
		}
		item.Result.ContextResults = append([]rag.RetrieveResult(nil), item.Result.Hits[:limit]...)
	}
	item.Result.ContextText = judgedKnowledgeContextText(item.Result.ContextResults)
	item.Result.TopScore = 0
	if len(item.Result.Hits) > 0 {
		item.Result.TopScore = float64(item.Result.Hits[0].Score)
	}
	item.Result.TraceSummary.HitCount = len(item.Result.Hits)
	item.Result.TraceSummary.ContextCount = len(item.Result.ContextResults)
	if len(item.Result.Hits) == 0 && len(item.Result.ContextResults) == 0 {
		item.Status = enums.AIReplyTurnTaskKnowledgeStatusNoHit
		if stats.droppedScope > 0 {
			item.FilterReason = "knowledge_scope_mismatch"
		} else if stats.droppedPolicy > 0 {
			item.FilterReason = "knowledge_use_not_allowed"
		} else if stats.droppedAction > 0 {
			item.FilterReason = "knowledge_unbound_action_marker"
		} else if stats.droppedMismatch > 0 || stats.droppedWeak > 0 {
			item.FilterReason = "knowledge_context_not_relevant"
		} else if stats.droppedMeta > 0 {
			item.FilterReason = "knowledge_meta_content"
		}
	}
}

func filterKnowledgeEvidenceForTask(req RunInput, item runtimeTaskKnowledgeItem, results []rag.RetrieveResult) ([]rag.RetrieveResult, runtimeKnowledgeEvidenceFilterStats) {
	kept, stats, _ := filterKnowledgeEvidenceForTaskWithDecisions(req, item, results)
	return kept, stats
}

func filterKnowledgeEvidenceForTaskWithDecisions(req RunInput, item runtimeTaskKnowledgeItem, results []rag.RetrieveResult) ([]rag.RetrieveResult, runtimeKnowledgeEvidenceFilterStats, map[string]runtimeKnowledgeEvidenceDecision) {
	stats := runtimeKnowledgeEvidenceFilterStats{}
	metadata := runtimeKnowledgeEvidenceMetadata(req, results)
	allowedKnowledgeBases := runtimeKnowledgeEvidenceScope(req, results)
	actionBindings := runtimeKnowledgeActionBindings(req, results)
	filtered := make([]rag.RetrieveResult, 0, len(results))
	decisions := make(map[string]runtimeKnowledgeEvidenceDecision, len(results))
	record := func(result rag.RetrieveResult, keep bool, reason string) {
		decisions[runtimeKnowledgeEvidenceTraceKey(result.KnowledgeBaseID, result.SourceRecordID)] = runtimeKnowledgeEvidenceDecision{Keep: keep, Reason: reason}
	}
	for _, result := range results {
		meta, hasMetadata := metadata[runtimeEvidenceSourceKey(result.KnowledgeBaseID, result.SourceRecordID)]
		if knowledgeEvidenceIsMetaOrBlocked(result, meta, hasMetadata) {
			stats.droppedMeta++
			record(result, false, "meta_or_blocked")
			continue
		}
		if !knowledgeEvidenceScopeAllowed(req, result, allowedKnowledgeBases) {
			stats.droppedScope++
			record(result, false, "scope_mismatch")
			continue
		}
		if knowledgeEvidenceUseBlocked(meta, hasMetadata) {
			stats.droppedPolicy++
			record(result, false, "use_policy_blocked")
			continue
		}
		if knowledgeEvidenceIsUnboundActionMarker(result, actionBindings) {
			stats.droppedAction++
			record(result, false, "unbound_action_marker")
			continue
		}
		if knowledgeEvidenceMismatchesTask(item, result) {
			stats.droppedMismatch++
			record(result, false, "task_mismatch")
			continue
		}
		if !knowledgeEvidenceHasPositiveRelevance(item, result, meta, hasMetadata) {
			stats.droppedWeak++
			record(result, false, "positive_relevance_missing")
			continue
		}
		filtered = append(filtered, result)
		record(result, true, "relevant_evidence")
	}
	return filtered, stats, decisions
}

func runtimeKnowledgeEvidenceTraceKey(knowledgeBaseID int64, sourceRecordID string) string {
	return fmt.Sprintf("%d:%s", knowledgeBaseID, strings.TrimSpace(sourceRecordID))
}

func applyRuntimeKnowledgeEvidenceDecisions(result *retrievers.KnowledgeRetrieveResult, decisions map[string]runtimeKnowledgeEvidenceDecision) {
	if result == nil || len(decisions) == 0 {
		return
	}
	for index := range result.TraceItems {
		item := &result.TraceItems[index]
		decision, ok := decisions[runtimeKnowledgeEvidenceTraceKey(item.KnowledgeBaseID, item.SourceRecordID)]
		if !ok {
			continue
		}
		if decision.Keep {
			item.JudgeDecision = "accepted"
		} else {
			item.JudgeDecision = "rejected"
			item.UsedInContext = false
			item.ContextRankNo = 0
			item.DiscardReason = "evidence_judge_" + decision.Reason
		}
		item.JudgeReason = decision.Reason
	}
}

func runtimeKnowledgeEvidenceMetadata(req RunInput, results []rag.RetrieveResult) map[string]models.KnowledgeEvidenceMetadata {
	ret := make(map[string]models.KnowledgeEvidenceMetadata)
	if req.Conversation.TenantID <= 0 || req.Conversation.StoreID <= 0 || sqls.DB() == nil {
		return ret
	}
	recordsByKnowledgeBase := make(map[int64][]string)
	for _, result := range results {
		recordID := strings.TrimSpace(result.SourceRecordID)
		if result.KnowledgeBaseID <= 0 || recordID == "" {
			continue
		}
		recordsByKnowledgeBase[result.KnowledgeBaseID] = appendUniqueStrings(recordsByKnowledgeBase[result.KnowledgeBaseID], recordID)
	}
	for knowledgeBaseID, recordIDs := range recordsByKnowledgeBase {
		items := services.KnowledgeEvidenceMetadataService.JudgeBySourceRecords(
			req.Conversation.TenantID,
			req.Conversation.StoreID,
			knowledgeBaseID,
			recordIDs,
		)
		for recordID, item := range items {
			ret[runtimeEvidenceSourceKey(knowledgeBaseID, recordID)] = item
		}
	}
	return ret
}

func runtimeKnowledgeEvidenceScope(req RunInput, results []rag.RetrieveResult) map[int64]bool {
	if req.Conversation.TenantID <= 0 || req.Conversation.StoreID <= 0 || sqls.DB() == nil {
		return nil
	}
	ids := make([]int64, 0, len(results))
	seen := make(map[int64]struct{}, len(results))
	for _, result := range results {
		if result.KnowledgeBaseID <= 0 {
			continue
		}
		if _, exists := seen[result.KnowledgeBaseID]; exists {
			continue
		}
		seen[result.KnowledgeBaseID] = struct{}{}
		ids = append(ids, result.KnowledgeBaseID)
	}
	allowed := make(map[int64]bool, len(ids))
	if len(ids) == 0 {
		return allowed
	}
	items := repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", req.Conversation.TenantID).
		Eq("store_id", req.Conversation.StoreID).
		Eq("status", enums.StatusOk).
		In("id", ids))
	for _, item := range items {
		allowed[item.ID] = true
	}
	return allowed
}

func knowledgeEvidenceScopeAllowed(req RunInput, result rag.RetrieveResult, allowed map[int64]bool) bool {
	if req.Conversation.TenantID <= 0 || req.Conversation.StoreID <= 0 || allowed == nil {
		return true
	}
	return result.KnowledgeBaseID > 0 && allowed[result.KnowledgeBaseID]
}

func knowledgeEvidenceIsMetaOrBlocked(result rag.RetrieveResult, meta models.KnowledgeEvidenceMetadata, hasMetadata bool) bool {
	if hasMetadata && (meta.ClaimType == "meta" || meta.TrustLevel == "blocked") {
		return true
	}
	title := firstNonEmpty(result.Title, result.DocumentTitle)
	return services.DetectKnowledgeMetaContent(title, result.Content)
}

func knowledgeEvidenceUseBlocked(meta models.KnowledgeEvidenceMetadata, hasMetadata bool) bool {
	if !hasMetadata {
		return false
	}
	if meta.ReviewStatus == "rejected" || meta.Freshness == "stale" {
		return true
	}
	switch meta.SourceClass {
	case "customer_content", "internal_control", "action_instruction":
		return true
	default:
		return false
	}
}

func runtimeKnowledgeActionBindings(req RunInput, results []rag.RetrieveResult) map[string]struct{} {
	ret := make(map[string]struct{})
	if req.Conversation.TenantID <= 0 || req.Conversation.StoreID <= 0 || sqls.DB() == nil {
		return ret
	}
	byKnowledgeBase := make(map[int64][]string)
	for _, result := range results {
		recordID := strings.TrimSpace(result.SourceRecordID)
		if result.KnowledgeBaseID <= 0 || recordID == "" {
			continue
		}
		byKnowledgeBase[result.KnowledgeBaseID] = appendUniqueStrings(byKnowledgeBase[result.KnowledgeBaseID], recordID)
	}
	for knowledgeBaseID, recordIDs := range byKnowledgeBase {
		bindings := repositories.KnowledgeActionBindingRepository.FindEnabledBySourceRecords(
			sqls.DB(), req.Conversation.TenantID, req.Conversation.StoreID, knowledgeBaseID, recordIDs,
		)
		for recordID, actionCode := range bindings {
			if strings.TrimSpace(actionCode) != "" {
				ret[runtimeEvidenceSourceKey(knowledgeBaseID, recordID)] = struct{}{}
			}
		}
	}
	return ret
}

func knowledgeEvidenceIsUnboundActionMarker(result rag.RetrieveResult, bindings map[string]struct{}) bool {
	if _, bound := bindings[runtimeEvidenceSourceKey(result.KnowledgeBaseID, result.SourceRecordID)]; bound {
		return false
	}
	content := strings.ToLower(compactRuntimeProtocolText(cleanRuntimeEvidenceAnswer(result.Content)))
	if content == "" || len([]rune(content)) > 24 {
		return false
	}
	switch content {
	case "转接", "转人工", "人工", "人工客服", "转接人工", "转接客服", "联系客服", "联系人工", "人工处理", "转客服", "human_handoff", "handoff":
		return true
	default:
		return false
	}
}

// knowledgeEvidenceHasPositiveRelevance is fail-closed: retrieval score is
// never sufficient by itself. A candidate must prove relevance through a
// matching business topic, reviewed metadata topic, normal-flow invariant, or
// meaningful lexical overlap with the current task.
func knowledgeEvidenceHasPositiveRelevance(item runtimeTaskKnowledgeItem, result rag.RetrieveResult, meta models.KnowledgeEvidenceMetadata, hasMetadata bool) bool {
	if isNormalCheckinKnowledgeItem(item) && knowledgeEvidenceSupportsNormalCheckinStep(strings.Join([]string{result.Title, result.DocumentTitle, result.Content}, "\n")) {
		return true
	}
	taskTopics := detectKnowledgeTopicClasses(item.Query + " " + item.SubIntent + " " + runtimeKnowledgeTopicLabel(item.SubIntent))
	candidate := strings.TrimSpace(strings.Join([]string{result.Title, result.DocumentTitle, result.SectionPath, result.Content}, "\n"))
	if candidate == "" {
		return false
	}
	candidateTopics := detectKnowledgeTopicClasses(candidate)
	if knowledgeTopicSetsIntersect(taskTopics, candidateTopics) {
		return true
	}
	if hasMetadata && knowledgeMetadataTopicMatches(taskTopics, meta.TopicLabels) {
		return true
	}
	query := strings.TrimSpace(item.Query + " " + runtimeKnowledgeTopicLabel(item.SubIntent))
	return knowledgeTextHasMeaningfulOverlap(query, candidate)
}

func knowledgeMetadataTopicMatches(taskTopics map[string]struct{}, raw string) bool {
	if len(taskTopics) == 0 || strings.TrimSpace(raw) == "" {
		return false
	}
	labels := make([]string, 0)
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		labels = strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == '，' || r == ';' || r == '；' || r == '|'
		})
	}
	for _, label := range labels {
		if knowledgeTopicSetsIntersect(taskTopics, detectKnowledgeTopicClasses(label)) {
			return true
		}
		if _, exists := taskTopics[strings.TrimSpace(label)]; exists {
			return true
		}
	}
	return false
}

func knowledgeTextHasMeaningfulOverlap(query, candidate string) bool {
	query = compactRuntimeProtocolText(query)
	candidate = compactRuntimeProtocolText(candidate)
	for _, noise := range []string{
		"请问", "麻烦", "帮我", "一下", "有没有", "有吗", "怎么", "如何", "哪里", "什么", "可以", "酒店", "门店", "这个", "那个", "想问", "我想", "我要", "我是", "需要",
	} {
		query = strings.ReplaceAll(query, noise, "")
	}
	queryRunes := []rune(query)
	if len(queryRunes) < 2 || candidate == "" {
		return false
	}
	for bigram := range bigramSet(queryRunes) {
		if strings.Contains(candidate, bigram) {
			return true
		}
	}
	return false
}

// knowledgeEvidenceMismatchesTask 只拒绝可确定的错配：正常问题命中异常处理 FAQ，
// 或任务与候选分别落入两个明确且不相交的酒店业务主题。无法确定时保留候选，
// 避免用模糊语义去重吞掉合法知识。
func knowledgeEvidenceMismatchesTask(item runtimeTaskKnowledgeItem, result rag.RetrieveResult) bool {
	query := strings.TrimSpace(item.Query)
	title := strings.TrimSpace(strings.Join([]string{result.Title, result.DocumentTitle, result.SectionPath}, "\n"))
	candidate := strings.TrimSpace(strings.Join([]string{title, result.Content}, "\n"))
	if candidate == "" {
		return true
	}
	if knowledgeEvidenceIsExceptionSpecific(result) && !knowledgeTextHasExceptionContext(query) {
		return true
	}
	// 正常入住只保留登记和开门步骤。入口/电梯属于独立路线问题；客户
	// 没有询问路线时不能因为知识库相似度较高而混进入住流程。
	if isNormalCheckinKnowledgeItem(item) && knowledgeEvidenceIsEntranceOnly(candidate) {
		return true
	}
	if isNormalCheckinKnowledgeItem(item) && knowledgeEvidenceSupportsNormalCheckinStep(candidate) {
		return false
	}
	// 用品经常存放在洗衣房、百宝箱等位置。只要问题中的具体物品或其
	// 等价别名在候选中明确出现，位置词不能再把正确证据判成跨主题。
	if knowledgeEvidenceHasExplicitEntityMatch(query, candidate) {
		return false
	}
	if knowledgeEvidenceHasConflictingExplicitEntity(query, candidate) {
		return true
	}
	taskTopics := detectKnowledgeTopicClasses(query + " " + item.SubIntent + " " + runtimeKnowledgeTopicLabel(item.SubIntent))
	strongTopics := detectKnowledgeTopicClasses(title)
	if len(taskTopics) > 0 && len(strongTopics) > 0 {
		if !knowledgeTopicSetsIntersect(taskTopics, strongTopics) {
			return true
		}
		// 地址是通用属性，不能因为候选标题同时出现“地址”就把洗衣房、餐厅等
		// 其他对象误当成门店地址。反方向不成立：问洗衣房时，标题写“洗衣房地址”
		// 仍然是合法知识。
		if _, addressTask := taskTopics["address"]; addressTask && knowledgeHasForeignSpecificTopic(taskTopics, strongTopics) {
			return true
		}
		return false
	}
	candidateTopics := detectKnowledgeTopicClasses(result.Content)
	return len(taskTopics) > 0 && len(candidateTopics) > 0 && !knowledgeTopicSetsIntersect(taskTopics, candidateTopics)
}

func knowledgeEvidenceHasExplicitEntityMatch(query, candidate string) bool {
	query = strings.ToLower(compactRuntimeProtocolText(query))
	candidate = strings.ToLower(compactRuntimeProtocolText(candidate))
	if query == "" || candidate == "" {
		return false
	}
	for _, aliases := range knowledgeEntityAliasGroups() {
		if containsAny(query, aliases) && containsAny(candidate, aliases) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceHasConflictingExplicitEntity(query, candidate string) bool {
	query = strings.ToLower(compactRuntimeProtocolText(query))
	candidate = strings.ToLower(compactRuntimeProtocolText(candidate))
	queryGroups := make(map[int]struct{})
	candidateGroups := make(map[int]struct{})
	for index, aliases := range knowledgeEntityAliasGroups() {
		if containsAny(query, aliases) {
			queryGroups[index] = struct{}{}
		}
		if containsAny(candidate, aliases) {
			candidateGroups[index] = struct{}{}
		}
	}
	if len(queryGroups) == 0 || len(candidateGroups) == 0 {
		return false
	}
	for index := range queryGroups {
		if _, ok := candidateGroups[index]; ok {
			return false
		}
	}
	return true
}

func knowledgeEntityAliasGroups() [][]string {
	return [][]string{
		{"熨斗", "挂烫机", "熨衣机", "蒸汽熨斗"},
		{"针线包", "针线", "缝衣包"},
		{"毛巾", "压缩毛巾", "一次性毛巾", "浴巾", "面巾"},
		{"草稿纸", "便签纸", "便签", "纸张", "纸笔"},
		{"百宝箱", "客用品柜", "自助用品柜", "自取柜"},
		{"牙刷", "牙具", "洗漱用品"},
		{"拖鞋", "一次性拖鞋"},
		{"剃须刀", "刮胡刀"},
		{"浴帽", "洗澡帽"},
	}
}

func isNormalCheckinKnowledgeItem(item runtimeTaskKnowledgeItem) bool {
	return isCheckinProcessSubIntent(item.SubIntent) &&
		!knowledgeTextHasExceptionContext(item.Query) &&
		runtimeTextHasCheckinContext(item.Query)
}

func knowledgeEvidenceSupportsNormalCheckinStep(text string) bool {
	compact := compactRuntimeProtocolText(text)
	doorStep := containsAny(compact, []string{"入住登记", "登记入住", "完成登记", "办理入住"}) &&
		strings.Contains(compact, "刷脸") && strings.Contains(compact, "开门")
	registrationStep := strings.Contains(compact, "小程序") &&
		containsAny(compact, []string{"登记", "实名", "住客信息", "订单信息", "办理入住"})
	return doorStep || registrationStep
}

func knowledgeEvidenceIsEntranceOnly(text string) bool {
	compact := compactRuntimeProtocolText(text)
	entry := containsAny(compact, []string{"酒店入口", "门店入口", "大楼入口", "入口位置"}) &&
		containsAny(compact, []string{"电梯", "大楼", "大厅", "停车场"})
	return entry && !knowledgeEvidenceSupportsNormalCheckinStep(text)
}

func knowledgeEvidenceIsExceptionSpecific(result rag.RetrieveResult) bool {
	title := strings.TrimSpace(strings.Join([]string{result.Title, result.DocumentTitle, result.SectionPath}, "\n"))
	if knowledgeTextHasExceptionContext(title) {
		return true
	}
	content := strings.TrimSpace(result.Content)
	if content == "" {
		return false
	}
	first := content
	if index := strings.IndexAny(first, "。！？!?\n"); index >= 0 {
		first = first[:index+1]
	}
	// 正常流程正文偶尔会在末尾附带异常兜底，不能因此整篇删除。只有标题、
	// 首句或整段短 FAQ 明确以异常为主题时，才视为异常知识。
	return knowledgeTextHasExceptionContext(first) && (len([]rune(content)) <= 240 || startsWithExceptionCondition(first))
}

func startsWithExceptionCondition(text string) bool {
	compact := compactRuntimeProtocolText(text)
	return strings.HasPrefix(compact, "如果") || strings.HasPrefix(compact, "若") ||
		strings.HasPrefix(compact, "如遇") || strings.HasPrefix(compact, "出现") ||
		strings.HasPrefix(compact, "遇到")
}

func knowledgeHasForeignSpecificTopic(taskTopics, candidateTopics map[string]struct{}) bool {
	generic := map[string]struct{}{"address": {}, "takeaway": {}, "store_name": {}}
	for topic := range candidateTopics {
		if _, isGeneric := generic[topic]; isGeneric {
			continue
		}
		if _, belongsToTask := taskTopics[topic]; !belongsToTask {
			return true
		}
	}
	return false
}

func retainJudgedKnowledgeResults(results []rag.RetrieveResult, allowed map[string]struct{}) []rag.RetrieveResult {
	ret := make([]rag.RetrieveResult, 0, len(results))
	for _, result := range results {
		if _, ok := allowed[runtimeEvidenceResultKey(result)]; ok {
			ret = append(ret, result)
		}
	}
	return ret
}

func judgedKnowledgeContextText(results []rag.RetrieveResult) string {
	parts := make([]string, 0, len(results))
	for _, result := range results {
		if content := strings.TrimSpace(result.Content); content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n\n")
}

func runtimeTaskHasAuthoritativeStoreFact(req RunInput, plan callbacks.ReplyTaskPlanTraceData) bool {
	if isStoreIdentitySubIntent(plan.SubIntent) {
		return authoritativeStoreIdentity(req) != ""
	}
	if !runtimeTaskUsesOnlyAuthoritativeStoreAddress(plan.SubIntent) {
		return false
	}
	return authoritativeStoreAddress(req) != ""
}

func runtimeTaskUsesOnlyAuthoritativeStoreAddress(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "address", "address_for_delivery", "delivery_address", "store_address":
		return true
	default:
		// 外卖/收货复合问题仍需检索门店规则，只把最终地址事实绑定到 Store。
		return false
	}
}

func taskRequestsStoreAddress(subIntent, query string) bool {
	switch strings.TrimSpace(subIntent) {
	case "address", "address_for_delivery", "delivery_address", "store_address":
		return true
	case "order_food_delivery", "takeaway", "food_delivery":
		compact := compactRuntimeProtocolText(query)
		return containsAny(compact, []string{"地址", "填哪里", "写哪里", "送到哪", "收货", "门牌", "定位", "位置"})
	default:
		return false
	}
}

func isStoreIdentitySubIntent(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "store_identity", "hotel_identity", "store_name", "hotel_name":
		return true
	default:
		return false
	}
}

func authoritativeStoreIdentity(req RunInput) string {
	instance := findRuntimeWxWorkInstance(req)
	if instance == nil {
		return ""
	}
	if name := strings.TrimSpace(instance.StoreNavigationName); name != "" {
		return name
	}
	if instance.StoreID <= 0 || sqls.DB() == nil {
		return ""
	}
	store := repositories.StoreRepository.Get(sqls.DB(), instance.StoreID)
	if store == nil || store.TenantID != req.Conversation.TenantID {
		return ""
	}
	return firstNonEmpty(store.NavigationName, store.Name, store.BrandName)
}

func runtimeKnowledgeTopicLabel(subIntent string) string {
	switch strings.TrimSpace(subIntent) {
	case "checkin_process", "check_in", "checkin", "check_in_process", "checkin_steps", "check_in_steps", "checkin_guide":
		return "入住流程"
	case "checkout_process", "check_out", "checkout":
		return "退房流程"
	case "parking":
		return "停车"
	case "breakfast":
		return "早餐"
	case "invoice":
		return "发票"
	case "network_wifi", "wifi", "network":
		return "WiFi"
	case "coffee":
		return "咖啡"
	case "laundry":
		return "洗衣"
	case "luggage", "luggage_storage":
		return "行李寄存"
	case "address", "address_for_delivery", "delivery_address", "store_address":
		return "门店地址"
	case "order_food_delivery", "food_delivery", "takeaway":
		return "外卖"
	case "discount", "promotion":
		return "优惠"
	case "tv_cast", "tvcast", "tv_screen_mirror":
		return "电视投屏"
	case "air_conditioner":
		return "空调"
	case "supplies_self_help", "supplies":
		return "客用品"
	case "store_identity", "hotel_identity", "store_name", "hotel_name":
		return "门店名称"
	default:
		return ""
	}
}

func knowledgeTextHasExceptionContext(text string) bool {
	compact := compactRuntimeProtocolText(text)
	return containsAny(compact, []string{
		"不能", "无法", "失败", "故障", "异常", "坏了", "用不了", "进不去", "打不开",
		"删掉", "删除", "取消", "退订", "退款", "换房", "另一间", "两间房", "改错", "不对",
		"投诉", "差评", "提前退房", "延迟退房", "没有收到", "没收到",
	})
}

func detectKnowledgeTopicClasses(text string) map[string]struct{} {
	compact := strings.ToLower(compactRuntimeProtocolText(text))
	ret := make(map[string]struct{})
	topics := map[string][]string{
		"checkin":      {"入住", "checkin", "check_in"},
		"checkout":     {"退房", "离店", "checkout", "check_out"},
		"address":      {"地址", "定位", "门牌", "收货"},
		"parking":      {"停车", "车位", "停车场"},
		"breakfast":    {"早餐", "早饭"},
		"coffee":       {"咖啡", "拿铁", "美式"},
		"invoice":      {"发票", "开票"},
		"wifi":         {"wifi", "无线网", "网络", "密码"},
		"laundry":      {"洗衣", "烘干", "洗衣房"},
		"luggage":      {"行李", "寄存"},
		"housekeeping": {"保洁", "打扫", "阿姨", "清洁"},
		"room_change":  {"换房", "升级房", "升级房间", "另一间房"},
		"door_access":  {"开门", "门锁", "刷脸", "房门"},
		"tv":           {"投屏", "电视"},
		"aircon":       {"空调", "制冷", "制热"},
		"supplies":     {"牙刷", "牙具", "拖鞋", "剃须刀", "刮胡刀", "浴帽", "客用品", "洗漱用品", "熨斗", "挂烫机", "熨衣机", "针线包", "针线", "毛巾", "浴巾", "面巾", "压缩毛巾", "一次性毛巾", "草稿纸", "便签纸", "便签", "纸张", "纸笔", "百宝箱", "客用品柜", "自助用品柜", "自取柜"},
		"discount":     {"优惠", "折扣", "会员价", "便宜"},
		"nearby_food":  {"附近吃", "吃的", "饿了", "推荐吃", "美食", "餐厅", "饭店", "小吃"},
		"nearby_fun":   {"附近玩", "好玩", "景点", "游玩"},
		"takeaway":     {"外卖", "送餐"},
		"store_name":   {"酒店名", "门店名", "公寓", "宾馆", "民宿", "hotel_identity", "store_identity"},
	}
	for topic, markers := range topics {
		if containsAny(compact, markers) {
			ret[topic] = struct{}{}
		}
	}
	return ret
}

func knowledgeTopicSetsIntersect(left, right map[string]struct{}) bool {
	for topic := range left {
		if _, ok := right[topic]; ok {
			return true
		}
	}
	return false
}
