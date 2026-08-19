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

const defaultHistoryLimit = 15
const maxStoreCustomerMemoryRunes = 1200

type HistoryBuildResult struct {
	Messages        []*schema.Message
	RawItems        []models.Message
	MemoryMessage   *schema.Message
	Memory          *models.ConversationSessionSummary
	MemorySource    string
	MemoryItemCount int
}

func BuildHistoryMessages(conversationID, currentMessageID, tenantID int64, limit int) HistoryBuildResult {
	if conversationID <= 0 {
		return HistoryBuildResult{}
	}
	if limit <= 0 {
		limit = configuredHistoryLimit(conversationID, tenantID)
	}
	sessionNo := currentSessionNo(currentMessageID, tenantID)
	items := findHistoryMessages(conversationID, currentMessageID, tenantID, sessionNo, limit+1)
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
	for _, item := range items {
		msg := BuildSchemaMessage(&item)
		if msg == nil {
			continue
		}
		ret.RawItems = append(ret.RawItems, item)
		ret.Messages = append(ret.Messages, msg)
	}
	ret.MemoryMessage, ret.MemorySource, ret.MemoryItemCount, ret.Memory = buildConversationMemoryMessage(conversationID, tenantID, sessionNo, oldestKeptMessageID)
	return ret
}

func findHistoryMessages(conversationID, currentMessageID, tenantID int64, sessionNo, target int) []models.Message {
	if target <= 0 {
		return nil
	}
	const pageSize = 32
	items := make([]models.Message, 0, target)
	beforeID := currentMessageID
	for len(items) < target {
		cnd := sqls.NewCnd().
			Eq("conversation_id", conversationID).
			Eq("session_no", sessionNo).
			Desc("id").
			Limit(pageSize)
		if beforeID > 0 {
			cnd.Lt("id", beforeID)
		}
		if tenantID > 0 {
			cnd.Eq("tenant_id", tenantID)
		}
		page := repositories.MessageRepository.Find(sqls.DB(), cnd)
		if len(page) == 0 {
			break
		}
		for _, item := range page {
			if isStandaloneOneHistoryControl(item) {
				continue
			}
			items = append(items, item)
			if len(items) == target {
				break
			}
		}
		if len(page) < pageSize || len(items) == target {
			break
		}
		beforeID = page[len(page)-1].ID
	}
	return items
}

func isStandaloneOneHistoryControl(message models.Message) bool {
	if message.SenderType == enums.IMSenderTypeCustomer &&
		utils.IsStandaloneOneTextControl(message.MessageType, message.Content, message.AIReplyTurnID, message.AIReplyTurnVersion) {
		return true
	}
	return message.SenderType == enums.IMSenderTypeAI &&
		strings.HasPrefix(strings.TrimSpace(message.ClientMsgID), "ai_reply_faq_one_")
}

func configuredHistoryLimit(conversationID, tenantID int64) int {
	if conversationID <= 0 {
		return defaultHistoryLimit
	}
	state := repositories.ConversationRouteStateRepository.Take(sqls.DB(), "conversation_id = ?", conversationID)
	if tenantID > 0 {
		state = repositories.ConversationRouteStateRepository.Take(sqls.DB(), "conversation_id = ? AND tenant_id = ?", conversationID, tenantID)
	}
	if state == nil || state.WxWorkInstanceID <= 0 {
		return defaultHistoryLimit
	}
	instance := repositories.WxWorkProtocolInstanceRepository.GetActivatedCurrentInTenant(sqls.DB(), state.WxWorkInstanceID, tenantID)
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

func buildConversationMemoryMessage(conversationID, tenantID int64, sessionNo int, beforeMessageID int64) (*schema.Message, string, int, *models.ConversationSessionSummary) {
	if conversationID <= 0 || sessionNo <= 0 {
		return nil, "", 0, nil
	}
	conversation := repositories.ConversationRepository.Get(sqls.DB(), conversationID)
	if tenantID > 0 {
		conversation = repositories.ConversationRepository.GetInTenant(sqls.DB(), conversationID, tenantID)
	}
	storeID, instanceID := resolveConversationStoreScope(conversationID, tenantID)
	if conversation != nil && conversation.TenantID > 0 && conversation.CustomerID > 0 && storeID > 0 {
		if message, source, count, memory := buildStoreCustomerMemoryMessage(conversation, storeID, instanceID, sessionNo, beforeMessageID); message != nil {
			return message, source, count, memory
		}
	}
	if beforeMessageID <= 0 {
		return nil, "", 0, nil
	}
	cnd := sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("session_no", sessionNo).
		Eq("status", 0).
		Desc("last_message_id").
		Desc("id")
	if tenantID > 0 {
		cnd.Eq("tenant_id", tenantID)
	}
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
			memory := *summary
			return schema.SystemMessage(text), "conversation_session_summary", summary.MessageCount, &memory
		}
	}
	return nil, "", 0, nil
}

func buildStoreCustomerMemoryMessage(
	conversation *models.Conversation,
	storeID, instanceID int64,
	sessionNo int,
	beforeMessageID int64,
) (*schema.Message, string, int, *models.ConversationSessionSummary) {
	if conversation == nil || conversation.TenantID <= 0 || conversation.CustomerID <= 0 || storeID <= 0 {
		return nil, "", 0, nil
	}
	parts := make([]string, 0, 8)
	seen := make(map[string]struct{})
	messageCount := 0
	if relation := repositories.StoreCustomerRelationRepository.TakeByCustomerAndStoreInTenant(
		sqls.DB(), conversation.TenantID, conversation.CustomerID, storeID,
	); relation != nil {
		parts = appendBoundedStoreMemoryPart(parts, seen, "门店客户稳定备注："+strings.TrimSpace(relation.StableNotes))
	}

	summaries := repositories.ConversationSessionSummaryRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", conversation.TenantID).
		Eq("store_id", storeID).
		Eq("customer_id", conversation.CustomerID).
		Eq("status", enums.StatusOk).
		Desc("last_message_id").
		Desc("id").
		Limit(12))
	for i := range summaries {
		summary := &summaries[i]
		if summary.ConversationID == conversation.ID && summary.SessionNo == sessionNo {
			continue
		}
		body := buildSummaryMemoryBody(summary)
		if body == "" {
			continue
		}
		parts = appendBoundedStoreMemoryPart(parts, seen, "同门店历史会话记忆："+body)
		messageCount += summary.MessageCount
	}

	if beforeMessageID > 0 {
		current := repositories.ConversationSessionSummaryRepository.FindOne(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", conversation.TenantID).
			Eq("conversation_id", conversation.ID).
			Eq("session_no", sessionNo).
			Eq("store_id", storeID).
			Eq("customer_id", conversation.CustomerID).
			Eq("wx_work_instance_id", instanceID).
			Eq("status", enums.StatusOk).
			Desc("last_message_id").
			Desc("id"))
		if current != nil {
			parts = appendBoundedStoreMemoryPart(parts, seen, "本会话压缩记忆："+buildSummaryMemoryBody(current))
			messageCount += current.MessageCount
		}
	}
	if len(parts) == 0 {
		return nil, "", 0, nil
	}
	body := strings.Join(parts, "\n")
	content := "以下是该客户在当前门店的历史记忆，只用于承接上下文；不同门店的数据不得混用，原始消息仍以数据库为准：\n" + body
	memory := &models.ConversationSessionSummary{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, SessionNo: sessionNo,
		StoreID: storeID, CustomerID: conversation.CustomerID, WxWorkInstanceID: instanceID,
		StableFacts: body, MessageCount: messageCount,
	}
	return schema.SystemMessage(content), "store_customer_memory", messageCount, memory
}

func appendBoundedStoreMemoryPart(parts []string, seen map[string]struct{}, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return parts
	}
	if _, exists := seen[value]; exists {
		return parts
	}
	used := 0
	for _, part := range parts {
		used += len([]rune(part)) + 1
	}
	remaining := maxStoreCustomerMemoryRunes - used
	if remaining <= 0 {
		return parts
	}
	runes := []rune(value)
	if len(runes) > remaining {
		runes = runes[:remaining]
		value = strings.TrimSpace(string(runes))
	}
	if value == "" {
		return parts
	}
	seen[value] = struct{}{}
	return append(parts, value)
}

func buildSummaryMemoryText(summary *models.ConversationSessionSummary) string {
	body := buildSummaryMemoryBody(summary)
	if body == "" {
		return ""
	}
	return "以下是本会话更早消息的压缩记忆，只用于承接上下文；原始消息仍以数据库为准，不能把未完成动作说成已完成：\n" + body
}

func buildSummaryMemoryBody(summary *models.ConversationSessionSummary) string {
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
	return strings.Join(parts, "\n")
}

func currentSessionNo(currentMessageID, tenantID int64) int {
	if currentMessageID <= 0 {
		return 1
	}
	message := repositories.MessageRepository.Get(sqls.DB(), currentMessageID)
	if tenantID > 0 {
		message = repositories.MessageRepository.GetInTenant(sqls.DB(), currentMessageID, tenantID)
	}
	if message == nil || message.SessionNo <= 0 {
		return 1
	}
	return message.SessionNo
}

func resolveConversationStoreScope(conversationID, tenantID int64) (int64, int64) {
	db := sqls.DB()
	if db == nil {
		return 0, 0
	}
	state := repositories.ConversationRouteStateRepository.Take(db, "conversation_id = ?", conversationID)
	if tenantID > 0 {
		state = repositories.ConversationRouteStateRepository.Take(db, "conversation_id = ? AND tenant_id = ?", conversationID, tenantID)
	}
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
