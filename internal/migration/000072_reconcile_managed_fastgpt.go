package migration

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const reconcileManagedFastGPTMigrationRemark = "stage Store-authoritative managed FastGPT reprovisioning"

var fastGPTNonterminalJobStatuses = []string{"pending", "uploading", "parsing", "indexing"}

type migrationFastGPTTarget struct {
	ProfileID          int64
	ProfileRevision    int64
	CredentialRevision int64
}

func init() {
	register(72, reconcileManagedFastGPTMigrationRemark, func() error {
		return reconcileManagedFastGPT(sqls.DB())
	})
}

func reconcileManagedFastGPT(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("managed FastGPT migration database is nil")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		stores, err := reconcileStoreKnowledgeAuthority(tx)
		if err != nil {
			return err
		}
		if err := reconcileFastGPTDatasetJobTargets(tx); err != nil {
			return err
		}
		for i := range stores {
			if err := reconcileManagedFastGPTStore(tx, &stores[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

func reconcileStoreKnowledgeAuthority(tx *gorm.DB) ([]models.Store, error) {
	stores := make([]models.Store, 0)
	if err := tx.Where("tenant_id > 0 AND status <> ?", enums.StatusDeleted).Order("tenant_id ASC, id ASC").Find(&stores).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	for i := range stores {
		knowledgeBaseID, err := resolveMigrationStoreKnowledgeBase(tx, &stores[i])
		if err != nil {
			return nil, err
		}
		if stores[i].KnowledgeBaseID != knowledgeBaseID {
			if err := tx.Model(&models.Store{}).Where("id = ? AND tenant_id = ?", stores[i].ID, stores[i].TenantID).
				Updates(map[string]any{"knowledge_base_id": knowledgeBaseID, "updated_at": now, "update_user_name": "migration_072"}).Error; err != nil {
				return nil, err
			}
			stores[i].KnowledgeBaseID = knowledgeBaseID
		}
		if err := tx.Model(&models.WxWorkProtocolInstance{}).
			Where("tenant_id = ? AND store_id = ?", stores[i].TenantID, stores[i].ID).
			Updates(map[string]any{"knowledge_base_id": knowledgeBaseID, "updated_at": now, "update_user_name": "migration_072"}).Error; err != nil {
			return nil, err
		}
		if err := tx.Model(&models.ConversationRouteState{}).
			Where("tenant_id = ? AND store_id = ?", stores[i].TenantID, stores[i].ID).
			Updates(map[string]any{"knowledge_base_id": knowledgeBaseID, "updated_at": now, "update_user_name": "migration_072"}).Error; err != nil {
			return nil, err
		}
	}
	return stores, nil
}

func resolveMigrationStoreKnowledgeBase(tx *gorm.DB, store *models.Store) (int64, error) {
	if store == nil || store.ID <= 0 || store.TenantID <= 0 {
		return 0, fmt.Errorf("managed FastGPT migration encountered an invalid Store")
	}
	if store.KnowledgeBaseID > 0 {
		if knowledgeBase := validMigrationStoreKnowledgeBase(tx, store, store.KnowledgeBaseID); knowledgeBase != nil {
			return knowledgeBase.ID, nil
		}
	}
	candidates := make(map[int64]struct{})
	var instances []models.WxWorkProtocolInstance
	if err := tx.Where("tenant_id = ? AND store_id = ? AND status <> ? AND knowledge_base_id > 0", store.TenantID, store.ID, enums.StatusDeleted).
		Find(&instances).Error; err != nil {
		return 0, err
	}
	for i := range instances {
		if knowledgeBase := validMigrationStoreKnowledgeBase(tx, store, instances[i].KnowledgeBaseID); knowledgeBase != nil {
			candidates[knowledgeBase.ID] = struct{}{}
		}
	}
	var routes []models.ConversationRouteState
	if err := tx.Where("tenant_id = ? AND store_id = ? AND knowledge_base_id > 0", store.TenantID, store.ID).Find(&routes).Error; err != nil {
		return 0, err
	}
	for i := range routes {
		if knowledgeBase := validMigrationStoreKnowledgeBase(tx, store, routes[i].KnowledgeBaseID); knowledgeBase != nil {
			candidates[knowledgeBase.ID] = struct{}{}
		}
	}
	if len(candidates) > 1 {
		return 0, fmt.Errorf("Store %d has conflicting knowledge-base projections; choose one authoritative knowledge base before startup", store.ID)
	}
	for knowledgeBaseID := range candidates {
		return knowledgeBaseID, nil
	}
	return 0, nil
}

func validMigrationStoreKnowledgeBase(tx *gorm.DB, store *models.Store, knowledgeBaseID int64) *models.KnowledgeBase {
	if knowledgeBaseID <= 0 {
		return nil
	}
	item := &models.KnowledgeBase{}
	if err := tx.Take(item, "id = ? AND tenant_id = ? AND store_id = ? AND status = ?", knowledgeBaseID, store.TenantID, store.ID, enums.StatusOk).Error; err != nil {
		return nil
	}
	return item
}

func reconcileFastGPTDatasetJobTargets(tx *gorm.DB) error {
	jobs := make([]models.FastGPTDatasetJob, 0)
	if err := tx.Where("status IN ?", fastGPTNonterminalJobStatuses).Order("id ASC").Find(&jobs).Error; err != nil {
		return err
	}
	now := time.Now()
	for i := range jobs {
		job := jobs[i]
		target, ready := loadMigrationFastGPTTarget(tx, job.TenantID, job.StoreID)
		columns := map[string]any{"lease_owner": "", "lease_expires_at": nil, "updated_at": now}
		switch {
		case !ready:
			columns["status"] = "failed"
			columns["completed_at"] = now
			columns["last_error"] = "migration_target_unavailable"
			columns["last_error_class"] = "migration_target_unavailable"
		case job.TargetProfileID == 0 && job.TargetProfileRevision == 0 && job.TargetCredentialRevision == 0:
			columns["target_profile_id"] = target.ProfileID
			columns["target_profile_revision"] = target.ProfileRevision
			columns["target_credential_revision"] = target.CredentialRevision
		case job.TargetProfileID != target.ProfileID || job.TargetProfileRevision != target.ProfileRevision || job.TargetCredentialRevision != target.CredentialRevision:
			columns["status"] = "failed"
			columns["completed_at"] = now
			columns["last_error"] = "target_revision_changed"
			columns["last_error_class"] = "target_revision_changed"
		}
		if err := tx.Model(&models.FastGPTDatasetJob{}).Where("id = ? AND tenant_id = ?", job.ID, job.TenantID).Updates(columns).Error; err != nil {
			return err
		}
	}
	return nil
}

func loadMigrationFastGPTTarget(tx *gorm.DB, tenantID, storeID int64) (migrationFastGPTTarget, bool) {
	if tenantID <= 0 || storeID <= 0 {
		return migrationFastGPTTarget{}, false
	}
	assignment := &models.StoreModelProfileAssignment{}
	if err := tx.Take(assignment, "tenant_id = ? AND store_id = ?", tenantID, storeID).Error; err != nil ||
		assignment.Status != enums.StoreModelAssignmentStatusReady || assignment.TemplateID <= 0 || assignment.TemplateRevision <= 0 {
		return migrationFastGPTTarget{}, false
	}
	template := &models.ModelProfileTemplate{}
	if err := tx.Take(template, "id = ? AND revision = ? AND status = ?", assignment.TemplateID, assignment.TemplateRevision, enums.ModelProfileStatusActive).Error; err != nil {
		return migrationFastGPTTarget{}, false
	}
	credential := &models.StoreModelCredential{}
	if err := tx.Take(credential, "tenant_id = ? AND store_id = ?", tenantID, storeID).Error; err != nil ||
		credential.Status != enums.StoreCredentialStatusActive || credential.CredentialRevision <= 0 || strings.TrimSpace(credential.EncryptedKey) == "" {
		return migrationFastGPTTarget{}, false
	}
	return migrationFastGPTTarget{
		ProfileID: template.ID, ProfileRevision: template.Revision, CredentialRevision: credential.CredentialRevision,
	}, true
}

func reconcileManagedFastGPTStore(tx *gorm.DB, store *models.Store) error {
	if store == nil || store.Status != enums.StatusOk || store.KnowledgeBaseID <= 0 {
		return nil
	}
	knowledgeBase := validMigrationStoreKnowledgeBase(tx, store, store.KnowledgeBaseID)
	if knowledgeBase == nil || strings.TrimSpace(knowledgeBase.ConnectionID) != fastgptapi.ManagedConnectionID || strings.TrimSpace(knowledgeBase.DatasetID) == "" {
		return nil
	}
	binding := &models.FastGPTStoreTenant{}
	err := tx.Take(binding, "tenant_id = ? AND store_id = ?", store.TenantID, store.ID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil {
		if err := backfillFastGPTKnowledgeBaseAppliedTarget(tx, knowledgeBase, binding); err != nil {
			return err
		}
		if err := backfillFastGPTUsageAttribution(tx, store, knowledgeBase, binding); err != nil {
			return err
		}
	}
	retirement, err := ensureMigrationFastGPTRemoteRetirement(tx, store, knowledgeBase, binding)
	if err != nil {
		return err
	}
	if err := retireMigrationFastGPTJobs(tx, store, knowledgeBase); err != nil {
		return err
	}
	target, ready := loadMigrationFastGPTTarget(tx, store.TenantID, store.ID)
	if !ready {
		return nil
	}
	if binding.ID > 0 {
		if err := tx.Model(&models.FastGPTStoreTenant{}).Where("id = ? AND tenant_id = ?", binding.ID, store.TenantID).Updates(map[string]any{
			"target_profile_id": target.ProfileID, "target_profile_revision": target.ProfileRevision,
			"target_credential_revision": target.CredentialRevision, "updated_at": time.Now(), "update_user_name": "migration_072",
		}).Error; err != nil {
			return err
		}
	}
	return ensureMigrationFastGPTReprovisionJob(tx, store, knowledgeBase, retirement, target)
}

func ensureMigrationFastGPTRemoteRetirement(tx *gorm.DB, store *models.Store, knowledgeBase *models.KnowledgeBase, binding *models.FastGPTStoreTenant) (*models.FastGPTRemoteResourceRetirement, error) {
	item := &models.FastGPTRemoteResourceRetirement{}
	err := tx.Take(item, "tenant_id = ? AND store_id = ? AND legacy_dataset_id = ?", store.TenantID, store.ID, knowledgeBase.DatasetID).Error
	if err == nil {
		if item.LegacyKnowledgeBaseID != knowledgeBase.ID {
			return nil, fmt.Errorf("FastGPT legacy Dataset %s maps to conflicting knowledge bases in Store %d", knowledgeBase.DatasetID, store.ID)
		}
		return item, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	teamID := ""
	if binding != nil {
		teamID = strings.TrimSpace(binding.TenantTeamID)
	}
	now := time.Now()
	item = &models.FastGPTRemoteResourceRetirement{
		TenantID: store.TenantID, StoreID: store.ID, LegacyKnowledgeBaseID: knowledgeBase.ID,
		LegacyTeamID: teamID, LegacyDatasetID: knowledgeBase.DatasetID,
		Status: enums.FastGPTRemoteRetirementAwaitingReplacement, Reason: "migration_072_reprovision",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.Create(item).Error; err != nil {
		return nil, err
	}
	return item, nil
}

func retireMigrationFastGPTJobs(tx *gorm.DB, store *models.Store, knowledgeBase *models.KnowledgeBase) error {
	now := time.Now()
	return tx.Model(&models.FastGPTDatasetJob{}).
		Where("tenant_id = ? AND store_id = ? AND knowledge_base_id = ? AND status IN ?", store.TenantID, store.ID, knowledgeBase.ID, fastGPTNonterminalJobStatuses).
		Updates(map[string]any{
			"status": "failed", "completed_at": now, "next_retry_at": nil,
			"last_error": "legacy_remote_resource_reprovisioned", "last_error_class": "legacy_remote_resource_reprovisioned",
			"lease_owner": "", "lease_expires_at": nil, "updated_at": now,
		}).Error
}

func backfillFastGPTKnowledgeBaseAppliedTarget(tx *gorm.DB, knowledgeBase *models.KnowledgeBase, binding *models.FastGPTStoreTenant) error {
	if knowledgeBase == nil || binding == nil || knowledgeBase.FastGPTProfileStatus != "ready" ||
		strings.TrimSpace(knowledgeBase.FastGPTProfileID) == "" || strings.TrimSpace(knowledgeBase.FastGPTProfileRevision) == "" ||
		binding.AppliedProfileID <= 0 || binding.AppliedProfileRevision <= 0 || binding.AppliedCredentialRevision <= 0 ||
		strings.TrimSpace(binding.AppliedKeyFingerprint) == "" {
		return nil
	}
	values := []int64{
		knowledgeBase.FastGPTAppliedProfileID,
		knowledgeBase.FastGPTAppliedProfileRevision,
		knowledgeBase.FastGPTAppliedCredentialRevision,
	}
	allZero := values[0] == 0 && values[1] == 0 && values[2] == 0
	allMatch := values[0] == binding.AppliedProfileID && values[1] == binding.AppliedProfileRevision && values[2] == binding.AppliedCredentialRevision
	if !allZero && !allMatch {
		return fmt.Errorf("FastGPT knowledge base %d has a partial applied target that cannot be reconciled", knowledgeBase.ID)
	}
	if allMatch {
		return nil
	}
	if err := tx.Model(&models.KnowledgeBase{}).Where("id = ? AND tenant_id = ?", knowledgeBase.ID, knowledgeBase.TenantID).Updates(map[string]any{
		"fast_gpt_applied_profile_id":          binding.AppliedProfileID,
		"fast_gpt_applied_profile_revision":    binding.AppliedProfileRevision,
		"fast_gpt_applied_credential_revision": binding.AppliedCredentialRevision,
		"updated_at":                           time.Now(), "update_user_name": "migration_072",
	}).Error; err != nil {
		return err
	}
	knowledgeBase.FastGPTAppliedProfileID = binding.AppliedProfileID
	knowledgeBase.FastGPTAppliedProfileRevision = binding.AppliedProfileRevision
	knowledgeBase.FastGPTAppliedCredentialRevision = binding.AppliedCredentialRevision
	return nil
}

func backfillFastGPTUsageAttribution(tx *gorm.DB, store *models.Store, knowledgeBase *models.KnowledgeBase, binding *models.FastGPTStoreTenant) error {
	if binding == nil || binding.AppliedProfileID <= 0 || binding.AppliedProfileRevision <= 0 || binding.AppliedCredentialRevision <= 0 ||
		strings.TrimSpace(binding.AppliedKeyFingerprint) == "" || strings.TrimSpace(binding.TenantTeamID) == "" ||
		strings.TrimSpace(knowledgeBase.FastGPTProfileID) == "" || strings.TrimSpace(knowledgeBase.FastGPTProfileRevision) == "" ||
		knowledgeBase.FastGPTAppliedProfileID != binding.AppliedProfileID || knowledgeBase.FastGPTAppliedProfileRevision != binding.AppliedProfileRevision ||
		knowledgeBase.FastGPTAppliedCredentialRevision != binding.AppliedCredentialRevision {
		return nil
	}
	state := &models.FastGPTUsageSyncState{}
	err := tx.Take(state, "tenant_id = ? AND knowledge_base_id = ?", store.TenantID, knowledgeBase.ID).Error
	now := time.Now()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		state = &models.FastGPTUsageSyncState{
			TenantID: store.TenantID, StoreID: store.ID, KnowledgeBaseID: knowledgeBase.ID, TenantTeamID: binding.TenantTeamID,
			ModelProfileID: binding.AppliedProfileID, ProfileRevision: binding.AppliedProfileRevision,
			CredentialRevision: binding.AppliedCredentialRevision, KeyFingerprint: binding.AppliedKeyFingerprint,
			FastGPTProfileID: knowledgeBase.FastGPTProfileID, FastGPTRevision: knowledgeBase.FastGPTProfileRevision,
			CreatedAt: now, UpdatedAt: now,
		}
		return tx.Create(state).Error
	}
	if err != nil {
		return err
	}
	if state.StoreID != store.ID || (strings.TrimSpace(state.TenantTeamID) != "" && state.TenantTeamID != binding.TenantTeamID) {
		return fmt.Errorf("FastGPT usage state %d has a conflicting Store or Team scope", state.ID)
	}
	complete := state.ModelProfileID > 0 && state.ProfileRevision > 0 && state.CredentialRevision > 0 &&
		strings.TrimSpace(state.KeyFingerprint) != "" && strings.TrimSpace(state.FastGPTProfileID) != "" && strings.TrimSpace(state.FastGPTRevision) != ""
	if complete {
		return nil
	}
	if (state.ModelProfileID > 0 && state.ModelProfileID != binding.AppliedProfileID) ||
		(state.ProfileRevision > 0 && state.ProfileRevision != binding.AppliedProfileRevision) ||
		(state.CredentialRevision > 0 && state.CredentialRevision != binding.AppliedCredentialRevision) ||
		(strings.TrimSpace(state.KeyFingerprint) != "" && state.KeyFingerprint != binding.AppliedKeyFingerprint) ||
		(strings.TrimSpace(state.FastGPTProfileID) != "" && state.FastGPTProfileID != knowledgeBase.FastGPTProfileID) ||
		(strings.TrimSpace(state.FastGPTRevision) != "" && state.FastGPTRevision != knowledgeBase.FastGPTProfileRevision) {
		return fmt.Errorf("FastGPT usage state %d has a partial attribution that cannot be reconciled", state.ID)
	}
	return tx.Model(&models.FastGPTUsageSyncState{}).Where("id = ? AND tenant_id = ?", state.ID, store.TenantID).Updates(map[string]any{
		"store_id": store.ID, "tenant_team_id": binding.TenantTeamID,
		"model_profile_id": binding.AppliedProfileID, "profile_revision": binding.AppliedProfileRevision,
		"credential_revision": binding.AppliedCredentialRevision, "key_fingerprint": binding.AppliedKeyFingerprint,
		"fast_gpt_profile_id": knowledgeBase.FastGPTProfileID, "fast_gpt_revision": knowledgeBase.FastGPTProfileRevision,
		"last_error": "", "updated_at": now,
	}).Error
}

func ensureMigrationFastGPTReprovisionJob(tx *gorm.DB, store *models.Store, knowledgeBase *models.KnowledgeBase, retirement *models.FastGPTRemoteResourceRetirement, target migrationFastGPTTarget) error {
	if retirement == nil || retirement.ID <= 0 {
		return fmt.Errorf("FastGPT remote retirement is required before reprovisioning")
	}
	taskKey := migrationFastGPTReprovisionTaskKey(store.TenantID, store.ID, retirement.ID, target)
	existing := &models.FastGPTDatasetJob{}
	err := tx.Take(existing, "task_key = ?", taskKey).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	now := time.Now()
	return tx.Create(&models.FastGPTDatasetJob{
		TenantID: store.TenantID, StoreID: store.ID, KnowledgeBaseID: 0,
		TaskKey: taskKey, Action: "create_dataset", Status: "pending", DatasetID: "", Filename: knowledgeBase.Name,
		TargetProfileID: target.ProfileID, TargetProfileRevision: target.ProfileRevision,
		TargetCredentialRevision: target.CredentialRevision, CreatedAt: now, UpdatedAt: now,
	}).Error
}

func migrationFastGPTReprovisionTaskKey(tenantID, storeID, retirementID int64, target migrationFastGPTTarget) string {
	raw := fmt.Sprintf("%d:%d:%d:%d:%d:%d", tenantID, storeID, retirementID, target.ProfileID, target.ProfileRevision, target.CredentialRevision)
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("fastgpt-reprovision-m72-%x", sum[:16])
}
