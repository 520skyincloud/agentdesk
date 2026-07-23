package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/assetaccess"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/imsource"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/pkg/utils"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/mlogclub/simple/web"
)

var WsService = newWsService()

const dashboardPresenceDisconnectGrace = 90 * time.Second

type dashboardPresenceKey struct {
	tenantID int64
	userID   int64
}

type wsService struct {
	upgrader                websocket.Upgrader
	seq                     atomic.Uint64
	manager                 *WsConnectionManager
	presenceEndMu           sync.Mutex
	presenceEndTimers       map[dashboardPresenceKey]*time.Timer
	presenceDisconnectGrace time.Duration
}

func newWsService() *wsService {
	return &wsService{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		manager:                 newWsConnectionManager(),
		presenceEndTimers:       make(map[dashboardPresenceKey]*time.Timer),
		presenceDisconnectGrace: dashboardPresenceDisconnectGrace,
	}
}

func (s *wsService) HandleDashboardWS(ctx *gin.Context) {
	principal := AuthService.GetAuthPrincipal(ctx)
	if principal == nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, web.JsonError(errorsx.Unauthorized("未登录或登录已过期")))
		return
	}
	if !AuthService.HasPermission(ctx, constants.PermissionConversationView.Code) {
		ctx.AbortWithStatusJSON(http.StatusForbidden, web.JsonError(errorsx.Forbidden("无权限查看会话")))
		return
	}
	if principal.ActiveTenantID <= 0 {
		ctx.AbortWithStatusJSON(http.StatusForbidden, web.JsonError(errorsx.Forbidden("请先进入需要管理会话的接入公司")))
		return
	}
	if err := s.upgradeConnection(ctx, principal, nil, realtimeRoleAdmin, principal.ActiveTenantID); err != nil {
		slog.Error("upgrade admin websocket failed", "error", err, "path", ctx.Request.URL.Path)
		ctx.Abort()
		return
	}
}

func (s *wsService) HandleDashboardNotificationWS(ctx *gin.Context) {
	principal := AuthService.GetAuthPrincipal(ctx)
	if principal == nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, web.JsonError(errorsx.Unauthorized("未登录或登录已过期")))
		return
	}
	if err := s.upgradeConnection(ctx, principal, nil, realtimeRoleNotification, principal.TenantID); err != nil {
		slog.Error("upgrade dashboard notification websocket failed", "error", err, "path", ctx.Request.URL.Path)
		ctx.Abort()
		return
	}
}

func (s *wsService) HandleOpenWS(ctx *gin.Context) {
	channel := ChannelService.GetEnabledChannel(ctx)
	if channel == nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, web.JsonErrorMsg("接入渠道不存在或已停用"))
		return
	}

	var (
		principal           = AuthService.GetAuthPrincipal(ctx)
		external            *openidentity.ExternalUser
		customerSessionInfo *CustomerSessionVerifyResult
	)
	if principal == nil {
		result, err := CustomerSessionService.VerifyRequest(ctx, channel)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, web.JsonError(err))
			return
		}
		external = result.ExternalUser
		customerSessionInfo = result
	}
	if err := s.upgradeConnection(ctx, principal, external, realtimeRoleUser, channel.TenantID, customerSessionInfo); err != nil {
		slog.Error("upgrade open im websocket failed", "error", err, "path", ctx.Request.URL.Path, "channelId", channel.ChannelID, "channel_id", channel.ID)
		ctx.Abort()
		return
	}
}

func (s *wsService) upgradeConnection(ctx *gin.Context, principal *dto.AuthPrincipal, external *openidentity.ExternalUser, role string, tenantID int64, customerSessionInfo ...*CustomerSessionVerifyResult) error {
	conn, err := s.upgrader.Upgrade(ctx.Writer, ctx.Request, nil)
	if err != nil {
		return err
	}

	session := &ClientSession{
		ID:           s.nextID("conn"),
		TenantID:     tenantID,
		Conn:         conn,
		Principal:    principal,
		External:     external,
		Role:         role,
		TerminalType: s.resolveTerminalType(ctx, role),
		Topics:       make(map[string]struct{}),
		Send:         make(chan []byte, realtimeSendBufferSize),
	}
	session.touch()

	conn.SetReadLimit(realtimeMaxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(realtimePongWait))
	conn.SetPongHandler(func(string) error {
		session.touch()
		return conn.SetReadDeadline(time.Now().Add(realtimePongWait))
	})

	var logUserID int64
	var logExternalID string
	if principal != nil {
		logUserID = principal.UserID
	}
	if external != nil {
		logExternalID = strings.TrimSpace(external.ExternalID)
	}

	sessionCount := s.manager.Register(session, s.defaultTopics(session))
	slog.Info("realtime client connected",
		"connId", session.ID,
		"role", session.Role,
		"tenantId", session.TenantID,
		"userId", logUserID,
		"externalId", logExternalID,
		"terminalType", session.TerminalType,
		"topicCount", len(session.Topics),
		"sessionCount", sessionCount,
	)
	if principal != nil && (role == realtimeRoleAdmin || role == realtimeRoleNotification) {
		now := time.Now()
		if err := AgentProfileService.MarkUserOnline(principal.UserID, principal.Username, now); err != nil {
			slog.Warn("mark realtime agent online failed", "error", err, "userId", principal.UserID, "role", role)
		}
		if err := AgentPresenceService.Touch(principal, "dashboard_ws", now); err != nil {
			slog.Warn("record realtime agent presence failed", "error", err, "userId", principal.UserID, "role", role)
		}
	}

	go s.writePump(session)
	go s.readPump(session)

	session.enqueueEvent(s.newEvent("", RealtimeConnectedEvent{
		Payload: RealtimeConnectedPayload{
			ConnID:       session.ID,
			UserID:       logUserID,
			GuestID:      logExternalID,
			Role:         role,
			TerminalType: session.TerminalType,
			Topics:       session.topicList(),
		},
	}))
	if len(customerSessionInfo) > 0 && customerSessionInfo[0] != nil && customerSessionInfo[0].Refreshed {
		session.enqueueEvent(s.newEvent("", RealtimeCustomerSessionRefreshEvent{
			Payload: RealtimeCustomerSessionRefreshPayload{
				CustomerSessionToken: customerSessionInfo[0].Token,
				ExpiresAt:            customerSessionInfo[0].ExpiresAt.Format(time.DateTime),
			},
		}))
	}
	return nil
}

func (s *wsService) readPump(session *ClientSession) {
	defer s.closeSession(session)

	for {
		_, body, err := session.Conn.ReadMessage()
		if err != nil {
			return
		}
		session.touch()

		input := realtimeClientMessage{}
		if err := json.Unmarshal(body, &input); err != nil {
			session.enqueueEvent(s.newEvent("", RealtimeResyncRequiredEvent{
				Payload: RealtimeResyncRequiredPayload{
					Reason: enums.IMRealtimeResyncReasonInvalidPayload,
				},
			}))
			continue
		}

		switch strings.TrimSpace(input.Type) {
		case enums.IMRealtimeClientTypePing:
			session.enqueueEvent(s.newEvent("", RealtimePongEvent{}))
		case enums.IMRealtimeClientTypeSubscribe:
			topics := s.subscribeTopics(session, input.Topics)
			if len(topics) > 0 {
				session.enqueueEvent(s.newEvent("", RealtimeSubscribedEvent{
					Payload: RealtimeTopicsPayload{Topics: topics},
				}))
			}
		case enums.IMRealtimeClientTypeUnsubscribe:
			topics := s.unsubscribeTopics(session, input.Topics)
			if len(topics) > 0 {
				session.enqueueEvent(s.newEvent("", RealtimeUnsubscribedEvent{
					Payload: RealtimeTopicsPayload{Topics: topics},
				}))
			}
		case enums.IMRealtimeClientTypeAck:
			slog.Debug("realtime event ack",
				"connId", session.ID,
				"eventId", strings.TrimSpace(input.EventID),
			)
		default:
			session.enqueueEvent(s.newEvent("", RealtimeResyncRequiredEvent{
				Payload: RealtimeResyncRequiredPayload{
					Reason: enums.IMRealtimeResyncReasonUnsupportedMessageType,
				},
			}))
		}
	}
}

func (s *wsService) writePump(session *ClientSession) {
	ticker := time.NewTicker(realtimePingPeriod)
	defer func() {
		ticker.Stop()
		s.closeSession(session)
	}()

	for {
		select {
		case payload, ok := <-session.Send:
			_ = session.Conn.SetWriteDeadline(time.Now().Add(realtimeWriteWait))
			if !ok {
				_ = session.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := session.Conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			s.touchDashboardPresence(session, time.Now())
			_ = session.Conn.SetWriteDeadline(time.Now().Add(realtimeWriteWait))
			if err := session.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (s *wsService) touchDashboardPresence(session *ClientSession, at time.Time) {
	if session == nil || session.Principal == nil || (session.Role != realtimeRoleAdmin && session.Role != realtimeRoleNotification) {
		return
	}
	s.cancelScheduledPresenceEnd(session.TenantID, session.Principal.UserID)
	if err := AgentProfileService.MarkUserOnline(session.Principal.UserID, session.Principal.Username, at); err != nil {
		slog.Warn("refresh realtime agent online state failed", "error", err, "userId", session.Principal.UserID, "role", session.Role)
	}
	if err := AgentPresenceService.Touch(session.Principal, "dashboard_ws", at); err != nil {
		slog.Warn("refresh realtime agent presence failed", "error", err, "userId", session.Principal.UserID, "role", session.Role)
	}
}

func (s *wsService) closeSession(session *ClientSession) {
	if session == nil {
		return
	}
	session.closeOnce.Do(func() {
		session.Closed.Store(true)
		remaining := s.manager.Unregister(session)

		if session.Send != nil {
			close(session.Send)
		}
		if session.Conn != nil {
			_ = session.Conn.Close()
		}

		var discUserID int64
		var discExternalID string
		if session.Principal != nil {
			discUserID = session.Principal.UserID
		}
		if session.External != nil {
			discExternalID = strings.TrimSpace(session.External.ExternalID)
		}
		slog.Info("realtime client disconnected",
			"connId", session.ID,
			"role", session.Role,
			"tenantId", session.TenantID,
			"userId", discUserID,
			"externalId", discExternalID,
			"terminalType", session.TerminalType,
			"sessionCount", remaining,
		)
		if session.Principal != nil && (session.Role == realtimeRoleAdmin || session.Role == realtimeRoleNotification) && s.manager.CountUserSessions(session.TenantID, session.Principal.UserID) == 0 {
			s.schedulePresenceEnd(session.TenantID, session.Principal.UserID, session.Role)
		}
	})
}

func (s *wsService) cancelScheduledPresenceEnd(tenantID, userID int64) {
	if tenantID <= 0 || userID <= 0 {
		return
	}
	key := dashboardPresenceKey{tenantID: tenantID, userID: userID}
	s.presenceEndMu.Lock()
	defer s.presenceEndMu.Unlock()
	if timer := s.presenceEndTimers[key]; timer != nil {
		timer.Stop()
		delete(s.presenceEndTimers, key)
	}
}

func (s *wsService) schedulePresenceEnd(tenantID, userID int64, role string) {
	if tenantID <= 0 || userID <= 0 {
		return
	}
	key := dashboardPresenceKey{tenantID: tenantID, userID: userID}
	s.presenceEndMu.Lock()
	defer s.presenceEndMu.Unlock()
	if timer := s.presenceEndTimers[key]; timer != nil {
		timer.Stop()
	}
	grace := s.presenceDisconnectGrace
	if grace < 0 {
		grace = 0
	}
	var timer *time.Timer
	timer = time.AfterFunc(grace, func() {
		s.finishScheduledPresenceEnd(key, timer, role)
	})
	s.presenceEndTimers[key] = timer
}

func (s *wsService) finishScheduledPresenceEnd(key dashboardPresenceKey, timer *time.Timer, role string) {
	s.presenceEndMu.Lock()
	if s.presenceEndTimers[key] != timer {
		s.presenceEndMu.Unlock()
		return
	}
	delete(s.presenceEndTimers, key)
	if s.manager.CountUserSessions(key.tenantID, key.userID) > 0 {
		s.presenceEndMu.Unlock()
		return
	}
	if err := AgentPresenceService.End(key.tenantID, key.userID, time.Now()); err != nil {
		slog.Warn("close realtime agent presence after reconnect grace failed", "error", err, "userId", key.userID, "role", role)
	}
	s.presenceEndMu.Unlock()
	if _, err := ConversationDispatchService.RecoverAssignmentsForAgent(key.tenantID, key.userID, 0); err != nil {
		slog.Warn("recover assignments after realtime agent disconnect failed", "error", err, "tenantId", key.tenantID, "userId", key.userID)
	}
}

func (s *wsService) subscribeTopics(session *ClientSession, topics []string) []string {
	allowed := s.filterAllowedTopics(session, topics)
	if len(allowed) == 0 {
		return nil
	}
	return s.manager.Subscribe(session, allowed)
}

func (s *wsService) unsubscribeTopics(session *ClientSession, topics []string) []string {
	allowed := s.filterAllowedTopics(session, topics)
	if len(allowed) == 0 {
		return nil
	}
	return s.manager.Unsubscribe(session, allowed, sliceToSet(s.defaultTopics(session)))
}

func (s *wsService) PublishMessageCreated(conversation *models.Conversation, message *models.Message) {
	if conversation == nil || message == nil {
		return
	}
	content, payload := utils.BuildRenderableMessage(message)

	event := s.newEvent(s.conversationTopic(conversation.ID), RealtimeMessageCreatedEvent{
		Payload: RealtimeMessageCreatedPayload{
			ConversationID:    conversation.ID,
			MessageID:         message.ID,
			RequestID:         message.RequestID,
			Message:           s.buildRealtimeMessage(message),
			Status:            conversation.Status,
			CurrentAssigneeID: conversation.CurrentAssigneeID,
			SenderType:        message.SenderType,
			SenderID:          message.SenderID,
			MessageType:       message.MessageType,
			Content:           content,
			Payload:           payload,
			SeqNo:             message.SeqNo,
			SendStatus:        message.SendStatus,
			SentAt:            formatWsTime(message.SentAt),
		},
	})
	s.PublishToTopics(s.routeConversationTopics(conversation), event)
}

func (s *wsService) PublishMessageUpdated(conversation *models.Conversation, message *models.Message) {
	if conversation == nil || message == nil {
		return
	}
	content, payload := utils.BuildRenderableMessage(message)
	event := s.newEvent(s.conversationTopic(conversation.ID), RealtimeMessageUpdatedEvent{
		Payload: RealtimeMessageCreatedPayload{
			ConversationID:    conversation.ID,
			MessageID:         message.ID,
			RequestID:         message.RequestID,
			Message:           s.buildRealtimeMessage(message),
			Status:            conversation.Status,
			CurrentAssigneeID: conversation.CurrentAssigneeID,
			SenderType:        message.SenderType,
			SenderID:          message.SenderID,
			MessageType:       message.MessageType,
			Content:           content,
			Payload:           payload,
			SeqNo:             message.SeqNo,
			SendStatus:        message.SendStatus,
			SentAt:            formatWsTime(message.SentAt),
		},
	})
	s.PublishToTopics(s.routeConversationTopics(conversation), event)
}

func (s *wsService) buildRealtimeMessage(item *models.Message) response.MessageResponse {
	if item == nil {
		return response.MessageResponse{}
	}
	agentReadState, customerReadState := ConversationReadStateService.GetConversationReadStates(item.ConversationID)
	content, payload := utils.BuildRenderableMessage(item)
	ret := response.MessageResponse{
		ID:              item.ID,
		ConversationID:  item.ConversationID,
		RequestID:       item.RequestID,
		ClientMsgID:     item.ClientMsgID,
		SenderType:      item.SenderType,
		SenderID:        item.SenderID,
		MessageType:     item.MessageType,
		Content:         content,
		Payload:         payload,
		SeqNo:           item.SeqNo,
		SendStatus:      item.SendStatus,
		SentAt:          utils.FormatTimePtr(item.SentAt),
		DeliveredAt:     utils.FormatTimePtr(item.DeliveredAt),
		ReadAt:          utils.FormatTimePtr(item.ReadAt),
		CustomerRead:    isRealtimeMessageRead(item, customerReadState),
		CustomerReadAt:  realtimeReadMessageAt(item, customerReadState),
		AgentRead:       isRealtimeMessageRead(item, agentReadState),
		AgentReadAt:     realtimeReadMessageAt(item, agentReadState),
		RecalledAt:      utils.FormatTimePtr(item.RecalledAt),
		QuotedMessageID: item.QuotedMessageID,
	}
	ret.SendSource = imsource.DetectSendSource(item.SenderType, item.RequestID, item.ClientMsgID)
	ret.SendSourceLabel = imsource.SendSourceLabel(ret.SendSource, "")
	s.fillRealtimeMessageSender(&ret, item)
	return ret
}

func (s *wsService) fillRealtimeMessageSender(ret *response.MessageResponse, item *models.Message) {
	if ret == nil || item == nil || item.SenderID <= 0 {
		return
	}
	switch item.SenderType {
	case enums.IMSenderTypeAI:
		if aiAgent := AIAgentService.GetByTenantID(item.SenderID, item.TenantID); aiAgent != nil {
			ret.SenderName = aiAgent.Name
		}
	case enums.IMSenderTypeAgent:
		if profile := AgentProfileService.GetByUserID(item.SenderID); profile != nil {
			if displayName := strings.TrimSpace(profile.DisplayName); displayName != "" {
				ret.SenderName = displayName
			}
			if avatar := AssetService.RefreshAccessURL(profile.Avatar, item.TenantID, assetaccess.PurposeInline); avatar != "" {
				ret.SenderAvatar = avatar
			}
		}
		if ret.SenderName == "" {
			s.fillRealtimeMessageUserName(ret, item.SenderID)
		}
	default:
		s.fillRealtimeMessageUserName(ret, item.SenderID)
	}
}

func (s *wsService) fillRealtimeMessageUserName(ret *response.MessageResponse, userID int64) {
	if ret == nil || userID <= 0 {
		return
	}
	if user := UserService.Get(userID); user != nil {
		ret.SenderName = user.Nickname
		if ret.SenderName == "" {
			ret.SenderName = user.Username
		}
	}
}

func isRealtimeMessageRead(item *models.Message, state *models.ConversationReadState) bool {
	return item != nil && state != nil && state.LastReadSeqNo >= item.SeqNo
}

func realtimeReadMessageAt(item *models.Message, state *models.ConversationReadState) string {
	if !isRealtimeMessageRead(item, state) {
		return ""
	}
	return utils.FormatTimePtr(state.LastReadAt)
}

func (s *wsService) PublishMessageRecalled(conversation *models.Conversation, message *models.Message) {
	if conversation == nil || message == nil {
		return
	}

	event := s.newEvent(s.conversationTopic(conversation.ID), RealtimeMessageRecalledEvent{
		Payload: RealtimeMessageRecalledPayload{
			ConversationID: conversation.ID,
			MessageID:      message.ID,
			SenderType:     message.SenderType,
			SenderID:       message.SenderID,
			SendStatus:     message.SendStatus,
			RecalledAt:     formatWsTime(message.RecalledAt),
		},
	})
	s.PublishToTopics(s.routeConversationTopics(conversation), event)
}

func (s *wsService) PublishConversationChanged(conversation *models.Conversation, eventType string) {
	if conversation == nil {
		return
	}
	agentReadState, customerReadState := ConversationReadStateService.GetConversationReadStates(conversation.ID)
	routePayload := s.buildConversationRouteRealtimePayload(conversation.ID, conversation.TenantID)

	event := s.newEvent(s.conversationTopic(conversation.ID), RealtimeConversationChangedEvent{
		Type: eventType,
		Payload: RealtimeConversationChangedPayload{
			ConversationID:            conversation.ID,
			Status:                    conversation.Status,
			ServiceMode:               conversation.ServiceMode,
			CurrentAssigneeID:         conversation.CurrentAssigneeID,
			CurrentTeamID:             conversation.CurrentTeamID,
			LastMessageID:             conversation.LastMessageID,
			LastMessageAt:             formatWsTime(&conversation.LastMessageAt),
			LastActiveAt:              formatWsTime(&conversation.LastActiveAt),
			LastMessageSummary:        utils.RepairMojibakeText(conversation.LastMessageSummary),
			CustomerUnreadCount:       conversation.CustomerUnreadCount,
			AgentUnreadCount:          conversation.AgentUnreadCount,
			CustomerLastReadMessageID: readStateMessageID(customerReadState),
			CustomerLastReadSeqNo:     readStateSeqNo(customerReadState),
			CustomerLastReadAt:        readStateAt(customerReadState),
			AgentLastReadMessageID:    readStateMessageID(agentReadState),
			AgentLastReadSeqNo:        readStateSeqNo(agentReadState),
			AgentLastReadAt:           readStateAt(agentReadState),
			RouteStatus:               routePayload.RouteStatus,
			RouteStatusLabel:          routePayload.RouteStatusLabel,
			RouteTarget:               routePayload.RouteTarget,
			HandoffReason:             routePayload.HandoffReason,
			NeedHumanFollowUp:         routePayload.NeedHumanFollowUp,
			ManualExpireAt:            routePayload.ManualExpireAt,
			ManualAttention:           routePayload.ManualAttention,
			StoreID:                   routePayload.StoreID,
			StoreName:                 routePayload.StoreName,
			WxWorkInstanceID:          routePayload.WxWorkInstanceID,
			WxWorkEmployeeName:        routePayload.WxWorkEmployeeName,
			WxWorkEmployeeUserID:      routePayload.WxWorkEmployeeUserID,
		},
	})
	s.PublishToTopics(s.routeConversationTopics(conversation), event)
}

func (s *wsService) PublishCustomerTagChanged(conversation *models.Conversation, storeID, storeCustomerRelationID int64, updatedAt time.Time) {
	if conversation == nil || conversation.TenantID <= 0 {
		return
	}
	event := s.newEvent(s.adminTenantTopic(conversation.TenantID), RealtimeCustomerTagChangedEvent{
		Payload: RealtimeCustomerTagChangedPayload{
			TenantID: conversation.TenantID, StoreID: storeID, CustomerID: conversation.CustomerID,
			StoreCustomerRelationID: storeCustomerRelationID, ConversationID: conversation.ID,
			UpdatedAt: updatedAt.Format(time.RFC3339Nano),
		},
	})
	topics := []string{s.adminTenantTopic(conversation.TenantID), s.dispatchTenantTopic(conversation.TenantID)}
	if conversation.CurrentAssigneeID > 0 {
		topics = append(topics, s.adminTopic(conversation.CurrentAssigneeID))
	}
	s.PublishToTopics(topics, event)
}

func (s *wsService) PublishCustomerTagRuntimePolicyChanged(
	tenantID int64,
	storeIDs []int64,
	allStores bool,
	tenantPolicyChanged bool,
	evolutionEnabled *bool,
	replyEnabled *bool,
	updatedAt time.Time,
) {
	if tenantID <= 0 {
		return
	}
	event := s.newEvent(s.adminTenantTopic(tenantID), RealtimeCustomerTagRuntimePolicyChangedEvent{
		Payload: RealtimeCustomerTagRuntimePolicyChangedPayload{
			TenantID: tenantID, StoreIDs: uniquePositive(storeIDs), AllStores: allStores,
			TenantPolicyChanged:         tenantPolicyChanged,
			CustomerTagEvolutionEnabled: evolutionEnabled, ReplyTagContextEnabled: replyEnabled,
			UpdatedAt: updatedAt.Format(time.RFC3339Nano),
		},
	})
	s.PublishToTopics([]string{s.adminTenantTopic(tenantID)}, event)
}

func (s *wsService) buildConversationRouteRealtimePayload(conversationID, tenantID int64) RealtimeConversationChangedPayload {
	route := ConversationRouteService.GetByConversationIDInTenant(conversationID, tenantID)
	if route == nil {
		return RealtimeConversationChangedPayload{}
	}
	payload := RealtimeConversationChangedPayload{
		RouteStatus:       route.RouteStatus,
		RouteStatusLabel:  enums.GetConversationRouteStatusLabel(route.RouteStatus),
		RouteTarget:       route.RouteTarget,
		HandoffReason:     utils.RepairMojibakeText(route.HandoffReason),
		NeedHumanFollowUp: route.NeedHumanFollowUp,
		ManualExpireAt:    utils.FormatTimePtr(route.ManualExpireAt),
		StoreID:           route.StoreID,
		WxWorkInstanceID:  route.WxWorkInstanceID,
	}
	payload.ManualAttention = buildRealtimeManualAttention(route, payload.ManualExpireAt)
	if route.StoreID > 0 {
		if store := StoreService.GetInTenant(route.StoreID, tenantID); store != nil {
			payload.StoreName = utils.RepairMojibakeText(store.Name)
		}
	}
	if route.WxWorkInstanceID > 0 {
		if instance := WxWorkProtocolInstanceService.GetByTenantID(route.WxWorkInstanceID, tenantID); instance != nil {
			payload.WxWorkEmployeeName = utils.RepairMojibakeText(instance.EmployeeName)
			payload.WxWorkEmployeeUserID = instance.EmployeeUserID
			if payload.StoreID == 0 {
				payload.StoreID = instance.StoreID
			}
			if payload.StoreName == "" && instance.StoreID > 0 {
				if store := StoreService.GetInTenant(instance.StoreID, tenantID); store != nil {
					payload.StoreName = utils.RepairMojibakeText(store.Name)
				}
			}
		}
	}
	return payload
}

func buildRealtimeManualAttention(route *models.ConversationRouteState, manualExpireAt string) response.ConversationManualAttentionResponse {
	ret := response.ConversationManualAttentionResponse{
		Level: "none",
		Label: "AI接待中",
	}
	if route == nil {
		return ret
	}
	isSafety := isSafetyHandoffReason(route.HandoffReason)
	switch route.RouteStatus {
	case enums.ConversationRouteStatusHQAgentDeskPending:
		ret.Dot = true
		ret.Level = "normal"
		ret.Label = "待总部接入"
		ret.ExpiresAt = manualExpireAt
		if isSafety {
			ret.Level = "urgent"
		}
	case enums.ConversationRouteStatusStoreWecomManual:
		ret.Level = "serving"
		ret.Label = "门店处理中"
		ret.ExpiresAt = manualExpireAt
		if route.NeedHumanFollowUp {
			ret.Dot = true
			ret.Level = "normal"
			ret.Label = "门店待跟进"
			if isSafety {
				ret.Level = "urgent"
				ret.Label = "安全风险待跟进"
			}
		}
	case enums.ConversationRouteStatusHQAgentDeskServing:
		ret.Level = "serving"
		ret.Label = "人工处理中"
		ret.ExpiresAt = manualExpireAt
	}
	return ret
}

func (s *wsService) PublishNotificationCreated(userID int64, notification response.NotificationResponse) {
	if userID <= 0 || notification.ID <= 0 {
		return
	}
	topic := s.notificationTopic(userID)
	event := s.newEvent(topic, RealtimeNotificationCreatedEvent{
		Payload: RealtimeNotificationCreatedPayload{
			Notification: notification,
		},
	})
	s.PublishToTopic(topic, event)
}

func (s *wsService) PublishResyncRequired(topics []string, reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = enums.IMRealtimeResyncReasonManual
	}
	s.PublishToTopics(topics, s.newEvent("", RealtimeResyncRequiredEvent{
		Payload: RealtimeResyncRequiredPayload{Reason: reason},
	}))
}

func readStateMessageID(state *models.ConversationReadState) int64 {
	if state == nil {
		return 0
	}
	return state.LastReadMessageID
}

func readStateSeqNo(state *models.ConversationReadState) int64 {
	if state == nil {
		return 0
	}
	return state.LastReadSeqNo
}

func readStateAt(state *models.ConversationReadState) string {
	if state == nil {
		return ""
	}
	return utils.FormatTimePtr(state.LastReadAt)
}

func (s *wsService) Publish(event RealtimeEvent) {
	if strings.TrimSpace(event.Topic) == "" {
		return
	}
	s.PublishToTopic(event.Topic, event)
}

func (s *wsService) PublishToTopic(topic string, event RealtimeEvent) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return
	}
	if event.Topic == "" {
		event.Topic = topic
	}
	s.PublishToTopics([]string{topic}, event)
}

func (s *wsService) PublishToTopics(topics []string, event RealtimeEvent) {
	normalized := normalizeRealtimeTopics(topics)
	if len(normalized) == 0 {
		return
	}

	targets := s.manager.FindByTopics(normalized)
	if len(targets) == 0 {
		return
	}

	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("marshal realtime event failed", "error", err, "type", event.Type)
		return
	}

	for _, session := range targets {
		if session.enqueue(payload) {
			continue
		}
		slog.Warn("drop slow realtime client",
			"connId", session.ID,
			"role", session.Role,
			"type", event.Type,
			"topic", event.Topic,
		)
		go s.closeSession(session)
	}
}

func (s *wsService) IsGuestOnline(tenantID int64, guestID string) bool {
	guestID = strings.TrimSpace(guestID)
	if tenantID <= 0 || guestID == "" {
		return false
	}
	return s.manager.HasTopic(s.guestTopic(tenantID, guestID))
}

func (s *wsService) routeConversationTopics(conversation *models.Conversation) []string {
	if conversation == nil {
		return nil
	}

	topics := []string{s.conversationTopic(conversation.ID)}
	if identity := ConversationService.GetConversationExternalIdentity(conversation); identity != nil && strings.TrimSpace(identity.ExternalID) != "" {
		topics = append(topics, s.guestTopic(conversation.TenantID, identity.ExternalID))
	}
	if conversation.CurrentAssigneeID > 0 {
		topics = append(topics, s.adminTopic(conversation.CurrentAssigneeID))
	} else if conversation.TenantID > 0 {
		topics = append(topics, s.adminTenantTopic(conversation.TenantID))
	}
	if conversation.TenantID > 0 {
		topics = append(topics, s.dispatchTenantTopic(conversation.TenantID))
	}
	return normalizeRealtimeTopics(topics)
}

func (s *wsService) defaultTopics(session *ClientSession) []string {
	if session == nil {
		return nil
	}

	switch session.Role {
	case realtimeRoleNotification:
		if session.Principal == nil || session.Principal.UserID <= 0 {
			return nil
		}
		return []string{s.notificationTopic(session.Principal.UserID)}
	case realtimeRoleAdmin:
		if session.Principal == nil || session.Principal.UserID <= 0 || session.TenantID <= 0 {
			return nil
		}
		topics := []string{s.adminTopic(session.Principal.UserID), s.adminTenantTopic(session.TenantID)}
		if slices.Contains(session.Principal.Permissions, constants.PermissionConversationHandover.Code) {
			topics = append(topics, s.dispatchTenantTopic(session.TenantID))
		}
		return topics
	default:
		// 开放 IM：仅 External、无 AuthPrincipal 的访客连接必须仍能订阅 guest:{externalId}，否则收不到推送。
		if session.TenantID > 0 && session.External != nil && strings.TrimSpace(session.External.ExternalID) != "" {
			return []string{s.guestTopic(session.TenantID, session.External.ExternalID)}
		}
		if session.Principal != nil && session.Principal.UserID > 0 {
			return []string{s.userTopic(session.Principal.UserID)}
		}
		return nil
	}
}

func (s *wsService) filterAllowedTopics(session *ClientSession, topics []string) []string {
	normalized := normalizeRealtimeTopics(topics)
	if len(normalized) == 0 || session == nil {
		return nil
	}
	switch session.Role {
	case realtimeRoleNotification:
		if session.Principal == nil {
			return nil
		}
	case realtimeRoleAdmin:
		if session.Principal == nil {
			return nil
		}
	default:
		hasUser := session.Principal != nil && session.Principal.UserID > 0
		hasExternal := session.External != nil && strings.TrimSpace(session.External.ExternalID) != ""
		if !hasUser && !hasExternal {
			return nil
		}
	}

	defaultTopics := sliceToSet(s.defaultTopics(session))
	ret := make([]string, 0, len(normalized))
	for _, topic := range normalized {
		if _, ok := defaultTopics[topic]; ok {
			ret = append(ret, topic)
			continue
		}
		if conversationID, ok := parseConversationTopic(topic); ok && s.canSubscribeConversation(session, conversationID) {
			ret = append(ret, topic)
		}
	}
	return ret
}

func (s *wsService) canSubscribeConversation(session *ClientSession, conversationID int64) bool {
	if session == nil || conversationID <= 0 {
		return false
	}
	if session.Role == realtimeRoleAdmin {
		return session.Principal != nil &&
			session.TenantID > 0 &&
			session.Principal.ActiveTenantID == session.TenantID &&
			AgentTeamScopeService.CanViewConversation(session.Principal, conversationID)
	}
	conversation := ConversationService.Get(conversationID)
	if conversation == nil || session.TenantID <= 0 || conversation.TenantID != session.TenantID {
		return false
	}
	if session.External != nil {
		return ConversationService.IsCustomerConversationOwner(conversation, *session.External)
	}
	return false
}

func (s *wsService) resolveTerminalType(ctx *gin.Context, role string) string {
	if ctx == nil {
		return "web"
	}
	terminalType := strings.TrimSpace(ctx.Query("terminalType"))
	if terminalType != "" {
		return terminalType
	}
	if role == realtimeRoleAdmin {
		return "dashboard_web"
	}
	return "web"
}

func (s *wsService) newEvent(topic string, event RealtimeDomainEvent) RealtimeEvent {
	if event == nil {
		return RealtimeEvent{
			EventID: s.nextID("evt"),
			Topic:   topic,
			At:      time.Now().Format(time.DateTime),
		}
	}
	return RealtimeEvent{
		EventID: s.nextID("evt"),
		Type:    event.EventType(),
		Topic:   topic,
		Data:    event.EventPayload(),
		At:      time.Now().Format(time.DateTime),
	}
}

func (s *wsService) nextID(prefix string) string {
	seq := s.seq.Add(1)
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), seq)
}

func (s *wsService) userTopic(userID int64) string {
	return realtimeTopicUserPrefix + strconv.FormatInt(userID, 10)
}

func (s *wsService) guestTopic(tenantID int64, guestID string) string {
	return realtimeTopicGuestPrefix + strconv.FormatInt(tenantID, 10) + ":" + strings.TrimSpace(guestID)
}

func (s *wsService) adminTopic(userID int64) string {
	return realtimeTopicAdminPrefix + strconv.FormatInt(userID, 10)
}

func (s *wsService) adminTenantTopic(tenantID int64) string {
	return realtimeTopicAdminTenantPrefix + strconv.FormatInt(tenantID, 10)
}

func (s *wsService) dispatchTenantTopic(tenantID int64) string {
	return realtimeTopicDispatchPrefix + strconv.FormatInt(tenantID, 10)
}

func (s *wsService) notificationTopic(userID int64) string {
	return realtimeTopicNotificationPrefix + strconv.FormatInt(userID, 10)
}

func (s *wsService) conversationTopic(conversationID int64) string {
	return realtimeTopicConversationPrefix + strconv.FormatInt(conversationID, 10)
}

func normalizeRealtimeTopics(topics []string) []string {
	if len(topics) == 0 {
		return nil
	}
	ret := make([]string, 0, len(topics))
	seen := make(map[string]struct{}, len(topics))
	for _, topic := range topics {
		item := strings.TrimSpace(topic)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		ret = append(ret, item)
	}
	return ret
}

func parseConversationTopic(topic string) (int64, bool) {
	if !strings.HasPrefix(topic, realtimeTopicConversationPrefix) {
		return 0, false
	}
	value := strings.TrimPrefix(topic, realtimeTopicConversationPrefix)
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func sliceToSet(items []string) map[string]struct{} {
	ret := make(map[string]struct{}, len(items))
	for _, item := range items {
		ret[item] = struct{}{}
	}
	return ret
}

func formatWsTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format(time.DateTime)
}
