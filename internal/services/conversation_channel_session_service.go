package services

import (
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	conversationSessionReasonInitial           = "initial"
	conversationSessionReasonInstanceChanged   = "instance_changed"
	conversationSessionReasonConversationOpen  = "conversation_reopened"
	conversationSessionReasonInactivity        = "inactivity"
	conversationSessionReasonManualInheritance = "manual_inheritance"
)

var ConversationChannelSessionService = newConversationChannelSessionService()

type conversationChannelSessionService struct{}

func newConversationChannelSessionService() *conversationChannelSessionService {
	return &conversationChannelSessionService{}
}

func (s *conversationChannelSessionService) ListInTenant(conversationID, tenantID int64) []models.ConversationChannelSession {
	return repositories.ConversationChannelSessionRepository.FindByConversation(sqls.DB(), tenantID, conversationID)
}

func (s *conversationChannelSessionService) PrepareInbound(conversationID int64, instance *models.WxWorkProtocolInstance, now time.Time) (int, error) {
	if instance == nil || instance.ID <= 0 || instance.TenantID <= 0 || !isActivatedCurrentWxWorkProtocolInstance(instance) {
		return 0, errorsx.InvalidParam("企微员工号实例不存在、未完成替换验证、已停用或已被替换")
	}
	if instance.StoreID <= 0 || instance.StoreStaffBindingID <= 0 {
		return 0, errorsx.InvalidParam("企微员工号缺少门店或门店员工号绑定")
	}
	if now.IsZero() {
		now = time.Now()
	}

	resultSessionNo := 0
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		currentInstance := repositories.WxWorkProtocolInstanceRepository.GetForUpdateInTenant(ctx.Tx, instance.ID, instance.TenantID)
		if currentInstance == nil || !isActivatedCurrentWxWorkProtocolInstance(currentInstance) {
			return errorsx.InvalidParam("企微员工号实例不存在、未完成替换验证、已停用或已被替换")
		}
		conversation, err := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, conversationID, instance.TenantID)
		if err != nil {
			return err
		}
		if conversation == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		binding, store, err := s.requireInstanceScopeDB(ctx.Tx, currentInstance)
		if err != nil {
			return err
		}
		if conversation.StoreID != store.ID || conversation.StoreStaffBindingID != binding.ID {
			return errorsx.InvalidParam("会话与企微员工号的门店员工范围不一致")
		}
		expectedThreadKey := buildStoreConversationThreadKey(conversation.TenantID, store.ID, conversation.CustomerID, binding.ID)
		if conversation.ThreadKey == nil || strings.TrimSpace(*conversation.ThreadKey) != expectedThreadKey {
			return errorsx.InvalidParam("会话线程键与门店员工范围不一致")
		}

		state, err := ConversationRouteService.ensureWithDB(ctx.Tx, conversation.ID)
		if err != nil {
			return err
		}
		state, err = repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
		if err != nil {
			return err
		}
		if state == nil {
			return errorsx.InvalidParam("会话路由不存在")
		}
		if state.StoreID > 0 && state.StoreID != store.ID {
			return errorsx.InvalidParam("会话路由门店范围不一致")
		}
		if state.StoreStaffBindingID > 0 && state.StoreStaffBindingID != binding.ID {
			return errorsx.InvalidParam("会话路由门店员工范围不一致")
		}

		currentSessionNo := state.SessionNo
		if currentSessionNo <= 0 {
			currentSessionNo = 1
		}
		activeSession, err := repositories.ConversationChannelSessionRepository.FindActiveForUpdate(ctx.Tx, conversation.TenantID, conversation.ID)
		if err != nil {
			return err
		}
		currentChannelSession, err := repositories.ConversationChannelSessionRepository.GetForUpdateByConversationSession(ctx.Tx, conversation.TenantID, conversation.ID, currentSessionNo)
		if err != nil {
			return err
		}
		instanceChanged := state.WxWorkInstanceID > 0 && state.WxWorkInstanceID != currentInstance.ID
		if activeSession != nil && activeSession.WxWorkInstanceID > 0 && activeSession.WxWorkInstanceID != currentInstance.ID {
			instanceChanged = true
		}
		reopened := conversation.Status == enums.IMConversationStatusClosed || state.RouteStatus == enums.ConversationRouteStatusClosed
		inactive := !reopened && !conversation.LastActiveAt.IsZero() && now.Sub(conversation.LastActiveAt) >= defaultConversationSessionGap
		if inactive && activeSession != nil && activeSession.SessionNo == currentSessionNo &&
			activeSession.StartedAt.After(conversation.LastActiveAt) && now.Sub(activeSession.StartedAt) < defaultConversationSessionGap {
			inactive = false
		}
		endedSession := currentChannelSession != nil && (currentChannelSession.EndedAt != nil || currentChannelSession.Status != enums.StatusOk)
		reusePendingManualSession := false
		if reopened && !instanceChanged && !endedSession && currentChannelSession != nil &&
			currentChannelSession.StartReason == conversationSessionReasonManualInheritance &&
			currentChannelSession.WxWorkInstanceID == currentInstance.ID {
			messageCount, countErr := repositories.MessageRepository.CountByConversationSessionInTenant(
				ctx.Tx,
				conversation.TenantID,
				conversation.ID,
				currentSessionNo,
			)
			if countErr != nil {
				return countErr
			}
			reusePendingManualSession = messageCount == 0
		}
		startNew := instanceChanged || (reopened && !reusePendingManualSession) || inactive || endedSession
		startReason := conversationSessionReasonInitial
		if instanceChanged {
			startReason = conversationSessionReasonInstanceChanged
		} else if reopened {
			startReason = conversationSessionReasonConversationOpen
		} else if inactive {
			startReason = conversationSessionReasonInactivity
		} else if endedSession {
			startReason = conversationSessionReasonConversationOpen
		}

		resultSessionNo = currentSessionNo
		if startNew {
			resultSessionNo = currentSessionNo + 1
			if activeSession != nil {
				if err := repositories.ConversationChannelSessionRepository.UpdatesInTenant(ctx.Tx, activeSession.ID, conversation.TenantID, map[string]any{
					"ended_at":         now,
					"status":           enums.StatusDisabled,
					"updated_at":       now,
					"update_user_name": "system",
				}); err != nil {
					return err
				}
			}
		}

		knowledgeBaseID, err := WxWorkProtocolInstanceService.resolveStoreKnowledgeBaseIDDB(ctx.Tx, conversation.TenantID, store.ID)
		if err != nil {
			return err
		}
		routeUpdates := map[string]any{
			"store_id":               store.ID,
			"store_staff_binding_id": binding.ID,
			"knowledge_base_id":      knowledgeBaseID,
			"wx_work_instance_id":    currentInstance.ID,
			"session_no":             resultSessionNo,
			"updated_at":             now,
			"update_user_name":       "system",
		}
		if startNew || state.SessionStartedAt == nil {
			routeUpdates["session_started_at"] = now
		}
		if reopened || inactive {
			if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversation.ID, now); err != nil {
				return err
			}
			routeUpdates["route_status"] = enums.ConversationRouteStatusAIServing
			routeUpdates["route_target"] = "ai"
			routeUpdates["manual_expire_at"] = nil
			routeUpdates["pending_action"] = ""
			routeUpdates["pending_action_payload"] = ""
			routeUpdates["pending_action_expire_at"] = nil
			routeUpdates["handoff_reason"] = ""
			routeUpdates["need_human_follow_up"] = false
			if err := s.reopenConversationDB(ctx.Tx, conversation, now); err != nil {
				return err
			}
		}
		if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, state.ID, conversation.TenantID, routeUpdates); err != nil {
			return err
		}

		channelSession := repositories.ConversationChannelSessionRepository.TakeByConversationSession(ctx.Tx, conversation.TenantID, conversation.ID, resultSessionNo)
		if channelSession == nil {
			if err := repositories.ConversationChannelSessionRepository.Create(ctx.Tx, s.buildSession(ctx.Tx, conversation, binding, currentInstance, resultSessionNo, startReason, now)); err != nil {
				return err
			}
		} else if channelSession.StoreID != store.ID || channelSession.StoreStaffBindingID != binding.ID || channelSession.WxWorkInstanceID != currentInstance.ID {
			return errorsx.InvalidParam("会话段与当前门店员工号实例不一致")
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return resultSessionNo, nil
}

func (s *conversationChannelSessionService) StartManualInheritanceDB(
	ctx *sqls.TxContext,
	conversation *models.Conversation,
	binding *models.StoreStaffBinding,
	instance *models.WxWorkProtocolInstance,
	now time.Time,
	operatorID int64,
	operatorName string,
) (int, error) {
	if ctx == nil || ctx.Tx == nil || conversation == nil || binding == nil || instance == nil {
		return 0, errorsx.InvalidParam("会话继承上下文不完整")
	}
	state, err := ConversationRouteService.ensureWithDB(ctx.Tx, conversation.ID)
	if err != nil {
		return 0, err
	}
	state, err = repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
	if err != nil {
		return 0, err
	}
	if state == nil {
		return 0, errorsx.InvalidParam("会话路由不存在")
	}
	activeSession, err := repositories.ConversationChannelSessionRepository.FindActiveForUpdate(ctx.Tx, conversation.TenantID, conversation.ID)
	if err != nil {
		return 0, err
	}
	currentSessionNo := state.SessionNo
	if currentSessionNo <= 0 {
		currentSessionNo = 1
	}
	if activeSession != nil && activeSession.SessionNo > currentSessionNo {
		currentSessionNo = activeSession.SessionNo
	}
	currentSession, err := repositories.ConversationChannelSessionRepository.GetForUpdateByConversationSession(
		ctx.Tx,
		conversation.TenantID,
		conversation.ID,
		currentSessionNo,
	)
	if err != nil {
		return 0, err
	}
	nextSessionNo := currentSessionNo + 1
	if activeSession == nil && currentSession == nil && currentSessionNo == 1 {
		nextSessionNo = 1
	}
	if activeSession != nil {
		if err := repositories.ConversationChannelSessionRepository.UpdatesInTenant(ctx.Tx, activeSession.ID, conversation.TenantID, map[string]any{
			"ended_at":         now,
			"status":           enums.StatusDisabled,
			"updated_at":       now,
			"update_user_id":   operatorID,
			"update_user_name": operatorName,
		}); err != nil {
			return 0, err
		}
	}
	knowledgeBaseID, err := WxWorkProtocolInstanceService.resolveStoreKnowledgeBaseIDDB(ctx.Tx, conversation.TenantID, binding.StoreID)
	if err != nil {
		return 0, err
	}
	if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, state.ID, conversation.TenantID, map[string]any{
		"store_id":                 binding.StoreID,
		"store_staff_binding_id":   binding.ID,
		"knowledge_base_id":        knowledgeBaseID,
		"wx_work_instance_id":      instance.ID,
		"session_no":               nextSessionNo,
		"session_started_at":       now,
		"route_status":             enums.ConversationRouteStatusAIServing,
		"route_target":             "ai",
		"manual_expire_at":         nil,
		"pending_action":           "",
		"pending_action_payload":   "",
		"pending_action_expire_at": nil,
		"handoff_reason":           "",
		"need_human_follow_up":     false,
		"updated_at":               now,
		"update_user_id":           operatorID,
		"update_user_name":         operatorName,
	}); err != nil {
		return 0, err
	}
	if existing := repositories.ConversationChannelSessionRepository.TakeByConversationSession(ctx.Tx, conversation.TenantID, conversation.ID, nextSessionNo); existing != nil {
		return 0, errorsx.InvalidParam("目标会话段编号冲突，请刷新后重试")
	}
	item := s.buildSession(ctx.Tx, conversation, binding, instance, nextSessionNo, conversationSessionReasonManualInheritance, now)
	item.CreateUserID = operatorID
	item.CreateUserName = operatorName
	item.UpdateUserID = operatorID
	item.UpdateUserName = operatorName
	if err := repositories.ConversationChannelSessionRepository.Create(ctx.Tx, item); err != nil {
		return 0, err
	}
	return nextSessionNo, nil
}

func (s *conversationChannelSessionService) requireInstanceScopeDB(db *gorm.DB, instance *models.WxWorkProtocolInstance) (*models.StoreStaffBinding, *models.Store, error) {
	binding := repositories.StoreStaffBindingRepository.GetInTenant(db, instance.StoreStaffBindingID, instance.TenantID)
	if binding == nil || binding.Status != enums.StatusOk || binding.StoreID != instance.StoreID {
		return nil, nil, errorsx.InvalidParam("企微员工号缺少有效门店员工号绑定")
	}
	if err := StoreStaffBindingService.validateBindingOwnerDB(db, binding); err != nil {
		return nil, nil, err
	}
	store := repositories.StoreRepository.GetInTenant(db, binding.StoreID, instance.TenantID)
	if store == nil || store.Status != enums.StatusOk {
		return nil, nil, errorsx.InvalidParam("企微员工号所属门店不存在或已停用")
	}
	return binding, store, nil
}

func (s *conversationChannelSessionService) buildSession(db *gorm.DB, conversation *models.Conversation, binding *models.StoreStaffBinding, instance *models.WxWorkProtocolInstance, sessionNo int, reason string, now time.Time) *models.ConversationChannelSession {
	staffDisplayName := ""
	if user := repositories.UserRepository.GetInTenant(db, binding.UserID, binding.TenantID); user != nil {
		staffDisplayName = strings.TrimSpace(utils.RepairMojibakeText(user.Nickname))
		if staffDisplayName == "" {
			staffDisplayName = strings.TrimSpace(utils.RepairMojibakeText(user.Username))
		}
	}
	return &models.ConversationChannelSession{
		TenantID:                  conversation.TenantID,
		ConversationID:            conversation.ID,
		SessionNo:                 sessionNo,
		StoreID:                   binding.StoreID,
		StoreStaffBindingID:       binding.ID,
		WxWorkInstanceID:          instance.ID,
		ChannelID:                 instance.ChannelID,
		StartReason:               reason,
		StoreStaffDisplayName:     staffDisplayName,
		WxWorkEmployeeDisplayName: strings.TrimSpace(utils.RepairMojibakeText(instance.EmployeeName)),
		StartedAt:                 now,
		Status:                    enums.StatusOk,
		AuditFields:               utils.BuildAuditFields(nil),
	}
}

func (s *conversationChannelSessionService) reopenConversationDB(db *gorm.DB, conversation *models.Conversation, now time.Time) error {
	return repositories.ConversationRepository.UpdatesInTenant(db, conversation.ID, conversation.TenantID, map[string]any{
		"status":              enums.IMConversationStatusAIServing,
		"priority":            0,
		"dispatch_weight":     1,
		"current_assignee_id": int64(0),
		"current_team_id":     int64(0),
		"handoff_at":          nil,
		"handoff_reason":      "",
		"ai_reply_rounds":     0,
		"closed_at":           nil,
		"closed_by":           int64(0),
		"close_reason":        "",
		"updated_at":          now,
		"update_user_id":      int64(0),
		"update_user_name":    "system",
	})
}
