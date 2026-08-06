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
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	svc "agent-desk/internal/services"
	"github.com/mlogclub/simple/sqls"
)

const aiReplyDebounceWindow = 120 * time.Millisecond
const aiReplyMediaSettleWindow = 900 * time.Millisecond
const aiReplyMediaContextWindow = 6 * time.Second
const aiReplyBurstTextWindow = 8 * time.Second

func (s *aiReplyService) resolveReplyTimeout(aiAgent models.AIAgent) time.Duration {
	if aiAgent.ReplyTimeoutSeconds <= 0 {
		return time.Duration(defaultAIReplyAsyncTimeoutSeconds) * time.Second
	}
	if aiAgent.ReplyTimeoutSeconds > maxAIReplyAsyncTimeoutSeconds {
		return time.Duration(maxAIReplyAsyncTimeoutSeconds) * time.Second
	}
	return time.Duration(aiAgent.ReplyTimeoutSeconds) * time.Second
}

func (s *aiReplyService) TriggerReplySync(ctx context.Context, conversation models.Conversation, message models.Message) (svc.AIReplyExecutionResult, error) {
	if sqls.DB() == nil {
		return svc.AIReplyExecutionResult{}, fmt.Errorf("database is not initialized")
	}
	aiAgent, ok := s.resolveRuntimeAIAgent(conversation)
	if !ok || aiAgent.Status != enums.StatusOk {
		return svc.AIReplyExecutionResult{}, fmt.Errorf("runtime AI agent is unavailable")
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
	aiAgent := svc.AIAgentService.GetByTenantID(conversation.AIAgentID, conversation.TenantID)
	if aiAgent == nil {
		return models.AIAgent{}, false
	}
	return *aiAgent, true
}

func (s *aiReplyService) TriggerReply(ctx context.Context, conversation models.Conversation, message models.Message, aiAgent models.AIAgent) (result svc.AIReplyExecutionResult, retErr error) {
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
		return result, err
	}
	settleStartedAt := time.Now()
	settled, waitReason := s.waitForConversationToSettle(ctx, conversation.ID, message.ID)
	if !settled {
		trace.SettleMs = time.Since(settleStartedAt).Milliseconds()
		trace.Status = waitReason
		switch waitReason {
		case "newer_customer_message":
			return svc.AIReplyExecutionResult{Status: svc.AIReplyExecutionStatusSuperseded, ReasonCode: waitReason}, nil
		case "waiting_media_understanding":
			retryAt := time.Now().Add(time.Second)
			return svc.AIReplyExecutionResult{Status: svc.AIReplyExecutionStatusDeferred, ReasonCode: waitReason, RetryAt: &retryAt}, nil
		default:
			if err := ctx.Err(); err != nil {
				return result, err
			}
			return svc.AIReplyExecutionResult{Status: svc.AIReplyExecutionStatusDeferred, ReasonCode: "runtime_not_settled"}, nil
		}
	}
	trace.SettleMs = time.Since(settleStartedAt).Milliseconds()
	if s.eligibility != nil && !s.eligibility.CanReply(conversation, message, aiAgent) {
		return svc.AIReplyExecutionResult{Status: svc.AIReplyExecutionStatusSkipped, ReasonCode: "runtime_not_eligible"}, nil
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
	checkpoint, checkpointErr := svc.AIReplyJobService.ValidateRuntimeCheckpoint(ctx, conversation, message)
	if checkpointErr != nil {
		return result, checkpointErr
	}
	if checkpoint.Status != svc.AIReplyExecutionStatusCompleted || checkpoint.ReasonCode != "checkpoint_valid" {
		return checkpoint, nil
	}
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
		Gte("sent_at", message.SentAt.Add(-aiReplyBurstTextWindow)).
		Asc("id").
		Limit(12)
	if latestOutbound := s.latestOutboundMessageBefore(conversationID, message.SessionNo, message.ID); latestOutbound != nil {
		cnd.Gt("id", latestOutbound.ID)
	}
	items := svc.MessageService.Find(cnd)
	if len(items) <= 1 {
		return message
	}
	parts := make([]string, 0, len(items))
	for idx, item := range items {
		text := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(item.MessageType, item.Content, item.Payload))
		if text == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(timePrefixForBurst(item, idx+1)+text))
	}
	if len(parts) <= 1 {
		return message
	}
	merged := message
	merged.Content = "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题；如果前面是图片、语音、文件，后面的短句通常是在追问它：\n" + strings.Join(parts, "\n")
	return merged
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
	return fmt.Sprintf("%d. [%s] ", index, label)
}

func (s *aiReplyService) resumePendingInterrupt(ctx context.Context, replyCtx aiReplyContext) (svc.AIReplyExecutionResult, error) {
	return s.interrupts.ResumePendingInterrupt(ctx, s, replyCtx)
}

func (s *aiReplyService) executeReply(ctx context.Context, replyCtx aiReplyContext) (svc.AIReplyExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return svc.AIReplyExecutionResult{}, err
	}
	summary, err := s.executor.Run(ctx, runtimeReplyRunInput{
		Conversation: replyCtx.Conversation,
		Message:      replyCtx.Message,
		AIAgent:      replyCtx.AIAgent,
		Trace:        replyCtx.Trace,
	})
	replyCtx.setSummary(summary)
	if err != nil {
		return svc.AIReplyExecutionResult{}, err
	}
	if summary != nil && summary.PolicySkipped {
		return svc.AIReplyExecutionResult{Status: svc.AIReplyExecutionStatusSkipped, ReasonCode: "policy_skipped"}, nil
	}
	if summary != nil && summary.Interrupted {
		return s.interrupts.HandleInterruptedSummary(s, replyCtx, summary)
	}
	if summary != nil && (strings.TrimSpace(summary.ReplyText) != "" || s.commit.HasStructuredVariableReply(replyCtx.Trace)) {
		if err := ctx.Err(); err != nil {
			return svc.AIReplyExecutionResult{}, err
		}
		checkpoint, checkpointErr := svc.AIReplyJobService.ValidateRuntimeCheckpoint(ctx, replyCtx.Conversation, replyCtx.Message)
		if checkpointErr != nil {
			return svc.AIReplyExecutionResult{}, checkpointErr
		}
		if checkpoint.Status != svc.AIReplyExecutionStatusCompleted || checkpoint.ReasonCode != "checkpoint_valid" {
			return checkpoint, nil
		}
		if !s.canCommitReplyForMessage(replyCtx.Conversation.ID, replyCtx.Message.ID) {
			slog.Info("skip stale ai reply because newer customer message arrived",
				"conversation_id", replyCtx.Conversation.ID,
				"message_id", replyCtx.Message.ID,
				"requestId", replyCtx.Message.RequestID,
			)
			return svc.AIReplyExecutionResult{Status: svc.AIReplyExecutionStatusSuperseded, ReasonCode: "newer_customer_message"}, nil
		}
		replyMessages, err := s.commit.CommitAIReplyBatch(replyCommitInput{
			Conversation: replyCtx.Conversation,
			Message:      replyCtx.Message,
			AIAgent:      replyCtx.AIAgent,
			ReplyText:    summary.ReplyText,
			Trace:        replyCtx.Trace,
			ClientPrefix: "ai_reply",
		})
		if err != nil {
			return svc.AIReplyExecutionResult{}, err
		}
		if len(replyMessages) > 0 && strings.TrimSpace(summary.ReplyText) == "" {
			summary.ReplyText = committedReplyText(replyMessages[len(replyMessages)-1])
		}
		replyCtx.Trace.ReplySent = len(replyMessages) > 0
		return completedInterruptResult("runtime_completed", replyMessages, 0), nil
	}
	if evidence := s.findCommittedReplyEvidence(replyCtx); len(evidence) > 0 {
		return svc.AIReplyExecutionResult{
			Status: svc.AIReplyExecutionStatusCompleted, ReasonCode: "runtime_action_committed",
			CommittedMessageIDs: evidence,
		}, nil
	}
	return svc.AIReplyExecutionResult{}, svc.NewAIReplyExecutionError(
		svc.AIReplyExecutionErrorEmptyOutput,
		fmt.Errorf("runtime completed without durable output"),
	)
}

func (s *aiReplyService) findCommittedReplyEvidence(replyCtx aiReplyContext) []int64 {
	items := svc.MessageService.Find(sqls.NewCnd().
		Eq("tenant_id", replyCtx.Conversation.TenantID).
		Eq("conversation_id", replyCtx.Conversation.ID).
		Eq("sender_type", enums.IMSenderTypeAI).
		Eq("request_id", strings.TrimSpace(replyCtx.Message.RequestID)).
		Gt("id", replyCtx.Message.ID).
		Asc("id"))
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		if item.SessionNo == replyCtx.Message.SessionNo {
			ids = append(ids, item.ID)
		}
	}
	return ids
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

func (s *aiReplyService) canCommitReplyForMessage(conversationID int64, messageID int64) bool {
	current := svc.MessageService.Get(messageID)
	if current == nil {
		return true
	}
	latest := svc.MessageService.FindOne(sqls.NewCnd().
		Eq("tenant_id", current.TenantID).
		Eq("conversation_id", conversationID).
		Eq("session_no", current.SessionNo).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		Where("recalled_at IS NULL AND send_status NOT IN (?, ?)", enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Desc("id"))
	if latest == nil || latest.ID == messageID {
		return true
	}
	if isMediaFollowUpTextMessage(*current) && isRuntimeReplyMediaMessage(latest.MessageType) {
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
	current := svc.MessageService.Get(messageID)
	if current == nil {
		return true
	}
	latest := svc.MessageService.FindOne(sqls.NewCnd().
		Eq("tenant_id", current.TenantID).
		Eq("conversation_id", conversationID).
		Eq("session_no", current.SessionNo).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		Where("recalled_at IS NULL AND send_status NOT IN (?, ?)", enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Desc("id"))
	return latest == nil || latest.ID == messageID
}
