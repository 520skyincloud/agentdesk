package services

import (
	"sync"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

func TestCompletedQualityInspectionIsImmutable(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.Local)
	tenantID := int64(901)
	team := &models.AgentTeam{TenantID: tenantID, Name: "质检组", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	agent := &models.User{TenantID: tenantID, Username: "immutable-quality-agent", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	if err := db.Create(team).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := db.Create(&models.AgentProfile{TenantID: tenantID, UserID: agent.ID, TeamID: team.ID, AgentCode: "IMMUTABLE", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}).Error; err != nil {
		t.Fatalf("create profile: %v", err)
	}
	conversation := &models.Conversation{ID: 9101, TenantID: tenantID, Status: enums.IMConversationStatusClosed, AuditFields: testAnalyticsAudit(now)}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	finishedAt := now.Add(10 * time.Minute)
	assignment := &models.ConversationAssignment{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, ToUserID: agent.ID,
		AssignType: string(enums.IMAssignmentTypeAssign), Status: enums.IMAssignmentStatusInactive,
		CreatedAt: now, FinishedAt: &finishedAt,
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	createAnalyticsMessage(t, db, tenantID, conversation.ID, 1, 1, enums.IMSenderTypeAgent, agent.ID, "immutable-quality-reply", now.Add(time.Minute))
	template := &models.QualityTemplate{
		TenantID: tenantID, Name: "人工评分", TotalScore: 100, PassScore: 80, Version: 1,
		IsDefault: true, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now),
	}
	if err := db.Create(template).Error; err != nil {
		t.Fatalf("create template: %v", err)
	}
	item := &models.QualityTemplateItem{
		TenantID: tenantID, TemplateID: template.ID, Code: "quality", Name: "服务质量",
		RuleType: enums.QualityRuleTypeScore, MaxScore: 100, Required: true, Status: enums.StatusOk,
		AuditFields: testAnalyticsAudit(now),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create template item: %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: 1, Username: "tenant-admin", ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}
	completed, err := QualityInspectionService.SaveInspection(request.SaveQualityInspectionRequest{
		AssignmentID: assignment.ID, TemplateID: template.ID, Status: string(enums.QualityInspectionStatusCompleted),
		Summary: "首次完成", Items: []request.QualityInspectionItemRequest{{TemplateItemID: item.ID, Score: 90}},
	}, operator)
	if err != nil {
		t.Fatalf("complete inspection: %v", err)
	}
	if _, err := QualityInspectionService.SaveInspection(request.SaveQualityInspectionRequest{
		ID: completed.Inspection.ID, AssignmentID: assignment.ID, TemplateID: template.ID, Status: string(enums.QualityInspectionStatusCompleted),
		Summary: "试图覆盖", Items: []request.QualityInspectionItemRequest{{TemplateItemID: item.ID, Score: 10}},
	}, operator); err == nil {
		t.Fatal("completed inspection must reject updates")
	}
	stored := repositories.QualityInspectionRepository.GetInTenant(db, completed.Inspection.ID, tenantID)
	if stored == nil || stored.TotalScore != 90 || stored.Summary != "首次完成" {
		t.Fatalf("completed inspection changed: %+v", stored)
	}
}

func TestConcurrentQualityCompletionCreatesOneImmutableResult(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	if db.Dialector.Name() == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxOpenConns(16)
	}
	now := time.Date(2026, 7, 17, 11, 0, 0, 0, time.Local)
	tenantID := int64(902)
	team := &models.AgentTeam{TenantID: tenantID, Name: "并发质检组", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	agent := &models.User{TenantID: tenantID, Username: "concurrent-quality-agent", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	if err := db.Create(team).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := db.Create(&models.AgentProfile{TenantID: tenantID, UserID: agent.ID, TeamID: team.ID, AgentCode: "CONCURRENT", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}).Error; err != nil {
		t.Fatalf("create profile: %v", err)
	}
	conversation := &models.Conversation{
		ID: 9201, TenantID: tenantID, Status: enums.IMConversationStatusClosed,
		LastMessageAt: now, LastActiveAt: now, AuditFields: testAnalyticsAudit(now),
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	finishedAt := now.Add(10 * time.Minute)
	assignment := &models.ConversationAssignment{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, ToUserID: agent.ID,
		AssignType: string(enums.IMAssignmentTypeAssign), Status: enums.IMAssignmentStatusInactive,
		CreatedAt: now, FinishedAt: &finishedAt,
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	createAnalyticsMessage(t, db, tenantID, conversation.ID, 1, 1, enums.IMSenderTypeAgent, agent.ID, "concurrent-quality-reply", now.Add(time.Minute))
	template := &models.QualityTemplate{
		TenantID: tenantID, Name: "并发人工评分", TotalScore: 100, PassScore: 80, Version: 1,
		IsDefault: true, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now),
	}
	if err := db.Create(template).Error; err != nil {
		t.Fatalf("create template: %v", err)
	}
	item := &models.QualityTemplateItem{
		TenantID: tenantID, TemplateID: template.ID, Code: "quality", Name: "服务质量",
		RuleType: enums.QualityRuleTypeScore, MaxScore: 100, Required: true, Status: enums.StatusOk,
		AuditFields: testAnalyticsAudit(now),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create template item: %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: 1, Username: "tenant-admin", ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}

	type completionResult struct {
		score int
		err   error
	}
	results := make(chan completionResult, 2)
	var wg sync.WaitGroup
	for _, score := range []int{90, 10} {
		score := score
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := QualityInspectionService.SaveInspection(request.SaveQualityInspectionRequest{
				AssignmentID: assignment.ID, TemplateID: template.ID, Status: string(enums.QualityInspectionStatusCompleted),
				Summary: "并发完成", Items: []request.QualityInspectionItemRequest{{TemplateItemID: item.ID, Score: score}},
			}, operator)
			results <- completionResult{score: score, err: err}
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	winnerScore := 0
	for result := range results {
		if result.err == nil {
			successes++
			winnerScore = result.score
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent quality completions succeeded %d times", successes)
	}
	inspections := repositories.QualityInspectionRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).Eq("assignment_id", assignment.ID))
	if len(inspections) != 1 || inspections[0].Status != enums.QualityInspectionStatusCompleted || inspections[0].TotalScore != winnerScore {
		t.Fatalf("concurrent inspection result=%+v winner=%d", inspections, winnerScore)
	}
}
