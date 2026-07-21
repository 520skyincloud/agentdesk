package services

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestAgentDeskHandoffNotificationFollowsDispatchMode(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	if err := db.Model(&models.AgentTeam{}).Where("id = ?", 1).Update("leader_user_id", 101).Error; err != nil {
		t.Fatalf("set team leader: %v", err)
	}
	conversation := &models.Conversation{
		TenantID: 101, Status: enums.IMConversationStatusPending, CurrentTeamID: 1,
		CustomerName: "通知测试客户",
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	service := newConversationHumanDispatchService()
	service.notifyAgentDeskHandoff(conversation.ID, "需要人工处理")
	var count int64
	if err := db.Model(&models.Notification{}).Count(&count).Error; err != nil {
		t.Fatalf("count rule notifications: %v", err)
	}
	if count != 0 {
		t.Fatalf("rule handoff should wait for assignment notification, got %d", count)
	}

	if err := db.Model(&models.AgentTeam{}).Where("id = ?", 1).Update("dispatch_mode", enums.AgentTeamDispatchModeManual).Error; err != nil {
		t.Fatalf("set manual dispatch mode: %v", err)
	}
	service.notifyAgentDeskHandoff(conversation.ID, "需要组长编排")
	notifications := make([]models.Notification, 0)
	if err := db.Order("id ASC").Find(&notifications).Error; err != nil {
		t.Fatalf("find manual notifications: %v", err)
	}
	if len(notifications) != 1 || notifications[0].RecipientUserID != 101 || notifications[0].NotificationType != dispatchAttentionNotificationType {
		t.Fatalf("manual notifications=%+v", notifications)
	}
	service.notifyAgentDeskHandoff(conversation.ID, "需要组长编排")
	if err := db.Model(&models.Notification{}).Count(&count).Error; err != nil {
		t.Fatalf("count deduplicated notifications: %v", err)
	}
	if count != 1 {
		t.Fatalf("manual handoff notification should be deduplicated, got %d", count)
	}
}
