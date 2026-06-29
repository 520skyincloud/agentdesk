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
)

const (
	DefaultManualTimeoutMinutes           = 10
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
		"session_no":         nextSessionNo,
		"session_started_at": now,
		"route_status":       enums.ConversationRouteStatusAIServing,
		"route_target":       "ai",
		"manual_expire_at":   nil,
		"pending_action":           "",
		"pending_action_payload":   "",
		"pending_action_expire_at": nil,
		"handoff_reason":     "",
		"updated_at":         now,
		"update_user_name":   "system",
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
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"last_customer_message_at": at,
		"updated_at":               time.Now(),
		"update_user_name":         "system",
	}
	if state.RouteStatus == enums.ConversationRouteStatusHQQiyuServing ||
		state.RouteStatus == enums.ConversationRouteStatusHQAgentDeskServing {
		updates["manual_expire_at"] = at.Add(DefaultManualTimeoutMinutes * time.Minute)
	}
	return repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, updates)
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
		"updated_at":                time.Now(),
		"update_user_name":          "system",
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
		"updated_at":                time.Now(),
		"update_user_name":          "system",
	})
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
		"route_status":         enums.ConversationRouteStatusHQAgentDeskPending,
		"route_target":         "agentdesk_hq",
		"manual_expire_at":     nil,
		"pending_action":       "",
		"pending_action_payload": "",
		"pending_action_expire_at": nil,
		"need_human_follow_up": true,
		"handoff_reason":       reason,
		"updated_at":           now,
		"update_user_name":     "system",
	}); err != nil {
		return nil, err
	}
	return s.GetByConversationID(conversationID), nil
}

func (s *conversationRouteService) EnterStoreWecomManual(conversationID int64, reason string, now time.Time) (*models.ConversationRouteState, error) {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return nil, err
	}
	if err := repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
		"route_status":         enums.ConversationRouteStatusStoreWecomManual,
		"route_target":         "store_wecom",
		"manual_expire_at":     nil,
		"pending_action":       "",
		"pending_action_payload": "",
		"pending_action_expire_at": nil,
		"need_human_follow_up": true,
		"handoff_reason":       reason,
		"updated_at":           now,
		"update_user_name":     "system",
	}); err != nil {
		return nil, err
	}
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
		"route_status":         enums.ConversationRouteStatusHQAgentDeskServing,
		"route_target":         "agentdesk_hq",
		"manual_expire_at":     expireAt,
		"pending_action":       "",
		"pending_action_payload": "",
		"pending_action_expire_at": nil,
		"need_human_follow_up": false,
		"handoff_reason":       reason,
		"updated_at":           now,
		"update_user_name":     "system",
	}); err != nil {
		return nil, err
	}
	return s.GetByConversationID(conversationID), nil
}

func (s *conversationRouteService) EnterHQQiyuServing(conversationID int64, reason string, now time.Time) (*models.ConversationRouteState, error) {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return nil, err
	}
	expireAt := now.Add(DefaultManualTimeoutMinutes * time.Minute)
	lastCustomerAt := now
	if state.LastCustomerMessageAt != nil {
		lastCustomerAt = *state.LastCustomerMessageAt
		expireAt = lastCustomerAt.Add(DefaultManualTimeoutMinutes * time.Minute)
	}
	if err := repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
		"route_status":         enums.ConversationRouteStatusHQQiyuServing,
		"route_target":         "qiyu_hq",
		"manual_expire_at":     expireAt,
		"pending_action":       "",
		"pending_action_payload": "",
		"pending_action_expire_at": nil,
		"need_human_follow_up": false,
		"handoff_reason":       reason,
		"updated_at":           now,
		"update_user_name":     "system",
	}); err != nil {
		return nil, err
	}
	return s.GetByConversationID(conversationID), nil
}

func (s *conversationRouteService) RestoreAI(conversationID int64, reason string, now time.Time) error {
	state, err := s.Ensure(conversationID)
	if err != nil {
		return err
	}
	return repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
		"route_status":         enums.ConversationRouteStatusAIServing,
		"route_target":         "ai",
		"manual_expire_at":     nil,
		"pending_action":       "",
		"pending_action_payload": "",
		"pending_action_expire_at": nil,
		"need_human_follow_up": false,
		"handoff_reason":       reason,
		"updated_at":           now,
		"update_user_name":     "system",
	})
}

func (s *conversationRouteService) ListExpiredHQQiyuServing(now time.Time, limit int) []models.ConversationRouteState {
	if limit <= 0 {
		limit = 50
	}
	return repositories.ConversationRouteStateRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("route_status", enums.ConversationRouteStatusHQQiyuServing).
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
