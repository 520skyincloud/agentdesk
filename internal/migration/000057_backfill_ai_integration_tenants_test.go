package migration

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestBackfillIntegratedAIFeatureTenantsUsesDurableParentsAndIsIdempotent(t *testing.T) {
	db := setupIntegratedAITenantBackfillDB(t)
	createIntegratedAITenant(t, db, constants.LegacyDefaultTenantCode)
	tenant := createIntegratedAITenant(t, db, "integrated-ai-a")
	company, store, knowledgeBase, customer, instance, conversation, message := createIntegratedAIParents(t, db, tenant.ID, "a")

	setting := &models.WxWorkCustomerHandoffSetting{CustomerID: customer.ID, WxWorkInstanceID: instance.ID, AutoHandoffEnabled: true, AuditFields: integratedAIAuditFields()}
	resume := &models.AIManualResumeTask{TaskKey: "resume-a", ConversationID: conversation.ID, WxWorkInstanceID: instance.ID, OriginMessageID: message.ID, LatestWaitingMessageID: message.ID, AuditFields: integratedAIAuditFields()}
	group := &models.KnowledgeResourceGroup{CompanyID: company.ID, StoreID: store.ID, KnowledgeBaseID: knowledgeBase.ID, SourceRecordID: "source-a", Status: enums.StatusOk, AuditFields: integratedAIAuditFields()}
	job := &models.FastGPTDatasetJob{TaskKey: "job-a", CompanyID: company.ID, StoreID: store.ID, KnowledgeBaseID: knowledgeBase.ID, Status: "pending", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	usage := &models.AIUsageEvent{EventKey: "usage-a", CompanyID: company.ID, StoreID: store.ID, WxWorkInstanceID: instance.ID, ConversationID: conversation.ID, MessageID: message.ID, KnowledgeBaseID: knowledgeBase.ID, CreatedAt: time.Now()}
	gateway := &models.AIUsageGatewayCall{CallKey: "gateway-a", CompanyID: company.ID, StoreID: store.ID, WxWorkInstanceID: instance.ID, ConversationID: conversation.ID, MessageID: message.ID, StartedAt: time.Now(), FinishedAt: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	platformGateway := &models.AIUsageGatewayCall{CallKey: "gateway-platform", Stage: "fastgpt_internal_model", StartedAt: time.Now(), FinishedAt: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	for _, item := range []any{setting, resume, group, job, usage, gateway, platformGateway} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create %T: %v", item, err)
		}
	}
	resourceItem := &models.KnowledgeResourceItem{KnowledgeResourceGroupID: group.ID, AssetID: "asset-a", Status: enums.StatusOk, AuditFields: integratedAIAuditFields()}
	if err := db.Create(resourceItem).Error; err != nil {
		t.Fatalf("create resource item: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := db.Transaction(backfillIntegratedAIFeatureTenants); err != nil {
			t.Fatalf("backfill pass %d: %v", i+1, err)
		}
	}
	for _, check := range []struct {
		model any
		id    int64
	}{
		{&models.WxWorkCustomerHandoffSetting{}, setting.ID}, {&models.AIManualResumeTask{}, resume.ID},
		{&models.KnowledgeResourceGroup{}, group.ID}, {&models.KnowledgeResourceItem{}, resourceItem.ID},
		{&models.FastGPTDatasetJob{}, job.ID}, {&models.AIUsageEvent{}, usage.ID}, {&models.AIUsageGatewayCall{}, gateway.ID},
	} {
		assertIntegratedAITenant(t, db, check.model, check.id, tenant.ID)
	}
	assertIntegratedAITenant(t, db, &models.AIUsageGatewayCall{}, platformGateway.ID, 0)
}

func TestBackfillIntegratedAIFeatureTenantsRejectsConflictingEvidence(t *testing.T) {
	db := setupIntegratedAITenantBackfillDB(t)
	createIntegratedAITenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createIntegratedAITenant(t, db, "integrated-ai-conflict-a")
	tenantB := createIntegratedAITenant(t, db, "integrated-ai-conflict-b")
	companyA, _, _, _, _, _, _ := createIntegratedAIParents(t, db, tenantA.ID, "conflict-a")
	_, storeB, _, _, _, _, _ := createIntegratedAIParents(t, db, tenantB.ID, "conflict-b")
	valid := &models.AIUsageEvent{EventKey: "usage-valid", CompanyID: companyA.ID, CreatedAt: time.Now()}
	conflict := &models.AIUsageEvent{EventKey: "usage-conflict", CompanyID: companyA.ID, StoreID: storeB.ID, CreatedAt: time.Now()}
	if err := db.Create(valid).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(conflict).Error; err != nil {
		t.Fatal(err)
	}
	err := db.Transaction(backfillIntegratedAIFeatureTenants)
	if err == nil || !strings.Contains(err.Error(), "conflicts with store") {
		t.Fatalf("backfill error=%v want tenant conflict", err)
	}
	assertIntegratedAITenant(t, db, &models.AIUsageEvent{}, valid.ID, 0)
}

func setupIntegratedAITenantBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "integrated-ai-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.Tenant{}, &models.Company{}, &models.Store{}, &models.KnowledgeBase{}, &models.Customer{},
		&models.WxWorkProtocolInstance{}, &models.Conversation{}, &models.Message{},
		&models.WxWorkCustomerHandoffSetting{}, &models.AIManualResumeTask{}, &models.KnowledgeResourceGroup{},
		&models.KnowledgeResourceItem{}, &models.FastGPTDatasetJob{}, &models.AIUsageEvent{}, &models.AIUsageGatewayCall{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func createIntegratedAITenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	item := &models.Tenant{TenantCode: code, LegalName: code, ShortName: code, RegistrationType: "test", RegistrationNo: code, Status: enums.StatusOk, AuditFields: integratedAIAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatal(err)
	}
	return item
}

func createIntegratedAIParents(t *testing.T, db *gorm.DB, tenantID int64, suffix string) (*models.Company, *models.Store, *models.KnowledgeBase, *models.Customer, *models.WxWorkProtocolInstance, *models.Conversation, *models.Message) {
	t.Helper()
	company := &models.Company{TenantID: tenantID, Name: "company-" + suffix, Status: enums.StatusOk, AuditFields: integratedAIAuditFields()}
	if err := db.Create(company).Error; err != nil {
		t.Fatal(err)
	}
	store := &models.Store{TenantID: tenantID, StoreCode: "store-" + suffix, Name: "store-" + suffix, CompanyID: company.ID, Status: enums.StatusOk, AuditFields: integratedAIAuditFields()}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	kb := &models.KnowledgeBase{TenantID: tenantID, Name: "kb-" + suffix, CompanyID: company.ID, StoreID: store.ID, Status: enums.StatusOk, AuditFields: integratedAIAuditFields()}
	if err := db.Create(kb).Error; err != nil {
		t.Fatal(err)
	}
	customer := &models.Customer{TenantID: tenantID, Name: "customer-" + suffix, Status: enums.StatusOk, AuditFields: integratedAIAuditFields()}
	if err := db.Create(customer).Error; err != nil {
		t.Fatal(err)
	}
	instance := &models.WxWorkProtocolInstance{TenantID: tenantID, Guid: "instance-" + suffix, CompanyID: company.ID, StoreID: store.ID, KnowledgeBaseID: kb.ID, Status: enums.StatusOk, AuditFields: integratedAIAuditFields()}
	if err := db.Create(instance).Error; err != nil {
		t.Fatal(err)
	}
	conversation := &models.Conversation{TenantID: tenantID, CustomerID: customer.ID, Status: enums.IMConversationStatusAIServing, AuditFields: integratedAIAuditFields()}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	message := &models.Message{TenantID: tenantID, ConversationID: conversation.ID, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, SentAt: &now, AuditFields: integratedAIAuditFields()}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	return company, store, kb, customer, instance, conversation, message
}

func assertIntegratedAITenant(t *testing.T, db *gorm.DB, model any, id, want int64) {
	t.Helper()
	var row struct{ TenantID int64 }
	if err := db.Model(model).Select("tenant_id").Where("id = ?", id).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.TenantID != want {
		t.Fatalf("%T %d tenant=%d want=%d", model, id, row.TenantID, want)
	}
}

func integratedAIAuditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, CreateUserName: "test", UpdatedAt: now, UpdateUserName: "test"}
}
