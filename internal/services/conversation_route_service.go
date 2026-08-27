package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"time"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

func (s *conversationRouteService) Ensure(conversationID int64) (*models.ConversationRouteState, error) {
	return s.ensureWithDB(sqls.DB(), conversationID)
}

func (s *conversationRouteService) ensureWithDB(db *gorm.DB, conversationID int64) (*models.ConversationRouteState, error) {
	if conversationID <= 0 {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	if existing := repositories.ConversationRouteStateRepository.Take(db, "conversation_id = ?", conversationID); existing != nil {
		return existing, nil
	}
	item := &models.ConversationRouteState{
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

func (s *conversationRouteService) lockByConversationIDWithDB(db *gorm.DB, conversationID int64) (*models.ConversationRouteState, error) {
	if db == nil || conversationID <= 0 {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	if _, err := s.ensureWithDB(db, conversationID); err != nil {
		return nil, err
	}
	state := &models.ConversationRouteState{}
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("conversation_id = ?", conversationID).
		Take(state).Error; err != nil {
		return nil, err
	}
	return state, nil
}

func (s *conversationRouteService) CurrentSessionNo(conversationID int64) int {
	state, err := s.Ensure(conversationID)
	if err != nil || state == nil || state.SessionNo <= 0 {
		return 1
	}
	return state.SessionNo
}

func (s *conversationRouteService) EnsureActiveSessionForCustomerMessage(conversation *models.Conversation, now time.Time) (int, error) {
	if conversation == nil || conversation.ID <= 0 {
		return 1, errorsx.InvalidParam("会话不存在")
	}
	state, err := s.Ensure(conversation.ID)
	if err != nil {
		return 1, err
	}
	if state.SessionNo <= 0 {
		state.SessionNo = 1
	}
	shouldStartNew := conversation.Status == enums.IMConversationStatusClosed || state.RouteStatus == enums.ConversationRouteStatusClosed
	if !shouldStartNew && !conversation.LastActiveAt.IsZero() && now.Sub(conversation.LastActiveAt) >= defaultConversationSessionGap {
		shouldStartNew = true
	}
	if !shouldStartNew {
		return state.SessionNo, nil
	}
	nextSessionNo := state.SessionNo + 1
	if err := repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
		"session_no":               nextSessionNo,
		"session_started_at":       now,
		"route_status":             enums.ConversationRouteStatusAIServing,
		"route_target":             "ai",
		"manual_expire_at":         nil,
		"pending_action":           "",
		"pending_action_payload":   "",
		"pending_action_expire_at": nil,
		"handoff_reason":           "",
		"updated_at":               now,
		"update_user_name":         "system",
	}); err != nil {
		return state.SessionNo, err
	}
	if conversation.Status == enums.IMConversationStatusClosed {
		_ = repositories.ConversationRepository.Updates(sqls.DB(), conversation.ID, map[string]any{
			"status":           enums.IMConversationStatusAIServing,
			"closed_at":        nil,
			"closed_by":        int64(0),
			"close_reason":     "",
			"update_user_id":   int64(0),
			"update_user_name": "system",
			"updated_at":       now,
		})
	}
	return nextSessionNo, nil
}

func routeTimePtr(t time.Time) *time.Time {
	return &t
}

func (s *conversationRouteService) MarkCustomerMessage(conversationID int64, at time.Time) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		state, err := s.lockByConversationIDWithDB(ctx.Tx, conversationID)
		if err != nil {
			return err
		}
		return s.markCustomerMessageWithDB(ctx.Tx, state, at)
	})
}

func (s *conversationRouteService) markCustomerMessageWithDB(db *gorm.DB, state *models.ConversationRouteState, at time.Time) error {
	if db == nil || state == nil {
		return errorsx.InvalidParam("会话路由不存在")
	}
	updates := map[string]any{
		"last_customer_message_at": at,
		"updated_at":               time.Now(),
		"update_user_name":         "system",
	}
	if state.RouteStatus == enums.ConversationRouteStatusHQAgentDeskServing {
		updates["manual_expire_at"] = at.Add(DefaultManualTimeoutMinutes * time.Minute)
		updates["need_human_follow_up"] = true
	}
	if state.RouteStatus == enums.ConversationRouteStatusStoreWecomManual {
		updates["manual_expire_at"] = at.Add(DefaultManualTimeoutMinutes * time.Minute)
		updates["need_human_follow_up"] = true
	}
	if state.RouteStatus == enums.ConversationRouteStatusHQAgentDeskPending {
		updates["need_human_follow_up"] = true
	}
	return repositories.ConversationRouteStateRepository.Updates(db, state.ID, updates)
}

func (s *conversationRouteService) MarkAgentMessage(conversationID int64, at time.Time) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		state, err := s.lockByConversationIDWithDB(ctx.Tx, conversationID)
		if err != nil {
			return err
		}
		return s.markAgentMessageWithDB(ctx.Tx, state, at)
	})
}

func (s *conversationRouteService) markAgentMessageWithDB(db *gorm.DB, state *models.ConversationRouteState, at time.Time) error {
	if db == nil || state == nil {
		return errorsx.InvalidParam("会话路由不存在")
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
	return repositories.ConversationRouteStateRepository.Updates(db, state.ID, updates)
}

// MarkExternalAgentMessage records a real reply sent from the bound WeCom employee account.
// A local reply is a human takeover even when the previous route had already returned to AI.
func (s *conversationRouteService) MarkExternalAgentMessage(conversationID int64, at time.Time) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		state, err := s.lockByConversationIDWithDB(ctx.Tx, conversationID)
		if err != nil {
			return err
		}
		return s.markExternalAgentMessageWithDB(ctx, state, at)
	})
}

func (s *conversationRouteService) markExternalAgentMessageWithDB(ctx *sqls.TxContext, state *models.ConversationRouteState, at time.Time) error {
	if ctx == nil || ctx.Tx == nil || state == nil {
		return errorsx.InvalidParam("会话路由不存在")
	}
	updates := map[string]any{
		"route_status":             enums.ConversationRouteStatusStoreWecomManual,
		"route_target":             "store_wecom",
		"manual_expire_at":         at.Add(DefaultManualTimeoutMinutes * time.Minute),
		"need_human_follow_up":     false,
		"pending_action":           "",
		"pending_action_payload":   "",
		"pending_action_expire_at": nil,
		"handoff_reason":           "企微员工号人工接待",
		"updated_at":               time.Now(),
		"update_user_name":         "system",
	}
	if state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual {
		updates["last_manual_handoff_at"] = at
	}
	if err := repositories.ConversationRouteStateRepository.Updates(ctx.Tx, state.ID, updates); err != nil {
		return err
	}
	if err := ConversationAssignmentService.FinishActiveAssignments(ctx, state.ConversationID, at); err != nil {
		return err
	}
	return repositories.ConversationRepository.Updates(ctx.Tx, state.ConversationID, map[string]any{
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
	return repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
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
	return repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
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
		if err := repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
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
	if state.PendingAction == "" || state.PendingAction != string(action) {
		return "", false, nil
	}
	if state.PendingActionExpireAt != nil && now.After(*state.PendingActionExpireAt) {
		_ = s.ClearPendingAction(conversationID)
		return "", false, nil
	}
	payload := state.PendingActionPayload
	if err := s.ClearPendingAction(conversationID); err != nil {
		return "", false, err
	}
	return payload, true, nil
}

func (s *conversationRouteService) EnterHQAgentDeskPending(conversationID int64, reason string, now time.Time) (*models.ConversationRouteState, error) {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return nil, err
	}
	if err := repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
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
	_, _ = ChannelMessageOutboxService.CancelPendingOrdinaryAI(conversationID, 0, "cancelled because conversation entered pending human service")
	return s.GetByConversationID(conversationID), nil
}

func (s *conversationRouteService) EnterStoreWecomManual(conversationID int64, reason string, now time.Time) (*models.ConversationRouteState, error) {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return nil, err
	}
	expireAt := now.Add(DefaultStoreWecomManualMinutes * time.Minute)
	if err := repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
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
	_, _ = ChannelMessageOutboxService.CancelPendingOrdinaryAI(conversationID, 0, "cancelled because conversation entered store human service")
	return s.GetByConversationID(conversationID), nil
}

func (s *conversationRouteService) MarkHumanFollowUpHandled(conversationID int64, now time.Time) error {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	return repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
		"need_human_follow_up": false,
		"updated_at":           now,
		"update_user_name":     "system",
	})
}

func (s *conversationRouteService) HoldManualRouteForAIResume(conversationID int64, now time.Time) error {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	if !routeStatusBlocksAIReply(state.RouteStatus) {
		return nil
	}
	return repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
		"manual_expire_at": nil,
		"updated_at":       now,
		"update_user_name": "system",
	})
}

func (s *conversationRouteService) ClaimExpiredManualRoute(state models.ConversationRouteState, now time.Time) (*models.ConversationRouteState, bool, error) {
	if state.ID <= 0 || state.ManualExpireAt == nil || state.ManualExpireAt.After(now) || !routeStatusBlocksAIReply(state.RouteStatus) {
		return nil, false, nil
	}
	// manual_expire_at is stored as DATETIME without fractional seconds. Return
	// the exact persisted precision so the follow-up CAS can match on MySQL.
	leaseExpireAt := now.Add(time.Minute).Truncate(time.Second)
	result := sqls.DB().Model(&models.ConversationRouteState{}).
		Where("id = ? AND route_status = ? AND need_human_follow_up = ? AND manual_expire_at = ? AND manual_expire_at <= ?", state.ID, state.RouteStatus, state.NeedHumanFollowUp, *state.ManualExpireAt, now).
		Updates(map[string]any{
			"manual_expire_at": leaseExpireAt,
			"updated_at":       now,
			"update_user_name": "system",
		})
	if result.Error != nil || result.RowsAffected != 1 {
		return nil, false, result.Error
	}
	state.ManualExpireAt = &leaseExpireAt
	return &state, true, nil
}

func (s *conversationRouteService) HoldManualRouteForAIResumeClaimed(state models.ConversationRouteState, now time.Time) (bool, error) {
	if state.ID <= 0 || state.ManualExpireAt == nil || !routeStatusBlocksAIReply(state.RouteStatus) {
		return false, nil
	}
	result := sqls.DB().Model(&models.ConversationRouteState{}).
		Where("id = ? AND route_status = ? AND need_human_follow_up = ? AND manual_expire_at = ?", state.ID, state.RouteStatus, state.NeedHumanFollowUp, *state.ManualExpireAt).
		Updates(map[string]any{
			"manual_expire_at": nil,
			"updated_at":       now,
			"update_user_name": "system",
		})
	return result.RowsAffected == 1, result.Error
}

func (s *conversationRouteService) EnterHQAgentDeskServing(conversationID int64, reason string, now time.Time) (*models.ConversationRouteState, error) {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return nil, err
	}
	expireAt := now.Add(DefaultManualTimeoutMinutes * time.Minute)
	if state.LastCustomerMessageAt != nil {
		expireAt = state.LastCustomerMessageAt.Add(DefaultManualTimeoutMinutes * time.Minute)
	}
	if err := repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
		"route_status":             enums.ConversationRouteStatusHQAgentDeskServing,
		"route_target":             "agentdesk_hq",
		"manual_expire_at":         expireAt,
		"pending_action":           "",
		"pending_action_payload":   "",
		"pending_action_expire_at": nil,
		"need_human_follow_up":     false,
		"handoff_reason":           reason,
		"updated_at":               now,
		"update_user_name":         "system",
	}); err != nil {
		return nil, err
	}
	_, _ = ChannelMessageOutboxService.CancelPendingOrdinaryAI(conversationID, 0, "cancelled because conversation entered assigned human service")
	return s.GetByConversationID(conversationID), nil
}

func (s *conversationRouteService) RestoreAI(conversationID int64, reason string, now time.Time) error {
	return s.RestoreAIWithFollowUp(conversationID, reason, now, false)
}

func (s *conversationRouteService) RestoreAIFromTimeoutClaim(state models.ConversationRouteState, reason string, now time.Time, needHumanFollowUp bool) (bool, error) {
	return s.restoreAIFromTimeoutClaimWithDB(sqls.DB(), state, reason, now, needHumanFollowUp)
}

func (s *conversationRouteService) restoreAIFromTimeoutClaimWithDB(db *gorm.DB, state models.ConversationRouteState, reason string, now time.Time, needHumanFollowUp bool) (bool, error) {
	if state.ID <= 0 || state.ManualExpireAt == nil || !routeStatusBlocksAIReply(state.RouteStatus) {
		return false, nil
	}
	result := db.Model(&models.ConversationRouteState{}).
		Where("id = ? AND route_status = ? AND need_human_follow_up = ? AND manual_expire_at = ?", state.ID, state.RouteStatus, state.NeedHumanFollowUp, *state.ManualExpireAt).
		Updates(map[string]any{
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
		})
	return result.RowsAffected == 1, result.Error
}

func (s *conversationRouteService) RestoreAIWithFollowUp(conversationID int64, reason string, now time.Time, needHumanFollowUp bool) error {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	return repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
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
	})
}

func (s *conversationRouteService) MarkStoreSafetyTimeoutReminder(conversationID int64, expireAt time.Time, now time.Time, remark string) error {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	return repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
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
