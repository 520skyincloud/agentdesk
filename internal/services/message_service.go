package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"log/slog"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
)

var MessageService = newMessageService()

func newMessageService() *messageService {
	return &messageService{}
}

type messageService struct {
}

type sendMessageOptions struct {
	skipOutbound                bool
	skipOutboundMediaValidation bool
	externalAgentReply          bool
	systemOutbound              bool
	sessionNo                   int
	eventContent                string
}

func (s *messageService) Get(id int64) *models.Message {
	return repositories.MessageRepository.Get(sqls.DB(), id)
}

func (s *messageService) Take(where ...interface{}) *models.Message {
	return repositories.MessageRepository.Take(sqls.DB(), where...)
}

func (s *messageService) Find(cnd *sqls.Cnd) []models.Message {
	return repositories.MessageRepository.Find(sqls.DB(), cnd)
}

// FindByConversationIDCursor 按 id 游标分页：cursor=0 取最新 limit 条；cursor>0 取 id<cursor 的更旧消息。
// 返回的 list 已按 id 升序（时间正序）。nextCursor 为下一页请求传入的游标（本批最小 id）；hasMore 表示可能还有更旧消息。
func (s *messageService) FindByConversationIDCursor(conversationID int64, cursor int64, limit int, senderType, messageType string) (list []models.Message, nextCursor int64, hasMore bool) {
	if limit > 100 {
		limit = 100
	} else if limit <= 0 {
		limit = 20
	}
	conversation, err := requireConversationParent(sqls.DB(), conversationID)
	if err != nil {
		return nil, cursor, false
	}
	cnd := sqls.NewCnd().Eq("tenant_id", conversation.TenantID).Eq("conversation_id", conversationID).Limit(limit).Desc("id")
	if cursor > 0 {
		cnd.Lt("id", cursor)
	}
	if strs.IsNotBlank(senderType) {
		cnd.Eq("sender_type", senderType)
	}
	if strs.IsNotBlank(messageType) {
		cnd.Eq("message_type", messageType)
	}
	list = s.Find(cnd)
	nextCursor = cursor
	hasMore = false
	if len(list) > 0 {
		nextCursor = list[len(list)-1].ID
		hasMore = len(list) == limit
	}
	slices.Reverse(list)
	return list, nextCursor, hasMore
}

func (s *messageService) FindOne(cnd *sqls.Cnd) *models.Message {
	return repositories.MessageRepository.FindOne(sqls.DB(), cnd)
}

func (s *messageService) FindLatestByConversationID(conversationID int64) (*models.Message, error) {
	return s.FindOne(sqls.NewCnd().Eq("conversation_id", conversationID).Desc("seq_no").Desc("id")), nil
}

func (s *messageService) FindLatestByConversationIDInTenant(conversationID, tenantID int64) (*models.Message, error) {
	if conversationID <= 0 || tenantID <= 0 {
		return nil, nil
	}
	return s.FindOne(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("conversation_id", conversationID).Desc("seq_no").Desc("id")), nil
}

func (s *messageService) FindPageByParams(params *params.QueryParams) (list []models.Message, paging *sqls.Paging) {
	return repositories.MessageRepository.FindPageByParams(sqls.DB(), params)
}

func (s *messageService) FindPageByCnd(cnd *sqls.Cnd) (list []models.Message, paging *sqls.Paging) {
	return repositories.MessageRepository.FindPageByCnd(sqls.DB(), cnd)
}

// FindPageByCndForImListAscending 与 FindPageByCnd 相同分页条件，将结果按 seq 升序排列（开放 IM 时间正序展示）。
func (s *messageService) FindPageByCndForImListAscending(cnd *sqls.Cnd) (list []models.Message, paging *sqls.Paging) {
	list, paging = s.FindPageByCnd(cnd)
	if len(list) <= 1 {
		return list, paging
	}
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, paging
}

func (s *messageService) Count(cnd *sqls.Cnd) int64 {
	return repositories.MessageRepository.Count(sqls.DB(), cnd)
}

func (s *messageService) Create(t *models.Message) error {
	return repositories.MessageRepository.Create(sqls.DB(), t)
}

func (s *messageService) Update(t *models.Message) error {
	return repositories.MessageRepository.Update(sqls.DB(), t)
}

func (s *messageService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.MessageRepository.Updates(sqls.DB(), id, columns)
}

func (s *messageService) UpdateColumn(id int64, name string, value interface{}) error {
	return repositories.MessageRepository.UpdateColumn(sqls.DB(), id, name, value)
}

func (s *messageService) Delete(id int64) {
	repositories.MessageRepository.Delete(sqls.DB(), id)
}

func (s *messageService) GetConversationReadTarget(conversationID, messageID int64) (*models.Message, error) {
	conversation, err := requireConversationParent(sqls.DB(), conversationID)
	if err != nil {
		return nil, err
	}
	if messageID > 0 {
		message := repositories.MessageRepository.GetInTenant(sqls.DB(), messageID, conversation.TenantID)
		if message == nil || message.ConversationID != conversationID {
			return nil, errorsx.InvalidParam("消息不存在")
		}
		return message, nil
	}
	return s.FindOne(sqls.NewCnd().Eq("tenant_id", conversation.TenantID).Eq("conversation_id", conversationID).Desc("seq_no").Desc("id")), nil
}

func (s *messageService) SendMessage(conversationID int64, senderType enums.IMSenderType, reqSenderID int64, clientMsgID string, messageType enums.IMMessageType, content, payload string, operator *dto.AuthPrincipal, external *openidentity.ExternalUser) (*models.Message, error) {
	switch senderType {
	case enums.IMSenderTypeAgent:
		return s.sendMessage(conversationID, enums.IMSenderTypeAgent, reqSenderID, clientMsgID, messageType, content, payload, operator, nil, "")
	case enums.IMSenderTypeAI:
		return s.sendMessage(conversationID, enums.IMSenderTypeAI, reqSenderID, clientMsgID, messageType, content, payload, operator, nil, "")
	case enums.IMSenderTypeCustomer:
		return s.sendMessage(conversationID, enums.IMSenderTypeCustomer, 0, clientMsgID, messageType, content, payload, nil, external, "")
	default:
		return nil, errorsx.InvalidParam("不支持的发送人类型")
	}
}

func (s *messageService) SendAgentMessage(conversationID int64, reqSenderID int64, clientMsgID string, messageType enums.IMMessageType, content, payload string, operator *dto.AuthPrincipal) (*models.Message, error) {
	return s.SendAgentMessageWithRequestID(conversationID, reqSenderID, clientMsgID, messageType, content, payload, operator, "")
}

func (s *messageService) SendAgentMessageWithRequestID(conversationID int64, reqSenderID int64, clientMsgID string, messageType enums.IMMessageType, content, payload string, operator *dto.AuthPrincipal, requestID string) (*models.Message, error) {
	return s.sendMessage(conversationID, enums.IMSenderTypeAgent, reqSenderID, clientMsgID, messageType, content, payload, operator, nil, requestID)
}

func (s *messageService) CreateExternalAgentMessageWithoutOutbox(conversationID int64, clientMsgID string, messageType enums.IMMessageType, content, payload string, requestID string) (*models.Message, error) {
	return s.CreateExternalAgentMessageWithoutOutboxInSession(conversationID, clientMsgID, messageType, content, payload, requestID, 0)
}

func (s *messageService) CreateExternalAgentMessageWithoutOutboxInSession(conversationID int64, clientMsgID string, messageType enums.IMMessageType, content, payload string, requestID string, sessionNo int) (*models.Message, error) {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	if conversation.Status == enums.IMConversationStatusClosed {
		return nil, errorsx.InvalidParam("会话已关闭")
	}
	return s.sendValidatedMessageWithOptions(conversation, enums.IMSenderTypeAgent, 0, clientMsgID, messageType, content, payload, &dto.AuthPrincipal{
		UserID:   0,
		Username: wxWorkProtocolSystemOperatorName,
		Nickname: "企微员工号",
	}, nil, requestID, sendMessageOptions{
		skipOutbound:                true,
		skipOutboundMediaValidation: true,
		externalAgentReply:          true,
		sessionNo:                   sessionNo,
		eventContent:                "企微员工号人工回复",
	})
}

func (s *messageService) SendSystemOutboundMessage(conversationID int64, clientMsgID string, messageType enums.IMMessageType, content, payload, requestID string) (*models.Message, error) {
	conversation, err := requireConversationParent(sqls.DB(), conversationID)
	if err != nil {
		return nil, err
	}
	if conversation.Status == enums.IMConversationStatusClosed {
		return nil, errorsx.InvalidParam("会话已关闭")
	}
	return s.sendValidatedMessageWithOptions(
		conversation,
		enums.IMSenderTypeSystem,
		0,
		clientMsgID,
		messageType,
		content,
		payload,
		&dto.AuthPrincipal{Username: "arrival", Nickname: "到店联动"},
		nil,
		requestID,
		sendMessageOptions{
			systemOutbound: true,
			eventContent:   "到店联动发送通知",
		},
	)
}

func (s *messageService) RecallAgentMessage(messageID int64, operator *dto.AuthPrincipal) (*models.Message, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	if messageID <= 0 {
		return nil, errorsx.InvalidParam("消息不存在")
	}

	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	message := repositories.MessageRepository.GetInTenant(sqls.DB(), messageID, tenantID)
	if message == nil {
		return nil, errorsx.InvalidParam("消息不存在")
	}
	if message.SenderType != enums.IMSenderTypeAgent {
		return nil, errorsx.InvalidParam("仅支持撤回客服消息")
	}
	if message.SenderID != operator.UserID {
		return nil, errorsx.Forbidden("仅允许撤回自己发送的消息")
	}
	conversation, err := s.ValidateConversationSender(message.ConversationID, enums.IMSenderTypeAgent, operator, nil)
	if err != nil {
		return nil, err
	}
	if message.RecalledAt != nil || message.SendStatus == enums.IMMessageStatusRecalled {
		return nil, errorsx.InvalidParam("消息已撤回")
	}

	return s.applyMessageRecall(message, conversation, operator.UserID, operator.Username, enums.IMSenderTypeAgent, "客服撤回消息", "")
}

func (s *messageService) ApplyExternalMessageRecall(messageID int64, source string, requestID string) (*models.Message, error) {
	if messageID <= 0 {
		return nil, errorsx.InvalidParam("消息不存在")
	}
	message := s.Get(messageID)
	if message == nil {
		return nil, errorsx.InvalidParam("消息不存在")
	}
	conversation, err := requireConversationParent(sqls.DB(), message.ConversationID)
	if err != nil || message.TenantID != conversation.TenantID {
		return nil, errorsx.InvalidParam("消息或会话不存在")
	}
	if message.RecalledAt != nil || message.SendStatus == enums.IMMessageStatusRecalled {
		return message, nil
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "外部渠道"
	}
	return s.applyMessageRecall(message, conversation, 0, wxWorkProtocolSystemOperatorName, message.SenderType, source+"撤回消息", requestID)
}

func (s *messageService) applyMessageRecall(message *models.Message, conversation *models.Conversation, operatorID int64, operatorName string, operatorType enums.IMSenderType, eventContent string, requestID string) (*models.Message, error) {
	now := time.Now()
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		updates := map[string]any{
			"send_status":      int(enums.IMMessageStatusRecalled),
			"recalled_at":      now,
			"updated_at":       now,
			"update_user_id":   operatorID,
			"update_user_name": operatorName,
		}
		if err := repositories.MessageRepository.UpdatesInTenant(ctx.Tx, message.ID, conversation.TenantID, updates); err != nil {
			return err
		}

		message.SendStatus = enums.IMMessageStatusRecalled
		message.RecalledAt = &now
		message.UpdatedAt = now
		message.UpdateUserID = operatorID
		message.UpdateUserName = operatorName

		agentReadState, customerReadState := ConversationReadStateService.getConversationReadStates(ctx.Tx, conversation.ID)
		agentUnreadCount, err := ConversationReadStateService.CountUnreadMessages(ctx, conversation.ID, s.readSeqNo(agentReadState), enums.IMSenderTypeCustomer)
		if err != nil {
			return err
		}
		customerUnreadCount, err := ConversationReadStateService.CountUnreadMessages(ctx, conversation.ID, s.readSeqNo(customerReadState), enums.IMSenderTypeAgent, enums.IMSenderTypeAI)
		if err != nil {
			return err
		}

		conversationUpdates := map[string]any{
			"agent_unread_count":    agentUnreadCount,
			"customer_unread_count": customerUnreadCount,
			"updated_at":            now,
			"update_user_id":        operatorID,
			"update_user_name":      operatorName,
		}
		if conversation.LastMessageID == message.ID {
			lastMessage := repositories.MessageRepository.FindLastUnrecalledByConversationIDInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
			if lastMessage != nil {
				conversationUpdates["last_message_id"] = lastMessage.ID
				conversationUpdates["last_message_at"] = lastMessage.SentAt
				conversationUpdates["last_message_summary"] = limitText(buildMessageSummary(lastMessage.MessageType, lastMessage.Content), 255)
			} else {
				conversationUpdates["last_message_id"] = 0
				conversationUpdates["last_message_at"] = nil
				conversationUpdates["last_message_summary"] = ""
			}
		}
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, conversation.ID, conversation.TenantID, conversationUpdates); err != nil {
			return err
		}

		if err := ConversationEventLogService.CreateEventWithRequestID(ctx, conversation.ID, requestID, enums.IMEventTypeMessageRecall, operatorType, operatorID, eventContent, ""); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if updatedConversation := ConversationService.Get(conversation.ID); updatedConversation != nil {
		WsService.PublishMessageRecalled(updatedConversation, message)
		WsService.PublishConversationChanged(updatedConversation, enums.IMRealtimeEventConversationUpdated)
	}
	return message, nil
}

func (s *messageService) SendAIMessage(conversationID int64, aiAgentID int64, clientMsgID string, messageType enums.IMMessageType, content, payload string, operator *dto.AuthPrincipal) (*models.Message, error) {
	return s.SendAIMessageWithRequestID(conversationID, aiAgentID, clientMsgID, messageType, content, payload, operator, "")
}

func (s *messageService) SendAIMessageWithRequestID(conversationID int64, aiAgentID int64, clientMsgID string, messageType enums.IMMessageType, content, payload string, operator *dto.AuthPrincipal, requestID string) (*models.Message, error) {
	return s.sendMessage(conversationID, enums.IMSenderTypeAI, aiAgentID, clientMsgID, messageType, content, payload, operator, nil, requestID)
}

func (s *messageService) SendAIServiceNotice(conversationID int64, aiAgentID int64, content string) (*models.Message, error) {
	return s.SendAIServiceNoticeWithRequestID(conversationID, aiAgentID, content, "")
}

func (s *messageService) SendAIServiceNoticeWithRequestID(conversationID int64, aiAgentID int64, content string, requestID string) (*models.Message, error) {
	return s.SendAIServiceNoticeWithPayloadAndRequestID(conversationID, aiAgentID, content, "", requestID)
}

func (s *messageService) SendAIServiceNoticeWithPayloadAndRequestID(conversationID int64, aiAgentID int64, content string, payload string, requestID string) (*models.Message, error) {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	if conversation.Status == enums.IMConversationStatusClosed {
		return nil, errorsx.InvalidParam("会话已关闭")
	}
	return s.sendValidatedMessage(conversation, enums.IMSenderTypeAI, aiAgentID, strs.UUID(), enums.IMMessageTypeText, content, payload, &dto.AuthPrincipal{
		UserID:   0,
		Username: "system",
		Nickname: "system",
	}, nil, requestID)
}

func (s *messageService) createAIWelcomeMessage(ctx *sqls.TxContext, conversation *models.Conversation, aiAgent *models.AIAgent, now time.Time) (*models.Message, error) {
	if ctx == nil || conversation == nil || aiAgent == nil || strings.TrimSpace(aiAgent.WelcomeMessage) == "" {
		return nil, nil
	}

	content, payload, summary, err := s.normalizeMessageContent(conversation.ID, enums.IMMessageTypeText, aiAgent.WelcomeMessage, "")
	if err != nil {
		return nil, err
	}
	if strs.IsBlank(content) && strs.IsBlank(payload) {
		return nil, nil
	}

	operator := &dto.AuthPrincipal{
		UserID:   0,
		Username: "system",
		Nickname: "system",
	}
	message := &models.Message{
		TenantID:       conversation.TenantID,
		ConversationID: conversation.ID,
		ClientMsgID:    strs.UUID(),
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        content,
		Payload:        payload,
		SeqNo:          repositories.MessageRepository.NextSeqNoInTenant(ctx.Tx, conversation.ID, conversation.TenantID),
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   operator.UserID,
			CreateUserName: operator.Username,
			UpdatedAt:      now,
			UpdateUserID:   operator.UserID,
			UpdateUserName: operator.Username,
		},
	}
	ChannelMessageOutboxService.PrepareOutboundMessage(ctx.Tx, conversation, message)
	if err := repositories.MessageRepository.Create(ctx.Tx, message); err != nil {
		return nil, err
	}

	if _, err := ConversationReadStateService.MarkAgentRead(ctx, conversation, operator, message); err != nil {
		return nil, err
	}
	agentReadState, customerReadState := ConversationReadStateService.getConversationReadStates(ctx.Tx, conversation.ID)
	agentUnreadCount, err := ConversationReadStateService.CountUnreadMessages(ctx, conversation.ID, s.readSeqNo(agentReadState), enums.IMSenderTypeCustomer)
	if err != nil {
		return nil, err
	}
	customerUnreadCount, err := ConversationReadStateService.CountUnreadMessages(ctx, conversation.ID, s.readSeqNo(customerReadState), enums.IMSenderTypeAgent, enums.IMSenderTypeAI)
	if err != nil {
		return nil, err
	}

	conversationUpdates := map[string]any{
		"last_message_id":       message.ID,
		"last_message_at":       now,
		"last_active_at":        now,
		"last_message_summary":  limitText(summary, 255),
		"update_user_id":        operator.UserID,
		"update_user_name":      operator.Username,
		"updated_at":            now,
		"agent_unread_count":    agentUnreadCount,
		"customer_unread_count": customerUnreadCount,
	}
	if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, conversation.ID, conversation.TenantID, conversationUpdates); err != nil {
		return nil, err
	}
	if err := ConversationEventLogService.CreateEvent(ctx,
		conversation.ID,
		enums.IMEventTypeMessageSend,
		enums.IMSenderTypeAI,
		0,
		enums.GetIMSenderTypeLabel(enums.IMSenderTypeAI)+"发送消息",
		"",
	); err != nil {
		return nil, err
	}

	conversation.LastMessageID = message.ID
	conversation.LastMessageAt = now
	conversation.LastActiveAt = now
	conversation.LastMessageSummary = limitText(summary, 255)
	conversation.AgentUnreadCount = int(agentUnreadCount)
	conversation.CustomerUnreadCount = int(customerUnreadCount)
	conversation.UpdatedAt = now
	conversation.UpdateUserID = operator.UserID
	conversation.UpdateUserName = operator.Username
	return message, nil
}

func (s *messageService) SendCustomerMessage(conversationID int64, clientMsgID string, messageType enums.IMMessageType, content, payload string, external openidentity.ExternalUser) (*models.Message, error) {
	return s.SendCustomerMessageWithRequestID(conversationID, clientMsgID, messageType, content, payload, external, "")
}

func (s *messageService) SendCustomerMessageInSession(conversationID int64, clientMsgID string, messageType enums.IMMessageType, content, payload string, external openidentity.ExternalUser, sessionNo int) (*models.Message, error) {
	if sessionNo <= 0 {
		return nil, errorsx.InvalidParam("企微入站消息缺少有效会话段")
	}
	if strings.TrimSpace(external.ExternalID) == "" {
		return nil, errorsx.Unauthorized("外部用户标识不能为空")
	}
	if strs.IsBlank(string(messageType)) {
		messageType = enums.IMMessageTypeText
	}
	conversation, err := s.ValidateConversationSender(conversationID, enums.IMSenderTypeCustomer, nil, &external)
	if err != nil {
		return nil, err
	}
	return s.sendValidatedMessageWithOptions(conversation, enums.IMSenderTypeCustomer, 0, clientMsgID, messageType, content, payload, nil, &external, "", sendMessageOptions{sessionNo: sessionNo})
}

func (s *messageService) PersistHistoricalCustomerMessageInSession(
	conversationID int64,
	clientMsgID string,
	messageType enums.IMMessageType,
	content, payload string,
	external openidentity.ExternalUser,
	sessionNo int,
	sentAt time.Time,
) (*models.Message, error) {
	if conversationID <= 0 || sessionNo <= 0 {
		return nil, errorsx.InvalidParam("历史消息缺少有效会话或会话段")
	}
	clientMsgID = strings.TrimSpace(clientMsgID)
	if clientMsgID == "" || strings.TrimSpace(external.ExternalID) == "" {
		return nil, errorsx.InvalidParam("历史消息缺少稳定外部标识")
	}
	if strs.IsBlank(string(messageType)) {
		messageType = enums.IMMessageTypeText
	}
	conversation, err := requireConversationParent(sqls.DB(), conversationID)
	if err != nil {
		return nil, err
	}
	if !ConversationService.IsCustomerConversationOwner(conversation, external) {
		return nil, errorsx.Forbidden("历史消息客户身份与原会话不一致")
	}
	session := repositories.ConversationChannelSessionRepository.TakeByConversationSession(
		sqls.DB(), conversation.TenantID, conversation.ID, sessionNo,
	)
	if session == nil || session.StoreID != conversation.StoreID || session.StoreStaffBindingID != conversation.StoreStaffBindingID {
		return nil, errorsx.InvalidParam("历史消息会话段与原会话范围不一致")
	}
	content, payload, _, err = s.normalizeMessageContent(conversation.ID, messageType, content, payload)
	if err != nil {
		return nil, err
	}
	if strs.IsBlank(content) && strs.IsBlank(payload) {
		return nil, errorsx.InvalidParam("历史消息内容不能为空")
	}
	now := time.Now()
	if sentAt.IsZero() {
		sentAt = now
	}
	var result *models.Message
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedConversation, lockErr := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
		if lockErr != nil {
			return lockErr
		}
		if lockedConversation == nil || lockedConversation.StoreID != conversation.StoreID || lockedConversation.StoreStaffBindingID != conversation.StoreStaffBindingID || lockedConversation.CustomerID != conversation.CustomerID {
			return errorsx.InvalidParam("历史消息原会话范围已变化")
		}
		lockedSession, lockErr := repositories.ConversationChannelSessionRepository.GetForUpdateByConversationSession(ctx.Tx, conversation.TenantID, conversation.ID, sessionNo)
		if lockErr != nil {
			return lockErr
		}
		if lockedSession == nil || lockedSession.StoreID != conversation.StoreID || lockedSession.StoreStaffBindingID != conversation.StoreStaffBindingID {
			return errorsx.InvalidParam("历史消息原会话段已变化")
		}
		if existing := repositories.MessageRepository.GetByClientMsgIDInTenant(ctx.Tx, conversation.ID, conversation.TenantID, clientMsgID); existing != nil {
			result = existing
			return nil
		}
		message := &models.Message{
			TenantID: conversation.TenantID, ConversationID: conversation.ID,
			SessionNo: sessionNo, HistoricalOnly: true, ClientMsgID: clientMsgID,
			SenderType: enums.IMSenderTypeCustomer, MessageType: messageType,
			Content: content, Payload: payload,
			SeqNo:      repositories.MessageRepository.NextSeqNoInTenant(ctx.Tx, conversation.ID, conversation.TenantID),
			SendStatus: enums.IMMessageStatusSent, SentAt: &sentAt,
			AuditFields: models.AuditFields{
				CreatedAt: now, CreateUserName: displayExternalName(&external),
				UpdatedAt: now, UpdateUserName: displayExternalName(&external),
			},
		}
		if createErr := repositories.MessageRepository.Create(ctx.Tx, message); createErr != nil {
			return createErr
		}
		result = message
		return nil
	})
	return result, err
}

func (s *messageService) SendCustomerMessageWithRequestID(conversationID int64, clientMsgID string, messageType enums.IMMessageType, content, payload string, external openidentity.ExternalUser, requestID string) (*models.Message, error) {
	ext := external
	return s.sendMessage(conversationID, enums.IMSenderTypeCustomer, 0, clientMsgID, messageType, content, payload, nil, &ext, requestID)
}

func (s *messageService) sendMessage(conversationID int64, senderType enums.IMSenderType, reqSenderID int64, clientMsgID string,
	messageType enums.IMMessageType, content, payload string, operator *dto.AuthPrincipal, external *openidentity.ExternalUser, requestID string) (*models.Message, error) {

	if senderType == enums.IMSenderTypeCustomer {
		if external == nil || strings.TrimSpace(external.ExternalID) == "" {
			return nil, errorsx.Unauthorized("外部用户标识不能为空")
		}
	} else if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}

	if strs.IsBlank(string(messageType)) {
		messageType = enums.IMMessageTypeText
	}
	conversation, err := s.ValidateConversationSender(conversationID, senderType, operator, external)
	if err != nil {
		return nil, err
	}
	return s.sendValidatedMessage(conversation, senderType, reqSenderID, clientMsgID, messageType, content, payload, operator, external, requestID)
}

func (s *messageService) sendValidatedMessage(conversation *models.Conversation, senderType enums.IMSenderType, reqSenderID int64, clientMsgID string,
	messageType enums.IMMessageType, content, payload string, operator *dto.AuthPrincipal, external *openidentity.ExternalUser, requestID string) (*models.Message, error) {
	return s.sendValidatedMessageWithOptions(conversation, senderType, reqSenderID, clientMsgID, messageType, content, payload, operator, external, requestID, sendMessageOptions{})
}

func (s *messageService) sendValidatedMessageWithOptions(conversation *models.Conversation, senderType enums.IMSenderType, reqSenderID int64, clientMsgID string,
	messageType enums.IMMessageType, content, payload string, operator *dto.AuthPrincipal, external *openidentity.ExternalUser, requestID string, options sendMessageOptions) (*models.Message, error) {
	var err error
	var summary string
	content, payload, summary, err = s.normalizeMessageContent(conversation.ID, messageType, content, payload)
	if err != nil {
		return nil, err
	}
	if !options.skipOutboundMediaValidation && (senderType == enums.IMSenderTypeAgent || senderType == enums.IMSenderTypeAI) {
		if err := WxWorkProtocolService.ValidateOutboundMediaReady(conversation.ID, messageType, payload); err != nil {
			return nil, err
		}
	}
	if strs.IsBlank(content) && strs.IsBlank(payload) {
		return nil, errorsx.InvalidParam("消息内容不能为空")
	}

	// 防抖，消息存在就不再发送了
	if strs.IsNotBlank(clientMsgID) {
		if existing := repositories.MessageRepository.GetByClientMsgIDInTenant(sqls.DB(), conversation.ID, conversation.TenantID, clientMsgID); existing != nil {
			if senderType == enums.IMSenderTypeCustomer {
				if _, _, err := AIReplyJobService.EnsureForMessage(existing.ID); err != nil {
					return nil, err
				}
			}
			if !options.skipOutbound {
				s.ensureCommittedOutboundMessage(conversation, existing)
			}
			return existing, nil
		}
	}
	if !options.skipOutbound && senderType != enums.IMSenderTypeCustomer {
		if err := WxWorkProtocolService.RequireConversationOutboundRoute(sqls.DB(), conversation); err != nil {
			return nil, err
		}
	}

	var (
		now           = time.Now()
		traceID       = tracex.EnsureRequestID(requestID)
		auditUserID   = int64(0)
		auditUserName = ""
		nextSeq       = repositories.MessageRepository.NextSeqNoInTenant(sqls.DB(), conversation.ID, conversation.TenantID)
		sessionNo     = ConversationRouteService.CurrentSessionNo(conversation.ID)
	)
	if options.sessionNo > 0 {
		sessionNo = options.sessionNo
	} else if senderType == enums.IMSenderTypeCustomer {
		if nextSessionNo, sessionErr := ConversationRouteService.EnsureActiveSessionForCustomerMessage(conversation, now); sessionErr == nil {
			sessionNo = nextSessionNo
		} else {
			return nil, sessionErr
		}
	}
	if operator != nil {
		auditUserID = operator.UserID
		auditUserName = operator.Username
	}
	if senderType == enums.IMSenderTypeCustomer && external != nil {
		auditUserID = 0
		auditUserName = displayExternalName(external)
	}
	message := &models.Message{
		TenantID:       conversation.TenantID,
		ConversationID: conversation.ID,
		SessionNo:      sessionNo,
		RequestID:      traceID,
		ClientMsgID:    clientMsgID,
		SenderType:     senderType,
		SenderID:       reqSenderID,
		MessageType:    messageType,
		Content:        content,
		Payload:        payload,
		SeqNo:          nextSeq,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   auditUserID,
			CreateUserName: auditUserName,
			UpdatedAt:      now,
			UpdateUserID:   auditUserID,
			UpdateUserName: auditUserName,
		},
	}

	switch senderType {
	case enums.IMSenderTypeAgent:
		if message.SenderID == 0 {
			message.SenderID = operator.UserID
		}
	case enums.IMSenderTypeAI:
		if message.SenderID == 0 {
			message.SenderID = reqSenderID
		}
	default:
		message.SenderID = 0
	}

	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if !options.skipOutbound {
			if options.systemOutbound {
				ChannelMessageOutboxService.PrepareSystemOutboundMessage(ctx.Tx, conversation, message)
			} else {
				ChannelMessageOutboxService.PrepareOutboundMessage(ctx.Tx, conversation, message)
			}
		}
		if err := repositories.MessageRepository.Create(ctx.Tx, message); err != nil {
			return err
		}
		if senderType == enums.IMSenderTypeCustomer {
			if _, _, err := AIReplyJobService.EnqueueForMessageDB(ctx.Tx, conversation, message); err != nil {
				return err
			}
		}

		// 处理已读、维度
		agentUnreadCount, customerUnreadCount, err := s.handleReadState(ctx, senderType, conversation, operator, message, external)
		if err != nil {
			return err
		}

		conversation.LastMessageID = message.ID
		conversation.LastMessageAt = now
		conversation.LastActiveAt = now
		conversation.LastMessageSummary = limitText(summary, 255)
		conversation.UpdateUserID = int64(0)
		conversation.UpdateUserName = ""
		if operator != nil {
			conversation.UpdateUserID = operator.UserID
			conversation.UpdateUserName = operator.Username
		}
		if senderType == enums.IMSenderTypeCustomer && external != nil {
			conversation.UpdateUserID = 0
			conversation.UpdateUserName = displayExternalName(external)
		}
		conversation.UpdatedAt = now
		conversation.AgentUnreadCount = int(agentUnreadCount)
		conversation.CustomerUnreadCount = int(customerUnreadCount)
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, conversation.ID, conversation.TenantID, map[string]any{
			"last_message_id":       conversation.LastMessageID,
			"last_message_at":       conversation.LastMessageAt,
			"last_active_at":        conversation.LastActiveAt,
			"last_message_summary":  conversation.LastMessageSummary,
			"update_user_id":        conversation.UpdateUserID,
			"update_user_name":      conversation.UpdateUserName,
			"updated_at":            conversation.UpdatedAt,
			"agent_unread_count":    conversation.AgentUnreadCount,
			"customer_unread_count": conversation.CustomerUnreadCount,
		}); err != nil {
			return err
		}

		eventContent := options.eventContent
		if eventContent == "" {
			eventContent = enums.GetIMSenderTypeLabel(senderType) + "发送消息"
		}
		if err := ConversationEventLogService.CreateEventWithRequestID(ctx, conversation.ID, traceID, enums.IMEventTypeMessageSend, senderType,
			func() int64 {
				if operator != nil {
					return operator.UserID
				}
				return 0
			}(),
			eventContent,
			"",
		); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	if senderType == enums.IMSenderTypeCustomer {
		AIReplyJobService.NotifyNewerMessage(conversation.ID, message.ID)
	}
	s.publishCommittedMessage(conversation, message)

	if senderType == enums.IMSenderTypeAgent {
		markRouteMessage := ConversationRouteService.MarkAgentMessage
		if options.externalAgentReply {
			markRouteMessage = ConversationRouteService.MarkExternalAgentMessage
		}
		if markErr := markRouteMessage(conversation.ID, now); markErr != nil {
			slog.Warn("mark agent route message failed", "conversation_id", conversation.ID, "error", markErr)
		}
		AIManualResumeTaskService.CancelActive(conversation.ID, "human agent replied")
		if options.externalAgentReply {
			if updatedConversation := ConversationService.Get(conversation.ID); updatedConversation != nil {
				WsService.PublishConversationChanged(updatedConversation, enums.IMRealtimeEventConversationUpdated)
			}
		}
	}

	// 客户发送消息，触发AI回复
	if senderType == enums.IMSenderTypeCustomer {
		if routeState := ConversationRouteService.GetByConversationID(conversation.ID); routeState != nil && routeState.StoreID > 0 {
			if err := CustomerService.TouchStoreRelation(conversation.CustomerID, routeState.StoreID, routeState.WxWorkInstanceID, conversation.ID, now); err != nil {
				slog.Warn("touch customer store relation failed", "conversation_id", conversation.ID, "customer_id", conversation.CustomerID, "store_id", routeState.StoreID, "error", err)
			}
		}
		if markErr := ConversationRouteService.MarkCustomerMessage(conversation.ID, now); markErr != nil {
			slog.Warn("mark customer route message failed", "conversation_id", conversation.ID, "error", markErr)
		}
		if routeState := ConversationRouteService.GetByConversationID(conversation.ID); routeState != nil {
			if routeStatusBlocksAIReply(routeState.RouteStatus) {
				if handled, handleErr := ConversationHandoffConfirmationService.HandleWaitingCustomerResolution(conversation, message, routeState); handleErr != nil {
					slog.Warn("handle customer manual-wait resolution failed", "conversation_id", conversation.ID, "message_id", message.ID, "error", handleErr)
					return message, err
				} else if handled {
					return message, err
				}
				if routeState.NeedHumanFollowUp {
					AIManualResumeTaskService.RecordWaitingCustomerMessage(conversation.ID, message.ID)
				}
				if routeState.RouteStatus == enums.ConversationRouteStatusHQAgentDeskPending && conversation.Status == enums.IMConversationStatusPending && conversation.CurrentAssigneeID == 0 {
					ConversationDispatchService.ScheduleDispatch(conversation.ID)
				}
				return message, err
			}
			if handled, handleErr := ConversationHandoffConfirmationService.HandleCustomerMessage(conversation, message); handleErr != nil {
				slog.Warn("consume pending human handoff confirmation failed", "conversation_id", conversation.ID, "message_id", message.ID, "error", handleErr)
				return message, err
			} else if handled {
				return message, err
			}
			if routeState.WxWorkInstanceID > 0 {
				instance := WxWorkProtocolInstanceService.GetByTenantID(routeState.WxWorkInstanceID, conversation.TenantID)
				if instance != nil && !instance.AIReplyEnabled {
					return message, err
				}
			}
			if routeState.RouteStatus == enums.ConversationRouteStatusAIServing {
				AIManualResumeTaskService.CancelActive(conversation.ID, "new customer message is handled by the normal AI path")
			}
		}
	}
	return message, err
}

func shouldTriggerAIReply(messageType enums.IMMessageType) bool {
	return messageType == enums.IMMessageTypeText || messageType == enums.IMMessageTypeHTML
}

func mediaMessageAlreadyUnderstood(message models.Message) bool {
	if !isMediaUnderstandingMessage(message.MessageType) {
		return false
	}
	mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
	return strings.TrimSpace(status) == "understood" && (strings.TrimSpace(mediaText) != "" || strings.TrimSpace(mediaSummary) != "")
}

func routeStatusBlocksAIReply(routeStatus enums.ConversationRouteStatus) bool {
	switch routeStatus {
	case enums.ConversationRouteStatusStoreWecomManual,
		enums.ConversationRouteStatusHQAgentDeskPending,
		enums.ConversationRouteStatusHQAgentDeskServing:
		return true
	default:
		return false
	}
}

func isMediaUnderstandingMessage(messageType enums.IMMessageType) bool {
	switch messageType {
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeAttachment:
		return true
	default:
		return false
	}
}

func (s *messageService) publishCommittedMessage(conversation *models.Conversation, message *models.Message) {
	if conversation == nil || message == nil {
		return
	}
	logServiceAnalyticsCaptureError("message", conversation.ID, ServiceAnalyticsCaptureService.RecordMessage(message))
	ConversationEvolutionService.ObserveCommittedMessage(conversation, message)
	s.ensureCommittedOutboundMessage(conversation, message)
	WsService.PublishMessageCreated(conversation, message)
	WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationUpdated)
}

func (s *messageService) ensureCommittedOutboundMessage(conversation *models.Conversation, message *models.Message) {
	if conversation == nil || message == nil || strings.TrimSpace(message.OutboundChannelType) == "" {
		return
	}
	if _, err := ChannelMessageOutboxService.EnsureMarkedOutboundMessage(conversation, message); err != nil {
		slog.Error("ensure outbound channel message failed",
			"channel_type", message.OutboundChannelType,
			"conversation_id", conversation.ID,
			"message_id", message.ID,
			"error", err,
		)
		return
	}
	if message.OutboundChannelType == enums.ChannelTypeWxWorkProtocol {
		go WxWorkProtocolService.DispatchPendingOutbox(10)
	}
}

// handleReadState 根据发送者类型更新会话已读状态，并返回更新后的客服和客户未读消息数。
func (s *messageService) handleReadState(ctx *sqls.TxContext, senderType enums.IMSenderType, conversation *models.Conversation, operator *dto.AuthPrincipal, message *models.Message, external *openidentity.ExternalUser) (agentUnreadCount int64, customerUnreadCount int64, err error) {
	readStateType := senderType
	if senderType == enums.IMSenderTypeAI || senderType == enums.IMSenderTypeSystem {
		readStateType = enums.IMSenderTypeAgent
	}
	if readStateType == enums.IMSenderTypeAgent {
		if _, err := ConversationReadStateService.MarkAgentRead(ctx, conversation, operator, message); err != nil {
			return 0, 0, err
		}
	} else {
		if _, err := ConversationReadStateService.MarkCustomerRead(ctx, conversation, external, message); err != nil {
			return 0, 0, err
		}
	}
	agentReadState, customerReadState := ConversationReadStateService.getConversationReadStates(ctx.Tx, conversation.ID)
	if agentUnreadCount, err = ConversationReadStateService.CountUnreadMessages(ctx, conversation.ID, s.readSeqNo(agentReadState), enums.IMSenderTypeCustomer); err != nil {
		return 0, 0, err
	}
	if customerUnreadCount, err = ConversationReadStateService.CountUnreadMessages(ctx, conversation.ID, s.readSeqNo(customerReadState), enums.IMSenderTypeAgent, enums.IMSenderTypeAI, enums.IMSenderTypeSystem); err != nil {
		return 0, 0, err
	}
	return agentUnreadCount, customerUnreadCount, nil
}

func limitText(value string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxLen {
		return value
	}
	return string(runes[:maxLen])
}

func buildMessageSummary(messageType enums.IMMessageType, content string) string {
	content = strings.TrimSpace(content)
	if content != "" {
		return content
	}
	switch messageType {
	case enums.IMMessageTypeImage:
		return "[图片]"
	case enums.IMMessageTypeVoice:
		return "[语音]"
	case enums.IMMessageTypeVideo:
		return "[视频]"
	case enums.IMMessageTypeAttachment:
		return "[附件]"
	case enums.IMMessageTypeHTML:
		return utils.BuildHTMLSummary(content)
	case "":
		return ""
	default:
		return "[" + string(messageType) + "]"
	}
}

func (s *messageService) normalizeMessageContent(conversationID int64, messageType enums.IMMessageType, content, payload string) (string, string, string, error) {
	switch messageType {
	case enums.IMMessageTypeHTML:
		conversation := ConversationService.Get(conversationID)
		if conversation == nil || conversation.TenantID <= 0 {
			return "", "", "", errorsx.InvalidParam("会话不存在或缺少接入公司归属")
		}
		sanitized := utils.SanitizeMessageHTML(content)
		normalized, err := utils.NormalizeMessageHTMLAssetsInTenant(sanitized, conversation.TenantID)
		if err != nil {
			return "", "", "", errorsx.InvalidParam("HTML消息中的图片必须使用已上传文件")
		}
		summary := utils.BuildHTMLSummary(normalized)
		if summary == "" {
			return "", "", "", errorsx.InvalidParam("消息内容不能为空")
		}
		return normalized, "", summary, nil
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeVideo, enums.IMMessageTypeAttachment, enums.IMMessageTypeGIF:
		assetPayload, err := parseIMMessageAssetPayload(payload)
		if err != nil {
			return "", "", "", err
		}
		conversation := ConversationService.Get(conversationID)
		if conversation == nil || conversation.TenantID <= 0 {
			return "", "", "", errorsx.InvalidParam("会话不存在或缺少接入公司归属")
		}
		asset := AssetService.GetByAssetIDInTenant(assetPayload.AssetID, conversation.TenantID)
		if err := validateConversationAsset(asset, conversation, messageType); err != nil {
			return "", "", "", err
		}
		canonicalPayload, err := buildIMMessageAssetPayloadWithMedia(asset, assetPayload.WxMedia)
		if err != nil {
			return "", "", "", err
		}
		canonicalPayload, err = mergeIMMessageAssetUnderstandingPayload(canonicalPayload, assetPayload)
		if err != nil {
			return "", "", "", err
		}
		summary := "[附件]"
		if messageType == enums.IMMessageTypeImage {
			summary = "[图片]"
		} else if messageType == enums.IMMessageTypeVoice {
			summary = "[语音]"
		} else if messageType == enums.IMMessageTypeVideo {
			summary = "[视频]"
		} else if messageType == enums.IMMessageTypeGIF {
			summary = "[动画表情]"
		}
		content = strings.TrimSpace(asset.Filename)
		return content, canonicalPayload, summary + s.suffixFilenameForSummary(asset.Filename), nil
	default:
		content = strings.TrimSpace(content)
		if content == "" && strings.TrimSpace(payload) == "" {
			return "", "", "", errorsx.InvalidParam("消息内容不能为空")
		}
		return content, strings.TrimSpace(payload), buildMessageSummary(messageType, content), nil
	}
}

func (s *messageService) ValidateConversationSender(conversationID int64, senderType enums.IMSenderType, operator *dto.AuthPrincipal, external *openidentity.ExternalUser) (*models.Conversation, error) {
	conversation, err := requireConversationParent(sqls.DB(), conversationID)
	if err != nil {
		return nil, err
	}
	if conversation.Status == enums.IMConversationStatusClosed {
		return nil, errorsx.InvalidParam("会话已关闭")
	}
	switch senderType {
	case enums.IMSenderTypeAgent:
		conversation, err = requireOperatorConversation(sqls.DB(), conversationID, operator)
		if err != nil {
			return nil, err
		}
		if s.canSendStoreManualAgentMessage(conversation, operator) {
			return conversation, nil
		}
		if conversation.Status != enums.IMConversationStatusActive || conversation.CurrentAssigneeID == 0 {
			return nil, errorsx.InvalidParam("会话未分配客服，暂不允许发送消息")
		}
		if conversation.CurrentAssigneeID != operator.UserID {
			return nil, errorsx.Forbidden("当前会话已分配给其他客服")
		}
	case enums.IMSenderTypeAI:
		if operator == nil {
			return nil, errorsx.Unauthorized("未登录或登录已过期")
		}
		if conversation.Status != enums.IMConversationStatusAIServing && !s.allowAIMessageOnPendingHandoff(conversation) {
			return nil, errorsx.Forbidden("当前会话不处于 AI 接待状态")
		}
		if conversation.CurrentAssigneeID != 0 {
			return nil, errorsx.Forbidden("当前会话已由人工客服接管")
		}
	case enums.IMSenderTypeCustomer:
		if external == nil || !ConversationService.IsCustomerConversationOwner(conversation, *external) {
			return nil, errorsx.Forbidden("无权访问该会话")
		}
	default:
		return nil, errorsx.InvalidParam("不支持的发送人类型")
	}
	return conversation, nil
}

func (s *messageService) canSendStoreManualAgentMessage(conversation *models.Conversation, operator *dto.AuthPrincipal) bool {
	if conversation == nil || operator == nil || operator.UserID <= 0 {
		return false
	}
	if conversation.Status == enums.IMConversationStatusClosed || conversation.CurrentAssigneeID != 0 {
		return false
	}
	if operator.ActiveTenantID != conversation.TenantID {
		return false
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, conversation.TenantID)
	if route == nil || route.RouteStatus != enums.ConversationRouteStatusStoreWecomManual {
		return false
	}
	if ConversationService.isAdmin(operator) {
		return true
	}
	if slices.Contains(operator.Roles, constants.RoleCodeStoreStaff) {
		return AgentTeamScopeService.CanViewConversation(operator, conversation.ID)
	}
	profile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ? AND status = ?", conversation.TenantID, operator.UserID, enums.StatusOk)
	return AgentProfileService.ProfileCanServeRoute(profile, route)
}

func (s *messageService) allowAIMessageOnPendingHandoff(conversation *models.Conversation) bool {
	if conversation == nil {
		return false
	}
	return conversation.Status == enums.IMConversationStatusPending &&
		conversation.HandoffAt != nil &&
		conversation.CurrentAssigneeID == 0
}

func (s *messageService) suffixFilenameForSummary(filename string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return ""
	}
	return " " + filename
}

func (s *messageService) readSeqNo(state *models.ConversationReadState) int64 {
	if state == nil {
		return 0
	}
	return state.LastReadSeqNo
}
