package migration

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
)

func backfillStoreBindingAttribution(tx *gorm.DB) error {
	if err := backfillLegacyStoreModelCredentials(tx); err != nil {
		return err
	}
	if err := backfillStoreModelCredentialAudits(tx); err != nil {
		return err
	}
	if err := backfillModelProfileTestRunBindings(tx); err != nil {
		return err
	}
	if err := backfillAIUsageEventBindings(tx); err != nil {
		return err
	}
	if err := backfillAIUsageGatewayCallBindings(tx); err != nil {
		return err
	}
	if err := backfillKnowledgeBaseCredentialOwners(tx); err != nil {
		return err
	}
	if err := backfillFastGPTStoreTenantOwners(tx); err != nil {
		return err
	}
	if err := backfillFastGPTDatasetJobOwners(tx); err != nil {
		return err
	}
	if err := backfillFastGPTUsageSyncOwners(tx); err != nil {
		return err
	}
	return validateStoreBindingAttributionBackfill(tx)
}

func backfillLegacyStoreModelCredentials(tx *gorm.DB) error {
	var credentials []models.StoreModelCredential
	if err := tx.Order("tenant_id ASC, store_id ASC, id ASC").Find(&credentials).Error; err != nil {
		return fmt.Errorf("load Store model credentials: %w", err)
	}
	for i := range credentials {
		credential := &credentials[i]
		bindings, err := migrationStoreBindings(tx, credential.TenantID, credential.StoreID)
		if err != nil {
			return err
		}
		if credential.StoreStaffBindingID > 0 {
			if !containsMigrationBinding(bindings, credential.StoreStaffBindingID) {
				return fmt.Errorf("Store model credential %d references an invalid Store staff binding", credential.ID)
			}
			continue
		}
		if !legacyStoreCredentialConfigured(credential) {
			if err := expandEmptyLegacyStoreCredential(tx, credential, bindings); err != nil {
				return err
			}
			continue
		}
		evidence, err := legacyCredentialBindingEvidence(tx, credential)
		if err != nil {
			return err
		}
		bindingID, err := chooseMigrationStoreBinding(bindings, evidence, fmt.Sprintf("configured Store model credential %d", credential.ID))
		if err != nil {
			return err
		}
		if err := tx.Model(&models.StoreModelCredential{}).Where("id = ?", credential.ID).Updates(map[string]any{
			"store_staff_binding_id": bindingID,
			"updated_at":             time.Now(),
			"update_user_id":         constants.SystemAuditUserID,
			"update_user_name":       constants.SystemAuditUserName,
		}).Error; err != nil {
			return fmt.Errorf("backfill Store model credential %d binding: %w", credential.ID, err)
		}
		credential.StoreStaffBindingID = bindingID
	}
	return nil
}

func expandEmptyLegacyStoreCredential(tx *gorm.DB, credential *models.StoreModelCredential, bindings []models.StoreStaffBinding) error {
	if credential == nil {
		return nil
	}
	if len(bindings) == 0 {
		var auditCount int64
		if err := tx.Model(&models.StoreModelCredentialAuditLog{}).Where("credential_id = ?", credential.ID).Count(&auditCount).Error; err != nil {
			return err
		}
		if auditCount > 0 {
			return fmt.Errorf("empty Store model credential %d has audit history but no Store staff binding", credential.ID)
		}
		if err := tx.Delete(&models.StoreModelCredential{}, credential.ID).Error; err != nil {
			return fmt.Errorf("remove orphan empty Store model credential %d: %w", credential.ID, err)
		}
		return nil
	}
	now := time.Now()
	if err := tx.Model(&models.StoreModelCredential{}).Where("id = ?", credential.ID).Updates(map[string]any{
		"store_staff_binding_id": bindings[0].ID,
		"updated_at":             now,
		"update_user_id":         constants.SystemAuditUserID,
		"update_user_name":       constants.SystemAuditUserName,
	}).Error; err != nil {
		return fmt.Errorf("assign empty Store model credential %d: %w", credential.ID, err)
	}
	for i := 1; i < len(bindings); i++ {
		clone := *credential
		clone.ID = 0
		clone.StoreStaffBindingID = bindings[i].ID
		clone.AuditFields = systemMigrationAudit(now)
		if err := tx.Create(&clone).Error; err != nil {
			return fmt.Errorf("create empty Store model credential for binding %d: %w", bindings[i].ID, err)
		}
	}
	return nil
}

func legacyStoreCredentialConfigured(credential *models.StoreModelCredential) bool {
	if credential == nil {
		return false
	}
	return strings.TrimSpace(credential.EncryptedKey) != "" || strings.TrimSpace(credential.KeyNonce) != "" ||
		strings.TrimSpace(credential.KeyFingerprint) != "" || credential.CredentialRevision > 0 ||
		strings.TrimSpace(credential.CandidateEncryptedKey) != "" || strings.TrimSpace(credential.CandidateKeyNonce) != "" ||
		strings.TrimSpace(credential.CandidateKeyFingerprint) != "" || credential.CandidateRevision > 0 ||
		(credential.Status != "" && credential.Status != enums.StoreCredentialStatusUnconfigured)
}

func legacyCredentialBindingEvidence(tx *gorm.DB, credential *models.StoreModelCredential) ([]int64, error) {
	if credential == nil {
		return nil, nil
	}
	revisions := positiveMigrationIDs(credential.CredentialRevision, credential.CandidateRevision)
	evidence := make([]int64, 0)
	if len(revisions) == 0 {
		return evidence, nil
	}
	queries := []struct {
		model          any
		bindingColumn  string
		revisionColumn string
	}{
		{&models.ModelProfileTestRun{}, "store_staff_binding_id", "credential_revision"},
		{&models.AIUsageEvent{}, "store_staff_binding_id", "credential_revision"},
		{&models.AIUsageGatewayCall{}, "store_staff_binding_id", "credential_revision"},
		{&models.FastGPTDatasetJob{}, "target_store_staff_binding_id", "target_credential_revision"},
		{&models.FastGPTUsageSyncState{}, "store_staff_binding_id", "credential_revision"},
	}
	for _, query := range queries {
		var ids []int64
		if err := tx.Model(query.model).Distinct(query.bindingColumn).
			Where("tenant_id = ? AND store_id = ? AND "+query.revisionColumn+" IN ? AND "+query.bindingColumn+" > 0", credential.TenantID, credential.StoreID, revisions).
			Pluck(query.bindingColumn, &ids).Error; err != nil {
			return nil, err
		}
		evidence = append(evidence, ids...)
	}
	var states []models.FastGPTStoreTenant
	if err := tx.Where("tenant_id = ? AND store_id = ?", credential.TenantID, credential.StoreID).Find(&states).Error; err != nil {
		return nil, err
	}
	for i := range states {
		if containsMigrationID(revisions, states[i].TargetCredentialRevision) {
			evidence = append(evidence, states[i].TargetStoreStaffBindingID)
		}
		if containsMigrationID(revisions, states[i].AppliedCredentialRevision) {
			evidence = append(evidence, states[i].AppliedStoreStaffBindingID)
		}
	}
	var knowledgeBases []models.KnowledgeBase
	if err := tx.Where("tenant_id = ? AND store_id = ? AND fast_gpt_applied_credential_revision IN ?", credential.TenantID, credential.StoreID, revisions).Find(&knowledgeBases).Error; err != nil {
		return nil, err
	}
	for i := range knowledgeBases {
		evidence = append(evidence, knowledgeBases[i].FastGPTAppliedStoreStaffBindingID)
	}
	return positiveMigrationIDs(evidence...), nil
}

func backfillStoreModelCredentialAudits(tx *gorm.DB) error {
	var logs []models.StoreModelCredentialAuditLog
	if err := tx.Order("id ASC").Find(&logs).Error; err != nil {
		return err
	}
	for i := range logs {
		log := &logs[i]
		if log.CredentialID == 0 {
			if log.Action != enums.CredentialAuditActionPolicyUpdate || log.StoreStaffBindingID != 0 {
				return fmt.Errorf("Store credential audit %d has no deterministic credential", log.ID)
			}
			continue
		}
		var credential models.StoreModelCredential
		if err := tx.First(&credential, log.CredentialID).Error; err != nil {
			return fmt.Errorf("Store credential audit %d references a missing credential", log.ID)
		}
		if credential.TenantID != log.TenantID || credential.StoreID != log.StoreID || credential.StoreStaffBindingID <= 0 {
			return fmt.Errorf("Store credential audit %d scope conflicts with its credential", log.ID)
		}
		if log.StoreStaffBindingID > 0 && log.StoreStaffBindingID != credential.StoreStaffBindingID {
			return fmt.Errorf("Store credential audit %d has conflicting binding attribution", log.ID)
		}
		if log.StoreStaffBindingID == 0 {
			if err := tx.Model(&models.StoreModelCredentialAuditLog{}).Where("id = ?", log.ID).
				Update("store_staff_binding_id", credential.StoreStaffBindingID).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func backfillModelProfileTestRunBindings(tx *gorm.DB) error {
	var runs []models.ModelProfileTestRun
	if err := tx.Order("id ASC").Find(&runs).Error; err != nil {
		return err
	}
	for i := range runs {
		run := &runs[i]
		if run.StoreID <= 0 || run.StoreStaffBindingID > 0 {
			continue
		}
		evidence, err := credentialRevisionBindingEvidence(tx, run.TenantID, run.StoreID, run.CredentialRevision, "")
		if err != nil {
			return err
		}
		bindingID, err := resolveMigrationStoreBinding(tx, run.TenantID, run.StoreID, evidence, fmt.Sprintf("model Profile test run %d", run.ID))
		if err != nil {
			return err
		}
		if err := tx.Model(&models.ModelProfileTestRun{}).Where("id = ?", run.ID).Update("store_staff_binding_id", bindingID).Error; err != nil {
			return err
		}
	}
	return nil
}

func backfillAIUsageEventBindings(tx *gorm.DB) error {
	var events []models.AIUsageEvent
	if err := tx.Order("id ASC").Find(&events).Error; err != nil {
		return err
	}
	for i := range events {
		event := &events[i]
		if event.StoreID <= 0 {
			continue
		}
		evidence, err := runtimeBindingEvidence(tx, event.TenantID, event.StoreID, event.StoreStaffBindingID, event.WxWorkInstanceID, event.ConversationID, event.MessageID, event.KnowledgeBaseID, event.CredentialRevision, event.KeyFingerprint)
		if err != nil {
			return err
		}
		bindingID, err := resolveMigrationStoreBinding(tx, event.TenantID, event.StoreID, evidence, fmt.Sprintf("AI usage event %d", event.ID))
		if err != nil {
			return err
		}
		if event.StoreStaffBindingID == 0 {
			if err := tx.Model(&models.AIUsageEvent{}).Where("id = ?", event.ID).Update("store_staff_binding_id", bindingID).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func backfillAIUsageGatewayCallBindings(tx *gorm.DB) error {
	var calls []models.AIUsageGatewayCall
	if err := tx.Order("id ASC").Find(&calls).Error; err != nil {
		return err
	}
	for i := range calls {
		call := &calls[i]
		if call.StoreID <= 0 {
			continue
		}
		evidence, err := runtimeBindingEvidence(tx, call.TenantID, call.StoreID, call.StoreStaffBindingID, call.WxWorkInstanceID, call.ConversationID, call.MessageID, 0, call.CredentialRevision, call.KeyFingerprint)
		if err != nil {
			return err
		}
		bindingID, err := resolveMigrationStoreBinding(tx, call.TenantID, call.StoreID, evidence, fmt.Sprintf("AI usage gateway call %d", call.ID))
		if err != nil {
			return err
		}
		if call.StoreStaffBindingID == 0 {
			if err := tx.Model(&models.AIUsageGatewayCall{}).Where("id = ?", call.ID).Updates(map[string]any{
				"store_staff_binding_id": bindingID, "updated_at": time.Now(),
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func backfillKnowledgeBaseCredentialOwners(tx *gorm.DB) error {
	var items []models.KnowledgeBase
	if err := tx.Order("id ASC").Find(&items).Error; err != nil {
		return err
	}
	for i := range items {
		item := &items[i]
		requiresOwner := item.StoreID > 0 && (item.FastGPTAppliedCredentialRevision > 0 || item.FastGPTAppliedProfileID > 0 || strings.EqualFold(item.FastGPTProfileStatus, "ready"))
		if !requiresOwner {
			continue
		}
		evidence := []int64{item.FastGPTAppliedStoreStaffBindingID}
		credentialEvidence, err := credentialRevisionBindingEvidence(tx, item.TenantID, item.StoreID, item.FastGPTAppliedCredentialRevision, "")
		if err != nil {
			return err
		}
		evidence = append(evidence, credentialEvidence...)
		var state models.FastGPTStoreTenant
		if err := tx.Where("tenant_id = ? AND store_id = ?", item.TenantID, item.StoreID).Take(&state).Error; err == nil {
			evidence = append(evidence, state.AppliedStoreStaffBindingID)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		bindingID, err := resolveMigrationStoreBinding(tx, item.TenantID, item.StoreID, evidence, fmt.Sprintf("FastGPT knowledge base %d", item.ID))
		if err != nil {
			return err
		}
		if item.FastGPTAppliedStoreStaffBindingID == 0 {
			if err := tx.Model(&models.KnowledgeBase{}).Where("id = ?", item.ID).Updates(map[string]any{
				"fast_gpt_applied_store_staff_binding_id": bindingID,
				"updated_at": time.Now(), "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func backfillFastGPTStoreTenantOwners(tx *gorm.DB) error {
	var states []models.FastGPTStoreTenant
	if err := tx.Order("id ASC").Find(&states).Error; err != nil {
		return err
	}
	for i := range states {
		state := &states[i]
		columns := map[string]any{}
		for _, target := range []struct {
			name       string
			bindingID  int64
			revision   int64
			profileID  int64
			otherOwner int64
		}{
			{"target_store_staff_binding_id", state.TargetStoreStaffBindingID, state.TargetCredentialRevision, state.TargetProfileID, state.AppliedStoreStaffBindingID},
			{"applied_store_staff_binding_id", state.AppliedStoreStaffBindingID, state.AppliedCredentialRevision, state.AppliedProfileID, state.TargetStoreStaffBindingID},
		} {
			if target.revision <= 0 && target.profileID <= 0 && target.bindingID <= 0 {
				continue
			}
			evidence := []int64{target.bindingID, target.otherOwner}
			credentialEvidence, err := credentialRevisionBindingEvidence(tx, state.TenantID, state.StoreID, target.revision, state.AppliedKeyFingerprint)
			if err != nil {
				return err
			}
			evidence = append(evidence, credentialEvidence...)
			var knowledgeBases []models.KnowledgeBase
			if err := tx.Where("tenant_id = ? AND store_id = ? AND fast_gpt_applied_store_staff_binding_id > 0", state.TenantID, state.StoreID).Find(&knowledgeBases).Error; err != nil {
				return err
			}
			for j := range knowledgeBases {
				evidence = append(evidence, knowledgeBases[j].FastGPTAppliedStoreStaffBindingID)
			}
			bindingID, err := resolveMigrationStoreBinding(tx, state.TenantID, state.StoreID, evidence, fmt.Sprintf("FastGPT Store state %d %s", state.ID, target.name))
			if err != nil {
				return err
			}
			if target.bindingID == 0 {
				columns[target.name] = bindingID
			}
		}
		if len(columns) > 0 {
			columns["updated_at"] = time.Now()
			columns["update_user_id"] = constants.SystemAuditUserID
			columns["update_user_name"] = constants.SystemAuditUserName
			if err := tx.Model(&models.FastGPTStoreTenant{}).Where("id = ?", state.ID).Updates(columns).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func backfillFastGPTDatasetJobOwners(tx *gorm.DB) error {
	var jobs []models.FastGPTDatasetJob
	if err := tx.Order("id ASC").Find(&jobs).Error; err != nil {
		return err
	}
	for i := range jobs {
		job := &jobs[i]
		if job.StoreID <= 0 || (job.TargetCredentialRevision <= 0 && job.TargetStoreStaffBindingID <= 0) {
			continue
		}
		evidence := []int64{job.TargetStoreStaffBindingID}
		credentialEvidence, err := credentialRevisionBindingEvidence(tx, job.TenantID, job.StoreID, job.TargetCredentialRevision, "")
		if err != nil {
			return err
		}
		evidence = append(evidence, credentialEvidence...)
		evidence = append(evidence, fastGPTResourceBindingEvidence(tx, job.TenantID, job.StoreID, job.KnowledgeBaseID)...)
		bindingID, err := resolveMigrationStoreBinding(tx, job.TenantID, job.StoreID, evidence, fmt.Sprintf("FastGPT Dataset job %d", job.ID))
		if err != nil {
			return err
		}
		if job.TargetStoreStaffBindingID == 0 {
			if err := tx.Model(&models.FastGPTDatasetJob{}).Where("id = ?", job.ID).Updates(map[string]any{
				"target_store_staff_binding_id": bindingID, "updated_at": time.Now(),
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func backfillFastGPTUsageSyncOwners(tx *gorm.DB) error {
	var states []models.FastGPTUsageSyncState
	if err := tx.Order("id ASC").Find(&states).Error; err != nil {
		return err
	}
	for i := range states {
		state := &states[i]
		if state.StoreID <= 0 || (state.CredentialRevision <= 0 && state.StoreStaffBindingID <= 0) {
			continue
		}
		evidence := []int64{state.StoreStaffBindingID}
		credentialEvidence, err := credentialRevisionBindingEvidence(tx, state.TenantID, state.StoreID, state.CredentialRevision, state.KeyFingerprint)
		if err != nil {
			return err
		}
		evidence = append(evidence, credentialEvidence...)
		evidence = append(evidence, fastGPTResourceBindingEvidence(tx, state.TenantID, state.StoreID, state.KnowledgeBaseID)...)
		bindingID, err := resolveMigrationStoreBinding(tx, state.TenantID, state.StoreID, evidence, fmt.Sprintf("FastGPT usage sync state %d", state.ID))
		if err != nil {
			return err
		}
		if state.StoreStaffBindingID == 0 {
			if err := tx.Model(&models.FastGPTUsageSyncState{}).Where("id = ?", state.ID).Updates(map[string]any{
				"store_staff_binding_id": bindingID, "updated_at": time.Now(),
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func runtimeBindingEvidence(tx *gorm.DB, tenantID, storeID, currentBindingID, instanceID, conversationID, messageID, knowledgeBaseID, credentialRevision int64, fingerprint string) ([]int64, error) {
	evidence := []int64{currentBindingID}
	if instanceID > 0 {
		var instance models.WxWorkProtocolInstance
		if err := tx.First(&instance, instanceID).Error; err != nil {
			return nil, err
		}
		if instance.TenantID != tenantID || instance.StoreID != storeID {
			return nil, fmt.Errorf("protocol instance %d conflicts with model usage Store scope", instanceID)
		}
		evidence = append(evidence, instance.StoreStaffBindingID)
	}
	if conversationID == 0 && messageID > 0 {
		var message models.Message
		if err := tx.First(&message, messageID).Error; err != nil {
			return nil, err
		}
		conversationID = message.ConversationID
	}
	if conversationID > 0 {
		var conversation models.Conversation
		if err := tx.First(&conversation, conversationID).Error; err != nil {
			return nil, err
		}
		if conversation.TenantID != tenantID || (conversation.StoreID > 0 && conversation.StoreID != storeID) {
			return nil, fmt.Errorf("conversation %d conflicts with model usage Store scope", conversationID)
		}
		evidence = append(evidence, conversation.StoreStaffBindingID)
		var route models.ConversationRouteState
		if err := tx.Where("tenant_id = ? AND conversation_id = ?", tenantID, conversationID).Take(&route).Error; err == nil {
			evidence = append(evidence, route.StoreStaffBindingID)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	if knowledgeBaseID > 0 {
		evidence = append(evidence, fastGPTResourceBindingEvidence(tx, tenantID, storeID, knowledgeBaseID)...)
	}
	credentialEvidence, err := credentialRevisionBindingEvidence(tx, tenantID, storeID, credentialRevision, fingerprint)
	if err != nil {
		return nil, err
	}
	evidence = append(evidence, credentialEvidence...)
	return positiveMigrationIDs(evidence...), nil
}

func credentialRevisionBindingEvidence(tx *gorm.DB, tenantID, storeID, revision int64, fingerprint string) ([]int64, error) {
	if revision <= 0 {
		return nil, nil
	}
	var credentials []models.StoreModelCredential
	query := tx.Where("tenant_id = ? AND store_id = ? AND store_staff_binding_id > 0 AND (credential_revision = ? OR candidate_revision = ?)", tenantID, storeID, revision, revision)
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint != "" {
		query = query.Where("key_fingerprint = ? OR candidate_key_fingerprint = ?", fingerprint, fingerprint)
	}
	if err := query.Find(&credentials).Error; err != nil {
		return nil, err
	}
	ret := make([]int64, 0, len(credentials))
	for i := range credentials {
		ret = append(ret, credentials[i].StoreStaffBindingID)
	}
	return positiveMigrationIDs(ret...), nil
}

func fastGPTResourceBindingEvidence(tx *gorm.DB, tenantID, storeID, knowledgeBaseID int64) []int64 {
	evidence := make([]int64, 0, 3)
	if knowledgeBaseID > 0 {
		var knowledgeBase models.KnowledgeBase
		if err := tx.First(&knowledgeBase, knowledgeBaseID).Error; err == nil && knowledgeBase.TenantID == tenantID && knowledgeBase.StoreID == storeID {
			evidence = append(evidence, knowledgeBase.FastGPTAppliedStoreStaffBindingID)
		}
	}
	var state models.FastGPTStoreTenant
	if err := tx.Where("tenant_id = ? AND store_id = ?", tenantID, storeID).Take(&state).Error; err == nil {
		evidence = append(evidence, state.TargetStoreStaffBindingID, state.AppliedStoreStaffBindingID)
	}
	return positiveMigrationIDs(evidence...)
}

func resolveMigrationStoreBinding(tx *gorm.DB, tenantID, storeID int64, evidence []int64, entity string) (int64, error) {
	bindings, err := migrationStoreBindings(tx, tenantID, storeID)
	if err != nil {
		return 0, err
	}
	return chooseMigrationStoreBinding(bindings, evidence, entity)
}

func chooseMigrationStoreBinding(bindings []models.StoreStaffBinding, evidence []int64, entity string) (int64, error) {
	unique := positiveMigrationIDs(evidence...)
	for _, bindingID := range unique {
		if !containsMigrationBinding(bindings, bindingID) {
			return 0, fmt.Errorf("%s references Store staff binding %d outside its Store", entity, bindingID)
		}
	}
	if len(unique) == 1 {
		return unique[0], nil
	}
	if len(unique) > 1 {
		return 0, fmt.Errorf("%s has ambiguous Store staff binding evidence", entity)
	}
	if len(bindings) == 1 {
		return bindings[0].ID, nil
	}
	if len(bindings) == 0 {
		return 0, fmt.Errorf("%s has no Store staff binding", entity)
	}
	return 0, fmt.Errorf("%s cannot deterministically choose one of %d Store staff bindings", entity, len(bindings))
}

func migrationStoreBindings(tx *gorm.DB, tenantID, storeID int64) ([]models.StoreStaffBinding, error) {
	var bindings []models.StoreStaffBinding
	if err := tx.Where("tenant_id = ? AND store_id = ? AND status <> ?", tenantID, storeID, enums.StatusDeleted).Order("id ASC").Find(&bindings).Error; err != nil {
		return nil, err
	}
	return bindings, nil
}

func containsMigrationBinding(bindings []models.StoreStaffBinding, bindingID int64) bool {
	for i := range bindings {
		if bindings[i].ID == bindingID {
			return true
		}
	}
	return false
}

func positiveMigrationIDs(values ...int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	ret := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	return ret
}

func containsMigrationID(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validateStoreBindingAttributionBackfill(tx *gorm.DB) error {
	checks := []struct {
		model any
		where string
		name  string
	}{
		{&models.StoreModelCredential{}, "store_staff_binding_id <= 0", "Store model credential"},
		{&models.StoreModelCredentialAuditLog{}, "credential_id > 0 AND store_staff_binding_id <= 0", "Store credential audit"},
		{&models.ModelProfileTestRun{}, "store_id > 0 AND credential_revision > 0 AND store_staff_binding_id <= 0", "model Profile test evidence"},
		{&models.AIUsageEvent{}, "store_id > 0 AND credential_revision > 0 AND store_staff_binding_id <= 0", "AI usage event"},
		{&models.AIUsageGatewayCall{}, "store_id > 0 AND credential_revision > 0 AND store_staff_binding_id <= 0", "AI gateway call"},
		{&models.FastGPTDatasetJob{}, "store_id > 0 AND target_credential_revision > 0 AND target_store_staff_binding_id <= 0", "FastGPT Dataset job"},
		{&models.FastGPTUsageSyncState{}, "store_id > 0 AND credential_revision > 0 AND store_staff_binding_id <= 0", "FastGPT usage sync state"},
		{&models.KnowledgeBase{}, "store_id > 0 AND fast_gpt_applied_credential_revision > 0 AND fast_gpt_applied_store_staff_binding_id <= 0", "FastGPT knowledge base"},
	}
	for _, check := range checks {
		var count int64
		if err := tx.Model(check.model).Where(check.where).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("%s binding attribution remains incomplete for %d records", check.name, count)
		}
	}
	return nil
}
