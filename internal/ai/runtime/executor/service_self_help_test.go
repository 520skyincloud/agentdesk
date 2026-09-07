package executor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
)

func TestServiceSelfHelpKeepsKnowledgeAndUnknownDeliveryWithoutHandoff(t *testing.T) {
	for _, supply := range []string{"面巾", "拖鞋", "牙刷"} {
		for _, usable := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/self_help_%v", supply, usable), func(t *testing.T) {
				question := "可以送一些" + supply + "来吗"
				answer := supply + "在1313对面的洗衣房可自行取用，送房服务暂未确认。"
				store := judgeTestHit(1, 101, "有多余的"+supply+"吗", "问题：有多余的"+supply+"吗\n答案："+supply+"在1313对面的洗衣房可自行取用。", 0.777)
				general := judgeTestHit(2, 201, question, "问题："+question+"\n答案：转接", 0.95)
				retriever := judgeTestRetriever(map[string]*retrievers.KnowledgeRetrieveResult{
					question: judgeTestRetrieveResult(store, general),
				})
				judge := &fakeKnowledgeEvidenceJudge{outcome: func(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
					raw := fmt.Sprintf(`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[
{"layer":"store","decision":"partial","hasUsableSelfService":%t,"selectedCandidateIds":["T1C1"],"supportedFacts":[{"factId":"F1","aspect":"method","statement":%q,"criticalValues":["1313"]}],"missingAspects":["是否送到房间"],"answerText":%q},
{"layer":"general","decision":"insufficient","hasUsableSelfService":false,"selectedCandidateIds":[],"supportedFacts":[],"missingAspects":[]}]}]}`, usable, supply+"在1313对面的洗衣房可自行取用。", answer)
					selected, err := parseKnowledgeEvidenceJudgeRuntimeResponse(raw, tasks)
					if err != nil {
						t.Fatal(err)
					}
					return knowledgeEvidenceJudgeOutcome{Applied: true, Selections: selected, Trace: callbacks.KnowledgeEvidenceJudgeTraceData{Status: "completed"}}
				}}
				collector := callbacks.NewRuntimeTraceCollector()
				collector.SetReplyPlan(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
					TaskID: "T1", Intent: "service_request", Objective: "action_request", Text: question, ResolvedText: question, NeedsKnowledge: true, Output: "knowledge_text_reply",
				}}})
				intent := hotelInfoIntent()
				intent.PrimaryIntent = "service_request"
				state, err := judgeTestGate(retriever, judge).Evaluate(context.Background(), answerabilityGateInput{
					Request: newKnowledgePolicyRunInput(question, "1"), Summary: &RunResult{}, Collector: collector, Intent: intent,
				})
				if err != nil {
					t.Fatal(err)
				}
				trace := collector.Data.Pipeline.EvidenceJudge
				if judge.calls != 1 || len(trace.Tasks) != 1 || trace.DeferredHandoff == usable {
					t.Fatalf("self help must affect routing, not calls or tasks: %+v", trace)
				}
				task := trace.Tasks[0]
				if task.SelectedLayer != "store" || task.Decision != "partial" || len(task.MissingAspects) != 1 || task.HasUsableSelfService != usable {
					t.Fatalf("preserve selected knowledge and unknown delivery: %+v", task)
				}
				if state.RetrieveResult == nil || !strings.Contains(state.RetrieveResult.ContextText, "1313") {
					t.Fatal("self-service evidence was lost")
				}
			})
		}
	}
}

func TestSelfHelpStoreLayerWinsGeneralHandoffButNotStoreHandoff(t *testing.T) {
	store := knowledgeEvidenceLayerSelection{
		Decision: "partial", HasUsableSelfService: true, SelectedCandidateIDs: []string{"S"},
		SupportedFacts: []knowledgeEvidenceFact{{FactID: "F1", Aspect: "method", Statement: "可自取。"}}, MissingAspects: []string{"送房"},
	}
	selections := map[string]knowledgeEvidenceLayerSelection{
		"store":   store,
		"general": {Decision: "direct_single", SelectedCandidateIDs: []string{"G"}},
	}
	candidates := map[string]knowledgeEvidenceJudgeCandidate{
		"S": {CandidateID: "S", Layer: "store", Hit: judgeTestHit(1, 1, "送毛巾", "问题：送毛巾\n答案：可自取。", 0.77)},
		"G": {CandidateID: "G", Layer: "general", Hit: judgeTestHit(2, 2, "送毛巾", "问题：送毛巾\n答案：转接", 0.95)},
	}
	if layer := selectKnowledgeEvidenceLayer(selections, candidates, "送毛巾"); layer != "store" {
		t.Fatalf("general handoff must not override usable store self help: %s", layer)
	}
	store.HasUsableSelfService = false
	store.Decision = "direct_single"
	store.SupportedFacts, store.MissingAspects = nil, nil
	selections["store"] = store
	candidates["S"] = knowledgeEvidenceJudgeCandidate{CandidateID: "S", Layer: "store", Hit: judgeTestHit(1, 1, "送毛巾", "问题：送毛巾\n答案：转接", 0.77)}
	if !selectionHasHandoffDirective(store, "store", candidates, "送毛巾") {
		t.Fatal("explicit store handoff must still be honored")
	}
}

func TestSelfHelpProtocolDoesNotGuessMissingServiceDecision(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{TaskID: "T", Intent: "service_request", Query: "送毛巾", Candidates: []knowledgeEvidenceJudgeCandidate{
		{CandidateID: "C", Layer: "store", Hit: judgeTestHit(1, 1, "毛巾", "可自取。", 0.8)},
	}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T","layers":[{"layer":"store","decision":"partial","selectedCandidateIds":["C"],"supportedFacts":[{"factId":"F","aspect":"method","statement":"可自取。","criticalValues":[]}],"missingAspects":["送房"]}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeRuntimeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatal(err)
	}
	if result := parsed["T"]["store"]; result.Decision != "protocol_invalid" || result.ProtocolError != "missing_service_resolution" {
		t.Fatalf("missing routing evidence is a protocol error, not lack of knowledge: %+v", result)
	}
}
