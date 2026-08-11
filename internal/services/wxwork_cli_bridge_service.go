package services

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
)

const wxWorkCLISystemOperatorName = "wxwork_cli"

var WxWorkCLIBridgeService = newWxWorkCLIBridgeService()

func newWxWorkCLIBridgeService() *wxWorkCLIBridgeService {
	return &wxWorkCLIBridgeService{}
}

type wxWorkCLIBridgeService struct{}

func (s *wxWorkCLIBridgeService) ConsumeInbound(req request.WxWorkCLIInboundRequest) (*response.WxWorkCLIInboundResponse, error) {
	channel, cfg, err := s.requireChannel(req.ChannelID, req.BridgeToken)
	if err != nil {
		return nil, err
	}
	chatID := strings.TrimSpace(req.ChatID)
	if chatID == "" {
		return nil, errorsx.InvalidParam("chatId不能为空")
	}
	chatType := s.normalizeChatType(req.ChatType, cfg.DefaultChatType)
	msgType := strings.TrimSpace(req.MsgType)
	if msgType == "" {
		msgType = "text"
	}

	wxMsgID := s.buildInboundMsgID(channel.ChannelID, chatType, chatID, req)
	if ref := WxWorkKFMessageRefService.GetByWxMsgIDInTenant(wxMsgID, channel.TenantID); ref != nil {
		return &response.WxWorkCLIInboundResponse{
			ConversationID: ref.ConversationID,
			MessageID:      ref.MessageID,
		}, nil
	}

	conversation, err := s.ensureConversation(channel, chatType, chatID, req)
	if err != nil {
		return nil, err
	}
	content := s.buildInboundContent(msgType, req.Content)
	rawPayload := s.rawPayload(req)
	message, err := MessageService.SendCustomerMessage(
		conversation.ID,
		"wxwork_cli:"+wxMsgID,
		enums.IMMessageTypeText,
		content,
		rawPayload,
		s.buildExternalUser(channel.ChannelID, chatType, chatID, req),
	)
	if err != nil {
		return nil, err
	}
	if err := s.createMessageRef(conversation.ID, message.ID, channel, chatType, chatID, wxMsgID, rawPayload, enums.WxWorkKFMessageDirectionIn, enums.WxWorkKFMessageSendStatusReceived); err != nil {
		return nil, err
	}
	return &response.WxWorkCLIInboundResponse{
		ConversationID: conversation.ID,
		MessageID:      message.ID,
	}, nil
}

func (s *wxWorkCLIBridgeService) PollOutbox(req request.WxWorkCLIOutboxPollRequest) (*response.WxWorkCLIOutboxPollResponse, error) {
	channel, _, err := s.requireChannel(req.ChannelID, req.BridgeToken)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	items := ChannelMessageOutboxService.ListPendingInTenant(enums.ChannelTypeWxWorkCLI, channel.TenantID, limit)
	results := make([]response.WxWorkCLIOutboxItemResponse, 0, len(items))
	now := time.Now()
	for i := range items {
		if items[i].NextRetryAt != nil && items[i].NextRetryAt.After(now) {
			continue
		}
		message := repositories.MessageRepository.GetInTenant(sqls.DB(), items[i].MessageID, channel.TenantID)
		if message == nil || message.ConversationID != items[i].ConversationID {
			_ = s.MarkOutboxFailed(request.WxWorkCLIOutboxFailedRequest{
				ChannelID:   channel.ChannelID,
				BridgeToken: req.BridgeToken,
				OutboxID:    items[i].ID,
				Error:       "平台消息不存在",
			})
			continue
		}
		mapping := WxWorkKFConversationService.GetByConversationIDInTenant(items[i].ConversationID, channel.TenantID)
		if mapping == nil || mapping.ChannelID != channel.ID {
			continue
		}
		conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), items[i].ConversationID, channel.TenantID)
		if conversation == nil || conversation.ChannelID != channel.ID {
			continue
		}
		chatID := strings.TrimSpace(mapping.ExternalUserID)
		if chatID == "" {
			continue
		}
		claimed, err := ChannelMessageOutboxService.TryMarkSending(items[i].ID, channel.TenantID)
		if err != nil {
			return nil, err
		}
		if !claimed {
			continue
		}
		results = append(results, response.WxWorkCLIOutboxItemResponse{
			OutboxID:       items[i].ID,
			ConversationID: items[i].ConversationID,
			MessageID:      items[i].MessageID,
			ChatID:         chatID,
			ChatName:       strings.TrimSpace(conversation.CustomerName),
			ChatType:       s.parseMappingChatType(mapping.OpenKfID),
			Content:        strings.TrimSpace(message.Content),
		})
	}
	return &response.WxWorkCLIOutboxPollResponse{Items: results}, nil
}

func (s *wxWorkCLIBridgeService) MarkOutboxSent(req request.WxWorkCLIOutboxSentRequest) error {
	channel, _, err := s.requireChannel(req.ChannelID, req.BridgeToken)
	if err != nil {
		return err
	}
	outbox := ChannelMessageOutboxService.GetInTenant(req.OutboxID, channel.TenantID)
	if outbox == nil || outbox.ChannelType != enums.ChannelTypeWxWorkCLI {
		return errorsx.InvalidParam("投递任务不存在")
	}
	conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), outbox.ConversationID, channel.TenantID)
	if conversation == nil || conversation.ChannelID != channel.ID {
		return errorsx.InvalidParam("投递任务不属于当前渠道")
	}
	mapping := WxWorkKFConversationService.GetByConversationIDInTenant(outbox.ConversationID, channel.TenantID)
	if mapping == nil || mapping.ChannelID != channel.ID {
		return errorsx.InvalidParam("企业微信CLI会话映射不存在")
	}
	message := repositories.MessageRepository.GetInTenant(sqls.DB(), outbox.MessageID, channel.TenantID)
	if message == nil || message.ConversationID != conversation.ID {
		return errorsx.InvalidParam("平台消息不存在")
	}

	now := time.Now()
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := AIReplyTurnService.MarkDeliveredDB(ctx.Tx, message, now); err != nil {
			return err
		}
		if err := repositories.ChannelMessageOutboxRepository.UpdatesInTenant(ctx.Tx, outbox.ID, channel.TenantID, map[string]any{
			"send_status": string(enums.ChannelMessageOutboxStatusSent),
			"sent_at":     now,
			"last_error":  "",
			"updated_at":  now,
		}); err != nil {
			return err
		}
		if err := AIReplyTurnActionService.MarkDeliveryByOutboxDB(ctx.Tx, channel.TenantID, outbox.ID, true, "", now); err != nil {
			return err
		}
		wxMsgID := strings.TrimSpace(req.ExternalMsgID)
		if wxMsgID == "" {
			wxMsgID = fmt.Sprintf("wxwork_cli_out:%d", outbox.ID)
		}
		wxMsgID = s.normalizeWxMsgID("wxcli_out", wxMsgID)
		if existing := repositories.WxWorkKFMessageRefRepository.FindOne(ctx.Tx, sqls.NewCnd().Eq("tenant_id", channel.TenantID).Eq("wx_msg_id", wxMsgID)); existing == nil {
			return repositories.WxWorkKFMessageRefRepository.Create(ctx.Tx, &models.WxWorkKFMessageRef{
				TenantID:       channel.TenantID,
				ConversationID: conversation.ID,
				MessageID:      message.ID,
				WxMsgID:        wxMsgID,
				Direction:      string(enums.WxWorkKFMessageDirectionOut),
				Origin:         0,
				OpenKfID:       strings.TrimSpace(mapping.OpenKfID),
				ExternalUserID: strings.TrimSpace(mapping.ExternalUserID),
				SendStatus:     string(enums.WxWorkKFMessageSendStatusSent),
				RawPayload:     strings.TrimSpace(req.ExternalResult),
				Status:         enums.StatusOk,
				AuditFields: models.AuditFields{
					CreatedAt:      now,
					CreateUserID:   0,
					CreateUserName: wxWorkCLISystemOperatorName,
					UpdatedAt:      now,
					UpdateUserID:   0,
					UpdateUserName: wxWorkCLISystemOperatorName,
				},
			})
		}
		return nil
	})
}

func (s *wxWorkCLIBridgeService) MarkOutboxFailed(req request.WxWorkCLIOutboxFailedRequest) error {
	channel, _, err := s.requireChannel(req.ChannelID, req.BridgeToken)
	if err != nil {
		return err
	}
	outbox := ChannelMessageOutboxService.GetInTenant(req.OutboxID, channel.TenantID)
	if outbox == nil || outbox.ChannelType != enums.ChannelTypeWxWorkCLI {
		return errorsx.InvalidParam("投递任务不存在")
	}
	conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), outbox.ConversationID, channel.TenantID)
	if conversation == nil || conversation.ChannelID != channel.ID {
		return errorsx.InvalidParam("投递任务不属于当前渠道")
	}
	now := time.Now()
	retryCount := outbox.RetryCount + 1
	nextRetryAt := now.Add(time.Minute)
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.ChannelMessageOutboxRepository.UpdatesInTenant(ctx.Tx, outbox.ID, channel.TenantID, map[string]any{
			"send_status":   string(enums.ChannelMessageOutboxStatusFailed),
			"retry_count":   retryCount,
			"next_retry_at": nextRetryAt,
			"last_error":    strings.TrimSpace(req.Error),
			"updated_at":    now,
		}); err != nil {
			return err
		}
		return AIReplyTurnActionService.MarkDeliveryByOutboxDB(ctx.Tx, channel.TenantID, outbox.ID, false, "delivery_failed", now)
	})
}

func (s *wxWorkCLIBridgeService) requireChannel(channelID, bridgeToken string) (*models.Channel, *dto.WxWorkCLIChannelConfig, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, nil, errorsx.InvalidParam("channelId不能为空")
	}
	channel := repositories.ChannelRepository.GetByChannelID(sqls.DB(), channelID)
	if channel == nil || channel.Status != enums.StatusOk || channel.ChannelType != enums.ChannelTypeWxWorkCLI {
		return nil, nil, errorsx.InvalidParam("企业微信CLI渠道不存在或未启用")
	}
	if channel.TenantID <= 0 {
		return nil, nil, errorsx.InvalidParam("企业微信CLI渠道缺少接入公司归属")
	}
	cfg, err := ChannelService.ParseWxWorkCLIChannelConfig(channel.ConfigJSON)
	if err != nil {
		return nil, nil, errorsx.InvalidParam("企业微信CLI渠道配置不合法")
	}
	if strings.TrimSpace(cfg.BridgeToken) == "" || strings.TrimSpace(bridgeToken) != strings.TrimSpace(cfg.BridgeToken) {
		return nil, nil, errorsx.Unauthorized("企业微信CLI桥接Token不正确")
	}
	return channel, cfg, nil
}

func (s *wxWorkCLIBridgeService) ensureConversation(channel *models.Channel, chatType int, chatID string, req request.WxWorkCLIInboundRequest) (*models.Conversation, error) {
	if mapping := WxWorkKFConversationService.FindOne(sqls.NewCnd().Eq("tenant_id", channel.TenantID).Eq("channel_id", channel.ID).Eq("external_user_id", chatID).Eq("status", enums.StatusOk)); mapping != nil {
		if conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), mapping.ConversationID, channel.TenantID); conversation != nil && conversation.Status != enums.IMConversationStatusClosed {
			return conversation, nil
		}
	}
	external := s.buildExternalUser(channel.ChannelID, chatType, chatID, req)
	conversation, err := ConversationService.Create(external, channel.ID, channel.AIAgentID)
	if err != nil {
		return nil, err
	}
	if err := s.upsertConversationMapping(conversation.ID, channel, chatType, chatID, req); err != nil {
		return nil, err
	}
	return conversation, nil
}

func (s *wxWorkCLIBridgeService) upsertConversationMapping(conversationID int64, channel *models.Channel, chatType int, chatID string, req request.WxWorkCLIInboundRequest) error {
	if channel == nil || channel.TenantID <= 0 {
		return errorsx.InvalidParam("企业微信CLI渠道缺少接入公司归属")
	}
	conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), conversationID, channel.TenantID)
	if conversation == nil || conversation.ChannelID != channel.ID {
		return errorsx.InvalidParam("企业微信CLI会话不属于当前渠道")
	}
	now := time.Now()
	openKfID := s.mappingOpenKfID(chatType)
	rawProfile := s.rawPayload(req)
	if existing := WxWorkKFConversationService.FindOne(sqls.NewCnd().Eq("tenant_id", channel.TenantID).Eq("channel_id", channel.ID).Eq("external_user_id", chatID)); existing != nil {
		return WxWorkKFConversationService.UpdatesInTenant(existing.ID, channel.TenantID, map[string]any{
			"conversation_id":  conversationID,
			"open_kf_id":       openKfID,
			"external_user_id": strings.TrimSpace(chatID),
			"session_status":   string(enums.WxWorkKFSessionStatusActive),
			"raw_profile":      rawProfile,
			"status":           enums.StatusOk,
			"updated_at":       now,
		})
	}
	return WxWorkKFConversationService.Create(&models.WxWorkKFConversation{
		ConversationID: conversationID,
		ChannelID:      channel.ID,
		OpenKfID:       openKfID,
		ExternalUserID: strings.TrimSpace(chatID),
		SessionStatus:  string(enums.WxWorkKFSessionStatusActive),
		RawProfile:     rawProfile,
		Status:         enums.StatusOk,
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   0,
			CreateUserName: wxWorkCLISystemOperatorName,
			UpdatedAt:      now,
			UpdateUserID:   0,
			UpdateUserName: wxWorkCLISystemOperatorName,
		},
	})
}

func (s *wxWorkCLIBridgeService) createMessageRef(conversationID, messageID int64, channel *models.Channel, chatType int, chatID, wxMsgID, rawPayload string, direction enums.WxWorkKFMessageDirection, sendStatus enums.WxWorkKFMessageSendStatus) error {
	if channel == nil || channel.TenantID <= 0 {
		return errorsx.InvalidParam("企业微信CLI消息映射缺少接入公司归属")
	}
	if WxWorkKFMessageRefService.GetByWxMsgIDInTenant(wxMsgID, channel.TenantID) != nil {
		return nil
	}
	now := time.Now()
	return WxWorkKFMessageRefService.Create(&models.WxWorkKFMessageRef{
		ConversationID: conversationID,
		MessageID:      messageID,
		WxMsgID:        strings.TrimSpace(wxMsgID),
		Direction:      string(direction),
		Origin:         0,
		OpenKfID:       s.mappingOpenKfID(chatType),
		ExternalUserID: strings.TrimSpace(chatID),
		SendStatus:     string(sendStatus),
		RawPayload:     strings.TrimSpace(rawPayload),
		Status:         enums.StatusOk,
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   0,
			CreateUserName: wxWorkCLISystemOperatorName + ":" + strings.TrimSpace(channel.ChannelID),
			UpdatedAt:      now,
			UpdateUserID:   0,
			UpdateUserName: wxWorkCLISystemOperatorName + ":" + strings.TrimSpace(channel.ChannelID),
		},
	})
}

func (s *wxWorkCLIBridgeService) buildExternalUser(channelID string, chatType int, chatID string, req request.WxWorkCLIInboundRequest) openidentity.ExternalUser {
	name := strings.TrimSpace(req.ChatName)
	if name == "" {
		name = strings.TrimSpace(req.SenderName)
	}
	if name == "" {
		name = strings.TrimSpace(req.SenderUserID)
	}
	if name == "" {
		name = chatID
	}
	return openidentity.ExternalUser{
		ExternalSource: enums.ExternalSourceWxWorkCLI,
		ExternalID:     fmt.Sprintf("%s:%d:%s", strings.TrimSpace(channelID), chatType, strings.TrimSpace(chatID)),
		ExternalName:   name,
	}
}

func (s *wxWorkCLIBridgeService) buildInboundMsgID(channelID string, chatType int, chatID string, req request.WxWorkCLIInboundRequest) string {
	if msgID := strings.TrimSpace(req.MsgID); msgID != "" {
		raw := strings.Join([]string{
			strings.TrimSpace(channelID),
			fmt.Sprint(chatType),
			strings.TrimSpace(chatID),
			msgID,
		}, "|")
		sum := sha1.Sum([]byte(raw))
		return "wxcli_in:" + hex.EncodeToString(sum[:])
	}
	raw := strings.Join([]string{
		strings.TrimSpace(channelID),
		fmt.Sprint(chatType),
		strings.TrimSpace(chatID),
		strings.TrimSpace(req.SenderUserID),
		strings.TrimSpace(req.SendTime),
		strings.TrimSpace(req.MsgType),
		strings.TrimSpace(req.Content),
	}, "|")
	sum := sha1.Sum([]byte(raw))
	return "wxcli_in:" + hex.EncodeToString(sum[:])
}

func (s *wxWorkCLIBridgeService) normalizeWxMsgID(prefix string, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) <= 64 {
		return raw
	}
	sum := sha1.Sum([]byte(raw))
	return strings.TrimSpace(prefix) + ":" + hex.EncodeToString(sum[:])
}

func (s *wxWorkCLIBridgeService) buildInboundContent(msgType, content string) string {
	content = strings.TrimSpace(content)
	if strings.TrimSpace(msgType) == "text" {
		return content
	}
	if content != "" {
		return content
	}
	switch strings.TrimSpace(msgType) {
	case "image":
		return "[图片]"
	case "file":
		return "[文件]"
	case "voice":
		return "[语音]"
	case "video":
		return "[视频]"
	default:
		if strs.IsBlank(msgType) {
			return "[消息]"
		}
		return "[" + strings.TrimSpace(msgType) + "]"
	}
}

func (s *wxWorkCLIBridgeService) rawPayload(req any) string {
	if inbound, ok := req.(request.WxWorkCLIInboundRequest); ok {
		inbound.BridgeToken = ""
		req = inbound
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return ""
	}
	return string(raw)
}

func (s *wxWorkCLIBridgeService) normalizeChatType(input, fallback int) int {
	if input == 1 || input == 2 {
		return input
	}
	if fallback == 1 || fallback == 2 {
		return fallback
	}
	return 1
}

func (s *wxWorkCLIBridgeService) mappingOpenKfID(chatType int) string {
	if chatType == 2 {
		return "wxwork_cli:group"
	}
	return "wxwork_cli:single"
}

func (s *wxWorkCLIBridgeService) parseMappingChatType(openKfID string) int {
	if strings.TrimSpace(openKfID) == "wxwork_cli:group" {
		return 2
	}
	return 1
}
