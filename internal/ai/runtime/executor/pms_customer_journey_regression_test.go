package executor

import (
	"context"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestCustomerJourneyMemberConditionPreservesBusinessGoal(t *testing.T) {
	for _, route := range []string{"room_upgrade", "upgrade_eligibility", "room_change", "late_checkout"} {
		got := applyRuntimeCustomerScenarioIntentCorrections(callbacks.IntentTraceData{
			IntentTasks: []callbacks.IntentTaskTraceData{{
				Intent: "hotel_info", SubIntent: route, Text: "我的会员能免费升级房间吗",
				NeedsTool: true, NeedsKnowledge: true,
			}},
		})
		if got.IntentTasks[0].SubIntent != route || !got.IntentTasks[0].NeedsKnowledge {
			t.Fatalf("membership condition replaced business goal: %#v", got)
		}
	}
}

func TestCustomerJourneyPhoneClarificationPreservesSelectedGoal(t *testing.T) {
	for _, route := range []string{"upgrade_eligibility", "member_benefits", "order_detail"} {
		task := callbacks.IntentTaskTraceData{
			SubIntent: "order_query", RelationToPrevious: "clarification_answer",
		}
		context := jevIntentContext{Intent: "hotel_info", SubIntent: route}
		if !shouldInheritJevBusinessRoute(task, context) {
			t.Fatalf("phone clarification lost %s", route)
		}
		task.RelationToPrevious = "independent"
		if shouldInheritJevBusinessRoute(task, context) {
			t.Fatal("independent lookup inherited an old goal")
		}
	}
}

func TestCustomerJourneyRenewalPriceRetainsSharedDates(t *testing.T) {
	tasks := []callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", SubIntent: "renewal", Text: "今晚再住一晚，原房还能住吗？",
			Objective: "availability", SourceRefs: []string{"U1"}, NeedsTool: true},
		{Intent: "hotel_info", SubIntent: "renewal", Text: "多少钱？",
			Objective: "price", SourceRefs: []string{"U1"}, NeedsTool: true},
	}
	got := mergeJevCompositeIntentTasks(tasks)
	if len(got) != 1 || !strings.Contains(got[0].ResolvedText, "多少钱") {
		t.Fatalf("one renewal goal was split: %#v", got)
	}
	input := runtimePMSReadPlanInputForTask(callbacks.ReplyTaskPlanTraceData{
		SubIntent: "renewal", OriginalText: got[0].Text, ResolvedText: got[0].ResolvedText,
	}, runtimePMSSessionLocator{}, time.Date(2026, 9, 28, 12, 0, 0, 0, time.FixedZone("CST", 8*3600)))
	if input.StartDate != "2026-09-28" || input.EndDate != "2026-09-29" {
		t.Fatalf("tonight plus one night became today's departure: %#v", input)
	}
	tasks[1].SourceRefs = []string{"U2"}
	if len(mergeJevCompositeIntentTasks(tasks)) != 2 {
		t.Fatal("different source messages must not be merged blindly")
	}
}

func TestCustomerJourneyCurrentSelectionOverridesStaleEntity(t *testing.T) {
	for _, text := range []string{"我已经选了云漫啊，我问的是换过去要补多少钱。", "我选的是庭院双床房，要补多少？"} {
		task := callbacks.ReplyTaskPlanTraceData{SubIntent: "room_change", OriginalText: text,
			Entities: []callbacks.IntentEntityTraceData{{Type: runtimeIntentEntityTargetRoomType, Text: "的"}},
		}
		target := runtimePMSTargetRoomTypeText(task)
		if target == "" || target == "的" || !strings.Contains(text, target) {
			t.Fatalf("customer correction lost: text=%q target=%q", text, target)
		}
	}
}

func TestCustomerJourneyDepartureClock(t *testing.T) {
	for _, text := range []string{"我下午三点才走，能晚点退吗", "下午三点离店，可以延迟退房吗"} {
		if got := runtimePMSLateCheckoutTargetTime(callbacks.ReplyTaskPlanTraceData{OriginalText: text}); got != "15:00" {
			t.Fatalf("departure clock lost: %q -> %q", text, got)
		}
	}
}

func TestCustomerJourneyPMSExecutionRetainsQueryPhone(t *testing.T) {
	intent := callbacks.IntentTraceData{NeedsTool: true, IntentTasks: []callbacks.IntentTaskTraceData{{
		SubIntent: "order_detail", NeedsTool: true,
	}}}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", SubIntent: "order_detail", OriginalText: "用13800138000查一下几点退房", NeedsTool: true,
	}}}
	_, got, handled := applyRuntimePMSReadPlansWithInvoker(context.Background(), RunInput{},
		adapter.HistoryBuildResult{}, intent, plan, nil, nil, time.Now(),
		&runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
			"reserve_order_by_phone": {{Status: pmsReadStepEmpty}},
			"recept_order_by_phone": {{Status: pmsReadStepOK, Data: map[string]any{
				"receptOrderId": "REC-1", "roomName": "大床房", "checkOutTime": "2026-09-29 12:00:00",
			}}},
		}})
	if !handled || runtimeIntentEntityValue(got.TaskPlans[0].Entities, runtimeIntentEntityCustomerPhone) != "13800138000" {
		t.Fatalf("successful query discarded phone locator: %#v", got)
	}
}

func TestCustomerJourneyRenewalLoadsLinkedRoomTypeAndChecksRoom(t *testing.T) {
	input := pmsReadPlanInput{Scenario: pmsReadScenarioRenewal, ReceptOrderID: "REC-1", ExtensionDays: 1}
	invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
		"recept_order_detail": {{Status: pmsReadStepOK, Data: map[string]any{
			"receptOrderId": "REC-1", "reserveOrderId": "RES-1", "roomName": "星旗", "homeName": "1304",
			"checkInTime": "2026-09-27 12:00:00", "checkOutTime": "2026-09-28 12:00:00",
		}}},
		"reserve_order_detail": {{Status: pmsReadStepOK, Data: map[string]any{
			"reserveOrderId": "RES-1", "checkInTime": "2026-09-27 12:00:00", "checkOutTime": "2026-09-28 12:00:00",
			"reserveProductList": []any{map[string]any{"roomId": "TYPE-1", "roomName": "星旗"}},
		}}},
	}}
	executeRuntimePMSReadPlan(context.Background(), buildPMSReadPlan(input), input, invoker)
	checked := make(map[string]bool)
	for _, call := range invoker.calls {
		if call.action == "inventory" || call.action == "stay_room_availability" {
			checked[call.action] = true
			if call.args["roomTypeId"] != "TYPE-1" || call.args["beginTime"] != "2026-09-28" || call.args["endTime"] != "2026-09-29" {
				t.Fatalf("renewal dates/linked room type lost: %#v", call)
			}
		}
	}
	if !checked["inventory"] || !checked["stay_room_availability"] {
		t.Fatalf("renewal skipped required facts: %#v", invoker.calls)
	}
}

func TestCustomerJourneyRoomListCannotAnswerMembershipEligibility(t *testing.T) {
	result := pmsReadPlanResult{Steps: []pmsReadStepResult{
		{StepID: "order.recept", Status: pmsReadStepOK, Data: map[string]any{"receptOrderId": "REC-1", "roomName": "标准房"}},
		{StepID: "inventory.stay", Status: pmsReadStepOK, Data: []any{map[string]any{"roomTypeName": "大床房", "availableCount": 2}}},
	}}
	for _, objective := range []string{"policy", "compound_information"} {
		answer := runtimePMSCustomerRoomChoiceAnswer(callbacks.ReplyTaskPlanTraceData{
			SubIntent: "upgrade_eligibility", Objective: objective, OriginalText: "我的会员可以免费升级吗",
		}, pmsReadPlan{Scenario: pmsReadScenarioRoomUpgrade}, result)
		if answer != "" {
			t.Fatalf("room list replaced eligibility answer: %q", answer)
		}
	}
}

func TestCustomerJourneyMemberFactsKeepDocumentedRulesAndBenefitConditions(t *testing.T) {
	benefits := []any{}
	for i := 0; i < 6; i++ {
		benefits = append(benefits, map[string]any{"label": "会员积分", "contentText": "1倍积分"})
	}
	benefits = append(benefits, map[string]any{
		"label": "延迟退房", "contentText": "14:00", "benefitDescription": "仅限会员客源的全天房",
	})
	data := map[string]any{
		"member": map[string]any{"gradeName": "金卡", "gradeAvailable": true, "validEndTime": "2026-12-31"},
		"grade": map[string]any{
			"benefits": benefits, "upgradeRuleSummary": "住宿5晚", "keepGradeRuleSummary": "每年住宿5晚",
			"validityText": "12个月",
		},
	}
	fact := runtimePMSMemberFactForPlan(pmsReadPlan{Scenario: pmsReadScenarioMemberBenefit}, data)
	for _, want := range []string{"14:00", "仅限会员客源的全天房", "住宿5晚", "12个月", "2026-12-31"} {
		if !strings.Contains(fact, want) {
			t.Fatalf("documented membership fact discarded: missing %q in %q", want, fact)
		}
	}
}
