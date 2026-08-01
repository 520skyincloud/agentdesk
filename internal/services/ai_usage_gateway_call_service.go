package services

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	AIUsageGatewayNewAPI          = "new_api"
	AIUsageReconcilePending       = "pending"
	AIUsageReconcileRetry         = "retry"
	AIUsageReconcileCompleted     = "completed"
	AIUsageMatchStrategyRequestID = "request_id"
	AIUsageMatchConfidenceExact   = "exact"
)

var AIUsageGatewayCallService = newAIUsageGatewayCallService()

type aiUsageGatewayCallService struct{}

func newAIUsageGatewayCallService() *aiUsageGatewayCallService {
	return &aiUsageGatewayCallService{}
}

func (s *aiUsageGatewayCallService) RecordFromEvent(event models.AIUsageEvent) error {
	gateway := strings.TrimSpace(event.Gateway)
	gatewayRequestID := strings.TrimSpace(event.GatewayRequestID)
	if gateway == "" || gatewayRequestID == "" {
		return nil
	}
	if event.TenantID <= 0 {
		return fmt.Errorf("AI usage gateway call has no tenant")
	}
	startedAt := event.CreatedAt
	finishedAt := event.CreatedAt
	if event.CallStartedAt != nil {
		startedAt = *event.CallStartedAt
	}
	if event.CallFinishedAt != nil {
		finishedAt = *event.CallFinishedAt
	}
	item := &models.AIUsageGatewayCall{
		TenantID:             event.TenantID,
		CallKey:              fmt.Sprintf("%s:%d:%d:%d:%s", gateway, event.TenantID, event.StoreID, event.StoreStaffBindingID, gatewayRequestID),
		EventKey:             event.EventKey,
		StoreID:              event.StoreID,
		StoreStaffBindingID:  event.StoreStaffBindingID,
		WxWorkInstanceID:     event.WxWorkInstanceID,
		ConversationID:       event.ConversationID,
		MessageID:            event.MessageID,
		LocalRequestID:       event.RequestID,
		Stage:                event.Stage,
		ModelProfileID:       event.ModelProfileID,
		ModelProfileRevision: event.ModelProfileRevision,
		UsageSlot:            event.UsageSlot,
		CredentialRevision:   event.CredentialRevision,
		KeyFingerprint:       event.KeyFingerprint,
		Gateway:              gateway,
		GatewayRequestID:     gatewayRequestID,
		UpstreamRequestID:    event.GatewayUpstreamID,
		StartedAt:            startedAt,
		FinishedAt:           finishedAt,
		LatencyMS:            finishedAt.Sub(startedAt).Milliseconds(),
		ReconcileStatus:      AIUsageReconcilePending,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	return repositories.AIUsageGatewayCallRepository.CreateIfAbsent(sqls.DB(), item)
}
