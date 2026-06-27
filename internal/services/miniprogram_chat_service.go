package services

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
)

const (
	miniprogramDefaultAgentName = "企微测试AI店长"
	miniprogramAnswerPending    = "pending"
	miniprogramAnswerAnswered   = "answered"
	miniprogramAnswerHandoff    = "handoff"
	miniprogramAnswerError      = "error"
)

var MiniprogramChatService = newMiniprogramChatService()

func newMiniprogramChatService() *miniprogramChatService {
	return &miniprogramChatService{}
}

type miniprogramChatService struct{}

func (s *miniprogramChatService) StartSession(req request.MiniprogramSessionStartRequest) (*response.MiniprogramSessionStartResponse, error) {
	conversation, _, err := s.ensureConversation(req.MiniprogramChatContextRequest)
	if err != nil {
		return nil, err
	}
	messages := s.buildMessages(s.findLatestMessages(conversation.ID, 50))
	return &response.MiniprogramSessionStartResponse{
		SessionID:      s.sessionID(conversation.ID),
		ConversationID: conversation.ID,
		WelcomeMessage: s.buildWelcomeMessage(req.MiniprogramChatContextRequest),
		Messages:       messages,
	}, nil
}

func (s *miniprogramChatService) SendMessage(req request.MiniprogramMessageSendRequest) (*response.MiniprogramMessageSendResponse, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, errorsx.InvalidParam("消息内容不能为空")
	}

	conversation, external, err := s.ensureConversation(req.MiniprogramChatContextRequest)
	if err != nil {
		return nil, err
	}
	userMessage, err := MessageService.SendCustomerMessage(conversation.ID, s.clientMsgID("mp_user"), enums.IMMessageTypeText, content, "", *external)
	if err != nil {
		return nil, err
	}

	aiMessage := s.waitAIMessage(conversation.ID, userMessage.ID, 25*time.Second)
	answerStatus := miniprogramAnswerAnswered
	if aiMessage == nil {
		answerStatus = miniprogramAnswerPending
	}
	updatedConversation := ConversationService.Get(conversation.ID)
	needHumanSupport := s.needHumanSupport(content, updatedConversation)
	if needHumanSupport && aiMessage != nil {
		answerStatus = miniprogramAnswerHandoff
	}
	if updatedConversation == nil {
		answerStatus = miniprogramAnswerError
	}

	resp := &response.MiniprogramMessageSendResponse{
		SessionID:        s.sessionID(conversation.ID),
		ConversationID:   conversation.ID,
		UserMessage:      s.buildMessagePtr(userMessage),
		AIMessage:        s.buildMessagePtr(aiMessage),
		CreatedAt:        utils.FormatTime(time.Now()),
		AnswerStatus:     answerStatus,
		NeedHumanSupport: needHumanSupport,
	}
	if aiMessage == nil {
		resp.Messages = s.buildMessages(s.findLatestMessages(conversation.ID, 20))
	}
	return resp, nil
}

func (s *miniprogramChatService) ListMessages(req request.MiniprogramChatContextRequest, limit int) (*response.MiniprogramMessageListResponse, error) {
	conversation, _, err := s.ensureConversation(req)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return &response.MiniprogramMessageListResponse{
		SessionID:      s.sessionID(conversation.ID),
		ConversationID: conversation.ID,
		Messages:       s.buildMessages(s.findLatestMessages(conversation.ID, limit)),
	}, nil
}

func (s *miniprogramChatService) ensureConversation(req request.MiniprogramChatContextRequest) (*models.Conversation, *openidentity.ExternalUser, error) {
	if req.ConversationID <= 0 {
		req.ConversationID = s.parseSessionID(req.SessionID)
	}
	if req.ConversationID > 0 {
		conversation := ConversationService.Get(req.ConversationID)
		if conversation == nil {
			return nil, nil, errorsx.InvalidParam("会话不存在")
		}
		external := s.externalFromConversation(conversation, req)
		if external == nil {
			return nil, nil, errorsx.InvalidParam("会话外部身份不存在")
		}
		return conversation, external, nil
	}

	channel, aiAgent := s.resolveEntry(req)
	if aiAgent == nil || aiAgent.Status != enums.StatusOk {
		return nil, nil, errorsx.InvalidParam("AI Agent 不存在或未启用")
	}
	channelID := int64(0)
	if channel != nil {
		channelID = channel.ID
	}
	external := s.buildExternalUser(req)
	conversation, err := ConversationService.Create(*external, channelID, aiAgent.ID)
	if err != nil {
		return nil, nil, err
	}
	return conversation, external, nil
}

func (s *miniprogramChatService) resolveEntry(req request.MiniprogramChatContextRequest) (*models.Channel, *models.AIAgent) {
	if channelID := strings.TrimSpace(req.ChannelID); channelID != "" {
		if channel := repositories.ChannelRepository.GetByChannelID(sqls.DB(), channelID); channel != nil && channel.Status == enums.StatusOk {
			return channel, AIAgentService.Get(channel.AIAgentID)
		}
	}

	channels := ChannelService.Find(sqls.NewCnd().Eq("status", enums.StatusOk).Asc("id"))
	for i := range channels {
		agent := AIAgentService.Get(channels[i].AIAgentID)
		if agent != nil && agent.Status == enums.StatusOk && strings.TrimSpace(agent.Name) == miniprogramDefaultAgentName {
			return &channels[i], agent
		}
	}
	for i := range channels {
		if channels[i].ChannelType == enums.ChannelTypeWechatMP {
			if agent := AIAgentService.Get(channels[i].AIAgentID); agent != nil && agent.Status == enums.StatusOk {
				return &channels[i], agent
			}
		}
	}
	for i := range channels {
		if agent := AIAgentService.Get(channels[i].AIAgentID); agent != nil && agent.Status == enums.StatusOk {
			return &channels[i], agent
		}
	}
	return nil, s.resolveAIAgent()
}

func (s *miniprogramChatService) resolveAIAgent() *models.AIAgent {
	if agent := AIAgentService.Take("name = ? AND status = ?", miniprogramDefaultAgentName, enums.StatusOk); agent != nil {
		return agent
	}
	return AIAgentService.FindOne(sqls.NewCnd().Eq("status", enums.StatusOk).Like("name", "%AI店长%").Asc("id"))
}

func (s *miniprogramChatService) buildExternalUser(req request.MiniprogramChatContextRequest) *openidentity.ExternalUser {
	name := strings.TrimSpace(req.HotelName)
	if name == "" {
		name = "小程序访客"
	}
	return &openidentity.ExternalUser{
		ExternalSource: enums.ExternalSourceGuest,
		ExternalID:     s.buildExternalID(req),
		ExternalName:   name,
	}
}

func (s *miniprogramChatService) externalFromConversation(conversation *models.Conversation, req request.MiniprogramChatContextRequest) *openidentity.ExternalUser {
	identity := ConversationService.GetConversationExternalIdentity(conversation)
	if identity == nil {
		return s.buildExternalUser(req)
	}
	name := strings.TrimSpace(req.HotelName)
	if name == "" {
		name = strings.TrimSpace(conversation.CustomerName)
	}
	if name == "" {
		name = "小程序访客"
	}
	return &openidentity.ExternalUser{
		ExternalSource: identity.ExternalSource,
		ExternalID:     identity.ExternalID,
		ExternalName:   name,
	}
}

func (s *miniprogramChatService) buildExternalID(req request.MiniprogramChatContextRequest) string {
	parts := []string{
		"miniprogram",
		strings.TrimSpace(req.BrandCode),
		strings.TrimSpace(req.StoreID),
		strings.TrimSpace(req.OrderNo),
		strings.TrimSpace(req.Source),
	}
	if strings.TrimSpace(req.OrderNo) == "" && strings.TrimSpace(req.StoreID) == "" && strings.TrimSpace(req.HotelName) != "" {
		parts = append(parts, strings.TrimSpace(req.HotelName))
	}
	raw := strings.Join(parts, "|")
	if strings.ReplaceAll(raw, "|", "") == "miniprogram" {
		if sessionID := strings.TrimSpace(req.SessionID); sessionID != "" {
			raw = "miniprogram|session|" + sessionID
		} else {
			raw = "miniprogram|guest|" + strs.UUID()
		}
	}
	sum := sha1.Sum([]byte(raw))
	return "mp_" + hex.EncodeToString(sum[:])
}

func (s *miniprogramChatService) buildWelcomeMessage(req request.MiniprogramChatContextRequest) string {
	if strings.TrimSpace(req.StoreID) != "" || strings.TrimSpace(req.OrderNo) != "" || strings.TrimSpace(req.HotelName) != "" {
		return "您好，我是您的 AI店长，已为您识别到当前门店，可咨询入住、早餐、停车、发票、续住等问题。"
	}
	return "您好，我是 AI店长。请告诉我您入住的酒店或订单信息，我会帮您查询对应服务。"
}

func (s *miniprogramChatService) waitAIMessage(conversationID, afterMessageID int64, timeout time.Duration) *models.Message {
	deadline := time.Now().Add(timeout)
	for {
		message := MessageService.FindOne(sqls.NewCnd().
			Eq("conversation_id", conversationID).
			Eq("sender_type", enums.IMSenderTypeAI).
			Gt("id", afterMessageID).
			Asc("id"))
		if message != nil {
			return message
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (s *miniprogramChatService) findLatestMessages(conversationID int64, limit int) []models.Message {
	list := MessageService.Find(sqls.NewCnd().Eq("conversation_id", conversationID).Desc("id").Limit(limit))
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list
}

func (s *miniprogramChatService) buildMessages(list []models.Message) []response.MiniprogramChatMessageResponse {
	ret := make([]response.MiniprogramChatMessageResponse, 0, len(list))
	for i := range list {
		ret = append(ret, s.buildMessage(&list[i]))
	}
	return ret
}

func (s *miniprogramChatService) buildMessagePtr(item *models.Message) *response.MiniprogramChatMessageResponse {
	if item == nil {
		return nil
	}
	ret := s.buildMessage(item)
	return &ret
}

func (s *miniprogramChatService) buildMessage(item *models.Message) response.MiniprogramChatMessageResponse {
	if item == nil {
		return response.MiniprogramChatMessageResponse{}
	}
	role := "ai"
	if item.SenderType == enums.IMSenderTypeCustomer {
		role = "user"
	}
	return response.MiniprogramChatMessageResponse{
		ID:             item.ID,
		ConversationID: item.ConversationID,
		Role:           role,
		SenderType:     string(item.SenderType),
		MessageType:    string(item.MessageType),
		Content:        strings.TrimSpace(item.Content),
		CreatedAt:      utils.FormatTimePtr(item.SentAt),
	}
}

func (s *miniprogramChatService) needHumanSupport(content string, conversation *models.Conversation) bool {
	if strings.Contains(content, "转人工") || strings.Contains(content, "人工") {
		return true
	}
	return conversation != nil && conversation.HandoffAt != nil
}

func (s *miniprogramChatService) parseSessionID(sessionID string) int64 {
	var id int64
	_, _ = fmt.Sscanf(strings.TrimSpace(sessionID), "%d", &id)
	return id
}

func (s *miniprogramChatService) sessionID(conversationID int64) string {
	if conversationID <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", conversationID)
}

func (s *miniprogramChatService) clientMsgID(prefix string) string {
	return strings.TrimSpace(prefix) + "_" + strs.UUID()
}
