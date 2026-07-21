package services

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const dispatchAttentionNotificationType = "conversation_dispatch_attention"

func (s *conversationDispatchService) notifyUnassignedConversationIfOverdue(conversation *models.Conversation, teamID int64, reasonCode, reason string, now time.Time) {
	if conversation == nil || conversation.TenantID <= 0 {
		return
	}
	pendingAt := pendingConversationAt(*conversation)
	queueTargetSeconds := ServiceAnalyticsService.GetPolicy(conversation.TenantID).QueueTargetSeconds
	if queueTargetSeconds <= 0 {
		queueTargetSeconds = 60
	}
	if pendingAt.IsZero() || now.Before(pendingAt.Add(time.Duration(queueTargetSeconds)*time.Second)) {
		return
	}
	s.notifyDispatchAttentionOnce(conversation, teamID, "dispatch_unassigned:"+strings.TrimSpace(reasonCode), "人工会话等待派发", reason, pendingAt)
}

func (s *conversationDispatchService) notifyRecoveredConversation(conversation *models.Conversation, assignment *models.ConversationAssignment, reason string) {
	if conversation == nil || assignment == nil {
		return
	}
	since := assignment.CreatedAt
	if since.IsZero() {
		since = pendingConversationAt(*conversation)
	}
	s.notifyDispatchAttentionOnce(conversation, conversation.CurrentTeamID, "dispatch_recovery", "人工会话需要编排", reason, since)
}

func (s *conversationDispatchService) notifyDispatchAttentionOnce(conversation *models.Conversation, teamID int64, bizType, title, reason string, since time.Time) {
	if conversation == nil || conversation.TenantID <= 0 || conversation.ID <= 0 {
		return
	}
	recipientID := dispatchAttentionRecipient(conversation.TenantID, teamID)
	if recipientID <= 0 {
		return
	}
	if since.IsZero() {
		since = conversation.CreatedAt
	}
	if existing := repositories.NotificationRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", conversation.TenantID).
		Eq("recipient_user_id", recipientID).
		Eq("notification_type", dispatchAttentionNotificationType).
		Eq("biz_type", bizType).
		Eq("biz_id", conversation.ID).
		Gte("created_at", since).
		Limit(1)); len(existing) > 0 {
		return
	}
	content := fmt.Sprintf("会话 #%d 尚需人工编排", conversation.ID)
	if summary := strings.TrimSpace(ConversationService.BuildConversationSummary(conversation)); summary != "" {
		content += "\n" + summary
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		content += "\n原因: " + reason
	}
	if _, err := NotificationService.CreateAndPushInTenant(request.CreateNotificationRequest{
		RecipientUserID:  recipientID,
		Title:            title,
		Content:          content,
		NotificationType: dispatchAttentionNotificationType,
		BizType:          bizType,
		BizID:            conversation.ID,
		ActionURL:        fmt.Sprintf("/dashboard/conversation-dispatch?keyword=%d", conversation.ID),
	}, conversation.TenantID); err != nil {
		slog.Warn("create dispatch attention notification failed", "conversation_id", conversation.ID, "recipient_user_id", recipientID, "error", err)
	}
}

func dispatchAttentionRecipient(tenantID, teamID int64) int64 {
	if tenantID <= 0 {
		return 0
	}
	if team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), teamID, tenantID); team != nil && team.LeaderUserID > 0 {
		if user := repositories.UserRepository.GetInTenant(sqls.DB(), team.LeaderUserID, tenantID); user != nil && user.Status == enums.StatusOk && user.DeletedAt == nil {
			return user.ID
		}
	}
	supervisors, err := repositories.UserRepository.FindTenantSupervisors(sqls.DB(), []int64{tenantID}, constants.RoleCodeTenantAdmin)
	if err != nil || supervisors[tenantID] == nil {
		return 0
	}
	return supervisors[tenantID].ID
}
