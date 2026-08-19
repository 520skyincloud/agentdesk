package executor

import (
	"encoding/json"
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/strictjson"
)

func TestReplyPlanV2AcceptsKnowledgeBoundaryConstraints(t *testing.T) {
	plan, err := buildRuntimeReplyPlanV2(1, []callbacks.ReplyTaskPlanTraceData{{
		TaskKey: "knowledge-no-hit", Sequence: 1, Intent: "hotel_info", SubIntent: "parking",
		Text: "停车场在哪", Output: "knowledge_text_reply",
	}}, map[string]AnswerabilityOutcome{
		"knowledge-no-hit": {Status: "no_context", ReasonCode: "knowledge_no_hit"},
	}, contracts.ActionLedgerV1{SchemaVersion: contracts.ActionLedgerV1SchemaVersion, TurnVersion: 1, Actions: []contracts.ActionLedgerItemV1{}})
	if err != nil {
		t.Fatalf("reply plan rejected its own knowledge boundary constraints: %v", err)
	}
	got := plan.Tasks[0].Constraints
	if !stringInSlice("state_knowledge_boundary_only", got) || !stringInSlice("do_not_ask_known_store_scope", got) {
		t.Fatalf("knowledge boundary constraints missing: %#v", got)
	}
	assertReplyPlanV2SchemaAccepts(t, plan)
}

func TestReplyPlanV2AcceptsMaximumRuntimeConstraintCombination(t *testing.T) {
	plan, err := buildRuntimeReplyPlanV2(1, []callbacks.ReplyTaskPlanTraceData{{
		TaskKey: "repeat-no-hit", Sequence: 1, Intent: "hotel_info", SubIntent: "parking",
		Text: "停车场在哪", Output: "knowledge_text_reply", RelationType: "repeat",
	}}, map[string]AnswerabilityOutcome{
		"repeat-no-hit": {Status: "no_context", ReasonCode: "knowledge_no_hit"},
	}, contracts.ActionLedgerV1{SchemaVersion: contracts.ActionLedgerV1SchemaVersion, TurnVersion: 1, Actions: []contracts.ActionLedgerItemV1{}})
	if err != nil {
		t.Fatalf("valid runtime constraint combination exceeded schema: %v", err)
	}
	if len(plan.Tasks[0].Constraints) != 9 {
		t.Fatalf("expected all 9 runtime constraints, got %#v", plan.Tasks[0].Constraints)
	}
	assertReplyPlanV2SchemaAccepts(t, plan)
}

func assertReplyPlanV2SchemaAccepts(t *testing.T, plan contracts.ReplyPlanV2) {
	t.Helper()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strictjson.DecodeObject[contracts.ReplyPlanV2](raw, strictjson.DecodeOptions{
		MaxBytes: 64 * 1024, Schema: contracts.MustSchema(contracts.SchemaReplyPlanV2),
	}); err != nil {
		t.Fatalf("build-time reply plan schema drift: %v", err)
	}
}
