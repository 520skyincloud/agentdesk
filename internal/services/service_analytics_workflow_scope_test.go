package services

import (
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
)

func TestAnalyticsWorkflowListsRespectTenantTeamAndAgentScopes(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.Local)
	tenantID := int64(701)
	foreignTenantID := int64(702)

	storeA := &models.Store{TenantID: tenantID, StoreCode: "analytics-scope-a", Name: "A门店", Status: enums.StatusOk}
	storeB := &models.Store{TenantID: tenantID, StoreCode: "analytics-scope-b", Name: "B门店", Status: enums.StatusOk}
	for _, store := range []*models.Store{storeA, storeB} {
		if err := db.Create(store).Error; err != nil {
			t.Fatalf("create store: %v", err)
		}
	}
	teamA := &models.AgentTeam{
		TenantID: tenantID, Name: "A客服组", LeaderUserID: 7101, StoreScopeIDs: fmt.Sprint(storeA.ID),
		Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now),
	}
	teamB := &models.AgentTeam{
		TenantID: tenantID, Name: "B客服组", LeaderUserID: 7201, StoreScopeIDs: fmt.Sprint(storeB.ID),
		Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now),
	}
	for _, team := range []*models.AgentTeam{teamA, teamB} {
		if err := db.Create(team).Error; err != nil {
			t.Fatalf("create team: %v", err)
		}
	}
	agentA := &models.User{TenantID: tenantID, Username: "analytics-scope-agent-a", Nickname: "客服A", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	agentB := &models.User{TenantID: tenantID, Username: "analytics-scope-agent-b", Nickname: "客服B", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	for _, user := range []*models.User{agentA, agentB} {
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("create agent: %v", err)
		}
	}
	profiles := []*models.AgentProfile{
		{TenantID: tenantID, UserID: agentA.ID, TeamID: teamA.ID, AgentCode: "SCOPE-A", DisplayName: agentA.Nickname, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)},
		{TenantID: tenantID, UserID: agentB.ID, TeamID: teamB.ID, AgentCode: "SCOPE-B", DisplayName: agentB.Nickname, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)},
	}
	for _, profile := range profiles {
		if err := db.Create(profile).Error; err != nil {
			t.Fatalf("create profile: %v", err)
		}
	}
	squadA := &models.AgentTeamSquad{TenantID: tenantID, TeamID: teamA.ID, Name: "A小组", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	squadB := &models.AgentTeamSquad{TenantID: tenantID, TeamID: teamB.ID, Name: "B小组", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	for _, squad := range []*models.AgentTeamSquad{squadA, squadB} {
		if err := db.Create(squad).Error; err != nil {
			t.Fatalf("create squad: %v", err)
		}
	}

	createScopedAssignment := func(conversationID, customerID, storeID, agentID, squadID int64) *models.ConversationAssignment {
		t.Helper()
		conversation := &models.Conversation{
			ID: conversationID, TenantID: tenantID, CustomerID: customerID, CustomerName: fmt.Sprintf("客户%d", customerID),
			Status: enums.IMConversationStatusClosed, AuditFields: testAnalyticsAudit(now),
		}
		if err := db.Create(conversation).Error; err != nil {
			t.Fatalf("create conversation: %v", err)
		}
		if err := db.Create(&models.ConversationRouteState{
			TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, StoreID: storeID,
			RouteStatus: enums.ConversationRouteStatusHQAgentDeskServing, AuditFields: testAnalyticsAudit(now),
		}).Error; err != nil {
			t.Fatalf("create route: %v", err)
		}
		assignment := &models.ConversationAssignment{
			TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, SquadID: squadID, ToUserID: agentID,
			AssignType: string(enums.IMAssignmentTypeAssign), Status: enums.IMAssignmentStatusInactive,
			CreatedAt: now, FinishedAt: ptrAnalyticsTime(now.Add(20 * time.Minute)),
		}
		if err := db.Create(assignment).Error; err != nil {
			t.Fatalf("create assignment: %v", err)
		}
		return assignment
	}
	assignmentA := createScopedAssignment(70101, 71101, storeA.ID, agentA.ID, squadA.ID)
	assignmentB := createScopedAssignment(70102, 71102, storeB.ID, agentB.ID, squadB.ID)

	batchA := &models.QualitySamplingBatch{TenantID: tenantID, Name: "A抽样", SampleSize: 1, Status: enums.QualitySamplingStatusReady, CreatedBy: 7101, AuditFields: testAnalyticsAudit(now)}
	batchB := &models.QualitySamplingBatch{TenantID: tenantID, Name: "B抽样", SampleSize: 1, Status: enums.QualitySamplingStatusReady, CreatedBy: 7201, AuditFields: testAnalyticsAudit(now)}
	foreignBatch := &models.QualitySamplingBatch{TenantID: foreignTenantID, Name: "外租户抽样", SampleSize: 1, Status: enums.QualitySamplingStatusReady, CreatedBy: 7101, AuditFields: testAnalyticsAudit(now)}
	for _, batch := range []*models.QualitySamplingBatch{batchA, batchB, foreignBatch} {
		if err := db.Create(batch).Error; err != nil {
			t.Fatalf("create sampling batch: %v", err)
		}
	}
	for _, item := range []*models.QualitySamplingItem{
		{TenantID: tenantID, BatchID: batchA.ID, AssignmentID: assignmentA.ID, ConversationID: assignmentA.ConversationID, SessionNo: 1, AgentID: agentA.ID, AuditFields: testAnalyticsAudit(now)},
		{TenantID: tenantID, BatchID: batchB.ID, AssignmentID: assignmentB.ID, ConversationID: assignmentB.ConversationID, SessionNo: 1, AgentID: agentB.ID, AuditFields: testAnalyticsAudit(now)},
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create sampling item: %v", err)
		}
	}

	evaluationA := &models.ConversationEvaluation{TenantID: tenantID, ConversationID: assignmentA.ConversationID, SessionNo: 1, AssignmentID: assignmentA.ID, CustomerID: 71101, Status: enums.ConversationEvaluationStatusSubmitted, TokenHash: "analytics-scope-eval-a", InvitedAt: now, ExpiresAt: now.Add(time.Hour), Rating: 5, AuditFields: testAnalyticsAudit(now)}
	evaluationB := &models.ConversationEvaluation{TenantID: tenantID, ConversationID: assignmentB.ConversationID, SessionNo: 1, AssignmentID: assignmentB.ID, CustomerID: 71102, Status: enums.ConversationEvaluationStatusSubmitted, TokenHash: "analytics-scope-eval-b", InvitedAt: now, ExpiresAt: now.Add(time.Hour), Rating: 2, AuditFields: testAnalyticsAudit(now)}
	foreignEvaluation := &models.ConversationEvaluation{TenantID: foreignTenantID, ConversationID: 70201, SessionNo: 1, AssignmentID: 999, CustomerID: 72201, Status: enums.ConversationEvaluationStatusPending, TokenHash: "analytics-scope-eval-foreign", InvitedAt: now, ExpiresAt: now.Add(time.Hour), AuditFields: testAnalyticsAudit(now)}
	for _, evaluation := range []*models.ConversationEvaluation{evaluationA, evaluationB, foreignEvaluation} {
		if err := db.Create(evaluation).Error; err != nil {
			t.Fatalf("create evaluation: %v", err)
		}
	}

	newCnd := func() *sqls.Cnd { return sqls.NewCnd().Page(1, 20).Desc("id") }
	assertScope := func(name string, operator *dto.AuthPrincipal, expectedBatchID, expectedEvaluationID int64, expectedTotal int64) {
		t.Helper()
		batches, batchPaging, err := QualityInspectionService.ListSamplingBatches(newCnd(), operator)
		if err != nil {
			t.Fatalf("%s list batches: %v", name, err)
		}
		if batchPaging.Total != expectedTotal || len(batches) != int(expectedTotal) {
			t.Fatalf("%s batch scope total=%d batches=%+v", name, batchPaging.Total, batches)
		}
		if expectedBatchID > 0 && (len(batches) != 1 || batches[0].Batch.ID != expectedBatchID || batches[0].Batch.SampleSize != 1) {
			t.Fatalf("%s batch scope=%+v", name, batches)
		}
		evaluations, evaluationPaging, err := ConversationEvaluationService.List(newCnd(), 0, 0, operator)
		if err != nil {
			t.Fatalf("%s list evaluations: %v", name, err)
		}
		if evaluationPaging.Total != expectedTotal || len(evaluations) != int(expectedTotal) {
			t.Fatalf("%s evaluation scope total=%d evaluations=%+v", name, evaluationPaging.Total, evaluations)
		}
		if expectedEvaluationID > 0 && (len(evaluations) != 1 || evaluations[0].ID != expectedEvaluationID) {
			t.Fatalf("%s evaluation scope=%+v", name, evaluations)
		}
	}

	admin := &dto.AuthPrincipal{UserID: 7001, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}
	adminBatches, adminBatchPaging, err := QualityInspectionService.ListSamplingBatches(newCnd(), admin)
	if err != nil || adminBatchPaging.Total != 2 || len(adminBatches) != 2 {
		t.Fatalf("tenant admin batch scope total=%v batches=%+v err=%v", adminBatchPaging, adminBatches, err)
	}
	adminEvaluations, adminEvaluationPaging, err := ConversationEvaluationService.List(newCnd(), 0, 0, admin)
	if err != nil || adminEvaluationPaging.Total != 2 || len(adminEvaluations) != 2 {
		t.Fatalf("tenant admin evaluation scope total=%v evaluations=%+v err=%v", adminEvaluationPaging, adminEvaluations, err)
	}
	assertScope("team leader", &dto.AuthPrincipal{UserID: teamA.LeaderUserID, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeCsTeamLeader}}, batchA.ID, evaluationA.ID, 1)
	assertScope("agent", &dto.AuthPrincipal{UserID: agentA.ID, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeCsUser}}, batchA.ID, evaluationA.ID, 1)
}

func ptrAnalyticsTime(value time.Time) *time.Time { return &value }
