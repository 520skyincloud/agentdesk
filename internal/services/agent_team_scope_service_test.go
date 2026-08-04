package services

import (
	"fmt"
	"slices"
	"testing"
	"time"

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
		&models.KnowledgeBase{},
		&models.Channel{},
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.ConversationAssignment{},
		&models.Message{},
	); err != nil {
		t.Fatalf("migrate scope models: %v", err)
	}
	sqls.SetDB(db)

	storeA := &models.Store{TenantID: 101, StoreCode: "scope-a", Name: "A门店", Status: enums.StatusOk}
	storeB := &models.Store{TenantID: 101, StoreCode: "scope-b", Name: "B门店", Status: enums.StatusOk}
	if err := db.Create(storeA).Error; err != nil {
		t.Fatalf("create store A: %v", err)
	}
	if err := db.Create(storeB).Error; err != nil {
		t.Fatalf("create store B: %v", err)
	}
	instanceA := &models.WxWorkProtocolInstance{TenantID: 101, Guid: "scope-instance-a", StoreID: storeA.ID, Status: enums.StatusOk}
	instanceAOther := &models.WxWorkProtocolInstance{TenantID: 101, Guid: "scope-instance-a-other", StoreID: storeA.ID, Status: enums.StatusOk}
	instanceB := &models.WxWorkProtocolInstance{TenantID: 101, Guid: "scope-instance-b", StoreID: storeB.ID, Status: enums.StatusOk}
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
		TenantID:               101,
		Name:                   "A组",
		LeaderUserID:           11,
		StoreScopeIDs:          fmt.Sprint(storeA.ID),
		WxWorkInstanceScopeIDs: fmt.Sprint(instanceA.ID),
		Status:                 enums.StatusOk,
	}
	teamB := &models.AgentTeam{
		TenantID:               101,
		Name:                   "B组",
		LeaderUserID:           12,
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
	if err := db.Create(&models.AgentProfile{TenantID: 101, UserID: 21, TeamID: teamA.ID, AgentCode: "A021", DisplayName: "客服A", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create agent profile: %v", err)
	}
	storeStaffUserID := int64(31)
	storeStaffBinding := &models.StoreStaffBinding{TenantID: 101, UserID: storeStaffUserID, ActiveUserID: &storeStaffUserID, StoreID: storeA.ID, Status: enums.StatusOk}
	if err := db.Create(storeStaffBinding).Error; err != nil {
		t.Fatalf("create store staff binding: %v", err)
	}
	otherStoreStaffUserID := int64(32)
	otherStoreStaffBinding := &models.StoreStaffBinding{TenantID: 101, UserID: otherStoreStaffUserID, ActiveUserID: &otherStoreStaffUserID, StoreID: storeA.ID, Status: enums.StatusOk}
	if err := db.Create(otherStoreStaffBinding).Error; err != nil {
		t.Fatalf("create other store staff binding: %v", err)
	}
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", instanceA.ID).Update("store_staff_binding_id", storeStaffBinding.ID).Error; err != nil {
		t.Fatalf("bind instance A: %v", err)
	}
	instanceA.StoreStaffBindingID = storeStaffBinding.ID
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", instanceAOther.ID).Update("store_staff_binding_id", otherStoreStaffBinding.ID).Error; err != nil {
		t.Fatalf("bind other instance in store A: %v", err)
	}
	instanceAOther.StoreStaffBindingID = otherStoreStaffBinding.ID
	instanceStoreMismatch := &models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "scope-instance-store-mismatch", StoreID: storeB.ID,
		StoreStaffBindingID: storeStaffBinding.ID, Status: enums.StatusOk,
	}
	if err := db.Create(instanceStoreMismatch).Error; err != nil {
		t.Fatalf("create store-mismatched instance: %v", err)
	}
	channel := &models.Channel{TenantID: 101, Name: "范围测试渠道", ChannelType: enums.ChannelTypeWxWorkProtocol, ChannelID: "scope-channel", Status: enums.StatusOk}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	now := time.Now()
	conversationA := &models.Conversation{TenantID: 101, StoreID: storeA.ID, StoreStaffBindingID: storeStaffBinding.ID, ChannelID: channel.ID, CustomerName: "客户A", Status: enums.IMConversationStatusActive, CurrentAssigneeID: 21, LastActiveAt: now.Add(-time.Minute), LastMessageAt: now.Add(-time.Minute)}
	conversationB := &models.Conversation{TenantID: 101, ChannelID: channel.ID, CustomerName: "客户B", Status: enums.IMConversationStatusActive, CurrentAssigneeID: 22, LastActiveAt: now, LastMessageAt: now}
	conversationAOther := &models.Conversation{TenantID: 101, StoreID: storeA.ID, StoreStaffBindingID: otherStoreStaffBinding.ID, ChannelID: channel.ID, CustomerName: "客户A-其他员工号", Status: enums.IMConversationStatusActive, LastActiveAt: now, LastMessageAt: now}
	closedConversationA := &models.Conversation{TenantID: 101, StoreID: storeA.ID, StoreStaffBindingID: storeStaffBinding.ID, ChannelID: channel.ID, CustomerName: "客户A-历史", Status: enums.IMConversationStatusClosed, CurrentAssigneeID: 999, LastActiveAt: now.Add(-2 * time.Hour), LastMessageAt: now.Add(-2 * time.Hour)}
	if err := db.Create(conversationA).Error; err != nil {
		t.Fatalf("create conversation A: %v", err)
	}
	if err := db.Create(conversationB).Error; err != nil {
		t.Fatalf("create conversation B: %v", err)
	}
	if err := db.Create(conversationAOther).Error; err != nil {
		t.Fatalf("create conversation from other instance in store A: %v", err)
	}
	if err := db.Create(closedConversationA).Error; err != nil {
		t.Fatalf("create closed conversation for binding A: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: conversationA.ID, StoreID: storeA.ID, StoreStaffBindingID: storeStaffBinding.ID, WxWorkInstanceID: instanceA.ID, NeedHumanFollowUp: true}).Error; err != nil {
		t.Fatalf("create route A: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: conversationB.ID, StoreID: storeB.ID, WxWorkInstanceID: instanceB.ID, NeedHumanFollowUp: true}).Error; err != nil {
		t.Fatalf("create route B: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: conversationAOther.ID, StoreID: storeA.ID, StoreStaffBindingID: otherStoreStaffBinding.ID, WxWorkInstanceID: instanceAOther.ID}).Error; err != nil {
		t.Fatalf("create route for other instance in store A: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: closedConversationA.ID, StoreID: storeA.ID, StoreStaffBindingID: storeStaffBinding.ID, WxWorkInstanceID: instanceA.ID}).Error; err != nil {
		t.Fatalf("create route for closed binding A conversation: %v", err)
	}

	admin := &dto.AuthPrincipal{UserID: 1, ActiveTenantID: 101, Roles: []string{constants.RoleCodeAdmin}}
	leaderA := &dto.AuthPrincipal{UserID: 11, ActiveTenantID: 101, Roles: []string{constants.RoleCodeCsTeamLeader}}
	agentA := &dto.AuthPrincipal{UserID: 21, ActiveTenantID: 101, Roles: []string{constants.RoleCodeCsUser}}
	storeStaffA := &dto.AuthPrincipal{UserID: storeStaffUserID, ActiveTenantID: 101, Roles: []string{constants.RoleCodeStoreStaff}}
	otherStoreStaffA := &dto.AuthPrincipal{UserID: otherStoreStaffUserID, ActiveTenantID: 101, Roles: []string{constants.RoleCodeStoreStaff}}
	hybridLeaderA := &dto.AuthPrincipal{UserID: 11, ActiveTenantID: 101, Roles: []string{constants.RoleCodeCsTeamLeader, constants.RoleCodeStoreStaff}}
	for name, principal := range map[string]*dto.AuthPrincipal{
		"admin":         admin,
		"leader":        leaderA,
		"hybrid leader": hybridLeaderA,
		"agent":         agentA,
		"store staff":   storeStaffA,
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
	if AgentTeamScopeService.CanViewConversation(storeStaffA, conversationAOther.ID) {
		t.Fatal("store staff must not see another staff binding in the same store")
	}
	if AgentTeamScopeService.CanViewConversation(storeStaffA, conversationB.ID) {
		t.Fatal("store staff must not see conversations from another store")
	}
	if AgentTeamScopeService.Resolve(hybridLeaderA).StoreStaffScoped {
		t.Fatal("adding the store staff role must not narrow an existing team leader scope")
	}
	if !AgentTeamScopeService.CanViewWxWorkInstance(storeStaffA, instanceA.ID) {
		t.Fatal("store staff should see the protocol instance attached to its binding")
	}
	if AgentTeamScopeService.CanViewWxWorkInstance(storeStaffA, instanceAOther.ID) {
		t.Fatal("store staff must not see another binding's protocol instance in the same store")
	}
	if AgentTeamScopeService.CanViewWxWorkInstance(storeStaffA, instanceStoreMismatch.ID) {
		t.Fatal("store staff must not see a protocol instance whose store does not match the binding")
	}
	storeStaffInstances := repositories.WxWorkProtocolInstanceRepository.Find(db,
		AgentTeamScopeService.ApplyWxWorkInstanceFilter(sqls.NewCnd().Asc("id"), storeStaffA))
	if len(storeStaffInstances) != 1 || storeStaffInstances[0].ID != instanceA.ID {
		t.Fatalf("store staff instances = %+v, want only binding/store-matched instance %d", storeStaffInstances, instanceA.ID)
	}
	if err := db.Model(&models.ConversationRouteState{}).
		Where("conversation_id = ? AND tenant_id = ?", conversationA.ID, int64(101)).
		Update("route_status", enums.ConversationRouteStatusStoreWecomManual).Error; err != nil {
		t.Fatalf("enter binding A store manual route: %v", err)
	}
	if err := db.Model(&models.Conversation{}).
		Where("id = ? AND tenant_id = ?", conversationA.ID, int64(101)).
		Update("current_assignee_id", 0).Error; err != nil {
		t.Fatalf("release binding A conversation from HQ assignment: %v", err)
	}
	conversationA.CurrentAssigneeID = 0
	if !MessageService.canSendStoreManualAgentMessage(conversationA, storeStaffA) {
		t.Fatal("store staff should reply to the store manual conversation owned by its binding")
	}
	if MessageService.canSendStoreManualAgentMessage(conversationA, otherStoreStaffA) {
		t.Fatal("store staff must not reply to another binding's conversation in the same store")
	}
	if _, err := MessageService.ValidateConversationSender(conversationA.ID, enums.IMSenderTypeAgent, storeStaffA, nil); err != nil {
		t.Fatalf("validate own binding store manual reply: %v", err)
	}
	if _, err := MessageService.ValidateConversationSender(conversationA.ID, enums.IMSenderTypeAgent, otherStoreStaffA, nil); err == nil {
		t.Fatal("another store staff binding unexpectedly passed sender validation")
	}
	if err := db.Model(&models.Conversation{}).
		Where("id = ? AND tenant_id = ?", conversationA.ID, int64(101)).
		Update("current_assignee_id", int64(21)).Error; err != nil {
		t.Fatalf("restore binding A HQ assignment: %v", err)
	}
	conversationA.CurrentAssigneeID = 21

	list, _, err := ConversationService.ListConversations(agentA, request.AgentConversationFilterAllOpen, "", 0, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("list scoped conversations: %v", err)
	}
	if len(list) != 1 || list[0].ID != conversationA.ID {
		t.Fatalf("scoped conversations = %+v, want only conversation %d", list, conversationA.ID)
	}
	storeStaffList, _, err := ConversationService.ListConversations(storeStaffA, request.AgentConversationFilterAllOpen, "", 0, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("list store staff conversations: %v", err)
	}
	if len(storeStaffList) != 1 || storeStaffList[0].ID != conversationA.ID {
		t.Fatalf("store staff conversations = %+v, want only binding conversation %d", storeStaffList, conversationA.ID)
	}
	forgedInstanceList, _, err := ConversationService.ListConversations(storeStaffA, request.AgentConversationFilterAllOpen, "", instanceAOther.ID, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("list with forged instance filter: %v", err)
	}
	if len(forgedInstanceList) != 0 {
		t.Fatalf("forged instance filter leaked conversations: %+v", forgedInstanceList)
	}
	closedStoreStaffList, _, err := ConversationService.ListConversations(storeStaffA, request.AgentConversationFilterClosed, "", 0, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("list closed store staff conversations: %v", err)
	}
	if len(closedStoreStaffList) != 1 || closedStoreStaffList[0].ID != closedConversationA.ID {
		t.Fatalf("closed store staff conversations = %+v, want binding history %d", closedStoreStaffList, closedConversationA.ID)
	}
	attention, _, err := ConversationService.ListConversations(agentA, request.AgentConversationFilterMyAttention, "", instanceB.ID, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("list my attention: %v", err)
	}
	if len(attention) != 1 || attention[0].ID != conversationA.ID {
		t.Fatalf("my attention = %+v, want only assigned conversation %d", attention, conversationA.ID)
	}
	pendingByTeam := ConversationDispatchWorkbenchService.PendingReplyCountsByTeam(agentA)
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

	managed := &models.AgentTeam{TenantID: 101, Name: "managed", LeaderUserID: 11, Status: enums.StatusOk}
	other := &models.AgentTeam{TenantID: 101, Name: "other", LeaderUserID: 12, Status: enums.StatusOk}
	if err := db.Create(managed).Error; err != nil {
		t.Fatalf("create managed team: %v", err)
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("create other team: %v", err)
	}

	admin := &dto.AuthPrincipal{UserID: 1, ActiveTenantID: 101, Roles: []string{constants.RoleCodeAdmin}}
	leader := &dto.AuthPrincipal{UserID: 11, ActiveTenantID: 101, Roles: []string{constants.RoleCodeCsTeamLeader}}
	agent := &dto.AuthPrincipal{UserID: 21, ActiveTenantID: 101, Roles: []string{constants.RoleCodeCsUser}}

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
	withoutTenant := *admin
	withoutTenant.ActiveTenantID = 0
	if AgentTeamScopeService.CanManageTeam(&withoutTenant, managed.ID) {
		t.Fatal("platform admin must select a tenant before managing teams")
	}
}

func TestTenantAdminCreatesAndManagesTeamsOnlyInActiveTenant(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.AgentTeam{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{}); err != nil {
		t.Fatalf("migrate tenant team models: %v", err)
	}
	sqls.SetDB(db)
	operator := &dto.AuthPrincipal{
		UserID: 51, Username: "tenant-admin", TenantID: 101, ActiveTenantID: 101,
		Roles: []string{constants.RoleCodeTenantAdmin},
	}
	team, err := AgentTeamService.CreateAgentTeam(request.CreateAgentTeamRequest{Name: "租户客服组", Status: int(enums.StatusOk)}, operator)
	if err != nil {
		t.Fatalf("CreateAgentTeam() error = %v", err)
	}
	if team.TenantID != operator.ActiveTenantID || !AgentTeamScopeService.CanManageTeam(operator, team.ID) {
		t.Fatalf("created team=%+v is not manageable in active tenant", team)
	}
	other := &models.AgentTeam{TenantID: 202, Name: "其他租户客服组", Status: enums.StatusOk}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("create other tenant team: %v", err)
	}
	if AgentTeamScopeService.CanManageTeam(operator, other.ID) {
		t.Fatal("tenant admin must not manage another tenant's team")
	}
	withoutContext := *operator
	withoutContext.ActiveTenantID = 0
	if _, err := AgentTeamService.CreateAgentTeam(request.CreateAgentTeamRequest{Name: "无上下文客服组", Status: int(enums.StatusOk)}, &withoutContext); err == nil {
		t.Fatal("expected team creation without active tenant to be rejected")
	}
}

func TestBindStoreStaffUserMovesCanonicalTeamAndSyncsWxWork(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Role{}, &models.UserRole{}, &models.Store{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{}, &models.AgentTeam{}); err != nil {
		t.Fatalf("migrate binding models: %v", err)
	}
	sqls.SetDB(db)

	store := &models.Store{TenantID: 101, StoreCode: "BIND-001", Name: "绑定测试门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	user := &models.User{TenantID: 101, Username: "binding-store-staff", Nickname: "绑定门店员工", Status: enums.StatusOk}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	role := &models.Role{Name: "门店员工", Code: constants.RoleCodeStoreStaff, Status: enums.StatusOk}
	if err := db.Create(role).Error; err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := db.Create(&models.UserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
		t.Fatalf("create user role: %v", err)
	}
	teamA := &models.AgentTeam{TenantID: 101, Name: "绑定A组", Status: enums.StatusOk}
	teamB := &models.AgentTeam{TenantID: 101, Name: "绑定B组", Status: enums.StatusOk}
	if err := db.Create(teamA).Error; err != nil {
		t.Fatalf("create team A: %v", err)
	}
	if err := db.Create(teamB).Error; err != nil {
		t.Fatalf("create team B: %v", err)
	}
	binding := &models.StoreStaffBinding{TenantID: 101, UserID: user.ID, StoreID: store.ID, Status: enums.StatusOk}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create store staff binding: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{TenantID: 101, Guid: "binding-instance", StoreID: store.ID, StoreStaffBindingID: binding.ID, Status: enums.StatusOk}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	admin := &dto.AuthPrincipal{UserID: 1, Username: "admin", ActiveTenantID: 101, Roles: []string{constants.RoleCodeAdmin}}

	if err := AgentTeamService.BindStoreStaffUser(user.ID, teamA.ID, admin); err != nil {
		t.Fatalf("bind team A: %v", err)
	}
	if current := StoreStaffBindingService.Get(binding.ID); current == nil || current.AgentTeamID != teamA.ID {
		t.Fatalf("store staff team = %+v, want %d", current, teamA.ID)
	}
	if current := WxWorkProtocolInstanceService.Get(instance.ID); current == nil || current.AgentTeamID != teamA.ID {
		t.Fatalf("instance team = %+v, want %d", current, teamA.ID)
	}
	if current := AgentTeamService.Get(teamA.ID); current == nil || current.WxWorkInstanceScopeIDs != fmt.Sprint(instance.ID) || current.StoreScopeIDs != fmt.Sprint(store.ID) {
		t.Fatalf("team A scope = %+v", current)
	}

	if err := AgentTeamService.BindStoreStaffUser(user.ID, teamB.ID, admin); err != nil {
		t.Fatalf("move to team B: %v", err)
	}
	if current := AgentTeamService.Get(teamA.ID); current == nil || current.WxWorkInstanceScopeIDs != "" || current.StoreScopeIDs != "" {
		t.Fatalf("team A scope was not cleared: %+v", current)
	}
	if current := AgentTeamService.Get(teamB.ID); current == nil || current.WxWorkInstanceScopeIDs != fmt.Sprint(instance.ID) || current.StoreScopeIDs != fmt.Sprint(store.ID) {
		t.Fatalf("team B scope = %+v", current)
	}
}

func TestUpdateAgentTeamAcceptsLegacyWxWorkScopeWithoutSilentClear(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Role{}, &models.UserRole{}, &models.Store{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{}, &models.AgentTeam{}); err != nil {
		t.Fatalf("migrate binding models: %v", err)
	}
	sqls.SetDB(db)

	role := &models.Role{Name: "门店员工", Code: constants.RoleCodeStoreStaff, Status: enums.StatusOk}
	user := &models.User{TenantID: 101, Username: "legacy-scope-store-staff", Nickname: "旧范围门店员工", Status: enums.StatusOk}
	store := &models.Store{TenantID: 101, StoreCode: "LEGACY-SCOPE", Name: "旧范围测试门店", Status: enums.StatusOk}
	team := &models.AgentTeam{TenantID: 101, Name: "旧范围测试组", Status: enums.StatusOk}
	for _, item := range []any{role, user, store, team} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create fixture: %v", err)
		}
	}
	if err := db.Create(&models.UserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
		t.Fatalf("create user role: %v", err)
	}
	binding := &models.StoreStaffBinding{TenantID: 101, UserID: user.ID, StoreID: store.ID, Status: enums.StatusOk}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create binding: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{TenantID: 101, Guid: "legacy-scope-instance", StoreID: store.ID, StoreStaffBindingID: binding.ID, Status: enums.StatusOk}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	admin := &dto.AuthPrincipal{UserID: 1, Username: "admin", ActiveTenantID: 101, Roles: []string{constants.RoleCodeAdmin}}

	if err := AgentTeamService.UpdateAgentTeam(request.UpdateAgentTeamRequest{
		ID: team.ID, Name: team.Name, Status: int(enums.StatusOk), WxWorkInstanceScopeIDs: []int64{instance.ID},
	}, admin); err != nil {
		t.Fatalf("update with legacy scope: %v", err)
	}
	if current := StoreStaffBindingService.Get(binding.ID); current == nil || current.AgentTeamID != team.ID {
		t.Fatalf("legacy scope binding = %+v, want team %d", current, team.ID)
	}
	if current := WxWorkProtocolInstanceService.Get(instance.ID); current == nil || current.AgentTeamID != team.ID {
		t.Fatalf("legacy scope instance = %+v, want team %d", current, team.ID)
	}
}

func TestAgentTeamLegacyInstanceScopeRejectsMissingExactBinding(t *testing.T) {
	db := setupStoreStaffTenantDB(t)
	user := createStoreStaffTenantUser(t, db, 101, "legacy-unbound-instance-user")
	store := createStoreStaffTenantStore(t, db, 101, "legacy-unbound-instance-store")
	createStoreStaffTenantBinding(t, db, 101, user.ID, 0, store.ID)
	legacyInstance := &models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "legacy-unbound-instance", StoreID: store.ID,
		StoreStaffBindingID: 0, Status: enums.StatusOk,
	}
	if err := db.Create(legacyInstance).Error; err != nil {
		t.Fatalf("create legacy unbound instance: %v", err)
	}

	userIDs, provided, err := AgentTeamService.resolveRequestedStoreStaffUserIDsDB(db, 101, nil, []int64{legacyInstance.ID})
	if err == nil || len(userIDs) != 0 {
		t.Fatalf("legacy instance borrowed Store binding: users=%v provided=%v err=%v", userIDs, provided, err)
	}
}

func TestUpdateAgentTeamReplacesStoreStaffBindingsAndSyncsBothDirections(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Role{}, &models.UserRole{}, &models.Store{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{}, &models.AgentTeam{}); err != nil {
		t.Fatalf("migrate binding models: %v", err)
	}
	sqls.SetDB(db)

	role := &models.Role{Name: "门店员工", Code: constants.RoleCodeStoreStaff, Status: enums.StatusOk}
	if err := db.Create(role).Error; err != nil {
		t.Fatalf("create role: %v", err)
	}
	teamA := &models.AgentTeam{TenantID: 101, Name: "批量绑定A组", Status: enums.StatusOk}
	teamB := &models.AgentTeam{TenantID: 101, Name: "批量绑定B组", Status: enums.StatusOk}
	if err := db.Create(teamA).Error; err != nil {
		t.Fatalf("create team A: %v", err)
	}
	if err := db.Create(teamB).Error; err != nil {
		t.Fatalf("create team B: %v", err)
	}

	type staffFixture struct {
		user     *models.User
		store    *models.Store
		binding  *models.StoreStaffBinding
		instance *models.WxWorkProtocolInstance
	}
	staff := make([]staffFixture, 0, 3)
	for i := 1; i <= 3; i++ {
		user := &models.User{TenantID: 101, Username: fmt.Sprintf("batch-store-staff-%d", i), Nickname: fmt.Sprintf("门店员工%d", i), Status: enums.StatusOk}
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("create user %d: %v", i, err)
		}
		if err := db.Create(&models.UserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
			t.Fatalf("create user role %d: %v", i, err)
		}
		store := &models.Store{TenantID: 101, StoreCode: fmt.Sprintf("BATCH-%03d", i), Name: fmt.Sprintf("批量门店%d", i), Status: enums.StatusOk}
		if err := db.Create(store).Error; err != nil {
			t.Fatalf("create store %d: %v", i, err)
		}
		initialTeamID := int64(0)
		if i == 2 {
			initialTeamID = teamB.ID
		}
		binding := &models.StoreStaffBinding{TenantID: 101, UserID: user.ID, AgentTeamID: initialTeamID, StoreID: store.ID, Status: enums.StatusOk}
		if err := db.Create(binding).Error; err != nil {
			t.Fatalf("create binding %d: %v", i, err)
		}
		instance := &models.WxWorkProtocolInstance{TenantID: 101, Guid: fmt.Sprintf("batch-instance-%d", i), AgentTeamID: initialTeamID, StoreID: store.ID, StoreStaffBindingID: binding.ID, Status: enums.StatusOk}
		if err := db.Create(instance).Error; err != nil {
			t.Fatalf("create instance %d: %v", i, err)
		}
		staff = append(staff, staffFixture{user: user, store: store, binding: binding, instance: instance})
	}
	admin := &dto.AuthPrincipal{UserID: 1, Username: "admin", ActiveTenantID: 101, Roles: []string{constants.RoleCodeAdmin}}

	if err := AgentTeamService.UpdateAgentTeam(request.UpdateAgentTeamRequest{
		ID: teamA.ID, Name: teamA.Name, Status: int(enums.StatusOk), StoreStaffUserIDs: []int64{staff[0].user.ID, staff[1].user.ID},
	}, admin); err != nil {
		t.Fatalf("batch assign team A: %v", err)
	}
	for _, index := range []int{0, 1} {
		if current := StoreStaffBindingService.Get(staff[index].binding.ID); current == nil || current.AgentTeamID != teamA.ID {
			t.Fatalf("binding %d team = %+v, want %d", index, current, teamA.ID)
		}
		if current := WxWorkProtocolInstanceService.Get(staff[index].instance.ID); current == nil || current.AgentTeamID != teamA.ID {
			t.Fatalf("instance %d team = %+v, want %d", index, current, teamA.ID)
		}
	}

	if err := AgentTeamService.UpdateAgentTeam(request.UpdateAgentTeamRequest{
		ID: teamB.ID, Name: teamB.Name, Status: int(enums.StatusOk), StoreStaffUserIDs: []int64{staff[1].user.ID, staff[2].user.ID},
	}, admin); err != nil {
		t.Fatalf("move and assign team B: %v", err)
	}
	if got := AgentTeamService.FindStoreStaffUserIDs(teamA.ID); len(got) != 1 || got[0] != staff[0].user.ID {
		t.Fatalf("team A users = %v, want [%d]", got, staff[0].user.ID)
	}
	if got := AgentTeamService.FindStoreStaffUserIDs(teamB.ID); len(got) != 2 || got[0] != staff[1].user.ID || got[1] != staff[2].user.ID {
		t.Fatalf("team B users = %v", got)
	}
	if current := AgentTeamService.Get(teamA.ID); current == nil || current.WxWorkInstanceScopeIDs != fmt.Sprint(staff[0].instance.ID) {
		t.Fatalf("team A scope = %+v", current)
	}

	if err := AgentTeamService.UpdateAgentTeam(request.UpdateAgentTeamRequest{
		ID: teamA.ID, Name: teamA.Name, Status: int(enums.StatusOk), StoreStaffUserIDs: []int64{},
	}, admin); err != nil {
		t.Fatalf("clear team A: %v", err)
	}
	if current := StoreStaffBindingService.Get(staff[0].binding.ID); current == nil || current.AgentTeamID != 0 {
		t.Fatalf("cleared binding = %+v, want unassigned", current)
	}
	if current := WxWorkProtocolInstanceService.Get(staff[0].instance.ID); current == nil || current.AgentTeamID != 0 {
		t.Fatalf("cleared instance = %+v, want unassigned", current)
	}
	if current := AgentTeamService.Get(teamA.ID); current == nil || current.StoreScopeIDs != "" || current.WxWorkInstanceScopeIDs != "" {
		t.Fatalf("team A scope was not cleared: %+v", current)
	}
}

func TestStoreStaffBidirectionalAssignmentUsesOrderedLocks(t *testing.T) {
	tests := []struct {
		name      string
		wantOrder []string
		action    func(fixture storeStaffAssignmentLockFixture) error
	}{
		{
			name:      "user management reverse binding",
			wantOrder: []string{"User", "StoreStaffBinding", "AgentTeam", "AgentTeam"},
			action: func(fixture storeStaffAssignmentLockFixture) error {
				return AgentTeamService.BindStoreStaffUser(fixture.user.ID, fixture.teamA.ID, fixture.admin)
			},
		},
		{
			name:      "team batch binding",
			wantOrder: []string{"StoreStaffBinding", "AgentTeam", "AgentTeam"},
			action: func(fixture storeStaffAssignmentLockFixture) error {
				return AgentTeamService.UpdateAgentTeam(request.UpdateAgentTeamRequest{
					ID: fixture.teamA.ID, Name: fixture.teamA.Name, Status: int(enums.StatusOk), StoreStaffUserIDs: []int64{fixture.user.ID},
				}, fixture.admin)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := setupStoreStaffAssignmentLockFixture(t)
			lockOrder := make([]string, 0, len(tt.wantOrder))
			teamIDs := make([]int64, 0, 2)
			callbackName := "test:store-staff-assignment-locks-" + tt.name
			if err := fixture.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if _, locked := tx.Statement.Clauses["FOR"]; !locked || tx.Statement.Schema == nil {
					return
				}
				switch tx.Statement.Schema.Name {
				case "User", "StoreStaffBinding":
					lockOrder = append(lockOrder, tx.Statement.Schema.Name)
				case "AgentTeam":
					lockOrder = append(lockOrder, tx.Statement.Schema.Name)
					if item, ok := tx.Statement.Dest.(*models.AgentTeam); ok {
						teamIDs = append(teamIDs, item.ID)
					}
				}
			}); err != nil {
				t.Fatalf("register lock callback: %v", err)
			}
			t.Cleanup(func() {
				if err := fixture.db.Callback().Query().Remove(callbackName); err != nil {
					t.Errorf("remove lock callback: %v", err)
				}
			})

			if err := tt.action(fixture); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if !slices.Equal(lockOrder, tt.wantOrder) {
				t.Fatalf("%s lock order = %v, want %v", tt.name, lockOrder, tt.wantOrder)
			}
			if !slices.Equal(teamIDs, []int64{fixture.teamA.ID, fixture.teamB.ID}) {
				t.Fatalf("%s team lock order = %v, want [%d %d]", tt.name, teamIDs, fixture.teamA.ID, fixture.teamB.ID)
			}
			if binding := StoreStaffBindingService.Get(fixture.binding.ID); binding == nil || binding.AgentTeamID != fixture.teamA.ID {
				t.Fatalf("%s binding = %+v, want team %d", tt.name, binding, fixture.teamA.ID)
			}
			if instance := WxWorkProtocolInstanceService.Get(fixture.instance.ID); instance == nil || instance.AgentTeamID != fixture.teamA.ID {
				t.Fatalf("%s instance = %+v, want team %d", tt.name, instance, fixture.teamA.ID)
			}
		})
	}
}

func TestStoreStaffAssignmentRejectsUnmanageableOriginalTeamWithoutChanges(t *testing.T) {
	fixture := setupStoreStaffAssignmentLockFixture(t)
	fixture.teamA.LeaderUserID = 7001
	fixture.teamB.LeaderUserID = 7002
	if err := fixture.db.Model(&models.AgentTeam{}).Where("id = ?", fixture.teamA.ID).Update("leader_user_id", fixture.teamA.LeaderUserID).Error; err != nil {
		t.Fatalf("set team A leader: %v", err)
	}
	if err := fixture.db.Model(&models.AgentTeam{}).Where("id = ?", fixture.teamB.ID).Update("leader_user_id", fixture.teamB.LeaderUserID).Error; err != nil {
		t.Fatalf("set team B leader: %v", err)
	}
	teamLeader := &dto.AuthPrincipal{
		UserID: fixture.teamA.LeaderUserID, Username: "team-a-leader", ActiveTenantID: 101, Roles: []string{constants.RoleCodeCsTeamLeader},
	}
	if err := AgentTeamService.BindStoreStaffUser(fixture.user.ID, fixture.teamA.ID, teamLeader); err == nil {
		t.Fatal("team A leader must not move store staff out of unmanaged team B")
	}
	if binding := StoreStaffBindingService.Get(fixture.binding.ID); binding == nil || binding.AgentTeamID != fixture.teamB.ID {
		t.Fatalf("rejected assignment changed binding: %+v", binding)
	}
	if instance := WxWorkProtocolInstanceService.Get(fixture.instance.ID); instance == nil || instance.AgentTeamID != fixture.teamB.ID {
		t.Fatalf("rejected assignment changed instance: %+v", instance)
	}
}

type storeStaffAssignmentLockFixture struct {
	db       *gorm.DB
	admin    *dto.AuthPrincipal
	user     *models.User
	teamA    *models.AgentTeam
	teamB    *models.AgentTeam
	binding  *models.StoreStaffBinding
	instance *models.WxWorkProtocolInstance
}

func setupStoreStaffAssignmentLockFixture(t *testing.T) storeStaffAssignmentLockFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Role{}, &models.UserRole{}, &models.Store{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{}, &models.AgentTeam{}); err != nil {
		t.Fatalf("migrate assignment lock models: %v", err)
	}
	sqls.SetDB(db)
	role := &models.Role{Name: "门店员工", Code: constants.RoleCodeStoreStaff, Scope: constants.RoleScopeTenant, Status: enums.StatusOk}
	user := &models.User{TenantID: 101, Username: "ordered-lock-store-staff", Status: enums.StatusOk}
	teamA := &models.AgentTeam{TenantID: 101, Name: "有序锁A组", Status: enums.StatusOk}
	teamB := &models.AgentTeam{TenantID: 101, Name: "有序锁B组", Status: enums.StatusOk}
	store := &models.Store{TenantID: 101, StoreCode: "ORDERED-LOCK", Name: "有序锁门店", Status: enums.StatusOk}
	for _, item := range []any{role, user, teamA, teamB, store} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create assignment lock fixture: %v", err)
		}
	}
	if err := db.Create(&models.UserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
		t.Fatalf("assign store staff role: %v", err)
	}
	binding := &models.StoreStaffBinding{TenantID: 101, UserID: user.ID, AgentTeamID: teamB.ID, StoreID: store.ID, Status: enums.StatusOk}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create assignment binding: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "ordered-lock-instance", AgentTeamID: teamB.ID, StoreID: store.ID, StoreStaffBindingID: binding.ID, Status: enums.StatusOk,
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create assignment instance: %v", err)
	}
	return storeStaffAssignmentLockFixture{
		db: db, admin: &dto.AuthPrincipal{UserID: 1, Username: "admin", ActiveTenantID: 101, Roles: []string{constants.RoleCodeAdmin}},
		user: user, teamA: teamA, teamB: teamB, binding: binding, instance: instance,
	}
}

func TestAgentTeamMutationsUseTeamRowLock(t *testing.T) {
	tests := []struct {
		name   string
		action func(team *models.AgentTeam, operator *dto.AuthPrincipal) error
	}{
		{
			name: "update",
			action: func(team *models.AgentTeam, operator *dto.AuthPrincipal) error {
				return AgentTeamService.UpdateAgentTeam(request.UpdateAgentTeamRequest{
					ID: team.ID, Name: "锁内更新客服组", Status: int(enums.StatusOk),
				}, operator)
			},
		},
		{
			name: "delete",
			action: func(team *models.AgentTeam, operator *dto.AuthPrincipal) error {
				return AgentTeamService.DeleteAgentTeam(team.ID, operator)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, team, operator := setupAgentTeamMutationTestDB(t)
			seenLock := false
			callbackName := "test:agent-team-" + tt.name + "-lock"
			if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "AgentTeam" {
					if _, locked := tx.Statement.Clauses["FOR"]; locked {
						seenLock = true
					}
				}
			}); err != nil {
				t.Fatalf("register team lock callback: %v", err)
			}
			t.Cleanup(func() {
				if err := db.Callback().Query().Remove(callbackName); err != nil {
					t.Errorf("remove team lock callback: %v", err)
				}
			})

			if err := tt.action(team, operator); err != nil {
				t.Fatalf("%s team: %v", tt.name, err)
			}
			if !seenLock {
				t.Fatalf("%s did not lock the AgentTeam row", tt.name)
			}
		})
	}
}

func TestDeleteAgentTeamChecksScheduleInsideTransaction(t *testing.T) {
	db, team, operator := setupAgentTeamMutationTestDB(t)
	schedule := &models.AgentTeamSchedule{
		TenantID: team.TenantID, TeamID: team.ID, StartAt: time.Now().Add(time.Hour), EndAt: time.Now().Add(2 * time.Hour), Status: enums.StatusOk,
	}
	if err := db.Create(schedule).Error; err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if err := AgentTeamService.DeleteAgentTeam(team.ID, operator); err == nil {
		t.Fatal("DeleteAgentTeam() with schedule should fail")
	}
	current := AgentTeamService.Get(team.ID)
	if current == nil || current.Status != enums.StatusOk {
		t.Fatalf("team changed after rejected delete: %+v", current)
	}
}

func setupAgentTeamMutationTestDB(t *testing.T) (*gorm.DB, *models.AgentTeam, *dto.AuthPrincipal) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open agent team mutation db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Role{}, &models.UserRole{}, &models.AgentTeam{}, &models.AgentProfile{},
		&models.AgentTeamSquad{}, &models.WxWorkProtocolInstance{}, &models.StoreStaffBinding{},
		&models.AgentTeamSchedule{}, &models.AIAgent{},
	); err != nil {
		t.Fatalf("migrate agent team mutation models: %v", err)
	}
	sqls.SetDB(db)
	team := &models.AgentTeam{TenantID: 101, Name: "事务客服组", Status: enums.StatusOk}
	if err := db.Create(team).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: 1, Username: "admin", ActiveTenantID: 101, Roles: []string{constants.RoleCodeAdmin}}
	return db, team, operator
}

func TestStoreStaffScopeIncludesEveryKnowledgeBaseOwnedByStore(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Store{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{}, &models.KnowledgeBase{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})

	const tenantID int64 = 101
	store := &models.Store{TenantID: tenantID, Name: "测试门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	storeStaffUserID := int64(77)
	if err := db.Create(&models.StoreStaffBinding{TenantID: tenantID, UserID: storeStaffUserID, ActiveUserID: &storeStaffUserID, StoreID: store.ID, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create binding: %v", err)
	}
	first := &models.KnowledgeBase{TenantID: tenantID, StoreID: store.ID, DatasetID: "dataset-1", Name: "当前库", Status: enums.StatusOk}
	second := &models.KnowledgeBase{TenantID: tenantID, StoreID: store.ID, DatasetID: "dataset-2", Name: "备用库", Status: enums.StatusOk}
	if err := db.Create(first).Error; err != nil {
		t.Fatalf("create first knowledge base: %v", err)
	}
	if err := db.Create(second).Error; err != nil {
		t.Fatalf("create second knowledge base: %v", err)
	}
	if err := db.Create(&models.WxWorkProtocolInstance{TenantID: tenantID, Guid: "scope-instance", StoreID: store.ID, KnowledgeBaseID: 999999, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	scope := AgentTeamScopeService.Resolve(&dto.AuthPrincipal{UserID: 77, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeStoreStaff}})
	if !testContainsInt64(scope.KnowledgeBaseIDs, first.ID) || !testContainsInt64(scope.KnowledgeBaseIDs, second.ID) {
		t.Fatalf("store staff cannot see every store knowledge base: %#v", scope.KnowledgeBaseIDs)
	}
	if testContainsInt64(scope.KnowledgeBaseIDs, 999999) {
		t.Fatalf("legacy instance knowledge-base copy leaked into scope: %#v", scope.KnowledgeBaseIDs)
	}
}

func testContainsInt64(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
