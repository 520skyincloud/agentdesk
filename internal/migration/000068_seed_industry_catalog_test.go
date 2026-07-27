package migration

import (
	"os"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/replyintent"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestSeedIndustryCatalogBuildsFreshHotelCatalog(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	runSeedIndustryCatalogScenario(t, db)
}

func TestSeedIndustryCatalogMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "b2_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	runSeedIndustryCatalogScenario(t, db)
}

func runSeedIndustryCatalogScenario(t *testing.T, db *gorm.DB) {
	t.Helper()
	modelsForTest := []any{
		&models.Tenant{}, &models.TenantIndustryChangeLog{},
		&models.ReplyIntentProfile{}, &models.ReplyIntentConfig{},
		&models.IndustryTagDefinition{}, &models.Tag{}, &models.TenantCustomerTagPolicy{},
		&models.CustomerTagRelation{}, &models.CustomerTagChangeLog{},
	}
	if err := db.AutoMigrate(modelsForTest...); err != nil {
		t.Fatalf("migrate fixtures: %v", err)
	}
	t.Cleanup(func() {
		for i := len(modelsForTest) - 1; i >= 0; i-- {
			if err := db.Migrator().DropTable(modelsForTest[i]); err != nil {
				t.Errorf("drop migration fixture %T: %v", modelsForTest[i], err)
			}
		}
	})

	if err := db.Transaction(ensureOIDCFallbackTenant); err != nil {
		t.Fatalf("ensure OIDC fallback tenant: %v", err)
	}
	var fallback models.Tenant
	if err := db.Where("tenant_code = ?", "legacy-default").Take(&fallback).Error; err != nil {
		t.Fatalf("load OIDC fallback tenant: %v", err)
	}
	now := time.Now().Add(-time.Hour)
	unrelated := &models.Tenant{
		TenantCode: "unrelated-tenant", LegalName: "Unrelated Tenant", ShortName: "Unrelated",
		RegistrationType: "test", RegistrationNo: "unrelated-tenant", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(unrelated).Error; err != nil {
		t.Fatalf("create unrelated tenant: %v", err)
	}

	if err := seedIndustryCatalog(db); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	profile := assertIndustryCatalog(t, db, fallback.ID)
	assertTenantRemainsUninitialized(t, db, unrelated.ID)

	custom := &models.IndustryTagDefinition{
		IntentProfileID:    profile.ID,
		Name:               "平台后续扩展分类",
		SemanticKey:        "category.platform_extension",
		DefinitionRevision: profile.Revision,
		Status:             enums.StatusDisabled,
		AuditFields:        models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(custom).Error; err != nil {
		t.Fatalf("create platform extension: %v", err)
	}
	customIntent := &models.ReplyIntentConfig{
		IntentProfileID: profile.ID,
		Code:            "platform_extension",
		Name:            "平台后续扩展意图",
		Status:          enums.StatusDisabled,
		AuditFields:     models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(customIntent).Error; err != nil {
		t.Fatalf("create platform intent extension: %v", err)
	}
	if err := seedIndustryCatalog(db); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	assertIndustryCatalog(t, db, fallback.ID)
	assertTenantRemainsUninitialized(t, db, unrelated.ID)
	if current := db.First(&models.IndustryTagDefinition{}, custom.ID); current.Error != nil {
		t.Fatalf("idempotent seed removed platform extension: %v", current.Error)
	}
	if current := db.First(&models.ReplyIntentConfig{}, customIntent.ID); current.Error != nil {
		t.Fatalf("idempotent seed removed platform intent extension: %v", current.Error)
	}
}

func assertIndustryCatalog(t *testing.T, db *gorm.DB, tenantID int64) *models.ReplyIntentProfile {
	t.Helper()
	var profile models.ReplyIntentProfile
	if err := db.Where("code = ?", replyintent.DefaultHotelProfileCode).First(&profile).Error; err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if profile.IndustryCode != replyintent.DefaultHotelIndustryCode ||
		profile.IntentDetectPrompt != replyintent.DefaultHotelIntentDetectPrompt() ||
		profile.IntentJSONSchema != replyintent.DefaultHotelIntentJSONSchema() ||
		profile.Revision != 1 || profile.PublishedAt == nil || profile.Status != enums.StatusOk {
		t.Fatalf("unexpected authoritative profile: %+v", profile)
	}

	var intents []models.ReplyIntentConfig
	if err := db.Where("intent_profile_id = ? AND status = ?", profile.ID, enums.StatusOk).
		Order("sort_no ASC").Find(&intents).Error; err != nil {
		t.Fatalf("load intents: %v", err)
	}
	wantCodes := []string{"hotel_info", "hotel_variable", "service_request", "human_complaint_risk", "interaction"}
	if len(intents) != len(wantCodes) {
		t.Fatalf("active intents=%d want=%d: %+v", len(intents), len(wantCodes), intents)
	}
	for i := range wantCodes {
		if intents[i].Code != wantCodes[i] || intents[i].IntentProfileID != profile.ID {
			t.Fatalf("intent[%d]=%+v want code=%s profile=%d", i, intents[i], wantCodes[i], profile.ID)
		}
	}

	var definitions []models.IndustryTagDefinition
	if err := db.Where("intent_profile_id = ? AND status = ?", profile.ID, enums.StatusOk).Find(&definitions).Error; err != nil {
		t.Fatalf("load definitions: %v", err)
	}
	parents, leaves, replyLeaves := 0, 0, 0
	conflicts := make(map[string]struct{})
	for i := range definitions {
		if definitions[i].ParentID == 0 {
			parents++
			continue
		}
		leaves++
		if !definitions[i].AIEnabled {
			t.Fatalf("leaf tag is not AI-enabled: %+v", definitions[i])
		}
		if definitions[i].ReplyEnabled {
			replyLeaves++
		}
		if definitions[i].ConflictGroup != "" {
			conflicts[definitions[i].ConflictGroup] = struct{}{}
		}
	}
	if parents != 4 || leaves != 31 || len(conflicts) != 8 || replyLeaves != 25 {
		t.Fatalf("catalog counts parents=%d leaves=%d conflicts=%d reply=%d", parents, leaves, len(conflicts), replyLeaves)
	}

	var tenant models.Tenant
	if err := db.First(&tenant, tenantID).Error; err != nil {
		t.Fatalf("load tenant: %v", err)
	}
	if tenant.IntentProfileID != profile.ID {
		t.Fatalf("tenant profile=%d want=%d", tenant.IntentProfileID, profile.ID)
	}
	var tenantTagCount int64
	if err := db.Model(&models.Tag{}).
		Where("tenant_id = ? AND intent_profile_id = ? AND status = ?", tenantID, profile.ID, enums.StatusOk).
		Count(&tenantTagCount).Error; err != nil {
		t.Fatalf("count tenant tags: %v", err)
	}
	if tenantTagCount != 35 {
		t.Fatalf("tenant tag count=%d want=35", tenantTagCount)
	}
	var policy models.TenantCustomerTagPolicy
	if err := db.Where("tenant_id = ?", tenantID).First(&policy).Error; err != nil {
		t.Fatalf("load tenant tag policy: %v", err)
	}
	if policy.IntentProfileID != profile.ID || policy.MaxOperationsPerRun != 6 {
		t.Fatalf("unexpected tenant policy: %+v", policy)
	}
	var logCount int64
	if err := db.Model(&models.TenantIndustryChangeLog{}).
		Where("tenant_id = ? AND action = ?", tenantID, "system_initialize").
		Count(&logCount).Error; err != nil {
		t.Fatalf("count industry logs: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("migration log count=%d want=1", logCount)
	}
	return &profile
}

func assertTenantRemainsUninitialized(t *testing.T, db *gorm.DB, tenantID int64) {
	t.Helper()
	var tenant models.Tenant
	if err := db.First(&tenant, tenantID).Error; err != nil {
		t.Fatalf("load unrelated tenant: %v", err)
	}
	if tenant.IntentProfileID != 0 {
		t.Fatalf("industry seed rewrote unrelated tenant profile to %d", tenant.IntentProfileID)
	}
	var tagCount int64
	if err := db.Model(&models.Tag{}).Where("tenant_id = ?", tenantID).Count(&tagCount).Error; err != nil {
		t.Fatalf("count unrelated tenant tags: %v", err)
	}
	if tagCount != 0 {
		t.Fatalf("industry seed created %d tags for unrelated tenant", tagCount)
	}
}
