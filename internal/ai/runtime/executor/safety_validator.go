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
		if containsAny(content, []string{"已经发给你", "已经发送", "已发送", "已经通知", "已经安排", "已转人工", "已经转人工", "已经完成", "已完成"}) {
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
