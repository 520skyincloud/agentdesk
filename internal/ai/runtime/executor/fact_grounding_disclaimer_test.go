package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func TestValidateReplyFactGroundingRequiresDisclaimerForGeneralGuidance(t *testing.T) {
	input := ReplyValidationInput{
		Plan: contracts.ReplyPlanV2{
			Tasks: []contracts.ReplyPlanTaskV2{
				{TaskKey: "t1", OutputMode: "text", Constraints: []string{"general_guidance_only_with_disclaimer"}},
			},
		},
		Output: contracts.ReplyOutputV2{
			Parts: []contracts.ReplyPartV2{
				{TaskKeys: []string{"t1"}, Content: "停车场免费，地下车库有充电桩。"},
			},
		},
	}
	issues := validateReplyFactGrounding(input)
	if len(issues) == 0 || issues[0].Code != "missing_disclaimer" {
		t.Fatalf("expected missing_disclaimer, got %+v", issues)
	}
}

func TestValidateReplyFactGroundingAllowsDisclaimer(t *testing.T) {
	input := ReplyValidationInput{
		Plan: contracts.ReplyPlanV2{
			Tasks: []contracts.ReplyPlanTaskV2{
				{TaskKey: "t1", OutputMode: "text", Constraints: []string{"general_guidance_only_with_disclaimer"}},
			},
		},
		Output: contracts.ReplyOutputV2{
			Parts: []contracts.ReplyPartV2{
				{TaskKeys: []string{"t1"}, Content: "停车场一般免费，具体以门店实际为准。"},
			},
		},
	}
	if issues := validateReplyFactGrounding(input); len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
}

func TestIsGeneralKnowledgeQuestion(t *testing.T) {
	cases := map[string]bool{
		"停车场有吗":    true,
		"wifi 怎么连": true,
		"发票怎么开":    true,
		"你们店老板叫什么": false,
		"帮我转人工":    false,
	}
	for text, want := range cases {
		if got := isGeneralKnowledgeQuestion(text); got != want {
			t.Fatalf("isGeneralKnowledgeQuestion(%q) = %v, want %v", text, got, want)
		}
	}
}
