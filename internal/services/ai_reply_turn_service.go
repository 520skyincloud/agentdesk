package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

const aiReplyTurnSentAtTolerance = time.Second

var AIReplyTurnService = newAIReplyTurnService()

type aiReplyTurnService struct{}

type AIReplyTurnCoverage struct {
	ReasonCode         string
	CoveredByMessageID int64
}

type AIReplyTurnCoveredError struct {
	ReasonCode         string
	CoveredByMessageID int64
}

func (e *AIReplyTurnCoveredError) Error() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.ReasonCode)
}

func newAIReplyTurnService() *aiReplyTurnService {
	return &aiReplyTurnService{}
}

func (s *aiReplyTurnService) EnabledFor(conversation *models.Conversation) bool {
	if conversation == nil || conversation.ID <= 0 || conversation.StoreStaffBindingID <= 0 {
		return false
	}
	if sqls.DB() == nil || !sqls.DB().Migrator().HasTable(&models.AIReplyTurn{}) {
		return false
	}
	enabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("AI_REPLY_TURN_COORDINATOR_ENABLED")))
	if err != nil || !enabled {
		return false
	}
	allowlist := strings.TrimSpace(os.Getenv("AI_REPLY_TURN_COORDINATOR_BINDING_IDS"))
	if allowlist == "" {
		return true
	}
	needle := strconv.FormatInt(conversation.StoreStaffBindingID, 10)
	for _, item := range strings.Split(allowlist, ",") {
		if strings.TrimSpace(item) == needle {
			return true
		}
	}
	return false
}

func (s *aiReplyTurnService) AssignCustomerMessageDB(db *gorm.DB, conversation *models.Conversation, message *models.Message) (*models.AIReplyTurn, bool, error) {
	if db == nil || conversation == nil || message == nil {
		return nil, false, errorsx.InvalidParam("AI 回复轮次缺少消息上下文")
	}
	if !s.EnabledFor(conversation) {
		return nil, false, nil
	}
	if message.ID <= 0 || message.TenantID != conversation.TenantID || message.ConversationID != conversation.ID ||
		message.SessionNo <= 0 || message.SenderType != enums.IMSenderTypeCustomer || message.HistoricalOnly {
		return nil, false, errorsx.InvalidParam("AI 回复轮次消息范围不一致")
	}

	lockedConversation, err := repositories.ConversationRepository.GetForUpdateInTenant(db, conversation.ID, conversation.TenantID)
	if err != nil {
		return nil, false, err
	}
	if lockedConversation == nil || lockedConversation.StoreID != conversation.StoreID ||
		lockedConversation.StoreStaffBindingID != conversation.StoreStaffBindingID {
		return nil, false, errorsx.InvalidParam("AI 回复轮次会话范围已变化")
	}

	now := time.Now()
	sentAt := now
	if message.SentAt != nil && !message.SentAt.IsZero() {
		sentAt = *message.SentAt
	}
	current, err := repositories.AIReplyTurnRepository.GetForUpdateInTenant(db, lockedConversation.CurrentAIReplyTurnID, conversation.TenantID)
	if err != nil {
		return nil, false, err
	}
	attach := current != nil && current.ConversationID == conversation.ID && current.SessionNo == message.SessionNo &&
		current.StoreID == conversation.StoreID && current.StoreStaffBindingID == conversation.StoreStaffBindingID &&
		current.Status != enums.AIReplyTurnStatusInterrupted && current.Status != enums.AIReplyTurnStatusClosed &&
		current.Status != enums.AIReplyTurnStatusFailed
	if attach && current.LastDeliveredAt != nil && sentAt.After(current.LastDeliveredAt.Add(aiReplyTurnSentAtTolerance)) {
		attach = false
	}

	created := false
	activeJobID := int64(0)
	if !attach {
		if current != nil && current.Status != enums.AIReplyTurnStatusInterrupted && current.Status != enums.AIReplyTurnStatusClosed {
			if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, current.ID, current.TenantID, map[string]any{
				"status":           enums.AIReplyTurnStatusClosed,
				"terminal_reason":  "new_customer_turn",
				"completed_at":     now,
				"updated_at":       now,
				"update_user_name": "ai_reply_turn",
			}); err != nil {
				return nil, false, err
			}
			if err := AIReplyTurnTaskService.SupersedeTurnDB(db, current.TenantID, current.ID, "turn_closed", now); err != nil {
				return nil, false, err
			}
		}
		current = &models.AIReplyTurn{
			TenantID: conversation.TenantID, ConversationID: conversation.ID, SessionNo: message.SessionNo,
			StoreID: conversation.StoreID, StoreStaffBindingID: conversation.StoreStaffBindingID,
			Version: 1, Status: enums.AIReplyTurnStatusOpen,
			FirstCustomerMessageID: message.ID, LastCustomerMessageID: message.ID,
			FirstCustomerSentAt: sentAt, LastCustomerSentAt: sentAt,
			AuditFields: models.AuditFields{
				CreatedAt: now, CreateUserName: "ai_reply_turn",
				UpdatedAt: now, UpdateUserName: "ai_reply_turn",
			},
		}
		if err := repositories.AIReplyTurnRepository.Create(db, current); err != nil {
			return nil, false, err
		}
		created = true
		if err := repositories.ConversationRepository.UpdatesInTenant(db, conversation.ID, conversation.TenantID, map[string]any{
			"current_ai_reply_turn_id": current.ID,
			"updated_at":               now,
		}); err != nil {
			return nil, false, err
		}
	} else {
		activeJobID = current.ActiveJobID
		current.Version++
		current.LastCustomerMessageID = message.ID
		if sentAt.After(current.LastCustomerSentAt) {
			current.LastCustomerSentAt = sentAt
		}
		current.Status = enums.AIReplyTurnStatusOpen
		current.CompletedAt = nil
		if activeJobID > 0 {
			if err := AIReplyTurnTaskService.ReleaseJobClaimsDB(db, current.TenantID, current.ID, activeJobID, now); err != nil {
				return nil, false, err
			}
		}
		if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, current.ID, current.TenantID, map[string]any{
			"version":                  current.Version,
			"status":                   current.Status,
			"terminal_reason":          "",
			"last_customer_message_id": current.LastCustomerMessageID,
			"last_customer_sent_at":    current.LastCustomerSentAt,
			"completed_at":             nil,
			"active_job_id":            0,
			"lease_owner":              "",
			"lease_expires_at":         nil,
			"updated_at":               now,
			"update_user_name":         "ai_reply_turn",
		}); err != nil {
			return nil, false, err
		}
	}

	message.AIReplyTurnID = current.ID
	message.AIReplyTurnVersion = current.Version
	if err := repositories.MessageRepository.UpdatesInTenant(db, message.ID, message.TenantID, map[string]any{
		"ai_reply_turn_id":      current.ID,
		"ai_reply_turn_version": current.Version,
		"updated_at":            now,
		"update_user_name":      "ai_reply_turn",
	}); err != nil {
		return nil, false, err
	}
	if err := repositories.AIReplyJobRepository.SupersedeOlderTurnVersions(db, conversation.TenantID, current.ID, current.Version, 0, now); err != nil {
		return nil, false, err
	}
	return current, created, nil
}

func (s *aiReplyTurnService) TryClaimJobDB(db *gorm.DB, job *models.AIReplyJob, owner string, now, leaseExpiresAt time.Time) (bool, error) {
	if db == nil || job == nil || job.TurnID <= 0 || job.TenantID <= 0 || strings.TrimSpace(owner) == "" {
		return false, nil
	}
	turn, err := repositories.AIReplyTurnRepository.GetForUpdateInTenant(db, job.TurnID, job.TenantID)
	if err != nil || turn == nil {
		return false, err
	}
	if turn.ConversationID != job.ConversationID || turn.SessionNo != job.SessionNo || turn.StoreID != job.StoreID ||
		turn.StoreStaffBindingID != job.StoreStaffBindingID || turn.Version != job.TurnVersion || aiReplyTurnTerminalStatus(turn.Status) {
		return false, nil
	}
	if turn.ActiveJobID > 0 && turn.ActiveJobID != job.ID && turn.LeaseExpiresAt != nil && turn.LeaseExpiresAt.After(now) {
		return false, nil
	}
	if turn.ActiveJobID > 0 && turn.ActiveJobID != job.ID {
		if err := AIReplyTurnTaskService.ReleaseJobClaimsDB(db, turn.TenantID, turn.ID, turn.ActiveJobID, now); err != nil {
			return false, err
		}
	}
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"status":           enums.AIReplyTurnStatusRunning,
		"active_job_id":    job.ID,
		"lease_owner":      owner,
		"lease_expires_at": leaseExpiresAt,
		"updated_at":       now,
		"update_user_name": "ai_reply_turn_lease",
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (s *aiReplyTurnService) RenewJobLeaseDB(db *gorm.DB, job *models.AIReplyJob, owner string, now, leaseExpiresAt time.Time) (bool, error) {
	if db == nil || job == nil || job.TurnID <= 0 || job.TenantID <= 0 || strings.TrimSpace(owner) == "" {
		return true, nil
	}
	result := db.Model(&models.AIReplyTurn{}).
		Where("id = ? AND tenant_id = ? AND active_job_id = ? AND lease_owner = ? AND lease_expires_at > ?",
			job.TurnID, job.TenantID, job.ID, owner, now).
		Updates(map[string]any{
			"lease_expires_at": leaseExpiresAt,
			"updated_at":       now,
			"update_user_name": "ai_reply_turn_lease",
		})
	return result.RowsAffected == 1, result.Error
}

func (s *aiReplyTurnService) ReleaseJobLeaseDB(db *gorm.DB, job *models.AIReplyJob, owner string, releaseTasks bool, now time.Time) error {
	if db == nil || job == nil || job.TurnID <= 0 || job.TenantID <= 0 || strings.TrimSpace(owner) == "" {
		return nil
	}
	turn, err := repositories.AIReplyTurnRepository.GetForUpdateInTenant(db, job.TurnID, job.TenantID)
	if err != nil || turn == nil {
		return err
	}
	if turn.ActiveJobID != job.ID || turn.LeaseOwner != owner {
		return nil
	}
	if releaseTasks {
		if err := AIReplyTurnTaskService.ReleaseJobClaimsDB(db, turn.TenantID, turn.ID, job.ID, now); err != nil {
			return err
		}
	}
	status := turn.Status
	if status == enums.AIReplyTurnStatusRunning {
		if AIReplyTurnTaskService.HasUnfinishedDB(db, turn.TenantID, turn.ID) {
			status = enums.AIReplyTurnStatusOpen
		} else if turn.LastDeliveredVersion > 0 {
			status = enums.AIReplyTurnStatusDelivered
		} else if turn.LastCommittedVersion > 0 {
			status = enums.AIReplyTurnStatusCommitted
		} else {
			status = enums.AIReplyTurnStatusOpen
		}
	}
	return repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"status":           status,
		"active_job_id":    0,
		"lease_owner":      "",
		"lease_expires_at": nil,
		"updated_at":       now,
		"update_user_name": "ai_reply_turn_lease",
	})
}

func (s *aiReplyTurnService) InterruptCurrentDB(db *gorm.DB, conversation *models.Conversation, sessionNo int, reason string) error {
	if db == nil || conversation == nil || !s.EnabledFor(conversation) {
		return nil
	}
	lockedConversation, err := repositories.ConversationRepository.GetForUpdateInTenant(db, conversation.ID, conversation.TenantID)
	if err != nil || lockedConversation == nil || lockedConversation.CurrentAIReplyTurnID <= 0 {
		return err
	}
	turn, err := repositories.AIReplyTurnRepository.GetForUpdateInTenant(db, lockedConversation.CurrentAIReplyTurnID, conversation.TenantID)
	if err != nil || turn == nil {
		return err
	}
	if sessionNo > 0 && turn.SessionNo != sessionNo {
		return nil
	}
	now := time.Now()
	aiFailureHandoff := aiReplyTurnAIHandoffReason(reason)
	requestedHandoff := aiReplyTurnRequestedHandoffReason(reason)
	turn.Version++
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"version":          turn.Version,
		"status":           enums.AIReplyTurnStatusInterrupted,
		"terminal_reason":  limitText(strings.TrimSpace(reason), 80),
		"completed_at":     now,
		"updated_at":       now,
		"update_user_name": limitText(strings.TrimSpace(reason), 100),
	}); err != nil {
		return err
	}
	excludeJobID := int64(0)
	if aiFailureHandoff {
		excludeJobID = turn.ActiveJobID
	}
	if err := repositories.AIReplyJobRepository.SupersedeOlderTurnVersions(db, turn.TenantID, turn.ID, turn.Version, excludeJobID, now); err != nil {
		return err
	}
	if aiFailureHandoff {
		if err := AIReplyTurnTaskService.FinalizeAIHandoffDB(db, turn.TenantID, turn.ID, "human_handoff", now); err != nil {
			return err
		}
	} else if requestedHandoff {
		if err := AIReplyTurnTaskService.FinalizeRequestedHandoffDB(db, turn.TenantID, turn.ID, "human_handoff", now); err != nil {
			return err
		}
	} else {
		if err := AIReplyTurnTaskService.SupersedeTurnDB(db, turn.TenantID, turn.ID, reason, now); err != nil {
			return err
		}
	}
	return repositories.ConversationRepository.UpdatesInTenant(db, conversation.ID, conversation.TenantID, map[string]any{
		"current_ai_reply_turn_id": 0,
		"updated_at":               now,
	})
}

func (s *aiReplyTurnService) InvalidateCustomerRecallDB(db *gorm.DB, conversation *models.Conversation, message *models.Message) error {
	if db == nil || conversation == nil || message == nil || message.SenderType != enums.IMSenderTypeCustomer ||
		message.AIReplyTurnID <= 0 || !s.EnabledFor(conversation) {
		return nil
	}
	lockedConversation, err := repositories.ConversationRepository.GetForUpdateInTenant(db, conversation.ID, conversation.TenantID)
	if err != nil || lockedConversation == nil {
		return err
	}
	turn, err := repositories.AIReplyTurnRepository.GetForUpdateInTenant(db, message.AIReplyTurnID, conversation.TenantID)
	if err != nil || turn == nil {
		return err
	}
	if turn.ConversationID != conversation.ID || turn.SessionNo != message.SessionNo ||
		turn.StoreID != conversation.StoreID || turn.StoreStaffBindingID != conversation.StoreStaffBindingID {
		return errorsx.InvalidParam("撤回消息与 AI 回复轮次范围不一致")
	}
	if turn.Status == enums.AIReplyTurnStatusInterrupted || turn.Status == enums.AIReplyTurnStatusClosed || turn.Status == enums.AIReplyTurnStatusFailed {
		return nil
	}
	now := time.Now()
	turn.Version++
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"version":          turn.Version,
		"status":           enums.AIReplyTurnStatusInterrupted,
		"terminal_reason":  "customer_message_recalled",
		"completed_at":     now,
		"updated_at":       now,
		"update_user_name": "customer_message_recalled",
	}); err != nil {
		return err
	}
	if err := repositories.AIReplyJobRepository.SupersedeOlderTurnVersions(db, turn.TenantID, turn.ID, turn.Version, 0, now); err != nil {
		return err
	}
	if err := AIReplyTurnTaskService.SupersedeTurnDB(db, turn.TenantID, turn.ID, "customer_message_recalled", now); err != nil {
		return err
	}
	if lockedConversation.CurrentAIReplyTurnID != turn.ID {
		return nil
	}
	return repositories.ConversationRepository.UpdatesInTenant(db, conversation.ID, conversation.TenantID, map[string]any{
		"current_ai_reply_turn_id": 0,
		"updated_at":               now,
	})
}

func (s *aiReplyTurnService) GetForJob(job *models.AIReplyJob, message *models.Message) (*models.AIReplyTurn, string) {
	if job == nil || message == nil || job.TurnID <= 0 || job.TurnVersion <= 0 {
		return nil, "turn_missing"
	}
	turn := repositories.AIReplyTurnRepository.GetInTenant(sqls.DB(), job.TurnID, job.TenantID)
	if turn == nil || message.AIReplyTurnID != turn.ID || message.AIReplyTurnVersion != job.TurnVersion ||
		turn.ConversationID != job.ConversationID || turn.SessionNo != job.SessionNo ||
		turn.StoreID != job.StoreID || turn.StoreStaffBindingID != job.StoreStaffBindingID {
		return nil, "turn_scope_invalid"
	}
	return turn, ""
}

func (s *aiReplyTurnService) FindCoverage(job *models.AIReplyJob, message *models.Message, turn *models.AIReplyTurn) *AIReplyTurnCoverage {
	if job == nil || message == nil || turn == nil || turn.LastCommittedVersion <= 0 || job.TurnVersion <= turn.LastCommittedVersion {
		return nil
	}
	if turn.LastDeliveredAt != nil && message.SentAt != nil && message.SentAt.After(turn.LastDeliveredAt.Add(aiReplyTurnSentAtTolerance)) {
		return nil
	}
	fingerprint := aiReplyQuestionFingerprint(*message)
	if fingerprint == "" {
		return nil
	}
	priorQuestions := repositories.MessageRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", job.TenantID).
		Eq("conversation_id", job.ConversationID).
		Eq("session_no", job.SessionNo).
		Eq("ai_reply_turn_id", turn.ID).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		Lte("ai_reply_turn_version", turn.LastCommittedVersion).
		Where("recalled_at IS NULL AND send_status NOT IN (?, ?)", enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Asc("id"))
	matchedVersion := 0
	for _, prior := range priorQuestions {
		if prior.ID != message.ID && aiReplyQuestionFingerprint(prior) == fingerprint {
			if prior.AIReplyTurnVersion > matchedVersion {
				matchedVersion = prior.AIReplyTurnVersion
			}
		}
	}
	if matchedVersion <= 0 {
		return nil
	}

	priorReplies := repositories.MessageRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", job.TenantID).
		Eq("conversation_id", job.ConversationID).
		Eq("session_no", job.SessionNo).
		Eq("ai_reply_turn_id", turn.ID).
		Eq("sender_type", enums.IMSenderTypeAI).
		Gte("ai_reply_turn_version", matchedVersion).
		Lte("ai_reply_turn_version", turn.LastCommittedVersion).
		Where("recalled_at IS NULL AND send_status NOT IN (?, ?)", enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Asc("ai_reply_turn_version").
		Asc("id"))
	for _, reply := range priorReplies {
		outbox := repositories.ChannelMessageOutboxRepository.Take(sqls.DB(), "tenant_id = ? AND message_id = ?", job.TenantID, reply.ID)
		if outbox == nil {
			return &AIReplyTurnCoverage{ReasonCode: "covered_by_inflight_reply", CoveredByMessageID: reply.ID}
		}
		switch enums.ChannelMessageOutboxStatus(outbox.SendStatus) {
		case enums.ChannelMessageOutboxStatusSent:
			return &AIReplyTurnCoverage{ReasonCode: "covered_by_inflight_reply", CoveredByMessageID: reply.ID}
		case enums.ChannelMessageOutboxStatusPending, enums.ChannelMessageOutboxStatusSending:
			return &AIReplyTurnCoverage{ReasonCode: "pending_delivery_reused", CoveredByMessageID: reply.ID}
		case enums.ChannelMessageOutboxStatusFailed:
			now := time.Now()
			_ = repositories.ChannelMessageOutboxRepository.UpdatesInTenant(sqls.DB(), outbox.ID, outbox.TenantID, map[string]any{
				"next_retry_at": now,
				"updated_at":    now,
			})
			return &AIReplyTurnCoverage{ReasonCode: "pending_delivery_reused", CoveredByMessageID: reply.ID}
		}
	}
	return nil
}

func (s *aiReplyTurnService) InputFloorVersion(message models.Message) int {
	if message.AIReplyTurnID <= 0 || message.TenantID <= 0 {
		return 0
	}
	turn := repositories.AIReplyTurnRepository.GetInTenant(sqls.DB(), message.AIReplyTurnID, message.TenantID)
	if turn == nil || turn.ConversationID != message.ConversationID || turn.SessionNo != message.SessionNo {
		return 0
	}
	return turn.LastDeliveredVersion
}

func (s *aiReplyTurnService) ValidateCommitDB(db *gorm.DB, tenantID, conversationID int64, sessionNo int, turnID int64, version int, jobID int64, taskKeys []string) (*models.AIReplyTurn, error) {
	if turnID <= 0 || version <= 0 {
		return nil, nil
	}
	conversation, err := repositories.ConversationRepository.GetForUpdateInTenant(db, conversationID, tenantID)
	if err != nil {
		return nil, err
	}
	if conversation == nil {
		return nil, errorsx.InvalidParam("AI 回复轮次提交会话不存在")
	}
	turn, err := repositories.AIReplyTurnRepository.GetForUpdateInTenant(db, turnID, tenantID)
	if err != nil {
		return nil, err
	}
	if turn == nil || turn.ConversationID != conversationID || turn.SessionNo != sessionNo ||
		turn.StoreID != conversation.StoreID || turn.StoreStaffBindingID != conversation.StoreStaffBindingID {
		return nil, errorsx.InvalidParam("AI 回复轮次提交范围不一致")
	}
	if version != turn.Version ||
		turn.Status == enums.AIReplyTurnStatusInterrupted ||
		turn.Status == enums.AIReplyTurnStatusClosed || turn.Status == enums.AIReplyTurnStatusFailed ||
		conversation.CurrentAIReplyTurnID != turn.ID || !aiReplyTurnConversationAllowsAI(conversation) {
		return nil, ErrAIReplyTurnStale
	}
	if jobID > 0 {
		if turn.ActiveJobID != jobID || strings.TrimSpace(turn.LeaseOwner) == "" ||
			turn.LeaseExpiresAt == nil || !turn.LeaseExpiresAt.After(time.Now()) {
			return nil, ErrAIReplyTurnStale
		}
		for _, taskKey := range uniqueTaskKeys(taskKeys) {
			task, taskErr := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, tenantID, turn.ID, taskKey)
			if taskErr != nil {
				return nil, taskErr
			}
			if task == nil || task.ConversationID != conversationID || task.SessionNo != sessionNo ||
				task.Status != enums.AIReplyTurnTaskStatusRunning || task.ClaimedByJobID != jobID ||
				task.IntroducedVersion > turn.Version {
				return nil, ErrAIReplyTurnStale
			}
		}
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, conversation.TenantID)
	if route == nil || route.TenantID != conversation.TenantID || route.ConversationID != conversation.ID ||
		route.StoreID != turn.StoreID || route.StoreStaffBindingID != turn.StoreStaffBindingID {
		return nil, errorsx.InvalidParam("AI 回复轮次提交路由范围不一致")
	}
	if route.SessionNo != turn.SessionNo || !aiReplyTurnRouteAllowsAI(route.RouteStatus) {
		return nil, ErrAIReplyTurnStale
	}
	if err := WxWorkProtocolService.RequireConversationOutboundRoute(db, conversation); err != nil {
		return nil, err
	}
	if route.WxWorkInstanceID > 0 {
		instance := repositories.WxWorkProtocolInstanceRepository.GetActivatedCurrentInTenant(db, route.WxWorkInstanceID, conversation.TenantID)
		if instance == nil || !instance.AIReplyEnabled || instance.StoreID != turn.StoreID ||
			instance.StoreStaffBindingID != turn.StoreStaffBindingID || instance.ChannelID != conversation.ChannelID {
			return nil, ErrAIReplyTurnStale
		}
	}
	return turn, nil
}

func (s *aiReplyTurnService) ValidatePreparedReplyDB(db *gorm.DB, turn *models.AIReplyTurn, requestID string, drafts []AIOutboundMessageDraft) error {
	if db == nil || turn == nil || turn.LastCommittedVersion <= 0 || turn.Version <= turn.LastCommittedVersion || len(drafts) == 0 {
		return nil
	}
	requestID = strings.TrimSpace(requestID)
	trigger := repositories.MessageRepository.Take(db,
		"tenant_id = ? AND conversation_id = ? AND session_no = ? AND ai_reply_turn_id = ? AND ai_reply_turn_version = ? AND sender_type = ? AND request_id = ? AND recalled_at IS NULL AND send_status NOT IN (?, ?)",
		turn.TenantID, turn.ConversationID, turn.SessionNo, turn.ID, turn.Version, enums.IMSenderTypeCustomer, requestID,
		enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled,
	)
	if trigger == nil || aiReplyQuestionFingerprint(*trigger) == "" {
		return nil
	}
	previousReplies := repositories.MessageRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", turn.TenantID).
		Eq("conversation_id", turn.ConversationID).
		Eq("session_no", turn.SessionNo).
		Eq("ai_reply_turn_id", turn.ID).
		Eq("ai_reply_turn_version", turn.LastCommittedVersion).
		Eq("sender_type", enums.IMSenderTypeAI).
		Where("recalled_at IS NULL AND send_status NOT IN (?, ?)", enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Asc("id"))
	if strings.TrimSpace(turn.LastCommittedRequestID) != "" {
		filtered := previousReplies[:0]
		for _, reply := range previousReplies {
			if strings.TrimSpace(reply.RequestID) == strings.TrimSpace(turn.LastCommittedRequestID) {
				filtered = append(filtered, reply)
			}
		}
		previousReplies = filtered
	}
	if !aiReplyPreparedBatchMatches(previousReplies, drafts) {
		return nil
	}

	currentQuestion := aiReplyQuestionFingerprint(*trigger)
	previousQuestions := repositories.MessageRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", turn.TenantID).
		Eq("conversation_id", turn.ConversationID).
		Eq("session_no", turn.SessionNo).
		Eq("ai_reply_turn_id", turn.ID).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		Lte("ai_reply_turn_version", turn.LastCommittedVersion).
		Where("recalled_at IS NULL AND send_status NOT IN (?, ?)", enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Asc("id"))
	for _, previous := range previousQuestions {
		if previous.ID != trigger.ID && aiReplyQuestionFingerprint(previous) == currentQuestion {
			return &AIReplyTurnCoveredError{
				ReasonCode:         "covered_by_inflight_reply",
				CoveredByMessageID: previousReplies[len(previousReplies)-1].ID,
			}
		}
	}
	return ErrAIReplyTurnDuplicateAnswer
}

func (s *aiReplyTurnService) MarkCommittedDB(db *gorm.DB, turn *models.AIReplyTurn, version int, requestID string, delivered bool, now time.Time) error {
	if turn == nil {
		return nil
	}
	status := enums.AIReplyTurnStatusCommitted
	if turn.ActiveJobID > 0 && AIReplyTurnTaskService.HasUnfinishedDB(db, turn.TenantID, turn.ID) {
		status = enums.AIReplyTurnStatusRunning
	}
	updates := map[string]any{
		"status":                    status,
		"terminal_reason":           "",
		"last_committed_version":    version,
		"last_committed_request_id": strings.TrimSpace(requestID),
		"updated_at":                now,
		"update_user_name":          "ai_reply_commit",
	}
	if delivered {
		if status != enums.AIReplyTurnStatusRunning {
			updates["status"] = enums.AIReplyTurnStatusDelivered
		}
		updates["last_delivered_version"] = version
		updates["last_delivered_request_id"] = strings.TrimSpace(requestID)
		updates["last_delivered_at"] = now
	}
	return repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, updates)
}

func (s *aiReplyTurnService) MarkDelivered(message *models.Message, deliveredAt time.Time) error {
	if message == nil || message.AIReplyTurnID <= 0 || message.AIReplyTurnVersion <= 0 || message.TenantID <= 0 {
		return nil
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return s.MarkDeliveredDB(ctx.Tx, message, deliveredAt)
	})
}

func (s *aiReplyTurnService) MarkDeliveredDB(db *gorm.DB, message *models.Message, deliveredAt time.Time) error {
	if db == nil || message == nil || message.AIReplyTurnID <= 0 || message.AIReplyTurnVersion <= 0 || message.TenantID <= 0 {
		return nil
	}
	conversation, err := repositories.ConversationRepository.GetForUpdateInTenant(db, message.ConversationID, message.TenantID)
	if err != nil || conversation == nil {
		return err
	}
	turn, err := repositories.AIReplyTurnRepository.GetForUpdateInTenant(db, message.AIReplyTurnID, message.TenantID)
	if err != nil || turn == nil {
		return err
	}
	if turn.ConversationID != message.ConversationID || turn.SessionNo != message.SessionNo ||
		turn.StoreID != conversation.StoreID || turn.StoreStaffBindingID != conversation.StoreStaffBindingID ||
		message.AIReplyTurnVersion < turn.LastDeliveredVersion {
		return nil
	}
	if message.AIReplyTurnVersion == turn.LastDeliveredVersion && turn.LastDeliveredAt != nil && !deliveredAt.After(*turn.LastDeliveredAt) {
		return nil
	}
	updates := map[string]any{
		"last_delivered_version":    message.AIReplyTurnVersion,
		"last_delivered_request_id": strings.TrimSpace(message.RequestID),
		"last_delivered_at":         deliveredAt,
		"updated_at":                deliveredAt,
		"update_user_name":          "outbox_delivery",
	}
	if !aiReplyTurnTerminalStatus(turn.Status) && message.AIReplyTurnVersion == turn.Version &&
		message.AIReplyTurnVersion >= turn.LastCommittedVersion {
		updates["status"] = enums.AIReplyTurnStatusDelivered
	}
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, updates); err != nil {
		return err
	}
	return AIReplyTurnTaskService.MarkDeliveredByMessageDB(db, message, deliveredAt)
}

func (s *aiReplyTurnService) CanDispatchOutbox(message *models.Message) (bool, string, error) {
	return s.CanDispatchOutboxDB(sqls.DB(), message)
}

func (s *aiReplyTurnService) CanDispatchOutboxDB(db *gorm.DB, message *models.Message) (bool, string, error) {
	if message == nil || message.AIReplyTurnID <= 0 || message.AIReplyTurnVersion <= 0 || message.SenderType != enums.IMSenderTypeAI {
		return true, "", nil
	}
	turn := repositories.AIReplyTurnRepository.GetInTenant(db, message.AIReplyTurnID, message.TenantID)
	if turn == nil || turn.ConversationID != message.ConversationID || turn.SessionNo != message.SessionNo {
		return false, "cancelled_turn_scope_invalid", nil
	}
	if message.AIReplyTurnVersion != turn.Version {
		return false, "cancelled_stale_turn", nil
	}
	conversation := repositories.ConversationRepository.GetInTenant(db, message.ConversationID, message.TenantID)
	if conversation == nil || conversation.StoreID != turn.StoreID || conversation.StoreStaffBindingID != turn.StoreStaffBindingID {
		return false, "cancelled_turn_scope_invalid", nil
	}
	taskStateKnown := false
	taskDispatchable := false
	if db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		tasks := repositories.AIReplyTurnTaskRepository.FindByCommittedMessageInTenant(db, message.TenantID, message.ID)
		if len(tasks) > 0 {
			taskStateKnown = true
			for _, task := range tasks {
				if task.TurnID == turn.ID && (task.Status == enums.AIReplyTurnTaskStatusCommitted || task.Status == enums.AIReplyTurnTaskStatusDelivered) {
					taskDispatchable = true
					break
				}
			}
		}
	}
	if aiReplyTurnTerminalStatus(turn.Status) {
		if aiReplyTurnAIHandoffReason(turn.TerminalReason) && taskStateKnown && taskDispatchable {
			return true, "", nil
		}
		return false, "cancelled_turn_inactive", nil
	}
	if conversation.CurrentAIReplyTurnID != turn.ID || !aiReplyTurnConversationAllowsAI(conversation) {
		return false, "cancelled_turn_inactive", nil
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, conversation.TenantID)
	if route == nil || route.TenantID != turn.TenantID || route.ConversationID != turn.ConversationID ||
		route.StoreID != turn.StoreID || route.StoreStaffBindingID != turn.StoreStaffBindingID {
		return false, "cancelled_turn_scope_invalid", nil
	}
	if route.SessionNo != turn.SessionNo || !aiReplyTurnRouteAllowsAI(route.RouteStatus) {
		return false, "cancelled_turn_inactive", nil
	}
	if err := WxWorkProtocolService.RequireConversationOutboundRoute(db, conversation); err != nil {
		return false, "cancelled_turn_scope_invalid", nil
	}
	if route.WxWorkInstanceID > 0 {
		instance := repositories.WxWorkProtocolInstanceRepository.GetActivatedCurrentInTenant(db, route.WxWorkInstanceID, conversation.TenantID)
		if instance == nil || !instance.AIReplyEnabled || instance.StoreID != turn.StoreID ||
			instance.StoreStaffBindingID != turn.StoreStaffBindingID || instance.ChannelID != conversation.ChannelID {
			return false, "cancelled_turn_inactive", nil
		}
	}
	if taskStateKnown {
		if taskDispatchable {
			return true, "", nil
		}
		return false, "cancelled_stale_task", nil
	}
	return true, "", nil
}

func aiReplyTurnConversationAllowsAI(conversation *models.Conversation) bool {
	if conversation == nil || conversation.Status != enums.IMConversationStatusAIServing || conversation.CurrentAssigneeID != 0 {
		return false
	}
	return conversation.ServiceMode == enums.IMConversationServiceModeAIOnly || conversation.ServiceMode == enums.IMConversationServiceModeAIFirst
}

func aiReplyTurnRouteAllowsAI(status enums.ConversationRouteStatus) bool {
	return status == enums.ConversationRouteStatusAIServing || status == enums.ConversationRouteStatusAIFallback
}

func aiReplyTurnTerminalStatus(status enums.AIReplyTurnStatus) bool {
	return status == enums.AIReplyTurnStatusInterrupted || status == enums.AIReplyTurnStatusClosed || status == enums.AIReplyTurnStatusFailed
}

func aiReplyTurnAIHandoffReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "ai_failure_store_wecom_handoff", "ai_failure_hq_agentdesk_handoff":
		return true
	default:
		return false
	}
}

func aiReplyTurnRequestedHandoffReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "store_wecom_handoff", "hq_agentdesk_handoff":
		return true
	default:
		return false
	}
}

var (
	ErrAIReplyTurnStale           = fmt.Errorf("AI reply turn version is stale")
	ErrAIReplyTurnDuplicateAnswer = errors.New("AI reply duplicated the previous answer for a different question")
)

func aiReplyQuestionFingerprint(message models.Message) string {
	text := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
	if text == "" {
		return ""
	}
	text = norm.NFKC.String(strings.ToLower(text))
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimRightFunc(text, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	})
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func aiReplyPreparedBatchMatches(previous []models.Message, drafts []AIOutboundMessageDraft) bool {
	if len(previous) == 0 || len(previous) != len(drafts) {
		return false
	}
	for index := range previous {
		if aiReplyPreparedItemFingerprint(previous[index].MessageType, previous[index].Content, previous[index].Payload) !=
			aiReplyPreparedItemFingerprint(drafts[index].MessageType, drafts[index].Content, drafts[index].Payload) {
			return false
		}
	}
	return true
}

func aiReplyPreparedItemFingerprint(messageType enums.IMMessageType, content, payload string) string {
	content = norm.NFKC.String(strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(content)), " ")))
	payload = canonicalAIReplyPayload(payload)
	if content == "" && payload == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(string(messageType) + "\x00" + content + "\x00" + payload))
	return hex.EncodeToString(sum[:])
}

func canonicalAIReplyPayload(payload string) string {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return ""
	}
	var value any
	if json.Unmarshal([]byte(payload), &value) != nil {
		return norm.NFKC.String(payload)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return norm.NFKC.String(payload)
	}
	return string(canonical)
}
