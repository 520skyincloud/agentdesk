package services

import (
	"context"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestWxWorkProtocolRemoteSetupBindsExistingStoreStaffWithoutCreatingAccount(t *testing.T) {
	db := setupWxWorkProtocolInstanceStoreTestDB(t)
	seedCustomerTagRuntimePolicyDefaults(t, db, 101)
	sender := &captureEmailSender{}
	originalEmailVerificationService := EmailVerificationService
	EmailVerificationService = newEmailVerificationService(sender)
	t.Cleanup(func() { EmailVerificationService = originalEmailVerificationService })
	operator := &dto.AuthPrincipal{UserID: 1, Username: "admin", ActiveTenantID: 101}
	role := &models.Role{Name: "门店员工", Code: constants.RoleCodeStoreStaff, Scope: constants.RoleScopeTenant, Status: enums.StatusOk}
	if err := db.Create(role).Error; err != nil {
		t.Fatalf("create store staff role: %v", err)
	}
	email := "owner@example.com"
	user := &models.User{TenantID: 101, Username: "store-owner", Nickname: "合成验收测试门店", Email: &email, Password: "test", Status: enums.StatusOk}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create existing store staff user: %v", err)
	}
	if err := db.Create(&models.UserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
		t.Fatalf("assign store staff role: %v", err)
	}
	store := createStoreStaffTenantStore(t, db, 101, "合成验收酒店测试门店")
	binding := createStoreStaffTenantBinding(t, db, 101, user.ID, 0, store.ID)
	if err := db.Create(&models.Channel{ID: 22, TenantID: 101, ChannelType: enums.ChannelTypeWxWorkProtocol, Name: "协议渠道", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	var usersBefore, rolesBefore, userRolesBefore int64
	db.Model(&models.User{}).Count(&usersBefore)
	db.Model(&models.Role{}).Count(&rolesBefore)
	db.Model(&models.UserRole{}).Count(&userRolesBefore)

	instance, err := WxWorkProtocolInstanceService.CreateRemoteSetupInstance(request.CreateWxWorkProtocolRemoteSetupRequest{
		ChannelID:        22,
		Guid:             "guid-store-staff-setup",
		StoreStaffUserID: user.ID,
		StoreName:        "合成验收酒店测试门店",
	}, operator)
	if err != nil {
		t.Fatalf("CreateRemoteSetupInstance() error = %v", err)
	}
	if instance.TenantID != 101 || instance.StoreID != store.ID || instance.StoreStaffBindingID != binding.ID {
		t.Fatalf("expected tenant/store/binding, got %#v", instance)
	}
	if _, err := EmailVerificationService.SendCode(context.Background(), EmailVerificationPurposeRemoteSetup, "owner@example.com", instance.RemoteSetupToken, "127.0.0.1", "test"); err != nil {
		t.Fatalf("send email code: %v", err)
	}
	verified, err := EmailVerificationService.VerifyCode(EmailVerificationPurposeRemoteSetup, "owner@example.com", instance.RemoteSetupToken, sender.code)
	if err != nil {
		t.Fatalf("verify email code: %v", err)
	}
	if err := WxWorkProtocolInstanceService.UpdateRemoteSetup(request.UpdateWxWorkProtocolRemoteSetupRequest{
		Token:                   instance.RemoteSetupToken,
		Email:                   "owner@example.com",
		EmailVerificationToken:  verified.VerificationToken,
		EmployeeName:            "吴朝伟",
		StoreName:               "合成验收酒店测试门店",
		ManagedMode:             constants.StoreManagedModeSemi,
		StoreRoomNotifyEnabled:  true,
		FallbackToHQ:            true,
		ManualTimeoutMinutes:    10,
		AutoAcceptFriendRequest: true,
	}); err != nil {
		t.Fatalf("UpdateRemoteSetup() error = %v", err)
	}
	updated := WxWorkProtocolInstanceService.Get(instance.ID)
	if updated == nil {
		t.Fatalf("expected updated instance")
	}
	if updated.StoreID != instance.StoreID || updated.StoreStaffBindingID != instance.StoreStaffBindingID {
		t.Fatalf("stable store binding changed during completion: %#v", updated)
	}
	currentStore := StoreService.Get(updated.StoreID)
	if currentStore == nil {
		t.Fatalf("expected selected store")
	}
	if currentStore.TenantID != 101 || currentStore.ID != store.ID || currentStore.Name != "合成验收酒店测试门店" {
		t.Fatalf("unexpected selected store: %#v", currentStore)
	}
	currentBinding := StoreStaffBindingService.GetInTenant(updated.StoreStaffBindingID, 101)
	if currentBinding == nil || currentBinding.ID != binding.ID || currentBinding.UserID != user.ID || currentBinding.StoreID != store.ID {
		t.Fatalf("unexpected store staff binding: %#v", currentBinding)
	}
	verifiedUser := UserService.GetByTenantID(user.ID, 101)
	if verifiedUser == nil || verifiedUser.EmailVerifiedAt == nil {
		t.Fatalf("existing user email was not marked verified: %#v", verifiedUser)
	}
	var usersAfter, rolesAfter, userRolesAfter int64
	db.Model(&models.User{}).Count(&usersAfter)
	db.Model(&models.Role{}).Count(&rolesAfter)
	db.Model(&models.UserRole{}).Count(&userRolesAfter)
	if usersAfter != usersBefore || rolesAfter != rolesBefore || userRolesAfter != userRolesBefore {
		t.Fatalf("binding created account or role rows: users %d->%d roles %d->%d userRoles %d->%d", usersBefore, usersAfter, rolesBefore, rolesAfter, userRolesBefore, userRolesAfter)
	}
}

func TestWxWorkProtocolAISettingsSyncsExistingRouteStateKnowledgeBase(t *testing.T) {
	db := setupWxWorkProtocolInstanceStoreTestDB(t)
	operator := &dto.AuthPrincipal{UserID: 1, Username: "admin", ActiveTenantID: 101}
	role := &models.Role{Name: "门店员工", Code: constants.RoleCodeStoreStaff, Scope: constants.RoleScopeTenant, Status: enums.StatusOk}
	if err := db.Create(role).Error; err != nil {
		t.Fatalf("create store staff role: %v", err)
	}
	user := &models.User{TenantID: 101, Username: "route-store-user", Nickname: "合肥南七店", Password: "test", Status: enums.StatusOk}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create store staff user: %v", err)
	}
	if err := db.Create(&models.UserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
		t.Fatalf("assign store staff role: %v", err)
	}
	publishedAt := time.Now()
	if err := sqls.DB().Create(&models.ReplyIntentProfile{
		ID: 301, Code: "hotel-test", Name: "测试酒店行业", IndustryCode: "hotel-test",
		IntentDetectPrompt: "detect", IntentJSONSchema: "schema", Revision: 1,
		PublishedAt: &publishedAt, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create intent profile: %v", err)
	}
	if err := sqls.DB().Create(&models.ReplyIntentConfig{
		Code: "hotel_info", Name: "酒店信息", IntentProfileID: 301, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create intent config: %v", err)
	}
	category := &models.IndustryTagDefinition{
		IntentProfileID: 301, Name: "分类", SemanticKey: "category.test", DefinitionRevision: 1, Status: enums.StatusOk,
	}
	if err := sqls.DB().Create(category).Error; err != nil {
		t.Fatalf("create tag category: %v", err)
	}
	if err := sqls.DB().Create(&models.IndustryTagDefinition{
		IntentProfileID: 301, ParentID: category.ID, Name: "标签", SemanticKey: "test.tag",
		AIEnabled: true, DefinitionRevision: 1, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create tag definition: %v", err)
	}
	if err := sqls.DB().Create(&models.Tenant{
		ID: 101, IntentProfileID: 301, TenantCode: "tenant-route-sync", LegalName: "测试公司",
		ShortName: "测试公司", RegistrationType: "test", RegistrationNo: "route-sync", Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if err := sqls.DB().Create(&models.Channel{ID: 22, TenantID: 101, ChannelType: enums.ChannelTypeWxWorkProtocol, Name: "协议渠道", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := sqls.DB().Create(&models.Store{ID: 31, TenantID: 101, StoreCode: "store-sync", Name: "合肥南七店", KnowledgeBaseID: 202, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	binding := &models.StoreStaffBinding{
		TenantID: 101, UserID: user.ID, ActiveUserID: positiveInt64Pointer(user.ID),
		StoreID: 31, Status: enums.StatusOk,
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create store staff binding: %v", err)
	}
	if err := sqls.DB().Create(&models.KnowledgeBase{ID: 101, TenantID: 101, StoreID: 31, Name: "旧知识库", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create old knowledge base: %v", err)
	}
	if err := sqls.DB().Create(&models.KnowledgeBase{ID: 202, TenantID: 101, StoreID: 31, Name: "合肥南七店", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create new knowledge base: %v", err)
	}
	if err := sqls.DB().Create(&models.WxWorkProtocolInstance{
		ID:                      7,
		TenantID:                101,
		Guid:                    "guid-route-sync",
		ChannelID:               22,
		EmployeeName:            "吴朝伟",
		StoreID:                 31,
		StoreStaffBindingID:     binding.ID,
		KnowledgeBaseID:         101,
		ServiceHours:            "legacy-hours",
		StoreRoomConversationID: "R:legacy-room",
		StoreRoomNotifyEnabled:  false,
		StoreRoomAtList:         "legacy-user",
		FallbackToHQ:            true,
		ManualTimeoutMinutes:    7,
		Status:                  enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if err := sqls.DB().Create(&models.ConversationRouteState{
		TenantID:         101,
		ConversationID:   9001,
		StoreID:          31,
		KnowledgeBaseID:  101,
		WxWorkInstanceID: 7,
		RouteStatus:      enums.ConversationRouteStatusAIServing,
		RouteTarget:      "ai",
		SessionNo:        1,
	}).Error; err != nil {
		t.Fatalf("create route state: %v", err)
	}

	if err := WxWorkProtocolInstanceService.UpdateAISettings(request.UpdateWxWorkProtocolAISettingsRequest{
		ID:                      7,
		AIReplyEnabled:          true,
		StoreID:                 31,
		KnowledgeBaseID:         202,
		ManagedMode:             constants.StoreManagedModeFull,
		ServiceHours:            "08:00-22:00",
		StoreRoomConversationID: "R:new-room",
		StoreRoomNotifyEnabled:  true,
		StoreRoomAtList:         "new-user",
		FallbackToHQ:            false,
		ManualTimeoutMinutes:    18,
	}, operator); err != nil {
		t.Fatalf("UpdateAISettings() error = %v", err)
	}

	var state models.ConversationRouteState
	if err := sqls.DB().Where("conversation_id = ?", int64(9001)).First(&state).Error; err != nil {
		t.Fatalf("load route state: %v", err)
	}
	if state.KnowledgeBaseID != 202 || state.StoreID != 31 {
		t.Fatalf("expected route state to sync binding, got store=%d knowledge=%d", state.StoreID, state.KnowledgeBaseID)
	}
	if state.UpdateUserName != "admin" {
		t.Fatalf("expected route state audit user admin, got %q", state.UpdateUserName)
	}
	currentBinding := StoreStaffBindingService.GetInTenant(binding.ID, 101)
	if currentBinding == nil || currentBinding.ManagedMode != constants.StoreManagedModeFull || currentBinding.ServiceHours != "08:00-22:00" ||
		currentBinding.StoreRoomConversationID != "R:new-room" || !currentBinding.StoreRoomNotifyEnabled || currentBinding.StoreRoomAtList != "new-user" ||
		currentBinding.FallbackToHQ || currentBinding.ManualTimeoutMinutes != 18 {
		t.Fatalf("binding runtime facts were not updated: %#v", currentBinding)
	}
	currentInstance := WxWorkProtocolInstanceService.GetByTenantID(7, 101)
	if currentInstance == nil || currentInstance.ServiceHours != "legacy-hours" || currentInstance.StoreRoomConversationID != "R:legacy-room" ||
		currentInstance.StoreRoomNotifyEnabled || currentInstance.StoreRoomAtList != "legacy-user" || !currentInstance.FallbackToHQ || currentInstance.ManualTimeoutMinutes != 7 {
		t.Fatalf("legacy instance runtime projection was unexpectedly rewritten: %#v", currentInstance)
	}
}

func TestStoreRuntimeProjectionUsesIndependentStoreWithoutMutatingInstance(t *testing.T) {
	db := setupWxWorkProtocolInstanceStoreTestDB(t)
	store := &models.Store{
		TenantID: 101, StoreCode: "runtime-store", Name: "运行时门店",
		Address: "新地址", NavigationName: "新导航名", Longitude: "117.1", Latitude: "31.8",
		MapProvider: "amap", ContactPhone: "0551-12345678", KnowledgeBaseID: 88,
		Status: enums.StatusOk,
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create runtime Store: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID: 101, StoreID: store.ID,
		StoreAddress: "旧地址", StoreNavigationName: "旧导航名",
		StoreLongitude: "1", StoreLatitude: "2", StoreMapProvider: "legacy",
		StoreContactPhone: "旧电话", KnowledgeBaseID: 7,
	}

	runtimeInstance, err := StoreService.HydrateRuntimeInstanceDB(db, instance)
	if err != nil {
		t.Fatalf("HydrateRuntimeInstanceDB() error = %v", err)
	}
	if runtimeInstance.StoreAddress != store.Address ||
		runtimeInstance.StoreNavigationName != store.NavigationName ||
		runtimeInstance.StoreLongitude != store.Longitude ||
		runtimeInstance.StoreLatitude != store.Latitude ||
		runtimeInstance.StoreMapProvider != store.MapProvider ||
		runtimeInstance.StoreContactPhone != store.ContactPhone ||
		runtimeInstance.KnowledgeBaseID != store.KnowledgeBaseID {
		t.Fatalf("runtime Store projection mismatch: %#v", runtimeInstance)
	}
	if instance.StoreAddress != "旧地址" || instance.KnowledgeBaseID != 7 {
		t.Fatalf("runtime projection mutated persisted instance snapshot: %#v", instance)
	}
	if err := db.Model(&models.Store{}).Where("id = ?", store.ID).
		Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatalf("disable runtime Store: %v", err)
	}
	if _, err := StoreService.HydrateRuntimeInstanceDB(db, instance); err == nil {
		t.Fatalf("disabled Store must not supply runtime facts")
	}
}

func TestRequireStoreKnowledgeRejectsDisabledStoreOrBinding(t *testing.T) {
	db := setupWxWorkProtocolInstanceStoreTestDB(t)
	user := &models.User{TenantID: 101, Username: "runtime-knowledge-owner", Password: "test", Status: enums.StatusOk}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create runtime owner: %v", err)
	}
	store := &models.Store{
		TenantID: 101, StoreCode: "runtime-knowledge", Name: "运行时知识门店",
		KnowledgeBaseID: 88, Status: enums.StatusOk,
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create runtime Store: %v", err)
	}
	if err := db.Create(&models.KnowledgeBase{
		ID: 88, TenantID: 101, StoreID: store.ID, Name: "运行时知识库", Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create runtime knowledge base: %v", err)
	}
	binding := &models.StoreStaffBinding{
		TenantID: 101, UserID: user.ID, ActiveUserID: positiveInt64Pointer(user.ID),
		StoreID: store.ID, Status: enums.StatusOk,
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create runtime binding: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID: 101, StoreID: store.ID, StoreStaffBindingID: binding.ID,
		Status: enums.StatusOk,
	}

	if gotStoreID, gotKnowledgeID, err := WxWorkProtocolInstanceService.RequireStoreKnowledge(instance); err != nil || gotStoreID != store.ID || gotKnowledgeID != 88 {
		t.Fatalf("active runtime knowledge = (%d,%d,%v)", gotStoreID, gotKnowledgeID, err)
	}
	if err := db.Model(&models.StoreStaffBinding{}).Where("id = ?", binding.ID).Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatalf("disable binding: %v", err)
	}
	if _, _, err := WxWorkProtocolInstanceService.RequireStoreKnowledge(instance); err == nil {
		t.Fatal("disabled Store staff binding must not use Store knowledge")
	}
	if err := db.Model(&models.StoreStaffBinding{}).Where("id = ?", binding.ID).Update("status", enums.StatusOk).Error; err != nil {
		t.Fatalf("restore binding: %v", err)
	}
	if err := db.Model(&models.Store{}).Where("id = ?", store.ID).Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatalf("disable Store: %v", err)
	}
	if _, _, err := WxWorkProtocolInstanceService.RequireStoreKnowledge(instance); err == nil {
		t.Fatal("disabled Store must not use Store knowledge")
	}
}

func setupWxWorkProtocolInstanceStoreTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite error = %v", err)
	}
	if err := db.AutoMigrate(
		&models.Tenant{},
		&models.ReplyIntentProfile{},
		&models.ReplyIntentConfig{},
		&models.IndustryTagDefinition{},
		&models.Store{},
		&models.Channel{},
		&models.WxWorkProtocolInstance{},
		&models.StoreStaffBinding{},
		&models.StoreModelCredential{},
		&models.StoreCredentialPolicy{},
		&models.TenantCustomerTagPolicy{},
		&models.StoreCustomerTagRuntimePolicy{},
		&models.KnowledgeBase{},
		&models.ConversationRouteState{},
		&models.WxWorkProtocolDevicePoolInstance{},
		&models.EmailVerificationCode{},
		&models.User{},
		&models.Role{},
		&models.UserRole{},
		&models.UserRoleChangeLog{},
	); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
