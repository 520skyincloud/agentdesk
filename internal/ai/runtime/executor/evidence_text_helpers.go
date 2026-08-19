package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

func cleanRuntimeEvidenceAnswer(content string) string {
	content = strings.TrimSpace(strings.NewReplacer("\r", "\n", "\t", " ").Replace(content))
	if index := strings.LastIndex(content, "答案："); index >= 0 {
		content = strings.TrimSpace(content[index+len("答案："):])
	} else if index := strings.LastIndex(content, "答案:"); index >= 0 {
		content = strings.TrimSpace(content[index+len("答案:"):])
	}
	return strings.TrimLeft(content, "-#*• ")
}

func runtimeProcessFactMask(text string) uint8 {
	compact := compactRuntimeProtocolText(text)
	var mask uint8
	if containsAny(compact, []string{"小程序", "登记", "实名", "证件", "订单", "入住信息"}) {
		mask |= runtimeProcessFactRegistration
	}
	if containsAny(compact, []string{"刷脸", "开门", "门禁", "房卡", "密码"}) {
		mask |= runtimeProcessFactAccess
	}
	if containsAny(compact, []string{"入口", "大楼", "大厅", "电梯", "楼层", "停车场"}) {
		mask |= runtimeProcessFactRoute
	}
	return mask
}

const (
	runtimeProcessFactRegistration uint8 = 1 << iota
	runtimeProcessFactAccess
	runtimeProcessFactRoute
)

func runtimeTaskRequestsRoute(task contracts.ReplyPlanTaskV2) bool {
	subIntent := strings.ToLower(strings.TrimSpace(task.SubIntent))
	if containsAny(subIntent, []string{"entrance", "navigation", "route", "address", "location"}) {
		return true
	}
	return runtimeTextRequestsEntranceRoute(task.Objective)
}

func runtimeTaskNeedsProcessCoverage(task contracts.ReplyPlanTaskV2) bool {
	subIntent := strings.ToLower(strings.TrimSpace(task.SubIntent))
	return strings.Contains(subIntent, "process") || strings.Contains(subIntent, "steps") ||
		strings.Contains(subIntent, "guide") || isCheckinProcessSubIntent(subIntent) ||
		subIntent == "checkout" || subIntent == "check_out"
}
