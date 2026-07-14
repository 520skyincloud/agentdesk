package services_test

import (
	"context"
	"encoding/json"
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
	for _, want := range []string{"有客人需要人工接待", "客户：测试访客", "摘要：客人要求人工协助处理入住问题", "原因：用户明确要求人工", "后台：/dashboard/conversations?conversationId="} {
		if !strings.Contains(notice.Content, want) {
			t.Fatalf("expected notice content to contain %q, got %q", want, notice.Content)
		}
	}
	for _, forbidden := range []string{"门店：", "会话ID：", "human_complaint_risk", "model IntentDetect JSON"} {
		if strings.Contains(notice.Content, forbidden) {
			t.Fatalf("expected notice content not to contain %q, got %q", forbidden, notice.Content)
		}
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

func TestConversationHandoffConfirmationExecutesOnce(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")

	handled, err := services.ConversationHandoffConfirmationService.RequestByAI(conversation.ID, aiAgent, "客人需要人工接待；客户消息：我要找人", "req-ask")
	if err != nil {
		t.Fatalf("RequestByAI() error = %v", err)
	}
	if !handled {
		t.Fatalf("expected confirmation prompt to be handled")
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.PendingAction != string(enums.ConversationPendingActionHumanHandoff) {
		t.Fatalf("expected pending human handoff action, got %+v", state)
	}
	confirm := createHumanDispatchMessage(t, db, conversation.ID, 20, enums.IMSenderTypeCustomer, "确认")
	handled, err = services.ConversationHandoffConfirmationService.HandleCustomerMessage(&conversation, &confirm)
	if err != nil {
		t.Fatalf("HandleCustomerMessage(confirm) error = %v", err)
	}
	if !handled {
		t.Fatalf("expected confirmation to be consumed")
	}
	state = services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.PendingAction != "" || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual {
		t.Fatalf("expected pending action cleared and store manual route, got %+v", state)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 1 {
		t.Fatalf("expected one store room notice after confirmation, got %d", count)
	}
	repeat := createHumanDispatchMessage(t, db, conversation.ID, 30, enums.IMSenderTypeCustomer, "确认")
	handled, err = services.ConversationHandoffConfirmationService.HandleCustomerMessage(&conversation, &repeat)
	if err != nil {
		t.Fatalf("HandleCustomerMessage(repeat) error = %v", err)
	}
	if handled {
		t.Fatalf("expected repeated confirmation without pending action not to be consumed")
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 1 {
		t.Fatalf("expected no duplicate store room notice, got %d", count)
	}
}

func TestConversationHandoffConfirmationPendingExpiresInFiveMinutes(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")

	start := time.Now()
	if _, err := services.ConversationHandoffConfirmationService.RequestByAI(conversation.ID, aiAgent, "客人需要人工接待", "req-ask"); err != nil {
		t.Fatalf("RequestByAI() error = %v", err)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.PendingAction != string(enums.ConversationPendingActionHumanHandoff) || state.PendingActionExpireAt == nil {
		t.Fatalf("expected pending human handoff with expiry, got %+v", state)
	}
	minExpire := start.Add(services.DefaultHandoffConfirmationMinutes * time.Minute).Add(-2 * time.Second)
	maxExpire := start.Add(services.DefaultHandoffConfirmationMinutes * time.Minute).Add(2 * time.Second)
	if state.PendingActionExpireAt.Before(minExpire) || state.PendingActionExpireAt.After(maxExpire) {
		t.Fatalf("expected pending expiry around 5 minutes, got %v", state.PendingActionExpireAt)
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

	if _, err := services.ConversationHandoffConfirmationService.RequestByAI(conversation.ID, aiAgent, "客人需要人工接待；客户消息：我要找人", "req-ask"); err != nil {
		t.Fatalf("RequestByAI() error = %v", err)
	}
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

	if _, err := services.ConversationHandoffConfirmationService.RequestByAI(conversation.ID, aiAgent, "客人需要人工接待", "req-ask"); err != nil {
		t.Fatalf("RequestByAI() error = %v", err)
	}
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
	if latest == nil || latest.SenderType != enums.IMSenderTypeAI || !strings.Contains(latest.Content, "先不转人工") {
		t.Fatalf("expected cancel acknowledgement, got %+v", latest)
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

	if _, err := services.ConversationHandoffConfirmationService.RequestByAI(conversation.ID, aiAgent, "客人需要人工接待", "req-ask"); err != nil {
		t.Fatalf("RequestByAI() error = %v", err)
	}
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

func TestManualSessionTimeoutRestoresHQPendingToAI(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "1")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)

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
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusAIServing || state.ManualExpireAt != nil || state.NeedHumanFollowUp {
		t.Fatalf("expected HQ pending timeout to restore AI route, got %+v", state)
	}
	current := services.ConversationService.Get(conversation.ID)
	if current == nil || current.Status != enums.IMConversationStatusAIServing || current.CurrentAssigneeID != 0 {
		t.Fatalf("expected conversation shell restored to AI, got %+v", current)
	}
}

func TestManualSessionTimeoutRestoresStoreManualOrdinaryToAI(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")

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
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusAIServing || state.ManualExpireAt != nil || state.NeedHumanFollowUp {
		t.Fatalf("expected ordinary store manual timeout to restore AI route, got %+v", state)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 1 {
		t.Fatalf("expected no duplicate store room notice on ordinary timeout, got %d", count)
	}
}

func TestManualSessionTimeoutStoreSafetyRemindsOnceThenRestoresAI(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")

	if _, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "客人摔倒流血，需要尽快处理"); err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.ManualExpireAt == nil {
		t.Fatalf("expected safety store manual with timeout, got %+v", state)
	}
	if state.ManualExpireAt.After(time.Now().Add(services.DefaultStoreWecomSafetyManualMinutes*time.Minute + 2*time.Second)) {
		t.Fatalf("expected safety store manual timeout around 2 minutes, got %v", state.ManualExpireAt)
	}

	setRouteManualExpireAt(t, db, conversation.ID, time.Now().Add(-time.Minute))
	if count := services.ManualSessionTimeoutService.ScanAndRestoreExpired(50); count != 1 {
		t.Fatalf("expected one safety timeout reminder handled, got %d", count)
	}
	state = services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || !strings.Contains(state.Remark, "storeSafetyTimeoutReminderSentAt") {
		t.Fatalf("expected safety reminder marker while staying store manual, got %+v", state)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 2 {
		t.Fatalf("expected exactly one extra safety reminder notice, got %d", count)
	}

	setRouteManualExpireAt(t, db, conversation.ID, time.Now().Add(-time.Minute))
	if count := services.ManualSessionTimeoutService.ScanAndRestoreExpired(50); count != 1 {
		t.Fatalf("expected second safety timeout to restore AI, got %d", count)
	}
	state = services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusAIServing || state.NeedHumanFollowUp {
		t.Fatalf("expected safety store manual second timeout to restore AI, got %+v", state)
	}
	if count := countStoreRoomHandoffNoticeOutbox(t, db, conversation.ID); count != 2 {
		t.Fatalf("expected no repeated safety reminder notice, got %d", count)
	}
}

func TestStoreManualAgentReplyStartsIdleTimeout(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	operator := createHumanDispatchStoreAgent(t, db, 101)

	if _, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "客人需要人工接待"); err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
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

func TestManualSessionTimeoutRestoresStoreManualAfterAgentReplyWithCustomerNotice(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	operator := createHumanDispatchStoreAgent(t, db, 101)

	if _, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "客人需要人工接待"); err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	if _, err := services.MessageService.SendAgentMessageWithRequestID(conversation.ID, 0, "store-manual-reply-timeout-notice", enums.IMMessageTypeText, "我来处理。", "", operator, "req-store-reply-notice"); err != nil {
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
	if latest == nil || latest.SenderType != enums.IMSenderTypeAI || !strings.Contains(latest.Content, "同事这边本次人工接待已结束") {
		t.Fatalf("expected AI handback notice after manual idle timeout, got %+v", latest)
	}
}

func TestConversationHumanDispatchStoreManualAllowsWebReplyWithoutClaim(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	operator := createHumanDispatchStoreAgent(t, db, 101)

	result, err := services.ConversationHumanDispatchService.HandoffByAI(conversation.ID, aiAgent, "客人需要人工接待")
	if err != nil {
		t.Fatalf("HandoffByAI() error = %v", err)
	}
	if result == nil || result.Decision != services.HandoffDecisionStoreWecom {
		t.Fatalf("expected store_wecom decision, got %+v", result)
	}

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
		&models.ConversationParticipant{},
		&models.ConversationAssignment{},
		&models.ConversationEventLog{},
		&models.ConversationReadState{},
		&models.Message{},
		&models.MessageSyncLog{},
		&models.ChannelMessageOutbox{},
		&models.Notification{},
	); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	if err := db.Create(&models.Channel{
		ID: 1, TenantID: 101, Name: "测试网页渠道", ChannelType: enums.ChannelTypeWeb,
		ChannelID: "human-dispatch-test", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}).Error; err != nil {
		t.Fatalf("create default channel: %v", err)
	}
	return db
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

func createHumanDispatchTeam(t *testing.T, db *gorm.DB, id int64, name string) {
	t.Helper()
	if err := db.Create(&models.AgentTeam{ID: id, TenantID: 101, Name: name, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create team error = %v", err)
	}
}

func createHumanDispatchActiveSchedule(t *testing.T, db *gorm.DB, teamID int64) {
	t.Helper()
	now := time.Now()
	if err := db.Create(&models.AgentTeamSchedule{
		TenantID: 101,
		TeamID:   teamID,
		StartAt:  now.Add(-time.Hour),
		EndAt:    now.Add(time.Hour),
		Status:   enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create schedule error = %v", err)
	}
}

func createHumanDispatchAgentProfile(t *testing.T, db *gorm.DB, userID, teamID int64, serviceStatus enums.ServiceStatus, maxConcurrent int, autoAssign bool, status enums.Status) {
	t.Helper()
	if err := db.Create(&models.User{
		ID:       userID,
		TenantID: 101,
		Username: "agent",
		Nickname: "客服",
		Status:   enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create user error = %v", err)
	}
	if err := db.Create(&models.AgentProfile{
		TenantID:           101,
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

func createHumanDispatchStoreAgent(t *testing.T, db *gorm.DB, userID int64) *dto.AuthPrincipal {
	t.Helper()
	createHumanDispatchTeam(t, db, 1, "门店人工接待组")
	if err := db.Model(&models.AgentTeam{}).Where("id = ?", 1).Updates(map[string]any{
		"store_scope_ids":            "88",
		"wx_work_instance_scope_ids": "77",
	}).Error; err != nil {
		t.Fatalf("bind store runtime to agent team error = %v", err)
	}
	createHumanDispatchAgentProfile(t, db, userID, 1, enums.ServiceStatusIdle, 3, false, enums.StatusOk)
	return &dto.AuthPrincipal{
		UserID:         userID,
		TenantID:       101,
		ActiveTenantID: 101,
		Username:       "store-agent",
		Nickname:       "门店客服",
		Roles:          []string{constants.RoleCodeCsUser},
	}
}

func createHumanDispatchConversation(t *testing.T, db *gorm.DB, aiAgentID int64, status enums.IMConversationStatus) models.Conversation {
	t.Helper()
	now := time.Now()
	item := models.Conversation{
		TenantID:      101,
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
		TenantID:       101,
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

func setHumanDispatchConversationSummary(t *testing.T, db *gorm.DB, conversationID int64, summary string) {
	t.Helper()
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversationID).Update("last_message_summary", summary).Error; err != nil {
		t.Fatalf("update conversation summary error = %v", err)
	}
}

func createHumanDispatchStoreRoomRuntime(t *testing.T, db *gorm.DB, conversationID int64, managedMode string, serviceHours string) {
	t.Helper()
	if err := db.Create(&models.Store{ID: 88, TenantID: 101, StoreCode: "store-room-test", Name: "测试门店", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create store error = %v", err)
	}
	binding := models.StoreStaffBinding{
		ID:                      55,
		TenantID:                101,
		StoreID:                 88,
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
		ID:                     77,
		TenantID:               101,
		Guid:                   "guid-store-room-test",
		StoreID:                binding.StoreID,
		StoreStaffBindingID:    binding.ID,
		StoreNavigationName:    "测试门店",
		Status:                 enums.StatusOk,
		HealthStatus:           "online",
		FallbackToHQ:           true,
		ManualTimeoutMinutes:   10,
		StoreRoomNotifyEnabled: true,
	}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatalf("create wxwork protocol instance error = %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: conversationID, StoreID: binding.StoreID, WxWorkInstanceID: instance.ID, RouteStatus: enums.ConversationRouteStatusAIServing, RouteTarget: "ai", SessionNo: 1}).Error; err != nil {
		t.Fatalf("create conversation route state error = %v", err)
	}
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

func testHumanDispatchOperator() *dto.AuthPrincipal {
	return &dto.AuthPrincipal{UserID: 9, TenantID: 101, ActiveTenantID: 101, Username: "dispatcher", Nickname: "调度员"}
}
