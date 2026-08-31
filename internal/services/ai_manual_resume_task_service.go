package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/replyruntime"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	aiManualResumeTaskWaiting           = "waiting"
	aiManualResumeTaskReady             = "ready"
	aiManualResumeTaskRunning           = "running"
	aiManualResumeTaskRetry             = "retry"
	aiManualResumeTaskSucceeded         = "succeeded"
	aiManualResumeTaskCancelled         = "cancelled"
	aiManualResumeTaskFailed            = "failed"
	aiManualResumeTaskBlockedAIDisabled = "blocked_ai_disabled"

	aiManualResumeNotice                  = "同事暂时没能接入，接下来我先继续帮你处理。"
	aiManualResumeAwaitingDeliveryMarker  = "committed reply is awaiting external delivery"
	aiManualResumeDeliveryUncertainMarker = "committed reply external delivery result is uncertain"
	aiManualResumeDeliveryFailedMarker    = "committed reply external delivery failed"
	aiManualResumeRunLogReconcileLimit    = 20
	aiManualResumeDeliveryReconcileDelay  = 3 * time.Second
	aiManualResumeDeliveryUncertainAfter  = 5 * time.Minute
)

var AIManualResumeTaskService = newAIManualResumeTaskService()

type aiManualResumeTaskService struct{}

func newAIManualResumeTaskService() *aiManualResumeTaskService {
	return &aiManualResumeTaskService{}
}

func (s *aiManualResumeTaskService) NewHandoffToken() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

func (s *aiManualResumeTaskService) Schedule(conversationID int64, originMessageID int64, handoffToken string) (*models.AIManualResumeTask, error) {
	if conversationID <= 0 {
		return nil, fmt.Errorf("conversation id is required")
	}
	db := sqls.DB()
	if db == nil || !db.Migrator().HasTable(&models.AIManualResumeTask{}) {
		return nil, fmt.Errorf("AI manual resume task table is unavailable")
	}
	handoffToken = strings.TrimSpace(handoffToken)
	if handoffToken == "" {
		handoffToken = s.NewHandoffToken()
	}
	taskKey := fmt.Sprintf("manual_resume:%d:%s", conversationID, handoffToken)
	var scheduled *models.AIManualResumeTask
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		state, err := ConversationRouteService.lockByConversationIDWithDB(ctx.Tx, conversationID)
		if err != nil {
			return err
		}
		if state == nil || !routeStatusBlocksManualResume(state.RouteStatus) {
			return fmt.Errorf("conversation is not in a manual route")
		}
		if originMessageID <= 0 {
			if message := s.latestCustomerMessageWithDB(ctx.Tx, conversationID); message != nil {
				originMessageID = message.ID
			}
		}

		active := s.latestActiveTaskWithDB(ctx.Tx, conversationID, []string{
			aiManualResumeTaskWaiting,
			aiManualResumeTaskReady,
			aiManualResumeTaskRunning,
			aiManualResumeTaskRetry,
			aiManualResumeTaskBlockedAIDisabled,
		})
		if active != nil {
			latestWaitingMessageID := active.LatestWaitingMessageID
			if latestWaitingMessageID <= 0 {
				latestWaitingMessageID = active.OriginMessageID
			}
			if originMessageID > latestWaitingMessageID {
				if err := s.recordWaitingCustomerMessageWithDB(ctx.Tx, state, originMessageID, time.Now()); err != nil {
					return err
				}
				active = repositories.AIManualResumeTaskRepository.Get(ctx.Tx, active.ID)
				if active == nil {
					return fmt.Errorf("cannot reload AI manual resume task %d", conversationID)
				}
			}
			scheduled = active
			return nil
		}
		if existing := repositories.AIManualResumeTaskRepository.Take(ctx.Tx, "task_key = ?", taskKey); existing != nil {
			scheduled = existing
			return nil
		}

		now := time.Now()
		item := &models.AIManualResumeTask{
			TaskKey:                taskKey,
			HandoffToken:           handoffToken,
			ConversationID:         conversationID,
			WxWorkInstanceID:       state.WxWorkInstanceID,
			OriginMessageID:        originMessageID,
			LatestWaitingMessageID: originMessageID,
			RouteStatus:            string(state.RouteStatus),
			TaskStatus:             aiManualResumeTaskWaiting,
			NextReminderAt:         nextManualResumeReminderAt(state, now),
			AuditFields: models.AuditFields{
				CreatedAt:      now,
				CreateUserName: "system",
				UpdatedAt:      now,
				UpdateUserName: "system",
			},
		}
		if err := repositories.AIManualResumeTaskRepository.Create(ctx.Tx, item); err != nil {
			return err
		}
		scheduled = item
		return nil
	})
	return scheduled, err
}

func (s *aiManualResumeTaskService) EnsureForTimeout(conversationID int64) bool {
	if task := s.latestActiveTask(conversationID, []string{aiManualResumeTaskWaiting, aiManualResumeTaskBlockedAIDisabled}); task != nil {
		return true
	}
	messages := MessageService.Find(sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		Where("recalled_at IS NULL AND send_status <> ?", enums.IMMessageStatusRecalled).
		Desc("seq_no").
		Desc("id").
		Limit(20))
	for _, message := range messages {
		if isConsumedHandoffConfirmationMessage(message) {
			continue
		}
		_, err := s.Schedule(conversationID, message.ID, "legacy_"+s.NewHandoffToken())
		return err == nil
	}
	return false
}

func (s *aiManualResumeTaskService) RecordWaitingCustomerMessage(conversationID int64, messageID int64) {
	if conversationID <= 0 || messageID <= 0 {
		return
	}
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		state, err := ConversationRouteService.lockByConversationIDWithDB(ctx.Tx, conversationID)
		if err != nil {
			return err
		}
		return s.recordWaitingCustomerMessageWithDB(ctx.Tx, state, messageID, time.Now())
	}); err != nil {
		slog.Warn("record waiting customer message failed", "conversation_id", conversationID, "message_id", messageID, "error", err)
	}
}

func (s *aiManualResumeTaskService) recordWaitingCustomerMessageWithDB(db *gorm.DB, state *models.ConversationRouteState, messageID int64, now time.Time) error {
	if db == nil || state == nil || state.ConversationID <= 0 || messageID <= 0 {
		return fmt.Errorf("manual resume route and message are required")
	}
	if !routeStatusBlocksManualResume(state.RouteStatus) || !db.Migrator().HasTable(&models.AIManualResumeTask{}) {
		return nil
	}
	statuses := []string{
		aiManualResumeTaskWaiting,
		aiManualResumeTaskReady,
		aiManualResumeTaskRetry,
		aiManualResumeTaskRunning,
		aiManualResumeTaskBlockedAIDisabled,
	}
	task := &models.AIManualResumeTask{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("conversation_id = ? AND task_status IN ?", state.ConversationID, statuses).
		Order("id DESC").
		Take(task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		handoffToken := fmt.Sprintf("followup_%d", messageID)
		return repositories.AIManualResumeTaskRepository.Create(db, &models.AIManualResumeTask{
			TaskKey:                fmt.Sprintf("manual_resume:%d:%s", state.ConversationID, handoffToken),
			HandoffToken:           handoffToken,
			ConversationID:         state.ConversationID,
			WxWorkInstanceID:       state.WxWorkInstanceID,
			OriginMessageID:        messageID,
			LatestWaitingMessageID: messageID,
			RouteStatus:            string(state.RouteStatus),
			TaskStatus:             aiManualResumeTaskWaiting,
			NextReminderAt:         nextManualResumeReminderAt(state, now),
			AuditFields: models.AuditFields{
				CreatedAt:      now,
				CreateUserName: "system",
				UpdatedAt:      now,
				UpdateUserName: "system",
			},
		})
	}
	if err != nil {
		return err
	}
	return repositories.AIManualResumeTaskRepository.Updates(db, task.ID, map[string]any{
		"task_status":               aiManualResumeTaskWaiting,
		"latest_waiting_message_id": messageID,
		"ready_at":                  nil,
		"next_retry_at":             nil,
		"retry_count":               0,
		"completed_at":              nil,
		"last_error":                "",
		"notice_sent_at":            nil,
		"next_reminder_at":          nextManualResumeReminderAt(state, now),
		"updated_at":                now,
		"update_user_name":          "system",
	})
}

func nextManualResumeReminderAt(state *models.ConversationRouteState, now time.Time) *time.Time {
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual {
		return nil
	}
	delay := 2 * time.Minute
	if isSafetyHandoffReason(state.HandoffReason) {
		delay = time.Minute
	}
	value := now.Add(delay)
	return &value
}

func (s *aiManualResumeTaskService) CancelActive(conversationID int64, reason string) {
	if conversationID <= 0 || sqls.DB() == nil || !sqls.DB().Migrator().HasTable(&models.AIManualResumeTask{}) {
		return
	}
	now := time.Now()
	_ = repositories.AIManualResumeTaskRepository.CancelActiveByConversation(sqls.DB(), conversationID, map[string]any{
		"task_status":      aiManualResumeTaskCancelled,
		"completed_at":     now,
		"last_error":       strings.TrimSpace(reason),
		"updated_at":       now,
		"update_user_name": "system",
	})
}

func (s *aiManualResumeTaskService) MarkReady(conversationID int64, now time.Time) bool {
	task := s.latestActiveTask(conversationID, []string{aiManualResumeTaskWaiting, aiManualResumeTaskBlockedAIDisabled})
	if task == nil {
		return false
	}
	if err := repositories.AIManualResumeTaskRepository.Updates(sqls.DB(), task.ID, map[string]any{
		"task_status":      aiManualResumeTaskReady,
		"ready_at":         now,
		"next_retry_at":    now,
		"last_error":       "",
		"updated_at":       now,
		"update_user_name": "system",
	}); err != nil {
		slog.Warn("mark AI manual resume task ready failed", "conversation_id", conversationID, "error", err)
		return false
	}
	return true
}

func (s *aiManualResumeTaskService) BlockForAIDisabled(conversationID int64, now time.Time) {
	task := s.latestActiveTask(conversationID, []string{aiManualResumeTaskWaiting, aiManualResumeTaskReady, aiManualResumeTaskRetry})
	if task == nil {
		return
	}
	_ = repositories.AIManualResumeTaskRepository.Updates(sqls.DB(), task.ID, map[string]any{
		"task_status":      aiManualResumeTaskBlockedAIDisabled,
		"next_retry_at":    nil,
		"last_error":       "AI reply is disabled for the employee account",
		"updated_at":       now,
		"update_user_name": "system",
	})
}

func (s *aiManualResumeTaskService) UnblockByWxWorkInstance(wxWorkInstanceID int64, now time.Time) error {
	if wxWorkInstanceID <= 0 || sqls.DB() == nil || !sqls.DB().Migrator().HasTable(&models.AIManualResumeTask{}) {
		return nil
	}
	return repositories.AIManualResumeTaskRepository.UnblockByWxWorkInstance(sqls.DB(), wxWorkInstanceID, map[string]any{
		"task_status":      aiManualResumeTaskReady,
		"ready_at":         now,
		"next_retry_at":    now,
		"last_error":       "",
		"updated_at":       now,
		"update_user_name": "system",
	})
}

func (s *aiManualResumeTaskService) ProcessDue(limit int) int {
	if limit <= 0 {
		limit = 50
	}
	db := sqls.DB()
	if db == nil || !db.Migrator().HasTable(&models.AIManualResumeTask{}) {
		return 0
	}
	now := time.Now()
	handled := s.processDueReminders(now, limit)
	items := repositories.AIManualResumeTaskRepository.Find(db, sqls.NewCnd().
		In("task_status", []string{aiManualResumeTaskReady, aiManualResumeTaskRetry}).
		Where("next_retry_at IS NOT NULL AND next_retry_at <= ?", now).
		Asc("next_retry_at").
		Limit(limit))
	for i := range items {
		if s.processOne(items[i], now) {
			handled++
		}
	}
	return handled
}

func (s *aiManualResumeTaskService) processDueReminders(now time.Time, limit int) int {
	items := repositories.AIManualResumeTaskRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("task_status", aiManualResumeTaskWaiting).
		Where("next_reminder_at IS NOT NULL AND next_reminder_at <= ?", now).
		Asc("next_reminder_at").
		Limit(limit))
	handled := 0
	for i := range items {
		if s.processReminder(items[i], now) {
			handled++
		}
	}
	return handled
}

func (s *aiManualResumeTaskService) processReminder(task models.AIManualResumeTask, now time.Time) bool {
	state := ConversationRouteService.GetByConversationID(task.ConversationID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || !state.NeedHumanFollowUp {
		s.cancelTask(&task, "manual reminder no longer applies")
		return true
	}
	safety := isSafetyHandoffReason(state.HandoffReason)
	maxReminders := 1
	if safety {
		maxReminders = 2
	}
	if task.ReminderCount >= maxReminders {
		_ = repositories.AIManualResumeTaskRepository.Updates(sqls.DB(), task.ID, map[string]any{
			"next_reminder_at": nil,
			"updated_at":       now,
		})
		return true
	}
	reminderIndex := task.ReminderCount + 1
	var nextReminderAt *time.Time
	if safety && reminderIndex < maxReminders {
		value := now.Add(time.Minute)
		nextReminderAt = &value
	}
	claimed, err := repositories.AIManualResumeTaskRepository.ClaimReminder(sqls.DB(), task.ID, task.ReminderCount, now, map[string]any{
		"reminder_count":   reminderIndex,
		"last_reminder_at": now,
		"next_reminder_at": nextReminderAt,
		"updated_at":       now,
		"update_user_name": "system",
	})
	if err != nil || !claimed {
		return false
	}
	reason := "人工接待仍未响应，请尽快处理。"
	if safety {
		reason = "安全/突发情况仍未响应，请立即关注并处理。"
	}
	noticeKey := fmt.Sprintf("%s:reminder:%d", task.TaskKey, reminderIndex)
	ConversationHumanDispatchService.notifyStoreRoomHandoffWithKey(task.ConversationID, reason+" "+state.HandoffReason, noticeKey)
	return true
}

func (s *aiManualResumeTaskService) processOne(task models.AIManualResumeTask, now time.Time) bool {
	claimed, err := repositories.AIManualResumeTaskRepository.Claim(sqls.DB(), task.ID,
		[]string{aiManualResumeTaskReady, aiManualResumeTaskRetry},
		map[string]any{"task_status": aiManualResumeTaskRunning, "updated_at": now, "update_user_name": "system"})
	if err != nil || !claimed {
		return false
	}
	current := repositories.AIManualResumeTaskRepository.Get(sqls.DB(), task.ID)
	if current == nil {
		return false
	}
	requestID := manualResumeRequestID(current)
	previousOutcome := s.manualResumeRunOutcome(current, manualResumeCompatibleRequestIDs(current))
	state := ConversationRouteService.GetByConversationID(current.ConversationID)
	if state != nil && state.RouteStatus == enums.ConversationRouteStatusAIServing {
		switch previousOutcome.State {
		case manualResumeRunCommitted:
			s.handleTransitionError(current, s.completeTask(current, now), now, "complete already-delivered manual resume")
			return true
		case manualResumeRunDeliveryPending:
			s.handleTransitionError(current, s.awaitCommittedDelivery(current, now), now, "wait for committed external delivery")
			return true
		case manualResumeRunDeliveryUncertain:
			s.handleTransitionError(current, s.holdUncertainDelivery(current, now), now, "hold uncertain external delivery")
			return true
		case manualResumeRunDeliveryFailed:
			s.handleTransitionError(current, s.holdFailedDelivery(current, now), now, "hold failed external delivery")
			return true
		}
		s.handleTransitionError(current, s.cancelTask(current, "conversation already left the waiting manual route"), now, "cancel obsolete manual resume")
		return true
	}
	if state == nil || !routeStatusBlocksManualResume(state.RouteStatus) {
		s.handleTransitionError(current, s.cancelTask(current, "conversation left the waiting manual route before resume"), now, "cancel manual resume outside human route")
		return true
	}
	if !s.aiReplyEnabled(current.WxWorkInstanceID) {
		_ = repositories.AIManualResumeTaskRepository.Updates(sqls.DB(), current.ID, map[string]any{
			"task_status": aiManualResumeTaskBlockedAIDisabled,
			"last_error":  "AI reply is disabled for the employee account",
			"updated_at":  now,
		})
		return true
	}
	currentSourceMessageID := current.LatestWaitingMessageID
	if currentSourceMessageID <= 0 {
		currentSourceMessageID = current.OriginMessageID
	}
	if !s.CanCommitRequest(current.ConversationID, requestID, currentSourceMessageID) {
		current = repositories.AIManualResumeTaskRepository.Get(sqls.DB(), task.ID)
		if current == nil || current.TaskStatus != aiManualResumeTaskRunning {
			return true
		}
		s.handleTransitionError(current, s.cancelTask(current, "manual resume was invalidated before rebuilding the deferred snapshot"), now, "cancel invalidated manual resume")
		return true
	}
	switch previousOutcome.State {
	case manualResumeRunCommitted:
		if err := s.finalizeResumeRoute(current, state, now); err != nil {
			s.handleRunError(current, err, now)
			return true
		}
		s.handleTransitionError(current, s.completeTask(current, now), now, "complete restored manual resume")
		return true
	case manualResumeRunKeepsManual:
		if err := s.finalizeResumeRouteSilently(current, state, now); err != nil {
			s.handleRunError(current, err, now)
			return true
		}
		if updated := ConversationService.Get(current.ConversationID); updated != nil {
			WsService.PublishConversationChanged(updated, enums.IMRealtimeEventConversationUpdated)
		}
		return true
	case manualResumeRunDeliveryPending:
		s.handleTransitionError(current, s.awaitCommittedDelivery(current, now), now, "wait for committed external delivery")
		return true
	case manualResumeRunDeliveryUncertain:
		s.handleTransitionError(current, s.holdUncertainDelivery(current, now), now, "hold uncertain external delivery")
		return true
	case manualResumeRunDeliveryFailed:
		s.handleTransitionError(current, s.holdFailedDelivery(current, now), now, "hold failed external delivery")
		return true
	}
	conversation := ConversationService.Get(current.ConversationID)
	message, resumeSnapshot, hasResumeSnapshot, snapshotState := s.resolveResumeMessageWithSnapshot(current)
	if conversation == nil || message == nil {
		s.handleRunError(current, fmt.Errorf("manual resume conversation or message is unavailable"), now)
		return true
	}
	switch snapshotState {
	case manualResumeSnapshotDeliveryPending:
		s.handleTransitionError(current, s.awaitCommittedDelivery(current, now), now, "wait for original deferred-run delivery")
		return true
	case manualResumeSnapshotDeliveryUncertain:
		s.handleTransitionError(current, s.holdUncertainDelivery(current, now), now, "hold uncertain original deferred-run delivery")
		return true
	case manualResumeSnapshotDeliveryFailed:
		s.handleTransitionError(current, s.holdFailedDelivery(current, now), now, "hold failed original deferred-run delivery")
		return true
	}
	if snapshotState == manualResumeSnapshotSettled && len(resumeSnapshot.NewSources) == 0 {
		if err := s.finalizeResumeRouteSilently(current, state, now); err != nil {
			s.handleRunError(current, err, now)
			return true
		}
		if updated := ConversationService.Get(current.ConversationID); updated != nil {
			WsService.PublishConversationChanged(updated, enums.IMRealtimeEventConversationUpdated)
		}
		return true
	}
	if TriggerAIReplySyncHook == nil {
		s.handleRunError(current, fmt.Errorf("synchronous AI reply hook is unavailable"), now)
		return true
	}
	messageCopy := *message
	messageCopy.RequestID = requestID
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	if hasResumeSnapshot {
		ctx = replyruntime.WithManualResumeSnapshot(ctx, resumeSnapshot)
	}
	err = TriggerAIReplySyncHook(ctx, *conversation, messageCopy)
	cancel()
	current = repositories.AIManualResumeTaskRepository.Get(sqls.DB(), task.ID)
	if current == nil || current.TaskStatus != aiManualResumeTaskRunning {
		return true
	}
	currentSourceMessageID = current.LatestWaitingMessageID
	if currentSourceMessageID <= 0 {
		currentSourceMessageID = current.OriginMessageID
	}
	if currentSourceMessageID != message.ID {
		return true
	}
	state = ConversationRouteService.GetByConversationID(current.ConversationID)
	if state == nil || !s.CanCommitRequest(current.ConversationID, requestID, message.ID) {
		if state == nil || !routeStatusBlocksManualResume(state.RouteStatus) {
			s.handleTransitionError(current, s.cancelTask(current, "conversation left the waiting manual route during resume"), time.Now(), "cancel manual resume after route change")
		} else {
			s.handleRunError(current, fmt.Errorf("manual resume state changed while AI was running"), time.Now())
		}
		return true
	}
	if err != nil {
		s.handleRunError(current, err, time.Now())
		return true
	}
	finishedAt := time.Now()
	switch outcome := s.manualResumeRunOutcome(current, []string{requestID}); outcome.State {
	case manualResumeRunCommitted:
		if err := s.finalizeResumeRoute(current, state, finishedAt); err != nil {
			s.handleRunError(current, err, finishedAt)
			return true
		}
		s.handleTransitionError(current, s.completeTask(current, time.Now()), time.Now(), "complete delivered manual resume")
	case manualResumeRunKeepsManual:
		if err := s.finalizeResumeRouteSilently(current, state, finishedAt); err != nil {
			s.handleRunError(current, err, finishedAt)
			return true
		}
	case manualResumeRunDeliveryPending:
		s.handleTransitionError(current, s.awaitCommittedDelivery(current, finishedAt), finishedAt, "wait for committed external delivery")
		return true
	case manualResumeRunDeliveryUncertain:
		s.handleTransitionError(current, s.holdUncertainDelivery(current, finishedAt), finishedAt, "hold uncertain external delivery")
		return true
	case manualResumeRunDeliveryFailed:
		s.handleTransitionError(current, s.holdFailedDelivery(current, finishedAt), finishedAt, "hold failed external delivery")
		return true
	default:
		s.handleRunError(current, fmt.Errorf("AI runtime completed without an authoritative resume outcome"), finishedAt)
		return true
	}
	if updated := ConversationService.Get(current.ConversationID); updated != nil {
		WsService.PublishConversationChanged(updated, enums.IMRealtimeEventConversationUpdated)
	}
	return true
}

func (s *aiManualResumeTaskService) finalizeResumeRoute(task *models.AIManualResumeTask, state *models.ConversationRouteState, now time.Time) error {
	if task == nil || state == nil {
		return fmt.Errorf("manual resume task or route state is unavailable")
	}
	message := s.resolveResumeMessage(task)
	if message == nil {
		return fmt.Errorf("manual resume is no longer eligible to restore AI")
	}
	return s.finalizeResumeRouteWithSource(task, state, message.ID, now, false)
}

func (s *aiManualResumeTaskService) finalizeResumeRouteSilently(task *models.AIManualResumeTask, state *models.ConversationRouteState, now time.Time) error {
	if task == nil || state == nil {
		return fmt.Errorf("manual resume task or route state is unavailable")
	}
	sourceMessageID := task.LatestWaitingMessageID
	if sourceMessageID <= 0 {
		sourceMessageID = task.OriginMessageID
	}
	if sourceMessageID <= 0 {
		return fmt.Errorf("manual resume source message is unavailable")
	}
	return s.finalizeResumeRouteWithSource(task, state, sourceMessageID, now, true)
}

func (s *aiManualResumeTaskService) finalizeResumeRouteWithSource(task *models.AIManualResumeTask, state *models.ConversationRouteState, sourceMessageID int64, now time.Time, silent bool) error {
	requestID := manualResumeRequestID(task)
	timeoutStage := "manual_wait_resume_committed"
	eventReason := "人工等待超时，AI已提交实际续答"
	if silent {
		timeoutStage = "manual_wait_resume_silent"
		eventReason = "原请求已完成转接处理，人工等待超时后AI静默恢复接待"
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedState, err := ConversationRouteService.lockByConversationIDWithDB(ctx.Tx, task.ConversationID)
		if err != nil {
			return err
		}
		if !s.canCommitRequestWithDB(ctx.Tx, lockedState, task.ConversationID, requestID, sourceMessageID) {
			return fmt.Errorf("manual resume is no longer eligible to restore AI")
		}
		if err := ManualSessionTimeoutService.restoreConversationShellWithDB(ctx, task.ConversationID, now, timeoutStage, eventReason, lockedState.RouteStatus); err != nil {
			return err
		}
		keepFollowUp := isSafetyHandoffReason(lockedState.HandoffReason)
		if err := repositories.ConversationRouteStateRepository.Updates(ctx.Tx, lockedState.ID, map[string]any{
			"route_status":             enums.ConversationRouteStatusAIServing,
			"route_target":             "ai",
			"manual_expire_at":         nil,
			"pending_action":           "",
			"pending_action_payload":   "",
			"pending_action_expire_at": nil,
			"need_human_follow_up":     keepFollowUp,
			"handoff_reason":           "人工等待超时，AI已恢复接待",
			"updated_at":               now,
			"update_user_name":         "system",
		}); err != nil {
			return err
		}
		taskUpdates := map[string]any{
			"task_status":      aiManualResumeTaskSucceeded,
			"completed_at":     now,
			"next_retry_at":    nil,
			"last_error":       "",
			"updated_at":       now,
			"update_user_name": "system",
		}
		if !silent {
			taskUpdates["notice_sent_at"] = now
		}
		return repositories.AIManualResumeTaskRepository.Updates(ctx.Tx, task.ID, taskUpdates)
	})
}

func manualResumeRequestID(task *models.AIManualResumeTask) string {
	if task == nil {
		return ""
	}
	requestID := "manual_resume_" + strings.ReplaceAll(task.HandoffToken, "-", "")
	sourceMessageID := task.LatestWaitingMessageID
	if sourceMessageID <= 0 {
		sourceMessageID = task.OriginMessageID
	}
	if sourceMessageID <= 0 {
		return requestID
	}
	return fmt.Sprintf("%s_%d", requestID, sourceMessageID)
}

func manualResumeLegacyRequestID(task *models.AIManualResumeTask) string {
	if task == nil {
		return ""
	}
	return "manual_resume_" + strings.ReplaceAll(task.HandoffToken, "-", "")
}

func manualResumeCompatibleRequestIDs(task *models.AIManualResumeTask) []string {
	current := strings.TrimSpace(manualResumeRequestID(task))
	if current == "" {
		return nil
	}
	ret := []string{current}
	legacy := strings.TrimSpace(manualResumeLegacyRequestID(task))
	if legacy != "" && legacy != current {
		ret = append(ret, legacy)
	}
	return ret
}

func (s *aiManualResumeTaskService) CanCommitRequest(conversationID int64, requestID string, sourceMessageID int64) bool {
	return s.canCommitRequestWithDB(sqls.DB(), nil, conversationID, requestID, sourceMessageID)
}

func (s *aiManualResumeTaskService) canCommitRequestWithDB(db *gorm.DB, state *models.ConversationRouteState, conversationID int64, requestID string, sourceMessageID int64) bool {
	requestID = strings.TrimSpace(requestID)
	if db == nil || conversationID <= 0 || !strings.HasPrefix(requestID, "manual_resume_") {
		return false
	}
	task := s.latestActiveTaskWithDB(db, conversationID, []string{aiManualResumeTaskRunning, aiManualResumeTaskRetry})
	if task == nil || manualResumeRequestID(task) != requestID {
		return false
	}
	if task.TaskStatus == aiManualResumeTaskRetry && strings.TrimSpace(task.LastError) != aiManualResumeAwaitingDeliveryMarker {
		return false
	}
	if state == nil {
		state = repositories.ConversationRouteStateRepository.Take(db, "conversation_id = ?", conversationID)
	}
	if state == nil || !routeStatusBlocksManualResume(state.RouteStatus) || !state.NeedHumanFollowUp {
		return false
	}
	latestWaitingMessageID := task.LatestWaitingMessageID
	if latestWaitingMessageID <= 0 {
		latestWaitingMessageID = task.OriginMessageID
	}
	if sourceMessageID > 0 && latestWaitingMessageID != sourceMessageID {
		return false
	}
	latestAgent := repositories.MessageRepository.FindOne(db, sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("sender_type", enums.IMSenderTypeAgent).
		Where("recalled_at IS NULL AND send_status <> ?", enums.IMMessageStatusRecalled).
		Desc("seq_no").
		Desc("id"))
	return latestAgent == nil || latestAgent.ID < latestWaitingMessageID
}

func (s *aiManualResumeTaskService) latestActiveTask(conversationID int64, statuses []string) *models.AIManualResumeTask {
	db := sqls.DB()
	if db == nil || !db.Migrator().HasTable(&models.AIManualResumeTask{}) {
		return nil
	}
	return s.latestActiveTaskWithDB(db, conversationID, statuses)
}

func (s *aiManualResumeTaskService) latestActiveTaskWithDB(db *gorm.DB, conversationID int64, statuses []string) *models.AIManualResumeTask {
	if db == nil {
		return nil
	}
	items := repositories.AIManualResumeTaskRepository.Find(db, sqls.NewCnd().
		Eq("conversation_id", conversationID).
		In("task_status", statuses).
		Desc("id").
		Limit(1))
	if len(items) == 0 {
		return nil
	}
	return &items[0]
}

func (s *aiManualResumeTaskService) latestCustomerMessage(conversationID int64) *models.Message {
	return s.latestCustomerMessageWithDB(sqls.DB(), conversationID)
}

func (s *aiManualResumeTaskService) latestCustomerMessageWithDB(db *gorm.DB, conversationID int64) *models.Message {
	if db == nil {
		return nil
	}
	return repositories.MessageRepository.FindOne(db, sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		Where("recalled_at IS NULL AND send_status <> ?", enums.IMMessageStatusRecalled).
		Desc("seq_no").
		Desc("id"))
}

func (s *aiManualResumeTaskService) resolveResumeMessage(task *models.AIManualResumeTask) *models.Message {
	message, _, _, _ := s.resolveResumeMessageWithSnapshot(task)
	return message
}

func (s *aiManualResumeTaskService) resolveResumeMessageWithSnapshot(task *models.AIManualResumeTask) (*models.Message, replyruntime.ManualResumeSnapshot, bool, manualResumeSnapshotState) {
	if task == nil {
		return nil, replyruntime.ManualResumeSnapshot{}, false, manualResumeSnapshotUnavailable
	}
	messageID := task.LatestWaitingMessageID
	if messageID <= 0 {
		messageID = task.OriginMessageID
	}
	message := MessageService.Get(messageID)
	if message == nil || message.ConversationID != task.ConversationID || message.SenderType != enums.IMSenderTypeCustomer || message.RecalledAt != nil {
		return nil, replyruntime.ManualResumeSnapshot{}, false, manualResumeSnapshotUnavailable
	}
	startID := task.OriginMessageID
	if startID <= 0 || startID > message.ID {
		startID = message.ID
	}
	waitingMessages := MessageService.Find(sqls.NewCnd().
		Eq("conversation_id", task.ConversationID).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		Gte("id", startID).
		Lte("id", message.ID).
		Where("recalled_at IS NULL AND send_status <> ?", enums.IMMessageStatusRecalled).
		Asc("seq_no").Asc("id"))
	snapshot, snapshotState := s.manualResumeExecutionSnapshotState(task, waitingMessages)
	hasSnapshot := snapshotState == manualResumeSnapshotRecoverable
	sources := manualResumeSourcesFromSnapshot(snapshot)
	if snapshotState == manualResumeSnapshotSettled {
		if len(snapshot.NewSources) == 0 {
			return message, snapshot, false, snapshotState
		}
		sources = manualResumeSourcesFromTrace(snapshot.NewSources)
	}
	parts := make([]string, 0, len(sources))
	for index, source := range sources {
		if text := strings.TrimSpace(source.Text); text != "" {
			parts = append(parts, manualResumeSourcePart(source, index+1))
		}
	}
	if len(parts) > 0 {
		copyMessage := *message
		copyMessage.Content = utils.BuildRuntimeCustomerBurstEnvelope(parts)
		return &copyMessage, snapshot, hasSnapshot, snapshotState
	}
	return message, snapshot, hasSnapshot, snapshotState
}

type manualResumeSource struct {
	Ref         string
	MessageID   int64
	MessageType enums.IMMessageType
	Text        string
}

type manualResumeDeferredTask = replyruntime.ManualResumeTaskPlan

type manualResumeSnapshotState string

const (
	manualResumeSnapshotUnavailable       manualResumeSnapshotState = "unavailable"
	manualResumeSnapshotRecoverable       manualResumeSnapshotState = "recoverable"
	manualResumeSnapshotSettled           manualResumeSnapshotState = "settled"
	manualResumeSnapshotDeliveryPending   manualResumeSnapshotState = "delivery_pending"
	manualResumeSnapshotDeliveryUncertain manualResumeSnapshotState = "delivery_uncertain"
	manualResumeSnapshotDeliveryFailed    manualResumeSnapshotState = "delivery_failed"
)

type manualResumeRunState string

const (
	manualResumeRunUnavailable       manualResumeRunState = "unavailable"
	manualResumeRunCommitted         manualResumeRunState = "committed"
	manualResumeRunKeepsManual       manualResumeRunState = "keeps_manual"
	manualResumeRunDeliveryPending   manualResumeRunState = "delivery_pending"
	manualResumeRunDeliveryUncertain manualResumeRunState = "delivery_uncertain"
	manualResumeRunDeliveryFailed    manualResumeRunState = "delivery_failed"
)

type manualResumeRunResult struct {
	RunLogID  int64
	RequestID string
	State     manualResumeRunState
}

type manualResumeDeferredSnapshotResult struct {
	RunLogID int64
	Tasks    []manualResumeDeferredTask
	Sources  []replyruntime.ManualResumeSource
	State    manualResumeSnapshotState
}

type manualResumeTraceProjection struct {
	Status         string `json:"status"`
	ReplySent      bool   `json:"replySent"`
	ReplyMessageID int64  `json:"replyMessageId"`
	Runtime        struct {
		Status string `json:"status"`
		Input  struct {
			CurrentTurnSources []replyruntime.ManualResumeSource `json:"currentTurnSources"`
		} `json:"input"`
		Pipeline struct {
			ReplyPlan struct {
				TaskPlans []replyruntime.ManualResumeTaskPlan `json:"taskPlans"`
			} `json:"replyPlan"`
			EvidenceJudge struct {
				DeferredTaskIDs []string `json:"deferredTaskIds"`
				Tasks           []struct {
					TaskID      string `json:"taskId"`
					Disposition string `json:"disposition"`
				} `json:"tasks"`
			} `json:"evidenceJudge"`
		} `json:"pipeline"`
		Output struct {
			FinishReason   string                      `json:"finishReason"`
			CommitMessages []manualResumeCommitMessage `json:"commitMessages"`
		} `json:"output"`
		Error struct {
			Stage string `json:"stage"`
		} `json:"error"`
	} `json:"runtime"`
}

type manualResumeCommitMessage struct {
	MessageID             int64    `json:"messageId"`
	MessageType           string   `json:"messageType"`
	ResourceType          string   `json:"resourceType"`
	FallbackResourceType  string   `json:"fallbackResourceType"`
	FallbackResourceTypes []string `json:"fallbackResourceTypes"`
	Content               string   `json:"content"`
	TaskID                string   `json:"taskId"`
	TaskIDs               []string `json:"taskIds"`
	Status                string   `json:"status"`
}

func (s *aiManualResumeTaskService) manualResumeRunOutcome(task *models.AIManualResumeTask, requestIDs []string) manualResumeRunResult {
	if task == nil || task.ConversationID <= 0 {
		return manualResumeRunResult{State: manualResumeRunUnavailable}
	}
	sourceMessageID := task.LatestWaitingMessageID
	if sourceMessageID <= 0 {
		sourceMessageID = task.OriginMessageID
	}
	seen := make(map[string]struct{}, len(requestIDs))
	best := manualResumeRunResult{State: manualResumeRunUnavailable}
	for _, requestID := range requestIDs {
		requestID = strings.TrimSpace(requestID)
		if requestID == "" {
			continue
		}
		if _, exists := seen[requestID]; exists {
			continue
		}
		seen[requestID] = struct{}{}
		runLogs := AgentRunLogService.Find(sqls.NewCnd().
			Eq("conversation_id", task.ConversationID).
			Eq("message_id", sourceMessageID).
			Eq("request_id", requestID).
			Desc("id").
			Limit(aiManualResumeRunLogReconcileLimit))
		requestBest := manualResumeRunResult{RequestID: requestID, State: manualResumeRunUnavailable}
		for i := range runLogs {
			candidate := s.manualResumeSingleRunOutcome(task, requestID, &runLogs[i])
			if candidate.State == manualResumeRunCommitted {
				return candidate
			}
			requestBest = preferredManualResumeRunResult(requestBest, candidate)
		}
		best = preferredManualResumeRunResult(best, requestBest)
	}
	if recovered := s.manualResumeRequestBoundMessageOutcome(task, manualResumeRequestID(task)); recovered.State == manualResumeRunCommitted {
		return recovered
	} else {
		best = preferredManualResumeRunResult(best, recovered)
	}
	return best
}

func (s *aiManualResumeTaskService) manualResumeRequestBoundMessageOutcome(task *models.AIManualResumeTask, requestID string) manualResumeRunResult {
	requestID = strings.TrimSpace(requestID)
	db := sqls.DB()
	if task == nil || task.ConversationID <= 0 || requestID == "" || db == nil || !db.Migrator().HasTable(&models.Message{}) {
		return manualResumeRunResult{State: manualResumeRunUnavailable}
	}
	messages := MessageService.Find(sqls.NewCnd().
		Eq("conversation_id", task.ConversationID).
		Eq("request_id", requestID).
		Eq("sender_type", enums.IMSenderTypeAI).
		Where("recalled_at IS NULL AND send_status <> ?", enums.IMMessageStatusRecalled).
		Asc("id"))
	if len(messages) == 0 {
		return manualResumeRunResult{State: manualResumeRunUnavailable}
	}

	result := manualResumeRunResult{RequestID: requestID, State: manualResumeRunUnavailable}
	now := time.Now()
	sourceMessageID := task.LatestWaitingMessageID
	if sourceMessageID <= 0 {
		sourceMessageID = task.OriginMessageID
	}
	for index := range messages {
		message := &messages[index]
		if !manualResumeRequestBoundMessageIsBusinessBarrier(message, requestID, sourceMessageID) {
			continue
		}
		candidate := manualResumeRunResult{RequestID: requestID, State: manualResumeRunUnavailable}
		switch manualResumeMessageDelivery(message) {
		case manualResumeMessageDeliveryFailed:
			candidate.State = manualResumeRunDeliveryFailed
		case manualResumeMessageDeliveryPending:
			candidate.State = manualResumeRunDeliveryPending
		case manualResumeMessageDeliveryUncertain:
			candidate.State = manualResumeRunDeliveryUncertain
		case manualResumeMessageDeliveryUnavailable, manualResumeMessageDeliverySent:
			if manualResumeMessageWithinPersistenceGrace(message, now) {
				candidate.State = manualResumeRunDeliveryPending
			} else {
				candidate.State = manualResumeRunDeliveryUncertain
			}
		}
		if candidate.State == manualResumeRunDeliveryFailed {
			return candidate
		}
		result = preferredManualResumeRunResult(result, candidate)
	}
	return result
}

func preferredManualResumeRunResult(current manualResumeRunResult, candidate manualResumeRunResult) manualResumeRunResult {
	if manualResumeRunStatePriority(candidate.State) > manualResumeRunStatePriority(current.State) {
		return candidate
	}
	return current
}

func manualResumeRunStatePriority(state manualResumeRunState) int {
	switch state {
	case manualResumeRunCommitted:
		return 50
	case manualResumeRunKeepsManual:
		return 40
	case manualResumeRunDeliveryFailed:
		return 30
	case manualResumeRunDeliveryUncertain:
		return 20
	case manualResumeRunDeliveryPending:
		return 10
	default:
		return 0
	}
}

func manualResumeRequestBoundMessageIsBusinessBarrier(message *models.Message, requestID string, sourceMessageID int64) bool {
	if message == nil || strings.TrimSpace(message.Content) == aiManualResumeNotice {
		return false
	}
	return replyruntime.StableOwnedManualResumeClientMessageIDMatches(message.ClientMsgID, requestID, sourceMessageID)
}

func manualResumeMessageWithinPersistenceGrace(message *models.Message, now time.Time) bool {
	if message == nil || now.IsZero() {
		return false
	}
	latest := message.CreatedAt
	if message.UpdatedAt.After(latest) {
		latest = message.UpdatedAt
	}
	if message.SentAt != nil && message.SentAt.After(latest) {
		latest = *message.SentAt
	}
	if latest.IsZero() {
		return false
	}
	age := now.Sub(latest)
	return age < 0 || age <= aiManualResumeDeliveryReconcileDelay
}

func (s *aiManualResumeTaskService) manualResumeSingleRunOutcome(task *models.AIManualResumeTask, requestID string, runLog *models.AgentRunLog) manualResumeRunResult {
	result := manualResumeRunResult{RequestID: requestID, State: manualResumeRunUnavailable}
	if runLog == nil {
		return result
	}
	result.RunLogID = runLog.ID
	if strings.TrimSpace(runLog.ErrorMessage) != "" {
		return result
	}
	runStatus := strings.ToLower(strings.TrimSpace(runLog.FinalStatus))
	switch runStatus {
	case "completed", "fallback", "interrupted":
	default:
		return result
	}
	var projection manualResumeTraceProjection
	if strings.TrimSpace(runLog.TraceData) == "" || json.Unmarshal([]byte(runLog.TraceData), &projection) != nil {
		return result
	}
	if strings.TrimSpace(projection.Runtime.Error.Stage) != "" {
		return result
	}
	coverage := s.manualResumeRunTraceCommitCoverageState(task, requestID, projection)
	commitComplete := coverage.Valid && coverage.TaskCoverageComplete && coverage.AllDelivered
	hasVisibleTasks := coverage.HasVisibleTasks
	hasCustomerVisibleCommit := coverage.HasDeliveredVisibleCommit
	pendingDelivery := manualResumeRunUsesStrictTaskCoverage(task, requestID) && coverage.Valid && coverage.TaskCoverageComplete &&
		coverage.HasVisibleTasks && coverage.HasVisibleCommit && coverage.HasPendingDelivery && !coverage.AllDelivered
	uncertainDelivery := manualResumeRunUsesStrictTaskCoverage(task, requestID) && coverage.Valid && coverage.TaskCoverageComplete &&
		coverage.HasVisibleTasks && coverage.HasVisibleCommit && coverage.HasUncertainDelivery && !coverage.AllDelivered
	failedDelivery := manualResumeRunUsesStrictTaskCoverage(task, requestID) && coverage.Valid && coverage.TaskCoverageComplete &&
		coverage.HasVisibleTasks && coverage.HasVisibleCommit && coverage.HasFailedDelivery && !coverage.AllDelivered
	keepsManual, validManualOutcome := manualResumeRunTraceKeepsManual(projection)
	if !validManualOutcome {
		return result
	}
	if keepsManual {
		if uncertainDelivery && runStatus != "interrupted" && manualResumeRunTraceCommitted(projection) {
			result.State = manualResumeRunDeliveryUncertain
			return result
		}
		if failedDelivery && runStatus != "interrupted" && manualResumeRunTraceCommitted(projection) {
			result.State = manualResumeRunDeliveryFailed
			return result
		}
		if pendingDelivery && runStatus != "interrupted" && manualResumeRunTraceCommitted(projection) {
			result.State = manualResumeRunDeliveryPending
			return result
		}
		if !s.manualResumeAnswerThenHandoffCommitsComplete(task, requestID, projection) {
			return result
		}
		if hasVisibleTasks && (!commitComplete || !hasCustomerVisibleCommit) {
			return result
		}
		result.State = manualResumeRunKeepsManual
		return result
	}
	completedVisibleWork := commitComplete && (hasVisibleTasks || hasCustomerVisibleCommit)
	if manualResumeRunUsesStrictTaskCoverage(task, requestID) {
		completedVisibleWork = commitComplete && hasVisibleTasks && hasCustomerVisibleCommit
	}
	if runStatus != "interrupted" && manualResumeRunTraceCommitted(projection) {
		if completedVisibleWork {
			result.State = manualResumeRunCommitted
		} else if uncertainDelivery {
			result.State = manualResumeRunDeliveryUncertain
		} else if failedDelivery {
			result.State = manualResumeRunDeliveryFailed
		} else if pendingDelivery {
			result.State = manualResumeRunDeliveryPending
		}
	}
	return result
}

func manualResumeRunTraceKeepsManual(projection manualResumeTraceProjection) (bool, bool) {
	if len(projection.Runtime.Pipeline.EvidenceJudge.DeferredTaskIDs) > 0 {
		dispositions, valid := manualResumeJudgeDispositions(projection)
		if !valid {
			return false, false
		}
		plansByTaskID := make(map[string]manualResumeDeferredTask, len(projection.Runtime.Pipeline.ReplyPlan.TaskPlans))
		for _, plan := range projection.Runtime.Pipeline.ReplyPlan.TaskPlans {
			taskID := strings.TrimSpace(plan.TaskID)
			if taskID == "" {
				continue
			}
			if _, duplicate := plansByTaskID[taskID]; duplicate {
				return false, false
			}
			plansByTaskID[taskID] = plan
		}
		seen := make(map[string]struct{}, len(projection.Runtime.Pipeline.EvidenceJudge.DeferredTaskIDs))
		for _, rawTaskID := range projection.Runtime.Pipeline.EvidenceJudge.DeferredTaskIDs {
			taskID := strings.TrimSpace(rawTaskID)
			if taskID == "" {
				return false, false
			}
			if _, duplicate := seen[taskID]; duplicate {
				return false, false
			}
			seen[taskID] = struct{}{}
			plan, exists := plansByTaskID[taskID]
			if !exists || manualResumeDeferredTaskState(plan, dispositions[taskID]) == manualResumeSnapshotUnavailable {
				return false, false
			}
		}
		return true, true
	}
	if len(projection.Runtime.Pipeline.EvidenceJudge.Tasks) > 0 {
		dispositions, valid := manualResumeJudgeDispositions(projection)
		if !valid {
			return false, false
		}
		plansByTaskID := make(map[string]manualResumeDeferredTask, len(projection.Runtime.Pipeline.ReplyPlan.TaskPlans))
		for _, plan := range projection.Runtime.Pipeline.ReplyPlan.TaskPlans {
			taskID := strings.TrimSpace(plan.TaskID)
			if taskID == "" {
				continue
			}
			if _, duplicate := plansByTaskID[taskID]; duplicate {
				return false, false
			}
			plansByTaskID[taskID] = plan
		}
		foundHandoffDisposition := false
		for taskID, disposition := range dispositions {
			switch strings.TrimSpace(disposition) {
			case "no_evidence_handoff", "knowledge_direct_handoff", "answer_then_handoff":
				plan, exists := plansByTaskID[taskID]
				if !exists || manualResumeDeferredTaskState(plan, disposition) == manualResumeSnapshotUnavailable {
					return false, false
				}
				foundHandoffDisposition = true
			}
		}
		if foundHandoffDisposition {
			return true, true
		}
	}
	for _, task := range projection.Runtime.Pipeline.ReplyPlan.TaskPlans {
		if strings.TrimSpace(task.OutputKind) == "handoff" || task.NeedsHumanRoute {
			return true, true
		}
	}
	finishReason := strings.TrimSpace(projection.Runtime.Output.FinishReason)
	return strings.HasPrefix(finishReason, "handoff_directive_") ||
		strings.HasPrefix(finishReason, "intent_human_route_") ||
		strings.HasPrefix(finishReason, "intent_emergency_human_route_") ||
		finishReason == "resume_generate_fallback_reinterrupt", true
}

func manualResumeRunTraceCommitted(projection manualResumeTraceProjection) bool {
	switch strings.ToLower(strings.TrimSpace(projection.Runtime.Status)) {
	case "completed", "fallback":
		return true
	default:
		return false
	}
}

type manualResumeCommitCoverageResult struct {
	Valid                     bool
	TaskCoverageComplete      bool
	HasVisibleTasks           bool
	HasVisibleCommit          bool
	HasDeliveredVisibleCommit bool
	HasPendingDelivery        bool
	HasUncertainDelivery      bool
	HasFailedDelivery         bool
	AllDelivered              bool
}

func (s *aiManualResumeTaskService) manualResumeRunTraceCommitCoverage(task *models.AIManualResumeTask, requestID string, projection manualResumeTraceProjection) (bool, bool, bool) {
	coverage := s.manualResumeRunTraceCommitCoverageState(task, requestID, projection)
	if !coverage.Valid {
		return false, coverage.HasVisibleTasks, false
	}
	return coverage.TaskCoverageComplete && coverage.AllDelivered, coverage.HasVisibleTasks, coverage.HasDeliveredVisibleCommit
}

func (s *aiManualResumeTaskService) manualResumeRunTraceCommitCoverageState(task *models.AIManualResumeTask, requestID string, projection manualResumeTraceProjection) manualResumeCommitCoverageResult {
	return s.manualResumeRunTraceCommitCoverageStateWithMode(task, requestID, projection, manualResumeRunUsesStrictTaskCoverage(task, requestID))
}

func (s *aiManualResumeTaskService) manualResumeRunTraceCommitCoverageStateWithMode(task *models.AIManualResumeTask, requestID string, projection manualResumeTraceProjection, strictTaskCoverage bool) manualResumeCommitCoverageResult {
	result := manualResumeCommitCoverageResult{Valid: true, AllDelivered: true}
	conversationID := int64(0)
	if task != nil {
		conversationID = task.ConversationID
	}
	expectedTextTaskIDs := make([]string, 0)
	expectedResourceTaskIDs := make(map[string]string)
	expectedVisibleTaskIDs := make(map[string]struct{})
	for _, task := range projection.Runtime.Pipeline.ReplyPlan.TaskPlans {
		taskID := strings.TrimSpace(task.TaskID)
		outputKind := strings.TrimSpace(task.OutputKind)
		if outputKind == "text" && task.ReplyRequired {
			if strictTaskCoverage && taskID == "" {
				result.Valid = false
				result.HasVisibleTasks = true
				return result
			}
			expectedTextTaskIDs = append(expectedTextTaskIDs, taskID)
			if taskID != "" {
				if _, duplicate := expectedVisibleTaskIDs[taskID]; strictTaskCoverage && duplicate {
					result.Valid = false
					result.HasVisibleTasks = true
					return result
				}
				expectedVisibleTaskIDs[taskID] = struct{}{}
			}
		}
		if outputKind == "resource" || task.NeedsResource || strings.TrimSpace(task.ResourceAction) != "" {
			if resourceType := manualResumeResourceType(task.ResourceAction, task.SubIntent); resourceType != "" {
				if strictTaskCoverage && taskID == "" {
					result.Valid = false
					result.HasVisibleTasks = true
					return result
				}
				expectedResourceTaskIDs[taskID] = resourceType
				if taskID != "" {
					if _, duplicate := expectedVisibleTaskIDs[taskID]; strictTaskCoverage && duplicate {
						result.Valid = false
						result.HasVisibleTasks = true
						return result
					}
					expectedVisibleTaskIDs[taskID] = struct{}{}
				}
			}
		}
	}
	result.HasVisibleTasks = len(expectedTextTaskIDs) > 0 || len(expectedResourceTaskIDs) > 0
	coveredTaskIDs := make(map[string]struct{})
	coveredResourceTypes := make(map[string]struct{})
	legacyTextMessageCount := 0
	hasPluralTaskIDs := false
	for _, item := range projection.Runtime.Output.CommitMessages {
		if strings.TrimSpace(item.Status) != "sent" || item.MessageID <= 0 {
			result.Valid = false
			return result
		}
		deliveryState := manualResumeAIMessageTraceDeliveryState(conversationID, requestID, item, strictTaskCoverage)
		if deliveryState == manualResumeMessageDeliveryUnavailable {
			result.Valid = false
			return result
		}
		if deliveryState == manualResumeMessageDeliveryPending {
			result.HasPendingDelivery = true
			result.AllDelivered = false
		}
		if deliveryState == manualResumeMessageDeliveryUncertain {
			result.HasUncertainDelivery = true
			result.AllDelivered = false
		}
		if deliveryState == manualResumeMessageDeliveryFailed {
			result.HasFailedDelivery = true
			result.AllDelivered = false
		}
		if len(item.TaskIDs) > 0 {
			hasPluralTaskIDs = true
		}
		messageTaskIDs := append([]string(nil), item.TaskIDs...)
		if !strictTaskCoverage {
			messageTaskIDs = append(messageTaskIDs, item.TaskID)
		}
		seenMessageTaskIDs := make(map[string]struct{}, len(messageTaskIDs))
		for _, taskID := range messageTaskIDs {
			if taskID = strings.TrimSpace(taskID); taskID != "" {
				if _, duplicate := seenMessageTaskIDs[taskID]; strictTaskCoverage && duplicate {
					result.Valid = false
					return result
				}
				seenMessageTaskIDs[taskID] = struct{}{}
				if strictTaskCoverage {
					if _, expected := expectedVisibleTaskIDs[taskID]; !expected {
						result.Valid = false
						return result
					}
					if _, duplicate := coveredTaskIDs[taskID]; duplicate {
						result.Valid = false
						return result
					}
				}
				coveredTaskIDs[taskID] = struct{}{}
			}
		}
		resourceTypes := manualResumeCommitResourceTypes(item)
		visibleCommit := false
		if len(resourceTypes) > 0 {
			for _, resourceType := range resourceTypes {
				coveredResourceTypes[resourceType] = struct{}{}
			}
			visibleCommit = true
		} else if strings.TrimSpace(item.MessageType) == "" || strings.TrimSpace(item.MessageType) == string(enums.IMMessageTypeText) {
			if strings.TrimSpace(item.Content) != aiManualResumeNotice {
				if strictTaskCoverage && strings.TrimSpace(item.ResourceType) == "" && len(item.TaskIDs) == 0 {
					result.Valid = false
					return result
				}
				legacyTextMessageCount++
				if strings.TrimSpace(item.Content) != "" {
					visibleCommit = true
				}
			}
		} else {
			visibleCommit = true
		}
		if visibleCommit {
			result.HasVisibleCommit = true
			if deliveryState == manualResumeMessageDeliverySent {
				result.HasDeliveredVisibleCommit = true
			}
		}
	}
	for _, resourceType := range expectedResourceTaskIDs {
		if _, ok := coveredResourceTypes[resourceType]; !ok {
			return result
		}
	}
	if strictTaskCoverage {
		for taskID := range expectedVisibleTaskIDs {
			if _, ok := coveredTaskIDs[taskID]; !ok {
				return result
			}
		}
		result.TaskCoverageComplete = true
		return result
	}
	missingTextTasks := 0
	for _, taskID := range expectedTextTaskIDs {
		if _, ok := coveredTaskIDs[taskID]; !ok {
			missingTextTasks++
		}
	}
	if missingTextTasks == 0 {
		result.TaskCoverageComplete = true
		return result
	}
	if hasPluralTaskIDs {
		return result
	}
	expectedLegacyMessages := len(expectedTextTaskIDs)
	if expectedLegacyMessages > 3 {
		expectedLegacyMessages = 3
	}
	result.TaskCoverageComplete = legacyTextMessageCount >= expectedLegacyMessages
	return result
}

func manualResumeCommitResourceTypes(item manualResumeCommitMessage) []string {
	if resourceType := strings.TrimSpace(item.ResourceType); resourceType != "" {
		return []string{resourceType}
	}
	resourceTypes := make([]string, 0, len(item.FallbackResourceTypes)+1)
	resourceTypes = append(resourceTypes, item.FallbackResourceType)
	resourceTypes = append(resourceTypes, item.FallbackResourceTypes...)
	ret := make([]string, 0, len(resourceTypes))
	seen := make(map[string]struct{}, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		resourceType = strings.TrimSpace(resourceType)
		if resourceType == "" {
			continue
		}
		if _, exists := seen[resourceType]; exists {
			continue
		}
		seen[resourceType] = struct{}{}
		ret = append(ret, resourceType)
	}
	return ret
}

func manualResumeRunUsesStrictTaskCoverage(task *models.AIManualResumeTask, requestID string) bool {
	requestID = strings.TrimSpace(requestID)
	return task != nil && requestID != "" && requestID == strings.TrimSpace(manualResumeRequestID(task)) &&
		requestID != strings.TrimSpace(manualResumeLegacyRequestID(task))
}

func (s *aiManualResumeTaskService) manualResumeAnswerThenHandoffCommitsComplete(task *models.AIManualResumeTask, requestID string, projection manualResumeTraceProjection) bool {
	dispositions, valid := manualResumeJudgeDispositions(projection)
	if !valid {
		return false
	}
	for taskID, disposition := range dispositions {
		if strings.TrimSpace(disposition) != "answer_then_handoff" {
			continue
		}
		if !s.manualResumeTraceHasCustomerVisibleTaskCommit(task, requestID, projection, taskID) {
			return false
		}
	}
	return true
}

func (s *aiManualResumeTaskService) manualResumeTraceHasCustomerVisibleTaskCommit(task *models.AIManualResumeTask, requestID string, projection manualResumeTraceProjection, expectedTaskID string) bool {
	if task == nil || task.ConversationID <= 0 || strings.TrimSpace(requestID) == "" || strings.TrimSpace(expectedTaskID) == "" {
		return false
	}
	expectedTaskID = strings.TrimSpace(expectedTaskID)
	strictTaskCoverage := manualResumeRunUsesStrictTaskCoverage(task, requestID)
	for _, item := range projection.Runtime.Output.CommitMessages {
		if strings.TrimSpace(item.Status) != "sent" || item.MessageID <= 0 ||
			!manualResumeAIMessageMatchesTrace(task.ConversationID, requestID, item, strictTaskCoverage) {
			continue
		}
		visible := strings.TrimSpace(item.ResourceType) != "" ||
			(strings.TrimSpace(item.MessageType) != "" && strings.TrimSpace(item.MessageType) != string(enums.IMMessageTypeText)) ||
			(strings.TrimSpace(item.Content) != "" && strings.TrimSpace(item.Content) != aiManualResumeNotice)
		if !visible {
			continue
		}
		messageTaskIDs := append([]string(nil), item.TaskIDs...)
		if !strictTaskCoverage {
			messageTaskIDs = append(messageTaskIDs, item.TaskID)
		}
		for _, taskID := range messageTaskIDs {
			if strings.TrimSpace(taskID) == expectedTaskID {
				return true
			}
		}
	}
	return false
}

func manualResumeResourceType(resourceAction string, subIntent string) string {
	switch strings.TrimSpace(resourceAction) {
	case "provide_location", "send_location":
		return "location"
	case "provide_mini_program", "send_miniprogram", "send_mini_program":
		return "mini_program"
	case "provide_phone", "send_phone":
		return "phone"
	}
	switch strings.TrimSpace(subIntent) {
	case "location", "mini_program", "phone":
		return strings.TrimSpace(subIntent)
	default:
		return ""
	}
}

type manualResumeMessageDeliveryState string

const (
	manualResumeMessageDeliveryUnavailable manualResumeMessageDeliveryState = "unavailable"
	manualResumeMessageDeliveryPending     manualResumeMessageDeliveryState = "pending"
	manualResumeMessageDeliveryUncertain   manualResumeMessageDeliveryState = "uncertain"
	manualResumeMessageDeliveryFailed      manualResumeMessageDeliveryState = "failed"
	manualResumeMessageDeliverySent        manualResumeMessageDeliveryState = "sent"
)

func manualResumeAIMessageMatchesTrace(conversationID int64, requestID string, item manualResumeCommitMessage, strict bool) bool {
	return manualResumeAIMessageTraceDeliveryState(conversationID, requestID, item, strict) == manualResumeMessageDeliverySent
}

func manualResumeAIMessageTraceDeliveryState(conversationID int64, requestID string, item manualResumeCommitMessage, strict bool) manualResumeMessageDeliveryState {
	if conversationID <= 0 || item.MessageID <= 0 || strings.TrimSpace(requestID) == "" {
		return manualResumeMessageDeliveryUnavailable
	}
	message := MessageService.Get(item.MessageID)
	if message == nil {
		return manualResumeMessageDeliveryUnavailable
	}
	invalidMessage := message.ConversationID != conversationID || message.SenderType != enums.IMSenderTypeAI ||
		strings.TrimSpace(message.RequestID) != strings.TrimSpace(requestID) || message.RecalledAt != nil || message.SendStatus == enums.IMMessageStatusRecalled
	if invalidMessage {
		return manualResumeMessageDeliveryUnavailable
	}
	if strict {
		if messageType := strings.TrimSpace(item.MessageType); messageType != "" && string(message.MessageType) != messageType {
			return manualResumeMessageDeliveryUnavailable
		}
		if strings.TrimSpace(message.Content) != strings.TrimSpace(item.Content) {
			return manualResumeMessageDeliveryUnavailable
		}
	}
	return manualResumeMessageDelivery(message)
}

func manualResumeMessageReachedCustomer(message *models.Message) bool {
	return manualResumeMessageDelivery(message) == manualResumeMessageDeliverySent
}

func manualResumeMessageDelivery(message *models.Message) manualResumeMessageDeliveryState {
	if message == nil || message.ID <= 0 || message.ConversationID <= 0 {
		return manualResumeMessageDeliveryUnavailable
	}
	db := sqls.DB()
	if db == nil || !db.Migrator().HasTable(&models.Conversation{}) {
		return manualResumeMessageDeliverySent
	}
	conversation := repositories.ConversationRepository.Get(db, message.ConversationID)
	if conversation == nil {
		return manualResumeMessageDeliveryUnavailable
	}
	if conversation.ChannelID <= 0 {
		return manualResumeMessageDeliverySent
	}
	if !db.Migrator().HasTable(&models.Channel{}) || !db.Migrator().HasTable(&models.ChannelMessageOutbox{}) {
		return manualResumeMessageDeliveryUnavailable
	}
	channel := repositories.ChannelRepository.Get(db, conversation.ChannelID)
	if channel == nil {
		return manualResumeMessageDeliveryUnavailable
	}
	switch strings.TrimSpace(channel.ChannelType) {
	case enums.ChannelTypeWxWorkKF, enums.ChannelTypeWxWorkCLI, enums.ChannelTypeWxWorkProtocol:
		outbox := repositories.ChannelMessageOutboxRepository.Take(db, "channel_type = ? AND message_id = ?", channel.ChannelType, message.ID)
		if outbox == nil {
			return manualResumeMessageDeliveryUnavailable
		}
		switch strings.TrimSpace(outbox.SendStatus) {
		case string(enums.ChannelMessageOutboxStatusSent):
			return manualResumeMessageDeliverySent
		case string(enums.ChannelMessageOutboxStatusPending):
			return manualResumeMessageDeliveryPending
		case string(enums.ChannelMessageOutboxStatusSending):
			if !outbox.UpdatedAt.IsZero() && time.Since(outbox.UpdatedAt) >= aiManualResumeDeliveryUncertainAfter {
				return manualResumeMessageDeliveryUncertain
			}
			return manualResumeMessageDeliveryPending
		case string(enums.ChannelMessageOutboxStatusFailed):
			if outbox.NextRetryAt != nil {
				return manualResumeMessageDeliveryPending
			}
			return manualResumeMessageDeliveryFailed
		case string(enums.ChannelMessageOutboxStatusCancelled):
			if strings.HasPrefix(strings.TrimSpace(outbox.LastError), channelMessageOutboxDispatchUncertainReasonPrefix) {
				return manualResumeMessageDeliveryUncertain
			}
			return manualResumeMessageDeliveryUnavailable
		default:
			return manualResumeMessageDeliveryUnavailable
		}
	default:
		return manualResumeMessageDeliverySent
	}
}

func (s *aiManualResumeTaskService) manualResumeSources(task *models.AIManualResumeTask, waitingMessages []models.Message) []manualResumeSource {
	snapshot, _ := s.manualResumeExecutionSnapshot(task, waitingMessages)
	return manualResumeSourcesFromSnapshot(snapshot)
}

func (s *aiManualResumeTaskService) manualResumeExecutionSnapshot(task *models.AIManualResumeTask, waitingMessages []models.Message) (replyruntime.ManualResumeSnapshot, bool) {
	snapshot, state := s.manualResumeExecutionSnapshotState(task, waitingMessages)
	return snapshot, state == manualResumeSnapshotRecoverable
}

func (s *aiManualResumeTaskService) manualResumeExecutionSnapshotState(task *models.AIManualResumeTask, waitingMessages []models.Message) (replyruntime.ManualResumeSnapshot, manualResumeSnapshotState) {
	if task == nil {
		return replyruntime.ManualResumeSnapshot{}, manualResumeSnapshotUnavailable
	}
	origin := MessageService.Get(task.OriginMessageID)
	deferredSnapshot := s.manualResumeDeferredSnapshotState(task)
	runLogID := deferredSnapshot.RunLogID
	deferredTasks := append([]manualResumeDeferredTask(nil), deferredSnapshot.Tasks...)
	tracedSources := append([]replyruntime.ManualResumeSource(nil), deferredSnapshot.Sources...)
	snapshotState := deferredSnapshot.State
	hasDeferredSnapshot := snapshotState == manualResumeSnapshotRecoverable
	originalTurn := s.manualResumeOriginalTurn(origin)
	sources, traceSourcesValidated := s.manualResumeResolvedTraceSources(task, tracedSources, originalTurn)
	traceSourcesInvalid := len(tracedSources) > 0 && len(sources) == 0
	if len(sources) == 0 {
		for _, item := range originalTurn {
			if source, ok := manualResumeSourceFromMessage(item); ok {
				sources = append(sources, source)
			}
		}
		traceSourcesValidated = false
	}
	if traceSourcesInvalid {
		hasDeferredSnapshot = false
		deferredTasks = nil
		snapshotState = manualResumeSnapshotUnavailable
	}

	newSources := make([]manualResumeSource, 0, len(waitingMessages))
	for _, item := range waitingMessages {
		if task.OriginMessageID > 0 && item.ID <= task.OriginMessageID {
			continue
		}
		if source, ok := manualResumeSourceFromMessage(item); ok {
			if !manualResumeSourcesContainMessageID(sources, source.MessageID) {
				sources = append(sources, source)
				newSources = append(newSources, source)
			}
		}
	}

	oldRefToNew := make(map[string]string, len(sources)*2)
	for index := range sources {
		newRef := fmt.Sprintf("U%d", index+1)
		oldRefToNew[newRef] = newRef
		if oldRef := strings.ToUpper(strings.TrimSpace(sources[index].Ref)); oldRef != "" {
			oldRefToNew[oldRef] = newRef
		}
		sources[index].Ref = newRef
	}
	for index := range newSources {
		for _, source := range sources {
			if source.MessageID == newSources[index].MessageID {
				newSources[index].Ref = source.Ref
				break
			}
		}
	}
	legacySourceRepair := false
	originRef := manualResumeSourceRefByMessageID(sources, task.OriginMessageID)
	for taskIndex := range deferredTasks {
		mappedRefs := make([]string, 0, len(deferredTasks[taskIndex].SourceRefs))
		for _, ref := range deferredTasks[taskIndex].SourceRefs {
			mapped := oldRefToNew[strings.ToUpper(strings.TrimSpace(ref))]
			if mapped == "" {
				hasDeferredSnapshot = false
				mappedRefs = nil
				break
			}
			mappedRefs = append(mappedRefs, mapped)
		}
		if !hasDeferredSnapshot {
			break
		}
		if len(mappedRefs) == 0 {
			if originRef == "" {
				hasDeferredSnapshot = false
				break
			}
			mappedRefs = []string{originRef}
			legacySourceRepair = true
		}
		deferredTasks[taskIndex].SourceRefs = mappedRefs
	}

	contractMode := ""
	sourcesValidated := false
	if hasDeferredSnapshot && len(deferredTasks) > 0 {
		contractMode, sourcesValidated = manualResumeSnapshotContract(deferredTasks, sources, traceSourcesValidated && !legacySourceRepair)
		if contractMode == "" {
			hasDeferredSnapshot = false
			deferredTasks = nil
		}
	}
	if !hasDeferredSnapshot {
		deferredTasks = nil
		if snapshotState == manualResumeSnapshotRecoverable {
			snapshotState = manualResumeSnapshotUnavailable
		}
	}

	snapshot := replyruntime.ManualResumeSnapshot{
		RunLogID:         runLogID,
		ContractMode:     contractMode,
		SourcesValidated: sourcesValidated,
		FrozenTasks:      append([]replyruntime.ManualResumeTaskPlan(nil), deferredTasks...),
		Sources:          make([]replyruntime.ManualResumeSource, 0, len(sources)),
		NewSources:       make([]replyruntime.ManualResumeSource, 0, len(newSources)),
	}
	for _, source := range sources {
		snapshot.Sources = append(snapshot.Sources, manualResumeTraceSource(source))
	}
	for _, source := range newSources {
		snapshot.NewSources = append(snapshot.NewSources, manualResumeTraceSource(source))
	}
	return snapshot, snapshotState
}

func (s *aiManualResumeTaskService) manualResumeResolvedTraceSources(task *models.AIManualResumeTask, traced []replyruntime.ManualResumeSource, originalTurn []models.Message) ([]manualResumeSource, bool) {
	if task == nil || len(traced) == 0 {
		return nil, false
	}
	ret := make([]manualResumeSource, 0, len(traced))
	seenRefs := make(map[string]struct{}, len(traced))
	seenMessageIDs := make(map[int64]struct{}, len(traced))
	strict := true
	lastMessageID := int64(0)
	for sourceIndex, tracedSource := range traced {
		ref := strings.ToUpper(strings.TrimSpace(tracedSource.Ref))
		if ref != fmt.Sprintf("U%d", sourceIndex+1) {
			return nil, false
		}
		if _, exists := seenRefs[ref]; exists {
			return nil, false
		}
		seenRefs[ref] = struct{}{}

		messageID := tracedSource.MessageID
		if messageID <= 0 {
			strict = false
			index := manualResumeSourceRefIndex(ref)
			if index < 0 || index >= len(originalTurn) {
				return nil, false
			}
			messageID = originalTurn[index].ID
		}
		if _, exists := seenMessageIDs[messageID]; exists {
			return nil, false
		}
		message := MessageService.Get(messageID)
		if message == nil || message.ConversationID != task.ConversationID || message.SenderType != enums.IMSenderTypeCustomer ||
			message.RecalledAt != nil || message.SendStatus == enums.IMMessageStatusRecalled || message.ID <= lastMessageID {
			return nil, false
		}
		source, ok := manualResumeSourceFromMessage(*message)
		if !ok {
			return nil, false
		}
		source.Ref = ref
		ret = append(ret, source)
		seenMessageIDs[messageID] = struct{}{}
		lastMessageID = message.ID
	}
	if len(ret) == 0 || task.OriginMessageID <= 0 || ret[len(ret)-1].MessageID != task.OriginMessageID {
		return nil, false
	}
	return ret, strict
}

func manualResumeSourceRefIndex(ref string) int {
	ref = strings.ToUpper(strings.TrimSpace(ref))
	if !strings.HasPrefix(ref, "U") {
		return -1
	}
	value, err := strconv.Atoi(strings.TrimPrefix(ref, "U"))
	if err != nil || value <= 0 {
		return -1
	}
	return value - 1
}

func manualResumeSourceRefByMessageID(sources []manualResumeSource, messageID int64) string {
	if messageID <= 0 {
		return ""
	}
	for _, source := range sources {
		if source.MessageID == messageID {
			return strings.ToUpper(strings.TrimSpace(source.Ref))
		}
	}
	return ""
}

func manualResumeSnapshotContract(tasks []manualResumeDeferredTask, sources []manualResumeSource, strictSources bool) (string, bool) {
	if len(tasks) == 0 || !manualResumeTaskPlansHaveValidSources(tasks, sources) {
		return "", false
	}
	if strictSources {
		if manualResumeTaskPlansAreV2(tasks) {
			return replyruntime.ManualResumeContractV2, true
		}
		return "", false
	}
	if manualResumeTaskPlansAreLegacySafe(tasks) {
		return replyruntime.ManualResumeContractLegacy, false
	}
	return "", false
}

func manualResumeTaskPlansHaveValidSources(tasks []manualResumeDeferredTask, sources []manualResumeSource) bool {
	validRefs := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		ref := strings.ToUpper(strings.TrimSpace(source.Ref))
		if ref == "" || source.MessageID <= 0 {
			return false
		}
		validRefs[ref] = struct{}{}
	}
	for _, task := range tasks {
		if len(task.SourceRefs) == 0 {
			return false
		}
		for _, ref := range task.SourceRefs {
			if _, ok := validRefs[strings.ToUpper(strings.TrimSpace(ref))]; !ok {
				return false
			}
		}
	}
	return true
}

func manualResumeTaskPlansAreV2(tasks []manualResumeDeferredTask) bool {
	seenTaskIDs := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			return false
		}
		if _, exists := seenTaskIDs[taskID]; exists {
			return false
		}
		seenTaskIDs[taskID] = struct{}{}
		if !manualResumeValidIntent(task.Intent) ||
			!manualResumeValidObjective(task.Objective) ||
			!manualResumeValidRelation(task.RelationToPrevious) ||
			!manualResumeValidResolution(task.ResolutionState) ||
			strings.TrimSpace(manualResumeFirstNonEmptyText(task.OriginalText, task.Text)) == "" ||
			strings.TrimSpace(task.ResolvedText) == "" {
			return false
		}
	}
	return true
}

func manualResumeFirstNonEmptyText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func manualResumeTaskPlansAreLegacySafe(tasks []manualResumeDeferredTask) bool {
	seenTaskIDs := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" || !manualResumeValidIntent(task.Intent) || strings.TrimSpace(manualResumeDeferredTaskText(task)) == "" {
			return false
		}
		if _, exists := seenTaskIDs[taskID]; exists {
			return false
		}
		seenTaskIDs[taskID] = struct{}{}
	}
	return true
}

func manualResumeValidIntent(value string) bool {
	switch strings.TrimSpace(value) {
	case "hotel_info", "service_request", "human_complaint_risk", "interaction", "hotel_variable":
		return true
	default:
		return false
	}
}

func manualResumeValidObjective(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "availability", "quantity", "location", "price", "time", "policy", "method", "explanation", "recommendation", "identity", "general_guidance", "compound_information", "action_request", "status", "modify", "cancel", "confirm", "complaint", "social", "unknown":
		return true
	default:
		return false
	}
}

func manualResumeValidRelation(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "independent", "follow_up", "clarification_answer", "reference_previous", "correction", "modify_previous", "cancel_previous", "answer_rejected":
		return true
	default:
		return false
	}
}

func manualResumeValidResolution(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "clear", "resolved_from_context", "ambiguous", "unresolved":
		return true
	default:
		return false
	}
}

func manualResumeSourcesFromSnapshot(snapshot replyruntime.ManualResumeSnapshot) []manualResumeSource {
	return manualResumeSourcesFromTrace(snapshot.Sources)
}

func manualResumeSourcesFromTrace(sources []replyruntime.ManualResumeSource) []manualResumeSource {
	ret := make([]manualResumeSource, 0, len(sources))
	for _, source := range sources {
		if converted, ok := manualResumeSourceFromTrace(source); ok {
			ret = append(ret, converted)
		}
	}
	return ret
}

func manualResumeSourceFromTrace(source replyruntime.ManualResumeSource) (manualResumeSource, bool) {
	text := strings.TrimSpace(source.Text)
	if text == "" {
		return manualResumeSource{}, false
	}
	return manualResumeSource{
		Ref:         strings.ToUpper(strings.TrimSpace(source.Ref)),
		MessageID:   source.MessageID,
		MessageType: enums.IMMessageType(strings.TrimSpace(source.MessageType)),
		Text:        text,
	}, true
}

func manualResumeTraceSource(source manualResumeSource) replyruntime.ManualResumeSource {
	return replyruntime.ManualResumeSource{
		Ref:         source.Ref,
		MessageID:   source.MessageID,
		MessageType: string(source.MessageType),
		Text:        source.Text,
	}
}

func manualResumeSourcesContainMessageID(sources []manualResumeSource, messageID int64) bool {
	if messageID <= 0 {
		return false
	}
	for _, source := range sources {
		if source.MessageID == messageID {
			return true
		}
	}
	return false
}

func appendManualResumeSourceText(current string, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if current == "" {
		return next
	}
	if next == "" || current == next {
		return current
	}
	for _, line := range strings.Split(current, "\n") {
		if strings.TrimSpace(line) == next {
			return current
		}
	}
	return current + "\n" + next
}

func (s *aiManualResumeTaskService) manualResumeDeferredSnapshot(task *models.AIManualResumeTask) (int64, []manualResumeDeferredTask, []replyruntime.ManualResumeSource, bool) {
	result := s.manualResumeDeferredSnapshotState(task)
	return result.RunLogID, result.Tasks, result.Sources, result.State == manualResumeSnapshotRecoverable
}

func (s *aiManualResumeTaskService) manualResumeDeferredSnapshotState(task *models.AIManualResumeTask) manualResumeDeferredSnapshotResult {
	unavailable := manualResumeDeferredSnapshotResult{State: manualResumeSnapshotUnavailable}
	if task == nil || task.OriginMessageID <= 0 {
		return unavailable
	}
	runLogs := AgentRunLogService.Find(sqls.NewCnd().
		Eq("conversation_id", task.ConversationID).
		Eq("message_id", task.OriginMessageID).
		Desc("id").
		Limit(50))
	for _, runLog := range runLogs {
		if strings.TrimSpace(runLog.TraceData) == "" {
			if manualResumeRunLogBlocksOlderSnapshot(runLog, nil) {
				return unavailable
			}
			continue
		}
		var projection manualResumeTraceProjection
		if err := json.Unmarshal([]byte(runLog.TraceData), &projection); err != nil {
			if manualResumeRunLogBlocksOlderSnapshot(runLog, nil) {
				return unavailable
			}
			continue
		}
		tracedSources := append([]replyruntime.ManualResumeSource(nil), projection.Runtime.Input.CurrentTurnSources...)
		deferredIDs := projection.Runtime.Pipeline.EvidenceJudge.DeferredTaskIDs
		if len(deferredIDs) == 0 {
			commitComplete, hasVisibleTasks, hasCustomerVisibleCommit := s.manualResumeRunTraceCommitCoverage(task, runLog.RequestID, projection)
			if commitComplete && hasVisibleTasks && hasCustomerVisibleCommit {
				return manualResumeDeferredSnapshotResult{
					RunLogID: runLog.ID,
					Sources:  tracedSources,
					State:    manualResumeSnapshotSettled,
				}
			}
		}
		if strings.TrimSpace(runLog.ErrorMessage) != "" || strings.ToLower(strings.TrimSpace(runLog.FinalStatus)) == "error" {
			continue
		}
		if !manualResumeTraceIsAuthoritative(projection) {
			if manualResumeRunLogBlocksOlderSnapshot(runLog, &projection) {
				return unavailable
			}
			continue
		}
		if len(deferredIDs) == 0 {
			if manualResumeTraceIsSettledHandoff(projection) {
				return manualResumeDeferredSnapshotResult{
					RunLogID: runLog.ID,
					Sources:  tracedSources,
					State:    manualResumeSnapshotSettled,
				}
			}
			runStatus := strings.ToLower(strings.TrimSpace(runLog.FinalStatus))
			commitComplete, hasVisibleTasks, hasCustomerVisibleCommit := s.manualResumeRunTraceCommitCoverage(task, runLog.RequestID, projection)
			if (runStatus == "completed" || runStatus == "fallback") && commitComplete && (hasVisibleTasks || hasCustomerVisibleCommit) && manualResumeRunTraceCommitted(projection) {
				return manualResumeDeferredSnapshotResult{
					RunLogID: runLog.ID,
					Sources:  tracedSources,
					State:    manualResumeSnapshotSettled,
				}
			}
			return unavailable
		}
		deferredOrder := make([]string, 0, len(deferredIDs))
		deferredSet := make(map[string]struct{}, len(deferredIDs))
		for _, taskID := range deferredIDs {
			taskID = strings.TrimSpace(taskID)
			if taskID == "" {
				return unavailable
			}
			if _, exists := deferredSet[taskID]; exists {
				return unavailable
			}
			deferredSet[taskID] = struct{}{}
			deferredOrder = append(deferredOrder, taskID)
		}
		if len(deferredSet) == 0 {
			return unavailable
		}
		plansByTaskID := make(map[string]manualResumeDeferredTask, len(deferredSet))
		for _, taskPlan := range projection.Runtime.Pipeline.ReplyPlan.TaskPlans {
			taskID := strings.TrimSpace(taskPlan.TaskID)
			if _, ok := deferredSet[taskID]; ok {
				if _, duplicate := plansByTaskID[taskID]; duplicate {
					return unavailable
				}
				plansByTaskID[taskID] = taskPlan
			}
		}
		if len(plansByTaskID) != len(deferredSet) {
			return unavailable
		}
		dispositions, dispositionsValid := manualResumeJudgeDispositions(projection)
		if !dispositionsValid {
			return unavailable
		}
		recoverable := make([]manualResumeDeferredTask, 0, len(deferredOrder))
		settledCount := 0
		settledRequiresCommit := false
		for _, taskID := range deferredOrder {
			plan := plansByTaskID[taskID]
			switch manualResumeDeferredTaskState(plan, dispositions[taskID]) {
			case manualResumeSnapshotRecoverable:
				recoverable = append(recoverable, plan)
			case manualResumeSnapshotSettled:
				settledCount++
				if strings.TrimSpace(plan.OutputKind) == "text" && plan.ReplyRequired {
					settledRequiresCommit = true
				}
			default:
				return unavailable
			}
		}
		coverage := s.manualResumeRunTraceCommitCoverageStateWithMode(task, runLog.RequestID, projection, true)
		if coverage.HasVisibleTasks {
			if !coverage.Valid || !coverage.TaskCoverageComplete || !coverage.HasVisibleCommit {
				return unavailable
			}
			if coverage.HasUncertainDelivery {
				return manualResumeDeferredSnapshotResult{
					RunLogID: runLog.ID,
					Tasks:    recoverable,
					Sources:  tracedSources,
					State:    manualResumeSnapshotDeliveryUncertain,
				}
			}
			if coverage.HasFailedDelivery {
				return manualResumeDeferredSnapshotResult{
					RunLogID: runLog.ID,
					Tasks:    recoverable,
					Sources:  tracedSources,
					State:    manualResumeSnapshotDeliveryFailed,
				}
			}
			if coverage.HasPendingDelivery {
				return manualResumeDeferredSnapshotResult{
					RunLogID: runLog.ID,
					Tasks:    recoverable,
					Sources:  tracedSources,
					State:    manualResumeSnapshotDeliveryPending,
				}
			}
			if !coverage.AllDelivered || !coverage.HasDeliveredVisibleCommit {
				return unavailable
			}
		}
		if len(recoverable) > 0 {
			return manualResumeDeferredSnapshotResult{
				RunLogID: runLog.ID,
				Tasks:    recoverable,
				Sources:  tracedSources,
				State:    manualResumeSnapshotRecoverable,
			}
		}
		if settledCount == len(deferredOrder) {
			if settledRequiresCommit && !coverage.HasVisibleTasks {
				return unavailable
			}
			return manualResumeDeferredSnapshotResult{
				RunLogID: runLog.ID,
				Sources:  tracedSources,
				State:    manualResumeSnapshotSettled,
			}
		}
		return unavailable
	}
	return unavailable
}

func manualResumeJudgeDispositions(projection manualResumeTraceProjection) (map[string]string, bool) {
	ret := make(map[string]string, len(projection.Runtime.Pipeline.EvidenceJudge.Tasks))
	for _, task := range projection.Runtime.Pipeline.EvidenceJudge.Tasks {
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			return nil, false
		}
		if _, exists := ret[taskID]; exists {
			return nil, false
		}
		ret[taskID] = strings.TrimSpace(task.Disposition)
	}
	return ret, true
}

func manualResumeDeferredTaskState(task manualResumeDeferredTask, disposition string) manualResumeSnapshotState {
	output := strings.TrimSpace(task.Output)
	outputKind := strings.TrimSpace(task.OutputKind)
	intent := strings.TrimSpace(task.Intent)
	switch strings.TrimSpace(disposition) {
	case "no_evidence_handoff":
		if output == replyruntime.ManualResumeDeferredKnowledgeOutput && outputKind == "handoff" && !task.ReplyRequired && task.NeedsKnowledge && (intent == "hotel_info" || intent == "service_request") {
			return manualResumeSnapshotRecoverable
		}
		return manualResumeSnapshotUnavailable
	case "knowledge_direct_handoff":
		if outputKind == "handoff" && !task.ReplyRequired && task.NeedsKnowledge {
			return manualResumeSnapshotSettled
		}
		return manualResumeSnapshotUnavailable
	case "answer_then_handoff":
		if output == "knowledge_text_reply" && outputKind == "text" && task.ReplyRequired {
			return manualResumeSnapshotSettled
		}
		return manualResumeSnapshotUnavailable
	case "answer":
		return manualResumeSnapshotUnavailable
	case "":
		// Older traces did not persist dispositions. Keep their structural contract
		// for a bounded rollout window, without allowing it to override new values.
	default:
		return manualResumeSnapshotUnavailable
	}
	if output == replyruntime.ManualResumeDeferredKnowledgeOutput {
		if outputKind == "handoff" && !task.ReplyRequired && task.NeedsKnowledge && (intent == "hotel_info" || intent == "service_request") {
			return manualResumeSnapshotRecoverable
		}
		return manualResumeSnapshotUnavailable
	}
	if strings.TrimSpace(task.SubIntent) == "explicit_handoff" && outputKind == "handoff" {
		return manualResumeSnapshotSettled
	}
	if output == "knowledge_text_reply" && outputKind == "text" && task.ReplyRequired && len(task.MissingAspects) > 0 {
		return manualResumeSnapshotSettled
	}
	return manualResumeSnapshotUnavailable
}

func manualResumeTraceIsSettledHandoff(projection manualResumeTraceProjection) bool {
	dispositions, valid := manualResumeJudgeDispositions(projection)
	if !valid {
		return false
	}
	found := false
	for _, task := range projection.Runtime.Pipeline.ReplyPlan.TaskPlans {
		if strings.TrimSpace(task.OutputKind) == "context_only" {
			continue
		}
		isExplicit := strings.TrimSpace(task.SubIntent) == "explicit_handoff" && strings.TrimSpace(task.OutputKind) == "handoff"
		isKnowledgeDirect := strings.TrimSpace(dispositions[strings.TrimSpace(task.TaskID)]) == "knowledge_direct_handoff" &&
			strings.TrimSpace(task.OutputKind) == "handoff" && task.NeedsKnowledge
		if !isExplicit && !isKnowledgeDirect {
			return false
		}
		found = true
	}
	return found
}

func manualResumeRunLogBlocksOlderSnapshot(runLog models.AgentRunLog, projection *manualResumeTraceProjection) bool {
	if strings.TrimSpace(runLog.ErrorMessage) != "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(runLog.FinalStatus)) {
	case "completed", "interrupted", "fallback", "expired":
		return true
	case "error", "started":
		return false
	}
	if projection == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(projection.Runtime.Status)) {
	case "completed", "interrupted", "fallback":
		return strings.TrimSpace(projection.Runtime.Error.Stage) == ""
	default:
		return false
	}
}

func manualResumeTraceIsAuthoritative(projection manualResumeTraceProjection) bool {
	switch strings.ToLower(strings.TrimSpace(projection.Runtime.Status)) {
	case "error", "started":
		return false
	case "completed", "interrupted", "fallback":
		return strings.TrimSpace(projection.Runtime.Error.Stage) == ""
	}
	if strings.TrimSpace(projection.Runtime.Error.Stage) != "" {
		return false
	}
	if len(projection.Runtime.Pipeline.EvidenceJudge.DeferredTaskIDs) > 0 &&
		len(projection.Runtime.Pipeline.ReplyPlan.TaskPlans) > 0 {
		return true
	}
	return strings.TrimSpace(projection.Runtime.Output.FinishReason) != ""
}

func (s *aiManualResumeTaskService) manualResumeOriginalTurn(origin *models.Message) []models.Message {
	if origin == nil || origin.ID <= 0 || origin.SenderType != enums.IMSenderTypeCustomer || origin.SentAt == nil {
		return nil
	}
	cnd := sqls.NewCnd().
		Eq("conversation_id", origin.ConversationID).
		Eq("session_no", origin.SessionNo).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		In("message_type", []string{
			string(enums.IMMessageTypeText),
			string(enums.IMMessageTypeVoice),
			string(enums.IMMessageTypeImage),
			string(enums.IMMessageTypeLocation),
			string(enums.IMMessageTypeMiniProgram),
			string(enums.IMMessageTypeAttachment),
		}).
		Lte("id", origin.ID).
		Desc("id").
		Limit(12)
	if latestOutbound := MessageService.FindOne(sqls.NewCnd().
		Eq("conversation_id", origin.ConversationID).
		Eq("session_no", origin.SessionNo).
		In("sender_type", []string{string(enums.IMSenderTypeAI), string(enums.IMSenderTypeAgent)}).
		Lt("id", origin.ID).
		Desc("id")); latestOutbound != nil {
		cnd.Gt("id", latestOutbound.ID)
	}
	items := MessageService.Find(cnd)
	selected := make([]models.Message, 0, len(items))
	newerAt := *origin.SentAt
	for _, item := range items {
		if item.ID > origin.ID || item.SentAt == nil || newerAt.Sub(*item.SentAt) > 8*time.Second {
			break
		}
		selected = append(selected, item)
		newerAt = *item.SentAt
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return selected
}

func manualResumeTaskSourceMessage(task manualResumeDeferredTask, originalTurn []models.Message, origin *models.Message) *models.Message {
	if len(task.SourceRefs) > 0 {
		ref := strings.ToUpper(strings.TrimSpace(task.SourceRefs[0]))
		if strings.HasPrefix(ref, "U") {
			if sourceIndex, err := strconv.Atoi(strings.TrimPrefix(ref, "U")); err == nil && sourceIndex > 0 && sourceIndex <= len(originalTurn) {
				item := originalTurn[sourceIndex-1]
				return &item
			}
		}
	}
	return origin
}

func manualResumeDeferredTaskText(task manualResumeDeferredTask) string {
	if strings.TrimSpace(task.ResolutionState) == "resolved_from_context" {
		if text := strings.TrimSpace(task.ResolvedText); text != "" {
			return text
		}
	}
	for _, text := range []string{task.OriginalText, task.Text, task.ResolvedText} {
		if text = strings.TrimSpace(text); text != "" {
			return text
		}
	}
	return ""
}

func manualResumeSourceFromMessage(message models.Message) (manualResumeSource, bool) {
	if message.RecalledAt != nil || message.SendStatus == enums.IMMessageStatusRecalled ||
		isConsumedHandoffConfirmationMessage(message) ||
		(message.SenderType == enums.IMSenderTypeCustomer && utils.IsStandaloneOneTextControl(message.MessageType, message.Content)) {
		return manualResumeSource{}, false
	}
	text := manualResumeMessageText(message)
	if text == "" {
		return manualResumeSource{}, false
	}
	return manualResumeSource{MessageID: message.ID, MessageType: message.MessageType, Text: text}, true
}

func manualResumeMessageText(message models.Message) string {
	if message.MessageType == enums.IMMessageTypeVoice {
		mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
		if strings.TrimSpace(status) != "understood" {
			return ""
		}
		if text := strings.TrimSpace(mediaText); text != "" {
			return text
		}
		return strings.TrimSpace(mediaSummary)
	}
	return strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
}

func manualResumeSourcePart(source manualResumeSource, index int) string {
	label := "消息"
	switch source.MessageType {
	case enums.IMMessageTypeImage:
		label = "图片"
	case enums.IMMessageTypeVoice:
		label = "语音"
	case enums.IMMessageTypeAttachment:
		label = "文件"
	case enums.IMMessageTypeLocation:
		label = "定位"
	case enums.IMMessageTypeMiniProgram:
		label = "小程序"
	case enums.IMMessageTypeGIF:
		label = "表情"
	}
	if source.MessageID > 0 {
		return fmt.Sprintf("%d. [%s%d] %s", index, label, source.MessageID, strings.TrimSpace(source.Text))
	}
	return fmt.Sprintf("%d. [%s] %s", index, label, strings.TrimSpace(source.Text))
}

func (s *aiManualResumeTaskService) aiReplyEnabled(wxWorkInstanceID int64) bool {
	if wxWorkInstanceID <= 0 {
		return true
	}
	instance := WxWorkProtocolInstanceService.Get(wxWorkInstanceID)
	return instance == nil || instance.AIReplyEnabled
}

func (s *aiManualResumeTaskService) awaitCommittedDelivery(task *models.AIManualResumeTask, now time.Time) error {
	if task == nil || sqls.DB() == nil {
		return fmt.Errorf("manual resume task or database is unavailable")
	}
	_, err := updateCurrentManualResumeTask(sqls.DB(), task, []string{aiManualResumeTaskRunning}, map[string]any{
		"task_status":      aiManualResumeTaskRetry,
		"next_retry_at":    now.Add(aiManualResumeDeliveryReconcileDelay),
		"last_error":       aiManualResumeAwaitingDeliveryMarker,
		"updated_at":       now,
		"update_user_name": "system",
	})
	return err
}

func (s *aiManualResumeTaskService) holdUncertainDelivery(task *models.AIManualResumeTask, now time.Time) error {
	return s.holdTerminalDelivery(task, now,
		aiManualResumeDeliveryUncertainMarker,
		"AI续答已提交，但外部投递结果无法确认，需要人工核对",
		"manual_resume_delivery_uncertain",
		"delivery_uncertain",
	)
}

func (s *aiManualResumeTaskService) holdFailedDelivery(task *models.AIManualResumeTask, now time.Time) error {
	return s.holdTerminalDelivery(task, now,
		aiManualResumeDeliveryFailedMarker,
		"AI续答已生成，但外部投递失败，需要人工继续处理",
		"manual_resume_delivery_failed",
		"delivery_failed",
	)
}

func (s *aiManualResumeTaskService) holdTerminalDelivery(task *models.AIManualResumeTask, now time.Time, marker string, reason string, action string, noticeSuffix string) error {
	if task == nil || sqls.DB() == nil {
		return fmt.Errorf("manual resume task or database is unavailable")
	}
	updated := false
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		state, err := ConversationRouteService.lockByConversationIDWithDB(ctx.Tx, task.ConversationID)
		if err != nil {
			return err
		}
		taskUpdated, err := updateCurrentManualResumeTask(ctx.Tx, task, []string{aiManualResumeTaskRunning}, map[string]any{
			"task_status":      aiManualResumeTaskFailed,
			"next_retry_at":    nil,
			"completed_at":     now,
			"last_error":       marker,
			"updated_at":       now,
			"update_user_name": "system",
		})
		if err != nil {
			return err
		}
		if !taskUpdated {
			return nil
		}
		updates := map[string]any{
			"manual_expire_at":     nil,
			"need_human_follow_up": true,
			"handoff_reason":       reason,
			"updated_at":           now,
			"update_user_name":     "system",
		}
		if !routeStatusBlocksManualResume(state.RouteStatus) {
			updates["route_status"] = enums.ConversationRouteStatusStoreWecomManual
			updates["route_target"] = "store_wecom"
		}
		if err := repositories.ConversationRouteStateRepository.Updates(ctx.Tx, state.ID, updates); err != nil {
			return err
		}
		if err := ConversationEventLogService.CreateEvent(ctx, task.ConversationID, enums.IMEventTypeTransfer, enums.IMSenderTypeSystem, 0,
			reason, ConversationService.buildEventPayload(map[string]any{
				"action":  action,
				"taskKey": task.TaskKey,
			})); err != nil {
			return err
		}
		updated = true
		return nil
	})
	if err != nil {
		return err
	}
	if !updated {
		return nil
	}
	if conversation := ConversationService.Get(task.ConversationID); conversation != nil {
		WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationUpdated)
	}
	if state := ConversationRouteService.GetByConversationID(task.ConversationID); state != nil && state.RouteStatus == enums.ConversationRouteStatusStoreWecomManual {
		ConversationHumanDispatchService.notifyStoreRoomHandoffWithKey(task.ConversationID, reason, task.TaskKey+":"+noticeSuffix)
	}
	return nil
}

func updateCurrentManualResumeTask(db *gorm.DB, task *models.AIManualResumeTask, fromStatuses []string, updates map[string]any) (bool, error) {
	if db == nil || task == nil || task.ID <= 0 || len(fromStatuses) == 0 {
		return false, nil
	}
	query := db.Model(&models.AIManualResumeTask{}).
		Where("id = ? AND task_status IN ?", task.ID, fromStatuses)
	if task.LatestWaitingMessageID > 0 {
		query = query.Where("latest_waiting_message_id = ?", task.LatestWaitingMessageID)
	} else {
		query = query.Where("latest_waiting_message_id <= 0 AND origin_message_id = ?", task.OriginMessageID)
	}
	result := query.Updates(updates)
	return result.RowsAffected == 1, result.Error
}

func (s *aiManualResumeTaskService) handleTransitionError(task *models.AIManualResumeTask, transitionErr error, now time.Time, action string) {
	if transitionErr == nil {
		return
	}
	s.handleRunError(task, fmt.Errorf("%s: %w", strings.TrimSpace(action), transitionErr), now)
}

func (s *aiManualResumeTaskService) handleRunError(task *models.AIManualResumeTask, runErr error, now time.Time) {
	if err := s.failOrRetry(task, runErr, now); err != nil {
		slog.Error("manual resume task transition failed", "conversation_id", task.ConversationID, "task_id", task.ID, "error", err)
	}
}

func (s *aiManualResumeTaskService) failOrRetry(task *models.AIManualResumeTask, runErr error, now time.Time) error {
	if task == nil {
		return nil
	}
	retryCount := task.RetryCount + 1
	if retryCount <= 3 {
		delays := []time.Duration{15 * time.Second, time.Minute, 3 * time.Minute}
		_, err := updateCurrentManualResumeTask(sqls.DB(), task, []string{aiManualResumeTaskRunning}, map[string]any{
			"task_status":      aiManualResumeTaskRetry,
			"retry_count":      retryCount,
			"next_retry_at":    now.Add(delays[retryCount-1]),
			"last_error":       limitText(runErr.Error(), 1000),
			"updated_at":       now,
			"update_user_name": "system",
		})
		return err
	}
	updated := false
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		state, err := ConversationRouteService.lockByConversationIDWithDB(ctx.Tx, task.ConversationID)
		if err != nil {
			return err
		}
		taskUpdated, err := updateCurrentManualResumeTask(ctx.Tx, task, []string{aiManualResumeTaskRunning}, map[string]any{
			"task_status":      aiManualResumeTaskFailed,
			"retry_count":      retryCount,
			"completed_at":     now,
			"last_error":       limitText(runErr.Error(), 1000),
			"updated_at":       now,
			"update_user_name": "system",
		})
		if err != nil {
			return err
		}
		if !taskUpdated {
			return nil
		}
		if err := repositories.ConversationRouteStateRepository.Updates(ctx.Tx, state.ID, map[string]any{
			"need_human_follow_up": true,
			"handoff_reason":       "人工超时后AI恢复失败，仍需人工关注",
			"updated_at":           now,
			"update_user_name":     "system",
		}); err != nil {
			return err
		}
		if err := ConversationEventLogService.CreateEvent(ctx, task.ConversationID, enums.IMEventTypeTransfer, enums.IMSenderTypeSystem, 0,
			"人工超时后AI恢复失败，仍需人工关注", ConversationService.buildEventPayload(map[string]any{
				"action":       "manual_resume_failed",
				"taskKey":      task.TaskKey,
				"retryCount":   retryCount,
				"errorMessage": limitText(runErr.Error(), 500),
			})); err != nil {
			return err
		}
		updated = true
		return nil
	})
	if err != nil {
		return err
	}
	if !updated {
		return nil
	}
	if conversation := ConversationService.Get(task.ConversationID); conversation != nil {
		WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationUpdated)
	}
	if state := ConversationRouteService.GetByConversationID(task.ConversationID); state != nil && state.RouteStatus == enums.ConversationRouteStatusStoreWecomManual {
		ConversationHumanDispatchService.notifyStoreRoomHandoffWithKey(task.ConversationID, "AI 临时恢复失败，仍需人工接待。 "+state.HandoffReason, task.TaskKey+":resume_failed")
	}
	return nil
}

func (s *aiManualResumeTaskService) completeTask(task *models.AIManualResumeTask, now time.Time) error {
	if task == nil {
		return nil
	}
	_, err := updateCurrentManualResumeTask(sqls.DB(), task, []string{aiManualResumeTaskRunning}, map[string]any{
		"task_status":      aiManualResumeTaskSucceeded,
		"completed_at":     now,
		"next_retry_at":    nil,
		"last_error":       "",
		"updated_at":       now,
		"update_user_name": "system",
	})
	return err
}

func (s *aiManualResumeTaskService) cancelTask(task *models.AIManualResumeTask, reason string) error {
	if task == nil {
		return nil
	}
	now := time.Now()
	status := strings.TrimSpace(task.TaskStatus)
	if status == "" {
		return fmt.Errorf("manual resume task status is unavailable")
	}
	_, err := updateCurrentManualResumeTask(sqls.DB(), task, []string{status}, map[string]any{
		"task_status":      aiManualResumeTaskCancelled,
		"completed_at":     now,
		"last_error":       strings.TrimSpace(reason),
		"updated_at":       now,
		"update_user_name": "system",
	})
	return err
}

func routeStatusBlocksManualResume(status enums.ConversationRouteStatus) bool {
	switch status {
	case enums.ConversationRouteStatusStoreWecomManual,
		enums.ConversationRouteStatusHQAgentDeskPending,
		enums.ConversationRouteStatusHQAgentDeskServing:
		return true
	default:
		return false
	}
}
