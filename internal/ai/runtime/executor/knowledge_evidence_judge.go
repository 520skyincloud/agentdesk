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
	knowledgeEvidenceJudgeSchemaVersion = "knowledge_evidence_judge.v1"

	knowledgeEvidenceClassificationDirect     = "direct"
	knowledgeEvidenceClassificationSupporting = "supporting"
	knowledgeEvidenceClassificationUnrelated  = "unrelated"

	knowledgeEvidenceLayerStore   = "store"
	knowledgeEvidenceLayerGeneral = "general"

	knowledgeEvidenceJudgeMaxTimeout      = 4 * time.Second
	knowledgeEvidenceJudgeMaxOutputTokens = 2048
)

type knowledgeEvidenceJudge interface {
	JudgeBatch(ctx context.Context, req RunInput, tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome
}

type knowledgeEvidenceJudgeTask struct {
	TaskID     string
	Query      string
	Candidates []knowledgeEvidenceJudgeCandidate
}

type knowledgeEvidenceJudgeCandidate struct {
	CandidateID string
	Layer       string
	Hit         rag.RetrieveResult
}

type knowledgeEvidenceJudgeOutcome struct {
	Applied         bool
	Classifications map[string]map[string]string
	Trace           callbacks.KnowledgeEvidenceJudgeTraceData
}

type modelKnowledgeEvidenceJudge struct{}

type knowledgeEvidenceJudgePrompt struct {
	SchemaVersion string                             `json:"schemaVersion"`
	Tasks         []knowledgeEvidenceJudgePromptTask `json:"tasks"`
}

type knowledgeEvidenceJudgePromptTask struct {
	TaskID     string                                  `json:"taskId"`
	Question   string                                  `json:"question"`
	Candidates []knowledgeEvidenceJudgePromptCandidate `json:"candidates"`
}

type knowledgeEvidenceJudgePromptCandidate struct {
	CandidateID string  `json:"candidateId"`
	Layer       string  `json:"layer"`
	FAQQuestion string  `json:"faqQuestion,omitempty"`
	Title       string  `json:"title,omitempty"`
	Content     string  `json:"content"`
	Score       float32 `json:"score"`
}

type knowledgeEvidenceJudgeResponse struct {
	SchemaVersion string                               `json:"schemaVersion"`
	Tasks         []knowledgeEvidenceJudgeResponseTask `json:"tasks"`
}

type knowledgeEvidenceJudgeResponseTask struct {
	TaskID     string                                    `json:"taskId"`
	Candidates []knowledgeEvidenceJudgeResponseCandidate `json:"candidates"`
}

type knowledgeEvidenceJudgeResponseCandidate struct {
	CandidateID    string `json:"candidateId"`
	Classification string `json:"classification"`
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

	classifications, parseErr := parseKnowledgeEvidenceJudgeResponse(result.Content, tasks)
	if parseErr != nil {
		trace.Reason = "knowledge judge returned an invalid protocol response; deterministic retrieval selection was preserved"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(parseErr)
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}
	trace.Status = "completed"
	trace.Reason = "knowledge candidates were classified once before deterministic layer selection"
	return knowledgeEvidenceJudgeOutcome{
		Applied:         true,
		Classifications: classifications,
		Trace:           trace,
	}
}

func buildKnowledgeEvidenceJudgePrompt(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgePrompt {
	prompt := knowledgeEvidenceJudgePrompt{SchemaVersion: knowledgeEvidenceJudgeSchemaVersion}
	prompt.Tasks = make([]knowledgeEvidenceJudgePromptTask, 0, len(tasks))
	for _, task := range tasks {
		item := knowledgeEvidenceJudgePromptTask{
			TaskID:   strings.TrimSpace(task.TaskID),
			Question: strings.TrimSpace(task.Query),
		}
		item.Candidates = make([]knowledgeEvidenceJudgePromptCandidate, 0, len(task.Candidates))
		for _, candidate := range task.Candidates {
			title := strings.TrimSpace(candidate.Hit.Title)
			if title == "" {
				title = strings.TrimSpace(candidate.Hit.DocumentTitle)
			}
			item.Candidates = append(item.Candidates, knowledgeEvidenceJudgePromptCandidate{
				CandidateID: strings.TrimSpace(candidate.CandidateID),
				Layer:       strings.TrimSpace(candidate.Layer),
				FAQQuestion: strings.TrimSpace(candidate.Hit.FaqQuestion),
				Title:       title,
				Content:     strings.TrimSpace(candidate.Hit.Content),
				Score:       candidate.Hit.Score,
			})
		}
		prompt.Tasks = append(prompt.Tasks, item)
	}
	return prompt
}

func knowledgeEvidenceJudgeSystemPrompt() string {
	return strings.TrimSpace(`你是酒店客服知识证据裁判。你不回答客户，不决定是否转人工，只判断每条候选知识与对应客户问题的关系。

对每个候选只能标记一种分类：
- direct：候选正文自身直接、明确且足以回答当前问题。问题条件、对象和答案必须一致。
- supporting：候选与问题相关，但单独不足以直接回答，只能补充 direct 证据。
- unrelated：候选答的是别的问题、条件不一致、对象不一致，或无法支持客户所问事实。

否定答案也可以是完整直接答案。客户询问某项服务的时间、地点、价格、方式或是否提供时，如果候选明确说明“不提供、没有、不支持”，它已经直接回答了问题，必须标记 direct。例如“早餐几点”对应“酒店不提供早餐”是 direct，不能标记 supporting。

必须区分能力/存在性与故障/执行请求。例如“有空调吗”不能把“空调不制冷需要处理”判为 direct；“谁是汤东强”不能把用品、人员无关内容判为 direct。检索分数、候选顺序和 store/general 层级都不能替代语义判断。

严格输出 JSON，不要 Markdown、解释或额外字段。必须原样返回每个 taskId，并且每个 candidateId 恰好出现一次。输出格式：
{"schemaVersion":"knowledge_evidence_judge.v1","tasks":[{"taskId":"T1","candidates":[{"candidateId":"T1C1","classification":"direct"}]}]}`)
}

func parseKnowledgeEvidenceJudgeResponse(raw string, tasks []knowledgeEvidenceJudgeTask) (map[string]map[string]string, error) {
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
	expected := make(map[string]map[string]struct{}, len(tasks))
	for _, task := range tasks {
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			return nil, fmt.Errorf("knowledge judge task id is empty")
		}
		candidateIDs := make(map[string]struct{}, len(task.Candidates))
		for _, candidate := range task.Candidates {
			candidateID := strings.TrimSpace(candidate.CandidateID)
			if candidateID == "" {
				return nil, fmt.Errorf("knowledge judge candidate id is empty for task %s", taskID)
			}
			if _, exists := candidateIDs[candidateID]; exists {
				return nil, fmt.Errorf("duplicate expected candidate id %s", candidateID)
			}
			candidateIDs[candidateID] = struct{}{}
		}
		expected[taskID] = candidateIDs
	}

	ret := make(map[string]map[string]string, len(tasks))
	for _, task := range parsed.Tasks {
		taskID := strings.TrimSpace(task.TaskID)
		expectedCandidates, ok := expected[taskID]
		if !ok {
			return nil, fmt.Errorf("unknown knowledge judge task id %s", taskID)
		}
		if _, exists := ret[taskID]; exists {
			return nil, fmt.Errorf("duplicate knowledge judge task id %s", taskID)
		}
		if len(task.Candidates) != len(expectedCandidates) {
			return nil, fmt.Errorf("knowledge judge candidate count mismatch for task %s", taskID)
		}
		classifications := make(map[string]string, len(task.Candidates))
		for _, candidate := range task.Candidates {
			candidateID := strings.TrimSpace(candidate.CandidateID)
			if _, ok := expectedCandidates[candidateID]; !ok {
				return nil, fmt.Errorf("unknown knowledge judge candidate id %s", candidateID)
			}
			if _, exists := classifications[candidateID]; exists {
				return nil, fmt.Errorf("duplicate knowledge judge candidate id %s", candidateID)
			}
			classification := strings.TrimSpace(candidate.Classification)
			switch classification {
			case knowledgeEvidenceClassificationDirect, knowledgeEvidenceClassificationSupporting, knowledgeEvidenceClassificationUnrelated:
			default:
				return nil, fmt.Errorf("invalid knowledge judge classification %q", classification)
			}
			classifications[candidateID] = classification
		}
		ret[taskID] = classifications
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
