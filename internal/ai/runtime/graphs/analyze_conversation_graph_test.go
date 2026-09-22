package graphs

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestBuildAnalyzeConversationResult_RecommendsHandoffForComplaint(t *testing.T) {
	conversation := models.Conversation{
		LastMessageSummary: "用户反馈被重复扣费，并要求人工处理",
	}
	messages := []models.Message{
		{SenderType: enums.IMSenderTypeCustomer, Content: "你们重复扣费了，我要投诉并转人工"},
	}

	got := buildAnalyzeConversationResult(conversation, messages, AnalyzeConversationInput{
		NeedHumanHandoff: true,
	})

	if got.UserIntent != "handoff_request" {
		t.Fatalf("expected handoff_request, got %q", got.UserIntent)
	}
	if got.RiskLevel != "high" {
		t.Fatalf("expected high risk, got %q", got.RiskLevel)
	}
	if got.RecommendedNextAction != "handoff_to_human" {
		t.Fatalf("expected handoff_to_human, got %q", got.RecommendedNextAction)
	}
}

func TestBuildAnalyzeConversationResult_RecommendsPrepareTicket(t *testing.T) {
	conversation := models.Conversation{
		LastMessageSummary: "用户要求登记问题并尽快处理",
	}
	messages := []models.Message{
		{SenderType: enums.IMSenderTypeCustomer, Content: "麻烦帮我建个工单，订单一直支付失败"},
	}

	got := buildAnalyzeConversationResult(conversation, messages, AnalyzeConversationInput{
		NeedTicket: true,
	})

	if got.UserIntent != "ticket_request" {
		t.Fatalf("expected ticket_request, got %q", got.UserIntent)
	}
	if got.RecommendedNextAction != "prepare_ticket" {
		t.Fatalf("expected prepare_ticket, got %q", got.RecommendedNextAction)
	}
}

func TestBuildAnalyzeConversationResult_CurrentMessageOwnsHandoffAuthorization(t *testing.T) {
	tests := []struct {
		name     string
		messages []models.Message
		input    AnalyzeConversationInput
	}{
		{
			name: "old handoff request cannot authorize the current order question",
			messages: []models.Message{
				{SenderType: enums.IMSenderTypeCustomer, Content: "之前帮我转人工"},
				{SenderType: enums.IMSenderTypeAI, Content: "好的"},
				{SenderType: enums.IMSenderTypeCustomer, Content: "不要转人工，先帮我查订单"},
			},
		},
		{
			name: "model hint cannot replace an explicit current request",
			messages: []models.Message{
				{SenderType: enums.IMSenderTypeCustomer, Content: "这个费用不对，先帮我核对订单"},
			},
			input: AnalyzeConversationInput{NeedHumanHandoff: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildAnalyzeConversationResult(models.Conversation{LastMessageSummary: "历史曾提过人工和退款"}, tt.messages, tt.input)
			if got.RecommendedNextAction != "continue_answering" || got.UserIntent == "handoff_request" || containsSignal(got.RiskSignals, "handoff_requested") {
				t.Fatalf("non-explicit current message must keep answering: %#v", got)
			}
		})
	}
}

func TestBuildAnalyzeConversationResult_ComplaintAndFinancialRiskDoNotForceHandoff(t *testing.T) {
	for _, text := range []string{
		"我要投诉你们重复扣费，先把这笔订单查清楚",
		"这个价格和订单金额对不上，帮我核对一下",
	} {
		got := buildAnalyzeConversationResult(models.Conversation{}, []models.Message{{SenderType: enums.IMSenderTypeCustomer, Content: text}}, AnalyzeConversationInput{})
		if got.RecommendedNextAction != "continue_answering" {
			t.Fatalf("complaint or financial risk without an explicit handoff must keep answering: %#v", got)
		}
	}
}
