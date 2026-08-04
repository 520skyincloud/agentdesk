package services

import (
	"path/filepath"
	"slices"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestStoreStaffAssignmentsAndWxWorkScopeStayInActiveTenant(t *testing.T) {
	db := setupStoreStaffTenantDB(t)
	teamA := createStoreStaffTenantTeam(t, db, 101, "租户A客服组")
	teamB := createStoreStaffTenantTeam(t, db, 202, "租户B客服组")
	userA := createStoreStaffTenantUser(t, db, 101, "tenant-a-store-staff")
	userB := createStoreStaffTenantUser(t, db, 202, "tenant-b-store-staff")
	storeA := createStoreStaffTenantStore(t, db, 101, "tenant-a-store")
	storeB := createStoreStaffTenantStore(t, db, 202, "tenant-b-store")
	storePolluted := createStoreStaffTenantStore(t, db, 202, "tenant-b-polluted-store")
	bindingA := createStoreStaffTenantBinding(t, db, 101, userA.ID, teamA.ID, storeA.ID)
	bindingB := createStoreStaffTenantBinding(t, db, 202, userB.ID, teamB.ID, storeB.ID)
	createStoreStaffTenantBinding(t, db, 202, userA.ID, teamB.ID, storePolluted.ID)
	instanceA := createStoreStaffTenantInstance(t, db, 101, "tenant-a-instance", teamA.ID, storeA.ID, bindingA.ID)
	instanceB := createStoreStaffTenantInstance(t, db, 202, "tenant-b-instance", teamB.ID, storeB.ID, bindingB.ID)

	assignments := StoreStaffBindingService.FindUserAssignments([]int64{userA.ID, userB.ID}, 101)
	if len(assignments) != 1 {
		t.Fatalf("tenant A assignments=%+v want one", assignments)
	}
	assignment := assignments[userA.ID]
	if assignment.TenantID != 101 || assignment.BindingID != bindingA.ID || assignment.StoreName != storeA.Name || assignment.AgentTeamName != teamA.Name || assignment.WxWorkInstanceID != instanceA.ID {
		t.Fatalf("tenant A assignment=%+v", assignment)
	}
	if empty := StoreStaffBindingService.FindUserAssignments([]int64{userA.ID}, 0); len(empty) != 0 {
		t.Fatalf("assignments without tenant=%+v want empty", empty)
	}

	operator := &dto.AuthPrincipal{UserID: 1, ActiveTenantID: 101, Roles: []string{constants.RoleCodeTenantAdmin}}
	cnd := AgentTeamScopeService.ApplyWxWorkInstanceFilter(sqls.NewCnd().Asc("id"), operator)
	instances := repositories.WxWorkProtocolInstanceRepository.Find(db, cnd)
	if len(instances) != 1 || instances[0].ID != instanceA.ID {
		t.Fatalf("tenant A wxwork instances=%+v want only %d", instances, instanceA.ID)
	}
	if !AgentTeamScopeService.CanViewWxWorkInstance(operator, instanceA.ID) {
		t.Fatal("tenant admin should view its own wxwork instance")
	}
	if AgentTeamScopeService.CanViewWxWorkInstance(operator, instanceB.ID) {
		t.Fatal("tenant admin must not view another tenant's wxwork instance")
	}
}

func TestEnsureStoreStaffBindingRequiresExistingStoreStaffAccount(t *testing.T) {
	db := setupStoreStaffTenantDB(t)
	store := createStoreStaffTenantStore(t, db, 101, "ensure-binding-store")
	instance := createStoreStaffTenantInstance(t, db, 101, "ensure-binding-instance", 0, store.ID, 0)
	wrongTenantOperator := &dto.AuthPrincipal{UserID: 2, Username: "tenant-b-admin", ActiveTenantID: 202}
	if _, err := StoreStaffBindingService.EnsureForInstance(instance, wrongTenantOperator); err == nil {
		t.Fatal("another tenant must not create a store staff binding")
	}
	if binding := repositories.StoreStaffBindingRepository.Take(db, "store_id = ?", store.ID); binding != nil {
		t.Fatalf("cross-tenant attempt created binding=%+v", binding)
	}

	operator := &dto.AuthPrincipal{UserID: 1, Username: "tenant-a-admin", ActiveTenantID: 101}
	if _, err := StoreStaffBindingService.EnsureForInstance(instance, operator); err == nil {
		t.Fatal("missing store staff account binding must not be created implicitly")
	}
	user := createStoreStaffTenantUser(t, db, 101, "ensure-binding-user")
	existing := createStoreStaffTenantBinding(t, db, 101, user.ID, 0, store.ID)
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", instance.ID).Update("store_staff_binding_id", existing.ID).Error; err != nil {
		t.Fatalf("assign explicit store staff binding: %v", err)
	}
	instance.StoreStaffBindingID = existing.ID
	binding, err := StoreStaffBindingService.EnsureForInstance(instance, operator)
	if err != nil {
		t.Fatalf("ensure existing store staff binding: %v", err)
	}
	if binding.ID != existing.ID || binding.TenantID != 101 || binding.UserID != user.ID || binding.StoreID != store.ID {
		t.Fatalf("unexpected canonical binding=%+v", binding)
	}
	updated := repositories.WxWorkProtocolInstanceRepository.GetInTenant(db, instance.ID, 101)
	if updated == nil || updated.StoreStaffBindingID != binding.ID {
		t.Fatalf("updated instance=%+v want binding %d", updated, binding.ID)
	}

	emptyOwnerStore := createStoreStaffTenantStore(t, db, 101, "ensure-empty-owner-store")
	emptyOwnerBinding := createStoreStaffTenantBinding(t, db, 101, 0, 0, emptyOwnerStore.ID)
	emptyOwnerInstance := createStoreStaffTenantInstance(t, db, 101, "ensure-empty-owner-instance", 0, emptyOwnerStore.ID, emptyOwnerBinding.ID)
	if _, err := StoreStaffBindingService.EnsureForInstance(emptyOwnerInstance, operator); err == nil {
		t.Fatal("binding without a registered user must be rejected")
	}
}

func TestEnsureStoreStaffBindingLocksCanonicalBindingBeforeTeam(t *testing.T) {
	db := setupStoreStaffTenantDB(t)
	team := createStoreStaffTenantTeam(t, db, 101, "同步锁测试客服组")
	user := createStoreStaffTenantUser(t, db, 101, "ensure-lock-user")
	store := createStoreStaffTenantStore(t, db, 101, "ensure-lock-store")
	binding := createStoreStaffTenantBinding(t, db, 101, user.ID, team.ID, store.ID)
	instance := createStoreStaffTenantInstance(t, db, 101, "ensure-lock-instance", 0, store.ID, binding.ID)
	lockOrder := make([]string, 0, 2)
	callbackName := "test:ensure-store-staff-binding-lock-order"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if _, locked := tx.Statement.Clauses["FOR"]; !locked || tx.Statement.Schema == nil {
			return
		}
		if tx.Statement.Schema.Name == "StoreStaffBinding" || tx.Statement.Schema.Name == "AgentTeam" {
			lockOrder = append(lockOrder, tx.Statement.Schema.Name)
		}
	}); err != nil {
		t.Fatalf("register ensure lock callback: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove ensure lock callback: %v", err)
		}
	})

	operator := &dto.AuthPrincipal{UserID: 1, Username: "tenant-admin", ActiveTenantID: 101}
	ensured, err := StoreStaffBindingService.EnsureForInstance(instance, operator)
	if err != nil {
		t.Fatalf("ensure existing store staff binding: %v", err)
	}
	if ensured == nil || ensured.ID != binding.ID || ensured.AgentTeamID != team.ID {
		t.Fatalf("ensured binding = %+v", ensured)
	}
	if !slices.Equal(lockOrder, []string{"StoreStaffBinding", "AgentTeam"}) {
		t.Fatalf("ensure lock order = %v, want [StoreStaffBinding AgentTeam]", lockOrder)
	}
	updated := repositories.WxWorkProtocolInstanceRepository.GetInTenant(db, instance.ID, 101)
	if updated == nil || updated.StoreStaffBindingID != binding.ID || updated.AgentTeamID != team.ID {
		t.Fatalf("ensured instance = %+v", updated)
	}
}

func TestStoreDomainRepositoriesKeepTenantInFinalWritePredicate(t *testing.T) {
	db := setupStoreStaffTenantDB(t)
	teamB := createStoreStaffTenantTeam(t, db, 202, "最终条件客服组")
	userB := createStoreStaffTenantUser(t, db, 202, "final-predicate-user")
	storeB := createStoreStaffTenantStore(t, db, 202, "final-predicate-store")
	bindingB := createStoreStaffTenantBinding(t, db, 202, userB.ID, teamB.ID, storeB.ID)
	instanceB := createStoreStaffTenantInstance(t, db, 202, "final-predicate-instance", teamB.ID, storeB.ID, bindingB.ID)

	if err := repositories.StoreRepository.UpdatesInTenant(db, storeB.ID, 101, map[string]any{"name": "越权门店"}); err != nil {
		t.Fatalf("scoped store update: %v", err)
	}
	if err := repositories.StoreStaffBindingRepository.UpdatesInTenant(db, bindingB.ID, 101, map[string]any{"agent_team_id": 0}); err != nil {
		t.Fatalf("scoped binding update: %v", err)
	}
	if err := repositories.WxWorkProtocolInstanceRepository.UpdatesInTenant(db, instanceB.ID, 101, map[string]any{"employee_name": "越权员工号"}); err != nil {
		t.Fatalf("scoped instance update: %v", err)
	}
	if err := repositories.WxWorkProtocolInstanceRepository.UpdatesByStoreStaffBindingIDsInTenant(db, []int64{bindingB.ID}, 101, map[string]any{"agent_team_id": 0}); err != nil {
		t.Fatalf("scoped instances by binding update: %v", err)
	}

	if current := repositories.StoreRepository.Get(db, storeB.ID); current == nil || current.Name != storeB.Name {
		t.Fatalf("tenant B store changed: %+v", current)
	}
	if current := repositories.StoreStaffBindingRepository.Get(db, bindingB.ID); current == nil || current.AgentTeamID != teamB.ID {
		t.Fatalf("tenant B binding changed: %+v", current)
	}
	if current := repositories.WxWorkProtocolInstanceRepository.Get(db, instanceB.ID); current == nil || current.EmployeeName != "" || current.AgentTeamID != teamB.ID {
		t.Fatalf("tenant B instance changed: %+v", current)
	}
}

func setupStoreStaffTenantDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "store-staff-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Role{}, &models.UserRole{}, &models.AgentTeam{}, &models.Store{},
		&models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{}, &models.KnowledgeBase{},
	); err != nil {
		t.Fatalf("migrate store staff tenant models: %v", err)
	}
	sqls.SetDB(db)
	return db
}

func createStoreStaffTenantTeam(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.AgentTeam {
	t.Helper()
	item := &models.AgentTeam{TenantID: tenantID, Name: name, Status: enums.StatusOk}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create team %s: %v", name, err)
	}
	return item
}

func createStoreStaffTenantUser(t *testing.T, db *gorm.DB, tenantID int64, username string) *models.User {
	t.Helper()
	item := &models.User{TenantID: tenantID, Username: username, Nickname: username, Password: "test", Status: enums.StatusOk}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	role := &models.Role{}
	if err := db.Where("code = ?", constants.RoleCodeStoreStaff).First(role).Error; err != nil {
		role = &models.Role{Name: "门店员工", Code: constants.RoleCodeStoreStaff, Scope: constants.RoleScopeTenant, Status: enums.StatusOk}
		if createErr := db.Create(role).Error; createErr != nil {
			t.Fatalf("create store staff role: %v", createErr)
		}
	}
	if err := db.Create(&models.UserRole{UserID: item.ID, RoleID: role.ID}).Error; err != nil {
		t.Fatalf("assign store staff role to %s: %v", username, err)
	}
	return item
}

func createStoreStaffTenantStore(t *testing.T, db *gorm.DB, tenantID int64, code string) *models.Store {
	t.Helper()
	item := &models.Store{TenantID: tenantID, StoreCode: code, Name: code, Status: enums.StatusOk}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create store %s: %v", code, err)
	}
	return item
}

func createStoreStaffTenantBinding(t *testing.T, db *gorm.DB, tenantID, userID, teamID, storeID int64) *models.StoreStaffBinding {
	t.Helper()
	var activeUserID *int64
	if user := repositories.UserRepository.Get(db, userID); user != nil && user.TenantID == tenantID {
		activeUserID = positiveInt64Pointer(userID)
	}
	item := &models.StoreStaffBinding{
		TenantID: tenantID, UserID: userID, ActiveUserID: activeUserID,
		AgentTeamID: teamID, StoreID: storeID, Status: enums.StatusOk,
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create store staff binding: %v", err)
	}
	return item
}

func createStoreStaffTenantInstance(t *testing.T, db *gorm.DB, tenantID int64, guid string, teamID, storeID, bindingID int64) *models.WxWorkProtocolInstance {
	t.Helper()
	item := &models.WxWorkProtocolInstance{
		TenantID: tenantID, Guid: guid, AgentTeamID: teamID, StoreID: storeID,
		StoreStaffBindingID: bindingID, Status: enums.StatusOk,
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create wxwork instance %s: %v", guid, err)
	}
	return item
}
