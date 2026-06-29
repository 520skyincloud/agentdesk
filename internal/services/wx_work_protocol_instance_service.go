package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
)

var WxWorkProtocolInstanceService = newWxWorkProtocolInstanceService()

func newWxWorkProtocolInstanceService() *wxWorkProtocolInstanceService {
	return &wxWorkProtocolInstanceService{}
}

type wxWorkProtocolInstanceService struct{}

const DefaultWxWorkProtocolPersonaPrompt = `你是酒店前台同事，说话简短、自然、像正常微信聊天。
不要用客服模板，不要加固定结尾，不要用“亲”“为您”“这边”“～”。
能确定就直接答；需要员工处理就先问房号、数量、时间，没创建工单前别说已安排。
轻互动只回“收到”“可以”“哈哈”，不解释。`

func (s *wxWorkProtocolInstanceService) Get(id int64) *models.WxWorkProtocolInstance {
	if id <= 0 {
		return nil
	}
	return repositories.WxWorkProtocolInstanceRepository.Get(sqls.DB(), id)
}

func (s *wxWorkProtocolInstanceService) Take(where ...any) *models.WxWorkProtocolInstance {
	return repositories.WxWorkProtocolInstanceRepository.Take(sqls.DB(), where...)
}

func (s *wxWorkProtocolInstanceService) CreatePendingFromLogin(guid string, raw json.RawMessage) (*models.WxWorkProtocolInstance, error) {
	guid = strings.TrimSpace(guid)
	if guid == "" {
		return nil, errorsx.InvalidParam("guid 不能为空")
	}
	if existing := s.Take("guid = ?", guid); existing != nil {
		return existing, nil
	}
	data := struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
		UserName string `json:"userName"`
		Name     string `json:"name"`
		RealName string `json:"real_name"`
		Nickname string `json:"nickname"`
		NickName string `json:"nickName"`
		Avatar   string `json:"avatar"`
		HeadImg  string `json:"head_img"`
	}{}
	_ = json.Unmarshal(raw, &data)
	employeeUserID := strings.TrimSpace(data.Username)
	if employeeUserID == "" {
		employeeUserID = strings.TrimSpace(data.UserName)
	}
	if employeeUserID == "" {
		employeeUserID = strings.TrimSpace(data.UserID)
	}
	employeeName := strings.TrimSpace(data.RealName)
	if employeeName == "" {
		employeeName = strings.TrimSpace(data.Name)
	}
	if employeeName == "" {
		employeeName = strings.TrimSpace(data.Nickname)
	}
	if employeeName == "" {
		employeeName = strings.TrimSpace(data.NickName)
	}
	employeeAvatar := strings.TrimSpace(data.Avatar)
	if employeeAvatar == "" {
		employeeAvatar = strings.TrimSpace(data.HeadImg)
	}
	now := time.Now()
	item := &models.WxWorkProtocolInstance{
		Guid:                      guid,
		EmployeeUserID:            employeeUserID,
		EmployeeName:              employeeName,
		EmployeeAvatar:            employeeAvatar,
		AIReplyEnabled:            true,
		ManualTimeoutMinutes:      DefaultManualTimeoutMinutes,
		PersonaPrompt:             DefaultWxWorkProtocolPersonaPrompt,
		ContextMaxMessages:        DefaultConversationContextMaxMessages,
		ContextMaxTokens:          DefaultConversationContextMaxTokens,
		ContextCompressionEnabled: true,
		HealthStatus:              "pending_binding",
		Status:                    enums.StatusDisabled,
		Remark:                    "登录回调自动登记，待绑定协议渠道、门店和知识库",
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserName: wxWorkProtocolSystemOperatorName,
			UpdatedAt:      now,
			UpdateUserName: wxWorkProtocolSystemOperatorName,
		},
	}
	if err := repositories.WxWorkProtocolInstanceRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	_ = WxWorkProtocolDevicePoolService.BindGUIDToInstance(guid, item.ID)
	return item, nil
}

func (s *wxWorkProtocolInstanceService) FindPageByCnd(cnd *sqls.Cnd) ([]models.WxWorkProtocolInstance, *sqls.Paging) {
	return repositories.WxWorkProtocolInstanceRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *wxWorkProtocolInstanceService) FindPageByParams(params *params.QueryParams) ([]models.WxWorkProtocolInstance, *sqls.Paging) {
	return repositories.WxWorkProtocolInstanceRepository.FindPageByParams(sqls.DB(), params)
}

func (s *wxWorkProtocolInstanceService) CreateInstance(req request.CreateWxWorkProtocolInstanceRequest, operator *dto.AuthPrincipal) (*models.WxWorkProtocolInstance, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	guid := strings.TrimSpace(req.Guid)
	if guid == "" {
		return nil, errorsx.InvalidParam("guid 不能为空")
	}
	if existing := s.Take("guid = ?", guid); existing != nil {
		return nil, errorsx.InvalidParam("guid 已存在")
	}
	if err := s.validateProtocolChannel(req.ChannelID); err != nil {
		return nil, err
	}
	if req.StoreID > 0 || req.KnowledgeBaseID > 0 {
		if err := s.validateBinding(req.ChannelID, req.StoreID, req.KnowledgeBaseID); err != nil {
			return nil, err
		}
	}
	now := time.Now()
	status := enums.Status(req.Status)
	if status != enums.StatusOk && status != enums.StatusDisabled {
		status = enums.StatusOk
	}
	item := &models.WxWorkProtocolInstance{
		Guid:                           guid,
		ChannelID:                      req.ChannelID,
		EmployeeUserID:                 strings.TrimSpace(req.EmployeeUserID),
		EmployeeName:                   utils.RepairMojibakeText(strings.TrimSpace(req.EmployeeName)),
		EmployeeAvatar:                 strings.TrimSpace(req.EmployeeAvatar),
		StoreID:                        req.StoreID,
		StoreAddress:                   utils.RepairMojibakeText(strings.TrimSpace(req.StoreAddress)),
		StoreNavigationName:            utils.RepairMojibakeText(strings.TrimSpace(req.StoreNavigationName)),
		StoreLongitude:                 strings.TrimSpace(req.StoreLongitude),
		StoreLatitude:                  strings.TrimSpace(req.StoreLatitude),
		StoreMapProvider:               strings.TrimSpace(req.StoreMapProvider),
		DefaultMiniProgramPayload:      normalizeWxWorkJSONText(req.DefaultMiniProgramPayload),
		WelcomeMessage:                 normalizeWxWorkWelcomeMessage(req.WelcomeMessage),
		WelcomeSendMiniProgram:         req.WelcomeSendMiniProgram,
		WelcomeAskLocation:             req.WelcomeAskLocation,
		KnowledgeBaseID:                req.KnowledgeBaseID,
		AIAgentID:                      req.AIAgentID,
		NotifyURL:                      strings.TrimSpace(req.NotifyURL),
		Proxy:                          strings.TrimSpace(req.Proxy),
		BridgeID:                       strings.TrimSpace(req.BridgeID),
		StaffUserIDs:                   strings.TrimSpace(req.StaffUserIDs),
		ServiceHours:                   strings.TrimSpace(req.ServiceHours),
		StoreRoomConversationID:        normalizeWxWorkRoomConversationID(req.StoreRoomConversationID),
		StoreRoomNotifyEnabled:         req.StoreRoomNotifyEnabled,
		StoreRoomAtList:                normalizeWxWorkAtList(req.StoreRoomAtList),
		FallbackToHQ:                   req.FallbackToHQ,
		ManualTimeoutMinutes:           normalizeManualTimeoutMinutes(req.ManualTimeoutMinutes),
		AIReplyEnabled:                 req.AIReplyEnabled,
		PersonaPrompt:                  normalizeWxWorkPersonaPrompt(req.PersonaPrompt),
		AutoAcceptFriendRequest:        req.AutoAcceptFriendRequest,
		AutoAcceptFriendRemarkTemplate: strings.TrimSpace(req.AutoAcceptFriendRemarkTemplate),
		ContextMaxMessages:             normalizeContextMaxMessages(req.ContextMaxMessages),
		ContextMaxTokens:               normalizeContextMaxTokens(req.ContextMaxTokens),
		ContextCompressionEnabled:      normalizeContextCompressionEnabled(req.ContextCompressionEnabled, req.ContextMaxMessages, req.ContextMaxTokens),
		HealthStatus:                   "unknown",
		Status:                         status,
		Remark:                         strings.TrimSpace(req.Remark),
		AuditFields:                    utils.BuildAuditFields(operator),
	}
	item.CreatedAt = now
	item.UpdatedAt = now
	if err := repositories.WxWorkProtocolInstanceRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	if err := s.syncStoreStaffBindingFromInstanceRequest(item, req.ManagedMode, req.ServiceHours, req.StoreRoomConversationID, req.StoreRoomNotifyEnabled, req.StoreRoomAtList, req.FallbackToHQ, req.ManualTimeoutMinutes, operator); err != nil {
		return nil, err
	}
	_ = WxWorkProtocolDevicePoolService.BindGUIDToInstance(guid, item.ID)
	return item, nil
}

func (s *wxWorkProtocolInstanceService) CreateLoginInstance(req request.StartWxWorkProtocolLoginRequest, operator *dto.AuthPrincipal) (*models.WxWorkProtocolInstance, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	channel, err := s.resolveEnabledProtocolChannel(req.ChannelID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	guid := normalizeProtocolDeviceGUID(req.Guid)
	if guid == "" {
		var err error
		guid, err = s.claimAvailableProtocolDeviceGUID(channel)
		if err != nil {
			return nil, err
		}
	}
	if existing := s.Take("guid = ? AND status <> ?", guid, enums.StatusDeleted); existing != nil {
		return existing, nil
	}
	item := &models.WxWorkProtocolInstance{
		Guid:                      guid,
		ChannelID:                 channel.ID,
		AIReplyEnabled:            true,
		PersonaPrompt:             DefaultWxWorkProtocolPersonaPrompt,
		ManualTimeoutMinutes:      DefaultManualTimeoutMinutes,
		ContextMaxMessages:        DefaultConversationContextMaxMessages,
		ContextMaxTokens:          DefaultConversationContextMaxTokens,
		ContextCompressionEnabled: true,
		HealthStatus:              "login_qrcode",
		Status:                    enums.StatusDisabled,
		Remark:                    "扫码登录创建，登录成功后请绑定门店和知识库",
		AuditFields:               utils.BuildAuditFields(operator),
	}
	item.CreatedAt = now
	item.UpdatedAt = now
	if err := repositories.WxWorkProtocolInstanceRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	_ = WxWorkProtocolDevicePoolService.BindGUIDToInstance(guid, item.ID)
	return item, nil
}

func (s *wxWorkProtocolInstanceService) CreateRemoteSetupInstance(req request.CreateWxWorkProtocolRemoteSetupRequest, operator *dto.AuthPrincipal) (*models.WxWorkProtocolInstance, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	channel, err := s.resolveEnabledProtocolChannel(req.ChannelID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	guid := normalizeProtocolDeviceGUID(req.Guid)
	if guid == "" {
		var err error
		guid, err = s.claimAvailableProtocolDeviceGUID(channel)
		if err != nil {
			return nil, err
		}
	}
	if existing := s.Take("guid = ? AND status <> ?", guid, enums.StatusDeleted); existing != nil {
		return nil, errorsx.InvalidParam("该协议设备 GUID 已绑定到其他员工号")
	}
	item := &models.WxWorkProtocolInstance{
		Guid:                      guid,
		ChannelID:                 channel.ID,
		AIReplyEnabled:            true,
		PersonaPrompt:             DefaultWxWorkProtocolPersonaPrompt,
		ManualTimeoutMinutes:      DefaultManualTimeoutMinutes,
		ContextMaxMessages:        DefaultConversationContextMaxMessages,
		ContextMaxTokens:          DefaultConversationContextMaxTokens,
		ContextCompressionEnabled: true,
		RemoteSetupToken:          strings.ReplaceAll(uuid.NewString(), "-", ""),
		HealthStatus:              "remote_setup",
		Status:                    enums.StatusDisabled,
		Remark:                    firstNonBlank(strings.TrimSpace(req.Remark), "远程开户链接创建，等待门店扫码登录并补充资料"),
		AuditFields:               utils.BuildAuditFields(operator),
	}
	expiresAt := now.Add(14 * 24 * time.Hour)
	item.RemoteSetupExpiresAt = &expiresAt
	item.CreatedAt = now
	item.UpdatedAt = now
	if err := repositories.WxWorkProtocolInstanceRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	_ = WxWorkProtocolDevicePoolService.BindGUIDToInstance(guid, item.ID)
	return item, nil
}

func (s *wxWorkProtocolInstanceService) GetRemoteSetupByToken(token string) (*models.WxWorkProtocolInstance, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errorsx.InvalidParam("远程配置链接无效")
	}
	item := s.Take("remote_setup_token = ? AND status <> ?", token, enums.StatusDeleted)
	if item == nil {
		return nil, errorsx.InvalidParam("远程配置链接不存在或已失效")
	}
	if item.RemoteSetupExpiresAt != nil && time.Now().After(*item.RemoteSetupExpiresAt) {
		return nil, errorsx.InvalidParam("远程配置链接已过期，请联系总部重新生成")
	}
	return item, nil
}

func (s *wxWorkProtocolInstanceService) UpdateRemoteSetup(req request.UpdateWxWorkProtocolRemoteSetupRequest) error {
	item, err := s.GetRemoteSetupByToken(req.Token)
	if err != nil {
		return err
	}
	now := time.Now()
	guid := normalizeProtocolDeviceGUID(req.Guid)
	updates := map[string]any{
		"employee_name":              utils.RepairMojibakeText(strings.TrimSpace(req.EmployeeName)),
		"store_id":                   req.StoreID,
		"store_address":              utils.RepairMojibakeText(strings.TrimSpace(req.StoreAddress)),
		"store_navigation_name":      utils.RepairMojibakeText(firstNonBlank(strings.TrimSpace(req.StoreNavigationName), strings.TrimSpace(req.StoreName))),
		"store_longitude":            strings.TrimSpace(req.StoreLongitude),
		"store_latitude":             strings.TrimSpace(req.StoreLatitude),
		"store_map_provider":         strings.TrimSpace(req.StoreMapProvider),
		"knowledge_base_id":          req.KnowledgeBaseID,
		"service_hours":              strings.TrimSpace(req.ServiceHours),
		"store_room_conversation_id": normalizeWxWorkRoomConversationID(req.StoreRoomConversationID),
		"store_room_notify_enabled":  req.StoreRoomNotifyEnabled,
		"store_room_at_list":         normalizeWxWorkAtList(req.StoreRoomAtList),
		"fallback_to_hq":             req.FallbackToHQ,
		"manual_timeout_minutes":     normalizeManualTimeoutMinutes(req.ManualTimeoutMinutes),
		"auto_accept_friend_request": req.AutoAcceptFriendRequest,
		"remote_setup_submitted_at":  now,
		"updated_at":                 now,
		"update_user_name":           "remote_store_setup",
	}
	if guid != "" && guid != item.Guid {
		if existing := s.Take("guid = ? AND id <> ? AND status <> ?", guid, item.ID, enums.StatusDeleted); existing != nil {
			return errorsx.InvalidParam("该协议设备 GUID 已绑定到其他员工号")
		}
		updates["guid"] = guid
	}
	if err := repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), item.ID, updates); err != nil {
		return err
	}
	updated := s.Get(item.ID)
	if updated == nil {
		return nil
	}
	return s.syncStoreStaffBindingFromInstanceRequest(updated, req.ManagedMode, req.ServiceHours, req.StoreRoomConversationID, req.StoreRoomNotifyEnabled, req.StoreRoomAtList, req.FallbackToHQ, req.ManualTimeoutMinutes, nil)
}

func (s *wxWorkProtocolInstanceService) UpdateInstance(req request.UpdateWxWorkProtocolInstanceRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	current := s.Get(req.ID)
	if current == nil {
		return errorsx.InvalidParam("企微员工号实例不存在")
	}
	guid := strings.TrimSpace(req.Guid)
	if guid == "" {
		return errorsx.InvalidParam("guid 不能为空")
	}
	if existing := s.Take("guid = ? AND id <> ?", guid, req.ID); existing != nil {
		return errorsx.InvalidParam("guid 已存在")
	}
	if err := s.validateProtocolChannel(req.ChannelID); err != nil {
		return err
	}
	if req.StoreID > 0 || req.KnowledgeBaseID > 0 {
		if err := s.validateBinding(req.ChannelID, req.StoreID, req.KnowledgeBaseID); err != nil {
			return err
		}
	}
	status := enums.Status(req.Status)
	if status != enums.StatusOk && status != enums.StatusDisabled {
		status = current.Status
	}
	if err := repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), req.ID, map[string]any{
		"guid":                               guid,
		"channel_id":                         req.ChannelID,
		"employee_user_id":                   strings.TrimSpace(req.EmployeeUserID),
		"employee_name":                      utils.RepairMojibakeText(strings.TrimSpace(req.EmployeeName)),
		"employee_avatar":                    strings.TrimSpace(req.EmployeeAvatar),
		"store_id":                           req.StoreID,
		"store_address":                      utils.RepairMojibakeText(strings.TrimSpace(req.StoreAddress)),
		"store_navigation_name":              utils.RepairMojibakeText(strings.TrimSpace(req.StoreNavigationName)),
		"store_longitude":                    strings.TrimSpace(req.StoreLongitude),
		"store_latitude":                     strings.TrimSpace(req.StoreLatitude),
		"store_map_provider":                 strings.TrimSpace(req.StoreMapProvider),
		"default_mini_program_payload":       normalizeWxWorkJSONText(req.DefaultMiniProgramPayload),
		"welcome_message":                    normalizeWxWorkWelcomeMessage(req.WelcomeMessage),
		"welcome_send_mini_program":          req.WelcomeSendMiniProgram,
		"welcome_ask_location":               req.WelcomeAskLocation,
		"knowledge_base_id":                  req.KnowledgeBaseID,
		"ai_agent_id":                        req.AIAgentID,
		"notify_url":                         strings.TrimSpace(req.NotifyURL),
		"proxy":                              strings.TrimSpace(req.Proxy),
		"bridge_id":                          strings.TrimSpace(req.BridgeID),
		"staff_user_ids":                     strings.TrimSpace(req.StaffUserIDs),
		"service_hours":                      strings.TrimSpace(req.ServiceHours),
		"store_room_conversation_id":         normalizeWxWorkRoomConversationID(req.StoreRoomConversationID),
		"store_room_notify_enabled":          req.StoreRoomNotifyEnabled,
		"store_room_at_list":                 normalizeWxWorkAtList(req.StoreRoomAtList),
		"fallback_to_hq":                     req.FallbackToHQ,
		"manual_timeout_minutes":             normalizeManualTimeoutMinutes(req.ManualTimeoutMinutes),
		"ai_reply_enabled":                   req.AIReplyEnabled,
		"persona_prompt":                     normalizeWxWorkPersonaPrompt(req.PersonaPrompt),
		"auto_accept_friend_request":         req.AutoAcceptFriendRequest,
		"auto_accept_friend_remark_template": strings.TrimSpace(req.AutoAcceptFriendRemarkTemplate),
		"context_max_messages":               normalizeContextMaxMessages(req.ContextMaxMessages),
		"context_max_tokens":                 normalizeContextMaxTokens(req.ContextMaxTokens),
		"context_compression_enabled":        normalizeContextCompressionEnabled(req.ContextCompressionEnabled, req.ContextMaxMessages, req.ContextMaxTokens),
		"status":                             status,
		"remark":                             strings.TrimSpace(req.Remark),
		"updated_at":                         time.Now(),
		"update_user_id":                     operator.UserID,
		"update_user_name":                   operator.Username,
	}); err != nil {
		return err
	}
	updated := s.Get(req.ID)
	if updated == nil {
		return nil
	}
	return s.syncStoreStaffBindingFromInstanceRequest(updated, req.ManagedMode, req.ServiceHours, req.StoreRoomConversationID, req.StoreRoomNotifyEnabled, req.StoreRoomAtList, req.FallbackToHQ, req.ManualTimeoutMinutes, operator)
}

func (s *wxWorkProtocolInstanceService) SetAIReplyEnabled(instanceID int64, enabled bool, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	instance := s.Get(instanceID)
	if instance == nil || instance.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("企微员工号实例不存在")
	}
	now := time.Now()
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.WxWorkProtocolInstanceRepository.Updates(ctx.Tx, instance.ID, map[string]any{
			"ai_reply_enabled": enabled,
			"updated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		if !enabled {
			return nil
		}
		if err := repositories.ConversationRouteStateRepository.ResetAIByWxWorkInstance(ctx.Tx, instance.ID, now, operator.Username); err != nil {
			return err
		}
		return repositories.ConversationRepository.ReleaseAIServingByWxWorkInstance(ctx.Tx, instance.ID, now, operator.UserID, operator.Username)
	})
}

func (s *wxWorkProtocolInstanceService) UpdateAISettings(req request.UpdateWxWorkProtocolAISettingsRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	instance := s.Get(req.ID)
	if instance == nil || instance.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("企微员工号实例不存在")
	}
	if err := s.validateBinding(instance.ChannelID, req.StoreID, req.KnowledgeBaseID); err != nil {
		return err
	}
	if err := repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), req.ID, map[string]any{
		"store_id":                           req.StoreID,
		"store_address":                      utils.RepairMojibakeText(strings.TrimSpace(req.StoreAddress)),
		"store_navigation_name":              utils.RepairMojibakeText(strings.TrimSpace(req.StoreNavigationName)),
		"store_longitude":                    strings.TrimSpace(req.StoreLongitude),
		"store_latitude":                     strings.TrimSpace(req.StoreLatitude),
		"store_map_provider":                 strings.TrimSpace(req.StoreMapProvider),
		"default_mini_program_payload":       normalizeWxWorkJSONText(req.DefaultMiniProgramPayload),
		"welcome_message":                    normalizeWxWorkWelcomeMessage(req.WelcomeMessage),
		"welcome_send_mini_program":          req.WelcomeSendMiniProgram,
		"welcome_ask_location":               req.WelcomeAskLocation,
		"knowledge_base_id":                  req.KnowledgeBaseID,
		"ai_agent_id":                        req.AIAgentID,
		"staff_user_ids":                     strings.TrimSpace(req.StaffUserIDs),
		"service_hours":                      strings.TrimSpace(req.ServiceHours),
		"store_room_conversation_id":         normalizeWxWorkRoomConversationID(req.StoreRoomConversationID),
		"store_room_notify_enabled":          req.StoreRoomNotifyEnabled,
		"store_room_at_list":                 normalizeWxWorkAtList(req.StoreRoomAtList),
		"fallback_to_hq":                     req.FallbackToHQ,
		"manual_timeout_minutes":             normalizeManualTimeoutMinutes(req.ManualTimeoutMinutes),
		"ai_reply_enabled":                   req.AIReplyEnabled,
		"persona_prompt":                     normalizeWxWorkPersonaPrompt(req.PersonaPrompt),
		"auto_accept_friend_request":         req.AutoAcceptFriendRequest,
		"auto_accept_friend_remark_template": strings.TrimSpace(req.AutoAcceptFriendRemarkTemplate),
		"context_max_messages":               normalizeContextMaxMessages(req.ContextMaxMessages),
		"context_max_tokens":                 normalizeContextMaxTokens(req.ContextMaxTokens),
		"context_compression_enabled":        normalizeContextCompressionEnabled(req.ContextCompressionEnabled, req.ContextMaxMessages, req.ContextMaxTokens),
		"updated_at":                         time.Now(),
		"update_user_id":                     operator.UserID,
		"update_user_name":                   operator.Username,
	}); err != nil {
		return err
	}
	updated := s.Get(req.ID)
	if updated == nil {
		return nil
	}
	return s.syncStoreStaffBindingFromInstanceRequest(updated, req.ManagedMode, req.ServiceHours, req.StoreRoomConversationID, req.StoreRoomNotifyEnabled, req.StoreRoomAtList, req.FallbackToHQ, req.ManualTimeoutMinutes, operator)
}

func (s *wxWorkProtocolInstanceService) syncStoreStaffBindingFromInstanceRequest(instance *models.WxWorkProtocolInstance, managedMode string, serviceHours string, roomConversationID string, roomNotifyEnabled bool, roomAtList string, fallbackToHQ bool, manualTimeoutMinutes int, operator *dto.AuthPrincipal) error {
	if instance == nil || instance.StoreID <= 0 {
		return nil
	}
	binding, err := StoreStaffBindingService.EnsureForInstance(instance, operator)
	if err != nil {
		return err
	}
	mode := normalizeStoreManagedMode(managedMode)
	now := time.Now()
	return repositories.StoreStaffBindingRepository.Updates(sqls.DB(), binding.ID, map[string]any{
		"managed_mode":               mode,
		"service_hours":              strings.TrimSpace(serviceHours),
		"store_room_conversation_id": normalizeWxWorkRoomConversationID(roomConversationID),
		"store_room_notify_enabled":  roomNotifyEnabled,
		"store_room_at_list":         normalizeWxWorkAtList(roomAtList),
		"fallback_to_hq":             fallbackToHQ,
		"manual_timeout_minutes":     normalizeManualTimeoutMinutes(manualTimeoutMinutes),
		"updated_at":                 now,
		"update_user_id":             auditUserID(operator),
		"update_user_name":           auditUsername(operator),
	})
}

func normalizeStoreManagedMode(value string) string {
	switch strings.TrimSpace(value) {
	case constants.StoreManagedModeFull:
		return constants.StoreManagedModeFull
	case constants.StoreManagedModeNone:
		return constants.StoreManagedModeNone
	default:
		return constants.StoreManagedModeSemi
	}
}

func auditUserID(operator *dto.AuthPrincipal) int64 {
	if operator == nil {
		return constants.SystemAuditUserID
	}
	return operator.UserID
}

func auditUsername(operator *dto.AuthPrincipal) string {
	if operator == nil {
		return constants.SystemAuditUserName
	}
	return operator.Username
}

func (s *wxWorkProtocolInstanceService) InitAIAgent(instanceID int64, operator *dto.AuthPrincipal) (*models.AIAgent, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	instance := s.Get(instanceID)
	if instance == nil || instance.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("企微员工号实例不存在")
	}
	if instance.KnowledgeBaseID <= 0 {
		return nil, errorsx.InvalidParam("请先给员工号绑定门店知识库")
	}
	if instance.AIAgentID > 0 {
		if existing := AIAgentService.Get(instance.AIAgentID); existing != nil && existing.Status != enums.StatusDeleted {
			return existing, nil
		}
	}
	baseAgent := s.defaultAIAgentTemplate()
	if baseAgent == nil {
		return nil, errorsx.InvalidParam("没有可复制的智能客服，请先配置一个启用的智能客服模板")
	}
	return s.initAIAgentFromBase(instance, baseAgent, operator)
}

func (s *wxWorkProtocolInstanceService) initAIAgentFromBase(instance *models.WxWorkProtocolInstance, baseAgent *models.AIAgent, operator *dto.AuthPrincipal) (*models.AIAgent, error) {
	if instance == nil || baseAgent == nil {
		return nil, errorsx.InvalidParam("员工号实例或智能客服模板不存在")
	}
	req := AIAgentService.BuildCreateRequestFromModel(baseAgent)
	req.Name = s.wxWorkAIAgentName(instance)
	req.Description = strings.TrimSpace(firstNonBlank(req.Description, "企微员工号独立智能客服配置"))
	req.SystemPrompt = mergeWxWorkPersonaIntoSystemPrompt(req.SystemPrompt, instance.PersonaPrompt)
	req.KnowledgeIDs = []int64{instance.KnowledgeBaseID}
	var created *models.AIAgent
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		item, err := AIAgentService.CreateAIAgentWithTx(ctx, req, operator)
		if err != nil {
			return err
		}
		created = item
		return repositories.WxWorkProtocolInstanceRepository.Updates(ctx.Tx, instance.ID, map[string]any{
			"ai_agent_id":      item.ID,
			"updated_at":       time.Now(),
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		})
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (s *wxWorkProtocolInstanceService) UpdateBoundAIAgent(req request.UpdateWxWorkProtocolAIAgentRequest, operator *dto.AuthPrincipal) (*models.AIAgent, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	instance := s.Get(req.ID)
	if instance == nil || instance.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("企微员工号实例不存在")
	}
	knowledgeBaseID := firstPositiveID(req.KnowledgeIDs)
	if knowledgeBaseID <= 0 {
		return nil, errorsx.InvalidParam("请在智能客服配置里选择门店知识库")
	}
	if knowledgeBase := KnowledgeBaseService.Get(knowledgeBaseID); knowledgeBase == nil || knowledgeBase.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("选择的门店知识库不存在或未启用")
	}
	var agentID = instance.AIAgentID
	var saved *models.AIAgent
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if agentID <= 0 || repositories.AIAgentRepository.Get(ctx.Tx, agentID) == nil {
			item, err := AIAgentService.CreateAIAgentWithTx(ctx, req.CreateAIAgentRequest, operator)
			if err != nil {
				return err
			}
			agentID = item.ID
			saved = item
		} else {
			if err := AIAgentService.UpdateAIAgentWithTx(ctx, request.UpdateAIAgentRequest{ID: agentID, CreateAIAgentRequest: req.CreateAIAgentRequest}, operator); err != nil {
				return err
			}
			saved = repositories.AIAgentRepository.Get(ctx.Tx, agentID)
		}
		return repositories.WxWorkProtocolInstanceRepository.Updates(ctx.Tx, instance.ID, map[string]any{
			"ai_agent_id":       agentID,
			"knowledge_base_id": knowledgeBaseID,
			"updated_at":        time.Now(),
			"update_user_id":    operator.UserID,
			"update_user_name":  operator.Username,
		})
	})
	if err != nil {
		return nil, err
	}
	if saved == nil {
		saved = AIAgentService.Get(agentID)
	}
	return saved, nil
}

// MigrateDedicatedAIAgents backfills existing WeCom employee accounts so each
// account owns one independent AI Agent. Runtime AI replies must read only the
// instance-bound Agent; this migration preserves old personaPrompt text by
// folding it once into the copied Agent's system prompt.
func (s *wxWorkProtocolInstanceService) MigrateDedicatedAIAgents() error {
	operator := &dto.AuthPrincipal{
		UserID:   constants.SystemAuditUserID,
		Username: constants.SystemAuditUserName,
		Nickname: constants.SystemAuditUserName,
	}
	instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("status", enums.StatusOk).
		Eq("ai_agent_id", 0).
		Gt("knowledge_base_id", 0).
		Asc("id"))
	if len(instances) == 0 {
		return nil
	}
	baseAgent := s.defaultAIAgentTemplate()
	if baseAgent == nil {
		return nil
	}
	for i := range instances {
		if _, err := s.initAIAgentFromBase(&instances[i], baseAgent, operator); err != nil {
			return err
		}
	}
	return nil
}

func (s *wxWorkProtocolInstanceService) defaultAIAgentForInstance(instance *models.WxWorkProtocolInstance) *models.AIAgent {
	if instance != nil && instance.AIAgentID > 0 {
		if item := AIAgentService.Get(instance.AIAgentID); item != nil && item.Status != enums.StatusDeleted {
			return item
		}
	}
	return s.defaultAIAgentTemplate()
}

func (s *wxWorkProtocolInstanceService) defaultAIAgentTemplate() *models.AIAgent {
	agents := AIAgentService.Find(sqls.NewCnd().Eq("status", enums.StatusOk).Desc("sort_no").Desc("id"))
	if len(agents) == 0 {
		return nil
	}
	bound := make(map[int64]bool)
	instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().Gt("ai_agent_id", 0))
	for i := range instances {
		bound[instances[i].AIAgentID] = true
	}
	for i := range agents {
		if !bound[agents[i].ID] {
			return &agents[i]
		}
	}
	return &agents[0]
}

func (s *wxWorkProtocolInstanceService) wxWorkAIAgentName(instance *models.WxWorkProtocolInstance) string {
	base := "企微员工号智能客服"
	if instance != nil {
		base = firstNonBlank(utils.RepairMojibakeText(strings.TrimSpace(instance.EmployeeName)), strings.TrimSpace(instance.EmployeeUserID), strings.TrimSpace(instance.Guid), base)
	}
	name := base + " - 独立配置"
	if AIAgentService.Take("name = ?", name) == nil {
		return name
	}
	return fmt.Sprintf("%s - 独立配置 %s", base, time.Now().Format("20060102150405"))
}

func mergeWxWorkPersonaIntoSystemPrompt(systemPrompt string, personaPrompt string) string {
	base := strings.TrimSpace(utils.RepairMojibakeText(systemPrompt))
	persona := strings.TrimSpace(utils.RepairMojibakeText(personaPrompt))
	if persona == "" {
		return base
	}
	if base == "" {
		return persona
	}
	if strings.Contains(base, persona) {
		return base
	}
	return base + "\n\n员工号专属人格提示词：\n" + persona
}

func normalizeManualTimeoutMinutes(value int) int {
	if value <= 0 {
		return DefaultManualTimeoutMinutes
	}
	if value > 120 {
		return 120
	}
	return value
}

func normalizeWxWorkPersonaPrompt(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultWxWorkProtocolPersonaPrompt
	}
	return utils.RepairMojibakeText(value)
}

func (s *wxWorkProtocolInstanceService) resolveEnabledProtocolChannel(channelID int64) (*models.Channel, error) {
	if channelID <= 0 {
		channel := ChannelService.Take("channel_type = ? AND status = ?", enums.ChannelTypeWxWorkProtocol, enums.StatusOk)
		if channel == nil {
			return nil, errorsx.InvalidParam("请先创建并启用企微员工号协议渠道")
		}
		return channel, nil
	}
	channel := ChannelService.Get(channelID)
	if channel == nil || channel.Status != enums.StatusOk || channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
		return nil, errorsx.InvalidParam("请选择已启用的企微员工号协议渠道")
	}
	return channel, nil
}

func (s *wxWorkProtocolInstanceService) claimAvailableProtocolDeviceGUID(channel *models.Channel) (string, error) {
	if channel == nil {
		return "", errorsx.InvalidParam("企微协议渠道不存在")
	}
	guid, poolErr := WxWorkProtocolDevicePoolService.ClaimAvailableGUID(channel)
	if poolErr == nil && guid != "" {
		return guid, nil
	}
	cfg, err := ChannelService.ParseWxWorkProtocolChannelConfig(channel.ConfigJSON)
	if err != nil {
		return "", errorsx.InvalidParam("企微协议渠道配置不合法")
	}
	if cfg.DevicePoolURL == "" {
		if poolErr != nil {
			return "", poolErr
		}
		return "", errorsx.InvalidParam("请先在系统管理 > 实例池配置聚合智能账号并同步设备列表")
	}
	devices, err := s.fetchProtocolDevicePool(cfg)
	if err != nil {
		return "", err
	}
	if len(devices) == 0 {
		return "", errorsx.InvalidParam("协议平台设备池没有返回可识别的 GUID")
	}
	bound := s.boundProtocolGUIDs()
	for _, device := range devices {
		guid := normalizeProtocolDeviceGUID(device.Guid)
		if guid == "" || bound[guid] || !device.Available {
			continue
		}
		return guid, nil
	}
	return "", errorsx.InvalidParam("协议平台暂无可绑定的空闲实例，请先在协议平台初始化新设备")
}

type wxWorkProtocolDeviceCandidate struct {
	Guid      string
	Status    string
	Available bool
}

func (s *wxWorkProtocolInstanceService) fetchProtocolDevicePool(cfg *dto.WxWorkProtocolChannelConfig) ([]wxWorkProtocolDeviceCandidate, error) {
	body := map[string]any{
		"app_key":    cfg.AppKey,
		"app_secret": cfg.AppSecret,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, cfg.DevicePoolURL, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取协议平台设备池失败: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("协议平台设备池接口返回HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return parseProtocolDevicePoolResponse(respBody), nil
}

func (s *wxWorkProtocolInstanceService) boundProtocolGUIDs() map[string]bool {
	items := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().NotEq("status", enums.StatusDeleted))
	ret := make(map[string]bool, len(items))
	now := time.Now()
	for i := range items {
		if !wxWorkProtocolInstanceBlocksDevicePool(items[i], now) {
			continue
		}
		guid := normalizeProtocolDeviceGUID(items[i].Guid)
		if guid != "" {
			ret[guid] = true
		}
	}
	return ret
}

func parseProtocolDevicePoolResponse(raw []byte) []wxWorkProtocolDeviceCandidate {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil
	}
	return parseProtocolDevicePoolValue(root)
}

func parseProtocolDevicePoolValue(value any) []wxWorkProtocolDeviceCandidate {
	switch typed := value.(type) {
	case []any:
		ret := make([]wxWorkProtocolDeviceCandidate, 0, len(typed))
		for _, item := range typed {
			ret = append(ret, parseProtocolDevicePoolValue(item)...)
		}
		return ret
	case map[string]any:
		guid := firstNonBlank(valueAsString(typed["guid"]), valueAsString(typed["Guid"]), valueAsString(typed["device_guid"]), valueAsString(typed["deviceGuid"]), valueAsString(typed["client_guid"]), valueAsString(typed["clientGuid"]))
		if guid != "" {
			status := strings.ToLower(firstNonBlank(valueAsString(typed["status"]), valueAsString(typed["state"]), valueAsString(typed["health_status"]), valueAsString(typed["healthStatus"]), valueAsString(typed["login_status"]), valueAsString(typed["loginStatus"])))
			return []wxWorkProtocolDeviceCandidate{{Guid: guid, Status: status, Available: protocolDeviceStatusAvailable(status, typed)}}
		}
		for _, key := range []string{"data", "list", "items", "results", "records", "devices", "clients"} {
			if nested, ok := typed[key]; ok {
				if ret := parseProtocolDevicePoolValue(nested); len(ret) > 0 {
					return ret
				}
			}
		}
	}
	return nil
}

func protocolDeviceStatusAvailable(status string, data map[string]any) bool {
	if available, ok := data["available"].(bool); ok {
		return available
	}
	if idle, ok := data["idle"].(bool); ok {
		return idle
	}
	if status == "" {
		return true
	}
	blocked := []string{"online", "login", "logged", "logged_in", "in_use", "busy", "bound", "绑定", "占用", "已登录", "登录"}
	for _, item := range blocked {
		if strings.Contains(status, item) {
			return false
		}
	}
	return true
}

func normalizeProtocolDeviceGUID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(value), "pending_") {
		return ""
	}
	return value
}

func valueAsString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func normalizeWxWorkWelcomeMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "您好，欢迎来到丽斯未来。自助入住可以在小程序里办理，需要门店定位的话我也可以发您。"
	}
	return utils.RepairMojibakeText(value)
}

func normalizeWxWorkRoomConversationID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "R:") {
		return value
	}
	return "R:" + value
}

func normalizeWxWorkAtList(value string) string {
	parts := strings.Split(strings.TrimSpace(value), ",")
	ret := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		ret = append(ret, item)
	}
	return strings.Join(ret, ",")
}

func normalizeWxWorkJSONText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var data any
	if err := json.Unmarshal([]byte(value), &data); err != nil {
		return utils.RepairMojibakeText(value)
	}
	data = repairWxWorkJSONStrings(data)
	bytes, err := json.Marshal(data)
	if err != nil {
		return utils.RepairMojibakeText(value)
	}
	return string(bytes)
}

func repairWxWorkJSONStrings(value any) any {
	switch typed := value.(type) {
	case string:
		return utils.RepairMojibakeText(strings.TrimSpace(typed))
	case []any:
		for i := range typed {
			typed[i] = repairWxWorkJSONStrings(typed[i])
		}
		return typed
	case map[string]any:
		for key, item := range typed {
			typed[key] = repairWxWorkJSONStrings(item)
		}
		return typed
	default:
		return value
	}
}

func normalizeContextMaxMessages(value int) int {
	if value <= 0 {
		return DefaultConversationContextMaxMessages
	}
	if value < 5 {
		return 5
	}
	if value > 200 {
		return 200
	}
	return value
}

func normalizeContextMaxTokens(value int) int {
	if value <= 0 {
		return DefaultConversationContextMaxTokens
	}
	if value < 1000 {
		return 1000
	}
	if value > 32000 {
		return 32000
	}
	return value
}

func normalizeContextCompressionEnabled(enabled bool, maxMessages int, maxTokens int) bool {
	if maxMessages == 0 && maxTokens == 0 {
		return true
	}
	return enabled
}

func firstPositiveID(values []int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func (s *wxWorkProtocolInstanceService) DeleteInstance(id int64) error {
	if s.Get(id) == nil {
		return errorsx.InvalidParam("企微员工号实例不存在")
	}
	return repositories.WxWorkProtocolInstanceRepository.Delete(sqls.DB(), id)
}

func (s *wxWorkProtocolInstanceService) RequireStoreKnowledge(instance *models.WxWorkProtocolInstance) (int64, int64, error) {
	if instance == nil || instance.Status != enums.StatusOk || instance.StoreID <= 0 || instance.KnowledgeBaseID <= 0 {
		return 0, 0, errorsx.InvalidParam("企微员工号未绑定门店或知识库")
	}
	return instance.StoreID, instance.KnowledgeBaseID, nil
}

func (s *wxWorkProtocolInstanceService) validateBinding(channelID, storeID, knowledgeBaseID int64) error {
	if err := s.validateProtocolChannel(channelID); err != nil {
		return err
	}
	store := StoreService.Get(storeID)
	if store == nil || store.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("门店不存在")
	}
	knowledgeBase := KnowledgeBaseService.Get(knowledgeBaseID)
	if knowledgeBase == nil || knowledgeBase.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("知识库不存在")
	}
	return nil
}

func (s *wxWorkProtocolInstanceService) validateProtocolChannel(channelID int64) error {
	channel := ChannelService.Get(channelID)
	if channel == nil || channel.Status == enums.StatusDeleted || channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
		return errorsx.InvalidParam("请选择企微员工号协议渠道")
	}
	return nil
}
