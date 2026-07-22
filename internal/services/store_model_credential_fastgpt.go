package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	storeCredentialFastGPTStatusNotRequired = "not_required"
	storeCredentialFastGPTStatusReady       = "ready"
	storeCredentialFastGPTStatusFailed      = "failed"
)

type storeCredentialActivationTarget struct {
	Store      models.Store
	Assignment models.StoreModelProfileAssignment
	Template   models.ModelProfileTemplate
	Slots      []models.ModelProfileSlot
}

type storeCredentialFastGPTSynchronizer interface {
	Sync(context.Context, storeCredentialActivationTarget, string, int64, string) (string, error)
}

type managedStoreCredentialFastGPTSynchronizer struct{}

type storeCredentialFastGPTSyncError struct {
	Class string
}

func (e *storeCredentialFastGPTSyncError) Error() string {
	return "FastGPT model profile synchronization failed (" + e.Class + ")"
}

func (s *managedStoreCredentialFastGPTSynchronizer) Sync(ctx context.Context, target storeCredentialActivationTarget, apiKey string, credentialRevision int64, fingerprint string) (string, error) {
	knowledgeBases := repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", target.Store.TenantID).
		Eq("store_id", target.Store.ID).
		Eq("connection_id", fastgptapi.ManagedConnectionID).
		Eq("status", enums.StatusOk).
		Asc("id"))
	if len(knowledgeBases) == 0 {
		return storeCredentialFastGPTStatusNotRequired, nil
	}
	binding := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(sqls.DB(), target.Store.ID, target.Store.TenantID)
	if binding == nil {
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "store_tenant_missing"}
	}
	now := time.Now()
	if err := repositories.FastGPTStoreTenantRepository.UpdatesInTenant(sqls.DB(), binding.ID, target.Store.TenantID, map[string]any{
		"target_profile_id": target.Template.ID, "target_profile_revision": target.Template.Revision,
		"target_credential_revision": credentialRevision, "readiness_status": "syncing",
		"last_error": "", "updated_at": now, "update_user_name": "store_model_credential",
	}); err != nil {
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "state_update_failed"}
	}

	connector, err := NewManagedStoreFastGPTConnector()
	if err != nil {
		s.markFailed(binding, target.Store.TenantID, "connector_unavailable")
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "connector_unavailable"}
	}
	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	current, err := connector.ForStore(target.Store.ID).GetModelProfile(callCtx, knowledgeBases[0].DatasetID)
	if err != nil {
		s.markFailed(binding, target.Store.TenantID, "profile_read_failed")
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "profile_read_failed"}
	}
	input, err := buildStoreCredentialFastGPTInput(target, knowledgeBases[0].DatasetID, current, apiKey)
	if err != nil {
		s.markFailed(binding, target.Store.TenantID, "profile_mapping_failed")
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "profile_mapping_failed"}
	}
	testResult, err := connector.ForStore(target.Store.ID).TestModelProfile(callCtx, input)
	if err != nil || testResult == nil || strings.TrimSpace(testResult.TestToken) == "" {
		s.markFailed(binding, target.Store.TenantID, "profile_test_failed")
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "profile_test_failed"}
	}
	for _, item := range testResult.Results {
		status := strings.ToLower(strings.TrimSpace(item.Status))
		if status != "success" && status != "passed" {
			s.markFailed(binding, target.Store.TenantID, "profile_test_failed")
			return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "profile_test_failed"}
		}
	}
	input.TestToken = testResult.TestToken
	upserted, err := connector.ForStore(target.Store.ID).UpsertModelProfile(callCtx, input)
	if err != nil || upserted == nil || strings.TrimSpace(upserted.Profile.ID) == "" {
		s.markFailed(binding, target.Store.TenantID, "profile_upsert_failed")
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "profile_upsert_failed"}
	}
	systemOperator := &dto.AuthPrincipal{UserID: constants.SystemAuditUserID, Username: constants.SystemAuditUserName}
	if err := FastGPTDatasetService.syncStoreModelProfileSnapshot(target.Store.ID, target.Store.TenantID, &upserted.Profile, systemOperator); err != nil {
		s.markFailed(binding, target.Store.TenantID, "snapshot_update_failed")
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "snapshot_update_failed"}
	}
	syncedAt := time.Now()
	if err := repositories.FastGPTStoreTenantRepository.UpdatesInTenant(sqls.DB(), binding.ID, target.Store.TenantID, map[string]any{
		"target_profile_id": target.Template.ID, "target_profile_revision": target.Template.Revision,
		"applied_profile_id": target.Template.ID, "applied_profile_revision": target.Template.Revision,
		"target_credential_revision": credentialRevision, "applied_credential_revision": credentialRevision,
		"applied_key_fingerprint": fingerprint, "readiness_status": "ready",
		"last_synced_at": syncedAt, "last_error": "", "updated_at": syncedAt,
		"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
	}); err != nil {
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "state_update_failed"}
	}
	return storeCredentialFastGPTStatusReady, nil
}

func (s *managedStoreCredentialFastGPTSynchronizer) markFailed(binding *models.FastGPTStoreTenant, tenantID int64, class string) {
	if binding == nil || tenantID <= 0 {
		return
	}
	now := time.Now()
	_ = repositories.FastGPTStoreTenantRepository.UpdatesInTenant(sqls.DB(), binding.ID, tenantID, map[string]any{
		"readiness_status": "failed", "last_error": class, "updated_at": now,
		"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
	})
}

func buildStoreCredentialFastGPTInput(target storeCredentialActivationTarget, datasetID string, current *FastGPTModelProfile, apiKey string) (FastGPTModelProfileInput, error) {
	slots := make(map[enums.ModelUsageSlot]models.ModelProfileSlot, len(target.Slots))
	for _, slot := range target.Slots {
		slots[slot.UsageCode] = slot
	}
	credential := func(usage enums.ModelUsageSlot) (fastgptapi.ModelCredential, error) {
		slot, ok := slots[usage]
		if !ok || !slot.Enabled || strings.TrimSpace(slot.ModelName) == "" {
			return fastgptapi.ModelCredential{}, fmt.Errorf("required FastGPT slot %s is missing", usage)
		}
		return fastgptapi.ModelCredential{
			Provider: slot.Provider, BaseURL: target.Template.GatewayBaseURL, Model: slot.ModelName, APIKey: apiKey,
		}, nil
	}
	embedding, err := credential(enums.ModelUsageSlotEmbedding)
	if err != nil {
		return FastGPTModelProfileInput{}, err
	}
	documentParser, err := credential(enums.ModelUsageSlotDocumentParser)
	if err != nil {
		return FastGPTModelProfileInput{}, err
	}
	vision, err := credential(enums.ModelUsageSlotVision)
	if err != nil {
		return FastGPTModelProfileInput{}, err
	}
	rerank, err := credential(enums.ModelUsageSlotRerank)
	if err != nil {
		return FastGPTModelProfileInput{}, err
	}
	input := FastGPTModelProfileInput{
		DatasetID: datasetID, Name: fmt.Sprintf("%s r%d", target.Template.Name, target.Template.Revision),
		Embedding: embedding, DocumentParser: documentParser, Vision: vision, Rerank: &rerank,
	}
	if current != nil {
		input.ProfileID = current.ID
	}
	return input, nil
}
