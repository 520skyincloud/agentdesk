package executor

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func runtimePMSAsksOrderHistory(text string) bool {
	return containsAny(text, []string{"历史订单", "以前的订单", "之前的订单", "以前住", "上次住", "上次的订单", "住过", "入住记录", "过去的订单", "所有订单", "全部订单"})
}

func runtimePMSHistoryRows(data any) []map[string]any {
	root, _ := data.(map[string]any)
	rows, _ := root["rows"].([]any)
	ret := make([]map[string]any, 0, len(rows))
	for _, value := range rows {
		if row, ok := value.(map[string]any); ok {
			ret = append(ret, row)
		}
	}
	return ret
}

func runtimePMSHistoryFacts(task callbacks.ReplyTaskPlanTraceData, data any) []runtimePMSReadTaskFact {
	rows := runtimePMSHistoryRows(data)
	if len(rows) == 0 {
		return nil
	}
	reserveID, receptID, _ := runtimePMSOrderLocators(runtimeIntentEntityValue(task.Entities, runtimeIntentEntityOrderLocator))
	explicitSelection := false
	if reserveID != "" || receptID != "" {
		selected := make([]map[string]any, 0, 1)
		for _, row := range rows {
			if (receptID != "" && firstRuntimePMSReadText(row, "receptOrderId") == receptID) ||
				(receptID == "" && reserveID != "" && firstRuntimePMSReadText(row, "reserveOrderId") == reserveID) {
				selected = append(selected, row)
			}
		}
		if len(selected) > 0 {
			rows = selected
			explicitSelection = true
		}
	}
	if !explicitSelection && runtimeIntentEntityValue(task.Entities, runtimeIntentEntityHistoryChoice) == "latest" {
		if latest, ok := runtimePMSLatestHistoryRow(rows, time.Now()); ok {
			rows = []map[string]any{latest}
		} else if len(rows) == 1 {
			return []runtimePMSReadTaskFact{{
				Aspect:    "pms_order_history_selection",
				Statement: "已查到订单记录，但当前日期信息不能确认它是最近一次已发生的住宿；需要客户补充住宿日期，不能把未来预订或日期不明的订单当作上次住宿。",
			}}
		}
	}
	if task.SubjectScope == runtimeSubjectCurrentOrder || task.SubjectScope == runtimeSubjectCurrentStay {
		return []runtimePMSReadTaskFact{{
			Aspect:    "pms_order_history_scope",
			Statement: "当前有效订单未匹配；手机号搜索另查到住宿记录，但不能以历史住宿替代当前订单。需要确认客户要查询的是哪次住宿。",
		}}
	}
	if len(rows) > 1 {
		options := make([]string, 0, min(len(rows), 3))
		for _, row := range rows[:min(len(rows), 3)] {
			fields := []string{}
			if date := firstRuntimePMSReadText(row, "checkInTime", "checkInBusinessDate"); date != "" {
				fields = append(fields, "入住日期"+runtimePMSCustomerDateTime(date))
			}
			if room := firstRuntimePMSReadText(row, "roomName"); room != "" {
				fields = append(fields, "房型"+room)
			}
			if len(fields) > 0 {
				options = append(options, strings.Join(fields, "、"))
			}
		}
		return []runtimePMSReadTaskFact{{
			Aspect: "pms_order_history_selection",
			Statement: fmt.Sprintf("匹配到%d笔住宿记录，当前尚未唯一选择；可用于区分的记录为%s。需要客户选择住宿日期，不能任选一笔回答金额或离店时间。",
				len(rows), strings.Join(options, "；")),
		}}
	}
	facts, requested := runtimePMSFocusedOrderFacts(task, rows[0])
	if !requested {
		facts = runtimePMSOrderOverviewFacts(rows[0], "历史住宿")
	}
	for index := range facts {
		facts[index].Statement = strings.ReplaceAll(facts[index].Statement, "当前订单", "所选历史住宿")
	}
	return facts
}

func runtimePMSLatestHistoryRow(rows []map[string]any, now time.Time) (map[string]any, bool) {
	var latest map[string]any
	var latestTime time.Time
	tied := false
	for _, row := range rows {
		checkIn, ok := runtimePMSHotelTime(firstRuntimePMSReadText(row, "checkInTime", "checkInBusinessDate"))
		if !ok {
			return nil, false
		}
		if checkIn.After(now) {
			continue
		}
		switch {
		case latest == nil || checkIn.After(latestTime):
			latest, latestTime, tied = row, checkIn, false
		case checkIn.Equal(latestTime):
			tied = true
		}
	}
	return latest, latest != nil && !tied
}

func runtimePMSHotelTime(value string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", time.RFC3339, "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, strings.TrimSpace(value), runtimeHotelLocation()); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func runtimePMSProgramFacts(task callbacks.ReplyTaskPlanTraceData, data any) []runtimePMSReadTaskFact {
	root, _ := data.(map[string]any)
	grades, _ := root["grades"].([]any)
	var all []map[string]any
	for _, value := range grades {
		grade, ok := value.(map[string]any)
		if !ok || firstRuntimePMSReadText(grade, "gradeAvailable") != "true" {
			continue
		}
		all = append(all, grade)
	}
	selected := runtimePMSGradesMentionedByTask(task, all)
	if len(selected) == 0 {
		selected = all
	}
	names := make([]string, 0, len(selected))
	for _, grade := range selected {
		if name := firstRuntimePMSReadText(grade, "gradeName"); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	facts := []runtimePMSReadTaskFact{{
		Aspect: "pms_member_level_names", Statement: "公开会员等级：" + strings.Join(names, "、") + "。",
		CriticalValues: append([]string(nil), names...),
	}}
	if len(task.RequestedAspects) == 0 || runtimePMSOnlyRequestedAspect(task, "member_level_names") {
		return facts
	}
	for _, grade := range selected {
		facts = append(facts, runtimePMSGradeFacts(task, grade, firstRuntimePMSReadText(grade, "gradeName"))...)
	}
	return facts
}

func runtimePMSGradesMentionedByTask(task callbacks.ReplyTaskPlanTraceData, grades []map[string]any) []map[string]any {
	for _, text := range []string{task.OriginalText, task.ResolvedText, task.Text} {
		selected := make([]map[string]any, 0, 1)
		for _, grade := range grades {
			name := strings.TrimSuffix(firstRuntimePMSReadText(grade, "gradeName"), "会员")
			if name != "" && strings.Contains(text, name) {
				selected = append(selected, grade)
			}
		}
		if len(selected) > 0 {
			return selected
		}
	}
	return nil
}

func runtimePMSHasRequestedAspect(task callbacks.ReplyTaskPlanTraceData, aspects ...string) bool {
	for _, requested := range task.RequestedAspects {
		for _, aspect := range aspects {
			if strings.TrimSpace(requested) == aspect {
				return true
			}
		}
	}
	return false
}

func runtimePMSOnlyRequestedAspect(task callbacks.ReplyTaskPlanTraceData, aspect string) bool {
	return len(task.RequestedAspects) == 1 && strings.TrimSpace(task.RequestedAspects[0]) == aspect
}

func runtimePMSGradeFacts(task callbacks.ReplyTaskPlanTraceData, grade map[string]any, name string) []runtimePMSReadTaskFact {
	facts := make([]runtimePMSReadTaskFact, 0, 4)
	add := func(aspect, label, value string) {
		if value == "" {
			return
		}
		facts = append(facts, runtimePMSReadTaskFact{
			Aspect: "pms_" + aspect, Statement: name + "的" + label + "：" + value + "。",
		})
	}
	if runtimePMSHasRequestedAspect(task, "member_upgrade_conditions") {
		add("member_upgrade_conditions", "等级升级条件", firstRuntimePMSReadText(grade, "upgradeRuleSummary"))
	}
	if runtimePMSHasRequestedAspect(task, "member_retention_conditions") {
		add("member_retention_conditions", "保级条件", firstRuntimePMSReadText(grade, "keepGradeRuleSummary"))
	}
	if runtimePMSHasRequestedAspect(task, "member_validity") {
		add("member_validity", "等级有效期", firstRuntimePMSReadText(grade, "validityText"))
	}
	if runtimePMSHasRequestedAspect(task, "member_benefits", "checkout_time", "price", "quantity", "policy", "compound_information") {
		benefits, _ := grade["benefits"].([]any)
		for _, value := range benefits {
			benefit, ok := value.(map[string]any)
			if !ok || !runtimePMSRequestedBenefit(task, benefit) {
				continue
			}
			label := firstRuntimePMSReadText(benefit, "benefitName")
			if label == "" {
				label = "权益"
			}
			parts := []string{}
			text := firstRuntimePMSReadText(benefit, "contentText")
			if text == "" {
				text = firstRuntimePMSReadText(benefit, "label")
				if text == label {
					text = ""
				}
			}
			if text == "" {
				text = firstRuntimePMSReadText(benefit, "contentValue")
			}
			if text != "" {
				parts = append(parts, text)
			}
			if description := firstRuntimePMSReadText(benefit, "benefitDescription"); description != "" {
				parts = appendIfMissing(parts, description)
			}
			aspect := "member_benefit"
			if runtimePMSBenefitRequestedAspect(benefit) == "checkout_time" {
				aspect = "member_checkout_time"
			}
			previousFactCount := len(facts)
			if len(parts) == 0 {
				add(aspect, "权益", label)
			} else {
				add(aspect, label, strings.Join(parts, "；"))
			}
			if len(facts) > previousFactCount {
				if runtimePMSBenefitRequestedAspect(benefit) == "price" {
					facts[len(facts)-1].Statement += "这是会员价格规则，尚未核实本次订单、渠道和目标房型的适用条件；没有已确认的计算结果，不得自行套用挂牌价生成折后价或补退金额。"
				}
				for _, token := range knowledgeEvidenceIndividualTimePattern.FindAllString(strings.Join(parts, "；"), -1) {
					facts[len(facts)-1].CriticalValues = appendIfMissing(facts[len(facts)-1].CriticalValues, token)
				}
			}
		}
	}
	return facts
}

func runtimePMSRequestedBenefit(task callbacks.ReplyTaskPlanTraceData, benefit map[string]any) bool {
	if runtimePMSRoomDecisionTask(task) && !runtimeIntentScopeIsMembership(task.SubjectScope) {
		aspect := runtimePMSBenefitRequestedAspect(benefit)
		if aspect == "price" {
			return runtimePMSHasRequestedAspect(task, "member_benefits", "price", "price_difference", "policy", "compound_information")
		}
		if aspect != "" {
			return runtimePMSHasRequestedAspect(task, aspect)
		}
		// Unknown benefit types are retained as rules, never interpreted as
		// granted eligibility or a price calculation.
		return runtimePMSHasRequestedAspect(task, "member_benefits")
	}
	if runtimePMSHasRequestedAspect(task, "member_benefits", "policy", "compound_information") {
		return true
	}
	aspect := runtimePMSBenefitRequestedAspect(benefit)
	return aspect != "" && runtimePMSHasRequestedAspect(task, aspect)
}

func runtimePMSRoomDecisionTask(task callbacks.ReplyTaskPlanTraceData) bool {
	switch pmsReadScenarioForSubIntent(task.SubIntent) {
	case pmsReadScenarioRoomChange, pmsReadScenarioRoomUpgrade, pmsReadScenarioPrice:
		return true
	default:
		return false
	}
}

func runtimePMSBenefitRequestedAspect(benefit map[string]any) string {
	// label is display content (for example "延迟至15:00"), not a benefit type.
	switch firstRuntimePMSReadText(benefit, "benefitType") {
	case "LATE_CHECKOUT":
		return "checkout_time"
	case "MEMBER_PRICE":
		return "price"
	case "FREE_BREAKFAST":
		return "quantity"
	case "":
		name := firstRuntimePMSReadText(benefit, "benefitName")
		switch {
		case strings.Contains(name, "退房") || strings.Contains(name, "延退"):
			return "checkout_time"
		case strings.Contains(name, "会员价") || strings.Contains(name, "折扣"):
			return "price"
		case strings.Contains(name, "早餐") || strings.Contains(name, "份数"):
			return "quantity"
		}
	}
	return ""
}

func runtimePMSPersonalMemberFacts(task callbacks.ReplyTaskPlanTraceData, data any) []runtimePMSReadTaskFact {
	root, _ := data.(map[string]any)
	member, _ := root["member"].(map[string]any)
	grade, _ := root["grade"].(map[string]any)
	if member == nil {
		member = root
	}
	name := firstRuntimePMSReadText(member, "gradeName")
	if name == "" {
		return nil
	}
	facts := []runtimePMSReadTaskFact{{
		Aspect: "pms_member_identity", Statement: "本次查询的客户会员等级为" + name + "。",
		CriticalValues: []string{runtimePMSMemberTierCriticalName(name)},
	}}
	if status := firstRuntimePMSReadText(member, "statusName"); status != "" {
		facts = append(facts, runtimePMSReadTaskFact{Aspect: "pms_member_status", Statement: "会员状态：" + status + "。"})
	}
	if available := firstRuntimePMSReadText(member, "gradeAvailable"); available == "true" || available == "false" {
		label := "当前会员等级有效"
		if available == "false" {
			label = "当前会员等级不可用，不能承诺使用对应权益"
		}
		facts = append(facts, runtimePMSReadTaskFact{Aspect: "pms_member_status", Statement: label + "。"})
	}
	if runtimePMSHasRequestedAspect(task, "member_validity") {
		for _, field := range []struct{ key, label string }{{"validStartTime", "会员有效开始时间"}, {"validEndTime", "会员有效结束时间"}} {
			if value := firstRuntimePMSReadText(member, field.key); value != "" {
				value = runtimePMSCustomerDateTime(value)
				facts = append(facts, runtimePMSReadTaskFact{
					Aspect: "pms_member_validity", Statement: field.label + "：" + value + "。", CriticalValues: []string{value},
				})
			}
		}
	}
	return append(facts, runtimePMSGradeFacts(task, grade, name)...)
}

func runtimePMSMemberTierCriticalName(name string) string {
	// The category suffix is optional in ordinary speech; the actual tier
	// name remains required, so "银卡" cannot be rewritten as "钻石".
	if short := strings.TrimSpace(strings.TrimSuffix(name, "会员")); short != "" {
		return short
	}
	return name
}

func runtimePMSRequestedProjectionMissing(task callbacks.ReplyTaskPlanTraceData) []string {
	hasFact := func(aspects ...string) bool {
		for _, fact := range task.SupportedFacts {
			for _, aspect := range aspects {
				if fact.Aspect == aspect {
					return true
				}
			}
		}
		return false
	}
	if task.PMSOutcome == nil || (task.PMSOutcome.Status != string(pmsReadStepOK) && task.PMSOutcome.Status != string(pmsReadStepPartial)) {
		return nil
	}
	missing := []string{}
	for _, requested := range task.RequestedAspects {
		aspect, label := "", ""
		switch requested {
		case "checkout_time":
			aspect, label = "pms_order_checkout_time", "订单离店时间"
			if runtimeIntentScopeIsMembership(task.SubjectScope) {
				aspect, label = "pms_member_checkout_time", "会员延迟退房权益"
			}
		case "checkin_time":
			aspect, label = "pms_order_checkin_time", "订单入住时间"
		case "order_amount":
			aspect, label = "pms_order_amount", "所选订单金额"
		case "member_upgrade_conditions":
			aspect, label = "pms_member_upgrade_conditions", "会员等级升级条件"
		case "member_retention_conditions":
			aspect, label = "pms_member_retention_conditions", "会员保级条件"
		default:
			continue
		}
		if !hasFact(aspect) {
			missing = append(missing, "本次查询未返回可确认的"+label+"；保留其他已知事实，不由其他订单或权益推断。")
		}
	}
	return missing
}

func runtimePMSPriceBoardFacts(plan pmsReadPlan, step pmsReadStepResult) []runtimePMSReadTaskFact {
	rows, _ := step.Data.([]any)
	dates, err := runtimePMSStayDates(step.Args["beginTime"], step.Args["endTime"])
	if err != nil {
		return nil
	}
	targetID := firstNonEmptyReplyTaskText(runtimePMSPlanTargetRoomTypeID(plan), step.Args["roomTypeId"])
	facts := []runtimePMSReadTaskFact{}
	seenRooms := 0
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
		id := firstRuntimePMSReadText(row, "roomId", "productId", "roomTypeId")
		if targetID != "" && id != targetID {
			continue
		}
		name := firstRuntimePMSReadText(row, "roomTypeName", "productName")
		if name == "" {
			continue
		}
		bookings, _ := row["bookings"].(map[string]any)
		for _, date := range dates {
			day, _ := bookings[date].(map[string]any)
			price := firstRuntimePMSReadText(day, "price")
			if price == "" {
				continue
			}
			customerDate, amount := runtimePMSCustomerDateTime(date), runtimePMSCustomerAmount(price)
			facts = append(facts, runtimePMSReadTaskFact{
				Aspect:         "pms_price_board",
				Statement:      name + "在" + customerDate + "晚的挂牌售价为" + amount + "；这不是原订单已付金额，也不是完成会员或渠道结算后的最终补退金额。",
				CriticalValues: []string{customerDate, amount},
			})
		}
		seenRooms++
		if targetID == "" && seenRooms >= 6 {
			break
		}
	}
	return facts
}

func runtimePMSPlanTargetRoomTypeID(plan pmsReadPlan) string {
	for _, stepID := range []string{"price.difference", "inventory.stay", "price.board"} {
		for _, step := range plan.Steps {
			if step.ID == stepID && step.Args["roomTypeId"] != "" {
				return step.Args["roomTypeId"]
			}
		}
	}
	return ""
}

func runtimePMSInventoryFacts(plan pmsReadPlan, step pmsReadStepResult) []runtimePMSReadTaskFact {
	rows, _ := step.Data.([]any)
	dates, err := runtimePMSStayDates(step.Args["beginTime"], step.Args["endTime"])
	if err != nil {
		return nil
	}
	targetID := firstNonEmptyReplyTaskText(runtimePMSPlanTargetRoomTypeID(plan), step.Args["roomTypeId"])
	start := runtimePMSCustomerDateTime(step.Args["beginTime"])
	end := runtimePMSCustomerDateTime(step.Args["endTime"])
	facts := []runtimePMSReadTaskFact{}
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok || !runtimePMSInventoryRoomTypeRow(row) {
			continue
		}
		if targetID != "" && firstRuntimePMSReadText(row, "roomTypeId", "productId", "roomId") != targetID {
			continue
		}
		name := firstRuntimePMSReadText(row, "roomTypeName", "productName", "roomName")
		if name == "" {
			continue
		}
		available := runtimePMSInventoryAvailability(row, dates)
		bookings, _ := row["bookings"].(map[string]any)
		complete := true
		for _, date := range dates {
			day, _ := bookings[date].(map[string]any)
			if firstRuntimePMSReadText(day, "available") == "" {
				complete = false
				break
			}
		}
		statement := name + "在" + start + "入住至" + end + "离店区间"
		critical := []string{start, end}
		if !complete || available == "" {
			statement += "仅返回部分库存，不能确认全程可售。"
		} else {
			statement += "的各晚最低可售库存为" + available + "间。"
		}
		statement += "这是房型库存，不代表同一房号全程可用，也没有锁房或办理变更。"
		facts = append(facts, runtimePMSReadTaskFact{
			Aspect: "pms_room_inventory", Statement: statement, CriticalValues: critical,
		})
		if targetID == "" && len(facts) >= runtimePMSRoomTypeFactLimit(plan) {
			break
		}
	}
	return facts
}

func runtimePMSStayRoomFacts(plan pmsReadPlan, step pmsReadStepResult) []runtimePMSReadTaskFact {
	root, _ := step.Data.(map[string]any)
	if root == nil {
		return nil
	}
	start := runtimePMSCustomerDateTime(firstRuntimePMSReadText(root, "startDate"))
	end := runtimePMSCustomerDateTime(firstRuntimePMSReadText(root, "endDate"))
	scope := start + "至" + end
	if firstRuntimePMSReadText(root, "coverageComplete") != "true" {
		return []runtimePMSReadTaskFact{{
			Aspect:    "pms_stay_room_availability",
			Statement: scope + "的具体房号占用资料未完整覆盖；" + firstRuntimePMSReadText(root, "reason") + "。不能将当前空房当作完整入住区间可分配。",
		}}
	}
	targetID := runtimePMSPlanTargetRoomTypeID(plan)
	candidates, _ := root["candidates"].([]any)
	facts := []runtimePMSReadTaskFact{}
	for _, value := range candidates {
		row, ok := value.(map[string]any)
		if !ok || (targetID != "" && firstRuntimePMSReadText(row, "roomTypeId") != targetID) {
			continue
		}
		home := firstRuntimePMSReadText(row, "homeName")
		if home == "" {
			continue
		}
		name := firstRuntimePMSReadText(row, "roomTypeName")
		statement := name + "的" + home + "房在" + scope + "区间没有查到订单冲突，且未锁房、未维修。"
		if floor := firstRuntimePMSReadText(row, "floorName"); floor != "" {
			statement += "楼层为" + floor + "。"
		}
		if firstRuntimePMSReadText(row, "readyNow") == "true" {
			statement += "当前为空净房。"
		} else {
			statement += "当前是否已清洁可入住尚未确认。"
		}
		statement += "本次仅查询，并未分配或锁定该房间。"
		facts = append(facts, runtimePMSReadTaskFact{
			Aspect: "pms_stay_room_availability", Statement: statement,
			CriticalValues: []string{start, end},
		})
		if len(facts) == runtimePMSConcreteRoomFactLimit(plan) {
			break
		}
	}
	if len(facts) == 0 {
		return []runtimePMSReadTaskFact{{
			Aspect:         "pms_stay_room_availability",
			Statement:      "在" + scope + "区间没有查到满足目标房型且无占用冲突的具体房间；这不等于其他房型均不可用。",
			CriticalValues: []string{start, end},
		}}
	}
	return facts
}

func runtimePMSRoomTypeFactLimit(plan pmsReadPlan) int {
	if strings.TrimSpace(plan.ReplyStrategy) == "recommend_one_supported_option" {
		return 2
	}
	return 6
}

func runtimePMSConcreteRoomFactLimit(plan pmsReadPlan) int {
	if strings.TrimSpace(plan.ReplyStrategy) == "recommend_one_supported_option" {
		return 2
	}
	return 3
}

func runtimePMSBoardPriceFact(step pmsReadStepResult) string {
	if prices := runtimePMSBoardPrices(step); prices != "" {
		return "所询日期的房价看板：" + prices + "。这些是逐日看板售价，不是原订单金额，也不是已结算的补退款金额；会员或渠道最终结算价格未在此接口计算。"
	}
	return ""
}

func runtimePMSCustomerBoardPrice(result pmsReadPlanResult) string {
	for _, step := range result.Steps {
		if step.StepID == "price.board" && step.Status == pmsReadStepOK {
			if prices := runtimePMSBoardPrices(step); prices != "" {
				return "查到所询日期的房价：" + prices + "。这是当前挂牌售价，最终优惠和结算金额需以订单为准。"
			}
		}
	}
	return ""
}

func runtimePMSBoardPrices(step pmsReadStepResult) string {
	rows, _ := step.Data.([]any)
	dates, err := runtimePMSStayDates(step.Args["beginTime"], step.Args["endTime"])
	if err != nil {
		return ""
	}
	var lines []string
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
		roomID := firstRuntimePMSReadText(row, "roomId", "productId", "roomTypeId")
		if filter := step.Args["roomTypeId"]; filter != "" && roomID != filter {
			continue
		}
		name := firstRuntimePMSReadText(row, "roomTypeName", "productName")
		bookings, _ := row["bookings"].(map[string]any)
		var daily []string
		for _, date := range dates {
			day, _ := bookings[date].(map[string]any)
			if price := firstRuntimePMSReadText(day, "price"); price != "" {
				daily = append(daily, date+"售价"+price+"元")
			}
		}
		if name != "" && len(daily) > 0 {
			lines = append(lines, name+"："+strings.Join(daily, "、"))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "；")
}
