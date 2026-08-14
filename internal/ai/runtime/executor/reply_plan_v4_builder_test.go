package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func planV4Fixture() (contracts.ReplyPlanV4, contracts.ReplyPartV3) {
	plan := contracts.ReplyPlanV4{
		SchemaVersion:   contracts.ReplyPlanV4SchemaVersion,
		TurnVersion:     1,
		PlanFingerprint: "fp",
		ShouldGenerate:  true,
		Tasks: []contracts.ReplyPlanTaskV4{
			{TaskKey: "t1", Sequence: 1, Intent: "hotel_info", AnswerGroupKey: "grp_a", OutputMode: "text",
				EvidenceRefs: []string{"K1", "S1"}, RequiredFactRefs: []string{"S1"}, ActionRefs: []string{}},
			{TaskKey: "t2", Sequence: 2, Intent: "hotel_info", AnswerGroupKey: "grp_a", OutputMode: "text",
				EvidenceRefs: []string{"K2"}, ActionRefs: []string{}},
			{TaskKey: "t3", Sequence: 3, Intent: "hotel_info", AnswerGroupKey: "grp_b", OutputMode: "resource_only",
				EvidenceRefs: []string{"K3"}, ActionRefs: []string{"act-1"}},
		},
		ReplyGroups: []contracts.ReplyPlanGroupV4{
			{GroupKey: "grp_a", TaskKeys: []string{"t1", "t2"}, Sequence: 1, OutputMode: "text", MaxParts: 1, Required: true},
			{GroupKey: "grp_b", TaskKeys: []string{"t3"}, Sequence: 2, OutputMode: "resource_only", MaxParts: 1, Required: true},
		},
	}
	part := contracts.ReplyPartV3{GroupKey: "grp_a", TaskKeys: []string{"t1", "t2"}, Content: " 有咖啡，24 小时供应。 "}
	return plan, part
}

func TestResolveReplyPartDerivesEvidenceServerSide(t *testing.T) {
	plan, part := planV4Fixture()
	resolved := ResolveReplyPart(plan, part)
	if resolved.Content != strings.TrimSpace(part.Content) {
		t.Fatalf("content must be trimmed: %q", resolved.Content)
	}
	// 服务端解析并集：K1+S1（含 requiredFact）与 K2。
	joined := strings.Join(resolved.GroundingEvidenceRefs, ",")
	for _, want := range []string{"K1", "S1", "K2"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("grounding refs must include %s: %v", want, resolved.GroundingEvidenceRefs)
		}
	}
	if len(resolved.ResolvedActionRefs) != 0 {
		t.Fatalf("text group must not resolve actions: %v", resolved.ResolvedActionRefs)
	}
}

func TestResolveReplyPartDropsForeignTaskKeys(t *testing.T) {
	plan, part := planV4Fixture()
	part.TaskKeys = []string{"t1", "t3", "t-bogus"}
	resolved := ResolveReplyPart(plan, part)
	joined := strings.Join(resolved.TaskKeys, ",")
	if strings.Contains(joined, "t3") || strings.Contains(joined, "t-bogus") {
		t.Fatalf("foreign task keys must be dropped: %v", resolved.TaskKeys)
	}
	if len(resolved.TaskKeys) == 0 {
		t.Fatal("member task keys must be retained")
	}
}

func TestBuildReplyPlanV4ProjectsDeterministically(t *testing.T) {
	input := ReplyPlanBuildInput{
		TurnID: 7, TurnVersion: 2,
		Tasks: []TaskRuntimeView{
			{TurnID: 7, TaskKey: "t1", Sequence: 1, Intent: "hotel_info", SubIntent: "facility"},
		},
		Decisions: map[string]CapabilityDecisionV1{
			"t1": {TaskKey: "t1", Route: "knowledge_answer", PolicyFingerprint: "pf-1", CapabilityCode: "hotel_info/facility"},
		},
		Groups: []AnswerGroup{
			{GroupKey: "grp_x", TaskKeys: []string{"t1"}, Sequence: 1, OutputMode: "text"},
		},
		EvidenceByTask:          map[string]TaskEvidenceResultView{"t1": {Status: "approved", Fingerprint: "ev-1", EvidenceRefs: []string{"K9"}}},
		RequiredFactsByTask:     map[string][]string{"t1": {"S2"}},
		ScopeFingerprint:        "scope-1",
		FactSnapshotFingerprint: "facts-1",
		PromptPolicyRevisions:   "prompt-1",
	}
	built, err := BuildReplyPlanV4(input)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if built.SchemaVersion != contracts.ReplyPlanV4SchemaVersion || !built.ShouldGenerate {
		t.Fatalf("unexpected plan head: %+v", built)
	}
	if len(built.Tasks) != 1 || built.Tasks[0].AnswerGroupKey != "grp_x" {
		t.Fatalf("task projection broken: %+v", built.Tasks)
	}
	if built.Tasks[0].Knowledge.Policy != "required" || built.Tasks[0].Knowledge.Status != "has_context" {
		t.Fatalf("knowledge projection broken: %+v", built.Tasks[0].Knowledge)
	}
	if len(built.ReplyGroups) != 1 || built.ReplyGroups[0].MaxParts != 1 {
		t.Fatalf("group projection broken: %+v", built.ReplyGroups)
	}
	if len(built.PlanFingerprint) != 64 {
		t.Fatalf("plan fingerprint must be sha256 hex: %q", built.PlanFingerprint)
	}
	again, _ := BuildReplyPlanV4(input)
	if again.PlanFingerprint != built.PlanFingerprint {
		t.Fatal("plan fingerprint must be deterministic for identical inputs")
	}
	// 任一输入变化必须改变 fingerprint。
	input.TurnVersion = 3
	changed, _ := BuildReplyPlanV4(input)
	if changed.PlanFingerprint == built.PlanFingerprint {
		t.Fatal("turn version change must change fingerprint")
	}
}
