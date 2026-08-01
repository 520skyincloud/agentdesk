package services

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var StoreService = newStoreService()

func newStoreService() *storeService {
	return &storeService{}
}

type storeService struct{}

func (s *storeService) Get(id int64) *models.Store {
	if id <= 0 {
		return nil
	}
	return repositories.StoreRepository.Get(sqls.DB(), id)
}

func (s *storeService) GetInTenant(id, tenantID int64) *models.Store {
	return repositories.StoreRepository.GetInTenant(sqls.DB(), id, tenantID)
}

// HydrateRuntimeInstanceDB returns an in-memory copy whose Store-owned facts
// come from the independent Store record. The instance itself is never updated.
func (s *storeService) HydrateRuntimeInstanceDB(
	db *gorm.DB,
	instance *models.WxWorkProtocolInstance,
) (*models.WxWorkProtocolInstance, error) {
	if db == nil || instance == nil || instance.TenantID <= 0 || instance.StoreID <= 0 {
		return nil, fmt.Errorf("企微实例缺少有效门店范围")
	}
	store := repositories.StoreRepository.GetInTenant(db, instance.StoreID, instance.TenantID)
	if store == nil || store.Status != enums.StatusOk {
		return nil, fmt.Errorf("企微实例所属门店不存在或已停用")
	}
	runtimeInstance := *instance
	runtimeInstance.StoreAddress = store.Address
	runtimeInstance.StoreNavigationName = store.NavigationName
	runtimeInstance.StoreLongitude = store.Longitude
	runtimeInstance.StoreLatitude = store.Latitude
	runtimeInstance.StoreMapProvider = store.MapProvider
	runtimeInstance.StoreContactPhone = store.ContactPhone
	runtimeInstance.KnowledgeBaseID = store.KnowledgeBaseID
	return &runtimeInstance, nil
}

func (s *storeService) Take(where ...any) *models.Store {
	return repositories.StoreRepository.Take(sqls.DB(), where...)
}

func (s *storeService) FindPageByCnd(cnd *sqls.Cnd) ([]models.Store, *sqls.Paging) {
	return repositories.StoreRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *storeService) ListActiveOptions(tenantID int64) []models.Store {
	if tenantID <= 0 {
		return []models.Store{}
	}
	return repositories.StoreRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("status", enums.StatusOk).
		Asc("name").Asc("id"))
}

func (s *storeService) RelationCounts(tenantID int64, stores []models.Store) (map[int64]int64, map[int64]int64) {
	storeIDs := make([]int64, 0, len(stores))
	for i := range stores {
		storeIDs = append(storeIDs, stores[i].ID)
	}
	return repositories.StoreRepository.CountActiveBindingsByStoreIDs(sqls.DB(), tenantID, storeIDs),
		repositories.StoreRepository.CountCurrentInstancesByStoreIDs(sqls.DB(), tenantID, storeIDs)
}

func (s *storeService) Create(req request.CreateStoreRequest, operator *dto.AuthPrincipal) (*models.Store, error) {
	if operator == nil || operator.ActiveTenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理门店的接入公司")
	}
	values, err := normalizeStoreRequest(req)
	if err != nil {
		return nil, err
	}
	var store *models.Store
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		now := time.Now()
		store = &models.Store{
			TenantID:       operator.ActiveTenantID,
			StoreCode:      generateStoreIdentityCode(operator.ActiveTenantID),
			Name:           values.Name,
			BrandName:      values.BrandName,
			Address:        values.Address,
			NavigationName: values.NavigationName,
			Longitude:      values.Longitude,
			Latitude:       values.Latitude,
			MapProvider:    values.MapProvider,
			ContactPhone:   values.ContactPhone,
			Status:         enums.StatusOk,
			Remark:         values.Remark,
			AuditFields:    utils.BuildAuditFields(operator),
		}
		store.CreatedAt = now
		store.UpdatedAt = now
		if err := repositories.StoreRepository.Create(ctx.Tx, store); err != nil {
			return err
		}
		if err := StoreModelCredentialService.EnsureStoreRecordsDB(ctx.Tx, store, operator); err != nil {
			return err
		}
		return CustomerTagRuntimePolicyService.EnsureStorePolicyDB(ctx.Tx, store, operator)
	})
	return store, err
}

func (s *storeService) Update(req request.UpdateStoreRequest, operator *dto.AuthPrincipal) (*models.Store, error) {
	if operator == nil || operator.ActiveTenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理门店的接入公司")
	}
	if req.ID <= 0 {
		return nil, errorsx.InvalidParam("请选择需要更新的门店")
	}
	values, err := normalizeStoreRequest(req.CreateStoreRequest)
	if err != nil {
		return nil, err
	}
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), req.ID, operator.ActiveTenantID)
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在")
	}
	err = repositories.StoreRepository.UpdatesInTenant(sqls.DB(), store.ID, store.TenantID, map[string]any{
		"name":             values.Name,
		"brand_name":       values.BrandName,
		"address":          values.Address,
		"navigation_name":  values.NavigationName,
		"longitude":        values.Longitude,
		"latitude":         values.Latitude,
		"map_provider":     values.MapProvider,
		"contact_phone":    values.ContactPhone,
		"remark":           values.Remark,
		"updated_at":       time.Now(),
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
	})
	if err != nil {
		return nil, err
	}
	return repositories.StoreRepository.GetInTenant(sqls.DB(), store.ID, store.TenantID), nil
}

func (s *storeService) UpdateStatus(req request.UpdateStoreStatusRequest, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.ActiveTenantID <= 0 {
		return errorsx.Forbidden("请先进入需要管理门店的接入公司")
	}
	status := enums.Status(req.Status)
	if status != enums.StatusOk && status != enums.StatusDisabled {
		return errorsx.InvalidParam("门店状态仅支持启用或停用")
	}
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), req.ID, operator.ActiveTenantID)
	if store == nil || store.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("门店不存在")
	}
	return repositories.StoreRepository.UpdatesInTenant(sqls.DB(), store.ID, store.TenantID, map[string]any{
		"status":           status,
		"updated_at":       time.Now(),
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
	})
}

func normalizeStoreRequest(req request.CreateStoreRequest) (request.CreateStoreRequest, error) {
	req.Name = utils.RepairMojibakeText(strings.TrimSpace(req.Name))
	req.BrandName = utils.RepairMojibakeText(strings.TrimSpace(req.BrandName))
	req.Address = utils.RepairMojibakeText(strings.TrimSpace(req.Address))
	req.NavigationName = utils.RepairMojibakeText(strings.TrimSpace(req.NavigationName))
	req.MapProvider = strings.TrimSpace(req.MapProvider)
	req.ContactPhone = strings.TrimSpace(req.ContactPhone)
	req.Remark = strings.TrimSpace(req.Remark)
	if req.Name == "" {
		return req, errorsx.InvalidParam("门店名称不能为空")
	}
	if len(req.Name) > 120 || len(req.BrandName) > 120 || len(req.Address) > 500 || len(req.NavigationName) > 200 || len(req.MapProvider) > 50 || len(req.ContactPhone) > 120 {
		return req, errorsx.InvalidParam("门店资料超过允许长度")
	}
	longitude, latitude, err := normalizeStoreWorkbenchCoordinates(req.Longitude, req.Latitude)
	if err != nil {
		return req, err
	}
	req.Longitude = longitude
	req.Latitude = latitude
	return req, nil
}
