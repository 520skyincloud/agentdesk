package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	"encoding/json"
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

type channelMessageOutboxPayload struct {
	AIServiceNotice bool `json:"aiServiceNotice,omitempty"`
}

func (s *channelMessageOutboxService) Get(id int64) *models.ChannelMessageOutbox {
	return repositories.ChannelMessageOutboxRepository.Get(sqls.DB(), id)
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
	return repositories.ChannelMessageOutboxRepository.Create(sqls.DB(), t)
}

func (s *channelMessageOutboxService) Update(t *models.ChannelMessageOutbox) error {
	return repositories.ChannelMessageOutboxRepository.Update(sqls.DB(), t)
}

func (s *channelMessageOutboxService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.ChannelMessageOutboxRepository.Updates(sqls.DB(), id, columns)
}

func (s *channelMessageOutboxService) TryMarkSending(id int64) (bool, error) {
	if id <= 0 {
		return false, nil
	}
	result := sqls.DB().Model(&models.ChannelMessageOutbox{}).
		Where("id = ? AND send_status IN ?", id, []string{
			string(enums.ChannelMessageOutboxStatusPending),
			string(enums.ChannelMessageOutboxStatusFailed),
		}).
		Updates(map[string]any{
			"send_status": string(enums.ChannelMessageOutboxStatusSending),
			"updated_at":  time.Now(),
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (s *channelMessageOutboxService) ClaimForDispatch(outbox models.ChannelMessageOutbox, message *models.Message) (bool, error) {
	if outbox.ID <= 0 {
		return false, nil
	}
	claimed := false
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if message != nil && message.SenderType == enums.IMSenderTypeAI && !s.isAIServiceNotice(outbox) && !isAIServiceNoticeMessage(message) {
			state, err := ConversationRouteService.lockByConversationIDWithDB(ctx.Tx, message.ConversationID)
			if err != nil {
				return err
			}
			if !MessageService.canSendAIReplyWithDB(ctx.Tx, message.ConversationID, message.RequestID, 0, state) {
				result := ctx.Tx.Model(&models.ChannelMessageOutbox{}).
					Where("id = ? AND send_status IN ?", outbox.ID, []string{
						string(enums.ChannelMessageOutboxStatusPending),
						string(enums.ChannelMessageOutboxStatusFailed),
					}).
					Updates(map[string]any{
						"send_status":   string(enums.ChannelMessageOutboxStatusCancelled),
						"next_retry_at": nil,
						"last_error":    "cancelled because conversation entered human service",
						"updated_at":    time.Now(),
					})
				return result.Error
			}
		}
		result := ctx.Tx.Model(&models.ChannelMessageOutbox{}).
			Where("id = ? AND send_status IN ?", outbox.ID, []string{
				string(enums.ChannelMessageOutboxStatusPending),
				string(enums.ChannelMessageOutboxStatusFailed),
			}).
			Updates(map[string]any{
				"send_status": string(enums.ChannelMessageOutboxStatusSending),
				"updated_at":  time.Now(),
			})
		if result.Error != nil {
			return result.Error
		}
		claimed = result.RowsAffected > 0
		return nil
	})
	return claimed, err
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

func (s *channelMessageOutboxService) EnqueueWxWorkKFMessage(conversation *models.Conversation, message *models.Message) error {
	return s.enqueueExternalTextMessage(enums.ChannelTypeWxWorkKF, conversation, message, false)
}

func (s *channelMessageOutboxService) EnqueueWxWorkCLIMessage(conversation *models.Conversation, message *models.Message) error {
	return s.enqueueExternalTextMessage(enums.ChannelTypeWxWorkCLI, conversation, message, false)
}

func (s *channelMessageOutboxService) EnqueueWxWorkProtocolMessage(conversation *models.Conversation, message *models.Message) error {
	return s.enqueueExternalMessage(enums.ChannelTypeWxWorkProtocol, conversation, message, true, false)
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

func (s *channelMessageOutboxService) enqueueExternalTextMessage(channelType string, conversation *models.Conversation, message *models.Message, aiServiceNotice bool) error {
	return s.enqueueExternalMessage(channelType, conversation, message, false, aiServiceNotice)
}

func (s *channelMessageOutboxService) enqueueExternalMessage(channelType string, conversation *models.Conversation, message *models.Message, richMedia bool, aiServiceNotice bool) error {
	if conversation == nil || message == nil {
		return nil
	}
	channel := ChannelService.Get(conversation.ChannelID)
	if channel == nil || channel.ChannelType != channelType {
		return nil
	}
	if message.SenderType != enums.IMSenderTypeAgent && message.SenderType != enums.IMSenderTypeAI {
		return nil
	}
	switch message.MessageType {
	case enums.IMMessageTypeText,
		enums.IMMessageTypeHTML,
		enums.IMMessageTypeImage,
		enums.IMMessageTypeAttachment,
		enums.IMMessageTypeVideo:
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
		if !richMedia {
			return nil
		}
	default:
		return nil
	}
	if existing := s.GetByMessageID(channelType, message.ID); existing != nil {
		return nil
	}

	payload, err := json.Marshal(map[string]any{
		"conversationId":  conversation.ID,
		"messageId":       message.ID,
		"messageType":     message.MessageType,
		"content":         strings.TrimSpace(message.Content),
		"payload":         strings.TrimSpace(message.Payload),
		"senderId":        message.SenderID,
		"aiServiceNotice": aiServiceNotice,
	})
	if err != nil {
		return err
	}

	now := time.Now()
	return s.Create(&models.ChannelMessageOutbox{
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

func (s *channelMessageOutboxService) cancelPendingOrdinaryAIWithDB(db *gorm.DB, conversationID int64, beforeMessageID int64, reason string) (int64, error) {
	if db == nil || conversationID <= 0 {
		return 0, nil
	}
	items := repositories.ChannelMessageOutboxRepository.Find(db, sqls.NewCnd().
		Eq("conversation_id", conversationID).
		In("send_status", []string{
			string(enums.ChannelMessageOutboxStatusPending),
			string(enums.ChannelMessageOutboxStatusFailed),
		}).
		Asc("id"))
	var cancelled int64
	now := time.Now()
	for i := range items {
		item := items[i]
		if item.MessageID <= 0 || (beforeMessageID > 0 && item.MessageID >= beforeMessageID) {
			continue
		}
		message := repositories.MessageRepository.Get(db, item.MessageID)
		if message == nil || message.SenderType != enums.IMSenderTypeAI {
			continue
		}
		if s.isAIServiceNotice(item) || isAIServiceNoticeMessage(message) {
			continue
		}
		result := db.Model(&models.ChannelMessageOutbox{}).
			Where("id = ? AND send_status IN ?", item.ID, []string{
				string(enums.ChannelMessageOutboxStatusPending),
				string(enums.ChannelMessageOutboxStatusFailed),
			}).
			Updates(map[string]any{
				"send_status":   string(enums.ChannelMessageOutboxStatusCancelled),
				"next_retry_at": nil,
				"last_error":    strings.TrimSpace(reason),
				"updated_at":    now,
			})
		if result.Error != nil {
			return cancelled, result.Error
		}
		cancelled += result.RowsAffected
	}
	return cancelled, nil
}

func (s *channelMessageOutboxService) CancelPendingOrdinaryAI(conversationID int64, beforeMessageID int64, reason string) (int64, error) {
	return s.cancelPendingOrdinaryAIWithDB(sqls.DB(), conversationID, beforeMessageID, reason)
}

func (s *channelMessageOutboxService) Cancel(id int64, reason string) error {
	if id <= 0 {
		return nil
	}
	now := time.Now()
	return sqls.DB().Model(&models.ChannelMessageOutbox{}).
		Where("id = ? AND send_status IN ?", id, []string{
			string(enums.ChannelMessageOutboxStatusPending),
			string(enums.ChannelMessageOutboxStatusFailed),
		}).
		Updates(map[string]any{
			"send_status":   string(enums.ChannelMessageOutboxStatusCancelled),
			"next_retry_at": nil,
			"last_error":    strings.TrimSpace(reason),
			"updated_at":    now,
		}).Error
}

func (s *channelMessageOutboxService) isAIServiceNotice(outbox models.ChannelMessageOutbox) bool {
	payload := channelMessageOutboxPayload{}
	return json.Unmarshal([]byte(strings.TrimSpace(outbox.Payload)), &payload) == nil && payload.AIServiceNotice
}

func (s *channelMessageOutboxService) CanDispatch(outbox models.ChannelMessageOutbox, message *models.Message) (bool, string) {
	if message == nil || message.SenderType != enums.IMSenderTypeAI || s.isAIServiceNotice(outbox) || isAIServiceNoticeMessage(message) {
		return true, ""
	}
	if MessageService.CanSendAIReply(message.ConversationID, message.RequestID, 0) {
		return true, ""
	}
	return false, "cancelled because conversation entered human service"
}

func (s *channelMessageOutboxService) ListPending(channelType string, limit int) []models.ChannelMessageOutbox {
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
	return s.Find(cnd)
}
