package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
	"github.com/spf13/cast"
	"gorm.io/gorm"
)

var WxWorkProtocolDevicePoolService = newWxWorkProtocolDevicePoolService()
var wxWorkProtocolGatewayURL = "https://chat-api.juhebot.com/open/GuidRequest"

const (
	wxWorkDevicePoolConfigGroup           = "wxwork_protocol_device_pool"
	wxWorkDevicePoolConfigAdminBaseURL    = "wxwork_protocol_device_pool.admin_base_url"
	wxWorkDevicePoolConfigCallbackBaseURL = "wxwork_protocol_device_pool.callback_base_url"
	wxWorkDevicePoolConfigUsername        = "wxwork_protocol_device_pool.username"
	wxWorkDevicePoolConfigPassword        = "wxwork_protocol_device_pool.password"
	wxWorkDevicePoolConfigToken           = "wxwork_protocol_device_pool.token"
	wxWorkDevicePoolConfigTokenExpire     = "wxwork_protocol_device_pool.token_expire_at"
	defaultWxWorkDevicePoolAdminBaseURL   = "https://chat-api.juhebot.com"
	wxWorkDevicePoolTemporaryHoldTTL      = 30 * time.Minute
)

func newWxWorkProtocolDevicePoolService() *wxWorkProtocolDevicePoolService {
	return &wxWorkProtocolDevicePoolService{httpClient: &http.Client{Timeout: 20 * time.Second}}
}

type wxWorkProtocolDevicePoolService struct {
	httpClient *http.Client
}

type WxWorkProtocolLoginAvailability struct {
	ExpiresAt *time.Time
	Expired   bool
	Available bool
	Reason    string
}

type wxWorkDevicePoolSettings struct {
	AdminBaseURL    string
	CallbackBaseURL string
	Username        string
	Password        string
	Token           string
	TokenExpire     *time.Time
}

type wxWorkProtocolProviderCredential struct {
	AppKey    string
	AppSecret string
}

type wxWorkAdminListInstanceItem struct {
	ID         int64           `json:"id"`
	Guid       string          `json:"guid"`
	Uin        string          `json:"uin"`
	UserID     int64           `json:"user_id"`
	ClientType int             `json:"client_type"`
	ExpiredAt  int64           `json:"expired_at"`
	SeatName   string          `json:"seat_name"`
	BridgeID   string          `json:"bridge_id"`
	State      string          `json:"state"`
	Raw        json.RawMessage `json:"-"`
}

func (s *wxWorkProtocolDevicePoolService) Get(id int64) *models.WxWorkProtocolDevicePoolInstance {
	if id <= 0 {
		return nil
	}
	return repositories.WxWorkProtocolDevicePoolRepository.Get(sqls.DB(), id)
}

func (s *wxWorkProtocolDevicePoolService) LoginAvailability(instance *models.WxWorkProtocolInstance, now time.Time) WxWorkProtocolLoginAvailability {
	ret := WxWorkProtocolLoginAvailability{Available: true}
	if instance == nil || instance.ID <= 0 {
		return ret
	}
	pool := repositories.WxWorkProtocolDevicePoolRepository.Take(
		sqls.DB(),
		"bound_wx_work_protocol_instance_id = ?",
		instance.ID,
	)
	if pool == nil && strings.TrimSpace(instance.Guid) != "" {
		pool = repositories.WxWorkProtocolDevicePoolRepository.Take(
			sqls.DB(),
			"guid = ?",
			strings.TrimSpace(instance.Guid),
		)
	}
	if pool == nil {
		return ret
	}
	ret.ExpiresAt = pool.ExpiredAt
	ret.Expired = devicePoolExpired(pool.ExpiredAt, now) ||
		strings.EqualFold(strings.TrimSpace(pool.SyncStatus), "expired") ||
		strings.EqualFold(strings.TrimSpace(pool.State), "expired")
	if ret.Expired {
		ret.Available = false
		ret.Reason = wxWorkProtocolSeatExpiredMessage
	}
	return ret
}

func (s *wxWorkProtocolDevicePoolService) FindPageByCnd(cnd *sqls.Cnd) ([]models.WxWorkProtocolDevicePoolInstance, *sqls.Paging) {
	return repositories.WxWorkProtocolDevicePoolRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *wxWorkProtocolDevicePoolService) FindPageByParams(params *params.QueryParams) ([]models.WxWorkProtocolDevicePoolInstance, *sqls.Paging) {
	return repositories.WxWorkProtocolDevicePoolRepository.FindPageByParams(sqls.DB(), params)
}

func (s *wxWorkProtocolDevicePoolService) Settings() response.WxWorkProtocolDevicePoolSettingsResponse {
	settings := s.loadSettings()
	return response.WxWorkProtocolDevicePoolSettingsResponse{
		AdminBaseURL:    settings.AdminBaseURL,
		CallbackBaseURL: settings.CallbackBaseURL,
		Username:        settings.Username,
		PasswordSet:     strings.TrimSpace(settings.Password) != "",
		TokenSet:        strings.TrimSpace(settings.Token) != "",
		TokenExpireAt:   settings.TokenExpire,
	}
}

func (s *wxWorkProtocolDevicePoolService) UpdateSettings(req request.UpdateWxWorkProtocolDevicePoolSettingsRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	baseURL := normalizeDevicePoolAdminBaseURL(req.AdminBaseURL)
	callbackBaseURL, err := normalizeWxWorkProtocolCallbackBaseURL(req.CallbackBaseURL)
	if err != nil {
		return err
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return errorsx.InvalidParam("聚合智能后台账号不能为空")
	}
	if err := s.upsertConfig(wxWorkDevicePoolConfigAdminBaseURL, baseURL, "聚合智能后台 API 地址", "用于同步 XBot 实例池", operator); err != nil {
		return err
	}
	if err := s.upsertConfig(wxWorkDevicePoolConfigCallbackBaseURL, callbackBaseURL, "企微协议公开回调地址", "真实员工号消息回调使用的 HTTPS 公网地址", operator); err != nil {
		return err
	}
	if err := s.upsertConfig(wxWorkDevicePoolConfigUsername, username, "聚合智能后台账号", "用于登录后台同步实例池", operator); err != nil {
		return err
	}
	if strings.TrimSpace(req.Password) != "" {
		if err := s.upsertConfig(wxWorkDevicePoolConfigPassword, strings.TrimSpace(req.Password), "聚合智能后台密码", "运行时加密环境外的数据库配置，接口读取时不返回明文", operator); err != nil {
			return err
		}
		_ = s.upsertConfig(wxWorkDevicePoolConfigToken, "", "聚合智能后台 Token", "登录后自动刷新", operator)
		_ = s.upsertConfig(wxWorkDevicePoolConfigTokenExpire, "", "聚合智能后台 Token 过期时间", "登录后自动刷新", operator)
	}
	return nil
}

func (s *wxWorkProtocolDevicePoolService) Sync(operator *dto.AuthPrincipal) (*response.WxWorkProtocolDevicePoolSyncResponse, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	items, err := s.fetchAdminInstances(operator)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		for _, remote := range items {
			guid := normalizeProtocolDeviceGUID(remote.Guid)
			if guid == "" {
				continue
			}
			raw := strings.TrimSpace(string(remote.Raw))
			expiredAt := unixSecondsPtr(remote.ExpiredAt)
			syncStatus := devicePoolSyncStatus(remote.Uin, remote.State, expiredAt, now)
			existing := repositories.WxWorkProtocolDevicePoolRepository.Take(ctx.Tx, "guid = ?", guid)
			columns := map[string]any{
				"provider_instance_id": remote.ID,
				"uin":                  strings.TrimSpace(remote.Uin),
				"provider_user_id":     remote.UserID,
				"client_type":          remote.ClientType,
				"seat_name":            strings.TrimSpace(remote.SeatName),
				"bridge_id":            strings.TrimSpace(remote.BridgeID),
				"state":                strings.TrimSpace(remote.State),
				"expired_at":           expiredAt,
				"sync_status":          syncStatus,
				"last_synced_at":       now,
				"raw_json":             raw,
				"status":               enums.StatusOk,
				"update_user_id":       operator.UserID,
				"update_user_name":     operator.Username,
				"updated_at":           now,
			}
			if existing != nil {
				if err := repositories.WxWorkProtocolDevicePoolRepository.Updates(ctx.Tx, existing.ID, columns); err != nil {
					return err
				}
				continue
			}
			item := &models.WxWorkProtocolDevicePoolInstance{
				Guid:        guid,
				AuditFields: utils.BuildAuditFields(operator),
			}
			item.CreatedAt = now
			item.UpdatedAt = now
			if err := repositories.WxWorkProtocolDevicePoolRepository.Create(ctx.Tx, item); err != nil {
				return err
			}
			if err := repositories.WxWorkProtocolDevicePoolRepository.Updates(ctx.Tx, item.ID, columns); err != nil {
				return err
			}
		}
		return s.refreshLocalBindings(ctx.Tx, now, operator)
	}); err != nil {
		return nil, err
	}
	return s.syncSummary(), nil
}

func (s *wxWorkProtocolDevicePoolService) AdoptionOptions(operator *dto.AuthPrincipal) ([]response.WxWorkProtocolAdoptionOptionResponse, error) {
	if operator == nil || !operator.IsPlatformAccount {
		return nil, errorsx.Forbidden("只有平台账号可以接入真实企微员工号")
	}
	bindings := repositories.StoreStaffBindingRepository.Find(
		sqls.DB(),
		sqls.NewCnd().Eq("status", enums.StatusOk).Asc("tenant_id").Asc("store_id").Asc("id"),
	)
	result := make([]response.WxWorkProtocolAdoptionOptionResponse, 0, len(bindings))
	for i := range bindings {
		binding := &bindings[i]
		if binding.TenantID <= 0 || binding.StoreID <= 0 || binding.UserID <= 0 {
			continue
		}
		tenant := repositories.TenantRepository.Get(sqls.DB(), binding.TenantID)
		store := repositories.StoreRepository.GetInTenant(sqls.DB(), binding.StoreID, binding.TenantID)
		user := repositories.UserRepository.GetInTenant(sqls.DB(), binding.UserID, binding.TenantID)
		if tenant == nil || tenant.Status != enums.StatusOk || store == nil || store.Status != enums.StatusOk ||
			user == nil || user.Status != enums.StatusOk || user.DeletedAt != nil {
			continue
		}
		result = append(result, response.WxWorkProtocolAdoptionOptionResponse{
			TenantID:            tenant.ID,
			TenantName:          firstNonBlank(tenant.ShortName, tenant.LegalName, tenant.TenantCode),
			StoreID:             store.ID,
			StoreName:           firstNonBlank(store.Name, store.StoreCode),
			StoreStaffBindingID: binding.ID,
			StoreStaffUserID:    user.ID,
			StoreStaffUserName:  firstNonBlank(user.Nickname, user.Username),
		})
	}
	return result, nil
}

func (s *wxWorkProtocolDevicePoolService) Adopt(req request.AdoptWxWorkProtocolDevicePoolRequest, operator *dto.AuthPrincipal) (*response.WxWorkProtocolAdoptionResponse, error) {
	if operator == nil || !operator.IsPlatformAccount {
		return nil, errorsx.Forbidden("只有平台账号可以接入真实企微员工号")
	}
	if req.DevicePoolID <= 0 || req.TenantID <= 0 || req.StoreStaffBindingID <= 0 {
		return nil, errorsx.InvalidParam("请选择真实实例和门店员工号")
	}
	settings := s.loadSettings()
	callbackBaseURL, err := normalizeWxWorkProtocolCallbackBaseURL(settings.CallbackBaseURL)
	if err != nil || callbackBaseURL == "" {
		return nil, errorsx.InvalidParam("请先配置有效的 HTTPS 企微协议公开回调地址")
	}
	pool := repositories.WxWorkProtocolDevicePoolRepository.Get(sqls.DB(), req.DevicePoolID)
	if err := validateAdoptableDevicePoolInstance(pool, time.Now()); err != nil {
		return nil, err
	}

	credential, err := s.fetchProviderCredential(operator)
	if err != nil {
		return nil, err
	}
	probeConfig := &dto.WxWorkProtocolChannelConfig{
		AppKey:        credential.AppKey,
		AppSecret:     credential.AppSecret,
		BaseURL:       wxWorkProtocolGatewayURL,
		DevicePoolURL: settings.AdminBaseURL,
	}
	profileRaw, err := WxWorkProtocolService.postJSON(probeConfig, "/user/get_profile", map[string]any{"guid": pool.Guid})
	if err != nil {
		return nil, errorsx.InvalidParam("真实企微实例当前未通过账号资料接口验证，请确认实例在线")
	}
	profileUpdates := WxWorkProtocolService.profileUpdatesFromResponse(profileRaw)

	var instance *models.WxWorkProtocolInstance
	var store *models.Store
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedPool := repositories.WxWorkProtocolDevicePoolRepository.GetForUpdate(ctx.Tx, req.DevicePoolID)
		if err := validateAdoptableDevicePoolInstance(lockedPool, time.Now()); err != nil {
			return err
		}
		binding, err := repositories.StoreStaffBindingRepository.GetForUpdateInTenant(ctx.Tx, req.StoreStaffBindingID, req.TenantID)
		if err != nil {
			return err
		}
		if binding == nil || binding.Status != enums.StatusOk || binding.StoreID <= 0 || binding.UserID <= 0 {
			return errorsx.InvalidParam("门店员工号绑定不存在或已停用")
		}
		tenant := repositories.TenantRepository.Get(ctx.Tx, req.TenantID)
		store = repositories.StoreRepository.GetInTenant(ctx.Tx, binding.StoreID, req.TenantID)
		user := repositories.UserRepository.GetInTenant(ctx.Tx, binding.UserID, req.TenantID)
		if tenant == nil || tenant.Status != enums.StatusOk || store == nil || store.Status != enums.StatusOk ||
			user == nil || user.Status != enums.StatusOk || user.DeletedAt != nil {
			return errorsx.InvalidParam("接入公司、门店或门店员工账号不存在或已停用")
		}

		if lockedPool.BoundWxWorkProtocolInstanceID > 0 {
			existing := repositories.WxWorkProtocolInstanceRepository.GetForUpdate(ctx.Tx, lockedPool.BoundWxWorkProtocolInstanceID)
			if existing == nil || existing.Status == enums.StatusDeleted {
				return errorsx.InvalidParam("实例池绑定记录异常，请先重新同步实例池")
			}
			if existing.TenantID != req.TenantID || existing.StoreStaffBindingID != binding.ID || existing.Guid != lockedPool.Guid {
				return errorsx.InvalidParam("真实企微实例已绑定到其他接入公司或门店")
			}
			instance = existing
			return nil
		}
		if existing := repositories.WxWorkProtocolInstanceRepository.Take(
			ctx.Tx,
			"guid = ? AND status <> ?",
			lockedPool.Guid,
			enums.StatusDeleted,
		); existing != nil {
			if existing.TenantID != req.TenantID || existing.StoreStaffBindingID != binding.ID {
				return errorsx.InvalidParam("真实企微实例已绑定到其他接入公司或门店")
			}
			instance = existing
			return repositories.WxWorkProtocolDevicePoolRepository.Updates(ctx.Tx, lockedPool.ID, map[string]any{
				"bound_wx_work_protocol_instance_id": existing.ID,
				"sync_status":                        "bound",
				"updated_at":                         time.Now(),
				"update_user_id":                     operator.UserID,
				"update_user_name":                   operator.Username,
			})
		}
		if existing := repositories.WxWorkProtocolInstanceRepository.Take(
			ctx.Tx,
			"(store_staff_binding_id = ? OR (tenant_id = ? AND store_id = ?)) AND status <> ?",
			binding.ID,
			req.TenantID,
			binding.StoreID,
			enums.StatusDeleted,
		); existing != nil {
			return errorsx.InvalidParam("该门店已绑定其他企微员工号实例")
		}

		channel, err := s.ensureTenantProtocolChannel(ctx.Tx, tenant, credential, settings, operator)
		if err != nil {
			return err
		}
		now := time.Now()
		instance = &models.WxWorkProtocolInstance{
			TenantID:                  req.TenantID,
			AgentTeamID:               binding.AgentTeamID,
			Guid:                      lockedPool.Guid,
			ChannelID:                 channel.ID,
			StoreID:                   binding.StoreID,
			StoreStaffBindingID:       binding.ID,
			KnowledgeBaseID:           store.KnowledgeBaseID,
			ServiceHours:              binding.ServiceHours,
			StoreRoomConversationID:   binding.StoreRoomConversationID,
			StoreRoomNotifyEnabled:    binding.StoreRoomNotifyEnabled,
			StoreRoomAtList:           binding.StoreRoomAtList,
			FallbackToHQ:              binding.FallbackToHQ,
			ManualTimeoutMinutes:      normalizeManualTimeoutMinutes(binding.ManualTimeoutMinutes),
			AIReplyEnabled:            true,
			WelcomeEnabled:            true,
			WelcomeSendMiniProgram:    true,
			WelcomeAskLocation:        true,
			PersonaPrompt:             DefaultWxWorkProtocolPersonaPrompt,
			ContextMaxMessages:        30,
			ContextMaxTokens:          8000,
			ContextCompressionEnabled: true,
			HealthStatus:              "online",
			LastHeartbeatAt:           &now,
			Status:                    enums.StatusOk,
			Remark:                    "由平台实例池接入真实企微员工号",
			AuditFields:               utils.BuildAuditFields(operator),
		}
		applyProtocolProfileUpdates(instance, profileUpdates)
		instance.CreatedAt = now
		instance.UpdatedAt = now
		if err := repositories.WxWorkProtocolInstanceRepository.Create(ctx.Tx, instance); err != nil {
			return err
		}
		return repositories.WxWorkProtocolDevicePoolRepository.Updates(ctx.Tx, lockedPool.ID, map[string]any{
			"bound_wx_work_protocol_instance_id": instance.ID,
			"sync_status":                        "bound",
			"remark":                             "已接入 AgentDesk，正在配置真实消息回调",
			"updated_at":                         now,
			"update_user_id":                     operator.UserID,
			"update_user_name":                   operator.Username,
		})
	})
	if err != nil {
		return nil, err
	}
	if instance == nil || store == nil {
		return nil, errorsx.BusinessError(1, "真实企微实例接入结果不完整")
	}
	callbackURL, err := buildWxWorkProtocolCallbackURL(callbackBaseURL, instance.ChannelID)
	if err != nil {
		return nil, err
	}
	if err := WxWorkProtocolService.SetNotifyURL(instance.ID, callbackURL); err != nil {
		_ = repositories.WxWorkProtocolDevicePoolRepository.Updates(sqls.DB(), req.DevicePoolID, map[string]any{
			"sync_status": "callback_error",
			"remark":      "门店绑定已完成，真实消息回调配置失败，可重试接入",
			"updated_at":  time.Now(),
		})
		return nil, fmt.Errorf("门店绑定已完成，但配置真实消息回调失败: %w", err)
	}
	_ = repositories.WxWorkProtocolDevicePoolRepository.Updates(sqls.DB(), req.DevicePoolID, map[string]any{
		"sync_status": "bound",
		"remark":      "真实企微员工号已接入，消息回调已配置",
		"updated_at":  time.Now(),
	})
	return &response.WxWorkProtocolAdoptionResponse{
		DevicePoolID:        req.DevicePoolID,
		InstanceID:          instance.ID,
		TenantID:            instance.TenantID,
		StoreID:             instance.StoreID,
		StoreStaffBindingID: instance.StoreStaffBindingID,
		EmployeeName:        firstNonBlank(instance.EmployeeName, instance.EmployeeUserID),
		StoreName:           store.Name,
		NotifyConfigured:    true,
	}, nil
}

func (s *wxWorkProtocolDevicePoolService) RepairMessages(req request.RepairWxWorkProtocolMessagesRequest, operator *dto.AuthPrincipal) (*response.WxWorkProtocolRepairResponse, error) {
	if operator == nil || !operator.IsPlatformAccount {
		return nil, errorsx.Forbidden("只有平台账号可以修复企微员工号漏消息")
	}
	pool := repositories.WxWorkProtocolDevicePoolRepository.Get(sqls.DB(), req.ID)
	if pool == nil || pool.BoundWxWorkProtocolInstanceID <= 0 {
		return nil, errorsx.InvalidParam("实例池记录未绑定企微员工号")
	}
	instance := repositories.WxWorkProtocolInstanceRepository.Get(sqls.DB(), pool.BoundWxWorkProtocolInstanceID)
	if instance == nil || instance.TenantID <= 0 || instance.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("企微员工号实例不存在或未启用")
	}
	syncKey := strings.TrimSpace(instance.MessageGapFromSeq)
	if syncKey == "" || syncKey == "0" {
		return nil, errorsx.InvalidParam("当前没有可修复的消息序列缺口")
	}
	if instance.MessageRepairLastAt != nil && time.Since(*instance.MessageRepairLastAt) < time.Minute {
		return nil, errorsx.InvalidParam("补漏接口受协议限频，请一分钟后再试")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 100 {
		limit = 100
	}
	now := time.Now()
	if err := repositories.WxWorkProtocolInstanceRepository.UpdatesInTenant(sqls.DB(), instance.ID, instance.TenantID, map[string]any{
		"message_repair_last_at":    now,
		"message_repair_last_error": "",
		"updated_at":                now,
		"update_user_id":            operator.UserID,
		"update_user_name":          operator.Username,
	}); err != nil {
		return nil, err
	}
	_, err := WxWorkProtocolService.callInstanceAPI(instance.ID, "/sync/sync_msg", map[string]any{
		"sync_key": syncKey,
		"limit":    limit,
	}, nil)
	if err != nil {
		_ = repositories.WxWorkProtocolInstanceRepository.UpdatesInTenant(sqls.DB(), instance.ID, instance.TenantID, map[string]any{
			"message_repair_last_error": "协议补漏请求失败",
			"updated_at":                time.Now(),
		})
		return nil, err
	}
	return &response.WxWorkProtocolRepairResponse{InstanceID: instance.ID, SyncKey: syncKey, Limit: limit}, nil
}

func (s *wxWorkProtocolDevicePoolService) ClaimAvailableGUID(channel *models.Channel) (string, error) {
	if channel == nil || channel.Status != enums.StatusOk || channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
		return "", errorsx.InvalidParam("企微协议渠道不存在或未启用")
	}
	cfg, err := ChannelService.ParseWxWorkProtocolChannelConfig(channel.ConfigJSON)
	if err != nil {
		return "", errorsx.InvalidParam("企微协议渠道配置不合法")
	}
	settings := s.loadSettings()
	if strings.TrimSpace(settings.Username) == "" || strings.TrimSpace(settings.Password) == "" {
		return "", errorsx.InvalidParam("请先在系统管理 > 实例池配置聚合智能后台账号并同步设备列表")
	}
	bound := WxWorkProtocolInstanceService.boundProtocolGUIDs()
	candidates := repositories.WxWorkProtocolDevicePoolRepository.Find(sqls.DB(), sqls.NewCnd().Eq("status", enums.StatusOk).Asc("id"))
	if len(candidates) == 0 {
		return "", errorsx.InvalidParam("实例池暂无实例，请先同步设备列表或初始化新实例")
	}
	var lastErr string
	onlineCount := 0
	for _, candidate := range candidates {
		guid := normalizeProtocolDeviceGUID(candidate.Guid)
		if guid == "" || bound[guid] || candidate.BoundWxWorkProtocolInstanceID > 0 || devicePoolExpired(candidate.ExpiredAt, time.Now()) {
			continue
		}
		if candidate.SyncStatus == "idle" {
			return guid, nil
		}
		raw, profileErr := WxWorkProtocolService.postJSON(cfg, "/user/get_profile", map[string]any{"guid": guid})
		if profileErr == nil {
			onlineCount++
			_ = repositories.WxWorkProtocolDevicePoolRepository.Updates(sqls.DB(), candidate.ID, map[string]any{
				"sync_status": "online",
				"remark":      "账号资料接口确认当前实例在线，未进入扫码认领",
				"updated_at":  time.Now(),
			})
			continue
		}
		if protocolProfileResponseShowsOffline(raw) || wxWorkProtocolResponseErrorCode(raw) == 1014 {
			remark := "账号资料接口确认实例离线，可在认领后配置异地代理并扫码"
			if wxWorkProtocolResponseErrorCode(raw) == 1014 {
				remark = "实例有效但登录环境未启动，可在认领后配置异地代理并扫码"
			}
			_ = repositories.WxWorkProtocolDevicePoolRepository.Updates(sqls.DB(), candidate.ID, map[string]any{
				"sync_status": "idle",
				"remark":      remark,
				"updated_at":  time.Now(),
			})
			return guid, nil
		}
		lastErr = profileErr.Error()
		_ = repositories.WxWorkProtocolDevicePoolRepository.Updates(sqls.DB(), candidate.ID, map[string]any{
			"sync_status": "unavailable",
			"remark":      lastErr,
			"updated_at":  time.Now(),
		})
	}
	if lastErr != "" {
		return "", errorsx.InvalidParam("实例池未找到可扫码实例，最后一次探测错误：" + lastErr)
	}
	if onlineCount > 0 {
		return "", errorsx.InvalidParam("实例池中的未绑定实例当前均已登录，请先准备离线实例再扫码")
	}
	return "", errorsx.InvalidParam("实例池里的空闲实例均已被本地绑定或已过期")
}

func protocolProfileResponseShowsOffline(raw string) bool {
	root := struct {
		ErrCode      int    `json:"err_code"`
		ErrMsg       string `json:"err_msg"`
		ErrorCode    int    `json:"error_code"`
		ErrorMessage string `json:"error_message"`
		Message      string `json:"message"`
	}{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &root); err != nil {
		return false
	}
	code := root.ErrCode
	if code == 0 {
		code = root.ErrorCode
	}
	message := strings.ToLower(firstNonBlank(root.ErrMsg, root.ErrorMessage, root.Message))
	return code == 1002 && (strings.Contains(message, "offline") || strings.Contains(message, "-102") || strings.Contains(message, "离线"))
}

func (s *wxWorkProtocolDevicePoolService) BindGUIDToInstance(guid string, instanceID int64) error {
	guid = normalizeProtocolDeviceGUID(guid)
	if guid == "" || instanceID <= 0 {
		return nil
	}
	return repositories.WxWorkProtocolDevicePoolRepository.UpdateByGUID(sqls.DB(), guid, map[string]any{
		"bound_wx_work_protocol_instance_id": instanceID,
		"sync_status":                        "bound",
		"updated_at":                         time.Now(),
		"update_user_name":                   wxWorkProtocolSystemOperatorName,
	})
}

func (s *wxWorkProtocolDevicePoolService) ReleaseGUIDBinding(guid string) error {
	guid = normalizeProtocolDeviceGUID(guid)
	if guid == "" {
		return nil
	}
	return repositories.WxWorkProtocolDevicePoolRepository.UpdateByGUID(sqls.DB(), guid, map[string]any{
		"bound_wx_work_protocol_instance_id": 0,
		"sync_status":                        "idle",
		"remark":                             "已清理未登录临时占用，可重新扫码绑定",
		"updated_at":                         time.Now(),
		"update_user_name":                   wxWorkProtocolSystemOperatorName,
	})
}

func (s *wxWorkProtocolDevicePoolService) fetchAdminInstances(operator *dto.AuthPrincipal) ([]wxWorkAdminListInstanceItem, error) {
	token, settings, err := s.ensureAdminToken(operator)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"page":        1,
		"page_size":   200,
		"current":     1,
		"pageSize":    200,
		"list_option": map[string]any{"page": 1, "page_size": 200},
	}
	raw, err := s.postAdminJSON(settings.AdminBaseURL, "/admin/ListInstance", token, body)
	if err != nil && strings.Contains(err.Error(), "HTTP 401") {
		_ = s.upsertConfig(wxWorkDevicePoolConfigToken, "", "聚合智能后台 Token", "登录后自动刷新", operator)
		token, settings, err = s.ensureAdminToken(operator)
		if err != nil {
			return nil, err
		}
		raw, err = s.postAdminJSON(settings.AdminBaseURL, "/admin/ListInstance", token, body)
	}
	if err != nil {
		return nil, err
	}
	return parseAdminListInstanceResponse(raw)
}

func (s *wxWorkProtocolDevicePoolService) ensureAdminToken(operator *dto.AuthPrincipal) (string, wxWorkDevicePoolSettings, error) {
	settings := s.loadSettings()
	if strings.TrimSpace(settings.Username) == "" || strings.TrimSpace(settings.Password) == "" {
		return "", settings, errorsx.InvalidParam("请先配置聚合智能后台账号和密码")
	}
	if strings.TrimSpace(settings.Token) != "" && settings.TokenExpire != nil && settings.TokenExpire.After(time.Now().Add(1*time.Minute)) {
		return settings.Token, settings, nil
	}
	raw, err := s.postAdminJSON(settings.AdminBaseURL, "/admin/login", "", map[string]any{
		"username": settings.Username,
		"password": settings.Password,
	})
	if err != nil {
		return "", settings, err
	}
	root := map[string]any{}
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", settings, fmt.Errorf("聚合智能登录响应无法解析: %w", err)
	}
	if code := cast.ToInt(root["code"]); code != 200 {
		return "", settings, errorsx.InvalidParam("聚合智能登录失败：" + firstNonBlank(cast.ToString(root["message"]), strings.TrimSpace(string(raw))))
	}
	token := strings.TrimSpace(cast.ToString(root["token"]))
	if token == "" {
		return "", settings, errorsx.InvalidParam("聚合智能登录响应未返回 token")
	}
	expireAt := parseFlexibleTime(cast.ToString(root["expire"]))
	_ = s.upsertConfig(wxWorkDevicePoolConfigToken, token, "聚合智能后台 Token", "登录后自动刷新", operator)
	if expireAt != nil {
		_ = s.upsertConfig(wxWorkDevicePoolConfigTokenExpire, expireAt.Format(time.RFC3339), "聚合智能后台 Token 过期时间", "登录后自动刷新", operator)
	}
	settings.Token = token
	settings.TokenExpire = expireAt
	return token, settings, nil
}

func (s *wxWorkProtocolDevicePoolService) postAdminJSON(baseURL, path, token string, body any) ([]byte, error) {
	baseURL = normalizeDevicePoolAdminBaseURL(baseURL)
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求聚合智能后台失败: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return respBody, fmt.Errorf("聚合智能后台接口返回HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}

func (s *wxWorkProtocolDevicePoolService) refreshLocalBindings(db *gorm.DB, now time.Time, operator *dto.AuthPrincipal) error {
	if err := db.Model(&models.WxWorkProtocolDevicePoolInstance{}).Where("status <> ?", enums.StatusDeleted).Updates(map[string]any{
		"bound_wx_work_protocol_instance_id": 0,
		"updated_at":                         now,
		"update_user_id":                     operator.UserID,
		"update_user_name":                   operator.Username,
	}).Error; err != nil {
		return err
	}
	instances := repositories.WxWorkProtocolInstanceRepository.Find(db, sqls.NewCnd().NotEq("status", enums.StatusDeleted))
	for _, instance := range instances {
		guid := normalizeProtocolDeviceGUID(instance.Guid)
		if guid == "" || !wxWorkProtocolInstanceBlocksDevicePool(instance, now) {
			continue
		}
		_ = repositories.WxWorkProtocolDevicePoolRepository.UpdateByGUID(db, guid, map[string]any{
			"bound_wx_work_protocol_instance_id": instance.ID,
			"sync_status":                        devicePoolBoundStatus(instance.HealthStatus),
			"updated_at":                         now,
			"update_user_id":                     operator.UserID,
			"update_user_name":                   operator.Username,
		})
	}
	return nil
}

func (s *wxWorkProtocolDevicePoolService) syncSummary() *response.WxWorkProtocolDevicePoolSyncResponse {
	items := repositories.WxWorkProtocolDevicePoolRepository.Find(sqls.DB(), sqls.NewCnd().Eq("status", enums.StatusOk))
	ret := &response.WxWorkProtocolDevicePoolSyncResponse{SyncedCount: len(items)}
	for _, item := range items {
		if item.BoundWxWorkProtocolInstanceID > 0 {
			ret.BoundCount++
		}
		if item.SyncStatus == "idle" {
			ret.IdleCount++
		}
	}
	return ret
}

func (s *wxWorkProtocolDevicePoolService) loadSettings() wxWorkDevicePoolSettings {
	settings := wxWorkDevicePoolSettings{AdminBaseURL: defaultWxWorkDevicePoolAdminBaseURL}
	for _, item := range SystemConfigService.Find(sqls.NewCnd().Eq("group_code", wxWorkDevicePoolConfigGroup).Eq("status", enums.StatusOk)) {
		switch item.ConfigKey {
		case wxWorkDevicePoolConfigAdminBaseURL:
			settings.AdminBaseURL = normalizeDevicePoolAdminBaseURL(item.ConfigValue)
		case wxWorkDevicePoolConfigCallbackBaseURL:
			settings.CallbackBaseURL = strings.TrimRight(strings.TrimSpace(item.ConfigValue), "/")
		case wxWorkDevicePoolConfigUsername:
			settings.Username = strings.TrimSpace(item.ConfigValue)
		case wxWorkDevicePoolConfigPassword:
			settings.Password = strings.TrimSpace(item.ConfigValue)
		case wxWorkDevicePoolConfigToken:
			settings.Token = strings.TrimSpace(item.ConfigValue)
		case wxWorkDevicePoolConfigTokenExpire:
			settings.TokenExpire = parseFlexibleTime(item.ConfigValue)
		}
	}
	settings.AdminBaseURL = normalizeDevicePoolAdminBaseURL(settings.AdminBaseURL)
	return settings
}

func (s *wxWorkProtocolDevicePoolService) upsertConfig(key, value, title, description string, operator *dto.AuthPrincipal) error {
	now := time.Now()
	if item := SystemConfigService.Take("config_key = ?", key); item != nil {
		return SystemConfigService.Updates(item.ID, map[string]interface{}{
			"config_value":     value,
			"group_code":       wxWorkDevicePoolConfigGroup,
			"title":            title,
			"description":      description,
			"status":           enums.StatusOk,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       now,
		})
	}
	item := &models.SystemConfig{
		ConfigKey:   key,
		ConfigValue: value,
		GroupCode:   wxWorkDevicePoolConfigGroup,
		Title:       title,
		Description: description,
		Status:      enums.StatusOk,
		AuditFields: utils.BuildAuditFields(operator),
	}
	item.CreatedAt = now
	item.UpdatedAt = now
	return SystemConfigService.Create(item)
}

func parseAdminListInstanceResponse(raw []byte) ([]wxWorkAdminListInstanceItem, error) {
	root := map[string]any{}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("聚合智能实例列表响应无法解析: %w", err)
	}
	errCode := cast.ToInt(root["err_code"])
	if errCode != 0 {
		return nil, errorsx.InvalidParam("聚合智能实例列表返回错误：" + firstNonBlank(cast.ToString(root["err_msg"]), strings.TrimSpace(string(raw))))
	}
	data, _ := root["data"].(map[string]any)
	list, _ := data["list"].([]any)
	ret := make([]wxWorkAdminListInstanceItem, 0, len(list))
	for _, value := range list {
		m, ok := value.(map[string]any)
		if !ok {
			continue
		}
		rawItem, _ := json.Marshal(m)
		ret = append(ret, wxWorkAdminListInstanceItem{
			ID:         cast.ToInt64(m["id"]),
			Guid:       strings.TrimSpace(cast.ToString(m["guid"])),
			Uin:        strings.TrimSpace(cast.ToString(m["uin"])),
			UserID:     cast.ToInt64(m["user_id"]),
			ClientType: cast.ToInt(m["client_type"]),
			ExpiredAt:  cast.ToInt64(m["expired_at"]),
			SeatName:   strings.TrimSpace(cast.ToString(m["seat_name"])),
			BridgeID:   strings.TrimSpace(cast.ToString(m["bridge_id"])),
			State:      strings.TrimSpace(cast.ToString(m["state"])),
			Raw:        rawItem,
		})
	}
	return ret, nil
}

func normalizeDevicePoolAdminBaseURL(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return defaultWxWorkDevicePoolAdminBaseURL
	}
	return value
}

func normalizeWxWorkProtocolCallbackBaseURL(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errorsx.InvalidParam("企微协议公开回调地址必须是无账号、参数和片段的 HTTPS 公网地址")
	}
	return value, nil
}

func validateAdoptableDevicePoolInstance(item *models.WxWorkProtocolDevicePoolInstance, now time.Time) error {
	if item == nil || item.Status != enums.StatusOk {
		return errorsx.InvalidParam("实例池记录不存在或已停用")
	}
	if strings.TrimSpace(item.Guid) == "" {
		return errorsx.InvalidParam("实例池记录缺少 GUID")
	}
	if devicePoolExpired(item.ExpiredAt, now) {
		return errorsx.InvalidParam("真实企微实例已过期")
	}
	if item.BoundWxWorkProtocolInstanceID > 0 {
		return nil
	}
	status := strings.ToLower(strings.TrimSpace(item.SyncStatus))
	if strings.TrimSpace(item.Uin) == "" && status != "online" {
		return errorsx.InvalidParam("只能接入已登录且在线的真实企微实例")
	}
	if status == "offline" || status == "unavailable" || status == "expired" {
		return errorsx.InvalidParam("真实企微实例当前不可用")
	}
	return nil
}

func (s *wxWorkProtocolDevicePoolService) fetchProviderCredential(operator *dto.AuthPrincipal) (*wxWorkProtocolProviderCredential, error) {
	token, settings, err := s.ensureAdminToken(operator)
	if err != nil {
		return nil, err
	}
	raw, err := s.postAdminJSON(settings.AdminBaseURL, "/admin/GetOpenApp", token, map[string]any{})
	if err != nil {
		return nil, errorsx.BusinessError(1, "读取企微协议应用凭据失败")
	}
	root := map[string]any{}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, errorsx.BusinessError(1, "企微协议应用凭据响应格式不合法")
	}
	data, _ := root["data"].(map[string]any)
	app, _ := data["app"].(map[string]any)
	credential := &wxWorkProtocolProviderCredential{
		AppKey:    strings.TrimSpace(cast.ToString(app["app_key"])),
		AppSecret: strings.TrimSpace(cast.ToString(app["app_secret"])),
	}
	if credential.AppKey == "" || credential.AppSecret == "" {
		return nil, errorsx.BusinessError(1, "企微协议应用凭据未配置完整")
	}
	return credential, nil
}

func (s *wxWorkProtocolDevicePoolService) ensureTenantProtocolChannel(
	db *gorm.DB,
	tenant *models.Tenant,
	credential *wxWorkProtocolProviderCredential,
	settings wxWorkDevicePoolSettings,
	operator *dto.AuthPrincipal,
) (*models.Channel, error) {
	if db == nil || tenant == nil || tenant.ID <= 0 || credential == nil {
		return nil, errorsx.InvalidParam("创建企微协议渠道缺少接入公司或应用凭据")
	}
	channel := repositories.ChannelRepository.Take(
		db,
		"tenant_id = ? AND channel_type = ? AND status <> ?",
		tenant.ID,
		enums.ChannelTypeWxWorkProtocol,
		enums.StatusDeleted,
	)
	cfg := &dto.WxWorkProtocolChannelConfig{
		BaseURL:       wxWorkProtocolGatewayURL,
		DevicePoolURL: settings.AdminBaseURL,
	}
	if channel != nil {
		parsed, err := ChannelService.ParseWxWorkProtocolChannelConfig(channel.ConfigJSON)
		if err != nil {
			return nil, errorsx.InvalidParam("现有企微协议渠道配置不合法")
		}
		cfg = parsed
	}
	cfg.AppKey = credential.AppKey
	cfg.AppSecret = credential.AppSecret
	cfg.DevicePoolURL = settings.AdminBaseURL
	if cfg.CallbackToken == "" {
		secret, err := generateUserTokenSecret()
		if err != nil {
			return nil, err
		}
		cfg.CallbackToken = secret
	}
	if cfg.PublicAssetBaseURL == "" {
		cfg.PublicAssetBaseURL = settings.CallbackBaseURL
	}
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if channel != nil {
		if err := repositories.ChannelRepository.UpdatesInTenant(db, channel.ID, tenant.ID, map[string]any{
			"name":             firstNonBlank(channel.Name, "企微员工号协议"),
			"config_json":      string(configJSON),
			"status":           enums.StatusOk,
			"updated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		}); err != nil {
			return nil, err
		}
		channel.ConfigJSON = string(configJSON)
		channel.Status = enums.StatusOk
		return channel, nil
	}
	channel = &models.Channel{
		TenantID:    tenant.ID,
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   strs.UUID(),
		Name:        "企微员工号协议",
		ConfigJSON:  string(configJSON),
		Status:      enums.StatusOk,
		Remark:      "由平台真实实例池自动创建",
		AuditFields: utils.BuildAuditFields(operator),
	}
	channel.CreatedAt = now
	channel.UpdatedAt = now
	if err := repositories.ChannelRepository.Create(db, channel); err != nil {
		return nil, err
	}
	return channel, nil
}

func applyProtocolProfileUpdates(instance *models.WxWorkProtocolInstance, updates map[string]any) {
	if instance == nil {
		return
	}
	if value := strings.TrimSpace(cast.ToString(updates["employee_user_id"])); value != "" {
		instance.EmployeeUserID = value
	}
	if value := strings.TrimSpace(cast.ToString(updates["employee_name"])); value != "" {
		instance.EmployeeName = utils.RepairMojibakeText(value)
	}
	if value := strings.TrimSpace(cast.ToString(updates["employee_avatar"])); value != "" {
		instance.EmployeeAvatar = value
	}
}

func buildWxWorkProtocolCallbackURL(baseURL string, channelID int64) (string, error) {
	channel := repositories.ChannelRepository.Get(sqls.DB(), channelID)
	if channel == nil || channel.Status != enums.StatusOk || channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
		return "", errorsx.InvalidParam("企微协议渠道不存在或未启用")
	}
	cfg, err := ChannelService.ParseWxWorkProtocolChannelConfig(channel.ConfigJSON)
	if err != nil || strings.TrimSpace(cfg.CallbackToken) == "" {
		return "", errorsx.InvalidParam("企微协议渠道缺少回调令牌")
	}
	return strings.TrimRight(baseURL, "/") +
		"/api/third/wxp?t=" +
		url.QueryEscape(cfg.CallbackToken), nil
}

func unixSecondsPtr(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	t := time.Unix(value, 0)
	return &t
}

func parseFlexibleTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, time.DateTime, "2006-01-02T15:04:05-07:00"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return &parsed
		}
	}
	return nil
}

func devicePoolSyncStatus(uin, state string, expiredAt *time.Time, now time.Time) string {
	if devicePoolExpired(expiredAt, now) {
		return "expired"
	}
	if strings.TrimSpace(uin) == "" {
		return "idle"
	}
	state = strings.ToLower(strings.TrimSpace(state))
	if state != "" {
		return state
	}
	return "online"
}

func devicePoolBoundStatus(healthStatus string) string {
	healthStatus = strings.TrimSpace(healthStatus)
	if healthStatus == "" || healthStatus == "unknown" || healthStatus == "login_qrcode" {
		return "bound"
	}
	return healthStatus
}

func wxWorkProtocolInstanceBlocksDevicePool(instance models.WxWorkProtocolInstance, now time.Time) bool {
	if instance.Status == enums.StatusDeleted {
		return false
	}
	healthStatus := strings.TrimSpace(instance.HealthStatus)
	if healthStatus == "login_qrcode" && now.Sub(instance.CreatedAt) > wxWorkDevicePoolTemporaryHoldTTL {
		return false
	}
	if healthStatus == "remote_setup" && instance.RemoteSetupSubmittedAt == nil && now.Sub(instance.CreatedAt) > wxWorkDevicePoolTemporaryHoldTTL {
		return false
	}
	return true
}

func devicePoolExpired(expiredAt *time.Time, now time.Time) bool {
	return expiredAt != nil && !expiredAt.After(now)
}
