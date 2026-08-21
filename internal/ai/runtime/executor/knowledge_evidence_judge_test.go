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
}

func TestParseKnowledgeEvidenceJudgeResponseRequiresEveryKnownCandidateExactlyOnce(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "有空调吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1"},
			{CandidateID: "T1C2"},
		},
	}}
	valid := `{"schemaVersion":"knowledge_evidence_judge.v1","tasks":[{"taskId":"T1","candidates":[{"candidateId":"T1C1","classification":"unrelated"},{"candidateId":"T1C2","classification":"direct"}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(valid, tasks)
	if err != nil {
		t.Fatalf("parse valid response: %v", err)
	}
	if parsed["T1"]["T1C2"] != knowledgeEvidenceClassificationDirect {
		t.Fatalf("unexpected parsed classifications: %#v", parsed)
	}

	invalid := []string{
		"```json\n" + valid + "\n```",
		`{"schemaVersion":"knowledge_evidence_judge.v1","tasks":[{"taskId":"T1","candidates":[{"candidateId":"T1C1","classification":"direct"}]}]}`,
		`{"schemaVersion":"knowledge_evidence_judge.v1","tasks":[{"taskId":"T1","candidates":[{"candidateId":"T1C1","classification":"maybe"},{"candidateId":"T1C2","classification":"direct"}]}]}`,
		`{"schemaVersion":"knowledge_evidence_judge.v1","tasks":[{"taskId":"T1","candidates":[{"candidateId":"T1C1","classification":"direct"},{"candidateId":"UNKNOWN","classification":"unrelated"}]}]}`,
		`{"schemaVersion":"knowledge_evidence_judge.v1","tasks":[{"taskId":"T1","candidates":[{"candidateId":"T1C1","classification":"direct"},{"candidateId":"T1C2","classification":"unrelated"}]}],"explanation":"extra"}`,
	}
	for index, raw := range invalid {
		if _, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks); err == nil {
			t.Fatalf("invalid response %d unexpectedly passed", index)
		}
	}
}

func TestKnowledgeEvidenceJudgePromptTreatsExplicitNegativeAnswerAsDirect(t *testing.T) {
	prompt := knowledgeEvidenceJudgeSystemPrompt()
	for _, required := range []string{"否定答案也可以是完整直接答案", "早餐几点", "酒店不提供早餐", "必须标记 direct"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("expected judge prompt to contain %q, got %q", required, prompt)
		}
	}
	if !strings.Contains(prompt, "有空调吗") || !strings.Contains(prompt, "空调不制冷需要处理") {
		t.Fatalf("expected capability-versus-fault boundary to remain, got %q", prompt)
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
		Applied:         true,
		Classifications: make(map[string]map[string]string, len(tasks)),
		Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
			SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
			Status:        "completed",
		},
	}
	for _, task := range tasks {
		values := classifications[task.TaskID]
		ret.Classifications[task.TaskID] = make(map[string]string, len(task.Candidates))
		for index, candidate := range task.Candidates {
			classification := knowledgeEvidenceClassificationUnrelated
			if index < len(values) {
				classification = values[index]
			}
			ret.Classifications[task.TaskID][candidate.CandidateID] = classification
		}
	}
	return ret
}
