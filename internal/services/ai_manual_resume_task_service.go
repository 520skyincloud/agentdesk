package services

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
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
	conversation := ConversationService.Get(conversationID)
	if conversation == nil || conversation.TenantID <= 0 {
		return nil, fmt.Errorf("conversation has no tenant")
	}
	unlock := lockConversationHandoff(conversationID)
	defer unlock()

	if originMessageID <= 0 {
		if message := s.latestCustomerMessage(conversationID, conversation.TenantID); message != nil {
			originMessageID = message.ID
		}
	}
	handoffToken = strings.TrimSpace(handoffToken)
	if handoffToken == "" {
		handoffToken = s.NewHandoffToken()
	}
	taskKey := fmt.Sprintf("manual_resume:%d:%s", conversationID, handoffToken)
	now := time.Now()
	var result *models.AIManualResumeTask
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedConversation, lockErr := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, conversationID, conversation.TenantID)
		if lockErr != nil {
			return lockErr
		}
		if lockedConversation == nil {
			return fmt.Errorf("conversation has no tenant")
		}
		state, lockErr := repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(ctx.Tx, conversationID, conversation.TenantID)
		if lockErr != nil {
			return lockErr
		}
		if state == nil || !routeStatusBlocksManualResume(state.RouteStatus) {
			return fmt.Errorf("conversation is not in a manual route")
		}
		if existing := repositories.AIManualResumeTaskRepository.Take(ctx.Tx,
			"tenant_id = ? AND conversation_id = ? AND task_status IN ?",
			conversation.TenantID,
			conversationID,
			[]string{aiManualResumeTaskWaiting, aiManualResumeTaskReady, aiManualResumeTaskRunning, aiManualResumeTaskRetry, aiManualResumeTaskBlockedAIDisabled},
		); existing != nil {
			result = existing
			return nil
		}
		if existing := repositories.AIManualResumeTaskRepository.Take(ctx.Tx, "task_key = ? AND tenant_id = ?", taskKey, conversation.TenantID); existing != nil {
			result = existing
			return nil
		}
		var nextReminderAt *time.Time
		if state.RouteStatus == enums.ConversationRouteStatusStoreWecomManual {
			delay := 2 * time.Minute
			if isSafetyHandoffReason(state.HandoffReason) {
				delay = time.Minute
			}
			value := now.Add(delay)
			nextReminderAt = &value
		}
		item := &models.AIManualResumeTask{
			TenantID:               conversation.TenantID,
			TaskKey:                taskKey,
			HandoffToken:           handoffToken,
			ConversationID:         conversationID,
			WxWorkInstanceID:       state.WxWorkInstanceID,
			OriginMessageID:        originMessageID,
			LatestWaitingMessageID: originMessageID,
			RouteStatus:            string(state.RouteStatus),
			TaskStatus:             aiManualResumeTaskWaiting,
			NextReminderAt:         nextReminderAt,
			AuditFields: models.AuditFields{
				CreatedAt:      now,
				CreateUserName: "system",
				UpdatedAt:      now,
				UpdateUserName: "system",
			},
		}
		if createErr := repositories.AIManualResumeTaskRepository.Create(ctx.Tx, item); createErr != nil {
			return createErr
		}
		result = item
		return nil
	})
	return result, err
}

func (s *aiManualResumeTaskService) EnsureForTimeout(conversationID int64) bool {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil || conversation.TenantID <= 0 {
		return false
	}
	if task := s.latestActiveTask(conversationID, conversation.TenantID, []string{aiManualResumeTaskWaiting, aiManualResumeTaskBlockedAIDisabled}); task != nil {
		return true
	}
	messages := MessageService.Find(sqls.NewCnd().
		Eq("tenant_id", conversation.TenantID).
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
		_, err := s.Schedule(conversationID, message.ID, "recovered_"+s.NewHandoffToken())
		return err == nil
	}
	return false
}

func (s *aiManualResumeTaskService) RecordWaitingCustomerMessage(conversationID int64, messageID int64) {
	if conversationID <= 0 || messageID <= 0 {
		return
	}
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return
	}
	task := s.latestActiveTask(conversationID, conversation.TenantID, []string{aiManualResumeTaskWaiting})
	if task == nil {
		return
	}
	_ = repositories.AIManualResumeTaskRepository.UpdatesInTenant(sqls.DB(), task.ID, task.TenantID, map[string]any{
		"latest_waiting_message_id": messageID,
		"updated_at":                time.Now(),
		"update_user_name":          "system",
	})
}

func (s *aiManualResumeTaskService) CancelActive(conversationID int64, reason string) {
	if conversationID <= 0 || sqls.DB() == nil || !sqls.DB().Migrator().HasTable(&models.AIManualResumeTask{}) {
		return
	}
	now := time.Now()
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return
	}
	_ = repositories.AIManualResumeTaskRepository.CancelActiveByConversationInTenant(sqls.DB(), conversationID, conversation.TenantID, map[string]any{
		"task_status":      aiManualResumeTaskCancelled,
		"completed_at":     now,
		"last_error":       strings.TrimSpace(reason),
		"updated_at":       now,
		"update_user_name": "system",
	})
}

func (s *aiManualResumeTaskService) MarkReady(conversationID int64, now time.Time) bool {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return false
	}
	task := s.latestActiveTask(conversationID, conversation.TenantID, []string{aiManualResumeTaskWaiting, aiManualResumeTaskBlockedAIDisabled})
	if task == nil {
		return false
	}
	if err := repositories.AIManualResumeTaskRepository.UpdatesInTenant(sqls.DB(), task.ID, task.TenantID, map[string]any{
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
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return
	}
	task := s.latestActiveTask(conversationID, conversation.TenantID, []string{aiManualResumeTaskWaiting, aiManualResumeTaskReady, aiManualResumeTaskRetry})
	if task == nil {
		return
	}
	_ = repositories.AIManualResumeTaskRepository.UpdatesInTenant(sqls.DB(), task.ID, task.TenantID, map[string]any{
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
	instance := WxWorkProtocolInstanceService.Get(wxWorkInstanceID)
	if instance == nil || instance.TenantID <= 0 {
		return nil
	}
	return repositories.AIManualResumeTaskRepository.UnblockByWxWorkInstanceInTenant(sqls.DB(), wxWorkInstanceID, instance.TenantID, map[string]any{
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
		Gt("tenant_id", 0).
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
	if task.TenantID <= 0 {
		return false
	}
	state := ConversationRouteService.GetByConversationIDInTenant(task.ConversationID, task.TenantID)
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
		_ = repositories.AIManualResumeTaskRepository.UpdatesInTenant(sqls.DB(), task.ID, task.TenantID, map[string]any{
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
	claimed, err := repositories.AIManualResumeTaskRepository.ClaimReminderInTenant(sqls.DB(), task.ID, task.TenantID, task.ReminderCount, now, map[string]any{
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
	if task.TenantID <= 0 {
		return false
	}
	claimed, err := repositories.AIManualResumeTaskRepository.ClaimInTenant(sqls.DB(), task.ID, task.TenantID,
		[]string{aiManualResumeTaskReady, aiManualResumeTaskRetry},
		map[string]any{"task_status": aiManualResumeTaskRunning, "updated_at": now, "update_user_name": "system"})
	if err != nil || !claimed {
		return false
	}
	current := repositories.AIManualResumeTaskRepository.GetInTenant(sqls.DB(), task.ID, task.TenantID)
	if current == nil {
		return false
	}
	requestID := "manual_resume_" + strings.ReplaceAll(current.HandoffToken, "-", "")
	state := ConversationRouteService.GetByConversationIDInTenant(current.ConversationID, current.TenantID)
	if state != nil && state.RouteStatus == enums.ConversationRouteStatusAIServing && s.hasCommittedResumeReply(current.ConversationID, current.TenantID, requestID) {
		s.completeTask(current, now)
		return true
	}
	if state == nil || !routeStatusBlocksManualResume(state.RouteStatus) {
		s.cancelTask(current, "conversation left the waiting manual route before resume")
		return true
	}
	if !s.aiReplyEnabled(current.WxWorkInstanceID, current.TenantID) {
		_ = repositories.AIManualResumeTaskRepository.UpdatesInTenant(sqls.DB(), current.ID, current.TenantID, map[string]any{
			"task_status": aiManualResumeTaskBlockedAIDisabled,
			"last_error":  "AI reply is disabled for the employee account",
			"updated_at":  now,
		})
		return true
	}
	conversation := ConversationService.GetByTenantID(current.ConversationID, current.TenantID)
	message := s.resolveResumeMessage(current)
	if conversation == nil || message == nil {
		s.failOrRetry(current, fmt.Errorf("manual resume conversation or message is unavailable"), now)
		return true
	}
	if existing := MessageService.FindOne(sqls.NewCnd().
		Eq("tenant_id", current.TenantID).
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
	result, triggerErr := TriggerAIReplySyncHook(ctx, *conversation, messageCopy)
	cancel()
	err = triggerErr
	if err == nil && result.Status != AIReplyExecutionStatusCompleted {
		err = fmt.Errorf("AI runtime did not complete manual resume: %s", result.ReasonCode)
	}
	if err == nil {
		if reply := MessageService.FindOne(sqls.NewCnd().
			Eq("tenant_id", current.TenantID).
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
	if updated := ConversationService.GetByTenantID(current.ConversationID, current.TenantID); updated != nil {
		WsService.PublishConversationChanged(updated, enums.IMRealtimeEventConversationUpdated)
	}
	return true
}

func (s *aiManualResumeTaskService) hasCommittedResumeReply(conversationID, tenantID int64, requestID string) bool {
	return MessageService.FindOne(sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("conversation_id", conversationID).
		Eq("sender_type", enums.IMSenderTypeAI).
		Eq("request_id", requestID).
		Desc("id")) != nil
}

func (s *aiManualResumeTaskService) finalizeResumeRoute(task *models.AIManualResumeTask, state *models.ConversationRouteState, now time.Time) error {
	if task == nil || state == nil {
		return fmt.Errorf("manual resume task or route state is unavailable")
	}
	if err := ManualSessionTimeoutService.restoreConversationShell(task.ConversationID, now, "manual_wait_resume_committed", "人工等待超时，AI已提交实际续答", state.RouteStatus); err != nil {
		return err
	}
	keepFollowUp := isSafetyHandoffReason(state.HandoffReason)
	if err := ConversationRouteService.RestoreAIWithFollowUpInTenant(task.ConversationID, task.TenantID, "人工等待超时，AI已恢复接待", now, keepFollowUp); err != nil {
		return err
	}
	return repositories.AIManualResumeTaskRepository.UpdatesInTenant(sqls.DB(), task.ID, task.TenantID, map[string]any{
		"notice_sent_at":   now,
		"updated_at":       now,
		"update_user_name": "system",
	})
}

func (s *aiManualResumeTaskService) latestActiveTask(conversationID, tenantID int64, statuses []string) *models.AIManualResumeTask {
	db := sqls.DB()
	if db == nil || !db.Migrator().HasTable(&models.AIManualResumeTask{}) {
		return nil
	}
	items := repositories.AIManualResumeTaskRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("conversation_id", conversationID).
		In("task_status", statuses).
		Desc("id").
		Limit(1))
	if len(items) == 0 {
		return nil
	}
	return &items[0]
}

func (s *aiManualResumeTaskService) latestCustomerMessage(conversationID, tenantID int64) *models.Message {
	return MessageService.FindOne(sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("conversation_id", conversationID).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		Where("recalled_at IS NULL AND send_status <> ?", enums.IMMessageStatusRecalled).
		Desc("seq_no").
		Desc("id"))
}

func (s *aiManualResumeTaskService) resolveResumeMessage(task *models.AIManualResumeTask) *models.Message {
	if task == nil {
		return nil
	}
	messageID := task.LatestWaitingMessageID
	if messageID <= 0 {
		messageID = task.OriginMessageID
	}
	message := repositories.MessageRepository.GetInTenant(sqls.DB(), messageID, task.TenantID)
	if message == nil || message.ConversationID != task.ConversationID || message.SenderType != enums.IMSenderTypeCustomer || message.RecalledAt != nil {
		return nil
	}
	startID := task.OriginMessageID
	if startID <= 0 || startID > message.ID {
		startID = message.ID
	}
	waitingMessages := MessageService.Find(sqls.NewCnd().
		Eq("tenant_id", task.TenantID).
		Eq("conversation_id", task.ConversationID).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		Gte("id", startID).
		Lte("id", message.ID).
		Where("recalled_at IS NULL AND send_status <> ?", enums.IMMessageStatusRecalled).
		Asc("seq_no").Asc("id"))
	parts := make([]string, 0, len(waitingMessages))
	for _, item := range waitingMessages {
		if isConsumedHandoffConfirmationMessage(item) {
			continue
		}
		text := strings.TrimSpace(item.Content)
		if text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) > 0 {
		copyMessage := *message
		copyMessage.Content = strings.Join(parts, "\n")
		return &copyMessage
	}
	return message
}

func (s *aiManualResumeTaskService) aiReplyEnabled(wxWorkInstanceID, tenantID int64) bool {
	if wxWorkInstanceID <= 0 {
		return true
	}
	instance := WxWorkProtocolInstanceService.GetByTenantID(wxWorkInstanceID, tenantID)
	return instance == nil || instance.AIReplyEnabled
}

func (s *aiManualResumeTaskService) failOrRetry(task *models.AIManualResumeTask, runErr error, now time.Time) {
	if task == nil {
		return
	}
	// 技术失败（intent/generation/knowledge/commit 等）是模型通道问题，不是客户诉求。
	// 不应该因此把会话升级成"仍需人工"，否则模型故障期间会"恢复→失败→再人工"死循环。
	// 这里改为退避重试，交给主链熔断（P2-2）与更大的重试上限兜底。
	if _, isTechnical := AIReplyExecutionErrorCodeOf(runErr); isTechnical {
		s.retryTechnical(task, runErr, now)
		return
	}
	retryCount := task.RetryCount + 1
	if retryCount <= 3 {
		delays := []time.Duration{15 * time.Second, time.Minute, 3 * time.Minute}
		_ = repositories.AIManualResumeTaskRepository.UpdatesInTenant(sqls.DB(), task.ID, task.TenantID, map[string]any{
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
		if err := repositories.AIManualResumeTaskRepository.UpdatesInTenant(ctx.Tx, task.ID, task.TenantID, map[string]any{
			"task_status":      aiManualResumeTaskFailed,
			"retry_count":      retryCount,
			"completed_at":     now,
			"last_error":       limitText(runErr.Error(), 1000),
			"updated_at":       now,
			"update_user_name": "system",
		}); err != nil {
			return err
		}
		state := repositories.ConversationRouteStateRepository.Take(ctx.Tx, "conversation_id = ? AND tenant_id = ?", task.ConversationID, task.TenantID)
		if state != nil {
			if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, state.ID, task.TenantID, map[string]any{
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
	if conversation := ConversationService.GetByTenantID(task.ConversationID, task.TenantID); conversation != nil {
		WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationUpdated)
	}
	if state := ConversationRouteService.GetByConversationIDInTenant(task.ConversationID, task.TenantID); state != nil && state.RouteStatus == enums.ConversationRouteStatusStoreWecomManual {
		ConversationHumanDispatchService.notifyStoreRoomHandoffWithKey(task.ConversationID, "AI 临时恢复失败，仍需人工接待。 "+state.HandoffReason, task.TaskKey+":resume_failed")
	}
}

// retryTechnical 处理技术失败的退避重试：不升级为人工，交给熔断与更大上限兜底。
func (s *aiManualResumeTaskService) retryTechnical(task *models.AIManualResumeTask, runErr error, now time.Time) {
	const maxTechnicalRetry = 6
	retryCount := task.RetryCount + 1
	if retryCount > maxTechnicalRetry {
		// 重试彻底耗尽，仍走原人工兜底，避免无限循环。
		s.failOrRetryNonTechnical(task, runErr, now)
		return
	}
	delays := []time.Duration{15 * time.Second, time.Minute, 3 * time.Minute}
	delay := delays[0]
	if retryCount-1 < len(delays) {
		delay = delays[retryCount-1]
	} else {
		delay = delays[len(delays)-1]
	}
	_ = repositories.AIManualResumeTaskRepository.UpdatesInTenant(sqls.DB(), task.ID, task.TenantID, map[string]any{
		"task_status":      aiManualResumeTaskRetry,
		"retry_count":      retryCount,
		"next_retry_at":    now.Add(delay),
		"last_error":       limitText(runErr.Error(), 1000),
		"updated_at":       now,
		"update_user_name": "system",
	})
}

// failOrRetryNonTechnical 是技术失败重试耗尽后的最终兜底（与历史语义一致）。
func (s *aiManualResumeTaskService) failOrRetryNonTechnical(task *models.AIManualResumeTask, runErr error, now time.Time) {
	retryCount := task.RetryCount + 1
	_ = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.AIManualResumeTaskRepository.UpdatesInTenant(ctx.Tx, task.ID, task.TenantID, map[string]any{
			"task_status":      aiManualResumeTaskFailed,
			"retry_count":      retryCount,
			"completed_at":     now,
			"last_error":       limitText(runErr.Error(), 1000),
			"updated_at":       now,
			"update_user_name": "system",
		}); err != nil {
			return err
		}
		state := repositories.ConversationRouteStateRepository.Take(ctx.Tx, "conversation_id = ? AND tenant_id = ?", task.ConversationID, task.TenantID)
		if state != nil {
			if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, state.ID, task.TenantID, map[string]any{
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
	if conversation := ConversationService.GetByTenantID(task.ConversationID, task.TenantID); conversation != nil {
		WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationUpdated)
	}
	if state := ConversationRouteService.GetByConversationIDInTenant(task.ConversationID, task.TenantID); state != nil && state.RouteStatus == enums.ConversationRouteStatusStoreWecomManual {
		ConversationHumanDispatchService.notifyStoreRoomHandoffWithKey(task.ConversationID, "AI 临时恢复失败，仍需人工接待。 "+state.HandoffReason, task.TaskKey+":resume_failed")
	}
}

func (s *aiManualResumeTaskService) completeTask(task *models.AIManualResumeTask, now time.Time) {
	_ = repositories.AIManualResumeTaskRepository.UpdatesInTenant(sqls.DB(), task.ID, task.TenantID, map[string]any{
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
	_ = repositories.AIManualResumeTaskRepository.UpdatesInTenant(sqls.DB(), task.ID, task.TenantID, map[string]any{
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
