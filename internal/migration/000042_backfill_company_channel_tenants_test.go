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

func TestBackfillCompanyAndChannelTenantsIsIdempotentAndPreservesExplicitValues(t *testing.T) {
	db := setupCompanyChannelTenantBackfillDB(t)
	legacyTenant := createCompanyChannelTenant(t, db, constants.LegacyDefaultTenantCode)
	explicitTenant := createCompanyChannelTenant(t, db, "explicit-tenant")

	historicalCompany := createTenantBackfillCompany(t, db, 0, "historical-company")
	explicitCompany := createTenantBackfillCompany(t, db, explicitTenant.ID, "explicit-company")
	historicalChannel := createTenantBackfillChannel(t, db, 0, "historical-channel")
	explicitChannel := createTenantBackfillChannel(t, db, explicitTenant.ID, "explicit-channel")

	if err := db.Transaction(backfillCompanyAndChannelTenants); err != nil {
		t.Fatalf("backfill company and channel tenants: %v", err)
	}
	if err := db.Transaction(backfillCompanyAndChannelTenants); err != nil {
		t.Fatalf("repeat company and channel tenant backfill: %v", err)
	}

	assertCompanyChannelTenant(t, db, &models.Company{}, historicalCompany.ID, legacyTenant.ID)
	assertCompanyChannelTenant(t, db, &models.Company{}, explicitCompany.ID, explicitTenant.ID)
	assertCompanyChannelTenant(t, db, &models.Channel{}, historicalChannel.ID, legacyTenant.ID)
	assertCompanyChannelTenant(t, db, &models.Channel{}, explicitChannel.ID, explicitTenant.ID)
}

func TestBackfillCompanyAndChannelTenantsRejectsMissingTenantAndRollsBack(t *testing.T) {
	db := setupCompanyChannelTenantBackfillDB(t)
	createCompanyChannelTenant(t, db, constants.LegacyDefaultTenantCode)
	historicalCompany := createTenantBackfillCompany(t, db, 0, "rollback-company")
	invalidChannel := createTenantBackfillChannel(t, db, 999999, "invalid-channel")

	err := db.Transaction(backfillCompanyAndChannelTenants)
	if err == nil || !strings.Contains(err.Error(), "references missing tenant") {
		t.Fatalf("backfill error=%v want missing tenant rejection", err)
	}
	assertCompanyChannelTenant(t, db, &models.Company{}, historicalCompany.ID, 0)
	assertCompanyChannelTenant(t, db, &models.Channel{}, invalidChannel.ID, invalidChannel.TenantID)
}

func setupCompanyChannelTenantBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "company-channel-backfill.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Tenant{}, &models.Company{}, &models.Channel{}); err != nil {
		t.Fatalf("migrate company and channel backfill tables: %v", err)
	}
	return db
}

func createCompanyChannelTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	tenant := &models.Tenant{
		TenantCode: code, LegalName: code, ShortName: code, RegistrationType: "test", RegistrationNo: "REG-" + code,
		VerificationStatus: enums.TenantVerificationStatusVerified, Status: enums.StatusOk, AuditFields: companyChannelBackfillAuditFields(),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return tenant
}

func createTenantBackfillCompany(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.Company {
	t.Helper()
	company := &models.Company{TenantID: tenantID, Name: name, Code: name, Status: enums.StatusOk, AuditFields: companyChannelBackfillAuditFields()}
	if err := db.Create(company).Error; err != nil {
		t.Fatalf("create company %s: %v", name, err)
	}
	return company
}

func createTenantBackfillChannel(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.Channel {
	t.Helper()
	channel := &models.Channel{
		TenantID: tenantID, Name: name, ChannelType: enums.ChannelTypeWeb, ChannelID: name, Status: enums.StatusOk,
		AuditFields: companyChannelBackfillAuditFields(),
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	return channel
}

func assertCompanyChannelTenant(t *testing.T, db *gorm.DB, model any, id, wantTenantID int64) {
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

func companyChannelBackfillAuditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, UpdatedAt: now}
}
