package adapter

import (
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/cloudwego/eino/schema"
	"github.com/mlogclub/simple/sqls"
)

const defaultHistoryLimit = 30

type HistoryBuildResult struct {
	Messages []*schema.Message
	RawItems []models.Message
}

func BuildHistoryMessages(conversationID int64, currentMessageID int64, limit int) HistoryBuildResult {
	if conversationID <= 0 {
		return HistoryBuildResult{}
	}
	if limit <= 0 {
		limit = configuredHistoryLimit(conversationID)
	}
	items := repositories.MessageRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("session_no", currentSessionNo(currentMessageID)).
		Desc("id").
		Limit(limit+1))
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	ret := HistoryBuildResult{
		Messages: make([]*schema.Message, 0, len(items)),
		RawItems: make([]models.Message, 0, len(items)),
	}
	for _, item := range items {
		if item.ID == currentMessageID {
			continue
		}
		msg := BuildSchemaMessage(&item)
		if msg == nil {
			continue
		}
		ret.RawItems = append(ret.RawItems, item)
		ret.Messages = append(ret.Messages, msg)
	}
	return ret
}

func configuredHistoryLimit(conversationID int64) int {
	if conversationID <= 0 {
		return defaultHistoryLimit
	}
	state := repositories.ConversationRouteStateRepository.Take(sqls.DB(), "conversation_id = ?", conversationID)
	if state == nil || state.WxWorkInstanceID <= 0 {
		return defaultHistoryLimit
	}
	instance := repositories.WxWorkProtocolInstanceRepository.Get(sqls.DB(), state.WxWorkInstanceID)
	if instance == nil || instance.ContextMaxMessages <= 0 {
		return defaultHistoryLimit
	}
	if instance.ContextMaxMessages < 5 {
		return 5
	}
	if instance.ContextMaxMessages > 200 {
		return 200
	}
	return instance.ContextMaxMessages
}

func currentSessionNo(currentMessageID int64) int {
	if currentMessageID <= 0 {
		return 1
	}
	message := repositories.MessageRepository.Get(sqls.DB(), currentMessageID)
	if message == nil || message.SessionNo <= 0 {
		return 1
	}
	return message.SessionNo
}

func BuildSchemaMessage(item *models.Message) *schema.Message {
	if item == nil {
		return nil
	}
	content := buildRuntimeMessageText(item)
	if content == "" {
		return nil
	}
	switch item.SenderType {
	case enums.IMSenderTypeCustomer:
		return schema.UserMessage(content)
	case enums.IMSenderTypeAI, enums.IMSenderTypeAgent:
		return schema.AssistantMessage(content, nil)
	default:
		return nil
	}
}

func buildRuntimeMessageText(item *models.Message) string {
	if item == nil {
		return ""
	}
	text := utils.BuildRuntimeMessageTextWithPayload(item.MessageType, item.Content, item.Payload)
	_, _, mediaStatus := utils.RuntimeMediaUnderstandingFromPayload(item.Payload)
	if isMediaMessageType(item.MessageType) && mediaStatus != "" && mediaStatus != "understood" {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(text)
}

func isMediaMessageType(messageType enums.IMMessageType) bool {
	switch messageType {
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeVideo, enums.IMMessageTypeAttachment, enums.IMMessageTypeGIF:
		return true
	default:
		return false
	}
}
