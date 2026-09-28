package executor

import (
	"fmt"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func runtimePMSAsksOrderHistory(text string) bool {
	return containsAny(text, []string{"历史订单", "以前的订单", "之前的订单", "以前住", "上次住", "上次的订单", "住过", "入住记录", "过去的订单", "所有订单", "全部订单"})
}

func runtimePMSHistoryAnswer(data any) string {
	root, _ := data.(map[string]any)
	rows, _ := root["rows"].([]any)
	if len(rows) == 0 {
		return ""
	}
	lines := make([]string, 0, len(rows))
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
		parts := []string{}
		start := runtimePMSCustomerDateTime(firstRuntimePMSReadText(row, "checkInTime"))
		end := runtimePMSCustomerDateTime(firstRuntimePMSReadText(row, "checkOutTime"))
		if start != "" {
			parts = append(parts, start+"入住")
		}
		if end != "" {
			parts = append(parts, end+"离店")
		}
		if room := firstRuntimePMSReadText(row, "roomName"); room != "" {
			parts = append(parts, room)
		}
		if status := runtimePMSCustomerOrderStatus(row); status != "" {
			parts = append(parts, status)
		}
		if amount := runtimePMSCustomerAmount(firstRuntimePMSReadText(row, "payAmount")); amount != "" {
			parts = append(parts, "订单金额"+amount)
		}
		if len(parts) > 0 {
			lines = append(lines, strings.Join(parts, "，")+"。")
		}
		if len(lines) == 10 {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}
	prefix := fmt.Sprintf("查到这个手机号有%d笔订单：", len(rows))
	if len(rows) > len(lines) {
		prefix += fmt.Sprintf("先列出其中%d笔，您可以告诉我想查哪段入住日期。", len(lines))
	}
	return prefix + "\n" + strings.Join(lines, "\n")
}

func runtimePMSProgramAnswer(task callbacks.ReplyTaskPlanTraceData, data any) string {
	root, _ := data.(map[string]any)
	grades, _ := root["grades"].([]any)
	text := strings.TrimSpace(task.OriginalText)
	if text == "" {
		text = strings.TrimSpace(task.ResolvedText + task.Text)
	}
	var selected []map[string]any
	var all []map[string]any
	for _, value := range grades {
		grade, ok := value.(map[string]any)
		if !ok || firstRuntimePMSReadText(grade, "gradeAvailable") != "true" {
			continue
		}
		all = append(all, grade)
		name := firstRuntimePMSReadText(grade, "gradeName")
		if name != "" && strings.Contains(text, strings.TrimSuffix(name, "会员")) {
			selected = append(selected, grade)
		}
	}
	if len(selected) == 0 || containsAny(text, []string{"所有", "各个", "哪些等级", "各等级", "各项"}) {
		selected = all
	}
	var lines []string
	for _, grade := range selected {
		name := firstRuntimePMSReadText(grade, "gradeName")
		parts := []string{}
		if benefits := runtimePMSMemberBenefitTexts(grade); len(benefits) > 0 {
			parts = append(parts, strings.Join(benefits, "、"))
		}
		if validity := firstRuntimePMSReadText(grade, "validityText"); validity != "" {
			parts = append(parts, "有效期"+validity)
		}
		if rule := firstRuntimePMSReadText(grade, "upgradeRuleSummary"); rule != "" {
			parts = append(parts, "升级条件："+rule)
		}
		if rule := firstRuntimePMSReadText(grade, "keepGradeRuleSummary"); rule != "" {
			parts = append(parts, "保级条件："+rule)
		}
		lines = append(lines, name+"："+strings.Join(parts, "；")+"。")
	}
	return strings.Join(lines, "\n")
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
