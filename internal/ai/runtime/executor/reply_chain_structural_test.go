package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestPMSReadResultProvidesEvidenceWithoutPrewritingCustomerAnswer(t *testing.T) {
	task := callbacks.ReplyTaskPlanTraceData{
		TaskID: "T1", Intent: "hotel_info", SubIntent: "order_detail",
		OriginalText: "我几点退房", OutputKind: "text", ReplyRequired: true,
	}
	applyRuntimePMSReadResultToTask(&task, pmsReadPlan{Scenario: pmsReadScenarioOrder}, pmsReadPlanResult{
		Status: pmsReadStepOK,
		Steps: []pmsReadStepResult{{
			StepID: "order.recept", Status: pmsReadStepOK,
			Data: map[string]any{"roomName": "沐阳大床房", "homeName": "1501", "checkOutTime": "2026-09-25 12:00:00"},
		}},
	}, 0)
	if task.AnswerText != nil {
		t.Fatalf("PMS must not prewrite customer wording: %#v", task.AnswerText)
	}
	if len(task.SupportedFacts) != 1 || task.SupportedFacts[0].Aspect == "pms_customer_answer" {
		t.Fatalf("PMS should expose typed evidence only: %#v", task.SupportedFacts)
	}
}

func TestSingleEvidenceTaskAcceptsNaturalTextWithoutReplyPartsProtocol(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "hotel_info", SubIntent: "order_detail", OutputKind: "text", ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "P1F1", Aspect: "pms_order_recept", Statement: "离店时间为2026-09-25 12:00:00。"}},
	}}}
	if instruction := buildMultiReplyOutputInstruction(plan, false); instruction != "" {
		t.Fatalf("one natural reply should not require the batch JSON protocol: %q", instruction)
	}
	got, err := normalizeGeneratedReplyPartsResult("您这笔订单是9月25日12点前退房。", plan, false)
	if err != nil || got != "您这笔订单是9月25日12点前退房。" {
		t.Fatalf("natural single-task reply was rejected: got=%q err=%v", got, err)
	}
}

func TestPMSProtocolFallbackNeverDumpsInternalFacts(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "hotel_info", SubIntent: "room_change", OutputKind: "text", ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
			FactID: "P1F1", Aspect: "pms_stay_room_availability",
			Statement: "PMS只读可分配评估：23间房无冲突，包含净房、脏房和锁房内部状态。",
		}},
	}}}
	got := deterministicGeneratedReplyFallback(collector)
	for _, internal := range []string{"PMS", "只读可分配评估", "23间", "脏房", "锁房"} {
		if strings.Contains(got, internal) {
			t.Fatalf("fallback leaked internal PMS detail %q: %q", internal, got)
		}
	}
}

func TestReasonTaskMergesIntoActiveCustomerGoal(t *testing.T) {
	plans := finalizeReplyTaskPlans([]callbacks.ReplyTaskPlanTraceData{
		{Intent: "service_request", SubIntent: "room_change", OriginalText: "帮我换个房间吧", ResolvedText: "帮我换个房间吧", Output: "text_reply"},
		{Intent: "service_request", SubIntent: "room_change", DialogueAct: "reason", OriginalText: "这房间有鬼", ResolvedText: "这房间有鬼", Output: "text_reply"},
	})
	if len(plans) != 1 || !strings.Contains(plans[0].ResolvedText, "客户补充原因：这房间有鬼") || plans[0].ReplyStrategy != "acknowledge_reason_and_continue_goal" {
		t.Fatalf("reason became an unrelated second answer task: %#v", plans)
	}
}

func TestKnowledgeJudgeScopesHotelMembershipAwayFromVideoMembership(t *testing.T) {
	domain := knowledgeEvidenceSubjectDomain(knowledgeEvidenceJudgeTask{
		OriginalText: "你们这会员有啥福利", Query: "酒店住客会员有什么权益", SubIntent: "member_benefits",
	})
	if domain != "hotel_customer_membership" {
		t.Fatalf("wrong membership domain: %q", domain)
	}
	prompt := knowledgeEvidenceJudgeSystemPrompt()
	if !strings.Contains(prompt, "不能使用电视、影视平台或投屏会员知识") {
		t.Fatal("Judge prompt does not enforce membership subject isolation")
	}
}

func TestInternalPMSMarkersAreBlockedFromCustomerReply(t *testing.T) {
	for _, reply := range []string{
		"PMS 当前有效订单：房号1501。",
		"只读可分配评估：1501、1502可选。",
		"接待单ID:REC-1",
	} {
		if got, err := SanitizeGeneratedReplyText(reply); err == nil || got != "" {
			t.Fatalf("internal PMS reply must fail closed: reply=%q got=%q err=%v", reply, got, err)
		}
	}
}
