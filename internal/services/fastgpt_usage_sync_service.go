package services

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

// FastGPTUsageSyncService imports immutable, non-sensitive FastGPT usage
// evidence. It is intentionally asynchronous: a failed export never delays a
// customer reply or changes the active knowledge-base binding.
var FastGPTUsageSyncService = newFastGPTUsageSyncService()

type fastGPTUsageSyncService struct{}

type fastGPTUsageAttribution struct {
	ModelProfileID     int64
	ProfileRevision    int64
	CredentialRevision int64
	KeyFingerprint     string
	FastGPTProfileID   string
	FastGPTRevision    string
}

func newFastGPTUsageSyncService() *fastGPTUsageSyncService { return &fastGPTUsageSyncService{} }

func (s *fastGPTUsageSyncService) ProcessDue(limit int) int {
	if limit <= 0 {
		limit = 50
	}
	// A deployment without the dedicated integration token cannot poll the
	// managed FastGPT usage export.
	if _, err := NewManagedStoreFastGPTConnector(); err != nil {
		return 0
	}
	knowledgeBases := repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("connection_id", fastgptapi.ManagedConnectionID).
		Gt("tenant_id", 0).
		Eq("status", enums.StatusOk).
		Asc("id").Limit(limit))
	processed := 0
	for i := range knowledgeBases {
		store := repositories.StoreRepository.GetInTenant(sqls.DB(), knowledgeBases[i].StoreID, knowledgeBases[i].TenantID)
		if store == nil || store.Status != enums.StatusOk || store.KnowledgeBaseID != knowledgeBases[i].ID {
			continue
		}
		if err := s.SyncKnowledgeBase(knowledgeBases[i].ID); err != nil {
			// Error details from FastGPT may contain upstream/provider text. Keep
			// observability scoped to IDs and persist only a safe class below.
			slog.Warn("FastGPT usage sync failed", "knowledgeBaseID", knowledgeBases[i].ID)
			continue
		}
		processed++
	}
	return processed
}

func (s *fastGPTUsageSyncService) SyncKnowledgeBase(knowledgeBaseID int64) error {
	knowledgeBase := KnowledgeBaseService.Get(knowledgeBaseID)
	if knowledgeBase == nil || knowledgeBase.TenantID <= 0 || knowledgeBase.ConnectionID != fastgptapi.ManagedConnectionID || knowledgeBase.StoreID <= 0 || strings.TrimSpace(knowledgeBase.DatasetID) == "" {
		return fmt.Errorf("managed FastGPT knowledge base is unavailable")
	}
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), knowledgeBase.StoreID, knowledgeBase.TenantID)
	if store == nil || store.Status != enums.StatusOk || store.KnowledgeBaseID != knowledgeBase.ID {
		s.saveFailure(knowledgeBase, "knowledge_base_not_authoritative")
		return fmt.Errorf("managed FastGPT knowledge base is not the Store authority")
	}
	target, credential, err := FastGPTDatasetService.resolveJobTarget(knowledgeBase.TenantID, knowledgeBase.StoreID)
	if err != nil {
		s.saveFailure(knowledgeBase, "model_target_unavailable")
		return err
	}
	if err := FastGPTDatasetService.requireAppliedTarget(knowledgeBase, target, credential); err != nil {
		s.saveFailure(knowledgeBase, "profile_not_ready")
		return err
	}
	tenant := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(sqls.DB(), knowledgeBase.StoreID, knowledgeBase.TenantID)
	if tenant == nil || tenant.TenantTeamID == "" || tenant.Status != "active" || tenant.ReadinessStatus != "ready" {
		s.saveFailure(knowledgeBase, "tenant_unavailable")
		return fmt.Errorf("managed FastGPT tenant is unavailable")
	}
	currentAttribution, err := fastGPTAttributionFromAppliedTarget(knowledgeBase, tenant)
	if err != nil {
		s.saveFailure(knowledgeBase, "usage_attribution_unavailable")
		return err
	}
	state, err := s.loadOrInitializeState(knowledgeBase, tenant, currentAttribution)
	if err != nil {
		s.saveFailure(knowledgeBase, "usage_cursor_scope_mismatch")
		return err
	}
	windowAttribution, err := fastGPTAttributionFromSyncState(state)
	if err != nil {
		s.saveFailure(knowledgeBase, "usage_cursor_attribution_missing")
		return err
	}
	connector, err := NewManagedStoreFastGPTConnector()
	if err != nil {
		s.saveFailure(knowledgeBase, "integration_unavailable")
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	page, err := connector.ForStore(knowledgeBase.StoreID).ListUsageEvents(ctx, knowledgeBase.DatasetID, state.Cursor, 100)
	if err != nil {
		s.saveFailure(knowledgeBase, "usage_export_failed")
		return err
	}
	nextCursor, err := nextFastGPTUsageCursor(state.Cursor, page)
	if err != nil {
		s.saveFailure(knowledgeBase, "usage_cursor_not_advanced")
		return err
	}
	for _, item := range page.Events {
		attribution, err := selectFastGPTUsageAttribution(item, windowAttribution, currentAttribution)
		if err != nil {
			s.saveFailure(knowledgeBase, "usage_event_revision_unknown")
			return err
		}
		event := toFastGPTUsageEvent(knowledgeBase, tenant, attribution, item)
		if item.Kind == "model" && event.UsageSlot == "" {
			s.saveFailure(knowledgeBase, "usage_slot_unresolved")
			return fmt.Errorf("managed FastGPT model usage slot is unresolved")
		}
		if err := AIUsageEventService.Record(event); err != nil {
			s.saveFailure(knowledgeBase, "usage_record_failed")
			return err
		}
	}
	now := time.Now()
	expected := *state
	next := expected
	next.StoreID = knowledgeBase.StoreID
	next.TenantTeamID = tenant.TenantTeamID
	next.Cursor = nextCursor
	applyFastGPTUsageAttribution(&next, currentAttribution)
	next.LastSyncedAt = &now
	next.LastError = ""
	next.UpdatedAt = now
	advanced, err := repositories.FastGPTUsageSyncStateRepository.CompareAndSwap(sqls.DB(), &expected, &next)
	if err != nil {
		return err
	}
	if advanced {
		return nil
	}
	// Another worker advanced this opaque cursor window first. Usage events are
	// immutable and idempotent, so losing the CAS is a successful no-op.
	current := repositories.FastGPTUsageSyncStateRepository.GetByKnowledgeBaseIDInTenant(sqls.DB(), knowledgeBase.ID, knowledgeBase.TenantID)
	if current == nil || current.StoreID != knowledgeBase.StoreID || strings.TrimSpace(current.TenantTeamID) != tenant.TenantTeamID {
		return fmt.Errorf("managed FastGPT usage cursor changed outside its Store scope")
	}
	return nil
}

func (s *fastGPTUsageSyncService) loadOrInitializeState(knowledgeBase *models.KnowledgeBase, tenant *models.FastGPTStoreTenant, attribution fastGPTUsageAttribution) (*models.FastGPTUsageSyncState, error) {
	if knowledgeBase == nil || tenant == nil {
		return nil, fmt.Errorf("managed FastGPT usage cursor scope is missing")
	}
	for attempt := 0; attempt < 3; attempt++ {
		state := repositories.FastGPTUsageSyncStateRepository.GetByKnowledgeBaseIDInTenant(sqls.DB(), knowledgeBase.ID, knowledgeBase.TenantID)
		if state == nil {
			now := time.Now()
			initial := &models.FastGPTUsageSyncState{
				TenantID: knowledgeBase.TenantID, StoreID: knowledgeBase.StoreID,
				KnowledgeBaseID: knowledgeBase.ID, TenantTeamID: tenant.TenantTeamID,
				CreatedAt: now, UpdatedAt: now,
			}
			applyFastGPTUsageAttribution(initial, attribution)
			created, err := repositories.FastGPTUsageSyncStateRepository.CreateIfAbsent(sqls.DB(), initial)
			if err != nil {
				return nil, err
			}
			if created {
				return initial, nil
			}
			continue
		}
		if state.StoreID != knowledgeBase.StoreID || (strings.TrimSpace(state.TenantTeamID) != "" && strings.TrimSpace(state.TenantTeamID) != tenant.TenantTeamID) {
			return nil, fmt.Errorf("managed FastGPT usage cursor scope mismatch")
		}
		if _, err := fastGPTAttributionFromSyncState(state); err == nil {
			return state, nil
		}
		if strings.TrimSpace(state.Cursor) != "" || !fastGPTUsageAttributionIsEmpty(state) {
			return nil, fmt.Errorf("managed FastGPT usage cursor attribution is incomplete")
		}
		expected := *state
		next := expected
		next.StoreID = knowledgeBase.StoreID
		next.TenantTeamID = tenant.TenantTeamID
		applyFastGPTUsageAttribution(&next, attribution)
		next.LastError = ""
		next.UpdatedAt = time.Now()
		initialized, err := repositories.FastGPTUsageSyncStateRepository.CompareAndSwap(sqls.DB(), &expected, &next)
		if err != nil {
			return nil, err
		}
		if initialized {
			return &next, nil
		}
	}
	return nil, fmt.Errorf("managed FastGPT usage cursor initialization conflicted")
}

func (s *fastGPTUsageSyncService) saveFailure(knowledgeBase *models.KnowledgeBase, errorClass string) {
	if knowledgeBase == nil || knowledgeBase.ID <= 0 {
		return
	}
	now := time.Now()
	state := repositories.FastGPTUsageSyncStateRepository.GetByKnowledgeBaseIDInTenant(sqls.DB(), knowledgeBase.ID, knowledgeBase.TenantID)
	if state == nil {
		state = &models.FastGPTUsageSyncState{
			TenantID:        knowledgeBase.TenantID,
			StoreID:         knowledgeBase.StoreID,
			KnowledgeBaseID: knowledgeBase.ID,
			CreatedAt:       now,
		}
	}
	_, _ = repositories.FastGPTUsageSyncStateRepository.SaveFailure(sqls.DB(), state, errorClass, now)
}

func toFastGPTUsageEvent(knowledgeBase *models.KnowledgeBase, tenant *models.FastGPTStoreTenant, attribution fastGPTUsageAttribution, item fastgptapi.UsageEvent) models.AIUsageEvent {
	metricSource := AIUsageMetricSourceProviderOperation
	provider := "fastgpt"
	modelSource := "fastgpt_operation"
	if item.Kind == "model" {
		metricSource = AIUsageMetricSourceUpstreamActual
		provider = firstNonBlank(item.Provider, "fastgpt")
		modelSource = "fastgpt_profile"
	}
	status := "completed"
	if item.Status == "error" || item.Status == "failed" || item.Status == "blocked" {
		status = "failed"
	}
	createdAt := item.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return models.AIUsageEvent{
		TenantID:             knowledgeBase.TenantID,
		EventKey:             fmt.Sprintf("fastgpt:%s:%s", tenant.TenantTeamID, item.ExternalEventID),
		StoreID:              knowledgeBase.StoreID,
		KnowledgeBaseID:      knowledgeBase.ID,
		RequestID:            item.RequestID,
		Stage:                firstNonBlank(item.Stage, "knowledge_operation"),
		Provider:             provider,
		Model:                item.Model,
		ModelProfileID:       attribution.ModelProfileID,
		ModelProfileRevision: attribution.ProfileRevision,
		UsageSlot:            string(fastGPTUsageSlot(item)),
		CredentialRevision:   attribution.CredentialRevision,
		KeyFingerprint:       attribution.KeyFingerprint,
		ModelSource:          modelSource,
		UpstreamRequestID:    item.ExternalEventID,
		PromptTokens:         item.PromptTokens,
		CompletionTokens:     item.CompletionTokens,
		CachedPromptTokens:   item.CachedTokens,
		ReasoningTokens:      item.ReasoningTokens,
		OperationType:        item.OperationType,
		RequestCount:         item.RequestCount,
		RerankCount:          item.RerankCount,
		TrainingCount:        item.TrainingCount,
		FileBytes:            item.FileBytes,
		MetricSource:         metricSource,
		LatencyMS:            item.LatencyMS,
		Status:               status,
		ErrorClass:           normalizeFastGPTUsageErrorClass(item.ErrorClass),
		CreatedAt:            createdAt,
	}
}

func fastGPTAttributionFromAppliedTarget(knowledgeBase *models.KnowledgeBase, tenant *models.FastGPTStoreTenant) (fastGPTUsageAttribution, error) {
	if knowledgeBase == nil || tenant == nil || tenant.AppliedProfileID <= 0 || tenant.AppliedProfileRevision <= 0 ||
		tenant.AppliedCredentialRevision <= 0 || strings.TrimSpace(tenant.AppliedKeyFingerprint) == "" ||
		strings.TrimSpace(knowledgeBase.FastGPTProfileID) == "" || strings.TrimSpace(knowledgeBase.FastGPTProfileRevision) == "" ||
		knowledgeBase.FastGPTAppliedProfileID != tenant.AppliedProfileID || knowledgeBase.FastGPTAppliedProfileRevision != tenant.AppliedProfileRevision ||
		knowledgeBase.FastGPTAppliedCredentialRevision != tenant.AppliedCredentialRevision {
		return fastGPTUsageAttribution{}, fmt.Errorf("managed FastGPT applied attribution is incomplete")
	}
	return fastGPTUsageAttribution{
		ModelProfileID: tenant.AppliedProfileID, ProfileRevision: tenant.AppliedProfileRevision,
		CredentialRevision: tenant.AppliedCredentialRevision, KeyFingerprint: tenant.AppliedKeyFingerprint,
		FastGPTProfileID: knowledgeBase.FastGPTProfileID, FastGPTRevision: knowledgeBase.FastGPTProfileRevision,
	}, nil
}

func fastGPTAttributionFromSyncState(state *models.FastGPTUsageSyncState) (fastGPTUsageAttribution, error) {
	if state == nil || state.ModelProfileID <= 0 || state.ProfileRevision <= 0 || state.CredentialRevision <= 0 ||
		strings.TrimSpace(state.KeyFingerprint) == "" || strings.TrimSpace(state.FastGPTProfileID) == "" || strings.TrimSpace(state.FastGPTRevision) == "" {
		return fastGPTUsageAttribution{}, fmt.Errorf("managed FastGPT usage cursor attribution is incomplete")
	}
	return fastGPTUsageAttribution{
		ModelProfileID: state.ModelProfileID, ProfileRevision: state.ProfileRevision,
		CredentialRevision: state.CredentialRevision, KeyFingerprint: state.KeyFingerprint,
		FastGPTProfileID: state.FastGPTProfileID, FastGPTRevision: state.FastGPTRevision,
	}, nil
}

func applyFastGPTUsageAttribution(state *models.FastGPTUsageSyncState, attribution fastGPTUsageAttribution) {
	if state == nil {
		return
	}
	state.ModelProfileID = attribution.ModelProfileID
	state.ProfileRevision = attribution.ProfileRevision
	state.CredentialRevision = attribution.CredentialRevision
	state.KeyFingerprint = attribution.KeyFingerprint
	state.FastGPTProfileID = attribution.FastGPTProfileID
	state.FastGPTRevision = attribution.FastGPTRevision
}

func fastGPTUsageAttributionIsEmpty(state *models.FastGPTUsageSyncState) bool {
	return state != nil && state.ModelProfileID == 0 && state.ProfileRevision == 0 && state.CredentialRevision == 0 &&
		strings.TrimSpace(state.KeyFingerprint) == "" && strings.TrimSpace(state.FastGPTProfileID) == "" && strings.TrimSpace(state.FastGPTRevision) == ""
}

func selectFastGPTUsageAttribution(item fastgptapi.UsageEvent, window, current fastGPTUsageAttribution) (fastGPTUsageAttribution, error) {
	if strings.TrimSpace(item.Kind) != "model" {
		return window, nil
	}
	profileID := strings.TrimSpace(item.ProfileID)
	if profileID == "" || item.ProfileRevision <= 0 {
		return fastGPTUsageAttribution{}, fmt.Errorf("managed FastGPT model usage is missing Profile revision")
	}
	revision := strconv.FormatInt(item.ProfileRevision, 10)
	if profileID == window.FastGPTProfileID && revision == window.FastGPTRevision {
		return window, nil
	}
	if profileID == current.FastGPTProfileID && revision == current.FastGPTRevision {
		return current, nil
	}
	return fastGPTUsageAttribution{}, fmt.Errorf("managed FastGPT model usage references an unknown Profile revision")
}

func nextFastGPTUsageCursor(current string, page *fastgptapi.UsageEventPage) (string, error) {
	current = strings.TrimSpace(current)
	if page == nil {
		return "", fmt.Errorf("managed FastGPT usage page is missing")
	}
	next := strings.TrimSpace(page.NextCursor)
	if len(page.Events) > 0 {
		if next == "" || next == current {
			return "", fmt.Errorf("managed FastGPT usage cursor did not advance")
		}
		return next, nil
	}
	if next == "" {
		return current, nil
	}
	return next, nil
}

func fastGPTUsageSlot(item fastgptapi.UsageEvent) enums.ModelUsageSlot {
	value := strings.ToLower(strings.TrimSpace(item.Stage + " " + item.OperationType))
	switch {
	case strings.Contains(value, "document") || strings.Contains(value, "parse"):
		return enums.ModelUsageSlotDocumentParser
	case strings.Contains(value, "rerank") || strings.Contains(value, "re_rank"):
		return enums.ModelUsageSlotRerank
	case strings.Contains(value, "embed") || strings.Contains(value, "vector"):
		return enums.ModelUsageSlotEmbedding
	case strings.Contains(value, "vision") || strings.Contains(value, "image") || strings.Contains(value, "ocr"):
		return enums.ModelUsageSlotVision
	default:
		return ""
	}
}

func normalizeFastGPTUsageErrorClass(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if len(value) > 80 {
		return "fastgpt_provider_error"
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return "fastgpt_provider_error"
	}
	return value
}
