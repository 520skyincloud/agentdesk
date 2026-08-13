package services

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/usagex"
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
		HTTPStatus:           event.GatewayHTTPStatus,
		ReconcileStatus:      AIUsageReconcilePending,
		LastErrorClass:       strings.TrimSpace(event.ErrorClass),
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	return repositories.AIUsageGatewayCallRepository.CreateIfAbsent(sqls.DB(), item)
}

func (s *aiUsageGatewayCallService) RecordReceiptsFromEvent(event models.AIUsageEvent, receipts []usagex.Receipt) error {
	for index := range receipts {
		receipt := receipts[index]
		gateway := strings.TrimSpace(receipt.Gateway)
		gatewayRequestID := strings.TrimSpace(receipt.RequestID)
		if gateway == "" || gatewayRequestID == "" {
			continue
		}
		attempt := receipt.Attempt
		if attempt <= 0 {
			attempt = index + 1
		}
		item := &models.AIUsageGatewayCall{
			TenantID: event.TenantID,
			CallKey:  fmt.Sprintf("%s:%d:%d:%d:%s", gateway, event.TenantID, event.StoreID, event.StoreStaffBindingID, gatewayRequestID),
			EventKey: event.EventKey, StoreID: event.StoreID, StoreStaffBindingID: event.StoreStaffBindingID,
			WxWorkInstanceID: event.WxWorkInstanceID, ConversationID: event.ConversationID, MessageID: event.MessageID,
			LocalRequestID: event.RequestID, Stage: event.Stage,
			ModelProfileID: event.ModelProfileID, ModelProfileRevision: event.ModelProfileRevision,
			UsageSlot: event.UsageSlot, CredentialRevision: event.CredentialRevision, KeyFingerprint: event.KeyFingerprint,
			Gateway: gateway, GatewayRequestID: gatewayRequestID, UpstreamRequestID: receipt.UpstreamRequestID,
			StartedAt: receipt.StartedAt, FinishedAt: receipt.FinishedAt, LatencyMS: receipt.LatencyMS(), HTTPStatus: receipt.StatusCode,
			ReconcileStatus: AIUsageReconcilePending,
			LastError:       fmt.Sprintf("attempt=%d provider_status=%s provider_code=%s", attempt, strings.TrimSpace(receipt.ProviderStatus), strings.TrimSpace(receipt.ProviderCode)),
			LastErrorClass:  strings.TrimSpace(receipt.ErrorClass), CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		if err := repositories.AIUsageGatewayCallRepository.CreateIfAbsent(sqls.DB(), item); err != nil {
			return err
		}
	}
	return nil
}
