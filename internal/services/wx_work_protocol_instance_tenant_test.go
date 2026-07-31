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
	channelA := createWxWorkProtocolTenantChannel(t, db, 101, "wxwork-tenant-a")
	channelB := createWxWorkProtocolTenantChannel(t, db, 202, "wxwork-tenant-b")
	userA := createStoreStaffTenantUser(t, db, 101, "wxwork-store-user-a")
	userB := createStoreStaffTenantUser(t, db, 202, "wxwork-store-user-b")
	storeA := createWxWorkProtocolTenantStore(t, db, 101, "WX-A")
	storeB := createWxWorkProtocolTenantStore(t, db, 202, "WX-B")
	createStoreStaffTenantBinding(t, db, 101, userA.ID, 0, storeA.ID)
	createStoreStaffTenantBinding(t, db, 202, userB.ID, 0, storeB.ID)

	instanceA, err := WxWorkProtocolInstanceService.CreateInstance(request.CreateWxWorkProtocolInstanceRequest{
		Guid: "tenant-a-guid", ChannelID: channelA.ID, StoreStaffUserID: userA.ID, Status: int(enums.StatusOk),
	}, adminA)
	if err != nil {
		t.Fatalf("create tenant A instance: %v", err)
	}
	instanceB, err := WxWorkProtocolInstanceService.CreateInstance(request.CreateWxWorkProtocolInstanceRequest{
		Guid: "tenant-b-guid", ChannelID: channelB.ID, StoreStaffUserID: userB.ID, Status: int(enums.StatusOk),
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
		{Guid: "foreign-channel-guid", ChannelID: channelB.ID, StoreStaffUserID: userA.ID},
		{Guid: "foreign-user-guid", ChannelID: channelA.ID, StoreStaffUserID: userB.ID},
		{Guid: instanceB.Guid, ChannelID: channelA.ID, StoreStaffUserID: userA.ID},
	}
	for index, req := range crossTenantCreates {
		if _, createErr := WxWorkProtocolInstanceService.CreateInstance(req, adminA); createErr == nil {
			t.Fatalf("cross-tenant create %d unexpectedly succeeded", index)
		}
	}

	if err := WxWorkProtocolInstanceService.UpdateInstance(request.UpdateWxWorkProtocolInstanceRequest{
		ID: instanceB.ID,
		CreateWxWorkProtocolInstanceRequest: request.CreateWxWorkProtocolInstanceRequest{
			Guid: instanceB.Guid, ChannelID: channelA.ID, StoreStaffUserID: userA.ID, Status: int(enums.StatusOk),
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

func TestWxWorkProtocolInstanceCreateRollsBackWhenBindingSyncFails(t *testing.T) {
	db := setupWxWorkProtocolTenantDB(t)
	operator := wxWorkProtocolTenantOperator(101, 1)
	channel := createWxWorkProtocolTenantChannel(t, db, 101, "wxwork-atomic-create")
	user := createStoreStaffTenantUser(t, db, 101, "wxwork-atomic-user")
	store := createWxWorkProtocolTenantStore(t, db, 101, "WX-ATOMIC")
	createStoreStaffTenantBinding(t, db, 101, user.ID, 0, store.ID)

	if err := db.Exec(`CREATE TRIGGER reject_store_binding_update
		BEFORE UPDATE ON t_store_staff_binding
		BEGIN
			SELECT RAISE(ABORT, 'binding sync failed');
		END`).Error; err != nil {
		t.Fatalf("create binding update trigger: %v", err)
	}

	if _, err := WxWorkProtocolInstanceService.CreateInstance(request.CreateWxWorkProtocolInstanceRequest{
		Guid: "atomic-create-guid", ChannelID: channel.ID, StoreStaffUserID: user.ID, Status: int(enums.StatusOk),
	}, operator); err == nil {
		t.Fatal("instance create unexpectedly succeeded when binding sync failed")
	}
	var count int64
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("guid = ?", "atomic-create-guid").Count(&count).Error; err != nil {
		t.Fatalf("count rolled back instance: %v", err)
	}
	if count != 0 {
		t.Fatalf("binding sync failure left %d instance rows", count)
	}
}

func TestWxWorkProtocolLoginClaimsOnlyUnownedInstance(t *testing.T) {
	db := setupWxWorkProtocolTenantDB(t)
	adminA := wxWorkProtocolTenantOperator(101, 1)
	channelA := createWxWorkProtocolTenantChannel(t, db, 101, "wxwork-claim-a")
	userA := createStoreStaffTenantUser(t, db, 101, "wxwork-claim-store-user")

	pending, err := WxWorkProtocolInstanceService.CreatePendingFromLogin("claimable-guid", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("create callback quarantine instance: %v", err)
	}
	if pending.TenantID != 0 || pending.HealthStatus != "pending_binding" {
		t.Fatalf("callback instance must remain quarantined: %+v", pending)
	}

	claimed, err := WxWorkProtocolInstanceService.CreateLoginInstance(request.StartWxWorkProtocolLoginRequest{
		ChannelID:        channelA.ID,
		Guid:             pending.Guid,
		StoreStaffUserID: userA.ID,
		StoreName:        "认领测试门店",
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

func TestWxWorkProtocolLoginResumesDraftBeforeClaimingDevice(t *testing.T) {
	db := setupWxWorkProtocolTenantDB(t)
	operator := wxWorkProtocolTenantOperator(101, 1)
	channel := createWxWorkProtocolTenantChannel(t, db, 101, "wxwork-resume-login")
	user := createStoreStaffTenantUser(t, db, 101, "wxwork-resume-store-user")
	store := createWxWorkProtocolTenantStore(t, db, 101, "WX-RESUME")
	binding := createStoreStaffTenantBinding(t, db, 101, user.ID, 0, store.ID)
	draft := &models.WxWorkProtocolInstance{
		TenantID:            101,
		Guid:                "resume-login-guid",
		ChannelID:           channel.ID,
		StoreID:             store.ID,
		StoreStaffBindingID: binding.ID,
		HealthStatus:        "login_qrcode",
		Status:              enums.StatusDisabled,
	}
	if err := db.Create(draft).Error; err != nil {
		t.Fatalf("create login draft: %v", err)
	}

	resumed, err := WxWorkProtocolInstanceService.CreateLoginInstance(request.StartWxWorkProtocolLoginRequest{
		ChannelID:        channel.ID,
		StoreStaffUserID: user.ID,
		StoreName:        "续用二维码门店",
	}, operator)
	if err != nil {
		t.Fatalf("resume login draft: %v", err)
	}
	if resumed.ID != draft.ID || resumed.Guid != draft.Guid {
		t.Fatalf("resumed instance=%+v want draft id=%d guid=%q", resumed, draft.ID, draft.Guid)
	}
	var count int64
	if err := db.Model(&models.WxWorkProtocolInstance{}).Count(&count).Error; err != nil {
		t.Fatalf("count login instances: %v", err)
	}
	if count != 1 {
		t.Fatalf("resume created %d login instances, want 1", count)
	}
}

func TestWxWorkProtocolLoginResumesLegacyRecoveringDraftWithoutEmployeeIdentity(t *testing.T) {
	db := setupWxWorkProtocolTenantDB(t)
	operator := wxWorkProtocolTenantOperator(101, 1)
	channel := createWxWorkProtocolTenantChannel(t, db, 101, "wxwork-resume-recovering-login")
	user := createStoreStaffTenantUser(t, db, 101, "wxwork-resume-recovering-user")
	store := createWxWorkProtocolTenantStore(t, db, 101, "WX-RESUME-RECOVERING")
	binding := createStoreStaffTenantBinding(t, db, 101, user.ID, 0, store.ID)
	draft := &models.WxWorkProtocolInstance{
		TenantID:            101,
		Guid:                "resume-recovering-login-guid",
		ChannelID:           channel.ID,
		StoreID:             store.ID,
		StoreStaffBindingID: binding.ID,
		HealthStatus:        "recovering",
		Status:              enums.StatusDisabled,
	}
	if err := db.Create(draft).Error; err != nil {
		t.Fatalf("create legacy recovering draft: %v", err)
	}

	resumed, err := WxWorkProtocolInstanceService.CreateLoginInstance(request.StartWxWorkProtocolLoginRequest{
		ChannelID:        channel.ID,
		StoreStaffUserID: user.ID,
		StoreName:        "恢复旧扫码草稿门店",
	}, operator)
	if err != nil {
		t.Fatalf("resume legacy recovering draft: %v", err)
	}
	if resumed.ID != draft.ID || resumed.Guid != draft.Guid || resumed.HealthStatus != "login_qrcode" {
		t.Fatalf("resumed instance=%+v want draft id=%d guid=%q health=login_qrcode", resumed, draft.ID, draft.Guid)
	}

	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", draft.ID).Updates(map[string]any{
		"employee_user_id": "existing-employee",
		"health_status":    "recovering",
	}).Error; err != nil {
		t.Fatalf("mark recovering instance as logged in: %v", err)
	}
	if _, err := WxWorkProtocolInstanceService.CreateLoginInstance(request.StartWxWorkProtocolLoginRequest{
		ChannelID:        channel.ID,
		StoreStaffUserID: user.ID,
		StoreName:        "不得复用已有身份门店",
	}, operator); err == nil {
		t.Fatal("recovering instance with an employee identity must not be reused as a login draft")
	}
}

func TestWxWorkProtocolRemoteSetupRejectsForeignStoreStaffBeforeGUIDClaim(t *testing.T) {
	db := setupWxWorkProtocolTenantDB(t)
	adminA := wxWorkProtocolTenantOperator(101, 1)
	userB := createStoreStaffTenantUser(t, db, 202, "remote-foreign-store-user")
	channelA := createWxWorkProtocolTenantChannel(t, db, 101, "wxwork-remote-a")
	pending, err := WxWorkProtocolInstanceService.CreatePendingFromLogin("remote-pending-guid", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("create pending instance: %v", err)
	}

	if _, err := WxWorkProtocolInstanceService.CreateRemoteSetupInstance(request.CreateWxWorkProtocolRemoteSetupRequest{
		ChannelID:        channelA.ID,
		Guid:             pending.Guid,
		StoreStaffUserID: userB.ID,
		StoreName:        "跨租户门店",
	}, adminA); err == nil {
		t.Fatal("foreign store staff remote setup unexpectedly succeeded")
	}
	current := repositories.WxWorkProtocolInstanceRepository.Get(db, pending.ID)
	if current == nil || current.TenantID != 0 || current.ChannelID != 0 {
		t.Fatalf("rejected remote setup mutated quarantined instance: %+v", current)
	}
}

func TestWxWorkProtocolBindingRejectsInvalidOrAlreadyBoundStoreStaff(t *testing.T) {
	db := setupWxWorkProtocolTenantDB(t)
	operator := wxWorkProtocolTenantOperator(101, 1)
	channel := createWxWorkProtocolTenantChannel(t, db, 101, "wxwork-binding-guards")
	validUser := createStoreStaffTenantUser(t, db, 101, "binding-valid-store-user")

	withoutRole := &models.User{TenantID: 101, Username: "binding-no-role", Nickname: "无角色账号", Password: "test", Status: enums.StatusOk}
	if err := db.Create(withoutRole).Error; err != nil {
		t.Fatalf("create user without role: %v", err)
	}
	if _, err := WxWorkProtocolInstanceService.CreateLoginInstance(request.StartWxWorkProtocolLoginRequest{
		ChannelID: channel.ID, Guid: "binding-no-role-guid", StoreStaffUserID: withoutRole.ID, StoreName: "无角色门店",
	}, operator); err == nil {
		t.Fatal("user without store_staff role unexpectedly bound a wxwork instance")
	}

	disabledUser := createStoreStaffTenantUser(t, db, 101, "binding-disabled-store-user")
	if err := db.Model(&models.User{}).Where("id = ?", disabledUser.ID).Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatalf("disable store staff user: %v", err)
	}
	if _, err := WxWorkProtocolInstanceService.CreateRemoteSetupInstance(request.CreateWxWorkProtocolRemoteSetupRequest{
		ChannelID: channel.ID, Guid: "binding-disabled-guid", StoreStaffUserID: disabledUser.ID, StoreName: "停用账号门店",
	}, operator); err == nil {
		t.Fatal("disabled store staff user unexpectedly received a binding link")
	}

	store := createWxWorkProtocolTenantStore(t, db, 101, "BINDING-EXISTING")
	binding := createStoreStaffTenantBinding(t, db, 101, validUser.ID, 0, store.ID)
	if err := db.Create(&models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "binding-existing-guid", ChannelID: channel.ID, StoreID: store.ID,
		StoreStaffBindingID: binding.ID, EmployeeUserID: "S:existing", Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create existing bound instance: %v", err)
	}
	var storesBefore int64
	db.Model(&models.Store{}).Where("tenant_id = ?", 101).Count(&storesBefore)
	if _, err := WxWorkProtocolInstanceService.CreateRemoteSetupInstance(request.CreateWxWorkProtocolRemoteSetupRequest{
		ChannelID: channel.ID, Guid: "binding-second-guid", StoreStaffUserID: validUser.ID, StoreName: "重复绑定门店",
	}, operator); err == nil {
		t.Fatal("already bound store staff user unexpectedly received a second instance")
	}
	var storesAfter int64
	db.Model(&models.Store{}).Where("tenant_id = ?", 101).Count(&storesAfter)
	if storesAfter != storesBefore {
		t.Fatalf("rejected duplicate binding created stores: %d -> %d", storesBefore, storesAfter)
	}
}

func TestWxWorkProtocolReplacementReusesStoreBindingAndAccount(t *testing.T) {
	db := setupWxWorkProtocolTenantDB(t)
	operator := wxWorkProtocolTenantOperator(101, 1)
	channel := createWxWorkProtocolTenantChannel(t, db, 101, "wxwork-replacement")
	user := createStoreStaffTenantUser(t, db, 101, "replacement-store-user")
	store := createWxWorkProtocolTenantStore(t, db, 101, "REPLACEMENT-STORE")
	binding := createStoreStaffTenantBinding(t, db, 101, user.ID, 0, store.ID)
	old := &models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "replacement-old-guid", ChannelID: channel.ID, StoreID: store.ID,
		StoreStaffBindingID: binding.ID, EmployeeUserID: "S:old", Status: enums.StatusOk,
	}
	if err := db.Create(old).Error; err != nil {
		t.Fatalf("create old instance: %v", err)
	}
	var usersBefore, storesBefore, bindingsBefore int64
	db.Model(&models.User{}).Count(&usersBefore)
	db.Model(&models.Store{}).Count(&storesBefore)
	db.Model(&models.StoreStaffBinding{}).Count(&bindingsBefore)

	replacement, err := WxWorkProtocolInstanceService.CreateReplacementRemoteSetup(request.CreateWxWorkProtocolReplacementSetupRequest{
		ID: old.ID, Guid: "replacement-new-guid",
	}, operator)
	if err != nil {
		t.Fatalf("create replacement setup: %v", err)
	}
	if replacement.StoreID != store.ID || replacement.StoreStaffBindingID != binding.ID || replacement.ReplacesInstanceID != old.ID {
		t.Fatalf("replacement changed stable store identity: %+v", replacement)
	}
	var usersAfter, storesAfter, bindingsAfter int64
	db.Model(&models.User{}).Count(&usersAfter)
	db.Model(&models.Store{}).Count(&storesAfter)
	db.Model(&models.StoreStaffBinding{}).Count(&bindingsAfter)
	if usersAfter != usersBefore || storesAfter != storesBefore || bindingsAfter != bindingsBefore {
		t.Fatalf("replacement created account identity rows: users %d->%d stores %d->%d bindings %d->%d", usersBefore, usersAfter, storesBefore, storesAfter, bindingsBefore, bindingsAfter)
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
		&models.User{}, &models.Role{}, &models.UserRole{}, &models.Store{}, &models.Channel{}, &models.StoreStaffBinding{},
		&models.WxWorkProtocolInstance{}, &models.WxWorkProtocolDevicePoolInstance{},
		&models.StoreModelCredential{}, &models.StoreCredentialPolicy{},
		&models.TenantCustomerTagPolicy{}, &models.StoreCustomerTagRuntimePolicy{},
	); err != nil {
		t.Fatalf("migrate wxwork tenant models: %v", err)
	}
	seedCustomerTagRuntimePolicyDefaults(t, db, 101)
	seedCustomerTagRuntimePolicyDefaults(t, db, 202)
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

func createWxWorkProtocolTenantStore(t *testing.T, db *gorm.DB, tenantID int64, code string) *models.Store {
	t.Helper()
	item := &models.Store{TenantID: tenantID, StoreCode: code, Name: code, Status: enums.StatusOk}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create store %s: %v", code, err)
	}
	return item
}
