package services

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
)

func TestStoreWorkbenchCurrentUsesOnlyCurrentUserAndTenant(t *testing.T) {
	db := setupStoreStaffTenantDB(t)
	teamA := createStoreStaffTenantTeam(t, db, 101, "workbench-team-a")
	userA := createStoreStaffTenantUser(t, db, 101, "workbench-user-a")
	storeA := createStoreStaffTenantStore(t, db, 101, "workbench-store-a")
	storeB := createStoreStaffTenantStore(t, db, 202, "workbench-store-b")
	bindingA := createStoreStaffTenantBinding(t, db, 101, userA.ID, teamA.ID, storeA.ID)
	createStoreStaffTenantBinding(t, db, 202, userA.ID, 0, storeB.ID)
	instanceA := createStoreStaffTenantInstance(t, db, 101, "workbench-instance-a", teamA.ID, storeA.ID, bindingA.ID)

	operator := &dto.AuthPrincipal{
		UserID: userA.ID, TenantID: 101, ActiveTenantID: 101, ActiveTenantName: "租户A",
		Username: userA.Username, Nickname: "门店员工A", Roles: []string{constants.RoleCodeStoreStaff},
	}
	snapshot, err := StoreWorkbenchService.Current(operator)
	if err != nil {
		t.Fatalf("current store workbench: %v", err)
	}
	if snapshot.Binding == nil || snapshot.Binding.ID != bindingA.ID || snapshot.Store == nil || snapshot.Store.ID != storeA.ID {
		t.Fatalf("snapshot binding/store = %+v/%+v", snapshot.Binding, snapshot.Store)
	}
	if snapshot.AgentTeam == nil || snapshot.AgentTeam.ID != teamA.ID || snapshot.WxWorkInstance == nil || snapshot.WxWorkInstance.ID != instanceA.ID {
		t.Fatalf("snapshot team/instance = %+v/%+v", snapshot.AgentTeam, snapshot.WxWorkInstance)
	}
	if snapshot.TenantID != 101 || snapshot.TenantName != "租户A" {
		t.Fatalf("snapshot tenant = %+v", snapshot)
	}

	otherTenantSnapshot, err := StoreWorkbenchService.Current(&dto.AuthPrincipal{UserID: userA.ID, ActiveTenantID: 303})
	if err != nil {
		t.Fatalf("other tenant current: %v", err)
	}
	if otherTenantSnapshot.Binding != nil {
		t.Fatalf("other tenant leaked binding: %+v", otherTenantSnapshot.Binding)
	}
}

func TestStoreWorkbenchUpdateSynchronizesBindingAndInstance(t *testing.T) {
	db := setupStoreStaffTenantDB(t)
	team := createStoreStaffTenantTeam(t, db, 101, "workbench-update-team")
	user := createStoreStaffTenantUser(t, db, 101, "workbench-update-user")
	store := createStoreStaffTenantStore(t, db, 101, "workbench-update-store")
	binding := createStoreStaffTenantBinding(t, db, 101, user.ID, team.ID, store.ID)
	instance := createStoreStaffTenantInstance(t, db, 101, "workbench-update-instance", team.ID, store.ID, binding.ID)
	operator := &dto.AuthPrincipal{UserID: user.ID, ActiveTenantID: 101, Username: user.Username}

	updated, err := StoreWorkbenchService.UpdateCurrent(request.UpdateStoreWorkbenchRequest{
		ManagedMode:             constants.StoreManagedModeNone,
		ServiceHours:            "09:00 至 12:00；13:30-22:00",
		StoreRoomConversationID: "room-100",
		StoreRoomNotifyEnabled:  true,
		StoreRoomAtList:         "staff-1, staff-1,0",
		ManualTimeoutMinutes:    18,
		StoreAddress:            "上海市测试路 1 号",
		StoreNavigationName:     "测试门店",
		StoreLongitude:          "121.4737014",
		StoreLatitude:           "31.2304162",
		StoreMapProvider:        "browser_geolocation",
	}, operator)
	if err != nil {
		t.Fatalf("update store workbench: %v", err)
	}
	if updated.Runtime.ManagedMode != constants.StoreManagedModeNone || updated.Runtime.FallbackToHQ {
		t.Fatalf("updated runtime = %+v", updated.Runtime)
	}
	if updated.Runtime.ServiceHours != "09:00-12:00,13:30-22:00" || updated.Runtime.StoreRoomConversationID != "R:room-100" || updated.Runtime.StoreRoomAtList != "staff-1,0" {
		t.Fatalf("normalized runtime = %+v", updated.Runtime)
	}

	currentBinding := repositories.StoreStaffBindingRepository.GetInTenant(db, binding.ID, 101)
	if currentBinding == nil || currentBinding.ManualTimeoutMinutes != 18 || currentBinding.FallbackToHQ {
		t.Fatalf("binding after update = %+v", currentBinding)
	}
	currentInstance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(db, instance.ID, 101)
	if currentInstance == nil || currentInstance.StoreAddress != "上海市测试路 1 号" || currentInstance.StoreLongitude != "121.473701" || currentInstance.StoreLatitude != "31.230416" {
		t.Fatalf("instance after update = %+v", currentInstance)
	}
	if currentInstance.StoreRoomConversationID != "R:room-100" || currentInstance.StoreRoomAtList != "staff-1,0" || currentInstance.FallbackToHQ {
		t.Fatalf("instance runtime after update = %+v", currentInstance)
	}
}

func TestStoreWorkbenchUpdateRejectsUnsafeOrAmbiguousConfiguration(t *testing.T) {
	db := setupStoreStaffTenantDB(t)
	user := createStoreStaffTenantUser(t, db, 101, "workbench-validation-user")
	storeA := createStoreStaffTenantStore(t, db, 101, "workbench-validation-store-a")
	bindingA := createStoreStaffTenantBinding(t, db, 101, user.ID, 0, storeA.ID)
	operator := &dto.AuthPrincipal{UserID: user.ID, ActiveTenantID: 101, Username: user.Username}

	_, err := StoreWorkbenchService.UpdateCurrent(request.UpdateStoreWorkbenchRequest{
		ManagedMode: constants.StoreManagedModeNone, ManualTimeoutMinutes: 10,
	}, operator)
	if err == nil {
		t.Fatal("non-managed mode without notification room must fail")
	}
	if current := repositories.StoreStaffBindingRepository.GetInTenant(db, bindingA.ID, 101); current == nil || current.ManagedMode == constants.StoreManagedModeNone {
		t.Fatalf("invalid update changed binding: %+v", current)
	}

	storeB := createStoreStaffTenantStore(t, db, 101, "workbench-validation-store-b")
	legacyDuplicate := &models.StoreStaffBinding{
		TenantID: 101, UserID: user.ID, StoreID: storeB.ID,
		Status: enums.StatusOk,
	}
	if err := db.Create(legacyDuplicate).Error; err != nil {
		t.Fatalf("create pre-migration duplicate binding: %v", err)
	}
	if _, err := StoreWorkbenchService.Current(operator); err == nil {
		t.Fatal("multiple store bindings must fail closed")
	}
}

func TestStoreWorkbenchCurrentReportsDisabledBinding(t *testing.T) {
	db := setupStoreStaffTenantDB(t)
	user := createStoreStaffTenantUser(t, db, 101, "workbench-disabled-user")
	store := createStoreStaffTenantStore(t, db, 101, "workbench-disabled-store")
	binding := createStoreStaffTenantBinding(t, db, 101, user.ID, 0, store.ID)
	if err := db.Model(&models.StoreStaffBinding{}).Where("id = ?", binding.ID).Updates(map[string]any{
		"active_user_id": nil,
		"status":         enums.StatusDisabled,
	}).Error; err != nil {
		t.Fatalf("disable binding: %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: user.ID, ActiveTenantID: 101, Username: user.Username}
	snapshot, err := StoreWorkbenchService.Current(operator)
	if err != nil || snapshot.Binding == nil || snapshot.Binding.Status != enums.StatusDisabled {
		t.Fatalf("disabled snapshot = %+v, err = %v", snapshot, err)
	}
	if _, err := StoreWorkbenchService.UpdateCurrent(request.UpdateStoreWorkbenchRequest{ManagedMode: constants.StoreManagedModeFull, ManualTimeoutMinutes: 10}, operator); err == nil {
		t.Fatal("disabled binding update must fail")
	}
}
