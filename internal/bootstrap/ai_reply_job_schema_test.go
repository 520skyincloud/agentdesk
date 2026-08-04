package bootstrap

import (
	"os"
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

func TestAIReplyJobSchemaAutoMigrateSQLite(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"),
		aiReplyJobSchemaGORMConfig("t_"),
	)
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	assertAIReplyJobSchema(t, db)
}

func TestAIReplyJobSchemaAutoMigrateMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	prefix := "arj_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_"
	db, err := gorm.Open(mysql.Open(dsn), aiReplyJobSchemaGORMConfig(prefix))
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Migrator().DropTable(&models.AIReplyJob{}); err != nil {
			t.Errorf("drop MySQL AI reply job fixture: %v", err)
		}
	})
	assertAIReplyJobSchema(t, db)
}

func assertAIReplyJobSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&models.AIReplyJob{}); err != nil {
		t.Fatalf("AI reply job AutoMigrate: %v", err)
	}
	if err := db.AutoMigrate(&models.AIReplyJob{}); err != nil {
		t.Fatalf("AI reply job idempotent AutoMigrate: %v", err)
	}
	if !db.Migrator().HasTable(&models.AIReplyJob{}) {
		t.Fatal("AI reply job table was not created")
	}
	if !db.Migrator().HasIndex(&models.AIReplyJob{}, "uk_ai_reply_job_message") {
		t.Fatal("AI reply job message unique index was not created")
	}
	for _, column := range []string{
		"tenant_id", "conversation_id", "message_id", "session_no", "store_id", "store_staff_binding_id",
		"request_id", "trigger_kind", "status", "attempt_count", "next_retry_at", "expires_at",
		"lease_owner", "lease_expires_at", "result_code", "last_error_class", "started_at", "completed_at",
	} {
		if !db.Migrator().HasColumn(&models.AIReplyJob{}, column) {
			t.Errorf("AI reply job column %s was not created", column)
		}
	}
}

func aiReplyJobSchemaGORMConfig(prefix string) *gorm.Config {
	return &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   prefix,
			SingularTable: true,
		},
	}
}
