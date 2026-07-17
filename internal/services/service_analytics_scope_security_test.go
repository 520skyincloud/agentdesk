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
)

func TestAnalyticsDirectAccessRequiresSourceAndAssignmentScope(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.Local)
	tenantID := int64(751)
	foreignTenantID := int64(752)
	store := &models.Store{TenantID: tenantID, StoreCode: "shared-quality-store", Name: "共享门店", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	teamA := &models.AgentTeam{
		TenantID: tenantID, Name: "A客服组", LeaderUserID: 7511, StoreScopeIDs: fmt.Sprint(store.ID),
		Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now),
	}
	teamB := &models.AgentTeam{
		TenantID: tenantID, Name: "B客服组", LeaderUserID: 7521, StoreScopeIDs: fmt.Sprint(store.ID),
		Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now),
	}
	for _, team := range []*models.AgentTeam{teamA, teamB} {
		if err := db.Create(team).Error; err != nil {
			t.Fatalf("create team: %v", err)
		}
	}
	agentA := &models.User{TenantID: tenantID, Username: "shared-scope-agent-a", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	agentB := &models.User{TenantID: tenantID, Username: "shared-scope-agent-b", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	for _, agent := range []*models.User{agentA, agentB} {
		if err := db.Create(agent).Error; err != nil {
			t.Fatalf("create agent: %v", err)
		}
	}
	for _, profile := range []*models.AgentProfile{
		{TenantID: tenantID, UserID: agentA.ID, TeamID: teamA.ID, AgentCode: "SHARED-A", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)},
		{TenantID: tenantID, UserID: agentB.ID, TeamID: teamB.ID, AgentCode: "SHARED-B", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)},
	} {
		if err := db.Create(profile).Error; err != nil {
			t.Fatalf("create profile: %v", err)
		}
	}

	type scopedFixture struct {
		conversation *models.Conversation
		session      *models.ConversationServiceSession
		assignment   *models.ConversationAssignment
	}
	createFixture := func(customerName string, teamID, agentID int64) scopedFixture {
		t.Helper()
		conversation := &models.Conversation{
			TenantID: tenantID, CustomerName: customerName, Status: enums.IMConversationStatusClosed,
			CurrentTeamID: teamID, CurrentAssigneeID: agentID, AuditFields: testAnalyticsAudit(now),
		}
		if err := db.Create(conversation).Error; err != nil {
			t.Fatalf("create conversation: %v", err)
		}
		if err := db.Create(&models.ConversationRouteState{
			TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, StoreID: store.ID,
			RouteStatus: enums.ConversationRouteStatusHQAgentDeskServing, AuditFields: testAnalyticsAudit(now),
		}).Error; err != nil {
			t.Fatalf("create route: %v", err)
		}
		assignment := &models.ConversationAssignment{
			TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, ToUserID: agentID,
			AssignType: string(enums.IMAssignmentTypeAssign), Status: enums.IMAssignmentStatusInactive,
			CreatedAt: now,
		}
		if err := db.Create(assignment).Error; err != nil {
			t.Fatalf("create assignment: %v", err)
		}
		session := &models.ConversationServiceSession{
			TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, StoreID: store.ID,
			Status: enums.ServiceSessionStatusClosed, AssignedTeamID: teamID, AssignedAgentID: agentID,
			FirstAssignmentID: assignment.ID, LastAssignmentID: assignment.ID, StartedAt: now,
			FactOrigin: enums.AnalyticsFactOriginRuntime, DataQuality: enums.AnalyticsDataQualityExact,
			AuditFields: testAnalyticsAudit(now),
		}
		if err := db.Create(session).Error; err != nil {
			t.Fatalf("create session: %v", err)
		}
		return scopedFixture{conversation: conversation, session: session, assignment: assignment}
	}
	fixtureA := createFixture("A组客户", teamA.ID, agentA.ID)
	fixtureB := createFixture("B组客户", teamB.ID, agentB.ID)

	template := &models.QualityTemplate{
		TenantID: tenantID, Name: "共享来源质检", TotalScore: 100, PassScore: 80, Version: 1,
		Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now),
	}
	if err := db.Create(template).Error; err != nil {
		t.Fatalf("create template: %v", err)
	}
	inspectionB := &models.QualityInspection{
		TenantID: tenantID, ConversationID: fixtureB.conversation.ID, SessionNo: 1,
		AssignmentID: fixtureB.assignment.ID, AgentID: agentB.ID, TeamID: teamB.ID, TemplateID: template.ID,
		Status: enums.QualityInspectionStatusDraft, MaxScore: 100, AuditFields: testAnalyticsAudit(now),
	}
	if err := db.Create(inspectionB).Error; err != nil {
		t.Fatalf("create inspection: %v", err)
	}
	batchB := &models.QualitySamplingBatch{
		TenantID: tenantID, Name: "B组抽样", SampleSize: 1, Status: enums.QualitySamplingStatusReady,
		CreatedBy: teamB.LeaderUserID, AuditFields: testAnalyticsAudit(now),
	}
	if err := db.Create(batchB).Error; err != nil {
		t.Fatalf("create sampling batch: %v", err)
	}
	if err := db.Create(&models.QualitySamplingItem{
		TenantID: tenantID, BatchID: batchB.ID, AssignmentID: fixtureB.assignment.ID,
		ConversationID: fixtureB.conversation.ID, SessionNo: 1, AgentID: agentB.ID,
		AuditFields: testAnalyticsAudit(now),
	}).Error; err != nil {
		t.Fatalf("create sampling item: %v", err)
	}

	leaderA := &dto.AuthPrincipal{UserID: teamA.LeaderUserID, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeCsTeamLeader}}
	agentAOperator := &dto.AuthPrincipal{UserID: agentA.ID, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeCsUser}}
	tenantAdmin := &dto.AuthPrincipal{UserID: 7501, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}
	foreignAdmin := &dto.AuthPrincipal{UserID: 7601, ActiveTenantID: foreignTenantID, Roles: []string{constants.RoleCodeTenantAdmin}}

	if !AgentTeamScopeService.CanViewConversation(leaderA, fixtureB.conversation.ID) {
		t.Fatal("test fixture must share source scope so assignment scope is independently exercised")
	}
	list, paging, err := ServiceAnalyticsService.ListSessions(ServiceSessionQuery{Page: 1, Limit: 20}, leaderA)
	if err != nil || paging.Total != 1 || len(list) != 1 || list[0].ID != fixtureA.session.ID {
		t.Fatalf("leader list scope total=%v sessions=%+v err=%v", paging, list, err)
	}
	exported, err := ServiceAnalyticsService.ExportSessions(ServiceSessionQuery{}, leaderA, 100)
	if err != nil || len(exported) != 1 || exported[0].ID != fixtureA.session.ID {
		t.Fatalf("leader export scope sessions=%+v err=%v", exported, err)
	}

	for name, operator := range map[string]*dto.AuthPrincipal{"leader": leaderA, "agent": agentAOperator, "foreign tenant": foreignAdmin} {
		if _, err := ServiceAnalyticsService.GetSession(fixtureB.session.ID, operator); err == nil {
			t.Fatalf("%s accessed another scope service session", name)
		}
		if _, err := ServiceAnalyticsService.UpdateSessionAnnotation(request.UpdateServiceSessionAnnotationRequest{ID: fixtureB.session.ID, SessionSummary: "越权修改"}, operator); err == nil {
			t.Fatalf("%s updated another scope service session", name)
		}
		if _, err := QualityInspectionService.GetInspection(inspectionB.ID, operator); err == nil {
			t.Fatalf("%s accessed another scope inspection", name)
		}
		if _, err := QualityInspectionService.SaveInspection(request.SaveQualityInspectionRequest{
			AssignmentID: fixtureB.assignment.ID, TemplateID: template.ID, Status: string(enums.QualityInspectionStatusDraft),
		}, operator); err == nil {
			t.Fatalf("%s updated another scope inspection", name)
		}
		if _, err := QualityInspectionService.GetSamplingBatch(batchB.ID, operator); err == nil {
			t.Fatalf("%s accessed another scope sampling batch", name)
		}
	}

	if _, err := ServiceAnalyticsService.GetSession(fixtureB.session.ID, tenantAdmin); err != nil {
		t.Fatalf("tenant admin get service session: %v", err)
	}
	if _, err := QualityInspectionService.GetInspection(inspectionB.ID, tenantAdmin); err != nil {
		t.Fatalf("tenant admin get inspection: %v", err)
	}
	if batch, err := QualityInspectionService.GetSamplingBatch(batchB.ID, tenantAdmin); err != nil || len(batch.Items) != 1 {
		t.Fatalf("tenant admin get sampling batch=%+v err=%v", batch, err)
	}
}
