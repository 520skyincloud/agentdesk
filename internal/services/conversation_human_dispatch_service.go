package services

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var ConversationHumanDispatchService = newConversationHumanDispatchService()

const (
	HandoffWaitingMessage     = "已经帮您通知同事了，我会继续关注。"
	HandoffOffHoursMessage    = "现在暂时不在人工服务时间内，您可以先把问题发我，我先帮您看着；同事上班后也会继续跟进。"
	HandoffStoreManualMessage = "已经帮您通知门店同事了，我会继续关注。"
	manualHandoffCooldown     = 2 * time.Minute
)

type HandoffDecisionType string

const (
	HandoffDecisionAssigned    HandoffDecisionType = "assigned"
	HandoffDecisionStoreWecom  HandoffDecisionType = "store_wecom"
	HandoffDecisionHQAgentDesk HandoffDecisionType = "hq_agentdesk"
	HandoffDecisionTeamPool    HandoffDecisionType = "team_pool"
	HandoffDecisionGlobalPool  HandoffDecisionType = "global_pool"
	HandoffDecisionOffHours    HandoffDecisionType = "off_hours"
)

type HandoffDecisionResult struct {
	Decision   HandoffDecisionType
	TeamID     int64
	AssigneeID int64
	Message    string
}

type conversationHumanDispatchService struct{}

func newConversationHumanDispatchService() *conversationHumanDispatchService {
	return &conversationHumanDispatchService{}
}

func (s *conversationHumanDispatchService) TryOffHoursHandoffByAI(conversationID int64, aiAgent models.AIAgent, reason string) (bool, error) {
	return s.TryOffHoursHandoffByAIWithRequestID(conversationID, aiAgent, reason, "")
}

func (s *conversationHumanDispatchService) TryOffHoursHandoffByAIWithRequestID(conversationID int64, aiAgent models.AIAgent, reason string, requestID string) (bool, error) {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return false, errorsx.InvalidParam("会话不存在")
	}
	if err := validateConversationAIAgentTenant(conversation, aiAgent); err != nil {
		return false, err
	}
	teamIDs := orderedPositiveIDs(aiAgent.TeamIDs)
	activeTeamIDs := ConversationDispatchService.findActiveScheduleTeamIDs(teamIDs, conversation.TenantID, time.Now())
	if len(activeTeamIDs) > 0 {
		return false, nil
	}
	if s.isRecentManualHandoff(conversationID, time.Now()) {
		return true, nil
	}
	_ = s.markManualHandoffRequested(conversationID, time.Now())
	if err := s.createEventWithRequestID(conversationID, requestID, enums.IMEventTypeTransfer, enums.IMSenderTypeAI, aiAgent.ID, "转人工失败：非服务时间", strings.TrimSpace(reason)); err != nil {
		return true, err
	}
	if err := s.sendAITextWithRequestID(conversationID, aiAgent.ID, HandoffOffHoursMessage, requestID); err != nil {
		return true, err
	}
	return true, nil
}

func (s *conversationHumanDispatchService) HandoffByAI(conversationID int64, aiAgent models.AIAgent, reason string) (*HandoffDecisionResult, error) {
	return s.HandoffByAIWithRequestID(conversationID, aiAgent, reason, "")
}

func (s *conversationHumanDispatchService) HandoffByAIWithRequestID(conversationID int64, aiAgent models.AIAgent, reason string, requestID string) (*HandoffDecisionResult, error) {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	if err := validateConversationAIAgentTenant(conversation, aiAgent); err != nil {
		return nil, err
	}
	if statusResult := s.recentHandoffResult(conversationID); statusResult != nil {
		return statusResult, nil
	}
	teamIDs := orderedPositiveIDs(aiAgent.TeamIDs)
	activeTeamIDs := ConversationDispatchService.findActiveScheduleTeamIDs(teamIDs, conversation.TenantID, time.Now())
	runtime := s.resolveStoreStaffRuntime(conversationID)
	now := time.Now()
	if runtime.NoWxWorkInstance && len(activeTeamIDs) > 0 {
		if err := s.markStoreRoomHandoff(conversationID, aiAgent, reason, requestID); err != nil {
			return nil, err
		}
		_ = s.sendAITextWithRequestID(conversationID, aiAgent.ID, HandoffStoreManualMessage, requestID)
		return &HandoffDecisionResult{Decision: HandoffDecisionStoreWecom, Message: HandoffStoreManualMessage}, nil
	}
	if s.shouldRouteToStoreRoom(runtime, now, len(activeTeamIDs) > 0) {
		if err := s.markStoreRoomHandoff(conversationID, aiAgent, reason, requestID); err != nil {
			return nil, err
		}
		_ = s.sendAITextWithRequestID(conversationID, aiAgent.ID, HandoffStoreManualMessage, requestID)
		return &HandoffDecisionResult{Decision: HandoffDecisionStoreWecom, Message: HandoffStoreManualMessage}, nil
	}
	if runtime.ManagedMode == constants.StoreManagedModeNone || !runtime.FallbackToHQ {
		if _, err := s.TryOffHoursHandoffByAIWithRequestID(conversationID, aiAgent, reason, requestID); err != nil {
			return nil, err
		}
		return &HandoffDecisionResult{Decision: HandoffDecisionOffHours, Message: HandoffOffHoursMessage}, nil
	}

	if err := s.markHQAgentDeskHandoff(conversationID, aiAgent, reason, requestID); err != nil {
		return nil, err
	}
	_ = s.sendAITextWithRequestID(conversationID, aiAgent.ID, HandoffWaitingMessage, requestID)
	return &HandoffDecisionResult{Decision: HandoffDecisionHQAgentDesk, Message: HandoffWaitingMessage}, nil
}

func (s *conversationHumanDispatchService) recentHandoffResult(conversationID int64) *HandoffDecisionResult {
	state := ConversationRouteService.GetByConversationID(conversationID)
	if state == nil || state.LastManualHandoffAt == nil || time.Since(*state.LastManualHandoffAt) > manualHandoffCooldown {
		return nil
	}
	switch state.RouteStatus {
	case enums.ConversationRouteStatusStoreWecomManual:
		return &HandoffDecisionResult{Decision: HandoffDecisionTeamPool, Message: HandoffStoreManualMessage}
	case enums.ConversationRouteStatusHQAgentDeskPending, enums.ConversationRouteStatusHQAgentDeskServing:
		return &HandoffDecisionResult{Decision: HandoffDecisionTeamPool, Message: HandoffWaitingMessage}
	}
	return nil
}

func (s *conversationHumanDispatchService) isRecentManualHandoff(conversationID int64, now time.Time) bool {
	state := ConversationRouteService.GetByConversationID(conversationID)
	return state != nil && state.LastManualHandoffAt != nil && now.Sub(*state.LastManualHandoffAt) <= manualHandoffCooldown
}

func (s *conversationHumanDispatchService) markManualHandoffRequested(conversationID int64, now time.Time) error {
	state, err := ConversationRouteService.Ensure(conversationID)
	if err != nil {
		return err
	}
	return repositories.ConversationRouteStateRepository.UpdatesInTenant(sqls.DB(), state.ID, state.TenantID, map[string]any{
		"last_manual_handoff_at": now,
		"updated_at":             now,
		"update_user_name":       "system",
	})
}

func (s *conversationHumanDispatchService) canUseStoreRoomHandoff(conversationID int64) bool {
	return s.storeRoomConfigured(s.resolveStoreStaffRuntime(conversationID))
}

func (s *conversationHumanDispatchService) canFallbackToHQ(conversationID int64) bool {
	runtime := s.resolveStoreStaffRuntime(conversationID)
	return runtime.ManagedMode != constants.StoreManagedModeNone && runtime.FallbackToHQ
}

func (s *conversationHumanDispatchService) resolveStoreStaffRuntime(conversationID int64) StoreStaffRuntimeConfig {
	conversation, err := requireConversationParent(sqls.DB(), conversationID)
	if err != nil {
		return StoreStaffRuntimeConfig{}
	}
	route := ConversationRouteService.GetByConversationIDInTenant(conversationID, conversation.TenantID)
	if route == nil || route.WxWorkInstanceID <= 0 {
		return StoreStaffRuntimeConfig{ManagedMode: constants.StoreManagedModeSemi, FallbackToHQ: true, ManualTimeoutMinutes: 10, NoWxWorkInstance: true}
	}
	return StoreStaffBindingService.ResolveForInstance(WxWorkProtocolInstanceService.GetByTenantID(route.WxWorkInstanceID, conversation.TenantID))
}

func (s *conversationHumanDispatchService) shouldRouteToStoreRoom(runtime StoreStaffRuntimeConfig, now time.Time, hasActiveTeamSchedule bool) bool {
	if !s.storeRoomConfigured(runtime) {
		return false
	}
	switch runtime.ManagedMode {
	case constants.StoreManagedModeFull:
		return false
	case constants.StoreManagedModeNone:
		return true
	default:
		if strings.TrimSpace(runtime.ServiceHours) == "" {
			return true
		}
		return isWithinStoreServiceHours(runtime.ServiceHours, now)
	}
}

func (s *conversationHumanDispatchService) storeRoomConfigured(runtime StoreStaffRuntimeConfig) bool {
	return runtime.StoreRoomNotifyEnabled && strings.TrimSpace(runtime.StoreRoomConversationID) != ""
}

func (s *conversationHumanDispatchService) ApplyHumanOnlyCreate(conversationID int64, aiAgent models.AIAgent) (*HandoffDecisionResult, error) {
	conversation, err := requireConversationParent(sqls.DB(), conversationID)
	if err != nil {
		return nil, err
	}
	if err := validateConversationAIAgentTenant(conversation, aiAgent); err != nil {
		return nil, err
	}
	teamIDs := orderedPositiveIDs(aiAgent.TeamIDs)
	activeTeamIDs := ConversationDispatchService.findActiveScheduleTeamIDs(teamIDs, conversation.TenantID, time.Now())
	if len(activeTeamIDs) == 0 {
		if err := s.moveToGlobalPool(conversationID, aiAgent.Name); err != nil {
			return nil, err
		}
		if err := s.sendAIText(conversationID, aiAgent.ID, HandoffWaitingMessage); err != nil {
			return nil, err
		}
		return &HandoffDecisionResult{Decision: HandoffDecisionGlobalPool, Message: HandoffWaitingMessage}, nil
	}
	return s.dispatchAfterHandoff(conversationID, aiAgent.ID, activeTeamIDs, "仅人工模式新会话")
}

func (s *conversationHumanDispatchService) DispatchPendingConversation(conversationID int64, aiAgent models.AIAgent) (*HandoffDecisionResult, error) {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	if err := validateConversationAIAgentTenant(conversation, aiAgent); err != nil {
		return nil, err
	}
	if conversation.Status != enums.IMConversationStatusPending || conversation.CurrentAssigneeID > 0 {
		return nil, errorsx.InvalidParam("只有待接入未分配会话允许自动分配")
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversationID, conversation.TenantID)
	teamIDs := ConversationDispatchService.resolveDispatchTeamIDs(conversation, &aiAgent, route)
	activeTeamIDs := ConversationDispatchService.findActiveScheduleTeamIDs(teamIDs, conversation.TenantID, time.Now())
	if len(activeTeamIDs) == 0 {
		return &HandoffDecisionResult{Decision: HandoffDecisionOffHours}, nil
	}
	dispatched, err := ConversationDispatchService.DispatchPendingConversation(conversation, &aiAgent)
	if err != nil {
		return nil, err
	}
	if dispatched != nil {
		return &HandoffDecisionResult{
			Decision:   HandoffDecisionAssigned,
			TeamID:     dispatched.CurrentTeamID,
			AssigneeID: dispatched.CurrentAssigneeID,
		}, nil
	}
	teamID := activeTeamIDs[0]
	teamPoolConversation, err := s.moveToTeamPool(conversationID, teamID, "手动触发自动分配")
	if err != nil {
		return nil, err
	}
	if teamPoolConversation != nil {
		WsService.PublishConversationChanged(teamPoolConversation, enums.IMRealtimeEventConversationUpdated)
	}
	return &HandoffDecisionResult{Decision: HandoffDecisionTeamPool, TeamID: teamID}, nil
}

func validateConversationAIAgentTenant(conversation *models.Conversation, aiAgent models.AIAgent) error {
	if conversation == nil || conversation.TenantID <= 0 || aiAgent.TenantID != conversation.TenantID {
		return errorsx.InvalidParam("接待策略不属于会话所在公司")
	}
	if aiAgent.ID > 0 && conversation.AIAgentID > 0 && aiAgent.ID != conversation.AIAgentID {
		return errorsx.InvalidParam("接待策略与会话绑定不一致")
	}
	return nil
}

func (s *conversationHumanDispatchService) dispatchAfterHandoff(conversationID, aiAgentID int64, activeTeamIDs []int64, reason string) (*HandoffDecisionResult, error) {
	conversation, err := requireConversationParent(sqls.DB(), conversationID)
	if err != nil {
		return nil, err
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversationID, conversation.TenantID)
	aiAgent := AIAgentService.GetByTenantID(aiAgentID, conversation.TenantID)
	if aiAgent != nil {
		if resolvedTeamIDs := ConversationDispatchService.resolveDispatchTeamIDs(conversation, aiAgent, route); len(resolvedTeamIDs) > 0 {
			if resolvedActiveTeamIDs := ConversationDispatchService.findActiveScheduleTeamIDs(resolvedTeamIDs, conversation.TenantID, time.Now()); len(resolvedActiveTeamIDs) > 0 {
				activeTeamIDs = resolvedActiveTeamIDs
			}
		}
		dispatched, err := ConversationDispatchService.DispatchPendingConversation(conversation, aiAgent)
		if err != nil {
			return nil, err
		}
		if dispatched != nil {
			return &HandoffDecisionResult{
				Decision:   HandoffDecisionAssigned,
				TeamID:     dispatched.CurrentTeamID,
				AssigneeID: dispatched.CurrentAssigneeID,
				Message:    HandoffWaitingMessage,
			}, nil
		}
	}

	if len(activeTeamIDs) == 0 {
		return &HandoffDecisionResult{Decision: HandoffDecisionOffHours, Message: HandoffOffHoursMessage}, nil
	}
	teamID := activeTeamIDs[0]
	teamPoolConversation, err := s.moveToTeamPool(conversationID, teamID, reason)
	if err != nil {
		return nil, err
	}
	if teamPoolConversation != nil {
		WsService.PublishConversationChanged(teamPoolConversation, enums.IMRealtimeEventConversationUpdated)
	}
	return &HandoffDecisionResult{Decision: HandoffDecisionTeamPool, TeamID: teamID, Message: HandoffWaitingMessage}, nil
}

func (s *conversationHumanDispatchService) markStoreRoomHandoff(conversationID int64, aiAgent models.AIAgent, reason string, requestID string) error {
	now := time.Now()
	trimmedReason := strings.TrimSpace(reason)
	if err := s.recordStoreRoomHandoff(conversationID, aiAgent, trimmedReason, requestID, now); err != nil {
		return err
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(conversationID, trimmedReason, now); err != nil {
		return err
	}
	_ = s.markManualHandoffRequested(conversationID, now)
	s.notifyStoreRoomHandoff(conversationID, trimmedReason)
	return nil
}

func (s *conversationHumanDispatchService) recordStoreRoomHandoff(conversationID int64, aiAgent models.AIAgent, reason string, requestID string, now time.Time) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, err := requireConversationParent(ctx.Tx, conversationID)
		if err != nil {
			return err
		}
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, conversationID, conversation.TenantID, map[string]any{
			"handoff_at":       now,
			"handoff_reason":   strings.TrimSpace(reason),
			"update_user_id":   0,
			"update_user_name": aiAgent.Name,
			"updated_at":       now,
		}); err != nil {
			return err
		}
		return ConversationEventLogService.CreateEventWithRequestID(ctx, conversationID, requestID, enums.IMEventTypeTransfer, enums.IMSenderTypeAI, aiAgent.ID, "AI通知门店群跟进", ConversationService.buildEventPayload(map[string]any{
			"status":   conversation.Status,
			"decision": string(HandoffDecisionStoreWecom),
			"reason":   strings.TrimSpace(reason),
		}))
	})
}

func (s *conversationHumanDispatchService) markHQAgentDeskHandoff(conversationID int64, aiAgent models.AIAgent, reason string, requestID string) error {
	now := time.Now()
	trimmedReason := strings.TrimSpace(reason)
	if err := s.recordHandoff(conversationID, aiAgent, trimmedReason, requestID, now); err != nil {
		return err
	}
	_ = s.markManualHandoffRequested(conversationID, now)
	s.notifyAgentDeskHandoff(conversationID, trimmedReason)
	ConversationDispatchService.ScheduleDispatch(conversationID)
	return nil
}

func (s *conversationHumanDispatchService) recordHandoff(conversationID int64, aiAgent models.AIAgent, reason string, requestID string, now time.Time) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, err := requireConversationParent(ctx.Tx, conversationID)
		if err != nil {
			return err
		}
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, conversationID, conversation.TenantID, map[string]any{
			"handoff_at":          now,
			"handoff_reason":      strings.TrimSpace(reason),
			"status":              enums.IMConversationStatusPending,
			"current_team_id":     0,
			"current_assignee_id": 0,
			"update_user_id":      0,
			"update_user_name":    aiAgent.Name,
			"updated_at":          now,
		}); err != nil {
			return err
		}
		if err := ConversationEventLogService.CreateEventWithRequestID(ctx, conversationID, requestID, enums.IMEventTypeTransfer, enums.IMSenderTypeAI, aiAgent.ID, "AI转人工", strings.TrimSpace(reason)); err != nil {
			return err
		}
		_, err = ConversationRouteService.enterHQAgentDeskPendingWithDB(ctx.Tx, conversationID, strings.TrimSpace(reason), now)
		return err
	})
}

func (s *conversationHumanDispatchService) moveToTeamPool(conversationID, teamID int64, reason string) (*models.Conversation, error) {
	return s.moveToTeamPoolWithRequestID(conversationID, teamID, reason, "")
}

func (s *conversationHumanDispatchService) moveToTeamPoolWithRequestID(conversationID, teamID int64, reason string, requestID string) (*models.Conversation, error) {
	now := time.Now()
	var conversation *models.Conversation
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current, err := requireConversationParent(ctx.Tx, conversationID)
		if err != nil {
			return err
		}
		if team := repositories.AgentTeamRepository.GetInTenant(ctx.Tx, teamID, current.TenantID); team == nil || team.Status != enums.StatusOk {
			return errorsx.InvalidParam("目标客服组不存在或不属于会话接入公司")
		}
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversationID, now); err != nil {
			return err
		}
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, conversationID, current.TenantID, map[string]any{
			"status":              enums.IMConversationStatusPending,
			"current_team_id":     teamID,
			"current_assignee_id": 0,
			"update_user_id":      0,
			"update_user_name":    "system",
			"updated_at":          now,
		}); err != nil {
			return err
		}
		if err := ConversationEventLogService.CreateEventWithRequestID(ctx, conversationID, requestID, enums.IMEventTypeTransfer, enums.IMSenderTypeSystem, 0, "会话进入客服组待接入", ConversationService.buildEventPayload(map[string]any{
			"fromStatus":     current.Status,
			"toStatus":       enums.IMConversationStatusPending,
			"fromAssigneeId": current.CurrentAssigneeID,
			"toAssigneeId":   int64(0),
			"toTeamId":       teamID,
			"reason":         strings.TrimSpace(reason),
			"decision":       string(HandoffDecisionTeamPool),
		})); err != nil {
			return err
		}
		if _, err := ConversationRouteService.enterHQAgentDeskPendingWithDB(ctx.Tx, conversationID, strings.TrimSpace(reason), now); err != nil {
			return err
		}
		current.Status = enums.IMConversationStatusPending
		current.CurrentTeamID = teamID
		current.CurrentAssigneeID = 0
		current.UpdateUserID = 0
		current.UpdateUserName = "system"
		current.UpdatedAt = now
		conversation = current
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.notifyAgentDeskHandoff(conversationID, strings.TrimSpace(reason))
	ConversationDispatchService.ScheduleDispatch(conversationID)
	return conversation, nil
}

func (s *conversationHumanDispatchService) moveToGlobalPool(conversationID int64, operatorName string) error {
	now := time.Now()
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, err := requireConversationParent(ctx.Tx, conversationID)
		if err != nil {
			return err
		}
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, conversationID, conversation.TenantID, map[string]any{
			"status":              enums.IMConversationStatusPending,
			"current_team_id":     0,
			"current_assignee_id": 0,
			"update_user_id":      0,
			"update_user_name":    operatorName,
			"updated_at":          now,
		}); err != nil {
			return err
		}
		if err := ConversationEventLogService.CreateEvent(ctx, conversationID, enums.IMEventTypeTransfer, enums.IMSenderTypeSystem, 0, "会话进入全局待接入", ConversationService.buildEventPayload(map[string]any{
			"fromStatus": conversation.Status,
			"toStatus":   enums.IMConversationStatusPending,
			"decision":   string(HandoffDecisionGlobalPool),
		})); err != nil {
			return err
		}
		_, err = ConversationRouteService.enterHQAgentDeskPendingWithDB(ctx.Tx, conversationID, "进入全局待接入", now)
		return err
	}); err != nil {
		return err
	}
	s.notifyAgentDeskHandoff(conversationID, "进入全局待接入")
	ConversationDispatchService.ScheduleDispatch(conversationID)
	return nil
}

func (s *conversationHumanDispatchService) notifyAgentDeskHandoff(conversationID int64, reason string) {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return
	}
	userIDs := AgentProfileService.GetActiveAgentUserIDsInTenant(conversation.TenantID)
	if len(userIDs) == 0 {
		return
	}
	content := fmt.Sprintf("会话 #%d 等待总部网页端接管", conversation.ID)
	if summary := strings.TrimSpace(ConversationService.BuildConversationSummary(conversation)); summary != "" {
		content = content + "\n" + summary
	}
	if trimmedReason := strings.TrimSpace(reason); trimmedReason != "" {
		content = content + "\n转人工原因: " + trimmedReason
	}
	for _, userID := range userIDs {
		_, err := NotificationService.CreateAndPushInTenant(request.CreateNotificationRequest{
			RecipientUserID:  userID,
			Title:            "新的转人工请求",
			Content:          content,
			NotificationType: "manual_handoff_created",
			BizType:          "conversation",
			BizID:            conversation.ID,
			ActionURL:        fmt.Sprintf("/dashboard/conversations?conversationId=%d", conversation.ID),
		}, conversation.TenantID)
		if err != nil {
			slog.Warn("create agentdesk handoff notification failed", "conversation_id", conversation.ID, "recipient_user_id", userID, "error", err)
		}
	}
}

func (s *conversationHumanDispatchService) notifyStoreRoomHandoff(conversationID int64, reason string) {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return
	}
	route := ConversationRouteService.GetByConversationIDInTenant(conversationID, conversation.TenantID)
	if route == nil || route.WxWorkInstanceID <= 0 {
		return
	}
	instance := WxWorkProtocolInstanceService.GetByTenantID(route.WxWorkInstanceID, conversation.TenantID)
	if instance == nil {
		return
	}
	runtime := StoreStaffBindingService.ResolveForInstance(instance)
	if !s.storeRoomConfigured(runtime) {
		return
	}
	content := s.buildStoreRoomHandoffNotice(conversation, reason)
	atList := uniqueNonBlankStrings(strings.Split(runtime.StoreRoomAtList, ","))
	if err := ChannelMessageOutboxService.EnqueueWxWorkProtocolStoreRoomNotice(conversationID, instance.ID, runtime.StoreRoomConversationID, content, atList); err != nil {
		slog.Warn("enqueue store room handoff notice failed", "conversation_id", conversationID, "wx_work_instance_id", instance.ID, "error", err)
	}
}

func (s *conversationHumanDispatchService) buildStoreRoomHandoffNotice(conversation *models.Conversation, reason string) string {
	return strings.Join([]string{
		"有客人需要人工接待",
		"客户：" + handoffNoticeCustomerName(conversation),
		"摘要：" + compactHandoffNoticeField(s.buildHandoffConversationSummary(conversation, reason)),
		"原因：" + compactHandoffNoticeField(cleanHandoffNoticeReason(reason)),
	}, "\n")
}

func handoffNoticeCustomerName(conversation *models.Conversation) string {
	if conversation == nil {
		return "未命名客户"
	}
	if name := strings.TrimSpace(conversation.CustomerName); name != "" {
		return name
	}
	if conversation.CustomerID > 0 {
		if customer := CustomerService.Get(conversation.CustomerID); customer != nil {
			if name := strings.TrimSpace(customer.Name); name != "" {
				return name
			}
		}
	}
	return "未命名客户"
}

func compactHandoffNoticeField(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

type handoffSummaryItem struct {
	Speaker string
	Text    string
}

func (s *conversationHumanDispatchService) buildHandoffConversationSummary(conversation *models.Conversation, reason string) string {
	if conversation == nil || conversation.ID <= 0 {
		return ""
	}
	items := s.collectHandoffSummaryItems(conversation.ID)
	if summary := s.buildAIHandoffConversationSummary(conversation, reason, items); summary != "" {
		return summary
	}
	if summary := buildFallbackHandoffConversationSummary(reason, items); summary != "" {
		return summary
	}
	return limitText(ConversationService.BuildConversationSummary(conversation), 180)
}

func (s *conversationHumanDispatchService) collectHandoffSummaryItems(conversationID int64) []handoffSummaryItem {
	messages, _, _ := MessageService.FindByConversationIDCursor(conversationID, 0, 12, "", "")
	parts := make([]handoffSummaryItem, 0, len(messages))
	seen := make(map[string]bool)
	for _, message := range messages {
		if message.SenderType == enums.IMSenderTypeAI {
			continue
		}
		text := handoffMessageSummary(message)
		if text == "" || shouldSkipHandoffSummaryText(text) || isConsumedHandoffConfirmationMessage(message) || isLegacyHandoffConfirmationSummaryText(text) {
			continue
		}
		key := fmt.Sprintf("%s:%s", message.SenderType, text)
		if seen[key] {
			continue
		}
		seen[key] = true
		switch message.SenderType {
		case enums.IMSenderTypeCustomer:
			parts = append(parts, handoffSummaryItem{Speaker: "客人", Text: text})
		case enums.IMSenderTypeAgent:
			parts = append(parts, handoffSummaryItem{Speaker: "人工", Text: text})
		}
	}
	return parts
}

func (s *conversationHumanDispatchService) buildAIHandoffConversationSummary(conversation *models.Conversation, reason string, items []handoffSummaryItem) string {
	if conversation == nil || (strings.TrimSpace(reason) == "" && len(items) == 0) {
		return ""
	}
	config, ok := s.resolveHandoffSummaryAIConfig(conversation)
	if !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	result, err := ai.LLM.ChatWithConfig(ctx, config,
		"你是酒店门店值班群通知摘要助手。只输出一句中文摘要，给门店同事快速判断要处理什么。不要输出内部意图code、JSON、工具名、门店名称、会话ID；不要逐条复述聊天记录；不要说“AI”。",
		buildHandoffSummaryPrompt(reason, items),
	)
	if err != nil {
		slog.Warn("build ai handoff summary failed", "conversation_id", conversation.ID, "error", err)
		return ""
	}
	return cleanHandoffAISummary(result.Content)
}

func (s *conversationHumanDispatchService) resolveHandoffSummaryAIConfig(conversation *models.Conversation) (models.AIConfig, bool) {
	if conversation != nil {
		if resolved, err := StoreAIModelSettingService.ResolveForConversation(conversation.ID, StoreAIModelUsageReplyLLM); err == nil && resolved != nil {
			return resolved.Config, true
		}
	}
	return models.AIConfig{}, false
}

func buildHandoffSummaryPrompt(reason string, items []handoffSummaryItem) string {
	lines := make([]string, 0, len(items)+2)
	if cleaned := cleanHandoffNoticeReason(reason); cleaned != "" {
		lines = append(lines, "转人工原因："+cleaned)
	}
	if len(items) > 0 {
		lines = append(lines, "近期有效对话：")
		for _, item := range items {
			lines = append(lines, item.Speaker+"："+item.Text)
		}
	}
	lines = append(lines, "请输出一句 40 字以内摘要，必须是自然中文。")
	return strings.Join(lines, "\n")
}

func buildFallbackHandoffConversationSummary(reason string, items []handoffSummaryItem) string {
	if len(items) > 0 {
		lastCustomer := ""
		for i := len(items) - 1; i >= 0; i-- {
			if items[i].Speaker == "客人" && strings.TrimSpace(items[i].Text) != "" {
				lastCustomer = items[i].Text
				break
			}
		}
		if lastCustomer != "" {
			if isSafetyHandoffReason(reason + " " + lastCustomer) {
				return limitText("客人表示遇到安全或突发情况，需要门店同事确认位置和具体情况。", 120)
			}
			return limitText("客人需要人工跟进："+lastCustomer, 120)
		}
		parts := make([]string, 0, len(items))
		for _, item := range items {
			parts = append(parts, item.Speaker+"："+item.Text)
		}
		return limitText(strings.Join(parts, "；"), 160)
	}
	return ""
}

func cleanHandoffAISummary(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"“”'` \n\t")
	value = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(value)
	value = cleanHumanHandoffReason(value)
	return limitText(value, 120)
}

func cleanHandoffNoticeReason(value string) string {
	value = cleanHumanHandoffReason(value)
	if value == "" {
		return ""
	}
	if idx := strings.LastIndex(value, "客户消息："); idx >= 0 {
		message := strings.TrimSpace(value[idx+len("客户消息："):])
		if message != "" {
			if isSafetyHandoffReason(value) {
				return limitText("安全/突发情况："+message, 120)
			}
			return limitText(message, 120)
		}
	}
	if isSafetyHandoffReason(value) {
		return limitText("安全/突发情况："+strings.TrimPrefix(value, "客人遇到安全或突发情况，需要门店同事尽快关注；"), 120)
	}
	return limitText(value, 120)
}

func shouldSkipHandoffSummaryText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	for _, value := range []string{
		HandoffWaitingMessage,
		HandoffOffHoursMessage,
		HandoffStoreManualMessage,
		"请直接回复“确认”或“取消”",
		"请回复“确认”或“取消”",
		"我准备为你转接人工客服",
		"要我帮您转人工吗",
		"要我现在通知门店同事吗",
	} {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func handoffMessageSummary(message models.Message) string {
	text := strings.TrimSpace(message.Content)
	if message.MessageType == enums.IMMessageTypeHTML {
		text = strings.TrimSpace(utils.BuildHTMLSummary(text))
	}
	if text == "" && strings.TrimSpace(message.Payload) != "" {
		mediaText, mediaSummary, _ := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
		text = strings.TrimSpace(strings.Join([]string{mediaText, mediaSummary}, " "))
	}
	if text == "" {
		text = buildMessageSummary(message.MessageType, message.Content)
	}
	text = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(text)
	return limitText(strings.Join(strings.Fields(text), " "), 80)
}

func (s *conversationHumanDispatchService) createEvent(conversationID int64, eventType enums.IMEventType, senderType enums.IMSenderType, senderID int64, content, payload string) error {
	return s.createEventWithRequestID(conversationID, "", eventType, senderType, senderID, content, payload)
}

func (s *conversationHumanDispatchService) createEventWithRequestID(conversationID int64, requestID string, eventType enums.IMEventType, senderType enums.IMSenderType, senderID int64, content, payload string) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return ConversationEventLogService.CreateEventWithRequestID(ctx, conversationID, requestID, eventType, senderType, senderID, content, payload)
	})
}

func (s *conversationHumanDispatchService) sendAIText(conversationID, aiAgentID int64, content string) error {
	return s.sendAITextWithRequestID(conversationID, aiAgentID, content, "")
}

func (s *conversationHumanDispatchService) sendAITextWithRequestID(conversationID, aiAgentID int64, content string, requestID string) error {
	_, err := MessageService.SendAIServiceNoticeWithRequestID(conversationID, aiAgentID, content, requestID)
	return err
}

func orderedPositiveIDs(value string) []int64 {
	return uniquePositiveInt64sFromStrings(strings.Split(value, ","))
}

func uniqueNonBlankStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	ret := make([]string, 0, len(values))
	for _, value := range values {
		item := strings.TrimSpace(value)
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

func isWithinStoreServiceHours(serviceHours string, now time.Time) bool {
	serviceHours = strings.TrimSpace(serviceHours)
	if serviceHours == "" {
		return false
	}
	normalized := strings.NewReplacer("；", ";", "，", ",", "、", ",", " ", "").Replace(serviceHours)
	parts := strings.FieldsFunc(normalized, func(r rune) bool { return r == ',' || r == ';' || r == '|' || r == '\n' })
	current := now.Hour()*60 + now.Minute()
	for _, part := range parts {
		if isWithinStoreServiceHourRange(part, current) {
			return true
		}
	}
	return false
}

func isWithinStoreServiceHourRange(value string, currentMinute int) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	value = strings.NewReplacer("~", "-", "至", "-", "到", "-").Replace(value)
	pieces := strings.Split(value, "-")
	if len(pieces) != 2 {
		return false
	}
	start, ok := parseStoreServiceClock(pieces[0])
	if !ok {
		return false
	}
	end, ok := parseStoreServiceClock(pieces[1])
	if !ok {
		return false
	}
	if start == end {
		return true
	}
	if start < end {
		return currentMinute >= start && currentMinute <= end
	}
	return currentMinute >= start || currentMinute <= end
}

func parseStoreServiceClock(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	var hour, minute int
	if strings.Contains(value, ":") {
		if _, err := fmt.Sscanf(value, "%d:%d", &hour, &minute); err != nil {
			return 0, false
		}
	} else {
		if _, err := fmt.Sscanf(value, "%d", &hour); err != nil {
			return 0, false
		}
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func uniquePositiveInt64sFromStrings(values []string) []int64 {
	seen := make(map[int64]struct{}, len(values))
	ret := make([]int64, 0, len(values))
	for _, value := range values {
		var id int64
		_, _ = fmt.Sscan(strings.TrimSpace(value), &id)
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ret = append(ret, id)
	}
	return ret
}
