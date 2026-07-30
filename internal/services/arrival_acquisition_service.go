package services

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	arrivalAcquisitionMaxProvisionAttempts = 3
	arrivalAcquisitionProvisionStaleAfter  = 10 * time.Minute
)

type arrivalCustomerAcquisitionProvider interface {
	GetCustomerAcquisitionQuota(
		authorization *models.WeComTenantAuthorization,
	) (*weComCustomerAcquisitionQuota, error)
	CreateCustomerAcquisitionLink(
		authorization *models.WeComTenantAuthorization,
		memberUserID string,
	) (*weComCustomerAcquisitionLink, error)
	GetCustomerAcquisitionLink(
		authorization *models.WeComTenantAuthorization,
		linkID string,
	) (*weComCustomerAcquisitionLink, error)
	ListCustomerAcquisitionCustomers(
		authorization *models.WeComTenantAuthorization,
		linkID string,
	) ([]weComCustomerAcquisitionCustomer, error)
}

type arrivalAcquisitionError struct {
	code string
	err  error
}

func (e *arrivalAcquisitionError) Error() string {
	if e == nil || e.err == nil {
		return "企业微信获客链接不可用"
	}
	return e.err.Error()
}

func (e *arrivalAcquisitionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func acquisitionFailureCode(err error, fallback string) string {
	var acquisitionErr *arrivalAcquisitionError
	if errors.As(err, &acquisitionErr) && strings.TrimSpace(acquisitionErr.code) != "" {
		return acquisitionErr.code
	}
	providerErr := asWeComProviderError(err)
	if providerErr != nil && providerErr.ErrCode == 48002 {
		return "acquisition_permission_denied"
	}
	return fallback
}

var ArrivalAcquisitionService = &arrivalAcquisitionService{}

type arrivalAcquisitionService struct {
	provider arrivalCustomerAcquisitionProvider
}

func (s *arrivalAcquisitionService) EnsureLink(
	connection *models.StoreArrivalConnection,
	authorization *models.WeComTenantAuthorization,
	memberUserID, requestID string,
) (*models.ArrivalAcquisitionLink, string, error) {
	if connection == nil || authorization == nil ||
		connection.TenantID <= 0 || connection.StoreID <= 0 ||
		connection.TenantAuthorizationID != authorization.ID ||
		authorization.TenantID != connection.TenantID {
		return nil, "", &arrivalAcquisitionError{
			code: "acquisition_link_invalid",
			err: newWeComProviderError(
				weComStageAuthorizationValidate,
				0,
				0,
				"企业微信获客链接上下文无效",
				false,
			),
		}
	}
	memberUserID = strings.TrimSpace(memberUserID)
	if memberUserID == "" {
		return nil, "", &arrivalAcquisitionError{
			code: "acquisition_member_unavailable",
			err: newWeComProviderError(
				weComStageContactMemberValidate,
				0,
				0,
				"门店客户联系成员不可用",
				false,
			),
		}
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, "", &arrivalAcquisitionError{
			code: "acquisition_link_invalid",
			err:  newWeComProviderError(weComStageContactWayPersist, 0, 0, "到店联动加密服务不可用", false),
		}
	}
	memberFingerprint := security.Fingerprint("contact_member", memberUserID)
	if connection.ContactMemberFingerprint != memberFingerprint {
		return nil, "", &arrivalAcquisitionError{
			code: "acquisition_member_out_of_scope",
			err: newWeComProviderError(
				weComStageContactMemberValidate,
				0,
				0,
				"门店客户联系成员与连接配置不一致",
				false,
			),
		}
	}
	requestID = tracex.EnsureRequestID(requestID)
	existing := repositories.ArrivalRepository.FindAcquisitionLink(
		sqls.DB(),
		connection.TenantID,
		authorization.ID,
		connection.StoreID,
		memberFingerprint,
	)
	if existing != nil {
		return s.useOrRetryLink(existing, authorization, memberUserID, requestID)
	}

	now := time.Now()
	link := &models.ArrivalAcquisitionLink{
		TenantID:                 connection.TenantID,
		TenantAuthorizationID:    authorization.ID,
		StoreID:                  connection.StoreID,
		ContactMemberFingerprint: memberFingerprint,
		LinkStatus:               enums.ArrivalAcquisitionLinkStatusProvisioning,
		ProvisionAttemptCount:    1,
		LastProvisionRequestID:   requestID,
		LastProvisionAttemptAt:   &now,
		Status:                   enums.StatusOk,
		AuditFields:              arrivalSystemAuditFields(now),
	}
	created, err := repositories.ArrivalRepository.CreateAcquisitionLinkIfAbsent(sqls.DB(), link)
	if err != nil {
		return nil, "", &arrivalAcquisitionError{
			code: "acquisition_link_create_failed",
			err:  newWeComProviderError(weComStageContactWayPersist, 0, 0, "保存企业微信获客链接记录失败", true),
		}
	}
	if !created {
		existing = repositories.ArrivalRepository.FindAcquisitionLink(
			sqls.DB(),
			connection.TenantID,
			authorization.ID,
			connection.StoreID,
			memberFingerprint,
		)
		if existing == nil {
			return nil, "", &arrivalAcquisitionError{
				code: "acquisition_link_create_failed",
				err:  newWeComProviderError(weComStageContactWayPersist, 0, 0, "企业微信获客链接并发创建状态未知", true),
			}
		}
		return s.useOrRetryLink(existing, authorization, memberUserID, requestID)
	}
	return s.provisionLink(link, authorization, memberUserID, requestID)
}

func (s *arrivalAcquisitionService) useOrRetryLink(
	link *models.ArrivalAcquisitionLink,
	authorization *models.WeComTenantAuthorization,
	memberUserID, requestID string,
) (*models.ArrivalAcquisitionLink, string, error) {
	if link == nil {
		return nil, "", &arrivalAcquisitionError{
			code: "acquisition_link_invalid",
			err:  newWeComProviderError(weComStageAcquisitionGet, 0, 0, "企业微信获客链接不存在", false),
		}
	}
	if link.Status != enums.StatusOk || link.LinkStatus == enums.ArrivalAcquisitionLinkStatusDisabled {
		return link, "", &arrivalAcquisitionError{
			code: "acquisition_link_invalid",
			err:  newWeComProviderError(weComStageAcquisitionGet, 0, 0, "企业微信获客链接已停用", false),
		}
	}
	if link.LinkStatus == enums.ArrivalAcquisitionLinkStatusActive {
		return s.decryptActiveLink(link)
	}
	now := time.Now()
	claimed, err := repositories.ArrivalRepository.TryClaimAcquisitionLinkProvision(
		sqls.DB(),
		link.ID,
		link.TenantID,
		now,
		now.Add(-arrivalAcquisitionProvisionStaleAfter),
		requestID,
		arrivalAcquisitionMaxProvisionAttempts,
	)
	if err != nil {
		return link, "", &arrivalAcquisitionError{
			code: "acquisition_link_create_failed",
			err:  newWeComProviderError(weComStageContactWayPersist, 0, 0, "认领企业微信获客链接创建任务失败", true),
		}
	}
	if !claimed {
		current := repositories.ArrivalRepository.GetAcquisitionLink(sqls.DB(), link.ID, link.TenantID)
		if current != nil && current.LinkStatus == enums.ArrivalAcquisitionLinkStatusActive {
			return s.decryptActiveLink(current)
		}
		if current != nil && current.LinkStatus == enums.ArrivalAcquisitionLinkStatusFailed && !current.FailureRetryable {
			return current, "", acquisitionErrorFromRecord(current)
		}
		return current, "", &arrivalAcquisitionError{
			code: "acquisition_link_create_failed",
			err:  newWeComProviderError(weComStageAcquisitionCreate, 0, 0, "企业微信获客链接正在创建，请稍后重试", true),
		}
	}
	current := repositories.ArrivalRepository.GetAcquisitionLink(sqls.DB(), link.ID, link.TenantID)
	return s.provisionLink(current, authorization, memberUserID, requestID)
}

func (s *arrivalAcquisitionService) provisionLink(
	link *models.ArrivalAcquisitionLink,
	authorization *models.WeComTenantAuthorization,
	memberUserID, requestID string,
) (*models.ArrivalAcquisitionLink, string, error) {
	if link == nil {
		return nil, "", &arrivalAcquisitionError{
			code: "acquisition_link_create_failed",
			err:  newWeComProviderError(weComStageAcquisitionCreate, 0, 0, "企业微信获客链接任务不存在", true),
		}
	}
	provider := s.customerAcquisitionProvider()
	providerLinkID := strings.TrimSpace(link.ProviderLinkID)
	if providerLinkID == "" {
		quota, err := provider.GetCustomerAcquisitionQuota(authorization)
		if err != nil {
			code := acquisitionFailureCode(err, "acquisition_link_create_failed")
			if providerErr := asWeComProviderError(err); providerErr != nil && providerErr.ErrCode == 48002 {
				code = "acquisition_permission_denied"
			}
			return nil, "", s.failLink(link, code, requestID, err)
		}
		if quota == nil {
			return nil, "", s.failLink(
				link,
				"acquisition_link_create_failed",
				requestID,
				newWeComProviderError(weComStageAcquisitionQuota, 0, 0, "企业微信未返回获客助手额度", false),
			)
		}
		_ = repositories.ArrivalRepository.UpdateAcquisitionLink(sqls.DB(), link.ID, link.TenantID, map[string]any{
			"quota_total":      quota.Total,
			"quota_balance":    quota.Balance,
			"updated_at":       time.Now(),
			"update_user_name": "arrival",
		})
		link.QuotaTotal = quota.Total
		link.QuotaBalance = quota.Balance
		if quota.Balance <= 0 {
			return nil, "", s.failLink(
				link,
				"acquisition_quota_exhausted",
				requestID,
				newWeComProviderError(weComStageAcquisitionQuota, 0, 0, "企业微信获客助手剩余额度为零", false),
			)
		}

		created, err := provider.CreateCustomerAcquisitionLink(authorization, memberUserID)
		if err != nil {
			return nil, "", s.failLink(
				link,
				acquisitionFailureCode(err, "acquisition_link_create_failed"),
				requestID,
				err,
			)
		}
		if created == nil || strings.TrimSpace(created.LinkID) == "" || strings.TrimSpace(created.URL) == "" {
			return nil, "", s.failLink(
				link,
				"acquisition_link_invalid",
				requestID,
				newWeComProviderError(weComStageAcquisitionCreate, 0, 0, "企业微信获客链接创建结果无效", false),
			)
		}
		providerLinkID = strings.TrimSpace(created.LinkID)
		if err := repositories.ArrivalRepository.UpdateAcquisitionLink(
			sqls.DB(),
			link.ID,
			link.TenantID,
			map[string]any{
				"provider_link_id":          providerLinkID,
				"provider_create_time":      created.CreateTime,
				"last_provision_request_id": requestID,
				"updated_at":                time.Now(),
				"update_user_name":          "arrival",
			},
		); err != nil {
			return nil, "", s.failLink(
				link,
				"acquisition_link_create_failed",
				requestID,
				newWeComProviderError(weComStageContactWayPersist, 0, 0, "保存企业微信获客链接标识失败", false),
			)
		}
		link.ProviderLinkID = providerLinkID
		link.ProviderCreateTime = created.CreateTime
	}

	verified, err := provider.GetCustomerAcquisitionLink(authorization, providerLinkID)
	if err != nil {
		return nil, "", s.failLink(
			link,
			"acquisition_link_verify_failed",
			requestID,
			err,
		)
	}
	if err := validateAcquisitionLinkDetails(providerLinkID, memberUserID, verified); err != nil {
		return nil, "", s.failLink(link, "acquisition_link_invalid", requestID, err)
	}

	security, err := newArrivalSecurity()
	if err != nil {
		return nil, "", s.failLink(
			link,
			"acquisition_link_invalid",
			requestID,
			newWeComProviderError(weComStageContactWayPersist, 0, 0, "到店联动加密服务不可用", false),
		)
	}
	linkURL := strings.TrimSpace(verified.URL)
	ciphertext, nonce, err := security.Encrypt("acquisition_link_url", linkURL)
	if err != nil {
		return nil, "", s.failLink(
			link,
			"acquisition_link_invalid",
			requestID,
			newWeComProviderError(weComStageContactWayPersist, 0, 0, "保存企业微信获客链接失败", false),
		)
	}
	now := time.Now()
	if err := repositories.ArrivalRepository.UpdateAcquisitionLink(sqls.DB(), link.ID, link.TenantID, map[string]any{
		"provider_link_id":             providerLinkID,
		"provider_link_url_ciphertext": ciphertext,
		"provider_link_url_nonce":      nonce,
		"provider_create_time":         verified.CreateTime,
		"link_status":                  enums.ArrivalAcquisitionLinkStatusActive,
		"last_verified_at":             now,
		"failure_code":                 "",
		"failure_stage":                "",
		"provider_http_status":         0,
		"provider_error_code":          0,
		"provider_error_message":       "",
		"failure_retryable":            false,
		"next_provision_retry_at":      nil,
		"last_provision_request_id":    requestID,
		"updated_at":                   now,
		"update_user_name":             "arrival",
	}); err != nil {
		return nil, "", s.failLink(
			link,
			"acquisition_link_create_failed",
			requestID,
			newWeComProviderError(weComStageContactWayPersist, 0, 0, "激活企业微信获客链接失败", false),
		)
	}
	current := repositories.ArrivalRepository.GetAcquisitionLink(sqls.DB(), link.ID, link.TenantID)
	if current == nil {
		return nil, "", &arrivalAcquisitionError{
			code: "acquisition_link_invalid",
			err:  newWeComProviderError(weComStageContactWayPersist, 0, 0, "企业微信获客链接记录丢失", false),
		}
	}
	return current, linkURL, nil
}

func (s *arrivalAcquisitionService) decryptActiveLink(
	link *models.ArrivalAcquisitionLink,
) (*models.ArrivalAcquisitionLink, string, error) {
	if link == nil ||
		link.LinkStatus != enums.ArrivalAcquisitionLinkStatusActive ||
		strings.TrimSpace(link.ProviderLinkID) == "" ||
		strings.TrimSpace(link.ProviderLinkURLCiphertext) == "" ||
		strings.TrimSpace(link.ProviderLinkURLNonce) == "" {
		return link, "", &arrivalAcquisitionError{
			code: "acquisition_link_invalid",
			err:  newWeComProviderError(weComStageAcquisitionGet, 0, 0, "企业微信获客链接记录不完整", false),
		}
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return link, "", &arrivalAcquisitionError{
			code: "acquisition_link_invalid",
			err:  newWeComProviderError(weComStageContactWayPersist, 0, 0, "到店联动加密服务不可用", false),
		}
	}
	linkURL, err := security.Decrypt(
		"acquisition_link_url",
		link.ProviderLinkURLCiphertext,
		link.ProviderLinkURLNonce,
	)
	if err != nil || strings.TrimSpace(linkURL) == "" {
		return link, "", &arrivalAcquisitionError{
			code: "acquisition_link_invalid",
			err:  newWeComProviderError(weComStageAcquisitionGet, 0, 0, "企业微信获客链接无法解密", false),
		}
	}
	return link, strings.TrimSpace(linkURL), nil
}

func (s *arrivalAcquisitionService) failLink(
	link *models.ArrivalAcquisitionLink,
	failureCode, requestID string,
	provisionErr error,
) error {
	providerErr := asWeComProviderError(provisionErr)
	if providerErr == nil {
		providerErr = &weComProviderError{
			Stage:     weComStageAcquisitionCreate,
			ErrMsg:    sanitizeWeComProviderMessage(fmt.Sprint(provisionErr)),
			Retryable: false,
		}
	}
	now := time.Now()
	current := repositories.ArrivalRepository.GetAcquisitionLink(sqls.DB(), link.ID, link.TenantID)
	attemptCount := link.ProvisionAttemptCount
	if current != nil {
		attemptCount = current.ProvisionAttemptCount
	}
	retryable := providerErr.Retryable && attemptCount < arrivalAcquisitionMaxProvisionAttempts
	var nextRetryAt any
	if retryable {
		nextRetryAt = now.Add(arrivalContactWayRetryDelay(attemptCount))
	}
	_ = repositories.ArrivalRepository.UpdateAcquisitionLink(sqls.DB(), link.ID, link.TenantID, map[string]any{
		"link_status":               enums.ArrivalAcquisitionLinkStatusFailed,
		"failure_code":              strings.TrimSpace(failureCode),
		"failure_stage":             normalizeWeComProviderStage(providerErr.Stage),
		"provider_http_status":      providerErr.HTTPStatus,
		"provider_error_code":       providerErr.ErrCode,
		"provider_error_message":    sanitizeWeComProviderMessage(providerErr.ErrMsg),
		"failure_retryable":         retryable,
		"last_provision_request_id": tracex.EnsureRequestID(requestID),
		"next_provision_retry_at":   nextRetryAt,
		"updated_at":                now,
		"update_user_name":          "arrival",
	})
	slog.Error(
		"arrival customer acquisition link provision failed",
		"request_id", tracex.EnsureRequestID(requestID),
		"store_id", link.StoreID,
		"authorization_id", link.TenantAuthorizationID,
		"provider_mode", enums.ArrivalContactProviderModeCustomerAcquisition,
		"failure_stage", normalizeWeComProviderStage(providerErr.Stage),
		"provider_http_status", providerErr.HTTPStatus,
		"provider_error_code", providerErr.ErrCode,
		"sanitized_provider_error_message", sanitizeWeComProviderMessage(providerErr.ErrMsg),
		"retryable", retryable,
	)
	return &arrivalAcquisitionError{code: strings.TrimSpace(failureCode), err: providerErr}
}

func acquisitionErrorFromRecord(link *models.ArrivalAcquisitionLink) error {
	if link == nil {
		return &arrivalAcquisitionError{
			code: "acquisition_link_invalid",
			err:  newWeComProviderError(weComStageAcquisitionGet, 0, 0, "企业微信获客链接不存在", false),
		}
	}
	return &arrivalAcquisitionError{
		code: strings.TrimSpace(link.FailureCode),
		err: &weComProviderError{
			Stage:      normalizeWeComProviderStage(link.FailureStage),
			HTTPStatus: link.ProviderHTTPStatus,
			ErrCode:    link.ProviderErrorCode,
			ErrMsg:     sanitizeWeComProviderMessage(link.ProviderErrorMessage),
			Retryable:  link.FailureRetryable,
		},
	}
}

func validateAcquisitionLinkDetails(
	linkID, memberUserID string,
	details *weComCustomerAcquisitionLink,
) error {
	if details == nil ||
		strings.TrimSpace(details.LinkID) != strings.TrimSpace(linkID) ||
		strings.TrimSpace(details.URL) == "" {
		return newWeComProviderError(weComStageAcquisitionGet, 0, 0, "企业微信获客链接详情不匹配", false)
	}
	if len(details.UserList) != 1 || strings.TrimSpace(details.UserList[0]) != strings.TrimSpace(memberUserID) {
		return newWeComProviderError(weComStageAcquisitionGet, 0, 0, "企业微信获客链接成员范围不匹配", false)
	}
	return nil
}

func appendAcquisitionCustomerChannel(linkURL, state string) (string, error) {
	linkURL = strings.TrimSpace(linkURL)
	state = strings.TrimSpace(state)
	if state == "" || len([]byte(state)) > 64 {
		return "", fmt.Errorf("企业微信获客渠道标识必须为 1 至 64 字节")
	}
	parsed, err := url.Parse(linkURL)
	if err != nil || parsed == nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" {
		return "", fmt.Errorf("企业微信获客链接无效")
	}
	query := parsed.Query()
	query.Set("customer_channel", state)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (s *arrivalAcquisitionService) Preflight(
	authorization *models.WeComTenantAuthorization,
) (*weComCustomerAcquisitionQuota, error) {
	quota, err := s.customerAcquisitionProvider().GetCustomerAcquisitionQuota(authorization)
	if err != nil {
		return nil, &arrivalAcquisitionError{
			code: acquisitionFailureCode(err, "acquisition_link_verify_failed"),
			err:  err,
		}
	}
	if quota == nil {
		return nil, &arrivalAcquisitionError{
			code: "acquisition_link_verify_failed",
			err:  newWeComProviderError(weComStageAcquisitionQuota, 0, 0, "企业微信未返回获客助手额度", false),
		}
	}
	if quota.Balance <= 0 {
		return quota, &arrivalAcquisitionError{
			code: "acquisition_quota_exhausted",
			err:  newWeComProviderError(weComStageAcquisitionQuota, 0, 0, "企业微信获客助手剩余额度为零", false),
		}
	}
	return quota, nil
}

func (s *arrivalAcquisitionService) ReconcileCustomers(limit int) int {
	links := repositories.ArrivalRepository.FindAcquisitionLinksForSync(sqls.DB(), limit)
	reconciled := 0
	for i := range links {
		count, err := s.reconcileLinkCustomers(&links[i])
		if err != nil {
			s.recordCustomerSyncFailure(&links[i], err)
			continue
		}
		reconciled += count
	}
	return reconciled
}

func (s *arrivalAcquisitionService) reconcileLinkCustomers(
	link *models.ArrivalAcquisitionLink,
) (int, error) {
	if link == nil || link.LinkStatus != enums.ArrivalAcquisitionLinkStatusActive {
		return 0, nil
	}
	authorization := repositories.ArrivalRepository.GetTenantAuthorization(
		sqls.DB(),
		link.TenantAuthorizationID,
		link.TenantID,
	)
	connection := repositories.ArrivalRepository.FindConnectionByStore(
		sqls.DB(),
		link.TenantID,
		link.StoreID,
	)
	if authorization == nil ||
		authorization.AuthorizationStatus != enums.WeComAuthorizationStatusActive ||
		connection == nil ||
		connection.Status != enums.StatusOk ||
		connection.ConnectionStatus != enums.ArrivalConnectionStatusActive ||
		connection.TenantAuthorizationID != authorization.ID ||
		connection.ContactMemberFingerprint != link.ContactMemberFingerprint {
		return 0, &arrivalAcquisitionError{
			code: "acquisition_member_unavailable",
			err:  newWeComProviderError(weComStageContactMemberValidate, 0, 0, "企业微信获客链接门店上下文不可用", false),
		}
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return 0, err
	}
	memberUserID, err := security.Decrypt(
		"contact_member",
		connection.ContactMemberCiphertext,
		connection.ContactMemberNonce,
	)
	if err != nil ||
		strings.TrimSpace(memberUserID) == "" ||
		security.Fingerprint("contact_member", memberUserID) != link.ContactMemberFingerprint {
		return 0, &arrivalAcquisitionError{
			code: "acquisition_member_unavailable",
			err:  newWeComProviderError(weComStageContactMemberValidate, 0, 0, "企业微信获客链接成员不可用", false),
		}
	}
	provider := s.customerAcquisitionProvider()
	details, err := provider.GetCustomerAcquisitionLink(authorization, link.ProviderLinkID)
	if err != nil {
		return 0, &arrivalAcquisitionError{
			code: acquisitionFailureCode(err, "acquisition_link_verify_failed"),
			err:  err,
		}
	}
	if err := validateAcquisitionLinkDetails(link.ProviderLinkID, memberUserID, details); err != nil {
		return 0, &arrivalAcquisitionError{code: "acquisition_link_invalid", err: err}
	}
	customers, err := provider.ListCustomerAcquisitionCustomers(authorization, link.ProviderLinkID)
	if err != nil {
		return 0, &arrivalAcquisitionError{
			code: acquisitionFailureCode(err, "acquisition_customer_sync_failed"),
			err:  err,
		}
	}
	reconciled := 0
	for _, customer := range customers {
		state := strings.TrimSpace(customer.State)
		externalUserID := strings.TrimSpace(customer.ExternalUserID)
		followUserID := strings.TrimSpace(customer.UserID)
		if state == "" ||
			len([]byte(state)) > 64 ||
			externalUserID == "" ||
			followUserID == "" ||
			followUserID != strings.TrimSpace(memberUserID) {
			continue
		}
		contactWay := repositories.ArrivalRepository.FindContactWayByStateHash(
			sqls.DB(),
			security.Fingerprint("contact_state", state),
		)
		if contactWay == nil ||
			contactWay.TenantID != link.TenantID ||
			contactWay.StoreID != link.StoreID ||
			contactWay.TenantAuthorizationID != link.TenantAuthorizationID ||
			contactWayProviderMode(contactWay) != enums.ArrivalContactProviderModeCustomerAcquisition ||
			contactWay.AcquisitionLinkID != link.ID {
			continue
		}
		evidenceHash := security.Fingerprint(
			"acquisition_customer_evidence",
			fmt.Sprintf(
				"%d:%d:%s:%s",
				link.ID,
				contactWay.ID,
				security.Fingerprint("external_user_id", externalUserID),
				security.Fingerprint("contact_state", state),
			),
		)
		if err := WeComProviderCallbackService.confirmOfficialRelationshipForContactWay(
			contactWay,
			externalUserID,
			followUserID,
			evidenceHash,
			nil,
		); err != nil {
			continue
		}
		reconciled++
	}
	now := time.Now()
	securityCiphertext, nonce, err := security.Encrypt("acquisition_link_url", strings.TrimSpace(details.URL))
	if err != nil {
		return reconciled, &arrivalAcquisitionError{
			code: "acquisition_link_verify_failed",
			err:  newWeComProviderError(weComStageContactWayPersist, 0, 0, "更新企业微信获客链接失败", false),
		}
	}
	if err := repositories.ArrivalRepository.UpdateAcquisitionLink(
		sqls.DB(),
		link.ID,
		link.TenantID,
		map[string]any{
			"provider_link_url_ciphertext": securityCiphertext,
			"provider_link_url_nonce":      nonce,
			"last_verified_at":             now,
			"last_customer_sync_at":        now,
			"failure_code":                 "",
			"failure_stage":                "",
			"provider_http_status":         0,
			"provider_error_code":          0,
			"provider_error_message":       "",
			"failure_retryable":            false,
			"updated_at":                   now,
			"update_user_name":             "arrival_reconciliation",
		},
	); err != nil {
		return reconciled, err
	}
	return reconciled, nil
}

func (s *arrivalAcquisitionService) recordCustomerSyncFailure(
	link *models.ArrivalAcquisitionLink,
	syncErr error,
) {
	if link == nil {
		return
	}
	providerErr := asWeComProviderError(syncErr)
	if providerErr == nil {
		providerErr = &weComProviderError{
			Stage:     weComStageAcquisitionCustomer,
			ErrMsg:    sanitizeWeComProviderMessage(fmt.Sprint(syncErr)),
			Retryable: false,
		}
	}
	failureCode := acquisitionFailureCode(syncErr, "acquisition_customer_sync_failed")
	now := time.Now()
	_ = repositories.ArrivalRepository.UpdateAcquisitionLink(
		sqls.DB(),
		link.ID,
		link.TenantID,
		map[string]any{
			"failure_code":           failureCode,
			"failure_stage":          normalizeWeComProviderStage(providerErr.Stage),
			"provider_http_status":   providerErr.HTTPStatus,
			"provider_error_code":    providerErr.ErrCode,
			"provider_error_message": sanitizeWeComProviderMessage(providerErr.ErrMsg),
			"failure_retryable":      providerErr.Retryable,
			"last_customer_sync_at":  now,
			"updated_at":             now,
			"update_user_name":       "arrival_reconciliation",
		},
	)
	slog.Warn(
		"arrival customer acquisition reconciliation failed",
		"store_id", link.StoreID,
		"authorization_id", link.TenantAuthorizationID,
		"provider_mode", enums.ArrivalContactProviderModeCustomerAcquisition,
		"failure_stage", normalizeWeComProviderStage(providerErr.Stage),
		"provider_http_status", providerErr.HTTPStatus,
		"provider_error_code", providerErr.ErrCode,
		"sanitized_provider_error_message", sanitizeWeComProviderMessage(providerErr.ErrMsg),
		"retryable", providerErr.Retryable,
	)
}

func (s *arrivalAcquisitionService) customerAcquisitionProvider() arrivalCustomerAcquisitionProvider {
	if s.provider != nil {
		return s.provider
	}
	return WeComProviderService
}
