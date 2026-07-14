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

func TestBackfillCustomerDomainTenantsUsesParentsAndIsIdempotent(t *testing.T) {
	db := setupCustomerDomainTenantBackfillDB(t)
	legacy := createCustomerDomainTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createCustomerDomainTenant(t, db, "customer-a")
	tenantB := createCustomerDomainTenant(t, db, "customer-b")
	companyA := createCustomerDomainCompany(t, db, tenantA.ID, "company-a")
	channelB := createCustomerDomainChannel(t, db, tenantB.ID, "channel-b")

	companyCustomer := createCustomerDomainCustomer(t, db, 0, companyA.ID, "company-customer")
	channelCustomer := createCustomerDomainCustomer(t, db, 0, 0, "channel-customer")
	legacyCustomer := createCustomerDomainCustomer(t, db, 0, 0, "legacy-customer")
	explicitCustomer := createCustomerDomainCustomer(t, db, tenantB.ID, 0, "explicit-customer")
	if err := db.Create(&models.Conversation{CustomerID: channelCustomer.ID, ChannelID: channelB.ID, Status: enums.IMConversationStatusClosed, AuditFields: customerDomainBackfillAuditFields()}).Error; err != nil {
		t.Fatalf("create customer conversation: %v", err)
	}
	identity := &models.CustomerIdentity{CustomerID: companyCustomer.ID, ExternalSource: enums.ExternalSourceUser, ExternalID: "identity-a", Status: enums.StatusOk, AuditFields: customerDomainBackfillAuditFields()}
	contact := &models.CustomerContact{CustomerID: channelCustomer.ID, ContactType: enums.ContactTypeMobile, ContactValue: "13800000000", Status: enums.StatusOk, AuditFields: customerDomainBackfillAuditFields()}
	relation := &models.StoreCustomerRelation{CustomerID: legacyCustomer.ID, StoreID: 99, Status: enums.StatusOk, AuditFields: customerDomainBackfillAuditFields()}
	explicitIdentity := &models.CustomerIdentity{TenantID: tenantB.ID, CustomerID: explicitCustomer.ID, ExternalSource: enums.ExternalSourceGuest, ExternalID: "explicit-identity", Status: enums.StatusOk, AuditFields: customerDomainBackfillAuditFields()}
	if err := db.Create(identity).Error; err != nil {
		t.Fatalf("create identity: %v", err)
	}
	if err := db.Create(contact).Error; err != nil {
		t.Fatalf("create contact: %v", err)
	}
	if err := db.Create(relation).Error; err != nil {
		t.Fatalf("create relation: %v", err)
	}
	if err := db.Create(explicitIdentity).Error; err != nil {
		t.Fatalf("create explicit identity: %v", err)
	}

	if err := db.Transaction(backfillCustomerDomainTenants); err != nil {
		t.Fatalf("backfill customer domain tenants: %v", err)
	}
	if err := db.Transaction(backfillCustomerDomainTenants); err != nil {
		t.Fatalf("repeat customer domain tenant backfill: %v", err)
	}

	assertCustomerDomainTenant(t, db, &models.Customer{}, companyCustomer.ID, tenantA.ID)
	assertCustomerDomainTenant(t, db, &models.Customer{}, channelCustomer.ID, tenantB.ID)
	assertCustomerDomainTenant(t, db, &models.Customer{}, legacyCustomer.ID, legacy.ID)
	assertCustomerDomainTenant(t, db, &models.Customer{}, explicitCustomer.ID, tenantB.ID)
	assertCustomerDomainTenant(t, db, &models.CustomerIdentity{}, identity.ID, tenantA.ID)
	assertCustomerDomainTenant(t, db, &models.CustomerIdentity{}, explicitIdentity.ID, tenantB.ID)
	assertCustomerDomainTenant(t, db, &models.CustomerContact{}, contact.ID, tenantB.ID)
	assertCustomerDomainTenant(t, db, &models.StoreCustomerRelation{}, relation.ID, legacy.ID)
}

func TestBackfillCustomerDomainTenantsRejectsCrossTenantParentsAndRollsBack(t *testing.T) {
	db := setupCustomerDomainTenantBackfillDB(t)
	createCustomerDomainTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createCustomerDomainTenant(t, db, "conflict-a")
	tenantB := createCustomerDomainTenant(t, db, "conflict-b")
	companyA := createCustomerDomainCompany(t, db, tenantA.ID, "conflict-company")
	channelB := createCustomerDomainChannel(t, db, tenantB.ID, "conflict-channel")
	good := createCustomerDomainCustomer(t, db, 0, companyA.ID, "good-before-conflict")
	conflict := createCustomerDomainCustomer(t, db, 0, companyA.ID, "conflicting-customer")
	if err := db.Create(&models.Conversation{CustomerID: conflict.ID, ChannelID: channelB.ID, Status: enums.IMConversationStatusClosed, AuditFields: customerDomainBackfillAuditFields()}).Error; err != nil {
		t.Fatalf("create conflicting conversation: %v", err)
	}

	err := db.Transaction(backfillCustomerDomainTenants)
	if err == nil || !strings.Contains(err.Error(), "conflicts with channel") {
		t.Fatalf("backfill error=%v want cross-tenant conflict", err)
	}
	assertCustomerDomainTenant(t, db, &models.Customer{}, good.ID, 0)
	assertCustomerDomainTenant(t, db, &models.Customer{}, conflict.ID, 0)
}

func TestBackfillCustomerDomainTenantsRejectsOrphanChildAndRollsBack(t *testing.T) {
	db := setupCustomerDomainTenantBackfillDB(t)
	createCustomerDomainTenant(t, db, constants.LegacyDefaultTenantCode)
	customer := createCustomerDomainCustomer(t, db, 0, 0, "rollback-customer")
	orphan := &models.CustomerContact{CustomerID: 999999, ContactType: enums.ContactTypeEmail, ContactValue: "orphan@example.com", Status: enums.StatusOk, AuditFields: customerDomainBackfillAuditFields()}
	if err := db.Create(orphan).Error; err != nil {
		t.Fatalf("create orphan contact: %v", err)
	}

	err := db.Transaction(backfillCustomerDomainTenants)
	if err == nil || !strings.Contains(err.Error(), "references missing customer") {
		t.Fatalf("backfill error=%v want orphan rejection", err)
	}
	assertCustomerDomainTenant(t, db, &models.Customer{}, customer.ID, 0)
	assertCustomerDomainTenant(t, db, &models.CustomerContact{}, orphan.ID, 0)
}

func setupCustomerDomainTenantBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "customer-domain-backfill.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Tenant{}, &models.Company{}, &models.Channel{}, &models.Customer{}, &models.CustomerIdentity{},
		&models.CustomerContact{}, &models.StoreCustomerRelation{}, &models.Conversation{},
	); err != nil {
		t.Fatalf("migrate customer domain backfill tables: %v", err)
	}
	return db
}

func createCustomerDomainTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	item := &models.Tenant{
		TenantCode: code, LegalName: code, ShortName: code, RegistrationType: "test", RegistrationNo: "REG-" + code,
		VerificationStatus: enums.TenantVerificationStatusVerified, Status: enums.StatusOk, AuditFields: customerDomainBackfillAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return item
}

func createCustomerDomainCompany(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.Company {
	t.Helper()
	item := &models.Company{TenantID: tenantID, Name: name, Code: name, Status: enums.StatusOk, AuditFields: customerDomainBackfillAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create company %s: %v", name, err)
	}
	return item
}

func createCustomerDomainChannel(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.Channel {
	t.Helper()
	item := &models.Channel{TenantID: tenantID, Name: name, ChannelType: enums.ChannelTypeWeb, ChannelID: name, Status: enums.StatusOk, AuditFields: customerDomainBackfillAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	return item
}

func createCustomerDomainCustomer(t *testing.T, db *gorm.DB, tenantID, companyID int64, name string) *models.Customer {
	t.Helper()
	item := &models.Customer{TenantID: tenantID, CompanyID: companyID, Name: name, Status: enums.StatusOk, AuditFields: customerDomainBackfillAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create customer %s: %v", name, err)
	}
	return item
}

func assertCustomerDomainTenant(t *testing.T, db *gorm.DB, model any, id, wantTenantID int64) {
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

func customerDomainBackfillAuditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, UpdatedAt: now}
}
