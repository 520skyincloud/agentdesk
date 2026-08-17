package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/strictjson"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

func buildRuntimeReplyPlanV2(
	turnVersion int,
	plans []callbacks.ReplyTaskPlanTraceData,
	knowledge map[string]AnswerabilityOutcome,
	ledger contracts.ActionLedgerV1,
) (contracts.ReplyPlanV2, error) {
	if turnVersion <= 0 {
		turnVersion = 1
	}
	actionsByTask := make(map[string][]string)
	for _, action := range ledger.Actions {
		if action.Status != "requested" && action.Status != "prepared" {
			continue
		}
		actionsByTask[action.TaskKey] = appendUniqueStrings(actionsByTask[action.TaskKey], action.ActionKey)
	}
	ordered := append([]callbacks.ReplyTaskPlanTraceData(nil), plans...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := ordered[i].Sequence
		right := ordered[j].Sequence
		if left <= 0 {
			left = i + 1
		}
		if right <= 0 {
			right = j + 1
		}
		return left < right
	})
	ret := contracts.ReplyPlanV2{
		SchemaVersion: contracts.ReplyPlanV2SchemaVersion,
		TurnVersion:   turnVersion, Tasks: make([]contracts.ReplyPlanTaskV2, 0, len(ordered)),
		GlobalConstraints: contracts.ReplyPlanGlobalConstraints{
			MaxReplyParts: 3, MaxQuestionsPerPart: 4,
			ForbiddenClaims: []string{
				"unprepared_resource_sent", "uncommitted_handoff", "unexecuted_tool_result",
				"unsupported_price", "unsupported_policy", "unsupported_time_promise",
				"cross_store_fact", "internal_tag_source", "internal_sender_label",
			},
		},
	}
	for index, plan := range ordered {
		sequence := plan.Sequence
		if sequence <= 0 {
			sequence = index + 1
		}
		knowledgePolicy := "forbidden"
		knowledgeStatus := "not_needed"
		reasonCode := "knowledge_not_needed"
		evidenceRefs := []string{}
		outputMode := runtimeReplyPlanOutputMode(plan)
		if runtimeTaskTypeForPlan(plan) == "knowledge" {
			knowledgePolicy = "required"
			knowledgeStatus = "pending"
			reasonCode = "knowledge_pending"
			if outcome, ok := knowledge[plan.TaskKey]; ok {
				knowledgeStatus = outcome.Status
				reasonCode = outcome.ReasonCode
				evidenceRefs = append([]string(nil), outcome.SupportingRefs...)
			}
			if knowledgeStatus == "no_context" || knowledgeStatus == "unanswerable" {
				outputMode = "clarification"
			}
			if knowledgeStatus == "unavailable" {
				outputMode = "handoff"
			}
		}
		constraints := []string{"no_unsupported_facts", "no_action_claim", "no_internal_terms", "short_wechat_style"}
		switch plan.RelationType {
		case "repeat":
			constraints = appendUniqueStrings(constraints, "do_not_repeat_resolved_answer")
		case "correction":
			constraints = appendUniqueStrings(constraints, "answer_current_correction_only")
		}
		if outputMode == "clarification" {
			constraints = appendUniqueStrings(constraints, "ask_one_missing_field", "acknowledge_uncertainty")
		}
		objective := strings.TrimSpace(plan.Text)
		if objective == "" {
			objective = runtimeTaskDisplayLabel(plan.SubIntent)
		}
		if objective == "" {
			objective = strings.TrimSpace(plan.Intent)
		}
		task := contracts.ReplyPlanTaskV2{
			TaskKey: plan.TaskKey, Sequence: sequence, Intent: plan.Intent, SubIntent: plan.SubIntent,
			Objective: boundedEvidenceText(objective, 500), OutputMode: outputMode,
			Knowledge:    contracts.ReplyPlanKnowledge{Policy: knowledgePolicy, Status: knowledgeStatus, ReasonCode: boundedEvidenceText(reasonCode, 80)},
			EvidenceRefs: nonNilStrings(evidenceRefs), ActionRefs: nonNilStrings(actionsByTask[plan.TaskKey]), Constraints: nonNilStrings(constraints),
		}
		if outputMode == "text" || outputMode == "text_and_resource" || outputMode == "clarification" {
			ret.ShouldGenerate = true
		}
		ret.Tasks = append(ret.Tasks, task)
	}
	if err := validateRuntimeReplyPlanContract(ret, ledger); err != nil {
		return contracts.ReplyPlanV2{}, err
	}
	return ret, nil
}

func nonNilStrings(items []string) []string {
	if len(items) == 0 {
		return []string{}
	}
	return append([]string(nil), items...)
}

func runtimeReplyPlanOutputMode(plan callbacks.ReplyTaskPlanTraceData) string {
	switch runtimeTaskTypeForPlan(plan) {
	case "resource":
		return "resource_only"
	case "human":
		return "handoff"
	default:
		return "text"
	}
}

func validateRuntimeReplyPlanContract(plan contracts.ReplyPlanV2, ledger contracts.ActionLedgerV1) error {
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	if _, err := strictjson.DecodeObject[contracts.ReplyPlanV2](raw, strictjson.DecodeOptions{
		MaxBytes: 64 * 1024, Schema: contracts.MustSchema(contracts.SchemaReplyPlanV2),
	}); err != nil {
		return err
	}
	actions := make(map[string]contracts.ActionLedgerItemV1, len(ledger.Actions))
	for _, action := range ledger.Actions {
		actions[action.ActionKey] = action
	}
	for _, task := range plan.Tasks {
		if !plan.ShouldGenerate && task.OutputMode != "resource_only" && task.OutputMode != "handoff" && task.OutputMode != "skip" {
			return &strictjson.ProtocolError{Code: strictjson.ErrorJSONBusinessInvariant, Path: "$.tasks", Message: "non-generating plan contains a text task"}
		}
		if task.Knowledge.Policy == "required" && task.Knowledge.Status == "has_context" && len(task.EvidenceRefs) == 0 {
			return &strictjson.ProtocolError{Code: strictjson.ErrorJSONReferenceInvalid, Path: "$.tasks", Message: "knowledge task has no supporting evidence ref"}
		}
		for _, ref := range task.ActionRefs {
			action, ok := actions[ref]
			if !ok || action.TaskKey != task.TaskKey || (action.Status != "requested" && action.Status != "prepared") {
				return &strictjson.ProtocolError{Code: strictjson.ErrorJSONReferenceInvalid, Path: "$.tasks.actionRefs", Message: "action ref is outside the current task plan"}
			}
		}
	}
	return nil
}

func ensureRuntimeActionLedger(req RunInput, taskState runtimeTaskBatchState, plans []callbacks.ReplyTaskPlanTraceData, evidence *contracts.EvidenceBundleV1) (contracts.ActionLedgerV1, error) {
	return ensureRuntimeActionLedgerWithEligibility(req, taskState, plans, evidence, nil)
}

func ensureRuntimeActionLedgerWithEligibility(
	req RunInput,
	taskState runtimeTaskBatchState,
	plans []callbacks.ReplyTaskPlanTraceData,
	evidence *contracts.EvidenceBundleV1,
	eligibility *contracts.ResourceEligibilityV1,
) (contracts.ActionLedgerV1, error) {
	turnVersion := taskState.TurnVersion
	if turnVersion <= 0 {
		turnVersion = 1
	}
	ledger := contracts.ActionLedgerV1{SchemaVersion: contracts.ActionLedgerV1SchemaVersion, TurnVersion: turnVersion, Actions: []contracts.ActionLedgerItemV1{}}
	inputs := runtimeActionInputsWithEligibility(plans, evidence, eligibility, gateEnabled(gateResourceEligibility, req))
	if len(inputs) == 0 {
		return ledger, nil
	}
	if !taskState.Enabled || taskState.TurnID <= 0 {
		for _, input := range inputs {
			resourceType := input.ResourceType
			ledger.Actions = append(ledger.Actions, contracts.ActionLedgerItemV1{
				ActionKey: ephemeralRuntimeActionKey(input), TaskKey: input.TaskKey,
				ActionType: input.ActionType, ResourceType: &resourceType, Status: "requested", ResultCode: "",
			})
		}
		return ledger, nil
	}
	turn := repositories.AIReplyTurnRepository.GetInTenant(sqls.DB(), taskState.TurnID, req.Conversation.TenantID)
	if turn == nil || turn.Version != taskState.TurnVersion || turn.ConversationID != req.Conversation.ID {
		return contracts.ActionLedgerV1{}, services.ErrAIReplyTurnStale
	}
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		locked, err := repositories.AIReplyTurnRepository.GetForUpdateInTenant(ctx.Tx, turn.ID, turn.TenantID)
		if err != nil {
			return err
		}
		if locked == nil || locked.Version != turn.Version {
			return services.ErrAIReplyTurnStale
		}
		_, err = services.AIReplyTurnActionService.EnsureRequestedDB(ctx.Tx, locked, inputs)
		return err
	}); err != nil {
		return contracts.ActionLedgerV1{}, err
	}
	ledger.Actions = services.AIReplyTurnActionService.ContractsForTurn(sqls.DB(), turn.TenantID, turn.ID, turn.Version)
	return ledger, nil
}

func runtimeActionInputs(plans []callbacks.ReplyTaskPlanTraceData, evidence *contracts.EvidenceBundleV1, resourceGate bool) []services.AIReplyTurnActionInput {
	return runtimeActionInputsWithEligibility(plans, evidence, nil, resourceGate)
}

func runtimeActionInputsWithEligibility(
	plans []callbacks.ReplyTaskPlanTraceData,
	evidence *contracts.EvidenceBundleV1,
	eligibility *contracts.ResourceEligibilityV1,
	resourceGate bool,
) []services.AIReplyTurnActionInput {
	ret := make([]services.AIReplyTurnActionInput, 0)
	seen := make(map[string]struct{})
	add := func(input services.AIReplyTurnActionInput) {
		key := input.TaskKey + "\x1f" + input.ActionType + "\x1f" + input.ResourceType
		if input.TaskKey == "" || input.ActionType == "" {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		ret = append(ret, input)
	}
	for _, plan := range plans {
		actionType, resourceType := runtimeActionTypeFromPlan(plan)
		if actionType != "" {
			add(services.AIReplyTurnActionInput{TaskKey: plan.TaskKey, ActionType: actionType, ResourceType: resourceType})
		}
	}
	if evidence != nil && resourceGate && eligibility != nil {
		for _, item := range eligibility.Items {
			if item.Decision != "eligible" || item.ResourceType != "image" || item.ResourceRef == "" || item.TaskKey == "" {
				continue
			}
			if !runtimeEvidenceResourceBelongsToTask(evidence, item.ResourceRef, item.TaskKey) {
				continue
			}
			add(services.AIReplyTurnActionInput{
				TaskKey: item.TaskKey, ActionType: "send_knowledge_image", ResourceType: "image:" + item.ResourceRef,
				EligibilityFingerprint: runtimeV3JSONFingerprint(item), SourceEvidenceRef: item.SourceEvidenceRef,
				SourceRecordID: item.SourceRecordID, ResourcePurpose: item.ResourcePurpose,
				EligibilityReasonCode: item.ReasonCode,
			})
		}
	} else if evidence != nil && resourceGate {
		for _, resource := range evidence.Resources {
			if resource.Type != "image" {
				continue
			}
			for _, taskKey := range resource.TaskKeys {
				// ResourceEligibility Phase1（文档 10.3 规则 7）：地址文字类任务的
				// 资源诉求是文字，默认禁止自动附图，即使检索命中了带图记录。
				if planByTask := planByTaskKey(plans, taskKey); planByTask != nil && isAddressTextSubIntent(planByTask.SubIntent) {
					continue
				}
				add(services.AIReplyTurnActionInput{TaskKey: taskKey, ActionType: "send_knowledge_image", ResourceType: "image:" + resource.Ref})
			}
		}
	}
	return ret
}

func runtimeEvidenceResourceBelongsToTask(evidence *contracts.EvidenceBundleV1, resourceRef, taskKey string) bool {
	if evidence == nil {
		return false
	}
	for _, resource := range evidence.Resources {
		if resource.Ref == resourceRef && resource.Type == "image" && stringInSlice(taskKey, resource.TaskKeys) {
			return true
		}
	}
	return false
}

func planByTaskKey(plans []callbacks.ReplyTaskPlanTraceData, taskKey string) *callbacks.ReplyTaskPlanTraceData {
	for index := range plans {
		if plans[index].TaskKey == taskKey {
			return &plans[index]
		}
	}
	return nil
}

// isAddressTextSubIntent 判断子意图是否属于「要地址文字」类。
// 地址文字任务的 resource demand 是 text，图片默认禁止（文档 10.3）。
func isAddressTextSubIntent(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "address", "address_for_delivery", "delivery_address", "store_address", "order_food_delivery":
		return true
	default:
		return false
	}
}

func runtimeActionTypeFromPlan(plan callbacks.ReplyTaskPlanTraceData) (string, string) {
	switch strings.TrimSpace(plan.ResourceAction) {
	case "provide_location":
		return "send_location", "location"
	case "provide_mini_program", "send_miniprogram":
		return "send_mini_program", "mini_program"
	case "provide_phone":
		return "send_phone", "phone"
	}
	if runtimeTaskTypeForPlan(plan) == "human" {
		return "human_handoff", ""
	}
	return "", ""
}

func ephemeralRuntimeActionKey(input services.AIReplyTurnActionInput) string {
	sum := sha256.Sum256([]byte(input.TaskKey + "\n" + input.ActionType + "\n" + input.ResourceType))
	return "action_" + hex.EncodeToString(sum[:16])
}

func actionContractsFromModels(items []models.AIReplyTurnAction) []contracts.ActionLedgerItemV1 {
	ret := make([]contracts.ActionLedgerItemV1, 0, len(items))
	for _, item := range items {
		resourceType := item.ResourceType
		ret = append(ret, contracts.ActionLedgerItemV1{
			ActionKey: item.ActionKey, TaskKey: item.TaskKey, ActionType: item.ActionType, ResourceType: &resourceType,
			Status: item.Status, CommittedMessageID: item.CommittedMessageID, OutboxID: item.OutboxID, ResultCode: item.ResultCode,
		})
	}
	return ret
}

func validateActionLedgerContract(ledger contracts.ActionLedgerV1) error {
	raw, err := json.Marshal(ledger)
	if err != nil {
		return err
	}
	_, err = strictjson.DecodeObject[contracts.ActionLedgerV1](raw, strictjson.DecodeOptions{
		MaxBytes: 64 * 1024, Schema: contracts.MustSchema(contracts.SchemaActionLedgerV1),
	})
	if err != nil {
		return fmt.Errorf("action ledger contract: %w", err)
	}
	return nil
}
