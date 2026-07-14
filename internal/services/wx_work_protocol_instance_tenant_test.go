package services

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestWxWorkProtocolInstanceCRUDStaysInActiveTenant(t *testing.T) {
	db := setupWxWorkProtocolTenantDB(t)
	adminA := wxWorkProtocolTenantOperator(101, 1)
	adminB := wxWorkProtocolTenantOperator(202, 2)
	companyA := createWxWorkProtocolTenantCompany(t, db, 101, "企微租户A企业")
	companyB := createWxWorkProtocolTenantCompany(t, db, 202, "企微租户B企业")
	channelA := createWxWorkProtocolTenantChannel(t, db, 101, "wxwork-tenant-a")
	channelB := createWxWorkProtocolTenantChannel(t, db, 202, "wxwork-tenant-b")
	storeA := createWxWorkProtocolTenantStore(t, db, 101, companyA.ID, "WX-A")
	storeB := createWxWorkProtocolTenantStore(t, db, 202, companyB.ID, "WX-B")

	instanceA, err := WxWorkProtocolInstanceService.CreateInstance(request.CreateWxWorkProtocolInstanceRequest{
		Guid: "tenant-a-guid", ChannelID: channelA.ID, CompanyID: companyA.ID, StoreID: storeA.ID, Status: int(enums.StatusOk),
	}, adminA)
	if err != nil {
		t.Fatalf("create tenant A instance: %v", err)
	}
	instanceB, err := WxWorkProtocolInstanceService.CreateInstance(request.CreateWxWorkProtocolInstanceRequest{
		Guid: "tenant-b-guid", ChannelID: channelB.ID, CompanyID: companyB.ID, StoreID: storeB.ID, Status: int(enums.StatusOk),
	}, adminB)
	if err != nil {
		t.Fatalf("create tenant B instance: %v", err)
	}
	if instanceA.TenantID != 101 || instanceB.TenantID != 202 {
		t.Fatalf("unexpected instance tenants: A=%d B=%d", instanceA.TenantID, instanceB.TenantID)
	}
	if got := WxWorkProtocolInstanceService.GetInTenant(instanceB.ID, adminA); got != nil {
		t.Fatalf("tenant A read tenant B instance: %+v", got)
	}

	crossTenantCreates := []request.CreateWxWorkProtocolInstanceRequest{
		{Guid: "foreign-channel-guid", ChannelID: channelB.ID, CompanyID: companyA.ID},
		{Guid: "foreign-company-guid", ChannelID: channelA.ID, CompanyID: companyB.ID},
		{Guid: "foreign-store-guid", ChannelID: channelA.ID, CompanyID: companyA.ID, StoreID: storeB.ID},
		{Guid: instanceB.Guid, ChannelID: channelA.ID, CompanyID: companyA.ID},
	}
	for index, req := range crossTenantCreates {
		if _, createErr := WxWorkProtocolInstanceService.CreateInstance(req, adminA); createErr == nil {
			t.Fatalf("cross-tenant create %d unexpectedly succeeded", index)
		}
	}

	if err := WxWorkProtocolInstanceService.UpdateInstance(request.UpdateWxWorkProtocolInstanceRequest{
		ID: instanceB.ID,
		CreateWxWorkProtocolInstanceRequest: request.CreateWxWorkProtocolInstanceRequest{
			Guid: instanceB.Guid, ChannelID: channelA.ID, CompanyID: companyA.ID, Status: int(enums.StatusOk),
		},
	}, adminA); err == nil {
		t.Fatal("tenant A updated tenant B instance")
	}
	if err := WxWorkProtocolInstanceService.DeleteInstance(instanceB.ID, adminA); err == nil {
		t.Fatal("tenant A deleted tenant B instance")
	}
	if current := repositories.WxWorkProtocolInstanceRepository.GetInTenant(db, instanceB.ID, 202); current == nil || current.Status != enums.StatusOk {
		t.Fatalf("tenant B instance changed: %+v", current)
	}

	filtered := repositories.WxWorkProtocolInstanceRepository.Find(db,
		AgentTeamScopeService.ApplyWxWorkInstanceFilter(sqls.NewCnd().Asc("id"), adminA))
	if len(filtered) != 1 || filtered[0].ID != instanceA.ID {
		t.Fatalf("tenant A list=%+v want only instance %d", filtered, instanceA.ID)
	}
}

func TestWxWorkProtocolLoginClaimsOnlyUnownedInstance(t *testing.T) {
	db := setupWxWorkProtocolTenantDB(t)
	adminA := wxWorkProtocolTenantOperator(101, 1)
	channelA := createWxWorkProtocolTenantChannel(t, db, 101, "wxwork-claim-a")

	pending, err := WxWorkProtocolInstanceService.CreatePendingFromLogin("claimable-guid", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("create callback quarantine instance: %v", err)
	}
	if pending.TenantID != 0 || pending.HealthStatus != "pending_binding" {
		t.Fatalf("callback instance must remain quarantined: %+v", pending)
	}

	claimed, err := WxWorkProtocolInstanceService.CreateLoginInstance(request.StartWxWorkProtocolLoginRequest{
		ChannelID: channelA.ID,
		Guid:      pending.Guid,
	}, adminA)
	if err != nil {
		t.Fatalf("claim callback instance: %v", err)
	}
	if claimed.ID != pending.ID || claimed.TenantID != 101 || claimed.ChannelID != channelA.ID {
		t.Fatalf("claimed instance=%+v", claimed)
	}
	claimedByOther, err := repositories.WxWorkProtocolInstanceRepository.ClaimTenant(db, pending.ID, 202, nil)
	if err != nil {
		t.Fatalf("second tenant claim: %v", err)
	}
	if claimedByOther {
		t.Fatal("second tenant claimed an owned instance")
	}
}

func TestWxWorkProtocolRemoteSetupRejectsForeignCompanyBeforeGUIDClaim(t *testing.T) {
	db := setupWxWorkProtocolTenantDB(t)
	adminA := wxWorkProtocolTenantOperator(101, 1)
	companyB := createWxWorkProtocolTenantCompany(t, db, 202, "远程配置租户B企业")
	channelA := createWxWorkProtocolTenantChannel(t, db, 101, "wxwork-remote-a")
	pending, err := WxWorkProtocolInstanceService.CreatePendingFromLogin("remote-pending-guid", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("create pending instance: %v", err)
	}

	if _, err := WxWorkProtocolInstanceService.CreateRemoteSetupInstance(request.CreateWxWorkProtocolRemoteSetupRequest{
		ChannelID: channelA.ID,
		Guid:      pending.Guid,
		CompanyID: companyB.ID,
	}, adminA); err == nil {
		t.Fatal("foreign company remote setup unexpectedly succeeded")
	}
	current := repositories.WxWorkProtocolInstanceRepository.Get(db, pending.ID)
	if current == nil || current.TenantID != 0 || current.ChannelID != 0 || current.CompanyID != 0 {
		t.Fatalf("rejected remote setup mutated quarantined instance: %+v", current)
	}
}

func TestWxWorkProtocolRepositoryFinalPredicatesProtectTenant(t *testing.T) {
	db := setupWxWorkProtocolTenantDB(t)
	instance := &models.WxWorkProtocolInstance{TenantID: 202, Guid: "final-wxwork-guid", Status: enums.StatusOk}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	if err := repositories.WxWorkProtocolInstanceRepository.UpdatesInTenant(db, instance.ID, 101, map[string]any{"employee_name": "越权修改"}); err != nil {
		t.Fatalf("scoped update: %v", err)
	}
	if err := repositories.WxWorkProtocolInstanceRepository.DeleteInTenant(db, instance.ID, 101); err != nil {
		t.Fatalf("scoped delete: %v", err)
	}
	released, err := repositories.WxWorkProtocolInstanceRepository.ReleaseLoginBinding(db, instance.ID, 101, map[string]any{"status": enums.StatusDeleted})
	if err != nil {
		t.Fatalf("scoped login release: %v", err)
	}
	if released {
		t.Fatal("tenant A released tenant B login binding")
	}
	current := repositories.WxWorkProtocolInstanceRepository.GetInTenant(db, instance.ID, 202)
	if current == nil || current.EmployeeName != "" || current.Status != enums.StatusOk {
		t.Fatalf("tenant B instance changed: %+v", current)
	}
}

func setupWxWorkProtocolTenantDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "wxwork-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Company{}, &models.Store{}, &models.Channel{}, &models.StoreStaffBinding{},
		&models.WxWorkProtocolInstance{}, &models.WxWorkProtocolDevicePoolInstance{},
	); err != nil {
		t.Fatalf("migrate wxwork tenant models: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })
	return db
}

func wxWorkProtocolTenantOperator(tenantID, userID int64) *dto.AuthPrincipal {
	return &dto.AuthPrincipal{
		UserID: userID, Username: "tenant-admin", ActiveTenantID: tenantID,
		Roles: []string{constants.RoleCodeTenantAdmin},
	}
}

func createWxWorkProtocolTenantCompany(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.Company {
	t.Helper()
	item := &models.Company{TenantID: tenantID, Name: name, Code: name, Status: enums.StatusOk}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create company %s: %v", name, err)
	}
	return item
}

func createWxWorkProtocolTenantChannel(t *testing.T, db *gorm.DB, tenantID int64, channelID string) *models.Channel {
	t.Helper()
	item := &models.Channel{
		TenantID: tenantID, Name: channelID, ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID: channelID, Status: enums.StatusOk,
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create channel %s: %v", channelID, err)
	}
	return item
}

func createWxWorkProtocolTenantStore(t *testing.T, db *gorm.DB, tenantID, companyID int64, code string) *models.Store {
	t.Helper()
	item := &models.Store{TenantID: tenantID, CompanyID: companyID, StoreCode: code, Name: code, Status: enums.StatusOk}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create store %s: %v", code, err)
	}
	return item
}
