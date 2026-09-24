package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"agent-desk/internal/ai/jev"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/pkg/utils"
)

func TestJevIntentSixQuestionsSharePhysicalSourceAndRetainOrder(t *testing.T) {
	texts := []string{"酒店有停车场吗？", "几点退房？", "帮我查订单。", "会员有哪些权益？", "能升房吗？", "发一下入住小程序。"}
	sources := []adapter.CurrentTurnSource{{Ref: "U1", Text: strings.Join(texts, "")}}
	choices := map[string]string{"U1_count": "6"}
	offset := 0
	for index, text := range texts {
		if index > 0 {
			choices[fmt.Sprintf("U1_start_%d", index+1)] = strconv.Itoa(offset)
		}
		offset += len([]rune(text))
	}
	spans := jevTestSegment(t, sources, choices)
	if len(spans) != len(texts) {
		t.Fatalf("got %d tasks, want six: %#v", len(spans), spans)
	}
	routes := []string{"parking", "checkout_process", "order_query", "member_benefits", "room_upgrade", "provide_mini_program"}
	for index, span := range spans {
		wantRef := fmt.Sprintf("T%d", index+1)
		if span.Ref != wantRef || span.SourceRef != "U1" || span.Text != texts[index] {
			t.Fatalf("task %d changed text, order or provenance: %#v", index, span)
		}
		choices[span.Ref+"_route"] = routes[index]
	}
	classification, contexts := buildJevClassificationQuestions(spans, jevIntentState{})
	result, err := buildIntentTraceFromJev(jevTestResponse(classification, choices, nil), spans, contexts)
	if err != nil {
		t.Fatal(err)
	}
	for index, task := range result.IntentTasks {
		if task.Text != texts[index] || task.SubIntent != routes[index] || !reflect.DeepEqual(task.SourceRefs, []string{"U1"}) {
			t.Fatalf("mapped task %d lost its question: %#v", index, task)
		}
	}
	if !result.NeedsKnowledge || !result.NeedsTool || !result.NeedsResource {
		t.Fatalf("mixed knowledge/PMS/resource capabilities lost: %#v", result)
	}
}

func TestJevBoundariesDoNotSplitIdentifiersOrAssumePunctuation(t *testing.T) {
	text := "手机号13800138000订单ABC-123查一下还有停车场吗"
	sources := []adapter.CurrentTurnSource{{Ref: "U1", Text: text}}
	runes := []rune(text)
	for index := 1; index < len(runes); index++ {
		if runes[index] >= '0' && runes[index] <= '9' && runes[index-1] >= '0' && runes[index-1] <= '9' {
			if jevCandidateBoundary(runes, index) {
				t.Fatalf("identifier has a candidate boundary at %d", index)
			}
		}
	}
	offset := len([]rune("手机号13800138000订单ABC-123查一下"))
	spans := jevTestSegment(t, sources, map[string]string{"U1_count": "2", "U1_start_2": strconv.Itoa(offset)})
	if len(spans) != 2 || spans[0].Text+spans[1].Text != text || spans[1].Text != "还有停车场吗" {
		t.Fatalf("unpunctuated independent request not preserved: %#v", spans)
	}
	one := jevTestSegment(t, []adapter.CurrentTurnSource{{Ref: "U1", Text: "不是这个号码，是13800138000。"}}, nil)
	if len(one) != 1 || one[0].Text != "不是这个号码，是13800138000。" {
		t.Fatalf("punctuation must not independently split or rewrite a correction: %#v", one)
	}
	withSlotAfterPeriod := "帮我查订单。手机号13800138000，还有停车场吗"
	parkingOffset := len([]rune("帮我查订单。手机号13800138000，"))
	spans = jevTestSegment(t, []adapter.CurrentTurnSource{{Ref: "U1", Text: withSlotAfterPeriod}}, map[string]string{
		"U1_count": "2", "U1_terminal_alignment": "not_exact", "U1_start_2": strconv.Itoa(parkingOffset),
	})
	if len(spans) != 2 || spans[0].Text != "帮我查订单。手机号13800138000，" || spans[1].Text != "还有停车场吗" {
		t.Fatalf("sentence punctuation split the order lookup from its phone slot: %#v", spans)
	}
}

func TestJevMixedOrderParkingBoundariesRecoverMechanically(t *testing.T) {
	text := "\u8fd9\u7b14\u8ba2\u5355\u51e0\u70b9\u9000\u623f\uff1f\u9152\u5e97\u505c\u8f66\u6536\u8d39\u5417\uff0c\u5165\u53e3\u5728\u54ea\uff1f"
	first := len([]rune("\u8fd9\u7b14\u8ba2\u5355\u51e0\u70b9\u9000\u623f\uff1f"))
	second := len([]rune("\u8fd9\u7b14\u8ba2\u5355\u51e0\u70b9\u9000\u623f\uff1f\u9152\u5e97\u505c\u8f66\u6536\u8d39\u5417\uff0c"))
	want := []string{
		"\u8fd9\u7b14\u8ba2\u5355\u51e0\u70b9\u9000\u623f\uff1f",
		"\u9152\u5e97\u505c\u8f66\u6536\u8d39\u5417\uff0c",
		"\u5165\u53e3\u5728\u54ea\uff1f",
	}
	for _, test := range []struct {
		name   string
		start2 int
		start3 int
	}{
		{name: "ordered", start2: first, start3: second},
		{name: "reversed", start2: second, start3: first},
	} {
		t.Run(test.name, func(t *testing.T) {
			spans := jevTestSegment(t, []adapter.CurrentTurnSource{{Ref: "U1", Text: text}}, map[string]string{
				"U1_count": "3", "U1_terminal_alignment": "not_exact",
				"U1_start_2": strconv.Itoa(test.start2), "U1_start_3": strconv.Itoa(test.start3),
			})
			if len(spans) != len(want) {
				t.Fatalf("got %d spans, want %d: %#v", len(spans), len(want), spans)
			}
			for index := range want {
				if spans[index].Text != want[index] {
					t.Fatalf("span %d=%q, want %q", index, spans[index].Text, want[index])
				}
			}
		})
	}
}

func TestJevMixedOrderParkingBoundaryRetriesInvalidSelectionOnce(t *testing.T) {
	text := "\u8fd9\u7b14\u8ba2\u5355\u51e0\u70b9\u9000\u623f\uff1f\u9152\u5e97\u505c\u8f66\u6536\u8d39\u5417\uff0c\u5165\u53e3\u5728\u54ea\uff1f"
	first := len([]rune("\u8fd9\u7b14\u8ba2\u5355\u51e0\u70b9\u9000\u623f\uff1f"))
	second := len([]rune("\u8fd9\u7b14\u8ba2\u5355\u51e0\u70b9\u9000\u623f\uff1f\u9152\u5e97\u505c\u8f66\u6536\u8d39\u5417\uff0c"))
	for _, test := range []struct {
		name      string
		badStart3 string
	}{
		{name: "duplicate", badStart3: strconv.Itoa(first)},
		{name: "invalid", badStart3: "999"},
	} {
		t.Run(test.name, func(t *testing.T) {
			startCalls := 0
			spans, err := segmentJevIntentSources(
				[]adapter.CurrentTurnSource{{Ref: "U1", Text: text}},
				jevIntentState{},
				func(_ any, questions map[string]jev.Question) (jev.Response, error) {
					if _, ok := questions["U1_count"]; ok {
						return jevTestResponse(questions, map[string]string{
							"U1_count": "3", "U1_terminal_alignment": "not_exact",
						}, nil), nil
					}
					startCalls++
					choices := map[string]string{
						"U1_start_2": strconv.Itoa(first),
						"U1_start_3": strconv.Itoa(second),
					}
					if startCalls == 1 {
						choices["U1_start_3"] = test.badStart3
					}
					return jevTestResponse(questions, choices, nil), nil
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if startCalls != 2 {
				t.Fatalf("boundary selection calls=%d, want exactly two", startCalls)
			}
			if len(spans) != 3 || spans[0].Text != "\u8fd9\u7b14\u8ba2\u5355\u51e0\u70b9\u9000\u623f\uff1f" || spans[1].Text != "\u9152\u5e97\u505c\u8f66\u6536\u8d39\u5417\uff0c" || spans[2].Text != "\u5165\u53e3\u5728\u54ea\uff1f" {
				t.Fatalf("unexpected recovered spans: %#v", spans)
			}
		})
	}
}

func TestJevSegmentationKeepsDecisionDimensionsInOneGoal(t *testing.T) {
	for _, text := range []string{
		"能不能升大床房，有没有房，会员能免差价吗，要补多少？",
		"房间太吵想换房，今晚有没有别的房，差价多少？",
	} {
		checked := false
		spans, err := segmentJevIntentSources([]adapter.CurrentTurnSource{{Ref: "U1", Text: text}}, jevIntentState{}, func(_ any, questions map[string]jev.Question) (jev.Response, error) {
			if question, ok := questions["U1_count"]; ok {
				instructions, _ := question.Instructions.(map[string]any)
				rule, _ := instructions["question"].(string)
				if !strings.Contains(rule, "dimensions of that one decision goal") {
					t.Fatalf("decision dimensions are not grouped in JEV segmentation: %s", rule)
				}
				checked = true
			}
			return jevTestResponse(questions, map[string]string{"U1_count": "1"}, nil), nil
		})
		if err != nil || !checked || len(spans) != 1 || spans[0].Text != text {
			t.Fatalf("decision request was not preserved as one goal: spans=%#v checked=%v err=%v", spans, checked, err)
		}
	}
}

func TestJevRouteCriteriaSeparateBroadDecisionsFromStandaloneFollowups(t *testing.T) {
	criteria := jevIntentRouteCriteria()
	for _, route := range []string{"room_upgrade", "room_change", "late_checkout", "renewal"} {
		description, _ := criteria[route].(string)
		if !strings.Contains(description, "One ") || !strings.Contains(description, " when asked together") {
			t.Fatalf("%s must describe one combined customer decision: %q", route, description)
		}
	}
	for _, route := range []string{"price_difference", "upgrade_eligibility"} {
		description, _ := criteria[route].(string)
		if !strings.Contains(description, "standalone") || !strings.Contains(description, "broader current") {
			t.Fatalf("%s must remain a standalone follow-up only: %q", route, description)
		}
	}
}

func TestJevRouteCriteriaKeepsSpecificOrderCheckoutFollowupsOnPMS(t *testing.T) {
	criteria := jevIntentRouteCriteria()
	checkout, _ := criteria["checkout_process"].(string)
	orderDetail, _ := criteria["order_detail"].(string)
	if !strings.Contains(checkout, "no specific order") || !strings.Contains(checkout, "selected history context") {
		t.Fatalf("general checkout route must exclude an identified order: %q", checkout)
	}
	if !strings.Contains(orderDetail, "original/latest checkout time") {
		t.Fatalf("specific order checkout follow-ups must remain order_detail: %q", orderDetail)
	}
	for _, phrase := range []string{"this order", "my original/latest checkout time"} {
		if !strings.Contains(jevIntentClassificationRules, phrase) {
			t.Fatalf("classification rules are missing contextual order phrase %q", phrase)
		}
	}
}

func TestJevMaintenanceTicketFollowupsBindExistingConfirmationTool(t *testing.T) {
	for _, current := range []string{
		"需要，帮我登记",
		"可以，麻烦建个维修工单",
	} {
		span := jevIntentSpan{Ref: "T1", SourceRef: "U1", Text: current}
		state := jevIntentState{History: []jevIntentText{
			{Ref: "H0", Role: "customer", Text: "空调不制冷，先不要转人工"},
			{Ref: "H1", Role: "service", Text: ungroundedMaintenanceOfferNoHandoffReply},
		}}
		questions, contexts := buildJevClassificationQuestions([]jevIntentSpan{span}, state)
		response := jevTestResponse(questions, map[string]string{
			"T1_route":      "create_ticket",
			"T1_objective":  "action_request",
			"T1_relation":   "follow_up",
			"T1_resolution": "resolved_from_context",
			"T1_context":    "H0",
		}, nil)

		intent, err := buildIntentTraceFromJev(response, []jevIntentSpan{span}, contexts)
		if err != nil {
			t.Fatalf("build ticket follow-up intent for %q: %v", current, err)
		}
		intent = retainRuntimeTicketTools(intent)
		if len(intent.IntentTasks) != 1 {
			t.Fatalf("ticket follow-up %q changed task count: %#v", current, intent.IntentTasks)
		}
		task := intent.IntentTasks[0]
		if task.Intent != "service_request" || task.SubIntent != "create_ticket" || !task.NeedsTool || task.NeedsKnowledge || task.NeedsHumanRoute {
			t.Fatalf("ticket follow-up %q did not bind the existing confirmation tool: %#v", current, task)
		}
		if !containsString(intent.ToolCodes, toolx.GraphCreateTicketConfirm.Code) || !strings.Contains(task.ResolvedText, "空调不制冷") {
			t.Fatalf("ticket follow-up %q lost its tool or maintenance context: intent=%#v task=%#v", current, intent, task)
		}
	}
}

func TestJevCurrentContextUsesEarlierSpansWithoutChangingSource(t *testing.T) {
	message := models.Message{
		ID: 103, MessageType: enums.IMMessageTypeText,
		Content: utils.BuildRuntimeCustomerBurstEnvelope([]string{
			"1. [消息101] 帮我查询订单",
			"2. [消息102] 13800138000",
			"3. [消息103] 酒店有停车场吗",
		}),
	}
	sources := adapter.BuildCurrentTurnSources(message)
	spans := jevTestSegment(t, sources, nil)
	state := buildJevIntentState(RunInput{UserMessage: message}, adapter.HistoryBuildResult{}, sources)
	questions, contexts := buildJevClassificationQuestions(spans, state)
	options := jevTestCriteria(questions["T2_context"].Criteria)
	if _, exists := options["T1"]; !exists {
		t.Fatal("second message cannot reference the first message")
	}
	for _, forbidden := range []string{"T2", "T3"} {
		if _, exists := options[forbidden]; exists {
			t.Fatalf("context options include self/future task %s", forbidden)
		}
	}
	response := jevTestResponse(questions, map[string]string{
		"T1_route": "order_query",
		"T2_route": "order_query", "T2_context": "T1",
		"T2_relation": "clarification_answer", "T2_resolution": "resolved_from_context",
		"T3_route": "parking",
	}, nil)
	result, err := buildIntentTraceFromJev(response, spans, contexts)
	if err != nil {
		t.Fatal(err)
	}
	task := result.IntentTasks[1]
	if task.Text != "13800138000" || task.RelationToPrevious != "independent" ||
		!reflect.DeepEqual(task.SourceRefs, []string{"U2", "U1"}) ||
		!strings.Contains(task.ResolvedText, "帮我查询订单") || !task.NeedsTool {
		t.Fatalf("same-turn slot lost order topic or provenance: %#v", task)
	}
	if result.IntentTasks[2].ResolvedText != "酒店有停车场吗" {
		t.Fatalf("new topic inherited previous lookup: %#v", result.IntentTasks[2])
	}
}

func TestJevActiveGoalContextCompactsRepeatedSupplements(t *testing.T) {
	contextText := strings.Join([]string{
		"我想把现在的儿童房换成大床房，完整入住期间有空房吗，差价多少？",
		"当前客户补充（以本次为准）：那还有其他房型可以换吗",
		"当前客户补充（以本次为准）：沐阳吧",
		"当前客户补充（以本次为准）：沐阳吧",
		"当前客户补充（以本次为准）：沐阳吧",
	}, "\n")
	got := buildJevActiveGoalText(contextText, "差价多少")
	if strings.Count(got, "沐阳吧") != 1 || strings.Count(got, "当前客户补充（以本次为准）：") != 2 {
		t.Fatalf("active goal kept duplicate supplements: %q", got)
	}
	for _, expected := range []string{"儿童房换成大床房", "沐阳吧", "差价多少"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("active goal lost %q: %q", expected, got)
		}
	}
	if len([]rune(got)) > jevResolvedContextLimit {
		t.Fatalf("active goal exceeded context budget: %d", len([]rune(got)))
	}
}

func TestJevActiveGoalCorrectionReplacesStaleSupplements(t *testing.T) {
	contextText := strings.Join([]string{
		"帮我查订单",
		"当前客户补充（以本次为准）：手机号13800138000",
		"当前客户补充（以本次为准）：查退房时间",
	}, "\n")
	got := buildJevActiveGoalTextForCurrent(contextText, "不是这个号码，是13700137000", true)
	if strings.Contains(got, "13800138000") || strings.Contains(got, "查退房时间") || !strings.Contains(got, "13700137000") {
		t.Fatalf("correction kept stale supplements or lost the latest value: %q", got)
	}
	if strings.Count(got, "当前客户补充（以本次为准）：") != 1 {
		t.Fatalf("correction must keep only one latest supplement: %q", got)
	}
}

func TestJevRulesTreatColloquialStayDateAsCheckoutNotRoomNumber(t *testing.T) {
	criteria, _ := jevIntentRouteCriteria()["order_detail"].(string)
	for _, phrase := range []string{"我的房到几号", "我的房住到几号", "房到几号 means checkout date"} {
		if !strings.Contains(jevIntentClassificationRules+criteria, phrase) {
			t.Fatalf("JEV checkout rule is missing %q", phrase)
		}
	}
}

func TestJevHistoryStateStripsDisplayEnvelope(t *testing.T) {
	history := adapter.HistoryBuildResult{RawItems: []models.Message{
		{SenderType: enums.IMSenderTypeCustomer, Content: "酒店有没有咖啡"},
		{SenderType: enums.IMSenderTypeAI, Content: "有的，酒店提供速溶咖啡。"},
	}}
	state := buildJevIntentState(RunInput{}, history, nil)
	if len(state.History) != 2 || state.History[0].Text != "酒店有没有咖啡" || state.History[1].Text != "有的，酒店提供速溶咖啡。" {
		t.Fatalf("JEV history kept display envelopes: %#v", state.History)
	}
}

func TestJevMergesSameOrderObjectFieldsIntoOneTask(t *testing.T) {
	tasks := []callbacks.IntentTaskTraceData{
		{
			Intent: "hotel_info", SubIntent: "order_detail", Objective: "compound_information",
			DialogueAct: "follow_up", RelationToPrevious: "independent", ResolutionState: runtimeIntentResolutionClear,
			Text: "那我订的是哪种房，", ResolvedText: "那我订的是哪种房，", SourceRefs: []string{"U1"}, NeedsTool: true,
			Entities: []callbacks.IntentEntityTraceData{{Type: runtimeIntentEntityCustomerPhone, Text: "13800138000"}},
		},
		{
			Intent: "hotel_info", SubIntent: "order_detail", Objective: "time",
			DialogueAct: "new_request", RelationToPrevious: "independent", ResolutionState: runtimeIntentResolutionClear,
			Text: "最晚几点退房？", ResolvedText: "最晚几点退房？", SourceRefs: []string{"U1"}, NeedsTool: true,
			Entities: []callbacks.IntentEntityTraceData{{Type: runtimeIntentEntityCustomerPhone, Text: "13800138000"}},
		},
	}
	got := mergeJevCompositeIntentTasks(tasks)
	if len(got) != 1 || got[0].Text != "那我订的是哪种房，最晚几点退房？" || got[0].Objective != "compound_information" ||
		got[0].DialogueAct != "follow_up" || !got[0].NeedsTool {
		t.Fatalf("same order fields were not merged: %#v", got)
	}

	other := tasks[1]
	other.SubIntent = "parking"
	if got = mergeJevCompositeIntentTasks([]callbacks.IntentTaskTraceData{tasks[0], other}); len(got) != 2 {
		t.Fatalf("different business routes must remain separate: %#v", got)
	}

	contextual := append([]callbacks.IntentTaskTraceData(nil), tasks...)
	for index := range contextual {
		contextual[index].RelationToPrevious = "follow_up"
		contextual[index].ResolutionState = runtimeIntentResolutionResolvedFromContext
		contextual[index].ResolvedText = "帮我查这笔订单\n当前客户补充（以本次为准）：" + contextual[index].Text
	}
	got = mergeJevCompositeIntentTasks(contextual)
	if len(got) != 1 || got[0].RelationToPrevious != "follow_up" || got[0].ResolutionState != runtimeIntentResolutionResolvedFromContext ||
		!strings.Contains(got[0].ResolvedText, "哪种房") || !strings.Contains(got[0].ResolvedText, "几点退房") {
		t.Fatalf("contextual order fields were not merged: %#v", got)
	}
}

func TestJevWeakShortReplyInheritsSelectedBusinessRoute(t *testing.T) {
	for _, tc := range []struct {
		name, current, priorSubIntent, dialogueAct, objective string
		wantIntent, wantSubIntent                             string
		wantKnowledge, wantTool                               bool
	}{
		{
			name: "coffee location followup", current: "放在哪里", priorSubIntent: "store_knowledge",
			dialogueAct: "follow_up", objective: "location", wantIntent: "hotel_info", wantSubIntent: "store_knowledge", wantKnowledge: true,
		},
		{
			name: "room candidate selection", current: "1501", priorSubIntent: "room_change",
			dialogueAct: "selection", objective: "confirm", wantIntent: "hotel_info", wantSubIntent: "room_change", wantTool: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			span := jevIntentSpan{Ref: "T1", SourceRef: "U1", Text: tc.current}
			state := jevIntentState{RecentBusinessTask: &jevIntentPriorTaskState{
				Ref: "R1", Intent: "hotel_info", SubIntent: tc.priorSubIntent,
				Text: "酒店有没有咖啡", ResolvedText: "酒店有没有咖啡",
			}}
			if tc.priorSubIntent == "room_change" {
				state.RecentBusinessTask.Text = "帮我换个房间"
				state.RecentBusinessTask.ResolvedText = "帮我换个房间\n当前客户补充（以本次为准）：沐阳吧"
			}
			questions, contexts := buildJevClassificationQuestions([]jevIntentSpan{span}, state)
			intent, err := buildIntentTraceFromJev(jevTestResponse(questions, map[string]string{
				"T1_route": "clarify", "T1_objective": tc.objective, "T1_dialogue_act": tc.dialogueAct,
				"T1_relation": "follow_up", "T1_resolution": "resolved_from_context", "T1_context": "R1",
			}, nil), []jevIntentSpan{span}, contexts)
			if err != nil {
				t.Fatal(err)
			}
			task := intent.IntentTasks[0]
			if task.Intent != tc.wantIntent || task.SubIntent != tc.wantSubIntent || task.NeedsKnowledge != tc.wantKnowledge || task.NeedsTool != tc.wantTool {
				t.Fatalf("short reply did not inherit active business route: %#v", task)
			}
			if !strings.Contains(task.ResolvedText, tc.current) || strings.Count(task.ResolvedText, tc.current) != 1 {
				t.Fatalf("short reply context was not compacted: %q", task.ResolvedText)
			}
		})
	}
}

func TestJevHistoryReferencesRemainStableAndExcludeAssistantFacts(t *testing.T) {
	history := adapter.HistoryBuildResult{}
	for index := 0; index < 20; index++ {
		role := enums.IMSenderTypeCustomer
		if index%2 == 1 {
			role = enums.IMSenderTypeAI
		}
		history.RawItems = append(history.RawItems, models.Message{
			ID: int64(index + 1), SenderType: role, MessageType: enums.IMMessageTypeText,
			Content: fmt.Sprintf("历史消息%d", index),
		})
	}
	history.RawItems[18].Content = "用13800138000查我的订单"
	history.RawItems[19].Content = "错误的客服答复13900139000已退房"
	req := RunInput{UserMessage: models.Message{ID: 30, Content: "号码写错了，是13700137000", MessageType: enums.IMMessageTypeText}}
	sources := adapter.BuildCurrentTurnSources(req.UserMessage)
	state := buildJevIntentState(req, history, sources)
	if len(state.History) != jevIntentHistoryLimit || state.History[0].Ref != "H12" || state.History[7].Ref != "H19" {
		t.Fatalf("bounded history has unstable references: %#v", state.History)
	}
	if state.History[6].Role != "customer" || state.History[7].Role != "service" {
		t.Fatalf("history speaker roles lost: %#v", state.History[6:])
	}
	spans := jevTestSegment(t, sources, nil)
	questions, contexts := buildJevClassificationQuestions(spans, state)
	if _, exists := contexts["H19"]; exists {
		t.Fatal("assistant facts can be selected as customer-provided context")
	}
	if contexts["H18"].Text != state.History[6].Text || !strings.Contains(contexts["H18"].Text, history.RawItems[18].Content) {
		t.Fatalf("selected context does not match its stable reference: %#v", contexts["H18"])
	}
	response := jevTestResponse(questions, map[string]string{
		"T1_route": "order_query", "T1_relation": "correction",
		"T1_resolution": "resolved_from_context", "T1_context": "H18",
	}, nil)
	result, err := buildIntentTraceFromJev(response, spans, contexts)
	if err != nil {
		t.Fatal(err)
	}
	task := result.IntentTasks[0]
	if task.SubIntent != "order_query" || !task.NeedsTool || task.NeedsHumanRoute ||
		task.RelationToPrevious != "correction" || !strings.Contains(task.ResolvedText, "13700137000") ||
		strings.Contains(task.ResolvedText, "13900139000") {
		t.Fatalf("phone correction lost route or inherited assistant facts: %#v", task)
	}
	response.Answers["T1_context"] = jev.Answer{Type: "choice", Choice: "H19"}
	if _, err := buildIntentTraceFromJev(response, spans, contexts); err == nil {
		t.Fatal("unselectable assistant context should fail mapping")
	}
}

func TestJevNormalizationRetainsHumanAuthorizationBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, text, route, objective string
		handoff                      bool
	}{
		{name: "explicit", text: "帮我转人工", route: "explicit_handoff", objective: "action_request", handoff: true},
		{name: "denied_even_if_model_misclassifies", text: "不要转人工，先回答我", route: "explicit_handoff", objective: "action_request"},
		{name: "cancel", text: "取消转人工", route: "explicit_handoff", objective: "cancel"},
		{name: "service", text: "帮忙送条毛巾", route: "room_supplies", objective: "action_request"},
		{name: "correction", text: "你回答错了", route: "answer_rejected", objective: "explanation"},
		{name: "emergency_without_explicit_handoff", text: "有人受伤流血了", route: "emergency_safety", objective: "action_request", handoff: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := RunInput{UserMessage: models.Message{Content: tc.text, MessageType: enums.IMMessageTypeText}}
			spans := []jevIntentSpan{{Ref: "T1", SourceRef: "U1", Text: tc.text}}
			questions, contexts := buildJevClassificationQuestions(spans, jevIntentState{})
			intent, err := buildIntentTraceFromJev(jevTestResponse(questions, map[string]string{
				"T1_route": tc.route, "T1_objective": tc.objective,
			}, nil), spans, contexts)
			if err != nil {
				t.Fatal(err)
			}
			intent.SemanticContractExpected, intent.SourceRefsValidated = true, true
			intent = normalizeModelIntentTrace(intent, req, adapter.HistoryBuildResult{}, nil)
			if intent.NeedsHumanRoute != tc.handoff {
				t.Fatalf("human-route authorization changed: %#v", intent)
			}
		})
	}
}

func TestJevCheckinNormalizationAttachesOneResourceWithoutLosingQuestion(t *testing.T) {
	req := RunInput{UserMessage: models.Message{Content: "我应该怎么入住", MessageType: enums.IMMessageTypeText}}
	spans := []jevIntentSpan{{Ref: "T1", SourceRef: "U1", Text: req.UserMessage.Content}}
	questions, contexts := buildJevClassificationQuestions(spans, jevIntentState{})
	intent, err := buildIntentTraceFromJev(jevTestResponse(questions, map[string]string{
		"T1_route": "checkin_process", "T1_objective": "method",
	}, nil), spans, contexts)
	if err != nil {
		t.Fatal(err)
	}
	intent.SemanticContractExpected, intent.SourceRefsValidated = true, true
	intent = normalizeModelIntentTrace(intent, req, adapter.HistoryBuildResult{}, nil)
	plan := buildReplyPlan(intent, selectIntentPromptPack(intent))
	resources, knowledge := 0, 0
	for _, task := range plan.TaskPlans {
		if task.OutputKind == "resource" && task.ResourceAction == "provide_mini_program" {
			resources++
			if !reflect.DeepEqual(task.SourceRefs, []string{"U1"}) || task.OriginalText != req.UserMessage.Content {
				t.Fatalf("check-in card lost parent question: %#v", task)
			}
		}
		if task.OutputKind == "text" && task.NeedsKnowledge {
			knowledge++
		}
	}
	if resources != 1 || knowledge != 1 {
		t.Fatalf("want one knowledge response plus one card, got %#v", plan.TaskPlans)
	}
}

func TestJevIntentConfigNeverInheritsOtherProviderCredentials(t *testing.T) {
	t.Setenv(jevAPIKeyEnv, "")
	t.Setenv(jevBaseURLEnv, "")
	t.Setenv(jevModelEnv, "")
	previous := models.AIConfig{ID: 87, APIKey: "old-provider-secret", BaseURL: "https://old-provider.invalid/v1", ModelName: "old-model"}
	config := applyJevIntentConfig(previous)
	if config.APIKey != "" || config.ID != 0 || config.BaseURL != jev.DefaultBaseURL || config.ModelName != jev.DefaultModel ||
		config.Provider != enums.AIProviderTypeSafeJev {
		t.Fatalf("JEV inherited another model's credentials or identity: %#v", config)
	}
	if _, err := jev.NewClient(config.BaseURL, config.APIKey, runtimeIntentDetectTimeout); err == nil {
		t.Fatal("missing JEV key should fail, not borrow another provider credential")
	}
}

func TestJevCoverageRepairFeedbackIsIncludedInTypedState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request jev.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		encoded, err := json.Marshal(request.State)
		if err != nil {
			t.Error(err)
		}
		state := string(encoded)
		for _, required := range []string{"repair", "previousTasks", "previousIntentTasks", "coverageIssues", "missing_question"} {
			if !strings.Contains(state, required) {
				t.Errorf("repair feedback missing %q from JEV state: %s", required, state)
			}
		}
		jevTestWriteResponse(t, w, request.Questions, map[string]string{"T1_route": "parking"}, nil)
	}))
	defer server.Close()
	jevTestSetEnvironment(t, server.URL)
	repair := &runtimeQuestionRepairRequest{
		Coverage:    &runtimeQuestionCoverageInput{Tasks: []runtimeQuestionCoverageTask{{TaskID: "task-1", Text: "酒店有停车场吗"}}},
		IntentTasks: []callbacks.IntentTaskTraceData{{Text: "酒店有停车场吗", SubIntent: "parking"}},
		Issues:      []runtimeQuestionCoverageIssue{{Kind: "missing_question", SourceRef: "U1", Text: "酒店有停车场吗", Reason: "missing"}},
	}
	ctx := context.WithValue(context.Background(), runtimeQuestionRepairContextKey{}, repair)
	result, err := (llmRuntimeIntentDetector{}).DetectRuntimeIntent(ctx, RunInput{
		UserMessage: models.Message{Content: "酒店有停车场吗", MessageType: enums.IMMessageTypeText},
	}, adapter.HistoryBuildResult{}, nil)
	if err != nil || len(result.IntentTasks) != 1 || result.IntentTasks[0].SubIntent != "parking" {
		t.Fatalf("typed repair failed: result=%#v err=%v", result, err)
	}
}

func TestJevUnknownProviderOverrideFailsClosed(t *testing.T) {
	t.Setenv(intentDetectProviderEnv, "typesafe_jev_typo")
	_, err := (llmRuntimeIntentDetector{}).DetectRuntimeIntent(context.Background(), RunInput{}, adapter.HistoryBuildResult{}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported intent detect provider") {
		t.Fatalf("unknown intent provider did not fail closed: %v", err)
	}
}

func TestJevDetectRuntimeIntentUsesTypedEndpointWithoutDatabase(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer intent-test-key" {
			t.Errorf("wrong JEV transport: %s %s", r.Method, r.URL.Path)
			http.Error(w, "wrong transport", http.StatusBadRequest)
			return
		}
		var request jev.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if request.Model != jev.DefaultModel {
			t.Errorf("wrong intent model: %q", request.Model)
		}
		jevTestWriteResponse(t, w, request.Questions, map[string]string{"T1_route": "order_query"}, nil)
	}))
	defer server.Close()
	jevTestSetEnvironment(t, server.URL)
	result, err := (llmRuntimeIntentDetector{}).DetectRuntimeIntent(context.Background(), RunInput{
		UserMessage: models.Message{Content: "帮我查询订单", MessageType: enums.IMMessageTypeText},
	}, adapter.HistoryBuildResult{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(result.IntentTasks) != 1 || result.IntentTasks[0].SubIntent != "order_query" ||
		!result.NeedsTool || result.NeedsHumanRoute || !result.SourceRefsValidated || !result.SemanticContractExpected {
		t.Fatalf("raw Intent integration failed: calls=%d result=%#v", calls.Load(), result)
	}
}

func TestJevDetectRuntimeIntentRejectsMissingTypedAnswers(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": jev.DefaultModel, "answers": map[string]any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0},
		})
	}))
	defer server.Close()
	jevTestSetEnvironment(t, server.URL)
	result, err := (llmRuntimeIntentDetector{}).DetectRuntimeIntent(context.Background(), RunInput{
		UserMessage: models.Message{Content: "查询订单", MessageType: enums.IMMessageTypeText},
	}, adapter.HistoryBuildResult{}, nil)
	if err == nil || len(result.IntentTasks) != 0 || calls.Load() != 1 {
		t.Fatalf("missing answers must fail without old-model fallback or fake reply: calls=%d result=%#v err=%v", calls.Load(), result, err)
	}
}

func TestJevIntentMappingRejectsMissingRoute(t *testing.T) {
	spans := []jevIntentSpan{{Ref: "T1", SourceRef: "U1", Text: "酒店有停车场吗"}}
	questions, contexts := buildJevClassificationQuestions(spans, jevIntentState{})
	response := jevTestResponse(questions, nil, nil)
	delete(response.Answers, "T1_route")
	if _, err := buildIntentTraceFromJev(response, spans, contexts); err == nil {
		t.Fatal("missing typed route must not silently become interaction")
	}
}

func jevTestSetEnvironment(t *testing.T, baseURL string) {
	t.Helper()
	t.Setenv(intentDetectProviderEnv, "typesafe_jev")
	t.Setenv(jevAPIKeyEnv, "intent-test-key")
	t.Setenv(jevBaseURLEnv, baseURL)
	t.Setenv(jevModelEnv, jev.DefaultModel)
}

func jevTestResponse(questions map[string]jev.Question, choices map[string]string, nouls map[string]float64) jev.Response {
	response := jev.Response{Model: jev.DefaultModel, Answers: make(map[string]jev.Answer)}
	for key, question := range questions {
		if question.Type == "noul" {
			response.Answers[key] = jev.Answer{Type: "noul", Noul: nouls[key]}
			continue
		}
		choice := choices[key]
		if choice == "" {
			for suffix, value := range map[string]string{
				"_route": "parking", "_objective": "availability", "_dialogue_act": "new_request", "_relation": "independent", "_resolution": "clear", "_context": "none", "_count": "1", "_terminal_alignment": "exact",
			} {
				if strings.HasSuffix(key, suffix) {
					choice = value
					break
				}
			}
		}
		probabilities := make(map[string]float64)
		for option := range jevTestCriteria(question.Criteria) {
			probabilities[option] = 0
		}
		probabilities[choice] = 1
		response.Answers[key] = jev.Answer{Type: "choice", Choice: choice, Confidence: 1, Probabilities: probabilities}
	}
	return response
}

func jevTestSegment(t *testing.T, sources []adapter.CurrentTurnSource, choices map[string]string) []jevIntentSpan {
	t.Helper()
	spans, err := segmentJevIntentSources(sources, jevIntentState{}, func(_ any, questions map[string]jev.Question) (jev.Response, error) {
		response := jevTestResponse(questions, choices, nil)
		for key, question := range questions {
			if _, ok := jevTestCriteria(question.Criteria)[response.Answers[key].Choice]; !ok {
				t.Fatalf("invalid mocked segmentation choice %s=%q", key, response.Answers[key].Choice)
			}
		}
		return response, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return spans
}

func jevTestCriteria(value any) map[string]any {
	criteria, _ := value.(map[string]any)
	return criteria
}

func jevTestWriteResponse(t *testing.T, w http.ResponseWriter, questions map[string]jev.Question, choices map[string]string, nouls map[string]float64) {
	t.Helper()
	response := jevTestResponse(questions, choices, nouls)
	answers := make(map[string]any)
	for key, answer := range response.Answers {
		if answer.Type == "noul" {
			answers[key] = map[string]any{"type": "noul", "noul": answer.Noul}
		} else {
			answers[key] = map[string]any{
				"type": "choice", "choice": answer.Choice, "confidence": answer.Confidence, "probabilities": answer.Probabilities,
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"model": response.Model, "answers": answers,
		"usage": map[string]int64{"input_tokens": 1, "output_tokens": 1},
	}); err != nil {
		t.Error(err)
	}
}
