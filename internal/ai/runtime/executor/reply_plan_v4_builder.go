package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"agent-desk/internal/ai/runtime/contracts"
)

// 契约 22.13：BuildReplyPlanV4 把 AnswerGroup 从 capability/knowledge 层
// 完整带到最终 Plan。每个 Task 的 objective/outputMode/knowledge/
// resourcePolicy/constraints 是 CapabilityDecision、Evidence、
// ResourceEligibility 和 ActionLedger 的确定性投影；模型不得改判。
// planFingerprint 对 canonical JSON 计算 SHA-256，不包含 Prompt、客户正文、
// 知识正文或模型原始响应。

// ReplyPlanBuildInput 是 BuildReplyPlanV4 的聚合输入。
type ReplyPlanBuildInput struct {
	TurnID      int64
	TurnVersion int
	Tasks       []TaskRuntimeView
	Decisions   map[string]CapabilityDecisionV1
	Groups      []AnswerGroup
	// EvidenceByTask: taskKey -> 证据摘要
	EvidenceByTask map[string]TaskEvidenceResultView
	// ObservationRefsByTask: taskKey -> 当前 Envelope 内允许该任务读取的 O*。
	ObservationRefsByTask map[string][]string
	// ActionRefsByTask: taskKey -> 服务端已准备 Action 引用
	ActionRefsByTask map[string][]string
	// RequiredFactsByTask: taskKey -> 权威事实引用（S*）
	RequiredFactsByTask map[string][]string
	// ScopeFingerprint: tenant/store/conversation/session/binding
	ScopeFingerprint string
	// FactSnapshotFingerprint / ResourceEligibilityFingerprint /
	// ActionLedgerFingerprint / PromptPolicyRevisions
	FactSnapshotFingerprint        string
	ResourceEligibilityFingerprint string
	ActionLedgerFingerprint        string
	PromptPolicyRevisions          string
}

// BuildReplyPlanV4 构造最终计划；maxPartsPerGroup 固定为 1。
func BuildReplyPlanV4(input ReplyPlanBuildInput) (contracts.ReplyPlanV4, error) {
	readyGroups := make([]AnswerGroup, 0, len(input.Groups))
	for _, group := range input.Groups {
		// resource_only/handoff are committed by their own deterministic paths;
		// they must never become required model output groups.
		if group.OutputMode == "text" {
			readyGroups = append(readyGroups, group)
		}
	}
	readyGroups = SelectReadyGroups(readyGroups)
	selectedTasks := make(map[string]struct{}, len(input.Tasks))
	for _, group := range readyGroups {
		for _, taskKey := range group.TaskKeys {
			selectedTasks[taskKey] = struct{}{}
		}
	}
	plan := contracts.ReplyPlanV4{
		SchemaVersion:  contracts.ReplyPlanV4SchemaVersion,
		TurnVersion:    input.TurnVersion,
		ShouldGenerate: len(readyGroups) > 0,
		Tasks:          make([]contracts.ReplyPlanTaskV4, 0, len(selectedTasks)),
		ReplyGroups:    make([]contracts.ReplyPlanGroupV4, 0, len(readyGroups)),
		GlobalConstraints: contracts.ReplyPlanGlobalV4{
			MaxReplyParts:       minInt(len(readyGroups), 3),
			MaxQuestionsPerPart: 4,
			ForbiddenClaims: []string{
				"unprepared_resource_sent", "uncommitted_handoff", "unexecuted_tool_result",
				"unsupported_price", "unsupported_policy", "unsupported_time_promise",
				"cross_store_fact", "internal_tag_source", "internal_sender_label",
				"customer_media_promoted_to_store_fact", "unsupported_store_fact",
			},
		},
	}
	if !plan.ShouldGenerate {
		plan.GlobalConstraints.MaxReplyParts = 0
	}
	groupKeyByTask := make(map[string]string, len(input.Tasks))
	for _, group := range readyGroups {
		for _, key := range group.TaskKeys {
			groupKeyByTask[key] = group.GroupKey
		}
		plan.ReplyGroups = append(plan.ReplyGroups, contracts.ReplyPlanGroupV4{
			GroupKey: group.GroupKey, TaskKeys: group.TaskKeys, Sequence: group.Sequence,
			OutputMode: group.OutputMode, MaxParts: 1, Required: true,
		})
	}
	for _, task := range input.Tasks {
		if _, selected := selectedTasks[task.TaskKey]; !selected {
			continue
		}
		decision := input.Decisions[task.TaskKey]
		evidence := input.EvidenceByTask[task.TaskKey]
		actionRefs := input.ActionRefsByTask[task.TaskKey]
		outputMode := FinalOutputMode(decision.Route, len(actionRefs) > 0)
		policy, status, reason := knowledgeProjection(decision, evidence)
		planTask := contracts.ReplyPlanTaskV4{
			TaskKey:        task.TaskKey,
			Sequence:       task.Sequence,
			Intent:         nonEmpty(task.Intent, "general"),
			SubIntent:      task.SubIntent,
			ClaimType:      nonEmpty(evidence.ClaimType, "unknown"),
			AnswerGroupKey: groupKeyByTask[task.TaskKey],
			Objective:      objectiveFor(task, decision),
			OutputMode:     outputMode,
			Knowledge: contracts.ReplyPlanKnowledgeV4{
				Policy: policy, Status: status, ReasonCode: reason,
			},
			EvidenceRefs:     evidenceRefsFor(evidence),
			ObservationRefs:  append([]string(nil), input.ObservationRefsByTask[task.TaskKey]...),
			RequiredFactRefs: input.RequiredFactsByTask[task.TaskKey],
			ActionRefs:       actionRefs,
			ResourcePolicy:   resourcePolicyFor(decision, outputMode),
			Constraints:      constraintsFor(decision, evidence),
		}
		if planTask.AnswerGroupKey == "" || planTask.EvidenceRefs == nil {
			planTask.EvidenceRefs = []string{}
		}
		if planTask.ActionRefs == nil {
			planTask.ActionRefs = []string{}
		}
		if planTask.ObservationRefs == nil {
			planTask.ObservationRefs = []string{}
		}
		if planTask.RequiredFactRefs == nil {
			planTask.RequiredFactRefs = []string{}
		}
		plan.Tasks = append(plan.Tasks, planTask)
	}
	fingerprint, err := PlanFingerprintV4(input, plan)
	if err != nil {
		return contracts.ReplyPlanV4{}, err
	}
	plan.PlanFingerprint = fingerprint
	return plan, nil
}

func knowledgeProjection(decision CapabilityDecisionV1, evidence TaskEvidenceResultView) (policy, status, reason string) {
	switch decision.Route {
	case "knowledge_answer":
		policy = "required"
		switch evidence.Status {
		case "approved", "hit":
			status = "has_context"
		case "no_context":
			status = "no_context"
			reason = "knowledge_no_context"
		case "unavailable":
			status = "unavailable"
			reason = "knowledge_unavailable"
		case "blocked":
			status = "unanswerable"
			reason = "knowledge_blocked"
		default:
			status = "no_context"
			reason = "knowledge_missing"
		}
	case "direct_answer", "social_reply":
		policy = "forbidden"
		status = "not_needed"
		reason = "route_" + decision.Route
	default:
		policy = "optional"
		status = "not_needed"
		reason = "route_" + decision.Route
	}
	return policy, status, reason
}

func evidenceRefsFor(evidence TaskEvidenceResultView) []string {
	refs := make([]string, 0, 4)
	refs = append(refs, evidence.EvidenceRefs...)
	return refs
}

func objectiveFor(task TaskRuntimeView, decision CapabilityDecisionV1) string {
	// Objective 是给 Generate 的客户请求，不是内部 route 描述。SourceText 已由
	// task ledger 依据消息 ID 和 rune span 校验，截断只服务于内部 JSON 上限。
	if sourceText := boundedIntentCatalogText(task.SourceText, 500); sourceText != "" {
		return sourceText
	}
	// 非持久 Task 的防御兜底；正常 V3 ledger 路径不应走到这里。
	return fmt.Sprintf("回答当前任务 %s", task.TaskKey)
}

func resourcePolicyFor(decision CapabilityDecisionV1, outputMode string) contracts.ReplyResourcePolicy {
	if outputMode == "handoff" || outputMode == "resource_only" || decision.Route == "reject_unsupported" || decision.Route == "no_reply" {
		return contracts.ReplyResourcePolicy{Mode: "forbidden", AllowedTypes: []string{}, AllowedPurposes: []string{}}
	}
	// 资源允许集合由 ResourceEligibility 账本独立判定；Task 内只保存保守策略。
	return contracts.ReplyResourcePolicy{Mode: "explicit_only", AllowedTypes: []string{}, AllowedPurposes: []string{}}
}

func constraintsFor(decision CapabilityDecisionV1, evidence TaskEvidenceResultView) []string {
	constraints := []string{"no_unsupported_facts", "no_action_claim", "no_internal_terms", "short_wechat_style"}
	if decision.Route == "clarify_required_fields" {
		constraints = append(constraints, "ask_one_missing_field")
	}
	if decision.Route == "knowledge_answer" && evidence.Status == "no_context" {
		constraints = append(constraints, "acknowledge_uncertainty")
	}
	if evidence.ClaimType == "recommendation" {
		constraints = append(constraints, "recommendation_evidence_only")
	}
	if decision.Route == "business_handoff" {
		constraints = append(constraints, "do_not_repeat_resolved_answer")
	}
	return constraints
}

// PlanFingerprintV4 对 scope/task/decision/group/evidence/action/prompt 输入
// 的 canonical JSON 计算 SHA-256。
func PlanFingerprintV4(input ReplyPlanBuildInput, plan contracts.ReplyPlanV4) (string, error) {
	taskKeys := make([]string, 0, len(input.Tasks))
	for _, task := range input.Tasks {
		taskKeys = append(taskKeys, task.TaskKey)
	}
	decisionFingerprints := make(map[string]string, len(input.Decisions))
	for key, decision := range input.Decisions {
		decisionFingerprints[key] = decision.PolicyFingerprint
	}
	evidenceFingerprints := make(map[string]string, len(input.EvidenceByTask))
	for key, evidence := range input.EvidenceByTask {
		evidenceFingerprints[key] = evidence.Fingerprint
	}
	groupKeys := make([]string, 0, len(input.Groups))
	for _, group := range input.Groups {
		groupKeys = append(groupKeys, group.GroupKey)
	}
	canonical := map[string]any{
		"contractVersions": map[string]string{
			"plan": contracts.ReplyPlanV4SchemaVersion,
			"out":  contracts.ReplyOutputV3SchemaVersion,
		},
		"scopeFingerprint":               input.ScopeFingerprint,
		"turnID":                         fmt.Sprintf("%d", input.TurnID),
		"turnVersion":                    input.TurnVersion,
		"taskKeys":                       taskKeys,
		"capabilityFingerprints":         decisionFingerprints,
		"answerGroupKeys":                groupKeys,
		"evidenceFingerprints":           evidenceFingerprints,
		"observationRefsByTask":          input.ObservationRefsByTask,
		"factSnapshotFingerprint":        input.FactSnapshotFingerprint,
		"resourceEligibilityFingerprint": input.ResourceEligibilityFingerprint,
		"actionLedgerFingerprint":        input.ActionLedgerFingerprint,
		"promptPolicyRevisions":          input.PromptPolicyRevisions,
		"globalConstraints":              plan.GlobalConstraints,
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
