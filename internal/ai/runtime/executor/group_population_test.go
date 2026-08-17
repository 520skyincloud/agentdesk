package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

// B2 验证：V3 链路的 BuildFinalAnswerGroups→BuildReplyPlanV4 确实产出 replyGroups。
func TestBuildFinalAnswerGroupsProducesGroups(t *testing.T) {
	tasks := []TaskRuntimeView{
		{TurnID: 1, TaskKey: "t1", Sequence: 1, Intent: "hotel_info", SubIntent: "store_knowledge"},
		{TurnID: 1, TaskKey: "t2", Sequence: 2, Intent: "hotel_info", SubIntent: "store_knowledge"},
	}
	decisions := map[string]CapabilityDecisionView{
		"t1": {TaskKey: "t1", Route: "knowledge_answer"},
		"t2": {TaskKey: "t2", Route: "knowledge_answer"},
	}
	evidence := map[string]TaskEvidenceResultView{
		"t1": {Status: "no_context", EvidenceRefs: []string{}},
		"t2": {Status: "no_context", EvidenceRefs: []string{}},
	}
	groups := BuildFinalAnswerGroups(1, tasks, decisions, evidence, nil)
	if len(groups) == 0 {
		t.Fatal("BuildFinalAnswerGroups returned empty groups")
	}
	for _, group := range groups {
		if group.GroupKey == "" || len(group.TaskKeys) == 0 {
			t.Fatalf("group missing key or tasks: %+v", group)
		}
		if group.OutputMode != "text" {
			t.Fatalf("knowledge answer group should be text mode: %+v", group)
		}
	}
	// BuildReplyPlanV4 should populate ReplyGroups from these groups
	plan, err := BuildReplyPlanV4(ReplyPlanBuildInput{
		TurnID: 1, TurnVersion: 1, Tasks: tasks,
		Decisions: map[string]CapabilityDecisionV1{
			"t1": {TaskKey: "t1", Route: "knowledge_answer", PolicyFingerprint: "pf"},
			"t2": {TaskKey: "t2", Route: "knowledge_answer", PolicyFingerprint: "pf"},
		},
		Groups:         groups,
		EvidenceByTask: evidence,
	})
	if err != nil {
		t.Fatalf("BuildReplyPlanV4: %v", err)
	}
	if len(plan.ReplyGroups) == 0 {
		t.Fatal("plan.ReplyGroups is empty — the group→plan pipeline is broken")
	}
	for _, task := range plan.Tasks {
		if task.AnswerGroupKey == "" {
			t.Fatalf("task %s has empty AnswerGroupKey", task.TaskKey)
		}
	}
	// generateTaskInputTextV1 should now include answerGroupKey
	for _, group := range plan.ReplyGroups {
		for _, taskKey := range group.TaskKeys {
			for _, task := range plan.Tasks {
				if task.TaskKey == taskKey && task.AnswerGroupKey != group.GroupKey {
					t.Fatalf("task %s groupKey mismatch: %s vs %s", taskKey, task.AnswerGroupKey, group.GroupKey)
				}
			}
		}
	}
}

// B3 验证：模型输出错误的 groupKey 时 ValidatorV3 只告警不拒绝。
func TestValidatorV3WrongGroupKeyOnlyWarns(t *testing.T) {
	plan := contracts.ReplyPlanV4{
		SchemaVersion: contracts.ReplyPlanV4SchemaVersion, TurnVersion: 1, ShouldGenerate: true,
		Tasks: []contracts.ReplyPlanTaskV4{{
			TaskKey: "t1", Sequence: 1, Intent: "hotel_info", AnswerGroupKey: "grp_correct",
			Objective: "answer", OutputMode: "text",
			Knowledge:    contracts.ReplyPlanKnowledgeV4{Policy: "required", Status: "not_needed"},
			EvidenceRefs: []string{}, ActionRefs: []string{}, RequiredFactRefs: []string{},
			ObservationRefs: []string{}, Constraints: []string{},
		}},
		ReplyGroups: []contracts.ReplyPlanGroupV4{{
			GroupKey: "grp_correct", TaskKeys: []string{"t1"}, Sequence: 1,
			OutputMode: "text", MaxParts: 1, Required: true,
		}},
		GlobalConstraints: contracts.ReplyPlanGlobalV4{MaxReplyParts: 3, MaxQuestionsPerPart: 4, ForbiddenClaims: []string{}},
	}
	// 模型输出了错误的 groupKey + 正确的 taskKeys。
	output := contracts.ReplyOutputV3{
		SchemaVersion: contracts.ReplyOutputV3SchemaVersion,
		Parts: []contracts.ReplyPartV3{{
			GroupKey: "grp_wrong_guess", TaskKeys: []string{"t1"}, Content: "有咖啡和一次性剃须刀。",
		}},
	}
	result := NewReplyValidatorV3().Validate(ReplyValidationInputV3{
		Output: output, Plan: plan,
		Evidence:     contracts.EvidenceBundleV2{Items: []contracts.EvidenceItemV2{}},
		ActionLedger: contracts.ActionLedgerV1{},
	})
	if result.Status == "rejected" {
		t.Fatalf("wrong groupKey must not reject, got %s errors=%+v warnings=%+v", result.Status, result.Errors, result.Warnings)
	}
	t.Logf("status=%s errors=%+v warnings=%+v", result.Status, result.Errors, result.Warnings)
	// 错误 groupKey + 正确 taskKeys → 通过（taskKeys 反查到正确组，不拒绝）。
	if result.Status != "passed" && result.Status != "warning" {
		t.Fatalf("wrong groupKey with correct taskKeys must pass/warn, got %s", result.Status)
	}
	if len(result.NormalizedParts) == 0 {
		t.Fatalf("normalized parts should be produced: %+v", result.NormalizedParts)
	}
}

// B3 验证：完全缺失组覆盖时（taskKey 也未出现在任何 part 中）才拒绝。
func TestValidatorV3FullyMissingGroupStillRejects(t *testing.T) {
	plan := contracts.ReplyPlanV4{
		SchemaVersion: contracts.ReplyPlanV4SchemaVersion, TurnVersion: 1, ShouldGenerate: true,
		Tasks: []contracts.ReplyPlanTaskV4{{
			TaskKey: "t1", Sequence: 1, Intent: "hotel_info", AnswerGroupKey: "grp_a",
			Objective: "answer", OutputMode: "text",
			Knowledge:    contracts.ReplyPlanKnowledgeV4{Policy: "required", Status: "not_needed"},
			EvidenceRefs: []string{}, ActionRefs: []string{}, RequiredFactRefs: []string{},
			ObservationRefs: []string{}, Constraints: []string{},
		}},
		ReplyGroups: []contracts.ReplyPlanGroupV4{{
			GroupKey: "grp_a", TaskKeys: []string{"t1"}, Sequence: 1,
			OutputMode: "text", MaxParts: 1, Required: true,
		}},
		GlobalConstraints: contracts.ReplyPlanGlobalV4{MaxReplyParts: 3, MaxQuestionsPerPart: 4, ForbiddenClaims: []string{}},
	}
	// 模型只回答了一个不存在的任务。
	output := contracts.ReplyOutputV3{
		SchemaVersion: contracts.ReplyOutputV3SchemaVersion,
		Parts: []contracts.ReplyPartV3{{
			GroupKey: "grp_other", TaskKeys: []string{"t_bogus"}, Content: "不相关的回复。",
		}},
	}
	result := NewReplyValidatorV3().Validate(ReplyValidationInputV3{
		Output: output, Plan: plan,
		Evidence:     contracts.EvidenceBundleV2{Items: []contracts.EvidenceItemV2{}},
		ActionLedger: contracts.ActionLedgerV1{},
	})
	if result.Status != "rejected" && result.Status != "repairable_protocol_error" {
		t.Fatalf("fully uncovered required group must not pass: %s", result.Status)
	}
}
