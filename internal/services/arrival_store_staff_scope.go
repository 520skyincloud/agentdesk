package services

import (
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"gorm.io/gorm"
)

type arrivalStoreStaffScope struct {
	Binding  *models.StoreStaffBinding
	Instance *models.WxWorkProtocolInstance
}

type arrivalBoundConversationScope struct {
	StoreStaff   *arrivalStoreStaffScope
	Conversation *models.Conversation
	Route        *models.ConversationRouteState
}

func arrivalStoreStaffAccountNameDB(db *gorm.DB, tenantID, bindingID int64) string {
	if db == nil || tenantID <= 0 || bindingID <= 0 {
		return ""
	}
	binding := repositories.StoreStaffBindingRepository.GetInTenant(db, bindingID, tenantID)
	if binding == nil {
		return ""
	}
	user := repositories.UserRepository.GetInTenant(db, binding.UserID, tenantID)
	if user == nil {
		return ""
	}
	if value := strings.TrimSpace(user.Nickname); value != "" {
		return value
	}
	return strings.TrimSpace(user.Username)
}

func resolveArrivalStoreStaffScopeDB(
	db *gorm.DB,
	tenantID, storeID, bindingID int64,
	forUpdate bool,
) (*arrivalStoreStaffScope, error) {
	if db == nil || tenantID <= 0 || storeID <= 0 || bindingID <= 0 {
		return nil, errorsx.BusinessError(71, "门店员工绑定范围不完整")
	}
	binding := repositories.StoreStaffBindingRepository.GetInTenant(db, bindingID, tenantID)
	if binding == nil || binding.Status != enums.StatusOk || binding.StoreID != storeID {
		return nil, errorsx.BusinessError(71, "门店员工绑定不存在、已停用或不属于当前门店")
	}
	if err := StoreStaffBindingService.validateBindingOwnerDB(db, binding); err != nil {
		return nil, err
	}
	instances, err := repositories.WxWorkProtocolInstanceRepository.FindCurrentByStoreStaffBindingInTenant(
		db,
		tenantID,
		binding.ID,
		forUpdate,
	)
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, errorsx.BusinessError(71, "门店员工号当前没有可用的企微实例")
	}
	if len(instances) > 1 {
		return nil, errorsx.BusinessError(71, "门店员工号存在多个当前企微实例，请先修复实例状态")
	}
	instance := &instances[0]
	if instance.StoreID != storeID || instance.StoreStaffBindingID != binding.ID {
		return nil, errorsx.BusinessError(71, "企微实例与门店员工绑定范围不一致")
	}
	return &arrivalStoreStaffScope{Binding: binding, Instance: instance}, nil
}

func resolveArrivalSelectedInstanceDB(
	db *gorm.DB,
	tenantID, storeID, instanceID int64,
	forUpdate bool,
) (*arrivalStoreStaffScope, error) {
	instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(db, instanceID, tenantID)
	if instance == nil || instance.Status != enums.StatusOk || instance.ReplacedByInstanceID > 0 ||
		instance.StoreID != storeID || instance.StoreStaffBindingID <= 0 {
		return nil, errorsx.InvalidParam("所选企微员工号实例不存在、已被替换或不属于当前门店")
	}
	scope, err := resolveArrivalStoreStaffScopeDB(db, tenantID, storeID, instance.StoreStaffBindingID, forUpdate)
	if err != nil {
		return nil, err
	}
	if scope.Instance.ID != instance.ID {
		return nil, errorsx.InvalidParam("所选企微员工号实例不是该门店员工号的当前实例")
	}
	return scope, nil
}

func resolveArrivalBoundConversationDB(
	db *gorm.DB,
	binding *models.ArrivalStoreBinding,
	requireOpen bool,
) (*arrivalBoundConversationScope, error) {
	if binding == nil || binding.TenantID <= 0 || binding.StoreID <= 0 ||
		binding.StoreStaffBindingID <= 0 || binding.CustomerID <= 0 || binding.ConversationID <= 0 {
		return nil, errorsx.BusinessError(71, "到店客户绑定范围不完整")
	}
	storeStaff, err := resolveArrivalStoreStaffScopeDB(
		db,
		binding.TenantID,
		binding.StoreID,
		binding.StoreStaffBindingID,
		false,
	)
	if err != nil {
		return nil, err
	}
	conversation := repositories.ConversationRepository.GetInTenant(db, binding.ConversationID, binding.TenantID)
	if conversation == nil || conversation.StoreID != binding.StoreID ||
		conversation.StoreStaffBindingID != binding.StoreStaffBindingID ||
		conversation.CustomerID != binding.CustomerID {
		return nil, errorsx.BusinessError(71, "到店客户会话与门店员工绑定范围不一致")
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, binding.TenantID)
	if route == nil || route.StoreID != binding.StoreID ||
		route.StoreStaffBindingID != binding.StoreStaffBindingID ||
		route.WxWorkInstanceID != storeStaff.Instance.ID {
		return nil, errorsx.BusinessError(71, "到店客户会话尚未路由到当前企微实例")
	}
	if requireOpen && (conversation.Status == enums.IMConversationStatusClosed || route.RouteStatus == enums.ConversationRouteStatusClosed) {
		return nil, errorsx.BusinessError(71, "到店客户会话已关闭")
	}
	return &arrivalBoundConversationScope{StoreStaff: storeStaff, Conversation: conversation, Route: route}, nil
}
