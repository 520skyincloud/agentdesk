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

	aiManualResumeNotice = "同事暂时没能接入，接下来我先继续帮你处理。"
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
	requestID := "manual_resume_" + strings.ReplaceAll(current.HandoffToken, "-", "")
	state := ConversationRouteService.GetByConversationID(current.ConversationID)
	if state != nil && state.RouteStatus == enums.ConversationRouteStatusAIServing && s.hasCommittedResumeReply(current.ConversationID, requestID) {
		s.completeTask(current, now)
		return true
	}
	if state == nil || !routeStatusBlocksManualResume(state.RouteStatus) {
		s.cancelTask(current, "conversation left the waiting manual route before resume")
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
		s.cancelTask(current, "manual resume was invalidated before rebuilding the deferred snapshot")
		return true
	}
	conversation := ConversationService.Get(current.ConversationID)
	message, resumeSnapshot, hasResumeSnapshot := s.resolveResumeMessageWithSnapshot(current)
	if conversation == nil || message == nil {
		s.failOrRetry(current, fmt.Errorf("manual resume conversation or message is unavailable"), now)
		return true
	}
	if existing := MessageService.FindOne(sqls.NewCnd().
		Eq("conversation_id", current.ConversationID).
		Eq("sender_type", enums.IMSenderTypeAI).
		Eq("request_id", requestID).
		Desc("id")); existing != nil {
		if err := s.finalizeResumeRoute(current, state, now); err != nil {
			s.failOrRetry(current, err, now)
			return true
		}
		s.completeTask(current, now)
		return true
	}
	if TriggerAIReplySyncHook == nil {
		s.failOrRetry(current, fmt.Errorf("synchronous AI reply hook is unavailable"), now)
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
			s.cancelTask(current, "conversation left the waiting manual route during resume")
		} else {
			s.failOrRetry(current, fmt.Errorf("manual resume state changed while AI was running"), time.Now())
		}
		return true
	}
	if err == nil {
		if reply := MessageService.FindOne(sqls.NewCnd().
			Eq("conversation_id", current.ConversationID).
			Eq("sender_type", enums.IMSenderTypeAI).
			Eq("request_id", requestID).
			Desc("id")); reply == nil {
			err = fmt.Errorf("AI runtime completed without committing a reply")
		}
	}
	if err != nil {
		s.failOrRetry(current, err, time.Now())
		return true
	}
	finishedAt := time.Now()
	if err := s.finalizeResumeRoute(current, state, finishedAt); err != nil {
		s.failOrRetry(current, err, finishedAt)
		return true
	}
	s.completeTask(current, time.Now())
	if updated := ConversationService.Get(current.ConversationID); updated != nil {
		WsService.PublishConversationChanged(updated, enums.IMRealtimeEventConversationUpdated)
	}
	return true
}

func (s *aiManualResumeTaskService) hasCommittedResumeReply(conversationID int64, requestID string) bool {
	return MessageService.FindOne(sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("sender_type", enums.IMSenderTypeAI).
		Eq("request_id", requestID).
		Desc("id")) != nil
}

func (s *aiManualResumeTaskService) finalizeResumeRoute(task *models.AIManualResumeTask, state *models.ConversationRouteState, now time.Time) error {
	if task == nil || state == nil {
		return fmt.Errorf("manual resume task or route state is unavailable")
	}
	requestID := manualResumeRequestID(task)
	message := s.resolveResumeMessage(task)
	if message == nil {
		return fmt.Errorf("manual resume is no longer eligible to restore AI")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedState, err := ConversationRouteService.lockByConversationIDWithDB(ctx.Tx, task.ConversationID)
		if err != nil {
			return err
		}
		if !s.canCommitRequestWithDB(ctx.Tx, lockedState, task.ConversationID, requestID, message.ID) {
			return fmt.Errorf("manual resume is no longer eligible to restore AI")
		}
		if err := ManualSessionTimeoutService.restoreConversationShellWithDB(ctx, task.ConversationID, now, "manual_wait_resume_committed", "人工等待超时，AI已提交实际续答", lockedState.RouteStatus); err != nil {
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
		return repositories.AIManualResumeTaskRepository.Updates(ctx.Tx, task.ID, map[string]any{
			"task_status":      aiManualResumeTaskSucceeded,
			"notice_sent_at":   now,
			"completed_at":     now,
			"next_retry_at":    nil,
			"last_error":       "",
			"updated_at":       now,
			"update_user_name": "system",
		})
	})
}

func manualResumeRequestID(task *models.AIManualResumeTask) string {
	if task == nil {
		return ""
	}
	return "manual_resume_" + strings.ReplaceAll(task.HandoffToken, "-", "")
}

func (s *aiManualResumeTaskService) CanCommitRequest(conversationID int64, requestID string, sourceMessageID int64) bool {
	return s.canCommitRequestWithDB(sqls.DB(), nil, conversationID, requestID, sourceMessageID)
}

func (s *aiManualResumeTaskService) canCommitRequestWithDB(db *gorm.DB, state *models.ConversationRouteState, conversationID int64, requestID string, sourceMessageID int64) bool {
	requestID = strings.TrimSpace(requestID)
	if db == nil || conversationID <= 0 || !strings.HasPrefix(requestID, "manual_resume_") {
		return false
	}
	task := s.latestActiveTaskWithDB(db, conversationID, []string{aiManualResumeTaskRunning})
	if task == nil || manualResumeRequestID(task) != requestID {
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
	message, _, _ := s.resolveResumeMessageWithSnapshot(task)
	return message
}

func (s *aiManualResumeTaskService) resolveResumeMessageWithSnapshot(task *models.AIManualResumeTask) (*models.Message, replyruntime.ManualResumeSnapshot, bool) {
	if task == nil {
		return nil, replyruntime.ManualResumeSnapshot{}, false
	}
	messageID := task.LatestWaitingMessageID
	if messageID <= 0 {
		messageID = task.OriginMessageID
	}
	message := MessageService.Get(messageID)
	if message == nil || message.ConversationID != task.ConversationID || message.SenderType != enums.IMSenderTypeCustomer || message.RecalledAt != nil {
		return nil, replyruntime.ManualResumeSnapshot{}, false
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
	snapshot, hasSnapshot := s.manualResumeExecutionSnapshot(task, waitingMessages)
	sources := manualResumeSourcesFromSnapshot(snapshot)
	parts := make([]string, 0, len(sources))
	for index, source := range sources {
		if text := strings.TrimSpace(source.Text); text != "" {
			parts = append(parts, manualResumeSourcePart(source, index+1))
		}
	}
	if len(parts) > 0 {
		copyMessage := *message
		copyMessage.Content = utils.BuildRuntimeCustomerBurstEnvelope(parts)
		return &copyMessage, snapshot, hasSnapshot
	}
	return message, snapshot, hasSnapshot
}

type manualResumeSource struct {
	Ref         string
	MessageID   int64
	MessageType enums.IMMessageType
	Text        string
}

type manualResumeDeferredTask = replyruntime.ManualResumeTaskPlan

type manualResumeTraceProjection struct {
	Runtime struct {
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
			} `json:"evidenceJudge"`
		} `json:"pipeline"`
		Output struct {
			FinishReason string `json:"finishReason"`
		} `json:"output"`
		Error struct {
			Stage string `json:"stage"`
		} `json:"error"`
	} `json:"runtime"`
}

func (s *aiManualResumeTaskService) manualResumeSources(task *models.AIManualResumeTask, waitingMessages []models.Message) []manualResumeSource {
	snapshot, _ := s.manualResumeExecutionSnapshot(task, waitingMessages)
	return manualResumeSourcesFromSnapshot(snapshot)
}

func (s *aiManualResumeTaskService) manualResumeExecutionSnapshot(task *models.AIManualResumeTask, waitingMessages []models.Message) (replyruntime.ManualResumeSnapshot, bool) {
	if task == nil {
		return replyruntime.ManualResumeSnapshot{}, false
	}
	origin := MessageService.Get(task.OriginMessageID)
	runLogID, deferredTasks, tracedSources, hasDeferredSnapshot := s.manualResumeDeferredSnapshot(task)
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
	return snapshot, hasDeferredSnapshot && len(deferredTasks) > 0
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
	ret := make([]manualResumeSource, 0, len(snapshot.Sources))
	for _, source := range snapshot.Sources {
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
	if task == nil || task.OriginMessageID <= 0 {
		return 0, nil, nil, false
	}
	runLogs := AgentRunLogService.Find(sqls.NewCnd().
		Eq("conversation_id", task.ConversationID).
		Eq("message_id", task.OriginMessageID).
		Desc("id").
		Limit(50))
	for _, runLog := range runLogs {
		if strings.TrimSpace(runLog.TraceData) == "" {
			continue
		}
		var projection manualResumeTraceProjection
		if err := json.Unmarshal([]byte(runLog.TraceData), &projection); err != nil {
			continue
		}
		if !manualResumeTraceIsAuthoritative(projection) {
			continue
		}
		deferredIDs := projection.Runtime.Pipeline.EvidenceJudge.DeferredTaskIDs
		if len(deferredIDs) == 0 {
			return 0, nil, nil, false
		}
		deferredSet := make(map[string]struct{}, len(deferredIDs))
		for _, taskID := range deferredIDs {
			if taskID = strings.TrimSpace(taskID); taskID != "" {
				if _, exists := deferredSet[taskID]; exists {
					return 0, nil, nil, false
				}
				deferredSet[taskID] = struct{}{}
			}
		}
		if len(deferredSet) == 0 {
			return 0, nil, nil, false
		}
		ret := make([]manualResumeDeferredTask, 0, len(deferredSet))
		matched := make(map[string]struct{}, len(deferredSet))
		for _, taskPlan := range projection.Runtime.Pipeline.ReplyPlan.TaskPlans {
			taskID := strings.TrimSpace(taskPlan.TaskID)
			if _, ok := deferredSet[taskID]; ok {
				ret = append(ret, taskPlan)
				matched[taskID] = struct{}{}
			}
		}
		if len(matched) != len(deferredSet) || len(ret) != len(deferredSet) {
			return 0, nil, nil, false
		}
		return runLog.ID, ret, append([]replyruntime.ManualResumeSource(nil), projection.Runtime.Input.CurrentTurnSources...), true
	}
	return 0, nil, nil, false
}

func manualResumeTraceIsAuthoritative(projection manualResumeTraceProjection) bool {
	if len(projection.Runtime.Pipeline.EvidenceJudge.DeferredTaskIDs) > 0 &&
		len(projection.Runtime.Pipeline.ReplyPlan.TaskPlans) > 0 {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(projection.Runtime.Status)) {
	case "completed", "interrupted", "fallback":
		return true
	case "error", "started":
		return false
	default:
		return strings.TrimSpace(projection.Runtime.Output.FinishReason) != "" &&
			strings.TrimSpace(projection.Runtime.Error.Stage) == ""
	}
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

func (s *aiManualResumeTaskService) failOrRetry(task *models.AIManualResumeTask, runErr error, now time.Time) {
	if task == nil {
		return
	}
	retryCount := task.RetryCount + 1
	if retryCount <= 3 {
		delays := []time.Duration{15 * time.Second, time.Minute, 3 * time.Minute}
		_ = repositories.AIManualResumeTaskRepository.Updates(sqls.DB(), task.ID, map[string]any{
			"task_status":      aiManualResumeTaskRetry,
			"retry_count":      retryCount,
			"next_retry_at":    now.Add(delays[retryCount-1]),
			"last_error":       limitText(runErr.Error(), 1000),
			"updated_at":       now,
			"update_user_name": "system",
		})
		return
	}
	_ = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.AIManualResumeTaskRepository.Updates(ctx.Tx, task.ID, map[string]any{
			"task_status":      aiManualResumeTaskFailed,
			"retry_count":      retryCount,
			"completed_at":     now,
			"last_error":       limitText(runErr.Error(), 1000),
			"updated_at":       now,
			"update_user_name": "system",
		}); err != nil {
			return err
		}
		state := repositories.ConversationRouteStateRepository.Take(ctx.Tx, "conversation_id = ?", task.ConversationID)
		if state != nil {
			if err := repositories.ConversationRouteStateRepository.Updates(ctx.Tx, state.ID, map[string]any{
				"need_human_follow_up": true,
				"handoff_reason":       "人工超时后AI恢复失败，仍需人工关注",
				"updated_at":           now,
				"update_user_name":     "system",
			}); err != nil {
				return err
			}
		}
		return ConversationEventLogService.CreateEvent(ctx, task.ConversationID, enums.IMEventTypeTransfer, enums.IMSenderTypeSystem, 0,
			"人工超时后AI恢复失败，仍需人工关注", ConversationService.buildEventPayload(map[string]any{
				"action":       "manual_resume_failed",
				"taskKey":      task.TaskKey,
				"retryCount":   retryCount,
				"errorMessage": limitText(runErr.Error(), 500),
			}))
	})
	if conversation := ConversationService.Get(task.ConversationID); conversation != nil {
		WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationUpdated)
	}
	if state := ConversationRouteService.GetByConversationID(task.ConversationID); state != nil && state.RouteStatus == enums.ConversationRouteStatusStoreWecomManual {
		ConversationHumanDispatchService.notifyStoreRoomHandoffWithKey(task.ConversationID, "AI 临时恢复失败，仍需人工接待。 "+state.HandoffReason, task.TaskKey+":resume_failed")
	}
}

func (s *aiManualResumeTaskService) completeTask(task *models.AIManualResumeTask, now time.Time) {
	_ = repositories.AIManualResumeTaskRepository.Updates(sqls.DB(), task.ID, map[string]any{
		"task_status":      aiManualResumeTaskSucceeded,
		"completed_at":     now,
		"next_retry_at":    nil,
		"last_error":       "",
		"updated_at":       now,
		"update_user_name": "system",
	})
}

func (s *aiManualResumeTaskService) cancelTask(task *models.AIManualResumeTask, reason string) {
	if task == nil {
		return
	}
	now := time.Now()
	_ = repositories.AIManualResumeTaskRepository.Updates(sqls.DB(), task.ID, map[string]any{
		"task_status":      aiManualResumeTaskCancelled,
		"completed_at":     now,
		"last_error":       strings.TrimSpace(reason),
		"updated_at":       now,
		"update_user_name": "system",
	})
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
