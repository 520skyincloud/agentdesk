package executor

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/toolx"
)

type runtimePMSFakeCall struct {
	action string
	args   map[string]string
}

type runtimePMSFakeInvoker struct {
	calls   []runtimePMSFakeCall
	results map[string][]pmsReadStepResult
}

func (f *runtimePMSFakeInvoker) Invoke(_ context.Context, action string, args map[string]string) pmsReadStepResult {
	f.calls = append(f.calls, runtimePMSFakeCall{action: action, args: clonePMSReadArgs(args)})
	queue := f.results[action]
	if len(queue) == 0 {
		return pmsReadStepResult{Status: pmsReadStepUnavailable, Message: "missing fake result"}
	}
	result := queue[0]
	f.results[action] = queue[1:]
	return result
}

func TestRuntimePMSReadPlanInputUsesCurrentAndSessionLocators(t *testing.T) {
	t.Run("current phone overrides the session phone", func(t *testing.T) {
		input := runtimePMSReadPlanInputForTask(callbacks.ReplyTaskPlanTraceData{
			OriginalText: "帮我查一下 139-0013-8000 的订单",
			SubIntent:    "order_query",
		}, runtimePMSSessionLocator{Phone: "13800138000"}, time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local))
		if input.Phone != "13900138000" {
			t.Fatalf("current phone must win, got %#v", input)
		}
	})

	t.Run("session phone is reused for a follow up", func(t *testing.T) {
		input := runtimePMSReadPlanInputForTask(callbacks.ReplyTaskPlanTraceData{
			OriginalText: "那我最晚几点退房",
			SubIntent:    "check_out_status",
		}, runtimePMSSessionLocator{Phone: "13800138000"}, time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local))
		if input.Phone != "13800138000" {
			t.Fatalf("follow up must reuse the session phone, got %#v", input)
		}
	})

	t.Run("explicit correction keeps only the corrected phone", func(t *testing.T) {
		input := runtimePMSReadPlanInputForTask(callbacks.ReplyTaskPlanTraceData{
			OriginalText: "不是 13800138000，是 13700137000",
			SubIntent:    "order_query",
		}, runtimePMSSessionLocator{Phone: "13800138000"}, time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local))
		if input.Phone != "13700137000" {
			t.Fatalf("corrected phone must replace the old phone, got %#v", input)
		}
	})

	t.Run("rejected phone is not silently restored", func(t *testing.T) {
		input := runtimePMSReadPlanInputForTask(callbacks.ReplyTaskPlanTraceData{
			OriginalText: "不是这个号码，先别查",
			SubIntent:    "order_query",
		}, runtimePMSSessionLocator{Phone: "13800138000"}, time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local))
		if input.Phone != "" {
			t.Fatalf("rejected phone must stay invalidated, got %#v", input)
		}
	})
}

func TestRuntimePMSOrderLocatorsPreserveMultipleKnownIDs(t *testing.T) {
	reserveID, receptID, customerNo := runtimePMSOrderLocators("预订单ID:RES-1 接待单ID:REC-2 会员编号:C-3")
	if reserveID != "RES-1" || receptID != "REC-2" || customerNo != "C-3" {
		t.Fatalf("all locators must survive parsing, got reserve=%q recept=%q customer=%q", reserveID, receptID, customerNo)
	}

	reserveID, receptID, customerNo = runtimePMSOrderLocators("本次查询订单定位：接待单ID:REC-9")
	if reserveID != "" || receptID != "REC-9" || customerNo != "" {
		t.Fatalf("single locator changed: reserve=%q recept=%q customer=%q", reserveID, receptID, customerNo)
	}
}

func TestRuntimePMSRoomKeywordExtractsCustomerRoomNumbers(t *testing.T) {
	for _, test := range []struct {
		text string
		want string
	}{
		{text: "我住在1401，房间打扫好了吗", want: "1401"},
		{text: "我到门口了，1208现在是干净房吗", want: "1208"},
	} {
		got := runtimePMSRoomKeyword(callbacks.ReplyTaskPlanTraceData{OriginalText: test.text})
		if got != test.want {
			t.Fatalf("room number was not extracted from %q: got %q want %q", test.text, got, test.want)
		}
	}
	if got := runtimePMSRoomKeyword(callbacks.ReplyTaskPlanTraceData{OriginalText: "手机号13800138000"}); got != "" {
		t.Fatalf("phone digits must not become a room number: %q", got)
	}
}

func TestRuntimePMSReadDatesResolveExplicitAndRelativeRanges(t *testing.T) {
	now := time.Date(2026, 9, 22, 18, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	t.Run("single digit explicit date is normalized", func(t *testing.T) {
		start, end := runtimePMSReadDates(callbacks.ReplyTaskPlanTraceData{OriginalText: "查 2026年9月3日 的房"}, pmsReadScenarioDateInventory, now)
		if start != "2026-09-03" || end != "2026-09-04" {
			t.Fatalf("unexpected explicit range %q to %q", start, end)
		}
	})

	t.Run("tonight resolves to the current business day", func(t *testing.T) {
		start, end := runtimePMSReadDates(callbacks.ReplyTaskPlanTraceData{OriginalText: "今晚还有大床房吗"}, pmsReadScenarioDateInventory, now)
		if start != "2026-09-22" || end != "2026-09-23" {
			t.Fatalf("unexpected tonight range %q to %q", start, end)
		}
	})

	t.Run("tomorrow night resolves to the next day", func(t *testing.T) {
		start, end := runtimePMSReadDates(callbacks.ReplyTaskPlanTraceData{OriginalText: "明晚还有房吗"}, pmsReadScenarioDateInventory, now)
		if start != "2026-09-23" || end != "2026-09-24" {
			t.Fatalf("unexpected tomorrow range %q to %q", start, end)
		}
	})

	t.Run("mixed relative check-in and absolute checkout preserve mention order", func(t *testing.T) {
		start, end := runtimePMSReadDates(callbacks.ReplyTaskPlanTraceData{OriginalText: "今天入住，2026-09-24离店"}, pmsReadScenarioDateInventory, now)
		if start != "2026-09-22" || end != "2026-09-24" {
			t.Fatalf("customer date order changed: %q to %q", start, end)
		}
	})

	for _, text := range []string{"续住到9月26日", "续住到26号"} {
		t.Run(text, func(t *testing.T) {
			start, end := runtimePMSReadDates(callbacks.ReplyTaskPlanTraceData{OriginalText: text}, pmsReadScenarioRenewal, now)
			if start != "" || end != "2026-09-26" {
				t.Fatalf("renewal target must be an end date: %q to %q", start, end)
			}
		})
	}

	for _, scenario := range []pmsReadScenario{pmsReadScenarioDateInventory, pmsReadScenarioRenewal} {
		t.Run("correction keeps only the new date "+string(scenario), func(t *testing.T) {
			start, end := runtimePMSReadDates(callbacks.ReplyTaskPlanTraceData{OriginalText: "不是25号，是26号"}, scenario, now)
			if scenario == pmsReadScenarioRenewal {
				if start != "" || end != "2026-09-26" {
					t.Fatalf("renewal correction mismatch: %q to %q", start, end)
				}
			} else if start != "2026-09-26" || end != "2026-09-27" {
				t.Fatalf("inventory correction mismatch: %q to %q", start, end)
			}
		})
	}
}

func TestRuntimePMSTargetRoomTypeRequiresARealTarget(t *testing.T) {
	for _, test := range []struct {
		text     string
		entities []callbacks.IntentEntityTraceData
		want     string
	}{
		{text: "标准房太小，能升房吗", entities: []callbacks.IntentEntityTraceData{{Type: "room_type", Text: "标准房"}}, want: ""},
		{text: "当前大床房，想换双床房", entities: []callbacks.IntentEntityTraceData{{Type: "room_type", Text: "大床房"}, {Type: "room_type", Text: "双床房"}}, want: "双床房"},
		{text: "能换豪华大床房吗", want: "豪华大床房"},
		{text: "大床房和双床房哪个更好", entities: []callbacks.IntentEntityTraceData{{Type: "room_type", Text: "大床房"}, {Type: "room_type", Text: "双床房"}}, want: ""},
	} {
		got := runtimePMSTargetRoomTypeText(callbacks.ReplyTaskPlanTraceData{OriginalText: test.text, Entities: test.entities})
		if got != test.want {
			t.Fatalf("target room mismatch for %q: got=%q want=%q", test.text, got, test.want)
		}
	}
}

func TestExecuteRuntimePMSReadPlanBindsOrderFacts(t *testing.T) {
	t.Run("order dates bind into inventory", func(t *testing.T) {
		invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
			"reserve_order_detail": {{Status: pmsReadStepOK, Data: map[string]any{
				"reserveOrderId": "RES-1", "checkInBusinessDate": "2026-09-22", "checkOutBusinessDate": "2026-09-24",
			}}},
			"inventory": {{Status: pmsReadStepOK, Data: []any{}}},
		}}
		input := pmsReadPlanInput{Scenario: pmsReadScenarioDateInventory, ReserveOrderID: "RES-1"}
		results, _ := executeRuntimePMSReadPlan(context.Background(), buildPMSReadPlan(input), input, invoker)
		if len(results) != 2 || len(invoker.calls) != 2 {
			t.Fatalf("unexpected calls/results: calls=%#v results=%#v", invoker.calls, results)
		}
		inventory := invoker.calls[1]
		if inventory.action != "inventory" || inventory.args["beginTime"] != "2026-09-22" || inventory.args["endTime"] != "2026-09-24" {
			t.Fatalf("order dates were not bound into inventory: %#v", inventory)
		}
	})

	t.Run("order room binds into room status", func(t *testing.T) {
		invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
			"recept_order_detail": {{Status: pmsReadStepOK, Data: map[string]any{
				"receptOrderId": "REC-1", "homeName": "1401", "checkInBusinessDate": "2026-09-22", "checkOutBusinessDate": "2026-09-24",
			}}},
			"inventory":   {{Status: pmsReadStepOK, Data: []any{}}},
			"room_status": {{Status: pmsReadStepOK, Data: map[string]any{"list": []any{}}}},
		}}
		input := pmsReadPlanInput{Scenario: pmsReadScenarioRoomChange, ReceptOrderID: "REC-1"}
		_, _ = executeRuntimePMSReadPlan(context.Background(), buildPMSReadPlan(input), input, invoker)
		var roomCall *runtimePMSFakeCall
		for index := range invoker.calls {
			if invoker.calls[index].action == "room_status" {
				roomCall = &invoker.calls[index]
			}
		}
		if roomCall == nil || roomCall.args["keyword"] != "1401" {
			t.Fatalf("current room was not bound into room status: %#v", invoker.calls)
		}
	})
}

func TestExecuteRuntimePMSReadPlanNeverQueriesInventoryWithoutDates(t *testing.T) {
	for _, input := range []pmsReadPlanInput{
		{Scenario: pmsReadScenarioDateInventory},
		{Scenario: pmsReadScenarioDateInventory, StartDate: "2026-09-25", EndDate: "2026-09-23"},
	} {
		invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{}}
		_, plan := executeRuntimePMSReadPlan(context.Background(), buildPMSReadPlan(input), input, invoker)
		for _, call := range invoker.calls {
			if call.action == "inventory" {
				t.Fatalf("inventory must not run without a valid range, input=%#v plan=%#v calls=%#v", input, plan, invoker.calls)
			}
		}
	}
}

func TestRuntimePMSInventoryCoverageRequiresEveryStayDate(t *testing.T) {
	for _, test := range []struct {
		name   string
		dates  []string
		status pmsReadStepStatus
	}{
		{name: "complete two night stay", dates: []string{"2026-09-22", "2026-09-23"}, status: pmsReadStepOK},
		{name: "missing second night", dates: []string{"2026-09-22"}, status: pmsReadStepPartial},
	} {
		t.Run(test.name, func(t *testing.T) {
			bookings := map[string]any{}
			for _, date := range test.dates {
				bookings[date] = map[string]any{"available": "2"}
			}
			got := validateRuntimePMSInventoryCoverage(pmsReadStepResult{
				Status: pmsReadStepOK,
				Args:   map[string]string{"beginTime": "2026-09-22", "endTime": "2026-09-24"},
				Data:   []any{map[string]any{"roomTypeName": "大床房", "bookings": bookings}},
			})
			if got.Status != test.status {
				t.Fatalf("coverage status mismatch: got=%q want=%q result=%#v", got.Status, test.status, got)
			}
		})
	}

	t.Run("different room types cannot combine partial dates", func(t *testing.T) {
		got := validateRuntimePMSInventoryCoverage(pmsReadStepResult{
			Status: pmsReadStepOK,
			Args:   map[string]string{"beginTime": "2026-09-22", "endTime": "2026-09-24"},
			Data: []any{
				map[string]any{"roomTypeName": "大床房", "bookings": map[string]any{"2026-09-22": map[string]any{"available": "2"}}},
				map[string]any{"roomTypeName": "双床房", "bookings": map[string]any{"2026-09-23": map[string]any{"available": "2"}}},
			},
		})
		if got.Status != pmsReadStepPartial || !strings.Contains(got.Message, "大床房") || !strings.Contains(got.Message, "双床房") {
			t.Fatalf("cross-room date union must remain partial: %#v", got)
		}
	})
}

func TestRuntimePMSMemoizingInvokerReusesIdenticalRead(t *testing.T) {
	for _, action := range []string{"reserve_order_by_phone", "inventory"} {
		delegate := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
			action: {{Status: pmsReadStepOK, Data: map[string]any{"ok": "true"}}},
		}}
		memo := &runtimePMSMemoizingInvoker{delegate: delegate}
		args := map[string]string{"phone": "13800138000"}
		if action == "inventory" {
			args = map[string]string{"beginTime": "2026-09-22", "endTime": "2026-09-24"}
		}
		first := memo.Invoke(context.Background(), action, args)
		second := memo.Invoke(context.Background(), action, clonePMSReadArgs(args))
		if first.Status != pmsReadStepOK || second.Status != pmsReadStepOK || len(delegate.calls) != 1 {
			t.Fatalf("identical reads must share one snapshot: action=%s calls=%#v", action, delegate.calls)
		}
	}
}

func TestResolveRuntimePMSTargetRoomTypeIsDeterministic(t *testing.T) {
	inventory := []any{
		map[string]any{"roomTypeId": "ROOM-1", "roomTypeName": "大床房"},
		map[string]any{"roomTypeId": "ROOM-2", "roomTypeName": "豪华大床房"},
		map[string]any{"roomTypeId": "ROOM-3", "roomTypeName": "双床房"},
	}

	id, name, status := resolveRuntimePMSTargetRoomType(inventory, "豪华大床房")
	if status != pmsReadStepOK || id != "ROOM-2" || name != "豪华大床房" {
		t.Fatalf("exact target must win over shorter contained names: id=%q name=%q status=%q", id, name, status)
	}

	id, name, status = resolveRuntimePMSTargetRoomType(inventory, "我想换到豪华大床房")
	if status != pmsReadStepOK || id != "ROOM-2" || name != "豪华大床房" {
		t.Fatalf("longest uniquely named target must be selected: id=%q name=%q status=%q", id, name, status)
	}

	ambiguous := []any{
		map[string]any{"roomTypeId": "ROOM-E", "roomTypeName": "东景观房"},
		map[string]any{"roomTypeId": "ROOM-W", "roomTypeName": "西景观房"},
	}
	if _, _, status = resolveRuntimePMSTargetRoomType(ambiguous, "东景观房或者西景观房都行"); status != pmsReadStepAmbiguous {
		t.Fatalf("multiple equally specific targets must remain ambiguous, got %q", status)
	}
}

func TestRuntimePMSOrderFactReadsSanitizedProductRoomNames(t *testing.T) {
	for _, test := range []struct {
		name string
		data map[string]any
		want string
	}{
		{
			name: "product room name fills missing root room",
			data: map[string]any{
				"checkInBusinessDate":  "2026-09-22",
				"checkOutBusinessDate": "2026-09-24",
				"reserveProductList": []any{
					map[string]any{"roomName": "标准大床房"},
				},
			},
			want: "房型标准大床房",
		},
		{
			name: "root and product room names are deduplicated",
			data: map[string]any{
				"roomName": "标准大床房",
				"reserveProductList": []any{
					map[string]any{"roomName": "标准大床房"},
					map[string]any{"roomName": "豪华大床房"},
				},
			},
			want: "房型标准大床房/豪华大床房",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fact := runtimePMSOrderFact(test.data)
			if !strings.Contains(fact, test.want) {
				t.Fatalf("order fact missing product room names: got=%q want fragment=%q", fact, test.want)
			}
		})
	}
}

func TestExecuteRuntimePMSUpgradeRunsOnlyGroundedReadSteps(t *testing.T) {
	for _, target := range []string{"豪华大床房", "我想换到豪华大床房"} {
		invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
			"recept_order_detail": {{Status: pmsReadStepOK, Data: map[string]any{
				"receptOrderId": "REC-1", "checkInBusinessDate": "2026-09-22", "checkOutBusinessDate": "2026-09-24",
			}}},
			"inventory": {{Status: pmsReadStepOK, Data: []any{
				map[string]any{"roomTypeId": "ROOM-1", "roomTypeName": "大床房"},
				map[string]any{"roomTypeId": "ROOM-2", "roomTypeName": "豪华大床房"},
			}}},
			"member_benefits_by_phone": {{Status: pmsReadStepOK, Data: map[string]any{"member": map[string]any{"gradeName": "金卡"}}}},
		}}
		input := pmsReadPlanInput{
			Scenario: pmsReadScenarioRoomUpgrade, Phone: "13800138000", ReceptOrderID: "REC-1", TargetRoomTypeText: target,
		}
		results, plan := executeRuntimePMSReadPlan(context.Background(), buildPMSReadPlan(input), input, invoker)
		if len(results) != 4 || len(invoker.calls) != 3 {
			t.Fatalf("upgrade must query order, inventory, member and price: plan=%#v calls=%#v results=%#v", plan, invoker.calls, results)
		}
		gotActions := []string{invoker.calls[0].action, invoker.calls[1].action, invoker.calls[2].action}
		wantActions := []string{"recept_order_detail", "inventory", "member_benefits_by_phone"}
		if !reflect.DeepEqual(gotActions, wantActions) || runtimePMSFakeCalled(invoker.calls, "price_difference") {
			t.Fatalf("unexpected upgrade read sequence: %#v", invoker.calls)
		}
	}
}

func TestExecuteRuntimePMSReadPlanPreservesPartialSuccess(t *testing.T) {
	for _, failedAction := range []string{"member_benefits_by_phone", "inventory"} {
		invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
			"recept_order_detail": {{Status: pmsReadStepOK, Data: map[string]any{
				"receptOrderId": "REC-1", "checkInBusinessDate": "2026-09-22", "checkOutBusinessDate": "2026-09-24", "roomName": "大床房",
			}}},
			"inventory": {{Status: pmsReadStepOK, Data: []any{
				map[string]any{"roomTypeId": "ROOM-2", "roomTypeName": "豪华大床房"},
			}}},
			"member_benefits_by_phone": {{Status: pmsReadStepOK, Data: map[string]any{"member": map[string]any{"gradeName": "金卡"}}}},
		}}
		invoker.results[failedAction] = []pmsReadStepResult{{Status: pmsReadStepUnavailable, Message: failedAction + " unavailable"}}
		input := pmsReadPlanInput{
			Scenario: pmsReadScenarioRoomUpgrade, Phone: "13800138000", ReceptOrderID: "REC-1", TargetRoomTypeID: "ROOM-2",
			StartDate: "2026-09-22", EndDate: "2026-09-24",
		}
		results, plan := executeRuntimePMSReadPlan(context.Background(), buildPMSReadPlan(input), input, invoker)
		aggregated, err := aggregatePMSReadPlanResults(plan, results)
		if err != nil || aggregated.Status != pmsReadStepPartial || len(aggregated.Confirmed) < 2 {
			t.Fatalf("one failed subquery must preserve other facts: failed=%s aggregated=%#v err=%v", failedAction, aggregated, err)
		}
	}
}

func TestExecuteRuntimePMSRenewalAndLateCheckoutRemainReadOnly(t *testing.T) {
	t.Run("renewal only queries candidates", func(t *testing.T) {
		for _, phone := range []string{"", "13800138000"} {
			invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
				"recept_order_detail": {{Status: pmsReadStepOK, Data: map[string]any{
					"receptOrderId": "REC-1", "checkOutBusinessDate": "2026-09-24",
				}}},
				"renew_candidates": {{Status: pmsReadStepOK, Data: map[string]any{"rows": []any{}}}},
			}}
			input := pmsReadPlanInput{Scenario: pmsReadScenarioRenewal, ReceptOrderID: "REC-1", Phone: phone}
			_, _ = executeRuntimePMSReadPlan(context.Background(), buildPMSReadPlan(input), input, invoker)
			for _, call := range invoker.calls {
				if call.action == "renew" || !isRuntimePMSReadOnlyAction(call.action) {
					t.Fatalf("renewal assessment attempted a write: %#v", invoker.calls)
				}
			}
			if !runtimePMSFakeCalled(invoker.calls, "renew_candidates") {
				t.Fatalf("renewal candidates were not queried: %#v", invoker.calls)
			}
		}
	})

	t.Run("late checkout only uses read actions", func(t *testing.T) {
		for _, phone := range []string{"13800138000", "13900139000"} {
			invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
				"recept_order_detail": {{Status: pmsReadStepOK, Data: map[string]any{
					"receptOrderId": "REC-1", "homeName": "1401", "checkOutBusinessDate": "2026-09-24",
				}}},
				"room_status":              {{Status: pmsReadStepOK, Data: map[string]any{"list": []any{}}}},
				"inventory":                {{Status: pmsReadStepOK, Data: []any{}}},
				"member_benefits_by_phone": {{Status: pmsReadStepOK, Data: map[string]any{"member": map[string]any{"gradeName": "金卡"}}}},
			}}
			input := pmsReadPlanInput{
				Scenario: pmsReadScenarioLateCheckout, ReceptOrderID: "REC-1", Phone: phone,
				StartDate: "2026-09-24", EndDate: "2026-09-25",
			}
			_, _ = executeRuntimePMSReadPlan(context.Background(), buildPMSReadPlan(input), input, invoker)
			for _, call := range invoker.calls {
				if !isRuntimePMSReadOnlyAction(call.action) {
					t.Fatalf("late checkout assessment attempted a write: %#v", invoker.calls)
				}
			}
		}
	})
}

func TestApplyRuntimePMSReadPlansAttachesFactsAndRemovesHandledTool(t *testing.T) {
	for _, subIntent := range []string{"order_query", "order_status"} {
		invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
			"reserve_order_by_phone": {{Status: pmsReadStepOK, Data: map[string]any{
				"reserveOrderId": "RES-1", "roomName": "大床房", "checkInBusinessDate": "2026-09-22", "checkOutBusinessDate": "2026-09-24",
			}}},
			"recept_order_by_phone": {{Status: pmsReadStepOK, Data: map[string]any{
				"receptOrderId": "REC-1", "homeName": "1401", "checkInBusinessDate": "2026-09-22", "checkOutBusinessDate": "2026-09-24",
			}}},
		}}
		intent := callbacks.IntentTraceData{
			NeedsTool: true, ToolCodes: []string{toolx.BuiltinPMSQuery.Code},
			IntentTasks: []callbacks.IntentTaskTraceData{{SubIntent: subIntent, NeedsTool: true}},
		}
		plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
			TaskID: "T1", Intent: "hotel_info", SubIntent: subIntent, OriginalText: "查 13800138000 的订单", NeedsTool: true,
			OutputKind: "text", ReplyRequired: true,
		}}}
		summary := &RunResult{}
		gotIntent, gotPlan, handled := applyRuntimePMSReadPlansWithInvoker(
			context.Background(), RunInput{}, adapter.HistoryBuildResult{}, intent, plan, summary, nil,
			time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local), invoker,
		)
		if !handled || gotIntent.NeedsTool || gotPlan.TaskPlans[0].NeedsTool || containsString(gotIntent.ToolCodes, toolx.BuiltinPMSQuery.Code) {
			t.Fatalf("handled PMS task must leave Generate without the tool: intent=%#v plan=%#v", gotIntent, gotPlan)
		}
		if len(gotPlan.TaskPlans[0].SupportedFacts) != 2 || !containsString(summary.InvokedToolCodes, toolx.BuiltinPMSQuery.Code) || summary.ToolCallCount != 1 {
			t.Fatalf("PMS facts or invocation trace missing: plan=%#v summary=%#v", gotPlan, summary)
		}
		if instruction := buildRuntimePMSResolvedInstruction(gotPlan); !strings.Contains(instruction, "不得再次调用 pms_query") {
			t.Fatalf("Generate boundary missing: %q", instruction)
		}
	}
}

func TestApplyRuntimePMSReadPlansExecutesMemberAndRoomStatus(t *testing.T) {
	t.Run("member routes query the documented phone actions", func(t *testing.T) {
		for _, test := range []struct {
			subIntent string
			action    string
		}{
			{subIntent: "member_info", action: "member_info_by_phone"},
			{subIntent: "member_benefits", action: "member_benefits_by_phone"},
		} {
			invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
				test.action: {{Status: pmsReadStepOK, Data: map[string]any{"member": map[string]any{"gradeName": "金卡", "statusName": "正常"}}}},
			}}
			intent := callbacks.IntentTraceData{NeedsTool: true, ToolCodes: []string{toolx.BuiltinPMSQuery.Code}, IntentTasks: []callbacks.IntentTaskTraceData{{SubIntent: test.subIntent, NeedsTool: true}}}
			plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{TaskID: "T1", Intent: "hotel_info", SubIntent: test.subIntent, OriginalText: "查一下 13800138000 的会员", NeedsTool: true, OutputKind: "text", ReplyRequired: true}}}
			_, gotPlan, handled := applyRuntimePMSReadPlansWithInvoker(context.Background(), RunInput{}, adapter.HistoryBuildResult{}, intent, plan, &RunResult{}, nil, time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local), invoker)
			if !handled || len(invoker.calls) != 1 || invoker.calls[0].action != test.action || invoker.calls[0].args["phone"] != "13800138000" || len(gotPlan.TaskPlans[0].SupportedFacts) != 1 {
				t.Fatalf("member route was not executed deterministically: test=%#v calls=%#v plan=%#v", test, invoker.calls, gotPlan)
			}
		}
	})

	t.Run("room status uses both explicit and order-bound rooms", func(t *testing.T) {
		for _, plan := range []callbacks.ReplyPlanTraceData{
			{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{TaskID: "T1", SubIntent: "room_status", OriginalText: "1401现在打扫好了吗", NeedsTool: true, OutputKind: "text", ReplyRequired: true}}},
			{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{TaskID: "T1", SubIntent: "room_status", OriginalText: "查接待单ID:REC-1这个房间状态", NeedsTool: true, OutputKind: "text", ReplyRequired: true}}},
		} {
			invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
				"recept_order_detail": {{Status: pmsReadStepOK, Data: map[string]any{"receptOrderId": "REC-1", "homeName": "1502"}}},
				"room_status":         {{Status: pmsReadStepOK, Data: map[string]any{"list": []any{}}}},
			}}
			intent := callbacks.IntentTraceData{NeedsTool: true, ToolCodes: []string{toolx.BuiltinPMSQuery.Code}, IntentTasks: []callbacks.IntentTaskTraceData{{SubIntent: "room_status", NeedsTool: true}}}
			_, _, handled := applyRuntimePMSReadPlansWithInvoker(context.Background(), RunInput{}, adapter.HistoryBuildResult{}, intent, plan, &RunResult{}, nil, time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local), invoker)
			if !handled || !runtimePMSFakeCalled(invoker.calls, "room_status") {
				t.Fatalf("room-status route did not query PMS: %#v", invoker.calls)
			}
		}
	})
}

func TestApplyRuntimePMSReadPlansKeepsFactsOnTheirOwnTask(t *testing.T) {
	for _, pmsIndex := range []int{0, 1} {
		invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
			"reserve_order_by_phone": {{Status: pmsReadStepOK, Data: map[string]any{"reserveOrderId": "RES-1", "roomName": "大床房"}}},
			"recept_order_by_phone":  {{Status: pmsReadStepOK, Data: map[string]any{"receptOrderId": "REC-1", "homeName": "1401"}}},
		}}
		intentTasks := []callbacks.IntentTaskTraceData{
			{Intent: "hotel_info", SubIntent: "parking", NeedsKnowledge: true},
			{Intent: "hotel_info", SubIntent: "order_query", NeedsTool: true},
		}
		tasks := []callbacks.ReplyTaskPlanTraceData{
			{TaskID: "T1", Intent: "hotel_info", SubIntent: "parking", NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true},
			{TaskID: "T2", Intent: "hotel_info", SubIntent: "order_query", OriginalText: "查 13800138000 的订单", NeedsTool: true, OutputKind: "text", ReplyRequired: true},
		}
		if pmsIndex == 0 {
			intentTasks[0], intentTasks[1] = intentTasks[1], intentTasks[0]
			tasks[0], tasks[1] = tasks[1], tasks[0]
		}
		_, gotPlan, _ := applyRuntimePMSReadPlansWithInvoker(
			context.Background(), RunInput{}, adapter.HistoryBuildResult{},
			callbacks.IntentTraceData{NeedsTool: true, ToolCodes: []string{toolx.BuiltinPMSQuery.Code}, IntentTasks: intentTasks},
			callbacks.ReplyPlanTraceData{TaskPlans: tasks}, &RunResult{}, nil,
			time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local), invoker,
		)
		if len(gotPlan.TaskPlans[pmsIndex].SupportedFacts) == 0 || len(gotPlan.TaskPlans[1-pmsIndex].SupportedFacts) != 0 {
			t.Fatalf("PMS facts leaked across tasks: %#v", gotPlan.TaskPlans)
		}
	}
}

func TestApplyRuntimePMSReadPlansMatchesIntentTasksWithoutUsingReplyIndex(t *testing.T) {
	for _, replyPMSIndex := range []int{0, 1} {
		invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
			"reserve_order_by_phone": {{Status: pmsReadStepOK, Data: map[string]any{"reserveOrderId": "RES-1"}}},
			"recept_order_by_phone":  {{Status: pmsReadStepOK, Data: map[string]any{"receptOrderId": "REC-1"}}},
		}}
		intent := callbacks.IntentTraceData{NeedsTool: true, ToolCodes: []string{toolx.BuiltinPMSQuery.Code}, IntentTasks: []callbacks.IntentTaskTraceData{
			{SubIntent: "order_query", Text: "查订单", SourceRefs: []string{"U1"}, NeedsTool: true},
			{SubIntent: "parking", Text: "停车", SourceRefs: []string{"U2"}, NeedsKnowledge: true},
		}}
		tasks := []callbacks.ReplyTaskPlanTraceData{
			{TaskID: "T2", SubIntent: "parking", OriginalText: "停车", SourceRefs: []string{"U2"}, NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true},
			{TaskID: "T1", SubIntent: "order_query", OriginalText: "查 13800138000 的订单", Text: "查订单", SourceRefs: []string{"U1"}, NeedsTool: true, OutputKind: "text", ReplyRequired: true},
		}
		if replyPMSIndex == 0 {
			tasks[0], tasks[1] = tasks[1], tasks[0]
		}
		gotIntent, _, handled := applyRuntimePMSReadPlansWithInvoker(
			context.Background(), RunInput{}, adapter.HistoryBuildResult{}, intent,
			callbacks.ReplyPlanTraceData{TaskPlans: tasks}, &RunResult{}, nil,
			time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local), invoker,
		)
		if !handled || gotIntent.IntentTasks[0].NeedsTool || gotIntent.IntentTasks[1].NeedsTool {
			t.Fatalf("semantic task mapping failed: replyPMSIndex=%d intent=%#v", replyPMSIndex, gotIntent)
		}
	}
}

func TestApplyRuntimePMSReadPlansClearsDuplicatePMSIntentsByProcessedCount(t *testing.T) {
	invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
		"reserve_order_by_phone": {
			{Status: pmsReadStepOK, Data: map[string]any{"reserveOrderId": "RES-1"}},
			{Status: pmsReadStepOK, Data: map[string]any{"reserveOrderId": "RES-2"}},
		},
		"recept_order_by_phone": {
			{Status: pmsReadStepOK, Data: map[string]any{"receptOrderId": "REC-1"}},
			{Status: pmsReadStepOK, Data: map[string]any{"receptOrderId": "REC-2"}},
		},
	}}
	intent := callbacks.IntentTraceData{NeedsTool: true, ToolCodes: []string{toolx.BuiltinPMSQuery.Code}, IntentTasks: []callbacks.IntentTaskTraceData{
		{SubIntent: "order_query", NeedsTool: true},
		{SubIntent: "order_query", NeedsTool: true},
	}}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "T1", SubIntent: "order_query", OriginalText: "查 13800138000 的订单", NeedsTool: true, OutputKind: "text", ReplyRequired: true},
		{TaskID: "T2", SubIntent: "order_query", OriginalText: "查 13900139000 的订单", NeedsTool: true, OutputKind: "text", ReplyRequired: true},
	}}
	gotIntent, _, handled := applyRuntimePMSReadPlansWithInvoker(
		context.Background(), RunInput{}, adapter.HistoryBuildResult{}, intent, plan, &RunResult{}, nil,
		time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local), invoker,
	)
	if !handled || gotIntent.NeedsTool || gotIntent.IntentTasks[0].NeedsTool || gotIntent.IntentTasks[1].NeedsTool {
		t.Fatalf("all processed duplicate PMS intents must be cleared: %#v", gotIntent)
	}
}

func TestApplyRuntimePMSReadPlansDoesNotClaimInvocationWithoutHTTPCall(t *testing.T) {
	for _, text := range []string{"查一下还有什么房", "帮我看看库存"} {
		invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{}}
		summary := &RunResult{}
		_, gotPlan, handled := applyRuntimePMSReadPlansWithInvoker(
			context.Background(), RunInput{}, adapter.HistoryBuildResult{},
			callbacks.IntentTraceData{NeedsTool: true, ToolCodes: []string{toolx.BuiltinPMSQuery.Code}, IntentTasks: []callbacks.IntentTaskTraceData{{SubIntent: "room_inventory", NeedsTool: true}}},
			callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{TaskID: "T1", SubIntent: "room_inventory", OriginalText: text, NeedsTool: true, OutputKind: "text", ReplyRequired: true}}},
			summary, nil, time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local), invoker,
		)
		if !handled || len(invoker.calls) != 0 || containsString(summary.InvokedToolCodes, toolx.BuiltinPMSQuery.Code) {
			t.Fatalf("missing dates must not be recorded as an invocation: calls=%#v summary=%#v", invoker.calls, summary)
		}
		if len(gotPlan.TaskPlans[0].MissingAspects) == 0 {
			t.Fatalf("missing query fields must remain explicit: %#v", gotPlan.TaskPlans[0])
		}
	}
}

func runtimePMSFakeCalled(calls []runtimePMSFakeCall, action string) bool {
	for _, call := range calls {
		if call.action == action {
			return true
		}
	}
	return false
}
