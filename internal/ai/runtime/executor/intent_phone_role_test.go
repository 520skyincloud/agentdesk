package executor

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestJevMemberPhoneCorrectionPreservesCurrentStayAcrossExecutionAndHistory(t *testing.T) {
	base := runtimePMSSessionLocator{Phone: "13800138000", OrderLocator: "接待单ID:REC-1"}
	for _, text := range []string{
		"换成这个需要补多少？我会员手机号是13900139000，能免差价吗？",
		"订房的没变，我卡上留的是13900139000，换沐阳能优惠多少？",
	} {
		t.Run(text, func(t *testing.T) {
			state := jevIntentState{RecentBusinessTask: &jevIntentPriorTaskState{
				Ref: "R1", Intent: "hotel_info", SubIntent: "room_change", SubjectScope: runtimeSubjectCurrentStay,
				Text: "我想换到沐阳", Entities: []callbacks.IntentEntityTraceData{
					{Type: runtimeIntentEntityCustomerPhone, Text: base.Phone},
					{Type: runtimeIntentEntityOrderLocator, Text: base.OrderLocator},
				},
			}}
			task := goalContractMappedTask(t, text, state, map[string]string{
				"T1_route": "room_change", "T1_objective": "room_change_assessment",
				"T1_relation": "follow_up", "T1_dialogue_act": "follow_up",
				"T1_resolution": "resolved_from_context", "T1_context": "R1", "PHONE_1_role": "membership",
			})
			intent := applyRuntimePMSRequiredSlotPreflight(callbacks.IntentTraceData{
				IntentTasks: []callbacks.IntentTaskTraceData{task},
			}, base)
			task = intent.IntentTasks[0]
			if !task.NeedsTool || runtimeIntentEntityValue(task.Entities, runtimeIntentEntityCustomerPhone) != base.Phone ||
				runtimeIntentEntityValue(task.Entities, runtimeIntentEntityMemberPhone) != "13900139000" ||
				runtimeIntentEntityValue(task.Entities, runtimeIntentEntityOrderLocator) != base.OrderLocator {
				t.Fatalf("membership phone overwrote the reservation at preflight: %#v", task)
			}
			reply := replyTaskPlanFromIntentTask(task)
			input := runtimePMSReadPlanInputForTask(reply, base, time.Now())
			applyRuntimePMSCurrentTurnPhone(&input, reply, base, text)
			if input.Phone != base.Phone || input.MemberPhone != "13900139000" || input.ReceptOrderID != "REC-1" {
				t.Fatalf("execution changed the current order to membership identity: %#v", input)
			}
			for _, step := range buildPMSReadPlan(input).Steps {
				if step.ID == "member.benefits" && step.Args["phone"] != "13900139000" {
					t.Fatalf("membership query lost its separate identity: %#v", step)
				}
			}

			reply.TaskID = "room"
			reply.SupportedFacts = []callbacks.KnowledgeEvidenceFactTraceData{{Aspect: "pms_room_inventory", Statement: "沐阳有房。"}}
			trace := callbacks.RuntimeTraceData{Status: "completed"}
			trace.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{reply}
			trace.Output.CommitMessages = []callbacks.CommitMessageTraceData{{Status: "sent", TaskIDs: []string{"room"}}}
			snapshot := runtimePMSSessionLocatorFromTrace(trace)
			if snapshot.Phone != base.Phone || snapshot.MemberPhone != "13900139000" || snapshot.OrderLocator != base.OrderLocator {
				t.Fatalf("successful trace merged membership phone into the reservation: %#v", snapshot)
			}
			snapshot.SourceMessageID = 30
			snapshot = runtimePMSSessionLocatorFromHistoryWithBase(adapter.HistoryBuildResult{RawItems: []models.Message{{
				ID: 30, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: text,
			}}}, snapshot)
			followUp := runtimePMSReadPlanInputForTask(callbacks.ReplyTaskPlanTraceData{
				OriginalText: "那就这个吧，需要补多少？", SubIntent: "price_difference", SubjectScope: runtimeSubjectCurrentStay,
			}, snapshot, time.Now())
			if followUp.Phone != base.Phone || followUp.MemberPhone != "13900139000" || followUp.ReceptOrderID != "REC-1" {
				t.Fatalf("next turn did not retain the same reservation: %#v", followUp)
			}
		})
	}
}

func TestJevPhoneRolesSeparateReservationAndMemberInSameTurn(t *testing.T) {
	for _, text := range []string{
		"订房手机号13800138000，会员手机号13900139000，换房要补多少？",
		"换房用13800138000找订单，我的会员卡是13900139000注册的。",
	} {
		task := goalContractMappedTask(t, text, jevIntentState{}, map[string]string{
			"T1_route": "room_change", "T1_objective": "room_change_assessment",
			"PHONE_1_role": "reservation", "PHONE_2_role": "membership",
		})
		task = applyRuntimePMSRequiredSlotPreflight(callbacks.IntentTraceData{
			IntentTasks: []callbacks.IntentTaskTraceData{task},
		}, runtimePMSSessionLocator{}).IntentTasks[0]
		reply := replyTaskPlanFromIntentTask(task)
		input := runtimePMSReadPlanInputForTask(reply, runtimePMSSessionLocator{}, time.Now())
		applyRuntimePMSCurrentTurnPhone(&input, reply, runtimePMSSessionLocator{}, text)
		if input.Phone != "13800138000" || input.MemberPhone != "13900139000" {
			t.Fatalf("same-turn typed phone roles collapsed into the last number: %#v", input)
		}
	}
}

func TestJevMemberOnlyPhoneCannotLocateUnknownReservation(t *testing.T) {
	task := goalContractMappedTask(t, "会员手机号13900139000，想换个房", jevIntentState{}, map[string]string{
		"T1_route": "room_change", "T1_objective": "room_change_assessment", "PHONE_1_role": "membership",
	})
	task = applyRuntimePMSRequiredSlotPreflight(callbacks.IntentTraceData{
		IntentTasks: []callbacks.IntentTaskTraceData{task},
	}, runtimePMSSessionLocator{MemberPhone: "13900139000"}).IntentTasks[0]
	if task.NeedsTool || task.ResolvedText != runtimePMSOrderPhoneClarification {
		t.Fatalf("membership-only phone was silently treated as reservation phone: %#v", task)
	}
}

func TestJevPhoneRolesStayWithinTaskSpanEvenWithSharedSource(t *testing.T) {
	for _, tc := range []struct {
		name, first, second, route1, route2, role1, role2 string
	}{
		{"different orders", "查13800138000的退房时间。", "再看13900139000订的什么房。",
			"order_detail", "order_detail", "reservation", "reservation"},
		{"order and membership", "查13800138000订的什么房。", "再看13900139000的会员等级。",
			"order_detail", "member_info", "reservation", "membership"},
		{"different members", "13800138000的会员最晚几点退房。", "13900139000的会员最晚几点退房。",
			"member_benefits", "member_benefits", "membership", "membership"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spans := []jevIntentSpan{
				{Ref: "T1", SourceRef: "U1", Text: tc.first},
				{Ref: "T2", SourceRef: "U1", Text: tc.second},
			}
			questions, contexts := buildJevClassificationQuestions(spans, jevIntentState{})
			choices := map[string]string{
				"T1_route": tc.route1, "T2_route": tc.route2,
				"PHONE_1_role": tc.role1, "PHONE_2_role": tc.role2,
			}
			intent, err := buildIntentTraceFromJev(jevTestResponse(questions, choices, nil), spans, contexts)
			if err != nil || len(intent.IntentTasks) != 2 {
				t.Fatalf("distinct customer objects were merged: tasks=%#v err=%v", intent.IntentTasks, err)
			}
			intent = applyRuntimePMSRequiredSlotPreflight(intent, runtimePMSSessionLocator{})
			for index, phone := range []string{"13800138000", "13900139000"} {
				task := intent.IntentTasks[index]
				reply := replyTaskPlanFromIntentTask(task)
				input := runtimePMSReadPlanInputForTask(reply, runtimePMSSessionLocator{}, time.Now())
				applyRuntimePMSCurrentTurnPhone(&input, reply, runtimePMSSessionLocator{}, tc.first+tc.second)
				if input.Phone != phone {
					t.Fatalf("task %d executed another task's phone: task=%#v input=%#v", index, task, input)
				}
				other := "13900139000"
				if index == 1 {
					other = "13800138000"
				}
				for _, entity := range task.Entities {
					if strings.Contains(entity.Text, other) {
						t.Fatalf("task %d inherited the other object's locator: %#v", index, task)
					}
				}
			}
		})
	}
}

func TestJevMultiplePhonesForOneRoleMustClarifyWithoutPickingLast(t *testing.T) {
	for _, tc := range []struct{ text, route, role string }{
		{"13800138000和13900139000，帮我查订单。", "order_detail", "reservation"},
		{"会员电话13800138000或者13900139000，看看能几点退房。", "member_benefits", "membership"},
	} {
		task := goalContractMappedTask(t, tc.text, jevIntentState{}, map[string]string{
			"T1_route": tc.route, "PHONE_1_role": tc.role, "PHONE_2_role": tc.role,
		})
		intent := normalizeModelOwnedIntentTaskActions(callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{task}})
		intent = applyRuntimePMSRequiredSlotPreflight(intent, runtimePMSSessionLocator{
			Phone: "13700137000", OrderLocator: "接待单ID:OLD",
		})
		task = intent.IntentTasks[0]
		if task.NeedsTool || task.NeedsHumanRoute || task.ResolutionState != runtimeIntentResolutionAmbiguous ||
			task.SubIntent != "clarify" || len(task.Entities) != 0 {
			t.Fatalf("ambiguous role executed a guessed phone or reused an unrelated order: %#v", task)
		}
	}
}

func TestJevExplicitPhoneCorrectionKeepsOnlyActiveLookup(t *testing.T) {
	for _, text := range []string{
		"不是13800138000，是13900139000，查这个订单。",
		"原来填了13800138000，现在改成13900139000，帮我看一下。",
	} {
		task := goalContractMappedTask(t, text, jevIntentState{}, map[string]string{
			"T1_route": "order_detail", "T1_dialogue_act": "correction", "T1_relation": "correction",
			"PHONE_1_role": "not_for_lookup", "PHONE_2_role": "reservation",
		})
		base := runtimePMSSessionLocator{Phone: "13800138000", OrderLocator: "接待单ID:OLD"}
		task = applyRuntimePMSRequiredSlotPreflight(callbacks.IntentTraceData{
			IntentTasks: []callbacks.IntentTaskTraceData{task},
		}, base).IntentTasks[0]
		input := runtimePMSReadPlanInputForTask(replyTaskPlanFromIntentTask(task), base, time.Now())
		if !task.NeedsTool || input.Phone != "13900139000" || input.ReceptOrderID != "" {
			t.Fatalf("explicit replacement did not update identity and invalidate its order: task=%#v input=%#v", task, input)
		}
	}
}
