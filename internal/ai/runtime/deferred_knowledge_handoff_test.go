package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	applicationruntime "agent-desk/internal/ai/application/runtime"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"
)

func TestDeferredKnowledgeHandoffFromTrace(t *testing.T) {
	reason, ok := deferredKnowledgeHandoffFromTrace(`{"pipeline":{"evidenceJudge":{"deferredHandoff":true,"deferredHandoffReason":"空调故障需要同事接手"}}}`)
	if !ok || reason != "空调故障需要同事接手" {
		t.Fatalf("unexpected deferred handoff: ok=%v reason=%q", ok, reason)
	}
	if _, ok := deferredKnowledgeHandoffFromTrace(`{"pipeline":{"evidenceJudge":{"deferredHandoff":false}}}`); ok {
		t.Fatal("disabled deferred handoff must not be dispatched")
	}
	if _, ok := deferredKnowledgeHandoffFromTrace(`not-json`); ok {
		t.Fatal("invalid trace must not dispatch a handoff")
	}
}

func TestResolveReplyExecutionActionsDispatchesDeferredHandoffWithoutReplyText(t *testing.T) {
	summary := &applicationruntime.Summary{TraceData: `{"pipeline":{"evidenceJudge":{"deferredHandoff":true,"deferredHandoffReason":"空调故障需要同事接手"}}}`}
	hasCommitPayload, hasDeferred := resolveReplyExecutionActions(summary, false)
	if hasCommitPayload {
		t.Fatal("empty generated text must not enter the reply commit path")
	}
	if !hasDeferred {
		t.Fatal("deferred handoff must still dispatch when generated reply text is empty")
	}
}

func TestResolveReplyExecutionActionsKeepsReplyAndDeferredActionsIndependent(t *testing.T) {
	summary := &applicationruntime.Summary{
		ReplyText: "酒店暂不提供早餐。",
		TraceData: `{"pipeline":{"evidenceJudge":{"deferredHandoff":true}}}`,
	}
	hasCommitPayload, hasDeferred := resolveReplyExecutionActions(summary, false)
	if !hasCommitPayload || !hasDeferred {
		t.Fatalf("expected both answer commit and deferred handoff, commit=%v deferred=%v", hasCommitPayload, hasDeferred)
	}
}

func TestDispatchDeferredKnowledgeHandoffKeepsCommittedAnswerBeforeDirectHandoff(t *testing.T) {
	db := setupRuntimeReplyMessageTestDB(t)
	if err := db.AutoMigrate(
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.ChannelMessageOutbox{},
		&models.AIAgent{},
		&models.AgentTeam{},
		&models.AgentTeamSchedule{},
		&models.AIManualResumeTask{},
		&models.ConversationReadState{},
		&models.ConversationEventLog{},
	); err != nil {
		t.Fatalf("auto migrate deferred handoff tables: %v", err)
	}

	const teamID int64 = 9501
	if err := db.Create(&models.AgentTeam{ID: teamID, Name: "测试客服组", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}
	now := time.Now()
	if err := db.Create(&models.AgentTeamSchedule{
		TeamID:  teamID,
		StartAt: now.Add(-time.Hour),
		EndAt:   now.Add(time.Hour),
		Status:  enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create team schedule: %v", err)
	}
	aiAgent := models.AIAgent{
		ID:          9502,
		Name:        "AI",
		TeamIDs:     "9501",
		ServiceMode: enums.IMConversationServiceModeAIFirst,
		Status:      enums.StatusOk,
	}
	if err := db.Create(&aiAgent).Error; err != nil {
		t.Fatalf("create ai agent: %v", err)
	}
	conversation := models.Conversation{
		ID:          9503,
		CustomerID:  9504,
		AIAgentID:   aiAgent.ID,
		Status:      enums.IMConversationStatusAIServing,
		ServiceMode: enums.IMConversationServiceModeAIFirst,
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		ConversationID: conversation.ID,
		RouteStatus:    enums.ConversationRouteStatusAIServing,
		RouteTarget:    "ai",
		SessionNo:      1,
	}).Error; err != nil {
		t.Fatalf("create route state: %v", err)
	}
	origin := models.Message{
		ID:             9505,
		ConversationID: conversation.ID,
		ClientMsgID:    "customer-multi-question",
		SeqNo:          1,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeText,
		Content:        "早餐几点，帮我把浴巾送到1208房间",
		RequestID:      "req-deferred-direct",
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
	}
	if err := db.Create(&origin).Error; err != nil {
		t.Fatalf("create origin message: %v", err)
	}
	answerTime := now.Add(time.Millisecond)
	answer := models.Message{
		ID:             9506,
		ConversationID: conversation.ID,
		ClientMsgID:    "ai-answer-before-handoff",
		SeqNo:          2,
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        "早餐时间是7:00-9:30。",
		RequestID:      origin.RequestID,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &answerTime,
	}
	if err := db.Create(&answer).Error; err != nil {
		t.Fatalf("create committed answer: %v", err)
	}
	answerOutbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID,
		MessageID:      answer.ID,
		Payload:        `{}`,
		SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
		AuditFields:    models.AuditFields{CreatedAt: answerTime, UpdatedAt: answerTime},
	}
	if err := db.Create(answerOutbox).Error; err != nil {
		t.Fatalf("create committed answer Outbox: %v", err)
	}

	summary := &applicationruntime.Summary{TraceData: `{"pipeline":{"evidenceJudge":{"deferredHandoff":true,"deferredHandoffReason":"待处理问题：帮客户把浴巾送到1208房间"}}}`}
	err := newAIReplyService().dispatchDeferredKnowledgeHandoff(context.Background(), aiReplyContext{
		Conversation: conversation,
		Message:      origin,
		AIAgent:      aiAgent,
	}, summary)
	if err != nil {
		t.Fatalf("dispatchDeferredKnowledgeHandoff() error = %v", err)
	}

	var aiReplies []models.Message
	if err := db.Where("conversation_id = ? AND sender_type = ?", conversation.ID, enums.IMSenderTypeAI).Order("seq_no ASC, id ASC").Find(&aiReplies).Error; err != nil {
		t.Fatalf("load AI replies: %v", err)
	}
	if len(aiReplies) != 2 || aiReplies[0].Content != answer.Content || aiReplies[1].Content != services.DirectHandoffSuccessMessage {
		t.Fatalf("expected committed answer followed by one direct handoff notice, got %+v", aiReplies)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.PendingAction != "" {
		t.Fatalf("expected direct manual route without a pending action, got %+v", state)
	}
	if err := db.First(answerOutbox, answerOutbox.ID).Error; err != nil {
		t.Fatalf("reload committed answer Outbox: %v", err)
	}
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(answerOutbox.Payload), &payload); err != nil {
		t.Fatalf("parse committed answer Outbox payload: %v", err)
	}
	if answerOutbox.SendStatus != string(enums.ChannelMessageOutboxStatusPending) || payload["replyBeforeDeferredHandoff"] != true {
		t.Fatalf("deferred route must preserve the already-committed sibling answer: %+v payload=%#v", answerOutbox, payload)
	}
	combined := strings.ToLower(aiReplies[0].Content + "\n" + aiReplies[1].Content)
	for _, forbidden := range []string{"confirmation", "确认或取消", "回复“确认”"} {
		if strings.Contains(combined, strings.ToLower(forbidden)) {
			t.Fatalf("deferred direct handoff still contains confirmation protocol %q: %s", forbidden, combined)
		}
	}
}
