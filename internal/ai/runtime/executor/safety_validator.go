package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

func validateReplySafety(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	issues := make([]contracts.ValidationIssueV1, 0)
	internalIdentifiers := make([]string, 0, len(input.Plan.Tasks)+len(input.Evidence.Items)+len(input.ActionLedger.Actions))
	for _, task := range input.Plan.Tasks {
		internalIdentifiers = append(internalIdentifiers, strings.TrimSpace(task.TaskKey))
	}
	for _, evidence := range input.Evidence.Items {
		internalIdentifiers = append(internalIdentifiers, strings.TrimSpace(evidence.Ref))
	}
	actionByKey := make(map[string]contracts.ActionLedgerItemV1, len(input.ActionLedger.Actions))
	for _, action := range input.ActionLedger.Actions {
		actionByKey[action.ActionKey] = action
		internalIdentifiers = append(internalIdentifiers, strings.TrimSpace(action.ActionKey))
	}
	for _, part := range input.Output.Parts {
		content := strings.TrimSpace(part.Content)
		lower := strings.ToLower(content)
		for _, internalTerm := range []string{"taskkey", "evidenceref", "actionref", "reply_plan", "intent_tasks", "内部标签", "模型提示词"} {
			if strings.Contains(lower, strings.ToLower(internalTerm)) {
				issues = append(issues, validationIssue("internal_term_exposed", "$.parts", "reply exposes runtime internal terminology"))
				break
			}
		}
		for _, identifier := range internalIdentifiers {
			if identifier != "" && strings.Contains(content, identifier) {
				issues = append(issues, validationIssue("internal_identifier_exposed", "$.parts", "reply exposes a runtime task, evidence, or action identifier"))
				break
			}
		}
		if containsAny(content, []string{
			"已经发给你", "已经发送", "已发送", "已经通知", "已经安排", "已转人工", "已经转人工", "已经完成", "已完成",
			"已经联系", "已联系", "已经提交", "已提交", "已经登记", "已登记", "已经处理", "已处理", "处理好了", "安排好了", "转过去了",
		}) {
			if len(part.ActionRefs) == 0 {
				issues = append(issues, validationIssue("unsupported_action_claim", "$.parts", "reply claims an action without prepared action evidence"))
				continue
			}
			for _, actionRef := range part.ActionRefs {
				action, ok := actionByKey[actionRef]
				if !ok || action.Status != "committed" && action.Status != "delivered" {
					issues = append(issues, validationIssue("action_not_committed_claim", "$.parts", "reply claims an action completed before commit or delivery"))
					break
				}
			}
		}
	}
	return issues
}

// validateReplyFutureCommitClaims 单独校验"承诺去做 / 正在做 / 已经做了"的越权承诺。
// 采用白名单判定（见 promise_allowlist.go）：句子出现承诺语态却未落到白名单动作上，
// 属于硬伤，直接 rejected，由人工兜底；中性短语（我看看/我确认）不含承诺语态，不受影响。
func validateReplyFutureCommitClaims(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	return validateReplyPromiseAllowlist(input)
}
