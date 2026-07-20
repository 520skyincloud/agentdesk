package migration

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestRetireLegacyCompanyStoreScopesKeepsHistoryAndEnforcesBindingOwnership(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Role{}, &models.UserRole{},
		&models.Store{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{},
		&models.Company{}, &models.AgentTeam{}, &models.KnowledgeBase{}, &models.StoreAIModelSetting{},
		&models.KnowledgeResourceGroup{}, &models.KnowledgeResourceItem{},
		&models.FastGPTStoreTenant{}, &models.FastGPTUsageSyncState{}, &models.FastGPTDatasetJob{},
		&models.ReplyIntentConfig{}, &models.Customer{}, &models.Permission{}, &models.RolePermission{},
	); err != nil {
		t.Fatalf("migrate fixtures: %v", err)
	}

	role := &models.Role{Name: "门店员工号", Code: constants.RoleCodeStoreStaff, Status: enums.StatusOk}
	user := &models.User{TenantID: 101, Username: "store-owner", Nickname: "门店负责人", Status: enums.StatusOk}
	roleOnlyUser := &models.User{TenantID: 101, Username: "role-only-store", Nickname: "待绑定门店", Status: enums.StatusOk}
	for name, item := range map[string]any{"role": role, "user": user, "role only user": roleOnlyUser} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if err := db.Create(&models.UserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
		t.Fatalf("assign store staff role: %v", err)
	}
	if err := db.Create(&models.UserRole{UserID: roleOnlyUser.ID, RoleID: role.ID}).Error; err != nil {
		t.Fatalf("assign role-only store staff role: %v", err)
	}

	store := createLegacyCompanyStore(t, db, "legacy-store")
	legacyCompany := &models.Company{TenantID: 101, Name: "旧公司档案", Code: "legacy-company", IntentProfileID: 88, Status: enums.StatusOk}
	if err := db.Create(legacyCompany).Error; err != nil {
		t.Fatalf("create legacy company: %v", err)
	}
	binding := &models.StoreStaffBinding{TenantID: 101, UserID: user.ID, StoreID: store.ID, CompanyID: 9, Status: enums.StatusOk}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create valid binding: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "legacy-company-instance", StoreID: store.ID,
		StoreStaffBindingID: binding.ID, CompanyID: 9, AIReplyEnabled: true, Status: enums.StatusOk,
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create valid instance: %v", err)
	}

	orphanStore := createLegacyCompanyStore(t, db, "orphan-store")
	orphanBinding := &models.StoreStaffBinding{TenantID: 101, UserID: 0, StoreID: orphanStore.ID, CompanyID: 9, Status: enums.StatusOk}
	if err := db.Create(orphanBinding).Error; err != nil {
		t.Fatalf("create orphan binding: %v", err)
	}
	orphanInstance := &models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "orphan-instance", StoreID: orphanStore.ID,
		StoreStaffBindingID: orphanBinding.ID, CompanyID: 9, AIReplyEnabled: true, Status: enums.StatusOk,
	}
	if err := db.Create(orphanInstance).Error; err != nil {
		t.Fatalf("create orphan instance: %v", err)
	}

	duplicateStore := createLegacyCompanyStore(t, db, "duplicate-store")
	duplicateBinding := &models.StoreStaffBinding{TenantID: 101, UserID: user.ID, StoreID: duplicateStore.ID, CompanyID: 9, Status: enums.StatusOk}
	if err := db.Create(duplicateBinding).Error; err != nil {
		t.Fatalf("create duplicate binding: %v", err)
	}
	duplicateInstance := &models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "duplicate-instance", StoreID: duplicateStore.ID,
		StoreStaffBindingID: duplicateBinding.ID, CompanyID: 9, AIReplyEnabled: true, Status: enums.StatusOk,
	}
	if err := db.Create(duplicateInstance).Error; err != nil {
		t.Fatalf("create duplicate instance: %v", err)
	}

	team := &models.AgentTeam{TenantID: 101, Name: "旧客服组", CompanyScopeIDs: "9", StoreScopeIDs: "1", Status: enums.StatusOk}
	kb := &models.KnowledgeBase{TenantID: 101, Name: "旧门店知识库", StoreID: store.ID, CompanyID: 9, Status: enums.StatusOk}
	setting := &models.StoreAIModelSetting{TenantID: 101, UsageCode: "reply_llm", StoreID: store.ID, CompanyID: 9, Status: enums.StatusOk}
	now := time.Now()
	currentResource := &models.KnowledgeResourceGroup{
		TenantID: 101, CompanyID: 0, StoreID: store.ID, KnowledgeBaseID: kb.ID,
		SourceProvider: "fastgpt_cloud", SourceRecordID: "resource-001", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)},
	}
	legacyResource := &models.KnowledgeResourceGroup{
		TenantID: 101, CompanyID: 9, StoreID: store.ID, KnowledgeBaseID: kb.ID,
		SourceProvider: "fastgpt_cloud", SourceRecordID: "resource-001", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
	}
	fastGPTStore := &models.FastGPTStoreTenant{
		TenantID: 101, CompanyID: 9, StoreID: store.ID, TenantTeamID: "team-001", Status: "ready",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	fastGPTSync := &models.FastGPTUsageSyncState{
		TenantID: 101, CompanyID: 9, StoreID: store.ID, KnowledgeBaseID: kb.ID,
		TenantTeamID: "team-001", CreatedAt: now, UpdatedAt: now,
	}
	fastGPTJob := &models.FastGPTDatasetJob{
		TenantID: 101, TaskKey: "legacy-company-job", CompanyID: 9, StoreID: store.ID,
		KnowledgeBaseID: kb.ID, Action: "create_dataset", Status: "completed", CreatedAt: now, UpdatedAt: now,
	}
	intent := &models.ReplyIntentConfig{Code: "legacy-company-intent", Name: "旧公司意图", ScopeType: "company", CompanyID: 9, Status: enums.StatusOk}
	customer := &models.Customer{TenantID: 101, Name: "历史客户", CompanyID: 9, Status: enums.StatusOk}
	permission := &models.Permission{Name: "查看客户企业", Code: "company.view", Type: "api", Status: enums.StatusOk}
	for name, item := range map[string]any{
		"team": team, "knowledge": kb, "model setting": setting,
		"intent": intent, "customer": customer, "permission": permission,
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if err := db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error; err != nil {
		t.Fatalf("create company role permission: %v", err)
	}
	currentResource.KnowledgeBaseID = kb.ID
	legacyResource.KnowledgeBaseID = kb.ID
	fastGPTSync.KnowledgeBaseID = kb.ID
	fastGPTJob.KnowledgeBaseID = kb.ID
	for name, item := range map[string]any{
		"current resource": currentResource, "legacy resource": legacyResource,
		"fastgpt store": fastGPTStore, "fastgpt sync": fastGPTSync, "fastgpt job": fastGPTJob,
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if err := db.Create(&models.KnowledgeResourceItem{TenantID: 101, KnowledgeResourceGroupID: currentResource.ID, AssetID: "old-resource", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create current resource item: %v", err)
	}
	if err := db.Create(&models.KnowledgeResourceItem{TenantID: 101, KnowledgeResourceGroupID: legacyResource.ID, AssetID: "new-resource", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create legacy resource item: %v", err)
	}

	if err := retireLegacyCompanyStoreScopes(db); err != nil {
		t.Fatalf("retire scopes: %v", err)
	}
	if err := retireLegacyCompanyStoreScopes(db); err != nil {
		t.Fatalf("second retire scopes: %v", err)
	}

	assertLegacyCompanyFieldZero(t, db, "store", &models.Store{}, store.ID)
	assertLegacyCompanyFieldZero(t, db, "binding", &models.StoreStaffBinding{}, binding.ID)
	assertLegacyCompanyFieldZero(t, db, "instance", &models.WxWorkProtocolInstance{}, instance.ID)
	assertLegacyCompanyFieldZero(t, db, "knowledge", &models.KnowledgeBase{}, kb.ID)
	assertLegacyCompanyFieldZero(t, db, "model setting", &models.StoreAIModelSetting{}, setting.ID)
	assertLegacyCompanyFieldZero(t, db, "fastgpt store", &models.FastGPTStoreTenant{}, fastGPTStore.ID)
	assertLegacyCompanyFieldZero(t, db, "fastgpt sync", &models.FastGPTUsageSyncState{}, fastGPTSync.ID)
	assertLegacyCompanyFieldZero(t, db, "fastgpt job", &models.FastGPTDatasetJob{}, fastGPTJob.ID)
	var resourceGroups []models.KnowledgeResourceGroup
	if err := db.Where("tenant_id = ? AND store_id = ? AND knowledge_base_id = ? AND source_record_id = ?", 101, store.ID, kb.ID, "resource-001").Find(&resourceGroups).Error; err != nil {
		t.Fatalf("load collapsed knowledge resources: %v", err)
	}
	if len(resourceGroups) != 1 || resourceGroups[0].ID != legacyResource.ID || resourceGroups[0].CompanyID != 0 {
		t.Fatalf("knowledge resource scopes were not collapsed deterministically: %+v", resourceGroups)
	}
	var resourceItems []models.KnowledgeResourceItem
	if err := db.Where("knowledge_resource_group_id = ?", legacyResource.ID).Find(&resourceItems).Error; err != nil {
		t.Fatalf("load retained knowledge resource items: %v", err)
	}
	if len(resourceItems) != 1 || resourceItems[0].AssetID != "new-resource" {
		t.Fatalf("latest active knowledge resource was not retained: %+v", resourceItems)
	}

	var validBinding models.StoreStaffBinding
	db.First(&validBinding, binding.ID)
	if validBinding.Status != enums.StatusOk {
		t.Fatalf("valid binding status=%d", validBinding.Status)
	}
	var validInstance models.WxWorkProtocolInstance
	db.First(&validInstance, instance.ID)
	if validInstance.Status != enums.StatusOk || !validInstance.AIReplyEnabled {
		t.Fatalf("valid instance was disabled: status=%d ai=%v", validInstance.Status, validInstance.AIReplyEnabled)
	}
	assertRetiredBindingAndInstance(t, db, orphanBinding.ID, orphanInstance.ID, "缺少系统账号")
	assertRetiredBindingAndInstance(t, db, duplicateBinding.ID, duplicateInstance.ID, "多个历史门店绑定")

	var currentTeam models.AgentTeam
	db.First(&currentTeam, team.ID)
	if currentTeam.CompanyScopeIDs != "" {
		t.Fatalf("team company scopes=%q", currentTeam.CompanyScopeIDs)
	}
	var currentIntent models.ReplyIntentConfig
	db.First(&currentIntent, intent.ID)
	if currentIntent.Status != enums.StatusDisabled {
		t.Fatalf("company intent status=%d", currentIntent.Status)
	}
	var currentCompany models.Company
	db.First(&currentCompany, legacyCompany.ID)
	if currentCompany.IntentProfileID != 0 || currentCompany.Name != legacyCompany.Name {
		t.Fatalf("legacy company runtime profile not retired safely: %+v", currentCompany)
	}
	var currentCustomer models.Customer
	db.First(&currentCustomer, customer.ID)
	if currentCustomer.CompanyID != 9 {
		t.Fatalf("historical customer evidence changed: %d", currentCustomer.CompanyID)
	}
	var permissionCount int64
	if err := db.Model(&models.Permission{}).Where("code = ?", "company.view").Count(&permissionCount).Error; err != nil {
		t.Fatalf("count retired permission: %v", err)
	}
	if permissionCount != 0 {
		t.Fatalf("company permission still active: %d", permissionCount)
	}
	var rolePermissionCount int64
	if err := db.Model(&models.RolePermission{}).Where("permission_id = ?", permission.ID).Count(&rolePermissionCount).Error; err != nil {
		t.Fatalf("count retired role permission: %v", err)
	}
	if rolePermissionCount != 0 {
		t.Fatalf("company role permission still active: %d", rolePermissionCount)
	}
	var backfilledBinding models.StoreStaffBinding
	if err := db.Where("tenant_id = ? AND user_id = ? AND status = ?", 101, roleOnlyUser.ID, enums.StatusOk).First(&backfilledBinding).Error; err != nil {
		t.Fatalf("load backfilled store identity: %v", err)
	}
	var backfilledStore models.Store
	if err := db.First(&backfilledStore, backfilledBinding.StoreID).Error; err != nil {
		t.Fatalf("load backfilled store: %v", err)
	}
	if backfilledStore.TenantID != 101 || backfilledStore.CompanyID != 0 || backfilledStore.Name != roleOnlyUser.Nickname {
		t.Fatalf("unexpected backfilled store: %+v", backfilledStore)
	}
}

func createLegacyCompanyStore(t *testing.T, db *gorm.DB, code string) *models.Store {
	t.Helper()
	store := &models.Store{TenantID: 101, StoreCode: code, Name: code, CompanyID: 9, Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store %s: %v", code, err)
	}
	return store
}

func assertLegacyCompanyFieldZero(t *testing.T, db *gorm.DB, name string, item any, id int64) {
	t.Helper()
	if err := db.First(item, "id = ?", id).Error; err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	var companyID int64
	switch value := item.(type) {
	case *models.Store:
		companyID = value.CompanyID
	case *models.StoreStaffBinding:
		companyID = value.CompanyID
	case *models.WxWorkProtocolInstance:
		companyID = value.CompanyID
	case *models.KnowledgeBase:
		companyID = value.CompanyID
	case *models.StoreAIModelSetting:
		companyID = value.CompanyID
	case *models.KnowledgeResourceGroup:
		companyID = value.CompanyID
	case *models.FastGPTStoreTenant:
		companyID = value.CompanyID
	case *models.FastGPTUsageSyncState:
		companyID = value.CompanyID
	case *models.FastGPTDatasetJob:
		companyID = value.CompanyID
	}
	if companyID != 0 {
		t.Fatalf("%s company=%d", name, companyID)
	}
}

func assertRetiredBindingAndInstance(t *testing.T, db *gorm.DB, bindingID, instanceID int64, reason string) {
	t.Helper()
	var binding models.StoreStaffBinding
	if err := db.First(&binding, bindingID).Error; err != nil {
		t.Fatalf("load retired binding: %v", err)
	}
	if binding.Status != enums.StatusDisabled || !strings.Contains(binding.Remark, reason) {
		t.Fatalf("retired binding status=%d remark=%q", binding.Status, binding.Remark)
	}
	var instance models.WxWorkProtocolInstance
	if err := db.First(&instance, instanceID).Error; err != nil {
		t.Fatalf("load retired instance: %v", err)
	}
	if instance.Status != enums.StatusDisabled || instance.AIReplyEnabled || instance.HealthStatus != "pending_binding" {
		t.Fatalf("retired instance status=%d ai=%v health=%q", instance.Status, instance.AIReplyEnabled, instance.HealthStatus)
	}
}
