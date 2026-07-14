package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"gorm.io/gorm"
)

func requireConversationParent(db *gorm.DB, conversationID int64) (*models.Conversation, error) {
	conversation := repositories.ConversationRepository.Get(db, conversationID)
	if conversation == nil {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	if conversation.TenantID <= 0 {
		return nil, errorsx.InvalidParam("会话缺少接入公司归属")
	}
	return conversation, nil
}

func requireOperatorConversation(db *gorm.DB, conversationID int64, operator *dto.AuthPrincipal) (*models.Conversation, error) {
	if operator == nil || operator.UserID <= 0 {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理会话的接入公司")
	}
	conversation := repositories.ConversationRepository.GetInTenant(db, conversationID, tenantID)
	if conversation == nil {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	return conversation, nil
}
