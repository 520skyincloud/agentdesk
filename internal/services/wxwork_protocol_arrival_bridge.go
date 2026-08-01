package services

import (
	"fmt"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/request"
)

func (s *wxWorkProtocolService) EnsureArrivalConversation(instanceID int64, protocolUserID, displayName, evidence string) (*models.Conversation, string, error) {
	instance := WxWorkProtocolInstanceService.Get(instanceID)
	if !isActivatedCurrentWxWorkProtocolInstance(instance) {
		return nil, "", fmt.Errorf("企微员工号实例不存在或未启用")
	}
	protocolUserID = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(protocolUserID), "S:"))
	if protocolUserID == "" || strings.HasPrefix(protocolUserID, "R:") {
		return nil, "", fmt.Errorf("企微员工号单聊联系人无效")
	}
	msg := request.WxProtocolChatMsg{
		FromUsername: protocolUserID,
		ToUsername:   strings.TrimSpace(instance.EmployeeUserID),
		Sender:       protocolUserID,
		Receiver:     strings.TrimSpace(instance.EmployeeUserID),
		Desc:         strings.TrimSpace(displayName),
		SenderName:   strings.TrimSpace(displayName),
	}
	conversation, _, err := s.ensureConversation(instance, msg, protocolUserID, strings.TrimSpace(evidence))
	if err != nil {
		return nil, "", err
	}
	if err := s.ensureRouteState(conversation.ID, instance); err != nil {
		return nil, "", err
	}
	return conversation, "S:" + protocolUserID, nil
}
