package services

import (
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var ConversationRuntimeModeService = &conversationRuntimeModeService{}

type conversationRuntimeModeService struct{}

type ConversationRuntimeModeDecision struct {
	Mode           enums.ConversationRuntimeMode
	AIReplyAllowed bool
	ReasonCode     string
}

func (s *conversationRuntimeModeService) Resolve(conversationID, tenantID int64) ConversationRuntimeModeDecision {
	db := sqls.DB()
	if db == nil || conversationID <= 0 || tenantID <= 0 {
		return runtimeModeDecision(enums.ConversationRuntimeModeAIDegraded, false, "runtime_scope_invalid")
	}
	conversation := repositories.ConversationRepository.GetInTenant(db, conversationID, tenantID)
	return s.ResolveDB(db, conversation, nil)
}

// ResolveDB is the only projection that interprets Conversation, route,
// assignee, resume and protocol-instance state into AI reply eligibility.
// Callers may still validate their own immutable scope/version invariants, but
// must not independently reinterpret whether the conversation is AI- or
// human-controlled.
func (s *conversationRuntimeModeService) ResolveDB(db *gorm.DB, conversation *models.Conversation, route *models.ConversationRouteState) ConversationRuntimeModeDecision {
	if db == nil || conversation == nil || conversation.ID <= 0 || conversation.TenantID <= 0 {
		return runtimeModeDecision(enums.ConversationRuntimeModeAIDegraded, false, "runtime_scope_invalid")
	}
	if conversation.Status == enums.IMConversationStatusClosed || conversation.ClosedAt != nil {
		return runtimeModeDecision(enums.ConversationRuntimeModeClosed, false, "conversation_closed")
	}
	if route == nil {
		route = repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, conversation.TenantID)
	}
	if route == nil {
		return runtimeModeDecision(enums.ConversationRuntimeModeAIDegraded, false, "route_missing")
	}
	if route.TenantID != conversation.TenantID || route.ConversationID != conversation.ID ||
		route.StoreID != conversation.StoreID || route.StoreStaffBindingID != conversation.StoreStaffBindingID {
		return runtimeModeDecision(enums.ConversationRuntimeModeAIDegraded, false, "route_scope_invalid")
	}
	if route.RouteStatus == enums.ConversationRouteStatusClosed {
		return runtimeModeDecision(enums.ConversationRuntimeModeClosed, false, "route_closed")
	}
	if conversation.CurrentAssigneeID > 0 {
		return runtimeModeDecision(enums.ConversationRuntimeModeHumanActive, false, "human_assignee_active")
	}
	if conversation.ServiceMode == enums.IMConversationServiceModeHumanOnly {
		return runtimeModeDecision(enums.ConversationRuntimeModeHumanPending, false, "human_only_service_mode")
	}
	if strings.TrimSpace(route.PendingAction) == string(enums.ConversationPendingActionHumanHandoff) &&
		route.RouteStatus != enums.ConversationRouteStatusAIFallback {
		return runtimeModeDecision(enums.ConversationRuntimeModeHumanPending, false, "human_handoff_pending")
	}
	switch route.RouteStatus {
	case enums.ConversationRouteStatusStoreWecomManual, enums.ConversationRouteStatusHQAgentDeskServing:
		if s.resumePendingDB(db, conversation) {
			return runtimeModeDecision(enums.ConversationRuntimeModeResumePending, false, "ai_resume_pending")
		}
		return runtimeModeDecision(enums.ConversationRuntimeModeHumanActive, false, "human_route_active")
	case enums.ConversationRouteStatusHQAgentDeskPending:
		if s.resumePendingDB(db, conversation) {
			return runtimeModeDecision(enums.ConversationRuntimeModeResumePending, false, "ai_resume_pending")
		}
		return runtimeModeDecision(enums.ConversationRuntimeModeHumanPending, false, "human_route_pending")
	case enums.ConversationRouteStatusAIServing, enums.ConversationRouteStatusAIFallback:
		return s.resolveAIRouteDB(db, conversation, route)
	default:
		return runtimeModeDecision(enums.ConversationRuntimeModeAIDegraded, false, "route_status_unknown")
	}
}

func (s *conversationRuntimeModeService) resolveAIRouteDB(db *gorm.DB, conversation *models.Conversation, route *models.ConversationRouteState) ConversationRuntimeModeDecision {
	if conversation.ServiceMode != enums.IMConversationServiceModeAIOnly && conversation.ServiceMode != enums.IMConversationServiceModeAIFirst {
		return runtimeModeDecision(enums.ConversationRuntimeModeAIDegraded, false, "ai_service_mode_disabled")
	}
	if route.WxWorkInstanceID <= 0 {
		return runtimeModeDecision(enums.ConversationRuntimeModeAIDegraded, false, "instance_missing")
	}
	instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(db, route.WxWorkInstanceID, conversation.TenantID)
	if instance == nil || instance.StoreID != conversation.StoreID ||
		instance.StoreStaffBindingID != conversation.StoreStaffBindingID {
		return runtimeModeDecision(enums.ConversationRuntimeModeAIDegraded, false, "instance_scope_invalid")
	}
	if instance.Status != enums.StatusOk || !instance.AIReplyEnabled {
		return runtimeModeDecision(enums.ConversationRuntimeModeAIDegraded, false, "ai_reply_disabled")
	}
	if route.RouteStatus == enums.ConversationRouteStatusAIFallback {
		return runtimeModeDecision(enums.ConversationRuntimeModeAIDegraded, true, "ai_fallback_active")
	}
	return runtimeModeDecision(enums.ConversationRuntimeModeAIActive, true, "ai_route_active")
}

func (s *conversationRuntimeModeService) resumePendingDB(db *gorm.DB, conversation *models.Conversation) bool {
	if db == nil || conversation == nil || !db.Migrator().HasTable(&models.AIManualResumeTask{}) {
		return false
	}
	return repositories.AIManualResumeTaskRepository.Take(
		db,
		"tenant_id = ? AND conversation_id = ? AND task_status IN ?",
		conversation.TenantID,
		conversation.ID,
		[]string{aiManualResumeTaskReady, aiManualResumeTaskRunning, aiManualResumeTaskRetry, aiManualResumeTaskBlockedAIDisabled},
	) != nil
}

func runtimeModeDecision(mode enums.ConversationRuntimeMode, allowed bool, reason string) ConversationRuntimeModeDecision {
	return ConversationRuntimeModeDecision{Mode: mode, AIReplyAllowed: allowed, ReasonCode: strings.TrimSpace(reason)}
}
