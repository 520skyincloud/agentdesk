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

func TestAIRuntimeV2SchemaAutoMigrateSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), aiReplyJobSchemaGORMConfig("t_"))
	if err != nil {
		t.Fatal(err)
	}
	assertAIRuntimeV2Schema(t, db)
}

func TestAIRuntimeV2SchemaAutoMigrateMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	prefix := "arv2_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_"
	db, err := gorm.Open(mysql.Open(dsn), aiReplyJobSchemaGORMConfig(prefix))
	if err != nil {
		t.Fatal(err)
	}
	modelsToMigrate := aiRuntimeV2SchemaModels()
	t.Cleanup(func() {
		for index := len(modelsToMigrate) - 1; index >= 0; index-- {
			_ = db.Migrator().DropTable(modelsToMigrate[index])
		}
	})
	assertAIRuntimeV2Schema(t, db)
}

func TestAIRuntimeV2ModelsAreRegistered(t *testing.T) {
	for _, target := range []any{
		&models.MessageAnalysis{},
		&models.ConversationDialogueState{},
		&models.AIReplyTurnAction{},
	} {
		targetType := reflect.TypeOf(target)
		registered := false
		for _, model := range models.Models {
			if reflect.TypeOf(model) == targetType {
				registered = true
				break
			}
		}
		if !registered {
			t.Errorf("%s is not registered in models.Models", targetType.Elem().Name())
		}
	}
}

func assertAIRuntimeV2Schema(t *testing.T, db *gorm.DB) {
	t.Helper()
	modelsToMigrate := aiRuntimeV2SchemaModels()
	if err := db.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatalf("AI runtime V2 AutoMigrate: %v", err)
	}
	if err := db.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatalf("AI runtime V2 idempotent AutoMigrate: %v", err)
	}
	for _, target := range []struct {
		model any
		index string
	}{
		{&models.MessageAnalysis{}, "uk_message_analysis"},
		{&models.MessageAnalysis{}, "idx_message_analysis_status_at"},
		{&models.ConversationDialogueState{}, "uk_dialogue_state"},
		{&models.ConversationDialogueState{}, "idx_dialogue_state_message"},
		{&models.AIReplyTurnAction{}, "uk_turn_action"},
		{&models.AIReplyTurnAction{}, "idx_turn_action_status_at"},
		{&models.AIReplyTurnAction{}, "idx_turn_action_message"},
		{&models.AIReplyTurnAction{}, "idx_turn_action_outbox"},
	} {
		if !db.Migrator().HasIndex(target.model, target.index) {
			t.Errorf("missing index %s", target.index)
		}
	}
	for _, target := range []struct {
		model  any
		column string
	}{
		{&models.MessageAnalysis{}, "analysis_json"},
		{&models.MessageAnalysis{}, "content_fingerprint"},
		{&models.ConversationDialogueState{}, "snapshot_json"},
		{&models.ConversationDialogueState{}, "revision"},
		{&models.AIReplyTurnAction{}, "prepared_revision"},
		{&models.AIReplyTurnAction{}, "committed_message_id"},
		{&models.AIReplyTurnAction{}, "outbox_id"},
	} {
		if !db.Migrator().HasColumn(target.model, target.column) {
			t.Errorf("missing column %s", target.column)
		}
	}
}

func aiRuntimeV2SchemaModels() []any {
	return []any{
		&models.Conversation{},
		&models.Message{},
		&models.AIReplyTurn{},
		&models.AIReplyTurnTask{},
		&models.ChannelMessageOutbox{},
		&models.MessageAnalysis{},
		&models.ConversationDialogueState{},
		&models.AIReplyTurnAction{},
	}
}
