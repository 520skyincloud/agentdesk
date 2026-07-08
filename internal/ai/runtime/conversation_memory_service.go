package runtime

import (
	"strings"
	"sync"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

type conversationMemoryService struct{}

const conversationMemoryIdleWindow = 24 * time.Hour
const conversationMemoryScanInterval = time.Hour
const targetConversationMemoryTokens = 40
const maxConversationMemoryTokens = 300

var conversationMemoryScannerOnce sync.Once

func newConversationMemoryService() *conversationMemoryService {
	service := &conversationMemoryService{}
	conversationMemoryScannerOnce.Do(func() {
		go service.RunIdleScanner()
	})
	return service
}

func (s *conversationMemoryService) RunIdleScanner() {
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	for {
		<-timer.C
		s.ScanIdleConversations()
		timer.Reset(conversationMemoryScanInterval)
	}
}

func (s *conversationMemoryService) ScanIdleConversations() {
	if sqls.DB() == nil {
		return
	}
	latest := make([]models.Message, 0)
	cutoff := time.Now().Add(-conversationMemoryIdleWindow)
	err := sqls.DB().Raw(`
SELECT m.* FROM t_message m
JOIN (
  SELECT conversation_id, session_no, MAX(id) AS max_id
  FROM t_message
  GROUP BY conversation_id, session_no
) latest ON latest.max_id = m.id
LEFT JOIN t_conversation_session_summary s
  ON s.conversation_id = m.conversation_id
 AND s.session_no = m.session_no
 AND s.last_message_id >= m.id
 AND s.status = ?
WHERE COALESCE(m.sent_at, m.created_at) <= ?
  AND s.id IS NULL
ORDER BY m.id ASC
LIMIT 50`, enums.StatusOk, cutoff).Scan(&latest).Error
	if err != nil {
		return
	}
	for _, message := range latest {
		conversation := repositories.ConversationRepository.Get(sqls.DB(), message.ConversationID)
		if conversation == nil {
			continue
		}
		_ = s.Update(*conversation, message)
	}
}

func (s *conversationMemoryService) ScheduleUpdate(conversation models.Conversation, triggerMessage models.Message) {
	if conversation.ID <= 0 || triggerMessage.ID <= 0 {
		return
	}
	go func() {
		time.Sleep(conversationMemoryIdleWindow)
		if !s.isStillIdle(conversation.ID, triggerMessage.ID, triggerMessage.SessionNo) {
			return
		}
		if err := s.Update(conversation, triggerMessage); err != nil {
			return
		}
	}()
}

func (s *conversationMemoryService) isStillIdle(conversationID int64, triggerMessageID int64, sessionNo int) bool {
	if sessionNo <= 0 {
		sessionNo = 1
	}
	latest := repositories.MessageRepository.FindOne(sqls.DB(), sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("session_no", sessionNo).
		Desc("id"))
	return latest != nil && latest.ID == triggerMessageID
}

func (s *conversationMemoryService) Update(conversation models.Conversation, triggerMessage models.Message) error {
	sessionNo := triggerMessage.SessionNo
	if sessionNo <= 0 {
		sessionNo = 1
	}
	storeID, instanceID := resolveRuntimeConversationScope(conversation.ID)
	items := repositories.MessageRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("conversation_id", conversation.ID).
		Eq("session_no", sessionNo).
		Desc("id").
		Limit(30))
	if len(items) == 0 {
		return nil
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	stableFacts, openIssues, preferences, mediaSummary := summarizeMemoryItems(items)
	now := time.Now()
	current := repositories.ConversationSessionSummaryRepository.FindOne(sqls.DB(), sqls.NewCnd().
		Eq("conversation_id", conversation.ID).
		Eq("session_no", sessionNo).
		Eq("store_id", storeID).
		Eq("customer_id", conversation.CustomerID).
		Eq("wx_work_instance_id", instanceID).
		Eq("status", enums.StatusOk))
	columns := map[string]any{
		"wx_work_instance_id":  instanceID,
		"store_id":             storeID,
		"customer_id":          conversation.CustomerID,
		"stable_facts":         stableFacts,
		"open_issues":          openIssues,
		"customer_preferences": preferences,
		"media_summary":        mediaSummary,
		"message_count":        len(items),
		"token_estimate":       estimateMemoryTokens(stableFacts + openIssues + preferences + mediaSummary),
		"last_message_id":      triggerMessage.ID,
		"status":               enums.StatusOk,
		"update_user_id":       constants.SystemAuditUserID,
		"update_user_name":     constants.SystemAuditUserName,
		"updated_at":           now,
	}
	if current != nil {
		return repositories.ConversationSessionSummaryRepository.Updates(sqls.DB(), current.ID, columns)
	}
	item := &models.ConversationSessionSummary{
		ConversationID:      conversation.ID,
		SessionNo:           sessionNo,
		WxWorkInstanceID:    instanceID,
		StoreID:             storeID,
		CustomerID:          conversation.CustomerID,
		StableFacts:         stableFacts,
		OpenIssues:          openIssues,
		CustomerPreferences: preferences,
		MediaSummary:        mediaSummary,
		MessageCount:        len(items),
		TokenEstimate:       estimateMemoryTokens(stableFacts + openIssues + preferences + mediaSummary),
		LastMessageID:       triggerMessage.ID,
		Status:              enums.StatusOk,
		AuditFields:         models.AuditFields{CreatedAt: now, UpdatedAt: now, CreateUserID: constants.SystemAuditUserID, UpdateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName, UpdateUserName: constants.SystemAuditUserName},
	}
	return repositories.ConversationSessionSummaryRepository.Create(sqls.DB(), item)
}

func summarizeMemoryItems(items []models.Message) (string, string, string, string) {
	stable := make([]string, 0, 2)
	issues := make([]string, 0, 2)
	media := make([]string, 0, 1)
	for _, item := range items {
		text := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(item.MessageType, item.Content, item.Payload))
		if text == "" {
			continue
		}
		if isMemoryMediaMessage(item.MessageType) {
			if _, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(item.Payload); status == "understood" && strings.TrimSpace(mediaSummary) != "" {
				media = appendLimitedMemoryLine(media, mediaSummary, 80, 1)
			}
		}
		if item.SenderType == enums.IMSenderTypeCustomer {
			if containsMemoryIssue(text) {
				issues = appendLimitedMemoryLine(issues, text, 80, 2)
			}
			if containsStableFact(text) {
				stable = appendLimitedMemoryLine(stable, text, 80, 2)
			}
		}
	}
	return trimMemoryText(strings.Join(stable, "\n")), trimMemoryText(strings.Join(issues, "\n")), "", trimMemoryText(strings.Join(media, "\n"))
}

func appendLimitedMemoryLine(list []string, text string, limit int, max int) []string {
	if len(list) >= max {
		return list
	}
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	runes := []rune(text)
	if len(runes) > limit {
		text = string(runes[:limit]) + "..."
	}
	return append(list, text)
}

func containsMemoryIssue(text string) bool {
	return strings.Contains(text, "还没") || strings.Contains(text, "打不开") || strings.Contains(text, "连不上") || strings.Contains(text, "坏") || strings.Contains(text, "投诉") || strings.Contains(text, "人工") || strings.Contains(text, "摔倒") || strings.Contains(text, "滑倒") || strings.Contains(text, "受伤") || strings.Contains(text, "流血")
}

func containsStableFact(text string) bool {
	if containsRoomNumberFact(text) {
		return false
	}
	return strings.Contains(text, "电话") || strings.Contains(text, "发票") || strings.Contains(text, "车牌") || strings.Contains(text, "入住")
}

func containsRoomNumberFact(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	return strings.Contains(text, "房号") || strings.Contains(text, "房间号") || strings.Contains(text, "我在") || strings.Contains(text, "住在")
}

func estimateMemoryTokens(text string) int {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return 0
	}
	estimated := len(runes)/2 + 1
	if estimated > maxConversationMemoryTokens {
		return maxConversationMemoryTokens
	}
	return estimated
}

func trimMemoryText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	maxRunes := maxConversationMemoryTokens * 2
	if maxRunes > targetConversationMemoryTokens*8 {
		maxRunes = targetConversationMemoryTokens * 8
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "..."
}

func isMemoryMediaMessage(messageType enums.IMMessageType) bool {
	switch messageType {
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeVideo, enums.IMMessageTypeAttachment, enums.IMMessageTypeGIF:
		return true
	default:
		return false
	}
}

func resolveRuntimeConversationScope(conversationID int64) (int64, int64) {
	state := repositories.ConversationRouteStateRepository.Take(sqls.DB(), "conversation_id = ?", conversationID)
	if state == nil {
		return 0, 0
	}
	return state.StoreID, state.WxWorkInstanceID
}
