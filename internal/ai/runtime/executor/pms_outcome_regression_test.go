package executor

import (
	"context"
	"strings"
	"testing"

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
