package executor

import (
	"context"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestPMSOutcomeFailureNeverAsksForKnownDates(t *testing.T) {
	for _, kind := range []string{"denied", "unavailable", "empty", "invalid_state"} {
		task := callbacks.ReplyTaskPlanTraceData{
			TaskID: "task-1", SubIntent: "room_change",
			MissingAspects: []string{"目标日期库存暂未确认", "当前接待单信息暂未确认"},
			PMSOutcome: &callbacks.PMSOutcomeTraceData{
				Status: "unavailable", Issues: []callbacks.PMSIssueTraceData{{StepID: "order.recept", Kind: kind}},
			},
		}
		got := deterministicPMSMissingBoundary(callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{task}}, "task-1")
		if got == "" || strings.Contains(got, "告诉我想查询的入住和离店日期") || strings.Contains(got, "打算住到") {
			t.Fatalf("%s produced missing-date clarification: %q", kind, got)
		}
	}
}

func TestCurrentExplicitOrderLocatorOverridesPreviousOrder(t *testing.T) {
	for _, text := range []string{"查一下接待单号295976948935847936的状态", "我换了订单，接待单ID:295976948935847936"} {
		task := callbacks.ReplyTaskPlanTraceData{
			SubIntent: "order_detail", OriginalText: text,
			Entities: []callbacks.IntentEntityTraceData{
				{Type: runtimeIntentEntityOrderLocator, Text: "接待单ID:OLD-1"},
			},
		}
		input := runtimePMSReadPlanInputForTask(task, runtimePMSSessionLocator{OrderLocator: "接待单ID:OLD-1"}, time.Now())
		if input.ReceptOrderID != "295976948935847936" || input.ReserveOrderID != "" {
			t.Fatalf("current order correction lost: %#v", input)
		}
	}
}

func TestInventoryReplyUsesAvailableFactsEvenWhenGenerationFails(t *testing.T) {
	for _, available := range []string{"1", "3"} {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: pmsReadScenarioDateInventory, StartDate: "2026-09-29", EndDate: "2026-09-30",
		})
		result := pmsReadPlanResult{Status: pmsReadStepPartial, Steps: []pmsReadStepResult{
			{StepID: "inventory.stay", Status: pmsReadStepOK,
				Args: map[string]string{"beginTime": "2026-09-29", "endTime": "2026-09-30"},
				Data: []any{
					map[string]any{"roomTypeId": "room-1", "roomTypeName": "云漫", "bookings": map[string]any{
						"2026-09-29": map[string]any{"available": available}}},
					map[string]any{"roomTypeId": "room-2", "roomTypeName": "大床房", "bookings": map[string]any{
						"2026-09-29": map[string]any{"available": "0"}}},
				}},
			{StepID: "stay.room_availability", Status: pmsReadStepUnavailable, ErrorKind: "unavailable"},
		}}
		task := callbacks.ReplyTaskPlanTraceData{TaskID: "task-1", SubIntent: "room_inventory",
			ReplyRequired: true, OutputKind: "text", OriginalText: "这两天有什么房可选"}
		applyRuntimePMSReadResultToTask(&task, plan, result, 0)
		if task.AnswerText == nil || !strings.Contains(*task.AnswerText, "云漫") || strings.Contains(*task.AnswerText, "大床房") {
			t.Fatalf("available inventory facts discarded: %#v", task.AnswerText)
		}
		collector := &callbacks.RuntimeTraceCollector{}
		collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{task}}
		reply := deterministicGeneratedReplyFallback(collector)
		if !strings.Contains(reply, "云漫") || strings.Contains(reply, "未能") {
			t.Fatalf("generation fallback discarded inventory success: %q", reply)
		}
	}
}

func TestPMSDependencyFailureDoesNotBecomeMissingCustomerInput(t *testing.T) {
	for _, kind := range []string{"denied", "empty"} {
		input := pmsReadPlanInput{Scenario: pmsReadScenarioRoomChange, ReceptOrderID: "REC-1"}
		invoker := &runtimePMSFakeInvoker{results: map[string][]pmsReadStepResult{
			"recept_order_detail": {{Status: pmsReadStepUnavailable, ErrorKind: kind, Message: "query failed"}},
		}}
		steps, _ := executeRuntimePMSReadPlan(context.Background(), buildPMSReadPlan(input), input, invoker)
		found := false
		for _, step := range steps {
			if step.StepID == "inventory.stay" {
				found = true
				if step.ErrorKind != "dependency_failed" || len(step.MissingFields) != 0 {
					t.Fatalf("failed order became missing customer dates: %#v", step)
				}
			}
		}
		if !found {
			t.Fatal("inventory dependency was not evaluated")
		}
	}
}

func TestPMSClosedStayNeverOffersRoomChange(t *testing.T) {
	for _, withInventory := range []bool{false, true} {
		steps := []pmsReadStepResult{{StepID: "order.recept", Status: pmsReadStepOK, Data: map[string]any{
			"receptOrderId": "REC-1", "orderStatus": "0015003", "roomName": "儿童房", "homeName": "V05",
			"checkInTime": "2026-09-23 14:00:00", "checkOutTime": "2026-09-28 10:09:39",
		}}}
		if withInventory {
			steps = append(steps, pmsReadStepResult{StepID: "inventory.stay", Status: pmsReadStepOK, Data: []any{
				map[string]any{"roomTypeName": "豪华房", "roomTypeId": "room-2", "roomCount": 3},
			}})
		}
		answer := runtimePMSCustomerRoomChoiceAnswer(
			callbacks.ReplyTaskPlanTraceData{SubIntent: "room_change"},
			pmsReadPlan{Scenario: pmsReadScenarioRoomChange}, pmsReadPlanResult{Steps: steps})
		if !strings.Contains(answer, "已经退房") || strings.Contains(answer, "可以选择") {
			t.Fatalf("closed stay offered room change: %q", answer)
		}
	}
}

func TestPMSClosedStayCannotQuoteNewRoomDifferenceButStillAnswersHistory(t *testing.T) {
	result := pmsReadPlanResult{Steps: []pmsReadStepResult{{
		StepID: "order.recept", Status: pmsReadStepOK, Data: map[string]any{
			"receptOrderId": "REC-1", "orderStatus": "0015003", "roomName": "儿童房",
			"payableAmount": "376.00", "checkOutTime": "2026-09-28 10:09:39",
		},
	}}}
	for _, question := range []string{"云漫比儿童房要补多少钱？", "重新选这个房要补多少？"} {
		answer := runtimePMSCustomerPriceAnswer(callbacks.ReplyTaskPlanTraceData{
			SubIntent: "price_difference", OriginalText: question,
		}, result)
		if strings.Contains(answer, "376") || !strings.Contains(answer, "已经退房") {
			t.Fatalf("closed order became current price basis: %q", answer)
		}
	}
	for _, question := range []string{"之前那笔订单金额是多少", "帮我看历史订单的房费"} {
		answer := runtimePMSCustomerOrderAnswer(callbacks.ReplyTaskPlanTraceData{
			SubIntent: "order_detail", OriginalText: question, Text: question, Objective: "price",
		}, result)
		if !strings.Contains(answer, "376") {
			t.Fatalf("history query lost its requested amount: %q", answer)
		}
	}
}

func TestPMSOutcomeCarriesActualMissingFieldsSeparately(t *testing.T) {
	for _, field := range []string{"inventoryStartDate", "inventoryEndDate"} {
		task := callbacks.ReplyTaskPlanTraceData{SubIntent: "room_inventory", PMSOutcome: &callbacks.PMSOutcomeTraceData{
			Status: "unavailable", MissingFields: []string{field},
		}}
		if got := runtimePMSOutcomeBoundary(task); !strings.Contains(got, "哪天入住") {
			t.Fatalf("missing field not requested: %s %q", field, got)
		}
	}
}

func TestPublicMembershipPlanDoesNotDemandIdentity(t *testing.T) {
	for _, phone := range []string{"", "13800138000"} {
		plan := buildPMSReadPlan(pmsReadPlanInput{SubIntent: "member_program", Phone: phone})
		for _, field := range plan.Missing {
			if field == "memberPhone" || field == "customerLocator" {
				t.Fatalf("public rules cannot demand personal identity: %#v", plan)
			}
		}
		if len(plan.Missing) != 1 || plan.Missing[0] != "memberGradeCatalog" {
			t.Fatalf("missing catalog must be explicit: %#v", plan)
		}
	}
}

func TestPMSReadToolResultPreservesBusinessFailure(t *testing.T) {
	for _, raw := range []string{
		`{"status":"unavailable","errorKind":"denied","businessCode":"403","httpStatus":200}`,
		`{"status":"empty","errorKind":"empty","businessCode":"1010005035","httpStatus":200}`,
	} {
		result := parseRuntimePMSReadToolResult(raw, nil)
		if result.ErrorKind == "" || result.BusinessCode == "" || result.HTTPStatus != 200 {
			t.Fatalf("lost safe diagnostics: %#v", result)
		}
	}
}
