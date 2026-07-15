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
	state := ConversationRouteService.GetByConversationIDInTenant(conversationID, conversation.TenantID)
	if state == nil || !routeStatusBlocksManualResume(state.RouteStatus) {
		return nil, fmt.Errorf("conversation is not in a manual route")
	}
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
	if existing := repositories.AIManualResumeTaskRepository.Take(db, "task_key = ? AND tenant_id = ?", taskKey, conversation.TenantID); existing != nil {
		return existing, nil
	}
	now := time.Now()
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
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserName: "system",
			UpdatedAt:      now,
			UpdateUserName: "system",
		},
	}
	if err := repositories.AIManualResumeTaskRepository.Create(db, item); err != nil {
		return nil, err
	}
	return item, nil
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
		_, err := s.Schedule(conversationID, message.ID, "legacy_"+s.NewHandoffToken())
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
	items := repositories.AIManualResumeTaskRepository.Find(db, sqls.NewCnd().
		In("task_status", []string{aiManualResumeTaskReady, aiManualResumeTaskRetry}).
		Where("next_retry_at IS NOT NULL AND next_retry_at <= ?", now).
		Asc("next_retry_at").
		Limit(limit))
	handled := 0
	for i := range items {
		if s.processOne(items[i], now) {
			handled++
		}
	}
	return handled
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
	state := ConversationRouteService.GetByConversationIDInTenant(current.ConversationID, current.TenantID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusAIServing {
		s.cancelTask(current, "conversation left AI serving before resume")
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
	requestID := "manual_resume_" + strings.ReplaceAll(current.HandoffToken, "-", "")
	if existing := MessageService.FindOne(sqls.NewCnd().
		Eq("tenant_id", current.TenantID).
		Eq("conversation_id", current.ConversationID).
		Eq("sender_type", enums.IMSenderTypeAI).
		Eq("request_id", requestID).
		Desc("id")); existing != nil {
		s.completeTask(current, now)
		return true
	}
	if current.NoticeSentAt == nil {
		if _, err := MessageService.SendAIServiceNoticeWithPayloadAndRequestID(current.ConversationID, conversation.AIAgentID, aiManualResumeNotice, `{"serviceEvent":"manual_ai_resumed_wait_timeout"}`, requestID+"_notice"); err != nil {
			s.failOrRetry(current, err, now)
			return true
		}
		noticeAt := time.Now()
		_ = repositories.AIManualResumeTaskRepository.UpdatesInTenant(sqls.DB(), current.ID, current.TenantID, map[string]any{
			"notice_sent_at":   noticeAt,
			"updated_at":       noticeAt,
			"update_user_name": "system",
		})
	}
	if TriggerAIReplySyncHook == nil {
		s.failOrRetry(current, fmt.Errorf("synchronous AI reply hook is unavailable"), now)
		return true
	}
	messageCopy := *message
	messageCopy.RequestID = requestID
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	err = TriggerAIReplySyncHook(ctx, *conversation, messageCopy)
	cancel()
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
	s.completeTask(current, time.Now())
	return true
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
