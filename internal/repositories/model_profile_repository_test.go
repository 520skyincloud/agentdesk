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

func TestModelProfileSlotRepositoryPreservesExplicitRetryCountSQLite(t *testing.T) {
	dsn := fmt.Sprintf("file:model_profile_repository_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), modelProfileRepositoryGormConfig())
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	runModelProfileSlotRetryCountContract(t, db)
}

func TestModelProfileSlotRepositoryPreservesExplicitRetryCountMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), modelProfileRepositoryGormConfig())
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	runModelProfileSlotRetryCountContract(t, db)
}

func runModelProfileSlotRetryCountContract(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Migrator().DropTable(&models.ModelProfileSlot{}); err != nil {
		t.Fatalf("drop model profile slot table: %v", err)
	}
	if err := db.AutoMigrate(&models.ModelProfileSlot{}); err != nil {
		t.Fatalf("migrate model profile slot table: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Migrator().DropTable(&models.ModelProfileSlot{}); err != nil {
			t.Errorf("cleanup model profile slot table: %v", err)
		}
	})

	now := time.Now()
	list := []models.ModelProfileSlot{
		modelProfileSlotFixture(101, enums.ModelUsageSlotReplyLLM, 0, 1, now),
		modelProfileSlotFixture(101, enums.ModelUsageSlotIntentDetectLLM, 2, 2, now),
	}
	if err := ModelProfileSlotRepository.ReplaceByTemplateID(db, 101, list); err != nil {
		t.Fatalf("replace model profile slots: %v", err)
	}
	assertModelProfileSlotIDs(t, list)
	assertModelProfileSlotRetryCounts(t, db, 101, map[enums.ModelUsageSlot]int{
		enums.ModelUsageSlotReplyLLM:        0,
		enums.ModelUsageSlotIntentDetectLLM: 2,
	})

	replacement := []models.ModelProfileSlot{
		modelProfileSlotFixture(101, enums.ModelUsageSlotReplyLLM, 2, 1, now.Add(time.Second)),
		modelProfileSlotFixture(101, enums.ModelUsageSlotIntentDetectLLM, 0, 2, now.Add(time.Second)),
	}
	if err := ModelProfileSlotRepository.ReplaceByTemplateID(db, 101, replacement); err != nil {
		t.Fatalf("replace model profile slots again: %v", err)
	}
	assertModelProfileSlotIDs(t, replacement)
	assertModelProfileSlotRetryCounts(t, db, 101, map[enums.ModelUsageSlot]int{
		enums.ModelUsageSlotReplyLLM:        2,
		enums.ModelUsageSlotIntentDetectLLM: 0,
	})
}

func assertModelProfileSlotIDs(t *testing.T, items []models.ModelProfileSlot) {
	t.Helper()
	for _, item := range items {
		if item.ID <= 0 {
			t.Fatalf("slot %q did not receive a persisted id", item.UsageCode)
		}
	}
}

func modelProfileSlotFixture(templateID int64, usage enums.ModelUsageSlot, retries, sortNo int, now time.Time) models.ModelProfileSlot {
	return models.ModelProfileSlot{
		TemplateID:    templateID,
		UsageCode:     usage,
		DisplayName:   string(usage),
		ModelType:     enums.AIModelTypeLLM,
		Provider:      "newapi",
		ModelName:     "model-a",
		APIMode:       "chat_completions",
		TimeoutMS:     30000,
		MaxRetryCount: retries,
		Enabled:       true,
		SortNo:        sortNo,
		AuditFields:   models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
}

func assertModelProfileSlotRetryCounts(t *testing.T, db *gorm.DB, templateID int64, expected map[enums.ModelUsageSlot]int) {
	t.Helper()
	items := ModelProfileSlotRepository.FindByTemplateID(db, templateID)
	if len(items) != len(expected) {
		t.Fatalf("slot count=%d want=%d", len(items), len(expected))
	}
	for _, item := range items {
		want, ok := expected[item.UsageCode]
		if !ok {
			t.Fatalf("unexpected slot %q", item.UsageCode)
		}
		if item.MaxRetryCount != want {
			t.Fatalf("slot %q maxRetryCount=%d want=%d", item.UsageCode, item.MaxRetryCount, want)
		}
	}
}

func modelProfileRepositoryGormConfig() *gorm.Config {
	return &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	}
}
