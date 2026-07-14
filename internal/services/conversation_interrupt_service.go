package services

import (
	"encoding/base64"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var ConversationInterruptService = newConversationInterruptService()

func newConversationInterruptService() *conversationInterruptService {
	return &conversationInterruptService{}
}

type conversationInterruptService struct{}

func (s *conversationInterruptService) Get(id int64) *models.ConversationInterrupt {
	return repositories.ConversationInterruptRepository.Get(sqls.DB(), id)
}

func (s *conversationInterruptService) GetByCheckPointID(checkPointID string) *models.ConversationInterrupt {
	checkPointID = strings.TrimSpace(checkPointID)
	if checkPointID == "" {
		return nil
	}
	return repositories.ConversationInterruptRepository.GetByCheckPointID(sqls.DB(), checkPointID)
}

func (s *conversationInterruptService) FindLatestPendingByConversationID(conversationID int64) *models.ConversationInterrupt {
	if conversationID <= 0 {
		return nil
	}
	conversation, err := requireConversationParent(sqls.DB(), conversationID)
	if err != nil {
		return nil
	}
	return repositories.ConversationInterruptRepository.FindLatestPendingByConversationIDInTenant(sqls.DB(), conversationID, conversation.TenantID)
}

func (s *conversationInterruptService) SaveCheckpoint(checkPointID string, data []byte) error {
	checkPointID = strings.TrimSpace(checkPointID)
	if checkPointID == "" {
		return nil
	}
	now := time.Now()
	item := &models.ConversationInterrupt{
		CheckPointID:   checkPointID,
		CheckPointData: base64.StdEncoding.EncodeToString(data),
		Status:         "checkpointed",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	item = s.mergeForCheckpointUpdate(s.GetByCheckPointID(checkPointID), item)
	return repositories.ConversationInterruptRepository.UpsertByCheckPointID(sqls.DB(), item)
}

func (s *conversationInterruptService) LoadCheckpoint(checkPointID string) ([]byte, bool, error) {
	item := s.GetByCheckPointID(checkPointID)
	if item == nil || strings.TrimSpace(item.CheckPointData) == "" {
		return nil, false, nil
	}
	data, err := base64.StdEncoding.DecodeString(item.CheckPointData)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (s *conversationInterruptService) CreateOrUpdatePending(item *models.ConversationInterrupt) error {
	if item == nil {
		return nil
	}
	conversation, err := requireConversationParent(sqls.DB(), item.ConversationID)
	if err != nil {
		return err
	}
	item.TenantID = conversation.TenantID
	now := time.Now()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	item.Status = strings.TrimSpace(item.Status)
	if item.Status == "" {
		item.Status = "pending"
	}
	current := s.GetByCheckPointID(item.CheckPointID)
	if current != nil && current.TenantID > 0 && current.TenantID != item.TenantID {
		return errorsx.InvalidParam("中断检查点已属于其他接入公司")
	}
	item = s.mergeForPendingUpdate(current, item)
	return repositories.ConversationInterruptRepository.UpsertByCheckPointID(sqls.DB(), item)
}

func (s *conversationInterruptService) mergeForCheckpointUpdate(current, next *models.ConversationInterrupt) *models.ConversationInterrupt {
	if next == nil {
		return nil
	}
	if current == nil {
		return next
	}
	merged := *current
	merged.ConversationID = current.ConversationID
	merged.AIAgentID = current.AIAgentID
	merged.SourceMessageID = current.SourceMessageID
	merged.LastResumeMessageID = current.LastResumeMessageID
	merged.InterruptID = current.InterruptID
	merged.InterruptType = current.InterruptType
	merged.Status = current.Status
	merged.PromptText = current.PromptText
	merged.RequestData = current.RequestData
	merged.ResumeCount = current.ResumeCount
	merged.ExpiresAt = current.ExpiresAt
	merged.CheckPointData = next.CheckPointData
	merged.UpdatedAt = next.UpdatedAt
	return &merged
}

func (s *conversationInterruptService) mergeForPendingUpdate(current, next *models.ConversationInterrupt) *models.ConversationInterrupt {
	if next == nil {
		return nil
	}
	if current == nil {
		return next
	}
	merged := *current
	merged.TenantID = next.TenantID
	merged.ConversationID = next.ConversationID
	merged.AIAgentID = next.AIAgentID
	merged.SourceMessageID = next.SourceMessageID
	merged.InterruptID = next.InterruptID
	merged.InterruptType = next.InterruptType
	merged.Status = next.Status
	merged.PromptText = next.PromptText
	merged.RequestData = next.RequestData
	merged.UpdatedAt = next.UpdatedAt
	return &merged
}

func (s *conversationInterruptService) MarkResolved(id int64, lastResumeMessageID int64) error {
	current := s.Get(id)
	if current == nil {
		return nil
	}
	nextCount := current.ResumeCount + 1
	return repositories.ConversationInterruptRepository.UpdatesInTenant(sqls.DB(), id, current.TenantID, map[string]any{
		"status":                 "resolved",
		"last_resume_message_id": lastResumeMessageID,
		"resume_count":           nextCount,
		"updated_at":             time.Now(),
	})
}

func (s *conversationInterruptService) MarkCancelled(id int64, lastResumeMessageID int64) error {
	current := s.Get(id)
	if current == nil {
		return nil
	}
	nextCount := current.ResumeCount + 1
	return repositories.ConversationInterruptRepository.UpdatesInTenant(sqls.DB(), id, current.TenantID, map[string]any{
		"status":                 "cancelled",
		"last_resume_message_id": lastResumeMessageID,
		"resume_count":           nextCount,
		"updated_at":             time.Now(),
	})
}

func (s *conversationInterruptService) MarkExpired(id int64, lastResumeMessageID int64) error {
	current := s.Get(id)
	if current == nil {
		return nil
	}
	nextCount := current.ResumeCount + 1
	return repositories.ConversationInterruptRepository.UpdatesInTenant(sqls.DB(), id, current.TenantID, map[string]any{
		"status":                 "expired",
		"last_resume_message_id": lastResumeMessageID,
		"resume_count":           nextCount,
		"updated_at":             time.Now(),
	})
}

func (s *conversationInterruptService) MarkPendingAgain(id int64, interruptID, promptText string, lastResumeMessageID int64) error {
	current := s.Get(id)
	if current == nil {
		return nil
	}
	nextCount := current.ResumeCount + 1
	return repositories.ConversationInterruptRepository.UpdatesInTenant(sqls.DB(), id, current.TenantID, map[string]any{
		"status":                 "pending",
		"interrupt_id":           strings.TrimSpace(interruptID),
		"prompt_text":            strings.TrimSpace(promptText),
		"last_resume_message_id": lastResumeMessageID,
		"resume_count":           nextCount,
		"updated_at":             time.Now(),
	})
}
