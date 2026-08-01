package services

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/newapi"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	billingBusinessTimezone      = "Asia/Shanghai"
	billingDefaultDetailLimit    = 500
	billingMaxDetailLimit        = 2000
	billingMaxExportLimit        = 10000
	billingMaxStoresPerQuery     = 50
	billingExternalConcurrency   = 4
	billingMaxReconcileEvidence  = 10000
	billingMaxReconcileWrites    = 1000
	billingOfficialStatusReady   = "ready"
	billingOfficialStatusFailed  = "failed"
	billingReconcileMatched      = "matched"
	billingReconcileOfficialOnly = "official_only"
	billingReconcileLocalOnly    = "local_only"
)

type billingTokenClient interface {
	GetBillingSettings(context.Context) (*newapi.TokenBillingSettings, error)
	GetUsageSummary(context.Context) (*newapi.TokenUsageSummary, error)
	ListUsageLogs(context.Context, int64, int64) ([]newapi.TokenUsageLog, error)
}

type billingTokenClientFactory func(string, string, time.Duration) (billingTokenClient, error)

type billingQueryService struct {
	newTokenClient billingTokenClientFactory
}

type billingDateRange struct {
	StartDate      string
	EndDate        string
	StartAt        time.Time
	EndExclusive   time.Time
	StartTimestamp int64
	EndTimestamp   int64
}

type billingAccessScope struct {
	Mode                string
	TenantID            int64
	StoreID             int64
	StoreStaffBindingID int64
	Platform            bool
}

type billingStoreTarget struct {
	Tenant             models.Tenant
	Store              models.Store
	Binding            models.StoreStaffBinding
	BindingAccountName string
}

type billingStoreAccess struct {
	Credential      *resolvedStoreModelCredential
	ProfileRevision int64
	GatewayBaseURL  string
	ModelNames      []string
	Timeout         time.Duration
}

type billingOfficialResult struct {
	Store    response.BillingOfficialStoreResponse
	RawLogs  []newapi.TokenUsageLog
	Settings *newapi.TokenBillingSettings
}

var BillingQueryService = newBillingQueryService()

func newBillingQueryService() *billingQueryService {
	return &billingQueryService{
		newTokenClient: func(baseURL, apiKey string, timeout time.Duration) (billingTokenClient, error) {
			return newapi.NewTokenClient(baseURL, apiKey, timeout)
		},
	}
}

func (s *billingQueryService) Options(operator *dto.AuthPrincipal) (*response.BillingQueryOptionsResponse, error) {
	if err := requireBillingPermission(operator, constants.PermissionBillingView.Code); err != nil {
		return nil, err
	}
	scope, err := resolveBillingAccessScope(operator)
	if err != nil {
		return nil, err
	}
	tenants := s.findTenants(scope)
	stores, err := s.findScopeStores(scope, 0, nil)
	if err != nil {
		return nil, err
	}
	tenantByID := make(map[int64]models.Tenant, len(tenants))
	ret := &response.BillingQueryOptionsResponse{
		ScopeMode: scope.Mode, CanFilterTenants: scope.Platform,
		DefaultTenantID: scope.TenantID, DefaultStoreID: scope.StoreID, DefaultStoreStaffBindingID: scope.StoreStaffBindingID,
		Tenants: make([]response.BillingTenantOptionResponse, 0, len(tenants)),
		Stores:  make([]response.BillingStoreOptionResponse, 0, len(stores)),
	}
	for i := range tenants {
		tenant := tenants[i]
		tenantByID[tenant.ID] = tenant
		ret.Tenants = append(ret.Tenants, response.BillingTenantOptionResponse{
			TenantID: tenant.ID, TenantCode: tenant.TenantCode, TenantName: billingTenantName(tenant),
		})
	}
	for i := range stores {
		store := stores[i]
		tenant := tenantByID[store.TenantID]
		ret.Stores = append(ret.Stores, s.buildStoreOption(store, tenant))
	}
	return ret, nil
}

func (s *billingQueryService) Query(ctx context.Context, req request.BillingQueryRequest, operator *dto.AuthPrincipal) (*response.BillingQueryResponse, error) {
	return s.query(ctx, req, operator, false)
}

func (s *billingQueryService) QueryExport(ctx context.Context, req request.BillingQueryRequest, operator *dto.AuthPrincipal) (*response.BillingQueryResponse, error) {
	return s.query(ctx, req, operator, true)
}

func (s *billingQueryService) query(ctx context.Context, req request.BillingQueryRequest, operator *dto.AuthPrincipal, export bool) (*response.BillingQueryResponse, error) {
	permission := constants.PermissionBillingView.Code
	maxLimit := billingMaxDetailLimit
	if export {
		permission = constants.PermissionBillingExport.Code
		maxLimit = billingMaxExportLimit
	}
	if err := requireBillingPermission(operator, permission); err != nil {
		return nil, err
	}
	scope, err := resolveBillingAccessScope(operator)
	if err != nil {
		return nil, err
	}
	dateRange, err := parseBillingDateRange(req.StartDate, req.EndDate)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = billingDefaultDetailLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	stores, err := s.findScopeStores(scope, req.TenantID, req.StoreIDs)
	if err != nil {
		return nil, err
	}
	if len(stores) == 0 {
		return nil, errorsx.InvalidParam("当前数据范围内没有可查询的门店")
	}
	if len(stores) > billingMaxStoresPerQuery {
		return nil, errorsx.InvalidParam(fmt.Sprintf("单次最多查询 %d 个门店，请先选择接入公司或具体门店", billingMaxStoresPerQuery))
	}
	targets, tenantIDs, storeIDs, bindingIDs, tenantByID, storeByID, accountByBinding, err := s.buildTargets(stores, scope, req.StoreStaffBindingIDs)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, errorsx.InvalidParam("当前数据范围内没有可查询的门店员工号")
	}
	officialResults := s.queryOfficial(ctx, targets, dateRange, req)
	trimOfficialDetails(officialResults, limit)

	evidenceQuery := repositories.AIUsageEvidenceQuery{
		TenantIDs: tenantIDs, StoreIDs: storeIDs,
		StoreStaffBindingIDs: bindingIDs,
		StartAt:              dateRange.StartAt, EndAt: dateRange.EndExclusive,
		ModelName: strings.TrimSpace(req.ModelName), RequestID: strings.TrimSpace(req.RequestID), Limit: limit + 1,
	}
	localAggregate := repositories.AIUsageEventRepository.AggregateEvidence(sqls.DB(), evidenceQuery)
	localEvents := repositories.AIUsageEventRepository.FindEvidence(sqls.DB(), evidenceQuery)
	localTruncated := len(localEvents) > limit
	if localTruncated {
		localEvents = localEvents[:limit]
	}

	result := &response.BillingQueryResponse{
		ScopeMode: scope.Mode, StartDate: dateRange.StartDate, EndDate: dateRange.EndDate,
		BusinessTimezone: billingBusinessTimezone, QueriedAt: time.Now(),
		Official: response.BillingOfficialSectionResponse{Stores: make([]response.BillingOfficialStoreResponse, 0, len(officialResults))},
		Local: response.BillingLocalSectionResponse{
			Aggregate: response.BillingLocalAggregateResponse{
				EventCount: localAggregate.EventCount, RequestCount: localAggregate.RequestCount, FailedCount: localAggregate.FailedCount,
				PromptTokens: localAggregate.PromptTokens, CompletionTokens: localAggregate.CompletionTokens, CachedPromptTokens: localAggregate.CachedPromptTokens,
			},
			Events: make([]response.BillingLocalUsageEventResponse, 0, len(localEvents)), Truncated: localTruncated,
		},
	}
	if len(tenantIDs) == 1 {
		result.TenantID = tenantIDs[0]
		result.TenantName = billingTenantName(tenantByID[tenantIDs[0]])
	}
	storeReady := make(map[int64]bool, len(stores))
	storeSeen := make(map[int64]struct{}, len(stores))
	for i := range officialResults {
		item := officialResults[i].Store
		result.Official.Stores = append(result.Official.Stores, item)
		storeSeen[item.StoreID] = struct{}{}
		if item.StoreStaffBindingID > 0 {
			result.Official.Aggregate.CredentialAccountCount++
		}
		if item.Status == billingOfficialStatusReady {
			storeReady[item.StoreID] = true
			if item.StoreStaffBindingID > 0 {
				result.Official.Aggregate.SuccessfulCredentialAccounts++
			}
		} else if item.StoreStaffBindingID > 0 {
			result.Official.Aggregate.FailedCredentialAccounts++
		}
		result.Official.Aggregate.LogCount += item.PeriodLogCount
		result.Official.Aggregate.PeriodQuota += item.PeriodQuota
		result.Official.Aggregate.PeriodCostCNY += item.PeriodCostCNY
		result.Official.Aggregate.PeriodPromptTokens += item.PeriodPromptTokens
		result.Official.Aggregate.PeriodOutputTokens += item.PeriodOutputTokens
	}
	result.Official.Aggregate.StoreCount = len(storeSeen)
	for storeID := range storeSeen {
		if storeReady[storeID] {
			result.Official.Aggregate.SuccessfulStores++
		} else {
			result.Official.Aggregate.FailedStores++
		}
	}
	for i := range localEvents {
		result.Local.Events = append(result.Local.Events, buildBillingLocalEvent(localEvents[i], tenantByID, storeByID, accountByBinding))
	}
	result.Reconciliation = s.reconcile(officialResults, evidenceQuery, tenantByID, storeByID, accountByBinding, limit)
	restrictStoreBillingEvidence(scope, result)
	return result, nil
}

func restrictStoreBillingEvidence(scope billingAccessScope, result *response.BillingQueryResponse) {
	if scope.Mode != "store" || result == nil {
		return
	}
	result.Local = response.BillingLocalSectionResponse{Events: []response.BillingLocalUsageEventResponse{}}
	result.Reconciliation = response.BillingReconciliationResponse{Items: []response.BillingReconciliationItemResponse{}}
}

func (s *billingQueryService) findTenants(scope billingAccessScope) []models.Tenant {
	cnd := sqls.NewCnd().Where("status <> ?", enums.StatusDeleted).Asc("short_name").Asc("id")
	if !scope.Platform {
		cnd.Eq("id", scope.TenantID)
	}
	return repositories.TenantRepository.Find(sqls.DB(), cnd)
}

func (s *billingQueryService) findScopeStores(scope billingAccessScope, requestedTenantID int64, requestedStoreIDs []int64) ([]models.Store, error) {
	storeIDs := normalizeBillingIDs(requestedStoreIDs)
	if scope.Mode == "store" {
		if requestedTenantID > 0 && requestedTenantID != scope.TenantID {
			return nil, errorsx.Forbidden("只能查看当前门店账单")
		}
		if len(storeIDs) > 0 && (len(storeIDs) != 1 || storeIDs[0] != scope.StoreID) {
			return nil, errorsx.Forbidden("只能查看当前门店账单")
		}
		requestedTenantID = scope.TenantID
		storeIDs = []int64{scope.StoreID}
	} else if !scope.Platform {
		if requestedTenantID > 0 && requestedTenantID != scope.TenantID {
			return nil, errorsx.Forbidden("只能查看当前接入公司的账单")
		}
		requestedTenantID = scope.TenantID
	} else if requestedTenantID > 0 && repositories.TenantRepository.Get(sqls.DB(), requestedTenantID) == nil {
		return nil, errorsx.InvalidParam("接入公司不存在")
	}
	cnd := sqls.NewCnd().Where("status <> ?", enums.StatusDeleted).Asc("tenant_id").Asc("name").Asc("id")
	if requestedTenantID > 0 {
		cnd.Eq("tenant_id", requestedTenantID)
	}
	if len(storeIDs) > 0 {
		cnd.In("id", storeIDs)
	}
	stores := repositories.StoreRepository.Find(sqls.DB(), cnd)
	if len(storeIDs) > 0 {
		found := make(map[int64]struct{}, len(stores))
		for i := range stores {
			found[stores[i].ID] = struct{}{}
		}
		for _, storeID := range storeIDs {
			if _, ok := found[storeID]; !ok {
				return nil, errorsx.Forbidden("所选门店不存在或超出当前数据范围")
			}
		}
	}
	return stores, nil
}

func (s *billingQueryService) buildTargets(
	stores []models.Store,
	scope billingAccessScope,
	requestedBindingIDs []int64,
) ([]billingStoreTarget, []int64, []int64, []int64, map[int64]models.Tenant, map[int64]models.Store, map[int64]string, error) {
	requestedBindingIDs = normalizeBillingIDs(requestedBindingIDs)
	requestedBindings := make(map[int64]struct{}, len(requestedBindingIDs))
	for _, bindingID := range requestedBindingIDs {
		requestedBindings[bindingID] = struct{}{}
	}
	tenantIDs := make([]int64, 0)
	storeIDs := make([]int64, 0, len(stores))
	bindingIDs := make([]int64, 0)
	tenantByID := make(map[int64]models.Tenant)
	storeByID := make(map[int64]models.Store, len(stores))
	accountByBinding := make(map[int64]string)
	tenantSeen := make(map[int64]struct{})
	targets := make([]billingStoreTarget, 0, len(stores))
	for i := range stores {
		store := stores[i]
		tenant, ok := tenantByID[store.TenantID]
		if !ok {
			if item := repositories.TenantRepository.Get(sqls.DB(), store.TenantID); item != nil {
				tenant = *item
			}
			tenantByID[store.TenantID] = tenant
		}
		if _, ok := tenantSeen[store.TenantID]; !ok {
			tenantSeen[store.TenantID] = struct{}{}
			tenantIDs = append(tenantIDs, store.TenantID)
		}
		storeIDs = append(storeIDs, store.ID)
		storeByID[store.ID] = store
		bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", store.TenantID).
			Eq("store_id", store.ID).
			Where("status <> ?", enums.StatusDeleted).
			Asc("id"))
		matchedBindings := 0
		for j := range bindings {
			binding := bindings[j]
			if scope.Mode == "store" && binding.ID != scope.StoreStaffBindingID {
				continue
			}
			if len(requestedBindings) > 0 {
				if _, ok := requestedBindings[binding.ID]; !ok {
					continue
				}
			}
			accountName := fmt.Sprintf("门店员工号 #%d", binding.ID)
			if user := repositories.UserRepository.Get(sqls.DB(), binding.UserID); user != nil {
				accountName = firstNonBlank(strings.TrimSpace(user.Nickname), strings.TrimSpace(user.Username), accountName)
			}
			bindingIDs = append(bindingIDs, binding.ID)
			accountByBinding[binding.ID] = accountName
			targets = append(targets, billingStoreTarget{Tenant: tenant, Store: store, Binding: binding, BindingAccountName: accountName})
			matchedBindings++
		}
		if matchedBindings == 0 && len(requestedBindings) == 0 && scope.Mode != "store" {
			targets = append(targets, billingStoreTarget{
				Tenant: tenant, Store: store, BindingAccountName: "未绑定门店员工号",
			})
		}
	}
	if len(requestedBindings) > 0 {
		found := make(map[int64]struct{}, len(bindingIDs))
		for _, bindingID := range bindingIDs {
			found[bindingID] = struct{}{}
		}
		for _, bindingID := range requestedBindingIDs {
			if _, ok := found[bindingID]; !ok {
				return nil, nil, nil, nil, nil, nil, nil, errorsx.Forbidden("所选门店员工号不存在或超出当前数据范围")
			}
		}
	}
	sort.Slice(tenantIDs, func(i, j int) bool { return tenantIDs[i] < tenantIDs[j] })
	sort.Slice(bindingIDs, func(i, j int) bool { return bindingIDs[i] < bindingIDs[j] })
	return targets, tenantIDs, storeIDs, bindingIDs, tenantByID, storeByID, accountByBinding, nil
}

func (s *billingQueryService) buildStoreOption(store models.Store, tenant models.Tenant) response.BillingStoreOptionResponse {
	ret := response.BillingStoreOptionResponse{
		TenantID: store.TenantID, TenantName: billingTenantName(tenant), StoreID: store.ID, StoreCode: store.StoreCode, StoreName: store.Name,
		ModelNames: []string{},
	}
	bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", store.TenantID).Eq("store_id", store.ID).Where("status <> ?", enums.StatusDeleted))
	ret.BindingCount = len(bindings)
	credentials := repositories.StoreModelCredentialRepository.FindByStore(sqls.DB(), store.TenantID, store.ID)
	if len(credentials) == 1 {
		ret.CredentialStatus = string(credentials[0].Status)
		ret.CredentialRevision = credentials[0].CredentialRevision
	} else if len(credentials) > 1 {
		ret.CredentialStatus = "multiple"
	}
	if assignment := repositories.StoreModelProfileAssignmentRepository.GetByStore(sqls.DB(), store.TenantID, store.ID); assignment != nil {
		ret.ModelProfileRevision = assignment.TemplateRevision
		ret.ModelNames = billingProfileModelNames(assignment.TemplateID)
	}
	return ret
}

func (s *billingQueryService) queryOfficial(ctx context.Context, targets []billingStoreTarget, dateRange billingDateRange, req request.BillingQueryRequest) []billingOfficialResult {
	results := make([]billingOfficialResult, len(targets))
	semaphore := make(chan struct{}, billingExternalConcurrency)
	var wg sync.WaitGroup
	for i := range targets {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index] = failedBillingOfficialResult(targets[index], "request_cancelled", "账单查询已取消")
				return
			}
			results[index] = s.queryOfficialStore(ctx, targets[index], dateRange, req)
		}(i)
	}
	wg.Wait()
	return results
}

func (s *billingQueryService) queryOfficialStore(ctx context.Context, target billingStoreTarget, dateRange billingDateRange, req request.BillingQueryRequest) billingOfficialResult {
	access, class, message := s.loadStoreBillingAccess(target)
	if access == nil {
		return failedBillingOfficialResult(target, class, message)
	}
	client, err := s.newTokenClient(access.GatewayBaseURL, access.Credential.APIKey, access.Timeout)
	if err != nil {
		return failedBillingOfficialResult(target, "billing_client_unavailable", "门店官方账单查询尚未就绪")
	}
	callCtx, cancel := context.WithTimeout(ctx, access.Timeout*3)
	defer cancel()
	settings, err := client.GetBillingSettings(callCtx)
	if err != nil {
		return failedBillingOfficialResult(target, "billing_settings_unavailable", "无法读取 NewAPI 人民币计费配置")
	}
	summary, err := client.GetUsageSummary(callCtx)
	if err != nil {
		return failedBillingOfficialResult(target, "credential_query_failed", "门店 NewAPI 凭据无效或额度查询失败")
	}
	logs, err := client.ListUsageLogs(callCtx, dateRange.StartTimestamp, dateRange.EndTimestamp)
	if err != nil {
		return failedBillingOfficialResult(target, "usage_log_query_failed", "门店 NewAPI 调用明细查询失败")
	}
	ret := billingOfficialResult{
		Settings: settings,
		Store: response.BillingOfficialStoreResponse{
			TenantID: target.Store.TenantID, TenantName: billingTenantName(target.Tenant),
			StoreID: target.Store.ID, StoreCode: target.Store.StoreCode, StoreName: target.Store.Name,
			StoreStaffBindingID: target.Binding.ID, StoreStaffAccountName: target.BindingAccountName,
			CredentialRevision: access.Credential.Revision, ModelProfileRevision: access.ProfileRevision,
			ModelNames: access.ModelNames, Status: billingOfficialStatusReady,
			Logs: []response.BillingOfficialUsageLogResponse{},
			Summary: response.BillingTokenSummaryResponse{
				UnlimitedQuota: summary.UnlimitedQuota,
				TotalGranted:   summary.TotalGranted, TotalUsed: summary.TotalUsed, TotalAvailable: summary.TotalAvailable,
				GrantedCNY: billingQuotaCNY(summary.TotalGranted, settings), UsedCNY: billingQuotaCNY(summary.TotalUsed, settings),
				AvailableCNY: billingQuotaCNY(summary.TotalAvailable, settings), ExpiresAt: summary.ExpiresAt,
			},
		},
		RawLogs: make([]newapi.TokenUsageLog, 0, len(logs)),
	}
	modelFilter := strings.ToLower(strings.TrimSpace(req.ModelName))
	requestFilter := strings.TrimSpace(req.RequestID)
	for _, item := range logs {
		if item.Type != 0 && item.Type != 2 {
			continue
		}
		if item.CreatedAt < dateRange.StartTimestamp || item.CreatedAt > dateRange.EndTimestamp {
			continue
		}
		if modelFilter != "" && strings.ToLower(strings.TrimSpace(item.ModelName)) != modelFilter {
			continue
		}
		if requestFilter != "" && strings.TrimSpace(item.RequestID) != requestFilter {
			continue
		}
		ret.RawLogs = append(ret.RawLogs, item)
		ret.Store.PeriodLogCount++
		ret.Store.PeriodQuota += item.Quota
		ret.Store.PeriodPromptTokens += item.PromptTokens
		ret.Store.PeriodOutputTokens += item.CompletionTokens
		ret.Store.Logs = append(ret.Store.Logs, buildOfficialUsageLog(target, item, settings))
	}
	ret.Store.PeriodCostCNY = billingQuotaCNY(ret.Store.PeriodQuota, settings)
	return ret
}

func (s *billingQueryService) loadStoreBillingAccess(target billingStoreTarget) (*billingStoreAccess, string, string) {
	assignment := repositories.StoreModelProfileAssignmentRepository.GetByStore(sqls.DB(), target.Store.TenantID, target.Store.ID)
	if assignment == nil || assignment.Status != enums.StoreModelAssignmentStatusReady || assignment.TemplateID <= 0 {
		return nil, "model_profile_unavailable", "门店模型方案尚未就绪"
	}
	template := repositories.ModelProfileTemplateRepository.Get(sqls.DB(), assignment.TemplateID)
	if template == nil || template.Status != enums.ModelProfileStatusActive || template.Revision != assignment.TemplateRevision || strings.TrimSpace(template.GatewayBaseURL) == "" {
		return nil, "model_profile_invalid", "门店当前模型方案 revision 无效"
	}
	slots := repositories.ModelProfileSlotRepository.FindByTemplateID(sqls.DB(), template.ID)
	if issues := ValidateModelProfileForPublication(template, slots); len(issues) > 0 {
		return nil, "model_profile_incomplete", "门店当前模型方案九槽不完整"
	}
	credential, err := StoreModelCredentialService.ResolveForBillingBinding(target.Store.TenantID, target.Store.ID, target.Binding.ID)
	if err != nil {
		return nil, "credential_unavailable", "门店员工号尚未配置可用的 NewAPI API Key"
	}
	timeoutMS := 10000
	for i := range slots {
		if slots[i].TimeoutMS > timeoutMS {
			timeoutMS = slots[i].TimeoutMS
		}
	}
	if timeoutMS > 30000 {
		timeoutMS = 30000
	}
	return &billingStoreAccess{
		Credential: credential, ProfileRevision: template.Revision, GatewayBaseURL: template.GatewayBaseURL,
		ModelNames: billingModelNamesFromSlots(slots), Timeout: time.Duration(timeoutMS) * time.Millisecond,
	}, "", ""
}

func (s *billingQueryService) reconcile(official []billingOfficialResult, eventQuery repositories.AIUsageEvidenceQuery, tenantByID map[int64]models.Tenant, storeByID map[int64]models.Store, accountByBinding map[int64]string, limit int) response.BillingReconciliationResponse {
	query := repositories.AIUsageGatewayEvidenceQuery{
		TenantIDs: eventQuery.TenantIDs, StoreIDs: eventQuery.StoreIDs, StartAt: eventQuery.StartAt, EndAt: eventQuery.EndAt,
		StoreStaffBindingIDs: eventQuery.StoreStaffBindingIDs, RequestID: eventQuery.RequestID, Limit: billingMaxReconcileEvidence + 1,
	}
	calls := repositories.AIUsageGatewayCallRepository.FindEvidence(sqls.DB(), query)
	truncated := len(calls) > billingMaxReconcileEvidence
	if truncated {
		calls = calls[:billingMaxReconcileEvidence]
	}
	modelEvents := repositories.AIUsageEventRepository.FindEvidence(sqls.DB(), repositories.AIUsageEvidenceQuery{
		TenantIDs: eventQuery.TenantIDs, StoreIDs: eventQuery.StoreIDs, StartAt: eventQuery.StartAt, EndAt: eventQuery.EndAt,
		StoreStaffBindingIDs: eventQuery.StoreStaffBindingIDs, ModelName: eventQuery.ModelName, RequestID: eventQuery.RequestID, Limit: billingMaxReconcileEvidence,
	})
	eventByRequest := make(map[string]models.AIUsageEvent, len(modelEvents))
	for i := range modelEvents {
		requestID := strings.TrimSpace(modelEvents[i].GatewayRequestID)
		if requestID != "" {
			eventByRequest[billingRequestKey(modelEvents[i].StoreID, modelEvents[i].StoreStaffBindingID, requestID)] = modelEvents[i]
		}
	}
	callByRequest := make(map[string]*models.AIUsageGatewayCall, len(calls))
	for i := range calls {
		requestID := strings.TrimSpace(calls[i].GatewayRequestID)
		if requestID == "" {
			continue
		}
		if eventQuery.ModelName != "" {
			if _, ok := eventByRequest[billingRequestKey(calls[i].StoreID, calls[i].StoreStaffBindingID, requestID)]; !ok {
				continue
			}
		}
		callByRequest[billingRequestKey(calls[i].StoreID, calls[i].StoreStaffBindingID, requestID)] = &calls[i]
	}
	ret := response.BillingReconciliationResponse{Items: []response.BillingReconciliationItemResponse{}, Truncated: truncated}
	matched := make(map[int64]struct{})
	writes := 0
	for i := range official {
		for _, log := range official[i].RawLogs {
			ret.OfficialLogCount++
			requestID := strings.TrimSpace(log.RequestID)
			if requestID == "" {
				ret.MissingRequestIDCount++
				ret.OfficialOnlyCount++
				continue
			}
			store := storeByID[official[i].Store.StoreID]
			item := response.BillingReconciliationItemResponse{
				StoreID: store.ID, StoreName: store.Name, RequestID: requestID,
				StoreStaffBindingID: official[i].Store.StoreStaffBindingID, StoreStaffAccountName: official[i].Store.StoreStaffAccountName,
				Status: billingReconcileOfficialOnly, OfficialModel: log.ModelName,
				OfficialTokens:  log.PromptTokens + log.CompletionTokens,
				OfficialCostCNY: billingQuotaCNY(log.Quota, official[i].Settings),
			}
			officialAt := time.Unix(log.CreatedAt, 0)
			item.OfficialAt = &officialAt
			key := billingRequestKey(store.ID, official[i].Store.StoreStaffBindingID, requestID)
			if call := callByRequest[key]; call != nil {
				item.Status = billingReconcileMatched
				item.LocalAt = &call.StartedAt
				item.LocalTokens = call.ExternalPromptTokens + call.ExternalCompletionTokens
				if event, ok := eventByRequest[key]; ok {
					item.LocalModel = event.Model
					item.LocalTokens = event.PromptTokens + event.CompletionTokens
				}
				ret.MatchedCount++
				matched[call.ID] = struct{}{}
				if writes < billingMaxReconcileWrites {
					now := time.Now()
					_ = repositories.AIUsageGatewayCallRepository.UpdatesInTenant(sqls.DB(), call.ID, call.TenantID, map[string]any{
						"reconcile_status": AIUsageReconcileCompleted, "match_strategy": AIUsageMatchStrategyRequestID,
						"match_confidence": AIUsageMatchConfidenceExact, "external_model": log.ModelName,
						"external_token_name": log.TokenName, "external_prompt_tokens": log.PromptTokens,
						"external_completion_tokens": log.CompletionTokens, "external_quota": log.Quota,
						"external_created_at": officialAt, "reconciled_at": now, "last_error": "", "last_error_class": "", "updated_at": now,
					})
					writes++
				}
			} else {
				ret.OfficialOnlyCount++
			}
			if len(ret.Items) < limit {
				ret.Items = append(ret.Items, item)
			} else {
				ret.Truncated = true
			}
		}
	}
	ret.LocalGatewayCallCount = len(callByRequest)
	for _, call := range callByRequest {
		if _, ok := matched[call.ID]; ok {
			continue
		}
		ret.LocalOnlyCount++
		item := response.BillingReconciliationItemResponse{
			StoreID: call.StoreID, StoreName: storeByID[call.StoreID].Name,
			StoreStaffBindingID: call.StoreStaffBindingID, StoreStaffAccountName: accountByBinding[call.StoreStaffBindingID],
			RequestID: strings.TrimSpace(call.GatewayRequestID), Status: billingReconcileLocalOnly,
			LocalAt: &call.StartedAt, LocalTokens: call.ExternalPromptTokens + call.ExternalCompletionTokens,
		}
		if event, ok := eventByRequest[billingRequestKey(call.StoreID, call.StoreStaffBindingID, call.GatewayRequestID)]; ok {
			item.LocalModel = event.Model
			item.LocalTokens = event.PromptTokens + event.CompletionTokens
		}
		if len(ret.Items) < limit {
			ret.Items = append(ret.Items, item)
		} else {
			ret.Truncated = true
		}
	}
	denominator := ret.MatchedCount + ret.OfficialOnlyCount + ret.LocalOnlyCount
	if denominator > 0 {
		ret.MatchRate = float64(ret.MatchedCount) / float64(denominator)
	}
	sort.SliceStable(ret.Items, func(i, j int) bool {
		left := billingReconciliationTime(ret.Items[i])
		right := billingReconciliationTime(ret.Items[j])
		return left.After(right)
	})
	_ = tenantByID
	return ret
}

func resolveBillingAccessScope(operator *dto.AuthPrincipal) (billingAccessScope, error) {
	if operator == nil {
		return billingAccessScope{}, errorsx.Unauthorized("未登录或登录已过期")
	}
	if slices.Contains(operator.Roles, constants.RoleCodeStoreStaff) {
		snapshot, err := StoreWorkbenchService.Current(operator)
		if err != nil {
			return billingAccessScope{}, err
		}
		if snapshot.Binding == nil || snapshot.Store == nil {
			return billingAccessScope{}, errorsx.Forbidden("当前账号尚未绑定门店")
		}
		return billingAccessScope{Mode: "store", TenantID: snapshot.TenantID, StoreID: snapshot.Store.ID, StoreStaffBindingID: snapshot.Binding.ID}, nil
	}
	if operator.IsPlatformAccount {
		return billingAccessScope{Mode: "platform", TenantID: operator.ActiveTenantID, Platform: true}, nil
	}
	tenantID := operator.ActiveTenantID
	if tenantID <= 0 {
		tenantID = operator.TenantID
	}
	if tenantID <= 0 {
		return billingAccessScope{}, errorsx.Forbidden("请先进入接入公司")
	}
	return billingAccessScope{Mode: "tenant", TenantID: tenantID}, nil
}

func requireBillingPermission(operator *dto.AuthPrincipal, permissionCode string) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if !slices.Contains(operator.Permissions, permissionCode) {
		return errorsx.Forbidden("无权限查看模型用量与账单")
	}
	return nil
}

func parseBillingDateRange(startDate, endDate string) (billingDateRange, error) {
	startDate = strings.TrimSpace(startDate)
	endDate = strings.TrimSpace(endDate)
	if startDate == "" || endDate == "" {
		return billingDateRange{}, errorsx.InvalidParam("开始日期和结束日期必须同时填写")
	}
	location, err := time.LoadLocation(billingBusinessTimezone)
	if err != nil {
		location = time.FixedZone(billingBusinessTimezone, 8*60*60)
	}
	start, err := time.ParseInLocation(time.DateOnly, startDate, location)
	if err != nil {
		return billingDateRange{}, errorsx.InvalidParam("开始日期格式不正确")
	}
	end, err := time.ParseInLocation(time.DateOnly, endDate, location)
	if err != nil {
		return billingDateRange{}, errorsx.InvalidParam("结束日期格式不正确")
	}
	if end.Before(start) {
		return billingDateRange{}, errorsx.InvalidParam("结束日期不能早于开始日期")
	}
	endExclusive := end.AddDate(0, 0, 1)
	if endExclusive.Sub(start) > 366*24*time.Hour {
		return billingDateRange{}, errorsx.InvalidParam("单次查询日期范围不能超过 366 天")
	}
	return billingDateRange{
		StartDate: startDate, EndDate: endDate, StartAt: start, EndExclusive: endExclusive,
		StartTimestamp: start.Unix(), EndTimestamp: endExclusive.Unix() - 1,
	}, nil
}

func failedBillingOfficialResult(target billingStoreTarget, class, message string) billingOfficialResult {
	return billingOfficialResult{Store: response.BillingOfficialStoreResponse{
		TenantID: target.Store.TenantID, TenantName: billingTenantName(target.Tenant),
		StoreID: target.Store.ID, StoreCode: target.Store.StoreCode, StoreName: target.Store.Name,
		StoreStaffBindingID: target.Binding.ID, StoreStaffAccountName: target.BindingAccountName,
		Status: billingOfficialStatusFailed, ErrorClass: class, ErrorMessage: message,
		ModelNames: []string{}, Logs: []response.BillingOfficialUsageLogResponse{},
	}}
}

func buildOfficialUsageLog(target billingStoreTarget, item newapi.TokenUsageLog, settings *newapi.TokenBillingSettings) response.BillingOfficialUsageLogResponse {
	return response.BillingOfficialUsageLogResponse{
		StoreID: target.Store.ID, StoreName: target.Store.Name, StoreStaffBindingID: target.Binding.ID, StoreStaffAccountName: target.BindingAccountName,
		ID: item.ID, CreatedAt: item.CreatedAt,
		ModelName: strings.TrimSpace(item.ModelName), PromptTokens: item.PromptTokens, CompletionTokens: item.CompletionTokens,
		UseTime: item.UseTime, Quota: item.Quota, CostCNY: billingQuotaCNY(item.Quota, settings), RequestID: strings.TrimSpace(item.RequestID),
	}
}

func buildBillingLocalEvent(item models.AIUsageEvent, tenantByID map[int64]models.Tenant, storeByID map[int64]models.Store, accountByBinding map[int64]string) response.BillingLocalUsageEventResponse {
	requestID := firstNonBlank(item.GatewayRequestID, item.UpstreamRequestID, item.RequestID)
	return response.BillingLocalUsageEventResponse{
		ID: item.ID, TenantID: item.TenantID, TenantName: billingTenantName(tenantByID[item.TenantID]),
		StoreID: item.StoreID, StoreName: storeByID[item.StoreID].Name, RequestID: requestID,
		StoreStaffBindingID: item.StoreStaffBindingID, StoreStaffAccountName: accountByBinding[item.StoreStaffBindingID],
		Stage: item.Stage, OperationType: item.OperationType, ModelName: item.Model,
		ModelProfileRevision: item.ModelProfileRevision, UsageSlot: item.UsageSlot, CredentialRevision: item.CredentialRevision,
		PromptTokens: item.PromptTokens, CompletionTokens: item.CompletionTokens, CachedPromptTokens: item.CachedPromptTokens,
		LatencyMS: item.LatencyMS, Status: item.Status, ErrorClass: item.ErrorClass, CreatedAt: item.CreatedAt,
	}
}

func trimOfficialDetails(results []billingOfficialResult, limit int) {
	type indexedLog struct {
		StoreIndex int
		Log        response.BillingOfficialUsageLogResponse
	}
	all := make([]indexedLog, 0)
	for i := range results {
		for _, log := range results[i].Store.Logs {
			all = append(all, indexedLog{StoreIndex: i, Log: log})
		}
		results[i].Store.Logs = []response.BillingOfficialUsageLogResponse{}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Log.CreatedAt > all[j].Log.CreatedAt })
	if len(all) > limit {
		for _, item := range all[limit:] {
			results[item.StoreIndex].Store.Truncated = true
		}
		all = all[:limit]
	}
	for _, item := range all {
		results[item.StoreIndex].Store.Logs = append(results[item.StoreIndex].Store.Logs, item.Log)
	}
}

func billingQuotaCNY(quota int64, settings *newapi.TokenBillingSettings) float64 {
	if settings == nil || settings.QuotaPerUnit <= 0 || settings.USDExchangeRate <= 0 {
		return 0
	}
	return float64(quota) / settings.QuotaPerUnit * settings.USDExchangeRate
}

func billingProfileModelNames(templateID int64) []string {
	if templateID <= 0 {
		return []string{}
	}
	return billingModelNamesFromSlots(repositories.ModelProfileSlotRepository.FindByTemplateID(sqls.DB(), templateID))
}

func billingModelNamesFromSlots(slots []models.ModelProfileSlot) []string {
	seen := make(map[string]struct{})
	ret := make([]string, 0, len(slots))
	for i := range slots {
		name := strings.TrimSpace(slots[i].ModelName)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ret = append(ret, name)
	}
	sort.Strings(ret)
	return ret
}

func billingTenantName(tenant models.Tenant) string {
	if name := strings.TrimSpace(tenant.ShortName); name != "" {
		return name
	}
	return strings.TrimSpace(tenant.LegalName)
}

func normalizeBillingIDs(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	ret := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i] < ret[j] })
	return ret
}

func billingRequestKey(storeID, bindingID int64, requestID string) string {
	return fmt.Sprintf("%d:%d:%s", storeID, bindingID, strings.TrimSpace(requestID))
}

func billingReconciliationTime(item response.BillingReconciliationItemResponse) time.Time {
	if item.OfficialAt != nil {
		return *item.OfficialAt
	}
	if item.LocalAt != nil {
		return *item.LocalAt
	}
	return time.Time{}
}
