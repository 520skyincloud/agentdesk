package executor

import (
	"context"
	"encoding/json"
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
	general := knowledgeEvidenceJudgeCandidate{CandidateID: "G", Layer: "general", Hit: judgeTestHit(2, 2, "送毛巾", "问题：送毛巾\n答案：转接", 0.9)}
	parsed["T"]["general"] = knowledgeEvidenceLayerSelection{Decision: "direct_single", SelectedCandidateIDs: []string{"G"}}
	if layer := selectKnowledgeEvidenceLayer(parsed["T"], map[string]knowledgeEvidenceJudgeCandidate{"G": general}, "送毛巾"); layer != "" {
		t.Fatalf("store protocol failure must not fall through to general handoff: %s", layer)
	}
}

func TestJudgeOutputExampleIncludesServiceResolution(t *testing.T) {
	prompt := knowledgeEvidenceJudgeSystemPrompt()
	raw := prompt[strings.LastIndex(prompt, "\n{")+1:]
	var output knowledgeEvidenceJudgeRawResponse
	if err := json.Unmarshal([]byte(raw), &output); err != nil {
		t.Fatalf("output example must be valid protocol: %v", err)
	}
	for _, task := range output.Tasks {
		for _, layer := range task.Layers {
			if layer.HasUsableSelfService == nil {
				t.Fatalf("output example omitted service resolution for %s", layer.Layer)
			}
		}
	}
}

func TestServiceSelfHelpKeepsExplicitUnknownsDespiteCompleteDecisionLabel(t *testing.T) {
	for _, tc := range []struct {
		name, intent, decision, ids, usable, want string
	}{
		{"single", "service_request", "direct_single", `"C1"`, "true", "partial"},
		{"combined", "service_request", "direct_combined", `"C1","C2","C3"`, "true", "partial"},
		{"cannot_self_serve", "service_request", "direct_combined", `"C1","C2"`, "false", "partial"},
		{"missing_routing", "service_request", "direct_single", `"C1"`, "null", "protocol_invalid"},
		{"ordinary_faq", "hotel_info", "direct_single", `"C1"`, "false", "protocol_invalid"},
		{"unknown_candidate", "service_request", "direct_single", `"unknown"`, "true", "protocol_invalid"},
		{"duplicate_candidate", "service_request", "direct_combined", `"C1","C1"`, "true", "protocol_invalid"},
		{"combined_missing_candidate", "service_request", "direct_combined", `"C1"`, "true", "protocol_invalid"},
		{"single_extra_candidate", "service_request", "direct_single", `"C1","C2"`, "true", "protocol_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := knowledgeEvidenceJudgeTask{TaskID: "T", Intent: tc.intent, Query: "送用品"}
			for _, id := range []string{"C1", "C2", "C3"} {
				task.Candidates = append(task.Candidates, knowledgeEvidenceJudgeCandidate{
					CandidateID: id, Layer: "store", Hit: judgeTestHit(1, 1, "用品", "可在洗衣房自取。", 0.8),
				})
			}
			raw := fmt.Sprintf(`{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T","layers":[{
"layer":"store","decision":%q,"hasUsableSelfService":%s,"selectedCandidateIds":[%s],
"supportedFacts":[{"factId":"F","aspect":"method","statement":"可在洗衣房自取。","criticalValues":["洗衣房"]}],
"missingAspects":["是否送到房间"],"answerText":"可在洗衣房自取，送房能力暂未确认。"}]}]}`, tc.decision, tc.usable, tc.ids)
			parsed, err := parseKnowledgeEvidenceJudgeRuntimeResponse(raw, []knowledgeEvidenceJudgeTask{task})
			if err != nil {
				t.Fatal(err)
			}
			result := parsed["T"]["store"]
			if result.Decision != tc.want {
				t.Fatalf("unexpected service decision: %+v", result)
			}
			if tc.want == "partial" && (len(result.SupportedFacts) != 1 || len(result.MissingAspects) != 1 ||
				result.HasUsableSelfService != (tc.usable == "true") || result.AnswerText == nil) {
				t.Fatalf("normalization must not alter facts, unknowns, answer or routing: %+v", result)
			}
		})
	}
}

func TestJudgeIgnoresUnconsumedFieldsWithoutRelaxingEvidenceContract(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{TaskID: "T", Intent: "service_request", Query: "送用品",
		Candidates: []knowledgeEvidenceJudgeCandidate{{
			CandidateID: "C", Layer: "store", Hit: judgeTestHit(1, 1, "用品", "可在洗衣房自取。", 0.8),
		}}}
	raw := `{"schemaVersion":"knowledge_evidence_judge.v2","intent":"ignored","tasks":[{
"taskId":"T","intent":"ignored","layers":[{"layer":"store","decision":"partial","hasUsableSelfService":true,
"intent":"ignored","selectedCandidateIds":["C"],"supportedFacts":[{
"factId":"F","aspect":"method","statement":"可在洗衣房自取。","criticalValues":["洗衣房"],"explanation":"ignored"}],
"missingAspects":["送房"],"answerText":"可在洗衣房自取。"}]}]}`
	parsed, err := parseKnowledgeEvidenceJudgeRuntimeResponse(raw, []knowledgeEvidenceJudgeTask{task})
	if err != nil {
		t.Fatal(err)
	}
	result := parsed["T"]["store"]
	if result.Decision != "partial" || !result.HasUsableSelfService || len(result.SupportedFacts) != 1 {
		t.Fatalf("extra metadata must not discard usable evidence: %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil || strings.Contains(string(encoded), "ignored") {
		t.Fatalf("unconsumed metadata must not enter the reply plan: %s, %v", encoded, err)
	}
	for _, broken := range []string{
		strings.Replace(raw, `"selectedCandidateIds":["C"]`, `"selectedCandidateIds":["unknown"]`, 1),
		strings.Replace(raw, `"taskId":"T"`, `"task":"T"`, 1),
		strings.Replace(raw, `"statement":"可在洗衣房自取。"`, `"text":"可在洗衣房自取。"`, 1),
		strings.Replace(raw, `"hasUsableSelfService":true`, `"hasUsableSelfService":"true"`, 1),
	} {
		parsed, err := parseKnowledgeEvidenceJudgeRuntimeResponse(broken, []knowledgeEvidenceJudgeTask{task})
		if err == nil && parsed["T"]["store"].Decision != "protocol_invalid" {
			t.Fatalf("required field and candidate validation must remain: %+v", parsed)
		}
	}
}
