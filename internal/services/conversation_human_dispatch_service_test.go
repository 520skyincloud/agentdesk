package services_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/services"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestConversationHumanDispatchAIHandoffWithoutStoreRuntimeFallsBackToHQAgentDesk(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "1")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "我要找人工处理")

	result, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "用户要求转人工")
	if err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	if result == nil || result.Decision != services.HandoffDecisionHQAgentDesk {
		t.Fatalf("expected hq_agentdesk decision, got %+v", result)
	}

	current := services.ConversationService.Get(conversation.ID)
	if current.Status != enums.IMConversationStatusPending {
		t.Fatalf("expected conversation to enter pending HQ pool, got status=%d", current.Status)
	}
	if current.HandoffAt == nil || current.HandoffReason != "用户要求转人工" {
		t.Fatalf("expected handoff metadata, got at=%v reason=%q", current.HandoffAt, current.HandoffReason)
	}
	route := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if route == nil || route.RouteStatus != enums.ConversationRouteStatusHQAgentDeskPending || !route.NeedHumanFollowUp {
		t.Fatalf("expected HQ AgentDesk pending route, got %+v", route)
	}

	message := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if message == nil {
		t.Fatalf("expected HQ handoff notice message")
	}
	if message.SenderType != enums.IMSenderTypeAI || !strings.Contains(message.Content, services.HandoffWaitingMessage) {
		t.Fatalf("unexpected HQ handoff message: %+v", message)
	}
}

func TestConversationHumanDispatchAIHandoffEntersStoreManual(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "1")
	createHumanDispatchTeam(t, db, 1, "售后支持组")
	createHumanDispatchActiveSchedule(t, db, 1)
	createHumanDispatchAgentProfile(t, db, 101, 1, enums.ServiceStatusIdle, 3, true, enums.StatusOk)
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)

	result, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "用户要求转人工")
	if err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	if result == nil || result.Decision != services.HandoffDecisionStoreWecom {
		t.Fatalf("expected store_wecom decision, got %+v", result)
	}

	current := services.ConversationService.Get(conversation.ID)
	if current.Status != enums.IMConversationStatusAIServing {
		t.Fatalf("expected store manual handoff to keep ai-serving conversation shell, got status=%d", current.Status)
	}
	if current.CurrentAssigneeID != 0 || current.CurrentTeamID != 0 {
		t.Fatalf("expected no direct assignment: assignee=%d team=%d", current.CurrentAssigneeID, current.CurrentTeamID)
	}
	if current.HandoffAt == nil || current.HandoffReason != "用户要求转人工" {
		t.Fatalf("expected handoff metadata, got at=%v reason=%q", current.HandoffAt, current.HandoffReason)
	}
	route := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if route == nil || route.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || !route.NeedHumanFollowUp {
		t.Fatalf("expected store manual route with follow-up, got %+v", route)
	}
}

func TestConversationHumanDispatchSemiManagedEnqueuesStoreRoomNotice(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	setHumanDispatchConversationSummary(t, db, conversation.ID, "客人要求人工协助处理入住问题")
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")

	result, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "用户明确要求人工")
	if err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	if result == nil || result.Decision != services.HandoffDecisionStoreWecom {
		t.Fatalf("expected store_wecom decision, got %+v", result)
	}

	route := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if route == nil || route.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || route.RouteTarget != "store_wecom" {
		t.Fatalf("expected original store_wecom route, got %+v", route)
	}
	notice := findStoreRoomHandoffNoticeOutbox(t, db, conversation.ID)
	if notice.RoomConversationID != "R:room-100" {
		t.Fatalf("unexpected room conversation id: %#v", notice)
	}
	if len(notice.AtList) != 2 || notice.AtList[0] != "staff-1" || notice.AtList[1] != "staff-2" {
		t.Fatalf("unexpected at list: %#v", notice)
	}
	for _, want := range []string{"有客人需要人工接待", "客户：测试访客", "摘要：客人要求人工协助处理入住问题", "原因：用户明确要求人工"} {
		if !strings.Contains(notice.Content, want) {
			t.Fatalf("expected notice content to contain %q, got %q", want, notice.Content)
		}
	}
	if strings.Contains(notice.Content, "客户：1") {
		t.Fatalf("expected notice not to expose numeric customer ID, got %q", notice.Content)
	}
	if got, want := strings.Count(notice.Content, "\n"), 3; got != want {
		t.Fatalf("expected fixed four-line notice, got %d line breaks: %q", got, notice.Content)
	}
	for _, forbidden := range []string{"门店：", "会话ID：", "后台：", "human_complaint_risk", "model IntentDetect JSON"} {
		if strings.Contains(notice.Content, forbidden) {
			t.Fatalf("expected notice content not to contain %q, got %q", forbidden, notice.Content)
		}
	}
}

func TestConversationHumanDispatchStoreNoticeFallsBackToCustomerProfileName(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	if err := db.Create(&models.Customer{ID: conversation.CustomerID, Name: "生椰拿铁", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create customer profile: %v", err)
	}
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Update("customer_name", "").Error; err != nil {
		t.Fatalf("clear conversation customer name: %v", err)
	}
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")

	if _, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "用户明确要求人工"); err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	notice := findStoreRoomHandoffNoticeOutbox(t, db, conversation.ID)
	if !strings.Contains(notice.Content, "客户：生椰拿铁") || strings.Contains(notice.Content, "客户：1") {
		t.Fatalf("expected customer profile name in notice, got %q", notice.Content)
	}
}

func TestConversationHumanDispatchStoreRoomNoticeDoesNotAtWithoutConfiguration(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	setHumanDispatchConversationSummary(t, db, conversation.ID, "客人需要人工协助")
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	if err := db.Model(&models.StoreStaffBinding{}).Where("id = ?", 55).Update("store_room_at_list", "").Error; err != nil {
		t.Fatalf("clear store room at list: %v", err)
	}

	result, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "用户明确要求人工")
	if err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	if result == nil || result.Decision != services.HandoffDecisionStoreWecom {
		t.Fatalf("expected store_wecom decision, got %+v", result)
	}
	notice := findStoreRoomHandoffNoticeOutbox(t, db, conversation.ID)
	if len(notice.AtList) != 0 {
		t.Fatalf("expected no at list without explicit configuration, got %#v", notice.AtList)
	}
}

func TestConversationHumanDispatchStoreRoomNoticeSummarizesRelevantCustomerContext(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchMessage(t, db, conversation.ID, 1, enums.IMSenderTypeCustomer, "我受伤了哈")
	createHumanDispatchMessage(t, db, conversation.ID, 2, enums.IMSenderTypeAI, "这类安全情况建议让门店同事尽快介入。要我现在通知门店同事吗？请回复“确认”或“取消”。")
	createHumanDispatchMessage(t, db, conversation.ID, 3, enums.IMSenderTypeCustomer, "确认")
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")

	result, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "客人遇到安全或突发情况，需要门店同事尽快关注；客户消息：我受伤了哈")
	if err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	if result == nil || result.Decision != services.HandoffDecisionStoreWecom {
		t.Fatalf("expected store_wecom decision, got %+v", result)
	}

	notice := findStoreRoomHandoffNoticeOutbox(t, db, conversation.ID)
	for _, want := range []string{"摘要：客人表示遇到安全或突发情况", "原因：安全/突发情况：我受伤了哈"} {
		if !strings.Contains(notice.Content, want) {
			t.Fatalf("expected notice content to contain %q, got %q", want, notice.Content)
		}
	}
	for _, forbidden := range []string{"请回复“确认”或“取消”", "AI:", "AI：", "确认；", "model IntentDetect JSON", "human_complaint_risk", "会话ID：", "门店："} {
		if strings.Contains(notice.Content, forbidden) {
			t.Fatalf("expected notice content not to contain %q, got %q", forbidden, notice.Content)
		}
	}
}

func TestConversationHandoffDirectDispatchWithoutRoomUsesExactSuccessMessage(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "这个问题需要人工处理")

	handled, err := services.ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(conversation.ID, aiAgent, "客人需要人工接待；客户消息：我要找人", "req-direct", origin.ID)
	if err != nil {
		t.Fatalf("RequestByAIWithOriginMessage() error = %v", err)
	}
	if !handled {
		t.Fatal("expected direct handoff to be handled")
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.PendingAction != "" {
		t.Fatalf("expected immediate store manual route without confirmation pending, got %+v", state)
	}
	latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if latest == nil || latest.Content != services.DirectHandoffSuccessMessage {
		t.Fatalf("expected exact direct handoff success message, got %+v", latest)
	}
	if strings.Contains(latest.Content, "确认") || strings.Contains(latest.Content, "取消") {
		t.Fatalf("direct handoff must not ask for confirmation, got %q", latest.Content)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 1 {
		t.Fatalf("expected one store room notice after direct handoff, got %d", count)
	}
	if count := countHumanDispatchMessages(t, db, conversation.ID, services.DirectHandoffSuccessMessage); count != 1 {
		t.Fatalf("expected one exact success message, got %d", count)
	}
	if count := countManualResumeTasks(t, db, conversation.ID); count != 1 {
		t.Fatalf("expected one manual resume task, got %d", count)
	}
}

func TestConversationHandoffRoomPromptFailureClearsPendingState(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "马桶堵了")
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Update("status", enums.IMConversationStatusClosed).Error; err != nil {
		t.Fatalf("close conversation: %v", err)
	}

	handled, err := services.ConversationHandoffConfirmationService.RequestByAI(conversation.ID, aiAgent, "知识库规则要求门店同事接手", "req-send-fails")
	if err == nil || handled {
		t.Fatalf("expected room prompt send failure to be retryable, handled=%v err=%v", handled, err)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.PendingAction != "" {
		t.Fatalf("expected failed prompt send to clear pending action for retry, got %+v", state)
	}
}

func TestConversationHandoffDirectDispatchIsIdempotentForOriginMessage(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "帮我转人工")
	backdateHumanDispatchMessage(t, db, origin.ID, time.Now().Add(-time.Second))

	for attempt := 0; attempt < 2; attempt++ {
		handled, err := services.ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(
			conversation.ID,
			aiAgent,
			"客户明确要求人工接待",
			"req-idempotent-direct",
			origin.ID,
		)
		if err != nil || !handled {
			t.Fatalf("RequestByAIWithOriginMessage() attempt %d handled=%v err=%v", attempt+1, handled, err)
		}
	}

	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.PendingAction != "" {
		t.Fatalf("expected one active manual route without confirmation pending, got %+v", state)
	}
	if count := countHumanDispatchMessages(t, db, conversation.ID, services.DirectHandoffSuccessMessage); count != 1 {
		t.Fatalf("expected one stable success message after retry, got %d", count)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 1 {
		t.Fatalf("expected one store room notice after retry, got %d", count)
	}
	if count := countManualResumeTasks(t, db, conversation.ID); count != 1 {
		t.Fatalf("expected one stable manual resume task after retry, got %d", count)
	}
}

func TestConversationHandoffAlreadyActiveDifferentOriginDoesNotDuplicateSuccessOrResumeTask(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	firstOrigin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "帮我转人工")

	first, err := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"客户明确要求人工接待",
		"req-direct-first-origin",
		firstOrigin.ID,
	)
	if err != nil {
		t.Fatalf("first DispatchByAIWithOriginMessage() error = %v", err)
	}
	if first == nil || first.Status != services.HandoffDispatchStatusDispatched {
		t.Fatalf("expected first origin to dispatch, got %+v", first)
	}
	if count := countHumanDispatchMessages(t, db, conversation.ID, services.DirectHandoffSuccessMessage); count != 1 {
		t.Fatalf("expected one success message after first origin, got %d", count)
	}
	if count := countManualResumeTasks(t, db, conversation.ID); count != 1 {
		t.Fatalf("expected one resume task after first origin, got %d", count)
	}

	secondOrigin := createHumanDispatchMessage(t, db, conversation.ID, 20, enums.IMSenderTypeCustomer, "我再补充一句")
	second, err := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"另一条消息再次触发人工接待",
		"req-direct-second-origin",
		secondOrigin.ID,
	)
	if err != nil {
		t.Fatalf("second DispatchByAIWithOriginMessage() error = %v", err)
	}
	if second == nil || second.Status != services.HandoffDispatchStatusAlreadyActive {
		t.Fatalf("expected different origin to observe already-active route, got %+v", second)
	}
	if count := countHumanDispatchMessages(t, db, conversation.ID, services.DirectHandoffSuccessMessage); count != 1 {
		t.Fatalf("different origin must not add another success message, got %d", count)
	}
	if count := countManualResumeTasks(t, db, conversation.ID); count != 1 {
		t.Fatalf("different origin must not add another resume task, got %d", count)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 1 {
		t.Fatalf("different origin must not add another store room notice, got %d", count)
	}
}

func TestConversationEmergencyHandoffBypassesRoomCollection(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "门锁坏了，我被困在房间里")

	result, err := services.ConversationHandoffConfirmationService.DispatchEmergencyByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"客人遇到安全或突发情况，需要门店同事尽快关注；客户消息：门锁坏了，我被困在房间里",
		"req-emergency-direct",
		origin.ID,
	)
	if err != nil {
		t.Fatalf("DispatchEmergencyByAIWithOriginMessage() error = %v", err)
	}
	if result == nil || result.Status != services.HandoffDispatchStatusDispatched {
		t.Fatalf("expected emergency direct dispatch, got %+v", result)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.PendingAction != "" {
		t.Fatalf("emergency route must not wait for room collection, got %+v", state)
	}
	if count := countHumanDispatchMessages(t, db, conversation.ID, services.DirectHandoffSuccessMessage); count != 1 {
		t.Fatalf("expected one emergency direct success message, got %d", count)
	}
}

func TestConversationHandoffRetryRepairsMissingSuccessOutboxWithoutDuplicatingMessage(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	createHumanDispatchWxWorkProtocolChannel(t, db, 1)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "帮我转人工")
	backdateHumanDispatchMessage(t, db, origin.ID, time.Now().Add(-time.Second))

	result, err := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"客户明确要求人工接待",
		"req-repair-success-outbox",
		origin.ID,
	)
	if err != nil || result == nil || result.Status != services.HandoffDispatchStatusDispatched {
		t.Fatalf("first dispatch result=%+v err=%v", result, err)
	}
	clientMsgID := "ai_handoff_success_" + result.HandoffToken
	successMessage := services.MessageService.FindOne(sqls.NewCnd().
		Eq("conversation_id", conversation.ID).
		Eq("client_msg_id", clientMsgID))
	if successMessage == nil {
		t.Fatalf("expected stable success message %q", clientMsgID)
	}
	if count := countChannelOutboxesForMessage(t, db, enums.ChannelTypeWxWorkProtocol, successMessage.ID); count != 1 {
		t.Fatalf("expected one success outbox before deletion, got %d", count)
	}
	if err := db.Where("channel_type = ? AND message_id = ?", enums.ChannelTypeWxWorkProtocol, successMessage.ID).Delete(&models.ChannelMessageOutbox{}).Error; err != nil {
		t.Fatalf("delete success outbox: %v", err)
	}

	retried, err := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"客户明确要求人工接待",
		"req-repair-success-outbox",
		origin.ID,
	)
	if err != nil || retried == nil || retried.Status != services.HandoffDispatchStatusAlreadyActive {
		t.Fatalf("retry result=%+v err=%v", retried, err)
	}
	if count := countMessagesByClientMsgID(t, db, conversation.ID, clientMsgID); count != 1 {
		t.Fatalf("retry must reuse the stable success message, got %d", count)
	}
	if count := countChannelOutboxesForMessage(t, db, enums.ChannelTypeWxWorkProtocol, successMessage.ID); count != 1 {
		t.Fatalf("retry must repair the missing success outbox, got %d", count)
	}
}

func TestConversationHandoffRetryRepairsMissingStoreRoomNoticeOutboxIdempotently(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "帮我转人工")
	backdateHumanDispatchMessage(t, db, origin.ID, time.Now().Add(-time.Second))

	result, err := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"客户明确要求人工接待",
		"req-repair-store-notice",
		origin.ID,
	)
	if err != nil || result == nil || result.Status != services.HandoffDispatchStatusDispatched {
		t.Fatalf("first dispatch result=%+v err=%v", result, err)
	}
	noticeOutbox := findStoreRoomHandoffNoticeOutboxModel(t, db, conversation.ID)
	if err := db.Delete(&models.ChannelMessageOutbox{}, noticeOutbox.ID).Error; err != nil {
		t.Fatalf("delete store-room notice outbox: %v", err)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 0 {
		t.Fatalf("expected deleted store-room notice before repair, got %d", count)
	}

	for attempt := 0; attempt < 2; attempt++ {
		retried, retryErr := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
			conversation.ID,
			aiAgent,
			"客户明确要求人工接待",
			"req-repair-store-notice",
			origin.ID,
		)
		if retryErr != nil || retried == nil || retried.Status != services.HandoffDispatchStatusAlreadyActive {
			t.Fatalf("retry %d result=%+v err=%v", attempt+1, retried, retryErr)
		}
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 1 {
		t.Fatalf("retry must repair exactly one stable store-room notice, got %d", count)
	}
	repairedNotice := findStoreRoomHandoffNoticeOutboxModel(t, db, conversation.ID)
	if repairedNotice.MessageID != noticeOutbox.MessageID {
		t.Fatalf("store-room notice repair must reuse stable message id: got %d want %d", repairedNotice.MessageID, noticeOutbox.MessageID)
	}
	if count := countHumanDispatchMessages(t, db, conversation.ID, services.DirectHandoffSuccessMessage); count != 1 {
		t.Fatalf("store notice repair must not duplicate success messages, got %d", count)
	}
	if count := countManualResumeTasks(t, db, conversation.ID); count != 1 {
		t.Fatalf("store notice repair must not duplicate resume tasks, got %d", count)
	}
}

func TestConversationHandoffHQNotificationIsIdempotentForSameToken(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	createHumanDispatchTeam(t, db, 1, "总部接待组")
	createHumanDispatchAgentProfile(t, db, 101, 1, enums.ServiceStatusIdle, 3, true, enums.StatusOk)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "帮我转人工")
	backdateHumanDispatchMessage(t, db, origin.ID, time.Now().Add(-time.Second))

	result, err := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"客户明确要求人工接待",
		"req-hq-notification-idempotent",
		origin.ID,
	)
	if err != nil || result == nil || result.Status != services.HandoffDispatchStatusDispatched || result.Decision != services.HandoffDecisionHQAgentDesk {
		t.Fatalf("first HQ dispatch result=%+v err=%v", result, err)
	}
	if count := countManualHandoffNotifications(t, db, conversation.ID, 101); count != 1 {
		t.Fatalf("expected one HQ handoff notification after dispatch, got %d", count)
	}

	for attempt := 0; attempt < 2; attempt++ {
		retried, retryErr := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
			conversation.ID,
			aiAgent,
			"客户明确要求人工接待",
			"req-hq-notification-idempotent",
			origin.ID,
		)
		if retryErr != nil || retried == nil || retried.Status != services.HandoffDispatchStatusAlreadyActive {
			t.Fatalf("HQ retry %d result=%+v err=%v", attempt+1, retried, retryErr)
		}
	}
	if count := countManualHandoffNotifications(t, db, conversation.ID, 101); count != 1 {
		t.Fatalf("same HQ handoff token must not duplicate notifications, got %d", count)
	}
	if count := countHumanDispatchMessages(t, db, conversation.ID, services.DirectHandoffSuccessMessage); count != 1 {
		t.Fatalf("same HQ handoff token must not duplicate success messages, got %d", count)
	}
	if count := countManualResumeTasks(t, db, conversation.ID); count != 1 {
		t.Fatalf("same HQ handoff token must not duplicate resume tasks, got %d", count)
	}
}

func TestConversationHandoffRoomReplyDoesNotDispatchAfterPendingConsumedOrExpired(t *testing.T) {
	for _, tc := range []struct {
		name       string
		invalidate func(t *testing.T, db *gorm.DB, conversationID int64)
	}{
		{
			name: "consumed",
			invalidate: func(t *testing.T, db *gorm.DB, conversationID int64) {
				t.Helper()
				if _, ok, err := services.ConversationRouteService.ConsumePendingAction(conversationID, enums.ConversationPendingActionHumanHandoff, time.Now()); err != nil || !ok {
					t.Fatalf("consume pending room action ok=%v err=%v", ok, err)
				}
			},
		},
		{
			name: "expired",
			invalidate: func(t *testing.T, db *gorm.DB, conversationID int64) {
				t.Helper()
				if err := db.Model(&models.ConversationRouteState{}).
					Where("conversation_id = ?", conversationID).
					Update("pending_action_expire_at", time.Now().Add(-time.Minute)).Error; err != nil {
					t.Fatalf("expire pending room action: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupConversationHumanDispatchTestDB(t)
			aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
			conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
			createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
			origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "马桶堵了")
			result, err := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
				conversation.ID,
				aiAgent,
				"知识库规则要求门店同事接手；客户消息：马桶堵了",
				"req-room-race-"+tc.name,
				origin.ID,
			)
			if err != nil || result == nil || result.Status != services.HandoffDispatchStatusAwaitingRoomNumber {
				t.Fatalf("room request result=%+v err=%v", result, err)
			}
			tc.invalidate(t, db, conversation.ID)

			roomReply := createHumanDispatchMessage(t, db, conversation.ID, 20, enums.IMSenderTypeCustomer, "1305")
			if _, err := services.ConversationHandoffConfirmationService.HandleCustomerMessage(&conversation, &roomReply); err != nil {
				t.Fatalf("HandleCustomerMessage(room) error = %v", err)
			}
			state := services.ConversationRouteService.GetByConversationID(conversation.ID)
			if state == nil || state.RouteStatus != enums.ConversationRouteStatusAIServing {
				t.Fatalf("invalid pending room reply must not enter human route, got %+v", state)
			}
			if count := countHumanDispatchMessages(t, db, conversation.ID, services.DirectHandoffSuccessMessage); count != 0 {
				t.Fatalf("invalid pending room reply must not send success, got %d", count)
			}
			if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 0 {
				t.Fatalf("invalid pending room reply must not notify store room, got %d", count)
			}
			if count := countManualResumeTasks(t, db, conversation.ID); count != 0 {
				t.Fatalf("invalid pending room reply must not create resume task, got %d", count)
			}
		})
	}
}

func TestConversationHandoffManagedNoneWithActiveTeamDispatchesInsteadOfOffHours(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "1")
	createHumanDispatchTeam(t, db, 1, "售后支持组")
	createHumanDispatchActiveSchedule(t, db, 1)
	createHumanDispatchAgentProfile(t, db, 101, 0, enums.ServiceStatusIdle, 3, false, enums.StatusOk)
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeNone, "")
	if err := db.Model(&models.StoreStaffBinding{}).Where("id = ?", 55).Updates(map[string]any{
		"store_room_notify_enabled": false,
		"fallback_to_hq":            false,
	}).Error; err != nil {
		t.Fatalf("disable store room handoff: %v", err)
	}
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", 77).Updates(map[string]any{
		"store_room_notify_enabled": false,
		"fallback_to_hq":            false,
	}).Error; err != nil {
		t.Fatalf("disable instance room handoff: %v", err)
	}
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "帮我转人工")

	result, err := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"客户明确要求人工接待",
		"req-managed-none-active-team",
		origin.ID,
	)
	if err != nil {
		t.Fatalf("DispatchByAIWithOriginMessage() error = %v", err)
	}
	if result == nil || result.Status != services.HandoffDispatchStatusDispatched || result.Decision != services.HandoffDecisionTeamPool {
		t.Fatalf("expected active team dispatch instead of off-hours, got %+v", result)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusHQAgentDeskPending {
		t.Fatalf("expected pending human route for active team, got %+v", state)
	}
	current := services.ConversationService.Get(conversation.ID)
	if current == nil || current.Status != enums.IMConversationStatusPending || current.CurrentTeamID != 1 {
		t.Fatalf("expected conversation in active team pool, got %+v", current)
	}
	if count := countHumanDispatchMessages(t, db, conversation.ID, services.DirectHandoffSuccessMessage); count != 1 {
		t.Fatalf("expected one direct success message, got %d", count)
	}
	if count := countHumanDispatchMessages(t, db, conversation.ID, services.HandoffOffHoursMessage); count != 0 {
		t.Fatalf("active team route must not send off-hours message, got %d", count)
	}
	if count := countManualResumeTasks(t, db, conversation.ID); count != 1 {
		t.Fatalf("expected one resume task for active team route, got %d", count)
	}
	if count := countManualHandoffNotifications(t, db, conversation.ID, 101); count != 1 {
		t.Fatalf("expected one stable HQ notification for active team route, got %d", count)
	}
	retried, retryErr := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"客户明确要求人工接待",
		"req-managed-none-active-team",
		origin.ID,
	)
	if retryErr != nil || retried == nil || retried.Status != services.HandoffDispatchStatusAlreadyActive {
		t.Fatalf("retry result=%+v err=%v", retried, retryErr)
	}
	if count := countManualHandoffNotifications(t, db, conversation.ID, 101); count != 1 {
		t.Fatalf("retry must not duplicate the active-team HQ notification, got %d", count)
	}
}

func TestConversationHandoffAssignedRetryDoesNotCreatePendingHQNotification(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "1")
	createHumanDispatchTeam(t, db, 1, "售后支持组")
	createHumanDispatchActiveSchedule(t, db, 1)
	createHumanDispatchAgentProfile(t, db, 101, 1, enums.ServiceStatusIdle, 3, true, enums.StatusOk)
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeNone, "")
	if err := db.Model(&models.StoreStaffBinding{}).Where("id = ?", 55).Updates(map[string]any{
		"store_room_notify_enabled": false,
		"fallback_to_hq":            false,
	}).Error; err != nil {
		t.Fatalf("disable store room handoff: %v", err)
	}
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", 77).Updates(map[string]any{
		"store_room_notify_enabled": false,
		"fallback_to_hq":            false,
	}).Error; err != nil {
		t.Fatalf("disable instance room handoff: %v", err)
	}
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "帮我转人工")

	result, err := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"客户明确要求人工接待",
		"req-assigned-direct",
		origin.ID,
	)
	if err != nil || result == nil || result.Status != services.HandoffDispatchStatusDispatched || result.Decision != services.HandoffDecisionAssigned {
		t.Fatalf("first assigned dispatch result=%+v err=%v", result, err)
	}
	if count := countManualHandoffNotifications(t, db, conversation.ID, 101); count != 0 {
		t.Fatalf("assigned route must not create a pending-HQ notification, got %d", count)
	}

	retried, retryErr := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"客户明确要求人工接待",
		"req-assigned-direct",
		origin.ID,
	)
	if retryErr != nil || retried == nil || retried.Status != services.HandoffDispatchStatusAlreadyActive {
		t.Fatalf("retry result=%+v err=%v", retried, retryErr)
	}
	if count := countManualHandoffNotifications(t, db, conversation.ID, 101); count != 0 {
		t.Fatalf("assigned retry must not add a pending-HQ notification, got %d", count)
	}
	if count := countHumanDispatchMessages(t, db, conversation.ID, services.DirectHandoffSuccessMessage); count != 1 {
		t.Fatalf("assigned retry must keep one success message, got %d", count)
	}
	if count := countManualResumeTasks(t, db, conversation.ID); count != 1 {
		t.Fatalf("assigned retry must keep one resume task, got %d", count)
	}
}

func TestConversationHandoffConfirmationUsesDeferredQuestionForRoomDecision(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "空调不制冷，发票能备注吗")

	if _, err := services.ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"部分酒店业务问题需要门店同事接手；待处理问题：发票能备注吗",
		"req-deferred-room-scope",
		origin.ID,
	); err != nil {
		t.Fatalf("RequestByAIWithOriginMessage() error = %v", err)
	}

	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.PendingAction != "" {
		t.Fatalf("expected deferred invoice question to dispatch directly without room collection, got %+v", state)
	}
	message := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if message == nil || message.Content != services.DirectHandoffSuccessMessage {
		t.Fatalf("expected exact direct success message for deferred invoice question, got %+v", message)
	}
}

func TestConversationHandoffConfirmationUsesRoomFromSameCustomerBurst(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "空调坏了")
	createHumanDispatchMessage(t, db, conversation.ID, 20, enums.IMSenderTypeCustomer, "我住1302")
	origin := createHumanDispatchMessage(t, db, conversation.ID, 30, enums.IMSenderTypeCustomer, "顺便问早餐几点")

	if _, err := services.ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"部分酒店业务问题需要门店同事接手；待处理问题：空调坏了",
		"req-deferred-room-burst",
		origin.ID,
	); err != nil {
		t.Fatalf("RequestByAIWithOriginMessage() error = %v", err)
	}

	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.PendingAction != "" || !strings.Contains(state.HandoffReason, "客户补充房号：1302") {
		t.Fatalf("expected room 1302 from the same customer burst to dispatch directly, got %+v", state)
	}
	latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if latest == nil || latest.Content != services.DirectHandoffSuccessMessage {
		t.Fatalf("expected direct handoff after burst room recovery, got %+v", latest)
	}
}

func TestConversationHandoffConfirmationDoesNotReuseRoomBeforeLatestOutbound(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "我住1302")
	createHumanDispatchMessage(t, db, conversation.ID, 20, enums.IMSenderTypeAI, "好的，有需要再告诉我")
	createHumanDispatchMessage(t, db, conversation.ID, 30, enums.IMSenderTypeCustomer, "空调坏了")
	origin := createHumanDispatchMessage(t, db, conversation.ID, 40, enums.IMSenderTypeCustomer, "顺便问早餐几点")

	if _, err := services.ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"部分酒店业务问题需要门店同事接手；待处理问题：空调坏了",
		"req-deferred-room-outbound-boundary",
		origin.ID,
	); err != nil {
		t.Fatalf("RequestByAIWithOriginMessage() error = %v", err)
	}

	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || !strings.Contains(state.PendingActionPayload, `"awaitingField":"room_number"`) || strings.Contains(state.PendingActionPayload, `"roomNumber":"1302"`) {
		t.Fatalf("room before the latest outbound must not be reused, got %+v", state)
	}
	latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if latest == nil || latest.Content != "方便说下是哪个房间吗？" {
		t.Fatalf("expected a fresh room question after the outbound boundary, got %+v", latest)
	}
}

func TestConversationHandoffConfirmationDoesNotReuseRoomOutsideBurstWindow(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	room := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "我住1302")
	createHumanDispatchMessage(t, db, conversation.ID, 20, enums.IMSenderTypeCustomer, "空调坏了")
	origin := createHumanDispatchMessage(t, db, conversation.ID, 30, enums.IMSenderTypeCustomer, "顺便问早餐几点")
	staleAt := origin.SentAt.Add(-9 * time.Second)
	if err := db.Model(&models.Message{}).Where("id = ?", room.ID).Update("sent_at", staleAt).Error; err != nil {
		t.Fatalf("backdate room message: %v", err)
	}

	if _, err := services.ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"部分酒店业务问题需要门店同事接手；待处理问题：空调坏了",
		"req-deferred-room-time-boundary",
		origin.ID,
	); err != nil {
		t.Fatalf("RequestByAIWithOriginMessage() error = %v", err)
	}

	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || !strings.Contains(state.PendingActionPayload, `"awaitingField":"room_number"`) || strings.Contains(state.PendingActionPayload, `"roomNumber":"1302"`) {
		t.Fatalf("room outside the short customer burst must not be reused, got %+v", state)
	}
}

func TestConversationLegacyHandoffConfirmationRemainsCompatibleWithinFiveMinutes(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	restore := services.SetHumanHandoffConfirmationClassifierForTest(func(ctx context.Context, conversation *models.Conversation, message *models.Message, reason string, text string) (string, float64, string) {
		return "confirm", 0.99, "legacy confirmation reply"
	})
	t.Cleanup(restore)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "我要找人工")

	start := time.Now()
	payload := fmt.Sprintf(`{"reason":"客人需要人工接待","aiAgentId":%d,"originMessageId":%d,"handoffToken":"legacy-five-minutes","createdAt":"%s"}`, aiAgent.ID, origin.ID, start.Format(time.RFC3339))
	if err := services.ConversationRouteService.SetPendingAction(conversation.ID, enums.ConversationPendingActionHumanHandoff, payload, start.Add(services.DefaultHandoffConfirmationMinutes*time.Minute)); err != nil {
		t.Fatalf("SetPendingAction() error = %v", err)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.PendingAction != string(enums.ConversationPendingActionHumanHandoff) || state.PendingActionExpireAt == nil {
		t.Fatalf("expected legacy pending handoff with expiry, got %+v", state)
	}
	minExpire := start.Add(services.DefaultHandoffConfirmationMinutes * time.Minute).Add(-2 * time.Second)
	maxExpire := start.Add(services.DefaultHandoffConfirmationMinutes * time.Minute).Add(2 * time.Second)
	if state.PendingActionExpireAt.Before(minExpire) || state.PendingActionExpireAt.After(maxExpire) {
		t.Fatalf("expected pending expiry around 5 minutes, got %v", state.PendingActionExpireAt)
	}
	confirm := createHumanDispatchMessage(t, db, conversation.ID, 20, enums.IMSenderTypeCustomer, "确认")
	handled, err := services.ConversationHandoffConfirmationService.HandleCustomerMessage(&conversation, &confirm)
	if err != nil || !handled {
		t.Fatalf("HandleCustomerMessage(legacy confirm) handled=%v err=%v", handled, err)
	}
	state = services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.PendingAction != "" || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual {
		t.Fatalf("expected legacy confirmation to dispatch successfully, got %+v", state)
	}
	latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if latest == nil || latest.Content != services.DirectHandoffSuccessMessage {
		t.Fatalf("expected legacy confirmation to use current success wording, got %+v", latest)
	}
}

func TestConversationHandoffConfirmationSemanticConfirmDispatches(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	restore := services.SetHumanHandoffConfirmationClassifierForTest(func(ctx context.Context, conversation *models.Conversation, message *models.Message, reason string, text string) (string, float64, string) {
		if text == "那你帮我叫一下人吧" {
			return "confirm", 0.94, "客人同意通知人工"
		}
		return "unknown", 0.4, "not sure"
	})
	t.Cleanup(restore)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")

	seedLegacyHandoffConfirmation(t, conversation.ID, aiAgent.ID, 10, "legacy-semantic-confirm", "客人需要人工接待；客户消息：我要找人")
	confirm := createHumanDispatchMessage(t, db, conversation.ID, 20, enums.IMSenderTypeCustomer, "那你帮我叫一下人吧")
	handled, err := services.ConversationHandoffConfirmationService.HandleCustomerMessage(&conversation, &confirm)
	if err != nil {
		t.Fatalf("HandleCustomerMessage(confirm) error = %v", err)
	}
	if !handled {
		t.Fatalf("expected semantic confirmation to be consumed")
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.PendingAction != "" || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual {
		t.Fatalf("expected store manual route after semantic confirmation, got %+v", state)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 1 {
		t.Fatalf("expected one store room notice after semantic confirmation, got %d", count)
	}
	notice := findStoreRoomHandoffNoticeOutbox(t, db, conversation.ID)
	if strings.Contains(notice.Content, "那你帮我叫一下人吧") {
		t.Fatalf("expected semantic confirmation text to be excluded from notice summary, got %q", notice.Content)
	}
	stored := services.MessageService.Get(confirm.ID)
	if stored == nil || !strings.Contains(stored.Payload, `"handoffConfirmationDecision":"confirm"`) {
		t.Fatalf("expected consumed confirmation message to be marked, got %+v", stored)
	}
}

func TestConversationHandoffConfirmationSemanticCancelClearsPending(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	restore := services.SetHumanHandoffConfirmationClassifierForTest(func(ctx context.Context, conversation *models.Conversation, message *models.Message, reason string, text string) (string, float64, string) {
		return "cancel", 0.91, "客人表示先不用转人工"
	})
	t.Cleanup(restore)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")

	seedLegacyHandoffConfirmation(t, conversation.ID, aiAgent.ID, 10, "legacy-semantic-cancel", "客人需要人工接待")
	cancelMsg := createHumanDispatchMessage(t, db, conversation.ID, 20, enums.IMSenderTypeCustomer, "先不用了我自己看看")
	handled, err := services.ConversationHandoffConfirmationService.HandleCustomerMessage(&conversation, &cancelMsg)
	if err != nil {
		t.Fatalf("HandleCustomerMessage(cancel) error = %v", err)
	}
	if !handled {
		t.Fatalf("expected semantic cancel to be consumed")
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.PendingAction != "" || state.RouteStatus == enums.ConversationRouteStatusStoreWecomManual {
		t.Fatalf("expected pending action cleared without store manual route, got %+v", state)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 0 {
		t.Fatalf("expected no store room notice after cancel, got %d", count)
	}
	latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if latest == nil || latest.SenderType != enums.IMSenderTypeAI || !strings.Contains(latest.Content, "先不联系同事") {
		t.Fatalf("expected cancel acknowledgement, got %+v", latest)
	}
}

func TestConversationHandoffCollectsRoomThenDispatchesWithoutConfirmation(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "我落东西了在房间")

	if _, err := services.ConversationHandoffConfirmationService.RequestByAI(conversation.ID, aiAgent, "知识库规则要求门店同事接手；客户消息：我落东西了在房间", "req-ask-room"); err != nil {
		t.Fatalf("RequestByAI() error = %v", err)
	}
	latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if latest == nil || latest.Content != "方便说下是哪个房间吗？" {
		t.Fatalf("expected natural room question, got %+v", latest)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || !strings.Contains(state.PendingActionPayload, `"awaitingField":"room_number"`) {
		t.Fatalf("expected room-number pending context, got %+v", state)
	}

	room := createHumanDispatchMessage(t, db, conversation.ID, 20, enums.IMSenderTypeCustomer, "1305")
	handled, err := services.ConversationHandoffConfirmationService.HandleCustomerMessage(&conversation, &room)
	if err != nil || !handled {
		t.Fatalf("HandleCustomerMessage(room) handled=%v err=%v", handled, err)
	}
	latest = services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if latest == nil || latest.Content != services.DirectHandoffSuccessMessage {
		t.Fatalf("expected immediate handoff success after room number, got %+v", latest)
	}
	state = services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.PendingAction != "" || !strings.Contains(state.HandoffReason, "客户补充房号：1305") {
		t.Fatalf("expected collected room number to dispatch immediately, got %+v", state)
	}
	if strings.Contains(latest.Content, "确认") || strings.Contains(latest.Content, "取消") {
		t.Fatalf("room completion must not trigger a second confirmation, got %q", latest.Content)
	}
}

func TestConversationHandoffWithoutRoomContextDispatchesDirectly(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")

	if _, err := services.ConversationHandoffConfirmationService.RequestByAI(conversation.ID, aiAgent, "知识库规则要求门店同事接手；客户消息：问一下前台几点有人", "req-confirm-wording"); err != nil {
		t.Fatalf("RequestByAI() error = %v", err)
	}
	latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if latest == nil || latest.Content != services.DirectHandoffSuccessMessage {
		t.Fatalf("expected direct coworker handoff wording, got %+v", latest)
	}
}

func TestConversationHandoffWithRoomNumberDispatchesDirectly(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "1306房间的马桶堵了")

	if _, err := services.ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"知识库规则要求门店同事接手；客户消息：1306房间的马桶堵了",
		"req-room-ready",
		origin.ID,
	); err != nil {
		t.Fatalf("RequestByAIWithOriginMessage() error = %v", err)
	}

	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.PendingAction != "" || !strings.Contains(state.HandoffReason, "1306") {
		t.Fatalf("expected direct handoff with supplied room number, got %+v", state)
	}
	latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if latest == nil || latest.Content != services.DirectHandoffSuccessMessage {
		t.Fatalf("expected exact direct handoff success message, got %+v", latest)
	}
}

func TestConversationHandoffOffHoursDoesNotSendSuccessMessage(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeNone, "")
	if err := db.Model(&models.StoreStaffBinding{}).Where("id = ?", 55).Updates(map[string]any{
		"store_room_notify_enabled": false,
		"fallback_to_hq":            false,
	}).Error; err != nil {
		t.Fatalf("disable store room handoff: %v", err)
	}
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", 77).Updates(map[string]any{
		"store_room_notify_enabled": false,
		"fallback_to_hq":            false,
	}).Error; err != nil {
		t.Fatalf("disable instance room handoff: %v", err)
	}
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "帮我转人工")

	if _, err := services.ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(
		conversation.ID,
		aiAgent,
		"客户明确要求人工接待",
		"req-off-hours-direct",
		origin.ID,
	); err != nil {
		t.Fatalf("RequestByAIWithOriginMessage() error = %v", err)
	}

	if count := countHumanDispatchMessages(t, db, conversation.ID, services.DirectHandoffSuccessMessage); count != 0 {
		t.Fatalf("off-hours handoff must not claim success, got %d success messages", count)
	}
	latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if latest == nil || latest.Content != services.HandoffOffHoursMessage {
		t.Fatalf("expected exact off-hours response, got %+v", latest)
	}
	if count := countManualResumeTasks(t, db, conversation.ID); count != 0 {
		t.Fatalf("off-hours response must not create a manual resume task, got %d", count)
	}
}

func TestConversationHandoffCollectsRoomForInRoomCategories(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")

	for index, reason := range []string{
		"房内电视突然坏了",
		"麻烦送两条毛巾过来",
		"卫生间漏水了",
		"我把东西遗落在酒店了",
	} {
		conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
		if _, err := services.ConversationHandoffConfirmationService.RequestByAI(conversation.ID, aiAgent, "知识库规则要求门店同事接手；客户消息："+reason, fmt.Sprintf("req-room-category-%d", index)); err != nil {
			t.Fatalf("RequestByAI(%q) error = %v", reason, err)
		}
		latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
		if latest == nil || latest.Content != "方便说下是哪个房间吗？" {
			t.Fatalf("expected room question for %q, got %+v", reason, latest)
		}
	}
}

func TestConversationHandoffDoesNotCollectRoomForInformationQuestions(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")

	cases := []struct {
		question string
		reason   string
	}{
		{question: "有空调不", reason: "知识库规则要求门店同事接手：空调不制冷；客户消息：有空调不"},
		{question: "每个房间都有空调吗", reason: "知识库规则要求门店同事接手：空调噪音；客户消息：每个房间都有空调吗"},
		{question: "空调怎么开", reason: "知识库规则要求门店同事接手：空调故障；客户消息：空调怎么开"},
		{question: "电视怎么投屏", reason: "知识库规则要求门店同事接手：电视坏了；客户消息：电视怎么投屏"},
		{question: "房间里有浴巾吗", reason: "知识库规则要求门店同事接手：送浴巾；客户消息：房间里有浴巾吗"},
		{question: "小程序不能用", reason: "知识库规则要求门店同事接手；客户消息：小程序不能用"},
		{question: "电梯坏了", reason: "知识库规则要求门店同事接手；客户消息：电梯坏了"},
		{question: "停车场很吵", reason: "知识库规则要求门店同事接手；客户消息：停车场很吵"},
	}

	for index, item := range cases {
		conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
		createHumanDispatchStoreRoomRuntimeWithIDs(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59", int64(100+index), int64(200+index), int64(300+index))
		origin := createHumanDispatchMessage(t, db, conversation.ID, int64(index+1)*10, enums.IMSenderTypeCustomer, item.question)
		if _, err := services.ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(conversation.ID, aiAgent, item.reason, fmt.Sprintf("req-room-info-%d", index), origin.ID); err != nil {
			t.Fatalf("RequestByAIWithOriginMessage(%q) error = %v", item.question, err)
		}
		latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
		if latest == nil || latest.Content != services.DirectHandoffSuccessMessage {
			t.Fatalf("information question %q must dispatch without collecting room, got %+v", item.question, latest)
		}
		state := services.ConversationRouteService.GetByConversationID(conversation.ID)
		if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.PendingAction != "" {
			t.Fatalf("information question %q unexpectedly waits for room or confirmation: %+v", item.question, state)
		}
	}
}

func TestConversationHandoffRoomDecisionUsesVoiceTranscript(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "wx_protocol_1305.mp3")
	origin.MessageType = enums.IMMessageTypeVoice
	origin.Payload = `{"mediaText":"有空调不","mediaUnderstandingStatus":"understood"}`
	if err := db.Save(&origin).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := services.ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(conversation.ID, aiAgent, "知识库规则要求门店同事接手：空调不制冷；客户消息：有空调不", "req-room-voice", origin.ID); err != nil {
		t.Fatalf("RequestByAIWithOriginMessage() error = %v", err)
	}
	latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if latest == nil || latest.Content != services.DirectHandoffSuccessMessage {
		t.Fatalf("voice filename digits must not be treated as room number, got %+v", latest)
	}
}

func TestConversationHandoffConfirmationClearsOnNewTopic(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	restore := services.SetHumanHandoffConfirmationClassifierForTest(func(ctx context.Context, conversation *models.Conversation, message *models.Message, reason string, text string) (string, float64, string) {
		return "unknown", 0.9, "客人在问新问题"
	})
	t.Cleanup(restore)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")

	seedLegacyHandoffConfirmation(t, conversation.ID, aiAgent.ID, 10, "legacy-new-topic", "客人需要人工接待")
	newTopic := createHumanDispatchMessage(t, db, conversation.ID, 20, enums.IMSenderTypeCustomer, "早餐几点")
	handled, err := services.ConversationHandoffConfirmationService.HandleCustomerMessage(&conversation, &newTopic)
	if err != nil {
		t.Fatalf("HandleCustomerMessage(new topic) error = %v", err)
	}
	if handled {
		t.Fatalf("expected new topic not to be consumed as handoff confirmation")
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.PendingAction != "" {
		t.Fatalf("expected pending action to be cleared on new topic, got %+v", state)
	}
}

func TestManualSessionTimeoutClearsExpiredHandoffConfirmationPending(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)

	if err := services.ConversationRouteService.SetPendingAction(conversation.ID, enums.ConversationPendingActionHumanHandoff, `{"reason":"test"}`, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("SetPendingAction() error = %v", err)
	}
	if count := services.ManualSessionTimeoutService.ScanAndRestoreExpired(50); count != 1 {
		t.Fatalf("expected one expired pending action handled, got %d", count)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.PendingAction != "" || state.PendingActionExpireAt != nil {
		t.Fatalf("expected expired pending action cleared, got %+v", state)
	}
}

func TestManualSessionTimeoutPreparesHQPendingResumeBeforeSwitchingToAI(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "1")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "我要找人工处理")

	if _, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "用户要求人工"); err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusHQAgentDeskPending || state.ManualExpireAt == nil {
		t.Fatalf("expected HQ pending with timeout, got %+v", state)
	}
	setRouteManualExpireAt(t, db, conversation.ID, time.Now().Add(-time.Minute))
	if count := services.ManualSessionTimeoutService.ScanAndRestoreExpired(50); count != 1 {
		t.Fatalf("expected one expired HQ pending handled, got %d", count)
	}
	state = services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusHQAgentDeskPending || state.ManualExpireAt != nil || !state.NeedHumanFollowUp {
		t.Fatalf("expected HQ timeout to retain manual route until an AI reply is committed, got %+v", state)
	}
	current := services.ConversationService.Get(conversation.ID)
	if current == nil || current.Status != enums.IMConversationStatusPending {
		t.Fatalf("expected conversation shell to remain pending before the resume reply commits, got %+v", current)
	}
	task := latestManualResumeTask(t, db, conversation.ID)
	if task == nil || task.TaskStatus != "ready" {
		t.Fatalf("expected a ready resume task, got %+v", task)
	}
}

func TestManualSessionTimeoutHQServingUnansweredCustomerStartsResume(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusActive)
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Updates(map[string]any{
		"current_team_id":     int64(1),
		"current_assignee_id": int64(101),
	}).Error; err != nil {
		t.Fatalf("assign HQ conversation: %v", err)
	}
	if _, err := services.ConversationRouteService.EnterHQAgentDeskServing(conversation.ID, "HQ人工接待", time.Now()); err != nil {
		t.Fatalf("EnterHQAgentDeskServing() error = %v", err)
	}
	customer := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "这个问题还没处理")
	if err := services.ConversationRouteService.MarkCustomerMessage(conversation.ID, time.Now()); err != nil {
		t.Fatalf("MarkCustomerMessage() error = %v", err)
	}
	services.AIManualResumeTaskService.RecordWaitingCustomerMessage(conversation.ID, customer.ID)
	setRouteManualExpireAt(t, db, conversation.ID, time.Now().Add(-time.Minute))

	if count := services.ManualSessionTimeoutService.ScanAndRestoreExpired(50); count != 1 {
		t.Fatalf("expected one expired HQ serving route handled, got %d", count)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusHQAgentDeskServing || !state.NeedHumanFollowUp || state.ManualExpireAt != nil {
		t.Fatalf("expected HQ route held until a real resume reply commits, got %+v", state)
	}
	current := services.ConversationService.Get(conversation.ID)
	if current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID != 101 {
		t.Fatalf("expected assigned shell retained before resume commit, got %+v", current)
	}
	task := latestManualResumeTask(t, db, conversation.ID)
	if task == nil || task.TaskStatus != "ready" || task.LatestWaitingMessageID != customer.ID {
		t.Fatalf("expected ready resume task for latest customer message, got %+v", task)
	}
}

func TestManualSessionTimeoutHQServingAfterAgentReplyRestoresSilently(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusActive)
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Updates(map[string]any{
		"current_team_id":     int64(1),
		"current_assignee_id": int64(101),
	}).Error; err != nil {
		t.Fatalf("assign HQ conversation: %v", err)
	}
	if _, err := services.ConversationRouteService.EnterHQAgentDeskServing(conversation.ID, "HQ人工接待", time.Now()); err != nil {
		t.Fatalf("EnterHQAgentDeskServing() error = %v", err)
	}
	customer := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "这个问题还没处理")
	if err := services.ConversationRouteService.MarkCustomerMessage(conversation.ID, time.Now()); err != nil {
		t.Fatalf("MarkCustomerMessage() error = %v", err)
	}
	services.AIManualResumeTaskService.RecordWaitingCustomerMessage(conversation.ID, customer.ID)
	operator := &dto.AuthPrincipal{UserID: 101, Username: "hq-agent", Nickname: "总部同事"}
	agent, err := services.MessageService.SendAgentMessageWithRequestID(
		conversation.ID,
		101,
		"hq-agent-final-reply",
		enums.IMMessageTypeText,
		"已经处理好了",
		"",
		operator,
		"req-hq-agent-final-reply",
	)
	if err != nil {
		t.Fatalf("SendAgentMessageWithRequestID() error = %v", err)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.NeedHumanFollowUp || state.ManualExpireAt == nil {
		t.Fatalf("expected agent reply to clear follow-up inside the message transaction, got %+v", state)
	}
	task := latestManualResumeTask(t, db, conversation.ID)
	if task == nil || task.TaskStatus != "cancelled" {
		t.Fatalf("expected agent reply to cancel the pending resume task, got %+v", task)
	}
	setRouteManualExpireAt(t, db, conversation.ID, time.Now().Add(-time.Minute))

	if count := services.ManualSessionTimeoutService.ScanAndRestoreExpired(50); count != 1 {
		t.Fatalf("expected one expired HQ serving route handled, got %d", count)
	}
	state = services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusAIServing || state.NeedHumanFollowUp || state.ManualExpireAt != nil {
		t.Fatalf("expected HQ route restored after the employee reply, got %+v", state)
	}
	current := services.ConversationService.Get(conversation.ID)
	if current == nil || current.Status != enums.IMConversationStatusAIServing || current.CurrentAssigneeID != 0 || current.CurrentTeamID != 0 {
		t.Fatalf("expected conversation shell restored atomically, got %+v", current)
	}
	latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("seq_no").Desc("id"))
	if latest == nil || latest.ID != agent.ID || latest.SenderType != enums.IMSenderTypeAgent {
		t.Fatalf("expected no AI restore notice after the employee's final reply, got %+v", latest)
	}
}

func TestManualSessionTimeoutPreparesStoreManualOrdinaryResume(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "这个问题需要人工处理")

	if _, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "客人需要人工接待"); err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.ManualExpireAt == nil {
		t.Fatalf("expected store manual with timeout, got %+v", state)
	}
	if state.ManualExpireAt.After(time.Now().Add(services.DefaultStoreWecomManualMinutes*time.Minute + 2*time.Second)) {
		t.Fatalf("expected ordinary store manual timeout around 5 minutes, got %v", state.ManualExpireAt)
	}
	setRouteManualExpireAt(t, db, conversation.ID, time.Now().Add(-time.Minute))
	if count := services.ManualSessionTimeoutService.ScanAndRestoreExpired(50); count != 1 {
		t.Fatalf("expected one expired store manual handled, got %d", count)
	}
	state = services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.ManualExpireAt != nil || !state.NeedHumanFollowUp {
		t.Fatalf("expected ordinary store timeout to retain manual route until an AI reply is committed, got %+v", state)
	}
	task := latestManualResumeTask(t, db, conversation.ID)
	if task == nil || task.TaskStatus != "ready" {
		t.Fatalf("expected a ready resume task, got %+v", task)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 2 {
		t.Fatalf("expected initial and final store room notices, got %d", count)
	}
}

func TestManualSessionTimeoutStoreSafetyRemindsTwiceThenPreparesResume(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "我摔倒流血了")

	if _, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "客人摔倒流血，需要尽快处理"); err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	if _, err := services.AIManualResumeTaskService.Schedule(conversation.ID, origin.ID, "safety-reminder-test"); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.ManualExpireAt == nil {
		t.Fatalf("expected safety store manual with timeout, got %+v", state)
	}
	if state.ManualExpireAt.After(time.Now().Add(services.DefaultStoreWecomManualMinutes*time.Minute + 2*time.Second)) {
		t.Fatalf("expected safety store manual final timeout around 5 minutes, got %v", state.ManualExpireAt)
	}

	setManualResumeNextReminderAt(t, db, conversation.ID, time.Now().Add(-time.Second))
	if count := services.AIManualResumeTaskService.ProcessDue(50); count != 1 {
		t.Fatalf("expected first safety reminder handled, got %d", count)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 2 {
		t.Fatalf("expected initial notice and first safety reminder, got %d", count)
	}

	setManualResumeNextReminderAt(t, db, conversation.ID, time.Now().Add(-time.Second))
	if count := services.AIManualResumeTaskService.ProcessDue(50); count != 1 {
		t.Fatalf("expected second safety reminder handled, got %d", count)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 3 {
		t.Fatalf("expected initial notice and two safety reminders, got %d", count)
	}

	setRouteManualExpireAt(t, db, conversation.ID, time.Now().Add(-time.Minute))
	if count := services.ManualSessionTimeoutService.ScanAndRestoreExpired(50); count != 1 {
		t.Fatalf("expected final safety timeout to prepare AI resume, got %d", count)
	}
	state = services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || !state.NeedHumanFollowUp || state.ManualExpireAt != nil {
		t.Fatalf("expected safety route and red dot to remain until AI commits a temporary answer, got %+v", state)
	}
	task := latestManualResumeTask(t, db, conversation.ID)
	if task == nil || task.TaskStatus != "ready" || task.ReminderCount != 2 {
		t.Fatalf("expected ready safety resume task after two reminders, got %+v", task)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 4 {
		t.Fatalf("expected initial, two reminders, and final temporary-resume notice, got %d", count)
	}
}

func TestStoreManualAgentReplyStartsIdleTimeout(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	createHumanDispatchAgentProfile(t, db, 101, 0, enums.ServiceStatusIdle, 3, false, enums.StatusOk)

	if _, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "客人需要人工接待"); err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: 101, Username: "store-staff", Nickname: "门店同事"}
	if _, err := services.MessageService.SendAgentMessageWithRequestID(conversation.ID, 0, "store-manual-reply-timeout", enums.IMMessageTypeText, "我来处理。", "", operator, "req-store-reply"); err != nil {
		t.Fatalf("SendAgentMessageWithRequestID() error = %v", err)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.NeedHumanFollowUp || state.ManualExpireAt == nil {
		t.Fatalf("expected store manual agent reply to clear follow-up and start idle timeout, got %+v", state)
	}
	if state.ManualExpireAt.After(time.Now().Add(services.DefaultManualTimeoutMinutes*time.Minute + 2*time.Second)) {
		t.Fatalf("expected store manual idle timeout around 10 minutes, got %v", state.ManualExpireAt)
	}
}

func TestManualSessionTimeoutRestoresStoreManualAfterAgentReplySilently(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	createHumanDispatchAgentProfile(t, db, 101, 0, enums.ServiceStatusIdle, 3, false, enums.StatusOk)

	if _, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "客人需要人工接待"); err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: 101, Username: "store-staff", Nickname: "门店同事"}
	agent, err := services.MessageService.SendAgentMessageWithRequestID(conversation.ID, 0, "store-manual-reply-timeout-notice", enums.IMMessageTypeText, "我来处理。", "", operator, "req-store-reply-notice")
	if err != nil {
		t.Fatalf("SendAgentMessageWithRequestID() error = %v", err)
	}
	setRouteManualExpireAt(t, db, conversation.ID, time.Now().Add(-time.Minute))
	if count := services.ManualSessionTimeoutService.ScanAndRestoreExpired(50); count != 1 {
		t.Fatalf("expected one expired store manual idle session handled, got %d", count)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusAIServing || state.NeedHumanFollowUp || state.ManualExpireAt != nil {
		t.Fatalf("expected store manual idle timeout to restore AI route, got %+v", state)
	}
	latest := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if latest == nil || latest.ID != agent.ID || latest.SenderType != enums.IMSenderTypeAgent {
		t.Fatalf("expected silent AI restore after the employee's final reply, got %+v", latest)
	}
}

func TestManualSessionTimeoutRequeuesLatestCustomerMessageAfterManualRoute(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	if err := db.Create(&models.Channel{
		ID: conversation.ChannelID, Name: "企微员工号", ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID: "manual-resume-requeue", AIAgentID: aiAgent.ID, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create protocol channel: %v", err)
	}
	origin := createHumanDispatchMessage(t, db, conversation.ID, 70, enums.IMSenderTypeCustomer, "水龙头怎么用")

	if _, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "客人需要人工接待"); err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	if _, err := services.AIManualResumeTaskService.Schedule(conversation.ID, origin.ID, "waiting-messages-test"); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	waiting := createHumanDispatchMessage(t, db, conversation.ID, 80, enums.IMSenderTypeCustomer, "投影能用吗")
	services.AIManualResumeTaskService.RecordWaitingCustomerMessage(conversation.ID, waiting.ID)

	previousHook := services.TriggerAIReplySyncHook
	defer func() { services.TriggerAIReplySyncHook = previousHook }()
	triggeredMessageID := int64(0)
	triggeredContent := ""
	services.TriggerAIReplySyncHook = func(_ context.Context, conversation models.Conversation, message models.Message) error {
		triggeredMessageID = message.ID
		triggeredContent = message.Content
		now := time.Now()
		reply := models.Message{
			ConversationID: conversation.ID,
			SessionNo:      1,
			RequestID:      message.RequestID,
			ClientMsgID:    "resume-test-reply",
			SenderType:     enums.IMSenderTypeAI,
			SenderID:       aiAgent.ID,
			MessageType:    enums.IMMessageTypeText,
			Content:        "我继续帮你处理电视问题。",
			SeqNo:          waiting.SeqNo + 1,
			SendStatus:     enums.IMMessageStatusSent,
			SentAt:         &now,
			AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}
		if err := db.Create(&reply).Error; err != nil {
			return err
		}
		if err := db.Create(&models.ChannelMessageOutbox{
			ChannelType: enums.ChannelTypeWxWorkProtocol, ConversationID: conversation.ID, MessageID: reply.ID,
			Payload: `{}`, SendStatus: string(enums.ChannelMessageOutboxStatusSent), SentAt: &now,
		}).Error; err != nil {
			return err
		}
		createHumanDispatchCompletedResumeRunLog(t, db, reply, message.RequestID, waiting.ID)
		return nil
	}

	setRouteManualExpireAt(t, db, conversation.ID, time.Now().Add(-time.Minute))
	if count := services.ManualSessionTimeoutService.ScanAndRestoreExpired(50); count != 1 {
		t.Fatalf("expected one expired store manual route, got %d", count)
	}
	if count := services.AIManualResumeTaskService.ProcessDue(10); count != 1 {
		t.Fatalf("expected one AI manual resume task processed, got %d", count)
	}
	if triggeredMessageID != waiting.ID {
		t.Fatalf("expected latest waiting customer message %d to resume AI, got %d", waiting.ID, triggeredMessageID)
	}
	if !strings.Contains(triggeredContent, "水龙头怎么用") || !strings.Contains(triggeredContent, "投影能用吗") {
		t.Fatalf("expected all unresolved waiting messages in resume input, got %q", triggeredContent)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusAIServing {
		var resumeTasks []models.AIManualResumeTask
		if err := db.Where("conversation_id = ?", conversation.ID).Order("id DESC").Find(&resumeTasks).Error; err != nil {
			t.Fatalf("inspect failed resume task: %v", err)
		}
		var replies []models.Message
		var runLogs []models.AgentRunLog
		var outboxes []models.ChannelMessageOutbox
		_ = db.Where("conversation_id = ? AND sender_type = ?", conversation.ID, enums.IMSenderTypeAI).Find(&replies).Error
		_ = db.Where("conversation_id = ?", conversation.ID).Find(&runLogs).Error
		_ = db.Where("conversation_id = ?", conversation.ID).Find(&outboxes).Error
		t.Fatalf("expected route restored to AI after requeue, got state=%+v tasks=%+v replies=%+v runLogs=%+v outboxes=%+v", state, resumeTasks, replies, runLogs, outboxes)
	}
}

func TestConversationHumanDispatchStoreManualAllowsWebReplyWithoutClaim(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	createHumanDispatchAgentProfile(t, db, 101, 0, enums.ServiceStatusIdle, 3, false, enums.StatusOk)

	result, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "客人需要人工接待")
	if err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	if result == nil || result.Decision != services.HandoffDecisionStoreWecom {
		t.Fatalf("expected store_wecom decision, got %+v", result)
	}

	operator := &dto.AuthPrincipal{UserID: 101, Username: "store-staff", Nickname: "门店同事"}
	if err := services.ConversationService.EnsureAgentCanReply(conversation.ID, "门店群跟进后网页端回复", operator); err != nil {
		t.Fatalf("EnsureAgentCanReply() error = %v", err)
	}
	message, err := services.MessageService.SendAgentMessageWithRequestID(conversation.ID, 0, "store-manual-reply-1", enums.IMMessageTypeText, "我来跟进处理。", "", operator, "req-store-manual-reply")
	if err != nil {
		t.Fatalf("SendAgentMessageWithRequestID() error = %v", err)
	}
	if message == nil || message.SenderType != enums.IMSenderTypeAgent {
		t.Fatalf("expected agent message, got %+v", message)
	}
	current := services.ConversationService.Get(conversation.ID)
	if current.Status == enums.IMConversationStatusPending || current.CurrentAssigneeID != 0 {
		t.Fatalf("store manual web reply should not require claim or move into pending pool, got %+v", current)
	}
}

func TestConversationHumanDispatchSemiManagedWithoutHoursEnqueuesStoreRoomNotice(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "")

	result, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "半托管门店人工跟进")
	if err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	if result == nil || result.Decision != services.HandoffDecisionStoreWecom {
		t.Fatalf("expected store_wecom decision for semi-managed store without hours, got %+v", result)
	}
	notice := findStoreRoomHandoffNoticeOutbox(t, db, conversation.ID)
	if notice.Kind != "store_room_handoff_notice" || notice.RoomConversationID != "R:room-100" {
		t.Fatalf("unexpected store room notice payload: %#v", notice)
	}
}

func TestConversationHumanDispatchNoneManagedEnqueuesStoreRoomNoticeWithoutSchedule(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeNone, "")

	result, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "非托管门店人工跟进")
	if err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	if result == nil || result.Decision != services.HandoffDecisionStoreWecom {
		t.Fatalf("expected store_wecom decision for non-managed store, got %+v", result)
	}
	notice := findStoreRoomHandoffNoticeOutbox(t, db, conversation.ID)
	if notice.Kind != "store_room_handoff_notice" || notice.WxWorkInstanceID != 77 || notice.RoomConversationID != "R:room-100" {
		t.Fatalf("unexpected store room notice payload: %#v", notice)
	}
}

func TestConversationHumanDispatchAIHandoffFallsBackToFirstScheduledTeam(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "3,1,2")
	createHumanDispatchTeam(t, db, 1, "售后支持组")
	createHumanDispatchTeam(t, db, 2, "VIP支持组")
	createHumanDispatchTeam(t, db, 3, "非值班组")
	createHumanDispatchActiveSchedule(t, db, 1)
	createHumanDispatchActiveSchedule(t, db, 2)
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)

	result, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "用户要求转人工")
	if err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	if result == nil || result.Decision != services.HandoffDecisionStoreWecom {
		t.Fatalf("expected store_wecom decision, got %+v", result)
	}

	current := services.ConversationService.Get(conversation.ID)
	if current.Status != enums.IMConversationStatusAIServing {
		t.Fatalf("expected store manual handoff to keep ai-serving conversation shell, got status=%d", current.Status)
	}
	if current.CurrentTeamID != 0 || current.CurrentAssigneeID != 0 {
		t.Fatalf("expected store manual with no assignee, got team=%d assignee=%d", current.CurrentTeamID, current.CurrentAssigneeID)
	}
	route := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if route == nil || route.RouteStatus != enums.ConversationRouteStatusStoreWecomManual {
		t.Fatalf("expected store manual route, got %+v", route)
	}
}

func TestConversationHumanDispatchHumanOnlyCreateOffHoursUsesGlobalPendingPool(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeHumanOnly, "1")

	conversation, err := services.ConversationService.Create(openidentity.ExternalUser{
		ExternalSource: enums.ExternalSourceGuest,
		ExternalID:     "guest-human-only-off-hours",
		ExternalName:   "非服务时间访客",
	}, 1, aiAgent.ID)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if conversation.Status != enums.IMConversationStatusPending {
		t.Fatalf("expected pending conversation, got status=%d", conversation.Status)
	}
	if conversation.CurrentTeamID != 0 || conversation.CurrentAssigneeID != 0 {
		t.Fatalf("expected global pending pool, got team=%d assignee=%d", conversation.CurrentTeamID, conversation.CurrentAssigneeID)
	}

	message := services.MessageService.FindOne(sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if message == nil || message.Content != services.HandoffWaitingMessage {
		t.Fatalf("expected waiting message, got %+v", message)
	}
}

func TestConversationHumanDispatchHumanOnlyCreateAssignsAvailableAgent(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeHumanOnly, "1")
	createHumanDispatchTeam(t, db, 1, "售后支持组")
	createHumanDispatchActiveSchedule(t, db, 1)
	createHumanDispatchAgentProfile(t, db, 101, 1, enums.ServiceStatusIdle, 3, true, enums.StatusOk)

	conversation, err := services.ConversationService.Create(openidentity.ExternalUser{
		ExternalSource: enums.ExternalSourceGuest,
		ExternalID:     "guest-human-only-assigned",
		ExternalName:   "服务时间访客",
	}, 1, aiAgent.ID)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if conversation.Status != enums.IMConversationStatusActive {
		t.Fatalf("expected active conversation, got status=%d", conversation.Status)
	}
	if conversation.CurrentAssigneeID != 101 || conversation.CurrentTeamID != 1 {
		t.Fatalf("unexpected assignment: assignee=%d team=%d", conversation.CurrentAssigneeID, conversation.CurrentTeamID)
	}
}

func TestConversationAutoAssignManualDispatchOffHoursReturnsBusinessMessage(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "1")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusPending)

	err := services.ConversationService.AutoAssignConversation(conversation.ID, testHumanDispatchOperator())
	if err == nil {
		t.Fatalf("expected off-hours manual dispatch to fail")
	}
	if !strings.Contains(err.Error(), "当前暂不在人工客服服务时间内") {
		t.Fatalf("expected off-hours error, got %v", err)
	}
}

func TestConversationAutoAssignManualDispatchFallsBackToTeamPool(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "1")
	createHumanDispatchTeam(t, db, 1, "售后支持组")
	createHumanDispatchActiveSchedule(t, db, 1)
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusPending)

	err := services.ConversationService.AutoAssignConversation(conversation.ID, testHumanDispatchOperator())
	if err != nil {
		t.Fatalf("AutoAssignConversation() error = %v", err)
	}
	current := services.ConversationService.Get(conversation.ID)
	if current.Status != enums.IMConversationStatusPending || current.CurrentTeamID != 1 || current.CurrentAssigneeID != 0 {
		t.Fatalf("expected team-pool pending conversation, got %+v", current)
	}
}

func setupConversationHumanDispatchTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open sqlite error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(
		&models.User{},
		&models.Customer{},
		&models.CustomerIdentity{},
		&models.WxWorkCustomerHandoffSetting{},
		&models.Channel{},
		&models.AIAgent{},
		&models.AgentTeam{},
		&models.AgentTeamSchedule{},
		&models.AgentProfile{},
		&models.AIConfig{},
		&models.Store{},
		&models.StoreStaffBinding{},
		&models.WxWorkProtocolInstance{},
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.AIManualResumeTask{},
		&models.ConversationParticipant{},
		&models.ConversationAssignment{},
		&models.ConversationEventLog{},
		&models.ConversationReadState{},
		&models.AgentRunLog{},
		&models.Message{},
		&models.ChannelMessageOutbox{},
		&models.MessageSyncLog{},
		&models.Notification{},
	); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	return db
}

func createHumanDispatchCompletedResumeRunLog(t *testing.T, db *gorm.DB, reply models.Message, requestID string, sourceMessageID int64) {
	t.Helper()
	taskID := "task-1"
	trace, err := json.Marshal(map[string]any{
		"status":         "runtime_prepared",
		"replySent":      true,
		"replyMessageId": reply.ID,
		"runtime": map[string]any{
			"status": "completed",
			"input": map[string]any{
				"currentTurnSources": []map[string]any{{
					"ref":         "U1",
					"messageId":   sourceMessageID,
					"messageType": string(enums.IMMessageTypeText),
					"text":        "等待人工期间的客户问题",
				}},
			},
			"pipeline": map[string]any{
				"replyPlan": map[string]any{
					"taskPlans": []map[string]any{{
						"taskId":             taskID,
						"intent":             "hotel_info",
						"subIntent":          "manual_resume_test",
						"objective":          "information",
						"relationToPrevious": "independent",
						"resolutionState":    "clear",
						"originalText":       "等待人工期间的客户问题",
						"resolvedText":       "等待人工期间的客户问题",
						"sourceRefs":         []string{"U1"},
						"outputKind":         "text",
						"replyRequired":      true,
						"output":             "knowledge_text_reply",
					}},
				},
			},
			"output": map[string]any{
				"finishReason": "committed_reply",
				"commitMessages": []map[string]any{{
					"messageId":   reply.ID,
					"messageType": string(reply.MessageType),
					"content":     reply.Content,
					"taskIds":     []string{taskID},
					"status":      "sent",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal completed manual resume trace: %v", err)
	}
	if err := db.Create(&models.AgentRunLog{
		ConversationID: reply.ConversationID,
		MessageID:      sourceMessageID,
		RequestID:      requestID,
		FinalAction:    "reply",
		FinalStatus:    "completed",
		ReplyText:      reply.Content,
		TraceData:      string(trace),
		CreatedAt:      time.Now(),
	}).Error; err != nil {
		t.Fatalf("create completed manual resume run log: %v", err)
	}
}

func createHumanDispatchAIAgent(t *testing.T, db *gorm.DB, mode enums.IMConversationServiceMode, teamIDs string) models.AIAgent {
	t.Helper()
	item := models.AIAgent{
		Name:        "测试AI",
		ServiceMode: mode,
		TeamIDs:     teamIDs,
		Status:      enums.StatusOk,
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("create ai agent error = %v", err)
	}
	return item
}

func createHumanDispatchWxWorkProtocolChannel(t *testing.T, db *gorm.DB, id int64) {
	t.Helper()
	now := time.Now()
	if err := db.Create(&models.Channel{
		ID:          id,
		Name:        "企微员工号测试渠道",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-protocol-handoff-" + strconv.FormatInt(id, 10),
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create wxwork protocol channel: %v", err)
	}
}

func createHumanDispatchTeam(t *testing.T, db *gorm.DB, id int64, name string) {
	t.Helper()
	if err := db.Create(&models.AgentTeam{ID: id, Name: name, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create team error = %v", err)
	}
}

func createHumanDispatchActiveSchedule(t *testing.T, db *gorm.DB, teamID int64) {
	t.Helper()
	now := time.Now()
	if err := db.Create(&models.AgentTeamSchedule{
		TeamID:  teamID,
		StartAt: now.Add(-time.Hour),
		EndAt:   now.Add(time.Hour),
		Status:  enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create schedule error = %v", err)
	}
}

func createHumanDispatchAgentProfile(t *testing.T, db *gorm.DB, userID, teamID int64, serviceStatus enums.ServiceStatus, maxConcurrent int, autoAssign bool, status enums.Status) {
	t.Helper()
	if err := db.Create(&models.User{
		ID:       userID,
		Username: "agent",
		Nickname: "客服",
		Status:   enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create user error = %v", err)
	}
	if err := db.Create(&models.AgentProfile{
		UserID:             userID,
		TeamID:             teamID,
		AgentCode:          "A001",
		DisplayName:        "客服",
		ServiceStatus:      serviceStatus,
		MaxConcurrentCount: maxConcurrent,
		AutoAssignEnabled:  autoAssign,
		Status:             status,
	}).Error; err != nil {
		t.Fatalf("create profile error = %v", err)
	}
}

func createHumanDispatchConversation(t *testing.T, db *gorm.DB, aiAgentID int64, status enums.IMConversationStatus) models.Conversation {
	t.Helper()
	now := time.Now()
	item := models.Conversation{
		AIAgentID:     aiAgentID,
		ChannelID:     1,
		CustomerID:    1,
		CustomerName:  "测试访客",
		Status:        status,
		ServiceMode:   enums.IMConversationServiceModeAIFirst,
		LastMessageAt: now,
		LastActiveAt:  now,
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("create conversation error = %v", err)
	}
	return item
}

func createHumanDispatchMessage(t *testing.T, db *gorm.DB, conversationID int64, seqNo int64, senderType enums.IMSenderType, content string) models.Message {
	t.Helper()
	now := time.Now().Add(time.Duration(seqNo) * time.Millisecond)
	item := models.Message{
		ConversationID: conversationID,
		SessionNo:      1,
		RequestID:      "req-test",
		ClientMsgID:    "msg-test-" + strconv.FormatInt(conversationID, 10) + "-" + strconv.FormatInt(seqNo, 10),
		SenderType:     senderType,
		MessageType:    enums.IMMessageTypeText,
		Content:        content,
		SeqNo:          seqNo,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields: models.AuditFields{
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("create message error = %v", err)
	}
	return item
}

func backdateHumanDispatchMessage(t *testing.T, db *gorm.DB, messageID int64, sentAt time.Time) {
	t.Helper()
	if err := db.Model(&models.Message{}).Where("id = ?", messageID).Updates(map[string]any{
		"sent_at":    sentAt,
		"created_at": sentAt,
		"updated_at": sentAt,
	}).Error; err != nil {
		t.Fatalf("backdate message: %v", err)
	}
}

func setHumanDispatchConversationSummary(t *testing.T, db *gorm.DB, conversationID int64, summary string) {
	t.Helper()
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversationID).Update("last_message_summary", summary).Error; err != nil {
		t.Fatalf("update conversation summary error = %v", err)
	}
}

func createHumanDispatchStoreRoomRuntime(t *testing.T, db *gorm.DB, conversationID int64, managedMode string, serviceHours string) {
	createHumanDispatchStoreRoomRuntimeWithIDs(t, db, conversationID, managedMode, serviceHours, 88, 55, 77)
}

func createHumanDispatchStoreRoomRuntimeWithIDs(t *testing.T, db *gorm.DB, conversationID int64, managedMode string, serviceHours string, storeID int64, bindingID int64, instanceID int64) {
	t.Helper()
	if err := db.Create(&models.Store{ID: storeID, StoreCode: "store-room-test-" + strconv.FormatInt(storeID, 10), Name: "测试门店", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create store error = %v", err)
	}
	binding := models.StoreStaffBinding{
		ID:                      bindingID,
		StoreID:                 storeID,
		ManagedMode:             managedMode,
		ServiceHours:            serviceHours,
		StoreRoomConversationID: "R:room-100",
		StoreRoomNotifyEnabled:  true,
		StoreRoomAtList:         "staff-1,staff-2",
		FallbackToHQ:            true,
		ManualTimeoutMinutes:    10,
		Status:                  enums.StatusOk,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create store staff binding error = %v", err)
	}
	instance := models.WxWorkProtocolInstance{
		ID:                     instanceID,
		Guid:                   "guid-store-room-test-" + strconv.FormatInt(instanceID, 10),
		StoreID:                binding.StoreID,
		StoreStaffBindingID:    binding.ID,
		StoreNavigationName:    "测试门店",
		Status:                 enums.StatusOk,
		HealthStatus:           "online",
		FallbackToHQ:           true,
		ManualTimeoutMinutes:   10,
		StoreRoomNotifyEnabled: true,
		AIReplyEnabled:         true,
	}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatalf("create wxwork protocol instance error = %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{ConversationID: conversationID, StoreID: binding.StoreID, WxWorkInstanceID: instance.ID, RouteStatus: enums.ConversationRouteStatusAIServing, RouteTarget: "ai", SessionNo: 1}).Error; err != nil {
		t.Fatalf("create conversation route state error = %v", err)
	}
}

func seedLegacyHandoffConfirmation(t *testing.T, conversationID int64, aiAgentID int64, originMessageID int64, handoffToken string, reason string) {
	t.Helper()
	payload := fmt.Sprintf(
		`{"reason":%q,"aiAgentId":%d,"originMessageId":%d,"handoffToken":%q,"createdAt":%q}`,
		reason,
		aiAgentID,
		originMessageID,
		handoffToken,
		time.Now().Format(time.RFC3339),
	)
	if err := services.ConversationRouteService.SetPendingAction(
		conversationID,
		enums.ConversationPendingActionHumanHandoff,
		payload,
		time.Now().Add(services.DefaultHandoffConfirmationMinutes*time.Minute),
	); err != nil {
		t.Fatalf("seed legacy handoff confirmation: %v", err)
	}
}

func countHumanDispatchMessages(t *testing.T, db *gorm.DB, conversationID int64, content string) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&models.Message{}).
		Where("conversation_id = ? AND sender_type = ? AND content = ?", conversationID, enums.IMSenderTypeAI, content).
		Count(&count).Error; err != nil {
		t.Fatalf("count human dispatch messages: %v", err)
	}
	return count
}

func countManualResumeTasks(t *testing.T, db *gorm.DB, conversationID int64) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&models.AIManualResumeTask{}).Where("conversation_id = ?", conversationID).Count(&count).Error; err != nil {
		t.Fatalf("count manual resume tasks: %v", err)
	}
	return count
}

func countMessagesByClientMsgID(t *testing.T, db *gorm.DB, conversationID int64, clientMsgID string) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&models.Message{}).
		Where("conversation_id = ? AND client_msg_id = ?", conversationID, clientMsgID).
		Count(&count).Error; err != nil {
		t.Fatalf("count messages by client message id: %v", err)
	}
	return count
}

func countChannelOutboxesForMessage(t *testing.T, db *gorm.DB, channelType string, messageID int64) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&models.ChannelMessageOutbox{}).
		Where("channel_type = ? AND message_id = ?", channelType, messageID).
		Count(&count).Error; err != nil {
		t.Fatalf("count channel outboxes for message: %v", err)
	}
	return count
}

func countManualHandoffNotifications(t *testing.T, db *gorm.DB, conversationID int64, recipientUserID int64) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&models.Notification{}).Where(
		"recipient_user_id = ? AND notification_type = ? AND biz_type = ? AND biz_id = ?",
		recipientUserID,
		"manual_handoff_created",
		"conversation",
		conversationID,
	).Count(&count).Error; err != nil {
		t.Fatalf("count manual handoff notifications: %v", err)
	}
	return count
}

type storeRoomHandoffNoticePayload struct {
	Kind               string   `json:"kind"`
	ConversationID     int64    `json:"conversationId"`
	WxWorkInstanceID   int64    `json:"wxWorkInstanceId"`
	RoomConversationID string   `json:"roomConversationId"`
	Content            string   `json:"content"`
	AtList             []string `json:"atList"`
}

func findStoreRoomHandoffNoticeOutbox(t *testing.T, db *gorm.DB, conversationID int64) storeRoomHandoffNoticePayload {
	t.Helper()
	var outboxes []models.ChannelMessageOutbox
	if err := db.Where("conversation_id = ? AND channel_type = ?", conversationID, enums.ChannelTypeWxWorkProtocol).Order("id ASC").Find(&outboxes).Error; err != nil {
		t.Fatalf("find outboxes error = %v", err)
	}
	for _, outbox := range outboxes {
		var payload storeRoomHandoffNoticePayload
		if err := json.Unmarshal([]byte(outbox.Payload), &payload); err != nil {
			continue
		}
		if payload.Kind == "store_room_handoff_notice" {
			return payload
		}
	}
	t.Fatalf("expected store room handoff notice outbox, got %#v", outboxes)
	return storeRoomHandoffNoticePayload{}
}

func findStoreRoomHandoffNoticeOutboxModel(t *testing.T, db *gorm.DB, conversationID int64) models.ChannelMessageOutbox {
	t.Helper()
	var outboxes []models.ChannelMessageOutbox
	if err := db.Where("conversation_id = ? AND channel_type = ?", conversationID, enums.ChannelTypeWxWorkProtocol).Order("id ASC").Find(&outboxes).Error; err != nil {
		t.Fatalf("find outboxes error = %v", err)
	}
	for _, outbox := range outboxes {
		var payload storeRoomHandoffNoticePayload
		if err := json.Unmarshal([]byte(outbox.Payload), &payload); err == nil && payload.Kind == "store_room_handoff_notice" {
			return outbox
		}
	}
	t.Fatalf("expected store room handoff notice outbox, got %#v", outboxes)
	return models.ChannelMessageOutbox{}
}

func countStoreRoomHandoffNoticeOutbox(t *testing.T, db *gorm.DB, conversationID int64) int {
	t.Helper()
	var outboxes []models.ChannelMessageOutbox
	if err := db.Where("conversation_id = ? AND channel_type = ?", conversationID, enums.ChannelTypeWxWorkProtocol).Order("id ASC").Find(&outboxes).Error; err != nil {
		t.Fatalf("find outboxes error = %v", err)
	}
	count := 0
	for _, outbox := range outboxes {
		var payload storeRoomHandoffNoticePayload
		if err := json.Unmarshal([]byte(outbox.Payload), &payload); err == nil && payload.Kind == "store_room_handoff_notice" {
			count++
		}
	}
	return count
}

func setRouteManualExpireAt(t *testing.T, db *gorm.DB, conversationID int64, expireAt time.Time) {
	t.Helper()
	if err := db.Model(&models.ConversationRouteState{}).
		Where("conversation_id = ?", conversationID).
		Update("manual_expire_at", expireAt).Error; err != nil {
		t.Fatalf("update route manual expire at error = %v", err)
	}
}

func setManualResumeNextReminderAt(t *testing.T, db *gorm.DB, conversationID int64, reminderAt time.Time) {
	t.Helper()
	if err := db.Model(&models.AIManualResumeTask{}).
		Where("conversation_id = ? AND task_status = ?", conversationID, "waiting").
		Update("next_reminder_at", reminderAt).Error; err != nil {
		t.Fatalf("update manual resume next reminder at error = %v", err)
	}
}

func latestManualResumeTask(t *testing.T, db *gorm.DB, conversationID int64) *models.AIManualResumeTask {
	t.Helper()
	item := &models.AIManualResumeTask{}
	if err := db.Where("conversation_id = ?", conversationID).Order("id DESC").First(item).Error; err != nil {
		t.Fatalf("find manual resume task error = %v", err)
	}
	return item
}

func testHumanDispatchOperator() *dto.AuthPrincipal {
	return &dto.AuthPrincipal{UserID: 9, Username: "dispatcher", Nickname: "调度员"}
}
