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

// validateReplyFutureCommitClaims 单独校验"未来承诺"表达。它只给一次协议修复机会
// （repairable），而不是像 safety 其它硬伤那样一票否决。因为模型表达"我看看/先确认"
// 等中性短语并不一定构成承诺执行，直接 rejected 会误杀正常回复并触发转人工。
func validateReplyFutureCommitClaims(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	issues := make([]contracts.ValidationIssueV1, 0)
	for partIndex, part := range input.Output.Parts {
		if futureCommitPhrase(strings.TrimSpace(part.Content)) {
			issues = append(issues, validationIssue("future_commit_claim", "$.parts", "reply promises a pending staff action without an action ledger entry"))
		}
		_ = partIndex
	}
	return issues
}

// futureCommitPhrase 识别“未落地就口头承诺后续动作”的表述。
// 这些动作必须由 ActionLedger/工具链落地，模型不能口头承诺“我去查/我帮你问/记下了”。
func futureCommitPhrase(content string) bool {
	compact := compactReplyText(content)
	if compact == "" {
		return false
	}
	for _, phrase := range futureCommitPhrases() {
		if strings.Contains(compact, phrase) {
			return true
		}
	}
	return false
}

func futureCommitPhrases() []string {
	return []string{
		"我帮你查", "我查一下", "我先查", "我去查", "这就去查", "我去查一下",
		"我帮你问", "我去问", "我帮你确认", "我确认一下", "我去确认",
		"我帮你记下", "我记下了", "帮你记下", "我这边先确认", "确认下再回复",
		"等下给你信", "稍后回复你", "稍后给你", "晚点回复", "我再去查",
	}
}
