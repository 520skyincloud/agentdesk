package executor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
