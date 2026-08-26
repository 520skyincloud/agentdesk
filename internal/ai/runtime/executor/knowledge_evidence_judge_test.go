package executor

import (
	"context"
	"fmt"
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

func TestKnowledgeEvidenceJudgeFailurePreservesDeterministicStoreSelection(t *testing.T) {
	storeHit := judgeTestHit(1, 101, "门店答案", "问题：早餐几点\n答案：南七店早餐时间为7:00-9:30。", 0.75)
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
	if state.RetrieveResult == nil || len(state.RetrieveResult.Hits) != 1 || state.RetrieveResult.Hits[0].KnowledgeBaseID != 1 {
		t.Fatalf("expected original deterministic store selection, got %#v", state.RetrieveResult)
	}
	if collector.Data.Pipeline.EvidenceJudge.Status != "fallback" {
		t.Fatalf("expected fallback trace, got %#v", collector.Data.Pipeline.EvidenceJudge)
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
		"顺便问早餐几点": judgeTestRetrieveResult(
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
		t.Fatal("the first deferred task must not suppress the later answer")
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
}

func TestParseKnowledgeEvidenceJudgeResponseRequiresEveryLayerAndKeepsCandidatesInLayer(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "有空调吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerGeneral},
		},
	}}
	valid := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"]},{"layer":"general","decision":"insufficient","selectedCandidateIds":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(valid, tasks)
	if err != nil {
		t.Fatalf("parse valid response: %v", err)
	}
	if parsed["T1"][knowledgeEvidenceLayerStore].Decision != knowledgeEvidenceDecisionDirectCombined || len(parsed["T1"][knowledgeEvidenceLayerStore].SelectedCandidateIDs) != 2 {
		t.Fatalf("unexpected parsed classifications: %#v", parsed)
	}

	invalid := []string{
		"```json\n" + valid + "\n```",
		`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"]}]}]}`,
		`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1"]},{"layer":"general","decision":"insufficient","selectedCandidateIds":[]}]}]}`,
		`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C3"]},{"layer":"general","decision":"insufficient","selectedCandidateIds":[]}]}]}`,
		`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"]},{"layer":"general","decision":"insufficient","selectedCandidateIds":[]}]}],"explanation":"extra"}`,
	}
	for index, raw := range invalid {
		if _, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks); err == nil {
			t.Fatalf("invalid response %d unexpectedly passed", index)
		}
	}
}

func TestKnowledgeEvidenceJudgePromptSupportsFAQRehydrationAndSameLayerCombination(t *testing.T) {
	prompt := knowledgeEvidenceJudgeSystemPrompt()
	for _, required := range []string{"faqQuestion", "faqAnswer", "省略表达", "direct_combined", "严禁跨 store/general", "沙发", "办公桌", "房间内有两瓶矿泉水，并且免费", "足以回答“房间里有几瓶矿泉水”", "答案如果只是“转接”", "不是酒店事实", "不能让“转接”候选参与 direct_combined"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("expected judge prompt to contain %q, got %q", required, prompt)
		}
	}
	if !strings.Contains(prompt, "有空调吗") || !strings.Contains(prompt, "空调不制冷需要处理") {
		t.Fatalf("expected capability-versus-fault boundary to remain, got %q", prompt)
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
	config := normalizeKnowledgeEvidenceJudgeConfig(models.AIConfig{
		TimeoutMS:       60_000,
		MaxOutputTokens: 8_192,
		MaxRetryCount:   3,
	})
	if config.TimeoutMS != 4_000 {
		t.Fatalf("expected judge timeout 4000ms, got %d", config.TimeoutMS)
	}
	if config.MaxOutputTokens != 2_048 {
		t.Fatalf("expected judge output cap 2048, got %d", config.MaxOutputTokens)
	}
	if config.MaxRetryCount != 0 {
		t.Fatalf("knowledge judge must not retry, got %d", config.MaxRetryCount)
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
