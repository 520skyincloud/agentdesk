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
	StoreID             int64
	StoreStaffBindingID int64
	UserID              int64
	AccountReady        int64
}

type TenantReleaseReadinessWxWorkProtocolState struct {
	StoreID             int64
	StoreStaffBindingID int64
	ActiveCount         int64
	ReadyChannelCount   int64
}

type TenantReleaseReadinessCredentialState struct {
	StoreID               int64
	StoreStaffBindingID   int64
	CredentialRevision    int64
	Status                enums.StoreCredentialStatus
	LastTestStatus        string
	LastTestedAt          *time.Time
	LastFastGPTSyncStatus string
	LastFastGPTSyncedAt   *time.Time
	HasActiveEncryptedKey int64
}

type TenantReleaseReadinessCredentialAuditState struct {
	ID                  int64
	StoreID             int64
	StoreStaffBindingID int64
	ToRevision          int64
	Action              enums.CredentialAuditAction
	Result              enums.CredentialAuditResult
	OperatorID          int64
	OperatorRole        string
	ApproverID          int64
}

type TenantReleaseReadinessFastGPTState struct {
	StoreID                    int64
	HasTenantTeam              int64
	Status                     string
	TargetProfileID            int64
	TargetProfileRevision      int64
	AppliedProfileID           int64
	AppliedProfileRevision     int64
	TargetStoreStaffBindingID  int64
	AppliedStoreStaffBindingID int64
	TargetCredentialRevision   int64
	AppliedCredentialRevision  int64
	ReadinessStatus            string
	LastSyncedAt               *time.Time
}

type TenantReleaseReadinessKnowledgeState struct {
	KnowledgeBaseID                   int64
	StoreID                           int64
	DatasetReady                      int64
	ConnectionID                      string
	FastGPTProfileReady               int64
	FastGPTAppliedProfileID           int64
	FastGPTAppliedProfileRevision     int64
	FastGPTAppliedStoreStaffBindingID int64
	FastGPTAppliedCredentialRevision  int64
	Status                            enums.Status
}

type TenantReleaseReadinessCursorSnapshot struct {
	MessageMaxID          int64
	MessageCount          int64
	OutboxMaxID           int64
	OutboxCount           int64
	UnsettledOutboxCount  int64
	AssignmentMaxID       int64
	AssignmentCount       int64
	ActiveAssignmentCount int64
}

type TenantReleaseReadinessEvidenceFilter struct {
	NewAPIGateway            string
	SuccessfulUsageStatuses  []string
	KnowledgeRetrieveStage   string
	KnowledgeProvider        string
	KnowledgeOperation       string
	KnowledgeStatus          string
	KnowledgeConnectionID    string
	KnowledgeLogSourceType   string
	KnowledgeChunkProvider   string
	KnowledgeChannel         string
	KnowledgeScene           string
	KnowledgeAnswerStatus    int
	AIHandoffContent         string
	ReconcileStatus          string
	ReconcileMatchStrategy   string
	ReconcileMatchConfidence string
	AITagSource              string
	WxWorkProtocolSource     string
	WxWorkProtocolTarget     string
}

type TenantReleaseReadinessEvidence struct {
	StoreID                     int64
	WxWorkProtocolInboundCount  int64
	WxWorkProtocolOutboundCount int64
	SuccessfulNewAPICallCount   int64
	FastGPTRetrievalCount       int64
	CustomerAIReplyCount        int64
	AIHandoffCount              int64
	RuleAssignmentCount         int64
	ReconciledBillingCount      int64
	AICustomerTagChangeCount    int64
}

type tenantReleaseReadinessCountRow struct {
	StoreID int64
	Count   int64
}

func (r *tenantReleaseReadinessRepository) FindCursorSnapshot(
	db *gorm.DB,
) (TenantReleaseReadinessCursorSnapshot, error) {
	ret := TenantReleaseReadinessCursorSnapshot{}
	if db == nil {
		return ret, nil
	}
	messageCursor := struct {
		MessageMaxID int64
		MessageCount int64
	}{}
	if err := db.Table("t_message").
		Select("COALESCE(MAX(id), 0) AS message_max_id, COUNT(*) AS message_count").
		Scan(&messageCursor).Error; err != nil {
		return TenantReleaseReadinessCursorSnapshot{}, err
	}
	ret.MessageMaxID = messageCursor.MessageMaxID
	ret.MessageCount = messageCursor.MessageCount
	outboxCursor := struct {
		OutboxMaxID int64
		OutboxCount int64
	}{}
	if err := db.Table("t_channel_message_outbox").
		Select("COALESCE(MAX(id), 0) AS outbox_max_id, COUNT(*) AS outbox_count").
		Scan(&outboxCursor).Error; err != nil {
		return TenantReleaseReadinessCursorSnapshot{}, err
	}
	ret.OutboxMaxID = outboxCursor.OutboxMaxID
	ret.OutboxCount = outboxCursor.OutboxCount
	if err := db.Table("t_channel_message_outbox").
		Where("send_status IN ?", []string{
			string(enums.ChannelMessageOutboxStatusPending),
			string(enums.ChannelMessageOutboxStatusSending),
			string(enums.ChannelMessageOutboxStatusFailed),
		}).
		Count(&ret.UnsettledOutboxCount).Error; err != nil {
		return TenantReleaseReadinessCursorSnapshot{}, err
	}
	assignmentCursor := struct {
		AssignmentMaxID int64
		AssignmentCount int64
	}{}
	if err := db.Table("t_conversation_assignment").
		Select("COALESCE(MAX(id), 0) AS assignment_max_id, COUNT(*) AS assignment_count").
		Scan(&assignmentCursor).Error; err != nil {
		return TenantReleaseReadinessCursorSnapshot{}, err
	}
	ret.AssignmentMaxID = assignmentCursor.AssignmentMaxID
	ret.AssignmentCount = assignmentCursor.AssignmentCount
	if err := db.Table("t_conversation_assignment").
		Where("status = ?", enums.IMAssignmentStatusActive).
		Count(&ret.ActiveAssignmentCount).Error; err != nil {
		return TenantReleaseReadinessCursorSnapshot{}, err
	}
	return ret, nil
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
			binding.id AS store_staff_binding_id,
			binding.user_id,
			CASE
				WHEN account.id IS NOT NULL
					AND account.tenant_id = binding.tenant_id
					AND account.status = ?
					AND account.approval_status = ?
					AND account.deleted_at IS NULL
					AND binding.active_user_id = binding.user_id
					AND binding.agent_team_id > 0
				THEN 1 ELSE 0
			END AS account_ready
		`,
			enums.StatusOk,
			enums.UserApprovalStatusApproved,
		).
		Joins("LEFT JOIN t_user AS account ON account.id = binding.user_id").
		Where("binding.tenant_id = ? AND binding.store_id IN ? AND binding.status = ?", tenantID, storeIDs, enums.StatusOk).
		Order("binding.store_id ASC, binding.id ASC").
		Scan(&ret).Error
	return ret, err
}

func (r *tenantReleaseReadinessRepository) FindWxWorkProtocolStates(
	db *gorm.DB,
	tenantID int64,
	storeIDs []int64,
) ([]TenantReleaseReadinessWxWorkProtocolState, error) {
	ret := make([]TenantReleaseReadinessWxWorkProtocolState, 0)
	if db == nil || tenantID <= 0 || len(storeIDs) == 0 {
		return ret, nil
	}
	err := db.Table("t_wx_work_protocol_instance AS instance").
		Select(`
			instance.store_id,
			instance.store_staff_binding_id,
			COUNT(instance.id) AS active_count,
			SUM(CASE
				WHEN instance.guid <> ''
					AND instance.channel_id > 0
					AND instance.store_staff_binding_id > 0
					AND binding.id IS NOT NULL
					AND binding.tenant_id = instance.tenant_id
					AND binding.store_id = instance.store_id
					AND binding.status = ?
					AND binding.active_user_id = binding.user_id
					AND binding.agent_team_id > 0
					AND instance.agent_team_id = binding.agent_team_id
					AND channel.id IS NOT NULL
					AND channel.tenant_id = instance.tenant_id
					AND channel.channel_type = ?
					AND channel.status = ?
					AND instance.health_status = 'online'
				THEN 1 ELSE 0
			END) AS ready_channel_count
		`,
			enums.StatusOk,
			enums.ChannelTypeWxWorkProtocol,
			enums.StatusOk,
		).
		Joins("LEFT JOIN t_store_staff_binding AS binding ON binding.id = instance.store_staff_binding_id").
		Joins("LEFT JOIN t_channel AS channel ON channel.id = instance.channel_id").
		Where(
			"instance.tenant_id = ? AND instance.store_id IN ? AND instance.status = ? AND "+wxWorkProtocolCurrentInstanceAliasedCondition,
			tenantID,
			storeIDs,
			enums.StatusOk,
		).
		Group("instance.store_id, instance.store_staff_binding_id").
		Order("instance.store_id ASC, instance.store_staff_binding_id ASC").
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
			store_staff_binding_id,
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

func (r *tenantReleaseReadinessRepository) FindCredentialApprovalAuditStates(
	db *gorm.DB,
	tenantID int64,
	storeIDs []int64,
) ([]TenantReleaseReadinessCredentialAuditState, error) {
	ret := make([]TenantReleaseReadinessCredentialAuditState, 0)
	if db == nil || tenantID <= 0 || len(storeIDs) == 0 {
		return ret, nil
	}
	err := db.Table("t_store_model_credential_audit_log").
		Select(`
			id,
			store_id,
			store_staff_binding_id,
			to_revision,
			action,
			result,
			operator_id,
			operator_role,
			approver_id
		`).
		Where(
			"tenant_id = ? AND store_id IN ? AND action IN ?",
			tenantID,
			storeIDs,
			[]enums.CredentialAuditAction{
				enums.CredentialAuditActionSubmit,
				enums.CredentialAuditActionApprove,
			},
		).
		Order("id ASC").
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
			target_store_staff_binding_id,
			applied_store_staff_binding_id,
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
			fast_gpt_applied_store_staff_binding_id,
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
				item.WxWorkProtocolInboundCount = count
			},
			query: db.Table("t_message_sync_log AS sync_log").
				Select("route.store_id, COUNT(DISTINCT sync_log.id) AS count").
				Joins("JOIN t_message AS customer_message ON customer_message.tenant_id = sync_log.tenant_id AND customer_message.id = sync_log.message_id AND customer_message.conversation_id = sync_log.conversation_id").
				Joins("JOIN t_wx_work_kf_message_ref AS message_ref ON message_ref.tenant_id = sync_log.tenant_id AND message_ref.message_id = sync_log.message_id AND message_ref.conversation_id = sync_log.conversation_id").
				Joins("JOIN t_conversation_route_state AS route ON route.tenant_id = sync_log.tenant_id AND route.conversation_id = sync_log.conversation_id").
				Joins("JOIN t_wx_work_protocol_instance AS instance ON instance.tenant_id = route.tenant_id AND instance.id = route.wx_work_instance_id AND instance.store_id = route.store_id AND instance.store_staff_binding_id = route.store_staff_binding_id").
				Where("sync_log.tenant_id = ? AND route.store_id IN ? AND sync_log.created_at >= ?", tenantID, storeIDs, start).
				Where(
					"sync_log.direction = ? AND sync_log.source = ? AND sync_log.target = ? AND sync_log.sync_status = ? AND sync_log.external_msg_id <> ''",
					enums.MessageSyncDirectionWecomToAgentDesk,
					filter.WxWorkProtocolSource,
					filter.WxWorkProtocolTarget,
					enums.MessageSyncStatusSuccess,
				).
				Where("customer_message.sender_type = ? AND customer_message.created_at >= ?", enums.IMSenderTypeCustomer, start).
				Where(
					"message_ref.direction = ? AND message_ref.send_status = ? AND message_ref.open_kf_id LIKE ? AND message_ref.status = ?",
					enums.WxWorkKFMessageDirectionIn,
					enums.WxWorkKFMessageSendStatusReceived,
					"wx_protocol:%",
					enums.StatusOk,
				).
				Where("instance.status = ? AND "+wxWorkProtocolCurrentInstanceAliasedCondition, enums.StatusOk).
				Group("route.store_id"),
		},
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.WxWorkProtocolOutboundCount = count
			},
			query: db.Table("t_channel_message_outbox AS outbox").
				Select("route.store_id, COUNT(DISTINCT outbox.id) AS count").
				Joins("JOIN t_message AS ai_message ON ai_message.tenant_id = outbox.tenant_id AND ai_message.id = outbox.message_id AND ai_message.conversation_id = outbox.conversation_id").
				Joins("JOIN t_conversation_route_state AS route ON route.tenant_id = outbox.tenant_id AND route.conversation_id = outbox.conversation_id").
				Joins("JOIN t_wx_work_protocol_instance AS instance ON instance.tenant_id = route.tenant_id AND instance.id = route.wx_work_instance_id AND instance.store_id = route.store_id AND instance.store_staff_binding_id = route.store_staff_binding_id").
				Joins("JOIN t_wx_work_kf_message_ref AS message_ref ON message_ref.tenant_id = outbox.tenant_id AND message_ref.message_id = outbox.message_id AND message_ref.conversation_id = outbox.conversation_id").
				Where("outbox.tenant_id = ? AND route.store_id IN ? AND outbox.created_at >= ?", tenantID, storeIDs, start).
				Where("outbox.channel_type = ? AND outbox.send_status = ? AND outbox.sent_at IS NOT NULL",
					enums.ChannelTypeWxWorkProtocol, enums.ChannelMessageOutboxStatusSent).
				Where("ai_message.sender_type = ? AND ai_message.created_at >= ?", enums.IMSenderTypeAI, start).
				Where("instance.status = ? AND "+wxWorkProtocolCurrentInstanceAliasedCondition, enums.StatusOk).
				Where(
					"message_ref.direction = ? AND message_ref.send_status = ? AND message_ref.open_kf_id LIKE ? AND message_ref.status = ?",
					enums.WxWorkKFMessageDirectionOut,
					enums.WxWorkKFMessageSendStatusSent,
					"wx_protocol:%",
					enums.StatusOk,
				).
				Where(`EXISTS (
					SELECT 1
					FROM t_message_sync_log AS inbound_log
					JOIN t_message AS inbound_message
						ON inbound_message.tenant_id = inbound_log.tenant_id
						AND inbound_message.id = inbound_log.message_id
						AND inbound_message.conversation_id = inbound_log.conversation_id
					WHERE inbound_log.tenant_id = outbox.tenant_id
						AND inbound_log.conversation_id = outbox.conversation_id
						AND inbound_log.direction = ?
						AND inbound_log.source = ?
						AND inbound_log.target = ?
						AND inbound_log.sync_status = ?
						AND inbound_log.external_msg_id <> ''
						AND inbound_log.created_at >= ?
						AND inbound_message.created_at <= ai_message.created_at
						AND inbound_message.sender_type = ?
				)`,
					enums.MessageSyncDirectionWecomToAgentDesk,
					filter.WxWorkProtocolSource,
					filter.WxWorkProtocolTarget,
					enums.MessageSyncStatusSuccess,
					start,
					enums.IMSenderTypeCustomer,
				).
				Group("route.store_id"),
		},
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.SuccessfulNewAPICallCount = count
			},
			query: db.Table("t_ai_usage_event AS usage_event").
				Select("usage_event.store_id, COUNT(*) AS count").
				Joins("JOIN t_store_model_profile_assignment AS assignment ON assignment.tenant_id = usage_event.tenant_id AND assignment.store_id = usage_event.store_id").
				Joins("JOIN t_store_model_credential AS credential ON credential.tenant_id = usage_event.tenant_id AND credential.store_id = usage_event.store_id AND credential.store_staff_binding_id = usage_event.store_staff_binding_id").
				Joins("JOIN t_wx_work_protocol_instance AS instance ON instance.tenant_id = usage_event.tenant_id AND instance.id = usage_event.wx_work_instance_id AND instance.store_id = usage_event.store_id AND instance.store_staff_binding_id = usage_event.store_staff_binding_id").
				Where("usage_event.tenant_id = ? AND usage_event.store_id IN ? AND usage_event.created_at >= ?", tenantID, storeIDs, start).
				Where("usage_event.gateway = ? AND usage_event.gateway_request_id <> '' AND usage_event.status IN ?", filter.NewAPIGateway, filter.SuccessfulUsageStatuses).
				Where("usage_event.model_profile_id = assignment.template_id AND usage_event.model_profile_revision = assignment.template_revision").
				Where("usage_event.credential_revision = credential.credential_revision").
				Where("usage_event.conversation_id > 0 AND instance.status = ? AND "+wxWorkProtocolCurrentInstanceAliasedCondition, enums.StatusOk).
				Where(`EXISTS (
					SELECT 1
					FROM t_message_sync_log AS inbound_log
					JOIN t_message AS inbound_message
						ON inbound_message.tenant_id = inbound_log.tenant_id
						AND inbound_message.id = inbound_log.message_id
						AND inbound_message.conversation_id = inbound_log.conversation_id
					WHERE inbound_log.tenant_id = usage_event.tenant_id
						AND inbound_log.conversation_id = usage_event.conversation_id
						AND inbound_log.direction = ?
						AND inbound_log.source = ?
						AND inbound_log.target = ?
						AND inbound_log.sync_status = ?
						AND inbound_log.external_msg_id <> ''
						AND inbound_log.created_at >= ?
						AND inbound_message.created_at <= usage_event.created_at
						AND inbound_message.sender_type = ?
				)`,
					enums.MessageSyncDirectionWecomToAgentDesk,
					filter.WxWorkProtocolSource,
					filter.WxWorkProtocolTarget,
					enums.MessageSyncStatusSuccess,
					start,
					enums.IMSenderTypeCustomer,
				).
				Group("usage_event.store_id"),
		},
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.FastGPTRetrievalCount = count
			},
			query: db.Table("t_ai_usage_event AS usage_event").
				Select("usage_event.store_id, COUNT(*) AS count").
				Joins("JOIN t_store AS store ON store.tenant_id = usage_event.tenant_id AND store.id = usage_event.store_id").
				Joins("JOIN t_knowledge_base AS knowledge ON knowledge.tenant_id = usage_event.tenant_id AND knowledge.id = usage_event.knowledge_base_id AND knowledge.store_id = usage_event.store_id").
				Joins("JOIN t_store_model_profile_assignment AS assignment ON assignment.tenant_id = usage_event.tenant_id AND assignment.store_id = usage_event.store_id").
				Joins("JOIN t_store_model_credential AS credential ON credential.tenant_id = usage_event.tenant_id AND credential.store_id = usage_event.store_id AND credential.store_staff_binding_id = usage_event.store_staff_binding_id").
				Joins("JOIN t_wx_work_protocol_instance AS instance ON instance.tenant_id = usage_event.tenant_id AND instance.id = usage_event.wx_work_instance_id AND instance.store_id = usage_event.store_id AND instance.store_staff_binding_id = usage_event.store_staff_binding_id").
				Where("usage_event.tenant_id = ? AND usage_event.store_id IN ? AND usage_event.created_at >= ?", tenantID, storeIDs, start).
				Where("usage_event.conversation_id > 0 AND usage_event.request_id <> '' AND usage_event.request_count > 0").
				Where("usage_event.stage = ? AND usage_event.provider = ? AND usage_event.operation_type = ? AND usage_event.status = ?",
					filter.KnowledgeRetrieveStage, filter.KnowledgeProvider, filter.KnowledgeOperation, filter.KnowledgeStatus).
				Where("store.status = ? AND store.knowledge_base_id = knowledge.id", enums.StatusOk).
				Where("knowledge.status = ? AND knowledge.connection_id = ?", enums.StatusOk, filter.KnowledgeConnectionID).
				Where("usage_event.model_profile_id = assignment.template_id AND usage_event.model_profile_revision = assignment.template_revision").
				Where("usage_event.credential_revision = credential.credential_revision").
				Where("instance.status = ? AND "+wxWorkProtocolCurrentInstanceAliasedCondition, enums.StatusOk).
				Where(`EXISTS (
						SELECT 1
						FROM t_message_sync_log AS inbound_log
						JOIN t_message AS customer_message
							ON customer_message.tenant_id = inbound_log.tenant_id
							AND customer_message.id = inbound_log.message_id
							AND customer_message.conversation_id = inbound_log.conversation_id
						WHERE inbound_log.tenant_id = usage_event.tenant_id
							AND inbound_log.conversation_id = usage_event.conversation_id
							AND inbound_log.direction = ?
							AND inbound_log.source = ?
							AND inbound_log.target = ?
							AND inbound_log.sync_status = ?
							AND inbound_log.external_msg_id <> ''
							AND inbound_log.created_at >= ?
							AND customer_message.created_at <= usage_event.created_at
							AND customer_message.sender_type = ?
					)`,
					enums.MessageSyncDirectionWecomToAgentDesk,
					filter.WxWorkProtocolSource,
					filter.WxWorkProtocolTarget,
					enums.MessageSyncStatusSuccess,
					start,
					enums.IMSenderTypeCustomer,
				).
				Where(`EXISTS (
						SELECT 1
						FROM t_message AS ai_message
						JOIN t_channel_message_outbox AS outbox
							ON outbox.tenant_id = ai_message.tenant_id
							AND outbox.message_id = ai_message.id
							AND outbox.conversation_id = ai_message.conversation_id
						JOIN t_wx_work_kf_message_ref AS message_ref
							ON message_ref.tenant_id = ai_message.tenant_id
							AND message_ref.message_id = ai_message.id
							AND message_ref.conversation_id = ai_message.conversation_id
						WHERE ai_message.tenant_id = usage_event.tenant_id
							AND ai_message.conversation_id = usage_event.conversation_id
							AND ai_message.sender_type = ?
							AND ai_message.send_status IN ?
							AND ai_message.created_at >= usage_event.created_at
							AND outbox.channel_type = ?
							AND outbox.send_status = ?
							AND outbox.sent_at IS NOT NULL
							AND message_ref.direction = ?
							AND message_ref.send_status = ?
							AND message_ref.open_kf_id LIKE ?
							AND message_ref.status = ?
					)`,
					enums.IMSenderTypeAI,
					[]enums.IMMessageStatus{
						enums.IMMessageStatusSent, enums.IMMessageStatusDelivered, enums.IMMessageStatusRead,
					},
					enums.ChannelTypeWxWorkProtocol,
					enums.ChannelMessageOutboxStatusSent,
					enums.WxWorkKFMessageDirectionOut,
					enums.WxWorkKFMessageSendStatusSent,
					"wx_protocol:%",
					enums.StatusOk,
				).
				Where(`EXISTS (
					SELECT 1 FROM t_knowledge_retrieve_log AS retrieve_log
					WHERE retrieve_log.tenant_id = usage_event.tenant_id
						AND retrieve_log.knowledge_base_id = usage_event.knowledge_base_id
						AND retrieve_log.conversation_id = usage_event.conversation_id
						AND retrieve_log.request_id = usage_event.request_id
						AND retrieve_log.created_at >= ?
						AND retrieve_log.source_type = ?
						AND retrieve_log.chunk_provider = ?
						AND retrieve_log.channel = ?
						AND retrieve_log.scene = ?
						AND retrieve_log.answer_status = ?
						AND retrieve_log.hit_count > 0
						AND retrieve_log.used_chunk_count > 0
				)`, start, filter.KnowledgeLogSourceType, filter.KnowledgeChunkProvider,
					filter.KnowledgeChannel, filter.KnowledgeScene, filter.KnowledgeAnswerStatus).
				Group("usage_event.store_id"),
		},
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.CustomerAIReplyCount = count
			},
			query: db.Table("t_message AS message").
				Select("route.store_id, COUNT(*) AS count").
				Joins("JOIN t_conversation_route_state AS route ON route.tenant_id = message.tenant_id AND route.conversation_id = message.conversation_id").
				Joins("JOIN t_wx_work_protocol_instance AS instance ON instance.tenant_id = route.tenant_id AND instance.id = route.wx_work_instance_id AND instance.store_id = route.store_id AND instance.store_staff_binding_id = route.store_staff_binding_id").
				Where("message.tenant_id = ? AND route.store_id IN ? AND message.created_at >= ?", tenantID, storeIDs, start).
				Where("message.sender_type = ? AND message.send_status IN ?", enums.IMSenderTypeAI, []enums.IMMessageStatus{
					enums.IMMessageStatusSent, enums.IMMessageStatusDelivered, enums.IMMessageStatusRead,
				}).
				Where("instance.status = ? AND "+wxWorkProtocolCurrentInstanceAliasedCondition, enums.StatusOk).
				Where(`EXISTS (
						SELECT 1
						FROM t_message_sync_log AS inbound_log
						JOIN t_message AS customer_message
							ON customer_message.tenant_id = inbound_log.tenant_id
							AND customer_message.id = inbound_log.message_id
							AND customer_message.conversation_id = inbound_log.conversation_id
						WHERE inbound_log.tenant_id = message.tenant_id
							AND inbound_log.conversation_id = message.conversation_id
							AND inbound_log.direction = ?
							AND inbound_log.source = ?
							AND inbound_log.target = ?
							AND inbound_log.sync_status = ?
							AND inbound_log.external_msg_id <> ''
							AND inbound_log.created_at >= ?
							AND customer_message.created_at <= message.created_at
							AND customer_message.sender_type = ?
					)`,
					enums.MessageSyncDirectionWecomToAgentDesk,
					filter.WxWorkProtocolSource,
					filter.WxWorkProtocolTarget,
					enums.MessageSyncStatusSuccess,
					start,
					enums.IMSenderTypeCustomer,
				).
				Where(`EXISTS (
						SELECT 1
						FROM t_channel_message_outbox AS outbox
						JOIN t_wx_work_kf_message_ref AS message_ref
							ON message_ref.tenant_id = outbox.tenant_id
							AND message_ref.message_id = outbox.message_id
							AND message_ref.conversation_id = outbox.conversation_id
						WHERE outbox.tenant_id = message.tenant_id
							AND outbox.conversation_id = message.conversation_id
							AND outbox.message_id = message.id
							AND outbox.channel_type = ?
							AND outbox.send_status = ?
							AND outbox.sent_at IS NOT NULL
							AND message_ref.direction = ?
							AND message_ref.send_status = ?
							AND message_ref.open_kf_id LIKE ?
							AND message_ref.status = ?
					)`,
					enums.ChannelTypeWxWorkProtocol,
					enums.ChannelMessageOutboxStatusSent,
					enums.WxWorkKFMessageDirectionOut,
					enums.WxWorkKFMessageSendStatusSent,
					"wx_protocol:%",
					enums.StatusOk,
				).
				Group("route.store_id"),
		},
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.AIHandoffCount = count
			},
			query: db.Table("t_conversation_event_log AS event").
				Select("route.store_id, COUNT(*) AS count").
				Joins("JOIN t_conversation_route_state AS route ON route.tenant_id = event.tenant_id AND route.conversation_id = event.conversation_id").
				Joins("JOIN t_wx_work_protocol_instance AS instance ON instance.tenant_id = route.tenant_id AND instance.id = route.wx_work_instance_id AND instance.store_id = route.store_id AND instance.store_staff_binding_id = route.store_staff_binding_id").
				Where("event.tenant_id = ? AND route.store_id IN ? AND event.created_at >= ?", tenantID, storeIDs, start).
				Where("event.event_type = ? AND event.operator_type = ? AND event.content = ?", enums.IMEventTypeTransfer, enums.IMSenderTypeAI, filter.AIHandoffContent).
				Where("instance.status = ? AND "+wxWorkProtocolCurrentInstanceAliasedCondition, enums.StatusOk).
				Where(`EXISTS (
						SELECT 1
						FROM t_message_sync_log AS inbound_log
						JOIN t_message AS customer_message
							ON customer_message.tenant_id = inbound_log.tenant_id
							AND customer_message.id = inbound_log.message_id
							AND customer_message.conversation_id = inbound_log.conversation_id
						WHERE inbound_log.tenant_id = event.tenant_id
							AND inbound_log.conversation_id = event.conversation_id
							AND inbound_log.direction = ?
							AND inbound_log.source = ?
							AND inbound_log.target = ?
							AND inbound_log.sync_status = ?
							AND inbound_log.external_msg_id <> ''
							AND inbound_log.created_at >= ?
							AND customer_message.created_at <= event.created_at
							AND customer_message.sender_type = ?
					)`,
					enums.MessageSyncDirectionWecomToAgentDesk,
					filter.WxWorkProtocolSource,
					filter.WxWorkProtocolTarget,
					enums.MessageSyncStatusSuccess,
					start,
					enums.IMSenderTypeCustomer,
				).
				Group("route.store_id"),
		},
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.RuleAssignmentCount = count
			},
			query: db.Table("t_conversation_assignment AS assignment").
				Select("route.store_id, COUNT(*) AS count").
				Joins("JOIN t_conversation_route_state AS route ON route.tenant_id = assignment.tenant_id AND route.conversation_id = assignment.conversation_id").
				Joins("JOIN t_wx_work_protocol_instance AS instance ON instance.tenant_id = route.tenant_id AND instance.id = route.wx_work_instance_id AND instance.store_id = route.store_id AND instance.store_staff_binding_id = route.store_staff_binding_id").
				Where("assignment.tenant_id = ? AND route.store_id IN ? AND assignment.created_at >= ?", tenantID, storeIDs, start).
				Where("assignment.dispatch_mode = ? AND assignment.to_user_id > 0", enums.AgentTeamDispatchModeRule).
				Where("instance.status = ? AND "+wxWorkProtocolCurrentInstanceAliasedCondition, enums.StatusOk).
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
				Where(`EXISTS (
						SELECT 1
						FROM t_message_sync_log AS inbound_log
						JOIN t_message AS customer_message
							ON customer_message.tenant_id = inbound_log.tenant_id
							AND customer_message.id = inbound_log.message_id
							AND customer_message.conversation_id = inbound_log.conversation_id
						WHERE inbound_log.tenant_id = assignment.tenant_id
							AND inbound_log.conversation_id = assignment.conversation_id
							AND inbound_log.direction = ?
							AND inbound_log.source = ?
							AND inbound_log.target = ?
							AND inbound_log.sync_status = ?
							AND inbound_log.external_msg_id <> ''
							AND inbound_log.created_at >= ?
							AND customer_message.created_at <= assignment.created_at
							AND customer_message.sender_type = ?
					)`,
					enums.MessageSyncDirectionWecomToAgentDesk,
					filter.WxWorkProtocolSource,
					filter.WxWorkProtocolTarget,
					enums.MessageSyncStatusSuccess,
					start,
					enums.IMSenderTypeCustomer,
				).
				Group("route.store_id"),
		},
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.ReconciledBillingCount = count
			},
			query: db.Table("t_ai_usage_gateway_call AS gateway_call").
				Select("gateway_call.store_id, COUNT(*) AS count").
				Joins("JOIN t_store_model_profile_assignment AS assignment ON assignment.tenant_id = gateway_call.tenant_id AND assignment.store_id = gateway_call.store_id").
				Joins("JOIN t_store_model_credential AS credential ON credential.tenant_id = gateway_call.tenant_id AND credential.store_id = gateway_call.store_id AND credential.store_staff_binding_id = gateway_call.store_staff_binding_id").
				Joins("JOIN t_wx_work_protocol_instance AS instance ON instance.tenant_id = gateway_call.tenant_id AND instance.id = gateway_call.wx_work_instance_id AND instance.store_id = gateway_call.store_id AND instance.store_staff_binding_id = gateway_call.store_staff_binding_id").
				Where("gateway_call.tenant_id = ? AND gateway_call.store_id IN ? AND gateway_call.created_at >= ?", tenantID, storeIDs, start).
				Where("gateway_call.gateway = ? AND gateway_call.gateway_request_id <> ''", filter.NewAPIGateway).
				Where("gateway_call.reconcile_status = ? AND gateway_call.match_strategy = ? AND gateway_call.match_confidence = ?",
					filter.ReconcileStatus, filter.ReconcileMatchStrategy, filter.ReconcileMatchConfidence).
				Where("gateway_call.reconciled_at IS NOT NULL AND gateway_call.external_created_at IS NOT NULL AND gateway_call.external_model <> ''").
				Where("gateway_call.model_profile_id = assignment.template_id AND gateway_call.model_profile_revision = assignment.template_revision").
				Where("gateway_call.credential_revision = credential.credential_revision").
				Where("gateway_call.conversation_id > 0 AND instance.status = ? AND "+wxWorkProtocolCurrentInstanceAliasedCondition, enums.StatusOk).
				Where(`EXISTS (
						SELECT 1
						FROM t_message_sync_log AS inbound_log
						JOIN t_message AS customer_message
							ON customer_message.tenant_id = inbound_log.tenant_id
							AND customer_message.id = inbound_log.message_id
							AND customer_message.conversation_id = inbound_log.conversation_id
						WHERE inbound_log.tenant_id = gateway_call.tenant_id
							AND inbound_log.conversation_id = gateway_call.conversation_id
							AND inbound_log.direction = ?
							AND inbound_log.source = ?
							AND inbound_log.target = ?
							AND inbound_log.sync_status = ?
							AND inbound_log.external_msg_id <> ''
							AND inbound_log.created_at >= ?
							AND customer_message.created_at <= gateway_call.created_at
							AND customer_message.sender_type = ?
					)`,
					enums.MessageSyncDirectionWecomToAgentDesk,
					filter.WxWorkProtocolSource,
					filter.WxWorkProtocolTarget,
					enums.MessageSyncStatusSuccess,
					start,
					enums.IMSenderTypeCustomer,
				).
				Group("gateway_call.store_id"),
		},
		{
			apply: func(item *TenantReleaseReadinessEvidence, count int64) {
				item.AICustomerTagChangeCount = count
			},
			query: db.Table("t_customer_tag_change_log AS change_log").
				Select("change_log.store_id, COUNT(*) AS count").
				Joins("JOIN t_conversation_route_state AS route ON route.tenant_id = change_log.tenant_id AND route.conversation_id = change_log.conversation_id AND route.store_id = change_log.store_id").
				Joins("JOIN t_wx_work_protocol_instance AS instance ON instance.tenant_id = route.tenant_id AND instance.id = route.wx_work_instance_id AND instance.store_id = route.store_id AND instance.store_staff_binding_id = route.store_staff_binding_id").
				Where("change_log.tenant_id = ? AND change_log.store_id IN ? AND change_log.created_at >= ?", tenantID, storeIDs, start).
				Where("change_log.source = ?", filter.AITagSource).
				Where("change_log.conversation_id > 0 AND instance.status = ? AND "+wxWorkProtocolCurrentInstanceAliasedCondition, enums.StatusOk).
				Where(`EXISTS (
						SELECT 1
						FROM t_message_sync_log AS inbound_log
						JOIN t_message AS customer_message
							ON customer_message.tenant_id = inbound_log.tenant_id
							AND customer_message.id = inbound_log.message_id
							AND customer_message.conversation_id = inbound_log.conversation_id
						WHERE inbound_log.tenant_id = change_log.tenant_id
							AND inbound_log.conversation_id = change_log.conversation_id
							AND inbound_log.direction = ?
							AND inbound_log.source = ?
							AND inbound_log.target = ?
							AND inbound_log.sync_status = ?
							AND inbound_log.external_msg_id <> ''
							AND inbound_log.created_at >= ?
							AND customer_message.created_at <= change_log.created_at
							AND customer_message.sender_type = ?
					)`,
					enums.MessageSyncDirectionWecomToAgentDesk,
					filter.WxWorkProtocolSource,
					filter.WxWorkProtocolTarget,
					enums.MessageSyncStatusSuccess,
					start,
					enums.IMSenderTypeCustomer,
				).
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
