package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"agent-desk/internal/events"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/eventbus"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"slices"
	"strings"
	"time"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var ConversationService = newConversationService()

func newConversationService() *conversationService {
	return &conversationService{}
}

type conversationService struct {
}

type StoreConversationScope struct {
	StoreID             int64
	StoreStaffBindingID int64
}

func (s *conversationService) Get(id int64) *models.Conversation {
	if id <= 0 {
		return nil
	}
	return repositories.ConversationRepository.Get(sqls.DB(), id)
}

func (s *conversationService) GetByTenantID(id, tenantID int64) *models.Conversation {
	return repositories.ConversationRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *conversationService) Find(cnd *sqls.Cnd) []models.Conversation {
	return repositories.ConversationRepository.Find(sqls.DB(), cnd)
}

func (s *conversationService) FindOne(cnd *sqls.Cnd) *models.Conversation {
	return repositories.ConversationRepository.FindOne(sqls.DB(), cnd)
}

func (s *conversationService) FindPageByCnd(cnd *sqls.Cnd) (list []models.Conversation, paging *sqls.Paging) {
	return repositories.ConversationRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *conversationService) ListConversations(operator *dto.AuthPrincipal, filter request.AgentConversationFilter, keyword string, wxWorkInstanceID int64, paging *sqls.Paging) ([]models.Conversation, *sqls.Paging, error) {
	if operator == nil || operator.UserID <= 0 {
		return nil, nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, nil, errorsx.Forbidden("请先进入需要管理会话的接入公司")
	}
	storeStaffScoped := AgentTeamScopeService.Resolve(operator).StoreStaffScoped
	cnd := sqls.NewCnd().Page(paging.Page, paging.Limit)

	if strs.IsNotBlank(keyword) {
		keyword = strings.TrimSpace(keyword)
		keywordLike := "%" + keyword + "%"
		cnd.Where("customer_name LIKE ? OR last_message_summary LIKE ?", keywordLike, keywordLike)
	}

	if wxWorkInstanceID > 0 && filter != request.AgentConversationFilterMyAttention {
		cnd.Where("id IN (SELECT conversation_id FROM t_conversation_route_state WHERE tenant_id = ? AND wx_work_instance_id = ?)", tenantID, wxWorkInstanceID)
	}
	cnd = AgentTeamScopeService.ApplyConversationFilter(cnd, operator)

	switch filter {
	case request.AgentConversationFilterAllOpen:
		cnd.NotEq("status", enums.IMConversationStatusClosed).Desc("last_active_at").Desc("id")
	case request.AgentConversationFilterAIServing:
		cnd.Eq("current_assignee_id", 0).Eq("status", enums.IMConversationStatusAIServing).Desc("last_active_at").Desc("id")
	case request.AgentConversationFilterMine:
		cnd.Eq("current_assignee_id", operator.UserID).Desc("last_active_at").Desc("id")
	case request.AgentConversationFilterActive:
		if !storeStaffScoped {
			cnd.Eq("current_assignee_id", operator.UserID)
		}
		cnd.Eq("status", enums.IMConversationStatusActive).Desc("last_active_at").Desc("id")
	case request.AgentConversationFilterPending:
		cnd.Eq("current_assignee_id", 0).Eq("status", enums.IMConversationStatusPending).Asc("last_active_at").Desc("id")
	case request.AgentConversationFilterClosed:
		if !storeStaffScoped {
			cnd.Eq("current_assignee_id", operator.UserID)
		}
		cnd.Eq("status", enums.IMConversationStatusClosed).Desc("last_active_at").Desc("id")
	case request.AgentConversationFilterMyAttention:
		if !slices.Contains(operator.Roles, constants.RoleCodeCsUser) {
			return nil, nil, errorsx.Forbidden("只有客服账号可以查看我的待回复")
		}
		cnd.Eq("current_assignee_id", operator.UserID).
			Eq("status", enums.IMConversationStatusActive).
			Where("id IN (SELECT conversation_id FROM t_conversation_route_state WHERE tenant_id = ? AND need_human_follow_up = ?)", tenantID, true).
			Asc("last_active_at").Desc("id")
	default:
		return nil, nil, errorsx.InvalidParam("会话筛选项不合法")
	}

	list, paging := repositories.ConversationRepository.FindPageByCndWithManualAttentionFirst(sqls.DB(), cnd)
	return list, paging, nil
}

func (s *conversationService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.ConversationRepository.Updates(sqls.DB(), id, columns)
}

func (s *conversationService) getLatestNotFinishedByCustomerID(db *gorm.DB, customerID, tenantID int64) *models.Conversation {
	if customerID <= 0 || tenantID <= 0 {
		return nil
	}
	cnd := sqls.NewCnd()
	cnd.Eq("tenant_id", tenantID)
	cnd.Eq("customer_id", customerID)
	cnd.In("status", []enums.IMConversationStatus{
		enums.IMConversationStatusAIServing,
		enums.IMConversationStatusPending,
		enums.IMConversationStatusActive,
	})
	cnd.Desc("id")
	return repositories.ConversationRepository.FindOne(db, cnd)
}

func (s *conversationService) Create(externalUser openidentity.ExternalUser, channelID, aiAgentID int64) (*models.Conversation, error) {
	return s.create(externalUser, channelID, aiAgentID, true)
}

func (s *conversationService) CreateWithoutWelcome(externalUser openidentity.ExternalUser, channelID, aiAgentID int64) (*models.Conversation, error) {
	return s.create(externalUser, channelID, aiAgentID, false)
}

func (s *conversationService) CreateWithRuntimeProfileWithoutWelcome(externalUser openidentity.ExternalUser, channelID int64, aiAgent models.AIAgent) (*models.Conversation, error) {
	conversation, _, err := s.createWithProfile(externalUser, channelID, aiAgent, false, nil)
	return conversation, err
}

func (s *conversationService) CreateStoreScopedWithRuntimeProfileWithoutWelcome(
	externalUser openidentity.ExternalUser,
	channelID int64,
	aiAgent models.AIAgent,
	scope StoreConversationScope,
) (*models.Conversation, bool, error) {
	return s.createWithProfile(externalUser, channelID, aiAgent, false, &scope)
}

func (s *conversationService) create(externalUser openidentity.ExternalUser, channelID, aiAgentID int64, createWelcome bool) (*models.Conversation, error) {
	channel := repositories.ChannelRepository.Get(sqls.DB(), channelID)
	if channel == nil || channel.Status != enums.StatusOk || channel.TenantID <= 0 {
		return nil, errorsx.InvalidParam("接入渠道不存在、已停用或缺少租户归属")
	}
	aiAgent := AIAgentService.GetByTenantID(aiAgentID, channel.TenantID)
	if aiAgent == nil || aiAgent.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("接待策略不存在")
	}
	conversation, _, err := s.createWithProfile(externalUser, channelID, *aiAgent, createWelcome, nil)
	return conversation, err
}

func (s *conversationService) createWithProfile(
	externalUser openidentity.ExternalUser,
	channelID int64,
	aiAgent models.AIAgent,
	createWelcome bool,
	storeScope *StoreConversationScope,
) (*models.Conversation, bool, error) {
	if aiAgent.Status != enums.StatusOk {
		return nil, false, errorsx.InvalidParam("接待策略不存在")
	}
	var conversation *models.Conversation
	var welcomeMessage *models.Message
	created := false
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		channel := repositories.ChannelRepository.Get(ctx.Tx, channelID)
		if channel == nil || channel.Status != enums.StatusOk || channel.TenantID <= 0 {
			return errorsx.InvalidParam("接入渠道不存在、已停用或缺少租户归属")
		}
		if aiAgent.TenantID != channel.TenantID {
			return errorsx.InvalidParam("接待策略不属于接入渠道所在公司")
		}
		if storeScope != nil {
			if err := s.validateStoreConversationScope(ctx.Tx, channel.TenantID, *storeScope); err != nil {
				return err
			}
		}
		customerID, err := CustomerService.EnsureExternalCustomer(ctx, channel.TenantID, externalUser)
		if err != nil {
			return err
		}
		customerName := s.getCustomerName(ctx.Tx, customerID, channel.TenantID)
		var existing *models.Conversation
		var threadKey *string
		if storeScope != nil {
			value := buildStoreConversationThreadKey(channel.TenantID, storeScope.StoreID, customerID, storeScope.StoreStaffBindingID)
			threadKey = &value
			existing = repositories.ConversationRepository.Take(ctx.Tx, "tenant_id = ? AND thread_key = ?", channel.TenantID, value)
		} else {
			existing = s.getLatestNotFinishedByCustomerID(ctx.Tx, customerID, channel.TenantID)
		}
		if existing != nil {
			conversation = existing
			updates := map[string]any{}
			if customerName != "" && existing.CustomerName != customerName {
				updates["customer_name"] = customerName
				conversation.CustomerName = customerName
			}
			if storeScope != nil {
				if existing.StoreID != storeScope.StoreID || existing.StoreStaffBindingID != storeScope.StoreStaffBindingID {
					return errorsx.InvalidParam("门店会话线程范围不一致")
				}
				if existing.ChannelID != channelID {
					updates["channel_id"] = channelID
					conversation.ChannelID = channelID
				}
			}
			if len(updates) > 0 {
				updates["updated_at"] = time.Now()
				updates["update_user_name"] = "system"
				if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, existing.ID, channel.TenantID, updates); err != nil {
					return err
				}
			}
			return nil
		}
		created = true
		now := time.Now()
		conversation = &models.Conversation{
			TenantID:      channel.TenantID,
			ThreadKey:     threadKey,
			AIAgentID:     aiAgent.ID,
			ChannelID:     channelID,
			CustomerID:    customerID,
			CustomerName:  customerName,
			Status:        s.resolveInitialStatus(aiAgent.ServiceMode),
			ServiceMode:   aiAgent.ServiceMode,
			Priority:      0,
			LastMessageAt: now,
			LastActiveAt:  now,
			AuditFields:   utils.BuildAuditFields(nil),
		}
		if storeScope != nil {
			conversation.StoreID = storeScope.StoreID
			conversation.StoreStaffBindingID = storeScope.StoreStaffBindingID
		}
		if err := ctx.Tx.Create(conversation).Error; err != nil {
			return err
		}
		if err := ConversationParticipantService.CreateCustomerParticipant(ctx, conversation.ID, externalUser); err != nil {
			return err
		}
		if err := ConversationEventLogService.CreateEvent(ctx, conversation.ID, enums.IMEventTypeCreate, enums.IMSenderTypeCustomer, 0, "用户创建会话", ""); err != nil {
			return err
		}
		if createWelcome {
			welcomeMessage, err = MessageService.createAIWelcomeMessage(ctx, conversation, &aiAgent, now)
		}
		return err
	}); err != nil {
		return nil, false, err
	}
	if conversation == nil {
		return nil, false, errorsx.BusinessError(1, "创建会话失败")
	}
	if !created {
		return conversation, false, nil
	}

	// 推送会话创建事件
	WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationCreated)
	if welcomeMessage != nil {
		if updatedConversation := s.Get(conversation.ID); updatedConversation != nil {
			MessageService.publishCommittedMessage(updatedConversation, welcomeMessage)
		}
	}

	if aiAgent.ServiceMode == enums.IMConversationServiceModeHumanOnly {
		if _, err := ConversationHumanDispatchService.ApplyHumanOnlyCreate(conversation.ID, aiAgent); err != nil {
			return nil, false, err
		}
	}
	return s.Get(conversation.ID), true, nil
}

func (s *conversationService) validateStoreConversationScope(db *gorm.DB, tenantID int64, scope StoreConversationScope) error {
	if scope.StoreID <= 0 || scope.StoreStaffBindingID <= 0 {
		return errorsx.InvalidParam("企微员工号缺少门店或门店员工号绑定")
	}
	store := repositories.StoreRepository.GetInTenant(db, scope.StoreID, tenantID)
	if store == nil || store.Status != enums.StatusOk {
		return errorsx.InvalidParam("企微员工号所属门店不存在或已停用")
	}
	binding := repositories.StoreStaffBindingRepository.GetInTenant(db, scope.StoreStaffBindingID, tenantID)
	if binding == nil || binding.Status != enums.StatusOk || binding.StoreID != scope.StoreID {
		return errorsx.InvalidParam("企微员工号缺少有效门店员工号绑定")
	}
	return StoreStaffBindingService.validateBindingOwnerDB(db, binding)
}

func buildStoreConversationThreadKey(tenantID, storeID, customerID, storeStaffBindingID int64) string {
	return fmt.Sprintf("store:%d:%d:%d:%d", tenantID, storeID, customerID, storeStaffBindingID)
}

func (s *conversationService) AssignConversation(req request.AssignConversationRequest, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return errorsx.Forbidden("请先进入需要管理会话的接入公司")
	}
	if req.AssigneeID <= 0 {
		return errorsx.InvalidParam("目标客服不能为空")
	}
	if req.AssigneeID == operator.UserID && s.canSupervisorTakeoverPendingConversation(operator) {
		return s.takeoverPendingHumanConversation(req.ConversationID, req.Reason, operator)
	}
	conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), req.ConversationID, tenantID)
	if conversation == nil {
		return errorsx.InvalidParam("会话不存在")
	}
	if conversation.Status != enums.IMConversationStatusPending || conversation.CurrentAssigneeID > 0 {
		return errorsx.InvalidParam("只有待接入会话允许分配")
	}
	targetProfile, err := ConversationDispatchWorkbenchService.requireManageableTargetProfile(req.AssigneeID, conversation, operator)
	if err != nil {
		return err
	}
	return ConversationDispatchWorkbenchService.assignToProfile(
		conversation,
		*targetProfile,
		0,
		strings.TrimSpace(req.Reason),
		operator,
		enums.IMAssignmentTypeAssign,
		enums.AgentTeamDispatchModeManual,
		true,
	)
}

func (s *conversationService) canSupervisorTakeoverPendingConversation(operator *dto.AuthPrincipal) bool {
	if operator == nil {
		return false
	}
	return AgentTeamScopeService.IsAdmin(operator) || slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader)
}

func (s *conversationService) takeoverPendingHumanConversation(conversationID int64, reason string, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	for _, permissionCode := range []string{
		constants.PermissionConversationView.Code,
		constants.PermissionConversationSend.Code,
		constants.PermissionConversationAssign.Code,
	} {
		if !slices.Contains(operator.Permissions, permissionCode) {
			return errorsx.Forbidden("缺少会话查看、回复或分配权限")
		}
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return errorsx.Forbidden("请先进入需要管理会话的接入公司")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "主管接管待人工会话"
	}

	var assignedEvent events.ConversationAssignedEvent
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, err := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, conversationID, tenantID)
		if err != nil {
			return err
		}
		if conversation == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		if conversation.Status != enums.IMConversationStatusPending || conversation.CurrentAssigneeID > 0 {
			return errorsx.InvalidParam("当前会话已被接管或不在待人工状态")
		}
		route, err := repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
		if err != nil {
			return err
		}
		if route == nil || route.RouteStatus != enums.ConversationRouteStatusHQAgentDeskPending || !route.NeedHumanFollowUp {
			return errorsx.InvalidParam("只有等待总部人工处理的会话允许直接接管")
		}
		teamIDs := ConversationDispatchService.resolveDispatchTeamIDsDB(ctx.Tx, conversation, route)
		if len(teamIDs) != 1 {
			return errorsx.InvalidParam("当前会话未匹配唯一客服组，不能直接接管")
		}
		teams, err := AgentTeamScopeService.lockManageableTeamsDB(ctx.Tx, teamIDs, operator, "只能接管自己负责客服组的会话")
		if err != nil {
			return err
		}
		team := teams[teamIDs[0]]
		if team == nil || team.Status != enums.StatusOk {
			return errorsx.InvalidParam("会话所属客服组已停用")
		}
		user := repositories.UserRepository.Get(ctx.Tx, operator.UserID)
		if user == nil || user.Status != enums.StatusOk || user.DeletedAt != nil {
			return errorsx.Forbidden("当前账号已停用")
		}
		platformAccount := operator.IsPlatformAccount && user.TenantID == 0
		if user.TenantID != conversation.TenantID && !platformAccount {
			return errorsx.Forbidden("当前账号不属于该接入公司")
		}

		now := time.Now()
		result := ctx.Tx.Model(&models.Conversation{}).
			Where("id = ? AND tenant_id = ? AND status = ? AND current_assignee_id = ?", conversation.ID, conversation.TenantID, enums.IMConversationStatusPending, 0).
			Updates(map[string]any{
				"current_assignee_id": operator.UserID,
				"current_team_id":     team.ID,
				"status":              enums.IMConversationStatusActive,
				"update_user_id":      operator.UserID,
				"update_user_name":    operator.Username,
				"updated_at":          now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errorsx.InvalidParam("当前会话已被其他客服接管，请刷新后重试")
		}
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversation.ID, now); err != nil {
			return err
		}
		if err := ConversationAssignmentService.CreateAssignmentWithOptions(ctx, conversation.ID, conversation.CurrentAssigneeID, operator.UserID, enums.IMAssignmentTypeAssign, reason, operator, now, ConversationAssignmentOptions{
			DispatchMode:   enums.AgentTeamDispatchModeManual,
			WorkloadWeight: normalizedWorkloadWeight(conversation),
		}); err != nil {
			return err
		}
		if err := ConversationEventLogService.CreateEvent(ctx, conversation.ID, enums.IMEventTypeAssign, enums.IMSenderTypeAgent, operator.UserID, "主管已接管待人工会话", s.buildEventPayload(map[string]any{
			"fromStatus":     conversation.Status,
			"toStatus":       enums.IMConversationStatusActive,
			"fromAssigneeId": conversation.CurrentAssigneeID,
			"toAssigneeId":   operator.UserID,
			"toTeamId":       team.ID,
			"reason":         reason,
			"dispatchMode":   enums.AgentTeamDispatchModeManual,
			"selfTakeover":   true,
		})); err != nil {
			return err
		}
		if _, err := ConversationRouteService.enterHQAgentDeskServingWithDB(ctx.Tx, conversation.ID, "主管接管:"+reason, now); err != nil {
			return err
		}
		assignedEvent = events.ConversationAssignedEvent{
			ConversationID: conversation.ID,
			FromUserID:     conversation.CurrentAssigneeID,
			ToUserID:       operator.UserID,
			OperatorID:     operator.UserID,
			Reason:         reason,
			AssignType:     events.ConversationAssignTypeSelfTakeover,
		}
		return nil
	}); err != nil {
		return err
	}
	if conversation := s.Get(conversationID); conversation != nil {
		WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationAssigned)
	}
	if assignedEvent.ConversationID > 0 {
		eventbus.PublishAsync(context.Background(), assignedEvent)
	}
	return nil
}

func (s *conversationService) TakeoverAIServingConversation(conversationID int64, reason string, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if operator.UserID <= 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	var assignedEvent events.ConversationAssignedEvent
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, err := requireOperatorConversation(ctx.Tx, conversationID, operator)
		if err != nil {
			return err
		}
		if conversation.Status == enums.IMConversationStatusClosed {
			return errorsx.InvalidParam("会话已关闭")
		}
		if conversation.Status == enums.IMConversationStatusActive && conversation.CurrentAssigneeID == operator.UserID {
			return nil
		}
		if conversation.Status != enums.IMConversationStatusAIServing || conversation.CurrentAssigneeID != 0 {
			return errorsx.InvalidParam("当前会话不允许直接接管")
		}

		state := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(ctx.Tx, conversationID, conversation.TenantID)
		if state == nil || (state.RouteStatus != enums.ConversationRouteStatusAIServing && state.RouteStatus != enums.ConversationRouteStatusAIFallback) {
			return errorsx.InvalidParam("当前会话不处于 AI 接待状态")
		}
		if state.WxWorkInstanceID <= 0 {
			return errorsx.InvalidParam("当前会话未绑定员工号，不能直接关闭 AI 回复")
		}
		instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(ctx.Tx, state.WxWorkInstanceID, conversation.TenantID)
		if instance == nil || instance.AIReplyEnabled {
			return errorsx.InvalidParam("请先关闭当前员工号的 AI 回复")
		}

		now := time.Now()
		reason = strings.TrimSpace(reason)
		if reason == "" {
			reason = "关闭AI回复后网页端接管"
		}
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversationID, now); err != nil {
			return err
		}
		if err := ConversationAssignmentService.CreateAssignment(ctx, conversationID, conversation.CurrentAssigneeID, operator.UserID, enums.IMAssignmentTypeAssign, reason, operator, now); err != nil {
			return err
		}
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, conversationID, conversation.TenantID, map[string]any{
			"current_assignee_id": operator.UserID,
			"status":              enums.IMConversationStatusActive,
			"update_user_id":      operator.UserID,
			"update_user_name":    operator.Username,
			"updated_at":          now,
		}); err != nil {
			return err
		}
		if err := ConversationEventLogService.CreateEvent(ctx, conversationID, enums.IMEventTypeAssign, enums.IMSenderTypeAgent, operator.UserID, "关闭AI回复后接管会话", s.buildEventPayload(map[string]any{
			"fromStatus":     conversation.Status,
			"toStatus":       enums.IMConversationStatusActive,
			"fromAssigneeId": conversation.CurrentAssigneeID,
			"toAssigneeId":   operator.UserID,
			"reason":         reason,
		})); err != nil {
			return err
		}
		if _, err := ConversationRouteService.EnterHQAgentDeskServing(conversationID, reason, now); err != nil {
			return err
		}
		assignedEvent = events.ConversationAssignedEvent{
			ConversationID: conversationID,
			FromUserID:     conversation.CurrentAssigneeID,
			ToUserID:       operator.UserID,
			OperatorID:     operator.UserID,
			Reason:         reason,
			AssignType:     events.ConversationAssignTypeSelfTakeover,
		}
		return nil
	}); err != nil {
		return err
	}
	if conversation := s.Get(conversationID); conversation != nil {
		WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationAssigned)
	}
	if assignedEvent.ConversationID > 0 {
		eventbus.PublishAsync(context.Background(), assignedEvent)
	}
	return nil
}

func (s *conversationService) EnsureAgentCanReply(conversationID int64, reason string, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	conversation, err := requireOperatorConversation(sqls.DB(), conversationID, operator)
	if err != nil {
		return err
	}
	if conversation.Status == enums.IMConversationStatusClosed {
		return errorsx.InvalidParam("会话已关闭")
	}
	if !AgentTeamScopeService.CanViewConversation(operator, conversationID) {
		return errorsx.Forbidden("当前客服未绑定该门店或员工号，无法处理此会话")
	}
	if route := ConversationRouteService.GetByConversationIDInTenant(conversationID, conversation.TenantID); route != nil && route.RouteStatus == enums.ConversationRouteStatusStoreWecomManual {
		return nil
	}
	if conversation.Status == enums.IMConversationStatusActive && conversation.CurrentAssigneeID == operator.UserID {
		return nil
	}
	if (conversation.Status == enums.IMConversationStatusAIServing || conversation.Status == enums.IMConversationStatusPending) && conversation.CurrentAssigneeID == 0 {
		return s.TakeoverAIServingConversation(conversationID, reason, operator)
	}
	_, err = MessageService.ValidateConversationSender(conversationID, enums.IMSenderTypeAgent, operator, nil)
	return err
}

func (s *conversationService) TransferConversation(conversationID, toUserID int64, reason string, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if toUserID <= 0 {
		return errorsx.InvalidParam("目标客服不能为空")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return errorsx.Forbidden("请先进入需要管理会话的接入公司")
	}
	targetProfile := AgentProfileService.GetEnabledForAssignment(sqls.DB(), tenantID, toUserID)
	if targetProfile == nil {
		return errorsx.InvalidParam("目标客服不存在或账号已停用")
	}
	var assignedEvent events.ConversationAssignedEvent
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, err := requireOperatorConversation(ctx.Tx, conversationID, operator)
		if err != nil {
			return err
		}
		if !s.canTransferConversation(conversation, operator) {
			return errorsx.Forbidden("无权转接该会话")
		}
		if conversation.Status != enums.IMConversationStatusActive {
			return errorsx.InvalidParam("只有处理中会话允许转接")
		}
		if conversation.CurrentAssigneeID <= 0 {
			return errorsx.InvalidParam("当前会话未分配客服")
		}
		if conversation.CurrentAssigneeID == toUserID {
			return errorsx.InvalidParam("目标客服不能与当前指派人相同")
		}
		now := time.Now()
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversationID, now); err != nil {
			return err
		}
		if err := ConversationAssignmentService.CreateAssignment(ctx, conversationID, conversation.CurrentAssigneeID, toUserID, enums.IMAssignmentTypeTransfer, reason, operator, now); err != nil {
			return err
		}
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, conversationID, conversation.TenantID, map[string]any{
			"current_assignee_id": toUserID,
			"status":              enums.IMConversationStatusActive,
			"update_user_id":      operator.UserID,
			"update_user_name":    operator.Username,
			"updated_at":          now,
		}); err != nil {
			return err
		}
		if err := ConversationEventLogService.CreateEvent(ctx, conversationID, enums.IMEventTypeTransfer, enums.IMSenderTypeAgent, operator.UserID, "会话已转接", s.buildEventPayload(map[string]any{
			"fromStatus":     conversation.Status,
			"toStatus":       enums.IMConversationStatusActive,
			"fromAssigneeId": conversation.CurrentAssigneeID,
			"toAssigneeId":   toUserID,
			"reason":         strings.TrimSpace(reason),
		})); err != nil {
			return err
		}
		if _, err := ConversationRouteService.EnterHQAgentDeskServing(conversationID, "网页端总部客服转接:"+strings.TrimSpace(reason), now); err != nil {
			return err
		}
		assignedEvent = events.ConversationAssignedEvent{
			ConversationID: conversationID,
			FromUserID:     conversation.CurrentAssigneeID,
			ToUserID:       toUserID,
			OperatorID:     operator.UserID,
			Reason:         strings.TrimSpace(reason),
			AssignType:     events.ConversationAssignTypeTransfer,
		}
		return nil
	}); err != nil {
		return err
	}
	if conversation := s.Get(conversationID); conversation != nil {
		WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationTransferred)
	}
	eventbus.PublishAsync(context.Background(), assignedEvent)
	return nil
}

func (s *conversationService) HandoffByAI(conversationID int64, aiAgent models.AIAgent, reason string) error {
	return s.HandoffByAIWithRequestID(conversationID, aiAgent, reason, "")
}

func (s *conversationService) HandoffByAIWithRequestID(conversationID int64, aiAgent models.AIAgent, reason string, requestID string) error {
	if conversationID <= 0 {
		return errorsx.InvalidParam("会话不存在")
	}
	_, err := ConversationHumanDispatchService.HandoffByAIWithRequestID(conversationID, aiAgent, reason, requestID)
	if err != nil {
		slog.Warn("schedule-aware ai handoff failed",
			"requestId", requestID,
			"conversation_id", conversationID,
			"ai_agent_id", aiAgent.ID,
			"error", err)
	}
	return err
}

func (s *conversationService) TryOffHoursHandoffByAI(conversationID int64, aiAgent models.AIAgent, reason string) (bool, error) {
	return s.TryOffHoursHandoffByAIWithRequestID(conversationID, aiAgent, reason, "")
}

func (s *conversationService) TryOffHoursHandoffByAIWithRequestID(conversationID int64, aiAgent models.AIAgent, reason string, requestID string) (bool, error) {
	if conversationID <= 0 {
		return false, errorsx.InvalidParam("会话不存在")
	}
	handled, err := ConversationHumanDispatchService.TryOffHoursHandoffByAIWithRequestID(conversationID, aiAgent, reason, requestID)
	if err != nil {
		slog.Warn("off-hours ai handoff failed",
			"requestId", requestID,
			"conversation_id", conversationID,
			"ai_agent_id", aiAgent.ID,
			"error", err)
	}
	return handled, err
}

func (s *conversationService) CloseConversation(conversationID int64, closeReason string, operator *dto.AuthPrincipal) error {
	if _, err := requireOperatorConversation(sqls.DB(), conversationID, operator); err != nil {
		return err
	}
	return s.closeConversation(conversationID, enums.IMSenderTypeAgent, closeReason, operator)
}

func (s *conversationService) CloseCustomerConversation(conversationID int64, externalUser openidentity.ExternalUser) error {
	conversation := s.Get(conversationID)
	if conversation == nil {
		return errorsx.InvalidParam("会话不存在")
	}
	if !s.IsCustomerConversationOwner(conversation, externalUser) {
		return errorsx.Forbidden("无权访问该会话")
	}
	return s.closeConversation(conversationID, enums.IMSenderTypeCustomer, "", nil)
}

func (s *conversationService) closeConversation(conversationID int64, senderType enums.IMSenderType, closeReason string, operator *dto.AuthPrincipal) error {
	var closedAt time.Time
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, err := requireConversationParent(ctx.Tx, conversationID)
		if err != nil {
			return err
		}
		if conversation.Status == enums.IMConversationStatusClosed {
			return nil
		}
		if conversation.Status != enums.IMConversationStatusAIServing &&
			conversation.Status != enums.IMConversationStatusPending &&
			conversation.Status != enums.IMConversationStatusActive {
			return errorsx.InvalidParam("当前状态不允许关闭会话")
		}
		var (
			now          = time.Now()
			eventDesc    = "会话已关闭"
			operatorID   int64
			operatorName string
		)
		closeReason = strings.TrimSpace(closeReason)
		closedAt = now
		if senderType == enums.IMSenderTypeCustomer {
			eventDesc = "客户关闭会话"
		} else {
			if operator == nil {
				return errorsx.InvalidParam("无权限操作")
			}
			if closeReason == "" {
				return errorsx.InvalidParam("关闭原因不能为空")
			}
			if !s.canCloseConversation(conversation, operator) {
				return errorsx.Forbidden("无权关闭该会话")
			}
			operatorID = operator.UserID
			operatorName = operator.Nickname
		}
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversationID, now); err != nil {
			return err
		}
		if err := AIReplyTurnService.InterruptCurrentDB(ctx.Tx, conversation, 0, "conversation_closed"); err != nil {
			return err
		}
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, conversationID, conversation.TenantID, map[string]any{
			"status":           enums.IMConversationStatusClosed,
			"closed_at":        now,
			"closed_by":        operatorID,
			"close_reason":     closeReason,
			"update_user_id":   operatorID,
			"update_user_name": operatorName,
			"updated_at":       now,
		}); err != nil {
			return err
		}
		if route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(ctx.Tx, conversationID, conversation.TenantID); route != nil {
			if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, route.ID, conversation.TenantID, map[string]any{
				"route_status":         enums.ConversationRouteStatusClosed,
				"route_target":         "closed",
				"manual_expire_at":     nil,
				"need_human_follow_up": false,
				"handoff_reason":       closeReason,
				"updated_at":           now,
				"update_user_id":       operatorID,
				"update_user_name":     operatorName,
			}); err != nil {
				return err
			}
		}
		return ConversationEventLogService.CreateEvent(ctx, conversationID, enums.IMEventTypeClose, senderType, operatorID, eventDesc, s.buildEventPayload(map[string]any{
			"fromStatus":     conversation.Status,
			"toStatus":       enums.IMConversationStatusClosed,
			"fromAssigneeId": conversation.CurrentAssigneeID,
			"toAssigneeId":   conversation.CurrentAssigneeID,
			"closeReason":    closeReason,
		}))
	}); err != nil {
		return err
	}
	logServiceAnalyticsCaptureError("close", conversationID, ServiceAnalyticsCaptureService.RecordClose(conversationID, closedAt, closeReason))
	if conversation := s.Get(conversationID); conversation != nil {
		WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationClosed)
		ConversationDispatchService.ScheduleTeamDispatch(conversation.TenantID, conversation.CurrentTeamID)
	}
	return nil
}

// MarkAgentConversationReadToMessage 控制台客服将会话已读推进到指定消息。
func (s *conversationService) MarkAgentConversationReadToMessage(conversationID, messageID int64, operator *dto.AuthPrincipal) error {
	conversation, err := requireOperatorConversation(sqls.DB(), conversationID, operator)
	if err != nil {
		return err
	}
	if !AgentTeamScopeService.CanViewConversation(operator, conversationID) {
		return errorsx.Forbidden("无权访问该会话")
	}
	changed, err := s.markConversationReadWithActor(conversation, messageID, agentConversationReadActor{operator: operator})
	if err != nil {
		return err
	}
	if changed {
		if updated := s.Get(conversationID); updated != nil {
			WsService.PublishConversationChanged(updated, enums.IMRealtimeEventConversationRead)
		}
	}
	return nil
}

// MarkCustomerConversationReadToMessage IM 客户将会话已读推进到指定消息（需为会话归属外部身份）。
func (s *conversationService) MarkCustomerConversationReadToMessage(conversationID, messageID int64, external *openidentity.ExternalUser) error {
	if external == nil || strings.TrimSpace(external.ExternalID) == "" {
		return errorsx.Unauthorized("外部用户标识不能为空")
	}
	conversation := s.Get(conversationID)
	if conversation == nil {
		return errorsx.InvalidParam("会话不存在")
	}
	if !s.IsCustomerConversationOwner(conversation, *external) {
		return errorsx.Forbidden("无权访问该会话")
	}
	changed, err := s.markConversationReadWithActor(conversation, messageID, customerConversationReadActor{external: external})
	if err != nil {
		return err
	}
	if changed {
		if updated := s.Get(conversationID); updated != nil {
			WsService.PublishConversationChanged(updated, enums.IMRealtimeEventConversationRead)
		}
	}
	return nil
}

func displayExternalName(ext *openidentity.ExternalUser) string {
	if ext == nil {
		return ""
	}
	if n := strings.TrimSpace(ext.ExternalName); n != "" {
		return n
	}
	return strings.TrimSpace(ext.ExternalID)
}

// conversationReadActor 抽象「读者身份」，供 markConversationReadWithActor 共用（包内私有）。
type conversationReadActor interface {
	isAgentSide() bool
	getReadState(conversationID int64) *models.ConversationReadState
	markRead(ctx *sqls.TxContext, conversation *models.Conversation, targetMessage *models.Message) error
	conversationUpdateAudit() (userID int64, userName string)
}

type agentConversationReadActor struct {
	operator *dto.AuthPrincipal
}

func (a agentConversationReadActor) isAgentSide() bool { return true }

func (a agentConversationReadActor) getReadState(conversationID int64) *models.ConversationReadState {
	return ConversationReadStateService.GetByAgentReader(conversationID, a.operator)
}

func (a agentConversationReadActor) markRead(ctx *sqls.TxContext, conversation *models.Conversation, targetMessage *models.Message) error {
	_, err := ConversationReadStateService.MarkAgentRead(ctx, conversation, a.operator, targetMessage)
	return err
}

func (a agentConversationReadActor) conversationUpdateAudit() (int64, string) {
	if a.operator == nil {
		return 0, ""
	}
	return a.operator.UserID, a.operator.Username
}

type customerConversationReadActor struct {
	external *openidentity.ExternalUser
}

func (a customerConversationReadActor) isAgentSide() bool { return false }

func (a customerConversationReadActor) getReadState(conversationID int64) *models.ConversationReadState {
	return ConversationReadStateService.GetByCustomerReader(conversationID, a.external)
}

func (a customerConversationReadActor) markRead(ctx *sqls.TxContext, conversation *models.Conversation, targetMessage *models.Message) error {
	_, err := ConversationReadStateService.MarkCustomerRead(ctx, conversation, a.external, targetMessage)
	return err
}

func (a customerConversationReadActor) conversationUpdateAudit() (int64, string) {
	return 0, displayExternalName(a.external)
}

func (s *conversationService) markConversationReadWithActor(conversation *models.Conversation, messageID int64, actor conversationReadActor) (bool, error) {
	if conversation == nil {
		return false, errorsx.InvalidParam("会话不存在")
	}
	targetMessage, err := MessageService.GetConversationReadTarget(conversation.ID, messageID)
	if err != nil {
		return false, err
	}
	if targetMessage == nil {
		if actor.isAgentSide() && conversation.AgentUnreadCount == 0 {
			return false, nil
		}
		if !actor.isAgentSide() && conversation.CustomerUnreadCount == 0 {
			return false, nil
		}
		now := time.Now()
		updateUserID, updateUserName := actor.conversationUpdateAudit()
		updates := map[string]any{
			"update_user_id":   updateUserID,
			"update_user_name": updateUserName,
			"updated_at":       now,
		}
		if actor.isAgentSide() {
			updates["agent_unread_count"] = 0
		} else {
			updates["customer_unread_count"] = 0
		}
		return true, repositories.ConversationRepository.UpdatesInTenant(sqls.DB(), conversation.ID, conversation.TenantID, updates)
	}

	currentReadState := actor.getReadState(conversation.ID)
	if currentReadState != nil && currentReadState.LastReadSeqNo >= targetMessage.SeqNo {
		if actor.isAgentSide() && conversation.AgentUnreadCount == 0 {
			return false, nil
		}
		if !actor.isAgentSide() && conversation.CustomerUnreadCount == 0 {
			return false, nil
		}
	}

	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		currentConversation := repositories.ConversationRepository.GetInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
		if currentConversation == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		if err := actor.markRead(ctx, currentConversation, targetMessage); err != nil {
			return err
		}
		agentReadState, customerReadState := ConversationReadStateService.getConversationReadStates(ctx.Tx, currentConversation.ID)
		agentUnreadCount, err := s.countUnreadByState(ctx, currentConversation.ID, agentReadState, enums.IMSenderTypeCustomer)
		if err != nil {
			return err
		}
		customerUnreadCount, err := s.countUnreadByState(ctx, currentConversation.ID, customerReadState, enums.IMSenderTypeAgent, enums.IMSenderTypeAI)
		if err != nil {
			return err
		}
		if actor.isAgentSide() && currentConversation.AgentUnreadCount == agentUnreadCount && currentReadState != nil && currentReadState.LastReadSeqNo >= targetMessage.SeqNo {
			return nil
		}
		if !actor.isAgentSide() && currentConversation.CustomerUnreadCount == customerUnreadCount && currentReadState != nil && currentReadState.LastReadSeqNo >= targetMessage.SeqNo {
			return nil
		}
		updateUserID, updateUserName := actor.conversationUpdateAudit()
		return repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, currentConversation.ID, currentConversation.TenantID, map[string]any{
			"agent_unread_count":    agentUnreadCount,
			"customer_unread_count": customerUnreadCount,
			"update_user_id":        updateUserID,
			"update_user_name":      updateUserName,
			"updated_at":            time.Now(),
		})
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *conversationService) countUnreadByState(ctx *sqls.TxContext, conversationID int64, state *models.ConversationReadState, senderTypes ...enums.IMSenderType) (int, error) {
	lastReadSeqNo := int64(0)
	if state != nil {
		lastReadSeqNo = state.LastReadSeqNo
	}
	normalizedSenderTypes := make([]enums.IMSenderType, 0, len(senderTypes))
	for _, senderType := range senderTypes {
		normalizedSenderTypes = append(normalizedSenderTypes, senderType)
	}
	count, err := ConversationReadStateService.CountUnreadMessages(ctx, conversationID, lastReadSeqNo, normalizedSenderTypes...)
	return int(count), err
}

func (s *conversationService) IsCustomerConversationOwner(conversation *models.Conversation, externalUser openidentity.ExternalUser) bool {
	if conversation == nil {
		return false
	}
	extID := strings.TrimSpace(externalUser.ExternalID)
	if extID == "" || strings.TrimSpace(string(externalUser.ExternalSource)) == "" || conversation.CustomerID <= 0 {
		return false
	}
	channel := repositories.ChannelRepository.GetInTenant(sqls.DB(), conversation.ChannelID, conversation.TenantID)
	if channel == nil || channel.TenantID <= 0 || channel.TenantID != conversation.TenantID {
		return false
	}
	identity := repositories.CustomerIdentityRepository.GetByInTenant(sqls.DB(), conversation.TenantID, externalUser.ExternalSource, extID)
	if identity == nil {
		return false
	}
	return identity.CustomerID == conversation.CustomerID
}

func (s *conversationService) BuildConversationSummary(conversation *models.Conversation) string {
	if conversation == nil {
		return ""
	}
	if strings.TrimSpace(conversation.LastMessageSummary) != "" {
		return conversation.LastMessageSummary
	}
	return strings.TrimSpace(conversation.CustomerName)
}

func (s *conversationService) getCustomerName(db *gorm.DB, customerID, tenantID int64) string {
	if customerID <= 0 || tenantID <= 0 {
		return ""
	}
	if customer := repositories.CustomerRepository.GetInTenant(db, customerID, tenantID); customer != nil {
		return strings.TrimSpace(customer.Name)
	}
	return ""
}

func (s *conversationService) canCloseConversation(conversation *models.Conversation, operator *dto.AuthPrincipal) bool {
	if conversation == nil || operator == nil {
		return false
	}
	if s.isAdmin(operator) {
		return true
	}
	return conversation.Status == enums.IMConversationStatusActive && conversation.CurrentAssigneeID > 0 && conversation.CurrentAssigneeID == operator.UserID
}

func (s *conversationService) canTransferConversation(conversation *models.Conversation, operator *dto.AuthPrincipal) bool {
	if conversation == nil || operator == nil {
		return false
	}
	if s.isAdmin(operator) {
		return true
	}
	return conversation.Status == enums.IMConversationStatusActive &&
		conversation.CurrentAssigneeID > 0 &&
		conversation.CurrentAssigneeID == operator.UserID
}

func (s *conversationService) isAdmin(operator *dto.AuthPrincipal) bool {
	if operator == nil {
		return false
	}
	return slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) ||
		slices.Contains(operator.Roles, constants.RoleCodeAdmin) ||
		slices.Contains(operator.Roles, constants.RoleCodeTenantAdmin)
}

func (s *conversationService) buildEventPayload(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(data)
}

func (s *conversationService) InheritStoreConversation(req request.InheritStoreConversationRequest, operator *dto.AuthPrincipal, requestID string) (*models.Conversation, error) {
	if operator == nil || operator.UserID <= 0 {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	if req.ConversationID <= 0 || req.TargetStoreStaffBindingID <= 0 || req.TargetWxWorkInstanceID <= 0 {
		return nil, errorsx.InvalidParam("请选择会话、目标门店员工号和企微实例")
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return nil, errorsx.InvalidParam("请填写会话继承原因")
	}
	if len([]rune(reason)) > 255 {
		return nil, errorsx.InvalidParam("会话继承原因不能超过255个字符")
	}
	if !AgentTeamScopeService.CanViewConversation(operator, req.ConversationID) {
		return nil, errorsx.Forbidden("无权限安排该会话继承")
	}
	if !AgentTeamScopeService.CanViewWxWorkInstance(operator, req.TargetWxWorkInstanceID) {
		return nil, errorsx.Forbidden("目标企微实例超出当前数据范围")
	}

	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	current := repositories.ConversationRepository.GetInTenant(sqls.DB(), req.ConversationID, tenantID)
	if current == nil {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	resultConversationID := int64(0)
	affectedConversationIDs := map[int64]struct{}{req.ConversationID: {}}
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		_, target, err := s.lockStoreConversationInheritanceBindingsDB(
			ctx.Tx,
			tenantID,
			current.StoreStaffBindingID,
			req.TargetStoreStaffBindingID,
			req.TargetWxWorkInstanceID,
		)
		if err != nil {
			return err
		}
		conversation, err := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, req.ConversationID, tenantID)
		if err != nil {
			return err
		}
		if conversation == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		if conversation.StoreID != current.StoreID {
			return errorsx.InvalidParam("会话门店归属已变化，请刷新后重试")
		}
		if conversation.StoreStaffBindingID != current.StoreStaffBindingID {
			return errorsx.InvalidParam("会话门店员工号归属已变化，请刷新后重试")
		}
		inheritance, err := s.inheritStoreConversationDB(ctx, conversation, target, reason, operator, requestID)
		if err != nil {
			return err
		}
		resultConversationID = inheritance.ConversationID
		affectedConversationIDs[inheritance.ConversationID] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var updated *models.Conversation
	for conversationID := range affectedConversationIDs {
		item := repositories.ConversationRepository.GetInTenant(sqls.DB(), conversationID, tenantID)
		if item != nil {
			WsService.PublishConversationChanged(item, enums.IMRealtimeEventConversationUpdated)
			if conversationID == resultConversationID {
				updated = item
			}
		}
	}
	return updated, nil
}

// LinkConversationCustomer 将会话绑定到指定客户。
func (s *conversationService) LinkConversationCustomer(conversationID, customerID int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if conversationID <= 0 || customerID <= 0 {
		return errorsx.InvalidParam("参数不合法")
	}
	if !AgentTeamScopeService.CanViewConversation(operator, conversationID) {
		return errorsx.Forbidden("无权限关联该会话")
	}
	cust := CustomerService.GetInTenant(customerID, operator)
	if cust == nil || cust.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("客户不存在")
	}
	conv, err := requireOperatorConversation(sqls.DB(), conversationID, operator)
	if err != nil {
		return err
	}
	if conv.Status == enums.IMConversationStatusClosed {
		return errorsx.InvalidParam("已关闭的会话无法关联客户")
	}
	if !s.canLinkConversationCustomer(conv, operator) {
		return errorsx.Forbidden("无权限关联该会话")
	}

	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current, err := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, conversationID, conv.TenantID)
		if err != nil {
			return err
		}
		if current == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		channel := repositories.ChannelRepository.GetInTenant(ctx.Tx, current.ChannelID, current.TenantID)
		if channel == nil {
			return errorsx.InvalidParam("会话接入渠道不存在")
		}
		externalSource := externalSourceForChannelType(channel.ChannelType)
		if strings.TrimSpace(string(externalSource)) == "" {
			return errorsx.InvalidParam("当前渠道不支持人工关联客户身份")
		}
		participant, err := repositories.ConversationParticipantRepository.GetCustomerForUpdateInTenant(ctx.Tx, current.TenantID, current.ID)
		if err != nil {
			return err
		}
		externalID := ""
		if participant != nil {
			externalID = strings.TrimSpace(participant.ExternalParticipantID)
		}
		if externalID == "" {
			return errorsx.InvalidParam("会话缺少可核验的外部客户身份")
		}
		identity, err := repositories.CustomerIdentityRepository.GetByForUpdateInTenant(ctx.Tx, current.TenantID, externalSource, externalID)
		if err != nil {
			return err
		}
		if identity != nil && identity.CustomerID != current.CustomerID && identity.CustomerID != customerID {
			return errorsx.InvalidParam("该外部身份已关联其他客户，请先核对客户身份")
		}

		var threadKey *string
		if current.StoreID > 0 || current.StoreStaffBindingID > 0 || current.ThreadKey != nil {
			if current.StoreID <= 0 || current.StoreStaffBindingID <= 0 {
				return errorsx.InvalidParam("会话门店线程范围不完整")
			}
			value := buildStoreConversationThreadKey(current.TenantID, current.StoreID, customerID, current.StoreStaffBindingID)
			conflict, err := repositories.ConversationRepository.GetForUpdateByThreadKeyInTenant(ctx.Tx, current.TenantID, value)
			if err != nil {
				return err
			}
			if conflict != nil && conflict.ID != current.ID {
				return errorsx.InvalidParam("目标客户在当前门店员工号下已有独立会话，不能直接合并")
			}
			threadKey = &value
		}
		now := time.Now()
		updates := map[string]any{
			"customer_id":      customerID,
			"customer_name":    strings.TrimSpace(cust.Name),
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       now,
		}
		if threadKey != nil {
			updates["thread_key"] = *threadKey
		}
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, conversationID, conv.TenantID, updates); err != nil {
			return err
		}
		if participant != nil && participant.ParticipantID != customerID {
			if err := repositories.ConversationParticipantRepository.UpdatesInTenant(ctx.Tx, participant.ID, current.TenantID, map[string]any{
				"participant_id":   customerID,
				"updated_at":       now,
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
			}); err != nil {
				return err
			}
		}
		if identity == nil {
			if err := repositories.CustomerIdentityRepository.Create(ctx.Tx, &models.CustomerIdentity{
				TenantID:       current.TenantID,
				CustomerID:     customerID,
				ExternalSource: externalSource,
				ExternalID:     externalID,
				Status:         enums.StatusOk,
				AuditFields:    utils.BuildAuditFields(operator),
			}); err != nil {
				return err
			}
		} else if identity.CustomerID != customerID || identity.Status != enums.StatusOk {
			if err := repositories.CustomerIdentityRepository.UpdatesInTenant(ctx.Tx, identity.ID, current.TenantID, map[string]any{
				"customer_id":      customerID,
				"status":           enums.StatusOk,
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
				"updated_at":       now,
			}); err != nil {
				return err
			}
		}
		if current.StoreID > 0 {
			instanceID := int64(0)
			if route, routeErr := repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(ctx.Tx, current.ID, current.TenantID); routeErr != nil {
				return routeErr
			} else if route != nil {
				instanceID = route.WxWorkInstanceID
			}
			if err := s.moveStoreCustomerRelationDB(ctx.Tx, current, customerID, instanceID, now, operator); err != nil {
				return err
			}
		}
		return ConversationEventLogService.CreateEvent(
			ctx,
			current.ID,
			enums.IMEventTypeRouteChange,
			enums.IMSenderTypeAgent,
			operator.UserID,
			"人工确认会话客户身份关联",
			s.buildEventPayload(map[string]any{
				"mappingMode":        "operator_confirmed_cross_namespace",
				"previousCustomerId": current.CustomerID,
				"targetCustomerId":   customerID,
			}),
		)
	})
	if err != nil {
		return err
	}
	if updated := s.Get(conversationID); updated != nil {
		WsService.PublishConversationChanged(updated, enums.IMRealtimeEventConversationUpdated)
	}
	return nil
}

func (s *conversationService) GetConversationExternalIdentity(conversation *models.Conversation) *models.CustomerIdentity {
	if conversation == nil || conversation.CustomerID <= 0 {
		return nil
	}
	if channel := repositories.ChannelRepository.GetInTenant(sqls.DB(), conversation.ChannelID, conversation.TenantID); channel != nil {
		identities := repositories.CustomerIdentityRepository.FindByCustomerIDInTenant(sqls.DB(), conversation.CustomerID, channel.TenantID)
		if len(identities) == 0 {
			return nil
		}
		expected := externalSourceForChannelType(channel.ChannelType)
		if strings.TrimSpace(string(expected)) != "" {
			for i := range identities {
				if identities[i].ExternalSource == expected {
					return &identities[i]
				}
			}
		}
		return &identities[0]
	}
	return nil
}

func externalSourceForChannelType(channelType string) enums.ExternalSource {
	switch strings.TrimSpace(channelType) {
	case enums.ChannelTypeWxWorkKF:
		return enums.ExternalSourceWxWorkKF
	case enums.ChannelTypeWxWorkProtocol:
		return enums.ExternalSourceWxWorkProtocol
	case enums.ChannelTypeWeb:
		return enums.ExternalSourceGuest
	default:
		return ""
	}
}

func (s *conversationService) touchStoreCustomerRelationDB(db *gorm.DB, conversation *models.Conversation, customerID, wxWorkInstanceID int64, now time.Time, operator *dto.AuthPrincipal) error {
	if db == nil || conversation == nil || conversation.TenantID <= 0 || conversation.StoreID <= 0 || customerID <= 0 {
		return errorsx.InvalidParam("门店客户关系范围不完整")
	}
	relation, err := repositories.StoreCustomerRelationRepository.GetForUpdateByCustomerAndStoreInTenant(db, conversation.TenantID, customerID, conversation.StoreID)
	if err != nil {
		return err
	}
	if relation == nil {
		return repositories.StoreCustomerRelationRepository.Create(db, &models.StoreCustomerRelation{
			TenantID:           conversation.TenantID,
			CustomerID:         customerID,
			StoreID:            conversation.StoreID,
			WxWorkInstanceID:   wxWorkInstanceID,
			LastConversationID: conversation.ID,
			LastActiveAt:       &now,
			Status:             enums.StatusOk,
			AuditFields:        utils.BuildAuditFields(operator),
		})
	}
	return repositories.StoreCustomerRelationRepository.UpdatesInTenant(db, relation.ID, conversation.TenantID, map[string]any{
		"wx_work_instance_id":  wxWorkInstanceID,
		"last_conversation_id": conversation.ID,
		"last_active_at":       now,
		"status":               enums.StatusOk,
		"update_user_id":       auditUserID(operator),
		"update_user_name":     auditUsername(operator),
		"updated_at":           now,
	})
}

func (s *conversationService) moveStoreCustomerRelationDB(db *gorm.DB, conversation *models.Conversation, targetCustomerID, wxWorkInstanceID int64, now time.Time, operator *dto.AuthPrincipal) error {
	if conversation == nil || conversation.CustomerID == targetCustomerID {
		return s.touchStoreCustomerRelationDB(db, conversation, targetCustomerID, wxWorkInstanceID, now, operator)
	}
	customerIDs := []int64{conversation.CustomerID, targetCustomerID}
	if customerIDs[0] > customerIDs[1] {
		customerIDs[0], customerIDs[1] = customerIDs[1], customerIDs[0]
	}
	locked := make(map[int64]*models.StoreCustomerRelation, 2)
	for _, customerID := range customerIDs {
		relation, err := repositories.StoreCustomerRelationRepository.GetForUpdateByCustomerAndStoreInTenant(db, conversation.TenantID, customerID, conversation.StoreID)
		if err != nil {
			return err
		}
		locked[customerID] = relation
	}
	source := locked[conversation.CustomerID]
	target := locked[targetCustomerID]
	if target == nil && source != nil {
		return repositories.StoreCustomerRelationRepository.UpdatesInTenant(db, source.ID, conversation.TenantID, map[string]any{
			"customer_id":          targetCustomerID,
			"wx_work_instance_id":  wxWorkInstanceID,
			"last_conversation_id": conversation.ID,
			"last_active_at":       now,
			"status":               enums.StatusOk,
			"update_user_id":       auditUserID(operator),
			"update_user_name":     auditUsername(operator),
			"updated_at":           now,
		})
	}
	return s.touchStoreCustomerRelationDB(db, conversation, targetCustomerID, wxWorkInstanceID, now, operator)
}

func (s *conversationService) canLinkConversationCustomer(conv *models.Conversation, operator *dto.AuthPrincipal) bool {
	if conv == nil || operator == nil {
		return false
	}
	if s.isAdmin(operator) {
		return true
	}
	switch conv.Status {
	case enums.IMConversationStatusAIServing:
		return true
	case enums.IMConversationStatusPending:
		return true
	case enums.IMConversationStatusActive:
		return conv.CurrentAssigneeID == 0 || conv.CurrentAssigneeID == operator.UserID
	default:
		return false
	}
}

func (s *conversationService) resolveInitialStatus(serviceMode enums.IMConversationServiceMode) enums.IMConversationStatus {
	switch serviceMode {
	case enums.IMConversationServiceModeHumanOnly:
		return enums.IMConversationStatusPending
	case enums.IMConversationServiceModeAIOnly, enums.IMConversationServiceModeAIFirst:
		return enums.IMConversationStatusAIServing
	default:
		return enums.IMConversationStatusAIServing
	}
}
