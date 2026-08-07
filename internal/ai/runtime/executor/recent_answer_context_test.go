package executor

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestRecentAnsweredTurnInstructionUsesImmediateAIBatchAndIgnoresSystem(t *testing.T) {
	now := time.Date(2026, time.August, 7, 10, 0, 0, 0, time.Local)
	history := adapter.HistoryBuildResult{RawItems: []models.Message{
		recentContextMessage(225, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "有速溶咖啡吗", "", "", now.Add(-2*time.Minute)),
		recentContextMessage(226, 1, enums.IMSenderTypeAI, enums.IMMessageTypeText, "有的，1313 房间对面的洗衣房提供速溶咖啡。", "", "coffee-answer", now.Add(-90*time.Second)),
		recentContextMessage(227, 1, enums.IMSenderTypeAI, enums.IMMessageTypeImage, "咖啡位置图", `{"assetId":"coffee-image"}`, "coffee-answer", now.Add(-89*time.Second)),
		recentContextMessage(228, 1, enums.IMSenderTypeSystem, enums.IMMessageTypeMiniProgram, "欢迎小程序", `{"appid":"wx-welcome"}`, "welcome", now.Add(-30*time.Second)),
	}}
	req := RunInput{UserMessage: recentContextMessage(229, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "有咖啡吗", "", "current", now)}

	instruction := buildRecentAnsweredTurnInstruction(req, history)
	for _, expected := range []string{"有速溶咖啡吗", "1313 房间对面的洗衣房", "结构化资源：图片"} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("recent answer instruction missing %q: %s", expected, instruction)
		}
	}
	if strings.Contains(instruction, "欢迎小程序") || strings.Contains(instruction, "wx-welcome") {
		t.Fatalf("system resource must not enter recent answered turn: %s", instruction)
	}
}

func TestRecentAnsweredTurnInstructionStopsAtConversationBoundaries(t *testing.T) {
	now := time.Date(2026, time.August, 7, 10, 0, 0, 0, time.Local)
	base := []models.Message{
		recentContextMessage(10, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "有咖啡吗", "", "", now.Add(-2*time.Minute)),
		recentContextMessage(11, 1, enums.IMSenderTypeAI, enums.IMMessageTypeText, "有速溶咖啡。", "", "answer-1", now.Add(-90*time.Second)),
	}
	tests := []struct {
		name    string
		history []models.Message
		current models.Message
	}{
		{
			name:    "agent message",
			history: append(append([]models.Message{}, base...), recentContextMessage(12, 1, enums.IMSenderTypeAgent, enums.IMMessageTypeText, "我来处理", "", "agent", now.Add(-30*time.Second))),
			current: recentContextMessage(13, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "还有吗", "", "current", now),
		},
		{
			name:    "new customer message",
			history: append(append([]models.Message{}, base...), recentContextMessage(12, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "停车呢", "", "customer", now.Add(-30*time.Second))),
			current: recentContextMessage(13, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "还有吗", "", "current", now),
		},
		{
			name:    "expired",
			history: base,
			current: recentContextMessage(13, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "还有吗", "", "current", now.Add(11*time.Minute)),
		},
		{
			name:    "new session",
			history: base,
			current: recentContextMessage(13, 2, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "还有吗", "", "current", now),
		},
		{
			name: "historical import",
			history: []models.Message{
				func() models.Message {
					message := recentContextMessage(10, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "历史问题", "", "", now.Add(-2*time.Minute))
					message.HistoricalOnly = true
					return message
				}(),
				func() models.Message {
					message := recentContextMessage(11, 1, enums.IMSenderTypeAI, enums.IMMessageTypeText, "历史回答", "", "answer-history", now.Add(-time.Minute))
					message.HistoricalOnly = true
					return message
				}(),
			},
			current: recentContextMessage(13, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "还有吗", "", "current", now),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			instruction := buildRecentAnsweredTurnInstruction(RunInput{UserMessage: tc.current}, adapter.HistoryBuildResult{RawItems: tc.history})
			if instruction != "" {
				t.Fatalf("expected boundary to disable recent answer instruction, got %q", instruction)
			}
		})
	}
}

func TestResolveClarifyKnowledgeProbeQueryRequiresResolvableReference(t *testing.T) {
	now := time.Date(2026, time.August, 7, 10, 0, 0, 0, time.Local)
	baseHistory := adapter.HistoryBuildResult{RawItems: []models.Message{
		recentContextMessage(20, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "有速溶咖啡吗", "", "", now.Add(-2*time.Minute)),
		recentContextMessage(21, 1, enums.IMSenderTypeAI, enums.IMMessageTypeText, "有的。", "", "answer-2", now.Add(-time.Minute)),
	}}

	concrete := RunInput{UserMessage: recentContextMessage(22, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "有现磨咖啡吗", "", "current", now)}
	if got := resolveClarifyKnowledgeProbeQuery(concrete, baseHistory); got != "有现磨咖啡吗" {
		t.Fatalf("concrete object query must probe directly, got %q", got)
	}

	referential := RunInput{UserMessage: recentContextMessage(22, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "这个呢", "", "current", now)}
	got := resolveClarifyKnowledgeProbeQuery(referential, baseHistory)
	if !strings.Contains(got, "有速溶咖啡吗") || !strings.Contains(got, "当前追问：这个呢") {
		t.Fatalf("expected uniquely resolved reference query, got %q", got)
	}

	ambiguousHistory := adapter.HistoryBuildResult{RawItems: []models.Message{
		recentContextMessage(30, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "咖啡有吗", "", "", now.Add(-3*time.Minute)),
		recentContextMessage(31, 1, enums.IMSenderTypeCustomer, enums.IMMessageTypeText, "停车收费吗", "", "", now.Add(-2*time.Minute)),
		recentContextMessage(32, 1, enums.IMSenderTypeAI, enums.IMMessageTypeText, "都已回答。", "", "answer-3", now.Add(-time.Minute)),
	}}
	if got := resolveClarifyKnowledgeProbeQuery(referential, ambiguousHistory); got != "" {
		t.Fatalf("ambiguous reference must stay clarify without knowledge probe, got %q", got)
	}
}

func recentContextMessage(id int64, sessionNo int, sender enums.IMSenderType, messageType enums.IMMessageType, content, payload, requestID string, createdAt time.Time) models.Message {
	sentAt := createdAt
	return models.Message{
		ID: id, TenantID: 1, ConversationID: 7, SessionNo: sessionNo, RequestID: requestID,
		SenderType: sender, MessageType: messageType, Content: content, Payload: payload,
		SendStatus: enums.IMMessageStatusSent, SentAt: &sentAt,
		AuditFields: models.AuditFields{CreatedAt: createdAt, UpdatedAt: createdAt},
	}
}
