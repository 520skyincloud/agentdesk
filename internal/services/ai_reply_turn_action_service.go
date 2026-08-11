package services

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var AIReplyTurnActionService = &aiReplyTurnActionService{}

type aiReplyTurnActionService struct{}

type AIReplyTurnActionInput struct {
	TaskKey      string
	ActionType   string
	ResourceType string
}

type AIReplyTurnActionCommitEvidence struct {
	ActionKey        string
	PreparedRevision string
	MessageID        int64
	OutboxID         int64
	Delivered        bool
	At               time.Time
}

func (s *aiReplyTurnActionService) StableActionKey(turnID int64, taskKey, actionType, resourceType string) string {
	payload := strconv.FormatInt(turnID, 10) + "\n" + strings.TrimSpace(taskKey) + "\n" + strings.TrimSpace(actionType) + "\n" + strings.TrimSpace(resourceType)
	sum := sha256.Sum256([]byte(payload))
	return "action_" + hex.EncodeToString(sum[:16])
}

func (s *aiReplyTurnActionService) EnsureRequestedDB(db *gorm.DB, turn *models.AIReplyTurn, inputs []AIReplyTurnActionInput) ([]models.AIReplyTurnAction, error) {
	if db == nil || turn == nil || turn.ID <= 0 || turn.TenantID <= 0 || turn.Version <= 0 {
		return nil, fmt.Errorf("AI reply action scope is invalid")
	}
	if err := repositories.AIReplyTurnActionRepository.SupersedeOlderVersions(db, turn.TenantID, turn.ID, turn.Version, time.Now()); err != nil {
		return nil, err
	}
	ret := make([]models.AIReplyTurnAction, 0, len(inputs))
	seen := make(map[string]struct{})
	for _, input := range inputs {
		input.TaskKey = strings.TrimSpace(input.TaskKey)
		input.ActionType = strings.TrimSpace(input.ActionType)
		input.ResourceType = strings.TrimSpace(input.ResourceType)
		if input.TaskKey == "" || !validAIReplyActionType(input.ActionType) {
			return nil, fmt.Errorf("AI reply action type or task is invalid")
		}
		actionKey := s.StableActionKey(turn.ID, input.TaskKey, input.ActionType, input.ResourceType)
		if _, duplicate := seen[actionKey]; duplicate {
			continue
		}
		seen[actionKey] = struct{}{}
		task := repositories.AIReplyTurnTaskRepository.GetByKeyInTenant(db, turn.TenantID, turn.ID, input.TaskKey)
		if task == nil || task.ConversationID != turn.ConversationID || task.SessionNo != turn.SessionNo {
			return nil, fmt.Errorf("AI reply action task scope is invalid")
		}
		now := time.Now()
		item := &models.AIReplyTurnAction{
			TenantID: turn.TenantID, TurnID: turn.ID, TaskKey: input.TaskKey, ActionKey: actionKey,
			ActionType: input.ActionType, ResourceType: input.ResourceType, Status: "requested", RequestedVersion: turn.Version,
			CreatedAt: now, UpdatedAt: now, CreateUserName: "ai_reply_action", UpdateUserName: "ai_reply_action",
		}
		created, err := repositories.AIReplyTurnActionRepository.CreateIfAbsent(db, item)
		if err != nil {
			return nil, err
		}
		if !created {
			item = repositories.AIReplyTurnActionRepository.GetByKeyInTenant(db, turn.TenantID, turn.ID, input.TaskKey, actionKey)
			if item == nil || item.ActionType != input.ActionType || item.ResourceType != input.ResourceType {
				return nil, fmt.Errorf("AI reply action key collision")
			}
			if item.RequestedVersion < turn.Version && item.Status == "superseded" {
				updated, err := repositories.AIReplyTurnActionRepository.CASStatusInTenant(db, item.ID, turn.TenantID, []string{"superseded"}, map[string]any{
					"status": "requested", "requested_version": turn.Version,
					"prepared_revision": "", "committed_message_id": int64(0), "outbox_id": int64(0),
					"result_code": "", "delivered_at": nil,
					"updated_at": now, "update_user_name": "ai_reply_action",
				})
				if err != nil {
					return nil, err
				}
				if !updated {
					return nil, fmt.Errorf("AI reply action re-request raced with another state change")
				}
				item = repositories.AIReplyTurnActionRepository.GetByKeyInTenant(db, turn.TenantID, turn.ID, input.TaskKey, actionKey)
			}
		}
		ret = append(ret, *item)
	}
	return ret, nil
}

func (s *aiReplyTurnActionService) FailDB(db *gorm.DB, tenantID, turnID int64, turnVersion int, actionKey, resultCode string, now time.Time) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || turnVersion <= 0 || strings.TrimSpace(actionKey) == "" || strings.TrimSpace(resultCode) == "" {
		return fmt.Errorf("AI reply action failure input is invalid")
	}
	items := repositories.AIReplyTurnActionRepository.FindByTurnInTenant(db, tenantID, turnID)
	for _, item := range items {
		if item.ActionKey != strings.TrimSpace(actionKey) {
			continue
		}
		if item.RequestedVersion != turnVersion {
			return fmt.Errorf("AI reply action belongs to stale turn version")
		}
		if item.Status == "failed" && item.ResultCode == strings.TrimSpace(resultCode) {
			return nil
		}
		updated, err := repositories.AIReplyTurnActionRepository.CASStatusInTenant(db, item.ID, tenantID, []string{"requested", "prepared"}, map[string]any{
			"status": "failed", "result_code": strings.TrimSpace(resultCode),
			"updated_at": now, "update_user_name": "ai_reply_action",
		})
		if err != nil {
			return err
		}
		if !updated {
			return fmt.Errorf("AI reply action failure raced with another state change")
		}
		return nil
	}
	return fmt.Errorf("AI reply action does not exist")
}

func (s *aiReplyTurnActionService) PrepareDB(db *gorm.DB, tenantID, turnID int64, turnVersion int, actionKey, preparedRevision string) (*models.AIReplyTurnAction, error) {
	if db == nil || tenantID <= 0 || turnID <= 0 || turnVersion <= 0 || strings.TrimSpace(actionKey) == "" || strings.TrimSpace(preparedRevision) == "" {
		return nil, fmt.Errorf("AI reply action prepare input is invalid")
	}
	items := repositories.AIReplyTurnActionRepository.FindByTurnInTenant(db, tenantID, turnID)
	for i := range items {
		item := &items[i]
		if item.ActionKey != actionKey {
			continue
		}
		if item.RequestedVersion != turnVersion {
			return nil, fmt.Errorf("AI reply action belongs to stale turn version")
		}
		if item.Status == "prepared" && item.PreparedRevision == preparedRevision {
			return item, nil
		}
		updated, err := repositories.AIReplyTurnActionRepository.CASStatusInTenant(db, item.ID, tenantID, []string{"requested"}, map[string]any{
			"status": "prepared", "prepared_revision": preparedRevision, "result_code": "", "updated_at": time.Now(), "update_user_name": "ai_reply_action",
		})
		if err != nil {
			return nil, err
		}
		if !updated {
			return nil, fmt.Errorf("AI reply action state changed concurrently")
		}
		item.Status = "prepared"
		item.PreparedRevision = preparedRevision
		return item, nil
	}
	return nil, fmt.Errorf("AI reply action does not exist")
}

func (s *aiReplyTurnActionService) CommitEvidenceDB(db *gorm.DB, tenantID, turnID int64, turnVersion int, evidence []AIReplyTurnActionCommitEvidence) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || turnVersion <= 0 {
		return fmt.Errorf("AI reply action commit scope is invalid")
	}
	items := repositories.AIReplyTurnActionRepository.FindPreparedByTurnInTenant(db, tenantID, turnID, turnVersion)
	byKey := make(map[string]*models.AIReplyTurnAction, len(items))
	for i := range items {
		byKey[items[i].ActionKey] = &items[i]
	}
	for _, item := range evidence {
		action := byKey[strings.TrimSpace(item.ActionKey)]
		if action == nil || item.MessageID <= 0 || strings.TrimSpace(item.PreparedRevision) == "" || action.PreparedRevision != strings.TrimSpace(item.PreparedRevision) {
			return fmt.Errorf("AI reply action commit evidence is incomplete")
		}
		at := item.At
		if at.IsZero() {
			at = time.Now()
		}
		status := "committed"
		updates := map[string]any{
			"status": status, "committed_message_id": item.MessageID, "outbox_id": item.OutboxID,
			"result_code": "", "updated_at": time.Now(), "update_user_name": "ai_reply_action",
		}
		if item.Delivered {
			updates["status"] = "delivered"
			updates["delivered_at"] = at
		}
		updated, err := repositories.AIReplyTurnActionRepository.CASStatusInTenant(db, action.ID, tenantID, []string{"prepared"}, updates)
		if err != nil {
			return err
		}
		if !updated {
			return fmt.Errorf("AI reply action commit raced with another state change")
		}
	}
	return nil
}

func (s *aiReplyTurnActionService) SuppressDB(db *gorm.DB, tenantID, turnID int64, turnVersion int, items []AIReplyTurnActionSuppression, now time.Time) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || turnVersion <= 0 || len(items) == 0 {
		return nil
	}
	for _, item := range items {
		action := repositories.AIReplyTurnActionRepository.GetByKeyInTenant(db, tenantID, turnID, strings.TrimSpace(item.TaskKey), strings.TrimSpace(item.ActionKey))
		if action == nil || action.RequestedVersion != turnVersion || action.Status != "prepared" || action.PreparedRevision != strings.TrimSpace(item.PreparedRevision) {
			return fmt.Errorf("AI reply action suppression evidence is invalid")
		}
		updated, err := repositories.AIReplyTurnActionRepository.CASStatusInTenant(db, action.ID, tenantID, []string{"prepared"}, map[string]any{
			"status": "suppressed", "result_code": strings.TrimSpace(item.ResultCode),
			"updated_at": now, "update_user_name": "ai_reply_action",
		})
		if err != nil {
			return err
		}
		if !updated {
			return fmt.Errorf("AI reply action suppression raced with another state change")
		}
	}
	return nil
}

func (s *aiReplyTurnActionService) MarkDeliveryByOutboxDB(db *gorm.DB, tenantID, outboxID int64, delivered bool, resultCode string, at time.Time) error {
	if db == nil || tenantID <= 0 || outboxID <= 0 {
		return nil
	}
	status := "delivery_failed"
	from := []string{"committed", "delivery_failed"}
	columns := map[string]any{"status": status, "result_code": strings.TrimSpace(resultCode), "updated_at": at, "update_user_name": "ai_reply_action"}
	if delivered {
		status = "delivered"
		columns["status"] = status
		columns["result_code"] = ""
		columns["delivered_at"] = at
	}
	for _, action := range repositories.AIReplyTurnActionRepository.FindByOutboxInTenant(db, tenantID, outboxID) {
		if action.Status == "delivered" && delivered {
			continue
		}
		updated, err := repositories.AIReplyTurnActionRepository.CASStatusInTenant(db, action.ID, tenantID, from, columns)
		if err != nil {
			return err
		}
		if updated {
			continue
		}
		current, err := repositories.AIReplyTurnActionRepository.GetForUpdateInTenant(db, action.ID, tenantID)
		if err != nil {
			return err
		}
		if current == nil {
			continue
		}
		// Channel delivery is an external fact. A ledger CAS miss must not roll
		// back the already persisted outbox result and cause a duplicate send.
		if delivered && current.Status == "delivered" || !delivered && (current.Status == "delivery_failed" || current.Status == "delivered") {
			continue
		}
	}
	return nil
}

func (s *aiReplyTurnActionService) SupersedeByOutboxDB(db *gorm.DB, tenantID, outboxID int64, resultCode string, at time.Time) error {
	if db == nil || tenantID <= 0 || outboxID <= 0 {
		return nil
	}
	resultCode = strings.TrimSpace(resultCode)
	if resultCode == "" {
		resultCode = "cancelled_stale_turn"
	}
	for _, action := range repositories.AIReplyTurnActionRepository.FindByOutboxInTenant(db, tenantID, outboxID) {
		if action.Status == "superseded" && action.ResultCode == resultCode {
			continue
		}
		updated, err := repositories.AIReplyTurnActionRepository.CASStatusInTenant(db, action.ID, tenantID, []string{"committed", "delivery_failed"}, map[string]any{
			"status": "superseded", "result_code": resultCode, "updated_at": at, "update_user_name": "outbox_stale_guard",
		})
		if err != nil {
			return err
		}
		if !updated {
			current, err := repositories.AIReplyTurnActionRepository.GetForUpdateInTenant(db, action.ID, tenantID)
			if err != nil {
				return err
			}
			if current != nil && current.Status != "superseded" && current.Status != "delivered" {
				return fmt.Errorf("AI reply action stale cancellation raced with another state change")
			}
		}
	}
	return nil
}

func (s *aiReplyTurnActionService) PreparedContracts(db *gorm.DB, tenantID, turnID int64, turnVersion int) []contracts.ActionLedgerItemV1 {
	items := repositories.AIReplyTurnActionRepository.FindPreparedByTurnInTenant(db, tenantID, turnID, turnVersion)
	ret := make([]contracts.ActionLedgerItemV1, 0, len(items))
	for _, item := range items {
		resourceType := item.ResourceType
		ret = append(ret, contracts.ActionLedgerItemV1{
			ActionKey: item.ActionKey, TaskKey: item.TaskKey, ActionType: item.ActionType, ResourceType: &resourceType,
			Status: item.Status, CommittedMessageID: item.CommittedMessageID, OutboxID: item.OutboxID, ResultCode: item.ResultCode,
		})
	}
	return ret
}

func (s *aiReplyTurnActionService) ContractsForTurn(db *gorm.DB, tenantID, turnID int64, turnVersion int) []contracts.ActionLedgerItemV1 {
	items := repositories.AIReplyTurnActionRepository.FindByTurnInTenant(db, tenantID, turnID)
	ret := make([]contracts.ActionLedgerItemV1, 0, len(items))
	for _, item := range items {
		if item.RequestedVersion != turnVersion {
			continue
		}
		resourceType := item.ResourceType
		ret = append(ret, contracts.ActionLedgerItemV1{
			ActionKey: item.ActionKey, TaskKey: item.TaskKey, ActionType: item.ActionType, ResourceType: &resourceType,
			Status: item.Status, CommittedMessageID: item.CommittedMessageID, OutboxID: item.OutboxID, ResultCode: item.ResultCode,
		})
	}
	return ret
}

func validAIReplyActionType(value string) bool {
	switch value {
	case "send_location", "send_mini_program", "send_phone", "send_knowledge_image", "tool_call", "human_handoff":
		return true
	default:
		return false
	}

}

func (s *aiReplyTurnActionService) EnsureRequested(turn *models.AIReplyTurn, inputs []AIReplyTurnActionInput) ([]models.AIReplyTurnAction, error) {
	var ret []models.AIReplyTurnAction
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		ret, err = s.EnsureRequestedDB(ctx.Tx, turn, inputs)
		return err
	})
	return ret, err
}
