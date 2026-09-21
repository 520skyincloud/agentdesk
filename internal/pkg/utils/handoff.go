package utils

import "strings"

// IsExplicitHumanHandoffRequest reports whether the current customer text
// explicitly asks for a human agent. Dissatisfaction, a service request, or
// uncertainty alone must not authorize a handoff.
func IsExplicitHumanHandoffRequest(text string) bool {
	if IsRuntimeCustomerBurstEnvelope(text) {
		text = RuntimeCustomerBurstDisplayText(text)
	}
	normalized := strings.Join(strings.Fields(strings.TrimSpace(text)), "")
	if normalized == "" || strings.Contains(normalized, "人工智能") {
		return false
	}
	for _, phrase := range []string{
		"转人工",
		"转接人工",
		"转真人",
		"找人工",
		"找真人",
		"人工客服",
		"真人客服",
		"找客服",
		"联系人工",
		"联系同事",
		"找同事",
		"接人工",
		"接同事",
		"人工处理",
		"人工介入",
		"前台同事",
	} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return normalized == "人工" || normalized == "真人" || normalized == "客服"
}
