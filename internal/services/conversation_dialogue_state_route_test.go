package services

import (
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
)

func TestConversationDialogueStateTracksHandoffAndResumeRouteModes(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	if err := db.AutoMigrate(&models.ConversationDialogueState{}); err != nil {
		t.Fatalf("migrate dialogue state: %v", err)
	}
	route := ConversationRouteService.GetByConversationIDInTenant(conversation.ID, conversation.TenantID)
	if route == nil {
		t.Fatal("route is missing")
	}
	now := time.Now().UTC()

	if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(db, route.ID, route.TenantID, map[string]any{
		"route_status": enums.ConversationRouteStatusHQAgentDeskPending, "updated_at": now,
	}); err != nil {
		t.Fatalf("set pending route: %v", err)
	}
	route = ConversationRouteService.GetByConversationIDInTenant(conversation.ID, conversation.TenantID)
	if _, err := ConversationDialogueStateService.CatchUpRouteStateDB(db, route, now); err != nil {
		t.Fatalf("catch up pending route: %v", err)
	}
	assertDialogueConversationMode(t, conversation, "human_pending")

	now = now.Add(time.Second)
	if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(db, route.ID, route.TenantID, map[string]any{
		"route_status": enums.ConversationRouteStatusHQAgentDeskServing, "updated_at": now,
	}); err != nil {
		t.Fatalf("set serving route: %v", err)
	}
	route = ConversationRouteService.GetByConversationIDInTenant(conversation.ID, conversation.TenantID)
	if _, err := ConversationDialogueStateService.CatchUpRouteStateDB(db, route, now); err != nil {
		t.Fatalf("catch up serving route: %v", err)
	}
	assertDialogueConversationMode(t, conversation, "human_serving")

	now = now.Add(time.Second)
	if err := ConversationRouteService.HoldManualRouteForAIResume(conversation.ID, now); err != nil {
		t.Fatalf("hold route for resume: %v", err)
	}
	assertDialogueConversationMode(t, conversation, "resume_pending")

	now = now.Add(time.Second)
	if err := ConversationRouteService.RestoreAI(conversation.ID, "resume test", now); err != nil {
		t.Fatalf("restore AI route: %v", err)
	}
	state := assertDialogueConversationMode(t, conversation, "ai_serving")
	if state.BasedOnMessageID != 0 || state.BasedOnTurnVersion != 0 {
		t.Fatalf("route-only events changed reply evidence: %+v", state)
	}
}

func assertDialogueConversationMode(t *testing.T, conversation *models.Conversation, want string) *contracts.DialogueStateSnapshotV1 {
	t.Helper()
	state, err := ConversationDialogueStateService.Load(conversation.TenantID, conversation.ID, 1)
	if err != nil {
		t.Fatalf("load dialogue state: %v", err)
	}
	if state == nil || state.ConversationMode != want {
		t.Fatalf("dialogue state=%+v want mode=%q", state, want)
	}
	return state
}
