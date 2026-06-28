package runtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	applicationruntime "agent-desk/internal/ai/application/runtime"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/tracex"
	svc "agent-desk/internal/services"
	"github.com/mlogclub/simple/sqls"
)

const aiReplyDebounceWindow = 700 * time.Millisecond
const aiReplyMediaSettleWindow = 4 * time.Second
const aiReplyMediaContextWindow = 8 * time.Second

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
		aiAgent := svc.AIAgentService.Get(conversation.AIAgentID)
		if aiAgent == nil || aiAgent.Status != enums.StatusOk {
			return
		}
		startedAt := time.Now()
		timeout := s.resolveReplyTimeout(*aiAgent)
		ctx, cancel := context.WithTimeout(tracex.ContextWithRequestID(context.Background(), message.RequestID), timeout)
		defer cancel()
		if err := s.TriggerReply(ctx, conversation, message, *aiAgent); err != nil {
			slog.Error("failed to trigger ai reply",
				"requestId", message.RequestID,
				"message_id", message.ID,
				"timeout_ms", timeout.Milliseconds(),
				"elapsed_ms", time.Since(startedAt).Milliseconds(),
				"error", err)
		}
	}()
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
	if !s.waitForConversationToSettle(ctx, conversation.ID, message.ID) {
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
	return s.executeReply(ctx, replyCtx)
}

func (s *aiReplyService) waitForConversationToSettle(ctx context.Context, conversationID int64, messageID int64) bool {
	if conversationID <= 0 || messageID <= 0 {
		return true
	}
	if !sleepWithContext(ctx, aiReplyDebounceWindow) {
		return false
	}
	if !s.isStillLatestCustomerMessage(conversationID, messageID) {
		slog.Info("skip ai reply because newer customer message arrived during debounce", "conversation_id", conversationID, "message_id", messageID)
		return false
	}
	deadline := time.Now().Add(aiReplyMediaSettleWindow)
	for time.Now().Before(deadline) {
		if !hasRecentPendingMediaUnderstanding(conversationID, messageID, aiReplyMediaContextWindow) {
			return true
		}
		if !sleepWithContext(ctx, 500*time.Millisecond) {
			return false
		}
		if !s.isStillLatestCustomerMessage(conversationID, messageID) {
			slog.Info("skip ai reply because newer customer message arrived while waiting media", "conversation_id", conversationID, "message_id", messageID)
			return false
		}
	}
	return true
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
	if summary != nil && strings.TrimSpace(summary.ReplyText) != "" {
		if !s.isStillLatestCustomerMessage(replyCtx.Conversation.ID, replyCtx.Message.ID) {
			slog.Info("skip stale ai reply because newer customer message arrived",
				"conversation_id", replyCtx.Conversation.ID,
				"message_id", replyCtx.Message.ID,
				"requestId", replyCtx.Message.RequestID,
			)
			return nil
		}
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
		replyCtx.Trace.ReplySent = replyMessage != nil
	}
	return nil
}

func (s *aiReplyService) isStillLatestCustomerMessage(conversationID int64, messageID int64) bool {
	latest, err := svc.MessageService.FindLatestByConversationID(conversationID)
	if err != nil || latest == nil {
		return true
	}
	if latest.SenderType == enums.IMSenderTypeCustomer {
		return latest.ID == messageID
	}
	return latest.ID <= messageID
}
