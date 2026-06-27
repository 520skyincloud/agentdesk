package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
)

type WxWorkProtocolAdapter interface {
	SendMessage(cfg *dto.WxWorkProtocolChannelConfig, instance *models.WxWorkProtocolInstance, conversationID string, message *models.Message) (string, error)
	CallDocumented(cfg *dto.WxWorkProtocolChannelConfig, path string, body map[string]any) (string, error)
}

type defaultWxWorkProtocolAdapter struct {
	service *wxWorkProtocolService
}

func newDefaultWxWorkProtocolAdapter(service *wxWorkProtocolService) WxWorkProtocolAdapter {
	return &defaultWxWorkProtocolAdapter{service: service}
}

func (a *defaultWxWorkProtocolAdapter) SendMessage(cfg *dto.WxWorkProtocolChannelConfig, instance *models.WxWorkProtocolInstance, conversationID string, message *models.Message) (string, error) {
	return a.service.sendOutboxMessage(cfg, instance, conversationID, message)
}

func (a *defaultWxWorkProtocolAdapter) CallDocumented(cfg *dto.WxWorkProtocolChannelConfig, path string, body map[string]any) (string, error) {
	return a.service.postJSON(cfg, path, body)
}
