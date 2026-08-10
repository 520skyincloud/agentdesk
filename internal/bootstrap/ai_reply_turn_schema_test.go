package bootstrap

import (
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestAIReplyTurnSchemaAutoMigrateSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), aiReplyJobSchemaGORMConfig("t_"))
	if err != nil {
		t.Fatal(err)
	}
	assertAIReplyTurnSchema(t, db)
}

func TestAIReplyTurnSchemaAutoMigrateMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	prefix := "art_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_"
	db, err := gorm.Open(mysql.Open(dsn), aiReplyJobSchemaGORMConfig(prefix))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Migrator().DropTable(&models.AIReplyTurnTask{}, &models.AIReplyTurn{}, &models.AIReplyJob{}, &models.Message{}, &models.Conversation{})
	})
	assertAIReplyTurnSchema(t, db)
}

func TestAIReplyTurnIsRegisteredWithoutConversationContent(t *testing.T) {
	assertRegisteredWithoutConversationContent(t, &models.AIReplyTurn{})
	assertRegisteredWithoutConversationContent(t, &models.AIReplyTurnTask{})
}

func assertAIReplyTurnSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&models.Conversation{}, &models.Message{}, &models.AIReplyTurn{}, &models.AIReplyTurnTask{}, &models.AIReplyJob{}); err != nil {
		t.Fatalf("AI reply turn AutoMigrate: %v", err)
	}
	if err := db.AutoMigrate(&models.Conversation{}, &models.Message{}, &models.AIReplyTurn{}, &models.AIReplyTurnTask{}, &models.AIReplyJob{}); err != nil {
		t.Fatalf("AI reply turn idempotent AutoMigrate: %v", err)
	}
	for _, target := range []struct {
		model  any
		column string
	}{
		{&models.Conversation{}, "current_ai_reply_turn_id"},
		{&models.Message{}, "ai_reply_turn_id"},
		{&models.Message{}, "ai_reply_turn_version"},
		{&models.AIReplyJob{}, "turn_id"},
		{&models.AIReplyJob{}, "turn_version"},
		{&models.AIReplyJob{}, "covered_by_message_id"},
		{&models.AIReplyJob{}, "covered_by_task_id"},
	} {
		if !db.Migrator().HasColumn(target.model, target.column) {
			t.Errorf("missing column %s", target.column)
		}
	}
	for _, column := range []string{
		"tenant_id", "conversation_id", "session_no", "store_id", "store_staff_binding_id", "version", "status", "terminal_reason",
		"first_customer_message_id", "last_customer_message_id", "first_customer_sent_at", "last_customer_sent_at",
		"last_committed_version", "last_delivered_version", "last_committed_request_id", "last_delivered_request_id",
		"last_delivered_at", "active_job_id", "lease_owner", "lease_expires_at", "completed_at",
	} {
		if !db.Migrator().HasColumn(&models.AIReplyTurn{}, column) {
			t.Errorf("missing AI reply turn column %s", column)
		}
	}
	for _, column := range []string{
		"tenant_id", "conversation_id", "session_no", "turn_id", "introduced_version", "source_message_id",
		"task_key", "sequence_no", "task_type", "intent", "sub_intent", "resource_action", "question_fingerprint",
		"stage", "status", "knowledge_status", "claimed_by_job_id", "claimed_version", "covered_by_task_id",
		"attempt_count", "knowledge_hit_count", "result_code", "committed_message_id", "next_retry_at", "completed_at",
	} {
		if !db.Migrator().HasColumn(&models.AIReplyTurnTask{}, column) {
			t.Errorf("missing AI reply turn task column %s", column)
		}
	}
	if !db.Migrator().HasIndex(&models.AIReplyTurnTask{}, "uk_ai_reply_turn_task") {
		t.Error("missing AI reply turn task stable-key index")
	}
	if !db.Migrator().HasIndex(&models.AIReplyTurnTask{}, "idx_ai_reply_turn_task_due") {
		t.Error("missing AI reply turn task due index")
	}
}

func assertRegisteredWithoutConversationContent(t *testing.T, target any) {
	t.Helper()
	targetType := reflect.TypeOf(target)
	registered := false
	for _, model := range models.Models {
		if reflect.TypeOf(model) == targetType {
			registered = true
			break
		}
	}
	if !registered {
		t.Fatalf("%s is not registered in models.Models", targetType.Elem().Name())
	}
	forbidden := []string{"content", "prompt", "output", "payload", "raw", "text"}
	modelType := targetType.Elem()
	for index := 0; index < modelType.NumField(); index++ {
		fieldName := strings.ToLower(modelType.Field(index).Name)
		for _, fragment := range forbidden {
			if strings.Contains(fieldName, fragment) {
				t.Errorf("%s field %s may persist conversation content", modelType.Name(), modelType.Field(index).Name)
			}
		}
	}
}
