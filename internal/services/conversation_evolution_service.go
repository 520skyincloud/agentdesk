package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
)

const (
	conversationEvolutionIdleWindow         = 24 * time.Hour
	conversationEvolutionMaxInputTokens     = 4000
	conversationEvolutionPromptTokenReserve = 500
	conversationEvolutionMaxMessageRunes    = 500
	conversationEvolutionMaxAllowedTags     = 200
	conversationEvolutionMaxOperations      = 10
	conversationEvolutionStatusWaiting      = "waiting"
	conversationEvolutionStatusProcessing   = "processing"
	conversationEvolutionStatusCompleted    = "completed"
	conversationEvolutionStatusFailed       = "failed"
	conversationEvolutionStatusBlocked      = "blocked"
	conversationEvolutionStatusSuperseded   = "superseded"
)

var ConversationEvolutionService = newConversationEvolutionService()

type conversationEvolutionService struct{}

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
	RunID            string `json:"runId"`
	TemplateRevision int64  `json:"templateRevision"`
}

type customerTagInputScope struct {
	CompanyID      int64 `json:"companyId"`
	StoreID        int64 `json:"storeId"`
	CustomerID     int64 `json:"customerId"`
	ConversationID int64 `json:"conversationId"`
	SessionNo      int   `json:"sessionNo"`
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

func newConversationEvolutionService() *conversationEvolutionService {
	return &conversationEvolutionService{}
}

// ObserveCommittedMessage only advances the durable inactivity deadline. It
// never invokes a model and is safe to call for every committed message.
func (s *conversationEvolutionService) ObserveCommittedMessage(conversation *models.Conversation, message *models.Message) {
	if conversation == nil || message == nil || conversation.ID <= 0 || message.ID <= 0 || conversation.CustomerID <= 0 {
		return
	}
	scope, err := CustomerTagService.resolveConversationScope(sqls.DB(), conversation.ID, true)
	if err != nil || scope.Relation == nil {
		return
	}
	sessionNo := message.SessionNo
	if sessionNo <= 0 {
		sessionNo = 1
	}
	messageAt := message.CreatedAt
	if message.SentAt != nil {
		messageAt = *message.SentAt
	}
	if messageAt.IsZero() {
		messageAt = time.Now()
	}
	now := time.Now()
	item := &models.ConversationEvolutionState{
		ConversationID: conversation.ID, SessionNo: sessionNo,
		CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: conversation.CustomerID,
		StoreCustomerRelationID: scope.Relation.ID, LastObservedMessageID: message.ID,
		NextEvolutionAt: evolutionTimePtr(messageAt.Add(conversationEvolutionIdleWindow)),
		LastStatus:      conversationEvolutionStatusWaiting, Status: enums.StatusOk,
		AuditFields: models.AuditFields{
			CreatedAt: now, UpdatedAt: now,
			CreateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName,
			UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
		},
	}
	if err := repositories.ConversationEvolutionStateRepository.Upsert(sqls.DB(), item); err != nil {
		slog.Warn("observe conversation evolution message failed",
			"conversation_id", conversation.ID, "message_id", message.ID, "error", err)
	}
}

func (s *conversationEvolutionService) ProcessDue(limit int) int {
	template := repositories.ModelProfileTemplateRepository.Get(sqls.DB())
	if template == nil || !template.CustomerTagEvolutionEnabled || template.Status != "active" {
		return 0
	}
	storeIDs := decodeModelProfileStoreIDs(template.CustomerTagEvolutionStoreIDs)
	if len(storeIDs) == 0 {
		return 0
	}
	items := repositories.ConversationEvolutionStateRepository.FindDue(sqls.DB(), time.Now(), storeIDs, limit)
	processed := 0
	for index := range items {
		if s.claim(&items[index]) {
			s.processState(&items[index])
			processed++
		}
	}
	return processed
}

func (s *conversationEvolutionService) Retry(req request.RetryConversationEvolutionRequest, operator *dto.AuthPrincipal) error {
	scope, err := CustomerTagService.resolveConversationScope(sqls.DB(), req.ConversationID, false)
	if err != nil {
		return err
	}
	if err := CustomerTagService.requireStoreAccess(scope.StoreID, operator); err != nil {
		return err
	}
	state := repositories.ConversationEvolutionStateRepository.GetByConversationSession(
		sqls.DB(), req.ConversationID, ConversationRouteService.CurrentSessionNo(req.ConversationID))
	if state == nil {
		return errorsx.InvalidParam("当前会话暂无可重试的知识进化任务")
	}
	now := time.Now()
	return repositories.ConversationEvolutionStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
		"next_evolution_at": now, "last_status": conversationEvolutionStatusWaiting,
		"last_error_class": "", "updated_at": now,
		"update_user_id": operator.UserID, "update_user_name": operator.Username,
	})
}

func (s *conversationEvolutionService) claim(state *models.ConversationEvolutionState) bool {
	if state == nil {
		return false
	}
	result := sqls.DB().Model(&models.ConversationEvolutionState{}).
		Where("id = ? AND last_status <> ? AND last_observed_message_id > last_evolved_message_id", state.ID, conversationEvolutionStatusProcessing).
		Updates(map[string]any{
			"last_status": conversationEvolutionStatusProcessing,
			"updated_at":  time.Now(), "update_user_name": constants.SystemAuditUserName,
		})
	return result.Error == nil && result.RowsAffected == 1
}

func (s *conversationEvolutionService) processState(state *models.ConversationEvolutionState) {
	conversation := repositories.ConversationRepository.Get(sqls.DB(), state.ConversationID)
	if conversation == nil {
		s.failState(state, "conversation_missing", 24*time.Hour)
		return
	}
	latest := repositories.MessageRepository.FindOne(sqls.DB(), sqls.NewCnd().
		Eq("conversation_id", state.ConversationID).
		Eq("session_no", state.SessionNo).
		Desc("id"))
	if latest == nil || latest.ID <= state.LastEvolvedMessageID {
		s.completeWithoutModel(state, latest)
		return
	}
	latestAt := latest.CreatedAt
	if latest.SentAt != nil {
		latestAt = *latest.SentAt
	}
	if time.Since(latestAt) < conversationEvolutionIdleWindow {
		next := latestAt.Add(conversationEvolutionIdleWindow)
		_ = repositories.ConversationEvolutionStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
			"last_observed_message_id": latest.ID, "next_evolution_at": next,
			"last_status": conversationEvolutionStatusWaiting, "updated_at": time.Now(),
		})
		return
	}
	existing := repositories.ConversationEvolutionRunRepository.GetByCheckpoint(sqls.DB(), state.ConversationID, state.SessionNo, latest.ID)
	if existing != nil && existing.RunStatus == conversationEvolutionStatusCompleted {
		s.finishState(state, existing.ID, latest.ID, existing.SummaryStatus == "completed")
		return
	}
	run := existing
	now := time.Now()
	if run == nil {
		run = &models.ConversationEvolutionRun{
			RunKey:         fmt.Sprintf("conversation-evolution:%d:%d:%d", state.ConversationID, state.SessionNo, latest.ID),
			ConversationID: state.ConversationID, SessionNo: state.SessionNo, EndMessageID: latest.ID,
			CompanyID: state.CompanyID, StoreID: state.StoreID, CustomerID: state.CustomerID,
			RunStatus: conversationEvolutionStatusProcessing, SummaryStatus: "pending",
			KnowledgeStatus: "pending", TagStatus: "pending", StartedAt: &now,
			AuditFields: models.AuditFields{
				CreatedAt: now, UpdatedAt: now,
				CreateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName,
				UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
			},
		}
		if err := repositories.ConversationEvolutionRunRepository.Create(sqls.DB(), run); err != nil {
			s.failState(state, "run_create_failed", time.Hour)
			return
		}
	} else {
		run.RetryCount++
		_ = repositories.ConversationEvolutionRunRepository.Updates(sqls.DB(), run.ID, map[string]any{
			"run_status": conversationEvolutionStatusProcessing, "retry_count": run.RetryCount,
			"started_at": now, "finished_at": nil, "last_error_class": "", "updated_at": now,
		})
	}

	messages := s.incrementalMessages(state.ConversationID, state.SessionNo, state.LastEvolvedMessageID, latest.ID)
	if len(messages) == 0 {
		s.completeRunAndState(run, state, latest.ID, "skipped", "skipped", "skipped", 0)
		return
	}

	summaryStatus := s.updateSessionSummary(conversation, state, latest, messages)
	knowledgeStatus := s.evolveKnowledge(state.ConversationID)
	_ = repositories.ConversationEvolutionRunRepository.Updates(sqls.DB(), run.ID, map[string]any{
		"summary_status": summaryStatus, "knowledge_status": knowledgeStatus, "updated_at": time.Now(),
	})
	if !hasPotentialCustomerEvolutionMessage(messages) || len(CustomerTagService.ListAllowedTags(state.CompanyID)) == 0 {
		s.completeRunAndState(run, state, latest.ID, summaryStatus, knowledgeStatus, "skipped", 0)
		return
	}

	resolved, err := ModelProfileTemplateService.ResolveSlot(state.StoreID, ModelProfileUsageCustomerTag)
	if err != nil {
		s.markRunFailed(run, "store_credential_or_tag_slot_unavailable", "blocked")
		s.failState(state, "store_credential_or_tag_slot_unavailable", time.Hour)
		return
	}
	run.TemplateRevision = resolved.Template.Revision
	run.CredentialRevision = resolved.CredentialRevision

	inputs, inputHash := s.buildTagInputs(run, state, resolved, messages)
	run.InputHash = inputHash
	run.ChunkCount = len(inputs)
	_ = repositories.ConversationEvolutionRunRepository.Updates(sqls.DB(), run.ID, map[string]any{
		"input_hash": inputHash, "template_revision": resolved.Template.Revision,
		"credential_revision": resolved.CredentialRevision, "chunk_count": len(inputs),
		"summary_status": summaryStatus, "knowledge_status": knowledgeStatus, "updated_at": time.Now(),
	})

	operations := make([]CustomerTagOperation, 0)
	tagStatus := "skipped"
	if len(inputs) > 0 {
		tagStatus = "completed"
		for chunkIndex, input := range inputs {
			chunkOperations, callErr := s.callTagModel(context.Background(), run, resolved, chunkIndex+1, input)
			if callErr != nil {
				tagStatus = "failed"
				break
			}
			operations = append(operations, chunkOperations...)
		}
	}
	if s.hasNewerMessage(state.ConversationID, state.SessionNo, latest.ID) {
		finishedAt := time.Now()
		_ = repositories.ConversationEvolutionRunRepository.Updates(sqls.DB(), run.ID, map[string]any{
			"run_status": conversationEvolutionStatusSuperseded, "tag_status": conversationEvolutionStatusSuperseded,
			"finished_at": finishedAt, "updated_at": finishedAt,
		})
		s.rescheduleAfterSuperseded(state)
		return
	}
	if tagStatus == "failed" {
		s.markRunFailed(run, "customer_tag_model_failed", "failed")
		s.failState(state, "customer_tag_model_failed", time.Hour)
		return
	}
	changed := false
	if len(operations) > 0 {
		changed, err = CustomerTagService.ApplyAI(state.ConversationID, run.ID, mergeCustomerTagOperations(operations))
		if err != nil {
			s.markRunFailed(run, "customer_tag_apply_failed", "failed")
			s.failState(state, "customer_tag_apply_failed", time.Hour)
			return
		}
	}
	s.completeRunAndState(run, state, latest.ID, summaryStatus, knowledgeStatus, tagStatus, len(operations))
	if changed {
		WsService.PublishCustomerTagChanged(state.ConversationID, state.StoreID, state.CustomerID, time.Now())
	}
}

func (s *conversationEvolutionService) incrementalMessages(conversationID int64, sessionNo int, afterMessageID int64, endMessageID int64) []models.Message {
	return repositories.MessageRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("session_no", sessionNo).
		Gt("id", afterMessageID).
		Lte("id", endMessageID).
		Asc("id"))
}

func (s *conversationEvolutionService) buildTagInputs(run *models.ConversationEvolutionRun, state *models.ConversationEvolutionState, resolved *ResolvedModelProfileSlot, messages []models.Message) ([]customerTagInput, string) {
	scope, err := CustomerTagService.resolveConversationScope(sqls.DB(), state.ConversationID, false)
	if err != nil || scope.Relation == nil {
		return nil, ""
	}
	currentRelations := repositories.CustomerTagRelationRepository.FindActiveByRelationID(sqls.DB(), scope.Relation.ID)
	currentTags := make([]customerTagInputCurrent, 0, len(currentRelations))
	for _, relation := range currentRelations {
		tag := repositories.TagRepository.Get(sqls.DB(), relation.TagID)
		if tag == nil || tag.Status != enums.StatusOk {
			continue
		}
		currentTags = append(currentTags, customerTagInputCurrent{
			ID: tag.ID, Name: tag.Name, Source: relation.Source, ManualProtected: relation.ManualProtected,
		})
	}
	allowed := CustomerTagService.ListAllowedTags(state.CompanyID)
	if len(allowed) > conversationEvolutionMaxAllowedTags {
		allowed = allowed[:conversationEvolutionMaxAllowedTags]
	}
	allowedTags := make([]customerTagInputAllowed, 0, len(allowed))
	for _, tag := range allowed {
		allowedTags = append(allowedTags, customerTagInputAllowed{
			ID: tag.ID, Name: tag.Name, SemanticKey: tag.SemanticKey,
			Aliases: tag.Aliases, ConflictGroup: tag.ConflictGroup,
			ApplicableScene: tag.ApplicableScene,
		})
	}
	normalizedMessages := make([]customerTagInputMessage, 0, len(messages))
	for _, message := range messages {
		role := evolutionMessageRole(message.SenderType)
		if role == "" {
			continue
		}
		content := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
		content = redactEvolutionSensitiveText(content)
		if content == "" || isContextlessEmoji(content) {
			continue
		}
		content = limitRunes(content, conversationEvolutionMaxMessageRunes)
		normalizedMessages = append(normalizedMessages, customerTagInputMessage{ID: message.ID, Role: role, Content: content})
	}
	if !hasCustomerEvolutionMessage(normalizedMessages) || len(allowedTags) == 0 {
		return nil, hashEvolutionInput(currentTags, allowedTags, normalizedMessages)
	}
	previousSummary := s.previousSummary(state.ConversationID, state.SessionNo)
	base := customerTagInput{
		SchemaVersion: "customer_tag_input.v1",
		Run:           customerTagInputRun{RunID: fmt.Sprintf("ev-%d", run.ID), TemplateRevision: resolved.Template.Revision},
		Scope: customerTagInputScope{
			CompanyID: state.CompanyID, StoreID: state.StoreID, CustomerID: state.CustomerID,
			ConversationID: state.ConversationID, SessionNo: state.SessionNo,
		},
		Checkpoint: customerTagInputCheckpoint{
			PreviousEndMessageID: state.LastEvolvedMessageID, EndMessageID: run.EndMessageID,
		},
		PreviousSummary: previousSummary, CurrentTags: currentTags, AllowedTags: allowedTags,
	}
	chunks := make([]customerTagInput, 0, 1)
	current := base
	for _, message := range normalizedMessages {
		candidate := current
		candidate.Messages = append(append([]customerTagInputMessage(nil), current.Messages...), message)
		raw, _ := json.Marshal(candidate)
		if exceedsConversationEvolutionInputBudget(string(raw)) && len(current.Messages) > 0 {
			if hasCustomerEvolutionMessage(current.Messages) {
				chunks = append(chunks, current)
			}
			current = base
			current.Messages = []customerTagInputMessage{message}
			continue
		}
		current = candidate
	}
	if len(current.Messages) > 0 && hasCustomerEvolutionMessage(current.Messages) {
		chunks = append(chunks, current)
	}
	return chunks, hashEvolutionInput(base, normalizedMessages)
}

func (s *conversationEvolutionService) callTagModel(ctx context.Context, run *models.ConversationEvolutionRun, resolved *ResolvedModelProfileSlot, chunkIndex int, input customerTagInput) ([]CustomerTagOperation, error) {
	rawInput, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	maxAttempts := resolved.Slot.MaxRetryCount + 1
	if maxAttempts < 2 {
		maxAttempts = 2
	}
	if maxAttempts > 3 {
		maxAttempts = 3
	}
	config := resolved.Config
	config.MaxRetryCount = 0
	systemPrompt := strings.TrimSpace(resolved.Slot.PromptTemplate)
	if systemPrompt == "" {
		systemPrompt = defaultCustomerTagEvolutionPrompt
	}
	var lastErr error
	var previousInvalidOutput string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		overallAttempt := run.RetryCount*3 + attempt
		requestID := "tag-evolution-" + strings.ReplaceAll(uuid.NewString(), "-", "")
		callCtx := usagex.WithScope(ctx, usagex.Scope{
			CompanyID: run.CompanyID, StoreID: run.StoreID, ConversationID: run.ConversationID,
			MessageID: run.EndMessageID, RequestID: requestID,
			CredentialRevision: resolved.CredentialRevision, ModelSource: "store_credential_tag_template",
		})
		callCtx, capture := usagex.WithCapture(callCtx)
		startedAt := time.Now()
		attemptPrompt := customerTagOutputContractPrompt(systemPrompt)
		if previousInvalidOutput != "" {
			attemptPrompt = customerTagRepairPrompt(attemptPrompt, previousInvalidOutput)
		}
		result, callErr := ai.LLM.ChatWithConfig(callCtx, config, attemptPrompt, string(rawInput))
		latencyMS := time.Since(startedAt).Milliseconds()
		status := "completed"
		errorClass := ""
		var operations []CustomerTagOperation
		if callErr == nil {
			if result == nil {
				callErr = fmt.Errorf("customer tag model returned no result")
			} else {
				operations, callErr = validateCustomerTagModelOutput(result.Content, input)
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
		record := ai.ModelUsageRecord{
			Stage: "customer_tag_evolution", OperationType: "customer_tag_extract",
			Config: resolved.Config, LatencyMS: latencyMS, Status: status, ErrorClass: errorClass,
			Receipt:          lastUsageReceipt(capture),
			ExternalEventKey: fmt.Sprintf("tag-evolution:%d:chunk:%d:attempt:%d", run.ID, chunkIndex, overallAttempt),
		}
		if result != nil {
			record.PromptTokens = int64(result.PromptTokens)
			record.CompletionTokens = int64(result.CompletionTokens)
		}
		ai.RecordModelUsage(callCtx, record)
		if callErr == nil {
			return operations, nil
		}
		lastErr = callErr
	}
	return nil, lastErr
}

func validateCustomerTagModelOutput(content string, input customerTagInput) ([]CustomerTagOperation, error) {
	normalizedContent, err := normalizeCustomerTagModelJSON(content)
	if err != nil {
		return nil, err
	}
	output, err := decodeStrictCustomerTagModelOutput(normalizedContent)
	if err != nil {
		return nil, err
	}
	if output.SchemaVersion != "customer_tag_evolution.v1" || len(output.Operations) > conversationEvolutionMaxOperations {
		return nil, fmt.Errorf("invalid customer tag schema version or operation count")
	}
	allowed := make(map[int64]struct{}, len(input.AllowedTags))
	for _, tag := range input.AllowedTags {
		allowed[tag.ID] = struct{}{}
	}
	evidence := make(map[int64]struct{})
	for _, message := range input.Messages {
		if message.Role == "customer" {
			evidence[message.ID] = struct{}{}
		}
	}
	ret := make([]CustomerTagOperation, 0, len(output.Operations))
	for _, operation := range output.Operations {
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
		if operation.Confidence < threshold {
			continue
		}
		ret = append(ret, CustomerTagOperation{
			Op: operation.Op, TagID: operation.TagID, Replaces: uniqueInt64s(operation.Replaces),
			Confidence: operation.Confidence, EvidenceMessageIDs: uniqueInt64s(operation.EvidenceMessageIDs),
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
	for _, rawOperationJSON := range rawOperations {
		operationDecoder := json.NewDecoder(bytes.NewReader(rawOperationJSON))
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

func customerTagRepairPrompt(basePrompt string, previousOutput string) string {
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
	return len(uniqueInt64s(operation.EvidenceMessageIDs)) == len(operation.EvidenceMessageIDs) &&
		len(uniqueInt64s(operation.Replaces)) == len(operation.Replaces)
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

func (s *conversationEvolutionService) updateSessionSummary(conversation *models.Conversation, state *models.ConversationEvolutionState, latest *models.Message, messages []models.Message) string {
	if conversation == nil || latest == nil {
		return "skipped"
	}
	current := repositories.ConversationSessionSummaryRepository.FindOne(sqls.DB(), sqls.NewCnd().
		Eq("conversation_id", conversation.ID).
		Eq("session_no", state.SessionNo))
	stable := ""
	issues := ""
	preferences := ""
	media := ""
	messageCount := len(messages)
	if current != nil {
		stable = current.StableFacts
		issues = current.OpenIssues
		preferences = current.CustomerPreferences
		media = current.MediaSummary
		messageCount += current.MessageCount
	}
	for _, message := range messages {
		text := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
		if text == "" {
			continue
		}
		if message.SenderType == enums.IMSenderTypeCustomer {
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
		if _, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload); status == "understood" && strings.TrimSpace(mediaSummary) != "" {
			media = appendSummaryLine(media, mediaSummary, 200)
		}
	}
	now := time.Now()
	wxWorkInstanceID := int64(0)
	if route := repositories.ConversationRouteStateRepository.Take(sqls.DB(), "conversation_id = ?", conversation.ID); route != nil {
		wxWorkInstanceID = route.WxWorkInstanceID
	}
	columns := map[string]any{
		"wx_work_instance_id": wxWorkInstanceID, "store_id": state.StoreID,
		"customer_id": state.CustomerID, "stable_facts": stable, "open_issues": issues,
		"customer_preferences": preferences, "media_summary": media,
		"message_count": messageCount, "token_estimate": estimateEvolutionTokens(stable + issues + preferences + media),
		"last_message_id": latest.ID, "status": enums.StatusOk,
		"updated_at": now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
	}
	if current != nil {
		if err := repositories.ConversationSessionSummaryRepository.Updates(sqls.DB(), current.ID, columns); err != nil {
			return "failed"
		}
		return "completed"
	}
	item := &models.ConversationSessionSummary{
		ConversationID: conversation.ID, SessionNo: state.SessionNo,
		WxWorkInstanceID: wxWorkInstanceID,
		StoreID:          state.StoreID, CustomerID: state.CustomerID,
		StableFacts: stable, OpenIssues: issues, CustomerPreferences: preferences, MediaSummary: media,
		MessageCount: messageCount, TokenEstimate: estimateEvolutionTokens(stable + issues + preferences + media),
		LastMessageID: latest.ID, Status: enums.StatusOk,
		AuditFields: models.AuditFields{
			CreatedAt: now, UpdatedAt: now,
			CreateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName,
			UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
		},
	}
	if err := repositories.ConversationSessionSummaryRepository.Create(sqls.DB(), item); err != nil {
		return "failed"
	}
	return "completed"
}

func (s *conversationEvolutionService) evolveKnowledge(conversationID int64) string {
	if _, err := KnowledgeCandidateService.ExtractFromResolvedConversation(conversationID, enums.KnowledgeCandidateSourceAgentDeskHQ); err != nil {
		return "failed"
	}
	return "completed"
}

func (s *conversationEvolutionService) previousSummary(conversationID int64, sessionNo int) string {
	current := repositories.ConversationSessionSummaryRepository.FindOne(sqls.DB(), sqls.NewCnd().
		Eq("conversation_id", conversationID).Eq("session_no", sessionNo))
	if current == nil {
		return ""
	}
	return limitRunes(strings.TrimSpace(strings.Join([]string{
		current.StableFacts, current.OpenIssues, current.CustomerPreferences, current.MediaSummary,
	}, "\n")), 200)
}

func (s *conversationEvolutionService) hasNewerMessage(conversationID int64, sessionNo int, endMessageID int64) bool {
	latest := repositories.MessageRepository.FindOne(sqls.DB(), sqls.NewCnd().
		Eq("conversation_id", conversationID).Eq("session_no", sessionNo).Desc("id"))
	return latest != nil && latest.ID != endMessageID
}

func (s *conversationEvolutionService) rescheduleAfterSuperseded(state *models.ConversationEvolutionState) {
	if state == nil {
		return
	}
	latest := repositories.MessageRepository.FindOne(sqls.DB(), sqls.NewCnd().
		Eq("conversation_id", state.ConversationID).
		Eq("session_no", state.SessionNo).
		Desc("id"))
	if latest == nil {
		s.failState(state, "superseded_without_latest_message", time.Hour)
		return
	}
	latestAt := latest.CreatedAt
	if latest.SentAt != nil {
		latestAt = *latest.SentAt
	}
	if latestAt.IsZero() {
		latestAt = time.Now()
	}
	_ = repositories.ConversationEvolutionStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
		"last_observed_message_id": latest.ID,
		"next_evolution_at":        latestAt.Add(conversationEvolutionIdleWindow),
		"last_status":              conversationEvolutionStatusWaiting,
		"last_error_class":         "",
		"updated_at":               time.Now(),
	})
}

func (s *conversationEvolutionService) completeRunAndState(run *models.ConversationEvolutionRun, state *models.ConversationEvolutionState, endMessageID int64, summaryStatus, knowledgeStatus, tagStatus string, operationCount int) {
	finishedAt := time.Now()
	redactedResult, _ := json.Marshal(map[string]any{"operationCount": operationCount})
	_ = repositories.ConversationEvolutionRunRepository.Updates(sqls.DB(), run.ID, map[string]any{
		"run_status": conversationEvolutionStatusCompleted, "summary_status": summaryStatus,
		"knowledge_status": knowledgeStatus, "tag_status": tagStatus,
		"redacted_result": string(redactedResult), "finished_at": finishedAt,
		"last_error_class": "", "updated_at": finishedAt,
	})
	s.finishState(state, run.ID, endMessageID, summaryStatus == "completed")
}

func (s *conversationEvolutionService) finishState(state *models.ConversationEvolutionState, runID int64, endMessageID int64, summaryCompleted bool) {
	now := time.Now()
	updates := map[string]any{
		"last_evolved_message_id": endMessageID, "last_evolution_run_id": runID,
		"next_evolution_at": nil, "last_status": conversationEvolutionStatusCompleted,
		"last_error_class": "", "updated_at": now,
		"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
	}
	if summaryCompleted {
		updates["summary_version"] = state.SummaryVersion + 1
	}
	_ = repositories.ConversationEvolutionStateRepository.Updates(sqls.DB(), state.ID, updates)
}

func (s *conversationEvolutionService) completeWithoutModel(state *models.ConversationEvolutionState, latest *models.Message) {
	endMessageID := state.LastObservedMessageID
	if latest != nil {
		endMessageID = latest.ID
	}
	_ = repositories.ConversationEvolutionStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
		"last_evolved_message_id": endMessageID, "next_evolution_at": nil,
		"last_status": conversationEvolutionStatusCompleted, "last_error_class": "", "updated_at": time.Now(),
	})
}

func (s *conversationEvolutionService) markRunFailed(run *models.ConversationEvolutionRun, errorClass, tagStatus string) {
	now := time.Now()
	_ = repositories.ConversationEvolutionRunRepository.Updates(sqls.DB(), run.ID, map[string]any{
		"run_status": conversationEvolutionStatusFailed, "tag_status": tagStatus,
		"last_error_class": errorClass, "finished_at": now, "updated_at": now,
	})
}

func (s *conversationEvolutionService) failState(state *models.ConversationEvolutionState, errorClass string, retryAfter time.Duration) {
	status := conversationEvolutionStatusFailed
	if strings.Contains(errorClass, "credential") || strings.Contains(errorClass, "slot") {
		status = conversationEvolutionStatusBlocked
	}
	next := time.Now().Add(retryAfter)
	_ = repositories.ConversationEvolutionStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
		"last_status": status, "last_error_class": errorClass,
		"next_evolution_at": next, "updated_at": time.Now(),
	})
}

func mergeCustomerTagOperations(list []CustomerTagOperation) []CustomerTagOperation {
	type key struct {
		Op    string
		TagID int64
	}
	merged := make(map[key]CustomerTagOperation)
	order := make([]key, 0)
	for _, item := range list {
		k := key{Op: item.Op, TagID: item.TagID}
		current, exists := merged[k]
		if !exists {
			merged[k] = item
			order = append(order, k)
			continue
		}
		current.EvidenceMessageIDs = uniqueInt64s(append(current.EvidenceMessageIDs, item.EvidenceMessageIDs...))
		current.Replaces = uniqueInt64s(append(current.Replaces, item.Replaces...))
		if item.Confidence > current.Confidence {
			current.Confidence = item.Confidence
		}
		merged[k] = current
	}
	ret := make([]CustomerTagOperation, 0, len(order))
	for _, k := range order {
		ret = append(ret, merged[k])
	}
	return ret
}

func uniqueInt64s(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	ret := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
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
	for _, message := range messages {
		if message.Role == "customer" {
			return true
		}
	}
	return false
}

func hasPotentialCustomerEvolutionMessage(messages []models.Message) bool {
	for _, message := range messages {
		if message.SenderType != enums.IMSenderTypeCustomer {
			continue
		}
		content := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
		content = redactEvolutionSensitiveText(content)
		if content != "" && !isContextlessEmoji(content) {
			return true
		}
	}
	return false
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

func evolutionTimePtr(value time.Time) *time.Time {
	return &value
}

const defaultCustomerTagEvolutionPrompt = `你是客户长期偏好标签抽取器。只能使用 allowedTags 中的 tagId。
只根据增量客户消息提取稳定长期偏好；临时请求、模糊信息或无变化必须返回空 operations。
不得创造标签、输出解释、使用客服或 AI 消息作为证据、推断敏感属性。
输出必须是严格的 customer_tag_evolution.v1 JSON。`
