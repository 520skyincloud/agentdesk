package services

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/repositories"

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
	AIServiceNotice            bool `json:"aiServiceNotice,omitempty"`
	ReplyBeforeDeferredHandoff bool `json:"replyBeforeDeferredHandoff,omitempty"`
}

const channelMessageOutboxDispatchUncertainReasonPrefix = "delivery result uncertain after dispatch claim: "

type externalDispatchResultUncertainError struct {
	cause error
}

func (e *externalDispatchResultUncertainError) Error() string {
	if e == nil || e.cause == nil {
		return "external dispatch result is uncertain"
	}
	return e.cause.Error()
}

func (e *externalDispatchResultUncertainError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func markExternalDispatchResultUncertain(err error) error {
	if err == nil || isExternalDispatchResultUncertain(err) {
		return err
	}
	return &externalDispatchResultUncertainError{cause: err}
}

func isExternalDispatchResultUncertain(err error) bool {
	var target *externalDispatchResultUncertainError
	return errors.As(err, &target)
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

func (s *channelMessageOutboxService) TryMarkSending(outbox models.ChannelMessageOutbox) (bool, error) {
	if outbox.ID <= 0 {
		return false, nil
	}
	status := strings.TrimSpace(outbox.SendStatus)
	if status != string(enums.ChannelMessageOutboxStatusPending) && status != string(enums.ChannelMessageOutboxStatusFailed) {
		return false, nil
	}
	result := sqls.DB().Model(&models.ChannelMessageOutbox{}).
		Where("id = ? AND send_status = ? AND retry_count = ?", outbox.ID, status, outbox.RetryCount).
		Updates(map[string]any{
			"send_status": string(enums.ChannelMessageOutboxStatusSending),
			"updated_at":  time.Now(),
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (s *channelMessageOutboxService) failUnclaimedDispatchWithDB(db *gorm.DB, outbox models.ChannelMessageOutbox, nextRetryAt *time.Time, reason string) (bool, error) {
	if db == nil || outbox.ID <= 0 {
		return false, nil
	}
	status := strings.TrimSpace(outbox.SendStatus)
	if status != string(enums.ChannelMessageOutboxStatusPending) && status != string(enums.ChannelMessageOutboxStatusFailed) {
		return false, nil
	}
	now := time.Now()
	result := db.Model(&models.ChannelMessageOutbox{}).
		Where("id = ? AND send_status = ? AND retry_count = ?", outbox.ID, status, outbox.RetryCount).
		Updates(map[string]any{
			"send_status":      string(enums.ChannelMessageOutboxStatusFailed),
			"retry_count":      outbox.RetryCount + 1,
			"next_retry_at":    nextRetryAt,
			"last_error":       strings.TrimSpace(reason),
			"updated_at":       now,
			"update_user_id":   outbox.UpdateUserID,
			"update_user_name": outbox.UpdateUserName,
		})
	return result.RowsAffected == 1, result.Error
}

func (s *channelMessageOutboxService) cancelClaimedDispatchUncertainWithDB(db *gorm.DB, outbox models.ChannelMessageOutbox, reason string) (bool, error) {
	if db == nil || outbox.ID <= 0 {
		return false, nil
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "external dispatcher reported failure without details"
	}
	now := time.Now()
	result := db.Model(&models.ChannelMessageOutbox{}).
		Where("id = ? AND send_status = ?", outbox.ID, string(enums.ChannelMessageOutboxStatusSending)).
		Updates(map[string]any{
			"send_status":      string(enums.ChannelMessageOutboxStatusCancelled),
			"retry_count":      outbox.RetryCount + 1,
			"next_retry_at":    nil,
			"last_error":       channelMessageOutboxDispatchUncertainReasonPrefix + reason,
			"updated_at":       now,
			"update_user_id":   outbox.UpdateUserID,
			"update_user_name": outbox.UpdateUserName,
		})
	return result.RowsAffected == 1, result.Error
}

func (s *channelMessageOutboxService) ClaimForDispatch(outbox models.ChannelMessageOutbox, message *models.Message) (bool, error) {
	if outbox.ID <= 0 {
		return false, nil
	}
	status := strings.TrimSpace(outbox.SendStatus)
	if status != string(enums.ChannelMessageOutboxStatusPending) && status != string(enums.ChannelMessageOutboxStatusFailed) {
		return false, nil
	}
	claimed := false
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if message != nil && message.SenderType == enums.IMSenderTypeAI && !s.isAIServiceNotice(outbox) && !isAIServiceNoticeMessage(message) {
			policyOutbox := outbox
			if current := repositories.ChannelMessageOutboxRepository.Get(ctx.Tx, outbox.ID); current != nil {
				policyOutbox = *current
			}
			state, err := ConversationRouteService.lockByConversationIDWithDB(ctx.Tx, message.ConversationID)
			if err != nil {
				return err
			}
			if !MessageService.canSendAIReplyWithDB(ctx.Tx, message.ConversationID, message.RequestID, 0, state) && !s.isReplyBeforeDeferredHandoff(policyOutbox) {
				result := ctx.Tx.Model(&models.ChannelMessageOutbox{}).
					Where("id = ? AND send_status = ? AND retry_count = ?", outbox.ID, status, outbox.RetryCount).
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
			Where("id = ? AND send_status = ? AND retry_count = ?", outbox.ID, status, outbox.RetryCount).
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

// RevalidateClaimedForDispatch closes the route-change window after Claim and
// before the channel performs its external send. It cannot make an external
// protocol atomic, but it guarantees that a persisted human route wins before
// this process starts the network call.
func (s *channelMessageOutboxService) RevalidateClaimedForDispatch(outbox models.ChannelMessageOutbox, message *models.Message) (bool, error) {
	if outbox.ID <= 0 {
		return false, nil
	}
	allowed := false
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if message != nil && message.SenderType == enums.IMSenderTypeAI && !s.isAIServiceNotice(outbox) && !isAIServiceNoticeMessage(message) {
			policyOutbox := outbox
			if current := repositories.ChannelMessageOutboxRepository.Get(ctx.Tx, outbox.ID); current != nil {
				policyOutbox = *current
			}
			state, err := ConversationRouteService.lockByConversationIDWithDB(ctx.Tx, message.ConversationID)
			if err != nil {
				return err
			}
			if !MessageService.canSendAIReplyWithDB(ctx.Tx, message.ConversationID, message.RequestID, 0, state) && !s.isReplyBeforeDeferredHandoff(policyOutbox) {
				result := ctx.Tx.Model(&models.ChannelMessageOutbox{}).
					Where("id = ? AND send_status = ?", outbox.ID, string(enums.ChannelMessageOutboxStatusSending)).
					Updates(map[string]any{
						"send_status":   string(enums.ChannelMessageOutboxStatusCancelled),
						"next_retry_at": nil,
						"last_error":    "cancelled before external send because conversation entered human service",
						"updated_at":    time.Now(),
					})
				return result.Error
			}
		}
		var count int64
		if err := ctx.Tx.Model(&models.ChannelMessageOutbox{}).
			Where("id = ? AND send_status = ?", outbox.ID, string(enums.ChannelMessageOutboxStatusSending)).
			Count(&count).Error; err != nil {
			return err
		}
		allowed = count == 1
		return nil
	})
	return allowed, err
}

func (s *channelMessageOutboxService) completeClaimedDispatchWithDB(db *gorm.DB, outbox models.ChannelMessageOutbox, now time.Time) (bool, error) {
	if db == nil || outbox.ID <= 0 {
		return false, nil
	}
	result := db.Model(&models.ChannelMessageOutbox{}).
		Where("id = ? AND send_status = ?", outbox.ID, string(enums.ChannelMessageOutboxStatusSending)).
		Updates(map[string]any{
			"send_status":      string(enums.ChannelMessageOutboxStatusSent),
			"sent_at":          now,
			"next_retry_at":    nil,
			"last_error":       "",
			"updated_at":       now,
			"update_user_id":   outbox.UpdateUserID,
			"update_user_name": outbox.UpdateUserName,
		})
	return result.RowsAffected == 1, result.Error
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
	if !supportsExternalOutboxMessageType(message.MessageType, richMedia) {
		return nil
	}
	if existing := s.GetByMessageID(channelType, message.ID); existing != nil {
		return nil
	}

	payload, err := buildExternalOutboxPayload(conversation, message, aiServiceNotice)
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

func (s *channelMessageOutboxService) ensureManualResumeMessage(conversation *models.Conversation, message *models.Message, sourceMessageID int64) (bool, error) {
	if conversation == nil || message == nil || message.SenderType != enums.IMSenderTypeAI ||
		!strings.HasPrefix(strings.TrimSpace(message.RequestID), "manual_resume_") || conversation.ChannelID <= 0 {
		return false, nil
	}
	channel := ChannelService.Get(conversation.ChannelID)
	if channel == nil {
		return false, nil
	}
	richMedia := channel.ChannelType == enums.ChannelTypeWxWorkProtocol
	switch channel.ChannelType {
	case enums.ChannelTypeWxWorkProtocol, enums.ChannelTypeWxWorkKF, enums.ChannelTypeWxWorkCLI:
	default:
		return false, nil
	}
	if !supportsExternalOutboxMessageType(message.MessageType, richMedia) {
		return false, nil
	}
	payload, err := buildExternalOutboxPayload(conversation, message, false)
	if err != nil {
		return false, err
	}

	repaired := false
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		state, lockErr := ConversationRouteService.lockByConversationIDWithDB(ctx.Tx, conversation.ID)
		if lockErr != nil {
			return lockErr
		}
		if !MessageService.canSendAIReplyWithDB(ctx.Tx, conversation.ID, message.RequestID, sourceMessageID, state) {
			return nil
		}
		existing := repositories.ChannelMessageOutboxRepository.Take(ctx.Tx, "channel_type = ? AND message_id = ?", channel.ChannelType, message.ID)
		if existing == nil {
			now := time.Now()
			if err := repositories.ChannelMessageOutboxRepository.Create(ctx.Tx, &models.ChannelMessageOutbox{
				ChannelType:    channel.ChannelType,
				ConversationID: conversation.ID,
				MessageID:      message.ID,
				Payload:        payload,
				SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
				AuditFields: models.AuditFields{
					CreatedAt:      now,
					CreateUserID:   message.UpdateUserID,
					CreateUserName: message.UpdateUserName,
					UpdatedAt:      now,
					UpdateUserID:   message.UpdateUserID,
					UpdateUserName: message.UpdateUserName,
				},
			}); err != nil {
				return err
			}
			repaired = true
			return nil
		}
		if strings.TrimSpace(existing.SendStatus) != string(enums.ChannelMessageOutboxStatusCancelled) {
			return nil
		}
		if strings.HasPrefix(strings.TrimSpace(existing.LastError), channelMessageOutboxDispatchUncertainReasonPrefix) {
			return nil
		}
		result := ctx.Tx.Model(&models.ChannelMessageOutbox{}).
			Where("id = ? AND send_status = ?", existing.ID, string(enums.ChannelMessageOutboxStatusCancelled)).
			Updates(map[string]any{
				"send_status":   string(enums.ChannelMessageOutboxStatusPending),
				"next_retry_at": nil,
				"last_error":    "",
				"updated_at":    time.Now(),
			})
		if result.Error != nil {
			return result.Error
		}
		repaired = result.RowsAffected > 0
		return nil
	})
	return repaired, err
}

func supportsExternalOutboxMessageType(messageType enums.IMMessageType, richMedia bool) bool {
	switch messageType {
	case enums.IMMessageTypeText,
		enums.IMMessageTypeHTML,
		enums.IMMessageTypeImage,
		enums.IMMessageTypeAttachment,
		enums.IMMessageTypeVideo:
		return true
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
		return richMedia
	default:
		return false
	}
}

func buildExternalOutboxPayload(conversation *models.Conversation, message *models.Message, aiServiceNotice bool) (string, error) {
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
		return "", err
	}
	return string(payload), nil
}

func (s *channelMessageOutboxService) MarkReplyBeforeDeferredHandoff(conversationID int64, requestID string) error {
	requestID = strings.TrimSpace(requestID)
	if conversationID <= 0 || requestID == "" {
		return nil
	}
	db := sqls.DB()
	messages := repositories.MessageRepository.Find(db, sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("sender_type", enums.IMSenderTypeAI).
		Eq("request_id", requestID).
		Asc("id"))
	messageIDs := make([]int64, 0, len(messages))
	for i := range messages {
		if messages[i].ID > 0 && !isAIServiceNoticeMessage(&messages[i]) {
			messageIDs = append(messageIDs, messages[i].ID)
		}
	}
	if len(messageIDs) == 0 {
		return nil
	}
	items := repositories.ChannelMessageOutboxRepository.Find(db, sqls.NewCnd().
		Eq("conversation_id", conversationID).
		In("message_id", messageIDs).
		In("send_status", []string{
			string(enums.ChannelMessageOutboxStatusPending),
			string(enums.ChannelMessageOutboxStatusSending),
			string(enums.ChannelMessageOutboxStatusFailed),
		}).
		Asc("id"))
	for i := range items {
		if s.isAIServiceNotice(items[i]) || s.isReplyBeforeDeferredHandoff(items[i]) {
			continue
		}
		payload := make(map[string]any)
		if err := json.Unmarshal([]byte(strings.TrimSpace(items[i].Payload)), &payload); err != nil {
			return err
		}
		payload["replyBeforeDeferredHandoff"] = true
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if err := db.Model(&models.ChannelMessageOutbox{}).
			Where("id = ?", items[i].ID).
			Update("payload", string(encoded)).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *channelMessageOutboxService) cancelPendingOrdinaryAIWithDB(db *gorm.DB, conversationID int64, beforeMessageID int64, reason string) (int64, error) {
	return s.cancelPendingOrdinaryAIWithOptionsDB(db, conversationID, beforeMessageID, reason, false)
}

func (s *channelMessageOutboxService) cancelPendingOrdinaryAIForRouteWithDB(db *gorm.DB, conversationID int64, beforeMessageID int64, reason string) (int64, error) {
	return s.cancelPendingOrdinaryAIWithOptionsDB(db, conversationID, beforeMessageID, reason, true)
}

func (s *channelMessageOutboxService) cancelPendingOrdinaryAIWithOptionsDB(db *gorm.DB, conversationID int64, beforeMessageID int64, reason string, preserveDeferredReply bool) (int64, error) {
	if db == nil || conversationID <= 0 {
		return 0, nil
	}
	items := repositories.ChannelMessageOutboxRepository.Find(db, sqls.NewCnd().
		Eq("conversation_id", conversationID).
		In("send_status", []string{
			string(enums.ChannelMessageOutboxStatusPending),
			string(enums.ChannelMessageOutboxStatusSending),
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
		if preserveDeferredReply && s.isReplyBeforeDeferredHandoff(item) {
			continue
		}
		lastError := strings.TrimSpace(reason)
		if strings.TrimSpace(item.SendStatus) == string(enums.ChannelMessageOutboxStatusSending) {
			lastError = channelMessageOutboxDispatchUncertainReasonPrefix + lastError
		}
		result := db.Model(&models.ChannelMessageOutbox{}).
			Where("id = ? AND send_status = ?", item.ID, item.SendStatus).
			Updates(map[string]any{
				"send_status":   string(enums.ChannelMessageOutboxStatusCancelled),
				"next_retry_at": nil,
				"last_error":    lastError,
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

func (s *channelMessageOutboxService) CancelPendingOrdinaryAIForRoute(conversationID int64, beforeMessageID int64, reason string) (int64, error) {
	return s.cancelPendingOrdinaryAIForRouteWithDB(sqls.DB(), conversationID, beforeMessageID, reason)
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

func (s *channelMessageOutboxService) isReplyBeforeDeferredHandoff(outbox models.ChannelMessageOutbox) bool {
	payload := channelMessageOutboxPayload{}
	return json.Unmarshal([]byte(strings.TrimSpace(outbox.Payload)), &payload) == nil && payload.ReplyBeforeDeferredHandoff
}

func (s *channelMessageOutboxService) CanDispatch(outbox models.ChannelMessageOutbox, message *models.Message) (bool, string) {
	if message == nil || message.SenderType != enums.IMSenderTypeAI || s.isAIServiceNotice(outbox) || isAIServiceNoticeMessage(message) {
		return true, ""
	}
	policyOutbox := outbox
	if current := s.Get(outbox.ID); current != nil {
		policyOutbox = *current
	}
	if MessageService.CanSendAIReply(message.ConversationID, message.RequestID, 0) || s.isReplyBeforeDeferredHandoff(policyOutbox) {
		return true, ""
	}
	return false, "cancelled because conversation entered human service"
}

func (s *channelMessageOutboxService) ListPending(channelType string, limit int) []models.ChannelMessageOutbox {
	if limit <= 0 {
		limit = 20
	}
	channelType = strings.TrimSpace(channelType)
	now := time.Now()
	ret := make([]models.ChannelMessageOutbox, 0, limit)
	lastID := int64(0)
	for len(ret) < limit {
		cnd := sqls.NewCnd().
			Eq("channel_type", channelType).
			Where("(send_status = ? OR (send_status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?))",
				string(enums.ChannelMessageOutboxStatusPending),
				string(enums.ChannelMessageOutboxStatusFailed),
				now).
			Asc("id").
			Limit(limit)
		if lastID > 0 {
			cnd.Gt("id", lastID)
		}
		items := s.Find(cnd)
		if len(items) == 0 {
			break
		}
		for i := range items {
			lastID = items[i].ID
			if s.handoffNoticeWaitsForDeferredReply(items[i]) {
				continue
			}
			ret = append(ret, items[i])
			if len(ret) == limit {
				return ret
			}
		}
		if len(items) < limit {
			break
		}
	}
	return ret
}

func (s *channelMessageOutboxService) handoffNoticeWaitsForDeferredReply(outbox models.ChannelMessageOutbox) bool {
	message := repositories.MessageRepository.Get(sqls.DB(), outbox.MessageID)
	if message == nil || !strings.HasPrefix(strings.TrimSpace(message.ClientMsgID), "ai_handoff_success_") {
		return false
	}
	requestID := strings.TrimSpace(message.RequestID)
	if requestID == "" || outbox.ID <= 0 {
		return false
	}
	items := repositories.ChannelMessageOutboxRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("channel_type", strings.TrimSpace(outbox.ChannelType)).
		Eq("conversation_id", outbox.ConversationID).
		Lt("id", outbox.ID).
		In("send_status", []string{
			string(enums.ChannelMessageOutboxStatusPending),
			string(enums.ChannelMessageOutboxStatusSending),
			string(enums.ChannelMessageOutboxStatusFailed),
		}).
		Asc("id"))
	for i := range items {
		if !s.isReplyBeforeDeferredHandoff(items[i]) {
			continue
		}
		if strings.TrimSpace(items[i].SendStatus) == string(enums.ChannelMessageOutboxStatusFailed) && items[i].NextRetryAt == nil {
			continue
		}
		answer := repositories.MessageRepository.Get(sqls.DB(), items[i].MessageID)
		if answer != nil && strings.TrimSpace(answer.RequestID) == requestID {
			return true
		}
	}
	return false
}
