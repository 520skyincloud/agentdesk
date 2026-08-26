package services

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const manualTimeoutNotice = "刚才由同事协助的这段接待先结束了，接下来我继续在。之后有问题随时发我。"

var ManualSessionTimeoutService = newManualSessionTimeoutService()

func newManualSessionTimeoutService() *manualSessionTimeoutService {
	return &manualSessionTimeoutService{}
}

type manualSessionTimeoutService struct{}

func (s *manualSessionTimeoutService) ScanAndRestoreExpired(limit int) int {
	now := time.Now()
	count := ConversationRouteService.ClearExpiredPendingActions(enums.ConversationPendingActionHumanHandoff, now, limit)
	states := ConversationRouteService.ListExpiredManualRoutes(now, limit)
	for _, state := range states {
		claimedState, claimed, claimErr := ConversationRouteService.ClaimExpiredManualRoute(state, now)
		if claimErr != nil {
			slog.Warn("manual session timeout claim failed", "conversation_id", state.ConversationID, "error", claimErr)
			continue
		}
		if !claimed || claimedState == nil {
			continue
		}
		if err := s.handleExpiredManualRoute(*claimedState, now); err != nil {
			slog.Warn("manual session timeout restore failed", "conversation_id", state.ConversationID, "error", err)
			continue
		}
		count++
	}
	return count
}

func (s *manualSessionTimeoutService) handleExpiredManualRoute(state models.ConversationRouteState, now time.Time) error {
	switch state.RouteStatus {
	case enums.ConversationRouteStatusStoreWecomManual:
		return s.handleExpiredStoreWecomManual(state, now)
	case enums.ConversationRouteStatusHQAgentDeskPending:
		return s.restoreWaitingRoute(state, now, "hq_pending_timeout", "总部网页待接入超时恢复AI")
	case enums.ConversationRouteStatusHQAgentDeskServing:
		if state.NeedHumanFollowUp {
			return s.restoreWaitingRoute(state, now, "hq_serving_wait_timeout", "总部网页人工接待未完成，超时恢复AI")
		}
		return s.restoreOne(state, now, "manual_idle_timeout", "人工服务空闲超时恢复AI", manualTimeoutNotice, false, state.RouteStatus)
	default:
		return nil
	}
}

func (s *manualSessionTimeoutService) handleExpiredStoreWecomManual(state models.ConversationRouteState, now time.Time) error {
	if state.NeedHumanFollowUp {
		return s.restoreWaitingRoute(state, now, "store_wecom_wait_timeout", "门店群人工跟进超时恢复AI")
	}
	return s.restoreOne(state, now, "manual_idle_timeout", "门店人工服务空闲超时恢复AI", manualTimeoutNotice, false, state.RouteStatus)
}

func (s *manualSessionTimeoutService) restoreWaitingRoute(state models.ConversationRouteState, now time.Time, timeoutStage string, reason string) error {
	if !s.isAIReplyEnabledForRoute(state) {
		if !AIManualResumeTaskService.EnsureForTimeout(state.ConversationID) {
			return fmt.Errorf("cannot persist AI manual resume task for conversation %d", state.ConversationID)
		}
		return s.markManualTimeoutBlockedByAIDisabled(state, now, timeoutStage)
	}
	if !AIManualResumeTaskService.EnsureForTimeout(state.ConversationID) {
		return fmt.Errorf("cannot persist AI manual resume task for conversation %d", state.ConversationID)
	}
	readyTask, prepared, err := s.prepareClaimedManualResume(state, now, timeoutStage, reason)
	if err != nil {
		return err
	}
	if !prepared {
		return nil
	}
	if state.RouteStatus == enums.ConversationRouteStatusStoreWecomManual {
		noticeReason := "门店员工暂未接入，AI 将先恢复处理。"
		if isSafetyHandoffReason(state.HandoffReason) {
			noticeReason = "安全/突发情况仍待人工跟进，AI 将先提供临时处理建议。"
		}
		noticeKey := fmt.Sprintf("manual_resume:%d:final", state.ConversationID)
		if readyTask != nil {
			noticeKey = readyTask.TaskKey + ":final"
		}
		ConversationHumanDispatchService.notifyStoreRoomHandoffWithKey(state.ConversationID, noticeReason+" "+state.HandoffReason, noticeKey)
	}
	return nil
}

func (s *manualSessionTimeoutService) prepareClaimedManualResume(state models.ConversationRouteState, now time.Time, timeoutStage string, reason string) (*models.AIManualResumeTask, bool, error) {
	if state.ID <= 0 || state.ManualExpireAt == nil {
		return nil, false, nil
	}
	var readyTask *models.AIManualResumeTask
	prepared := false
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		result := ctx.Tx.Model(&models.ConversationRouteState{}).
			Where("id = ? AND route_status = ? AND need_human_follow_up = ? AND manual_expire_at = ?", state.ID, state.RouteStatus, state.NeedHumanFollowUp, *state.ManualExpireAt).
			Updates(map[string]any{
				"manual_expire_at": nil,
				"updated_at":       now,
				"update_user_name": "system",
			})
		if result.Error != nil || result.RowsAffected != 1 {
			return result.Error
		}
		task := AIManualResumeTaskService.latestActiveTaskWithDB(ctx.Tx, state.ConversationID, []string{aiManualResumeTaskWaiting, aiManualResumeTaskBlockedAIDisabled})
		if task == nil {
			return fmt.Errorf("cannot find AI manual resume task for conversation %d", state.ConversationID)
		}
		if err := repositories.AIManualResumeTaskRepository.Updates(ctx.Tx, task.ID, map[string]any{
			"task_status":      aiManualResumeTaskReady,
			"ready_at":         now,
			"next_retry_at":    now,
			"last_error":       "",
			"updated_at":       now,
			"update_user_name": "system",
		}); err != nil {
			return err
		}
		if err := ConversationEventLogService.CreateEvent(ctx, state.ConversationID, enums.IMEventTypeTransfer, enums.IMSenderTypeSystem, 0, reason+"，已准备真实续答", ConversationService.buildEventPayload(map[string]any{
			"timeoutStage":   timeoutStage,
			"fromRoute":      state.RouteStatus,
			"expiredAt":      now.Format(time.RFC3339),
			"action":         "prepare_manual_resume",
			"timeoutMinutes": timeoutMinutesForStage(timeoutStage),
		})); err != nil {
			return err
		}
		copyTask := *task
		copyTask.TaskStatus = aiManualResumeTaskReady
		copyTask.ReadyAt = &now
		copyTask.NextRetryAt = &now
		readyTask = &copyTask
		prepared = true
		return nil
	})
	return readyTask, prepared, err
}

func (s *manualSessionTimeoutService) restoreOne(state models.ConversationRouteState, now time.Time, timeoutStage string, reason string, customerNotice string, resumeWaiting bool, fromStatus enums.ConversationRouteStatus) error {
	conversationID := state.ConversationID
	conversation := ConversationService.Get(conversationID)
	restored := false
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		restored, err = ConversationRouteService.restoreAIFromTimeoutClaimWithDB(ctx.Tx, state, reason, now, false)
		if err != nil || !restored {
			return err
		}
		return s.restoreConversationShellWithDB(ctx, conversationID, now, timeoutStage, reason, fromStatus)
	})
	if err != nil {
		return err
	}
	if !restored {
		return nil
	}
	if resumeWaiting {
		if !AIManualResumeTaskService.MarkReady(conversationID, now) {
			return fmt.Errorf("cannot mark AI manual resume task ready for conversation %d", conversationID)
		}
	}
	if conversation != nil && !resumeWaiting && s.isAIReplyEnabledForRoute(state) && strings.TrimSpace(customerNotice) != "" && s.shouldSendIdleRestoreNotice(conversationID) {
		if _, err := MessageService.SendAIServiceNoticeWithPayloadAndRequestID(conversationID, conversation.AIAgentID, customerNotice, `{"serviceEvent":"manual_ai_resumed_idle_timeout"}`, "manual_timeout"); err != nil {
			return err
		}
		_ = MessageSyncLogService.Create(conversationID, 0, enums.MessageSyncDirectionAgentDeskToWecom, "agentdesk", "store_wecom", "", enums.MessageSyncStatusSuccess, customerNotice, "")
	}
	if fromStatus == enums.ConversationRouteStatusHQAgentDeskServing {
		_, _ = KnowledgeCandidateService.ExtractFromResolvedConversation(conversationID, enums.KnowledgeCandidateSourceAgentDeskHQ)
	}
	if updated := ConversationService.Get(conversationID); updated != nil {
		WsService.PublishConversationChanged(updated, enums.IMRealtimeEventConversationUpdated)
	}
	return nil
}

func (s *manualSessionTimeoutService) shouldSendIdleRestoreNotice(conversationID int64) bool {
	latest := MessageService.FindOne(sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Where("recalled_at IS NULL AND send_status <> ?", enums.IMMessageStatusRecalled).
		Desc("seq_no").
		Desc("id"))
	return latest == nil || latest.SenderType != enums.IMSenderTypeAgent
}

func (s *manualSessionTimeoutService) isAIReplyEnabledForRoute(state models.ConversationRouteState) bool {
	if state.WxWorkInstanceID <= 0 {
		return true
	}
	instance := WxWorkProtocolInstanceService.Get(state.WxWorkInstanceID)
	return instance == nil || instance.AIReplyEnabled
}

func (s *manualSessionTimeoutService) markManualTimeoutBlockedByAIDisabled(state models.ConversationRouteState, now time.Time, timeoutStage string) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if state.ID <= 0 || state.ManualExpireAt == nil {
			return nil
		}
		result := ctx.Tx.Model(&models.ConversationRouteState{}).
			Where("id = ? AND route_status = ? AND need_human_follow_up = ? AND manual_expire_at = ?", state.ID, state.RouteStatus, state.NeedHumanFollowUp, *state.ManualExpireAt).
			Updates(map[string]any{
				"manual_expire_at":     nil,
				"need_human_follow_up": true,
				"handoff_reason":       "人工接待超时，但当前员工号AI回复已关闭",
				"updated_at":           now,
				"update_user_name":     "system",
			})
		if result.Error != nil || result.RowsAffected != 1 {
			return result.Error
		}
		task := AIManualResumeTaskService.latestActiveTaskWithDB(ctx.Tx, state.ConversationID, []string{
			aiManualResumeTaskWaiting,
			aiManualResumeTaskReady,
			aiManualResumeTaskRetry,
			aiManualResumeTaskRunning,
			aiManualResumeTaskBlockedAIDisabled,
		})
		if task == nil {
			return fmt.Errorf("cannot find AI manual resume task for conversation %d", state.ConversationID)
		}
		if err := repositories.AIManualResumeTaskRepository.Updates(ctx.Tx, task.ID, map[string]any{
			"task_status":      aiManualResumeTaskBlockedAIDisabled,
			"next_retry_at":    nil,
			"last_error":       "AI reply is disabled for the employee account",
			"updated_at":       now,
			"update_user_name": "system",
		}); err != nil {
			return err
		}
		return ConversationEventLogService.CreateEvent(ctx, state.ConversationID, enums.IMEventTypeTransfer, enums.IMSenderTypeSystem, 0,
			"人工接待超时，但当前员工号AI回复已关闭，仍需人工处理", ConversationService.buildEventPayload(map[string]any{
				"timeoutStage": timeoutStage,
				"fromRoute":    state.RouteStatus,
				"action":       "keep_manual_ai_disabled",
				"expiredAt":    now.Format(time.RFC3339),
			}))
	})
}

func (s *manualSessionTimeoutService) restoreConversationShell(conversationID int64, now time.Time, timeoutStage string, reason string, fromStatus enums.ConversationRouteStatus) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return s.restoreConversationShellWithDB(ctx, conversationID, now, timeoutStage, reason, fromStatus)
	})
}

func (s *manualSessionTimeoutService) restoreConversationShellWithDB(ctx *sqls.TxContext, conversationID int64, now time.Time, timeoutStage string, reason string, fromStatus enums.ConversationRouteStatus) error {
	conversation := repositories.ConversationRepository.Get(ctx.Tx, conversationID)
	if conversation == nil {
		return nil
	}
	if conversation.Status != enums.IMConversationStatusClosed {
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversationID, now); err != nil {
			return err
		}
		if err := repositories.ConversationRepository.Updates(ctx.Tx, conversationID, map[string]any{
			"status":              enums.IMConversationStatusAIServing,
			"current_team_id":     int64(0),
			"current_assignee_id": int64(0),
			"update_user_id":      int64(0),
			"update_user_name":    "system",
			"updated_at":          now,
		}); err != nil {
			return err
		}
	}
	return ConversationEventLogService.CreateEvent(ctx, conversationID, enums.IMEventTypeTransfer, enums.IMSenderTypeSystem, 0, reason, ConversationService.buildEventPayload(map[string]any{
		"timeoutStage":   timeoutStage,
		"fromRoute":      fromStatus,
		"toRoute":        enums.ConversationRouteStatusAIServing,
		"expiredAt":      now.Format(time.RFC3339),
		"action":         "restore_ai",
		"timeoutMinutes": timeoutMinutesForStage(timeoutStage),
	}))
}

func (s *manualSessionTimeoutService) createTimeoutEvent(conversationID int64, timeoutStage string, content string, fromStatus enums.ConversationRouteStatus, now time.Time) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return ConversationEventLogService.CreateEvent(ctx, conversationID, enums.IMEventTypeTransfer, enums.IMSenderTypeSystem, 0, content, ConversationService.buildEventPayload(map[string]any{
			"timeoutStage":   timeoutStage,
			"fromRoute":      fromStatus,
			"expiredAt":      now.Format(time.RFC3339),
			"action":         "send_store_room_reminder",
			"timeoutMinutes": DefaultStoreWecomSafetyManualMinutes,
		}))
	})
}

func timeoutMinutesForStage(stage string) int {
	switch stage {
	case "hq_pending_timeout":
		return DefaultHQAgentDeskPendingMinutes
	case "hq_serving_wait_timeout":
		return DefaultManualTimeoutMinutes
	case "store_wecom_wait_timeout":
		return DefaultStoreWecomManualMinutes
	case "manual_idle_timeout":
		return DefaultManualTimeoutMinutes
	default:
		return 0
	}
}
