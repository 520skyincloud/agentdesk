package services

import (
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	AIUsageMetricSourceUpstreamActual    = "upstream_actual"
	AIUsageMetricSourceProviderOperation = "provider_operation"
	AIUsageMetricSourceEstimatedOnly     = "estimated_only"
)

var AIUsageEventService = newAIUsageEventService()

type aiUsageEventService struct{}

func newAIUsageEventService() *aiUsageEventService { return &aiUsageEventService{} }

// Record inserts once by EventKey. Existing events are never updated because
// retries and corrections must remain separately auditable usage evidence.
func (s *aiUsageEventService) Record(event models.AIUsageEvent) error {
	event.EventKey = strings.TrimSpace(event.EventKey)
	if event.EventKey == "" {
		return nil
	}
	event.RequestID = strings.TrimSpace(event.RequestID)
	event.Stage = strings.TrimSpace(event.Stage)
	event.Provider = strings.TrimSpace(event.Provider)
	event.Model = strings.TrimSpace(event.Model)
	event.ModelSource = strings.TrimSpace(event.ModelSource)
	event.UpstreamRequestID = strings.TrimSpace(event.UpstreamRequestID)
	event.Gateway = strings.TrimSpace(event.Gateway)
	event.GatewayRequestID = strings.TrimSpace(event.GatewayRequestID)
	event.GatewayUpstreamID = strings.TrimSpace(event.GatewayUpstreamID)
	event.OperationType = strings.TrimSpace(event.OperationType)
	event.MetricSource = strings.TrimSpace(event.MetricSource)
	event.Status = strings.TrimSpace(event.Status)
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	if event.ConversationID > 0 && (event.StoreID <= 0 || event.WxWorkInstanceID <= 0) {
		if route := ConversationRouteService.GetByConversationID(event.ConversationID); route != nil {
			if event.StoreID <= 0 {
				event.StoreID = route.StoreID
			}
			if event.WxWorkInstanceID <= 0 {
				event.WxWorkInstanceID = route.WxWorkInstanceID
			}
		}
	}
	if event.WxWorkInstanceID > 0 && event.CompanyID <= 0 {
		if instance := WxWorkProtocolInstanceService.Get(event.WxWorkInstanceID); instance != nil {
			event.CompanyID = instance.CompanyID
		}
	}
	if err := repositories.AIUsageEventRepository.CreateIfAbsent(sqls.DB(), &event); err != nil {
		return err
	}
	return AIUsageGatewayCallService.RecordFromEvent(event)
}
