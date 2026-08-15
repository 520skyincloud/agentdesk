package services

import (
	"strings"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/events"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/eventbus"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestConversationSupervisorTakeoverWithoutAgentProfileCanViewAndReply(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	conversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)

	if profile := AgentProfileService.GetByUserID(fixture.leaderA.UserID); profile != nil {
		t.Fatalf("team leader must not need an agent profile, got %+v", profile)
	}
	if err := ConversationService.AssignConversation(request.AssignConversationRequest{
		ConversationID: conversation.ID,
		AssigneeID:     fixture.leaderA.UserID,
		Reason:         "组长直接接管",
	}, fixture.leaderA); err != nil {
		t.Fatalf("team leader takeover error = %v", err)
	}

	current := ConversationService.GetByTenantID(conversation.ID, fixture.tenantID)
	if current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID != fixture.leaderA.UserID || current.CurrentTeamID != fixture.teamA.ID {
		t.Fatalf("unexpected conversation after takeover: %+v", current)
	}
	route := ConversationRouteService.GetByConversationIDInTenant(conversation.ID, fixture.tenantID)
	if route == nil || route.RouteStatus != enums.ConversationRouteStatusHQAgentDeskServing || !route.NeedHumanFollowUp {
		t.Fatalf("unexpected route after takeover: %+v", route)
	}
	assignment := repositories.ConversationAssignmentRepository.FindOne(fixture.db, sqls.NewCnd().
		Eq("tenant_id", fixture.tenantID).
		Eq("conversation_id", conversation.ID).
		Eq("status", enums.IMAssignmentStatusActive))
	if assignment == nil || assignment.ToUserID != fixture.leaderA.UserID || assignment.DispatchMode != enums.AgentTeamDispatchModeManual {
		t.Fatalf("unexpected active assignment: %+v", assignment)
	}
	if !AgentTeamScopeService.CanViewConversation(fixture.leaderA, conversation.ID) {
		t.Fatal("assigned team leader must retain conversation visibility")
	}
	list, _, err := ConversationService.ListConversations(fixture.leaderA, request.AgentConversationFilterMine, "", 0, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil || len(list) != 1 || list[0].ID != conversation.ID {
		t.Fatalf("assigned conversation list = %+v, err = %v", list, err)
	}
	if _, err := MessageService.ValidateConversationSender(conversation.ID, enums.IMSenderTypeAgent, fixture.leaderA, nil); err != nil {
		t.Fatalf("assigned team leader must be allowed to reply: %v", err)
	}
}

func TestConversationSupervisorTakeoverRejectsCrossTeamAndAgentWithoutProfile(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)

	crossTeam := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)
	if err := ConversationService.AssignConversation(request.AssignConversationRequest{
		ConversationID: crossTeam.ID,
		AssigneeID:     fixture.leaderB.UserID,
		Reason:         "跨组接管",
	}, fixture.leaderB); err == nil || !strings.Contains(err.Error(), "只能接管自己负责客服组") {
		t.Fatalf("expected cross-team takeover rejection, got %v", err)
	}

	ordinaryAgent := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)
	if err := ConversationService.AssignConversation(request.AssignConversationRequest{
		ConversationID: ordinaryAgent.ID,
		AssigneeID:     fixture.agentWithoutProfile.UserID,
		Reason:         "普通客服领取",
	}, fixture.agentWithoutProfile); err == nil || !strings.Contains(err.Error(), "目标客服不存在") {
		t.Fatalf("expected agent without profile rejection, got %v", err)
	}
}

func TestConversationTenantAdminTakeoverWithoutAgentProfile(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	conversation := fixture.createPendingConversation(t, fixture.teamB.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)

	if err := ConversationService.AssignConversation(request.AssignConversationRequest{
		ConversationID: conversation.ID,
		AssigneeID:     fixture.adminA.UserID,
		Reason:         "管理员接管",
	}, fixture.adminA); err != nil {
		t.Fatalf("tenant admin takeover error = %v", err)
	}
	current := ConversationService.GetByTenantID(conversation.ID, fixture.tenantID)
	if current == nil || current.CurrentAssigneeID != fixture.adminA.UserID || current.CurrentTeamID != fixture.teamB.ID || current.Status != enums.IMConversationStatusActive {
		t.Fatalf("unexpected admin takeover result: %+v", current)
	}
}

func TestConversationPlatformAdminTakeoverRequiresActiveTenant(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	conversation := fixture.createPendingConversation(t, fixture.teamB.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)

	withoutTenant := *fixture.platformAdmin
	withoutTenant.ActiveTenantID = 0
	if err := ConversationService.AssignConversation(request.AssignConversationRequest{
		ConversationID: conversation.ID,
		AssigneeID:     withoutTenant.UserID,
		Reason:         "平台管理员未切入租户",
	}, &withoutTenant); err == nil || !strings.Contains(err.Error(), "请先进入需要管理会话的接入公司") {
		t.Fatalf("expected active tenant rejection, got %v", err)
	}

	if err := ConversationService.AssignConversation(request.AssignConversationRequest{
		ConversationID: conversation.ID,
		AssigneeID:     fixture.platformAdmin.UserID,
		Reason:         "平台管理员接管",
	}, fixture.platformAdmin); err != nil {
		t.Fatalf("platform admin takeover error = %v", err)
	}
	current := ConversationService.GetByTenantID(conversation.ID, fixture.tenantID)
	if current == nil || current.CurrentAssigneeID != fixture.platformAdmin.UserID || current.CurrentTeamID != fixture.teamB.ID || current.Status != enums.IMConversationStatusActive {
		t.Fatalf("unexpected platform admin takeover result: %+v", current)
	}
}

func TestConversationPlatformAdminResolveStateAndDirectTakeover(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	conversation := fixture.createPendingConversation(t, fixture.teamB.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)

	state := ConversationTakeoverService.ResolveState(conversation, fixture.platformAdmin)
	if !state.CanDirectTakeover || state.CanRequest {
		t.Fatalf("platform admin did not receive direct takeover state: %+v", state)
	}
	if err := ConversationTakeoverService.DirectTakeover(request.RequestConversationTakeoverRequest{
		ConversationID: conversation.ID,
		Reason:         "超管直接接管",
	}, fixture.platformAdmin); err != nil {
		t.Fatalf("platform admin direct takeover: %v", err)
	}

	current := ConversationService.GetByTenantID(conversation.ID, fixture.tenantID)
	if current == nil || current.CurrentAssigneeID != fixture.platformAdmin.UserID || current.Status != enums.IMConversationStatusActive {
		t.Fatalf("unexpected platform admin direct takeover result: %+v", current)
	}
	if _, err := MessageService.ValidateConversationSender(conversation.ID, enums.IMSenderTypeAgent, fixture.platformAdmin, nil); err != nil {
		t.Fatalf("platform admin cannot reply after direct takeover: %v", err)
	}
}

func TestConversationPlatformAdminCanDirectTakeoverAIConversation(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	for _, routeStatus := range []enums.ConversationRouteStatus{
		enums.ConversationRouteStatusAIServing,
		enums.ConversationRouteStatusAIFallback,
	} {
		t.Run(string(routeStatus), func(t *testing.T) {
			conversation := fixture.createPendingConversation(t, fixture.teamB.ID, routeStatus, false)

			state := ConversationTakeoverService.ResolveState(conversation, fixture.platformAdmin)
			if !state.CanDirectTakeover || state.CanRequest {
				t.Fatalf("platform admin did not receive AI direct takeover state: %+v", state)
			}
			if err := ConversationTakeoverService.DirectTakeover(request.RequestConversationTakeoverRequest{
				ConversationID: conversation.ID,
				Reason:         "超管从AI接管",
			}, fixture.platformAdmin); err != nil {
				t.Fatalf("platform admin direct takeover from %s: %v", routeStatus, err)
			}

			current := ConversationService.GetByTenantID(conversation.ID, fixture.tenantID)
			if current == nil || current.CurrentAssigneeID != fixture.platformAdmin.UserID || current.Status != enums.IMConversationStatusActive {
				t.Fatalf("unexpected conversation after AI direct takeover: %+v", current)
			}
			route := ConversationRouteService.GetByConversationIDInTenant(conversation.ID, fixture.tenantID)
			if route == nil || route.RouteStatus != enums.ConversationRouteStatusHQAgentDeskServing {
				t.Fatalf("unexpected route after AI direct takeover: %+v", route)
			}
		})
	}
}

func TestConversationPlatformAdminDirectTakeoverRejectsUnsupportedRoute(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	for _, tt := range []struct {
		name         string
		routeStatus  enums.ConversationRouteStatus
		needFollowUp bool
	}{
		{name: "store manual", routeStatus: enums.ConversationRouteStatusStoreWecomManual, needFollowUp: true},
		{name: "cleared human pool", routeStatus: enums.ConversationRouteStatusHQAgentDeskPending, needFollowUp: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			conversation := fixture.createPendingConversation(t, fixture.teamA.ID, tt.routeStatus, tt.needFollowUp)
			state := ConversationTakeoverService.ResolveState(conversation, fixture.platformAdmin)
			if state.CanDirectTakeover {
				t.Fatalf("unsupported route exposed direct takeover: %+v", state)
			}
			if err := ConversationTakeoverService.DirectTakeover(request.RequestConversationTakeoverRequest{
				ConversationID: conversation.ID,
				Reason:         "错误状态直接接管",
			}, fixture.platformAdmin); err == nil || !strings.Contains(err.Error(), "状态不允许") {
				t.Fatalf("unsupported route unexpectedly accepted direct takeover: %v", err)
			}
		})
	}
}

func TestConversationSupervisorTakeoverAllowsAIAndRejectsUnsupportedRoutes(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	aiConversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusAIServing, false)
	if err := ConversationService.AssignConversation(request.AssignConversationRequest{
		ConversationID: aiConversation.ID,
		AssigneeID:     fixture.leaderA.UserID,
		Reason:         "组长从AI接管",
	}, fixture.leaderA); err != nil {
		t.Fatalf("team leader cannot take over AI conversation: %v", err)
	}
	current := ConversationService.GetByTenantID(aiConversation.ID, fixture.tenantID)
	if current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID != fixture.leaderA.UserID {
		t.Fatalf("unexpected AI conversation after team leader takeover: %+v", current)
	}

	tests := []struct {
		name         string
		routeStatus  enums.ConversationRouteStatus
		needFollowUp bool
	}{
		{name: "follow up cleared", routeStatus: enums.ConversationRouteStatusHQAgentDeskPending, needFollowUp: false},
		{name: "store manual", routeStatus: enums.ConversationRouteStatusStoreWecomManual, needFollowUp: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conversation := fixture.createPendingConversation(t, fixture.teamA.ID, tt.routeStatus, tt.needFollowUp)
			err := ConversationService.AssignConversation(request.AssignConversationRequest{
				ConversationID: conversation.ID,
				AssigneeID:     fixture.leaderA.UserID,
				Reason:         "错误路由接管",
			}, fixture.leaderA)
			if err == nil || !strings.Contains(err.Error(), "状态不允许") {
				t.Fatalf("expected route rejection, got %v", err)
			}
		})
	}
}

func TestConversationSupervisorTakeoverConcurrentSingleWinner(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	conversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)

	operators := []*dto.AuthPrincipal{fixture.adminA, fixture.adminB}
	start := make(chan struct{})
	errCh := make(chan error, len(operators))
	var wg sync.WaitGroup
	for _, operator := range operators {
		operator := operator
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errCh <- ConversationService.AssignConversation(request.AssignConversationRequest{
				ConversationID: conversation.ID,
				AssigneeID:     operator.UserID,
				Reason:         "并发接管",
			}, operator)
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)

	successes := 0
	for err := range errCh {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent takeover successes = %d, want 1", successes)
	}
	current := ConversationService.GetByTenantID(conversation.ID, fixture.tenantID)
	if current == nil || current.Status != enums.IMConversationStatusActive || (current.CurrentAssigneeID != fixture.adminA.UserID && current.CurrentAssigneeID != fixture.adminB.UserID) {
		t.Fatalf("unexpected concurrent takeover result: %+v", current)
	}
	var activeAssignments int64
	if err := fixture.db.Model(&models.ConversationAssignment{}).
		Where("tenant_id = ? AND conversation_id = ? AND status = ?", fixture.tenantID, conversation.ID, enums.IMAssignmentStatusActive).
		Count(&activeAssignments).Error; err != nil {
		t.Fatalf("count active assignments: %v", err)
	}
	if activeAssignments != 1 {
		t.Fatalf("active assignments = %d, want 1", activeAssignments)
	}
	var totalAssignments int64
	if err := fixture.db.Model(&models.ConversationAssignment{}).
		Where("tenant_id = ? AND conversation_id = ?", fixture.tenantID, conversation.ID).
		Count(&totalAssignments).Error; err != nil {
		t.Fatalf("count all assignments: %v", err)
	}
	if totalAssignments != 1 {
		t.Fatalf("total assignments = %d, want 1", totalAssignments)
	}
}

func TestConversationManualAssignmentIgnoresPresence(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	targets := []struct {
		name   string
		userID int64
	}{
		{name: "offline", userID: fixture.offlineAgent.ID},
		{name: "break", userID: fixture.breakAgent.ID},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			conversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)
			if err := ConversationService.AssignConversation(request.AssignConversationRequest{
				ConversationID: conversation.ID,
				AssigneeID:     target.userID,
				Reason:         "主管人工指定",
			}, fixture.adminA); err != nil {
				t.Fatalf("manual assignment to %s agent error = %v", target.name, err)
			}
			current := ConversationService.GetByTenantID(conversation.ID, fixture.tenantID)
			if current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID != target.userID || current.CurrentTeamID != fixture.teamA.ID {
				t.Fatalf("unexpected manual assignment result: %+v", current)
			}
		})
	}
}

type conversationSupervisorTakeoverFixture struct {
	db                  *gorm.DB
	tenantID            int64
	teamA               models.AgentTeam
	teamB               models.AgentTeam
	leaderA             *dto.AuthPrincipal
	leaderB             *dto.AuthPrincipal
	adminA              *dto.AuthPrincipal
	adminB              *dto.AuthPrincipal
	platformAdmin       *dto.AuthPrincipal
	agentWithoutProfile *dto.AuthPrincipal
	offlineAgent        models.User
	breakAgent          models.User
}

func setupConversationSupervisorTakeoverFixture(t *testing.T) conversationSupervisorTakeoverFixture {
	t.Helper()
	const tenantID int64 = 101
	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared&_pragma=busy_timeout(5000)"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		eventbus.WaitAsync[events.ConversationAssignedEvent]()
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(
		&models.User{},
		&models.Role{},
		&models.Permission{},
		&models.UserRole{},
		&models.RolePermission{},
		&models.AgentTeam{},
		&models.AgentProfile{},
		&models.AgentPresenceSession{},
		&models.StoreStaffBinding{},
		&models.Channel{},
		&models.WxWorkProtocolInstance{},
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.WxWorkKFConversation{},
		&models.ConversationTakeoverRequest{},
		&models.ConversationAssignment{},
		&models.ConversationEventLog{},
		&models.ConversationReadState{},
		&models.Message{},
		&models.ChannelMessageOutbox{},
		&models.Notification{},
	); err != nil {
		t.Fatalf("migrate supervisor takeover models: %v", err)
	}
	sqls.SetDB(db)

	users := []models.User{
		{ID: 11, TenantID: tenantID, Username: "leader-a", Status: enums.StatusOk},
		{ID: 12, TenantID: tenantID, Username: "leader-b", Status: enums.StatusOk},
		{ID: 21, TenantID: tenantID, Username: "tenant-admin-a", Status: enums.StatusOk},
		{ID: 22, TenantID: tenantID, Username: "tenant-admin-b", Status: enums.StatusOk},
		{ID: 31, TenantID: tenantID, Username: "agent-without-profile", Status: enums.StatusOk},
		{ID: 41, TenantID: tenantID, Username: "offline-agent", Status: enums.StatusOk},
		{ID: 42, TenantID: tenantID, Username: "break-agent", Status: enums.StatusOk},
		{ID: 23, TenantID: 0, Username: "platform-admin", Status: enums.StatusOk},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create takeover users: %v", err)
	}
	teamA := models.AgentTeam{ID: 1, TenantID: tenantID, Name: "A客服组", LeaderUserID: 11, IsDefault: true, Status: enums.StatusOk}
	teamB := models.AgentTeam{ID: 2, TenantID: tenantID, Name: "B客服组", LeaderUserID: 12, Status: enums.StatusOk}
	if err := db.Create(&[]models.AgentTeam{teamA, teamB}).Error; err != nil {
		t.Fatalf("create takeover teams: %v", err)
	}

	agentRole := models.Role{ID: 1, Name: "客服", Code: constants.RoleCodeCsUser, Status: enums.StatusOk}
	permissions := []models.Permission{
		{ID: 1, Name: "查看会话", Code: constants.PermissionConversationView.Code, Status: enums.StatusOk},
		{ID: 2, Name: "发送会话消息", Code: constants.PermissionConversationSend.Code, Status: enums.StatusOk},
	}
	if err := db.Create(&agentRole).Error; err != nil {
		t.Fatalf("create agent role: %v", err)
	}
	if err := db.Create(&permissions).Error; err != nil {
		t.Fatalf("create conversation permissions: %v", err)
	}
	for _, permission := range permissions {
		if err := db.Create(&models.RolePermission{RoleID: agentRole.ID, PermissionID: permission.ID}).Error; err != nil {
			t.Fatalf("bind agent permission: %v", err)
		}
	}
	for _, userID := range []int64{41, 42} {
		if err := db.Create(&models.UserRole{UserID: userID, RoleID: agentRole.ID}).Error; err != nil {
			t.Fatalf("bind agent role: %v", err)
		}
	}
	profiles := []models.AgentProfile{
		{ID: 1, TenantID: tenantID, UserID: 41, TeamID: teamA.ID, AgentCode: "offline-agent", DisplayName: "离线客服", Status: enums.StatusOk, AutoAssignEnabled: true, MaxConcurrentCount: 5},
		{ID: 2, TenantID: tenantID, UserID: 42, TeamID: teamA.ID, AgentCode: "break-agent", DisplayName: "休息客服", Status: enums.StatusOk, AutoAssignEnabled: true, MaxConcurrentCount: 5},
	}
	if err := db.Create(&profiles).Error; err != nil {
		t.Fatalf("create takeover profiles: %v", err)
	}
	now := time.Now()
	if err := db.Create(&models.AgentPresenceSession{
		TenantID: tenantID, UserID: 42, AgentProfileID: 2, TeamID: teamA.ID,
		Status: enums.AgentPresenceStatusBreak, Source: "test", StartedAt: now, LastSeenAt: now,
	}).Error; err != nil {
		t.Fatalf("create break presence: %v", err)
	}

	permissionsForTakeover := []string{
		constants.PermissionConversationView.Code,
		constants.PermissionConversationSend.Code,
		constants.PermissionConversationAssign.Code,
	}
	principal := func(userID int64, username, role string) *dto.AuthPrincipal {
		return &dto.AuthPrincipal{
			UserID: userID, Username: username, ActiveTenantID: tenantID,
			Roles: []string{role}, Permissions: append([]string(nil), permissionsForTakeover...),
		}
	}
	platformAdmin := principal(23, "platform-admin", constants.RoleCodeSuperAdmin)
	platformAdmin.IsPlatformAccount = true
	return conversationSupervisorTakeoverFixture{
		db: db, tenantID: tenantID, teamA: teamA, teamB: teamB,
		leaderA:             principal(11, "leader-a", constants.RoleCodeCsTeamLeader),
		leaderB:             principal(12, "leader-b", constants.RoleCodeCsTeamLeader),
		adminA:              principal(21, "tenant-admin-a", constants.RoleCodeTenantAdmin),
		adminB:              principal(22, "tenant-admin-b", constants.RoleCodeTenantAdmin),
		platformAdmin:       platformAdmin,
		agentWithoutProfile: principal(31, "agent-without-profile", constants.RoleCodeCsUser),
		offlineAgent:        users[5], breakAgent: users[6],
	}
}

func (f conversationSupervisorTakeoverFixture) createPendingConversation(t *testing.T, teamID int64, routeStatus enums.ConversationRouteStatus, needFollowUp bool) *models.Conversation {
	t.Helper()
	now := time.Now()
	conversation := &models.Conversation{
		TenantID: f.tenantID, CustomerName: "待人工客户", Status: enums.IMConversationStatusPending,
		ServiceMode: enums.IMConversationServiceModeAIFirst, CurrentTeamID: teamID,
		DispatchWeight: 1, LastActiveAt: now, LastMessageAt: now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := f.db.Create(conversation).Error; err != nil {
		t.Fatalf("create pending conversation: %v", err)
	}
	route := &models.ConversationRouteState{
		TenantID: f.tenantID, ConversationID: conversation.ID, RouteStatus: routeStatus,
		RouteTarget: "agentdesk_hq", SessionNo: 1, NeedHumanFollowUp: needFollowUp,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := f.db.Create(route).Error; err != nil {
		t.Fatalf("create pending route: %v", err)
	}
	return conversation
}
