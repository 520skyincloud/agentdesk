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

func TestBackfillStoreAndWxWorkTenantsUsesAllParentsAndIsIdempotent(t *testing.T) {
	db := setupStoreWxWorkTenantBackfillDB(t)
	legacy := createStoreWxWorkTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createStoreWxWorkTenant(t, db, "store-a")
	tenantB := createStoreWxWorkTenant(t, db, "store-b")
	companyA := createStoreWxWorkCompany(t, db, tenantA.ID, "store-company-a")
	channelA := createStoreWxWorkChannel(t, db, tenantA.ID, "store-channel-a")
	userA := createStoreWxWorkUser(t, db, tenantA.ID, "store-user-a")
	teamA := createStoreWxWorkTeam(t, db, tenantA.ID, "store-team-a")
	customerB := createStoreWxWorkCustomer(t, db, tenantB.ID, "store-customer-b")

	companyStore := createStoreWxWorkStore(t, db, 0, companyA.ID, "company-store")
	relationStore := createStoreWxWorkStore(t, db, 0, 0, "relation-store")
	legacyStore := createStoreWxWorkStore(t, db, 0, 0, "legacy-store")
	explicitStore := createStoreWxWorkStore(t, db, tenantB.ID, 0, "explicit-store")
	relation := &models.StoreCustomerRelation{
		TenantID: tenantB.ID, CustomerID: customerB.ID, StoreID: relationStore.ID, Status: enums.StatusOk,
		AuditFields: storeWxWorkBackfillAuditFields(),
	}
	if err := db.Create(relation).Error; err != nil {
		t.Fatalf("create store customer relation: %v", err)
	}
	binding := &models.StoreStaffBinding{
		UserID: userA.ID, AgentTeamID: teamA.ID, CompanyID: companyA.ID, StoreID: companyStore.ID,
		Status: enums.StatusOk, AuditFields: storeWxWorkBackfillAuditFields(),
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create store staff binding: %v", err)
	}
	explicitBinding := &models.StoreStaffBinding{
		TenantID: tenantB.ID, StoreID: explicitStore.ID, Status: enums.StatusOk,
		AuditFields: storeWxWorkBackfillAuditFields(),
	}
	if err := db.Create(explicitBinding).Error; err != nil {
		t.Fatalf("create explicit store staff binding: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		Guid: "store-instance-a", ChannelID: channelA.ID, CompanyID: companyA.ID, StoreID: companyStore.ID,
		StoreStaffBindingID: binding.ID, AgentTeamID: teamA.ID, Status: enums.StatusOk,
		AuditFields: storeWxWorkBackfillAuditFields(),
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create wxwork instance: %v", err)
	}
	legacyInstance := &models.WxWorkProtocolInstance{
		Guid: "store-instance-legacy", Status: enums.StatusDisabled, AuditFields: storeWxWorkBackfillAuditFields(),
	}
	if err := db.Create(legacyInstance).Error; err != nil {
		t.Fatalf("create legacy wxwork instance: %v", err)
	}

	if err := db.Transaction(backfillStoreAndWxWorkTenants); err != nil {
		t.Fatalf("backfill store and wxwork tenants: %v", err)
	}
	if err := db.Transaction(backfillStoreAndWxWorkTenants); err != nil {
		t.Fatalf("repeat store and wxwork tenant backfill: %v", err)
	}

	assertStoreWxWorkTenant(t, db, &models.Store{}, companyStore.ID, tenantA.ID)
	assertStoreWxWorkTenant(t, db, &models.Store{}, relationStore.ID, tenantB.ID)
	assertStoreWxWorkTenant(t, db, &models.Store{}, legacyStore.ID, legacy.ID)
	assertStoreWxWorkTenant(t, db, &models.Store{}, explicitStore.ID, tenantB.ID)
	assertStoreWxWorkTenant(t, db, &models.StoreStaffBinding{}, binding.ID, tenantA.ID)
	assertStoreWxWorkTenant(t, db, &models.StoreStaffBinding{}, explicitBinding.ID, tenantB.ID)
	assertStoreWxWorkTenant(t, db, &models.WxWorkProtocolInstance{}, instance.ID, tenantA.ID)
	assertStoreWxWorkTenant(t, db, &models.WxWorkProtocolInstance{}, legacyInstance.ID, legacy.ID)
}

func TestBackfillStoreAndWxWorkTenantsRejectsCrossTenantEvidenceAndRollsBack(t *testing.T) {
	db := setupStoreWxWorkTenantBackfillDB(t)
	createStoreWxWorkTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createStoreWxWorkTenant(t, db, "store-conflict-a")
	tenantB := createStoreWxWorkTenant(t, db, "store-conflict-b")
	companyA := createStoreWxWorkCompany(t, db, tenantA.ID, "store-conflict-company")
	channelB := createStoreWxWorkChannel(t, db, tenantB.ID, "store-conflict-channel")
	good := createStoreWxWorkStore(t, db, 0, companyA.ID, "store-good-before-conflict")
	conflict := createStoreWxWorkStore(t, db, 0, companyA.ID, "store-conflict")
	instance := &models.WxWorkProtocolInstance{
		Guid: "store-conflict-instance", ChannelID: channelB.ID, StoreID: conflict.ID,
		Status: enums.StatusOk, AuditFields: storeWxWorkBackfillAuditFields(),
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create conflicting wxwork instance: %v", err)
	}

	err := db.Transaction(backfillStoreAndWxWorkTenants)
	if err == nil || !strings.Contains(err.Error(), "conflicts with channel") {
		t.Fatalf("backfill error=%v want cross-tenant conflict", err)
	}
	assertStoreWxWorkTenant(t, db, &models.Store{}, good.ID, 0)
	assertStoreWxWorkTenant(t, db, &models.Store{}, conflict.ID, 0)
	assertStoreWxWorkTenant(t, db, &models.WxWorkProtocolInstance{}, instance.ID, 0)
}

func TestBackfillStoreAndWxWorkTenantsRejectsOrphansAndInvalidExplicitTenant(t *testing.T) {
	db := setupStoreWxWorkTenantBackfillDB(t)
	createStoreWxWorkTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createStoreWxWorkTenant(t, db, "store-orphan-a")
	userA := createStoreWxWorkUser(t, db, tenantA.ID, "store-orphan-user")
	good := createStoreWxWorkStore(t, db, 0, 0, "store-rollback-good")
	orphanBinding := &models.StoreStaffBinding{
		UserID: userA.ID, StoreID: 999999, Status: enums.StatusOk, AuditFields: storeWxWorkBackfillAuditFields(),
	}
	if err := db.Create(orphanBinding).Error; err != nil {
		t.Fatalf("create orphan store binding: %v", err)
	}
	invalidInstance := &models.WxWorkProtocolInstance{
		TenantID: 888888, Guid: "store-invalid-explicit", Status: enums.StatusDisabled,
		AuditFields: storeWxWorkBackfillAuditFields(),
	}
	if err := db.Create(invalidInstance).Error; err != nil {
		t.Fatalf("create invalid explicit instance: %v", err)
	}

	err := db.Transaction(backfillStoreAndWxWorkTenants)
	if err == nil || !strings.Contains(err.Error(), "missing store") {
		t.Fatalf("backfill error=%v want missing store rejection", err)
	}
	assertStoreWxWorkTenant(t, db, &models.Store{}, good.ID, 0)
	assertStoreWxWorkTenant(t, db, &models.StoreStaffBinding{}, orphanBinding.ID, 0)
	assertStoreWxWorkTenant(t, db, &models.WxWorkProtocolInstance{}, invalidInstance.ID, invalidInstance.TenantID)

	if err := db.Delete(orphanBinding).Error; err != nil {
		t.Fatalf("delete orphan binding: %v", err)
	}
	err = db.Transaction(backfillStoreAndWxWorkTenants)
	if err == nil || !strings.Contains(err.Error(), "references missing tenant") {
		t.Fatalf("backfill error=%v want missing explicit tenant rejection", err)
	}
	assertStoreWxWorkTenant(t, db, &models.Store{}, good.ID, 0)
}

func setupStoreWxWorkTenantBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "store-wxwork-backfill.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Tenant{}, &models.Company{}, &models.Channel{}, &models.User{}, &models.AgentTeam{},
		&models.Customer{}, &models.Store{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{},
		&models.StoreCustomerRelation{},
	); err != nil {
		t.Fatalf("migrate store and wxwork backfill tables: %v", err)
	}
	return db
}

func createStoreWxWorkTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	item := &models.Tenant{
		TenantCode: code, LegalName: code, ShortName: code, RegistrationType: "test", RegistrationNo: "REG-" + code,
		VerificationStatus: enums.TenantVerificationStatusVerified, Status: enums.StatusOk,
		AuditFields: storeWxWorkBackfillAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return item
}

func createStoreWxWorkCompany(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.Company {
	t.Helper()
	item := &models.Company{TenantID: tenantID, Name: name, Code: name, Status: enums.StatusOk, AuditFields: storeWxWorkBackfillAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create company %s: %v", name, err)
	}
	return item
}

func createStoreWxWorkChannel(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.Channel {
	t.Helper()
	item := &models.Channel{
		TenantID: tenantID, Name: name, ChannelType: enums.ChannelTypeWxWorkProtocol, ChannelID: name,
		Status: enums.StatusOk, AuditFields: storeWxWorkBackfillAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	return item
}

func createStoreWxWorkUser(t *testing.T, db *gorm.DB, tenantID int64, username string) *models.User {
	t.Helper()
	item := &models.User{
		TenantID: tenantID, Username: username, Nickname: username, Password: "test", Status: enums.StatusOk,
		AuditFields: storeWxWorkBackfillAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return item
}

func createStoreWxWorkTeam(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.AgentTeam {
	t.Helper()
	item := &models.AgentTeam{TenantID: tenantID, Name: name, Status: enums.StatusOk, AuditFields: storeWxWorkBackfillAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create agent team %s: %v", name, err)
	}
	return item
}

func createStoreWxWorkCustomer(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.Customer {
	t.Helper()
	item := &models.Customer{TenantID: tenantID, Name: name, Status: enums.StatusOk, AuditFields: storeWxWorkBackfillAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create customer %s: %v", name, err)
	}
	return item
}

func createStoreWxWorkStore(t *testing.T, db *gorm.DB, tenantID, companyID int64, code string) *models.Store {
	t.Helper()
	item := &models.Store{
		TenantID: tenantID, StoreCode: code, Name: code, CompanyID: companyID, Status: enums.StatusOk,
		AuditFields: storeWxWorkBackfillAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create store %s: %v", code, err)
	}
	return item
}

func assertStoreWxWorkTenant(t *testing.T, db *gorm.DB, model any, id, wantTenantID int64) {
	t.Helper()
	var row struct {
		TenantID int64
	}
	if err := db.Model(model).Select("tenant_id").Where("id = ?", id).Take(&row).Error; err != nil {
		t.Fatalf("read tenant for %T %d: %v", model, id, err)
	}
	if row.TenantID != wantTenantID {
		t.Fatalf("%T %d tenant = %d, want %d", model, id, row.TenantID, wantTenantID)
	}
}

func storeWxWorkBackfillAuditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, UpdatedAt: now}
}
