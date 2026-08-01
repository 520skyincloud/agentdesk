package services

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestWxWorkProtocolReplacedInstanceArchivesAuthenticatedLateMessage(t *testing.T) {
	db, store, channel, binding, replaced, external, conversation := setupStoreConversationContinuityFixture(t, "late-replaced-callback")
	callbackToken := "late-replaced-callback-token"
	configureWxWorkProtocolCallbackToken(t, db, channel, callbackToken)
	if _, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, replaced, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("prepare replaced instance session: %v", err)
	}
	replacement := createContinuityTestInstance(t, db, store, channel, binding, "late-replacement-current", "online")
	now := time.Now()
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", replaced.ID).Updates(map[string]any{
		"status": enums.StatusDisabled, "replaced_by_instance_id": replacement.ID, "replaced_at": now,
	}).Error; err != nil {
		t.Fatalf("retire replaced instance: %v", err)
	}
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", replacement.ID).Updates(map[string]any{
		"replaces_instance_id":      replaced.ID,
		"remote_setup_submitted_at": now,
	}).Error; err != nil {
		t.Fatalf("link replacement instance: %v", err)
	}
	replaced.Status = enums.StatusDisabled
	replaced.ReplacedByInstanceID = replacement.ID
	replacement.ReplacesInstanceID = replaced.ID
	replacement.RemoteSetupSubmittedAt = &now
	if sessionNo, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, replacement, now); err != nil || sessionNo != 2 {
		t.Fatalf("prepare replacement session=%d err=%v", sessionNo, err)
	}
	externalID := strings.TrimPrefix(external.ExternalID, "wxwork_protocol:")
	if err := WxWorkProtocolService.upsertConversationMapping(replacement, conversation.ID, request.WxProtocolChatMsg{}, externalID, `{}`); err != nil {
		t.Fatalf("create current mapping: %v", err)
	}
	relation := &models.StoreCustomerRelation{
		TenantID: conversation.TenantID, CustomerID: conversation.CustomerID, StoreID: store.ID,
		WxWorkInstanceID: replacement.ID, LastConversationID: conversation.ID, LastActiveAt: &now,
		VisitCount: 3, Tags: `["vip"]`, StableNotes: "keep-current-relation", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(relation).Error; err != nil {
		t.Fatalf("create current store customer relation: %v", err)
	}
	assignment := &models.ConversationAssignment{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, SessionNo: 2,
		ToUserID: binding.UserID, AssignType: "rule", DispatchMode: enums.AgentTeamDispatchModeRule,
		WorkloadWeight: 1, Status: enums.IMAssignmentStatusActive, CreatedAt: now,
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatalf("create current assignment: %v", err)
	}

	before := ConversationService.Get(conversation.ID)
	if before == nil {
		t.Fatal("conversation missing before callback")
	}
	beforeRelation := repositories.StoreCustomerRelationRepository.TakeByCustomerAndStoreInTenant(
		db, store.TenantID, conversation.CustomerID, store.ID,
	)
	if beforeRelation == nil {
		t.Fatal("store customer relation missing before callback")
	}
	beforeEventCount := countConversationHistoryTestRows(t, db, &models.ConversationEventLog{}, "conversation_id = ?", conversation.ID)
	beforeConversationCount := countConversationHistoryTestRows(t, db, &models.Conversation{})
	beforeAssignmentCount := countConversationHistoryTestRows(t, db, &models.ConversationAssignment{}, "conversation_id = ?", conversation.ID)
	triggerCount := 0
	previousHook := TriggerAIReplyAsyncHook
	TriggerAIReplyAsyncHook = func(models.Conversation, models.Message) { triggerCount++ }
	t.Cleanup(func() { TriggerAIReplyAsyncHook = previousHook })

	msg := request.WxProtocolChatMsg{
		Seq: "501", ID: "late-history-501", Sender: externalID, Receiver: replaced.EmployeeUserID,
		RoomID: "0", SenderName: "迟到客户", MsgType: wxProtocolMsgText,
		ContentType: wxProtocolMsgText, Content: "这是旧实例迟到的消息", SendTime: now.Add(-30 * time.Second).Unix(),
	}
	msg.Normalize()
	data, _ := json.Marshal(msg)
	req := request.WxWorkProtocolCallbackRequest{Guid: replaced.Guid, NotifyType: wxProtocolNotifyNewMsgAlt, Data: data}
	if err := WxWorkProtocolService.HandleCallback(req, string(data), callbackToken); err != nil {
		t.Fatalf("archive late callback: %v", err)
	}

	clientMsgID := WxWorkProtocolService.clientMessageID(replaced.Guid, msg)
	message := repositories.MessageRepository.GetByClientMsgIDInTenant(db, conversation.ID, store.TenantID, clientMsgID)
	if message == nil || !message.HistoricalOnly || message.SessionNo != 1 || message.Content != msg.Content {
		t.Fatalf("late callback was not archived in original session: %+v", message)
	}
	after := ConversationService.Get(conversation.ID)
	if after == nil || after.LastMessageID != before.LastMessageID || after.LastMessageSummary != before.LastMessageSummary ||
		after.AgentUnreadCount != before.AgentUnreadCount || after.CustomerUnreadCount != before.CustomerUnreadCount ||
		!after.LastActiveAt.Equal(before.LastActiveAt) {
		t.Fatalf("late callback changed active conversation state: before=%+v after=%+v", before, after)
	}
	if route := ConversationRouteService.GetByConversationIDInTenant(conversation.ID, store.TenantID); route == nil || route.WxWorkInstanceID != replacement.ID || route.SessionNo != 2 {
		t.Fatalf("late callback replaced current route: %+v", route)
	}
	mapping := WxWorkKFConversationService.FindOne(sqls.NewCnd().Eq("tenant_id", store.TenantID).Eq("conversation_id", conversation.ID))
	if mapping == nil || !strings.Contains(mapping.OpenKfID, replacement.Guid) {
		t.Fatalf("late callback replaced current protocol mapping: %+v", mapping)
	}
	if got := countConversationHistoryTestRows(t, db, &models.ConversationEventLog{}, "conversation_id = ?", conversation.ID); got != beforeEventCount {
		t.Fatalf("late callback created conversation events: before=%d after=%d", beforeEventCount, got)
	}
	if got := countConversationHistoryTestRows(t, db, &models.Conversation{}); got != beforeConversationCount {
		t.Fatalf("late callback created a conversation: before=%d after=%d", beforeConversationCount, got)
	}
	afterRelation := repositories.StoreCustomerRelationRepository.TakeByCustomerAndStoreInTenant(
		db, store.TenantID, conversation.CustomerID, store.ID,
	)
	if afterRelation == nil || afterRelation.LastConversationID != beforeRelation.LastConversationID ||
		afterRelation.WxWorkInstanceID != beforeRelation.WxWorkInstanceID || afterRelation.VisitCount != beforeRelation.VisitCount ||
		afterRelation.Tags != beforeRelation.Tags || afterRelation.StableNotes != beforeRelation.StableNotes ||
		!afterRelation.UpdatedAt.Equal(beforeRelation.UpdatedAt) {
		t.Fatalf("late callback changed store customer relation: before=%+v after=%+v", beforeRelation, afterRelation)
	}
	if got := countConversationHistoryTestRows(t, db, &models.ConversationAssignment{}, "conversation_id = ?", conversation.ID); got != beforeAssignmentCount {
		t.Fatalf("late callback changed assignment count: before=%d after=%d", beforeAssignmentCount, got)
	}
	currentAssignment := repositories.ConversationAssignmentRepository.Get(db, assignment.ID)
	if currentAssignment == nil || currentAssignment.Status != assignment.Status || currentAssignment.SessionNo != assignment.SessionNo || currentAssignment.ToUserID != assignment.ToUserID {
		t.Fatalf("late callback changed current assignment: before=%+v after=%+v", assignment, currentAssignment)
	}
	if triggerCount != 0 {
		t.Fatalf("late callback triggered AI %d times", triggerCount)
	}
	currentReplaced := WxWorkProtocolInstanceService.Get(replaced.ID)
	if currentReplaced == nil || currentReplaced.Status != enums.StatusDisabled || currentReplaced.MessageSyncSeq != "501" {
		t.Fatalf("replaced instance checkpoint was not safely advanced: %+v", currentReplaced)
	}
	if err := WxWorkProtocolService.HandleCallback(req, string(data), callbackToken); err != nil {
		t.Fatalf("duplicate late callback: %v", err)
	}
	if got := countConversationHistoryTestRows(t, db, &models.Message{}, "conversation_id = ? AND client_msg_id = ?", conversation.ID, clientMsgID); got != 1 {
		t.Fatalf("duplicate late callback created %d messages", got)
	}
}

func TestWxWorkProtocolReplacedInstanceAuthenticatesBeforeArchiveAndSkipsNonMessage(t *testing.T) {
	db, store, channel, binding, replaced, _, conversation := setupStoreConversationContinuityFixture(t, "late-callback-auth")
	callbackToken := "late-callback-auth-token"
	configureWxWorkProtocolCallbackToken(t, db, channel, callbackToken)
	if _, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, replaced, time.Now()); err != nil {
		t.Fatalf("prepare original session: %v", err)
	}
	replacement := createContinuityTestInstance(t, db, store, channel, binding, "late-callback-auth-current", "online")
	if err := db.Model(replaced).Updates(map[string]any{"status": enums.StatusDisabled, "replaced_by_instance_id": replacement.ID}).Error; err != nil {
		t.Fatalf("retire instance: %v", err)
	}
	replaced.Status = enums.StatusDisabled
	replaced.ReplacedByInstanceID = replacement.ID
	messageData := json.RawMessage(`{"seq":"601","id":"late-auth","sender":"unknown","receiver":"employee","roomid":"0","msg_type":2,"content":"unauthorized"}`)
	req := request.WxWorkProtocolCallbackRequest{Guid: replaced.Guid, NotifyType: wxProtocolNotifyNewMsgAlt, Data: messageData}
	err := WxWorkProtocolService.HandleCallback(req, string(messageData), "wrong-token")
	status, stage := WxWorkProtocolCallbackErrorStatus(err)
	if err == nil || status != 401 || stage != "authenticate" {
		t.Fatalf("wrong token status=%d stage=%q err=%v", status, stage, err)
	}
	if got := countConversationHistoryTestRows(t, db, &models.Message{}, "client_msg_id LIKE ?", "%late-auth%"); got != 0 {
		t.Fatalf("unauthenticated callback created %d messages", got)
	}

	beforeHealth := replaced.HealthStatus
	loginData := json.RawMessage(`{"user_id":"should-not-reactivate","name":"ignored"}`)
	if err := WxWorkProtocolService.HandleCallback(
		request.WxWorkProtocolCallbackRequest{Guid: replaced.Guid, NotifyType: wxProtocolNotifyUserLoginAlt, Data: loginData},
		string(loginData), callbackToken,
	); err != nil {
		t.Fatalf("skip replaced non-message callback: %v", err)
	}
	current := WxWorkProtocolInstanceService.Get(replaced.ID)
	if current == nil || current.Status != enums.StatusDisabled || current.HealthStatus != beforeHealth || current.EmployeeUserID != replaced.EmployeeUserID {
		t.Fatalf("non-message callback reactivated replaced instance: %+v", current)
	}
}

func configureWxWorkProtocolCallbackToken(t *testing.T, db *gorm.DB, channel *models.Channel, token string) {
	t.Helper()
	configJSON, err := json.Marshal(map[string]any{
		"appKey": "callback-test-app", "appSecret": "callback-test-secret", "callbackToken": token,
	})
	if err != nil {
		t.Fatalf("marshal callback config: %v", err)
	}
	if err := db.Model(&models.Channel{}).Where("id = ?", channel.ID).Update("config_json", string(configJSON)).Error; err != nil {
		t.Fatalf("update callback config: %v", err)
	}
	channel.ConfigJSON = string(configJSON)
}

func countConversationHistoryTestRows(t *testing.T, db *gorm.DB, model any, where ...any) int64 {
	t.Helper()
	query := db.Model(model)
	if len(where) > 0 {
		query = query.Where(where[0], where[1:]...)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}
