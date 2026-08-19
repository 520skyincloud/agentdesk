package executor

import (
	"fmt"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

// buildRuntimeAnswerBriefInstruction tells Generate which evidence is mandatory
// without prescribing customer-facing wording. The model still writes the
// answer; the server only separates authoritative facts from optional context.
func buildRuntimeAnswerBriefInstruction(plan contracts.ReplyPlanV2, evidence contracts.EvidenceBundleV1) string {
	itemsByRef := make(map[string]contracts.EvidenceItemV1, len(evidence.Items))
	for _, item := range evidence.Items {
		itemsByRef[item.Ref] = item
	}
	lines := []string{
		"【当前答案提纲】模型负责自然组织语言，服务器只规定事实覆盖范围。",
		"必答引用中的事实必须全部覆盖；补充引用只在与当前问题直接相关时使用。不得输出知识库的“问题：”“答案：”标签。",
	}
	count := 0
	for _, task := range plan.Tasks {
		if task.OutputMode != "text" && task.OutputMode != "text_and_resource" && task.OutputMode != "clarification" {
			continue
		}
		required := make([]string, 0, len(task.EvidenceRefs))
		supporting := make([]string, 0, len(task.EvidenceRefs))
		for _, ref := range task.EvidenceRefs {
			item, ok := itemsByRef[ref]
			if !ok || strings.TrimSpace(item.Content) == "" || item.Answerability == "not_supporting" {
				continue
			}
			if item.SourceType == "store_fact" || runtimeProcessEvidenceIsRequired(task, item) {
				required = appendUniqueStrings(required, ref)
			} else {
				supporting = appendUniqueStrings(supporting, ref)
			}
		}
		if len(required) == 0 && len(supporting) == 0 {
			continue
		}
		line := fmt.Sprintf("- taskKey=%s", task.TaskKey)
		if len(required) > 0 {
			line += "；必答=" + strings.Join(required, ",")
		}
		if len(supporting) > 0 {
			line += "；补充=" + strings.Join(supporting, ",")
		}
		if runtimeTaskNeedsProcessCoverage(task) {
			line += "；流程类答案要按先后顺序说完整，不能只摘第一条证据"
		}
		lines = append(lines, line)
		count++
	}
	if count == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func runtimeProcessEvidenceIsRequired(task contracts.ReplyPlanTaskV2, item contracts.EvidenceItemV1) bool {
	if !runtimeTaskNeedsProcessCoverage(task) {
		return false
	}
	mask := runtimeProcessFactMask(item.Content)
	if mask == 0 {
		return false
	}
	if isCheckinProcessSubIntent(task.SubIntent) && mask == runtimeProcessFactRoute && !runtimeTaskRequestsRoute(task) {
		return false
	}
	return true
}
