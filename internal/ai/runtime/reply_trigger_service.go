package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	applicationruntime "agent-desk/internal/ai/application/runtime"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/pkg/utils"
	svc "agent-desk/internal/services"
	"github.com/mlogclub/simple/sqls"
)

const aiReplyDebounceWindow = 120 * time.Millisecond
const aiReplyMediaSettleWindow = 900 * time.Millisecond
const aiReplyMediaContextWindow = 6 * time.Second
const aiReplyBurstTextWindow = 8 * time.Second
const standaloneOneReplyMaxAttempts = 3
const deferredKnowledgeHandoffMaxAttempts = 3

func (s *aiReplyService) TriggerStandaloneOneReplyAsync(conversation models.Conversation, message models.Message) {
	go func() {
		ctx, cancel := context.WithTimeout(tracex.ContextWithRequestID(context.Background(), message.RequestID), 15*time.Second)
		defer cancel()
		var lastErr error
		for attempt := 1; attempt <= standaloneOneReplyMaxAttempts; attempt++ {
			if lastErr = s.triggerStandaloneOneReply(ctx, conversation, message); lastErr == nil {
				return
			}
			if attempt == standaloneOneReplyMaxAttempts || !sleepWithContext(ctx, time.Duration(attempt)*500*time.Millisecond) {
				break
			}
		}
		slog.Error("failed to send standalone one reply",
			"requestId", message.RequestID,
			"conversation_id", conversation.ID,
			"message_id", message.ID,
			"attempts", standaloneOneReplyMaxAttempts,
			"error", lastErr,
		)
	}()
}

func (s *aiReplyService) triggerStandaloneOneReply(ctx context.Context, conversation models.Conversation, message models.Message) error {
	if message.SenderType != enums.IMSenderTypeCustomer || !utils.IsStandaloneOneTextControl(message.MessageType, message.Content) {
		return fmt.Errorf("standalone one message scope is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	route := svc.ConversationRouteService.GetByConversationID(conversation.ID)
	if route == nil || route.WxWorkInstanceID <= 0 {
		return fmt.Errorf("standalone one conversation has no employee account")
	}
	instance := svc.WxWorkProtocolInstanceService.Get(route.WxWorkInstanceID)
	if instance == nil || instance.Status != enums.StatusOk {
		return fmt.Errorf("standalone one employee account is unavailable")
	}
	welcomeText := strings.TrimSpace(utils.RepairMojibakeText(instance.WelcomeMessage))
	if welcomeText == "" {
		return fmt.Errorf("standalone one welcome message is not configured")
	}
	miniProgramContent, miniProgramPayload, err := svc.WxWorkProtocolDefaultResourceService.BuildDefaultMiniProgramMessage(instance)
	if err != nil {
		return err
	}
	operatorName := strings.TrimSpace(instance.EmployeeName)
	if operatorName == "" {
		operatorName = "AI"
	}
	operator := &dto.AuthPrincipal{
		UserID:   0,
		Username: operatorName,
		Nickname: operatorName,
	}
	if _, err := svc.MessageService.SendAIMessageWithRequestID(
		conversation.ID,
		conversation.AIAgentID,
		fmt.Sprintf("ai_reply_faq_one_%d_text", message.ID),
		enums.IMMessageTypeText,
		welcomeText,
		"",
		operator,
		message.RequestID,
	); err != nil {
		return err
	}
	_, err = svc.MessageService.SendAIMessageWithRequestID(
		conversation.ID,
		conversation.AIAgentID,
		fmt.Sprintf("ai_reply_faq_one_%d_mini_program", message.ID),
		enums.IMMessageTypeMiniProgram,
		miniProgramContent,
		miniProgramPayload,
		operator,
		message.RequestID,
	)
	return err
}

func (s *aiReplyService) resolveReplyTimeout(aiAgent models.AIAgent) time.Duration {
	if aiAgent.ReplyTimeoutSeconds <= 0 {
		return time.Duration(defaultAIReplyAsyncTimeoutSeconds) * time.Second
	}
	if aiAgent.ReplyTimeoutSeconds > maxAIReplyAsyncTimeoutSeconds {
		return time.Duration(maxAIReplyAsyncTimeoutSeconds) * time.Second
	}
	return time.Duration(aiAgent.ReplyTimeoutSeconds) * time.Second
}

func (s *aiReplyService) TriggerReplyAsync(conversation models.Conversation, message models.Message) {
	go func() {
		if sqls.DB() == nil {
			slog.Warn("skip async ai reply because database is not initialized", "conversation_id", conversation.ID, "message_id", message.ID)
			return
		}
		aiAgent, ok := s.resolveRuntimeAIAgent(conversation)
		if !ok || aiAgent.Status != enums.StatusOk {
			return
		}
		startedAt := time.Now()
		timeout := s.resolveReplyTimeout(aiAgent)
		ctx, cancel := context.WithTimeout(tracex.ContextWithRequestID(context.Background(), message.RequestID), timeout)
		defer cancel()
		err := s.TriggerReply(ctx, conversation, message, aiAgent)
		if err != nil {
			slog.Error("failed to trigger ai reply",
				"requestId", message.RequestID,
				"message_id", message.ID,
				"timeout_ms", timeout.Milliseconds(),
				"elapsed_ms", time.Since(startedAt).Milliseconds(),
				"error", err)
		}
	}()
}

func (s *aiReplyService) TriggerReplySync(ctx context.Context, conversation models.Conversation, message models.Message) error {
	if sqls.DB() == nil {
		return fmt.Errorf("database is not initialized")
	}
	aiAgent, ok := s.resolveRuntimeAIAgent(conversation)
	if !ok || aiAgent.Status != enums.StatusOk {
		return fmt.Errorf("runtime AI agent is unavailable")
	}
	timeout := s.resolveReplyTimeout(aiAgent)
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > timeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return s.TriggerReply(ctx, conversation, message, aiAgent)
}

func (s *aiReplyService) resolveRuntimeAIAgent(conversation models.Conversation) (models.AIAgent, bool) {
	if route := svc.ConversationRouteService.GetByConversationID(conversation.ID); route != nil && route.WxWorkInstanceID > 0 {
		return svc.WxWorkProtocolInstanceService.BuildRuntimeAIAgentForConversation(conversation.ID)
	}
	aiAgent := svc.AIAgentService.Get(conversation.AIAgentID)
	if aiAgent == nil {
		return models.AIAgent{}, false
	}
	return *aiAgent, true
}

func (s *aiReplyService) TriggerReply(ctx context.Context, conversation models.Conversation, message models.Message, aiAgent models.AIAgent) (retErr error) {
	startedAt := time.Now()
	trace := &aiReplyTraceData{Status: "started"}
	var summary *applicationruntime.Summary
	replyCtx := aiReplyContext{
		Conversation: conversation,
		Message:      message,
		AIAgent:      aiAgent,
		Trace:        trace,
		SummaryRef:   &summary,
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	settleStartedAt := time.Now()
	settled, waitReason := s.waitForConversationToSettle(ctx, conversation.ID, message.ID)
	if !settled {
		trace.SettleMs = time.Since(settleStartedAt).Milliseconds()
		trace.Status = waitReason
		return nil
	}
	trace.SettleMs = time.Since(settleStartedAt).Milliseconds()
	if !svc.MessageService.CanSendAIReply(conversation.ID, message.RequestID, message.ID) {
		return nil
	}
	if s.eligibility != nil && !s.eligibility.CanReply(conversation, message, aiAgent) {
		return nil
	}
	defer func() {
		s.runlog.Write(replyRunLogInput{
			StartedAt:    startedAt,
			Message:      message,
			Conversation: conversation,
			AIAgent:      aiAgent,
			Question:     message.Content,
			RunErr:       retErr,
			Trace:        trace,
			Summary:      summary,
		})
	}()
	if pendingInterrupt := svc.ConversationInterruptService.FindLatestPendingByConversationID(conversation.ID); pendingInterrupt != nil {
		replyCtx.PendingInterrupt = pendingInterrupt
		return s.resumePendingInterrupt(ctx, replyCtx)
	}
	replyCtx.Message = s.mergeRecentCustomerBurstMessage(conversation.ID, message)
	return s.executeReply(ctx, replyCtx)
}

func (s *aiReplyService) waitForConversationToSettle(ctx context.Context, conversationID int64, messageID int64) (bool, string) {
	if conversationID <= 0 || messageID <= 0 {
		return true, ""
	}
	if !sleepWithContext(ctx, aiReplyDebounceWindow) {
		return false, "context_cancelled"
	}
	if !s.isStillLatestCustomerMessage(conversationID, messageID) {
		slog.Info("skip ai reply because newer customer message arrived during debounce", "conversation_id", conversationID, "message_id", messageID)
		return false, "newer_customer_message"
	}
	current := svc.MessageService.Get(messageID)
	if current == nil || !shouldWaitForRecentMediaUnderstanding(*current) {
		return true, ""
	}
	deadline := time.Now().Add(aiReplyMediaSettleWindow)
	for time.Now().Before(deadline) {
		if !hasRecentPendingMediaUnderstanding(conversationID, messageID, aiReplyMediaContextWindow) {
			return true, ""
		}
		if !sleepWithContext(ctx, 250*time.Millisecond) {
			return false, "context_cancelled"
		}
		if !s.isStillLatestCustomerMessage(conversationID, messageID) {
			slog.Info("skip ai reply because newer customer message arrived while waiting media", "conversation_id", conversationID, "message_id", messageID)
			return false, "newer_customer_message"
		}
	}
	slog.Info("defer ai reply because recent media understanding is still pending", "conversation_id", conversationID, "message_id", messageID)
	return false, "waiting_media_understanding"
}

func shouldWaitForRecentMediaUnderstanding(message models.Message) bool {
	if message.MessageType != enums.IMMessageTypeText && message.MessageType != enums.IMMessageTypeHTML {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(message.Content))
	if text == "" {
		return false
	}
	compact := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "，", "", "。", "", "！", "", "!", "", "？", "", "?", "").Replace(text)
	if isClearlyIndependentText(compact) {
		return false
	}
	mediaNeedles := []string{"图片", "照片", "图里", "图上", "这图", "截图", "语音", "听下", "文件", "附件", "表格", "pdf", "word", "这个", "这个东西", "这是什么", "这是啥", "这是干嘛", "这是干啥", "看下", "帮我看", "识别"}
	questionNeedles := []string{"什么", "啥", "哪", "怎么", "干嘛", "干啥", "多少", "多少钱", "贵吗", "是不是", "能不能", "能用吗", "能买吗", "可以吗", "对吗", "什么意思", "帮", "看", "听", "识别"}
	if containsAnyText(compact, mediaNeedles) && containsAnyText(compact, questionNeedles) {
		return true
	}
	return isLikelyImplicitMediaFollowUp(compact)
}

func isLikelyImplicitMediaFollowUp(compact string) bool {
	if compact == "" || len([]rune(compact)) > 24 {
		return false
	}
	if containsAnyText(compact, []string{"这个", "这个东西", "这是什么", "这是啥", "这是干嘛", "这是干啥", "这能", "能用吗", "能买吗", "多少钱", "多少", "贵吗", "对吗", "行吗", "可以吗", "咋弄", "怎么弄", "什么意思"}) {
		return true
	}
	return false
}

func isClearlyIndependentText(compact string) bool {
	if compact == "" {
		return false
	}
	if containsAnyText(compact, []string{"早餐", "停车", "发票", "押金", "退房", "入住时间", "会员", "wifi", "无线网", "洗衣", "健身房", "餐厅"}) {
		return true
	}
	if containsAnyText(compact, []string{"发定位", "酒店定位", "门店定位", "导航", "怎么去", "酒店地址", "我要办入住", "办理入住", "办入住", "小程序", "安心宿"}) {
		return true
	}
	if containsAnyText(compact, []string{"送水", "拖鞋", "牙刷", "纸巾", "维修", "打扫", "保洁", "投诉", "转人工", "人工客服"}) {
		return true
	}
	shortExact := []string{"你好", "您好", "在吗", "谢谢", "好的", "好", "嗯", "嗯嗯", "确认", "确认确认", "收到", "可以", "行"}
	for _, value := range shortExact {
		if compact == value {
			return true
		}
	}
	return false
}

func containsAnyText(text string, values []string) bool {
	for _, value := range values {
		if value != "" && strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func sleepWithContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func hasRecentPendingMediaUnderstanding(conversationID int64, currentMessageID int64, window time.Duration) bool {
	if conversationID <= 0 || currentMessageID <= 0 {
		return false
	}
	current := svc.MessageService.Get(currentMessageID)
	if current == nil || current.SentAt == nil {
		return false
	}
	items := svc.MessageService.Find(sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("session_no", current.SessionNo).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		In("message_type", []string{string(enums.IMMessageTypeImage), string(enums.IMMessageTypeVoice), string(enums.IMMessageTypeAttachment)}).
		Lt("id", currentMessageID).
		Gte("sent_at", current.SentAt.Add(-window)).
		Desc("id").
		Limit(10))
	for i := range items {
		if mediaUnderstandingPending(items[i].Payload) {
			return true
		}
	}
	return false
}

func mediaUnderstandingPending(payload string) bool {
	payload = strings.TrimSpace(payload)
	if payload == "" || !strings.HasPrefix(payload, "{") {
		return false
	}
	var parsed struct {
		MediaStatus string `json:"mediaUnderstandingStatus"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return false
	}
	switch strings.TrimSpace(parsed.MediaStatus) {
	case "understood", "failed", "empty":
		return false
	default:
		return true
	}
}

func (s *aiReplyService) mergeRecentCustomerBurstMessage(conversationID int64, message models.Message) models.Message {
	if conversationID <= 0 || message.ID <= 0 || message.SenderType != enums.IMSenderTypeCustomer || message.SentAt == nil {
		return message
	}
	cnd := sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("session_no", message.SessionNo).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		In("message_type", []string{string(enums.IMMessageTypeText), string(enums.IMMessageTypeVoice), string(enums.IMMessageTypeImage), string(enums.IMMessageTypeLocation), string(enums.IMMessageTypeMiniProgram), string(enums.IMMessageTypeAttachment)}).
		Lte("id", message.ID).
		Desc("id").
		Limit(12)
	if latestOutbound := s.latestOutboundMessageBefore(conversationID, message.SessionNo, message.ID); latestOutbound != nil {
		cnd.Gt("id", latestOutbound.ID)
	}
	items := contiguousRecentCustomerBurstMessages(svc.MessageService.Find(cnd), message)
	if len(items) <= 1 {
		return message
	}
	parts := make([]string, 0, len(items))
	for idx, item := range items {
		if utils.IsStandaloneOneTextControl(item.MessageType, item.Content) {
			continue
		}
		text := runtimeBurstMessageText(item)
		if text == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(timePrefixForBurst(item, idx+1)+text))
	}
	if len(parts) <= 1 {
		return message
	}
	merged := message
	merged.Content = utils.BuildRuntimeCustomerBurstEnvelope(parts)
	return merged
}

func contiguousRecentCustomerBurstMessages(items []models.Message, current models.Message) []models.Message {
	if len(items) == 0 || current.SentAt == nil {
		return items
	}
	selected := make([]models.Message, 0, len(items))
	newerAt := *current.SentAt
	for _, item := range items {
		if item.ID > current.ID || item.SentAt == nil {
			continue
		}
		gap := newerAt.Sub(*item.SentAt)
		if gap > aiReplyBurstTextWindow {
			break
		}
		selected = append(selected, item)
		newerAt = *item.SentAt
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return selected
}

func runtimeBurstMessageText(message models.Message) string {
	if message.MessageType == enums.IMMessageTypeVoice {
		mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
		if strings.TrimSpace(status) != "understood" {
			return ""
		}
		if text := strings.TrimSpace(mediaText); text != "" {
			return text
		}
		if text := strings.TrimSpace(mediaSummary); text != "" {
			return text
		}
		return ""
	}
	return strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
}

func (s *aiReplyService) latestOutboundMessageBefore(conversationID int64, sessionNo int, messageID int64) *models.Message {
	if conversationID <= 0 || messageID <= 0 {
		return nil
	}
	return svc.MessageService.FindOne(sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("session_no", sessionNo).
		In("sender_type", []string{string(enums.IMSenderTypeAI), string(enums.IMSenderTypeAgent)}).
		Lt("id", messageID).
		Desc("id"))
}

func timePrefixForBurst(item models.Message, index int) string {
	label := "消息"
	switch item.MessageType {
	case enums.IMMessageTypeImage:
		label = "图片"
	case enums.IMMessageTypeVoice:
		label = "语音"
	case enums.IMMessageTypeAttachment:
		label = "文件"
	case enums.IMMessageTypeLocation:
		label = "定位"
	case enums.IMMessageTypeMiniProgram:
		label = "小程序"
	case enums.IMMessageTypeGIF:
		label = "表情"
	}
	if item.ID > 0 {
		return fmt.Sprintf("%d. [%s%d] ", index, label, item.ID)
	}
	return fmt.Sprintf("%d. [%s] ", index, label)
}

func (s *aiReplyService) resumePendingInterrupt(ctx context.Context, replyCtx aiReplyContext) error {
	return s.interrupts.ResumePendingInterrupt(ctx, s, replyCtx)
}

func (s *aiReplyService) executeReply(ctx context.Context, replyCtx aiReplyContext) error {
	summary, err := s.executor.Run(ctx, runtimeReplyRunInput{
		Conversation: replyCtx.Conversation,
		Message:      replyCtx.Message,
		AIAgent:      replyCtx.AIAgent,
		Trace:        replyCtx.Trace,
	})
	replyCtx.setSummary(summary)
	if err != nil {
		return err
	}
	if summary != nil && summary.Interrupted {
		return s.interrupts.HandleInterruptedSummary(s, replyCtx, summary)
	}
	hasCommitPayload, hasDeferredKnowledge := resolveReplyExecutionActions(summary, s.commit.HasStructuredVariableReply(replyCtx.Trace))
	if summary != nil && (hasCommitPayload || hasDeferredKnowledge) && !s.canCommitReplyForMessage(replyCtx.Conversation.ID, replyCtx.Message.ID, replyCtx.Message.RequestID) {
		slog.Info("skip stale ai reply because newer customer message arrived",
			"conversation_id", replyCtx.Conversation.ID,
			"message_id", replyCtx.Message.ID,
			"requestId", replyCtx.Message.RequestID,
		)
		return nil
	}
	if hasCommitPayload {
		replyMessage, err := s.commit.CommitAIReply(replyCommitInput{
			Conversation: replyCtx.Conversation,
			Message:      replyCtx.Message,
			AIAgent:      replyCtx.AIAgent,
			ReplyText:    summary.ReplyText,
			Trace:        replyCtx.Trace,
			ClientPrefix: "ai_reply",
		})
		if err != nil {
			return err
		}
		if replyMessage != nil && strings.TrimSpace(summary.ReplyText) == "" {
			summary.ReplyText = committedReplyText(*replyMessage)
		}
		replyCtx.Trace.ReplySent = replyMessage != nil
	}
	if hasDeferredKnowledge {
		return s.dispatchDeferredKnowledgeHandoff(ctx, replyCtx, summary)
	}
	return nil
}

func resolveReplyExecutionActions(summary *applicationruntime.Summary, hasStructuredVariableReply bool) (hasCommitPayload bool, hasDeferredKnowledge bool) {
	if summary == nil {
		return false, false
	}
	hasCommitPayload = strings.TrimSpace(summary.ReplyText) != "" || hasStructuredVariableReply
	_, hasDeferredKnowledge = deferredKnowledgeHandoffFromTrace(summary.TraceData)
	return hasCommitPayload, hasDeferredKnowledge
}

func (s *aiReplyService) dispatchDeferredKnowledgeHandoff(ctx context.Context, replyCtx aiReplyContext, summary *applicationruntime.Summary) error {
	if summary == nil || !svc.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabledForConversation(replyCtx.Conversation.ID) {
		return nil
	}
	reason, ok := deferredKnowledgeHandoffFromTrace(summary.TraceData)
	if !ok {
		return nil
	}
	if err := svc.ChannelMessageOutboxService.MarkReplyBeforeDeferredHandoff(
		replyCtx.Conversation.ID,
		strings.TrimSpace(replyCtx.Message.RequestID),
	); err != nil {
		return err
	}
	var lastErr error
	for attempt := 1; attempt <= deferredKnowledgeHandoffMaxAttempts; attempt++ {
		_, lastErr = svc.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
			replyCtx.Conversation.ID,
			replyCtx.AIAgent,
			reason,
			strings.TrimSpace(replyCtx.Message.RequestID),
			replyCtx.Message.ID,
		)
		if lastErr == nil {
			return nil
		}
		if attempt == deferredKnowledgeHandoffMaxAttempts || !sleepWithContext(ctx, time.Duration(attempt)*150*time.Millisecond) {
			break
		}
	}
	return lastErr
}

func deferredKnowledgeHandoffFromTrace(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return "", false
	}
	var trace struct {
		Pipeline struct {
			EvidenceJudge struct {
				DeferredHandoff       bool   `json:"deferredHandoff"`
				DeferredHandoffReason string `json:"deferredHandoffReason"`
			} `json:"evidenceJudge"`
		} `json:"pipeline"`
	}
	if err := json.Unmarshal([]byte(raw), &trace); err != nil || !trace.Pipeline.EvidenceJudge.DeferredHandoff {
		return "", false
	}
	reason := strings.TrimSpace(trace.Pipeline.EvidenceJudge.DeferredHandoffReason)
	if reason == "" {
		reason = "部分酒店业务问题需要门店同事接手"
	}
	return reason, true
}

func committedReplyText(message models.Message) string {
	content := strings.TrimSpace(message.Content)
	switch message.MessageType {
	case enums.IMMessageTypeLocation:
		if content == "" {
			content = "位置"
		}
		return "[位置] " + content
	case enums.IMMessageTypeMiniProgram:
		if content == "" {
			content = "小程序"
		}
		return "[小程序] " + content
	default:
		return content
	}
}

func (s *aiReplyService) canCommitReplyForMessage(conversationID int64, messageID int64, requestIDs ...string) bool {
	requestID := ""
	if len(requestIDs) > 0 {
		requestID = strings.TrimSpace(requestIDs[0])
		if !svc.MessageService.CanSendAIReply(conversationID, requestID, messageID) {
			return false
		}
	}
	latest := s.latestNonStandaloneConversationMessage(conversationID)
	if latest == nil {
		return true
	}
	if latest.SenderType != enums.IMSenderTypeCustomer {
		return latest.ID <= messageID
	}
	if latest.ID == messageID {
		return true
	}
	current := svc.MessageService.Get(messageID)
	if current != nil && isMediaFollowUpTextMessage(*current) && isRuntimeReplyMediaMessage(latest.MessageType) {
		return false
	}
	return isNonActionableMediaMessage(*latest)
}

func isMediaFollowUpTextMessage(message models.Message) bool {
	if message.MessageType != enums.IMMessageTypeText && message.MessageType != enums.IMMessageTypeHTML {
		return false
	}
	if isClearlyIndependentText(strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "，", "", "。", "", "！", "", "!", "", "？", "", "?", "").Replace(strings.ToLower(strings.TrimSpace(message.Content)))) {
		return false
	}
	return shouldWaitForRecentMediaUnderstanding(message)
}

func isNonActionableMediaMessage(message models.Message) bool {
	if !isRuntimeReplyMediaMessage(message.MessageType) {
		return false
	}
	if message.MessageType == enums.IMMessageTypeVoice {
		mediaText, mediaSummary, mediaStatus := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
		if strings.TrimSpace(mediaText) != "" || strings.TrimSpace(mediaSummary) != "" {
			return false
		}
		return strings.TrimSpace(mediaStatus) != "understood"
	}
	text := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
	if text == "" {
		return true
	}
	compact := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "").Replace(strings.ToLower(text))
	return !containsAnyText(compact, []string{"?", "？", "怎么", "什么", "啥", "报错", "打不开", "失败", "异常", "不能", "不行", "处理", "求助"})
}

func isRuntimeReplyMediaMessage(messageType enums.IMMessageType) bool {
	switch messageType {
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeAttachment, enums.IMMessageTypeVideo, enums.IMMessageTypeGIF:
		return true
	default:
		return false
	}
}

func (s *aiReplyService) isStillLatestCustomerMessage(conversationID int64, messageID int64) bool {
	latest := s.latestNonStandaloneConversationMessage(conversationID)
	if latest == nil {
		return true
	}
	if latest.SenderType == enums.IMSenderTypeCustomer {
		return latest.ID == messageID
	}
	return latest.ID <= messageID
}

func (s *aiReplyService) latestNonStandaloneConversationMessage(conversationID int64) *models.Message {
	if conversationID <= 0 {
		return nil
	}
	const pageSize = 32
	beforeID := int64(0)
	for {
		cnd := sqls.NewCnd().
			Eq("conversation_id", conversationID).
			Desc("seq_no").
			Desc("id").
			Limit(pageSize)
		if beforeID > 0 {
			cnd.Lt("id", beforeID)
		}
		items := svc.MessageService.Find(cnd)
		if len(items) == 0 {
			return nil
		}
		for index := range items {
			if isStandaloneOneRuntimeMessage(&items[index]) {
				continue
			}
			return &items[index]
		}
		if len(items) < pageSize {
			return nil
		}
		beforeID = items[len(items)-1].ID
	}
}

func isStandaloneOneRuntimeMessage(message *models.Message) bool {
	if message == nil {
		return false
	}
	if utils.IsAIServiceNoticeMessage(message) {
		return true
	}
	if message.SenderType == enums.IMSenderTypeCustomer &&
		utils.IsStandaloneOneTextControl(message.MessageType, message.Content) {
		return true
	}
	return (message.SenderType == enums.IMSenderTypeAI || message.SenderType == enums.IMSenderTypeAgent) &&
		strings.HasPrefix(strings.TrimSpace(message.ClientMsgID), "ai_reply_faq_one_")
}
