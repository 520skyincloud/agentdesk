package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var WeComProviderService = newWeComProviderService()

type weComProviderService struct {
	httpClient     *http.Client
	suiteTokenMu   sync.Mutex
	corpTokenLocks sync.Map
}

func newWeComProviderService() *weComProviderService {
	return &weComProviderService{httpClient: &http.Client{Timeout: 20 * time.Second}}
}

type weChatCodeSessionResult struct {
	OpenID  string `json:"openid"`
	UnionID string `json:"unionid"`
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

type weComPermanentAuthorizationResult struct {
	PermanentCode string `json:"permanent_code"`
	AuthCorpInfo  struct {
		CorpID   string `json:"corpid"`
		CorpName string `json:"corp_name"`
	} `json:"auth_corp_info"`
	AuthInfo json.RawMessage `json:"auth_info"`
}

type weComContactWayResult struct {
	ConfigID string `json:"config_id"`
	QRCode   string `json:"qr_code"`
}

type weComCustomerAcquisitionQuota struct {
	Total   int64 `json:"total"`
	Balance int64 `json:"balance"`
}

type weComCustomerAcquisitionLink struct {
	LinkID     string `json:"link_id"`
	LinkName   string `json:"link_name"`
	URL        string `json:"url"`
	CreateTime int64  `json:"create_time"`
	UserList   []string
}

type weComCustomerAcquisitionCustomer struct {
	ExternalUserID string `json:"external_userid"`
	UserID         string `json:"userid"`
	ChatStatus     int    `json:"chat_status"`
	State          string `json:"state"`
}

type weComCustomerAcquisitionCreateRequest struct {
	LinkName   string                        `json:"link_name"`
	Range      weComCustomerAcquisitionRange `json:"range"`
	SkipVerify bool                          `json:"skip_verify"`
}

type weComCustomerAcquisitionRange struct {
	UserList []string `json:"user_list"`
}

type weComSetSessionInfoRequest struct {
	PreAuthCode string                 `json:"pre_auth_code"`
	SessionInfo weComAuthorizationInfo `json:"session_info"`
}

type weComAuthorizationInfo struct {
	AuthType int `json:"auth_type"`
}

var weComAuthorizationStatePattern = regexp.MustCompile(`^[A-Za-z0-9]{1,128}$`)

func (s *weComProviderService) ExchangeMiniProgramLoginCode(loginCode string) (*weChatCodeSessionResult, error) {
	cfg := config.Current().Arrival
	loginCode = strings.TrimSpace(loginCode)
	if loginCode == "" {
		return nil, fmt.Errorf("小程序登录凭证不能为空")
	}
	if strings.TrimSpace(cfg.MiniProgramAppID) == "" || strings.TrimSpace(cfg.MiniProgramAppSecret) == "" {
		return nil, fmt.Errorf("小程序身份服务未配置")
	}
	query := url.Values{}
	query.Set("appid", strings.TrimSpace(cfg.MiniProgramAppID))
	query.Set("secret", strings.TrimSpace(cfg.MiniProgramAppSecret))
	query.Set("js_code", loginCode)
	query.Set("grant_type", "authorization_code")
	endpoint := cfg.WeChatBaseURL() + "/sns/jscode2session?" + query.Encode()
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建小程序身份请求失败")
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("小程序身份服务暂不可用")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("小程序身份服务暂不可用")
	}
	result := &weChatCodeSessionResult{}
	if err := json.Unmarshal(body, result); err != nil {
		return nil, fmt.Errorf("小程序身份服务响应无效")
	}
	if result.ErrCode != 0 || strings.TrimSpace(result.OpenID) == "" {
		return nil, fmt.Errorf("小程序登录凭证无效或已过期")
	}
	return result, nil
}

func (s *weComProviderService) StoreSuiteTicket(suiteID, ticket string, occurredAt time.Time) error {
	cfg := config.Current().Arrival
	if strings.TrimSpace(suiteID) != strings.TrimSpace(cfg.WeComSuiteID) {
		return fmt.Errorf("企业微信 SuiteID 不匹配")
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return err
	}
	ciphertext, nonce, err := security.Encrypt("suite_ticket", ticket)
	if err != nil {
		return fmt.Errorf("保存企业微信 suite ticket 失败")
	}
	now := time.Now()
	item := repositories.ArrivalRepository.FindSuiteCredential(sqls.DB(), suiteID)
	if item == nil {
		return repositories.ArrivalRepository.CreateSuiteCredential(sqls.DB(), &models.WeComSuiteCredential{
			SuiteID:               strings.TrimSpace(suiteID),
			SuiteTicketCiphertext: ciphertext,
			SuiteTicketNonce:      nonce,
			LastTicketAt:          &occurredAt,
			Status:                enums.StatusOk,
			AuditFields:           arrivalSystemAuditFields(now),
		})
	}
	return repositories.ArrivalRepository.UpdateSuiteCredential(sqls.DB(), item.ID, map[string]any{
		"suite_ticket_ciphertext":       ciphertext,
		"suite_ticket_nonce":            nonce,
		"suite_access_token_ciphertext": "",
		"suite_access_token_nonce":      "",
		"suite_access_token_expires_at": nil,
		"last_ticket_at":                occurredAt,
		"status":                        enums.StatusOk,
		"updated_at":                    now,
		"update_user_name":              "arrival_provider",
	})
}

func (s *weComProviderService) GetSuiteAccessToken() (string, *models.WeComSuiteCredential, error) {
	s.suiteTokenMu.Lock()
	defer s.suiteTokenMu.Unlock()

	cfg := config.Current().Arrival
	security, err := newArrivalSecurity()
	if err != nil {
		return "", nil, newWeComProviderError(weComStageSuiteToken, 0, 0, "企业微信凭据加密服务不可用", false)
	}
	suite := repositories.ArrivalRepository.FindSuiteCredential(sqls.DB(), strings.TrimSpace(cfg.WeComSuiteID))
	if suite == nil || strings.TrimSpace(suite.SuiteTicketCiphertext) == "" {
		return "", nil, newWeComProviderError(weComStageSuiteToken, 0, 0, "尚未收到企业微信 suite ticket", false)
	}
	now := time.Now()
	if suite.SuiteAccessTokenExpiresAt != nil && suite.SuiteAccessTokenExpiresAt.After(now.Add(2*time.Minute)) && suite.SuiteAccessTokenCiphertext != "" {
		token, decryptErr := security.Decrypt("suite_access_token", suite.SuiteAccessTokenCiphertext, suite.SuiteAccessTokenNonce)
		if decryptErr == nil && strings.TrimSpace(token) != "" {
			return token, suite, nil
		}
	}
	ticket, err := security.Decrypt("suite_ticket", suite.SuiteTicketCiphertext, suite.SuiteTicketNonce)
	if err != nil {
		return "", nil, newWeComProviderError(weComStageSuiteToken, 0, 0, "企业微信 suite ticket 无法解密", false)
	}
	var response struct {
		weComAPIResponse
		SuiteAccessToken string `json:"suite_access_token"`
		ExpiresIn        int    `json:"expires_in"`
	}
	if err := s.postWeComJSON("/cgi-bin/service/get_suite_token", nil, map[string]any{
		"suite_id":     strings.TrimSpace(cfg.WeComSuiteID),
		"suite_secret": strings.TrimSpace(cfg.WeComSuiteSecret),
		"suite_ticket": ticket,
	}, &response); err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(response.SuiteAccessToken) == "" {
		return "", nil, newWeComProviderError(weComStageSuiteToken, 0, 0, "企业微信未返回 suite access token", false)
	}
	expiresIn := response.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 7200
	}
	expiresAt := now.Add(time.Duration(expiresIn) * time.Second)
	ciphertext, nonce, err := security.Encrypt("suite_access_token", response.SuiteAccessToken)
	if err != nil {
		return "", nil, newWeComProviderError(weComStageSuiteToken, 0, 0, "缓存企业微信 suite access token 失败", true)
	}
	if err := repositories.ArrivalRepository.UpdateSuiteCredential(sqls.DB(), suite.ID, map[string]any{
		"suite_access_token_ciphertext": ciphertext,
		"suite_access_token_nonce":      nonce,
		"suite_access_token_expires_at": expiresAt,
		"updated_at":                    now,
		"update_user_name":              "arrival_provider",
	}); err != nil {
		return "", nil, newWeComProviderError(weComStageSuiteToken, 0, 0, "保存企业微信 suite access token 失败", true)
	}
	suite.SuiteAccessTokenCiphertext = ciphertext
	suite.SuiteAccessTokenNonce = nonce
	suite.SuiteAccessTokenExpiresAt = &expiresAt
	return response.SuiteAccessToken, suite, nil
}

func (s *weComProviderService) BeginAuthorization(state string) (string, string, error) {
	if !weComAuthorizationStatePattern.MatchString(state) {
		return "", "", fmt.Errorf("企业微信授权 state 必须为 1 至 128 位 ASCII 字母或数字")
	}
	cfg := config.Current().Arrival
	if cfg.WeComAuthType != 0 && cfg.WeComAuthType != 1 {
		return "", "", fmt.Errorf("企业微信授权类型配置无效")
	}
	token, _, err := s.GetSuiteAccessToken()
	if err != nil {
		return "", "", err
	}
	var preAuth struct {
		weComAPIResponse
		PreAuthCode string `json:"pre_auth_code"`
	}
	query := url.Values{"suite_access_token": []string{token}}
	if err := s.getWeComJSON("/cgi-bin/service/get_pre_auth_code", query, &preAuth); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(preAuth.PreAuthCode) == "" {
		return "", "", fmt.Errorf("企业微信未返回预授权码")
	}
	sessionRequest := weComSetSessionInfoRequest{
		PreAuthCode: preAuth.PreAuthCode,
		SessionInfo: weComAuthorizationInfo{AuthType: cfg.WeComAuthType},
	}
	var configured weComAPIResponse
	if err := s.postWeComJSON("/cgi-bin/service/set_session_info", query, sessionRequest, &configured); err != nil {
		return "", "", err
	}
	redirectURL := strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/") + "/api/wecom/provider/authorization/callback"
	authorizationQuery := url.Values{}
	authorizationQuery.Set("suite_id", strings.TrimSpace(cfg.WeComSuiteID))
	authorizationQuery.Set("pre_auth_code", preAuth.PreAuthCode)
	authorizationQuery.Set("redirect_uri", redirectURL)
	authorizationQuery.Set("state", state)
	return "https://open.work.weixin.qq.com/3rdapp/install?" + authorizationQuery.Encode(), preAuth.PreAuthCode, nil
}

func (s *weComProviderService) ExchangePermanentAuthorization(authCode string) (*weComPermanentAuthorizationResult, error) {
	token, _, err := s.GetSuiteAccessToken()
	if err != nil {
		return nil, err
	}
	query := url.Values{"suite_access_token": []string{token}}
	var response struct {
		weComAPIResponse
		weComPermanentAuthorizationResult
	}
	if err := s.postWeComJSON("/cgi-bin/service/v2/get_permanent_code", query, map[string]any{
		"auth_code": strings.TrimSpace(authCode),
	}, &response); err != nil {
		return nil, err
	}
	result := response.weComPermanentAuthorizationResult
	if strings.TrimSpace(result.PermanentCode) == "" || strings.TrimSpace(result.AuthCorpInfo.CorpID) == "" {
		return nil, fmt.Errorf("企业微信授权结果缺少企业身份")
	}
	return &result, nil
}

func (s *weComProviderService) GetAuthorizationInfo(corpID, permanentCode string) (json.RawMessage, error) {
	token, _, err := s.GetSuiteAccessToken()
	if err != nil {
		return nil, err
	}
	query := url.Values{"suite_access_token": []string{token}}
	var response struct {
		weComAPIResponse
		AuthInfo json.RawMessage `json:"auth_info"`
	}
	if err := s.postWeComJSON("/cgi-bin/service/v2/get_auth_info", query, map[string]any{
		"auth_corpid":    strings.TrimSpace(corpID),
		"permanent_code": strings.TrimSpace(permanentCode),
	}, &response); err != nil {
		return nil, err
	}
	return response.AuthInfo, nil
}

func (s *weComProviderService) GetCorpAccessToken(authorization *models.WeComTenantAuthorization) (string, error) {
	if authorization == nil || authorization.ID <= 0 || authorization.TenantID <= 0 {
		return "", newWeComProviderError(weComStageAuthorizationValidate, 0, 0, "企业微信主体授权不存在", false)
	}
	lock := s.corpTokenLock(authorization.ID)
	lock.Lock()
	defer lock.Unlock()

	current := repositories.ArrivalRepository.GetTenantAuthorization(sqls.DB(), authorization.ID, authorization.TenantID)
	if current == nil || current.AuthorizationStatus != enums.WeComAuthorizationStatusActive {
		return "", newWeComProviderError(weComStageAuthorizationValidate, 0, 0, "企业微信主体未授权或已撤销", false)
	}

	security, err := newArrivalSecurity()
	if err != nil {
		return "", newWeComProviderError(weComStageCorpToken, 0, 0, "企业微信凭据加密服务不可用", false)
	}
	now := time.Now()
	if current.CorpAccessTokenExpiresAt != nil &&
		current.CorpAccessTokenExpiresAt.After(now.Add(2*time.Minute)) &&
		current.CorpAccessTokenCiphertext != "" {
		token, decryptErr := security.Decrypt("corp_access_token", current.CorpAccessTokenCiphertext, current.CorpAccessTokenNonce)
		if decryptErr == nil && strings.TrimSpace(token) != "" {
			copyCorpAccessTokenCache(authorization, current)
			return token, nil
		}
	}
	suiteToken, _, err := s.GetSuiteAccessToken()
	if err != nil {
		return "", err
	}
	corpID, err := security.Decrypt("corp_id", current.CorpIDCiphertext, current.CorpIDNonce)
	if err != nil {
		return "", newWeComProviderError(weComStageCorpToken, 0, 0, "企业微信主体身份无法解密", false)
	}
	permanentCode, err := security.Decrypt("permanent_code", current.PermanentCodeCiphertext, current.PermanentCodeNonce)
	if err != nil {
		return "", newWeComProviderError(weComStageCorpToken, 0, 0, "企业微信永久授权码无法解密", false)
	}
	query := url.Values{"suite_access_token": []string{suiteToken}}
	var response struct {
		weComAPIResponse
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := s.postWeComJSON("/cgi-bin/service/get_corp_token", query, map[string]any{
		"auth_corpid":    corpID,
		"permanent_code": permanentCode,
	}, &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		return "", newWeComProviderError(weComStageCorpToken, 0, 0, "企业微信未返回企业 access token", false)
	}
	expiresIn := response.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 7200
	}
	expiresAt := now.Add(time.Duration(expiresIn) * time.Second)
	ciphertext, nonce, err := security.Encrypt("corp_access_token", response.AccessToken)
	if err != nil {
		return "", newWeComProviderError(weComStageCorpToken, 0, 0, "缓存企业 access token 失败", true)
	}
	if err := repositories.ArrivalRepository.UpdateTenantAuthorization(sqls.DB(), current.ID, current.TenantID, map[string]any{
		"corp_access_token_ciphertext": ciphertext,
		"corp_access_token_nonce":      nonce,
		"corp_access_token_expires_at": expiresAt,
		"updated_at":                   now,
		"update_user_name":             "arrival_provider",
	}); err != nil {
		return "", newWeComProviderError(weComStageCorpToken, 0, 0, "保存企业 access token 失败", true)
	}
	current.CorpAccessTokenCiphertext = ciphertext
	current.CorpAccessTokenNonce = nonce
	current.CorpAccessTokenExpiresAt = &expiresAt
	copyCorpAccessTokenCache(authorization, current)
	return response.AccessToken, nil
}

func (s *weComProviderService) corpTokenLock(authorizationID int64) *sync.Mutex {
	value, _ := s.corpTokenLocks.LoadOrStore(authorizationID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func copyCorpAccessTokenCache(target, source *models.WeComTenantAuthorization) {
	if target == nil || source == nil {
		return
	}
	target.AuthorizationStatus = source.AuthorizationStatus
	target.CorpAccessTokenCiphertext = source.CorpAccessTokenCiphertext
	target.CorpAccessTokenNonce = source.CorpAccessTokenNonce
	target.CorpAccessTokenExpiresAt = source.CorpAccessTokenExpiresAt
}

func (s *weComProviderService) ListContactMembers(authorization *models.WeComTenantAuthorization) ([]string, error) {
	token, err := s.GetCorpAccessToken(authorization)
	if err != nil {
		return nil, err
	}
	var response struct {
		weComAPIResponse
		FollowUser []string `json:"follow_user"`
	}
	query := url.Values{"access_token": []string{token}}
	if err := s.getWeComJSON("/cgi-bin/externalcontact/get_follow_user_list", query, &response); err != nil {
		return nil, err
	}
	ret := make([]string, 0, len(response.FollowUser))
	for _, item := range response.FollowUser {
		if value := strings.TrimSpace(item); value != "" {
			ret = append(ret, value)
		}
	}
	return ret, nil
}

func (s *weComProviderService) AddContactWay(authorization *models.WeComTenantAuthorization, memberUserID, state string) (*weComContactWayResult, error) {
	memberUserID = strings.TrimSpace(memberUserID)
	if memberUserID == "" {
		return nil, newWeComProviderError(weComStageContactMemberValidate, 0, 0, "门店客户联系成员 UserID 缺失", false)
	}
	state = strings.TrimSpace(state)
	if state == "" || len([]byte(state)) > 30 {
		return nil, newWeComProviderError(weComStageAddContactWay, 0, 0, "联系我 state 必须为 1 至 30 字节", false)
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, err := s.GetCorpAccessToken(authorization)
		if err != nil {
			return nil, err
		}
		usedCiphertext := authorization.CorpAccessTokenCiphertext
		result, err := s.addContactWayWithToken(token, memberUserID, state)
		if err == nil {
			return result, nil
		}
		if attempt > 0 || !isWeComCorpAccessTokenError(err) {
			return nil, err
		}
		if _, clearErr := repositories.ArrivalRepository.ClearCorpAccessTokenIfMatches(
			sqls.DB(),
			authorization.ID,
			authorization.TenantID,
			usedCiphertext,
			time.Now(),
		); clearErr != nil {
			return nil, newWeComProviderError(weComStageCorpToken, 0, 0, "清除失效企业 access token 失败", true)
		}
		authorization.CorpAccessTokenCiphertext = ""
		authorization.CorpAccessTokenNonce = ""
		authorization.CorpAccessTokenExpiresAt = nil
	}
	return nil, newWeComProviderError(weComStageAddContactWay, 0, 0, "企业微信联系我创建失败", true)
}

func (s *weComProviderService) addContactWayWithToken(token, memberUserID, state string) (*weComContactWayResult, error) {
	query := url.Values{"access_token": []string{token}}
	var response struct {
		weComAPIResponse
		weComContactWayResult
	}
	if err := s.postWeComJSON("/cgi-bin/externalcontact/add_contact_way", query, map[string]any{
		"type":        1,
		"scene":       2,
		"remark":      "门店到店管家",
		"skip_verify": true,
		"state":       state,
		"user":        []string{memberUserID},
	}, &response); err != nil {
		return nil, err
	}
	result := response.weComContactWayResult
	if strings.TrimSpace(result.ConfigID) == "" || strings.TrimSpace(result.QRCode) == "" {
		return nil, newWeComProviderError(weComStageAddContactWayResponse, 0, 0, "企业微信未返回真实联系二维码", false)
	}
	return &result, nil
}

func (s *weComProviderService) DeleteContactWay(authorization *models.WeComTenantAuthorization, configID string) error {
	token, err := s.GetCorpAccessToken(authorization)
	if err != nil {
		return err
	}
	query := url.Values{"access_token": []string{token}}
	var response weComAPIResponse
	return s.postWeComJSON("/cgi-bin/externalcontact/del_contact_way", query, map[string]any{
		"config_id": strings.TrimSpace(configID),
	}, &response)
}

func (s *weComProviderService) GetCustomerAcquisitionQuota(
	authorization *models.WeComTenantAuthorization,
) (*weComCustomerAcquisitionQuota, error) {
	var result *weComCustomerAcquisitionQuota
	err := s.withCorpAccessTokenRetry(authorization, func(token string) error {
		query := url.Values{"access_token": []string{token}}
		var response struct {
			weComAPIResponse
			weComCustomerAcquisitionQuota
		}
		if err := s.getWeComJSON(
			"/cgi-bin/externalcontact/customer_acquisition_quota",
			query,
			&response,
		); err != nil {
			return err
		}
		value := response.weComCustomerAcquisitionQuota
		result = &value
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *weComProviderService) CreateCustomerAcquisitionLink(
	authorization *models.WeComTenantAuthorization,
	memberUserID string,
) (*weComCustomerAcquisitionLink, error) {
	memberUserID = strings.TrimSpace(memberUserID)
	if memberUserID == "" {
		return nil, newWeComProviderError(
			weComStageContactMemberValidate,
			0,
			0,
			"门店客户联系成员 UserID 缺失",
			false,
		)
	}
	var result *weComCustomerAcquisitionLink
	err := s.withCorpAccessTokenRetry(authorization, func(token string) error {
		query := url.Values{"access_token": []string{token}}
		var response struct {
			weComAPIResponse
			Link weComCustomerAcquisitionLink `json:"link"`
		}
		body := weComCustomerAcquisitionCreateRequest{
			LinkName: "门店到店管家",
			Range: weComCustomerAcquisitionRange{
				UserList: []string{memberUserID},
			},
			SkipVerify: true,
		}
		if err := s.postWeComJSON(
			"/cgi-bin/externalcontact/customer_acquisition/create_link",
			query,
			body,
			&response,
		); err != nil {
			return err
		}
		if strings.TrimSpace(response.Link.LinkID) == "" || strings.TrimSpace(response.Link.URL) == "" {
			return newWeComProviderError(
				weComStageAcquisitionCreate,
				0,
				0,
				"企业微信未返回真实获客链接",
				false,
			)
		}
		value := response.Link
		value.LinkID = strings.TrimSpace(value.LinkID)
		value.URL = strings.TrimSpace(value.URL)
		value.UserList = []string{memberUserID}
		result = &value
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *weComProviderService) GetCustomerAcquisitionLink(
	authorization *models.WeComTenantAuthorization,
	linkID string,
) (*weComCustomerAcquisitionLink, error) {
	linkID = strings.TrimSpace(linkID)
	if linkID == "" {
		return nil, newWeComProviderError(
			weComStageAcquisitionGet,
			0,
			0,
			"企业微信获客链接 ID 缺失",
			false,
		)
	}
	var result *weComCustomerAcquisitionLink
	err := s.withCorpAccessTokenRetry(authorization, func(token string) error {
		query := url.Values{"access_token": []string{token}}
		var response struct {
			weComAPIResponse
			Link struct {
				LinkName   string `json:"link_name"`
				URL        string `json:"url"`
				CreateTime int64  `json:"create_time"`
			} `json:"link"`
			Range weComCustomerAcquisitionRange `json:"range"`
		}
		if err := s.postWeComJSON(
			"/cgi-bin/externalcontact/customer_acquisition/get",
			query,
			map[string]string{"link_id": linkID},
			&response,
		); err != nil {
			return err
		}
		if strings.TrimSpace(response.Link.URL) == "" {
			return newWeComProviderError(
				weComStageAcquisitionGet,
				0,
				0,
				"企业微信获客链接详情无效",
				false,
			)
		}
		result = &weComCustomerAcquisitionLink{
			LinkID:     linkID,
			LinkName:   strings.TrimSpace(response.Link.LinkName),
			URL:        strings.TrimSpace(response.Link.URL),
			CreateTime: response.Link.CreateTime,
			UserList:   append([]string(nil), response.Range.UserList...),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *weComProviderService) ListCustomerAcquisitionCustomers(
	authorization *models.WeComTenantAuthorization,
	linkID string,
) ([]weComCustomerAcquisitionCustomer, error) {
	linkID = strings.TrimSpace(linkID)
	if linkID == "" {
		return nil, newWeComProviderError(
			weComStageAcquisitionCustomer,
			0,
			0,
			"企业微信获客链接 ID 缺失",
			false,
		)
	}
	var result []weComCustomerAcquisitionCustomer
	err := s.withCorpAccessTokenRetry(authorization, func(token string) error {
		result = result[:0]
		cursor := ""
		seenCursors := map[string]struct{}{}
		for page := 0; page < 100; page++ {
			query := url.Values{"access_token": []string{token}}
			body := map[string]any{
				"link_id": linkID,
				"limit":   1000,
			}
			if cursor != "" {
				body["cursor"] = cursor
			}
			var response struct {
				weComAPIResponse
				CustomerList []weComCustomerAcquisitionCustomer `json:"customer_list"`
				NextCursor   string                             `json:"next_cursor"`
			}
			if err := s.postWeComJSON(
				"/cgi-bin/externalcontact/customer_acquisition/customer",
				query,
				body,
				&response,
			); err != nil {
				return err
			}
			result = append(result, response.CustomerList...)
			nextCursor := strings.TrimSpace(response.NextCursor)
			if nextCursor == "" {
				return nil
			}
			if _, exists := seenCursors[nextCursor]; exists {
				return newWeComProviderError(
					weComStageAcquisitionCustomer,
					0,
					0,
					"企业微信获客客户分页游标重复",
					false,
				)
			}
			seenCursors[nextCursor] = struct{}{}
			cursor = nextCursor
		}
		return newWeComProviderError(
			weComStageAcquisitionCustomer,
			0,
			0,
			"企业微信获客客户分页超过安全上限",
			false,
		)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *weComProviderService) withCorpAccessTokenRetry(
	authorization *models.WeComTenantAuthorization,
	call func(token string) error,
) error {
	if call == nil {
		return newWeComProviderError("wecom_api", 0, 0, "企业微信调用未配置", false)
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, err := s.GetCorpAccessToken(authorization)
		if err != nil {
			return err
		}
		usedCiphertext := authorization.CorpAccessTokenCiphertext
		err = call(token)
		if err == nil {
			return nil
		}
		if attempt > 0 || !isWeComCorpAccessTokenError(err) {
			return err
		}
		if _, clearErr := repositories.ArrivalRepository.ClearCorpAccessTokenIfMatches(
			sqls.DB(),
			authorization.ID,
			authorization.TenantID,
			usedCiphertext,
			time.Now(),
		); clearErr != nil {
			return newWeComProviderError(
				weComStageCorpToken,
				0,
				0,
				"清除失效企业 access token 失败",
				true,
			)
		}
		authorization.CorpAccessTokenCiphertext = ""
		authorization.CorpAccessTokenNonce = ""
		authorization.CorpAccessTokenExpiresAt = nil
	}
	return newWeComProviderError("wecom_api", 0, 0, "企业微信调用失败", true)
}

type weComAPIResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

func (s *weComProviderService) getWeComJSON(path string, query url.Values, target any) error {
	stage := weComRequestStage(path)
	endpoint := config.Current().Arrival.WeComBaseURL() + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return newWeComProviderError(stage, 0, 0, "创建企业微信请求失败", false)
	}
	return s.doWeComRequest(req, target, stage)
}

func (s *weComProviderService) postWeComJSON(path string, query url.Values, body any, target any) error {
	stage := weComRequestStage(path)
	raw, err := json.Marshal(body)
	if err != nil {
		return newWeComProviderError(stage, 0, 0, "构建企业微信请求失败", false)
	}
	endpoint := config.Current().Arrival.WeComBaseURL() + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return newWeComProviderError(stage, 0, 0, "创建企业微信请求失败", false)
	}
	req.Header.Set("Content-Type", "application/json")
	return s.doWeComRequest(req, target, stage)
}

func (s *weComProviderService) doWeComRequest(req *http.Request, target any, stage string) error {
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return newWeComProviderError(stage, 0, 0, "企业微信服务暂不可用", true)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if readErr != nil {
		return newWeComProviderError(stage, resp.StatusCode, 0, "读取企业微信服务响应失败", true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiResponse := &weComAPIResponse{}
		_ = json.Unmarshal(body, apiResponse)
		message := apiResponse.ErrMsg
		if strings.TrimSpace(message) == "" {
			message = "企业微信服务返回异常状态"
		}
		return newWeComProviderError(
			stage,
			resp.StatusCode,
			apiResponse.ErrCode,
			message,
			isRetryableWeComError(apiResponse.ErrCode, resp.StatusCode),
		)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return newWeComProviderError(stage, resp.StatusCode, 0, "企业微信服务响应无效", false)
	}
	if apiResponse := extractWeComAPIResponse(target); apiResponse != nil && apiResponse.ErrCode != 0 {
		return newWeComProviderError(
			stage,
			resp.StatusCode,
			apiResponse.ErrCode,
			apiResponse.ErrMsg,
			isRetryableWeComError(apiResponse.ErrCode, resp.StatusCode),
		)
	}
	return nil
}

func extractWeComAPIResponse(target any) *weComAPIResponse {
	raw, err := json.Marshal(target)
	if err != nil {
		return nil
	}
	response := &weComAPIResponse{}
	if err := json.Unmarshal(raw, response); err != nil {
		return nil
	}
	return response
}
