package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

func validateReplyActionReferences(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	actions := make(map[string]contracts.ActionLedgerItemV1, len(input.ActionLedger.Actions))
	for _, action := range input.ActionLedger.Actions {
		actions[action.ActionKey] = action
	}
	issues := make([]contracts.ValidationIssueV1, 0)
	for _, part := range input.Output.Parts {
		for _, ref := range part.ActionRefs {
			action, ok := actions[ref]
			if !ok {
				issues = append(issues, validationIssue("unknown_action_ref", "$.parts.actionRefs", "unknown action ref "+ref))
				continue
			}
			if action.Status != "prepared" {
				issues = append(issues, validationIssue("action_not_prepared", "$.parts.actionRefs", "action is not prepared: "+ref))
			}
			if !containsTrimmedString(part.TaskKeys, action.TaskKey) {
				issues = append(issues, validationIssue("action_task_mismatch", "$.parts.actionRefs", "action does not belong to this reply part: "+ref))
			}
		}
	}
	return issues
}

func containsTrimmedString(items []string, value string) bool {
	value = strings.TrimSpace(value)
	for _, item := range items {
		if strings.TrimSpace(item) == value {
			return true
		}
	}
	return false
}
