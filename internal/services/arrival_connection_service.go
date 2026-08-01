package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	arrivalAuthorizationTTL = 2 * time.Hour
)

var ArrivalConnectionService = &arrivalConnectionService{}

type arrivalConnectionService struct {
	providerUpdateMu sync.Mutex
}

func (s *arrivalConnectionService) ListConnections(cnd *sqls.Cnd, operator *dto.AuthPrincipal) ([]response.ArrivalConnectionResponse, *sqls.Paging, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, nil, errorsx.Forbidden("请先进入需要管理的接入公司")
	}
	if cnd == nil {
		cnd = sqls.NewCnd().Desc("id")
	}
	stores, paging := repositories.StoreRepository.FindPageByCnd(
		sqls.DB(),
		cnd.Eq("tenant_id", tenantID).Where("status <> ?", enums.StatusDeleted),
	)
	results := make([]response.ArrivalConnectionResponse, 0, len(stores))
	for i := range stores {
		results = append(results, s.buildConnectionResponse(&stores[i], repositories.ArrivalRepository.FindConnectionByStore(sqls.DB(), tenantID, stores[i].ID)))
	}
	return results, paging, nil
}

func (s *arrivalConnectionService) ListAuthorizations(operator *dto.AuthPrincipal) ([]response.ArrivalAuthorizationOptionResponse, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理的接入公司")
	}
	items := repositories.ArrivalRepository.FindTenantAuthorizations(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Where("authorization_status <> ?", enums.WeComAuthorizationStatusRevoked).
		Desc("id"))
	results := make([]response.ArrivalAuthorizationOptionResponse, 0, len(items))
	for i := range items {
		results = append(results, response.ArrivalAuthorizationOptionResponse{
			ID:       items[i].ID,
			CorpName: strings.TrimSpace(items[i].CorpName),
			Status:   string(items[i].AuthorizationStatus),
		})
	}
	return results, nil
}

func (s *arrivalConnectionService) ListProtocolInstances(
	storeID int64,
	operator *dto.AuthPrincipal,
) ([]response.ArrivalProtocolInstanceOptionResponse, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理的接入公司")
	}
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), storeID, tenantID)
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在")
	}
	items := repositories.WxWorkProtocolInstanceRepository.FindActivatedCurrent(
		sqls.DB(),
		sqls.NewCnd().
			Eq("tenant_id", tenantID).
			Eq("store_id", storeID).
			Asc("id"),
	)
	results := make([]response.ArrivalProtocolInstanceOptionResponse, 0, len(items))
	for i := range items {
		results = append(results, response.ArrivalProtocolInstanceOptionResponse{
			ID:                    items[i].ID,
			Name:                  firstNonBlank(strings.TrimSpace(items[i].EmployeeName), "企微员工号"),
			HealthStatus:          strings.TrimSpace(items[i].HealthStatus),
			StoreID:               items[i].StoreID,
			StoreStaffBindingID:   items[i].StoreStaffBindingID,
			StoreStaffAccountName: arrivalStoreStaffAccountNameDB(sqls.DB(), tenantID, items[i].StoreStaffBindingID),
		})
	}
	return results, nil
}

func (s *arrivalConnectionService) UpdateProvider(
	req request.UpdateArrivalConnectionProviderRequest,
	operator *dto.AuthPrincipal,
) (*response.ArrivalConnectionResponse, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), req.StoreID, tenantID)
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在")
	}
	mode, err := parseArrivalProviderMode(req.ContactProvider)
	if err != nil {
		return nil, err
	}
	var selectedScope *arrivalStoreStaffScope
	var instance *models.WxWorkProtocolInstance
	if req.WxWorkProtocolInstanceID > 0 {
		selectedScope, err = resolveArrivalSelectedInstanceDB(sqls.DB(), tenantID, store.ID, req.WxWorkProtocolInstanceID, false)
		if err != nil {
			return nil, err
		}
		instance = selectedScope.Instance
	}
	plugID := strings.TrimSpace(req.StaticContactPlugID)
	if mode == enums.ArrivalContactProviderModeStaticPluginTicket {
		if plugID == "" || len(plugID) > 191 || strings.ContainsAny(plugID, " \t\r\n") {
			return nil, errorsx.InvalidParam("请填写企业微信后台生成的真实 plugId")
		}
		if instance == nil || instance.Status != enums.StatusOk {
			return nil, errorsx.InvalidParam("请选择当前门店可用的企微员工号实例")
		}
		if _, _, err := buildStoredArrivalBindingCardPayload(instance, 1); err != nil {
			return nil, errorsx.InvalidParam(err.Error())
		}
	} else {
		plugID = ""
	}
	s.providerUpdateMu.Lock()
	defer s.providerUpdateMu.Unlock()

	now := time.Now()
	var connection *models.StoreArrivalConnection
	var revokedTicketCount int64
	var validationErr error
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedStore, lockErr := repositories.StoreRepository.GetForUpdateInTenant(ctx.Tx, store.ID, tenantID)
		if lockErr != nil {
			return lockErr
		}
		if lockedStore == nil || lockedStore.Status == enums.StatusDeleted {
			validationErr = errorsx.InvalidParam("门店不存在")
			return validationErr
		}
		store = lockedStore

		if req.WxWorkProtocolInstanceID > 0 {
			selectedScope, err = resolveArrivalSelectedInstanceDB(ctx.Tx, tenantID, store.ID, req.WxWorkProtocolInstanceID, true)
			if err != nil {
				validationErr = err
				return validationErr
			}
			instance = selectedScope.Instance
		}
		if mode == enums.ArrivalContactProviderModeStaticPluginTicket {
			if instance == nil || instance.Status != enums.StatusOk {
				validationErr = errorsx.InvalidParam("请选择当前门店可用的企微员工号实例")
				return validationErr
			}
			if _, _, cardErr := buildStoredArrivalBindingCardPayload(instance, 1); cardErr != nil {
				validationErr = errorsx.InvalidParam(cardErr.Error())
				return validationErr
			}
			candidates, findErr := repositories.ArrivalRepository.FindActiveStaticConnectionsByBindingForUpdate(
				ctx.Tx,
				tenantID,
				selectedScope.Binding.ID,
			)
			if findErr != nil {
				return findErr
			}
			for i := range candidates {
				if candidates[i].StoreID != store.ID {
					validationErr = errorsx.InvalidParam("该企微员工号实例已映射其他静态到店门店")
					return validationErr
				}
			}
		}

		connection = repositories.ArrivalRepository.FindConnectionByStore(ctx.Tx, tenantID, store.ID)
		if connection == nil {
			scene, sceneErr := s.generateStoreScene()
			if sceneErr != nil {
				return sceneErr
			}
			connection = &models.StoreArrivalConnection{
				TenantID:            tenantID,
				StoreID:             store.ID,
				StoreScene:          scene,
				ContactProviderMode: mode,
				ConnectionStatus:    enums.ArrivalConnectionStatusPendingAuthorization,
				Status:              enums.StatusOk,
				AuditFields: models.AuditFields{
					CreatedAt:      now,
					CreateUserID:   operator.UserID,
					CreateUserName: operator.Username,
					UpdatedAt:      now,
					UpdateUserID:   operator.UserID,
					UpdateUserName: operator.Username,
				},
			}
			if err := repositories.ArrivalRepository.CreateConnection(ctx.Tx, connection); err != nil {
				return err
			}
		}
		revokedTicketCount, err = repositories.ArrivalRepository.RevokePendingBindingTicketsByStore(
			ctx.Tx,
			tenantID,
			store.ID,
			now,
		)
		if err != nil {
			return err
		}
		connectionStatus := enums.ArrivalConnectionStatusPendingAuthorization
		if mode == enums.ArrivalContactProviderModeStaticPluginTicket {
			connectionStatus = enums.ArrivalConnectionStatusActive
		} else if connection.TenantAuthorizationID > 0 {
			connectionStatus = enums.ArrivalConnectionStatusPendingBinding
			effectiveBindingID := connection.StoreStaffBindingID
			if selectedScope != nil {
				effectiveBindingID = selectedScope.Binding.ID
			}
			if strings.TrimSpace(connection.ContactMemberFingerprint) != "" && effectiveBindingID > 0 {
				connectionStatus = enums.ArrivalConnectionStatusActive
			}
		}
		updates := map[string]any{
			"contact_provider_mode":        mode,
			"static_contact_plug_id":       plugID,
			"connection_status":            connectionStatus,
			"last_verification_error_code": "",
			"status":                       enums.StatusOk,
			"updated_at":                   now,
			"update_user_id":               operator.UserID,
			"update_user_name":             operator.Username,
		}
		if instance != nil {
			updates["store_staff_binding_id"] = selectedScope.Binding.ID
			updates["wx_work_protocol_instance_id"] = instance.ID
		}
		if err := repositories.ArrivalRepository.UpdateConnection(
			ctx.Tx,
			connection.ID,
			tenantID,
			updates,
		); err != nil {
			return err
		}
		return s.createAuditLog(
			ctx.Tx,
			tenantID,
			store.ID,
			"connection.provider_update",
			"StoreArrivalConnection",
			connection.ID,
			"success",
			operator,
			map[string]any{
				"providerMode":       mode,
				"instanceSet":        instance != nil,
				"plugIdSet":          plugID != "",
				"revokedTicketCount": revokedTicketCount,
			},
		)
	})
	if err != nil {
		if validationErr != nil {
			return nil, validationErr
		}
		return nil, errorsx.BusinessError(71, "保存门店到店模式失败")
	}
	connection = repositories.ArrivalRepository.FindConnectionByStore(sqls.DB(), tenantID, store.ID)
	result := s.buildConnectionResponse(store, connection)
	return &result, nil
}

func (s *arrivalConnectionService) CreateInvitation(req request.CreateArrivalInvitationRequest, operator *dto.AuthPrincipal) (*response.ArrivalInvitationResponse, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	if err := ArrivalLinkService.ValidateConfiguration(); err != nil {
		return nil, err
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), req.StoreID, tenantID)
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在")
	}
	if connection := repositories.ArrivalRepository.FindConnectionByStore(sqls.DB(), tenantID, store.ID); connection != nil &&
		arrivalProviderModeForConnection(connection) == enums.ArrivalContactProviderModeStaticPluginTicket {
		return nil, errorsx.InvalidParam("静态联系我模式无需创建企微服务商授权邀请")
	}
	var authorization *models.WeComTenantAuthorization
	if req.TenantAuthorizationID > 0 {
		authorization = repositories.ArrivalRepository.GetTenantAuthorization(sqls.DB(), req.TenantAuthorizationID, tenantID)
		if authorization == nil || authorization.AuthorizationStatus != enums.WeComAuthorizationStatusActive {
			return nil, errorsx.InvalidParam("所选企微主体授权不可用")
		}
	}
	token, err := randomArrivalToken(32)
	if err != nil {
		return nil, errorsx.BusinessError(67, "生成门店邀请失败")
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, errorsx.BusinessError(61, err.Error())
	}
	now := time.Now()
	expiresAt := now.Add(time.Duration(config.Current().Arrival.InvitationTTL()) * time.Minute)
	var invitation *models.StoreArrivalInvitation
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		connection := repositories.ArrivalRepository.FindConnectionByStore(ctx.Tx, tenantID, store.ID)
		if connection == nil {
			scene, sceneErr := s.generateStoreScene()
			if sceneErr != nil {
				return sceneErr
			}
			connection = &models.StoreArrivalConnection{
				TenantID:         tenantID,
				StoreID:          store.ID,
				StoreScene:       scene,
				ConnectionStatus: enums.ArrivalConnectionStatusPendingAuthorization,
				Status:           enums.StatusOk,
				AuditFields: models.AuditFields{
					CreatedAt:      now,
					CreateUserID:   operator.UserID,
					CreateUserName: operator.Username,
					UpdatedAt:      now,
					UpdateUserID:   operator.UserID,
					UpdateUserName: operator.Username,
				},
			}
			if err := repositories.ArrivalRepository.CreateConnection(ctx.Tx, connection); err != nil {
				return err
			}
		}
		connectionUpdates := map[string]any{
			"status":           enums.StatusOk,
			"updated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		}
		if authorization != nil {
			connectionUpdates["tenant_authorization_id"] = authorization.ID
			connectionUpdates["connection_status"] = enums.ArrivalConnectionStatusPendingBinding
		} else if connection.TenantAuthorizationID <= 0 {
			connectionUpdates["connection_status"] = enums.ArrivalConnectionStatusPendingAuthorization
		}
		if err := repositories.ArrivalRepository.UpdateConnection(ctx.Tx, connection.ID, tenantID, connectionUpdates); err != nil {
			return err
		}
		if previous := repositories.ArrivalRepository.FindActiveInvitationByStore(ctx.Tx, tenantID, store.ID); previous != nil {
			if err := repositories.ArrivalRepository.UpdateInvitation(ctx.Tx, previous.ID, tenantID, map[string]any{
				"status":           enums.StatusDeleted,
				"updated_at":       now,
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
			}); err != nil {
				return err
			}
		}
		invitation = &models.StoreArrivalInvitation{
			TenantID:  tenantID,
			StoreID:   store.ID,
			TokenHash: security.Fingerprint("arrival_invitation", token),
			ExpiresAt: expiresAt,
			Status:    enums.StatusOk,
			AuditFields: models.AuditFields{
				CreatedAt:      now,
				CreateUserID:   operator.UserID,
				CreateUserName: operator.Username,
				UpdatedAt:      now,
				UpdateUserID:   operator.UserID,
				UpdateUserName: operator.Username,
			},
		}
		if err := repositories.ArrivalRepository.CreateInvitation(ctx.Tx, invitation); err != nil {
			return err
		}
		return s.createAuditLog(ctx.Tx, tenantID, store.ID, "invitation.create", "StoreArrivalInvitation", invitation.ID, "success", operator, map[string]any{
			"reusedAuthorization": authorization != nil,
		})
	})
	if err != nil {
		return nil, errorsx.BusinessError(67, "创建门店邀请失败")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(config.Current().Arrival.PublicBaseURL), "/")
	return &response.ArrivalInvitationResponse{
		InvitationURL: baseURL + "/wecom/provider/settings?invite=" + token,
		ExpiresAt:     expiresAt,
	}, nil
}

func (s *arrivalConnectionService) ValidateInvitation(rawToken string) (*response.ArrivalProviderInvitationResponse, error) {
	invitation, connection, store, err := s.requireInvitation(rawToken)
	if err != nil {
		return nil, err
	}
	authorized := false
	if connection.TenantAuthorizationID > 0 {
		authorization := repositories.ArrivalRepository.GetTenantAuthorization(sqls.DB(), connection.TenantAuthorizationID, connection.TenantID)
		authorized = authorization != nil && authorization.AuthorizationStatus == enums.WeComAuthorizationStatusActive
	}
	return &response.ArrivalProviderInvitationResponse{
		Valid:            true,
		StoreName:        strings.TrimSpace(store.Name),
		BrandName:        strings.TrimSpace(store.BrandName),
		ConnectionStatus: string(connection.ConnectionStatus),
		Authorized:       authorized,
		ExpiresAt:        invitation.ExpiresAt,
	}, nil
}

func (s *arrivalConnectionService) BeginAuthorization(rawInvitationToken string) (*response.ArrivalAuthorizationBeginResponse, error) {
	invitation, connection, _, err := s.requireInvitation(rawInvitationToken)
	if err != nil {
		return nil, err
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, errorsx.BusinessError(61, err.Error())
	}
	state, err := randomWeComAuthorizationState()
	if err != nil {
		return nil, errorsx.BusinessError(68, "创建企微授权会话失败")
	}
	now := time.Now()
	expiresAt := now.Add(arrivalAuthorizationTTL)
	attempt := &models.WeComAuthorizationAttempt{
		TenantID:     invitation.TenantID,
		StoreID:      invitation.StoreID,
		InvitationID: invitation.ID,
		StateHash:    security.Fingerprint("authorization_state", state),
		ExpiresAt:    expiresAt,
		Status:       enums.StatusOk,
		AuditFields:  arrivalSystemAuditFields(now),
	}
	alreadyAuthorized := false
	if connection.TenantAuthorizationID > 0 {
		authorization := repositories.ArrivalRepository.GetTenantAuthorization(sqls.DB(), connection.TenantAuthorizationID, connection.TenantID)
		if authorization != nil && authorization.AuthorizationStatus == enums.WeComAuthorizationStatusActive {
			attempt.TenantAuthorizationID = authorization.ID
			attempt.CompletedAt = &now
			alreadyAuthorized = true
		}
	}
	authorizationURL := ""
	if !alreadyAuthorized {
		var preAuthCode string
		var beginErr error
		authorizationURL, preAuthCode, beginErr = WeComProviderService.BeginAuthorization(state)
		if beginErr != nil {
			return nil, errorsx.BusinessError(68, beginErr.Error())
		}
		attempt.PreAuthCodeHash = security.Fingerprint("pre_auth_code", preAuthCode)
	}
	if err := repositories.ArrivalRepository.CreateAuthorizationAttempt(sqls.DB(), attempt); err != nil {
		return nil, errorsx.BusinessError(68, "保存企微授权会话失败")
	}
	return &response.ArrivalAuthorizationBeginResponse{
		AuthorizationURL:   authorizationURL,
		AuthorizationState: state,
		AlreadyAuthorized:  alreadyAuthorized,
	}, nil
}

func (s *arrivalConnectionService) CompleteAuthorization(rawState, authCode string) (*models.WeComAuthorizationAttempt, error) {
	attempt, err := s.requireAuthorizationAttempt(rawState, false)
	if err != nil {
		return nil, err
	}
	if attempt.CompletedAt != nil && attempt.TenantAuthorizationID > 0 {
		return attempt, nil
	}
	authCode = strings.TrimSpace(authCode)
	if authCode == "" {
		return nil, errorsx.InvalidParam("企业微信未返回授权码")
	}
	result, err := WeComProviderService.ExchangePermanentAuthorization(authCode)
	if err != nil {
		return nil, errorsx.BusinessError(69, err.Error())
	}
	authInfo, err := WeComProviderService.GetAuthorizationInfo(result.AuthCorpInfo.CorpID, result.PermanentCode)
	if err != nil {
		return nil, errorsx.BusinessError(69, err.Error())
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, errorsx.BusinessError(61, err.Error())
	}
	suite := repositories.ArrivalRepository.FindSuiteCredential(sqls.DB(), config.Current().Arrival.WeComSuiteID)
	if suite == nil {
		return nil, errorsx.BusinessError(69, "企业微信服务商凭证尚未就绪")
	}
	corpFingerprint := security.Fingerprint("corp_id", result.AuthCorpInfo.CorpID)
	existing := repositories.ArrivalRepository.FindTenantAuthorizationByCorpFingerprint(sqls.DB(), suite.ID, corpFingerprint)
	if existing != nil && existing.TenantID != attempt.TenantID {
		return nil, errorsx.BusinessError(69, "该企微主体已归属其他接入公司")
	}
	corpCiphertext, corpNonce, err := security.Encrypt("corp_id", result.AuthCorpInfo.CorpID)
	if err != nil {
		return nil, errorsx.BusinessError(69, "保存企微主体身份失败")
	}
	permanentCiphertext, permanentNonce, err := security.Encrypt("permanent_code", result.PermanentCode)
	if err != nil {
		return nil, errorsx.BusinessError(69, "保存企微授权凭证失败")
	}
	now := time.Now()
	var authorizationID int64
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if existing == nil {
			item := &models.WeComTenantAuthorization{
				TenantID:                    attempt.TenantID,
				SuiteCredentialID:           suite.ID,
				CorpIDCiphertext:            corpCiphertext,
				CorpIDNonce:                 corpNonce,
				CorpIDFingerprint:           corpFingerprint,
				CorpName:                    strings.TrimSpace(result.AuthCorpInfo.CorpName),
				PermanentCodeCiphertext:     permanentCiphertext,
				PermanentCodeNonce:          permanentNonce,
				AuthorizedScopeSnapshotJSON: string(authInfo),
				AuthorizationStatus:         enums.WeComAuthorizationStatusActive,
				AuthorizedAt:                &now,
				AuditFields:                 arrivalSystemAuditFields(now),
			}
			if err := repositories.ArrivalRepository.CreateTenantAuthorization(ctx.Tx, item); err != nil {
				return err
			}
			authorizationID = item.ID
		} else {
			authorizationID = existing.ID
			if err := repositories.ArrivalRepository.UpdateTenantAuthorization(ctx.Tx, existing.ID, existing.TenantID, map[string]any{
				"corp_name":                      strings.TrimSpace(result.AuthCorpInfo.CorpName),
				"permanent_code_ciphertext":      permanentCiphertext,
				"permanent_code_nonce":           permanentNonce,
				"corp_access_token_ciphertext":   "",
				"corp_access_token_nonce":        "",
				"corp_access_token_expires_at":   nil,
				"authorized_scope_snapshot_json": string(authInfo),
				"authorization_status":           enums.WeComAuthorizationStatusActive,
				"authorized_at":                  now,
				"revoked_at":                     nil,
				"updated_at":                     now,
				"update_user_name":               "arrival_provider",
			}); err != nil {
				return err
			}
		}
		if err := repositories.ArrivalRepository.UpdateAuthorizationAttempt(ctx.Tx, attempt.ID, attempt.TenantID, map[string]any{
			"tenant_authorization_id": authorizationID,
			"completed_at":            now,
			"updated_at":              now,
			"update_user_name":        "arrival_provider",
		}); err != nil {
			return err
		}
		connection := repositories.ArrivalRepository.FindConnectionByStore(ctx.Tx, attempt.TenantID, attempt.StoreID)
		if connection == nil {
			return fmt.Errorf("门店到店连接不存在")
		}
		if err := repositories.ArrivalRepository.UpdateConnection(ctx.Tx, connection.ID, connection.TenantID, map[string]any{
			"tenant_authorization_id": authorizationID,
			"connection_status":       enums.ArrivalConnectionStatusPendingBinding,
			"updated_at":              now,
			"update_user_name":        "arrival_provider",
		}); err != nil {
			return err
		}
		return s.createAuditLog(ctx.Tx, attempt.TenantID, attempt.StoreID, "authorization.complete", "WeComTenantAuthorization", authorizationID, "success", nil, map[string]any{
			"scopeVerified": len(authInfo) > 0,
		})
	})
	if err != nil {
		return nil, errorsx.BusinessError(69, "保存企微授权结果失败")
	}
	attempt.TenantAuthorizationID = authorizationID
	attempt.CompletedAt = &now
	return attempt, nil
}

func (s *arrivalConnectionService) ProviderOptions(rawState string) (*response.ArrivalProviderOptionsResponse, error) {
	attempt, err := s.requireAuthorizationAttempt(rawState, true)
	if err != nil {
		return nil, err
	}
	authorization := repositories.ArrivalRepository.GetTenantAuthorization(sqls.DB(), attempt.TenantAuthorizationID, attempt.TenantID)
	if authorization == nil || authorization.AuthorizationStatus != enums.WeComAuthorizationStatusActive {
		return nil, errorsx.BusinessError(70, "企微主体授权不可用")
	}
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), attempt.StoreID, attempt.TenantID)
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在")
	}
	memberIDs, err := WeComProviderService.ListContactMembers(authorization)
	if err != nil {
		return nil, errorsx.BusinessError(70, err.Error())
	}
	instances := repositories.WxWorkProtocolInstanceRepository.FindActivatedCurrent(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", attempt.TenantID).
		Eq("store_id", attempt.StoreID).
		Asc("id"))
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, errorsx.BusinessError(61, err.Error())
	}
	instanceResponses := make([]response.ArrivalProviderInstanceOptionResponse, 0, len(instances))
	for i := range instances {
		instanceResponses = append(instanceResponses, response.ArrivalProviderInstanceOptionResponse{
			ID:                    instances[i].ID,
			Name:                  firstNonBlank(instances[i].EmployeeName, "企微员工号"),
			HealthStatus:          strings.TrimSpace(instances[i].HealthStatus),
			BoundStoreID:          instances[i].StoreID,
			StoreStaffBindingID:   instances[i].StoreStaffBindingID,
			StoreStaffAccountName: arrivalStoreStaffAccountNameDB(sqls.DB(), attempt.TenantID, instances[i].StoreStaffBindingID),
		})
	}
	memberResponses := make([]response.ArrivalProviderOptionResponse, 0, len(memberIDs))
	for i, memberID := range memberIDs {
		token, tokenErr := security.SelectionToken("contact_member", attempt.ID, memberID, attempt.ExpiresAt)
		if tokenErr != nil {
			return nil, errorsx.BusinessError(70, "生成成员选择凭证失败")
		}
		memberResponses = append(memberResponses, response.ArrivalProviderOptionResponse{
			Value: token,
			Label: fmt.Sprintf("可用客户联系成员 %d", i+1),
		})
	}
	connection := repositories.ArrivalRepository.FindConnectionByStore(sqls.DB(), attempt.TenantID, attempt.StoreID)
	status := string(enums.ArrivalConnectionStatusPendingBinding)
	if connection != nil {
		status = string(connection.ConnectionStatus)
	}
	return &response.ArrivalProviderOptionsResponse{
		StoreName:        strings.TrimSpace(store.Name),
		ConnectionStatus: status,
		Members:          memberResponses,
		Instances:        instanceResponses,
	}, nil
}

func (s *arrivalConnectionService) CompleteConnection(req request.CompleteArrivalConnectionRequest) (*response.ArrivalConnectionVerificationResponse, error) {
	attempt, err := s.requireAuthorizationAttempt(req.AuthorizationState, true)
	if err != nil {
		return nil, err
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, errorsx.BusinessError(61, err.Error())
	}
	memberUserID, err := security.ParseSelectionToken("contact_member", req.ContactMemberToken, attempt.ID, time.Now())
	if err != nil {
		return nil, errorsx.InvalidParam(err.Error())
	}
	authorization := repositories.ArrivalRepository.GetTenantAuthorization(sqls.DB(), attempt.TenantAuthorizationID, attempt.TenantID)
	if authorization == nil || authorization.AuthorizationStatus != enums.WeComAuthorizationStatusActive {
		return nil, errorsx.BusinessError(70, "企微主体授权不可用")
	}
	eligibleMembers, err := WeComProviderService.ListContactMembers(authorization)
	if err != nil || !containsTrimmed(eligibleMembers, memberUserID) {
		return nil, errorsx.InvalidParam("所选客户联系成员已不可用")
	}
	selectedScope, err := resolveArrivalSelectedInstanceDB(
		sqls.DB(),
		attempt.TenantID,
		attempt.StoreID,
		req.WxWorkProtocolInstanceID,
		false,
	)
	if err != nil {
		return nil, err
	}
	instance := selectedScope.Instance
	memberCiphertext, memberNonce, err := security.Encrypt("contact_member", memberUserID)
	if err != nil {
		return nil, errorsx.BusinessError(70, "保存客户联系成员失败")
	}
	connection := repositories.ArrivalRepository.FindConnectionByStore(sqls.DB(), attempt.TenantID, attempt.StoreID)
	if connection == nil {
		return nil, errorsx.InvalidParam("门店到店连接不存在")
	}
	now := time.Now()
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedScope, lockErr := resolveArrivalSelectedInstanceDB(
			ctx.Tx,
			attempt.TenantID,
			attempt.StoreID,
			req.WxWorkProtocolInstanceID,
			true,
		)
		if lockErr != nil {
			return lockErr
		}
		instance = lockedScope.Instance
		if err := repositories.ArrivalRepository.UpdateConnection(ctx.Tx, connection.ID, connection.TenantID, map[string]any{
			"tenant_authorization_id":      authorization.ID,
			"contact_member_ciphertext":    memberCiphertext,
			"contact_member_nonce":         memberNonce,
			"contact_member_fingerprint":   security.Fingerprint("contact_member", memberUserID),
			"store_staff_binding_id":       lockedScope.Binding.ID,
			"wx_work_protocol_instance_id": instance.ID,
			"connection_status":            enums.ArrivalConnectionStatusActive,
			"last_verified_at":             now,
			"last_verification_error_code": "",
			"status":                       enums.StatusOk,
			"updated_at":                   now,
			"update_user_name":             "arrival_provider",
		}); err != nil {
			return err
		}
		if err := repositories.ArrivalRepository.UpdateInvitation(ctx.Tx, attempt.InvitationID, attempt.TenantID, map[string]any{
			"used_at":          now,
			"status":           enums.StatusDisabled,
			"updated_at":       now,
			"update_user_name": "arrival_provider",
		}); err != nil {
			return err
		}
		if err := repositories.ArrivalRepository.UpdateAuthorizationAttempt(ctx.Tx, attempt.ID, attempt.TenantID, map[string]any{
			"status":           enums.StatusDisabled,
			"updated_at":       now,
			"update_user_name": "arrival_provider",
		}); err != nil {
			return err
		}
		return s.createAuditLog(ctx.Tx, attempt.TenantID, attempt.StoreID, "connection.complete", "StoreArrivalConnection", connection.ID, "success", nil, map[string]any{
			"instanceOnline": strings.EqualFold(strings.TrimSpace(instance.HealthStatus), "online"),
			"mappingMode":    "operator_confirmed_cross_namespace",
		})
	})
	if err != nil {
		return nil, errorsx.BusinessError(70, "完成门店连接失败")
	}
	return s.verifyConnection(connection.ID, attempt.TenantID, nil)
}

func (s *arrivalConnectionService) VerifyConnection(connectionID int64, operator *dto.AuthPrincipal) (*response.ArrivalConnectionVerificationResponse, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	return s.verifyConnection(connectionID, tenantID, operator)
}

func (s *arrivalConnectionService) DisableConnection(req request.DisableArrivalConnectionRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	connection := repositories.ArrivalRepository.GetConnection(sqls.DB(), req.ConnectionID, tenantID)
	if connection == nil {
		return errorsx.InvalidParam("门店到店连接不存在")
	}
	now := time.Now()
	contactWays := repositories.ArrivalRepository.FindContactWays(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("store_id", connection.StoreID).
		In("contact_way_status", []enums.ArrivalContactWayStatus{
			enums.ArrivalContactWayStatusActive,
			enums.ArrivalContactWayStatusExpired,
			enums.ArrivalContactWayStatusFailed,
		}))
	var revokedTicketCount int64
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.ArrivalRepository.UpdateConnection(ctx.Tx, connection.ID, tenantID, map[string]any{
			"connection_status":            enums.ArrivalConnectionStatusDisabled,
			"status":                       enums.StatusDisabled,
			"last_verification_error_code": "disabled_by_operator",
			"updated_at":                   now,
			"update_user_id":               operator.UserID,
			"update_user_name":             operator.Username,
		}); err != nil {
			return err
		}
		var err error
		revokedTicketCount, err = repositories.ArrivalRepository.RevokePendingBindingTicketsByStore(
			ctx.Tx,
			tenantID,
			connection.StoreID,
			now,
		)
		if err != nil {
			return err
		}
		return s.createAuditLog(
			ctx.Tx,
			tenantID,
			connection.StoreID,
			"connection.disable",
			"StoreArrivalConnection",
			connection.ID,
			"success",
			operator,
			map[string]any{
				"reasonProvided":        strings.TrimSpace(req.Reason) != "",
				"cleanupRequestedCount": len(contactWays),
				"revokedTicketCount":    revokedTicketCount,
			},
		)
	}); err != nil {
		return errorsx.BusinessError(71, "停用门店到店连接失败")
	}
	cleanedCount := 0
	for i := range contactWays {
		if ArrivalMaintenanceService.cleanupContactWay(&contactWays[i], now) {
			cleanedCount++
		}
	}
	if cleanedCount < len(contactWays) {
		_ = s.createAuditLog(
			sqls.DB(),
			tenantID,
			connection.StoreID,
			"connection.disable_cleanup",
			"StoreArrivalConnection",
			connection.ID,
			"partial",
			operator,
			map[string]any{
				"cleanupRequestedCount": len(contactWays),
				"cleanedCount":          cleanedCount,
			},
		)
	}
	return nil
}

func (s *arrivalConnectionService) AuditLogs(cnd *sqls.Cnd, operator *dto.AuthPrincipal) ([]response.ArrivalAuditLogResponse, *sqls.Paging, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, nil, errorsx.Forbidden("请先进入需要管理的接入公司")
	}
	if cnd == nil {
		cnd = sqls.NewCnd().Desc("id")
	}
	items, paging := repositories.ArrivalRepository.FindAuditLogs(sqls.DB(), cnd.Eq("tenant_id", tenantID))
	results := make([]response.ArrivalAuditLogResponse, 0, len(items))
	for i := range items {
		results = append(results, response.ArrivalAuditLogResponse{
			ID:           items[i].ID,
			StoreID:      items[i].StoreID,
			Action:       items[i].Action,
			EntityType:   items[i].EntityType,
			EntityID:     items[i].EntityID,
			Result:       items[i].Result,
			DetailJSON:   items[i].DetailJSON,
			OperatorName: items[i].OperatorName,
			CreatedAt:    items[i].CreatedAt,
		})
	}
	return results, paging, nil
}

func (s *arrivalConnectionService) requireInvitation(rawToken string) (*models.StoreArrivalInvitation, *models.StoreArrivalConnection, *models.Store, error) {
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, nil, nil, errorsx.BusinessError(61, err.Error())
	}
	tokenHash := security.Fingerprint("arrival_invitation", strings.TrimSpace(rawToken))
	invitation := repositories.ArrivalRepository.FindInvitationByHash(sqls.DB(), tokenHash)
	if invitation == nil || invitation.Status != enums.StatusOk || invitation.UsedAt != nil || !invitation.ExpiresAt.After(time.Now()) {
		return nil, nil, nil, errorsx.InvalidToken("门店邀请不存在、已使用或已过期")
	}
	connection := repositories.ArrivalRepository.FindConnectionByStore(sqls.DB(), invitation.TenantID, invitation.StoreID)
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), invitation.StoreID, invitation.TenantID)
	if connection == nil || store == nil || store.Status == enums.StatusDeleted {
		return nil, nil, nil, errorsx.InvalidToken("门店邀请上下文不存在")
	}
	return invitation, connection, store, nil
}

func (s *arrivalConnectionService) requireAuthorizationAttempt(rawState string, requireCompleted bool) (*models.WeComAuthorizationAttempt, error) {
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, errorsx.BusinessError(61, err.Error())
	}
	attempt := repositories.ArrivalRepository.FindAuthorizationAttemptByStateHash(
		sqls.DB(),
		security.Fingerprint("authorization_state", strings.TrimSpace(rawState)),
	)
	if attempt == nil || attempt.Status != enums.StatusOk || !attempt.ExpiresAt.After(time.Now()) {
		return nil, errorsx.InvalidToken("企微授权会话不存在或已过期")
	}
	if requireCompleted && (attempt.CompletedAt == nil || attempt.TenantAuthorizationID <= 0) {
		return nil, errorsx.BusinessError(69, "企微主体授权尚未完成")
	}
	return attempt, nil
}

func (s *arrivalConnectionService) verifyConnection(connectionID, tenantID int64, operator *dto.AuthPrincipal) (*response.ArrivalConnectionVerificationResponse, error) {
	connection := repositories.ArrivalRepository.GetConnection(sqls.DB(), connectionID, tenantID)
	if connection == nil {
		return nil, errorsx.InvalidParam("门店到店连接不存在")
	}
	providerMode := arrivalProviderModeForConnection(connection)
	result := &response.ArrivalConnectionVerificationResponse{
		ConnectionStatus: string(connection.ConnectionStatus),
		ProviderMode:     string(providerMode),
		ProviderOK:       true,
		ErrorCode:        "",
	}
	if providerMode == enums.ArrivalContactProviderModeStaticPluginTicket {
		return s.verifyStaticConnection(connection, operator, result)
	}
	authorization := repositories.ArrivalRepository.GetTenantAuthorization(sqls.DB(), connection.TenantAuthorizationID, tenantID)
	result.AuthorizationOK = authorization != nil && authorization.AuthorizationStatus == enums.WeComAuthorizationStatusActive
	security, securityErr := newArrivalSecurity()
	memberUserID := ""
	if securityErr == nil && strings.TrimSpace(connection.ContactMemberCiphertext) != "" {
		memberUserID, _ = security.Decrypt("contact_member", connection.ContactMemberCiphertext, connection.ContactMemberNonce)
	}
	result.MemberOK = strings.TrimSpace(memberUserID) != "" &&
		strings.TrimSpace(connection.ContactMemberFingerprint) != "" &&
		securityErr == nil &&
		security.Fingerprint("contact_member", memberUserID) == connection.ContactMemberFingerprint
	scope, scopeErr := resolveArrivalStoreStaffScopeDB(
		sqls.DB(),
		connection.TenantID,
		connection.StoreID,
		connection.StoreStaffBindingID,
		false,
	)
	result.InstanceOK = scopeErr == nil && scope != nil && scope.Instance != nil
	switch {
	case !result.AuthorizationOK:
		result.ErrorCode = "authorization_unavailable"
	case !result.MemberOK:
		result.ErrorCode = "contact_member_missing"
	case !result.InstanceOK:
		result.ErrorCode = "instance_mismatch"
	}
	if result.ErrorCode == "" &&
		providerMode == enums.ArrivalContactProviderModeCustomerAcquisition {
		quota, preflightErr := ArrivalAcquisitionService.Preflight(authorization)
		if quota != nil {
			result.QuotaTotal = quota.Total
			result.QuotaBalance = quota.Balance
		}
		if preflightErr != nil {
			result.ProviderOK = false
			result.ErrorCode = acquisitionFailureCode(preflightErr, "acquisition_link_verify_failed")
		}
	}
	now := time.Now()
	status := enums.ArrivalConnectionStatusActive
	if result.ErrorCode != "" {
		status = enums.ArrivalConnectionStatusInvalid
	}
	updates := map[string]any{
		"connection_status":            status,
		"last_verified_at":             now,
		"last_verification_error_code": result.ErrorCode,
		"updated_at":                   now,
		"update_user_name":             "arrival_verifier",
	}
	if operator != nil {
		updates["update_user_id"] = operator.UserID
		updates["update_user_name"] = operator.Username
	}
	if err := repositories.ArrivalRepository.UpdateConnection(sqls.DB(), connection.ID, tenantID, updates); err != nil {
		return nil, errorsx.BusinessError(72, "保存门店连接验证结果失败")
	}
	result.ConnectionStatus = string(status)
	_ = s.createAuditLog(sqls.DB(), tenantID, connection.StoreID, "connection.verify", "StoreArrivalConnection", connection.ID, map[bool]string{true: "success", false: "failed"}[result.ErrorCode == ""], operator, map[string]any{
		"authorizationOK": result.AuthorizationOK,
		"memberOK":        result.MemberOK,
		"instanceOK":      result.InstanceOK,
		"providerMode":    result.ProviderMode,
		"providerOK":      result.ProviderOK,
		"quotaTotal":      result.QuotaTotal,
		"quotaBalance":    result.QuotaBalance,
		"errorCode":       result.ErrorCode,
	})
	return result, nil
}

func (s *arrivalConnectionService) verifyStaticConnection(
	connection *models.StoreArrivalConnection,
	operator *dto.AuthPrincipal,
	result *response.ArrivalConnectionVerificationResponse,
) (*response.ArrivalConnectionVerificationResponse, error) {
	if connection == nil || result == nil {
		return nil, errorsx.InvalidParam("门店到店连接不存在")
	}
	scope, scopeErr := resolveArrivalStoreStaffScopeDB(
		sqls.DB(),
		connection.TenantID,
		connection.StoreID,
		connection.StoreStaffBindingID,
		false,
	)
	var instance *models.WxWorkProtocolInstance
	if scope != nil {
		instance = scope.Instance
	}
	result.AuthorizationOK = false
	result.MemberOK = false
	result.InstanceOK = scopeErr == nil && instance != nil
	plugID := strings.TrimSpace(connection.StaticContactPlugID)
	switch {
	case plugID == "" || len(plugID) > 191 || strings.ContainsAny(plugID, " \t\r\n"):
		result.ProviderOK = false
		result.ErrorCode = "static_plug_id_invalid"
	case !result.InstanceOK:
		result.ProviderOK = false
		result.ErrorCode = "instance_mismatch"
	default:
		connections := ArrivalBindingTicketService.staticConnectionsForInstance(instance)
		if len(connections) != 1 || connections[0].StoreID != connection.StoreID {
			result.ProviderOK = false
			result.ErrorCode = "static_store_mapping_ambiguous"
		} else if _, _, err := buildStoredArrivalBindingCardPayload(instance, 1); err != nil {
			result.ProviderOK = false
			result.ErrorCode = "static_card_template_invalid"
		}
	}
	now := time.Now()
	status := enums.ArrivalConnectionStatusActive
	if result.ErrorCode != "" {
		status = enums.ArrivalConnectionStatusInvalid
	}
	updates := map[string]any{
		"connection_status":            status,
		"last_verified_at":             now,
		"last_verification_error_code": result.ErrorCode,
		"updated_at":                   now,
		"update_user_name":             "arrival_verifier",
	}
	if operator != nil {
		updates["update_user_id"] = operator.UserID
		updates["update_user_name"] = operator.Username
	}
	if err := repositories.ArrivalRepository.UpdateConnection(
		sqls.DB(),
		connection.ID,
		connection.TenantID,
		updates,
	); err != nil {
		return nil, errorsx.BusinessError(72, "保存门店连接验证结果失败")
	}
	result.ConnectionStatus = string(status)
	_ = s.createAuditLog(
		sqls.DB(),
		connection.TenantID,
		connection.StoreID,
		"connection.verify",
		"StoreArrivalConnection",
		connection.ID,
		map[bool]string{true: "success", false: "failed"}[result.ErrorCode == ""],
		operator,
		map[string]any{
			"authorizationRequired": false,
			"instanceOK":            result.InstanceOK,
			"providerMode":          result.ProviderMode,
			"providerOK":            result.ProviderOK,
			"errorCode":             result.ErrorCode,
		},
	)
	return result, nil
}

func (s *arrivalConnectionService) buildConnectionResponse(store *models.Store, connection *models.StoreArrivalConnection) response.ArrivalConnectionResponse {
	result := response.ArrivalConnectionResponse{
		StoreID:          store.ID,
		TenantID:         store.TenantID,
		StoreCode:        strings.TrimSpace(store.StoreCode),
		StoreName:        strings.TrimSpace(store.Name),
		BrandName:        strings.TrimSpace(store.BrandName),
		ConnectionStatus: string(enums.ArrivalConnectionStatusPendingAuthorization),
		ContactProvider:  string(config.Current().Arrival.ContactProviderMode()),
	}
	if connection == nil {
		return result
	}
	result.ID = connection.ID
	result.Scene = strings.TrimSpace(connection.StoreScene)
	result.ConnectionStatus = string(connection.ConnectionStatus)
	result.ContactProvider = string(arrivalProviderModeForConnection(connection))
	result.StaticContactPlugID = strings.TrimSpace(connection.StaticContactPlugID)
	result.ContactMemberConfigured = strings.TrimSpace(connection.ContactMemberCiphertext) != ""
	result.StoreStaffBindingID = connection.StoreStaffBindingID
	result.StoreStaffAccountName = arrivalStoreStaffAccountNameDB(sqls.DB(), connection.TenantID, connection.StoreStaffBindingID)
	result.WxWorkProtocolInstanceID = connection.WxWorkProtocolInstanceID
	result.LastVerifiedAt = connection.LastVerifiedAt
	result.LastErrorCode = strings.TrimSpace(connection.LastVerificationErrorCode)
	result.UpdatedAt = connection.UpdatedAt
	if authorization := repositories.ArrivalRepository.GetTenantAuthorization(sqls.DB(), connection.TenantAuthorizationID, connection.TenantID); authorization != nil {
		result.AuthorizationStatus = string(authorization.AuthorizationStatus)
		result.AuthorizedCorpName = strings.TrimSpace(authorization.CorpName)
	}
	if result.ContactProvider == string(enums.ArrivalContactProviderModeCustomerAcquisition) &&
		connection.TenantAuthorizationID > 0 &&
		strings.TrimSpace(connection.ContactMemberFingerprint) != "" {
		if link := repositories.ArrivalRepository.FindAcquisitionLink(
			sqls.DB(),
			connection.TenantID,
			connection.TenantAuthorizationID,
			connection.StoreID,
			connection.ContactMemberFingerprint,
		); link != nil {
			result.AcquisitionLinkStatus = string(link.LinkStatus)
			result.AcquisitionQuotaTotal = link.QuotaTotal
			result.AcquisitionQuotaBalance = link.QuotaBalance
			result.AcquisitionFailureCode = strings.TrimSpace(link.FailureCode)
			result.AcquisitionLastVerifiedAt = link.LastVerifiedAt
		}
	}
	var instance *models.WxWorkProtocolInstance
	if scope, err := resolveArrivalStoreStaffScopeDB(
		sqls.DB(),
		connection.TenantID,
		connection.StoreID,
		connection.StoreStaffBindingID,
		false,
	); err == nil && scope != nil {
		instance = scope.Instance
		result.WxWorkProtocolInstanceID = scope.Instance.ID
	} else {
		instance = WxWorkProtocolInstanceService.GetByTenantID(connection.WxWorkProtocolInstanceID, connection.TenantID)
	}
	if instance != nil {
		result.WxWorkProtocolAccountName = strings.TrimSpace(instance.EmployeeName)
		result.WxWorkProtocolHealth = strings.TrimSpace(instance.HealthStatus)
	}
	since := time.Now().Add(-24 * time.Hour)
	result.RecentScanCount = repositories.ArrivalRepository.CountScanEvents(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", connection.TenantID).
		Eq("store_id", connection.StoreID).
		Gte("created_at", since))
	result.RecentBoundCount = repositories.ArrivalRepository.CountScanEvents(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", connection.TenantID).
		Eq("store_id", connection.StoreID).
		Eq("binding_status", enums.ArrivalBindingStatusBound).
		Gte("created_at", since))
	return result
}

func (s *arrivalConnectionService) generateStoreScene() (string, error) {
	for i := 0; i < 10; i++ {
		token, err := randomArrivalToken(12)
		if err != nil {
			return "", err
		}
		scene := "arr_" + token
		if len(scene) > 64 {
			scene = scene[:64]
		}
		if repositories.ArrivalRepository.FindConnectionByScene(sqls.DB(), scene) == nil {
			return scene, nil
		}
	}
	return "", fmt.Errorf("无法生成唯一门店 scene")
}

func (s *arrivalConnectionService) createAuditLog(db *gorm.DB, tenantID, storeID int64, action, entityType string, entityID int64, result string, operator *dto.AuthPrincipal, detail map[string]any) error {
	if db == nil {
		db = sqls.DB()
	}
	raw, _ := json.Marshal(detail)
	item := &models.ArrivalAuditLog{
		TenantID:   tenantID,
		StoreID:    storeID,
		Action:     strings.TrimSpace(action),
		EntityType: strings.TrimSpace(entityType),
		EntityID:   entityID,
		Result:     strings.TrimSpace(result),
		DetailJSON: string(raw),
		CreatedAt:  time.Now(),
	}
	if operator != nil {
		item.OperatorID = operator.UserID
		item.OperatorName = operator.Username
	} else {
		item.OperatorName = "arrival"
	}
	return repositories.ArrivalRepository.CreateAuditLog(db, item)
}

func containsTrimmed(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func parseArrivalProviderMode(value string) (enums.ArrivalContactProviderMode, error) {
	switch enums.ArrivalContactProviderMode(strings.TrimSpace(strings.ToLower(value))) {
	case enums.ArrivalContactProviderModeContactWay:
		return enums.ArrivalContactProviderModeContactWay, nil
	case enums.ArrivalContactProviderModeCustomerAcquisition:
		return enums.ArrivalContactProviderModeCustomerAcquisition, nil
	case enums.ArrivalContactProviderModeStaticPluginTicket:
		return enums.ArrivalContactProviderModeStaticPluginTicket, nil
	default:
		return "", errorsx.InvalidParam("不支持的到店联系模式")
	}
}
