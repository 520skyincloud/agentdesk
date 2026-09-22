package executor

import (
	"fmt"
	"strings"
	"time"
)

type pmsReadScenario string

const (
	pmsReadScenarioOrder         pmsReadScenario = "order"
	pmsReadScenarioRoomStatus    pmsReadScenario = "room_status"
	pmsReadScenarioDateInventory pmsReadScenario = "date_inventory"
	pmsReadScenarioMemberInfo    pmsReadScenario = "member_info"
	pmsReadScenarioMemberBenefit pmsReadScenario = "member_benefits"
	pmsReadScenarioRoomUpgrade   pmsReadScenario = "room_upgrade"
	pmsReadScenarioRoomChange    pmsReadScenario = "room_change"
	pmsReadScenarioPrice         pmsReadScenario = "price_difference"
	pmsReadScenarioRenewal       pmsReadScenario = "renewal"
	pmsReadScenarioLateCheckout  pmsReadScenario = "late_checkout"
)

type pmsReadPlanInput struct {
	Scenario           pmsReadScenario
	SubIntent          string
	Phone              string
	CustomerNo         string
	ReserveOrderID     string
	ReceptOrderID      string
	RoomKeyword        string
	TargetRoomTypeID   string
	TargetRoomTypeText string
	StartDate          string
	EndDate            string
}

type pmsReadPlan struct {
	Scenario pmsReadScenario
	Steps    []pmsReadPlanStep
	Missing  []string
}

type pmsReadPlanStep struct {
	ID              string
	Action          string
	Purpose         string
	Required        bool
	Args            map[string]string
	Bindings        []pmsReadPlanBinding
	RequiredArgs    []string
	RequiredAnyArgs []string
}

type pmsReadPlanBinding struct {
	Argument string
	Sources  []pmsReadPlanSource
}

type pmsReadPlanSource struct {
	StepID string
	Fields []string
}

type pmsReadStepStatus string

const (
	pmsReadStepOK          pmsReadStepStatus = "ok"
	pmsReadStepPartial     pmsReadStepStatus = "partial"
	pmsReadStepEmpty       pmsReadStepStatus = "empty"
	pmsReadStepAmbiguous   pmsReadStepStatus = "ambiguous"
	pmsReadStepUnavailable pmsReadStepStatus = "unavailable"
	pmsReadStepUnsupported pmsReadStepStatus = "unsupported"
)

type pmsReadStepResult struct {
	StepID  string
	Status  pmsReadStepStatus
	Data    any
	Message string
	Args    map[string]string
}

type pmsReadPlanResult struct {
	Status      pmsReadStepStatus
	Steps       []pmsReadStepResult
	Confirmed   map[string]any
	Unconfirmed []string
}

var (
	pmsReadReserveIDFields = []string{"reserveOrderId", "receptOrderList[].reserveOrderId"}
	pmsReadReceptIDFields  = []string{"receptOrderId", "receptOrderList[].receptOrderId"}
	pmsReadStayStartFields = []string{"checkInBusinessDate", "checkInTime", "receptOrderList[].checkInBusinessDate", "receptOrderList[].checkInTime"}
	pmsReadStayEndFields   = []string{"checkOutBusinessDate", "checkOutTime", "receptOrderList[].checkOutBusinessDate", "receptOrderList[].checkOutTime"}
	pmsReadRoomFields      = []string{"homeName", "receptOrderList[].homeName"}
)

func buildPMSReadPlan(input pmsReadPlanInput) pmsReadPlan {
	input = normalizePMSReadPlanInput(input)
	scenario := input.Scenario
	if scenario == "" {
		scenario = pmsReadScenarioForSubIntent(input.SubIntent)
	}
	plan := pmsReadPlan{Scenario: scenario}

	switch scenario {
	case pmsReadScenarioOrder:
		appendPMSReadOrderSteps(&plan, input, false)
	case pmsReadScenarioRoomStatus:
		orderSteps := []string(nil)
		if input.RoomKeyword == "" {
			orderSteps = appendPMSReadOrderSteps(&plan, input, true)
		}
		appendPMSReadRoomStatusStep(&plan, input, orderSteps)
	case pmsReadScenarioDateInventory:
		orderSteps := []string(nil)
		if input.StartDate == "" || input.EndDate == "" {
			orderSteps = appendPMSReadOrderSteps(&plan, input, false)
		}
		appendPMSReadInventoryStep(&plan, input, orderSteps, pmsReadStayStartFields, pmsReadStayEndFields, true)
	case pmsReadScenarioRoomUpgrade:
		orderSteps := appendPMSReadOrderSteps(&plan, input, false)
		appendPMSReadInventoryStep(&plan, input, orderSteps, pmsReadStayStartFields, pmsReadStayEndFields, true)
		appendPMSReadMemberStep(&plan, input)
		appendPMSReadPriceStep(&plan, input, orderSteps, false)
	case pmsReadScenarioRoomChange:
		orderSteps := appendPMSReadOrderSteps(&plan, input, true)
		appendPMSReadInventoryStep(&plan, input, orderSteps, pmsReadStayStartFields, pmsReadStayEndFields, true)
		appendPMSReadRoomStatusStep(&plan, input, orderSteps)
		appendPMSReadPriceStep(&plan, input, orderSteps, false)
	case pmsReadScenarioPrice:
		orderSteps := appendPMSReadOrderSteps(&plan, input, false)
		appendPMSReadInventoryStep(&plan, input, orderSteps, pmsReadStayStartFields, pmsReadStayEndFields, true)
		appendPMSReadPriceStep(&plan, input, orderSteps, true)
	case pmsReadScenarioRenewal:
		orderSteps := appendPMSReadOrderSteps(&plan, input, true)
		appendPMSReadInventoryStep(&plan, input, orderSteps, pmsReadStayEndFields, nil, true)
		appendPMSReadRenewCandidateStep(&plan, input, orderSteps)
	case pmsReadScenarioLateCheckout:
		orderSteps := appendPMSReadOrderSteps(&plan, input, true)
		appendPMSReadRoomStatusStep(&plan, input, orderSteps)
		appendPMSReadInventoryStep(&plan, input, orderSteps, pmsReadStayEndFields, nil, true)
		appendPMSReadMemberStep(&plan, input)
	case pmsReadScenarioMemberInfo:
		appendPMSReadMemberInfoStep(&plan, input)
	case pmsReadScenarioMemberBenefit:
		appendPMSReadMemberStep(&plan, input)
	default:
		plan.Missing = append(plan.Missing, "supportedScenario")
	}
	plan.Missing = uniquePMSReadStrings(plan.Missing)
	return plan
}

func normalizePMSReadPlanInput(input pmsReadPlanInput) pmsReadPlanInput {
	input.SubIntent = strings.ToLower(strings.TrimSpace(input.SubIntent))
	input.Phone = strings.TrimSpace(input.Phone)
	input.CustomerNo = strings.TrimSpace(input.CustomerNo)
	input.ReserveOrderID = strings.TrimSpace(input.ReserveOrderID)
	input.ReceptOrderID = strings.TrimSpace(input.ReceptOrderID)
	input.RoomKeyword = strings.TrimSpace(input.RoomKeyword)
	input.TargetRoomTypeID = strings.TrimSpace(input.TargetRoomTypeID)
	input.TargetRoomTypeText = strings.TrimSpace(input.TargetRoomTypeText)
	input.StartDate = normalizePMSReadDate(input.StartDate)
	input.EndDate = normalizePMSReadDate(input.EndDate)
	return input
}

func pmsReadScenarioForSubIntent(subIntent string) pmsReadScenario {
	switch strings.ToLower(strings.TrimSpace(subIntent)) {
	case "order_query", "order_detail", "order_status", "check_in_status", "check_out_status":
		return pmsReadScenarioOrder
	case "room_status":
		return pmsReadScenarioRoomStatus
	case "room_inventory", "room_availability", "inventory", "room_availability_evaluation":
		return pmsReadScenarioDateInventory
	case "room_upgrade", "upgrade_room", "room_upgrade_eligibility", "upgrade_eligibility":
		return pmsReadScenarioRoomUpgrade
	case "room_change", "change_room", "room_change_eligibility", "room_assignment", "assign_room":
		return pmsReadScenarioRoomChange
	case "price_difference", "upgrade_price", "room_change_price", "order_price_dispute":
		return pmsReadScenarioPrice
	case "renew", "renewal":
		return pmsReadScenarioRenewal
	case "late_checkout", "late_check_out", "late_checkout_eligibility":
		return pmsReadScenarioLateCheckout
	case "member_info", "member_info_by_phone":
		return pmsReadScenarioMemberInfo
	case "member_benefits", "member_benefits_by_grade", "member_benefits_by_phone":
		return pmsReadScenarioMemberBenefit
	default:
		return ""
	}
}

func appendPMSReadOrderSteps(plan *pmsReadPlan, input pmsReadPlanInput, receptOnly bool) []string {
	stepIDs := make([]string, 0, 2)
	if input.ReceptOrderID != "" {
		plan.Steps = append(plan.Steps, pmsReadPlanStep{
			ID: "order.recept", Action: "recept_order_detail", Purpose: "读取当前接待单事实", Required: true,
			Args: map[string]string{"receptOrderId": input.ReceptOrderID}, RequiredArgs: []string{"receptOrderId"},
		})
		stepIDs = append(stepIDs, "order.recept")
	}
	if input.ReserveOrderID != "" {
		plan.Steps = append(plan.Steps, pmsReadPlanStep{
			ID: "order.reserve", Action: "reserve_order_detail", Purpose: "读取当前预订单事实", Required: !receptOnly,
			Args: map[string]string{"reserveOrderId": input.ReserveOrderID}, RequiredArgs: []string{"reserveOrderId"},
		})
		stepIDs = append(stepIDs, "order.reserve")
	}
	if len(stepIDs) > 0 {
		return stepIDs
	}

	locator := pmsReadLocatorArgs(input)
	if len(locator) == 0 {
		if receptOnly {
			plan.Missing = append(plan.Missing, "receptOrderLocator")
		} else {
			plan.Missing = append(plan.Missing, "customerLocator")
		}
		return nil
	}
	if !receptOnly {
		plan.Steps = append(plan.Steps, pmsReadPlanStep{
			ID: "order.reserve", Action: "reserve_order_by_phone", Purpose: "查询当前有效预订单", Required: true,
			Args: clonePMSReadArgs(locator), RequiredAnyArgs: []string{"phone", "customerNo"},
		})
		stepIDs = append(stepIDs, "order.reserve")
	}
	plan.Steps = append(plan.Steps, pmsReadPlanStep{
		ID: "order.recept", Action: "recept_order_by_phone", Purpose: "查询当前有效接待单", Required: true,
		Args: clonePMSReadArgs(locator), RequiredAnyArgs: []string{"phone", "customerNo"},
	})
	return append(stepIDs, "order.recept")
}

func appendPMSReadInventoryStep(plan *pmsReadPlan, input pmsReadPlanInput, orderSteps []string, startFields, endFields []string, required bool) {
	if input.StartDate != "" && input.EndDate != "" && !validPMSReadDateRange(input.StartDate, input.EndDate) {
		plan.Missing = append(plan.Missing, "validInventoryDateRange")
		return
	}
	step := pmsReadPlanStep{
		ID: "inventory.stay", Action: "inventory", Purpose: "查询明确日期区间的房型库存和价格", Required: required,
		Args: map[string]string{"metrics": "sold,sellable"}, RequiredArgs: []string{"beginTime", "endTime"},
	}
	if input.TargetRoomTypeID != "" {
		step.Args["roomTypeId"] = input.TargetRoomTypeID
	}
	if input.StartDate != "" {
		step.Args["beginTime"] = input.StartDate
	} else if len(orderSteps) > 0 && len(startFields) > 0 {
		step.Bindings = append(step.Bindings, pmsReadBinding("beginTime", orderSteps, startFields))
	} else {
		plan.Missing = append(plan.Missing, "inventoryStartDate")
	}
	if input.EndDate != "" {
		step.Args["endTime"] = input.EndDate
	} else if len(orderSteps) > 0 && len(endFields) > 0 {
		step.Bindings = append(step.Bindings, pmsReadBinding("endTime", orderSteps, endFields))
	} else {
		plan.Missing = append(plan.Missing, "inventoryEndDate")
	}
	if pmsReadStepCanResolveArgs(step) {
		plan.Steps = append(plan.Steps, step)
	}
}

func appendPMSReadMemberStep(plan *pmsReadPlan, input pmsReadPlanInput) {
	if input.Phone == "" {
		plan.Missing = append(plan.Missing, "memberPhone")
		return
	}
	plan.Steps = append(plan.Steps, pmsReadPlanStep{
		ID: "member.benefits", Action: "member_benefits_by_phone", Purpose: "查询当前会员等级及明确权益", Required: false,
		Args: map[string]string{"phone": input.Phone}, RequiredArgs: []string{"phone"},
	})
}

func appendPMSReadMemberInfoStep(plan *pmsReadPlan, input pmsReadPlanInput) {
	if input.Phone == "" {
		plan.Missing = append(plan.Missing, "memberPhone")
		return
	}
	plan.Steps = append(plan.Steps, pmsReadPlanStep{
		ID: "member.info", Action: "member_info_by_phone", Purpose: "查询当前会员等级和状态", Required: true,
		Args: map[string]string{"phone": input.Phone}, RequiredArgs: []string{"phone"},
	})
}

func appendPMSReadPriceStep(plan *pmsReadPlan, input pmsReadPlanInput, orderSteps []string, required bool) {
	if input.TargetRoomTypeID == "" {
		plan.Missing = append(plan.Missing, "targetRoomTypeId")
		return
	}
	if len(orderSteps) == 0 {
		plan.Missing = append(plan.Missing, "orderIdForPrice")
		return
	}
	step := pmsReadPlanStep{
		ID: "price.difference", Action: "price_difference", Purpose: "按订单逐日价格和目标房型逐日价格评估差价", Required: required,
		Args:         map[string]string{"roomTypeId": input.TargetRoomTypeID},
		RequiredArgs: []string{"roomTypeId"}, RequiredAnyArgs: []string{"reserveOrderId", "receptOrderId"},
	}
	if input.ReserveOrderID != "" {
		step.Args["reserveOrderId"] = input.ReserveOrderID
	}
	if input.ReceptOrderID != "" {
		step.Args["receptOrderId"] = input.ReceptOrderID
	}
	if input.StartDate != "" {
		step.Args["beginTime"] = input.StartDate
	}
	if input.EndDate != "" {
		step.Args["endTime"] = input.EndDate
	}
	if input.ReserveOrderID == "" {
		step.Bindings = append(step.Bindings, pmsReadBinding("reserveOrderId", filterPMSReadStepIDs(orderSteps, "order.reserve"), pmsReadReserveIDFields))
	}
	if input.ReceptOrderID == "" {
		step.Bindings = append(step.Bindings, pmsReadBinding("receptOrderId", filterPMSReadStepIDs(orderSteps, "order.recept"), pmsReadReceptIDFields))
	}
	plan.Steps = append(plan.Steps, step)
}

func appendPMSReadRoomStatusStep(plan *pmsReadPlan, input pmsReadPlanInput, orderSteps []string) {
	step := pmsReadPlanStep{
		ID: "room.status", Action: "room_status", Purpose: "查询当前房间的实时净脏和控制状态", Required: false,
		Args: map[string]string{}, RequiredArgs: []string{"keyword"},
	}
	if input.RoomKeyword != "" {
		step.Args["keyword"] = input.RoomKeyword
	} else if len(orderSteps) > 0 {
		step.Bindings = append(step.Bindings, pmsReadBinding("keyword", orderSteps, pmsReadRoomFields))
	} else {
		plan.Missing = append(plan.Missing, "roomLocator")
		return
	}
	if input.StartDate != "" {
		step.Args["startDate"] = input.StartDate
	}
	if input.EndDate != "" {
		step.Args["endDate"] = input.EndDate
	}
	plan.Steps = append(plan.Steps, step)
}

func appendPMSReadRenewCandidateStep(plan *pmsReadPlan, input pmsReadPlanInput, orderSteps []string) {
	step := pmsReadPlanStep{
		ID: "renew.candidates", Action: "renew_candidates", Purpose: "查询换单续住候选和允许的房间处理方式", Required: true,
		Args: map[string]string{}, RequiredArgs: []string{"currentReceptOrderId"},
	}
	if input.ReceptOrderID != "" {
		step.Args["currentReceptOrderId"] = input.ReceptOrderID
	} else {
		if len(orderSteps) == 0 {
			plan.Missing = append(plan.Missing, "currentReceptOrderId")
			return
		}
		step.Bindings = append(step.Bindings, pmsReadBinding("currentReceptOrderId", orderSteps, pmsReadReceptIDFields))
	}
	if input.Phone != "" {
		step.Args["reservePhone"] = input.Phone
	}
	plan.Steps = append(plan.Steps, step)
}

func pmsReadBinding(argument string, stepIDs, fields []string) pmsReadPlanBinding {
	binding := pmsReadPlanBinding{Argument: argument}
	for _, stepID := range stepIDs {
		if stepID == "" {
			continue
		}
		binding.Sources = append(binding.Sources, pmsReadPlanSource{
			StepID: stepID,
			Fields: append([]string(nil), fields...),
		})
	}
	return binding
}

func pmsReadStepCanResolveArgs(step pmsReadPlanStep) bool {
	for _, argument := range step.RequiredArgs {
		if strings.TrimSpace(step.Args[argument]) != "" || pmsReadStepHasBinding(step, argument) {
			continue
		}
		return false
	}
	return true
}

func pmsReadStepHasBinding(step pmsReadPlanStep, argument string) bool {
	for _, binding := range step.Bindings {
		if binding.Argument == argument && len(binding.Sources) > 0 {
			return true
		}
	}
	return false
}

func pmsReadLocatorArgs(input pmsReadPlanInput) map[string]string {
	args := map[string]string{}
	if input.Phone != "" {
		args["phone"] = input.Phone
	}
	if input.CustomerNo != "" {
		args["customerNo"] = input.CustomerNo
	}
	return args
}

func filterPMSReadStepIDs(stepIDs []string, want string) []string {
	ret := make([]string, 0, 1)
	for _, stepID := range stepIDs {
		if stepID == want {
			ret = append(ret, stepID)
		}
	}
	return ret
}

func clonePMSReadArgs(args map[string]string) map[string]string {
	ret := make(map[string]string, len(args))
	for key, value := range args {
		ret[key] = value
	}
	return ret
}

func normalizePMSReadDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 10 {
		value = value[:10]
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return ""
	}
	return value
}

func validPMSReadDateRange(startDate, endDate string) bool {
	start, startErr := time.Parse("2006-01-02", startDate)
	end, endErr := time.Parse("2006-01-02", endDate)
	return startErr == nil && endErr == nil && end.After(start)
}

func uniquePMSReadStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	ret := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	return ret
}

func aggregatePMSReadPlanResults(plan pmsReadPlan, results []pmsReadStepResult) (pmsReadPlanResult, error) {
	known := make(map[string]pmsReadPlanStep, len(plan.Steps))
	for _, step := range plan.Steps {
		if step.ID == "" {
			return pmsReadPlanResult{}, fmt.Errorf("PMS read plan contains an empty step id")
		}
		if _, exists := known[step.ID]; exists {
			return pmsReadPlanResult{}, fmt.Errorf("PMS read plan contains duplicate step %s", step.ID)
		}
		known[step.ID] = step
	}

	provided := make(map[string]pmsReadStepResult, len(results))
	for _, result := range results {
		if _, ok := known[result.StepID]; !ok {
			return pmsReadPlanResult{}, fmt.Errorf("PMS read result references unknown step %s", result.StepID)
		}
		if _, exists := provided[result.StepID]; exists {
			return pmsReadPlanResult{}, fmt.Errorf("PMS read result duplicates step %s", result.StepID)
		}
		if !validPMSReadStepStatus(result.Status) {
			return pmsReadPlanResult{}, fmt.Errorf("PMS read result has invalid status %s", result.Status)
		}
		provided[result.StepID] = result
	}

	merged := pmsReadPlanResult{Confirmed: make(map[string]any), Unconfirmed: append([]string(nil), plan.Missing...)}
	completed, failures, ambiguous, empty, okCount, unsupported := 0, 0, 0, 0, 0, 0
	for _, step := range plan.Steps {
		result, exists := provided[step.ID]
		if !exists {
			result = pmsReadStepResult{StepID: step.ID, Status: pmsReadStepUnavailable, Message: "查询步骤未执行"}
		}
		merged.Steps = append(merged.Steps, result)
		switch result.Status {
		case pmsReadStepOK:
			completed++
			okCount++
			merged.Confirmed[step.ID] = result.Data
		case pmsReadStepPartial:
			completed++
			failures++
			merged.Confirmed[step.ID] = result.Data
			merged.Unconfirmed = append(merged.Unconfirmed, step.ID)
		case pmsReadStepEmpty:
			completed++
			empty++
		case pmsReadStepAmbiguous:
			completed++
			ambiguous++
			merged.Unconfirmed = append(merged.Unconfirmed, step.ID)
		case pmsReadStepUnavailable:
			failures++
			merged.Unconfirmed = append(merged.Unconfirmed, step.ID)
		case pmsReadStepUnsupported:
			failures++
			unsupported++
			merged.Unconfirmed = append(merged.Unconfirmed, step.ID)
		}
	}
	merged.Unconfirmed = uniquePMSReadStrings(merged.Unconfirmed)

	switch {
	case failures > 0 || len(plan.Missing) > 0:
		if completed > 0 {
			merged.Status = pmsReadStepPartial
		} else if unsupported > 0 && unsupported == len(plan.Steps) && len(plan.Steps) > 0 {
			merged.Status = pmsReadStepUnsupported
		} else {
			merged.Status = pmsReadStepUnavailable
		}
	case ambiguous > 0:
		merged.Status = pmsReadStepAmbiguous
	case okCount > 0:
		merged.Status = pmsReadStepOK
	case empty > 0 && empty == len(plan.Steps):
		merged.Status = pmsReadStepEmpty
	case len(plan.Steps) == 0:
		merged.Status = pmsReadStepUnavailable
	default:
		merged.Status = pmsReadStepOK
	}
	return merged, nil
}

func validPMSReadStepStatus(status pmsReadStepStatus) bool {
	switch status {
	case pmsReadStepOK, pmsReadStepPartial, pmsReadStepEmpty, pmsReadStepAmbiguous, pmsReadStepUnavailable, pmsReadStepUnsupported:
		return true
	default:
		return false
	}
}
