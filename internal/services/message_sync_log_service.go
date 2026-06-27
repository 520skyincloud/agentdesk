package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
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
	return repositories.MessageSyncLogRepository.Create(sqls.DB(), &models.MessageSyncLog{
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
