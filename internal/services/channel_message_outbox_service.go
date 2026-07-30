package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var ChannelMessageOutboxService = newChannelMessageOutboxService()

func newChannelMessageOutboxService() *channelMessageOutboxService {
	return &channelMessageOutboxService{}
}

type channelMessageOutboxService struct {
}

func (s *channelMessageOutboxService) Get(id int64) *models.ChannelMessageOutbox {
	return repositories.ChannelMessageOutboxRepository.Get(sqls.DB(), id)
}

func (s *channelMessageOutboxService) GetInTenant(id, tenantID int64) *models.ChannelMessageOutbox {
	return repositories.ChannelMessageOutboxRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *channelMessageOutboxService) Take(where ...interface{}) *models.ChannelMessageOutbox {
	return repositories.ChannelMessageOutboxRepository.Take(sqls.DB(), where...)
}

func (s *channelMessageOutboxService) Find(cnd *sqls.Cnd) []models.ChannelMessageOutbox {
	return repositories.ChannelMessageOutboxRepository.Find(sqls.DB(), cnd)
}

func (s *channelMessageOutboxService) FindOne(cnd *sqls.Cnd) *models.ChannelMessageOutbox {
	return repositories.ChannelMessageOutboxRepository.FindOne(sqls.DB(), cnd)
}

func (s *channelMessageOutboxService) FindPageByParams(params *params.QueryParams) (list []models.ChannelMessageOutbox, paging *sqls.Paging) {
	return repositories.ChannelMessageOutboxRepository.FindPageByParams(sqls.DB(), params)
}

func (s *channelMessageOutboxService) FindPageByCnd(cnd *sqls.Cnd) (list []models.ChannelMessageOutbox, paging *sqls.Paging) {
	return repositories.ChannelMessageOutboxRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *channelMessageOutboxService) Count(cnd *sqls.Cnd) int64 {
	return repositories.ChannelMessageOutboxRepository.Count(sqls.DB(), cnd)
}

func (s *channelMessageOutboxService) Create(t *models.ChannelMessageOutbox) error {
	if t == nil {
		return nil
	}
	conversation, err := requireConversationParent(sqls.DB(), t.ConversationID)
	if err != nil {
		return err
	}
	if t.TenantID > 0 && t.TenantID != conversation.TenantID {
		return errorsx.InvalidParam("渠道投递任务不属于当前会话接入公司")
	}
	if t.MessageID > 0 {
		message := repositories.MessageRepository.GetInTenant(sqls.DB(), t.MessageID, conversation.TenantID)
		if message == nil || message.ConversationID != conversation.ID {
			return errorsx.InvalidParam("渠道投递消息不属于当前会话")
		}
	}
	t.TenantID = conversation.TenantID
	return repositories.ChannelMessageOutboxRepository.Create(sqls.DB(), t)
}

func (s *channelMessageOutboxService) Update(t *models.ChannelMessageOutbox) error {
	return repositories.ChannelMessageOutboxRepository.Update(sqls.DB(), t)
}

func (s *channelMessageOutboxService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.ChannelMessageOutboxRepository.Updates(sqls.DB(), id, columns)
}

func (s *channelMessageOutboxService) UpdatesInTenant(id, tenantID int64, columns map[string]any) error {
	return repositories.ChannelMessageOutboxRepository.UpdatesInTenant(sqls.DB(), id, tenantID, columns)
}

func (s *channelMessageOutboxService) TryMarkSending(id, tenantID int64) (bool, error) {
	return repositories.ChannelMessageOutboxRepository.TryMarkSending(sqls.DB(), id, tenantID, time.Now())
}

func (s *channelMessageOutboxService) UpdateColumn(id int64, name string, value interface{}) error {
	return repositories.ChannelMessageOutboxRepository.UpdateColumn(sqls.DB(), id, name, value)
}

func (s *channelMessageOutboxService) Delete(id int64) {
	repositories.ChannelMessageOutboxRepository.Delete(sqls.DB(), id)
}

// GetByMessageID retrieves the outbox entry by message ID and channel type.
func (s *channelMessageOutboxService) GetByMessageID(channelType string, messageID int64) *models.ChannelMessageOutbox {
	return repositories.ChannelMessageOutboxRepository.Take(sqls.DB(), "channel_type = ? AND message_id = ?", channelType, messageID)
}

func (s *channelMessageOutboxService) GetByMessageIDInTenant(channelType string, messageID, tenantID int64) *models.ChannelMessageOutbox {
	if messageID == 0 || tenantID <= 0 {
		return nil
	}
	return repositories.ChannelMessageOutboxRepository.Take(sqls.DB(), "tenant_id = ? AND channel_type = ? AND message_id = ?", tenantID, channelType, messageID)
}

func (s *channelMessageOutboxService) EnqueueWxWorkKFMessage(conversation *models.Conversation, message *models.Message) error {
	return s.enqueueExternalTextMessage(enums.ChannelTypeWxWorkKF, conversation, message)
}

func (s *channelMessageOutboxService) EnqueueWxWorkCLIMessage(conversation *models.Conversation, message *models.Message) error {
	return s.enqueueExternalTextMessage(enums.ChannelTypeWxWorkCLI, conversation, message)
}

func (s *channelMessageOutboxService) EnqueueWxWorkProtocolMessage(conversation *models.Conversation, message *models.Message) error {
	return s.enqueueExternalMessage(enums.ChannelTypeWxWorkProtocol, conversation, message)
}

func (s *channelMessageOutboxService) PrepareOutboundMessage(db *gorm.DB, conversation *models.Conversation, message *models.Message) {
	if message == nil {
		return
	}
	message.OutboundChannelType = ""
	if db == nil || conversation == nil || conversation.ChannelID <= 0 ||
		(message.SenderType != enums.IMSenderTypeAgent && message.SenderType != enums.IMSenderTypeAI) {
		return
	}
	channel := repositories.ChannelRepository.GetInTenant(db, conversation.ChannelID, conversation.TenantID)
	if channel == nil || channel.TenantID != conversation.TenantID ||
		!supportsOutboundMessageType(channel.ChannelType, message.MessageType) {
		return
	}
	message.OutboundChannelType = channel.ChannelType
}

func (s *channelMessageOutboxService) PrepareSystemOutboundMessage(db *gorm.DB, conversation *models.Conversation, message *models.Message) {
	if message == nil {
		return
	}
	message.OutboundChannelType = ""
	if db == nil || conversation == nil || conversation.ChannelID <= 0 ||
		message.SenderType != enums.IMSenderTypeSystem {
		return
	}
	channel := repositories.ChannelRepository.GetInTenant(db, conversation.ChannelID, conversation.TenantID)
	if channel == nil || channel.TenantID != conversation.TenantID ||
		!supportsOutboundMessageType(channel.ChannelType, message.MessageType) {
		return
	}
	message.OutboundChannelType = channel.ChannelType
}

func (s *channelMessageOutboxService) EnsureMarkedOutboundMessage(conversation *models.Conversation, message *models.Message) (bool, error) {
	if message == nil || strings.TrimSpace(message.OutboundChannelType) == "" {
		return false, nil
	}
	return s.ensureExternalMessage(sqls.DB(), message.OutboundChannelType, conversation, message, true)
}

func (s *channelMessageOutboxService) RepairMissingOutboundMessages(limit int) (int, error) {
	db := sqls.DB()
	messages, err := repositories.MessageRepository.FindMissingOutboundOutbox(db, limit)
	if err != nil {
		return 0, err
	}
	repaired := 0
	var firstErr error
	for i := range messages {
		message := &messages[i]
		conversation := repositories.ConversationRepository.GetInTenant(db, message.ConversationID, message.TenantID)
		if conversation == nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("outbox repair message %d has no tenant-scoped conversation", message.ID)
			}
			continue
		}
		created, ensureErr := s.ensureExternalMessage(db, message.OutboundChannelType, conversation, message, true)
		if ensureErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("repair outbox for message %d: %w", message.ID, ensureErr)
			}
			continue
		}
		if created {
			repaired++
		}
	}
	return repaired, firstErr
}

func (s *channelMessageOutboxService) EnqueueWxWorkProtocolStoreRoomNotice(conversationID int64, wxWorkInstanceID int64, roomConversationID string, content string, atList []string) error {
	return s.EnqueueWxWorkProtocolStoreRoomNoticeWithKey(conversationID, wxWorkInstanceID, roomConversationID, content, atList, "")
}

func (s *channelMessageOutboxService) EnqueueWxWorkProtocolStoreRoomNoticeWithKey(conversationID int64, wxWorkInstanceID int64, roomConversationID string, content string, atList []string, noticeKey string) error {
	roomConversationID = strings.TrimSpace(roomConversationID)
	content = strings.TrimSpace(content)
	if conversationID <= 0 || wxWorkInstanceID <= 0 || roomConversationID == "" || content == "" {
		return nil
	}
	conversation, err := requireConversationParent(sqls.DB(), conversationID)
	if err != nil {
		return err
	}
	instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(sqls.DB(), wxWorkInstanceID, conversation.TenantID)
	if instance == nil {
		return errorsx.InvalidParam("企微员工号实例不存在或不属于会话接入公司")
	}
	payload, err := json.Marshal(map[string]any{
		"kind":               "store_room_handoff_notice",
		"conversationId":     conversationID,
		"wxWorkInstanceId":   wxWorkInstanceID,
		"roomConversationId": roomConversationID,
		"content":            content,
		"atList":             atList,
		"noticeKey":          strings.TrimSpace(noticeKey),
	})
	if err != nil {
		return err
	}
	now := time.Now()
	messageID := -now.UnixNano()
	if key := strings.TrimSpace(noticeKey); key != "" {
		hasher := fnv.New64a()
		_, _ = hasher.Write([]byte(key))
		messageID = -int64(hasher.Sum64() & 0x7fffffffffffffff)
		if existing := s.GetByMessageID(enums.ChannelTypeWxWorkProtocol, messageID); existing != nil {
			return nil
		}
	}
	return s.Create(&models.ChannelMessageOutbox{
		TenantID:       conversation.TenantID,
		ChannelType:    enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversationID,
		MessageID:      messageID,
		Payload:        string(payload),
		SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserName: "store_room_handoff",
			UpdatedAt:      now,
			UpdateUserName: "store_room_handoff",
		},
	})
}

func (s *channelMessageOutboxService) enqueueExternalTextMessage(channelType string, conversation *models.Conversation, message *models.Message) error {
	return s.enqueueExternalMessage(channelType, conversation, message)
}

func (s *channelMessageOutboxService) enqueueExternalMessage(channelType string, conversation *models.Conversation, message *models.Message) error {
	_, err := s.ensureExternalMessage(sqls.DB(), channelType, conversation, message, false)
	return err
}

func (s *channelMessageOutboxService) ensureExternalMessage(db *gorm.DB, channelType string, conversation *models.Conversation, message *models.Message, requireMarker bool) (bool, error) {
	if conversation == nil || message == nil {
		return false, nil
	}
	channelType = strings.TrimSpace(channelType)
	if requireMarker && strings.TrimSpace(message.OutboundChannelType) != channelType {
		return false, errorsx.InvalidParam("消息渠道投递标记不一致")
	}
	channel := repositories.ChannelRepository.GetInTenant(db, conversation.ChannelID, conversation.TenantID)
	if channel == nil || channel.TenantID != conversation.TenantID || channel.ChannelType != channelType || message.TenantID != conversation.TenantID {
		if requireMarker {
			return false, errorsx.InvalidParam("消息渠道投递范围不一致")
		}
		return false, nil
	}
	allowedSender := message.SenderType == enums.IMSenderTypeAgent ||
		message.SenderType == enums.IMSenderTypeAI ||
		(requireMarker && message.SenderType == enums.IMSenderTypeSystem)
	if !allowedSender {
		if requireMarker {
			return false, errorsx.InvalidParam("消息发送人不允许当前渠道投递")
		}
		return false, nil
	}
	if !supportsOutboundMessageType(channelType, message.MessageType) {
		if requireMarker {
			return false, errorsx.InvalidParam("消息类型不支持当前渠道投递")
		}
		return false, nil
	}

	payload, err := json.Marshal(map[string]any{
		"conversationId": conversation.ID,
		"messageId":      message.ID,
		"messageType":    message.MessageType,
		"content":        strings.TrimSpace(message.Content),
		"payload":        strings.TrimSpace(message.Payload),
		"senderId":       message.SenderID,
	})
	if err != nil {
		return false, err
	}

	now := time.Now()
	return repositories.ChannelMessageOutboxRepository.CreateIfAbsent(db, &models.ChannelMessageOutbox{
		TenantID:       conversation.TenantID,
		ChannelType:    channelType,
		ConversationID: conversation.ID,
		MessageID:      message.ID,
		Payload:        string(payload),
		SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   message.UpdateUserID,
			CreateUserName: message.UpdateUserName,
			UpdatedAt:      now,
			UpdateUserID:   message.UpdateUserID,
			UpdateUserName: message.UpdateUserName,
		},
	})
}

func supportsOutboundMessageType(channelType string, messageType enums.IMMessageType) bool {
	switch messageType {
	case enums.IMMessageTypeText,
		enums.IMMessageTypeHTML,
		enums.IMMessageTypeImage,
		enums.IMMessageTypeAttachment,
		enums.IMMessageTypeVideo:
		return channelType == enums.ChannelTypeWxWorkProtocol ||
			channelType == enums.ChannelTypeWxWorkKF ||
			channelType == enums.ChannelTypeWxWorkCLI
	case enums.IMMessageTypeVoice,
		enums.IMMessageTypeGIF,
		enums.IMMessageTypeLocation,
		enums.IMMessageTypeContactCard,
		enums.IMMessageTypeLink,
		enums.IMMessageTypeMiniProgram,
		enums.IMMessageTypeFeed,
		enums.IMMessageTypeFeedLive,
		enums.IMMessageTypeQuote,
		enums.IMMessageTypeMergedForward,
		enums.IMMessageTypeShopProduct:
		return channelType == enums.ChannelTypeWxWorkProtocol
	default:
		return false
	}
}

func (s *channelMessageOutboxService) ListPending(channelType string, limit int) []models.ChannelMessageOutbox {
	return s.listPending(channelType, 0, limit)
}

func (s *channelMessageOutboxService) ListPendingInTenant(channelType string, tenantID int64, limit int) []models.ChannelMessageOutbox {
	if tenantID <= 0 {
		return nil
	}
	return s.listPending(channelType, tenantID, limit)
}

func (s *channelMessageOutboxService) listPending(channelType string, tenantID int64, limit int) []models.ChannelMessageOutbox {
	if limit <= 0 {
		limit = 20
	}
	cnd := sqls.NewCnd().
		Eq("channel_type", strings.TrimSpace(channelType)).
		In("send_status", []string{
			string(enums.ChannelMessageOutboxStatusPending),
			string(enums.ChannelMessageOutboxStatusFailed),
		}).
		Asc("id").
		Limit(limit)
	if tenantID > 0 {
		cnd.Eq("tenant_id", tenantID)
	}
	return s.Find(cnd)
}
