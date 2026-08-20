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
		var supplyFactMask uint8
		for _, ref := range task.EvidenceRefs {
			item, ok := itemsByRef[ref]
			if !ok || strings.TrimSpace(item.Content) == "" || item.Answerability == "not_supporting" {
				continue
			}
			if item.SourceType == "store_fact" || runtimeProcessEvidenceIsRequired(task, item) || runtimeSupplyEvidenceIsRequired(task, item) {
				required = appendUniqueStrings(required, ref)
			} else {
				supporting = appendUniqueStrings(supporting, ref)
			}
			if runtimeTaskIsSupplyKnowledge(task) {
				supplyFactMask |= runtimeSupplyFactMask(task, item.Content)
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
		if labels := runtimeSupplyFactLabels(supplyFactMask); len(labels) > 0 {
			line += "；用品必答事实=" + strings.Join(labels, ",") + "（只按证据作答，位置和取用方式不得省略）"
		}
		lines = append(lines, line)
		count++
	}
	if count == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

const (
	runtimeSupplyFactAvailability uint8 = 1 << iota
	runtimeSupplyFactEquivalent
	runtimeSupplyFactLocation
	runtimeSupplyFactAccess
)

func runtimeTaskIsSupplyKnowledge(task contracts.ReplyPlanTaskV2) bool {
	subIntent := strings.ToLower(strings.TrimSpace(task.SubIntent))
	if subIntent == "supplies_self_help" || subIntent == "supplies" || strings.Contains(subIntent, "suppl") {
		return true
	}
	_, ok := detectKnowledgeTopicClasses(task.Objective)["supplies"]
	return ok
}

func runtimeSupplyEvidenceIsRequired(task contracts.ReplyPlanTaskV2, item contracts.EvidenceItemV1) bool {
	if !runtimeTaskIsSupplyKnowledge(task) || item.SourceType == "store_fact" {
		return false
	}
	return runtimeSupplyFactMask(task, item.Content) != 0
}

func runtimeSupplyFactMask(task contracts.ReplyPlanTaskV2, content string) uint8 {
	if !runtimeTaskIsSupplyKnowledge(task) || strings.TrimSpace(content) == "" {
		return 0
	}
	compact := compactRuntimeProtocolText(content)
	if compact == "" {
		return 0
	}
	var mask uint8
	if knowledgeEvidenceHasExplicitEntityMatch(task.Objective, content) || containsAny(compact, []string{"提供", "配有", "配备", "备有", "可用", "有", "没有", "暂无"}) {
		mask |= runtimeSupplyFactAvailability
	}
	if runtimeSupplyEvidenceMentionsEquivalent(task.Objective, content) {
		mask |= runtimeSupplyFactEquivalent
	}
	if containsAny(compact, []string{"位于", "位置", "在哪里", "在哪", "楼", "层", "旁", "百宝箱", "用品柜", "自取柜", "房间内", "房间里", "客房内"}) {
		mask |= runtimeSupplyFactLocation
	}
	if containsAny(compact, []string{"自取", "取用", "领取", "拿取", "借用", "使用", "打开", "扫码", "按需取"}) {
		mask |= runtimeSupplyFactAccess
	}
	return mask
}

func runtimeSupplyEvidenceMentionsEquivalent(query, content string) bool {
	query = compactRuntimeProtocolText(query)
	content = compactRuntimeProtocolText(content)
	if query == "" || content == "" {
		return false
	}
	for _, aliases := range knowledgeEntityAliasGroups() {
		queryAlias := ""
		for _, alias := range aliases {
			if strings.Contains(query, alias) {
				queryAlias = alias
				break
			}
		}
		if queryAlias == "" {
			continue
		}
		for _, alias := range aliases {
			if alias != queryAlias && strings.Contains(content, alias) {
				return true
			}
		}
	}
	return containsAny(content, []string{"替代", "代替", "等同", "可用作"})
}

func runtimeSupplyFactLabels(mask uint8) []string {
	labels := make([]string, 0, 4)
	if mask&runtimeSupplyFactAvailability != 0 {
		labels = append(labels, "是否提供")
	}
	if mask&runtimeSupplyFactEquivalent != 0 {
		labels = append(labels, "等价替代")
	}
	if mask&runtimeSupplyFactLocation != 0 {
		labels = append(labels, "具体位置")
	}
	if mask&runtimeSupplyFactAccess != 0 {
		labels = append(labels, "取用方式")
	}
	return labels
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
