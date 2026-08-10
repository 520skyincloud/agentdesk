package executor

import (
	"context"
	"strings"
	"sync"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/pkg/enums"
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
}

type runtimeTaskKnowledgeItem struct {
	TaskKey string
	Query   string
	Result  *retrievers.KnowledgeRetrieveResult
	Status  enums.AIReplyTurnTaskKnowledgeStatus
	Err     error
}

type runtimeTaskKnowledgeRetriever interface {
	KnowledgeBaseIDs() []int64
	RetrieveContextByOptions(context.Context, retrievers.KnowledgeRetrieveOptions, string) (*retrievers.KnowledgeRetrieveResult, error)
}

func retrieveRuntimeTaskKnowledge(ctx context.Context, req RunInput, plans []callbacks.ReplyTaskPlanTraceData, probe *retrievers.KnowledgeRetrieveResult, taskState runtimeTaskBatchState) (runtimeTaskKnowledgeOutcome, error) {
	return retrieveRuntimeTaskKnowledgeWithRetriever(ctx, req, plans, probe, taskState, retrievers.NewKnowledgeRetriever(req.AIAgent))
}

func retrieveRuntimeTaskKnowledgeWithRetriever(ctx context.Context, req RunInput, plans []callbacks.ReplyTaskPlanTraceData, probe *retrievers.KnowledgeRetrieveResult, taskState runtimeTaskBatchState, retriever runtimeTaskKnowledgeRetriever) (runtimeTaskKnowledgeOutcome, error) {
	outcome := runtimeTaskKnowledgeOutcome{ActiveTaskPlans: append([]callbacks.ReplyTaskPlanTraceData(nil), plans...)}
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
		items[index] = runtimeTaskKnowledgeItem{TaskKey: plan.TaskKey, Query: runtimeTaskKnowledgeQuery(plan)}
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
	outcome.Prefetched = mergeRuntimeTaskKnowledge(items, retriever.KnowledgeBaseIDs())
	return outcome, nil
}

func runtimeTaskKnowledgeQuery(plan callbacks.ReplyTaskPlanTraceData) string {
	if text := strings.TrimSpace(currentTurnDisplayText(plan.Text)); text != "" {
		return text
	}
	if subIntent := strings.TrimSpace(plan.SubIntent); subIntent != "" {
		return subIntent
	}
	return strings.TrimSpace(plan.Intent)
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
