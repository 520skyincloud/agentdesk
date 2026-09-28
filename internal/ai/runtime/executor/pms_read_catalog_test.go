package executor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestOrderHistorySearchExplicitAndFallback(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		input := pmsReadPlanInput{Scenario: pmsReadScenarioOrder, Phone: "13800000000", OrderHistory: explicit}
		data := map[string]any{"rows": []any{map[string]any{
			"receptOrderId": "9007199254740993", "roomName": "云漫", "orderStatus": "0015003",
			"checkInTime": "2026-09-24 12:00:00", "checkOutTime": "2026-09-28 12:00:00",
		}}}
		invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
			"reserve_order_by_phone": {{Status: pmsReadStepEmpty}},
			"recept_order_by_phone":  {{Status: pmsReadStepEmpty}},
			"orders_by_phone":        {{Status: pmsReadStepOK, Data: data}},
		}}
		results, plan := executeRuntimePMSReadPlan(context.Background(), buildPMSReadPlan(input), input, invoker)
		if !runtimePMSFakeCalled(invoker.calls, "orders_by_phone") ||
			(explicit && runtimePMSFakeCalled(invoker.calls, "recept_order_by_phone")) {
			t.Fatalf("history routing failed: %#v", invoker.calls)
		}
		aggregate, err := aggregatePMSReadPlanResults(plan, results)
		if err != nil {
			t.Fatal(err)
		}
		answer := runtimePMSCustomerAnswer(callbacks.ReplyTaskPlanTraceData{}, plan, aggregate)
		if !strings.Contains(answer, "已退房") || !strings.Contains(answer, "云漫") ||
			strings.Contains(answer, "9007199254740993") || strings.Contains(answer, "13800000000") {
			t.Fatalf("history answer wrong or leaks identifiers: %s", answer)
		}
		if !runtimePMSReadHasOtherOrderFact(aggregate, "order.recept") {
			t.Fatal("history must suppress contradictory empty current-order facts")
		}
	}
}

func TestCatalogFollowupsUseCurrentTurnLocatorAndDates(t *testing.T) {
	now := time.Date(2026, 9, 28, 15, 0, 0, 0, time.FixedZone("CST", 8*3600))
	session := runtimePMSSessionLocator{Phone: "13900000000", OrderLocator: "接待单ID:123"}
	task := callbacks.ReplyTaskPlanTraceData{SubIntent: "upgrade_eligibility", OriginalText: "能免费升房吗？",
		Entities: []callbacks.IntentEntityTraceData{{Type: runtimeIntentEntityCustomerPhone, Text: "13900000000"}}}
	input := runtimePMSReadPlanInputForTask(task, session, now)
	applyRuntimePMSCurrentTurnPhone(&input, task, session, "我的会员是13800000000，帮我看看权益，能免费升房吗？")
	if input.Phone != "13800000000" || input.ReceptOrderID != "" {
		t.Fatalf("sibling task retained old identity: %#v", input)
	}
	for _, tc := range []struct{ current, resolved, start, end string }{
		{"我还想住一晚，今晚原来的房间还能住吗，多少钱？", "", "2026-09-28", "2026-09-29"},
		{"如果我住到9月30日中午呢，这两晚房价分别多少？", "今晚再住一晚。\n当前客户补充：如果我住到9月30日中午呢", "", "2026-09-30"},
	} {
		start, end := runtimePMSReadDates(callbacks.ReplyTaskPlanTraceData{OriginalText: tc.current, ResolvedText: tc.resolved}, pmsReadScenarioRenewal, now)
		if start != tc.start || end != tc.end {
			t.Fatalf("current date contaminated by history: %s => %s/%s", tc.current, start, end)
		}
	}
	if normalizeRuntimePMSRoomTypeText("沐阳呢") != normalizeRuntimePMSRoomTypeText("沐阳") {
		t.Fatal("question particle became part of the selected room type")
	}
	plan := buildPMSReadPlan(pmsReadPlanInput{Scenario: pmsReadScenarioRoomChange, Phone: "13800000000", AssessMembership: true})
	if !hasPMSReadAction(plan, "member_benefits_by_phone") {
		t.Fatal("room change with membership question skipped membership query")
	}
}

func TestMembershipClassificationKeepsConditionedBenefitsTogether(t *testing.T) {
	if !strings.Contains(jevIntentClassificationRules, "one member_program task") ||
		!strings.Contains(jevIntentRouteCriteria()["checkout_process"].(string), "conditioned on a membership tier") {
		t.Fatal("membership-conditioned checkout must not become an unrelated generic policy")
	}
}

func TestPublicDiamondBenefitsAndDatedBoardProjection(t *testing.T) {
	answer := runtimePMSProgramAnswer(callbacks.ReplyTaskPlanTraceData{OriginalText: "钻石会员有哪些权益，怎么保级？"},
		map[string]any{"grades": []any{
			map[string]any{"gradeName": "普通会员", "gradeAvailable": true, "benefits": []any{map[string]any{"label": "9.9折"}}},
			map[string]any{"gradeName": "钻石会员", "gradeAvailable": true, "benefits": []any{map[string]any{"label": "8.5折"}, map[string]any{"label": "15:00退房"}},
				"keepGradeRuleSummary": "2000成长值或12房夜"},
		}})
	for _, want := range []string{"钻石", "8.5折", "15:00", "2000成长值或12房夜"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("missing benefit %s: %s", want, answer)
		}
	}
	if strings.Contains(answer, "普通会员") {
		t.Fatalf("unrequested grade included: %s", answer)
	}
	step := pmsReadStepResult{StepID: "price.board", Status: pmsReadStepOK,
		Args: map[string]string{"beginTime": "2026-09-28", "endTime": "2026-09-29", "roomTypeId": "R1"},
		Data: []any{map[string]any{"roomId": "R1", "roomTypeName": "云漫", "bookings": map[string]any{
			"2026-09-28": map[string]any{"price": json.Number("218")},
			"2026-09-29": map[string]any{"price": json.Number("999")},
		}}}}
	price := runtimePMSCustomerPriceClause(callbacks.ReplyTaskPlanTraceData{}, pmsReadPlanResult{Steps: []pmsReadStepResult{step}})
	if !strings.Contains(price, "218元") || strings.Contains(price, "999") || strings.Contains(price, "没有显示") {
		t.Fatalf("dated price missing or checkout day charged: %s", price)
	}
}
