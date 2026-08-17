package executor

import (
	"strings"
	"testing"
)

func answerGroupTask(turnID int64, seq int, key string) TaskRuntimeView {
	return TaskRuntimeView{TurnID: turnID, TaskKey: key, Sequence: seq, Intent: "hotel_info", SubIntent: "facility"}
}

func knowledgeDecision(key string) CapabilityDecisionView {
	return CapabilityDecisionView{TaskKey: key, Route: "knowledge_answer"}
}

func TestAnswerGroupsMergeSameEvidenceFingerprint(t *testing.T) {
	evidence := map[string]TaskEvidenceResultView{
		"t1": {Status: "approved", Fingerprint: "ev-1"},
		"t2": {Status: "approved", Fingerprint: "ev-1"},
	}
	decisions := map[string]CapabilityDecisionView{"t1": knowledgeDecision("t1"), "t2": knowledgeDecision("t2")}
	groups := BuildFinalAnswerGroups(1, []TaskRuntimeView{answerGroupTask(1, 1, "t1"), answerGroupTask(1, 2, "t2")}, decisions, evidence, nil)
	if len(groups) != 1 {
		t.Fatalf("same evidence fingerprint must merge into one group, got %d", len(groups))
	}
	if len(groups[0].TaskKeys) != 2 || !strings.HasPrefix(groups[0].GroupKey, "grp_") {
		t.Fatalf("unexpected group: %+v", groups[0])
	}
}

func TestAnswerGroupsDoNotMergeDifferentEvidence(t *testing.T) {
	evidence := map[string]TaskEvidenceResultView{
		"t1": {Status: "approved", Fingerprint: "ev-1"},
		"t2": {Status: "approved", Fingerprint: "ev-2"},
	}
	decisions := map[string]CapabilityDecisionView{"t1": knowledgeDecision("t1"), "t2": knowledgeDecision("t2")}
	groups := BuildFinalAnswerGroups(1, []TaskRuntimeView{answerGroupTask(1, 1, "t1"), answerGroupTask(1, 2, "t2")}, decisions, evidence, nil)
	if len(groups) != 2 {
		t.Fatalf("different evidence fingerprints must not merge, got %d groups", len(groups))
	}
}

func TestAnswerGroupsDoNotMergeDifferentNoContextQuestions(t *testing.T) {
	evidence := map[string]TaskEvidenceResultView{
		"food": {Status: "no_context"},
		"play": {Status: "no_context"},
	}
	decisions := map[string]CapabilityDecisionView{"food": knowledgeDecision("food"), "play": knowledgeDecision("play")}
	tasks := []TaskRuntimeView{
		{TurnID: 1, TaskKey: "food", Sequence: 1, Intent: "hotel_info", SubIntent: "nearby_food"},
		{TurnID: 1, TaskKey: "play", Sequence: 2, Intent: "hotel_info", SubIntent: "nearby_attractions"},
	}
	groups := BuildFinalAnswerGroups(1, tasks, decisions, evidence, nil)
	if len(groups) != 2 {
		t.Fatalf("different no-context questions must remain independently accountable: %+v", groups)
	}

	tasks[1].SubIntent = "nearby_food"
	groups = BuildFinalAnswerGroups(1, tasks, decisions, evidence, nil)
	if len(groups) != 1 || len(groups[0].TaskKeys) != 2 {
		t.Fatalf("equivalent no-context questions may share one answer: %+v", groups)
	}
}

func TestAnswerGroupsHandoffIsolated(t *testing.T) {
	evidence := map[string]TaskEvidenceResultView{"t1": {Status: "approved", Fingerprint: "ev-1"}, "t2": {}}
	decisions := map[string]CapabilityDecisionView{
		"t1": knowledgeDecision("t1"),
		"t2": {TaskKey: "t2", Route: "business_handoff"},
	}
	groups := BuildFinalAnswerGroups(1, []TaskRuntimeView{answerGroupTask(1, 1, "t1"), answerGroupTask(1, 2, "t2")}, decisions, evidence, nil)
	if len(groups) != 2 {
		t.Fatalf("handoff must be isolated, got %d groups", len(groups))
	}
	if groups[0].OutputMode != "text" || groups[1].OutputMode != "handoff" {
		t.Fatalf("unexpected output modes: %+v %+v", groups[0], groups[1])
	}
	if len(groups[1].TaskKeys) != 1 {
		t.Fatalf("handoff group must contain exactly one task: %+v", groups[1])
	}
}

func TestAnswerGroupsMaxFourTasksAndThreeReadyGroups(t *testing.T) {
	var tasks []TaskRuntimeView
	evidence := map[string]TaskEvidenceResultView{}
	decisions := map[string]CapabilityDecisionView{}
	for i := 1; i <= 9; i++ {
		key := "t" + string(rune('0'+i))
		tasks = append(tasks, answerGroupTask(1, i, key))
		// 三个一组的独立证据 fingerprint，保证不合并。
		evidence[key] = TaskEvidenceResultView{Status: "approved", Fingerprint: key}
		decisions[key] = knowledgeDecision(key)
	}
	groups := BuildFinalAnswerGroups(1, tasks, decisions, evidence, nil)
	if len(groups) != 9 {
		t.Fatalf("expected 9 singleton groups, got %d", len(groups))
	}
	// 4 个任务 + 相同签名应合并到上限 4。
	sameEvidence := map[string]TaskEvidenceResultView{}
	sameDecisions := map[string]CapabilityDecisionView{}
	for i := 1; i <= 5; i++ {
		key := "s" + string(rune('0'+i))
		sameEvidence[key] = TaskEvidenceResultView{Status: "approved", Fingerprint: "same"}
		sameDecisions[key] = knowledgeDecision(key)
		tasks = append(tasks, answerGroupTask(1, 10+i, key))
	}
	merged := BuildFinalAnswerGroups(1, tasks, decisions, evidence, nil)
	// 追加 5 个同签名任务：前 4 合并，第 5 单独。
	if len(merged) != 9+2 {
		t.Fatalf("expected 11 groups after merge cap, got %d", len(merged))
	}
	selected := SelectReadyGroups(merged)
	if len(selected) != 3 {
		t.Fatalf("one generate batch must select at most 3 ready groups, got %d", len(selected))
	}
}
