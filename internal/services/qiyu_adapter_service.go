package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
)

const qiyuSystemOperatorName = "qiyu_hq"

var QiyuAdapterService = newQiyuAdapterService()

func newQiyuAdapterService() *qiyuAdapterService {
	return &qiyuAdapterService{}
}

type qiyuAdapterService struct{}

func (s *qiyuAdapterService) HandleCallback(req request.QiyuCallbackRequest) error {
	eventType := strings.ToUpper(strings.TrimSpace(req.EventType))
	raw := s.rawPayload(req)
	switch eventType {
	case "MSG":
		return s.handleMessage(req, raw)
	case "SESSION_START":
		return s.handleSessionStart(req, raw)
	case "SESSION_END":
		return s.handleSessionEnd(req, raw)
	default:
		return errorsx.InvalidParam("不支持的七鱼回调类型")
	}
}

func (s *qiyuAdapterService) StartHandoff(conversationID int64, reason string) (*models.QiyuConversation, error) {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	now := time.Now()
	state, err := ConversationRouteService.EnterHQQiyuServing(conversationID, reason, now)
	if err != nil {
		return nil, err
	}
	qiyu := repositories.QiyuConversationRepository.Take(sqls.DB(), "conversation_id = ?", conversationID)
	expireAt := state.ManualExpireAt
	if qiyu == nil {
		qiyu = &models.QiyuConversation{
			ConversationID: conversationID,
			QiyuUID:        fmt.Sprintf("conversation_%d", conversationID),
			Status:         "pending",
			ManualExpireAt: expireAt,
			AuditFields:    utils.BuildAuditFields(nil),
		}
		if route := HQQiyuRouteService.GetDefault(); route != nil {
			qiyu.GroupID = route.DefaultGroupID
		}
		if err := repositories.QiyuConversationRepository.Create(sqls.DB(), qiyu); err != nil {
			return nil, err
		}
	} else {
		if err := repositories.QiyuConversationRepository.Updates(sqls.DB(), qiyu.ID, map[string]any{
			"status":           "pending",
			"manual_expire_at": expireAt,
			"updated_at":       now,
			"update_user_name": qiyuSystemOperatorName,
		}); err != nil {
			return nil, err
		}
	}
	if err := MessageSyncLogService.Create(conversationID, 0, enums.MessageSyncDirectionAgentDeskToQiyu, "agentdesk", "qiyu_hq", "", enums.MessageSyncStatusPending, s.buildHandoffContext(conversationID, reason), ""); err != nil {
		return nil, err
	}
	return repositories.QiyuConversationRepository.Take(sqls.DB(), "conversation_id = ?", conversationID), nil
}

func (s *qiyuAdapterService) handleSessionStart(req request.QiyuCallbackRequest, raw string) error {
	if req.ConversationID <= 0 {
		return errorsx.InvalidParam("conversationId不能为空")
	}
	now := time.Now()
	state, err := ConversationRouteService.EnterHQQiyuServing(req.ConversationID, "七鱼人工接入", now)
	if err != nil {
		return err
	}
	qiyu := repositories.QiyuConversationRepository.Take(sqls.DB(), "conversation_id = ?", req.ConversationID)
	updates := map[string]any{
		"qiyu_uid":         strings.TrimSpace(req.QiyuUID),
		"session_id":       strings.TrimSpace(req.SessionID),
		"staff_id":         strings.TrimSpace(req.StaffID),
		"staff_name":       strings.TrimSpace(req.StaffName),
		"status":           "serving",
		"manual_expire_at": state.ManualExpireAt,
		"raw_payload":      raw,
		"updated_at":       now,
		"update_user_name": qiyuSystemOperatorName,
	}
	if qiyu == nil {
		item := &models.QiyuConversation{
			ConversationID: req.ConversationID,
			QiyuUID:        updates["qiyu_uid"].(string),
			SessionID:      updates["session_id"].(string),
			StaffID:        updates["staff_id"].(string),
			StaffName:      updates["staff_name"].(string),
			Status:         "serving",
			ManualExpireAt: state.ManualExpireAt,
			RawPayload:     raw,
			AuditFields:    utils.BuildAuditFields(nil),
		}
		return repositories.QiyuConversationRepository.Create(sqls.DB(), item)
	}
	return repositories.QiyuConversationRepository.Updates(sqls.DB(), qiyu.ID, updates)
}

func (s *qiyuAdapterService) handleSessionEnd(req request.QiyuCallbackRequest, raw string) error {
	if req.ConversationID <= 0 {
		return errorsx.InvalidParam("conversationId不能为空")
	}
	now := time.Now()
	qiyu := repositories.QiyuConversationRepository.Take(sqls.DB(), "conversation_id = ?", req.ConversationID)
	if qiyu != nil {
		if err := repositories.QiyuConversationRepository.Updates(sqls.DB(), qiyu.ID, map[string]any{
			"status":           "ended",
			"close_reason":     strings.TrimSpace(req.CloseReason),
			"ended_at":         now,
			"raw_payload":      raw,
			"updated_at":       now,
			"update_user_name": qiyuSystemOperatorName,
		}); err != nil {
			return err
		}
	}
	if err := ConversationRouteService.RestoreAI(req.ConversationID, "七鱼结束会话:"+strings.TrimSpace(req.CloseReason), now); err != nil {
		return err
	}
	_, _ = KnowledgeCandidateService.ExtractFromResolvedConversation(req.ConversationID, enums.KnowledgeCandidateSourceQiyuHQ)
	return nil
}

func (s *qiyuAdapterService) handleMessage(req request.QiyuCallbackRequest, raw string) error {
	if req.ConversationID <= 0 {
		return errorsx.InvalidParam("conversationId不能为空")
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil
	}
	qiyu := repositories.QiyuConversationRepository.Take(sqls.DB(), "conversation_id = ?", req.ConversationID)
	if qiyu == nil {
		return errorsx.InvalidParam("七鱼会话不存在")
	}
	clientMsgID := "qiyu:" + strings.TrimSpace(req.MsgID)
	if clientMsgID == "qiyu:" {
		clientMsgID = "qiyu:" + strs.UUID()
	}
	state := ConversationRouteService.GetByConversationID(req.ConversationID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusHQQiyuServing {
		message, err := MessageService.CreateExternalAgentMessageWithoutOutbox(req.ConversationID, clientMsgID, content, raw, strings.TrimSpace(req.MsgID))
		if err != nil {
			return err
		}
		_ = MessageSyncLogService.Create(req.ConversationID, message.ID, enums.MessageSyncDirectionQiyuToAgentDesk, "qiyu_hq", "agentdesk", req.MsgID, enums.MessageSyncStatusSkipped, raw, "人工超时后迟到回复，默认不自动转发客户")
		return nil
	}
	message, err := MessageService.CreateExternalAgentMessage(req.ConversationID, clientMsgID, content, raw, strings.TrimSpace(req.MsgID))
	if err != nil {
		return err
	}
	_ = MessageSyncLogService.Create(req.ConversationID, message.ID, enums.MessageSyncDirectionQiyuToAgentDesk, "qiyu_hq", "agentdesk", req.MsgID, enums.MessageSyncStatusSuccess, raw, "")
	return nil
}

func (s *qiyuAdapterService) rawPayload(req request.QiyuCallbackRequest) string {
	if strings.TrimSpace(req.RawPayload) != "" {
		return strings.TrimSpace(req.RawPayload)
	}
	buf, _ := json.Marshal(req)
	return string(buf)
}

func (s *qiyuAdapterService) buildHandoffContext(conversationID int64, reason string) string {
	messages := MessageService.Find(sqls.NewCnd().Eq("conversation_id", conversationID).Desc("seq_no").Limit(10))
	type contextMessage struct {
		SenderType string `json:"senderType"`
		Content    string `json:"content"`
		SentAt     string `json:"sentAt"`
	}
	items := make([]contextMessage, 0, len(messages))
	for _, item := range messages {
		sentAt := ""
		if item.SentAt != nil {
			sentAt = item.SentAt.Format(time.DateTime)
		}
		items = append(items, contextMessage{
			SenderType: string(item.SenderType),
			Content:    item.Content,
			SentAt:     sentAt,
		})
	}
	payload := map[string]any{
		"conversationId": conversationID,
		"reason":         strings.TrimSpace(reason),
		"recentMessages": items,
	}
	buf, _ := json.Marshal(payload)
	return string(buf)
}
