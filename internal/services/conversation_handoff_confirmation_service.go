package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
)

var ConversationHandoffConfirmationService = newConversationHandoffConfirmationService()

const handoffConfirmationModelTimeout = 2 * time.Second

type conversationHandoffConfirmationService struct{}

type handoffConfirmationPayload struct {
	Reason          string `json:"reason"`
	AIAgentID       int64  `json:"aiAgentId"`
	OriginMessageID int64  `json:"originMessageId"`
	HandoffToken    string `json:"handoffToken"`
	CreatedAt       string `json:"createdAt"`
}

type handoffConfirmationClassifyResult struct {
	Decision   humanHandoffConfirmationDecision
	Confidence float64
	Reason     string
	Source     string
}

type handoffConfirmationClassifierFunc func(ctx context.Context, conversation *models.Conversation, message *models.Message, payload handoffConfirmationPayload, text string) handoffConfirmationClassifyResult

var classifyHumanHandoffConfirmation = classifyHumanHandoffConfirmationWithModel

func newConversationHandoffConfirmationService() *conversationHandoffConfirmationService {
	return &conversationHandoffConfirmationService{}
}

func SetHumanHandoffConfirmationClassifierForTest(classifier func(context.Context, *models.Conversation, *models.Message, string, string) (string, float64, string)) func() {
	previous := classifyHumanHandoffConfirmation
	if classifier == nil {
		return func() {}
	}
	classifyHumanHandoffConfirmation = func(ctx context.Context, conversation *models.Conversation, message *models.Message, payload handoffConfirmationPayload, text string) handoffConfirmationClassifyResult {
		decision, confidence, reason := classifier(ctx, conversation, message, payload.Reason, text)
		return handoffConfirmationClassifyResult{
			Decision:   normalizeHandoffConfirmationDecision(decision),
			Confidence: confidence,
			Reason:     strings.TrimSpace(reason),
			Source:     "test",
		}
	}
	return func() {
		classifyHumanHandoffConfirmation = previous
	}
}

func (s *conversationHandoffConfirmationService) RequestByAI(conversationID int64, aiAgent models.AIAgent, reason string, requestID string) (bool, error) {
	originMessageID := int64(0)
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return false, fmt.Errorf("会话不存在")
	}
	if message := AIManualResumeTaskService.latestCustomerMessage(conversationID, conversation.TenantID); message != nil {
		originMessageID = message.ID
	}
	return s.RequestByAIWithOriginMessage(conversationID, aiAgent, reason, requestID, originMessageID)
}

func (s *conversationHandoffConfirmationService) RequestByAIWithOriginMessage(conversationID int64, aiAgent models.AIAgent, reason string, requestID string, originMessageID int64) (bool, error) {
	unlock := lockConversationHandoff(conversationID)
	defer unlock()

	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return false, fmt.Errorf("会话不存在")
	}
	if err := validateConversationAIAgentTenant(conversation, aiAgent); err != nil {
		return false, err
	}
	if s.alreadyInHumanRoute(conversationID) {
		return true, nil
	}
	handoffToken := handoffConfirmationToken(conversationID, requestID)
	clientMsgID := "ai_handoff_confirm_" + handoffToken
	if existing := repositories.MessageRepository.GetByClientMsgIDInTenant(sqls.DB(), conversationID, conversation.TenantID, clientMsgID); existing != nil {
		return true, nil
	}
	if state := ConversationRouteService.GetByConversationID(conversationID); state != nil && state.PendingAction == string(enums.ConversationPendingActionHumanHandoff) {
		if state.PendingActionExpireAt == nil || time.Now().Before(*state.PendingActionExpireAt) {
			return true, nil
		}
		_ = ConversationRouteService.ClearPendingAction(conversationID)
	}
	payload, _ := json.Marshal(handoffConfirmationPayload{
		Reason:          cleanHumanHandoffReason(reason),
		AIAgentID:       aiAgent.ID,
		OriginMessageID: originMessageID,
		HandoffToken:    handoffToken,
		CreatedAt:       time.Now().Format(time.RFC3339),
	})
	claimed, err := ConversationRouteService.TrySetPendingAction(conversationID, enums.ConversationPendingActionHumanHandoff, string(payload), time.Now().Add(DefaultHandoffConfirmationMinutes*time.Minute))
	if err != nil {
		return false, err
	}
	if !claimed {
		return true, nil
	}
	content := buildHandoffConfirmationPrompt(reason)
	_, err = MessageService.SendAIMessageWithRequestID(conversationID, aiAgent.ID, clientMsgID, enums.IMMessageTypeText, content, "", systemOperator(), requestID)
	return true, err
}

func handoffConfirmationToken(conversationID int64, requestID string) string {
	normalizedRequestID := tracex.NormalizeRequestID(requestID)
	if normalizedRequestID == "" {
		return AIManualResumeTaskService.NewHandoffToken()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", conversationID, normalizedRequestID)))
	return fmt.Sprintf("%x", sum[:16])
}

func (s *conversationHandoffConfirmationService) HandleCustomerMessage(conversation *models.Conversation, message *models.Message) (bool, error) {
	if conversation == nil || message == nil || message.SenderType != enums.IMSenderTypeCustomer {
		return false, nil
	}
	if message.MessageType != enums.IMMessageTypeText && message.MessageType != enums.IMMessageTypeHTML && message.MessageType != enums.IMMessageTypeVoice {
		return false, nil
	}
	state := ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.PendingAction != string(enums.ConversationPendingActionHumanHandoff) {
		return false, nil
	}
	if !WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabledInTenant(conversation.CustomerID, conversation.StoreStaffBindingID, conversation.TenantID) {
		_ = ConversationRouteService.ClearPendingAction(conversation.ID)
		return false, nil
	}
	text := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
	payload := handoffConfirmationPayload{}
	_ = json.Unmarshal([]byte(state.PendingActionPayload), &payload)
	classifyCtx, cancel := context.WithTimeout(context.Background(), handoffConfirmationModelTimeout)
	result := classifyHumanHandoffConfirmation(classifyCtx, conversation, message, payload, text)
	cancel()
	slog.Info("human handoff confirmation classified",
		"conversation_id", conversation.ID,
		"message_id", message.ID,
		"decision", result.Decision,
		"confidence", result.Confidence,
		"source", result.Source,
	)
	decision := result.Decision
	if decision == humanHandoffConfirmationUnknown {
		_ = ConversationRouteService.ClearPendingAction(conversation.ID)
		return false, nil
	}
	_ = markHandoffConfirmationMessage(message, result)
	payloadText, ok, err := ConversationRouteService.ConsumePendingAction(conversation.ID, enums.ConversationPendingActionHumanHandoff, time.Now())
	if err != nil || !ok {
		return false, err
	}
	if decision == humanHandoffConfirmationCancel {
		_, err = MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "ai_handoff_cancel_"+strs.UUID(), enums.IMMessageTypeText, "好，那先不转人工。我继续帮您看这个问题。", "", systemOperator(), message.RequestID)
		return true, err
	}
	payload = handoffConfirmationPayload{}
	_ = json.Unmarshal([]byte(payloadText), &payload)
	aiAgent := s.resolveRuntimeAIAgent(conversation, payload.AIAgentID)
	reason := cleanHumanHandoffReason(payload.Reason)
	if reason == "" {
		reason = "用户确认需要人工接待"
	}
	_, err = ConversationHumanDispatchService.HandoffByAIWithRequestID(conversation.ID, aiAgent, reason, message.RequestID)
	if err == nil {
		if _, scheduleErr := AIManualResumeTaskService.Schedule(conversation.ID, payload.OriginMessageID, payload.HandoffToken); scheduleErr != nil {
			slog.Warn("schedule AI manual resume task failed", "conversation_id", conversation.ID, "origin_message_id", payload.OriginMessageID, "error", scheduleErr)
		}
	}
	return true, err
}

func (s *conversationHandoffConfirmationService) HandleWaitingCustomerResolution(conversation *models.Conversation, message *models.Message, state *models.ConversationRouteState) (bool, error) {
	if conversation == nil || message == nil || state == nil || message.SenderType != enums.IMSenderTypeCustomer || !state.NeedHumanFollowUp {
		return false, nil
	}
	if message.MessageType != enums.IMMessageTypeText && message.MessageType != enums.IMMessageTypeHTML && message.MessageType != enums.IMMessageTypeVoice {
		return false, nil
	}
	text := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
	if text == "" {
		return false, nil
	}
	payload := handoffConfirmationPayload{Reason: state.HandoffReason}
	classifyCtx, cancel := context.WithTimeout(context.Background(), handoffConfirmationModelTimeout)
	result := classifyHumanHandoffConfirmation(classifyCtx, conversation, message, payload, text)
	cancel()
	if result.Decision != humanHandoffConfirmationCancel || result.Confidence < 0.55 {
		return false, nil
	}
	now := time.Now()
	if err := ManualSessionTimeoutService.restoreConversationShell(conversation.ID, now, "customer_resolved_manual_wait", "客户明确表示问题已解决或无需人工", state.RouteStatus); err != nil {
		return true, err
	}
	if err := ConversationRouteService.RestoreAI(conversation.ID, "客户取消本次人工接待", now); err != nil {
		return true, err
	}
	AIManualResumeTaskService.CancelActive(conversation.ID, "customer resolved or cancelled manual reception")
	_, err := MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "ai_manual_wait_cancel_"+strs.UUID(), enums.IMMessageTypeText, "好，这次人工接待先取消。有新问题直接发我就行。", "", systemOperator(), message.RequestID)
	return true, err
}

func (s *conversationHandoffConfirmationService) resolveRuntimeAIAgent(conversation *models.Conversation, payloadAgentID int64) models.AIAgent {
	if conversation != nil {
		if aiAgent, ok := WxWorkProtocolInstanceService.BuildRuntimeAIAgentForConversation(conversation.ID); ok {
			return aiAgent
		}
	}
	if conversation != nil && payloadAgentID > 0 {
		if aiAgent := AIAgentService.GetByTenantID(payloadAgentID, conversation.TenantID); aiAgent != nil {
			return *aiAgent
		}
	}
	if conversation != nil && conversation.AIAgentID > 0 {
		if aiAgent := AIAgentService.GetByTenantID(conversation.AIAgentID, conversation.TenantID); aiAgent != nil {
			return *aiAgent
		}
	}
	tenantID := int64(0)
	if conversation != nil {
		tenantID = conversation.TenantID
	}
	return models.AIAgent{ID: 0, TenantID: tenantID, Name: "AI", Status: enums.StatusOk, ServiceMode: enums.IMConversationServiceModeAIFirst}
}

func (s *conversationHandoffConfirmationService) alreadyInHumanRoute(conversationID int64) bool {
	state := ConversationRouteService.GetByConversationID(conversationID)
	if state == nil {
		return false
	}
	switch state.RouteStatus {
	case enums.ConversationRouteStatusStoreWecomManual, enums.ConversationRouteStatusHQAgentDeskPending, enums.ConversationRouteStatusHQAgentDeskServing:
		return true
	default:
		return false
	}
}

type humanHandoffConfirmationDecision string

const (
	humanHandoffConfirmationUnknown humanHandoffConfirmationDecision = ""
	humanHandoffConfirmationConfirm humanHandoffConfirmationDecision = "confirm"
	humanHandoffConfirmationCancel  humanHandoffConfirmationDecision = "cancel"
)

func classifyHumanHandoffConfirmationWithModel(ctx context.Context, conversation *models.Conversation, message *models.Message, payload handoffConfirmationPayload, text string) handoffConfirmationClassifyResult {
	text = strings.TrimSpace(text)
	if text == "" {
		return handoffConfirmationClassifyResult{Decision: humanHandoffConfirmationUnknown, Source: "empty"}
	}
	resolved, ok := resolveHandoffConfirmationModelCall(conversation)
	if !ok {
		return classifyHumanHandoffConfirmationWithFallback(text, "fallback:model_unavailable")
	}
	requestID := ""
	if message != nil {
		requestID = strings.TrimSpace(message.RequestID)
	}
	if requestID == "" {
		requestID = fmt.Sprintf("handoff-classify-%d-%d", safeConversationID(conversation), safeMessageID(message))
	}
	callCtx := usagex.WithScope(ctx, modelCallUsageScope(resolved, safeConversationID(conversation), safeMessageID(message), requestID))
	callCtx, capture := usagex.WithCapture(callCtx)
	runtimeConfig := resolved.RuntimeConfig()
	if runtimeConfig.MaxOutputTokens <= 0 || runtimeConfig.MaxOutputTokens > 120 {
		runtimeConfig.MaxOutputTokens = 80
	}
	startedAt := time.Now()
	result, err := ai.LLM.ChatWithRuntimeConfig(callCtx, runtimeConfig, handoffConfirmationClassifySystemPrompt(), buildHandoffConfirmationClassifyUserPrompt(payload.Reason, text))
	if err != nil {
		recordResolvedModelCall(models.AIUsageEvent{
			EventKey: requestID + ":handoff_classify", ConversationID: safeConversationID(conversation),
			MessageID: safeMessageID(message), RequestID: requestID, Stage: "handoff_classify",
			OperationType: "handoff_classify", MetricSource: AIUsageMetricSourceProviderOperation,
			RequestCount: 1, LatencyMS: time.Since(startedAt).Milliseconds(),
			Status: "failed", ErrorClass: "model_call_failed",
		}, resolved, lastUsageReceipt(capture))
		slog.Warn("human handoff confirmation model classify failed",
			"conversation_id", safeConversationID(conversation),
			"message_id", safeMessageID(message),
			"model", resolved.ModelName,
			"error", err,
		)
		return classifyHumanHandoffConfirmationWithFallback(text, "fallback:model_error")
	}
	metricSource := AIUsageMetricSourceProviderOperation
	if result.PromptTokens > 0 || result.CompletionTokens > 0 {
		metricSource = AIUsageMetricSourceUpstreamActual
	}
	recordResolvedModelCall(models.AIUsageEvent{
		EventKey: requestID + ":handoff_classify", ConversationID: safeConversationID(conversation),
		MessageID: safeMessageID(message), RequestID: requestID, Stage: "handoff_classify",
		OperationType: "handoff_classify", MetricSource: metricSource, RequestCount: 1,
		PromptTokens: int64(result.PromptTokens), CompletionTokens: int64(result.CompletionTokens),
		LatencyMS: time.Since(startedAt).Milliseconds(), Status: "completed",
	}, resolved, lastUsageReceipt(capture))
	parsed, err := parseHandoffConfirmationClassifyJSON(result.Content)
	if err != nil {
		slog.Warn("human handoff confirmation model output parse failed",
			"conversation_id", safeConversationID(conversation),
			"message_id", safeMessageID(message),
			"model", resolved.ModelName,
			"content", result.Content,
			"error", err,
		)
		return classifyHumanHandoffConfirmationWithFallback(text, "fallback:parse_error")
	}
	parsed.Source = "model"
	if parsed.Confidence <= 0 || parsed.Confidence > 1 {
		parsed.Confidence = 0.6
	}
	if parsed.Confidence < 0.55 {
		parsed.Decision = humanHandoffConfirmationUnknown
	}
	return parsed
}

func classifyHumanHandoffConfirmationWithFallback(text string, source string) handoffConfirmationClassifyResult {
	decision := parseHumanHandoffConfirmationFallback(text)
	confidence := 0.0
	if decision != humanHandoffConfirmationUnknown {
		confidence = 0.65
	}
	return handoffConfirmationClassifyResult{
		Decision:   decision,
		Confidence: confidence,
		Reason:     "fallback explicit confirmation keywords only",
		Source:     source,
	}
}

func resolveHandoffConfirmationModelCall(conversation *models.Conversation) (*ModelCallConfig, bool) {
	if conversation == nil || conversation.ID <= 0 {
		return nil, false
	}
	resolved, err := ModelCallResolverService.ResolveForConversation(conversation.ID, enums.ModelUsageSlotIntentDetectLLM)
	if err != nil || resolved == nil {
		return nil, false
	}
	return resolved, true
}

func handoffConfirmationClassifySystemPrompt() string {
	return strings.TrimSpace(`你是酒店客服系统的“转人工二次确认”判别器，只输出 JSON，不回复客户。
当前系统刚询问客人是否要通知/转接人工。你的任务只判断客人最新一句话的语义：
- confirm：客人同意、授权、要求现在通知人工或门店同事介入。
- cancel：客人拒绝、取消、表示先不用/别转人工。
- unknown：客人在说新问题、继续描述问题、信息不明确、或不是在回答是否转人工。
不要根据酒店业务内容做回复，不要重新判断主意图。只返回字段：decision, confidence, reason。`)
}

func buildHandoffConfirmationClassifyUserPrompt(reason string, text string) string {
	var b strings.Builder
	if cleaned := cleanHumanHandoffReason(reason); cleaned != "" {
		b.WriteString("此前转人工原因：")
		b.WriteString(cleaned)
		b.WriteString("\n")
	}
	b.WriteString("客人最新消息：")
	b.WriteString(strings.TrimSpace(text))
	b.WriteString("\n请输出严格 JSON。")
	return b.String()
}

func parseHandoffConfirmationClassifyJSON(content string) (handoffConfirmationClassifyResult, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end >= start {
		content = content[start : end+1]
	}
	var raw struct {
		Decision   string  `json:"decision"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return handoffConfirmationClassifyResult{}, err
	}
	ret := handoffConfirmationClassifyResult{
		Decision:   normalizeHandoffConfirmationDecision(raw.Decision),
		Confidence: raw.Confidence,
		Reason:     strings.TrimSpace(raw.Reason),
	}
	return ret, nil
}

func normalizeHandoffConfirmationDecision(value string) humanHandoffConfirmationDecision {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "confirm":
		return humanHandoffConfirmationConfirm
	case "cancel":
		return humanHandoffConfirmationCancel
	default:
		return humanHandoffConfirmationUnknown
	}
}

func parseHumanHandoffConfirmationFallback(value string) humanHandoffConfirmationDecision {
	text := strings.ToLower(strings.TrimSpace(value))
	text = strings.Trim(text, " ，。,.!！?？~～\n\t")
	if text == "" || len([]rune(text)) > 16 {
		return humanHandoffConfirmationUnknown
	}
	for _, keyword := range []string{"不确认", "先不", "不用", "不要", "取消", "算了", "别转", "不转"} {
		if strings.Contains(text, keyword) {
			return humanHandoffConfirmationCancel
		}
	}
	for _, keyword := range []string{"确认", "确定", "可以", "可以的", "好", "好的", "行", "行的", "嗯", "嗯嗯", "对", "对的", "是", "是的", "ok", "okay", "yes", "转", "转人工"} {
		if text == keyword || strings.Contains(text, keyword) {
			return humanHandoffConfirmationConfirm
		}
	}
	return humanHandoffConfirmationUnknown
}

func markHandoffConfirmationMessage(message *models.Message, result handoffConfirmationClassifyResult) error {
	if message == nil || message.ID <= 0 {
		return nil
	}
	payload := map[string]any{}
	raw := strings.TrimSpace(message.Payload)
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return nil
		}
	}
	payload["handoffConfirmationDecision"] = string(result.Decision)
	payload["handoffConfirmationSource"] = strings.TrimSpace(result.Source)
	if result.Confidence > 0 {
		payload["handoffConfirmationConfidence"] = result.Confidence
	}
	if reason := strings.TrimSpace(result.Reason); reason != "" {
		payload["handoffConfirmationReason"] = limitText(reason, 120)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	message.Payload = string(data)
	return repositories.MessageRepository.UpdatesInTenant(sqls.DB(), message.ID, message.TenantID, map[string]any{"payload": message.Payload})
}

func isConsumedHandoffConfirmationMessage(message models.Message) bool {
	raw := strings.TrimSpace(message.Payload)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return false
	}
	var payload struct {
		Decision string `json:"handoffConfirmationDecision"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return false
	}
	return normalizeHandoffConfirmationDecision(payload.Decision) != humanHandoffConfirmationUnknown
}

func isLegacyHandoffConfirmationSummaryText(text string) bool {
	return parseHumanHandoffConfirmationFallback(text) != humanHandoffConfirmationUnknown
}

func safeConversationID(conversation *models.Conversation) int64 {
	if conversation == nil {
		return 0
	}
	return conversation.ID
}

func safeMessageID(message *models.Message) int64 {
	if message == nil {
		return 0
	}
	return message.ID
}

func buildHandoffConfirmationPrompt(reason string) string {
	cleaned := cleanHumanHandoffReason(reason)
	if isSafetyHandoffReason(cleaned) {
		return "这类安全情况建议让门店同事尽快介入。要我现在通知门店同事吗？请回复“确认”或“取消”。"
	}
	return "这个需要门店同事接手处理。要我帮您转人工吗？请回复“确认”或“取消”。"
}

func cleanHumanHandoffReason(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "model IntentDetect JSON:")
	replacer := strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")
	value = replacer.Replace(value)
	for _, token := range []string{
		"human_complaint_risk",
		"service_request",
		"hotel_info",
		"hotel_variable",
		"interaction",
		"social_confirm",
		"unknown_clarify",
		"emergency_safety",
		"NeedsHumanRoute",
		"needsHumanRoute",
		"handoff_to_human",
		"graph/handoff_to_human",
		"toolCode",
		"subIntent",
	} {
		value = strings.ReplaceAll(value, token, "")
	}
	return limitText(strings.TrimSpace(strings.Join(strings.Fields(value), " ")), 180)
}

func isSafetyHandoffReason(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, keyword := range []string{"安全", "突发", "摔倒", "摔跤", "滑倒", "受伤", "流血", "出血", "骨折", "晕倒", "昏倒", "报警", "120", "安全事故"} {
		if strings.Contains(value, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}
