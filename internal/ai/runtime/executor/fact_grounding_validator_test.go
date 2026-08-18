package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
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

func TestValidatorRejectsReaskingKnownStoreOnNoHit(t *testing.T) {
	input := ReplyValidationInput{
		Req: RunInput{Conversation: models.Conversation{StoreID: 7}},
		Plan: contracts.ReplyPlanV2{Tasks: []contracts.ReplyPlanTaskV2{{
			TaskKey: "checkin", OutputMode: "clarification",
			Knowledge: contracts.ReplyPlanKnowledge{Policy: "required", Status: "no_context"},
		}}},
		Output: contracts.ReplyOutputV2{Parts: []contracts.ReplyPartV2{{
			TaskKeys: []string{"checkin"}, Content: "请问您订的是哪家店？",
		}}},
	}
	issues := validateNoHitKnownScopeClarification(input)
	if len(issues) != 1 || issues[0].Code != "known_scope_reasked" {
		t.Fatalf("known store scope must not be re-asked: %+v", issues)
	}
}

func TestValidateReplyFactGroundingBlocksRoomStatusFabrication(t *testing.T) {
	// “今晚大床房有房”是无数据源领域（房态）的断言，即使任务是 text 模式也必须一票否决，
	// 因为系统没有查询房态的能力，任何“有房/无房”都是编造。
	input := ReplyValidationInput{
		Plan: contracts.ReplyPlanV2{
			Tasks: []contracts.ReplyPlanTaskV2{
				{TaskKey: "t1", OutputMode: "text"},
			},
		},
		Output: contracts.ReplyOutputV2{
			Parts: []contracts.ReplyPartV2{
				{TaskKeys: []string{"t1"}, Content: "今晚大床房有房，你换过去的话明天中午12点前退原房就行。"},
			},
		},
	}
	result := NewReplyValidator().Validate(input)
	if result.Status != "rejected" {
		t.Fatalf("expected rejected for room status fabrication, got %s", result.Status)
	}
	found := false
	for _, issue := range result.Errors {
		if issue.Code == "unsupported_domain_assertion" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unsupported_domain_assertion issue, got %+v", result.Errors)
	}
}

func TestValidateReplyFactGroundingAllowsSafeRoomQuery(t *testing.T) {
	// 不含确定性断言的房态追问应放行（例如“方便说下要哪个房型吗”）。
	input := ReplyValidationInput{
		Plan: contracts.ReplyPlanV2{
			Tasks: []contracts.ReplyPlanTaskV2{
				{TaskKey: "t1", OutputMode: "clarification"},
			},
		},
		Output: contracts.ReplyOutputV2{
			Parts: []contracts.ReplyPartV2{
				{TaskKeys: []string{"t1"}, Content: "方便说下你想换到哪个房型吗？"},
			},
		},
	}
	if issues := validateReplyFactGrounding(input); len(issues) != 0 {
		t.Fatalf("unexpected fact_grounding issues for safe query: %+v", issues)
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
