package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
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

const DefaultWxWorkProtocolPersonaPrompt = `你是酒店门店前台同事，在微信里自然回复客人。
说话短一点，像真人：默认一句话，别写客服模板。
少用“您”，优先说“你”；不要说“亲”“为您”“这边”“感谢理解”。
客人只发表情包、哈哈、OK，就回“哈哈”“收到”“好嘞”这种短句。
送水、送拖鞋、维修、叫醒、打扫等需要员工动作的事，工具或人工没成功前不能说已经安排，只能追问房号/数量/时间或转同事。`

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
	now := time.Now()
	item := &models.WxWorkProtocolInstance{
		Guid:                      guid,
		EmployeeUserID:            employeeUserID,
		EmployeeName:              employeeName,
		AIReplyEnabled:            true,
		ManualTimeoutMinutes:      DefaultManualTimeoutMinutes,
		PersonaPrompt:             DefaultWxWorkProtocolPersonaPrompt,
		ContextMaxMessages:        DefaultConversationContextMaxMessages,
		ContextMaxTokens:          DefaultConversationContextMaxTokens,
		ContextCompressionEnabled: true,
		HealthStatus:              "pending_binding",
		Status:                    enums.StatusOk,
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
	if err := s.validateBinding(req.ChannelID, req.StoreID, req.KnowledgeBaseID); err != nil {
		return nil, err
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
		EmployeeName:                   strings.TrimSpace(req.EmployeeName),
		StoreID:                        req.StoreID,
		StoreAddress:                   strings.TrimSpace(req.StoreAddress),
		StoreNavigationName:            strings.TrimSpace(req.StoreNavigationName),
		StoreLongitude:                 strings.TrimSpace(req.StoreLongitude),
		StoreLatitude:                  strings.TrimSpace(req.StoreLatitude),
		StoreMapProvider:               strings.TrimSpace(req.StoreMapProvider),
		KnowledgeBaseID:                req.KnowledgeBaseID,
		AIAgentID:                      req.AIAgentID,
		NotifyURL:                      strings.TrimSpace(req.NotifyURL),
		Proxy:                          strings.TrimSpace(req.Proxy),
		BridgeID:                       strings.TrimSpace(req.BridgeID),
		StaffUserIDs:                   strings.TrimSpace(req.StaffUserIDs),
		ServiceHours:                   strings.TrimSpace(req.ServiceHours),
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
	return item, nil
}

func (s *wxWorkProtocolInstanceService) CreateLoginInstance(req request.StartWxWorkProtocolLoginRequest, operator *dto.AuthPrincipal) (*models.WxWorkProtocolInstance, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	channelID := req.ChannelID
	if channelID <= 0 {
		channel := ChannelService.Take("channel_type = ? AND status = ?", enums.ChannelTypeWxWorkProtocol, enums.StatusOk)
		if channel == nil {
			return nil, errorsx.InvalidParam("请先创建并启用企微员工号协议渠道")
		}
		channelID = channel.ID
	} else {
		channel := ChannelService.Get(channelID)
		if channel == nil || channel.Status != enums.StatusOk || channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
			return nil, errorsx.InvalidParam("请选择已启用的企微员工号协议渠道")
		}
	}
	now := time.Now()
	guid := fmt.Sprintf("ad_%s", strings.ReplaceAll(uuid.NewString(), "-", ""))
	item := &models.WxWorkProtocolInstance{
		Guid:                      guid,
		ChannelID:                 channelID,
		AIReplyEnabled:            true,
		PersonaPrompt:             DefaultWxWorkProtocolPersonaPrompt,
		ManualTimeoutMinutes:      DefaultManualTimeoutMinutes,
		ContextMaxMessages:        DefaultConversationContextMaxMessages,
		ContextMaxTokens:          DefaultConversationContextMaxTokens,
		ContextCompressionEnabled: true,
		HealthStatus:              "login_qrcode",
		Status:                    enums.StatusOk,
		Remark:                    "扫码登录创建，登录成功后请绑定门店和知识库",
		AuditFields:               utils.BuildAuditFields(operator),
	}
	item.CreatedAt = now
	item.UpdatedAt = now
	if err := repositories.WxWorkProtocolInstanceRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
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
	if err := s.validateBinding(req.ChannelID, req.StoreID, req.KnowledgeBaseID); err != nil {
		return err
	}
	status := enums.Status(req.Status)
	if status != enums.StatusOk && status != enums.StatusDisabled {
		status = current.Status
	}
	return repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), req.ID, map[string]any{
		"guid":                               guid,
		"channel_id":                         req.ChannelID,
		"employee_user_id":                   strings.TrimSpace(req.EmployeeUserID),
		"employee_name":                      strings.TrimSpace(req.EmployeeName),
		"store_id":                           req.StoreID,
		"store_address":                      strings.TrimSpace(req.StoreAddress),
		"store_navigation_name":              strings.TrimSpace(req.StoreNavigationName),
		"store_longitude":                    strings.TrimSpace(req.StoreLongitude),
		"store_latitude":                     strings.TrimSpace(req.StoreLatitude),
		"store_map_provider":                 strings.TrimSpace(req.StoreMapProvider),
		"knowledge_base_id":                  req.KnowledgeBaseID,
		"ai_agent_id":                        req.AIAgentID,
		"notify_url":                         strings.TrimSpace(req.NotifyURL),
		"proxy":                              strings.TrimSpace(req.Proxy),
		"bridge_id":                          strings.TrimSpace(req.BridgeID),
		"staff_user_ids":                     strings.TrimSpace(req.StaffUserIDs),
		"service_hours":                      strings.TrimSpace(req.ServiceHours),
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
	})
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
	return repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), req.ID, map[string]any{
		"store_id":                           req.StoreID,
		"store_address":                      strings.TrimSpace(req.StoreAddress),
		"store_navigation_name":              strings.TrimSpace(req.StoreNavigationName),
		"store_longitude":                    strings.TrimSpace(req.StoreLongitude),
		"store_latitude":                     strings.TrimSpace(req.StoreLatitude),
		"store_map_provider":                 strings.TrimSpace(req.StoreMapProvider),
		"knowledge_base_id":                  req.KnowledgeBaseID,
		"ai_agent_id":                        req.AIAgentID,
		"staff_user_ids":                     strings.TrimSpace(req.StaffUserIDs),
		"service_hours":                      strings.TrimSpace(req.ServiceHours),
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
	})
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
	return value
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
	channel := ChannelService.Get(channelID)
	if channel == nil || channel.Status == enums.StatusDeleted || channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
		return errorsx.InvalidParam("请选择企微员工号协议渠道")
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
