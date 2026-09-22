package utils

import (
	"regexp"
	"strings"
)

var (
	explicitHandoffTargetPattern = regexp.MustCompile(`转接人工|转人工|转真人|找人工|找真人|人工客服|真人客服|找客服|联系人工|联系同事|找同事|接人工|接同事|人工处理|人工介入|前台同事`)
	handoffNegationPattern       = regexp.MustCompile(`(?:可不可以不|能不能不|是否可以不|能否不|可以不|先不|不要|不用|不需要|无需|无须|不必|不想|不愿|不能|不准|禁止|拒绝|取消|撤销|停止|先别|别|勿|没让|没有让|没说|没有说|不是要|并非要|不)(?:你|您|我|帮|替|为|给|请|再|去|要|让|直接|自动|继续|现在|先|马上|立刻|随便|动不动|就|总是|一直)*$`)
	handoffConnectorPattern      = regexp.MustCompile(`^(?:和|或|或者|以及|还有|跟|与|及|、|/|并且)*$`)
	quotedHandoffTextPattern     = regexp.MustCompile("“[^”]*”|‘[^’]*’|「[^」]*」|『[^』]*』|\"[^\"]*\"|'[^']*'|`[^`]*`")
)

// IsExplicitHumanHandoffRequest reports whether the current customer text
// explicitly asks for a human agent. Dissatisfaction, a service request, or
// uncertainty alone must not authorize a handoff.
func IsExplicitHumanHandoffRequest(text string) bool {
	if IsRuntimeCustomerBurstEnvelope(text) {
		text = RuntimeCustomerBurstDisplayText(text)
	}
	text = quotedHandoffTextPattern.ReplaceAllString(text, "")
	clauses := strings.FieldsFunc(text, func(r rune) bool {
		return strings.ContainsRune("，,。.!！？?；;\n\r", r)
	})
	authorized := false
	for _, clause := range clauses {
		clause = strings.Join(strings.Fields(clause), "")
		if clause == "人工" || clause == "真人" || clause == "客服" {
			authorized = true
			continue
		}
		// A later explicit cancellation revokes an earlier request in this turn.
		switch clause {
		case "取消", "撤销", "算了", "不用", "不用了", "不需要了", "先不用了", "还是不用了", "还是算了", "别了":
			authorized = false
			continue
		}
		previousEnd := 0
		nonAuthorizationCarries := false
		for _, match := range explicitHandoffTargetPattern.FindAllStringIndex(clause, -1) {
			prefix := clause[previousEnd:match[0]]
			suffix := clause[match[1]:]
			previousEnd = match[1]
			if strings.HasPrefix(suffix, "智能") {
				continue
			}
			negationPrefix := strings.ReplaceAll(prefix, "能不能", "能否")
			negated := handoffNegationPattern.MatchString(negationPrefix) ||
				(nonAuthorizationCarries && handoffConnectorPattern.MatchString(prefix))
			if negated || isHandoffExplanationQuestion(prefix, suffix) {
				authorized = false
				nonAuthorizationCarries = true
				continue
			}
			authorized = true
			nonAuthorizationCarries = false
		}
	}
	return authorized
}

func isHandoffExplanationQuestion(prefix, suffix string) bool {
	for _, marker := range []string{"为什么", "为何", "什么叫", "你怎么", "你干嘛", "怎么就", "是不是要", "你说的"} {
		if strings.Contains(prefix, marker) {
			return true
		}
	}
	for _, marker := range []string{"是什么意思", "是什么", "什么意思", "几个意思", "这句话"} {
		if strings.HasPrefix(suffix, marker) {
			return true
		}
	}
	return false
}
