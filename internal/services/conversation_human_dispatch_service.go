package services

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/events"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/eventbus"
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
	teamIDs := orderedPositiveIDs(aiAgent.TeamIDs)
	activeTeamIDs := ConversationDispatchService.findActiveScheduleTeamIDs(teamIDs, time.Now())
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
	if statusResult := s.recentHandoffResult(conversationID); statusResult != nil {
		return statusResult, nil
	}
	teamIDs := orderedPositiveIDs(aiAgent.TeamIDs)
	activeTeamIDs := ConversationDispatchService.findActiveScheduleTeamIDs(teamIDs, time.Now())
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
	return repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
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
	route := ConversationRouteService.GetByConversationID(conversationID)
	if route == nil || route.WxWorkInstanceID <= 0 {
		return StoreStaffRuntimeConfig{ManagedMode: constants.StoreManagedModeSemi, FallbackToHQ: true, ManualTimeoutMinutes: 10, NoWxWorkInstance: true}
	}
	return StoreStaffBindingService.ResolveForInstance(WxWorkProtocolInstanceService.Get(route.WxWorkInstanceID))
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
			return hasActiveTeamSchedule
		}
		return isWithinStoreServiceHours(runtime.ServiceHours, now)
	}
}

func (s *conversationHumanDispatchService) storeRoomConfigured(runtime StoreStaffRuntimeConfig) bool {
	return runtime.StoreRoomNotifyEnabled && strings.TrimSpace(runtime.StoreRoomConversationID) != ""
}

func (s *conversationHumanDispatchService) ApplyHumanOnlyCreate(conversationID int64, aiAgent models.AIAgent) (*HandoffDecisionResult, error) {
	teamIDs := orderedPositiveIDs(aiAgent.TeamIDs)
	activeTeamIDs := ConversationDispatchService.findActiveScheduleTeamIDs(teamIDs, time.Now())
	if len(activeTeamIDs) == 0 {
		if err := s.moveToGlobalPool(conversationID, aiAgent.Name); err != nil {
			return nil, err
		}
		if err := s.sendAIText(conversationID, aiAgent.ID, HandoffWaitingMessage); err != nil {
			return nil, err
		}
		return &HandoffDecisionResult{Decision: HandoffDecisionGlobalPool, Message: HandoffWaitingMessage}, nil
	}
	return s.dispatchAfterHandoff(conversationID, aiAgent.ID, activeTeamIDs, "仅人工模式新会话", false)
}

func (s *conversationHumanDispatchService) DispatchPendingConversation(conversationID int64, aiAgent models.AIAgent) (*HandoffDecisionResult, error) {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	if conversation.Status != enums.IMConversationStatusPending || conversation.CurrentAssigneeID > 0 {
		return nil, errorsx.InvalidParam("只有待接入未分配会话允许自动分配")
	}
	activeTeamIDs := ConversationDispatchService.findActiveScheduleTeamIDs(orderedPositiveIDs(aiAgent.TeamIDs), time.Now())
	if len(activeTeamIDs) == 0 {
		return &HandoffDecisionResult{Decision: HandoffDecisionOffHours}, nil
	}
	route := repositories.ConversationRouteStateRepository.Take(sqls.DB(), "conversation_id = ?", conversationID)
	candidates, _, err := ConversationDispatchService.pickDispatchCandidates(activeTeamIDs, route, time.Now())
	if err != nil {
		return nil, err
	}
	if len(candidates) > 0 {
		dispatched, err := ConversationDispatchService.tryAssignConversation(conversationID, candidates[0].profile, "自动分配")
		if err != nil {
			return nil, err
		}
		if dispatched != nil {
			WsService.PublishConversationChanged(dispatched, enums.IMRealtimeEventConversationAssigned)
			return &HandoffDecisionResult{
				Decision:   HandoffDecisionAssigned,
				TeamID:     dispatched.CurrentTeamID,
				AssigneeID: dispatched.CurrentAssigneeID,
			}, nil
		}
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

func (s *conversationHumanDispatchService) dispatchAfterHandoff(conversationID, aiAgentID int64, activeTeamIDs []int64, reason string, publishAssignEvent bool) (*HandoffDecisionResult, error) {
	return s.dispatchAfterHandoffWithRequestID(conversationID, aiAgentID, activeTeamIDs, reason, publishAssignEvent, "")
}

func (s *conversationHumanDispatchService) dispatchAfterHandoffWithRequestID(conversationID, aiAgentID int64, activeTeamIDs []int64, reason string, publishAssignEvent bool, requestID string) (*HandoffDecisionResult, error) {
	route := repositories.ConversationRouteStateRepository.Take(sqls.DB(), "conversation_id = ?", conversationID)
	candidates, _, err := ConversationDispatchService.pickDispatchCandidates(activeTeamIDs, route, time.Now())
	if err != nil {
		return nil, err
	}
	if len(candidates) > 0 {
		dispatched, err := ConversationDispatchService.tryAssignConversation(conversationID, candidates[0].profile, "自动分配")
		if err != nil {
			return nil, err
		}
		if dispatched != nil {
			WsService.PublishConversationChanged(dispatched, enums.IMRealtimeEventConversationAssigned)
			if publishAssignEvent {
				eventbus.PublishAsync(context.Background(), events.ConversationAssignedEvent{
					ConversationID: dispatched.ID,
					ToUserID:       dispatched.CurrentAssigneeID,
					OperatorID:     systemDispatchPrincipal().UserID,
					Reason:         "自动分配",
					AssignType:     events.ConversationAssignTypeAutoAssign,
				})
			}
			return &HandoffDecisionResult{
				Decision:   HandoffDecisionAssigned,
				TeamID:     dispatched.CurrentTeamID,
				AssigneeID: dispatched.CurrentAssigneeID,
				Message:    HandoffWaitingMessage,
			}, nil
		}
	}

	teamID := activeTeamIDs[0]
	teamPoolConversation, err := s.moveToTeamPoolWithRequestID(conversationID, teamID, reason, requestID)
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
	if err := s.recordHandoff(conversationID, aiAgent, trimmedReason, requestID, now); err != nil {
		return err
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(conversationID, trimmedReason, now); err != nil {
		return err
	}
	_ = s.markManualHandoffRequested(conversationID, now)
	s.notifyStoreRoomHandoff(conversationID, trimmedReason)
	return nil
}

func (s *conversationHumanDispatchService) markHQAgentDeskHandoff(conversationID int64, aiAgent models.AIAgent, reason string, requestID string) error {
	now := time.Now()
	trimmedReason := strings.TrimSpace(reason)
	if err := s.recordHandoff(conversationID, aiAgent, trimmedReason, requestID, now); err != nil {
		return err
	}
	if _, err := ConversationRouteService.EnterHQAgentDeskPending(conversationID, trimmedReason, now); err != nil {
		return err
	}
	_ = s.markManualHandoffRequested(conversationID, now)
	s.notifyAgentDeskHandoff(conversationID, trimmedReason)
	return nil
}

func (s *conversationHumanDispatchService) recordHandoff(conversationID int64, aiAgent models.AIAgent, reason string, requestID string, now time.Time) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.ConversationRepository.Updates(ctx.Tx, conversationID, map[string]any{
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
		return ConversationEventLogService.CreateEventWithRequestID(ctx, conversationID, requestID, enums.IMEventTypeTransfer, enums.IMSenderTypeAI, aiAgent.ID, "AI转人工", strings.TrimSpace(reason))
	})
}

func (s *conversationHumanDispatchService) moveToTeamPool(conversationID, teamID int64, reason string) (*models.Conversation, error) {
	return s.moveToTeamPoolWithRequestID(conversationID, teamID, reason, "")
}

func (s *conversationHumanDispatchService) moveToTeamPoolWithRequestID(conversationID, teamID int64, reason string, requestID string) (*models.Conversation, error) {
	now := time.Now()
	var conversation *models.Conversation
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current := repositories.ConversationRepository.Get(ctx.Tx, conversationID)
		if current == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversationID, now); err != nil {
			return err
		}
		if err := repositories.ConversationRepository.Updates(ctx.Tx, conversationID, map[string]any{
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
	if _, err := ConversationRouteService.EnterHQAgentDeskPending(conversationID, strings.TrimSpace(reason), now); err != nil {
		return nil, err
	}
	s.notifyAgentDeskHandoff(conversationID, strings.TrimSpace(reason))
	return conversation, nil
}

func (s *conversationHumanDispatchService) moveToGlobalPool(conversationID int64, operatorName string) error {
	now := time.Now()
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation := repositories.ConversationRepository.Get(ctx.Tx, conversationID)
		if conversation == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		if err := repositories.ConversationRepository.Updates(ctx.Tx, conversationID, map[string]any{
			"status":              enums.IMConversationStatusPending,
			"current_team_id":     0,
			"current_assignee_id": 0,
			"update_user_id":      0,
			"update_user_name":    operatorName,
			"updated_at":          now,
		}); err != nil {
			return err
		}
		return ConversationEventLogService.CreateEvent(ctx, conversationID, enums.IMEventTypeTransfer, enums.IMSenderTypeSystem, 0, "会话进入全局待接入", ConversationService.buildEventPayload(map[string]any{
			"fromStatus": conversation.Status,
			"toStatus":   enums.IMConversationStatusPending,
			"decision":   string(HandoffDecisionGlobalPool),
		}))
	}); err != nil {
		return err
	}
	if _, err := ConversationRouteService.EnterHQAgentDeskPending(conversationID, "进入全局待接入", now); err != nil {
		return err
	}
	s.notifyAgentDeskHandoff(conversationID, "进入全局待接入")
	return nil
}

func (s *conversationHumanDispatchService) notifyAgentDeskHandoff(conversationID int64, reason string) {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return
	}
	userIDs := AgentProfileService.GetActiveAgentUserIDs()
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
		_, err := NotificationService.CreateAndPush(request.CreateNotificationRequest{
			RecipientUserID:  userID,
			Title:            "新的转人工请求",
			Content:          content,
			NotificationType: "manual_handoff_created",
			BizType:          "conversation",
			BizID:            conversation.ID,
			ActionURL:        fmt.Sprintf("/dashboard/conversations?conversationId=%d", conversation.ID),
		})
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
	route := ConversationRouteService.GetByConversationID(conversationID)
	if route == nil || route.WxWorkInstanceID <= 0 {
		return
	}
	instance := WxWorkProtocolInstanceService.Get(route.WxWorkInstanceID)
	if instance == nil {
		return
	}
	runtime := StoreStaffBindingService.ResolveForInstance(instance)
	if !s.storeRoomConfigured(runtime) {
		return
	}
	content := s.buildStoreRoomHandoffNotice(conversation, instance, reason)
	atList := uniqueNonBlankStrings(strings.Split(runtime.StoreRoomAtList, ","))
	if err := ChannelMessageOutboxService.EnqueueWxWorkProtocolStoreRoomNotice(conversationID, instance.ID, runtime.StoreRoomConversationID, content, atList); err != nil {
		slog.Warn("enqueue store room handoff notice failed", "conversation_id", conversationID, "wx_work_instance_id", instance.ID, "error", err)
	}
}

func (s *conversationHumanDispatchService) buildStoreRoomHandoffNotice(conversation *models.Conversation, instance *models.WxWorkProtocolInstance, reason string) string {
	lines := []string{"有客人需要人工接待"}
	if name := strings.TrimSpace(conversation.CustomerName); name != "" {
		lines = append(lines, "客户："+name)
	}
	if storeName := strings.TrimSpace(instance.StoreNavigationName); storeName != "" {
		lines = append(lines, "门店："+storeName)
	}
	if summary := strings.TrimSpace(ConversationService.BuildConversationSummary(conversation)); summary != "" {
		lines = append(lines, "摘要："+summary)
	}
	if trimmedReason := strings.TrimSpace(reason); trimmedReason != "" {
		lines = append(lines, "原因："+trimmedReason)
	}
	lines = append(lines, fmt.Sprintf("会话ID：%d", conversation.ID))
	return strings.Join(lines, "\n")
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
