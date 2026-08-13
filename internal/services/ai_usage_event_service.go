package services

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	AIUsageMetricSourceUpstreamActual    = "upstream_actual"
	AIUsageMetricSourceProviderOperation = "provider_operation"
	AIUsageMetricSourceEstimatedOnly     = "estimated_only"
	AIModelSourceStoreProfile            = "store_profile_assignment"
)

var AIUsageEventService = newAIUsageEventService()

type aiUsageEventService struct{}

func newAIUsageEventService() *aiUsageEventService { return &aiUsageEventService{} }

func (s *aiUsageEventService) ApplyModelCallAttribution(event *models.AIUsageEvent, resolved *ModelCallConfig) {
	if event == nil || resolved == nil {
		return
	}
	event.TenantID = resolved.TenantID
	event.StoreID = resolved.StoreID
	event.StoreStaffBindingID = resolved.StoreStaffBindingID
	event.Provider = strings.TrimSpace(resolved.Provider)
	event.Model = strings.TrimSpace(resolved.ModelName)
	event.ModelProfileID = resolved.ProfileID
	event.ModelProfileRevision = resolved.ProfileRevision
	event.UsageSlot = string(resolved.UsageCode)
	event.CredentialRevision = resolved.CredentialRevision
	event.KeyFingerprint = strings.TrimSpace(resolved.KeyFingerprint)
	event.ModelSource = AIModelSourceStoreProfile
}

func recordResolvedModelCall(event models.AIUsageEvent, resolved *ModelCallConfig, receipt *usagex.Receipt) {
	if resolved == nil {
		return
	}
	AIUsageEventService.ApplyModelCallAttribution(&event, resolved)
	if receipt != nil {
		event.Gateway = receipt.Gateway
		event.GatewayRequestID = receipt.RequestID
		event.GatewayUpstreamID = receipt.UpstreamRequestID
		event.CallStartedAt = &receipt.StartedAt
		event.CallFinishedAt = &receipt.FinishedAt
		if receipt.LatencyMS() > 0 {
			event.LatencyMS = receipt.LatencyMS()
		}
	}
	_ = AIUsageEventService.Record(event)
}

// Record inserts once by EventKey. Existing events are never updated because
// retries and corrections must remain separately auditable usage evidence.
func (s *aiUsageEventService) Record(event models.AIUsageEvent) error {
	return s.record(event, nil)
}

func (s *aiUsageEventService) RecordWithGatewayReceipts(event models.AIUsageEvent, receipts []usagex.Receipt) error {
	return s.record(event, receipts)
}

func (s *aiUsageEventService) record(event models.AIUsageEvent, receipts []usagex.Receipt) error {
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
	if event.ConversationID > 0 && (event.StoreID <= 0 || event.StoreStaffBindingID <= 0 || event.WxWorkInstanceID <= 0) {
		if route := ConversationRouteService.GetByConversationIDInTenant(event.ConversationID, tenantID); route != nil {
			if event.StoreID <= 0 {
				event.StoreID = route.StoreID
			}
			if event.WxWorkInstanceID <= 0 {
				event.WxWorkInstanceID = route.WxWorkInstanceID
			}
			if event.StoreStaffBindingID <= 0 {
				event.StoreStaffBindingID = route.StoreStaffBindingID
			}
		}
	}
	if err := s.validateStoreBindingScope(event); err != nil {
		return err
	}
	s.enrichStoreModelAttribution(&event)
	if event.RequestCount <= 0 && (event.Model != "" || event.GatewayRequestID != "") {
		event.RequestCount = 1
	}
	if err := repositories.AIUsageEventRepository.CreateIfAbsent(sqls.DB(), &event); err != nil {
		return err
	}
	if len(receipts) > 0 {
		return AIUsageGatewayCallService.RecordReceiptsFromEvent(event, receipts)
	}
	return AIUsageGatewayCallService.RecordFromEvent(event)
}

func (s *aiUsageEventService) validateStoreBindingScope(event models.AIUsageEvent) error {
	if event.StoreID <= 0 {
		if event.StoreStaffBindingID > 0 {
			return fmt.Errorf("AI usage binding has no store scope")
		}
		return nil
	}
	if event.StoreStaffBindingID <= 0 {
		return fmt.Errorf("AI usage store scope has no staff binding")
	}
	binding := repositories.StoreStaffBindingRepository.GetInTenant(sqls.DB(), event.StoreStaffBindingID, event.TenantID)
	if binding == nil || binding.StoreID != event.StoreID {
		return fmt.Errorf("AI usage staff binding does not belong to the store")
	}
	if event.ConversationID > 0 {
		conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), event.ConversationID, event.TenantID)
		if conversation == nil || (conversation.StoreID > 0 && conversation.StoreID != event.StoreID) ||
			(conversation.StoreStaffBindingID > 0 && conversation.StoreStaffBindingID != event.StoreStaffBindingID) {
			return fmt.Errorf("AI usage conversation store scope mismatch")
		}
	}
	if event.WxWorkInstanceID > 0 {
		instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(sqls.DB(), event.WxWorkInstanceID, event.TenantID)
		if instance == nil || instance.StoreID != event.StoreID || instance.StoreStaffBindingID != event.StoreStaffBindingID {
			return fmt.Errorf("AI usage WeCom instance store scope mismatch")
		}
	}
	if event.KnowledgeBaseID > 0 {
		knowledgeBase := repositories.KnowledgeBaseRepository.GetInTenant(sqls.DB(), event.KnowledgeBaseID, event.TenantID)
		if knowledgeBase == nil || knowledgeBase.StoreID != event.StoreID {
			return fmt.Errorf("AI usage knowledge base store scope mismatch")
		}
	}
	return nil
}

func (s *aiUsageEventService) enrichStoreModelAttribution(event *models.AIUsageEvent) {
	if event == nil || event.TenantID <= 0 || event.StoreID <= 0 {
		return
	}
	assignment := repositories.StoreModelProfileAssignmentRepository.GetByStore(sqls.DB(), event.TenantID, event.StoreID)
	if assignment != nil && assignment.Status == enums.StoreModelAssignmentStatusReady && assignment.TemplateID > 0 && assignment.TemplateRevision > 0 {
		template := repositories.ModelProfileTemplateRepository.Get(sqls.DB(), assignment.TemplateID)
		if template != nil && template.Status == enums.ModelProfileStatusActive && template.Revision == assignment.TemplateRevision {
			if event.ModelProfileID <= 0 {
				event.ModelProfileID = template.ID
			}
			if event.ModelProfileRevision <= 0 {
				event.ModelProfileRevision = template.Revision
			}
			usage := enums.ModelUsageSlot(strings.TrimSpace(event.UsageSlot))
			if usage == "" {
				usage = inferUsageSlot(*event)
			}
			if usage != "" {
				if slot := repositories.ModelProfileSlotRepository.GetByUsage(sqls.DB(), template.ID, usage); slot != nil && slot.Enabled {
					event.UsageSlot = string(slot.UsageCode)
					if event.Model == "" {
						event.Model = strings.TrimSpace(slot.ModelName)
					}
					if event.Provider == "" {
						event.Provider = strings.TrimSpace(slot.Provider)
					}
				}
			}
		}
	}
	credential := repositories.StoreModelCredentialRepository.GetByBinding(sqls.DB(), event.TenantID, event.StoreID, event.StoreStaffBindingID)
	if credential != nil && credential.Status == enums.StoreCredentialStatusActive && credential.CredentialRevision > 0 {
		if event.CredentialRevision <= 0 {
			event.CredentialRevision = credential.CredentialRevision
		}
		if event.KeyFingerprint == "" {
			event.KeyFingerprint = strings.TrimSpace(credential.KeyFingerprint)
		}
	}
}

func inferUsageSlot(event models.AIUsageEvent) enums.ModelUsageSlot {
	switch strings.TrimSpace(event.Stage) {
	case "reply_generate":
		return enums.ModelUsageSlotReplyLLM
	case "intent_detect":
		return enums.ModelUsageSlotIntentDetectLLM
	case "memory_summary":
		return enums.ModelUsageSlotMemorySummary
	case "customer_tag", "customer_tag_evolution":
		return enums.ModelUsageSlotCustomerTag
	case "media_understanding":
		switch strings.TrimSpace(event.OperationType) {
		case "vision":
			return enums.ModelUsageSlotVision
		case "asr":
			return enums.ModelUsageSlotASR
		}
	case "document_parse":
		return enums.ModelUsageSlotDocumentParser
	}
	return ""
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
	if tenantID <= 0 {
		return 0, fmt.Errorf("AI usage event has no tenant evidence")
	}
	return tenantID, nil
}
