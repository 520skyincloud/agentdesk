package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/knowledgepolicy"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/strictjson"
)

type runtimeV3PlanArtifacts struct {
	Plan               contracts.ReplyPlanV4
	Decisions          map[string]CapabilityDecisionV1
	EvidenceByTask     map[string]TaskEvidenceResultView
	AuthoritativeFacts []contracts.RuntimeContextFactV2
	SelectedTaskKeys   []string
	DeferredTaskKeys   []string
}

func buildRuntimeV3PlanArtifacts(
	req RunInput,
	taskState runtimeTaskBatchState,
	plans []callbacks.ReplyTaskPlanTraceData,
	knowledgeByTask map[string]AnswerabilityOutcome,
	evidence contracts.EvidenceBundleV2,
	eligibility contracts.ResourceEligibilityV1,
	ledger contracts.ActionLedgerV1,
	observations []contracts.ObservationV1,
) (runtimeV3PlanArtifacts, error) {
	turnVersion := taskState.TurnVersion
	if turnVersion <= 0 {
		turnVersion = req.UserMessage.AIReplyTurnVersion
	}
	if turnVersion <= 0 {
		turnVersion = 1
	}
	turnID := taskState.TurnID
	if turnID <= 0 {
		turnID = req.UserMessage.AIReplyTurnID
	}

	configs := loadEnabledIntentConfigs(resolveRuntimeIntentScope(req))
	configByCode := make(map[string]models.ReplyIntentConfig, len(configs))
	for _, config := range configs {
		if config.Status == enums.StatusOk && strings.TrimSpace(config.Code) != "" {
			configByCode[strings.TrimSpace(config.Code)] = config
		}
	}
	actionRefsByTask, actionFingerprints := runtimeV3ActionViews(ledger)
	evidenceByTask, requiredFactsByTask, authoritativeFacts := runtimeV3EvidenceViews(plans, knowledgeByTask, evidence)

	tasks := make([]TaskRuntimeView, 0, len(plans))
	decisions := make(map[string]CapabilityDecisionV1, len(plans))
	decisionViews := make(map[string]CapabilityDecisionView, len(plans))
	for index, plan := range plans {
		sequence := plan.Sequence
		if sequence <= 0 {
			sequence = index + 1
		}
		view := TaskRuntimeView{
			TurnID: turnID, TaskKey: plan.TaskKey, Sequence: sequence,
			Intent: strings.TrimSpace(plan.Intent), SubIntent: strings.TrimSpace(plan.SubIntent),
			SourceText:          strings.TrimSpace(plan.Text),
			ObservationBindings: append([]callbacks.TaskObservationBindingTraceData(nil), plan.ObservationBindings...),
		}
		if view.TaskKey == "" {
			return runtimeV3PlanArtifacts{}, fmt.Errorf("reply_plan.v4 task key is empty")
		}
		requestMode := normalizedRuntimeV3RequestMode(plan)
		unit := QuestionUnit{
			QuestionKey: plan.QuestionUnitKey, Sequence: sequence, Intent: view.Intent,
			SubIntent: view.SubIntent, RequestMode: requestMode, Text: plan.Text,
		}
		policy := runtimeV3CapabilityPolicy(plan, configByCode[view.Intent])
		decision, err := DeriveCapabilityDecision(unit, policy)
		if err != nil {
			return runtimeV3PlanArtifacts{}, fmt.Errorf("derive capability for %s: %w", view.TaskKey, err)
		}
		decision.TaskKey = view.TaskKey
		decisions[view.TaskKey] = decision
		decisionViews[view.TaskKey] = CapabilityDecisionView{
			TaskKey: view.TaskKey, Route: decision.Route,
			MissingFieldSet:   strings.Join(decision.MissingFields, ","),
			ClarificationGoal: decision.ReasonCode,
		}
		tasks = append(tasks, view)
	}
	observationRefsByTask, err := runtimeV3ObservationRefsByTask(tasks, observations)
	if err != nil {
		return runtimeV3PlanArtifacts{}, err
	}

	groups := BuildFinalAnswerGroups(turnID, tasks, decisionViews, evidenceByTask, MapActionLedgerView(actionFingerprints))
	plan, err := BuildReplyPlanV4(ReplyPlanBuildInput{
		TurnID: turnID, TurnVersion: turnVersion, Tasks: tasks, Decisions: decisions, Groups: groups,
		EvidenceByTask: evidenceByTask, ObservationRefsByTask: observationRefsByTask, ActionRefsByTask: actionRefsByTask,
		RequiredFactsByTask: requiredFactsByTask, ScopeFingerprint: evidence.ScopeFingerprint,
		FactSnapshotFingerprint:        runtimeV3JSONFingerprint(authoritativeFacts),
		ResourceEligibilityFingerprint: runtimeV3JSONFingerprint(eligibility),
		ActionLedgerFingerprint:        runtimeV3JSONFingerprint(ledger),
		PromptPolicyRevisions:          fmt.Sprintf("intent:%d/reply:v3/validator:v3", runtimeIntentProfileRevision(req)),
	})
	if err != nil {
		return runtimeV3PlanArtifacts{}, err
	}
	if err := validateRuntimeReplyPlanV4Contract(plan, evidence, ledger, observations); err != nil {
		return runtimeV3PlanArtifacts{}, err
	}
	selected := make(map[string]struct{}, len(plan.Tasks))
	selectedKeys := make([]string, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		selected[task.TaskKey] = struct{}{}
		selectedKeys = append(selectedKeys, task.TaskKey)
	}
	deferred := make([]string, 0)
	for _, task := range tasks {
		decision := decisions[task.TaskKey]
		if FinalOutputMode(decision.Route, len(actionRefsByTask[task.TaskKey]) > 0) != "text" {
			continue
		}
		if _, ok := selected[task.TaskKey]; !ok {
			deferred = append(deferred, task.TaskKey)
		}
	}
	return runtimeV3PlanArtifacts{
		Plan: plan, Decisions: decisions, EvidenceByTask: evidenceByTask,
		AuthoritativeFacts: authoritativeFacts, SelectedTaskKeys: selectedKeys, DeferredTaskKeys: deferred,
	}, nil
}

func normalizedRuntimeV3RequestMode(plan callbacks.ReplyTaskPlanTraceData) string {
	if mode := strings.TrimSpace(plan.RequestMode); mode != "" {
		return mode
	}
	switch {
	case plan.Intent == "interaction":
		return "social"
	case runtimeTaskTypeForPlan(plan) == enums.AIReplyTurnTaskTypeResource,
		runtimeTaskTypeForPlan(plan) == enums.AIReplyTurnTaskTypeHuman,
		plan.Intent == "service_request":
		return "request_action"
	default:
		return "answer"
	}
}

func runtimeV3CapabilityPolicy(plan callbacks.ReplyTaskPlanTraceData, config models.ReplyIntentConfig) CapabilityPolicy {
	policy := CapabilityPolicy{
		IntentCode:       strings.TrimSpace(plan.Intent),
		NeedsKnowledge:   runtimeTaskTypeForPlan(plan) == enums.AIReplyTurnTaskTypeKnowledge,
		NeedsHumanRoute:  config.NeedsHumanRoute && strings.TrimSpace(plan.Intent) == "human_complaint_risk",
		HumanRoutePolicy: strings.TrimSpace(config.HumanRoutePolicy),
		ToolCodes:        splitIntentTerms(config.ToolCodes), CollectedFields: map[string]string{},
	}
	if runtimeTaskTypeForPlan(plan) == enums.AIReplyTurnTaskTypeResource {
		policy.ToolCodes = appendUniqueStrings(policy.ToolCodes, "structured_resource:"+strings.TrimSpace(plan.ResourceAction))
	}
	if runtimeTaskTypeForPlan(plan) == enums.AIReplyTurnTaskTypeHuman {
		policy.NeedsHumanRoute = true
		if policy.HumanRoutePolicy == "" {
			policy.HumanRoutePolicy = "managed_mode"
		}
	}
	return policy
}

func runtimeV3ActionViews(ledger contracts.ActionLedgerV1) (map[string][]string, map[string]string) {
	refs := make(map[string][]string)
	fingerprints := make(map[string]string)
	byTask := make(map[string][]contracts.ActionLedgerItemV1)
	for _, action := range ledger.Actions {
		if action.Status != "requested" && action.Status != "prepared" {
			continue
		}
		refs[action.TaskKey] = appendUniqueStrings(refs[action.TaskKey], action.ActionKey)
		byTask[action.TaskKey] = append(byTask[action.TaskKey], action)
	}
	for taskKey, actions := range byTask {
		sort.SliceStable(actions, func(i, j int) bool { return actions[i].ActionKey < actions[j].ActionKey })
		fingerprints[taskKey] = runtimeV3JSONFingerprint(actions)
	}
	return refs, fingerprints
}

func runtimeV3EvidenceViews(
	plans []callbacks.ReplyTaskPlanTraceData,
	knowledgeByTask map[string]AnswerabilityOutcome,
	evidence contracts.EvidenceBundleV2,
) (map[string]TaskEvidenceResultView, map[string][]string, []contracts.RuntimeContextFactV2) {
	views := make(map[string]TaskEvidenceResultView, len(plans))
	requiredFacts := make(map[string][]string, len(plans))
	authoritativeFacts := make([]contracts.RuntimeContextFactV2, 0)
	for _, item := range evidence.Items {
		if item.SourceType == "store_fact" && strings.HasPrefix(item.Ref, "S") && strings.TrimSpace(item.FactKey) != "" && strings.TrimSpace(item.Content) != "" {
			authoritativeFacts = append(authoritativeFacts, contracts.RuntimeContextFactV2{Ref: item.Ref, Key: item.FactKey, Value: item.Content})
			for _, taskKey := range item.TaskKeys {
				requiredFacts[taskKey] = appendUniqueStrings(requiredFacts[taskKey], item.Ref)
			}
		}
	}
	for _, plan := range plans {
		outcome := knowledgeByTask[plan.TaskKey]
		view := TaskEvidenceResultView{
			Status: "not_needed", EvidenceRefs: []string{}, AuthoritativeFacts: requiredFacts[plan.TaskKey],
			ClaimType: knowledgepolicy.InferClaimType(plan.Text, "", ""),
		}
		if runtimeTaskTypeForPlan(plan) == enums.AIReplyTurnTaskTypeKnowledge {
			switch outcome.Status {
			case "has_context":
				view.Status = "approved"
			case "unavailable":
				view.Status = "unavailable"
			case "unanswerable":
				view.Status = "blocked"
			default:
				view.Status = "no_context"
			}
		}
		items := make([]contracts.EvidenceItemV2, 0)
		for _, item := range evidence.Items {
			if !stringInSlice(plan.TaskKey, item.TaskKeys) {
				continue
			}
			if item.ClaimType != "" && item.ClaimType != "meta" {
				view.ClaimType = item.ClaimType
			}
			if item.Answerability != "supporting" ||
				item.TopicMatch != "exact" || !stringInSlice("answer_text", item.AllowedUses) {
				continue
			}
			view.EvidenceRefs = appendUniqueStrings(view.EvidenceRefs, item.Ref)
			items = append(items, item)
		}
		if view.Status == "approved" && len(view.EvidenceRefs) == 0 {
			view.Status = "no_context"
		}
		if len(items) > 0 {
			view.Answerability = "supporting"
			view.Fingerprint = runtimeV3JSONFingerprint(items)
		}
		views[plan.TaskKey] = view
	}
	sort.SliceStable(authoritativeFacts, func(i, j int) bool { return authoritativeFacts[i].Ref < authoritativeFacts[j].Ref })
	return views, requiredFacts, authoritativeFacts
}

func validateRuntimeReplyPlanV4Contract(plan contracts.ReplyPlanV4, evidence contracts.EvidenceBundleV2, ledger contracts.ActionLedgerV1, observations []contracts.ObservationV1) error {
	raw, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("marshal reply_plan.v4: %w", err)
	}
	if _, err := strictjson.DecodeObject[contracts.ReplyPlanV4](raw, strictjson.DecodeOptions{
		MaxBytes: 96 * 1024, Schema: contracts.MustSchema(contracts.SchemaReplyPlanV4),
	}); err != nil {
		return fmt.Errorf("validate reply_plan.v4: %w", err)
	}
	evidenceRaw, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("marshal evidence_bundle.v2: %w", err)
	}
	if _, err := strictjson.DecodeObject[contracts.EvidenceBundleV2](evidenceRaw, strictjson.DecodeOptions{
		MaxBytes: 256 * 1024, Schema: contracts.MustSchema(contracts.SchemaEvidenceBundleV2),
	}); err != nil {
		return fmt.Errorf("validate evidence_bundle.v2: %w", err)
	}
	evidenceRefs := make(map[string]contracts.EvidenceItemV2, len(evidence.Items))
	for _, item := range evidence.Items {
		evidenceRefs[item.Ref] = item
	}
	actionRefs := make(map[string]contracts.ActionLedgerItemV1, len(ledger.Actions))
	for _, action := range ledger.Actions {
		actionRefs[action.ActionKey] = action
	}
	observationRefs := make(map[string]contracts.ObservationV1, len(observations))
	for _, observation := range observations {
		observationRefs[observation.Ref] = observation
	}
	for _, task := range plan.Tasks {
		if task.Knowledge.Policy == "required" && task.Knowledge.Status == "has_context" && len(task.EvidenceRefs) == 0 {
			return fmt.Errorf("reply_plan.v4 knowledge task %s lacks evidence", task.TaskKey)
		}
		for _, ref := range task.EvidenceRefs {
			item, ok := evidenceRefs[ref]
			if !ok || item.Answerability != "supporting" || item.TopicMatch != "exact" || !stringInSlice(task.TaskKey, item.TaskKeys) {
				return fmt.Errorf("reply_plan.v4 evidence ref %s is outside task %s", ref, task.TaskKey)
			}
		}
		for _, ref := range task.ActionRefs {
			action, ok := actionRefs[ref]
			if !ok || action.TaskKey != task.TaskKey || (action.Status != "requested" && action.Status != "prepared") {
				return fmt.Errorf("reply_plan.v4 action ref %s is outside task %s", ref, task.TaskKey)
			}
		}
		for _, ref := range task.ObservationRefs {
			observation, ok := observationRefs[ref]
			if !ok || observation.Status != "ready" || strings.TrimSpace(observation.Text) == "" {
				return fmt.Errorf("reply_plan.v4 observation ref %s is unavailable for task %s", ref, task.TaskKey)
			}
		}
	}
	return nil
}

func runtimeV3ObservationRefsByTask(tasks []TaskRuntimeView, observations []contracts.ObservationV1) (map[string][]string, error) {
	refsBySource := make(map[string][]string, len(observations))
	for _, observation := range observations {
		if observation.Status != "ready" || strings.TrimSpace(observation.Text) == "" || !strings.HasPrefix(observation.Ref, "O") {
			continue
		}
		revision := observation.SourceRevision
		if revision <= 0 {
			revision = 1
		}
		key := fmt.Sprintf("%d/%d", observation.SourceMessageID, revision)
		refsBySource[key] = appendUniqueStrings(refsBySource[key], observation.Ref)
	}
	ret := make(map[string][]string, len(tasks))
	for _, task := range tasks {
		refs := make([]string, 0, len(task.ObservationBindings))
		for _, binding := range task.ObservationBindings {
			if binding.MessageID <= 0 || binding.SourceRevision <= 0 {
				return nil, fmt.Errorf("reply_plan.v4 task %s has invalid observation binding", task.TaskKey)
			}
			key := fmt.Sprintf("%d/%d", binding.MessageID, binding.SourceRevision)
			bound := refsBySource[key]
			if len(bound) == 0 {
				return nil, fmt.Errorf("reply_plan.v4 task %s waits for observation %s", task.TaskKey, key)
			}
			refs = appendUniqueStrings(refs, bound...)
		}
		ret[task.TaskKey] = refs
	}
	return ret, nil
}

func runtimeV3JSONFingerprint(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func runtimeV3ObservationsFromEnvelope(envelope contextcompiler.TurnInputEnvelope) []contracts.ObservationV1 {
	ret := make([]contracts.ObservationV1, 0, len(envelope.Observations))
	for _, item := range envelope.Observations {
		revision := item.SourceRevision
		if revision <= 0 {
			revision = 1
		}
		ret = append(ret, contracts.ObservationV1{
			Ref: item.Ref, SourceMessageID: item.MessageID, SourceRevision: revision,
			Status: item.Status, SourceType: item.SourceType, ObservationType: item.ObservationType,
			Text: boundedEvidenceText(item.Text, 4000), Confidence: item.Confidence,
			AllowedUses: nonNilStrings(item.AllowedUses), ForbiddenUses: nonNilStrings(item.ForbiddenUses),
		})
	}
	return ret
}
