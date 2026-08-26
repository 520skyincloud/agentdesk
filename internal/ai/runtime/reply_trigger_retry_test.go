package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	runtimeexecutor "agent-desk/internal/ai/runtime/executor"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	svc "agent-desk/internal/services"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestTriggerReplyWithProtocolRetryRetriesThenSucceeds(t *testing.T) {
	db := setupReplyTriggerRetryTestDB(t)
	service := newAIReplyService()
	conversation, message := seedReplyTriggerRetryAttempt(t, db, 9101)
	attempts := 0

	runCount, err := service.triggerReplyWithProtocolRetry(
		context.Background(),
		conversation.ID,
		message.ID,
		time.Second,
		0,
		func(_ context.Context, currentConversation models.Conversation, currentMessage models.Message, currentAgent models.AIAgent) error {
			attempts++
			if attempts == 1 {
				return fmt.Errorf("%w: missing content", runtimeexecutor.ErrGeneratedReplyProtocol)
			}
			_, err := svc.MessageService.SendAIMessageWithRequestIDAndSourceMessageID(
				currentConversation.ID,
				currentAgent.ID,
				fmt.Sprintf("reply-retry-%d", currentMessage.ID),
				enums.IMMessageTypeText,
				"早餐供应到上午十点。",
				"",
				&dto.AuthPrincipal{Username: "runtime-test", Nickname: "runtime-test"},
				currentMessage.RequestID,
				currentMessage.ID,
			)
			return err
		},
	)
	if err != nil || runCount != 2 || attempts != 2 {
		t.Fatalf("protocol failure must retry once and succeed, runCount=%d attempts=%d err=%v", runCount, attempts, err)
	}
	var committed int64
	if err := db.Model(&models.Message{}).
		Where("conversation_id = ? AND sender_type = ?", conversation.ID, enums.IMSenderTypeAI).
		Count(&committed).Error; err != nil {
		t.Fatalf("count committed AI replies: %v", err)
	}
	if committed != 1 {
		t.Fatalf("retry must commit exactly one customer reply, got %d", committed)
	}
}

func TestTriggerReplyWithProtocolRetryDoesNotRetryOrdinaryFailure(t *testing.T) {
	db := setupReplyTriggerRetryTestDB(t)
	service := newAIReplyService()
	conversation, message := seedReplyTriggerRetryAttempt(t, db, 9102)
	attempts := 0

	runCount, err := service.triggerReplyWithProtocolRetry(
		context.Background(),
		conversation.ID,
		message.ID,
		time.Second,
		0,
		func(context.Context, models.Conversation, models.Message, models.AIAgent) error {
			attempts++
			return fmt.Errorf("upstream unavailable")
		},
	)
	if err == nil || runCount != 1 || attempts != 1 {
		t.Fatalf("ordinary failure must not be retried, runCount=%d attempts=%d err=%v", runCount, attempts, err)
	}
}

func TestTriggerReplyWithProtocolRetryStopsAfterManualTakeover(t *testing.T) {
	db := setupReplyTriggerRetryTestDB(t)
	service := newAIReplyService()
	conversation, message := seedReplyTriggerRetryAttempt(t, db, 9103)
	attempts := 0

	runCount, err := service.triggerReplyWithProtocolRetry(
		context.Background(),
		conversation.ID,
		message.ID,
		time.Second,
		0,
		func(context.Context, models.Conversation, models.Message, models.AIAgent) error {
			attempts++
			if err := db.Model(&models.ConversationRouteState{}).
				Where("conversation_id = ?", conversation.ID).
				Updates(map[string]any{
					"route_status": enums.ConversationRouteStatusStoreWecomManual,
					"route_target": "store_wecom",
				}).Error; err != nil {
				t.Fatalf("switch route to manual: %v", err)
			}
			return fmt.Errorf("%w: missing content", runtimeexecutor.ErrGeneratedReplyProtocol)
		},
	)
	if err != nil || runCount != 1 || attempts != 1 {
		t.Fatalf("manual takeover must cancel the retry, runCount=%d attempts=%d err=%v", runCount, attempts, err)
	}
}

func setupReplyTriggerRetryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := "reply_trigger_retry_" + strings.NewReplacer("/", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.AIManualResumeTask{},
		&models.ConversationReadState{},
		&models.ConversationEventLog{},
		&models.Message{},
		&models.AIAgent{},
	); err != nil {
		t.Fatalf("migrate reply trigger retry fixtures: %v", err)
	}
	sqls.SetDB(db)
	return db
}

func seedReplyTriggerRetryAttempt(t *testing.T, db *gorm.DB, conversationID int64) (models.Conversation, models.Message) {
	t.Helper()
	agent := models.AIAgent{
		ID:                  conversationID + 1000,
		Name:                "runtime-test",
		Status:              enums.StatusOk,
		ServiceMode:         enums.IMConversationServiceModeAIFirst,
		ReplyTimeoutSeconds: 1,
	}
	if err := db.Create(&agent).Error; err != nil {
		t.Fatalf("create AI agent: %v", err)
	}
	conversation := models.Conversation{
		ID:        conversationID,
		AIAgentID: agent.ID,
		Status:    enums.IMConversationStatusAIServing,
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		ConversationID: conversation.ID,
		RouteStatus:    enums.ConversationRouteStatusAIServing,
		RouteTarget:    "ai",
		SessionNo:      1,
	}).Error; err != nil {
		t.Fatalf("create route state: %v", err)
	}
	now := time.Now()
	message := models.Message{
		ID:             conversationID + 2000,
		ConversationID: conversation.ID,
		RequestID:      fmt.Sprintf("req-%d", conversationID),
		ClientMsgID:    fmt.Sprintf("msg-%d", conversationID),
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeText,
		Content:        "请问早餐几点",
		SentAt:         &now,
	}
	if err := db.Create(&message).Error; err != nil {
		t.Fatalf("create customer message: %v", err)
	}
	return conversation, message
}
