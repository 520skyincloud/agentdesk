package executor

import (
	"strconv"
	"strings"
)

func runtimeIntentConcreteEntityText(entityText string) bool {
	return len([]rune(entityText)) >= 2 && !containsAnyPrefix(entityText, []string{"酒店", "门店", "房间", "客房", "客户", "服务", "问题"})
}

func runtimeIntentAtomicCandidateRequiresContext(candidate string) bool {
	compact := normalizeRuntimeKnowledgeQuery(candidate)
	if compact == "" || isDependentIntentTaskClause(candidate) {
		return true
	}
	if len([]rune(compact)) <= 10 && containsAnyPrefix(compact, []string{"这", "那", "刚才", "刚刚", "前面", "上面", "之前", "同样"}) {
		return true
	}
	standalone := strings.Trim(compact, "啊呀呢吧哈啦哦嘛么")
	if standalone == "你懂我意思吗" || standalone == "你明白我意思吗" || standalone == "明白我意思吧" {
		return true
	}
	return containsAny(compact, []string{"再说一遍", "再复述", "重新说", "上一个", "刚才那个", "前面那个", "上面那个", "同样呢"})
}

func runtimeIntentAtomicKnowledgeObjective(text string) string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	hasPrice := containsAny(compact, []string{"价格", "收费", "免费", "多少钱"})
	hasQuantity := containsAny(compact, []string{"几瓶", "几个", "多少瓶", "数量"})
	if hasPrice && hasQuantity {
		return "compound_information"
	}
	switch {
	case hasPrice:
		return "price"
	case containsAny(compact, []string{"推荐", "好玩", "好吃"}):
		return "recommendation"
	case strings.Contains(compact, "怎么"), strings.Contains(compact, "如何"), strings.Contains(compact, "方式"), strings.Contains(compact, "流程"), strings.Contains(compact, "咋"), strings.Contains(compact, "填"):
		return "method"
	case strings.Contains(compact, "地址"), strings.Contains(compact, "位置"), strings.Contains(compact, "在哪里"), strings.Contains(compact, "在哪"), strings.Contains(compact, "哪里"):
		return "location"
	case hasQuantity:
		return "quantity"
	case strings.Contains(compact, "几点"), strings.Contains(compact, "多久"), strings.Contains(compact, "什么时候"), strings.Contains(compact, "时间"):
		return "time"
	case strings.Contains(compact, "有没有"), strings.Contains(compact, "是否有"), runtimeIntentAtomicLooksLikeAvailabilityQuestion(compact):
		return "availability"
	case strings.Contains(compact, "是不是"), strings.Contains(compact, "是否"), strings.Contains(compact, "规则"):
		return "policy"
	default:
		return "general_guidance"
	}
}

func runtimeIntentAtomicLooksLikeAvailabilityQuestion(text string) bool {
	compact := strings.Trim(normalizeRuntimeKnowledgeQuery(text), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	if compact == "" {
		return false
	}
	return (strings.HasPrefix(compact, "有") && strings.HasSuffix(normalizeRuntimeKnowledgeQuery(text), "吗")) ||
		strings.Contains(compact, "有吗") || strings.Contains(compact, "提供吗") || strings.Contains(compact, "配备吗")
}

func runtimeIntentSourceRefIndex(ref string) int {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "U") {
		return -1
	}
	value, err := strconv.Atoi(strings.TrimPrefix(ref, "U"))
	if err != nil || value <= 0 {
		return -1
	}
	return value - 1
}
