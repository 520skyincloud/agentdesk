package services

import (
	"log/slog"
	"time"

	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const manualTimeoutNotice = "本次人工服务已暂时结束，后续我会继续为您服务。"

var ManualSessionTimeoutService = newManualSessionTimeoutService()

func newManualSessionTimeoutService() *manualSessionTimeoutService {
	return &manualSessionTimeoutService{}
}

type manualSessionTimeoutService struct{}

func (s *manualSessionTimeoutService) ScanAndRestoreExpired(limit int) int {
	now := time.Now()
	states := ConversationRouteService.ListExpiredHQQiyuServing(now, limit)
	states = append(states, ConversationRouteService.ListExpiredHQAgentDeskServing(now, limit)...)
	count := 0
	for _, state := range states {
		if err := s.restoreOne(state.ConversationID, now); err != nil {
			slog.Warn("manual session timeout restore failed", "conversation_id", state.ConversationID, "error", err)
			continue
		}
		count++
	}
	return count
}

func (s *manualSessionTimeoutService) restoreOne(conversationID int64, now time.Time) error {
	conversation := ConversationService.Get(conversationID)
	if conversation != nil {
		if _, err := MessageService.SendAIServiceNoticeWithRequestID(conversationID, conversation.AIAgentID, manualTimeoutNotice, "manual_timeout"); err != nil {
			return err
		}
	}
	if qiyu := repositories.QiyuConversationRepository.Take(sqls.DB(), "conversation_id = ?", conversationID); qiyu != nil {
		if err := repositories.QiyuConversationRepository.Updates(sqls.DB(), qiyu.ID, map[string]any{
			"status":           "local_expired",
			"close_reason":     "10分钟无客户新消息，本地恢复AI",
			"manual_expire_at": nil,
			"updated_at":       now,
			"update_user_name": "system",
		}); err != nil {
			return err
		}
	}
	if err := ConversationRouteService.RestoreAI(conversationID, "10分钟无客户新消息，本地恢复AI", now); err != nil {
		return err
	}
	_, _ = KnowledgeCandidateService.ExtractFromResolvedConversation(conversationID, enums.KnowledgeCandidateSourceAgentDeskHQ)
	_ = MessageSyncLogService.Create(conversationID, 0, enums.MessageSyncDirectionAgentDeskToWecom, "agentdesk", "store_wecom", "", enums.MessageSyncStatusSuccess, manualTimeoutNotice, "")
	return nil
}
