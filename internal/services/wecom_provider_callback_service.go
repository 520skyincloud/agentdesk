package services

import (
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const weComCallbackReplayWindow = 10 * time.Minute

var WeComProviderCallbackService = &weComProviderCallbackService{}

type weComProviderCallbackService struct{}

type weComCallbackStageError struct {
	stage string
	err   error
}

func (e *weComCallbackStageError) Error() string {
	return e.err.Error()
}

func (e *weComCallbackStageError) Unwrap() error {
	return e.err
}

func newWeComCallbackStageError(stage string, err error) error {
	if err == nil {
		return nil
	}
	return &weComCallbackStageError{stage: stage, err: err}
}

func WeComCallbackFailureStage(err error) string {
	var staged *weComCallbackStageError
	if errors.As(err, &staged) && strings.TrimSpace(staged.stage) != "" {
		return staged.stage
	}
	return "processing"
}

type weComProviderCallbackPayload struct {
	XMLName        xml.Name `xml:"xml"`
	SuiteID        string   `xml:"SuiteId"`
	InfoType       string   `xml:"InfoType"`
	SuiteTicket    string   `xml:"SuiteTicket"`
	AuthCode       string   `xml:"AuthCode"`
	AuthCorpID     string   `xml:"AuthCorpId"`
	TimeStamp      int64    `xml:"TimeStamp"`
	ToUserName     string   `xml:"ToUserName"`
	CreateTime     int64    `xml:"CreateTime"`
	Event          string   `xml:"Event"`
	ChangeType     string   `xml:"ChangeType"`
	UserID         string   `xml:"UserID"`
	ExternalUserID string   `xml:"ExternalUserID"`
	State          string   `xml:"State"`
}

func (s *weComProviderCallbackService) VerifyURL(kind, signature, timestamp, nonce, encryptedEcho string) (string, error) {
	if err := validateWeComCallbackTimestamp(timestamp, time.Now()); err != nil {
		return "", newWeComCallbackStageError("timestamp", err)
	}
	cfg := config.Current().Arrival
	if !verifyWeComCallbackSignature(cfg.WeComProviderCallbackToken, timestamp, nonce, encryptedEcho, signature) {
		return "", newWeComCallbackStageError("signature", fmt.Errorf("企业微信回调签名无效"))
	}
	plaintext, receiveID, err := decryptWeComCallback(cfg.WeComProviderEncodingAESKey, encryptedEcho)
	if err != nil {
		return "", newWeComCallbackStageError("decrypt", err)
	}
	if strings.TrimSpace(kind) == "command" && strings.TrimSpace(receiveID) == "" {
		return "", newWeComCallbackStageError("receive_id", fmt.Errorf("企业微信指令回调接收方为空"))
	}
	return string(plaintext), nil
}

func (s *weComProviderCallbackService) Handle(kind, signature, timestamp, nonce string, body []byte) error {
	if err := validateWeComCallbackTimestamp(timestamp, time.Now()); err != nil {
		return newWeComCallbackStageError("timestamp", err)
	}
	encrypted, err := decodeWeComCallbackEnvelope(body)
	if err != nil {
		return newWeComCallbackStageError("envelope", err)
	}
	cfg := config.Current().Arrival
	if !verifyWeComCallbackSignature(cfg.WeComProviderCallbackToken, timestamp, nonce, encrypted, signature) {
		return newWeComCallbackStageError("signature", fmt.Errorf("企业微信回调签名无效"))
	}
	plaintext, receiveID, err := decryptWeComCallback(cfg.WeComProviderEncodingAESKey, encrypted)
	if err != nil {
		return newWeComCallbackStageError("decrypt", err)
	}
	payload := &weComProviderCallbackPayload{}
	if err := xml.Unmarshal(plaintext, payload); err != nil {
		return newWeComCallbackStageError("payload", fmt.Errorf("企业微信回调消息无效"))
	}
	if err := validateWeComProviderPayload(kind, receiveID, payload); err != nil {
		return newWeComCallbackStageError("receive_id", err)
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return newWeComCallbackStageError("security", err)
	}
	eventHash := security.Fingerprint("wecom_callback_event", strings.Join([]string{
		strings.TrimSpace(kind),
		strings.TrimSpace(timestamp),
		strings.TrimSpace(nonce),
		encrypted,
	}, "\x00"))
	now := time.Now()
	occurredAt := callbackOccurredAt(payload, timestamp)
	event := &models.WeComProviderCallbackEvent{
		EventHash:        eventHash,
		CallbackKind:     strings.TrimSpace(kind),
		InfoType:         firstNonBlank(payload.InfoType, payload.Event, payload.ChangeType),
		SuiteFingerprint: security.Fingerprint("suite_id", payload.SuiteID),
		CorpFingerprint:  security.Fingerprint("corp_id", firstNonBlank(payload.AuthCorpID, payload.ToUserName)),
		OccurredAt:       &occurredAt,
		CallbackStatus:   enums.ArrivalCallbackStatusProcessing,
		AuditFields:      arrivalSystemAuditFields(now),
	}
	created, err := repositories.ArrivalRepository.CreateCallbackEventIfAbsent(sqls.DB(), event)
	if err != nil {
		return newWeComCallbackStageError("persistence", fmt.Errorf("保存企业微信回调幂等记录失败"))
	}
	if !created {
		existing := repositories.ArrivalRepository.FindCallbackEventByHash(sqls.DB(), eventHash)
		if existing == nil ||
			existing.CallbackStatus == enums.ArrivalCallbackStatusProcessed ||
			existing.CallbackStatus == enums.ArrivalCallbackStatusIgnored ||
			(existing.CallbackStatus == enums.ArrivalCallbackStatusProcessing && existing.UpdatedAt.After(now.Add(-weComCallbackReplayWindow))) {
			return nil
		}
		event = existing
		_ = repositories.ArrivalRepository.UpdateCallbackEvent(sqls.DB(), event.ID, map[string]any{
			"callback_status":  enums.ArrivalCallbackStatusProcessing,
			"failure_code":     "",
			"updated_at":       now,
			"update_user_name": "arrival_provider",
		})
	}
	status, failureCode, processErr := s.process(kind, payload, event)
	updates := map[string]any{
		"callback_status":  status,
		"failure_code":     failureCode,
		"updated_at":       time.Now(),
		"update_user_name": "arrival_provider",
	}
	if processErr != nil {
		updates["callback_status"] = enums.ArrivalCallbackStatusFailed
		if strings.TrimSpace(failureCode) == "" {
			updates["failure_code"] = "processing_failed"
		}
	}
	_ = repositories.ArrivalRepository.UpdateCallbackEvent(sqls.DB(), event.ID, updates)
	if processErr != nil {
		return newWeComCallbackStageError("processing", processErr)
	}
	return nil
}

func (s *weComProviderCallbackService) process(kind string, payload *weComProviderCallbackPayload, event *models.WeComProviderCallbackEvent) (enums.ArrivalCallbackStatus, string, error) {
	if strings.TrimSpace(kind) == "command" {
		return s.processCommand(payload, event)
	}
	return s.processData(payload, event)
}

func (s *weComProviderCallbackService) processCommand(payload *weComProviderCallbackPayload, event *models.WeComProviderCallbackEvent) (enums.ArrivalCallbackStatus, string, error) {
	infoType := strings.ToLower(strings.TrimSpace(payload.InfoType))
	switch infoType {
	case "suite_ticket":
		if strings.TrimSpace(payload.SuiteTicket) == "" {
			return enums.ArrivalCallbackStatusFailed, "suite_ticket_missing", fmt.Errorf("企业微信 suite ticket 回调缺少票据")
		}
		if err := WeComProviderService.StoreSuiteTicket(payload.SuiteID, payload.SuiteTicket, callbackOccurredAt(payload, "")); err != nil {
			return enums.ArrivalCallbackStatusFailed, "suite_ticket_store_failed", err
		}
		return enums.ArrivalCallbackStatusProcessed, "", nil
	case "create_auth":
		// The browser authorization callback owns the one-time auth_code exchange
		// because it also carries the opaque onboarding state.
		return enums.ArrivalCallbackStatusProcessed, "", nil
	case "change_auth":
		if err := s.refreshAuthorization(payload.AuthCorpID); err != nil {
			return enums.ArrivalCallbackStatusFailed, "authorization_refresh_failed", err
		}
		return enums.ArrivalCallbackStatusProcessed, "", nil
	case "cancel_auth":
		if err := s.revokeAuthorization(payload.AuthCorpID, event); err != nil {
			return enums.ArrivalCallbackStatusFailed, "authorization_revoke_failed", err
		}
		return enums.ArrivalCallbackStatusProcessed, "", nil
	default:
		return enums.ArrivalCallbackStatusIgnored, "", nil
	}
}

func (s *weComProviderCallbackService) processData(payload *weComProviderCallbackPayload, event *models.WeComProviderCallbackEvent) (enums.ArrivalCallbackStatus, string, error) {
	eventType := strings.ToLower(strings.TrimSpace(payload.Event))
	changeType := strings.ToLower(strings.TrimSpace(payload.ChangeType))
	if eventType != "change_external_contact" {
		return enums.ArrivalCallbackStatusIgnored, "", nil
	}
	switch changeType {
	case "add_external_contact":
		if err := s.confirmOfficialRelationship(payload, event); err != nil {
			return enums.ArrivalCallbackStatusFailed, "relationship_binding_failed", err
		}
		return enums.ArrivalCallbackStatusProcessed, "", nil
	case "del_external_contact", "del_follow_user":
		if err := s.invalidateOfficialRelationship(payload, event); err != nil {
			return enums.ArrivalCallbackStatusFailed, "relationship_invalidate_failed", err
		}
		return enums.ArrivalCallbackStatusProcessed, "", nil
	default:
		return enums.ArrivalCallbackStatusIgnored, "", nil
	}
}

func (s *weComProviderCallbackService) confirmOfficialRelationship(payload *weComProviderCallbackPayload, callbackEvent *models.WeComProviderCallbackEvent) error {
	state := strings.TrimSpace(payload.State)
	externalUserID := strings.TrimSpace(payload.ExternalUserID)
	memberUserID := strings.TrimSpace(payload.UserID)
	if state == "" || externalUserID == "" || memberUserID == "" {
		return fmt.Errorf("企业微信客户关系回调缺少确定性关联字段")
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return err
	}
	contactWay := repositories.ArrivalRepository.FindContactWayByStateHash(sqls.DB(), security.Fingerprint("contact_state", state))
	if contactWay == nil || contactWay.Status != enums.StatusOk {
		return fmt.Errorf("企业微信客户关系回调未命中到店扫码事件")
	}
	authorization := repositories.ArrivalRepository.GetTenantAuthorization(sqls.DB(), contactWay.TenantAuthorizationID, contactWay.TenantID)
	if authorization == nil || authorization.AuthorizationStatus != enums.WeComAuthorizationStatusActive {
		return fmt.Errorf("到店扫码关联的企微授权不可用")
	}
	expectedCorpID, err := security.Decrypt("corp_id", authorization.CorpIDCiphertext, authorization.CorpIDNonce)
	if err != nil || strings.TrimSpace(expectedCorpID) == "" ||
		strings.TrimSpace(firstNonBlank(payload.AuthCorpID, payload.ToUserName)) != strings.TrimSpace(expectedCorpID) {
		return fmt.Errorf("企业微信客户关系主体与门店授权不一致")
	}
	return s.confirmOfficialRelationshipForContactWay(
		contactWay,
		externalUserID,
		memberUserID,
		arrivalSafeEvidenceHash("official_relationship", callbackEvent.EventHash),
		callbackEvent,
	)
}

func (s *weComProviderCallbackService) confirmOfficialRelationshipForContactWay(
	contactWay *models.ArrivalContactWay,
	externalUserID, memberUserID, evidenceHash string,
	callbackEvent *models.WeComProviderCallbackEvent,
) error {
	if contactWay == nil ||
		strings.TrimSpace(externalUserID) == "" ||
		strings.TrimSpace(memberUserID) == "" ||
		strings.TrimSpace(evidenceHash) == "" {
		return fmt.Errorf("企业微信客户关系缺少确定性关联字段")
	}
	scanEvent := repositories.ArrivalRepository.GetScanEvent(sqls.DB(), contactWay.ScanEventID, contactWay.TenantID)
	connection := repositories.ArrivalRepository.FindConnectionByStore(sqls.DB(), contactWay.TenantID, contactWay.StoreID)
	authorization := repositories.ArrivalRepository.GetTenantAuthorization(sqls.DB(), contactWay.TenantAuthorizationID, contactWay.TenantID)
	if scanEvent == nil || connection == nil || authorization == nil ||
		connection.ID <= 0 ||
		connection.TenantAuthorizationID != authorization.ID ||
		connection.WxWorkProtocolInstanceID <= 0 ||
		authorization.AuthorizationStatus != enums.WeComAuthorizationStatusActive {
		return fmt.Errorf("到店扫码关联的企微授权或员工实例不可用")
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return err
	}
	expectedMember, err := security.Decrypt("contact_member", connection.ContactMemberCiphertext, connection.ContactMemberNonce)
	if err != nil || strings.TrimSpace(expectedMember) != strings.TrimSpace(memberUserID) {
		return fmt.Errorf("企业微信客户关系成员与门店配置不一致")
	}
	if contactWayProviderMode(contactWay) == enums.ArrivalContactProviderModeCustomerAcquisition {
		link := repositories.ArrivalRepository.GetAcquisitionLink(
			sqls.DB(),
			contactWay.AcquisitionLinkID,
			contactWay.TenantID,
		)
		if link == nil ||
			link.Status != enums.StatusOk ||
			link.LinkStatus != enums.ArrivalAcquisitionLinkStatusActive ||
			link.TenantAuthorizationID != authorization.ID ||
			link.StoreID != contactWay.StoreID ||
			link.ContactMemberFingerprint != connection.ContactMemberFingerprint ||
			link.ContactMemberFingerprint != security.Fingerprint("contact_member", memberUserID) {
			return fmt.Errorf("企业微信获客链接与门店成员范围不一致")
		}
	}
	externalCiphertext, externalNonce, err := security.Encrypt("external_user_id", externalUserID)
	if err != nil {
		return fmt.Errorf("保存企业微信客户关系失败")
	}
	memberCiphertext, memberNonce, err := security.Encrypt("contact_member", memberUserID)
	if err != nil {
		return fmt.Errorf("保存企业微信客户联系成员失败")
	}
	now := time.Now()
	binding := repositories.ArrivalRepository.FindBinding(sqls.DB(), scanEvent.TenantID, scanEvent.MiniProgramIdentityID, scanEvent.StoreID)
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if binding == nil {
			binding = &models.ArrivalStoreBinding{
				TenantID:                  scanEvent.TenantID,
				StoreID:                   scanEvent.StoreID,
				MiniProgramIdentityID:     scanEvent.MiniProgramIdentityID,
				TenantAuthorizationID:     authorization.ID,
				ExternalUserIDCiphertext:  externalCiphertext,
				ExternalUserIDNonce:       externalNonce,
				ExternalUserIDFingerprint: security.Fingerprint("external_user_id", externalUserID),
				ContactMemberCiphertext:   memberCiphertext,
				ContactMemberNonce:        memberNonce,
				ContactMemberFingerprint:  security.Fingerprint("contact_member", memberUserID),
				WxWorkProtocolInstanceID:  connection.WxWorkProtocolInstanceID,
				OfficialRelationStatus:    enums.ArrivalOfficialRelationStatusConfirmed,
				BindingStatus:             enums.ArrivalBindingStatusLegacyUnmapped,
				EvidenceHash:              evidenceHash,
				OfficialRelationshipAt:    &now,
				Status:                    enums.StatusOk,
				AuditFields:               arrivalSystemAuditFields(now),
			}
			if err := repositories.ArrivalRepository.CreateBinding(ctx.Tx, binding); err != nil {
				return err
			}
		} else {
			if binding.TenantAuthorizationID != 0 && binding.TenantAuthorizationID != authorization.ID {
				return fmt.Errorf("到店身份已绑定其他企微主体")
			}
			if err := repositories.ArrivalRepository.UpdateBinding(ctx.Tx, binding.ID, binding.TenantID, map[string]any{
				"tenant_authorization_id":      authorization.ID,
				"external_user_id_ciphertext":  externalCiphertext,
				"external_user_id_nonce":       externalNonce,
				"external_user_id_fingerprint": security.Fingerprint("external_user_id", externalUserID),
				"contact_member_ciphertext":    memberCiphertext,
				"contact_member_nonce":         memberNonce,
				"contact_member_fingerprint":   security.Fingerprint("contact_member", memberUserID),
				"wx_work_protocol_instance_id": connection.WxWorkProtocolInstanceID,
				"official_relation_status":     enums.ArrivalOfficialRelationStatusConfirmed,
				"binding_status":               enums.ArrivalBindingStatusLegacyUnmapped,
				"evidence_hash":                evidenceHash,
				"official_relationship_at":     now,
				"status":                       enums.StatusOk,
				"updated_at":                   now,
				"update_user_name":             "arrival_provider",
			}); err != nil {
				return err
			}
			binding.BindingStatus = enums.ArrivalBindingStatusLegacyUnmapped
			binding.ExternalUserIDCiphertext = externalCiphertext
			binding.ExternalUserIDNonce = externalNonce
			binding.ExternalUserIDFingerprint = security.Fingerprint("external_user_id", externalUserID)
			binding.ContactMemberCiphertext = memberCiphertext
			binding.ContactMemberNonce = memberNonce
			binding.WxWorkProtocolInstanceID = connection.WxWorkProtocolInstanceID
			binding.TenantAuthorizationID = authorization.ID
			binding.OfficialRelationStatus = enums.ArrivalOfficialRelationStatusConfirmed
		}
		if err := repositories.ArrivalRepository.UpdateScanEvent(ctx.Tx, scanEvent.ID, scanEvent.TenantID, map[string]any{
			"binding_status":   enums.ArrivalBindingStatusLegacyUnmapped,
			"delivery_status":  enums.ArrivalDeliveryStatusNotBound,
			"updated_at":       now,
			"update_user_name": "arrival_provider",
		}); err != nil {
			return err
		}
		if callbackEvent != nil && callbackEvent.ID > 0 {
			return repositories.ArrivalRepository.UpdateCallbackEvent(ctx.Tx, callbackEvent.ID, map[string]any{
				"tenant_id":        scanEvent.TenantID,
				"store_id":         scanEvent.StoreID,
				"updated_at":       now,
				"update_user_name": "arrival_provider",
			})
		}
		return nil
	})
	if err != nil {
		return err
	}
	mapped, reconcileErr := s.reconcileBinding(binding.ID, binding.TenantID)
	if reconcileErr != nil {
		slog.Warn(
			"arrival official relationship confirmed but protocol mapping remains pending",
			"binding_id", binding.ID,
			"tenant_id", binding.TenantID,
			"store_id", binding.StoreID,
			"error", reconcileErr,
		)
		return nil
	}
	if !mapped {
		slog.Info("arrival relationship awaits deterministic protocol mapping", "binding_id", binding.ID, "tenant_id", binding.TenantID, "store_id", binding.StoreID)
	}
	return nil
}

func (s *weComProviderCallbackService) reconcileBinding(bindingID, tenantID int64) (bool, error) {
	bridge := ArrivalBindingBridge
	if bridge == nil || !bridge.Available() {
		return false, nil
	}
	binding := repositories.ArrivalRepository.GetBinding(sqls.DB(), bindingID, tenantID)
	if binding == nil || binding.BindingStatus == enums.ArrivalBindingStatusBound {
		return binding != nil && binding.BindingStatus == enums.ArrivalBindingStatusBound, nil
	}
	authorization := repositories.ArrivalRepository.GetTenantAuthorization(sqls.DB(), binding.TenantAuthorizationID, tenantID)
	connection := repositories.ArrivalRepository.FindConnectionByStore(sqls.DB(), tenantID, binding.StoreID)
	if authorization == nil || connection == nil ||
		binding.OfficialRelationStatus != enums.ArrivalOfficialRelationStatusConfirmed ||
		authorization.AuthorizationStatus != enums.WeComAuthorizationStatusActive ||
		connection.WxWorkProtocolInstanceID != binding.WxWorkProtocolInstanceID {
		return false, fmt.Errorf("到店绑定上下文不完整")
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return false, err
	}
	externalUserID, err := security.Decrypt("external_user_id", binding.ExternalUserIDCiphertext, binding.ExternalUserIDNonce)
	if err != nil {
		return false, fmt.Errorf("企业微信官方客户身份无法解密")
	}
	memberUserID, err := security.Decrypt("contact_member", binding.ContactMemberCiphertext, binding.ContactMemberNonce)
	if err != nil {
		return false, fmt.Errorf("企业微信客户联系成员无法解密")
	}
	corpID, err := security.Decrypt("corp_id", authorization.CorpIDCiphertext, authorization.CorpIDNonce)
	if err != nil || strings.TrimSpace(corpID) == "" {
		return false, fmt.Errorf("企业微信授权主体无法解密")
	}
	resolution, err := bridge.Resolve(ArrivalProtocolBindingRequest{
		TenantID:                     binding.TenantID,
		StoreID:                      binding.StoreID,
		TenantAuthorizationID:        binding.TenantAuthorizationID,
		WxWorkProtocolInstanceID:     binding.WxWorkProtocolInstanceID,
		CorpID:                       corpID,
		ContactMemberUserID:          memberUserID,
		ExternalUserID:               externalUserID,
		OfficialRelationshipEvidence: binding.EvidenceHash,
	})
	if err != nil {
		return false, err
	}
	if resolution == nil {
		return false, nil
	}
	if resolution.WxWorkProtocolInstanceID != binding.WxWorkProtocolInstanceID ||
		strings.TrimSpace(resolution.CorpID) != strings.TrimSpace(corpID) ||
		strings.TrimSpace(resolution.ExternalUserID) != strings.TrimSpace(externalUserID) ||
		strings.TrimSpace(resolution.ProtocolUserID) == "" ||
		strings.TrimSpace(resolution.EvidenceType) == "" ||
		strings.TrimSpace(resolution.EvidenceDigest) == "" {
		return false, fmt.Errorf("到店协议桥返回的确定性证据不完整或上下文不一致")
	}
	conversation, protocolConversationID, err := WxWorkProtocolService.EnsureArrivalConversation(
		binding.WxWorkProtocolInstanceID,
		resolution.ProtocolUserID,
		resolution.DisplayName,
		arrivalSafeEvidenceHash("arrival_binding", binding.EvidenceHash, resolution.EvidenceType, resolution.EvidenceDigest),
	)
	if err != nil {
		return false, err
	}
	route := ConversationRouteService.GetByConversationIDInTenant(conversation.ID, tenantID)
	if route == nil || route.WxWorkInstanceID != binding.WxWorkProtocolInstanceID || route.StoreID != binding.StoreID {
		return false, fmt.Errorf("到店会话路由未绑定目标门店员工实例")
	}
	protocolCiphertext, protocolNonce, err := security.Encrypt("protocol_conversation_id", protocolConversationID)
	if err != nil {
		return false, fmt.Errorf("保存企微员工号会话映射失败")
	}
	now := time.Now()
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.ArrivalRepository.UpdateBinding(ctx.Tx, binding.ID, binding.TenantID, map[string]any{
			"customer_id":                       conversation.CustomerID,
			"conversation_id":                   conversation.ID,
			"protocol_conversation_ciphertext":  protocolCiphertext,
			"protocol_conversation_nonce":       protocolNonce,
			"protocol_conversation_fingerprint": security.Fingerprint("protocol_conversation_id", protocolConversationID),
			"binding_status":                    enums.ArrivalBindingStatusBound,
			"protocol_mapped_at":                now,
			"evidence_hash":                     arrivalSafeEvidenceHash(binding.EvidenceHash, "protocol_mapped", resolution.EvidenceType, resolution.EvidenceDigest, strconv.FormatInt(conversation.ID, 10)),
			"updated_at":                        now,
			"update_user_name":                  "arrival_provider",
		}); err != nil {
			return err
		}
		scans := repositories.ArrivalRepository.FindPendingScanEvents(ctx.Tx, 500)
		for i := range scans {
			if scans[i].TenantID != binding.TenantID ||
				scans[i].StoreID != binding.StoreID ||
				scans[i].MiniProgramIdentityID != binding.MiniProgramIdentityID {
				continue
			}
			if err := repositories.ArrivalRepository.UpdateScanEvent(ctx.Tx, scans[i].ID, scans[i].TenantID, map[string]any{
				"binding_status":   enums.ArrivalBindingStatusBound,
				"updated_at":       now,
				"update_user_name": "arrival_provider",
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (s *weComProviderCallbackService) ReconcilePendingBindings(limit int) int {
	if !config.Current().Arrival.Enabled {
		return 0
	}
	if ArrivalBindingBridge == nil || !ArrivalBindingBridge.Available() {
		return 0
	}
	if limit <= 0 {
		limit = 50
	}
	events := repositories.ArrivalRepository.FindPendingScanEvents(sqls.DB(), limit)
	handled := 0
	seen := make(map[int64]struct{}, len(events))
	for i := range events {
		binding := repositories.ArrivalRepository.FindBinding(sqls.DB(), events[i].TenantID, events[i].MiniProgramIdentityID, events[i].StoreID)
		if binding == nil {
			continue
		}
		if _, ok := seen[binding.ID]; ok {
			continue
		}
		seen[binding.ID] = struct{}{}
		mapped, err := s.reconcileBinding(binding.ID, binding.TenantID)
		if err != nil {
			slog.Warn("reconcile pending arrival binding failed", "binding_id", binding.ID, "tenant_id", binding.TenantID, "store_id", binding.StoreID, "error", err)
			continue
		}
		if mapped {
			handled++
		}
	}
	return handled
}

func (s *weComProviderCallbackService) invalidateOfficialRelationship(payload *weComProviderCallbackPayload, callbackEvent *models.WeComProviderCallbackEvent) error {
	externalUserID := strings.TrimSpace(payload.ExternalUserID)
	memberUserID := strings.TrimSpace(payload.UserID)
	corpID := strings.TrimSpace(firstNonBlank(payload.AuthCorpID, payload.ToUserName))
	if externalUserID == "" || memberUserID == "" || corpID == "" {
		return fmt.Errorf("企业微信客户关系删除回调缺少确定性关联字段")
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return err
	}
	suite := repositories.ArrivalRepository.FindSuiteCredential(sqls.DB(), config.Current().Arrival.WeComSuiteID)
	if suite == nil {
		return nil
	}
	authorization := repositories.ArrivalRepository.FindTenantAuthorizationByCorpFingerprint(sqls.DB(), suite.ID, security.Fingerprint("corp_id", corpID))
	if authorization == nil {
		return nil
	}
	bindings := repositories.ArrivalRepository.FindBindingsByOfficialIdentity(
		sqls.DB(),
		authorization.TenantID,
		authorization.ID,
		security.Fingerprint("external_user_id", externalUserID),
		security.Fingerprint("contact_member", memberUserID),
	)
	now := time.Now()
	for i := range bindings {
		if err := repositories.ArrivalRepository.UpdateBinding(sqls.DB(), bindings[i].ID, bindings[i].TenantID, map[string]any{
			"official_relation_status": enums.ArrivalOfficialRelationStatusRevoked,
			"binding_status":           enums.ArrivalBindingStatusUnbound,
			"updated_at":               now,
			"update_user_name":         "arrival_provider",
		}); err != nil {
			return err
		}
	}
	return repositories.ArrivalRepository.UpdateCallbackEvent(sqls.DB(), callbackEvent.ID, map[string]any{
		"tenant_id":        authorization.TenantID,
		"updated_at":       now,
		"update_user_name": "arrival_provider",
	})
}

func (s *weComProviderCallbackService) refreshAuthorization(corpID string) error {
	security, err := newArrivalSecurity()
	if err != nil {
		return err
	}
	suite := repositories.ArrivalRepository.FindSuiteCredential(sqls.DB(), config.Current().Arrival.WeComSuiteID)
	if suite == nil {
		return fmt.Errorf("企业微信服务商凭证尚未就绪")
	}
	authorization := repositories.ArrivalRepository.FindTenantAuthorizationByCorpFingerprint(sqls.DB(), suite.ID, security.Fingerprint("corp_id", corpID))
	if authorization == nil {
		return nil
	}
	permanentCode, err := security.Decrypt("permanent_code", authorization.PermanentCodeCiphertext, authorization.PermanentCodeNonce)
	if err != nil {
		return fmt.Errorf("企业微信永久授权码无法解密")
	}
	authInfo, err := WeComProviderService.GetAuthorizationInfo(corpID, permanentCode)
	if err != nil {
		return err
	}
	return repositories.ArrivalRepository.UpdateTenantAuthorization(sqls.DB(), authorization.ID, authorization.TenantID, map[string]any{
		"authorized_scope_snapshot_json": string(authInfo),
		"authorization_status":           enums.WeComAuthorizationStatusActive,
		"updated_at":                     time.Now(),
		"update_user_name":               "arrival_provider",
	})
}

func (s *weComProviderCallbackService) revokeAuthorization(corpID string, callbackEvent *models.WeComProviderCallbackEvent) error {
	security, err := newArrivalSecurity()
	if err != nil {
		return err
	}
	suite := repositories.ArrivalRepository.FindSuiteCredential(sqls.DB(), config.Current().Arrival.WeComSuiteID)
	if suite == nil {
		return nil
	}
	authorization := repositories.ArrivalRepository.FindTenantAuthorizationByCorpFingerprint(sqls.DB(), suite.ID, security.Fingerprint("corp_id", corpID))
	if authorization == nil {
		return nil
	}
	now := time.Now()
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.ArrivalRepository.UpdateTenantAuthorization(ctx.Tx, authorization.ID, authorization.TenantID, map[string]any{
			"authorization_status":         enums.WeComAuthorizationStatusRevoked,
			"corp_access_token_ciphertext": "",
			"corp_access_token_nonce":      "",
			"corp_access_token_expires_at": nil,
			"revoked_at":                   now,
			"updated_at":                   now,
			"update_user_name":             "arrival_provider",
		}); err != nil {
			return err
		}
		connections := repositories.ArrivalRepository.FindConnectionsByAuthorization(ctx.Tx, authorization.ID)
		for i := range connections {
			if err := repositories.ArrivalRepository.UpdateConnection(ctx.Tx, connections[i].ID, connections[i].TenantID, map[string]any{
				"connection_status":            enums.ArrivalConnectionStatusInvalid,
				"last_verification_error_code": "authorization_revoked",
				"updated_at":                   now,
				"update_user_name":             "arrival_provider",
			}); err != nil {
				return err
			}
		}
		contactWays := repositories.ArrivalRepository.FindContactWays(ctx.Tx, sqls.NewCnd().
			Eq("tenant_id", authorization.TenantID).
			Eq("tenant_authorization_id", authorization.ID).
			In("contact_way_status", []enums.ArrivalContactWayStatus{
				enums.ArrivalContactWayStatusProvisioning,
				enums.ArrivalContactWayStatusActive,
				enums.ArrivalContactWayStatusFailed,
				enums.ArrivalContactWayStatusExpired,
			}))
		for i := range contactWays {
			if err := repositories.ArrivalRepository.UpdateContactWay(ctx.Tx, contactWays[i].ID, contactWays[i].TenantID, map[string]any{
				"contact_way_status": enums.ArrivalContactWayStatusExpired,
				"expires_at":         now,
				"failure_code":       "authorization_revoked",
				"updated_at":         now,
				"update_user_name":   "arrival_provider",
			}); err != nil {
				return err
			}
		}
		return repositories.ArrivalRepository.UpdateCallbackEvent(ctx.Tx, callbackEvent.ID, map[string]any{
			"tenant_id":        authorization.TenantID,
			"updated_at":       now,
			"update_user_name": "arrival_provider",
		})
	})
}

func validateWeComProviderPayload(kind, receiveID string, payload *weComProviderCallbackPayload) error {
	if payload == nil {
		return fmt.Errorf("企业微信回调消息为空")
	}
	cfg := config.Current().Arrival
	if strings.TrimSpace(payload.SuiteID) != "" && strings.TrimSpace(payload.SuiteID) != strings.TrimSpace(cfg.WeComSuiteID) {
		return fmt.Errorf("企业微信回调 SuiteID 不匹配")
	}
	if strings.TrimSpace(kind) == "command" {
		if strings.TrimSpace(receiveID) != strings.TrimSpace(cfg.WeComSuiteID) {
			return fmt.Errorf("企业微信指令回调接收方不匹配")
		}
		return nil
	}
	corpID := strings.TrimSpace(firstNonBlank(payload.AuthCorpID, payload.ToUserName))
	if corpID == "" || strings.TrimSpace(receiveID) != corpID {
		return fmt.Errorf("企业微信数据回调接收方不匹配")
	}
	return nil
}

func validateWeComCallbackTimestamp(value string, now time.Time) error {
	unixTime, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || unixTime <= 0 {
		return fmt.Errorf("企业微信回调时间戳无效")
	}
	delta := now.Sub(time.Unix(unixTime, 0))
	if delta < 0 {
		delta = -delta
	}
	if delta > weComCallbackReplayWindow {
		return fmt.Errorf("企业微信回调已超过防重放时间窗")
	}
	return nil
}

func callbackOccurredAt(payload *weComProviderCallbackPayload, fallbackTimestamp string) time.Time {
	if payload != nil {
		if payload.CreateTime > 0 {
			return time.Unix(payload.CreateTime, 0)
		}
		if payload.TimeStamp > 0 {
			return time.Unix(payload.TimeStamp, 0)
		}
	}
	if unixTime, err := strconv.ParseInt(strings.TrimSpace(fallbackTimestamp), 10, 64); err == nil && unixTime > 0 {
		return time.Unix(unixTime, 0)
	}
	return time.Now()
}
