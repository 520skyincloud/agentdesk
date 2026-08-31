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

func TestKnowledgeEvidenceJudgeCandidateBudgetKeepsRawCandidatesForConflictChecks(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "马桶堵了怎么办",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 101, "转接", "问题：马桶堵了怎么办\n答案：转接", 0.99)},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: judgeTestHit(1, 102, "处理", "问题：马桶堵了怎么办\n答案：可以先使用马桶吸处理。", 0.80)},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerGeneral, Hit: judgeTestHit(2, 201, "通用处理", "问题：马桶堵了怎么办\n答案：请联系门店处理。", 0.79)},
		},
	}
	limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{task}, map[string]string{"T1": "action_request"}, 1)
	if len(limited) != 1 || len(limited[0].Candidates) != 1 {
		t.Fatalf("expected one budgeted Judge candidate: %#v", limited)
	}
	if len(limited[0].RawCandidates) != 3 {
		t.Fatalf("all raw candidates must remain available for deterministic conflict checks: %#v", limited[0].RawCandidates)
	}
	if limited[0].Candidates[0].CandidateID != "T1C2" {
		t.Fatalf("a competing complete factual answer must occupy the tight slot before handoff: %#v", limited[0].Candidates)
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

func TestKnowledgeEvidenceJudgeCandidateBudgetTenTasksKeepsLowerRankCompleteStoreFAQAtQuotaTwo(t *testing.T) {
	tasks := make([]knowledgeEvidenceJudgeTask, 0, 10)
	objectives := make(map[string]string, 10)
	for index := 0; index < 10; index++ {
		taskID := fmt.Sprintf("T%d", index+1)
		task := candidateBudgetTask(taskID, 6)
		objectives[taskID] = "general_guidance"
		if index == 9 {
			task.Query = "发票能备注入住人姓名吗"
			task.Candidates[0].Hit = rag.RetrieveResult{Score: 0.7797, Content: "问题：发票怎么申请\n答案：退房后在小程序申请。"}
			task.Candidates[1].Hit = rag.RetrieveResult{Score: 0.7386, Content: "问题：可以开专票吗\n答案：可以申请电子专票。"}
			task.Candidates[2].Hit = rag.RetrieveResult{Score: 0.7096, Content: "问题：发票能备注入住人姓名吗\n答案：可以备注入住人姓名。"}
			task.Candidates[3].Hit = rag.RetrieveResult{Score: 0.91, Content: "问题：发票能备注入住人姓名吗\n答案：通用规则允许备注入住人姓名。"}
		}
		tasks = append(tasks, task)
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates(tasks, objectives, knowledgeEvidenceJudgeBatchCandidateBudget)
	total := 0
	var target knowledgeEvidenceJudgeTask
	for _, task := range limited {
		total += len(task.Candidates)
		if task.TaskID == "T10" {
			target = task
		}
	}
	if total != knowledgeEvidenceJudgeBatchCandidateBudget {
		t.Fatalf("candidate selection must stay within the batch budget: got %d", total)
	}
	if len(target.Candidates) != 2 {
		t.Fatalf("expected constrained ordinary task quota 2, got %#v", target.Candidates)
	}
	want := []string{"T10C3", "T10C4"}
	for index, candidateID := range want {
		if target.Candidates[index].CandidateID != candidateID {
			t.Fatalf("quota two must preserve the complete store FAQ and a general fallback: %#v", target.Candidates)
		}
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetKeepsCompleteStoreFAQInSingleSlot(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "发票能备注入住人姓名吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.95, Content: "问题：发票怎么申请\n答案：退房后在小程序申请。"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.70, Content: "问题：发票能备注入住人姓名吗\n答案：可以备注入住人姓名。"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.99, Content: "问题：发票能备注入住人姓名吗\n答案：通用规则允许备注入住人姓名。"}},
		},
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{task}, map[string]string{"T1": "general_guidance"}, 1)
	if len(limited) != 1 || len(limited[0].Candidates) != 1 || limited[0].Candidates[0].CandidateID != "T1C2" {
		t.Fatalf("single slot must retain the complete store FAQ: %#v", limited)
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetKeepsLowScoreStrictExactStoreFAQAtTightQuotas(t *testing.T) {
	base := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "房间有几瓶矿泉水",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.98, Content: "问题：房间矿泉水不够怎么办\n答案：可以联系门店同事。"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.95, Content: "问题：矿泉水放在哪里\n答案：矿泉水放在房间桌面。"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.31, Content: "问题：房间有几瓶矿泉水\n答案：房间内有两瓶矿泉水。"}},
			{CandidateID: "T1C4", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.99, Content: "问题：房间有几瓶矿泉水\n答案：通用标准为每间房两瓶矿泉水。"}},
		},
	}

	tests := []struct {
		name  string
		quota int
		want  []string
	}{
		{name: "single slot keeps exact store FAQ", quota: 1, want: []string{"T1C3"}},
		{name: "two slots keep exact store and general fallback", quota: 2, want: []string{"T1C3", "T1C4"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limited := limitKnowledgeEvidenceJudgeTaskCandidates(
				[]knowledgeEvidenceJudgeTask{base},
				map[string]string{"T1": "quantity"},
				test.quota,
			)
			if len(limited) != 1 || len(limited[0].Candidates) != test.quota {
				t.Fatalf("quota %d returned unexpected candidates: %#v", test.quota, limited)
			}
			for index, candidateID := range test.want {
				if limited[0].Candidates[index].CandidateID != candidateID {
					t.Fatalf("quota %d must preserve strict exact knowledge before higher-score unrelated candidates: %#v", test.quota, limited[0].Candidates)
				}
			}
		})
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetKeepsSemanticStoreServiceFAQAtTightQuotas(t *testing.T) {
	base := knowledgeEvidenceJudgeTask{
		TaskID: "T1", Intent: "service_request", Query: "拖鞋没了", SubIntent: "supplies_self_help", Objective: "action_request",
		Entities: []knowledgeEvidenceJudgeEntity{{Text: "拖鞋", Type: "supply"}},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.97, Content: "问题：如何让同事送拖鞋\n答案：同事会把拖鞋送到房间。"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.91, Content: "问题：需要额外拖鞋怎么办\n答案：可前往洗衣房领取。"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.99, Content: "问题：拖鞋没了怎么办\n答案：转接"}},
		},
	}
	question, _ := splitKnowledgeEvidenceFAQForQuery(base.Candidates[1].Hit, base.Query)
	if match := knowledgeEvidenceFAQQuestionMatchScore(question, base.Query); match >= 0.82 {
		t.Fatalf("test must exercise the semantic store-service eligibility path, got %.3f", match)
	}
	for _, quota := range []int{1, 2} {
		limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{base}, map[string]string{"T1": "action_request"}, quota)
		if len(limited) != 1 || len(limited[0].Candidates) != quota {
			t.Fatalf("quota %d returned unexpected candidates: %#v", quota, limited)
		}
		seenTarget := false
		for _, candidate := range limited[0].Candidates {
			if candidate.CandidateID == "T1C2" {
				seenTarget = true
			}
		}
		if !seenTarget {
			t.Fatalf("quota %d dropped the lower-ranked complete store service FAQ: %#v", quota, limited[0].Candidates)
		}
	}
}

func TestKnowledgeEvidenceJudgeCompoundBudgetKeepsLowerRankCompleteStoreFAQ(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "发票能备注入住人姓名吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.95, Content: "问题：发票怎么申请\n答案：退房后在小程序申请。"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.90, Content: "问题：发票多久开好\n答案：申请后按页面进度为准。"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.68, Content: "问题：发票能备注入住人姓名吗\n答案：可以备注入住人姓名。"}},
			{CandidateID: "T1C4", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.99, Content: "问题：发票能备注入住人姓名吗\n答案：通用规则允许备注入住人姓名。"}},
		},
	}
	limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{task}, map[string]string{"T1": "compound_information"}, 2)
	if len(limited) != 1 || len(limited[0].Candidates) != 2 {
		t.Fatalf("expected compound quota 2, got %#v", limited)
	}
	want := []string{"T1C3", "T1C4"}
	for index, candidateID := range want {
		if limited[0].Candidates[index].CandidateID != candidateID {
			t.Fatalf("compound quota two must preserve the complete store FAQ and a general fallback: %#v", limited[0].Candidates)
		}
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetUsesGeneralFallbackOnlyWithoutCompleteStoreFAQ(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "发票能备注入住人姓名吗",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.95, Content: "问题：发票怎么申请\n答案：退房后在小程序申请。"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.90, Content: "问题：发票多久开好\n答案：申请后按页面进度为准。"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.80, Content: "问题：发票能备注入住人姓名吗\n答案：可以备注入住人姓名。"}},
		},
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{task}, map[string]string{"T1": "general_guidance"}, 2)
	if len(limited) != 1 || len(limited[0].Candidates) != 2 {
		t.Fatalf("expected store candidate plus general fallback, got %#v", limited)
	}
	want := []string{"T1C1", "T1C3"}
	for index, candidateID := range want {
		if limited[0].Candidates[index].CandidateID != candidateID {
			t.Fatalf("general fallback priority changed: %#v", limited[0].Candidates)
		}
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetKeepsRequiredStorePairBeforeGeneralFallback(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID:    "T1",
		Query:     "停车收费吗，并且停车场有没有充电桩",
		Objective: "compound_information",
		Entities: []knowledgeEvidenceJudgeEntity{
			{Text: "停车", Type: "service"},
			{Text: "充电桩", Type: "facility"},
		},
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.93, Content: "问题：停车收费吗\n答案：酒店提供免费停车服务。"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.91, Content: "问题：停车场有没有充电桩\n答案：停车场有充电桩。"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.99, Content: "问题：停车相关问题怎么办\n答案：请联系门店确认。"}},
		},
	}
	if first, second, ok := bestCompleteKnowledgeEvidenceJudgeCandidatePairIndexes(task, knowledgeEvidenceLayerStore); !ok || first != 0 || second != 1 {
		t.Fatalf("expected the two complementary store candidates to form a complete pair, got %d/%d complete=%v", first, second, ok)
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{task}, map[string]string{"T1": "compound_information"}, 2)
	if len(limited) != 1 || len(limited[0].Candidates) != 2 {
		t.Fatalf("expected the required two-candidate store pair, got %#v", limited)
	}
	want := []string{"T1C1", "T1C2"}
	for index, candidateID := range want {
		if limited[0].Candidates[index].CandidateID != candidateID {
			t.Fatalf("general fallback displaced required store evidence: %#v", limited[0].Candidates)
		}
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetPrioritizesCompleteStoreAnswerWhenHandoffConflicts(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "房间门锁打不开怎么办",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.95, Content: "问题：房间门锁打不开怎么办\n答案：请重新输入门锁密码。"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.70, Content: "问题：房间门锁打不开怎么办\n答案：转接"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.99, Content: "问题：门锁打不开怎么办\n答案：请联系工作人员。"}},
		},
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{task}, map[string]string{"T1": "method"}, 1)
	if len(limited) != 1 || len(limited[0].Candidates) != 1 || limited[0].Candidates[0].CandidateID != "T1C1" {
		t.Fatalf("a complete store answer must win a tight quota when the same layer also contains handoff: %#v", limited)
	}
	if index := bestCompleteKnowledgeEvidenceJudgeCandidateIndex(knowledgeEvidenceJudgeTask{
		TaskID: "T2",
		Query:  "房间门锁打不开怎么办",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T2C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.90, Content: "问题：房间门锁打不开怎么办\n答案：转接"}},
		},
	}, knowledgeEvidenceLayerStore); index >= 0 {
		t.Fatalf("handoff directive must not count as a complete factual FAQ, got index %d", index)
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetShowsSemanticBodyBeforeExactHandoff(t *testing.T) {
	tests := []struct {
		name         string
		layer        string
		query        string
		bodyQuestion string
		bodyAnswer   string
		bodyScore    float32
	}{
		{
			name:         "store owner identity",
			layer:        knowledgeEvidenceLayerStore,
			query:        "老板是谁",
			bodyQuestion: "董事长是谁",
			bodyAnswer:   "董事长是汤东强。",
			bodyScore:    0.848863,
		},
		{
			name:         "store nearby attractions",
			layer:        knowledgeEvidenceLayerStore,
			query:        "附近有什么好玩的",
			bodyQuestion: "周边有哪些游玩地点",
			bodyAnswer:   "可以去罍街和合柴1972游玩。",
			bodyScore:    0.894273,
		},
		{
			name:         "general owner identity",
			layer:        knowledgeEvidenceLayerGeneral,
			query:        "老板是谁",
			bodyQuestion: "董事长是谁",
			bodyAnswer:   "董事长是汤东强。",
			bodyScore:    0.848863,
		},
		{
			name:         "general nearby attractions",
			layer:        knowledgeEvidenceLayerGeneral,
			query:        "附近有什么好玩的",
			bodyQuestion: "周边有哪些游玩地点",
			bodyAnswer:   "可以去罍街和合柴1972游玩。",
			bodyScore:    0.894273,
		},
		{
			name:         "store restroom alias",
			layer:        knowledgeEvidenceLayerStore,
			query:        "房间有洗手间吗",
			bodyQuestion: "客房内有卫生间吗",
			bodyAnswer:   "客房内有卫生间。",
			bodyScore:    0.86,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates := []knowledgeEvidenceJudgeCandidate{
				{CandidateID: "T1C1", Layer: test.layer, Hit: rag.RetrieveResult{Score: 0.99, Content: "问题：" + test.query + "\n答案：转接"}},
				{CandidateID: "T1C2", Layer: test.layer, Hit: rag.RetrieveResult{Score: test.bodyScore, Content: "问题：" + test.bodyQuestion + "\n答案：" + test.bodyAnswer}},
				{CandidateID: "T1C3", Layer: test.layer, Hit: rag.RetrieveResult{Score: 0.98, Content: "问题：酒店还有哪些服务\n答案：酒店还提供其他基础服务。"}},
			}
			if test.layer == knowledgeEvidenceLayerStore {
				candidates = append(candidates, knowledgeEvidenceJudgeCandidate{
					CandidateID: "T1C4",
					Layer:       knowledgeEvidenceLayerGeneral,
					Hit:         rag.RetrieveResult{Score: 1, Content: "问题：" + test.query + "\n答案：请结合实际情况确认。"},
				})
			}
			task := knowledgeEvidenceJudgeTask{TaskID: "T1", Query: test.query, Candidates: candidates}

			selected := selectKnowledgeEvidenceJudgeTaskCandidates(task, 1, false)
			if len(selected) != 1 || selected[0].CandidateID != "T1C2" {
				t.Fatalf("one slot must expose the credible body to Judge, got %#v", selected)
			}

			selected = selectKnowledgeEvidenceJudgeTaskCandidates(task, 2, false)
			selectedIDs := make(map[string]struct{}, len(selected))
			for _, candidate := range selected {
				selectedIDs[candidate.CandidateID] = struct{}{}
			}
			if len(selected) != 2 {
				t.Fatalf("two slots must keep exactly the same-layer conflict pair, got %#v", selected)
			}
			for _, candidateID := range []string{"T1C1", "T1C2"} {
				if _, ok := selectedIDs[candidateID]; !ok {
					t.Fatalf("Judge did not receive conflict peer %s: %#v", candidateID, selected)
				}
			}
		})
	}
}

func TestKnowledgeEvidenceJudgeCandidateBudgetKeepsExactStoreHandoffWithoutFactualConflict(t *testing.T) {
	task := knowledgeEvidenceJudgeTask{
		TaskID: "T1",
		Query:  "房间门锁打不开怎么办",
		Candidates: []knowledgeEvidenceJudgeCandidate{
			{CandidateID: "T1C1", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.95, Content: "问题：房间空调打不开怎么办\n答案：请检查空调面板。"}},
			{CandidateID: "T1C2", Layer: knowledgeEvidenceLayerStore, Hit: rag.RetrieveResult{Score: 0.70, Content: "问题：房间门锁打不开怎么办\n答案：转接"}},
			{CandidateID: "T1C3", Layer: knowledgeEvidenceLayerGeneral, Hit: rag.RetrieveResult{Score: 0.99, Content: "问题：门锁打不开怎么办\n答案：请联系工作人员。"}},
		},
	}

	limited := limitKnowledgeEvidenceJudgeTaskCandidates([]knowledgeEvidenceJudgeTask{task}, map[string]string{"T1": "method"}, 1)
	if len(limited) != 1 || len(limited[0].Candidates) != 1 || limited[0].Candidates[0].CandidateID != "T1C2" {
		t.Fatalf("an uncontested exact store handoff must retain the tight slot: %#v", limited)
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
