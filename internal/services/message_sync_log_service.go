package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var MessageSyncLogService = newMessageSyncLogService()

func newMessageSyncLogService() *messageSyncLogService {
	return &messageSyncLogService{}
}

type messageSyncLogService struct{}

func (s *messageSyncLogService) Create(conversationID, messageID int64, direction enums.MessageSyncDirection, source, target, externalMsgID string, status enums.MessageSyncStatus, payload, errMsg string) error {
	tenantID := int64(0)
	if conversationID > 0 {
		conversation, err := requireConversationParent(sqls.DB(), conversationID)
		if err != nil {
			return err
		}
		tenantID = conversation.TenantID
		if messageID > 0 {
			message := repositories.MessageRepository.GetInTenant(sqls.DB(), messageID, tenantID)
			if message == nil || message.ConversationID != conversationID {
				return errorsx.InvalidParam("同步日志消息不存在或不属于当前会话")
			}
		}
	} else if messageID > 0 {
		return errorsx.InvalidParam("同步日志消息缺少所属会话")
	}
	return s.CreateInTenant(tenantID, conversationID, messageID, direction, source, target, externalMsgID, status, payload, errMsg)
}

func (s *messageSyncLogService) CreateInTenant(tenantID, conversationID, messageID int64, direction enums.MessageSyncDirection, source, target, externalMsgID string, status enums.MessageSyncStatus, payload, errMsg string) error {
	if tenantID < 0 {
		return errorsx.InvalidParam("同步日志接入公司归属不合法")
	}
	if conversationID > 0 {
		conversation, err := requireConversationParent(sqls.DB(), conversationID)
		if err != nil {
			return err
		}
		if tenantID <= 0 {
			tenantID = conversation.TenantID
		}
		if conversation.TenantID != tenantID {
			return errorsx.InvalidParam("同步日志会话不属于指定接入公司")
		}
		if messageID > 0 {
			message := repositories.MessageRepository.GetInTenant(sqls.DB(), messageID, tenantID)
			if message == nil || message.ConversationID != conversationID {
				return errorsx.InvalidParam("同步日志消息不存在或不属于当前会话")
			}
		}
	} else if messageID > 0 {
		return errorsx.InvalidParam("同步日志消息缺少所属会话")
	}
	return repositories.MessageSyncLogRepository.Create(sqls.DB(), &models.MessageSyncLog{
		TenantID:       tenantID,
		ConversationID: conversationID,
		MessageID:      messageID,
		Direction:      direction,
		Source:         source,
		Target:         target,
		ExternalMsgID:  externalMsgID,
		SyncStatus:     status,
		ErrorMessage:   errMsg,
		Payload:        payload,
		AuditFields:    utils.BuildAuditFields(nil),
	})
}
