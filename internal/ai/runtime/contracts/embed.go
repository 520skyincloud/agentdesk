package contracts

import (
	"embed"
	"fmt"
	"sort"

	"agent-desk/internal/pkg/strictjson"
)

const (
	SchemaMessageAnalysisV1        = "message_analysis.v1"
	SchemaDialogueStateSnapshotV1  = "dialogue_state_snapshot.v1"
	SchemaIntentTasksV2            = "intent_tasks.v2"
	SchemaReplyPlanV2              = "reply_plan.v2"
	SchemaActionLedgerV1           = "action_ledger.v1"
	SchemaEvidenceBundleV1         = "evidence_bundle.v1"
	SchemaRuntimeContextSnapshotV1 = "runtime_context_snapshot.v1"
	SchemaReplyOutputV2            = "reply_output.v2"
	SchemaValidationResultV1       = "validation_result.v1"
	SchemaReplyTagContextV1        = "reply_tag_context.v1"
	SchemaRuntimeTraceV2           = "runtime_trace.v2"
	// 分层上下文隔离计划 P6 新增契约（先登记校验，生产路径按灰度切换）。
	SchemaEvidenceBundleV2         = "evidence_bundle.v2"
	SchemaReplyPlanV3              = "reply_plan.v3"
	SchemaResourceEligibilityV1    = "resource_eligibility.v1"
	SchemaValidationResultV2       = "validation_result.v2"
	SchemaRuntimeContextSnapshotV2 = "runtime_context_snapshot.v2"
	// 多模态/调度可靠性计划新增契约（先登记校验，生产路径成组灰度切换）。
	SchemaObservationV1             = "observation.v1"
	SchemaMediaAnalysisCandidateV1  = "media_analysis_candidate.v1"
	SchemaMessageAnalysisV2         = "message_analysis.v2"
	SchemaTurnInputEnvelopeV1       = "turn_input_envelope.v1"
	SchemaIntentTasksV3             = "intent_tasks.v3"
	SchemaTaskSourceBindingsV1      = "task_source_bindings.v1"
	SchemaQuestionUnitV1            = "question_unit.v1"
	SchemaTaskNormalizationResultV1 = "task_normalization_result.v1"
	SchemaCapabilityDecisionV1      = "capability_decision.v1"
	SchemaReplyPlanV4               = "reply_plan.v4"
	SchemaReplyOutputV3             = "reply_output.v3"
	SchemaValidationIssueV1         = "validation_issue.v1"
	SchemaHandoffDecisionV2         = "handoff_decision.v2"
	SchemaHandoffPendingActionV2    = "handoff_pending_action.v2"
	SchemaAnswerRequirementSetV1    = "answer_requirement_set.v1"
	SchemaRequirementStateSetV1     = "requirement_state_set.v1"
	SchemaResolvedTurnCoverageV1    = "resolved_turn_coverage.v1"
	SchemaGenerateTaskInputV1       = "generate_task_input.v1"
	SchemaValidationResultV3        = "validation_result.v3"
)

//go:embed *.schema.json
var schemaFiles embed.FS

var schemaFilenameByName = map[string]string{
	SchemaMessageAnalysisV1:         "message_analysis_v1.schema.json",
	SchemaDialogueStateSnapshotV1:   "dialogue_state_snapshot_v1.schema.json",
	SchemaIntentTasksV2:             "intent_tasks_v2.schema.json",
	SchemaReplyPlanV2:               "reply_plan_v2.schema.json",
	SchemaActionLedgerV1:            "action_ledger_v1.schema.json",
	SchemaEvidenceBundleV1:          "evidence_bundle_v1.schema.json",
	SchemaRuntimeContextSnapshotV1:  "runtime_context_snapshot_v1.schema.json",
	SchemaReplyOutputV2:             "reply_output_v2.schema.json",
	SchemaValidationResultV1:        "validation_result_v1.schema.json",
	SchemaReplyTagContextV1:         "reply_tag_context_v1.schema.json",
	SchemaRuntimeTraceV2:            "runtime_trace_v2.schema.json",
	SchemaEvidenceBundleV2:          "evidence_bundle_v2.schema.json",
	SchemaReplyPlanV3:               "reply_plan_v3.schema.json",
	SchemaResourceEligibilityV1:     "resource_eligibility_v1.schema.json",
	SchemaValidationResultV2:        "validation_result_v2.schema.json",
	SchemaRuntimeContextSnapshotV2:  "runtime_context_snapshot_v2.schema.json",
	SchemaObservationV1:             "observation_v1.schema.json",
	SchemaMediaAnalysisCandidateV1:  "media_analysis_candidate_v1.schema.json",
	SchemaMessageAnalysisV2:         "message_analysis_v2.schema.json",
	SchemaTurnInputEnvelopeV1:       "turn_input_envelope_v1.schema.json",
	SchemaIntentTasksV3:             "intent_tasks_v3.schema.json",
	SchemaTaskSourceBindingsV1:      "task_source_bindings_v1.schema.json",
	SchemaQuestionUnitV1:            "question_unit_v1.schema.json",
	SchemaTaskNormalizationResultV1: "task_normalization_result_v1.schema.json",
	SchemaCapabilityDecisionV1:      "capability_decision_v1.schema.json",
	SchemaReplyPlanV4:               "reply_plan_v4.schema.json",
	SchemaReplyOutputV3:             "reply_output_v3.schema.json",
	SchemaValidationIssueV1:         "validation_issue_v1.schema.json",
	SchemaHandoffDecisionV2:         "handoff_decision_v2.schema.json",
	SchemaHandoffPendingActionV2:    "handoff_pending_action_v2.schema.json",
	SchemaAnswerRequirementSetV1:    "answer_requirement_set_v1.schema.json",
	SchemaRequirementStateSetV1:     "requirement_state_set_v1.schema.json",
	SchemaResolvedTurnCoverageV1:    "resolved_turn_coverage_v1.schema.json",
	SchemaGenerateTaskInputV1:       "generate_task_input_v1.schema.json",
	SchemaValidationResultV3:        "validation_result_v3.schema.json",
}

func Schema(name string) ([]byte, error) {
	filename, ok := schemaFilenameByName[name]
	if !ok {
		return nil, fmt.Errorf("unknown runtime contract schema %q", name)
	}
	raw, err := schemaFiles.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read runtime contract schema %q: %w", name, err)
	}
	return append([]byte(nil), raw...), nil
}

func MustSchema(name string) []byte {
	raw, err := Schema(name)
	if err != nil {
		panic(err)
	}
	return raw
}

func SchemaNames() []string {
	names := make([]string, 0, len(schemaFilenameByName))
	for name := range schemaFilenameByName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func ValidateEmbeddedSchemas() error {
	for _, name := range SchemaNames() {
		raw, err := Schema(name)
		if err != nil {
			return err
		}
		if err := strictjson.ValidateSchemaDefinition(raw); err != nil {
			return fmt.Errorf("validate runtime contract schema %q: %w", name, err)
		}
	}
	return nil
}
