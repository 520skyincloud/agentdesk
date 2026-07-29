package migration

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"

	"github.com/glebarez/sqlite"
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
	stored := models.Migration{Version: 68, Remark: seedIndustryCatalogMigrationRemark, Success: true}
	current := MigrationFunc{Version: 68, Remark: seedIndustryCatalogMigrationRemark}

	if err := validateMigrationDefinition(stored, current); err != nil {
		t.Fatalf("validateMigrationDefinition() error=%v", err)
	}
}

func TestPreflightAllowsFreshDatabaseWithoutMigrationTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "fresh.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open fresh database: %v", err)
	}
	if err := Preflight(db); err != nil {
		t.Fatalf("Preflight() fresh database error=%v", err)
	}
}

func TestPreflightAcceptsMatchingDefinition(t *testing.T) {
	db := setupMigrationCompatibilityDB(t)
	rows := []models.Migration{
		{Version: 68, Remark: migrationFuncs[68].Remark, Success: true, CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create migration definitions: %v", err)
	}
	if err := Preflight(db); err != nil {
		t.Fatalf("Preflight() error=%v", err)
	}
}

func TestPreflightRejectsUnknownDefinitionBeforeSchemaMutation(t *testing.T) {
	for _, fixture := range []models.Migration{
		{Version: 68, Remark: "unknown parallel definition", Success: true, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{Version: 21, Remark: "retired industry migration", Success: true, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{Version: 999, Remark: "future branch migration", Success: true, CreatedAt: time.Now(), UpdatedAt: time.Now()},
	} {
		t.Run(fixture.Remark, func(t *testing.T) {
			db := setupMigrationCompatibilityDB(t)
			if err := db.Create(&fixture).Error; err != nil {
				t.Fatalf("create unknown migration definition: %v", err)
			}
			if err := Preflight(db); err == nil {
				t.Fatal("Preflight() error=nil want rejection")
			}
		})
	}
}

func TestFreshOnlyMigrationBaselineContainsCurrentInitializers(t *testing.T) {
	want := []int64{2, 15, 35, 68, 69, 70, 71}
	if !reflect.DeepEqual(versions, want) {
		t.Fatalf("registered migration versions=%v want=%v", versions, want)
	}
	for _, version := range versions {
		item := migrationFuncs[version]
		lowerRemark := strings.ToLower(item.Remark)
		for _, retiredTerm := range []string{"backfill", "legacy", "retire", "remove old", "migrate existing"} {
			if strings.Contains(lowerRemark, retiredTerm) {
				t.Fatalf("fresh-only migration %d retains obsolete remark %q", version, item.Remark)
			}
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
	if err := db.AutoMigrate(&models.Migration{}); err != nil {
		t.Fatalf("migrate compatibility models: %v", err)
	}
	return db
}
