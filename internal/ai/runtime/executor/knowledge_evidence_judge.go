package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/services"
)

const (
	knowledgeEvidenceJudgeSchemaVersion = "knowledge_evidence_judge.v2"

	knowledgeEvidenceDecisionDirectSingle   = "direct_single"
	knowledgeEvidenceDecisionDirectCombined = "direct_combined"
	knowledgeEvidenceDecisionInsufficient   = "insufficient"

	knowledgeEvidenceLayerStore   = "store"
	knowledgeEvidenceLayerGeneral = "general"

	knowledgeEvidenceJudgeMaxTimeout      = 4 * time.Second
	knowledgeEvidenceJudgeMaxOutputTokens = 2048
)

type knowledgeEvidenceJudge interface {
	JudgeBatch(ctx context.Context, req RunInput, tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome
}

type knowledgeEvidenceJudgeTask struct {
	TaskID        string
	Query         string
	SourceContext []knowledgeEvidenceJudgeSourceMessage
	Candidates    []knowledgeEvidenceJudgeCandidate
}

type knowledgeEvidenceJudgeSourceMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type knowledgeEvidenceJudgeCandidate struct {
	CandidateID string
	Layer       string
	Hit         rag.RetrieveResult
}

type knowledgeEvidenceJudgeOutcome struct {
	Applied    bool
	Selections map[string]map[string]knowledgeEvidenceLayerSelection
	Trace      callbacks.KnowledgeEvidenceJudgeTraceData
}

type knowledgeEvidenceLayerSelection struct {
	Decision             string
	SelectedCandidateIDs []string
}

type modelKnowledgeEvidenceJudge struct{}

type knowledgeEvidenceJudgePrompt struct {
	SchemaVersion string                             `json:"schemaVersion"`
	Tasks         []knowledgeEvidenceJudgePromptTask `json:"tasks"`
}

type knowledgeEvidenceJudgePromptTask struct {
	TaskID        string                                  `json:"taskId"`
	Question      string                                  `json:"question"`
	SourceContext []knowledgeEvidenceJudgeSourceMessage   `json:"sourceContext,omitempty"`
	Candidates    []knowledgeEvidenceJudgePromptCandidate `json:"candidates"`
}

type knowledgeEvidenceJudgePromptCandidate struct {
	CandidateID string  `json:"candidateId"`
	Layer       string  `json:"layer"`
	FAQQuestion string  `json:"faqQuestion,omitempty"`
	FAQAnswer   string  `json:"faqAnswer,omitempty"`
	Title       string  `json:"title,omitempty"`
	RawContent  string  `json:"rawContent"`
	Score       float32 `json:"score"`
}

type knowledgeEvidenceJudgeResponse struct {
	SchemaVersion string                               `json:"schemaVersion"`
	Tasks         []knowledgeEvidenceJudgeResponseTask `json:"tasks"`
}

type knowledgeEvidenceJudgeResponseTask struct {
	TaskID string                                `json:"taskId"`
	Layers []knowledgeEvidenceJudgeResponseLayer `json:"layers"`
}

type knowledgeEvidenceJudgeResponseLayer struct {
	Layer                string   `json:"layer"`
	Decision             string   `json:"decision"`
	SelectedCandidateIDs []string `json:"selectedCandidateIds"`
}

func (modelKnowledgeEvidenceJudge) JudgeBatch(ctx context.Context, req RunInput, tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
	prompt := buildKnowledgeEvidenceJudgePrompt(tasks)
	fingerprint := fingerprintKnowledgeEvidenceJudgePrompt(prompt)
	trace := callbacks.KnowledgeEvidenceJudgeTraceData{
		SchemaVersion:        knowledgeEvidenceJudgeSchemaVersion,
		Status:               "fallback",
		Reason:               "judge was not completed; deterministic retrieval selection was preserved",
		CandidateFingerprint: fingerprint,
		TaskCount:            len(prompt.Tasks),
		CandidateCount:       countKnowledgeEvidenceJudgeCandidates(prompt),
	}
	if len(prompt.Tasks) == 0 {
		trace.Status = "skipped"
		trace.Reason = "no retrieved candidate required evidence judging"
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}

	resolved, err := services.StoreAIModelSettingService.ResolveForConversation(req.Conversation.ID, services.StoreAIModelUsageKnowledgeJudgeLLM, 0)
	if err != nil || resolved == nil {
		trace.Reason = "knowledge judge model is unavailable; deterministic retrieval selection was preserved"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(err)
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}
	config := normalizeKnowledgeEvidenceJudgeConfig(resolved.Config)
	trace.Model = config.ModelName
	if strings.TrimSpace(config.ModelName) == "" || strings.TrimSpace(string(config.Provider)) == "" {
		trace.Reason = "knowledge judge model configuration is incomplete; deterministic retrieval selection was preserved"
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}

	systemPrompt := knowledgeEvidenceJudgeSystemPrompt()
	userPrompt, err := json.Marshal(prompt)
	if err != nil {
		trace.Reason = "knowledge judge prompt could not be encoded; deterministic retrieval selection was preserved"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(err)
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}

	callCtx := knowledgeEvidenceJudgeUsageContext(ctx, req, resolved)
	callCtx, capture := usagex.WithCapture(callCtx)
	callCtx, cancel := context.WithTimeout(callCtx, time.Duration(config.TimeoutMS)*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	result, callErr := ai.LLM.ChatWithConfig(callCtx, config, systemPrompt, string(userPrompt))
	trace.LatencyMs = time.Since(startedAt).Milliseconds()
	recordKnowledgeEvidenceJudgeUsage(callCtx, req, config, result, lastKnowledgeEvidenceJudgeReceipt(capture), fingerprint, trace.LatencyMs, callErr)
	if callErr != nil {
		trace.Reason = "knowledge judge model call failed; deterministic retrieval selection was preserved"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(callErr)
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}

	selections, parseErr := parseKnowledgeEvidenceJudgeResponse(result.Content, tasks)
	if parseErr != nil {
		trace.Reason = "knowledge judge returned an invalid protocol response; deterministic retrieval selection was preserved"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(parseErr)
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}
	trace.Status = "completed"
	trace.Reason = "knowledge evidence was selected once per task and layer before deterministic store priority"
	return knowledgeEvidenceJudgeOutcome{
		Applied:    true,
		Selections: selections,
		Trace:      trace,
	}
}

func buildKnowledgeEvidenceJudgePrompt(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgePrompt {
	prompt := knowledgeEvidenceJudgePrompt{SchemaVersion: knowledgeEvidenceJudgeSchemaVersion}
	prompt.Tasks = make([]knowledgeEvidenceJudgePromptTask, 0, len(tasks))
	for _, task := range tasks {
		item := knowledgeEvidenceJudgePromptTask{
			TaskID:        strings.TrimSpace(task.TaskID),
			Question:      strings.TrimSpace(task.Query),
			SourceContext: append([]knowledgeEvidenceJudgeSourceMessage(nil), task.SourceContext...),
		}
		item.Candidates = make([]knowledgeEvidenceJudgePromptCandidate, 0, len(task.Candidates))
		for _, candidate := range task.Candidates {
			title := strings.TrimSpace(candidate.Hit.Title)
			if title == "" {
				title = strings.TrimSpace(candidate.Hit.DocumentTitle)
			}
			faqQuestion, faqAnswer := splitKnowledgeEvidenceFAQ(candidate.Hit)
			item.Candidates = append(item.Candidates, knowledgeEvidenceJudgePromptCandidate{
				CandidateID: strings.TrimSpace(candidate.CandidateID),
				Layer:       strings.TrimSpace(candidate.Layer),
				FAQQuestion: faqQuestion,
				FAQAnswer:   faqAnswer,
				Title:       title,
				RawContent:  strings.TrimSpace(candidate.Hit.Content),
				Score:       candidate.Hit.Score,
			})
		}
		prompt.Tasks = append(prompt.Tasks, item)
	}
	return prompt
}

func splitKnowledgeEvidenceFAQ(hit rag.RetrieveResult) (string, string) {
	raw := strings.TrimSpace(hit.Content)
	question := strings.TrimSpace(hit.FaqQuestion)
	answer := ""
	if parsedQuestion, parsedAnswer, ok := parseQuestionAnswerContent(raw); ok {
		if question == "" {
			question = parsedQuestion
		}
		answer = parsedAnswer
	} else if question != "" {
		answer = raw
	}
	return question, answer
}

func parseQuestionAnswerContent(raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	answerIndex := -1
	answerPrefixLength := 0
	for _, marker := range []string{"\n答案：", "\n答案:", "答案：", "答案:"} {
		if index := strings.Index(raw, marker); index >= 0 && (answerIndex < 0 || index < answerIndex) {
			answerIndex = index
			answerPrefixLength = len(marker)
		}
	}
	if answerIndex < 0 {
		return "", "", false
	}
	question := strings.TrimSpace(raw[:answerIndex])
	question = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(question, "问题："), "问题:"))
	answer := strings.TrimSpace(raw[answerIndex+answerPrefixLength:])
	if question == "" || answer == "" {
		return "", "", false
	}
	return question, answer, true
}

func knowledgeEvidenceJudgeSystemPrompt() string {
	return strings.TrimSpace(`你是酒店客服知识证据裁判。你不回答客户，不决定是否转人工，只为每个客户任务在每个知识层选择足以回答的证据。

每个 task 会提供当前原子问题、紧邻会话 sourceContext，以及带 layer 的候选。sourceContext 只用于理解“这几个、上面那种、都”等指代，不能当作酒店事实来源。

必须分别裁决 store 和 general 两层，每层只能输出一种 decision：
- direct_single：单条候选的完整语义足以回答当前问题，只选择这一条。
- direct_combined：同一层内至少两条候选指向同一门店、同一实体和同一适用范围，合在一起足以回答当前问题，只选择必要的候选。
- insufficient：该层没有足够证据，selectedCandidateIds 必须为空。

严禁跨 store/general 拼接证据，也不能把不同门店、不同房型对象、不同时间条件或互相矛盾的内容组合。检索分数和候选顺序不能替代语义判断。

FAQ 必须把 faqQuestion 和 faqAnswer 作为一个完整问答来理解。答案出现“是的、可以、不需要、没有”等省略表达时，可以结合 FAQ 问题还原其中已经被明确确认的对象、数量、条件和结论；不得补出 FAQ 问答没有确认的事实。rawContent 只用于核对原文。

例如 FAQ 问题“问下房间的两瓶矿泉水是免费的吗？”、答案“是的，房间内的矿泉水都是免费的”，完整语义已经确认“房间内有两瓶矿泉水，并且免费”。它足以回答“房间里有几瓶矿泉水”，应判 direct_single；不能因为数量只写在 faqQuestion 中就丢掉这个已被肯定回答确认的事实。这个规则同样适用于其他 FAQ 中被肯定或否定答案确认的对象、数量与条件。

候选答案如果只是“转接”，它是流程指令，不是酒店事实。只有 FAQ 问题与当前任务语义直接匹配时，才可以把该候选作为单条流程指令选择；绝不能把 FAQ 问题文字当作已经确认的事实，也不能让“转接”候选参与 direct_combined。

同层组合示例：客户问“既有沙发又有办公桌的房型有哪些”，一条候选列出有沙发的房型，另一条候选列出有办公桌的房型，两条属于同一门店和房型范围时，可以判 direct_combined，让后续生成阶段计算交集。只知道沙发或只知道办公桌时必须判 insufficient。

否定答案也可以完整回答问题。例如“早餐几点”对应“酒店不提供早餐”可以判 direct_single。必须区分能力/存在性与故障/执行请求，例如“有空调吗”不能选择“空调不制冷需要处理”。

严格输出 JSON，不要 Markdown、解释或额外字段。必须原样返回每个 taskId；对输入实际包含的每个 layer 恰好返回一次。输出格式：
{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"]},{"layer":"general","decision":"insufficient","selectedCandidateIds":[]}]}]}`)
}

func parseKnowledgeEvidenceJudgeResponse(raw string, tasks []knowledgeEvidenceJudgeTask) (map[string]map[string]knowledgeEvidenceLayerSelection, error) {
	parsed := knowledgeEvidenceJudgeResponse{}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode knowledge judge response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("knowledge judge response contains trailing content")
	}
	if parsed.SchemaVersion != knowledgeEvidenceJudgeSchemaVersion {
		return nil, fmt.Errorf("unexpected knowledge judge schema version %q", parsed.SchemaVersion)
	}
	if len(parsed.Tasks) != len(tasks) {
		return nil, fmt.Errorf("knowledge judge task count mismatch: got %d want %d", len(parsed.Tasks), len(tasks))
	}
	expected := make(map[string]map[string]map[string]struct{}, len(tasks))
	for _, task := range tasks {
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			return nil, fmt.Errorf("knowledge judge task id is empty")
		}
		layerCandidates := make(map[string]map[string]struct{}, 2)
		for _, candidate := range task.Candidates {
			candidateID := strings.TrimSpace(candidate.CandidateID)
			if candidateID == "" {
				return nil, fmt.Errorf("knowledge judge candidate id is empty for task %s", taskID)
			}
			layer := strings.TrimSpace(candidate.Layer)
			if layer != knowledgeEvidenceLayerStore && layer != knowledgeEvidenceLayerGeneral {
				return nil, fmt.Errorf("invalid knowledge judge layer %q for candidate %s", layer, candidateID)
			}
			if layerCandidates[layer] == nil {
				layerCandidates[layer] = make(map[string]struct{})
			}
			if _, exists := layerCandidates[layer][candidateID]; exists {
				return nil, fmt.Errorf("duplicate expected candidate id %s", candidateID)
			}
			layerCandidates[layer][candidateID] = struct{}{}
		}
		expected[taskID] = layerCandidates
	}

	ret := make(map[string]map[string]knowledgeEvidenceLayerSelection, len(tasks))
	for _, task := range parsed.Tasks {
		taskID := strings.TrimSpace(task.TaskID)
		expectedLayers, ok := expected[taskID]
		if !ok {
			return nil, fmt.Errorf("unknown knowledge judge task id %s", taskID)
		}
		if _, exists := ret[taskID]; exists {
			return nil, fmt.Errorf("duplicate knowledge judge task id %s", taskID)
		}
		if len(task.Layers) != len(expectedLayers) {
			return nil, fmt.Errorf("knowledge judge layer count mismatch for task %s", taskID)
		}
		selections := make(map[string]knowledgeEvidenceLayerSelection, len(task.Layers))
		for _, layerResult := range task.Layers {
			layer := strings.TrimSpace(layerResult.Layer)
			expectedCandidates, ok := expectedLayers[layer]
			if !ok {
				return nil, fmt.Errorf("unknown knowledge judge layer %s for task %s", layer, taskID)
			}
			if _, exists := selections[layer]; exists {
				return nil, fmt.Errorf("duplicate knowledge judge layer %s for task %s", layer, taskID)
			}
			decision := strings.TrimSpace(layerResult.Decision)
			switch decision {
			case knowledgeEvidenceDecisionDirectSingle, knowledgeEvidenceDecisionDirectCombined, knowledgeEvidenceDecisionInsufficient:
			default:
				return nil, fmt.Errorf("invalid knowledge judge decision %q", decision)
			}
			selectedIDs := make([]string, 0, len(layerResult.SelectedCandidateIDs))
			seenSelected := make(map[string]struct{}, len(layerResult.SelectedCandidateIDs))
			for _, rawCandidateID := range layerResult.SelectedCandidateIDs {
				candidateID := strings.TrimSpace(rawCandidateID)
				if _, ok := expectedCandidates[candidateID]; !ok {
					return nil, fmt.Errorf("candidate %s does not belong to task %s layer %s", candidateID, taskID, layer)
				}
				if _, exists := seenSelected[candidateID]; exists {
					return nil, fmt.Errorf("duplicate selected candidate id %s", candidateID)
				}
				seenSelected[candidateID] = struct{}{}
				selectedIDs = append(selectedIDs, candidateID)
			}
			switch decision {
			case knowledgeEvidenceDecisionInsufficient:
				if len(selectedIDs) != 0 {
					return nil, fmt.Errorf("insufficient decision must not select candidates for task %s layer %s", taskID, layer)
				}
			case knowledgeEvidenceDecisionDirectSingle:
				if len(selectedIDs) != 1 {
					return nil, fmt.Errorf("direct_single must select exactly one candidate for task %s layer %s", taskID, layer)
				}
			case knowledgeEvidenceDecisionDirectCombined:
				if len(selectedIDs) < 2 {
					return nil, fmt.Errorf("direct_combined must select at least two candidates for task %s layer %s", taskID, layer)
				}
			}
			selections[layer] = knowledgeEvidenceLayerSelection{Decision: decision, SelectedCandidateIDs: selectedIDs}
		}
		ret[taskID] = selections
	}
	return ret, nil
}

func normalizeKnowledgeEvidenceJudgeConfig(config models.AIConfig) models.AIConfig {
	if config.TimeoutMS <= 0 || time.Duration(config.TimeoutMS)*time.Millisecond > knowledgeEvidenceJudgeMaxTimeout {
		config.TimeoutMS = int(knowledgeEvidenceJudgeMaxTimeout / time.Millisecond)
	}
	if config.MaxOutputTokens <= 0 || config.MaxOutputTokens > knowledgeEvidenceJudgeMaxOutputTokens {
		config.MaxOutputTokens = knowledgeEvidenceJudgeMaxOutputTokens
	}
	config.MaxRetryCount = 0
	return config
}

func knowledgeEvidenceJudgeUsageContext(ctx context.Context, req RunInput, resolved *services.ResolvedAIConfig) context.Context {
	scope := usagex.ScopeFromContext(ctx)
	runtimeScope := resolveRuntimeIntentScope(req)
	if scope.CompanyID <= 0 {
		scope.CompanyID = runtimeScope.CompanyID
	}
	if scope.StoreID <= 0 {
		scope.StoreID = runtimeScope.StoreID
	}
	if scope.WxWorkInstanceID <= 0 {
		scope.WxWorkInstanceID = runtimeScope.WxWorkInstanceID
	}
	if scope.ConversationID <= 0 {
		scope.ConversationID = req.Conversation.ID
	}
	if scope.MessageID <= 0 {
		scope.MessageID = req.UserMessage.ID
	}
	if strings.TrimSpace(scope.RequestID) == "" {
		scope.RequestID = strings.TrimSpace(req.UserMessage.RequestID)
	}
	if resolved != nil {
		scope.CredentialRevision = resolved.CredentialRevision
		scope.ModelSource = resolved.Source
	}
	return usagex.WithScope(ctx, scope)
}

func recordKnowledgeEvidenceJudgeUsage(ctx context.Context, req RunInput, config models.AIConfig, result *ai.ChatCompletionResult, receipt *usagex.Receipt, fingerprint string, latencyMS int64, callErr error) {
	status := "completed"
	errorClass := ""
	if callErr != nil {
		status = "failed"
		errorClass = "model_call_failed"
	}
	record := ai.ModelUsageRecord{
		Stage:            "knowledge_evidence_judge",
		OperationType:    "batch_select",
		Config:           config,
		LatencyMS:        latencyMS,
		Status:           status,
		ErrorClass:       errorClass,
		Receipt:          receipt,
		ExternalEventKey: knowledgeEvidenceJudgeUsageEventKey(req, fingerprint),
	}
	if result != nil {
		record.PromptTokens = int64(result.PromptTokens)
		record.CompletionTokens = int64(result.CompletionTokens)
	}
	ai.RecordModelUsage(ctx, record)
}

func knowledgeEvidenceJudgeUsageEventKey(req RunInput, fingerprint string) string {
	requestID := strings.TrimSpace(req.UserMessage.RequestID)
	if requestID == "" {
		requestID = fmt.Sprintf("conversation-%d-message-%d", req.Conversation.ID, req.UserMessage.ID)
	}
	return requestID + ":knowledge_evidence_judge:" + fingerprint
}

func lastKnowledgeEvidenceJudgeReceipt(capture *usagex.Capture) *usagex.Receipt {
	if capture == nil {
		return nil
	}
	receipts := capture.Receipts()
	if len(receipts) == 0 {
		return nil
	}
	receipt := receipts[len(receipts)-1]
	return &receipt
}

func fingerprintKnowledgeEvidenceJudgePrompt(prompt knowledgeEvidenceJudgePrompt) string {
	raw, _ := json.Marshal(prompt)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:12])
}

func countKnowledgeEvidenceJudgeCandidates(prompt knowledgeEvidenceJudgePrompt) int {
	count := 0
	for _, task := range prompt.Tasks {
		count += len(task.Candidates)
	}
	return count
}

func compactKnowledgeEvidenceJudgeError(err error) string {
	if err == nil {
		return ""
	}
	return preview(strings.Join(strings.Fields(err.Error()), " "), 240)
}
