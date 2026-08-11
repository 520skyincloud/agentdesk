package executor

import "agent-desk/internal/ai/runtime/contracts"

func validateReplyCommitInvariants(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	issues := make([]contracts.ValidationIssueV1, 0)
	if input.Plan.TurnVersion <= 0 || input.ActionLedger.TurnVersion != input.Plan.TurnVersion {
		issues = append(issues, validationIssue("turn_version_mismatch", "$", "reply plan and action ledger turn versions differ"))
	}
	if input.Evidence.ScopeFingerprint == "" {
		issues = append(issues, validationIssue("evidence_scope_missing", "$.evidence", "evidence scope fingerprint is missing"))
	}
	if len(input.Output.Parts) > input.Plan.GlobalConstraints.MaxReplyParts {
		issues = append(issues, validationIssue("too_many_reply_parts", "$.parts", "reply exceeds the plan part limit"))
	}
	for _, part := range input.Output.Parts {
		if part.Content == "" || len(part.TaskKeys) == 0 {
			issues = append(issues, validationIssue("empty_reply_part", "$.parts", "reply part must contain content and task keys"))
		}
	}
	return issues
}
