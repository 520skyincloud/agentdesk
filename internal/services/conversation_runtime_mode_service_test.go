package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestConversationRuntimeModeProjection(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+testNameKey(t.Name())+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.WxWorkProtocolInstance{}, &models.AIManualResumeTask{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	instance := &models.WxWorkProtocolInstance{
		ID: 50, TenantID: 1, StoreID: 2, StoreStaffBindingID: 3,
		Guid: "runtime-mode-instance", AIReplyEnabled: true, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatal(err)
	}
	conversation := &models.Conversation{
		ID: 10, TenantID: 1, StoreID: 2, StoreStaffBindingID: 3, ChannelID: 4,
		Status: enums.IMConversationStatusPending, ServiceMode: enums.IMConversationServiceModeAIFirst,
	}
	route := &models.ConversationRouteState{
		TenantID: 1, ConversationID: 10, StoreID: 2, StoreStaffBindingID: 3,
		WxWorkInstanceID: 50, RouteStatus: enums.ConversationRouteStatusAIServing, SessionNo: 1,
	}

	decision := ConversationRuntimeModeService.ResolveDB(db, conversation, route)
	if decision.Mode != enums.ConversationRuntimeModeAIActive || !decision.AIReplyAllowed {
		t.Fatalf("AI route must remain authoritative when legacy conversation status lags: %#v", decision)
	}

	route.RouteStatus = enums.ConversationRouteStatusAIFallback
	decision = ConversationRuntimeModeService.ResolveDB(db, conversation, route)
	if decision.Mode != enums.ConversationRuntimeModeAIDegraded || !decision.AIReplyAllowed {
		t.Fatalf("AI fallback must still allow replies: %#v", decision)
	}

	route.RouteStatus = enums.ConversationRouteStatusHQAgentDeskPending
	decision = ConversationRuntimeModeService.ResolveDB(db, conversation, route)
	if decision.Mode != enums.ConversationRuntimeModeHumanPending || decision.AIReplyAllowed {
		t.Fatalf("pending human route must stop AI: %#v", decision)
	}

	if err := db.Create(&models.AIManualResumeTask{
		TenantID: 1, TaskKey: "runtime-mode-resume", ConversationID: 10,
		TaskStatus:  aiManualResumeTaskReady,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	decision = ConversationRuntimeModeService.ResolveDB(db, conversation, route)
	if decision.Mode != enums.ConversationRuntimeModeResumePending || decision.AIReplyAllowed {
		t.Fatalf("ready resume task must project resume_pending: %#v", decision)
	}

	conversation.CurrentAssigneeID = 99
	decision = ConversationRuntimeModeService.ResolveDB(db, conversation, route)
	if decision.Mode != enums.ConversationRuntimeModeHumanActive || decision.AIReplyAllowed {
		t.Fatalf("active assignee must own the conversation: %#v", decision)
	}
}
