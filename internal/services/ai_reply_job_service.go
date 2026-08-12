package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	aiReplyJobLifetime          = 15 * time.Minute
	aiReplyJobInitialDelay      = 250 * time.Millisecond
	aiReplyJobLeaseDuration     = 90 * time.Second
	aiReplyJobRenewInterval     = 30 * time.Second
	aiReplyJobContinuationDelay = 25 * time.Millisecond
	aiReplyJobMaxAttempts       = 4
	aiReplyJobMaxConcurrency    = 4
)

var aiReplyJobRetryDelays = []time.Duration{15 * time.Second, time.Minute, 3 * time.Minute}

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
	s.activeMu.Lock()
	active := s.activeExecutions[conversationID]
	cancels := make([]context.CancelFunc, 0, len(active))
	newMessage := repositories.MessageRepository.Get(sqls.DB(), messageID)
	for activeMessageID, cancel := range active {
		if activeMessageID >= messageID || cancel == nil {
			continue
		}
		activeMessage := repositories.MessageRepository.Get(sqls.DB(), activeMessageID)
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
	s.processClaimed(job, owner)
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
			if turn := repositories.AIReplyTurnRepository.GetInTenant(sqls.DB(), job.TurnID, job.TenantID); turn != nil &&
				job.TurnVersion > 0 && job.TurnVersion < turn.Version {
				s.markTerminal(job, owner, enums.AIReplyJobStatusSuperseded, "stale_turn_version", "", time.Now())
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
	if time.Now().After(job.ExpiresAt) {
		if decision := s.inspectFreshness(state); decision != nil {
			return executionResultForDecision(*decision), nil
		}
		if err := s.dispatchHuman(state, job, "AI 回复任务超过等待时限"); err != nil {
			return AIReplyExecutionResult{}, err
		}
		return AIReplyExecutionResult{Status: AIReplyExecutionStatusCompleted, ReasonCode: "expired_human_dispatch"}, errAIReplyJobExpired
	}
	if job.TriggerKind == enums.AIReplyJobTriggerKindMedia {
		if err := s.prepareMedia(ctx, state); err != nil {
			return AIReplyExecutionResult{}, err
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
	if TriggerAIReplySyncHook == nil {
		return AIReplyExecutionResult{}, fmt.Errorf("synchronous AI reply runtime unavailable")
	}
	return TriggerAIReplySyncHook(ctx, *state.Conversation, *state.Message)
}

var errAIReplyJobExpired = errors.New("AI reply job expired after human dispatch")

func (s *aiReplyJobService) prepareMedia(ctx context.Context, state *aiReplyJobExecutionState) error {
	if state == nil || state.Message == nil {
		return fmt.Errorf("media execution state unavailable")
	}
	_, _, status := utils.RuntimeMediaUnderstandingFromPayload(state.Message.Payload)
	if strings.TrimSpace(status) == "understood" {
		return nil
	}
	return MediaUnderstandingService.UnderstandInboundMessage(ctx, state.Message.ID)
}

func (s *aiReplyJobService) finishClaimed(job *models.AIReplyJob, owner string, result AIReplyExecutionResult, runErr error) {
	now := time.Now()
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
		s.markTerminal(job, owner, enums.AIReplyJobStatusExpired, "expired_human_dispatch", "", now)
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
				s.dispatchControlledFailure(job, owner, string(AIReplyExecutionErrorCommitFailed), now)
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
		s.dispatchControlledFailure(job, owner, string(code), now)
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
	failedTaskKeys := uniqueTaskKeys(result.FailedTaskKeys)
	if taskFailureRequiresHuman(runErr) {
		failedTaskKeys = uniqueTaskKeys(append(failedTaskKeys, result.TaskKeys...))
	}
	if len(failedTaskKeys) > 0 {
		if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return AIReplyTurnTaskService.MarkHandoffPendingDB(
				ctx.Tx, job.TenantID, job.TurnID, job.ID, failedTaskKeys, classifyTaskFailure(runErr), now,
			)
		}); err != nil {
			return false
		}
	}

	hasRunnable := result.HasRemainingTasks || AIReplyTurnTaskService.HasRunnable(job.TenantID, job.TurnID)
	if hasRunnable {
		if result.Status == AIReplyExecutionStatusCompleted {
			if err := s.validateCompletionEvidence(job, result); err != nil {
				return false
			}
		}
		_, _ = repositories.AIReplyJobRepository.MarkRetry(
			sqls.DB(), job.ID, job.TenantID, owner,
			"turn_tasks_remaining", classifyTaskFailure(runErr), now.Add(aiReplyJobContinuationDelay), now, false,
		)
		return true
	}

	hasFailureHandoff := len(failedTaskKeys) > 0 || AIReplyTurnTaskService.HasFailureHandoffs(job.TenantID, job.TurnID)
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

func taskFailureRequiresHuman(err error) bool {
	code, ok := AIReplyExecutionErrorCodeOf(err)
	if !ok {
		return false
	}
	switch code {
	case AIReplyExecutionErrorGenerationFailed, AIReplyExecutionErrorKnowledgeUnavailable,
		AIReplyExecutionErrorEmptyOutput, AIReplyExecutionErrorResourceInvariantBroken:
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
	if current.AttemptCount < aiReplyJobMaxAttempts {
		delayIndex := current.AttemptCount - 1
		if delayIndex < 0 {
			delayIndex = 0
		}
		if delayIndex >= len(aiReplyJobRetryDelays) {
			delayIndex = len(aiReplyJobRetryDelays) - 1
		}
		_, _ = repositories.AIReplyJobRepository.MarkRetry(sqls.DB(), current.ID, current.TenantID, owner,
			"runtime_retry", errorClass, now.Add(aiReplyJobRetryDelays[delayIndex]), now, true)
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
	if err := s.dispatchHuman(state, current, "AI 自动回复连续失败，需要人工跟进"); err != nil {
		_, _ = repositories.AIReplyJobRepository.MarkRetry(sqls.DB(), current.ID, current.TenantID, owner,
			"human_dispatch_retry", "human_dispatch_failed", now.Add(time.Minute), now, false)
		return
	}
	s.markTerminal(current, owner, enums.AIReplyJobStatusFailed, "retry_exhausted_human_dispatch", errorClass, now)
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
	requestID := fmt.Sprintf("ai_reply_job_handoff_%d", job.ID)
	_, err := ConversationHumanDispatchService.HandoffByAIWithRequestID(state.Conversation.ID, aiAgent, reason, requestID)
	return err
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
	if conversation.Status == enums.IMConversationStatusClosed {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusSkipped, Code: "conversation_closed"}
	}
	if conversation.CurrentAssigneeID > 0 {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusSkipped, Code: "human_agent_serving"}
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
	if routeStatusBlocksAIReply(route.RouteStatus) || route.RouteStatus == enums.ConversationRouteStatusClosed {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusSkipped, Code: "manual_or_closed_route"}
	}
	session := repositories.ConversationChannelSessionRepository.TakeByConversationSession(db, conversation.TenantID, conversation.ID, message.SessionNo)
	if session == nil || session.TenantID != conversation.TenantID || session.StoreID != conversation.StoreID ||
		session.StoreStaffBindingID != conversation.StoreStaffBindingID || session.WxWorkInstanceID != route.WxWorkInstanceID {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: "session_scope_invalid"}
	}
	if session.Status != enums.StatusOk || session.EndedAt != nil {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusSuperseded, Code: "session_inactive"}
	}
	if _, err := StoreModelCredentialService.requireStoreStaffCredentialScopeDB(db, conversation.TenantID, conversation.StoreID, conversation.StoreStaffBindingID, true); err != nil {
		return nil, &aiReplyJobDecision{Status: enums.AIReplyJobStatusFailed, Code: "binding_scope_invalid"}
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
		AIReplyTurnTaskService.HasRunnable(state.Job.TenantID, state.Job.TurnID)
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
			if state.Job.TurnID <= 0 || message.AIReplyTurnID != state.Job.TurnID || message.SessionNo != state.Job.SessionNo {
				return &aiReplyJobDecision{Status: enums.AIReplyJobStatusSuperseded, Code: "newer_customer_message"}
			}
			if message.AIReplyTurnVersion > state.Job.TurnVersion {
				return &aiReplyJobDecision{Status: enums.AIReplyJobStatusSuperseded, Code: "stale_turn_version"}
			}
		case enums.IMSenderTypeAgent:
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
