package executor

import (
	"fmt"

	"agent-desk/internal/ai/runtime/contracts"
)

func validateReplyTaskCoverage(input ReplyValidationInput) ([]contracts.ValidationIssueV1, bool) {
	expected := make(map[string]int)
	for _, task := range input.Plan.Tasks {
		if task.OutputMode == "text" || task.OutputMode == "text_and_resource" || task.OutputMode == "clarification" {
			expected[task.TaskKey] = task.Sequence
		}
	}
	seen := make(map[string]int)
	issues := make([]contracts.ValidationIssueV1, 0)
	repairable := true
	for partIndex, part := range input.Output.Parts {
		for _, taskKey := range part.TaskKeys {
			if _, ok := expected[taskKey]; !ok {
				issues = append(issues, validationIssue("unknown_task_key", fmt.Sprintf("$.parts[%d].taskKeys", partIndex), "reply references a task outside the current plan"))
				repairable = false
				continue
			}
			seen[taskKey]++
		}
	}
	for taskKey := range expected {
		switch seen[taskKey] {
		case 0:
			issues = append(issues, validationIssue("missing_task_key", "$.parts", "reply does not cover task "+taskKey))
		case 1:
		default:
			issues = append(issues, validationIssue("duplicate_task_key", "$.parts", "reply covers task more than once: "+taskKey))
		}
	}
	return issues, repairable
}

func minimumTaskSequence(taskKeys []string, plan *contracts.ReplyPlanV2) int {
	if plan == nil {
		return 1 << 30
	}
	sequenceByKey := make(map[string]int, len(plan.Tasks))
	for _, task := range plan.Tasks {
		sequenceByKey[task.TaskKey] = task.Sequence
	}
	minimum := 1 << 30
	for _, taskKey := range taskKeys {
		if sequence, ok := sequenceByKey[taskKey]; ok && sequence < minimum {
			minimum = sequence
		}
	}
	return minimum
}
