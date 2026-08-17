package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestDuplicateResourceKnowledgeImageKeepsTextAndSuppressesImage(t *testing.T) {
	db := setupReplyResourcePolicyTestDB(t)
	now := time.Now()
	previous := createRecentAIResourceMessage(t, db, resourcePolicyFixture{
		ID: 100, MessageType: enums.IMMessageTypeImage, Payload: `{"assetId":"coffee-image"}`,
		RequestID: "previous-coffee", SentAt: now.Add(-time.Minute), OutboxStatus: enums.ChannelMessageOutboxStatusSent,
	})
	input := resourcePolicyInput(now, "有咖啡吗")
	replies := []structuredVariableReply{{ResourceType: "knowledge_image", MessageType: enums.IMMessageTypeImage, Payload: previous.Payload}}

	filtered, replyText := newReplyCommitService().applyRecentResourceDeliveryPolicy(input, replies, "有的，就是刚才说的速溶咖啡，放在1313房间对面的洗衣房。")
	if len(filtered) != 0 {
		t.Fatalf("expected duplicate knowledge image to be suppressed, got %#v", filtered)
	}
	if replyText != "有的，就是刚才说的速溶咖啡，放在1313房间对面的洗衣房。" {
		t.Fatalf("expected natural text answer to remain unchanged, got %q", replyText)
	}
	assertResourcePolicyTraceReason(t, input.Trace, "recent_duplicate_suppressed")
}

func TestDuplicateResourceWithinCurrentBatchKeepsOneMessageAndCoversOtherAction(t *testing.T) {
	setupReplyResourcePolicyTestDB(t)
	now := time.Now()
	input := resourcePolicyInput(now, "帮我看看图片")
	replies := []structuredVariableReply{
		{
			ResourceType: "knowledge_image", MessageType: enums.IMMessageTypeImage,
			Payload:   `{"assetId":"shared-knowledge-image"}`,
			ActionKey: "action-image-1", TaskKey: "task-image-1", PreparedRevision: "revision-1",
		},
		{
			ResourceType: "knowledge_image", MessageType: enums.IMMessageTypeImage,
			Payload:   `{"assetId":"shared-knowledge-image"}`,
			ActionKey: "action-image-2", TaskKey: "task-image-2", PreparedRevision: "revision-2",
		},
	}

	filtered, replyText, suppressions := newReplyCommitService().applyRecentResourceDeliveryPolicyDetailed(input, replies, "这是对应的图片。")
	if len(filtered) != 1 || filtered[0].ActionKey != "action-image-1" || replyText != "这是对应的图片。" {
		t.Fatalf("unexpected same-batch dedupe result: replies=%#v text=%q", filtered, replyText)
	}
	if len(suppressions) != 1 || suppressions[0].ActionKey != "action-image-2" ||
		suppressions[0].CoveredByActionKey != "action-image-1" || suppressions[0].CoveredByMessageID != 0 {
		t.Fatalf("same-batch suppression did not reference the kept action: %#v", suppressions)
	}
	assertResourcePolicyTraceReason(t, input.Trace, "same_batch_duplicate_suppressed")
}

// 生产消息 1452/1456-1458 回放：三个独立知识任务命中同一张用品图片时，
// Task 可以分别完成，但当前提交批次只能保留一个实际资源消息。
func TestProductionMessage1452SameKnowledgeImageCommitsOnce(t *testing.T) {
	setupReplyResourcePolicyTestDB(t)
	input := resourcePolicyInput(time.Now(), productionMessage1452TranscriptForResourcePolicy)
	replies := []structuredVariableReply{
		{ResourceType: "knowledge_image", MessageType: enums.IMMessageTypeImage, Payload: `{"assetId":"b97ee22c00d24d6d91e6716b68a1c522"}`, ActionKey: "action-1452-1", TaskKey: "task-1452-play", PreparedRevision: "revision-1"},
		{ResourceType: "knowledge_image", MessageType: enums.IMMessageTypeImage, Payload: `{"assetId":"b97ee22c00d24d6d91e6716b68a1c522"}`, ActionKey: "action-1452-2", TaskKey: "task-1452-room", PreparedRevision: "revision-2"},
		{ResourceType: "knowledge_image", MessageType: enums.IMMessageTypeImage, Payload: `{"assetId":"b97ee22c00d24d6d91e6716b68a1c522"}`, ActionKey: "action-1452-3", TaskKey: "task-1452-coffee", PreparedRevision: "revision-3"},
	}

	filtered, _, suppressions := newReplyCommitService().applyRecentResourceDeliveryPolicyDetailed(input, replies, "")
	if len(filtered) != 1 || filtered[0].ActionKey != "action-1452-1" {
		t.Fatalf("production 1452 image batch=%#v want one committed resource", filtered)
	}
	if len(suppressions) != 2 {
		t.Fatalf("production 1452 suppressions=%#v want two covered actions", suppressions)
	}
	for _, suppression := range suppressions {
		if suppression.CoveredByActionKey != "action-1452-1" || suppression.ResultCode != "same_batch_duplicate_suppressed" {
			t.Fatalf("production 1452 suppression is not linked to kept action: %#v", suppression)
		}
	}
}

const productionMessage1452TranscriptForResourcePolicy = "这附近有附近有什么地方好玩儿的呀，什么景点啊，好吃的之类的有没有啊？我就换个安静点的房间，别帮我换了吧，你就说有没有安静的房间吧。最后告诉我有什么酒店什么。好困，能不能搞点咖啡来呀？"

func TestExplicitResendAllowsDuplicateImageResource(t *testing.T) {
	db := setupReplyResourcePolicyTestDB(t)
	now := time.Now()
	previous := createRecentAIResourceMessage(t, db, resourcePolicyFixture{
		ID: 100, MessageType: enums.IMMessageTypeImage, Payload: `{"assetId":"coffee-image"}`,
		RequestID: "previous-image", SentAt: now.Add(-time.Minute), OutboxStatus: enums.ChannelMessageOutboxStatusSent,
	})
	input := resourcePolicyInput(now, "图片没看到，再发一下")
	replies := []structuredVariableReply{{ResourceType: "knowledge_image", MessageType: enums.IMMessageTypeImage, Payload: previous.Payload}}

	filtered, _ := newReplyCommitService().applyRecentResourceDeliveryPolicy(input, replies, "")
	if len(filtered) != 1 {
		t.Fatalf("expected explicit image resend to remain, got %#v", filtered)
	}
	assertResourcePolicyTraceReason(t, input.Trace, "explicit_resend_allowed")
}

func TestDuplicateResourcePendingDeliveryIsReusedAndRetryExpedited(t *testing.T) {
	for _, status := range []enums.ChannelMessageOutboxStatus{
		enums.ChannelMessageOutboxStatusPending,
		enums.ChannelMessageOutboxStatusSending,
		enums.ChannelMessageOutboxStatusFailed,
	} {
		t.Run(string(status), func(t *testing.T) {
			db := setupReplyResourcePolicyTestDB(t)
			now := time.Now()
			previous := createRecentAIResourceMessage(t, db, resourcePolicyFixture{
				ID: 100, MessageType: enums.IMMessageTypeImage, Payload: `{"assetId":"retry-image"}`,
				RequestID: "previous-retry", SentAt: now.Add(-time.Minute), OutboxStatus: status,
			})
			input := resourcePolicyInput(now, "图片没看到，再发一下")
			replies := []structuredVariableReply{{ResourceType: "knowledge_image", MessageType: enums.IMMessageTypeImage, Payload: previous.Payload}}

			filtered, replyText := newReplyCommitService().applyRecentResourceDeliveryPolicy(input, replies, "")
			if len(filtered) != 0 || !strings.Contains(replyText, "正在重新发送") {
				t.Fatalf("expected original delivery reuse with deterministic notice, replies=%#v text=%q", filtered, replyText)
			}
			assertResourcePolicyTraceReason(t, input.Trace, "pending_delivery_reused")

			var outbox models.ChannelMessageOutbox
			if err := db.First(&outbox, "message_id = ?", previous.ID).Error; err != nil {
				t.Fatalf("reload outbox: %v", err)
			}
			if status == enums.ChannelMessageOutboxStatusPending || status == enums.ChannelMessageOutboxStatusFailed {
				if outbox.NextRetryAt == nil || outbox.NextRetryAt.After(time.Now().Add(time.Second)) {
					t.Fatalf("expected retry to be expedited, got %#v", outbox.NextRetryAt)
				}
			}
		})
	}
}

func TestDuplicateResourceAmbiguousResendRequiresClarification(t *testing.T) {
	db := setupReplyResourcePolicyTestDB(t)
	now := time.Now()
	image := createRecentAIResourceMessage(t, db, resourcePolicyFixture{
		ID: 100, MessageType: enums.IMMessageTypeImage, Payload: `{"assetId":"coffee-image"}`,
		RequestID: "previous-multi", SentAt: now.Add(-time.Minute), OutboxStatus: enums.ChannelMessageOutboxStatusSent,
	})
	location := createRecentAIResourceMessage(t, db, resourcePolicyFixture{
		ID: 101, MessageType: enums.IMMessageTypeLocation, Payload: `{"longitude":117.2639,"latitude":31.824091,"address":"测试路 1 号"}`,
		RequestID: "previous-multi", SentAt: now.Add(-59 * time.Second), OutboxStatus: enums.ChannelMessageOutboxStatusSent,
	})
	input := resourcePolicyInput(now, "再发一下")
	replies := []structuredVariableReply{
		{ResourceType: "knowledge_image", MessageType: enums.IMMessageTypeImage, Payload: image.Payload},
		{ResourceType: "location", MessageType: enums.IMMessageTypeLocation, Payload: location.Payload},
	}

	filtered, replyText := newReplyCommitService().applyRecentResourceDeliveryPolicy(input, replies, "")
	if len(filtered) != 0 {
		t.Fatalf("ambiguous resend must not resend all resources, got %#v", filtered)
	}
	if !strings.Contains(replyText, "图片") || !strings.Contains(replyText, "定位") || !strings.Contains(replyText, "哪一个") {
		t.Fatalf("expected resource clarification, got %q", replyText)
	}
	assertResourcePolicyTraceReason(t, input.Trace, "ambiguous_resend_requires_clarification")
}

func TestDuplicateResourceLocationAndMiniProgramUseDeterministicFallbackText(t *testing.T) {
	tests := []struct {
		name         string
		messageType  enums.IMMessageType
		resourceType string
		payload      string
		wantText     string
	}{
		{
			name: "location", messageType: enums.IMMessageTypeLocation, resourceType: "location",
			payload:  `{"longitude":117.2639,"latitude":31.824091,"address":"测试路 1 号"}`,
			wantText: "刚才的定位还在上面，可以直接点开查看。",
		},
		{
			name: "mini program", messageType: enums.IMMessageTypeMiniProgram, resourceType: "mini_program",
			payload:  `{"appid":"wx-test","pagePath":"pages/checkin/index?room=1313&storeId=88"}`,
			wantText: "刚才的小程序卡片还在上面，可以直接点开。",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := setupReplyResourcePolicyTestDB(t)
			now := time.Now()
			previous := createRecentAIResourceMessage(t, db, resourcePolicyFixture{
				ID: 100, MessageType: tc.messageType, Payload: tc.payload,
				RequestID: "previous-resource", SentAt: now.Add(-time.Minute), OutboxStatus: enums.ChannelMessageOutboxStatusSent,
			})
			input := resourcePolicyInput(now, "再看一下")
			replies := []structuredVariableReply{{ResourceType: tc.resourceType, MessageType: tc.messageType, Payload: previous.Payload}}

			filtered, replyText := newReplyCommitService().applyRecentResourceDeliveryPolicy(input, replies, "")
			if len(filtered) != 0 || replyText != tc.wantText {
				t.Fatalf("unexpected duplicate resource result: replies=%#v text=%q", filtered, replyText)
			}
		})
	}
}

func TestDuplicateResourceIsScopedAndUsesLatestAIBatch(t *testing.T) {
	tests := []struct {
		name        string
		previous    resourcePolicyFixture
		addLatestAI bool
	}{
		{name: "other tenant", previous: resourcePolicyFixture{TenantID: 2}},
		{name: "other conversation", previous: resourcePolicyFixture{ConversationID: 99}},
		{name: "other session", previous: resourcePolicyFixture{SessionNo: 2}},
		{name: "historical import", previous: resourcePolicyFixture{HistoricalOnly: true}},
		{name: "expired", previous: resourcePolicyFixture{SentAtOffset: -11 * time.Minute}},
		{name: "latest ai batch has no resource", addLatestAI: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := setupReplyResourcePolicyTestDB(t)
			now := time.Now()
			fixture := tc.previous
			fixture.ID = 100
			fixture.MessageType = enums.IMMessageTypeImage
			fixture.Payload = `{"assetId":"scoped-image"}`
			fixture.RequestID = "previous-scoped"
			fixture.OutboxStatus = enums.ChannelMessageOutboxStatusSent
			if fixture.SentAt.IsZero() {
				fixture.SentAt = now.Add(-time.Minute + fixture.SentAtOffset)
			}
			previous := createRecentAIResourceMessage(t, db, fixture)
			if tc.addLatestAI {
				createRecentAIResourceMessage(t, db, resourcePolicyFixture{
					ID: 101, MessageType: enums.IMMessageTypeText, Content: "这是后一轮纯文本回复。",
					RequestID: "latest-text", SentAt: now.Add(-30 * time.Second), SkipOutbox: true,
				})
			}
			input := resourcePolicyInput(now, "有咖啡吗")
			replies := []structuredVariableReply{{ResourceType: "knowledge_image", MessageType: enums.IMMessageTypeImage, Payload: previous.Payload}}

			filtered, replyText := newReplyCommitService().applyRecentResourceDeliveryPolicy(input, replies, "")
			if len(filtered) != 1 || replyText != "" {
				t.Fatalf("out-of-scope or non-adjacent resource must not be suppressed, replies=%#v text=%q", filtered, replyText)
			}
		})
	}
}

func TestDuplicateResourceIgnoresSupersededCustomerMessageInSameTurn(t *testing.T) {
	db := setupReplyResourcePolicyTestDB(t)
	now := time.Now()
	previous := createRecentAIResourceMessage(t, db, resourcePolicyFixture{
		ID: 100, MessageType: enums.IMMessageTypeImage, Payload: `{"assetId":"same-turn-image"}`,
		RequestID: "previous-same-turn", SentAt: now.Add(-time.Minute), OutboxStatus: enums.ChannelMessageOutboxStatusSent,
		AIReplyTurnID: 77, AIReplyTurnVersion: 1,
	})
	intermediateSentAt := now.Add(-30 * time.Second)
	if err := db.Create(&models.Message{
		ID: 150, TenantID: 1, ConversationID: 10, SessionNo: 1,
		RequestID: "superseded-customer", ClientMsgID: "superseded-customer", SenderType: enums.IMSenderTypeCustomer,
		MessageType: enums.IMMessageTypeText, Content: "再问一次", SeqNo: 150, SendStatus: enums.IMMessageStatusSent,
		SentAt: &intermediateSentAt, AIReplyTurnID: 77, AIReplyTurnVersion: 2,
		AuditFields: models.AuditFields{CreatedAt: intermediateSentAt, UpdatedAt: intermediateSentAt},
	}).Error; err != nil {
		t.Fatal(err)
	}
	input := resourcePolicyInput(now, "图片还在吗")
	input.Message.AIReplyTurnID = 77
	input.Message.AIReplyTurnVersion = 3
	replies := []structuredVariableReply{{ResourceType: "knowledge_image", MessageType: enums.IMMessageTypeImage, Payload: previous.Payload}}

	filtered, _ := newReplyCommitService().applyRecentResourceDeliveryPolicy(input, replies, "已经说明过了。")
	if len(filtered) != 0 {
		t.Fatalf("same-turn superseded customer message must not break resource dedupe: %#v", filtered)
	}
}

type resourcePolicyFixture struct {
	ID                 int64
	TenantID           int64
	ConversationID     int64
	SessionNo          int
	MessageType        enums.IMMessageType
	Content            string
	Payload            string
	RequestID          string
	SentAt             time.Time
	SentAtOffset       time.Duration
	HistoricalOnly     bool
	OutboxStatus       enums.ChannelMessageOutboxStatus
	SkipOutbox         bool
	AIReplyTurnID      int64
	AIReplyTurnVersion int
}

func setupReplyResourcePolicyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := "reply_resource_policy_" + strings.NewReplacer("/", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Message{}, &models.ChannelMessageOutbox{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, dbErr := db.DB(); dbErr == nil {
			_ = raw.Close()
		}
	})
	return db
}

func createRecentAIResourceMessage(t *testing.T, db *gorm.DB, fixture resourcePolicyFixture) models.Message {
	t.Helper()
	if fixture.TenantID == 0 {
		fixture.TenantID = 1
	}
	if fixture.ConversationID == 0 {
		fixture.ConversationID = 10
	}
	if fixture.SessionNo == 0 {
		fixture.SessionNo = 1
	}
	if fixture.SentAt.IsZero() {
		fixture.SentAt = time.Now().Add(-time.Minute)
	}
	message := models.Message{
		ID: fixture.ID, TenantID: fixture.TenantID, ConversationID: fixture.ConversationID, SessionNo: fixture.SessionNo,
		HistoricalOnly: fixture.HistoricalOnly, RequestID: fixture.RequestID,
		ClientMsgID: fmt.Sprintf("previous-%d-%d-%d", fixture.TenantID, fixture.ConversationID, fixture.ID),
		SenderType:  enums.IMSenderTypeAI, SenderID: 9, MessageType: fixture.MessageType,
		Content: fixture.Content, Payload: fixture.Payload, SeqNo: fixture.ID,
		SendStatus: enums.IMMessageStatusSent, SentAt: &fixture.SentAt,
		OutboundChannelType: enums.ChannelTypeWxWorkProtocol,
		AIReplyTurnID:       fixture.AIReplyTurnID, AIReplyTurnVersion: fixture.AIReplyTurnVersion,
		AuditFields: models.AuditFields{CreatedAt: fixture.SentAt, UpdatedAt: fixture.SentAt},
	}
	if err := db.Create(&message).Error; err != nil {
		t.Fatalf("create previous message: %v", err)
	}
	if fixture.SkipOutbox {
		return message
	}
	nextRetryAt := time.Now().Add(time.Hour)
	outbox := models.ChannelMessageOutbox{
		TenantID: message.TenantID, ChannelType: enums.ChannelTypeWxWorkProtocol,
		ConversationID: message.ConversationID, MessageID: message.ID,
		SendStatus: string(fixture.OutboxStatus), NextRetryAt: &nextRetryAt,
		AuditFields: models.AuditFields{CreatedAt: fixture.SentAt, UpdatedAt: fixture.SentAt},
	}
	if err := db.Create(&outbox).Error; err != nil {
		t.Fatalf("create previous outbox: %v", err)
	}
	return message
}

func resourcePolicyInput(now time.Time, content string) replyCommitInput {
	return replyCommitInput{
		Conversation: models.Conversation{ID: 10, TenantID: 1, StoreID: 88},
		Message: models.Message{
			ID: 200, TenantID: 1, ConversationID: 10, SessionNo: 1,
			SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: content,
			SendStatus: enums.IMMessageStatusSent, SentAt: &now,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		},
		Trace:        &aiReplyTraceData{Runtime: json.RawMessage(`{}`)},
		ClientPrefix: "ai_reply",
	}
}

func assertResourcePolicyTraceReason(t *testing.T, trace *aiReplyTraceData, want string) {
	t.Helper()
	data := struct {
		ActionLedger struct {
			SuppressedActions []struct {
				Reason string `json:"reason"`
			} `json:"suppressedActions"`
		} `json:"actionLedger"`
	}{}
	if trace == nil || json.Unmarshal(trace.Runtime, &data) != nil {
		t.Fatalf("unmarshal resource policy trace: %s", trace.Runtime)
	}
	for _, item := range data.ActionLedger.SuppressedActions {
		if item.Reason == want {
			return
		}
	}
	t.Fatalf("resource policy trace missing reason %q: %s", want, trace.Runtime)
}
