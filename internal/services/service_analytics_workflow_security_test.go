package services

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
)

func TestConversationEvaluationConcurrentSubmitIsIdempotent(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	if err := db.AutoMigrate(&models.Tenant{}); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	if db.Dialector.Name() == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxOpenConns(16)
	}
	now := time.Now().Truncate(time.Second)
	tenant := &models.Tenant{
		TenantCode: "evaluation-concurrency", LegalName: "评价并发测试公司", ShortName: "评价测试",
		RegistrationType: "test", RegistrationNo: "evaluation-concurrency", Status: enums.StatusOk,
		AuditFields: testAnalyticsAudit(now),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	token := "concurrent-evaluation-token"
	evaluation := &models.ConversationEvaluation{
		TenantID: tenant.ID, ConversationID: 8801, SessionNo: 1, CustomerID: 9901,
		Status: enums.ConversationEvaluationStatusPending, TokenHash: evaluationTokenHash(token),
		InvitedAt: now, ExpiresAt: now.Add(time.Hour), AuditFields: testAnalyticsAudit(now),
	}
	if err := db.Create(evaluation).Error; err != nil {
		t.Fatalf("create evaluation: %v", err)
	}

	const submitters = 12
	errCh := make(chan error, submitters)
	var wg sync.WaitGroup
	for i := 0; i < submitters; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := ConversationEvaluationService.Submit(request.SubmitConversationEvaluationRequest{
				Token: token, Rating: i%5 + 1, TagCodes: []string{"resolved"}, Comment: fmt.Sprintf("并发评价%d", i),
			})
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("idempotent concurrent submit returned error: %v", err)
		}
	}
	stored := repositories.ConversationEvaluationRepository.GetInTenant(db, evaluation.ID, tenant.ID)
	if stored == nil || stored.Status != enums.ConversationEvaluationStatusSubmitted || stored.SubmittedAt == nil || stored.Rating < 1 || stored.Rating > 5 {
		t.Fatalf("submitted evaluation=%+v", stored)
	}
	var count int64
	if err := db.Model(&models.ConversationEvaluation{}).Where("tenant_id = ? AND token_hash = ?", tenant.ID, evaluation.TokenHash).Count(&count).Error; err != nil {
		t.Fatalf("count evaluations: %v", err)
	}
	if count != 1 {
		t.Fatalf("evaluation token created %d rows", count)
	}

	expiredToken := "expired-evaluation-token"
	expired := &models.ConversationEvaluation{
		TenantID: tenant.ID, ConversationID: 8802, SessionNo: 1, CustomerID: 9902,
		Status: enums.ConversationEvaluationStatusPending, TokenHash: evaluationTokenHash(expiredToken),
		InvitedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour), AuditFields: testAnalyticsAudit(now),
	}
	if err := db.Create(expired).Error; err != nil {
		t.Fatalf("create expired evaluation: %v", err)
	}
	if _, err := ConversationEvaluationService.Submit(request.SubmitConversationEvaluationRequest{Token: expiredToken, Rating: 5}); err == nil {
		t.Fatal("expired evaluation token accepted submission")
	}
	validated, err := ConversationEvaluationService.Validate(expiredToken)
	if err != nil || validated.Evaluation.Status != enums.ConversationEvaluationStatusExpired {
		t.Fatalf("expired evaluation validation=%+v err=%v", validated, err)
	}
}

func TestReportViewPresetOwnershipAndDefaultSelection(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	owner := &dto.AuthPrincipal{UserID: 8101, Username: "view-owner", ActiveTenantID: 801}
	otherUser := &dto.AuthPrincipal{UserID: 8102, Username: "other-user", ActiveTenantID: 801}
	foreignTenant := &dto.AuthPrincipal{UserID: owner.UserID, Username: owner.Username, ActiveTenantID: 802}

	first, err := ReportViewPresetService.Save(request.SaveReportViewPresetRequest{
		PageCode: "conversation-records", Name: "我的默认视图", FiltersJSON: `{"status":"open"}`,
		ColumnsJSON: `["customer","status"]`, SortJSON: `{"startedAt":"desc"}`, IsDefault: true,
	}, owner)
	if err != nil {
		t.Fatalf("save first preset: %v", err)
	}
	second, err := ReportViewPresetService.Save(request.SaveReportViewPresetRequest{
		PageCode: "conversation-records", Name: "新的默认视图", FiltersJSON: `{"waitingReply":true}`,
		ColumnsJSON: `["customer","response"]`, SortJSON: `{}`, IsDefault: true,
	}, owner)
	if err != nil {
		t.Fatalf("save second preset: %v", err)
	}
	list, err := ReportViewPresetService.List("conversation-records", owner)
	if err != nil || len(list) != 2 {
		t.Fatalf("owner preset list=%+v err=%v", list, err)
	}
	defaultCount := 0
	for _, item := range list {
		if item.IsDefault {
			defaultCount++
			if item.ID != second.ID {
				t.Fatalf("unexpected default preset=%+v", item)
			}
		}
	}
	if defaultCount != 1 {
		t.Fatalf("default preset count=%d list=%+v", defaultCount, list)
	}
	if storedFirst := repositories.ReportViewPresetRepository.GetOwned(db, first.ID, owner.ActiveTenantID, owner.UserID); storedFirst == nil || storedFirst.IsDefault {
		t.Fatalf("first preset default was not cleared: %+v", storedFirst)
	}

	for name, operator := range map[string]*dto.AuthPrincipal{"other user": otherUser, "foreign tenant": foreignTenant} {
		items, err := ReportViewPresetService.List("conversation-records", operator)
		if err != nil || len(items) != 0 {
			t.Fatalf("%s saw owner presets=%+v err=%v", name, items, err)
		}
		if _, err := ReportViewPresetService.Save(request.SaveReportViewPresetRequest{
			ID: second.ID, PageCode: "conversation-records", Name: "越权修改", FiltersJSON: `{}`,
		}, operator); err == nil {
			t.Fatalf("%s updated owner preset", name)
		}
		if err := ReportViewPresetService.Delete(second.ID, operator); err == nil {
			t.Fatalf("%s deleted owner preset", name)
		}
	}
	if _, err := ReportViewPresetService.Save(request.SaveReportViewPresetRequest{
		PageCode: "conversation-records", Name: "非法视图", FiltersJSON: `"not-an-object"`,
	}, owner); err == nil {
		t.Fatal("scalar report view JSON was accepted")
	}
	if err := ReportViewPresetService.Delete(second.ID, owner); err != nil {
		t.Fatalf("owner delete preset: %v", err)
	}
	list, err = ReportViewPresetService.List("conversation-records", owner)
	if err != nil || len(list) != 1 || list[0].ID != first.ID {
		t.Fatalf("owner list after delete=%+v err=%v", list, err)
	}
}
