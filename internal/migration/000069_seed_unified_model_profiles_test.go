package migration

import (
	"os"
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestMigrateUnifiedModelProfilesSeedsNineSlotDraftWithUnifiedGateway(t *testing.T) {
	db := openUnifiedModelProfileMigrationDB(t)

	if err := migrateUnifiedModelProfiles(db); err != nil {
		t.Fatalf("migrateUnifiedModelProfiles() error=%v", err)
	}
	var profile models.ModelProfileTemplate
	if err := db.Where("code = ?", "standard").Take(&profile).Error; err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if profile.Status != enums.ModelProfileStatusDraft || profile.GatewayBaseURL != constants.UnifiedNewAPIGatewayBaseURL {
		t.Fatalf("profile=%#v", profile)
	}
	var slots []models.ModelProfileSlot
	if err := db.Where("template_id = ?", profile.ID).Order("sort_no ASC").Find(&slots).Error; err != nil {
		t.Fatal(err)
	}
	if len(slots) != 9 {
		t.Fatalf("slot count=%d want=9", len(slots))
	}
	for _, slot := range slots {
		if slot.Provider != "newapi" || !slot.Enabled || slot.ModelName != "" {
			t.Fatalf("slot must be an enabled, unconfigured NewAPI slot: %#v", slot)
		}
	}
	var credentialCount int64
	if err := db.Model(&models.StoreModelCredential{}).Count(&credentialCount).Error; err != nil {
		t.Fatal(err)
	}
	if credentialCount != 0 {
		t.Fatalf("credential count=%d; migration must not create credentials", credentialCount)
	}

	if err := migrateUnifiedModelProfiles(db); err != nil {
		t.Fatalf("idempotent migrate error=%v", err)
	}
	var profileCount, slotCount int64
	if err := db.Model(&models.ModelProfileTemplate{}).Count(&profileCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.ModelProfileSlot{}).Count(&slotCount).Error; err != nil {
		t.Fatal(err)
	}
	if profileCount != 1 || slotCount != 9 {
		t.Fatalf("idempotent counts profiles=%d slots=%d", profileCount, slotCount)
	}
}

func TestMigrateUnifiedModelProfilesMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), unifiedModelProfileMigrationGORMConfig())
	if err != nil {
		t.Fatalf("open MySQL migration database: %v", err)
	}
	if err = migrateUnifiedModelProfileTables(db); err != nil {
		t.Fatalf("MySQL AutoMigrate() error=%v", err)
	}
	if err = migrateUnifiedModelProfiles(db); err != nil {
		t.Fatalf("MySQL migrateUnifiedModelProfiles() error=%v", err)
	}
	if err = migrateUnifiedModelProfiles(db); err != nil {
		t.Fatalf("MySQL idempotent migration error=%v", err)
	}

	var profile models.ModelProfileTemplate
	if err = db.Where("code = ? AND revision = ?", "standard", 1).Take(&profile).Error; err != nil {
		t.Fatalf("load MySQL model profile: %v", err)
	}
	var slotCount, credentialCount int64
	if err = db.Model(&models.ModelProfileSlot{}).Where("template_id = ?", profile.ID).Count(&slotCount).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&models.StoreModelCredential{}).Count(&credentialCount).Error; err != nil {
		t.Fatal(err)
	}
	if profile.Status != enums.ModelProfileStatusDraft || profile.GatewayBaseURL != constants.UnifiedNewAPIGatewayBaseURL || slotCount != 9 || credentialCount != 0 {
		t.Fatalf("MySQL migration profile=%s slots=%d credentials=%d", profile.Status, slotCount, credentialCount)
	}
}

func openUnifiedModelProfileMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), unifiedModelProfileMigrationGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateUnifiedModelProfileTables(db); err != nil {
		t.Fatalf("AutoMigrate() error=%v", err)
	}
	return db
}

func migrateUnifiedModelProfileTables(db *gorm.DB) error {
	return db.AutoMigrate(
		&models.ModelProfileTemplate{}, &models.ModelProfileSlot{},
		&models.StoreModelCredential{},
	)
}

func unifiedModelProfileMigrationGORMConfig() *gorm.Config {
	return &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	}
}
