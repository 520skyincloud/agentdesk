package services

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
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
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	arrivalBindInputVersion       = "arrival_bind_input.v1"
	arrivalBindResultVersion      = "arrival_bind_result.v1"
	arrivalBindingTicketIDKey     = "arrival_binding_ticket_id"
	arrivalBindingCardKindKey     = "arrival_card_kind"
	arrivalBindingCardKind        = "binding_ticket"
	arrivalBindingCardPagePath    = "pages/arrival/index"
	arrivalBindingTicketMaxLength = 192
)

var ArrivalBindingTicketService = &arrivalBindingTicketService{}

var arrivalBindingTicketQueryPattern = regexp.MustCompile(`(?i)(bindTicket(?:=|%3D))[A-Za-z0-9_-]+`)

type arrivalBindingCardMessageSender interface {
	SendSystemOutboundMessage(
		conversationID int64,
		clientMsgID string,
		messageType enums.IMMessageType,
		content, payload, requestID string,
	) (*models.Message, error)
}

type arrivalBindingTicketService struct {
	conversationLocks sync.Map
	ticketLocks       sync.Map
	loginExchanger    arrivalMiniProgramLoginExchanger
	messageSender     arrivalBindingCardMessageSender
}

func (s *arrivalBindingTicketService) HasActiveStaticConnection(instanceID int64) bool {
	instance := WxWorkProtocolInstanceService.Get(instanceID)
	if !isActivatedCurrentWxWorkProtocolInstance(instance) || instance.StoreStaffBindingID <= 0 {
		return false
	}
	return len(s.staticConnectionsForInstance(instance)) > 0
}

func (s *arrivalBindingTicketService) SendBindingCardForNewContact(
	conversation *models.Conversation,
	instance *models.WxWorkProtocolInstance,
	requestID string,
) error {
	if conversation == nil || instance == nil {
		return nil
	}
	connection, err := s.uniqueStaticConnectionForInstance(instance)
	if err != nil {
		return err
	}
	if connection == nil {
		return nil
	}
	return s.ensureAndSendBindingCard(conversation, instance, connection, requestID)
}

func (s *arrivalBindingTicketService) SendBindingCardForConversation(
	conversationID int64,
	operator *dto.AuthPrincipal,
	requestID string,
) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if !AgentTeamScopeService.CanViewConversation(operator, conversationID) {
		return errorsx.Forbidden("无权操作该会话")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), conversationID, tenantID)
	if conversation == nil {
		return errorsx.InvalidParam("会话不存在")
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(
		sqls.DB(),
		conversation.ID,
		conversation.TenantID,
	)
	if route == nil || route.StoreID <= 0 || route.StoreStaffBindingID <= 0 || route.WxWorkInstanceID <= 0 {
		return errorsx.BusinessError(71, "会话无法确定唯一门店和企微员工号实例")
	}
	scope, err := resolveArrivalStoreStaffScopeDB(
		sqls.DB(),
		conversation.TenantID,
		route.StoreID,
		route.StoreStaffBindingID,
		false,
	)
	if err != nil {
		return err
	}
	instance := scope.Instance
	if instance.ID != route.WxWorkInstanceID {
		return errorsx.BusinessError(71, "会话尚未切换到门店员工号的当前企微实例")
	}
	connection, err := s.uniqueStaticConnectionForInstance(instance)
	if err != nil {
		return err
	}
	if connection == nil ||
		connection.StoreID != route.StoreID ||
		connection.StoreStaffBindingID != route.StoreStaffBindingID {
		return errorsx.BusinessError(71, "会话门店未配置静态联系我绑定")
	}
	return s.ensureAndSendBindingCard(conversation, instance, connection, requestID)
}

func (s *arrivalBindingTicketService) Bind(
	req request.ArrivalBindRequest,
	requestID string,
) (*response.ArrivalBindResultResponse, error) {
	if strings.TrimSpace(req.SchemaVersion) != arrivalBindInputVersion {
		return nil, errorsx.InvalidParam("不支持的到店绑定契约版本")
	}
	if len(strings.TrimSpace(req.LoginCode)) < 4 || len(req.LoginCode) > 256 {
		return nil, errorsx.InvalidParam("小程序登录凭证无效")
	}
	rawTicket := strings.TrimSpace(req.BindTicket)
	if !validArrivalBindingTicketText(rawTicket) {
		return nil, errorsx.InvalidParam("到店绑定票据格式无效")
	}
	if err := validateArrivalCommonConfiguration(config.Current().Arrival); err != nil {
		return nil, err
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, errorsx.BusinessError(61, err.Error())
	}
	ticketID, entropyHash, err := security.ParseBindingTicket(rawTicket)
	if err != nil {
		return nil, errorsx.BusinessError(66, "到店绑定票据无效")
	}
	ticketHash := security.Fingerprint("arrival_binding_ticket", rawTicket)
	ticketHint := repositories.ArrivalRepository.GetBindingTicketByID(sqls.DB(), ticketID)
	if ticketHint == nil ||
		ticketHint.TicketHash != ticketHash ||
		ticketHint.TokenEntropyHash != entropyHash {
		return nil, errorsx.BusinessError(66, "到店绑定票据无效")
	}
	login, err := s.miniProgramLoginExchanger().ExchangeMiniProgramLoginCode(req.LoginCode)
	if err != nil {
		return nil, errorsx.BusinessError(62, "小程序登录凭证无效或已失效")
	}
	identity, _, err := ArrivalLinkService.ensureIdentity(
		ticketHint.TenantID,
		login.OpenID,
		login.UnionID,
	)
	if err != nil {
		return nil, err
	}
	lockValue, _ := s.ticketLocks.LoadOrStore(ticketHash, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer func() {
		lock.Unlock()
		s.ticketLocks.Delete(ticketHash)
	}()

	var (
		store             *models.Store
		committedErr      error
		boundIdentity     *models.MiniProgramIdentity
		currentInstanceID int64
	)
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		ticket, findErr := repositories.ArrivalRepository.FindBindingTicketByHashForUpdate(ctx.Tx, ticketHash)
		if findErr != nil {
			return findErr
		}
		if ticket == nil ||
			ticket.ID != ticketID ||
			ticket.TokenEntropyHash != entropyHash ||
			ticket.Status != enums.StatusOk {
			return errorsx.BusinessError(66, "到店绑定票据无效")
		}
		expectedTicket, tokenErr := security.BindingTicket(ticket.ID, ticket.TokenEntropyHash)
		if tokenErr != nil ||
			security.Fingerprint("arrival_binding_ticket", expectedTicket) != ticket.TicketHash {
			return errorsx.BusinessError(66, "到店绑定票据无效")
		}
		boundIdentity = identity
		if boundIdentity.TenantID != ticket.TenantID {
			return errorsx.BusinessError(69, "小程序身份与绑定票据不属于同一接入公司")
		}
		store = repositories.StoreRepository.GetInTenant(ctx.Tx, ticket.StoreID, ticket.TenantID)
		switch ticket.TicketStatus {
		case enums.ArrivalBindingTicketStatusRevoked:
			return errorsx.BusinessError(68, "到店绑定票据已撤销")
		case enums.ArrivalBindingTicketStatusConsumed:
			if ticket.ConsumedMiniProgramIdentityID != boundIdentity.ID {
				return errorsx.BusinessError(69, "到店绑定票据已由其他身份使用")
			}
			binding, bindingErr := repositories.ArrivalRepository.FindBindingForUpdate(
				ctx.Tx,
				ticket.TenantID,
				boundIdentity.ID,
				ticket.StoreID,
			)
			if bindingErr != nil {
				return bindingErr
			}
			if !arrivalBindingMatchesTicketDB(ctx.Tx, binding, ticket) {
				return errorsx.BusinessError(69, "到店绑定关系与票据不一致")
			}
			return nil
		case enums.ArrivalBindingTicketStatusExpired:
			return errorsx.BusinessError(67, "到店绑定票据已过期")
		case enums.ArrivalBindingTicketStatusPending:
		default:
			return errorsx.BusinessError(66, "到店绑定票据无效")
		}
		now := time.Now()
		if !ticket.ExpiresAt.After(now) {
			if updateErr := repositories.ArrivalRepository.UpdateBindingTicket(
				ctx.Tx,
				ticket.ID,
				ticket.TenantID,
				map[string]any{
					"ticket_status":    enums.ArrivalBindingTicketStatusExpired,
					"updated_at":       now,
					"update_user_name": "arrival",
				},
			); updateErr != nil {
				return updateErr
			}
			committedErr = errorsx.BusinessError(67, "到店绑定票据已过期")
			return nil
		}
		ticketContext, contextErr := s.validateTicketContext(ctx.Tx, ticket)
		if contextErr != nil {
			return contextErr
		}
		currentInstanceID = ticketContext.StoreStaff.Instance.ID
		if repositories.ArrivalRepository.FindRecentScanEvent(
			ctx.Tx,
			ticket.TenantID,
			ticket.StoreID,
			boundIdentity.ID,
			now.Add(-time.Duration(config.Current().Arrival.BindPendingScanWindow())*time.Minute),
		) == nil {
			return errorsx.BusinessError(70, "没有符合条件的近期门店扫码")
		}
		binding, bindingErr := repositories.ArrivalRepository.FindBindingForUpdate(
			ctx.Tx,
			ticket.TenantID,
			boundIdentity.ID,
			ticket.StoreID,
		)
		if bindingErr != nil {
			return bindingErr
		}
		if binding != nil &&
			binding.BindingStatus == enums.ArrivalBindingStatusBound &&
			!arrivalBindingMatchesTicketDB(ctx.Tx, binding, ticket) {
			return errorsx.BusinessError(69, "该身份已绑定当前门店的其他会话")
		}
		evidenceHash := security.Fingerprint(
			"arrival_card_ticket_evidence",
			strings.Join([]string{
				strconv.FormatInt(ticket.ID, 10),
				strconv.FormatInt(boundIdentity.ID, 10),
				strconv.FormatInt(ticket.StoreID, 10),
				strconv.FormatInt(ticket.ConversationID, 10),
			}, ":"),
		)
		if binding == nil {
			binding = &models.ArrivalStoreBinding{
				TenantID:                 ticket.TenantID,
				StoreID:                  ticket.StoreID,
				StoreStaffBindingID:      ticket.StoreStaffBindingID,
				MiniProgramIdentityID:    boundIdentity.ID,
				WxWorkProtocolInstanceID: currentInstanceID,
				CustomerID:               ticket.CustomerID,
				ConversationID:           ticket.ConversationID,
				OfficialRelationStatus:   enums.ArrivalOfficialRelationStatusUnconfirmed,
				BindingProofType:         enums.ArrivalBindingProofTypeCardTicket,
				BindingTicketID:          ticket.ID,
				BindingStatus:            enums.ArrivalBindingStatusBound,
				EvidenceHash:             evidenceHash,
				ProtocolMappedAt:         &now,
				Status:                   enums.StatusOk,
				AuditFields:              arrivalSystemAuditFields(now),
			}
			if err := repositories.ArrivalRepository.CreateBinding(ctx.Tx, binding); err != nil {
				return err
			}
		} else {
			if err := repositories.ArrivalRepository.UpdateBinding(
				ctx.Tx,
				binding.ID,
				binding.TenantID,
				map[string]any{
					"tenant_authorization_id":           0,
					"external_user_id_ciphertext":       "",
					"external_user_id_nonce":            "",
					"external_user_id_fingerprint":      "",
					"contact_member_ciphertext":         "",
					"contact_member_nonce":              "",
					"contact_member_fingerprint":        "",
					"store_staff_binding_id":            ticket.StoreStaffBindingID,
					"wx_work_protocol_instance_id":      currentInstanceID,
					"customer_id":                       ticket.CustomerID,
					"conversation_id":                   ticket.ConversationID,
					"protocol_conversation_ciphertext":  "",
					"protocol_conversation_nonce":       "",
					"protocol_conversation_fingerprint": "",
					"official_relation_status":          enums.ArrivalOfficialRelationStatusUnconfirmed,
					"binding_proof_type":                enums.ArrivalBindingProofTypeCardTicket,
					"binding_ticket_id":                 ticket.ID,
					"binding_status":                    enums.ArrivalBindingStatusBound,
					"evidence_hash":                     evidenceHash,
					"official_relationship_at":          nil,
					"protocol_mapped_at":                now,
					"status":                            enums.StatusOk,
					"updated_at":                        now,
					"update_user_name":                  "arrival",
				},
			); err != nil {
				return err
			}
		}
		if err := repositories.ArrivalRepository.UpdateBindingTicket(
			ctx.Tx,
			ticket.ID,
			ticket.TenantID,
			map[string]any{
				"ticket_status":                     enums.ArrivalBindingTicketStatusConsumed,
				"consumed_at":                       now,
				"consumed_mini_program_identity_id": boundIdentity.ID,
				"updated_at":                        now,
				"update_user_name":                  "arrival",
			},
		); err != nil {
			return err
		}
		return ArrivalConnectionService.createAuditLog(
			ctx.Tx,
			ticket.TenantID,
			ticket.StoreID,
			"binding_ticket.consume",
			"ArrivalBindingTicket",
			ticket.ID,
			"success",
			nil,
			map[string]any{
				"mappingMode": "card_ticket",
				"requestId":   tracex.NormalizeRequestID(requestID),
			},
		)
	})
	if err != nil {
		return nil, err
	}
	if committedErr != nil {
		return nil, committedErr
	}
	if store == nil || boundIdentity == nil {
		return nil, errorsx.BusinessError(71, "门店或小程序身份暂不可用")
	}
	storeResponse := response.ArrivalStoreResponse{
		Name:      strings.TrimSpace(store.Name),
		BrandName: strings.TrimSpace(store.BrandName),
		Address:   strings.TrimSpace(store.Address),
		Phone:     strings.TrimSpace(store.ContactPhone),
	}
	return &response.ArrivalBindResultResponse{
		SchemaVersion: arrivalBindResultVersion,
		BindingStatus: string(enums.ArrivalBindingStatusBound),
		Store:         storeResponse,
	}, nil
}

func (s *arrivalBindingTicketService) MaterializeOutboundMessage(
	message *models.Message,
) (*models.Message, bool, error) {
	if message == nil || message.MessageType != enums.IMMessageTypeMiniProgram {
		return message, false, nil
	}
	body := map[string]any{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(message.Payload)), &body); err != nil {
		return message, false, nil
	}
	if strings.TrimSpace(fmt.Sprint(body[arrivalBindingCardKindKey])) != arrivalBindingCardKind {
		return message, false, nil
	}
	ticketID := int64FromAny(body[arrivalBindingTicketIDKey])
	if ticketID <= 0 {
		return nil, true, errorsx.InvalidParam("到店绑定卡片缺少内部票据引用")
	}
	ticket := repositories.ArrivalRepository.GetBindingTicket(sqls.DB(), ticketID, message.TenantID)
	if ticket == nil ||
		ticket.ConversationID != message.ConversationID ||
		ticket.Status != enums.StatusOk ||
		(ticket.TicketStatus == enums.ArrivalBindingTicketStatusPending &&
			!ticket.ExpiresAt.After(time.Now())) ||
		ticket.TicketStatus == enums.ArrivalBindingTicketStatusExpired ||
		ticket.TicketStatus == enums.ArrivalBindingTicketStatusRevoked {
		return nil, true, errorsx.InvalidParam("到店绑定票据不存在或已失效")
	}
	security, err := newArrivalSecurity()
	if err != nil {
		return nil, true, err
	}
	rawTicket, err := security.BindingTicket(ticket.ID, ticket.TokenEntropyHash)
	if err != nil ||
		security.Fingerprint("arrival_binding_ticket", rawTicket) != ticket.TicketHash {
		return nil, true, errorsx.InvalidParam("到店绑定票据校验失败")
	}
	delete(body, arrivalBindingTicketIDKey)
	delete(body, arrivalBindingCardKindKey)
	for _, key := range []string{"page_path", "pagePath", "path"} {
		delete(body, key)
	}
	body["appid"] = strings.TrimSpace(config.Current().Arrival.MiniProgramAppID)
	body["page_path"] = arrivalBindingCardPagePath + "?bindTicket=" + rawTicket
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, true, err
	}
	copyMessage := *message
	copyMessage.Payload = string(payload)
	return &copyMessage, true, nil
}

func (s *arrivalBindingTicketService) ensureAndSendBindingCard(
	conversation *models.Conversation,
	instance *models.WxWorkProtocolInstance,
	connection *models.StoreArrivalConnection,
	requestID string,
) error {
	if err := s.validateCardContext(sqls.DB(), conversation, instance, connection); err != nil {
		return err
	}
	ticket, err := s.ensurePendingTicket(conversation, instance, connection)
	if err != nil {
		return err
	}
	content, payload, err := buildStoredArrivalBindingCardPayload(instance, ticket.ID)
	if err != nil {
		return errorsx.BusinessError(71, err.Error())
	}
	message, err := s.bindingCardMessageSender().SendSystemOutboundMessage(
		conversation.ID,
		"arrival_bind_ticket_"+strconv.FormatInt(ticket.ID, 10),
		enums.IMMessageTypeMiniProgram,
		content,
		payload,
		tracex.NormalizeRequestID(requestID),
	)
	if err != nil {
		return err
	}
	outbox := ChannelMessageOutboxService.GetByMessageIDInTenant(
		enums.ChannelTypeWxWorkProtocol,
		message.ID,
		conversation.TenantID,
	)
	updates := map[string]any{
		"message_id":       message.ID,
		"updated_at":       time.Now(),
		"update_user_name": "arrival",
	}
	if outbox != nil {
		updates["outbox_id"] = outbox.ID
	}
	return repositories.ArrivalRepository.UpdateBindingTicket(
		sqls.DB(),
		ticket.ID,
		ticket.TenantID,
		updates,
	)
}

func (s *arrivalBindingTicketService) ensurePendingTicket(
	conversation *models.Conversation,
	instance *models.WxWorkProtocolInstance,
	connection *models.StoreArrivalConnection,
) (*models.ArrivalBindingTicket, error) {
	key := fmt.Sprintf("%d:%d", conversation.TenantID, conversation.ID)
	lockValue, _ := s.conversationLocks.LoadOrStore(key, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer func() {
		lock.Unlock()
		s.conversationLocks.Delete(key)
	}()

	security, err := newArrivalSecurity()
	if err != nil {
		return nil, err
	}
	var ticket *models.ArrivalBindingTicket
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedConversation, lockErr := repositories.ConversationRepository.GetForUpdateInTenant(
			ctx.Tx,
			conversation.ID,
			conversation.TenantID,
		)
		if lockErr != nil {
			return lockErr
		}
		if lockedConversation == nil {
			return errorsx.BusinessError(71, "真实会话暂不可用")
		}
		now := time.Now()
		existing := repositories.ArrivalRepository.FindPendingBindingTicketByConversation(
			ctx.Tx,
			conversation.TenantID,
			conversation.ID,
			now,
		)
		if existing != nil &&
			existing.StoreID == connection.StoreID &&
			existing.StoreStaffBindingID == conversation.StoreStaffBindingID &&
			existing.WxWorkProtocolInstanceID == instance.ID &&
			existing.CustomerID == conversation.CustomerID {
			ticket = existing
			return nil
		}
		if existing != nil {
			if err := repositories.ArrivalRepository.UpdateBindingTicket(
				ctx.Tx,
				existing.ID,
				existing.TenantID,
				map[string]any{
					"ticket_status":    enums.ArrivalBindingTicketStatusRevoked,
					"revoked_at":       now,
					"updated_at":       now,
					"update_user_name": "arrival",
				},
			); err != nil {
				return err
			}
		}
		entropy, err := randomArrivalToken(32)
		if err != nil {
			return err
		}
		entropyHash := security.Fingerprint("arrival_binding_ticket_entropy", entropy)
		ticket = &models.ArrivalBindingTicket{
			TenantID:                 conversation.TenantID,
			StoreID:                  connection.StoreID,
			StoreStaffBindingID:      conversation.StoreStaffBindingID,
			WxWorkProtocolInstanceID: instance.ID,
			CustomerID:               conversation.CustomerID,
			ConversationID:           conversation.ID,
			TicketHash:               security.Fingerprint("arrival_binding_ticket_placeholder", entropy),
			TokenEntropyHash:         entropyHash,
			TicketStatus:             enums.ArrivalBindingTicketStatusPending,
			ExpiresAt:                now.Add(time.Duration(config.Current().Arrival.BindTicketTTL()) * time.Minute),
			Status:                   enums.StatusOk,
			AuditFields:              arrivalSystemAuditFields(now),
		}
		if err := repositories.ArrivalRepository.CreateBindingTicket(ctx.Tx, ticket); err != nil {
			return err
		}
		rawTicket, err := security.BindingTicket(ticket.ID, entropyHash)
		if err != nil {
			return err
		}
		ticket.TicketHash = security.Fingerprint("arrival_binding_ticket", rawTicket)
		return repositories.ArrivalRepository.UpdateBindingTicket(
			ctx.Tx,
			ticket.ID,
			ticket.TenantID,
			map[string]any{
				"ticket_hash":      ticket.TicketHash,
				"updated_at":       now,
				"update_user_name": "arrival",
			},
		)
	})
	return ticket, err
}

func (s *arrivalBindingTicketService) validateTicketContext(
	db *gorm.DB,
	ticket *models.ArrivalBindingTicket,
) (*arrivalBoundConversationScope, error) {
	if ticket == nil || ticket.TenantID <= 0 || ticket.StoreID <= 0 ||
		ticket.StoreStaffBindingID <= 0 || ticket.WxWorkProtocolInstanceID <= 0 ||
		ticket.CustomerID <= 0 || ticket.ConversationID <= 0 {
		return nil, errorsx.BusinessError(66, "到店绑定票据无效")
	}
	connection := repositories.ArrivalRepository.FindConnectionByStore(
		db,
		ticket.TenantID,
		ticket.StoreID,
	)
	issuedByInstance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(
		db,
		ticket.WxWorkProtocolInstanceID,
		ticket.TenantID,
	)
	conversation := repositories.ConversationRepository.GetInTenant(
		db,
		ticket.ConversationID,
		ticket.TenantID,
	)
	store := repositories.StoreRepository.GetInTenant(db, ticket.StoreID, ticket.TenantID)
	if connection == nil || connection.Status != enums.StatusOk ||
		connection.ConnectionStatus != enums.ArrivalConnectionStatusActive ||
		arrivalProviderModeForConnection(connection) != enums.ArrivalContactProviderModeStaticPluginTicket ||
		connection.StoreStaffBindingID != ticket.StoreStaffBindingID ||
		store == nil || store.Status != enums.StatusOk ||
		issuedByInstance == nil || issuedByInstance.StoreID != ticket.StoreID ||
		issuedByInstance.StoreStaffBindingID != ticket.StoreStaffBindingID ||
		conversation == nil || conversation.StoreID != ticket.StoreID ||
		conversation.StoreStaffBindingID != ticket.StoreStaffBindingID ||
		conversation.CustomerID != ticket.CustomerID ||
		conversation.Status == enums.IMConversationStatusClosed {
		return nil, errorsx.BusinessError(71, "真实会话暂不可用")
	}
	storeStaff, err := resolveArrivalStoreStaffScopeDB(
		db,
		ticket.TenantID,
		ticket.StoreID,
		ticket.StoreStaffBindingID,
		false,
	)
	if err != nil {
		return nil, errorsx.BusinessError(71, "真实会话暂不可用")
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(
		db,
		conversation.ID,
		conversation.TenantID,
	)
	if route == nil || route.RouteStatus == enums.ConversationRouteStatusClosed ||
		route.StoreID != ticket.StoreID ||
		route.StoreStaffBindingID != ticket.StoreStaffBindingID ||
		route.WxWorkInstanceID != storeStaff.Instance.ID {
		return nil, errorsx.BusinessError(71, "真实会话暂不可用")
	}
	return &arrivalBoundConversationScope{
		StoreStaff:   storeStaff,
		Conversation: conversation,
		Route:        route,
	}, nil
}

func (s *arrivalBindingTicketService) validateCardContext(
	db *gorm.DB,
	conversation *models.Conversation,
	instance *models.WxWorkProtocolInstance,
	connection *models.StoreArrivalConnection,
) error {
	if db == nil ||
		conversation == nil ||
		instance == nil ||
		connection == nil ||
		conversation.TenantID <= 0 ||
		conversation.CustomerID <= 0 ||
		connection.TenantID != conversation.TenantID ||
		instance.TenantID != conversation.TenantID ||
		connection.StoreID != instance.StoreID ||
		connection.StoreID != conversation.StoreID ||
		connection.StoreStaffBindingID <= 0 ||
		connection.StoreStaffBindingID != instance.StoreStaffBindingID ||
		connection.StoreStaffBindingID != conversation.StoreStaffBindingID ||
		arrivalProviderModeForConnection(connection) != enums.ArrivalContactProviderModeStaticPluginTicket ||
		connection.ConnectionStatus != enums.ArrivalConnectionStatusActive ||
		connection.Status != enums.StatusOk ||
		!isActivatedCurrentWxWorkProtocolInstance(instance) {
		return errorsx.BusinessError(71, "门店、员工实例或会话暂不可用")
	}
	scope, err := resolveArrivalStoreStaffScopeDB(
		db,
		conversation.TenantID,
		connection.StoreID,
		connection.StoreStaffBindingID,
		false,
	)
	if err != nil || scope.Instance.ID != instance.ID {
		return errorsx.BusinessError(71, "门店员工号当前企微实例与会话路由不一致")
	}
	store := repositories.StoreRepository.GetInTenant(db, connection.StoreID, connection.TenantID)
	customer := repositories.CustomerRepository.GetInTenant(db, conversation.CustomerID, conversation.TenantID)
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(
		db,
		conversation.ID,
		conversation.TenantID,
	)
	mapping := repositories.WxWorkKFConversationRepository.FindOne(
		db,
		sqls.NewCnd().
			Eq("tenant_id", conversation.TenantID).
			Eq("conversation_id", conversation.ID).
			Eq("status", enums.StatusOk),
	)
	protocolConversationID := WxWorkProtocolService.protocolConversationID(mapping)
	if store == nil ||
		store.Status != enums.StatusOk ||
		customer == nil ||
		customer.Status == enums.StatusDeleted ||
		conversation.Status == enums.IMConversationStatusClosed ||
		route == nil ||
		route.RouteStatus == enums.ConversationRouteStatusClosed ||
		route.StoreID != connection.StoreID ||
		route.StoreStaffBindingID != connection.StoreStaffBindingID ||
		route.WxWorkInstanceID != instance.ID ||
		!strings.HasPrefix(protocolConversationID, "S:") ||
		strings.TrimSpace(strings.TrimPrefix(protocolConversationID, "S:")) == "" {
		return errorsx.BusinessError(71, "门店、员工实例或会话暂不可用")
	}
	if _, _, err := buildStoredArrivalBindingCardPayload(instance, 1); err != nil {
		return errorsx.BusinessError(71, err.Error())
	}
	return nil
}

func (s *arrivalBindingTicketService) staticConnectionsForInstance(
	instance *models.WxWorkProtocolInstance,
) []models.StoreArrivalConnection {
	if instance == nil || instance.TenantID <= 0 || instance.StoreID <= 0 || instance.StoreStaffBindingID <= 0 ||
		!isActivatedCurrentWxWorkProtocolInstance(instance) {
		return nil
	}
	scope, err := resolveArrivalStoreStaffScopeDB(
		sqls.DB(),
		instance.TenantID,
		instance.StoreID,
		instance.StoreStaffBindingID,
		false,
	)
	if err != nil || scope.Instance.ID != instance.ID {
		return nil
	}
	candidates := repositories.ArrivalRepository.FindActiveConnectionsByBinding(
		sqls.DB(),
		instance.TenantID,
		instance.StoreStaffBindingID,
	)
	result := make([]models.StoreArrivalConnection, 0, len(candidates))
	for i := range candidates {
		if arrivalProviderModeForConnection(&candidates[i]) == enums.ArrivalContactProviderModeStaticPluginTicket {
			result = append(result, candidates[i])
		}
	}
	return result
}

func (s *arrivalBindingTicketService) uniqueStaticConnectionForInstance(
	instance *models.WxWorkProtocolInstance,
) (*models.StoreArrivalConnection, error) {
	connections := s.staticConnectionsForInstance(instance)
	if len(connections) == 0 {
		return nil, nil
	}
	if len(connections) > 1 {
		_ = ArrivalConnectionService.createAuditLog(
			sqls.DB(),
			instance.TenantID,
			0,
			"binding_ticket.mapping_ambiguous",
			"WxWorkProtocolInstance",
			instance.ID,
			"failed",
			nil,
			map[string]any{"candidateCount": len(connections)},
		)
		return nil, errorsx.BusinessError(71, "企微员工号实例映射了多个静态到店门店")
	}
	return &connections[0], nil
}

func (s *arrivalBindingTicketService) miniProgramLoginExchanger() arrivalMiniProgramLoginExchanger {
	if s.loginExchanger != nil {
		return s.loginExchanger
	}
	return WeComProviderService
}

func (s *arrivalBindingTicketService) bindingCardMessageSender() arrivalBindingCardMessageSender {
	if s.messageSender != nil {
		return s.messageSender
	}
	return MessageService
}

func buildStoredArrivalBindingCardPayload(
	instance *models.WxWorkProtocolInstance,
	ticketID int64,
) (string, string, error) {
	content, payload, err := WxWorkProtocolDefaultResourceService.BuildDefaultMiniProgramMessage(instance)
	if err != nil {
		return "", "", fmt.Errorf("门店员工实例未配置可用的小程序卡片模板")
	}
	body := map[string]any{}
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return "", "", fmt.Errorf("门店小程序卡片模板无效")
	}
	for _, key := range []string{
		"page_path", "pagePath", "path",
		"bindTicket", "conversation_id", "conversationId",
		"customer_id", "customerId", "external_userid", "externalUserId",
		"guid", "access_token", "sessionToken",
	} {
		delete(body, key)
	}
	username := strings.TrimSpace(fmt.Sprint(body["username"]))
	cover := firstNonBlank(
		strings.TrimSpace(fmt.Sprint(body["thumb_url"])),
		strings.TrimSpace(fmt.Sprint(body["image_url"])),
		strings.TrimSpace(fmt.Sprint(body["cover_url"])),
		strings.TrimSpace(fmt.Sprint(body["appicon"])),
	)
	if username == "" || username == "<nil>" || cover == "" || cover == "<nil>" {
		return "", "", fmt.Errorf("门店小程序卡片模板缺少 username 或封面")
	}
	body["appid"] = strings.TrimSpace(config.Current().Arrival.MiniProgramAppID)
	body["title"] = firstNonBlank(strings.TrimSpace(fmt.Sprint(body["title"])), "连接门店服务")
	body[arrivalBindingTicketIDKey] = ticketID
	body[arrivalBindingCardKindKey] = arrivalBindingCardKind
	raw, err := json.Marshal(body)
	if err != nil {
		return "", "", err
	}
	return firstNonBlank(strings.TrimSpace(content), "连接门店服务"), string(raw), nil
}

func validArrivalBindingTicketText(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > arrivalBindingTicketMaxLength {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '-' ||
			c == '_' {
			continue
		}
		return false
	}
	return true
}

func redactArrivalBindingTicketError(message *models.Message, reason string) string {
	reason = arrivalBindingTicketQueryPattern.ReplaceAllString(strings.TrimSpace(reason), "${1}[REDACTED]")
	if message == nil || strings.TrimSpace(message.Payload) == "" {
		return reason
	}
	body := map[string]any{}
	if err := json.Unmarshal([]byte(message.Payload), &body); err != nil {
		return reason
	}
	for _, key := range []string{"page_path", "pagePath", "path"} {
		pagePath := strings.TrimSpace(fmt.Sprint(body[key]))
		index := strings.Index(pagePath, "bindTicket=")
		if index < 0 {
			continue
		}
		rawTicket := pagePath[index+len("bindTicket="):]
		if separator := strings.IndexByte(rawTicket, '&'); separator >= 0 {
			rawTicket = rawTicket[:separator]
		}
		if validArrivalBindingTicketText(rawTicket) {
			reason = strings.ReplaceAll(reason, rawTicket, "[REDACTED]")
		}
	}
	return reason
}

func arrivalBindingMatchesTicketDB(
	db *gorm.DB,
	binding *models.ArrivalStoreBinding,
	ticket *models.ArrivalBindingTicket,
) bool {
	if db == nil || binding == nil || ticket == nil ||
		binding.Status != enums.StatusOk ||
		binding.BindingStatus != enums.ArrivalBindingStatusBound ||
		binding.BindingProofType != enums.ArrivalBindingProofTypeCardTicket ||
		binding.BindingTicketID != ticket.ID ||
		binding.TenantID != ticket.TenantID ||
		binding.StoreID != ticket.StoreID ||
		binding.CustomerID != ticket.CustomerID {
		return false
	}
	if binding.StoreStaffBindingID == ticket.StoreStaffBindingID && binding.ConversationID == ticket.ConversationID {
		return true
	}
	// The ticket stays immutable issuance evidence; only an explicit linear
	// conversation inheritance can move the current binding away from it.
	current := repositories.ConversationRepository.GetInTenant(db, binding.ConversationID, binding.TenantID)
	source := repositories.ConversationRepository.GetInTenant(db, ticket.ConversationID, ticket.TenantID)
	if current == nil || source == nil ||
		current.StoreID != binding.StoreID || current.CustomerID != binding.CustomerID ||
		current.StoreStaffBindingID != binding.StoreStaffBindingID ||
		source.StoreID != ticket.StoreID || source.CustomerID != ticket.CustomerID ||
		source.StoreStaffBindingID != ticket.StoreStaffBindingID {
		return false
	}
	links, err := repositories.ConversationContinuityLinkRepository.FindPredecessorChain(db, binding.TenantID, current.ID, conversationHistoryMaxSegments)
	if err != nil {
		return false
	}
	for i := range links {
		if links[i].PredecessorConversationID == source.ID {
			return true
		}
	}
	return false
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		ret, _ := typed.Int64()
		return ret
	case string:
		ret, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return ret
	default:
		return 0
	}
}
