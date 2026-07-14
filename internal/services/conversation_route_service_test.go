package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestHQAgentDeskServingTracksPendingReply(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Conversation{}, &models.ConversationRouteState{}); err != nil {
		t.Fatalf("migrate route state: %v", err)
	}
	sqls.SetDB(db)
	conversation := &models.Conversation{TenantID: 101, Status: enums.IMConversationStatusAIServing, LastActiveAt: time.Now(), LastMessageAt: time.Now()}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	route := &models.ConversationRouteState{
		TenantID:          conversation.TenantID,
		ConversationID:    conversation.ID,
		RouteStatus:       enums.ConversationRouteStatusAIServing,
		NeedHumanFollowUp: false,
	}
	if err := db.Create(route).Error; err != nil {
		t.Fatalf("create route state: %v", err)
	}
	now := time.Now()

	state, err := ConversationRouteService.EnterHQAgentDeskServing(route.ConversationID, "测试派发", now)
	if err != nil {
		t.Fatalf("enter HQ serving: %v", err)
	}
	if !state.NeedHumanFollowUp {
		t.Fatal("a newly assigned HQ conversation should wait for an agent reply")
	}
	if err := ConversationRouteService.MarkAgentMessage(route.ConversationID, now.Add(time.Minute)); err != nil {
		t.Fatalf("mark agent message: %v", err)
	}
	if state = ConversationRouteService.GetByConversationID(route.ConversationID); state == nil || state.NeedHumanFollowUp {
		t.Fatal("an agent reply should clear pending reply")
	}
	if err := ConversationRouteService.MarkCustomerMessage(route.ConversationID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("mark customer message: %v", err)
	}
	if state = ConversationRouteService.GetByConversationID(route.ConversationID); state == nil || !state.NeedHumanFollowUp {
		t.Fatal("a new customer message should require another agent reply")
	}
}

func TestStoreManualTracksPendingReply(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Conversation{}, &models.ConversationRouteState{}); err != nil {
		t.Fatalf("migrate route state: %v", err)
	}
	sqls.SetDB(db)
	conversation := &models.Conversation{TenantID: 101, Status: enums.IMConversationStatusAIServing, LastActiveAt: time.Now(), LastMessageAt: time.Now()}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	route := &models.ConversationRouteState{
		TenantID:          conversation.TenantID,
		ConversationID:    conversation.ID,
		RouteStatus:       enums.ConversationRouteStatusStoreWecomManual,
		NeedHumanFollowUp: false,
	}
	if err := db.Create(route).Error; err != nil {
		t.Fatalf("create route state: %v", err)
	}
	now := time.Now()
	if err := ConversationRouteService.MarkCustomerMessage(route.ConversationID, now); err != nil {
		t.Fatalf("mark customer message: %v", err)
	}
	state := ConversationRouteService.GetByConversationID(route.ConversationID)
	if state == nil || !state.NeedHumanFollowUp {
		t.Fatal("a customer message during store manual service should require a reply")
	}
	if err := ConversationRouteService.MarkAgentMessage(route.ConversationID, now.Add(time.Minute)); err != nil {
		t.Fatalf("mark store reply: %v", err)
	}
	if state = ConversationRouteService.GetByConversationID(route.ConversationID); state == nil || state.NeedHumanFollowUp {
		t.Fatal("a store reply should clear pending reply")
	}
}
