package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"gorm.io/gorm"
)

func TestCustomerTagRuntimePolicyTenantScopeDefaultsBatchAndRequeue(t *testing.T) {
	db, platformOperator := setupTenantManagementTestDB(t)
	if err := db.AutoMigrate(&models.StoreCustomerTagRuntimePolicy{}, &models.ConversationEvolutionState{}); err != nil {
		t.Fatal(err)
	}
	tenantA, err := TenantService.CreateTenant(tenantManagementCreateRequest("tag-policy-a", "91350100MA8P1A2B3C"), platformOperator)
	if err != nil {
		t.Fatal(err)
	}
	tenantB, err := TenantService.CreateTenant(tenantManagementCreateRequest("tag-policy-b", "91350100MA8P4D5E6F"), platformOperator)
	if err != nil {
		t.Fatal(err)
	}
	operator := &dto.AuthPrincipal{
		UserID: 9201, Username: "tenant-policy-admin", TenantID: tenantA.Tenant.ID, ActiveTenantID: tenantA.Tenant.ID,
		Roles: []string{constants.RoleCodeTenantAdmin}, Permissions: []string{constants.PermissionTagView.Code, constants.PermissionTagUpdate.Code},
	}
	storeA := createCustomerTagPolicyStore(t, db, tenantA.Tenant.ID, "policy-a", "A 门店")
	storeB := createCustomerTagPolicyStore(t, db, tenantA.Tenant.ID, "policy-b", "B 门店")
	foreignStore := createCustomerTagPolicyStore(t, db, tenantB.Tenant.ID, "policy-foreign", "外部门店")
	for _, store := range []*models.Store{storeA, storeB, foreignStore} {
		if err := CustomerTagRuntimePolicyService.EnsureStorePolicyDB(db, store, operator); err != nil {
			t.Fatalf("ensure Store %d policy: %v", store.ID, err)
		}
	}

	future := time.Now().Add(24 * time.Hour)
	state := &models.ConversationEvolutionState{
		TenantID: tenantA.Tenant.ID, StoreID: storeA.ID, ConversationID: 7001, SessionNo: 1,
		LastObservedMessageID: 2, LastEvolvedMessageID: 1, NextEvolutionAt: &future,
		Status: enums.StatusOk,
	}
	if err := db.Create(state).Error; err != nil {
		t.Fatal(err)
	}
	if err := CustomerTagRuntimePolicyService.UpdatePolicy(request.UpdateCustomerTagPolicyRequest{
		QuietPeriodMinutes: 60, MinimumConfidence: 0.9, MaxOperationsPerRun: 4,
		EvolutionDefaultEnabled: true, ReplyTagContextDefaultEnabled: true,
	}, operator); err != nil {
		t.Fatal(err)
	}
	policy, err := CustomerTagRuntimePolicyService.GetPolicy(operator)
	if err != nil {
		t.Fatal(err)
	}
	if policy.QuietPeriodMinutes != 60 || policy.MinimumConfidence != 0.9 || policy.MaxOperationsPerRun != 4 ||
		!policy.EvolutionDefaultEnabled || !policy.ReplyTagContextDefaultEnabled || policy.TenantID != tenantA.Tenant.ID {
		t.Fatalf("updated policy=%#v", policy)
	}
	var requeued models.ConversationEvolutionState
	if err := db.First(&requeued, state.ID).Error; err != nil {
		t.Fatal(err)
	}
	if requeued.NextEvolutionAt == nil || requeued.NextEvolutionAt.After(time.Now().Add(time.Minute)) {
		t.Fatalf("quiet-period change did not requeue pending state: %#v", requeued.NextEvolutionAt)
	}

	storeC := createCustomerTagPolicyStore(t, db, tenantA.Tenant.ID, "policy-c", "C 门店")
	if err := CustomerTagRuntimePolicyService.EnsureStorePolicyDB(db, storeC, operator); err != nil {
		t.Fatal(err)
	}
	assertCustomerTagStorePolicy(t, db, tenantA.Tenant.ID, storeC.ID, true, true)
	if _, err := CustomerTagRuntimePolicyService.BatchToggle(request.BatchToggleCustomerTagRuntimeRequest{
		StoreIDs: []int64{storeA.ID}, CustomerTagEvolutionEnabled: boolPointer(true),
	}, operator); err != nil {
		t.Fatal(err)
	}
	assertCustomerTagStorePolicy(t, db, tenantA.Tenant.ID, storeA.ID, true, false)
	assertCustomerTagStorePolicy(t, db, tenantA.Tenant.ID, storeB.ID, false, false)

	if _, err := CustomerTagRuntimePolicyService.BatchToggle(request.BatchToggleCustomerTagRuntimeRequest{
		StoreIDs: []int64{storeA.ID, foreignStore.ID}, ReplyTagContextEnabled: boolPointer(true),
	}, operator); err == nil {
		t.Fatal("cross-Tenant Store selection must be rejected atomically")
	}
	assertCustomerTagStorePolicy(t, db, tenantA.Tenant.ID, storeA.ID, true, false)
	assertCustomerTagStorePolicy(t, db, tenantB.Tenant.ID, foreignStore.ID, false, false)

	affected, err := CustomerTagRuntimePolicyService.BatchToggle(request.BatchToggleCustomerTagRuntimeRequest{
		AllStores: true, ReplyTagContextEnabled: boolPointer(true),
	}, operator)
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 3 {
		t.Fatalf("all-store affected count=%d want=3", len(affected))
	}
	for _, store := range []*models.Store{storeA, storeB, storeC} {
		current, err := repositories.StoreCustomerTagRuntimePolicyRepository.GetByStore(db, tenantA.Tenant.ID, store.ID)
		if err != nil || current == nil || !current.ReplyTagContextEnabled {
			t.Fatalf("Store %d reply policy=%#v err=%v", store.ID, current, err)
		}
	}
	assertCustomerTagStorePolicy(t, db, tenantB.Tenant.ID, foreignStore.ID, false, false)

	disabled := false
	list, paging, err := CustomerTagRuntimePolicyService.ListStorePolicies(CustomerTagRuntimePolicyListFilter{
		Page: 1, Limit: 20, EvolutionEnabled: &disabled,
	}, operator)
	if err != nil {
		t.Fatal(err)
	}
	if paging.Total != 1 || len(list) != 1 || list[0].StoreID != storeB.ID || list[0].CustomerTagEvolutionEnabled {
		t.Fatalf("disabled evolution list=%#v paging=%#v", list, paging)
	}
}

func TestCustomerTagRuntimePolicyValidation(t *testing.T) {
	invalid := []request.UpdateCustomerTagPolicyRequest{
		{QuietPeriodMinutes: 0, MinimumConfidence: 0.9, MaxOperationsPerRun: 4},
		{QuietPeriodMinutes: 60, MinimumConfidence: 0.79, MaxOperationsPerRun: 4},
		{QuietPeriodMinutes: 60, MinimumConfidence: 0.9, MaxOperationsPerRun: 7},
	}
	for i := range invalid {
		if err := validateCustomerTagPolicyRequest(invalid[i]); err == nil {
			t.Fatalf("invalid policy %d was accepted: %#v", i, invalid[i])
		}
	}
}

func createCustomerTagPolicyStore(t *testing.T, db *gorm.DB, tenantID int64, code, name string) *models.Store {
	t.Helper()
	store := &models.Store{TenantID: tenantID, StoreCode: code, Name: name, Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	return store
}

func assertCustomerTagStorePolicy(t *testing.T, db *gorm.DB, tenantID, storeID int64, evolution, reply bool) {
	t.Helper()
	policy, err := repositories.StoreCustomerTagRuntimePolicyRepository.GetByStore(db, tenantID, storeID)
	if err != nil || policy == nil {
		t.Fatalf("Store policy missing tenant=%d Store=%d err=%v", tenantID, storeID, err)
	}
	if policy.CustomerTagEvolutionEnabled != evolution || policy.ReplyTagContextEnabled != reply {
		t.Fatalf("Store policy=%#v want evolution=%v reply=%v", policy, evolution, reply)
	}
}

func seedCustomerTagRuntimePolicyDefaults(t *testing.T, db *gorm.DB, tenantID int64) {
	t.Helper()
	if err := db.Create(&models.TenantCustomerTagPolicy{
		TenantID: tenantID, IntentProfileID: tenantID,
		QuietPeriodMinutes: 24 * 60, MinimumConfidence: 0.8, MaxOperationsPerRun: 6,
		EvolutionDefaultEnabled: false, ReplyTagContextDefaultEnabled: false, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("seed Tenant %d customer tag policy: %v", tenantID, err)
	}
}

func boolPointer(value bool) *bool { return &value }
