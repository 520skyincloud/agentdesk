package repositories

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestChannelMessageOutboxRepositorySQLite(t *testing.T) {
	dsn := fmt.Sprintf("file:outbox_repository_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), outboxRepositoryGormConfig())
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	runChannelMessageOutboxRepositoryContract(t, db)
}

func TestChannelMessageOutboxRepositoryMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), outboxRepositoryGormConfig())
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	runChannelMessageOutboxRepositoryContract(t, db)
}

func runChannelMessageOutboxRepositoryContract(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Migrator().DropTable(&models.ChannelMessageOutbox{}, &models.Message{}); err != nil {
		t.Fatalf("drop outbox contract tables: %v", err)
	}
	if err := db.AutoMigrate(&models.Message{}, &models.ChannelMessageOutbox{}); err != nil {
		t.Fatalf("migrate outbox contract tables: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Migrator().DropTable(&models.ChannelMessageOutbox{}, &models.Message{}); err != nil {
			t.Errorf("cleanup outbox contract tables: %v", err)
		}
	})

	now := time.Now()
	message := &models.Message{
		TenantID:            101,
		ConversationID:      201,
		SessionNo:           1,
		ClientMsgID:         "outbox-contract-message",
		SenderType:          enums.IMSenderTypeAI,
		MessageType:         enums.IMMessageTypeText,
		Content:             "contract",
		SeqNo:               1,
		SendStatus:          enums.IMMessageStatusSent,
		OutboundChannelType: enums.ChannelTypeWxWorkCLI,
		SentAt:              &now,
		AuditFields:         models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create marked message: %v", err)
	}
	unmarked := &models.Message{
		TenantID:       101,
		ConversationID: 201,
		SessionNo:      1,
		ClientMsgID:    "outbox-contract-self-echo",
		RequestID:      "wx_protocol_self_echo",
		SenderType:     enums.IMSenderTypeAgent,
		MessageType:    enums.IMMessageTypeText,
		Content:        "self echo",
		SeqNo:          2,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(unmarked).Error; err != nil {
		t.Fatalf("create unmarked message: %v", err)
	}

	missing, err := MessageRepository.FindMissingOutboundOutbox(db, 10)
	if err != nil {
		t.Fatalf("find missing outbox: %v", err)
	}
	if len(missing) != 1 || missing[0].ID != message.ID {
		t.Fatalf("missing messages=%+v want only %d", missing, message.ID)
	}

	candidate := models.ChannelMessageOutbox{
		TenantID:       message.TenantID,
		ChannelType:    message.OutboundChannelType,
		ConversationID: message.ConversationID,
		MessageID:      message.ID,
		Payload:        `{"messageId":1}`,
		SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	created, err := ChannelMessageOutboxRepository.CreateIfAbsent(db, &candidate)
	if err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	if !created {
		t.Fatal("first outbox insert must create a row")
	}
	duplicate := candidate
	duplicate.ID = 0
	created, err = ChannelMessageOutboxRepository.CreateIfAbsent(db, &duplicate)
	if err != nil {
		t.Fatalf("repeat outbox create: %v", err)
	}
	if created {
		t.Fatal("duplicate outbox insert must be ignored")
	}

	missing, err = MessageRepository.FindMissingOutboundOutbox(db, 10)
	if err != nil {
		t.Fatalf("find missing outbox after insert: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing messages after insert=%+v want none", missing)
	}
}

func outboxRepositoryGormConfig() *gorm.Config {
	return &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	}
}
