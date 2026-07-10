package services

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAgentTeamScopeCanManageTeam(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.AgentTeam{}); err != nil {
		t.Fatalf("migrate agent team: %v", err)
	}
	sqls.SetDB(db)

	managed := &models.AgentTeam{Name: "managed", LeaderUserID: 11, Status: enums.StatusOk}
	other := &models.AgentTeam{Name: "other", LeaderUserID: 12, Status: enums.StatusOk}
	if err := db.Create(managed).Error; err != nil {
		t.Fatalf("create managed team: %v", err)
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("create other team: %v", err)
	}

	admin := &dto.AuthPrincipal{UserID: 1, Roles: []string{constants.RoleCodeAdmin}}
	leader := &dto.AuthPrincipal{UserID: 11, Roles: []string{constants.RoleCodeCsTeamLeader}}
	agent := &dto.AuthPrincipal{UserID: 21, Roles: []string{constants.RoleCodeCsUser}}

	if !AgentTeamScopeService.CanManageTeam(admin, other.ID) {
		t.Fatal("admin should manage every team")
	}
	if !AgentTeamScopeService.CanManageTeam(leader, managed.ID) {
		t.Fatal("leader should manage the bound team")
	}
	if AgentTeamScopeService.CanManageTeam(leader, other.ID) {
		t.Fatal("leader must not manage an unbound team")
	}
	if AgentTeamScopeService.CanManageTeam(agent, managed.ID) {
		t.Fatal("agent must not manage a team")
	}
}

func TestAgentTeamDerivesScopeFromWxWorkInstances(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Store{}, &models.WxWorkProtocolInstance{}); err != nil {
		t.Fatalf("migrate scope models: %v", err)
	}
	sqls.SetDB(db)

	store := &models.Store{StoreCode: "S001", Name: "测试门店", CompanyID: 7, Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{Guid: "scope-guid", StoreID: store.ID, CompanyID: 7, Status: enums.StatusOk}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	companyIDs, storeIDs, instanceIDs, err := AgentTeamService.deriveScopeFromWxWorkInstances([]int64{instance.ID})
	if err != nil {
		t.Fatalf("derive scope: %v", err)
	}
	if len(companyIDs) != 1 || companyIDs[0] != 7 {
		t.Fatalf("company scope = %v, want [7]", companyIDs)
	}
	if len(storeIDs) != 1 || storeIDs[0] != store.ID {
		t.Fatalf("store scope = %v, want [%d]", storeIDs, store.ID)
	}
	if len(instanceIDs) != 1 || instanceIDs[0] != instance.ID {
		t.Fatalf("instance scope = %v, want [%d]", instanceIDs, instance.ID)
	}
}
