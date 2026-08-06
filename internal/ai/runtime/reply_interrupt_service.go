package runtime

import (
	"context"
	"fmt"
	"strings"

	applicationruntime "agent-desk/internal/ai/application/runtime"
	"agent-desk/internal/ai/runtime/graphs"
	"agent-desk/internal/models"
	svc "agent-desk/internal/services"
)

type replyInterruptService struct{}

func newReplyInterruptService() *replyInterruptService {
	return &replyInterruptService{}
}

func (s *replyInterruptService) ResumePendingInterrupt(ctx context.Context, owner *aiReplyService, replyCtx aiReplyContext) (svc.AIReplyExecutionResult, error) {
	if replyCtx.PendingInterrupt == nil {
		return svc.AIReplyExecutionResult{}, fmt.Errorf("pending interrupt is required")
	}
	summary, err := owner.executor.ResumePendingInterrupt(ctx, runtimeReplyResumeInput{
		Conversation:     replyCtx.Conversation,
		Message:          replyCtx.Message,
		AIAgent:          replyCtx.AIAgent,
		PendingInterrupt: replyCtx.PendingInterrupt,
		Trace:            replyCtx.Trace,
	})
	replyCtx.setSummary(summary)
	if err != nil {
		if isCheckpointMissingError(err) {
			summary = expiredInterruptSummary()
			replyCtx.setSummary(summary)
			replyCtx.Trace.Status = "interrupt_expired"
			replyCtx.Trace.FinalAction = "expired"
			replyMessages, expireErr := owner.commit.CommitAIReplyBatch(replyCommitInput{
				Conversation: replyCtx.Conversation,
				Message:      replyCtx.Message,
				AIAgent:      replyCtx.AIAgent,
				ReplyText:    summary.ReplyText,
				Trace:        replyCtx.Trace,
				ClientPrefix: "ai_interrupt_expired",
			})
			if expireErr != nil {
				return svc.AIReplyExecutionResult{}, expireErr
			}
			lastResumeMessageID := int64(0)
			if len(replyMessages) > 0 {
				lastResumeMessageID = replyMessages[len(replyMessages)-1].ID
			}
			if expireMarkErr := svc.ConversationInterruptService.MarkExpired(replyCtx.PendingInterrupt.ID, lastResumeMessageID); expireMarkErr != nil {
				return svc.AIReplyExecutionResult{}, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorCommitFailed, expireMarkErr)
			}
			return completedInterruptResult("interrupt_expired", replyMessages, 0), nil
		}
		return svc.AIReplyExecutionResult{}, err
	}
	if summary != nil && summary.Interrupted {
		return s.HandleInterruptedResume(owner, replyCtx, summary)
	}
	if summary != nil && strings.TrimSpace(summary.ReplyText) != "" {
		replyMessages, err := owner.commit.CommitAIReplyBatch(replyCommitInput{
			Conversation: replyCtx.Conversation,
			Message:      replyCtx.Message,
			AIAgent:      replyCtx.AIAgent,
			ReplyText:    summary.ReplyText,
			Trace:        replyCtx.Trace,
			ClientPrefix: "ai_resume",
		})
		if err != nil {
			return svc.AIReplyExecutionResult{}, err
		}
		replyMessageID := int64(0)
		if len(replyMessages) > 0 {
			replyMessageID = replyMessages[len(replyMessages)-1].ID
		}
		if graphs.IsCancellationReply(summary.ReplyText) {
			if err := svc.ConversationInterruptService.MarkCancelled(replyCtx.PendingInterrupt.ID, replyMessageID); err != nil {
				return svc.AIReplyExecutionResult{}, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorCommitFailed, err)
			}
			return completedInterruptResult("interrupt_cancelled", replyMessages, 0), nil
		}
		if err := svc.ConversationInterruptService.MarkResolved(replyCtx.PendingInterrupt.ID, replyMessageID); err != nil {
			return svc.AIReplyExecutionResult{}, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorCommitFailed, err)
		}
		return completedInterruptResult("interrupt_resolved", replyMessages, 0), nil
	}
	return svc.AIReplyExecutionResult{}, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorEmptyOutput, fmt.Errorf("interrupt resume produced no output"))
}

func (s *replyInterruptService) HandleInterruptedSummary(owner *aiReplyService, replyCtx aiReplyContext, summary *applicationruntime.Summary) (svc.AIReplyExecutionResult, error) {
	pending := buildConversationInterrupt(replyCtx.Conversation, replyCtx.Message, replyCtx.AIAgent, summary)
	if err := svc.ConversationInterruptService.CreateOrUpdatePending(pending); err != nil {
		return svc.AIReplyExecutionResult{}, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorCommitFailed, err)
	}
	pending = svc.ConversationInterruptService.GetByCheckPointIDInTenant(summary.CheckPointID, replyCtx.Conversation.TenantID)
	if pending == nil || pending.SourceMessageID != replyCtx.Message.ID {
		return svc.AIReplyExecutionResult{}, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorCommitFailed, fmt.Errorf("persisted interrupt evidence unavailable"))
	}
	replyText := resolveInterruptPrompt(summary)
	replyMessages, err := owner.commit.CommitAIReplyBatch(replyCommitInput{
		Conversation: replyCtx.Conversation,
		Message:      replyCtx.Message,
		AIAgent:      replyCtx.AIAgent,
		ReplyText:    replyText,
		Trace:        replyCtx.Trace,
		ClientPrefix: "ai_interrupt",
	})
	if err != nil {
		return svc.AIReplyExecutionResult{}, err
	}
	if len(replyMessages) > 0 {
		if err := svc.ConversationInterruptService.MarkPendingAgain(pending.ID, pending.InterruptID, replyText, replyMessages[len(replyMessages)-1].ID); err != nil {
			return svc.AIReplyExecutionResult{}, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorCommitFailed, err)
		}
	}
	return completedInterruptResult("runtime_interrupted", replyMessages, pending.ID), nil
}

func (s *replyInterruptService) HandleInterruptedResume(owner *aiReplyService, replyCtx aiReplyContext, summary *applicationruntime.Summary) (svc.AIReplyExecutionResult, error) {
	if replyCtx.PendingInterrupt == nil {
		return svc.AIReplyExecutionResult{}, fmt.Errorf("pending interrupt is required")
	}
	replyText := resolveInterruptPrompt(summary)
	replyMessages, err := owner.commit.CommitAIReplyBatch(replyCommitInput{
		Conversation: replyCtx.Conversation,
		Message:      replyCtx.Message,
		AIAgent:      replyCtx.AIAgent,
		ReplyText:    replyText,
		Trace:        replyCtx.Trace,
		ClientPrefix: "ai_interrupt_resume",
	})
	if err != nil {
		return svc.AIReplyExecutionResult{}, err
	}
	if len(replyMessages) > 0 {
		if err := svc.ConversationInterruptService.MarkPendingAgain(replyCtx.PendingInterrupt.ID, firstInterruptID(summary), replyText, replyMessages[len(replyMessages)-1].ID); err != nil {
			return svc.AIReplyExecutionResult{}, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorCommitFailed, err)
		}
	}
	return completedInterruptResult("interrupt_resumed", replyMessages, 0), nil
}

func completedInterruptResult(reason string, messages []models.Message, interruptID int64) svc.AIReplyExecutionResult {
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		if message.ID > 0 {
			ids = append(ids, message.ID)
		}
	}
	return svc.AIReplyExecutionResult{
		Status: svc.AIReplyExecutionStatusCompleted, ReasonCode: reason,
		CommittedMessageIDs: ids, PersistedInterruptID: interruptID,
	}
}
