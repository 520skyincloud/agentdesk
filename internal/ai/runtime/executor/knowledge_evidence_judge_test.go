package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestKnowledgeEvidenceJudgePromptDefinesExternalProxyEvidenceBoundary(t *testing.T) {
	prompt := knowledgeEvidenceJudgeSystemPrompt()
	for _, expected := range []string{
		"subIntent=external_proxy_action",
		"地址、电话、入口或操作步骤",
		"不得输出或暗示酒店已经代点",
		"酒店内部送物、维修、开门",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("Judge prompt missing external proxy boundary %q", expected)
		}
	}
}

func TestKnowledgeEvidenceJudgePromptDefinesApplicableAspectsAndPolicyAnswers(t *testing.T) {
	prompt := knowledgeEvidenceJudgeSystemPrompt()
	for _, expected := range []string{
		"只有证据明确否定前提时",
		"步行分钟数不再适用",
		"仅“建议驾车”不能推导“不能步行”",
		"独立问题或客户明确追问的其他交通方式、时间仍须检查",
		"候选 FAQ 与客户问题语义一致",
		"不能改成“价格不一样”",
		"没有覆盖客户新增具体要求的政策不适用此规则",
		"supportedFacts 不得混入“无法证明、证据不足”等裁决分析",
	} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("Judge prompt missing applicability or policy boundary %q", expected)
		}
	}
}

func TestKnowledgeEvidenceJudgeRuntimePreservesApplicableAspectAndPolicyDecisions(t *testing.T) {
	for _, test := range []struct {
		name, question, answer, decision, aspect string
		missing                                  []string
	}{
		{"inapplicable walking duration", "能步行过去吗，大概几分钟？", "不能步行到达，需要驾车前往。", "direct_single", "method", nil},
		{"driving recommendation leaves walking unknown", "能步行过去吗，大概几分钟？", "建议驾车前往。", "partial", "method", []string{"是否可以步行", "步行时长"}},
		{"independent driving duration remains required", "能步行吗，开车几分钟？", "不能步行到达，需要驾车前往。", "partial", "method", []string{"驾车时长"}},
		{"complete platform policy", "携程、抖音、美团的价格一样吗？", "每个客户在不同平台享受的平台权益是不一样的，建议您可以对比价格后选择合适您的。", "direct_single", "condition", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{
				TaskID: "T1", Query: test.question,
				Candidates: []knowledgeEvidenceJudgeCandidate{{
					CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
					Hit: judgeTestHit(3, 101, test.question, "问题："+test.question+"\n答案："+test.answer, 0.95),
				}},
			}
			missing, _ := json.Marshal(test.missing)
			if test.missing == nil {
				missing = []byte("[]")
			}
			raw := fmt.Sprintf(`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":%q,"selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":%q,"statement":%q,"criticalValues":[]}],"missingAspects":%s}]}]}`,
				test.decision, test.aspect, test.answer, missing)
			parsed, err := parseKnowledgeEvidenceJudgeRuntimeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatal(err)
			}
			selection := parsed["T1"][knowledgeEvidenceLayerStore]
			if selection.Decision != test.decision || len(selection.MissingAspects) != len(test.missing) || len(selection.SupportedFacts) != 1 || selection.SupportedFacts[0].Statement != test.answer {
				t.Fatalf("runtime must preserve the Judge decision and qualified answer: %#v", selection)
			}
		})
	}
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

func TestKnowledgeEvidenceJudgeFailureUsesStrictExactFAQWithoutScoreThreshold(t *testing.T) {
	storeHit := judgeTestHit(1, 101, "门店答案", "问题：早餐几点\n答案：南七店早餐时间为7:00-9:30。", 0.60)
	generalHit := judgeTestHit(2, 201, "通用答案", "问题：早餐几点\n答案：通常为7:00-10:00。", 0.99)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"早餐几点": judgeTestRetrieveResult(storeHit, generalHit),
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return failedKnowledgeEvidenceJudgeOutcome(tasks, callbacks.KnowledgeEvidenceJudgeTraceData{
			SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
			Status:        knowledgeEvidenceDecisionMalformed,
			Reason:        "simulated timeout",
		}, knowledgeEvidenceDecisionMalformed)
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
	if judge.calls != 1 {
		t.Fatalf("a strict exact store FAQ must avoid an unnecessary Judge retry, calls=%d", judge.calls)
	}
	if state.RetrieveResult == nil || len(state.RetrieveResult.EffectiveHits) != 1 || !strings.Contains(state.RetrieveResult.ContextText, "南七店早餐时间为7:00-9:30") {
		t.Fatalf("strict exact FAQ should recover the store answer regardless of score, got %#v", state.RetrieveResult)
	}
	if len(state.RetrieveResult.RawHits) != 2 {
		t.Fatalf("raw hits must remain available for diagnostics, got %#v", state.RetrieveResult.RawHits)
	}
	if state.AnswerabilityStatus != answerabilityStatusHasContext || state.Input.Summary.handoffDirective {
		t.Fatalf("strict exact FAQ recovery must answer without handoff, status=%q summary=%#v", state.AnswerabilityStatus, state.Input.Summary)
	}
	if collector.Data.Pipeline.EvidenceJudge.Status != knowledgeEvidenceDecisionMalformed || len(collector.Data.Pipeline.EvidenceJudge.Tasks) != 1 || collector.Data.Pipeline.EvidenceJudge.Tasks[0].DecisionSource != "exact_faq_fallback" || collector.Data.Pipeline.EvidenceJudge.Tasks[0].Disposition != runtimeKnowledgeDispositionAnswer {
		t.Fatalf("expected exact fallback trace, got %#v", collector.Data.Pipeline.EvidenceJudge)
	}
}

func TestKnowledgeEvidenceJudgeModelInsufficientDoesNotUseStrictExactFAQFallback(t *testing.T) {
	storeHit := judgeTestHit(1, 101, "门店早餐", "问题：早餐几点\n答案：南七店早餐时间为7:00-9:30。", 0.99)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"早餐几点": {
			KnowledgeBaseIDs: []int64{1},
			RawHits:          []rag.RetrieveResult{storeHit},
			Hits:             []rag.RetrieveResult{storeHit},
			ContextResults:   []rag.RetrieveResult{storeHit},
			ContextText:      storeHit.Content,
		},
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return knowledgeEvidenceJudgeOutcome{
			Applied: true,
			Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
				"T1": {
					knowledgeEvidenceLayerStore: insufficientKnowledgeEvidenceLayerSelection(),
				},
			},
			Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
				SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
				Status:        "completed",
			},
		}
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
	if state.RetrieveResult == nil || len(state.RetrieveResult.RawHits) != 1 || len(state.RetrieveResult.EffectiveHits) != 0 || len(state.RetrieveResult.Hits) != 0 {
		t.Fatalf("a completed model insufficient decision must not be promoted by exact FAQ fallback: %#v", state.RetrieveResult)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if len(trace.Tasks) != 1 || trace.Tasks[0].Decision != knowledgeEvidenceDecisionInsufficient || trace.Tasks[0].DecisionSource != "model" || trace.Tasks[0].Disposition != runtimeKnowledgeDispositionNoEvidenceHandoff {
		t.Fatalf("the completed model decision must remain authoritative: %#v", trace)
	}
}

func TestKnowledgeEvidenceJudgeFailureDoesNotUseLegacySemanticScoreRescue(t *testing.T) {
	storeHit := judgeTestHit(1, 101, "拖鞋自取", "问题：需要额外拖鞋怎么办\n答案：可前往1313对面洗衣房领取拖鞋。", 0.93)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"拖鞋没了": {
			KnowledgeBaseIDs: []int64{1, 2},
			RawHits:          []rag.RetrieveResult{storeHit},
			Hits:             []rag.RetrieveResult{storeHit},
			ContextResults:   []rag.RetrieveResult{storeHit},
			ContextText:      storeHit.Content,
		},
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return failedKnowledgeEvidenceJudgeOutcome(tasks, callbacks.KnowledgeEvidenceJudgeTraceData{
			SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
			Status:        knowledgeEvidenceDecisionMalformed,
			Reason:        "simulated protocol failure",
		}, knowledgeEvidenceDecisionMalformed)
	}}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "service_request", Text: "拖鞋没了", ResolvedText: "拖鞋没了",
		Output: "knowledge_text_reply", OutputKind: "text", ReplyRequired: true,
	}}})
	intent := hotelInfoIntent()
	intent.PrimaryIntent = "service_request"
	intent.MatchedIntentCode = "service_request"
	intent.SubIntent = "supplies_self_help"
	intent.IntentTasks = []callbacks.IntentTaskTraceData{{
		Intent: "service_request", SubIntent: "supplies_self_help", Objective: "action_request",
		Text: "拖鞋没了", ResolvedText: "拖鞋没了", NeedsKnowledge: true,
		Entities: []callbacks.IntentEntityTraceData{{Text: "拖鞋", Type: "supply"}},
	}}
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("拖鞋没了", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent:    intent,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if judge.calls != 1 {
		t.Fatalf("Judge must still be called exactly once, calls=%d", judge.calls)
	}
	if state.RetrieveResult == nil || len(state.RetrieveResult.RawHits) != 1 || len(state.RetrieveResult.EffectiveHits) != 0 || strings.TrimSpace(state.RetrieveResult.ContextText) != "" {
		t.Fatalf("a non-exact semantic FAQ must remain diagnostic-only after Judge failure: %#v", state.RetrieveResult)
	}
	if trace := collector.Data.Pipeline.EvidenceJudge; len(trace.Tasks) != 1 || trace.Tasks[0].DecisionSource == "store_exact_faq_rescue" {
		t.Fatalf("the production Judge path must not invoke the legacy semantic score rescue: %#v", trace)
	}
}

func TestKnowledgeEvidenceJudgeProtocolFailureUsesStrictExactFAQWithPoliteSuffix(t *testing.T) {
	hit := judgeTestHit(1, 101, "老板信息", "问题：老板是谁\n答案：老板是汤东强。", 0.999)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"老板是谁呀": {
			KnowledgeBaseIDs: []int64{1, 2},
			RawHits:          []rag.RetrieveResult{hit},
			Hits:             []rag.RetrieveResult{hit},
			ContextResults:   []rag.RetrieveResult{hit},
			ContextText:      hit.Content,
		},
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return failedKnowledgeEvidenceJudgeOutcome(tasks, callbacks.KnowledgeEvidenceJudgeTraceData{
			SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
			Status:        knowledgeEvidenceDecisionMalformed,
		}, knowledgeEvidenceDecisionMalformed)
	}}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "hotel_info", Text: "老板是谁呀", ResolvedText: "老板是谁呀", Output: "knowledge_text_reply", OutputKind: "text", ReplyRequired: true,
	}}})
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("老板是谁呀", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if judge.calls != 1 {
		t.Fatalf("the reply run must call Judge exactly once, calls=%d", judge.calls)
	}
	if state.RetrieveResult == nil || len(state.RetrieveResult.RawHits) != 1 || len(state.RetrieveResult.EffectiveHits) != 1 || len(state.RetrieveResult.Hits) != 1 || !strings.Contains(state.RetrieveResult.ContextText, "老板是汤东强") {
		t.Fatalf("a terminal polite particle must not prevent strict exact FAQ recovery: %#v", state.RetrieveResult)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if len(trace.Tasks) != 1 || trace.Tasks[0].Disposition != runtimeKnowledgeDispositionAnswer || trace.Tasks[0].DecisionSource != "exact_faq_fallback" {
		t.Fatalf("the polite-suffix match must use score-independent exact fallback: %#v", trace)
	}
}

func TestStrictExactFAQNormalizationOnlyStripsConfiguredTerminalParticles(t *testing.T) {
	hit := judgeTestHit(1, 101, "老板信息", "问题：老板是谁\n答案：老板是汤东强。", 0.9)
	for _, suffix := range []string{"呀", "啊", "呢", "啦", "哈"} {
		t.Run(suffix, func(t *testing.T) {
			if _, _, exact := exactKnowledgeEvidenceFAQMatch(hit, "老板是谁"+suffix); !exact {
				t.Fatalf("terminal polite particle %q should preserve strict FAQ equality", suffix)
			}
		})
	}
	if _, _, exact := exactKnowledgeEvidenceFAQMatch(hit, "老板是谁嘛"); exact {
		t.Fatal("an unconfigured suffix must not be removed by strict FAQ normalization")
	}
	if normalizeStrictKnowledgeEvidenceFAQText("老哈板是谁") == normalizeStrictKnowledgeEvidenceFAQText("老板是谁") {
		t.Fatal("polite particles must not be removed from the middle of a question")
	}
	if normalizeStrictKnowledgeEvidenceFAQText("小哈") == normalizeStrictKnowledgeEvidenceFAQText("小") {
		t.Fatal("a legitimate entity suffix must not be removed without an explicit question form")
	}
	if normalizeStrictKnowledgeEvidenceFAQAnswerText("负责人是小哈") == normalizeStrictKnowledgeEvidenceFAQAnswerText("负责人是小") {
		t.Fatal("answer normalization must preserve legitimate terminal entity characters")
	}
}

func TestKnowledgeEvidenceJudgeUsageKeyIsStable(t *testing.T) {
	req := RunInput{
		Conversation: models.Conversation{ID: 81},
		UserMessage:  models.Message{ID: 91, RequestID: "request-91"},
	}
	key := knowledgeEvidenceJudgeUsageEventKey(req, "fingerprint")
	if key != "request-91:knowledge_evidence_judge:fingerprint" {
		t.Fatalf("unexpected usage key: %q", key)
	}
	if knowledgeEvidenceJudgeUsageEventKey(req, "fingerprint") != key {
		t.Fatal("usage key must remain stable")
	}
}

func TestKnowledgeEvidenceJudgeStopsBeforeGenerateAfterProtocolFailure(t *testing.T) {
	storeHit := judgeTestHit(1, 101, "早餐供应", "问题：酒店早餐供应时段\n答案：早餐时间为7:00-9:30。", 0.91)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"早餐几点呀": {
			KnowledgeBaseIDs: []int64{1, 2},
			RawHits:          []rag.RetrieveResult{storeHit},
			Hits:             []rag.RetrieveResult{storeHit},
			ContextResults:   []rag.RetrieveResult{storeHit},
			ContextText:      storeHit.Content,
		},
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return failedKnowledgeEvidenceJudgeOutcome(tasks, callbacks.KnowledgeEvidenceJudgeTraceData{
			SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
			Status:        knowledgeEvidenceDecisionMalformed,
			LatencyMs:     5,
		}, knowledgeEvidenceDecisionMalformed)
	}}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "hotel_info", Text: "早餐几点呀", ResolvedText: "早餐几点呀", Output: "knowledge_text_reply", OutputKind: "text", ReplyRequired: true,
	}}})
	summary := &RunResult{}
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点呀", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if judge.calls != 1 || collector.Data.Pipeline.EvidenceJudge.LatencyMs != 5 {
		t.Fatalf("Judge must be called exactly once: calls=%d trace=%#v", judge.calls, collector.Data.Pipeline.EvidenceJudge)
	}
	if state.RetrieveResult == nil || len(state.RetrieveResult.RawHits) != 1 || len(state.RetrieveResult.Hits) != 0 || len(state.RetrieveResult.EffectiveHits) != 0 || strings.TrimSpace(state.RetrieveResult.ContextText) != "" {
		t.Fatalf("failed Judge candidates must remain diagnostic-only: %#v", state.RetrieveResult)
	}
	if summary.handoffDirective {
		t.Fatal("Judge protocol failure must never become a human handoff")
	}
	if got := ungroundedKnowledgeReplyTaskIDs(collector.Data.Pipeline.ReplyPlan); len(got) != 1 || got[0] != "T1" {
		t.Fatalf("the failed knowledge task must be stopped by the pre-Generate deterministic fallback: %#v plan=%#v", got, collector.Data.Pipeline.ReplyPlan)
	}
	traceTask := collector.Data.Pipeline.EvidenceJudge.Tasks[0]
	if traceTask.Disposition != runtimeKnowledgeDispositionJudgeProtocolRetry || traceTask.Decision != knowledgeEvidenceDecisionMalformed {
		t.Fatalf("unexpected failed task trace: %#v", traceTask)
	}
}

func TestKnowledgeEvidenceJudgeMixedProtocolFailureOnlyIsolatesFailedTask(t *testing.T) {
	breakfastHit := judgeTestHit(1, 101, "门店早餐", "问题：酒店早餐供应时段\n答案：早餐时间为7:00-9:30。", 0.91)
	ownerHit := judgeTestHit(1, 102, "老板信息", "问题：老板是谁\n答案：老板是汤东强。", 0.88)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"早餐几点呀": {
			KnowledgeBaseIDs: []int64{1, 2},
			RawHits:          []rag.RetrieveResult{breakfastHit},
			Hits:             []rag.RetrieveResult{breakfastHit},
			ContextResults:   []rag.RetrieveResult{breakfastHit},
			ContextText:      breakfastHit.Content,
		},
		"老板叫什么": {
			KnowledgeBaseIDs: []int64{1, 2},
			RawHits:          []rag.RetrieveResult{ownerHit},
			Hits:             []rag.RetrieveResult{ownerHit},
			ContextResults:   []rag.RetrieveResult{ownerHit},
			ContextText:      ownerHit.Content,
		},
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return knowledgeEvidenceJudgeOutcome{
			Applied: true,
			Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
				"T1": {
					knowledgeEvidenceLayerStore: {
						Decision:             knowledgeEvidenceDecisionDirectSingle,
						DecisionSource:       "model",
						SelectedCandidateIDs: []string{"T1C1"},
						SupportedFacts: []knowledgeEvidenceFact{{
							FactID: "T1F1", Aspect: "time", Statement: "早餐时间为7:00-9:30。", CriticalValues: []string{"7:00-9:30"},
						}},
					},
				},
				"T2": {
					knowledgeEvidenceLayerStore: protocolInvalidKnowledgeEvidenceLayerSelection(),
				},
			},
			Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
				SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
				Status:        knowledgeEvidenceDecisionProtocolInvalid,
				LatencyMs:     4,
			},
		}
	}}
	intent := hotelInfoIntent()
	intent.IntentTasks = []callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", Text: "早餐几点呀", ResolvedText: "早餐几点呀", NeedsKnowledge: true},
		{Intent: "hotel_info", Text: "老板叫什么", ResolvedText: "老板叫什么", NeedsKnowledge: true},
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "T1", Intent: "hotel_info", Text: "早餐几点呀", ResolvedText: "早餐几点呀", Output: "knowledge_text_reply", OutputKind: "text", ReplyRequired: true},
		{TaskID: "T2", Intent: "hotel_info", Text: "老板叫什么", ResolvedText: "老板叫什么", Output: "knowledge_text_reply", OutputKind: "text", ReplyRequired: true},
	}})
	summary := &RunResult{}
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点呀，老板叫什么", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    intent,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if judge.calls != 1 {
		t.Fatalf("mixed protocol failure must not add another Judge call, calls=%d", judge.calls)
	}
	if summary.handoffDirective {
		t.Fatal("a Judge protocol failure must not become a human handoff")
	}
	if state.RetrieveResult == nil || !strings.Contains(state.RetrieveResult.ContextText, "7:00-9:30") || strings.Contains(state.RetrieveResult.ContextText, "汤东强") {
		t.Fatalf("only the successful task may expose effective evidence: %#v", state.RetrieveResult)
	}
	if len(state.RetrieveResult.RawHits) != 2 {
		t.Fatalf("raw candidates must remain diagnostic-only for both tasks: %#v", state.RetrieveResult.RawHits)
	}

	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) != 2 || plan.TaskPlans[0].SelectedLayer != knowledgeEvidenceLayerStore || len(plan.TaskPlans[0].SupportedFacts) != 1 {
		t.Fatalf("the successful task lost its selected evidence during retry handling: %#v", plan.TaskPlans)
	}
	if plan.TaskPlans[1].SelectedLayer != "" || len(plan.TaskPlans[1].SupportedFacts) != 0 {
		t.Fatalf("the failed task must remain ungrounded before isolation: %#v", plan.TaskPlans[1])
	}
	if got := ungroundedKnowledgeReplyTaskIDs(plan); len(got) != 1 || got[0] != "T2" {
		t.Fatalf("only T2 should require a safe reply, got %#v", got)
	}

	isolated, taskIDs := isolateUngroundedKnowledgeReplyTasks(plan)
	if len(taskIDs) != 1 || taskIDs[0] != "T2" {
		t.Fatalf("only the failed task should be isolated, got %#v", taskIDs)
	}
	if isolated.TaskPlans[0].SelectedLayer != knowledgeEvidenceLayerStore || len(isolated.TaskPlans[0].SupportedFacts) != 1 {
		t.Fatalf("isolating T2 must not rewrite the successful T1 task: %#v", isolated.TaskPlans[0])
	}
	failedTask := isolated.TaskPlans[1]
	if failedTask.SelectedLayer != "runtime_safe_fallback" || len(failedTask.SupportedFacts) != 1 || failedTask.SupportedFacts[0].Statement != ungroundedKnowledgeSafeReply {
		t.Fatalf("T2 must receive only the fixed knowledge-safe reply: %#v", failedTask)
	}
	if got := ungroundedKnowledgeReplyTaskIDs(isolated); len(got) != 0 {
		t.Fatalf("isolated tasks must not re-enter free knowledge generation: %#v", got)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if trace.LatencyMs != 4 || trace.Status != knowledgeEvidenceDecisionProtocolInvalid || len(trace.Tasks) != 2 || trace.Tasks[0].Disposition != runtimeKnowledgeDispositionAnswer || trace.Tasks[1].Disposition != runtimeKnowledgeDispositionJudgeProtocolRetry {
		t.Fatalf("unexpected mixed Judge trace: %#v", trace)
	}
}

func TestKnowledgeEvidenceJudgePartialAndProtocolFailureKeepAnswerHandoffAndSafeReply(t *testing.T) {
	robotHit := judgeTestHit(1, 101, "外卖机器人", "问题：外卖机器人能送到房间吗\n答案：门店有外卖机器人。", 0.93)
	ownerHit := judgeTestHit(1, 102, "客房用品", "问题：浴巾在哪里\n答案：浴巾放在衣柜里。", 0.82)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"外卖机器人能送到房间吗": {
			KnowledgeBaseIDs: []int64{1, 2}, RawHits: []rag.RetrieveResult{robotHit}, Hits: []rag.RetrieveResult{robotHit}, ContextResults: []rag.RetrieveResult{robotHit}, ContextText: robotHit.Content,
		},
		"老板叫什么": {
			KnowledgeBaseIDs: []int64{1, 2}, RawHits: []rag.RetrieveResult{ownerHit}, Hits: []rag.RetrieveResult{ownerHit}, ContextResults: []rag.RetrieveResult{ownerHit}, ContextText: ownerHit.Content,
		},
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return knowledgeEvidenceJudgeOutcome{
			Applied: true,
			Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
				"T1": {
					knowledgeEvidenceLayerStore: {
						Decision:             knowledgeEvidenceDecisionPartial,
						DecisionSource:       "model",
						SelectedCandidateIDs: []string{"T1C1"},
						SupportedFacts: []knowledgeEvidenceFact{{
							FactID: "T1F1", Aspect: "existence", Statement: "门店有外卖机器人。", CriticalValues: []string{"有外卖机器人"},
						}},
						MissingAspects: []string{"机器人是否能送到房间"},
					},
				},
				"T2": {
					knowledgeEvidenceLayerStore: protocolInvalidKnowledgeEvidenceLayerSelection(),
				},
			},
			Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
				SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
				Status:        knowledgeEvidenceDecisionProtocolInvalid,
				LatencyMs:     4,
			},
		}
	}}
	intent := hotelInfoIntent()
	intent.IntentTasks = []callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", Text: "外卖机器人能送到房间吗", ResolvedText: "外卖机器人能送到房间吗", NeedsKnowledge: true},
		{Intent: "hotel_info", Text: "老板叫什么", ResolvedText: "老板叫什么", NeedsKnowledge: true},
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "T1", Intent: "hotel_info", Text: "外卖机器人能送到房间吗", ResolvedText: "外卖机器人能送到房间吗", Output: "knowledge_text_reply", OutputKind: "text", ReplyRequired: true},
		{TaskID: "T2", Intent: "hotel_info", Text: "老板叫什么", ResolvedText: "老板叫什么", Output: "knowledge_text_reply", OutputKind: "text", ReplyRequired: true},
	}})
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("外卖机器人能送到房间吗，老板叫什么", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent:    intent,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if judge.calls != 1 || state.RetrieveResult == nil || !strings.Contains(state.RetrieveResult.ContextText, "门店有外卖机器人") {
		t.Fatalf("partial evidence must survive a sibling protocol failure without another Judge call: calls=%d result=%#v", judge.calls, state.RetrieveResult)
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if !trace.DeferredHandoff || len(trace.DeferredTaskIDs) != 1 || trace.DeferredTaskIDs[0] != "T1" {
		t.Fatalf("the partial task must retain its missing-aspect handoff: %#v", trace)
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) != 2 || plan.TaskPlans[0].SelectedLayer != knowledgeEvidenceLayerStore || len(plan.TaskPlans[0].SupportedFacts) != 1 || plan.TaskPlans[1].SelectedLayer != "" {
		t.Fatalf("partial facts and the isolated retry task must both remain in ReplyPlan: %#v", plan.TaskPlans)
	}
	isolated, taskIDs := isolateUngroundedKnowledgeReplyTasks(plan)
	if len(taskIDs) != 1 || taskIDs[0] != "T2" || isolated.TaskPlans[1].SelectedLayer != "runtime_safe_fallback" {
		t.Fatalf("only the failed task should become a fixed safe reply: ids=%#v plan=%#v", taskIDs, isolated.TaskPlans)
	}
	collector.SetReplyPlan(isolated)
	finalReply := deterministicGeneratedReplyFallback(collector)
	if !strings.Contains(finalReply, "门店有外卖机器人") || !strings.Contains(finalReply, "暂时没法准确回答") {
		t.Fatalf("final fallback must preserve partial facts and answer the failed task safely: %q", finalReply)
	}
}

func TestKnowledgeEvidenceJudgeExactHandoffAndProtocolFailurePreserveBothActions(t *testing.T) {
	handoffHit := judgeTestHit(1, 101, "马桶故障", "问题：马桶堵了怎么办\n答案：转接", 0.91)
	ownerHit := judgeTestHit(1, 102, "客房用品", "问题：浴巾在哪里\n答案：浴巾放在衣柜里。", 0.82)
	retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
		"马桶堵了怎么办": {
			KnowledgeBaseIDs: []int64{1, 2}, RawHits: []rag.RetrieveResult{handoffHit}, Hits: []rag.RetrieveResult{handoffHit}, ContextResults: []rag.RetrieveResult{handoffHit}, ContextText: handoffHit.Content,
		},
		"老板叫什么": {
			KnowledgeBaseIDs: []int64{1, 2}, RawHits: []rag.RetrieveResult{ownerHit}, Hits: []rag.RetrieveResult{ownerHit}, ContextResults: []rag.RetrieveResult{ownerHit}, ContextText: ownerHit.Content,
		},
	})
	judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
		return knowledgeEvidenceJudgeOutcome{
			Applied: true,
			Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
				"T1": {
					knowledgeEvidenceLayerStore: {
						Decision:             knowledgeEvidenceDecisionDirectSingle,
						DecisionSource:       "model",
						SelectedCandidateIDs: []string{"T1C1"},
						SupportedFacts: []knowledgeEvidenceFact{{
							FactID: "T1F1", Aspect: "other", Statement: "转接",
						}},
					},
				},
				"T2": {
					knowledgeEvidenceLayerStore: protocolInvalidKnowledgeEvidenceLayerSelection(),
				},
			},
			Trace: callbacks.KnowledgeEvidenceJudgeTraceData{
				SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
				Status:        knowledgeEvidenceDecisionProtocolInvalid,
				LatencyMs:     4,
			},
		}
	}}
	intent := hotelInfoIntent()
	intent.IntentTasks = []callbacks.IntentTaskTraceData{
		{Intent: "service_request", Text: "马桶堵了怎么办", ResolvedText: "马桶堵了怎么办", NeedsKnowledge: true},
		{Intent: "hotel_info", Text: "老板叫什么", ResolvedText: "老板叫什么", NeedsKnowledge: true},
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "T1", Intent: "service_request", Text: "马桶堵了怎么办", ResolvedText: "马桶堵了怎么办", Output: "knowledge_text_reply", OutputKind: "text", ReplyRequired: true},
		{TaskID: "T2", Intent: "hotel_info", Text: "老板叫什么", ResolvedText: "老板叫什么", Output: "knowledge_text_reply", OutputKind: "text", ReplyRequired: true},
	}})
	summary := &RunResult{}
	state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("马桶堵了怎么办，老板叫什么", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    intent,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if judge.calls != 1 {
		t.Fatalf("mixed handoff/protocol failure must call Judge once, calls=%d", judge.calls)
	}
	if summary.handoffDirective {
		t.Fatal("the valid handoff must be deferred until the safe reply is committed")
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if !trace.DeferredHandoff || len(trace.DeferredTaskIDs) != 1 || trace.DeferredTaskIDs[0] != "T1" {
		t.Fatalf("the exact knowledge handoff action was lost: %#v", trace)
	}
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) != 2 || plan.TaskPlans[0].TaskID != "T1" || plan.TaskPlans[0].Output != runtimeKnowledgeDeferredHandoffOutput ||
		plan.TaskPlans[0].OutputKind != "handoff" || plan.TaskPlans[0].ReplyRequired || plan.TaskPlans[1].TaskID != "T2" ||
		len(ungroundedKnowledgeReplyTaskIDs(plan)) != 1 {
		t.Fatalf("the handoff Task must remain recoverable while only the failed reply task enters local safe output: %#v", plan.TaskPlans)
	}
	decisionText := ""
	for _, instruction := range state.Decision.Instructions {
		if instruction != nil {
			decisionText += instruction.Content + "\n"
		}
	}
	if !strings.Contains(decisionText, "知识证据裁决隔离") || strings.Contains(decisionText, "知识库检索暂时不可用") || strings.Contains(decisionText, "当前没有从知识库检索到可用资料") {
		t.Fatalf("mixed handoff/retry must use per-task protocol isolation, got %q", decisionText)
	}
	isolated, isolatedIDs := isolateUngroundedKnowledgeReplyTasks(plan)
	if len(isolatedIDs) != 1 || isolatedIDs[0] != "T2" || isolated.TaskPlans[0].Output != runtimeKnowledgeDeferredHandoffOutput || isolated.TaskPlans[1].SelectedLayer != "runtime_safe_fallback" {
		t.Fatalf("safe isolation must preserve the deferred handoff Task: ids=%#v plan=%#v", isolatedIDs, isolated.TaskPlans)
	}
	collector.SetReplyPlan(isolated)
	finalSummary, err := completeUngroundedKnowledgeFallback(summary, collector, []string{"T2"})
	if err != nil || finalSummary.ReplyText != ungroundedKnowledgeSafeReply {
		t.Fatalf("the failed task must produce exactly the fixed safe reply: summary=%#v err=%v", finalSummary, err)
	}
	if !collector.Data.Pipeline.EvidenceJudge.DeferredHandoff || collector.Data.Pipeline.EvidenceJudge.DeferredTaskIDs[0] != "T1" {
		t.Fatalf("building the safe reply must not erase the valid deferred handoff: %#v", collector.Data.Pipeline.EvidenceJudge)
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

	storeHit := judgeTestHit(1, 101, "门店早餐", "问题：早餐供应时间\n答案：7:00-9:30。", 0.82)
	generalHit := judgeTestHit(2, 201, "通用早餐", "问题：早餐供应时间\n答案：7:00-10:00。", 0.93)
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
	if trace.DeferredHandoff || len(trace.Tasks) != 1 || trace.Tasks[0].Disposition != runtimeKnowledgeDispositionJudgeProtocolRetry {
		t.Fatalf("judge failure must request protocol recovery without handoff, got %#v", trace)
	}
	activePlan := collector.Data.Pipeline.ReplyPlan
	if len(activePlan.TaskPlans) != 2 {
		t.Fatalf("protocol failure must not delete the independent resource task: %#v", activePlan.TaskPlans)
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

func TestBuildKnowledgeEvidenceJudgeTasksExpandsEveryFAQUnitBeforeBudgeting(t *testing.T) {
	rawContent := strings.Join([]string{
		"问题：老板是谁",
		"答案：老板是汤东强。",
		"相似问法：酒店董事长是谁",
		"问题：附近有什么好玩的",
		"答案：附近可以去罍街和合柴1972。",
		"问题：房间有办公桌吗",
		"答案：部分房型配有办公桌。",
	}, "\n")
	rawHit := judgeTestHit(1, 101, "南七店问答", rawContent, 0.91)
	batch := &runtimeKnowledgeRetrieveBatch{Questions: []runtimeKnowledgeQuestionResult{{
		TaskID: "T1",
		Query:  "附近有什么好玩的",
		Result: &retrievers.KnowledgeRetrieveResult{RawHits: []rag.RetrieveResult{rawHit}},
	}}}

	tasks := buildKnowledgeEvidenceJudgeTasks(batch, []int64{1}, []int64{1}, nil, "附近有什么好玩的")
	if len(tasks) != 1 || len(tasks[0].Candidates) != 3 || len(tasks[0].RawCandidates) != 3 {
		t.Fatalf("every FAQ unit must become an independent pre-budget candidate: %#v", tasks)
	}
	questions := make([]string, 0, len(tasks[0].Candidates))
	for _, candidate := range tasks[0].Candidates {
		questions = append(questions, candidate.Hit.FaqQuestion)
		if len(parseKnowledgeEvidenceFAQUnits(candidate.Hit.Content)) != 1 {
			t.Fatalf("expanded candidate must contain exactly one FAQ unit: %#v", candidate.Hit)
		}
	}
	for _, expected := range []string{"老板是谁", "附近有什么好玩的", "房间有办公桌吗"} {
		if !knowledgeEvidenceContainsString(questions, expected) {
			t.Fatalf("expanded candidates lost FAQ %q: %#v", expected, questions)
		}
	}
	if len(batch.Questions[0].Result.RawHits) != 1 || batch.Questions[0].Result.RawHits[0].Content != rawContent || batch.Questions[0].Result.RawHits[0].FaqQuestion != "" {
		t.Fatalf("Judge expansion must not rewrite Retriever RawHits: %#v", batch.Questions[0].Result.RawHits)
	}
	if !strings.Contains(tasks[0].Candidates[0].Hit.Content, "相似问法：酒店董事长是谁") {
		t.Fatalf("FAQ-local aliases must stay attached to their expanded unit: %#v", tasks[0].Candidates[0].Hit)
	}
}

func TestBuildKnowledgeEvidenceJudgeTasksBudgetsAfterMultiFAQExpansion(t *testing.T) {
	faqLines := make([]string, 0, 60)
	for index := 1; index <= 30; index++ {
		faqLines = append(faqLines,
			fmt.Sprintf("问题：测试问题%d", index),
			fmt.Sprintf("答案：测试答案%d。", index),
		)
	}
	rawContent := strings.Join(faqLines, "\n")
	rawHit := judgeTestHit(1, 101, "批量问答", rawContent, 0.91)
	batch := &runtimeKnowledgeRetrieveBatch{Questions: []runtimeKnowledgeQuestionResult{{
		TaskID: "T1",
		Query:  "测试问题1",
		Result: &retrievers.KnowledgeRetrieveResult{RawHits: []rag.RetrieveResult{rawHit}},
	}}}

	tasks := buildKnowledgeEvidenceJudgeTasks(batch, []int64{1}, []int64{1}, nil, "测试问题1")
	if len(tasks) != 1 || len(tasks[0].RawCandidates) != 30 {
		t.Fatalf("all FAQ units must be available to pre-budget candidate selection: %#v", tasks)
	}
	if len(tasks[0].Candidates) != knowledgeEvidenceJudgeBatchCandidateBudget {
		t.Fatalf("expanded candidates must still obey the shared %d-item Judge budget, got %d", knowledgeEvidenceJudgeBatchCandidateBudget, len(tasks[0].Candidates))
	}
	if len(batch.Questions[0].Result.RawHits) != 1 || batch.Questions[0].Result.RawHits[0].Content != rawContent {
		t.Fatalf("budgeting expanded Judge candidates must not rewrite Retriever RawHits: %#v", batch.Questions[0].Result.RawHits)
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
		{TaskID: "T1", Intent: "hotel_info", SubIntent: "breakfast", Objective: "time", RelationToPrevious: "independent", ResolutionState: "clear", Text: "早餐几点", OriginalText: "早餐几点", ResolvedText: "早餐几点", SourceRefs: []string{"U1"}, NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
		{TaskID: "T2", Intent: "hotel_info", SubIntent: "identity", Objective: "identity", RelationToPrevious: "independent", ResolutionState: "clear", Text: "汤东强是谁", OriginalText: "汤东强是谁", ResolvedText: "汤东强是谁", SourceRefs: []string{"U1"}, NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
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
	plan := collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) != 2 || plan.TaskPlans[0].TaskID != "T1" || plan.TaskPlans[1].TaskID != "T2" {
		t.Fatalf("answered and deferred sibling Tasks must both remain in ReplyPlan: %#v", plan.TaskPlans)
	}
	if plan.TaskPlans[1].OutputKind != "handoff" || plan.TaskPlans[1].ReplyRequired || plan.TaskPlans[1].Output != runtimeKnowledgeDeferredHandoffOutput {
		t.Fatalf("T2 must remain as a non-text deferred Task for manual resume: %#v", plan.TaskPlans[1])
	}
	if active := activeGenerationTaskPlans(intent, plan); len(active) != 1 || active[0].TaskID != "T1" {
		t.Fatalf("Generate must only receive the answered sibling: %#v", active)
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
			judgeTestHit(1, 101, "空调故障", "问题：空调坏了，我住1302\n答案：转接", 0.91),
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
	if len(activePlan.TaskPlans) != 2 || activePlan.TaskPlans[0].TaskID != "T1" || activePlan.TaskPlans[1].TaskID != "T2" {
		t.Fatalf("expected the deferred and answerable Tasks to remain in customer order, got %#v", activePlan.TaskPlans)
	}
	if deferred := activePlan.TaskPlans[0]; deferred.OutputKind != "handoff" || deferred.ReplyRequired || deferred.Output != runtimeKnowledgeDeferredHandoffOutput {
		t.Fatalf("expected T1 to remain as a non-text Deferred Task, got %#v", deferred)
	}
	active := activeGenerationTaskPlans(intent, activePlan)
	if len(active) != 1 || active[0].TaskID != "T2" || active[0].Text != "顺便问早餐几点" {
		t.Fatalf("Generate must see only the answerable breakfast Task, got %#v", active)
	}
	if activePlan.ActiveTaskCount != 2 || activePlan.ReplyRequiredTaskCount != 1 {
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
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(selection.SelectedCandidateIDs) != 0 {
		t.Fatalf("single-candidate combined decision must be protocol-invalid: %#v", selection)
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
	if invalidStore.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(invalidStore.SelectedCandidateIDs) != 0 || len(invalidStore.SupportedFacts) != 0 {
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
	if missingLayer := parsed["T2"][knowledgeEvidenceLayerGeneral]; missingLayer.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(missingLayer.SelectedCandidateIDs) != 0 {
		t.Fatalf("missing layer must be isolated as protocol-invalid: %#v", missingLayer)
	}
	if missingTask := parsed["T3"][knowledgeEvidenceLayerStore]; missingTask.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(missingTask.SelectedCandidateIDs) != 0 {
		t.Fatalf("missing task must be isolated as protocol-invalid: %#v", missingTask)
	}
	if _, exists := parsed["UNKNOWN"]; exists {
		t.Fatalf("unknown task must never enter parsed selections: %#v", parsed["UNKNOWN"])
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRepairsMalformedFactsWithoutDroppingSiblingTask(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{
		{
			TaskID:    "T1",
			Query:     "外卖地址应该怎么填",
			Objective: "method",
			Entities:  []knowledgeEvidenceJudgeEntity{{Text: "外卖地址", Type: "location"}},
			Candidates: []knowledgeEvidenceJudgeCandidate{{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "外卖地址", "问题：外卖地址填哪些\n答案：丽斯未来酒店合肥南七店+对应楼层房间号。", 0.96),
			}},
		},
		{
			TaskID:    "T2",
			Query:     "早餐几点",
			Objective: "time",
			Candidates: []knowledgeEvidenceJudgeCandidate{{
				CandidateID: "T2C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 102, "早餐时间", "问题：早餐几点\n答案：早餐时间是7:00-9:30。", 0.95),
			}},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[` +
		`{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"location","statement":"外卖地址填写丽斯未来酒店合肥南七店加对应楼层房间号。","criticalValues":"丽斯未来酒店合肥南七店"}],"missingAspects":[]}]},` +
		`{"taskId":"T2","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T2C1"],"supportedFacts":[{"factId":"T2F1","aspect":"time","statement":"早餐时间是7:00-9:30。","criticalValues":["7:00-9:30"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("a malformed fact field must be isolated to its own layer: %v", err)
	}
	address := parsed["T1"][knowledgeEvidenceLayerStore]
	if address.Decision != knowledgeEvidenceDecisionDirectSingle || address.DecisionSource != "model_selected_repair" ||
		len(address.SelectedCandidateIDs) != 1 || address.SelectedCandidateIDs[0] != "T1C1" {
		t.Fatalf("the model-selected address FAQ must survive local fact repair: %#v", address)
	}
	joinedAddress := ""
	for _, fact := range address.SupportedFacts {
		joinedAddress += fact.Statement + " " + strings.Join(fact.CriticalValues, " ")
	}
	for _, required := range []string{"丽斯未来酒店合肥南七店", "楼层", "房间号"} {
		if !strings.Contains(joinedAddress, required) {
			t.Fatalf("repaired address facts lost %q: %#v", required, address.SupportedFacts)
		}
	}
	breakfast := parsed["T2"][knowledgeEvidenceLayerStore]
	if breakfast.Decision != knowledgeEvidenceDecisionDirectSingle || breakfast.DecisionSource != "model" ||
		len(breakfast.SupportedFacts) != 1 || !strings.Contains(breakfast.SupportedFacts[0].Statement, "7:00-9:30") {
		t.Fatalf("a malformed sibling layer must not damage a valid task: %#v", breakfast)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRepairsMalformedMissingAspectsWithoutPromotingPartial(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "外卖机器人能送到房间吗",
		Objective: "availability",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "外卖机器人", Type: "facility"},
			{Text: "房间", Type: "location"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "外卖机器人", "问题：有外卖机器人吗\n答案：有外卖机器人的。", 0.96),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"partial","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"门店有外卖机器人。","criticalValues":[]}],"missingAspects":"机器人配送范围"}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse partial response with malformed missingAspects: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionPartial || selection.DecisionSource != "model_selected_repair" ||
		len(selection.SelectedCandidateIDs) != 1 || len(selection.SupportedFacts) == 0 || len(selection.MissingAspects) == 0 {
		t.Fatalf("malformed missingAspects must be recomputed without promoting partial evidence: %#v", selection)
	}
	if !strings.Contains(strings.Join(selection.MissingAspects, " "), "范围") {
		t.Fatalf("partial repair must retain the unproven delivery scope: %#v", selection.MissingAspects)
	}
}

func TestParseKnowledgeEvidenceJudgeResponsePromotesCompletePartialWithMalformedMissingAspects(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "你们有外卖机器人吗",
		Objective: "availability",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "外卖机器人", Type: "facility"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "外卖机器人", "问题：你们有外卖机器人吗\n答案：有外卖机器人的。", 0.96),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"partial","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"门店有外卖机器人。","criticalValues":[]}],"missingAspects":"是否存在"}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse complete partial response with malformed missingAspects: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || selection.DecisionSource != "model_selected_repair" ||
		len(selection.SelectedCandidateIDs) != 1 || len(selection.SupportedFacts) == 0 || len(selection.MissingAspects) != 0 {
		t.Fatalf("a mechanically complete selected FAQ must survive malformed missingAspects repair: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRestoresTaskBoundQuantityAlongsidePrice(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "房间内两瓶矿泉水是否都免费",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "矿泉水数量和费用", "问题：房间里有两瓶矿泉水吗\n答案：是的，都是免费的。", 0.97),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"房间内矿泉水都是免费的。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse price-only model fact: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || selection.DecisionSource != "model_selected_repair" {
		t.Fatalf("the selected FAQ should be repaired rather than discarded: %#v", selection)
	}
	joined := ""
	for _, fact := range selection.SupportedFacts {
		joined += fact.Statement + " " + strings.Join(fact.CriticalValues, " ")
	}
	for _, required := range []string{"两瓶", "免费"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("repaired facts must retain both quantity and price, missing %q in %#v", required, selection.SupportedFacts)
		}
	}
}

func TestParseKnowledgeEvidenceJudgeResponsePromotesPartialWhenSelectedFAQProvesMissingQuantity(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "房间内两瓶矿泉水是否都免费",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "矿泉水数量和费用", "问题：房间里有两瓶矿泉水吗\n答案：是的，都是免费的。", 0.97),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"partial","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"房间内矿泉水都是免费的。","criticalValues":["免费"]}],"missingAspects":["矿泉水数量"]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse repairable partial response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || selection.DecisionSource != "model_selected_repair" || len(selection.MissingAspects) != 0 {
		t.Fatalf("a complete selected FAQ must be promoted instead of handed off: %#v", selection)
	}
	joined := ""
	for _, fact := range selection.SupportedFacts {
		joined += fact.Statement + " " + strings.Join(fact.CriticalValues, " ")
	}
	for _, required := range []string{"两瓶", "免费"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("promoted facts lost %q: %#v", required, selection.SupportedFacts)
		}
	}
}

func TestUnresolvedModelKnowledgeEvidenceMissingAspectsKeepsOtherSubjectQuantity(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		Query:     "矿泉水有两瓶，枕头有几个",
		Objective: "compound_information",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "矿泉水", Type: "supply"},
			{Text: "枕头", Type: "supply"},
		},
	}
	facts := []knowledgeEvidenceFact{{
		FactID:         "T1F1",
		Aspect:         "quantity",
		Statement:      "房间内有两瓶矿泉水。",
		CriticalValues: []string{"两瓶"},
	}}
	missing := unresolvedModelKnowledgeEvidenceMissingAspects(task, facts, []string{"枕头数量"})
	if len(missing) != 1 || missing[0] != "枕头数量" {
		t.Fatalf("one subject's quantity must not resolve another subject: %#v", missing)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsConflictingSelectedQuantity(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "房间内两瓶矿泉水是否都免费",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "矿泉水数量和费用", "问题：房间里有四瓶矿泉水吗\n答案：是的，都是免费的。", 0.97),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"房间内有四瓶矿泉水。","criticalValues":["四瓶"]},{"factId":"T1F2","aspect":"price","statement":"房间内矿泉水都是免费的。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse conflicting selected quantity: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("two bottles must not accept a selected FAQ that only confirms four: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsConflictingSelectedQuantityWithoutEntities(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "房间内两瓶矿泉水是否都免费",
		Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "矿泉水数量和费用", "问题：房间里有四瓶矿泉水吗\n答案：是的，都是免费的。", 0.97),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"房间内有四瓶矿泉水。","criticalValues":["四瓶"]},{"factId":"T1F2","aspect":"price","statement":"房间内矿泉水都是免费的。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse conflicting selected quantity without entities: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("an omitted entity list must not let four bottles answer two bottles: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseAcceptsEquivalentSelectedQuantityWithoutEntities(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "房间内2瓶矿泉水是否都免费",
		Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "矿泉水数量和费用", "问题：房间里有两瓶矿泉水吗\n答案：是的，都是免费的。", 0.97),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"房间内有两瓶矿泉水。","criticalValues":["两瓶"]},{"factId":"T1F2","aspect":"price","statement":"房间内矿泉水都是免费的。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse equivalent selected quantity without entities: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || !knowledgeEvidenceFactsContainCriticalValue(selection.SupportedFacts, "2瓶") {
		t.Fatalf("an omitted entity list must still accept an equivalent quantity: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRequiresEveryRequestedTimeSlot(t *testing.T) {
	startOnlyTask := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "早餐几点开始，几点结束",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "早餐开始时间", "问题：早餐几点开始\n答案：早餐7:00开始。", 0.97),
		}},
	}
	t.Run("direct start only is invalid", func(t *testing.T) {
		raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"早餐7:00开始。","criticalValues":["7:00"]}],"missingAspects":[]}]}]}`
		parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{startOnlyTask})
		if err != nil {
			t.Fatalf("parse start-only direct response: %v", err)
		}
		selection := parsed["T1"][knowledgeEvidenceLayerStore]
		if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
			t.Fatalf("start time alone cannot fully answer start and end: %#v", selection)
		}
	})
	t.Run("partial start keeps missing end", func(t *testing.T) {
		raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"partial","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"早餐7:00开始。","criticalValues":["7:00"]}],"missingAspects":["早餐结束时间"]}]}]}`
		parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{startOnlyTask})
		if err != nil {
			t.Fatalf("parse start-only partial response: %v", err)
		}
		selection := parsed["T1"][knowledgeEvidenceLayerStore]
		if selection.Decision != knowledgeEvidenceDecisionPartial || !strings.Contains(strings.Join(selection.MissingAspects, " "), "结束") {
			t.Fatalf("the unproven end time must remain missing: %#v", selection)
		}
	})
	t.Run("schedule range covers both slots", func(t *testing.T) {
		completeTask := startOnlyTask
		completeTask.Candidates = []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "早餐时间", "问题：早餐时间\n答案：早餐时间为7:00-9:30。", 0.97),
		}}
		raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"早餐时间为7:00-9:30。","criticalValues":["7:00-9:30"]}],"missingAspects":[]}]}]}`
		parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{completeTask})
		if err != nil {
			t.Fatalf("parse complete schedule response: %v", err)
		}
		selection := parsed["T1"][knowledgeEvidenceLayerStore]
		if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.MissingAspects) != 0 {
			t.Fatalf("a time range must cover both requested slots: %#v", selection)
		}
	})
}

func TestParseKnowledgeEvidenceJudgeResponseTreatsChineseAndArabicTaskBoundQuantitiesAsEquivalent(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "房间内2瓶矿泉水是否都免费",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "矿泉水数量和费用", "问题：房间里有两瓶矿泉水吗\n答案：是的，都是免费的。", 0.97),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"房间内矿泉水都是免费的。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse equivalent numeric quantity response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || selection.DecisionSource != "model_selected_repair" ||
		!knowledgeEvidenceFactsContainCriticalValue(selection.SupportedFacts, "2瓶") {
		t.Fatalf("2瓶 and 两瓶 must share one grounded quantity value: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRestoresIndividualCounterQuantity(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "房间内两个枕头是否都免费",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "枕头", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "枕头数量和费用", "问题：房间里有两个枕头吗\n答案：是的，都是免费的。", 0.97),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"房间内枕头都是免费的。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse individual-counter quantity response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || selection.DecisionSource != "model_selected_repair" ||
		!knowledgeEvidenceFactsContainCriticalValue(selection.SupportedFacts, "两个") ||
		!knowledgeEvidenceFactsContainCriticalValue(selection.SupportedFacts, "免费") {
		t.Fatalf("two pillows and their price must both remain grounded: %#v", selection)
	}
}

func TestKnowledgeEvidenceTaskBoundCriticalValuesExcludesIndividualCounterScope(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		excluded  []string
		preserved []string
	}{
		{name: "one room capacity", query: "一间房最多住几个人", excluded: []string{"一间"}},
		{name: "two guests sharing one room", query: "两个人住一间房可以吗", excluded: []string{"两个", "一间"}},
		{name: "three rooms facility scope", query: "三间房都有办公桌吗", excluded: []string{"三间"}},
		{name: "requested towel amount", query: "浴巾需要加一条，怎么取", excluded: []string{"一条"}},
		{name: "recommendation result count", query: "推荐一个既有沙发又有办公桌的房型", excluded: []string{"一个"}},
		{name: "requested pickup amount", query: "帮我拿两瓶矿泉水", excluded: []string{"两瓶"}},
		{name: "requested first person amount", query: "我要两瓶矿泉水", excluded: []string{"两瓶"}},
		{name: "requested needed amount", query: "我需要两条浴巾", excluded: []string{"两条"}},
		{name: "bottled water quantity", query: "房间内有两瓶矿泉水吗", preserved: []string{"两瓶"}},
		{name: "bottled water price scope", query: "两瓶矿泉水是否都免费", preserved: []string{"两瓶"}},
		{name: "delivery action does not erase price quantity", query: "你们送两瓶矿泉水都免费吗", preserved: []string{"两瓶"}},
		{name: "postposed pickup does not erase price quantity", query: "两瓶矿泉水免费吗怎么拿", preserved: []string{"两瓶"}},
		{name: "postposed pickup does not erase existence quantity", query: "房间有两瓶矿泉水吗怎么拿", preserved: []string{"两瓶"}},
		{name: "pillow quantity", query: "房间内有两个枕头吗", preserved: []string{"两个"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := knowledgeEvidenceTaskBoundCriticalValues(test.query)
			for _, excluded := range test.excluded {
				if containsString(values, excluded) {
					t.Fatalf("scope quantity %q must be excluded: %#v", excluded, values)
				}
			}
			for _, preserved := range test.preserved {
				if !containsString(values, preserved) {
					t.Fatalf("answer quantity %q must be preserved: %#v", preserved, values)
				}
			}
		})
	}
}

func TestParseKnowledgeEvidenceJudgeResponseDoesNotForceContextQuantityIntoDirectAnswer(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "一个房间最多住几个人",
		Objective: "quantity",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "房间", Type: "room"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "入住人数", "问题：一个房间最多住几个人\n答案：每间房最多住2人。", 0.96),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"每间房最多住2人。","criticalValues":["2人"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse room occupancy response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SupportedFacts) != 1 ||
		!knowledgeEvidenceFactsContainCriticalValue(selection.SupportedFacts, "2人") {
		t.Fatalf("one room is query scope, while 2 people is the grounded answer: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseKeepsPartialWhenTaskBoundQuantityIsStillMissing(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "房间内两瓶矿泉水是否都免费",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "矿泉水费用", "问题：房间内矿泉水收费吗\n答案：房间内矿泉水是免费的。", 0.92),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"partial","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"房间内矿泉水是免费的。","criticalValues":["免费"]}],"missingAspects":["矿泉水数量"]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse partial quantity response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionPartial || len(selection.SelectedCandidateIDs) != 1 || len(selection.SupportedFacts) == 0 {
		t.Fatalf("a confirmed price fact must survive while quantity remains unresolved: %#v", selection)
	}
	joinedFacts := ""
	for _, fact := range selection.SupportedFacts {
		joinedFacts += fact.Statement + " " + strings.Join(fact.CriticalValues, " ")
	}
	if !strings.Contains(joinedFacts, "免费") || strings.Contains(joinedFacts, "两瓶") {
		t.Fatalf("partial evidence must preserve only the grounded price fact: %#v", selection.SupportedFacts)
	}
	if !strings.Contains(strings.Join(selection.MissingAspects, " "), "数量") {
		t.Fatalf("the unresolved quantity must remain explicit: %#v", selection.MissingAspects)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseAcceptsPartialFactsAndStoreHandoff(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{
		{
			TaskID: "T1",
			Query:  "外卖机器人能送到房间吗",
			Entities: []knowledgeEvidenceJudgeEntity{
				{Text: "外卖机器人", Type: "facility"},
				{Text: "房间", Type: "location"},
			},
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
			{Text: "麦田房型", Type: "room_type"},
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
	if !containsString(store.SupportedFacts[0].CriticalValues, "麦田房型") {
		t.Fatalf("room-type suffix normalization must retain the customer's original entity in the grounded fact: %#v", store.SupportedFacts)
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

func TestDeterministicKnowledgeFallbackRejectsNonExactHandoff(t *testing.T) {
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
	if ok {
		t.Fatalf("non-exact handoff must not become authoritative: %#v", selection)
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

func TestRepairStoreServiceSupplyInsufficientFAQSelection(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Intent:    "service_request",
		Query:     "拖鞋没了",
		SubIntent: "supplies_self_help",
		Objective: "action_request",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "拖鞋自取", "问题：需要额外拖鞋怎么办\n答案：如需额外拖鞋，可前往1313对面洗衣房领取。", 0.8834),
		}},
	}
	selections := map[string]map[string]knowledgeEvidenceLayerSelection{
		"T1": {knowledgeEvidenceLayerStore: insufficientKnowledgeEvidenceLayerSelection()},
	}

	if repaired := repairStoreServiceSupplyInsufficientFAQSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 1 {
		t.Fatalf("expected one narrow store service rescue, repaired=%d selections=%#v", repaired, selections)
	}
	selection := selections["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || selection.DecisionSource != "store_service_faq_rescue" {
		t.Fatalf("unexpected narrow rescue selection: %#v", selection)
	}
	if missing := missingRequiredKnowledgeEvidenceAspects(task, selection.SupportedFacts); len(missing) != 0 {
		t.Fatalf("rescued supply FAQ must cover the complete self-service path, missing=%#v facts=%#v", missing, selection.SupportedFacts)
	}

	selections["T1"][knowledgeEvidenceLayerStore] = knowledgeEvidenceLayerSelection{Decision: knowledgeEvidenceDecisionPartial}
	if repaired := repairStoreServiceSupplyInsufficientFAQSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("partial Judge evidence remains model-owned, repaired=%d selections=%#v", repaired, selections)
	}
	task.Intent = "hotel_info"
	selections["T1"][knowledgeEvidenceLayerStore] = insufficientKnowledgeEvidenceLayerSelection()
	if repaired := repairStoreServiceSupplyInsufficientFAQSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("non-service tasks must not use the narrow rescue, repaired=%d selections=%#v", repaired, selections)
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
	selections := map[string]map[string]knowledgeEvidenceLayerSelection{
		"T1": {knowledgeEvidenceLayerStore: insufficientKnowledgeEvidenceLayerSelection()},
	}
	if repaired := repairStoreServiceSupplyInsufficientFAQSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("narrow production rescue must preserve same-layer conflicts: %#v", selections)
	}
	task.RawCandidates = append([]knowledgeEvidenceJudgeCandidate(nil), task.Candidates...)
	task.Candidates = task.Candidates[:1]
	selections["T1"][knowledgeEvidenceLayerStore] = insufficientKnowledgeEvidenceLayerSelection()
	if repaired := repairStoreServiceSupplyInsufficientFAQSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("budget-excluded raw conflicts must also block the narrow production rescue: %#v", selections)
	}
}

func TestHighConfidenceHotelInfoSupplySelfHelpUsesStrictStoreFAQRescue(t *testing.T) {
	for _, tt := range []struct {
		query     string
		subIntent string
		objective string
		item      string
		question  string
		answer    string
		location  string
	}{
		{
			query: "纸巾不够，在哪里拿", subIntent: "supplies_location", objective: "location", item: "纸巾",
			question: "房间纸巾用完了怎么办", answer: "可前往1313对面洗衣房领取纸巾。", location: "1313对面洗衣房",
		},
		{
			query: "浴巾需要加一条，怎么取", subIntent: "amenity_pickup", objective: "method", item: "浴巾",
			question: "需要额外浴巾怎么办", answer: "可前往1313对面洗衣房自取浴巾。", location: "1313对面洗衣房",
		},
		{
			query: "剃须刀在哪", subIntent: "amenity_location", objective: "location", item: "剃须刀",
			question: "剃须刀有吗", answer: "酒店提供一次性剃须刀，放置在1313房间对面的洗衣房内，可自行取用。", location: "1313房间对面的洗衣房",
		},
		{
			query: "牙刷没有了，怎么获取或补充", subIntent: "supplies_self_help", objective: "method", item: "牙刷",
			question: "牙刷不够怎么办", answer: "可前往1313对面洗衣房自取牙刷。", location: "1313对面洗衣房",
		},
	} {
		task := knowledgeEvidenceJudgeTask{
			TaskID: "T1", Intent: "hotel_info", Query: tt.query, SubIntent: tt.subIntent, Objective: tt.objective,
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
			t.Fatalf("hotel_info operation task must use the same strict store FAQ rescue: %#v ok=%v", selection, ok)
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

func TestHighConfidenceHotelInfoSupplyRescueKeepsMinimumScore(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Intent: "hotel_info", Query: "纸巾不够用了，怎么获取或补充", SubIntent: "supplies_self_help", Objective: "method",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "纸巾", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "纸巾自取", "问题：房间纸巾用完了怎么办\n答案：可前往1313对面洗衣房领取纸巾。", 0.81),
		}},
	}
	if selection, ok := highConfidenceDirectFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("semantic store rescue must not lower the fixed score threshold: %#v", selection)
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
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
		t.Fatalf("a direct answer missing the requested quantity must be protocol-invalid, got %#v", selection)
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
		TaskID:   "T1",
		Query:    "麦田房型有办公桌吗",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "麦田房型", Type: "room_type"}, {Text: "办公桌", Type: "facility"}},
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
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || selection.DecisionSource != "model_selected_repair" ||
		len(selection.SelectedCandidateIDs) != 1 || selection.SelectedCandidateIDs[0] != "T1C1" || len(selection.SupportedFacts) != 1 ||
		!strings.Contains(selection.SupportedFacts[0].Statement, "办公桌") || strings.Contains(selection.SupportedFacts[0].Statement, "沙发") {
		t.Fatalf("hallucinated facts must be replaced only with facts rebuilt from the selected FAQ: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseDoesNotGroundFactsFromTitleOrTaskQuery(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:   "T1",
		Query:    "麦田房型配备沙发吗",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "麦田房型", Type: "room_type"}, {Text: "沙发", Type: "facility"}},
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
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
		t.Fatalf("a selected FAQ about a different requested facility must be protocol-invalid: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRepairsGroundedSynonymFacts(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Intent:    "service_request",
		SubIntent: "supplies_self_help",
		Objective: "method",
		Query:     "牙具在哪里领取",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "牙具", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "牙刷领取", "问题：牙刷在哪里拿\n答案：牙刷可以到1313对面洗衣房领取。", 0.95),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"method","statement":"牙刷可以到1313对面洗衣房领取。","criticalValues":["1313对面洗衣房","1313对面洗衣房"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || selection.DecisionSource != "model_selected_repair" ||
		len(selection.SelectedCandidateIDs) != 1 || selection.SelectedCandidateIDs[0] != "T1C1" || len(selection.SupportedFacts) == 0 {
		t.Fatalf("a grounded model-selected synonym must survive fact protocol repair: %#v", selection)
	}
	combined := ""
	for _, fact := range selection.SupportedFacts {
		combined += fact.Statement
	}
	if !strings.Contains(combined, "牙刷") || !strings.Contains(combined, "1313对面洗衣房") {
		t.Fatalf("repaired facts must come from the selected FAQ: %#v", selection.SupportedFacts)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseDoesNotJoinOneFactAcrossSelectedFAQs(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:   "T1",
		Query:    "麦田房型有办公桌吗",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "麦田房型", Type: "room_type"}, {Text: "办公桌", Type: "facility"}},
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
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
		t.Fatalf("different room types must not be joined into one fact: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseCombinesSeparateRoomTypeFacts(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "合柴和艺林房型都有沙发吗",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "合柴房型", Type: "room_type"},
			{Text: "艺林房型", Type: "room_type"},
			{Text: "沙发", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "合柴沙发", "问题：合柴房型有沙发吗\n答案：有沙发。", 0.95)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "艺林沙发", "问题：艺林房型有沙发吗\n答案：有沙发。", 0.94)},
		},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"合柴和艺林房型都有沙发。","criticalValues":["合柴","艺林","沙发"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectCombined || len(selection.SelectedCandidateIDs) != 2 || len(selection.SupportedFacts) == 0 {
		t.Fatalf("separate same-layer FAQs may jointly cover different requested room types: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsRoomTypeFacilityMatrixMismatch(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "合柴和艺林房型都有沙发吗",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "合柴房型", Type: "room_type"},
			{Text: "艺林房型", Type: "room_type"},
			{Text: "沙发", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "合柴沙发", "问题：合柴房型有沙发吗\n答案：有沙发。", 0.95)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "艺林办公桌", "问题：艺林房型有办公桌吗\n答案：有办公桌。", 0.94)},
		},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"合柴和艺林房型都有沙发。","criticalValues":["合柴","艺林","沙发"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
		t.Fatalf("room-type/facility pairs must be covered without cross-candidate mismatch: %#v", selection)
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
			if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
				t.Fatalf("unsafe intersection must be protocol-invalid: %#v", selection)
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
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
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
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
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
	for _, answer := range []string{
		"可以联系门店确认。", "可以咨询同事。", "可以尝试申请。", "支持联系管家。", "提供咨询服务。", "不需要。", "不能。",
	} {
		if knowledgeEvidenceFAQAnswerConfirmsQuestion(answer) {
			t.Fatalf("guidance or negative answer must not confirm its FAQ question: %q", answer)
		}
	}
}

func TestKnowledgeEvidenceFAQAnswerConfirmationChecksTaskBoundContradictions(t *testing.T) {
	breakfast := knowledgeEvidenceJudgeTask{
		Query:     "有早餐吗",
		Objective: "availability",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "service"}},
	}
	if !knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(breakfast, "有早餐吗", "是的，但不需要预约。") {
		t.Fatal("an unrelated reservation condition must not erase breakfast existence")
	}
	if !knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(breakfast, "有早餐吗", "是的，早餐正常提供，但具体菜品可能调整。") {
		t.Fatal("uncertainty about an unrelated detail must not erase breakfast existence")
	}
	for _, answer := range []string{"是的，不过当前没有提供。", "是的，但不提供早餐。", "是的，当前有早餐，不过实际上早餐没有提供。"} {
		if knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(breakfast, "有早餐吗", answer) {
			t.Fatalf("a contradictory answer must not confirm the FAQ question: %q", answer)
		}
	}
	water := knowledgeEvidenceJudgeTask{
		Query:     "有两瓶矿泉水吗",
		Objective: "quantity",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
	}
	if knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(water, "有两瓶矿泉水吗", "是的，但不是两瓶，是四瓶。") {
		t.Fatal("a conflicting quantity must not confirm the FAQ premise")
	}
	if knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(water, "有两瓶矿泉水吗", "是的，但具体数量不确定。") {
		t.Fatal("an uncertain quantity must not confirm the FAQ premise")
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

func TestFinalizeKnowledgeEvidenceFactsDoesNotRestoreCourtesyOnlyFact(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "老板是谁"}
	facts := finalizeKnowledgeEvidenceFactsForTask(task, []knowledgeEvidenceFact{{
		FactID:    "T1F1",
		Aspect:    "other",
		Statement: "祝您入住愉快。",
	}})
	if len(facts) != 0 {
		t.Fatalf("courtesy-only text must not be restored by the single-fact fallback: %#v", facts)
	}
}

func TestFinalizeKnowledgeEvidenceFactsKeepsGroundedOtherWithoutBypassingRequiredAspect(t *testing.T) {
	identityFacts := finalizeKnowledgeEvidenceFactsForTask(knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "老板是谁",
	}, []knowledgeEvidenceFact{{
		FactID:    "T1F1",
		Aspect:    "other",
		Statement: "酒店董事长是汤东强。",
	}})
	if len(identityFacts) != 1 || identityFacts[0].Aspect != "other" {
		t.Fatalf("a grounded descriptive fact must survive an unclassified aspect: %#v", identityFacts)
	}

	methodTask := knowledgeEvidenceJudgeTask{
		TaskID:    "T2",
		Intent:    "service_request",
		Query:     "拖鞋没了",
		Objective: "action_request",
	}
	methodFacts := finalizeKnowledgeEvidenceFactsForTask(methodTask, []knowledgeEvidenceFact{{
		FactID:    "T2F1",
		Aspect:    "existence",
		Statement: "房间内配有拖鞋。",
	}})
	if len(methodFacts) != 0 {
		t.Fatalf("an existence fact must not be restored as a missing service method: %#v", methodFacts)
	}
	if missing := strictMechanicalMissingKnowledgeEvidenceAspects(methodTask, methodFacts); !containsString(missing, knowledgeEvidenceAspectLabel("method")) {
		t.Fatalf("the required service method must remain missing: %#v", missing)
	}
}

func TestSelectedExistenceFAQUsesQuestionAnswerUnitForShortAndListAnswers(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		subject string
		answer  string
		want    bool
	}{
		{name: "short capability", query: "可以开发票吗", subject: "发票", answer: "可以。", want: true},
		{name: "short negative", query: "有静音空调房吗", subject: "静音空调房", answer: "没有。", want: true},
		{name: "concrete list", query: "早餐有哪些", subject: "早餐", answer: "牛奶、面包。", want: true},
		{name: "guidance is not an answer", query: "可以开发票吗", subject: "发票", answer: "可以联系门店确认。", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{
				TaskID:    "T1",
				Query:     tt.query,
				Objective: "availability",
				Entities:  []knowledgeEvidenceJudgeEntity{{Text: tt.subject, Type: "facility"}},
				Candidates: []knowledgeEvidenceJudgeCandidate{{
					CandidateID: "T1C1",
					Layer:       knowledgeEvidenceLayerStore,
					Hit:         judgeTestHit(1, 101, tt.query, "问题："+tt.query+"\n答案："+tt.answer, 0.95),
				}},
			}
			got := selectedKnowledgeEvidenceAnswersMatchSingleExistenceSubject(task, knowledgeEvidenceLayerStore, []string{"T1C1"})
			if got != tt.want {
				t.Fatalf("unexpected FAQ unit support result: got %v want %v", got, tt.want)
			}
		})
	}
}

func TestEntitylessSingleExistenceQuestionKeepsCandidateSubjectBound(t *testing.T) {
	tests := []struct {
		name              string
		query             string
		candidateQuestion string
		candidateAnswer   string
		want              bool
	}{
		{name: "same subject", query: "有早餐吗", candidateQuestion: "有早餐吗", candidateAnswer: "有的。", want: true},
		{name: "different subject", query: "有早餐吗", candidateQuestion: "有晚餐吗", candidateAnswer: "有晚餐。", want: false},
		{name: "specific coupon cannot answer breakfast", query: "有早餐吗", candidateQuestion: "有早餐券吗", candidateAnswer: "有的。", want: false},
		{name: "breakfast cannot answer specific coupon", query: "有早餐券吗", candidateQuestion: "有早餐吗", candidateAnswer: "有的。", want: false},
		{name: "specific quiet room cannot answer air conditioner", query: "房间有空调吗", candidateQuestion: "有静音空调房吗", candidateAnswer: "有的。", want: false},
		{name: "coupon action cannot answer breakfast", query: "有早餐吗", candidateQuestion: "早餐券能用吗", candidateAnswer: "可以。", want: false},
		{name: "room booking action cannot answer air conditioner", query: "房间有空调吗", candidateQuestion: "静音空调房能订吗", candidateAnswer: "可以。", want: false},
		{name: "affirmative subtype proves generic existence", query: "有拖鞋吗", candidateQuestion: "有一次性拖鞋吗", candidateAnswer: "有的。", want: true},
		{name: "irrelevant negative clause keeps affirmative subtype", query: "有拖鞋吗", candidateQuestion: "有一次性拖鞋吗", candidateAnswer: "有一次性拖鞋，无需自带。", want: true},
		{name: "negative subtype does not prove generic absence", query: "有拖鞋吗", candidateQuestion: "有洗澡用拖鞋吗", candidateAnswer: "没有。", want: false},
		{name: "explicit negative provision does not prove generic existence", query: "有拖鞋吗", candidateQuestion: "有一次性拖鞋吗", candidateAnswer: "客房不配备一次性拖鞋。", want: false},
		{name: "reverse qualifier is not a subtype", query: "有押金吗", candidateQuestion: "可以免押金吗", candidateAnswer: "可以。", want: false},
		{name: "negative qualifier is not a subtype", query: "有吸烟房吗", candidateQuestion: "有非吸烟房吗", candidateAnswer: "有的。", want: false},
		{name: "normalized synonym", query: "房间有书桌吗", candidateQuestion: "客房配备办公桌吗", candidateAnswer: "有的。", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hit := judgeTestHit(1, 101, test.candidateQuestion, "问题："+test.candidateQuestion+"\n答案："+test.candidateAnswer, 0.97)
			task := knowledgeEvidenceJudgeTask{
				TaskID: "T1", Query: test.query, Objective: "availability",
				Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
			}
			candidate := task.Candidates[0]
			question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
			if got := knowledgeEvidenceCandidateMatchesTaskSubjects(task, candidate, question, answer); got != test.want {
				t.Fatalf("candidate subject guard mismatch: got=%v want=%v question=%q answer=%q", got, test.want, question, answer)
			}
			if got := selectedKnowledgeEvidenceAnswersMatchSingleExistenceSubject(task, knowledgeEvidenceLayerStore, []string{"T1C1"}); got != test.want {
				t.Fatalf("selected existence subject guard mismatch: got=%v want=%v", got, test.want)
			}
		})
	}
}

func TestModelSelectedNearbyFAQIsNotRejectedByColloquialSubjectSpelling(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Intent:    "hotel_info",
		Query:     "酒店附近有没有好玩儿的地方",
		SubIntent: "surrounding_facilities",
		Objective: "recommendation",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "附近", Type: "location"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(
				3,
				101,
				"附近有什么好玩的地方？",
				"问题：附近有什么好玩的地方？\n答案：酒店周边有罍街、包公园和逍遥津公园。",
				0.8918,
			),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"酒店周边有罍街、包公园和逍遥津公园。","criticalValues":["罍街","包公园","逍遥津公园"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse nearby FAQ response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SelectedCandidateIDs) != 1 || selection.SelectedCandidateIDs[0] != "T1C1" {
		t.Fatalf("model-selected nearby FAQ must survive local protocol validation: %#v", selection)
	}
}

func TestGenericEntityDoesNotDisableConcreteExistenceSubjectGuard(t *testing.T) {
	tests := []struct {
		name              string
		query             string
		entity            knowledgeEvidenceJudgeEntity
		candidateQuestion string
		candidateAnswer   string
		want              bool
	}{
		{name: "generic room rejects sofa", query: "房间有空调吗", entity: knowledgeEvidenceJudgeEntity{Text: "房间", Type: "location"}, candidateQuestion: "房间有沙发吗", candidateAnswer: "有的。", want: false},
		{name: "generic room accepts air conditioner", query: "房间有空调吗", entity: knowledgeEvidenceJudgeEntity{Text: "房间", Type: "location"}, candidateQuestion: "客房配备空调吗", candidateAnswer: "有的。", want: true},
		{name: "generic hotel rejects dinner", query: "酒店有早餐吗", entity: knowledgeEvidenceJudgeEntity{Text: "酒店", Type: "location"}, candidateQuestion: "酒店有晚餐吗", candidateAnswer: "有的。", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hit := judgeTestHit(1, 101, test.candidateQuestion, "问题："+test.candidateQuestion+"\n答案："+test.candidateAnswer, 0.97)
			task := knowledgeEvidenceJudgeTask{
				TaskID: "T1", Query: test.query, Objective: "availability", Entities: []knowledgeEvidenceJudgeEntity{test.entity},
				Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
			}
			candidate := task.Candidates[0]
			question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
			if got := knowledgeEvidenceCandidateMatchesTaskSubjects(task, candidate, question, answer); got != test.want {
				t.Fatalf("generic entity subject guard mismatch: got=%v want=%v subject=%q", got, test.want, func() string { subject, _ := knowledgeEvidenceImplicitSingleExistenceSubject(task); return subject }())
			}
		})
	}
}

func TestSingleExistenceSubjectSurvivesSameSubjectCompoundAspects(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "有早餐吗，还免费吗", Objective: "compound_information"}
	subject, guarded := knowledgeEvidenceImplicitSingleExistenceSubject(task)
	if !guarded || subject != "早餐" {
		t.Fatalf("same-subject compound question must retain the breakfast binding: subject=%q guarded=%v", subject, guarded)
	}

	breakfastHit := judgeTestHit(1, 101, "早餐是否提供且免费", "问题：有早餐吗，早餐免费吗\n答案：有早餐，并且免费。", 0.97)
	dinnerHit := judgeTestHit(1, 102, "晚餐是否提供且免费", "问题：有晚餐吗，晚餐免费吗\n答案：有晚餐，并且免费。", 0.97)
	for _, test := range []struct {
		name string
		hit  rag.RetrieveResult
		want bool
	}{
		{name: "same subject", hit: breakfastHit, want: true},
		{name: "different subject", hit: dinnerHit, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := knowledgeEvidenceJudgeCandidate{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: test.hit}
			task.Candidates = []knowledgeEvidenceJudgeCandidate{candidate}
			question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
			if got := knowledgeEvidenceCandidateMatchesTaskSubjects(task, candidate, question, answer); got != test.want {
				t.Fatalf("compound subject guard mismatch: got=%v want=%v question=%q answer=%q", got, test.want, question, answer)
			}
		})
	}
}

func TestEntitylessMultiSubjectExistenceDoesNotInventSingleSubject(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "办公桌和沙发都有吗", Objective: "availability"}
	if subject, guarded := knowledgeEvidenceImplicitSingleExistenceSubject(task); guarded || subject != "" {
		t.Fatalf("a compound existence question must stay model-owned, got subject=%q guarded=%v", subject, guarded)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsEntitylessCrossSubjectExistence(t *testing.T) {
	hit := judgeTestHit(1, 101, "有晚餐吗", "问题：有晚餐吗\n答案：有晚餐。", 0.97)
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "有早餐吗", Objective: "availability",
		Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"有晚餐。","criticalValues":["晚餐"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse cross-subject existence response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("an entityless breakfast question must not accept a dinner fact: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseKeepsReviewQuestionOutOfPriceAndMethod(t *testing.T) {
	hit := judgeTestHit(1, 101, "住客评价怎么样", "问题：住客评价怎么样\n答案：住客普遍评价房间干净，入住方便。", 0.97)
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "你们酒店住客评价怎么样", Intent: "hotel_info", Objective: "general_guidance",
		Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
	}
	if aspects := requiredKnowledgeEvidenceAspects(task); knowledgeEvidenceContainsString(aspects, "price") || knowledgeEvidenceContainsString(aspects, "method") {
		t.Fatalf("a guest-review question must not inherit price or method from substrings: %#v", aspects)
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"other","statement":"住客普遍评价房间干净，入住方便。","criticalValues":[]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse exact guest-review FAQ: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("an exact grounded review FAQ must remain direct: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseKeepsWalletRequestOutOfPrice(t *testing.T) {
	hit := judgeTestHit(1, 101, "钱包落在房间了怎么办", "问题：钱包落在房间了怎么办\n答案：请先确认房号，再联系门店同事处理。", 0.97)
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "钱包落在房间了怎么办", Intent: "service_request", Objective: "method",
		Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
	}
	aspects := requiredKnowledgeEvidenceAspects(task)
	if !knowledgeEvidenceContainsString(aspects, "method") || knowledgeEvidenceContainsString(aspects, "price") {
		t.Fatalf("a wallet service request must require method without inventing price: %#v", aspects)
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"method","statement":"请先确认房号，再联系门店同事处理。","criticalValues":[]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse exact wallet service FAQ: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("an exact grounded wallet FAQ must remain direct: %#v", selection)
	}
}

func TestRequiredKnowledgeEvidenceAspectsDistinguishesPhoneValueFromContactAction(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		objective  string
		wantMethod bool
		wantPhone  bool
	}{
		{name: "phone value", query: "管家的联系电话是多少", objective: "general_guidance", wantMethod: false, wantPhone: true},
		{name: "contact method", query: "怎么联系管家", objective: "method", wantMethod: true},
		{name: "phone action", query: "请通过电话联系前台", objective: "action_request", wantMethod: true},
		{name: "modify contact", query: "帮我修改联系电话", objective: "action_request", wantMethod: true},
		{name: "ordinary service", query: "拖鞋没了", objective: "action_request", wantMethod: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{Intent: "service_request", Query: tt.query, Objective: tt.objective}
			aspects := requiredKnowledgeEvidenceAspects(task)
			gotMethod := knowledgeEvidenceContainsString(aspects, "method")
			gotPhone := knowledgeEvidenceContainsString(aspects, "phone")
			if gotMethod != tt.wantMethod || gotPhone != tt.wantPhone {
				t.Fatalf("method=%v phone=%v wantMethod=%v wantPhone=%v aspects=%#v", gotMethod, gotPhone, tt.wantMethod, tt.wantPhone, aspects)
			}
		})
	}
}

func TestParseKnowledgeEvidenceJudgeResponseKeepsPhoneValueAnswerDirect(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Intent: "service_request", Query: "管家的联系电话是多少", Objective: "general_guidance",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "管家电话", "问题：管家的联系电话是多少\n答案：18256022128", 0.93),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"other","statement":"管家电话是18256022128。","criticalValues":["18256022128"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse phone value answer: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("a grounded phone value must not be rejected as a missing contact method: %#v", selection)
	}
	incompleteTask := task
	incompleteTask.Candidates = []knowledgeEvidenceJudgeCandidate{{
		CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
		Hit: judgeTestHit(1, 101, "管家电话", "问题：管家的联系电话是多少\n答案：前台在一楼。", 0.93),
	}}
	raw = `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"other","statement":"前台在一楼。","criticalValues":[]}],"missingAspects":[]}]}]}`
	parsed, err = parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{incompleteTask})
	if err != nil {
		t.Fatalf("parse incomplete phone value answer: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision == knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("a phone query without a phone value must remain incomplete: %#v", selection)
	}
}

func TestKnowledgeEvidencePhoneValueValidationRejectsOrdinaryIdentifiers(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{text: "管家电话是18256022128", want: true},
		{text: "客服电话是400-123-4567", want: true},
		{text: "手机号是138 0013 8000", want: true},
		{text: "座机是0551-12345678", want: true},
		{text: "座机是(0551) 12345678", want: true},
		{text: "请提供房号，管家电话是18256022128", want: true},
		{text: "订单号是20260902", want: false},
		{text: "工号是12345678", want: false},
		{text: "房号是1313", want: false},
		{text: "订单号是13800138000", want: false},
		{text: "工号是13800138000", want: false},
	}
	for _, test := range tests {
		fact := knowledgeEvidenceFact{Aspect: "other", Statement: test.text}
		if got := knowledgeEvidenceFactHasPhoneValue(fact); got != test.want {
			t.Fatalf("phone match for %q = %v, want %v", test.text, got, test.want)
		}
	}
}

func TestTaskBoundQuantityConflictKeepsDifferentConditionsIndependent(t *testing.T) {
	tests := []struct {
		name              string
		query             string
		candidateQuestion string
		wantConflict      bool
	}{
		{name: "matching conditions", query: "工作日矿泉水两瓶，周末四瓶吗", candidateQuestion: "工作日矿泉水两瓶，周末四瓶吗", wantConflict: false},
		{name: "same condition differs", query: "工作日矿泉水两瓶，周末四瓶吗", candidateQuestion: "工作日矿泉水三瓶，周末四瓶吗", wantConflict: true},
		{name: "weekend is missing", query: "工作日矿泉水两瓶，周末四瓶吗", candidateQuestion: "工作日矿泉水两瓶吗", wantConflict: true},
		{name: "same value still requires weekend", query: "工作日矿泉水两瓶，周末两瓶吗", candidateQuestion: "工作日矿泉水两瓶吗", wantConflict: true},
		{name: "different explicit condition does not cover", query: "工作日矿泉水两瓶，周末四瓶吗", candidateQuestion: "工作日矿泉水两瓶，节假日矿泉水四瓶吗", wantConflict: true},
		{name: "conditional evidence cannot answer a general rule", query: "矿泉水两瓶吗", candidateQuestion: "工作日矿泉水两瓶吗", wantConflict: true},
		{name: "general evidence can answer a conditioned same-value rule", query: "工作日矿泉水两瓶，周末两瓶吗", candidateQuestion: "矿泉水两瓶吗", wantConflict: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hit := judgeTestHit(1, 101, "矿泉水数量", "问题："+test.candidateQuestion+"\n答案：是的。", 0.97)
			task := knowledgeEvidenceJudgeTask{
				TaskID: "T1", Query: test.query, Objective: "quantity",
				Entities:   []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
				Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
			}
			got := knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(task, knowledgeEvidenceLayerStore, []string{"T1C1"})
			if got != test.wantConflict {
				question, answer := splitKnowledgeEvidenceFAQForQuery(hit, task.Query)
				clauses := splitKnowledgeEvidenceAnswerClauses(question)
				clauseOccurrences := make([][]knowledgeEvidenceQuantityOccurrence, 0, len(clauses))
				for _, clause := range clauses {
					clauseOccurrences = append(clauseOccurrences, knowledgeEvidenceQuantityOccurrences(clause, "矿泉水"))
				}
				t.Fatalf("condition-bound quantity conflict mismatch: got=%v want=%v queryValues=%#v queryConditions=%#v question=%q answer=%q clauses=%#v clauseOccurrences=%#v questionOccurrences=%#v answerOccurrences=%#v",
					got, test.wantConflict,
					knowledgeEvidenceTaskBoundCriticalValues(task.Query),
					knowledgeEvidenceQuantityConditionsByValue(task.Query, "矿泉水"),
					question,
					answer,
					clauses,
					clauseOccurrences,
					knowledgeEvidenceQuantityOccurrences(question, "矿泉水"),
					knowledgeEvidenceQuantityOccurrences(answer, "矿泉水"),
				)
			}
		})
	}
}

func TestTaskBoundQuantityConflictRejectsNarrowerFAQScope(t *testing.T) {
	tests := []struct {
		name              string
		query             string
		candidateQuestion string
		candidateAnswer   string
	}{
		{
			name:              "extra daypart condition",
			query:             "工作日矿泉水两瓶吗",
			candidateQuestion: "工作日晚上矿泉水两瓶吗",
			candidateAnswer:   "是的。",
		},
		{
			name:              "answer inherits faq question condition",
			query:             "周末矿泉水两瓶吗",
			candidateQuestion: "工作日矿泉水两瓶吗",
			candidateAnswer:   "矿泉水有两瓶。",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hit := judgeTestHit(1, 101, "矿泉水数量", "问题："+test.candidateQuestion+"\n答案："+test.candidateAnswer, 0.97)
			task := knowledgeEvidenceJudgeTask{
				TaskID: "T1", Query: test.query, Objective: "quantity",
				Entities:   []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
				Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
			}
			if !knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(task, knowledgeEvidenceLayerStore, []string{"T1C1"}) {
				t.Fatal("a narrower or differently conditioned FAQ must not cover the query")
			}
		})
	}
}

func TestTaskBoundQuantityConflictAllowsExplicitDateUniversalAnswerToOverrideFAQDate(t *testing.T) {
	hit := judgeTestHit(
		1,
		101,
		"矿泉水数量",
		"问题：周末矿泉水两瓶吗\n答案：每天矿泉水都有两瓶。",
		0.97,
	)
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "工作日矿泉水两瓶吗", Objective: "quantity",
		Entities:   []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
	}
	if knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(task, knowledgeEvidenceLayerStore, []string{"T1C1"}) {
		t.Fatal("an answer that explicitly applies every day must not inherit the FAQ question's different calendar-day condition")
	}
}

func TestTaskBoundQuantityUniversalAnswerOnlyOverridesDeclaredDimension(t *testing.T) {
	tests := []struct {
		name         string
		answer       string
		wantConflict bool
	}{
		{name: "date universal keeps unmentioned daypart", answer: "每天矿泉水都有两瓶。", wantConflict: true},
		{name: "date universal plus matching daypart", answer: "每天晚上矿泉水都有两瓶。", wantConflict: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hit := judgeTestHit(
				1,
				101,
				"矿泉水数量",
				"问题：周末白天矿泉水两瓶吗\n答案："+test.answer,
				0.97,
			)
			task := knowledgeEvidenceJudgeTask{
				TaskID: "T1", Query: "工作日晚上矿泉水两瓶吗", Objective: "quantity",
				Entities:   []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
				Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
			}
			if got := knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(task, knowledgeEvidenceLayerStore, []string{"T1C1"}); got != test.wantConflict {
				t.Fatalf("dimension-specific universal condition mismatch: got=%v want=%v", got, test.wantConflict)
			}
		})
	}
}

func TestKnowledgeEvidenceQuantityConditionsComparableKeepsImplicitScopeStrict(t *testing.T) {
	tests := []struct {
		name      string
		required  []string
		candidate []string
		want      bool
	}{
		{name: "candidate silently omits daypart", required: []string{"workday", "night"}, candidate: []string{"workday"}, want: false},
		{name: "candidate adds daypart", required: []string{"workday"}, candidate: []string{"workday", "night"}, want: false},
		{name: "candidate conflicts on calendar", required: []string{"workday", "night"}, candidate: []string{"weekend"}, want: false},
		{name: "conditioned evidence cannot prove general", required: nil, candidate: []string{"workday"}, want: false},
		{name: "general evidence proves conditioned", required: []string{"workday"}, candidate: nil, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := knowledgeEvidenceQuantityConditionsComparable(test.required, test.candidate); got != test.want {
				t.Fatalf("quantity condition comparison mismatch: got=%v want=%v", got, test.want)
			}
		})
	}
}

func TestKnowledgeEvidenceQuantityConditionsComparableWithUniversalOnlyWidensDeclaredDimension(t *testing.T) {
	tests := []struct {
		name      string
		required  []string
		candidate []string
		universal map[string]struct{}
		want      bool
	}{
		{
			name:      "date universal keeps matching daypart",
			required:  []string{"workday", "night"},
			candidate: []string{"night"},
			universal: map[string]struct{}{"calendar_day_type": {}},
			want:      true,
		},
		{
			name:      "date universal does not erase daypart",
			required:  []string{"workday", "night"},
			candidate: []string{"daytime"},
			universal: map[string]struct{}{"calendar_day_type": {}},
			want:      false,
		},
		{
			name:      "daypart universal keeps matching date",
			required:  []string{"workday", "night"},
			candidate: []string{"workday"},
			universal: map[string]struct{}{"daypart": {}},
			want:      true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := knowledgeEvidenceQuantityConditionsComparableWithUniversal(test.required, test.candidate, test.universal); got != test.want {
				t.Fatalf("universal quantity condition comparison mismatch: got=%v want=%v", got, test.want)
			}
		})
	}
}

func TestTaskBoundQuantityRequirementsKeepSameValueConditionsSeparate(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "工作日矿泉水两瓶，周末两瓶吗", Objective: "quantity",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
	}
	requirements := knowledgeEvidenceTaskBoundQuantityRequirements(task, "矿泉水")
	if len(requirements) != 2 {
		t.Fatalf("same quantity under two conditions must remain two requirements: %#v", requirements)
	}
	if strings.Join(requirements[0].Conditions, ",") == strings.Join(requirements[1].Conditions, ",") {
		t.Fatalf("workday and weekend requirements must not collapse: %#v", requirements)
	}
}

func TestTaskBoundQuantityRequirementsSplitSharedPredicateConditions(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "工作日和周末矿泉水都是两瓶吗", Objective: "quantity",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
	}
	requirements := knowledgeEvidenceTaskBoundQuantityRequirements(task, "矿泉水")
	if len(requirements) != 2 {
		t.Fatalf("shared quantity predicate must expand to one requirement per condition: %#v", requirements)
	}
	if len(requirements[0].Conditions) != 1 || len(requirements[1].Conditions) != 1 ||
		requirements[0].Conditions[0] == requirements[1].Conditions[0] {
		t.Fatalf("workday and weekend must remain independently provable: %#v", requirements)
	}

	workdayOnly := judgeTestHit(1, 101, "工作日矿泉水数量", "问题：工作日矿泉水两瓶吗\n答案：是的。", 0.97)
	task.Candidates = []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: workdayOnly}}
	if !knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(task, knowledgeEvidenceLayerStore, []string{"T1C1"}) {
		t.Fatal("a workday-only FAQ must not prove the shared weekend requirement")
	}

	complete := judgeTestHit(1, 102, "工作日和周末矿泉水数量", "问题：工作日和周末矿泉水都是两瓶吗\n答案：是的。", 0.97)
	task.Candidates = []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: complete}}
	if knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(task, knowledgeEvidenceLayerStore, []string{"T1C1"}) {
		t.Fatal("an FAQ covering both named conditions must remain complete")
	}
}

func TestTaskBoundQuantityRequirementsPreserveCrossDimensionConjunction(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "入住当天晚上矿泉水是两瓶吗", Objective: "quantity",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
	}
	requirements := knowledgeEvidenceTaskBoundQuantityRequirements(task, "矿泉水")
	if len(requirements) != 1 || len(requirements[0].Conditions) != 2 ||
		!knowledgeEvidenceContainsString(requirements[0].Conditions, "checkin_day") ||
		!knowledgeEvidenceContainsString(requirements[0].Conditions, "night") {
		t.Fatalf("stay-day and daypart conditions must remain conjunctive: %#v", requirements)
	}

	checkinDay := judgeTestHit(1, 101, "入住当天矿泉水数量", "问题：入住当天矿泉水两瓶吗\n答案：是的。", 0.97)
	night := judgeTestHit(2, 102, "晚上矿泉水数量", "问题：晚上矿泉水两瓶吗\n答案：是的。", 0.96)
	task.Candidates = []knowledgeEvidenceJudgeCandidate{
		{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: checkinDay},
		{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: night},
	}
	if !knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(task, knowledgeEvidenceLayerStore, []string{"T1C1", "T1C2"}) {
		t.Fatal("separate stay-day and night FAQs must not prove their intersection")
	}

	combined := judgeTestHit(3, 103, "入住当天晚上矿泉水数量", "问题：入住当天晚上矿泉水是两瓶吗\n答案：是的。", 0.98)
	task.Candidates = []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: combined}}
	if knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(task, knowledgeEvidenceLayerStore, []string{"T1C1"}) {
		t.Fatal("one FAQ covering the full cross-dimension condition must remain complete")
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsSplitCrossDimensionQuantityFacts(t *testing.T) {
	checkinDay := judgeTestHit(1, 101, "入住当天矿泉水数量", "问题：入住当天矿泉水两瓶吗\n答案：是的。", 0.97)
	night := judgeTestHit(2, 102, "晚上矿泉水数量", "问题：晚上矿泉水两瓶吗\n答案：是的。", 0.96)
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "入住当天晚上矿泉水是两瓶吗", Objective: "quantity",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: checkinDay},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: night},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"入住当天矿泉水有两瓶。","criticalValues":["两瓶"]},{"factId":"T1F2","aspect":"quantity","statement":"晚上矿泉水有两瓶。","criticalValues":["两瓶"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse split cross-dimension facts: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("facts covering each condition separately must not prove the conjunction: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseClosesEveryConditionedQuantityFact(t *testing.T) {
	completeHit := judgeTestHit(1, 101, "矿泉水数量", "问题：工作日矿泉水两瓶，周末两瓶吗\n答案：是的。", 0.97)
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "工作日矿泉水两瓶，周末两瓶吗", Objective: "quantity",
		Entities:   []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: completeHit}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"工作日矿泉水有两瓶。","criticalValues":["两瓶"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse conditioned quantity response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		if missing := strictMechanicalMissingKnowledgeEvidenceAspects(task, selection.SupportedFacts); len(missing) != 0 {
			t.Fatalf("a direct selection must be repaired to cover every condition: selection=%#v missing=%#v", selection, missing)
		}
	}

	workdayOnlyHit := judgeTestHit(1, 102, "工作日矿泉水数量", "问题：工作日矿泉水两瓶吗\n答案：是的。", 0.97)
	task.Candidates = []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: workdayOnlyHit}}
	inventedWeekend := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"工作日矿泉水有两瓶，周末矿泉水有两瓶。","criticalValues":["两瓶"]}],"missingAspects":[]}]}]}`
	parsed, err = parseKnowledgeEvidenceJudgeResponse(inventedWeekend, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse incomplete candidate response: %v", err)
	}
	selection = parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("a workday-only candidate cannot become direct by inventing a weekend fact: %#v", selection)
	}
}

func TestParseAndApplyKnowledgeEvidenceShortPolarityFAQUsesQuestionSubject(t *testing.T) {
	tests := []struct {
		name              string
		query             string
		subject           string
		answer            string
		modelStatement    string
		expectedStatement string
		unexpected        string
	}{
		{name: "positive capability", query: "可以开发票吗", subject: "发票", answer: "可以。", modelStatement: "可以开发票。", expectedStatement: "可以开发票。"},
		{name: "explicit capability overrides negative question", query: "不能开发票吗", subject: "发票", answer: "可以。", modelStatement: "可以开发票。", expectedStatement: "可以开发票。", unexpected: "\"statement\":\"不能开发票。\""},
		{name: "explicit availability overrides negative question", query: "没有空调吗", subject: "空调", answer: "有的。", modelStatement: "有空调。", expectedStatement: "有空调。", unexpected: "\"statement\":\"没有空调。\""},
		{name: "negative availability", query: "有静音空调房吗", subject: "静音空调房", answer: "没有。", modelStatement: "有静音空调房。", expectedStatement: "没有静音空调房。", unexpected: "\"statement\":\"有静音空调房。\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hit := judgeTestHit(1, 101, tt.subject, "问题："+tt.query+"\n答案："+tt.answer, 0.97)
			task := knowledgeEvidenceJudgeTask{
				TaskID: "T1", Query: tt.query, Objective: "availability",
				Entities:   []knowledgeEvidenceJudgeEntity{{Text: tt.subject, Type: "facility"}},
				Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
			}
			if statement, ok := resolvedKnowledgeEvidenceFAQQuestionStatement(task, tt.query, tt.answer); !ok || statement != tt.expectedStatement {
				t.Fatalf("unexpected resolved FAQ statement: statement=%q ok=%v", statement, ok)
			}
			raw := fmt.Sprintf(`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":%q,"criticalValues":[%q]}],"missingAspects":[]}]}]}`, tt.modelStatement, tt.subject)
			parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatalf("parse short-polarity response: %v", err)
			}
			selection := parsed["T1"][knowledgeEvidenceLayerStore]
			if selection.Decision != knowledgeEvidenceDecisionDirectSingle {
				t.Fatalf("short polarity answer must remain directly usable: %#v", selection)
			}
			found := false
			for _, fact := range selection.SupportedFacts {
				if fact.Statement == tt.expectedStatement {
					found = true
				}
			}
			if !found {
				t.Fatalf("resolved question subject and polarity missing: %#v", selection.SupportedFacts)
			}

			result := &retrievers.KnowledgeRetrieveResult{
				KnowledgeBaseIDs: []int64{1}, RawHits: []rag.RetrieveResult{hit}, Hits: []rag.RetrieveResult{hit},
				ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content,
			}
			batch := &runtimeKnowledgeRetrieveBatch{Questions: []runtimeKnowledgeQuestionResult{{TaskID: "T1", Query: tt.query, Result: result}}}
			batch.Merged = mergeRuntimeKnowledgeQuestionResults([]int64{1}, result.Options, tt.query, batch.Questions)
			applyKnowledgeEvidenceJudgeOutcome(batch, []knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceJudgeOutcome{Applied: true, Selections: parsed})
			if !strings.Contains(result.ContextText, tt.expectedStatement) || (tt.unexpected != "" && strings.Contains(result.ContextText, tt.unexpected)) {
				t.Fatalf("applied fact boundary lost short-answer polarity: %q", result.ContextText)
			}
		})
	}
}

func TestParseAndApplyKnowledgeEvidenceRejectsConditionalPolarityInheritance(t *testing.T) {
	hit := judgeTestHit(1, 101, "发票", "问题：可以开发票吗\n答案：可以，联系门店确认。", 0.97)
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "可以开发票吗", Objective: "availability",
		Entities:   []knowledgeEvidenceJudgeEntity{{Text: "发票", Type: "service"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"可以开发票。","criticalValues":["发票"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse conditional polarity response: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision == knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("conditional confirmation must not become an unconditional direct fact: %#v", selection)
	}

	result := &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: []int64{1}, RawHits: []rag.RetrieveResult{hit}, Hits: []rag.RetrieveResult{hit}, ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content}
	batch := &runtimeKnowledgeRetrieveBatch{Questions: []runtimeKnowledgeQuestionResult{{TaskID: "T1", Query: task.Query, Result: result}}}
	batch.Merged = mergeRuntimeKnowledgeQuestionResults([]int64{1}, result.Options, task.Query, batch.Questions)
	applyKnowledgeEvidenceJudgeOutcome(batch, []knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceJudgeOutcome{Applied: true, Selections: parsed})
	if len(result.EffectiveHits) != 0 || strings.Contains(result.ContextText, "可以开发票") {
		t.Fatalf("apply must not expose an unconditional fact from conditional guidance: %#v", result)
	}
}

func TestParseAndApplyKnowledgeEvidenceRejectsConditionalYesPolarityInheritance(t *testing.T) {
	hit := judgeTestHit(1, 101, "发票", "问题：可以开发票吗\n答案：是的，联系门店确认。", 0.97)
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "可以开发票吗", Objective: "availability",
		Entities:   []knowledgeEvidenceJudgeEntity{{Text: "发票", Type: "service"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"可以开发票。","criticalValues":["发票"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse conditional yes-polarity response: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision == knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("conditional yes confirmation must not become an unconditional direct fact: %#v", selection)
	}

	result := &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: []int64{1}, RawHits: []rag.RetrieveResult{hit}, Hits: []rag.RetrieveResult{hit}, ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content}
	batch := &runtimeKnowledgeRetrieveBatch{Questions: []runtimeKnowledgeQuestionResult{{TaskID: "T1", Query: task.Query, Result: result}}}
	batch.Merged = mergeRuntimeKnowledgeQuestionResults([]int64{1}, result.Options, task.Query, batch.Questions)
	applyKnowledgeEvidenceJudgeOutcome(batch, []knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceJudgeOutcome{Applied: true, Selections: parsed})
	if len(result.EffectiveHits) != 0 || strings.Contains(result.ContextText, "可以开发票") {
		t.Fatalf("apply must not expose an unconditional fact from conditional yes guidance: %#v", result)
	}
}

func TestParseAndApplyKnowledgeEvidenceRejectsContradictedYesPolarityInheritance(t *testing.T) {
	hit := judgeTestHit(1, 101, "矿泉水费用", "问题：矿泉水免费吗\n答案：是的，但不是免费的。", 0.97)
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "矿泉水免费吗", Objective: "price",
		Entities:   []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"矿泉水免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse contradicted yes-polarity response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	foundNegative := false
	for _, fact := range selection.SupportedFacts {
		if strings.Contains(fact.Statement, "矿泉水免费") && !knowledgeEvidenceTextHasNegativeBoundary(fact.Statement) {
			t.Fatalf("a later task-relevant negation must cancel the false affirmative fact: %#v", selection)
		}
		if knowledgeEvidenceTextHasNegativeBoundary(fact.Statement) && strings.Contains(fact.Statement, "免费") {
			foundNegative = true
		}
	}
	if !foundNegative {
		t.Fatalf("the grounded negative answer must remain available after removing the false affirmative fact: %#v", selection)
	}
}

func TestSelectedExistenceFAQAllowsSameSubjectCompanionFacts(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "有没有早餐，几点",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "有没有早餐", "问题：有没有早餐\n答案：有早餐。", 0.95)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "早餐几点", "问题：早餐几点\n答案：早餐时间是7:00-9:30。", 0.93)},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"有早餐。","criticalValues":["早餐"]},{"factId":"T1F2","aspect":"time","statement":"早餐时间是7:00-9:30。","criticalValues":["7:00-9:30"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectCombined || len(selection.SupportedFacts) != 2 {
		t.Fatalf("a time FAQ for the same subject must be allowed to complement the existence FAQ: %#v", selection)
	}

	partialRaw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"partial","selectedCandidateIds":["T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"早餐时间是7:00-9:30。","criticalValues":["7:00-9:30"]}],"missingAspects":["是否提供早餐"]}]}]}`
	parsed, err = parseKnowledgeEvidenceJudgeResponse(partialRaw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse partial response: %v", err)
	}
	selection = parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionPartial || len(selection.SupportedFacts) != 1 || len(selection.MissingAspects) == 0 {
		t.Fatalf("partial same-subject evidence must keep its confirmed fact and missing existence aspect: %#v", selection)
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
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
		t.Fatalf("breakfast task must not select checkout evidence: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseDoesNotTreatAnswerConditionAsCandidateTopic(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "发票怎么开",
		SubIntent: "invoice",
		Objective: "method",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "发票", Type: "document"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "发票申请", "问题：发票怎么开\n答案：退房后可以在小程序申请发票。", 0.95),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"method","statement":"退房后可以在小程序申请发票。","criticalValues":["小程序"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SupportedFacts) != 1 {
		t.Fatalf("an answer condition must not override the FAQ question's invoice topic: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeRuntimeResponsePreservesModelSelectedFacts(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{
		{
			TaskID: "T1", Query: "停车场有充电桩吗", SubIntent: "parking", Objective: "availability",
			Candidates: []knowledgeEvidenceJudgeCandidate{{
				CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 101, "停车场充电桩", "问题：停车场有没有充电桩\n答案：地下车库提供充电桩，进入地下车库后右拐可以找到。", 0.8572),
			}},
		},
		{
			TaskID: "T2", Query: "发票怎么开，多久能下载", SubIntent: "invoice", Objective: "compound_information",
			Candidates: []knowledgeEvidenceJudgeCandidate{{
				CandidateID: "T2C1", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 102, "发票申请", "问题：发票怎么开，多久能下载\n答案：退房后在自由家安心宿小程序申请，申请后1至3个工作日上传。", 0.8828),
			}},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"location","statement":"地下车库提供充电桩，进入地下车库后右拐可以找到。","criticalValues":["充电桩"]}],"missingAspects":[]}]},{"taskId":"T2","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T2C1"],"supportedFacts":[{"factId":"T2F1","aspect":"method","statement":"退房后在自由家安心宿小程序申请发票。","criticalValues":["退房后","自由家安心宿小程序"]},{"factId":"T2F2","aspect":"time","statement":"发票会在申请后1至3个工作日上传。","criticalValues":["1至3个工作日"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeRuntimeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse runtime Judge response: %v", err)
	}
	charging := parsed["T1"][knowledgeEvidenceLayerStore]
	if charging.Decision != knowledgeEvidenceDecisionDirectSingle || charging.DecisionSource != "model" || len(charging.SupportedFacts) != 1 || charging.SupportedFacts[0].Statement != "地下车库提供充电桩，进入地下车库后右拐可以找到。" {
		t.Fatalf("runtime parser must preserve the Judge-selected charging fact: %#v", charging)
	}
	invoice := parsed["T2"][knowledgeEvidenceLayerStore]
	if invoice.Decision != knowledgeEvidenceDecisionDirectSingle || invoice.DecisionSource != "model" || len(invoice.SupportedFacts) != 2 || !strings.Contains(invoice.SupportedFacts[0].Statement, "小程序申请发票") || !strings.Contains(invoice.SupportedFacts[1].Statement, "1至3个工作日") {
		t.Fatalf("runtime parser must preserve the complete Judge-selected invoice facts: %#v", invoice)
	}
}

func TestParseKnowledgeEvidenceJudgeRuntimeResponseAcceptsSelectedPureHandoffWithoutExactQuestionText(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1", Intent: "service_request", Query: "马桶堵了", SubIntent: "toilet_repair", Objective: "action_request",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "马桶故障", "问题：马桶堵住了，怎么办？\n答案：转接", 0.8023),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"method","statement":"需要转接人工处理。","criticalValues":[]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeRuntimeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse runtime Judge response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || selection.DecisionSource != "model" ||
		len(selection.SelectedCandidateIDs) != 1 || selection.SelectedCandidateIDs[0] != "T1C1" ||
		len(selection.SupportedFacts) != 0 || len(selection.MissingAspects) != 0 {
		t.Fatalf("a Judge-selected pure handoff candidate must remain executable: %#v", selection)
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{"T1C1": tasks[0].Candidates[0]}
	if !selectionHasHandoffDirective(selection, knowledgeEvidenceLayerStore, candidates, tasks[0].Query) {
		t.Fatalf("selected pure handoff must reach the existing handoff route: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeRuntimeResponseRejectsNonExactHandoffWithCompetingBodyOutsideBudget(t *testing.T) {
	handoff := knowledgeEvidenceJudgeCandidate{
		CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
		Hit: judgeTestHit(1, 101, "马桶故障", "问题：马桶堵住了，怎么办？\n答案：转接", 0.8023),
	}
	body := knowledgeEvidenceJudgeCandidate{
		CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore,
		Hit: judgeTestHit(1, 102, "马桶处理", "问题：马桶堵了如何处理？\n答案：可以先使用马桶吸处理。", 0.78),
	}
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1", Intent: "service_request", Query: "马桶堵了", SubIntent: "toilet_repair", Objective: "action_request",
		Candidates:    []knowledgeEvidenceJudgeCandidate{handoff},
		RawCandidates: []knowledgeEvidenceJudgeCandidate{handoff, body},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeRuntimeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse runtime Judge response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("a competing same-layer body answer must still block non-exact handoff: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeRuntimeResponseStillRejectsMechanicalProtocolErrors(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "停车场有充电桩吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "停车场充电桩", "问题：停车场有没有充电桩\n答案：地下车库提供充电桩。", 0.9),
		}},
	}
	for name, raw := range map[string]string{
		"unknown candidate":           `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"地下车库提供充电桩。","criticalValues":[]}],"missingAspects":[]}]}]}`,
		"combined with one candidate": `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"地下车库提供充电桩。","criticalValues":[]}],"missingAspects":[]}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			parsed, err := parseKnowledgeEvidenceJudgeRuntimeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatalf("parse runtime Judge response: %v", err)
			}
			if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
				t.Fatalf("mechanically invalid Judge output must fail: %#v", selection)
			}
		})
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsUnrelatedTopicInsideCombinedSelection(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Query:     "早餐几点，供应到什么时候",
		SubIntent: "breakfast",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "早餐开始时间", "问题：早餐几点开始\n答案：早餐7:00开始。", 0.95)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "退房截止时间", "问题：最晚几点退房\n答案：最晚12:00退房。", 0.94)},
		},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"早餐7:00开始。","criticalValues":["7:00"]},{"factId":"T1F2","aspect":"time","statement":"早餐供应到12:00。","criticalValues":["12:00"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
		t.Fatalf("one unrelated explicit topic must invalidate the combined layer: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseAllowsUnknownButGroundedFAQTopic(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:   "T1",
		Query:    "拖鞋没了怎么办",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "一次性用品领取", "问题：拖鞋用完了怎么办\n答案：可以前往洗衣房领取拖鞋。", 0.9),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"method","statement":"可以前往洗衣房领取拖鞋。","criticalValues":["洗衣房"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SupportedFacts) != 1 {
		t.Fatalf("an unclassified but explicitly grounded FAQ must remain under Judge control: %#v", selection)
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

func TestCanonicalizeKnowledgeEvidenceFactsKeepsDifferentSuppliesWithSamePickupMethodSeparate(t *testing.T) {
	facts := canonicalizeKnowledgeEvidenceFacts([]knowledgeEvidenceFact{
		{FactID: "T1F1", Aspect: "method", Statement: "可以前往洗衣房领取。", CriticalValues: []string{"洗衣房"}},
		{FactID: "T1F2", Aspect: "method", Statement: "可以前往洗衣房领取。", CriticalValues: []string{"洗衣房"}},
	})
	if len(facts) != 2 || facts[0].FactID != "T1F1" || facts[1].FactID != "T1F2" {
		t.Fatalf("facts for different supplies must remain independent: %#v", facts)
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
		DecisionSource:       "exact_faq_fallback",
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
	if got.DecisionSource != "exact_faq_fallback" {
		t.Fatalf("reconciliation must preserve the fallback decision source, got %q", got.DecisionSource)
	}
}

func TestApplyKnowledgeEvidenceJudgeOutcomePreservesModelSelectionVerbatim(t *testing.T) {
	hit := judgeTestHit(1, 101, "发票申请", "问题：发票怎么开，多久能下载\n答案：退房后在小程序申请，申请后1至3个工作日上传。", 0.95)
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "发票怎么开，多久能下载",
		SubIntent: "invoice",
		Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         hit,
		}},
	}
	want := knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		DecisionSource:       "model",
		SelectedCandidateIDs: []string{"T1C1"},
		SupportedFacts: []knowledgeEvidenceFact{{
			FactID:         "T1F1",
			Aspect:         "method",
			Statement:      "退房后在小程序申请发票。",
			CriticalValues: []string{"退房后", "小程序"},
		}},
	}
	result := &retrievers.KnowledgeRetrieveResult{
		KnowledgeBaseIDs: []int64{1},
		RawHits:          []rag.RetrieveResult{hit},
		Hits:             []rag.RetrieveResult{hit},
		ContextResults:   []rag.RetrieveResult{hit},
		ContextText:      hit.Content,
	}
	batch := &runtimeKnowledgeRetrieveBatch{
		Questions: []runtimeKnowledgeQuestionResult{{TaskID: "T1", Query: task.Query, Result: result}},
	}
	batch.Merged = mergeRuntimeKnowledgeQuestionResults([]int64{1}, result.Options, task.Query, batch.Questions)
	outcome := knowledgeEvidenceJudgeOutcome{
		Applied: true,
		Selections: map[string]map[string]knowledgeEvidenceLayerSelection{
			"T1": {knowledgeEvidenceLayerStore: want},
		},
		Trace: callbacks.KnowledgeEvidenceJudgeTraceData{SchemaVersion: knowledgeEvidenceJudgeSchemaVersion, Status: "completed"},
	}

	trace := applyKnowledgeEvidenceJudgeOutcome(batch, []knowledgeEvidenceJudgeTask{task}, outcome)
	got := outcome.Selections["T1"][knowledgeEvidenceLayerStore]
	if got.Decision != want.Decision || got.DecisionSource != want.DecisionSource || len(got.SelectedCandidateIDs) != 1 || got.SelectedCandidateIDs[0] != "T1C1" {
		t.Fatalf("model selection metadata changed during apply: %#v", got)
	}
	if len(got.SupportedFacts) != 1 || got.SupportedFacts[0].FactID != "T1F1" || got.SupportedFacts[0].Aspect != "method" || got.SupportedFacts[0].Statement != "退房后在小程序申请发票。" {
		t.Fatalf("model facts must pass through apply verbatim: %#v", got.SupportedFacts)
	}
	if len(got.SupportedFacts[0].CriticalValues) != 2 || got.SupportedFacts[0].CriticalValues[0] != "退房后" || got.SupportedFacts[0].CriticalValues[1] != "小程序" {
		t.Fatalf("model critical values must not be expanded or rewritten: %#v", got.SupportedFacts[0].CriticalValues)
	}
	if len(trace.Tasks) != 1 || trace.Tasks[0].DecisionSource != "model" || trace.Tasks[0].Disposition != runtimeKnowledgeDispositionAnswer || len(result.EffectiveHits) != 1 {
		t.Fatalf("the preserved model selection must remain the applied answer: trace=%#v result=%#v", trace, result)
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
	contextItems := buildKnowledgeEvidenceJudgeSourceContext(messages, "那这4个房型都有办公桌吗", runtimeKnowledgeQuestionResult{
		Query:              "那这4个房型都有办公桌吗",
		RelationToPrevious: "reference_previous",
		ResolutionState:    runtimeIntentResolutionResolvedFromContext,
	})
	if len(contextItems) != 3 {
		t.Fatalf("expected previous customer, previous assistant and current primary only, got %#v", contextItems)
	}
	joined := contextItems[0].Content + contextItems[1].Content + contextItems[2].Content
	if strings.Contains(joined, "早餐") || !strings.Contains(joined, "哪些房间有沙发") || !strings.Contains(joined, "四种房型") {
		t.Fatalf("source context was polluted or incomplete: %#v", contextItems)
	}
	plain := buildKnowledgeEvidenceJudgeSourceContext(messages, "这个房间有矿泉水吗", runtimeKnowledgeQuestionResult{
		Query:              "这个房间有矿泉水吗",
		RelationToPrevious: "independent",
		ResolutionState:    runtimeIntentResolutionClear,
	})
	if len(plain) != 1 || plain[0].Role != "customer_current" {
		t.Fatalf("independent task must not carry adjacent history merely because its wording contains a reference marker: %#v", plain)
	}
	followUp := buildKnowledgeEvidenceJudgeSourceContext(messages, "具体几点开始", runtimeKnowledgeQuestionResult{
		Query:              "早餐具体几点开始",
		RelationToPrevious: "follow_up",
		ResolutionState:    runtimeIntentResolutionClear,
	})
	if len(followUp) != 3 || followUp[0].Role != "customer" || followUp[1].Role != "assistant" || followUp[2].Role != "customer_current" {
		t.Fatalf("relation-driven follow-up must carry the adjacent customer/assistant pair even without marker words: %#v", followUp)
	}
	currentTurnReference := buildKnowledgeEvidenceJudgeSourceContext(messages, "几点", runtimeKnowledgeQuestionResult{
		Query:              "早餐几点",
		RelationToPrevious: "independent",
		ResolutionState:    runtimeIntentResolutionResolvedFromContext,
		SourceRefs:         []string{"U2", "U1"},
	})
	if len(currentTurnReference) != 1 || currentTurnReference[0].Role != "customer_current" {
		t.Fatalf("current-turn URef context is already resolved into the query and must not attach stale history: %#v", currentTurnReference)
	}
	withTrailingCustomer := append(append([]*schema.Message(nil), messages...), schema.UserMessage("我又问了一个新的问题"))
	stale := buildKnowledgeEvidenceJudgeSourceContext(withTrailingCustomer, "那几个房型呢", runtimeKnowledgeQuestionResult{
		Query:              "那几个房型呢",
		RelationToPrevious: "reference_previous",
		ResolutionState:    runtimeIntentResolutionResolvedFromContext,
	})
	if len(stale) != 1 || stale[0].Role != "customer_current" {
		t.Fatalf("non-adjacent AI reply must not be attached across a newer customer message: %#v", stale)
	}
	legacyMessages := []*schema.Message{
		schema.UserMessage("艺林房型有没有办公桌"),
		schema.AssistantMessage("艺林房型有办公桌。", nil),
	}
	legacyReference := buildKnowledgeEvidenceJudgeSourceContext(legacyMessages, "那麦田呢", runtimeKnowledgeQuestionResult{
		OriginalText: "那麦田呢",
		Query:        "麦田房型有没有办公桌",
		Entities:     []callbacks.IntentEntityTraceData{{Text: "麦田", Type: "room_type"}},
	})
	if len(legacyReference) != 3 || legacyReference[0].Role != "customer" || legacyReference[1].Role != "assistant" {
		t.Fatalf("legacy Profile must keep adjacent context when the resolved predicate is grounded there: %#v", legacyReference)
	}
	legacyRewrite := buildKnowledgeEvidenceJudgeSourceContext(messages, "早餐几点", runtimeKnowledgeQuestionResult{
		OriginalText: "早餐几点",
		Query:        "酒店早餐几点开放",
	})
	if len(legacyRewrite) != 1 || legacyRewrite[0].Role != "customer_current" {
		t.Fatalf("legacy Profile wording normalization must not pull unrelated adjacent history: %#v", legacyRewrite)
	}
	sameDomainMessages := []*schema.Message{
		schema.UserMessage("附近有什么吃的"),
		schema.AssistantMessage("附近有小吃和餐馆。", nil),
	}
	completeNewTopic := buildKnowledgeEvidenceJudgeSourceContext(sameDomainMessages, "附近有什么好玩的", runtimeKnowledgeQuestionResult{
		OriginalText: "附近有什么好玩的",
		Query:        "酒店附近有什么好玩的地方",
	})
	if len(completeNewTopic) != 1 || completeNewTopic[0].Role != "customer_current" {
		t.Fatalf("a complete new question must not inherit same-domain history under a legacy Profile: %#v", completeNewTopic)
	}
}

func TestNormalizeKnowledgeEvidenceJudgeConfigKeepsBatchCapacityWithoutRetries(t *testing.T) {
	for _, tc := range []struct {
		timeoutMS      int
		taskCount      int
		candidateCount int
		want           int
	}{{0, 1, 3, 15_000}, {3_000, 1, 3, 11_000}, {60_000, 1, 3, 15_000}, {4_000, 4, 28, 23_000}, {15_000, 8, 28, 28_000}, {60_000, 8, 28, 28_000}, {60_000, 100, 100, 28_000}} {
		config := normalizeKnowledgeEvidenceJudgeConfig(models.AIConfig{
			TimeoutMS:       tc.timeoutMS,
			MaxOutputTokens: 8_192,
			MaxRetryCount:   3,
		}, tc.taskCount, tc.candidateCount)
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
	longBatch := normalizeKnowledgeEvidenceJudgeConfig(models.AIConfig{TimeoutMS: 4_000, MaxOutputTokens: 1_024}, 8, 28)
	if longBatch.TimeoutMS != 28_000 || longBatch.MaxOutputTokens != 2_560 {
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

func TestParseKnowledgeEvidenceJudgeResponseRejectsUnknownCombinedCandidatePerLayer(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "早餐几点",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "门店早餐", "问题：早餐几点\n答案：7:00-9:30。", 0.9)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerGeneral, Hit: judgeTestHit(2, 201, "通用早餐", "问题：早餐几点\n答案：7:00-10:00。", 0.9)},
		},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","UNKNOWN"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"7:00-9:30。","criticalValues":["7:00-9:30"]}],"missingAspects":[]},{"layer":"general","decision":"direct_single","selectedCandidateIds":["T1C2"],"supportedFacts":[{"factId":"T1F2","aspect":"time","statement":"7:00-10:00。","criticalValues":["7:00-10:00"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if got := parsed["T1"][knowledgeEvidenceLayerStore]; got.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(got.SelectedCandidateIDs) != 0 {
		t.Fatalf("unknown combined candidate must invalidate the whole store layer: %#v", got)
	}
	if got := parsed["T1"][knowledgeEvidenceLayerGeneral]; got.Decision != knowledgeEvidenceDecisionDirectSingle || len(got.SelectedCandidateIDs) != 1 {
		t.Fatalf("invalid store layer must not clear the valid general layer: %#v", got)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsMalformedFactsOnInsufficientLayer(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "早餐几点",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "门店早餐", "问题：早餐几点\n答案：7:00-9:30。", 0.9),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"insufficient","selectedCandidateIds":[],"supportedFacts":[{"factId":"","aspect":"time","statement":"7:00-9:30。","criticalValues":["7:00-9:30"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
		t.Fatalf("malformed facts must not be disguised as a clean insufficient decision: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseAcceptsOmittedOrNullEmptyArraysOnInsufficientLayer(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "早餐几点",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "门店早餐", "问题：早餐几点\n答案：7:00-9:30。", 0.9),
		}},
	}}
	tests := map[string]string{
		"omitted": `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"insufficient","selectedCandidateIds":[]}]}]}`,
		"null":    `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"insufficient","selectedCandidateIds":[],"supportedFacts":null,"missingAspects":null}]}]}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
			if err != nil {
				t.Fatalf("parse response: %v", err)
			}
			selection := parsed["T1"][knowledgeEvidenceLayerStore]
			if selection.Decision != knowledgeEvidenceDecisionInsufficient || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
				t.Fatalf("empty insufficient arrays must normalize safely: %#v", selection)
			}
		})
	}
}

func TestParseKnowledgeEvidenceJudgeResponseNormalizesUnknownGroundedAspectToOther(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1",
		Query:  "老板是谁",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "老板信息", "问题：老板是谁\n答案：老板是汤东强。", 0.9),
		}},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"identity","statement":"老板是汤东强。","criticalValues":["汤东强"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SupportedFacts) != 1 || selection.SupportedFacts[0].Aspect != "other" {
		t.Fatalf("grounded unknown aspect should be retained as other: %#v", selection)
	}
}

func TestModelSelectedRepairKeepsCandidateAndRebuildsOnlyItsFacts(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "老板是谁",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "老板信息", "问题：老板是谁\n答案：老板是汤东强。", 0.9)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "附近景点", "问题：附近有什么好玩的\n答案：附近有罍街。", 0.95)},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"identity","statement":"老板是汤东强。","criticalValues":[""]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.DecisionSource != "model_selected_repair" || len(selection.SelectedCandidateIDs) != 1 || selection.SelectedCandidateIDs[0] != "T1C1" {
		t.Fatalf("repair changed the model-selected candidate: %#v", selection)
	}
	if len(selection.SupportedFacts) == 0 || strings.Contains(selection.SupportedFacts[0].Statement, "罍街") {
		t.Fatalf("repair used facts outside the selected FAQ: %#v", selection.SupportedFacts)
	}
}

func TestModelSelectedRepairRejectsWrongSubjectAfterMalformedFactNormalization(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:   "T1",
		Query:    "麦田房型有沙发吗",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "麦田", Type: "room_type"}, {Text: "沙发", Type: "facility"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "麦田办公桌", "问题：麦田房型有办公桌吗\n答案：麦田房型有办公桌。", 0.96),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"麦田房型有办公桌。","criticalValues":["麦田","办公桌"]},{"factId":"T1F1","aspect":"existence","statement":"麦田房型有办公桌。","criticalValues":["麦田","办公桌"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid || len(selection.SelectedCandidateIDs) != 0 || len(selection.SupportedFacts) != 0 {
		t.Fatalf("malformed model facts must not let an unrelated selected FAQ bypass subject validation: %#v", selection)
	}
}

func TestStrictExactFAQFallbackIgnoresScoreAndUsesExplicitAlias(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "老板叫啥", Candidates: []knowledgeEvidenceJudgeCandidate{{
		CandidateID: "T1C1",
		Layer:       knowledgeEvidenceLayerStore,
		Hit:         judgeTestHit(1, 101, "老板信息", "问题：老板是谁\n答案：老板是汤东强。\n相似问法：老板叫啥、酒店老板是谁", 0.01),
	}}}
	selections := map[string]map[string]knowledgeEvidenceLayerSelection{"T1": {
		knowledgeEvidenceLayerStore: {Decision: knowledgeEvidenceDecisionTimeout},
	}}

	if repaired := repairExactFAQFallbackSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 1 {
		t.Fatalf("explicit exact alias should recover without using score: %#v", selections)
	}
	selection := selections["T1"][knowledgeEvidenceLayerStore]
	if selection.DecisionSource != "exact_faq_fallback" || len(selection.SelectedCandidateIDs) != 1 {
		t.Fatalf("unexpected exact FAQ recovery: %#v", selection)
	}

	task.Query = "老板大概是谁呀"
	selections["T1"][knowledgeEvidenceLayerStore] = knowledgeEvidenceLayerSelection{Decision: knowledgeEvidenceDecisionTimeout}
	if repaired := repairExactFAQFallbackSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("non-exact semantic similarity must not trigger fallback: %#v", selections)
	}
}

func TestStrictExactFAQFallbackRejectsIncompleteCompoundAnswer(t *testing.T) {
	hit := judgeTestHit(
		1,
		101,
		"外卖机器人",
		"问题：你们有外卖机器人吗，能送到房间吗\n答案：有外卖机器人的。",
		0.99,
	)
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Intent:    "hotel_info",
		Query:     "你们有外卖机器人吗，能送到房间吗",
		Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         hit,
		}},
	}

	if selection, ok := strictExactKnowledgeEvidenceFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("an exact FAQ that confirms only existence must not become a complete existence-and-scope answer: %#v", selection)
	}

	selection := normalizeParsedKnowledgeEvidenceLayerSelection(
		"T1",
		knowledgeEvidenceLayerStore,
		knowledgeEvidenceJudgeResponseLayer{
			Layer:                knowledgeEvidenceLayerStore,
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			SelectedCandidateIDs: []string{"T1C1"},
			SupportedFacts: []knowledgeEvidenceFact{{
				FactID: "T1F1", Aspect: "existence", Statement: "门店有外卖机器人。",
			}},
		},
		map[string]struct{}{"T1C1": {}},
		task,
	)
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("model-selected incomplete evidence must be rejected for protocol retry: %#v", selection)
	}
}

func TestStrictExactFAQFallbackRequiresEveryRequestedRoomType(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "合柴和艺林房型都有沙发吗",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "合柴房型", Type: "room_type"},
			{Text: "艺林房型", Type: "room_type"},
			{Text: "沙发", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "房型沙发", "问题：合柴和艺林房型都有沙发吗\n答案：合柴房型有沙发。", 0.95),
		}},
	}
	selections := failedKnowledgeEvidenceLayerSelections([]knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceDecisionTimeout)
	if repaired := repairExactFAQFallbackSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("an exact FAQ that answers only one requested room type must not recover, got %#v", selections)
	}
	if selection := selections["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionTimeout {
		t.Fatalf("incomplete multi-room FAQ must preserve the original Judge failure: %#v", selection)
	}
}

func TestStrictExactFAQFallbackRejectsFabricatedRoomFacilityCartesianProduct(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "合柴和艺林房型都有沙发吗",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "合柴房型", Type: "room_type"},
			{Text: "艺林房型", Type: "room_type"},
			{Text: "沙发", Type: "facility"},
			{Text: "办公桌", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "房型设施", "问题：合柴房型有沙发、艺林房型有办公桌吗\n答案：是的。\n相似问法：合柴和艺林房型都有沙发吗", 0.95),
		}},
	}
	selections := failedKnowledgeEvidenceLayerSelections([]knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceDecisionTimeout)
	if repaired := repairExactFAQFallbackSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("mixed room/facility pairs must not become a Cartesian product: %#v", selections)
	}
}

func TestStrictExactFAQFallbackAllowsSeveralRoomsSharingOneFacility(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "合柴和艺林房型都有沙发吗",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "合柴房型", Type: "room_type"},
			{Text: "艺林房型", Type: "room_type"},
			{Text: "沙发", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "房型沙发", "问题：合柴和艺林房型都有沙发吗\n答案：是的。", 0.95),
		}},
	}
	selections := failedKnowledgeEvidenceLayerSelections([]knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceDecisionTimeout)
	if repaired := repairExactFAQFallbackSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 1 {
		t.Fatalf("one shared facility may cover every explicitly named room: %#v", selections)
	}
}

func TestStrictExactFAQFallbackRejectsUncertainRoomCoverage(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "合柴和艺林房型都有沙发吗",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "合柴房型", Type: "room_type"},
			{Text: "艺林房型", Type: "room_type"},
			{Text: "沙发", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "房型沙发", "问题：合柴和艺林房型都有沙发吗\n答案：合柴房型有沙发，艺林房型是否有沙发暂不确定。", 0.95),
		}},
	}
	selections := failedKnowledgeEvidenceLayerSelections([]knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceDecisionTimeout)
	if repaired := repairExactFAQFallbackSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("an uncertain room fact must not count as complete coverage: %#v", selections)
	}
}

func TestStrictExactFAQFallbackRequiresEveryRequestedSubjectAspectPair(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水和枕头分别有几个、是否免费",
		Objective: "compound_information",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "矿泉水", Type: "supply"},
			{Text: "枕头", Type: "supply"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "用品", "问题：矿泉水和枕头分别有几个、是否免费\n答案：矿泉水免费，枕头有两个。", 0.95),
		}},
	}
	selections := failedKnowledgeEvidenceLayerSelections([]knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceDecisionTimeout)
	if repaired := repairExactFAQFallbackSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("global aspect coverage must not hide missing per-supply facts: %#v", selections)
	}
}

func TestStrictExactFAQFallbackAcceptsCompleteSubjectAspectMatrix(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水和枕头分别有几个、是否免费",
		Objective: "compound_information",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "矿泉水", Type: "supply"},
			{Text: "枕头", Type: "supply"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "用品", "问题：矿泉水和枕头分别有几个、是否免费\n答案：矿泉水有两瓶且免费，枕头有两个且免费。", 0.95),
		}},
	}
	selections := failedKnowledgeEvidenceLayerSelections([]knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceDecisionTimeout)
	if repaired := repairExactFAQFallbackSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 1 {
		t.Fatalf("a complete per-supply fact matrix should recover: %#v", selections)
	}
}

func TestStrictExactFAQFallbackDoesNotCrossIndependentSubjectAspects(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水有几瓶，枕头免费吗",
		Objective: "compound_information",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "矿泉水", Type: "supply"},
			{Text: "枕头", Type: "supply"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "用品", "问题：矿泉水有几瓶，枕头免费吗\n答案：矿泉水有两瓶，枕头免费。", 0.95),
		}},
	}
	selections := failedKnowledgeEvidenceLayerSelections([]knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceDecisionTimeout)
	if repaired := repairExactFAQFallbackSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 1 {
		t.Fatalf("independent subject/aspect clauses must not require a full Cartesian matrix: %#v", selections)
	}
}

func TestStrictExactHandoffFallbackRejectsCompetingCompleteStoreAnswer(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Intent: "service_request", Query: "马桶堵了怎么办", Objective: "action_request",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "马桶", Type: "facility"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "转接", "问题：马桶堵了怎么办\n答案：转接", 0.99)},
		},
		RawCandidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "转接", "问题：马桶堵了怎么办\n答案：转接", 0.99)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "处理", "问题：马桶堵住怎么办\n答案：可以先使用马桶吸处理。", 0.91)},
		},
	}
	selections := failedKnowledgeEvidenceLayerSelections([]knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceDecisionTimeout)
	if repaired := repairExactFAQFallbackSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("exact handoff must not swallow a competing complete store answer: %#v", selections)
	}
}

func TestStrictExactHandoffFallbackRejectsReviewWorthySemanticBodyOutsideJudge(t *testing.T) {
	tests := []struct {
		name         string
		layer        string
		query        string
		bodyQuestion string
		bodyAnswer   string
		bodyScore    float32
	}{
		{name: "store owner identity", layer: knowledgeEvidenceLayerStore, query: "老板是谁", bodyQuestion: "董事长是谁", bodyAnswer: "董事长是汤东强。", bodyScore: 0.848863},
		{name: "store nearby attractions", layer: knowledgeEvidenceLayerStore, query: "附近有什么好玩的", bodyQuestion: "周边有哪些游玩地点", bodyAnswer: "可以去罍街和合柴1972游玩。", bodyScore: 0.894273},
		{name: "general owner identity", layer: knowledgeEvidenceLayerGeneral, query: "老板是谁", bodyQuestion: "董事长是谁", bodyAnswer: "董事长是汤东强。", bodyScore: 0.848863},
		{name: "general nearby attractions", layer: knowledgeEvidenceLayerGeneral, query: "附近有什么好玩的", bodyQuestion: "周边有哪些游玩地点", bodyAnswer: "可以去罍街和合柴1972游玩。", bodyScore: 0.894273},
		{name: "store restroom alias", layer: knowledgeEvidenceLayerStore, query: "房间有洗手间吗", bodyQuestion: "客房内有卫生间吗", bodyAnswer: "客房内有卫生间。", bodyScore: 0.86},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handoff := knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C1",
				Layer:       test.layer,
				Hit:         judgeTestHit(1, 101, "转接规则", "问题："+test.query+"\n答案：转接", 0.99),
			}
			body := knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C2",
				Layer:       test.layer,
				Hit:         judgeTestHit(1, 102, "正文答案", "问题："+test.bodyQuestion+"\n答案："+test.bodyAnswer, test.bodyScore),
			}
			task := knowledgeEvidenceJudgeTask{
				TaskID:        "T1",
				Query:         test.query,
				Candidates:    []knowledgeEvidenceJudgeCandidate{handoff},
				RawCandidates: []knowledgeEvidenceJudgeCandidate{handoff, body},
			}

			if selection, ok := strictExactKnowledgeEvidenceFAQSelection(task, test.layer); ok {
				t.Fatalf("budget-excluded semantic body must block deterministic handoff: %#v", selection)
			}
			selections := failedKnowledgeEvidenceLayerSelections([]knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceDecisionTimeout)
			if repaired := repairExactFAQFallbackSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
				t.Fatalf("fallback must wait for Judge instead of treating score as an answer: %#v", selections)
			}
		})
	}
}

func TestReviewWorthyBodyUsesSemanticShapeWithoutIgnoringExplicitConflicts(t *testing.T) {
	tests := []struct {
		name       string
		task       knowledgeEvidenceJudgeTask
		question   string
		answer     string
		wantReview bool
	}{
		{name: "owner alias", task: knowledgeEvidenceJudgeTask{Query: "老板是谁"}, question: "董事长是谁", answer: "董事长是汤东强。", wantReview: true},
		{name: "nearby alias", task: knowledgeEvidenceJudgeTask{Query: "附近有什么好玩的"}, question: "周边有哪些游玩地点", answer: "可以去罍街和合柴1972游玩。", wantReview: true},
		{name: "restroom alias", task: knowledgeEvidenceJudgeTask{Query: "房间有洗手间吗"}, question: "客房内有卫生间吗", answer: "客房内有卫生间。", wantReview: true},
		{name: "unrelated generic service", task: knowledgeEvidenceJudgeTask{Query: "老板是谁"}, question: "酒店还有哪些服务", answer: "酒店还提供其他基础服务。", wantReview: false},
		{name: "different method domain", task: knowledgeEvidenceJudgeTask{Query: "房间门锁打不开怎么办"}, question: "房间空调打不开怎么办", answer: "请检查空调面板。", wantReview: false},
		{name: "different phone subject", task: knowledgeEvidenceJudgeTask{Query: "门店电话是多少"}, question: "老板电话是多少", answer: "老板电话是18156022128。", wantReview: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C2",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 102, "正文", "问题："+test.question+"\n答案："+test.answer, 0.9),
			}
			got := knowledgeEvidenceJudgeReviewWorthyBodyPeer(test.task, candidate, test.task.Query, "转接")
			if got != test.wantReview {
				t.Fatalf("review=%v want=%v taskSig=%q candidateSig=%q taskShape=%q candidateShape=%q explicitConflict=%v",
					got,
					test.wantReview,
					knowledgeEvidenceConflictQuestionSignature(test.task.Query),
					knowledgeEvidenceConflictQuestionSignature(test.question),
					knowledgeEvidenceReviewQuestionShape(test.task.Query),
					knowledgeEvidenceReviewQuestionShape(test.question),
					knowledgeEvidenceCandidateHasExplicitTaskConflict(test.task, candidate, test.question, test.answer),
				)
			}
		})
	}
}

func TestModelSelectedServiceLocationDoesNotBecomeApplicabilityScope(t *testing.T) {
	serviceTask := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Intent: "service_request", Query: "房间拖鞋没了怎么办", Objective: "action_request",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
	}
	serviceCandidate := knowledgeEvidenceJudgeCandidate{
		CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
		Hit: judgeTestHit(1, 101, "拖鞋领取", "问题：房间拖鞋没了怎么办\n答案：可以去大堂领取拖鞋。", 0.9),
	}
	serviceTask.Candidates = []knowledgeEvidenceJudgeCandidate{serviceCandidate}
	serviceTask.RawCandidates = append([]knowledgeEvidenceJudgeCandidate(nil), serviceTask.Candidates...)
	selection := normalizeParsedKnowledgeEvidenceLayerSelection(
		"T1",
		knowledgeEvidenceLayerStore,
		knowledgeEvidenceJudgeResponseLayer{
			Layer:                knowledgeEvidenceLayerStore,
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			SelectedCandidateIDs: []string{"T1C1"},
			SupportedFacts: []knowledgeEvidenceFact{{
				FactID: "T1F1", Aspect: "method", Statement: "可以去大堂领取拖鞋。", CriticalValues: []string{"大堂"},
			}},
		},
		map[string]struct{}{"T1C1": {}},
		serviceTask,
	)
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("a pickup destination must not be treated as the FAQ applicability scope: %#v", selection)
	}
	serviceCandidate.Hit.Title = "大堂用品领取"
	serviceTask.Candidates = []knowledgeEvidenceJudgeCandidate{serviceCandidate}
	serviceTask.RawCandidates = append([]knowledgeEvidenceJudgeCandidate(nil), serviceTask.Candidates...)
	selection = normalizeParsedKnowledgeEvidenceLayerSelection(
		"T1",
		knowledgeEvidenceLayerStore,
		knowledgeEvidenceJudgeResponseLayer{
			Layer:                knowledgeEvidenceLayerStore,
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			SelectedCandidateIDs: []string{"T1C1"},
			SupportedFacts: []knowledgeEvidenceFact{{
				FactID: "T1F1", Aspect: "method", Statement: "可以去大堂领取拖鞋。", CriticalValues: []string{"大堂"},
			}},
		},
		map[string]struct{}{"T1C1": {}},
		serviceTask,
	)
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("a destination named in the FAQ title must not become the service applicability scope: %#v", selection)
	}

	configTask := knowledgeEvidenceJudgeTask{TaskID: "T2", Query: "客房wifi账号密码是什么"}
	configCandidate := knowledgeEvidenceJudgeCandidate{
		CandidateID: "T2C1", Layer: knowledgeEvidenceLayerStore,
		Hit: judgeTestHit(1, 102, "大堂wifi", "问题：大堂wifi账号密码是什么\n答案：大堂wifi账号是Lobby，密码是12345678。", 0.9),
	}
	if !knowledgeEvidenceCandidateHasExplicitTaskConflict(configTask, configCandidate, "大堂wifi账号密码是什么", "大堂wifi账号是Lobby，密码是12345678。") {
		t.Fatal("a real room-versus-lobby configuration mismatch must still be rejected")
	}
}

func TestMalformedPartialRepairFiltersUnrequestedFacts(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "早餐几点、多少钱、在几楼", Objective: "compound_information",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "早餐", "问题：早餐几点、多少钱、在几楼\n答案：早餐时间为7:00-9:30，价格20元；老板是汤东强。", 0.95),
		}},
	}
	selection := normalizeParsedKnowledgeEvidenceLayerSelection(
		"T1",
		knowledgeEvidenceLayerStore,
		knowledgeEvidenceJudgeResponseLayer{
			Layer:                knowledgeEvidenceLayerStore,
			Decision:             knowledgeEvidenceDecisionPartial,
			SelectedCandidateIDs: []string{"T1C1"},
			SupportedFacts: []knowledgeEvidenceFact{{
				FactID: "bad", Aspect: "time", Statement: "早餐时间为7:00-9:30。", CriticalValues: []string{""},
			}},
			MissingAspects: []string{"位置"},
		},
		map[string]struct{}{"T1C1": {}},
		task,
	)
	if selection.Decision != knowledgeEvidenceDecisionPartial {
		t.Fatalf("expected repaired partial selection, got %#v", selection)
	}
	for _, fact := range selection.SupportedFacts {
		if strings.Contains(fact.Statement, "汤东强") {
			t.Fatalf("partial repair leaked an unrequested owner fact: %#v", selection.SupportedFacts)
		}
	}
}

func TestKnowledgeEvidenceTaskFailureDecisionSourceUsesSameLayer(t *testing.T) {
	decision, source := knowledgeEvidenceTaskFailureDecisionAndSource(map[string]knowledgeEvidenceLayerSelection{
		knowledgeEvidenceLayerStore:   {Decision: knowledgeEvidenceDecisionInsufficient, DecisionSource: "model"},
		knowledgeEvidenceLayerGeneral: {Decision: knowledgeEvidenceDecisionTimeout, DecisionSource: knowledgeEvidenceDecisionTimeout},
	})
	if decision != knowledgeEvidenceDecisionTimeout || source != knowledgeEvidenceDecisionTimeout {
		t.Fatalf("task failure trace must report a decision/source pair from the same layer: decision=%q source=%q", decision, source)
	}
}

func TestPartialKnowledgeEvidenceAddsMechanicallyMissingAspects(t *testing.T) {
	hit := judgeTestHit(1, 101, "外卖机器人", "问题：你们有外卖机器人吗\n答案：有外卖机器人的。", 0.99)
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Intent: "hotel_info", Query: "外卖机器人能送到房间吗", Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
	}
	selection := normalizeParsedKnowledgeEvidenceLayerSelection(
		"T1",
		knowledgeEvidenceLayerStore,
		knowledgeEvidenceJudgeResponseLayer{
			Layer:                knowledgeEvidenceLayerStore,
			Decision:             knowledgeEvidenceDecisionPartial,
			SelectedCandidateIDs: []string{"T1C1"},
			SupportedFacts: []knowledgeEvidenceFact{{
				FactID: "T1F1", Aspect: "existence", Statement: "门店有外卖机器人。",
			}},
			MissingAspects: []string{"具体服务条件"},
		},
		map[string]struct{}{"T1C1": {}},
		task,
	)
	if selection.Decision != knowledgeEvidenceDecisionPartial {
		t.Fatalf("grounded partial evidence should remain usable: %#v", selection)
	}
	if !knowledgeEvidenceMissingAspectCovered(selection.MissingAspects, "适用范围") {
		t.Fatalf("partial evidence must include the mechanically missing delivery scope: %#v", selection.MissingAspects)
	}
}

func TestStrictExactHandoffRejectsSameLayerConflictingBody(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:     "T1",
		Query:      "马桶堵了怎么办",
		Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "转接规则", "问题：马桶堵了怎么办\n答案：转接", 0.99)}},
	}
	task.RawCandidates = append([]knowledgeEvidenceJudgeCandidate(nil), task.Candidates...)
	task.RawCandidates = append(task.RawCandidates, knowledgeEvidenceJudgeCandidate{
		CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "处理说明", "问题：马桶堵了怎么办\n答案：可以先使用马桶吸处理。", 0.8),
	})

	if selection, ok := strictExactKnowledgeEvidenceFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("a conflicting exact body outside the Judge budget must block direct handoff: %#v", selection)
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"T1C1": task.RawCandidates[0],
		"T1C2": task.RawCandidates[1],
	}
	if strictExactKnowledgeEvidenceHandoffSelectionMatches(task.Query, knowledgeEvidenceLayerStore, []string{"T1C1"}, candidates) {
		t.Fatal("model-selected handoff must also respect the same-layer conflict")
	}
}

func TestStrictExactHandoffTreatsEquivalentDirectivesAsOneAnswer(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "马桶堵了怎么办",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "转接规则一", "问题：马桶堵了怎么办\n答案：转接", 0.99)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "转接规则二", "问题：马桶堵了怎么办\n答案：转人工", 0.98)},
		},
	}
	task.RawCandidates = append([]knowledgeEvidenceJudgeCandidate(nil), task.Candidates...)

	selection, ok := strictExactKnowledgeEvidenceFAQSelection(task, knowledgeEvidenceLayerStore)
	if !ok || selection.DecisionSource != "deterministic_handoff" {
		t.Fatalf("equivalent handoff directives should form one exact answer: ok=%v selection=%#v", ok, selection)
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"T1C1": task.Candidates[0],
		"T1C2": task.Candidates[1],
	}
	if !strictExactKnowledgeEvidenceHandoffSelectionMatches(task.Query, knowledgeEvidenceLayerStore, []string{"T1C1"}, candidates) {
		t.Fatal("model-selected equivalent handoff directive should remain valid")
	}
}

func TestModelSelectedExactHandoffRemainsValidWhenJudgeSawCompetingBody(t *testing.T) {
	handoff := knowledgeEvidenceJudgeCandidate{
		CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
		Hit: judgeTestHit(1, 101, "老板转接", "问题：老板是谁\n答案：转接", 0.99),
	}
	body := knowledgeEvidenceJudgeCandidate{
		CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore,
		Hit: judgeTestHit(1, 102, "董事长信息", "问题：董事长是谁\n答案：董事长是汤东强。", 0.848863),
	}
	task := knowledgeEvidenceJudgeTask{
		TaskID:     "T1",
		Query:      "老板是谁",
		Candidates: []knowledgeEvidenceJudgeCandidate{handoff, body},
		RawCandidates: []knowledgeEvidenceJudgeCandidate{
			handoff,
			body,
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerStore, Hit: body.Hit},
		},
	}

	selection := normalizeParsedKnowledgeEvidenceLayerSelection(
		"T1",
		knowledgeEvidenceLayerStore,
		knowledgeEvidenceJudgeResponseLayer{
			Layer:                knowledgeEvidenceLayerStore,
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			SelectedCandidateIDs: []string{"T1C1"},
		},
		map[string]struct{}{"T1C1": {}, "T1C2": {}},
		task,
	)
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SelectedCandidateIDs) != 1 || selection.SelectedCandidateIDs[0] != "T1C1" {
		t.Fatalf("Judge-visible body must not invalidate its explicit exact-handoff choice: %#v", selection)
	}
}

func TestReviewWorthyBodyConflictHonorsMechanicalScopeBoundaries(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		bodyQuestion string
		bodyAnswer   string
	}{
		{name: "different object", query: "老板是谁", bodyQuestion: "门店管家是谁", bodyAnswer: "门店管家是李明。"},
		{name: "different room type", query: "麦田房型有办公桌吗", bodyQuestion: "合柴房型有办公桌吗", bodyAnswer: "合柴房型有办公桌。"},
		{name: "different configuration scope", query: "客房wifi账号密码是什么", bodyQuestion: "大堂wifi账号密码是什么", bodyAnswer: "大堂wifi账号是Lobby，密码是12345678。"},
		{name: "different condition", query: "周末早餐几点开始", bodyQuestion: "工作日早餐几点开始", bodyAnswer: "工作日早餐7:00开始。"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handoff := knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 101, "精确转接", "问题："+test.query+"\n答案：转接", 0.99),
			}
			body := knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 102, "其他范围正文", "问题："+test.bodyQuestion+"\n答案："+test.bodyAnswer, 0.95),
			}
			task := knowledgeEvidenceJudgeTask{
				TaskID:        "T1",
				Query:         test.query,
				Candidates:    []knowledgeEvidenceJudgeCandidate{handoff},
				RawCandidates: []knowledgeEvidenceJudgeCandidate{handoff, body},
			}

			selection, ok := strictExactKnowledgeEvidenceFAQSelection(task, knowledgeEvidenceLayerStore)
			if !ok || selection.DecisionSource != "deterministic_handoff" {
				t.Fatalf("an unrelated scope must not suppress the real exact handoff: ok=%v selection=%#v", ok, selection)
			}
		})
	}
}

func TestStrictExactFactualFallbackRejectsSameLayerConflictingCriticalValuesOutsideBudget(t *testing.T) {
	tests := []struct {
		name             string
		query            string
		exactQuestion    string
		exactAnswer      string
		conflictQuestion string
		conflictAnswer   string
	}{
		{
			name:             "quantity",
			query:            "房间有几瓶矿泉水",
			exactQuestion:    "房间有几瓶矿泉水",
			exactAnswer:      "房间内有两瓶矿泉水。",
			conflictQuestion: "房间有几瓶矿泉水",
			conflictAnswer:   "房间内有四瓶矿泉水。",
		},
		{
			name:             "address",
			query:            "酒店地址在哪里",
			exactQuestion:    "酒店地址在哪里",
			exactAnswer:      "酒店地址是合肥市蜀山区望江西路108号。",
			conflictQuestion: "酒店的具体地址是什么",
			conflictAnswer:   "酒店地址是合肥市蜀山区黄山路88号。",
		},
		{
			name:             "phone",
			query:            "门店电话是多少",
			exactQuestion:    "门店电话是多少",
			exactAnswer:      "门店电话是18256022128。",
			conflictQuestion: "酒店联系电话是多少",
			conflictAnswer:   "门店电话是18156022128。",
		},
		{
			name:             "same time field",
			query:            "早餐几点开始",
			exactQuestion:    "早餐几点开始",
			exactAnswer:      "早餐7:00开始。",
			conflictQuestion: "早餐什么时候开始",
			conflictAnswer:   "早餐8:00开始。",
		},
		{
			name:             "conditional schedule distribution marker",
			query:            "工作日和周末早餐时间分别是多少",
			exactQuestion:    "工作日和周末早餐时间分别是多少",
			exactAnswer:      "工作日早餐7:00开始，周末早餐8:00开始。",
			conflictQuestion: "周末早餐几点开始",
			conflictAnswer:   "周末早餐9:00开始。",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exactCandidate := knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "精确答案", "问题："+test.exactQuestion+"\n答案："+test.exactAnswer, 0.99),
			}
			task := knowledgeEvidenceJudgeTask{
				TaskID:     "T1",
				Query:      test.query,
				Candidates: []knowledgeEvidenceJudgeCandidate{exactCandidate},
				RawCandidates: []knowledgeEvidenceJudgeCandidate{
					exactCandidate,
					{
						CandidateID: "T1C2",
						Layer:       knowledgeEvidenceLayerStore,
						Hit:         judgeTestHit(1, 102, "冲突答案", "问题："+test.conflictQuestion+"\n答案："+test.conflictAnswer, 0.95),
					},
				},
			}

			if selection, ok := strictExactKnowledgeEvidenceFAQSelection(task, knowledgeEvidenceLayerStore); ok {
				t.Fatalf("a conflicting complete answer outside the Judge budget must block factual fallback: %#v", selection)
			}
		})
	}
}

func TestStrictExactFactualFallbackIgnoresUnrelatedSameAspectCandidates(t *testing.T) {
	tests := []struct {
		name              string
		query             string
		exactAnswer       string
		unrelatedQuestion string
		unrelatedAnswer   string
	}{
		{
			name:              "quantity in a different room scope",
			query:             "房间有几瓶矿泉水",
			exactAnswer:       "房间内有两瓶矿泉水。",
			unrelatedQuestion: "会议室有几瓶矿泉水",
			unrelatedAnswer:   "会议室有四瓶矿泉水。",
		},
		{
			name:              "address for a different object",
			query:             "酒店地址在哪里",
			exactAnswer:       "酒店地址是合肥市蜀山区望江西路108号。",
			unrelatedQuestion: "停车场入口地址在哪里",
			unrelatedAnswer:   "停车场入口地址是合肥市蜀山区黄山路88号。",
		},
		{
			name:              "phone for a different person",
			query:             "门店电话是多少",
			exactAnswer:       "门店电话是18256022128。",
			unrelatedQuestion: "老板电话是多少",
			unrelatedAnswer:   "老板电话是18156022128。",
		},
		{
			name:              "quantity for a different room type",
			query:             "麦田房型有几瓶矿泉水",
			exactAnswer:       "麦田房型有两瓶矿泉水。",
			unrelatedQuestion: "合柴房型有几瓶矿泉水",
			unrelatedAnswer:   "合柴房型有四瓶矿泉水。",
		},
		{
			name:              "time under a different day condition",
			query:             "周末早餐几点开始",
			exactAnswer:       "周末早餐7:00开始。",
			unrelatedQuestion: "工作日早餐几点开始",
			unrelatedAnswer:   "工作日早餐6:00开始。",
		},
		{
			name:              "different time field",
			query:             "早餐几点开始",
			exactAnswer:       "早餐7:00开始。",
			unrelatedQuestion: "早餐几点结束",
			unrelatedAnswer:   "早餐9:30结束。",
		},
		{
			name:              "physical address and delivery address",
			query:             "酒店地址在哪里",
			exactAnswer:       "酒店地址是合肥市蜀山区望江西路108号。",
			unrelatedQuestion: "外卖地址怎么填",
			unrelatedAnswer:   "外卖地址填写丽斯未来酒店合肥南七店加对应楼层房间号。",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exactCandidate := knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "精确答案", "问题："+test.query+"\n答案："+test.exactAnswer, 0.99),
			}
			task := knowledgeEvidenceJudgeTask{
				TaskID:     "T1",
				Query:      test.query,
				Candidates: []knowledgeEvidenceJudgeCandidate{exactCandidate},
				RawCandidates: []knowledgeEvidenceJudgeCandidate{
					exactCandidate,
					{
						CandidateID: "T1C2",
						Layer:       knowledgeEvidenceLayerStore,
						Hit:         judgeTestHit(1, 102, "不相关答案", "问题："+test.unrelatedQuestion+"\n答案："+test.unrelatedAnswer, 0.95),
					},
				},
			}

			selection, ok := strictExactKnowledgeEvidenceFAQSelection(task, knowledgeEvidenceLayerStore)
			if !ok || selection.DecisionSource != "exact_faq_fallback" {
				t.Fatalf("an unrelated same-aspect candidate must not block exact fallback: ok=%v selection=%#v", ok, selection)
			}
		})
	}
}

func TestSelectedHandoffCandidateRequiresStrictExactSingleDecision(t *testing.T) {
	handoffCandidate := knowledgeEvidenceJudgeCandidate{
		CandidateID: "T1C1",
		Layer:       knowledgeEvidenceLayerStore,
		Hit:         judgeTestHit(1, 101, "马桶故障", "问题：马桶堵了怎么办\n答案：转接", 0.99),
	}
	expectedCandidates := map[string]struct{}{"T1C1": {}}

	t.Run("non exact direct single", func(t *testing.T) {
		task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "马桶有点问题", Candidates: []knowledgeEvidenceJudgeCandidate{handoffCandidate}}
		selection := normalizeParsedKnowledgeEvidenceLayerSelection("T1", knowledgeEvidenceLayerStore, knowledgeEvidenceJudgeResponseLayer{
			Layer:                knowledgeEvidenceLayerStore,
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			SelectedCandidateIDs: []string{"T1C1"},
			SupportedFacts:       []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "other", Statement: "转接"}},
		}, expectedCandidates, task)
		if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
			t.Fatalf("a non-exact transfer FAQ must not become ordinary answer evidence: %#v", selection)
		}
	})

	t.Run("partial", func(t *testing.T) {
		task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "马桶堵了怎么办", Candidates: []knowledgeEvidenceJudgeCandidate{handoffCandidate}}
		selection := normalizeParsedKnowledgeEvidenceLayerSelection("T1", knowledgeEvidenceLayerStore, knowledgeEvidenceJudgeResponseLayer{
			Layer:                knowledgeEvidenceLayerStore,
			Decision:             knowledgeEvidenceDecisionPartial,
			SelectedCandidateIDs: []string{"T1C1"},
			SupportedFacts:       []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "other", Statement: "转接"}},
			MissingAspects:       []string{"处理结果"},
		}, expectedCandidates, task)
		if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
			t.Fatalf("a transfer candidate must never participate in partial evidence: %#v", selection)
		}
	})

	t.Run("direct combined", func(t *testing.T) {
		factCandidate := knowledgeEvidenceJudgeCandidate{
			CandidateID: "T1C2",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 102, "马桶处理", "问题：马桶堵了怎么办\n答案：可以先使用马桶吸。", 0.80),
		}
		task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "马桶堵了怎么办", Candidates: []knowledgeEvidenceJudgeCandidate{handoffCandidate, factCandidate}}
		selection := normalizeParsedKnowledgeEvidenceLayerSelection("T1", knowledgeEvidenceLayerStore, knowledgeEvidenceJudgeResponseLayer{
			Layer:                knowledgeEvidenceLayerStore,
			Decision:             knowledgeEvidenceDecisionDirectCombined,
			SelectedCandidateIDs: []string{"T1C1", "T1C2"},
			SupportedFacts:       []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "method", Statement: "可以先使用马桶吸。"}},
		}, map[string]struct{}{"T1C1": {}, "T1C2": {}}, task)
		if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
			t.Fatalf("a transfer candidate must never participate in combined evidence: %#v", selection)
		}
	})

	t.Run("strict exact direct single", func(t *testing.T) {
		task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "马桶堵了怎么办", Candidates: []knowledgeEvidenceJudgeCandidate{handoffCandidate}}
		selection := normalizeParsedKnowledgeEvidenceLayerSelection("T1", knowledgeEvidenceLayerStore, knowledgeEvidenceJudgeResponseLayer{
			Layer:                knowledgeEvidenceLayerStore,
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			SelectedCandidateIDs: []string{"T1C1"},
		}, expectedCandidates, task)
		if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SelectedCandidateIDs) != 1 || len(selection.SupportedFacts) != 0 {
			t.Fatalf("a strict exact single transfer directive must remain valid: %#v", selection)
		}
	})

	t.Run("strict exact availability handoff", func(t *testing.T) {
		candidate := knowledgeEvidenceJudgeCandidate{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "静音空调房转接", "问题：你们有静音空调房吗\n答案：转接", 0.99),
		}
		task := knowledgeEvidenceJudgeTask{
			TaskID:    "T1",
			Intent:    "hotel_info",
			Query:     "你们有静音空调房吗",
			Objective: "availability",
			Entities:  []knowledgeEvidenceJudgeEntity{{Text: "静音空调房", Type: "facility"}},
			Candidates: []knowledgeEvidenceJudgeCandidate{
				candidate,
			},
		}
		selection := normalizeParsedKnowledgeEvidenceLayerSelection("T1", knowledgeEvidenceLayerStore, knowledgeEvidenceJudgeResponseLayer{
			Layer:                knowledgeEvidenceLayerStore,
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			SelectedCandidateIDs: []string{"T1C1"},
		}, map[string]struct{}{"T1C1": {}}, task)
		if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SelectedCandidateIDs) != 1 || len(selection.SupportedFacts) != 0 {
			t.Fatalf("an exact availability handoff must remain executable: %#v", selection)
		}
	})
}

func TestJudgeProtocolFailureKeepsRawHitsAndDoesNotRequestHandoff(t *testing.T) {
	hit := judgeTestHit(1, 101, "门店早餐", "问题：早餐供应时间\n答案：7:00-9:30。", 0.9)
	result := &retrievers.KnowledgeRetrieveResult{
		KnowledgeBaseIDs: []int64{1},
		RawHits:          []rag.RetrieveResult{hit},
		Hits:             []rag.RetrieveResult{hit},
		ContextResults:   []rag.RetrieveResult{hit},
		ContextText:      hit.Content,
	}
	batch := &runtimeKnowledgeRetrieveBatch{
		Questions: []runtimeKnowledgeQuestionResult{{TaskID: "T1", Query: "早餐几点", Result: result}},
		Merged:    mergeRuntimeKnowledgeQuestionResults([]int64{1}, result.Options, "早餐几点", []runtimeKnowledgeQuestionResult{{TaskID: "T1", Query: "早餐几点", Result: result}}),
	}
	tasks := []knowledgeEvidenceJudgeTask{{TaskID: "T1", Query: "早餐几点", Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}}}}
	outcome := knowledgeEvidenceJudgeOutcome{Applied: true, Selections: failedKnowledgeEvidenceLayerSelections(tasks, knowledgeEvidenceDecisionTimeout)}

	trace := applyKnowledgeEvidenceJudgeOutcome(batch, tasks, outcome)
	if len(batch.Questions[0].Result.RawHits) != 1 || batch.Questions[0].Result.RawHits[0].SourceRecordID != hit.SourceRecordID {
		t.Fatalf("judge failure mutated raw retrieval: %#v", batch.Questions[0].Result.RawHits)
	}
	if len(batch.Questions[0].Result.EffectiveHits) != 0 || len(batch.Questions[0].Result.Hits) != 0 {
		t.Fatalf("failed judge must not expose unselected hits: %#v", batch.Questions[0].Result)
	}
	if len(trace.Tasks) != 1 || trace.Tasks[0].Disposition != runtimeKnowledgeDispositionJudgeProtocolRetry {
		t.Fatalf("judge failure must retain an explicit retry disposition: %#v", trace)
	}
	dispositions := runtimeKnowledgeQuestionDispositions(batch)
	if len(dispositions) != 1 || !dispositions[0].NeedsRetry || dispositions[0].NeedsHandoff {
		t.Fatalf("judge protocol failure must not become a handoff: %#v", dispositions)
	}
}

func TestStorePartialWithGeneralProtocolFailureIsIsolatedWithoutHandoff(t *testing.T) {
	storeHit := judgeTestHit(1, 101, "外卖机器人", "问题：有外卖机器人吗\n答案：有外卖机器人的。", 0.93)
	generalHit := judgeTestHit(2, 201, "机器人配送范围", "问题：外卖机器人能送到哪里\n答案：机器人可以送到房门口。", 0.88)
	result := &retrievers.KnowledgeRetrieveResult{
		KnowledgeBaseIDs: []int64{1, 2},
		RawHits:          []rag.RetrieveResult{storeHit, generalHit},
		Hits:             []rag.RetrieveResult{storeHit},
		ContextResults:   []rag.RetrieveResult{storeHit},
		ContextText:      storeHit.Content,
	}
	batch := &runtimeKnowledgeRetrieveBatch{
		Questions: []runtimeKnowledgeQuestionResult{{TaskID: "T1", Query: "外卖机器人能送到房间吗", Result: result}},
	}
	batch.Merged = mergeRuntimeKnowledgeQuestionResults([]int64{1, 2}, result.Options, "外卖机器人能送到房间吗", batch.Questions)
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID: "T1", Query: "外卖机器人能送到房间吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: storeHit},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerGeneral, Hit: generalHit},
		},
	}}
	outcome := knowledgeEvidenceJudgeOutcome{
		Applied: true,
		Selections: map[string]map[string]knowledgeEvidenceLayerSelection{"T1": {
			knowledgeEvidenceLayerStore: {
				Decision: knowledgeEvidenceDecisionPartial, DecisionSource: "model", SelectedCandidateIDs: []string{"T1C1"},
				SupportedFacts: []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "existence", Statement: "门店有外卖机器人。"}},
				MissingAspects: []string{"适用范围"},
			},
			knowledgeEvidenceLayerGeneral: {Decision: knowledgeEvidenceDecisionTimeout, DecisionSource: knowledgeEvidenceDecisionTimeout},
		}},
		Trace: callbacks.KnowledgeEvidenceJudgeTraceData{SchemaVersion: knowledgeEvidenceJudgeSchemaVersion, Status: knowledgeEvidenceDecisionTimeout},
	}

	trace := applyKnowledgeEvidenceJudgeOutcome(batch, tasks, outcome)
	if len(result.EffectiveHits) != 1 || len(result.Hits) != 1 || !strings.Contains(result.ContextText, "有外卖机器人") {
		t.Fatalf("a valid store partial must remain customer-visible despite a failed general layer: %#v", result)
	}
	if len(trace.Tasks) != 1 || trace.Tasks[0].Disposition != runtimeKnowledgeDispositionAnswerThenHandoff || trace.Tasks[0].SelectedLayer != knowledgeEvidenceLayerStore {
		t.Fatalf("the valid partial answer must win without a Judge protocol retry: %#v", trace)
	}
	dispositions := runtimeKnowledgeQuestionDispositions(batch)
	if len(dispositions) != 1 || dispositions[0].NeedsRetry || !dispositions[0].NeedsHandoff || !dispositions[0].HasAnswer {
		t.Fatalf("valid partial evidence must answer confirmed facts and defer only the missing part: %#v", dispositions)
	}
}

func TestModelSelectedEvidenceAllowsSemanticEntityAliases(t *testing.T) {
	tests := []struct {
		name      string
		task      knowledgeEvidenceJudgeTask
		candidate knowledgeEvidenceJudgeCandidate
		fact      knowledgeEvidenceFact
	}{
		{
			name: "owner and chairman",
			task: knowledgeEvidenceJudgeTask{
				TaskID:   "T1",
				Query:    "老板是谁",
				Entities: []knowledgeEvidenceJudgeEntity{{Text: "老板", Type: "person_role"}},
			},
			candidate: knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 101, "董事长信息", "问题：董事长是谁\n答案：董事长是汤东强。", 0.9),
			},
			fact: knowledgeEvidenceFact{FactID: "T1F1", Aspect: "other", Statement: "董事长是汤东强。", CriticalValues: []string{"汤东强"}},
		},
		{
			name: "desk and writing desk",
			task: knowledgeEvidenceJudgeTask{
				TaskID:   "T1",
				Query:    "房间有没有办公桌",
				Entities: []knowledgeEvidenceJudgeEntity{{Text: "办公桌", Type: "facility"}},
			},
			candidate: knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 101, "客房书桌", "问题：客房是否配有书桌\n答案：客房配有书桌。", 0.9),
			},
			fact: knowledgeEvidenceFact{FactID: "T1F1", Aspect: "existence", Statement: "客房配有书桌。"},
		},
		{
			name: "nearby and surrounding",
			task: knowledgeEvidenceJudgeTask{
				TaskID:   "T1",
				Query:    "附近有什么好玩的",
				Entities: []knowledgeEvidenceJudgeEntity{{Text: "附近", Type: "location_scope"}},
			},
			candidate: knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 101, "周边游玩", "问题：周边有哪些游玩地点\n答案：可以去罍街和合柴1972游玩。", 0.9),
			},
			fact: knowledgeEvidenceFact{FactID: "T1F1", Aspect: "other", Statement: "可以去罍街和合柴1972游玩。", CriticalValues: []string{"罍街", "合柴1972"}},
		},
		{
			name: "restroom and bathroom",
			task: knowledgeEvidenceJudgeTask{
				TaskID:   "T1",
				Query:    "房间里有洗手间吗",
				Entities: []knowledgeEvidenceJudgeEntity{{Text: "洗手间", Type: "facility"}},
			},
			candidate: knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 101, "客房卫生间", "问题：客房内有卫生间吗\n答案：客房内有卫生间。", 0.9),
			},
			fact: knowledgeEvidenceFact{FactID: "T1F1", Aspect: "existence", Statement: "客房内有卫生间。"},
		},
		{
			name: "slippers and indoor shoes",
			task: knowledgeEvidenceJudgeTask{
				TaskID:   "T1",
				Query:    "拖鞋去哪里拿",
				Entities: []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "item"}},
			},
			candidate: knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 101, "室内鞋领取", "问题：室内鞋去哪里领取\n答案：室内鞋可前往1313对面洗衣房领取。", 0.9),
			},
			fact: knowledgeEvidenceFact{FactID: "T1F1", Aspect: "location", Statement: "室内鞋可前往1313对面洗衣房领取。", CriticalValues: []string{"1313", "洗衣房"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.task.Candidates = []knowledgeEvidenceJudgeCandidate{test.candidate}
			test.task.RawCandidates = append([]knowledgeEvidenceJudgeCandidate(nil), test.task.Candidates...)
			selection := normalizeParsedKnowledgeEvidenceLayerSelection(
				"T1",
				knowledgeEvidenceLayerStore,
				knowledgeEvidenceJudgeResponseLayer{
					Layer:                knowledgeEvidenceLayerStore,
					Decision:             knowledgeEvidenceDecisionDirectSingle,
					SelectedCandidateIDs: []string{"T1C1"},
					SupportedFacts:       []knowledgeEvidenceFact{test.fact},
				},
				map[string]struct{}{"T1C1": {}},
				test.task,
			)
			if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SelectedCandidateIDs) != 1 {
				t.Fatalf("model-selected semantic alias must not be rejected by local literal matching: %#v", selection)
			}
		})
	}
}

func TestModelSelectedEvidenceRejectsExplicitRoomTypeAndConfigurationScopeConflicts(t *testing.T) {
	tests := []struct {
		name      string
		task      knowledgeEvidenceJudgeTask
		candidate knowledgeEvidenceJudgeCandidate
	}{
		{
			name: "different room type",
			task: knowledgeEvidenceJudgeTask{
				TaskID:   "T1",
				Query:    "麦田房型有办公桌吗",
				Entities: []knowledgeEvidenceJudgeEntity{{Text: "麦田", Type: "room_type"}},
			},
			candidate: knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 101, "合柴房型办公桌", "问题：合柴房型有办公桌吗\n答案：合柴房型有办公桌。", 0.9),
			},
		},
		{
			name: "different wifi scope",
			task: knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "客房wifi账号和密码是什么"},
			candidate: knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 101, "大堂wifi", "问题：大堂wifi账号和密码是什么\n答案：大堂wifi账号是Lobby，密码是12345678。", 0.9),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.task.Candidates = []knowledgeEvidenceJudgeCandidate{test.candidate}
			test.task.RawCandidates = append([]knowledgeEvidenceJudgeCandidate(nil), test.task.Candidates...)
			selection := normalizeParsedKnowledgeEvidenceLayerSelection(
				"T1",
				knowledgeEvidenceLayerStore,
				knowledgeEvidenceJudgeResponseLayer{
					Layer:                knowledgeEvidenceLayerStore,
					Decision:             knowledgeEvidenceDecisionDirectSingle,
					SelectedCandidateIDs: []string{"T1C1"},
					SupportedFacts:       []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "other", Statement: test.candidate.Hit.Content}},
				},
				map[string]struct{}{"T1C1": {}},
				test.task,
			)
			if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
				t.Fatalf("an explicit room/configuration scope conflict must fail closed: %#v", selection)
			}
		})
	}
}

func TestModelSelectedEvidenceDoesNotTreatGenericRoomTypeQuestionsAsExplicitConflicts(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		entities []knowledgeEvidenceJudgeEntity
		question string
		answer   string
	}{
		{
			name:     "room type list",
			query:    "你们有哪些房型",
			entities: []knowledgeEvidenceJudgeEntity{{Text: "房型", Type: "room_type"}},
			question: "你们有哪些房型",
			answer:   "酒店房型包括合柴、麦田。",
		},
		{
			name:  "room type filtered by desk",
			query: "有办公桌的房型有哪些",
			entities: []knowledgeEvidenceJudgeEntity{
				{Text: "房型", Type: "room_type"},
				{Text: "办公桌", Type: "facility"},
			},
			question: "有办公桌的房型有哪些",
			answer:   "有办公桌的房型包括合柴、麦田。",
		},
		{
			name:  "room type filtered by two facilities",
			query: "推荐一个既有沙发又有办公桌的房型",
			entities: []knowledgeEvidenceJudgeEntity{
				{Text: "房型", Type: "room_type"},
				{Text: "沙发", Type: "facility"},
				{Text: "办公桌", Type: "facility"},
			},
			question: "哪些房型既有沙发又有办公桌",
			answer:   "合柴房型既有沙发又有办公桌。",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "房型设施", "问题："+test.question+"\n答案："+test.answer, 0.9),
			}
			task := knowledgeEvidenceJudgeTask{
				TaskID:        "T1",
				Query:         test.query,
				Entities:      test.entities,
				Candidates:    []knowledgeEvidenceJudgeCandidate{candidate},
				RawCandidates: []knowledgeEvidenceJudgeCandidate{candidate},
			}
			if conflict := knowledgeEvidenceCandidateHasExplicitTaskConflict(task, candidate, test.question, test.answer); conflict {
				t.Fatalf("generic room-type wording was treated as an explicit conflict")
			}
			selection := normalizeParsedKnowledgeEvidenceLayerSelection(
				"T1",
				knowledgeEvidenceLayerStore,
				knowledgeEvidenceJudgeResponseLayer{
					Layer:                knowledgeEvidenceLayerStore,
					Decision:             knowledgeEvidenceDecisionDirectSingle,
					SelectedCandidateIDs: []string{"T1C1"},
					SupportedFacts: []knowledgeEvidenceFact{{
						FactID: "T1F1", Aspect: "scope", Statement: test.answer,
					}},
				},
				map[string]struct{}{"T1C1": {}},
				task,
			)
			if selection.Decision == knowledgeEvidenceDecisionProtocolInvalid {
				t.Fatalf("generic room-type wording must remain owned by Judge: %#v", selection)
			}
		})
	}
}

func TestModelSelectedRoomTypeConflictUsesExplicitIntentEntity(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		candidateText string
		wantInvalid   bool
	}{
		{
			name:          "named room without room type suffix conflicts",
			query:         "麦田有办公桌吗",
			candidateText: "问题：合柴房型有办公桌吗\n答案：合柴房型有办公桌。",
			wantInvalid:   true,
		},
		{
			name:          "target room in candidate enumeration is compatible",
			query:         "麦田房型有办公桌吗",
			candidateText: "问题：哪些房型有办公桌\n答案：有办公桌的房型包括合柴、麦田。",
			wantInvalid:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := knowledgeEvidenceJudgeCandidate{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "房型办公桌", test.candidateText, 0.9),
			}
			task := knowledgeEvidenceJudgeTask{
				TaskID:        "T1",
				Query:         test.query,
				Entities:      []knowledgeEvidenceJudgeEntity{{Text: "麦田", Type: "room_type"}},
				Candidates:    []knowledgeEvidenceJudgeCandidate{candidate},
				RawCandidates: []knowledgeEvidenceJudgeCandidate{candidate},
			}
			selection := normalizeParsedKnowledgeEvidenceLayerSelection(
				"T1",
				knowledgeEvidenceLayerStore,
				knowledgeEvidenceJudgeResponseLayer{
					Layer:                knowledgeEvidenceLayerStore,
					Decision:             knowledgeEvidenceDecisionDirectSingle,
					SelectedCandidateIDs: []string{"T1C1"},
					SupportedFacts: []knowledgeEvidenceFact{{
						FactID: "T1F1", Aspect: "existence", Statement: splitJudgeTestAnswer(test.candidateText),
					}},
				},
				map[string]struct{}{"T1C1": {}},
				task,
			)
			if gotInvalid := selection.Decision == knowledgeEvidenceDecisionProtocolInvalid; gotInvalid != test.wantInvalid {
				t.Fatalf("selection invalid=%v want %v: %#v", gotInvalid, test.wantInvalid, selection)
			}
		})
	}
}

func splitJudgeTestAnswer(content string) string {
	_, answer := splitKnowledgeEvidenceFAQForQuery(judgeTestHit(1, 1, "", content, 1), "")
	return answer
}

func TestModelSelectedExactHandoffIsBlockedByCompleteSameLayerBodyOutsideBudget(t *testing.T) {
	handoff := knowledgeEvidenceJudgeCandidate{
		CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
		Hit: judgeTestHit(1, 101, "马桶转接", "问题：马桶堵了怎么办\n答案：转接", 0.99),
	}
	body := knowledgeEvidenceJudgeCandidate{
		CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore,
		Hit: judgeTestHit(1, 102, "马桶处理", "问题：马桶堵了如何处理\n答案：可以先使用马桶吸处理。", 0.95),
	}
	task := knowledgeEvidenceJudgeTask{
		TaskID:        "T1",
		Intent:        "service_request",
		Query:         "马桶堵了怎么办",
		Entities:      []knowledgeEvidenceJudgeEntity{{Text: "马桶", Type: "facility"}},
		Candidates:    []knowledgeEvidenceJudgeCandidate{handoff},
		RawCandidates: []knowledgeEvidenceJudgeCandidate{handoff, body},
	}

	selection := normalizeParsedKnowledgeEvidenceLayerSelection(
		"T1",
		knowledgeEvidenceLayerStore,
		knowledgeEvidenceJudgeResponseLayer{
			Layer:                knowledgeEvidenceLayerStore,
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			SelectedCandidateIDs: []string{"T1C1"},
		},
		map[string]struct{}{"T1C1": {}},
		task,
	)
	if selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("an exact handoff must not hide a complete same-layer body answer outside the model budget: %#v", selection)
	}
}

func TestKnowledgeEvidenceTimeConflictComparesOnlyOverlappingSlots(t *testing.T) {
	if conflict, comparable := knowledgeEvidenceTimeSlotAnswersConflict(
		"早餐几点开始", "早餐7:00开始。",
		"早餐几点结束", "早餐9:30结束。",
	); conflict || comparable {
		t.Fatalf("start and end are different slots and must not conflict: conflict=%v comparable=%v", conflict, comparable)
	}

	tests := []struct {
		name           string
		peerQuestion   string
		peerAnswer     string
		wantConflict   bool
		wantComparable bool
	}{
		{name: "schedule and matching start", peerQuestion: "早餐几点开始", peerAnswer: "早餐7:00开始。", wantComparable: true},
		{name: "schedule and matching end", peerQuestion: "早餐几点结束", peerAnswer: "早餐9:30结束。", wantComparable: true},
		{name: "schedule and conflicting start", peerQuestion: "早餐几点开始", peerAnswer: "早餐8:00开始。", wantConflict: true, wantComparable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conflict, comparable := knowledgeEvidenceTimeSlotAnswersConflict(
				"早餐时间", "早餐时间为7:00-9:30。",
				test.peerQuestion, test.peerAnswer,
			)
			if conflict != test.wantConflict || comparable != test.wantComparable {
				t.Fatalf("unexpected slot comparison: conflict=%v comparable=%v", conflict, comparable)
			}
		})
	}
}

func TestKnowledgeEvidenceTimeFieldRolesMapCheckinAndCheckout(t *testing.T) {
	if got := knowledgeEvidenceConflictQuestionFieldRole(normalizeStrictKnowledgeEvidenceFAQText("入住时间是什么时候"), "time"); got != "start" {
		t.Fatalf("入住时间 must map to start, got %q", got)
	}
	if got := knowledgeEvidenceConflictQuestionFieldRole(normalizeStrictKnowledgeEvidenceFAQText("退房时间是什么时候"), "time"); got != "end" {
		t.Fatalf("退房时间 must map to end, got %q", got)
	}
}

func TestStrictExactFactualFallbackRejectsConflictingOwnerNames(t *testing.T) {
	exact := knowledgeEvidenceJudgeCandidate{
		CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
		Hit: judgeTestHit(1, 101, "老板信息", "问题：老板是谁\n答案：老板是汤东强。", 0.99),
	}
	task := knowledgeEvidenceJudgeTask{
		TaskID:     "T1",
		Query:      "老板是谁",
		Candidates: []knowledgeEvidenceJudgeCandidate{exact},
		RawCandidates: []knowledgeEvidenceJudgeCandidate{
			exact,
			{
				CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 102, "董事长信息", "问题：董事长叫什么\n答案：董事长为李明。", 0.95),
			},
		},
	}

	if selection, ok := strictExactKnowledgeEvidenceFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("conflicting same-object owner names must block exact fallback: %#v", selection)
	}
	if !knowledgeEvidenceIdentityValuesConflict("老板是汤东强。", "董事长为李明。") {
		t.Fatal("owner identity extractor failed to expose the conflicting names")
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsSelectedQuantityThatAlsoConflicts(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "房间内两瓶矿泉水是否都免费",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "矿泉水数量和费用",
				"问题：房间里有两瓶矿泉水吗\n答案：是的，房间内两瓶免费，但同一房间说明里又写了四瓶。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"房间内有两瓶矿泉水。","criticalValues":["两瓶"]},{"factId":"T1F2","aspect":"price","statement":"房间内两瓶矿泉水免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse conflicting quantity response: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("a matching quantity must not hide another selected value with the same unit: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseIgnoresQuantityFromDifferentScope(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "房间内两瓶矿泉水是否都免费",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "矿泉水数量和费用",
				"问题：房间里有两瓶矿泉水吗\n答案：是的，房间内两瓶免费，会议室另有四瓶。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"房间内有两瓶矿泉水。","criticalValues":["两瓶"]},{"factId":"T1F2","aspect":"price","statement":"房间内两瓶矿泉水免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse scope-bound quantity response: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("a meeting-room quantity must not invalidate the selected room answer: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsConflictingQuantityAcrossSelectedCandidatesWithoutEntities(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "房间内两瓶矿泉水是否都免费",
		Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "两瓶矿泉水", "问题：房间里有两瓶矿泉水吗\n答案：是的，都是免费的。", 0.97)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "四瓶矿泉水", "问题：房间里有四瓶矿泉水吗\n答案：是的。", 0.96)},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"房间内有两瓶矿泉水。","criticalValues":["两瓶"]},{"factId":"T1F2","aspect":"price","statement":"房间内矿泉水都是免费的。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse multi-candidate quantity response: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("multiple selected candidates cannot bypass the task quantity constraint: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsConflictingSelectedDeliveryAddresses(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "外卖地址怎么填",
		Objective: "location",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "南七外卖地址", "问题：外卖地址怎么填\n答案：丽斯未来酒店合肥南七店+对应楼层房间号。", 0.97)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "外卖收货地址", "问题：收货地址应该写什么\n答案：壹间公寓+对应楼层房间号。", 0.96)},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"location","statement":"外卖地址填写丽斯未来酒店合肥南七店加对应楼层房间号。","criticalValues":["丽斯未来酒店合肥南七店"]},{"factId":"T1F2","aspect":"location","statement":"外卖地址填写壹间公寓加对应楼层房间号。","criticalValues":["壹间公寓"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse conflicting delivery addresses: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("different selected delivery address values must not reach Generate: %#v", selection)
	}
}

func TestSelectedDeliveryAddressConflictAllowsSameAddressWithComplementaryWording(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "外卖地址怎么填",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "外卖地址", "问题：外卖地址怎么填\n答案：丽斯未来酒店合肥南七店+对应楼层房间号。", 0.97)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "收货地址", "问题：收货地址应该写什么\n答案：外卖地址请填写丽斯未来酒店合肥南七店，并补充楼层和房号。", 0.96)},
		},
	}
	if knowledgeEvidenceSelectedCandidatesHaveConflictingAnswers(task, knowledgeEvidenceLayerStore, []string{"T1C1", "T1C2"}) {
		t.Fatal("the same delivery address with complementary room-number wording must remain combinable")
	}
}

func TestParseKnowledgeEvidenceJudgeResponseUsesFAQQuestionSubjectForTimeRange(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "早餐几点开始和结束",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "早餐时间", "问题：早餐几点到几点\n答案：7:00-9:30。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"7:00-9:30。","criticalValues":["7:00-9:30"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse subject-bound time range: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.MissingAspects) != 0 ||
		len(selection.SupportedFacts) == 0 || !strings.Contains(selection.SupportedFacts[0].Statement, "早餐") {
		probeFacts := bindKnowledgeEvidenceFAQTimeSubject(task, "早餐几点到几点", "7:00-9:30。", []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "time", Statement: "7:00-9:30。", CriticalValues: []string{"7:00-9:30"}}})
		t.Fatalf("the FAQ question must bind its subject to an answer-only time range: selection=%#v facts=%#v missing=%#v", selection, probeFacts, missingRequiredKnowledgeEvidenceAspects(task, probeFacts))
	}
}

func TestBindKnowledgeEvidenceFAQTimeSubjectDoesNotRelabelDifferentSubject(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "早餐几点开始",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
	}
	lunchFact := knowledgeEvidenceFact{
		FactID:         "T1F1",
		Aspect:         "time",
		Statement:      "午餐12:00开始。",
		CriticalValues: []string{"12:00"},
	}
	got := bindKnowledgeEvidenceFAQTimeSubject(
		task,
		"早餐几点开始",
		"早餐7:00开始，午餐12:00开始。",
		[]knowledgeEvidenceFact{lunchFact},
	)
	if len(got) != 1 || got[0].Statement != lunchFact.Statement {
		t.Fatalf("an explicitly named different time subject must not be relabeled as breakfast: %#v", got)
	}

	subjectless := bindKnowledgeEvidenceFAQTimeSubject(
		task,
		"早餐几点开始",
		"7:00开始。",
		[]knowledgeEvidenceFact{{FactID: "T1F2", Aspect: "time", Statement: "7:00开始。", CriticalValues: []string{"7:00"}}},
	)
	if len(subjectless) != 1 || !strings.Contains(subjectless[0].Statement, "早餐") {
		t.Fatalf("a subjectless time answer from the same FAQ must still bind to breakfast: %#v", subjectless)
	}
}

func TestMissingRequiredKnowledgeEvidenceAspectsRequiresEveryTimeSubject(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "早餐和晚餐几点开始",
		Objective: "time",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "早餐", Type: "meal"},
			{Text: "晚餐", Type: "meal"},
		},
	}
	facts := []knowledgeEvidenceFact{{
		FactID:         "T1F1",
		Aspect:         "time",
		Statement:      "早餐7:00开始。",
		CriticalValues: []string{"7:00"},
	}}
	missing := missingRequiredKnowledgeEvidenceAspects(task, facts)
	joined := strings.Join(missing, " ")
	if !strings.Contains(joined, "晚餐开始时间") || strings.Contains(joined, "早餐开始时间") {
		t.Fatalf("time completeness must be checked for every requested subject: %#v", missing)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRepairsChineseNaturalTimeFacts(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "早餐几点开始，几点结束",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "早餐时间", "问题：早餐时间\n答案：早餐早上七点开始，晚上九点结束。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[],"missingAspects":[]}]}]}`
	question, answer := splitKnowledgeEvidenceFAQForQuery(task.Candidates[0].Hit, task.Query)
	rebuilt := deterministicKnowledgeEvidenceFactsFromFAQ(task.TaskID, answer)
	rebuilt = enrichKnowledgeEvidenceFactsFromFAQUnit(task, question, answer, rebuilt)
	grounded := groundedKnowledgeEvidenceFacts(task, knowledgeEvidenceLayerStore, []string{"T1C1"}, rebuilt)
	if len(grounded) != 2 {
		t.Fatalf("Chinese start and end time facts must both remain grounded before protocol repair: rebuilt=%#v grounded=%#v", rebuilt, grounded)
	}
	if missing := strictMechanicalMissingKnowledgeEvidenceAspects(task, grounded); len(missing) != 0 {
		t.Fatalf("Chinese time facts must cover both requested slots before protocol repair: rebuilt=%#v grounded=%#v missing=%#v", rebuilt, grounded, missing)
	}

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse Chinese natural time: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.MissingAspects) != 0 {
		t.Fatalf("Chinese start and end times must survive deterministic repair: %#v", selection)
	}
}

func TestRequiredKnowledgeEvidenceTimeSlotsRecognizesStartAndEndConjunction(t *testing.T) {
	got := requiredKnowledgeEvidenceTimeSlots("早餐几点开始和结束")
	if len(got) != 2 || got[0] != "start" || got[1] != "end" {
		t.Fatalf("start/end conjunction must require both slots, got %#v", got)
	}

	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "早餐几点开始和结束",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "早餐开始时间", "问题：早餐几点开始\n答案：早餐7:00开始。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"早餐7:00开始。","criticalValues":["7:00"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse incomplete start/end response: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("a start time alone cannot answer a start-and-end question: %#v", selection)
	}

	endOnly := requiredKnowledgeEvidenceTimeSlots("早餐开始了吗，几点结束")
	if len(endOnly) != 1 || endOnly[0] != "end" {
		t.Fatalf("mentioning that breakfast started must not invent a requested start-time slot: %#v", endOnly)
	}
}

func TestKnowledgeEvidenceNaturalAndNumericTimesCompareAsEquivalent(t *testing.T) {
	conflict, comparable := knowledgeEvidenceTimeSlotAnswersConflict(
		"早餐几点开始", "早餐7:00开始。",
		"早餐几点开始", "早餐早上七点开始。",
	)
	if conflict || !comparable {
		t.Fatalf("equivalent natural and numeric times must compare cleanly: conflict=%v comparable=%v numeric=%#v natural=%#v", conflict, comparable,
			knowledgeEvidenceTimeSlotValues("start", "早餐7:00开始。"), knowledgeEvidenceTimeSlotValues("start", "早餐早上七点开始。"))
	}
}

func TestKnowledgeEvidenceTimeConflictComparisonKeepsSubjectsSeparate(t *testing.T) {
	conflict, comparable := knowledgeEvidenceTimeSlotAnswersConflict(
		"早餐几点开始", "早餐7点开始，午餐12点开始。",
		"早餐几点开始", "早餐7点开始。",
	)
	if conflict || !comparable {
		t.Fatalf("lunch time must not overwrite breakfast during conflict comparison: conflict=%v comparable=%v left=%#v", conflict, comparable,
			knowledgeEvidenceTimeSlotValuesForQuestion("早餐几点开始", "start", "早餐7点开始，午餐12点开始。"))
	}
}

func TestKnowledgeEvidenceTimeConflictComparisonBindsLeadingImplicitClauseToFAQSubject(t *testing.T) {
	conflict, comparable := knowledgeEvidenceTimeSlotAnswersConflict(
		"早餐几点开始", "7点开始，午餐12点开始。",
		"早餐几点开始", "早餐8点开始。",
	)
	if !conflict || !comparable {
		t.Fatalf("the leading subjectless clause must remain bound to breakfast: conflict=%v comparable=%v left=%#v", conflict, comparable,
			knowledgeEvidenceTimeSlotValuesForQuestion("早餐几点开始", "start", "7点开始，午餐12点开始。"))
	}
}

func TestKnowledgeEvidenceTimeConflictComparisonKeepsExclusiveConditionsSeparate(t *testing.T) {
	conflict, comparable := knowledgeEvidenceTimeSlotAnswersConflict(
		"工作日早餐几点开始", "工作日早餐7点开始。",
		"周末早餐几点开始", "周末早餐8点开始。",
	)
	if conflict || comparable {
		t.Fatalf("weekday and weekend schedules are complementary rather than conflicting: conflict=%v comparable=%v", conflict, comparable)
	}
}

func TestFilterKnowledgeEvidenceFAQTimeFactsDropsExplicitOtherSubject(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "早餐几点开始",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
	}
	facts := filterKnowledgeEvidenceFAQTimeFacts(task, "早餐时间", "早餐7点开始，午餐12点开始。", []knowledgeEvidenceFact{
		{FactID: "T1F1", Aspect: "time", Statement: "早餐7点开始。", CriticalValues: []string{"7点"}},
		{FactID: "T1F2", Aspect: "time", Statement: "午餐12点开始。", CriticalValues: []string{"12点"}},
	})
	if len(facts) != 1 || !strings.Contains(facts[0].Statement, "早餐") || strings.Contains(facts[0].Statement, "午餐") {
		t.Fatalf("an explicit lunch clause must not survive in breakfast facts: %#v", facts)
	}
}

func TestKnowledgeEvidenceClockNormalizationHandlesMidnightAndNextDayRange(t *testing.T) {
	if got := normalizeKnowledgeEvidenceClockTime("晚上12点"); got != "00:00" {
		t.Fatalf("evening twelve must normalize to midnight, got %q", got)
	}
	values := knowledgeEvidenceTimeSlotValues("schedule", "晚上10点到次日2点。")
	if values["start"] != "22:00" || values["end"] != "02:00" {
		t.Fatalf("a next-day endpoint must not inherit the evening period: %#v", values)
	}
}

func TestKnowledgeEvidenceTimeSlotsDoNotInferRangeAcrossThreePoints(t *testing.T) {
	values := knowledgeEvidenceTimeSlotValues("schedule", "7点，12点到18点。")
	if values["start"] != "" || values["end"] != "" {
		t.Fatalf("three time points must not be collapsed into one implicit range: %#v", values)
	}
}

func TestKnowledgeEvidenceTimeSlotsDoNotTurnTwoStartsIntoACompleteRange(t *testing.T) {
	values := knowledgeEvidenceTimeSlotValues("schedule", "工作日7点开始，周末8点开始。")
	if values["start"] == "" || values["end"] != "" {
		t.Fatalf("two conditional start times must not invent an end time: %#v", values)
	}

	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "早餐几点开始和结束",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "早餐开始时间", "问题：早餐时间\n答案：工作日7点开始，周末8点开始。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"partial","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"工作日7点开始。","criticalValues":["7点"]},{"factId":"T1F2","aspect":"time","statement":"周末8点开始。","criticalValues":["8点"]}],"missingAspects":["早餐结束时间"]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse conditional start times: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionPartial || !strings.Contains(strings.Join(selection.MissingAspects, " "), "结束") {
		t.Fatalf("the unresolved end time must survive reconciliation: %#v", selection)
	}
}

func TestKnowledgeEvidenceTimeSlotsRecognizeStartToImplicitEndRange(t *testing.T) {
	values := knowledgeEvidenceTimeSlotValues("schedule", "早餐时间为7点开始到9点。")
	if values["start"] != "07:00" || values["end"] != "09:00" {
		t.Fatalf("an explicit connected range must preserve both endpoints: %#v", values)
	}

	values = knowledgeEvidenceTimeSlotValues("schedule", "早餐时间为7点到9点结束。")
	if values["start"] != "07:00" || values["end"] != "09:00" {
		t.Fatalf("an end-marked connected range must preserve both endpoints: %#v", values)
	}
}

func TestKnowledgeEvidenceTimeSlotsDoNotTreatArrivalWordsAsRangeConnectors(t *testing.T) {
	for _, answer := range []string{
		"14:00签到，18:00离店。",
		"14:00到店，18:00离店。",
	} {
		values := knowledgeEvidenceTimeSlotValues("schedule", answer)
		if values["start"] != "" || values["end"] != "" {
			t.Fatalf("arrival wording must not become an implicit time range: answer=%q values=%#v", answer, values)
		}
	}
	for _, answer := range []string{"7点到9点。", "7点至9点。", "7点开始到9点。"} {
		values := knowledgeEvidenceTimeSlotValues("schedule", answer)
		if values["start"] != "07:00" || values["end"] != "09:00" {
			t.Fatalf("an explicit range connector must remain supported: answer=%q values=%#v", answer, values)
		}
	}
}

func TestKnowledgeEvidenceAfternoonPeriodPropagatesAcrossNaturalRange(t *testing.T) {
	conflict, comparable := knowledgeEvidenceTimeSlotAnswersConflict(
		"早餐时间", "下午2点到5点。",
		"早餐时间", "14:00-17:00。",
	)
	if conflict || !comparable {
		t.Fatalf("an afternoon prefix must apply to both ends of a natural range: conflict=%v comparable=%v left=%#v right=%#v", conflict, comparable,
			knowledgeEvidenceTimeSlotValues("schedule", "下午2点到5点。"), knowledgeEvidenceTimeSlotValues("schedule", "14:00-17:00。"))
	}
}

func TestKnowledgeEvidenceIdentityComparisonIgnoresHonorificSuffixes(t *testing.T) {
	if knowledgeEvidenceIdentityValuesConflict("老板是汤东强。", "董事长为汤东强先生。") {
		t.Fatal("the same identity with an honorific suffix must not be treated as a conflict")
	}
	if !knowledgeEvidenceIdentityValuesConflict("老板是汤东强先生。", "董事长为李明女士。") {
		t.Fatal("different names must remain conflicting after honorific normalization")
	}
}

func TestKnowledgeEvidenceIdentityComparisonSupportsBothRoleOrders(t *testing.T) {
	if knowledgeEvidenceIdentityValuesConflict("老板是汤东强。", "汤东强是老板。") {
		t.Fatal("the same owner written in opposite role order must not conflict")
	}
	if knowledgeEvidenceIdentityValuesConflict("董事长为汤东强先生。", "汤东强先生担任董事长。") {
		t.Fatal("opposite role order must preserve honorific normalization")
	}
	if !knowledgeEvidenceIdentityValuesConflict("汤东强是老板。", "李明是老板。") {
		t.Fatal("different people assigned to the same role must remain conflicting")
	}
	if value := knowledgeEvidenceIdentityValue("汤东强是老板。"); normalizeKnowledgeEvidenceIdentityValue(value) != "汤东强" {
		t.Fatalf("person-first role assignment extracted the wrong identity: %q", value)
	}
	if value := knowledgeEvidenceIdentityValue("老板。"); value != "" {
		t.Fatalf("a role word must never be accepted as a person identity: %q", value)
	}
}

func TestKnowledgeEvidenceIdentityComparisonIgnoresUnavailableBareReplies(t *testing.T) {
	for _, unavailable := range []string{"不知道。", "无法提供。", "请联系前台。"} {
		if knowledgeEvidenceIdentityValuesConflict(unavailable, "汤东强。") {
			t.Fatalf("an unavailable reply must not be parsed as a conflicting person name: %q", unavailable)
		}
	}
}

func TestKnowledgeEvidenceTimeSlotsDoNotCrossSubjects(t *testing.T) {
	facts := []knowledgeEvidenceFact{{
		FactID:         "T1F1",
		Aspect:         "time",
		Statement:      "早餐7点开始，午餐9点结束。",
		CriticalValues: []string{"7点", "9点"},
	}}
	if !knowledgeEvidenceFactsCoverSubjectTimeSlot(facts, "早餐", "start") {
		t.Fatal("breakfast must retain its own start time")
	}
	if knowledgeEvidenceFactsCoverSubjectTimeSlot(facts, "早餐", "end") {
		t.Fatal("lunch end time must not complete the breakfast range")
	}
	if !knowledgeEvidenceFactsCoverSubjectTimeSlot(facts, "午餐", "end") {
		t.Fatal("lunch must retain its own end time")
	}
}

func TestKnowledgeEvidenceTimeSlotsKeepRangeBeforeNextSubject(t *testing.T) {
	facts := []knowledgeEvidenceFact{{
		FactID:         "T1F1",
		Aspect:         "time",
		Statement:      "早餐7点开始到9点，午餐12点开始。",
		CriticalValues: []string{"7点", "9点", "12点"},
	}}
	if !knowledgeEvidenceFactsCoverSubjectTimeSlot(facts, "早餐", "start") ||
		!knowledgeEvidenceFactsCoverSubjectTimeSlot(facts, "早餐", "end") {
		t.Fatalf("breakfast range must keep both endpoints: %#v", facts)
	}
	if !knowledgeEvidenceFactsCoverSubjectTimeSlot(facts, "午餐", "start") ||
		knowledgeEvidenceFactsCoverSubjectTimeSlot(facts, "午餐", "end") {
		t.Fatalf("lunch must expose only its explicit start: %#v", facts)
	}
}

func TestBindKnowledgeEvidenceFAQTimeSubjectAllowsGenericBusinessHours(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "早餐几点到几点",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
	}
	facts := bindKnowledgeEvidenceFAQTimeSubject(task, task.Query, "营业时间为7点到9点。", []knowledgeEvidenceFact{{
		FactID:         "T1F1",
		Aspect:         "time",
		Statement:      "营业时间为7点到9点。",
		CriticalValues: []string{"7点", "9点"},
	}})
	if len(facts) != 1 || !strings.Contains(facts[0].Statement, "早餐") ||
		!knowledgeEvidenceFactsCoverSubjectTimeSlot(facts, "早餐", "start") ||
		!knowledgeEvidenceFactsCoverSubjectTimeSlot(facts, "早餐", "end") {
		t.Fatalf("generic FAQ hours must bind to the question's unique subject: %#v", facts)
	}
}

func TestBindKnowledgeEvidenceFAQTimeSubjectRejectsCheckoutTime(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "早餐几点结束",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "meal"}},
	}
	facts := bindKnowledgeEvidenceFAQTimeSubject(task, task.Query, "退房12点。", []knowledgeEvidenceFact{{
		FactID:         "T1F1",
		Aspect:         "time",
		Statement:      "退房12点。",
		CriticalValues: []string{"12点"},
	}})
	if len(facts) != 1 || strings.Contains(facts[0].Statement, "早餐") ||
		knowledgeEvidenceFactsCoverSubjectTimeSlot(facts, "早餐", "end") {
		t.Fatalf("checkout time must not be relabeled as breakfast: %#v", facts)
	}
}

func TestRequiredKnowledgeEvidenceTimeSlotsRecognizesNaturalRangeQuestions(t *testing.T) {
	for _, query := range []string{"早餐从什么时候到什么时候", "早餐几点至几点"} {
		got := requiredKnowledgeEvidenceTimeSlots(query)
		if len(got) != 2 || got[0] != "start" || got[1] != "end" {
			t.Fatalf("%q must require both time endpoints, got %#v", query, got)
		}
	}
}

func TestParseKnowledgeEvidenceJudgeResponseIgnoresQuantityForExplicitDifferentSubject(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "房间内两瓶矿泉水是否都免费",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "矿泉水数量和费用",
				"问题：房间里有两瓶矿泉水吗\n答案：房间内两瓶矿泉水免费，另有四瓶饮料。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"房间内有两瓶矿泉水。","criticalValues":["两瓶"]},{"factId":"T1F2","aspect":"price","statement":"房间内两瓶矿泉水免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse different-subject quantity response: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("a drink quantity must not conflict with mineral-water quantity: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseInfersQuerySubjectWhenEntitiesAreMissing(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "房间内两瓶矿泉水是否都免费",
		Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "矿泉水数量和费用",
				"问题：房间里有两瓶矿泉水吗\n答案：房间内两瓶矿泉水免费，另有四瓶饮料。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"房间内有两瓶矿泉水。","criticalValues":["两瓶"]},{"factId":"T1F2","aspect":"price","statement":"房间内两瓶矿泉水免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse missing-entity different-subject quantity response: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("an explicitly different drink quantity must not invalidate mineral water when Intent omits entities: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsMissingEntityQuantitySubjectSwap(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水有两瓶，饮料有四瓶吗",
		Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "饮品数量",
				"问题：矿泉水有两瓶，饮料有四瓶吗\n答案：矿泉水有四瓶，饮料有两瓶。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"矿泉水有两瓶，饮料有四瓶。","criticalValues":["两瓶","四瓶"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse missing-entity quantity swap: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("omitted entities must not allow quantities to swap between explicit query subjects: %#v", selection)
	}
}

func TestKnowledgeEvidenceQuantityClauseSubjectClassification(t *testing.T) {
	tests := map[string]string{
		"四瓶矿泉水": "required",
		"四瓶饮料":  "other",
		"矿泉水四瓶": "required",
		"饮料四瓶":  "other",
		"又写了四瓶": "implicit",
	}
	for clause, want := range tests {
		if got := knowledgeEvidenceQuantityClauseSubject(clause, "矿泉水"); got != want {
			t.Fatalf("unexpected quantity subject for %q: got %q want %q", clause, got, want)
		}
	}
}

func TestKnowledgeEvidenceQuantityOccurrencesKeepConjoinedSubjectsSeparate(t *testing.T) {
	for _, text := range []string{"房间内有两瓶矿泉水和四瓶饮料", "房间内有两瓶矿泉水、四瓶饮料"} {
		occurrences := knowledgeEvidenceQuantityOccurrences(text, "矿泉水")
		if len(occurrences) != 2 || occurrences[0].SubjectRelation != "required" || occurrences[1].SubjectRelation != "other" {
			t.Fatalf("conjoined quantities must keep their adjacent objects for %q: %#v", text, occurrences)
		}
	}
}

func TestKnowledgeEvidenceQuantityOccurrencesAllowQuestionBoundRoomAdverbs(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		Query:     "房间里有四瓶矿泉水吗",
		Objective: "quantity",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
	}
	for _, answer := range []string{"房间里又有四瓶。", "房间里现在有四瓶。", "房间内还放了四瓶。"} {
		if _, ok := knowledgeEvidenceEquivalentTaskQuantityInText(task, task.Query, answer, "四瓶"); !ok {
			t.Fatalf("the FAQ question must bind an elliptical room quantity for %q", answer)
		}
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsMultiSubjectQuantitySwap(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水和饮料分别有两瓶吗",
		Objective: "compound_information",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "矿泉水", Type: "supply"},
			{Text: "饮料", Type: "supply"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "饮品数量",
				"问题：矿泉水和饮料分别有两瓶吗\n答案：饮料两瓶，矿泉水四瓶。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"矿泉水和饮料分别有两瓶。","criticalValues":["两瓶"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse multi-subject quantity response: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("one subject's matching value must not hide another subject's conflicting quantity: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseAcceptsSharedQuantityPredicate(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水和饮料都有两瓶吗",
		Objective: "quantity",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "矿泉水", Type: "supply"},
			{Text: "饮料", Type: "supply"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "饮品数量",
				"问题：矿泉水和饮料都有两瓶吗\n答案：矿泉水和饮料都有两瓶。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"矿泉水和饮料都有两瓶。","criticalValues":["两瓶"]}],"missingAspects":[]}]}]}`
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if !knowledgeEvidenceSharedQuantityAppliesToSubjects(task.Query, requiredSubjects) {
		t.Fatalf("shared predicate must be recognized for %#v", requiredSubjects)
	}
	if targets, combined := knowledgeEvidenceTaskQuantityTargetsBySubject(task.Query, requiredSubjects); combined || len(targets["矿泉水"]) != 1 || len(targets["饮料"]) != 1 {
		t.Fatalf("shared value must bind to both subjects: combined=%v targets=%#v", combined, targets)
	}
	if knowledgeEvidenceSelectedCandidatesHaveExplicitSubjectConflict(task, knowledgeEvidenceLayerStore, []string{"T1C1"}) {
		t.Fatal("the matching shared FAQ must not have an explicit subject conflict")
	}
	if knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(task, knowledgeEvidenceLayerStore, []string{"T1C1"}) {
		t.Fatal("the matching shared FAQ must not have a task-bound quantity conflict")
	}
	if knowledgeEvidenceSelectedCandidatesHaveConflictingAnswers(task, knowledgeEvidenceLayerStore, []string{"T1C1"}) {
		t.Fatal("a single matching FAQ must not conflict with itself")
	}

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse shared quantity predicate: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("a shared quantity predicate must bind the value to every named subject: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseAcceptsUnpunctuatedQuantityBindings(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		answer string
		values []string
	}{
		{name: "and", query: "矿泉水有两瓶和饮料有四瓶吗", answer: "矿泉水有两瓶和饮料有四瓶。", values: []string{"两瓶", "四瓶"}},
		{name: "also", query: "矿泉水有两瓶且枕头有三个", answer: "矿泉水有两瓶且枕头有三个。", values: []string{"两瓶", "三个"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{
				TaskID:    "T1",
				Query:     test.query,
				Objective: "compound_information",
				Candidates: []knowledgeEvidenceJudgeCandidate{{
					CandidateID: "T1C1",
					Layer:       knowledgeEvidenceLayerStore,
					Hit:         judgeTestHit(1, 101, "用品数量", "问题："+test.query+"\n答案："+test.answer, 0.97),
				}},
			}
			raw := fmt.Sprintf(`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":%q,"criticalValues":[%q,%q]}],"missingAspects":[]}]}]}`,
				test.answer, test.values[0], test.values[1])

			parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatalf("parse unpunctuated quantity binding: %v", err)
			}
			if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionDirectSingle {
				t.Fatalf("correct unpunctuated subject bindings must remain answerable: %#v", selection)
			}
		})
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsUnpunctuatedQuantitySwap(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水有两瓶和饮料有四瓶吗",
		Objective: "compound_information",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "饮品数量",
				"问题：矿泉水有两瓶和饮料有四瓶吗\n答案：矿泉水有四瓶和饮料有两瓶。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"矿泉水有两瓶，饮料有四瓶。","criticalValues":["两瓶","四瓶"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse unpunctuated quantity swap: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("swapped values must remain protocol-invalid after connector-aware binding: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsAffirmativeAnswerThatNarrowsMultiSubjectQuestion(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		objective string
		answer    string
		aspect    string
		statement string
		partial   string
		missing   string
		values    []string
	}{
		{
			name:      "quantity",
			query:     "矿泉水和饮料都有两瓶吗",
			objective: "quantity",
			answer:    "是的，矿泉水有两瓶。",
			aspect:    "quantity",
			statement: "矿泉水和饮料都有两瓶。",
			partial:   "矿泉水有两瓶。",
			missing:   "饮料数量",
			values:    []string{"两瓶"},
		},
		{
			name:      "price",
			query:     "矿泉水和饮料都免费吗",
			objective: "price",
			answer:    "是的，矿泉水免费。",
			aspect:    "price",
			statement: "矿泉水和饮料都免费。",
			partial:   "矿泉水免费。",
			missing:   "饮料费用",
			values:    []string{"免费"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{
				TaskID:    "T1",
				Query:     test.query,
				Objective: test.objective,
				Entities: []knowledgeEvidenceJudgeEntity{
					{Text: "矿泉水", Type: "supply"},
					{Text: "饮料", Type: "supply"},
				},
				Candidates: []knowledgeEvidenceJudgeCandidate{{
					CandidateID: "T1C1",
					Layer:       knowledgeEvidenceLayerStore,
					Hit:         judgeTestHit(1, 101, "饮品信息", "问题："+test.query+"\n答案："+test.answer, 0.97),
				}},
			}
			valuesJSON, err := json.Marshal(test.values)
			if err != nil {
				t.Fatalf("marshal critical values: %v", err)
			}
			raw := fmt.Sprintf(`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":%q,"statement":%q,"criticalValues":%s}],"missingAspects":[]}]}]}`,
				test.aspect, test.statement, valuesJSON)

			parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatalf("parse narrowed affirmative answer: %v", err)
			}
			if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
				t.Fatalf("an affirmative answer naming only one required subject must not confirm every subject: %#v", selection)
			}

			partialRaw := fmt.Sprintf(`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"partial","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":%q,"statement":%q,"criticalValues":%s}],"missingAspects":[]}]}]}`,
				test.aspect, test.partial, valuesJSON)
			parsed, err = parseKnowledgeEvidenceJudgeResponse(partialRaw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatalf("parse narrowed partial answer: %v", err)
			}
			selection := parsed["T1"][knowledgeEvidenceLayerStore]
			if selection.Decision != knowledgeEvidenceDecisionPartial || !knowledgeEvidenceContainsString(selection.MissingAspects, test.missing) {
				t.Fatalf("a narrowed answer may retain its grounded fact only as partial evidence: %#v", selection)
			}
		})
	}
}

func TestParseKnowledgeEvidenceJudgeResponseAcceptsCollectiveMultiSubjectAnswer(t *testing.T) {
	for _, answer := range []string{"是的。", "都是免费的。"} {
		t.Run(answer, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{
				TaskID:    "T1",
				Query:     "矿泉水和饮料都免费吗",
				Objective: "price",
				Entities: []knowledgeEvidenceJudgeEntity{
					{Text: "矿泉水", Type: "supply"},
					{Text: "饮料", Type: "supply"},
				},
				Candidates: []knowledgeEvidenceJudgeCandidate{{
					CandidateID: "T1C1",
					Layer:       knowledgeEvidenceLayerStore,
					Hit:         judgeTestHit(1, 101, "饮品费用", "问题：矿泉水和饮料都免费吗\n答案："+answer, 0.97),
				}},
			}
			raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"矿泉水和饮料都免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

			parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatalf("parse collective multi-subject answer: %v", err)
			}
			if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionDirectSingle {
				t.Fatalf("a bare or collective answer may confirm every named subject: %#v", selection)
			}
		})
	}
}

func TestParseKnowledgeEvidenceJudgeResponseKeepsCollectiveFactWithSingleSubjectDetail(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		objective string
		answer    string
		aspect    string
		statement string
		values    []string
	}{
		{
			name:      "group price with one quantity detail",
			query:     "矿泉水和饮料都免费吗",
			objective: "price",
			answer:    "都是免费的，矿泉水每天补两瓶。",
			aspect:    "price",
			statement: "矿泉水和饮料都免费。",
			values:    []string{"免费"},
		},
		{
			name:      "group existence with one location detail",
			query:     "矿泉水和饮料都有吗",
			objective: "availability",
			answer:    "都有的，矿泉水放在桌上。",
			aspect:    "existence",
			statement: "矿泉水和饮料都有。",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{
				TaskID:    "T1",
				Query:     test.query,
				Objective: test.objective,
				Entities: []knowledgeEvidenceJudgeEntity{
					{Text: "矿泉水", Type: "supply"},
					{Text: "饮料", Type: "supply"},
				},
				Candidates: []knowledgeEvidenceJudgeCandidate{{
					CandidateID: "T1C1",
					Layer:       knowledgeEvidenceLayerStore,
					Hit:         judgeTestHit(1, 101, "饮品信息", "问题："+test.query+"\n答案："+test.answer, 0.97),
				}},
			}
			valuesJSON, err := json.Marshal(test.values)
			if err != nil {
				t.Fatalf("marshal critical values: %v", err)
			}
			raw := fmt.Sprintf(`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":%q,"statement":%q,"criticalValues":%s}],"missingAspects":[]}]}]}`,
				test.aspect, test.statement, valuesJSON)

			parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatalf("parse collective fact with detail: %v", err)
			}
			if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionDirectSingle {
				t.Fatalf("a single-subject detail must not erase the collective fact: %#v", selection)
			}
		})
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsCrossedFAQQuestionAndAnswerSubjects(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水和饮料都免费吗",
		Objective: "price",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "矿泉水", Type: "supply"},
			{Text: "饮料", Type: "supply"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "饮品费用", "问题：饮料免费吗\n答案：矿泉水免费。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"矿泉水免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse crossed FAQ subjects: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("a FAQ answer about another subject must not inherit the question subject: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseCombinesSubjectBoundBareAnswers(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水和饮料都免费吗",
		Objective: "price",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "矿泉水", Type: "supply"},
			{Text: "饮料", Type: "supply"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "矿泉水费用", "问题：矿泉水免费吗\n答案：是的。", 0.97),
			},
			{
				CandidateID: "T1C2",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(2, 102, "饮料费用", "问题：饮料免费吗\n答案：是的。", 0.96),
			},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"矿泉水免费。","criticalValues":["免费"]},{"factId":"T1F2","aspect":"price","statement":"饮料免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse subject-bound bare answers: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("two subject-bound bare answers should jointly cover the task: %#v", selection)
	}
}

func TestStrictExactKnowledgeEvidenceFAQSelectionRejectsNarrowedMultiSubjectAnswer(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水和饮料都免费吗",
		Objective: "price",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "矿泉水", Type: "supply"},
			{Text: "饮料", Type: "supply"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "饮品费用", "问题：矿泉水和饮料都免费吗\n答案：是的，矿泉水免费。", 0.97),
		}},
	}

	if selection, ok := strictExactKnowledgeEvidenceFAQSelection(task, knowledgeEvidenceLayerStore); ok {
		t.Fatalf("exact FAQ fallback must not promote a narrowed answer to direct: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseAcceptsConjoinedDifferentSubjectQuantity(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "房间内两瓶矿泉水是否免费",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "矿泉水数量和费用",
				"问题：房间内两瓶矿泉水是否免费\n答案：房间内有两瓶矿泉水和四瓶饮料，都是免费的。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"房间内有两瓶矿泉水。","criticalValues":["两瓶"]},{"factId":"T1F2","aspect":"price","statement":"房间内矿泉水免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse conjoined quantity response: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionDirectSingle {
		t.Fatalf("an explicitly different drink quantity must not invalidate mineral water: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsQuantityBoundToDifferentSubject(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水有四瓶且免费吗",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "矿泉水数量和费用",
				"问题：矿泉水有四瓶且免费吗\n答案：饮料四瓶，矿泉水免费。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"矿泉水有四瓶。","criticalValues":["四瓶"]},{"factId":"T1F2","aspect":"price","statement":"矿泉水免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse different-subject quantity response: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("a drink quantity must not satisfy the mineral-water quantity slot: %#v", selection)
	}
}

func TestKnowledgeEvidenceIdentityComparisonSupportsBareNames(t *testing.T) {
	if !knowledgeEvidenceIdentityValuesConflict("汤东强。", "李明。") {
		t.Fatal("different bare names must conflict")
	}
	if knowledgeEvidenceIdentityValuesConflict("汤东强。", "汤东强先生。") {
		t.Fatal("a bare name and the same honorific name must match")
	}
	if knowledgeEvidenceIdentityValuesConflict("暂无资料。", "李明。") {
		t.Fatal("an uncertainty answer is not an identity value")
	}
	if knowledgeEvidenceIdentityValuesConflict("老板是暂无资料。", "李明。") {
		t.Fatal("an explicit no-data phrase is not an identity value")
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsConflictingBareIdentityAnswers(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "老板是谁",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "老板信息", "问题：老板是谁\n答案：汤东强。", 0.97)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "老板姓名", "问题：老板叫什么\n答案：李明。", 0.96)},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"identity","statement":"老板是汤东强。","criticalValues":["汤东强"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse conflicting bare identities: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("conflicting bare identity answers must not reach Generate: %#v", selection)
	}
}

func TestKnowledgeEvidenceDeliveryAddressShortVenueConflicts(t *testing.T) {
	if !knowledgeEvidenceDeliveryAddressAnswersConflict("南七店+对应楼层房间号", "东七店+对应楼层房间号") {
		t.Fatal("different short store names must conflict")
	}
	if knowledgeEvidenceDeliveryAddressAnswersConflict("丽斯未来酒店合肥南七店+对应楼层房间号", "南七店+对应楼层房间号") {
		t.Fatal("a full venue name and its contained short name must match")
	}
	if payload, kind := knowledgeEvidenceDeliveryAddressPayload("到店后联系管家"); payload != "" || kind != "" {
		t.Fatalf("generic arrival wording must not become a venue: payload=%q kind=%q", payload, kind)
	}
	if !knowledgeEvidenceDeliveryAddressAnswersConflict("外卖地址填南七店即可", "外卖地址填东七店即可") {
		t.Fatal("common fill/suffix wording must not hide conflicting short store names")
	}
	if !knowledgeEvidenceDeliveryAddressAnswersConflict("外卖地址填南七店即可", "安徽省合肥市蜀山区望江西路123号") {
		t.Fatal("a concrete venue and a different concrete street address must conflict")
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsVenueStreetDeliveryConflict(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "外卖地址怎么填",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "门店地址", "问题：外卖地址怎么填\n答案：南七店+对应楼层房间号。", 0.97)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "街道地址", "问题：收货地址怎么填\n答案：安徽省合肥市蜀山区望江西路123号。", 0.96)},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"location","statement":"外卖地址是南七店。","criticalValues":["南七店"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse venue/street delivery conflict: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("cross-kind concrete delivery destinations must conflict: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsConflictingShortStoreAddresses(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "外卖地址怎么填",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "南七外卖地址", "问题：外卖地址怎么填\n答案：南七店+对应楼层房间号。", 0.97)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "东七外卖地址", "问题：外卖地址怎么填\n答案：东七店+对应楼层房间号。", 0.96)},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"location","statement":"外卖地址填写南七店加对应楼层房间号。","criticalValues":["南七店"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse conflicting short store addresses: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("conflicting short store addresses must not reach Generate: %#v", selection)
	}
}

func TestKnowledgeEvidenceCollectiveAnswerKeepsQuestionSubjectScopeLocal(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水和饮料都免费吗",
		Objective: "price",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "矿泉水", Type: "supply"},
			{Text: "饮料", Type: "supply"},
		},
	}
	for _, answer := range []string{
		"都是免费的，矿泉水每天补两瓶。",
		"是的，都是免费的。",
		"矿泉水和饮料都是免费的。",
	} {
		if !knowledgeEvidenceFAQAnswerCollectivelyCoversTaskSubjects(task, answer) {
			t.Fatalf("a genuine collective answer must cover both task subjects: %q", answer)
		}
	}
	for _, answer := range []string{
		"矿泉水收费，早餐和晚餐都是免费的。",
		"矿泉水收费，都是免费的。",
		"停车位都是免费的。",
	} {
		if knowledgeEvidenceFAQAnswerCollectivelyCoversTaskSubjects(task, answer) {
			t.Fatalf("an unrelated or narrowed collective clause must not expand to all task subjects: %q", answer)
		}
	}
}

func TestKnowledgeEvidenceQuantityGroundingKeepsSubjectValueBindings(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水和饮料分别有几瓶",
		Objective: "quantity",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "矿泉水", Type: "supply"},
			{Text: "饮料", Type: "supply"},
		},
	}
	evidence := []string{"矿泉水有两瓶，饮料有四瓶。"}
	correct := knowledgeEvidenceFact{FactID: "T1F1", Aspect: "quantity", Statement: "矿泉水有两瓶，饮料有四瓶。", CriticalValues: []string{"两瓶", "四瓶"}}
	if !knowledgeEvidenceFactQuantityBindingsGroundedByParts(task, correct, evidence) {
		t.Fatal("matching subject-to-quantity bindings must remain grounded")
	}
	swapped := knowledgeEvidenceFact{FactID: "T1F1", Aspect: "quantity", Statement: "矿泉水有四瓶，饮料有两瓶。", CriticalValues: []string{"四瓶", "两瓶"}}
	if knowledgeEvidenceFactQuantityBindingsGroundedByParts(task, swapped, evidence) {
		t.Fatal("the same subjects and values with swapped ownership must not remain grounded")
	}
}

func TestKnowledgeEvidencePureOpenQuantityRequiresEverySubject(t *testing.T) {
	newTask := func(answer string) knowledgeEvidenceJudgeTask {
		return knowledgeEvidenceJudgeTask{
			TaskID: "T1", Query: "矿泉水和饮料分别有几瓶", Objective: "quantity",
			Entities: []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}, {Text: "饮料", Type: "supply"}},
			Candidates: []knowledgeEvidenceJudgeCandidate{{
				CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(1, 101, "饮品数量", "问题：矿泉水和饮料分别有几瓶\n答案："+answer, 0.97),
			}},
		}
	}
	if knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(newTask("矿泉水有两瓶，饮料有四瓶。"), knowledgeEvidenceLayerStore, []string{"T1C1"}) {
		t.Fatal("a pure open quantity answer covering both subjects must remain complete")
	}
	task := newTask("矿泉水有两瓶，饮料免费。")
	if !knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(task, knowledgeEvidenceLayerStore, []string{"T1C1"}) {
		t.Fatal("a pure open quantity answer must provide a quantity for every requested subject")
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"矿泉水有两瓶。","criticalValues":["两瓶"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse pure open multi-subject quantity: %v", err)
	}
	if selection := parsed["T1"][knowledgeEvidenceLayerStore]; selection.Decision != knowledgeEvidenceDecisionProtocolInvalid {
		t.Fatalf("missing per-subject quantity coverage must not remain direct: %#v", selection)
	}
}

func TestParseAndApplyKnowledgeEvidenceGenericCountUnitCompatibility(t *testing.T) {
	tests := []struct {
		name       string
		answer     string
		statement  string
		wantDirect bool
	}{
		{name: "countable bottle and item", answer: "矿泉水有两瓶，枕头有两个。", statement: "矿泉水有两瓶，枕头有两个。", wantDirect: true},
		{name: "room unit cannot answer generic item count", answer: "矿泉水有两间，枕头有两个。", statement: "矿泉水有两间，枕头有两个。"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hit := judgeTestHit(1, 101, "用品数量", "问题：矿泉水和枕头分别有几个\n答案："+tt.answer, 0.97)
			task := knowledgeEvidenceJudgeTask{
				TaskID: "T1", Query: "矿泉水和枕头分别有几个", Objective: "quantity",
				Entities:   []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}, {Text: "枕头", Type: "supply"}},
				Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
			}
			raw := fmt.Sprintf(`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":%q,"criticalValues":[]}],"missingAspects":[]}]}]}`, tt.statement)
			parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatalf("parse generic-count response: %v", err)
			}
			selection := parsed["T1"][knowledgeEvidenceLayerStore]
			if gotDirect := selection.Decision == knowledgeEvidenceDecisionDirectSingle; gotDirect != tt.wantDirect {
				t.Fatalf("generic-count compatibility mismatch: %#v", selection)
			}

			result := &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: []int64{1}, RawHits: []rag.RetrieveResult{hit}, Hits: []rag.RetrieveResult{hit}, ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content}
			batch := &runtimeKnowledgeRetrieveBatch{Questions: []runtimeKnowledgeQuestionResult{{TaskID: "T1", Query: task.Query, Result: result}}}
			batch.Merged = mergeRuntimeKnowledgeQuestionResults([]int64{1}, result.Options, task.Query, batch.Questions)
			applyKnowledgeEvidenceJudgeOutcome(batch, []knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceJudgeOutcome{Applied: true, Selections: parsed})
			if gotApplied := len(result.EffectiveHits) == 1; gotApplied != tt.wantDirect {
				t.Fatalf("applied generic-count selection mismatch: %#v", result)
			}
		})
	}
}

func TestParseAndApplyKnowledgeEvidenceSingleAspectRequiresEverySameTypeSubject(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		answer     string
		statement  string
		wantDirect bool
	}{
		{name: "other aspect cannot replace beverage price", query: "矿泉水和饮料都免费吗", answer: "矿泉水免费，饮料有四瓶。", statement: "矿泉水免费。"},
		{name: "collective price covers both subjects", query: "矿泉水和饮料都免费吗", answer: "矿泉水和饮料都是免费的。", statement: "矿泉水和饮料都是免费的。", wantDirect: true},
		{name: "elliptical aspect still requires every subject", query: "矿泉水和饮料呢，收费吗", answer: "矿泉水免费，饮料有四瓶。", statement: "矿泉水免费。"},
		{name: "elliptical collective price covers both subjects", query: "矿泉水和饮料呢，收费吗", answer: "矿泉水和饮料都是免费的。", statement: "矿泉水和饮料都是免费的。", wantDirect: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hit := judgeTestHit(1, 101, "饮品费用", "问题："+tt.query+"\n答案："+tt.answer, 0.97)
			task := knowledgeEvidenceJudgeTask{
				TaskID: "T1", Query: tt.query, Objective: "price",
				Entities:   []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}, {Text: "饮料", Type: "supply"}},
				Candidates: []knowledgeEvidenceJudgeCandidate{{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: hit}},
			}
			raw := fmt.Sprintf(`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":%q,"criticalValues":["免费"]}],"missingAspects":[]}]}]}`, tt.statement)
			parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatalf("parse same-type subject response: %v", err)
			}
			selection := parsed["T1"][knowledgeEvidenceLayerStore]
			if gotDirect := selection.Decision == knowledgeEvidenceDecisionDirectSingle; gotDirect != tt.wantDirect {
				t.Fatalf("single-aspect subject completeness mismatch: %#v", selection)
			}

			result := &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: []int64{1}, RawHits: []rag.RetrieveResult{hit}, Hits: []rag.RetrieveResult{hit}, ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content}
			batch := &runtimeKnowledgeRetrieveBatch{Questions: []runtimeKnowledgeQuestionResult{{TaskID: "T1", Query: task.Query, Result: result}}}
			batch.Merged = mergeRuntimeKnowledgeQuestionResults([]int64{1}, result.Options, task.Query, batch.Questions)
			applyKnowledgeEvidenceJudgeOutcome(batch, []knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceJudgeOutcome{Applied: true, Selections: parsed})
			if gotApplied := len(result.EffectiveHits) == 1; gotApplied != tt.wantDirect {
				t.Fatalf("applied single-aspect selection mismatch: %#v", result)
			}
		})
	}
}

func TestKnowledgeEvidenceCombinedQuantityTotalRequiresExactEvidence(t *testing.T) {
	newTask := func(query string, answer string) knowledgeEvidenceJudgeTask {
		return knowledgeEvidenceJudgeTask{
			TaskID:    "T1",
			Query:     query,
			Objective: "quantity",
			Entities: []knowledgeEvidenceJudgeEntity{
				{Text: "矿泉水", Type: "supply"},
				{Text: "饮料", Type: "supply"},
			},
			Candidates: []knowledgeEvidenceJudgeCandidate{{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "饮品数量", "问题：矿泉水和饮料分别有几瓶\n答案："+answer, 0.97),
			}},
		}
	}
	selected := []string{"T1C1"}
	matching := newTask("矿泉水和饮料一共六瓶吗", "矿泉水有两瓶，饮料有四瓶。")
	if knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(matching, knowledgeEvidenceLayerStore, selected) {
		t.Fatal("two plus four bottles must support an exact six-bottle total")
	}
	wrongSum := newTask("矿泉水和饮料一共六瓶吗", "矿泉水有两瓶，饮料有两瓶。")
	if !knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(wrongSum, knowledgeEvidenceLayerStore, selected) {
		t.Fatal("a four-bottle sum must conflict with a six-bottle total")
	}
	explicitConflict := newTask("矿泉水和饮料一共六瓶吗", "矿泉水有两瓶，饮料有四瓶，总共五瓶。")
	if !knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(explicitConflict, knowledgeEvidenceLayerStore, selected) {
		t.Fatal("an explicit conflicting total must override a coincidentally matching item sum")
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsPriceOnlyDirectForFixedQuantityQuestion(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "房间内两瓶矿泉水是否都免费",
		Objective: "compound_information",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "矿泉水费用", "问题：房间内矿泉水免费吗\n答案：是的，免费的。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"房间内矿泉水免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse price-only fixed-quantity response: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("price evidence alone must not complete a fixed-quantity question: %#v", selection)
	}
}

func TestKnowledgeEvidenceTimeCompletenessBindsEachRequestedCondition(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "工作日和周末早餐分别几点开始",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "service"}},
	}
	workdayOnly := []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "time", Statement: "工作日早餐7点开始。", CriticalValues: []string{"7点"}}}
	missing := missingRequiredKnowledgeEvidenceAspects(task, workdayOnly)
	if !knowledgeEvidenceContainsString(missing, "周末早餐开始时间") {
		t.Fatalf("a workday-only fact must leave the weekend slot missing: %#v", missing)
	}
	complete := append(workdayOnly, knowledgeEvidenceFact{FactID: "T1F2", Aspect: "time", Statement: "周末早餐8点开始。", CriticalValues: []string{"8点"}})
	if missing = missingRequiredKnowledgeEvidenceAspects(task, complete); len(missing) != 0 {
		t.Fatalf("both requested calendar conditions must close the time task: %#v", missing)
	}
}

func TestKnowledgeEvidenceTimeCompletenessPreservesRequestedSubjectConditionPairs(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "工作日早餐几点，周末晚餐几点",
		Objective: "time",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "早餐", Type: "meal"},
			{Text: "晚餐", Type: "meal"},
		},
	}
	workdayBreakfast := knowledgeEvidenceFact{FactID: "T1F1", Aspect: "time", Statement: "工作日早餐7点。", CriticalValues: []string{"7点"}}
	missing := missingRequiredKnowledgeEvidenceAspects(task, []knowledgeEvidenceFact{workdayBreakfast})
	if !knowledgeEvidenceContainsString(missing, "周末晚餐时间") {
		t.Fatalf("the actual second subject-condition pair must remain missing: %#v", missing)
	}
	if knowledgeEvidenceContainsString(missing, "周末早餐时间") || knowledgeEvidenceContainsString(missing, "工作日晚餐时间") {
		t.Fatalf("time requirements must not invent cartesian subject-condition pairs: %#v", missing)
	}
	complete := append([]knowledgeEvidenceFact{workdayBreakfast}, knowledgeEvidenceFact{
		FactID: "T1F2", Aspect: "time", Statement: "周末晚餐18点。", CriticalValues: []string{"18点"},
	})
	if missing = missingRequiredKnowledgeEvidenceAspects(task, complete); len(missing) != 0 {
		t.Fatalf("the two requested subject-condition pairs must close the task: %#v", missing)
	}

	slotTask := task
	slotTask.Query = "工作日早餐几点开始，周末晚餐几点结束"
	slotFacts := []knowledgeEvidenceFact{
		{FactID: "T2F1", Aspect: "time", Statement: "工作日早餐7点开始。", CriticalValues: []string{"7点"}},
		{FactID: "T2F2", Aspect: "time", Statement: "周末晚餐20点结束。", CriticalValues: []string{"20点"}},
	}
	if missing = missingRequiredKnowledgeEvidenceAspects(slotTask, slotFacts); len(missing) != 0 {
		t.Fatalf("each clause must retain its own subject, condition, and slot instead of forming a cartesian product: %#v", missing)
	}
}

func TestKnowledgeEvidenceIdentityTrimsTrailingDescriptions(t *testing.T) {
	if knowledgeEvidenceIdentityValuesConflict("老板是汤东强负责门店运营。", "董事长为汤东强主管战略。") {
		t.Fatal("the same name with trailing role descriptions must not conflict")
	}
	if !knowledgeEvidenceIdentityValuesConflict("老板是汤东强负责门店运营。", "董事长为李明负责门店运营。") {
		t.Fatal("different names must still conflict after trimming shared descriptions")
	}
	if knowledgeEvidenceIdentityValuesConflict("目前汤东强是老板。", "老板是汤东强。") {
		t.Fatal("context prefixes must not become part of the person's name")
	}
}

func TestKnowledgeEvidenceNightRangeHandlesImplicitCrossMidnightEnd(t *testing.T) {
	tests := []struct {
		text      string
		wantStart string
		wantEnd   string
	}{
		{text: "晚上8点到2点", wantStart: "20:00", wantEnd: "02:00"},
		{text: "晚上8点到9点", wantStart: "20:00", wantEnd: "21:00"},
		{text: "晚上10点到12点", wantStart: "22:00", wantEnd: "00:00"},
		{text: "下午2点到5点", wantStart: "14:00", wantEnd: "17:00"},
		{text: "晚上10点到次日2点", wantStart: "22:00", wantEnd: "02:00"},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			values := knowledgeEvidenceTimeSlotValues("", test.text)
			if values["start"] != test.wantStart || values["end"] != test.wantEnd {
				t.Fatalf("unexpected normalized range: %#v", values)
			}
		})
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRequiresEveryConditionForGenericScheduleQuestion(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "工作日和周末早餐时间分别是多少",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "service"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "工作日早餐时间", "问题：工作日早餐时间是多少\n答案：工作日早餐时间是7点。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"工作日早餐时间是7点。","criticalValues":["7点"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse generic conditioned schedule: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("workday-only evidence must not complete a workday-and-weekend schedule question: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseDoesNotInheritQuestionSubjectsFromUnrelatedAnswer(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水和饮料都免费吗",
		Objective: "price",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "矿泉水", Type: "supply"},
			{Text: "饮料", Type: "supply"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "饮品费用", "问题：矿泉水和饮料都免费吗\n答案：停车位免费。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"矿泉水和饮料都免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse unrelated answer subject inheritance: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("an unrelated parking answer must not inherit mineral-water and beverage subjects from the FAQ question: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsSingleSubjectPriceBorrowedFromAnotherFAQ(t *testing.T) {
	for _, answer := range []string{"是的，免费。", "是的，不收费。", "是的，无需付费。"} {
		t.Run(answer, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{
				TaskID:    "T1",
				Query:     "停车免费吗",
				Objective: "price",
				Entities:  []knowledgeEvidenceJudgeEntity{{Text: "停车", Type: "facility"}},
				Candidates: []knowledgeEvidenceJudgeCandidate{{
					CandidateID: "T1C1",
					Layer:       knowledgeEvidenceLayerStore,
					Hit:         judgeTestHit(1, 101, "早餐费用", "问题：早餐免费吗\n答案："+answer, 0.97),
				}},
			}
			raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"停车免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

			parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatalf("parse cross-subject price fact: %v", err)
			}
			selection := parsed["T1"][knowledgeEvidenceLayerStore]
			if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
				t.Fatalf("a breakfast price answer must not support parking, even through a price synonym: %#v", selection)
			}
		})
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsPriceBorrowedFromAnotherFAQWithoutEntities(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "停车免费吗",
		Objective: "price",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "早餐费用", "问题：早餐免费吗\n答案：是的，免费。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"停车免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse entity-free cross-subject price fact: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("a breakfast price answer must not support parking when Intent omitted entities: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsSingleSubjectTimeBorrowedFromAnotherFAQ(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "早餐几点开始",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "service"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "晚餐时间", "问题：晚餐几点开始\n答案：晚上六点。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"早餐晚上六点开始。","criticalValues":["晚上六点"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse cross-subject time fact: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("a dinner clock value must not support the breakfast task: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsNaturalTimeQuestionBorrowedFromAnotherFAQWithoutEntities(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "早餐开到多晚",
		Objective: "time",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "健身房时间", "问题：健身房几点关门\n答案：晚上十点。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"早餐营业到晚上十点。","criticalValues":["晚上十点"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse entity-free natural time fact: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("a fitness-room closing time must not support a natural breakfast time question without entities: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRejectsSingleSubjectQuantityBorrowedFromAnotherSubject(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "矿泉水有几瓶",
		Objective: "quantity",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "饮品信息", "问题：房间饮品怎么配置\n答案：饮料有四瓶，矿泉水免费。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"矿泉水有四瓶。","criticalValues":["四瓶"]}],"missingAspects":[]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse borrowed single-subject quantity: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("a beverage quantity must not complete the mineral-water quantity task: %#v", selection)
	}
}

func TestKnowledgeEvidenceGroundingRejectsSwappedMultiSubjectCriticalValues(t *testing.T) {
	tests := []struct {
		name      string
		task      knowledgeEvidenceJudgeTask
		evidence  string
		facts     []knowledgeEvidenceFact
		candidate string
		raw       string
	}{
		{
			name: "price",
			task: knowledgeEvidenceJudgeTask{
				TaskID: "T1", Query: "矿泉水和饮料分别怎么收费", Objective: "price",
				Entities: []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}, {Text: "饮料", Type: "supply"}},
			},
			evidence:  "矿泉水免费，饮料收费。",
			facts:     []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "price", Statement: "矿泉水收费。", CriticalValues: []string{"收费"}}, {FactID: "T1F2", Aspect: "price", Statement: "饮料免费。", CriticalValues: []string{"免费"}}},
			candidate: "问题：矿泉水和饮料分别怎么收费\n答案：矿泉水免费，饮料收费。",
			raw:       `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"矿泉水收费。","criticalValues":["收费"]},{"factId":"T1F2","aspect":"price","statement":"饮料免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`,
		},
		{
			name: "time",
			task: knowledgeEvidenceJudgeTask{
				TaskID: "T1", Query: "早餐和退房时间分别是多少", Objective: "time",
				Entities: []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "service"}, {Text: "退房", Type: "service"}},
			},
			evidence:  "早餐7点开始，退房12点。",
			facts:     []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "time", Statement: "早餐12点。", CriticalValues: []string{"12点"}}, {FactID: "T1F2", Aspect: "time", Statement: "退房7点开始。", CriticalValues: []string{"7点"}}},
			candidate: "问题：早餐和退房时间分别是多少\n答案：早餐7点开始，退房12点。",
			raw:       `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"time","statement":"早餐12点。","criticalValues":["12点"]},{"factId":"T1F2","aspect":"time","statement":"退房7点开始。","criticalValues":["7点"]}],"missingAspects":[]}]}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, fact := range test.facts {
				if knowledgeEvidenceFactGroundedForTask(test.task, fact, []string{test.evidence}) {
					t.Errorf("swapped %s fact must not be grounded by values owned by another subject: %#v", test.name, fact)
				}
			}
			test.task.Candidates = []knowledgeEvidenceJudgeCandidate{{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "subject-bound "+test.name, test.candidate, 0.97),
			}}
			parsed, err := parseKnowledgeEvidenceJudgeResponse(test.raw, []knowledgeEvidenceJudgeTask{test.task})
			if err != nil {
				t.Fatalf("parse swapped %s facts: %v", test.name, err)
			}
			selection := parsed["T1"][knowledgeEvidenceLayerStore]
			if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
				for _, fact := range selection.SupportedFacts {
					if !knowledgeEvidenceFactGroundedForTask(test.task, fact, []string{test.evidence}) {
						t.Errorf("a direct %s repair must contain only correctly bound facts: %#v", test.name, selection)
					}
				}
			}
		})
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRequiresFixedQuantityForConfirmStatusAndPolicy(t *testing.T) {
	for _, objective := range []string{"confirm", "status", "policy"} {
		t.Run(objective, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{
				TaskID:    "T1",
				Query:     "房间明确配置两瓶矿泉水",
				Objective: objective,
				Entities:  []knowledgeEvidenceJudgeEntity{{Text: "矿泉水", Type: "supply"}},
				Candidates: []knowledgeEvidenceJudgeCandidate{{
					CandidateID: "T1C1",
					Layer:       knowledgeEvidenceLayerStore,
					Hit:         judgeTestHit(1, 101, "矿泉水配置", "问题：房间有矿泉水吗\n答案：房间有矿泉水。", 0.97),
				}},
			}
			raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"房间有矿泉水。","criticalValues":["矿泉水"]}],"missingAspects":[]}]}]}`

			parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatalf("parse fixed quantity for %s objective: %v", objective, err)
			}
			selection := parsed["T1"][knowledgeEvidenceLayerStore]
			if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
				t.Fatalf("%s must preserve the explicit two-bottle quantity before becoming direct: %#v", objective, selection)
			}
		})
	}
}

func TestKnowledgeEvidenceConditionalScheduleConflictMatchesConditionAndSlot(t *testing.T) {
	tests := []struct {
		name           string
		question       string
		answer         string
		wantConflict   bool
		wantComparable bool
	}{
		{name: "workday start conflict", question: "工作日早餐几点开始", answer: "工作日早餐8点开始。", wantConflict: true, wantComparable: true},
		{name: "weekend start match", question: "周末早餐几点开始", answer: "周末早餐8点开始。", wantComparable: true},
		{name: "weekend start conflict", question: "周末早餐几点开始", answer: "周末早餐9点开始。", wantConflict: true, wantComparable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conflict, comparable := knowledgeEvidenceTimeSlotAnswersConflict(
				"工作日和周末早餐时间分别是多少",
				"工作日早餐7点开始，周末早餐8点开始。",
				test.question,
				test.answer,
			)
			if conflict != test.wantConflict || comparable != test.wantComparable {
				t.Fatalf("condition-and-slot conflict mismatch: conflict=%v comparable=%v", conflict, comparable)
			}
		})
	}
}

func TestKnowledgeEvidenceIdentityComparisonIgnoresCombinedContextPrefix(t *testing.T) {
	if knowledgeEvidenceIdentityValuesConflict("目前本店由汤东强是老板。", "老板是汤东强。") {
		t.Fatal("a combined context prefix must not turn the same owner identity into a conflict")
	}
}

func TestKnowledgeEvidenceCompleteSubjectGroupAliasRequiresClauseLocalAspect(t *testing.T) {
	comparisonTask := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "携程和美团的价格一样吗",
		Objective: "price",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "携程", Type: "company"},
			{Text: "美团", Type: "company"},
		},
	}
	if !knowledgeEvidenceFAQAnswerCollectivelyCoversTaskSubjects(comparisonTask, "不同平台的权益不一样，建议对比价格后选择。") {
		t.Fatal("a clause-local platform comparison must retain complete subject-group inheritance")
	}

	freeTask := comparisonTask
	freeTask.Query = "携程和美团都免费吗"
	freeTask.Candidates = []knowledgeEvidenceJudgeCandidate{{
		CandidateID: "T1C1",
		Layer:       knowledgeEvidenceLayerStore,
		Hit:         judgeTestHit(1, 101, "平台费用", "问题：携程和美团都免费吗\n答案：不同平台权益不一样，停车位免费。", 0.97),
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"携程和美团都免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{freeTask})
	if err != nil {
		t.Fatalf("parse unrelated alias answer: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("a group alias in one clause must not borrow another subject's price from a later clause: %#v", selection)
	}

	relativeTask := freeTask
	relativeTask.Candidates = []knowledgeEvidenceJudgeCandidate{{
		CandidateID: "T1C1",
		Layer:       knowledgeEvidenceLayerStore,
		Hit:         judgeTestHit(1, 102, "平台费用", "问题：携程和美团都免费吗\n答案：不同平台价格不一样，停车位免费。", 0.97),
	}}
	parsed, err = parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{relativeTask})
	if err != nil {
		t.Fatalf("parse relative platform price answer: %v", err)
	}
	selection = parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("a relative platform-price comparison must not prove an absolute free predicate: %#v", selection)
	}
}

func TestKnowledgeEvidenceSingleTimeConditionBindsRequestedSchedule(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "周末早餐几点开始",
		Objective: "time",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "service"}},
	}
	wrongCondition := []knowledgeEvidenceFact{{
		FactID: "T1F1", Aspect: "time", Statement: "工作日早餐7点开始。", CriticalValues: []string{"7点"},
	}}
	missing := missingRequiredKnowledgeEvidenceAspects(task, wrongCondition)
	if !knowledgeEvidenceContainsString(missing, "周末早餐开始时间") {
		t.Fatalf("a workday schedule must not complete a weekend-only question: %#v", missing)
	}
	matching := []knowledgeEvidenceFact{{
		FactID: "T1F1", Aspect: "time", Statement: "星期六和星期日早餐8点开始。", CriticalValues: []string{"8点"},
	}}
	if missing = missingRequiredKnowledgeEvidenceAspects(task, matching); len(missing) != 0 {
		t.Fatalf("an equivalent weekend expression must satisfy the requested condition: %#v", missing)
	}
}

func TestKnowledgeEvidenceCalendarConditionCommonExpressions(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{text: "周一到周五早餐7点开始", want: "workday"},
		{text: "周一至周五早餐7点开始", want: "workday"},
		{text: "星期一到星期五早餐7点开始", want: "workday"},
		{text: "星期一至星期五早餐7点开始", want: "workday"},
		{text: "周六日早餐8点开始", want: "weekend"},
		{text: "周六和周日早餐8点开始", want: "weekend"},
		{text: "星期六和星期日早餐8点开始", want: "weekend"},
		{text: "星期六到星期日早餐8点开始", want: "weekend"},
		{text: "法定假日早餐9点开始", want: "holiday"},
		{text: "平时早餐7点开始", want: "workday"},
		{text: "星期三早餐7点开始", want: "weekday:wednesday"},
		{text: "礼拜五早餐7点开始", want: "weekday:friday"},
		{text: "双休早餐8点开始", want: "weekend"},
		{text: "礼拜天早餐8点开始", want: "weekday:sunday"},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			conditions := requiredKnowledgeEvidenceTimeConditions(test.text)
			if len(conditions) != 1 || conditions[0] != test.want {
				t.Fatalf("unexpected normalized condition: %#v", conditions)
			}
		})
	}
}

func TestKnowledgeEvidenceStandaloneWeekdayConditionDoesNotCrossWeekend(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "礼拜天早餐几点开始", Objective: "time",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "service"}},
	}
	wrong := []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "time", Statement: "星期三早餐7点开始。", CriticalValues: []string{"7点"}}}
	if missing := missingRequiredKnowledgeEvidenceAspects(task, wrong); !knowledgeEvidenceContainsString(missing, "周日早餐开始时间") {
		t.Fatalf("a standalone workday must not satisfy a standalone weekend query: %#v", missing)
	}
	matching := []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "time", Statement: "星期日早餐8点开始。", CriticalValues: []string{"8点"}}}
	if missing := missingRequiredKnowledgeEvidenceAspects(task, matching); len(missing) != 0 {
		t.Fatalf("equivalent standalone weekend forms must match: %#v", missing)
	}
}

func TestKnowledgeEvidenceStandaloneWeekdaysRemainDistinct(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "周一早餐几点开始", Objective: "time",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "service"}},
	}
	wrong := []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "time", Statement: "周五早餐7点开始。", CriticalValues: []string{"7点"}}}
	if missing := missingRequiredKnowledgeEvidenceAspects(task, wrong); !knowledgeEvidenceContainsString(missing, "周一早餐开始时间") {
		t.Fatalf("a Friday schedule must not satisfy a Monday question: %#v", missing)
	}
	matching := []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "time", Statement: "星期一早餐8点开始。", CriticalValues: []string{"8点"}}}
	if missing := missingRequiredKnowledgeEvidenceAspects(task, matching); len(missing) != 0 {
		t.Fatalf("equivalent Monday expressions must match: %#v", missing)
	}
}

func TestKnowledgeEvidenceMixedFixedAndOpenQuantityBindings(t *testing.T) {
	newTask := func(answer string) knowledgeEvidenceJudgeTask {
		return knowledgeEvidenceJudgeTask{
			TaskID:    "T1",
			Query:     "矿泉水两瓶，饮料有几瓶",
			Objective: "quantity",
			Entities: []knowledgeEvidenceJudgeEntity{
				{Text: "矿泉水", Type: "supply"},
				{Text: "饮料", Type: "supply"},
			},
			Candidates: []knowledgeEvidenceJudgeCandidate{{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 101, "饮品数量", "问题：房间饮品有几瓶\n答案："+answer, 0.97),
			}},
		}
	}
	selected := []string{"T1C1"}
	complete := newTask("矿泉水有两瓶，饮料有四瓶。")
	if knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(complete, knowledgeEvidenceLayerStore, selected) {
		t.Fatal("the fixed two-bottle value and an open four-bottle value must both be accepted")
	}
	missingOpen := newTask("矿泉水有两瓶。")
	if !knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(missingOpen, knowledgeEvidenceLayerStore, selected) {
		t.Fatal("a selected answer without any beverage bottle count must remain incomplete")
	}
	wrongFixed := newTask("矿泉水有三瓶，饮料有四瓶。")
	if !knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(wrongFixed, knowledgeEvidenceLayerStore, selected) {
		t.Fatal("an open quantity must not hide a conflicting fixed mineral-water count")
	}
}

func TestKnowledgeEvidenceJudgeTimeoutWithinRemainingReservesParentDeadline(t *testing.T) {
	tests := []struct {
		name       string
		configured time.Duration
		remaining  time.Duration
		want       time.Duration
		wantOK     bool
	}{
		{name: "long parent keeps configured timeout", configured: 28 * time.Second, remaining: 60 * time.Second, want: 28 * time.Second, wantOK: true},
		{name: "thirty second parent keeps twelve second downstream reserve", configured: 28 * time.Second, remaining: 30 * time.Second, want: 18 * time.Second, wantOK: true},
		{name: "short parent trims judge", configured: 28 * time.Second, remaining: 25 * time.Second, want: 13 * time.Second, wantOK: true},
		{name: "configured timeout remains lower", configured: 15 * time.Second, remaining: 40 * time.Second, want: 15 * time.Second, wantOK: true},
		{name: "one second judge budget remains", configured: 15 * time.Second, remaining: 13 * time.Second, want: time.Second, wantOK: true},
		{name: "no stage budget remains", configured: 15 * time.Second, remaining: 12 * time.Second, wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := knowledgeEvidenceJudgeTimeoutWithinRemaining(test.configured, test.remaining)
			if ok != test.wantOK || got != test.want {
				t.Fatalf("timeout budget mismatch: got=%s ok=%v want=%s ok=%v", got, ok, test.want, test.wantOK)
			}
		})
	}

	configured := 28 * time.Second
	got, ok := knowledgeEvidenceJudgeTimeoutWithinParent(context.Background(), configured)
	if !ok || got != configured {
		t.Fatalf("a context without a parent deadline must keep the configured timeout: got=%s ok=%v", got, ok)
	}
}

func TestKnowledgeEvidencePriceClaimsSeparatePolarityRelationAndPolicy(t *testing.T) {
	tests := []struct {
		name           string
		text           string
		wantClaims     []string
		unwantedClaims []string
		comparison     bool
	}{
		{name: "negative free status", text: "不同平台都不免费。", wantClaims: []string{"charged"}, unwantedClaims: []string{"free", "not_equal"}},
		{name: "free status", text: "不同平台都不收费。", wantClaims: []string{"free"}, unwantedClaims: []string{"charged", "not_equal"}},
		{name: "no charge required", text: "矿泉水无需收费。", wantClaims: []string{"free"}, unwantedClaims: []string{"charged"}},
		{name: "does not need charge", text: "矿泉水不需要收费。", wantClaims: []string{"free"}, unwantedClaims: []string{"charged"}},
		{name: "no payment required", text: "矿泉水不用付费。", wantClaims: []string{"free"}, unwantedClaims: []string{"charged"}},
		{name: "explicit price relation", text: "不同平台价格不一样。", wantClaims: []string{"not_equal"}, comparison: true},
		{name: "relative free policy", text: "不同平台免费政策不一样。", wantClaims: []string{"dynamic"}, unwantedClaims: []string{"free", "charged", "not_equal"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := knowledgeEvidencePriceClaims(test.text)
			for _, kind := range test.wantClaims {
				if !knowledgeEvidencePriceClaimsContain(claims, kind) {
					t.Fatalf("missing %s claim in %#v", kind, claims)
				}
			}
			for _, kind := range test.unwantedClaims {
				if knowledgeEvidencePriceClaimsContain(claims, kind) {
					t.Fatalf("unexpected %s claim in %#v", kind, claims)
				}
			}
			if got := knowledgeEvidenceQueryAsksComparison(test.text); got != test.comparison {
				t.Fatalf("comparison mismatch: got=%v want=%v", got, test.comparison)
			}
		})
	}
}

func TestKnowledgeEvidencePriceGroundingAcceptsEquivalentFreePhrases(t *testing.T) {
	fact := knowledgeEvidenceFact{
		FactID: "T1F1", Aspect: "price", Statement: "矿泉水免费。", CriticalValues: []string{"免费"},
	}
	for _, evidence := range []string{"矿泉水不收费。", "矿泉水无需收费。", "矿泉水不需要付费。", "矿泉水不用付费。"} {
		t.Run(evidence, func(t *testing.T) {
			if !knowledgeEvidenceFactGroundedByText(fact, []string{evidence}) {
				t.Fatalf("equivalent free wording must ground the canonical free fact: %q", evidence)
			}
		})
	}
	if got := knowledgeEvidencePriceCriticalValues("矿泉水无需收费。"); len(got) != 1 || got[0] != "免费" {
		t.Fatalf("free critical values must be canonical and customer-safe: %#v", got)
	}
}

func TestKnowledgeEvidenceQuantityRequestParametersStaySeparateFromFacts(t *testing.T) {
	for _, query := range []string{
		"我要两瓶矿泉水",
		"我需要两条浴巾",
		"能不能送两瓶水",
		"帮我拿两瓶水，是否可以送到房间",
	} {
		if got := knowledgeEvidenceTaskBoundCriticalValues(query); len(got) != 0 {
			t.Fatalf("a service request quantity must not become a knowledge fact: query=%q values=%#v", query, got)
		}
	}

	for _, query := range []string{
		"你们送两瓶矿泉水都免费吗",
		"每个房间送两瓶水吗",
		"原来两瓶矿泉水都免费吗",
	} {
		got := knowledgeEvidenceTaskBoundCriticalValues(query)
		if !knowledgeEvidenceContainsString(got, "两瓶") {
			t.Fatalf("a factual quantity must remain a required value: query=%q values=%#v", query, got)
		}
	}
}

func TestParseKnowledgeEvidenceJudgeResponseDoesNotReverseNegativePriceEvidence(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "携程和美团都免费吗", Objective: "price",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "携程", Type: "company"}, {Text: "美团", Type: "company"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "平台费用", "问题：携程和美团都免费吗\n答案：不同平台都不免费。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"携程和美团都免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse negative price evidence: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	for _, fact := range selection.SupportedFacts {
		claims := knowledgeEvidencePriceClaims(fact.Statement + " " + strings.Join(fact.CriticalValues, " "))
		if knowledgeEvidencePriceClaimsContain(claims, "free") && !knowledgeEvidencePriceClaimsContain(claims, "charged") {
			t.Fatalf("negative evidence must never become a positive free fact: %#v", selection)
		}
	}
}

func TestParseKnowledgeEvidenceJudgeResponseDoesNotUseRelativePolicyAsAbsolutePrice(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "携程和美团都免费吗", Objective: "price",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "携程", Type: "company"}, {Text: "美团", Type: "company"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "平台政策", "问题：携程和美团都免费吗\n答案：不同平台免费政策不一样。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"携程和美团都免费。","criticalValues":["免费"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse relative price policy: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("a relative policy must not complete an absolute free-status question: %#v", selection)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseDoesNotTreatChargedGroupAsPriceDifference(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "携程和美团价格一样吗", Objective: "price",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "携程", Type: "company"}, {Text: "美团", Type: "company"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "平台费用", "问题：携程和美团价格一样吗\n答案：不同平台都不免费。", 0.97),
		}},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"price","statement":"携程和美团价格不一样。","criticalValues":["价格不同"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse non-comparison price evidence: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision == knowledgeEvidenceDecisionDirectSingle || selection.Decision == knowledgeEvidenceDecisionDirectCombined {
		t.Fatalf("a shared charged status does not prove equal or different prices: %#v", selection)
	}
}

func TestKnowledgeEvidenceConditionalAffirmationKeepsBindingQualifier(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "可以开发票吗", Objective: "availability",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "发票", Type: "service"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore,
			Hit: judgeTestHit(1, 101, "发票", "问题：可以开发票吗\n答案：是的，仅限退房前办理。", 0.97),
		}},
	}
	statement, ok := resolvedKnowledgeEvidenceFAQQuestionStatement(task, "可以开发票吗", "是的，仅限退房前办理。")
	if !ok || !strings.Contains(statement, "可以开发票") || !strings.Contains(statement, "退房前") {
		t.Fatalf("conditional affirmation must resolve with its restriction: statement=%q ok=%v", statement, ok)
	}
	unconditional := knowledgeEvidenceFact{FactID: "T1F1", Aspect: "existence", Statement: "可以开发票。", CriticalValues: []string{"发票"}}
	parts := knowledgeEvidenceCandidateGroundingParts(task, task.Candidates[0])
	if knowledgeEvidenceFactGroundedForTask(task, unconditional, parts) {
		t.Fatalf("an unconditional capability must not ground against qualified evidence: parts=%#v", parts)
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"T1F1","aspect":"existence","statement":"可以开发票。","criticalValues":["发票"]}],"missingAspects":[]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatalf("parse conditional affirmation: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	for _, fact := range selection.SupportedFacts {
		if strings.Contains(fact.Statement, "可以开发票") && !strings.Contains(fact.Statement, "退房前") {
			t.Fatalf("an unconditional capability must not survive a qualified FAQ: %#v", selection)
		}
	}
}

func TestKnowledgeEvidenceGroundingScopesQualifiersToStrongPropositionBoundaries(t *testing.T) {
	airConditioningFact := knowledgeEvidenceFact{Aspect: "existence", Statement: "房间配有空调。", CriticalValues: []string{"空调"}}
	parts := knowledgeEvidenceGroundingPartsCompatibleWithFact(
		airConditioningFact,
		[]string{"房间配有空调。发票仅限退房后申请。"},
	)
	if !knowledgeEvidenceContainsString(parts, "房间配有空调") {
		t.Fatalf("a qualifier in a separate sentence must not contaminate the air-conditioning fact: %#v", parts)
	}

	unqualifiedFact := knowledgeEvidenceFact{Aspect: "method", Statement: "可以办理。"}
	parts = knowledgeEvidenceGroundingPartsCompatibleWithFact(unqualifiedFact, []string{"可以办理，仅限退房前。"})
	if len(parts) != 0 {
		t.Fatalf("a comma-tail qualifier must remain bound to the preceding conclusion: %#v", parts)
	}
}

func TestKnowledgeEvidenceBindingQualifiersCannotBeInventedOrReplaced(t *testing.T) {
	tests := []struct {
		name     string
		evidence string
		fact     string
		want     bool
	}{
		{name: "matching checkout boundary", evidence: "仅限退房前办理。", fact: "发票仅限退房前办理。", want: true},
		{name: "checkin day cannot become checkout day", evidence: "入住当天早餐7点开始。", fact: "退房当天早餐7点开始。", want: false},
		{name: "unqualified evidence cannot invent workday", evidence: "早餐7点开始。", fact: "仅工作日早餐7点开始。", want: false},
		{name: "room type restriction cannot become membership", evidence: "仅限指定房型使用。", fact: "仅限会员使用。", want: false},
		{name: "named room type restrictions remain distinct", evidence: "仅限合柴房型使用。", fact: "仅限麦田房型使用。", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fact := knowledgeEvidenceFact{Aspect: "condition", Statement: test.fact}
			if got := knowledgeEvidenceFactPreservesBindingQualifiers(fact, test.evidence); got != test.want {
				t.Fatalf("binding qualifier mismatch: got=%v want=%v evidence=%q fact=%q required=%#v actual=%#v", got, test.want, test.evidence, test.fact,
					knowledgeEvidenceBindingQualifierSignatures(test.evidence), knowledgeEvidenceBindingQualifierSignatures(test.fact))
			}
		})
	}
}

func TestKnowledgeEvidenceGenericScheduleBindsConditionAndDetectsConflict(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Query: "周末早餐时间是多少", Objective: "time",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "早餐", Type: "service"}},
	}
	wrongCondition := []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "time", Statement: "工作日早餐时间是7点。", CriticalValues: []string{"7点"}}}
	if missing := missingRequiredKnowledgeEvidenceAspects(task, wrongCondition); !knowledgeEvidenceContainsString(missing, "周末早餐时间") {
		t.Fatalf("a workday generic schedule must not complete a weekend task: %#v", missing)
	}
	matching := []knowledgeEvidenceFact{{FactID: "T1F1", Aspect: "time", Statement: "周末早餐时间是8点。", CriticalValues: []string{"8点"}}}
	if missing := missingRequiredKnowledgeEvidenceAspects(task, matching); len(missing) != 0 {
		t.Fatalf("a matching weekend generic schedule must complete the task: %#v", missing)
	}
	conflict, comparable := knowledgeEvidenceTimeSlotAnswersConflict("早餐时间是多少", "早餐时间是7点。", "早餐时间是多少", "早餐时间是8点。")
	if !conflict || !comparable {
		t.Fatalf("two different generic schedules must conflict: conflict=%v comparable=%v", conflict, comparable)
	}
	conflict, comparable = knowledgeEvidenceTimeSlotAnswersConflict("工作日早餐时间是多少", "工作日早餐时间是7点。", "周末早餐时间是多少", "周末早餐时间是8点。")
	if conflict || comparable {
		t.Fatalf("different calendar conditions must remain incomparable: conflict=%v comparable=%v", conflict, comparable)
	}
}

func TestParseKnowledgeEvidenceJudgeResponseRepairsGenericExistenceFromAffirmativeSubtypeFAQ(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{{
		TaskID:    "T1",
		Intent:    "hotel_info",
		Query:     "有拖鞋吗",
		SubIntent: "availability",
		Objective: "availability",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(
					1,
					101,
					"一次性拖鞋",
					"问题：房间里有一次性拖鞋吗？\n答案：酒店房间内均配备一次性拖鞋，若有额外需求，可前往1313对面洗衣房领取。",
					0.8462,
				),
			},
			{
				CandidateID: "T1C2",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 102, "洗澡用拖鞋", "问题：酒店提供洗澡用拖鞋吗？\n答案：酒店不提供洗澡用拖鞋。", 0.8442),
			},
		},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_single","selectedCandidateIds":["T1C1"]}]}]}`

	parsed, err := parseKnowledgeEvidenceJudgeResponse(raw, tasks)
	if err != nil {
		t.Fatalf("parse selected subtype FAQ: %v", err)
	}
	selection := parsed["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SelectedCandidateIDs) != 1 || selection.SelectedCandidateIDs[0] != "T1C1" {
		t.Fatalf("an affirmative subtype must be able to prove generic existence: %#v", selection)
	}
	joined := ""
	for _, fact := range selection.SupportedFacts {
		joined += fact.Statement + " " + strings.Join(fact.CriticalValues, " ")
	}
	if !strings.Contains(joined, "一次性拖鞋") {
		t.Fatalf("repaired evidence lost the selected subtype: %#v", selection.SupportedFacts)
	}
}

func TestStoreSupplyInsufficientRecoversAffirmativeSubtypeFAQAtNarrowMinimum(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Intent:    "hotel_info",
		Query:     "有拖鞋吗",
		SubIntent: "availability",
		Objective: "availability",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "T1C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit: judgeTestHit(
					1,
					101,
					"一次性拖鞋",
					"问题：房间里有一次性拖鞋吗？\n答案：酒店房间内均配备一次性拖鞋，若有额外需求，可前往1313对面洗衣房领取。",
					0.72,
				),
			},
			{
				CandidateID: "T1C2",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 102, "洗澡用拖鞋", "问题：酒店提供洗澡用拖鞋吗？\n答案：酒店不提供洗澡用拖鞋。", 0.8442),
			},
			{
				CandidateID: "T1C3",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 103, "塑料拖鞋", "问题：房间提供塑料拖鞋吗？\n答案：酒店不提供塑料拖鞋。", 0.8399),
			},
		},
	}
	task.RawCandidates = append([]knowledgeEvidenceJudgeCandidate(nil), task.Candidates...)
	selections := map[string]map[string]knowledgeEvidenceLayerSelection{
		"T1": {
			knowledgeEvidenceLayerStore: insufficientKnowledgeEvidenceLayerSelection(),
		},
	}

	if repaired := repairStoreServiceSupplyInsufficientFAQSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 1 {
		t.Fatalf("insufficient store supply task should recover once, got %d: %#v", repaired, selections)
	}
	selection := selections["T1"][knowledgeEvidenceLayerStore]
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle || len(selection.SelectedCandidateIDs) != 1 || selection.SelectedCandidateIDs[0] != "T1C1" {
		t.Fatalf("different negative subtypes must not block the affirmative matching FAQ: %#v", selection)
	}

	selections["T1"][knowledgeEvidenceLayerStore] = protocolInvalidKnowledgeEvidenceLayerSelection()
	if repaired := repairStoreServiceSupplyInsufficientFAQSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("a real protocol error must not be hidden by score rescue: %#v", selections)
	}

	task.Candidates[0].Hit.Score = 0.69
	task.RawCandidates = append([]knowledgeEvidenceJudgeCandidate(nil), task.Candidates...)
	selections["T1"][knowledgeEvidenceLayerStore] = insufficientKnowledgeEvidenceLayerSelection()
	if repaired := repairStoreServiceSupplyInsufficientFAQSelections([]knowledgeEvidenceJudgeTask{task}, selections); repaired != 0 {
		t.Fatalf("the narrow recovery must keep 0.70 as a hard floor: %#v", selections)
	}
}

func TestKnowledgeEvidenceExistenceSubtypeConflictComparison(t *testing.T) {
	if knowledgeEvidenceExistenceFAQSubjectsComparable(
		"房间里有一次性拖鞋吗？",
		"酒店房间内均配备一次性拖鞋。",
		"酒店提供洗澡用拖鞋吗？",
		"酒店不提供洗澡用拖鞋。",
	) {
		t.Fatal("different explicit subtypes must not be treated as contradictory claims")
	}
	if !knowledgeEvidenceExistenceFAQSubjectsComparable(
		"酒店有拖鞋吗？",
		"酒店没有拖鞋。",
		"房间里有一次性拖鞋吗？",
		"酒店房间内均配备一次性拖鞋。",
	) {
		t.Fatal("a generic negative claim must conflict with a positive subtype claim")
	}
	if !knowledgeEvidenceExistenceFAQSubjectsComparable(
		"房间里有一次性拖鞋吗？",
		"酒店房间内均配备一次性拖鞋。",
		"房间里有一次性拖鞋吗？",
		"酒店房间内没有一次性拖鞋。",
	) {
		t.Fatal("opposite claims about the same subtype must remain comparable")
	}
	if !knowledgeEvidenceFAQClaimsComparableForConflict(
		"房间有拖鞋吗？",
		"房间有拖鞋，可到大堂领取。",
		"房间有拖鞋吗？",
		"房间不提供拖鞋。",
	) {
		t.Fatal("a pickup destination must not be mistaken for the fact's applicability scope")
	}
	if !knowledgeEvidenceFAQClaimsComparableForConflict(
		"房间有拖鞋吗？",
		"有的。",
		"房间有哪些拖鞋？",
		"房间没有拖鞋。",
	) {
		t.Fatal("existence and list questions about the same subject must remain comparable")
	}
	if knowledgeEvidenceFAQClaimsConflict(
		"房间有一次性拖鞋吗？",
		"房间配备一次性拖鞋。",
		"房间有一次性拖鞋吗？",
		"房间配备一次性拖鞋，无需自带。",
	) {
		t.Fatal("an unrelated negative clause must not turn two affirmative existence answers into a conflict")
	}
	if !knowledgeEvidenceFAQClaimsConflict(
		"房间有一次性拖鞋吗？",
		"有的。",
		"客用品配置说明",
		"客房不提供一次性拖鞋。",
	) {
		t.Fatal("an explicit same-subtype negative answer must conflict even when its FAQ question is a description")
	}
}

func TestStoreSupplyNarrowRecoveryRejectsConditionAndSameSubtypeConflicts(t *testing.T) {
	conditionTask := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Intent:    "hotel_info",
		Query:     "工作日有毛巾吗",
		SubIntent: "availability",
		Objective: "availability",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "毛巾", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "T1C1",
			Layer:       knowledgeEvidenceLayerStore,
			Hit:         judgeTestHit(1, 101, "周末毛巾", "问题：周末有毛巾吗？\n答案：周末提供毛巾。", 0.72),
		}},
	}
	if selection, ok := highConfidenceDirectFAQSelectionAtMinimum(conditionTask, knowledgeEvidenceLayerStore, knowledgeEvidenceStoreSupplyRescueScore); ok {
		t.Fatalf("a candidate with a conflicting condition must not be rescued: %#v", selection)
	}

	conflictTask := knowledgeEvidenceJudgeTask{
		TaskID:    "T2",
		Intent:    "hotel_info",
		Query:     "有拖鞋吗",
		SubIntent: "availability",
		Objective: "availability",
		Entities:  []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{
				CandidateID: "T2C1",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 201, "一次性拖鞋", "问题：房间有一次性拖鞋吗？\n答案：房间配备一次性拖鞋。", 0.72),
			},
			{
				CandidateID: "T2C2",
				Layer:       knowledgeEvidenceLayerStore,
				Hit:         judgeTestHit(1, 202, "一次性拖鞋供应", "问题：客用品配置说明\n答案：客房不提供一次性拖鞋。", 0.73),
			},
		},
	}
	leftQuestion, leftAnswer := splitKnowledgeEvidenceFAQForQuery(conflictTask.Candidates[0].Hit, conflictTask.Query)
	rightQuestion, rightAnswer := splitKnowledgeEvidenceFAQForQuery(conflictTask.Candidates[1].Hit, conflictTask.Query)
	if !knowledgeEvidenceFAQClaimsComparableForConflict(leftQuestion, leftAnswer, rightQuestion, rightAnswer) {
		t.Fatalf("same-subtype claims must be comparable: left=%q/%q right=%q/%q subjects=%q/%q", leftQuestion, leftAnswer, rightQuestion, rightAnswer, knowledgeEvidenceFAQExistenceSubject(leftQuestion, leftAnswer), knowledgeEvidenceFAQExistenceSubject(rightQuestion, rightAnswer))
	}
	if !knowledgeEvidenceFAQAnswersConflict(leftAnswer, rightAnswer) {
		t.Fatalf("same-subtype positive and negative answers must conflict: %q / %q", leftAnswer, rightAnswer)
	}
	if selection, ok := highConfidenceDirectFAQSelectionAtMinimum(conflictTask, knowledgeEvidenceLayerStore, knowledgeEvidenceStoreSupplyRescueScore); ok {
		t.Fatalf("opposite claims about the same subtype must block recovery: %#v", selection)
	}
}
