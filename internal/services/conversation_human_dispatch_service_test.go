package services_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/events"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/eventbus"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/repositories"
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

func TestConversationHumanDispatchSameRequestIsIdempotent(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "我要找人工")

	const callers = 8
	errCh := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := services.ConversationHumanDispatchService.HandoffByAIWithRequestID(conversation.ID, aiAgent, "用户明确要求人工", "handoff-idempotent-request")
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent HandoffByAIWithRequestID() error = %v", err)
		}
	}

	var transferCount int64
	if err := db.Model(&models.ConversationEventLog{}).
		Where("tenant_id = ? AND conversation_id = ? AND request_id = ? AND event_type = ? AND operator_type = ?", 101, conversation.ID, "handoff-idempotent-request", enums.IMEventTypeTransfer, enums.IMSenderTypeAI).
		Count(&transferCount).Error; err != nil {
		t.Fatalf("count handoff events: %v", err)
	}
	if transferCount != 1 {
		t.Fatalf("expected one handoff event, got %d", transferCount)
	}
	var waitingCount int64
	if err := db.Model(&models.Message{}).
		Where("tenant_id = ? AND conversation_id = ? AND sender_type = ? AND content = ?", 101, conversation.ID, enums.IMSenderTypeAI, services.HandoffWaitingMessage).
		Count(&waitingCount).Error; err != nil {
		t.Fatalf("count handoff waiting messages: %v", err)
	}
	if waitingCount != 1 {
		t.Fatalf("expected one handoff waiting message, got %d", waitingCount)
	}

	resumeErrCh := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := services.AIManualResumeTaskService.Schedule(conversation.ID, origin.ID, "concurrent-token-"+strconv.Itoa(index))
			resumeErrCh <- err
		}(i)
	}
	wg.Wait()
	close(resumeErrCh)
	for err := range resumeErrCh {
		if err != nil {
			t.Fatalf("concurrent Schedule() error = %v", err)
		}
	}
	var taskCount int64
	if err := db.Model(&models.AIManualResumeTask{}).
		Where("tenant_id = ? AND conversation_id = ?", 101, conversation.ID).
		Count(&taskCount).Error; err != nil {
		t.Fatalf("count manual resume tasks: %v", err)
	}
	if taskCount != 1 {
		t.Fatalf("expected one manual resume task, got %d", taskCount)
	}
}

func TestConversationHandoffConfirmationSameRequestSendsOnePrompt(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	origin := createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "我要投诉")

	const callers = 8
	errCh := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := services.ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(conversation.ID, aiAgent, "客人需要人工接待", "handoff-confirm-idempotent", origin.ID)
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent RequestByAIWithOriginMessage() error = %v", err)
		}
	}

	var promptCount int64
	if err := db.Model(&models.Message{}).
		Where("tenant_id = ? AND conversation_id = ? AND client_msg_id LIKE ?", 101, conversation.ID, "ai_handoff_confirm_%").
		Count(&promptCount).Error; err != nil {
		t.Fatalf("count handoff prompts: %v", err)
	}
	if promptCount != 1 {
		t.Fatalf("expected one handoff confirmation prompt, got %d", promptCount)
	}
	state := services.ConversationRouteService.GetByConversationIDInTenant(conversation.ID, 101)
	if state == nil || state.PendingAction != string(enums.ConversationPendingActionHumanHandoff) {
		t.Fatalf("expected one pending handoff confirmation, got %+v", state)
	}
}

func TestConversationHumanDispatchAssignsScheduledAgentEvenOnBreak(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	createHumanDispatchTeam(t, db, 1, "综合客服组")
	if err := db.Model(&models.AgentTeam{}).Where("tenant_id = ? AND id = ?", 101, 1).Updates(map[string]any{
		"is_default":    true,
		"dispatch_mode": enums.AgentTeamDispatchModeRule,
	}).Error; err != nil {
		t.Fatalf("configure default rule team: %v", err)
	}
	createHumanDispatchActiveSchedule(t, db, 1)
	createHumanDispatchAgentProfile(t, db, 101, 1, 3, true, enums.StatusOk)
	if err := db.Model(&models.AgentPresenceSession{}).
		Where("tenant_id = ? AND user_id = ? AND ended_at IS NULL", 101, 101).
		Updates(map[string]any{"status": enums.AgentPresenceStatusBreak, "last_seen_at": time.Now()}).Error; err != nil {
		t.Fatalf("put agent on break: %v", err)
	}
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)

	if _, err := services.ConversationHumanDispatchService.HandoffByAIWithRequestID(conversation.ID, aiAgent, "用户明确要求人工", "handoff-pending-recovery"); err != nil {
		t.Fatalf("HandoffByAIWithRequestID() error = %v", err)
	}
	dispatched, err := services.ConversationDispatchService.DispatchConversation(conversation.ID)
	if err != nil {
		t.Fatalf("dispatch scheduled agent error = %v", err)
	}
	if dispatched == nil {
		t.Fatal("scheduled agent on break must still receive the task")
	}
	current := services.ConversationService.Get(conversation.ID)
	if current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID != 101 || current.CurrentTeamID != 1 {
		t.Fatalf("expected deterministic rule assignment while agent is on break, got %+v", current)
	}
	assignment := repositories.ConversationAssignmentRepository.FindOne(db, sqls.NewCnd().
		Eq("tenant_id", 101).
		Eq("conversation_id", conversation.ID).
		Eq("status", enums.IMAssignmentStatusActive))
	if assignment == nil || assignment.DispatchMode != enums.AgentTeamDispatchModeRule {
		t.Fatalf("expected active rule assignment, got %+v", assignment)
	}
}

func TestConversationHumanDispatchAIHandoffEntersStoreManual(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "1")
	createHumanDispatchTeam(t, db, 1, "售后支持组")
	createHumanDispatchActiveSchedule(t, db, 1)
	createHumanDispatchAgentProfile(t, db, 101, 1, 3, true, enums.StatusOk)
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")

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

func TestConversationHandoffConfirmationExecutesOnce(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
	createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "这个问题需要人工处理")

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
	createHumanDispatchMessage(t, db, conversation.ID, 10, enums.IMSenderTypeCustomer, "我摔倒流血了")

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
	if latest == nil || latest.SenderType != enums.IMSenderTypeAI || !strings.Contains(latest.Content, "刚才由同事协助的这段接待先结束了") {
		t.Fatalf("expected AI handback notice after manual idle timeout, got %+v", latest)
	}
}

func TestManualSessionTimeoutRequeuesLatestCustomerMessageAfterManualRoute(t *testing.T) {
	db := setupConversationHumanDispatchTestDB(t)
	aiAgent := createHumanDispatchAIAgent(t, db, enums.IMConversationServiceModeAIFirst, "")
	conversation := createHumanDispatchConversation(t, db, aiAgent.ID, enums.IMConversationStatusAIServing)
	createHumanDispatchStoreRoomRuntime(t, db, conversation.ID, constants.StoreManagedModeSemi, "00:00-23:59")
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
	services.TriggerAIReplySyncHook = func(_ context.Context, conversation models.Conversation, message models.Message) (services.AIReplyExecutionResult, error) {
		triggeredMessageID = message.ID
		triggeredContent = message.Content
		_, err := services.MessageService.SendAIMessageWithRequestID(conversation.ID, aiAgent.ID, "resume-test-reply", enums.IMMessageTypeText, "我继续帮你处理电视问题。", "", &dto.AuthPrincipal{Username: "AI"}, message.RequestID)
		return services.AIReplyExecutionResult{Status: services.AIReplyExecutionStatusCompleted, ReasonCode: "test_completed"}, err
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
		t.Fatalf("expected route restored to AI after requeue, got %+v", state)
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

func TestConversationHumanDispatchAIHandoffIgnoresLegacyAgentTeamIDsWithoutRouteOwner(t *testing.T) {
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
	if result == nil || result.Decision != services.HandoffDecisionHQAgentDesk {
		t.Fatalf("expected hq_agentdesk decision, got %+v", result)
	}

	current := services.ConversationService.Get(conversation.ID)
	if current.Status != enums.IMConversationStatusPending {
		t.Fatalf("expected pending HQ conversation, got status=%d", current.Status)
	}
	if current.CurrentTeamID != 0 || current.CurrentAssigneeID != 0 {
		t.Fatalf("legacy AI Agent team IDs must not select a team, got team=%d assignee=%d", current.CurrentTeamID, current.CurrentAssigneeID)
	}
	route := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if route == nil || route.RouteStatus != enums.ConversationRouteStatusHQAgentDeskPending {
		t.Fatalf("expected HQ pending route, got %+v", route)
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
	if err := db.Model(&models.AgentTeam{}).Where("id = ?", 1).Update("is_default", true).Error; err != nil {
		t.Fatalf("mark default team: %v", err)
	}
	createHumanDispatchActiveSchedule(t, db, 1)
	createHumanDispatchAgentProfile(t, db, 101, 1, 3, true, enums.StatusOk)

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
		eventbus.WaitAsync[events.ConversationAssignedEvent]()
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(
		&models.User{},
		&models.Role{},
		&models.Permission{},
		&models.UserRole{},
		&models.RolePermission{},
		&models.Customer{},
		&models.CustomerIdentity{},
		&models.WxWorkCustomerHandoffSetting{},
		&models.Channel{},
		&models.AIAgent{},
		&models.AgentTeam{},
		&models.AgentTeamSchedule{},
		&models.AgentProfile{},
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
		&models.Message{},
		&models.ConversationServiceSession{},
		&models.ConversationResponseSpan{},
		&models.AgentPresenceSession{},
		&models.ServiceAnalyticsPolicy{},
		&models.DispatchDecisionLog{},
		&models.MessageSyncLog{},
		&models.ChannelMessageOutbox{},
		&models.MessageSyncLog{},
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
	role := models.Role{ID: 1, Name: "客服", Code: constants.RoleCodeCsUser, Status: enums.StatusOk}
	if err := db.Create(&role).Error; err != nil {
		t.Fatalf("create default agent role: %v", err)
	}
	permissions := []models.Permission{
		{ID: 1, Name: "查看会话", Code: constants.PermissionConversationView.Code, Status: enums.StatusOk},
		{ID: 2, Name: "发送会话消息", Code: constants.PermissionConversationSend.Code, Status: enums.StatusOk},
	}
	if err := db.Create(&permissions).Error; err != nil {
		t.Fatalf("create default conversation permissions: %v", err)
	}
	for _, permission := range permissions {
		if err := db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error; err != nil {
			t.Fatalf("bind default conversation permission: %v", err)
		}
	}
	return db
}

func createHumanDispatchAIAgent(t *testing.T, db *gorm.DB, mode enums.IMConversationServiceMode, teamIDs string) models.AIAgent {
	t.Helper()
	item := models.AIAgent{
		TenantID:    101,
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

func createHumanDispatchAgentProfile(t *testing.T, db *gorm.DB, userID, teamID int64, maxConcurrent int, autoAssign bool, status enums.Status) {
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
	if err := db.Create(&models.UserRole{UserID: userID, RoleID: 1}).Error; err != nil {
		t.Fatalf("bind agent role error = %v", err)
	}
	if err := db.Create(&models.AgentProfile{
		TenantID:           101,
		UserID:             userID,
		TeamID:             teamID,
		AgentCode:          "A001",
		DisplayName:        "客服",
		MaxConcurrentCount: maxConcurrent,
		AutoAssignEnabled:  autoAssign,
		Status:             status,
	}).Error; err != nil {
		t.Fatalf("create profile error = %v", err)
	}
	if autoAssign {
		profile := repositories.AgentProfileRepository.Take(db, "tenant_id = ? AND user_id = ?", 101, userID)
		now := time.Now()
		if profile == nil {
			t.Fatal("created auto-assignment profile not found")
		}
		if err := db.Create(&models.AgentPresenceSession{
			TenantID: 101, UserID: userID, AgentProfileID: profile.ID, TeamID: teamID,
			Status: enums.AgentPresenceStatusIdle, Source: "test", StartedAt: now, LastSeenAt: now,
		}).Error; err != nil {
			t.Fatalf("create profile presence error = %v", err)
		}
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
	createHumanDispatchAgentProfile(t, db, userID, 1, 3, false, enums.StatusOk)
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
		AIReplyEnabled:         true,
	}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatalf("create wxwork protocol instance error = %v", err)
	}
	if err := db.Model(&models.Conversation{}).Where("id = ? AND tenant_id = ?", conversationID, int64(101)).Updates(map[string]any{
		"store_id": binding.StoreID, "store_staff_binding_id": binding.ID,
	}).Error; err != nil {
		t.Fatalf("scope conversation to Store staff binding: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: conversationID, StoreID: binding.StoreID, StoreStaffBindingID: binding.ID, WxWorkInstanceID: instance.ID, RouteStatus: enums.ConversationRouteStatusAIServing, RouteTarget: "ai", SessionNo: 1}).Error; err != nil {
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
	return &dto.AuthPrincipal{UserID: 9, TenantID: 101, ActiveTenantID: 101, Username: "dispatcher", Nickname: "调度员"}
}
