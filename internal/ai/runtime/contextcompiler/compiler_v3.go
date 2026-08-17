package contextcompiler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/strictjson"

	"github.com/cloudwego/eino/schema"
)

// compileGenerateV3 is intentionally stricter than the V2 compiler. Generate
// receives the current task plan, supporting evidence, bounded observations and
// authoritative facts. Raw prior assistant answers and compressed business facts
// are not replayed into the model, so an earlier hallucination cannot become a
// new fact merely by appearing in conversation history.
func (c *Compiler) compileGenerateV3(input CompileInput, budget Budget, estimator TokenEstimator) (compiledStageResult, error) {
	if input.ReplyPlanV4 == nil || input.ReplyPlanV4.SchemaVersion != contracts.ReplyPlanV4SchemaVersion {
		return compiledStageResult{}, fmt.Errorf("reply contract v3 requires reply_plan.v4")
	}
	if input.EvidenceV2 == nil || input.EvidenceV2.SchemaVersion != contracts.EvidenceBundleV2SchemaVersion {
		return compiledStageResult{}, fmt.Errorf("reply contract v3 requires evidence_bundle.v2")
	}

	policyMessage := schema.SystemMessage(buildGeneratePolicy(input))
	repairMessage := buildRepairMessage(input.RepairInstruction)
	current, err := generateTaskInputTextV1(input.ReplyPlanV4)
	if err != nil {
		return compiledStageResult{}, err
	}
	currentMessage := schema.UserMessage(current)

	snapshot := buildRuntimeContextSnapshotV2(input)
	stateMessage, err := snapshotSystemMessageV2(snapshot)
	if err != nil {
		return compiledStageResult{}, err
	}

	requiredTaskKeys := requiredEvidenceTaskKeysV3(input.ReplyPlanV4)
	selectedEvidence, complete := selectRequiredEvidenceV2(input.EvidenceV2, requiredTaskKeys)
	if !complete {
		return compiledStageResult{}, fmt.Errorf("%w: at least one V3 knowledge task lacks exact supporting evidence", ErrRequiredEvidenceOverflow)
	}
	projectedEvidence, evidenceMessage, evidenceTokens, pruned, err := fitRequiredEvidenceV2(
		input, budget, estimator, policyMessage, stateMessage, currentMessage, selectedEvidence,
	)
	if err != nil {
		return compiledStageResult{}, err
	}

	messages := assembleGenerateMessages(policyMessage, stateMessage, nil, evidenceMessage, nil, repairMessage, currentMessage)
	if estimated := estimator.CountMessages(input.Model.ModelName, messages); estimated > budget.AvailableInput {
		return compiledStageResult{}, fmt.Errorf("%w: generate v3 mandatory=%d available=%d", ErrMandatoryContextOverflow, estimated, budget.AvailableInput)
	}
	return compiledStageResult{
		messages:            messages,
		fingerprintMessages: assembleGenerateMessages(policyMessage, stateMessage, nil, evidenceMessage, nil, nil, currentMessage),
		categoryTokens: map[string]int{
			"stable_policy":     estimator.CountMessages(input.Model.ModelName, []*schema.Message{policyMessage}),
			"runtime_state_v2":  estimator.CountMessages(input.Model.ModelName, []*schema.Message{stateMessage}),
			"required_evidence": evidenceTokens,
			"protocol_repair":   estimator.CountMessages(input.Model.ModelName, nonNilMessages(repairMessage)),
			"current_turn":      estimator.CountMessages(input.Model.ModelName, []*schema.Message{currentMessage}),
		},
		pruned: pruned, evidenceV2: projectedEvidence,
	}, nil
}

const generateTaskInputV1SchemaVersion = contracts.SchemaGenerateTaskInputV1

type generateTaskInputV1 struct {
	SchemaVersion string                    `json:"schemaVersion"`
	Tasks         []generateTaskInputTaskV1 `json:"tasks"`
}

type generateTaskInputTaskV1 struct {
	TaskKey         string `json:"taskKey"`
	Sequence        int    `json:"sequence"`
	AnswerGroupKey  string `json:"answerGroupKey"`
	CustomerRequest string `json:"customerRequest"`
}

// generateTaskInputTextV1 projects only the tasks selected into reply_plan.v4.
// It deliberately ignores CompileInput.CurrentMessages: that slice is retained
// for scope validation and V1/V2 compatibility, but replaying it here would let
// completed or deferred questions leak back into this Generate call.
func generateTaskInputTextV1(plan *contracts.ReplyPlanV4) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("%w: reply_plan.v4 is missing", ErrMandatoryContextOverflow)
	}
	activeGroups := make(map[string]struct{}, len(plan.ReplyGroups))
	for _, group := range plan.ReplyGroups {
		if group.Required && group.OutputMode == "text" {
			activeGroups[group.GroupKey] = struct{}{}
		}
	}
	tasks := make([]generateTaskInputTaskV1, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if _, active := activeGroups[task.AnswerGroupKey]; !active || task.OutputMode != "text" {
			continue
		}
		request := strings.TrimSpace(task.Objective)
		if task.TaskKey == "" || task.Sequence <= 0 || request == "" {
			return "", fmt.Errorf("%w: V3 selected task lacks verified customer request", ErrMandatoryContextOverflow)
		}
		tasks = append(tasks, generateTaskInputTaskV1{
			TaskKey: task.TaskKey, Sequence: task.Sequence, AnswerGroupKey: task.AnswerGroupKey, CustomerRequest: request,
		})
	}
	if len(tasks) == 0 {
		return "", fmt.Errorf("%w: V3 selected task input is empty", ErrMandatoryContextOverflow)
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Sequence == tasks[j].Sequence {
			return tasks[i].TaskKey < tasks[j].TaskKey
		}
		return tasks[i].Sequence < tasks[j].Sequence
	})
	raw, err := json.Marshal(generateTaskInputV1{SchemaVersion: generateTaskInputV1SchemaVersion, Tasks: tasks})
	if err != nil {
		return "", fmt.Errorf("marshal generate task input v1: %w", err)
	}
	if _, err := strictjson.DecodeObject[generateTaskInputV1](raw, strictjson.DecodeOptions{
		MaxBytes: 16 * 1024,
		Schema:   contracts.MustSchema(contracts.SchemaGenerateTaskInputV1),
	}); err != nil {
		return "", fmt.Errorf("validate generate task input v1: %w", err)
	}
	return string(raw), nil
}

func buildRuntimeContextSnapshotV2(input CompileInput) contracts.RuntimeContextSnapshotV2 {
	mode := "ai_serving"
	if input.Resume {
		mode = "resume"
	}
	dialogueAct := normalizeV3DialogueAct(input.DialogueAct)
	focus := contracts.RuntimeContextFocus{RelationToPrior: "unknown", ResolvedTaskKeys: []string{}}
	if input.DialogueState != nil {
		focus.Topic = boundedText(input.DialogueState.Focus.Topic, 120)
		focus.RelationToPrior = normalizeDialogueRelation(input.DialogueState.Focus.RelationToPrior)
		for _, resolved := range input.DialogueState.ResolvedTasks {
			focus.ResolvedTaskKeys = appendUnique(focus.ResolvedTaskKeys, boundedText(resolved.TaskKey, 128))
			if len(focus.ResolvedTaskKeys) >= 12 {
				break
			}
		}
	}
	if dialogueAct == "unknown" && focus.RelationToPrior != "unknown" {
		dialogueAct = focus.RelationToPrior
	}

	tasks := make([]contracts.RuntimeContextTaskV2, 0, len(input.ReplyPlanV4.Tasks))
	activeGroups := make(map[string]struct{}, len(input.ReplyPlanV4.ReplyGroups))
	for _, group := range input.ReplyPlanV4.ReplyGroups {
		if group.Required {
			activeGroups[group.GroupKey] = struct{}{}
		}
	}
	for _, task := range input.ReplyPlanV4.Tasks {
		if _, active := activeGroups[task.AnswerGroupKey]; !active || task.OutputMode != "text" {
			continue
		}
		outputMode := "text"
		if containsString(task.Constraints, "ask_one_missing_field") {
			outputMode = "clarification"
		} else if len(task.ActionRefs) > 0 {
			outputMode = "text_and_resource"
		}
		tasks = append(tasks, contracts.RuntimeContextTaskV2{
			TaskKey: task.TaskKey, Sequence: task.Sequence, Objective: boundedText(task.Objective, 500),
			ClaimType:  task.ClaimType,
			OutputMode: outputMode, KnowledgeStatus: normalizeRuntimeKnowledgeStatus(task.Knowledge.Status),
			EvidenceRefs: append([]string{}, task.EvidenceRefs...), ObservationRefs: append([]string{}, task.ObservationRefs...),
			RequiredFactRefs: append([]string{}, task.RequiredFactRefs...),
			ActionRefs:       append([]string{}, task.ActionRefs...), Constraints: append([]string{}, task.Constraints[:min(len(task.Constraints), 8)]...),
		})
	}

	prepared := make([]contracts.RuntimeContextPreparedAction, 0, len(input.PreparedActions))
	for _, action := range input.PreparedActions {
		if action.Status != "prepared" || action.ActionKey == "" || action.TaskKey == "" || !runtimeVisibleActionType(action.ActionType) {
			continue
		}
		prepared = append(prepared, contracts.RuntimeContextPreparedAction{
			ActionRef: action.ActionKey, TaskKey: action.TaskKey, Type: action.ActionType, Status: "prepared",
		})
		if len(prepared) >= 8 {
			break
		}
	}

	facts := make([]contracts.RuntimeContextFactV2, 0, len(input.AuthoritativeFacts))
	for _, fact := range input.AuthoritativeFacts {
		fact.Ref = boundedText(fact.Ref, 16)
		fact.Key = boundedText(fact.Key, 120)
		fact.Value = boundedText(fact.Value, 1000)
		if !strings.HasPrefix(fact.Ref, "S") || fact.Key == "" || fact.Value == "" {
			continue
		}
		facts = append(facts, fact)
		if len(facts) >= 16 {
			break
		}
	}

	maxParts := input.ReplyPlanV4.GlobalConstraints.MaxReplyParts
	if maxParts < 1 {
		maxParts = 1
	}
	if maxParts > 3 {
		maxParts = 3
	}
	return contracts.RuntimeContextSnapshotV2{
		SchemaVersion: contracts.SchemaRuntimeContextSnapshotV2, ConversationMode: mode,
		DialogueAct: dialogueAct, Focus: focus, Tasks: tasks,
		Observations: buildRuntimeContextObservationsV2(input), Facts: facts, PreparedActions: prepared,
		ResponsePolicy: contracts.RuntimeContextResponsePolicyV2{
			MaxParts: maxParts, Style: "concise_wechat_service", MustNotMentionInternalState: true,
			MustNotClaimUncommittedActions: true, MustCiteProtectedFacts: true,
		},
	}
}

func buildRuntimeContextObservationsV2(input CompileInput) []contracts.RuntimeContextObservationV2 {
	ret := make([]contracts.RuntimeContextObservationV2, 0, len(input.Observations)+2)
	allowedRefs := activeV3ObservationRefs(input.ReplyPlanV4)
	for _, observation := range input.Observations {
		if len(ret) >= 16 {
			break
		}
		if observation.Status != "ready" || strings.TrimSpace(observation.Text) == "" {
			continue
		}
		if _, allowed := allowedRefs[observation.Ref]; !allowed {
			continue
		}
		allowed := make([]string, 0, 2)
		if containsString(observation.AllowedUses, "describe_media") {
			allowed = append(allowed, "describe_media")
		}
		if containsString(observation.AllowedUses, "resolve_reference") {
			allowed = append(allowed, "resolve_reference")
		}
		if len(allowed) == 0 {
			allowed = []string{"context_only"}
		}
		ret = append(ret, contracts.RuntimeContextObservationV2{
			Ref: observation.Ref, SourceClass: "customer_media_ocr",
			SourceID: fmt.Sprintf("message:%d/revision:%d", observation.SourceMessageID, observation.SourceRevision),
			Speaker:  "customer", Content: boundedText(observation.Text, 1000), AllowedUses: allowed,
			ForbiddenUses: []string{"assert_store_fact", "recommend", "send_resource", "prepare_resource"},
		})
	}
	if !v3HistoryReferenceAllowed(input) {
		return ret
	}
	items := append([]models.Message(nil), input.RecentHistory...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].SeqNo == items[j].SeqNo {
			return items[i].ID < items[j].ID
		}
		return items[i].SeqNo < items[j].SeqNo
	})
	historySeq := 0
	for index := len(items) - 1; index >= 0 && historySeq < 2; index-- {
		item := items[index]
		if item.MessageType != enums.IMMessageTypeText && item.MessageType != enums.IMMessageTypeHTML {
			continue
		}
		content := boundedText(item.Content, 1000)
		if content == "" {
			continue
		}
		speaker := "system"
		switch item.SenderType {
		case enums.IMSenderTypeCustomer:
			speaker = "customer"
		case enums.IMSenderTypeAI:
			speaker = "ai"
		case enums.IMSenderTypeAgent:
			speaker = "agent"
		default:
			continue
		}
		historySeq++
		ret = append(ret, contracts.RuntimeContextObservationV2{
			Ref: fmt.Sprintf("M%d", historySeq), SourceClass: "conversation_history",
			SourceID: fmt.Sprintf("message:%d", item.ID), Speaker: speaker, Content: content,
			AllowedUses:   []string{"resolve_reference", "context_only"},
			ForbiddenUses: []string{"assert_store_fact", "recommend", "send_resource", "answer_text", "prepare_resource"},
		})
	}
	return ret
}

func activeV3ObservationRefs(plan *contracts.ReplyPlanV4) map[string]struct{} {
	ret := make(map[string]struct{})
	if plan == nil {
		return ret
	}
	activeGroups := make(map[string]struct{}, len(plan.ReplyGroups))
	for _, group := range plan.ReplyGroups {
		if group.Required && group.OutputMode == "text" {
			activeGroups[group.GroupKey] = struct{}{}
		}
	}
	for _, task := range plan.Tasks {
		if _, active := activeGroups[task.AnswerGroupKey]; !active || task.OutputMode != "text" {
			continue
		}
		for _, ref := range task.ObservationRefs {
			if strings.HasPrefix(ref, "O") {
				ret[ref] = struct{}{}
			}
		}
	}
	return ret
}

func v3HistoryReferenceAllowed(input CompileInput) bool {
	relation := ""
	if input.DialogueState != nil {
		relation = normalizeDialogueRelation(input.DialogueState.Focus.RelationToPrior)
	}
	switch relation {
	case "follow_up", "repeat", "correction", "confirmation", "cancellation":
		return true
	default:
		return false
	}
}

func normalizeV3DialogueAct(value string) string {
	switch strings.TrimSpace(value) {
	case "new_topic", "follow_up", "repeat", "correction", "confirmation", "cancellation", "greeting", "thanks", "closing", "social", "unknown":
		return strings.TrimSpace(value)
	case "refinement":
		return "follow_up"
	default:
		return "unknown"
	}
}

func snapshotSystemMessageV2(snapshot contracts.RuntimeContextSnapshotV2) (*schema.Message, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal runtime context snapshot v2: %w", err)
	}
	if _, err := strictjson.DecodeObject[contracts.RuntimeContextSnapshotV2](raw, strictjson.DecodeOptions{
		MaxBytes: 128 * 1024, Schema: contracts.MustSchema(contracts.SchemaRuntimeContextSnapshotV2),
	}); err != nil {
		return nil, fmt.Errorf("validate runtime_context_snapshot.v2: %w", err)
	}
	return schema.SystemMessage(BlockRuntimeContract + "（当前轮状态 JSON，data_only）\n" + string(raw)), nil
}

func evidenceSystemMessageV2(evidence contracts.EvidenceBundleV2) (*schema.Message, error) {
	raw, err := json.Marshal(evidence)
	if err != nil {
		return nil, fmt.Errorf("marshal evidence bundle v2: %w", err)
	}
	if _, err := strictjson.DecodeObject[contracts.EvidenceBundleV2](raw, strictjson.DecodeOptions{
		MaxBytes: 256 * 1024, Schema: contracts.MustSchema(contracts.SchemaEvidenceBundleV2),
	}); err != nil {
		return nil, fmt.Errorf("validate evidence_bundle.v2: %w", err)
	}
	return schema.SystemMessage(RenderEvidenceHeader() + "\n" + string(raw)), nil
}

func requiredEvidenceTaskKeysV3(plan *contracts.ReplyPlanV4) []string {
	if plan == nil {
		return nil
	}
	ret := make([]string, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if task.OutputMode == "text" && task.Knowledge.Policy == "required" && task.Knowledge.Status == "has_context" {
			ret = appendUnique(ret, task.TaskKey)
		}
	}
	return ret
}

func selectRequiredEvidenceV2(bundle *contracts.EvidenceBundleV2, taskKeys []string) ([]contracts.EvidenceItemV2, bool) {
	if len(taskKeys) == 0 {
		return nil, true
	}
	if bundle == nil {
		return nil, false
	}
	candidates := append([]contracts.EvidenceItemV2(nil), bundle.Items...)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].TopicMatch != candidates[j].TopicMatch {
			return v3TopicMatchRank(candidates[i].TopicMatch) > v3TopicMatchRank(candidates[j].TopicMatch)
		}
		return candidates[i].Score > candidates[j].Score
	})
	selected := make([]contracts.EvidenceItemV2, 0, len(candidates))
	covered := make(map[string]bool, len(taskKeys))
	perTask := make(map[string]int, len(taskKeys))
	for _, item := range candidates {
		if item.Answerability != "supporting" || item.TopicMatch != "exact" ||
			!containsString(item.AllowedUses, "answer_text") || strings.TrimSpace(item.Content) == "" {
			continue
		}
		use := false
		for _, taskKey := range taskKeys {
			if containsString(item.TaskKeys, taskKey) && perTask[taskKey] < 6 {
				covered[taskKey] = true
				perTask[taskKey]++
				use = true
			}
		}
		if use {
			item.ResourceRefs = []string{}
			selected = append(selected, item)
		}
	}
	for _, taskKey := range taskKeys {
		if !covered[taskKey] {
			return selected, false
		}
	}
	return selected, true
}

func fitRequiredEvidenceV2(
	input CompileInput,
	budget Budget,
	estimator TokenEstimator,
	policyMessage, stateMessage, currentMessage *schema.Message,
	items []contracts.EvidenceItemV2,
) (*contracts.EvidenceBundleV2, *schema.Message, int, []PrunedContextItem, error) {
	if len(items) == 0 {
		return nil, nil, 0, nil, nil
	}
	working := append([]contracts.EvidenceItemV2(nil), items...)
	pruned := make([]PrunedContextItem, 0)
	evidenceCap := min(4000, budget.AvailableInput*45/100)
	for {
		projected := contracts.EvidenceBundleV2{
			SchemaVersion:    contracts.EvidenceBundleV2SchemaVersion,
			ScopeFingerprint: input.EvidenceV2.ScopeFingerprint,
			RetrievalStatus:  input.EvidenceV2.RetrievalStatus,
			Items:            append([]contracts.EvidenceItemV2(nil), working...), Resources: []contracts.EvidenceResourceV2{},
		}
		message, err := evidenceSystemMessageV2(projected)
		if err != nil {
			return nil, nil, 0, pruned, err
		}
		tokens := estimator.CountMessages(input.Model.ModelName, []*schema.Message{message})
		allTokens := compiledGenerateTokenCount(estimator, input.Model.ModelName, policyMessage, stateMessage, nil, message, nil, buildRepairMessage(input.RepairInstruction), currentMessage)
		if tokens <= evidenceCap && allTokens <= budget.AvailableInput {
			return &projected, message, tokens, pruned, nil
		}
		longest := -1
		longestRunes := 0
		for index, item := range working {
			if length := len([]rune(item.Content)); length > longestRunes && length > 96 {
				longest = index
				longestRunes = length
			}
		}
		if longest < 0 {
			return nil, nil, 0, pruned, fmt.Errorf("%w: required V3 evidence cannot fit within budget", ErrRequiredEvidenceOverflow)
		}
		before := estimator.CountText(input.Model.ModelName, working[longest].Content)
		working[longest].Content = truncateV3EvidenceContent(working[longest].Content, max(longestRunes*3/4, 96))
		after := estimator.CountText(input.Model.ModelName, working[longest].Content)
		pruned = append(pruned, PrunedContextItem{Category: "evidence_v2", ItemRef: working[longest].Ref, Reason: "required_evidence_sentence_truncated", EstimatedTokens: max(before-after, 0)})
	}
}

func truncateV3EvidenceContent(content string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	cut := maxRunes
	for index := maxRunes - 1; index >= maxRunes/2; index-- {
		switch runes[index] {
		case '。', '！', '？', '.', '!', '?', '\n':
			cut = index + 1
			index = -1
		}
	}
	return strings.TrimSpace(string(runes[:cut]))
}

func v3TopicMatchRank(value string) int {
	switch value {
	case "exact":
		return 3
	case "related":
		return 2
	default:
		return 1
	}
}
