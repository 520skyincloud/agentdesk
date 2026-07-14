package services

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var StoreWorkbenchService = newStoreWorkbenchService()

func newStoreWorkbenchService() *storeWorkbenchService { return &storeWorkbenchService{} }

type storeWorkbenchService struct{}

type StoreWorkbenchSnapshot struct {
	TenantID       int64
	TenantName     string
	UserID         int64
	Username       string
	Nickname       string
	Avatar         string
	Binding        *models.StoreStaffBinding
	Company        *models.Company
	Store          *models.Store
	AgentTeam      *models.AgentTeam
	WxWorkInstance *models.WxWorkProtocolInstance
	KnowledgeBase  *models.KnowledgeBase
	Runtime        StoreStaffRuntimeConfig
}

func (s *storeWorkbenchService) Current(operator *dto.AuthPrincipal) (*StoreWorkbenchSnapshot, error) {
	if operator == nil || operator.UserID <= 0 {
		return nil, errorsx.Forbidden("请先登录")
	}
	if operator.ActiveTenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入接入公司")
	}
	snapshot := &StoreWorkbenchSnapshot{
		TenantID:   operator.ActiveTenantID,
		TenantName: operator.ActiveTenantName,
		UserID:     operator.UserID,
		Username:   operator.Username,
		Nickname:   operator.Nickname,
		Avatar:     operator.Avatar,
	}
	bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", operator.ActiveTenantID).
		Eq("user_id", operator.UserID).
		Where("status <> ?", enums.StatusDeleted).
		Asc("id"))
	if len(bindings) == 0 {
		return snapshot, nil
	}
	if len(bindings) > 1 {
		return nil, errorsx.InvalidParam("当前账号关联了多个门店，请联系公司主管修正后再使用门店工作台")
	}
	binding := &bindings[0]
	snapshot.Binding = binding
	snapshot.Runtime = StoreStaffBindingService.runtimeConfigFromBinding(binding)

	if binding.CompanyID > 0 {
		snapshot.Company = repositories.CompanyRepository.GetInTenant(sqls.DB(), binding.CompanyID, operator.ActiveTenantID)
	}
	if binding.StoreID <= 0 {
		return nil, errorsx.InvalidParam("当前门店员工账号尚未绑定门店")
	}
	snapshot.Store = repositories.StoreRepository.GetInTenant(sqls.DB(), binding.StoreID, operator.ActiveTenantID)
	if snapshot.Store == nil || snapshot.Store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("当前账号绑定的门店不存在，请联系公司主管修正")
	}
	if binding.AgentTeamID > 0 {
		snapshot.AgentTeam = repositories.AgentTeamRepository.GetInTenant(sqls.DB(), binding.AgentTeamID, operator.ActiveTenantID)
	}

	instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", operator.ActiveTenantID).
		Eq("store_staff_binding_id", binding.ID).
		Where("status <> ?", enums.StatusDeleted).
		Asc("id"))
	if len(instances) == 0 {
		instances = repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", operator.ActiveTenantID).
			Eq("store_id", binding.StoreID).
			Eq("store_staff_binding_id", 0).
			Where("status <> ?", enums.StatusDeleted).
			Asc("id"))
	}
	if len(instances) > 1 {
		return nil, errorsx.InvalidParam("当前门店关联了多个企微员工号，请联系公司主管修正后再使用门店工作台")
	}
	if len(instances) == 1 {
		snapshot.WxWorkInstance = &instances[0]
		snapshot.Runtime = StoreStaffBindingService.ResolveForInstance(snapshot.WxWorkInstance)
	}

	knowledgeBaseID := snapshot.Store.KnowledgeBaseID
	if snapshot.WxWorkInstance != nil && snapshot.WxWorkInstance.KnowledgeBaseID > 0 {
		knowledgeBaseID = snapshot.WxWorkInstance.KnowledgeBaseID
	}
	if knowledgeBaseID > 0 {
		snapshot.KnowledgeBase = repositories.KnowledgeBaseRepository.GetInTenant(sqls.DB(), knowledgeBaseID, operator.ActiveTenantID)
	}
	return snapshot, nil
}

func (s *storeWorkbenchService) UpdateCurrent(req request.UpdateStoreWorkbenchRequest, operator *dto.AuthPrincipal) (*StoreWorkbenchSnapshot, error) {
	snapshot, err := s.Current(operator)
	if err != nil {
		return nil, err
	}
	if snapshot.Binding == nil {
		return nil, errorsx.InvalidParam("当前账号尚未绑定门店，不能保存门店配置")
	}
	if snapshot.Binding.Status != enums.StatusOk {
		return nil, errorsx.Forbidden("当前门店员工绑定已停用，不能修改配置")
	}

	mode, err := validateStoreWorkbenchManagedMode(req.ManagedMode)
	if err != nil {
		return nil, err
	}
	serviceHours, err := normalizeStoreWorkbenchServiceHours(req.ServiceHours)
	if err != nil {
		return nil, err
	}
	if req.ManualTimeoutMinutes < 1 || req.ManualTimeoutMinutes > 120 {
		return nil, errorsx.InvalidParam("人工超时分钟必须在 1 到 120 之间")
	}
	roomConversationID := normalizeWxWorkRoomConversationID(req.StoreRoomConversationID)
	roomAtList := normalizeWxWorkAtList(req.StoreRoomAtList)
	if len(roomConversationID) > 128 || len(roomAtList) > 500 {
		return nil, errorsx.InvalidParam("门店通知群或 @ 成员数据过长，请重新选择")
	}
	if req.StoreRoomNotifyEnabled && roomConversationID == "" {
		return nil, errorsx.InvalidParam("启用门店群通知前必须选择通知群")
	}
	if roomConversationID == "" && roomAtList != "" {
		return nil, errorsx.InvalidParam("选择 @ 成员前必须先选择门店通知群")
	}
	if mode == constants.StoreManagedModeNone && (!req.StoreRoomNotifyEnabled || roomConversationID == "") {
		return nil, errorsx.InvalidParam("非托管模式只通过门店群接待，必须启用并选择门店通知群")
	}

	storeAddress := utils.RepairMojibakeText(strings.TrimSpace(req.StoreAddress))
	storeNavigationName := utils.RepairMojibakeText(strings.TrimSpace(req.StoreNavigationName))
	storeMapProvider := strings.TrimSpace(req.StoreMapProvider)
	if len(storeAddress) > 500 || len(storeNavigationName) > 200 || len(storeMapProvider) > 50 {
		return nil, errorsx.InvalidParam("门店地址或导航信息过长")
	}
	storeLongitude, storeLatitude, err := normalizeStoreWorkbenchCoordinates(req.StoreLongitude, req.StoreLatitude)
	if err != nil {
		return nil, err
	}

	fallbackToHQ := mode != constants.StoreManagedModeNone
	now := time.Now()
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.StoreStaffBindingRepository.UpdatesInTenant(ctx.Tx, snapshot.Binding.ID, snapshot.TenantID, map[string]any{
			"managed_mode":               mode,
			"service_hours":              serviceHours,
			"store_room_conversation_id": roomConversationID,
			"store_room_notify_enabled":  req.StoreRoomNotifyEnabled,
			"store_room_at_list":         roomAtList,
			"fallback_to_hq":             fallbackToHQ,
			"manual_timeout_minutes":     req.ManualTimeoutMinutes,
			"updated_at":                 now,
			"update_user_id":             operator.UserID,
			"update_user_name":           operator.Username,
		}); err != nil {
			return err
		}
		if snapshot.WxWorkInstance == nil {
			return nil
		}
		return repositories.WxWorkProtocolInstanceRepository.UpdatesInTenant(ctx.Tx, snapshot.WxWorkInstance.ID, snapshot.TenantID, map[string]any{
			"service_hours":              serviceHours,
			"store_room_conversation_id": roomConversationID,
			"store_room_notify_enabled":  req.StoreRoomNotifyEnabled,
			"store_room_at_list":         roomAtList,
			"fallback_to_hq":             fallbackToHQ,
			"manual_timeout_minutes":     req.ManualTimeoutMinutes,
			"store_address":              storeAddress,
			"store_navigation_name":      storeNavigationName,
			"store_longitude":            storeLongitude,
			"store_latitude":             storeLatitude,
			"store_map_provider":         storeMapProvider,
			"updated_at":                 now,
			"update_user_id":             operator.UserID,
			"update_user_name":           operator.Username,
		})
	}); err != nil {
		return nil, err
	}
	return s.Current(operator)
}

func validateStoreWorkbenchManagedMode(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch value {
	case constants.StoreManagedModeFull, constants.StoreManagedModeSemi, constants.StoreManagedModeNone:
		return value, nil
	default:
		return "", errorsx.InvalidParam("请选择有效的门店托管模式")
	}
}

func normalizeStoreWorkbenchServiceHours(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > 200 {
		return "", errorsx.InvalidParam("门店服务时间过长")
	}
	normalized := strings.NewReplacer("；", ";", "，", ",", "、", ",", " ", "").Replace(value)
	parts := strings.FieldsFunc(normalized, func(r rune) bool { return r == ',' || r == ';' || r == '|' || r == '\n' })
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		pieces := strings.Split(strings.NewReplacer("~", "-", "至", "-", "到", "-").Replace(strings.TrimSpace(part)), "-")
		if len(pieces) != 2 {
			return "", errorsx.InvalidParam("门店服务时间格式应为 09:00-22:00，多个时段用逗号分隔")
		}
		start, startOK := parseStoreServiceClock(pieces[0])
		end, endOK := parseStoreServiceClock(pieces[1])
		if !startOK || !endOK {
			return "", errorsx.InvalidParam("门店服务时间格式应为 09:00-22:00，多个时段用逗号分隔")
		}
		ret = append(ret, fmt.Sprintf("%02d:%02d-%02d:%02d", start/60, start%60, end/60, end%60))
	}
	if len(ret) == 0 {
		return "", errorsx.InvalidParam("门店服务时间不能为空")
	}
	return strings.Join(ret, ","), nil
}

func normalizeStoreWorkbenchCoordinates(longitudeValue string, latitudeValue string) (string, string, error) {
	longitudeValue = strings.TrimSpace(longitudeValue)
	latitudeValue = strings.TrimSpace(latitudeValue)
	if longitudeValue == "" && latitudeValue == "" {
		return "", "", nil
	}
	if longitudeValue == "" || latitudeValue == "" {
		return "", "", errorsx.InvalidParam("门店经纬度必须同时填写")
	}
	longitude, err := strconv.ParseFloat(longitudeValue, 64)
	if err != nil || longitude < -180 || longitude > 180 {
		return "", "", errorsx.InvalidParam("门店经度必须在 -180 到 180 之间")
	}
	latitude, err := strconv.ParseFloat(latitudeValue, 64)
	if err != nil || latitude < -90 || latitude > 90 {
		return "", "", errorsx.InvalidParam("门店纬度必须在 -90 到 90 之间")
	}
	return strconv.FormatFloat(longitude, 'f', 6, 64), strconv.FormatFloat(latitude, 'f', 6, 64), nil
}
