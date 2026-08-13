package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

// validateReplyFactGrounding 拦截“知识未命中却下确定性结论”的编造。
// 对 OutputMode=clarification 的任务，Generate 只能追问或说明资料未写明，
// 不得断言有/没有、可以/不可以、提供/在/不在等事实；命中则触发一次协议修复。
func validateReplyFactGrounding(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	planByTask := make(map[string]contracts.ReplyPlanTaskV2, len(input.Plan.Tasks))
	for _, task := range input.Plan.Tasks {
		planByTask[task.TaskKey] = task
	}
	issues := make([]contracts.ValidationIssueV1, 0)
	for partIndex, part := range input.Output.Parts {
		content := strings.TrimSpace(part.Content)
		if content == "" {
			continue
		}
		for _, taskKey := range part.TaskKeys {
			task, ok := planByTask[taskKey]
			if !ok || task.OutputMode != "clarification" {
				continue
			}
			if assertsUngroundedFact(content) {
				issues = append(issues, validationIssue(
					"fact_ungrounded",
					"$.parts",
					"clarification task "+taskKey+" asserts an ungrounded fact instead of asking or stating the knowledge is absent",
				))
				break
			}
		}
		_ = partIndex
	}
	return issues
}

// assertsUngroundedFact 检测确定性断言模式。只拦截“动词/判断词 + 具体宾语”的强断言，
// 不误伤“资料里没写”“需要帮你问”等澄清句。
func assertsUngroundedFact(content string) bool {
	compact := compactReplyText(content)
	if compact == "" {
		return false
	}
	// 先排除明确表示“未写明/不知道/要确认”的澄清句，避免误伤安全表达。
	for _, safe := range []string{"没写", "未写明", "没有写", "资料里没有", "不清楚", "不确定", "要确认", "帮你问", "帮你查", "需要确认", "稍等确认"} {
		if strings.Contains(compact, safe) {
			return false
		}
	}
	assertionVerbs := []string{
		"有", "没有", "无", "可以", "不可以", "不能", "能", "是", "不是",
		"免费", "收费", "提供", "包含", "配有", "支持", "在", "不在",
	}
	count := 0
	for _, verb := range assertionVerbs {
		if strings.Contains(compact, verb) {
			count++
		}
	}
	// 两个及以上断言词才判编造，降低单字误伤（如“可以吗？”的疑问句）。
	return count >= 2
}
