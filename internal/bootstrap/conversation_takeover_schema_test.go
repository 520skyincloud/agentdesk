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
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestConversationTakeoverSchemaAutoMigrateSQLite(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"),
		conversationTakeoverSchemaGORMConfig("t_"),
	)
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	assertConversationTakeoverSchema(t, db)
}

func TestConversationTakeoverSchemaAutoMigrateMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	prefix := "ctr_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_"
	db, err := gorm.Open(mysql.Open(dsn), conversationTakeoverSchemaGORMConfig(prefix))
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Migrator().DropTable(&models.ConversationTakeoverRequest{}); err != nil {
			t.Errorf("drop MySQL conversation takeover fixture: %v", err)
		}
	})
	assertConversationTakeoverSchema(t, db)
}

func TestConversationTakeoverModelIsRegistered(t *testing.T) {
	targetType := reflect.TypeOf(&models.ConversationTakeoverRequest{})
	for _, model := range models.Models {
		if reflect.TypeOf(model) == targetType {
			return
		}
	}
	t.Fatalf("%s is not registered in models.Models", targetType.Elem().Name())
}

func assertConversationTakeoverSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&models.ConversationTakeoverRequest{}); err != nil {
		t.Fatalf("conversation takeover AutoMigrate: %v", err)
	}
	if err := db.AutoMigrate(&models.ConversationTakeoverRequest{}); err != nil {
		t.Fatalf("conversation takeover idempotent AutoMigrate: %v", err)
	}
	if !db.Migrator().HasTable(&models.ConversationTakeoverRequest{}) {
		t.Fatal("conversation takeover request table was not created")
	}
	if !db.Migrator().HasIndex(&models.ConversationTakeoverRequest{}, "uk_conversation_takeover_active") {
		t.Fatal("conversation takeover active request unique index was not created")
	}
	for _, column := range []string{
		"tenant_id", "conversation_id", "session_no", "team_id", "requester_user_id",
		"source_assignee_id", "source_route_status", "reason", "status",
		"reviewer_user_id", "reviewed_at", "terminal_reason", "active_key",
	} {
		if !db.Migrator().HasColumn(&models.ConversationTakeoverRequest{}, column) {
			t.Errorf("conversation takeover column %s was not created", column)
		}
	}
}

func conversationTakeoverSchemaGORMConfig(prefix string) *gorm.Config {
	return &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   prefix,
			SingularTable: true,
		},
	}
}
