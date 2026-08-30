package adapter

import (
	"regexp"
	"strconv"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/cloudwego/eino/schema"
	"github.com/mlogclub/simple/sqls"
)

const defaultHistoryLimit = 15

var runtimeCurrentTurnSourcePattern = regexp.MustCompile(`^\s*(?:\d+\s*[.．、]\s*)?\[(消息|文字|图片|语音|文件|定位|小程序|表情|视频|动画表情)(\d*)\]\s*`)

type CurrentTurnSource struct {
	Ref         string
	MessageID   int64
	MessageType enums.IMMessageType
	Text        string
}

type HistoryBuildResult struct {
	Messages        []*schema.Message
	RawItems        []models.Message
	LatestRawItem   *models.Message
	MemoryMessage   *schema.Message
	MemorySource    string
	MemoryItemCount int
}

// BuildCurrentTurnSources preserves the physical customer-message identity
// behind the U1..Un references used by Intent. Burst envelopes created before
// message IDs were embedded remain readable, but only the final item can be
// tied back to the current physical message with certainty.
func BuildCurrentTurnSources(message models.Message) []CurrentTurnSource {
	content := strings.TrimSpace(message.Content)
	if !utils.IsRuntimeCustomerBurstEnvelope(content) {
		text := currentTurnSourceText(message)
		if text == "" {
			return nil
		}
		return []CurrentTurnSource{{
			Ref:         "U1",
			MessageID:   message.ID,
			MessageType: message.MessageType,
			Text:        text,
		}}
	}

	items := utils.RuntimeCustomerBurstItems(content)
	ret := make([]CurrentTurnSource, 0, len(items))
	for index, item := range items {
		text := utils.RuntimeCustomerBurstItemText(item)
		if text == "" {
			continue
		}
		messageType, messageID := parseRuntimeCurrentTurnSource(item)
		if messageID <= 0 && index == len(items)-1 {
			messageID = message.ID
		}
		if messageType == "" && index == len(items)-1 {
			messageType = message.MessageType
		}
		ret = append(ret, CurrentTurnSource{
			Ref:         "U" + strconv.Itoa(len(ret)+1),
			MessageID:   messageID,
			MessageType: messageType,
			Text:        text,
		})
	}
	return ret
}

func currentTurnSourceText(message models.Message) string {
	if message.MessageType == enums.IMMessageTypeVoice {
		mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
		if strings.TrimSpace(status) != "understood" {
			return ""
		}
		if text := strings.TrimSpace(mediaText); text != "" {
			return text
		}
		return strings.TrimSpace(mediaSummary)
	}
	return buildRuntimeMessageText(&message)
}

func parseRuntimeCurrentTurnSource(item string) (enums.IMMessageType, int64) {
	matches := runtimeCurrentTurnSourcePattern.FindStringSubmatch(strings.TrimSpace(item))
	if len(matches) != 3 {
		return "", 0
	}
	messageID, _ := strconv.ParseInt(matches[2], 10, 64)
	return runtimeCurrentTurnMessageType(matches[1]), messageID
}

func runtimeCurrentTurnMessageType(label string) enums.IMMessageType {
	switch strings.TrimSpace(label) {
	case "图片":
		return enums.IMMessageTypeImage
	case "语音":
		return enums.IMMessageTypeVoice
	case "文件":
		return enums.IMMessageTypeAttachment
	case "定位":
		return enums.IMMessageTypeLocation
	case "小程序":
		return enums.IMMessageTypeMiniProgram
	case "表情", "动画表情":
		return enums.IMMessageTypeGIF
	case "视频":
		return enums.IMMessageTypeVideo
	default:
		return enums.IMMessageTypeText
	}
}

func BuildHistoryMessages(conversationID int64, currentMessageID int64, limit int) HistoryBuildResult {
	if conversationID <= 0 {
		return HistoryBuildResult{}
	}
	if limit <= 0 {
		limit = configuredHistoryLimit(conversationID)
	}
	sessionNo := currentSessionNo(currentMessageID)
	cnd := sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("session_no", sessionNo).
		Desc("id").
		Limit(limit + 1)
	if currentMessageID > 0 {
		cnd.Lt("id", currentMessageID)
	}
	items := repositories.MessageRepository.Find(sqls.DB(), cnd)
	oldestKeptMessageID := int64(0)
	if len(items) > 0 {
		oldestKeptMessageID = items[len(items)-1].ID
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	ret := HistoryBuildResult{
		Messages: make([]*schema.Message, 0, len(items)),
		RawItems: make([]models.Message, 0, len(items)),
	}
	if len(items) > 0 {
		latest := items[len(items)-1]
		ret.LatestRawItem = &latest
	}
	for _, item := range items {
		msg := BuildSchemaMessage(&item)
		if msg == nil {
			continue
		}
		ret.RawItems = append(ret.RawItems, item)
		ret.Messages = append(ret.Messages, msg)
	}
	ret.MemoryMessage, ret.MemorySource, ret.MemoryItemCount = buildConversationMemoryMessage(conversationID, sessionNo, oldestKeptMessageID)
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

func buildConversationMemoryMessage(conversationID int64, sessionNo int, beforeMessageID int64) (*schema.Message, string, int) {
	if conversationID <= 0 || sessionNo <= 0 || beforeMessageID <= 0 {
		return nil, "", 0
	}
	conversation := repositories.ConversationRepository.Get(sqls.DB(), conversationID)
	storeID, instanceID := resolveConversationStoreScope(conversationID)
	cnd := sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("session_no", sessionNo).
		Eq("status", 0).
		Desc("last_message_id").
		Desc("id")
	if conversation != nil && conversation.CustomerID > 0 {
		cnd.Eq("customer_id", conversation.CustomerID)
	}
	if storeID > 0 {
		cnd.Eq("store_id", storeID)
	}
	if instanceID > 0 {
		cnd.Eq("wx_work_instance_id", instanceID)
	}
	if summary := repositories.ConversationSessionSummaryRepository.FindOne(sqls.DB(), cnd); summary != nil {
		if text := buildSummaryMemoryText(summary); text != "" {
			return schema.SystemMessage(text), "conversation_session_summary", summary.MessageCount
		}
	}
	return nil, "", 0
}

func buildSummaryMemoryText(summary *models.ConversationSessionSummary) string {
	if summary == nil {
		return ""
	}
	parts := make([]string, 0, 5)
	if text := strings.TrimSpace(summary.StableFacts); text != "" {
		parts = append(parts, "稳定事实："+text)
	}
	if text := strings.TrimSpace(summary.OpenIssues); text != "" {
		parts = append(parts, "未解决事项："+text)
	}
	if text := strings.TrimSpace(summary.CustomerPreferences); text != "" {
		parts = append(parts, "客人偏好："+text)
	}
	if text := strings.TrimSpace(summary.MediaSummary); text != "" {
		parts = append(parts, "媒体理解："+text)
	}
	if len(parts) == 0 {
		return ""
	}
	return "以下是本会话更早消息的压缩记忆，只用于承接上下文；原始消息仍以数据库为准，不能把未完成动作说成已完成：\n" + strings.Join(parts, "\n")
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

func resolveConversationStoreScope(conversationID int64) (int64, int64) {
	db := sqls.DB()
	if db == nil {
		return 0, 0
	}
	state := repositories.ConversationRouteStateRepository.Take(db, "conversation_id = ?", conversationID)
	if state == nil {
		return 0, 0
	}
	return state.StoreID, state.WxWorkInstanceID
}

func BuildSchemaMessage(item *models.Message) *schema.Message {
	if item == nil {
		return nil
	}
	content := RuntimeHistoryMessageContent(item)
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

func RuntimeHistoryMessageContent(item *models.Message) string {
	if item == nil {
		return ""
	}
	if isStandaloneOneHistoryMessage(item) {
		return ""
	}
	content := buildRuntimeMessageText(item)
	if content == "" {
		return ""
	}
	parts := make([]string, 0, 3)
	parts = append(parts, "历史消息", RuntimeSpeakerLabel(item.SenderType))
	if timeLabel := RuntimeMessageTimeLabel(item); timeLabel != "" {
		parts = append(parts, timeLabel)
	}
	return "[" + strings.Join(parts, "][") + "] " + content
}

func isStandaloneOneHistoryMessage(message *models.Message) bool {
	if message == nil {
		return false
	}
	if utils.IsAIServiceNoticeMessage(message) {
		return true
	}
	if message.SenderType == enums.IMSenderTypeCustomer &&
		utils.IsStandaloneOneTextControl(message.MessageType, message.Content) {
		return true
	}
	return (message.SenderType == enums.IMSenderTypeAI || message.SenderType == enums.IMSenderTypeAgent) &&
		strings.HasPrefix(strings.TrimSpace(message.ClientMsgID), "ai_reply_faq_one_")
}

func RuntimeSpeakerLabel(sender enums.IMSenderType) string {
	switch sender {
	case enums.IMSenderTypeCustomer:
		return "客户"
	case enums.IMSenderTypeAI:
		return "AI客服"
	case enums.IMSenderTypeAgent:
		return "人工客服"
	default:
		return "未知发送方"
	}
}

func RuntimeMessageTimeLabel(item *models.Message) string {
	if item == nil {
		return ""
	}
	at := item.CreatedAt
	if item.SentAt != nil && !item.SentAt.IsZero() {
		at = *item.SentAt
	}
	if at.IsZero() {
		return ""
	}
	return at.Local().Format("2006-01-02 15:04:05")
}

func buildRuntimeMessageText(item *models.Message) string {
	if item == nil {
		return ""
	}
	text := utils.BuildRuntimeMessageTextWithPayload(item.MessageType, item.Content, item.Payload)
	_, _, mediaStatus := utils.RuntimeMediaUnderstandingFromPayload(item.Payload)
	if item.MessageType == enums.IMMessageTypeVoice && mediaStatus != "understood" {
		return ""
	}
	return strings.TrimSpace(text)
}
