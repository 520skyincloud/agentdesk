package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	aiReplyJobLifetime                 = 15 * time.Minute
	aiReplyJobInitialDelay             = 250 * time.Millisecond
	aiReplyJobLeaseDuration            = 90 * time.Second
	aiReplyJobRenewInterval            = 30 * time.Second
	aiReplyJobContinuationDelay        = 250 * time.Millisecond
	aiReplyJobMaxAttempts              = 4
	aiReplyJobModelRecoveryMaxAttempts = 2
	aiReplyJobMaxConcurrency           = 4
)

var aiReplyJobRetryDelays = []time.Duration{15 * time.Second, time.Minute, 3 * time.Minute}

// aiReplyJobProtocolRetryDelays keeps the first abnormal recovery fast, then
// backs off to a five-minute ceiling. Technical failures remain durable rather
// than becoming a false customer-visible answer or a silent terminal Job.
var aiReplyJobProtocolRetryDelays = []time.Duration{
	800 * time.Millisecond,
	1500 * time.Millisecond,
	5 * time.Second,
	15 * time.Second,
	time.Minute,
	3 * time.Minute,
	5 * time.Minute,
}

// aiReplyJobRetryDelayFor 按失败类别选择退避表。
func aiReplyJobRetryDelayFor(errorClass string, attempt int) time.Duration {
	delays := aiReplyJobRetryDelays
	if isAIReplyProtocolOrContentFailure(errorClass) {
		delays = aiReplyJobProtocolRetryDelays
	}
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(delays) {
		attempt = len(delays) - 1
	}
	return delays[attempt]
}

// isAIReplyProtocolOrContentFailure 判定协议/内容类失败（本地可修复，
// 不值得长退避）。计划 3.5：generation_failed 实际多为本地 validation 拒绝。
func isAIReplyProtocolOrContentFailure(errorClass string) bool {
	switch strings.TrimSpace(errorClass) {
	case "intent_detect_failed", "generation_failed", "empty_output",
		"resource_invariant_broken", "runtime_result_invalid", "protocol_error":
		return true
	default:
		return false
	}
}

var (
	AIReplyJobService                  = newAIReplyJobService()
	errAIReplyMediaUnderstandingFailed = errors.New("media understanding failed")
)

type aiReplyJobService struct {
	workerSlots      chan struct{}
	activeMu         sync.Mutex
	activeExecutions map[int64]map[int64]context.CancelFunc
	humanDispatch    func(state *aiReplyJobExecutionState, job *models.AIReplyJob, reason string) error
}

type aiReplyJobLeaseContext struct {
	JobID    int64
	TenantID int64
	Owner    string
}

type aiReplyJobLeaseContextKey struct{}

func (s *aiReplyJobService) CurrentJobID(ctx context.Context, tenantID, conversationID int64) int64 {
	if ctx == nil {
		return 0
	}
	lease, ok := ctx.Value(aiReplyJobLeaseContextKey{}).(aiReplyJobLeaseContext)
	if !ok || lease.JobID <= 0 || lease.TenantID != tenantID {
		return 0
	}
	job := repositories.AIReplyJobRepository.GetInTenant(sqls.DB(), lease.JobID, lease.TenantID)
	if job == nil || job.ConversationID != conversationID || job.Status != enums.AIReplyJobStatusProcessing ||
		job.LeaseOwner != lease.Owner || job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(time.Now()) {
		return 0
	}
	return job.ID
}

type aiReplyJobExecutionState struct {
	Job          *models.AIReplyJob
	Conversation *models.Conversation
	Message      *models.Message
	Route        *models.ConversationRouteState
	Session      *models.ConversationChannelSession
	Instance     *models.WxWorkProtocolInstance
}

type aiReplyJobDecision struct {
	Status               enums.AIReplyJobStatus
	Code                 string
	CommittedMessageIDs  []int64
	PersistedInterruptID int64
	CoveredByMessageID   int64
	CoveredByTaskID      int64
}

type aiReplyJobTerminalError struct {
	code string
}

type aiReplyJobDispatchOnlyError struct {
	cause error
}

func (e *aiReplyJobDispatchOnlyError) Error() string { return "human_dispatch_failed" }
func (e *aiReplyJobDispatchOnlyError) Unwrap() error { return e.cause }

type aiReplyJobDispatchCompleted struct {
	errorClass string
}

func (e *aiReplyJobDispatchCompleted) Error() string { return "human_dispatch_completed" }

func (e *aiReplyJobTerminalError) Error() string {
	return "AI reply job cannot continue"
}

func newAIReplyJobService() *aiReplyJobService {
	return &aiReplyJobService{
		workerSlots:      make(chan struct{}, aiReplyJobMaxConcurrency),
		activeExecutions: make(map[int64]map[int64]context.CancelFunc),
	}
}

func (s *aiReplyJobService) EnqueueForMessageDB(db *gorm.DB, conversation *models.Conversation, message *models.Message) (*models.AIReplyJob, bool, error) {
	if db == nil || conversation == nil || message == nil {
		return nil, false, errorsx.InvalidParam("AI 回复任务缺少消息上下文")
	}
	triggerKind, ok := aiReplyTriggerKind(message)
	if !ok || message.HistoricalOnly || message.SenderType != enums.IMSenderTypeCustomer ||
		message.SendStatus == enums.IMMessageStatusFailed || message.SendStatus == enums.IMMessageStatusRecalled || message.RecalledAt != nil {
		return nil, false, nil
	}
	if conversation.ID <= 0 || conversation.TenantID <= 0 || message.ID <= 0 ||
		message.TenantID != conversation.TenantID || message.ConversationID != conversation.ID {
		return nil, false, errorsx.InvalidParam("AI 回复任务消息与会话范围不一致")
	}
	// 转人工二次确认是普通消息的独占消费者。确认窗口内先不创建普通 AI Job，
	// 由事务后的确认分类决定：confirm/cancel 直接消费；unknown 清除门禁后
	// 再通过 EnsureForMessage 幂等补建。精确文本 1 是独立入住入口，不表达确认语义。
	if triggerKind != enums.AIReplyJobTriggerKindStandaloneOne &&
		(isConsumedHandoffConfirmationMessage(*message) || activeHumanHandoffConfirmationDB(db, conversation.ID, conversation.TenantID, time.Now())) {
		return nil, false, nil
	}
	requestID := tracex.EnsureRequestID(message.RequestID)
	if requestID == "" {
		return nil, false, fmt.Errorf("generate AI reply request id")
	}
	if requestID != message.RequestID {
		if err := repositories.MessageRepository.UpdatesInTenant(db, message.ID, message.TenantID, map[string]any{
			"request_id":       requestID,
			"updated_at":       time.Now(),
			"update_user_name": "ai_reply_enqueue",
		}); err != nil {
			return nil, false, err
		}
		message.RequestID = requestID
	}
	now := time.Now()
	createdAt := message.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	nextRetryAt := now.Add(aiReplyJobInitialDelay)
	item := &models.AIReplyJob{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, MessageID: message.ID,
		SessionNo: message.SessionNo, StoreID: conversation.StoreID, StoreStaffBindingID: conversation.StoreStaffBindingID,
		TurnID: message.AIReplyTurnID, TurnVersion: message.AIReplyTurnVersion,
		RequestID: requestID, TriggerKind: triggerKind, Status: enums.AIReplyJobStatusPending,
		NextRetryAt: &nextRetryAt, ExpiresAt: createdAt.Add(aiReplyJobLifetime),
		AuditFields: models.AuditFields{
			CreatedAt: createdAt, CreateUserName: "ai_reply_enqueue",
			UpdatedAt: now, UpdateUserName: "ai_reply_enqueue",
		},
	}
	created, err := repositories.AIReplyJobRepository.CreateIfAbsent(db, item)
	if err != nil {
		return nil, false, err
	}
	if created {
		return item, true, nil
	}
	existing := repositories.AIReplyJobRepository.GetByMessageInTenant(db, message.TenantID, message.ConversationID, message.ID)
	if existing == nil {
		return nil, false, fmt.Errorf("AI reply job conflict without existing row")
	}
	return existing, false, nil
}

func (s *aiReplyJobService) EnsureForMessage(messageID int64) (*models.AIReplyJob, bool, error) {
	message := repositories.MessageRepository.Get(sqls.DB(), messageID)
	if message == nil {
		return nil, false, errorsx.InvalidParam("AI 回复任务消息不存在")
	}
	conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), message.ConversationID, message.TenantID)
	if conversation == nil {
		return nil, false, errorsx.InvalidParam("AI 回复任务会话不存在或跨租户")
	}
	var (
		item    *models.AIReplyJob
		created bool
	)
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		item, created, err = s.EnqueueForMessageDB(ctx.Tx, conversation, message)
		return err
	})
	return item, created, err
}

func (s *aiReplyJobService) RepairMissingRecent(limit int) (int, error) {
	messages, err := repositories.AIReplyJobRepository.FindMessagesMissingJobs(sqls.DB(), time.Now().Add(-aiReplyJobLifetime), limit)
	if err != nil {
		return 0, err
	}
	repaired := 0
	for i := range messages {
		_, created, ensureErr := s.EnsureForMessage(messages[i].ID)
		if ensureErr != nil {
			return repaired, ensureErr
		}
		if created {
			repaired++
		}
	}
	return repaired, nil
}

func aiReplyTriggerKind(message *models.Message) (enums.AIReplyJobTriggerKind, bool) {
	if message == nil {
		return "", false
	}
	if isStandaloneOneCustomerMessage(message) {
		return enums.AIReplyJobTriggerKindStandaloneOne, true
	}
	switch message.MessageType {
	case enums.IMMessageTypeText, enums.IMMessageTypeHTML:
		return enums.AIReplyJobTriggerKindText, true
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeAttachment:
		return enums.AIReplyJobTriggerKindMedia, true
	default:
		return "", false
	}
}

func (s *aiReplyJobService) ProcessDue(limit int) int {
	if limit <= 0 || limit > aiReplyJobMaxConcurrency {
		limit = aiReplyJobMaxConcurrency
	}
	now := time.Now()
	candidates, err := repositories.AIReplyJobRepository.FindClaimable(sqls.DB(), now, limit)
	if err != nil {
		slog.Error("find due AI reply jobs failed", "stage", "claim_scan", "error_class", "database_error")
		return 0
	}
	claimed := 0
	for i := range candidates {
		if !s.tryReserveWorkerSlot() {
			break
		}
		owner := "ai-reply-" + strings.ReplaceAll(uuid.NewString(), "-", "")
		ok, claimErr := repositories.AIReplyJobRepository.TryClaim(
			sqls.DB(), candidates[i].ID, candidates[i].TenantID, owner, now, now.Add(aiReplyJobLeaseDuration),
		)
		if claimErr != nil {
			s.releaseWorkerSlot()
			slog.Warn("claim AI reply job failed", "job_id", candidates[i].ID, "stage", "claim", "error_class", "database_error")
			continue
		}
		if !ok {
			s.releaseWorkerSlot()
			continue
		}
		current := repositories.AIReplyJobRepository.GetInTenant(sqls.DB(), candidates[i].ID, candidates[i].TenantID)
		if current == nil {
			s.releaseWorkerSlot()
			continue
		}
		claimed++
		go func(item models.AIReplyJob, leaseOwner string) {
			defer s.releaseWorkerSlot()
			s.processClaimed(&item, leaseOwner)
		}(*current, owner)
	}
	return claimed
}

func (s *aiReplyJobService) tryReserveWorkerSlot() bool {
	select {
	case s.workerSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *aiReplyJobService) releaseWorkerSlot() {
	<-s.workerSlots
}

func (s *aiReplyJobService) NotifyNewerMessage(conversationID, messageID int64) {
	if conversationID <= 0 || messageID <= 0 {
		return
	}
	newMessage := repositories.MessageRepository.Get(sqls.DB(), messageID)
	if isStandaloneOneCustomerMessage(newMessage) {
		return
	}
	s.activeMu.Lock()
	active := s.activeExecutions[conversationID]
	cancels := make([]context.CancelFunc, 0, len(active))
	for activeMessageID, cancel := range active {
		if activeMessageID >= messageID || cancel == nil {
			continue
		}
		activeMessage := repositories.MessageRepository.Get(sqls.DB(), activeMessageID)
		if isStandaloneOneCustomerMessage(activeMessage) {
			continue
		}
		if newMessage != nil && activeMessage != nil && newMessage.AIReplyTurnID > 0 &&
			newMessage.AIReplyTurnID == activeMessage.AIReplyTurnID && newMessage.SessionNo == activeMessage.SessionNo &&
			newMessage.AIReplyTurnVersion <= activeMessage.AIReplyTurnVersion {
			continue
		}
		if activeMessageID < messageID {
			cancels = append(cancels, cancel)
		}
	}
	s.activeMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// SkipPendingForMessage 把某条客户消息尚未执行的 AI Reply Job 标记为 skipped。
// 用于该消息已被确定性消费（例如转人工二次确认的"取消/确认"），不需要再走 AI 生成，
// 避免 job worker 把确认/取消消息当成普通诉求重新触发意图或转人工。
func (s *aiReplyJobService) SkipPendingForMessage(tenantID, conversationID, messageID int64, resultCode string) error {
	if tenantID <= 0 || conversationID <= 0 || messageID <= 0 {
		return nil
	}
	now := time.Now()
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.AIReplyJobRepository.SkipPendingByMessageInTenant(ctx.Tx, tenantID, conversationID, messageID, resultCode, now); err != nil {
			return err
		}
		return AIReplyTurnTaskService.SkipNonTerminalBySourceMessageDB(ctx.Tx, tenantID, conversationID, messageID, resultCode, now)
	})
	s.cancelActiveExecution(conversationID, messageID)
	return err
}

func activeHumanHandoffConfirmationDB(db *gorm.DB, conversationID, tenantID int64, now time.Time) bool {
	if db == nil || conversationID <= 0 || tenantID <= 0 {
		return false
	}
	state := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversationID, tenantID)
	if state == nil || state.PendingAction != string(enums.ConversationPendingActionHumanHandoff) {
		return false
	}
	return state.PendingActionExpireAt == nil || now.Before(*state.PendingActionExpireAt)
}

func (s *aiReplyJobService) cancelActiveExecution(conversationID, messageID int64) {
	if conversationID <= 0 || messageID <= 0 {
		return
	}
	s.activeMu.Lock()
	cancel := s.activeExecutions[conversationID][messageID]
	s.activeMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *aiReplyJobService) registerActiveExecution(job *models.AIReplyJob, cancel context.CancelFunc) func() {
	if job == nil || job.ConversationID <= 0 || job.MessageID <= 0 || cancel == nil {
		return func() {}
	}
	s.activeMu.Lock()
	active := s.activeExecutions[job.ConversationID]
	if active == nil {
		active = make(map[int64]context.CancelFunc)
		s.activeExecutions[job.ConversationID] = active
	}
	active[job.MessageID] = cancel
	s.activeMu.Unlock()
	return func() {
		s.activeMu.Lock()
		if active := s.activeExecutions[job.ConversationID]; active != nil {
			delete(active, job.MessageID)
			if len(active) == 0 {
				delete(s.activeExecutions, job.ConversationID)
			}
		}
		s.activeMu.Unlock()
	}
}

func (s *aiReplyJobService) ProcessMessageNow(messageID int64) (*models.AIReplyJob, error) {
	job, _, err := s.EnsureForMessage(messageID)
	if err != nil || job == nil {
		return job, err
	}
	if isTerminalAIReplyJobStatus(job.Status) {
		return job, nil
	}
	now := time.Now()
	if job.NextRetryAt != nil && job.NextRetryAt.After(now) {
		return job, nil
	}
	owner := "ai-reply-sync-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	claimed, err := repositories.AIReplyJobRepository.TryClaim(sqls.DB(), job.ID, job.TenantID, owner, now, now.Add(aiReplyJobLeaseDuration))
	if err != nil || !claimed {
		return repositories.AIReplyJobRepository.GetInTenant(sqls.DB(), job.ID, job.TenantID), err
	}
	current := repositories.AIReplyJobRepository.GetInTenant(sqls.DB(), job.ID, job.TenantID)
	if current == nil {
		return nil, fmt.Errorf("claimed AI reply job disappeared")
	}
	s.processClaimed(current, owner)
	return repositories.AIReplyJobRepository.GetInTenant(sqls.DB(), job.ID, job.TenantID), nil
}

func (s *aiReplyJobService) processClaimed(job *models.AIReplyJob, owner string) {
	if job == nil || strings.TrimSpace(owner) == "" {
		return
	}
	if job.TurnID > 0 {
		claimed, err := s.tryClaimTurn(job, owner)
		if err != nil {
			_, _ = repositories.AIReplyJobRepository.MarkRetry(sqls.DB(), job.ID, job.TenantID, owner,
				"turn_claim_failed", "database_error", time.Now().Add(time.Second), time.Now(), false)
			return
		}
		if !claimed {
			turn := repositories.AIReplyTurnRepository.GetInTenant(sqls.DB(), job.TurnID, job.TenantID)
			if turn != nil && job.TurnVersion > 0 && job.TurnVersion < turn.Version {
				s.markTerminal(job, owner, enums.AIReplyJobStatusSuperseded, "stale_turn_version", "", time.Now())
				return
			}
			// turn 已是终态（closed/interrupted/failed）时，job 再重试也永远 claim 不到，
			// 只会 turn_busy 死循环占满 worker。直接 superseded，别让它无限重试。
			if turn != nil && aiReplyTurnTerminalStatus(turn.Status) {
				s.markTerminal(job, owner, enums.AIReplyJobStatusSuperseded, "turn_terminal", "", time.Now())
				return
			}
			_, _ = repositories.AIReplyJobRepository.MarkRetry(sqls.DB(), job.ID, job.TenantID, owner,
				"turn_busy", "", time.Now().Add(500*time.Millisecond), time.Now(), false)
			return
		}
		defer s.releaseTurn(job, owner)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, aiReplyJobLeaseContextKey{}, aiReplyJobLeaseContext{JobID: job.ID, TenantID: job.TenantID, Owner: owner})
	unregister := s.registerActiveExecution(job, cancel)
	defer unregister()
	leaseLost := &atomic.Bool{}
	done := make(chan struct{})
	go s.renewLease(ctx, cancel, done, leaseLost, job, owner)
	leaseStopped := false
	defer func() {
		if !leaseStopped {
			cancel()
			<-done
		}
		if recovered := recover(); recovered != nil {
			slog.Error("AI reply job worker panicked", "job_id", job.ID, "stage", "execute", "error_class", "worker_panic")
			if !leaseLost.Load() {
				s.retryOrDispatch(job, owner, "worker_panic", time.Now())
			}
		}
	}()

	result, runErr := s.executeClaimed(ctx, job)
	cancel()
	<-done
	leaseStopped = true
	if leaseLost.Load() {
		return
	}
	s.finishClaimed(job, owner, result, runErr)
}

func (s *aiReplyJobService) tryClaimTurn(job *models.AIReplyJob, owner string) (bool, error) {
	var claimed bool
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		now := time.Now()
		claimed, err = AIReplyTurnService.TryClaimJobDB(ctx.Tx, job, owner, now, now.Add(aiReplyJobLeaseDuration))
		return err
	})
	return claimed, err
}

func (s *aiReplyJobService) releaseTurn(job *models.AIReplyJob, owner string) {
	if job == nil || job.TurnID <= 0 {
		return
	}
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return AIReplyTurnService.ReleaseJobLeaseDB(ctx.Tx, job, owner, true, time.Now())
	}); err != nil {
		slog.Warn("release AI reply turn lease failed", "job_id", job.ID, "turn_id", job.TurnID, "stage", "turn_release", "error_class", "database_error")
	}
}

func (s *aiReplyJobService) renewLease(ctx context.Context, cancel context.CancelFunc, done chan<- struct{}, lost *atomic.Bool, job *models.AIReplyJob, owner string) {
	defer close(done)
	ticker := time.NewTicker(aiReplyJobRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			ok, err := s.renewJobAndTurnLease(job, owner, now)
			if err != nil || !ok {
				lost.Store(true)
				cancel()
				return
			}
		}
	}
}

func (s *aiReplyJobService) renewJobAndTurnLease(job *models.AIReplyJob, owner string, now time.Time) (bool, error) {
	var renewed bool
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		renewed, err = repositories.AIReplyJobRepository.RenewLease(ctx.Tx, job.ID, job.TenantID, owner, now, now.Add(aiReplyJobLeaseDuration))
		if err != nil || !renewed || job.TurnID <= 0 {
			return err
		}
		renewed, err = AIReplyTurnService.RenewJobLeaseDB(ctx.Tx, job, owner, now, now.Add(aiReplyJobLeaseDuration))
		return err
	})
	return renewed, err
}

func (s *aiReplyJobService) executeClaimed(ctx context.Context, job *models.AIReplyJob) (AIReplyExecutionResult, error) {
	state, decision := s.inspectExecutionState(job, false)
	if decision != nil {
		if decision.Status == enums.AIReplyJobStatusFailed {
			return AIReplyExecutionResult{}, &aiReplyJobTerminalError{code: decision.Code}
		}
		return executionResultForDecision(*decision), nil
	}
	if state == nil {
		return AIReplyExecutionResult{}, fmt.Errorf("AI reply execution state unavailable")
	}
	if retryAt, held := WxWorkKFMessageRefService.ActiveOutboundReconciliationHold(job.TenantID, job.ConversationID, time.Now()); held {
		return AIReplyExecutionResult{
			Status: AIReplyExecutionStatusDeferred, ReasonCode: "unknown_outbound_reconciliation_hold", RetryAt: retryAt,
		}, nil
	}
	if strings.TrimSpace(job.ResultCode) == "human_dispatch_retry" {
		if decision := s.inspectFreshness(state); decision != nil {
			if decision.Status != enums.AIReplyJobStatusCompleted ||
				!AIReplyTurnTaskService.HasFailureHandoffs(job.TenantID, job.TurnID) {
				return executionResultForDecision(*decision), nil
			}
		}
		if err := s.dispatchHuman(state, job, "AI 自动回复失败，需要人工跟进"); err != nil {
			return AIReplyExecutionResult{}, &aiReplyJobDispatchOnlyError{cause: err}
		}
		if job.TurnID > 0 && AIReplyTurnTaskService.Enabled() {
			if err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
				return AIReplyTurnTaskService.MarkPendingHandoffsDB(tx.Tx, job.TenantID, job.TurnID, "human_handoff", time.Now())
			}); err != nil {
				return AIReplyExecutionResult{}, &aiReplyJobDispatchOnlyError{cause: err}
			}
			if AIReplyTurnTaskService.HasRunnable(job.TenantID, job.TurnID) {
				retryAt := time.Now().Add(aiReplyJobContinuationDelay)
				return AIReplyExecutionResult{
					Status: AIReplyExecutionStatusDeferred, ReasonCode: "turn_tasks_remaining", RetryAt: &retryAt,
					TaskLedgerEnabled: true, HasRemainingTasks: true,
				}, nil
			}
			if decision := s.inspectFreshness(state); decision != nil && decision.Status == enums.AIReplyJobStatusCompleted {
				result := executionResultForDecision(*decision)
				result.TaskLedgerEnabled = true
				return result, nil
			}
		}
		return AIReplyExecutionResult{}, &aiReplyJobDispatchCompleted{errorClass: controlledErrorClass(job.LastErrorClass)}
	}
	if time.Now().After(job.ExpiresAt) && !aiReplyJobPersistentTechnicalRetry(job) {
		if decision := s.inspectFreshness(state); decision != nil {
			return executionResultForDecision(*decision), nil
		}
		return AIReplyExecutionResult{}, errAIReplyJobExpired
	}
	if job.TriggerKind == enums.AIReplyJobTriggerKindMedia {
		retryAt, err := s.prepareMedia(ctx, state)
		if err != nil {
			return AIReplyExecutionResult{}, err
		}
		if retryAt != nil {
			return AIReplyExecutionResult{
				Status: AIReplyExecutionStatusDeferred, ReasonCode: "media_analysis_pending", RetryAt: retryAt,
			}, nil
		}
		state.Message = repositories.MessageRepository.GetInTenant(sqls.DB(), job.MessageID, job.TenantID)
		if state.Message == nil {
			return AIReplyExecutionResult{}, fmt.Errorf("media message disappeared")
		}
		_, _, mediaStatus := utils.RuntimeMediaUnderstandingFromPayload(state.Message.Payload)
		switch strings.TrimSpace(mediaStatus) {
		case "failed":
			return AIReplyExecutionResult{}, errAIReplyMediaUnderstandingFailed
		case "empty":
			return AIReplyExecutionResult{Status: AIReplyExecutionStatusSkipped, ReasonCode: "media_understanding_empty"}, nil
		}
		if decision := s.inspectFreshness(state); decision != nil {
			return executionResultForDecision(*decision), nil
		}
		if !MediaUnderstandingService.mediaUnderstandingShouldTriggerAI(*state.Message) {
			return AIReplyExecutionResult{Status: AIReplyExecutionStatusSkipped, ReasonCode: "media_without_actionable_request"}, nil
		}
	} else if decision := s.inspectFreshness(state); decision != nil {
		return executionResultForDecision(*decision), nil
	}
	if job.TriggerKind == enums.AIReplyJobTriggerKindStandaloneOne {
		return StandaloneOneReplyService.Execute(ctx, state)
	}
	if TriggerAIReplySyncHook == nil {
		return AIReplyExecutionResult{}, fmt.Errorf("synchronous AI reply runtime unavailable")
	}
	return TriggerAIReplySyncHook(ctx, *state.Conversation, *state.Message)
}

var errAIReplyJobExpired = errors.New("AI reply job expired")

func (s *aiReplyJobService) prepareMedia(ctx context.Context, state *aiReplyJobExecutionState) (*time.Time, error) {
	if state == nil || state.Message == nil {
		return nil, fmt.Errorf("media execution state unavailable")
	}
	// 媒体理解必须独立于当前 job 的生命周期：客户常常发图后紧跟文字追问，
	// 文字消息会把本 media job supersede 并 cancel ctx；若媒体理解继承该 ctx，
	// vision 调用会被 context canceled 打断，导致图片永远"没加载出来"。
	// 这里剥离 cancel 但保留 values（requestID 等），再套独立超时。
	mediaCtx, mediaCancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer mediaCancel()
	runErr := MediaUnderstandingService.UnderstandInboundMessage(mediaCtx, state.Message.ID)
	analysis := repositories.MessageAnalysisRepository.GetLatestInTenant(sqls.DB(), state.Message.TenantID, state.Message.ID)
	if analysis == nil {
		if runErr != nil {
			return nil, runErr
		}
		return nil, fmt.Errorf("media analysis state unavailable")
	}
	now := time.Now()
	switch enums.NormalizeMessageAnalysisStatus(analysis.AnalysisStatus) {
	case enums.MessageAnalysisStatusReady:
		return nil, nil
	case enums.MessageAnalysisStatusPending, enums.MessageAnalysisStatusProcessing, enums.MessageAnalysisStatusFailedRetryable:
		retryAt := now.Add(aiReplyJobContinuationDelay)
		if analysis.NextRetryAt != nil && analysis.NextRetryAt.After(retryAt) {
			retryAt = *analysis.NextRetryAt
		}
		return &retryAt, nil
	case enums.MessageAnalysisStatusFailedTerminal, enums.MessageAnalysisStatusStale:
		return nil, errAIReplyMediaUnderstandingFailed
	default:
		if runErr != nil {
			return nil, runErr
		}
		return nil, errAIReplyMediaUnderstandingFailed
	}
}

func (s *aiReplyJobService) finishClaimed(job *models.AIReplyJob, owner string, result AIReplyExecutionResult, runErr error) {
	now := time.Now()
	// 任何 Task 状态写入前先重新检查轮次所有权。客户新消息会提升 Turn Version
	// 并取消旧 Context；旧 Job 即使稍后才从模型调用返回，也只能结束自己，绝不能
	// 把新版本仍要处理的 Task 写成 failed/handoff。
	if _, decision := s.inspectExecutionState(job, true); decision != nil {
		switch decision.Status {
		case enums.AIReplyJobStatusSkipped, enums.AIReplyJobStatusSuperseded:
			s.markTerminalWithCoverage(job, owner, decision.Status, decision.Code, "", decision.CoveredByMessageID, decision.CoveredByTaskID, now)
			return
		case enums.AIReplyJobStatusFailed:
			s.markTerminal(job, owner, decision.Status, decision.Code, "scope_invalid", now)
			return
		}
	}
	if result.Status == AIReplyExecutionStatusSuperseded || errors.Is(runErr, context.Canceled) {
		reason := controlledResultCode(result.ReasonCode, "runtime_cancelled")
		s.markTerminalWithCoverage(job, owner, enums.AIReplyJobStatusSuperseded, reason, "", result.CoveredByMessageID, result.CoveredByTaskID, now)
		return
	}
	if runErr == nil && result.Status == AIReplyExecutionStatusSkipped {
		if result.TaskLedgerEnabled {
			_ = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
				return AIReplyTurnTaskService.SkipNonTerminalBySourceMessageDB(
					ctx.Tx, job.TenantID, job.ConversationID, job.MessageID,
					controlledResultCode(result.ReasonCode, "runtime_skipped"), now,
				)
			})
		}
		s.markTerminal(job, owner, enums.AIReplyJobStatusSkipped, controlledResultCode(result.ReasonCode, "runtime_skipped"), "", now)
		return
	}
	if s.finishTaskLedgerOutcome(job, owner, result, runErr, now) {
		return
	}
	var dispatchCompleted *aiReplyJobDispatchCompleted
	if errors.As(runErr, &dispatchCompleted) {
		s.markTerminal(job, owner, enums.AIReplyJobStatusFailed, "model_failure_human_dispatch", controlledErrorClass(dispatchCompleted.errorClass), now)
		return
	}
	var dispatchOnlyErr *aiReplyJobDispatchOnlyError
	if errors.As(runErr, &dispatchOnlyErr) {
		_, _ = repositories.AIReplyJobRepository.MarkRetry(sqls.DB(), job.ID, job.TenantID, owner,
			"human_dispatch_retry", controlledErrorClass(job.LastErrorClass), now.Add(time.Minute), now, false)
		return
	}
	var terminalErr *aiReplyJobTerminalError
	if errors.As(runErr, &terminalErr) {
		s.markTerminal(job, owner, enums.AIReplyJobStatusFailed, controlledResultCode(terminalErr.code, "scope_invalid"), "scope_invalid", now)
		return
	}
	if errors.Is(runErr, errAIReplyJobExpired) {
		s.markTerminal(job, owner, enums.AIReplyJobStatusExpired, "expired_technical_terminal", "timeout", now)
		return
	}
	if runErr != nil {
		_, decision := s.inspectExecutionState(job, true)
		if decision != nil {
			switch decision.Status {
			case enums.AIReplyJobStatusCompleted:
				decisionResult := executionResultForDecision(*decision)
				if err := s.validateCompletionEvidence(job, decisionResult); err == nil {
					s.markTerminal(job, owner, decision.Status, decision.Code, "", now)
					return
				}
			case enums.AIReplyJobStatusSkipped, enums.AIReplyJobStatusSuperseded:
				s.markTerminalWithCoverage(job, owner, decision.Status, decision.Code, "", decision.CoveredByMessageID, decision.CoveredByTaskID, now)
				return
			case enums.AIReplyJobStatusFailed:
				s.markTerminal(job, owner, decision.Status, decision.Code, "scope_invalid", now)
				return
			}
		}
	}
	if runErr == nil {
		switch result.Status {
		case AIReplyExecutionStatusCompleted:
			if err := s.validateCompletionEvidence(job, result); err != nil {
				s.retryOrDispatch(job, owner, string(AIReplyExecutionErrorCommitFailed), now)
				return
			}
			s.markTerminal(job, owner, enums.AIReplyJobStatusCompleted, controlledResultCode(result.ReasonCode, "runtime_completed"), "", now)
		case AIReplyExecutionStatusSkipped:
			s.markTerminal(job, owner, enums.AIReplyJobStatusSkipped, controlledResultCode(result.ReasonCode, "runtime_skipped"), "", now)
		case AIReplyExecutionStatusSuperseded:
			s.markTerminalWithCoverage(job, owner, enums.AIReplyJobStatusSuperseded, controlledResultCode(result.ReasonCode, "newer_message"), "", result.CoveredByMessageID, result.CoveredByTaskID, now)
		case AIReplyExecutionStatusDeferred:
			next := now.Add(time.Second)
			if result.RetryAt != nil && result.RetryAt.After(now) {
				next = *result.RetryAt
			}
			_, _ = repositories.AIReplyJobRepository.MarkRetry(sqls.DB(), job.ID, job.TenantID, owner,
				controlledResultCode(result.ReasonCode, "runtime_deferred"), "", next, now, false)
		default:
			s.retryOrDispatch(job, owner, "runtime_result_invalid", now)
		}
		return
	}
	if code, ok := AIReplyExecutionErrorCodeOf(runErr); ok {
		if controlledExecutionErrorShouldRetry(runErr) {
			s.retryOrDispatch(job, owner, string(code), now)
		} else {
			s.dispatchControlledFailure(job, owner, string(code), now)
		}
		return
	}
	s.retryOrDispatch(job, owner, classifyAIReplyJobError(runErr), now)
}

func (s *aiReplyJobService) finishTaskLedgerOutcome(job *models.AIReplyJob, owner string, result AIReplyExecutionResult, runErr error, now time.Time) bool {
	if job == nil || job.TurnID <= 0 || !result.TaskLedgerEnabled || !AIReplyTurnTaskService.Enabled() {
		return false
	}
	if runErr == nil && result.Status == AIReplyExecutionStatusCompleted && len(result.HumanTaskKeys) > 0 {
		if err := s.validateCompletionEvidence(job, result); err != nil {
			return false
		}
		if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return AIReplyTurnTaskService.MarkTaskKeysHandoffDB(
				ctx.Tx, job.TenantID, job.TurnID, result.HumanTaskKeys, "human_route_requested", now,
			)
		}); err != nil {
			return false
		}
	}
	hasRunnable := result.HasRemainingTasks || AIReplyTurnTaskService.HasRunnable(job.TenantID, job.TurnID)
	hasActiveTasks := AIReplyTurnTaskService.HasUnfinished(job.TenantID, job.TurnID) &&
		!AIReplyTurnTaskService.HasFailureHandoffs(job.TenantID, job.TurnID)
	hasFailureHandoff := AIReplyTurnTaskService.HasFailureHandoffs(job.TenantID, job.TurnID)
	attemptCount := job.AttemptCount
	retryLimit := aiReplyJobMaxAttempts
	if runErr != nil {
		if current := repositories.AIReplyJobRepository.GetInTenant(sqls.DB(), job.ID, job.TenantID); current != nil {
			attemptCount = current.AttemptCount
		}
		retryLimit = aiReplyJobRetryAttemptLimit(classifyTaskFailure(runErr))
	}
	if hasActiveTasks && !hasFailureHandoff {
		failureClass := classifyTaskFailure(runErr)
		retryable := runErr == nil || controlledExecutionErrorShouldRetry(runErr) ||
			!failureClassAllowsHumanHandoff(failureClass)
		if runErr != nil && (!retryable || attemptCount >= retryLimit) {
			if failureClassAllowsHumanHandoff(failureClass) {
				if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
					if len(result.TaskKeys) > 0 {
						return AIReplyTurnTaskService.MarkHandoffPendingDB(
							ctx.Tx, job.TenantID, job.TurnID, job.ID, result.TaskKeys, failureClass, now,
						)
					}
					return AIReplyTurnTaskService.MarkUnfinishedHandoffPendingDB(
						ctx.Tx, job.TenantID, job.TurnID, job.ID, failureClass, now,
					)
				}); err != nil {
					return false
				}
				hasFailureHandoff = AIReplyTurnTaskService.HasFailureHandoffs(job.TenantID, job.TurnID)
			} else {
				// 防御性兜底：技术失败始终保留为可恢复 Task，不进入
				// handoff_pending，也不能因为一次未知错误永久封死。
				if err := s.markUnfinishedTasksTechnicalFailure(job, result.TaskKeys, failureClass, now); err != nil {
					return false
				}
				// 当前批次里未失败的 Task 仍可能处于 running/claimed。把它们释放回
				// pending，下面的 continuation 才能继续处理，不能随失败 Job 一起悬空。
				if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
					return AIReplyTurnTaskService.ReleaseJobClaimsDB(ctx.Tx, job.TenantID, job.TurnID, job.ID, now)
				}); err != nil {
					return false
				}
				hasRunnable = AIReplyTurnTaskService.HasRunnable(job.TenantID, job.TurnID)
				hasActiveTasks = AIReplyTurnTaskService.HasUnfinished(job.TenantID, job.TurnID) &&
					!AIReplyTurnTaskService.HasFailureHandoffs(job.TenantID, job.TurnID)
			}
		} else {
			nextRetryAt := now.Add(aiReplyJobContinuationDelay)
			if taskRetryAt := AIReplyTurnTaskService.NextRetryAt(job.TenantID, job.TurnID); taskRetryAt != nil && taskRetryAt.After(nextRetryAt) {
				nextRetryAt = *taskRetryAt
			}
			consumeAttempt := runErr != nil
			if consumeAttempt && attemptCount < retryLimit {
				modelRetryAt := now.Add(aiReplyJobRetryDelayFor(classifyTaskFailure(runErr), attemptCount-1))
				if modelRetryAt.After(nextRetryAt) {
					nextRetryAt = modelRetryAt
				}
			}
			_, _ = repositories.AIReplyJobRepository.MarkRetry(
				sqls.DB(), job.ID, job.TenantID, owner,
				"turn_tasks_remaining", classifyTaskFailure(runErr), nextRetryAt, now, consumeAttempt,
			)
			return true
		}
	}

	if hasRunnable && !hasFailureHandoff {
		if result.Status == AIReplyExecutionStatusCompleted {
			if err := s.validateCompletionEvidence(job, result); err != nil {
				return false
			}
		}
		// 续批延迟由任务层状态驱动：任务都在退避等待时，等最早可执行时间，
		// 不做 25ms 无进展空转（文档第 15 节：不允许无进展空转，续批由状态变化触发）。
		nextRetryAt := now.Add(aiReplyJobContinuationDelay)
		if taskRetryAt := AIReplyTurnTaskService.NextRetryAt(job.TenantID, job.TurnID); taskRetryAt != nil && taskRetryAt.After(nextRetryAt) {
			nextRetryAt = *taskRetryAt
		}
		_, _ = repositories.AIReplyJobRepository.MarkRetry(
			sqls.DB(), job.ID, job.TenantID, owner,
			"turn_tasks_remaining", classifyTaskFailure(runErr), nextRetryAt, now, false,
		)
		return true
	}

	hasFailureHandoff = hasFailureHandoff || AIReplyTurnTaskService.HasFailureHandoffs(job.TenantID, job.TurnID)
	if !hasFailureHandoff {
		return false
	}
	current := repositories.AIReplyJobRepository.GetInTenant(sqls.DB(), job.ID, job.TenantID)
	if current == nil || current.Status != enums.AIReplyJobStatusProcessing || current.LeaseOwner != owner {
		return true
	}
	state, decision := s.inspectExecutionState(current, false)
	if decision != nil {
		s.markTerminalWithCoverage(current, owner, decision.Status, decision.Code, classifyTaskFailure(runErr), decision.CoveredByMessageID, decision.CoveredByTaskID, now)
		return true
	}
	if state == nil {
		s.markTerminal(current, owner, enums.AIReplyJobStatusFailed, "scope_invalid", "scope_invalid", now)
		return true
	}
	if !failureClassAllowsHumanHandoff(classifyTaskFailure(runErr)) {
		slog.Warn("ai reply partial failure kept technical without handoff",
			"job_id", current.ID, "blocked_transition", "handoff_technical_failure_blocked")
		s.markTerminal(current, owner, enums.AIReplyJobStatusFailed, "technical_failure_no_handoff", classifyTaskFailure(runErr), now)
		s.sendTechnicalFailureNotice(state, current)
		return true
	}
	if err := s.dispatchHuman(state, current, "AI 部分问题自动处理失败，需要人工跟进"); err != nil {
		_, _ = repositories.AIReplyJobRepository.MarkRetry(
			sqls.DB(), current.ID, current.TenantID, owner,
			"human_dispatch_retry", classifyTaskFailure(runErr), now.Add(time.Minute), now, false,
		)
		return true
	}
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return AIReplyTurnTaskService.MarkPendingHandoffsDB(ctx.Tx, current.TenantID, current.TurnID, "human_handoff", now)
	}); err != nil {
		_, _ = repositories.AIReplyJobRepository.MarkRetry(
			sqls.DB(), current.ID, current.TenantID, owner,
			"human_dispatch_retry", "database_error", now.Add(time.Minute), now, false,
		)
		return true
	}
	if result.Status == AIReplyExecutionStatusCompleted && s.validateCompletionEvidence(current, result) == nil {
		s.markTerminal(current, owner, enums.AIReplyJobStatusCompleted, "partial_success_human_dispatch", classifyTaskFailure(runErr), now)
		return true
	}
	s.markTerminal(
		current, owner, enums.AIReplyJobStatusFailed,
		controlledResultCode(classifyTaskFailure(runErr)+"_human_dispatch", "task_failure_human_dispatch"),
		classifyTaskFailure(runErr), now,
	)
	return true
}

func controlledExecutionErrorShouldRetry(err error) bool {
	code, ok := AIReplyExecutionErrorCodeOf(err)
	if !ok {
		return false
	}
	// All controlled runtime errors are technical execution failures. Even when
	// an individual provider response is marked non-retryable (for example an
	// invalid JSON response), a later durable Job attempt can succeed after the
	// model, configuration, or application is repaired. The Task remains the
	// source of truth, so retries do not repeat committed answers.
	switch code {
	case AIReplyExecutionErrorIntentDetectFailed,
		AIReplyExecutionErrorGenerationFailed,
		AIReplyExecutionErrorEmptyOutput,
		AIReplyExecutionErrorKnowledgeUnavailable,
		AIReplyExecutionErrorResourceInvariantBroken,
		AIReplyExecutionErrorCommitFailed:
		return true
	default:
		return false
	}
}

func aiReplyJobRetryAttemptLimit(errorClass string) int {
	if !failureClassAllowsHumanHandoff(errorClass) {
		return int(^uint(0) >> 1)
	}
	switch strings.TrimSpace(errorClass) {
	case string(AIReplyExecutionErrorIntentDetectFailed),
		string(AIReplyExecutionErrorGenerationFailed),
		string(AIReplyExecutionErrorEmptyOutput):
		return aiReplyJobModelRecoveryMaxAttempts
	default:
		return aiReplyJobMaxAttempts
	}
}

func aiReplyJobPersistentTechnicalRetry(job *models.AIReplyJob) bool {
	if job == nil {
		return false
	}
	if job.TriggerKind == enums.AIReplyJobTriggerKindMedia {
		analysis := repositories.MessageAnalysisRepository.GetLatestInTenant(sqls.DB(), job.TenantID, job.MessageID)
		if analysis == nil {
			return true
		}
		switch enums.NormalizeMessageAnalysisStatus(analysis.AnalysisStatus) {
		case enums.MessageAnalysisStatusPending,
			enums.MessageAnalysisStatusProcessing,
			enums.MessageAnalysisStatusFailedRetryable,
			enums.MessageAnalysisStatusReady:
			return true
		default:
			return false
		}
	}
	// 服务停机或队列拥塞可能让文本 Job 在首次领取前超过旧的 15 分钟窗口。
	// 只要它从未执行过，就至少运行一次；更新消息、人工回复和 Turn 终态仍由
	// inspectFreshness/inspectExecutionState 精确拦截。媒体 Job 则以上面的
	// Analysis 权威状态为准。
	if job.AttemptCount == 1 {
		return true
	}
	if strings.TrimSpace(job.LastErrorClass) == "" {
		return false
	}
	return !failureClassAllowsHumanHandoff(job.LastErrorClass)
}

// failureClassAllowsHumanHandoff 契约 22.16：技术失败（协议/网络/数据库/
// 内容/知识/范围）绝不允许进入人工兜底；只有业务能力路由或安全政策明确
// 要求时才可以派单。违反迁移记录 handoff_technical_failure_blocked。
func failureClassAllowsHumanHandoff(errorClass string) bool {
	switch NormalizeAIReplyFailureClass(errorClass) {
	case FailureBusiness, FailureSafety:
		return true
	default:
		return false
	}
}

func classifyTaskFailure(err error) string {
	if code, ok := AIReplyExecutionErrorCodeOf(err); ok {
		return controlledErrorClass(string(code))
	}
	if err != nil {
		return controlledErrorClass(classifyAIReplyJobError(err))
	}
	return "knowledge_unavailable"
}

func (s *aiReplyJobService) dispatchControlledFailure(job *models.AIReplyJob, owner, errorClass string, now time.Time) {
	current := repositories.AIReplyJobRepository.GetInTenant(sqls.DB(), job.ID, job.TenantID)
	if current == nil || current.Status != enums.AIReplyJobStatusProcessing || current.LeaseOwner != owner {
		return
	}
	state, decision := s.inspectExecutionState(current, true)
	if decision != nil {
		switch decision.Status {
		case enums.AIReplyJobStatusCompleted:
			result := executionResultForDecision(*decision)
			if s.validateCompletionEvidence(current, result) == nil {
				s.markTerminal(current, owner, decision.Status, decision.Code, "", now)
				return
			}
		case enums.AIReplyJobStatusSkipped, enums.AIReplyJobStatusSuperseded, enums.AIReplyJobStatusFailed:
			s.markTerminal(current, owner, decision.Status, decision.Code, errorClass, now)
			return
		}
	}
	if state == nil {
		s.markTerminal(current, owner, enums.AIReplyJobStatusFailed, "scope_invalid", "scope_invalid", now)
		return
	}
	// 契约 22.16：受控失败同样按失败类别门禁——generation_failed 等技术类
	// 失败不得触发人工派单（生产 1473 场景根因：本路径漏设闸）。
	if !failureClassAllowsHumanHandoff(controlledErrorClass(errorClass)) {
		slog.Warn("controlled model failure kept technical without handoff",
			"job_id", current.ID, "error_class", errorClass, "blocked_transition", "handoff_technical_failure_blocked")
		s.markTerminal(current, owner, enums.AIReplyJobStatusFailed, "technical_failure_no_handoff", controlledErrorClass(errorClass), now)
		s.sendTechnicalFailureNotice(state, current)
		return
	}
	if err := s.dispatchHuman(state, current, "AI 自动回复失败，需要人工跟进"); err != nil {
		_, _ = repositories.AIReplyJobRepository.MarkRetry(sqls.DB(), current.ID, current.TenantID, owner,
			"human_dispatch_retry", controlledErrorClass(errorClass), now.Add(time.Minute), now, false)
		return
	}
	s.markTerminal(current, owner, enums.AIReplyJobStatusFailed, controlledResultCode(errorClass+"_human_dispatch", "model_failure_human_dispatch"), errorClass, now)
}

func (s *aiReplyJobService) retryOrDispatch(job *models.AIReplyJob, owner, errorClass string, now time.Time) {
	current := repositories.AIReplyJobRepository.GetInTenant(sqls.DB(), job.ID, job.TenantID)
	if current == nil || current.Status != enums.AIReplyJobStatusProcessing || current.LeaseOwner != owner {
		return
	}
	if current.AttemptCount < aiReplyJobRetryAttemptLimit(errorClass) {
		delay := aiReplyJobRetryDelayFor(errorClass, current.AttemptCount-1)
		_, _ = repositories.AIReplyJobRepository.MarkRetry(sqls.DB(), current.ID, current.TenantID, owner,
			"runtime_retry", errorClass, now.Add(delay), now, true)
		return
	}
	state, decision := s.inspectExecutionState(current, true)
	if decision != nil {
		s.markTerminal(current, owner, decision.Status, decision.Code, errorClass, now)
		return
	}
	if state == nil {
		s.markTerminal(current, owner, enums.AIReplyJobStatusFailed, "scope_invalid", "scope_invalid", now)
		return
	}
	if !failureClassAllowsHumanHandoff(errorClass) {
		// 契约 22.16：技术失败耗尽预算后进入确定性终态并告警，
		// 不伪装成“客户需要人工”。
		slog.Warn("ai reply job technical failure exhausted without handoff",
			"job_id", current.ID, "error_class", errorClass, "blocked_transition", "handoff_technical_failure_blocked")
		s.markTerminal(current, owner, enums.AIReplyJobStatusFailed, "technical_failure_no_handoff", errorClass, now)
		s.sendTechnicalFailureNotice(state, current)
		return
	}
	if err := s.dispatchHuman(state, current, "AI 自动回复连续失败，需要人工跟进"); err != nil {
		_, _ = repositories.AIReplyJobRepository.MarkRetry(sqls.DB(), current.ID, current.TenantID, owner,
			"human_dispatch_retry", "human_dispatch_failed", now.Add(time.Minute), now, false)
		return
	}
	s.markTerminal(current, owner, enums.AIReplyJobStatusFailed, "retry_exhausted_human_dispatch", errorClass, now)
}

// markUnfinishedTasksTechnicalFailure 把未完成 Task 逐个释放为技术退避状态，
// 不创建 handoff_pending，也不把可恢复问题写成永久 failed。
func (s *aiReplyJobService) markUnfinishedTasksTechnicalFailure(job *models.AIReplyJob, taskKeys []string, failureClass string, now time.Time) error {
	selected := make(map[string]struct{}, len(taskKeys))
	for _, taskKey := range taskKeys {
		if taskKey = strings.TrimSpace(taskKey); taskKey != "" {
			selected[taskKey] = struct{}{}
		}
	}
	tasks := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(sqls.DB(), job.TenantID, job.TurnID)
	for index := range tasks {
		task := &tasks[index]
		if task.Status == enums.AIReplyTurnTaskStatusHandoffPending || aiReplyTurnTaskTerminal(task.Status) {
			continue
		}
		if len(selected) > 0 {
			if _, ok := selected[task.TaskKey]; !ok {
				continue
			}
		} else if task.ClaimedByJobID != job.ID || task.ClaimedVersion != job.TurnVersion {
			continue
		}
		if task.ClaimedByJobID != 0 && task.ClaimedByJobID != job.ID {
			continue
		}
		if err := AIReplyTurnTaskService.MarkTechnicalFailureDB(sqls.DB(), &models.AIReplyTurn{
			ID: job.TurnID, TenantID: job.TenantID, ConversationID: job.ConversationID, SessionNo: job.SessionNo,
		}, task.TaskKey, failureClass, int(^uint(0)>>1), now); err != nil {
			return err
		}
	}
	return nil
}

// sendTechnicalFailureNotice 只记录内部告警。技术故障不是客户意图，也不是
// “资料库无内容”；向客户发送固定失败话术会覆盖原问题并污染下一轮上下文。
func (s *aiReplyJobService) sendTechnicalFailureNotice(state *aiReplyJobExecutionState, job *models.AIReplyJob) {
	if state == nil || state.Conversation == nil || job == nil {
		return
	}
	slog.Warn("ai reply technical failure kept internal",
		"job_id", job.ID,
		"tenant_id", job.TenantID,
		"conversation_id", state.Conversation.ID,
		"error_class", controlledErrorClass(job.LastErrorClass),
	)
}

func (s *aiReplyJobService) markTerminal(job *models.AIReplyJob, owner string, status enums.AIReplyJobStatus, resultCode, errorClass string, now time.Time) {
	s.markTerminalWithCoverage(job, owner, status, resultCode, errorClass, 0, 0, now)
}

func (s *aiReplyJobService) markTerminalWithCoverage(
	job *models.AIReplyJob,
	owner string,
	status enums.AIReplyJobStatus,
	resultCode, errorClass string,
	coveredByMessageID, coveredByTaskID int64,
	now time.Time,
) {
	_, err := repositories.AIReplyJobRepository.MarkTerminal(
		sqls.DB(), job.ID, job.TenantID, owner, status,
		controlledResultCode(resultCode, "unknown"), controlledErrorClass(errorClass),
		coveredByMessageID, coveredByTaskID, now,
	)
	if err != nil {
		slog.Warn("finish AI reply job failed", "job_id", job.ID, "stage", "finalize", "error_class", "database_error")
	}
}

func (s *aiReplyJobService) dispatchToExistingHumanPool(state *aiReplyJobExecutionState, job *models.AIReplyJob, reason string) error {
	if state == nil || state.Conversation == nil || job == nil {
		return fmt.Errorf("human dispatch scope unavailable")
	}
	aiAgent, ok := WxWorkProtocolInstanceService.BuildRuntimeAIAgentForConversation(state.Conversation.ID)
	if !ok || aiAgent.TenantID != state.Conversation.TenantID {
		return fmt.Errorf("human dispatch AI agent unavailable")
	}
	requestID := s.stableHandoffRequestID(job)
	// 技术失败兜底也必须先向客户二次确认，而不是直接转人工；
	// 确认后才真正进入人工池，避免“模型一抖就直接转人工”。
	_, err := ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(
		state.Conversation.ID, aiAgent, reason, requestID, state.Message.ID,
	)
	return err
}

// stableHandoffRequestID 生成文档第 10 节要求的稳定派单键：
// ai_handoff_<tenantID>_<conversationID>_<turnID>_<failedTaskFingerprint>。
// 同一 Turn 内失败任务集合不变时，重复派单命中 handoffAlreadyRecordedDB 幂等短路，
// 不产生第二条派单；失败任务集合变化时才允许新派单。
func (s *aiReplyJobService) stableHandoffRequestID(job *models.AIReplyJob) string {
	if job == nil || job.TenantID <= 0 || job.ConversationID <= 0 {
		if job != nil {
			return fmt.Sprintf("ai_reply_job_handoff_%d", job.ID)
		}
		return "ai_reply_job_handoff_0"
	}
	fingerprint := "none"
	if job.TurnID > 0 && AIReplyTurnTaskService.Enabled() {
		if tasks := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(sqls.DB(), job.TenantID, job.TurnID); len(tasks) > 0 {
			keys := make([]string, 0, len(tasks))
			for _, task := range tasks {
				if task.Status == enums.AIReplyTurnTaskStatusHandoffPending || task.Status == enums.AIReplyTurnTaskStatusFailed {
					keys = append(keys, task.TaskKey)
				}
			}
			sort.Strings(keys)
			if len(keys) > 0 {
				sum := sha256.Sum256([]byte(strings.Join(keys, ",")))
				fingerprint = hex.EncodeToString(sum[:8])
			}
		}
	}
	if fingerprint == "none" {
		return fmt.Sprintf("ai_reply_job_handoff_%d", job.ID)
	}
	return fmt.Sprintf("ai_handoff_%d_%d_%d_%s", job.TenantID, job.ConversationID, job.TurnID, fingerprint)
}

func (s *aiReplyJobService) dispatchHuman(state *aiReplyJobExecutionState, job *models.AIReplyJob, reason string) error {
	if s.humanDispatch != nil {
		return s.humanDispatch(state, job, reason)
	}
	return s.dispatchToExistingHumanPool(state, job, reason)
}

func (s *aiReplyJobService) inspectExecutionState(job *models.AIReplyJob, includeFreshness bool) (*aiReplyJobExecutionState, *aiReplyJobDecision) {
	if job == nil || job.ID <= 0 || job.TenantID <= 0 || job.ConversationID <= 0 || job.MessageID <= 0 {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: "scope_invalid"}
	}
	db := sqls.DB()
	conversation := repositories.ConversationRepository.GetInTenant(db, job.ConversationID, job.TenantID)
	message := repositories.MessageRepository.GetInTenant(db, job.MessageID, job.TenantID)
	if conversation == nil || message == nil || message.ConversationID != conversation.ID ||
		conversation.StoreID != job.StoreID || conversation.StoreStaffBindingID != job.StoreStaffBindingID ||
		message.SessionNo != job.SessionNo || strings.TrimSpace(message.RequestID) != strings.TrimSpace(job.RequestID) {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: "scope_invalid"}
	}
	standaloneOne := job.TriggerKind == enums.AIReplyJobTriggerKindStandaloneOne
	if standaloneOne && (job.TurnID > 0 || job.TurnVersion > 0 || message.AIReplyTurnID > 0 || message.AIReplyTurnVersion > 0) {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: "standalone_one_turn_bound"}
	}
	if job.TurnID > 0 || job.TurnVersion > 0 || message.AIReplyTurnID > 0 || message.AIReplyTurnVersion > 0 {
		turn, turnCode := AIReplyTurnService.GetForJob(job, message)
		if turn == nil {
			return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: turnCode}
		}
		if aiReplyTurnTerminalStatus(turn.Status) || conversation.CurrentAIReplyTurnID != turn.ID {
			return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusSuperseded, Code: "turn_inactive"}
		}
		if job.TurnVersion < turn.Version {
			return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusSuperseded, Code: "stale_turn_version"}
		}
	}
	if message.HistoricalOnly || message.SenderType != enums.IMSenderTypeCustomer {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusSkipped, Code: "message_not_runtime_eligible"}
	}
	if message.RecalledAt != nil || message.SendStatus == enums.IMMessageStatusRecalled || message.SendStatus == enums.IMMessageStatusFailed {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusSkipped, Code: "message_unavailable"}
	}
	if job.StoreID <= 0 || job.StoreStaffBindingID <= 0 {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: "scope_invalid"}
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, conversation.TenantID)
	if route == nil || route.TenantID != conversation.TenantID || route.StoreID != conversation.StoreID ||
		route.StoreStaffBindingID != conversation.StoreStaffBindingID || route.WxWorkInstanceID <= 0 {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: "route_scope_invalid"}
	}
	if route.SessionNo != message.SessionNo {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusSuperseded, Code: "session_changed"}
	}
	if decision := ConversationRuntimeModeService.ResolveDB(db, conversation, route); !decision.AIReplyAllowed {
		if !standaloneOne || decision.ReasonCode != "human_handoff_pending" {
			return nil, aiReplyJobDecisionForRuntimeMode(decision)
		}
	}
	session := repositories.ConversationChannelSessionRepository.TakeByConversationSession(db, conversation.TenantID, conversation.ID, message.SessionNo)
	if session == nil || session.TenantID != conversation.TenantID || session.StoreID != conversation.StoreID ||
		session.StoreStaffBindingID != conversation.StoreStaffBindingID || session.WxWorkInstanceID != route.WxWorkInstanceID {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: "session_scope_invalid"}
	}
	if session.Status != enums.StatusOk || session.EndedAt != nil {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusSuperseded, Code: "session_inactive"}
	}
	if !standaloneOne {
		if _, err := StoreModelCredentialService.requireStoreStaffCredentialScopeDB(db, conversation.TenantID, conversation.StoreID, conversation.StoreStaffBindingID, true); err != nil {
			return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: "binding_scope_invalid"}
		}
	}
	instance, err := WxWorkProtocolInstanceService.activeInstanceForBindingDB(db, conversation.TenantID, conversation.StoreStaffBindingID)
	if err != nil || instance == nil || instance.ID != route.WxWorkInstanceID || instance.TenantID != conversation.TenantID ||
		instance.StoreID != conversation.StoreID || instance.StoreStaffBindingID != conversation.StoreStaffBindingID {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: "instance_scope_invalid"}
	}
	if !instance.AIReplyEnabled {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusSkipped, Code: "ai_reply_disabled"}
	}
	state := &aiReplyJobExecutionState{Job: job, Conversation: conversation, Message: message, Route: route, Session: session, Instance: instance}
	if includeFreshness {
		if decision := s.inspectFreshness(state); decision != nil {
			return nil, decision
		}
	}
	return state, nil
}

func aiReplyJobDecisionForRuntimeMode(decision ConversationRuntimeModeDecision) *aiReplyJobDecision {
	switch decision.Mode {
	case enums.ConversationRuntimeModeClosed:
		return &aiReplyJobDecision{Status: enums.AIReplyJobStatusSkipped, Code: "conversation_closed"}
	case enums.ConversationRuntimeModeHumanActive:
		if decision.ReasonCode == "human_assignee_active" {
			return &aiReplyJobDecision{Status: enums.AIReplyJobStatusSkipped, Code: "human_agent_serving"}
		}
		return &aiReplyJobDecision{Status: enums.AIReplyJobStatusSkipped, Code: "manual_or_closed_route"}
	case enums.ConversationRuntimeModeHumanPending, enums.ConversationRuntimeModeResumePending:
		return &aiReplyJobDecision{Status: enums.AIReplyJobStatusSkipped, Code: "manual_or_closed_route"}
	case enums.ConversationRuntimeModeAIDegraded:
		if decision.ReasonCode == "ai_reply_disabled" || decision.ReasonCode == "ai_service_mode_disabled" {
			return &aiReplyJobDecision{Status: enums.AIReplyJobStatusSkipped, Code: "ai_reply_disabled"}
		}
		code := strings.TrimSpace(decision.ReasonCode)
		if code == "" {
			code = "runtime_mode_invalid"
		}
		return &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: code}
	default:
		return &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: "runtime_mode_invalid"}
	}
}

func (s *aiReplyJobService) inspectFreshness(state *aiReplyJobExecutionState) *aiReplyJobDecision {
	if state == nil || state.Job == nil || state.Message == nil || state.Conversation == nil {
		return &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: "scope_invalid"}
	}
	committed := repositories.MessageRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", state.Job.TenantID).
		Eq("conversation_id", state.Job.ConversationID).
		Eq("sender_type", enums.IMSenderTypeAI).
		Eq("request_id", state.Job.RequestID).
		Eq("session_no", state.Job.SessionNo).
		Gt("id", state.Job.MessageID).
		Asc("id"))
	committedIDs := make([]int64, 0, len(committed))
	for _, message := range committed {
		if isStableRuntimeAIClientMsgID(message.ClientMsgID) && message.SendStatus != enums.IMMessageStatusFailed &&
			message.SendStatus != enums.IMMessageStatusRecalled && message.RecalledAt == nil {
			committedIDs = append(committedIDs, message.ID)
		}
	}
	taskLedgerUnfinished := state.Job.TurnID > 0 && AIReplyTurnTaskService.Enabled() &&
		AIReplyTurnTaskService.HasUnfinished(state.Job.TenantID, state.Job.TurnID)
	if len(committedIDs) > 0 && !taskLedgerUnfinished {
		return &aiReplyJobDecision{Status: enums.AIReplyJobStatusCompleted, Code: "reply_already_committed", CommittedMessageIDs: committedIDs}
	}
	if interrupt := repositories.ConversationInterruptRepository.FindOne(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", state.Job.TenantID).
		Eq("conversation_id", state.Job.ConversationID).
		Eq("source_message_id", state.Job.MessageID).
		Desc("id")); interrupt != nil && interrupt.ID > 0 && strings.TrimSpace(interrupt.Status) != "checkpointed" {
		return &aiReplyJobDecision{Status: enums.AIReplyJobStatusCompleted, Code: "interrupt_already_persisted", PersistedInterruptID: interrupt.ID}
	}
	if state.Job.TurnID > 0 {
		turn, turnCode := AIReplyTurnService.GetForJob(state.Job, state.Message)
		if turn == nil {
			return &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: turnCode}
		}
		if coverage := AIReplyTurnService.FindCoverage(state.Job, state.Message, turn); coverage != nil {
			return &aiReplyJobDecision{
				Status: enums.AIReplyJobStatusSuperseded, Code: coverage.ReasonCode,
				CoveredByMessageID: coverage.CoveredByMessageID,
			}
		}
	}
	standaloneOne := state.Job.TriggerKind == enums.AIReplyJobTriggerKindStandaloneOne
	newer := repositories.MessageRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", state.Job.TenantID).
		Eq("conversation_id", state.Job.ConversationID).
		Eq("session_no", state.Job.SessionNo).
		Gt("id", state.Job.MessageID).
		Where("recalled_at IS NULL AND send_status NOT IN (?, ?)", enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Asc("id"))
	for _, message := range newer {
		switch message.SenderType {
		case enums.IMSenderTypeCustomer:
			if standaloneOne || isStandaloneOneCustomerMessage(&message) {
				continue
			}
			if state.Job.TurnID <= 0 || message.AIReplyTurnID != state.Job.TurnID || message.SessionNo != state.Job.SessionNo {
				return &aiReplyJobDecision{Status: enums.AIReplyJobStatusSuperseded, Code: "newer_customer_message"}
			}
			if message.AIReplyTurnVersion > state.Job.TurnVersion {
				return &aiReplyJobDecision{Status: enums.AIReplyJobStatusSuperseded, Code: "stale_turn_version"}
			}
		case enums.IMSenderTypeAgent:
			if standaloneOne {
				continue
			}
			return &aiReplyJobDecision{Status: enums.AIReplyJobStatusSkipped, Code: "human_agent_replied"}
		}
	}
	return nil
}

func (s *aiReplyJobService) validateCompletionEvidence(job *models.AIReplyJob, result AIReplyExecutionResult) error {
	if job == nil || job.ID <= 0 || job.TenantID <= 0 {
		return NewAIReplyExecutionError(AIReplyExecutionErrorCommitFailed, fmt.Errorf("job scope unavailable"))
	}
	if len(result.CommittedMessageIDs) == 0 && result.PersistedInterruptID <= 0 {
		return NewAIReplyExecutionError(AIReplyExecutionErrorCommitFailed, fmt.Errorf("durable completion evidence missing"))
	}
	seen := make(map[int64]struct{}, len(result.CommittedMessageIDs))
	for _, messageID := range result.CommittedMessageIDs {
		if messageID <= 0 {
			return NewAIReplyExecutionError(AIReplyExecutionErrorCommitFailed, fmt.Errorf("invalid committed message evidence"))
		}
		if _, exists := seen[messageID]; exists {
			continue
		}
		seen[messageID] = struct{}{}
		message := repositories.MessageRepository.GetInTenant(sqls.DB(), messageID, job.TenantID)
		if message == nil || message.ConversationID != job.ConversationID || message.SessionNo != job.SessionNo ||
			message.ID <= job.MessageID || message.SenderType != enums.IMSenderTypeAI ||
			strings.TrimSpace(message.RequestID) != strings.TrimSpace(job.RequestID) ||
			!isStableRuntimeAIClientMsgID(message.ClientMsgID) || message.RecalledAt != nil ||
			message.SendStatus == enums.IMMessageStatusFailed || message.SendStatus == enums.IMMessageStatusRecalled {
			return NewAIReplyExecutionError(AIReplyExecutionErrorCommitFailed, fmt.Errorf("committed message evidence scope mismatch"))
		}
	}
	if result.PersistedInterruptID > 0 {
		interrupt := repositories.ConversationInterruptRepository.Get(sqls.DB(), result.PersistedInterruptID)
		if interrupt == nil || interrupt.TenantID != job.TenantID || interrupt.ConversationID != job.ConversationID ||
			interrupt.SourceMessageID != job.MessageID || strings.TrimSpace(interrupt.Status) == "" ||
			strings.TrimSpace(interrupt.Status) == "checkpointed" {
			return NewAIReplyExecutionError(AIReplyExecutionErrorCommitFailed, fmt.Errorf("interrupt evidence scope mismatch"))
		}
	}
	return nil
}

func isStableRuntimeAIClientMsgID(clientMsgID string) bool {
	clientMsgID = strings.TrimSpace(clientMsgID)
	for _, prefix := range []string{
		"ai_reply_", "ai_interrupt_", "ai_interrupt_resume_", "ai_interrupt_expired_",
		"ai_resume_", "ai_handoff_confirm_",
	} {
		if strings.HasPrefix(clientMsgID, prefix) && len(clientMsgID) > len(prefix) {
			return true
		}
	}
	if len(clientMsgID) != 48 {
		return false
	}
	for _, char := range clientMsgID {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func (s *aiReplyJobService) ValidateRuntimeCheckpoint(ctx context.Context, conversation models.Conversation, message models.Message) (AIReplyExecutionResult, error) {
	lease, ok := ctx.Value(aiReplyJobLeaseContextKey{}).(aiReplyJobLeaseContext)
	if !ok {
		return AIReplyExecutionResult{Status: AIReplyExecutionStatusCompleted, ReasonCode: "checkpoint_valid"}, nil
	}
	if err := ctx.Err(); err != nil {
		return AIReplyExecutionResult{Status: AIReplyExecutionStatusDeferred, ReasonCode: "context_cancelled"}, err
	}
	job := repositories.AIReplyJobRepository.GetInTenant(sqls.DB(), lease.JobID, lease.TenantID)
	if job == nil || job.Status != enums.AIReplyJobStatusProcessing || job.LeaseOwner != lease.Owner ||
		job.ConversationID != conversation.ID || job.MessageID != message.ID || job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(time.Now()) {
		return AIReplyExecutionResult{Status: AIReplyExecutionStatusDeferred, ReasonCode: "lease_lost"}, context.Canceled
	}
	if retryAt, held := WxWorkKFMessageRefService.ActiveOutboundReconciliationHold(job.TenantID, job.ConversationID, time.Now()); held {
		return AIReplyExecutionResult{Status: AIReplyExecutionStatusDeferred, ReasonCode: "unknown_outbound_reconciliation_hold", RetryAt: retryAt}, nil
	}
	_, decision := s.inspectExecutionState(job, true)
	if decision == nil {
		return AIReplyExecutionResult{Status: AIReplyExecutionStatusCompleted, ReasonCode: "checkpoint_valid"}, nil
	}
	if decision.Status == enums.AIReplyJobStatusFailed {
		return AIReplyExecutionResult{}, &aiReplyJobTerminalError{code: decision.Code}
	}
	return executionResultForDecision(*decision), nil
}

func executionResultForDecision(decision aiReplyJobDecision) AIReplyExecutionResult {
	switch decision.Status {
	case enums.AIReplyJobStatusCompleted:
		return AIReplyExecutionResult{
			Status: AIReplyExecutionStatusCompleted, ReasonCode: decision.Code,
			CommittedMessageIDs:  append([]int64(nil), decision.CommittedMessageIDs...),
			PersistedInterruptID: decision.PersistedInterruptID,
		}
	case enums.AIReplyJobStatusSkipped:
		return AIReplyExecutionResult{Status: AIReplyExecutionStatusSkipped, ReasonCode: decision.Code}
	case enums.AIReplyJobStatusSuperseded:
		return AIReplyExecutionResult{
			Status: AIReplyExecutionStatusSuperseded, ReasonCode: decision.Code,
			CoveredByMessageID: decision.CoveredByMessageID,
			CoveredByTaskID:    decision.CoveredByTaskID,
		}
	default:
		return AIReplyExecutionResult{Status: AIReplyExecutionStatusSkipped, ReasonCode: decision.Code}
	}
}

func classifyAIReplyJobError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "runtime_timeout"
	case errors.Is(err, context.Canceled):
		return "runtime_cancelled"
	case errors.Is(err, errAIReplyMediaUnderstandingFailed):
		return "media_understanding_failed"
	default:
		return "runtime_error"
	}
}

func controlledResultCode(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 80 {
		return fallback
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return fallback
		}
	}
	return value
}

func controlledErrorClass(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return controlledResultCode(value, "runtime_error")
}

func isTerminalAIReplyJobStatus(status enums.AIReplyJobStatus) bool {
	switch status {
	case enums.AIReplyJobStatusCompleted, enums.AIReplyJobStatusSkipped, enums.AIReplyJobStatusSuperseded,
		enums.AIReplyJobStatusExpired, enums.AIReplyJobStatusFailed:
		return true
	default:
		return false
	}
}
