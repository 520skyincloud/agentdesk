package executor

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestJevStayEndDateIsTypedAndSurvivesRoomFollowup(t *testing.T) {
	for _, text := range []string{
		"我想换个房间，住到2026年9月29日中午",
		"今天帮我换房，2026年9月29日退房",
	} {
		t.Run(text, func(t *testing.T) {
			spans := []jevIntentSpan{{Ref: "T1", SourceRef: "U1", Text: text}}
			questions, contexts := buildJevClassificationQuestions(spans, jevIntentState{})
			choices := map[string]string{
				"T1_route": "room_change", "T1_scope": runtimeSubjectCurrentStay,
				"T1_objective": "availability", "T1_dialogue_act": "new_request",
			}
			for key, candidate := range contexts {
				if candidate.DateValue != "" {
					choices[key] = "ignored"
					if candidate.DateValue == "2026-09-29" {
						choices[key] = "end"
					}
				}
			}
			intent, err := buildIntentTraceFromJev(jevTestResponse(questions, choices, nil), spans, contexts)
			if err != nil {
				t.Fatal(err)
			}
			first := intent.IntentTasks[0]
			state := jevIntentState{RecentBusinessTask: &jevIntentPriorTaskState{
				Ref: "R1", Text: first.Text, ResolvedText: first.ResolvedText,
				Intent: first.Intent, SubIntent: first.SubIntent, SubjectScope: first.SubjectScope,
				Entities: first.Entities,
			}}
			for _, followup := range []string{"随便帮我挑个合适的", "那就看看沐阳吧，有房吗？"} {
				target := "none"
				if strings.Contains(followup, "沐阳") {
					target = "current"
				}
				mapped := goalContractMappedTask(t, followup, state, map[string]string{
					"T1_route": "room_inventory", "T1_scope": "inherit_context",
					"T1_context": "R1", "T1_target_ref": target, "T1_objective": "availability",
				})
				if mapped.SubIntent != "room_change" || mapped.SubjectScope != runtimeSubjectCurrentStay {
					t.Fatalf("active room-change route lost: %#v", mapped)
				}
				start, end := runtimePMSReadDates(replyTaskPlanFromIntentTask(mapped), pmsReadScenarioRoomChange, time.Now())
				if start != "" || end != "2026-09-29" {
					t.Fatalf("end date became a new arrival: %q / %q", start, end)
				}
			}
		})
	}
}

func TestJevRoomPronounUsesNamedCustomerSelection(t *testing.T) {
	state := jevIntentState{RecentBusinessTask: &jevIntentPriorTaskState{
		Ref: "R1", Intent: "hotel_info", SubIntent: "room_change",
		SubjectScope: runtimeSubjectCurrentStay, Text: "那就看看沐阳吧，有房吗",
		SelectionSource: "customer", SelectionRef: "U1",
		Entities: []callbacks.IntentEntityTraceData{
			{Type: runtimeIntentEntityTargetRoomType, Text: "沐阳"},
			{Type: runtimeIntentEntityStayEndDate, Text: "2026-09-29"},
		},
	}}
	for _, text := range []string{"换成这个需要补多少？", "那间的差价是多少？"} {
		task := goalContractMappedTask(t, text, state, map[string]string{
			"T1_route": "price_difference", "T1_scope": "inherit_context",
			"T1_context": "none", "T1_target_ref": "R1", "T1_objective": "price_difference",
		})
		if got := runtimePMSTargetRoomTypeText(replyTaskPlanFromIntentTask(task)); got != "沐阳" {
			t.Fatalf("price question was parsed as room name: %q", got)
		}
		if task.SelectionSource != "customer" || task.SelectionRef != "R1" {
			t.Fatalf("customer choice became a service recommendation: %#v", task)
		}
	}
	for _, text := range []string{"换成这个需要补多少", "这个房型的价格呢"} {
		task := callbacks.ReplyTaskPlanTraceData{SubjectScope: runtimeSubjectCurrentStay, OriginalText: text}
		if got := runtimePMSTargetRoomTypeText(task); got != "" {
			t.Fatalf("unbound scoped pronoun gained a target: %q", got)
		}
	}
}

func TestJevNamedFirstRoomChoiceUsesCatalogBoundText(t *testing.T) {
	for _, text := range []string{"沐阳现在有房吗", "可以帮我换成云漫吗", "换成沐阳，住到2026年9月29日"} {
		task := goalContractMappedTask(t, text, jevIntentState{}, map[string]string{
			"T1_route": "room_change", "T1_scope": runtimeSubjectCurrentStay,
			"T1_dialogue_act": "selection", "T1_objective": "availability",
		})
		if got := runtimePMSTargetRoomTypeText(replyTaskPlanFromIntentTask(task)); got != text {
			t.Fatalf("named customer request should be matched against real catalog intact: %q", got)
		}
	}
}

func TestJevHistoricalPhoneCorrectionRetainsLatestSelection(t *testing.T) {
	state := jevIntentState{RecentBusinessTask: &jevIntentPriorTaskState{
		Ref: "R1", Intent: "hotel_info", SubIntent: "order_query",
		SubjectScope: runtimeSubjectHistoricalOrder, Text: "上次住的订单多少钱",
		Entities: []callbacks.IntentEntityTraceData{
			{Type: runtimeIntentEntityCustomerPhone, Text: "13800138000"},
			{Type: runtimeIntentEntityHistoryChoice, Text: "latest"},
		},
	}}
	for _, text := range []string{
		"换成13900139000，上次订的什么房？",
		"手机号改为13900139000，最近一次住的哪种房？",
	} {
		task := goalContractMappedTask(t, text, state, map[string]string{
			"T1_route": "order_detail", "T1_scope": "latest_history",
			"T1_context": "R1", "T1_relation": "correction", "T1_dialogue_act": "correction",
			"T1_objective": "room_type", "PHONE_1_role": "reservation",
		})
		if task.SubjectScope != runtimeSubjectHistoricalOrder || task.SubIntent != "order_query" ||
			runtimeIntentEntityValue(task.Entities, runtimeIntentEntityCustomerPhone) != "13900139000" ||
			runtimeIntentEntityValue(task.Entities, runtimeIntentEntityHistoryChoice) != "latest" {
			t.Fatalf("phone correction lost historical/latest semantics: %#v", task)
		}
		next := goalContractMappedTask(t, "那次是哪天入住？", jevIntentState{
			RecentBusinessTask: &jevIntentPriorTaskState{
				Ref: "R1", Text: task.Text, Intent: task.Intent, SubIntent: task.SubIntent,
				SubjectScope: task.SubjectScope, Entities: task.Entities,
			},
		}, map[string]string{
			"T1_route": "order_detail", "T1_scope": "inherit_context", "T1_context": "R1",
			"T1_objective": "checkin_time",
		})
		if next.SubjectScope != runtimeSubjectHistoricalOrder ||
			runtimeIntentEntityValue(next.Entities, runtimeIntentEntityHistoryChoice) != "latest" {
			t.Fatalf("historical followup lost latest scope: %#v", next)
		}
	}
}

func TestJevRoomAttributeKeepsPolicyAndLiveQueries(t *testing.T) {
	for _, text := range []string{"想换安静一点、不临街的房间", "有离电梯远一点的房间吗"} {
		spans := []jevIntentSpan{{Ref: "T1", SourceRef: "U1", Text: text}}
		questions, contexts := buildJevClassificationQuestions(spans, jevIntentState{})
		intent, err := buildIntentTraceFromJev(jevTestResponse(questions, map[string]string{
			"T1_route": "room_change", "T1_scope": runtimeSubjectCurrentStay,
		}, map[string]float64{"T1_policy": 1}), spans, contexts)
		if err != nil {
			t.Fatal(err)
		}
		mapped := normalizeModelOwnedIntentTaskActions(intent).IntentTasks[0]
		if !mapped.NeedsKnowledge || !mapped.NeedsTool || mapped.NeedsHumanRoute {
			t.Fatalf("typed policy/live query contract overwritten: %#v", mapped)
		}
	}
}

func TestPMSRemainingStayDatesUseHotelTodayFloor(t *testing.T) {
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)
	for _, originalCheckIn := range []string{"2026-09-27 14:00:00", "2026-09-28 14:00:00"} {
		task := callbacks.ReplyTaskPlanTraceData{
			SubIntent: "room_change", SubjectScope: runtimeSubjectCurrentStay,
			Entities: []callbacks.IntentEntityTraceData{
				{Type: runtimeIntentEntityStayEndDate, Text: "2026-09-29"},
			},
		}
		input := runtimePMSReadPlanInputForTask(task, runtimePMSSessionLocator{Phone: "13800138000"}, now)
		input.TargetRoomTypeID = "R2"
		plan := buildPMSReadPlan(input)
		results := map[string]pmsReadStepResult{
			"order.recept": {Status: pmsReadStepOK, Data: map[string]any{
				"receptOrderId": "REC-1", "checkInTime": originalCheckIn,
				"checkOutTime": "2026-09-28 12:00:00", "roomId": "R1",
			}},
		}
		for _, id := range []string{"inventory.stay", "stay.room_availability", "price.board", "price.difference"} {
			step, ok := runtimePMSReadPlanStepByID(plan, id)
			if !ok {
				t.Fatalf("missing required date-dependent step %s", id)
			}
			args, status, message := resolveRuntimePMSReadStepArgs(step, results)
			if status != "" || args["beginTime"] != "2026-09-28" || args["endTime"] != "2026-09-29" {
				t.Fatalf("%s used stale/new-arrival dates: %#v %s %s", id, args, status, message)
			}
		}
	}
}

func TestPMSDateFloorKeepsFutureArrivalAndRejectsExpiredRange(t *testing.T) {
	for _, tc := range []struct {
		checkIn, checkOut, start string
		valid                    bool
	}{
		{"2026-10-01", "2026-10-03", "2026-10-01", true},
		{"2026-09-27", "2026-09-28", "2026-09-28", false},
	} {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: pmsReadScenarioRoomChange, Phone: "13800138000", EarliestStartDate: "2026-09-28",
		})
		step, _ := runtimePMSReadPlanStepByID(plan, "inventory.stay")
		args, status, _ := resolveRuntimePMSReadStepArgs(step, map[string]pmsReadStepResult{
			"order.recept": {Status: pmsReadStepOK, Data: map[string]any{
				"receptOrderId": "REC-1", "checkInTime": tc.checkIn, "checkOutTime": tc.checkOut,
			}},
		})
		if args["beginTime"] != tc.start || (status == "") != tc.valid {
			t.Fatalf("date floor changed future arrival or queried expired stay: %#v %s", args, status)
		}
	}
}

func TestPMSLatestHistoricalSelectionUsesChronologyNotRowOrder(t *testing.T) {
	older := map[string]any{"checkInTime": "2020-09-23 20:03:00", "roomName": "星旗"}
	newer := map[string]any{"checkInTime": "2020-09-24 13:53:00", "roomName": "云漫"}
	task := callbacks.ReplyTaskPlanTraceData{
		SubjectScope: runtimeSubjectHistoricalOrder, RequestedAspects: []string{"room_type"},
		Entities: []callbacks.IntentEntityTraceData{{Type: runtimeIntentEntityHistoryChoice, Text: "latest"}},
	}
	for _, rows := range [][]any{{older, newer}, {newer, older}} {
		facts := runtimePMSHistoryFacts(task, map[string]any{"rows": rows})
		answer := runtimePMSFactStatementsForTest(facts)
		if !strings.Contains(answer, "云漫") || strings.Contains(answer, "星旗") || strings.Contains(answer, "需要客户选择") {
			t.Fatalf("latest historical record was not selected by actual stay time: %s", answer)
		}
	}
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, runtimeHotelLocation())
	for _, rows := range [][]map[string]any{
		{older, {"checkInTime": "2020-09-23 20:03:00", "roomName": "云漫"}},
		{older, {"roomName": "云漫"}},
	} {
		if _, ok := runtimePMSLatestHistoryRow(rows, now); ok {
			t.Fatalf("tied/missing timestamps must not choose first row: %#v", rows)
		}
	}
	future := runtimePMSHistoryFacts(task, map[string]any{"rows": []any{
		map[string]any{"checkInTime": "2099-09-29 12:00:00", "roomName": "未来房型"},
	}})
	if answer := runtimePMSFactStatementsForTest(future); strings.Contains(answer, "未来房型") || !strings.Contains(answer, "不能确认") {
		t.Fatalf("future reservation became latest historical stay: %s", answer)
	}
}

func TestPMSCheckoutTimeIsScheduledNotInferredActualStatus(t *testing.T) {
	now := time.Date(2026, 9, 28, 5, 0, 0, 0, time.UTC)
	for _, value := range []string{"2026-09-28 12:00:00", "2026-09-28T12:00:00+08:00"} {
		statement := runtimePMSCurrentCheckoutStatement(value, now)
		if !strings.Contains(statement, "原定") || !strings.Contains(statement, "该时间已过") ||
			!strings.Contains(statement, "9月28日12点") || strings.Contains(statement, "已退房") {
			t.Fatalf("expired scheduled date became actual checkout/current deadline: %s", statement)
		}
	}
	statement := runtimePMSCurrentCheckoutStatement("2026-09-29 12:00:00", now)
	if !strings.Contains(statement, "约定") || strings.Contains(statement, "该时间已过") {
		t.Fatalf("future checkout marked elapsed: %s", statement)
	}
}

func TestJevGoalSlotQuestionsAreCandidateBounded(t *testing.T) {
	spans := []jevIntentSpan{{Ref: "T1", SourceRef: "U1", Text: "酒店有停车场吗"}}
	base, _ := buildJevClassificationQuestions(spans, jevIntentState{})
	if len(base) != 8 {
		t.Fatalf("ordinary question unnecessarily gained room/date slots: %d", len(base))
	}
	spans[0].Text = "住到2026年9月29日，29日中午退房"
	withDates, _ := buildJevClassificationQuestions(spans, jevIntentState{})
	if len(withDates) != 9 {
		t.Fatalf("same date should create only one role question: %d", len(withDates))
	}
	withRoom, _ := buildJevClassificationQuestions(spans, jevIntentState{RecentBusinessTask: &jevIntentPriorTaskState{
		Ref: "R1", Intent: "hotel_info", SubIntent: "room_change", Text: "换个房间",
		SubjectScope: runtimeSubjectCurrentStay,
	}})
	if len(withRoom) != 10 {
		t.Fatalf("active room context should add one bounded target choice: %d", len(withRoom))
	}
}

func TestJevExternalProxyKeepsSelfServiceKnowledgeButNoExecution(t *testing.T) {
	for _, text := range []string{"帮我点个外卖", "能替我叫辆车吗"} {
		task := goalContractMappedTask(t, text, jevIntentState{}, map[string]string{
			"T1_route": "external_proxy_action", "T1_objective": "action_request",
		})
		if !task.NeedsKnowledge {
			t.Fatalf("derived typed intent cleared self-service knowledge: %#v", task)
		}
		task = semanticGateRestrictTaskActions(task)
		if !task.NeedsKnowledge || task.NeedsTool || task.NeedsHumanRoute || task.NeedsResource ||
			task.SubIntent != "external_proxy_action" {
			t.Fatalf("external execution restriction blocked useful self-service knowledge: %#v", task)
		}
		legacy := applyRuntimeCustomerScenarioIntentCorrections(callbacks.IntentTraceData{
			IntentTasks: []callbacks.IntentTaskTraceData{{Intent: "service_request", SubIntent: "external_proxy_action", Text: text}},
		}).IntentTasks[0]
		if !legacy.NeedsKnowledge || legacy.NeedsTool || legacy.NeedsHumanRoute {
			t.Fatalf("legacy correction suppressed self-service query: %#v", legacy)
		}
	}
}
