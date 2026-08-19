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

const (
	runtimeKnowledgeTaskConcurrency        = 4
	runtimeNormalCheckinKnowledgeQueryText = "如何通过入住小程序完成入住登记 登记完成后怎么刷脸开门"
)

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
	TaskKey      string
	Query        string
	Intent       string
	SubIntent    string
	AnswerGroup  string
	FilterReason string
	Result       *retrievers.KnowledgeRetrieveResult
	Status       enums.AIReplyTurnTaskKnowledgeStatus
	Err          error
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
	// 意图模型未把 task.text 拆成子句、而是整句复制到每个任务时，逐任务检索会错配。
	// 确定性兜底：按连接词拆分整句为子句，按任务 Sequence 顺序一一对应，不靠 subIntent 锚点。
	knowledgePlans = redistributeMultiTopicClauses(knowledgePlans)

	items := make([]runtimeTaskKnowledgeItem, len(knowledgePlans))
	semaphore := make(chan struct{}, runtimeKnowledgeTaskConcurrency)
	var wg sync.WaitGroup
	executeQuery := func(itemIndex int, query string, options retrievers.KnowledgeRetrieveOptions) (*retrievers.KnowledgeRetrieveResult, error) {
		select {
		case semaphore <- struct{}{}:
			defer func() { <-semaphore }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		plan := BuildKnowledgeQueryPlan(req.Conversation.TenantID, req.Conversation.StoreID, req.Conversation.ID, req.UserMessage.SessionNo,
			query, "answer", taskState.TurnID, options.TaskID, items[itemIndex].TaskKey)
		return ExecuteKnowledgeQuery(ctx, sqls.DB(), plan, retriever, options, nil)
	}
	// 生产回归 2026-08-18：模型把多主题整句压成单个任务时（吃的+玩的+开门），
	// 单条整句检索只命中其中一两个主题，其余主题零证据被模型自由发挥
	// （编造“刷卡还是密码锁”）。对单任务多主题按连接词确定性拆成多路检索，
	// 命中合并回同一任务，与多任务拆题行为对齐。
	singleTaskClauses := make([]string, 0)
	if len(knowledgePlans) == 1 {
		if clauses := dedupeAdjacentClauses(splitMultiTopicClauses(runtimeTaskKnowledgeQuery(knowledgePlans[0]))); len(clauses) > 1 {
			singleTaskClauses = clauses
		}
	}
	for index, plan := range knowledgePlans {
		items[index] = runtimeTaskKnowledgeItem{TaskKey: plan.TaskKey, Query: runtimeTaskKnowledgeQuery(plan), Intent: plan.Intent, SubIntent: plan.SubIntent}
		if runtimeTaskHasAuthoritativeStoreFact(req, plan) {
			items[index].Status = enums.AIReplyTurnTaskKnowledgeStatusHit
			continue
		}
		if index == 0 && len(knowledgePlans) == 1 && probe != nil && len(singleTaskClauses) == 0 {
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
			queries := []string{items[itemIndex].Query}
			if itemIndex == 0 && len(singleTaskClauses) > 0 {
				queries = singleTaskClauses
			}
			options := retrievers.DefaultKnowledgeRetrieveOptions()
			options.QueryPreview = preview(items[itemIndex].Query, 120)
			options.TurnID = taskState.TurnID
			options.TaskID = taskState.TaskIDByTaskKey[items[itemIndex].TaskKey]
			options.TaskKey = items[itemIndex].TaskKey
			// 契约 4.12/3.9.5：不得固定截断前两条；按 top-multi 预算保留
			// 排名靠后但必要的证据（剃须刀排第 4 名必须有资格进入 Generate）。
			if options.MaxContextItems <= 0 || options.MaxContextItems > knowledgeContextItemBudget {
				options.MaxContextItems = knowledgeContextItemBudget
			}
			if options.TopK <= 0 || options.TopK > knowledgeTopKBudget {
				options.TopK = knowledgeTopKBudget
			}
			// 契约 4.18/22.12：统一执行器——checkpoint 复用 + 租约 + 有界并发。
			if len(queries) == 1 {
				result, err := executeQuery(itemIndex, queries[0], options)
				items[itemIndex].Result = result
				items[itemIndex].Err = err
				items[itemIndex].Status = runtimeKnowledgeStatus(result, err)
				items[itemIndex].AnswerGroup = runtimeKnowledgeAnswerGroup(result)
				return
			}
			subItems := make([]runtimeTaskKnowledgeItem, len(queries))
			var subWG sync.WaitGroup
			for queryIndex, query := range queries {
				subWG.Add(1)
				go func(index int, subQuery string) {
					defer subWG.Done()
					result, err := executeQuery(itemIndex, subQuery, options)
					subItems[index] = runtimeTaskKnowledgeItem{TaskKey: items[itemIndex].TaskKey, Query: subQuery,
						Intent: items[itemIndex].Intent, SubIntent: items[itemIndex].SubIntent,
						Result: result, Err: err, Status: runtimeKnowledgeStatus(result, err)}
				}(queryIndex, query)
			}
			subWG.Wait()
			merged := mergeRuntimeTaskKnowledge(subItems, retriever.KnowledgeBaseIDs())
			items[itemIndex].Result = merged
			items[itemIndex].Err = nil
			items[itemIndex].Status = runtimeKnowledgeStatus(merged, nil)
			items[itemIndex].AnswerGroup = runtimeKnowledgeAnswerGroup(merged)
		}(index)
	}
	wg.Wait()
	for index := range items {
		judgeRuntimeTaskKnowledgeEvidence(req, &items[index])
		items[index].AnswerGroup = runtimeKnowledgeAnswerGroup(items[index].Result)
	}

	updates := make([]services.AIReplyTurnTaskKnowledgeUpdate, 0, len(items))
	for _, item := range items {
		outcome.KnowledgeTaskIDs = append(outcome.KnowledgeTaskIDs, item.TaskKey)
		resultCode := string(item.Status)
		if item.Status == enums.AIReplyTurnTaskKnowledgeStatusFailed {
			resultCode = "knowledge_unavailable"
			outcome.FailedTaskKeys = append(outcome.FailedTaskKeys, item.TaskKey)
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
			RetrieveLogID: retrieveLogIDFor(item), QueryFingerprint: knowledgeQueryFingerprint(item.Query),
		})
	}
	if taskState.Enabled && len(updates) > 0 {
		if err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
			return services.AIReplyTurnTaskService.MarkKnowledgeResultsDB(tx.Tx, req.Conversation.TenantID, taskState.TurnID, req.JobID, updates)
		}); err != nil {
			return runtimeTaskKnowledgeOutcome{}, err
		}
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
	storeFactSeq := 0
	addressTaskKeys := make([]string, 0, len(items))
	identityTaskKeys := make([]string, 0, len(items))
	checkinTaskKeys := make([]string, 0, len(items))
	for _, item := range items {
		// ProtectedFact Phase1（文档 9.3/18.2）：地址类任务的 store.address 是权威事实，
		// 从 hydrate 后实例确定性取值注入 S* 证据。Generate 只能用它声明酒店地址，
		// FactSourceBoundaryValidator 拒绝任何与它不一致的地址断言（如客户 OCR 里的壹间公寓）。
		if taskRequestsStoreAddress(item.SubIntent, item.Query) {
			addressTaskKeys = append(addressTaskKeys, item.TaskKey)
		}
		if isStoreIdentitySubIntent(item.SubIntent) {
			identityTaskKeys = append(identityTaskKeys, item.TaskKey)
		}
		if isNormalCheckinKnowledgeItem(item) {
			checkinTaskKeys = append(checkinTaskKeys, item.TaskKey)
		}
		outcome := AnswerabilityOutcome{Status: "no_context", ReasonCode: "knowledge_no_context", SupportingRefs: []string{}}
		switch item.Status {
		case enums.AIReplyTurnTaskKnowledgeStatusFailed:
			outcome.Status = "unavailable"
			outcome.ReasonCode = "knowledge_unavailable"
		case enums.AIReplyTurnTaskKnowledgeStatusHit:
			outcome.Status = "has_context"
			outcome.ReasonCode = "knowledge_context_available"
		}
		if item.Status == enums.AIReplyTurnTaskKnowledgeStatusNoHit && strings.TrimSpace(item.FilterReason) != "" {
			outcome.ReasonCode = item.FilterReason
		}
		results := []rag.RetrieveResult(nil)
		if item.Result != nil {
			results = item.Result.ContextResults
			if len(results) == 0 {
				results = item.Result.Hits
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
		// 动作只认显式 KnowledgeActionBinding。知识正文是事实证据，不是控制协议；
		// “转接”“人工”等普通文字不得改变路由状态。
		if outcome.Status == "has_context" && strings.TrimSpace(taskActionCodes[item.TaskKey]) == "" && len(results) > 0 {
			top := results[0]
			if actionCode := services.KnowledgeActionBindingService.ActionCodeForHit(
				req.Conversation.TenantID, req.Conversation.StoreID, top.KnowledgeBaseID, top.SourceRecordID,
			); actionCode != "" {
				taskActionCodes[item.TaskKey] = actionCode
			}
		}
		if outcome.Status == "has_context" && len(outcome.SupportingRefs) == 0 {
			outcome.Status = "unanswerable"
			outcome.ReasonCode = "knowledge_context_not_supporting"
		}
		byTask[item.TaskKey] = outcome
	}

	storeIdentityTaskKeys := appendUniqueStrings(append([]string(nil), identityTaskKeys...), addressTaskKeys...)
	if len(storeIdentityTaskKeys) > 0 {
		if name := authoritativeStoreIdentity(req); name != "" {
			storeFactSeq++
			ref := fmt.Sprintf("S%d", storeFactSeq)
			bundle.Items = append(bundle.Items, contracts.EvidenceItemV1{
				Ref: ref, SourceType: "store_fact", TaskKeys: storeIdentityTaskKeys,
				Title: authoritativeStoreNameEvidenceTitle, Content: name, Score: 1,
				Answerability: "supporting", ResourceRefs: []string{},
			})
			for _, taskKey := range storeIdentityTaskKeys {
				outcome := byTask[taskKey]
				outcome.SupportingRefs = appendUniqueStrings(outcome.SupportingRefs, ref)
				outcome.Status = "has_context"
				outcome.ReasonCode = "authoritative_store_fact_available"
				byTask[taskKey] = outcome
			}
		}
	}

	// ProtectedFact Phase1：地址类任务注入 store.address 权威 S* 证据（文档 9.3）。
	// 有值时地址任务至少 has_context 且 S ref 进入 SupportingRefs；
	// 未配置时不伪造，地址断言由边界校验兜底（禁止从 OCR/历史猜地址）。
	if len(addressTaskKeys) > 0 {
		if address := authoritativeStoreAddress(req); address != "" {
			storeFactSeq++
			ref := fmt.Sprintf("S%d", storeFactSeq)
			bundle.Items = append(bundle.Items, contracts.EvidenceItemV1{
				Ref: ref, SourceType: "store_fact", TaskKeys: append([]string(nil), addressTaskKeys...),
				Title: authoritativeStoreAddressEvidenceTitle, Content: address, Score: 1,
				Answerability: "supporting", ResourceRefs: []string{},
			})
			for _, taskKey := range addressTaskKeys {
				outcome := byTask[taskKey]
				outcome.SupportingRefs = appendUniqueStrings(outcome.SupportingRefs, ref)
				if outcome.Status == "no_context" {
					outcome.Status = "has_context"
					outcome.ReasonCode = "authoritative_store_fact_available"
				}
				byTask[taskKey] = outcome
			}
		}
	}

	// 正常入住流程同时依赖系统配置事实和门店知识：当前门店确实配置了
	// 可发送的入住小程序时，系统可确定“先登记、后刷脸开门”的基础流程。
	// 这份事实只绑定正常入住 Task；小程序打不开、手机不可用等异常问题仍
	// 必须依赖对应知识，不能被正常流程覆盖。
	if len(checkinTaskKeys) > 0 && runtimeCheckinMiniProgramAvailable(req) {
		storeFactSeq++
		ref := fmt.Sprintf("S%d", storeFactSeq)
		bundle.Items = append(bundle.Items, contracts.EvidenceItemV1{
			Ref: ref, SourceType: "store_fact", TaskKeys: append([]string(nil), checkinTaskKeys...),
			Title:   "当前门店入住方式（系统权威）",
			Content: "当前门店为无人值守智能化酒店，没有传统常驻前台和房卡；客户通过当前门店已配置的入住小程序完成入住登记，登记完成后到店刷脸开门。",
			Score:   1, Answerability: "supporting", ResourceRefs: []string{},
		})
		for _, taskKey := range checkinTaskKeys {
			outcome := byTask[taskKey]
			outcome.SupportingRefs = appendUniqueStrings(outcome.SupportingRefs, ref)
			outcome.Status = "has_context"
			outcome.ReasonCode = "authoritative_checkin_store_fact_available"
			byTask[taskKey] = outcome
		}
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
		// 检索与回答保持 top 多条；此处只是记录「哪个命中记录关联哪些任务」，
		// 图片是否真的发送由 runtimeActionInputs 的 ResourceEligibility 门禁决定
		//（如地址文字任务禁图），不在绑定层做限制。
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

// runtimeKnowledgeAnswerGroup 契约 4.13/11.1：分组依据是排序后的完整合格
// 命中集合 fingerprint，不是第一条 hit。首条 sourceRecordID 相同、相同全文
// Query、相同 subIntent 都不足以合并；只有证据集合一致（答案语义相同的
// 确定性代理）才允许一次自然回答。
func runtimeKnowledgeAnswerGroup(result *retrievers.KnowledgeRetrieveResult) string {
	if result == nil {
		return ""
	}
	items := result.Hits
	if len(items) == 0 {
		items = result.ContextResults
	}
	if len(items) == 0 {
		return ""
	}
	keys := make([]string, 0, len(items))
	for _, item := range items {
		sourceRecordID := strings.TrimSpace(item.SourceRecordID)
		if item.KnowledgeBaseID <= 0 || sourceRecordID == "" {
			continue
		}
		keys = append(keys, fmt.Sprintf("%d:%s", item.KnowledgeBaseID, sourceRecordID))
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, "|")))
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
	// Query 仍以客户当前问题为主体；若已有稳定的中文业务主题，只追加一个短主题锚点，
	// 帮助向量检索区分“普通流程”与“同词异常 FAQ”。不追加英文内部枚举，也不发起第二次检索。
	if text := runtimeTaskKnowledgeQueryText(plan); text != "" {
		text = normalizeRuntimeTaskKnowledgeQuery(plan, text)
		if text == runtimeNormalCheckinKnowledgeQueryText {
			return text
		}
		if label := runtimeKnowledgeTopicLabelForPlan(plan); label != "" && !strings.Contains(text, label) {
			return text + " " + label
		}
		return text
	}
	if subIntent := strings.TrimSpace(plan.SubIntent); subIntent != "" {
		return subIntent
	}
	return strings.TrimSpace(plan.Intent)
}

func runtimeTaskKnowledgeQueryText(plan callbacks.ReplyTaskPlanTraceData) string {
	text := strings.TrimSpace(stripKnowledgeQueryTransportWrapper(currentTurnDisplayText(plan.Text)))
	if text == "" {
		return ""
	}
	clauses := dedupeAdjacentClauses(runtimeAtomicKnowledgeClauses(text))
	if len(clauses) <= 1 || strings.TrimSpace(plan.SubIntent) == "" {
		return text
	}

	matched := ""
	for _, clause := range clauses {
		if !runtimeTaskKnowledgeClauseMatchesSubIntent(plan.SubIntent, clause) {
			continue
		}
		if matched != "" {
			// 多个子句属于同一业务主题时无法仅凭 SubIntent 可靠定位；保留
			// 原有逐子句检索兜底，避免任意丢掉其中一个真实问题。
			return text
		}
		matched = clause
	}
	if matched != "" {
		return matched
	}
	return text
}

func runtimeTaskKnowledgeClauseMatchesSubIntent(subIntent, clause string) bool {
	target := strings.TrimSpace(subIntent)
	inferred := strings.TrimSpace(runtimeAtomicClauseSubIntent("", clause))
	if target == "" || inferred == "" {
		return false
	}
	if target == inferred {
		return true
	}
	targetLabel := runtimeKnowledgeTopicLabel(target)
	return targetLabel != "" && targetLabel == runtimeKnowledgeTopicLabel(inferred)
}

func normalizeRuntimeTaskKnowledgeQuery(plan callbacks.ReplyTaskPlanTraceData, text string) string {
	if isNormalCheckinKnowledgeRequest(plan.SubIntent, text) {
		return runtimeNormalCheckinKnowledgeQueryText
	}
	topics := detectKnowledgeTopicClasses(text)
	_, hasSupplyObject := topics["supplies"]
	if isRuntimeSupplySubIntent(plan.SubIntent) || hasSupplyObject {
		if normalized := normalizeRuntimeSpecificSupplyQuery(text); normalized != "" {
			return normalized
		}
	}
	return text
}

func isNormalCheckinKnowledgeRequest(subIntent, text string) bool {
	if !isCheckinProcessSubIntent(subIntent) || knowledgeTextHasExceptionContext(text) || runtimeTextRequestsEntranceRoute(text) {
		return false
	}
	return runtimeTextHasCheckinContext(text)
}

func runtimeTextRequestsEntranceRoute(text string) bool {
	compact := compactRuntimeProtocolText(text)
	return containsAny(compact, []string{"入口", "怎么走", "怎么去", "怎么上楼", "大楼在哪", "电梯在哪", "路线"})
}

func runtimeTextHasCheckinContext(text string) bool {
	compact := compactRuntimeProtocolText(text)
	return containsAny(compact, []string{"入住", "办房", "开门", "刷脸", "前台"})
}

func isRuntimeSupplySubIntent(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "supplies_self_help", "supplies", "room_supply", "room_supplies":
		return true
	default:
		return false
	}
}

// normalizeRuntimeSpecificSupplyQuery 去掉用品问题中的请求语气和存在性问法，
// 保留客户实际询问的对象。FastGPT 的 FAQ 标题通常以用品名为主，直接查对象
// 比追加“客用品”或保留整句口语更稳定，也适用于牙刷、拖鞋、纸笔等同类问题。
func normalizeRuntimeSpecificSupplyQuery(text string) string {
	original := strings.TrimSpace(text)
	value := strings.TrimSpace(strings.Trim(original, "，,。.!！?？；;、… "))
	if value == "" {
		return original
	}
	prefixes := []string{
		"请问你们酒店有没有", "请问酒店有没有", "你们酒店有没有", "房间里有没有", "房间有没有", "酒店有没有", "这里有没有",
		"可以给我拿点", "能不能给我拿点", "麻烦给我拿点", "能给我拿点", "帮我拿点", "给我拿点",
		"可以给我", "能不能给我", "麻烦给我", "能给我", "我想要点", "想要点", "我想要", "帮我", "给我",
		"麻烦问一下", "麻烦问下", "我想知道", "请问", "有没有",
	}
	suffixes := []string{
		"什么的有没有", "什么的有吗", "什么的吗", "什么的么", "什么的呀", "什么的啊", "什么的",
		"有没有啊", "有没有呀", "有没有呢", "有没有", "可以给吗", "能给吗", "给我吗", "有吗", "有么", "有不有",
		"可以吗", "行吗", "吗", "么", "呢", "呀", "啊",
	}
	for {
		previous := value
		for _, prefix := range prefixes {
			if strings.HasPrefix(value, prefix) {
				value = strings.TrimSpace(strings.TrimPrefix(value, prefix))
				break
			}
		}
		for _, suffix := range suffixes {
			if strings.HasSuffix(value, suffix) {
				value = strings.TrimSpace(strings.TrimSuffix(value, suffix))
				break
			}
		}
		value = strings.TrimSpace(strings.Trim(value, "，,。.!！?？；;、… "))
		if value == previous {
			break
		}
	}
	compact := compactRuntimeProtocolText(value)
	if value == "" || containsAny(compact, []string{"有哪些", "有什么", "什么用品"}) {
		return original
	}
	switch compact {
	case "用品", "客用品", "洗漱用品", "一次性用品", "东西":
		return original
	default:
		return value
	}
}

func runtimeKnowledgeTopicLabelForPlan(plan callbacks.ReplyTaskPlanTraceData) string {
	if label := runtimeKnowledgeTopicLabel(plan.SubIntent); label != "" {
		// 原子拆题会保留模型给整句的 subIntent。若当前子句本身已明显不是
		// 入住问题，不能再把“入住流程”错误追加到停车、用品等子句上。
		if label == "入住流程" && strings.TrimSpace(plan.Text) != "" && !runtimeTextHasCheckinContext(plan.Text) {
			return ""
		}
		// 具体用品名称已经是更强的检索词。追加“客用品”这类宽泛分类会让
		// FastGPT 全文检索把“草稿纸”拉向驱蚊用品、消毒用品等同类问句。
		// 只有省略了具体对象的追问，才保留大类锚点。
		if label == "客用品" {
			if _, hasSpecificSupply := detectKnowledgeTopicClasses(plan.Text)["supplies"]; hasSpecificSupply {
				return ""
			}
		}
		return label
	}
	if strings.TrimSpace(plan.SubIntent) != "surrounding_facilities" {
		return ""
	}
	compact := compactRuntimeProtocolText(plan.Text)
	switch {
	case containsAny(compact, []string{"好玩", "玩的", "游玩", "景点", "景区", "逛逛", "逛街"}):
		return "附近游玩"
	case containsAny(compact, []string{"好吃", "吃的", "餐饮", "美食", "饭店", "餐厅", "小吃", "宵夜"}):
		return "附近餐饮"
	default:
		return "周边设施"
	}
}

// expandRuntimeAtomicReplyTaskPlans 把模型仍合并在一个知识任务里的独立问句
// 在入账前拆成独立 Task。这样每个问句都有自己的检索结果、终态和回复覆盖，
// 不会再出现“咖啡命中后顺带把草稿纸也当成已回答”。
func expandRuntimeAtomicReplyTaskPlans(plans []callbacks.ReplyTaskPlanTraceData) []callbacks.ReplyTaskPlanTraceData {
	ret := make([]callbacks.ReplyTaskPlanTraceData, 0, len(plans))
	seen := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		clauses := []string{strings.TrimSpace(plan.Text)}
		taskType := runtimeTaskTypeForPlan(plan)
		if taskType == enums.AIReplyTurnTaskTypeKnowledge {
			if split := runtimeAtomicKnowledgeClauses(plan.Text); len(split) > 1 {
				clauses = split
			}
		}
		if strings.TrimSpace(plan.Text) == "" && taskType != enums.AIReplyTurnTaskTypeKnowledge {
			item := plan
			item.Sequence = len(ret) + 1
			ret = append(ret, item)
			continue
		}
		for _, clause := range clauses {
			clause = trimRuntimeAtomicClause(clause)
			if normalizeRuntimeTaskText(clause) == "" {
				continue
			}
			item := plan
			item.TaskKey = ""
			item.AnswerGroup = ""
			item.Text = clause
			if len(clauses) > 1 {
				item.SubIntent = runtimeAtomicClauseSubIntent(item.SubIntent, clause)
			}
			key := strings.Join([]string{item.Intent, item.SubIntent, item.Output, item.ResourceAction, normalizeRuntimeTaskText(item.Text)}, "|")
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			item.Sequence = len(ret) + 1
			ret = append(ret, item)
		}
	}
	return ret
}

func runtimeAtomicKnowledgeClauses(text string) []string {
	text = strings.TrimSpace(stripKnowledgeQueryTransportWrapper(currentTurnDisplayText(text)))
	if text == "" {
		return nil
	}
	// 句号、问号、分号、换行和明显追加连接词是可靠的独立问题边界。
	strongParts := splitBySeparators([]string{text}, []string{"还有啊", "另外", "再加上", "顺便"})
	strongParts = splitBySeparators(strongParts, []string{"？", "?", "。", "！", "!", "；", ";", "\n", "……"})
	strongParts = normalizeRuntimeAtomicClauses(strongParts)

	// 每个强边界子句内再尝试按并列词和口语逗号拆分；只有各段能识别为
	// 不同业务主题才拆，避免把“早餐时间以及地点”错误拆成两个问题。
	parts := make([]string, 0, len(strongParts))
	for _, strongPart := range strongParts {
		split := []string{strongPart}
		for _, separator := range []string{"或者", "或是", "以及", "还有", "和", "跟", "、", "，", ","} {
			candidate := normalizeRuntimeAtomicClauses(splitBySeparators([]string{strongPart}, []string{separator}))
			commaBoundary := separator == "，" || separator == ","
			if len(candidate) > 1 && runtimeClausesHaveDistinctTopics(candidate) && (!commaBoundary || runtimeClausesHaveIndependentRequests(candidate)) {
				split = candidate
				break
			}
		}
		parts = append(parts, split...)
	}
	if len(parts) == 0 {
		return []string{text}
	}
	return parts
}

func runtimeAtomicClauseSubIntent(current, clause string) string {
	topics := detectKnowledgeTopicClasses(clause)
	ordered := []struct {
		topic     string
		subIntent string
	}{
		{"checkin", "checkin_process"},
		{"checkout", "checkout_process"},
		{"address", "address"},
		{"parking", "parking"},
		{"breakfast", "breakfast"},
		{"coffee", "coffee"},
		{"invoice", "invoice"},
		{"wifi", "network_wifi"},
		{"laundry", "laundry"},
		{"luggage", "luggage_storage"},
		{"housekeeping", "housekeeping"},
		{"room_change", "room_change"},
		{"door_access", "door_access"},
		{"tv", "tv_cast"},
		{"aircon", "air_conditioner"},
		{"supplies", "supplies_self_help"},
		{"discount", "discount"},
		{"nearby_food", "surrounding_facilities"},
		{"nearby_fun", "surrounding_facilities"},
		{"takeaway", "order_food_delivery"},
		{"store_name", "store_identity"},
	}
	for _, item := range ordered {
		if _, ok := topics[item.topic]; ok {
			return item.subIntent
		}
	}
	return current
}

func normalizeRuntimeAtomicClauses(parts []string) []string {
	ret := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = trimRuntimeAtomicClause(part)
		fingerprint := normalizeRuntimeTaskText(part)
		if fingerprint == "" {
			continue
		}
		if _, exists := seen[fingerprint]; exists {
			continue
		}
		seen[fingerprint] = struct{}{}
		ret = append(ret, part)
	}
	return ret
}

func trimRuntimeAtomicClause(value string) string {
	value = strings.TrimSpace(strings.Trim(value, "，,。.!！?？；;、… "))
	for {
		trimmed := value
		for _, prefix := range []string{"或者", "或是", "以及", "还有啊", "还有", "另外", "再加上", "顺便", "然后"} {
			if strings.HasPrefix(trimmed, prefix) {
				trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
				break
			}
		}
		if trimmed == value {
			return value
		}
		value = trimmed
	}
}

func runtimeClausesHaveDistinctTopics(clauses []string) bool {
	seen := make(map[string]struct{})
	for _, clause := range clauses {
		topics := detectKnowledgeTopicClasses(clause)
		if len(topics) != 1 {
			return false
		}
		for topic := range topics {
			seen[topic] = struct{}{}
		}
	}
	return len(seen) >= len(clauses)
}

func runtimeClausesHaveIndependentRequests(clauses []string) bool {
	for _, clause := range clauses {
		compact := compactRuntimeProtocolText(clause)
		if containsAny(compact, []string{
			"吗", "么", "呢", "什么", "怎么", "怎样", "如何", "哪里", "在哪", "哪儿", "多少", "几点", "是否", "有没有", "能不能", "可不可以",
			"发我", "给我", "告诉我", "帮我", "想问", "我想", "需要", "介绍一下", "推荐一下",
			"坏了", "打不开", "进不去", "连不上", "用不了", "无法", "失败", "异常", "漏水", "不制冷", "不制热", "没了", "没有了", "填错了",
		}) {
			continue
		}
		return false
	}
	return len(clauses) > 1
}

// redistributeMultiTopicClauses 是意图模型没拆好 task.text 时的确定性兜底：
// 当多个知识任务的 text 是同一个整句（多主题）时，按连接词拆成子句，
// 并按任务 Sequence 顺序一一对应。子句数不足时保持原样，避免错误分配。
func redistributeMultiTopicClauses(plans []callbacks.ReplyTaskPlanTraceData) []callbacks.ReplyTaskPlanTraceData {
	if len(plans) <= 1 {
		return plans
	}
	firstText := strings.TrimSpace(plans[0].Text)
	if firstText == "" {
		return plans
	}
	// 只有当所有知识任务的 text 都是同一个整句时才拆分；模型已拆好则不动。
	for _, p := range plans[1:] {
		if strings.TrimSpace(p.Text) != firstText {
			return plans
		}
	}
	clauses := dedupeAdjacentClauses(splitMultiTopicClauses(firstText))
	if len(clauses) != len(plans) {
		// 二级拆分：口语停顿逗号；仍不匹配则保持原样，避免错误分配。
		clauses = dedupeAdjacentClauses(splitBySeparators([]string{firstText}, []string{"，", ","}))
	}
	if len(clauses) != len(plans) {
		return plans
	}
	ret := make([]callbacks.ReplyTaskPlanTraceData, len(plans))
	for i, plan := range plans {
		plan.Text = strings.TrimSpace(clauses[i])
		ret[i] = plan
	}
	return ret
}

// splitMultiTopicClauses 按并列/追加连接词和语音自然停顿标点把整句拆成子句，
// 确定性、通用，不依赖 subIntent。契约 3.9.4：问号、句号和感叹号是语音多题的
// 真实边界；逗号仅在子句数不足时作为二级拆分尝试。
func splitMultiTopicClauses(text string) []string {
	return runtimeAtomicKnowledgeClauses(text)
}

// dedupeAdjacentClauses 去掉 ASR 口语连续重复（“都可以，都可以”）。
func dedupeAdjacentClauses(clauses []string) []string {
	ret := make([]string, 0, len(clauses))
	for _, clause := range clauses {
		if len(ret) > 0 && ret[len(ret)-1] == clause {
			continue
		}
		ret = append(ret, clause)
	}
	return ret
}

func splitBySeparators(parts []string, separators []string) []string {
	for _, sep := range separators {
		next := make([]string, 0, len(parts))
		for _, part := range parts {
			for _, seg := range strings.Split(part, sep) {
				if s := strings.TrimSpace(seg); s != "" {
					next = append(next, s)
				}
			}
		}
		parts = next
	}
	return parts
}

// stripKnowledgeQueryTransportWrapper 去除 Query 文本中的运输包装：
// “[语音] 文件名”、“语音内容是：”、burst merge 前缀与编号行首。
func stripKnowledgeQueryTransportWrapper(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	for _, prefix := range []string{"[语音]", "[图片]", "[文件]", "[附件]", "[视频]"} {
		if strings.HasPrefix(text, prefix) {
			text = strings.TrimSpace(strings.TrimPrefix(text, prefix))
			// 去掉紧随的文件名 token（无空格的中文场景按常见扩展名切）。
			for _, ext := range []string{".mp3", ".amr", ".wav", ".jpg", ".jpeg", ".png", ".pdf", ".docx"} {
				if idx := strings.Index(text, ext); idx >= 0 && idx <= 120 {
					text = strings.TrimSpace(text[idx+len(ext):])
					break
				}
			}
		}
	}
	for _, lead := range []string{"语音内容是：", "语音内容是:", "图片内容是：", "图片内容是:", "文件内容是："} {
		text = strings.TrimSpace(strings.TrimPrefix(text, lead))
	}
	if strings.HasPrefix(text, "本轮客户连续消息") {
		if idx := strings.Index(text, "\n"); idx >= 0 {
			text = text[idx:]
		}
		lines := strings.Split(text, "\n")
		body := make([]string, 0, len(lines))
		for _, line := range lines {
			line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "0123456789.、"))
			line = strings.TrimSpace(strings.TrimPrefix(line, "[消息]"))
			line = strings.TrimSpace(strings.TrimPrefix(line, "[语音]"))
			if line != "" {
				body = append(body, line)
			}
		}
		text = strings.Join(body, "；")
	}
	return strings.TrimSpace(text)
}

const (
	// knowledgeContextItemBudget 是进入 Generate 的上下文条数预算（top-multi）。
	knowledgeContextItemBudget = 5
	// knowledgeTopKBudget 是单次检索的召回条数预算。
	knowledgeTopKBudget = 5
)

// retrieveLogIDFor 从检索结果提取持久日志 ID（契约 4.17 审计链）。
func retrieveLogIDFor(item runtimeTaskKnowledgeItem) int64 {
	if item.Result == nil {
		return 0
	}
	return item.Result.RetrieveLogID
}

// knowledgeQueryFingerprint 对 Query 文本计算稳定指纹。
func knowledgeQueryFingerprint(query string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(query)))
	return hex.EncodeToString(sum[:])
}

func runtimeKnowledgeStatus(result *retrievers.KnowledgeRetrieveResult, err error) enums.AIReplyTurnTaskKnowledgeStatus {
	if err != nil {
		return enums.AIReplyTurnTaskKnowledgeStatusFailed
	}
	// Hits 是证据本体；ContextText 只是展示缓存，高并发下可能未及时拼装。
	// 生产回归 2026-08-18：命中 5 条正确答案（含行李寄存 top1）却因 ContextText
	// 为空被判 no_hit，客户收到"资料没写明"。
	if result == nil || len(result.Hits) == 0 {
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

// filterKnowledgeMetaEvidence 是 Evidence Judge 的元问题过滤（文档 7.5 判定矩阵）。
// 判定双来源：KnowledgeEvidenceMetadata 侧车表（claimType=meta）命中即剔除；
// 无侧车记录时用确定性模式（DetectKnowledgeMetaContent）兜底，覆盖未回填数据。
func filterKnowledgeMetaEvidence(req RunInput, results []rag.RetrieveResult) (kept []rag.RetrieveResult, droppedMeta int) {
	if len(results) == 0 {
		return results, 0
	}
	recordIDs := make([]string, 0, len(results))
	kbIDs := make(map[int64]struct{}, 1)
	for _, r := range results {
		if r.KnowledgeBaseID > 0 && strings.TrimSpace(r.SourceRecordID) != "" {
			recordIDs = append(recordIDs, r.SourceRecordID)
			kbIDs[r.KnowledgeBaseID] = struct{}{}
		}
	}
	metaSet := make(map[string]struct{}, len(recordIDs))
	if req.Conversation.TenantID > 0 && req.Conversation.StoreID > 0 && len(recordIDs) > 0 && len(kbIDs) == 1 {
		var kbID int64
		for id := range kbIDs {
			kbID = id
		}
		for _, meta := range services.KnowledgeEvidenceMetadataService.JudgeBySourceRecords(
			req.Conversation.TenantID, req.Conversation.StoreID, kbID, recordIDs) {
			if meta.ClaimType == "meta" || meta.TrustLevel == "blocked" {
				metaSet[meta.SourceRecordID] = struct{}{}
			}
		}
	}
	kept = make([]rag.RetrieveResult, 0, len(results))
	for _, r := range results {
		if _, blocked := metaSet[strings.TrimSpace(r.SourceRecordID)]; blocked {
			droppedMeta++
			continue
		}
		title := firstNonEmpty(r.Title, r.DocumentTitle)
		if services.DetectKnowledgeMetaContent(title, r.Content) {
			droppedMeta++
			continue
		}
		kept = append(kept, r)
	}
	return kept, droppedMeta
}
