package repositories

import (
	"time"

	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
)

var TenantReleaseReadinessRepository = newTenantReleaseReadinessRepository()

type tenantReleaseReadinessRepository struct{}

func newTenantReleaseReadinessRepository() *tenantReleaseReadinessRepository {
	return &tenantReleaseReadinessRepository{}
}

type TenantReleaseReadinessStoreAccountState struct {
	StoreID            int64
	ActiveBindingCount int64
	ReadyAccountCount  int64
}

type TenantReleaseReadinessCredentialState struct {
	StoreID               int64
	CredentialRevision    int64
	Status                enums.StoreCredentialStatus
	LastTestStatus        string
	LastTestedAt          *time.Time
	LastFastGPTSyncStatus string
	LastFastGPTSyncedAt   *time.Time
	HasActiveEncryptedKey int64
}

type TenantReleaseReadinessFastGPTState struct {
	StoreID                   int64
	HasTenantTeam             int64
	Status                    string
	TargetProfileID           int64
	TargetProfileRevision     int64
	AppliedProfileID          int64
	AppliedProfileRevision    int64
	TargetCredentialRevision  int64
	AppliedCredentialRevision int64
	ReadinessStatus           string
	LastSyncedAt              *time.Time
}

type TenantReleaseReadinessKnowledgeState struct {
	KnowledgeBaseID                  int64
	StoreID                          int64
	DatasetReady                     int64
	ConnectionID                     string
	FastGPTProfileReady              int64
	FastGPTAppliedProfileID          int64
	FastGPTAppliedProfileRevision    int64
	FastGPTAppliedCredentialRevision int64
	Status                           enums.Status
}

type TenantReleaseReadinessEvidenceFilter struct {
	NewAPIGateway            string
	SuccessfulUsageStatuses  []string
	AIHandoffContent         string
	ReconcileStatus          string
	ReconcileMatchStrategy   string
	ReconcileMatchConfidence string
	AITagSource              string
}

type TenantReleaseReadinessEvidence struct {
	StoreID                   int64
	SuccessfulNewAPICallCount int64
	CustomerAIReplyCount      int64
	AIHandoffCount            int64
	RuleAssignmentCount       int64
	ReconciledBillingCount    int64
	AICustomerTagChangeCount  int64
}

type tenantReleaseReadinessCountRow struct {
	StoreID int64
	Count   int64
}

func (r *tenantReleaseReadinessRepository) FindStoreAccountStates(
	db *gorm.DB,
	tenantID int64,
	storeIDs []int64,
) ([]TenantReleaseReadinessStoreAccountState, error) {
	ret := make([]TenantReleaseReadinessStoreAccountState, 0)
	if db == nil || tenantID <= 0 || len(storeIDs) == 0 {
		return ret, nil
	}
	err := db.Table("t_store_staff_binding AS binding").
		Select(`
			binding.store_id,
			COUNT(binding.id) AS active_binding_count,
			SUM(CASE
				WHEN account.id IS NOT NULL
					AND account.tenant_id = binding.tenant_id
					AND account.status = ?
					AND account.approval_status = ?
					AND account.deleted_at IS NULL
					AND binding.agent_team_id > 0
				THEN 1 ELSE 0
			END) AS ready_account_count
		`, enums.StatusOk, enums.UserApprovalStatusApproved).
		Joins("LEFT JOIN t_user AS account ON account.id = binding.user_id").
		Where("binding.tenant_id = ? AND binding.store_id IN ? AND binding.status = ?", tenantID, storeIDs, enums.StatusOk).
		Group("binding.store_id").
		Order("binding.store_id ASC").
		Scan(&ret).Error
	return ret, err
}

func (r *tenantReleaseReadinessRepository) FindCredentialStates(
	db *gorm.DB,
	tenantID int64,
	storeIDs []int64,
) ([]TenantReleaseReadinessCredentialState, error) {
	ret := make([]TenantReleaseReadinessCredentialState, 0)
	if db == nil || tenantID <= 0 || len(storeIDs) == 0 {
		return ret, nil
	}
	err := db.Table("t_store_model_credential").
		Select(`
			store_id,
			credential_revision,
			status,
			last_test_status,
			last_tested_at,
			last_fast_gpt_sync_status,
			last_fast_gpt_synced_at,
			CASE
				WHEN encrypted_key <> ''
					AND key_nonce <> ''
					AND key_fingerprint <> ''
					AND cipher_version <> ''
					AND master_key_id <> ''
				THEN 1 ELSE 0
			END AS has_active_encrypted_key
		`).
		Where("tenant_id = ? AND store_id IN ?", tenantID, storeIDs).
		Order("store_id ASC").
		Scan(&ret).Error
	return ret, err
}

func (r *tenantReleaseReadinessRepository) FindFastGPTStates(
	db *gorm.DB,
	tenantID int64,
	storeIDs []int64,
) ([]TenantReleaseReadinessFastGPTState, error) {
	ret := make([]TenantReleaseReadinessFastGPTState, 0)
	if db == nil || tenantID <= 0 || len(storeIDs) == 0 {
		return ret, nil
	}
	err := db.Table("t_fast_gpt_store_tenant").
		Select(`
			store_id,
			CASE WHEN tenant_team_id <> '' THEN 1 ELSE 0 END AS has_tenant_team,
			status,
			target_profile_id,
			target_profile_revision,
			applied_profile_id,
			applied_profile_revision,
			target_credential_revision,
			applied_credential_revision,
			readiness_status,
			last_synced_at
		`).
		Where("tenant_id = ? AND store_id IN ?", tenantID, storeIDs).
		Order("store_id ASC").
		Scan(&ret).Error
	return ret, err
}

func (r *tenantReleaseReadinessRepository) FindKnowledgeStates(
	db *gorm.DB,
	tenantID int64,
	knowledgeBaseIDs []int64,
) ([]TenantReleaseReadinessKnowledgeState, error) {
	ret := make([]TenantReleaseReadinessKnowledgeState, 0)
	if db == nil || tenantID <= 0 || len(knowledgeBaseIDs) == 0 {
		return ret, nil
	}
	err := db.Table("t_knowledge_base").
		Select(`
			id AS knowledge_base_id,
			store_id,
			CASE WHEN dataset_id <> '' THEN 1 ELSE 0 END AS dataset_ready,
			connection_id,
			CASE
				WHEN fast_gpt_profile_id <> ''
					AND fast_gpt_profile_revision <> ''
					AND fast_gpt_profile_status = 'ready'
					AND fast_gpt_profile_synced_at IS NOT NULL
				THEN 1 ELSE 0
			END AS fast_gpt_profile_ready,
			fast_gpt_applied_profile_id,
			fast_gpt_applied_profile_revision,
			fast_gpt_applied_credential_revision,
			status
		`).
		Where("tenant_id = ? AND id IN ?", tenantID, knowledgeBaseIDs).
		Order("id ASC").
		Scan(&ret).Error
	return ret, err
}

func (r *tenantReleaseReadinessRepository) FindEvidence(
	db *gorm.DB,
	tenantID int64,
	storeIDs []int64,
	start time.Time,
	filter TenantReleaseReadinessEvidenceFilter,
) (map[int64]TenantReleaseReadinessEvidence, error) {
	ret := make(map[int64]TenantReleaseReadinessEvidence, len(storeIDs))
	for _, storeID := range storeIDs {
		ret[storeID] = TenantReleaseReadinessEvidence{StoreID: storeID}
	}
	if db == nil || tenantID <= 0 || len(storeIDs) == 0 || start.IsZero() {
		return ret, nil
	}

	queries := []struct {
		apply func(*TenantReleaseReadinessEvidence, int64)
		query *gorm.DB
	}{
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.SuccessfulNewAPICallCount = count
			},
			query: db.Table("t_ai_usage_event AS usage_event").
				Select("usage_event.store_id, COUNT(*) AS count").
				Joins("JOIN t_store_model_profile_assignment AS assignment ON assignment.tenant_id = usage_event.tenant_id AND assignment.store_id = usage_event.store_id").
				Joins("JOIN t_store_model_credential AS credential ON credential.tenant_id = usage_event.tenant_id AND credential.store_id = usage_event.store_id").
				Where("usage_event.tenant_id = ? AND usage_event.store_id IN ? AND usage_event.created_at >= ?", tenantID, storeIDs, start).
				Where("usage_event.gateway = ? AND usage_event.gateway_request_id <> '' AND usage_event.status IN ?", filter.NewAPIGateway, filter.SuccessfulUsageStatuses).
				Where("usage_event.model_profile_id = assignment.template_id AND usage_event.model_profile_revision = assignment.template_revision").
				Where("usage_event.credential_revision = credential.credential_revision").
				Group("usage_event.store_id"),
		},
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.CustomerAIReplyCount = count
			},
			query: db.Table("t_message AS message").
				Select("route.store_id, COUNT(*) AS count").
				Joins("JOIN t_conversation_route_state AS route ON route.tenant_id = message.tenant_id AND route.conversation_id = message.conversation_id").
				Where("message.tenant_id = ? AND route.store_id IN ? AND message.created_at >= ?", tenantID, storeIDs, start).
				Where("message.sender_type = ? AND message.send_status IN ?", enums.IMSenderTypeAI, []enums.IMMessageStatus{
					enums.IMMessageStatusSent, enums.IMMessageStatusDelivered, enums.IMMessageStatusRead,
				}).
				Where(`EXISTS (
					SELECT 1 FROM t_message AS customer_message
					WHERE customer_message.tenant_id = message.tenant_id
						AND customer_message.conversation_id = message.conversation_id
						AND customer_message.sender_type = ?
						AND customer_message.created_at >= ?
						AND customer_message.created_at <= message.created_at
				)`, enums.IMSenderTypeCustomer, start).
				Group("route.store_id"),
		},
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.AIHandoffCount = count
			},
			query: db.Table("t_conversation_event_log AS event").
				Select("route.store_id, COUNT(*) AS count").
				Joins("JOIN t_conversation_route_state AS route ON route.tenant_id = event.tenant_id AND route.conversation_id = event.conversation_id").
				Where("event.tenant_id = ? AND route.store_id IN ? AND event.created_at >= ?", tenantID, storeIDs, start).
				Where("event.event_type = ? AND event.operator_type = ? AND event.content = ?", enums.IMEventTypeTransfer, enums.IMSenderTypeAI, filter.AIHandoffContent).
				Group("route.store_id"),
		},
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.RuleAssignmentCount = count
			},
			query: db.Table("t_conversation_assignment AS assignment").
				Select("route.store_id, COUNT(*) AS count").
				Joins("JOIN t_conversation_route_state AS route ON route.tenant_id = assignment.tenant_id AND route.conversation_id = assignment.conversation_id").
				Where("assignment.tenant_id = ? AND route.store_id IN ? AND assignment.created_at >= ?", tenantID, storeIDs, start).
				Where("assignment.dispatch_mode = ? AND assignment.to_user_id > 0", enums.AgentTeamDispatchModeRule).
				Where(`EXISTS (
					SELECT 1 FROM t_conversation_event_log AS handoff
					WHERE handoff.tenant_id = assignment.tenant_id
						AND handoff.conversation_id = assignment.conversation_id
						AND handoff.event_type = ?
						AND handoff.operator_type = ?
						AND handoff.content = ?
						AND handoff.created_at >= ?
						AND handoff.created_at <= assignment.created_at
				)`, enums.IMEventTypeTransfer, enums.IMSenderTypeAI, filter.AIHandoffContent, start).
				Group("route.store_id"),
		},
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.ReconciledBillingCount = count
			},
			query: db.Table("t_ai_usage_gateway_call AS gateway_call").
				Select("gateway_call.store_id, COUNT(*) AS count").
				Joins("JOIN t_store_model_profile_assignment AS assignment ON assignment.tenant_id = gateway_call.tenant_id AND assignment.store_id = gateway_call.store_id").
				Joins("JOIN t_store_model_credential AS credential ON credential.tenant_id = gateway_call.tenant_id AND credential.store_id = gateway_call.store_id").
				Where("gateway_call.tenant_id = ? AND gateway_call.store_id IN ? AND gateway_call.created_at >= ?", tenantID, storeIDs, start).
				Where("gateway_call.gateway = ? AND gateway_call.gateway_request_id <> ''", filter.NewAPIGateway).
				Where("gateway_call.reconcile_status = ? AND gateway_call.match_strategy = ? AND gateway_call.match_confidence = ?",
					filter.ReconcileStatus, filter.ReconcileMatchStrategy, filter.ReconcileMatchConfidence).
				Where("gateway_call.reconciled_at IS NOT NULL AND gateway_call.external_created_at IS NOT NULL AND gateway_call.external_model <> ''").
				Where("gateway_call.model_profile_id = assignment.template_id AND gateway_call.model_profile_revision = assignment.template_revision").
				Where("gateway_call.credential_revision = credential.credential_revision").
				Group("gateway_call.store_id"),
		},
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.AICustomerTagChangeCount = count
			},
			query: db.Table("t_customer_tag_change_log AS change_log").
				Select("change_log.store_id, COUNT(*) AS count").
				Where("change_log.tenant_id = ? AND change_log.store_id IN ? AND change_log.created_at >= ?", tenantID, storeIDs, start).
				Where("change_log.source = ?", filter.AITagSource).
				Group("change_log.store_id"),
		},
	}

	for _, item := range queries {
		rows := make([]tenantReleaseReadinessCountRow, 0)
		if err := item.query.Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			evidence, ok := ret[row.StoreID]
			if !ok {
				continue
			}
			item.apply(&evidence, row.Count)
			ret[row.StoreID] = evidence
		}
	}
	return ret, nil
}
