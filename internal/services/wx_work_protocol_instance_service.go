package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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

type WxWorkProtocolInstanceStats struct {
	CustomerCount              int64
	ManualAttentionCount       int64
	UrgentManualAttentionCount int64
}

const DefaultWxWorkProtocolPersonaPrompt = `你是线上酒店接待，说话简短、自然、像正常微信聊天。
不要用客服模板，不要加固定结尾，不要用“亲”“为您”“这边”“～”。
能确定就直接答；需要真实动作时先收集一个最关键字段或进入接待路由，没工具或路由结果前别表达动作已执行或后续有人处理。
互动要接住上下文，别总回“哈哈/收到”。闲聊、感谢、确认、表情和纠错都要顺着当前话题自然回应，结束类就收住。`

const (
	wxWorkFrontDeskModeUnmanned  = "unmanned"
	wxWorkFrontDeskModeStaffed   = "staffed"
	wxWorkFrontDeskModeScheduled = "scheduled"
)

func (s *wxWorkProtocolInstanceService) BuildRuntimeAIAgent(instance *models.WxWorkProtocolInstance) models.AIAgent {
	name := "企微员工号AI"
	systemPrompt := DefaultWxWorkProtocolPersonaPrompt
	knowledgeIDs := ""
	if instance != nil {
		name = firstNonBlank(
			utils.RepairMojibakeText(strings.TrimSpace(instance.EmployeeName)),
			strings.TrimSpace(instance.EmployeeUserID),
			strings.TrimSpace(instance.Guid),
			name,
		)
		if strings.TrimSpace(instance.PersonaPrompt) != "" {
			systemPrompt = mergeWxWorkPersonaIntoSystemPrompt(DefaultWxWorkProtocolPersonaPrompt, instance.PersonaPrompt)
		}
		systemPrompt = appendWxWorkReceptionContext(systemPrompt, instance, time.Now())
		if instance.KnowledgeBaseID > 0 {
			knowledgeIDs = fmt.Sprintf("%d", instance.KnowledgeBaseID)
		}
	}
	return models.AIAgent{
		ID:                  0,
		Name:                name,
		Description:         "企微员工号运行时配置",
		Status:              enums.StatusOk,
		ServiceMode:         enums.IMConversationServiceModeAIFirst,
		SystemPrompt:        systemPrompt,
		ReplyTimeoutSeconds: 180,
		HandoffMode:         enums.AIAgentHandoffModeWaitPool,
		FallbackMode:        enums.AIAgentFallbackModeNoAnswer,
		KnowledgeIDs:        knowledgeIDs,
		AllowedGraphTools:   `["builtin/get_weather"]`,
	}
}

func (s *wxWorkProtocolInstanceService) BuildRuntimeAIAgentForConversation(conversationID int64) (models.AIAgent, bool) {
	route := ConversationRouteService.GetByConversationID(conversationID)
	if route == nil || route.WxWorkInstanceID <= 0 {
		return models.AIAgent{}, false
	}
	instance := s.Get(route.WxWorkInstanceID)
	if instance == nil || !instance.AIReplyEnabled {
		return models.AIAgent{}, false
	}
	if _, err := s.resolveEffectiveIntentProfileID(instance.CompanyID, instance.IntentProfileID, true); err != nil {
		return models.AIAgent{}, false
	}
	return s.BuildRuntimeAIAgent(instance), true
}

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
		FrontDeskMode:             wxWorkFrontDeskModeUnmanned,
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

func (s *wxWorkProtocolInstanceService) CountStats(instanceID int64) WxWorkProtocolInstanceStats {
	if instanceID <= 0 {
		return WxWorkProtocolInstanceStats{}
	}
	ret := WxWorkProtocolInstanceStats{}
	type row struct {
		CustomerCount              int64
		ManualAttentionCount       int64
		UrgentManualAttentionCount int64
	}
	var out row
	err := sqls.DB().Raw(`
SELECT
  COUNT(DISTINCT CASE WHEN c.customer_id > 0 THEN c.customer_id ELSE c.id END) AS customer_count,
  COALESCE(SUM(CASE
    WHEN crs.route_status = ? THEN 1
    WHEN crs.route_status = ? AND crs.need_human_follow_up = 1 THEN 1
    ELSE 0
  END), 0) AS manual_attention_count,
  COALESCE(SUM(CASE
    WHEN (
      crs.route_status = ? OR (crs.route_status = ? AND crs.need_human_follow_up = 1)
    ) AND (
      crs.handoff_reason LIKE '%摔倒%' OR crs.handoff_reason LIKE '%受伤%' OR crs.handoff_reason LIKE '%流血%' OR crs.handoff_reason LIKE '%报警%' OR crs.handoff_reason LIKE '%安全%'
    ) THEN 1 ELSE 0
  END), 0) AS urgent_manual_attention_count
FROM t_conversation_route_state crs
JOIN t_conversation c ON c.id = crs.conversation_id
WHERE crs.wx_work_instance_id = ?`,
		enums.ConversationRouteStatusHQAgentDeskPending,
		enums.ConversationRouteStatusStoreWecomManual,
		enums.ConversationRouteStatusHQAgentDeskPending,
		enums.ConversationRouteStatusStoreWecomManual,
		instanceID,
	).Scan(&out).Error
	if err != nil {
		return ret
	}
	ret.CustomerCount = out.CustomerCount
	ret.ManualAttentionCount = out.ManualAttentionCount
	ret.UrgentManualAttentionCount = out.UrgentManualAttentionCount
	return ret
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
	companyID, storeID, err := s.normalizeCompanyStoreBinding(req.CompanyID, req.StoreID, req.StoreName, req.EmployeeName, operator)
	if err != nil {
		return nil, err
	}
	intentProfileID, err := validateOptionalReplyIntentProfileID(req.IntentProfileID)
	if err != nil {
		return nil, err
	}
	effectiveIntentProfileID, err := s.resolveEffectiveIntentProfileID(companyID, intentProfileID, req.AIReplyEnabled)
	if err != nil {
		return nil, err
	}
	if err := s.validateBinding(req.ChannelID, storeID, req.KnowledgeBaseID, effectiveIntentProfileID); err != nil {
		return nil, err
	}
	welcomeImageAssetID, err := validateWxWorkWelcomeImageAsset(req.WelcomeImageAssetID)
	if err != nil {
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
		EmployeeName:                   utils.RepairMojibakeText(strings.TrimSpace(req.EmployeeName)),
		EmployeeAvatar:                 strings.TrimSpace(req.EmployeeAvatar),
		CompanyID:                      companyID,
		IntentProfileID:                intentProfileID,
		StoreID:                        storeID,
		StoreAddress:                   utils.RepairMojibakeText(strings.TrimSpace(req.StoreAddress)),
		StoreNavigationName:            utils.RepairMojibakeText(strings.TrimSpace(req.StoreNavigationName)),
		StoreLongitude:                 strings.TrimSpace(req.StoreLongitude),
		StoreLatitude:                  strings.TrimSpace(req.StoreLatitude),
		StoreMapProvider:               strings.TrimSpace(req.StoreMapProvider),
		StoreContactPhone:              utils.RepairMojibakeText(strings.TrimSpace(req.StoreContactPhone)),
		DefaultMiniProgramPayload:      normalizeWxWorkJSONText(req.DefaultMiniProgramPayload),
		WelcomeEnabled:                 req.WelcomeEnabled,
		WelcomeMessage:                 normalizeWxWorkWelcomeMessage(req.WelcomeMessage),
		WelcomeImageAssetID:            welcomeImageAssetID,
		WelcomeSendMiniProgram:         req.WelcomeSendMiniProgram,
		WelcomeAskLocation:             req.WelcomeAskLocation,
		KnowledgeBaseID:                req.KnowledgeBaseID,
		NotifyURL:                      strings.TrimSpace(req.NotifyURL),
		Proxy:                          strings.TrimSpace(req.Proxy),
		BridgeID:                       strings.TrimSpace(req.BridgeID),
		StaffUserIDs:                   strings.TrimSpace(req.StaffUserIDs),
		ServiceHours:                   strings.TrimSpace(req.ServiceHours),
		FrontDeskMode:                  normalizeWxWorkFrontDeskMode(req.FrontDeskMode),
		FrontDeskHours:                 normalizeWxWorkFrontDeskHours(req.FrontDeskMode, req.FrontDeskHours),
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
	if req.CompanyID > 0 {
		if company := CompanyService.Get(req.CompanyID); company == nil || company.Status == enums.StatusDeleted {
			return nil, errorsx.InvalidParam("公司不存在")
		}
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
		if req.CompanyID > 0 && existing.CompanyID > 0 && existing.CompanyID != req.CompanyID {
			return nil, errorsx.InvalidParam("该协议设备 GUID 已绑定到其他公司账号")
		}
		if req.CompanyID > 0 && existing.CompanyID == 0 {
			_ = repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), existing.ID, map[string]any{
				"company_id":       req.CompanyID,
				"updated_at":       now,
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
			})
			existing.CompanyID = req.CompanyID
		}
		return existing, nil
	}
	item := &models.WxWorkProtocolInstance{
		Guid:                      guid,
		ChannelID:                 channel.ID,
		AIReplyEnabled:            false,
		CompanyID:                 req.CompanyID,
		PersonaPrompt:             DefaultWxWorkProtocolPersonaPrompt,
		FrontDeskMode:             wxWorkFrontDeskModeUnmanned,
		ManualTimeoutMinutes:      DefaultManualTimeoutMinutes,
		ContextMaxMessages:        DefaultConversationContextMaxMessages,
		ContextMaxTokens:          DefaultConversationContextMaxTokens,
		ContextCompressionEnabled: true,
		HealthStatus:              "login_qrcode",
		Status:                    enums.StatusDisabled,
		Remark:                    "扫码登录创建，登录成功后请补充店名、账号资料、行业 Profile 和知识库",
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
		if s.canReuseForLogin(existing, now) {
			if req.CompanyID > 0 && existing.CompanyID > 0 && existing.CompanyID != req.CompanyID {
				return nil, errorsx.InvalidParam("该协议设备 GUID 已绑定到其他公司账号")
			}
			if req.CompanyID > 0 && existing.CompanyID == 0 {
				_ = repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), existing.ID, map[string]any{
					"company_id":       req.CompanyID,
					"updated_at":       now,
					"update_user_id":   operator.UserID,
					"update_user_name": operator.Username,
				})
				existing.CompanyID = req.CompanyID
			}
			return existing, nil
		}
		return nil, errorsx.InvalidParam("该协议设备 GUID 已绑定到其他员工号")
	}
	if req.CompanyID > 0 {
		if company := CompanyService.Get(req.CompanyID); company == nil || company.Status == enums.StatusDeleted {
			return nil, errorsx.InvalidParam("公司不存在")
		}
	}
	item := &models.WxWorkProtocolInstance{
		Guid:                      guid,
		ChannelID:                 channel.ID,
		CompanyID:                 req.CompanyID,
		AIReplyEnabled:            false,
		PersonaPrompt:             DefaultWxWorkProtocolPersonaPrompt,
		FrontDeskMode:             wxWorkFrontDeskModeUnmanned,
		ManualTimeoutMinutes:      DefaultManualTimeoutMinutes,
		ContextMaxMessages:        DefaultConversationContextMaxMessages,
		ContextMaxTokens:          DefaultConversationContextMaxTokens,
		ContextCompressionEnabled: true,
		RemoteSetupToken:          strings.ReplaceAll(uuid.NewString(), "-", ""),
		HealthStatus:              "remote_setup",
		Status:                    enums.StatusDisabled,
		Remark:                    firstNonBlank(strings.TrimSpace(req.Remark), "远程开户链接创建，等待门店扫码登录并补充行业 Profile、店名和知识库"),
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

func (s *wxWorkProtocolInstanceService) CreateReplacementRemoteSetup(req request.CreateWxWorkProtocolReplacementSetupRequest, operator *dto.AuthPrincipal) (*models.WxWorkProtocolInstance, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	old := s.Get(req.ID)
	if old == nil || old.Status == enums.StatusDeleted || old.StoreID <= 0 {
		return nil, errorsx.InvalidParam("原企微员工号不存在或未绑定门店")
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if !scope.Unrestricted {
		allowed := false
		for _, storeID := range scope.StoreIDs {
			if storeID == old.StoreID {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, errorsx.Forbidden("无权限更换该门店的企微员工号")
		}
	}
	if old.ReplacedByInstanceID > 0 {
		return nil, errorsx.InvalidParam("该员工号已经完成替换")
	}
	if existing := s.Take("replaces_instance_id = ? AND remote_setup_submitted_at IS NULL AND status <> ?", old.ID, enums.StatusDeleted); existing != nil {
		return existing, nil
	}
	if _, err := s.resolveEnabledProtocolChannel(old.ChannelID); err != nil {
		return nil, err
	}
	guid := normalizeProtocolDeviceGUID(req.Guid)
	var err error
	if guid == "" {
		channel := ChannelService.Get(old.ChannelID)
		guid, err = s.claimStaleProtocolDeviceGUID(channel)
		if err != nil {
			return nil, err
		}
	}
	if existing := s.Take("guid = ? AND status <> ?", guid, enums.StatusDeleted); existing != nil {
		return nil, errorsx.InvalidParam("该协议设备 GUID 已绑定到其他员工号")
	}
	now := time.Now()
	expiresAt := now.Add(14 * 24 * time.Hour)
	item := &models.WxWorkProtocolInstance{
		Guid:                           guid,
		ChannelID:                      old.ChannelID,
		CompanyID:                      old.CompanyID,
		IntentProfileID:                old.IntentProfileID,
		StoreID:                        old.StoreID,
		StoreStaffBindingID:            old.StoreStaffBindingID,
		ReplacesInstanceID:             old.ID,
		StoreAddress:                   old.StoreAddress,
		StoreNavigationName:            old.StoreNavigationName,
		StoreLongitude:                 old.StoreLongitude,
		StoreLatitude:                  old.StoreLatitude,
		StoreMapProvider:               old.StoreMapProvider,
		StoreContactPhone:              old.StoreContactPhone,
		DefaultMiniProgramPayload:      old.DefaultMiniProgramPayload,
		WelcomeEnabled:                 old.WelcomeEnabled,
		WelcomeMessage:                 old.WelcomeMessage,
		WelcomeImageAssetID:            old.WelcomeImageAssetID,
		WelcomeSendMiniProgram:         old.WelcomeSendMiniProgram,
		WelcomeAskLocation:             old.WelcomeAskLocation,
		KnowledgeBaseID:                old.KnowledgeBaseID,
		Proxy:                          old.Proxy,
		BridgeID:                       old.BridgeID,
		StaffUserIDs:                   old.StaffUserIDs,
		ServiceHours:                   old.ServiceHours,
		FrontDeskMode:                  normalizeWxWorkFrontDeskMode(old.FrontDeskMode),
		FrontDeskHours:                 normalizeWxWorkFrontDeskHours(old.FrontDeskMode, old.FrontDeskHours),
		StoreRoomConversationID:        old.StoreRoomConversationID,
		StoreRoomNotifyEnabled:         old.StoreRoomNotifyEnabled,
		StoreRoomAtList:                old.StoreRoomAtList,
		FallbackToHQ:                   old.FallbackToHQ,
		ManualTimeoutMinutes:           old.ManualTimeoutMinutes,
		AIReplyEnabled:                 false,
		PersonaPrompt:                  old.PersonaPrompt,
		AutoAcceptFriendRequest:        old.AutoAcceptFriendRequest,
		AutoAcceptFriendRemarkTemplate: old.AutoAcceptFriendRemarkTemplate,
		ContextMaxMessages:             old.ContextMaxMessages,
		ContextMaxTokens:               old.ContextMaxTokens,
		ContextCompressionEnabled:      old.ContextCompressionEnabled,
		RemoteSetupToken:               strings.ReplaceAll(uuid.NewString(), "-", ""),
		RemoteSetupExpiresAt:           &expiresAt,
		HealthStatus:                   "remote_setup",
		Status:                         enums.StatusDisabled,
		Remark:                         fmt.Sprintf("替换企微员工号 #%d，等待新员工号扫码并验证主邮箱", old.ID),
		AuditFields:                    utils.BuildAuditFields(operator),
	}
	item.CreatedAt = now
	item.UpdatedAt = now
	if err := repositories.WxWorkProtocolInstanceRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	_ = WxWorkProtocolDevicePoolService.BindGUIDToInstance(guid, item.ID)
	return item, nil
}

func (s *wxWorkProtocolInstanceService) ResolveLoginBinding(req request.ResolveWxWorkProtocolLoginBindingRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	channel, err := s.resolveEnabledProtocolChannel(req.ChannelID)
	if err != nil {
		return err
	}
	guid := normalizeProtocolDeviceGUID(req.Guid)
	if guid == "" {
		guid, err = s.claimStaleProtocolDeviceGUID(channel)
		if err != nil {
			return err
		}
	}
	existing := s.Take("guid = ? AND status <> ?", guid, enums.StatusDeleted)
	if existing == nil {
		return WxWorkProtocolDevicePoolService.ReleaseGUIDBinding(guid)
	}
	if !s.canReleaseLoginBinding(existing, time.Now()) {
		name := strings.TrimSpace(existing.EmployeeName)
		if name == "" {
			name = fmt.Sprintf("员工号 #%d", existing.ID)
		}
		return errorsx.InvalidParam("该实例已绑定到已登录账号「" + name + "」，不能自动解绑；请在账号设置中执行退出登录或手动停用后再重新扫码")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.WxWorkProtocolInstanceRepository.Updates(ctx.Tx, existing.ID, map[string]any{
			"status":           enums.StatusDeleted,
			"remark":           strings.TrimSpace(existing.Remark + "\n已清理未登录临时占用，允许重新扫码绑定"),
			"updated_at":       time.Now(),
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		return repositories.WxWorkProtocolDevicePoolRepository.UpdateByGUID(ctx.Tx, guid, map[string]any{
			"bound_wx_work_protocol_instance_id": 0,
			"sync_status":                        "idle",
			"remark":                             "已清理未登录临时占用，可重新扫码绑定",
			"updated_at":                         time.Now(),
			"update_user_id":                     operator.UserID,
			"update_user_name":                   operator.Username,
		})
	})
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
	updated, err := StoreAccountLifecycleService.CompleteRemoteSetup(item, req)
	if err != nil {
		return err
	}
	if updated == nil {
		return nil
	}
	if err := s.syncRouteStateBindingFromInstance(updated, "remote_store_setup"); err != nil {
		return err
	}
	if updated.StoreID > 0 && updated.KnowledgeBaseID <= 0 {
		if _, err := FastGPTDatasetService.EnqueueDefaultDatasetForRemoteSetup(updated.StoreID, firstNonBlank(req.StoreName, req.EmployeeName)); err != nil {
			slog.Warn("enqueue FastGPT dataset after remote setup failed", "instanceId", updated.ID, "storeId", updated.StoreID, "error", err)
		}
	}
	return nil
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
	companyID, storeID, err := s.normalizeCompanyStoreBinding(req.CompanyID, req.StoreID, req.StoreName, req.EmployeeName, operator)
	if err != nil {
		return err
	}
	intentProfileID, err := validateOptionalReplyIntentProfileID(req.IntentProfileID)
	if err != nil {
		return err
	}
	effectiveIntentProfileID, err := s.resolveEffectiveIntentProfileID(companyID, intentProfileID, req.AIReplyEnabled)
	if err != nil {
		return err
	}
	if err := s.validateBinding(req.ChannelID, storeID, req.KnowledgeBaseID, effectiveIntentProfileID); err != nil {
		return err
	}
	welcomeImageAssetID, err := validateWxWorkWelcomeImageAsset(req.WelcomeImageAssetID)
	if err != nil {
		return err
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
		"company_id":                         companyID,
		"intent_profile_id":                  intentProfileID,
		"store_id":                           storeID,
		"store_address":                      utils.RepairMojibakeText(strings.TrimSpace(req.StoreAddress)),
		"store_navigation_name":              utils.RepairMojibakeText(strings.TrimSpace(req.StoreNavigationName)),
		"store_longitude":                    strings.TrimSpace(req.StoreLongitude),
		"store_latitude":                     strings.TrimSpace(req.StoreLatitude),
		"store_map_provider":                 strings.TrimSpace(req.StoreMapProvider),
		"store_contact_phone":                utils.RepairMojibakeText(strings.TrimSpace(req.StoreContactPhone)),
		"default_mini_program_payload":       normalizeWxWorkJSONText(req.DefaultMiniProgramPayload),
		"welcome_enabled":                    req.WelcomeEnabled,
		"welcome_message":                    normalizeWxWorkWelcomeMessage(req.WelcomeMessage),
		"welcome_image_asset_id":             welcomeImageAssetID,
		"welcome_send_mini_program":          req.WelcomeSendMiniProgram,
		"welcome_ask_location":               req.WelcomeAskLocation,
		"knowledge_base_id":                  req.KnowledgeBaseID,
		"notify_url":                         strings.TrimSpace(req.NotifyURL),
		"proxy":                              strings.TrimSpace(req.Proxy),
		"bridge_id":                          strings.TrimSpace(req.BridgeID),
		"staff_user_ids":                     strings.TrimSpace(req.StaffUserIDs),
		"service_hours":                      strings.TrimSpace(req.ServiceHours),
		"front_desk_mode":                    normalizeWxWorkFrontDeskMode(req.FrontDeskMode),
		"front_desk_hours":                   normalizeWxWorkFrontDeskHours(req.FrontDeskMode, req.FrontDeskHours),
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
	if oldAssetID := strings.TrimSpace(current.WelcomeImageAssetID); oldAssetID != "" && oldAssetID != welcomeImageAssetID {
		if err := AssetService.CleanupWelcomeImageAsset(oldAssetID, operator); err != nil {
			slog.Warn("cleanup replaced wxwork welcome image failed", "instance_id", current.ID, "asset_id", oldAssetID, "error", err)
		}
	}
	if err := s.syncRouteStateBindingFromInstance(updated, operator.Username); err != nil {
		return err
	}
	if err := s.syncStoreStaffBindingFromInstanceRequest(updated, req.ManagedMode, req.ServiceHours, req.StoreRoomConversationID, req.StoreRoomNotifyEnabled, req.StoreRoomAtList, req.FallbackToHQ, req.ManualTimeoutMinutes, operator); err != nil {
		return err
	}
	return nil
}

func (s *wxWorkProtocolInstanceService) SetAIReplyEnabled(instanceID int64, enabled bool, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	instance := s.Get(instanceID)
	if instance == nil || instance.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("企微员工号实例不存在")
	}
	if enabled {
		if _, err := s.resolveEffectiveIntentProfileID(instance.CompanyID, instance.IntentProfileID, true); err != nil {
			return err
		}
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
		if ctx.Tx.Migrator().HasTable(&models.AIManualResumeTask{}) {
			if err := repositories.AIManualResumeTaskRepository.UnblockByWxWorkInstance(ctx.Tx, instance.ID, map[string]any{
				"task_status":      aiManualResumeTaskReady,
				"ready_at":         now,
				"next_retry_at":    now,
				"last_error":       "",
				"updated_at":       now,
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
			}); err != nil {
				return err
			}
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
	companyID, storeID, err := s.normalizeCompanyStoreBinding(req.CompanyID, req.StoreID, req.StoreName, instance.EmployeeName, operator)
	if err != nil {
		return err
	}
	intentProfileID, err := validateOptionalReplyIntentProfileID(req.IntentProfileID)
	if err != nil {
		return err
	}
	effectiveIntentProfileID, err := s.resolveEffectiveIntentProfileID(companyID, intentProfileID, req.AIReplyEnabled)
	if err != nil {
		return err
	}
	if err := s.validateBinding(instance.ChannelID, storeID, req.KnowledgeBaseID, effectiveIntentProfileID); err != nil {
		return err
	}
	if err := repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), req.ID, map[string]any{
		"company_id":                         companyID,
		"intent_profile_id":                  intentProfileID,
		"store_id":                           storeID,
		"store_address":                      utils.RepairMojibakeText(strings.TrimSpace(req.StoreAddress)),
		"store_navigation_name":              utils.RepairMojibakeText(strings.TrimSpace(req.StoreNavigationName)),
		"store_longitude":                    strings.TrimSpace(req.StoreLongitude),
		"store_latitude":                     strings.TrimSpace(req.StoreLatitude),
		"store_map_provider":                 strings.TrimSpace(req.StoreMapProvider),
		"store_contact_phone":                utils.RepairMojibakeText(strings.TrimSpace(req.StoreContactPhone)),
		"default_mini_program_payload":       normalizeWxWorkJSONText(req.DefaultMiniProgramPayload),
		"welcome_message":                    normalizeWxWorkWelcomeMessage(req.WelcomeMessage),
		"welcome_send_mini_program":          req.WelcomeSendMiniProgram,
		"welcome_ask_location":               req.WelcomeAskLocation,
		"knowledge_base_id":                  req.KnowledgeBaseID,
		"staff_user_ids":                     strings.TrimSpace(req.StaffUserIDs),
		"service_hours":                      strings.TrimSpace(req.ServiceHours),
		"front_desk_mode":                    normalizeWxWorkFrontDeskMode(req.FrontDeskMode),
		"front_desk_hours":                   normalizeWxWorkFrontDeskHours(req.FrontDeskMode, req.FrontDeskHours),
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
	if err := s.syncRouteStateBindingFromInstance(updated, operator.Username); err != nil {
		return err
	}
	return s.syncStoreStaffBindingFromInstanceRequest(updated, req.ManagedMode, req.ServiceHours, req.StoreRoomConversationID, req.StoreRoomNotifyEnabled, req.StoreRoomAtList, req.FallbackToHQ, req.ManualTimeoutMinutes, operator)
}

func (s *wxWorkProtocolInstanceService) syncRouteStateBindingFromInstance(instance *models.WxWorkProtocolInstance, operatorName string) error {
	if instance == nil || instance.ID <= 0 {
		return nil
	}
	if strings.TrimSpace(operatorName) == "" {
		operatorName = "system"
	}
	return repositories.ConversationRouteStateRepository.UpdateBindingByWxWorkInstance(sqls.DB(), instance.ID, instance.StoreID, instance.KnowledgeBaseID, time.Now(), operatorName)
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

func (s *wxWorkProtocolInstanceService) normalizeCompanyStoreBinding(companyID int64, storeID int64, storeName string, fallbackName string, operator *dto.AuthPrincipal) (int64, int64, error) {
	if companyID < 0 {
		companyID = 0
	}
	if storeID > 0 {
		store := StoreService.Get(storeID)
		if store == nil || store.Status == enums.StatusDeleted {
			return 0, 0, errorsx.InvalidParam("门店不存在")
		}
		if companyID <= 0 {
			companyID = store.CompanyID
		}
	}
	resolvedStoreID, err := s.ensureStoreForCompany(companyID, storeID, storeName, fallbackName, operator)
	if err != nil {
		return 0, 0, err
	}
	if companyID <= 0 && resolvedStoreID > 0 {
		if store := StoreService.Get(resolvedStoreID); store != nil {
			companyID = store.CompanyID
		}
	}
	return companyID, resolvedStoreID, nil
}

func (s *wxWorkProtocolInstanceService) ensureStoreForCompany(companyID int64, storeID int64, storeName string, fallbackName string, operator *dto.AuthPrincipal) (int64, error) {
	if companyID < 0 {
		companyID = 0
	}
	if companyID > 0 {
		if company := CompanyService.Get(companyID); company == nil || company.Status == enums.StatusDeleted {
			return 0, errorsx.InvalidParam("公司不存在")
		}
	}
	name := utils.RepairMojibakeText(strings.TrimSpace(firstNonBlank(storeName, fallbackName)))
	now := time.Now()
	if storeID > 0 {
		store := StoreService.Get(storeID)
		if store == nil || store.Status == enums.StatusDeleted {
			return 0, errorsx.InvalidParam("门店不存在")
		}
		if companyID > 0 && store.CompanyID > 0 && store.CompanyID != companyID {
			return 0, errorsx.InvalidParam("门店不属于当前公司")
		}
		columns := map[string]any{}
		if companyID > 0 && store.CompanyID == 0 {
			columns["company_id"] = companyID
		}
		if name != "" && store.Name != name {
			columns["name"] = name
		}
		if len(columns) > 0 {
			columns["updated_at"] = now
			columns["update_user_id"] = auditUserID(operator)
			columns["update_user_name"] = auditUsername(operator)
			if err := repositories.StoreRepository.Updates(sqls.DB(), store.ID, columns); err != nil {
				return 0, err
			}
		}
		return store.ID, nil
	}
	if companyID <= 0 || name == "" {
		return 0, nil
	}
	item := &models.Store{
		StoreCode:   generateWxWorkInternalStoreCode(companyID),
		Name:        name,
		CompanyID:   companyID,
		Status:      enums.StatusOk,
		Remark:      "企微员工号开户自动生成的内部兼容门店记录",
		AuditFields: utils.BuildAuditFields(operator),
	}
	item.CreatedAt = now
	item.UpdatedAt = now
	if item.CreateUserID == 0 {
		item.CreateUserID = constants.SystemAuditUserID
		item.CreateUserName = constants.SystemAuditUserName
		item.UpdateUserID = constants.SystemAuditUserID
		item.UpdateUserName = constants.SystemAuditUserName
	}
	if err := repositories.StoreRepository.Create(sqls.DB(), item); err != nil {
		return 0, err
	}
	return item.ID, nil
}

func generateWxWorkInternalStoreCode(companyID int64) string {
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	if len(suffix) > 12 {
		suffix = suffix[:12]
	}
	return fmt.Sprintf("wxwork-%d-%s", companyID, suffix)
}

func (s *wxWorkProtocolInstanceService) BackfillCompanyIDFromStore() error {
	now := time.Now()
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		items := repositories.WxWorkProtocolInstanceRepository.Find(ctx.Tx, sqls.NewCnd().
			Eq("company_id", 0).
			Gt("store_id", 0).
			Where("status <> ?", enums.StatusDeleted).
			Asc("id"))
		for _, item := range items {
			store := repositories.StoreRepository.Get(ctx.Tx, item.StoreID)
			if store == nil || store.CompanyID <= 0 {
				continue
			}
			if err := repositories.WxWorkProtocolInstanceRepository.Updates(ctx.Tx, item.ID, map[string]any{
				"company_id":       store.CompanyID,
				"updated_at":       now,
				"update_user_id":   constants.SystemAuditUserID,
				"update_user_name": constants.SystemAuditUserName,
			}); err != nil {
				return err
			}
		}
		return nil
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

// MigrateDedicatedAIAgents is intentionally a no-op. Older versions created a
// hidden AIAgent per WeCom employee account; WeCom runtime now builds an
// in-memory profile from the instance, store, and model settings instead.
func (s *wxWorkProtocolInstanceService) MigrateDedicatedAIAgents() error {
	return nil
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

func normalizeWxWorkFrontDeskMode(value string) string {
	switch strings.TrimSpace(value) {
	case wxWorkFrontDeskModeStaffed:
		return wxWorkFrontDeskModeStaffed
	case wxWorkFrontDeskModeScheduled:
		return wxWorkFrontDeskModeScheduled
	default:
		return wxWorkFrontDeskModeUnmanned
	}
}

func normalizeWxWorkFrontDeskHours(mode, value string) string {
	if normalizeWxWorkFrontDeskMode(mode) != wxWorkFrontDeskModeScheduled {
		return ""
	}
	return strings.TrimSpace(value)
}

func appendWxWorkReceptionContext(systemPrompt string, instance *models.WxWorkProtocolInstance, now time.Time) string {
	if instance == nil {
		return strings.TrimSpace(systemPrompt)
	}
	mode := normalizeWxWorkFrontDeskMode(instance.FrontDeskMode)
	hours := normalizeWxWorkFrontDeskHours(mode, instance.FrontDeskHours)
	var contextText string
	switch mode {
	case wxWorkFrontDeskModeStaffed:
		contextText = "当前门店配置为有前台酒店。只有知识库或门店配置明确支持时，才可以引导客人去前台；经营模式本身不代表前台已经接单或能够完成具体动作。"
	case wxWorkFrontDeskModeScheduled:
		if hours == "" {
			contextText = "当前门店配置为分时段前台，但尚未配置有效时段。不得据此声称前台有人或引导客人去前台。"
		} else if isWithinStoreServiceHours(hours, now) {
			contextText = fmt.Sprintf("当前门店配置为分时段前台，前台时段为 %s，当前处于该时段。仍只有知识库或门店配置明确支持时，才可以引导客人去前台；这不代表前台已经接单。", hours)
		} else {
			contextText = fmt.Sprintf("当前门店配置为分时段前台，前台时段为 %s，当前不在该时段。不得声称前台有人或引导客人去前台。", hours)
		}
	default:
		contextText = "当前门店为无人化酒店，不设常驻前台。不得无依据引导客人去前台、声称前台有人或暂时离开，也不得承诺前台处理；需要人工时只能使用系统已有接待路由。"
	}
	return strings.TrimSpace(systemPrompt) + "\n\n门店接待模式：\n" + contextText
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

func (s *wxWorkProtocolInstanceService) claimStaleProtocolDeviceGUID(channel *models.Channel) (string, error) {
	if channel == nil {
		return "", errorsx.InvalidParam("企微协议渠道不存在")
	}
	now := time.Now()
	candidates := repositories.WxWorkProtocolDevicePoolRepository.Find(sqls.DB(), sqls.NewCnd().Eq("status", enums.StatusOk).Asc("id"))
	for _, candidate := range candidates {
		guid := normalizeProtocolDeviceGUID(candidate.Guid)
		if guid == "" || devicePoolExpired(candidate.ExpiredAt, now) || candidate.BoundWxWorkProtocolInstanceID <= 0 {
			continue
		}
		instance := s.Get(candidate.BoundWxWorkProtocolInstanceID)
		if instance != nil && s.canReleaseLoginBinding(instance, now) {
			return guid, nil
		}
	}
	items := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().NotEq("status", enums.StatusDeleted).Asc("id"))
	for _, item := range items {
		if s.canReleaseLoginBinding(&item, now) {
			guid := normalizeProtocolDeviceGUID(item.Guid)
			if guid != "" {
				return guid, nil
			}
		}
	}
	return "", errorsx.InvalidParam("没有找到可自动清理的未登录临时占用实例")
}

func (s *wxWorkProtocolInstanceService) canReuseForLogin(instance *models.WxWorkProtocolInstance, now time.Time) bool {
	if instance == nil || instance.Status == enums.StatusDeleted {
		return false
	}
	health := strings.TrimSpace(instance.HealthStatus)
	if health != "login_qrcode" && health != "remote_setup" && health != "pending_binding" {
		return false
	}
	return strings.TrimSpace(instance.EmployeeUserID) == "" && strings.TrimSpace(instance.EmployeeName) == ""
}

func (s *wxWorkProtocolInstanceService) canReleaseLoginBinding(instance *models.WxWorkProtocolInstance, now time.Time) bool {
	if instance == nil || instance.Status == enums.StatusDeleted {
		return false
	}
	health := strings.TrimSpace(instance.HealthStatus)
	if health != "login_qrcode" && health != "remote_setup" && health != "pending_binding" && health != "unknown" {
		return false
	}
	if strings.TrimSpace(instance.EmployeeUserID) != "" || strings.TrimSpace(instance.EmployeeName) != "" {
		return false
	}
	if instance.RemoteSetupSubmittedAt != nil {
		return false
	}
	if s.hasProtocolConversation(instance) {
		return false
	}
	if health == "login_qrcode" || health == "remote_setup" {
		return now.Sub(instance.CreatedAt) > 2*time.Minute
	}
	return true
}

func (s *wxWorkProtocolInstanceService) hasProtocolConversation(instance *models.WxWorkProtocolInstance) bool {
	if instance == nil {
		return false
	}
	prefix := "wx_protocol:" + strings.TrimSpace(instance.Guid) + ":"
	return repositories.WxWorkKFConversationRepository.Count(sqls.DB(), sqls.NewCnd().Eq("channel_id", instance.ChannelID).Like("open_kf_id", prefix+"%")) > 0
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
	return utils.RepairMojibakeText(strings.TrimSpace(value))
}

func validateWxWorkWelcomeImageAsset(value string) (string, error) {
	assetID := strings.TrimSpace(value)
	if assetID == "" {
		return "", nil
	}
	asset := AssetService.GetByAssetID(assetID)
	if asset == nil || asset.Status != enums.AssetStatusSuccess {
		return "", errorsx.InvalidParam("欢迎语图片不存在或尚未上传完成")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(asset.MimeType)), "image/") {
		return "", errorsx.InvalidParam("欢迎语资源必须是图片")
	}
	return assetID, nil
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
		return 0, 0, errorsx.InvalidParam("企微员工号未配置内部门店兼容记录或知识库")
	}
	return instance.StoreID, instance.KnowledgeBaseID, nil
}

func (s *wxWorkProtocolInstanceService) resolveEffectiveIntentProfileID(companyID, instanceIntentProfileID int64, requireProfile bool) (int64, error) {
	if instanceIntentProfileID > 0 {
		if _, err := validateOptionalReplyIntentProfileID(instanceIntentProfileID); err != nil {
			return 0, err
		}
	}
	if companyID > 0 {
		company := CompanyService.Get(companyID)
		if company == nil || company.Status == enums.StatusDeleted {
			return 0, errorsx.InvalidParam("公司不存在")
		}
		companyIntentProfileID, err := validateOptionalReplyIntentProfileID(company.IntentProfileID)
		if err != nil {
			return 0, err
		}
		if companyIntentProfileID > 0 {
			if instanceIntentProfileID > 0 && instanceIntentProfileID != companyIntentProfileID {
				return 0, errorsx.InvalidParam("员工号行业 Profile 必须与绑定公司一致")
			}
			return companyIntentProfileID, nil
		}
	}
	if instanceIntentProfileID > 0 {
		return instanceIntentProfileID, nil
	}
	if requireProfile {
		return 0, errorsx.InvalidParam("启用 AI 前请先为公司或企微员工号绑定行业 Profile")
	}
	return 0, nil
}

func (s *wxWorkProtocolInstanceService) validateBinding(channelID, storeID, knowledgeBaseID, effectiveIntentProfileID int64) error {
	if err := s.validateProtocolChannel(channelID); err != nil {
		return err
	}
	if storeID > 0 {
		store := StoreService.Get(storeID)
		if store == nil || store.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("门店不存在")
		}
	}
	if knowledgeBaseID > 0 {
		knowledgeBase := KnowledgeBaseService.Get(knowledgeBaseID)
		if knowledgeBase == nil || knowledgeBase.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("知识库不存在")
		}
		if knowledgeBase.StoreID > 0 && storeID > 0 && knowledgeBase.StoreID != storeID {
			return errorsx.InvalidParam("只能绑定当前门店自己的知识库")
		}
		if knowledgeBase.CompanyID > 0 && storeID > 0 {
			store := StoreService.Get(storeID)
			if store != nil && store.CompanyID > 0 && store.CompanyID != knowledgeBase.CompanyID {
				return errorsx.InvalidParam("只能绑定当前公司下门店的知识库")
			}
		}
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
