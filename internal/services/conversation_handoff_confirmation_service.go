package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/pkg/utils"

	"github.com/mlogclub/simple/common/strs"
)

var ConversationHandoffConfirmationService = newConversationHandoffConfirmationService()

const handoffConfirmationModelTimeout = 2 * time.Second

type conversationHandoffConfirmationService struct{}

type handoffConfirmationPayload struct {
	Reason          string `json:"reason"`
	AIAgentID       int64  `json:"aiAgentId"`
	OriginMessageID int64  `json:"originMessageId"`
	HandoffToken    string `json:"handoffToken"`
	AwaitingField   string `json:"awaitingField,omitempty"`
	RoomNumber      string `json:"roomNumber,omitempty"`
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
	if message := AIManualResumeTaskService.latestCustomerMessage(conversationID); message != nil {
		originMessageID = message.ID
	}
	return s.RequestByAIWithOriginMessage(conversationID, aiAgent, reason, requestID, originMessageID)
}

func (s *conversationHandoffConfirmationService) RequestByAIWithOriginMessage(conversationID int64, aiAgent models.AIAgent, reason string, requestID string, originMessageID int64) (bool, error) {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return false, fmt.Errorf("会话不存在")
	}
	if s.alreadyInHumanRoute(conversationID) {
		return true, nil
	}
	if state := ConversationRouteService.GetByConversationID(conversationID); state != nil && state.PendingAction == string(enums.ConversationPendingActionHumanHandoff) {
		if state.PendingActionExpireAt == nil || time.Now().Before(*state.PendingActionExpireAt) {
			return true, nil
		}
		_ = ConversationRouteService.ClearPendingAction(conversationID)
	}
	cleanedReason := cleanHumanHandoffReason(reason)
	payloadValue := handoffConfirmationPayload{
		Reason:          cleanedReason,
		AIAgentID:       aiAgent.ID,
		OriginMessageID: originMessageID,
		HandoffToken:    AIManualResumeTaskService.NewHandoffToken(),
		CreatedAt:       time.Now().Format(time.RFC3339),
	}
	if handoffNeedsRoomNumber(cleanedReason) && extractRoomNo(cleanedReason) == "" {
		payloadValue.AwaitingField = "room_number"
	}
	payload, _ := json.Marshal(payloadValue)
	if err := ConversationRouteService.SetPendingAction(conversationID, enums.ConversationPendingActionHumanHandoff, string(payload), time.Now().Add(DefaultHandoffConfirmationMinutes*time.Minute)); err != nil {
		return false, err
	}
	content := buildHandoffConfirmationPrompt(cleanedReason)
	if payloadValue.AwaitingField == "room_number" {
		content = "方便说下是哪个房间吗？"
	}
	_, err := MessageService.SendAIMessageWithRequestID(conversationID, aiAgent.ID, "ai_handoff_confirm_"+strs.UUID(), enums.IMMessageTypeText, content, "", systemOperator(), requestID)
	if err != nil {
		if clearErr := ConversationRouteService.ClearPendingAction(conversationID); clearErr != nil {
			return false, fmt.Errorf("发送转人工确认失败: %w；清理待确认状态失败: %v", err, clearErr)
		}
		return false, err
	}
	return true, nil
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
	if !WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabled(conversation.CustomerID, state.WxWorkInstanceID) {
		_ = ConversationRouteService.ClearPendingAction(conversation.ID)
		return false, nil
	}
	text := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
	payload := handoffConfirmationPayload{}
	_ = json.Unmarshal([]byte(state.PendingActionPayload), &payload)
	if payload.AwaitingField == "room_number" {
		return s.handleRoomNumberReply(conversation, message, payload, text)
	}
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
		_, err = MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "ai_handoff_cancel_"+strs.UUID(), enums.IMMessageTypeText, "好的，那先不联系同事。", "", systemOperator(), message.RequestID)
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

func (s *conversationHandoffConfirmationService) handleRoomNumberReply(conversation *models.Conversation, message *models.Message, payload handoffConfirmationPayload, text string) (bool, error) {
	if isExplicitHandoffContextCancel(text) {
		_, _, err := ConversationRouteService.ConsumePendingAction(conversation.ID, enums.ConversationPendingActionHumanHandoff, time.Now())
		if err != nil {
			return true, err
		}
		_, err = MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "ai_handoff_context_cancel_"+strs.UUID(), enums.IMMessageTypeText, "好的，那先不联系同事。", "", systemOperator(), message.RequestID)
		return true, err
	}
	roomNumber := extractRoomNo(text)
	if roomNumber == "" {
		_ = ConversationRouteService.ClearPendingAction(conversation.ID)
		return false, nil
	}
	payload.AwaitingField = ""
	payload.RoomNumber = roomNumber
	payload.Reason = appendHandoffRoomNumber(payload.Reason, roomNumber)
	data, _ := json.Marshal(payload)
	if err := ConversationRouteService.SetPendingAction(conversation.ID, enums.ConversationPendingActionHumanHandoff, string(data), time.Now().Add(DefaultHandoffConfirmationMinutes*time.Minute)); err != nil {
		return true, err
	}
	_, err := MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "ai_handoff_confirm_after_room_"+strs.UUID(), enums.IMMessageTypeText, buildHandoffConfirmationPrompt(payload.Reason), "", systemOperator(), message.RequestID)
	return true, err
}

func isExplicitHandoffContextCancel(text string) bool {
	text = strings.ToLower(strings.Trim(strings.TrimSpace(text), " ，。,.!！?？~～\n\t"))
	switch text {
	case "取消", "算了", "不用了", "先不用", "先不用了", "不用联系", "不要联系", "不需要联系":
		return true
	default:
		return false
	}
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
	if payloadAgentID > 0 {
		if aiAgent := AIAgentService.Get(payloadAgentID); aiAgent != nil {
			return *aiAgent
		}
	}
	if conversation != nil && conversation.AIAgentID > 0 {
		if aiAgent := AIAgentService.Get(conversation.AIAgentID); aiAgent != nil {
			return *aiAgent
		}
	}
	return models.AIAgent{ID: 0, Name: "AI", Status: enums.StatusOk, ServiceMode: enums.IMConversationServiceModeAIFirst}
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
	config, credentialRevision, modelSource, ok := resolveHandoffConfirmationAIConfig(conversation, payload)
	if !ok {
		return classifyHumanHandoffConfirmationWithFallback(text, "fallback:no_ai_config")
	}
	requestID := strings.TrimSpace(message.RequestID)
	if requestID == "" {
		requestID = fmt.Sprintf("handoff-classify-%d-%d", safeConversationID(conversation), safeMessageID(message))
	}
	callCtx := usagex.WithScope(ctx, usagex.Scope{
		ConversationID: safeConversationID(conversation), MessageID: safeMessageID(message),
		RequestID: requestID, CredentialRevision: credentialRevision, ModelSource: modelSource,
	})
	callCtx, capture := usagex.WithCapture(callCtx)
	startedAt := time.Now()
	result, err := ai.LLM.ChatWithConfig(callCtx, config, handoffConfirmationClassifySystemPrompt(), buildHandoffConfirmationClassifyUserPrompt(payload.Reason, text))
	if err != nil {
		ai.RecordModelUsage(callCtx, ai.ModelUsageRecord{
			Stage: "handoff_classify", OperationType: "handoff_classify",
			Config: config, LatencyMS: time.Since(startedAt).Milliseconds(),
			Status: "failed", ErrorClass: "model_call_failed",
			Receipt:          lastUsageReceipt(capture),
			ExternalEventKey: requestID + ":handoff_classify",
		})
		slog.Warn("human handoff confirmation model classify failed",
			"conversation_id", safeConversationID(conversation),
			"message_id", safeMessageID(message),
			"model", config.ModelName,
			"error", err,
		)
		return classifyHumanHandoffConfirmationWithFallback(text, "fallback:model_error")
	}
	ai.RecordModelUsage(callCtx, ai.ModelUsageRecord{
		Stage: "handoff_classify", OperationType: "handoff_classify",
		Config: config, PromptTokens: int64(result.PromptTokens),
		CompletionTokens: int64(result.CompletionTokens),
		LatencyMS:        time.Since(startedAt).Milliseconds(), Status: "completed",
		Receipt:          lastUsageReceipt(capture),
		ExternalEventKey: requestID + ":handoff_classify",
	})
	parsed, err := parseHandoffConfirmationClassifyJSON(result.Content)
	if err != nil {
		slog.Warn("human handoff confirmation model output parse failed",
			"conversation_id", safeConversationID(conversation),
			"message_id", safeMessageID(message),
			"model", config.ModelName,
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

func resolveHandoffConfirmationAIConfig(conversation *models.Conversation, payload handoffConfirmationPayload) (models.AIConfig, int64, string, bool) {
	_ = payload
	var fallback models.AIConfig
	if conversation == nil || conversation.ID <= 0 {
		return models.AIConfig{}, 0, "", false
	}
	resolved, err := StoreAIModelSettingService.ResolveForConversation(conversation.ID, StoreAIModelUsageIntentDetectLLM, 0)
	if err != nil || resolved == nil || !isUsableHandoffConfirmationAIConfig(&resolved.Config) {
		return models.AIConfig{}, 0, "", false
	}
	fallback = resolved.Config
	if fallback.MaxOutputTokens <= 0 || fallback.MaxOutputTokens > 120 {
		fallback.MaxOutputTokens = 80
	}
	return fallback, resolved.CredentialRevision, resolved.Source, true
}

func isUsableHandoffConfirmationAIConfig(config *models.AIConfig) bool {
	return config != nil &&
		config.Status == enums.StatusOk &&
		config.ModelType == enums.AIModelTypeLLM &&
		strings.TrimSpace(config.ModelName) != "" &&
		strings.TrimSpace(string(config.Provider)) != ""
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
	return MessageService.Updates(message.ID, map[string]any{"payload": message.Payload})
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
	return "这个需要我帮您联系同事来解决吗？回复“确认”或“取消”。"
}

func handoffNeedsRoomNumber(reason string) bool {
	text := strings.ToLower(strings.TrimSpace(reason))
	if text == "" {
		return false
	}
	return containsAny(text, []string{
		"房间", "房内", "屋里", "客房",
		"床单", "被套", "被子", "枕头", "浴巾", "毛巾",
		"遥控器", "电视", "投屏", "空调", "窗外", "噪音", "好吵",
		"门锁", "开门", "敲门", "马桶", "卫生间", "洗手间", "漏水", "停电", "异味",
		"打扫", "保洁", "维修", "送到", "送来", "送水", "同住人",
		"落东西", "落了东西", "遗落", "遗失", "忘在", "跑腿",
	})
}

func appendHandoffRoomNumber(reason string, roomNumber string) string {
	reason = cleanHumanHandoffReason(reason)
	roomNumber = strings.TrimSpace(roomNumber)
	if roomNumber == "" || strings.Contains(reason, "客户补充房号："+roomNumber) {
		return reason
	}
	if reason == "" {
		return "客户补充房号：" + roomNumber
	}
	return reason + "；客户补充房号：" + roomNumber
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
