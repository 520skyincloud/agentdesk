package bootstrap

import (
	"path/filepath"
	"testing"

	"agent-desk/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestTenantScopedUniqueIndexesValidateFreshSchema(t *testing.T) {
	db := openTenantUniqueIndexTestDB(t)
	modelsToMigrate := []any{&models.Store{}, &models.AgentProfile{}}
	if err := db.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatalf("create current schema: %v", err)
	}
	if err := validateTenantScopedUniqueIndexes(db); err != nil {
		t.Fatalf("validate tenant indexes: %v", err)
	}
	for _, spec := range tenantScopedUniqueIndexSpecs() {
		if err := requireUniqueIndex(db, spec.model, spec.currentName, spec.currentFields); err != nil {
			t.Errorf("current index %s invalid: %v", spec.currentName, err)
		}
	}

	seedTenantUniqueRows(t, db, 101, "shared-code")
	seedTenantUniqueRows(t, db, 102, "shared-code")
	assertTenantUniqueConflicts(t, db)
}

func TestTenantScopedUniqueIndexesRejectSameTenantDuplicates(t *testing.T) {
	db := openTenantUniqueIndexTestDB(t)
	if err := db.AutoMigrate(&models.Store{}); err != nil {
		t.Fatalf("create store schema: %v", err)
	}
	if err := db.Migrator().DropIndex(&models.Store{}, "uk_store_tenant_code"); err != nil {
		t.Fatalf("drop store tenant index: %v", err)
	}
	stores := []models.Store{
		{TenantID: 101, StoreCode: "duplicate", Name: "first"},
		{TenantID: 101, StoreCode: "duplicate", Name: "second"},
	}
	if err := db.Create(&stores).Error; err != nil {
		t.Fatalf("seed duplicate stores without index: %v", err)
	}
	if err := db.AutoMigrate(&models.Store{}); err == nil {
		t.Fatal("AutoMigrate must reject existing same-tenant duplicate store codes")
	}
	var count int64
	if err := db.Model(&models.Store{}).Where("tenant_id = ? AND store_code = ?", 101, "duplicate").Count(&count).Error; err != nil {
		t.Fatalf("count duplicate stores: %v", err)
	}
	if count != 2 {
		t.Fatalf("duplicate rows changed after failed migration: %d", count)
	}
}

func TestTenantScopedUniqueIndexValidationRejectsMissingIndex(t *testing.T) {
	db := openTenantUniqueIndexTestDB(t)
	if err := db.AutoMigrate(&models.Store{}, &models.AgentProfile{}); err != nil {
		t.Fatalf("create current schema: %v", err)
	}
	if err := db.Migrator().DropIndex(&models.Store{}, "uk_store_tenant_code"); err != nil {
		t.Fatalf("drop required index: %v", err)
	}
	if err := validateTenantScopedUniqueIndexes(db); err == nil {
		t.Fatal("missing current tenant index must stop startup")
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

func seedTenantUniqueRows(t *testing.T, db *gorm.DB, tenantID int64, code string) {
	t.Helper()
	rows := []any{
		&models.Store{TenantID: tenantID, StoreCode: code, Name: "store"},
		&models.AgentProfile{TenantID: tenantID, UserID: tenantID, AgentCode: code, DisplayName: "agent"},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("create tenant %d row %T: %v", tenantID, row, err)
		}
	}
}

func assertTenantUniqueConflicts(t *testing.T, db *gorm.DB) {
	t.Helper()
	checks := []any{
		&models.Store{TenantID: 101, StoreCode: "shared-code", Name: "duplicate store"},
		&models.AgentProfile{TenantID: 101, UserID: 9999, AgentCode: "shared-code", DisplayName: "duplicate agent"},
	}
	for _, row := range checks {
		if err := db.Create(row).Error; err == nil {
			t.Errorf("same-tenant duplicate %T was accepted", row)
		}
	}
}
