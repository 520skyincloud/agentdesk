package services

import (
	"fmt"
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
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestAgentTeamScopeConversationVisibility(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Store{},
		&models.WxWorkProtocolInstance{},
		&models.AgentTeam{},
		&models.AgentProfile{},
		&models.StoreStaffBinding{},
		&models.Conversation{},
		&models.ConversationRouteState{},
	); err != nil {
		t.Fatalf("migrate scope models: %v", err)
	}
	sqls.SetDB(db)

	storeA := &models.Store{StoreCode: "scope-a", Name: "A门店", CompanyID: 1, Status: enums.StatusOk}
	storeB := &models.Store{StoreCode: "scope-b", Name: "B门店", CompanyID: 1, Status: enums.StatusOk}
	if err := db.Create(storeA).Error; err != nil {
		t.Fatalf("create store A: %v", err)
	}
	if err := db.Create(storeB).Error; err != nil {
		t.Fatalf("create store B: %v", err)
	}
	instanceA := &models.WxWorkProtocolInstance{Guid: "scope-instance-a", CompanyID: 1, StoreID: storeA.ID, Status: enums.StatusOk}
	instanceAOther := &models.WxWorkProtocolInstance{Guid: "scope-instance-a-other", CompanyID: 1, StoreID: storeA.ID, Status: enums.StatusOk}
	instanceB := &models.WxWorkProtocolInstance{Guid: "scope-instance-b", CompanyID: 1, StoreID: storeB.ID, Status: enums.StatusOk}
	if err := db.Create(instanceA).Error; err != nil {
		t.Fatalf("create instance A: %v", err)
	}
	if err := db.Create(instanceB).Error; err != nil {
		t.Fatalf("create instance B: %v", err)
	}
	if err := db.Create(instanceAOther).Error; err != nil {
		t.Fatalf("create other instance in store A: %v", err)
	}
	teamA := &models.AgentTeam{
		Name:                   "A组",
		LeaderUserID:           11,
		CompanyScopeIDs:        "1",
		StoreScopeIDs:          fmt.Sprint(storeA.ID),
		WxWorkInstanceScopeIDs: fmt.Sprint(instanceA.ID),
		Status:                 enums.StatusOk,
	}
	teamB := &models.AgentTeam{
		Name:                   "B组",
		LeaderUserID:           12,
		CompanyScopeIDs:        "1",
		StoreScopeIDs:          fmt.Sprint(storeB.ID),
		WxWorkInstanceScopeIDs: fmt.Sprint(instanceB.ID),
		Status:                 enums.StatusOk,
	}
	if err := db.Create(teamA).Error; err != nil {
		t.Fatalf("create team A: %v", err)
	}
	if err := db.Create(teamB).Error; err != nil {
		t.Fatalf("create team B: %v", err)
	}
	if err := db.Create(&models.AgentProfile{UserID: 21, TeamID: teamA.ID, AgentCode: "A021", DisplayName: "客服A", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create agent profile: %v", err)
	}
	if err := db.Create(&models.StoreStaffBinding{UserID: 31, CompanyID: 1, StoreID: storeA.ID, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create store staff binding: %v", err)
	}

	now := time.Now()
	conversationA := &models.Conversation{CustomerName: "客户A", Status: enums.IMConversationStatusActive, CurrentAssigneeID: 21, LastActiveAt: now.Add(-time.Minute), LastMessageAt: now.Add(-time.Minute)}
	conversationB := &models.Conversation{CustomerName: "客户B", Status: enums.IMConversationStatusActive, CurrentAssigneeID: 22, LastActiveAt: now, LastMessageAt: now}
	conversationAOther := &models.Conversation{CustomerName: "客户A-其他员工号", Status: enums.IMConversationStatusActive, LastActiveAt: now, LastMessageAt: now}
	if err := db.Create(conversationA).Error; err != nil {
		t.Fatalf("create conversation A: %v", err)
	}
	if err := db.Create(conversationB).Error; err != nil {
		t.Fatalf("create conversation B: %v", err)
	}
	if err := db.Create(conversationAOther).Error; err != nil {
		t.Fatalf("create conversation from other instance in store A: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{ConversationID: conversationA.ID, StoreID: storeA.ID, WxWorkInstanceID: instanceA.ID, NeedHumanFollowUp: true}).Error; err != nil {
		t.Fatalf("create route A: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{ConversationID: conversationB.ID, StoreID: storeB.ID, WxWorkInstanceID: instanceB.ID, NeedHumanFollowUp: true}).Error; err != nil {
		t.Fatalf("create route B: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{ConversationID: conversationAOther.ID, StoreID: storeA.ID, WxWorkInstanceID: instanceAOther.ID}).Error; err != nil {
		t.Fatalf("create route for other instance in store A: %v", err)
	}

	admin := &dto.AuthPrincipal{UserID: 1, Roles: []string{constants.RoleCodeAdmin}}
	leaderA := &dto.AuthPrincipal{UserID: 11, Roles: []string{constants.RoleCodeCsTeamLeader}}
	agentA := &dto.AuthPrincipal{UserID: 21, Roles: []string{constants.RoleCodeCsUser}}
	storeStaffA := &dto.AuthPrincipal{UserID: 31, Roles: []string{constants.RoleCodeStoreStaff}}
	for name, principal := range map[string]*dto.AuthPrincipal{
		"admin":       admin,
		"leader":      leaderA,
		"agent":       agentA,
		"store staff": storeStaffA,
	} {
		if !AgentTeamScopeService.CanViewConversation(principal, conversationA.ID) {
			t.Fatalf("%s should see conversation A", name)
		}
	}
	if AgentTeamScopeService.CanViewConversation(leaderA, conversationB.ID) {
		t.Fatal("leader must not see conversations from an unbound team")
	}
	if AgentTeamScopeService.CanViewConversation(agentA, conversationB.ID) {
		t.Fatal("agent must not see conversations from another team")
	}
	if AgentTeamScopeService.CanViewConversation(agentA, conversationAOther.ID) {
		t.Fatal("agent must not see an unbound account just because it belongs to the same store")
	}
	if !AgentTeamScopeService.CanViewConversation(storeStaffA, conversationAOther.ID) {
		t.Fatal("store staff should see every account in the bound store")
	}
	if AgentTeamScopeService.CanViewConversation(storeStaffA, conversationB.ID) {
		t.Fatal("store staff must not see conversations from another store")
	}

	list, _, err := ConversationService.ListConversations(agentA, request.AgentConversationFilterAllOpen, "", 0, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("list scoped conversations: %v", err)
	}
	if len(list) != 1 || list[0].ID != conversationA.ID {
		t.Fatalf("scoped conversations = %+v, want only conversation %d", list, conversationA.ID)
	}
	attention, _, err := ConversationService.ListConversations(agentA, request.AgentConversationFilterMyAttention, "", instanceB.ID, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("list my attention: %v", err)
	}
	if len(attention) != 1 || attention[0].ID != conversationA.ID {
		t.Fatalf("my attention = %+v, want only assigned conversation %d", attention, conversationA.ID)
	}
	pendingByTeam := ConversationDispatchWorkbenchService.PendingReplyCountsByTeam()
	if pendingByTeam[teamA.ID] != 1 || pendingByTeam[teamB.ID] != 1 {
		t.Fatalf("pending reply counts = %v, want team A=1 and team B=1", pendingByTeam)
	}
}

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
