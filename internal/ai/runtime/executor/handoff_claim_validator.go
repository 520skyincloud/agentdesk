package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

// explicitUnauthorizedHandoffClaim only matches a completed/first-person
// handoff promise. Advice such as "如需人工可联系前台" or a quoted knowledge
// fact such as "另一间房异常时请转接" remains ordinary evidence text.
func explicitUnauthorizedHandoffClaim(content string) bool {
	compact := compactReplyText(content)
	if compact == "" {
		return false
	}
	for _, phrase := range []string{
		"我帮你转人工", "我帮您转人工", "我这边帮你转人工", "我这边帮您转人工",
		"我现在帮你转人工", "我现在帮您转人工", "我给你转人工", "我给您转人工",
		"给你转人工", "给您转人工", "我给你安排人工", "我给您安排人工",
		"已转人工", "已经转人工", "已为你转人工", "已为您转人工",
		"已为你转接人工", "已为您转接人工", "人工已接手", "人工已经接手",
		"人工会接手", "人工将接手", "人工会联系你", "人工会联系您", "客服会联系你", "客服会联系您",
		"已安排人工", "已经安排人工", "安排人工处理", "转给人工处理",
		"转接人工处理", "同事已接手", "同事已经接手", "同事会联系你", "同事会联系您",
		// This catches the malformed but customer-visible variant observed in
		// production (for example, "入住得人工接手") without blocking a
		// neutral knowledge statement such as "如需人工可联系前台".
		"入住得人工接手", "这得人工接手", "这个得人工接手", "这边人工接手",
	} {
		if strings.Contains(compact, compactReplyText(phrase)) {
			return true
		}
	}
	return false
}

func validateReplyUnauthorizedHandoffClaims(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	authorizedTasks := make(map[string]bool, len(input.Plan.Tasks))
	for _, task := range input.Plan.Tasks {
		if task.OutputMode == "handoff" || task.Intent == "human_complaint_risk" {
			authorizedTasks[task.TaskKey] = true
		}
	}
	authorizedActions := make(map[string]bool, len(input.ActionLedger.Actions))
	for _, action := range input.ActionLedger.Actions {
		kind := strings.ToLower(strings.TrimSpace(action.ActionType + " " + action.ActionKey))
		if strings.Contains(kind, "handoff") || strings.Contains(kind, "human") {
			authorizedActions[action.ActionKey] = true
		}
	}
	issues := make([]contracts.ValidationIssueV1, 0)
	for _, part := range input.Output.Parts {
		if !explicitUnauthorizedHandoffClaim(part.Content) || replyPartHasHandoffAuthority(part.TaskKeys, part.ActionRefs, authorizedTasks, authorizedActions) {
			continue
		}
		issues = append(issues, validationIssue(
			"unauthorized_handoff_claim", "$.parts.content",
			"reply claims that a human handoff occurred without a current human route or committed handoff action",
		))
	}
	return issues
}

func replyPartHasHandoffAuthority(taskKeys, actionRefs []string, tasks, actions map[string]bool) bool {
	for _, key := range taskKeys {
		if tasks[strings.TrimSpace(key)] {
			return true
		}
	}
	for _, ref := range actionRefs {
		if actions[strings.TrimSpace(ref)] {
			return true
		}
	}
	return false
}

func validateV3UnauthorizedHandoffClaims(input ReplyValidationInputV3, result *contracts.ValidationResultV3) {
	authorizedTasks := make(map[string]bool, len(input.Plan.Tasks))
	for _, task := range input.Plan.Tasks {
		if task.OutputMode == "handoff" || task.Intent == "human_complaint_risk" {
			authorizedTasks[task.TaskKey] = true
		}
	}
	authorizedActions := make(map[string]bool, len(input.ActionLedger.Actions))
	for _, action := range input.ActionLedger.Actions {
		kind := strings.ToLower(strings.TrimSpace(action.ActionType + " " + action.ActionKey))
		if strings.Contains(kind, "handoff") || strings.Contains(kind, "human") {
			authorizedActions[action.ActionKey] = true
		}
	}
	for index, part := range result.NormalizedParts {
		if !explicitUnauthorizedHandoffClaim(part.Content) || replyPartHasHandoffAuthority(part.TaskKeys, part.ResolvedActionRefs, authorizedTasks, authorizedActions) {
			continue
		}
		result.Checks.ActionClaims = "failed"
		result.Errors = append(result.Errors, contracts.ValidationIssueV1{
			Code: "unauthorized_handoff_claim", Path: partPath(index, "content"),
			Message: "reply claims a human handoff without a current human route or committed handoff action",
		})
	}
}
