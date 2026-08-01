package services

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	conversationEvolutionDefaultQuietMinutes = 24 * 60
	conversationEvolutionMaxQuietMinutes     = 30 * 24 * 60
	conversationEvolutionLeaseDuration       = 30 * time.Minute
	conversationEvolutionMaxFailures         = 5

	conversationEvolutionStatusWaiting    = "waiting"
	conversationEvolutionStatusProcessing = "processing"
	conversationEvolutionStatusCompleted  = "completed"
	conversationEvolutionStatusFailed     = "failed"
	conversationEvolutionStatusBlocked    = "blocked"
	conversationEvolutionStatusSuperseded = "superseded"
)

var ConversationEvolutionService = &conversationEvolutionService{}

type conversationEvolutionService struct{}

type conversationEvolutionPolicy struct {
	TenantID            int64
	StoreID             int64
	IntentProfileID     int64
	QuietPeriod         time.Duration
	MinimumConfidence   float64
	MaxOperationsPerRun int
}

type evolutionFinalizeResult struct {
	changed      bool
	superseded   bool
	conversation *models.Conversation
	storeID      int64
	relationID   int64
}

// ObserveCommittedMessage advances only the inactivity cursor. It runs after
// the message transaction and never resolves or calls a model.
func (s *conversationEvolutionService) ObserveCommittedMessage(conversation *models.Conversation, message *models.Message) {
	if conversation == nil || message == nil || conversation.ID <= 0 || conversation.TenantID <= 0 ||
		conversation.CustomerID <= 0 || message.ID <= 0 || message.ConversationID != conversation.ID {
		return
	}
	if message.TenantID > 0 && message.TenantID != conversation.TenantID {
		return
	}
	if message.SendStatus == enums.IMMessageStatusFailed || message.SendStatus == enums.IMMessageStatusRecalled {
		return
	}
	scope, err := CustomerTagService.resolveConversationScope(sqls.DB(), conversation.ID, true)
	if err != nil || scope == nil || scope.Relation == nil {
		slog.Warn("observe customer tag evolution skipped", "conversation_id", conversation.ID, "message_id", message.ID, "class", "scope_unavailable")
		return
	}
	sessionNo := message.SessionNo
	if sessionNo <= 0 {
		sessionNo = 1
	}
	messageAt := evolutionMessageTime(message)
	quietPeriod := s.quietPeriodForTenant(scope.TenantID)
	now := time.Now()
	nextEvolutionAt := messageAt.Add(quietPeriod)
	item := &models.ConversationEvolutionState{
		TenantID: scope.TenantID, ConversationID: conversation.ID, SessionNo: sessionNo,
		StoreID: scope.StoreID, CustomerID: conversation.CustomerID, StoreCustomerRelationID: scope.Relation.ID,
		LastObservedMessageID: message.ID, NextEvolutionAt: &nextEvolutionAt,
		LastStatus: conversationEvolutionStatusWaiting, Status: enums.StatusOk,
		AuditFields: models.AuditFields{
			CreatedAt: now, UpdatedAt: now,
			CreateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName,
			UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
		},
	}
	if err := repositories.ConversationEvolutionStateRepository.Observe(sqls.DB(), item); err != nil {
		slog.Warn("observe customer tag evolution failed", "conversation_id", conversation.ID, "message_id", message.ID, "class", "state_write_failed")
	}
}

func (s *conversationEvolutionService) ProcessDue(limit int) int {
	now := time.Now()
	states, err := repositories.ConversationEvolutionStateRepository.FindDue(sqls.DB(), now, limit)
	if err != nil {
		slog.Warn("find due customer tag evolution states failed", "class", "state_query_failed")
		return 0
	}
	processed := 0
	for i := range states {
		claimAt := time.Now()
		owner := "customer-tag-evolution:" + uuid.NewString()
		claimed, claimErr := repositories.ConversationEvolutionStateRepository.Claim(
			sqls.DB(), states[i].ID, states[i].TenantID, owner, claimAt, claimAt.Add(conversationEvolutionLeaseDuration),
		)
		if claimErr != nil {
			slog.Warn("claim customer tag evolution state failed", "state_id", states[i].ID, "class", "lease_claim_failed")
			continue
		}
		if !claimed {
			continue
		}
		states[i].LeaseOwner = owner
		leaseExpiresAt := claimAt.Add(conversationEvolutionLeaseDuration)
		states[i].LeaseExpiresAt = &leaseExpiresAt
		states[i].LastStatus = conversationEvolutionStatusProcessing
		s.processClaimedState(&states[i], owner)
		processed++
	}
	return processed
}

func (s *conversationEvolutionService) processClaimedState(state *models.ConversationEvolutionState, owner string) {
	policy, policyClass, err := s.loadPolicy(sqls.DB(), state, true)
	if err != nil {
		s.failClaim(state, owner, nil, "policy_query_failed", true, false)
		return
	}
	if policy == nil {
		if policyClass == "evolution_disabled" {
			s.releaseDisabledClaim(state, owner)
			return
		}
		s.failClaim(state, owner, nil, policyClass, true, false)
		return
	}

	scope, scopeErr := CustomerTagService.resolveConversationScope(sqls.DB(), state.ConversationID, false)
	if scopeErr != nil || !evolutionScopeMatchesState(scope, state) || scope.ProfileID != policy.IntentProfileID {
		s.failClaim(state, owner, nil, "conversation_scope_unavailable", true, false)
		return
	}
	latest, err := repositories.ConversationEvolutionStateRepository.FindLatestCommittedMessage(
		sqls.DB(), state.TenantID, state.ConversationID, state.SessionNo,
	)
	if err != nil {
		s.failClaim(state, owner, nil, "latest_message_query_failed", false, false)
		return
	}
	if latest == nil || latest.ID <= state.LastEvolvedMessageID {
		s.completeClaimWithoutRun(state, owner)
		return
	}
	if latest.ID != state.LastObservedMessageID {
		s.rescheduleClaim(state, owner, latest, policy.QuietPeriod)
		return
	}
	if next := evolutionMessageTime(latest).Add(policy.QuietPeriod); next.After(time.Now()) {
		s.rescheduleClaimAt(state, owner, latest.ID, next)
		return
	}

	run, completed, err := s.beginRun(state, policy, latest.ID)
	if err != nil || run == nil {
		s.failClaim(state, owner, run, "run_begin_failed", false, false)
		return
	}
	if completed {
		s.recoverCompletedRun(state, owner, run, policy)
		return
	}

	messages, err := repositories.ConversationEvolutionStateRepository.FindCommittedMessages(
		sqls.DB(), state.TenantID, state.ConversationID, state.SessionNo, state.LastEvolvedMessageID, latest.ID,
	)
	if err != nil {
		s.failClaim(state, owner, run, "incremental_message_query_failed", false, false)
		return
	}
	if len(messages) == 0 {
		s.completeRun(state, owner, run, policy, "skipped", "skipped", "skipped", false, nil)
		return
	}

	summaryStatus, summaryAdvanced := s.updateSessionSummary(state, latest.ID)
	knowledgeStatus := s.evolveKnowledge(state.ConversationID)
	if err := repositories.ConversationEvolutionRunRepository.UpdatesInTenant(sqls.DB(), run.ID, run.TenantID, map[string]any{
		"summary_status": summaryStatus, "knowledge_status": knowledgeStatus, "updated_at": time.Now(),
	}); err != nil {
		s.failClaim(state, owner, run, "run_branch_update_failed", false, false)
		return
	}
	if !s.renewClaim(state, owner) {
		return
	}

	allowedTags, err := CustomerTagService.listAllowedAITags(sqls.DB(), scope)
	if err != nil {
		s.failClaim(state, owner, run, "allowed_tag_query_failed", false, false)
		return
	}
	if !hasPotentialCustomerEvolutionMessage(messages) || len(allowedTags) == 0 {
		s.completeRun(state, owner, run, policy, summaryStatus, knowledgeStatus, "skipped", summaryAdvanced, nil)
		return
	}

	resolved, err := ModelCallResolverService.ResolveForConversation(state.ConversationID, enums.ModelUsageSlotCustomerTag)
	if err != nil || resolved == nil {
		s.failClaim(state, owner, run, "store_credential_or_tag_slot_unavailable", true, false)
		return
	}
	if resolved.SchemaVersion != "customer_tag_evolution.v1" {
		s.failClaim(state, owner, run, "customer_tag_schema_unsupported", true, false)
		return
	}
	run.ModelProfileID = resolved.ProfileID
	run.ModelProfileRevision = resolved.ProfileRevision
	run.CredentialRevision = resolved.CredentialRevision
	inputs, inputHash, err := s.buildTagInputs(run, state, policy, resolved, scope, allowedTags, messages)
	if err != nil {
		s.failClaim(state, owner, run, "customer_tag_input_failed", false, false)
		return
	}
	run.InputHash = inputHash
	run.ChunkCount = len(inputs)
	if err := repositories.ConversationEvolutionRunRepository.UpdatesInTenant(sqls.DB(), run.ID, run.TenantID, map[string]any{
		"intent_profile_id": policy.IntentProfileID,
		"model_profile_id":  resolved.ProfileID, "model_profile_revision": resolved.ProfileRevision,
		"credential_revision": resolved.CredentialRevision, "input_hash": inputHash,
		"chunk_count": len(inputs), "summary_status": summaryStatus, "knowledge_status": knowledgeStatus,
		"updated_at": time.Now(),
	}); err != nil {
		s.failClaim(state, owner, run, "run_attribution_update_failed", false, false)
		return
	}
	if len(inputs) == 0 {
		s.completeRun(state, owner, run, policy, summaryStatus, knowledgeStatus, "skipped", summaryAdvanced, nil)
		return
	}

	operations := make([]CustomerTagOperation, 0)
	for chunkIndex := range inputs {
		if !s.renewClaim(state, owner) {
			return
		}
		chunkOperations, callErr := s.callTagModel(run, resolved, policy, chunkIndex+1, inputs[chunkIndex])
		if callErr != nil {
			s.failClaim(state, owner, run, "customer_tag_model_failed", false, false)
			return
		}
		operations = append(operations, chunkOperations...)
	}
	operations = mergeCustomerTagOperations(operations)
	if len(operations) > policy.MaxOperationsPerRun {
		operations = operations[:policy.MaxOperationsPerRun]
	}
	s.completeRun(state, owner, run, policy, summaryStatus, knowledgeStatus, "completed", summaryAdvanced, operations)
}

func (s *conversationEvolutionService) beginRun(
	state *models.ConversationEvolutionState,
	policy *conversationEvolutionPolicy,
	checkpoint int64,
) (*models.ConversationEvolutionRun, bool, error) {
	existing, err := repositories.ConversationEvolutionRunRepository.GetByCheckpoint(
		sqls.DB(), state.TenantID, state.ConversationID, state.SessionNo, checkpoint,
	)
	if err != nil {
		return nil, false, err
	}
	if existing != nil && existing.RunStatus == conversationEvolutionStatusCompleted {
		return existing, true, nil
	}
	now := time.Now()
	if existing == nil {
		run := &models.ConversationEvolutionRun{
			RunKey:   fmt.Sprintf("customer-tag-evolution:%d:%d:%d:%d", state.TenantID, state.ConversationID, state.SessionNo, checkpoint),
			TenantID: state.TenantID, ConversationID: state.ConversationID, SessionNo: state.SessionNo,
			EndMessageID: checkpoint, StoreID: state.StoreID, CustomerID: state.CustomerID,
			StoreCustomerRelationID: state.StoreCustomerRelationID, IntentProfileID: policy.IntentProfileID,
			RunStatus: conversationEvolutionStatusProcessing, SummaryStatus: "pending",
			KnowledgeStatus: "pending", TagStatus: "pending", StartedAt: &now,
			AuditFields: utils.BuildAuditFields(nil),
		}
		if err := repositories.ConversationEvolutionRunRepository.Create(sqls.DB(), run); err == nil {
			return run, false, nil
		}
		existing, err = repositories.ConversationEvolutionRunRepository.GetByCheckpoint(
			sqls.DB(), state.TenantID, state.ConversationID, state.SessionNo, checkpoint,
		)
		if err != nil || existing == nil {
			return nil, false, err
		}
		if existing.RunStatus == conversationEvolutionStatusCompleted {
			return existing, true, nil
		}
	}
	existing.RetryCount++
	if err := repositories.ConversationEvolutionRunRepository.UpdatesInTenant(sqls.DB(), existing.ID, existing.TenantID, map[string]any{
		"store_id": state.StoreID, "customer_id": state.CustomerID,
		"store_customer_relation_id": state.StoreCustomerRelationID, "intent_profile_id": policy.IntentProfileID,
		"run_status": conversationEvolutionStatusProcessing, "summary_status": "pending",
		"knowledge_status": "pending", "tag_status": "pending", "retry_count": existing.RetryCount,
		"redacted_result": "", "last_error_class": "", "started_at": now, "finished_at": nil,
		"updated_at": now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
	}); err != nil {
		return nil, false, err
	}
	existing.RunStatus = conversationEvolutionStatusProcessing
	existing.SummaryStatus = "pending"
	existing.KnowledgeStatus = "pending"
	existing.TagStatus = "pending"
	existing.StartedAt = &now
	existing.FinishedAt = nil
	return existing, false, nil
}

func (s *conversationEvolutionService) completeRun(
	state *models.ConversationEvolutionState,
	owner string,
	run *models.ConversationEvolutionRun,
	policy *conversationEvolutionPolicy,
	summaryStatus, knowledgeStatus, tagStatus string,
	summaryAdvanced bool,
	operations []CustomerTagOperation,
) {
	result, err := s.finalizeRun(state, owner, run, policy, summaryStatus, knowledgeStatus, tagStatus, summaryAdvanced, operations)
	if err != nil {
		s.failClaim(state, owner, run, "customer_tag_apply_failed", false, false)
		return
	}
	if result.changed && result.conversation != nil {
		WsService.PublishCustomerTagChanged(result.conversation, result.storeID, result.relationID, time.Now())
	}
}

func (s *conversationEvolutionService) finalizeRun(
	state *models.ConversationEvolutionState,
	owner string,
	run *models.ConversationEvolutionRun,
	policy *conversationEvolutionPolicy,
	summaryStatus, knowledgeStatus, tagStatus string,
	summaryAdvanced bool,
	operations []CustomerTagOperation,
) (evolutionFinalizeResult, error) {
	result := evolutionFinalizeResult{}
	unlock := lockCustomerTags(state.TenantID, state.StoreID, state.CustomerID)
	defer unlock()
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedState, err := repositories.ConversationEvolutionStateRepository.GetForUpdateOwned(ctx.Tx, state.ID, state.TenantID, owner)
		if err != nil {
			return err
		}
		if lockedState == nil {
			return fmt.Errorf("evolution lease lost")
		}
		lockedRun, err := repositories.ConversationEvolutionRunRepository.GetForUpdateInTenant(ctx.Tx, run.ID, run.TenantID)
		if err != nil {
			return err
		}
		if lockedRun == nil {
			return fmt.Errorf("evolution run missing")
		}
		conversation, err := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, state.ConversationID, state.TenantID)
		if err != nil || conversation == nil {
			return fmt.Errorf("evolution conversation unavailable")
		}
		latest, err := repositories.ConversationEvolutionStateRepository.FindLatestCommittedMessage(
			ctx.Tx, state.TenantID, state.ConversationID, state.SessionNo,
		)
		if err != nil {
			return err
		}
		if latest == nil || latest.ID != run.EndMessageID || lockedState.LastObservedMessageID != run.EndMessageID {
			if err := s.supersedeRunAndRescheduleTx(ctx.Tx, lockedState, owner, lockedRun, latest, policy.QuietPeriod); err != nil {
				return err
			}
			result.superseded = true
			return nil
		}
		currentPolicy, policyClass, err := s.loadPolicy(ctx.Tx, lockedState, true)
		if err != nil {
			return err
		}
		if currentPolicy == nil || policyClass != "" || currentPolicy.IntentProfileID != policy.IntentProfileID {
			if err := s.supersedeRunForPolicyTx(ctx.Tx, lockedState, owner, lockedRun); err != nil {
				return err
			}
			result.superseded = true
			return nil
		}
		scope, err := CustomerTagService.resolveConversationScope(ctx.Tx, state.ConversationID, false)
		if err != nil || !evolutionScopeMatchesState(scope, lockedState) || scope.ProfileID != currentPolicy.IntentProfileID {
			return fmt.Errorf("evolution scope changed")
		}
		if err := CustomerTagService.lockScopeRelation(ctx.Tx, scope); err != nil {
			return err
		}
		changed, err := CustomerTagService.applyAIOperationsDB(ctx.Tx, scope, run.ID, operations)
		if err != nil {
			return err
		}
		finishedAt := time.Now()
		redactedResult, _ := json.Marshal(map[string]any{"operationCount": len(operations), "changed": changed})
		if err := repositories.ConversationEvolutionRunRepository.UpdatesInTenant(ctx.Tx, lockedRun.ID, lockedRun.TenantID, map[string]any{
			"run_status": conversationEvolutionStatusCompleted, "summary_status": summaryStatus,
			"knowledge_status": knowledgeStatus, "tag_status": tagStatus,
			"redacted_result": string(redactedResult), "last_error_class": "",
			"finished_at": finishedAt, "updated_at": finishedAt,
			"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		}); err != nil {
			return err
		}
		stateUpdates := map[string]any{
			"last_evolved_message_id": run.EndMessageID, "last_evolution_run_id": run.ID,
			"next_evolution_at": nil, "next_retry_at": nil,
			"last_status": conversationEvolutionStatusCompleted, "attempt_count": 0,
			"last_error_class": "", "lease_owner": "", "lease_expires_at": nil,
			"updated_at": finishedAt, "update_user_id": constants.SystemAuditUserID,
			"update_user_name": constants.SystemAuditUserName,
		}
		if summaryAdvanced {
			stateUpdates["summary_version"] = lockedState.SummaryVersion + 1
		}
		updated, err := repositories.ConversationEvolutionStateRepository.UpdatesOwned(
			ctx.Tx, lockedState.ID, lockedState.TenantID, owner, stateUpdates,
		)
		if err != nil {
			return err
		}
		if !updated {
			return fmt.Errorf("evolution state finalization lost lease")
		}
		result.changed = changed
		result.conversation = conversation
		result.storeID = scope.StoreID
		result.relationID = scope.Relation.ID
		return nil
	})
	return result, err
}

func (s *conversationEvolutionService) recoverCompletedRun(
	state *models.ConversationEvolutionState,
	owner string,
	run *models.ConversationEvolutionRun,
	policy *conversationEvolutionPolicy,
) {
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedState, err := repositories.ConversationEvolutionStateRepository.GetForUpdateOwned(ctx.Tx, state.ID, state.TenantID, owner)
		if err != nil || lockedState == nil {
			return err
		}
		latest, err := repositories.ConversationEvolutionStateRepository.FindLatestCommittedMessage(ctx.Tx, state.TenantID, state.ConversationID, state.SessionNo)
		if err != nil {
			return err
		}
		if latest == nil || latest.ID != run.EndMessageID || lockedState.LastObservedMessageID != run.EndMessageID {
			return s.rescheduleOwnedTx(ctx.Tx, lockedState, owner, latest, policy.QuietPeriod)
		}
		updates := map[string]any{
			"last_evolved_message_id": run.EndMessageID, "last_evolution_run_id": run.ID,
			"next_evolution_at": nil, "next_retry_at": nil, "last_status": conversationEvolutionStatusCompleted,
			"attempt_count": 0, "last_error_class": "", "lease_owner": "", "lease_expires_at": nil,
			"updated_at": time.Now(), "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		}
		if run.SummaryStatus == "completed" {
			updates["summary_version"] = lockedState.SummaryVersion + 1
		}
		_, err = repositories.ConversationEvolutionStateRepository.UpdatesOwned(ctx.Tx, lockedState.ID, lockedState.TenantID, owner, updates)
		return err
	})
	if err != nil {
		s.failClaim(state, owner, run, "completed_run_recovery_failed", false, false)
	}
}

func (s *conversationEvolutionService) supersedeRunAndRescheduleTx(
	db *gorm.DB,
	state *models.ConversationEvolutionState,
	owner string,
	run *models.ConversationEvolutionRun,
	latest *models.Message,
	quietPeriod time.Duration,
) error {
	now := time.Now()
	if err := repositories.ConversationEvolutionRunRepository.UpdatesInTenant(db, run.ID, run.TenantID, map[string]any{
		"run_status": conversationEvolutionStatusSuperseded, "tag_status": conversationEvolutionStatusSuperseded,
		"last_error_class": "newer_message_committed", "finished_at": now, "updated_at": now,
	}); err != nil {
		return err
	}
	return s.rescheduleOwnedTx(db, state, owner, latest, quietPeriod)
}

func (s *conversationEvolutionService) supersedeRunForPolicyTx(
	db *gorm.DB,
	state *models.ConversationEvolutionState,
	owner string,
	run *models.ConversationEvolutionRun,
) error {
	now := time.Now()
	if err := repositories.ConversationEvolutionRunRepository.UpdatesInTenant(db, run.ID, run.TenantID, map[string]any{
		"run_status": conversationEvolutionStatusSuperseded, "tag_status": "skipped",
		"last_error_class": "evolution_disabled_before_commit", "finished_at": now, "updated_at": now,
	}); err != nil {
		return err
	}
	_, err := repositories.ConversationEvolutionStateRepository.UpdatesOwned(db, state.ID, state.TenantID, owner, map[string]any{
		"last_status": conversationEvolutionStatusWaiting, "last_error_class": "evolution_disabled",
		"next_retry_at": nil, "lease_owner": "", "lease_expires_at": nil, "updated_at": now,
	})
	return err
}

func (s *conversationEvolutionService) completeClaimWithoutRun(state *models.ConversationEvolutionState, owner string) {
	endMessageID := state.LastObservedMessageID
	updated, err := repositories.ConversationEvolutionStateRepository.UpdatesOwnedAtCheckpoint(sqls.DB(), state.ID, state.TenantID, owner, endMessageID, map[string]any{
		"last_evolved_message_id": endMessageID, "next_evolution_at": nil, "next_retry_at": nil,
		"last_status": conversationEvolutionStatusCompleted, "attempt_count": 0, "last_error_class": "",
		"lease_owner": "", "lease_expires_at": nil, "updated_at": time.Now(),
	})
	if err != nil {
		slog.Warn("complete empty customer tag evolution state failed", "state_id", state.ID, "class", "state_update_failed")
		return
	}
	if !updated {
		s.releaseSupersededLease(state, owner)
	}
}

func (s *conversationEvolutionService) releaseDisabledClaim(state *models.ConversationEvolutionState, owner string) {
	_, _ = repositories.ConversationEvolutionStateRepository.UpdatesOwned(sqls.DB(), state.ID, state.TenantID, owner, map[string]any{
		"last_status": conversationEvolutionStatusWaiting, "last_error_class": "evolution_disabled",
		"next_retry_at": nil, "lease_owner": "", "lease_expires_at": nil, "updated_at": time.Now(),
	})
}

func (s *conversationEvolutionService) rescheduleClaim(state *models.ConversationEvolutionState, owner string, latest *models.Message, quietPeriod time.Duration) {
	if latest == nil {
		s.completeClaimWithoutRun(state, owner)
		return
	}
	s.rescheduleClaimAt(state, owner, latest.ID, evolutionMessageTime(latest).Add(quietPeriod))
}

func (s *conversationEvolutionService) rescheduleClaimAt(state *models.ConversationEvolutionState, owner string, messageID int64, next time.Time) {
	updated, err := repositories.ConversationEvolutionStateRepository.UpdatesOwnedAtCheckpoint(
		sqls.DB(), state.ID, state.TenantID, owner, state.LastObservedMessageID, map[string]any{
			"last_observed_message_id": messageID, "next_evolution_at": next, "next_retry_at": nil,
			"last_status": conversationEvolutionStatusWaiting, "attempt_count": 0, "last_error_class": "",
			"lease_owner": "", "lease_expires_at": nil, "updated_at": time.Now(),
		})
	if err != nil {
		slog.Warn("reschedule customer tag evolution state failed", "state_id", state.ID, "class", "state_update_failed")
		return
	}
	if !updated {
		s.releaseSupersededLease(state, owner)
	}
}

func (s *conversationEvolutionService) releaseSupersededLease(state *models.ConversationEvolutionState, owner string) {
	if state == nil {
		return
	}
	if _, err := repositories.ConversationEvolutionStateRepository.ReleaseOwned(
		sqls.DB(), state.ID, state.TenantID, owner, time.Now(),
	); err != nil {
		slog.Warn("release superseded customer tag evolution lease failed", "state_id", state.ID, "class", "lease_release_failed")
	}
}

func (s *conversationEvolutionService) rescheduleOwnedTx(
	db *gorm.DB,
	state *models.ConversationEvolutionState,
	owner string,
	latest *models.Message,
	quietPeriod time.Duration,
) error {
	if latest == nil {
		_, err := repositories.ConversationEvolutionStateRepository.UpdatesOwned(db, state.ID, state.TenantID, owner, map[string]any{
			"last_evolved_message_id": state.LastObservedMessageID, "next_evolution_at": nil, "next_retry_at": nil,
			"last_status": conversationEvolutionStatusCompleted, "attempt_count": 0, "last_error_class": "",
			"lease_owner": "", "lease_expires_at": nil, "updated_at": time.Now(),
		})
		return err
	}
	next := evolutionMessageTime(latest).Add(quietPeriod)
	_, err := repositories.ConversationEvolutionStateRepository.UpdatesOwned(db, state.ID, state.TenantID, owner, map[string]any{
		"last_observed_message_id": latest.ID, "next_evolution_at": next, "next_retry_at": nil,
		"last_status": conversationEvolutionStatusWaiting, "attempt_count": 0, "last_error_class": "",
		"lease_owner": "", "lease_expires_at": nil, "updated_at": time.Now(),
	})
	return err
}

func (s *conversationEvolutionService) failClaim(
	state *models.ConversationEvolutionState,
	owner string,
	run *models.ConversationEvolutionRun,
	errorClass string,
	blocked bool,
	terminal bool,
) {
	if state == nil {
		return
	}
	errorClass = strings.TrimSpace(errorClass)
	if errorClass == "" {
		errorClass = "evolution_failed"
	}
	attempt := state.AttemptCount + 1
	status := conversationEvolutionStatusFailed
	if blocked {
		status = conversationEvolutionStatusBlocked
	}
	now := time.Now()
	var nextRetryAt *time.Time
	if !terminal && (blocked || attempt < conversationEvolutionMaxFailures) {
		next := now.Add(evolutionRetryDelay(attempt, blocked))
		nextRetryAt = &next
	}
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedState, err := repositories.ConversationEvolutionStateRepository.GetForUpdateOwned(ctx.Tx, state.ID, state.TenantID, owner)
		if err != nil {
			return err
		}
		if lockedState == nil {
			return nil
		}
		if lockedState.LastObservedMessageID != state.LastObservedMessageID {
			if run != nil && run.ID > 0 && run.RunStatus != conversationEvolutionStatusCompleted {
				if err := repositories.ConversationEvolutionRunRepository.UpdatesInTenant(ctx.Tx, run.ID, run.TenantID, map[string]any{
					"run_status":       conversationEvolutionStatusSuperseded,
					"tag_status":       conversationEvolutionStatusSuperseded,
					"last_error_class": "newer_message_committed", "finished_at": now, "updated_at": now,
				}); err != nil {
					return err
				}
			}
			_, err = repositories.ConversationEvolutionStateRepository.ReleaseOwned(
				ctx.Tx, lockedState.ID, lockedState.TenantID, owner, now,
			)
			return err
		}
		attempt = lockedState.AttemptCount + 1
		if !terminal && (blocked || attempt < conversationEvolutionMaxFailures) {
			next := now.Add(evolutionRetryDelay(attempt, blocked))
			nextRetryAt = &next
		} else {
			nextRetryAt = nil
		}
		if run != nil && run.ID > 0 {
			tagStatus := conversationEvolutionStatusFailed
			if blocked {
				tagStatus = conversationEvolutionStatusBlocked
			}
			if err := repositories.ConversationEvolutionRunRepository.UpdatesInTenant(ctx.Tx, run.ID, run.TenantID, map[string]any{
				"run_status": conversationEvolutionStatusFailed, "tag_status": tagStatus,
				"last_error_class": errorClass, "finished_at": now, "updated_at": now,
			}); err != nil {
				return err
			}
		}
		_, err = repositories.ConversationEvolutionStateRepository.UpdatesOwned(ctx.Tx, state.ID, state.TenantID, owner, map[string]any{
			"last_status": status, "attempt_count": attempt, "next_evolution_at": nil,
			"next_retry_at": nextRetryAt, "last_error_class": errorClass,
			"lease_owner": "", "lease_expires_at": nil, "updated_at": now,
			"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		})
		return err
	})
	if err != nil {
		slog.Warn("persist customer tag evolution failure failed", "state_id", state.ID, "class", "failure_state_write_failed")
	}
}

func (s *conversationEvolutionService) renewClaim(state *models.ConversationEvolutionState, owner string) bool {
	now := time.Now()
	renewed, err := repositories.ConversationEvolutionStateRepository.RenewLease(
		sqls.DB(), state.ID, state.TenantID, owner, state.LastObservedMessageID,
		now.Add(conversationEvolutionLeaseDuration), now,
	)
	if err != nil || !renewed {
		if err == nil {
			s.releaseSupersededLease(state, owner)
		}
		slog.Warn("renew customer tag evolution lease failed", "state_id", state.ID, "class", "lease_lost")
		return false
	}
	return true
}

func (s *conversationEvolutionService) loadPolicy(
	db *gorm.DB,
	state *models.ConversationEvolutionState,
	requireEnabled bool,
) (*conversationEvolutionPolicy, string, error) {
	if state == nil || state.TenantID <= 0 || state.StoreID <= 0 {
		return nil, "evolution_scope_invalid", nil
	}
	tenantPolicy := repositories.TenantCustomerTagPolicyRepository.GetByTenant(db, state.TenantID)
	if tenantPolicy == nil || tenantPolicy.Status != enums.StatusOk || tenantPolicy.IntentProfileID <= 0 {
		return nil, "tenant_tag_policy_unavailable", nil
	}
	storePolicy, err := repositories.StoreCustomerTagRuntimePolicyRepository.GetByStore(db, state.TenantID, state.StoreID)
	if err != nil {
		return nil, "store_tag_policy_query_failed", err
	}
	if storePolicy == nil || storePolicy.Status != enums.StatusOk || (requireEnabled && !storePolicy.CustomerTagEvolutionEnabled) {
		return nil, "evolution_disabled", nil
	}
	quietMinutes := normalizeEvolutionQuietMinutes(tenantPolicy.QuietPeriodMinutes)
	minimumConfidence := tenantPolicy.MinimumConfidence
	if minimumConfidence <= 0 || minimumConfidence > 1 {
		minimumConfidence = 0.8
	}
	maxOperations := tenantPolicy.MaxOperationsPerRun
	if maxOperations <= 0 {
		maxOperations = 6
	}
	if maxOperations > conversationEvolutionMaxOperations {
		maxOperations = conversationEvolutionMaxOperations
	}
	return &conversationEvolutionPolicy{
		TenantID: state.TenantID, StoreID: state.StoreID, IntentProfileID: tenantPolicy.IntentProfileID,
		QuietPeriod:       time.Duration(quietMinutes) * time.Minute,
		MinimumConfidence: minimumConfidence, MaxOperationsPerRun: maxOperations,
	}, "", nil
}

func (s *conversationEvolutionService) quietPeriodForTenant(tenantID int64) time.Duration {
	minutes := conversationEvolutionDefaultQuietMinutes
	if policy := repositories.TenantCustomerTagPolicyRepository.GetByTenant(sqls.DB(), tenantID); policy != nil && policy.Status == enums.StatusOk {
		minutes = normalizeEvolutionQuietMinutes(policy.QuietPeriodMinutes)
	}
	return time.Duration(minutes) * time.Minute
}

func (s *conversationEvolutionService) updateSessionSummary(state *models.ConversationEvolutionState, checkpoint int64) (string, bool) {
	if state == nil || checkpoint <= 0 {
		return "skipped", false
	}
	for attempt := 0; attempt < 2; attempt++ {
		current := repositories.ConversationSessionSummaryRepository.FindOne(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", state.TenantID).
			Eq("conversation_id", state.ConversationID).
			Eq("session_no", state.SessionNo))
		afterMessageID := int64(0)
		stable, issues, preferences, media := "", "", "", ""
		messageCount := 0
		if current != nil {
			if current.LastMessageID >= checkpoint {
				return "completed", false
			}
			afterMessageID = current.LastMessageID
			stable, issues = current.StableFacts, current.OpenIssues
			preferences, media = current.CustomerPreferences, current.MediaSummary
			messageCount = current.MessageCount
		}
		messages, err := repositories.ConversationEvolutionStateRepository.FindCommittedMessages(
			sqls.DB(), state.TenantID, state.ConversationID, state.SessionNo, afterMessageID, checkpoint,
		)
		if err != nil {
			return "failed", false
		}
		if len(messages) == 0 {
			return "completed", false
		}
		stable, issues, preferences, media = evolveSummaryFields(messages, stable, issues, preferences, media)
		messageCount += len(messages)
		now := time.Now()
		columns := map[string]any{
			"store_id": state.StoreID, "customer_id": state.CustomerID,
			"stable_facts": stable, "open_issues": issues,
			"customer_preferences": preferences, "media_summary": media,
			"message_count":   messageCount,
			"token_estimate":  estimateEvolutionTokens(stable + issues + preferences + media),
			"last_message_id": checkpoint, "status": enums.StatusOk,
			"updated_at": now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		}
		if current != nil {
			updated, err := repositories.ConversationEvolutionStateRepository.UpdateSessionSummaryIfOlder(
				sqls.DB(), current.ID, state.TenantID, checkpoint, columns,
			)
			if err != nil {
				return "failed", false
			}
			if updated {
				return "completed", true
			}
			continue
		}
		route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), state.ConversationID, state.TenantID)
		instanceID := int64(0)
		if route != nil {
			instanceID = route.WxWorkInstanceID
		}
		item := &models.ConversationSessionSummary{
			TenantID: state.TenantID, ConversationID: state.ConversationID, SessionNo: state.SessionNo,
			WxWorkInstanceID: instanceID, StoreID: state.StoreID, CustomerID: state.CustomerID,
			StableFacts: stable, OpenIssues: issues, CustomerPreferences: preferences, MediaSummary: media,
			MessageCount: messageCount, TokenEstimate: estimateEvolutionTokens(stable + issues + preferences + media),
			LastMessageID: checkpoint, Status: enums.StatusOk, AuditFields: utils.BuildAuditFields(nil),
		}
		created, err := repositories.ConversationEvolutionStateRepository.CreateSessionSummaryIfAbsent(sqls.DB(), item)
		if err != nil {
			return "failed", false
		}
		if created {
			return "completed", true
		}
	}
	return "completed", false
}

func (s *conversationEvolutionService) evolveKnowledge(conversationID int64) string {
	if _, err := KnowledgeCandidateService.ExtractFromResolvedConversation(conversationID, enums.KnowledgeCandidateSourceAgentDeskHQ); err != nil {
		return "failed"
	}
	return "completed"
}

func evolutionScopeMatchesState(scope *customerTagScope, state *models.ConversationEvolutionState) bool {
	return scope != nil && scope.Conversation != nil && scope.Relation != nil && state != nil &&
		scope.TenantID == state.TenantID && scope.StoreID == state.StoreID &&
		scope.Conversation.ID == state.ConversationID && scope.Conversation.CustomerID == state.CustomerID &&
		scope.Relation.ID == state.StoreCustomerRelationID && scope.Relation.TenantID == state.TenantID &&
		scope.Relation.StoreID == state.StoreID && scope.Relation.CustomerID == state.CustomerID
}

func evolutionMessageTime(message *models.Message) time.Time {
	if message == nil {
		return time.Now()
	}
	if message.SentAt != nil && !message.SentAt.IsZero() {
		return *message.SentAt
	}
	if !message.CreatedAt.IsZero() {
		return message.CreatedAt
	}
	return time.Now()
}

func normalizeEvolutionQuietMinutes(minutes int) int {
	if minutes <= 0 {
		return conversationEvolutionDefaultQuietMinutes
	}
	if minutes > conversationEvolutionMaxQuietMinutes {
		return conversationEvolutionMaxQuietMinutes
	}
	return minutes
}

func evolutionRetryDelay(attempt int, blocked bool) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if blocked {
		delays := []time.Duration{time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour}
		if attempt > len(delays) {
			return delays[len(delays)-1]
		}
		return delays[attempt-1]
	}
	delays := []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}
	if attempt > len(delays) {
		return delays[len(delays)-1]
	}
	return delays[attempt-1]
}
