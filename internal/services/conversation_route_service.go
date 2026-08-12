package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"strings"
	"time"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	DefaultManualTimeoutMinutes           = 10
	DefaultHandoffConfirmationMinutes     = 5
	DefaultHQAgentDeskPendingMinutes      = 3
	DefaultStoreWecomManualMinutes        = 5
	DefaultStoreWecomSafetyManualMinutes  = 2
	DefaultConversationContextMaxMessages = 30
	DefaultConversationContextMaxTokens   = 8000
)

const defaultConversationSessionGap = 12 * time.Hour

var ConversationRouteService = newConversationRouteService()

func newConversationRouteService() *conversationRouteService {
	return &conversationRouteService{}
}

type conversationRouteService struct{}

func (s *conversationRouteService) GetByConversationID(conversationID int64) *models.ConversationRouteState {
	return repositories.ConversationRouteStateRepository.Take(sqls.DB(), "conversation_id = ?", conversationID)
}

func (s *conversationRouteService) GetByConversationIDInTenant(conversationID, tenantID int64) *models.ConversationRouteState {
	return repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversationID, tenantID)
}

func (s *conversationRouteService) Ensure(conversationID int64) (*models.ConversationRouteState, error) {
	return s.ensureWithDB(sqls.DB(), conversationID)
}

func (s *conversationRouteService) ensureWithDB(db *gorm.DB, conversationID int64) (*models.ConversationRouteState, error) {
	conversation, err := requireConversationParent(db, conversationID)
	if err != nil {
		return nil, err
	}
	if existing := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversationID, conversation.TenantID); existing != nil {
		return existing, nil
	}
	item := &models.ConversationRouteState{
		TenantID:         conversation.TenantID,
		ConversationID:   conversationID,
		RouteStatus:      enums.ConversationRouteStatusAIServing,
		RouteTarget:      "ai",
		SessionNo:        1,
		SessionStartedAt: routeTimePtr(time.Now()),
		AuditFields:      utils.BuildAuditFields(nil),
	}
	if err := repositories.ConversationRouteStateRepository.Create(db, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *conversationRouteService) CurrentSessionNo(conversationID int64) int {
	state, err := s.Ensure(conversationID)
	if err != nil || state == nil || state.SessionNo <= 0 {
		return 1
	}
	return state.SessionNo
}

func (s *conversationRouteService) EnsureActiveSessionForCustomerMessage(conversation *models.Conversation, now time.Time) (int, error) {
	if conversation == nil || conversation.ID <= 0 || conversation.TenantID <= 0 {
		return 1, errorsx.InvalidParam("会话不存在")
	}
	state, err := s.Ensure(conversation.ID)
	if err != nil {
		return 1, err
	}
	currentSessionNo := state.SessionNo
	if currentSessionNo <= 0 {
		currentSessionNo = 1
	}
	resultSessionNo := currentSessionNo
	startedNewSession := false
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current, lockErr := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
		if lockErr != nil {
			return lockErr
		}
		if current == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		lockedState, lockErr := repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(ctx.Tx, current.ID, current.TenantID)
		if lockErr != nil {
			return lockErr
		}
		if lockedState == nil {
			return errorsx.InvalidParam("会话路由不存在")
		}
		if lockedState.SessionNo <= 0 {
			lockedState.SessionNo = 1
			if updateErr := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, lockedState.ID, lockedState.TenantID, map[string]any{
				"session_no":       lockedState.SessionNo,
				"update_user_name": "system",
				"updated_at":       now,
			}); updateErr != nil {
				return updateErr
			}
		}
		resultSessionNo = lockedState.SessionNo
		shouldStartNew := current.Status == enums.IMConversationStatusClosed || lockedState.RouteStatus == enums.ConversationRouteStatusClosed
		if !shouldStartNew && !current.LastActiveAt.IsZero() && now.Sub(current.LastActiveAt) >= defaultConversationSessionGap {
			shouldStartNew = true
		}
		if !shouldStartNew {
			return nil
		}

		resultSessionNo = lockedState.SessionNo + 1
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, current.ID, now); err != nil {
			return err
		}
		if ctx.Tx.Migrator().HasTable(&models.ConversationTakeoverRequest{}) {
			if err := repositories.ConversationTakeoverRequestRepository.CancelPendingByConversationSession(
				ctx.Tx,
				current.TenantID,
				current.ID,
				lockedState.SessionNo,
				"session_changed",
				map[string]any{
					"updated_at":       now,
					"update_user_name": "system",
				},
			); err != nil {
				return err
			}
		}
		if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, lockedState.ID, lockedState.TenantID, map[string]any{
			"session_no":               resultSessionNo,
			"session_started_at":       now,
			"route_status":             enums.ConversationRouteStatusAIServing,
			"route_target":             "ai",
			"manual_expire_at":         nil,
			"pending_action":           "",
			"pending_action_payload":   "",
			"pending_action_expire_at": nil,
			"handoff_reason":           "",
			"need_human_follow_up":     false,
			"updated_at":               now,
			"update_user_name":         "system",
		}); err != nil {
			return err
		}
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, current.ID, current.TenantID, map[string]any{
			"status":              enums.IMConversationStatusAIServing,
			"priority":            0,
			"dispatch_weight":     1,
			"current_assignee_id": int64(0),
			"current_team_id":     int64(0),
			"handoff_at":          nil,
			"handoff_reason":      "",
			"ai_reply_rounds":     0,
			"closed_at":           nil,
			"closed_by":           int64(0),
			"close_reason":        "",
			"update_user_id":      int64(0),
			"update_user_name":    "system",
			"updated_at":          now,
		}); err != nil {
			return err
		}
		startedNewSession = true
		return nil
	})
	if err != nil {
		return currentSessionNo, err
	}
	if startedNewSession {
		conversation.Status = enums.IMConversationStatusAIServing
		conversation.Priority = 0
		conversation.DispatchWeight = 1
		conversation.CurrentAssigneeID = 0
		conversation.CurrentTeamID = 0
		conversation.HandoffAt = nil
		conversation.HandoffReason = ""
		conversation.AIReplyRounds = 0
		conversation.ClosedAt = nil
		conversation.ClosedBy = 0
		conversation.CloseReason = ""
	}
	return resultSessionNo, nil
}

func routeTimePtr(t time.Time) *time.Time {
	return &t
}

func (s *conversationRouteService) MarkCustomerMessage(conversationID int64, at time.Time) error {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"last_customer_message_at": at,
		"updated_at":               time.Now(),
		"update_user_name":         "system",
	}
	if state.RouteStatus == enums.ConversationRouteStatusHQAgentDeskServing {
		updates["need_human_follow_up"] = true
		updates["manual_expire_at"] = at.Add(DefaultManualTimeoutMinutes * time.Minute)
	}
	if state.RouteStatus == enums.ConversationRouteStatusStoreWecomManual {
		updates["need_human_follow_up"] = true
		updates["manual_expire_at"] = at.Add(DefaultManualTimeoutMinutes * time.Minute)
	}
	return repositories.ConversationRouteStateRepository.UpdatesInTenant(sqls.DB(), state.ID, state.TenantID, updates)
}

func (s *conversationRouteService) MarkAgentMessage(conversationID int64, at time.Time) error {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"updated_at":       time.Now(),
		"update_user_name": "system",
	}
	switch state.RouteStatus {
	case enums.ConversationRouteStatusHQAgentDeskServing:
		updates["need_human_follow_up"] = false
		updates["manual_expire_at"] = at.Add(DefaultManualTimeoutMinutes * time.Minute)
	case enums.ConversationRouteStatusStoreWecomManual:
		updates["need_human_follow_up"] = false
		updates["manual_expire_at"] = at.Add(DefaultManualTimeoutMinutes * time.Minute)
	default:
		return nil
	}
	return repositories.ConversationRouteStateRepository.UpdatesInTenant(sqls.DB(), state.ID, state.TenantID, updates)
}

// MarkExternalAgentMessage records a real reply sent from the bound WeCom employee account.
// A local reply is a human takeover even when the previous route had already returned to AI.
func (s *conversationRouteService) MarkExternalAgentMessage(conversationID int64, at time.Time) error {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"manual_expire_at":     at.Add(DefaultManualTimeoutMinutes * time.Minute),
		"need_human_follow_up": false,
		"updated_at":           time.Now(),
		"update_user_name":     "system",
	}
	enteredStoreManual := false
	switch state.RouteStatus {
	case enums.ConversationRouteStatusHQAgentDeskServing:
		// Headquarters is already actively serving this conversation; keep its ownership.
	case enums.ConversationRouteStatusStoreWecomManual:
		// A store employee replied to an existing store-manual route; only extend the idle timer.
	default:
		enteredStoreManual = true
		updates["route_status"] = enums.ConversationRouteStatusStoreWecomManual
		updates["route_target"] = "store_wecom"
		updates["pending_action"] = ""
		updates["pending_action_payload"] = ""
		updates["pending_action_expire_at"] = nil
		updates["handoff_reason"] = "企微员工号人工接待"
		updates["last_manual_handoff_at"] = at
	}
	if err := repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, updates); err != nil {
		return err
	}
	if !enteredStoreManual {
		return nil
	}
	return repositories.ConversationRepository.Updates(sqls.DB(), conversationID, map[string]any{
		"status":              enums.IMConversationStatusAIServing,
		"current_team_id":     int64(0),
		"current_assignee_id": int64(0),
		"updated_at":          at,
		"update_user_id":      int64(0),
		"update_user_name":    "system",
	})
}

func (s *conversationRouteService) SetPendingAction(conversationID int64, action enums.ConversationPendingAction, payload string, expireAt time.Time) error {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	return repositories.ConversationRouteStateRepository.UpdatesInTenant(sqls.DB(), state.ID, state.TenantID, map[string]any{
		"pending_action":           string(action),
		"pending_action_payload":   payload,
		"pending_action_expire_at": expireAt,
		"updated_at":               time.Now(),
		"update_user_name":         "system",
	})
}

func (s *conversationRouteService) ClearPendingAction(conversationID int64) error {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	return repositories.ConversationRouteStateRepository.UpdatesInTenant(sqls.DB(), state.ID, state.TenantID, map[string]any{
		"pending_action":           "",
		"pending_action_payload":   "",
		"pending_action_expire_at": nil,
		"updated_at":               time.Now(),
		"update_user_name":         "system",
	})
}

func (s *conversationRouteService) ClearExpiredPendingActions(action enums.ConversationPendingAction, now time.Time, limit int) int {
	states := s.ListExpiredPendingActions(action, now, limit)
	count := 0
	for _, state := range states {
		if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(sqls.DB(), state.ID, state.TenantID, map[string]any{
			"pending_action":           "",
			"pending_action_payload":   "",
			"pending_action_expire_at": nil,
			"updated_at":               now,
			"update_user_name":         "system",
		}); err == nil {
			count++
		}
	}
	return count
}

func (s *conversationRouteService) ConsumePendingAction(conversationID int64, action enums.ConversationPendingAction, now time.Time) (string, bool, error) {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return "", false, err
	}
	unlock := lockConversationHandoff(conversationID)
	defer unlock()

	payload := ""
	consumed := false
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		locked, lockErr := repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(ctx.Tx, conversationID, state.TenantID)
		if lockErr != nil {
			return lockErr
		}
		if locked == nil || locked.PendingAction == "" || locked.PendingAction != string(action) {
			return nil
		}
		payload = locked.PendingActionPayload
		expired := locked.PendingActionExpireAt != nil && now.After(*locked.PendingActionExpireAt)
		if updateErr := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, locked.ID, locked.TenantID, map[string]any{
			"pending_action":           "",
			"pending_action_payload":   "",
			"pending_action_expire_at": nil,
			"updated_at":               now,
			"update_user_name":         "system",
		}); updateErr != nil {
			return updateErr
		}
		consumed = !expired
		return nil
	})
	if err != nil {
		return "", false, err
	}
	if !consumed {
		return "", false, nil
	}
	return payload, true, nil
}

func (s *conversationRouteService) TrySetPendingAction(conversationID int64, action enums.ConversationPendingAction, payload string, expireAt time.Time) (bool, error) {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return false, err
	}
	claimed := false
	now := time.Now()
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		locked, lockErr := repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(ctx.Tx, conversationID, state.TenantID)
		if lockErr != nil {
			return lockErr
		}
		if locked == nil {
			return errorsx.InvalidParam("会话路由不存在")
		}
		if routeStatusBlocksAIReply(locked.RouteStatus) {
			return nil
		}
		if locked.PendingAction != "" && (locked.PendingActionExpireAt == nil || now.Before(*locked.PendingActionExpireAt)) {
			return nil
		}
		if updateErr := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, locked.ID, locked.TenantID, map[string]any{
			"pending_action":           string(action),
			"pending_action_payload":   payload,
			"pending_action_expire_at": expireAt,
			"updated_at":               now,
			"update_user_name":         "system",
		}); updateErr != nil {
			return updateErr
		}
		claimed = true
		return nil
	})
	return claimed, err
}

func (s *conversationRouteService) EnterHQAgentDeskPending(conversationID int64, reason string, now time.Time) (*models.ConversationRouteState, error) {
	return s.enterHQAgentDeskPendingWithDB(sqls.DB(), conversationID, reason, now)
}

func (s *conversationRouteService) enterHQAgentDeskPendingWithDB(db *gorm.DB, conversationID int64, reason string, now time.Time) (*models.ConversationRouteState, error) {
	state, err := s.ensureWithDB(db, conversationID)
	if err != nil {
		return nil, err
	}
	if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(db, state.ID, state.TenantID, map[string]any{
		"route_status":             enums.ConversationRouteStatusHQAgentDeskPending,
		"route_target":             "agentdesk_hq",
		"manual_expire_at":         now.Add(DefaultHQAgentDeskPendingMinutes * time.Minute),
		"pending_action":           "",
		"pending_action_payload":   "",
		"pending_action_expire_at": nil,
		"need_human_follow_up":     true,
		"handoff_reason":           reason,
		"updated_at":               now,
		"update_user_name":         "system",
	}); err != nil {
		return nil, err
	}
	if err := ServiceAnalyticsCaptureService.RecordQueueEntryWithDB(db, conversationID, state.TenantID, now); err != nil {
		return nil, err
	}
	updated := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversationID, state.TenantID)
	if _, err := ConversationDialogueStateService.CatchUpRouteStateDB(db, updated, now); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *conversationRouteService) EnterStoreWecomManual(conversationID int64, reason string, now time.Time) (*models.ConversationRouteState, error) {
	return s.enterStoreWecomManualWithDB(sqls.DB(), conversationID, reason, now)
}

func (s *conversationRouteService) enterStoreWecomManualWithDB(db *gorm.DB, conversationID int64, reason string, now time.Time) (*models.ConversationRouteState, error) {
	state, err := s.ensureWithDB(db, conversationID)
	if err != nil {
		return nil, err
	}
	expireAt := now.Add(DefaultStoreWecomManualMinutes * time.Minute)
	if isSafetyHandoffReason(reason) {
		expireAt = now.Add(DefaultStoreWecomSafetyManualMinutes * time.Minute)
	}
	if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(db, state.ID, state.TenantID, map[string]any{
		"route_status":             enums.ConversationRouteStatusStoreWecomManual,
		"route_target":             "store_wecom",
		"manual_expire_at":         expireAt,
		"pending_action":           "",
		"pending_action_payload":   "",
		"pending_action_expire_at": nil,
		"need_human_follow_up":     true,
		"handoff_reason":           reason,
		"updated_at":               now,
		"update_user_name":         "system",
	}); err != nil {
		return nil, err
	}
	if err := ServiceAnalyticsCaptureService.RecordQueueEntryWithDB(db, conversationID, state.TenantID, now); err != nil {
		return nil, err
	}
	updated := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversationID, state.TenantID)
	if _, err := ConversationDialogueStateService.CatchUpRouteStateDB(db, updated, now); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *conversationRouteService) MarkHumanFollowUpHandled(conversationID int64, now time.Time) error {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	return repositories.ConversationRouteStateRepository.UpdatesInTenant(sqls.DB(), state.ID, state.TenantID, map[string]any{
		"need_human_follow_up": false,
		"updated_at":           now,
		"update_user_name":     "system",
	})
}

func (s *conversationRouteService) HoldManualRouteForAIResume(conversationID int64, now time.Time) error {
	current, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		state, err := repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(ctx.Tx, conversationID, current.TenantID)
		if err != nil {
			return err
		}
		if state == nil {
			return errorsx.InvalidParam("会话路由不存在")
		}
		if !routeStatusBlocksAIReply(state.RouteStatus) {
			return nil
		}
		if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, state.ID, state.TenantID, map[string]any{
			"manual_expire_at": nil,
			"updated_at":       now,
			"update_user_name": "system",
		}); err != nil {
			return err
		}
		updated := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(ctx.Tx, conversationID, state.TenantID)
		_, err = ConversationDialogueStateService.CatchUpResumePendingDB(ctx.Tx, updated, now)
		return err
	})
}

func (s *conversationRouteService) EnterHQAgentDeskServing(conversationID int64, reason string, now time.Time) (*models.ConversationRouteState, error) {
	return s.enterHQAgentDeskServingWithDB(sqls.DB(), conversationID, reason, now)
}

func (s *conversationRouteService) enterHQAgentDeskServingWithDB(db *gorm.DB, conversationID int64, reason string, now time.Time) (*models.ConversationRouteState, error) {
	state, err := s.ensureWithDB(db, conversationID)
	if err != nil {
		return nil, err
	}
	locked, err := repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(db, conversationID, state.TenantID)
	if err != nil {
		return nil, err
	}
	if locked == nil {
		return nil, errorsx.InvalidParam("会话路由不存在")
	}
	state = locked
	expireAt := now.Add(DefaultManualTimeoutMinutes * time.Minute)
	if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(db, state.ID, state.TenantID, map[string]any{
		"route_status":             enums.ConversationRouteStatusHQAgentDeskServing,
		"route_target":             "agentdesk_hq",
		"manual_expire_at":         expireAt,
		"pending_action":           "",
		"pending_action_payload":   "",
		"pending_action_expire_at": nil,
		"need_human_follow_up":     true,
		"handoff_reason":           reason,
		"updated_at":               now,
		"update_user_name":         "system",
	}); err != nil {
		return nil, err
	}
	if db.Migrator().HasTable(&models.ConversationTakeoverRequest{}) {
		if err := repositories.ConversationTakeoverRequestRepository.CancelPendingByConversationSession(
			db,
			state.TenantID,
			conversationID,
			state.SessionNo,
			"conversation_assigned",
			map[string]any{
				"updated_at":       now,
				"update_user_name": "system",
			},
		); err != nil {
			return nil, err
		}
	}
	updated := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversationID, state.TenantID)
	if _, err := ConversationDialogueStateService.CatchUpRouteStateDB(db, updated, now); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *conversationRouteService) RestoreAI(conversationID int64, reason string, now time.Time) error {
	return s.RestoreAIWithFollowUp(conversationID, reason, now, false)
}

func (s *conversationRouteService) RestoreAIWithFollowUp(conversationID int64, reason string, now time.Time, needHumanFollowUp bool) error {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	return s.restoreAIWithFollowUpInTenant(conversationID, state.TenantID, reason, now, needHumanFollowUp)
}

func (s *conversationRouteService) restoreAIWithFollowUpInTenant(conversationID, tenantID int64, reason string, now time.Time, needHumanFollowUp bool) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		state, err := repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(ctx.Tx, conversationID, tenantID)
		if err != nil {
			return err
		}
		if state == nil {
			return errorsx.InvalidParam("会话路由不存在")
		}
		if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, state.ID, tenantID, map[string]any{
			"route_status":             enums.ConversationRouteStatusAIServing,
			"route_target":             "ai",
			"manual_expire_at":         nil,
			"pending_action":           "",
			"pending_action_payload":   "",
			"pending_action_expire_at": nil,
			"need_human_follow_up":     needHumanFollowUp,
			"handoff_reason":           reason,
			"updated_at":               now,
			"update_user_name":         "system",
		}); err != nil {
			return err
		}
		updated := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(ctx.Tx, conversationID, tenantID)
		_, err = ConversationDialogueStateService.CatchUpRouteStateDB(ctx.Tx, updated, now)
		return err
	})
}

// restoreAIWithFollowUpDB atomically returns a manually served conversation to
// AI. Callers must pass the conversation and route rows locked in the current
// transaction so assignment, route and dialogue state cannot diverge.
func (s *conversationRouteService) restoreAIWithFollowUpDB(
	ctx *sqls.TxContext,
	conversation *models.Conversation,
	route *models.ConversationRouteState,
	reason string,
	now time.Time,
	needHumanFollowUp bool,
	operator *dto.AuthPrincipal,
	source string,
) (bool, error) {
	if ctx == nil || ctx.Tx == nil || conversation == nil || route == nil ||
		conversation.ID <= 0 || conversation.TenantID <= 0 ||
		route.ConversationID != conversation.ID || route.TenantID != conversation.TenantID ||
		route.SessionNo <= 0 {
		return false, errorsx.InvalidParam("会话恢复AI上下文无效")
	}
	if conversation.Status == enums.IMConversationStatusClosed || route.RouteStatus == enums.ConversationRouteStatusClosed {
		return false, errorsx.InvalidParam("会话已关闭")
	}
	if (route.RouteStatus == enums.ConversationRouteStatusAIServing || route.RouteStatus == enums.ConversationRouteStatusAIFallback) &&
		conversation.CurrentAssigneeID == 0 {
		return false, nil
	}

	operatorID := int64(0)
	operatorName := "system"
	if operator != nil {
		operatorID = operator.UserID
		if operator.Username != "" {
			operatorName = operator.Username
		}
	}
	fromStatus := conversation.Status
	fromAssigneeID := conversation.CurrentAssigneeID
	fromRoute := route.RouteStatus
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "恢复AI接待"
	}
	source = strings.TrimSpace(source)

	if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversation.ID, now); err != nil {
		return false, err
	}
	if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, conversation.ID, conversation.TenantID, map[string]any{
		"status":              enums.IMConversationStatusAIServing,
		"current_assignee_id": int64(0),
		"current_team_id":     int64(0),
		"handoff_at":          nil,
		"handoff_reason":      "",
		"updated_at":          now,
		"update_user_id":      operatorID,
		"update_user_name":    operatorName,
	}); err != nil {
		return false, err
	}
	if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, route.ID, route.TenantID, map[string]any{
		"route_status":             enums.ConversationRouteStatusAIServing,
		"route_target":             "ai",
		"manual_expire_at":         nil,
		"pending_action":           "",
		"pending_action_payload":   "",
		"pending_action_expire_at": nil,
		"need_human_follow_up":     needHumanFollowUp,
		"handoff_reason":           reason,
		"updated_at":               now,
		"update_user_id":           operatorID,
		"update_user_name":         operatorName,
	}); err != nil {
		return false, err
	}
	if ctx.Tx.Migrator().HasTable(&models.ConversationTakeoverRequest{}) {
		if err := repositories.ConversationTakeoverRequestRepository.CancelPendingByConversationSession(
			ctx.Tx,
			conversation.TenantID,
			conversation.ID,
			route.SessionNo,
			"conversation_resumed_ai",
			map[string]any{
				"updated_at":       now,
				"update_user_id":   operatorID,
				"update_user_name": operatorName,
			},
		); err != nil {
			return false, err
		}
	}
	updatedRoute := *route
	updatedRoute.RouteStatus = enums.ConversationRouteStatusAIServing
	updatedRoute.RouteTarget = "ai"
	updatedRoute.ManualExpireAt = nil
	updatedRoute.PendingAction = ""
	updatedRoute.PendingActionPayload = ""
	updatedRoute.PendingActionExpireAt = nil
	updatedRoute.NeedHumanFollowUp = needHumanFollowUp
	updatedRoute.HandoffReason = reason
	updatedRoute.UpdatedAt = now
	if _, err := ConversationDialogueStateService.CatchUpRouteStateDB(ctx.Tx, &updatedRoute, now); err != nil {
		return false, err
	}
	if err := ConversationEventLogService.CreateEvent(ctx, conversation.ID, enums.IMEventTypeTransfer, enums.IMSenderTypeAgent, operatorID, reason, ConversationService.buildEventPayload(map[string]any{
		"source":            source,
		"fromStatus":        fromStatus,
		"toStatus":          enums.IMConversationStatusAIServing,
		"fromAssigneeId":    fromAssigneeID,
		"toAssigneeId":      0,
		"fromRoute":         fromRoute,
		"toRoute":           enums.ConversationRouteStatusAIServing,
		"sessionNo":         route.SessionNo,
		"needHumanFollowUp": needHumanFollowUp,
	})); err != nil {
		return false, err
	}
	return true, nil
}

func (s *conversationRouteService) RestoreAIWithFollowUpInTenant(conversationID, tenantID int64, reason string, now time.Time, needHumanFollowUp bool) error {
	return s.restoreAIWithFollowUpInTenant(conversationID, tenantID, reason, now, needHumanFollowUp)
}

func (s *conversationRouteService) MarkStoreSafetyTimeoutReminder(conversationID int64, expireAt time.Time, now time.Time, remark string) error {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	return repositories.ConversationRouteStateRepository.UpdatesInTenant(sqls.DB(), state.ID, state.TenantID, map[string]any{
		"manual_expire_at": expireAt,
		"remark":           remark,
		"updated_at":       now,
		"update_user_name": "system",
	})
}

func (s *conversationRouteService) ListExpiredPendingActions(action enums.ConversationPendingAction, now time.Time, limit int) []models.ConversationRouteState {
	if limit <= 0 {
		limit = 50
	}
	return repositories.ConversationRouteStateRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("pending_action", string(action)).
		Where("pending_action_expire_at IS NOT NULL AND pending_action_expire_at <= ?", now).
		Asc("pending_action_expire_at").
		Limit(limit))
}

func (s *conversationRouteService) ListExpiredManualRoutes(now time.Time, limit int) []models.ConversationRouteState {
	if limit <= 0 {
		limit = 50
	}
	return repositories.ConversationRouteStateRepository.Find(sqls.DB(), sqls.NewCnd().
		In("route_status", []enums.ConversationRouteStatus{
			enums.ConversationRouteStatusStoreWecomManual,
			enums.ConversationRouteStatusHQAgentDeskPending,
			enums.ConversationRouteStatusHQAgentDeskServing,
		}).
		Where("manual_expire_at IS NOT NULL AND manual_expire_at <= ?", now).
		Asc("manual_expire_at").
		Limit(limit))
}

func (s *conversationRouteService) ListExpiredHQAgentDeskServing(now time.Time, limit int) []models.ConversationRouteState {
	if limit <= 0 {
		limit = 50
	}
	return repositories.ConversationRouteStateRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("route_status", enums.ConversationRouteStatusHQAgentDeskServing).
		Where("manual_expire_at IS NOT NULL AND manual_expire_at <= ?", now).
		Asc("manual_expire_at").
		Limit(limit))
}
