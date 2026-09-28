package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestJevActiveGoalSnapshotSurvivesOlderNamedRoomReference(t *testing.T) {
	for _, preference := range []string{
		"我主要想安静点，不想靠马路，只告诉我有哪些选择",
		"那几间里面有不靠电梯的吗？先别转人工",
	} {
		t.Run(preference, func(t *testing.T) {
			previous := callbacks.IntentTaskTraceData{
				Intent: "hotel_info", SubIntent: "room_change", SubjectScope: runtimeSubjectCurrentStay,
				Text: "我想换房，住到9月29日中午", ResolvedText: "我想换房，住到9月29日中午",
				Entities: []callbacks.IntentEntityTraceData{
					{Type: runtimeIntentEntityStayEndDate, Text: "2026-09-29"},
					{Type: runtimeIntentEntityCustomerPhone, Text: "13800138000"},
					{Type: runtimeIntentEntityOrderLocator, Text: "接待单ID:REC-1"},
				},
			}
			for index, current := range []struct {
				text, act, objective, target, context string
			}{
				{"房型我也不懂，帮我挑个合适的吧", "recommendation", "recommendation", "none", "R1"},
				{"那就看看沐阳吧，有房吗？", "selection", "availability", "current", "R1"},
				{"换成这个补多少？会员手机号13900139000能免吗", "follow_up", "room_change_assessment", "H5", "H5"},
				{preference, "follow_up", "recommendation", "H5", "H5"},
			} {
				state := jevIntentState{
					RecentBusinessTask: &jevIntentPriorTaskState{
						Ref: "R1", Intent: previous.Intent, SubIntent: previous.SubIntent,
						SubjectScope: previous.SubjectScope, Text: previous.Text, ResolvedText: previous.ResolvedText,
						Entities: previous.Entities, SelectionSource: previous.SelectionSource, SelectionRef: previous.SelectionRef,
					},
					History: []jevIntentText{{Ref: "H5", Role: "customer", Text: "那就看看沐阳吧，有房吗？"}},
				}
				previous = goalContractMappedTask(t, current.text, state, map[string]string{
					"T1_route": "room_change", "T1_scope": "continue_R1",
					"T1_context": current.context, "T1_target_ref": current.target,
					"T1_objective": current.objective, "T1_dialogue_act": current.act,
					"T1_relation": "follow_up", "T1_resolution": "resolved_from_context",
					"PHONE_1_role": "membership",
				})
				if runtimeIntentEntityValue(previous.Entities, runtimeIntentEntityStayEndDate) != "2026-09-29" ||
					runtimeIntentEntityValue(previous.Entities, runtimeIntentEntityCustomerPhone) != "13800138000" ||
					runtimeIntentEntityValue(previous.Entities, runtimeIntentEntityOrderLocator) != "接待单ID:REC-1" {
					t.Fatalf("round %d replaced the current goal with a short historical sentence: %#v", index+2, previous)
				}
				if index >= 2 && runtimeIntentEntityValue(previous.Entities, runtimeIntentEntityMemberPhone) != "13900139000" {
					t.Fatalf("round %d lost the separately supplied member identity: %#v", index+2, previous)
				}
				if !strings.Contains(previous.ResolvedText, "住到9月29日") {
					t.Fatalf("round %d lost the bounded original goal: %s", index+2, previous.ResolvedText)
				}
			}
		})
	}
}

func TestJevGoalScopeSelectsOneSnapshotWithoutBlendingOtherGoal(t *testing.T) {
	state := jevIntentState{RecentBusinessTasks: []jevIntentPriorTaskState{
		{
			Ref: "R1", Intent: "hotel_info", SubIntent: "room_change", SubjectScope: runtimeSubjectCurrentStay,
			Text: "我的订单换成沐阳", Entities: []callbacks.IntentEntityTraceData{
				{Type: runtimeIntentEntityStayEndDate, Text: "2026-09-29"},
				{Type: runtimeIntentEntityCustomerPhone, Text: "13800138000"},
			},
		},
		{
			Ref: "R2", Intent: "hotel_info", SubIntent: "room_change", SubjectScope: runtimeSubjectCurrentStay,
			Text: "朋友的订单也想换成沐阳", Entities: []callbacks.IntentEntityTraceData{
				{Type: runtimeIntentEntityStayEndDate, Text: "2026-10-01"},
				{Type: runtimeIntentEntityCustomerPhone, Text: "13900139000"},
			},
		},
	}, History: []jevIntentText{{Ref: "H5", Role: "customer", Text: "那就看看沐阳吧"}}}
	for _, tc := range []struct{ ref, date, phone string }{
		{"R1", "2026-09-29", "13800138000"},
		{"R2", "2026-10-01", "13900139000"},
	} {
		task := goalContractMappedTask(t, "这个要加多少钱", state, map[string]string{
			"T1_route": "price_difference", "T1_scope": "continue_" + tc.ref,
			"T1_context": "H5", "T1_target_ref": "H5", "T1_objective": "price_difference",
			"T1_relation": "follow_up", "T1_dialogue_act": "follow_up",
		})
		if runtimeIntentEntityValue(task.Entities, runtimeIntentEntityStayEndDate) != tc.date ||
			runtimeIntentEntityValue(task.Entities, runtimeIntentEntityCustomerPhone) != tc.phone {
			t.Fatalf("selected snapshot blended another stay: %#v", task)
		}
	}
}

func TestJevScopeChoicesOnlyContinueBoundedSnapshots(t *testing.T) {
	state := jevIntentState{RecentBusinessTask: &jevIntentPriorTaskState{
		Ref: "R1", Intent: "hotel_info", SubIntent: "room_change", SubjectScope: runtimeSubjectCurrentStay,
		Text: "想换房", Entities: []callbacks.IntentEntityTraceData{{Type: runtimeIntentEntityStayEndDate, Text: "2026-09-29"}},
	}, History: []jevIntentText{{Ref: "H5", Role: "customer", Text: "沐阳有房吗"}}}
	spans := []jevIntentSpan{{Ref: "T1", SourceRef: "U1", Text: "那个安静吗"}}
	questions, contexts := buildJevClassificationQuestions(spans, state)
	scopes := jevTestCriteria(questions["T1_scope"].Criteria)
	if _, exists := scopes["continue_R1"]; !exists {
		t.Fatal("active task snapshot is not an explicit goal-scope choice")
	}
	for _, invalid := range []string{"continue_H5", "continue_R2", "inherit_context"} {
		if _, exists := scopes[invalid]; exists {
			t.Fatalf("scope permits missing or untyped business ownership: %s", invalid)
		}
	}
	if len(questions) != 9 {
		t.Fatalf("snapshot identity added another classification question: %d", len(questions))
	}
	for _, invalid := range []string{"continue_H5", "continue_R2"} {
		response := jevTestResponse(questions, map[string]string{
			"T1_route": "room_change", "T1_scope": invalid, "T1_context": "H5",
		}, nil)
		if _, err := buildIntentTraceFromJev(response, spans, contexts); err == nil {
			t.Fatalf("invalid snapshot reference was accepted: %s", invalid)
		}
	}
	task := goalContractMappedTask(t, "改问新订单", state, map[string]string{
		"T1_route": "order_query", "T1_scope": runtimeSubjectCurrentOrder,
		"T1_context": "none", "T1_target_ref": "none",
	})
	if runtimeIntentEntityValue(task.Entities, runtimeIntentEntityStayEndDate) != "" {
		t.Fatalf("new independent order inherited the old goal: %#v", task)
	}
}

func TestJevCancellationDoesNotRetainBusinessSnapshotScope(t *testing.T) {
	for _, text := range []string{"先不换了", "不考虑这个了"} {
		task := goalContractMappedTask(t, text, jevIntentState{RecentBusinessTask: &jevIntentPriorTaskState{
			Ref: "R1", Intent: "hotel_info", SubIntent: "room_change", SubjectScope: runtimeSubjectCurrentStay,
			Text: "想换房", Entities: []callbacks.IntentEntityTraceData{{Type: runtimeIntentEntityStayEndDate, Text: "2026-09-29"}},
		}}, map[string]string{
			"T1_route": "room_change", "T1_scope": "continue_R1",
			"T1_context": "R1", "T1_dialogue_act": "cancellation", "T1_objective": "cancel",
			"T1_relation": "cancel_previous",
		})
		if task.SubjectScope != "" || len(task.Entities) != 0 || task.NeedsTool {
			t.Fatalf("cancelled goal retained an executable business scope: %#v", task)
		}
	}
}
