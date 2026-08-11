package services

import (
	"encoding/json"
	"fmt"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/strictjson"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const dialogueStateMaxCASAttempts = 5

var ConversationDialogueStateService = &conversationDialogueStateService{}

type conversationDialogueStateService struct{}

func (s *conversationDialogueStateService) Load(tenantID, conversationID int64, sessionNo int) (*contracts.DialogueStateSnapshotV1, error) {
	db := sqls.DB()
	if db == nil || !db.Migrator().HasTable(&models.ConversationDialogueState{}) {
		return nil, nil
	}
	item := repositories.ConversationDialogueStateRepository.GetByScope(db, tenantID, conversationID, sessionNo)
	if item == nil {
		return nil, nil
	}
	return s.decode(item)
}

func (s *conversationDialogueStateService) ReduceCAS(tenantID, conversationID int64, sessionNo int, event DialogueStateEvent) (*contracts.DialogueStateSnapshotV1, error) {
	return s.reduceCASDB(sqls.DB(), tenantID, conversationID, sessionNo, event)
}

func (s *conversationDialogueStateService) reduceCASDB(db *gorm.DB, tenantID, conversationID int64, sessionNo int, event DialogueStateEvent) (*contracts.DialogueStateSnapshotV1, error) {
	if tenantID <= 0 || conversationID <= 0 || sessionNo <= 0 {
		return nil, fmt.Errorf("dialogue state scope is invalid")
	}
	if db == nil || !db.Migrator().HasTable(&models.ConversationDialogueState{}) {
		return nil, nil
	}
	for attempt := 0; attempt < dialogueStateMaxCASAttempts; attempt++ {
		item := repositories.ConversationDialogueStateRepository.GetByScope(db, tenantID, conversationID, sessionNo)
		if item == nil {
			initial := newDialogueStateSnapshot(conversationID, sessionNo, event.Now)
			reduced := ReduceDialogueState(initial, event)
			reduced.Revision = 1
			raw, err := encodeDialogueState(reduced)
			if err != nil {
				return nil, err
			}
			now := reduced.UpdatedAt
			created, err := repositories.ConversationDialogueStateRepository.CreateIfAbsent(db, &models.ConversationDialogueState{
				TenantID: tenantID, ConversationID: conversationID, SessionNo: sessionNo, Revision: 1,
				BasedOnMessageID: reduced.BasedOnMessageID, BasedOnTurnVersion: reduced.BasedOnTurnVersion,
				SchemaVersion: contracts.DialogueStateSnapshotV1SchemaVersion, SnapshotJSON: string(raw),
				AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now, CreateUserName: "dialogue_state", UpdateUserName: "dialogue_state"},
			})
			if err != nil {
				return nil, err
			}
			if created {
				return &reduced, nil
			}
			continue
		}
		current, err := s.decode(item)
		if err != nil {
			return nil, err
		}
		if dialogueStateEventIsStale(*current, event) {
			return current, nil
		}
		reduced := ReduceDialogueState(*current, event)
		reduced.Revision = item.Revision + 1
		raw, err := encodeDialogueState(reduced)
		if err != nil {
			return nil, err
		}
		updated, err := repositories.ConversationDialogueStateRepository.CASUpdate(db, item.ID, tenantID, item.Revision, map[string]any{
			"revision": reduced.Revision, "based_on_message_id": reduced.BasedOnMessageID, "based_on_turn_version": reduced.BasedOnTurnVersion,
			"schema_version": contracts.DialogueStateSnapshotV1SchemaVersion, "snapshot_json": string(raw),
			"updated_at": reduced.UpdatedAt, "update_user_name": "dialogue_state",
		})
		if err != nil {
			return nil, err
		}
		if updated {
			return &reduced, nil
		}
	}
	return nil, fmt.Errorf("dialogue state CAS retries exhausted")
}

func (s *conversationDialogueStateService) CatchUpRouteStateDB(db *gorm.DB, route *models.ConversationRouteState, now time.Time) (*contracts.DialogueStateSnapshotV1, error) {
	if route == nil || route.ID <= 0 || route.TenantID <= 0 || route.ConversationID <= 0 || route.SessionNo <= 0 {
		return nil, nil
	}
	mode := dialogueConversationModeForRoute(route.RouteStatus)
	if mode == "" {
		return nil, nil
	}
	return s.reduceCASDB(db, route.TenantID, route.ConversationID, route.SessionNo, DialogueStateEvent{
		Kind: DialogueStateEventRouteChanged, ConversationMode: mode, Now: now,
	})
}

func (s *conversationDialogueStateService) CatchUpResumePendingDB(db *gorm.DB, route *models.ConversationRouteState, now time.Time) (*contracts.DialogueStateSnapshotV1, error) {
	if route == nil || route.ID <= 0 || route.TenantID <= 0 || route.ConversationID <= 0 || route.SessionNo <= 0 {
		return nil, nil
	}
	return s.reduceCASDB(db, route.TenantID, route.ConversationID, route.SessionNo, DialogueStateEvent{
		Kind: DialogueStateEventResumeChanged, ConversationMode: "resume_pending", Now: now,
	})
}

func dialogueConversationModeForRoute(status enums.ConversationRouteStatus) string {
	switch status {
	case enums.ConversationRouteStatusAIServing, enums.ConversationRouteStatusAIFallback:
		return "ai_serving"
	case enums.ConversationRouteStatusHQAgentDeskPending:
		return "human_pending"
	case enums.ConversationRouteStatusStoreWecomManual, enums.ConversationRouteStatusHQAgentDeskServing:
		return "human_serving"
	case enums.ConversationRouteStatusClosed:
		return "closed"
	default:
		return ""
	}
}

func (s *conversationDialogueStateService) CatchUpCustomerMessage(message *models.Message) (*contracts.DialogueStateSnapshotV1, error) {
	if message == nil || message.ID <= 0 || message.TenantID <= 0 || message.ConversationID <= 0 || message.SessionNo <= 0 {
		return nil, nil
	}
	return s.ReduceCAS(message.TenantID, message.ConversationID, message.SessionNo, DialogueStateEvent{
		Kind: DialogueStateEventCustomerCommitted, MessageID: message.ID, Now: dialogueStateMessageEventTime(message),
	})
}

func (s *conversationDialogueStateService) CatchUpAgentMessage(message *models.Message) (*contracts.DialogueStateSnapshotV1, error) {
	if message == nil || message.ID <= 0 || message.TenantID <= 0 || message.ConversationID <= 0 || message.SessionNo <= 0 {
		return nil, nil
	}
	return s.ReduceCAS(message.TenantID, message.ConversationID, message.SessionNo, DialogueStateEvent{
		Kind: DialogueStateEventRouteChanged, MessageID: message.ID, ConversationMode: "human_serving",
		AssistantMessage: message, Now: dialogueStateMessageEventTime(message),
	})
}

func (s *conversationDialogueStateService) CatchUpAssistantBatch(turn *models.AIReplyTurn, messages []models.Message, resolvedTaskKeys []string) (*contracts.DialogueStateSnapshotV1, error) {
	if turn == nil || turn.ID <= 0 || turn.TenantID <= 0 || len(messages) == 0 {
		return nil, nil
	}
	tasks := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(sqls.DB(), turn.TenantID, turn.ID)
	actions := repositories.AIReplyTurnActionRepository.FindByTurnInTenant(sqls.DB(), turn.TenantID, turn.ID)
	last := messages[len(messages)-1]
	activeKeys := make([]string, 0, len(tasks))
	for _, task := range tasks {
		activeKeys = append(activeKeys, task.TaskKey)
	}
	return s.ReduceCAS(turn.TenantID, turn.ConversationID, turn.SessionNo, DialogueStateEvent{
		Kind: DialogueStateEventAssistantCommitted, MessageID: last.ID, TurnVersion: turn.Version,
		ActiveTaskKeys: activeKeys, Tasks: tasks, Actions: actions,
		ResolvedTaskKeys: resolvedTaskKeys, AssistantMessage: &last, ConversationMode: "ai_serving", Now: dialogueStateMessageEventTime(&last),
	})
}

func (s *conversationDialogueStateService) CatchUpTurn(turn *models.AIReplyTurn, messageID int64, dialogueAct, topic string) (*contracts.DialogueStateSnapshotV1, error) {
	if turn == nil || turn.ID <= 0 || turn.TenantID <= 0 {
		return nil, fmt.Errorf("dialogue state turn is invalid")
	}
	tasks := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(sqls.DB(), turn.TenantID, turn.ID)
	actions := repositories.AIReplyTurnActionRepository.FindByTurnInTenant(sqls.DB(), turn.TenantID, turn.ID)
	activeKeys := make([]string, 0, len(tasks))
	for _, task := range tasks {
		activeKeys = append(activeKeys, task.TaskKey)
	}
	eventTime := time.Now().UTC()
	if message := repositories.MessageRepository.GetInTenant(sqls.DB(), messageID, turn.TenantID); message != nil {
		eventTime = dialogueStateMessageEventTime(message)
	}
	return s.ReduceCAS(turn.TenantID, turn.ConversationID, turn.SessionNo, DialogueStateEvent{
		Kind: DialogueStateEventTasksChanged, MessageID: messageID, TurnVersion: turn.Version,
		DialogueAct: dialogueAct, Topic: topic, ActiveTaskKeys: activeKeys, Tasks: tasks, Actions: actions, Now: eventTime,
	})
}

func dialogueStateMessageEventTime(message *models.Message) time.Time {
	if message == nil {
		return time.Now().UTC()
	}
	if !message.CreatedAt.IsZero() {
		return message.CreatedAt.UTC()
	}
	if !message.UpdatedAt.IsZero() {
		return message.UpdatedAt.UTC()
	}
	if message.SentAt != nil && !message.SentAt.IsZero() {
		return message.SentAt.UTC()
	}
	return time.Now().UTC()
}

func (s *conversationDialogueStateService) decode(item *models.ConversationDialogueState) (*contracts.DialogueStateSnapshotV1, error) {
	if item == nil || item.SnapshotJSON == "" {
		return nil, nil
	}
	decoded, err := strictjson.DecodeObject[contracts.DialogueStateSnapshotV1]([]byte(item.SnapshotJSON), strictjson.DecodeOptions{
		MaxBytes: 64 * 1024, Schema: contracts.MustSchema(contracts.SchemaDialogueStateSnapshotV1),
	})
	if err != nil {
		return nil, err
	}
	if decoded.ConversationID != item.ConversationID || decoded.SessionNo != item.SessionNo || decoded.Revision != item.Revision ||
		decoded.BasedOnMessageID != item.BasedOnMessageID || decoded.BasedOnTurnVersion != item.BasedOnTurnVersion {
		return nil, fmt.Errorf("dialogue state snapshot scope does not match row")
	}
	return &decoded, nil
}

func newDialogueStateSnapshot(conversationID int64, sessionNo int, now time.Time) contracts.DialogueStateSnapshotV1 {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return contracts.DialogueStateSnapshotV1{
		SchemaVersion: contracts.DialogueStateSnapshotV1SchemaVersion, ConversationID: conversationID, SessionNo: sessionNo, Revision: 1,
		ConversationMode: "ai_serving", Focus: contracts.DialogueStateFocus{RelationToPrior: "unknown", ActiveTaskKeys: []string{}},
		ResolvedTasks: []contracts.DialogueStateResolvedTask{}, OpenTasks: []contracts.DialogueStateOpenTask{},
		SessionFacts: []contracts.DialogueStateSessionFact{}, LastAssistant: nil, UpdatedAt: now.UTC(),
	}
}

func encodeDialogueState(snapshot contracts.DialogueStateSnapshotV1) ([]byte, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if _, err := strictjson.DecodeObject[contracts.DialogueStateSnapshotV1](raw, strictjson.DecodeOptions{
		MaxBytes: 64 * 1024, Schema: contracts.MustSchema(contracts.SchemaDialogueStateSnapshotV1),
	}); err != nil {
		return nil, err
	}
	return raw, nil
}
