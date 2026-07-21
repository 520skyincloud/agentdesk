package services

import (
	"testing"
	"time"

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
		{ID: 211, TenantID: 101, UserID: 111, TeamID: teamA.ID, AgentCode: "scope-a", Status: enums.StatusOk, AutoAssignEnabled: true, MaxConcurrentCount: 10},
		{ID: 212, TenantID: 101, UserID: 112, TeamID: teamB.ID, AgentCode: "scope-b", Status: enums.StatusOk, AutoAssignEnabled: true, MaxConcurrentCount: 10},
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

func TestConversationDispatchWorkbenchUsesQueueAndFirstResponseSLA(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	if err := db.Create(&models.ServiceAnalyticsPolicy{
		TenantID: 101, QueueTargetSeconds: 60, FirstResponseTargetSeconds: 120,
	}).Error; err != nil {
		t.Fatalf("create analytics policy: %v", err)
	}
	now := time.Now()
	handoffAt := now.Add(-50 * time.Second)
	manualExpiredAt := now.Add(-time.Hour)
	pending := &models.Conversation{TenantID: 101, Status: enums.IMConversationStatusPending, HandoffAt: &handoffAt}
	route := &models.ConversationRouteState{TenantID: 101, ManualExpireAt: &manualExpiredAt}
	sla := ConversationDispatchWorkbenchService.resolveTaskSLA(pending, route, nil, nil, now)
	status, _ := ConversationDispatchWorkbenchService.resolveTaskStatus(pending, nil, nil, sla)
	if sla.slaType != dispatchSLATypeQueue || sla.status != dispatchSLAStatusWarning || status != ConversationDispatchStatusWarning {
		t.Fatalf("queue sla=%+v status=%s", sla, status)
	}
	if sla.deadlineAt == nil || sla.deadlineAt.Before(now.Add(9*time.Second)) || sla.deadlineAt.After(now.Add(11*time.Second)) {
		t.Fatalf("queue deadline=%v", sla.deadlineAt)
	}

	assignedAt := now.Add(-30 * time.Second)
	active := &models.Conversation{TenantID: 101, Status: enums.IMConversationStatusActive, CurrentAssigneeID: 10}
	sla = ConversationDispatchWorkbenchService.resolveTaskSLA(active, route, &assignedAt, nil, now)
	status, _ = ConversationDispatchWorkbenchService.resolveTaskStatus(active, &assignedAt, nil, sla)
	if sla.slaType != dispatchSLATypeFirstResponse || sla.status != dispatchSLAStatusNormal || status != ConversationDispatchStatusAssigned {
		t.Fatalf("first response sla=%+v status=%s", sla, status)
	}
	firstReplyAt := now.Add(-time.Second)
	sla = ConversationDispatchWorkbenchService.resolveTaskSLA(active, route, &assignedAt, &firstReplyAt, now)
	status, _ = ConversationDispatchWorkbenchService.resolveTaskStatus(active, &assignedAt, &firstReplyAt, sla)
	if sla.slaType != "" || status != ConversationDispatchStatusProcessing {
		t.Fatalf("processing sla=%+v status=%s", sla, status)
	}
}

func TestConversationDispatchWorkbenchKeepsOnlyRecentClosedOperationalHistory(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	now := time.Now()
	recentClosedAt := now.Add(-time.Hour)
	oldClosedAt := now.Add(-48 * time.Hour)
	conversations := []models.Conversation{
		{ID: 801, TenantID: 101, Status: enums.IMConversationStatusPending, LastActiveAt: now, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		{ID: 802, TenantID: 101, Status: enums.IMConversationStatusClosed, ClosedAt: &recentClosedAt, LastActiveAt: recentClosedAt, AuditFields: models.AuditFields{CreatedAt: recentClosedAt, UpdatedAt: recentClosedAt}},
		{ID: 803, TenantID: 101, Status: enums.IMConversationStatusClosed, ClosedAt: &oldClosedAt, LastActiveAt: oldClosedAt, AuditFields: models.AuditFields{CreatedAt: oldClosedAt, UpdatedAt: oldClosedAt}},
	}
	if err := db.Create(&conversations).Error; err != nil {
		t.Fatalf("create operational history conversations: %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: 800, ActiveTenantID: 101, Roles: []string{constants.RoleCodeTenantAdmin}}
	items, paging, err := ConversationDispatchWorkbenchService.ListTasks(request.ConversationDispatchListRequest{}, operator, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil || paging.Total != 2 || len(items) != 2 {
		t.Fatalf("operational tasks=%+v paging=%+v err=%v", items, paging, err)
	}
	closed, closedPaging, err := ConversationDispatchWorkbenchService.ListTasks(request.ConversationDispatchListRequest{Status: ConversationDispatchStatusClosed}, operator, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil || closedPaging.Total != 1 || len(closed) != 1 || closed[0].ConversationID != 802 {
		t.Fatalf("recent closed tasks=%+v paging=%+v err=%v", closed, closedPaging, err)
	}
}

func TestConversationDispatchWorkbenchBatchLoadsFirstAgentReply(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	now := time.Now()
	team := models.AgentTeam{ID: 91, TenantID: 101, Name: "首响测试组", Status: enums.StatusOk}
	user := models.User{ID: 911, TenantID: 101, Username: "first-reply-agent", Nickname: "首响客服", Status: enums.StatusOk}
	conversation := models.Conversation{ID: 912, TenantID: 101, Status: enums.IMConversationStatusActive, CurrentTeamID: team.ID, CurrentAssigneeID: user.ID, LastActiveAt: now}
	if err := db.Create(&team).Error; err != nil {
		t.Fatalf("create first reply team: %v", err)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create first reply user: %v", err)
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create first reply conversation: %v", err)
	}
	assignedAt := now.Add(-2 * time.Minute)
	if err := db.Create(&models.ConversationAssignment{
		TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, ToUserID: user.ID,
		Status: enums.IMAssignmentStatusActive, CreatedAt: assignedAt,
	}).Error; err != nil {
		t.Fatalf("create first reply assignment: %v", err)
	}
	sentAt := now.Add(-50 * time.Second)
	if err := db.Create(&models.Message{
		TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, ClientMsgID: "first-reply-batch",
		SenderType: enums.IMSenderTypeAgent, SenderID: user.ID, SeqNo: 1, SentAt: &sentAt,
		AuditFields: models.AuditFields{CreatedAt: now.Add(-time.Minute)},
	}).Error; err != nil {
		t.Fatalf("create first reply message: %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: 800, ActiveTenantID: 101, Roles: []string{constants.RoleCodeTenantAdmin}}
	items, _, err := ConversationDispatchWorkbenchService.ListTasks(request.ConversationDispatchListRequest{}, operator, &sqls.Paging{Page: 1, Limit: 20})
	if err != nil || len(items) != 1 {
		t.Fatalf("first reply tasks=%+v err=%v", items, err)
	}
	if items[0].Status != ConversationDispatchStatusProcessing || items[0].FirstAgentReplyAt == "" || items[0].CurrentAssigneeName != user.Nickname {
		t.Fatalf("batch first reply response=%+v", items[0])
	}
}
