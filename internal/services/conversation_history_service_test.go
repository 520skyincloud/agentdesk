package services

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
)

func TestConversationHistoryServiceOrdersLineageAndInterleavedMessageIDs(t *testing.T) {
	_, current, messages := setupConversationHistoryFixture(t)

	segments, err := ConversationHistoryService.ListSegments(current)
	if err != nil {
		t.Fatalf("list segments: %v", err)
	}
	if len(segments) != 3 {
		t.Fatalf("segments=%d, want 3: %+v", len(segments), segments)
	}
	for index := range segments {
		if segments[index].Index != index {
			t.Fatalf("segment index=%d at position %d", segments[index].Index, index)
		}
		wantInherited := index < 2
		if segments[index].InheritedHistory != wantInherited {
			t.Fatalf("segment %d inherited=%v, want %v", index, segments[index].InheritedHistory, wantInherited)
		}
	}

	page, cursor, hasMore, err := ConversationHistoryService.ListMessages(current, "", 20, "", "")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if cursor != "" || hasMore {
		t.Fatalf("unexpected terminal cursor=%q hasMore=%v", cursor, hasMore)
	}
	assertConversationHistoryContents(t, page, []string{
		"A-90", "A-100", "B-20", "C-5", "C-10",
	})
	if !page[0].InheritedHistory || !page[0].Message.HistoricalOnly {
		t.Fatalf("historical flags missing from first message: %+v", page[0])
	}
	if page[len(page)-1].InheritedHistory || page[len(page)-1].Message.HistoricalOnly {
		t.Fatalf("current message incorrectly marked historical: %+v", page[len(page)-1])
	}
	if messages[0].ID <= messages[len(messages)-1].ID {
		t.Fatal("fixture must use predecessor IDs larger than current IDs")
	}
}

func TestConversationHistoryServiceCurrentSegmentsExcludeInheritedConversations(t *testing.T) {
	_, current, _ := setupConversationHistoryFixture(t)

	segments, err := ConversationHistoryService.ListCurrentSegments(current)
	if err != nil {
		t.Fatalf("list current segments: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("current segments=%d, want 1: %+v", len(segments), segments)
	}
	if segments[0].ConversationID != current.ID || segments[0].InheritedHistory || !segments[0].CurrentConversation {
		t.Fatalf("current segment exposed inherited lineage: %+v", segments[0])
	}
}

func TestConversationHistoryServiceRequiresScopeForEveryPredecessor(t *testing.T) {
	db, current, _ := setupConversationHistoryFixture(t)
	if err := db.AutoMigrate(&models.AgentTeam{}, &models.WxWorkProtocolInstance{}, &models.ConversationRouteState{}); err != nil {
		t.Fatalf("migrate scope fixture: %v", err)
	}
	now := time.Now()
	oldInstance := &models.WxWorkProtocolInstance{
		TenantID: current.TenantID, Guid: "history-scope-old", StoreID: current.StoreID,
		HealthStatus: "online", Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	currentInstance := &models.WxWorkProtocolInstance{
		TenantID: current.TenantID, Guid: "history-scope-current", StoreID: current.StoreID,
		HealthStatus: "online", Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(oldInstance).Error; err != nil {
		t.Fatalf("create old instance: %v", err)
	}
	if err := db.Create(currentInstance).Error; err != nil {
		t.Fatalf("create current instance: %v", err)
	}
	var conversations []models.Conversation
	if err := db.Where("tenant_id = ? AND store_id = ? AND customer_id = ?", current.TenantID, current.StoreID, current.CustomerID).Order("id ASC").Find(&conversations).Error; err != nil {
		t.Fatalf("list lineage conversations: %v", err)
	}
	for i := range conversations {
		instanceID := oldInstance.ID
		if conversations[i].ID == current.ID {
			instanceID = currentInstance.ID
		}
		if err := db.Create(&models.ConversationRouteState{
			TenantID: current.TenantID, ConversationID: conversations[i].ID, StoreID: current.StoreID,
			StoreStaffBindingID: conversations[i].StoreStaffBindingID, WxWorkInstanceID: instanceID,
			RouteStatus: enums.ConversationRouteStatusClosed,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}).Error; err != nil {
			t.Fatalf("create route state: %v", err)
		}
	}
	team := &models.AgentTeam{
		TenantID: current.TenantID, Name: "history-scope-team", LeaderUserID: 707,
		WxWorkInstanceScopeIDs: fmt.Sprintf("%d", currentInstance.ID), Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(team).Error; err != nil {
		t.Fatalf("create scoped team: %v", err)
	}
	operator := &dto.AuthPrincipal{
		UserID: 707, ActiveTenantID: current.TenantID, Roles: []string{constants.RoleCodeCsTeamLeader},
	}
	allowed, err := ConversationHistoryService.CanViewLineage(current, operator)
	if err != nil {
		t.Fatalf("validate restricted lineage: %v", err)
	}
	if allowed {
		t.Fatal("current-instance scope exposed predecessor conversations")
	}
	if err := db.Model(team).Update("wx_work_instance_scope_ids", fmt.Sprintf("%d,%d", oldInstance.ID, currentInstance.ID)).Error; err != nil {
		t.Fatalf("expand team instance scope: %v", err)
	}
	allowed, err = ConversationHistoryService.CanViewLineage(current, operator)
	if err != nil || !allowed {
		t.Fatalf("complete lineage scope allowed=%v err=%v", allowed, err)
	}
}

func TestConversationHistoryServicePaginatesAcrossSegmentsWithoutLoss(t *testing.T) {
	_, current, _ := setupConversationHistoryFixture(t)
	cursor := ""
	loaded := make([]ConversationHistoryMessage, 0, 5)
	for pageNo := 0; pageNo < 10; pageNo++ {
		page, nextCursor, hasMore, err := ConversationHistoryService.ListMessages(current, cursor, 2, "", "")
		if err != nil {
			t.Fatalf("page %d: %v", pageNo, err)
		}
		loaded = append(page, loaded...)
		if !hasMore {
			break
		}
		if nextCursor == "" || nextCursor == cursor {
			t.Fatalf("page %d returned invalid cursor %q", pageNo, nextCursor)
		}
		cursor = nextCursor
	}
	assertConversationHistoryContents(t, loaded, []string{
		"A-90", "A-100", "B-20", "C-5", "C-10",
	})
	seen := make(map[int64]struct{}, len(loaded))
	for i := range loaded {
		if _, exists := seen[loaded[i].Message.ID]; exists {
			t.Fatalf("duplicate message id %d", loaded[i].Message.ID)
		}
		seen[loaded[i].Message.ID] = struct{}{}
	}
}

func TestConversationHistoryServiceRejectsTamperedAndStaleCursor(t *testing.T) {
	db, current, _ := setupConversationHistoryFixture(t)
	_, cursor, hasMore, err := ConversationHistoryService.ListMessages(current, "", 2, "", "")
	if err != nil || !hasMore || cursor == "" {
		t.Fatalf("create cursor: cursor=%q hasMore=%v err=%v", cursor, hasMore, err)
	}
	tampered := cursor[:len(cursor)-1] + map[bool]string{true: "A", false: "B"}[cursor[len(cursor)-1] != 'A']
	if _, _, _, err := ConversationHistoryService.ListMessages(current, tampered, 2, "", ""); err == nil {
		t.Fatal("tampered cursor was accepted")
	}

	now := time.Now().Add(time.Minute)
	if err := db.Create(&models.ConversationChannelSession{
		TenantID: current.TenantID, ConversationID: current.ID, SessionNo: 2,
		StoreID: current.StoreID, StoreStaffBindingID: current.StoreStaffBindingID,
		StartReason: "instance_changed", StartedAt: now, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("change history lineage: %v", err)
	}
	if _, _, _, err := ConversationHistoryService.ListMessages(current, cursor, 2, "", ""); err == nil || !strings.Contains(err.Error(), "游标已失效") {
		t.Fatalf("stale cursor error=%v", err)
	}
}

func TestConversationHistoryServiceRejectsMultiplePredecessors(t *testing.T) {
	db, current, _ := setupConversationHistoryFixture(t)
	if err := db.Migrator().DropIndex(&models.ConversationContinuityLink{}, "uk_conversation_continuity_successor"); err != nil {
		t.Fatalf("drop successor index for malformed fixture: %v", err)
	}
	now := time.Now()
	threadKey := "malformed-predecessor"
	extra := &models.Conversation{
		TenantID: current.TenantID, StoreID: current.StoreID, StoreStaffBindingID: 99,
		ThreadKey: &threadKey, CustomerID: current.CustomerID, CustomerName: current.CustomerName,
		Status: enums.IMConversationStatusClosed, ServiceMode: current.ServiceMode,
		LastActiveAt: now, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(extra).Error; err != nil {
		t.Fatalf("create extra predecessor: %v", err)
	}
	if err := db.Create(&models.ConversationContinuityLink{
		TenantID: current.TenantID, StoreID: current.StoreID, CustomerID: current.CustomerID,
		PredecessorConversationID: extra.ID, SuccessorConversationID: current.ID,
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create ambiguous predecessor: %v", err)
	}
	if _, err := ConversationHistoryService.ListSegments(current); err == nil || !strings.Contains(err.Error(), "线性关系") {
		t.Fatalf("ambiguous lineage error=%v", err)
	}
}

func setupConversationHistoryFixture(t *testing.T) (*gorm.DB, *models.Conversation, []models.Message) {
	t.Helper()
	db := setupMessageWelcomeTestDB(t)
	store := createContinuityTestStore(t, db, 101, "history-lineage")
	customer := createContinuityTestCustomer(t, db, store.TenantID, "history-lineage")
	now := time.Now().Add(-time.Hour)
	conversations := make([]models.Conversation, 3)
	for index := range conversations {
		threadKey := strings.Repeat(string(rune('a'+index)), 8)
		conversations[index] = models.Conversation{
			TenantID: store.TenantID, StoreID: store.ID, StoreStaffBindingID: int64(index + 1),
			ThreadKey: &threadKey, CustomerID: customer.ID, CustomerName: customer.Name,
			Status: enums.IMConversationStatusAIServing, ServiceMode: enums.IMConversationServiceModeAIFirst,
			LastActiveAt: now.Add(time.Duration(index) * time.Minute),
			AuditFields:  models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}
		if err := db.Create(&conversations[index]).Error; err != nil {
			t.Fatalf("create conversation %d: %v", index, err)
		}
		if err := db.Create(&models.ConversationChannelSession{
			TenantID: store.TenantID, ConversationID: conversations[index].ID, SessionNo: 1,
			StoreID: store.ID, StoreStaffBindingID: conversations[index].StoreStaffBindingID,
			StartReason: map[bool]string{true: "manual_inheritance", false: "initial"}[index > 0],
			StartedAt:   now.Add(time.Duration(index) * time.Minute), Status: enums.StatusOk,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}).Error; err != nil {
			t.Fatalf("create session %d: %v", index, err)
		}
	}
	for index := 0; index < 2; index++ {
		if err := db.Create(&models.ConversationContinuityLink{
			TenantID: store.TenantID, StoreID: store.ID, CustomerID: customer.ID,
			PredecessorConversationID: conversations[index].ID,
			SuccessorConversationID:   conversations[index+1].ID,
			Status:                    enums.StatusOk,
			AuditFields:               models.AuditFields{CreatedAt: now.Add(time.Duration(index) * time.Minute), UpdatedAt: now},
		}).Error; err != nil {
			t.Fatalf("create continuity link %d: %v", index, err)
		}
	}
	messages := []models.Message{
		{ID: 90, TenantID: store.TenantID, ConversationID: conversations[0].ID, SessionNo: 1, Content: "A-90", HistoricalOnly: true},
		{ID: 100, TenantID: store.TenantID, ConversationID: conversations[0].ID, SessionNo: 1, Content: "A-100"},
		{ID: 20, TenantID: store.TenantID, ConversationID: conversations[1].ID, SessionNo: 1, Content: "B-20"},
		{ID: 5, TenantID: store.TenantID, ConversationID: conversations[2].ID, SessionNo: 1, Content: "C-5"},
		{ID: 10, TenantID: store.TenantID, ConversationID: conversations[2].ID, SessionNo: 1, Content: "C-10"},
	}
	for index := range messages {
		messages[index].ClientMsgID = "history-" + messages[index].Content
		messages[index].SenderType = enums.IMSenderTypeCustomer
		messages[index].MessageType = enums.IMMessageTypeText
		messages[index].SeqNo = int64(index + 1)
		messages[index].AuditFields = models.AuditFields{CreatedAt: now.Add(time.Duration(index) * time.Second), UpdatedAt: now}
		if err := db.Create(&messages[index]).Error; err != nil {
			t.Fatalf("create message %s: %v", messages[index].Content, err)
		}
	}
	return db, &conversations[2], messages
}

func assertConversationHistoryContents(t *testing.T, messages []ConversationHistoryMessage, want []string) {
	t.Helper()
	if len(messages) != len(want) {
		t.Fatalf("messages=%d, want %d: %+v", len(messages), len(want), messages)
	}
	for index := range want {
		if messages[index].Message.Content != want[index] {
			t.Fatalf("message %d=%q, want %q", index, messages[index].Message.Content, want[index])
		}
	}
}
