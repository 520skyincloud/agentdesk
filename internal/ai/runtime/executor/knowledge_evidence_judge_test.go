package executor

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"

	"github.com/cloudwego/eino/schema"
)

const (
	knowledgeEvidenceClassificationDirect     = "direct"
	knowledgeEvidenceClassificationSupporting = "supporting"
	knowledgeEvidenceClassificationUnrelated  = "unrelated"
)

type fakeKnowledgeEvidenceJudge struct {
	calls   int
	tasks   []knowledgeEvidenceJudgeTask
	outcome func([]knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome
}

func (j *fakeKnowledgeEvidenceJudge) JudgeBatch(_ context.Context, _ RunInput, tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
	j.calls++
	j.tasks = append([]knowledgeEvidenceJudgeTask(nil), tasks...)
	if j.outcome != nil {
		return j.outcome(tasks)
	}
	return knowledgeEvidenceJudgeOutcome{
		Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
			SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
			Status:        "fallback",
		},
	}
}

func TestKnowledgeEvidenceJudgeSelectsGeneralDirectAnswerOverUnrelatedStoreHit(t *testing.T) {
	storeHit := judgeTestHit(1, 101, "空调不制冷", "问题：空调不制冷怎么办\n答案：转接", 0.96)
	generalHit := judgeTestHit(2, 201, "客房设施", "问题：房间有空调吗\n答案：客房内配有空调。", 0.84)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"有空调不": judgeTestRetrieveResult(storeHit, generalHit),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return completedJudgeOutcome(tasks, map[string][]string{
			"T1": {knowledgeEvidenceClassificationUnrelated, knowledgeEvidenceClassificationDirect},
		})
	}}
	collector := callbacks.NewRuntimeTraceCollector()
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("有空调不", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if judge.calls != 1 || len(judge.tasks) != 1 {
		t.Fatalf("expected one judge batch with one task, calls=%d tasks=%#v", judge.calls, judge.tasks)
	}
	if state.RetrieveResult == nil || len(state.RetrieveResult.Hits) != 1 || state.RetrieveResult.Hits[0].KnowledgeBaseID != 2 {
		t.Fatalf("expected general direct answer to win, got %#v", state.RetrieveResult)
	}
	if state.Input.Summary.handoffDirective {
		t.Fatal("unrelated store transfer directive must not trigger handoff")
	}
	if !strings.Contains(state.RetrieveResult.ContextText, "客房内配有空调") {
		t.Fatalf("expected selected general answer in context, got %q", state.RetrieveResult.ContextText)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if trace.Status != "completed" || len(trace.Tasks) != 1 || trace.Tasks[0].SelectedLayer != knowledgeEvidenceLayerGeneral {
		t.Fatalf("unexpected judge trace: %#v", trace)
	}
}

func TestKnowledgeEvidenceJudgeKeepsStorePriorityWhenBothLayersDirectlyAnswer(t *testing.T) {
	storeHit := judgeTestHit(1, 101, "门店早餐", "问题：早餐几点\n答案：南七店早餐时间为7:00-9:30。", 0.75)
	generalHit := judgeTestHit(2, 201, "通用早餐", "问题：早餐几点\n答案：通常为7:00-10:00。", 0.99)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"早餐几点": judgeTestRetrieveResult(storeHit, generalHit),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return completedJudgeOutcome(tasks, map[string][]string{
			"T1": {knowledgeEvidenceClassificationDirect, knowledgeEvidenceClassificationDirect},
		})
	}}
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("早餐几点", "1"),
		Summary: &RunResult{},
		Intent:  hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.RetrieveResult == nil || len(state.RetrieveResult.Hits) != 1 || state.RetrieveResult.Hits[0].KnowledgeBaseID != 1 {
		t.Fatalf("store direct answer must win even with a lower score, got %#v", state.RetrieveResult)
	}
	if !strings.Contains(state.RetrieveResult.ContextText, "7:00-9:30") || strings.Contains(state.RetrieveResult.ContextText, "7:00-10:00") {
		t.Fatalf("expected only store evidence in context, got %q", state.RetrieveResult.ContextText)
	}
}

func TestKnowledgeEvidenceJudgeClearsQuestionWhenNeitherLayerDirectlyAnswers(t *testing.T) {
	storeHit := judgeTestHit(1, 101, "浴巾", "问题：浴巾在哪里\n答案：浴巾放在房间衣柜内。", 0.66)
	generalHit := judgeTestHit(2, 201, "用品", "问题：可以补充用品吗\n答案：可以联系同事。", 0.61)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"汤东强是谁": judgeTestRetrieveResult(storeHit, generalHit),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return completedJudgeOutcome(tasks, map[string][]string{
			"T1": {knowledgeEvidenceClassificationUnrelated, knowledgeEvidenceClassificationSupporting},
		})
	}}
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("汤东强是谁", "1"),
		Summary: &RunResult{},
		Intent:  hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.RetrieveResult == nil || len(state.RetrieveResult.Hits) != 0 || strings.TrimSpace(state.RetrieveResult.ContextText) != "" {
		t.Fatalf("expected no selected evidence, got %#v", state.RetrieveResult)
	}
	if state.AnswerabilityStatus != answerabilityStatusNoContext {
		t.Fatalf("expected no-context routing after a completed no-direct decision, got %q", state.AnswerabilityStatus)
	}
}

func TestKnowledgeEvidenceJudgeFailureDoesNotExposeLowScoreUnselectedRetrieval(t *testing.T) {
	storeHit := judgeTestHit(1, 101, "门店答案", "问题：早餐几点\n答案：南七店早餐时间为7:00-9:30。", 0.60)
	generalHit := judgeTestHit(2, 201, "通用答案", "问题：早餐几点\n答案：通常为7:00-10:00。", 0.99)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"早餐几点": judgeTestRetrieveResult(storeHit, generalHit),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(_ []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return knowledgeEvidenceJudgeOutcome{Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
			SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
			Status:        "fallback",
			Reason:        "simulated timeout",
		}}
	}}
	collector := callbacks.NewRuntimeTraceCollector()
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("judge failure must not fail the reply path: %v", err)
	}
	if state.RetrieveResult == nil || len(state.RetrieveResult.Hits) != 0 || strings.TrimSpace(state.RetrieveResult.ContextText) != "" {
		t.Fatalf("judge failure must not expose unselected retrieval to Generate, got %#v", state.RetrieveResult)
	}
	if len(state.RetrieveResult.RawHits) != 2 {
		t.Fatalf("raw hits must remain available for diagnostics, got %#v", state.RetrieveResult.RawHits)
	}
	if state.AnswerabilityStatus != answerabilityStatusNoContext || !state.Input.Summary.handoffDirective {
		t.Fatalf("judge failure must use the existing real handoff route, status=%q summary=%#v", state.AnswerabilityStatus, state.Input.Summary)
	}
	if collector.Data.Pipeline.EvidenceJudge.Status != "fallback" || len(collector.Data.Pipeline.EvidenceJudge.Tasks) != 1 || collector.Data.Pipeline.EvidenceJudge.Tasks[0].Decision != knowledgeEvidenceDecisionInsufficient {
		t.Fatalf("expected fallback trace, got %#v", collector.Data.Pipeline.EvidenceJudge)
	}
}

func TestKnowledgeEvidenceJudgeFailureKeepsSafeTasksWithoutExposingUnsafeOnes(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{
		{
			TaskID: "T1",
			Query:  "房间有几瓶矿泉水",
			Candidates: []knowledgeEvidenceJudgeCandidate{{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "矿泉水", "问题：房间有几瓶矿泉水\n答案：房间内有两瓶矿泉水，都是免费的。", 0.96),
			}},
		},
		{
			TaskID: "T2",
			Query:  "汤东强是谁",
			Candidates: []knowledgeEvidenceJudgeCandidate{{
				CandidateID: "T2C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 102, "浴巾", "问题：浴巾在哪里\n答案：浴巾放在房间衣柜内。", 0.66),
			}},
		},
	}

	selections, grounded, handoffs := deterministicKnowledgeEvidenceJudgeFallbackSelections(tasks)
	if grounded != 1 || handoffs != 0 {
		t.Fatalf("expected one safe deterministic answer, grounded=%d handoffs=%d", grounded, handoffs)
	}
	if got := selections["T1"][knowledgeEvidenceLayerStore]; got.Decision != knowledgeEvidenceDecisionDirectSingle || len(got.SupportedFacts) == 0 {
		t.Fatalf("safe exact FAQ should remain answerable: %#v", got)
	}
	if got := selections["T2"][knowledgeEvidenceLayerStore]; got.Decision != knowledgeEvidenceDecisionInsufficient || len(got.SelectedCandidateIDs) != 0 {
		t.Fatalf("unrelated candidate must remain withheld: %#v", got)
	}
}

func TestKnowledgeEvidenceJudgeFailurePreservesIndependentMiniProgramCommit(t *testing.T) {
	db := setupRuntimeIntentConfigTestDB(t)
	conversation := models.Conversation{ID: 88, CustomerID: 901}
	instance := models.WxWorkProtocolInstance{
		ID:                        990,
		DefaultMiniProgramPayload: `{"title":"入住小程序"}`,
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatalf("create wxwork instance: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		ConversationID:   conversation.ID,
		WxWorkInstanceID: instance.ID,
	}).Error; err != nil {
		t.Fatalf("create conversation route: %v", err)
	}

	storeHit := judgeTestHit(1, 101, "门店早餐", "问题：早餐几点\n答案：7:00-9:30。", 0.82)
	generalHit := judgeTestHit(2, 201, "通用早餐", "问题：早餐几点\n答案：7:00-10:00。", 0.93)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"早餐几点": judgeTestRetrieveResult(storeHit, generalHit),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(_ []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return knowledgeEvidenceJudgeOutcome{Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
			SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
			Status:        "fallback",
			Reason:        "simulated timeout",
		}}
	}}
	intent := callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_variable",
		SubIntent:      "mini_program",
		NeedsKnowledge: true,
		NeedsResource:  true,
		ShouldReply:    true,
		ResourceAction: "provide_mini_program",
		ResourceActions: []string{
			"provide_mini_program",
		},
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "hotel_info", SubIntent: "breakfast", Text: "早餐几点", NeedsKnowledge: true},
			{Intent: "hotel_variable", SubIntent: "mini_program", Text: "发入住小程序", NeedsResource: true, ResourceAction: "provide_mini_program"},
		},
	}
	summary := &RunResult{}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = intent
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{
		ActiveTaskCount:        2,
		ReplyRequiredTaskCount: 1,
		TaskPlans: []callbacks.ReplyTaskPlanTraceData{
			{TaskID: "T1", Intent: "hotel_info", SubIntent: "breakfast", Text: "早餐几点", OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
			{TaskID: "T2", Intent: "hotel_variable", SubIntent: "mini_program", Text: "发入住小程序", OutputKind: "resource", Output: "structured_resource_commit", ResourceAction: "provide_mini_program"},
		},
	})
	req := newKnowledgePolicyRunInput("早餐几点，发入住小程序", "1")
	req.Conversation = conversation

	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   req,
		Summary:   summary,
		Collector: collector,
		Intent:    intent,
	})
	if err != nil {
		t.Fatalf("judge failure must not fail the mixed resource path: %v", err)
	}
	if state.RetrieveResult == nil || len(state.RetrieveResult.Hits) != 0 || strings.TrimSpace(state.RetrieveResult.ContextText) != "" {
		t.Fatalf("judge failure must not expose unselected knowledge to Generate, got %#v", state.RetrieveResult)
	}
	if summary.handoffDirective {
		t.Fatalf("independent resource must commit before deferred handoff, got %#v", summary)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if !trace.DeferredHandoff || len(trace.DeferredTaskIDs) != 1 || trace.DeferredTaskIDs[0] != "T1" {
		t.Fatalf("expected only the knowledge task to be deferred, got %#v", trace)
	}
	activePlan := collector.Data.Pipeline.ReplyPlan
	if len(activePlan.TaskPlans) != 1 || activePlan.TaskPlans[0].TaskID != "T2" || activePlan.TaskPlans[0].Output != "structured_resource_commit" {
		t.Fatalf("expected only the mini-program resource task to remain active, got %#v", activePlan.TaskPlans)
	}
	if runtimeReplyPlanRequiresGeneratedText(activePlan) {
		t.Fatalf("resource-only active plan must not require Generate, got %#v", activePlan)
	}
	if !prepareHotelVariableDirectCommit(req, summary, collector) {
		t.Fatal("configured mini-program must remain eligible for structured commit")
	}
	if strings.TrimSpace(summary.ReplyText) != "" {
		t.Fatalf("structured mini-program commit must not be replaced by fallback text, got %q", summary.ReplyText)
	}
}

func TestKnowledgeEvidenceJudgeFailurePreservesStoreHandoffBoundary(t *testing.T) {
	storeHit := judgeTestHit(1, 101, "马桶故障", "问题：马桶堵了怎么办\n答案：转接", 0.82)
	generalHit := judgeTestHit(2, 201, "通用处理", "问题：马桶堵了怎么办\n答案：可以自行疏通。", 0.99)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"马桶堵了怎么办": judgeTestRetrieveResult(storeHit, generalHit),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(_ []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return knowledgeEvidenceJudgeOutcome{Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
			SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
			Status:        "fallback",
			Reason:        "simulated timeout",
		}}
	}}
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("马桶堵了怎么办", "1"),
		Summary: &RunResult{},
		Intent:  hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("judge failure must not fail the reply path: %v", err)
	}
	if !state.Input.Summary.handoffDirective || state.AnswerabilityStatus != answerabilityStatusSkipped {
		t.Fatalf("store handoff directive must remain authoritative during fallback, status=%q summary=%#v", state.AnswerabilityStatus, state.Input.Summary)
	}
}

func TestKnowledgeEvidenceJudgeBatchesAllConflictingAtomicQuestionsOnce(t *testing.T) {
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"有空调不": judgeTestRetrieveResult(
			judgeTestHit(1, 101, "空调故障", "问题：空调不制冷\n答案：转接", 0.96),
			judgeTestHit(2, 201, "空调配置", "问题：有空调吗\n答案：有空调。", 0.84),
		),
		"早餐几点": judgeTestRetrieveResult(
			judgeTestHit(1, 102, "门店早餐", "问题：早餐几点\n答案：7:00-9:30。", 0.82),
			judgeTestHit(2, 202, "通用早餐", "问题：早餐几点\n答案：7:00-10:00。", 0.93),
		),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return completedJudgeOutcome(tasks, map[string][]string{
			"T1": {knowledgeEvidenceClassificationUnrelated, knowledgeEvidenceClassificationDirect},
			"T2": {knowledgeEvidenceClassificationDirect, knowledgeEvidenceClassificationDirect},
		})
	}}
	intent := hotelInfoIntent()
	intent.IntentTasks = []callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", Text: "有空调不", NeedsKnowledge: true},
		{Intent: "hotel_info", Text: "早餐几点", NeedsKnowledge: true},
	}
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("有空调不，早餐几点", "1"),
		Summary: &RunResult{},
		Intent:  intent,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if judge.calls != 1 || len(judge.tasks) != 2 {
		t.Fatalf("expected exactly one judge call for both tasks, calls=%d tasks=%d", judge.calls, len(judge.tasks))
	}
	if state.RetrieveResult == nil || !strings.Contains(state.RetrieveResult.ContextText, "有空调") || !strings.Contains(state.RetrieveResult.ContextText, "7:00-9:30") {
		t.Fatalf("expected independently selected evidence for both questions, got %#v", state.RetrieveResult)
	}
	if strings.Contains(state.RetrieveResult.ContextText, "7:00-10:00") || strings.Contains(state.RetrieveResult.ContextText, "空调不制冷") {
		t.Fatalf("unexpected losing-layer evidence in context: %q", state.RetrieveResult.ContextText)
	}
}

func TestBuildKnowledgeEvidenceJudgeTasksCapsLargeBatchAndKeepsEveryTaskLayer(t *testing.T) {
	batch := &runtimeKnowledgeRetrieveBatch{Questions: make([]runtimeKnowledgeQuestionResult, 0, 8)}
	for taskIndex := 0; taskIndex < 8; taskIndex++ {
		hits := make([]rag.RetrieveResult, 0, 8)
		for candidateIndex := 0; candidateIndex < 8; candidateIndex++ {
			knowledgeBaseID := int64(1)
			if candidateIndex >= 4 {
				knowledgeBaseID = 2
			}
			hits = append(hits, judgeTestHit(
				knowledgeBaseID,
				int64((taskIndex+1)*100+candidateIndex+1),
				fmt.Sprintf("任务%d候选%d", taskIndex+1, candidateIndex+1),
				fmt.Sprintf("问题：任务%d\n答案：候选%d", taskIndex+1, candidateIndex+1),
				float32(1)-float32(candidateIndex)/100,
			))
		}
		batch.Questions = append(batch.Questions, runtimeKnowledgeQuestionResult{
			TaskID: fmt.Sprintf("T%d", taskIndex+1),
			Query:  fmt.Sprintf("任务%d", taskIndex+1),
			Result: &retrievers.KnowledgeRetrieveResult{RawHits: hits},
		})
	}

	tasks := buildKnowledgeEvidenceJudgeTasks(batch, []int64{1}, []int64{1, 2}, nil, "")
	if len(tasks) != 8 {
		t.Fatalf("expected all eight tasks to reach one judge batch, got %d", len(tasks))
	}
	total := 0
	for _, task := range tasks {
		total += len(task.Candidates)
		if len(task.Candidates) == 0 {
			t.Fatalf("task %s lost all candidate coverage", task.TaskID)
		}
		layers := map[string]bool{}
		for _, candidate := range task.Candidates {
			layers[candidate.Layer] = true
		}
		if !layers[knowledgeEvidenceLayerStore] || !layers[knowledgeEvidenceLayerGeneral] {
			t.Fatalf("task %s lost store/general coverage: %#v", task.TaskID, task.Candidates)
		}
	}
	if total != knowledgeEvidenceJudgeBatchCandidateBudget {
		t.Fatalf("expected %d total judge candidates, got %d", knowledgeEvidenceJudgeBatchCandidateBudget, total)
	}
}

func TestKnowledgeEvidenceJudgeValidatesQuestionWithOnlyOneKnowledgeLayer(t *testing.T) {
	storeHit := judgeTestHit(1, 101, "门店早餐", "问题：早餐几点\n答案：7:00-9:30。", 0.82)
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1, 2},
		resultsByQuery: map[string]*retrievers.KnowledgeRetrieveResult{
			"早餐几点": {
				KnowledgeBaseIDs: []int64{1, 2},
				RawHits:          []rag.RetrieveResult{storeHit},
				Hits:             []rag.RetrieveResult{storeHit},
				ContextResults:   []rag.RetrieveResult{storeHit},
				ContextText:      storeHit.Content,
			},
		},
	}
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return completedJudgeOutcome(tasks, map[string][]string{
			"T1": {knowledgeEvidenceClassificationDirect},
		})
	}}
	collector := callbacks.NewRuntimeTraceCollector()
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if judge.calls != 1 {
		t.Fatalf("single-layer retrieval must share the one batch judge call, got %d calls", judge.calls)
	}
	if state.RetrieveResult == nil || len(state.RetrieveResult.Hits) != 1 || state.RetrieveResult.Hits[0].KnowledgeBaseID != 1 {
		t.Fatalf("expected validated store answer, got %#v", state.RetrieveResult)
	}
	if collector.Data.Pipeline.EvidenceJudge.Status != "completed" {
		t.Fatalf("expected completed trace, got %#v", collector.Data.Pipeline.EvidenceJudge)
	}
}

func TestKnowledgeEvidenceJudgeDefersUnansweredQuestionWithoutDroppingAnsweredQuestion(t *testing.T) {
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"早餐几点": judgeTestRetrieveResult(
			judgeTestHit(1, 101, "门店早餐", "问题：早餐几点\n答案：7:00-9:30。", 0.82),
			judgeTestHit(2, 201, "通用早餐", "问题：早餐几点\n答案：7:00-10:00。", 0.93),
		),
		"汤东强是谁": judgeTestRetrieveResult(
			judgeTestHit(1, 102, "客房用品", "问题：浴巾在哪里\n答案：衣柜里。", 0.81),
			judgeTestHit(2, 202, "酒店用品", "问题：有吹风机吗\n答案：有。", 0.78),
		),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return completedJudgeOutcome(tasks, map[string][]string{
			"T1": {knowledgeEvidenceClassificationDirect, knowledgeEvidenceClassificationDirect},
			"T2": {knowledgeEvidenceClassificationUnrelated, knowledgeEvidenceClassificationUnrelated},
		})
	}}
	intent := hotelInfoIntent()
	intent.IntentTasks = []callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", Text: "早餐几点", NeedsKnowledge: true},
		{Intent: "hotel_info", Text: "汤东强是谁", NeedsKnowledge: true},
	}
	summary := &RunResult{}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "service_request", SubIntent: "air_conditioner", Text: "空调坏了，我住1302", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
	}})
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点，汤东强是谁", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    intent,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if judge.calls != 1 || len(judge.tasks) != 2 {
		t.Fatalf("expected one batch judge for both questions, calls=%d tasks=%d", judge.calls, len(judge.tasks))
	}
	if summary.handoffDirective {
		t.Fatal("a mixed batch must answer available questions before requesting handoff")
	}
	if state.RetrieveResult == nil || !strings.Contains(state.RetrieveResult.ContextText, "7:00-9:30") {
		t.Fatalf("answered question was dropped: %#v", state.RetrieveResult)
	}
	if strings.Contains(state.RetrieveResult.ContextText, "浴巾") || strings.Contains(state.RetrieveResult.ContextText, "吹风机") {
		t.Fatalf("unrelated evidence leaked into Generate: %q", state.RetrieveResult.ContextText)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if !trace.DeferredHandoff || len(trace.DeferredTaskIDs) != 1 || trace.DeferredTaskIDs[0] != "T2" {
		t.Fatalf("expected T2 deferred handoff, got %#v", trace)
	}
}

func TestKnowledgeEvidenceJudgeDefersLaterTransferDirectiveAfterAnsweredQuestion(t *testing.T) {
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"早餐几点": judgeTestRetrieveResult(
			judgeTestHit(1, 101, "门店早餐", "问题：早餐几点\n答案：7:00-9:30。", 0.82),
			judgeTestHit(2, 201, "通用早餐", "问题：早餐几点\n答案：7:00-10:00。", 0.93),
		),
		"马桶堵了": judgeTestRetrieveResult(
			judgeTestHit(1, 102, "马桶堵塞", "问题：马桶堵了\n答案：转接", 0.91),
			judgeTestHit(2, 202, "客房设施", "问题：房间有卫生间吗\n答案：有。", 0.67),
		),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return completedJudgeOutcome(tasks, map[string][]string{
			"T1": {knowledgeEvidenceClassificationDirect, knowledgeEvidenceClassificationDirect},
			"T2": {knowledgeEvidenceClassificationDirect, knowledgeEvidenceClassificationSupporting},
		})
	}}
	intent := hotelInfoIntent()
	intent.IntentTasks = []callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", Text: "早餐几点", NeedsKnowledge: true},
		{Intent: "service_request", Text: "马桶堵了", NeedsKnowledge: true},
	}
	summary := &RunResult{}
	collector := callbacks.NewRuntimeTraceCollector()
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点，马桶堵了", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    intent,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if summary.handoffDirective {
		t.Fatal("later transfer directive must not suppress the earlier answer")
	}
	if state.RetrieveResult == nil || !strings.Contains(state.RetrieveResult.ContextText, "7:00-9:30") || strings.Contains(state.RetrieveResult.ContextText, "转接") {
		t.Fatalf("expected only answerable evidence before deferred handoff, got %#v", state.RetrieveResult)
	}
	if !collector.Data.Pipeline.EvidenceJudge.DeferredHandoff {
		t.Fatalf("expected deferred transfer trace, got %#v", collector.Data.Pipeline.EvidenceJudge)
	}
}

func TestKnowledgeEvidenceJudgeDefersFirstTransferDirectiveWithoutDroppingLaterAnswer(t *testing.T) {
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"空调坏了，我住1302": judgeTestRetrieveResult(
			judgeTestHit(1, 101, "空调故障", "问题：空调坏了\n答案：转接", 0.91),
			judgeTestHit(2, 201, "空调设施", "问题：房间有空调吗\n答案：有。", 0.72),
		),
		"早餐几点": judgeTestRetrieveResult(
			judgeTestHit(1, 102, "门店早餐", "问题：早餐几点\n答案：酒店暂不提供早餐。", 0.88),
			judgeTestHit(2, 202, "通用早餐", "问题：早餐几点\n答案：7:00-10:00。", 0.83),
		),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return completedJudgeOutcome(tasks, map[string][]string{
			"T1": {knowledgeEvidenceClassificationDirect, knowledgeEvidenceClassificationSupporting},
			"T2": {knowledgeEvidenceClassificationDirect, knowledgeEvidenceClassificationDirect},
		})
	}}
	intent := callbacks.IntentTraceData{
		PrimaryIntent:  "service_request",
		SubIntent:      "air_conditioner",
		NeedsKnowledge: true,
		ShouldReply:    true,
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "service_request", Text: "空调坏了，我住1302", NeedsKnowledge: true},
			{Intent: "hotel_info", Text: "顺便问早餐几点", NeedsKnowledge: true},
		},
	}
	summary := &RunResult{}
	collector := callbacks.NewRuntimeTraceCollector()
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("空调坏了，我住1302，顺便问早餐几点", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    intent,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if summary.handoffDirective {
		t.Fatalf("the first deferred task must not suppress the later answer: summary=%#v state=%#v trace=%#v plan=%#v", summary, state, collector.Data.Pipeline.EvidenceJudge, collector.Data.Pipeline.ReplyPlan)
	}
	if state.RetrieveResult == nil || !strings.Contains(state.RetrieveResult.ContextText, "酒店暂不提供早餐") || strings.Contains(state.RetrieveResult.ContextText, "转接") {
		t.Fatalf("expected only the later breakfast answer in Generate context, got %#v", state.RetrieveResult)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if !trace.DeferredHandoff || len(trace.DeferredTaskIDs) != 1 || trace.DeferredTaskIDs[0] != "T1" {
		t.Fatalf("expected only T1 deferred, got %#v", trace)
	}
	activePlan := collector.Data.Pipeline.ReplyPlan
	if len(activePlan.TaskPlans) != 1 || activePlan.TaskPlans[0].Text != "顺便问早餐几点" {
		t.Fatalf("expected Generate plan to contain only the answerable breakfast task, got %#v", activePlan.TaskPlans)
	}
	if activePlan.ActiveTaskCount != 1 || activePlan.ReplyRequiredTaskCount != 1 {
		t.Fatalf("active ReplyPlan counts must match the rebuilt task set, got %#v", activePlan)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseKeepsStrictJSONAndCandidatesInLayer(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "有空调吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "问题：房间有空调吗\n答案：是的。"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "问题：客房是否配备空调\n答案：房间配备空调。"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerGeneral},
		},
	}}
	valid := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"房间有空调。","criticalValues":[]}],"missingAspects":[]},{"layer":"general","decision":"insufficient","selectedCandidateIds":[],"supportedFacts":[],"missingAspects":["没有通用证据"]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(valid, tasks)
	if err != nil {
		t.Fatalf("parse valid response: %v", err)
	}
	if parsed["T1"][knowledgeEvidenceLayerStore].Decision != knowledgeEvidenceDecisionDirectCombined || len(parsed["T1"][knowledgeEvidenceLayerStore].SelectedCandidateIDs) != 2 {
		t.Fatalf("unexpected parsed classifications: %#v", parsed)
	}
	if facts := parsed["T1"][knowledgeEvidenceLayerStore].SupportedFacts; len(facts) != 1 || facts[0].Aspect != "existence" {
		t.Fatalf("expected validated supported facts, got %#v", facts)
	}
	wrapped := []string{
		"```json\n" + valid + "\n```",
		"```\n" + valid + "\n```",
		strconv.Quote(valid),
		strconv.Quote("```json\n" + valid + "\n```"),
	}
	for index, raw := range wrapped {
		got, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
		if err != nil {
			t.Fatalf("wrapped valid response %d was rejected: %v", index, err)
		}
		if got["T1"][knowledgeEvidenceLayerStore].Decision != knowledgeEvidenceDecisionDirectCombined {
			t.Fatalf("wrapped valid response %d changed the parsed decision: %#v", index, got)
		}
	}
	validWithUnknownField := strings.TrimSuffix(valid, "}") + `,"explanation":"extra"}`

	invalid := []string{
		`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"]},{"layer":"general","decision":"insufficient","selectedCandidateIds":[]}]}],"explanation":"extra"}`,
		"回答如下：\n```json\n" + valid + "\n```",
		"```javascript\n" + valid + "\n```",
		"```json\n" + valid + "\n```\n解释",
		strconv.Quote(strconv.Quote(valid)),
		"```json\n" + validWithUnknownField + "\n```",
		strconv.Quote(validWithUnknownField),
	}
	for index, raw := range invalid {
		if _, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks); err == nil {
			t.Fatalf("invalid response %d unexpectedly passed", index)
		}
	}
}

func TestParseKnowledgeEvidenceJudgeResponseNormalizesSingleCandidateCombined(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "task-1",
		Query:  "房间里有几瓶矿泉水",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "task-1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "问题：房间里有两瓶矿泉水吗\n答案：是的。"}},
			{CandidateID: "task-1C2", Layer: knowledgeEvidenceLayerStore},
		},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"task-1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["task-1C1"],"supportedFacts":[{"factId":"task-1F1","aspect":"quantity","statement":"房间内有两瓶矿泉水。","criticalValues":["两瓶"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("single valid candidate must not fail the whole judge batch: %v", err)
	}
	selection := parsed["task-1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SelectedCandidateIDs) != 1 || selection.SelectedCandidateIDs[0] != "task-1C1" {
		t.Fatalf("single-candidate combined decision was not normalized safely: %#v", selection)
	}
	if len(selection.SupportedFacts) != 1 || selection.SupportedFacts[0].CriticalValues[0] != "两瓶" {
		t.Fatalf("validated facts must survive normalization: %#v", selection.SupportedFacts)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseIsolatesInvalidAndMissingSelections(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{
		{
			TaskID: "T1",
			Query:  "早餐几点",
			Candidates: []knowledgeEvidenceJudgeCandidate{
				{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore},
				{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Content: "问题：早餐几点\n答案：通用早餐时间为7:00-10:00。"}},
			},
		},
		{
			TaskID: "T2",
			Query:  "有空调吗",
			Candidates: []knowledgeEvidenceJudgeCandidate{
				{CandidateID: "T2C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "问题：房间有空调吗\n答案：是的。"}},
				{CandidateID: "T2C2", Layer: knowledgeEvidenceLayerGeneral},
			},
		},
		{
			TaskID: "T3",
			Query:  "有办公桌吗",
			Candidates: []knowledgeEvidenceJudgeCandidate{
				{CandidateID: "T3C1", Layer: knowledgeEvidenceLayerStore},
			},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[` +
		`{"taskId":"T1","layers":[` +
		`{"layer":"store","decision":"direct_single","selectedCandidateIds":["UNKNOWN"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"早餐时间为7:00-9:30。","criticalValues":["7:00-9:30"]}],"missingAspects":[]},` +
		`{"layer":"general","decision":"direct_single","selectedCandidateIds":["T1C2"],"supportedFacts":[{"factId":"T1F2","aspect":"time","statement":"通用早餐时间为7:00-10:00。","criticalValues":["7:00-10:00"]}],"missingAspects":[]}` +
		`]},` +
		`{"taskId":"T2","layers":[` +
		`{"layer":"store","decision":"direct_single","selectedCandidateIds":["T2C1"],"supportedFacts":[{"factId":"T2F1","aspect":"existence","statement":"房间有空调。","criticalValues":["有空调"]}],"missingAspects":[]}` +
		`]},` +
		`{"taskId":"UNKNOWN","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["UNKNOWN-C1"],"supportedFacts":[],"missingAspects":[]}]}` +
		`]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("one invalid layer must not discard other valid tasks: %v", err)
	}
	invalidStore := parsed["T1"][knowledgeEvidenceLayerStore]
	if invalidStore.Decision != knowledgeEvidenceDecisionInsufficient || len(invalidStore.SelectedCandidateIDs) != 0 || len(invalidStore.SupportedFacts) != 0 {
		t.Fatalf("unknown candidate must be withheld from Generate: %#v", invalidStore)
	}
	validGeneral := parsed["T1"][knowledgeEvidenceLayerGeneral]
	if validGeneral.Decision != knowledgeEvidenceDecisionDirectSingle || len(validGeneral.SelectedCandidateIDs) != 1 || validGeneral.SelectedCandidateIDs[0] != "T1C2" {
		t.Fatalf("valid sibling layer was lost: %#v", validGeneral)
	}
	validTask := parsed["T2"][knowledgeEvidenceLayerStore]
	if validTask.Decision != knowledgeEvidenceDecisionDirectSingle || len(validTask.SelectedCandidateIDs) != 1 || validTask.SelectedCandidateIDs[0] != "T2C1" {
		t.Fatalf("valid independent task was lost: %#v", validTask)
	}
	if missingLayer := parsed["T2"][knowledgeEvidenceLayerGeneral]; missingLayer.Decision != knowledgeEvidenceDecisionInsufficient || len(missingLayer.SelectedCandidateIDs) != 0 {
		t.Fatalf("missing layer must degrade locally: %#v", missingLayer)
	}
	if missingTask := parsed["T3"][knowledgeEvidenceLayerStore]; missingTask.Decision != knowledgeEvidenceDecisionInsufficient || len(missingTask.SelectedCandidateIDs) != 0 {
		t.Fatalf("missing task must degrade locally: %#v", missingTask)
	}
	if _, exists := parsed["UNKNOWN"]; exists {
		t.Fatalf("unknown task must never enter parsed selections: %#v", parsed["UNKNOWN"])
	}
}

func TestParseKnowledgeEvidenceJudgeResponseAcceptsPartialFactsAndStoreHandoff(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{
		{
			TaskID: "T1",
			Query:  "外卖机器人能送到房间吗",
			Candidates: []knowledgeEvidenceJudgeCandidate{{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "外卖机器人", "问题：有外卖机器人吗\n答案：有外卖机器人的。", 0.9),
			}},
		},
		{
			TaskID: "T2",
			Query:  "马桶堵了",
			Candidates: []knowledgeEvidenceJudgeCandidate{{
				CandidateID: "T2C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 102, "马桶故障", "问题：马桶堵了\n答案：转接", 0.95),
			}},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"partial","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"门店有外卖机器人。","criticalValues":[]}],"missingAspects":["机器人配送范围"]}]},{"taskId":"T2","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T2C1"],"supportedFacts":[],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse partial and handoff response: %v", err)
	}
	partial := parsed["T1"][knowledgeEvidenceLayerStore]
	if partial.Decision != knowledgeEvidenceDecisionPartial || len(partial.SupportedFacts) != 1 || len(partial.MissingAspects) != 1 {
		t.Fatalf("unexpected partial selection: %#v", partial)
	}
	handoff := parsed["T2"][knowledgeEvidenceLayerStore]
	if handoff.Decision != knowledgeEvidenceDecisionDirectSingle || len(handoff.SupportedFacts) != 0 {
		t.Fatalf("unexpected handoff selection: %#v", handoff)
	}
}

func TestRepairHighConfidenceInsufficientKnowledgeSelectionUsesGroundedConsensus(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "task-1",
		Query:     "麦田房型有办公桌吗？",
		Objective: "availability",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "麦田", Type: "room_type"},
			{Text: "办公桌", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "task-1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "哪些房型有办公桌", "问题：哪些房型有办公桌\n答案：酒店部分房型配备办公桌，如合柴、麦田和艺林。", 0.95),
			},
			{
				CandidateID: "task-1C2",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 102, "麦田房型设施", "问题：麦田房型设施有哪些\n答案：酒店部分房型配备办公桌，如合柴、麦田和艺林。", 0.94),
			},
			{
				CandidateID: "task-1C3",
				Layer:       knowledgeEvidenceLayerGeneral,
				Hit:         judgeTestHit(2, 201, "酒店设施", "问题：酒店有桌子吗\n答案：以具体门店房型为准。", 0.91),
			},
		},
	}
	selections := map[string]map[string]knowledgeEvidenceLayerSelection{
		"task-1": {
			knowledgeEvidenceLayerStore:   insufficientKnowledgeEvidenceLayerSelection(),
			knowledgeEvidenceLayerGeneral: insufficientKnowledgeEvidenceLayerSelection(),
		},
	}

	if repaired := repairHighConfidenceInsufficientKnowledgeSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 1 {
		t.Fatalf("expected one repaired store selection, got %d: %#v", repaired, selections)
	}
	store := selections["task-1"][knowledgeEvidenceLayerStore]
	if store.Decision != knowledgeEvidenceDecisionDirectSingle || len(store.SelectedCandidateIDs) != 1 || store.SelectedCandidateIDs[0] != "task-1C1" {
		t.Fatalf("unexpected repaired selection: %#v", store)
	}
	if len(store.SupportedFacts) != 1 || !strings.Contains(store.SupportedFacts[0].Statement, "麦田") || !containsString(store.SupportedFacts[0].CriticalValues, "办公桌") {
		t.Fatalf("repair must keep only grounded answer text and entities: %#v", store.SupportedFacts)
	}
	if general := selections["task-1"][knowledgeEvidenceLayerGeneral]; general.Decision != knowledgeEvidenceDecisionInsufficient {
		t.Fatalf("general layer must remain insufficient: %#v", general)
	}
}

func TestRepairHighConfidenceInsufficientKnowledgeSelectionUsesStrictHighScoreConfigurationMatch(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "酒店WiFi账号和密码是什么",
		Objective: "compound_information",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "WiFi", Type: "network"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "无线网信息", "问题：无线网密码是什么\n答案：无线网账号是 alilys，密码是 yzbh8888。", 0.94),
			},
		},
	}
	question, _ := splitKnowledgeEvidenceFAQForQuery(task.Candidates[0].Hit, task.Query)
	if match := knowledgeEvidenceFAQQuestionMatchScore(question, task.Query); match >= 0.82 {
		t.Fatalf("test must exercise answer coverage beyond plain question similarity, got %.3f", match)
	}
	selections := map[string]map[string]knowledgeEvidenceLayerSelection{
		"T1": {knowledgeEvidenceLayerStore: insufficientKnowledgeEvidenceLayerSelection()},
	}
	if match, complete := knowledgeEvidenceJudgeCandidateCompletesTask(task, task.Candidates[0]); !complete || match >= 0.82 {
		t.Fatalf("strict high-score configuration FAQ must be retained without pretending its question is an exact match, complete=%v match=%.3f", complete, match)
	}
	if repaired := repairHighConfidenceInsufficientKnowledgeSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 1 {
		t.Fatalf("expected strict high-score configuration repair, repaired=%d selections=%#v", repaired, selections)
	}
	selection := selections["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SelectedCandidateIDs) != 1 || selection.SelectedCandidateIDs[0] != "T1C1" {
		t.Fatalf("strict configuration FAQ must become a direct single selection, got %#v", selection)
	}
	criticalValues := make([]string, 0, 2)
	for _, fact := range selection.SupportedFacts {
		criticalValues = append(criticalValues, fact.CriticalValues...)
	}
	if !containsString(criticalValues, "alilys") || !containsString(criticalValues, "yzbh8888") {
		t.Fatalf("configuration repair must preserve both requested values: %#v", selection.SupportedFacts)
	}
}

func TestHighConfidenceConfigurationSelectionAllowsEquivalentSameScopeDuplicates(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "酒店WiFi账号和密码是什么",
		Objective: "compound_information",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "Wi-Fi", Type: "network"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "无线网信息", "问题：无线网密码是什么\n答案：无线网账号是 alilys，密码是 yzbh8888。", 0.8946)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "无线网络信息", "问题：无线网络密码是什么\n答案：无线网络账号是 alilys，密码是 yzbh8888。", 0.8792)},
		},
	}

	selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore)
	if !ok {
		t.Fatal("equivalent same-scope configuration FAQs must not block deterministic rescue")
	}
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SelectedCandidateIDs) != 1 {
		t.Fatalf("unexpected configuration selection: %#v", selection)
	}
	criticalValues := make([]string, 0, 2)
	for _, fact := range selection.SupportedFacts {
		criticalValues = append(criticalValues, fact.CriticalValues...)
	}
	if !containsString(criticalValues, "alilys") || !containsString(criticalValues, "yzbh8888") {
		t.Fatalf("equivalent FAQ rescue must keep both requested values: %#v", selection.SupportedFacts)
	}
}

func TestRepairHighConfidenceInsufficientKnowledgeSelectionRejectsCompleteConfigurationBelowDirectThreshold(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "WiFi账号密码多少",
		Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "无线网故障", "问题：WiFi连不上怎么办\n答案：可以断开后重新连接。", 0.8578),
			},
			{
				CandidateID: "T1C2",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 102, "无线网信息", "问题：无线网密码是什么\n答案：无线网账号是 LISI，密码是 lis888888。", 0.8116),
			},
			{
				CandidateID: "T1C3",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 103, "无线网密码", "问题：WiFi密码是什么\n答案：密码是 lis888888。", 0.8182),
			},
		},
	}
	selections := map[string]map[string]knowledgeEvidenceLayerSelection{
		"T1": {knowledgeEvidenceLayerStore: insufficientKnowledgeEvidenceLayerSelection()},
	}

	if repaired := repairHighConfidenceInsufficientKnowledgeSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("complete fields must not lower the direct FAQ threshold, repaired=%d selections=%#v", repaired, selections)
	}
	selection := selections["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionInsufficient || len(selection.SelectedCandidateIDs) != 0 {
		t.Fatalf("below-threshold configuration must remain insufficient, got %#v", selection)
	}
}

func TestHighConfidenceConfigurationSelectionRequiresMatchingUsageScope(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "客房WiFi账号密码是多少",
		Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "大堂WiFi", "问题：大堂WiFi账号密码是多少\n答案：大堂WiFi账号是 LOBBY，密码是 lobby888。", 0.98),
		}},
	}
	if selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("lobby configuration must not answer a guest-room query: %#v", selection)
	}
}

func TestHighConfidenceConfigurationSelectionRejectsAmbiguousUnscopedConfigurations(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "WiFi账号密码是多少",
		Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "客房WiFi", "问题：客房WiFi账号密码是多少\n答案：客房WiFi账号是 ROOM，密码是 room888。", 0.98)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "大堂WiFi", "问题：大堂WiFi账号密码是多少\n答案：大堂WiFi账号是 LOBBY，密码是 lobby888。", 0.84)},
		},
	}
	if selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("unscoped query must not choose between multiple scoped configurations, even when one conflict is below the rescue threshold: %#v", selection)
	}
}

func TestHighConfidenceConfigurationSelectionRejectsConflictingSameScopeValues(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "酒店WiFi账号和密码是什么",
		Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "无线网信息", "问题：无线网密码是什么\n答案：无线网账号是 alilys，密码是 yzbh8888。", 0.94)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "无线网信息", "问题：无线网密码是什么\n答案：无线网账号是 other，密码是 other8888。", 0.84)},
		},
	}
	if selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("conflicting same-layer configuration values must block deterministic rescue, even below the rescue threshold: %#v", selection)
	}
}

func TestRepairHighConfidenceInsufficientKnowledgeSelectionDoesNotOverrideValidJudgeSelection(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "酒店WiFi账号和密码是什么",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "无线网信息", "问题：无线网密码是什么\n答案：无线网账号是 alilys，密码是 yzbh8888。", 0.94),
		}},
	}
	selections := map[string]map[string]knowledgeEvidenceLayerSelection{
		"T1": {knowledgeEvidenceLayerStore: {
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			SelectedCandidateIDs: []string{"T1C1"},
			SupportedFacts: []knowledgeEvidenceFact{{
				FactID: "T1F1", Aspect: "other", Statement: "Judge 已确认的有效答案。",
			}},
		}},
	}
	if repaired := repairHighConfidenceInsufficientKnowledgeSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("valid Judge selection must not be overwritten, repaired=%d selections=%#v", repaired, selections)
	}
	if got := selections["T1"][knowledgeEvidenceLayerStore]; len(got.SupportedFacts) != 1 || got.SupportedFacts[0].Statement != "Judge 已确认的有效答案。" {
		t.Fatalf("valid Judge selection changed unexpectedly: %#v", got)
	}
}

func TestConfigurationAnswerCoverageRequiresEveryRequestedField(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "无线网络的登录账号和连接口令分别是多少",
		Objective: "general_guidance",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "无线网密码", "问题：WiFi密码是什么\n答案：密码是 lis888888。", 0.96),
			},
		},
	}
	if selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("password-only answer must not satisfy an account-and-password query: %#v", selection)
	}
}

func TestConfigurationAnswerCoverageRequiresConcreteValues(t *testing.T) {
	if knowledgeEvidenceConfigurationAnswerCoversQuery(
		"WiFi账号密码多少",
		"WiFi账号密码是多少",
		"请联系门店确认。",
	) {
		t.Fatal("field names without concrete values must not become a direct answer")
	}
}

func TestConfigurationValuesKeepChineseSpacesAndPasswordSymbols(t *testing.T) {
	values := knowledgeEvidenceConfigurationValues("WiFi名称：丽斯 南七，密码：ab!^=/?:()#9。")
	if got := values["account"]; len(got) != 1 || got[0] != "丽斯 南七" {
		t.Fatalf("unexpected account values: %#v", got)
	}
	if got := values["password"]; len(got) != 1 || got[0] != "ab!^=/?:()#9" {
		t.Fatalf("unexpected password values: %#v", got)
	}
}

func TestFilterKnowledgeEvidenceFactsKeepsOnlyRelevantConfigurationOtherFacts(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "WiFi账号密码多少"}
	facts := []knowledgeEvidenceFact{
		{FactID: "T1F1", Aspect: "other", Statement: "WiFi账号：LISI，密码：lis888888。"},
		{FactID: "T1F2", Aspect: "other", Statement: "如有问题请联系门店。"},
	}
	filtered := filterKnowledgeEvidenceFactsForTask(task, facts)
	if len(filtered) != 1 || filtered[0].FactID != "T1F1" {
		t.Fatalf("only concrete requested configuration facts should remain: %#v", filtered)
	}
}

func TestDeterministicKnowledgeFallbackIgnoresUnrelatedHandoffAndKeepsDirectFAQ(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "WiFi账号密码多少",
		Objective: "general_guidance",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "网络故障", "问题：网络连接不上怎么办\n答案：转接", 0.99),
			},
			{
				CandidateID: "T1C2",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 102, "无线网信息", "问题：WiFi账号密码多少\n答案：无线网账号是 LISI，密码是 lis888888。", 0.94),
			},
		},
	}
	selections, grounded, handoffs := deterministicKnowledgeEvidenceJudgeFallbackSelections([]knowledgeEvidenceJudgeTask{task})
	selection := selections["T1"][knowledgeEvidenceLayerStore]
	if handoffs != 0 || grounded != 1 {
		t.Fatalf("unrelated transfer must not override the grounded FAQ, grounded=%d handoffs=%d", grounded, handoffs)
	}
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SelectedCandidateIDs) != 1 || selection.SelectedCandidateIDs[0] != "T1C2" {
		t.Fatalf("expected the direct WiFi FAQ, got %#v", selection)
	}
}

func TestDeterministicKnowledgeFallbackKeepsSemanticallyMatchedHandoff(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "马桶堵了",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "马桶故障", "问题：马桶堵了怎么办\n答案：转接", 0.82),
			},
		},
	}
	selection, ok := deterministicKnowledgeEvidenceHandoffSelection(task, knowledgeEvidenceLayerStore)
	if !ok || selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SelectedCandidateIDs) != 1 {
		t.Fatalf("matched store handoff must remain authoritative: %#v ok=%v", selection, ok)
	}
}

func TestRepairHighConfidenceInsufficientKnowledgeSelectionUsesSingleEntityAvailabilityFAQ(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "task-1",
		Query:     "你们有外卖机器人吗？",
		Objective: "availability",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "外卖机器人", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "task-1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "外卖机器人", "问题：你们家有外卖机器人吗？\n答案：有外卖机器人的。", 0.9508),
			},
		},
	}
	selections := map[string]map[string]knowledgeEvidenceLayerSelection{
		"task-1": {knowledgeEvidenceLayerStore: insufficientKnowledgeEvidenceLayerSelection()},
	}
	if repaired := repairHighConfidenceInsufficientKnowledgeSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 1 {
		t.Fatalf("expected one availability FAQ repair, got %d: %#v", repaired, selections)
	}
	selection := selections["task-1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SupportedFacts) != 1 {
		t.Fatalf("expected one direct existence fact: %#v", selection)
	}
	fact := selection.SupportedFacts[0]
	if fact.Aspect != "existence" || !strings.Contains(fact.Statement, "外卖机器人") || !containsString(fact.CriticalValues, "外卖机器人") {
		t.Fatalf("unexpected availability fact: %#v", fact)
	}
}

func TestRepairHighConfidenceInsufficientKnowledgeSelectionUsesComparisonGuidanceFAQ(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "task-1",
		Query:     "携程、抖音、美团的价格是一样的吗？",
		Objective: "price",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "携程", Type: "company"},
			{Text: "抖音", Type: "company"},
			{Text: "美团", Type: "company"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "task-1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "平台价格", "问题：问题：携程，抖音，美团的价格是一样的吗\n答案：每个客户在不同平台享受的平台权益是不一样的，建议您可以对比价格后选择合适您的。", 0.9630),
			},
		},
	}
	selections := map[string]map[string]knowledgeEvidenceLayerSelection{
		"task-1": {knowledgeEvidenceLayerStore: insufficientKnowledgeEvidenceLayerSelection()},
	}
	if repaired := repairHighConfidenceInsufficientKnowledgeSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 1 {
		question, answer := splitKnowledgeEvidenceFAQForQuery(task.Candidates[0].Hit, task.Query)
		facts := deterministicKnowledgeEvidenceFactsFromFAQ(task.TaskID, answer)
		facts = filterKnowledgeEvidenceFactsForTask(task, facts)
		t.Fatalf("expected comparison FAQ repair, repaired=%d question=%q match=%.3f answer=%q facts=%#v missing=%#v", repaired, question, knowledgeEvidenceFAQQuestionMatchScore(question, task.Query), answer, facts, missingRequiredKnowledgeEvidenceAspects(task, facts))
	}
	selection := selections["task-1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SupportedFacts) == 0 {
		t.Fatalf("expected direct comparison guidance facts: %#v", selection)
	}
}

func TestSingleEntityAvailabilityRepairRejectsDifferentEntityAnswer(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "task-1",
		Query:     "你们有外卖机器人吗？",
		Objective: "availability",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "外卖机器人", Type: "facility"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "task-1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "外卖机器人", "问题：你们家有外卖机器人吗？\n答案：有早餐的。", 0.96)},
		},
	}
	if selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("a different entity answer must not repair availability: %#v", selection)
	}
}

func TestRepairHighConfidenceInsufficientKnowledgeSelectionDoesNotInferMissingScope(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "task-1",
		Query:     "外卖机器人能送到房间吗？",
		Objective: "availability",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "外卖机器人", Type: "facility"},
			{Text: "房间", Type: "location"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "task-1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "外卖机器人", "问题：有外卖机器人吗\n答案：有外卖机器人的。", 0.96)},
			{CandidateID: "task-1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "机器人", "问题：门店有机器人吗\n答案：有外卖机器人的。", 0.94)},
		},
	}
	selections := map[string]map[string]knowledgeEvidenceLayerSelection{
		"task-1": {knowledgeEvidenceLayerStore: insufficientKnowledgeEvidenceLayerSelection()},
	}

	if repaired := repairHighConfidenceInsufficientKnowledgeSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("existence evidence must not be promoted to delivery scope, repaired=%d selection=%#v", repaired, selections)
	}
}

func TestSplitKnowledgeEvidenceFAQForQuerySelectsMatchingUnit(t *testing.T) {
	hit := judgeTestHit(1, 101, "入住与开门", `问题：怎么办理入住
答案：我们酒店没有传统前台，你可以通过入住机或小程序线上智能化方式办理入住。
### 酒店房门怎么打开
问题：酒店房门怎么打开
答案：完成登记扫人脸就可以开门啦，无需房卡。`, 0.96)

	checkInQuestion, checkInAnswer := splitKnowledgeEvidenceFAQForQuery(hit, "怎么办理入住")
	if checkInQuestion != "怎么办理入住" || !strings.Contains(checkInAnswer, "入住机") || strings.Contains(checkInAnswer, "房门怎么打开") {
		t.Fatalf("check-in query selected the wrong FAQ unit: question=%q answer=%q", checkInQuestion, checkInAnswer)
	}
	doorQuestion, doorAnswer := splitKnowledgeEvidenceFAQForQuery(hit, "酒店房门怎么打开")
	if doorQuestion != "酒店房门怎么打开" || !strings.Contains(doorAnswer, "扫人脸") || strings.Contains(doorAnswer, "入住机") {
		t.Fatalf("door query selected the wrong FAQ unit: question=%q answer=%q", doorQuestion, doorAnswer)
	}
}

func TestRepairHighConfidenceDirectFAQSelectionKeepsCompleteAnswer(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		content     string
		mustContain []string
	}{
		{
			name:        "platform prices",
			query:       "携程抖音美团的价格一样吗",
			content:     "问题：携程抖音美团的价格一样吗\n答案：每个客户在不同平台享受的平台权益是不一样的，建议您可以对比价格后选择合适您的。",
			mustContain: []string{"平台权益", "对比价格", "选择"},
		},
		{
			name:        "check in",
			query:       "怎么办理入住",
			content:     "问题：怎么办理入住\n答案：我们酒店没有传统前台，你可以通过入住机或小程序线上智能化方式办理入住。",
			mustContain: []string{"没有传统前台", "入住机", "小程序"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{
				TaskID: "task-1",
				Query:  tc.query,
				Candidates: []knowledgeEvidenceJudgeCandidate{{
					CandidateID: "task-1C1",
					Layer:       knowledgeEvidenceLayerStore,
					Hit:         judgeTestHit(1, 101, tc.query, tc.content, 0.96),
				}},
			}
			selections := map[string]map[string]knowledgeEvidenceLayerSelection{
				"task-1": {knowledgeEvidenceLayerStore: insufficientKnowledgeEvidenceLayerSelection()},
			}

			if repaired := repairHighConfidenceInsufficientKnowledgeSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 1 {
				t.Fatalf("expected high-confidence FAQ repair, got %d: %#v", repaired, selections)
			}
			selection := selections["task-1"][knowledgeEvidenceLayerStore]
			joined := ""
			for _, fact := range selection.SupportedFacts {
				joined += fact.Statement + " " + strings.Join(fact.CriticalValues, " ")
			}
			for _, required := range tc.mustContain {
				if !strings.Contains(joined, required) {
					t.Fatalf("repaired facts lost %q: %#v", required, selection.SupportedFacts)
				}
			}
		})
	}
}

func TestRepairHighConfidencePartialKnowledgeSelectionUsesCompleteExactFAQ(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "task-1",
		Query:     "房间里有几瓶矿泉水，都是免费的吗",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "task-1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "矿泉水数量和费用", "问题：房间里有几瓶矿泉水，都是免费的吗\n答案：房间内有两瓶矿泉水，都是免费的。", 0.97),
			},
			{
				CandidateID: "task-1C2",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 102, "补充矿泉水", "问题：还要矿泉水怎么办\n答案：洗衣房有多余矿泉水，可以去1313对面自取。", 0.9),
			},
		},
	}
	selections := map[string]map[string]knowledgeEvidenceLayerSelection{
		"task-1": {
			knowledgeEvidenceLayerStore: {
				Decision:             knowledgeEvidenceDecisionPartial,
				SelectedCandidateIDs: []string{"task-1C2"},
				SupportedFacts: []knowledgeEvidenceFact{{
					FactID: "task-1F1", Aspect: "price", Statement: "房间内矿泉水免费。", CriticalValues: []string{"免费"},
				}},
				MissingAspects: []string{"数量"},
			},
		},
	}

	if repaired := repairHighConfidenceInsufficientKnowledgeSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 1 {
		t.Fatalf("expected exact FAQ to repair partial selection, repaired=%d selections=%#v", repaired, selections)
	}
	selection := selections["task-1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SelectedCandidateIDs) != 1 || selection.SelectedCandidateIDs[0] != "task-1C1" {
		t.Fatalf("expected complete exact FAQ selection, got %#v", selection)
	}
	if missing := missingRequiredKnowledgeEvidenceAspects(task, selection.SupportedFacts); len(missing) != 0 {
		t.Fatalf("repaired FAQ must cover quantity and price, missing=%#v facts=%#v", missing, selection.SupportedFacts)
	}
}

func TestRepairHighConfidenceServiceSupplyFAQUsesGroundedSelfServicePath(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		question string
		answer   string
		state    knowledgeEvidenceLayerSelection
		wantItem string
		wantSite string
	}{
		{
			name:     "slippers insufficient",
			query:    "拖鞋没了",
			question: "需要额外拖鞋怎么办",
			answer:   "如需额外拖鞋，可前往1313对面洗衣房领取。",
			state:    insufficientKnowledgeEvidenceLayerSelection(),
			wantItem: "拖鞋",
			wantSite: "1313对面洗衣房",
		},
		{
			name:     "toothbrush partial",
			query:    "牙刷没有了",
			question: "需要额外牙刷怎么办",
			answer:   "额外牙刷可以到1313对面洗衣房自取。",
			state: knowledgeEvidenceLayerSelection{
				Decision:             knowledgeEvidenceDecisionPartial,
				SelectedCandidateIDs: []string{"T1C1"},
				SupportedFacts: []knowledgeEvidenceFact{{
					FactID: "T1F1", Aspect: "location", Statement: "牙刷在1313对面洗衣房。",
				}},
				MissingAspects: []string{"处理方式"},
			},
			wantItem: "牙刷",
			wantSite: "1313对面洗衣房",
		},
		{
			name:     "tissue insufficient",
			query:    "纸巾不够",
			question: "纸巾不够怎么办",
			answer:   "可以前往1020对面的洗衣房拿取纸巾。",
			state:    insufficientKnowledgeEvidenceLayerSelection(),
			wantItem: "纸巾",
			wantSite: "1020对面的洗衣房",
		},
		{
			name:     "towel insufficient",
			query:    "浴巾在哪拿",
			question: "额外浴巾在哪里领取",
			answer:   "额外浴巾可前往1313对面洗衣房领取。",
			state:    insufficientKnowledgeEvidenceLayerSelection(),
			wantItem: "浴巾",
			wantSite: "1313对面洗衣房",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{
				TaskID:    "T1",
				Intent:    "service_request",
				Query:     tt.query,
				SubIntent: "supplies_self_help",
				Objective: "action_request",
				Entities:  []knowledgeEvidenceJudgeEntity{{Text: tt.wantItem, Type: "supply"}},
				Candidates: []knowledgeEvidenceJudgeCandidate{{
					CandidateID: "T1C1",
					Layer:       knowledgeEvidenceLayerStore,
					Hit:         judgeTestHit(1, 101, tt.wantItem+"自取", "问题："+tt.question+"\n答案："+tt.answer, 0.92),
				}},
			}
			question, answer := splitKnowledgeEvidenceFAQForQuery(task.Candidates[0].Hit, task.Query)
			if match := knowledgeEvidenceFAQQuestionMatchScore(question, task.Query); match >= 0.82 {
				t.Fatalf("test must cover semantic FAQ rescue below the character-match gate, got %.3f", match)
			}
			selections := map[string]map[string]knowledgeEvidenceLayerSelection{
				"T1": {knowledgeEvidenceLayerStore: tt.state},
			}
			if repaired := repairHighConfidenceInsufficientKnowledgeSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 1 {
				t.Fatalf("expected one grounded store FAQ repair, repaired=%d selection=%#v", repaired, selections)
			}
			selection := selections["T1"][knowledgeEvidenceLayerStore]
			if selection.Decision != knowledgeEvidenceDecisionDirectSingle || selection.DecisionSource != "store_exact_faq_rescue" {
				t.Fatalf("unexpected repaired selection: %#v", selection)
			}
			aspects := make(map[string]bool)
			joined := ""
			for _, fact := range selection.SupportedFacts {
				aspects[fact.Aspect] = true
				joined += fact.Statement + " "
			}
			if !aspects["method"] || !aspects["location"] {
				t.Fatalf("self-service path must retain method and location facts: %#v", selection.SupportedFacts)
			}
			if !strings.Contains(joined, tt.wantSite) {
				t.Fatalf("repaired facts lost the pickup location: %#v", selection.SupportedFacts)
			}
			if missing := missingRequiredKnowledgeEvidenceAspects(task, selection.SupportedFacts); len(missing) != 0 {
				t.Fatalf("repaired self-service FAQ must be complete, missing=%#v facts=%#v answer=%q", missing, selection.SupportedFacts, answer)
			}
		})
	}
}

func TestHighConfidenceServiceSupplyFAQRejectsSameLayerConflict(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Intent:    "service_request",
		Query:     "拖鞋没了",
		SubIntent: "supplies_self_help",
		Objective: "action_request",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "拖鞋自取", "问题：需要额外拖鞋怎么办\n答案：可前往1313对面洗衣房领取。", 0.93)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "拖鞋领取", "问题：额外拖鞋去哪里拿\n答案：可前往1020对面洗衣房领取。", 0.92)},
		},
	}
	if selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("conflicting pickup locations must not be rescued: %#v", selection)
	}
}

func TestHighConfidenceHotelInfoSupplySelfHelpUsesStrictStoreFAQRescue(t *testing.T) {
	for _, tt := range []struct {
		query     string
		objective string
		item      string
		question  string
		answer    string
		location  string
	}{
		{
			query: "纸巾不够，在哪里拿", objective: "location", item: "纸巾",
			question: "房间纸巾用完了怎么办", answer: "可前往1313对面洗衣房领取纸巾。", location: "1313对面洗衣房",
		},
		{
			query: "浴巾需要加一条，怎么取", objective: "method", item: "浴巾",
			question: "需要额外浴巾怎么办", answer: "可前往1313对面洗衣房自取浴巾。", location: "1313对面洗衣房",
		},
	} {
		task := knowledgeEvidenceJudgeTask{
			TaskID: "T1", Intent: "hotel_info", Query: tt.query, SubIntent: "supplies_self_help", Objective: tt.objective,
			Entities: []knowledgeEvidenceJudgeEntity{{Text: tt.item, Type: "supply"}},
			Candidates: []knowledgeEvidenceJudgeCandidate{{
				CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 101, tt.item+"自取", "问题："+tt.question+"\n答案："+tt.answer, 0.91),
			}},
		}
		question, _ := splitKnowledgeEvidenceFAQForQuery(task.Candidates[0].Hit, task.Query)
		if match := knowledgeEvidenceFAQQuestionMatchScore(question, task.Query); match >= 0.94 {
			t.Fatalf("test must exercise semantic rescue below exact FAQ matching, got %.3f", match)
		}
		selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore)
		if !ok || selection.Decision != knowledgeEvidenceDecisionDirectSingle || selection.DecisionSource != "store_exact_faq_rescue" {
			t.Fatalf("hotel_info supplies self-help must use the same strict store FAQ rescue: %#v ok=%v", selection, ok)
		}
		joined := ""
		for _, fact := range selection.SupportedFacts {
			joined += fact.Statement + " "
		}
		if !strings.Contains(joined, tt.location) || len(missingRequiredKnowledgeEvidenceAspects(task, selection.SupportedFacts)) != 0 {
			t.Fatalf("rescued FAQ must fully cover the requested pickup answer: %#v", selection.SupportedFacts)
		}
	}
}

func TestHighConfidenceHotelInfoSemanticRescueStaysLimitedToSupplySelfHelp(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Intent: "hotel_info", Query: "附近有什么餐馆", SubIntent: "surrounding_facilities", Objective: "location",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "餐馆", Type: "place"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "餐馆", "问题：附近吃饭去哪里\n答案：可前往小丁小吃。", 0.93),
		}},
	}
	if selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("non-supply hotel information must continue to rely on exact matching or Judge: %#v", selection)
	}
}

func TestHighConfidenceServiceRequestAcceptsActionableLocationWithoutMethodVerb(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Intent:    "service_request",
		Query:     "遥控器找不到",
		SubIntent: "room_supply_location",
		Objective: "action_request",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "遥控器", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "遥控器位置", "问题：遥控器一般放在哪里\n答案：遥控器在床头柜抽屉或电视柜里。", 0.93),
		}},
	}
	selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore)
	if !ok || selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("an actionable item location must complete the service request: %#v ok=%v", selection, ok)
	}
	if missing := missingRequiredKnowledgeEvidenceAspects(task, selection.SupportedFacts); len(missing) != 0 {
		t.Fatalf("the grounded location must satisfy the service action, missing=%#v facts=%#v", missing, selection.SupportedFacts)
	}
}

func TestHighConfidenceServiceSemanticFAQRejectsDifferentOperation(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Intent:    "service_request",
		Query:     "我想把房间空调温度调低一点",
		SubIntent: "air_conditioner_control",
		Objective: "action_request",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "空调", Type: "facility"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "关闭空调", "问题：关闭空调的方法\n答案：按遥控器电源键关闭空调。", 0.96),
		}},
	}
	question, _ := splitKnowledgeEvidenceFAQForQuery(task.Candidates[0].Hit, task.Query)
	if match := knowledgeEvidenceFAQQuestionMatchScore(question, task.Query); match >= 0.82 {
		t.Fatalf("test must exercise semantic rescue rather than exact question matching, got %.3f", match)
	}
	if selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("temperature adjustment must not be rescued by a turn-off FAQ: %#v", selection)
	}
}

func TestHighConfidenceServiceSupplyFAQRejectsNonnumericLocationConflict(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Intent: "service_request", Query: "拖鞋没了", SubIntent: "supplies_self_help", Objective: "action_request",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "拖鞋自取", "问题：需要额外拖鞋怎么办\n答案：可前往洗衣房领取。", 0.93)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "拖鞋领取", "问题：额外拖鞋去哪里拿\n答案：可到百宝箱领取。", 0.92)},
		},
	}
	if selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("different named pickup locations must block deterministic rescue: %#v", selection)
	}
}

func TestHighConfidenceServiceSupplyFAQRejectsArbitraryPickupLocationConflict(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Intent: "service_request", Query: "拖鞋没了", SubIntent: "supplies_self_help", Objective: "action_request",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "拖鞋自取", "问题：需要额外拖鞋怎么办\n答案：可前往布草间领取。", 0.93)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "拖鞋领取", "问题：额外拖鞋去哪里拿\n答案：可以到储物柜自取。", 0.92)},
		},
	}
	if selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("arbitrary conflicting pickup locations must block deterministic rescue: %#v", selection)
	}
}

func TestHighConfidenceServiceSupplyFAQAllowsDetailedAndShortPickupLocation(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Intent: "service_request", Query: "拖鞋没了", SubIntent: "supplies_self_help", Objective: "action_request",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "拖鞋自取", "问题：需要额外拖鞋怎么办\n答案：可前往二楼布草间领取。", 0.93)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "拖鞋领取", "问题：额外拖鞋去哪里拿\n答案：可以到布草间自取。", 0.92)},
		},
	}
	selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore)
	if !ok || selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("detailed and short forms of the same pickup location must remain compatible: %#v ok=%v", selection, ok)
	}
}

func TestKnowledgeEvidenceExplicitPickupLocationPrefersActionPreposition(t *testing.T) {
	got := knowledgeEvidenceExplicitPickupLocationSignatures("可前往签到处领取。")
	if len(got) != 1 || got[0] != "签到处" {
		t.Fatalf("a character inside the location must not replace the action preposition: %#v", got)
	}
}

func TestHighConfidenceServiceSupplyFAQRejectsPickupDeliveryConflict(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Intent: "service_request", Query: "拖鞋没了", SubIntent: "supplies_self_help", Objective: "action_request",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "拖鞋自取", "问题：需要额外拖鞋怎么办\n答案：可前往洗衣房自取。", 0.93)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "拖鞋配送", "问题：需要额外拖鞋怎么办\n答案：同事会把拖鞋送到房间。", 0.92)},
		},
	}
	if selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("self-pickup and room delivery must not be treated as compatible facts: %#v", selection)
	}
}

func TestHighConfidenceServiceSupplyFAQAllowsCompatibleLocationMethodSupplements(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Intent: "service_request", Query: "拖鞋没了", SubIntent: "supplies_self_help", Objective: "action_request",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "拖鞋位置", "问题：额外拖鞋在哪里\n答案：额外拖鞋在洗衣房。", 0.93)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "拖鞋自取", "问题：需要额外拖鞋怎么办\n答案：可前往洗衣房领取。", 0.92)},
		},
	}
	selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore)
	if !ok || selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("compatible location and pickup guidance must remain eligible: %#v ok=%v", selection, ok)
	}
}

func TestKnowledgeEvidenceFAQConflictSeparatesScopeDimensions(t *testing.T) {
	if !knowledgeEvidenceFAQAnswersConflict("所有房间都配有该设施。", "只有部分房型配有该设施。") {
		t.Fatal("all-room and partial-room coverage must conflict")
	}
	if !knowledgeEvidenceFAQAnswersConflict("外卖只能放一楼。", "外卖可以送到房间。") {
		t.Fatal("first-floor-only and room-delivery scopes must conflict")
	}
	if knowledgeEvidenceFAQAnswersConflict("所有房间都配有该设施。", "外卖可以送到房间。") {
		t.Fatal("different scope dimensions are compatible supplements, not a conflict")
	}
	if !knowledgeEvidenceFAQAnswersConflict("服务每天开放。", "服务仅工作日开放。") {
		t.Fatal("daily and workday-only conditions must conflict")
	}
}

func TestKnowledgeEvidenceLayerPriorityKeepsStoreBodyOverGeneralHandoff(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "拖鞋没了",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "拖鞋自取", "问题：拖鞋没了怎么办\n答案：可前往1313对面洗衣房领取。", 0.94)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerGeneral, Hit: judgeTestHit(2, 201, "用品服务", "问题：拖鞋没了怎么办\n答案：转接", 0.99)},
		},
	}
	selections := map[string]knowledgeEvidenceLayerSelection{
		knowledgeEvidenceLayerStore: {
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			SelectedCandidateIDs: []string{"T1C1"},
			SupportedFacts:       []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "method", Statement: "可前往1313对面洗衣房领取。"}},
		},
		knowledgeEvidenceLayerGeneral: {
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			SelectedCandidateIDs: []string{"T1C2"},
		},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{"T1C1": task.Candidates[0], "T1C2": task.Candidates[1]}
	if got := selectKnowledgeEvidenceLayer(selections, candidates, task.Query); got != knowledgeEvidenceLayerStore {
		t.Fatalf("store body must win over general handoff, got %q", got)
	}
}

func TestKnowledgeEvidenceLayerPriorityKeepsExactStoreHandoffOverGeneralBody(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "床单有毛发帮我换一下",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "更换床单", "问题：床单有毛发帮我换一下\n答案：转接", 0.93)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerGeneral, Hit: judgeTestHit(2, 201, "布草", "问题：床单有毛发怎么办\n答案：可以自行到洗衣房拿床单。", 0.99)},
		},
	}
	selections := map[string]knowledgeEvidenceLayerSelection{
		knowledgeEvidenceLayerStore:   {Decision: knowledgeEvidenceDecisionDirectSingle, SelectedCandidateIDs: []string{"T1C1"}},
		knowledgeEvidenceLayerGeneral: {Decision: knowledgeEvidenceDecisionDirectSingle, SelectedCandidateIDs: []string{"T1C2"}, SupportedFacts: []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "method", Statement: "可以自行到洗衣房拿床单。"}}},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{"T1C1": task.Candidates[0], "T1C2": task.Candidates[1]}
	if got := selectKnowledgeEvidenceLayer(selections, candidates, task.Query); got != knowledgeEvidenceLayerStore {
		t.Fatalf("exact store handoff must win over general body, got %q", got)
	}
}

func TestRepairHighConfidenceKnowledgeSelectionsKeepsTasksIndependent(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{
		{
			TaskID: "T1", Intent: "service_request", Query: "拖鞋没了", SubIntent: "supplies_self_help", Objective: "action_request",
			Entities:   []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
			Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "拖鞋自取", "问题：需要额外拖鞋怎么办\n答案：可前往1313对面洗衣房领取。", 0.93)}},
		},
		{
			TaskID: "T2", Intent: "service_request", Query: "帮我换个新枕头", SubIntent: "room_supply_request", Objective: "action_request",
			Entities:   []knowledgeEvidenceJudgeEntity{{Text: "枕头", Type: "supply"}},
			Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T2C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "其他用品", "问题：哪里能拿牙刷\n答案：牙刷在洗衣房自取。", 0.91)}},
		},
	}
	selections := map[string]map[string]knowledgeEvidenceLayerSelection{
		"T1": {knowledgeEvidenceLayerStore: insufficientKnowledgeEvidenceLayerSelection()},
		"T2": {knowledgeEvidenceLayerStore: insufficientKnowledgeEvidenceLayerSelection()},
	}
	if repaired := repairHighConfidenceInsufficientKnowledgeSelections(tasks, selections); repaired != 1 {
		t.Fatalf("only the grounded task should be rescued, repaired=%d selections=%#v", repaired, selections)
	}
	if got := selections["T1"][knowledgeEvidenceLayerStore]; got.Decision != knowledgeEvidenceDecisionDirectSingle || len(got.SupportedFacts) == 0 {
		t.Fatalf("first task must retain its rescued answer: %#v", got)
	}
	if got := selections["T2"][knowledgeEvidenceLayerStore]; got.Decision != knowledgeEvidenceDecisionInsufficient || len(got.SelectedCandidateIDs) != 0 || len(got.SupportedFacts) != 0 {
		t.Fatalf("unrelated second task must remain independently insufficient: %#v", got)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseDowngradesMissingCompoundSlot(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "task-1",
		Query:     "房间里有几瓶矿泉水，都是免费的吗",
		Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "task-1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "补充矿泉水", "问题：还要矿泉水怎么办\n答案：房间内矿泉水免费，可以去1313对面的洗衣房自取。", 0.91),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"task-1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["task-1C1"],"supportedFacts":[{"factId":"task-1F1","aspect":"price","statement":"房间内矿泉水免费。","criticalValues":["免费"]},{"factId":"task-1F2","aspect":"quantity","statement":"可以去1313对面的洗衣房自取。","criticalValues":["1313"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["task-1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionPartial || !containsString(selection.MissingAspects, "数量") {
		t.Fatalf("missing quantity must downgrade the selection to partial, got %#v", selection)
	}
	for _, fact := range selection.SupportedFacts {
		if fact.Aspect == "quantity" || strings.Contains(fact.Statement, "1313") {
			t.Fatalf("room number or location must not masquerade as quantity: %#v", selection.SupportedFacts)
		}
	}
}

func TestFilterKnowledgeEvidenceFactsKeepsOnlyCurrentObjective(t *testing.T) {
	facts := []knowledgeEvidenceFact{
		{FactID: "T1F1", Aspect: "existence", Statement: "酒店所有房间均配有空调，可使用控制面板或遥控器调节温度和风速。", CriticalValues: []string{"所有房间", "控制面板", "遥控器"}},
		{FactID: "T1F2", Aspect: "method", Statement: "也可以通过智能语音操控空调。", CriticalValues: []string{"智能语音"}},
		{FactID: "T1F3", Aspect: "other", Statement: "帮您解放双手。"},
	}
	filtered := filterKnowledgeEvidenceFactsForTask(knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "房间里有空调吗", Objective: "availability"}, facts)
	if len(filtered) != 1 || filtered[0].Aspect != "existence" {
		t.Fatalf("availability should retain only the existence fact, got %#v", filtered)
	}
	if strings.Contains(filtered[0].Statement, "控制面板") || strings.Contains(filtered[0].Statement, "智能语音") || strings.Contains(filtered[0].Statement, "解放双手") {
		t.Fatalf("availability fact must not carry method or marketing extensions: %#v", filtered[0])
	}

	invoiceFacts := []knowledgeEvidenceFact{
		{FactID: "T2F1", Aspect: "method", Statement: "退房后在自由家安心宿小程序申请发票。", CriticalValues: []string{"退房后", "小程序", "申请"}},
		{FactID: "T2F2", Aspect: "time", Statement: "发票会在申请后1至3个工作日上传。", CriticalValues: []string{"1至3个工作日"}},
	}
	methodOnly := filterKnowledgeEvidenceFactsForTask(knowledgeEvidenceJudgeTask{TaskID: "T2", Query: "发票怎么开", Objective: "method"}, invoiceFacts)
	if len(methodOnly) != 1 || methodOnly[0].Aspect != "method" {
		t.Fatalf("invoice method task must not repeat download time, got %#v", methodOnly)
	}
	timeOnly := filterKnowledgeEvidenceFactsForTask(knowledgeEvidenceJudgeTask{TaskID: "T3", Query: "多久能下载", Objective: "time"}, invoiceFacts)
	if len(timeOnly) != 1 || timeOnly[0].Aspect != "time" {
		t.Fatalf("invoice time task must not repeat application method, got %#v", timeOnly)
	}
}

func TestSplitKnowledgeEvidenceFAQTrimsTrainingMetadata(t *testing.T) {
	hit := judgeTestHit(1, 101, "开门", "问题：酒店房门怎么打开\n答案：完成登记扫人脸就可以开门啦，无需房卡。\n相似问题：怎么开房门、没有房卡怎么开门、刷脸能开门吗", 0.96)
	question, answer := splitKnowledgeEvidenceFAQForQuery(hit, "酒店房门怎么打开")
	if question != "酒店房门怎么打开" || !strings.Contains(answer, "无需房卡") {
		t.Fatalf("expected usable FAQ unit, question=%q answer=%q", question, answer)
	}
	if strings.Contains(answer, "相似问题") || strings.Contains(answer, "没有房卡怎么开门") {
		t.Fatalf("training metadata must not enter Judge facts: %q", answer)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseWithholdsUngroundedFacts(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "麦田房型有办公桌吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "麦田办公桌", "问题：麦田房型有办公桌吗\n答案：麦田房型配备办公桌。", 0.95),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"麦田房型配备沙发。","criticalValues":["麦田","沙发"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionInsufficient || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
		t.Fatalf("hallucinated fact must be withheld from ReplyPlan: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseDoesNotGroundFactsFromTitleOrTaskQuery(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "麦田房型配备沙发吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "麦田房型配备沙发", "问题：麦田房型有办公桌吗\n答案：麦田房型配备办公桌。", 0.95),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"麦田房型配备沙发。","criticalValues":["麦田","沙发"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionInsufficient || len(selection.SupportedFacts) != 0 {
		t.Fatalf("title and task query must not ground a fact missing from the selected FAQ answer: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseDoesNotJoinOneFactAcrossSelectedFAQs(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "麦田房型有办公桌吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "麦田设施", "问题：麦田房型有什么设施\n答案：麦田房型配备沙发。", 0.95)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "办公桌房型", "问题：哪些房型有办公桌\n答案：合柴房型配备办公桌。", 0.94)},
		},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"麦田房型配备办公桌。","criticalValues":["麦田","办公桌"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionInsufficient || len(selection.SupportedFacts) != 0 {
		t.Fatalf("one fact must be grounded by one selected FAQ unit, not values pooled across FAQs: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseComputesSelectedEnumerationIntersectionBeforeGrounding(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "哪些房型既有沙发又有办公桌？",
		SubIntent: "room_features",
		Objective: "availability",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "沙发", Type: "facility"},
			{Text: "办公桌", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "沙发房型", "问题：哪些房型有沙发\n答案：有沙发的房型包括合柴、艺林、塔川和岭南。", 0.91)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "办公桌房型", "问题：哪些房型有办公桌\n答案：有办公桌的房型包括合柴、麦田和艺林。", 0.89)},
		},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"scope","statement":"同时有沙发和办公桌的房型是合柴、艺林。","criticalValues":["合柴","艺林"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectCombined || len(selection.SupportedFacts) != 1 {
		t.Fatalf("selected complete enumerations must be intersected before single-FAQ grounding: %#v", selection)
	}
	fact := selection.SupportedFacts[0]
	if fact.Aspect != "scope" || !strings.Contains(fact.Statement, "合柴、艺林") || strings.Contains(fact.Statement, "麦田") || strings.Contains(fact.Statement, "塔川") {
		t.Fatalf("unexpected deterministic intersection fact: %#v", fact)
	}
	if len(selection.SelectedCandidateIDs) != 2 || selection.SelectedCandidateIDs[0] != "T1C1" || selection.SelectedCandidateIDs[1] != "T1C2" {
		t.Fatalf("intersection must use only the Judge-selected candidates: %#v", selection.SelectedCandidateIDs)
	}
}

func TestParseKnowledgeEvidenceJudgeResponsePreservesPartialEnumerationIntersection(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "哪些房型既有沙发又有办公桌？",
		SubIntent: "room_features",
		Objective: "availability",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "沙发", Type: "facility"},
			{Text: "办公桌", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "沙发房型", "问题：哪些房型有沙发\n答案：有沙发的房型包括合柴、艺林。", 0.91)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "办公桌房型", "问题：哪些房型有办公桌\n答案：部分房型配备办公桌，例如合柴、麦田。", 0.89)},
		},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"partial","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"scope","statement":"当前资料能确认同时有沙发和办公桌的房型包括合柴。","criticalValues":["合柴"]}],"missingAspects":["完整房型范围"]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionPartial || len(selection.SupportedFacts) != 1 || !containsString(selection.MissingAspects, "完整房型范围") {
		t.Fatalf("partial enumeration must remain explicitly partial: %#v", selection)
	}
	if !strings.Contains(selection.SupportedFacts[0].Statement, "当前资料能确认") || !containsString(selection.SupportedFacts[0].CriticalValues, "合柴") {
		t.Fatalf("unexpected partial intersection fact: %#v", selection.SupportedFacts)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsUnsafeEnumerationIntersection(t *testing.T) {
	baseTask := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "哪些房型既有沙发又有办公桌？",
		SubIntent: "room_features",
		Objective: "availability",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "沙发", Type: "facility"},
			{Text: "办公桌", Type: "facility"},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"scope","statement":"同时有沙发和办公桌的房型是合柴。","criticalValues":["合柴"]}],"missingAspects":[]}]}]}`

	for _, tt := range []struct {
		name       string
		candidates []knowledgeEvidenceJudgeCandidate
	}{
		{
			name: "empty intersection",
			candidates: []knowledgeEvidenceJudgeCandidate{
				{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "沙发房型", "问题：哪些房型有沙发\n答案：有沙发的房型包括合柴。", 0.91)},
				{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "办公桌房型", "问题：哪些房型有办公桌\n答案：有办公桌的房型包括麦田。", 0.89)},
			},
		},
		{
			name: "invalid enumeration",
			candidates: []knowledgeEvidenceJudgeCandidate{
				{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "沙发房型", "问题：哪些房型有沙发\n答案：有沙发的房型包括合柴、艺林。", 0.91)},
				{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "办公桌房型", "问题：哪些房型有办公桌\n答案：共3种房型，分别是合柴、麦田。", 0.89)},
			},
		},
		{
			name: "different object",
			candidates: []knowledgeEvidenceJudgeCandidate{
				{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "沙发房型", "问题：哪些房型有沙发\n答案：有沙发的房型包括合柴、艺林。", 0.91)},
				{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "办公桌会议室", "问题：哪些会议室有办公桌\n答案：有办公桌的会议室包括合柴、麦田。", 0.89)},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			task := baseTask
			task.Candidates = tt.candidates
			parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatalf("parse response: %v", err)
			}
			selection := parsed["T1"][knowledgeEvidenceLayerStore]
			if selection.Decision != knowledgeEvidenceDecisionInsufficient || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
				t.Fatalf("unsafe intersection must remain insufficient: %#v", selection)
			}
		})
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsConflictingDuplicateIntersectionOperand(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "哪些房型既有沙发又有办公桌？",
		SubIntent: "room_features",
		Objective: "availability",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "沙发", Type: "facility"},
			{Text: "办公桌", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "沙发房型一", "问题：哪些房型有沙发\n答案：有沙发的房型包括合柴、艺林。", 0.91)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "沙发房型二", "问题：哪些房型有沙发\n答案：有沙发的房型包括塔川、岭南。", 0.90)},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 103, "办公桌房型", "问题：哪些房型有办公桌\n答案：有办公桌的房型包括合柴、塔川。", 0.89)},
		},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2","T1C3"],"supportedFacts":[{"factId":"T1F1","aspect":"scope","statement":"同时有沙发和办公桌的房型是合柴。","criticalValues":["合柴"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionInsufficient || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
		t.Fatalf("conflicting duplicate operands must fail closed: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseAllowsEquivalentDuplicateIntersectionOperand(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "哪些房型既有沙发又有办公桌？",
		SubIntent: "room_features",
		Objective: "availability",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "沙发", Type: "facility"},
			{Text: "办公桌", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "沙发房型一", "问题：哪些房型有沙发\n答案：有沙发的房型包括合柴、艺林。", 0.91)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "沙发房型二", "问题：哪些房型有沙发\n答案：有沙发的房型包括艺林、合柴。", 0.90)},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 103, "办公桌房型", "问题：哪些房型有办公桌\n答案：有办公桌的房型包括合柴、麦田。", 0.89)},
		},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2","T1C3"],"supportedFacts":[{"factId":"T1F1","aspect":"scope","statement":"同时有沙发和办公桌的房型是合柴。","criticalValues":["合柴"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectCombined || len(selection.SelectedCandidateIDs) != 3 ||
		len(selection.SupportedFacts) != 1 || !containsString(selection.SupportedFacts[0].CriticalValues, "合柴") {
		t.Fatalf("equivalent duplicate operands may be collapsed without losing provenance: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsNegativeIntersectionOperand(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "哪些房型既有沙发又有办公桌？",
		SubIntent: "room_features",
		Objective: "availability",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "沙发", Type: "facility"},
			{Text: "办公桌", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "无沙发房型", "问题：哪些房型没有沙发\n答案：没有沙发的房型包括合柴、艺林。", 0.91)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "办公桌房型", "问题：哪些房型有办公桌\n答案：有办公桌的房型包括合柴、麦田。", 0.89)},
		},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"scope","statement":"同时有沙发和办公桌的房型是合柴。","criticalValues":["合柴"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionInsufficient || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
		t.Fatalf("negative enumeration must not be converted into an affirmative intersection: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseStillUsesAffirmativeFAQQuestionAndAnswerTogether(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "房间有几瓶矿泉水",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "矿泉水", "问题：房间内有两瓶矿泉水吗\n答案：是的，都是免费的。", 0.95),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"房间内有两瓶矿泉水。","criticalValues":["两瓶"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SupportedFacts) != 1 {
		t.Fatalf("affirmative FAQ answer must still confirm facts stated in its own question: %#v", selection)
	}
}

func TestKnowledgeEvidenceFAQAnswerConfirmationIsStrict(t *testing.T) {
	for _, answer := range []string{"是的。", "对的，具体以知识答案为准。", "没错！", "有的，房间内配备。"} {
		if !knowledgeEvidenceFAQAnswerConfirmsQuestion(answer) {
			t.Fatalf("strict affirmative answer should confirm its FAQ question: %q", answer)
		}
	}
	for _, answer := range []string{"可以联系门店确认。", "可以咨询同事。", "可以尝试申请。", "支持联系管家。", "提供咨询服务。", "不需要。", "不能。"} {
		if knowledgeEvidenceFAQAnswerConfirmsQuestion(answer) {
			t.Fatalf("guidance or negative answer must not confirm its FAQ question: %q", answer)
		}
	}
}

func TestHighConfidenceFAQDoesNotInventPriceFactFromGuidanceAnswer(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "延迟退房收费吗",
		Objective: "price",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "延迟退房", "问题：延迟退房收费吗\n答案：可以联系门店管家确认，具体情况以当天为准。", 0.96),
		}},
	}
	if selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("guidance answer must not be repaired into a direct price answer: %#v", selection)
	}
}

func TestDeterministicKnowledgeEvidenceHotelAspectCues(t *testing.T) {
	for _, tt := range []struct {
		name   string
		clause string
		aspect string
	}{
		{name: "door method", clause: "完成登记扫人脸就可以开门啦", aspect: "method"},
		{name: "washing machine quantity", clause: "洗衣房有两台洗衣机", aspect: "quantity"},
		{name: "towel quantity", clause: "房间有两条浴巾", aspect: "quantity"},
		{name: "laundry location", clause: "洗衣机在洗衣房", aspect: "location"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			aspect, _ := knowledgeEvidenceAnswerClauseAspect(tt.clause)
			if aspect != tt.aspect {
				t.Fatalf("unexpected aspect for %q: got %s want %s", tt.clause, aspect, tt.aspect)
			}
			fact := knowledgeEvidenceFact{Aspect: aspect, Statement: tt.clause}
			if !knowledgeEvidenceFactSupportsAspect(fact, tt.aspect) {
				t.Fatalf("classified fact must cover %s: %#v", tt.aspect, fact)
			}
		})
	}
	if aspect, _ := knowledgeEvidenceAnswerClauseAspect("1313对面的洗衣房自取"); aspect == "quantity" {
		t.Fatalf("room number must not be treated as a quantity")
	}
}

func TestReconcileSelectedFAQFactsUsesTaskObjectiveBoundary(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "房间里有空调吗？", Objective: "availability"}
	selection := knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		SelectedCandidateIDs: []string{"T1C1"},
		SupportedFacts: []knowledgeEvidenceFact{{
			FactID: "T1F1", Aspect: "existence", Statement: "酒店所有房间均配有空调。", CriticalValues: []string{"空调"},
		}},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"T1C1": {
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerGeneral,
			Hit:         judgeTestHit(7, 701, "空调", "问题：房间里有空调吗\n答案：酒店所有房间均配有空调。您可以使用控制面板或者遥控器自由调节温度和风速。同时也可以通过呼唤房内的智能语音操控空调。", 0.91),
		},
	}

	got := reconcileSelectedFAQGuidanceFactsForTask(task, knowledgeEvidenceLayerGeneral, selection, candidates)
	if len(got.SupportedFacts) != 1 || got.SupportedFacts[0].Aspect != "existence" || !strings.Contains(got.SupportedFacts[0].Statement, "配有空调") {
		t.Fatalf("availability task must not restore unasked control methods: %#v", got.SupportedFacts)
	}
}

func TestHighConfidenceExternalAddressFAQTreatsHowToFillAsLocation(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "酒店外卖地址应该怎么填？",
		Objective: "method",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(3, 301, "外卖地址", "问题：# 外卖地址\n问：外卖地址填哪些？\n答：丽斯未来酒店合肥南七店+对应楼层房间号。", 0.9047),
			},
			{
				CandidateID: "T1C2",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(3, 302, "订单修改", "问题：怎么改订单？\n答案：转接", 0.6726),
			},
		},
	}

	selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore)
	if !ok || selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("exact address-format FAQ must repair to a direct answer: %#v ok=%v", selection, ok)
	}
	if len(selection.SupportedFacts) != 1 || selection.SupportedFacts[0].Aspect != "location" || !strings.Contains(selection.SupportedFacts[0].Statement, "房间号") {
		t.Fatalf("address-format FAQ must provide the location value: %#v", selection.SupportedFacts)
	}
}

func TestBuildKnowledgeEvidenceJudgeTasksCarriesIntentObjectiveAndEntities(t *testing.T) {
	hit := judgeTestHit(1, 101, "麦田办公桌", "问题：麦田房型有办公桌吗\n答案：麦田房型配备办公桌。", 0.95)
	batch := &runtimeKnowledgeRetrieveBatch{Questions: []runtimeKnowledgeQuestionResult{{
		TaskID: "task-1",
		Query:  "麦田房型有办公桌吗？",
		Result: &retrievers.KnowledgeRetrieveResult{RawHits: []rag.RetrieveResult{hit}},
	}}}
	intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{{
		Intent:         "hotel_info",
		Objective:      "availability",
		ResolvedText:   "麦田房型有办公桌吗？",
		NeedsKnowledge: true,
		Entities: []callbacks.IntentEntityTraceData{
			{Text: "麦田", Type: "room_type"},
			{Text: "办公桌", Type: "facility"},
		},
	}}}

	tasks := buildKnowledgeEvidenceJudgeTasks(batch, []int64{1}, []int64{1}, nil, "麦田房型有办公桌吗？", intent)
	if len(tasks) != 1 || tasks[0].Objective != "availability" || len(tasks[0].Entities) != 2 {
		t.Fatalf("judge task lost intent metadata: %#v", tasks)
	}
}

func TestKnowledgeEvidenceJudgePromptSupportsFAQRehydrationAndSameLayerCombination(t *testing.T) {
	prompt := knowledgeEvidenceJudgeSystemPrompt()
	for _, required := range []string{"内部事实维度清单", "当前 layer 提供的全部候选逐条检查", "不能在看到第一条相关候选后提前停止", "必须判 direct_combined", "只要同层还有候选能补齐 missingAspects，就不得判 partial", "faqQuestion", "faqAnswer", "省略表达", "direct_combined", "partial", "supportedFacts", "missingAspects", "criticalValues", "严禁跨 store/general", "最小完整答案规则", "未被客户询问的路线/时长/价格/延伸建议不得加入", "普通动作词不得放入 criticalValues", "同一完整句已经覆盖多个维度", "禁止再输出被该完整句包含的摘要或碎片", "沙发", "办公桌", "房间内有两瓶矿泉水，并且免费", "足以回答“房间里有几瓶矿泉水”", "答案如果只是“转接”", "不是酒店事实", "不能让“转接”候选参与 direct_combined", "有外卖机器人", "不能生成“能送到房间”"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("expected judge prompt to contain %q, got %q", required, prompt)
		}
	}
	if !strings.Contains(prompt, "有空调吗") || !strings.Contains(prompt, "空调不制冷需要处理") {
		t.Fatalf("expected capability-versus-fault boundary to remain, got %q", prompt)
	}
}

func TestReconcileSelectedFAQGuidanceFactsRestoresOmittedAnswerUnit(t *testing.T) {
	selection := knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		SelectedCandidateIDs: []string{"task-4C1"},
		SupportedFacts: []knowledgeEvidenceFact{{
			FactID: "task-4F1", Aspect: "scope",
			Statement:      "每个客户在不同平台享受的平台权益是不一样的。",
			CriticalValues: []string{"携程", "抖音", "美团"},
		}},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"task-4C1": {
			CandidateID: "task-4C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: rag.RetrieveResult{
				Content: "问题：携程，抖音，美团的价格是一样的吗\n答案：每个客户在不同平台享受的平台权益是不一样的，建议您可以对比价格后选择合适您的。",
			},
		},
	}

	got := reconcileSelectedFAQGuidanceFactsForTask(knowledgeEvidenceJudgeTask{
		TaskID: "task-4", Query: "携程，抖音，美团的价格是一样的吗", Objective: "price",
	}, knowledgeEvidenceLayerStore, selection, candidates)
	if len(got.SupportedFacts) != 3 {
		t.Fatalf("selected FAQ condition and guidance must be restored as independent aspects, got %#v", got.SupportedFacts)
	}
	condition := got.SupportedFacts[1]
	if condition.FactID != "task-4F2" || condition.Aspect != "condition" || !strings.Contains(condition.Statement, "平台权益") {
		t.Fatalf("unexpected restored condition fact: %#v", condition)
	}
	guidance := got.SupportedFacts[2]
	if guidance.FactID != "task-4F3" || guidance.Aspect != "method" || !strings.Contains(guidance.Statement, "对比价格") || len(guidance.CriticalValues) != 0 {
		t.Fatalf("unexpected restored guidance fact: %#v", guidance)
	}
}

func TestReconcileSelectedFAQGuidanceFactsDoesNotDuplicateCoveredGuidance(t *testing.T) {
	selection := knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		SelectedCandidateIDs: []string{"T1C1"},
		SupportedFacts: []knowledgeEvidenceFact{{
			FactID: "T1F1", Aspect: "method", Statement: "建议客户比较价格后选择合适的平台。", CriticalValues: []string{"比较"},
		}},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"T1C1": {
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         rag.RetrieveResult{Content: "问题：平台价格一样吗\n答案：建议您可以对比价格后选择合适您的。"},
		},
	}

	got := reconcileSelectedFAQGuidanceFactsForTask(knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "平台价格一样吗", Objective: "price",
	}, knowledgeEvidenceLayerStore, selection, candidates)
	if len(got.SupportedFacts) != 1 {
		t.Fatalf("equivalent comparison guidance must not be duplicated, got %#v", got.SupportedFacts)
	}
	if len(got.SupportedFacts[0].CriticalValues) != 0 {
		t.Fatalf("comparison wording must remain semantic rather than literal: %#v", got.SupportedFacts)
	}
}

func TestReconcileSelectedFAQGuidanceFactsKeepsMissingCriticalOnMethodFact(t *testing.T) {
	selection := knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		SelectedCandidateIDs: []string{"T1C1"},
		SupportedFacts: []knowledgeEvidenceFact{{
			FactID: "T1F1", Aspect: "scope",
			Statement:      "不同平台的权益不一样，建议对比后选择合适的平台。",
			CriticalValues: []string{"平台权益不一样"},
		}},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"T1C1": {
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         rag.RetrieveResult{Content: "问题：平台价格一样吗\n答案：不同平台的权益不一样，建议您可以对比价格后选择合适您的。"},
		},
	}

	got := reconcileSelectedFAQGuidanceFactsForTask(knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "平台价格一样吗", Objective: "price",
	}, knowledgeEvidenceLayerStore, selection, candidates)
	if len(got.SupportedFacts) != 3 {
		t.Fatalf("scope, condition, and method facts must remain independent, got %#v", got.SupportedFacts)
	}
	method := got.SupportedFacts[2]
	if method.Aspect != "method" || len(method.CriticalValues) != 0 || !strings.Contains(method.Statement, "对比") {
		t.Fatalf("comparison action must remain in the fact statement without fixed wording, got %#v", method)
	}
	if containsString(got.SupportedFacts[0].CriticalValues, "对比") || containsString(got.SupportedFacts[1].CriticalValues, "对比") {
		t.Fatalf("method values must not migrate into other aspects: %#v", got.SupportedFacts)
	}
}

func TestFinalizeKnowledgeEvidenceFactsKeepsLiteralValuesAndDropsParaphrasableWords(t *testing.T) {
	facts := finalizeKnowledgeEvidenceFactsForTask(knowledgeEvidenceJudgeTask{}, []knowledgeEvidenceFact{
		{FactID: "T1F1", Aspect: "method", Statement: "具体情况可以联系门店管家18256022128。", CriticalValues: []string{"联系", "18256022128"}},
		{FactID: "T1F2", Aspect: "method", Statement: "请回复“确认”或“取消”。", CriticalValues: []string{"回复", "确认", "取消"}},
		{FactID: "T1F3", Aspect: "time", Statement: "早餐时间是7:00-9:30。", CriticalValues: []string{"建议", "7:00-9:30", "1"}},
	})
	if len(facts) != 3 {
		t.Fatalf("literal-bearing facts must remain, got %#v", facts)
	}
	if containsString(facts[0].CriticalValues, "联系") || !containsString(facts[0].CriticalValues, "18256022128") {
		t.Fatalf("phone must remain without forcing contact wording: %#v", facts[0])
	}
	if containsString(facts[1].CriticalValues, "回复") || !containsString(facts[1].CriticalValues, "确认") || !containsString(facts[1].CriticalValues, "取消") {
		t.Fatalf("fixed options must remain without forcing reply wording: %#v", facts[1])
	}
	if containsString(facts[2].CriticalValues, "建议") || containsString(facts[2].CriticalValues, "1") || !containsString(facts[2].CriticalValues, "7:00-9:30") {
		t.Fatalf("time must remain while list markers and suggestions are removed: %#v", facts[2])
	}
}

func TestSanitizeKnowledgeEvidenceCriticalValuesDistinguishesOptionsFromListMarkers(t *testing.T) {
	for _, tt := range []struct {
		name      string
		statement string
		values    []string
		want      []string
	}{
		{name: "quoted options", statement: "请回复“1”或“2”。", values: []string{"回复", "1", "2"}, want: []string{"1", "2"}},
		{name: "plain options", statement: "回复1或2都可以。", values: []string{"1", "2"}, want: []string{"1", "2"}},
		{name: "single digit list", statement: "1. 早餐时间", values: []string{"1"}},
		{name: "double digit list", statement: "10、早餐时间", values: []string{"10"}},
		{name: "metro line", statement: "乘坐地铁1号线。", values: []string{"1"}, want: []string{"1"}},
		{name: "floor", statement: "洗衣房在8楼。", values: []string{"8"}, want: []string{"8"}},
		{name: "room number", statement: "房间号是1313。", values: []string{"1313"}, want: []string{"1313"}},
		{name: "phone", statement: "电话是18256022128。", values: []string{"18256022128"}, want: []string{"18256022128"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeKnowledgeEvidenceCriticalValuesForStatement(tt.values, tt.statement)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("unexpected sanitized values: got=%#v want=%#v", got, tt.want)
			}
		})
	}
}

func TestReconcileSelectedFAQAnswerClausesKeepsNegativeBoundaryAndMethod(t *testing.T) {
	selection := knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		SelectedCandidateIDs: []string{"T1C1"},
		SupportedFacts: []knowledgeEvidenceFact{{
			FactID: "T1F1", Aspect: "method", Statement: "可以通过入住机或小程序线上智能化方式办理入住。",
		}},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"T1C1": {
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: rag.RetrieveResult{
				Content: "问题：怎么办理入住\n答案：我们酒店没有传统前台，你可以通过入住机或小程序线上智能化方式办理入住。酒店不提供早餐，也不支持微信支付。",
			},
		},
	}

	got := reconcileSelectedFAQGuidanceFactsForTask(knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "怎么办理入住", Objective: "method",
	}, knowledgeEvidenceLayerStore, selection, candidates)
	if len(got.SupportedFacts) != 2 {
		t.Fatalf("negative service boundary and check-in method must both be preserved, got %#v", got.SupportedFacts)
	}
	var boundary *knowledgeEvidenceFact
	for index := range got.SupportedFacts {
		if strings.Contains(got.SupportedFacts[index].Statement, "没有传统前台") {
			boundary = &got.SupportedFacts[index]
			break
		}
	}
	if boundary == nil || boundary.Aspect != "existence" || !containsString(boundary.CriticalValues, "传统前台") {
		t.Fatalf("missing grounded traditional-front-desk boundary: %#v", got.SupportedFacts)
	}
	for _, fact := range got.SupportedFacts {
		if strings.Contains(fact.Statement, "早餐") || strings.Contains(fact.Statement, "微信支付") {
			t.Fatalf("unrelated negative FAQ extension or channel domain must not be restored: %#v", got.SupportedFacts)
		}
	}
}

func TestReconcileSelectedFAQAnswerClausesKeepsSameDomainDoorBoundary(t *testing.T) {
	selection := knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		SelectedCandidateIDs: []string{"T1C1"},
		SupportedFacts: []knowledgeEvidenceFact{{
			FactID: "T1F1", Aspect: "method", Statement: "完成登记后扫人脸开门。", CriticalValues: []string{"人脸", "开门"},
		}},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"T1C1": {
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         rag.RetrieveResult{Content: "问题：酒店房门怎么打开\n答案：完成登记后扫人脸开门；无需房卡。"},
		},
	}
	got := reconcileSelectedFAQGuidanceFactsForTask(knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "酒店房门怎么打开", Objective: "method",
	}, knowledgeEvidenceLayerStore, selection, candidates)
	combined := ""
	for _, fact := range got.SupportedFacts {
		combined += fact.Statement
	}
	if !strings.Contains(combined, "扫人脸") || !strings.Contains(combined, "无需房卡") {
		t.Fatalf("same-domain access method and boundary must both remain: %#v", got.SupportedFacts)
	}
}

func TestReconcileSelectedFAQAnswerClausesAddsNewRequiredValuesWithinSameAspect(t *testing.T) {
	for _, tt := range []struct {
		name         string
		task         knowledgeEvidenceJudgeTask
		initialFact  knowledgeEvidenceFact
		content      string
		mustContain  []string
		mustCritical []string
	}{
		{
			name:         "check-in channels",
			task:         knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "怎么办理入住", Objective: "method"},
			initialFact:  knowledgeEvidenceFact{FactID: "T1F1", Aspect: "method", Statement: "可以通过入住机办理入住。", CriticalValues: []string{"入住机"}},
			content:      "问题：怎么办理入住\n答案：可以通过入住机办理入住；也可以通过小程序办理入住。",
			mustContain:  []string{"入住机", "小程序"},
			mustCritical: []string{"小程序"},
		},
		{
			name:         "delivery address components",
			task:         knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "外卖地址怎么填", Objective: "location"},
			initialFact:  knowledgeEvidenceFact{FactID: "T1F1", Aspect: "location", Statement: "外卖地址填写丽斯未来酒店合肥南七店。", CriticalValues: []string{"丽斯未来酒店合肥南七店"}},
			content:      "问题：外卖地址怎么填\n答案：外卖地址填写丽斯未来酒店合肥南七店；还要加对应楼层房间号。",
			mustContain:  []string{"丽斯未来酒店合肥南七店", "对应楼层房间号"},
			mustCritical: []string{"房号"},
		},
		{
			name:         "wifi account and password",
			task:         knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "WiFi账号密码是多少"},
			initialFact:  knowledgeEvidenceFact{FactID: "T1F1", Aspect: "other", Statement: "WiFi账号是LISI。", CriticalValues: []string{"LISI"}},
			content:      "问题：WiFi账号密码是多少\n答案：WiFi账号是LISI；WiFi密码是lis888888。",
			mustContain:  []string{"LISI", "lis888888"},
			mustCritical: []string{"lis888888"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			selection := knowledgeEvidenceLayerSelection{
				Decision:             knowledgeEvidenceDecisionDirectSingle,
				SelectedCandidateIDs: []string{"T1C1"},
				SupportedFacts:       []knowledgeEvidenceFact{tt.initialFact},
			}
			candidates := map[string]knowledgeEvidenceJudgeCandidate{
				"T1C1": {CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: tt.content}},
			}
			got := reconcileSelectedFAQGuidanceFactsForTask(tt.task, knowledgeEvidenceLayerStore, selection, candidates)
			combined := ""
			criticalValues := make([]string, 0, 4)
			for _, fact := range got.SupportedFacts {
				combined += fact.Statement
				criticalValues = appendKnowledgeEvidenceCriticalValues(criticalValues, fact.CriticalValues)
			}
			for _, required := range tt.mustContain {
				if !strings.Contains(combined, required) {
					t.Fatalf("missing required same-aspect value %q: %#v", required, got.SupportedFacts)
				}
			}
			for _, required := range tt.mustCritical {
				if !knowledgeEvidenceContainsString(criticalValues, required) {
					t.Fatalf("missing required critical value %q: %#v", required, got.SupportedFacts)
				}
			}
		})
	}
}

func TestReconcileSelectedFAQAnswerClausesKeepsOnlyFactsRequiredByCurrentTask(t *testing.T) {
	selection := knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		SelectedCandidateIDs: []string{"T1C1"},
		SupportedFacts: []knowledgeEvidenceFact{{
			FactID: "T1F1", Aspect: "quantity", Statement: "房间内有两瓶矿泉水。",
		}},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"T1C1": {
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: rag.RetrieveResult{
				Content: "问题：房间矿泉水怎么提供\n答案：房间内有两瓶矿泉水，都是免费的；每日补充时间为10:00。",
			},
		},
	}

	got := reconcileSelectedFAQGuidanceFactsForTask(knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "房间里有几瓶矿泉水", Objective: "quantity",
	}, knowledgeEvidenceLayerStore, selection, candidates)
	if len(got.SupportedFacts) != 1 || got.SupportedFacts[0].Aspect != "quantity" || !strings.Contains(got.SupportedFacts[0].Statement, "两瓶") {
		t.Fatalf("a quantity-only task must not inherit unasked price and replenishment time, got %#v", got.SupportedFacts)
	}
}

func TestReconcileSelectedFAQAnswerClausesIgnoresUnselectedCandidates(t *testing.T) {
	selection := knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		SelectedCandidateIDs: []string{"T1C1"},
		SupportedFacts: []knowledgeEvidenceFact{{
			FactID: "T1F1", Aspect: "method", Statement: "可以通过小程序办理入住。",
		}},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"T1C1": {
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         rag.RetrieveResult{Content: "问题：怎么办理入住\n答案：可以通过小程序办理入住。"},
		},
		"T1C2": {
			CandidateID: "T1C2",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         rag.RetrieveResult{Content: "问题：房间有几瓶水\n答案：房间内有两瓶矿泉水。"},
		},
	}

	got := reconcileSelectedFAQGuidanceFacts("T1", knowledgeEvidenceLayerStore, selection, candidates)
	if len(got.SupportedFacts) != 1 || strings.Contains(got.SupportedFacts[0].Statement, "矿泉水") {
		t.Fatalf("unselected candidate facts must not be restored, got %#v", got.SupportedFacts)
	}
}

func TestReconcileSelectedFAQAnswerClausesIgnoresEmptyAndCourtesyClauses(t *testing.T) {
	selection := knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		SelectedCandidateIDs: []string{"T1C1"},
		SupportedFacts: []knowledgeEvidenceFact{{
			FactID: "T1F1", Aspect: "method", Statement: "可以通过小程序办理入住。",
		}},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"T1C1": {
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: rag.RetrieveResult{
				Content: "问题：怎么办理入住\n答案：您好。感谢您的咨询。。祝您入住愉快！可以通过小程序办理入住。",
			},
		},
	}

	got := reconcileSelectedFAQGuidanceFacts("T1", knowledgeEvidenceLayerStore, selection, candidates)
	if len(got.SupportedFacts) != 1 {
		t.Fatalf("empty and courtesy-only clauses must not become facts, got %#v", got.SupportedFacts)
	}
}

func TestBuildKnowledgeEvidenceJudgePromptSeparatesFastGPTFAQQuestionAnswerAndRaw(t *testing.T) {
	raw := "问题：问下房间的两瓶矿泉水是免费的吗？\n答案：是的，房间内的矿泉水都是免费的"
	prompt := buildKnowledgeEvidenceJudgePrompt([]knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "房间里有几瓶矿泉水",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "问下房间的两瓶矿泉水是免费的吗？", raw, 0.91),
		}},
	}})
	got := prompt.Tasks[0].Candidates[0]
	if got.FAQQuestion != "问下房间的两瓶矿泉水是免费的吗？" {
		t.Fatalf("FAQ question was not separated: %#v", got)
	}
	if got.FAQAnswer != "是的，房间内的矿泉水都是免费的" {
		t.Fatalf("FAQ answer was not separated: %#v", got)
	}
	if got.RawContent != raw {
		t.Fatalf("raw content must remain auditable: %#v", got)
	}
}

func TestBuildKnowledgeEvidenceJudgePromptIncludesTaskSemantics(t *testing.T) {
	prompt := buildKnowledgeEvidenceJudgePrompt([]knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "早餐几点",
		SubIntent: "breakfast",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
	}})
	if len(prompt.Tasks) != 1 {
		t.Fatalf("expected one prompt task, got %#v", prompt.Tasks)
	}
	task := prompt.Tasks[0]
	if task.SubIntent != "breakfast" || task.Objective != "time" || len(task.Entities) != 1 || task.Entities[0].Text != "早餐" || task.Entities[0].Type != "meal" {
		t.Fatalf("task semantics must be disclosed to Judge: %#v", task)
	}
}

func TestHighConfidenceDirectFAQSelectionRescuesExactQuestionAtMediumVectorScore(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "外卖地址怎么填",
		SubIntent: "delivery_address",
		Objective: "location",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "外卖地址", Type: "location"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "外卖地址", "问题：外卖地址怎么填\n答案：外卖地址填写安徽省合肥市蜀山区望江西路与肥西路交口。", 0.72),
		}},
	}
	selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore)
	if !ok || selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SupportedFacts) == 0 {
		t.Fatalf("exact FAQ question must rescue a medium vector score: %#v ok=%v", selection, ok)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsDifferentExplicitSubject(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "早餐几点",
		SubIntent: "breakfast",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "退房时间", "问题：最晚几点退房\n答案：退房时间是12:00。", 0.99),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"退房时间是12:00。","criticalValues":["12:00"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionInsufficient || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
		t.Fatalf("breakfast task must not select checkout evidence: %#v", selection)
	}
}

func TestCanonicalizeKnowledgeEvidenceFactsKeepsFactDimensionsAndPolaritySeparate(t *testing.T) {
	checkin := canonicalizeKnowledgeEvidenceFacts([]knowledgeEvidenceFact{
		{FactID: "T1F1", Aspect: "method", Statement: "酒店没有传统前台，您可以通过入住机或小程序线上智能化方式办理入住。", CriticalValues: []string{"传统前台", "入住机", "小程序"}},
		{FactID: "T1F2", Aspect: "existence", Statement: "酒店没有传统前台。", CriticalValues: []string{"传统前台"}},
		{FactID: "T1F3", Aspect: "method", Statement: "您可以通过入住机或小程序线上智能化方式办理入住。", CriticalValues: []string{"入住机", "小程序"}},
	})
	if len(checkin) != 3 || checkin[0].Statement == checkin[1].Statement || checkin[0].Statement == checkin[2].Statement {
		t.Fatalf("method and negative existence facts must remain independent: %#v", checkin)
	}

	water := canonicalizeKnowledgeEvidenceFacts([]knowledgeEvidenceFact{
		{FactID: "T2F1", Aspect: "price", Statement: "房间内的矿泉水均免费。", CriticalValues: []string{"免费"}},
		{FactID: "T2F2", Aspect: "quantity", Statement: "房间的两瓶矿泉水是免费的。", CriticalValues: []string{"两瓶", "免费"}},
		{FactID: "T2F3", Aspect: "price", Statement: "房间内的矿泉水都是免费的。", CriticalValues: []string{"免费"}},
	})
	if len(water) != 3 || water[0].Aspect != "price" || water[1].Aspect != "quantity" || water[2].Aspect != "price" || water[0].Statement == water[1].Statement {
		t.Fatalf("similar wording must not collapse distinct fact records across dimensions: %#v", water)
	}
	if containsString(water[0].CriticalValues, "两瓶") || !containsString(water[1].CriticalValues, "两瓶") || containsString(water[2].CriticalValues, "两瓶") {
		t.Fatalf("quantity values must not migrate into the price fact: %#v", water)
	}

	waterWithSplitCriticalValues := canonicalizeKnowledgeEvidenceFacts([]knowledgeEvidenceFact{
		{FactID: "T3F1", Aspect: "price", Statement: "房间内的矿泉水是免费的。", CriticalValues: []string{"免费"}},
		{FactID: "T3F2", Aspect: "quantity", Statement: "房间的两瓶矿泉水是免费的。", CriticalValues: []string{"两瓶"}},
	})
	if len(waterWithSplitCriticalValues) != 2 || waterWithSplitCriticalValues[0].Statement == waterWithSplitCriticalValues[1].Statement {
		t.Fatalf("complementary fact dimensions must remain separate: %#v", waterWithSplitCriticalValues)
	}
}

func TestCanonicalizeKnowledgeEvidenceFactsKeepsPositiveNegativeAndObjectsSeparate(t *testing.T) {
	facts := canonicalizeKnowledgeEvidenceFacts([]knowledgeEvidenceFact{
		{FactID: "T1F1", Aspect: "existence", Statement: "麦田房型配备办公桌。", CriticalValues: []string{"麦田", "办公桌"}},
		{FactID: "T1F2", Aspect: "existence", Statement: "麦田房型没有办公桌。", CriticalValues: []string{"麦田", "办公桌"}},
		{FactID: "T1F3", Aspect: "existence", Statement: "合柴房型配备办公桌。", CriticalValues: []string{"合柴", "办公桌"}},
	})
	if len(facts) != 3 {
		t.Fatalf("opposite polarity and different room types must never merge: %#v", facts)
	}
	for left := 0; left < len(facts); left++ {
		for right := left + 1; right < len(facts); right++ {
			if facts[left].Statement == facts[right].Statement {
				t.Fatalf("independent facts must keep distinct statements: %#v", facts)
			}
		}
	}
}

func TestReconcileSelectedFAQFactsComputesCompleteEnumerationIntersection(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "哪些房型既有沙发又有办公桌？",
		SubIntent: "room_features",
		Objective: "availability",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "沙发", Type: "facility"},
			{Text: "办公桌", Type: "facility"},
		},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"T1C1": {CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "沙发房型", "问题：哪些房型有沙发\n答案：有沙发的房型包括合柴、艺林、塔川和岭南。", 0.91)},
		"T1C2": {CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "办公桌房型", "问题：哪些房型有办公桌\n答案：有办公桌的房型包括合柴、麦田和艺林。", 0.89)},
	}
	task.Candidates = []knowledgeEvidenceJudgeCandidate{candidates["T1C1"], candidates["T1C2"]}
	selection := knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectCombined,
		DecisionSource:       "model",
		SelectedCandidateIDs: []string{"T1C1", "T1C2"},
	}
	got := reconcileSelectedFAQGuidanceFactsForTask(task, knowledgeEvidenceLayerStore, selection, candidates)
	if got.Decision != knowledgeEvidenceDecisionDirectCombined || len(got.SupportedFacts) != 1 {
		t.Fatalf("complete enumerations must produce one intersection fact: %#v", got)
	}
	fact := got.SupportedFacts[0]
	if fact.Aspect != "scope" || !strings.Contains(fact.Statement, "合柴、艺林") || strings.Contains(fact.Statement, "麦田") || strings.Contains(fact.Statement, "塔川") {
		t.Fatalf("unexpected intersection fact: %#v", fact)
	}
	if len(fact.CriticalValues) != 2 || fact.CriticalValues[0] != "合柴" || fact.CriticalValues[1] != "艺林" {
		t.Fatalf("only intersection members may remain mandatory: %#v", fact.CriticalValues)
	}
	if got.DecisionSource != "model" {
		t.Fatalf("reconciliation must preserve the model decision source, got %q", got.DecisionSource)
	}
}

func TestReconcileSelectedFAQFactsComputesIntersectionFromRealKnowledgePhrasing(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "哪些房型既有沙发又有办公桌？",
		SubIntent: "room_features",
		Objective: "availability",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "沙发", Type: "facility"},
			{Text: "办公桌", Type: "facility"},
		},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"T1C1": {CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "沙发房型", "问题：哪些房型有沙发\n答案：我们合柴、艺林、塔川、岭南四种房型是配备沙发的。", 0.91)},
		"T1C2": {CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "办公桌房型", "问题：哪些房型有办公桌\n答案：酒店部分房型配备办公桌，如合柴、麦田和艺林。", 0.89)},
	}
	task.Candidates = []knowledgeEvidenceJudgeCandidate{candidates["T1C1"], candidates["T1C2"]}
	selection := knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectCombined,
		SelectedCandidateIDs: []string{"T1C1", "T1C2"},
	}

	got := reconcileSelectedFAQGuidanceFactsForTask(task, knowledgeEvidenceLayerStore, selection, candidates)
	if got.Decision != knowledgeEvidenceDecisionPartial || len(got.SupportedFacts) != 1 || !containsString(got.MissingAspects, "完整房型范围") {
		t.Fatalf("partial enumeration intersection must preserve its missing scope: %#v", got)
	}
	fact := got.SupportedFacts[0]
	if !strings.Contains(fact.Statement, "当前资料能确认") || !strings.Contains(fact.Statement, "合柴、艺林") ||
		strings.Contains(fact.Statement, "麦田") || strings.Contains(fact.Statement, "塔川") || strings.Contains(fact.Statement, "岭南") {
		t.Fatalf("intersection must contain only confirmed shared room types: %#v", fact)
	}
}

func TestKnowledgeEvidenceEnumerationCompletenessClassification(t *testing.T) {
	for _, tt := range []struct {
		name         string
		answer       string
		label        string
		completeness knowledgeEvidenceEnumerationCompleteness
		members      []string
	}{
		{name: "complete include", answer: "有办公桌的房型包括合柴、麦田。", completeness: knowledgeEvidenceEnumerationComplete, members: []string{"合柴", "麦田"}},
		{name: "partial etc", answer: "有办公桌的房型包括合柴、麦田等房型。", completeness: knowledgeEvidenceEnumerationPartial, members: []string{"合柴", "麦田"}},
		{name: "partial other room types", answer: "有办公桌的房型包括合柴、麦田等其他房型。", completeness: knowledgeEvidenceEnumerationPartial, members: []string{"合柴", "麦田"}},
		{name: "partial common room types", answer: "有办公桌的房型包括合柴、麦田等常见房型。", completeness: knowledgeEvidenceEnumerationPartial, members: []string{"合柴", "麦田"}},
		{name: "partial including but not limited to prefix", answer: "有办公桌的房型包括但不限于合柴、麦田。", completeness: knowledgeEvidenceEnumerationPartial, members: []string{"合柴", "麦田"}},
		{name: "partial including but not limited to suffix", answer: "有办公桌的房型包括合柴、麦田，但不限于其他房型。", completeness: knowledgeEvidenceEnumerationPartial, members: []string{"合柴", "麦田"}},
		{name: "partial examples", answer: "酒店部分房型配备办公桌，例如合柴、麦田。", completeness: knowledgeEvidenceEnumerationPartial, members: []string{"合柴", "麦田"}},
		{name: "following list", answer: "有办公桌的房型如下：合柴、麦田。", completeness: knowledgeEvidenceEnumerationComplete, members: []string{"合柴", "麦田"}},
		{name: "matching declared count", answer: "共2种房型，分别是合柴、麦田。", completeness: knowledgeEvidenceEnumerationComplete, members: []string{"合柴", "麦田"}},
		{name: "spoken lead with declared count", answer: "我们合柴、艺林、塔川、岭南四种房型是配备沙发的。", label: "沙发", completeness: knowledgeEvidenceEnumerationComplete, members: []string{"合柴", "艺林", "塔川", "岭南"}},
		{name: "mismatched declared count", answer: "共3种房型，分别是合柴、麦田。", completeness: knowledgeEvidenceEnumerationInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			label := tt.label
			if label == "" {
				label = "办公桌"
			}
			got := parseKnowledgeEvidenceIntersectionEnumeration(tt.answer, label)
			if got.Completeness != tt.completeness {
				t.Fatalf("unexpected completeness for %q: got %s want %s (%#v)", tt.answer, got.Completeness, tt.completeness, got)
			}
			if len(tt.members) == 0 {
				if len(got.Members) != 0 {
					t.Fatalf("invalid enumeration must not expose members: %#v", got)
				}
				return
			}
			if len(got.Members) != len(tt.members) {
				t.Fatalf("unexpected members for %q: %#v", tt.answer, got.Members)
			}
			for index := range tt.members {
				if got.Members[index] != tt.members[index] || strings.HasPrefix(got.Members[index], "下") {
					t.Fatalf("unexpected member parsing for %q: %#v", tt.answer, got.Members)
				}
			}
		})
	}
}

func TestIntersectionRepairNeverUsesUnselectedCandidates(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "哪些房型既有沙发又有办公桌？",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "沙发", Type: "facility"}, {Text: "办公桌", Type: "facility"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "沙发房型", "问题：哪些房型有沙发\n答案：有沙发的房型包括合柴、艺林。", 0.91)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "办公桌房型", "问题：哪些房型有办公桌\n答案：有办公桌的房型包括合柴、麦田。", 0.20)},
		},
	}
	if got, ok := deterministicKnowledgeEvidenceIntersectionSelection(task, knowledgeEvidenceLayerStore, []string{"T1C1"}); ok {
		t.Fatalf("intersection must not consume an unselected candidate: %#v", got)
	}
}

func TestCanonicalizeKnowledgeEvidenceFactsKeepsDifferentConditionsSeparate(t *testing.T) {
	facts := canonicalizeKnowledgeEvidenceFacts([]knowledgeEvidenceFact{
		{FactID: "T1F1", Aspect: "time", Statement: "工作日早餐时间是7:00到9:00。", CriticalValues: []string{"工作日", "7:00", "9:00"}},
		{FactID: "T1F2", Aspect: "time", Statement: "周末早餐时间是8:00到10:00。", CriticalValues: []string{"周末", "8:00", "10:00"}},
	})
	if len(facts) != 2 || facts[0].Statement == facts[1].Statement {
		t.Fatalf("facts with different conditions and values must remain separate: %#v", facts)
	}
}

func TestKnowledgeEvidenceJudgeUsesOnlySelectedSameLayerCombinedEvidence(t *testing.T) {
	storeSofa := judgeTestHit(1, 101, "沙发房型", "合柴、艺林、塔川、岭南带沙发。", 0.91)
	storeDesk := judgeTestHit(1, 102, "办公桌房型", "合柴、麦田、艺林带办公桌。", 0.89)
	storeUnselected := judgeTestHit(1, 103, "茶几房型", "岭南、合柴、塔川、积木带茶几。", 0.87)
	generalAnswer := judgeTestHit(2, 201, "通用房型", "通用房型可能同时有沙发和办公桌。", 0.99)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"既有沙发又有办公桌的房间有哪些": {
			KnowledgeBaseIDs: []int64{1, 2},
			RawHits:          []rag.RetrieveResult{storeSofa, storeDesk, storeUnselected, generalAnswer},
			Hits:             []rag.RetrieveResult{storeSofa, storeDesk, storeUnselected},
			ContextResults:   []rag.RetrieveResult{storeSofa, storeDesk, storeUnselected},
			ContextText:      storeSofa.Content + "\n" + storeDesk.Content + "\n" + storeUnselected.Content,
		},
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(_ []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return knowledgeEvidenceJudgeOutcome{
			Applied: true,
			Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
				"T1": {
					knowledgeEvidenceLayerStore: {
						Decision:             knowledgeEvidenceDecisionDirectCombined,
						SelectedCandidateIDs: []string{"T1C1", "T1C2"},
					},
					knowledgeEvidenceLayerGeneral: {
						Decision:             knowledgeEvidenceDecisionDirectSingle,
						SelectedCandidateIDs: []string{"T1C4"},
					},
				},
			},
			Trace: callbacks.KnowledgeEvidenceJudgeTraceData{SchemaVersion: knowledgeEvidenceJudgeSchemaVersion, Status: "completed"},
		}
	}}
	collector := callbacks.NewRuntimeTraceCollector()
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("既有沙发又有办公桌的房间有哪些", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.RetrieveResult == nil || len(state.RetrieveResult.Hits) != 2 {
		t.Fatalf("expected exactly two selected store hits, got %#v", state.RetrieveResult)
	}
	contextText := state.RetrieveResult.ContextText
	if !strings.Contains(contextText, "带沙发") || !strings.Contains(contextText, "带办公桌") {
		t.Fatalf("combined evidence missing from Generate context: %q", contextText)
	}
	if strings.Contains(contextText, "带茶几") || strings.Contains(contextText, "通用房型") {
		t.Fatalf("unselected or losing-layer evidence leaked into Generate: %q", contextText)
	}
	trace := collector.Data.Pipeline.EvidenceJudge.Tasks[0]
	if trace.SelectedLayer != knowledgeEvidenceLayerStore || trace.Decision != knowledgeEvidenceDecisionDirectCombined || len(trace.SelectedCandidateIDs) != 2 {
		t.Fatalf("unexpected V2 trace: %#v", trace)
	}
}

func TestKnowledgeEvidenceJudgeStoreHandoffWinsGeneralCompleteAnswer(t *testing.T) {
	storeHandoff := judgeTestHit(1, 101, "遗失物", "问题：东西落在房间怎么办\n答案：转接", 0.82)
	generalAnswer := judgeTestHit(2, 201, "通用遗失物", "问题：东西落在房间怎么办\n答案：可以稍后自行回来取。", 0.99)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"东西落在房间怎么办": judgeTestRetrieveResult(storeHandoff, generalAnswer),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(_ []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return knowledgeEvidenceJudgeOutcome{
			Applied: true,
			Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
				"T1": {
					knowledgeEvidenceLayerStore: {
						Decision:             knowledgeEvidenceDecisionDirectSingle,
						SelectedCandidateIDs: []string{"T1C1"},
					},
					knowledgeEvidenceLayerGeneral: {
						Decision:             knowledgeEvidenceDecisionDirectSingle,
						SelectedCandidateIDs: []string{"T1C2"},
						SupportedFacts: []knowledgeEvidenceFact{{
							FactID: "T1F1", Aspect: "method", Statement: "可以稍后自行回来取。",
						}},
					},
				},
			},
			Trace: callbacks.KnowledgeEvidenceJudgeTraceData{SchemaVersion: knowledgeEvidenceJudgeSchemaVersion, Status: "completed"},
		}
	}}
	summary := &RunResult{}
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("东西落在房间怎么办", "1"),
		Summary: summary,
		Intent:  hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !summary.handoffDirective || summary.handoffDirectiveSource != "knowledge_top_answer" {
		t.Fatalf("store handoff must win over a general complete answer, got %#v", summary)
	}
	if state.RetrieveResult == nil || strings.Contains(state.RetrieveResult.ContextText, "自行回来取") {
		t.Fatalf("losing general answer leaked into Generate context: %#v", state.RetrieveResult)
	}
}

func TestKnowledgeEvidenceJudgeOnlyExposesSelectedFAQUnit(t *testing.T) {
	storeHit := judgeTestHit(1, 101, "入住与服务", `问题：怎么办理入住
	答案：我们酒店没有传统前台，可以通过入住机或小程序线上办理入住。
问题：马桶堵了怎么办
答案：转接`, 0.96)
	generalHit := judgeTestHit(2, 201, "通用入住", "问题：酒店可以入住吗\n答案：请以门店实际情况为准。", 0.61)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"怎么办理入住": judgeTestRetrieveResult(storeHit, generalHit),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(_ []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return knowledgeEvidenceJudgeOutcome{
			Applied: true,
			Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
				"T1": {
					knowledgeEvidenceLayerStore: {
						Decision:             knowledgeEvidenceDecisionDirectSingle,
						SelectedCandidateIDs: []string{"T1C1"},
						SupportedFacts: []knowledgeEvidenceFact{{
							FactID: "T1F1", Aspect: "method", Statement: "可以通过入住机或小程序线上办理入住。", CriticalValues: []string{"入住机", "小程序"},
						}},
					},
				},
			},
			Trace: callbacks.KnowledgeEvidenceJudgeTraceData{SchemaVersion: knowledgeEvidenceJudgeSchemaVersion, Status: "completed"},
		}
	}}
	summary := &RunResult{}
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("怎么办理入住", "1"),
		Summary: summary,
		Intent:  hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if summary.handoffDirective {
		t.Fatal("an unrelated handoff FAQ in the same chunk must not trigger handoff")
	}
	if state.RetrieveResult == nil || !strings.Contains(state.RetrieveResult.ContextText, "入住机") || strings.Contains(state.RetrieveResult.ContextText, "马桶堵了") || strings.Contains(state.RetrieveResult.ContextText, "答案：转接") {
		t.Fatalf("Generate must see only the selected FAQ unit: %#v", state.RetrieveResult)
	}
}

func TestKnowledgeEvidenceJudgeGeneralCompleteWinsStorePartial(t *testing.T) {
	storePartial := judgeTestHit(1, 101, "外卖机器人", "问题：有外卖机器人吗\n答案：有外卖机器人的。", 0.93)
	generalComplete := judgeTestHit(2, 201, "机器人范围", "问题：外卖机器人能送到哪里\n答案：机器人可以送到房门口。", 0.79)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"外卖机器人能送到房间吗": judgeTestRetrieveResult(storePartial, generalComplete),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(_ []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return knowledgeEvidenceJudgeOutcome{
			Applied: true,
			Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
				"T1": {
					knowledgeEvidenceLayerStore: {
						Decision:             knowledgeEvidenceDecisionPartial,
						SelectedCandidateIDs: []string{"T1C1"},
						SupportedFacts: []knowledgeEvidenceFact{{
							FactID: "T1F1", Aspect: "existence", Statement: "门店有外卖机器人。",
						}},
						MissingAspects: []string{"机器人配送范围"},
					},
					knowledgeEvidenceLayerGeneral: {
						Decision:             knowledgeEvidenceDecisionDirectSingle,
						SelectedCandidateIDs: []string{"T1C2"},
						SupportedFacts: []knowledgeEvidenceFact{{
							FactID: "T1F2", Aspect: "scope", Statement: "机器人可以送到房门口。",
						}},
					},
				},
			},
			Trace: callbacks.KnowledgeEvidenceJudgeTraceData{SchemaVersion: knowledgeEvidenceJudgeSchemaVersion, Status: "completed"},
		}
	}}
	collector := callbacks.NewRuntimeTraceCollector()
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("外卖机器人能送到房间吗", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.RetrieveResult == nil || len(state.RetrieveResult.Hits) != 1 || state.RetrieveResult.Hits[0].KnowledgeBaseID != 2 {
		t.Fatalf("general complete answer must win over store partial evidence, got %#v", state.RetrieveResult)
	}
	if got := collector.Data.Pipeline.EvidenceJudge.Tasks[0]; got.SelectedLayer != knowledgeEvidenceLayerGeneral || got.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("unexpected selected evidence trace: %#v", got)
	}
}

func TestKnowledgeEvidenceJudgeStorePartialFactsReachGenerateAndReplyPlan(t *testing.T) {
	storePartial := judgeTestHit(1, 101, "外卖机器人", "问题：有外卖机器人吗\n答案：有外卖机器人的。", 0.93)
	generalPartial := judgeTestHit(2, 201, "通用机器人", "问题：酒店会配机器人吗\n答案：部分酒店会配。", 0.88)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"外卖机器人能送到房间吗": judgeTestRetrieveResult(storePartial, generalPartial),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(_ []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return knowledgeEvidenceJudgeOutcome{
			Applied: true,
			Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
				"T1": {
					knowledgeEvidenceLayerStore: {
						Decision:             knowledgeEvidenceDecisionPartial,
						SelectedCandidateIDs: []string{"T1C1"},
						SupportedFacts: []knowledgeEvidenceFact{{
							FactID: "T1F1", Aspect: "existence", Statement: "门店有外卖机器人。", CriticalValues: []string{"有外卖机器人"},
						}},
						MissingAspects: []string{"机器人是否能送到房间"},
					},
					knowledgeEvidenceLayerGeneral: {
						Decision:             knowledgeEvidenceDecisionPartial,
						SelectedCandidateIDs: []string{"T1C2"},
						SupportedFacts: []knowledgeEvidenceFact{{
							FactID: "T1F2", Aspect: "existence", Statement: "部分酒店会配机器人。",
						}},
						MissingAspects: []string{"当前门店是否配置", "机器人配送范围"},
					},
				},
			},
			Trace: callbacks.KnowledgeEvidenceJudgeTraceData{SchemaVersion: knowledgeEvidenceJudgeSchemaVersion, Status: "completed"},
		}
	}}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "hotel_info", Text: "外卖机器人能送到房间吗", Output: "knowledge_text_reply",
	}}})
	summary := &RunResult{}
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("外卖机器人能送到房间吗", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.RetrieveResult == nil || len(state.RetrieveResult.Hits) != 1 || state.RetrieveResult.Hits[0].KnowledgeBaseID != 1 {
		t.Fatalf("store partial evidence must win over general partial evidence, got %#v", state.RetrieveResult)
	}
	contextText := state.RetrieveResult.ContextText
	if !strings.Contains(contextText, "门店有外卖机器人") || !strings.Contains(contextText, "不得推测") || !strings.Contains(contextText, "机器人是否能送到房间") {
		t.Fatalf("fact boundary was not injected for Generate: %q", contextText)
	}
	traceTask := collector.Data.Pipeline.EvidenceJudge.Tasks[0]
	if traceTask.SelectedLayer != knowledgeEvidenceLayerStore || len(traceTask.SupportedFacts) != 1 || len(traceTask.MissingAspects) != 1 {
		t.Fatalf("selected facts were not recorded in Judge trace: %#v", traceTask)
	}
	planTask := collector.Data.Pipeline.ReplyPlan.TaskPlans[0]
	if planTask.SelectedLayer != knowledgeEvidenceLayerStore || len(planTask.SupportedFacts) != 1 || planTask.SupportedFacts[0].FactID != "T1F1" || len(planTask.MissingAspects) != 1 {
		t.Fatalf("selected facts were not propagated to ReplyPlan: %#v", planTask)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if !trace.DeferredHandoff || len(trace.DeferredTaskIDs) != 1 || trace.DeferredTaskIDs[0] != "T1" {
		t.Fatalf("partial missing scope must be deferred without removing its answer task: %#v", trace)
	}
	if !strings.Contains(trace.DeferredHandoffReason, "仅待确认缺失方面") || !strings.Contains(trace.DeferredHandoffReason, "机器人是否能送到房间") || strings.Contains(trace.DeferredHandoffReason, "完整待处理问题") {
		t.Fatalf("deferred reason must contain only the missing scope, got %q", trace.DeferredHandoffReason)
	}
	if summary.handoffDirective {
		t.Fatal("partial evidence must answer first and defer only its missing aspect")
	}
	instructions := make([]string, 0, len(state.Decision.Instructions))
	for _, message := range state.Decision.Instructions {
		if message != nil {
			instructions = append(instructions, message.Content)
		}
	}
	joinedInstructions := strings.Join(instructions, "\n")
	if !strings.Contains(joinedInstructions, "仍保留在 active ReplyPlan") || !strings.Contains(joinedInstructions, "必须回答其 supportedFacts") || !strings.Contains(joinedInstructions, "机器人是否能送到房间") {
		t.Fatalf("partial Generate boundary did not preserve the answer while deferring scope: %q", joinedInstructions)
	}
}

func TestKnowledgeEvidenceJudgeSourceContextOnlyUsesAdjacentTurnForReference(t *testing.T) {
	messages := []*schema.Message{
		schema.UserMessage("很久以前问过早餐"),
		schema.AssistantMessage("酒店不提供早餐", nil),
		schema.UserMessage("你们哪些房间有沙发"),
		schema.AssistantMessage("合柴、艺林、塔川、岭南这四种房型都带沙发。", nil),
	}
	contextItems := buildKnowledgeEvidenceJudgeSourceContext(messages, "那这4个房型都有办公桌吗", "那这4个房型都有办公桌吗")
	if len(contextItems) != 3 {
		t.Fatalf("expected previous customer, previous assistant and current primary only, got %#v", contextItems)
	}
	joined := contextItems[0].Content + contextItems[1].Content + contextItems[2].Content
	if strings.Contains(joined, "早餐") || !strings.Contains(joined, "哪些房间有沙发") || !strings.Contains(joined, "四种房型") {
		t.Fatalf("source context was polluted or incomplete: %#v", contextItems)
	}
	plain := buildKnowledgeEvidenceJudgeSourceContext(messages, "房间有矿泉水吗", "房间有矿泉水吗")
	if len(plain) != 1 || plain[0].Role != "customer_current" {
		t.Fatalf("independent task must not carry adjacent history: %#v", plain)
	}
	withTrailingCustomer := append(append([]*schema.Message(nil), messages...), schema.UserMessage("我又问了一个新的问题"))
	stale := buildKnowledgeEvidenceJudgeSourceContext(withTrailingCustomer, "那几个房型呢", "那几个房型呢")
	if len(stale) != 1 || stale[0].Role != "customer_current" {
		t.Fatalf("non-adjacent AI reply must not be attached across a newer customer message: %#v", stale)
	}
}

func TestNormalizeKnowledgeEvidenceJudgeConfigKeepsBatchCapacityWithoutRetries(t *testing.T) {
	for _, tc := range []struct {
		timeoutMS int
		taskCount int
		want      int
	}{{0, 1, 15_000}, {3_000, 1, 5_000}, {4_000, 8, 12_000}, {15_000, 8, 15_000}, {60_000, 8, 15_000}} {
		config := normalizeKnowledgeEvidenceJudgeConfig(models.AIConfig{
			TimeoutMS:       tc.timeoutMS,
			MaxOutputTokens: 8_192,
			MaxRetryCount:   3,
		}, tc.taskCount)
		if config.TimeoutMS != tc.want {
			t.Fatalf("expected configured timeout %dms to normalize to %dms, got %d", tc.timeoutMS, tc.want, config.TimeoutMS)
		}
		if config.MaxOutputTokens != 4_096 {
			t.Fatalf("expected judge output cap 4096, got %d", config.MaxOutputTokens)
		}
		if config.MaxRetryCount != 0 {
			t.Fatalf("knowledge judge must not retry, got %d", config.MaxRetryCount)
		}
	}
	longBatch := normalizeKnowledgeEvidenceJudgeConfig(models.AIConfig{TimeoutMS: 4_000, MaxOutputTokens: 1_024}, 8)
	if longBatch.TimeoutMS != 12_000 || longBatch.MaxOutputTokens != 2_560 {
		t.Fatalf("eight-task batch needs enough protocol capacity, got timeout=%d output=%d", longBatch.TimeoutMS, longBatch.MaxOutputTokens)
	}
}

func TestDeterministicKnowledgeEvidenceFactsClassifyClockRangeAsTime(t *testing.T) {
	facts := deterministicKnowledgeEvidenceFactsFromFAQ("T1", "南七店早餐时间为7:00-9:30。")
	if len(facts) != 1 || facts[0].Aspect != "time" {
		t.Fatalf("clock range should be a time fact: %#v", facts)
	}
	if len(facts[0].CriticalValues) != 1 || facts[0].CriticalValues[0] != "7:00-9:30" {
		t.Fatalf("clock range should not be split into quantity fragments: %#v", facts[0].CriticalValues)
	}
}

func judgeTestGate(retriever knowledgeContextRetriever, judge knowledgeEvidenceJudge) *KnowledgeAnswerabilityGate {
	return &KnowledgeAnswerabilityGate{
		newRetriever: func(_ models.AIAgent) knowledgeContextRetriever {
			return retriever
		},
		judge: judge,
	}
}

func judgeTestRetriever(results map[string]*retrievers.KnowledgeRetrieveResult) *fakeKnowledgeContextRetriever {
	return &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1, 2},
		resultsByQuery:   results,
	}
}

func judgeTestRetrieveResult(storeHit rag.RetrieveResult, generalHit rag.RetrieveResult) *retrievers.KnowledgeRetrieveResult {
	return &retrievers.KnowledgeRetrieveResult{
		KnowledgeBaseIDs: []int64{1, 2},
		RawHits:          []rag.RetrieveResult{storeHit, generalHit},
		Hits:             []rag.RetrieveResult{storeHit},
		ContextResults:   []rag.RetrieveResult{storeHit},
		ContextText:      storeHit.Content,
	}
}

func judgeTestHit(knowledgeBaseID int64, chunkID int64, title string, content string, score float32) rag.RetrieveResult {
	return rag.RetrieveResult{
		KnowledgeBaseID: knowledgeBaseID,
		ChunkID:         chunkID,
		SourceRecordID:  fmt.Sprintf("kb-%d-chunk-%d", knowledgeBaseID, chunkID),
		Title:           title,
		Content:         content,
		Score:           score,
	}
}

func completedJudgeOutcome(tasks []knowledgeEvidenceJudgeTask, classifications map[string][]string) knowledgeEvidenceJudgeOutcome {
	ret := knowledgeEvidenceJudgeOutcome{
		Applied:    true,
		Selections: make(map[string]map[string]knowledgeEvidenceLayerSelection, len(tasks)),
		Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
			SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
			Status:        "completed",
		},
	}
	for _, task := range tasks {
		values := classifications[task.TaskID]
		ret.Selections[task.TaskID] = make(map[string]knowledgeEvidenceLayerSelection, 2)
		directByLayer := make(map[string][]string, 2)
		supportingByLayer := make(map[string][]string, 2)
		for index, candidate := range task.Candidates {
			classification := knowledgeEvidenceClassificationUnrelated
			if index < len(values) {
				classification = values[index]
			}
			switch classification {
			case knowledgeEvidenceClassificationDirect:
				directByLayer[candidate.Layer] = append(directByLayer[candidate.Layer], candidate.CandidateID)
			case knowledgeEvidenceClassificationSupporting:
				supportingByLayer[candidate.Layer] = append(supportingByLayer[candidate.Layer], candidate.CandidateID)
			}
		}
		for _, layer := range []string{knowledgeEvidenceLayerStore, knowledgeEvidenceLayerGeneral} {
			selected := append([]string(nil), directByLayer[layer]...)
			if len(selected) > 0 {
				selected = append(selected, supportingByLayer[layer]...)
			}
			decision := knowledgeEvidenceDecisionInsufficient
			if len(selected) == 1 {
				decision = knowledgeEvidenceDecisionDirectSingle
			} else if len(selected) > 1 {
				decision = knowledgeEvidenceDecisionDirectCombined
			}
			if layerHasKnowledgeEvidenceCandidates(task, layer) {
				ret.Selections[task.TaskID][layer] = knowledgeEvidenceLayerSelection{Decision: decision, SelectedCandidateIDs: selected}
			}
		}
	}
	return ret
}

func layerHasKnowledgeEvidenceCandidates(task knowledgeEvidenceJudgeTask, layer string) bool {
	for _, candidate := range task.Candidates {
		if candidate.Layer == layer {
			return true
		}
	}
	return false
}
