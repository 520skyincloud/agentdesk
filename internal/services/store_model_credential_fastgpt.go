package services

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
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
	if target.Store.KnowledgeBaseID <= 0 {
		return storeCredentialFastGPTStatusNotRequired, nil
	}
	knowledgeBase := repositories.KnowledgeBaseRepository.GetInTenant(sqls.DB(), target.Store.KnowledgeBaseID, target.Store.TenantID)
	if knowledgeBase == nil || knowledgeBase.StoreID != target.Store.ID || knowledgeBase.Status != enums.StatusOk ||
		strings.TrimSpace(knowledgeBase.ConnectionID) != fastgptapi.ManagedConnectionID || strings.TrimSpace(knowledgeBase.DatasetID) == "" {
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "knowledge_base_invalid"}
	}
	binding := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(sqls.DB(), target.Store.ID, target.Store.TenantID)
	if binding == nil {
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "store_tenant_missing"}
	}
	now := time.Now()
	readinessStatus := "syncing"
	if binding.AppliedProfileID > 0 && binding.AppliedProfileRevision > 0 && binding.AppliedCredentialRevision > 0 {
		readinessStatus = "ready"
	}
	if err := repositories.FastGPTStoreTenantRepository.UpdatesInTenant(sqls.DB(), binding.ID, target.Store.TenantID, map[string]any{
		"target_profile_id": target.Template.ID, "target_profile_revision": target.Template.Revision,
		"target_credential_revision": credentialRevision, "readiness_status": readinessStatus,
		"last_error": "", "updated_at": now, "update_user_name": "store_model_credential",
	}); err != nil {
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "state_update_failed"}
	}
	publishFastGPTConfigurationState(target.Store.TenantID, target.Store.ID, now)

	connector, err := NewManagedStoreFastGPTConnector()
	if err != nil {
		s.markFailed(binding, target.Store.TenantID, "connector_unavailable")
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "connector_unavailable"}
	}
	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	profile, syncClass, err := syncManagedStoreFastGPTProfile(callCtx, connector, target, apiKey, knowledgeBase.DatasetID)
	if err != nil {
		s.markFailed(binding, target.Store.TenantID, syncClass)
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: syncClass}
	}
	systemOperator := &dto.AuthPrincipal{UserID: constants.SystemAuditUserID, Username: constants.SystemAuditUserName}
	if err := commitManagedStoreFastGPTProfile(target, credentialRevision, fingerprint, profile, knowledgeBase.ID, systemOperator); err != nil {
		s.markFailed(binding, target.Store.TenantID, "snapshot_update_failed")
		return storeCredentialFastGPTStatusFailed, &storeCredentialFastGPTSyncError{Class: "state_update_failed"}
	}
	return storeCredentialFastGPTStatusReady, nil
}

func (s *managedStoreCredentialFastGPTSynchronizer) markFailed(binding *models.FastGPTStoreTenant, tenantID int64, class string) {
	if binding == nil || tenantID <= 0 {
		return
	}
	current := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(sqls.DB(), binding.StoreID, tenantID)
	readinessStatus := "failed"
	if current != nil && current.AppliedProfileID > 0 && current.AppliedProfileRevision > 0 && current.AppliedCredentialRevision > 0 {
		readinessStatus = "ready"
	}
	now := time.Now()
	_ = repositories.FastGPTStoreTenantRepository.UpdatesInTenant(sqls.DB(), binding.ID, tenantID, map[string]any{
		"readiness_status": readinessStatus, "last_error": class, "updated_at": now,
		"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
	})
	publishFastGPTConfigurationState(tenantID, binding.StoreID, now)
}

func syncManagedStoreFastGPTProfile(ctx context.Context, connector *FastGPTConnector, target storeCredentialActivationTarget, apiKey, datasetID string) (*FastGPTModelProfile, string, error) {
	if connector == nil || strings.TrimSpace(datasetID) == "" {
		return nil, "dataset_unavailable", errors.New("managed FastGPT dataset is unavailable")
	}
	scoped := connector.ForStore(target.Store.ID)
	current, err := scoped.GetModelProfile(ctx, datasetID)
	if err != nil {
		return nil, "profile_read_failed", err
	}
	input, err := buildStoreCredentialFastGPTInput(target, datasetID, current, apiKey)
	if err != nil {
		return nil, "profile_mapping_failed", err
	}
	testResult, err := scoped.TestModelProfile(ctx, input)
	if err != nil || testResult == nil || strings.TrimSpace(testResult.TestToken) == "" {
		return nil, "profile_test_failed", errors.New("managed FastGPT profile test failed")
	}
	for _, item := range testResult.Results {
		status := strings.ToLower(strings.TrimSpace(item.Status))
		if status != "success" && status != "passed" {
			return nil, "profile_test_failed", errors.New("managed FastGPT profile test failed")
		}
	}
	input.TestToken = testResult.TestToken
	upserted, err := scoped.UpsertModelProfile(ctx, input)
	if err != nil || upserted == nil || strings.TrimSpace(upserted.Profile.ID) == "" {
		return nil, "profile_upsert_failed", errors.New("managed FastGPT profile publish failed")
	}
	return &upserted.Profile, "", nil
}

func commitManagedStoreFastGPTProfile(target storeCredentialActivationTarget, credentialRevision int64, fingerprint string, profile *FastGPTModelProfile, knowledgeBaseID int64, operator *dto.AuthPrincipal) error {
	if target.Store.TenantID <= 0 || target.Store.ID <= 0 || target.Template.ID <= 0 || target.Template.Revision <= 0 ||
		credentialRevision <= 0 || profile == nil || strings.TrimSpace(profile.ID) == "" || knowledgeBaseID <= 0 {
		return errors.New("managed FastGPT profile commit target is invalid")
	}
	operator = operatorOrSystem(operator)
	if err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
		return commitManagedStoreFastGPTProfileDB(tx.Tx, target, credentialRevision, fingerprint, profile, knowledgeBaseID, operator, time.Now())
	}); err != nil {
		return err
	}
	publishFastGPTConfigurationState(target.Store.TenantID, target.Store.ID, time.Now())
	return nil
}

func publishFastGPTConfigurationState(tenantID, storeID int64, updatedAt time.Time) {
	if tenantID <= 0 || storeID <= 0 {
		return
	}
	binding := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(sqls.DB(), storeID, tenantID)
	if binding == nil {
		WsService.PublishFastGPTProfileChanged(tenantID, storeID, 0, 0, "unconfigured", updatedAt)
		return
	}
	profileID := binding.AppliedProfileID
	revision := binding.AppliedProfileRevision
	if binding.TargetProfileID > 0 {
		profileID = binding.TargetProfileID
		revision = binding.TargetProfileRevision
	}
	WsService.PublishFastGPTProfileChanged(
		tenantID,
		storeID,
		profileID,
		revision,
		firstNonBlank(binding.ReadinessStatus, binding.Status),
		updatedAt,
	)
}

func commitManagedStoreFastGPTProfileDB(db *gorm.DB, target storeCredentialActivationTarget, credentialRevision int64, fingerprint string, profile *FastGPTModelProfile, knowledgeBaseID int64, operator *dto.AuthPrincipal, now time.Time) error {
	if db == nil || knowledgeBaseID <= 0 {
		return errors.New("managed FastGPT profile commit database scope is invalid")
	}
	knowledgeBase := repositories.KnowledgeBaseRepository.GetInTenant(db, knowledgeBaseID, target.Store.TenantID)
	if knowledgeBase == nil || knowledgeBase.StoreID != target.Store.ID || knowledgeBase.Status != enums.StatusOk ||
		strings.TrimSpace(knowledgeBase.ConnectionID) != fastgptapi.ManagedConnectionID {
		return errors.New("managed FastGPT profile knowledge base is invalid")
	}
	updated, err := repositories.FastGPTStoreTenantRepository.ApplyTargetRevisions(db, target.Store.TenantID, target.Store.ID, target.Template.ID, target.Template.Revision, credentialRevision, map[string]any{
		"applied_profile_id": target.Template.ID, "applied_profile_revision": target.Template.Revision,
		"applied_credential_revision": credentialRevision, "applied_key_fingerprint": fingerprint,
		"readiness_status": "ready", "last_synced_at": now, "last_error": "", "updated_at": now,
		"update_user_id": operator.UserID, "update_user_name": operator.Username,
	})
	if err != nil {
		return err
	}
	if !updated {
		return errors.New("managed FastGPT target changed during synchronization")
	}
	return commitManagedKnowledgeBaseFastGPTProfileDB(db, target, credentialRevision, profile, knowledgeBase.ID, operator, now)
}

// commitManagedKnowledgeBaseFastGPTProfileDB records a candidate Dataset's
// tested Profile without changing the Store-level applied revision. This keeps
// the currently active Dataset usable until an explicit atomic cutover.
func commitManagedKnowledgeBaseFastGPTProfileDB(db *gorm.DB, target storeCredentialActivationTarget, credentialRevision int64, profile *FastGPTModelProfile, knowledgeBaseID int64, operator *dto.AuthPrincipal, now time.Time) error {
	if db == nil || knowledgeBaseID <= 0 || credentialRevision <= 0 || profile == nil || strings.TrimSpace(profile.ID) == "" {
		return errors.New("managed FastGPT knowledge-base profile commit target is invalid")
	}
	knowledgeBase := repositories.KnowledgeBaseRepository.GetInTenant(db, knowledgeBaseID, target.Store.TenantID)
	if knowledgeBase == nil || knowledgeBase.StoreID != target.Store.ID || knowledgeBase.Status != enums.StatusOk ||
		strings.TrimSpace(knowledgeBase.ConnectionID) != fastgptapi.ManagedConnectionID {
		return errors.New("managed FastGPT profile knowledge base is invalid")
	}
	return repositories.KnowledgeBaseRepository.UpdatesInTenant(db, knowledgeBase.ID, target.Store.TenantID,
		fastGPTProfileSnapshotColumns(profile, target, credentialRevision, operator, now))
}

func fastGPTProfileSnapshotColumns(profile *FastGPTModelProfile, target storeCredentialActivationTarget, credentialRevision int64, operator *dto.AuthPrincipal, now time.Time) map[string]any {
	operator = operatorOrSystem(operator)
	fingerprintSource := strings.Join([]string{
		profile.Embedding.KeyFingerprint,
		profile.DocumentParser.KeyFingerprint,
		profile.Vision.KeyFingerprint,
		func() string {
			if profile.Rerank != nil {
				return profile.Rerank.KeyFingerprint
			}
			return ""
		}(),
	}, ":")
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprintSource)))
	return map[string]any{
		"fast_gpt_profile_id": profile.ID, "fast_gpt_profile_name": profile.Name,
		"fast_gpt_profile_revision":    strconv.FormatInt(profile.Revision, 10),
		"fast_gpt_profile_fingerprint": fingerprint, "fast_gpt_profile_status": "ready",
		"fast_gpt_profile_synced_at":           now,
		"fast_gpt_applied_profile_id":          target.Template.ID,
		"fast_gpt_applied_profile_revision":    target.Template.Revision,
		"fast_gpt_applied_credential_revision": credentialRevision,
		"updated_at":                           now,
		"update_user_id":                       operator.UserID, "update_user_name": operator.Username,
	}
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
