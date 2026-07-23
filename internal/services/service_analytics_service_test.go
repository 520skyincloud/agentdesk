package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
)

func TestServiceAnalyticsTenantTeamAndSelfScopes(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	t0 := time.Date(2026, 7, 17, 7, 0, 0, 0, time.Local)
	tenantA := int64(501)
	tenantB := int64(502)

	teamA := &models.AgentTeam{TenantID: tenantA, Name: "一组", LeaderUserID: 11, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	teamB := &models.AgentTeam{TenantID: tenantA, Name: "二组", LeaderUserID: 22, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	foreignTeam := &models.AgentTeam{TenantID: tenantB, Name: "其他公司组", LeaderUserID: 11, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	for _, team := range []*models.AgentTeam{teamA, teamB, foreignTeam} {
		if err := db.Create(team).Error; err != nil {
			t.Fatalf("create team: %v", err)
		}
	}
	agentA := &models.User{TenantID: tenantA, Username: "scope-agent-a", Nickname: "客服甲", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	agentB := &models.User{TenantID: tenantA, Username: "scope-agent-b", Nickname: "客服乙", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	foreignAgent := &models.User{TenantID: tenantB, Username: "scope-agent-foreign", Nickname: "外部客服", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	for _, user := range []*models.User{agentA, agentB, foreignAgent} {
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	profiles := []*models.AgentProfile{
		{TenantID: tenantA, UserID: agentA.ID, TeamID: teamA.ID, AgentCode: "S-A", DisplayName: "客服甲", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)},
		{TenantID: tenantA, UserID: agentB.ID, TeamID: teamB.ID, AgentCode: "S-B", DisplayName: "客服乙", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)},
		{TenantID: tenantB, UserID: foreignAgent.ID, TeamID: foreignTeam.ID, AgentCode: "S-F", DisplayName: "外部客服", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)},
	}
	for _, profile := range profiles {
		if err := db.Create(profile).Error; err != nil {
			t.Fatalf("create profile: %v", err)
		}
	}
	createScopedAnalyticsSession(t, db, tenantA, 1001, 2001, teamA.ID, agentA.ID, t0)
	createScopedAnalyticsSession(t, db, tenantA, 1002, 2002, teamB.ID, agentB.ID, t0.Add(time.Minute))
	createScopedAnalyticsSession(t, db, tenantB, 1003, 2003, foreignTeam.ID, foreignAgent.ID, t0.Add(2*time.Minute))

	query := ServiceAnalyticsQuery{StartAt: t0.Add(-time.Minute), EndAt: t0.Add(time.Hour)}
	admin := &dto.AuthPrincipal{UserID: 1, ActiveTenantID: tenantA, Roles: []string{constants.RoleCodeTenantAdmin}}
	adminOverview, err := ServiceAnalyticsService.GetOverview(query, admin)
	if err != nil {
		t.Fatalf("admin overview: %v", err)
	}
	if adminOverview.Summary.SessionCount != 2 {
		t.Fatalf("tenant admin sessions=%d want 2", adminOverview.Summary.SessionCount)
	}
	adminDimensions, err := ServiceAnalyticsService.GetDimensions(admin)
	if err != nil {
		t.Fatalf("admin dimensions: %v", err)
	}
	if len(adminDimensions.Teams) != 2 || len(adminDimensions.Agents) != 2 {
		t.Fatalf("tenant dimensions teams=%+v agents=%+v", adminDimensions.Teams, adminDimensions.Agents)
	}

	leader := &dto.AuthPrincipal{UserID: teamA.LeaderUserID, ActiveTenantID: tenantA, Roles: []string{constants.RoleCodeCsTeamLeader}}
	leaderOverview, err := ServiceAnalyticsService.GetOverview(query, leader)
	if err != nil {
		t.Fatalf("leader overview: %v", err)
	}
	if leaderOverview.Summary.SessionCount != 1 {
		t.Fatalf("team leader sessions=%d want 1", leaderOverview.Summary.SessionCount)
	}
	leaderDimensions, err := ServiceAnalyticsService.GetDimensions(leader)
	if err != nil {
		t.Fatalf("leader dimensions: %v", err)
	}
	if len(leaderDimensions.Teams) != 1 || leaderDimensions.Teams[0].ID != teamA.ID || len(leaderDimensions.Agents) != 1 || leaderDimensions.Agents[0].ID != agentA.ID {
		t.Fatalf("leader dimensions=%+v", leaderDimensions)
	}

	agent := &dto.AuthPrincipal{UserID: agentA.ID, ActiveTenantID: tenantA, Roles: []string{constants.RoleCodeCsUser}}
	agentOverview, err := ServiceAnalyticsService.GetOverview(query, agent)
	if err != nil {
		t.Fatalf("agent overview: %v", err)
	}
	if agentOverview.Summary.SessionCount != 1 {
		t.Fatalf("agent sessions=%d want 1", agentOverview.Summary.SessionCount)
	}
	agentDimensions, err := ServiceAnalyticsService.GetDimensions(agent)
	if err != nil {
		t.Fatalf("agent dimensions: %v", err)
	}
	if len(agentDimensions.Agents) != 1 || agentDimensions.Agents[0].ID != agentA.ID {
		t.Fatalf("agent dimensions=%+v", agentDimensions)
	}
}

func TestServiceAnalyticsDispatchEvidenceRespectsRoleScope(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	now := time.Date(2026, 7, 17, 11, 0, 0, 0, time.Local)
	tenantID := int64(503)
	teamA := &models.AgentTeam{TenantID: tenantID, Name: "派单A组", LeaderUserID: 31, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	teamB := &models.AgentTeam{TenantID: tenantID, Name: "派单B组", LeaderUserID: 32, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	if err := db.Create(teamA).Error; err != nil {
		t.Fatalf("create team A: %v", err)
	}
	if err := db.Create(teamB).Error; err != nil {
		t.Fatalf("create team B: %v", err)
	}
	agent := &models.User{TenantID: tenantID, Username: "dispatch-scope-agent", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := db.Create(&models.AgentProfile{TenantID: tenantID, UserID: agent.ID, TeamID: teamA.ID, AgentCode: "DISPATCH-SCOPE", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}).Error; err != nil {
		t.Fatalf("create profile: %v", err)
	}
	logs := []models.DispatchDecisionLog{
		{TenantID: tenantID, DecisionKey: "scope-unattributed", Status: enums.DispatchDecisionStatusFailed, DecisionMode: "rule", DecidedAt: now, AuditFields: testAnalyticsAudit(now)},
		{TenantID: tenantID, DecisionKey: "scope-team-a-failure", SelectedTeamID: teamA.ID, Status: enums.DispatchDecisionStatusFailed, DecisionMode: "rule", DecidedAt: now, AuditFields: testAnalyticsAudit(now)},
		{TenantID: tenantID, DecisionKey: "scope-team-b-failure", SelectedTeamID: teamB.ID, Status: enums.DispatchDecisionStatusFailed, DecisionMode: "rule", DecidedAt: now, AuditFields: testAnalyticsAudit(now)},
		{TenantID: tenantID, DecisionKey: "scope-agent-a-selected", SelectedTeamID: teamA.ID, SelectedUserID: agent.ID, AssignmentID: 9001, Status: enums.DispatchDecisionStatusSelected, DecisionMode: "rule", DecidedAt: now, AuditFields: testAnalyticsAudit(now)},
	}
	for i := range logs {
		if err := db.Create(&logs[i]).Error; err != nil {
			t.Fatalf("create decision log: %v", err)
		}
	}
	query := ServiceAnalyticsQuery{StartAt: now.Add(-time.Minute), EndAt: now.Add(time.Minute)}
	assertDecisionCount := func(name string, operator *dto.AuthPrincipal, want int64) {
		t.Helper()
		overview, err := ServiceAnalyticsService.GetOverview(query, operator)
		if err != nil {
			t.Fatalf("%s overview: %v", name, err)
		}
		if overview.Dispatch.DecisionCount != want {
			t.Fatalf("%s decision count=%d want %d", name, overview.Dispatch.DecisionCount, want)
		}
	}
	assertDecisionCount("tenant admin", &dto.AuthPrincipal{UserID: 1, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}, 4)
	assertDecisionCount("team leader", &dto.AuthPrincipal{UserID: teamA.LeaderUserID, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeCsTeamLeader}}, 2)
	assertDecisionCount("agent", &dto.AuthPrincipal{UserID: agent.ID, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeCsUser}}, 1)
}

func TestServiceAnalyticsUsesAssignmentSquadSnapshot(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	t0 := time.Date(2026, 7, 16, 9, 0, 0, 0, time.Local)
	tenantID := int64(601)
	oldTeam := &models.AgentTeam{TenantID: tenantID, Name: "原客服组", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	currentTeam := &models.AgentTeam{TenantID: tenantID, Name: "现客服组", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	for _, team := range []*models.AgentTeam{oldTeam, currentTeam} {
		if err := db.Create(team).Error; err != nil {
			t.Fatalf("create team: %v", err)
		}
	}
	oldSquad := &models.AgentTeamSquad{TenantID: tenantID, TeamID: oldTeam.ID, Name: "历史白班", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	currentSquad := &models.AgentTeamSquad{TenantID: tenantID, TeamID: currentTeam.ID, Name: "当前晚班", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	for _, squad := range []*models.AgentTeamSquad{oldSquad, currentSquad} {
		if err := db.Create(squad).Error; err != nil {
			t.Fatalf("create squad: %v", err)
		}
	}
	agent := &models.User{TenantID: tenantID, Username: "moved-agent", Nickname: "已换组客服", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}
	profile := &models.AgentProfile{TenantID: tenantID, UserID: agent.ID, TeamID: currentTeam.ID, AgentCode: "MOVED", DisplayName: agent.Nickname, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if err := db.Create(&models.AgentTeamSquadMember{TenantID: tenantID, SquadID: currentSquad.ID, AgentProfileID: profile.ID, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}).Error; err != nil {
		t.Fatalf("create current squad membership: %v", err)
	}
	conversation := &models.Conversation{
		ID: 6101, TenantID: tenantID, CustomerName: "历史质检客户", Status: enums.IMConversationStatusClosed,
		LastMessageAt: t0, LastActiveAt: t0, AuditFields: testAnalyticsAudit(t0),
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create historical conversation: %v", err)
	}

	finishedAt := t0.Add(30 * time.Minute)
	assignment := &models.ConversationAssignment{
		TenantID: tenantID, ConversationID: 6101, SessionNo: 1, SquadID: oldSquad.ID, ToUserID: agent.ID,
		AssignType: string(enums.IMAssignmentTypeAssign), Status: enums.IMAssignmentStatusInactive, CreatedAt: t0, FinishedAt: &finishedAt,
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatalf("create historical assignment: %v", err)
	}
	repliedAt := t0.Add(3 * time.Minute)
	endedAt := t0.Add(20 * time.Minute)
	session := &models.ConversationServiceSession{
		TenantID: tenantID, ConversationID: assignment.ConversationID, SessionNo: 1, CustomerID: 6201,
		Status: enums.ServiceSessionStatusClosed, StartedAt: t0, AssignedAt: &t0, FirstHumanReplyAt: &repliedAt, EndedAt: &endedAt,
		FirstAssignmentID: assignment.ID, LastAssignmentID: assignment.ID, AssignedTeamID: oldTeam.ID, AssignedSquadID: oldSquad.ID,
		AssignedAgentID: agent.ID, HumanMessageCount: 1, HumanHandled: true, AssignmentCount: 1, FirstResponseSeconds: 180,
		AuditFields: testAnalyticsAudit(t0),
	}
	if err := db.Create(session).Error; err != nil {
		t.Fatalf("create historical session: %v", err)
	}
	message := createAnalyticsMessage(t, db, tenantID, assignment.ConversationID, 1, 1, enums.IMSenderTypeAgent, agent.ID, "historical-human-reply", repliedAt)
	if err := db.Create(&models.ConversationResponseSpan{
		TenantID: tenantID, ConversationID: assignment.ConversationID, SessionNo: 1, AssignmentID: assignment.ID,
		TeamID: oldTeam.ID, SquadID: oldSquad.ID, AgentID: agent.ID, CustomerStartMessageID: 7001, CustomerEndMessageID: 7001,
		StartedAt: t0.Add(time.Minute), RepliedAt: &repliedAt, ReplyMessageID: message.ID, WaitSeconds: 120, Status: enums.ResponseSpanStatusReplied,
		AuditFields: testAnalyticsAudit(t0),
	}).Error; err != nil {
		t.Fatalf("create historical response span: %v", err)
	}
	if err := db.Create(&models.QualityInspection{
		TenantID: tenantID, ConversationID: assignment.ConversationID, SessionNo: 1, AssignmentID: assignment.ID, AgentID: agent.ID,
		TeamID: oldTeam.ID, TemplateID: 1, Status: enums.QualityInspectionStatusCompleted, TotalScore: 90, MaxScore: 100,
		Result: enums.QualityInspectionResultPassed, InspectedAt: &endedAt, AuditFields: testAnalyticsAudit(endedAt),
	}).Error; err != nil {
		t.Fatalf("create historical quality inspection: %v", err)
	}

	operator := &dto.AuthPrincipal{UserID: 1, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}
	query := ServiceAnalyticsQuery{StartAt: t0.Add(-time.Minute), EndAt: t0.Add(time.Hour), SquadID: oldSquad.ID}
	overview, err := ServiceAnalyticsService.GetOverview(query, operator)
	if err != nil {
		t.Fatalf("historical squad overview: %v", err)
	}
	if overview.Summary.SessionCount != 1 || overview.Summary.HumanMessageCount != 1 || overview.Summary.QualityInspectableCount != 1 || overview.Summary.QualityInspectionCount != 1 || overview.Summary.QualityPendingCount != 0 || overview.Summary.QualityPassedCount != 1 || overview.Summary.QualityCoverageRate != 100 || overview.Summary.QualityPassRate != 100 || overview.Summary.AverageQualityScore != 90 {
		t.Fatalf("historical squad metrics lost after membership change: %+v", overview.Summary)
	}
	if len(overview.Agents) != 1 || overview.Agents[0].AssignedCount != 1 || overview.Agents[0].HumanMessageCount != 1 || overview.Agents[0].ResponseCount != 1 || len(overview.Agents[0].SquadNames) != 1 || overview.Agents[0].SquadNames[0] != oldSquad.Name {
		t.Fatalf("historical agent metrics=%+v", overview.Agents)
	}
	qualityPool, _, err := QualityInspectionService.ListPool(QualityPoolQuery{Page: 1, Limit: 20, TeamID: oldTeam.ID}, operator)
	if err != nil {
		t.Fatalf("historical quality pool: %v", err)
	}
	if len(qualityPool) != 1 || qualityPool[0].Assignment.ID != assignment.ID || qualityPool[0].Team == nil || qualityPool[0].Team.ID != oldTeam.ID {
		t.Fatalf("historical quality pool used current profile: %+v", qualityPool)
	}

	query.SquadID = currentSquad.ID
	currentOverview, err := ServiceAnalyticsService.GetOverview(query, operator)
	if err != nil {
		t.Fatalf("current squad overview: %v", err)
	}
	if currentOverview.Summary.SessionCount != 0 || currentOverview.Summary.QualityInspectableCount != 0 || currentOverview.Summary.QualityInspectionCount != 0 || currentOverview.Summary.AverageQualityScore != 0 || len(currentOverview.Agents) != 0 {
		t.Fatalf("current membership rewrote historical metrics: summary=%+v agents=%+v", currentOverview.Summary, currentOverview.Agents)
	}

	query.IncludeCurrentAgentRoster = true
	rosterOverview, err := ServiceAnalyticsService.GetOverview(query, operator)
	if err != nil {
		t.Fatalf("current squad roster overview: %v", err)
	}
	if len(rosterOverview.Agents) != 1 || rosterOverview.Agents[0].AgentID != agent.ID || rosterOverview.Agents[0].AssignedCount != 0 || rosterOverview.Agents[0].HumanMessageCount != 0 {
		t.Fatalf("explicit current roster was not isolated from historical metrics: %+v", rosterOverview.Agents)
	}
}

func TestServiceAnalyticsQualityMetricsOnlyCountHumanReplyAssignments(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	t0 := time.Date(2026, 7, 17, 8, 0, 0, 0, time.Local)
	tenantID := int64(651)
	team := &models.AgentTeam{TenantID: tenantID, Name: "人工质检组", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	agent := &models.User{TenantID: tenantID, Username: "quality-agent", Nickname: "质检客服", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	if err := db.Create(team).Error; err != nil {
		t.Fatalf("create quality team: %v", err)
	}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create quality agent: %v", err)
	}
	if err := db.Create(&models.AgentProfile{TenantID: tenantID, UserID: agent.ID, TeamID: team.ID, AgentCode: "QUALITY", DisplayName: agent.Nickname, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}).Error; err != nil {
		t.Fatalf("create quality profile: %v", err)
	}

	assignments := make([]models.ConversationAssignment, 4)
	for i := range assignments {
		conversationID := int64(6501 + i)
		conversation := &models.Conversation{
			ID: conversationID, TenantID: tenantID, CustomerName: "质检客户", Status: enums.IMConversationStatusClosed,
			LastMessageAt: t0, LastActiveAt: t0, AuditFields: testAnalyticsAudit(t0),
		}
		if err := db.Create(conversation).Error; err != nil {
			t.Fatalf("create quality conversation: %v", err)
		}
		assignments[i] = models.ConversationAssignment{
			TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ToUserID: agent.ID,
			AssignType: string(enums.IMAssignmentTypeAssign), Status: enums.IMAssignmentStatusInactive, CreatedAt: t0.Add(time.Duration(i) * time.Minute),
		}
		if err := db.Create(&assignments[i]).Error; err != nil {
			t.Fatalf("create quality assignment: %v", err)
		}
		senderType := enums.IMSenderTypeAgent
		senderID := agent.ID
		if i == len(assignments)-1 {
			senderType = enums.IMSenderTypeAI
			senderID = 0
		}
		createAnalyticsMessage(t, db, tenantID, conversationID, 1, 1, senderType, senderID, "quality evidence", t0.Add(time.Duration(i+1)*time.Minute))
	}
	inspectedAt := t0.Add(30 * time.Minute)
	for index, score := range []int{90, 60} {
		result := enums.QualityInspectionResultPassed
		if score < 80 {
			result = enums.QualityInspectionResultFailed
		}
		inspection := &models.QualityInspection{
			TenantID: tenantID, ConversationID: assignments[index].ConversationID, SessionNo: 1, AssignmentID: assignments[index].ID,
			AgentID: agent.ID, TeamID: team.ID, TemplateID: 1, Status: enums.QualityInspectionStatusCompleted,
			TotalScore: score, MaxScore: 100, Result: result, InspectedAt: &inspectedAt, AuditFields: testAnalyticsAudit(inspectedAt),
		}
		if err := db.Create(inspection).Error; err != nil {
			t.Fatalf("create quality inspection: %v", err)
		}
	}

	operator := &dto.AuthPrincipal{UserID: 1, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}
	overview, err := ServiceAnalyticsService.GetOverview(ServiceAnalyticsQuery{StartAt: t0.Add(-time.Minute), EndAt: t0.Add(time.Hour)}, operator)
	if err != nil {
		t.Fatalf("get quality overview: %v", err)
	}
	summary := overview.Summary
	if summary.QualityInspectableCount != 3 || summary.QualityInspectionCount != 2 || summary.QualityPendingCount != 1 || summary.QualityPassedCount != 1 || summary.QualityFailedCount != 1 || summary.QualityPassRate != 50 || summary.AverageQualityScore != 75 {
		t.Fatalf("quality summary=%+v", summary)
	}
	if summary.QualityCoverageRate < 66.6 || summary.QualityCoverageRate > 66.7 {
		t.Fatalf("quality coverage=%f", summary.QualityCoverageRate)
	}
	if len(overview.Agents) != 1 {
		t.Fatalf("quality agents=%+v", overview.Agents)
	}
	qualityAgent := overview.Agents[0]
	if qualityAgent.QualityInspectableCount != 3 || qualityAgent.QualityInspectionCount != 2 || qualityAgent.QualityPendingCount != 1 || qualityAgent.QualityPassedCount != 1 || qualityAgent.QualityFailedCount != 1 || qualityAgent.QualityPassRate != 50 || qualityAgent.AverageQualityScore != 75 {
		t.Fatalf("quality agent=%+v", qualityAgent)
	}
}

func TestServiceAnalyticsRealtimeUsesCurrentRouteAndMessageTime(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tenantID := int64(701)
	operator := &dto.AuthPrincipal{UserID: 1, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}
	oldStartedAt := todayStart.Add(-time.Hour)
	assignedAt := oldStartedAt.Add(10 * time.Minute)
	queueEnteredAt := now.Add(-2 * time.Minute)
	sessions := []*models.ConversationServiceSession{
		{TenantID: tenantID, ConversationID: 7101, SessionNo: 1, Status: enums.ServiceSessionStatusOpen, StartedAt: oldStartedAt, QueueEnteredAt: &queueEnteredAt, AssignedAt: &assignedAt, AuditFields: testAnalyticsAudit(oldStartedAt)},
		{TenantID: tenantID, ConversationID: 7102, SessionNo: 1, Status: enums.ServiceSessionStatusOpen, StartedAt: now.Add(-time.Minute), AssignedAt: &assignedAt, AuditFields: testAnalyticsAudit(now)},
		{TenantID: tenantID, ConversationID: 7103, SessionNo: 1, Status: enums.ServiceSessionStatusOpen, StartedAt: now.Add(-time.Minute), AuditFields: testAnalyticsAudit(now)},
	}
	for _, session := range sessions {
		if err := db.Create(session).Error; err != nil {
			t.Fatalf("create realtime session: %v", err)
		}
	}
	routes := []*models.ConversationRouteState{
		{TenantID: tenantID, ConversationID: 7101, SessionNo: 1, RouteStatus: enums.ConversationRouteStatusHQAgentDeskPending, AuditFields: testAnalyticsAudit(now)},
		{TenantID: tenantID, ConversationID: 7102, SessionNo: 1, RouteStatus: enums.ConversationRouteStatusAIServing, AuditFields: testAnalyticsAudit(now)},
		{TenantID: tenantID, ConversationID: 7103, SessionNo: 1, RouteStatus: enums.ConversationRouteStatusStoreWecomManual, AuditFields: testAnalyticsAudit(now)},
	}
	for _, route := range routes {
		if err := db.Create(route).Error; err != nil {
			t.Fatalf("create realtime route: %v", err)
		}
	}
	createAnalyticsMessage(t, db, tenantID, 7101, 1, 1, enums.IMSenderTypeCustomer, 0, "today-on-old-session", now)
	createAnalyticsMessage(t, db, tenantID, 7103, 1, 1, enums.IMSenderTypeCustomer, 0, "yesterday-message", oldStartedAt)

	overview, err := ServiceAnalyticsService.GetOverview(ServiceAnalyticsQuery{StartAt: todayStart, EndAt: now}, operator)
	if err != nil {
		t.Fatalf("get realtime overview: %v", err)
	}
	if overview.Realtime.OpenSessionCount != 3 || overview.Realtime.QueueingCount != 1 || overview.Realtime.AssignedActiveCount != 1 {
		t.Fatalf("realtime route counts=%+v", overview.Realtime)
	}
	if overview.Realtime.TodaySessionCount != 2 || overview.Realtime.TodayQueueCount != 1 || overview.Realtime.TodayMessageCount != 1 {
		t.Fatalf("realtime today counts=%+v", overview.Realtime)
	}
}

func TestServiceAnalyticsCurrentAgentStatusIgnoresStaleOpenPresence(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	now := time.Now()
	tenantID := int64(751)
	team := &models.AgentTeam{TenantID: tenantID, Name: "实时状态组", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	if err := db.Create(team).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}
	staleUser := &models.User{TenantID: tenantID, Username: "stale-presence-agent", Nickname: "离线客服", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	freshUser := &models.User{TenantID: tenantID, Username: "fresh-presence-agent", Nickname: "在线客服", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	for _, user := range []*models.User{staleUser, freshUser} {
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	staleProfile := &models.AgentProfile{TenantID: tenantID, UserID: staleUser.ID, TeamID: team.ID, AgentCode: "STALE", DisplayName: staleUser.Nickname, MaxConcurrentCount: 5, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	freshProfile := &models.AgentProfile{TenantID: tenantID, UserID: freshUser.ID, TeamID: team.ID, AgentCode: "FRESH", DisplayName: freshUser.Nickname, MaxConcurrentCount: 5, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	for _, profile := range []*models.AgentProfile{staleProfile, freshProfile} {
		if err := db.Create(profile).Error; err != nil {
			t.Fatalf("create profile: %v", err)
		}
	}
	staleSeenAt := now.Add(-3 * presenceHeartbeatInterval)
	freshSeenAt := now.Add(-presenceHeartbeatInterval / 2)
	presences := []*models.AgentPresenceSession{
		{TenantID: tenantID, UserID: staleUser.ID, AgentProfileID: staleProfile.ID, TeamID: team.ID, Status: enums.AgentPresenceStatusIdle, Source: "test", StartedAt: staleSeenAt, LastSeenAt: staleSeenAt, AuditFields: testAnalyticsAudit(staleSeenAt)},
		{TenantID: tenantID, UserID: freshUser.ID, AgentProfileID: freshProfile.ID, TeamID: team.ID, Status: enums.AgentPresenceStatusIdle, Source: "test", StartedAt: freshSeenAt, LastSeenAt: freshSeenAt, AuditFields: testAnalyticsAudit(freshSeenAt)},
	}
	for _, presence := range presences {
		if err := db.Create(presence).Error; err != nil {
			t.Fatalf("create presence: %v", err)
		}
	}

	operator := &dto.AuthPrincipal{UserID: 1, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}
	overview, err := ServiceAnalyticsService.GetOverview(ServiceAnalyticsQuery{
		StartAt:                   now.Add(-time.Hour),
		EndAt:                     now,
		IncludeCurrentAgentRoster: true,
	}, operator)
	if err != nil {
		t.Fatalf("get realtime overview: %v", err)
	}
	statuses := map[int64]string{}
	for _, agent := range overview.Agents {
		statuses[agent.AgentID] = agent.CurrentStatus
	}
	if statuses[staleUser.ID] != "offline" {
		t.Fatalf("stale open presence status=%q want offline", statuses[staleUser.ID])
	}
	if statuses[freshUser.ID] != string(enums.AgentPresenceStatusIdle) {
		t.Fatalf("fresh presence status=%q want idle", statuses[freshUser.ID])
	}
	if overview.Realtime.OnlineAgentCount != 1 || overview.Realtime.OfflineAgentCount != 1 {
		t.Fatalf("realtime presence counts=%+v", overview.Realtime)
	}
}

func TestServiceAnalyticsPercentilesAndSourceQuality(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	t0 := time.Date(2026, 7, 17, 9, 0, 0, 0, time.Local)
	tenantID := int64(801)
	team := &models.AgentTeam{TenantID: tenantID, Name: "来源分析组", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	agent := &models.User{TenantID: tenantID, Username: "source-agent", Nickname: "来源客服", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	store := &models.Store{TenantID: tenantID, StoreCode: "STORE-801", Name: "丽斯未来测试门店", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	for _, item := range []any{team, agent, store} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create source fixture: %v", err)
		}
	}
	profile := &models.AgentProfile{TenantID: tenantID, UserID: agent.ID, TeamID: team.ID, AgentCode: "SOURCE", DisplayName: agent.Nickname, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	instance := &models.WxWorkProtocolInstance{TenantID: tenantID, Guid: "source-instance-801", EmployeeName: "丽斯测试员工号", StoreID: store.ID, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create source profile: %v", err)
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create source instance: %v", err)
	}
	conversation := &models.Conversation{
		ID: 8101, TenantID: tenantID, CustomerName: "来源测试客户", Status: enums.IMConversationStatusClosed,
		LastMessageAt: t0, LastActiveAt: t0, AuditFields: testAnalyticsAudit(t0),
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create source conversation: %v", err)
	}
	queueAt := t0.Add(time.Minute)
	assignedAt := t0.Add(2 * time.Minute)
	repliedAt := t0.Add(5 * time.Minute)
	endedAt := t0.Add(20 * time.Minute)
	assignment := &models.ConversationAssignment{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, ToUserID: agent.ID,
		AssignType: string(enums.IMAssignmentTypeAssign), Status: enums.IMAssignmentStatusInactive,
		CreatedAt: assignedAt, FinishedAt: &endedAt,
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatalf("create source assignment: %v", err)
	}
	session := &models.ConversationServiceSession{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, CustomerID: 8201,
		StoreID: store.ID, WxWorkInstanceID: instance.ID, Status: enums.ServiceSessionStatusClosed,
		StartedAt: t0, QueueEnteredAt: &queueAt, AssignedAt: &assignedAt, FirstHumanReplyAt: &repliedAt, EndedAt: &endedAt,
		FirstAssignmentID: assignment.ID, LastAssignmentID: assignment.ID, AssignedTeamID: team.ID, AssignedAgentID: agent.ID,
		HumanMessageCount: 1, HumanHandled: true, AssignmentCount: 1, QueueSeconds: 60,
		FirstResponseSeconds: 180, TotalHumanWaitSeconds: 240, AuditFields: testAnalyticsAudit(t0),
	}
	if err := db.Create(session).Error; err != nil {
		t.Fatalf("create source session: %v", err)
	}
	reply := createAnalyticsMessage(t, db, tenantID, conversation.ID, 1, 1, enums.IMSenderTypeAgent, agent.ID, "source-human-reply", repliedAt)
	if err := db.Create(&models.ConversationResponseSpan{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, AssignmentID: assignment.ID,
		TeamID: team.ID, AgentID: agent.ID, CustomerStartMessageID: 9001, CustomerEndMessageID: 9001,
		StartedAt: queueAt, RepliedAt: &repliedAt, ReplyMessageID: reply.ID, WaitSeconds: 240,
		Status: enums.ResponseSpanStatusReplied, AuditFields: testAnalyticsAudit(t0),
	}).Error; err != nil {
		t.Fatalf("create source response span: %v", err)
	}
	if err := db.Create(&models.QualityInspection{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, AssignmentID: assignment.ID,
		AgentID: agent.ID, TeamID: team.ID, TemplateID: 1, Status: enums.QualityInspectionStatusCompleted,
		TotalScore: 88, MaxScore: 100, Result: enums.QualityInspectionResultPassed, InspectedAt: &endedAt,
		AuditFields: testAnalyticsAudit(endedAt),
	}).Error; err != nil {
		t.Fatalf("create source inspection: %v", err)
	}
	if err := db.Create(&models.ConversationEvaluation{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, AssignmentID: assignment.ID,
		CustomerID: session.CustomerID, Status: enums.ConversationEvaluationStatusSubmitted,
		TokenHash: "source-evaluation-801", InvitedAt: endedAt, ExpiresAt: endedAt.Add(24 * time.Hour), SubmittedAt: &endedAt,
		Rating: 5, AuditFields: testAnalyticsAudit(endedAt),
	}).Error; err != nil {
		t.Fatalf("create source evaluation: %v", err)
	}

	operator := &dto.AuthPrincipal{UserID: 1, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}
	overview, err := ServiceAnalyticsService.GetOverview(ServiceAnalyticsQuery{StartAt: t0.Add(-time.Minute), EndAt: t0.Add(time.Hour)}, operator)
	if err != nil {
		t.Fatalf("get source overview: %v", err)
	}
	if overview.Summary.P50QueueSeconds != 60 || overview.Summary.P90QueueSeconds != 60 || overview.Summary.P50FirstReplySeconds != 180 || overview.Summary.P90ResponseSeconds != 240 || overview.Summary.P50HumanWaitSeconds != 240 || overview.Summary.P90SessionSeconds != 1200 {
		t.Fatalf("percentiles=%+v", overview.Summary)
	}
	if len(overview.Sources) != 1 {
		t.Fatalf("sources=%+v", overview.Sources)
	}
	source := overview.Sources[0]
	if source.QualityInspectableCount != 1 || source.QualityInspectionCount != 1 || source.QualityPassedCount != 1 || source.QualityCoverageRate != 100 || source.QualityPassRate != 100 || source.AverageQualityScore != 88 || source.EvaluationInviteCount != 1 || source.EvaluationSubmittedCount != 1 || source.SatisfactionRate != 100 || source.AverageSatisfaction != 5 {
		t.Fatalf("source quality=%+v", source)
	}
	if len(overview.Agents) != 1 || overview.Agents[0].EvaluationSubmittedCount != 1 || overview.Agents[0].SatisfactionRate != 100 || overview.Agents[0].ServiceSeconds != 1080 {
		t.Fatalf("agent quality and workload=%+v", overview.Agents)
	}
}

func TestNearestRankPercentile(t *testing.T) {
	p50, p90 := percentilePair([]int64{100, 10, 40, 20, 30})
	if p50 != 30 || p90 != 100 {
		t.Fatalf("percentiles p50=%v p90=%v", p50, p90)
	}
}

func TestServiceAnalyticsTrendFillsCrossDayAndEmptyDates(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	tenantID := int64(831)
	startAt := time.Date(2026, 7, 16, 23, 0, 0, 0, time.Local)
	endAt := time.Date(2026, 7, 18, 1, 0, 0, 0, time.Local)
	team := &models.AgentTeam{TenantID: tenantID, Name: "跨日客服组", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(startAt)}
	agent := &models.User{TenantID: tenantID, Username: "cross-day-agent", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(startAt)}
	if err := db.Create(team).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := db.Create(&models.AgentProfile{TenantID: tenantID, UserID: agent.ID, TeamID: team.ID, AgentCode: "CROSS-DAY", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(startAt)}).Error; err != nil {
		t.Fatalf("create profile: %v", err)
	}
	sessionStartedAt := startAt.Add(50 * time.Minute)
	sessionEndedAt := sessionStartedAt.Add(30 * time.Minute)
	if err := db.Create(&models.ConversationServiceSession{
		TenantID: tenantID, ConversationID: 83101, SessionNo: 1, Status: enums.ServiceSessionStatusClosed,
		StartedAt: sessionStartedAt, EndedAt: &sessionEndedAt, AssignedTeamID: team.ID, AssignedAgentID: agent.ID,
		FactOrigin: enums.AnalyticsFactOriginRuntime, DataQuality: enums.AnalyticsDataQualityExact,
		AuditFields: testAnalyticsAudit(sessionStartedAt),
	}).Error; err != nil {
		t.Fatalf("create cross-day session: %v", err)
	}
	responseStartedAt := time.Date(2026, 7, 16, 23, 58, 0, 0, time.Local)
	responseRepliedAt := time.Date(2026, 7, 17, 0, 5, 0, 0, time.Local)
	if err := db.Create(&models.ConversationResponseSpan{
		TenantID: tenantID, ConversationID: 83101, SessionNo: 1, TeamID: team.ID, AgentID: agent.ID,
		CustomerStartMessageID: 831001, StartedAt: responseStartedAt, RepliedAt: &responseRepliedAt,
		WaitSeconds: 420, Status: enums.ResponseSpanStatusReplied,
		FactOrigin: enums.AnalyticsFactOriginRuntime, DataQuality: enums.AnalyticsDataQualityExact,
		AuditFields: testAnalyticsAudit(responseStartedAt),
	}).Error; err != nil {
		t.Fatalf("create cross-day response: %v", err)
	}

	operator := &dto.AuthPrincipal{UserID: 1, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}
	overview, err := ServiceAnalyticsService.GetOverview(ServiceAnalyticsQuery{StartAt: startAt, EndAt: endAt}, operator)
	if err != nil {
		t.Fatalf("cross-day overview: %v", err)
	}
	if len(overview.Trend) != 3 {
		t.Fatalf("cross-day trend=%+v, want three calendar dates", overview.Trend)
	}
	if overview.Trend[0].Date != "2026-07-16" || overview.Trend[0].Sessions != 1 {
		t.Fatalf("session should remain on start date, trend=%+v", overview.Trend)
	}
	if overview.Trend[1].Date != "2026-07-17" || overview.Trend[1].AverageResponse != 420 {
		t.Fatalf("response should belong to reply date, trend=%+v", overview.Trend)
	}
	if overview.Trend[2].Date != "2026-07-18" || overview.Trend[2].Sessions != 0 || overview.Trend[2].AverageResponse != 0 {
		t.Fatalf("empty trailing date should be explicit zero, trend=%+v", overview.Trend)
	}

	emptyOperator := &dto.AuthPrincipal{UserID: 2, ActiveTenantID: tenantID + 1, Roles: []string{constants.RoleCodeTenantAdmin}}
	empty, err := ServiceAnalyticsService.GetOverview(ServiceAnalyticsQuery{StartAt: startAt, EndAt: endAt}, emptyOperator)
	if err != nil {
		t.Fatalf("empty overview: %v", err)
	}
	if empty.Summary.SessionCount != 0 || len(empty.Trend) != 3 {
		t.Fatalf("empty overview should preserve zero-valued calendar axis, summary=%+v trend=%+v", empty.Summary, empty.Trend)
	}
}

func createScopedAnalyticsSession(t *testing.T, db *gorm.DB, tenantID, conversationID, customerID, teamID, agentID int64, startedAt time.Time) {
	t.Helper()
	endedAt := startedAt.Add(10 * time.Minute)
	item := &models.ConversationServiceSession{
		TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, CustomerID: customerID,
		Status: enums.ServiceSessionStatusClosed, StartedAt: startedAt, EndedAt: &endedAt,
		AssignedTeamID: teamID, AssignedAgentID: agentID, AssignedAt: &startedAt,
		AuditFields: testAnalyticsAudit(startedAt),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create service session: %v", err)
	}
}
