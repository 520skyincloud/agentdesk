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
	UserID                  int64
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

func (s *storeStaffBindingService) Get(id int64) *models.StoreStaffBinding {
	if id <= 0 {
		return nil
	}
	return repositories.StoreStaffBindingRepository.Get(sqls.DB(), id)
}

func (s *storeStaffBindingService) Take(where ...any) *models.StoreStaffBinding {
	return repositories.StoreStaffBindingRepository.Take(sqls.DB(), where...)
}

func (s *storeStaffBindingService) ResolveForInstance(instance *models.WxWorkProtocolInstance) StoreStaffRuntimeConfig {
	if instance == nil {
		return StoreStaffRuntimeConfig{ManagedMode: constants.StoreManagedModeSemi, FallbackToHQ: true, ManualTimeoutMinutes: 10}
	}
	if instance.StoreStaffBindingID > 0 {
		if binding := s.Get(instance.StoreStaffBindingID); binding != nil && binding.Status != enums.StatusDeleted {
			return s.runtimeConfigFromBinding(binding)
		}
	}
	if instance.StoreID > 0 {
		if binding := s.Take("store_id = ? AND status <> ?", instance.StoreID, enums.StatusDeleted); binding != nil {
			return s.runtimeConfigFromBinding(binding)
		}
	}
	return StoreStaffRuntimeConfig{
		StoreID:                 instance.StoreID,
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
		UserID:                  binding.UserID,
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
	if existing := s.Take("store_id = ? AND status <> ?", instance.StoreID, enums.StatusDeleted); existing != nil {
		if instance.StoreStaffBindingID != existing.ID {
			_ = repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instance.ID, map[string]any{"store_staff_binding_id": existing.ID, "updated_at": time.Now()})
		}
		return existing, nil
	}
	store := StoreService.Get(instance.StoreID)
	companyID := int64(0)
	if store != nil {
		companyID = store.CompanyID
	}
	item := &models.StoreStaffBinding{
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
	if err := repositories.StoreStaffBindingRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	_ = repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instance.ID, map[string]any{"store_staff_binding_id": item.ID, "updated_at": time.Now()})
	return item, nil
}
