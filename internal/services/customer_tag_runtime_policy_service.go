package services

import (
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	customerTagPolicyMinQuietMinutes = 1
	customerTagPolicyMaxQuietMinutes = 30 * 24 * 60
	customerTagPolicyMinConfidence   = 0.8
	customerTagPolicyMaxOperations   = 6
)

var CustomerTagRuntimePolicyService = &customerTagRuntimePolicyService{}

type customerTagRuntimePolicyService struct{}

type CustomerTagRuntimePolicyListFilter struct {
	Page             int
	Limit            int
	Keyword          string
	StoreStatus      *enums.Status
	EvolutionEnabled *bool
	ReplyEnabled     *bool
}

func (s *customerTagRuntimePolicyService) GetPolicy(operator *dto.AuthPrincipal) (*response.CustomerTagPolicyResponse, error) {
	tenantID, err := requireActiveTenantID(operator, "客户标签策略")
	if err != nil {
		return nil, err
	}
	profile, err := TenantIndustryService.ResolveTenantProfileDB(sqls.DB(), tenantID)
	if err != nil {
		return nil, err
	}
	policy := repositories.TenantCustomerTagPolicyRepository.GetByTenant(sqls.DB(), tenantID)
	if policy == nil || policy.Status != enums.StatusOk || policy.IntentProfileID != profile.ID {
		return nil, errorsx.InvalidParam("当前公司的客户标签策略尚未初始化")
	}
	return buildCustomerTagPolicyResponse(policy, profile), nil
}

func (s *customerTagRuntimePolicyService) ListStorePolicies(
	filter CustomerTagRuntimePolicyListFilter,
	operator *dto.AuthPrincipal,
) ([]response.StoreCustomerTagRuntimePolicyResponse, *sqls.Paging, error) {
	tenantID, err := requireActiveTenantID(operator, "客户标签策略")
	if err != nil {
		return nil, nil, err
	}
	if _, err := TenantIndustryService.ResolveTenantProfileDB(sqls.DB(), tenantID); err != nil {
		return nil, nil, err
	}
	rows, total, err := repositories.StoreCustomerTagRuntimePolicyRepository.FindStorePage(
		sqls.DB(), tenantID, repositories.StoreCustomerTagRuntimePolicyListFilter{
			Page: filter.Page, Limit: filter.Limit, Keyword: filter.Keyword,
			StoreStatus: filter.StoreStatus, EvolutionEnabled: filter.EvolutionEnabled, ReplyEnabled: filter.ReplyEnabled,
		},
	)
	if err != nil {
		return nil, nil, err
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.Limit < 1 {
		filter.Limit = 20
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	ret := make([]response.StoreCustomerTagRuntimePolicyResponse, 0, len(rows))
	for i := range rows {
		row := rows[i]
		ret = append(ret, response.StoreCustomerTagRuntimePolicyResponse{
			StoreID: row.StoreID, StoreCode: row.StoreCode, StoreName: utils.RepairMojibakeText(row.StoreName),
			StoreStatus: int(row.StoreStatus), PolicyReady: row.PolicyID > 0 && row.PolicyStatus == enums.StatusOk,
			CustomerTagEvolutionEnabled: row.PolicyID > 0 && row.PolicyStatus == enums.StatusOk && row.CustomerTagEvolutionEnabled,
			ReplyTagContextEnabled:      row.PolicyID > 0 && row.PolicyStatus == enums.StatusOk && row.ReplyTagContextEnabled,
			UpdatedAt:                   utils.FormatTimePtr(row.UpdatedAt),
		})
	}
	return ret, &sqls.Paging{Page: filter.Page, Limit: filter.Limit, Total: total}, nil
}

func (s *customerTagRuntimePolicyService) UpdatePolicy(req request.UpdateCustomerTagPolicyRequest, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "客户标签策略")
	if err != nil {
		return err
	}
	if err := validateCustomerTagPolicyRequest(req); err != nil {
		return err
	}
	policyChanged := false
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		profile, err := TenantIndustryService.ResolveTenantProfileDB(ctx.Tx, tenantID)
		if err != nil {
			return err
		}
		policy, err := repositories.TenantCustomerTagPolicyRepository.GetByTenantForUpdate(ctx.Tx, tenantID)
		if err != nil {
			return err
		}
		if policy == nil || policy.Status != enums.StatusOk || policy.IntentProfileID != profile.ID {
			return errorsx.InvalidParam("当前公司的客户标签策略尚未初始化")
		}
		quietChanged := policy.QuietPeriodMinutes != req.QuietPeriodMinutes
		now := time.Now()
		if err := repositories.TenantCustomerTagPolicyRepository.UpdatesByTenant(ctx.Tx, tenantID, map[string]any{
			"quiet_period_minutes":              req.QuietPeriodMinutes,
			"minimum_confidence":                req.MinimumConfidence,
			"max_operations_per_run":            req.MaxOperationsPerRun,
			"evolution_default_enabled":         req.EvolutionDefaultEnabled,
			"reply_tag_context_default_enabled": req.ReplyTagContextDefaultEnabled,
			"update_user_id":                    operator.UserID,
			"update_user_name":                  operator.Username,
			"updated_at":                        now,
		}); err != nil {
			return err
		}
		if quietChanged {
			if err := repositories.ConversationEvolutionStateRepository.RequeuePendingByTenant(ctx.Tx, tenantID, now); err != nil {
				return err
			}
		}
		policyChanged = true
		return nil
	})
	if err == nil && policyChanged {
		WsService.PublishCustomerTagRuntimePolicyChanged(tenantID, nil, false, true, nil, nil, time.Now())
	}
	return err
}

func (s *customerTagRuntimePolicyService) BatchToggle(
	req request.BatchToggleCustomerTagRuntimeRequest,
	operator *dto.AuthPrincipal,
) ([]int64, error) {
	tenantID, err := requireActiveTenantID(operator, "客户标签策略")
	if err != nil {
		return nil, err
	}
	if req.CustomerTagEvolutionEnabled == nil && req.ReplyTagContextEnabled == nil {
		return nil, errorsx.InvalidParam("请至少选择一个需要调整的客户标签能力")
	}
	storeIDs := uniquePositive(req.StoreIDs)
	if req.AllStores && len(storeIDs) > 0 {
		return nil, errorsx.InvalidParam("全部门店与指定门店不能同时提交")
	}
	if !req.AllStores && len(storeIDs) == 0 {
		return nil, errorsx.InvalidParam("请选择需要调整的门店")
	}
	if len(storeIDs) > 1000 {
		return nil, errorsx.InvalidParam("单次最多选择 1000 家门店")
	}
	affected := make([]int64, 0)
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		profile, err := TenantIndustryService.ResolveTenantProfileDB(ctx.Tx, tenantID)
		if err != nil {
			return err
		}
		tenantPolicy, err := repositories.TenantCustomerTagPolicyRepository.GetByTenantForUpdate(ctx.Tx, tenantID)
		if err != nil {
			return err
		}
		if tenantPolicy == nil || tenantPolicy.Status != enums.StatusOk || tenantPolicy.IntentProfileID != profile.ID {
			return errorsx.InvalidParam("当前公司的客户标签策略尚未初始化")
		}
		var stores []models.Store
		if req.AllStores {
			stores, err = repositories.StoreRepository.FindAllForUpdateInTenant(ctx.Tx, tenantID)
		} else {
			stores, err = repositories.StoreRepository.FindByIDsForUpdateInTenant(ctx.Tx, tenantID, storeIDs)
		}
		if err != nil {
			return err
		}
		if !req.AllStores && len(stores) != len(storeIDs) {
			return errorsx.InvalidParam("部分门店不存在、已删除或不属于当前公司")
		}
		if len(stores) == 0 {
			return nil
		}
		lockedIDs := make([]int64, 0, len(stores))
		for i := range stores {
			lockedIDs = append(lockedIDs, stores[i].ID)
		}
		existing, err := repositories.StoreCustomerTagRuntimePolicyRepository.FindByStores(ctx.Tx, tenantID, lockedIDs)
		if err != nil {
			return err
		}
		existingByStore := make(map[int64]models.StoreCustomerTagRuntimePolicy, len(existing))
		for i := range existing {
			existingByStore[existing[i].StoreID] = existing[i]
		}
		now := time.Now()
		items := make([]models.StoreCustomerTagRuntimePolicy, 0, len(stores))
		for i := range stores {
			store := stores[i]
			item, ok := existingByStore[store.ID]
			if !ok {
				item = models.StoreCustomerTagRuntimePolicy{
					TenantID: tenantID, StoreID: store.ID,
					CustomerTagEvolutionEnabled: tenantPolicy.EvolutionDefaultEnabled,
					ReplyTagContextEnabled:      tenantPolicy.ReplyTagContextDefaultEnabled,
					Status:                      enums.StatusOk, AuditFields: utils.BuildAuditFields(operator),
				}
				item.CreatedAt = now
			}
			if req.CustomerTagEvolutionEnabled != nil {
				item.CustomerTagEvolutionEnabled = *req.CustomerTagEvolutionEnabled
			}
			if req.ReplyTagContextEnabled != nil {
				item.ReplyTagContextEnabled = *req.ReplyTagContextEnabled
			}
			item.Status = enums.StatusOk
			item.UpdateUserID = operator.UserID
			item.UpdateUserName = operator.Username
			item.UpdatedAt = now
			items = append(items, item)
			affected = append(affected, store.ID)
		}
		columns := []string{"status", "update_user_id", "update_user_name", "updated_at"}
		if req.CustomerTagEvolutionEnabled != nil {
			columns = append(columns, "customer_tag_evolution_enabled")
		}
		if req.ReplyTagContextEnabled != nil {
			columns = append(columns, "reply_tag_context_enabled")
		}
		return repositories.StoreCustomerTagRuntimePolicyRepository.UpsertBatch(ctx.Tx, items, columns)
	})
	if err != nil {
		return nil, err
	}
	WsService.PublishCustomerTagRuntimePolicyChanged(
		tenantID, affected, req.AllStores, false,
		req.CustomerTagEvolutionEnabled, req.ReplyTagContextEnabled, time.Now(),
	)
	return affected, nil
}

func (s *customerTagRuntimePolicyService) EnsureStorePolicyDB(
	db *gorm.DB,
	store *models.Store,
	operator *dto.AuthPrincipal,
) error {
	if db == nil || store == nil || store.ID <= 0 || store.TenantID <= 0 {
		return errorsx.InvalidParam("门店客户标签策略范围无效")
	}
	current, err := repositories.StoreCustomerTagRuntimePolicyRepository.GetByStoreForUpdate(db, store.TenantID, store.ID)
	if err != nil || current != nil {
		return err
	}
	policy := repositories.TenantCustomerTagPolicyRepository.GetByTenant(db, store.TenantID)
	if policy == nil || policy.Status != enums.StatusOk {
		return errorsx.InvalidParam("门店所属公司的客户标签策略尚未初始化")
	}
	now := time.Now()
	item := &models.StoreCustomerTagRuntimePolicy{
		TenantID: store.TenantID, StoreID: store.ID,
		CustomerTagEvolutionEnabled: policy.EvolutionDefaultEnabled,
		ReplyTagContextEnabled:      policy.ReplyTagContextDefaultEnabled,
		Status:                      enums.StatusOk, AuditFields: utils.BuildAuditFields(operator),
	}
	item.CreatedAt = now
	item.UpdatedAt = now
	return repositories.StoreCustomerTagRuntimePolicyRepository.Create(db, item)
}

func validateCustomerTagPolicyRequest(req request.UpdateCustomerTagPolicyRequest) error {
	if req.QuietPeriodMinutes < customerTagPolicyMinQuietMinutes || req.QuietPeriodMinutes > customerTagPolicyMaxQuietMinutes {
		return errorsx.InvalidParam("静默时间必须在 1 分钟到 30 天之间")
	}
	if req.MinimumConfidence < customerTagPolicyMinConfidence || req.MinimumConfidence > 1 {
		return errorsx.InvalidParam("最低置信度必须在 0.8 到 1 之间")
	}
	if req.MaxOperationsPerRun < 1 || req.MaxOperationsPerRun > customerTagPolicyMaxOperations {
		return errorsx.InvalidParam("每轮标签操作上限必须在 1 到 6 之间")
	}
	return nil
}

func buildCustomerTagPolicyResponse(
	policy *models.TenantCustomerTagPolicy,
	profile *models.ReplyIntentProfile,
) *response.CustomerTagPolicyResponse {
	if policy == nil || profile == nil {
		return nil
	}
	return &response.CustomerTagPolicyResponse{
		TenantID: policy.TenantID, IntentProfileID: policy.IntentProfileID,
		IndustryName: utils.RepairMojibakeText(strings.TrimSpace(profile.Name)), IndustryCode: strings.TrimSpace(profile.IndustryCode),
		QuietPeriodMinutes: policy.QuietPeriodMinutes, MinimumConfidence: policy.MinimumConfidence,
		MaxOperationsPerRun:           policy.MaxOperationsPerRun,
		EvolutionDefaultEnabled:       policy.EvolutionDefaultEnabled,
		ReplyTagContextDefaultEnabled: policy.ReplyTagContextDefaultEnabled,
		UpdatedAt:                     utils.FormatTime(policy.UpdatedAt),
	}
}
