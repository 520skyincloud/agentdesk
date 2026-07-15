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
	tenantID, err := s.resolveTenantID(event)
	if err != nil {
		return err
	}
	event.TenantID = tenantID
	if event.ConversationID > 0 && (event.StoreID <= 0 || event.WxWorkInstanceID <= 0) {
		if route := ConversationRouteService.GetByConversationIDInTenant(event.ConversationID, tenantID); route != nil {
			if event.StoreID <= 0 {
				event.StoreID = route.StoreID
			}
			if event.WxWorkInstanceID <= 0 {
				event.WxWorkInstanceID = route.WxWorkInstanceID
			}
		}
	}
	if event.WxWorkInstanceID > 0 && event.CompanyID <= 0 {
		if instance := WxWorkProtocolInstanceService.GetByTenantID(event.WxWorkInstanceID, tenantID); instance != nil {
			event.CompanyID = instance.CompanyID
		}
	}
	if err := repositories.AIUsageEventRepository.CreateIfAbsent(sqls.DB(), &event); err != nil {
		return err
	}
	return AIUsageGatewayCallService.RecordFromEvent(event)
}

func (s *aiUsageEventService) resolveTenantID(event models.AIUsageEvent) (int64, error) {
	tenantID := event.TenantID
	merge := func(entity string, candidate int64) error {
		if candidate <= 0 {
			return fmt.Errorf("AI usage %s has no tenant", entity)
		}
		if tenantID == 0 {
			tenantID = candidate
			return nil
		}
		if tenantID != candidate {
			return fmt.Errorf("AI usage tenant mismatch: %s belongs to tenant %d, expected %d", entity, candidate, tenantID)
		}
		return nil
	}
	if event.ConversationID > 0 {
		item := repositories.ConversationRepository.Get(sqls.DB(), event.ConversationID)
		if item == nil {
			return 0, fmt.Errorf("AI usage conversation does not exist")
		}
		if err := merge("conversation", item.TenantID); err != nil {
			return 0, err
		}
	}
	if event.MessageID > 0 {
		item := repositories.MessageRepository.Get(sqls.DB(), event.MessageID)
		if item == nil {
			return 0, fmt.Errorf("AI usage message does not exist")
		}
		if err := merge("message", item.TenantID); err != nil {
			return 0, err
		}
	}
	if event.KnowledgeBaseID > 0 {
		item := repositories.KnowledgeBaseRepository.Get(sqls.DB(), event.KnowledgeBaseID)
		if item == nil {
			return 0, fmt.Errorf("AI usage knowledge base does not exist")
		}
		if err := merge("knowledge base", item.TenantID); err != nil {
			return 0, err
		}
	}
	if event.WxWorkInstanceID > 0 {
		item := repositories.WxWorkProtocolInstanceRepository.Get(sqls.DB(), event.WxWorkInstanceID)
		if item == nil {
			return 0, fmt.Errorf("AI usage WeCom instance does not exist")
		}
		if err := merge("WeCom instance", item.TenantID); err != nil {
			return 0, err
		}
	}
	if event.StoreID > 0 {
		item := repositories.StoreRepository.Get(sqls.DB(), event.StoreID)
		if item == nil {
			return 0, fmt.Errorf("AI usage store does not exist")
		}
		if err := merge("store", item.TenantID); err != nil {
			return 0, err
		}
	}
	if event.CompanyID > 0 {
		item := repositories.CompanyRepository.Get(sqls.DB(), event.CompanyID)
		if item == nil {
			return 0, fmt.Errorf("AI usage company does not exist")
		}
		if err := merge("company", item.TenantID); err != nil {
			return 0, err
		}
	}
	if tenantID <= 0 {
		return 0, fmt.Errorf("AI usage event has no tenant evidence")
	}
	return tenantID, nil
}
