package services

import (
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var ConversationAIRecoveryService = &conversationAIRecoveryService{}

type conversationAIRecoveryService struct{}

// Restore returns a manual-routed conversation to AI service as one domain
// operation. It is internal maintenance/runtime plumbing, not a public API.
func (s *conversationAIRecoveryService) Restore(conversationID int64, reason string, now time.Time) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "恢复AI接待"
	}
	var updatedConversation *models.Conversation
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		parent, err := requireConversationParent(ctx.Tx, conversationID)
		if err != nil {
			return err
		}
		conversation, err := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, parent.ID, parent.TenantID)
		if err != nil {
			return err
		}
		if conversation == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		if conversation.Status == enums.IMConversationStatusClosed {
			return errorsx.InvalidParam("已关闭会话不能恢复AI接待")
		}
		route, err := repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
		if err != nil {
			return err
		}
		if route == nil {
			return errorsx.InvalidParam("会话路由不存在")
		}
		fromConversationStatus := conversation.Status
		fromRouteStatus := route.RouteStatus
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversation.ID, now); err != nil {
			return err
		}
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, conversation.ID, conversation.TenantID, map[string]any{
			"status":              enums.IMConversationStatusAIServing,
			"current_team_id":     int64(0),
			"current_assignee_id": int64(0),
			"handoff_at":          nil,
			"handoff_reason":      "",
			"update_user_id":      int64(0),
			"update_user_name":    "system",
			"updated_at":          now,
		}); err != nil {
			return err
		}
		if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, route.ID, route.TenantID, map[string]any{
			"route_status":             enums.ConversationRouteStatusAIServing,
			"route_target":             "ai",
			"manual_expire_at":         nil,
			"pending_action":           "",
			"pending_action_payload":   "",
			"pending_action_expire_at": nil,
			"need_human_follow_up":     false,
			"handoff_reason":           reason,
			"updated_at":               now,
			"update_user_name":         "system",
		}); err != nil {
			return err
		}
		if ctx.Tx.Migrator().HasTable(&models.AIManualResumeTask{}) {
			if err := repositories.AIManualResumeTaskRepository.CancelActiveByConversationInTenant(ctx.Tx, conversation.ID, conversation.TenantID, map[string]any{
				"task_status":      aiManualResumeTaskCancelled,
				"completed_at":     now,
				"last_error":       reason,
				"updated_at":       now,
				"update_user_name": "system",
			}); err != nil {
				return err
			}
		}
		updatedRoute := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
		if _, err := ConversationDialogueStateService.CatchUpRouteStateDB(ctx.Tx, updatedRoute, now); err != nil {
			return err
		}
		if err := ConversationEventLogService.CreateEvent(ctx, conversation.ID, enums.IMEventTypeTransfer, enums.IMSenderTypeSystem, 0, reason, ConversationService.buildEventPayload(map[string]any{
			"action":                 "restore_ai_complete",
			"fromConversationStatus": fromConversationStatus,
			"toConversationStatus":   enums.IMConversationStatusAIServing,
			"fromRoute":              fromRouteStatus,
			"toRoute":                enums.ConversationRouteStatusAIServing,
		})); err != nil {
			return err
		}
		updatedConversation = repositories.ConversationRepository.GetInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
		return nil
	})
	if err != nil {
		return err
	}
	if updatedConversation != nil {
		WsService.PublishConversationChanged(updatedConversation, enums.IMRealtimeEventConversationUpdated)
	}
	return nil
}
