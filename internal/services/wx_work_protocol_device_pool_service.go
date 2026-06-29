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
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"github.com/spf13/cast"
	"gorm.io/gorm"
)

var WxWorkProtocolDevicePoolService = newWxWorkProtocolDevicePoolService()

const (
	wxWorkDevicePoolConfigGroup         = "wxwork_protocol_device_pool"
	wxWorkDevicePoolConfigAdminBaseURL  = "wxwork_protocol_device_pool.admin_base_url"
	wxWorkDevicePoolConfigUsername      = "wxwork_protocol_device_pool.username"
	wxWorkDevicePoolConfigPassword      = "wxwork_protocol_device_pool.password"
	wxWorkDevicePoolConfigToken         = "wxwork_protocol_device_pool.token"
	wxWorkDevicePoolConfigTokenExpire   = "wxwork_protocol_device_pool.token_expire_at"
	defaultWxWorkDevicePoolAdminBaseURL = "https://chat-api.juhebot.com"
	wxWorkDevicePoolTemporaryHoldTTL    = 30 * time.Minute
)

func newWxWorkProtocolDevicePoolService() *wxWorkProtocolDevicePoolService {
	return &wxWorkProtocolDevicePoolService{httpClient: &http.Client{Timeout: 20 * time.Second}}
}

type wxWorkProtocolDevicePoolService struct {
	httpClient *http.Client
}

type wxWorkDevicePoolSettings struct {
	AdminBaseURL string
	Username     string
	Password     string
	Token        string
	TokenExpire  *time.Time
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

func (s *wxWorkProtocolDevicePoolService) FindPageByCnd(cnd *sqls.Cnd) ([]models.WxWorkProtocolDevicePoolInstance, *sqls.Paging) {
	return repositories.WxWorkProtocolDevicePoolRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *wxWorkProtocolDevicePoolService) FindPageByParams(params *params.QueryParams) ([]models.WxWorkProtocolDevicePoolInstance, *sqls.Paging) {
	return repositories.WxWorkProtocolDevicePoolRepository.FindPageByParams(sqls.DB(), params)
}

func (s *wxWorkProtocolDevicePoolService) Settings() response.WxWorkProtocolDevicePoolSettingsResponse {
	settings := s.loadSettings()
	return response.WxWorkProtocolDevicePoolSettingsResponse{
		AdminBaseURL:  settings.AdminBaseURL,
		Username:      settings.Username,
		PasswordSet:   strings.TrimSpace(settings.Password) != "",
		TokenSet:      strings.TrimSpace(settings.Token) != "",
		TokenExpireAt: settings.TokenExpire,
	}
}

func (s *wxWorkProtocolDevicePoolService) UpdateSettings(req request.UpdateWxWorkProtocolDevicePoolSettingsRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	baseURL := normalizeDevicePoolAdminBaseURL(req.AdminBaseURL)
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return errorsx.InvalidParam("聚合智能后台账号不能为空")
	}
	if err := s.upsertConfig(wxWorkDevicePoolConfigAdminBaseURL, baseURL, "聚合智能后台 API 地址", "用于同步 XBot 实例池", operator); err != nil {
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
	candidates := repositories.WxWorkProtocolDevicePoolRepository.Find(sqls.DB(), sqls.NewCnd().Eq("status", enums.StatusOk).Eq("sync_status", "idle").Asc("id"))
	if len(candidates) == 0 {
		return "", errorsx.InvalidParam("实例池暂无空闲实例，请先同步设备列表或初始化新实例")
	}
	var lastErr string
	for _, candidate := range candidates {
		guid := normalizeProtocolDeviceGUID(candidate.Guid)
		if guid == "" || bound[guid] || candidate.BoundWxWorkProtocolInstanceID > 0 || devicePoolExpired(candidate.ExpiredAt, time.Now()) {
			continue
		}
		_, err := WxWorkProtocolService.postJSON(cfg, "/login/get_login_qrcode", map[string]any{
			"guid":         guid,
			"verify_login": false,
		})
		if err == nil {
			return guid, nil
		}
		lastErr = err.Error()
		_ = repositories.WxWorkProtocolDevicePoolRepository.Updates(sqls.DB(), candidate.ID, map[string]any{
			"sync_status": "unavailable",
			"remark":      lastErr,
			"updated_at":  time.Now(),
		})
	}
	if lastErr != "" {
		return "", errorsx.InvalidParam("实例池未找到可扫码实例，最后一次探测错误：" + lastErr)
	}
	return "", errorsx.InvalidParam("实例池里的空闲实例均已被本地绑定或已过期")
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
	return expiredAt != nil && expiredAt.Before(now)
}
