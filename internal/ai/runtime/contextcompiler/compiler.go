package contextcompiler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"

	"github.com/cloudwego/eino/schema"
)

var ErrRuntimeScopeMismatch = errors.New("runtime_context_scope_mismatch")

type Compiler struct {
	estimators *EstimatorRegistry
}

type compiledStageResult struct {
	messages            []*schema.Message
	fingerprintMessages []*schema.Message
	categoryTokens      map[string]int
	pruned              []PrunedContextItem
	evidence            *contracts.EvidenceBundleV1
}

func New(estimatorRegistry *EstimatorRegistry) *Compiler {
	if estimatorRegistry == nil {
		estimatorRegistry = NewEstimatorRegistry()
	}
	return &Compiler{estimators: estimatorRegistry}
}

func (c *Compiler) Compile(ctx context.Context, input CompileInput) (CompiledModelContext, error) {
	if err := ctx.Err(); err != nil {
		return CompiledModelContext{}, err
	}
	if err := validateCompileScope(input); err != nil {
		return CompiledModelContext{}, err
	}
	budget, err := CalculateBudget(input.Stage, input.Model.MaxContextTokens, input.Instance.ContextMaxTokens, input.Model.MaxOutputTokens)
	if err != nil {
		return CompiledModelContext{}, err
	}
	estimator := c.estimators.Resolve(input.Model.ModelName)
	var stageResult compiledStageResult
	switch input.Stage {
	case CompileStageIntent:
		stageResult, err = c.compileIntent(input, budget, estimator)
	case CompileStageGenerate:
		stageResult, err = c.compileGenerate(input, budget, estimator)
	default:
		err = fmt.Errorf("%w: unsupported stage %q", ErrInvalidContextLimit, input.Stage)
	}
	if err != nil {
		return CompiledModelContext{}, err
	}
	estimated := estimator.CountMessages(input.Model.ModelName, stageResult.messages)
	if estimated > budget.AvailableInput {
		return CompiledModelContext{}, fmt.Errorf("%w: estimated=%d available=%d", ErrMandatoryContextOverflow, estimated, budget.AvailableInput)
	}
	fingerprintMessages := stageResult.fingerprintMessages
	if len(fingerprintMessages) == 0 {
		fingerprintMessages = stageResult.messages
	}
	return CompiledModelContext{
		Messages: stageResult.messages, ContextLimit: budget.ContextLimit, ReservedOutput: budget.ReservedOutput,
		SafetyMargin: budget.SafetyMargin, AvailableInput: budget.AvailableInput, EstimatedInput: estimated,
		Estimator: estimator.Name(), CategoryTokens: stageResult.categoryTokens, PrunedItems: stageResult.pruned,
		Fingerprint: contextFingerprint(input, fingerprintMessages, stageResult.evidence, input.ReplyTagText),
	}, nil
}

func (c *Compiler) compileIntent(input CompileInput, budget Budget, estimator TokenEstimator) (compiledStageResult, error) {
	policy := buildIntentPolicy(input)
	stateRaw, err := json.Marshal(buildIntentStateProjection(input))
	if err != nil {
		return compiledStageResult{}, fmt.Errorf("marshal intent dialogue state: %w", err)
	}
	current := currentUserText(input.CurrentMessages)
	if current == "" {
		return compiledStageResult{}, fmt.Errorf("%w: current user message is empty", ErrMandatoryContextOverflow)
	}
	policyMessage := schema.SystemMessage(policy)
	stateMessage := schema.SystemMessage(string(stateRaw))
	repairMessage := buildRepairMessage(input.RepairInstruction)
	currentMessage := schema.UserMessage(current)
	mandatory := assembleIntentMessages(policyMessage, stateMessage, nil, repairMessage, currentMessage)
	if tokens := estimator.CountMessages(input.Model.ModelName, mandatory); tokens > budget.AvailableInput {
		return compiledStageResult{}, fmt.Errorf("%w: intent mandatory=%d available=%d", ErrMandatoryContextOverflow, tokens, budget.AvailableInput)
	}

	turns := BuildHistoryTurns(input.RecentHistory, input.CurrentMessages, estimator, input.Model.ModelName)
	if len(turns) > 4 {
		turns = turns[len(turns)-4:]
	}
	historyCap := min(1600, budget.AvailableInput*30/100)
	selected := make([]HistoryTurn, 0, len(turns))
	historyTokens := 0
	pruned := make([]PrunedContextItem, 0)
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		candidate := append([]HistoryTurn{turn}, selected...)
		candidateMessages := assembleIntentMessages(policyMessage, stateMessage, candidate, repairMessage, currentMessage)
		if historyTokens+turn.TokenCount <= historyCap && estimator.CountMessages(input.Model.ModelName, candidateMessages) <= budget.AvailableInput {
			selected = candidate
			historyTokens += turn.TokenCount
		} else {
			pruned = append(pruned, PrunedContextItem{Category: "history", ItemRef: fmt.Sprintf("seq:%d-%d", turn.FirstSeqNo, turn.LastSeqNo), Reason: "history_budget", EstimatedTokens: turn.TokenCount})
		}
	}
	messages := assembleIntentMessages(policyMessage, stateMessage, selected, repairMessage, currentMessage)
	return compiledStageResult{
		messages:            messages,
		fingerprintMessages: assembleIntentMessages(policyMessage, stateMessage, selected, nil, currentMessage),
		categoryTokens: map[string]int{
			"stable_policy":   estimator.CountMessages(input.Model.ModelName, []*schema.Message{policyMessage}),
			"runtime_state":   estimator.CountMessages(input.Model.ModelName, []*schema.Message{stateMessage}),
			"history":         historyTokens,
			"protocol_repair": estimator.CountMessages(input.Model.ModelName, nonNilMessages(repairMessage)),
			"current_turn":    estimator.CountMessages(input.Model.ModelName, []*schema.Message{currentMessage}),
		},
		pruned: pruned,
	}, nil
}

func (c *Compiler) compileGenerate(input CompileInput, budget Budget, estimator TokenEstimator) (compiledStageResult, error) {
	policyMessage := schema.SystemMessage(buildGeneratePolicy(input))
	repairMessage := buildRepairMessage(input.RepairInstruction)
	current := currentUserText(input.CurrentMessages)
	if current == "" {
		return compiledStageResult{}, fmt.Errorf("%w: current user message is empty", ErrMandatoryContextOverflow)
	}
	currentMessage := schema.UserMessage(current)
	baseSnapshot := buildRuntimeContextSnapshot(input, nil)
	stateMessage, err := snapshotSystemMessage(baseSnapshot)
	if err != nil {
		return compiledStageResult{}, err
	}
	stableTokens := estimator.CountMessages(input.Model.ModelName, []*schema.Message{policyMessage})
	stateTokens := estimator.CountMessages(input.Model.ModelName, []*schema.Message{stateMessage})
	// Category percentages guide optional-context pruning. Stable policy and the
	// base runtime snapshot are mandatory, so only the complete input budget may
	// reject them. A mandatory category crossing its soft share must not turn an
	// otherwise valid customer question into a human handoff.
	stateCap := max(min(1600, budget.AvailableInput*20/100), stateTokens)
	if mandatory := estimator.CountMessages(input.Model.ModelName, assembleGenerateMessages(policyMessage, stateMessage, nil, nil, nil, repairMessage, currentMessage)); mandatory > budget.AvailableInput {
		return compiledStageResult{}, fmt.Errorf("%w: generate mandatory=%d available=%d", ErrMandatoryContextOverflow, mandatory, budget.AvailableInput)
	}

	pruned := make([]PrunedContextItem, 0)
	requiredTaskKeys := requiredEvidenceTaskKeys(input.ReplyPlan)
	selectedEvidence, complete := selectRequiredEvidence(input.Evidence, requiredTaskKeys)
	if !complete {
		return compiledStageResult{}, fmt.Errorf("%w: at least one knowledge task lacks supporting evidence", ErrRequiredEvidenceOverflow)
	}
	evidenceCap := min(4000, budget.AvailableInput*45/100)
	projectedEvidence, evidenceMessage, evidenceTokens, evidencePruned, err := fitRequiredEvidence(
		input, budget, estimator, policyMessage, stateMessage, currentMessage, selectedEvidence, evidenceCap,
	)
	if err != nil {
		return compiledStageResult{}, err
	}
	pruned = append(pruned, evidencePruned...)

	tagMessage := (*schema.Message)(nil)
	tagTokens := 0
	if tagText := strings.TrimSpace(input.ReplyTagText); tagText != "" {
		candidate := schema.SystemMessage(tagText)
		candidateTokens := estimator.CountMessages(input.Model.ModelName, []*schema.Message{candidate})
		if candidateTokens <= 96 && compiledGenerateTokenCount(estimator, input.Model.ModelName, policyMessage, stateMessage, nil, evidenceMessage, candidate, repairMessage, currentMessage) <= budget.AvailableInput {
			tagMessage = candidate
			tagTokens = candidateTokens
		} else {
			pruned = append(pruned, PrunedContextItem{Category: "reply_tag", ItemRef: "reply_tag_context.v1", Reason: "reply_tag_budget", EstimatedTokens: candidateTokens})
		}
	}

	turns := BuildHistoryTurns(input.RecentHistory, input.CurrentMessages, estimator, input.Model.ModelName)
	historyCap := min(2400, budget.AvailableInput*30/100)
	selectedTurns := make([]HistoryTurn, 0, len(turns))
	historyTokens := 0
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		candidate := append([]HistoryTurn{turn}, selectedTurns...)
		if historyTokens+turn.TokenCount <= historyCap && compiledGenerateTokenCount(estimator, input.Model.ModelName, policyMessage, stateMessage, candidate, evidenceMessage, tagMessage, repairMessage, currentMessage) <= budget.AvailableInput {
			selectedTurns = candidate
			historyTokens += turn.TokenCount
		} else {
			pruned = append(pruned, PrunedContextItem{Category: "history", ItemRef: fmt.Sprintf("seq:%d-%d", turn.FirstSeqNo, turn.LastSeqNo), Reason: "history_budget", EstimatedTokens: turn.TokenCount})
		}
	}

	memoryTokens := 0
	selectedMemoryFacts := make([]contracts.RuntimeContextFact, 0)
	memoryCap := min(800, budget.AvailableInput*10/100)
	for _, fact := range memoryFacts(input) {
		candidateFacts := append(append([]contracts.RuntimeContextFact(nil), selectedMemoryFacts...), fact)
		candidateState, marshalErr := snapshotSystemMessage(buildRuntimeContextSnapshot(input, candidateFacts))
		if marshalErr != nil {
			return compiledStageResult{}, marshalErr
		}
		candidateStateTokens := estimator.CountMessages(input.Model.ModelName, []*schema.Message{candidateState})
		delta := max(candidateStateTokens-stateTokens, 0)
		if candidateStateTokens <= stateCap && delta <= memoryCap && compiledGenerateTokenCount(estimator, input.Model.ModelName, policyMessage, candidateState, selectedTurns, evidenceMessage, tagMessage, repairMessage, currentMessage) <= budget.AvailableInput {
			selectedMemoryFacts = candidateFacts
			stateMessage = candidateState
			memoryTokens = delta
		} else {
			pruned = append(pruned, PrunedContextItem{Category: "memory", ItemRef: fact.Key, Reason: "memory_budget", EstimatedTokens: max(delta, estimator.CountText(input.Model.ModelName, fact.Value))})
		}
	}

	optional := optionalEvidenceItems(input.Evidence, selectedEvidence)
	for _, item := range optional {
		candidateItems := append(append([]contracts.EvidenceItemV1(nil), selectedEvidence...), item)
		candidateEvidence := projectEvidence(input.Evidence, candidateItems)
		candidateMessage, marshalErr := evidenceSystemMessage(candidateEvidence)
		if marshalErr != nil {
			return compiledStageResult{}, marshalErr
		}
		candidateTokens := estimator.CountMessages(input.Model.ModelName, []*schema.Message{candidateMessage})
		if candidateTokens <= evidenceCap && compiledGenerateTokenCount(estimator, input.Model.ModelName, policyMessage, stateMessage, selectedTurns, candidateMessage, tagMessage, repairMessage, currentMessage) <= budget.AvailableInput {
			selectedEvidence = candidateItems
			projectedEvidence = &candidateEvidence
			evidenceMessage = candidateMessage
			evidenceTokens = candidateTokens
		} else {
			pruned = append(pruned, PrunedContextItem{Category: "evidence", ItemRef: item.Ref, Reason: "optional_evidence_budget", EstimatedTokens: estimator.CountText(input.Model.ModelName, item.Content)})
		}
	}

	messages := assembleGenerateMessages(policyMessage, stateMessage, selectedTurns, evidenceMessage, tagMessage, repairMessage, currentMessage)
	return compiledStageResult{
		messages:            messages,
		fingerprintMessages: assembleGenerateMessages(policyMessage, stateMessage, selectedTurns, evidenceMessage, tagMessage, nil, currentMessage),
		categoryTokens: map[string]int{
			"stable_policy": stableTokens, "runtime_state": stateTokens,
			"required_evidence": evidenceTokens, "history": historyTokens,
			"compressed_memory": memoryTokens, "reply_tag": tagTokens,
			"protocol_repair": estimator.CountMessages(input.Model.ModelName, nonNilMessages(repairMessage)),
			"current_turn":    estimator.CountMessages(input.Model.ModelName, []*schema.Message{currentMessage}),
		},
		pruned: pruned, evidence: projectedEvidence,
	}, nil
}

func fitRequiredEvidence(
	input CompileInput,
	budget Budget,
	estimator TokenEstimator,
	policyMessage, stateMessage, currentMessage *schema.Message,
	items []contracts.EvidenceItemV1,
	evidenceCap int,
) (*contracts.EvidenceBundleV1, *schema.Message, int, []PrunedContextItem, error) {
	if len(items) == 0 {
		return nil, nil, 0, nil, nil
	}
	working := append([]contracts.EvidenceItemV1(nil), items...)
	pruned := make([]PrunedContextItem, 0)
	for {
		projected := projectEvidence(input.Evidence, working)
		message, err := evidenceSystemMessage(projected)
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
		for i, item := range working {
			if length := len([]rune(item.Content)); length > longestRunes && length > 96 {
				longest = i
				longestRunes = length
			}
		}
		if longest < 0 {
			return nil, nil, 0, pruned, fmt.Errorf("%w: required evidence cannot fit within budget", ErrRequiredEvidenceOverflow)
		}
		before := estimator.CountText(input.Model.ModelName, working[longest].Content)
		working[longest] = truncateEvidenceContent(working[longest], max(longestRunes*3/4, 96))
		after := estimator.CountText(input.Model.ModelName, working[longest].Content)
		pruned = append(pruned, PrunedContextItem{Category: "evidence", ItemRef: working[longest].Ref, Reason: "required_evidence_sentence_truncated", EstimatedTokens: max(before-after, 0)})
	}
}

func buildIntentPolicy(input CompileInput) string {
	base := strings.TrimSpace(input.IntentInstruction)
	if base == "" {
		base = "你是 IntentDetect 阶段，只分析当前客户消息并拆分任务，不回复客户。"
	}
	intentSchema := input.IntentSchema
	if len(intentSchema) == 0 {
		intentSchema = contracts.MustSchema(contracts.SchemaIntentTasksV2)
	}
	return strings.TrimSpace(base + "\n\n只输出符合 intent_tasks.v2 的 UTF-8 JSON Object。不得输出 Markdown、解释、注释或额外字段。\nSchema:\n" + string(intentSchema))
}

func buildGeneratePolicy(input CompileInput) string {
	parts := make([]string, 0, 7)
	if value := strings.TrimSpace(input.StablePolicy); value != "" {
		parts = append(parts, value)
	} else {
		if value := strings.TrimSpace(input.Agent.SystemPrompt); value != "" {
			parts = append(parts, value)
		}
		if value := strings.TrimSpace(input.Instance.PersonaPrompt); value != "" {
			parts = append(parts, value)
		}
	}
	if value := strings.TrimSpace(input.GenerationInstruction); value != "" {
		parts = append(parts, value)
	}
	parts = append(parts, "你只负责把当前 ReplyPlan 和 Evidence 自然表达给客户，不新增任务、不决定动作、不声称未提交的资源、工具或人工动作。")
	contract := input.ReplyContract
	if contract == "" {
		contract = ReplyContractV2
	}
	if contract == ReplyContractV2 {
		parts = append(parts,
			"只输出一个符合 reply_output.v2 的 UTF-8 JSON Object。每个当前文本 taskKey 必须且只能出现一次，最多三段；不得输出 Markdown、解释、注释或额外字段。",
			`固定结构：{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["..."],"content":"给客户的话","evidenceRefs":[],"actionRefs":[]}]}`,
		)
	} else {
		parts = append(parts, "只输出客户可见的最终回复正文；不得输出思考过程、内部字段、JSON 协议说明或动作执行状态。")
	}
	return strings.Join(parts, "\n\n")
}

func currentUserText(items []models.Message) string {
	ordered := append([]models.Message(nil), items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].SeqNo == ordered[j].SeqNo {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].SeqNo < ordered[j].SeqNo
	})
	return joinVisibleMessages(ordered)
}

func snapshotSystemMessage(snapshot contracts.RuntimeContextSnapshotV1) (*schema.Message, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal runtime context snapshot: %w", err)
	}
	return schema.SystemMessage(string(raw)), nil
}

func evidenceSystemMessage(evidence contracts.EvidenceBundleV1) (*schema.Message, error) {
	raw, err := json.Marshal(evidence)
	if err != nil {
		return nil, fmt.Errorf("marshal evidence bundle: %w", err)
	}
	return schema.SystemMessage(string(raw)), nil
}

func assembleIntentMessages(policy, state *schema.Message, turns []HistoryTurn, repair, current *schema.Message) []*schema.Message {
	messages := []*schema.Message{policy, state}
	for _, turn := range turns {
		messages = append(messages, historyTurnMessages(turn)...)
	}
	if repair != nil {
		messages = append(messages, repair)
	}
	return append(messages, current)
}

func assembleGenerateMessages(policy, state *schema.Message, turns []HistoryTurn, evidence, tag, repair, current *schema.Message) []*schema.Message {
	messages := []*schema.Message{policy, state}
	for _, turn := range turns {
		messages = append(messages, historyTurnMessages(turn)...)
	}
	if evidence != nil {
		messages = append(messages, evidence)
	}
	if tag != nil {
		messages = append(messages, tag)
	}
	if repair != nil {
		messages = append(messages, repair)
	}
	return append(messages, current)
}

func compiledGenerateTokenCount(estimator TokenEstimator, modelName string, policy, state *schema.Message, turns []HistoryTurn, evidence, tag, repair, current *schema.Message) int {
	return estimator.CountMessages(modelName, assembleGenerateMessages(policy, state, turns, evidence, tag, repair, current))
}

func buildRepairMessage(instruction string) *schema.Message {
	if instruction = strings.TrimSpace(instruction); instruction != "" {
		return schema.SystemMessage(instruction)
	}
	return nil
}

func nonNilMessages(messages ...*schema.Message) []*schema.Message {
	ret := make([]*schema.Message, 0, len(messages))
	for _, message := range messages {
		if message != nil {
			ret = append(ret, message)
		}
	}
	return ret
}

func validateCompileScope(input CompileInput) error {
	if input.Scope.TenantID <= 0 || input.Scope.StoreID <= 0 || input.Scope.ConversationID <= 0 || input.Scope.SessionNo <= 0 ||
		input.Scope.WxWorkInstanceID <= 0 || input.Scope.StoreStaffBindingID <= 0 {
		return fmt.Errorf("%w: incomplete runtime scope", ErrRuntimeScopeMismatch)
	}
	if input.Model.TenantID != input.Scope.TenantID || input.Model.StoreID != input.Scope.StoreID || input.Model.StoreStaffBindingID != input.Scope.StoreStaffBindingID {
		return fmt.Errorf("%w: model scope differs from runtime scope", ErrRuntimeScopeMismatch)
	}
	if input.Instance.ID != input.Scope.WxWorkInstanceID || input.Instance.TenantID != input.Scope.TenantID || input.Instance.StoreID != input.Scope.StoreID ||
		input.Instance.StoreStaffBindingID != input.Scope.StoreStaffBindingID {
		return fmt.Errorf("%w: instance scope differs from runtime scope", ErrRuntimeScopeMismatch)
	}
	for _, message := range append(append([]models.Message(nil), input.CurrentMessages...), input.RecentHistory...) {
		if message.TenantID != input.Scope.TenantID || message.ConversationID != input.Scope.ConversationID || message.SessionNo != input.Scope.SessionNo {
			return fmt.Errorf("%w: message %d differs from runtime scope", ErrRuntimeScopeMismatch, message.ID)
		}
	}
	if input.Evidence != nil && strings.TrimSpace(input.ExpectedEvidenceScopeFingerprint) != "" &&
		input.Evidence.ScopeFingerprint != strings.TrimSpace(input.ExpectedEvidenceScopeFingerprint) {
		return fmt.Errorf("%w: evidence scope fingerprint differs from runtime scope", ErrRuntimeScopeMismatch)
	}
	return nil
}
