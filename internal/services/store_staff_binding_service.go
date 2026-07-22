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
	FromLegacyInstance      bool
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

func (s *storeStaffBindingService) prepareForUserDB(db *gorm.DB, tenantID, userID int64, storeName string, operator *dto.AuthPrincipal) (*preparedStoreStaffBinding, error) {
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理门店员工的接入公司")
	}
	if userID <= 0 {
		return nil, errorsx.InvalidParam("请选择已分配门店员工号角色的系统账号")
	}
	storeName = strings.TrimSpace(storeName)
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
	if len(bindings) > 1 {
		return nil, errorsx.InvalidParam("该账号存在多个门店绑定，请先修复历史数据")
	}
	now := time.Now()
	if len(bindings) == 1 {
		binding := &bindings[0]
		store := repositories.StoreRepository.GetInTenant(db, binding.StoreID, tenantID)
		if store == nil || store.Status == enums.StatusDeleted {
			return nil, errorsx.InvalidParam("该账号绑定的门店不存在或已删除")
		}
		if storeName == "" {
			storeName = strings.TrimSpace(store.Name)
		}
		if storeName == "" {
			return nil, errorsx.InvalidParam("请填写门店名称")
		}
		if err := repositories.StoreRepository.UpdatesInTenant(db, store.ID, tenantID, map[string]any{
			"name":             storeName,
			"company_id":       0,
			"status":           enums.StatusOk,
			"updated_at":       now,
			"update_user_id":   auditUserID(operator),
			"update_user_name": auditUsername(operator),
		}); err != nil {
			return nil, err
		}
		if err := repositories.StoreStaffBindingRepository.UpdatesInTenant(db, binding.ID, tenantID, map[string]any{
			"company_id":       0,
			"status":           enums.StatusOk,
			"updated_at":       now,
			"update_user_id":   auditUserID(operator),
			"update_user_name": auditUsername(operator),
		}); err != nil {
			return nil, err
		}
		store.Name = storeName
		store.CompanyID = 0
		store.Status = enums.StatusOk
		binding.CompanyID = 0
		binding.Status = enums.StatusOk
		if err := StoreModelCredentialService.EnsureStoreRecordsDB(db, store, operator); err != nil {
			return nil, err
		}
		return &preparedStoreStaffBinding{User: user, Store: store, Binding: binding}, nil
	}
	if storeName == "" {
		return nil, errorsx.InvalidParam("请填写门店名称")
	}

	store := &models.Store{
		TenantID:    tenantID,
		StoreCode:   generateStoreIdentityCode(tenantID),
		Name:        storeName,
		CompanyID:   0,
		Status:      enums.StatusOk,
		Remark:      "门店员工号角色账号生成的稳定门店身份",
		AuditFields: utils.BuildAuditFields(operator),
	}
	store.CreatedAt = now
	store.UpdatedAt = now
	if err := repositories.StoreRepository.Create(db, store); err != nil {
		return nil, err
	}
	if err := StoreModelCredentialService.EnsureStoreRecordsDB(db, store, operator); err != nil {
		return nil, err
	}
	binding := &models.StoreStaffBinding{
		TenantID:             tenantID,
		UserID:               user.ID,
		CompanyID:            0,
		StoreID:              store.ID,
		ManagedMode:          constants.StoreManagedModeSemi,
		FallbackToHQ:         true,
		ManualTimeoutMinutes: DefaultManualTimeoutMinutes,
		Status:               enums.StatusOk,
		Remark:               "公司主管分配的门店员工号角色账号",
		AuditFields:          utils.BuildAuditFields(operator),
	}
	binding.CreatedAt = now
	binding.UpdatedAt = now
	if err := repositories.StoreStaffBindingRepository.Create(db, binding); err != nil {
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
			"status":           enums.StatusDisabled,
			"updated_at":       now,
			"update_user_id":   auditUserID(operator),
			"update_user_name": auditUsername(operator),
		}); err != nil {
			return err
		}
		if binding.StoreID > 0 {
			if err := repositories.StoreRepository.UpdatesInTenant(db, binding.StoreID, tenantID, map[string]any{
				"status":           enums.StatusDisabled,
				"updated_at":       now,
				"update_user_id":   auditUserID(operator),
				"update_user_name": auditUsername(operator),
			}); err != nil {
				return err
			}
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
		instance := firstWxWorkProtocolInstance(repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", tenantID).
			Eq("store_staff_binding_id", binding.ID).
			Eq("replaced_by_instance_id", 0).
			Where("status <> ?", enums.StatusDeleted).
			Desc("id")))
		if instance == nil && binding.StoreID > 0 {
			instance = firstWxWorkProtocolInstance(repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().
				Eq("tenant_id", tenantID).
				Eq("store_id", binding.StoreID).
				Eq("replaced_by_instance_id", 0).
				Where("status <> ?", enums.StatusDeleted).
				Desc("id")))
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

func firstWxWorkProtocolInstance(list []models.WxWorkProtocolInstance) *models.WxWorkProtocolInstance {
	if len(list) == 0 {
		return nil
	}
	return &list[0]
}

func (s *storeStaffBindingService) ResolveForInstance(instance *models.WxWorkProtocolInstance) StoreStaffRuntimeConfig {
	if instance == nil {
		return StoreStaffRuntimeConfig{ManagedMode: constants.StoreManagedModeSemi, FallbackToHQ: true, ManualTimeoutMinutes: 10}
	}
	if instance.TenantID > 0 && instance.StoreStaffBindingID > 0 {
		if binding := s.GetInTenant(instance.StoreStaffBindingID, instance.TenantID); binding != nil && binding.Status == enums.StatusOk {
			return s.runtimeConfigFromBinding(binding)
		}
	}
	if instance.TenantID > 0 && instance.StoreID > 0 {
		if binding := repositories.StoreStaffBindingRepository.TakeInTenant(sqls.DB(), instance.TenantID, "store_id = ? AND status = ?", instance.StoreID, enums.StatusOk); binding != nil {
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
		existing := repositories.StoreStaffBindingRepository.TakeInTenant(ctx.Tx, current.TenantID, "store_id = ? AND status = ?", current.StoreID, enums.StatusOk)
		if existing == nil {
			return errorsx.InvalidParam("该门店尚未绑定已分配门店员工号角色的系统账号，请先在用户管理完成绑定")
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
