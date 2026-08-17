package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/ai/runtime/knowledgepolicy"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

const (
	runtimeKnowledgeTaskConcurrency = 4
	runtimeEvidenceMaxItems         = 24
)

type runtimeTaskKnowledgeOutcome struct {
	Prefetched          *retrievers.KnowledgeRetrieveResult
	ActiveTaskPlans     []callbacks.ReplyTaskPlanTraceData
	FailedTaskKeys      []string
	NoHitTaskKeys       []string
	KnowledgeTaskIDs    []string
	KnowledgeByTask     map[string]AnswerabilityOutcome
	TaskActionCodes     map[string]string
	Evidence            *contracts.EvidenceBundleV1
	EvidenceV2          *contracts.EvidenceBundleV2
	ResourceEligibility contracts.ResourceEligibilityV1
}

type runtimeTaskKnowledgeItem struct {
	TaskKey     string
	Query       string
	Intent      string
	SubIntent   string
	RequestMode string
	AnswerGroup string
	Result      *retrievers.KnowledgeRetrieveResult
	Status      enums.AIReplyTurnTaskKnowledgeStatus
	Err         error
}

type runtimeEvidenceIndex struct {
	legacyIndex  int
	qualityIndex int
	ref          string
	sourceKey    string
	supporting   bool
}

type runtimeTaskKnowledgeRetriever interface {
	KnowledgeBaseIDs() []int64
	RetrieveContextByOptions(context.Context, retrievers.KnowledgeRetrieveOptions, string) (*retrievers.KnowledgeRetrieveResult, error)
}

func retrieveRuntimeTaskKnowledge(ctx context.Context, req RunInput, plans []callbacks.ReplyTaskPlanTraceData, probes runtimeConditionalKnowledgeProbes, taskState runtimeTaskBatchState) (runtimeTaskKnowledgeOutcome, error) {
	return retrieveRuntimeTaskKnowledgeWithRetriever(ctx, req, plans, probes, taskState, retrievers.NewKnowledgeRetriever(req.AIAgent))
}

func retrieveRuntimeTaskKnowledgeWithRetriever(ctx context.Context, req RunInput, plans []callbacks.ReplyTaskPlanTraceData, probes runtimeConditionalKnowledgeProbes, taskState runtimeTaskBatchState, retriever runtimeTaskKnowledgeRetriever) (runtimeTaskKnowledgeOutcome, error) {
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
	// Legacy/V2 compatibility only. V3 tasks already carry validated source
	// spans, so redistributing a full sentence by sequence would discard the
	// authoritative binding and can cross-wire two questions.
	if !runtimePlansHaveAuthoritativeSources(knowledgePlans) {
		knowledgePlans = redistributeMultiTopicClauses(knowledgePlans)
	}

	items := make([]runtimeTaskKnowledgeItem, len(knowledgePlans))
	semaphore := make(chan struct{}, knowledgeTaskConcurrencyForDB(sqls.DB()))
	var wg sync.WaitGroup
	for index, plan := range knowledgePlans {
		items[index] = runtimeTaskKnowledgeItem{TaskKey: plan.TaskKey, Query: runtimeTaskKnowledgeQuery(plan), Intent: plan.Intent, SubIntent: plan.SubIntent, RequestMode: plan.RequestMode}
		if probe := probes[conditionalKnowledgeProbeIdentityForPlan(plan)]; probe != nil {
			promoted, promoteErr := promoteConditionalProbeCheckpoint(req, taskState, items[index], probe)
			items[index].Result = promoted
			items[index].Err = promoteErr
			items[index].Status = runtimeKnowledgeStatus(promoted, promoteErr)
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
			plan := BuildKnowledgeQueryPlan(req.Conversation.TenantID, req.Conversation.StoreID, req.Conversation.ID, req.UserMessage.SessionNo,
				items[itemIndex].Query, "answer", taskState.TurnID, taskState.TurnVersion, options.TaskID, items[itemIndex].TaskKey)
			result, err := ExecuteKnowledgeQuery(ctx, plan, retriever, options, sqls.DB())
			items[itemIndex].Result = result
			items[itemIndex].Err = err
			items[itemIndex].Status = runtimeKnowledgeStatus(result, err)
			items[itemIndex].AnswerGroup = runtimeKnowledgeAnswerGroup(result)
		}(index)
	}
	wg.Wait()

	outcome.Prefetched = mergeRuntimeTaskKnowledge(items, retriever.KnowledgeBaseIDs())
	artifacts := buildRuntimeEvidenceArtifacts(req, items, outcome.Prefetched)
	for index := range items {
		if items[index].Status != enums.AIReplyTurnTaskKnowledgeStatusHit {
			continue
		}
		if taskOutcome := artifacts.ByTask[items[index].TaskKey]; taskOutcome.Status != "has_context" {
			items[index].Status = enums.AIReplyTurnTaskKnowledgeStatusNoContext
			items[index].AnswerGroup = ""
		}
	}

	updates := make([]services.AIReplyTurnTaskKnowledgeUpdate, 0, len(items))
	failed := make(map[string]struct{})
	for _, item := range items {
		outcome.KnowledgeTaskIDs = append(outcome.KnowledgeTaskIDs, item.TaskKey)
		taskOutcome := artifacts.ByTask[item.TaskKey]
		resultCode := runtimeKnowledgeResultCode(item.Status, taskOutcome)
		if item.Status == enums.AIReplyTurnTaskKnowledgeStatusFailed {
			outcome.FailedTaskKeys = append(outcome.FailedTaskKeys, item.TaskKey)
			failed[item.TaskKey] = struct{}{}
		}
		if item.Status == enums.AIReplyTurnTaskKnowledgeStatusNoHit || item.Status == enums.AIReplyTurnTaskKnowledgeStatusNoContext {
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
	outcome.Evidence = artifacts.Legacy
	outcome.EvidenceV2 = artifacts.Quality
	outcome.ResourceEligibility = artifacts.ResourceEligibility
	outcome.KnowledgeByTask = artifacts.ByTask
	outcome.TaskActionCodes = artifacts.TaskActionCodes
	return outcome, nil
}

func runtimeKnowledgeResultCode(status enums.AIReplyTurnTaskKnowledgeStatus, outcome AnswerabilityOutcome) string {
	switch status {
	case enums.AIReplyTurnTaskKnowledgeStatusFailed:
		return "knowledge_unavailable"
	case enums.AIReplyTurnTaskKnowledgeStatusNoContext:
		return firstNonEmpty(strings.TrimSpace(outcome.ReasonCode), "knowledge_no_context")
	case enums.AIReplyTurnTaskKnowledgeStatusNoHit:
		return "no_hit"
	default:
		return string(status)
	}
}

func summarizeRuntimeTaskAnswerability(byTask map[string]AnswerabilityOutcome) callbacks.AnswerabilityTraceData {
	if len(byTask) == 0 {
		return callbacks.AnswerabilityTraceData{Status: "skipped", Reason: "no knowledge task"}
	}
	hasContext := 0
	noContext := 0
	unavailable := 0
	missing := make([]string, 0, len(byTask))
	for taskKey, outcome := range byTask {
		switch outcome.Status {
		case "has_context":
			hasContext++
		case "unavailable", "unanswerable":
			unavailable++
			missing = append(missing, taskKey+":"+firstNonEmpty(outcome.ReasonCode, "knowledge_unavailable"))
		default:
			noContext++
			missing = append(missing, taskKey+":"+firstNonEmpty(outcome.ReasonCode, "knowledge_no_context"))
		}
	}
	sort.Strings(missing)
	switch {
	case hasContext > 0 && noContext == 0 && unavailable == 0:
		return callbacks.AnswerabilityTraceData{Status: "has_context", Reason: "all knowledge tasks have supporting evidence"}
	case hasContext > 0:
		return callbacks.AnswerabilityTraceData{Status: "partial_context", Reason: "some knowledge tasks lack supporting evidence", MissingInfo: missing}
	case unavailable > 0 && noContext == 0:
		return callbacks.AnswerabilityTraceData{Status: "unanswerable", Reason: "knowledge retrieval unavailable", MissingInfo: missing}
	default:
		return callbacks.AnswerabilityTraceData{Status: "no_context", Reason: "no supporting evidence after quality gate", MissingInfo: missing}
	}
}

func runtimePlansHaveAuthoritativeSources(plans []callbacks.ReplyTaskPlanTraceData) bool {
	if len(plans) == 0 {
		return false
	}
	for _, plan := range plans {
		if !runtimeTaskPlanHasAuthoritativeSource(plan) {
			return false
		}
	}
	return true
}

type runtimeEvidenceArtifacts struct {
	Legacy              *contracts.EvidenceBundleV1
	Quality             *contracts.EvidenceBundleV2
	ResourceEligibility contracts.ResourceEligibilityV1
	ByTask              map[string]AnswerabilityOutcome
	TaskActionCodes     map[string]string
}

func promoteConditionalProbeCheckpoint(req RunInput, taskState runtimeTaskBatchState, item runtimeTaskKnowledgeItem, probe *retrievers.KnowledgeRetrieveResult) (*retrievers.KnowledgeRetrieveResult, error) {
	if probe == nil || probe.RetrieveLogID <= 0 || !taskState.Enabled || taskState.TurnID <= 0 || taskState.TurnVersion <= 0 {
		return probe, nil
	}
	taskID := taskState.TaskIDByTaskKey[item.TaskKey]
	if taskID <= 0 {
		return nil, fmt.Errorf("conditional knowledge probe lacks persisted task identity")
	}
	plan := BuildKnowledgeQueryPlan(
		req.Conversation.TenantID,
		req.Conversation.StoreID,
		req.Conversation.ID,
		req.UserMessage.SessionNo,
		item.Query,
		"answer",
		taskState.TurnID,
		taskState.TurnVersion,
		taskID,
		item.TaskKey,
	)
	if cached := findTerminalCheckpoint(sqls.DB(), plan, probe.Options); cached != nil {
		_ = sqls.DB().Model(&models.KnowledgeRetrieveLog{}).
			Where("id = ? AND tenant_id = ? AND task_id = 0", probe.RetrieveLogID, req.Conversation.TenantID).
			Updates(map[string]any{"execution_status": "superseded", "completed_at": time.Now()}).Error
		return cached, nil
	}
	checkpointKey := plan.CheckpointKey
	result := sqls.DB().Model(&models.KnowledgeRetrieveLog{}).
		Where("id = ? AND tenant_id = ? AND turn_id = ? AND task_id = 0 AND query_fingerprint = ? AND execution_status IN ?",
			probe.RetrieveLogID,
			req.Conversation.TenantID,
			taskState.TurnID,
			plan.QueryFingerprint,
			[]string{"succeeded", "no_hit"},
		).
		Updates(map[string]any{
			"turn_version":      taskState.TurnVersion,
			"task_id":           taskID,
			"task_key":          item.TaskKey,
			"query_key":         plan.QueryKey,
			"query_purpose":     "answer",
			"scope_fingerprint": plan.ScopeFingerprint,
			"checkpoint_key":    &checkpointKey,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, fmt.Errorf("conditional knowledge probe checkpoint could not be promoted")
	}
	probe.Options.TurnID = taskState.TurnID
	probe.Options.TurnVersion = taskState.TurnVersion
	probe.Options.TaskID = taskID
	probe.Options.TaskKey = item.TaskKey
	probe.Options.QueryKey = plan.QueryKey
	probe.Options.QueryPurpose = "answer"
	probe.Options.ScopeFingerprint = plan.ScopeFingerprint
	return probe, nil
}

func buildRuntimeEvidenceBundle(req RunInput, items []runtimeTaskKnowledgeItem, merged *retrievers.KnowledgeRetrieveResult) (*contracts.EvidenceBundleV1, map[string]AnswerabilityOutcome, map[string]string) {
	artifacts := buildRuntimeEvidenceArtifacts(req, items, merged)
	return artifacts.Legacy, artifacts.ByTask, artifacts.TaskActionCodes
}

func buildRuntimeEvidenceArtifacts(req RunInput, items []runtimeTaskKnowledgeItem, merged *retrievers.KnowledgeRetrieveResult) runtimeEvidenceArtifacts {
	byTask := make(map[string]AnswerabilityOutcome, len(items))
	taskActionCodes := make(map[string]string, len(items))
	scopeFingerprint := runtimeEvidenceScopeFingerprint(req, items)
	bundle := &contracts.EvidenceBundleV1{
		SchemaVersion:    contracts.EvidenceBundleV1SchemaVersion,
		ScopeFingerprint: scopeFingerprint,
		RetrievalStatus:  "not_needed", Items: []contracts.EvidenceItemV1{}, Resources: []contracts.EvidenceResourceV1{},
	}
	quality := &contracts.EvidenceBundleV2{
		SchemaVersion: contracts.EvidenceBundleV2SchemaVersion, ScopeFingerprint: scopeFingerprint,
		RetrievalStatus: "not_needed", Items: []contracts.EvidenceItemV2{}, Resources: []contracts.EvidenceResourceV2{},
	}
	eligibility := contracts.ResourceEligibilityV1{SchemaVersion: contracts.ResourceEligibilityV1SchemaVersion, Items: []contracts.ResourceEligibilityItemV1{}}
	metadataBySource := loadRuntimeKnowledgeMetadata(req, items)
	indexes := make(map[string]runtimeEvidenceIndex)
	sourceEntries := make(map[string][]runtimeEvidenceIndex)
	itemByTask := make(map[string]runtimeTaskKnowledgeItem, len(items))
	storeFactSeq := 0
	addressTaskKeys := make([]string, 0, len(items))
	for _, item := range items {
		itemByTask[item.TaskKey] = item
		// ProtectedFact Phase1（文档 9.3/18.2）：地址类任务的 store.address 是权威事实，
		// 从 hydrate 后实例确定性取值注入 S* 证据。Generate 只能用它声明酒店地址，
		// FactSourceBoundaryValidator 拒绝任何与它不一致的地址断言（如客户 OCR 里的壹间公寓）。
		if runtimeTaskRequiresStoreAddress(item) {
			addressTaskKeys = append(addressTaskKeys, item.TaskKey)
		}
	}
	storeAddress := ""
	knowledgeEvidenceLimit := runtimeEvidenceMaxItems
	if len(addressTaskKeys) > 0 {
		storeAddress = authoritativeStoreAddress(req)
		if storeAddress != "" {
			// evidence_bundle.v2 最多 24 项。地址任务必须为权威 store.address
			// 预留一个 S* 槽，不能先塞满 24 条 FastGPT Evidence 再越界追加。
			knowledgeEvidenceLimit--
		}
	}
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
		supportingResults := make([]rag.RetrieveResult, 0, len(results))
		droppedMeta := 0
		droppedQuality := 0
		for _, result := range results {
			if len(quality.Items) >= knowledgeEvidenceLimit || strings.TrimSpace(result.Content) == "" {
				break
			}
			sourceKey := runtimeEvidenceSourceKey(result.KnowledgeBaseID, result.SourceRecordID)
			metadata := metadataBySource[sourceKey]
			var metadataPtr *models.KnowledgeEvidenceMetadata
			if metadata.ID > 0 {
				metadataCopy := metadata
				metadataPtr = &metadataCopy
			}
			judgement := knowledgepolicy.Judge(knowledgepolicy.EvidenceJudgeInput{
				TenantID: req.Conversation.TenantID, StoreID: req.Conversation.StoreID,
				Task:      knowledgepolicy.Task{TaskKey: item.TaskKey, Intent: item.Intent, SubIntent: item.SubIntent, Query: item.Query, RequestMode: item.RequestMode},
				Candidate: result, Metadata: metadataPtr,
			})
			if !gateEnabled(gateEvidenceQuality, req) {
				judgement.Answerability = "supporting"
				judgement.AllowedUses = []string{"answer_text", "prepare_resource"}
				judgement.BlockedReasons = []string{}
			}
			key := runtimeEvidenceResultKey(result) + "|" + runtimeEvidenceJudgementKey(judgement)
			entry, exists := indexes[key]
			if !exists {
				ref := fmt.Sprintf("K%d", len(quality.Items)+1)
				quality.Items = append(quality.Items, contracts.EvidenceItemV2{
					Ref: ref, SourceType: "fastgpt", SourceClass: judgement.SourceClass,
					SourceRecordID: strings.TrimSpace(result.SourceRecordID), TaskKeys: []string{item.TaskKey},
					Title: boundedEvidenceText(firstNonEmpty(result.Title, result.DocumentTitle), 200), Content: boundedEvidenceText(result.Content, 4000),
					Score: clampEvidenceScore(float64(result.Score)), FactScope: judgement.FactScope, ClaimType: judgement.ClaimType,
					TrustLevel: judgement.TrustLevel, Freshness: judgement.Freshness, TopicLabels: limitEvidenceStrings(knowledgepolicy.SortedUnique(judgement.TopicLabels), 8),
					TopicMatch: judgement.TopicMatch, Answerability: judgement.Answerability,
					AllowedUses: nonNilStrings(judgement.AllowedUses), BlockedReasons: nonNilStrings(judgement.BlockedReasons), ResourceRefs: []string{},
				})
				entry = runtimeEvidenceIndex{legacyIndex: -1, qualityIndex: len(quality.Items) - 1, ref: ref, sourceKey: sourceKey, supporting: judgement.Answerability == "supporting"}
				if entry.supporting {
					bundle.Items = append(bundle.Items, contracts.EvidenceItemV1{
						Ref: ref, SourceType: "fastgpt", TaskKeys: []string{item.TaskKey},
						Title: quality.Items[entry.qualityIndex].Title, Content: quality.Items[entry.qualityIndex].Content,
						Score: quality.Items[entry.qualityIndex].Score, Answerability: "supporting", ResourceRefs: []string{},
					})
					entry.legacyIndex = len(bundle.Items) - 1
				}
				indexes[key] = entry
				sourceEntries[sourceKey] = append(sourceEntries[sourceKey], entry)
			} else {
				quality.Items[entry.qualityIndex].TaskKeys = appendUniqueStrings(quality.Items[entry.qualityIndex].TaskKeys, item.TaskKey)
				if entry.supporting && entry.legacyIndex >= 0 {
					bundle.Items[entry.legacyIndex].TaskKeys = appendUniqueStrings(bundle.Items[entry.legacyIndex].TaskKeys, item.TaskKey)
				}
			}
			if entry.supporting {
				outcome.SupportingRefs = appendUniqueStrings(outcome.SupportingRefs, entry.ref)
				supportingResults = append(supportingResults, result)
			} else {
				droppedQuality++
				if judgement.ClaimType == "meta" || stringInSlice("meta_content", judgement.BlockedReasons) {
					droppedMeta++
				}
			}
		}
		if len(outcome.SupportingRefs) == 0 && outcome.Status == "has_context" {
			outcome.Status = "no_context"
			switch {
			case droppedMeta > 0 && droppedMeta == droppedQuality:
				outcome.ReasonCode = "knowledge_meta_content"
			case droppedQuality > 0:
				outcome.ReasonCode = "knowledge_quality_blocked"
			default:
				outcome.ReasonCode = "knowledge_no_context"
			}
		}
		// 只有「排名第一」的检索结果（实际采用的答案）才允许触发"转接"判定；
		// 后续候选只是噪声，不能因为它们正文里出现"转接"就误判转人工。
		if outcome.Status == "has_context" && strings.TrimSpace(taskActionCodes[item.TaskKey]) == "" && len(supportingResults) > 0 {
			top := supportingResults[0]
			if actionCode := services.KnowledgeActionBindingService.ActionCodeForHit(
				req.Conversation.TenantID, req.Conversation.StoreID, top.KnowledgeBaseID, top.SourceRecordID,
			); actionCode != "" {
				taskActionCodes[item.TaskKey] = actionCode
			} else if knowledgeContentRequiresHandoff(top.Content) {
				taskActionCodes[item.TaskKey] = "human_handoff"
			}
		}
		byTask[item.TaskKey] = outcome
	}

	// ProtectedFact Phase1：地址类任务注入 store.address 权威 S* 证据（文档 9.3）。
	// 有值时地址任务至少 has_context 且 S ref 进入 SupportingRefs；
	// 未配置时不伪造，地址断言由边界校验兜底（禁止从 OCR/历史猜地址）。
	if len(addressTaskKeys) > 0 {
		if address := storeAddress; address != "" {
			storeFactSeq++
			ref := fmt.Sprintf("S%d", storeFactSeq)
			bundle.Items = append(bundle.Items, contracts.EvidenceItemV1{
				Ref: ref, SourceType: "store_fact", TaskKeys: append([]string(nil), addressTaskKeys...),
				Title: "当前门店地址（系统权威）", Content: address, Score: 1,
				Answerability: "supporting", ResourceRefs: []string{},
			})
			quality.Items = append(quality.Items, contracts.EvidenceItemV2{
				Ref: ref, SourceType: "store_fact", SourceClass: "authoritative_store_fact", FactKey: "store.address",
				TaskKeys: append([]string(nil), addressTaskKeys...), Title: "当前门店地址（系统权威）", Content: address, Score: 1,
				FactScope: "store", ClaimType: "fact", TrustLevel: "authoritative", Freshness: "current",
				TopicLabels: []string{"store.address"}, TopicMatch: "exact", Answerability: "supporting",
				AllowedUses: []string{"answer_text"}, BlockedReasons: []string{}, ResourceRefs: []string{},
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

	if merged != nil {
		for _, resource := range resolveRuntimeKnowledgeResources(req, merged) {
			if len(bundle.Resources) >= 16 || strings.TrimSpace(resource.AssetID) == "" {
				break
			}
			sourceKey := runtimeEvidenceSourceKey(resource.KnowledgeBaseID, resource.SourceRecordID)
			taskKeys := make([]string, 0, 4)
			entries := sourceEntries[sourceKey]
			for _, entry := range entries {
				if !entry.supporting || entry.qualityIndex < 0 || entry.qualityIndex >= len(quality.Items) {
					continue
				}
				taskKeys = appendUniqueStrings(taskKeys, quality.Items[entry.qualityIndex].TaskKeys...)
			}
			if len(taskKeys) == 0 {
				continue
			}
			ref := fmt.Sprintf("R%d", len(bundle.Resources)+1)
			assetID := strings.TrimSpace(resource.AssetID)
			bundle.Resources = append(bundle.Resources, contracts.EvidenceResourceV1{
				Ref: ref, Type: "image", AssetID: &assetID,
				Title: boundedEvidenceText(firstNonEmpty(resource.Title, resource.Description), 200), TaskKeys: taskKeys,
			})
			quality.Resources = append(quality.Resources, contracts.EvidenceResourceV2{
				Ref: ref, Type: "image", AssetID: &assetID,
				Title: boundedEvidenceText(firstNonEmpty(resource.Title, resource.Description), 200), TaskKeys: append([]string(nil), taskKeys...),
			})
			for _, entry := range entries {
				if !entry.supporting {
					continue
				}
				if entry.qualityIndex >= 0 && entry.qualityIndex < len(quality.Items) {
					quality.Items[entry.qualityIndex].ResourceRefs = appendUniqueStrings(quality.Items[entry.qualityIndex].ResourceRefs, ref)
				}
				if entry.legacyIndex >= 0 && entry.legacyIndex < len(bundle.Items) {
					bundle.Items[entry.legacyIndex].ResourceRefs = appendUniqueStrings(bundle.Items[entry.legacyIndex].ResourceRefs, ref)
				}
			}
			metadata := metadataBySource[sourceKey]
			for _, taskKey := range taskKeys {
				entry := firstSupportingEvidenceEntryForTask(entries, quality.Items, taskKey)
				if entry == nil {
					continue
				}
				eligibility.Items = append(eligibility.Items, buildKnowledgeImageEligibility(itemByTask[taskKey], ref, *entry, metadata))
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
		quality.RetrievalStatus = "has_context"
	case statusCounts["unavailable"] > 0:
		bundle.RetrievalStatus = "unavailable"
		quality.RetrievalStatus = "unavailable"
	case statusCounts["unanswerable"] > 0:
		bundle.RetrievalStatus = "unanswerable"
		quality.RetrievalStatus = "unanswerable"
	case len(byTask) > 0:
		bundle.RetrievalStatus = "no_context"
		quality.RetrievalStatus = "no_context"
	}
	return runtimeEvidenceArtifacts{Legacy: bundle, Quality: quality, ResourceEligibility: eligibility, ByTask: byTask, TaskActionCodes: taskActionCodes}
}

// runtimeTaskRequiresStoreAddress is the protected-fact selector for the
// address category. It primarily trusts the structured sub-intent, then uses a
// narrow domain classifier over the verified source quote so an unusual but
// valid address request still receives the authoritative store.address fact.
// It never selects an address from history, OCR, retrieval, or model output.
func runtimeTaskRequiresStoreAddress(item runtimeTaskKnowledgeItem) bool {
	if isAddressTextSubIntent(item.SubIntent) {
		return true
	}
	query := compactDialogueText(stripKnowledgeQueryTransportWrapper(item.Query))
	if query == "" {
		return false
	}
	for _, marker := range []string{"酒店地址", "门店地址", "外卖地址", "收货地址", "导航地址", "酒店定位", "门店定位"} {
		if strings.Contains(query, marker) {
			return true
		}
	}
	return strings.Contains(query, "外卖") &&
		(strings.Contains(query, "填哪") || strings.Contains(query, "填哪里") || strings.Contains(query, "送到哪"))
}

func loadRuntimeKnowledgeMetadata(req RunInput, items []runtimeTaskKnowledgeItem) map[string]models.KnowledgeEvidenceMetadata {
	ret := make(map[string]models.KnowledgeEvidenceMetadata)
	if req.Conversation.TenantID <= 0 || req.Conversation.StoreID <= 0 || sqls.DB() == nil {
		return ret
	}
	byKnowledgeBase := make(map[int64][]string)
	for _, item := range items {
		if item.Result == nil {
			continue
		}
		results := append([]rag.RetrieveResult(nil), item.Result.ContextResults...)
		results = append(results, item.Result.Hits...)
		for _, result := range results {
			if result.KnowledgeBaseID <= 0 || strings.TrimSpace(result.SourceRecordID) == "" {
				continue
			}
			byKnowledgeBase[result.KnowledgeBaseID] = appendUniqueStrings(byKnowledgeBase[result.KnowledgeBaseID], result.SourceRecordID)
		}
	}
	for knowledgeBaseID, sourceRecordIDs := range byKnowledgeBase {
		metadata := services.KnowledgeEvidenceMetadataService.JudgeBySourceRecords(
			req.Conversation.TenantID, req.Conversation.StoreID, knowledgeBaseID, sourceRecordIDs,
		)
		for sourceRecordID, item := range metadata {
			ret[runtimeEvidenceSourceKey(knowledgeBaseID, sourceRecordID)] = item
		}
	}
	return ret
}

func runtimeEvidenceJudgementKey(result knowledgepolicy.EvidenceJudgeResult) string {
	return strings.Join([]string{
		result.SourceClass, result.FactScope, result.ClaimType, result.TrustLevel,
		result.Freshness, result.TopicMatch, result.Answerability,
		strings.Join(result.AllowedUses, ","), strings.Join(result.BlockedReasons, ","),
	}, "|")
}

func firstSupportingEvidenceEntryForTask(entries []runtimeEvidenceIndex, items []contracts.EvidenceItemV2, taskKey string) *contracts.EvidenceItemV2 {
	for _, entry := range entries {
		if !entry.supporting || entry.qualityIndex < 0 || entry.qualityIndex >= len(items) {
			continue
		}
		if stringInSlice(taskKey, items[entry.qualityIndex].TaskKeys) {
			return &items[entry.qualityIndex]
		}
	}
	return nil
}

func buildKnowledgeImageEligibility(task runtimeTaskKnowledgeItem, resourceRef string, source contracts.EvidenceItemV2, metadata models.KnowledgeEvidenceMetadata) contracts.ResourceEligibilityItemV1 {
	purpose := strings.TrimSpace(metadata.ResourcePurpose)
	if purpose == "" {
		purpose = "unknown"
	}
	explicit := explicitKnowledgeImageRequest(task.Query)
	requestMatch := "not_requested"
	if explicit {
		requestMatch = "explicit"
	} else if metadata.AutoAttachResource {
		requestMatch = "implicit_allowed"
	}
	item := contracts.ResourceEligibilityItemV1{
		ResourceRef: resourceRef, TaskKey: task.TaskKey, SourceEvidenceRef: source.Ref,
		SourceRecordID: source.SourceRecordID, ResourceType: "image", ResourcePurpose: purpose,
		TopicMatch: source.TopicMatch, RequestMatch: requestMatch, AutoAttach: metadata.AutoAttachResource,
		Decision: "blocked", ReasonCode: "resource_not_requested",
	}
	switch {
	case source.Answerability != "supporting":
		item.ReasonCode = "source_evidence_not_supporting"
	case source.TopicMatch != "exact":
		item.ReasonCode = "resource_topic_mismatch"
	case purpose == "unknown":
		item.ReasonCode = "resource_metadata_missing"
	case explicit:
		item.Decision = "eligible"
		item.ReasonCode = "eligible_explicit_request"
	case metadata.AutoAttachResource:
		item.Decision = "eligible"
		item.ReasonCode = "eligible_auto_attach"
	}
	return item
}

func explicitKnowledgeImageRequest(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	mediaNoun := strings.Contains(text, "图片") || strings.Contains(text, "照片") || strings.Contains(text, "相片") || strings.Contains(text, "图给") || text == "图"
	requestVerb := strings.Contains(text, "发") || strings.Contains(text, "给我") || strings.Contains(text, "看看") || strings.Contains(text, "看下") || strings.Contains(text, "看一下")
	return mediaNoun && requestVerb
}

func limitEvidenceStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return append([]string(nil), values[:limit]...)
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
	// 契约 4.11/22.12：知识 Query 只使用规范化业务文本；[语音]/[图片]/文件名
	// 等运输包装与 merge 前缀禁止进入检索文本。
	if text := strings.TrimSpace(stripKnowledgeQueryTransportWrapper(currentTurnDisplayText(plan.Text))); text != "" {
		return text
	}
	if subIntent := strings.TrimSpace(plan.SubIntent); subIntent != "" {
		return subIntent
	}
	return strings.TrimSpace(plan.Intent)
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
	parts := splitBySeparators([]string{text}, []string{"还有啊", "还有", "以及", "另外", "再加上", "顺便"})
	parts = splitBySeparators(parts, []string{"？", "?", "。", "！", "!", "；", ";"})
	return parts
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
