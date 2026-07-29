package services

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	arrivalScanInputVersion  = "arrival_scan_input.v1"
	arrivalScanResultVersion = "arrival_scan_result.v2"

	arrivalContactWayMaxProvisionAttempts = 3
	arrivalContactWayProvisionStaleAfter  = 10 * time.Minute
)

var ArrivalLinkService = &arrivalLinkService{}

type arrivalMiniProgramLoginExchanger interface {
	ExchangeMiniProgramLoginCode(loginCode string) (*weChatCodeSessionResult, error)
}

type arrivalContactWayCreator interface {
	AddContactWay(authorization *models.WeComTenantAuthorization, memberUserID, state string) (*weComContactWayResult, error)
}

type arrivalQRCodeArtifactBuilder interface {
	BuildArtifact(qrCodeURL string) (*arrivalQRCodeArtifact, error)
}

type arrivalCardSender interface {
	SendArrivalCard(conversationID, instanceID int64, clientMsgID string) (enums.ArrivalDeliveryStatus, error)
}

type arrivalLinkService struct {
	scanLocks         sync.Map
	loginExchanger    arrivalMiniProgramLoginExchanger
	contactWayCreator arrivalContactWayCreator
	qrCodeBuilder     arrivalQRCodeArtifactBuilder
	cardSender        arrivalCardSender
}

func (s *arrivalLinkService) ValidateConfiguration() error {
	cfg := config.Current().Arrival
	if !cfg.Enabled {
		return errorsx.BusinessError(60, "到店联动功能尚未启用")
	}
	if strings.TrimSpace(cfg.PublicBaseURL) == "" ||
		strings.TrimSpace(cfg.MiniProgramAppID) == "" ||
		strings.TrimSpace(cfg.MiniProgramAppSecret) == "" ||
		strings.TrimSpace(cfg.WeComSuiteID) == "" ||
		strings.TrimSpace(cfg.WeComSuiteSecret) == "" ||
		strings.TrimSpace(cfg.WeComProviderCallbackToken) == "" ||
		strings.TrimSpace(cfg.WeComProviderEncodingAESKey) == "" {
		return errorsx.BusinessError(61, "到店联动服务配置不完整")
	}
	if _, err := newArrivalSecurity(); err != nil {
		return errorsx.BusinessError(61, err.Error())
	}
	return nil
}

func (s *arrivalLinkService) Bootstrap(req request.ArrivalBootstrapRequest) (*response.ArrivalScanResultResponse, error) {
	return s.BootstrapWithRequestID(req, "")
}

func (s *arrivalLinkService) BootstrapWithRequestID(req request.ArrivalBootstrapRequest, requestID string) (*response.ArrivalScanResultResponse, error) {
	requestID = tracex.EnsureRequestID(requestID)
	if err := s.ValidateConfiguration(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.SchemaVersion) != arrivalScanInputVersion {
		return nil, errorsx.InvalidParam("不支持的到店扫码契约版本")
	}
	if len(strings.TrimSpace(req.LoginCode)) < 4 || len(req.LoginCode) > 256 {
		return nil, errorsx.InvalidParam("小程序登录凭证无效")
	}
	scene := strings.TrimSpace(req.Scene)
	if scene == "" || len(scene) > 64 {
		return nil, errorsx.InvalidParam("门店 scene 无效")
	}
	scanEventID := strings.TrimSpace(req.ScanEventID)
	if len(scanEventID) < 8 || len(scanEventID) > 128 {
		return nil, errorsx.InvalidParam("扫码事件标识无效")
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, errorsx.BusinessError(61, err.Error())
	}
	scanHash := security.Fingerprint("scan_event", scanEventID)
	requestFingerprint := security.Fingerprint("scan_request", strings.Join([]string{
		arrivalScanInputVersion,
		scene,
		strings.TrimSpace(req.LoginCode),
	}, "\x00"))
	lockValue, _ := s.scanLocks.LoadOrStore(scanHash, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer func() {
		lock.Unlock()
		s.scanLocks.Delete(scanHash)
	}()

	if existing := repositories.ArrivalRepository.FindScanEventByHash(sqls.DB(), scanHash); existing != nil {
		if existing.RequestFingerprint == "" || existing.RequestFingerprint != requestFingerprint {
			return nil, errorsx.InvalidParam("扫码事件标识已被其他请求使用")
		}
		s.retryExistingContactWay(existing, requestID)
		return s.buildResult(existing)
	}
	connection := repositories.ArrivalRepository.FindConnectionByScene(sqls.DB(), scene)
	if connection == nil || connection.Status != enums.StatusOk ||
		connection.ConnectionStatus != enums.ArrivalConnectionStatusActive {
		return nil, errorsx.InvalidParam("门店 scene 不存在或已停用")
	}
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), connection.StoreID, connection.TenantID)
	if store == nil || store.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("门店不存在或已停用")
	}
	login, err := s.miniProgramLoginExchanger().ExchangeMiniProgramLoginCode(req.LoginCode)
	if err != nil {
		return nil, errorsx.BusinessError(62, err.Error())
	}
	identity, identityStatus, err := s.ensureIdentity(connection.TenantID, login.OpenID, login.UnionID)
	if err != nil {
		return nil, err
	}
	bindingStatus := enums.ArrivalBindingStatusUnbound
	if binding := repositories.ArrivalRepository.FindBinding(sqls.DB(), connection.TenantID, identity.ID, connection.StoreID); binding != nil {
		bindingStatus = s.bindingStatusForContext(connection.TenantID, connection.StoreID, binding)
	}
	now := time.Now()
	event := &models.ArrivalScanEvent{
		TenantID:              connection.TenantID,
		StoreID:               connection.StoreID,
		MiniProgramIdentityID: identity.ID,
		ScanEventHash:         scanHash,
		RequestFingerprint:    requestFingerprint,
		SchemaVersion:         arrivalScanInputVersion,
		IdentityStatus:        identityStatus,
		BindingStatus:         bindingStatus,
		DeliveryStatus:        enums.ArrivalDeliveryStatusNotBound,
		Status:                enums.StatusOk,
		AuditFields:           arrivalSystemAuditFields(now),
	}
	var session *models.ArrivalSession
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.ArrivalRepository.CreateScanEvent(ctx.Tx, event); err != nil {
			return err
		}
		expiresAt := now.Add(time.Duration(config.Current().Arrival.SessionTTL()) * time.Minute)
		placeholder, randomErr := randomArrivalToken(24)
		if randomErr != nil {
			return randomErr
		}
		session = &models.ArrivalSession{
			TenantID:    event.TenantID,
			StoreID:     event.StoreID,
			ScanEventID: event.ID,
			TokenHash:   security.Fingerprint("session_placeholder", placeholder),
			ExpiresAt:   expiresAt,
			AuditFields: arrivalSystemAuditFields(now),
		}
		if err := repositories.ArrivalRepository.CreateSession(ctx.Tx, session); err != nil {
			return err
		}
		token := security.SessionToken(session.ID, session.ExpiresAt)
		return repositories.ArrivalRepository.UpdateSession(ctx.Tx, session.ID, map[string]any{
			"token_hash":       security.Fingerprint("session_token", token),
			"updated_at":       now,
			"update_user_name": "arrival",
		})
	})
	if err != nil {
		if existing := repositories.ArrivalRepository.FindScanEventByHash(sqls.DB(), scanHash); existing != nil {
			return s.buildResult(existing)
		}
		return nil, errorsx.BusinessError(63, "创建到店扫码事件失败")
	}

	switch bindingStatus {
	case enums.ArrivalBindingStatusBound:
		s.deliverBoundEvent(event)
	default:
		if connection.ConnectionStatus == enums.ArrivalConnectionStatusActive {
			_, _ = s.provisionContactWay(event, connection, requestID)
		}
	}
	return s.buildResult(event)
}

func (s *arrivalLinkService) Status(sessionToken string) (*response.ArrivalScanResultResponse, error) {
	if err := s.ValidateConfiguration(); err != nil {
		return nil, err
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, errorsx.BusinessError(61, err.Error())
	}
	sessionID, expiresAt, err := security.ParseSessionToken(sessionToken, time.Now())
	if err != nil {
		return nil, errorsx.InvalidToken(err.Error())
	}
	session := repositories.ArrivalRepository.GetSession(sqls.DB(), sessionID)
	if session == nil || session.RevokedAt != nil || session.ExpiresAt.Unix() != expiresAt.Unix() {
		return nil, errorsx.InvalidToken("到店会话令牌无效")
	}
	expectedToken := security.SessionToken(session.ID, session.ExpiresAt)
	if security.Fingerprint("session_token", expectedToken) != session.TokenHash {
		return nil, errorsx.InvalidToken("到店会话令牌无效")
	}
	event := repositories.ArrivalRepository.GetScanEvent(sqls.DB(), session.ScanEventID, session.TenantID)
	if event == nil || event.StoreID != session.StoreID {
		return nil, errorsx.InvalidToken("到店会话上下文不存在")
	}
	return s.buildResult(event)
}

func (s *arrivalLinkService) PublicQRCode(resourceToken string) ([]byte, error) {
	if err := s.ValidateConfiguration(); err != nil {
		return nil, err
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, err
	}
	resourceToken = strings.TrimSuffix(strings.TrimSpace(resourceToken), ".png")
	contactWayID, err := security.ParsePublicResourceToken(resourceToken)
	if err != nil {
		return nil, errorsx.InvalidParam("二维码资源不存在")
	}
	tokenHash := security.Fingerprint("public_qr_token", resourceToken)
	contactWay := repositories.ArrivalRepository.FindContactWayByPublicTokenHash(sqls.DB(), tokenHash)
	if contactWay == nil || contactWay.ID != contactWayID || contactWay.Status != enums.StatusOk ||
		contactWay.ContactWayStatus != enums.ArrivalContactWayStatusActive ||
		(contactWay.ExpiresAt != nil && !contactWay.ExpiresAt.After(time.Now())) {
		return nil, errorsx.InvalidParam("二维码资源不存在或已失效")
	}
	encoded := strings.TrimSpace(contactWay.ArtworkPNGBase64)
	if encoded == "" {
		encoded = strings.TrimSpace(contactWay.OriginalPNGBase64)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 {
		return nil, errorsx.InvalidParam("二维码资源不存在")
	}
	return raw, nil
}

func (s *arrivalLinkService) ensureIdentity(tenantID int64, openID, unionID string) (*models.MiniProgramIdentity, enums.ArrivalIdentityStatus, error) {
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, "", err
	}
	cfg := config.Current().Arrival
	openFingerprint := security.Fingerprint("miniprogram_openid", openID)
	now := time.Now()
	existing := repositories.ArrivalRepository.FindMiniProgramIdentityByOpenFingerprint(sqls.DB(), tenantID, cfg.MiniProgramAppID, openFingerprint)
	if existing != nil {
		updates := map[string]any{
			"last_login_at":    now,
			"updated_at":       now,
			"update_user_name": "arrival",
		}
		if unionID != "" && security.Fingerprint("miniprogram_unionid", unionID) != existing.UnionIDFingerprint {
			ciphertext, nonce, encryptErr := security.Encrypt("miniprogram_unionid", unionID)
			if encryptErr != nil {
				return nil, "", errorsx.BusinessError(64, "更新小程序身份失败")
			}
			updates["union_id_ciphertext"] = ciphertext
			updates["union_id_nonce"] = nonce
			updates["union_id_fingerprint"] = security.Fingerprint("miniprogram_unionid", unionID)
			existing.UnionIDCiphertext = ciphertext
			existing.UnionIDNonce = nonce
			existing.UnionIDFingerprint = updates["union_id_fingerprint"].(string)
		}
		if err := repositories.ArrivalRepository.UpdateMiniProgramIdentity(sqls.DB(), existing.ID, tenantID, updates); err != nil {
			return nil, "", errorsx.BusinessError(64, "更新小程序身份失败")
		}
		return existing, enums.ArrivalIdentityStatusMatched, nil
	}
	openCiphertext, openNonce, err := security.Encrypt("miniprogram_openid", openID)
	if err != nil {
		return nil, "", errorsx.BusinessError(64, "保存小程序身份失败")
	}
	unionCiphertext, unionNonce, err := security.Encrypt("miniprogram_unionid", unionID)
	if err != nil {
		return nil, "", errorsx.BusinessError(64, "保存小程序身份失败")
	}
	item := &models.MiniProgramIdentity{
		TenantID:           tenantID,
		AppID:              strings.TrimSpace(cfg.MiniProgramAppID),
		OpenIDCiphertext:   openCiphertext,
		OpenIDNonce:        openNonce,
		OpenIDFingerprint:  openFingerprint,
		UnionIDCiphertext:  unionCiphertext,
		UnionIDNonce:       unionNonce,
		UnionIDFingerprint: security.Fingerprint("miniprogram_unionid", unionID),
		LastLoginAt:        &now,
		Status:             enums.StatusOk,
		AuditFields:        arrivalSystemAuditFields(now),
	}
	if err := repositories.ArrivalRepository.CreateMiniProgramIdentity(sqls.DB(), item); err != nil {
		if existing := repositories.ArrivalRepository.FindMiniProgramIdentityByOpenFingerprint(sqls.DB(), tenantID, cfg.MiniProgramAppID, openFingerprint); existing != nil {
			return existing, enums.ArrivalIdentityStatusMatched, nil
		}
		return nil, "", errorsx.BusinessError(64, "保存小程序身份失败")
	}
	return item, enums.ArrivalIdentityStatusCreated, nil
}

func (s *arrivalLinkService) provisionContactWay(
	event *models.ArrivalScanEvent,
	connection *models.StoreArrivalConnection,
	requestID string,
) (*models.ArrivalContactWay, error) {
	if event == nil || connection == nil {
		return nil, fmt.Errorf("到店扫码上下文不存在")
	}
	if existing := repositories.ArrivalRepository.FindContactWayByScanEvent(sqls.DB(), event.ID); existing != nil {
		return s.retryContactWayProvision(event, connection, existing, requestID)
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, err
	}
	placeholderState, err := randomArrivalToken(16)
	if err != nil {
		return nil, err
	}
	placeholderResource, err := randomArrivalToken(16)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	requestID = tracex.EnsureRequestID(requestID)
	expiresAt := now.Add(time.Duration(config.Current().Arrival.ContactWayTTL()) * time.Minute)
	contactWay := &models.ArrivalContactWay{
		TenantID:                event.TenantID,
		StoreID:                 event.StoreID,
		ScanEventID:             event.ID,
		TenantAuthorizationID:   connection.TenantAuthorizationID,
		ContactStateHash:        security.Fingerprint("contact_state_placeholder", placeholderState),
		PublicResourceTokenHash: security.Fingerprint("public_qr_placeholder", placeholderResource),
		Mode:                    enums.ArrivalContactWayModeNone,
		ContactWayStatus:        enums.ArrivalContactWayStatusProvisioning,
		ProvisionAttemptCount:   1,
		LastProvisionRequestID:  requestID,
		LastProvisionAttemptAt:  &now,
		ExpiresAt:               &expiresAt,
		Status:                  enums.StatusOk,
		AuditFields:             arrivalSystemAuditFields(now),
	}
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.ArrivalRepository.CreateContactWay(ctx.Tx, contactWay); err != nil {
			return err
		}
		state := security.ContactState(contactWay.ID)
		resourceToken := security.PublicResourceToken(contactWay.ID)
		if err := repositories.ArrivalRepository.UpdateContactWay(ctx.Tx, contactWay.ID, contactWay.TenantID, map[string]any{
			"contact_state_hash":         security.Fingerprint("contact_state", state),
			"public_resource_token_hash": security.Fingerprint("public_qr_token", resourceToken),
			"updated_at":                 now,
			"update_user_name":           "arrival",
		}); err != nil {
			return err
		}
		return repositories.ArrivalRepository.UpdateScanEvent(ctx.Tx, event.ID, event.TenantID, map[string]any{
			"contact_way_id":   contactWay.ID,
			"updated_at":       now,
			"update_user_name": "arrival",
		})
	})
	if err != nil {
		if existing := repositories.ArrivalRepository.FindContactWayByScanEvent(sqls.DB(), event.ID); existing != nil {
			return s.retryContactWayProvision(event, connection, existing, requestID)
		}
		return nil, err
	}
	contactWay = repositories.ArrivalRepository.GetContactWay(sqls.DB(), contactWay.ID, contactWay.TenantID)
	if contactWay == nil {
		return nil, fmt.Errorf("到店联系码记录不存在")
	}
	return s.executeContactWayProvision(event, connection, contactWay, requestID)
}

func (s *arrivalLinkService) retryExistingContactWay(event *models.ArrivalScanEvent, requestID string) {
	if event == nil || s.currentBindingStatus(event) != enums.ArrivalBindingStatusUnbound {
		return
	}
	contactWay := repositories.ArrivalRepository.FindContactWayByScanEvent(sqls.DB(), event.ID)
	if contactWay == nil || contactWay.ContactWayStatus == enums.ArrivalContactWayStatusActive {
		return
	}
	connection := repositories.ArrivalRepository.FindConnectionByStore(sqls.DB(), event.TenantID, event.StoreID)
	if connection == nil || connection.Status != enums.StatusOk ||
		connection.ConnectionStatus != enums.ArrivalConnectionStatusActive {
		return
	}
	_, _ = s.retryContactWayProvision(event, connection, contactWay, requestID)
}

func (s *arrivalLinkService) retryContactWayProvision(
	event *models.ArrivalScanEvent,
	connection *models.StoreArrivalConnection,
	contactWay *models.ArrivalContactWay,
	requestID string,
) (*models.ArrivalContactWay, error) {
	if event == nil || connection == nil || contactWay == nil {
		return contactWay, nil
	}
	if contactWay.ContactWayStatus == enums.ArrivalContactWayStatusActive {
		return contactWay, nil
	}
	now := time.Now()
	requestID = tracex.EnsureRequestID(requestID)
	claimed, err := repositories.ArrivalRepository.TryClaimContactWayProvision(
		sqls.DB(),
		contactWay.ID,
		contactWay.TenantID,
		now,
		now.Add(-arrivalContactWayProvisionStaleAfter),
		requestID,
		arrivalContactWayMaxProvisionAttempts,
	)
	if err != nil {
		return contactWay, err
	}
	if !claimed {
		return repositories.ArrivalRepository.GetContactWay(sqls.DB(), contactWay.ID, contactWay.TenantID), nil
	}
	claimedContactWay := repositories.ArrivalRepository.GetContactWay(sqls.DB(), contactWay.ID, contactWay.TenantID)
	if claimedContactWay == nil {
		return nil, fmt.Errorf("到店联系码记录不存在")
	}
	return s.executeContactWayProvision(event, connection, claimedContactWay, requestID)
}

func (s *arrivalLinkService) executeContactWayProvision(
	event *models.ArrivalScanEvent,
	connection *models.StoreArrivalConnection,
	contactWay *models.ArrivalContactWay,
	requestID string,
) (*models.ArrivalContactWay, error) {
	authorization, memberUserID, err := s.resolveContactWayProvisioningContext(connection)
	if err != nil {
		s.failContactWay(contactWay, contactWayFailureCode(err, "contact_way_context_invalid"), requestID, err)
		return nil, err
	}
	security, err := newArrivalSecurity()
	if err != nil {
		providerErr := newWeComProviderError(weComStageContactWayPersist, 0, 0, "到店联系码加密服务不可用", false)
		s.failContactWay(contactWay, "contact_way_encrypt_failed", requestID, providerErr)
		return nil, providerErr
	}

	qrCodeURL := ""
	if strings.TrimSpace(contactWay.ConfigID) != "" && strings.TrimSpace(contactWay.OriginalQRCodeCiphertext) != "" {
		qrCodeURL, err = security.Decrypt(
			"contact_qr_url",
			contactWay.OriginalQRCodeCiphertext,
			contactWay.OriginalQRCodeNonce,
		)
		if err != nil || strings.TrimSpace(qrCodeURL) == "" {
			providerErr := newWeComProviderError(weComStageQRCodeArtifact, 0, 0, "已保存的企业微信二维码引用无法解密", false)
			s.failContactWay(contactWay, "contact_way_qr_reference_invalid", requestID, providerErr)
			return nil, providerErr
		}
	} else {
		state := security.ContactState(contactWay.ID)
		official, providerErr := s.contactWayProvider().AddContactWay(authorization, memberUserID, state)
		if providerErr != nil {
			s.failContactWay(contactWay, contactWayFailureCode(providerErr, "contact_way_api_failed"), requestID, providerErr)
			return nil, providerErr
		}
		qrCodeURL = strings.TrimSpace(official.QRCode)
		qrCiphertext, qrNonce, encryptErr := security.Encrypt("contact_qr_url", qrCodeURL)
		if encryptErr != nil {
			_ = repositories.ArrivalRepository.UpdateContactWay(sqls.DB(), contactWay.ID, contactWay.TenantID, map[string]any{
				"config_id":        strings.TrimSpace(official.ConfigID),
				"updated_at":       time.Now(),
				"update_user_name": "arrival",
			})
			providerErr = newWeComProviderError(weComStageContactWayPersist, 0, 0, "保存企业微信二维码引用失败", false)
			s.failContactWay(contactWay, "contact_way_encrypt_failed", requestID, providerErr)
			return nil, providerErr
		}
		if err := repositories.ArrivalRepository.UpdateContactWay(sqls.DB(), contactWay.ID, contactWay.TenantID, map[string]any{
			"config_id":                   strings.TrimSpace(official.ConfigID),
			"original_qr_code_ciphertext": qrCiphertext,
			"original_qr_code_nonce":      qrNonce,
			"updated_at":                  time.Now(),
			"update_user_name":            "arrival",
		}); err != nil {
			providerErr = newWeComProviderError(weComStageContactWayPersist, 0, 0, "保存企业微信联系码结果失败", false)
			s.failContactWay(contactWay, "contact_way_persist_failed", requestID, providerErr)
			return nil, providerErr
		}
		contactWay.ConfigID = strings.TrimSpace(official.ConfigID)
		contactWay.OriginalQRCodeCiphertext = qrCiphertext
		contactWay.OriginalQRCodeNonce = qrNonce
	}

	artifact, artifactErr := s.qrCodeArtifactBuilder().BuildArtifact(qrCodeURL)
	if artifactErr != nil {
		retryable := strings.Contains(artifactErr.Error(), "下载失败") ||
			strings.Contains(artifactErr.Error(), "重定向次数过多")
		providerErr := newWeComProviderError(
			weComStageQRCodeArtifact,
			0,
			0,
			artifactErr.Error(),
			retryable,
		)
		s.failContactWay(contactWay, "official_qr_cache_failed", requestID, providerErr)
		return nil, providerErr
	}
	now := time.Now()
	if err := repositories.ArrivalRepository.UpdateContactWay(sqls.DB(), contactWay.ID, contactWay.TenantID, map[string]any{
		"original_png_base64":       artifact.OriginalPNGBase64,
		"artwork_png_base64":        artifact.PublishedPNGBase64,
		"source_payload_hash":       artifact.PayloadHash,
		"published_payload_hash":    artifact.PayloadHash,
		"mode":                      enums.ArrivalContactWayModeQRCode,
		"contact_way_status":        enums.ArrivalContactWayStatusActive,
		"failure_code":              "",
		"failure_stage":             "",
		"provider_http_status":      0,
		"provider_error_code":       0,
		"provider_error_message":    "",
		"failure_retryable":         false,
		"next_provision_retry_at":   nil,
		"last_provision_request_id": requestID,
		"updated_at":                now,
		"update_user_name":          "arrival",
	}); err != nil {
		providerErr := newWeComProviderError(weComStageContactWayPersist, 0, 0, "激活企业微信联系码失败", false)
		s.failContactWay(contactWay, "contact_way_persist_failed", requestID, providerErr)
		return nil, providerErr
	}
	_ = repositories.ArrivalRepository.UpdateConnection(sqls.DB(), connection.ID, connection.TenantID, map[string]any{
		"last_contact_provisioned_at": now,
		"updated_at":                  now,
		"update_user_name":            "arrival",
	})
	return repositories.ArrivalRepository.FindContactWayByScanEvent(sqls.DB(), event.ID), nil
}

func (s *arrivalLinkService) resolveContactWayProvisioningContext(
	connection *models.StoreArrivalConnection,
) (*models.WeComTenantAuthorization, string, error) {
	if connection == nil || connection.TenantAuthorizationID <= 0 {
		return nil, "", newWeComProviderError(
			weComStageAuthorizationValidate,
			0,
			0,
			"门店未绑定企业微信授权主体",
			false,
		)
	}
	if connection.WxWorkProtocolInstanceID <= 0 ||
		strings.TrimSpace(connection.ContactMemberCiphertext) == "" ||
		strings.TrimSpace(connection.ContactMemberNonce) == "" {
		return nil, "", newWeComProviderError(
			weComStageContactMemberValidate,
			0,
			0,
			"门店客户联系成员配置缺失",
			false,
		)
	}
	authorization := repositories.ArrivalRepository.GetTenantAuthorization(
		sqls.DB(),
		connection.TenantAuthorizationID,
		connection.TenantID,
	)
	if authorization == nil || authorization.AuthorizationStatus != enums.WeComAuthorizationStatusActive {
		return nil, "", newWeComProviderError(
			weComStageAuthorizationValidate,
			0,
			0,
			"门店企业微信授权主体不可用或已撤销",
			false,
		)
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, "", newWeComProviderError(
			weComStageContactMemberValidate,
			0,
			0,
			"门店客户联系成员加密服务不可用",
			false,
		)
	}
	memberUserID, err := security.Decrypt(
		"contact_member",
		connection.ContactMemberCiphertext,
		connection.ContactMemberNonce,
	)
	if err != nil || strings.TrimSpace(memberUserID) == "" {
		return nil, "", newWeComProviderError(
			weComStageContactMemberValidate,
			0,
			0,
			"门店客户联系成员配置无效",
			false,
		)
	}
	return authorization, strings.TrimSpace(memberUserID), nil
}

func (s *arrivalLinkService) failContactWay(
	contactWay *models.ArrivalContactWay,
	failureCode, requestID string,
	provisionErr error,
) {
	if contactWay == nil {
		return
	}
	requestID = tracex.EnsureRequestID(requestID)
	providerErr := asWeComProviderError(provisionErr)
	if providerErr == nil {
		message := "到店联系码创建失败"
		if provisionErr != nil {
			message = provisionErr.Error()
		}
		providerErr = &weComProviderError{
			Stage:     "contact_way_provision",
			ErrMsg:    sanitizeWeComProviderMessage(message),
			Retryable: false,
		}
	}
	now := time.Now()
	current := repositories.ArrivalRepository.GetContactWay(sqls.DB(), contactWay.ID, contactWay.TenantID)
	attemptCount := contactWay.ProvisionAttemptCount
	if current != nil {
		attemptCount = current.ProvisionAttemptCount
	}
	retryable := providerErr.Retryable && attemptCount < arrivalContactWayMaxProvisionAttempts
	var nextRetryAt any
	if retryable {
		nextRetryAt = now.Add(arrivalContactWayRetryDelay(attemptCount))
	}
	persistErr := repositories.ArrivalRepository.UpdateContactWay(sqls.DB(), contactWay.ID, contactWay.TenantID, map[string]any{
		"mode":                      enums.ArrivalContactWayModeNone,
		"contact_way_status":        enums.ArrivalContactWayStatusFailed,
		"failure_code":              strings.TrimSpace(failureCode),
		"failure_stage":             normalizeWeComProviderStage(providerErr.Stage),
		"provider_http_status":      providerErr.HTTPStatus,
		"provider_error_code":       providerErr.ErrCode,
		"provider_error_message":    sanitizeWeComProviderMessage(providerErr.ErrMsg),
		"failure_retryable":         retryable,
		"last_provision_request_id": requestID,
		"next_provision_retry_at":   nextRetryAt,
		"updated_at":                now,
		"update_user_name":          "arrival",
	})
	slog.Error(
		"arrival contact way provision failed",
		"request_id", requestID,
		"store_id", contactWay.StoreID,
		"authorization_id", contactWay.TenantAuthorizationID,
		"contact_way_id", contactWay.ID,
		"stage", normalizeWeComProviderStage(providerErr.Stage),
		"provider_http_status", providerErr.HTTPStatus,
		"provider_error_code", providerErr.ErrCode,
		"provider_error_message", sanitizeWeComProviderMessage(providerErr.ErrMsg),
		"retryable", retryable,
	)
	if persistErr != nil {
		slog.Error(
			"persist arrival contact way failure diagnostics failed",
			"request_id", requestID,
			"store_id", contactWay.StoreID,
			"authorization_id", contactWay.TenantAuthorizationID,
			"contact_way_id", contactWay.ID,
			"stage", weComStageContactWayPersist,
		)
	}
}

func contactWayFailureCode(err error, fallback string) string {
	providerErr := asWeComProviderError(err)
	if providerErr == nil {
		return fallback
	}
	if providerErr.Stage == weComStageAddContactWay && providerErr.ErrCode == 48002 {
		return "contact_way_permission_denied"
	}
	switch providerErr.Stage {
	case weComStageAuthorizationValidate:
		return "authorization_unavailable"
	case weComStageContactMemberValidate:
		return "contact_member_invalid"
	case weComStageSuiteToken:
		return "suite_token_failed"
	case weComStageCorpToken:
		return "corp_token_failed"
	default:
		return fallback
	}
}

func arrivalContactWayRetryDelay(attemptCount int) time.Duration {
	switch {
	case attemptCount <= 1:
		return time.Minute
	case attemptCount == 2:
		return 5 * time.Minute
	default:
		return 15 * time.Minute
	}
}

func (s *arrivalLinkService) deliverBoundEvent(event *models.ArrivalScanEvent) {
	if event == nil || event.DeliveryAttemptedAt != nil {
		return
	}
	binding := repositories.ArrivalRepository.FindBinding(sqls.DB(), event.TenantID, event.MiniProgramIdentityID, event.StoreID)
	if binding == nil ||
		s.bindingStatusForContext(event.TenantID, event.StoreID, binding) != enums.ArrivalBindingStatusBound {
		return
	}
	now := time.Now()
	claimed, err := repositories.ArrivalRepository.TryClaimScanEventDelivery(sqls.DB(), event.ID, event.TenantID, now)
	if err != nil || !claimed {
		return
	}
	if recent := repositories.ArrivalRepository.FindRecentSentScanEvent(
		sqls.DB(), event.TenantID, event.StoreID, event.MiniProgramIdentityID,
		now.Add(-time.Duration(config.Current().Arrival.DeliveryRateLimit())*time.Second),
	); recent != nil {
		_ = repositories.ArrivalRepository.UpdateScanEvent(sqls.DB(), event.ID, event.TenantID, map[string]any{
			"delivery_status":       enums.ArrivalDeliveryStatusRateLimited,
			"delivery_attempted_at": now,
			"delivery_completed_at": now,
			"updated_at":            now,
			"update_user_name":      "arrival",
		})
		return
	}
	instance := WxWorkProtocolInstanceService.GetByTenantID(binding.WxWorkProtocolInstanceID, event.TenantID)
	if instance == nil || instance.Status != enums.StatusOk || !strings.EqualFold(strings.TrimSpace(instance.HealthStatus), "online") {
		_ = repositories.ArrivalRepository.UpdateScanEvent(sqls.DB(), event.ID, event.TenantID, map[string]any{
			"delivery_status":       enums.ArrivalDeliveryStatusInstanceOffline,
			"delivery_attempted_at": now,
			"delivery_completed_at": now,
			"delivery_error_code":   "instance_offline",
			"updated_at":            now,
			"update_user_name":      "arrival",
		})
		return
	}
	status, sendErr := s.arrivalCardSender().SendArrivalCard(
		binding.ConversationID,
		binding.WxWorkProtocolInstanceID,
		"arrival_scan_"+strconv.FormatInt(event.ID, 10),
	)
	completedAt := time.Now()
	updates := map[string]any{
		"delivery_status":       status,
		"delivery_completed_at": completedAt,
		"updated_at":            completedAt,
		"update_user_name":      "arrival",
	}
	if sendErr != nil {
		updates["delivery_status"] = enums.ArrivalDeliveryStatusFailed
		updates["delivery_error_code"] = "protocol_send_failed"
	}
	_ = repositories.ArrivalRepository.UpdateScanEvent(sqls.DB(), event.ID, event.TenantID, updates)
}

func (s *arrivalLinkService) buildResult(event *models.ArrivalScanEvent) (*response.ArrivalScanResultResponse, error) {
	if event == nil {
		return nil, errorsx.InvalidParam("到店扫码事件不存在")
	}
	event = repositories.ArrivalRepository.GetScanEvent(sqls.DB(), event.ID, event.TenantID)
	if event == nil {
		return nil, errorsx.InvalidParam("到店扫码事件不存在")
	}
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), event.StoreID, event.TenantID)
	if store == nil {
		return nil, errorsx.InvalidParam("门店不存在")
	}
	session := repositories.ArrivalRepository.FindSessionByScanEvent(sqls.DB(), event.ID)
	if session == nil {
		return nil, errorsx.BusinessError(65, "到店会话不存在")
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, errorsx.BusinessError(61, err.Error())
	}
	bindingStatus := s.currentBindingStatus(event)
	result := &response.ArrivalScanResultResponse{
		SchemaVersion:  arrivalScanResultVersion,
		SessionToken:   security.SessionToken(session.ID, session.ExpiresAt),
		IdentityStatus: string(event.IdentityStatus),
		BindingStatus:  string(bindingStatus),
		DeliveryStatus: string(event.DeliveryStatus),
		ContactWay: response.ArrivalContactWayResponse{
			Available: false,
			Mode:      string(enums.ArrivalContactWayModeNone),
		},
		Store: response.ArrivalStoreResponse{
			Name:      strings.TrimSpace(store.Name),
			BrandName: strings.TrimSpace(store.BrandName),
		},
	}
	connection := repositories.ArrivalRepository.FindConnectionByStore(sqls.DB(), event.TenantID, event.StoreID)
	if connection != nil && connection.WxWorkProtocolInstanceID > 0 {
		if instance := WxWorkProtocolInstanceService.GetByTenantID(connection.WxWorkProtocolInstanceID, event.TenantID); instance != nil {
			result.Store.Address = strings.TrimSpace(instance.StoreAddress)
			result.Store.Phone = strings.TrimSpace(instance.StoreContactPhone)
		}
	}
	if bindingStatus == enums.ArrivalBindingStatusBound {
		return result, nil
	}
	contactWay := repositories.ArrivalRepository.FindContactWayByScanEvent(sqls.DB(), event.ID)
	if contactWay == nil || contactWay.Status != enums.StatusOk ||
		contactWay.ContactWayStatus != enums.ArrivalContactWayStatusActive ||
		(contactWay.ExpiresAt != nil && !contactWay.ExpiresAt.After(time.Now())) {
		return result, nil
	}
	switch contactWay.Mode {
	case enums.ArrivalContactWayModeQRCode:
		publicBaseURL := strings.TrimRight(strings.TrimSpace(config.Current().Arrival.PublicBaseURL), "/")
		if strings.HasPrefix(strings.ToLower(publicBaseURL), "https://") {
			result.ContactWay.Available = true
			result.ContactWay.Mode = string(enums.ArrivalContactWayModeQRCode)
			result.ContactWay.QRCodeURL = publicBaseURL + "/api/miniprogram/arrival/contact-way/" + security.PublicResourceToken(contactWay.ID) + ".png"
		}
	case enums.ArrivalContactWayModePluginButton:
		if strings.TrimSpace(contactWay.ConfigID) != "" {
			result.ContactWay.Available = true
			result.ContactWay.Mode = string(enums.ArrivalContactWayModePluginButton)
			result.ContactWay.PlugID = strings.TrimSpace(contactWay.ConfigID)
		}
	}
	return result, nil
}

// currentBindingStatus reads the current relationship without mutating scan or
// delivery state. This keeps GET /status strictly read-only while ensuring an
// authorization or relationship revocation is reflected immediately.
func (s *arrivalLinkService) currentBindingStatus(event *models.ArrivalScanEvent) enums.ArrivalBindingStatus {
	if event == nil {
		return enums.ArrivalBindingStatusUnbound
	}
	binding := repositories.ArrivalRepository.FindBinding(
		sqls.DB(),
		event.TenantID,
		event.MiniProgramIdentityID,
		event.StoreID,
	)
	return s.bindingStatusForContext(event.TenantID, event.StoreID, binding)
}

func (s *arrivalLinkService) bindingStatusForContext(tenantID, storeID int64, binding *models.ArrivalStoreBinding) enums.ArrivalBindingStatus {
	if binding == nil || binding.TenantID != tenantID || binding.StoreID != storeID ||
		binding.Status != enums.StatusOk ||
		binding.OfficialRelationStatus == enums.ArrivalOfficialRelationStatusRevoked {
		return enums.ArrivalBindingStatusUnbound
	}
	if binding.OfficialRelationStatus != enums.ArrivalOfficialRelationStatusConfirmed {
		return enums.ArrivalBindingStatusUnbound
	}
	connection := repositories.ArrivalRepository.FindConnectionByStore(sqls.DB(), tenantID, storeID)
	if connection == nil || connection.Status != enums.StatusOk ||
		connection.ConnectionStatus != enums.ArrivalConnectionStatusActive ||
		connection.TenantAuthorizationID <= 0 ||
		connection.TenantAuthorizationID != binding.TenantAuthorizationID ||
		connection.WxWorkProtocolInstanceID <= 0 ||
		connection.WxWorkProtocolInstanceID != binding.WxWorkProtocolInstanceID {
		return enums.ArrivalBindingStatusUnbound
	}
	authorization := repositories.ArrivalRepository.GetTenantAuthorization(
		sqls.DB(),
		binding.TenantAuthorizationID,
		tenantID,
	)
	if authorization == nil || authorization.AuthorizationStatus != enums.WeComAuthorizationStatusActive {
		return enums.ArrivalBindingStatusUnbound
	}
	if binding.BindingStatus == enums.ArrivalBindingStatusBound &&
		binding.TenantAuthorizationID > 0 &&
		binding.ExternalUserIDCiphertext != "" &&
		strings.TrimSpace(binding.ExternalUserIDNonce) != "" &&
		strings.TrimSpace(binding.ExternalUserIDFingerprint) != "" &&
		binding.ContactMemberCiphertext != "" &&
		strings.TrimSpace(binding.ContactMemberNonce) != "" &&
		strings.TrimSpace(binding.ContactMemberFingerprint) != "" &&
		binding.CustomerID > 0 &&
		binding.ConversationID > 0 &&
		binding.WxWorkProtocolInstanceID > 0 &&
		strings.TrimSpace(binding.ProtocolConversationCiphertext) != "" &&
		strings.TrimSpace(binding.ProtocolConversationNonce) != "" &&
		strings.TrimSpace(binding.ProtocolConversationFingerprint) != "" &&
		binding.OfficialRelationshipAt != nil &&
		binding.ProtocolMappedAt != nil &&
		strings.TrimSpace(binding.EvidenceHash) != "" {
		security, err := newArrivalSecurity()
		if err != nil {
			return enums.ArrivalBindingStatusLegacyUnmapped
		}
		externalUserID, externalErr := security.Decrypt(
			"external_user_id",
			binding.ExternalUserIDCiphertext,
			binding.ExternalUserIDNonce,
		)
		memberUserID, memberErr := security.Decrypt(
			"contact_member",
			binding.ContactMemberCiphertext,
			binding.ContactMemberNonce,
		)
		if externalErr != nil ||
			memberErr != nil ||
			strings.TrimSpace(externalUserID) == "" ||
			strings.TrimSpace(memberUserID) == "" ||
			security.Fingerprint("external_user_id", externalUserID) != binding.ExternalUserIDFingerprint ||
			security.Fingerprint("contact_member", memberUserID) != binding.ContactMemberFingerprint ||
			binding.ContactMemberFingerprint != connection.ContactMemberFingerprint {
			return enums.ArrivalBindingStatusLegacyUnmapped
		}
		instance := WxWorkProtocolInstanceService.GetByTenantID(binding.WxWorkProtocolInstanceID, tenantID)
		customer := repositories.CustomerRepository.GetInTenant(sqls.DB(), binding.CustomerID, tenantID)
		conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), binding.ConversationID, tenantID)
		route := ConversationRouteService.GetByConversationIDInTenant(binding.ConversationID, tenantID)
		if instance == nil ||
			instance.Status != enums.StatusOk ||
			instance.StoreID != storeID ||
			customer == nil ||
			customer.Status == enums.StatusDeleted ||
			conversation == nil ||
			conversation.CustomerID != binding.CustomerID ||
			route == nil ||
			route.StoreID != storeID ||
			route.WxWorkInstanceID != binding.WxWorkProtocolInstanceID {
			return enums.ArrivalBindingStatusLegacyUnmapped
		}
		conversationID, err := security.Decrypt(
			"protocol_conversation_id",
			binding.ProtocolConversationCiphertext,
			binding.ProtocolConversationNonce,
		)
		protocolUserID := strings.TrimSpace(strings.TrimPrefix(conversationID, "S:"))
		if err == nil &&
			strings.HasPrefix(conversationID, "S:") &&
			protocolUserID != "" &&
			!strings.HasPrefix(protocolUserID, "R:") &&
			security.Fingerprint("protocol_conversation_id", conversationID) == binding.ProtocolConversationFingerprint {
			return enums.ArrivalBindingStatusBound
		}
	}
	return enums.ArrivalBindingStatusLegacyUnmapped
}

func (s *arrivalLinkService) miniProgramLoginExchanger() arrivalMiniProgramLoginExchanger {
	if s.loginExchanger != nil {
		return s.loginExchanger
	}
	return WeComProviderService
}

func (s *arrivalLinkService) contactWayProvider() arrivalContactWayCreator {
	if s.contactWayCreator != nil {
		return s.contactWayCreator
	}
	return WeComProviderService
}

func (s *arrivalLinkService) qrCodeArtifactBuilder() arrivalQRCodeArtifactBuilder {
	if s.qrCodeBuilder != nil {
		return s.qrCodeBuilder
	}
	return ArrivalQRCodeService
}

func (s *arrivalLinkService) arrivalCardSender() arrivalCardSender {
	if s.cardSender != nil {
		return s.cardSender
	}
	return WxWorkProtocolService
}

func arrivalSystemAuditFields(now time.Time) models.AuditFields {
	return models.AuditFields{
		CreatedAt:      now,
		CreateUserName: "arrival",
		UpdatedAt:      now,
		UpdateUserName: "arrival",
	}
}

func arrivalSafeEvidenceHash(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return fmt.Sprintf("%x", sum[:])
}
