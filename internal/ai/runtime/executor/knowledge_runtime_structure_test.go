package executor

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
)

func TestKnowledgeRuntimeKeepsPartialStoreEvidenceBeyondThreeCandidates(t *testing.T) {
	for _, query := range []string{"房里毛巾不够，能再送一条吗？", "我想再拿条毛巾，哪里可以拿？"} {
		t.Run(query, func(t *testing.T) {
			task := candidateBudgetTask("T1", 10)
			task.Intent, task.Query, task.OriginalText = "service_request", query, query
			task.Candidates[0].Hit.Content = "问题：可以再送一个枕套吗？\n答案：不提供多余枕套。"
			task.Candidates[1].Hit.Content = "问题：可以再送一个床单吗？\n答案：不提供多余床单。"
			task.Candidates[2].Hit.Content = "问题：有多余的毛巾吗？\n答案：酒店提供面巾，放在1313房间对面的洗衣房内，可自行取用。"
			task.Candidates[5].Hit.Content = "问题：毛巾不够怎么办？\n答案：转接"
			limited := limitKnowledgeEvidenceJudgeInput([]knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceJudgeInputByteBudget)
			if len(limited) != 1 || len(limited[0].Candidates) != 10 {
				t.Fatalf("short relevant evidence must not be capped at three: %+v", limited)
			}
			visible := false
			for _, candidate := range limited[0].Candidates {
				visible = visible || candidate.CandidateID == "T1C3"
			}
			if !visible {
				t.Fatal("the store self-service fact was withheld from the Judge")
			}
		})
	}
}

func TestKnowledgeRuntimeBudgetsSourceBytesAndPreservesConflictsTogether(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "停车收费吗", Candidates: []knowledgeEvidenceJudgeCandidate{
		{CandidateID: "C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "问题：停车收费吗\n答案：免费。", Score: .9}},
		{CandidateID: "C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "问题：停车收费吗\n答案：收费30元。", Score: .8}},
		{CandidateID: "C3", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Content: "问题：入口在哪\n答案：" + strings.Repeat("很长的不同资料。", 500)}},
	}}
	completeCost := knowledgeEvidenceJudgeCandidateInputBytes(task.Candidates[0]) + knowledgeEvidenceJudgeCandidateInputBytes(task.Candidates[1])
	for _, budget := range []int{completeCost - 1, completeCost} {
		limited := limitKnowledgeEvidenceJudgeInput([]knowledgeEvidenceJudgeTask{task}, budget)
		if budget < completeCost {
			if len(limited) != 0 {
				t.Fatalf("one side of a conflicting source group must not masquerade as complete: %+v", limited)
			}
			continue
		}
		if len(limited) != 1 || len(limited[0].Candidates) != 2 {
			t.Fatalf("both conflicting answers must be visible together: %+v", limited)
		}
		if len(limited[0].RawCandidates) != 3 {
			t.Fatal("budgeting must retain original sources for audit")
		}
	}
}

func TestKnowledgeRuntimeInputBudgetDoesNotTurnQuestionCountIntoAnswerability(t *testing.T) {
	tasks := make([]knowledgeEvidenceJudgeTask, 0, 32)
	for index := 0; index < 32; index++ {
		tasks = append(tasks, candidateBudgetTask(fmt.Sprintf("T%d", index), 1))
	}
	limited := limitKnowledgeEvidenceJudgeInput(tasks, knowledgeEvidenceJudgeInputByteBudget)
	if len(limited) != len(tasks) {
		t.Fatalf("small sources fit the byte budget regardless of old 28-candidate cap: %d", len(limited))
	}
	total := 0
	for _, task := range limited {
		for _, candidate := range task.Candidates {
			total += knowledgeEvidenceJudgeCandidateInputBytes(candidate)
		}
	}
	if total > knowledgeEvidenceJudgeInputByteBudget {
		t.Fatalf("byte budget exceeded: %d", total)
	}
}

func TestKnowledgeRuntimeAcceptsEquivalentHandoffSourcesWithoutFactCardinality(t *testing.T) {
	for _, query := range []string{"房间空调不制冷，热得睡不着", "空调开半天也不凉，能帮忙看看吗"} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/legacy=%t", query, legacy), func(t *testing.T) {
				task := knowledgeEvidenceJudgeTask{
					TaskID: "T1", Intent: "service_request", OriginalText: query, Query: query,
					Candidates: []knowledgeEvidenceJudgeCandidate{
						{CandidateID: "C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "问题：空调不制冷怎么办\n答案：转接"}},
						{CandidateID: "C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "问题：空调坏了怎么办\n答案：转接"}},
					},
				}
				fields := `"selectedCandidateIds":[],"handoffCandidateIds":["C1","C2"],"decision":"insufficient"`
				if legacy {
					fields = `"selectedCandidateIds":["C1","C2"],"decision":"direct_combined"`
				}
				raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store",` + fields + `,"supportedFacts":[],"missingAspects":[],"answerText":"","hasUsableSelfService":false}]}]}`
				parsed, err := parseKnowledgeEvidenceJudgeRuntimeResponse(raw, []knowledgeEvidenceJudgeTask{task})
				if err != nil {
					t.Fatal(err)
				}
				selection := parsed["T1"][knowledgeEvidenceLayerStore]
				if selection.ProtocolError != "" || len(selection.HandoffCandidateIDs) != 2 || len(selection.SupportedFacts) != 0 {
					t.Fatalf("equivalent instructions are not two conflicting factual answers: %+v", selection)
				}
				batch := structuralKnowledgeBatch(task)
				trace := applyKnowledgeEvidenceJudgeOutcome(batch, []knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceJudgeOutcome{Applied: true, Selections: parsed})
				if len(trace.Tasks) != 1 || trace.Tasks[0].Disposition != runtimeKnowledgeDispositionDirectHandoff ||
					len(trace.Tasks[0].SelectedCandidateIDs) != 2 {
					t.Fatalf("handoff provenance must reach the existing runtime once: %+v", trace.Tasks)
				}
			})
		}
	}
}

func TestKnowledgeRuntimeKeepsFactsAndApplicableHandoffAsSeparateOutputs(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Intent: "service_request", Query: "毛巾在哪里拿，空调也不制冷",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "问题：毛巾在哪里拿\n答案：在洗衣房自取。"}},
			{CandidateID: "C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "问题：空调不制冷\n答案：转接"}},
		},
	}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","answerText":"毛巾可在洗衣房自取。","selectedCandidateIds":["C1"],"handoffCandidateIds":["C2"],"supportedFacts":[{"factId":"T1F1","aspect":"method","statement":"毛巾可在洗衣房自取。","criticalValues":[]}],"missingAspects":[],"decision":"direct_single","hasUsableSelfService":true}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeRuntimeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatal(err)
	}
	batch := structuralKnowledgeBatch(task)
	trace := applyKnowledgeEvidenceJudgeOutcome(batch, []knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceJudgeOutcome{Applied: true, Selections: parsed})
	if trace.Tasks[0].Disposition != runtimeKnowledgeDispositionAnswerThenHandoff || len(trace.Tasks[0].SupportedFacts) != 1 {
		t.Fatalf("known facts must survive an independent service instruction: %+v", trace.Tasks[0])
	}
	dispositions := runtimeKnowledgeQuestionDispositions(batch)
	if !dispositions[0].HasAnswer || !dispositions[0].NeedsHandoff {
		t.Fatalf("existing runtime must receive both outputs: %+v", dispositions)
	}
}

func TestKnowledgeRuntimeRetainsUnknownDeliveryBoundaryForMixedProxyQuestions(t *testing.T) {
	for _, query := range []string{"帮我点个外卖，能送上来吗？", "那我自己下单，你们能送到房间吗？"} {
		t.Run(query, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{
				TaskID: "T1", Intent: "service_request", SubIntent: "external_proxy_action", Objective: "action_request", Query: query,
				Candidates: []knowledgeEvidenceJudgeCandidate{{
					CandidateID: "C1", Layer: knowledgeEvidenceLayerStore,
					Hit: rag.RetrieveResult{Content: "问题：酒店地址\n答案：昭潭路1号。"},
				}},
			}
			raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","answerText":"地址可以填昭潭路1号。","selectedCandidateIds":["C1"],"handoffCandidateIds":[],"supportedFacts":[{"factId":"T1F1","aspect":"location","statement":"地址可以填昭潭路1号。","criticalValues":["昭潭路1号"]}],"missingAspects":["能否送到房间"],"decision":"partial","hasUsableSelfService":true}]}]}`
			parsed, err := parseKnowledgeEvidenceJudgeRuntimeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatal(err)
			}
			batch := structuralKnowledgeBatch(task)
			trace := applyKnowledgeEvidenceJudgeOutcome(batch, []knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceJudgeOutcome{Applied: true, Selections: parsed})
			if len(trace.Tasks[0].MissingAspects) != 1 ||
				!strings.Contains(batch.Questions[0].Result.ContextText, "能否送到房间") {
				t.Fatalf("an address cannot erase the delivery capability gap: %+v", trace.Tasks[0])
			}
			if trace.Tasks[0].Disposition != runtimeKnowledgeDispositionAnswer {
				t.Fatalf("partial evidence alone does not authorize handoff: %+v", trace.Tasks[0])
			}
		})
	}
}

func TestKnowledgeRuntimeDoesNotReplaceFailureOrInsufficiencyWithLocalBusinessDecision(t *testing.T) {
	for _, answer := range []string{"转接", "酒店停车免费。"} {
		task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "停车收费吗", Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "C1", Layer: knowledgeEvidenceLayerStore,
			Hit: rag.RetrieveResult{Content: "问题：停车收费吗\n答案：" + answer, Score: .99},
		}}}
		for _, decision := range []string{knowledgeEvidenceDecisionInsufficient, knowledgeEvidenceDecisionTimeout} {
			t.Run(answer+"/"+decision, func(t *testing.T) {
				outcome := failedKnowledgeEvidenceJudgeOutcome([]knowledgeEvidenceJudgeTask{task}, callbacks.KnowledgeEvidenceJudgeTraceData{}, decision)
				batch := structuralKnowledgeBatch(task)
				trace := applyKnowledgeEvidenceJudgeOutcome(batch, []knowledgeEvidenceJudgeTask{task}, outcome)
				if trace.Tasks[0].Decision != decision || len(trace.Tasks[0].SelectedCandidateIDs) != 0 {
					t.Fatalf("local FAQ selection replaced the authoritative failure/decision: %+v", trace.Tasks[0])
				}
			})
		}
	}
}

func TestKnowledgeRuntimePromptCarriesEachSourceOnce(t *testing.T) {
	task := candidateBudgetTask("T1", 1)
	task.Candidates[0].Hit.Content = "问题：咖啡在哪\n答案：1313房间对面的洗衣房。"
	prompt := buildKnowledgeEvidenceJudgePrompt([]knowledgeEvidenceJudgeTask{task})
	raw, err := json.Marshal(prompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "1313房间对面的洗衣房") != 1 {
		t.Fatalf("FAQ answer is duplicated in the prompt: %s", raw)
	}
	if !strings.Contains(knowledgeEvidenceJudgeSystemPrompt(), "handoffCandidateIds") {
		t.Fatal("the Judge must select applicable flow sources separately from fact cardinality")
	}
	systemPrompt := knowledgeEvidenceJudgeSystemPrompt()
	if len(systemPrompt) > 14000 {
		t.Fatalf("Judge rules must not regrow into a repeated multi-stage prompt: %d bytes", len(systemPrompt))
	}
	t.Logf("Judge system prompt: %d bytes, %d characters; single-source input: %d bytes",
		len(systemPrompt), utf8.RuneCountInString(systemPrompt), len(raw))
}

func TestKnowledgeRuntimeRecoveryRetainsPartialSourcesAndDistinctConditions(t *testing.T) {
	for _, query := range []string{"毛巾不够，帮我再拿一条", "停车要钱吗，入口怎么走"} {
		task := candidateBudgetTask("T1", 6)
		task.Query = query
		recovery := compactKnowledgeEvidenceJudgeRecoveryTasks([]knowledgeEvidenceJudgeTask{task})
		if len(recovery) != 1 || len(recovery[0].Candidates) != len(task.Candidates) {
			t.Fatalf("a timeout must not replace the source set with a best-score-only answer: %+v", recovery)
		}
	}
}

func TestKnowledgeRuntimeTraceDistinguishesDedupeFromBudgetExclusion(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: "停车收费吗", Candidates: []knowledgeEvidenceJudgeCandidate{
		{CandidateID: "C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "问题：停车收费吗\n答案：免费。"}},
		{CandidateID: "C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "问题：停车收费吗\n答案：免费。"}},
	}}
	limited := limitKnowledgeEvidenceJudgeInput([]knowledgeEvidenceJudgeTask{task}, knowledgeEvidenceJudgeInputByteBudget)
	trace := buildKnowledgeEvidenceCandidateTrace(limited[0], []string{"C1"})
	if len(trace) != 2 || trace[1].Disposition != "duplicate_source_unit" {
		t.Fatalf("deduplicated evidence must not be blamed on the input budget: %+v", trace)
	}
}

func structuralKnowledgeBatch(task knowledgeEvidenceJudgeTask) *runtimeKnowledgeRetrieveBatch {
	hits := make([]rag.RetrieveResult, 0, len(task.Candidates))
	for _, candidate := range task.Candidates {
		hits = append(hits, candidate.Hit)
	}
	result := &retrievers.KnowledgeRetrieveResult{Hits: hits, RawHits: hits}
	return &runtimeKnowledgeRetrieveBatch{
		Questions: []runtimeKnowledgeQuestionResult{{TaskID: task.TaskID, Intent: task.Intent, Query: task.Query, Result: result}},
		Merged:    &retrievers.KnowledgeRetrieveResult{},
	}
}
