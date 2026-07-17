package services

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
)

func TestConversationDispatchWorkbenchScopesTasksStatsAndLoadsByManagedTeam(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)

	teamA := models.AgentTeam{ID: 11, TenantID: 101, Name: "A客服组", LeaderUserID: 901, Status: enums.StatusOk}
	teamB := models.AgentTeam{ID: 12, TenantID: 101, Name: "B客服组", LeaderUserID: 902, Status: enums.StatusOk}
	if err := db.Create(&[]models.AgentTeam{teamA, teamB}).Error; err != nil {
		t.Fatalf("create teams: %v", err)
	}
	users := []models.User{
		{ID: 111, TenantID: 101, Username: "agent-a", Status: enums.StatusOk},
		{ID: 112, TenantID: 101, Username: "agent-b", Status: enums.StatusOk},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create users: %v", err)
	}
	profiles := []models.AgentProfile{
		{ID: 211, TenantID: 101, UserID: 111, TeamID: teamA.ID, AgentCode: "scope-a", Status: enums.StatusOk, ServiceStatus: enums.ServiceStatusIdle, AutoAssignEnabled: true, MaxConcurrentCount: 10},
		{ID: 212, TenantID: 101, UserID: 112, TeamID: teamB.ID, AgentCode: "scope-b", Status: enums.StatusOk, ServiceStatus: enums.ServiceStatusIdle, AutoAssignEnabled: true, MaxConcurrentCount: 10},
	}
	if err := db.Create(&profiles).Error; err != nil {
		t.Fatalf("create profiles: %v", err)
	}
	conversations := []models.Conversation{
		{ID: 311, TenantID: 101, CustomerName: "A组客户", Status: enums.IMConversationStatusActive, CurrentTeamID: teamA.ID, CurrentAssigneeID: users[0].ID},
		{ID: 312, TenantID: 101, CustomerName: "B组客户", Status: enums.IMConversationStatusActive, CurrentTeamID: teamB.ID, CurrentAssigneeID: users[1].ID},
		{ID: 313, TenantID: 101, CustomerName: "未归属客户", Status: enums.IMConversationStatusPending},
	}
	if err := db.Create(&conversations).Error; err != nil {
		t.Fatalf("create conversations: %v", err)
	}

	tenantAdmin := &dto.AuthPrincipal{UserID: 800, ActiveTenantID: 101, Roles: []string{constants.RoleCodeTenantAdmin}}
	leaderA := &dto.AuthPrincipal{UserID: teamA.LeaderUserID, ActiveTenantID: 101, Roles: []string{constants.RoleCodeCsTeamLeader}}
	agentA := &dto.AuthPrincipal{UserID: users[0].ID, ActiveTenantID: 101, Roles: []string{constants.RoleCodeCsUser}}

	adminTasks, adminPaging, err := ConversationDispatchWorkbenchService.ListTasks(request.ConversationDispatchListRequest{}, tenantAdmin, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil || adminPaging.Total != 3 || len(adminTasks) != 3 {
		t.Fatalf("tenant admin tasks=%+v paging=%+v err=%v", adminTasks, adminPaging, err)
	}
	leaderTasks, leaderPaging, err := ConversationDispatchWorkbenchService.ListTasks(request.ConversationDispatchListRequest{}, leaderA, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil || leaderPaging.Total != 1 || len(leaderTasks) != 1 || leaderTasks[0].TeamID != teamA.ID {
		t.Fatalf("leader tasks=%+v paging=%+v err=%v", leaderTasks, leaderPaging, err)
	}
	foreignTasks, foreignPaging, err := ConversationDispatchWorkbenchService.ListTasks(request.ConversationDispatchListRequest{TeamID: teamB.ID}, leaderA, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil || foreignPaging.Total != 0 || len(foreignTasks) != 0 {
		t.Fatalf("leader foreign tasks=%+v paging=%+v err=%v", foreignTasks, foreignPaging, err)
	}
	agentTasks, agentPaging, err := ConversationDispatchWorkbenchService.ListTasks(request.ConversationDispatchListRequest{}, agentA, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil || agentPaging.Total != 0 || len(agentTasks) != 0 {
		t.Fatalf("agent direct service tasks=%+v paging=%+v err=%v", agentTasks, agentPaging, err)
	}

	stats, err := ConversationDispatchWorkbenchService.Stats(request.ConversationDispatchListRequest{}, leaderA)
	if err != nil || stats.Total != 1 {
		t.Fatalf("leader stats=%+v err=%v", stats, err)
	}
	loads, err := ConversationDispatchWorkbenchService.ListAgentLoads(0, leaderA)
	if err != nil || len(loads) != 1 || loads[0].TeamID != teamA.ID {
		t.Fatalf("leader loads=%+v err=%v", loads, err)
	}
	foreignLoads, err := ConversationDispatchWorkbenchService.ListAgentLoads(teamB.ID, leaderA)
	if err != nil || len(foreignLoads) != 0 {
		t.Fatalf("leader foreign loads=%+v err=%v", foreignLoads, err)
	}
	adminLoads, err := ConversationDispatchWorkbenchService.ListAgentLoads(0, tenantAdmin)
	if err != nil || len(adminLoads) != 2 {
		t.Fatalf("tenant admin loads=%+v err=%v", adminLoads, err)
	}
}
