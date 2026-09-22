package executor

import (
	"reflect"
	"testing"
)

func TestBuildPMSReadPlanOrder(t *testing.T) {
	t.Run("phone queries both current order types", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{SubIntent: "order_query", Phone: "13800138000"})
		if plan.Scenario != pmsReadScenarioOrder || len(plan.Steps) != 2 || len(plan.Missing) != 0 {
			t.Fatalf("unexpected order plan: %#v", plan)
		}
		reserve := requirePMSReadStep(t, plan, "order.reserve")
		recept := requirePMSReadStep(t, plan, "order.recept")
		if reserve.Action != "reserve_order_by_phone" || recept.Action != "recept_order_by_phone" ||
			reserve.Args["phone"] != "13800138000" || recept.Args["phone"] != "13800138000" {
			t.Fatalf("phone order plan changed: %#v", plan.Steps)
		}
	})

	t.Run("known reception order uses detail only", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{Scenario: pmsReadScenarioOrder, ReceptOrderID: "90071992547409931"})
		if len(plan.Steps) != 1 || plan.Steps[0].Action != "recept_order_detail" ||
			plan.Steps[0].Args["receptOrderId"] != "90071992547409931" {
			t.Fatalf("known order must avoid broad lookup: %#v", plan)
		}
	})
}

func TestBuildPMSReadPlanRoomStatus(t *testing.T) {
	t.Run("explicit room queries current status directly", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{SubIntent: "room_status", RoomKeyword: "1401"})
		if len(plan.Steps) != 1 || len(plan.Missing) != 0 {
			t.Fatalf("unexpected direct room-status plan: %#v", plan)
		}
		step := requirePMSReadStep(t, plan, "room.status")
		if step.Action != "room_status" || step.Args["keyword"] != "1401" {
			t.Fatalf("room-status query lost the room: %#v", step)
		}
	})

	t.Run("known stay binds its current room", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{Scenario: pmsReadScenarioRoomStatus, ReceptOrderID: "REC-1"})
		if len(plan.Steps) != 2 || len(plan.Missing) != 0 {
			t.Fatalf("unexpected order-bound room-status plan: %#v", plan)
		}
		assertPMSReadBinding(t, requirePMSReadStep(t, plan, "room.status"), "keyword", "order.recept")
	})
}

func TestBuildPMSReadPlanMemberQueries(t *testing.T) {
	t.Run("member info uses the supplied phone", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{SubIntent: "member_info", Phone: "13800138000"})
		if len(plan.Steps) != 1 || len(plan.Missing) != 0 {
			t.Fatalf("unexpected member-info plan: %#v", plan)
		}
		step := requirePMSReadStep(t, plan, "member.info")
		if step.Action != "member_info_by_phone" || step.Args["phone"] != "13800138000" {
			t.Fatalf("member-info phone changed: %#v", step)
		}
	})

	t.Run("member benefits uses the composite phone action", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{SubIntent: "member_benefits", Phone: "13900139000"})
		if len(plan.Steps) != 1 || len(plan.Missing) != 0 {
			t.Fatalf("unexpected member-benefit plan: %#v", plan)
		}
		step := requirePMSReadStep(t, plan, "member.benefits")
		if step.Action != "member_benefits_by_phone" || step.Args["phone"] != "13900139000" {
			t.Fatalf("member-benefit phone changed: %#v", step)
		}
	})

	t.Run("missing member phone stays explicit", func(t *testing.T) {
		for _, subIntent := range []string{"member_info", "member_benefits"} {
			plan := buildPMSReadPlan(pmsReadPlanInput{SubIntent: subIntent})
			if len(plan.Steps) != 0 || !containsPMSReadString(plan.Missing, "memberPhone") {
				t.Fatalf("%s must not query without a phone: %#v", subIntent, plan)
			}
		}
	})
}

func TestBuildPMSReadPlanDateInventory(t *testing.T) {
	t.Run("explicit dates never need an order lookup", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: pmsReadScenarioDateInventory, StartDate: "2026-09-23", EndDate: "2026-09-25", TargetRoomTypeID: "ROOM-2",
		})
		if len(plan.Steps) != 1 || len(plan.Missing) != 0 {
			t.Fatalf("unexpected explicit inventory plan: %#v", plan)
		}
		inventory := requirePMSReadStep(t, plan, "inventory.stay")
		if inventory.Args["beginTime"] != "2026-09-23" || inventory.Args["endTime"] != "2026-09-25" ||
			inventory.Args["roomTypeId"] != "ROOM-2" {
			t.Fatalf("inventory dates or target changed: %#v", inventory)
		}
	})

	t.Run("missing dates bind to the known order", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{Scenario: pmsReadScenarioDateInventory, ReserveOrderID: "RES-1"})
		if len(plan.Steps) != 2 || len(plan.Missing) != 0 {
			t.Fatalf("unexpected order-derived inventory plan: %#v", plan)
		}
		inventory := requirePMSReadStep(t, plan, "inventory.stay")
		assertPMSReadBinding(t, inventory, "beginTime", "order.reserve")
		assertPMSReadBinding(t, inventory, "endTime", "order.reserve")
	})

	t.Run("invalid interval never reaches inventory", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: pmsReadScenarioDateInventory, StartDate: "2026-09-25", EndDate: "2026-09-23",
		})
		if hasPMSReadStep(plan, "inventory.stay") || !containsPMSReadString(plan.Missing, "validInventoryDateRange") {
			t.Fatalf("invalid inventory interval must be blocked mechanically: %#v", plan)
		}
	})
}

func TestBuildPMSReadPlanRoomUpgrade(t *testing.T) {
	t.Run("complete upgrade evaluates order inventory member and price", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			SubIntent: "room_upgrade", Phone: "13800138000", ReceptOrderID: "REC-1",
			TargetRoomTypeID: "ROOM-2", StartDate: "2026-09-23", EndDate: "2026-09-25",
		})
		if len(plan.Steps) != 4 || len(plan.Missing) != 0 {
			t.Fatalf("unexpected upgrade plan: %#v", plan)
		}
		for _, id := range []string{"order.recept", "inventory.stay", "member.benefits", "price.difference"} {
			requirePMSReadStep(t, plan, id)
		}
		price := requirePMSReadStep(t, plan, "price.difference")
		if price.Required || price.Args["receptOrderId"] != "REC-1" || price.Args["roomTypeId"] != "ROOM-2" {
			t.Fatalf("upgrade price step must remain optional and grounded: %#v", price)
		}
	})

	t.Run("unknown target keeps real options query but skips price", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{Scenario: pmsReadScenarioRoomUpgrade, Phone: "13800138000"})
		if containsPMSReadString(plan.Missing, "targetRoomTypeId") || hasPMSReadStep(plan, "price.difference") {
			t.Fatalf("upgrade must not guess a target room type: %#v", plan)
		}
		inventory := requirePMSReadStep(t, plan, "inventory.stay")
		if inventory.Args["roomTypeId"] != "" {
			t.Fatalf("inventory must list real options when target is unknown: %#v", inventory)
		}
		assertPMSReadBinding(t, inventory, "beginTime", "order.reserve")
	})

	t.Run("explicit price question still requires a target room type", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{Scenario: pmsReadScenarioPrice, Phone: "13800138000"})
		if !containsPMSReadString(plan.Missing, "targetRoomTypeId") || hasPMSReadStep(plan, "price.difference") {
			t.Fatalf("price comparison must wait for a concrete target room type: %#v", plan)
		}
	})
}

func TestBuildPMSReadPlanRoomChange(t *testing.T) {
	t.Run("known stay queries target inventory and current room status", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: pmsReadScenarioRoomChange, ReceptOrderID: "REC-1", RoomKeyword: "1401",
			TargetRoomTypeID: "ROOM-2", StartDate: "2026-09-23", EndDate: "2026-09-25",
		})
		if len(plan.Steps) != 4 || len(plan.Missing) != 0 {
			t.Fatalf("unexpected room change plan: %#v", plan)
		}
		room := requirePMSReadStep(t, plan, "room.status")
		if room.Args["keyword"] != "1401" {
			t.Fatalf("room status lost the explicit room: %#v", room)
		}
	})

	t.Run("phone lookup binds room and dates without inventing a room", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{SubIntent: "room_change", Phone: "13800138000"})
		if len(plan.Steps) != 3 || containsPMSReadString(plan.Missing, "targetRoomTypeId") {
			t.Fatalf("unexpected contextual room change plan: %#v", plan)
		}
		room := requirePMSReadStep(t, plan, "room.status")
		assertPMSReadBinding(t, room, "keyword", "order.recept")
		inventory := requirePMSReadStep(t, plan, "inventory.stay")
		assertPMSReadBinding(t, inventory, "beginTime", "order.recept")
		assertPMSReadBinding(t, inventory, "endTime", "order.recept")
		if inventory.Args["roomTypeId"] != "" {
			t.Fatalf("unknown target must list real options instead of reusing the current room type: %#v", inventory)
		}
		for _, binding := range inventory.Bindings {
			if binding.Argument == "roomTypeId" {
				t.Fatalf("unknown target must not bind the current room type as the requested target: %#v", inventory)
			}
		}
	})
}

func TestBuildPMSReadPlanPriceDifference(t *testing.T) {
	t.Run("known order and target produce independent facts and assessment", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: pmsReadScenarioPrice, ReserveOrderID: "RES-1", TargetRoomTypeID: "ROOM-2",
			StartDate: "2026-09-23", EndDate: "2026-09-25",
		})
		if len(plan.Steps) != 3 || len(plan.Missing) != 0 {
			t.Fatalf("unexpected price plan: %#v", plan)
		}
		price := requirePMSReadStep(t, plan, "price.difference")
		if !price.Required || price.Args["reserveOrderId"] != "RES-1" || price.Args["roomTypeId"] != "ROOM-2" {
			t.Fatalf("price assessment lost required identifiers: %#v", price)
		}
	})

	t.Run("phone-located order binds a real order id", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			SubIntent: "upgrade_price", Phone: "13800138000", TargetRoomTypeID: "ROOM-2",
			StartDate: "2026-09-23", EndDate: "2026-09-25",
		})
		if len(plan.Steps) != 4 || len(plan.Missing) != 0 {
			t.Fatalf("unexpected phone price plan: %#v", plan)
		}
		price := requirePMSReadStep(t, plan, "price.difference")
		assertPMSReadBinding(t, price, "reserveOrderId", "order.reserve")
		assertPMSReadBinding(t, price, "receptOrderId", "order.recept")
	})
}

func TestBuildPMSReadPlanRenewal(t *testing.T) {
	t.Run("known reception order checks extension inventory and candidates", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: pmsReadScenarioRenewal, ReceptOrderID: "REC-1", EndDate: "2026-09-26",
		})
		if len(plan.Steps) != 3 || len(plan.Missing) != 0 {
			t.Fatalf("unexpected renewal plan: %#v", plan)
		}
		inventory := requirePMSReadStep(t, plan, "inventory.stay")
		assertPMSReadBinding(t, inventory, "beginTime", "order.recept")
		candidate := requirePMSReadStep(t, plan, "renew.candidates")
		if candidate.Args["currentReceptOrderId"] != "REC-1" || candidate.Action != "renew_candidates" {
			t.Fatalf("renewal must remain read-only and grounded: %#v", candidate)
		}
	})

	t.Run("phone lookup supplies the reception id without a write step", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			SubIntent: "renewal", Phone: "13800138000", StartDate: "2026-09-25", EndDate: "2026-09-27",
		})
		if len(plan.Steps) != 4 || hasPMSReadAction(plan, "renew") {
			t.Fatalf("renewal plan exposed a write: %#v", plan)
		}
		requirePMSReadStep(t, plan, "order.reserve")
		requirePMSReadStep(t, plan, "order.recept")
		candidate := requirePMSReadStep(t, plan, "renew.candidates")
		assertPMSReadBinding(t, candidate, "currentReceptOrderId", "order.reserve")
		assertPMSReadBinding(t, candidate, "currentReceptOrderId", "order.recept")
		if candidate.Args["reservePhone"] != "13800138000" {
			t.Fatalf("candidate lookup lost the explicit phone: %#v", candidate)
		}
	})

	t.Run("reserve detail may supply its linked reception id", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: pmsReadScenarioRenewal, ReserveOrderID: "RES-1", StartDate: "2026-09-25", EndDate: "2026-09-27",
		})
		candidate := requirePMSReadStep(t, plan, "renew.candidates")
		assertPMSReadBinding(t, candidate, "currentReceptOrderId", "order.reserve")
	})

	t.Run("relative one-day extension derives the end date from the current checkout", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: pmsReadScenarioRenewal, Phone: "13800138000", ExtensionDays: 1,
		})
		inventory := requirePMSReadStep(t, plan, "inventory.stay")
		assertPMSReadBinding(t, inventory, "beginTime", "order.reserve")
		assertPMSReadBinding(t, inventory, "endTime", "order.reserve")
		assertPMSReadBinding(t, inventory, "roomTypeId", "order.reserve")
		for _, binding := range inventory.Bindings {
			if binding.Argument == "endTime" && binding.DateOffsetDays != 1 {
				t.Fatalf("relative renewal end date must add exactly one day: %#v", binding)
			}
		}
	})
}

func TestBuildPMSReadPlanLateCheckout(t *testing.T) {
	t.Run("late checkout combines order room inventory and member facts", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: pmsReadScenarioLateCheckout, Phone: "13800138000", ReceptOrderID: "REC-1", RoomKeyword: "1401",
			StartDate: "2026-09-25", EndDate: "2026-09-26", TargetCheckoutTime: "15:00",
		})
		if len(plan.Steps) != 5 || len(plan.Missing) != 0 {
			t.Fatalf("unexpected late checkout plan: %#v", plan)
		}
		for _, id := range []string{"order.recept", "room.status", "inventory.stay", "member.benefits", "late_checkout.assessment"} {
			requirePMSReadStep(t, plan, id)
		}
		assessment := requirePMSReadStep(t, plan, "late_checkout.assessment")
		if assessment.Args["targetCheckoutTime"] != "15:00" || assessment.Args["targetCheckoutDate"] != "2026-09-26" {
			t.Fatalf("late checkout target slot was not preserved: %#v", assessment)
		}
		assertPMSReadBinding(t, assessment, "currentCheckoutTime", "order.recept")
	})

	t.Run("explicit checkout time does not require an inventory date", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{SubIntent: "late_checkout", Phone: "13800138000", TargetCheckoutTime: "15:30"})
		if len(plan.Steps) != 4 || len(plan.Missing) != 0 {
			t.Fatalf("time-only late checkout assessment changed: %#v", plan)
		}
		if hasPMSReadStep(plan, "inventory.stay") {
			t.Fatalf("late checkout must not query inventory without explicit dates: %#v", plan)
		}
		room := requirePMSReadStep(t, plan, "room.status")
		assertPMSReadBinding(t, room, "keyword", "order.recept")
		assessment := requirePMSReadStep(t, plan, "late_checkout.assessment")
		if assessment.Args["targetCheckoutTime"] != "15:30" {
			t.Fatalf("target checkout time changed: %#v", assessment)
		}
	})

	t.Run("missing checkout time stays explicit", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{SubIntent: "late_checkout", Phone: "13800138000"})
		if !containsPMSReadString(plan.Missing, "targetCheckoutTime") || hasPMSReadStep(plan, "late_checkout.assessment") {
			t.Fatalf("missing target checkout time must not be guessed: %#v", plan)
		}
	})

	t.Run("one target date does not create an invalid inventory range", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			SubIntent: "late_checkout", Phone: "13800138000", EndDate: "2026-09-25", TargetCheckoutTime: "15:00",
		})
		if hasPMSReadStep(plan, "inventory.stay") || len(plan.Missing) != 0 {
			t.Fatalf("single checkout date must stay on the assessment instead of becoming inventory range: %#v", plan)
		}
		assessment := requirePMSReadStep(t, plan, "late_checkout.assessment")
		if assessment.Args["targetCheckoutDate"] != "2026-09-25" {
			t.Fatalf("target checkout date was lost: %#v", assessment)
		}
	})
}

func TestAggregatePMSReadPlanResultsPreservesPartialSuccess(t *testing.T) {
	t.Run("order keeps preorder when reception lookup fails", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{Scenario: pmsReadScenarioOrder, Phone: "13800138000"})
		result, err := aggregatePMSReadPlanResults(plan, []pmsReadStepResult{
			{StepID: "order.reserve", Status: pmsReadStepOK, Data: map[string]any{"reserveOrderId": "RES-1"}},
			{StepID: "order.recept", Status: pmsReadStepUnavailable, Message: "接待单查询暂时不可用"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "partial" || result.Confirmed["order.reserve"] == nil || result.Confirmed["order.recept"] != nil ||
			!containsPMSReadString(result.Unconfirmed, "order.recept") {
			t.Fatalf("partial order facts were not preserved: %#v", result)
		}
	})

	t.Run("upgrade keeps order and inventory when member and price fail", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: pmsReadScenarioRoomUpgrade, Phone: "13800138000", ReceptOrderID: "REC-1",
			TargetRoomTypeID: "ROOM-2", StartDate: "2026-09-23", EndDate: "2026-09-25",
		})
		result, err := aggregatePMSReadPlanResults(plan, []pmsReadStepResult{
			{StepID: "order.recept", Status: pmsReadStepOK, Data: map[string]any{"roomName": "标准房"}},
			{StepID: "inventory.stay", Status: pmsReadStepOK, Data: []any{map[string]any{"roomTypeId": "ROOM-2"}}},
			{StepID: "member.benefits", Status: pmsReadStepUnavailable, Message: "会员查询失败"},
			{StepID: "price.difference", Status: pmsReadStepUnavailable, Message: "价格不完整"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "partial" || len(result.Confirmed) != 2 ||
			result.Confirmed["order.recept"] == nil || result.Confirmed["inventory.stay"] == nil ||
			!reflect.DeepEqual(result.Unconfirmed, []string{"member.benefits", "price.difference"}) {
			t.Fatalf("successful upgrade subqueries were lost: %#v", result)
		}
	})

	t.Run("composite member query keeps returned member facts", func(t *testing.T) {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: pmsReadScenarioLateCheckout, Phone: "13800138000", ReceptOrderID: "REC-1",
			RoomKeyword: "1401", StartDate: "2026-09-25", EndDate: "2026-09-26",
		})
		results := make([]pmsReadStepResult, 0, len(plan.Steps))
		for _, step := range plan.Steps {
			status := pmsReadStepOK
			data := any(map[string]any{"step": step.ID})
			if step.ID == "member.benefits" {
				status = pmsReadStepPartial
				data = map[string]any{"member": map[string]any{"gradeName": "金卡"}, "grade": nil}
			}
			results = append(results, pmsReadStepResult{StepID: step.ID, Status: status, Data: data})
		}
		result, err := aggregatePMSReadPlanResults(plan, results)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != pmsReadStepPartial || result.Confirmed["member.benefits"] == nil ||
			!containsPMSReadString(result.Unconfirmed, "member.benefits") {
			t.Fatalf("partial member facts were discarded: %#v", result)
		}
	})
}

func TestBuildPMSReadPlanNeverEmitsWriteActions(t *testing.T) {
	allowed := map[string]bool{
		"reserve_order_detail": true, "reserve_order_by_phone": true,
		"recept_order_detail": true, "recept_order_by_phone": true,
		"renew_candidates": true, "room_status": true, "inventory": true,
		"member_info_by_phone": true, "member_benefits_by_phone": true, "price_difference": true,
	}
	for _, scenario := range []pmsReadScenario{
		pmsReadScenarioOrder, pmsReadScenarioRoomStatus, pmsReadScenarioDateInventory,
		pmsReadScenarioMemberInfo, pmsReadScenarioMemberBenefit, pmsReadScenarioRoomUpgrade,
		pmsReadScenarioRoomChange, pmsReadScenarioPrice, pmsReadScenarioRenewal, pmsReadScenarioLateCheckout,
	} {
		plan := buildPMSReadPlan(pmsReadPlanInput{
			Scenario: scenario, Phone: "13800138000", ReserveOrderID: "RES-1", ReceptOrderID: "REC-1",
			RoomKeyword: "1401", TargetRoomTypeID: "ROOM-2", StartDate: "2026-09-23", EndDate: "2026-09-25",
		})
		for _, step := range plan.Steps {
			if !allowed[step.Action] {
				t.Fatalf("scenario %s exposed non-read action %s: %#v", scenario, step.Action, plan)
			}
		}
	}
}

func requirePMSReadStep(t *testing.T, plan pmsReadPlan, id string) pmsReadPlanStep {
	t.Helper()
	for _, step := range plan.Steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("missing PMS read step %s in %#v", id, plan)
	return pmsReadPlanStep{}
}

func assertPMSReadBinding(t *testing.T, step pmsReadPlanStep, argument, sourceStep string) {
	t.Helper()
	for _, binding := range step.Bindings {
		if binding.Argument != argument {
			continue
		}
		for _, source := range binding.Sources {
			if source.StepID == sourceStep && len(source.Fields) > 0 {
				return
			}
		}
	}
	t.Fatalf("missing binding %s <- %s in %#v", argument, sourceStep, step)
}

func hasPMSReadStep(plan pmsReadPlan, id string) bool {
	for _, step := range plan.Steps {
		if step.ID == id {
			return true
		}
	}
	return false
}

func hasPMSReadAction(plan pmsReadPlan, action string) bool {
	for _, step := range plan.Steps {
		if step.Action == action {
			return true
		}
	}
	return false
}

func containsPMSReadString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
