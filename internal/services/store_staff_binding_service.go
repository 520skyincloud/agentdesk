package services

import (
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var StoreStaffBindingService = newStoreStaffBindingService()

func newStoreStaffBindingService() *storeStaffBindingService { return &storeStaffBindingService{} }

type storeStaffBindingService struct{}

type StoreStaffRuntimeConfig struct {
	BindingID               int64
	TenantID                int64
	UserID                  int64
	AgentTeamID             int64
	CompanyID               int64
	StoreID                 int64
	ManagedMode             string
	ServiceHours            string
	StoreRoomConversationID string
	StoreRoomNotifyEnabled  bool
	StoreRoomAtList         string
	FallbackToHQ            bool
	ManualTimeoutMinutes    int
	FromLegacyInstance      bool
	NoWxWorkInstance        bool
}

type StoreStaffUserAssignment struct {
	BindingID          int64
	TenantID           int64
	UserID             int64
	CompanyID          int64
	CompanyName        string
	StoreID            int64
	StoreName          string
	WxWorkInstanceID   int64
	WxWorkEmployeeName string
	WxWorkEmployeeID   string
	AgentTeamID        int64
	AgentTeamName      string
}

func (s *storeStaffBindingService) Get(id int64) *models.StoreStaffBinding {
	if id <= 0 {
		return nil
	}
	return repositories.StoreStaffBindingRepository.Get(sqls.DB(), id)
}

func (s *storeStaffBindingService) GetInTenant(id, tenantID int64) *models.StoreStaffBinding {
	return repositories.StoreStaffBindingRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *storeStaffBindingService) Take(where ...any) *models.StoreStaffBinding {
	return repositories.StoreStaffBindingRepository.Take(sqls.DB(), where...)
}

func (s *storeStaffBindingService) FindUserAssignments(userIDs []int64, tenantID int64) map[int64]StoreStaffUserAssignment {
	result := make(map[int64]StoreStaffUserAssignment)
	userIDs = uniquePositive(userIDs)
	if len(userIDs) == 0 || tenantID <= 0 {
		return result
	}
	bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().In("user_id", userIDs).Eq("tenant_id", tenantID).Where("status <> ?", enums.StatusDeleted).Asc("id"))
	for i := range bindings {
		binding := &bindings[i]
		if binding.UserID <= 0 {
			continue
		}
		if _, exists := result[binding.UserID]; exists {
			continue
		}
		assignment := StoreStaffUserAssignment{
			BindingID:   binding.ID,
			TenantID:    binding.TenantID,
			UserID:      binding.UserID,
			CompanyID:   binding.CompanyID,
			StoreID:     binding.StoreID,
			AgentTeamID: binding.AgentTeamID,
		}
		if store := repositories.StoreRepository.GetInTenant(sqls.DB(), binding.StoreID, tenantID); store != nil && store.Status != enums.StatusDeleted {
			assignment.StoreName = store.Name
			if assignment.CompanyID <= 0 {
				assignment.CompanyID = store.CompanyID
			}
		}
		if company := repositories.CompanyRepository.GetInTenant(sqls.DB(), assignment.CompanyID, tenantID); company != nil && company.Status != enums.StatusDeleted {
			assignment.CompanyName = company.Name
		}
		if team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), binding.AgentTeamID, tenantID); team != nil && team.Status != enums.StatusDeleted {
			assignment.AgentTeamName = team.Name
		}
		instance := repositories.WxWorkProtocolInstanceRepository.Take(sqls.DB(), "tenant_id = ? AND store_staff_binding_id = ? AND status <> ?", tenantID, binding.ID, enums.StatusDeleted)
		if instance == nil && binding.StoreID > 0 {
			instance = repositories.WxWorkProtocolInstanceRepository.Take(sqls.DB(), "tenant_id = ? AND store_id = ? AND status <> ?", tenantID, binding.StoreID, enums.StatusDeleted)
		}
		if instance != nil {
			assignment.WxWorkInstanceID = instance.ID
			assignment.WxWorkEmployeeName = instance.EmployeeName
			assignment.WxWorkEmployeeID = instance.EmployeeUserID
		}
		result[binding.UserID] = assignment
	}
	return result
}

func (s *storeStaffBindingService) ResolveForInstance(instance *models.WxWorkProtocolInstance) StoreStaffRuntimeConfig {
	if instance == nil {
		return StoreStaffRuntimeConfig{ManagedMode: constants.StoreManagedModeSemi, FallbackToHQ: true, ManualTimeoutMinutes: 10}
	}
	if instance.TenantID > 0 && instance.StoreStaffBindingID > 0 {
		if binding := s.GetInTenant(instance.StoreStaffBindingID, instance.TenantID); binding != nil && binding.Status != enums.StatusDeleted {
			return s.runtimeConfigFromBinding(binding)
		}
	}
	if instance.TenantID > 0 && instance.StoreID > 0 {
		if binding := repositories.StoreStaffBindingRepository.TakeInTenant(sqls.DB(), instance.TenantID, "store_id = ? AND status <> ?", instance.StoreID, enums.StatusDeleted); binding != nil {
			return s.runtimeConfigFromBinding(binding)
		}
	}
	return StoreStaffRuntimeConfig{
		TenantID:                instance.TenantID,
		StoreID:                 instance.StoreID,
		AgentTeamID:             instance.AgentTeamID,
		ManagedMode:             constants.StoreManagedModeSemi,
		ServiceHours:            strings.TrimSpace(instance.ServiceHours),
		StoreRoomConversationID: strings.TrimSpace(instance.StoreRoomConversationID),
		StoreRoomNotifyEnabled:  instance.StoreRoomNotifyEnabled,
		StoreRoomAtList:         strings.TrimSpace(instance.StoreRoomAtList),
		FallbackToHQ:            instance.FallbackToHQ,
		ManualTimeoutMinutes:    normalizeManualTimeoutMinutes(instance.ManualTimeoutMinutes),
		FromLegacyInstance:      true,
	}
}

func (s *storeStaffBindingService) runtimeConfigFromBinding(binding *models.StoreStaffBinding) StoreStaffRuntimeConfig {
	mode := strings.TrimSpace(binding.ManagedMode)
	if mode != constants.StoreManagedModeFull && mode != constants.StoreManagedModeSemi && mode != constants.StoreManagedModeNone {
		mode = constants.StoreManagedModeSemi
	}
	return StoreStaffRuntimeConfig{
		BindingID:               binding.ID,
		TenantID:                binding.TenantID,
		UserID:                  binding.UserID,
		AgentTeamID:             binding.AgentTeamID,
		CompanyID:               binding.CompanyID,
		StoreID:                 binding.StoreID,
		ManagedMode:             mode,
		ServiceHours:            strings.TrimSpace(binding.ServiceHours),
		StoreRoomConversationID: strings.TrimSpace(binding.StoreRoomConversationID),
		StoreRoomNotifyEnabled:  binding.StoreRoomNotifyEnabled,
		StoreRoomAtList:         strings.TrimSpace(binding.StoreRoomAtList),
		FallbackToHQ:            binding.FallbackToHQ,
		ManualTimeoutMinutes:    normalizeManualTimeoutMinutes(binding.ManualTimeoutMinutes),
	}
}

func (s *storeStaffBindingService) EnsureForInstance(instance *models.WxWorkProtocolInstance, operator *dto.AuthPrincipal) (*models.StoreStaffBinding, error) {
	if instance == nil || instance.StoreID <= 0 {
		return nil, errorsx.InvalidParam("员工号未绑定门店")
	}
	if instance.TenantID <= 0 {
		return nil, errorsx.InvalidParam("员工号缺少接入公司归属")
	}
	if operator != nil && operator.ActiveTenantID > 0 && operator.ActiveTenantID != instance.TenantID {
		return nil, errorsx.Forbidden("无权管理其他接入公司的门店员工绑定")
	}
	if existing := repositories.StoreStaffBindingRepository.TakeInTenant(sqls.DB(), instance.TenantID, "store_id = ? AND status <> ?", instance.StoreID, enums.StatusDeleted); existing != nil {
		if instance.StoreStaffBindingID != existing.ID || instance.AgentTeamID != existing.AgentTeamID {
			if err := repositories.WxWorkProtocolInstanceRepository.UpdatesInTenant(sqls.DB(), instance.ID, instance.TenantID, map[string]any{
				"store_staff_binding_id": existing.ID,
				"agent_team_id":          existing.AgentTeamID,
				"updated_at":             time.Now(),
			}); err != nil {
				return nil, err
			}
		}
		return existing, nil
	}
	store := StoreService.GetInTenant(instance.StoreID, instance.TenantID)
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("员工号绑定的门店不存在或不属于当前接入公司")
	}
	companyID := int64(0)
	companyID = store.CompanyID
	item := &models.StoreStaffBinding{
		TenantID:                instance.TenantID,
		AgentTeamID:             instance.AgentTeamID,
		CompanyID:               companyID,
		StoreID:                 instance.StoreID,
		ManagedMode:             constants.StoreManagedModeSemi,
		ServiceHours:            strings.TrimSpace(instance.ServiceHours),
		StoreRoomConversationID: strings.TrimSpace(instance.StoreRoomConversationID),
		StoreRoomNotifyEnabled:  instance.StoreRoomNotifyEnabled,
		StoreRoomAtList:         strings.TrimSpace(instance.StoreRoomAtList),
		FallbackToHQ:            instance.FallbackToHQ,
		ManualTimeoutMinutes:    normalizeManualTimeoutMinutes(instance.ManualTimeoutMinutes),
		Status:                  enums.StatusOk,
		AuditFields:             utils.BuildAuditFields(operator),
	}
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.StoreStaffBindingRepository.Create(ctx.Tx, item); err != nil {
			return err
		}
		return repositories.WxWorkProtocolInstanceRepository.UpdatesInTenant(ctx.Tx, instance.ID, instance.TenantID, map[string]any{
			"store_staff_binding_id": item.ID,
			"updated_at":             time.Now(),
		})
	}); err != nil {
		return nil, err
	}
	return item, nil
}
