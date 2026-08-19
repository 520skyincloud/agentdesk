package services

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/events"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/eventbus"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const conversationTakeoverNotificationType = "conversation_takeover_request"

var ConversationTakeoverService = &conversationTakeoverService{}

type conversationTakeoverService struct{}

type conversationTakeoverScope struct {
	conversation *models.Conversation
	route        *models.ConversationRouteState
	team         *models.AgentTeam
}

func (s *conversationTakeoverService) ResolveState(conversation *models.Conversation, operator *dto.AuthPrincipal) response.ConversationTakeoverStateResponse {
	state := response.ConversationTakeoverStateResponse{}
	if conversation == nil || operator == nil || operator.UserID <= 0 || conversation.TenantID != AgentTeamScopeService.ActiveTenantID(operator) {
		return state
	}
	db := sqls.DB()
	if db == nil {
		return state
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, conversation.TenantID)
	if route == nil {
		return state
	}
	state.IsCurrentAssignee = conversation.Status == enums.IMConversationStatusActive && conversation.CurrentAssigneeID == operator.UserID
	state.CanReply = state.IsCurrentAssignee && s.routeAllowsWebReply(route.RouteStatus) && s.hasPermission(operator, constants.PermissionConversationSend.Code)
	state.CanResumeAI = s.canResumeAIDB(db, conversation, route, operator)

	scope, err := s.resolveScopeDB(db, conversation, route)
	if err != nil {
		return state
	}
	state.TeamID = scope.team.ID
	state.TeamName = utils.RepairMojibakeText(scope.team.Name)
	state.CanDirectTakeover = !state.IsCurrentAssignee &&
		conversation.CurrentAssigneeID == 0 &&
		s.directTakeoverRouteAllowed(conversation, route) &&
		s.hasPermissions(operator,
			constants.PermissionConversationView.Code,
			constants.PermissionConversationSend.Code,
			constants.PermissionConversationAssign.Code,
		) && s.canManageTeamDB(operator, scope.team)
	activeRequest := s.findActiveDB(db, conversation.TenantID, conversation.ID, route.SessionNo, false)
	if activeRequest != nil {
		s.applyRequestState(&state, activeRequest, operator)
		state.CanDirectTakeover = false
		state.CanReview = activeRequest.Status == enums.ConversationTakeoverRequestStatusPending &&
			s.hasPermission(operator, constants.PermissionConversationAssign.Code) && s.canManageTeamDB(operator, scope.team)
		return state
	}
	state.CanRequest = !state.IsCurrentAssignee &&
		conversation.CurrentAssigneeID == 0 &&
		s.hasPermission(operator, constants.PermissionConversationSend.Code) &&
		s.canRequestTakeoverDB(db, scope, operator) == nil &&
		!state.CanDirectTakeover
	return state
}

func (s *conversationTakeoverService) Request(req request.RequestConversationTakeoverRequest, operator *dto.AuthPrincipal) (*models.ConversationTakeoverRequest, error) {
	if operator == nil || operator.UserID <= 0 {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理会话的接入公司")
	}
	if !s.hasPermissions(operator, constants.PermissionConversationView.Code, constants.PermissionConversationSend.Code) {
		return nil, errorsx.Forbidden("缺少会话查看或回复权限")
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "申请主动接管会话"
	}
	if len([]rune(reason)) > 500 {
		return nil, errorsx.InvalidParam("接管原因不能超过500个字符")
	}

	var result *models.ConversationTakeoverRequest
	created := false
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, route, err := s.lockConversationRouteDB(ctx.Tx, req.ConversationID, tenantID)
		if err != nil {
			return err
		}
		if conversation.Status == enums.IMConversationStatusClosed || route.RouteStatus == enums.ConversationRouteStatusClosed {
			return errorsx.InvalidParam("会话已关闭")
		}
		if conversation.Status == enums.IMConversationStatusActive && conversation.CurrentAssigneeID == operator.UserID {
			return errorsx.InvalidParam("当前会话已由你接待")
		}
		if conversation.CurrentAssigneeID > 0 {
			return errorsx.InvalidParam("当前会话已由其他客服接待，请使用转派流程")
		}
		scope, err := s.resolveScopeDB(ctx.Tx, conversation, route)
		if err != nil {
			return err
		}
		if s.canManageTeamDB(operator, scope.team) && s.hasPermission(operator, constants.PermissionConversationAssign.Code) {
			return errorsx.InvalidParam("客服组长及以上请使用直接接管")
		}
		if err := s.canRequestTakeoverDB(ctx.Tx, scope, operator); err != nil {
			return err
		}
		if pending := s.findActiveDB(ctx.Tx, tenantID, conversation.ID, route.SessionNo, true); pending != nil {
			if pending.RequesterUserID == operator.UserID {
				result = pending
				return nil
			}
			return errorsx.InvalidParam("当前会话已有待审批的接管申请")
		}

		now := time.Now()
		activeKey := fmt.Sprintf("%d:%d:%d", tenantID, conversation.ID, route.SessionNo)
		item := &models.ConversationTakeoverRequest{
			TenantID:          tenantID,
			ConversationID:    conversation.ID,
			SessionNo:         route.SessionNo,
			TeamID:            scope.team.ID,
			RequesterUserID:   operator.UserID,
			RequesterName:     s.operatorName(operator),
			SourceAssigneeID:  conversation.CurrentAssigneeID,
			SourceRouteStatus: route.RouteStatus,
			Reason:            reason,
			Status:            enums.ConversationTakeoverRequestStatusPending,
			ActiveKey:         &activeKey,
			AuditFields: models.AuditFields{
				CreatedAt:      now,
				CreateUserID:   operator.UserID,
				CreateUserName: operator.Username,
				UpdatedAt:      now,
				UpdateUserID:   operator.UserID,
				UpdateUserName: operator.Username,
			},
		}
		if err := repositories.ConversationTakeoverRequestRepository.Create(ctx.Tx, item); err != nil {
			return err
		}
		if err := ConversationEventLogService.CreateEvent(ctx, conversation.ID, enums.IMEventTypeAssign, enums.IMSenderTypeAgent, operator.UserID, "已提交主动接管申请", ConversationService.buildEventPayload(map[string]any{
			"requestId": item.ID,
			"teamId":    scope.team.ID,
			"sessionNo": route.SessionNo,
			"reason":    reason,
		})); err != nil {
			return err
		}
		result = item
		created = true
		return nil
	})
	if err != nil {
		if !isDuplicateKeyError(err) {
			return nil, err
		}
		route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), req.ConversationID, tenantID)
		if route != nil {
			if pending := s.findActiveDB(sqls.DB(), tenantID, req.ConversationID, route.SessionNo, false); pending != nil {
				if pending.RequesterUserID == operator.UserID {
					return pending, nil
				}
				return nil, errorsx.InvalidParam("当前会话已有待审批的接管申请")
			}
		}
		return nil, err
	}
	if created && result != nil {
		s.notifyReviewer(result)
	}
	if conversation := ConversationService.GetByTenantID(req.ConversationID, tenantID); conversation != nil {
		WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationUpdated)
	}
	return result, nil
}

func (s *conversationTakeoverService) DirectTakeover(req request.RequestConversationTakeoverRequest, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return errorsx.Forbidden("请先进入需要管理会话的接入公司")
	}
	if !s.hasPermissions(operator,
		constants.PermissionConversationView.Code,
		constants.PermissionConversationSend.Code,
		constants.PermissionConversationAssign.Code,
	) {
		return errorsx.Forbidden("缺少会话查看、回复或分配权限")
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "主管主动接管会话"
	}
	if len([]rune(reason)) > 500 {
		return errorsx.InvalidParam("接管原因不能超过500个字符")
	}

	var assignedEvent events.ConversationAssignedEvent
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, route, err := s.lockConversationRouteDB(ctx.Tx, req.ConversationID, tenantID)
		if err != nil {
			return err
		}
		if conversation.Status == enums.IMConversationStatusClosed || route.RouteStatus == enums.ConversationRouteStatusClosed {
			return errorsx.InvalidParam("会话已关闭")
		}
		if conversation.Status == enums.IMConversationStatusActive && conversation.CurrentAssigneeID == operator.UserID {
			return nil
		}
		if conversation.CurrentAssigneeID != 0 {
			return errorsx.InvalidParam("只有尚未分配的开放会话允许直接接管，已分配会话请使用转派")
		}
		if !s.directTakeoverRouteAllowed(conversation, route) {
			return errorsx.InvalidParam("当前会话状态不允许直接接管")
		}
		scope, err := s.resolveScopeDB(ctx.Tx, conversation, route)
		if err != nil {
			return err
		}
		if !s.canManageTeamDB(operator, scope.team) {
			return errorsx.Forbidden("只能接管自己负责客服组的会话")
		}
		return s.takeoverDB(ctx, scope, operator.UserID, operator, reason, 0, &assignedEvent)
	})
	if err != nil {
		return err
	}
	s.publishTakeover(req.ConversationID, assignedEvent)
	return nil
}

// ActivateAuthorizedTakeover consumes the requester's approved authorization
// only after the requester confirms the same conversation switch a second time.
func (s *conversationTakeoverService) ActivateAuthorizedTakeover(req request.RequestConversationTakeoverRequest, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return errorsx.Forbidden("请先进入需要管理会话的接入公司")
	}
	if !s.hasPermissions(operator,
		constants.PermissionConversationView.Code,
		constants.PermissionConversationSend.Code,
	) {
		return errorsx.Forbidden("缺少会话查看或回复权限")
	}
	if req.RequestID <= 0 {
		return errorsx.InvalidParam("接管申请不能为空")
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "确认接管会话"
	}
	if len([]rune(reason)) > 500 {
		return errorsx.InvalidParam("接管原因不能超过500个字符")
	}

	itemPreview, err := repositories.ConversationTakeoverRequestRepository.GetInTenant(sqls.DB(), req.RequestID, tenantID)
	if err != nil {
		return err
	}
	if itemPreview == nil {
		return errorsx.InvalidParam("接管申请不存在")
	}
	conversationID := itemPreview.ConversationID
	var assignedEvent events.ConversationAssignedEvent
	var staleErr error
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, route, err := s.lockConversationRouteDB(ctx.Tx, itemPreview.ConversationID, tenantID)
		if err != nil {
			return err
		}
		item, err := repositories.ConversationTakeoverRequestRepository.GetForUpdateInTenant(ctx.Tx, req.RequestID, tenantID)
		if err != nil {
			return err
		}
		if item == nil {
			return errorsx.InvalidParam("接管申请不存在")
		}
		if item.RequesterUserID != operator.UserID {
			return errorsx.Forbidden("只有申请人可以确认接管")
		}
		if item.Status == enums.ConversationTakeoverRequestStatusApproved && conversation.CurrentAssigneeID == operator.UserID {
			return nil
		}
		if item.Status != enums.ConversationTakeoverRequestStatusAuthorized {
			return errorsx.InvalidParam("接管申请尚未获批或已失效")
		}
		if conversation.Status == enums.IMConversationStatusClosed || route.RouteStatus == enums.ConversationRouteStatusClosed {
			if err := s.cancelStaleRequestDB(ctx.Tx, item, operator, "conversation_closed"); err != nil {
				return err
			}
			staleErr = errorsx.InvalidParam("会话已关闭，接管申请已自动取消")
			return nil
		}
		if route.SessionNo != item.SessionNo {
			if err := s.cancelStaleRequestDB(ctx.Tx, item, operator, "session_changed"); err != nil {
				return err
			}
			staleErr = errorsx.InvalidParam("会话服务段已变化，接管申请已自动取消")
			return nil
		}
		if conversation.CurrentAssigneeID != item.SourceAssigneeID || route.RouteStatus != item.SourceRouteStatus || conversation.CurrentAssigneeID != 0 {
			if err := s.cancelStaleRequestDB(ctx.Tx, item, operator, "conversation_changed"); err != nil {
				return err
			}
			staleErr = errorsx.InvalidParam("会话接待状态已变化，接管申请已自动取消")
			return nil
		}
		scope, err := s.resolveScopeDB(ctx.Tx, conversation, route)
		if err != nil {
			return err
		}
		if err := s.canRequestTakeoverDB(ctx.Tx, scope, operator); err != nil {
			return err
		}
		if err := s.takeoverDB(ctx, scope, operator.UserID, operator, reason, item.ID, &assignedEvent); err != nil {
			return err
		}
		now := time.Now()
		return repositories.ConversationTakeoverRequestRepository.UpdatesInTenant(ctx.Tx, item.ID, tenantID, map[string]any{
			"status":           enums.ConversationTakeoverRequestStatusApproved,
			"terminal_reason":  "approved",
			"active_key":       nil,
			"updated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": s.operatorName(operator),
		})
	})
	if err != nil {
		return err
	}
	if staleErr != nil {
		return staleErr
	}
	s.publishTakeover(conversationID, assignedEvent)
	return nil
}

func (s *conversationTakeoverService) Review(req request.ReviewConversationTakeoverRequest, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return errorsx.Forbidden("请先进入需要管理会话的接入公司")
	}
	if !s.hasPermissions(operator, constants.PermissionConversationView.Code, constants.PermissionConversationAssign.Code) {
		return errorsx.Forbidden("缺少会话查看或分配权限")
	}
	remark := strings.TrimSpace(req.Remark)
	if len([]rune(remark)) > 500 {
		return errorsx.InvalidParam("审核备注不能超过500个字符")
	}

	var assignedEvent events.ConversationAssignedEvent
	var reviewed *models.ConversationTakeoverRequest
	var staleErr error
	preview, err := repositories.ConversationTakeoverRequestRepository.GetInTenant(sqls.DB(), req.RequestID, tenantID)
	if err != nil {
		return err
	}
	if preview == nil {
		return errorsx.InvalidParam("接管申请不存在")
	}
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, route, err := s.lockConversationRouteDB(ctx.Tx, preview.ConversationID, tenantID)
		if err != nil {
			return err
		}
		item, err := repositories.ConversationTakeoverRequestRepository.GetForUpdateInTenant(ctx.Tx, req.RequestID, tenantID)
		if err != nil {
			return err
		}
		if item == nil {
			return errorsx.InvalidParam("接管申请不存在")
		}
		if item.Status != enums.ConversationTakeoverRequestStatusPending {
			return errorsx.InvalidParam("接管申请已处理")
		}
		if item.ConversationID != conversation.ID {
			return errorsx.InvalidParam("接管申请关联会话已变化")
		}
		if conversation.Status == enums.IMConversationStatusClosed || route.RouteStatus == enums.ConversationRouteStatusClosed {
			if err := s.cancelStaleRequestDB(ctx.Tx, item, operator, "conversation_closed"); err != nil {
				return err
			}
			staleErr = errorsx.InvalidParam("会话已关闭，接管申请已自动取消")
			return nil
		}
		if route.SessionNo != item.SessionNo {
			if err := s.cancelStaleRequestDB(ctx.Tx, item, operator, "session_changed"); err != nil {
				return err
			}
			staleErr = errorsx.InvalidParam("会话服务段已变化，接管申请已自动取消")
			return nil
		}
		scope, err := s.resolveScopeDB(ctx.Tx, conversation, route)
		if err != nil {
			return err
		}
		if scope.team.ID != item.TeamID || !s.canManageTeamDB(operator, scope.team) {
			return errorsx.Forbidden("只能审核自己负责客服组的接管申请")
		}
		if conversation.CurrentAssigneeID != item.SourceAssigneeID || route.RouteStatus != item.SourceRouteStatus {
			if err := s.cancelStaleRequestDB(ctx.Tx, item, operator, "conversation_changed"); err != nil {
				return err
			}
			staleErr = errorsx.InvalidParam("会话接待状态已变化，接管申请已自动取消")
			return nil
		}

		now := time.Now()
		status := enums.ConversationTakeoverRequestStatusRejected
		terminalReason := "rejected"
		if req.Approved {
			requester := &dto.AuthPrincipal{
				UserID:         item.RequesterUserID,
				Username:       item.RequesterName,
				ActiveTenantID: tenantID,
			}
			if user := repositories.UserRepository.GetInTenant(ctx.Tx, item.RequesterUserID, tenantID); user == nil || user.Status != enums.StatusOk || user.DeletedAt != nil {
				return errorsx.InvalidParam("申请人账号已停用")
			}
			requester.Roles = s.userRoleCodesDB(ctx.Tx, item.RequesterUserID)
			requester.Permissions = s.userPermissionCodesDB(ctx.Tx, item.RequesterUserID)
			if !s.hasPermissions(requester, constants.PermissionConversationView.Code, constants.PermissionConversationSend.Code) {
				return errorsx.Forbidden("申请人已失去会话查看或回复权限")
			}
			if err := s.canRequestTakeoverDB(ctx.Tx, scope, requester); err != nil {
				return err
			}
			status = enums.ConversationTakeoverRequestStatusAuthorized
			terminalReason = "authorized"
		}
		activeKey := item.ActiveKey
		if req.Approved && activeKey == nil {
			key := fmt.Sprintf("%d:%d:%d", item.TenantID, item.ConversationID, item.SessionNo)
			activeKey = &key
		}
		if err := repositories.ConversationTakeoverRequestRepository.UpdatesInTenant(ctx.Tx, item.ID, tenantID, map[string]any{
			"status":           status,
			"reviewer_user_id": operator.UserID,
			"reviewer_name":    s.operatorName(operator),
			"review_remark":    remark,
			"reviewed_at":      now,
			"terminal_reason":  terminalReason,
			"active_key": func() any {
				if req.Approved {
					return activeKey
				}
				return nil
			}(),
			"updated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		if req.Approved {
			if err := ConversationEventLogService.CreateEvent(ctx, conversation.ID, enums.IMEventTypeAssign, enums.IMSenderTypeAgent, operator.UserID, "主动接管申请已通过，等待申请人确认", ConversationService.buildEventPayload(map[string]any{
				"requestId": item.ID,
				"sessionNo": item.SessionNo,
				"status":    string(status),
			})); err != nil {
				return err
			}
		} else {
			if err := ConversationEventLogService.CreateEvent(ctx, conversation.ID, enums.IMEventTypeAssign, enums.IMSenderTypeAgent, operator.UserID, "主动接管申请已拒绝", ConversationService.buildEventPayload(map[string]any{
				"requestId": item.ID,
				"remark":    remark,
			})); err != nil {
				return err
			}
		}
		copy := *item
		copy.Status = status
		copy.ReviewerUserID = operator.UserID
		copy.ReviewerName = s.operatorName(operator)
		copy.ReviewRemark = remark
		copy.ReviewedAt = &now
		copy.TerminalReason = terminalReason
		copy.ActiveKey = activeKey
		if !req.Approved {
			copy.ActiveKey = nil
		}
		reviewed = &copy
		return nil
	})
	if err != nil {
		return err
	}
	if staleErr != nil {
		return staleErr
	}
	if reviewed != nil {
		s.notifyRequester(reviewed, req.Approved)
	}
	s.publishTakeover(func() int64 {
		if reviewed != nil {
			return reviewed.ConversationID
		}
		return 0
	}(), assignedEvent)
	return nil
}

func (s *conversationTakeoverService) ResumeAI(conversationID int64, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return errorsx.Forbidden("请先进入需要管理会话的接入公司")
	}
	if !s.hasPermission(operator, constants.PermissionConversationView.Code) {
		return errorsx.Forbidden("缺少会话查看权限")
	}
	changed := false
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, route, err := s.lockConversationRouteDB(ctx.Tx, conversationID, tenantID)
		if err != nil {
			return err
		}
		decision := ConversationRuntimeModeService.ResolveDB(ctx.Tx, conversation, route)
		if decision.Mode == enums.ConversationRuntimeModeClosed {
			return errorsx.InvalidParam("会话已关闭")
		}
		if conversation.ServiceMode == enums.IMConversationServiceModeHumanOnly {
			return errorsx.InvalidParam("仅人工服务会话不能交还AI")
		}
		if decision.AIReplyAllowed {
			return nil
		}
		if decision.Mode != enums.ConversationRuntimeModeHumanActive &&
			decision.Mode != enums.ConversationRuntimeModeHumanPending &&
			decision.Mode != enums.ConversationRuntimeModeResumePending {
			return errorsx.InvalidParam("当前会话状态不能交还AI")
		}
		isAssignee := conversation.Status == enums.IMConversationStatusActive && conversation.CurrentAssigneeID == operator.UserID &&
			s.hasPermission(operator, constants.PermissionConversationSend.Code)
		if !isAssignee {
			return errorsx.Forbidden("只有当前接待人可以交还AI")
		}
		if !s.aiReplyEnabledDB(ctx.Tx, route, tenantID) {
			return errorsx.InvalidParam("当前员工号AI回复已关闭，暂不能交还AI")
		}
		changed, err = ConversationRouteService.restoreAIWithFollowUpDB(ctx, conversation, route, "人工主动结束接待，恢复AI", time.Now(), false, operator, "manual_resume")
		return err
	})
	if err != nil {
		return err
	}
	if changed {
		AIManualResumeTaskService.CancelActive(conversationID, "manual resume AI")
		if conversation := ConversationService.GetByTenantID(conversationID, tenantID); conversation != nil {
			WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationUpdated)
		}
	}
	return nil
}

func (s *conversationTakeoverService) EnsureCanReply(conversationID int64, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	conversation, err := requireOperatorConversation(sqls.DB(), conversationID, operator)
	if err != nil {
		return err
	}
	if !AgentTeamScopeService.CanViewConversation(operator, conversationID) {
		return errorsx.Forbidden("当前账号无权处理此会话")
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, conversation.TenantID)
	return s.ensureCanReplyDB(conversation, route, operator)
}

func (s *conversationTakeoverService) EnsureCanInviteRoomMember(conversationID, instanceID int64, roomID string, operator *dto.AuthPrincipal) error {
	if conversationID <= 0 || instanceID <= 0 {
		return errorsx.InvalidParam("会话和企微员工号实例不能为空")
	}
	roomID = normalizeConversationRoomID(roomID)
	if roomID == "" {
		return errorsx.InvalidParam("群ID不能为空")
	}
	if err := s.EnsureCanReply(conversationID, operator); err != nil {
		return err
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), conversationID, tenantID)
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversationID, tenantID)
	if conversation == nil || route == nil || route.WxWorkInstanceID != instanceID {
		return errorsx.Forbidden("当前群邀请不属于该会话绑定的企微员工号")
	}
	instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(sqls.DB(), instanceID, tenantID)
	if instance == nil || instance.ChannelID <= 0 ||
		(route.StoreID > 0 && instance.StoreID != route.StoreID) ||
		(route.StoreStaffBindingID > 0 && instance.StoreStaffBindingID != route.StoreStaffBindingID) {
		return errorsx.Forbidden("当前会话与企微员工号范围不一致")
	}
	mapping := repositories.WxWorkKFConversationRepository.FindOne(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("conversation_id", conversation.ID).
		Eq("channel_id", instance.ChannelID).
		Eq("status", enums.StatusOk))
	if mapping == nil || !isConversationRoomMapping(mapping) || normalizeConversationRoomID(mapping.ExternalUserID) != roomID {
		return errorsx.Forbidden("群ID与当前会话不一致")
	}
	return nil
}

func (s *conversationTakeoverService) lockAndEnsureCanReplyDB(db *gorm.DB, conversationID, tenantID int64, operator *dto.AuthPrincipal) (*models.Conversation, *models.ConversationRouteState, error) {
	conversation, route, err := s.lockConversationRouteDB(db, conversationID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	if err := s.ensureCanReplyDB(conversation, route, operator); err != nil {
		return nil, nil, err
	}
	return conversation, route, nil
}

func (s *conversationTakeoverService) ensureCanReplyDB(conversation *models.Conversation, route *models.ConversationRouteState, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if !s.hasPermission(operator, constants.PermissionConversationSend.Code) {
		return errorsx.Forbidden("缺少会话回复权限")
	}
	if conversation == nil || conversation.TenantID != AgentTeamScopeService.ActiveTenantID(operator) {
		return errorsx.Forbidden("当前账号无权处理此会话")
	}
	if conversation.Status == enums.IMConversationStatusClosed {
		return errorsx.InvalidParam("会话已关闭")
	}
	if conversation.Status != enums.IMConversationStatusActive || conversation.CurrentAssigneeID <= 0 {
		return errorsx.Forbidden("请先完成接管审批后再回复")
	}
	if conversation.CurrentAssigneeID != operator.UserID {
		return errorsx.Forbidden("当前会话已由其他客服接待")
	}
	if route == nil || route.TenantID != conversation.TenantID || route.ConversationID != conversation.ID || !s.routeAllowsWebReply(route.RouteStatus) {
		return errorsx.Forbidden("当前会话不处于网页人工接待状态")
	}
	return nil
}

func (s *conversationTakeoverService) takeoverDB(ctx *sqls.TxContext, scope conversationTakeoverScope, assigneeID int64, operator *dto.AuthPrincipal, reason string, requestID int64, assignedEvent *events.ConversationAssignedEvent) error {
	if ctx == nil || scope.conversation == nil || scope.route == nil || scope.team == nil || assigneeID <= 0 || operator == nil {
		return errorsx.InvalidParam("接管上下文无效")
	}
	conversation := scope.conversation
	route := scope.route
	now := time.Now()
	user := repositories.UserRepository.Get(ctx.Tx, assigneeID)
	platformAssignee := operator.IsPlatformAccount && operator.UserID == assigneeID && user != nil && user.TenantID == 0
	if user == nil || user.Status != enums.StatusOk || user.DeletedAt != nil || (user.TenantID != conversation.TenantID && !platformAssignee) {
		return errorsx.InvalidParam("接管人账号不存在或已停用")
	}
	if err := AIReplyTurnService.InterruptCurrentDB(ctx.Tx, conversation, route.SessionNo, "manual_takeover"); err != nil {
		return err
	}
	if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversation.ID, now); err != nil {
		return err
	}
	if err := ConversationAssignmentService.CreateAssignmentWithOptions(ctx, conversation.ID, conversation.CurrentAssigneeID, assigneeID, enums.IMAssignmentTypeAssign, reason, operator, now, ConversationAssignmentOptions{
		DispatchMode:   enums.AgentTeamDispatchModeManual,
		WorkloadWeight: normalizedWorkloadWeight(conversation),
	}); err != nil {
		return err
	}
	result := ctx.Tx.Model(&models.Conversation{}).
		Where("id = ? AND tenant_id = ? AND status = ? AND current_assignee_id = ?", conversation.ID, conversation.TenantID, conversation.Status, conversation.CurrentAssigneeID).
		Updates(map[string]any{
			"current_assignee_id": assigneeID,
			"current_team_id":     scope.team.ID,
			"status":              enums.IMConversationStatusActive,
			"update_user_id":      operator.UserID,
			"update_user_name":    operator.Username,
			"updated_at":          now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errorsx.InvalidParam("会话接待状态已变化，请刷新后重试")
	}
	if _, err := ConversationRouteService.enterHQAgentDeskServingWithDB(ctx.Tx, conversation.ID, reason, now); err != nil {
		return err
	}
	if err := ConversationEventLogService.CreateEvent(ctx, conversation.ID, enums.IMEventTypeAssign, enums.IMSenderTypeAgent, operator.UserID, "会话已主动接管", ConversationService.buildEventPayload(map[string]any{
		"requestId":      requestID,
		"fromStatus":     conversation.Status,
		"toStatus":       enums.IMConversationStatusActive,
		"fromAssigneeId": conversation.CurrentAssigneeID,
		"toAssigneeId":   assigneeID,
		"toTeamId":       scope.team.ID,
		"sessionNo":      route.SessionNo,
		"reason":         reason,
	})); err != nil {
		return err
	}
	if assignedEvent != nil {
		*assignedEvent = events.ConversationAssignedEvent{
			ConversationID: conversation.ID,
			FromUserID:     conversation.CurrentAssigneeID,
			ToUserID:       assigneeID,
			OperatorID:     operator.UserID,
			Reason:         reason,
			AssignType:     events.ConversationAssignTypeSelfTakeover,
		}
	}
	return nil
}

func (s *conversationTakeoverService) resolveScopeDB(db *gorm.DB, conversation *models.Conversation, route *models.ConversationRouteState) (conversationTakeoverScope, error) {
	if db == nil || conversation == nil || route == nil || conversation.TenantID <= 0 || route.TenantID != conversation.TenantID || route.ConversationID != conversation.ID {
		return conversationTakeoverScope{}, errorsx.InvalidParam("会话接管范围不完整")
	}
	teamIDs := ConversationDispatchService.resolveDispatchTeamIDsDB(db, conversation, route)
	if len(teamIDs) != 1 {
		return conversationTakeoverScope{}, errorsx.InvalidParam("当前会话未匹配唯一负责客服组")
	}
	team := repositories.AgentTeamRepository.GetInTenant(db, teamIDs[0], conversation.TenantID)
	if team == nil || team.Status != enums.StatusOk {
		return conversationTakeoverScope{}, errorsx.InvalidParam("会话负责客服组不存在或已停用")
	}
	return conversationTakeoverScope{conversation: conversation, route: route, team: team}, nil
}

func (s *conversationTakeoverService) lockConversationRouteDB(db *gorm.DB, conversationID, tenantID int64) (*models.Conversation, *models.ConversationRouteState, error) {
	conversation, err := repositories.ConversationRepository.GetForUpdateInTenant(db, conversationID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	if conversation == nil {
		return nil, nil, errorsx.InvalidParam("会话不存在")
	}
	route, err := repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(db, conversation.ID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	if route == nil || route.SessionNo <= 0 {
		return nil, nil, errorsx.InvalidParam("会话路由不存在或会话段无效")
	}
	return conversation, route, nil
}

func (s *conversationTakeoverService) canRequestTakeoverDB(db *gorm.DB, scope conversationTakeoverScope, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 || scope.conversation == nil || scope.route == nil || scope.team == nil {
		return errorsx.Forbidden("当前账号不能申请接管")
	}
	if operator.ActiveTenantID != scope.conversation.TenantID {
		return errorsx.Forbidden("当前账号与会话不属于同一接入公司")
	}
	hasCSRole := slices.Contains(operator.Roles, constants.RoleCodeCsUser)
	hasStoreStaffRole := slices.Contains(operator.Roles, constants.RoleCodeStoreStaff)
	if hasCSRole {
		profile := repositories.AgentProfileRepository.Take(db, "tenant_id = ? AND user_id = ? AND status = ?", scope.conversation.TenantID, operator.UserID, enums.StatusOk)
		if profile != nil && profile.TeamID == scope.team.ID && teamCanServeRoute(scope.team, scope.route) {
			return nil
		}
	}
	if hasStoreStaffRole {
		bindingID := scope.route.StoreStaffBindingID
		if bindingID <= 0 {
			bindingID = scope.conversation.StoreStaffBindingID
		}
		binding := repositories.StoreStaffBindingRepository.GetInTenant(db, bindingID, scope.conversation.TenantID)
		if binding != nil && binding.UserID == operator.UserID && binding.ActiveUserID != nil && *binding.ActiveUserID == operator.UserID && binding.AgentTeamID == scope.team.ID && binding.StoreID == scope.route.StoreID {
			return nil
		}
	}
	if hasCSRole && hasStoreStaffRole {
		return errorsx.Forbidden("当前客服和门店员工身份均不在该会话负责范围")
	}
	if hasCSRole {
		return errorsx.Forbidden("当前客服不属于该会话负责客服组或服务范围")
	}
	if hasStoreStaffRole {
		return errorsx.Forbidden("当前门店员工未绑定该会话门店员工号")
	}
	return errorsx.Forbidden("只有客服或门店员工可以申请主动接管")
}

func normalizeConversationRoomID(value string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "R:"))
}

func isConversationRoomMapping(mapping *models.WxWorkKFConversation) bool {
	if mapping == nil {
		return false
	}
	externalID := strings.TrimSpace(mapping.ExternalUserID)
	return strings.HasPrefix(externalID, "R:") ||
		strings.Contains(externalID, "@chatroom") ||
		strings.Contains(externalID, "@openim") ||
		strings.HasSuffix(strings.TrimSpace(mapping.OpenKfID), ":room")
}

func (s *conversationTakeoverService) canManageTeamDB(operator *dto.AuthPrincipal, team *models.AgentTeam) bool {
	if operator == nil || team == nil || operator.ActiveTenantID != team.TenantID {
		return false
	}
	if AgentTeamScopeService.IsAdmin(operator) {
		return true
	}
	return slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) && team.LeaderUserID == operator.UserID
}

func (s *conversationTakeoverService) canResumeAIDB(db *gorm.DB, conversation *models.Conversation, route *models.ConversationRouteState, operator *dto.AuthPrincipal) bool {
	if conversation == nil || route == nil || operator == nil || conversation.ServiceMode == enums.IMConversationServiceModeHumanOnly || !s.aiReplyEnabledDB(db, route, conversation.TenantID) {
		return false
	}
	decision := ConversationRuntimeModeService.ResolveDB(db, conversation, route)
	if decision.Mode != enums.ConversationRuntimeModeHumanActive &&
		decision.Mode != enums.ConversationRuntimeModeHumanPending &&
		decision.Mode != enums.ConversationRuntimeModeResumePending {
		return false
	}
	return conversation.Status == enums.IMConversationStatusActive &&
		conversation.CurrentAssigneeID == operator.UserID &&
		s.hasPermission(operator, constants.PermissionConversationSend.Code)
}

func (s *conversationTakeoverService) aiReplyEnabledDB(db *gorm.DB, route *models.ConversationRouteState, tenantID int64) bool {
	if route == nil || route.WxWorkInstanceID <= 0 {
		return true
	}
	instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(db, route.WxWorkInstanceID, tenantID)
	return instance != nil && instance.Status == enums.StatusOk && instance.AIReplyEnabled
}

func (s *conversationTakeoverService) routeAllowsWebReply(status enums.ConversationRouteStatus) bool {
	return status == enums.ConversationRouteStatusHQAgentDeskServing
}

func (s *conversationTakeoverService) routeIsManual(status enums.ConversationRouteStatus) bool {
	return status == enums.ConversationRouteStatusHQAgentDeskPending ||
		status == enums.ConversationRouteStatusHQAgentDeskServing ||
		status == enums.ConversationRouteStatusStoreWecomManual
}

func (s *conversationTakeoverService) hasPermission(operator *dto.AuthPrincipal, permission string) bool {
	return operator != nil && slices.Contains(operator.Permissions, permission)
}

func (s *conversationTakeoverService) hasPermissions(operator *dto.AuthPrincipal, permissions ...string) bool {
	for _, permission := range permissions {
		if !s.hasPermission(operator, permission) {
			return false
		}
	}
	return true
}

func (s *conversationTakeoverService) hasTakeoverTable(db *gorm.DB) bool {
	return db != nil && db.Migrator().HasTable(&models.ConversationTakeoverRequest{})
}

func (s *conversationTakeoverService) directTakeoverRouteAllowed(conversation *models.Conversation, route *models.ConversationRouteState) bool {
	if conversation == nil || route == nil {
		return false
	}
	decision := ConversationRuntimeModeService.ResolveDB(sqls.DB(), conversation, route)
	if decision.Mode == enums.ConversationRuntimeModeAIActive || decision.Mode == enums.ConversationRuntimeModeAIDegraded {
		return true
	}
	return decision.Mode == enums.ConversationRuntimeModeHumanPending && route.NeedHumanFollowUp
}

func (s *conversationTakeoverService) findPendingDB(db *gorm.DB, tenantID, conversationID int64, sessionNo int, forUpdate bool) *models.ConversationTakeoverRequest {
	if !s.hasTakeoverTable(db) {
		return nil
	}
	item, err := repositories.ConversationTakeoverRequestRepository.FindPendingByConversationSession(db, tenantID, conversationID, sessionNo, forUpdate)
	if err != nil {
		slog.Warn("load pending conversation takeover request failed", "conversation_id", conversationID, "error", err)
		return nil
	}
	return item
}

func (s *conversationTakeoverService) findActiveDB(db *gorm.DB, tenantID, conversationID int64, sessionNo int, forUpdate bool) *models.ConversationTakeoverRequest {
	if !s.hasTakeoverTable(db) {
		return nil
	}
	item, err := repositories.ConversationTakeoverRequestRepository.FindActiveByConversationSession(db, tenantID, conversationID, sessionNo, forUpdate)
	if err != nil {
		slog.Warn("load active conversation takeover request failed", "conversation_id", conversationID, "error", err)
		return nil
	}
	return item
}

func (s *conversationTakeoverService) applyRequestState(state *response.ConversationTakeoverStateResponse, item *models.ConversationTakeoverRequest, operator *dto.AuthPrincipal) {
	if state == nil || item == nil || operator == nil {
		return
	}
	state.RequestID = item.ID
	state.RequestStatus = string(item.Status)
	state.RequesterUserID = item.RequesterUserID
	state.RequesterName = utils.RepairMojibakeText(item.RequesterName)
	state.Reason = utils.RepairMojibakeText(item.Reason)
	state.ReviewRemark = utils.RepairMojibakeText(item.ReviewRemark)
	state.RequestedAt = utils.FormatTime(item.CreatedAt)
	state.ReviewedAt = utils.FormatTimePtr(item.ReviewedAt)
	state.PendingForMe = item.Status == enums.ConversationTakeoverRequestStatusPending && item.RequesterUserID == operator.UserID
	state.PendingForAnother = item.Status == enums.ConversationTakeoverRequestStatusPending && item.RequesterUserID != operator.UserID
	state.AuthorizedForMe = item.Status == enums.ConversationTakeoverRequestStatusAuthorized && item.RequesterUserID == operator.UserID
	state.AuthorizedForAnother = item.Status == enums.ConversationTakeoverRequestStatusAuthorized && item.RequesterUserID != operator.UserID
	state.CanActivateTakeover = state.AuthorizedForMe
	state.CanRequest = false
}

func (s *conversationTakeoverService) cancelStaleRequestDB(db *gorm.DB, item *models.ConversationTakeoverRequest, operator *dto.AuthPrincipal, reason string) error {
	if item == nil {
		return errorsx.InvalidParam("接管申请不存在")
	}
	now := time.Now()
	if err := repositories.ConversationTakeoverRequestRepository.UpdatesInTenant(db, item.ID, item.TenantID, map[string]any{
		"status":          enums.ConversationTakeoverRequestStatusCancelled,
		"terminal_reason": reason,
		"active_key":      nil,
		"reviewer_user_id": func() int64 {
			if operator != nil {
				return operator.UserID
			}
			return 0
		}(),
		"reviewer_name": func() string {
			if operator != nil {
				return s.operatorName(operator)
			}
			return "system"
		}(),
		"reviewed_at":      now,
		"updated_at":       now,
		"update_user_name": "system",
	}); err != nil {
		return err
	}
	return nil
}

func (s *conversationTakeoverService) userRoleCodesDB(db *gorm.DB, userID int64) []string {
	if db == nil || userID <= 0 {
		return nil
	}
	type roleCodeRow struct {
		Code string `gorm:"column:code"`
	}
	rows := make([]roleCodeRow, 0)
	db.Table("t_role AS role").
		Select("role.code").
		Joins("JOIN t_user_role AS user_role ON user_role.role_id = role.id").
		Where("user_role.user_id = ? AND role.status = ?", userID, enums.StatusOk).
		Scan(&rows)
	ret := make([]string, 0, len(rows))
	for _, row := range rows {
		if value := strings.TrimSpace(row.Code); value != "" {
			ret = append(ret, value)
		}
	}
	return ret
}

func (s *conversationTakeoverService) userPermissionCodesDB(db *gorm.DB, userID int64) []string {
	if db == nil || userID <= 0 {
		return nil
	}
	type permissionCodeRow struct {
		Code string `gorm:"column:code"`
	}
	rows := make([]permissionCodeRow, 0)
	db.Table("t_permission AS permission").
		Select("DISTINCT permission.code").
		Joins("JOIN t_role_permission AS role_permission ON role_permission.permission_id = permission.id").
		Joins("JOIN t_user_role AS user_role ON user_role.role_id = role_permission.role_id").
		Joins("JOIN t_role AS role ON role.id = user_role.role_id").
		Where("user_role.user_id = ? AND role.status = ? AND permission.status = ?", userID, enums.StatusOk, enums.StatusOk).
		Scan(&rows)
	ret := make([]string, 0, len(rows))
	for _, row := range rows {
		if value := strings.TrimSpace(row.Code); value != "" {
			ret = append(ret, value)
		}
	}
	return ret
}

func (s *conversationTakeoverService) operatorName(operator *dto.AuthPrincipal) string {
	if operator == nil {
		return ""
	}
	if name := strings.TrimSpace(operator.Nickname); name != "" {
		return name
	}
	return strings.TrimSpace(operator.Username)
}

func (s *conversationTakeoverService) notifyReviewer(item *models.ConversationTakeoverRequest) {
	if item == nil {
		return
	}
	recipientID := dispatchAttentionRecipient(item.TenantID, item.TeamID)
	if recipientID <= 0 {
		return
	}
	content := fmt.Sprintf("%s申请接管会话 #%d", item.RequesterName, item.ConversationID)
	if reason := strings.TrimSpace(item.Reason); reason != "" {
		content += "\n原因: " + reason
	}
	if _, err := NotificationService.CreateAndPushInTenant(request.CreateNotificationRequest{
		RecipientUserID:  recipientID,
		Title:            "新的会话接管申请",
		Content:          content,
		NotificationType: conversationTakeoverNotificationType,
		BizType:          "conversation_takeover_request",
		BizID:            item.ID,
		ActionURL:        fmt.Sprintf("/dashboard/conversations?conversationId=%d", item.ConversationID),
	}, item.TenantID); err != nil {
		slog.Warn("notify conversation takeover reviewer failed", "request_id", item.ID, "error", err)
	}
}

func (s *conversationTakeoverService) notifyRequester(item *models.ConversationTakeoverRequest, approved bool) {
	if item == nil || item.RequesterUserID <= 0 {
		return
	}
	title := "会话接管申请已拒绝"
	content := fmt.Sprintf("会话 #%d 的接管申请未通过", item.ConversationID)
	if approved {
		title = "会话接管申请已通过"
		content = fmt.Sprintf("会话 #%d 的接管申请已通过，请再次点击 AI 回复开关确认接管", item.ConversationID)
	}
	if remark := strings.TrimSpace(item.ReviewRemark); remark != "" {
		content += "\n备注: " + remark
	}
	if _, err := NotificationService.CreateAndPushInTenant(request.CreateNotificationRequest{
		RecipientUserID:  item.RequesterUserID,
		Title:            title,
		Content:          content,
		NotificationType: conversationTakeoverNotificationType,
		BizType:          "conversation_takeover_request",
		BizID:            item.ID,
		ActionURL:        fmt.Sprintf("/dashboard/conversations?conversationId=%d", item.ConversationID),
	}, item.TenantID); err != nil {
		slog.Warn("notify conversation takeover requester failed", "request_id", item.ID, "error", err)
	}
}

func (s *conversationTakeoverService) publishTakeover(conversationID int64, assignedEvent events.ConversationAssignedEvent) {
	if conversationID > 0 {
		if conversation := ConversationService.Get(conversationID); conversation != nil {
			eventType := enums.IMRealtimeEventConversationUpdated
			if assignedEvent.ConversationID > 0 {
				eventType = enums.IMRealtimeEventConversationAssigned
			}
			WsService.PublishConversationChanged(conversation, eventType)
		}
	}
	if assignedEvent.ConversationID > 0 {
		eventbus.PublishAsync(context.Background(), assignedEvent)
	}
}
