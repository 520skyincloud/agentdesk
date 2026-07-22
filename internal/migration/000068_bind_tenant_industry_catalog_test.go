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

func TestMigrateTenantIndustryCatalogBuildsSingleAuthoritativeHotelScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	runMigrateTenantIndustryCatalogScenario(t, db)
}

func TestMigrateTenantIndustryCatalogMySQL(t *testing.T) {
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
	runMigrateTenantIndustryCatalogScenario(t, db)
}

func runMigrateTenantIndustryCatalogScenario(t *testing.T, db *gorm.DB) {
	t.Helper()
	modelsForTest := []any{
		&models.Tenant{}, &models.TenantIndustryChangeLog{},
		&models.ReplyIntentProfile{}, &models.ReplyIntentConfig{},
		&models.IndustryTagDefinition{}, &models.Tag{}, &models.TenantCustomerTagPolicy{},
		&models.CustomerTagRelation{}, &models.CustomerTagChangeLog{},
		&models.Company{}, &models.WxWorkProtocolInstance{}, &models.KnowledgeBase{}, &models.KnowledgeResourceGroup{},
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
	now := time.Now().Add(-time.Hour)
	legacyProfile := &models.ReplyIntentProfile{
		Code: replyintent.DefaultHotelProfileCode, Name: "旧酒店行业", IndustryCode: "legacy-hotel",
		IntentDetectPrompt: "legacy prompt", IntentJSONSchema: "legacy schema", Revision: 3,
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(legacyProfile).Error; err != nil {
		t.Fatalf("create legacy profile: %v", err)
	}
	legacyIntent := &models.ReplyIntentConfig{
		Code: "hotel_info", Name: "旧酒店信息", ScopeType: "global", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	overrideIntent := &models.ReplyIntentConfig{
		Code: "store_override", Name: "门店覆盖", IntentProfileID: legacyProfile.ID,
		ScopeType: "store", StoreID: 99, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&[]*models.ReplyIntentConfig{legacyIntent, overrideIntent}).Error; err != nil {
		t.Fatalf("create legacy intents: %v", err)
	}
	tenant := &models.Tenant{
		TenantCode: "tenant-hotel", LegalName: "丽斯未来酒店", ShortName: "丽斯未来",
		RegistrationType: "test", RegistrationNo: "tenant-hotel", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	company := &models.Company{TenantID: tenant.ID, Name: "旧门店公司", Code: "legacy", IntentProfileID: legacyProfile.ID, Status: enums.StatusOk}
	instance := &models.WxWorkProtocolInstance{TenantID: tenant.ID, Guid: "industry-migration", IntentProfileID: legacyProfile.ID, Status: enums.StatusOk}
	knowledge := &models.KnowledgeBase{TenantID: tenant.ID, Name: "丽斯未来知识库", IntentProfileID: legacyProfile.ID, Status: enums.StatusOk}
	resourceGroup := &models.KnowledgeResourceGroup{
		TenantID: tenant.ID, StoreID: 1, IntentProfileID: legacyProfile.ID, KnowledgeBaseID: 1,
		SourceProvider: "fastgpt", SourceRecordID: "legacy-resource", Status: enums.StatusOk,
	}
	for name, item := range map[string]any{"company": company, "instance": instance, "knowledge": knowledge, "resourceGroup": resourceGroup} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	if err := migrateTenantIndustryCatalog(db); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	assertTenantIndustryCatalog(t, db, tenant.ID, legacyProfile.ID)
	if err := migrateTenantIndustryCatalog(db); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	assertTenantIndustryCatalog(t, db, tenant.ID, legacyProfile.ID)
}

func assertTenantIndustryCatalog(t *testing.T, db *gorm.DB, tenantID, profileID int64) {
	t.Helper()
	var profile models.ReplyIntentProfile
	if err := db.First(&profile, profileID).Error; err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if profile.IndustryCode != replyintent.DefaultHotelIndustryCode ||
		profile.IntentDetectPrompt != replyintent.DefaultHotelIntentDetectPrompt() ||
		profile.IntentJSONSchema != replyintent.DefaultHotelIntentJSONSchema() ||
		profile.Revision != 4 || profile.PublishedAt == nil || profile.Status != enums.StatusOk {
		t.Fatalf("unexpected authoritative profile: %+v", profile)
	}

	var intents []models.ReplyIntentConfig
	if err := db.Where("intent_profile_id = ? AND status = ?", profileID, enums.StatusOk).
		Order("sort_no ASC").Find(&intents).Error; err != nil {
		t.Fatalf("load intents: %v", err)
	}
	wantCodes := []string{"hotel_info", "hotel_variable", "service_request", "human_complaint_risk", "interaction"}
	if len(intents) != len(wantCodes) {
		t.Fatalf("active intents=%d want=%d: %+v", len(intents), len(wantCodes), intents)
	}
	for i := range wantCodes {
		if intents[i].Code != wantCodes[i] || intents[i].ScopeType != "global" || intents[i].CompanyID != 0 || intents[i].StoreID != 0 || intents[i].WxWorkInstanceID != 0 {
			t.Fatalf("intent[%d]=%+v want code=%s global scope", i, intents[i], wantCodes[i])
		}
	}
	var lowerScopeCount int64
	if err := db.Model(&models.ReplyIntentConfig{}).
		Where("scope_type <> ? OR company_id <> 0 OR store_id <> 0 OR wx_work_instance_id <> 0", "global").
		Count(&lowerScopeCount).Error; err != nil {
		t.Fatalf("count lower scopes: %v", err)
	}
	if lowerScopeCount != 0 {
		t.Fatalf("lower-level intent override count=%d", lowerScopeCount)
	}

	var definitions []models.IndustryTagDefinition
	if err := db.Where("intent_profile_id = ? AND status = ?", profileID, enums.StatusOk).Find(&definitions).Error; err != nil {
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
	if tenant.IntentProfileID != profileID {
		t.Fatalf("tenant profile=%d want=%d", tenant.IntentProfileID, profileID)
	}
	var tenantTagCount int64
	if err := db.Model(&models.Tag{}).Where("tenant_id = ? AND intent_profile_id = ? AND status = ?", tenantID, profileID, enums.StatusOk).Count(&tenantTagCount).Error; err != nil {
		t.Fatalf("count tenant tags: %v", err)
	}
	if tenantTagCount != 35 {
		t.Fatalf("tenant tag count=%d want=35", tenantTagCount)
	}
	var policy models.TenantCustomerTagPolicy
	if err := db.Where("tenant_id = ?", tenantID).First(&policy).Error; err != nil {
		t.Fatalf("load tenant tag policy: %v", err)
	}
	if policy.IntentProfileID != profileID || policy.MaxOperationsPerRun != 6 {
		t.Fatalf("unexpected tenant policy: %+v", policy)
	}
	var logCount int64
	if err := db.Model(&models.TenantIndustryChangeLog{}).Where("tenant_id = ? AND action = ?", tenantID, "migration").Count(&logCount).Error; err != nil {
		t.Fatalf("count industry logs: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("migration log count=%d want=1", logCount)
	}
	for name, item := range map[string]any{
		"company": &models.Company{}, "instance": &models.WxWorkProtocolInstance{},
		"knowledge": &models.KnowledgeBase{}, "resourceGroup": &models.KnowledgeResourceGroup{},
	} {
		if err := db.Where("tenant_id = ?", tenantID).First(item).Error; err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		switch value := item.(type) {
		case *models.Company:
			if value.IntentProfileID != 0 {
				t.Fatalf("company override=%d", value.IntentProfileID)
			}
		case *models.WxWorkProtocolInstance:
			if value.IntentProfileID != 0 {
				t.Fatalf("instance override=%d", value.IntentProfileID)
			}
		case *models.KnowledgeBase:
			if value.IntentProfileID != 0 {
				t.Fatalf("knowledge override=%d", value.IntentProfileID)
			}
		case *models.KnowledgeResourceGroup:
			if value.IntentProfileID != 0 {
				t.Fatalf("knowledge resource override=%d", value.IntentProfileID)
			}
		}
	}
}
