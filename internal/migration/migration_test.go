package migration

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestValidateMigrationDefinitionRejectsReusedVersion(t *testing.T) {
	stored := models.Migration{Version: 25, Remark: "backfill wxwork protocol instance agent team bindings", Success: true}
	current := MigrationFunc{Version: 25, Remark: "refresh default hotel intent profile human route rules"}

	err := validateMigrationDefinition(stored, current)
	if err == nil || !strings.Contains(err.Error(), "definition mismatch") {
		t.Fatalf("validateMigrationDefinition() error=%v want definition mismatch", err)
	}
}

func TestValidateMigrationDefinitionAcceptsMatchingIdentity(t *testing.T) {
	stored := models.Migration{Version: 39, Remark: "backfill agent organization tenants", Success: true}
	current := MigrationFunc{Version: 39, Remark: "backfill agent organization tenants"}

	if err := validateMigrationDefinition(stored, current); err != nil {
		t.Fatalf("validateMigrationDefinition() error=%v", err)
	}
}

func TestValidateMigrationDefinitionAcceptsKnownHistoricalPredecessor(t *testing.T) {
	stored := models.Migration{Version: 13, Remark: "normalize reply intent configs to seven categories", Success: true}
	current := MigrationFunc{Version: 13, Remark: "normalize reply intent configs to five categories"}

	if err := validateMigrationDefinition(stored, current); err != nil {
		t.Fatalf("validateMigrationDefinition() error=%v", err)
	}
}

func TestArchiveSupersededMigrationDefinitionsPreservesEvidence(t *testing.T) {
	db := setupMigrationCompatibilityDB(t)
	legacy := &models.Migration{
		Version:    21,
		Remark:     "backfill wxwork protocol instance agent team bindings",
		Success:    true,
		ErrorInfo:  "historical evidence",
		RetryCount: 2,
		CreatedAt:  time.Now().Add(-time.Hour),
		UpdatedAt:  time.Now().Add(-30 * time.Minute),
	}
	if err := db.Create(legacy).Error; err != nil {
		t.Fatalf("create legacy migration: %v", err)
	}
	if err := archiveSupersededMigrationDefinitions(db); err != nil {
		t.Fatalf("archiveSupersededMigrationDefinitions() error=%v", err)
	}
	if err := archiveSupersededMigrationDefinitions(db); err != nil {
		t.Fatalf("archiveSupersededMigrationDefinitions() second error=%v", err)
	}

	var activeCount int64
	if err := db.Model(&models.Migration{}).Where("version = ?", legacy.Version).Count(&activeCount).Error; err != nil {
		t.Fatalf("count active migrations: %v", err)
	}
	if activeCount != 0 {
		t.Fatalf("active migration count=%d want 0", activeCount)
	}
	var archive models.MigrationDefinitionArchive
	if err := db.Take(&archive, "source_migration_id = ?", legacy.ID).Error; err != nil {
		t.Fatalf("load migration archive: %v", err)
	}
	if archive.Version != legacy.Version || archive.Remark != legacy.Remark || archive.Success != legacy.Success || archive.ErrorInfo != legacy.ErrorInfo || archive.RetryCount != legacy.RetryCount {
		t.Fatalf("archive=%+v does not preserve legacy=%+v", archive, *legacy)
	}
	if archive.ReplacementRemark != migrationFuncs[21].Remark || archive.ArchiveReason == "" {
		t.Fatalf("archive replacement metadata=%+v", archive)
	}
}

func TestArchiveSupersededMigrationDefinitionsLeavesUnknownConflict(t *testing.T) {
	db := setupMigrationCompatibilityDB(t)
	unknown := &models.Migration{Version: 21, Remark: "unknown reused definition", Success: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(unknown).Error; err != nil {
		t.Fatalf("create unknown migration: %v", err)
	}
	if err := archiveSupersededMigrationDefinitions(db); err != nil {
		t.Fatalf("archiveSupersededMigrationDefinitions() error=%v", err)
	}
	var stored models.Migration
	if err := db.Take(&stored, "id = ?", unknown.ID).Error; err != nil {
		t.Fatalf("unknown migration must remain active: %v", err)
	}
	if err := validateMigrationDefinition(stored, migrationFuncs[21]); err == nil || !strings.Contains(err.Error(), "definition mismatch") {
		t.Fatalf("validateMigrationDefinition() error=%v want definition mismatch", err)
	}
}

func TestCurrentIntentCleanupProducesFiveActiveCategories(t *testing.T) {
	db := setupMigrationCompatibilityDB(t)
	if err := db.AutoMigrate(&models.ReplyIntentConfig{}); err != nil {
		t.Fatalf("migrate reply intent config: %v", err)
	}
	previousDB := sqls.DB()
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(previousDB) })

	if err := migrationFuncs[21].Fn(); err != nil {
		t.Fatalf("run current migration 21: %v", err)
	}
	var active []models.ReplyIntentConfig
	if err := db.Where("status = ?", enums.StatusOk).Order("code ASC").Find(&active).Error; err != nil {
		t.Fatalf("load active reply intents: %v", err)
	}
	want := []string{"hotel_info", "hotel_variable", "human_complaint_risk", "interaction", "service_request"}
	if len(active) != len(want) {
		t.Fatalf("active intent count=%d want %d: %+v", len(active), len(want), active)
	}
	for i := range want {
		if active[i].Code != want[i] {
			t.Fatalf("active[%d].Code=%q want %q", i, active[i].Code, want[i])
		}
	}
}

func setupMigrationCompatibilityDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migration-compatibility.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open migration compatibility db: %v", err)
	}
	if err := db.AutoMigrate(&models.Migration{}, &models.MigrationDefinitionArchive{}); err != nil {
		t.Fatalf("migrate compatibility models: %v", err)
	}
	return db
}
