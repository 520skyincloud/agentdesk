package contextcompiler

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestBuildHistoryTurnsUsesRawVisibleContentAndCompleteTurns(t *testing.T) {
	now := time.Now()
	items := []models.Message{
		historyFixtureMessage(1, 1, enums.IMSenderTypeAI, "orphan", now),
		historyFixtureMessage(2, 2, enums.IMSenderTypeCustomer, "停车在哪", now),
		historyFixtureMessage(3, 3, enums.IMSenderTypeAI, "入口在东侧", now),
		historyFixtureMessage(4, 4, enums.IMSenderTypeCustomer, "早餐呢", now),
		historyFixtureMessage(5, 5, enums.IMSenderTypeSystem, "欢迎语", now),
	}
	turns := BuildHistoryTurns(items, nil, ConservativeEstimator{}, "")
	if len(turns) != 1 {
		t.Fatalf("turns=%d want=1", len(turns))
	}
	messages := historyTurnMessages(turns[0])
	if len(messages) != 2 || messages[0].Content != "停车在哪" {
		t.Fatalf("messages=%+v", messages)
	}
	// 契约 4.6/4.15：历史 AI 回复必须带非权威标签，且保留原文。
	if !strings.Contains(messages[1].Content, "[NON_AUTHORITATIVE_ASSISTANT_HISTORY]") || !strings.Contains(messages[1].Content, "入口在东侧") {
		t.Fatalf("historical assistant must carry non-authoritative label: %q", messages[1].Content)
	}
	for _, message := range messages {
		if strings.Contains(message.Content, "[客户]") || strings.Contains(message.Content, "AI客服") || strings.Contains(message.Content, "202") {
			t.Fatalf("history leaked sender/time label: %q", message.Content)
		}
	}
}

func historyFixtureMessage(id, seq int64, sender enums.IMSenderType, content string, at time.Time) models.Message {
	return models.Message{
		ID: id, TenantID: 1, ConversationID: 2, SessionNo: 1, SeqNo: seq,
		SenderType: sender, MessageType: enums.IMMessageTypeText, Content: content,
		SendStatus: enums.IMMessageStatusSent, AuditFields: models.AuditFields{CreatedAt: at, UpdatedAt: at},
	}
}
