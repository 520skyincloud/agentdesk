package bootstrap

import (
	"fmt"
	"path/filepath"
	"testing"

	"agent-desk/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestTenantScopedUniqueIndexesUpgradeLegacyIndexes(t *testing.T) {
	db := openTenantUniqueIndexTestDB(t)
	modelsToMigrate := []any{&models.Company{}, &models.Store{}, &models.AgentProfile{}}
	if err := db.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatalf("create current schema: %v", err)
	}
	for _, spec := range tenantScopedUniqueIndexSpecs() {
		if err := db.Migrator().DropIndex(spec.model, spec.currentName); err != nil {
			t.Fatalf("drop current index %s: %v", spec.currentName, err)
		}
		if err := db.Exec(fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s (%s)", spec.legacyName, tenantUniqueTestTable(spec.model), spec.legacyFields[0])).Error; err != nil {
			t.Fatalf("create legacy index %s: %v", spec.legacyName, err)
		}
	}
	seedTenantUniqueRows(t, db, "legacy-a", "legacy-b")

	if err := db.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatalf("create replacement indexes: %v", err)
	}
	if err := retireLegacyGlobalUniqueIndexes(db); err != nil {
		t.Fatalf("retire legacy indexes: %v", err)
	}
	if err := retireLegacyGlobalUniqueIndexes(db); err != nil {
		t.Fatalf("repeat legacy index retirement: %v", err)
	}
	for _, spec := range tenantScopedUniqueIndexSpecs() {
		if db.Migrator().HasIndex(spec.model, spec.legacyName) {
			t.Errorf("legacy index %s still exists", spec.legacyName)
		}
		if err := requireUniqueIndex(db, spec.model, spec.currentName, spec.currentFields); err != nil {
			t.Errorf("replacement index %s invalid: %v", spec.currentName, err)
		}
	}

	seedTenantUniqueRows(t, db, "legacy-a", "legacy-b")
	assertTenantUniqueConflicts(t, db, "legacy-a")
}

func TestTenantScopedUniqueIndexesRejectSameTenantDuplicates(t *testing.T) {
	db := openTenantUniqueIndexTestDB(t)
	if err := db.AutoMigrate(&models.Company{}); err != nil {
		t.Fatalf("create company schema: %v", err)
	}
	if err := db.Migrator().DropIndex(&models.Company{}, "uk_company_tenant_name"); err != nil {
		t.Fatalf("drop company tenant index: %v", err)
	}
	companies := []models.Company{
		{TenantID: 101, Name: "duplicate"},
		{TenantID: 101, Name: "duplicate"},
	}
	if err := db.Create(&companies).Error; err != nil {
		t.Fatalf("seed duplicate companies without index: %v", err)
	}
	if err := db.AutoMigrate(&models.Company{}); err == nil {
		t.Fatal("AutoMigrate must reject existing same-tenant duplicate names")
	}
	var count int64
	if err := db.Model(&models.Company{}).Where("tenant_id = ? AND name = ?", 101, "duplicate").Count(&count).Error; err != nil {
		t.Fatalf("count duplicate companies: %v", err)
	}
	if count != 2 {
		t.Fatalf("duplicate rows changed after failed migration: %d", count)
	}
}

func TestRetireLegacyGlobalUniqueIndexesRejectsUnexpectedShape(t *testing.T) {
	db := openTenantUniqueIndexTestDB(t)
	if err := db.AutoMigrate(&models.Company{}, &models.Store{}, &models.AgentProfile{}); err != nil {
		t.Fatalf("create current schema: %v", err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX uk_company_name ON t_company (tenant_id)").Error; err != nil {
		t.Fatalf("create unexpected legacy index: %v", err)
	}
	if err := retireLegacyGlobalUniqueIndexes(db); err == nil {
		t.Fatal("unexpected legacy index shape must stop cleanup")
	}
	if !db.Migrator().HasIndex(&models.Company{}, "uk_company_name") {
		t.Fatal("unexpected legacy index was dropped")
	}
}

func openTenantUniqueIndexTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tenant-unique.db")), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func tenantUniqueTestTable(model any) string {
	switch model.(type) {
	case *models.Company:
		return "t_company"
	case *models.Store:
		return "t_store"
	case *models.AgentProfile:
		return "t_agent_profile"
	default:
		panic(fmt.Sprintf("unsupported model %T", model))
	}
}

func seedTenantUniqueRows(t *testing.T, db *gorm.DB, companyName, code string) {
	t.Helper()
	var offset int64
	if err := db.Model(&models.Company{}).Count(&offset).Error; err != nil {
		t.Fatalf("count company rows: %v", err)
	}
	tenantID := offset + 101
	rows := []any{
		&models.Company{TenantID: tenantID, Name: companyName},
		&models.Store{TenantID: tenantID, StoreCode: code, Name: "store"},
		&models.AgentProfile{TenantID: tenantID, UserID: tenantID, AgentCode: code, DisplayName: "agent"},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("create tenant %d row %T: %v", tenantID, row, err)
		}
	}
}

func assertTenantUniqueConflicts(t *testing.T, db *gorm.DB, companyName string) {
	t.Helper()
	checks := []any{
		&models.Company{TenantID: 101, Name: companyName},
		&models.Store{TenantID: 101, StoreCode: "legacy-b", Name: "duplicate store"},
		&models.AgentProfile{TenantID: 101, UserID: 9999, AgentCode: "legacy-b", DisplayName: "duplicate agent"},
	}
	for _, row := range checks {
		if err := db.Create(row).Error; err == nil {
			t.Errorf("same-tenant duplicate %T was accepted", row)
		}
	}
}
