package services

import (
	"encoding/json"
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
const storeSafetyTimeoutReminderKey = "storeSafetyTimeoutReminderSentAt"

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
		if err := s.handleExpiredManualRoute(state, now); err != nil {
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
		return s.restoreOne(state, now, "manual_idle_timeout", "人工服务空闲超时恢复AI", manualTimeoutNotice, false, state.RouteStatus)
	default:
		return nil
	}
}

func (s *manualSessionTimeoutService) handleExpiredStoreWecomManual(state models.ConversationRouteState, now time.Time) error {
	if isSafetyHandoffReason(state.HandoffReason) && !storeSafetyReminderAlreadySent(state.Remark) {
		ConversationHumanDispatchService.notifyStoreRoomHandoff(state.ConversationID, "安全/突发情况等待门店跟进超时，请尽快处理；"+state.HandoffReason)
		remark := buildStoreSafetyReminderRemark(state.Remark, now)
		expireAt := now.Add(DefaultStoreWecomManualMinutes * time.Minute)
		if err := ConversationRouteService.MarkStoreSafetyTimeoutReminder(state.ConversationID, expireAt, now, remark); err != nil {
			return err
		}
		return s.createTimeoutEvent(state.ConversationID, "store_safety_wait_timeout_reminded", "门店群安全风险跟进超时，已补发群提醒", state.RouteStatus, now)
	}
	if state.NeedHumanFollowUp {
		return s.restoreWaitingRoute(state, now, "store_wecom_wait_timeout", "门店群人工跟进超时恢复AI")
	}
	return s.restoreOne(state, now, "manual_idle_timeout", "门店人工服务空闲超时恢复AI", manualTimeoutNotice, false, state.RouteStatus)
}

func (s *manualSessionTimeoutService) restoreWaitingRoute(state models.ConversationRouteState, now time.Time, timeoutStage string, reason string) error {
	if !s.isAIReplyEnabledForRoute(state) {
		AIManualResumeTaskService.EnsureForTimeout(state.ConversationID)
		AIManualResumeTaskService.BlockForAIDisabled(state.ConversationID, now)
		return s.markManualTimeoutBlockedByAIDisabled(state, now, timeoutStage)
	}
	if !AIManualResumeTaskService.EnsureForTimeout(state.ConversationID) {
		return fmt.Errorf("cannot persist AI manual resume task for conversation %d", state.ConversationID)
	}
	return s.restoreOne(state, now, timeoutStage, reason, "", true, state.RouteStatus)
}

func (s *manualSessionTimeoutService) restoreOne(state models.ConversationRouteState, now time.Time, timeoutStage string, reason string, customerNotice string, resumeWaiting bool, fromStatus enums.ConversationRouteStatus) error {
	conversationID := state.ConversationID
	conversation := ConversationService.Get(conversationID)
	if err := s.restoreConversationShell(conversationID, now, timeoutStage, reason, fromStatus); err != nil {
		return err
	}
	if err := ConversationRouteService.RestoreAI(conversationID, reason, now); err != nil {
		return err
	}
	if resumeWaiting {
		if !AIManualResumeTaskService.MarkReady(conversationID, now) {
			return fmt.Errorf("cannot mark AI manual resume task ready for conversation %d", conversationID)
		}
	}
	if conversation != nil && !resumeWaiting && s.isAIReplyEnabledForRoute(state) && strings.TrimSpace(customerNotice) != "" {
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

func (s *manualSessionTimeoutService) isAIReplyEnabledForRoute(state models.ConversationRouteState) bool {
	if state.WxWorkInstanceID <= 0 {
		return true
	}
	instance := WxWorkProtocolInstanceService.Get(state.WxWorkInstanceID)
	return instance == nil || instance.AIReplyEnabled
}

func (s *manualSessionTimeoutService) markManualTimeoutBlockedByAIDisabled(state models.ConversationRouteState, now time.Time, timeoutStage string) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current := repositories.ConversationRouteStateRepository.Take(ctx.Tx, "conversation_id = ?", state.ConversationID)
		if current == nil || current.RouteStatus != state.RouteStatus {
			return nil
		}
		if err := repositories.ConversationRouteStateRepository.Updates(ctx.Tx, current.ID, map[string]any{
			"manual_expire_at":     nil,
			"need_human_follow_up": true,
			"handoff_reason":       "人工接待超时，但当前员工号AI回复已关闭",
			"updated_at":           now,
			"update_user_name":     "system",
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
	})
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
	case "store_wecom_wait_timeout":
		return DefaultStoreWecomManualMinutes
	case "manual_idle_timeout":
		return DefaultManualTimeoutMinutes
	default:
		return 0
	}
}

func storeSafetyReminderAlreadySent(remark string) bool {
	remark = strings.TrimSpace(remark)
	if remark == "" {
		return false
	}
	values := map[string]any{}
	if err := json.Unmarshal([]byte(remark), &values); err != nil {
		return strings.Contains(remark, storeSafetyTimeoutReminderKey)
	}
	value, _ := values[storeSafetyTimeoutReminderKey].(string)
	return strings.TrimSpace(value) != ""
}

func buildStoreSafetyReminderRemark(existing string, now time.Time) string {
	values := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		_ = json.Unmarshal([]byte(existing), &values)
	}
	values[storeSafetyTimeoutReminderKey] = now.Format(time.RFC3339)
	bytes, _ := json.Marshal(values)
	return string(bytes)
}
