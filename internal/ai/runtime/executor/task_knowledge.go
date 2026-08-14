package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

const runtimeKnowledgeTaskConcurrency = 4

type runtimeTaskKnowledgeOutcome struct {
	Prefetched       *retrievers.KnowledgeRetrieveResult
	ActiveTaskPlans  []callbacks.ReplyTaskPlanTraceData
	FailedTaskKeys   []string
	NoHitTaskKeys    []string
	KnowledgeTaskIDs []string
	KnowledgeByTask  map[string]AnswerabilityOutcome
	TaskActionCodes  map[string]string
	Evidence         *contracts.EvidenceBundleV1
}

type runtimeTaskKnowledgeItem struct {
	TaskKey     string
	Query       string
	Intent      string
	SubIntent   string
	AnswerGroup string
	Result      *retrievers.KnowledgeRetrieveResult
	Status      enums.AIReplyTurnTaskKnowledgeStatus
	Err         error
}

type runtimeEvidenceIndex struct {
	index     int
	sourceKey string
}

type runtimeTaskKnowledgeRetriever interface {
	KnowledgeBaseIDs() []int64
	RetrieveContextByOptions(context.Context, retrievers.KnowledgeRetrieveOptions, string) (*retrievers.KnowledgeRetrieveResult, error)
}

func retrieveRuntimeTaskKnowledge(ctx context.Context, req RunInput, plans []callbacks.ReplyTaskPlanTraceData, probe *retrievers.KnowledgeRetrieveResult, taskState runtimeTaskBatchState) (runtimeTaskKnowledgeOutcome, error) {
	return retrieveRuntimeTaskKnowledgeWithRetriever(ctx, req, plans, probe, taskState, retrievers.NewKnowledgeRetriever(req.AIAgent))
}

func retrieveRuntimeTaskKnowledgeWithRetriever(ctx context.Context, req RunInput, plans []callbacks.ReplyTaskPlanTraceData, probe *retrievers.KnowledgeRetrieveResult, taskState runtimeTaskBatchState, retriever runtimeTaskKnowledgeRetriever) (runtimeTaskKnowledgeOutcome, error) {
	outcome := runtimeTaskKnowledgeOutcome{
		ActiveTaskPlans: append([]callbacks.ReplyTaskPlanTraceData(nil), plans...),
		KnowledgeByTask: make(map[string]AnswerabilityOutcome),
		TaskActionCodes: make(map[string]string),
	}
	knowledgePlans := make([]callbacks.ReplyTaskPlanTraceData, 0, len(plans))
	for _, plan := range plans {
		if runtimeTaskTypeForPlan(plan) == enums.AIReplyTurnTaskTypeKnowledge {
			knowledgePlans = append(knowledgePlans, plan)
		}
	}
	if len(knowledgePlans) == 0 {
		return outcome, nil
	}

	items := make([]runtimeTaskKnowledgeItem, len(knowledgePlans))
	semaphore := make(chan struct{}, runtimeKnowledgeTaskConcurrency)
	var wg sync.WaitGroup
	for index, plan := range knowledgePlans {
		items[index] = runtimeTaskKnowledgeItem{TaskKey: plan.TaskKey, Query: runtimeTaskKnowledgeQuery(plan), Intent: plan.Intent, SubIntent: plan.SubIntent}
		if index == 0 && len(knowledgePlans) == 1 && probe != nil {
			items[index].Result = probe
			items[index].Status = runtimeKnowledgeStatus(probe, nil)
			continue
		}
		if len(retriever.KnowledgeBaseIDs()) == 0 {
			items[index].Status = enums.AIReplyTurnTaskKnowledgeStatusNoHit
			continue
		}
		wg.Add(1)
		go func(itemIndex int) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				items[itemIndex].Err = ctx.Err()
				items[itemIndex].Status = enums.AIReplyTurnTaskKnowledgeStatusFailed
				return
			}
			options := retrievers.DefaultKnowledgeRetrieveOptions()
			options.QueryPreview = preview(items[itemIndex].Query, 120)
			if options.MaxContextItems <= 0 || options.MaxContextItems > 2 {
				options.MaxContextItems = 2
			}
			if options.TopK <= 0 || options.TopK > 4 {
				options.TopK = 4
			}
			result, err := retriever.RetrieveContextByOptions(ctx, options, items[itemIndex].Query)
			items[itemIndex].Result = result
			items[itemIndex].Err = err
			items[itemIndex].Status = runtimeKnowledgeStatus(result, err)
			items[itemIndex].AnswerGroup = runtimeKnowledgeAnswerGroup(result)
		}(index)
	}
	wg.Wait()

	updates := make([]services.AIReplyTurnTaskKnowledgeUpdate, 0, len(items))
	failed := make(map[string]struct{})
	for _, item := range items {
		outcome.KnowledgeTaskIDs = append(outcome.KnowledgeTaskIDs, item.TaskKey)
		resultCode := string(item.Status)
		if item.Status == enums.AIReplyTurnTaskKnowledgeStatusFailed {
			resultCode = "knowledge_unavailable"
			outcome.FailedTaskKeys = append(outcome.FailedTaskKeys, item.TaskKey)
			failed[item.TaskKey] = struct{}{}
		}
		if item.Status == enums.AIReplyTurnTaskKnowledgeStatusNoHit {
			outcome.NoHitTaskKeys = append(outcome.NoHitTaskKeys, item.TaskKey)
		}
		hitCount := 0
		if item.Result != nil {
			hitCount = len(item.Result.Hits)
		}
		updates = append(updates, services.AIReplyTurnTaskKnowledgeUpdate{
			TaskKey: item.TaskKey, Status: item.Status, HitCount: hitCount, ResultCode: resultCode,
		})
	}
	if taskState.Enabled && len(updates) > 0 {
		if err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
			return services.AIReplyTurnTaskService.MarkKnowledgeResultsDB(tx.Tx, req.Conversation.TenantID, taskState.TurnID, req.JobID, updates)
		}); err != nil {
			return runtimeTaskKnowledgeOutcome{}, err
		}
	}

	if len(failed) > 0 {
		active := make([]callbacks.ReplyTaskPlanTraceData, 0, len(plans)-len(failed))
		for _, plan := range plans {
			if _, isFailed := failed[plan.TaskKey]; !isFailed {
				active = append(active, plan)
			}
		}
		outcome.ActiveTaskPlans = active
	}
	outcome.ActiveTaskPlans = applyRuntimeKnowledgeAnswerGroups(outcome.ActiveTaskPlans, items)
	outcome.Prefetched = mergeRuntimeTaskKnowledge(items, retriever.KnowledgeBaseIDs())
	outcome.Evidence, outcome.KnowledgeByTask, outcome.TaskActionCodes = buildRuntimeEvidenceBundle(req, items, outcome.Prefetched)
	return outcome, nil
}

func buildRuntimeEvidenceBundle(req RunInput, items []runtimeTaskKnowledgeItem, merged *retrievers.KnowledgeRetrieveResult) (*contracts.EvidenceBundleV1, map[string]AnswerabilityOutcome, map[string]string) {
	byTask := make(map[string]AnswerabilityOutcome, len(items))
	taskActionCodes := make(map[string]string, len(items))
	bundle := &contracts.EvidenceBundleV1{
		SchemaVersion:    contracts.EvidenceBundleV1SchemaVersion,
		ScopeFingerprint: runtimeEvidenceScopeFingerprint(req, items),
		RetrievalStatus:  "not_needed", Items: []contracts.EvidenceItemV1{}, Resources: []contracts.EvidenceResourceV1{},
	}
	indexes := make(map[string]runtimeEvidenceIndex)
	for _, item := range items {
		outcome := AnswerabilityOutcome{Status: "no_context", ReasonCode: "knowledge_no_context", SupportingRefs: []string{}}
		switch item.Status {
		case enums.AIReplyTurnTaskKnowledgeStatusFailed:
			outcome.Status = "unavailable"
			outcome.ReasonCode = "knowledge_unavailable"
		case enums.AIReplyTurnTaskKnowledgeStatusHit:
			outcome.Status = "has_context"
			outcome.ReasonCode = "knowledge_context_available"
		}
		results := []rag.RetrieveResult(nil)
		if item.Result != nil {
			results = item.Result.ContextResults
			if len(results) == 0 {
				results = item.Result.Hits
			}
		}
		// 证据主题校验兜底：逐任务检索即使命中，若命中内容全是别的主题（如“开门”任务却命中周边游玩），
		// 也视为错配，降级 no_context，走转接/追问，而不是让模型拿错误证据自由发挥。
		if outcome.Status == "has_context" && len(results) > 0 {
			contents := make([]string, 0, len(results))
			for _, r := range results {
				contents = append(contents, strings.TrimSpace(r.Content))
			}
			if !knowledgeEvidenceMatchesTopic(item.SubIntent, contents) {
				outcome.Status = "no_context"
				outcome.ReasonCode = "knowledge_topic_mismatch"
				outcome.SupportingRefs = nil
				results = nil
			}
		}
		for _, result := range results {
			if len(bundle.Items) >= 24 || strings.TrimSpace(result.Content) == "" {
				break
			}
			key := runtimeEvidenceResultKey(result)
			entry, exists := indexes[key]
			if !exists {
				ref := fmt.Sprintf("K%d", len(bundle.Items)+1)
				bundle.Items = append(bundle.Items, contracts.EvidenceItemV1{
					Ref: ref, SourceType: "fastgpt", TaskKeys: []string{item.TaskKey},
					Title:   boundedEvidenceText(firstNonEmpty(result.Title, result.DocumentTitle), 200),
					Content: boundedEvidenceText(result.Content, 4000), Score: clampEvidenceScore(float64(result.Score)),
					Answerability: "supporting", ResourceRefs: []string{},
				})
				entry = runtimeEvidenceIndex{index: len(bundle.Items) - 1, sourceKey: runtimeEvidenceSourceKey(result.KnowledgeBaseID, result.SourceRecordID)}
				indexes[key] = entry
			} else {
				bundle.Items[entry.index].TaskKeys = appendUniqueStrings(bundle.Items[entry.index].TaskKeys, item.TaskKey)
			}
			outcome.SupportingRefs = appendUniqueStrings(outcome.SupportingRefs, bundle.Items[entry.index].Ref)
		}
		// 只有「排名第一」的检索结果（实际采用的答案）才允许触发"转接"判定；
		// 后续候选只是噪声，不能因为它们正文里出现"转接"就误判转人工。
		if outcome.Status == "has_context" && strings.TrimSpace(taskActionCodes[item.TaskKey]) == "" && len(results) > 0 {
			top := results[0]
			if actionCode := services.KnowledgeActionBindingService.ActionCodeForHit(
				req.Conversation.TenantID, req.Conversation.StoreID, top.KnowledgeBaseID, top.SourceRecordID,
			); actionCode != "" {
				taskActionCodes[item.TaskKey] = actionCode
			} else if knowledgeContentRequiresHandoff(top.Content) {
				taskActionCodes[item.TaskKey] = "human_handoff"
			}
		}
		// 要动作（service_request）但知识库没有任何答案（no_context）：系统没有自动执行能力，
		// 统一转人工二次确认，不再让模型在"没答案"时自由发挥、口头编造"帮你办/改成1203"。
		if outcome.Status == "no_context" && strings.TrimSpace(item.Intent) == "service_request" {
			taskActionCodes[item.TaskKey] = "human_handoff"
		}
		if outcome.Status == "has_context" && len(outcome.SupportingRefs) == 0 {
			outcome.Status = "unanswerable"
			outcome.ReasonCode = "knowledge_context_not_supporting"
		}
		byTask[item.TaskKey] = outcome
	}

	if merged != nil {
		resourceTaskKeys := runtimeEvidenceResourceTaskKeys(items)
		for _, resource := range resolveRuntimeKnowledgeResources(req, merged) {
			if len(bundle.Resources) >= 16 || strings.TrimSpace(resource.AssetID) == "" {
				break
			}
			sourceKey := runtimeEvidenceSourceKey(resource.KnowledgeBaseID, resource.SourceRecordID)
			taskKeys := append([]string(nil), resourceTaskKeys[sourceKey]...)
			if len(taskKeys) == 0 {
				continue
			}
			ref := fmt.Sprintf("R%d", len(bundle.Resources)+1)
			assetID := strings.TrimSpace(resource.AssetID)
			bundle.Resources = append(bundle.Resources, contracts.EvidenceResourceV1{
				Ref: ref, Type: "image", AssetID: &assetID,
				Title: boundedEvidenceText(firstNonEmpty(resource.Title, resource.Description), 200), TaskKeys: taskKeys,
			})
			for index := range bundle.Items {
				if !evidenceItemMatchesSource(bundle.Items[index], indexes, sourceKey) {
					continue
				}
				bundle.Items[index].ResourceRefs = appendUniqueStrings(bundle.Items[index].ResourceRefs, ref)
			}
		}
	}

	statusCounts := make(map[string]int)
	for _, outcome := range byTask {
		statusCounts[outcome.Status]++
	}
	switch {
	case statusCounts["has_context"] > 0:
		bundle.RetrievalStatus = "has_context"
	case statusCounts["unavailable"] > 0:
		bundle.RetrievalStatus = "unavailable"
	case statusCounts["unanswerable"] > 0:
		bundle.RetrievalStatus = "unanswerable"
	case len(byTask) > 0:
		bundle.RetrievalStatus = "no_context"
	}
	return bundle, byTask, taskActionCodes
}

func runtimeEvidenceScopeFingerprint(req RunInput, items []runtimeTaskKnowledgeItem) string {
	parts := []string{fmt.Sprintf("tenant:%d", req.Conversation.TenantID), fmt.Sprintf("store:%d", req.Conversation.StoreID)}
	knowledgeIDs := make(map[int64]struct{})
	for _, item := range items {
		if item.Result == nil {
			continue
		}
		for _, id := range item.Result.KnowledgeBaseIDs {
			knowledgeIDs[id] = struct{}{}
		}
	}
	ids := make([]int64, 0, len(knowledgeIDs))
	for id := range knowledgeIDs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	db := sqls.DB()
	for _, id := range ids {
		if db == nil {
			parts = append(parts, fmt.Sprintf("knowledge:%d:unresolved", id))
			continue
		}
		knowledge := repositories.KnowledgeBaseRepository.Get(db, id)
		if knowledge == nil || knowledge.TenantID != req.Conversation.TenantID || knowledge.StoreID != req.Conversation.StoreID {
			parts = append(parts, fmt.Sprintf("knowledge:%d:scope_invalid", id))
			continue
		}
		parts = append(parts, fmt.Sprintf("knowledge:%d:%s:%d:%d", id, knowledge.FastGPTProfileRevision, knowledge.FastGPTAppliedProfileRevision, knowledge.UpdatedAt.UnixNano()))
	}
	var profile *models.ReplyIntentProfile
	if db != nil {
		profile = resolveRuntimeIntentProfile(resolveRuntimeIntentScope(req))
	}
	if profile != nil {
		parts = append(parts, fmt.Sprintf("intent_profile:%d", profile.Revision))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "/")))
	return hex.EncodeToString(sum[:])
}

func runtimeEvidenceResourceTaskKeys(items []runtimeTaskKnowledgeItem) map[string][]string {
	ret := make(map[string][]string)
	for _, item := range items {
		if item.Result == nil {
			continue
		}
		results := append([]rag.RetrieveResult(nil), item.Result.ContextResults...)
		results = append(results, item.Result.Hits...)
		for _, result := range results {
			key := runtimeEvidenceSourceKey(result.KnowledgeBaseID, result.SourceRecordID)
			if key != "" {
				ret[key] = appendUniqueStrings(ret[key], item.TaskKey)
			}
		}
	}
	return ret
}

func evidenceItemMatchesSource(item contracts.EvidenceItemV1, indexes map[string]runtimeEvidenceIndex, sourceKey string) bool {
	for _, entry := range indexes {
		if entry.sourceKey == sourceKey && item.Ref == fmt.Sprintf("K%d", entry.index+1) {
			return true
		}
	}
	return false
}

func runtimeEvidenceResultKey(item rag.RetrieveResult) string {
	contentHash := sha256.Sum256([]byte(strings.TrimSpace(item.Content)))
	return runtimeEvidenceSourceKey(item.KnowledgeBaseID, item.SourceRecordID) + "|" + hex.EncodeToString(contentHash[:8])
}

func runtimeEvidenceSourceKey(knowledgeBaseID int64, sourceRecordID string) string {
	sourceRecordID = strings.TrimSpace(sourceRecordID)
	if knowledgeBaseID <= 0 || sourceRecordID == "" {
		return ""
	}
	return fmt.Sprintf("%d:%s", knowledgeBaseID, sourceRecordID)
}

func boundedEvidenceText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return strings.TrimSpace(string(runes))
}

func clampEvidenceScore(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func runtimeKnowledgeAnswerGroup(result *retrievers.KnowledgeRetrieveResult) string {
	if result == nil {
		return ""
	}
	var item rag.RetrieveResult
	switch {
	case len(result.Hits) > 0:
		item = result.Hits[0]
	case len(result.ContextResults) > 0:
		item = result.ContextResults[0]
	default:
		return ""
	}
	sourceRecordID := strings.TrimSpace(item.SourceRecordID)
	if item.KnowledgeBaseID <= 0 || sourceRecordID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", item.KnowledgeBaseID, sourceRecordID)))
	return "knowledge_answer_" + hex.EncodeToString(sum[:8])
}

func applyRuntimeKnowledgeAnswerGroups(plans []callbacks.ReplyTaskPlanTraceData, items []runtimeTaskKnowledgeItem) []callbacks.ReplyTaskPlanTraceData {
	groupByTaskKey := make(map[string]string, len(items))
	groupCounts := make(map[string]int, len(items))
	for _, item := range items {
		if item.Status != enums.AIReplyTurnTaskKnowledgeStatusHit || strings.TrimSpace(item.AnswerGroup) == "" {
			continue
		}
		groupByTaskKey[item.TaskKey] = item.AnswerGroup
		groupCounts[item.AnswerGroup]++
	}
	ret := append([]callbacks.ReplyTaskPlanTraceData(nil), plans...)
	for index := range ret {
		group := strings.TrimSpace(groupByTaskKey[ret[index].TaskKey])
		if group != "" && groupCounts[group] > 1 {
			ret[index].AnswerGroup = group
		}
	}
	return ret
}

func runtimeTaskKnowledgeQuery(plan callbacks.ReplyTaskPlanTraceData) string {
	text := strings.TrimSpace(currentTurnDisplayText(plan.Text))
	// 意图模型把 task.text 填成整句原文（含多个主题）时，逐任务检索会错配到别的主题。
	// 此时用 subIntent 的锚点词检索，保证每个子任务拿到自己的证据（例如“怎么开门”命中刷脸开门）。
	if text != "" && isMultiTopicText(text) {
		if anchor := knowledgeSubIntentAnchor(plan.SubIntent); anchor != "" {
			return anchor
		}
	}
	if text != "" {
		return text
	}
	if subIntent := strings.TrimSpace(plan.SubIntent); subIntent != "" {
		return subIntent
	}
	return strings.TrimSpace(plan.Intent)
}

// isMultiTopicText 判断 text 是否是「多主题整句」：含多个疑问语气词，或含明显的并列/追加连接词。
// 单主题子句（如“怎么把门打开”）不命中，仍直接用原话检索。
func isMultiTopicText(text string) bool {
	compact := compactRuntimeProtocolText(text)
	if compact == "" {
		return false
	}
	questionCount := 0
	for _, r := range compact {
		switch r {
		case '？', '?', '吗', '啊', '呢':
			questionCount++
		}
	}
	if questionCount >= 2 {
		return true
	}
	return containsAny(compact, []string{"以及", "还有", "另外", "再加上", "顺便", "还有啊"})
}

// knowledgeSubIntentAnchor 是酒店信息子意图的检索锚点（核心主题词，空格分隔）。
// 当 task.text 不可靠（整句多主题）时，用锚点词检索，保证每个子任务拿到自己的证据。
// 这是「主题锚点」而非「动作黑名单」：每个已定义 subIntent 对应少量核心词，数量有限。
func knowledgeSubIntentAnchor(subIntent string) string {
	switch strings.TrimSpace(subIntent) {
	case "door_access", "open_door", "door_lock":
		return "开门 刷脸 房门 门锁 人脸 房卡"
	case "network_wifi":
		return "wifi 无线网 网络 密码"
	case "tv_cast":
		return "投屏 电视"
	case "air_conditioner":
		return "空调 制冷 制热"
	case "checkin_process", "check_in":
		return "入住 办理入住"
	case "checkout_process":
		return "退房 退房流程"
	case "supplies_self_help":
		return "拖鞋 牙刷 毛巾 用品 领取"
	case "laundry":
		return "洗衣 洗衣机"
	case "parking":
		return "停车 车位 停车场"
	case "breakfast":
		return "早餐 早饭"
	case "invoice":
		return "发票 报销 抬头"
	case "surrounding_facilities":
		return "附近 周边 景点 公园 街 推荐"
	case "discount":
		return "优惠 折扣 续住"
	case "address", "address_for_delivery", "delivery_address", "store_address":
		return "地址 位置 收货 外卖地址 酒店地址 大厦 大楼 街道 路"
	default:
		return ""
	}
}

// knowledgeEvidenceMatchesTopic 判断证据内容是否命中该 subIntent 的主题词。
// 用于兜底：即使逐任务检索命中了内容，若这些内容全是别的主题（例如“开门”任务却命中周边游玩），
// 也视为错配，降级 no_context，走转接/追问，而不是让模型拿错误证据自由发挥。
func knowledgeEvidenceMatchesTopic(subIntent string, contents []string) bool {
	anchor := knowledgeSubIntentAnchor(subIntent)
	if anchor == "" {
		return true // 无主题锚点的 subIntent 不做主题校验，避免误伤
	}
	terms := strings.Fields(anchor)
	joined := strings.ToLower(strings.Join(contents, " "))
	for _, term := range terms {
		if strings.Contains(joined, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func runtimeKnowledgeStatus(result *retrievers.KnowledgeRetrieveResult, err error) enums.AIReplyTurnTaskKnowledgeStatus {
	if err != nil {
		return enums.AIReplyTurnTaskKnowledgeStatusFailed
	}
	if result == nil || len(result.Hits) == 0 || strings.TrimSpace(result.ContextText) == "" {
		return enums.AIReplyTurnTaskKnowledgeStatusNoHit
	}
	return enums.AIReplyTurnTaskKnowledgeStatusHit
}

func mergeRuntimeTaskKnowledge(items []runtimeTaskKnowledgeItem, knowledgeBaseIDs []int64) *retrievers.KnowledgeRetrieveResult {
	merged := &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: append([]int64(nil), knowledgeBaseIDs...)}
	seenHits := map[string]bool{}
	seenContext := map[string]bool{}
	sections := make([]string, 0, len(items))
	for _, item := range items {
		result := item.Result
		if result == nil || item.Status == enums.AIReplyTurnTaskKnowledgeStatusFailed {
			continue
		}
		if merged.Query == "" {
			merged.Query = item.Query
		}
		if len(merged.Policies) == 0 {
			merged.Policies = append([]retrievers.KnowledgeBaseRetrievePolicy(nil), result.Policies...)
		}
		if result.AnswerMode != 0 {
			merged.AnswerMode = result.AnswerMode
		}
		if result.TopScore > merged.TopScore {
			merged.TopScore = result.TopScore
		}
		merged.Hits = appendUniqueRuntimeRetrieveResults(merged.Hits, result.Hits, seenHits)
		merged.ContextResults = appendUniqueRuntimeRetrieveResults(merged.ContextResults, result.ContextResults, seenContext)
		merged.TraceItems = append(merged.TraceItems, result.TraceItems...)
		if strings.TrimSpace(result.ContextText) != "" {
			sections = append(sections, "【任务 "+item.TaskKey+"｜问题："+item.Query+"】\n"+strings.TrimSpace(result.ContextText))
		}
		if merged.TraceSummary.TopK == 0 && merged.TraceSummary.ContextMaxTokens == 0 {
			merged.TraceSummary = result.TraceSummary
		}
	}
	merged.ContextText = strings.TrimSpace(strings.Join(sections, "\n\n"))
	if merged.ContextText == "" && len(merged.ContextResults) > 0 {
		merged.ContextText = strings.TrimSpace(rag.Retrieve.BuildContext(context.Background(), merged.ContextResults, 1<<30))
	}
	merged.TraceSummary.HitCount = len(merged.Hits)
	merged.TraceSummary.ContextCount = len(merged.ContextResults)
	if len(merged.Hits) == 0 && merged.ContextText == "" {
		return nil
	}
	return merged
}

// knowledgeContentRequiresHandoff 判断知识正文是否明确要求转接。
// 只认"转接"两个字精确出现；"转人工/需要人工/人工客服/联系人工"等一律不算，
// 避免把"让客户打客服电话"这类知识误判成要转接。
func knowledgeContentRequiresHandoff(content string) bool {
	return strings.Contains(strings.TrimSpace(content), "转接")
}
