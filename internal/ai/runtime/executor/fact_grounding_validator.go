package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

// validateReplyFactGrounding 拦截“知识未命中却下确定性结论”的编造。
// 知识边界任务只能说明资料未写明；只有真正含糊的表达才使用 clarification 追问。
// 不得下确定性结论。领域硬约束（房态/会员）由 validateReplyUnsupportedDomain 独立处理。
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
			if !ok {
				continue
			}
			if (task.OutputMode == "clarification" || replyTaskHasKnowledgeBoundary(task)) && assertsUngroundedFact(content) {
				issues = append(issues, validationIssue(
					"fact_ungrounded",
					"$.parts",
					"knowledge-boundary task "+taskKey+" asserts an ungrounded fact instead of stating the knowledge is absent",
				))
				break
			}
		}
		_ = partIndex
	}
	issues = append(issues, validateReplyRoomStockClaims(input)...)
	return issues
}

// validateReplyRoomStockClaims prevents a self-service or substitute item from
// being promoted into a room-standard claim. A room placement claim is allowed
// only when task-bound evidence explicitly states that the same item is in-room.
func validateReplyRoomStockClaims(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	planByTask := make(map[string]contracts.ReplyPlanTaskV2, len(input.Plan.Tasks))
	for _, task := range input.Plan.Tasks {
		planByTask[task.TaskKey] = task
	}
	issues := make([]contracts.ValidationIssueV1, 0)
	for _, part := range input.Output.Parts {
		content := strings.TrimSpace(part.Content)
		if !assertsRoomStockFact(content) {
			continue
		}
		for _, taskKey := range part.TaskKeys {
			task, ok := planByTask[taskKey]
			if !ok || !runtimeTaskIsSupplyKnowledge(task) {
				continue
			}
			if roomStockClaimSupported(task, content, input.Evidence) {
				continue
			}
			issues = append(issues, validationIssue(
				"room_stock_ungrounded",
				"$.parts",
				"reply promotes self-service or substitute supply evidence into an unsupported in-room stock claim",
			))
			break
		}
	}
	return issues
}

func assertsRoomStockFact(content string) bool {
	compact := compactReplyText(content)
	if compact == "" || !containsAny(compact, []string{"房间里", "房间内", "房内", "客房内", "客房里", "每间房", "房间标配", "客房标配"}) {
		return false
	}
	_, supplies := detectKnowledgeTopicClasses(content)["supplies"]
	return supplies && containsAny(compact, []string{"有", "提供", "配有", "配备", "标配", "放着", "放在"})
}

func roomStockClaimSupported(task contracts.ReplyPlanTaskV2, content string, evidence contracts.EvidenceBundleV1) bool {
	for _, item := range evidence.Items {
		if !stringInSlice(item.Ref, task.EvidenceRefs) || !stringInSlice(task.TaskKey, item.TaskKeys) ||
			item.Answerability == "not_supporting" || strings.TrimSpace(item.Content) == "" {
			continue
		}
		if !knowledgeEvidenceHasExplicitEntityMatch(content, item.Content) {
			continue
		}
		if assertsRoomStockFact(item.Content) {
			return true
		}
	}
	return false
}

func replyTaskHasKnowledgeBoundary(task contracts.ReplyPlanTaskV2) bool {
	return task.Knowledge.Policy == "required" && runtimeKnowledgeBoundaryStatus(task.Knowledge.Status)
}

// validateReplyUnsupportedDomain 拦截「系统无数据源领域」的确定性断言。
// 房态/会员对应的能力（query_room_status / query_member_level）是 external 且未接入，
// 系统永远无法知道这些实时状态，因此任何断言都是编造，一票否决（rejected），不修复。
func validateReplyUnsupportedDomain(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	issues := make([]contracts.ValidationIssueV1, 0)
	for _, part := range input.Output.Parts {
		content := strings.TrimSpace(part.Content)
		if content == "" {
			continue
		}
		if assertsUngroundedDomainFact(compactReplyText(content)) {
			issues = append(issues, validationIssue(
				"unsupported_domain_assertion",
				"$.parts",
				"reply asserts a fact about an unsupported domain (room status/member) without a data source",
			))
		}
	}
	return issues
}

// assertsUngroundedFact 检测通用确定性断言模式：动词/判断词 + 具体宾语。
// 先排除明确表示“未写明/不知道/要确认”的澄清句，避免误伤安全表达。
// 领域断言（房态/会员）由 validateReplyUnsupportedDomain 独立负责，不在这里处理。
func assertsUngroundedFact(content string) bool {
	compact := compactReplyText(content)
	if compact == "" {
		return false
	}
	for _, safe := range []string{"没写", "未写明", "没有写", "资料里没有", "不清楚", "不确定", "无法确认", "不能确认", "要确认", "需要确认"} {
		if index := strings.Index(compact, safe); index >= 0 {
			tail := strings.TrimSpace(compact[index+len(safe):])
			if tail == "" || isNoHitClarifyingQuestion(tail) || isNoHitBoundaryContinuation(tail) {
				return false
			}
			// “资料没写”只能作为边界说明，不能再接“通常会有、可以去翻找”
			// 等没有证据的酒店常识。让协议修复只移除这段猜测即可。
			return containsAny(tail, []string{
				"通常", "一般", "应该", "大概率", "可能有", "会有", "会备", "可以去", "可以先",
				"提供", "配有", "放在", "位于", "桌上", "抽屉", "前台可以", "客房可以",
			})
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

func isNoHitClarifyingQuestion(text string) bool {
	return containsAny(text, []string{
		"请问", "方便说下", "方便告诉", "能说下", "可以说下", "你是想", "你主要想",
		"具体是", "具体要", "哪个", "哪种", "哪一", "什么", "多少", "几点", "何时",
	})
}

func isNoHitBoundaryContinuation(text string) bool {
	return containsAny(text, []string{
		"不清楚", "不确定", "无法确认", "暂时无法确认", "需要确认", "需要再确认", "还要确认",
		"以门店实际为准", "以订单页面为准", "以小程序页面为准",
	})
}

// assertsUngroundedDomainFact 判断内容是否对「系统没有数据源的领域」下了确定性断言。
// 领域是封闭集合：房态、会员——都对应 actions 目录里 external 且未接入的能力。
// 只要命中任一领域词就判编造，不依赖意图模型分类正确，也不依赖“几个断言词”。
func assertsUngroundedDomainFact(compact string) bool {
	for _, keyword := range ungroundedDomainKeywords() {
		if strings.Contains(compact, keyword) {
			return true
		}
	}
	return false
}

// ungroundedDomainKeywords 是「无数据源领域」的封闭断言词表。只覆盖「实时状态断言」，
// 这类词永远不该出现在合法答案里（实时房态/会员数据会变，知识库不该存会过期的事实）。
// 刻意不含“换房/大床房/双床房/房源”等房型/动作/查询词——它们可能合法出现在静态知识里，
// 也可能只是“去查”的软承诺，不单独触发；由 service_request 未命中转人工兜底。
func ungroundedDomainKeywords() []string {
	return []string{
		// 实时房态（对应 query_room_status，未接入）：系统无法知道“现在有没有房”
		"有房", "没房", "无房", "满房", "空房", "有房源", "没房源",
		// 会员（对应 query_member_level，未接入）
		"会员等级", "会员权益",
	}
}
