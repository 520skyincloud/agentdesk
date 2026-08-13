package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func TestValidateReplyFactGroundingBlocksUngroundedAssertion(t *testing.T) {
	input := ReplyValidationInput{
		Plan: contracts.ReplyPlanV2{
			Tasks: []contracts.ReplyPlanTaskV2{
				{TaskKey: "t1", OutputMode: "clarification"},
			},
		},
		Output: contracts.ReplyOutputV2{
			Parts: []contracts.ReplyPartV2{
				{TaskKeys: []string{"t1"}, Content: "没有会议室，也不能提供升房。"},
			},
		},
	}
	issues := validateReplyFactGrounding(input)
	if len(issues) == 0 {
		t.Fatalf("expected fact_ungrounded issue for assertion in clarification task")
	}
	if issues[0].Code != "fact_ungrounded" {
		t.Fatalf("issue code = %q, want fact_ungrounded", issues[0].Code)
	}
}

func TestValidateReplyFactGroundingAllowsClarification(t *testing.T) {
	input := ReplyValidationInput{
		Plan: contracts.ReplyPlanV2{
			Tasks: []contracts.ReplyPlanTaskV2{
				{TaskKey: "t1", OutputMode: "clarification"},
			},
		},
		Output: contracts.ReplyOutputV2{
			Parts: []contracts.ReplyPartV2{
				{TaskKeys: []string{"t1"}, Content: "资料里没写，方便说下你的房型吗？"},
			},
		},
	}
	if issues := validateReplyFactGrounding(input); len(issues) != 0 {
		t.Fatalf("unexpected fact_grounding issues: %+v", issues)
	}
}

func TestValidateReplyFactGroundingSkipsGroundedTask(t *testing.T) {
	input := ReplyValidationInput{
		Plan: contracts.ReplyPlanV2{
			Tasks: []contracts.ReplyPlanTaskV2{
				{TaskKey: "t1", OutputMode: "text"},
			},
		},
		Output: contracts.ReplyOutputV2{
			Parts: []contracts.ReplyPartV2{
				{TaskKeys: []string{"t1"}, Content: "停车场免费，地下车库有充电桩。"},
			},
		},
	}
	if issues := validateReplyFactGrounding(input); len(issues) != 0 {
		t.Fatalf("unexpected fact_grounding issues for grounded text task: %+v", issues)
	}
}

func TestKnowledgeContentRequiresHandoff(t *testing.T) {
	cases := map[string]bool{
		"订错房间需要转人工处理":    true,
		"请联系人工客服":        true,
		"停车场免费，地下车库有充电桩": false,
		"退房在小程序里操作即可":    false,
	}
	for content, want := range cases {
		if got := knowledgeContentRequiresHandoff(content); got != want {
			t.Fatalf("knowledgeContentRequiresHandoff(%q) = %v, want %v", content, got, want)
		}
	}
}
