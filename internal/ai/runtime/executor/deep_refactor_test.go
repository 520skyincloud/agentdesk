package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"

	"agent-desk/internal/ai/runtime/contracts"
)

// 契约 10.8：trace Requirements → answer_requirement_set.v1 持久 JSON。
func TestBuildAnswerRequirementsJSONAssignsServerKeys(t *testing.T) {
	plan := callbacks.ReplyTaskPlanTraceData{Requirements: []string{"existence|true", "location|true", "clarify|false"}}
	jsonText, err := buildAnswerRequirementsJSON(plan, "task-1", 42, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if jsonText == "" {
		t.Fatal("requirements JSON must build")
	}
	set, err := contracts.DecodeAnswerRequirementSetV1([]byte(jsonText))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if set.SchemaVersion != contracts.AnswerRequirementSetV1SchemaVersion || len(set.Requirements) != 3 {
		t.Fatalf("set: %+v", set)
	}
	if set.TaskKey != "task-1" || set.Requirements[0].SourceMsgID != 42 || set.Requirements[0].SpanEnd != 10 {
		t.Fatalf("source binding missing: %+v", set)
	}
	if set.Requirements[0].Key != "R1" || !set.Requirements[0].Required || set.Requirements[2].Required {
		t.Fatalf("server-assigned keys/required broken: %+v", set.Requirements)
	}
}

// 契约 10.10：终态集合判定。
func TestRequirementOutcomeTerminal(t *testing.T) {
	for _, outcome := range []string{"answered", "no_hit", "failed_terminal", "covered", "superseded"} {
		if !contracts.RequirementOutcomeTerminal(outcome) {
			t.Fatalf("%s must be terminal", outcome)
		}
	}
	for _, outcome := range []string{"", "pending", "failed_retryable"} {
		if contracts.RequirementOutcomeTerminal(outcome) {
			t.Fatalf("%s must not be terminal", outcome)
		}
	}
}

// 契约 4.18：QueryPlan 唯一键稳定性。
func TestBuildKnowledgeQueryPlanStableKeys(t *testing.T) {
	a := BuildKnowledgeQueryPlan(1, 1, 2, 1, "停车场在哪里", "answer", 3, 4, 5, "t1")
	b := BuildKnowledgeQueryPlan(1, 1, 2, 1, "停车场在哪里", "conditional_probe", 3, 4, 5, "t1")
	if a.QueryFingerprint != b.QueryFingerprint {
		t.Fatal("same normalized query text must share query fingerprint")
	}
	if a.CheckpointKey == b.CheckpointKey || a.QueryKey == b.QueryKey {
		t.Fatal("different purposes must have isolated recovery checkpoints")
	}
	c := BuildKnowledgeQueryPlan(1, 1, 2, 1, "有没有安静房", "answer", 3, 4, 5, "t1")
	if a.QueryFingerprint == c.QueryFingerprint {
		t.Fatal("different query must differ")
	}
	d := BuildKnowledgeQueryPlan(1, 1, 2, 1, "停车场在哪里", "answer", 3, 5, 5, "t1")
	if a.CheckpointKey != d.CheckpointKey {
		t.Fatal("the same persisted task must reuse its checkpoint after a turn version upgrade")
	}
	e := BuildKnowledgeQueryPlan(1, 1, 2, 1, "停车场在哪里", "answer", 3, 5, 6, "t2")
	if a.CheckpointKey == e.CheckpointKey {
		t.Fatal("different persisted tasks must not share a checkpoint")
	}
	probeV4 := BuildKnowledgeQueryPlan(1, 1, 2, 1, "停车场在哪里", "conditional_probe", 3, 4, 0, "conditional_probe")
	probeV5 := BuildKnowledgeQueryPlan(1, 1, 2, 1, "停车场在哪里", "conditional_probe", 3, 5, 0, "conditional_probe")
	if probeV4.CheckpointKey == probeV5.CheckpointKey {
		t.Fatal("taskless probes from different turn versions must remain isolated")
	}
}

type dummyPlanRequirements struct{ Requirements []string }

func planWithRequirements(seeds ...string) dummyPlanRequirements {
	return dummyPlanRequirements{Requirements: seeds}
}
