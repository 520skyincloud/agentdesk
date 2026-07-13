package services_test

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"
)

func TestCustomerAutoHandoffSettingIsScopedToWxWorkInstance(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, "semi", "00:00-23:59")

	secondConversation := models.Conversation{
		AIAgentID:     aiAgent.ID,
		ChannelID:     1,
		CustomerID:    conversation.CustomerID,
		CustomerName:  "同一客户另一员工号",
		Status:        enums.IMConversationStatusAIServing,
		ServiceMode:   enums.IMConversationServiceModeAIFirst,
		LastMessageAt: time.Now(),
		LastActiveAt:  time.Now(),
	}
	if err := db.Create(&secondConversation).Error; err != nil {
		t.Fatalf("create second conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		ConversationID:   secondConversation.ID,
		WxWorkInstanceID: 78,
		RouteStatus:      enums.ConversationRouteStatusAIServing,
		RouteTarget:      "ai",
		SessionNo:        1,
	}).Error; err != nil {
		t.Fatalf("create second route: %v", err)
	}

	operator := &dto.AuthPrincipal{UserID: 9, Username: "operator"}
	if err := services.ConversationRouteService.SetPendingAction(conversation.ID, enums.ConversationPendingActionHumanHandoff, `{"reason":"test"}`, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("set pending action: %v", err)
	}
	if err := services.WxWorkCustomerHandoffSettingService.SetForConversation(conversation.ID, false, operator); err != nil {
		t.Fatalf("disable auto handoff: %v", err)
	}
	if services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabled(conversation.CustomerID, 77) {
		t.Fatal("expected first employee-account setting to be disabled")
	}
	if !services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabled(conversation.CustomerID, 78) {
		t.Fatal("expected second employee-account setting to keep its default enabled value")
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.PendingAction != "" || state.PendingActionExpireAt != nil {
		t.Fatalf("expected disabling auto handoff to clear the pending confirmation, got %+v", state)
	}
}
