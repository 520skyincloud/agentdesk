package executor

import "testing"

func answerUnit() QuestionUnit {
	return QuestionUnit{QuestionKey: "Q1", Sequence: 1, Intent: "hotel_info",
		SubIntent: "supplies_self_help", RequestMode: "answer", Text: "有拖鞋吗"}
}

func actionUnit() QuestionUnit {
	return QuestionUnit{QuestionKey: "Q2", Sequence: 2, Intent: "service_request",
		SubIntent: "room_change", RequestMode: "request_action", Text: "换个房"}
}

func TestInformationAnswerWithoutToolNeverHandsOff(t *testing.T) {
	// 信息询问 + 无 Tool：知识链回答，不人工（契约 21.3/27.3）。
	decision, err := DeriveCapabilityDecision(answerUnit(), CapabilityPolicy{IntentCode: "hotel_info", NeedsKnowledge: true})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Route != "knowledge_answer" || decision.ExecutionMode != "none" {
		t.Fatalf("expected knowledge_answer/none, got %+v", decision)
	}
}

func TestActionRequestMissingFieldsClarifiesNow(t *testing.T) {
	// 办理请求 + Tool + 缺字段：当前 Turn 立即澄清，不 pending。
	decision, err := DeriveCapabilityDecision(actionUnit(), CapabilityPolicy{
		IntentCode: "service_request", ToolCodes: []string{"room_change"},
		RequiredFields: []string{"room_number"}, CollectedFields: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Route != "clarify_required_fields" || decision.ResponseMode != "clarification" {
		t.Fatalf("expected clarify_required_fields, got %+v", decision)
	}
	if len(decision.MissingFields) != 1 || decision.MissingFields[0] != "room_number" {
		t.Fatalf("expected missing room_number, got %v", decision.MissingFields)
	}
}

func TestActionRequestBusinessHandoffOnlyWhenPublished(t *testing.T) {
	// 无 Tool + 发布允许人工 -> business_handoff。
	decision, err := DeriveCapabilityDecision(actionUnit(), CapabilityPolicy{
		IntentCode: "service_request", NeedsHumanRoute: true, HumanRoutePolicy: "managed_mode",
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Route != "business_handoff" || decision.ResponseMode != "handoff_confirmation" {
		t.Fatalf("expected business_handoff, got %+v", decision)
	}
	// 无 Tool + 不允许人工 -> reject_unsupported（有明确文本，不静默）。
	decision, err = DeriveCapabilityDecision(actionUnit(), CapabilityPolicy{IntentCode: "service_request"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Route != "reject_unsupported" || decision.ResponseMode != "text" {
		t.Fatalf("expected reject_unsupported/text, got %+v", decision)
	}
}
