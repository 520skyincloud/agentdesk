package executor

import (
	"reflect"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestJevGoalContractKeepsScopeAndExactRequestedAspect(t *testing.T) {
	for _, tc := range []struct {
		text, route, objective, scope, aspect string
	}{
		{"你们分哪些会员等级", "member_program", "member_level_names", runtimeSubjectPublicMembership, "member_level_names"},
		{"会员卡有哪几种", "member_program", "member_level_names", runtimeSubjectPublicMembership, "member_level_names"},
		{"钻石卡能几点退房", "member_program", "checkout_time", runtimeSubjectPublicMembership, "checkout_time"},
		{"钻石会员最晚什么时候走", "member_program", "checkout_time", runtimeSubjectPublicMembership, "checkout_time"},
		{"我这个会员也能到三点吗", "member_benefits", "checkout_time", runtimeSubjectPersonalMembership, "checkout_time"},
		{"按我的会员能晚几点走", "member_benefits", "checkout_time", runtimeSubjectPersonalMembership, "checkout_time"},
		{"上次那一单花了多少钱", "order_history", "order_amount", runtimeSubjectHistoricalOrder, "order_amount"},
		{"查一下之前住的那笔费用", "order_history", "order_amount", runtimeSubjectHistoricalOrder, "order_amount"},
		{"我这次预订到哪天结束", "order_detail", "checkout_time", runtimeSubjectCurrentOrder, "checkout_time"},
		{"帮我看这笔订单最晚几点离店", "order_detail", "checkout_time", runtimeSubjectCurrentOrder, "checkout_time"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			task := goalContractMappedTask(t, tc.text, jevIntentState{}, map[string]string{
				"T1_route": tc.route, "T1_objective": tc.objective,
			})
			if task.SubjectScope != tc.scope || !reflect.DeepEqual(task.RequestedAspects, []string{tc.aspect}) {
				t.Fatalf("goal scope/aspect lost: %#v", task)
			}
			plan := replyTaskPlanFromIntentTask(task)
			if plan.SubjectScope != tc.scope || !reflect.DeepEqual(plan.RequestedAspects, task.RequestedAspects) {
				t.Fatalf("reply-plan mapping lost the typed goal: %#v", plan)
			}
			if tc.scope == runtimeSubjectHistoricalOrder && task.SubIntent != "order_query" {
				t.Fatalf("history route must preserve the existing execution contract: %#v", task)
			}
		})
	}
}

func TestJevGoalContractMembershipDoesNotInheritHistoricalOrder(t *testing.T) {
	state := jevIntentState{RecentBusinessTask: &jevIntentPriorTaskState{
		Ref: "R1", Intent: "hotel_info", SubIntent: "order_detail",
		SubjectScope: runtimeSubjectHistoricalOrder, Text: "上次住的订单几点离店",
		ResolvedText: "查询已完成订单，接待单ID:OLD-ORDER",
		Entities: []callbacks.IntentEntityTraceData{
			{Type: runtimeIntentEntityCustomerPhone, Text: "13800138000"},
			{Type: runtimeIntentEntityOrderLocator, Text: "接待单ID:OLD-ORDER"},
		},
	}}
	for _, text := range []string{"我这个会员也能三点退房吗", "按我现在的会员最晚几点走"} {
		task := goalContractMappedTask(t, text, state, map[string]string{
			"T1_route": "member_benefits", "T1_objective": "checkout_time",
			"T1_relation": "follow_up", "T1_dialogue_act": "follow_up",
			"T1_resolution": "resolved_from_context", "T1_context": "R1",
		})
		if task.SubjectScope != runtimeSubjectPersonalMembership ||
			strings.Contains(task.ResolvedText, "已完成订单") ||
			runtimeIntentEntityValue(task.Entities, runtimeIntentEntityOrderLocator) != "" {
			t.Fatalf("personal membership reused an unrelated historical order: %#v", task)
		}
	}
}

func TestJevGoalContractMixedPreviousSubjectsRemainSelectable(t *testing.T) {
	state := jevIntentState{RecentBusinessTasks: []jevIntentPriorTaskState{
		{Ref: "R1", Intent: "hotel_info", SubIntent: "member_benefits", SubjectScope: runtimeSubjectPersonalMembership, Text: "我的会员几点退房"},
		{Ref: "R2", Intent: "hotel_info", SubIntent: "food_delivery", SubjectScope: runtimeSubjectPublicPolicy, Text: "外卖能不能送上来"},
	}}
	for _, tc := range []struct{ text, context, route string }{
		{"那我的会员能用这个时间吗", "R1", "member_benefits"},
		{"那配送要我下楼吗", "R2", "food_delivery"},
	} {
		task := goalContractMappedTask(t, tc.text, state, map[string]string{
			"T1_route": "clarify", "T1_objective": "policy", "T1_dialogue_act": "follow_up",
			"T1_relation": "follow_up", "T1_resolution": "resolved_from_context", "T1_context": tc.context,
		})
		if task.SubIntent != tc.route {
			t.Fatalf("mixed previous turn bound wrong subject: %#v", task)
		}
	}
}

func TestJevGoalContractLocatorAnswerRetainsCheckoutGoal(t *testing.T) {
	for _, text := range []string{"13800138000", "用刚才那个号码帮我查查"} {
		state := jevIntentState{RecentBusinessTask: &jevIntentPriorTaskState{
			Ref: "R1", Intent: "hotel_info", SubIntent: "order_detail",
			Objective: "time", SubjectScope: runtimeSubjectCurrentOrder, RequestedAspects: []string{"checkout_time"},
			Text: "我这次最晚几点离店", ResolvedText: "我这次最晚几点离店",
		}}
		task := goalContractMappedTask(t, text, state, map[string]string{
			"T1_route": "order_query", "T1_objective": "identity", "T1_dialogue_act": "confirmation",
			"T1_relation": "clarification_answer", "T1_resolution": "resolved_from_context", "T1_context": "R1",
		})
		if task.SubIntent != "order_detail" || task.Objective != "time" ||
			!reflect.DeepEqual(task.RequestedAspects, []string{"checkout_time"}) {
			t.Fatalf("locator answer replaced the customer's question: %#v", task)
		}
	}
}

func TestJevGoalContractSelectionIsNotAssistantRecommendation(t *testing.T) {
	state := jevIntentState{RecentBusinessTask: &jevIntentPriorTaskState{
		Ref: "R1", Intent: "hotel_info", SubIntent: "room_change", SubjectScope: runtimeSubjectCurrentStay,
		Text: "想换个房间", ResolvedText: "想换个房间",
	}}
	for _, text := range []string{"那就看看沐阳吧", "我自己选庭院那种"} {
		task := goalContractMappedTask(t, text, state, map[string]string{
			"T1_route": "room_change", "T1_objective": "availability", "T1_dialogue_act": "selection",
			"T1_relation": "follow_up", "T1_resolution": "resolved_from_context", "T1_context": "R1",
		})
		if task.SelectionSource != "customer" || task.SelectionRef != "U1" ||
			task.ReplyStrategy != "confirm_selection_and_continue_goal" {
			t.Fatalf("customer choice lost its provenance: %#v", task)
		}
	}
	for _, text := range []string{"随便，你帮我选", "我不懂房型，推荐一下吧"} {
		task := goalContractMappedTask(t, text, state, map[string]string{
			"T1_route": "room_change", "T1_objective": "recommendation", "T1_dialogue_act": "recommendation",
			"T1_relation": "follow_up", "T1_resolution": "resolved_from_context", "T1_context": "R1",
		})
		if task.SelectionSource != "" || task.ReplyStrategy != "recommend_one_supported_option" {
			t.Fatalf("recommendation was recorded as a customer selection: %#v", task)
		}
	}
}

func TestJevGoalContractCancellationNeverRevivesOldGoal(t *testing.T) {
	state := jevIntentState{RecentBusinessTask: &jevIntentPriorTaskState{
		Ref: "R1", Intent: "hotel_info", SubIntent: "member_program", Text: "查查会员权益",
	}}
	for _, text := range []string{"不用查了", "算了取消，先不看"} {
		task := goalContractMappedTask(t, text, state, map[string]string{
			"T1_route": "member_program", "T1_objective": "cancel", "T1_dialogue_act": "cancellation",
			"T1_relation": "cancel_previous", "T1_resolution": "resolved_from_context", "T1_context": "R1",
		})
		if task.NeedsTool || task.NeedsKnowledge || task.NeedsHumanRoute || task.Objective != "cancel" {
			t.Fatalf("cancellation retained executable work: %#v", task)
		}
	}
}

func TestJevGoalContractDoesNotIncreaseQuestionBatch(t *testing.T) {
	spans := []jevIntentSpan{{Ref: "T1", SourceRef: "U1", Text: "我的会员能几点退房"}}
	questions, _ := buildJevClassificationQuestions(spans, jevIntentState{})
	if len(questions) != 7 {
		t.Fatalf("goal contract added another model question: %d", len(questions))
	}
}

func TestPMSMemberPhoneDoesNotReplaceOrderLocator(t *testing.T) {
	base := runtimePMSSessionLocator{Phone: "13800138000", OrderLocator: "接待单ID:REC-1"}
	for _, text := range []string{"会员绑定手机号13900139000", "会员手机号是13900139000"} {
		got := runtimePMSSessionLocatorFromHistoryWithBase(adapter.HistoryBuildResult{RawItems: []models.Message{{
			SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: text,
		}}}, base)
		if got.Phone != base.Phone || got.OrderLocator != base.OrderLocator || got.MemberPhone != "13900139000" {
			t.Fatalf("member identity replaced the order identity: %#v", got)
		}
	}
}

func TestPMSPublicMembershipPlanDoesNotQueryPersonalMember(t *testing.T) {
	for _, phone := range []string{"13800138000", "13900139000"} {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: pmsReadScenarioMemberProgram, SubjectScope: runtimeSubjectPublicMembership,
			Phone: phone, MemberPhone: phone,
		})
		if len(plan.Steps) != 1 || plan.Steps[0].Action != "member_program" {
			t.Fatalf("public tier directory queried an unrelated customer: %#v", plan)
		}
	}
}

func TestPMSScopedPhonePlanningUsesSeparateOrderAndMember(t *testing.T) {
	for _, scenario := range []pmsReadScenario{pmsReadScenarioRoomUpgrade, pmsReadScenarioRoomChange} {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: scenario, Phone: "13800138000", MemberPhone: "13900139000", AssessMembership: true,
		})
		for _, step := range plan.Steps {
			if strings.HasPrefix(step.ID, "order.") && step.Args["phone"] != "13800138000" {
				t.Fatalf("order lookup used membership phone: %#v", step)
			}
			if step.ID == "member.benefits" && step.Args["phone"] != "13900139000" {
				t.Fatalf("membership lookup used reservation phone: %#v", step)
			}
		}
	}
}

func goalContractMappedTask(t *testing.T, text string, state jevIntentState, choices map[string]string) callbacks.IntentTaskTraceData {
	t.Helper()
	spans := []jevIntentSpan{{Ref: "T1", SourceRef: "U1", Text: text}}
	questions, contexts := buildJevClassificationQuestions(spans, state)
	intent, err := buildIntentTraceFromJev(jevTestResponse(questions, choices, nil), spans, contexts)
	if err != nil || len(intent.IntentTasks) != 1 {
		t.Fatalf("map typed goal: tasks=%#v err=%v", intent.IntentTasks, err)
	}
	return intent.IntentTasks[0]
}
