package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/cloudwego/eino/schema"
)

type fakeKnowledgeContextRetriever struct {
	mu               sync.Mutex
	knowledgeBaseIDs []int64
	result           *retrievers.KnowledgeRetrieveResult
	resultsByQuery   map[string]*retrievers.KnowledgeRetrieveResult
	errorsByQuery    map[string]error
	err              error
	called           bool
	queries          []string
}

func (r *fakeKnowledgeContextRetriever) KnowledgeBaseIDs() []int64 {
	return append([]int64(nil), r.knowledgeBaseIDs...)
}

func (r *fakeKnowledgeContextRetriever) RetrieveContextByOptions(ctx context.Context, opts retrievers.KnowledgeRetrieveOptions, query string) (*retrievers.KnowledgeRetrieveResult, error) {
	r.mu.Lock()
	r.called = true
	r.queries = append(r.queries, query)
	r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	if err := r.errorsByQuery[query]; err != nil {
		return nil, err
	}
	if r.resultsByQuery != nil {
		if result, ok := r.resultsByQuery[query]; ok {
			return result, nil
		}
	}
	if r.result != nil {
		return r.result, nil
	}
	return &retrievers.KnowledgeRetrieveResult{
		KnowledgeBaseIDs: append([]int64(nil), r.knowledgeBaseIDs...),
		Query:            query,
	}, nil
}

func TestKnowledgePolicyRetrievesEachBurstQuestion(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		resultsByQuery: map[string]*retrievers.KnowledgeRetrieveResult{
			"能开专票不": {
				KnowledgeBaseIDs: []int64{1},
				RawHits: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 1001, Title: "发票原始候选", Content: "发票原始候选", Score: 0.99},
				},
				Hits: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 101, Title: "发票", Content: "可以开电子专票，退房后在小程序申请。", Score: 0.95},
				},
				ContextResults: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 101, Title: "发票", Content: "可以开电子专票，退房后在小程序申请。", Score: 0.95},
				},
				ContextText: "可以开电子专票，退房后在小程序申请。",
				AnswerMode:  enums.KnowledgeAnswerModeStrict,
			},
			"WiFi是哪个": {
				KnowledgeBaseIDs: []int64{1},
				RawHits: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 1002, Title: "WiFi原始候选", Content: "WiFi原始候选", Score: 0.98},
				},
				Hits: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 102, Title: "WiFi", Content: "WiFi 名称是 LISI，密码看房间桌牌。", Score: 0.93},
				},
				ContextResults: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 102, Title: "WiFi", Content: "WiFi 名称是 LISI，密码看房间桌牌。", Score: 0.93},
				},
				ContextText: "WiFi 名称是 LISI，密码看房间桌牌。",
				AnswerMode:  enums.KnowledgeAnswerModeStrict,
			},
		},
	}
	gate := newTestKnowledgePolicyGate(retriever)
	intent := hotelInfoIntent()
	intent.IntentTasks = []callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", Text: "能开专票不", ResolvedText: "能开专票不", NeedsKnowledge: true},
		{Intent: "hotel_info", Text: "WiFi是哪个", ResolvedText: "WiFi是哪个", NeedsKnowledge: true},
	}
	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("客人刚才连续发了几条消息，请一起理解，不要只回复最后一句：\n能开专票不\nWiFi是哪个", "1"),
		Summary: &RunResult{},
		Intent:  intent,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !stringSliceSetEqual(retriever.queries, []string{"能开专票不", "WiFi是哪个"}) {
		t.Fatalf("expected retrieval for each current burst question, got %#v", retriever.queries)
	}
	if state.RetrieveResult == nil {
		t.Fatalf("expected retrieval result, got nil")
	}
	if len(state.RetrieveResult.RawHits) != 2 {
		t.Fatalf("expected merged raw hits for both burst questions, got %#v", state.RetrieveResult.RawHits)
	}
	if !strings.Contains(state.RetrieveResult.ContextText, "能开专票不") || !strings.Contains(state.RetrieveResult.ContextText, "WiFi是哪个") {
		t.Fatalf("expected merged context to label each question, got %q", state.RetrieveResult.ContextText)
	}
}

func TestKnowledgePolicyIsolatesPerTaskRetrievalFailure(t *testing.T) {
	breakfastHit := rag.RetrieveResult{
		KnowledgeBaseID: 1,
		ChunkID:         101,
		Title:           "早餐",
		Content:         "问题：早餐几点\n答案：早餐时间是 7:00-9:30。",
		Score:           0.95,
	}
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		resultsByQuery: map[string]*retrievers.KnowledgeRetrieveResult{
			"早餐几点": {
				KnowledgeBaseIDs: []int64{1},
				RawHits:          []rag.RetrieveResult{breakfastHit},
				Hits:             []rag.RetrieveResult{breakfastHit},
				ContextResults:   []rag.RetrieveResult{breakfastHit},
				ContextText:      breakfastHit.Content,
			},
		},
		errorsByQuery: map[string]error{
			"老板是谁": errors.New("owner knowledge shard unavailable"),
		},
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", Text: "早餐几点", ResolvedText: "早餐几点", NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
		{TaskID: "task-2", Intent: "hotel_info", Text: "老板是谁", ResolvedText: "老板是谁", NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
	}})
	intent := hotelInfoIntent()
	intent.IntentTasks = []callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", Text: "早餐几点", ResolvedText: "早餐几点", NeedsKnowledge: true},
		{Intent: "hotel_info", Text: "老板是谁", ResolvedText: "老板是谁", NeedsKnowledge: true},
	}
	summary := &RunResult{}

	state, err := newTestKnowledgePolicyGate(retriever).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点，老板是谁", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    intent,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.AnswerabilityStatus != answerabilityStatusHasContext || state.RetrieveResult == nil ||
		!strings.Contains(state.RetrieveResult.ContextText, "7:00-9:30") {
		t.Fatalf("successful sibling evidence must continue through Generate, state=%#v result=%#v", state, state.RetrieveResult)
	}
	if strings.Contains(state.RetrieveResult.ContextText, "老板") {
		t.Fatalf("failed Task must not expose invented or sibling evidence: %q", state.RetrieveResult.ContextText)
	}
	if summary.handoffDirective {
		t.Fatalf("one failed Task must not turn the entire turn into a global handoff: %#v", summary)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if len(trace.Tasks) != 2 || trace.Tasks[0].TaskID != "task-1" || trace.Tasks[1].TaskID != "task-2" {
		t.Fatalf("Judge trace must retain both Task outcomes in source order: %#v", trace.Tasks)
	}
	failed := trace.Tasks[1]
	if failed.Decision != knowledgeEvidenceDecisionInsufficient || failed.DecisionSource != "source_unavailable" ||
		failed.Disposition != runtimeKnowledgeDispositionNoEvidenceHandoff {
		t.Fatalf("failed retrieval must be isolated as source_unavailable: %#v", failed)
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) != 2 || plan.TaskPlans[0].TaskID != "task-1" || plan.TaskPlans[0].OutputKind != "text" ||
		plan.TaskPlans[1].TaskID != "task-2" || plan.TaskPlans[1].OutputKind != "handoff" || plan.TaskPlans[1].ReplyRequired {
		t.Fatalf("successful and failed Tasks must keep independent execution paths: %#v", plan.TaskPlans)
	}
}

func TestKnowledgePolicyPersistsExplicitNoEvidenceForZeroCandidateSibling(t *testing.T) {
	breakfastHit := rag.RetrieveResult{
		KnowledgeBaseID: 1,
		ChunkID:         101,
		Title:           "早餐",
		Content:         "问题：早餐几点\n答案：早餐时间是 7:00-9:30。",
		Score:           0.95,
	}
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		resultsByQuery: map[string]*retrievers.KnowledgeRetrieveResult{
			"早餐几点": {
				KnowledgeBaseIDs: []int64{1},
				RawHits:          []rag.RetrieveResult{breakfastHit},
				Hits:             []rag.RetrieveResult{breakfastHit},
				ContextResults:   []rag.RetrieveResult{breakfastHit},
				ContextText:      breakfastHit.Content,
			},
			"老板是谁": {
				KnowledgeBaseIDs: []int64{1},
				Query:            "老板是谁",
			},
		},
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", Text: "早餐几点", ResolvedText: "早餐几点", NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
		{TaskID: "task-2", Intent: "hotel_info", Text: "老板是谁", ResolvedText: "老板是谁", NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
	}})
	intent := hotelInfoIntent()
	intent.IntentTasks = []callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", Text: "早餐几点", ResolvedText: "早餐几点", NeedsKnowledge: true},
		{Intent: "hotel_info", Text: "老板是谁", ResolvedText: "老板是谁", NeedsKnowledge: true},
	}
	summary := &RunResult{}

	state, err := newTestKnowledgePolicyGate(retriever).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点，老板是谁", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    intent,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.AnswerabilityStatus != answerabilityStatusHasContext || state.RetrieveResult == nil ||
		!strings.Contains(state.RetrieveResult.ContextText, "7:00-9:30") {
		t.Fatalf("answerable sibling must continue through Generate, state=%#v result=%#v", state, state.RetrieveResult)
	}
	if summary.handoffDirective {
		t.Fatalf("one zero-candidate Task must not turn the whole turn into a global handoff: %#v", summary)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if len(trace.Tasks) != 2 || trace.Tasks[0].TaskID != "task-1" || trace.Tasks[1].TaskID != "task-2" {
		t.Fatalf("Judge trace must retain answered and zero-candidate Tasks in source order: %#v", trace.Tasks)
	}
	missing := trace.Tasks[1]
	if missing.CandidateCount != 0 || missing.Decision != knowledgeEvidenceDecisionInsufficient ||
		missing.DecisionSource != "retriever_no_evidence" || missing.Disposition != runtimeKnowledgeDispositionNoEvidenceHandoff {
		t.Fatalf("zero-candidate retrieval must persist an explicit no-evidence disposition: %#v", missing)
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) != 2 || plan.TaskPlans[0].OutputKind != "text" ||
		plan.TaskPlans[1].OutputKind != "handoff" || plan.TaskPlans[1].ReplyRequired {
		t.Fatalf("answered and zero-candidate Tasks must keep independent execution paths: %#v", plan.TaskPlans)
	}
}

func TestKnowledgePolicyKeepsExternalProxyNoEvidenceInGenerateWithoutHandoff(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "service_request", SubIntent: "external_proxy_action", Objective: "action_request",
		Text: "帮我点个外卖", OriginalText: "帮我点个外卖", ResolvedText: "帮我点个外卖",
		NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply",
	}}})
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "service_request", SubIntent: "external_proxy_action", NeedsKnowledge: true, ShouldReply: true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: "service_request", SubIntent: "external_proxy_action", Objective: "action_request",
			Text: "帮我点个外卖", ResolvedText: "帮我点个外卖", SourceRefs: []string{"U1"}, NeedsKnowledge: true,
		}},
	}
	summary := &RunResult{}

	state, err := newTestKnowledgePolicyGate(retriever).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("帮我点个外卖", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    intent,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if summary.handoffDirective {
		t.Fatalf("external proxy capability boundary must not request handoff: %#v", summary)
	}
	if state.AnswerabilityStatus != answerabilityStatusNoContext ||
		!messagesContainContent(state.Decision.Instructions, "当前没有选中可用的自助知识事实") {
		t.Fatalf("external proxy no-evidence path must continue to Generate with a boundary instruction: %#v", state)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if len(trace.Tasks) != 1 || trace.Tasks[0].Disposition != runtimeKnowledgeDispositionAnswer || trace.DeferredHandoff {
		t.Fatalf("external proxy no-evidence trace must be answerable without deferred handoff: %#v", trace)
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) != 1 {
		t.Fatalf("expected one preserved task, got %#v", plan.TaskPlans)
	}
	task := plan.TaskPlans[0]
	if task.Output != "text_reply" || task.OutputKind != "text" || !task.ReplyRequired || task.NeedsKnowledge ||
		task.SelectedLayer != "" || len(task.SupportedFacts) != 0 {
		t.Fatalf("external proxy no-evidence task must become a plain text capability reply: %#v", task)
	}
}

func TestKnowledgePolicyKeepsExternalProxySourceUnavailableOutOfHandoff(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	tests := []struct {
		name         string
		knowledgeIDs string
		retriever    knowledgeContextRetriever
	}{
		{name: "no configured knowledge", knowledgeIDs: ""},
		{name: "retriever unavailable", knowledgeIDs: "1"},
		{name: "retriever has no knowledge", knowledgeIDs: "1", retriever: &fakeKnowledgeContextRetriever{}},
		{name: "retrieval failed", knowledgeIDs: "1", retriever: &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}, err: errors.New("retrieve failed")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := callbacks.NewRuntimeTraceCollector()
			summary := &RunResult{}
			intent := callbacks.IntentTraceData{
				PrimaryIntent: "service_request", SubIntent: "external_proxy_action", NeedsKnowledge: true, ShouldReply: true,
				IntentTasks: []callbacks.IntentTaskTraceData{{
					Intent: "service_request", SubIntent: "external_proxy_action", Objective: "action_request",
					Text: "帮我点个外卖", ResolvedText: "帮我点个外卖", SourceRefs: []string{"U1"}, NeedsKnowledge: true,
				}},
			}
			gate := newTestKnowledgePolicyGate(tt.retriever)
			state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
				Request:   newKnowledgePolicyRunInput("帮我点个外卖", tt.knowledgeIDs),
				Summary:   summary,
				Collector: collector,
				Intent:    intent,
			})
			if err != nil {
				t.Fatalf("Evaluate returned error: %v", err)
			}
			if summary.handoffDirective {
				t.Fatalf("source-unavailable external proxy must not request handoff: %#v", summary)
			}
			if !messagesContainContent(state.Decision.Instructions, "外部代执行能力边界") {
				t.Fatalf("missing capability boundary instruction: %#v", state.Decision.Instructions)
			}
			plan := collector.Data.Pipeline.ReplyPlan
			if len(plan.TaskPlans) != 1 || plan.TaskPlans[0].Output != "text_reply" || plan.TaskPlans[0].NeedsKnowledge {
				t.Fatalf("external proxy must remain a plain text task: %#v", plan.TaskPlans)
			}
			for _, action := range collector.Data.ActionLedger.RequestedActions {
				if action.Action == "human_route" {
					t.Fatalf("external proxy source failure must not request human_route: %#v", collector.Data.ActionLedger)
				}
			}
		})
	}
}

func TestExternalProxyCapabilityBoundaryDoesNotOverrideOtherServiceRoutes(t *testing.T) {
	batch := &runtimeKnowledgeRetrieveBatch{Questions: []runtimeKnowledgeQuestionResult{
		{TaskID: "external", Intent: "service_request", SubIntent: "external_proxy_action", Objective: "action_request", Disposition: runtimeKnowledgeDispositionDirectHandoff},
		{TaskID: "internal", Intent: "service_request", SubIntent: "room_supplies", Objective: "action_request", Disposition: runtimeKnowledgeDispositionNoEvidenceHandoff},
	}}
	trace := callbacks.KnowledgeEvidenceJudgeTraceData{Tasks: []callbacks.KnowledgeEvidenceJudgeTaskTraceData{
		{TaskID: "external", Disposition: runtimeKnowledgeDispositionDirectHandoff},
		{TaskID: "internal", Disposition: runtimeKnowledgeDispositionNoEvidenceHandoff},
	}}
	if taskIDs := routeExternalProxyNoEvidenceAsCapabilityBoundary(batch, &trace); len(taskIDs) != 0 {
		t.Fatalf("direct handoff and internal service routes must remain unchanged: %#v", taskIDs)
	}
	if batch.Questions[0].Disposition != runtimeKnowledgeDispositionDirectHandoff ||
		batch.Questions[1].Disposition != runtimeKnowledgeDispositionNoEvidenceHandoff {
		t.Fatalf("unrelated dispositions changed: %#v", batch.Questions)
	}
}

func TestExternalProxyPartialKeepsSelectedEvidenceWithoutHandoff(t *testing.T) {
	hit := judgeTestHit(1, 101, "外卖下单", "问题：怎么点外卖？\n答案：可以自行在美团下单。", 0.9)
	result := &retrievers.KnowledgeRetrieveResult{RawHits: []rag.RetrieveResult{hit}}
	batch := &runtimeKnowledgeRetrieveBatch{
		Questions: []runtimeKnowledgeQuestionResult{{TaskID: "external", Result: result}},
		Merged:    &retrievers.KnowledgeRetrieveResult{},
	}
	task := knowledgeEvidenceJudgeTask{
		TaskID: "external", Intent: "service_request", Query: "帮我点个外卖", SubIntent: "external_proxy_action", Objective: "action_request",
		Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "externalC1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
	}
	outcome := knowledgeEvidenceJudgeOutcome{Applied: true, Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
		"external": {knowledgeEvidenceLayerStore: {
			Decision: knowledgeEvidenceDecisionPartial, DecisionSource: "model", SelectedCandidateIDs: []string{"externalC1"},
			SupportedFacts: []knowledgeEvidenceFact{{FactID: "externalF1", Aspect: "method", Statement: "可以自行在美团下单。"}},
			MissingAspects: []string{"酒店不能代执行外部下单"},
		}},
	}}

	trace := applyKnowledgeEvidenceJudgeOutcome(batch, []knowledgeEvidenceJudgeTask{task}, outcome)
	if batch.Questions[0].Disposition != runtimeKnowledgeDispositionAnswer || len(batch.Questions[0].MissingAspects) != 0 {
		t.Fatalf("external proxy partial evidence must answer without active missing aspects: %#v", batch.Questions[0])
	}
	if strings.Contains(result.ContextText, "尚未确认方面") || strings.Contains(result.ContextText, "酒店不能代执行外部下单") {
		t.Fatalf("external proxy capability boundary must not leak into the fact boundary: %q", result.ContextText)
	}
	if len(trace.Tasks) != 1 || trace.Tasks[0].Disposition != runtimeKnowledgeDispositionAnswer || len(trace.Tasks[0].MissingAspects) != 0 || len(trace.Tasks[0].SupportedFacts) != 1 {
		t.Fatalf("execution trace must keep facts and clear active handoff gaps: %#v", trace.Tasks)
	}
}

func TestKnowledgeEvidenceJudgeBudgetExhaustionIsProtocolRetryNotNoEvidence(t *testing.T) {
	batch := &runtimeKnowledgeRetrieveBatch{Questions: make([]runtimeKnowledgeQuestionResult, 0, knowledgeEvidenceJudgeBatchCandidateBudget+1)}
	for index := 0; index < knowledgeEvidenceJudgeBatchCandidateBudget+1; index++ {
		taskID := fmt.Sprintf("task-%d", index+1)
		query := fmt.Sprintf("问题%d", index+1)
		hit := rag.RetrieveResult{
			KnowledgeBaseID: 1,
			ChunkID:         int64(index + 1),
			Title:           query,
			Content:         "问题：" + query + "\n答案：这是对应答案。",
			Score:           0.9,
		}
		batch.Questions = append(batch.Questions, runtimeKnowledgeQuestionResult{
			TaskID: taskID,
			Intent: "hotel_info",
			Query:  query,
			Result: &retrievers.KnowledgeRetrieveResult{
				KnowledgeBaseIDs: []int64{1},
				RawHits:          []rag.RetrieveResult{hit},
				Hits:             []rag.RetrieveResult{hit},
				ContextResults:   []rag.RetrieveResult{hit},
				ContextText:      hit.Content,
			},
		})
	}

	judgeTasks := buildKnowledgeEvidenceJudgeTasks(batch, []int64{1}, []int64{1}, nil, "")
	if len(judgeTasks) != knowledgeEvidenceJudgeBatchCandidateBudget {
		t.Fatalf("candidate budget must leave one of 29 single-candidate Tasks unjudged, got %d", len(judgeTasks))
	}
	trace := callbacks.KnowledgeEvidenceJudgeTraceData{
		SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
		Status:        "completed",
		Tasks:         make([]callbacks.KnowledgeEvidenceJudgeTaskTraceData, 0, len(judgeTasks)),
	}
	for _, task := range judgeTasks {
		trace.Tasks = append(trace.Tasks, callbacks.KnowledgeEvidenceJudgeTaskTraceData{
			TaskID:         task.TaskID,
			CandidateCount: len(task.Candidates),
			Decision:       knowledgeEvidenceDecisionDirectSingle,
			DecisionSource: "model",
			Disposition:    runtimeKnowledgeDispositionAnswer,
		})
	}

	trace = appendRuntimeKnowledgeUnjudgedTaskTrace(trace, batch)
	if len(trace.Tasks) != knowledgeEvidenceJudgeBatchCandidateBudget+1 {
		t.Fatalf("Trace must retain all Tasks including the budget-exhausted Task, got %d", len(trace.Tasks))
	}
	if trace.Status != knowledgeEvidenceDecisionProtocolInvalid || !strings.Contains(trace.Reason, "task-29") {
		t.Fatalf("top-level Trace must expose the budget-exhausted protocol gap: %#v", trace)
	}
	unjudged := trace.Tasks[len(trace.Tasks)-1]
	if unjudged.TaskID != "task-29" || unjudged.CandidateCount != 1 ||
		unjudged.Decision != knowledgeEvidenceDecisionProtocolInvalid ||
		unjudged.DecisionSource != "unjudged_candidates" ||
		unjudged.Disposition != runtimeKnowledgeDispositionJudgeProtocolRetry {
		t.Fatalf("a Task with retrieved candidates but no Judge quota must retry the protocol, not hand off: %#v", unjudged)
	}
	question := batch.Questions[len(batch.Questions)-1]
	if question.Disposition != runtimeKnowledgeDispositionJudgeProtocolRetry || question.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("budget-exhausted Task execution state must remain retryable: %#v", question)
	}
}

func TestKnowledgeEvidenceJudgeNoEvidenceTaskDoesNotDegradeCompletedTrace(t *testing.T) {
	batch := &runtimeKnowledgeRetrieveBatch{Questions: []runtimeKnowledgeQuestionResult{{
		TaskID: "task-1",
		Query:  "完全没有召回的问题",
		Result: &retrievers.KnowledgeRetrieveResult{},
	}}}
	trace := appendRuntimeKnowledgeUnjudgedTaskTrace(callbacks.KnowledgeEvidenceJudgeTraceData{
		Status: "completed",
		Reason: "judged available candidates",
	}, batch)
	if trace.Status != "completed" || strings.Contains(trace.Reason, "candidate budget") {
		t.Fatalf("a real no-evidence Task is not a Judge budget failure: %#v", trace)
	}
	if len(trace.Tasks) != 1 || trace.Tasks[0].DecisionSource != "retriever_no_evidence" ||
		trace.Tasks[0].Disposition != runtimeKnowledgeDispositionNoEvidenceHandoff {
		t.Fatalf("the no-evidence Task must retain its explicit disposition: %#v", trace.Tasks)
	}
}

func TestRuntimeKnowledgeQuestionDispositionsDoNotInferHandoffFromEmptyHits(t *testing.T) {
	batch := &runtimeKnowledgeRetrieveBatch{Questions: []runtimeKnowledgeQuestionResult{{
		TaskID: "task-1",
		Query:  "老板是谁",
		Result: &retrievers.KnowledgeRetrieveResult{},
	}}}

	dispositions := runtimeKnowledgeQuestionDispositions(batch)
	if len(dispositions) != 1 || !dispositions[0].NeedsRetry || dispositions[0].NeedsHandoff ||
		dispositions[0].Disposition != runtimeKnowledgeDispositionJudgeProtocolRetry {
		t.Fatalf("an empty disposition is a protocol gap, not implicit proof of no knowledge: %#v", dispositions)
	}
}

func TestRuntimeKnowledgeRetrievalReturnsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := retrieveContextForRuntimeQuestionList(
		ctx,
		&fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}},
		retrievers.KnowledgeRetrieveOptions{},
		"早餐几点",
		[]runtimeKnowledgeQuestionSpec{{TaskID: "task-1", Query: "早餐几点"}},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled parent context must remain a batch-level error, got %v", err)
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetDoesNotPromoteSimilarHandoffFAQ(t *testing.T) {
	const query = "附近有什么好玩的"
	similarHandoff := rag.RetrieveResult{
		Score:   0.99,
		Content: "问题：附近有什么好玩的嘛\n答案：转接",
	}
	if !knowledgeEvidenceHandoffFAQMatchesQuery("附近有什么好玩的嘛", query) {
		t.Fatal("test setup must remain similar enough to exercise the former fuzzy handoff priority")
	}
	if _, _, exact := exactKnowledgeEvidenceFAQMatch(similarHandoff, query); exact {
		t.Fatal("test setup must not be a strict FAQ match")
	}
	task := knowledgeEvidenceJudgeTask{
		TaskID: "task-1",
		Query:  query,
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "task-1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.88, Content: "问题：周边游玩推荐\n答案：附近可以去罍街和合柴1972。"}},
			{CandidateID: "task-1C2", Layer: knowledgeEvidenceLayerStore, Hit: similarHandoff},
			{CandidateID: "task-1C3", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.86, Content: "问题：附近有什么好玩的\n答案：可以结合地图选择附近景点。"}},
		},
	}
	if index := bestExactKnowledgeEvidenceJudgeHandoffCandidateIndex(task, knowledgeEvidenceLayerStore); index != -1 {
		t.Fatalf("similar transfer FAQ must not receive exact-handoff priority, got candidate %d", index)
	}

	for _, quota := range []int{1, 2} {
		selected := selectKnowledgeEvidenceJudgeTaskCandidates(task, quota, false)
		if len(selected) != quota {
			t.Fatalf("quota %d returned %d candidates: %#v", quota, len(selected), selected)
		}
		if selected[0].CandidateID != "task-1C1" {
			t.Fatalf("quota %d must preserve the factual store candidate before a merely similar transfer FAQ: %#v", quota, selected)
		}
		for _, candidate := range selected {
			if candidate.CandidateID == "task-1C2" {
				t.Fatalf("quota %d must not spend scarce budget on a non-exact transfer FAQ: %#v", quota, selected)
			}
		}
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetKeepsExactHandoffAndExactFactualPeer(t *testing.T) {
	tests := []struct {
		name           string
		query          string
		factualContent string
	}{
		{
			name:           "owner identity alias",
			query:          "老板是谁",
			factualContent: "问题：董事长是谁\n答案：董事长是汤东强。\n相似问法：老板是谁",
		},
		{
			name:           "nearby attraction alias",
			query:          "附近有什么好玩的",
			factualContent: "问题：周边游玩推荐\n答案：附近可以去罍街和合柴1972。\n相似问法：附近有什么好玩的",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{
				TaskID: "task-1",
				Intent: "hotel_info",
				Query:  tt.query,
				Candidates: []knowledgeEvidenceJudgeCandidate{
					{CandidateID: "task-1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.99, Content: "问题：" + tt.query + "\n答案：转接"}},
					{CandidateID: "task-1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.71, Content: tt.factualContent}},
					{CandidateID: "task-1C3", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.98, Content: "问题：" + tt.query + "\n答案：请结合实际情况确认。"}},
				},
			}
			if _, complete := knowledgeEvidenceJudgeCandidateCompletesTask(task, task.Candidates[1]); !complete {
				t.Fatal("an exact factual alias must be recognized as a complete peer")
			}
			if selection, ok := strictExactKnowledgeEvidenceFAQSelection(task, knowledgeEvidenceLayerStore); ok {
				t.Fatalf("a same-layer exact factual peer must block deterministic handoff fallback: %#v", selection)
			}
			if !selectedKnowledgeEvidenceIsHandoffDirective(task, knowledgeEvidenceLayerStore, []string{"task-1C1"}) {
				t.Fatal("once Judge has seen both peers, its explicit exact-handoff choice must remain valid")
			}

			selected := selectKnowledgeEvidenceJudgeTaskCandidates(task, 1, false)
			if len(selected) != 1 || selected[0].CandidateID != "task-1C2" {
				t.Fatalf("a one-slot budget must retain the complete factual FAQ instead of auto-transfer: %#v", selected)
			}

			selected = selectKnowledgeEvidenceJudgeTaskCandidates(task, 2, false)
			selectedIDs := make(map[string]struct{}, len(selected))
			for _, candidate := range selected {
				selectedIDs[candidate.CandidateID] = struct{}{}
			}
			if len(selected) != 2 {
				t.Fatalf("two-slot budget returned %d candidates: %#v", len(selected), selected)
			}
			for _, candidateID := range []string{"task-1C1", "task-1C2"} {
				if _, ok := selectedIDs[candidateID]; !ok {
					t.Fatalf("the Judge must receive both same-layer conflict peers, missing %s in %#v", candidateID, selected)
				}
			}
			if _, ok := selectedIDs["task-1C3"]; ok {
				t.Fatalf("general fallback must not displace a same-layer conflict peer: %#v", selected)
			}
		})
	}
}

func TestSelectionHasHandoffDirectiveTrustsJudgeVisibleConflictChoice(t *testing.T) {
	query := "老板是谁"
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"T1C1": {
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         rag.RetrieveResult{Score: 0.99, Content: "问题：老板是谁\n答案：转接"},
		},
		"T1C2": {
			CandidateID: "T1C2",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         rag.RetrieveResult{Score: 0.848863, Content: "问题：董事长是谁\n答案：董事长是汤东强。"},
		},
	}
	selection := knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		DecisionSource:       "model",
		SelectedCandidateIDs: []string{"T1C1"},
	}
	if !selectionHasHandoffDirective(selection, knowledgeEvidenceLayerStore, candidates, query) {
		t.Fatal("final disposition must preserve an exact handoff explicitly selected after Judge saw the body peer")
	}
}

func TestMergeRuntimeKnowledgeQueriesTrustsIntentTaskBoundaries(t *testing.T) {
	query := "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 早餐有吗\n2. [消息] 停车免费吗\n3. [消息] 剃须刀在哪"
	got := mergeRuntimeKnowledgeQueries(query, []string{"剃须刀在哪"}, nil)
	want := []string{"剃须刀在哪"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("retrieval must use IntentDetect tasks without local re-splitting, got %#v", got)
	}
}

func TestMergeRuntimeKnowledgeQueriesDoesNotTurnPureContextIntoTask(t *testing.T) {
	query := "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 好困啊\n2. [消息] 有没有咖啡"
	got := mergeRuntimeKnowledgeQueries(query, []string{"有没有咖啡"}, nil)
	want := []string{"有没有咖啡"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected pure context to remain attached to the actual question, got %#v", got)
	}
}

func TestMergeRuntimeKnowledgeQueriesWithoutIntentTaskKeepsWholeTurn(t *testing.T) {
	query := "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 早餐几点\n2. [消息] 停车免费吗\n3. [消息] 发票咋开"
	got := mergeRuntimeKnowledgeQueries(query, nil, nil)
	want := []string{query}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("missing model tasks may use only one coarse query, got %#v", got)
	}
}

func TestMergeRuntimeKnowledgeQueriesSkipsExplicitResourceTasks(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		knowledgeTask string
		resourceTask  string
	}{
		{
			name:          "location resource before breakfast knowledge",
			query:         "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 定位发我\n2. [消息] 早餐几点",
			knowledgeTask: "早餐几点",
			resourceTask:  "定位发我",
		},
		{
			name:          "knowledge before repeated location resource",
			query:         "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [语音] 洗衣房在哪\n2. [消息] 定位再发我",
			knowledgeTask: "洗衣房在哪",
			resourceTask:  "定位再发我",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{
				{Intent: "hotel_info", Text: tt.knowledgeTask, NeedsKnowledge: true},
				{Intent: "hotel_variable", Text: tt.resourceTask, NeedsResource: true, ResourceAction: "provide_location"},
			}}
			got := mergeRuntimeKnowledgeQueries(
				tt.query,
				knowledgeQueriesFromIntentTasks(intent),
				nonKnowledgeQueriesFromIntentTasks(intent),
			)
			want := []string{tt.knowledgeTask}
			if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("expected only the knowledge task to be retrieved, got %#v", got)
			}
		})
	}
}

func TestResolvedIntentTaskTextReplacesEllipticalBurstQuery(t *testing.T) {
	intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{
		{
			Intent:         "hotel_info",
			SubIntent:      "room_facilities",
			Text:           "那麦田呢？",
			ResolvedText:   "麦田房型有没有办公桌？",
			NeedsKnowledge: true,
		},
	}}
	excluded := nonKnowledgeQueriesFromIntentTasks(intent)
	for _, sourceQuery := range knowledgeSourceQueriesFromIntentTasks(intent) {
		excluded = appendRuntimeKnowledgeQuery(excluded, sourceQuery)
	}
	got := mergeRuntimeKnowledgeQueries(
		"客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 那麦田呢？",
		knowledgeQueriesFromIntentTasks(intent),
		excluded,
	)
	want := []string{"麦田房型有没有办公桌？"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("resolvedText must be the only retrieval query, got %#v", got)
	}
}

func TestRuntimeKnowledgeRetrievalTrimsConversationalLeadButKeepsLogicalQuery(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{RawHits: []rag.RetrieveResult{{
			KnowledgeBaseID: 1,
			ChunkID:         101,
			Content:         "问题：怎么办理入住\n答案：请按入住指引办理。",
			Score:           0.95,
		}}},
	}
	batch, err := retrieveContextForRuntimeQuestionList(
		context.Background(),
		retriever,
		retrievers.KnowledgeRetrieveOptions{},
		"还有怎么办理入住",
		[]runtimeKnowledgeQuestionSpec{{TaskID: "T1", Query: "还有怎么办理入住"}},
	)
	if err != nil {
		t.Fatalf("retrieveContextForRuntimeQuestionList returned error: %v", err)
	}
	if len(retriever.queries) != 1 || retriever.queries[0] != "怎么办理入住" {
		t.Fatalf("expected cleaned FastGPT query, got %#v", retriever.queries)
	}
	if batch == nil || len(batch.Questions) != 1 || batch.Questions[0].Query != "还有怎么办理入住" {
		t.Fatalf("logical task query must remain unchanged for Judge mapping, got %#v", batch)
	}
	if batch.Questions[0].EvidenceQuery != "怎么办理入住" {
		t.Fatalf("retrieval query must keep the cleaned question, got %#v", batch.Questions[0])
	}
	tasks := buildKnowledgeEvidenceJudgeTasks(batch, []int64{1}, []int64{1}, nil, "还有怎么办理入住")
	if len(tasks) != 1 || tasks[0].Query != "还有怎么办理入住" || tasks[0].RetrievalQuery != "怎么办理入住" {
		t.Fatalf("Judge semantic and exact-recovery queries must stay separate: %#v", tasks)
	}
	if selection, ok := strictExactKnowledgeEvidenceFAQSelection(tasks[0], knowledgeEvidenceLayerStore); !ok || selection.DecisionSource != "exact_faq_fallback" {
		t.Fatalf("strict failure recovery must still use the cleaned retrieval query: ok=%v selection=%#v", ok, selection)
	}
}

func TestRuntimeKnowledgeShortLabelUsesOneEnrichedQueryAndCarriesMetadata(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{RawHits: []rag.RetrieveResult{{
			KnowledgeBaseID: 1,
			ChunkID:         101,
			Title:           "矿泉水",
			Content:         "房间提供两瓶免费矿泉水。",
			Score:           0.95,
		}}},
	}
	intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{{
		Intent: "hotel_info", SubIntent: "drinking_water", Objective: "compound_information",
		Text: "矿泉水数量和费用", ResolvedText: "矿泉水数量和费用",
		Entities: []callbacks.IntentEntityTraceData{{Text: "矿泉水", Type: "supply"}}, NeedsKnowledge: true,
	}}}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "hotel_info", SubIntent: "drinking_water", Objective: "compound_information",
		Text: "矿泉水数量和费用", ResolvedText: "矿泉水数量和费用", Output: "knowledge_text_reply",
	}}}
	batch, err := retrieveContextForRuntimeQuestions(
		context.Background(), retriever, retrievers.KnowledgeRetrieveOptions{}, "矿泉水数量和费用", intent, plan,
	)
	if err != nil {
		t.Fatalf("retrieveContextForRuntimeQuestions returned error: %v", err)
	}
	if len(retriever.queries) != 1 || retriever.queries[0] != "房间矿泉水有几瓶，是否免费或收费" {
		t.Fatalf("short label must issue exactly one enriched retrieval, got %#v", retriever.queries)
	}
	if batch == nil || len(batch.Questions) != 1 {
		t.Fatalf("expected one knowledge question, got %#v", batch)
	}
	question := batch.Questions[0]
	if question.SubIntent != "drinking_water" || question.Objective != "compound_information" || len(question.Entities) != 1 || question.Entities[0].Text != "矿泉水" {
		t.Fatalf("question metadata was not preserved: %#v", question)
	}
	tasks := buildKnowledgeEvidenceJudgeTasks(batch, []int64{1}, []int64{1}, nil, "矿泉水数量和费用")
	if len(tasks) != 1 || tasks[0].Query != "矿泉水数量和费用" || tasks[0].Objective != "compound_information" || len(tasks[0].Entities) != 1 || tasks[0].Entities[0].Text != "矿泉水" {
		t.Fatalf("Judge task did not receive objective/entities from question metadata: %#v", tasks)
	}
}

func TestRuntimeIntentEvidenceQueryEnrichesExternalProxyOnlyForRetrieval(t *testing.T) {
	spec := runtimeKnowledgeQuestionSpec{
		Intent: "service_request", SubIntent: "external_proxy_action", Objective: "action_request",
		Query: "帮我点个外卖", Entities: []callbacks.IntentEntityTraceData{{Text: "外卖", Type: "order"}},
	}
	got := runtimeIntentEvidenceQuery(spec)
	if got != "外卖办理时酒店地址怎么填写；外卖如何自行办理" {
		t.Fatalf("external proxy retrieval query was not enriched: %q", got)
	}

	internal := spec
	internal.SubIntent = "room_supplies"
	if got := runtimeIntentEvidenceQuery(internal); got != "帮我点个外卖" {
		t.Fatalf("hotel-internal service retrieval must not receive external proxy enrichment: %q", got)
	}

	withoutEntity := spec
	withoutEntity.Entities = nil
	if got := runtimeIntentEvidenceQuery(withoutEntity); got != "帮我点个外卖；办理时酒店地址怎么填写；如何自行办理" {
		t.Fatalf("external proxy retrieval must keep a focused fallback without guessing a target: %q", got)
	}

	ride := spec
	ride.Query = "帮我叫辆网约车"
	ride.Entities = []callbacks.IntentEntityTraceData{{Text: "网约车", Type: "service"}}
	if got := runtimeIntentEvidenceQuery(ride); got != "网约车办理时酒店地址怎么填写；网约车如何自行办理" {
		t.Fatalf("external proxy enrichment must apply by structured entity, got %q", got)
	}

	multiEntity := spec
	multiEntity.Entities = []callbacks.IntentEntityTraceData{
		{Text: "美团", Type: "company"},
		{Text: "外卖", Type: "order"},
	}
	if got := runtimeIntentEvidenceQuery(multiEntity); got != "外卖办理时酒店地址怎么填写；外卖如何自行办理" {
		t.Fatalf("external action entity must win over the platform entity, got %q", got)
	}
}

func TestRuntimeKnowledgeQuestionsKeepReplyPlanEntitiesForExternalProxyRetrieval(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "service_request", SubIntent: "external_proxy_action", Objective: "action_request",
		Text: "帮我点个外卖", ResolvedText: "帮我点个外卖", NeedsKnowledge: true,
		OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply",
		Entities: []callbacks.IntentEntityTraceData{{Text: "外卖", Type: "order"}},
	}}}
	questions, ok := runtimeKnowledgeQuestionsFromReplyPlan(plan)
	if !ok || len(questions) != 1 || len(questions[0].Entities) != 1 || questions[0].Entities[0].Text != "外卖" {
		t.Fatalf("reply plan entities must reach retrieval directly: ok=%v questions=%#v", ok, questions)
	}
	if got := runtimeIntentEvidenceQuery(questions[0]); got != "外卖办理时酒店地址怎么填写；外卖如何自行办理" {
		t.Fatalf("external proxy retrieval did not use the preserved entity: %q", got)
	}
}

func TestExternalProxyUsesOneFocusedRetrievalAndKeepsOriginalJudgeQuestion(t *testing.T) {
	const evidenceQuery = "外卖办理时酒店地址怎么填写；外卖如何自行办理"
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		resultsByQuery: map[string]*retrievers.KnowledgeRetrieveResult{
			evidenceQuery: {RawHits: []rag.RetrieveResult{{
				KnowledgeBaseID: 1, ChunkID: 101, Title: "外卖地址",
				Content: "问题：外卖地址怎么填写？\n答案：填写丽斯未来酒店合肥南七店加楼层房间号。", Score: 0.95,
			}}},
		},
	}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "service_request", SubIntent: "external_proxy_action", Objective: "action_request",
		Text: "帮我点个外卖", ResolvedText: "帮我点个外卖", NeedsKnowledge: true,
		OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply",
		Entities: []callbacks.IntentEntityTraceData{{Text: "外卖", Type: "order"}},
	}}}
	batch, err := retrieveContextForRuntimeQuestions(
		context.Background(), retriever, retrievers.KnowledgeRetrieveOptions{}, "帮我点个外卖", callbacks.IntentTraceData{}, plan,
	)
	if err != nil {
		t.Fatalf("retrieveContextForRuntimeQuestions returned error: %v", err)
	}
	if len(retriever.queries) != 1 || retriever.queries[0] != evidenceQuery {
		t.Fatalf("external proxy must use exactly one focused retrieval, got %#v", retriever.queries)
	}
	if batch == nil || len(batch.Questions) != 1 || batch.Questions[0].Query != "帮我点个外卖" || batch.Questions[0].EvidenceQuery != evidenceQuery {
		t.Fatalf("semantic and retrieval questions were not kept separate: %#v", batch)
	}
	tasks := buildKnowledgeEvidenceJudgeTasks(batch, []int64{1}, []int64{1}, nil, "帮我点个外卖")
	if len(tasks) != 1 || tasks[0].Query != "帮我点个外卖" || tasks[0].RetrievalQuery != evidenceQuery {
		t.Fatalf("Judge must receive the original question plus the focused retrieval query: %#v", tasks)
	}
}

func TestRuntimeIntentEvidenceQueryEnrichesKnownShortLabels(t *testing.T) {
	tests := []struct {
		name string
		spec runtimeKnowledgeQuestionSpec
		want string
	}{
		{name: "room access", spec: runtimeKnowledgeQuestionSpec{Query: "开门方式", SubIntent: "room_access", Objective: "method"}, want: "酒店房门怎么打开"},
		{name: "delivery address", spec: runtimeKnowledgeQuestionSpec{Query: "外卖地址", SubIntent: "delivery_address", Objective: "location"}, want: "酒店外卖地址怎么填写"},
		{name: "wifi credentials", spec: runtimeKnowledgeQuestionSpec{Query: "WiFi账号密码", SubIntent: "network_wifi", Objective: "general_guidance"}, want: "酒店WiFi账号和密码是什么"},
		{name: "checkin", spec: runtimeKnowledgeQuestionSpec{Query: "入住方式", SubIntent: "checkin_process", Objective: "method"}, want: "酒店怎么办理入住"},
		{name: "parking and charging", spec: runtimeKnowledgeQuestionSpec{Query: "停车和充电桩", SubIntent: "parking", Objective: "compound_information"}, want: "酒店停车场和充电桩情况"},
		{name: "invoice", spec: runtimeKnowledgeQuestionSpec{Query: "发票流程", SubIntent: "invoice", Objective: "method"}, want: "酒店发票怎么申请"},
		{name: "keeps time objective", spec: runtimeKnowledgeQuestionSpec{Query: "开门时间", SubIntent: "room_access", Objective: "time", Entities: []callbacks.IntentEntityTraceData{{Text: "房门", Type: "facility"}}}, want: "酒店开门时间是什么"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runtimeIntentEvidenceQuery(tt.spec); got != tt.want {
				t.Fatalf("runtimeIntentEvidenceQuery(%#v)=%q, want %q", tt.spec, got, tt.want)
			}
		})
	}
}

func TestModelEvidenceQueryDoesNotReplaceFullJudgeQuestion(t *testing.T) {
	resolved := "合柴和艺林这两种房型有免费停车吗"
	tasks := convertRuntimeIntentTasks([]runtimeIntentTaskJSON{{
		Intent: "hotel_info", SubIntent: "parking", Objective: "price",
		Text: "这两种房型有免费停车吗", ResolvedText: resolved,
		EvidenceQuery: "酒店停车收费政策", NeedsKnowledge: true, SourceRefs: []string{"U1"},
	}})
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "hotel_info", SubIntent: "parking", Objective: "price",
		Text: tasks[0].Text, ResolvedText: resolved, NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true,
	}}}
	specs, ok := runtimeKnowledgeQuestionsFromReplyPlan(plan, callbacks.IntentTraceData{IntentTasks: tasks})
	if !ok || len(specs) != 1 || runtimeIntentEvidenceQuery(specs[0]) != "酒店停车收费政策" || specs[0].Query != resolved {
		t.Fatalf("retrieval target and Judge conditions must remain separate: %+v", specs)
	}
	tasks[0].EvidenceQuery = ""
	specs, ok = runtimeKnowledgeQuestionsFromReplyPlan(plan, callbacks.IntentTraceData{IntentTasks: tasks})
	if !ok || runtimeIntentEvidenceQuery(specs[0]) != resolved {
		t.Fatalf("old profiles must retain the original full-question query: %+v", specs)
	}
}
func TestMergeRuntimeKnowledgeQueriesDoesNotInventKnowledgeBesideResourceTask(t *testing.T) {
	query := "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 定位发我\n2. [消息] 早餐几点"
	got := mergeRuntimeKnowledgeQueries(query, nil, []string{"定位发我"})
	var want []string
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("retrieval must not locally invent a task omitted by IntentDetect, got %#v", got)
	}
}

func TestMergeRuntimeKnowledgeQueriesDoesNotLocallySplitNonKnowledgeTask(t *testing.T) {
	tests := []struct {
		query       string
		resource    string
		wantQueries []string
	}{
		{query: "定位发我，早餐几点", resource: "定位发我", wantQueries: nil},
		{query: "定位发我，早餐几点", resource: "定位", wantQueries: nil},
		{query: "把入住小程序发我，空调坏了怎么办", resource: "把入住小程序发我", wantQueries: nil},
		{query: "把入住小程序发我，空调坏了怎么办", resource: "入住小程序", wantQueries: nil},
	}
	for _, tt := range tests {
		got := mergeRuntimeKnowledgeQueries(tt.query, nil, []string{tt.resource})
		if strings.Join(got, "\x00") != strings.Join(tt.wantQueries, "\x00") {
			t.Fatalf("expected residual knowledge queries %#v for %q, got %#v", tt.wantQueries, tt.query, got)
		}
	}
}

func TestRebuildRuntimeKnowledgeReplyPlanUsesActualQuestionOrder(t *testing.T) {
	tests := []struct {
		name           string
		plan           callbacks.ReplyPlanTraceData
		questions      []runtimeKnowledgeQuestionResult
		pending        []runtimeKnowledgeQuestionDisposition
		wantAll        []string
		wantGeneration []string
	}{
		{
			name: "restored deferred question appears before planned answer",
			plan: callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
				{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
			}},
			questions: []runtimeKnowledgeQuestionResult{
				{TaskID: "T1", Query: "空调坏了，我住1302"},
				{TaskID: "T2", Query: "顺便问早餐几点"},
			},
			pending:        []runtimeKnowledgeQuestionDisposition{{TaskID: "T1", Query: "空调坏了，我住1302", NeedsHandoff: true}},
			wantAll:        []string{"空调坏了，我住1302", "顺便问早餐几点"},
			wantGeneration: []string{"顺便问早餐几点"},
		},
		{
			name: "restored answer appears before planned deferred question",
			plan: callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
				{Intent: "service_request", SubIntent: "air_conditioner", Text: "空调坏了，我住1302", Output: "knowledge_text_reply"},
			}},
			questions: []runtimeKnowledgeQuestionResult{
				{TaskID: "T1", Query: "顺便问早餐几点"},
				{TaskID: "T2", Query: "空调坏了，我住1302"},
			},
			pending:        []runtimeKnowledgeQuestionDisposition{{TaskID: "T2", Query: "空调坏了，我住1302", NeedsHandoff: true}},
			wantAll:        []string{"顺便问早餐几点", "空调坏了，我住1302"},
			wantGeneration: []string{"顺便问早餐几点"},
		},
		{
			name: "intent task order differs from customer question order",
			plan: callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
				{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
				{Intent: "service_request", SubIntent: "air_conditioner", Text: "空调坏了，我住1302", Output: "knowledge_text_reply"},
			}},
			questions: []runtimeKnowledgeQuestionResult{
				{TaskID: "T1", Query: "空调坏了，我住1302"},
				{TaskID: "T2", Query: "顺便问早餐几点"},
			},
			pending:        []runtimeKnowledgeQuestionDisposition{{TaskID: "T1", Query: "空调坏了，我住1302", NeedsHandoff: true}},
			wantAll:        []string{"空调坏了，我住1302", "顺便问早餐几点"},
			wantGeneration: []string{"顺便问早餐几点"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rebuildRuntimeKnowledgeReplyPlan(tt.plan, tt.questions, tt.pending, true)
			texts := make([]string, 0, len(got.TaskPlans))
			for _, task := range got.TaskPlans {
				texts = append(texts, task.Text)
			}
			if strings.Join(texts, "\x00") != strings.Join(tt.wantAll, "\x00") {
				t.Fatalf("expected all knowledge tasks %#v, got %#v", tt.wantAll, texts)
			}
			active := activeGenerationTaskPlans(callbacks.IntentTraceData{}, got)
			activeTexts := make([]string, 0, len(active))
			for _, task := range active {
				activeTexts = append(activeTexts, task.Text)
			}
			if strings.Join(activeTexts, "\x00") != strings.Join(tt.wantGeneration, "\x00") {
				t.Fatalf("expected Generate tasks %#v, got %#v", tt.wantGeneration, activeTexts)
			}
			if got.ActiveTaskCount != len(tt.wantAll) || got.ReplyRequiredTaskCount != len(tt.wantGeneration) {
				t.Fatalf("unexpected ReplyPlan counts: %#v", got)
			}
		})
	}
}

func TestRebuildLegacyRuntimeKnowledgeReplyPlanPreservesResolvedReferenceIdentity(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		Intent: "hotel_info", SubIntent: "room_type_facility", Text: "那麦田呢", OriginalText: "那麦田呢",
		ResolvedText: "麦田房型有没有办公桌", SourceRefs: []string{"U2", "U1"}, NeedsKnowledge: true,
		OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply",
	}}}
	questions := []runtimeKnowledgeQuestionResult{{
		TaskID: "T1", Intent: "hotel_info", Query: "麦田房型有没有办公桌", OriginalText: "那麦田呢",
		SubIntent: "room_type_facility", SourceRefs: []string{"U2", "U1"},
	}}
	pending := []runtimeKnowledgeQuestionDisposition{{TaskID: "T1", Query: "麦田房型有没有办公桌", NeedsHandoff: true}}

	got := rebuildRuntimeKnowledgeReplyPlan(plan, questions, pending, true)
	if len(got.TaskPlans) != 1 {
		t.Fatalf("expected one preserved deferred Task, got %#v", got.TaskPlans)
	}
	task := got.TaskPlans[0]
	if task.TaskID != "T1" || task.Intent != "hotel_info" || task.Text != "那麦田呢" || task.OriginalText != "那麦田呢" ||
		task.ResolvedText != "麦田房型有没有办公桌" || len(task.SourceRefs) != 2 ||
		task.SourceRefs[0] != "U2" || task.SourceRefs[1] != "U1" || task.OutputKind != "handoff" || task.ReplyRequired {
		t.Fatalf("legacy rebuild lost resolved-reference identity: %#v", task)
	}
}

func TestRebuildRuntimeKnowledgeReplyPlanPreservesStableResolvedReferenceIdentity(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "hotel_info", SubIntent: "room_type_facility", Text: "那麦田呢", OriginalText: "那麦田呢",
		ResolvedText: "麦田房型有没有办公桌", SourceRefs: []string{"U2", "U1"}, NeedsKnowledge: true,
		OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply",
	}}}
	questions := []runtimeKnowledgeQuestionResult{{
		TaskID: "T1", Intent: "hotel_info", Query: "麦田房型有没有办公桌", OriginalText: "那麦田呢",
		SubIntent: "room_type_facility", SourceRefs: []string{"U2", "U1"},
	}}

	got := rebuildRuntimeKnowledgeReplyPlan(plan, questions, nil, false)
	if len(got.TaskPlans) != 1 {
		t.Fatalf("expected one stable Task, got %#v", got.TaskPlans)
	}
	task := got.TaskPlans[0]
	if task.Text != "那麦田呢" || task.OriginalText != "那麦田呢" || task.ResolvedText != "麦田房型有没有办公桌" {
		t.Fatalf("stable rebuild must keep customer wording separate from the resolved query: %#v", task)
	}
}

func TestRebuildRuntimeKnowledgeReplyPlanRestoresEveryAnswerableBurstQuestion(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", SubIntent: "supplies_self_help", Text: "剃须刀在哪", Output: "knowledge_text_reply"},
	}}
	questions := []runtimeKnowledgeQuestionResult{
		{TaskID: "T1", Query: "早餐有吗"},
		{TaskID: "T2", Query: "停车免费吗"},
		{TaskID: "T3", Query: "剃须刀在哪"},
	}
	got := rebuildRuntimeKnowledgeReplyPlan(plan, questions, nil, false)
	texts := make([]string, 0, len(got.TaskPlans))
	for _, task := range got.TaskPlans {
		if runtimeReplyTaskUsesKnowledge(task) {
			texts = append(texts, task.Text)
		}
	}
	want := []string{"早餐有吗", "停车免费吗", "剃须刀在哪"}
	if strings.Join(texts, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected restored tasks in customer order %#v, got %#v", want, texts)
	}
}

func TestRebuildRuntimeKnowledgeReplyPlanPreservesBlankServiceRequestTask(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "service_request", SubIntent: "air_conditioner", Output: "knowledge_text_reply"},
	}}
	questions := []runtimeKnowledgeQuestionResult{{TaskID: "T1", Query: "空调坏了"}}
	got := rebuildRuntimeKnowledgeReplyPlan(plan, questions, nil, false)
	if len(got.TaskPlans) != 1 {
		t.Fatalf("expected one rebuilt task, got %#v", got.TaskPlans)
	}
	task := got.TaskPlans[0]
	if task.Intent != "service_request" || task.SubIntent != "air_conditioner" || task.Text != "空调坏了" {
		t.Fatalf("expected the original service request semantics to survive query binding, got %#v", task)
	}
}

func TestRebuildRuntimeKnowledgeReplyPlanRetainsStableDeferredTaskForResume(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{
			TaskID: "task-1", Intent: "hotel_info", SubIntent: "breakfast", Objective: "time",
			RelationToPrevious: "independent", ResolutionState: "clear", Text: "早餐几点",
			OriginalText: "早餐几点", ResolvedText: "早餐几点", SourceRefs: []string{"U1"},
			NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply",
			SelectedLayer: knowledgeEvidenceLayerStore, SelectedCandidateIDs: []string{"T1C1"},
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "T1F1", Statement: "早餐时间为7:00-9:30。"}},
		},
		{
			TaskID: "task-2", Intent: "service_request", SubIntent: "lost_item", Objective: "action_request",
			RelationToPrevious: "independent", ResolutionState: "clear", Text: "东西落房间了",
			OriginalText: "东西落房间了", ResolvedText: "东西落房间了", SourceRefs: []string{"U1"},
			NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply",
			SelectedLayer: knowledgeEvidenceLayerStore, SelectedCandidateIDs: []string{"T2C1"},
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "T2F1", Statement: "旧事实"}},
		},
	}}
	questions := []runtimeKnowledgeQuestionResult{
		{TaskID: "task-1", Query: "早餐几点"},
		{TaskID: "task-2", Query: "东西落房间了"},
	}
	pending := []runtimeKnowledgeQuestionDisposition{{
		TaskID: "task-2", Query: "东西落房间了", NeedsHandoff: true,
		Disposition: runtimeKnowledgeDispositionNoEvidenceHandoff, MissingAspects: []string{"room_number"},
	}}

	got := rebuildRuntimeKnowledgeReplyPlan(plan, questions, pending, true)
	if len(got.TaskPlans) != 2 || got.TaskPlans[0].TaskID != "task-1" || got.TaskPlans[1].TaskID != "task-2" {
		t.Fatalf("stable deferred Task must remain in its original order: %#v", got.TaskPlans)
	}
	deferred := got.TaskPlans[1]
	if deferred.OutputKind != "handoff" || deferred.ReplyRequired || deferred.Output != runtimeKnowledgeDeferredHandoffOutput ||
		deferred.NeedsHumanRoute || !deferred.NeedsKnowledge || len(deferred.SourceRefs) != 1 || deferred.SourceRefs[0] != "U1" ||
		deferred.SelectedLayer != "" || len(deferred.SelectedCandidateIDs) != 0 || len(deferred.SupportedFacts) != 0 ||
		strings.Join(deferred.MissingAspects, ",") != "room_number" {
		t.Fatalf("deferred Task must preserve identity and source metadata while leaving Generate: %#v", deferred)
	}
	active := activeGenerationTaskPlans(callbacks.IntentTraceData{}, got)
	if len(active) != 1 || active[0].TaskID != "task-1" {
		t.Fatalf("Generate must see only the answerable sibling: %#v", active)
	}
	if blocked := ungroundedKnowledgeReplyTaskIDs(got); len(blocked) != 0 {
		t.Fatalf("non-text deferred Task must not become a knowledge-safe fallback: %#v", blocked)
	}
}

func TestDeferredRuntimeKnowledgeInstructionHidesDeferredTaskTextFromGenerate(t *testing.T) {
	pending := []runtimeKnowledgeQuestionDisposition{{TaskID: "T1", Query: "空调坏了，我住1302，需要维修", NeedsHandoff: true}}
	handoffInstruction := buildDeferredRuntimeKnowledgeInstruction(pending, true)
	if strings.Contains(handoffInstruction, "空调") || strings.Contains(handoffInstruction, "1302") || strings.Contains(handoffInstruction, "维修") {
		t.Fatalf("handoff-enabled Generate instruction must not expose removed task text, got %q", handoffInstruction)
	}
	if !strings.Contains(handoffInstruction, "不属于本次 Generate 的文本任务") {
		t.Fatalf("expected an explicit deferred-task output boundary, got %q", handoffInstruction)
	}

	disabledInstruction := buildDeferredRuntimeKnowledgeInstruction(pending, false)
	if !strings.Contains(disabledInstruction, "空调坏了") {
		t.Fatalf("handoff-disabled flow still needs the pending label for a natural no-answer response, got %q", disabledInstruction)
	}
}

func TestKnowledgePolicyPromotesTopExactHandoffDirective(t *testing.T) {
	top := rag.RetrieveResult{
		KnowledgeBaseID: 1,
		SourceRecordID:  "toilet-blocked",
		Title:           "马桶堵了怎么办",
		Content:         "问题：马桶堵了怎么办\n答案：转接",
		Score:           0.98,
	}
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits:             []rag.RetrieveResult{top},
			ContextResults:   []rag.RetrieveResult{top},
			ContextText:      top.Content,
			AnswerMode:       enums.KnowledgeAnswerModeStrict,
		},
	}
	summary := &RunResult{}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetActionLedger(buildInitialActionLedger(hotelInfoIntent()))
	state, err := newTestKnowledgePolicyGate(retriever).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("马桶堵了怎么办", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !summary.handoffDirective || summary.handoffDirectiveSource != "knowledge_top_answer" {
		t.Fatalf("expected exact top answer to request handoff, got %+v", summary)
	}
	if state.RetrieveResult == nil || strings.Contains(state.RetrieveResult.ContextText, "转接") {
		t.Fatalf("expected internal directive to stay out of Generate context, got %#v", state.RetrieveResult)
	}
	if collector.Data.Retriever.ContextCount != 0 || len(collector.Data.Retriever.Items) != 1 ||
		collector.Data.Retriever.Items[0].UsedInContext || collector.Data.Retriever.Items[0].ContextRankNo != 0 {
		t.Fatalf("handoff evidence must not remain marked as Generate context: %#v", collector.Data.Retriever)
	}
	if !actionLedgerContainsAction(collector.Data.ActionLedger.RequestedActions, "human_route") {
		t.Fatalf("expected handoff request in action ledger, got %#v", collector.Data.ActionLedger)
	}
}

func TestKnowledgePolicyKeepsRoomNumberAnswerAndIgnoresLowerRankedDirective(t *testing.T) {
	top := rag.RetrieveResult{
		KnowledgeBaseID: 1,
		SourceRecordID:  "room-number",
		Title:           "设备故障",
		Content:         "问题：空调坏了怎么办\n答案：请告诉我房间号，我先确认是哪一间房。",
		Score:           0.98,
	}
	lower := rag.RetrieveResult{
		KnowledgeBaseID: 1,
		SourceRecordID:  "legacy-transfer",
		Title:           "其他服务",
		Content:         "问题：其他服务\n答案：转接",
		Score:           0.80,
	}
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits:             []rag.RetrieveResult{top, lower},
			ContextResults:   []rag.RetrieveResult{top, lower},
			ContextText:      top.Content + "\n" + lower.Content,
			AnswerMode:       enums.KnowledgeAnswerModeStrict,
		},
	}
	summary := &RunResult{}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetActionLedger(buildInitialActionLedger(hotelInfoIntent()))
	state, err := newTestKnowledgePolicyGate(retriever).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("空调坏了", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if summary.handoffDirective {
		t.Fatal("lower-ranked transfer directive must not bypass the room-number reply")
	}
	if state.RetrieveResult == nil || !strings.Contains(state.RetrieveResult.ContextText, "房间号") {
		t.Fatalf("expected normal room-number answer to remain available, got %#v", state.RetrieveResult)
	}
	if strings.Contains(state.RetrieveResult.ContextText, "转接") {
		t.Fatalf("expected lower-ranked internal directive to stay out of Generate context, got %q", state.RetrieveResult.ContextText)
	}
	if actionLedgerContainsAction(collector.Data.ActionLedger.RequestedActions, "human_route") {
		t.Fatalf("ordinary room-number clarification must not request handoff, got %#v", collector.Data.ActionLedger)
	}
}

func TestKnowledgePolicyDoesNotUseHardcodedSupplyFallback(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	gate := newTestKnowledgePolicyGate(retriever)
	_, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("能不能送两瓶水到房间", "1"),
		Summary: &RunResult{},
		Intent:  hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !retriever.called {
		t.Fatal("expected supply request to go through knowledge/runtime path")
	}
}

func TestKnowledgePolicyDoesNotLetSupplyFastPathHideMixedKnowledgeQuestions(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		resultsByQuery: map[string]*retrievers.KnowledgeRetrieveResult{
			"wifi和停车都发我一下": {
				KnowledgeBaseIDs: []int64{1},
				Hits: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 199, Title: "错域", Content: "在公司介绍模式里，如果用户问早餐、停车这类门店服务，可以先说明这里主要回答公司、品牌、展厅、加盟和AI方案；具体门店服务再由现场工作人员确认。", Score: 0.98},
					{KnowledgeBaseID: 1, ChunkID: 200, Title: "停车异常", Content: "车辆出场或闸口问题需要门店工作人员协助处理，请联系门店管家或前台。", Score: 0.96},
					{KnowledgeBaseID: 1, ChunkID: 201, Title: "WiFi", Content: "WiFi 名称是 LISI，密码看房间桌牌。", Score: 0.94},
					{KnowledgeBaseID: 1, ChunkID: 202, Title: "停车", Content: "门店有免费地上停车场。", Score: 0.91},
				},
				ContextResults: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 199, Title: "错域", Content: "在公司介绍模式里，如果用户问早餐、停车这类门店服务，可以先说明这里主要回答公司、品牌、展厅、加盟和AI方案；具体门店服务再由现场工作人员确认。", Score: 0.98},
					{KnowledgeBaseID: 1, ChunkID: 200, Title: "停车异常", Content: "车辆出场或闸口问题需要门店工作人员协助处理，请联系门店管家或前台。", Score: 0.96},
					{KnowledgeBaseID: 1, ChunkID: 201, Title: "WiFi", Content: "WiFi 名称是 LISI，密码看房间桌牌。", Score: 0.94},
					{KnowledgeBaseID: 1, ChunkID: 202, Title: "停车", Content: "门店有免费地上停车场。", Score: 0.91},
				},
				ContextText: "WiFi 名称是 LISI，密码看房间桌牌。\n门店有免费地上停车场。",
				AnswerMode:  enums.KnowledgeAnswerModeStrict,
			},
			"房间没纸巾": {
				KnowledgeBaseIDs: []int64{1},
				Hits: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 203, Title: "用品", Content: "纸巾在1020对面的洗衣房，可以自取。", Score: 0.95},
				},
				ContextResults: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 203, Title: "用品", Content: "纸巾在1020对面的洗衣房，可以自取。", Score: 0.95},
				},
				ContextText: "纸巾在1020对面的洗衣房，可以自取。",
				AnswerMode:  enums.KnowledgeAnswerModeStrict,
			},
		},
	}
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		selections := make(map[string]map[string]knowledgeEvidenceLayerSelection, len(tasks))
		for _, task := range tasks {
			selectedIDs := make([]string, 0, 2)
			for _, candidate := range task.Candidates {
				content := strings.TrimSpace(candidate.Hit.Content)
				if strings.Contains(content, "WiFi 名称是 LISI") || strings.Contains(content, "免费地上停车场") || strings.Contains(content, "纸巾在1020") {
					selectedIDs = append(selectedIDs, candidate.CandidateID)
				}
			}
			decision := knowledgeEvidenceDecisionDirectSingle
			if len(selectedIDs) > 1 {
				decision = knowledgeEvidenceDecisionDirectCombined
			}
			selections[task.TaskID] = map[string]knowledgeEvidenceLayerSelection{
				knowledgeEvidenceLayerStore: {Decision: decision, SelectedCandidateIDs: selectedIDs},
			}
		}
		return knowledgeEvidenceJudgeOutcome{
			Applied:    true,
			Selections: selections,
			Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
				SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
				Status:        "completed",
			},
		}
	}}
	gate := &KnowledgeAnswerabilityGate{
		newRetriever: func(models.AIAgent) knowledgeContextRetriever { return retriever },
		judge:        judge,
	}
	intent := hotelInfoIntent()
	intent.IntentTasks = []callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", Text: "wifi和停车都发我一下", ResolvedText: "wifi和停车都发我一下", NeedsKnowledge: true},
		{Intent: "hotel_info", Text: "房间没纸巾", ResolvedText: "房间没纸巾", NeedsKnowledge: true},
	}
	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("客人刚才连续发了几条消息，请一起理解，不要只回复最后一句：\nwifi和停车都发我一下\n房间没纸巾", "1"),
		Summary: &RunResult{},
		Intent:  intent,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !stringSliceSetEqual(retriever.queries, []string{"wifi和停车都发我一下", "房间没纸巾"}) {
		t.Fatalf("expected retrieval for each current burst question, got %#v", retriever.queries)
	}
	if state.RetrieveResult == nil {
		t.Fatalf("expected retrieval result, got nil")
	}
	if !strings.Contains(state.RetrieveResult.ContextText, "WiFi 名称是 LISI") || !strings.Contains(state.RetrieveResult.ContextText, "纸巾在1020") {
		t.Fatalf("expected merged context from each burst question, got %q", state.RetrieveResult.ContextText)
	}
}

func TestKnowledgePolicyUsesFastPathWhenMediaFollowUpHasNoUnderstanding(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	gate := newTestKnowledgePolicyGate(retriever)
	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("刚才发的语音你听懂了吗", "1"),
		Summary: &RunResult{},
		Intent:  mediaUnderstandingIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !state.SkipGate {
		t.Fatal("missing media context should skip knowledge and let the model respond from instruction")
	}
	if len(state.Decision.Instructions) == 0 || !strings.Contains(state.Decision.Instructions[0].Content, "媒体上下文状态") {
		t.Fatalf("expected missing media instruction, got %#v", state.Decision.Instructions)
	}
	if retriever.called {
		t.Fatal("missing media context should not call knowledge retriever")
	}
}

func TestKnowledgePolicyKeepsModelPathWhenMediaUnderstandingExists(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	gate := newTestKnowledgePolicyGate(retriever)
	_, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("刚才发的语音你听懂了吗", "1"),
		Summary: &RunResult{},
		Intent:  mediaUnderstandingIntent(),
		Messages: []*schema.Message{
			schema.UserMessage("[语音]\n语音内容是：确认确认"),
		},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
}

func TestKnowledgePolicyKeepsModelPathWhenImageUnderstandingExists(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	gate := newTestKnowledgePolicyGate(retriever)
	req := newKnowledgePolicyRunInput("你看不到我的照片吗", "1")
	_, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request: req,
		Summary: &RunResult{},
		Intent:  mediaUnderstandingIntent(),
		Messages: []*schema.Message{
			schema.UserMessage("[图片]\n图片内容是：图片中可见一桌菜、果粒橙和保温桶。"),
		},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
}

func newTestKnowledgePolicyGate(retriever knowledgeContextRetriever) *KnowledgeAnswerabilityGate {
	return &KnowledgeAnswerabilityGate{
		newRetriever: func(aiAgent models.AIAgent) knowledgeContextRetriever {
			return retriever
		},
		judge: deterministicTestKnowledgeEvidenceJudge{},
	}
}

type deterministicTestKnowledgeEvidenceJudge struct{}

func (deterministicTestKnowledgeEvidenceJudge) JudgeBatch(_ context.Context, _ RunInput, tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
	selections := make(map[string]map[string]knowledgeEvidenceLayerSelection, len(tasks))
	for _, task := range tasks {
		layers := make(map[string]knowledgeEvidenceLayerSelection)
		for _, candidate := range task.Candidates {
			if _, exists := layers[candidate.Layer]; exists {
				continue
			}
			layers[candidate.Layer] = knowledgeEvidenceLayerSelection{
				Decision:             knowledgeEvidenceDecisionDirectSingle,
				SelectedCandidateIDs: []string{candidate.CandidateID},
			}
		}
		selections[task.TaskID] = layers
	}
	return knowledgeEvidenceJudgeOutcome{
		Applied:    true,
		Selections: selections,
		Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
			SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
			Status:        "completed",
		},
	}
}

func stringSliceSetEqual(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[string]int, len(want))
	for _, item := range want {
		counts[item]++
	}
	for _, item := range got {
		if counts[item] == 0 {
			return false
		}
		counts[item]--
	}
	return true
}

func newKnowledgePolicyRunInput(content string, knowledgeIDs string) RunInput {
	return RunInput{
		UserMessage: models.Message{Content: content},
		AIAgent: models.AIAgent{
			KnowledgeIDs:    knowledgeIDs,
			FallbackMode:    enums.AIAgentFallbackModeSuggestRetry,
			FallbackMessage: "我暂时没有找到足够准确的信息。你可以补充更具体的问题，我再继续帮你查。",
			AllowedMCPTools: "[]",
		},
		AIConfig: models.AIConfig{ModelName: "fake-model"},
	}
}

func hotelInfoIntent() callbacks.IntentTraceData {
	return callbacks.IntentTraceData{PrimaryIntent: "hotel_info", MatchedIntentCode: "hotel_info", NeedsKnowledge: true, ShouldReply: true}
}

func socialConfirmIntent() callbacks.IntentTraceData {
	return callbacks.IntentTraceData{PrimaryIntent: "interaction", MatchedIntentCode: "interaction", ShouldReply: true}
}

func mediaUnderstandingIntent() callbacks.IntentTraceData {
	return callbacks.IntentTraceData{PrimaryIntent: "interaction", MatchedIntentCode: "interaction", SubIntent: "media_context_follow_up", ShouldReply: true}
}

func humanRiskIntent() callbacks.IntentTraceData {
	return callbacks.IntentTraceData{PrimaryIntent: "human_complaint_risk", MatchedIntentCode: "human_complaint_risk", NeedsHumanRoute: true, ShouldReply: true}
}

func messagesContainContent(messages []*schema.Message, needle string) bool {
	for _, message := range messages {
		if message != nil && strings.Contains(message.Content, needle) {
			return true
		}
	}
	return false
}

func TestKnowledgePolicyEvaluateUsesDeterministicConversationalReply(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(retriever)

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("你好", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent:    socialConfirmIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !state.SkipGate {
		t.Fatal("expected conversational intent to skip knowledge and continue to model")
	}
	if retriever.called {
		t.Fatal("expected retriever not to run for conversational intent")
	}
	if collector.Data.Answerability.Status != answerabilityStatusSkipped {
		t.Fatalf("unexpected policy status: %q", collector.Data.Answerability.Status)
	}
	if collector.Data.Answerability.Reason != "intent does not require knowledge" {
		t.Fatalf("unexpected policy reason: %q", collector.Data.Answerability.Reason)
	}
}

func TestKnowledgePolicyEvaluateInjectsNoContextInstructionForKnowledgeQuestion(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
		},
	})

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点", "1"),
		Summary:   &RunResult{},
		Intent:    hotelInfoIntent(),
		Collector: collector,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if state.SkipGate {
		t.Fatal("expected configured knowledge to inject policy, not skip")
	}
	if len(state.Decision.Instructions) != 1 {
		t.Fatalf("expected one no-context instruction, got %d", len(state.Decision.Instructions))
	}
	if !strings.Contains(state.Decision.Instructions[0].Content, "当前没有从知识库检索到可用资料") {
		t.Fatalf("unexpected no-context instruction: %q", state.Decision.Instructions[0].Content)
	}
	if !strings.Contains(state.Decision.Instructions[0].Content, "不得编造") {
		t.Fatalf("expected anti-hallucination policy, got %q", state.Decision.Instructions[0].Content)
	}
	if !strings.Contains(state.Decision.Instructions[0].Content, "图片/语音/文件等媒体理解结果") {
		t.Fatalf("expected media-aware no-context policy, got %q", state.Decision.Instructions[0].Content)
	}
	if !strings.Contains(state.Decision.Instructions[0].Content, "不要因为知识库未命中就输出固定兜底话术") {
		t.Fatalf("expected policy to avoid robotic fallback, got %q", state.Decision.Instructions[0].Content)
	}
	assertNoFixedFallbackSource(t, state.Decision.Instructions[0].Content)
	if !strings.Contains(state.Decision.Instructions[0].Content, "否则进入接待路由") {
		t.Fatalf("expected actionable no-context policy, got %q", state.Decision.Instructions[0].Content)
	}
	if state.Input.Summary == nil || !state.Input.Summary.handoffDirective || state.Input.Summary.handoffDirectiveSource != "knowledge_no_context" {
		t.Fatalf("expected no-context business question to request handoff, got %#v", state.Input.Summary)
	}
	if collector.Data.Answerability.Status != answerabilityStatusNoContext {
		t.Fatalf("unexpected policy status: %q", collector.Data.Answerability.Status)
	}
}

func TestKnowledgePolicyRetrievesWifiInsteadOfSkippingAsAction(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
		},
	}
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(retriever)

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("房间网连不上", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.SkipGate {
		t.Fatal("wifi/network question should retrieve configured knowledge, not skip as an action")
	}
	if !retriever.called {
		t.Fatal("expected wifi/network question to call knowledge retriever")
	}
	if got := strings.Join(retriever.queries, "|"); !strings.Contains(got, "房间网连不上") {
		t.Fatalf("expected original wifi query to be retrieved, got %q", got)
	}
	if collector.Data.Answerability.Status != answerabilityStatusNoContext {
		t.Fatalf("unexpected policy status: %q", collector.Data.Answerability.Status)
	}
	if len(state.Decision.Instructions) != 1 || !strings.Contains(state.Decision.Instructions[0].Content, "WiFi") {
		t.Fatalf("expected wifi-aware no-context instruction, got %#v", state.Decision.Instructions)
	}
}

func assertNoFixedFallbackSource(t *testing.T, content string) {
	t.Helper()
	badFragments := []string{
		"我先记下",
		"帮你确认下",
		"帮您确认下",
		"让同事确认",
		"稍后让同事跟进",
		"可参考但不要死抄",
		"确实无法确认时才短句回复",
	}
	for _, bad := range badFragments {
		if strings.Contains(content, bad) {
			t.Fatalf("knowledge instruction leaks fixed fallback source %q: %q", bad, content)
		}
	}
}

func TestKnowledgeGuardInstructionsDoNotLeakFixedFallbackSources(t *testing.T) {
	for name, instruction := range map[string]string{
		"no_context":      buildKnowledgeNoContextInstruction(),
		"retrieval_error": buildKnowledgeRetrievalErrorInstruction(),
		"strict":          buildKnowledgeRuntimeInstruction(enums.KnowledgeAnswerModeStrict),
		"assist":          buildKnowledgeRuntimeInstruction(enums.KnowledgeAnswerModeAssist),
	} {
		t.Run(name, func(t *testing.T) {
			assertNoFixedFallbackSource(t, instruction)
		})
	}
}

func TestBuildRunMessagesMarksHandoffWhenNoContext(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 200, MatchMode: "keyword", Keywords: "早餐", NeedsKnowledge: true, Status: enums.StatusOk})
	summary := &RunResult{}
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
		},
	})

	messages := make([]*schema.Message, 0)
	req := newKnowledgePolicyRunInput("早餐几点", "1")
	outcome := appendRetrievedContext(context.Background(), req, hotelInfoIntent(), summary, nil, gate, &messages)
	messages = append(messages, schema.UserMessage(req.UserMessage.Content))
	if outcome.AnswerabilityStatus != answerabilityStatusNoContext {
		t.Fatalf("unexpected answerability status: %q", outcome.AnswerabilityStatus)
	}

	if summary.ReplyText != "" {
		t.Fatalf("expected no early fallback reply, got %q", summary.ReplyText)
	}
	if !summary.handoffDirective || summary.handoffDirectiveSource != "knowledge_no_context" {
		t.Fatalf("expected no-context business question to enter handoff flow, got %#v", summary)
	}
	if !messagesContainContent(messages, "当前没有从知识库检索到可用资料") {
		t.Fatalf("expected no-context instruction in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "早餐几点") {
		t.Fatalf("expected current user message to remain in messages: %#v", messages)
	}
}

func TestKnowledgePolicyDefersMissingKnowledgeWithoutSwallowingIndependentSiblings(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", Text: "早餐几点", OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
		{TaskID: "task-2", Intent: "interaction", Text: "谢谢", OutputKind: "text", ReplyRequired: true, Output: "text_reply"},
		{TaskID: "task-3", Intent: "hotel_variable", Text: "定位发我", OutputKind: "resource", Output: "structured_resource_commit", ResourceAction: "provide_location"},
	}})
	summary := &RunResult{}

	state, err := newTestKnowledgePolicyGate(nil).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点，谢谢，定位发我", ""),
		Summary:   summary,
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.AnswerabilityStatus != answerabilityStatusNoContext {
		t.Fatalf("unexpected answerability status: %q", state.AnswerabilityStatus)
	}
	if summary.handoffDirective {
		t.Fatalf("missing knowledge must not create a global handoff when an interaction sibling can still run: %#v", summary)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if !trace.DeferredHandoff || len(trace.DeferredTaskIDs) != 1 || trace.DeferredTaskIDs[0] != "task-1" {
		t.Fatalf("knowledge task must be deferred independently, got %#v", trace)
	}
	if len(trace.Tasks) != 1 || trace.Tasks[0].TaskID != "task-1" ||
		trace.Tasks[0].Disposition != runtimeKnowledgeDispositionNoEvidenceHandoff ||
		trace.Tasks[0].Decision != knowledgeEvidenceDecisionInsufficient || trace.Tasks[0].DecisionSource != "source_unavailable" {
		t.Fatalf("pre-judge knowledge deferral must persist an explicit Task disposition, got %#v", trace.Tasks)
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) != 3 || plan.TaskPlans[0].TaskID != "task-1" || plan.TaskPlans[1].TaskID != "task-2" || plan.TaskPlans[2].TaskID != "task-3" {
		t.Fatalf("deferred knowledge and independent siblings must keep stable order, got %#v", plan.TaskPlans)
	}
	deferred := plan.TaskPlans[0]
	if deferred.Output != runtimeKnowledgeDeferredHandoffOutput || deferred.OutputKind != "handoff" || deferred.ReplyRequired {
		t.Fatalf("missing knowledge must remain as a non-text Deferred Task, got %#v", deferred)
	}
	active := activeGenerationTaskPlans(callbacks.IntentTraceData{}, plan)
	if len(active) != 1 || active[0].TaskID != "task-2" {
		t.Fatalf("Generate must see only the interaction sibling, got %#v", active)
	}
}

func TestDeferUnavailableKnowledgePersistsTraceWhenAutoHandoffDisabled(t *testing.T) {
	db := setupRuntimeIntentConfigTestDB(t)
	conversation := models.Conversation{ID: 8101, CustomerID: 9101, Status: enums.IMConversationStatusAIServing}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		ConversationID:   conversation.ID,
		WxWorkInstanceID: 771,
		RouteStatus:      enums.ConversationRouteStatusAIServing,
		RouteTarget:      "ai",
		SessionNo:        1,
	}).Error; err != nil {
		t.Fatalf("create conversation route: %v", err)
	}
	now := time.Now()
	if err := db.Model(&models.WxWorkCustomerHandoffSetting{}).Create(map[string]any{
		"customer_id":          conversation.CustomerID,
		"wx_work_instance_id":  int64(771),
		"auto_handoff_enabled": false,
		"created_at":           now,
		"updated_at":           now,
	}).Error; err != nil {
		t.Fatalf("create disabled handoff setting: %v", err)
	}
	collector := callbacks.NewRuntimeTraceCollector()
	originalPlan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", Text: "早餐几点", ResolvedText: "早餐几点", NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
		{TaskID: "task-2", Intent: "interaction", Text: "谢谢", OutputKind: "text", ReplyRequired: true, Output: "text_reply"},
	}}
	collector.SetReplyPlan(originalPlan)
	state := &answerabilityGateState{Input: answerabilityGateInput{
		Request:   RunInput{Conversation: conversation},
		Collector: collector,
		Intent:    hotelInfoIntent(),
	}}

	if preserved := deferUnavailableKnowledgeForIndependentWork(state, "知识检索暂时不可用"); !preserved {
		t.Fatal("independent interaction sibling must remain runnable")
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if trace.DeferredHandoff || len(trace.DeferredTaskIDs) != 0 {
		t.Fatalf("disabled auto handoff must not create a real deferred route: %#v", trace)
	}
	if len(trace.Tasks) != 1 || trace.Tasks[0].TaskID != "task-1" ||
		trace.Tasks[0].Decision != knowledgeEvidenceDecisionInsufficient ||
		trace.Tasks[0].DecisionSource != "source_unavailable" ||
		trace.Tasks[0].Disposition != runtimeKnowledgeDispositionNoEvidenceHandoff {
		t.Fatalf("source failure must remain visible per Task even when handoff is disabled: %#v", trace.Tasks)
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) != 2 || plan.TaskPlans[0].Output != "knowledge_text_reply" ||
		plan.TaskPlans[0].OutputKind != "text" || !plan.TaskPlans[0].ReplyRequired ||
		plan.TaskPlans[1].TaskID != "task-2" {
		t.Fatalf("disabled handoff customer behavior and ReplyPlan must remain unchanged: %#v", plan.TaskPlans)
	}
}

func TestKnowledgePolicyKeepsPureMissingKnowledgeHandoff(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "hotel_info", Text: "早餐几点", OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply",
	}}})
	summary := &RunResult{}

	state, err := newTestKnowledgePolicyGate(nil).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点", ""),
		Summary:   summary,
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.AnswerabilityStatus != answerabilityStatusNoContext || !summary.handoffDirective {
		t.Fatalf("pure knowledge request must keep the existing direct handoff behavior, state=%#v summary=%#v", state, summary)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if !trace.DeferredHandoff || len(trace.DeferredTaskIDs) != 1 || trace.DeferredTaskIDs[0] != "task-1" {
		t.Fatalf("pure knowledge handoff must retain its Task for precise resume: %#v", trace)
	}
	if len(trace.Tasks) != 1 || trace.Tasks[0].TaskID != "task-1" ||
		trace.Tasks[0].Disposition != runtimeKnowledgeDispositionNoEvidenceHandoff ||
		trace.Tasks[0].DecisionSource != "source_unavailable" {
		t.Fatalf("pure pre-judge handoff must retain an explicit Task disposition: %#v", trace.Tasks)
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) != 1 || plan.TaskPlans[0].Output != runtimeKnowledgeDeferredHandoffOutput ||
		plan.TaskPlans[0].OutputKind != "handoff" || plan.TaskPlans[0].ReplyRequired {
		t.Fatalf("pure knowledge task must remain as a non-text Deferred Task for resume: %#v", plan.TaskPlans)
	}
}

func TestKnowledgePolicyAllPendingPersistsRetrieverAndJudgeTrace(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	storeHit := rag.RetrieveResult{KnowledgeBaseID: 1, ChunkID: 101, Title: "门店用品", Content: "问题：拖鞋没了怎么办\n答案：当前资料没有可用处理方式。", Score: 0.91}
	storeDiscardedHit := rag.RetrieveResult{KnowledgeBaseID: 1, ChunkID: 102, Title: "门店其他用品", Content: "问题：浴巾放在哪里\n答案：浴巾在房间衣柜内。", Score: 0.89}
	generalHit := rag.RetrieveResult{KnowledgeBaseID: 2, ChunkID: 201, Title: "通用用品", Content: "问题：可以补充用品吗\n答案：具体情况需要同事处理。", Score: 0.88}
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1, 2},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1, 2},
			RawHits:          []rag.RetrieveResult{storeHit, storeDiscardedHit, generalHit},
			Hits:             []rag.RetrieveResult{storeHit},
			ContextResults:   []rag.RetrieveResult{storeHit},
			ContextText:      storeHit.Content,
			TraceSummary: callbacks.RetrieverTraceSummary{
				TopK:         10,
				HitCount:     1,
				ContextCount: 1,
			},
			TraceItems: []callbacks.RetrieverTraceItem{
				{Query: "拖鞋没了", KnowledgeBaseID: 1, DocumentID: 101, Score: 0.91, RawRankNo: 1, ContextRankNo: 1, UsedInContext: true},
				{Query: "拖鞋没了", KnowledgeBaseID: 1, DocumentID: 102, Score: 0.89, RawRankNo: 2, DiscardReason: "context_budget"},
				{Query: "拖鞋没了", KnowledgeBaseID: 2, DocumentID: 201, Score: 0.88, RawRankNo: 3, DiscardReason: "context_budget"},
			},
		},
	}
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		if len(tasks) != 1 {
			t.Fatalf("expected one knowledge task, got %#v", tasks)
		}
		return knowledgeEvidenceJudgeOutcome{
			Applied: true,
			Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
				tasks[0].TaskID: {
					knowledgeEvidenceLayerStore: {
						Decision:       knowledgeEvidenceDecisionInsufficient,
						DecisionSource: "model",
						MissingAspects: []string{"处理方式"},
					},
					knowledgeEvidenceLayerGeneral: {
						Decision:       knowledgeEvidenceDecisionInsufficient,
						DecisionSource: "model",
						MissingAspects: []string{"门店适用范围"},
					},
				},
			},
			Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
				SchemaVersion:  knowledgeEvidenceJudgeSchemaVersion,
				Status:         "completed",
				TaskCount:      1,
				CandidateCount: 3,
			},
		}
	}}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "hotel_info", Text: "拖鞋没了", ResolvedText: "拖鞋没了",
		OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply",
	}}})
	summary := &RunResult{}
	state, err := (&KnowledgeAnswerabilityGate{
		newRetriever: func(models.AIAgent) knowledgeContextRetriever { return retriever },
		judge:        judge,
	}).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("拖鞋没了", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.AnswerabilityStatus != answerabilityStatusNoContext || !summary.handoffDirective {
		t.Fatalf("all-pending knowledge must enter the existing handoff path, state=%#v summary=%#v", state, summary)
	}
	if summary.RetrieverCount != 3 || collector.Data.Retriever.Count != 3 || len(collector.Data.Retriever.Items) != 3 {
		t.Fatalf("raw retrieval trace must survive all-pending early return, summary=%d retriever=%#v", summary.RetrieverCount, collector.Data.Retriever)
	}
	if collector.Data.Retriever.ContextCount != 0 {
		t.Fatalf("final retriever summary must reflect the cleared effective context: %#v", collector.Data.Retriever)
	}
	for _, item := range collector.Data.Retriever.Items {
		if item.UsedInContext || item.ContextRankNo != 0 {
			t.Fatalf("final retriever trace must reflect the empty Judge selection: %#v", collector.Data.Retriever.Items)
		}
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if trace.Status != "completed" || trace.TaskCount != 1 || trace.CandidateCount != 3 || len(trace.Tasks) != 1 {
		t.Fatalf("judge batch trace must survive all-pending early return: %#v", trace)
	}
	if !trace.DeferredHandoff || len(trace.DeferredTaskIDs) != 1 {
		t.Fatalf("all-pending handoff must retain the exact deferred Task: %#v", trace)
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) != 1 || plan.TaskPlans[0].TaskID != trace.DeferredTaskIDs[0] ||
		plan.TaskPlans[0].Output != runtimeKnowledgeDeferredHandoffOutput || plan.TaskPlans[0].OutputKind != "handoff" || plan.TaskPlans[0].ReplyRequired {
		t.Fatalf("all-pending handoff must persist a recoverable non-text TaskPlan: plan=%#v trace=%#v", plan, trace)
	}
	taskTrace := trace.Tasks[0]
	if taskTrace.CandidateCount != 3 || taskTrace.Decision != knowledgeEvidenceDecisionInsufficient || taskTrace.DecisionSource != "model" || len(taskTrace.Layers) != 2 {
		t.Fatalf("unexpected all-pending task trace: %#v", taskTrace)
	}
	if taskTrace.Layers[0].Layer != knowledgeEvidenceLayerStore || taskTrace.Layers[0].CandidateCount != 2 || taskTrace.Layers[0].DecisionSource != "model" || strings.Join(taskTrace.Layers[0].MissingAspects, "|") != "处理方式" {
		t.Fatalf("unexpected store layer trace: %#v", taskTrace.Layers[0])
	}
	if taskTrace.Layers[1].Layer != knowledgeEvidenceLayerGeneral || taskTrace.Layers[1].CandidateCount != 1 || taskTrace.Layers[1].DecisionSource != "model" || strings.Join(taskTrace.Layers[1].MissingAspects, "|") != "门店适用范围" {
		t.Fatalf("unexpected general layer trace: %#v", taskTrace.Layers[1])
	}
}

func TestApplyKnowledgeEvidenceJudgeOutcomeKeepsTaskAndLayerTraceBoundaries(t *testing.T) {
	storeHit := rag.RetrieveResult{KnowledgeBaseID: 1, ChunkID: 101, Content: "问题：有外卖机器人吗\n答案：有外卖机器人的。", Score: 0.92}
	generalHit := rag.RetrieveResult{KnowledgeBaseID: 2, ChunkID: 201, Content: "问题：外卖怎么取\n答案：请根据门店实际情况处理。", Score: 0.86}
	batch := &runtimeKnowledgeRetrieveBatch{
		Questions: []runtimeKnowledgeQuestionResult{{
			TaskID: "T1",
			Query:  "有外卖机器人吗，能送到房间吗",
			Result: &retrievers.KnowledgeRetrieveResult{
				KnowledgeBaseIDs: []int64{1, 2},
				RawHits:          []rag.RetrieveResult{storeHit, generalHit},
				Hits:             []rag.RetrieveResult{storeHit, generalHit},
				ContextResults:   []rag.RetrieveResult{storeHit, generalHit},
				ContextText:      storeHit.Content + "\n" + generalHit.Content,
			},
		}},
	}
	batch.Merged = mergeRuntimeKnowledgeQuestionResults([]int64{1, 2}, retrievers.DefaultKnowledgeRetrieveOptions(), "有外卖机器人吗，能送到房间吗", batch.Questions)
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "有外卖机器人吗，能送到房间吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: storeHit},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerGeneral, Hit: generalHit},
		},
	}}
	outcome := knowledgeEvidenceJudgeOutcome{
		Applied: true,
		Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
			"T1": {
				knowledgeEvidenceLayerStore: {
					Decision:             knowledgeEvidenceDecisionPartial,
					DecisionSource:       "model",
					SelectedCandidateIDs: []string{"T1C1"},
					SupportedFacts: []knowledgeEvidenceFact{{
						FactID: "T1F1", Aspect: "existence", Statement: "有外卖机器人的。",
					}},
					MissingAspects: []string{"配送范围"},
				},
				knowledgeEvidenceLayerGeneral: {
					Decision:       knowledgeEvidenceDecisionInsufficient,
					DecisionSource: "model",
					MissingAspects: []string{"门店是否配置", "配送范围"},
				},
			},
		},
		Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
			SchemaVersion:  knowledgeEvidenceJudgeSchemaVersion,
			Status:         "completed",
			TaskCount:      1,
			CandidateCount: 2,
		},
	}

	trace := applyKnowledgeEvidenceJudgeOutcome(batch, tasks, outcome)
	if len(trace.Tasks) != 1 {
		t.Fatalf("expected one task trace, got %#v", trace.Tasks)
	}
	taskTrace := trace.Tasks[0]
	if taskTrace.SelectedLayer != knowledgeEvidenceLayerStore || taskTrace.Decision != knowledgeEvidenceDecisionPartial || taskTrace.DecisionSource != "model" || taskTrace.CandidateCount != 2 {
		t.Fatalf("unexpected selected task trace: %#v", taskTrace)
	}
	if len(taskTrace.SupportedFacts) != 1 || taskTrace.SupportedFacts[0].Aspect != "existence" || strings.Join(taskTrace.MissingAspects, "|") != "配送范围" {
		t.Fatalf("task trace must keep only the selected store fact boundary: %#v", taskTrace)
	}
	if len(taskTrace.Layers) != 2 {
		t.Fatalf("expected store and general layer traces, got %#v", taskTrace.Layers)
	}
	if len(taskTrace.Layers[0].SupportedFacts) != 1 || strings.Join(taskTrace.Layers[0].MissingAspects, "|") != "配送范围" {
		t.Fatalf("store layer fact boundary was not preserved: %#v", taskTrace.Layers[0])
	}
	if len(taskTrace.Layers[1].SupportedFacts) != 0 || strings.Join(taskTrace.Layers[1].MissingAspects, "|") != "门店是否配置|配送范围" {
		t.Fatalf("general layer must keep its own missing aspects: %#v", taskTrace.Layers[1])
	}
}

func TestKnowledgePolicyDefersUnavailableRetrieverWithoutSwallowingResourceSibling(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", Text: "早餐几点", OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
		{TaskID: "task-2", Intent: "hotel_variable", Text: "定位发我", OutputKind: "resource", Output: "structured_resource_commit", ResourceAction: "provide_location"},
	}})
	summary := &RunResult{}

	state, err := newTestKnowledgePolicyGate(nil).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点，定位发我", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.AnswerabilityStatus != answerabilityStatusUnanswerable {
		t.Fatalf("unexpected answerability status: %q", state.AnswerabilityStatus)
	}
	if summary.handoffDirective {
		t.Fatalf("unavailable retriever must not create a global handoff for a mixed resource run: %#v", summary)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if !trace.DeferredHandoff || len(trace.DeferredTaskIDs) != 1 || trace.DeferredTaskIDs[0] != "task-1" {
		t.Fatalf("knowledge task must be deferred when the retriever is unavailable, got %#v", trace)
	}
	if len(trace.Tasks) != 1 || trace.Tasks[0].TaskID != "task-1" ||
		trace.Tasks[0].Disposition != runtimeKnowledgeDispositionNoEvidenceHandoff ||
		trace.Tasks[0].DecisionSource != "source_unavailable" {
		t.Fatalf("unavailable retriever must persist an explicit Task disposition: %#v", trace.Tasks)
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) != 2 || plan.TaskPlans[0].TaskID != "task-1" || plan.TaskPlans[0].Output != runtimeKnowledgeDeferredHandoffOutput ||
		plan.TaskPlans[0].OutputKind != "handoff" || plan.TaskPlans[0].ReplyRequired || plan.TaskPlans[1].TaskID != "task-2" ||
		plan.TaskPlans[1].OutputKind != "resource" {
		t.Fatalf("resource sibling and recoverable deferred knowledge Task must both remain, got %#v", plan.TaskPlans)
	}
	if active := activeGenerationTaskPlans(callbacks.IntentTraceData{}, plan); len(active) != 0 {
		t.Fatalf("resource-only sibling must not create a Generate text task: %#v", active)
	}
}

func TestAppendRetrievedContextKeepsSkippedRuntimeActionInstruction(t *testing.T) {
	messages := []*schema.Message{schema.SystemMessage("base instruction")}
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}})
	intent := callbacks.IntentTraceData{
		PrimaryIntent:     "hotel_variable",
		MatchedIntentCode: "hotel_variable",
		SubIntent:         "location",
		ResourceAction:    "provide_location",
		NeedsResource:     true,
		ShouldReply:       true,
	}

	outcome := appendRetrievedContext(context.Background(), newKnowledgePolicyRunInput("发一下酒店定位", ""), intent, &RunResult{}, nil, gate, &messages)
	if outcome.AnswerabilityStatus != answerabilityStatusSkipped {
		t.Fatalf("unexpected answerability status: %q", outcome.AnswerabilityStatus)
	}

	if !messagesContainContent(messages, "酒店变量-定位/地址") {
		t.Fatalf("expected hotel variable instruction in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "不能说让同事发送") {
		t.Fatalf("expected anti fake coworker fallback instruction in messages: %#v", messages)
	}
}

func TestBuildLocationDirectReplyUsesCurrentAccountVariable(t *testing.T) {
	reply := buildLocationDirectReply(&models.WxWorkProtocolInstance{
		StoreNavigationName: "丽斯未来酒店合肥包河店",
		StoreAddress:        "安徽省合肥市包河大道100号",
		StoreLongitude:      "117.263908",
		StoreLatitude:       "31.824097",
	})
	if !strings.Contains(reply, "酒店") || !strings.Contains(reply, "地址") || !strings.Contains(reply, "安徽省合肥市包河大道100号") || !strings.Contains(reply, "uri.amap.com") {
		t.Fatalf("expected address and map uri in direct location reply, got %q", reply)
	}
	if strings.Contains(reply, "发你了") || strings.Contains(reply, "点开就能") {
		t.Fatalf("direct location reply must not pretend a resource was sent, got %q", reply)
	}
}

func TestBuildHotelVariableDirectReplyRoutesLocationIntent(t *testing.T) {
	reply := buildHotelVariableDirectReply(&models.WxWorkProtocolInstance{
		EmployeeName:   "丽斯未来酒店",
		StoreAddress:   "安徽省合肥市包河大道100号",
		StoreLongitude: "117.263908",
		StoreLatitude:  "31.824097",
	}, callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_variable",
		SubIntent:      "location",
		ResourceType:   "location",
		ResourceAction: "provide_location",
	}, "发一下酒店定位")
	if !strings.Contains(reply, "安徽省合肥市包河大道100号") || !strings.Contains(reply, "酒店定位") {
		t.Fatalf("expected location direct reply for location intent, got %q", reply)
	}
}

func TestBuildHotelVariableDirectReplyDoesNotInferResourcesFromMergedText(t *testing.T) {
	reply := buildHotelVariableDirectReply(&models.WxWorkProtocolInstance{
		EmployeeName:              "丽斯未来酒店",
		StoreAddress:              "安徽省合肥市包河大道100号",
		StoreLongitude:            "117.263908",
		StoreLatitude:             "31.824097",
		DefaultMiniProgramPayload: `{"title":"安心宿入住小程序"}`,
	}, callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_variable",
		SubIntent:      "location",
		ResourceType:   "location",
		ResourceAction: "provide_location",
	}, "定位发我一个\n我要办理入住")
	if !strings.Contains(reply, "酒店定位") {
		t.Fatalf("expected location direct reply, got %q", reply)
	}
	if strings.Contains(reply, "入住小程序入口") {
		t.Fatalf("must not infer mini program from merged text when intent action is location only, got %q", reply)
	}
}

func TestKnowledgePolicyKeepsHotelVariableInstructionWhenMixedKnowledgeRetrieves(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, ChunkID: 101, Title: "停车", Content: "停车免费，地上停车场从繁华大道辅路进。", Score: 0.91},
			},
			ContextResults: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, ChunkID: 101, Title: "停车", Content: "停车免费，地上停车场从繁华大道辅路进。", Score: 0.91},
			},
			ContextText: "停车免费，地上停车场从繁华大道辅路进。",
			AnswerMode:  enums.KnowledgeAnswerModeStrict,
		},
	})
	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("定位和入住小程序都发我，顺便问下停车", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent: callbacks.IntentTraceData{
			PrimaryIntent:     "hotel_variable",
			MatchedIntentCode: "hotel_variable",
			SubIntent:         "location",
			ResourceAction:    "provide_location",
			NeedsResource:     true,
			NeedsKnowledge:    true,
			ShouldReply:       true,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.SkipGate {
		t.Fatalf("expected mixed variable+knowledge to retrieve, got skip")
	}
	if len(state.Decision.Instructions) == 0 {
		t.Fatalf("expected mixed variable+knowledge instruction, got %#v", state.Decision.Instructions)
	}
	first := state.Decision.Instructions[0].Content
	if !strings.Contains(first, "Commit 阶段") || !strings.Contains(first, "本阶段只回答停车") {
		t.Fatalf("expected mixed variable+knowledge instruction to leave variables for commit, got %#v", state.Decision.Instructions)
	}
	if strings.Contains(first, "酒店变量-定位/地址") || strings.Contains(first, "酒店变量-入住小程序") {
		t.Fatalf("mixed knowledge generation must not expose variable details to Generate, got %#v", state.Decision.Instructions)
	}
}

func TestBuildRunMessagesUsesDeterministicReplyForAmbiguousConfirm(t *testing.T) {
	summary := &RunResult{}
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	gate := newTestKnowledgePolicyGate(retriever)

	_ = buildRunMessages(context.Background(), newKnowledgePolicyRunInput("确认确认", "1"), summary, nil, gate)

	if summary.ReplyText != "" {
		t.Fatalf("expected model-generated confirm reply, got fixed reply %q", summary.ReplyText)
	}
	if retriever.called {
		t.Fatal("expected retriever not to run")
	}
}

func TestKnowledgePolicyEvaluateDoesNotFallbackWhenHitsHaveNoContextText(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, DocumentID: 10, ChunkID: 101, Content: "入住办理在小程序里。", Score: 0.2},
			},
		},
	})

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("你能干啥啊", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if len(state.Decision.Instructions) != 1 {
		t.Fatalf("expected one no-context instruction, got %d", len(state.Decision.Instructions))
	}
	assertNoFixedFallbackSource(t, state.Decision.Instructions[0].Content)
	if !strings.Contains(state.Decision.Instructions[0].Content, "回复运行时决策") {
		t.Fatalf("expected runtime-engine no-context policy, got %q", state.Decision.Instructions[0].Content)
	}
}

func TestKnowledgePolicyEvaluateInjectsGroundedInstructionAndContext(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, DocumentID: 10, ChunkID: 101, Content: "早餐时间是 7:00-9:30。", Score: 0.91},
			},
			ContextResults: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, DocumentID: 10, ChunkID: 101, Content: "早餐时间是 7:00-9:30。", Score: 0.91},
			},
			ContextText: "知识库片段：早餐时间是 7:00-9:30。",
			AnswerMode:  enums.KnowledgeAnswerModeStrict,
		},
	})

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点", "1"),
		Summary:   &RunResult{},
		Intent:    hotelInfoIntent(),
		Collector: collector,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if len(state.Decision.Instructions) != 1 {
		t.Fatalf("expected one grounded instruction, got %d", len(state.Decision.Instructions))
	}
	if !strings.Contains(state.Decision.Instructions[0].Content, "知识库回答约束") {
		t.Fatalf("unexpected grounded instruction: %q", state.Decision.Instructions[0].Content)
	}
	if collector.Data.Answerability.Status != answerabilityStatusHasContext {
		t.Fatalf("unexpected policy status: %q", collector.Data.Answerability.Status)
	}
}

func TestBuildRunMessagesInjectsRetrievedContextWhenHasContext(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 200, MatchMode: "keyword", Keywords: "早餐", NeedsKnowledge: true, Status: enums.StatusOk})
	summary := &RunResult{}
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, DocumentID: 10, ChunkID: 101, Content: "早餐时间是 7:00-9:30。", Score: 0.91},
			},
			ContextText: "知识库片段：早餐时间是 7:00-9:30。",
			AnswerMode:  enums.KnowledgeAnswerModeStrict,
		},
	})

	messages := make([]*schema.Message, 0)
	req := newKnowledgePolicyRunInput("早餐几点", "1")
	outcome := appendRetrievedContext(context.Background(), req, hotelInfoIntent(), summary, nil, gate, &messages)
	messages = append(messages, schema.UserMessage(req.UserMessage.Content))
	if outcome.AnswerabilityStatus != answerabilityStatusHasContext {
		t.Fatalf("unexpected answerability status: %q", outcome.AnswerabilityStatus)
	}

	if summary.ReplyText != "" {
		t.Fatalf("expected no fallback, got %q", summary.ReplyText)
	}
	if !messagesContainContent(messages, "知识库回答约束") {
		t.Fatalf("expected knowledge instruction in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "早餐时间") {
		t.Fatalf("expected retrieved context in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "早餐几点") {
		t.Fatalf("expected current user message in messages: %#v", messages)
	}
}

func TestKnowledgePolicyUsesRuntimeFallbackForHumanDecision(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(retriever)

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("怎么退款", "1"),
		Collector: collector,
		Intent:    humanRiskIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !state.SkipGate {
		t.Fatal("refund should skip knowledge and let the model respond from action instruction")
	}
	if len(state.Decision.Instructions) == 0 || !strings.Contains(state.Decision.Instructions[0].Content, "人工/投诉/风险") {
		t.Fatalf("expected human decision instruction, got %#v", state.Decision.Instructions)
	}
	if retriever.called {
		t.Fatal("refund should not let the model answer from knowledge directly")
	}
	if collector.Data.Answerability.Reason != "intent does not require knowledge" {
		t.Fatalf("unexpected reason: %q", collector.Data.Answerability.Reason)
	}
}

func TestKnowledgePolicyEvaluateSkipsWhenNoKnowledgeConfigured(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{}
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(retriever)

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("你好", ""),
		Collector: collector,
		Intent:    socialConfirmIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if !state.SkipGate {
		t.Fatal("expected conversational intent to skip knowledge and continue to model")
	}
	if retriever.called {
		t.Fatal("expected retriever not to run without configured knowledge")
	}
	if collector.Data.Answerability.Status != answerabilityStatusSkipped {
		t.Fatalf("unexpected status: %q", collector.Data.Answerability.Status)
	}
}

func TestKnowledgePolicyEvaluateUsesRuntimeActionFallback(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(retriever)

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("帮我转人工", "1"),
		Collector: collector,
		Intent:    humanRiskIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if !state.SkipGate {
		t.Fatal("expected runtime action to skip knowledge and continue to model")
	}
	if len(state.Decision.Instructions) == 0 || !strings.Contains(state.Decision.Instructions[0].Content, "人工/投诉/风险") {
		t.Fatalf("expected handoff instruction, got %#v", state.Decision.Instructions)
	}
	if retriever.called {
		t.Fatal("expected retriever not to run for runtime action")
	}
	if collector.Data.Answerability.Status != answerabilityStatusSkipped {
		t.Fatalf("unexpected status: %q", collector.Data.Answerability.Status)
	}
}

func TestKnowledgePolicyEvaluatePersistsSourceUnavailableDispositionOnRetrievalError(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		err:              errors.New("vector store unavailable"),
	})
	summary := &RunResult{}

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if len(state.Decision.Instructions) != 1 {
		t.Fatalf("expected one retrieval-error instruction, got %d", len(state.Decision.Instructions))
	}
	if !strings.Contains(state.Decision.Instructions[0].Content, "知识库检索暂时不可用") {
		t.Fatalf("unexpected retrieval-error instruction: %q", state.Decision.Instructions[0].Content)
	}
	if collector.Data.Answerability.Status != answerabilityStatusUnanswerable {
		t.Fatalf("unexpected status: %q", collector.Data.Answerability.Status)
	}
	if collector.Data.Answerability.Reason != "knowledge retrieval failed" {
		t.Fatalf("unexpected reason: %q", collector.Data.Answerability.Reason)
	}
	if !summary.handoffDirective || summary.handoffDirectiveSource != "knowledge_no_context" {
		t.Fatalf("a pure knowledge source failure must enter the real handoff path: %#v", summary)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if !trace.DeferredHandoff || len(trace.DeferredTaskIDs) != 1 || len(trace.Tasks) != 1 ||
		trace.Tasks[0].Disposition != runtimeKnowledgeDispositionNoEvidenceHandoff ||
		trace.Tasks[0].DecisionSource != "source_unavailable" {
		t.Fatalf("retrieval failure must persist an explicit per-task source disposition: %#v", trace)
	}
}

func TestAppendRetrievedContextRequestsHandoffWhenRetrievalFails(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 200, MatchMode: "keyword", Keywords: "早餐", NeedsKnowledge: true, Status: enums.StatusOk})
	summary := &RunResult{}
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		err:              errors.New("vector store unavailable"),
	})

	messages := make([]*schema.Message, 0)
	req := newKnowledgePolicyRunInput("早餐几点", "1")
	outcome := appendRetrievedContext(context.Background(), req, hotelInfoIntent(), summary, nil, gate, &messages)
	messages = append(messages, schema.UserMessage(req.UserMessage.Content))
	if outcome.AnswerabilityStatus != answerabilityStatusUnanswerable {
		t.Fatalf("unexpected answerability status: %q", outcome.AnswerabilityStatus)
	}

	if summary.ReplyText != "" {
		t.Fatalf("expected no early fallback reply, got %q", summary.ReplyText)
	}
	if !summary.handoffDirective || summary.handoffDirectiveSource != "knowledge_no_context" {
		t.Fatalf("retrieval failure must not continue as an ungrounded hotel answer: %#v", summary)
	}
	if !messagesContainContent(messages, "知识库检索暂时不可用") {
		t.Fatalf("expected retrieval-error instruction in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "早餐几点") {
		t.Fatalf("expected current user message to remain in messages: %#v", messages)
	}
}

func TestRuntimeKnowledgeResourceTaskIDsFollowSelectedQuestionEvidence(t *testing.T) {
	batch := &runtimeKnowledgeRetrieveBatch{Questions: []runtimeKnowledgeQuestionResult{
		{
			TaskID: "T1",
			Result: &retrievers.KnowledgeRetrieveResult{ContextResults: []rag.RetrieveResult{
				{KnowledgeBaseID: 11, SourceRecordID: "source-a"},
				{KnowledgeBaseID: 11, SourceRecordID: "source-b"},
			}},
		},
		{
			TaskID: "T2",
			Result: &retrievers.KnowledgeRetrieveResult{ContextResults: []rag.RetrieveResult{
				{KnowledgeBaseID: 11, SourceRecordID: "source-a"},
			}},
		},
	}}

	if got := runtimeKnowledgeResourceTaskIDs(batch, 11, "source-a"); strings.Join(got, ",") != "T1,T2" {
		t.Fatalf("shared knowledge resource Task ownership=%#v, want T1,T2", got)
	}
	if got := runtimeKnowledgeResourceTaskIDs(batch, 11, "source-b"); len(got) != 1 || got[0] != "T1" {
		t.Fatalf("single-task knowledge resource ownership=%#v, want T1", got)
	}
	if got := runtimeKnowledgeResourceTaskIDs(batch, 12, "source-a"); len(got) != 0 {
		t.Fatalf("knowledge resource ownership crossed knowledge-base scope: %#v", got)
	}
}
