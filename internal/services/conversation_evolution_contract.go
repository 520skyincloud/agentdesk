package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	conversationEvolutionMaxInputTokens     = 4000
	conversationEvolutionPromptTokenReserve = 500
	conversationEvolutionMaxMessageRunes    = 500
	conversationEvolutionMaxAllowedTags     = 200
	conversationEvolutionMaxOperations      = 10
)

type customerTagInput struct {
	SchemaVersion   string                     `json:"schemaVersion"`
	Run             customerTagInputRun        `json:"run"`
	Scope           customerTagInputScope      `json:"scope"`
	Checkpoint      customerTagInputCheckpoint `json:"checkpoint"`
	PreviousSummary string                     `json:"previousSummary"`
	CurrentTags     []customerTagInputCurrent  `json:"currentTags"`
	AllowedTags     []customerTagInputAllowed  `json:"allowedTags"`
	Messages        []customerTagInputMessage  `json:"messages"`
}

type customerTagInputRun struct {
	RunID                string `json:"runId"`
	ModelProfileRevision int64  `json:"modelProfileRevision"`
}

type customerTagInputScope struct {
	TenantID                int64 `json:"tenantId"`
	StoreID                 int64 `json:"storeId"`
	CustomerID              int64 `json:"customerId"`
	StoreCustomerRelationID int64 `json:"storeCustomerRelationId"`
	ConversationID          int64 `json:"conversationId"`
	SessionNo               int   `json:"sessionNo"`
}

type customerTagInputCheckpoint struct {
	PreviousEndMessageID int64 `json:"previousEndMessageId"`
	EndMessageID         int64 `json:"endMessageId"`
}

type customerTagInputCurrent struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Source          string `json:"source"`
	ManualProtected bool   `json:"manualProtected"`
}

type customerTagInputAllowed struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	SemanticKey     string `json:"semanticKey"`
	Aliases         string `json:"aliases"`
	ConflictGroup   string `json:"conflictGroup"`
	ApplicableScene string `json:"applicableScene"`
}

type customerTagInputMessage struct {
	ID      int64  `json:"id"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

type customerTagModelOutput struct {
	SchemaVersion string                      `json:"schemaVersion"`
	Operations    []customerTagModelOperation `json:"operations"`
}

type customerTagModelOperation struct {
	Op                 string  `json:"op"`
	TagID              int64   `json:"tagId"`
	Replaces           []int64 `json:"replaces"`
	Confidence         float64 `json:"confidence"`
	Persistence        string  `json:"persistence"`
	EvidenceMessageIDs []int64 `json:"evidenceMessageIds"`
	ReasonCode         string  `json:"reasonCode"`
}

type rawCustomerTagModelOutput struct {
	SchemaVersion *string         `json:"schemaVersion"`
	Operations    json.RawMessage `json:"operations"`
}

type rawCustomerTagModelOperation struct {
	Op                 *string         `json:"op"`
	TagID              *int64          `json:"tagId"`
	Replaces           json.RawMessage `json:"replaces"`
	Confidence         *float64        `json:"confidence"`
	Persistence        *string         `json:"persistence"`
	EvidenceMessageIDs json.RawMessage `json:"evidenceMessageIds"`
	ReasonCode         *string         `json:"reasonCode"`
}

func (s *conversationEvolutionService) buildTagInputs(
	run *models.ConversationEvolutionRun,
	state *models.ConversationEvolutionState,
	policy *conversationEvolutionPolicy,
	resolved *ModelCallConfig,
	scope *customerTagScope,
	allowed []models.Tag,
	messages []models.Message,
) ([]customerTagInput, string, error) {
	if run == nil || state == nil || policy == nil || resolved == nil || scope == nil || scope.Relation == nil {
		return nil, "", fmt.Errorf("customer tag evolution scope is incomplete")
	}
	currentRelations, err := repositories.CustomerTagRelationRepository.FindActiveByStoreRelation(
		sqls.DB(), scope.TenantID, scope.StoreID, scope.Relation.ID,
	)
	if err != nil {
		return nil, "", err
	}
	currentTags := make([]customerTagInputCurrent, 0, len(currentRelations))
	for i := range currentRelations {
		tag := repositories.TagRepository.GetInTenant(sqls.DB(), currentRelations[i].TagID, scope.TenantID)
		if tag == nil || tag.Status != enums.StatusOk || tag.IntentProfileID != policy.IntentProfileID || !tag.SystemDefined || tag.TemplateDefinitionID == nil {
			continue
		}
		currentTags = append(currentTags, customerTagInputCurrent{
			ID: tag.ID, Name: tag.Name, Source: currentRelations[i].Source,
			ManualProtected: currentRelations[i].ManualProtected,
		})
	}
	if len(allowed) > conversationEvolutionMaxAllowedTags {
		allowed = allowed[:conversationEvolutionMaxAllowedTags]
	}
	allowedTags := make([]customerTagInputAllowed, 0, len(allowed))
	for i := range allowed {
		allowedTags = append(allowedTags, customerTagInputAllowed{
			ID: allowed[i].ID, Name: allowed[i].Name, SemanticKey: allowed[i].SemanticKey,
			Aliases: allowed[i].Aliases, ConflictGroup: allowed[i].ConflictGroup,
			ApplicableScene: allowed[i].ApplicableScene,
		})
	}
	normalizedMessages := make([]customerTagInputMessage, 0, len(messages))
	for i := range messages {
		role := evolutionMessageRole(messages[i].SenderType)
		if role == "" {
			continue
		}
		content := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(messages[i].MessageType, messages[i].Content, messages[i].Payload))
		content = redactEvolutionSensitiveText(content)
		if content == "" || isContextlessEmoji(content) {
			continue
		}
		normalizedMessages = append(normalizedMessages, customerTagInputMessage{
			ID: messages[i].ID, Role: role, Content: limitRunes(content, conversationEvolutionMaxMessageRunes),
		})
	}
	if !hasCustomerEvolutionMessage(normalizedMessages) || len(allowedTags) == 0 {
		return nil, hashEvolutionInput(currentTags, allowedTags, normalizedMessages), nil
	}
	base := customerTagInput{
		SchemaVersion: "customer_tag_input.v1",
		Run: customerTagInputRun{
			RunID: fmt.Sprintf("ev-%d", run.ID), ModelProfileRevision: resolved.ProfileRevision,
		},
		Scope: customerTagInputScope{
			TenantID: state.TenantID, StoreID: state.StoreID, CustomerID: state.CustomerID,
			StoreCustomerRelationID: state.StoreCustomerRelationID,
			ConversationID:          state.ConversationID, SessionNo: state.SessionNo,
		},
		Checkpoint: customerTagInputCheckpoint{
			PreviousEndMessageID: state.LastEvolvedMessageID, EndMessageID: run.EndMessageID,
		},
		PreviousSummary: s.previousSummary(state.TenantID, state.ConversationID, state.SessionNo),
		CurrentTags:     currentTags, AllowedTags: allowedTags,
	}
	chunks := make([]customerTagInput, 0, 1)
	current := base
	for i := range normalizedMessages {
		candidate := current
		candidate.Messages = append(append([]customerTagInputMessage(nil), current.Messages...), normalizedMessages[i])
		raw, _ := json.Marshal(candidate)
		if exceedsConversationEvolutionInputBudget(string(raw)) && len(current.Messages) > 0 {
			if hasCustomerEvolutionMessage(current.Messages) {
				chunks = append(chunks, current)
			}
			current = base
			current.Messages = []customerTagInputMessage{normalizedMessages[i]}
			continue
		}
		current = candidate
	}
	if len(current.Messages) > 0 && hasCustomerEvolutionMessage(current.Messages) {
		chunks = append(chunks, current)
	}
	return chunks, hashEvolutionInput(base, normalizedMessages), nil
}

func (s *conversationEvolutionService) callTagModel(
	run *models.ConversationEvolutionRun,
	resolved *ModelCallConfig,
	policy *conversationEvolutionPolicy,
	chunkIndex int,
	input customerTagInput,
) ([]CustomerTagOperation, error) {
	rawInput, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	maxAttempts := resolved.MaxRetryCount + 1
	if maxAttempts < 2 {
		maxAttempts = 2
	}
	if maxAttempts > 3 {
		maxAttempts = 3
	}
	runtimeConfig := resolved.RuntimeConfig()
	runtimeConfig.MaxRetryCount = 0
	systemPrompt := strings.TrimSpace(resolved.PromptTemplate)
	if systemPrompt == "" {
		systemPrompt = defaultCustomerTagEvolutionPrompt
	}
	var lastErr error
	previousInvalidOutput := ""
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		overallAttempt := run.RetryCount*3 + attempt
		requestID := fmt.Sprintf("tag-evolution:%d:chunk:%d:attempt:%d", run.ID, chunkIndex, overallAttempt)
		callTimeout := time.Duration(resolved.TimeoutMS)*time.Millisecond + 5*time.Second
		if callTimeout < 10*time.Second {
			callTimeout = 10 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		callCtx := usagex.WithScope(ctx, modelCallUsageScope(resolved, run.ConversationID, run.EndMessageID, requestID))
		callCtx, capture := usagex.WithCapture(callCtx)
		attemptPrompt := customerTagOutputContractPrompt(systemPrompt)
		if previousInvalidOutput != "" {
			attemptPrompt = customerTagRepairPrompt(attemptPrompt, previousInvalidOutput)
		}
		startedAt := time.Now()
		result, callErr := ai.LLM.ChatWithRuntimeConfig(callCtx, runtimeConfig, attemptPrompt, string(rawInput))
		latencyMS := time.Since(startedAt).Milliseconds()
		cancel()
		status := "completed"
		errorClass := ""
		metricSource := AIUsageMetricSourceProviderOperation
		operations := make([]CustomerTagOperation, 0)
		if callErr == nil {
			if result == nil {
				callErr = fmt.Errorf("customer tag model returned no result")
			} else {
				operations, callErr = validateCustomerTagModelOutput(result.Content, input, policy)
				if result.PromptTokens > 0 || result.CompletionTokens > 0 {
					metricSource = AIUsageMetricSourceUpstreamActual
				}
			}
			if callErr != nil {
				status = "invalid_schema"
				errorClass = "customer_tag_schema_invalid"
				if result != nil {
					previousInvalidOutput = limitRunes(result.Content, 2000)
				}
			}
		} else {
			status = "failed"
			errorClass = "customer_tag_model_request_failed"
		}
		event := models.AIUsageEvent{
			EventKey: requestID, ConversationID: run.ConversationID, MessageID: run.EndMessageID,
			RequestID: requestID, Stage: "customer_tag_evolution", OperationType: "customer_tag_extract",
			MetricSource: metricSource, RequestCount: 1, LatencyMS: latencyMS,
			Status: status, ErrorClass: errorClass,
		}
		if result != nil {
			event.PromptTokens = int64(result.PromptTokens)
			event.CompletionTokens = int64(result.CompletionTokens)
		}
		recordResolvedModelCall(event, resolved, lastUsageReceipt(capture))
		if callErr == nil {
			return operations, nil
		}
		lastErr = callErr
	}
	return nil, lastErr
}

func validateCustomerTagModelOutput(
	content string,
	input customerTagInput,
	policy *conversationEvolutionPolicy,
) ([]CustomerTagOperation, error) {
	normalizedContent, err := normalizeCustomerTagModelJSON(content)
	if err != nil {
		return nil, err
	}
	output, err := decodeStrictCustomerTagModelOutput(normalizedContent)
	if err != nil {
		return nil, err
	}
	maxOperations := conversationEvolutionMaxOperations
	minimumConfidence := 0.8
	if policy != nil {
		maxOperations = policy.MaxOperationsPerRun
		minimumConfidence = policy.MinimumConfidence
	}
	if maxOperations <= 0 || maxOperations > conversationEvolutionMaxOperations {
		maxOperations = conversationEvolutionMaxOperations
	}
	if minimumConfidence <= 0 || minimumConfidence > 1 {
		minimumConfidence = 0.8
	}
	if output.SchemaVersion != "customer_tag_evolution.v1" || len(output.Operations) > maxOperations {
		return nil, fmt.Errorf("invalid customer tag schema version or operation count")
	}
	allowed := make(map[int64]struct{}, len(input.AllowedTags))
	for i := range input.AllowedTags {
		allowed[input.AllowedTags[i].ID] = struct{}{}
	}
	evidence := make(map[int64]struct{})
	for i := range input.Messages {
		if input.Messages[i].Role == "customer" {
			evidence[input.Messages[i].ID] = struct{}{}
		}
	}
	ret := make([]CustomerTagOperation, 0, len(output.Operations))
	for i := range output.Operations {
		operation := output.Operations[i]
		if _, ok := allowed[operation.TagID]; !ok {
			return nil, fmt.Errorf("unknown tag id")
		}
		if !validCustomerTagOperation(operation) {
			return nil, fmt.Errorf("invalid customer tag operation")
		}
		for _, id := range operation.EvidenceMessageIDs {
			if _, ok := evidence[id]; !ok {
				return nil, fmt.Errorf("evidence is not an incremental customer message")
			}
		}
		for _, id := range operation.Replaces {
			if _, ok := allowed[id]; !ok {
				return nil, fmt.Errorf("unknown replacement tag id")
			}
		}
		if operation.Persistence != "long_term" {
			continue
		}
		threshold := 0.92
		if operation.Op == "refresh" {
			threshold = 0.85
		}
		if minimumConfidence > threshold {
			threshold = minimumConfidence
		}
		if operation.Confidence < threshold {
			continue
		}
		ret = append(ret, CustomerTagOperation{
			Op: operation.Op, TagID: operation.TagID,
			Replaces:           sortedUniqueEvolutionIDs(operation.Replaces),
			Confidence:         operation.Confidence,
			EvidenceMessageIDs: sortedUniqueEvolutionIDs(operation.EvidenceMessageIDs),
		})
	}
	return ret, nil
}

func decodeStrictCustomerTagModelOutput(content string) (customerTagModelOutput, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(content))
	decoder.DisallowUnknownFields()
	rawOutput := rawCustomerTagModelOutput{}
	if err := decoder.Decode(&rawOutput); err != nil {
		return customerTagModelOutput{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return customerTagModelOutput{}, err
	}
	if rawOutput.SchemaVersion == nil {
		return customerTagModelOutput{}, fmt.Errorf("schemaVersion is required")
	}
	rawOperations, err := decodeRequiredJSONArray(rawOutput.Operations, "operations")
	if err != nil {
		return customerTagModelOutput{}, err
	}
	output := customerTagModelOutput{
		SchemaVersion: *rawOutput.SchemaVersion,
		Operations:    make([]customerTagModelOperation, 0, len(rawOperations)),
	}
	for i := range rawOperations {
		operationDecoder := json.NewDecoder(bytes.NewReader(rawOperations[i]))
		operationDecoder.DisallowUnknownFields()
		rawOperation := rawCustomerTagModelOperation{}
		if err := operationDecoder.Decode(&rawOperation); err != nil {
			return customerTagModelOutput{}, err
		}
		if err := ensureJSONEOF(operationDecoder); err != nil {
			return customerTagModelOutput{}, err
		}
		if rawOperation.Op == nil || rawOperation.TagID == nil || rawOperation.Confidence == nil ||
			rawOperation.Persistence == nil || rawOperation.ReasonCode == nil {
			return customerTagModelOutput{}, fmt.Errorf("customer tag operation is missing required fields")
		}
		replaces, err := decodeRequiredInt64Array(rawOperation.Replaces, "replaces")
		if err != nil {
			return customerTagModelOutput{}, err
		}
		evidenceMessageIDs, err := decodeRequiredInt64Array(rawOperation.EvidenceMessageIDs, "evidenceMessageIds")
		if err != nil {
			return customerTagModelOutput{}, err
		}
		output.Operations = append(output.Operations, customerTagModelOperation{
			Op: *rawOperation.Op, TagID: *rawOperation.TagID, Replaces: replaces,
			Confidence: *rawOperation.Confidence, Persistence: *rawOperation.Persistence,
			EvidenceMessageIDs: evidenceMessageIDs, ReasonCode: *rawOperation.ReasonCode,
		})
	}
	return output, nil
}

func decodeRequiredJSONArray(raw json.RawMessage, fieldName string) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '[' {
		return nil, fmt.Errorf("%s must be an array", fieldName)
	}
	values := []json.RawMessage{}
	if err := json.Unmarshal(trimmed, &values); err != nil {
		return nil, fmt.Errorf("%s must be an array: %w", fieldName, err)
	}
	return values, nil
}

func decodeRequiredInt64Array(raw json.RawMessage, fieldName string) ([]int64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '[' {
		return nil, fmt.Errorf("%s must be an array", fieldName)
	}
	values := []int64{}
	if err := json.Unmarshal(trimmed, &values); err != nil {
		return nil, fmt.Errorf("%s must be an integer array: %w", fieldName, err)
	}
	if values == nil {
		return nil, fmt.Errorf("%s must be an array", fieldName)
	}
	return values, nil
}

func normalizeCustomerTagModelJSON(content string) (string, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lineEnd := strings.IndexByte(content, '\n')
		if lineEnd < 0 {
			return "", fmt.Errorf("invalid customer tag JSON fence")
		}
		opening := strings.ToLower(strings.TrimSpace(content[:lineEnd]))
		if opening != "```" && opening != "```json" {
			return "", fmt.Errorf("unsupported customer tag JSON fence")
		}
		content = strings.TrimSpace(content[lineEnd+1:])
		if !strings.HasSuffix(content, "```") {
			return "", fmt.Errorf("unterminated customer tag JSON fence")
		}
		content = strings.TrimSpace(strings.TrimSuffix(content, "```"))
	}
	if !strings.HasPrefix(content, "{") || !strings.HasSuffix(content, "}") {
		return "", fmt.Errorf("customer tag model output must contain only one JSON object")
	}
	return content, nil
}

func customerTagOutputContractPrompt(configuredPrompt string) string {
	return strings.TrimSpace(configuredPrompt) + `

输出契约不可省略或改名：
- 根对象必须且只能包含 schemaVersion、operations。
- schemaVersion 固定为 customer_tag_evolution.v1。
- 每个 operation 必须且只能包含 op、tagId、replaces、confidence、persistence、evidenceMessageIds、reasonCode。
- op 只能是 add、refresh、replace、remove。
- tagId 和 replaces 只能使用 allowedTags 中的整数 id。
- replaces 即使为空也必须输出 []，禁止输出 null。
- evidenceMessageIds 只能使用本次 messages 中 role=customer 的整数 id。
- confidence 必须是 0 到 1 的数字；persistence 只能是 long_term、temporary、unclear。
- reasonCode 只能是 explicit_preference、repeated_preference、semantic_merge、explicit_reversal。
- 无符合条件的长期标签时返回 {"schemaVersion":"customer_tag_evolution.v1","operations":[]}。
- 禁止使用 action、id 等简化字段，禁止 Markdown 代码块和解释文字。`
}

func customerTagRepairPrompt(basePrompt, previousOutput string) string {
	return basePrompt + `

上一次输出未通过严格 Schema 校验。请重新阅读输入并完整重写 JSON，不要解释。
上一次错误输出仅用于识别格式问题，不得直接照抄：
` + previousOutput
}

func validCustomerTagOperation(operation customerTagModelOperation) bool {
	switch operation.Op {
	case "add", "refresh", "replace", "remove":
	default:
		return false
	}
	if operation.TagID <= 0 || operation.Confidence < 0 || operation.Confidence > 1 {
		return false
	}
	if operation.Persistence != "long_term" && operation.Persistence != "temporary" && operation.Persistence != "unclear" {
		return false
	}
	switch operation.ReasonCode {
	case "explicit_preference", "repeated_preference", "semantic_merge", "explicit_reversal":
	default:
		return false
	}
	if len(operation.EvidenceMessageIDs) < 1 || len(operation.EvidenceMessageIDs) > 5 || len(operation.Replaces) > 5 {
		return false
	}
	if operation.Op == "replace" && len(operation.Replaces) == 0 {
		return false
	}
	if operation.Op == "remove" && operation.ReasonCode != "explicit_reversal" {
		return false
	}
	return len(sortedUniqueEvolutionIDs(operation.EvidenceMessageIDs)) == len(operation.EvidenceMessageIDs) &&
		len(sortedUniqueEvolutionIDs(operation.Replaces)) == len(operation.Replaces)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("multiple JSON values")
	}
	return err
}

func (s *conversationEvolutionService) previousSummary(tenantID, conversationID int64, sessionNo int) string {
	current := repositories.ConversationSessionSummaryRepository.FindOne(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).Eq("conversation_id", conversationID).Eq("session_no", sessionNo))
	if current == nil {
		return ""
	}
	return limitRunes(strings.TrimSpace(strings.Join([]string{
		current.StableFacts, current.OpenIssues, current.CustomerPreferences, current.MediaSummary,
	}, "\n")), 200)
}

func mergeCustomerTagOperations(list []CustomerTagOperation) []CustomerTagOperation {
	type operationKey struct {
		Op    string
		TagID int64
	}
	merged := make(map[operationKey]CustomerTagOperation)
	order := make([]operationKey, 0)
	for i := range list {
		key := operationKey{Op: list[i].Op, TagID: list[i].TagID}
		current, exists := merged[key]
		if !exists {
			merged[key] = list[i]
			order = append(order, key)
			continue
		}
		current.EvidenceMessageIDs = sortedUniqueEvolutionIDs(append(current.EvidenceMessageIDs, list[i].EvidenceMessageIDs...))
		current.Replaces = sortedUniqueEvolutionIDs(append(current.Replaces, list[i].Replaces...))
		if list[i].Confidence > current.Confidence {
			current.Confidence = list[i].Confidence
		}
		merged[key] = current
	}
	ret := make([]CustomerTagOperation, 0, len(order))
	for _, key := range order {
		ret = append(ret, merged[key])
	}
	return ret
}

func sortedUniqueEvolutionIDs(values []int64) []int64 {
	ret := uniquePositive(values)
	sort.Slice(ret, func(i, j int) bool { return ret[i] < ret[j] })
	return ret
}

func evolutionMessageRole(senderType enums.IMSenderType) string {
	switch senderType {
	case enums.IMSenderTypeCustomer:
		return "customer"
	case enums.IMSenderTypeAgent:
		return "agent"
	case enums.IMSenderTypeAI:
		return "assistant"
	default:
		return ""
	}
}

func hasCustomerEvolutionMessage(messages []customerTagInputMessage) bool {
	for i := range messages {
		if messages[i].Role == "customer" {
			return true
		}
	}
	return false
}

func hasPotentialCustomerEvolutionMessage(messages []models.Message) bool {
	for i := range messages {
		if messages[i].SenderType != enums.IMSenderTypeCustomer {
			continue
		}
		content := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(messages[i].MessageType, messages[i].Content, messages[i].Payload))
		content = redactEvolutionSensitiveText(content)
		if content != "" && !isContextlessEmoji(content) {
			return true
		}
	}
	return false
}

func evolveSummaryFields(messages []models.Message, stable, issues, preferences, media string) (string, string, string, string) {
	for i := range messages {
		text := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(messages[i].MessageType, messages[i].Content, messages[i].Payload))
		if text == "" {
			continue
		}
		if messages[i].SenderType == enums.IMSenderTypeCustomer {
			if containsEvolutionPreference(text) {
				preferences = appendSummaryLine(preferences, text, 200)
			}
			if containsEvolutionIssue(text) {
				issues = appendSummaryLine(issues, text, 200)
			}
			if containsEvolutionStableFact(text) {
				stable = appendSummaryLine(stable, text, 200)
			}
		}
		if _, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(messages[i].Payload); status == "understood" && strings.TrimSpace(mediaSummary) != "" {
			media = appendSummaryLine(media, mediaSummary, 200)
		}
	}
	return stable, issues, preferences, media
}

func hashEvolutionInput(values ...any) string {
	raw, _ := json.Marshal(values)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

var evolutionPhonePattern = regexp.MustCompile(`(?i)(?:\+?86[- ]?)?1[3-9][0-9]{9}`)
var evolutionEmailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)

func redactEvolutionSensitiveText(value string) string {
	value = evolutionPhonePattern.ReplaceAllString(value, "[手机号已脱敏]")
	value = evolutionEmailPattern.ReplaceAllString(value, "[邮箱已脱敏]")
	return strings.TrimSpace(value)
}

func isContextlessEmoji(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	for _, r := range value {
		if r >= 0x4E00 && r <= 0x9FFF || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return false
		}
	}
	return len([]rune(value)) <= 4
}

func limitRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}

func appendSummaryLine(current, value string, limit int) string {
	value = limitRunes(strings.Join(strings.Fields(strings.TrimSpace(value)), " "), 80)
	if value == "" || strings.Contains(current, value) {
		return limitRunes(current, limit)
	}
	if strings.TrimSpace(current) == "" {
		return limitRunes(value, limit)
	}
	return limitRunes(current+"\n"+value, limit)
}

func containsEvolutionPreference(value string) bool {
	for _, token := range []string{"喜欢", "偏好", "不要", "不喜欢", "每次", "习惯", "安静", "无烟", "高楼层", "低楼层", "靠窗"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func containsEvolutionIssue(value string) bool {
	for _, token := range []string{"还没", "打不开", "连不上", "投诉", "人工", "坏了", "受伤", "流血"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func containsEvolutionStableFact(value string) bool {
	for _, token := range []string{"发票", "车牌", "入住", "退房", "公司抬头"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func estimateEvolutionTokens(value string) int {
	estimated := estimateEvolutionTokensUncapped(value)
	if estimated > conversationEvolutionMaxInputTokens {
		return conversationEvolutionMaxInputTokens
	}
	return estimated
}

func estimateEvolutionTokensUncapped(value string) int {
	runes := len([]rune(strings.TrimSpace(value)))
	if runes == 0 {
		return 0
	}
	return runes/2 + 1
}

func exceedsConversationEvolutionInputBudget(value string) bool {
	return estimateEvolutionTokensUncapped(value)+conversationEvolutionPromptTokenReserve > conversationEvolutionMaxInputTokens
}

const defaultCustomerTagEvolutionPrompt = `你是客户长期偏好标签抽取器。只能使用 allowedTags 中的 tagId。
只根据增量客户消息提取稳定长期偏好；临时请求、模糊信息或无变化必须返回空 operations。
不得创造标签、输出解释、使用客服或 AI 消息作为证据、推断敏感属性。
输出必须是严格的 customer_tag_evolution.v1 JSON。`
