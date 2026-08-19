package executor

import "agent-desk/internal/ai/runtime/contracts"

func validateReplyEvidenceReferences(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	evidenceByRef := make(map[string]contracts.EvidenceItemV1, len(input.Evidence.Items))
	for _, item := range input.Evidence.Items {
		evidenceByRef[item.Ref] = item
	}
	planByTask := make(map[string]contracts.ReplyPlanTaskV2, len(input.Plan.Tasks))
	for _, task := range input.Plan.Tasks {
		planByTask[task.TaskKey] = task
	}
	issues := make([]contracts.ValidationIssueV1, 0)
	for partIndex, part := range input.Output.Parts {
		if isRuntimeTaskFailureNotice(part.Content) {
			continue
		}
		for _, ref := range part.EvidenceRefs {
			item, ok := evidenceByRef[ref]
			if !ok {
				issues = append(issues, validationIssue("unknown_evidence_ref", "$.parts", "unknown evidence ref "+ref))
				continue
			}
			if !stringsIntersect(part.TaskKeys, item.TaskKeys) {
				issues = append(issues, validationIssue("evidence_task_mismatch", "$.parts", "evidence ref does not belong to this reply part: "+ref))
			}
		}
		for _, taskKey := range part.TaskKeys {
			task := planByTask[taskKey]
			if task.Knowledge.Policy != "required" || task.Knowledge.Status != "has_context" {
				continue
			}
			if !stringsIntersect(part.EvidenceRefs, task.EvidenceRefs) {
				issues = append(issues, validationIssue("missing_task_evidence", "$.parts", "knowledge task lacks a supporting evidence ref: "+taskKey))
			}
		}
		_ = partIndex
	}
	return issues
}

func stringsIntersect(first, second []string) bool {
	seen := make(map[string]struct{}, len(first))
	for _, value := range first {
		seen[value] = struct{}{}
	}
	for _, value := range second {
		if _, ok := seen[value]; ok {
			return true
		}
	}
	return false
}
