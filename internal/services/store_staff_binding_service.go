package services

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var StoreStaffBindingService = newStoreStaffBindingService()

func newStoreStaffBindingService() *storeStaffBindingService { return &storeStaffBindingService{} }

type storeStaffBindingService struct{}

type preparedStoreStaffBinding struct {
	User    *models.User
	Store   *models.Store
	Binding *models.StoreStaffBinding
}

type StoreStaffRuntimeConfig struct {
	BindingID               int64
	TenantID                int64
	UserID                  int64
	AgentTeamID             int64
	StoreID                 int64
	ManagedMode             string
	ServiceHours            string
	StoreRoomConversationID string
	StoreRoomNotifyEnabled  bool
	StoreRoomAtList         string
	FallbackToHQ            bool
	ManualTimeoutMinutes    int
	NoWxWorkInstance        bool
}

type StoreStaffUserAssignment struct {
	BindingID          int64
	TenantID           int64
	UserID             int64
	StoreID            int64
	StoreName          string
	WxWorkInstanceID   int64
	WxWorkEmployeeName string
	WxWorkEmployeeID   string
	WxWorkHealthStatus string
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

func (s *storeStaffBindingService) prepareForUserDB(db *gorm.DB, tenantID, userID, storeID int64, operator *dto.AuthPrincipal) (*preparedStoreStaffBinding, error) {
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理门店员工的接入公司")
	}
	if userID <= 0 {
		return nil, errorsx.InvalidParam("请选择已分配门店员工号角色的系统账号")
	}
	user, err := repositories.UserRepository.GetForUpdate(db, userID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.TenantID != tenantID || user.Status != enums.StatusOk || user.DeletedAt != nil {
		return nil, errorsx.InvalidParam("系统账号不存在、已停用或不属于当前接入公司")
	}
	role := repositories.RoleRepository.GetByCode(db, constants.RoleCodeStoreStaff)
	if role == nil || role.Status != enums.StatusOk || repositories.UserRoleRepository.FindOne(db, sqls.NewCnd().Eq("user_id", user.ID).Eq("role_id", role.ID)) == nil {
		return nil, errorsx.InvalidParam("所选账号尚未分配门店员工号角色")
	}

	bindings, err := repositories.StoreStaffBindingRepository.FindAllForUpdateByUserInTenant(db, tenantID, user.ID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var activeBinding *models.StoreStaffBinding
	var reusableBinding *models.StoreStaffBinding
	var historicalBinding *models.StoreStaffBinding
	for i := range bindings {
		binding := &bindings[i]
		if binding.Status == enums.StatusOk && binding.ActiveUserID != nil && *binding.ActiveUserID == user.ID {
			if activeBinding != nil {
				return nil, errorsx.InvalidParam("该账号存在多个活动门店绑定，请先修复历史数据")
			}
			activeBinding = binding
		}
		if storeID > 0 && binding.StoreID == storeID && binding.Status != enums.StatusDeleted {
			reusableBinding = binding
		}
		if binding.Status != enums.StatusDeleted {
			if historicalBinding != nil && historicalBinding.ID != binding.ID {
				historicalBinding = nil
			} else if historicalBinding == nil && len(bindings) == 1 {
				historicalBinding = binding
			}
		}
	}
	if storeID <= 0 {
		if activeBinding != nil {
			storeID = activeBinding.StoreID
		} else if historicalBinding != nil {
			storeID = historicalBinding.StoreID
			reusableBinding = historicalBinding
		} else {
			return nil, errorsx.InvalidParam("该门店员工号尚未选择门店，请先在用户管理完成绑定")
		}
	}
	store := repositories.StoreRepository.GetInTenant(db, storeID, tenantID)
	if store == nil || store.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("所选门店不存在或未启用")
	}
	if activeBinding != nil {
		if activeBinding.StoreID != storeID {
			return nil, errorsx.InvalidParam("该账号已绑定其他门店，请先停用原绑定后再分配")
		}
		if err := repositories.StoreStaffBindingRepository.UpdatesInTenant(db, activeBinding.ID, tenantID, map[string]any{
			"active_user_id":   user.ID,
			"status":           enums.StatusOk,
			"updated_at":       now,
			"update_user_id":   auditUserID(operator),
			"update_user_name": auditUsername(operator),
		}); err != nil {
			return nil, err
		}
		activeBinding.ActiveUserID = positiveInt64Pointer(user.ID)
		activeBinding.Status = enums.StatusOk
		if err := StoreModelCredentialService.EnsureBindingRecordDB(db, activeBinding, operator); err != nil {
			return nil, err
		}
		return &preparedStoreStaffBinding{User: user, Store: store, Binding: activeBinding}, nil
	}
	if reusableBinding != nil {
		binding := reusableBinding
		if err := repositories.StoreStaffBindingRepository.UpdatesInTenant(db, binding.ID, tenantID, map[string]any{
			"active_user_id":   user.ID,
			"status":           enums.StatusOk,
			"updated_at":       now,
			"update_user_id":   auditUserID(operator),
			"update_user_name": auditUsername(operator),
		}); err != nil {
			return nil, err
		}
		binding.ActiveUserID = positiveInt64Pointer(user.ID)
		binding.Status = enums.StatusOk
		if err := StoreModelCredentialService.EnsureBindingRecordDB(db, binding, operator); err != nil {
			return nil, err
		}
		return &preparedStoreStaffBinding{User: user, Store: store, Binding: binding}, nil
	}
	binding := &models.StoreStaffBinding{
		TenantID:             tenantID,
		UserID:               user.ID,
		ActiveUserID:         positiveInt64Pointer(user.ID),
		StoreID:              store.ID,
		ManagedMode:          constants.StoreManagedModeSemi,
		FallbackToHQ:         true,
		ManualTimeoutMinutes: DefaultManualTimeoutMinutes,
		Status:               enums.StatusOk,
		Remark:               "公司主管分配到既有门店的门店员工号角色账号",
		AuditFields:          utils.BuildAuditFields(operator),
	}
	binding.CreatedAt = now
	binding.UpdatedAt = now
	if err := repositories.StoreStaffBindingRepository.Create(db, binding); err != nil {
		return nil, err
	}
	if err := StoreModelCredentialService.EnsureBindingRecordDB(db, binding, operator); err != nil {
		return nil, err
	}
	return &preparedStoreStaffBinding{User: user, Store: store, Binding: binding}, nil
}

func (s *storeStaffBindingService) RetireForUserDB(db *gorm.DB, tenantID, userID int64, operator *dto.AuthPrincipal) error {
	bindings, err := repositories.StoreStaffBindingRepository.FindForUpdateByUserInTenant(db, tenantID, userID)
	if err != nil || len(bindings) == 0 {
		return err
	}
	now := time.Now()
	bindingIDs := make([]int64, 0, len(bindings))
	affectedTeamIDs := make([]int64, 0, len(bindings))
	for i := range bindings {
		binding := &bindings[i]
		bindingIDs = append(bindingIDs, binding.ID)
		affectedTeamIDs = appendPositive(affectedTeamIDs, binding.AgentTeamID)
		if err := repositories.StoreStaffBindingRepository.UpdatesInTenant(db, binding.ID, tenantID, map[string]any{
			"active_user_id":   nil,
			"status":           enums.StatusDisabled,
			"updated_at":       now,
			"update_user_id":   auditUserID(operator),
			"update_user_name": auditUsername(operator),
		}); err != nil {
			return err
		}
	}
	if err := repositories.WxWorkProtocolInstanceRepository.UpdatesActiveByStoreStaffBindingIDsInTenant(db, bindingIDs, tenantID, map[string]any{
		"status":           enums.StatusDisabled,
		"ai_reply_enabled": false,
		"health_status":    "identity_disabled",
		"updated_at":       now,
		"update_user_id":   auditUserID(operator),
		"update_user_name": auditUsername(operator),
	}); err != nil {
		return err
	}
	for _, teamID := range uniquePositive(affectedTeamIDs) {
		if err := AgentTeamService.syncTeamScopeFromAssignments(db, teamID, operator); err != nil {
			return err
		}
	}
	return nil
}

func generateStoreIdentityCode(tenantID int64) string {
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	if len(suffix) > 12 {
		suffix = suffix[:12]
	}
	return fmt.Sprintf("store-%d-%s", tenantID, suffix)
}

func (s *storeStaffBindingService) FindUserAssignments(userIDs []int64, tenantID int64) map[int64]StoreStaffUserAssignment {
	result := make(map[int64]StoreStaffUserAssignment)
	userIDs = uniquePositive(userIDs)
	if len(userIDs) == 0 || tenantID <= 0 {
		return result
	}
	bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().In("user_id", userIDs).Eq("tenant_id", tenantID).Eq("status", enums.StatusOk).Asc("id"))
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
			StoreID:     binding.StoreID,
			AgentTeamID: binding.AgentTeamID,
		}
		if store := repositories.StoreRepository.GetInTenant(sqls.DB(), binding.StoreID, tenantID); store != nil && store.Status != enums.StatusDeleted {
			assignment.StoreName = store.Name
		}
		if team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), binding.AgentTeamID, tenantID); team != nil && team.Status != enums.StatusDeleted {
			assignment.AgentTeamName = team.Name
		}
		instance, instanceErr := WxWorkProtocolInstanceService.bindingInstanceReservationDB(sqls.DB(), tenantID, binding.ID)
		if instanceErr != nil {
			assignment.WxWorkHealthStatus = "integrity_error"
		}
		if instance != nil {
			assignment.WxWorkInstanceID = instance.ID
			assignment.WxWorkEmployeeName = instance.EmployeeName
			assignment.WxWorkEmployeeID = instance.EmployeeUserID
			assignment.WxWorkHealthStatus = instance.HealthStatus
		}
		result[binding.UserID] = assignment
	}
	return result
}

func (s *storeStaffBindingService) ResolveForInstance(instance *models.WxWorkProtocolInstance) StoreStaffRuntimeConfig {
	if instance == nil {
		return unavailableStoreStaffRuntimeConfig(0)
	}
	if instance.TenantID > 0 && instance.StoreStaffBindingID > 0 {
		if binding := s.GetInTenant(instance.StoreStaffBindingID, instance.TenantID); binding != nil &&
			binding.Status == enums.StatusOk && binding.StoreID == instance.StoreID {
			return s.runtimeConfigFromBinding(binding)
		}
	}
	return unavailableStoreStaffRuntimeConfig(instance.TenantID)
}

func unavailableStoreStaffRuntimeConfig(tenantID int64) StoreStaffRuntimeConfig {
	return StoreStaffRuntimeConfig{
		TenantID:             tenantID,
		ManagedMode:          constants.StoreManagedModeSemi,
		FallbackToHQ:         true,
		ManualTimeoutMinutes: DefaultManualTimeoutMinutes,
		NoWxWorkInstance:     true,
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
	var binding *models.StoreStaffBinding
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current := repositories.WxWorkProtocolInstanceRepository.GetInTenant(ctx.Tx, instance.ID, instance.TenantID)
		if current == nil || current.Status == enums.StatusDeleted || current.StoreID <= 0 {
			return errorsx.InvalidParam("员工号不存在、已删除或未绑定门店")
		}
		if current.StoreStaffBindingID <= 0 {
			return errorsx.InvalidParam("该企微实例尚未绑定具体门店员工号，请先在实例管理完成归属")
		}
		existing := repositories.StoreStaffBindingRepository.GetInTenant(ctx.Tx, current.StoreStaffBindingID, current.TenantID)
		if existing == nil || existing.StoreID != current.StoreID {
			return errorsx.InvalidParam("企微实例与门店员工号归属不一致")
		}
		var err error
		binding, err = repositories.StoreStaffBindingRepository.GetForUpdateInTenant(ctx.Tx, existing.ID, current.TenantID)
		if err != nil {
			return err
		}
		if binding == nil || binding.Status != enums.StatusOk {
			return errorsx.InvalidParam("门店员工绑定已变化，请重试")
		}
		if err := s.validateBindingOwnerDB(ctx.Tx, binding); err != nil {
			return err
		}
		if binding.AgentTeamID > 0 {
			team, err := repositories.AgentTeamRepository.GetForUpdateInTenant(ctx.Tx, binding.AgentTeamID, binding.TenantID)
			if err != nil {
				return err
			}
			if team == nil || team.Status == enums.StatusDeleted {
				return errorsx.InvalidParam("门店员工绑定的客服组不存在或已删除")
			}
		}
		if current.StoreStaffBindingID != binding.ID || current.AgentTeamID != binding.AgentTeamID {
			if err := repositories.WxWorkProtocolInstanceRepository.UpdatesInTenant(ctx.Tx, current.ID, current.TenantID, map[string]any{
				"store_staff_binding_id": binding.ID,
				"agent_team_id":          binding.AgentTeamID,
				"updated_at":             time.Now(),
			}); err != nil {
				return err
			}
		}
		if binding.AgentTeamID > 0 {
			return AgentTeamService.syncTeamScopeFromAssignments(ctx.Tx, binding.AgentTeamID, operator)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return binding, nil
}

func (s *storeStaffBindingService) validateBindingOwnerDB(db *gorm.DB, binding *models.StoreStaffBinding) error {
	if binding == nil || binding.Status != enums.StatusOk || binding.TenantID <= 0 || binding.StoreID <= 0 || binding.UserID <= 0 {
		return errorsx.InvalidParam("门店员工绑定不完整，请在用户管理重新绑定")
	}
	if binding.ActiveUserID == nil || *binding.ActiveUserID != binding.UserID {
		return errorsx.InvalidParam("门店员工绑定缺少唯一账号占用标记，请在用户管理重新绑定")
	}
	user := repositories.UserRepository.GetInTenant(db, binding.UserID, binding.TenantID)
	if user == nil || user.Status != enums.StatusOk || user.DeletedAt != nil {
		return errorsx.InvalidParam("已分配门店员工号角色的系统账号不存在或已停用")
	}
	role := repositories.RoleRepository.GetByCode(db, constants.RoleCodeStoreStaff)
	if role == nil || role.Status != enums.StatusOk || repositories.UserRoleRepository.FindOne(db, sqls.NewCnd().Eq("user_id", user.ID).Eq("role_id", role.ID)) == nil {
		return errorsx.InvalidParam("绑定账号未持有门店员工号角色")
	}
	return nil
}

func positiveInt64Pointer(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	ret := value
	return &ret
}
