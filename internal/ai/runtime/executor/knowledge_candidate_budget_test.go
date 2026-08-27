package executor

import (
	"fmt"
	"testing"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestKnowledgeEvidenceJudgeCandidateBudgetWeightsCompoundTasks(t *testing.T) {
	tasks := make([]knowledgeEvidenceJudgeTask, 0, 8)
	objectives := make(map[string]string, 8)
	for index := 0; index < 8; index++ {
		taskID := fmt.Sprintf("T%d", index+1)
		tasks = append(tasks, candidateBudgetTask(taskID, 8))
		if index < 4 {
			objectives[taskID] = "compound_information"
		} else {
			objectives[taskID] = "quantity"
		}
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates(tasks, objectives, knowledgeEvidenceJudgeBatchCandidateBudget)
	if len(limited) != len(tasks) {
		t.Fatalf("expected all tasks to remain, got %d", len(limited))
	}
	total := 0
	for index, task := range limited {
		want := 3
		if index < 4 {
			want = 4
		}
		if len(task.Candidates) != want {
			t.Fatalf("task %s expected %d candidates, got %d", task.TaskID, want, len(task.Candidates))
		}
		assertCandidateBudgetLayers(t, task, true)
		total += len(task.Candidates)
	}
	if total != knowledgeEvidenceJudgeBatchCandidateBudget {
		t.Fatalf("expected total candidate budget %d, got %d", knowledgeEvidenceJudgeBatchCandidateBudget, total)
	}
}

func TestKnowledgeEvidenceJudgeCompoundQuotaKeepsThirdStoreFactAndGeneralFallback(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "同时询问多个事实维度",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "门店事实一"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "门店事实二"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "门店事实三"}},
			{CandidateID: "T1C4", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Content: "门店事实四"}},
			{CandidateID: "T1C5", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Content: "通用兜底"}},
		},
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates(
		[]knowledgeEvidenceJudgeTask{task},
		map[string]string{"T1": "compound_information"},
		knowledgeEvidenceJudgeCompoundTaskCandidates,
	)
	if len(limited) != 1 || len(limited[0].Candidates) != 4 {
		t.Fatalf("compound task must keep four candidates, got %#v", limited)
	}
	want := []string{"T1C1", "T1C2", "T1C3", "T1C5"}
	for index, candidateID := range want {
		if limited[0].Candidates[index].CandidateID != candidateID {
			t.Fatalf("compound quota lost a required store fact or general fallback: %#v", limited[0].Candidates)
		}
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetRedistributesUnusedShare(t *testing.T) {
	tasks := []knowledgeEvidenceJudgeTask{
		candidateBudgetTask("T1", 2),
		candidateBudgetTask("T2", 8),
		candidateBudgetTask("T3", 8),
	}
	objectives := map[string]string{
		"T1": "compound_information",
		"T2": "quantity",
		"T3": "location",
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates(tasks, objectives, 9)
	want := []int{2, 4, 3}
	total := 0
	for index, task := range limited {
		if len(task.Candidates) != want[index] {
			t.Fatalf("task %s expected deterministic redistributed quota %d, got %d", task.TaskID, want[index], len(task.Candidates))
		}
		total += len(task.Candidates)
	}
	if total != 9 {
		t.Fatalf("expected redistributed budget 9, got %d", total)
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetNeverExpandsPastBatchLimit(t *testing.T) {
	tasks := make([]knowledgeEvidenceJudgeTask, 0, 13)
	objectives := make(map[string]string, 13)
	for index := 0; index < 13; index++ {
		taskID := fmt.Sprintf("T%d", index+1)
		tasks = append(tasks, candidateBudgetTask(taskID, 4))
		objectives[taskID] = "quantity"
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates(tasks, objectives, knowledgeEvidenceJudgeBatchCandidateBudget)
	if len(limited) != len(tasks) {
		t.Fatalf("expected every task to keep at least one candidate, got %d tasks", len(limited))
	}
	total := 0
	for index, task := range limited {
		total += len(task.Candidates)
		want := 2
		if index < 2 {
			want = 3
		}
		if len(task.Candidates) != want {
			t.Fatalf("unexpected constrained quota for task %s: got %d want %d", task.TaskID, len(task.Candidates), want)
		}
		assertCandidateBudgetLayers(t, task, true)
	}
	if total != knowledgeEvidenceJudgeBatchCandidateBudget {
		t.Fatalf("candidate count must remain capped at %d, got %d", knowledgeEvidenceJudgeBatchCandidateBudget, total)
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetKeepsStorePriorityWhenOnlyOneSlotFits(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "测试问题",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{KnowledgeBaseID: 2, Content: "通用高分候选"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{KnowledgeBaseID: 1, Content: "门店候选"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{KnowledgeBaseID: 1, Content: "门店补充"}},
		},
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{task}, map[string]string{"T1": "quantity"}, 1)
	if len(limited) != 1 || len(limited[0].Candidates) != 1 {
		t.Fatalf("expected one retained candidate, got %#v", limited)
	}
	if limited[0].Candidates[0].Layer != knowledgeEvidenceLayerStore || limited[0].Candidates[0].CandidateID != "T1C2" {
		t.Fatalf("single constrained slot must retain the store layer, got %#v", limited[0].Candidates)
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetKeepsTwoStoreCandidatesAndGeneralFallback(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "怎么办理入住",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.78, Content: "问题：另一个房间怎么办入住\n答案：转接"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.91, Content: "问题：怎么办理入住\n答案：通过入住机或小程序线上办理入住。"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.80, Content: "问题：同住人怎么办理入住\n答案：在小程序添加同住人。"}},
		},
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{task}, map[string]string{"T1": "method"}, 3)
	if len(limited) != 1 || len(limited[0].Candidates) != 3 {
		t.Fatalf("expected two store candidates and one general candidate, got %#v", limited)
	}
	want := []string{"T1C1", "T1C2", "T1C3"}
	for index, candidateID := range want {
		if limited[0].Candidates[index].CandidateID != candidateID {
			t.Fatalf("candidate selection must preserve retrieval rank within each layer, got %#v", limited[0].Candidates)
		}
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetDeduplicatesIdenticalFAQUnits(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "停车收费且有没有充电桩",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.84, Content: "问题：停车场有没有充电桩\n答案：地下车库提供充电桩，进入地下车库后右拐可以找到。"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.82, Content: "问题：停车场有没有充电桩\n答案：地下车库提供充电桩，进入地下车库后右拐可以找到。"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.75, Content: "问题：停车收费吗\n答案：酒店提供免费停车服务，设有地上和地下停车场。"}},
		},
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{task}, map[string]string{"T1": "compound_information"}, 2)
	if len(limited) != 1 || len(limited[0].Candidates) != 2 {
		t.Fatalf("expected duplicate charging answers to share one slot, got %#v", limited)
	}
	if limited[0].Candidates[0].CandidateID != "T1C1" || limited[0].Candidates[1].CandidateID != "T1C3" {
		t.Fatalf("deduplication must retain charging and parking-price facts, got %#v", limited[0].Candidates)
	}
}

func TestKnowledgeEvidenceJudgeCompoundDiversityKeepsDifferentAnswerFact(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "同时询问两类信息",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.95, Content: "问题：设施A有没有\n答案：设施A位于地下区域。"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.93, Content: "问题：请问有设施A吗\n答案：设施A位于地下区域。"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.91, Content: "问题：设施A配备了吗\n答案：设施A位于地下区域。"}},
			{CandidateID: "T1C4", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.80, Content: "问题：另一项服务收费吗\n答案：另一项服务免费。"}},
			{CandidateID: "T1C5", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.70, Content: "问题：通用规则\n答案：以门店规则为准。"}},
		},
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates(
		[]knowledgeEvidenceJudgeTask{task},
		map[string]string{"T1": "compound_information"},
		4,
	)
	if len(limited) != 1 || len(limited[0].Candidates) != 4 {
		t.Fatalf("expected three diverse store candidates and one general fallback, got %#v", limited)
	}
	got := limited[0].Candidates
	if got[0].CandidateID != "T1C1" || got[1].CandidateID != "T1C4" || got[3].CandidateID != "T1C5" {
		t.Fatalf("near-duplicate answers crowded out a different fact: %#v", got)
	}
}

func TestKnowledgeEvidenceJudgeCompoundDiversityKeepsDifferentQuestionObjectsWithShortAnswer(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "两个设施都有吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.95, Content: "问题：房间有沙发吗\n答案：是的"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.90, Content: "问题：房间里面有没有沙发\n答案：是的"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.85, Content: "问题：房间有办公桌吗\n答案：是的"}},
			{CandidateID: "T1C4", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.70, Content: "问题：通用设施\n答案：不同房型配置不同"}},
		},
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates(
		[]knowledgeEvidenceJudgeTask{task},
		map[string]string{"T1": "compound_information"},
		3,
	)
	got := limited[0].Candidates
	if len(got) != 3 || got[0].CandidateID != "T1C1" || got[1].CandidateID != "T1C3" || got[2].CandidateID != "T1C4" {
		t.Fatalf("same short answer must not merge different question objects: %#v", got)
	}
}

func TestKnowledgeEvidenceJudgeCompoundDiversityKeepsQuantityPremise(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "数量和费用都是什么",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.95, Content: "问题：房间矿泉水收费吗\n答案：是的，房间内的矿泉水都是免费的"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.90, Content: "问题：赠送矿泉水收费吗\n答案：是的，房间内的矿泉水都是免费的"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.85, Content: "问题：房间的两瓶矿泉水免费吗\n答案：是的，房间内的矿泉水都是免费的"}},
			{CandidateID: "T1C4", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.70, Content: "问题：通用饮用水规则\n答案：以门店配置为准"}},
		},
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates(
		[]knowledgeEvidenceJudgeTask{task},
		map[string]string{"T1": "compound_information"},
		3,
	)
	got := limited[0].Candidates
	if len(got) != 3 || got[0].CandidateID != "T1C1" || got[1].CandidateID != "T1C3" || got[2].CandidateID != "T1C4" {
		t.Fatalf("quantity-bearing FAQ premise was crowded out: %#v", got)
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetKeepsSameAnswerWhenFAQQuestionsDiffer(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "房间有几瓶矿泉水，收费吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.90, Content: "问题：问下房间的两瓶矿泉水是免费的吗？\n答案：是的，房间内的矿泉水都是免费的"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.80, Content: "问题：房间内赠送的矿泉水收费吗？\n答案：是的，房间内的矿泉水都是免费的"}},
		},
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{task}, map[string]string{"T1": "compound_information"}, 2)
	if len(limited) != 1 || len(limited[0].Candidates) != 2 {
		t.Fatalf("same answers with different FAQ premises must both remain, got %#v", limited)
	}
	if limited[0].Candidates[0].CandidateID != "T1C1" || limited[0].Candidates[1].CandidateID != "T1C2" {
		t.Fatalf("FAQ premise candidates changed order: %#v", limited[0].Candidates)
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetKeepsContextDependentShortAnswers(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "房间设施",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.90, Content: "问题：房间有空调吗\n答案：是的"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.80, Content: "问题：房间有办公桌吗\n答案：是的"}},
		},
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{task}, map[string]string{"T1": "compound_information"}, 2)
	if len(limited) != 1 || len(limited[0].Candidates) != 2 {
		t.Fatalf("short FAQ answers must retain their own questions, got %#v", limited)
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetStaysAtBatchLimitWithStableDiversity(t *testing.T) {
	candidates := make([]knowledgeEvidenceJudgeCandidate, 0, 32)
	for index := 1; index <= 32; index++ {
		questionNumber := index
		if index == 6 {
			questionNumber = 2
		}
		candidates = append(candidates, knowledgeEvidenceJudgeCandidate{
			CandidateID: fmt.Sprintf("T1C%d", index),
			Layer:       knowledgeEvidenceLayerStore,
			Hit: rag.RetrieveResult{
				Score:   float32(1) - float32(index)/100,
				Content: fmt.Sprintf("问题：停车规则%d\n答案：停车规则答案%d", questionNumber, questionNumber),
			},
		})
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{{
		TaskID:     "T1",
		Query:      "停车规则",
		Candidates: candidates,
	}}, map[string]string{"T1": "compound_information"}, knowledgeEvidenceJudgeBatchCandidateBudget)
	if len(limited) != 1 || len(limited[0].Candidates) != knowledgeEvidenceJudgeBatchCandidateBudget {
		t.Fatalf("candidate budget must stay at %d after dedupe, got %#v", knowledgeEvidenceJudgeBatchCandidateBudget, limited)
	}
	if limited[0].Candidates[0].CandidateID != "T1C1" {
		t.Fatalf("highest-score candidate must remain first, got %#v", limited[0].Candidates)
	}
	seen := make(map[string]bool, len(limited[0].Candidates))
	for _, candidate := range limited[0].Candidates {
		if candidate.CandidateID == "T1C6" {
			t.Fatalf("exact duplicate candidate survived compaction: %#v", limited[0].Candidates)
		}
		if seen[candidate.CandidateID] {
			t.Fatalf("candidate was selected twice: %#v", limited[0].Candidates)
		}
		seen[candidate.CandidateID] = true
	}
	repeated := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{{
		TaskID:     "T1",
		Query:      "停车规则",
		Candidates: candidates,
	}}, map[string]string{"T1": "compound_information"}, knowledgeEvidenceJudgeBatchCandidateBudget)
	for index, candidate := range limited[0].Candidates {
		if repeated[0].Candidates[index].CandidateID != candidate.CandidateID {
			t.Fatalf("semantic diversity selection must be stable, first=%#v repeated=%#v", limited[0].Candidates, repeated[0].Candidates)
		}
	}
}

func TestRuntimeKnowledgeObjectiveForQueryUsesResolvedIntentTask(t *testing.T) {
	intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{
		{
			Intent:         "hotel_info",
			Text:           "停车呢",
			ResolvedText:   "停车收费吗，有没有充电桩？",
			Objective:      "compound_information",
			NeedsKnowledge: true,
		},
		{
			Intent:         "hotel_info",
			Text:           "早餐几点",
			ResolvedText:   "早餐几点",
			Objective:      "time",
			NeedsKnowledge: true,
		},
	}}

	if got := runtimeKnowledgeObjectiveForQuery(intent, "停车收费吗，有没有充电桩？"); got != "compound_information" {
		t.Fatalf("expected compound objective from resolvedText, got %q", got)
	}
	if got := runtimeKnowledgeObjectiveForQuery(intent, "早餐几点"); got != "time" {
		t.Fatalf("expected ordinary objective from intent task, got %q", got)
	}
	if got := runtimeKnowledgeObjectiveForQuery(intent, "附近有什么"); got != "" {
		t.Fatalf("unmatched fallback query must not inherit an unrelated objective, got %q", got)
	}
}

func candidateBudgetTask(taskID string, count int) knowledgeEvidenceJudgeTask {
	candidates := make([]knowledgeEvidenceJudgeCandidate, 0, count)
	storeCount := (count + 1) / 2
	for index := 0; index < count; index++ {
		layer := knowledgeEvidenceLayerStore
		knowledgeBaseID := int64(1)
		if index >= storeCount {
			layer = knowledgeEvidenceLayerGeneral
			knowledgeBaseID = 2
		}
		candidates = append(candidates, knowledgeEvidenceJudgeCandidate{
			CandidateID: fmt.Sprintf("%sC%d", taskID, index+1),
			Layer:       layer,
			Hit: rag.RetrieveResult{
				KnowledgeBaseID: knowledgeBaseID,
				SourceRecordID:  fmt.Sprintf("%s-source-%d", taskID, index+1),
				Content:         fmt.Sprintf("候选%d", index+1),
			},
		})
	}
	return knowledgeEvidenceJudgeTask{TaskID: taskID, Query: taskID, Candidates: candidates}
}

func assertCandidateBudgetLayers(t *testing.T, task knowledgeEvidenceJudgeTask, wantBoth bool) {
	t.Helper()
	layers := map[string]bool{}
	for _, candidate := range task.Candidates {
		layers[candidate.Layer] = true
	}
	if wantBoth && (!layers[knowledgeEvidenceLayerStore] || !layers[knowledgeEvidenceLayerGeneral]) {
		t.Fatalf("task %s lost store/general judge visibility: %#v", task.TaskID, task.Candidates)
	}
}
