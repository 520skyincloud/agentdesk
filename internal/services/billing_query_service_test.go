package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/newapi"
)

type billingTokenClientStub struct {
	settings    *newapi.TokenBillingSettings
	summary     *newapi.TokenUsageSummary
	logs        []newapi.TokenUsageLog
	settingsErr error
	summaryErr  error
	logsErr     error
}

func (s *billingTokenClientStub) GetBillingSettings(context.Context) (*newapi.TokenBillingSettings, error) {
	if s.settingsErr != nil {
		return nil, s.settingsErr
	}
	return s.settings, nil
}

func (s *billingTokenClientStub) GetUsageSummary(context.Context) (*newapi.TokenUsageSummary, error) {
	if s.summaryErr != nil {
		return nil, s.summaryErr
	}
	return s.summary, nil
}

func (s *billingTokenClientStub) ListUsageLogs(context.Context, int64, int64) ([]newapi.TokenUsageLog, error) {
	if s.logsErr != nil {
		return nil, s.logsErr
	}
	return append([]newapi.TokenUsageLog(nil), s.logs...), nil
}

func TestBillingQuerySeparatesOfficialLocalAndExactReconciliation(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	if err := fixture.db.AutoMigrate(&models.AIUsageEvent{}, &models.AIUsageGatewayCall{}); err != nil {
		t.Fatal(err)
	}
	seedActiveStoreCredential(t, fixture, "sk-billing-private", 1)
	operator := billingManager(fixture)
	location := billingTestLocation(t)
	occurredAt := time.Date(2026, time.July, 22, 10, 30, 0, 0, location)

	event := &models.AIUsageEvent{
		TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, StoreStaffBindingID: fixture.binding.ID, EventKey: "billing-event-match",
		RequestID: "local-request", Gateway: AIUsageGatewayNewAPI, GatewayRequestID: "req-match",
		Stage: "reply_generate", Model: "model-reply_llm", ModelProfileID: fixture.profile.ID,
		ModelProfileRevision: fixture.profile.Revision, UsageSlot: "reply_llm", CredentialRevision: 1,
		PromptTokens: 30, CompletionTokens: 12, RequestCount: 1, Status: "success", CreatedAt: occurredAt,
	}
	if err := fixture.db.Create(event).Error; err != nil {
		t.Fatal(err)
	}
	for _, call := range []models.AIUsageGatewayCall{
		{
			TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, StoreStaffBindingID: fixture.binding.ID, CallKey: "newapi:match", EventKey: event.EventKey,
			Gateway: AIUsageGatewayNewAPI, GatewayRequestID: "req-match", StartedAt: occurredAt, FinishedAt: occurredAt.Add(time.Second),
			ReconcileStatus: AIUsageReconcilePending, CreatedAt: occurredAt, UpdatedAt: occurredAt,
		},
		{
			TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, StoreStaffBindingID: fixture.binding.ID, CallKey: "newapi:local-only",
			Gateway: AIUsageGatewayNewAPI, GatewayRequestID: "req-local-only", StartedAt: occurredAt.Add(time.Minute), FinishedAt: occurredAt.Add(time.Minute + time.Second),
			ReconcileStatus: AIUsageReconcilePending, CreatedAt: occurredAt, UpdatedAt: occurredAt,
		},
	} {
		item := call
		if err := fixture.db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}

	client := &billingTokenClientStub{
		settings: &newapi.TokenBillingSettings{QuotaDisplayType: "CNY", QuotaPerUnit: 100, USDExchangeRate: 2},
		summary:  &newapi.TokenUsageSummary{Name: "private-token-name", TotalGranted: 1000, TotalUsed: 250, TotalAvailable: 750},
		logs: []newapi.TokenUsageLog{
			{ID: 1, CreatedAt: occurredAt.Unix(), Type: 2, TokenName: "private-token-name", ModelName: "model-reply_llm", Quota: 20, PromptTokens: 30, CompletionTokens: 12, RequestID: "req-match"},
			{ID: 2, CreatedAt: occurredAt.Add(2 * time.Minute).Unix(), Type: 2, TokenName: "private-token-name", ModelName: "model-reply_llm", Quota: 10, PromptTokens: 8, CompletionTokens: 3, RequestID: "req-official-only"},
		},
	}
	service := newBillingQueryService()
	service.newTokenClient = func(baseURL, apiKey string, _ time.Duration) (billingTokenClient, error) {
		if !strings.Contains(baseURL, "newapi.example.com") || apiKey != "sk-billing-private" {
			t.Fatalf("unexpected billing access baseURL=%q apiKey=%q", baseURL, apiKey)
		}
		return client, nil
	}

	result, err := service.Query(context.Background(), request.BillingQueryRequest{
		StartDate: "2026-07-22", EndDate: "2026-07-22", Limit: 100,
	}, operator)
	if err != nil {
		t.Fatal(err)
	}
	if result.Official.Aggregate.SuccessfulStores != 1 || result.Official.Aggregate.PeriodCostCNY != 0.6 {
		t.Fatalf("official aggregate=%#v", result.Official.Aggregate)
	}
	if result.Local.Aggregate.EventCount != 1 || len(result.Local.Events) != 1 {
		t.Fatalf("local evidence=%#v", result.Local)
	}
	if result.Reconciliation.MatchedCount != 1 || result.Reconciliation.OfficialOnlyCount != 1 || result.Reconciliation.LocalOnlyCount != 1 {
		t.Fatalf("reconciliation=%#v", result.Reconciliation)
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, forbidden := range []string{"sk-billing-private", "private-token-name", "newapi.example.com", "keyFingerprint", "provider"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("billing response leaked %q: %s", forbidden, body)
		}
	}
	var reconciled models.AIUsageGatewayCall
	if err := fixture.db.Where("gateway_request_id = ?", "req-match").Take(&reconciled).Error; err != nil {
		t.Fatal(err)
	}
	if reconciled.ReconcileStatus != AIUsageReconcileCompleted || reconciled.MatchStrategy != AIUsageMatchStrategyRequestID {
		t.Fatalf("persisted reconciliation=%#v", reconciled)
	}
}

func TestBillingQueryAllowsPartialStoreFailure(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	if err := fixture.db.AutoMigrate(&models.AIUsageEvent{}, &models.AIUsageGatewayCall{}); err != nil {
		t.Fatal(err)
	}
	seedActiveStoreCredential(t, fixture, "sk-ready-store", 1)
	second := &models.Store{TenantID: fixture.tenant.ID, StoreCode: "billing-unready", Name: "未就绪门店", Status: 0}
	if err := fixture.db.Create(second).Error; err != nil {
		t.Fatal(err)
	}
	client := &billingTokenClientStub{
		settings: &newapi.TokenBillingSettings{QuotaDisplayType: "CNY", QuotaPerUnit: 100, USDExchangeRate: 1},
		summary:  &newapi.TokenUsageSummary{TotalGranted: 100},
	}
	service := newBillingQueryService()
	service.newTokenClient = func(_ string, apiKey string, _ time.Duration) (billingTokenClient, error) {
		if apiKey != "sk-ready-store" {
			return nil, errors.New("unexpected credential")
		}
		return client, nil
	}
	result, err := service.Query(context.Background(), request.BillingQueryRequest{StartDate: "2026-07-22", EndDate: "2026-07-22"}, billingManager(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if result.Official.Aggregate.StoreCount != 2 || result.Official.Aggregate.SuccessfulStores != 1 || result.Official.Aggregate.FailedStores != 1 {
		t.Fatalf("partial store aggregate=%#v", result.Official.Aggregate)
	}
	if result.Official.Aggregate.CredentialAccountCount != 1 || result.Official.Aggregate.SuccessfulCredentialAccounts != 1 || result.Official.Aggregate.FailedCredentialAccounts != 0 {
		t.Fatalf("partial credential-account aggregate=%#v", result.Official.Aggregate)
	}
	if len(result.Official.Stores) != 2 {
		t.Fatalf("official stores=%d", len(result.Official.Stores))
	}
}

func TestBillingQueryEnforcesTenantStoreAndExportScope(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	manager := billingManager(fixture)
	service := newBillingQueryService()
	if _, err := service.Query(context.Background(), request.BillingQueryRequest{
		TenantID: fixture.tenant.ID + 1, StartDate: "2026-07-22", EndDate: "2026-07-22",
	}, manager); err == nil {
		t.Fatal("tenant manager queried another tenant")
	}

	viewOnly := *manager
	viewOnly.Permissions = []string{constants.PermissionBillingView.Code}
	if _, err := service.QueryExport(context.Background(), request.BillingQueryRequest{StartDate: "2026-07-22", EndDate: "2026-07-22"}, &viewOnly); err == nil {
		t.Fatal("billing export succeeded without billing.export")
	}

	otherStore := &models.Store{TenantID: fixture.tenant.ID, StoreCode: "billing-other", Name: "其他门店", Status: 0}
	if err := fixture.db.Create(otherStore).Error; err != nil {
		t.Fatal(err)
	}
	staff := *fixture.staff
	staff.Permissions = append(staff.Permissions, constants.PermissionBillingView.Code, constants.PermissionBillingExport.Code)
	scope, err := resolveBillingAccessScope(&staff)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Mode != "store" || scope.StoreID != fixture.store.ID {
		t.Fatalf("store scope=%#v", scope)
	}
	if _, err = service.findScopeStores(scope, fixture.tenant.ID, []int64{otherStore.ID}); err == nil {
		t.Fatal("store staff queried a different store")
	}

	platform := &dto.AuthPrincipal{
		UserID: 900, Username: "platform", IsPlatformAccount: true,
		Roles: []string{constants.RoleCodeAdmin}, Permissions: []string{constants.PermissionBillingView.Code},
	}
	platformScope, err := resolveBillingAccessScope(platform)
	if err != nil || !platformScope.Platform || platformScope.Mode != "platform" {
		t.Fatalf("platform scope=%#v err=%v", platformScope, err)
	}
	stores, err := service.findScopeStores(platformScope, fixture.tenant.ID, nil)
	if err != nil || len(stores) != 2 {
		t.Fatalf("platform tenant stores=%d err=%v", len(stores), err)
	}
}

func TestParseBillingDateRangeUsesShanghaiCalendarAnd366DayLimit(t *testing.T) {
	rangeValue, err := parseBillingDateRange("2024-01-01", "2024-12-31")
	if err != nil {
		t.Fatal(err)
	}
	if rangeValue.EndExclusive.Sub(rangeValue.StartAt) != 366*24*time.Hour || rangeValue.StartAt.Location().String() != billingBusinessTimezone {
		t.Fatalf("date range=%#v", rangeValue)
	}
	if _, err = parseBillingDateRange("2024-01-01", "2025-01-01"); err == nil {
		t.Fatal("367-day inclusive range must be rejected")
	}
}

func TestStoreBillingResponseHidesInternalAttribution(t *testing.T) {
	result := &response.BillingQueryResponse{
		Local: response.BillingLocalSectionResponse{
			Events: []response.BillingLocalUsageEventResponse{{RequestID: "internal-local"}},
		},
		Reconciliation: response.BillingReconciliationResponse{
			Items: []response.BillingReconciliationItemResponse{{RequestID: "internal-reconcile"}},
		},
	}
	restrictStoreBillingEvidence(billingAccessScope{Mode: "store", TenantID: 1, StoreID: 2}, result)
	if len(result.Local.Events) != 0 || len(result.Reconciliation.Items) != 0 {
		t.Fatalf("store billing leaked internal evidence: %#v", result)
	}
}

func billingManager(fixture storeCredentialFixture) *dto.AuthPrincipal {
	operator := *fixture.manager
	operator.Permissions = append(operator.Permissions, constants.PermissionBillingView.Code, constants.PermissionBillingExport.Code)
	return &operator
}

func billingTestLocation(t *testing.T) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(billingBusinessTimezone)
	if err != nil {
		t.Fatal(err)
	}
	return location
}
