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
	if plan.Tasks[0].OutputMode != "text" {
		t.Fatalf("complete knowledge question must state the boundary instead of asking a clarification: %#v", plan.Tasks[0])
	}
	if !stringInSlice("state_knowledge_boundary_only", got) || !stringInSlice("do_not_collect_customer_fields", got) || !stringInSlice("do_not_ask_known_store_scope", got) {
		t.Fatalf("knowledge boundary constraints missing: %#v", got)
	}
	if stringInSlice("clarify_ambiguous_expression_only", got) {
		t.Fatalf("complete knowledge question must not be converted into ambiguity clarification: %#v", got)
	}
	assertReplyPlanV2SchemaAccepts(t, plan)
}

func TestReplyPlanV2KeepsCompleteDiscountNoHitAsBoundaryText(t *testing.T) {
	plan, err := buildRuntimeReplyPlanV2(1, []callbacks.ReplyTaskPlanTraceData{{
		TaskKey: "discount-no-hit", Sequence: 1, Intent: "hotel_info", SubIntent: "discount",
		Text: "现在入住有没有优惠", RequestMode: "answer", Output: "knowledge_text_reply",
	}}, map[string]AnswerabilityOutcome{
		"discount-no-hit": {Status: "unavailable", ReasonCode: "knowledge_unavailable"},
	}, contracts.ActionLedgerV1{SchemaVersion: contracts.ActionLedgerV1SchemaVersion, TurnVersion: 1, Actions: []contracts.ActionLedgerItemV1{}})
	if err != nil {
		t.Fatal(err)
	}
	got := plan.Tasks[0]
	if got.OutputMode != "text" || !stringInSlice("state_knowledge_boundary_only", got.Constraints) || stringInSlice("clarify_ambiguous_expression_only", got.Constraints) {
		t.Fatalf("complete discount question must receive a direct knowledge boundary: %#v", got)
	}
}

func TestReplyPlanV2KeepsGenuineAmbiguityAsClarification(t *testing.T) {
	plan, err := buildRuntimeReplyPlanV2(1, []callbacks.ReplyTaskPlanTraceData{{
		TaskKey: "ambiguous", Sequence: 1, Intent: "interaction", SubIntent: "clarify",
		Text: "那个可以吗", RequestMode: "clarify_previous", Output: "knowledge_text_reply",
	}}, map[string]AnswerabilityOutcome{
		"ambiguous": {Status: "no_context", ReasonCode: "knowledge_no_hit"},
	}, contracts.ActionLedgerV1{SchemaVersion: contracts.ActionLedgerV1SchemaVersion, TurnVersion: 1, Actions: []contracts.ActionLedgerItemV1{}})
	if err != nil {
		t.Fatal(err)
	}
	got := plan.Tasks[0]
	if got.OutputMode != "clarification" || !stringInSlice("clarify_ambiguous_expression_only", got.Constraints) || !stringInSlice("state_knowledge_boundary_only", got.Constraints) {
		t.Fatalf("genuinely ambiguous expression must keep one clarification question: %#v", got)
	}
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
	if len(plan.Tasks[0].Constraints) != 8 {
		t.Fatalf("expected all 8 runtime constraints, got %#v", plan.Tasks[0].Constraints)
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
