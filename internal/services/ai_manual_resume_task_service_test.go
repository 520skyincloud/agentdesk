package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/replyruntime"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestManualResumeCommittedExternalReplyWaitsForOutboxWithoutRerunningModel(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "delivery-pending")
	requestID := manualResumeRequestID(task)
	now := time.Now()
	reply := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      origin.SessionNo,
		RequestID:      requestID,
		ClientMsgID:    "manual-resume-delivery-pending-reply",
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        "早餐时间是7:00-9:30。",
		SeqNo:          origin.SeqNo + 1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(reply).Error; err != nil {
		t.Fatalf("create committed reply: %v", err)
	}
	outbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID,
		MessageID:      reply.ID,
		Payload:        `{}`,
		SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create pending Outbox: %v", err)
	}
	createCompletedManualResumeRunLog(t, db, *origin, requestID, reply)

	previousHook := TriggerAIReplySyncHook
	defer func() { TriggerAIReplySyncHook = previousHook }()
	hookCalls := 0
	TriggerAIReplySyncHook = func(context.Context, models.Conversation, models.Message) error {
		hookCalls++
		return nil
	}
	if !AIManualResumeTaskService.processOne(*task, now) {
		t.Fatal("expected pending delivery task to be reconciled")
	}
	if hookCalls != 0 {
		t.Fatalf("a committed reply awaiting Outbox delivery must not rerun the model, hook calls=%d", hookCalls)
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatalf("reload delivery-pending task: %v", err)
	}
	if task.TaskStatus != aiManualResumeTaskRetry || task.RetryCount != 0 || task.NextRetryAt == nil || task.LastError != aiManualResumeAwaitingDeliveryMarker {
		t.Fatalf("delivery reconciliation must not consume a model retry: %+v", task)
	}
	if !MessageService.CanSendAIReply(conversation.ID, requestID, origin.ID) {
		t.Fatal("the same request and source must retain Outbox dispatch eligibility while delivery is pending")
	}
	state := ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual {
		t.Fatalf("the route must remain manual until the committed reply is delivered: %+v", state)
	}

	if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{
		"send_status": string(enums.ChannelMessageOutboxStatusSent),
		"sent_at":     now,
	}).Error; err != nil {
		t.Fatalf("mark Outbox sent: %v", err)
	}
	if !AIManualResumeTaskService.processOne(*task, now.Add(aiManualResumeDeliveryReconcileDelay)) {
		t.Fatal("expected delivered task to finalize")
	}
	if hookCalls != 0 {
		t.Fatalf("delivery finalization must reuse the old Commit, hook calls=%d", hookCalls)
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatalf("reload finalized task: %v", err)
	}
	if task.TaskStatus != aiManualResumeTaskSucceeded || task.CompletedAt == nil || task.RetryCount != 0 {
		t.Fatalf("sent Outbox must finalize the original task without model retries: %+v", task)
	}
	state = ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusAIServing {
		t.Fatalf("sent Outbox must restore the AI route: %+v", state)
	}
}

func TestManualResumeAwaitingDeliveryDoesNotOverrideNewWaitingSource(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	_, _, origin, task := prepareReadyExternalManualResumeTask(t, db, "delivery-new-source")
	if err := db.Model(&models.AIManualResumeTask{}).Where("id = ?", task.ID).Update("task_status", aiManualResumeTaskRunning).Error; err != nil {
		t.Fatalf("mark task running: %v", err)
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatalf("reload running task: %v", err)
	}
	staleRunning := *task
	newSourceID := origin.ID + 100
	if err := db.Model(&models.AIManualResumeTask{}).Where("id = ?", task.ID).Updates(map[string]any{
		"task_status":               aiManualResumeTaskWaiting,
		"latest_waiting_message_id": newSourceID,
		"next_retry_at":             nil,
		"last_error":                "",
	}).Error; err != nil {
		t.Fatalf("record newer waiting source: %v", err)
	}

	if err := AIManualResumeTaskService.awaitCommittedDelivery(&staleRunning, time.Now()); err != nil {
		t.Fatalf("awaitCommittedDelivery() error = %v", err)
	}
	reloaded := &models.AIManualResumeTask{}
	if err := db.First(reloaded, task.ID).Error; err != nil {
		t.Fatalf("reload task after stale delivery reconciliation: %v", err)
	}
	if reloaded.TaskStatus != aiManualResumeTaskWaiting || reloaded.LatestWaitingMessageID != newSourceID || reloaded.NextRetryAt != nil || reloaded.LastError != "" {
		t.Fatalf("a stale delivery reconciliation must not overwrite a newer customer source: %+v", reloaded)
	}
}

func TestManualResumeStaleTransitionsDoNotOverrideNewWaitingSource(t *testing.T) {
	tests := []struct {
		name       string
		transition func(*models.AIManualResumeTask, time.Time) error
	}{
		{
			name: "await delivery",
			transition: func(task *models.AIManualResumeTask, now time.Time) error {
				return AIManualResumeTaskService.awaitCommittedDelivery(task, now)
			},
		},
		{
			name: "hold uncertain delivery",
			transition: func(task *models.AIManualResumeTask, now time.Time) error {
				return AIManualResumeTaskService.holdUncertainDelivery(task, now)
			},
		},
		{
			name: "hold failed delivery",
			transition: func(task *models.AIManualResumeTask, now time.Time) error {
				return AIManualResumeTaskService.holdFailedDelivery(task, now)
			},
		},
		{
			name: "complete",
			transition: func(task *models.AIManualResumeTask, now time.Time) error {
				return AIManualResumeTaskService.completeTask(task, now)
			},
		},
		{
			name: "cancel",
			transition: func(task *models.AIManualResumeTask, _ time.Time) error {
				return AIManualResumeTaskService.cancelTask(task, "stale cancellation")
			},
		},
		{
			name: "retry",
			transition: func(task *models.AIManualResumeTask, now time.Time) error {
				return AIManualResumeTaskService.failOrRetry(task, fmt.Errorf("stale retry"), now)
			},
		},
		{
			name: "terminal failure",
			transition: func(task *models.AIManualResumeTask, now time.Time) error {
				task.RetryCount = 3
				return AIManualResumeTaskService.failOrRetry(task, fmt.Errorf("stale terminal failure"), now)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupMessageWelcomeTestDB(t)
			_, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "stale-transition-"+strings.ReplaceAll(test.name, " ", "-"))
			if err := db.Model(&models.AIManualResumeTask{}).Where("id = ?", task.ID).Update("task_status", aiManualResumeTaskRunning).Error; err != nil {
				t.Fatalf("mark task running: %v", err)
			}
			if err := db.First(task, task.ID).Error; err != nil {
				t.Fatalf("reload running task: %v", err)
			}
			staleRunning := *task
			newSourceID := origin.ID + 100
			if err := db.Model(&models.AIManualResumeTask{}).Where("id = ?", task.ID).Updates(map[string]any{
				"task_status":               aiManualResumeTaskWaiting,
				"latest_waiting_message_id": newSourceID,
				"next_retry_at":             nil,
				"completed_at":              nil,
				"last_error":                "",
			}).Error; err != nil {
				t.Fatalf("record newer waiting source: %v", err)
			}
			routeBefore := ConversationRouteService.GetByConversationID(conversation.ID)
			if routeBefore == nil {
				t.Fatal("expected conversation route")
			}

			if err := test.transition(&staleRunning, time.Now()); err != nil {
				t.Fatalf("stale transition returned error: %v", err)
			}

			reloaded := &models.AIManualResumeTask{}
			if err := db.First(reloaded, task.ID).Error; err != nil {
				t.Fatalf("reload task after stale transition: %v", err)
			}
			if reloaded.TaskStatus != aiManualResumeTaskWaiting || reloaded.LatestWaitingMessageID != newSourceID || reloaded.NextRetryAt != nil || reloaded.CompletedAt != nil || reloaded.LastError != "" {
				t.Fatalf("stale transition overwrote the newer customer source: %+v", reloaded)
			}
			routeAfter := ConversationRouteService.GetByConversationID(conversation.ID)
			if routeAfter == nil || routeAfter.RouteStatus != routeBefore.RouteStatus || routeAfter.NeedHumanFollowUp != routeBefore.NeedHumanFollowUp || routeAfter.HandoffReason != routeBefore.HandoffReason || !sameOptionalTime(routeAfter.ManualExpireAt, routeBefore.ManualExpireAt) {
				t.Fatalf("stale transition changed the newer route: before=%+v after=%+v", routeBefore, routeAfter)
			}
		})
	}
}

func sameOptionalTime(left *time.Time, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func TestManualResumeRunOutcomeFindsOlderVerifiedSuccessForSameRequest(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "older-success")
	requestID := manualResumeRequestID(task)
	now := time.Now()
	reply := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      origin.SessionNo,
		RequestID:      requestID,
		ClientMsgID:    "manual-resume-older-success-reply",
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        "房间内有两瓶矿泉水，都是免费的。",
		SeqNo:          origin.SeqNo + 1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(reply).Error; err != nil {
		t.Fatalf("create verified reply: %v", err)
	}
	if err := db.Create(&models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkProtocol, ConversationID: conversation.ID, MessageID: reply.ID,
		Payload: `{}`, SendStatus: string(enums.ChannelMessageOutboxStatusSent), SentAt: &now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create sent Outbox: %v", err)
	}
	createCompletedManualResumeRunLog(t, db, *origin, requestID, reply)
	if err := db.Create(&models.AgentRunLog{
		ConversationID: conversation.ID,
		MessageID:      origin.ID,
		RequestID:      requestID,
		FinalStatus:    "error",
		ErrorMessage:   "later reconciliation attempt failed before commit",
		TraceData:      `{"runtime":{"status":"error","error":{"stage":"generate"}}}`,
		CreatedAt:      now.Add(time.Second),
	}).Error; err != nil {
		t.Fatalf("create newer failed RunLog: %v", err)
	}

	outcome := AIManualResumeTaskService.manualResumeRunOutcome(task, []string{requestID})
	if outcome.State != manualResumeRunCommitted || outcome.RunLogID <= 0 {
		t.Fatalf("a newer failed RunLog must not hide an older verified success: %+v", outcome)
	}
}

func TestManualResumeRunOutcomeFindsVerifiedLegacySuccessAcrossCompatibleRequestIDs(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "legacy-success")
	currentRequestID := manualResumeRequestID(task)
	legacyRequestID := manualResumeLegacyRequestID(task)
	now := time.Now()

	currentReply := createManualResumeRequestBoundMessage(t, db, aiAgent.ID, *conversation, *origin, currentRequestID,
		manualResumeOwnedClientMessageID(currentRequestID, "task", origin.ID), enums.IMMessageTypeText, "处理中", now.Add(time.Second))
	createManualResumeRequestBoundOutbox(t, db, conversation.ID, currentReply.ID, enums.ChannelMessageOutboxStatusPending, now.Add(time.Second))
	createCompletedManualResumeRunLog(t, db, *origin, currentRequestID, currentReply)

	reply := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      origin.SessionNo,
		RequestID:      legacyRequestID,
		ClientMsgID:    "manual-resume-legacy-success-reply",
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        "拖鞋可以到1313对面的洗衣房领取。",
		SeqNo:          currentReply.SeqNo + 1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(reply).Error; err != nil {
		t.Fatalf("create legacy verified reply: %v", err)
	}
	if err := db.Create(&models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkProtocol, ConversationID: conversation.ID, MessageID: reply.ID,
		Payload: `{}`, SendStatus: string(enums.ChannelMessageOutboxStatusSent), SentAt: &now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create legacy sent Outbox: %v", err)
	}
	createCompletedManualResumeRunLog(t, db, *origin, legacyRequestID, reply)

	outcome := AIManualResumeTaskService.manualResumeRunOutcome(task, manualResumeCompatibleRequestIDs(task))
	if outcome.State != manualResumeRunCommitted || outcome.RequestID != legacyRequestID {
		t.Fatalf("a verified legacy success must outrank a current non-terminal outcome: %+v", outcome)
	}
}

func TestManualResumeDeferredSnapshotStopsAtNewerDeliveredCommitDespiteRunLogError(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "delivered-barrier")
	createManualResumeTrace(t, db, *origin, fmt.Sprintf(`{
		"runtime":{"status":"interrupted","input":{"currentTurnSources":[{"ref":"U1","messageId":%d,"messageType":"text","text":"拖鞋去哪里拿"}]},"pipeline":{
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}]},
			"evidenceJudge":{"deferredTaskIds":["task-1"],"tasks":[{"taskId":"task-1","disposition":"no_evidence_handoff"}]}
		},"output":{"finishReason":"human_route_dispatched"}}
	}`, origin.ID))

	requestID := manualResumeRequestID(task)
	now := time.Now()
	reply := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      origin.SessionNo,
		RequestID:      requestID,
		ClientMsgID:    "manual-resume-delivered-barrier-reply",
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        "拖鞋可以到1313对面的洗衣房领取。",
		SeqNo:          origin.SeqNo + 1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(reply).Error; err != nil {
		t.Fatalf("create delivered reply: %v", err)
	}
	if err := db.Create(&models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkProtocol, ConversationID: conversation.ID, MessageID: reply.ID,
		Payload: `{}`, SendStatus: string(enums.ChannelMessageOutboxStatusSent), SentAt: &now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create delivered Outbox: %v", err)
	}
	createCompletedManualResumeRunLog(t, db, *origin, requestID, reply)
	newer := AgentRunLogService.FindOne(sqls.NewCnd().
		Eq("conversation_id", conversation.ID).
		Eq("message_id", origin.ID).
		Eq("request_id", requestID).
		Desc("id"))
	if newer == nil {
		t.Fatal("expected newer committed RunLog")
	}
	if err := db.Model(&models.AgentRunLog{}).Where("id = ?", newer.ID).Updates(map[string]any{
		"final_status":  "error",
		"error_message": "post-commit trace finalization failed",
	}).Error; err != nil {
		t.Fatalf("mark newer RunLog errored after Commit: %v", err)
	}

	snapshot := AIManualResumeTaskService.manualResumeDeferredSnapshotState(task)
	if snapshot.State != manualResumeSnapshotSettled || snapshot.RunLogID != newer.ID || len(snapshot.Tasks) != 0 {
		t.Fatalf("a newer verified delivered Commit must block revival of an older Deferred snapshot: %+v", snapshot)
	}
}

func prepareReadyExternalManualResumeTask(t *testing.T, db *gorm.DB, key string) (*models.AIAgent, *models.Conversation, *models.Message, *models.AIManualResumeTask) {
	t.Helper()
	now := time.Now()
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	channel := &models.Channel{
		Name:        "manual-resume-" + key,
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "manual-resume-" + key,
		AIAgentID:   aiAgent.ID,
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create external channel: %v", err)
	}
	external := welcomeTestExternalUser("manual-resume-" + key)
	conversation, err := ConversationService.Create(external, channel.ID, aiAgent.ID)
	if err != nil {
		t.Fatalf("create external conversation: %v", err)
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "等待门店同事", now); err != nil {
		t.Fatalf("enter store manual route: %v", err)
	}
	origin, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID,
		"manual-resume-"+key+"-origin",
		enums.IMMessageTypeText,
		"拖鞋去哪里拿",
		"",
		external,
		"req-manual-resume-"+key,
	)
	if err != nil {
		t.Fatalf("send origin customer message: %v", err)
	}
	task := AIManualResumeTaskService.latestActiveTask(conversation.ID, []string{aiManualResumeTaskWaiting})
	if task == nil {
		t.Fatal("expected waiting manual resume task")
	}
	readyAt := now.Add(-time.Second)
	if err := db.Model(&models.AIManualResumeTask{}).Where("id = ?", task.ID).Updates(map[string]any{
		"task_status":   aiManualResumeTaskReady,
		"ready_at":      readyAt,
		"next_retry_at": readyAt,
	}).Error; err != nil {
		t.Fatalf("mark manual resume task ready: %v", err)
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatalf("reload ready manual resume task: %v", err)
	}
	return aiAgent, conversation, origin, task
}

func TestManualResumeRunTraceKeepsManualValidatesDeferredTaskOwnership(t *testing.T) {
	base := `{
		"runtime":{"pipeline":{
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}]},
			"evidenceJudge":{"deferredTaskIds":%s,"tasks":[{"taskId":"task-1","disposition":"no_evidence_handoff"}]}
		}}
	}`
	tests := []struct {
		name      string
		deferred  string
		wantKeeps bool
		wantValid bool
	}{
		{name: "valid", deferred: `["task-1"]`, wantKeeps: true, wantValid: true},
		{name: "unknown", deferred: `["task-2"]`, wantKeeps: false, wantValid: false},
		{name: "duplicate", deferred: `["task-1","task-1"]`, wantKeeps: false, wantValid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var projection manualResumeTraceProjection
			if err := json.Unmarshal([]byte(fmt.Sprintf(base, tt.deferred)), &projection); err != nil {
				t.Fatalf("unmarshal projection: %v", err)
			}
			keeps, valid := manualResumeRunTraceKeepsManual(projection)
			if keeps != tt.wantKeeps || valid != tt.wantValid {
				t.Fatalf("keeps=%v valid=%v, want keeps=%v valid=%v", keeps, valid, tt.wantKeeps, tt.wantValid)
			}
		})
	}
}

func TestManualResumeRunTraceKeepsManualRejectsUnboundLegacyDisposition(t *testing.T) {
	var projection manualResumeTraceProjection
	if err := json.Unmarshal([]byte(`{
		"runtime":{"pipeline":{
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"}]},
			"evidenceJudge":{"tasks":[{"taskId":"task-2","disposition":"answer_then_handoff"}]}
		}}
	}`), &projection); err != nil {
		t.Fatalf("unmarshal projection: %v", err)
	}
	if keeps, valid := manualResumeRunTraceKeepsManual(projection); keeps || valid {
		t.Fatalf("a handoff disposition without its TaskPlan must be rejected: keeps=%v valid=%v", keeps, valid)
	}
}

func TestManualResumeStrictCoverageRequiresExactTaskIDsAcrossMergedMessages(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	task := &models.AIManualResumeTask{
		HandoffToken:           "strict-coverage",
		ConversationID:         9901,
		OriginMessageID:        801,
		LatestWaitingMessageID: 801,
	}
	requestID := manualResumeRequestID(task)
	now := time.Now()
	for index, content := range []string{"回答一和回答二", "回答三", "回答四"} {
		message := &models.Message{
			ID:             int64(811 + index),
			ConversationID: task.ConversationID,
			SessionNo:      1,
			RequestID:      requestID,
			ClientMsgID:    fmt.Sprintf("strict-coverage-%d", index+1),
			SenderType:     enums.IMSenderTypeAI,
			MessageType:    enums.IMMessageTypeText,
			Content:        content,
			SeqNo:          int64(index + 1),
			SendStatus:     enums.IMMessageStatusSent,
			SentAt:         &now,
		}
		if err := db.Create(message).Error; err != nil {
			t.Fatalf("create committed message %d: %v", index+1, err)
		}
	}
	trace := fmt.Sprintf(`{
		"runtime":{"pipeline":{"replyPlan":{"taskPlans":[
			{"taskId":"task-1","intent":"hotel_info","outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"},
			{"taskId":"task-2","intent":"hotel_info","outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"},
			{"taskId":"task-3","intent":"hotel_info","outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"},
			{"taskId":"task-4","intent":"hotel_info","outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"}
		]}},"output":{"commitMessages":[
			{"messageId":811,"messageType":"text","content":"回答一和回答二","taskIds":["task-1","task-2"],"status":"sent"},
			{"messageId":812,"messageType":"text","content":"回答三","taskIds":["task-3"],"status":"sent"},
			{"messageId":813,"messageType":"text","content":"回答四","taskIds":["task-4"],"status":"sent"}
		]}}
	}`)
	var projection manualResumeTraceProjection
	if err := json.Unmarshal([]byte(trace), &projection); err != nil {
		t.Fatalf("unmarshal strict projection: %v", err)
	}
	complete, hasVisibleTasks, hasCustomerVisibleCommit := AIManualResumeTaskService.manualResumeRunTraceCommitCoverage(task, requestID, projection)
	if !complete || !hasVisibleTasks || !hasCustomerVisibleCommit {
		t.Fatalf("all four Task IDs merged into three real messages must be complete: complete=%v visible=%v committed=%v", complete, hasVisibleTasks, hasCustomerVisibleCommit)
	}
	projection.Runtime.Output.CommitMessages[0].TaskIDs = nil
	complete, _, _ = AIManualResumeTaskService.manualResumeRunTraceCommitCoverage(task, requestID, projection)
	if complete {
		t.Fatal("a source-bound resume cannot use message count as a substitute for missing taskIds[]")
	}
	projection.Runtime.Output.CommitMessages[0].TaskID = "task-1"
	if AIManualResumeTaskService.manualResumeTraceHasCustomerVisibleTaskCommit(task, requestID, projection, "task-1") {
		t.Fatal("a source-bound answer_then_handoff cannot use legacy taskId as exact Task coverage")
	}
}

func TestManualResumeStrictCoverageAcceptsDeliveredResourceTextFallback(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	task := &models.AIManualResumeTask{
		HandoffToken:           "resource-text-fallback",
		ConversationID:         9902,
		OriginMessageID:        901,
		LatestWaitingMessageID: 901,
	}
	requestID := manualResumeRequestID(task)
	now := time.Now()
	message := &models.Message{
		ID: 911, ConversationID: task.ConversationID, SessionNo: 1, RequestID: requestID,
		ClientMsgID: "resource-text-fallback", SenderType: enums.IMSenderTypeAI,
		MessageType: enums.IMMessageTypeText, Content: "入住小程序入口：智慧入住。",
		SeqNo: 1, SendStatus: enums.IMMessageStatusSent, SentAt: &now,
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create fallback message: %v", err)
	}
	trace := `{
		"runtime":{"pipeline":{"replyPlan":{"taskPlans":[
			{"taskId":"task-mini","intent":"hotel_variable","subIntent":"mini_program","outputKind":"resource","needsResource":true,"resourceAction":"send_miniprogram","output":"structured_resource_commit"}
		]}},"output":{"commitMessages":[
			{"messageId":911,"messageType":"text","fallbackResourceType":"mini_program","content":"入住小程序入口：智慧入住。","taskIds":["task-mini"],"status":"sent"}
		]}}
	}`
	var projection manualResumeTraceProjection
	if err := json.Unmarshal([]byte(trace), &projection); err != nil {
		t.Fatalf("unmarshal fallback projection: %v", err)
	}
	complete, hasVisibleTasks, hasCustomerVisibleCommit := AIManualResumeTaskService.manualResumeRunTraceCommitCoverage(task, requestID, projection)
	if !complete || !hasVisibleTasks || !hasCustomerVisibleCommit {
		t.Fatalf("delivered resource text fallback must complete its resource Task: complete=%v visible=%v committed=%v", complete, hasVisibleTasks, hasCustomerVisibleCommit)
	}
}

func TestManualResumeStrictCoverageAcceptsDeliveredMergedResourceTextFallbacks(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	task := &models.AIManualResumeTask{
		HandoffToken:           "merged-resource-text-fallbacks",
		ConversationID:         9902,
		OriginMessageID:        901,
		LatestWaitingMessageID: 901,
	}
	requestID := manualResumeRequestID(task)
	now := time.Now()
	message := &models.Message{
		ID: 912, ConversationID: task.ConversationID, SessionNo: 1, RequestID: requestID,
		ClientMsgID: "merged-resource-text-fallbacks", SenderType: enums.IMSenderTypeAI,
		MessageType: enums.IMMessageTypeText, Content: "酒店地址：测试路1号。\n\n入住小程序入口：智慧入住。\n\n门店电话：12345678。",
		SeqNo: 1, SendStatus: enums.IMMessageStatusSent, SentAt: &now,
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create merged fallback message: %v", err)
	}
	trace := `{
		"runtime":{"pipeline":{"replyPlan":{"taskPlans":[
			{"taskId":"task-location","intent":"hotel_variable","subIntent":"location","outputKind":"resource","needsResource":true,"resourceAction":"provide_location","output":"structured_resource_commit"},
			{"taskId":"task-mini","intent":"hotel_variable","subIntent":"mini_program","outputKind":"resource","needsResource":true,"resourceAction":"send_miniprogram","output":"structured_resource_commit"},
			{"taskId":"task-phone","intent":"hotel_variable","subIntent":"phone","outputKind":"resource","needsResource":true,"resourceAction":"provide_phone","output":"structured_resource_commit"}
		]}},"output":{"commitMessages":[
			{"messageId":912,"messageType":"text","fallbackResourceType":"location","fallbackResourceTypes":["location","mini_program","phone"],"content":"酒店地址：测试路1号。\n\n入住小程序入口：智慧入住。\n\n门店电话：12345678。","taskIds":["task-location","task-mini","task-phone"],"status":"sent"}
		]}}
	}`
	var projection manualResumeTraceProjection
	if err := json.Unmarshal([]byte(trace), &projection); err != nil {
		t.Fatalf("unmarshal merged fallback projection: %v", err)
	}
	complete, hasVisibleTasks, hasCustomerVisibleCommit := AIManualResumeTaskService.manualResumeRunTraceCommitCoverage(task, requestID, projection)
	if !complete || !hasVisibleTasks || !hasCustomerVisibleCommit {
		t.Fatalf("one delivered fallback message must cover every merged resource Task: complete=%v visible=%v committed=%v", complete, hasVisibleTasks, hasCustomerVisibleCommit)
	}
}

func TestManualResumeMessageReachedCustomerRequiresSentExternalOutbox(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	if err := db.AutoMigrate(&models.Channel{}, &models.Conversation{}, &models.ChannelMessageOutbox{}); err != nil {
		t.Fatalf("migrate external visibility fixtures: %v", err)
	}
	channel := &models.Channel{ID: 1, Name: "企微员工号", ChannelType: enums.ChannelTypeWxWorkProtocol, ChannelID: "manual-resume-visible"}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	conversation := &models.Conversation{ID: 9903, ChannelID: channel.ID}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	now := time.Now()
	message := &models.Message{
		ID: 912, ConversationID: conversation.ID, SessionNo: 1, RequestID: "manual-resume-visible",
		ClientMsgID: "manual-resume-visible", SenderType: enums.IMSenderTypeAI,
		MessageType: enums.IMMessageTypeText, Content: "早餐时间是7:00-9:30。",
		SeqNo: 1, SendStatus: enums.IMMessageStatusSent, SentAt: &now,
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create message: %v", err)
	}
	if manualResumeMessageReachedCustomer(message) {
		t.Fatal("an external message without an Outbox record must not count as customer-visible")
	}
	outbox := &models.ChannelMessageOutbox{
		ChannelType: channel.ChannelType, ConversationID: conversation.ID, MessageID: message.ID,
		Payload: `{}`, SendStatus: string(enums.ChannelMessageOutboxStatusPending),
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	for _, test := range []struct {
		status enums.ChannelMessageOutboxStatus
		want   bool
	}{
		{status: enums.ChannelMessageOutboxStatusPending},
		{status: enums.ChannelMessageOutboxStatusSending},
		{status: enums.ChannelMessageOutboxStatusFailed},
		{status: enums.ChannelMessageOutboxStatusCancelled},
		{status: enums.ChannelMessageOutboxStatusSent, want: true},
	} {
		if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Update("send_status", string(test.status)).Error; err != nil {
			t.Fatalf("set outbox status %q: %v", test.status, err)
		}
		if got := manualResumeMessageReachedCustomer(message); got != test.want {
			t.Fatalf("outbox status %q customer-visible=%v want %v", test.status, got, test.want)
		}
	}
}

func TestManualResumeStrictOutcomeRejectsUnboundNonTextCommit(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	task := &models.AIManualResumeTask{
		HandoffToken:           "strict-unbound-resource",
		ConversationID:         9902,
		OriginMessageID:        901,
		LatestWaitingMessageID: 901,
	}
	requestID := manualResumeRequestID(task)
	now := time.Now()
	message := &models.Message{
		ID:             911,
		ConversationID: task.ConversationID,
		SessionNo:      1,
		RequestID:      requestID,
		ClientMsgID:    "strict-unbound-resource",
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeImage,
		Content:        "asset-911",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create unbound non-text message: %v", err)
	}
	trace := `{"runtime":{"status":"completed","pipeline":{"replyPlan":{"taskPlans":[]}},"output":{"finishReason":"committed_reply","commitMessages":[{"messageId":911,"messageType":"image","content":"asset-911","status":"sent"}]}}}`
	if err := db.Create(&models.AgentRunLog{
		ConversationID: task.ConversationID,
		MessageID:      task.OriginMessageID,
		RequestID:      requestID,
		FinalStatus:    "completed",
		TraceData:      trace,
		CreatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create strict outcome RunLog: %v", err)
	}
	if outcome := AIManualResumeTaskService.manualResumeRunOutcome(task, []string{requestID}); outcome.State != manualResumeRunUnavailable {
		t.Fatalf("an unbound non-text commit must not restore AI: %+v", outcome)
	}
}

func TestManualResumeSourcesSelectsOnlyDeferredTaskFromSinglePhysicalMessage(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 101, ConversationID: 9001, SessionNo: 1, ClientMsgID: "single-source", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: "早餐几点，拖鞋去哪里拿？",
	})
	now := time.Now()
	answer := createManualResumeSourceMessage(t, db, models.Message{
		ID: 102, ConversationID: origin.ConversationID, SessionNo: origin.SessionNo, ClientMsgID: "single-source-answer", SeqNo: 2,
		RequestID: "trace", SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText,
		Content: "早餐时间是7:00-9:30。", SendStatus: enums.IMMessageStatusSent, SentAt: &now,
	})
	createManualResumeTrace(t, db, origin, fmt.Sprintf(`{
		"runtime":{"status":"completed","input":{"currentTurnSources":[
			{"ref":"U1","messageId":101,"messageType":"text","text":"早餐几点，拖鞋去哪里拿？"}
		]},"pipeline":{
			"replyPlan":{"taskPlans":[
				{"taskId":"task-1","intent":"hotel_info","subIntent":"breakfast","objective":"time","relationToPrevious":"independent","resolutionState":"clear","originalText":"早餐几点","resolvedText":"早餐几点","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"},
				{"taskId":"task-2","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","entities":[{"text":"拖鞋","type":"supply"}],"originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff","missingAspects":["location"]}
			]},
			"evidenceJudge":{"deferredTaskIds":["task-2"],"tasks":[{"taskId":"task-1","disposition":"answer"},{"taskId":"task-2","disposition":"no_evidence_handoff"}]}
		},"output":{"finishReason":"completed","commitMessages":[{"messageId":%d,"messageType":"text","content":"早餐时间是7:00-9:30。","taskIds":["task-1"],"status":"sent"}]}}
	}`, answer.ID))

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	sources := AIManualResumeTaskService.manualResumeSources(task, []models.Message{origin})
	if len(sources) != 1 || sources[0].MessageID != origin.ID {
		t.Fatalf("expected one source bound to the original physical message, got %#v", sources)
	}
	if sources[0].Text != origin.Content {
		t.Fatalf("resume source must preserve the physical message while frozen tasks select the deferred sibling: %#v", sources[0])
	}
	snapshot, ok := AIManualResumeTaskService.manualResumeExecutionSnapshot(task, []models.Message{origin})
	if !ok || len(snapshot.FrozenTasks) != 1 || snapshot.FrozenTasks[0].TaskID != "task-2" {
		t.Fatalf("only the deferred TaskPlan must be frozen for resume, got %#v", snapshot)
	}
	frozen := snapshot.FrozenTasks[0]
	if snapshot.ContractMode != replyruntime.ManualResumeContractV2 || !snapshot.SourcesValidated ||
		len(frozen.Entities) != 1 || frozen.Entities[0].Text != "拖鞋" || !frozen.NeedsKnowledge ||
		frozen.OutputKind != "handoff" || frozen.ReplyRequired || frozen.Output != "deferred_knowledge_handoff" ||
		strings.Join(frozen.MissingAspects, ",") != "location" || snapshot.Sources[0].MessageID != origin.ID {
		t.Fatalf("V2 resume snapshot must preserve source and frozen task execution metadata: %#v", snapshot)
	}
}

func TestManualResumeSourcesGroupsDeferredTasksByPhysicalMessage(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 201, ConversationID: 9002, SessionNo: 1, ClientMsgID: "grouped-source", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: "拖鞋去哪里拿，牙刷去哪里拿？",
	})
	createManualResumeTrace(t, db, origin, `{
		"runtime":{"input":{"currentTurnSources":[
			{"ref":"U1","messageId":201,"messageType":"text","text":"拖鞋去哪里拿，牙刷去哪里拿？"}
		]},"pipeline":{
			"replyPlan":{"taskPlans":[
				{"taskId":"task-1","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"},
				{"taskId":"task-2","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"牙刷去哪里拿","resolvedText":"牙刷去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}
			]},
			"evidenceJudge":{"deferredTaskIds":["task-1","task-2"]}
		}}
	}`)

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	sources := AIManualResumeTaskService.manualResumeSources(task, []models.Message{origin})
	if len(sources) != 1 || sources[0].MessageID != origin.ID {
		t.Fatalf("two deferred tasks from one physical message must share one source, got %#v", sources)
	}
	if sources[0].Text != origin.Content {
		t.Fatalf("two frozen tasks must not duplicate or rewrite their one physical source, got %q", sources[0].Text)
	}
	snapshot, ok := AIManualResumeTaskService.manualResumeExecutionSnapshot(task, []models.Message{origin})
	if !ok || len(snapshot.FrozenTasks) != 2 || len(snapshot.Sources) != 1 {
		t.Fatalf("two deferred tasks from one message must remain two frozen tasks over one source: %#v", snapshot)
	}
}

func TestManualResumeMixedTraceFreezesOnlyRecoverableKnowledgeTask(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 221, ConversationID: 9022, SessionNo: 1, ClientMsgID: "mixed-deferred-source", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: "拖鞋去哪里拿，机器人能送到房间吗，转人工",
	})
	now := time.Now()
	partialReply := createManualResumeSourceMessage(t, db, models.Message{
		ConversationID: origin.ConversationID, SessionNo: 1, RequestID: "trace",
		ClientMsgID: "mixed-deferred-partial", SeqNo: 2, SenderType: enums.IMSenderTypeAI,
		MessageType: enums.IMMessageTypeText, Content: "门店有外卖机器人，但能否送到房间需要同事确认。",
		SendStatus: enums.IMMessageStatusSent, SentAt: &now,
	})
	createManualResumeTrace(t, db, origin, fmt.Sprintf(`{
		"runtime":{"status":"completed","input":{"currentTurnSources":[
			{"ref":"U1","messageId":221,"messageType":"text","text":"拖鞋去哪里拿，机器人能送到房间吗，转人工"}
		]},"pipeline":{
			"replyPlan":{"taskPlans":[
				{"taskId":"task-deferred","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff","missingAspects":["location"]},
				{"taskId":"task-partial","intent":"hotel_info","subIntent":"delivery_robot","objective":"compound_information","relationToPrevious":"independent","resolutionState":"clear","originalText":"机器人能送到房间吗","resolvedText":"机器人能送到房间吗","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply","missingAspects":["scope"]},
				{"taskId":"task-explicit","intent":"human_complaint_risk","subIntent":"explicit_handoff","objective":"action_request","relationToPrevious":"independent","resolutionState":"clear","originalText":"转人工","resolvedText":"转人工","sourceRefs":["U1"],"needsHumanRoute":true,"outputKind":"handoff","replyRequired":false,"output":"human_route_confirmation_or_dispatch"}
			]},
			"evidenceJudge":{"deferredTaskIds":["task-deferred","task-partial","task-explicit"],"tasks":[
				{"taskId":"task-partial","disposition":"answer_then_handoff"}
			]}
		},"output":{"finishReason":"completed","commitMessages":[
			{"messageId":%d,"messageType":"text","content":"门店有外卖机器人，但能否送到房间需要同事确认。","taskIds":["task-partial"],"status":"sent"}
		]}}
	}`, partialReply.ID))

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	snapshot, state := AIManualResumeTaskService.manualResumeExecutionSnapshotState(task, []models.Message{origin})
	if state != manualResumeSnapshotRecoverable {
		t.Fatalf("mixed trace with one true deferred knowledge task must remain recoverable, state=%q snapshot=%#v", state, snapshot)
	}
	if len(snapshot.FrozenTasks) != 1 || snapshot.FrozenTasks[0].TaskID != "task-deferred" {
		t.Fatalf("only the true deferred knowledge task may be frozen, got %#v", snapshot.FrozenTasks)
	}
	if len(snapshot.Sources) != 1 || snapshot.Sources[0].MessageID != origin.ID {
		t.Fatalf("mixed trace must retain its one physical customer source, got %#v", snapshot.Sources)
	}
}

func TestManualResumeOriginalDeferredRunRequiresStrictSiblingDeliveryCoverage(t *testing.T) {
	tests := []struct {
		name           string
		outboxStatus   enums.ChannelMessageOutboxStatus
		lastError      string
		staleSending   bool
		includeTaskIDs bool
		wantState      manualResumeSnapshotState
		wantTasks      int
	}{
		{
			name:           "delivered sibling keeps only deferred task",
			outboxStatus:   enums.ChannelMessageOutboxStatusSent,
			includeTaskIDs: true,
			wantState:      manualResumeSnapshotRecoverable,
			wantTasks:      1,
		},
		{
			name:           "pending sibling waits without rerun",
			outboxStatus:   enums.ChannelMessageOutboxStatusPending,
			includeTaskIDs: true,
			wantState:      manualResumeSnapshotDeliveryPending,
			wantTasks:      1,
		},
		{
			name:           "stale sending sibling requires human review",
			outboxStatus:   enums.ChannelMessageOutboxStatusSending,
			staleSending:   true,
			includeTaskIDs: true,
			wantState:      manualResumeSnapshotDeliveryUncertain,
			wantTasks:      1,
		},
		{
			name:           "terminal failed sibling remains a delivery failure",
			outboxStatus:   enums.ChannelMessageOutboxStatusFailed,
			includeTaskIDs: true,
			wantState:      manualResumeSnapshotDeliveryFailed,
			wantTasks:      1,
		},
		{
			name:           "cancelled sibling fails closed",
			outboxStatus:   enums.ChannelMessageOutboxStatusCancelled,
			lastError:      "cancelled after employee reply",
			includeTaskIDs: true,
			wantState:      manualResumeSnapshotUnavailable,
		},
		{
			name:           "legacy commit without task ownership fails closed",
			outboxStatus:   enums.ChannelMessageOutboxStatusSent,
			includeTaskIDs: false,
			wantState:      manualResumeSnapshotUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupMessageWelcomeTestDB(t)
			aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "original-deferred-"+strings.ReplaceAll(tt.name, " ", "-"))
			origin.Content = "酒店有拖鞋吗，拖鞋去哪里拿"
			if err := db.Model(&models.Message{}).Where("id = ?", origin.ID).Update("content", origin.Content).Error; err != nil {
				t.Fatalf("update original multi-task content: %v", err)
			}
			now := time.Now()
			reply := &models.Message{
				ConversationID: conversation.ID,
				SessionNo:      origin.SessionNo,
				RequestID:      "trace",
				ClientMsgID:    "original-deferred-sibling-" + strings.ReplaceAll(tt.name, " ", "-"),
				SenderType:     enums.IMSenderTypeAI,
				SenderID:       aiAgent.ID,
				MessageType:    enums.IMMessageTypeText,
				Content:        "酒店有拖鞋。",
				SeqNo:          origin.SeqNo + 1,
				SendStatus:     enums.IMMessageStatusSent,
				SentAt:         &now,
				AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
			}
			if err := db.Create(reply).Error; err != nil {
				t.Fatalf("create original sibling reply: %v", err)
			}
			updatedAt := now
			if tt.staleSending {
				updatedAt = now.Add(-aiManualResumeDeliveryUncertainAfter - time.Second)
			}
			outbox := &models.ChannelMessageOutbox{
				ChannelType:    enums.ChannelTypeWxWorkProtocol,
				ConversationID: conversation.ID,
				MessageID:      reply.ID,
				Payload:        `{}`,
				SendStatus:     string(tt.outboxStatus),
				LastError:      tt.lastError,
				AuditFields:    models.AuditFields{CreatedAt: updatedAt, UpdatedAt: updatedAt},
			}
			if tt.outboxStatus == enums.ChannelMessageOutboxStatusSent {
				outbox.SentAt = &now
			}
			if err := db.Create(outbox).Error; err != nil {
				t.Fatalf("create original sibling Outbox: %v", err)
			}
			taskIDs := ""
			if tt.includeTaskIDs {
				taskIDs = `,"taskIds":["task-answer"]`
			}
			createManualResumeTrace(t, db, *origin, fmt.Sprintf(`{
				"runtime":{"status":"completed","input":{"currentTurnSources":[{"ref":"U1","messageId":%d,"messageType":"text","text":"酒店有拖鞋吗，拖鞋去哪里拿"}]},"pipeline":{
					"replyPlan":{"taskPlans":[
						{"taskId":"task-answer","intent":"hotel_info","subIntent":"supplies","objective":"existence","relationToPrevious":"independent","resolutionState":"clear","originalText":"酒店有拖鞋吗","resolvedText":"酒店有拖鞋吗","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"},
						{"taskId":"task-deferred","intent":"service_request","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}
					]},
					"evidenceJudge":{"deferredTaskIds":["task-deferred"],"tasks":[{"taskId":"task-answer","disposition":"answer"},{"taskId":"task-deferred","disposition":"no_evidence_handoff"}]}
				},"output":{"finishReason":"completed","commitMessages":[{"messageId":%d,"messageType":"text","content":"酒店有拖鞋。"%s,"status":"sent"}]}}
			}`, origin.ID, reply.ID, taskIDs))

			snapshot := AIManualResumeTaskService.manualResumeDeferredSnapshotState(task)
			if snapshot.State != tt.wantState || len(snapshot.Tasks) != tt.wantTasks {
				t.Fatalf("state=%q tasks=%#v, want state=%q task count=%d", snapshot.State, snapshot.Tasks, tt.wantState, tt.wantTasks)
			}
		})
	}
}

func TestManualResumeOriginalDeferredPendingDeliveryDoesNotRerunModel(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "original-deferred-pending-process")
	now := time.Now()
	reply := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      origin.SessionNo,
		RequestID:      "trace",
		ClientMsgID:    "original-deferred-pending-reply",
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        "酒店有拖鞋。",
		SeqNo:          origin.SeqNo + 1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(reply).Error; err != nil {
		t.Fatalf("create pending original sibling reply: %v", err)
	}
	if err := db.Create(&models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkProtocol, ConversationID: conversation.ID, MessageID: reply.ID,
		Payload: `{}`, SendStatus: string(enums.ChannelMessageOutboxStatusPending),
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create pending original sibling Outbox: %v", err)
	}
	createManualResumeTrace(t, db, *origin, fmt.Sprintf(`{
		"runtime":{"status":"completed","input":{"currentTurnSources":[{"ref":"U1","messageId":%d,"messageType":"text","text":"拖鞋去哪里拿"}]},"pipeline":{
			"replyPlan":{"taskPlans":[
				{"taskId":"task-answer","intent":"hotel_info","subIntent":"supplies","objective":"existence","relationToPrevious":"independent","resolutionState":"clear","originalText":"酒店有拖鞋吗","resolvedText":"酒店有拖鞋吗","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"},
				{"taskId":"task-deferred","intent":"service_request","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}
			]},
			"evidenceJudge":{"deferredTaskIds":["task-deferred"],"tasks":[{"taskId":"task-answer","disposition":"answer"},{"taskId":"task-deferred","disposition":"no_evidence_handoff"}]}
		},"output":{"finishReason":"completed","commitMessages":[{"messageId":%d,"messageType":"text","content":"酒店有拖鞋。","taskIds":["task-answer"],"status":"sent"}]}}
	}`, origin.ID, reply.ID))

	previousHook := TriggerAIReplySyncHook
	defer func() { TriggerAIReplySyncHook = previousHook }()
	hookCalls := 0
	TriggerAIReplySyncHook = func(context.Context, models.Conversation, models.Message) error {
		hookCalls++
		return nil
	}
	if !AIManualResumeTaskService.processOne(*task, now) {
		t.Fatal("expected original pending delivery to be reconciled")
	}
	if hookCalls != 0 {
		t.Fatalf("pending original sibling delivery must not rerun the model, hook calls=%d", hookCalls)
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatalf("reload pending original task: %v", err)
	}
	if task.TaskStatus != aiManualResumeTaskRetry || task.RetryCount != 0 || task.LastError != aiManualResumeAwaitingDeliveryMarker {
		t.Fatalf("pending original delivery must wait without consuming a model retry: %+v", task)
	}
}

func TestManualResumeOriginalDeferredFailedDeliveryDoesNotRerunModel(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "original-deferred-failed-process")
	now := time.Now()
	reply := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      origin.SessionNo,
		RequestID:      "trace",
		ClientMsgID:    "original-deferred-failed-reply",
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        "酒店有拖鞋。",
		SeqNo:          origin.SeqNo + 1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(reply).Error; err != nil {
		t.Fatalf("create failed original sibling reply: %v", err)
	}
	if err := db.Create(&models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkProtocol, ConversationID: conversation.ID, MessageID: reply.ID,
		Payload: `{}`, SendStatus: string(enums.ChannelMessageOutboxStatusFailed), LastError: "channel retry limit reached",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create failed original sibling Outbox: %v", err)
	}
	createManualResumeTrace(t, db, *origin, fmt.Sprintf(`{
		"runtime":{"status":"completed","input":{"currentTurnSources":[{"ref":"U1","messageId":%d,"messageType":"text","text":"拖鞋去哪里拿"}]},"pipeline":{
			"replyPlan":{"taskPlans":[
				{"taskId":"task-answer","intent":"hotel_info","subIntent":"supplies","objective":"existence","relationToPrevious":"independent","resolutionState":"clear","originalText":"酒店有拖鞋吗","resolvedText":"酒店有拖鞋吗","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"},
				{"taskId":"task-deferred","intent":"service_request","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}
			]},
			"evidenceJudge":{"deferredTaskIds":["task-deferred"],"tasks":[{"taskId":"task-answer","disposition":"answer"},{"taskId":"task-deferred","disposition":"no_evidence_handoff"}]}
		},"output":{"finishReason":"completed","commitMessages":[{"messageId":%d,"messageType":"text","content":"酒店有拖鞋。","taskIds":["task-answer"],"status":"sent"}]}}
	}`, origin.ID, reply.ID))

	previousHook := TriggerAIReplySyncHook
	defer func() { TriggerAIReplySyncHook = previousHook }()
	hookCalls := 0
	TriggerAIReplySyncHook = func(context.Context, models.Conversation, models.Message) error {
		hookCalls++
		return nil
	}
	if !AIManualResumeTaskService.processOne(*task, now) {
		t.Fatal("expected original failed delivery to be reconciled")
	}
	if hookCalls != 0 {
		t.Fatalf("failed original sibling delivery must not rerun the model, hook calls=%d", hookCalls)
	}
	reloadedTask := &models.AIManualResumeTask{}
	if err := db.First(reloadedTask, task.ID).Error; err != nil {
		t.Fatalf("reload failed original task: %v", err)
	}
	if reloadedTask.TaskStatus != aiManualResumeTaskFailed || reloadedTask.RetryCount != 0 || reloadedTask.NextRetryAt != nil || reloadedTask.LastError != aiManualResumeDeliveryFailedMarker {
		t.Fatalf("failed original delivery must stop without consuming a model retry: %+v", reloadedTask)
	}
	state := ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || !state.NeedHumanFollowUp || state.ManualExpireAt != nil {
		t.Fatalf("failed original delivery must keep the conversation with a human: %+v", state)
	}
}

func TestManualResumeKnowledgeHandoffSnapshotStates(t *testing.T) {
	tests := []struct {
		name      string
		trace     string
		wantState manualResumeSnapshotState
		wantTasks int
	}{
		{
			name: "no evidence remains recoverable",
			trace: `{
				"runtime":{"status":"completed","input":{"currentTurnSources":[{"ref":"U1","messageId":231,"messageType":"text","text":"拖鞋去哪里拿"}]},"pipeline":{
					"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}]},
					"evidenceJudge":{"deferredTaskIds":["task-1"],"tasks":[{"taskId":"task-1","disposition":"no_evidence_handoff"}]}
				},"output":{"finishReason":"human_route_dispatched"}}
			}`,
			wantState: manualResumeSnapshotRecoverable,
			wantTasks: 1,
		},
		{
			name: "knowledge direct handoff with deferred id is settled",
			trace: `{
				"runtime":{"status":"completed","input":{"currentTurnSources":[{"ref":"U1","messageId":231,"messageType":"text","text":"前台几点有人"}]},"pipeline":{
					"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","subIntent":"front_desk_hours","objective":"time","relationToPrevious":"independent","resolutionState":"clear","originalText":"前台几点有人","resolvedText":"前台几点有人","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}]},
					"evidenceJudge":{"deferredTaskIds":["task-1"],"tasks":[{"taskId":"task-1","disposition":"knowledge_direct_handoff"}]}
				},"output":{"finishReason":"handoff_directive_dispatched"}}
			}`,
			wantState: manualResumeSnapshotSettled,
		},
		{
			name: "knowledge direct handoff without deferred id is settled",
			trace: `{
				"runtime":{"status":"completed","input":{"currentTurnSources":[{"ref":"U1","messageId":231,"messageType":"text","text":"前台几点有人"}]},"pipeline":{
					"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","subIntent":"front_desk_hours","objective":"time","relationToPrevious":"independent","resolutionState":"clear","originalText":"前台几点有人","resolvedText":"前台几点有人","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"human_route_confirmation_or_dispatch"}]},
					"evidenceJudge":{"tasks":[{"taskId":"task-1","disposition":"knowledge_direct_handoff"}]}
				},"output":{"finishReason":"handoff_directive_dispatched"}}
			}`,
			wantState: manualResumeSnapshotSettled,
		},
		{
			name: "answer then handoff without a committed answer is unavailable",
			trace: `{
				"runtime":{"status":"completed","input":{"currentTurnSources":[{"ref":"U1","messageId":231,"messageType":"text","text":"机器人能送到房间吗"}]},"pipeline":{
					"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","subIntent":"delivery_robot","objective":"compound_information","relationToPrevious":"independent","resolutionState":"clear","originalText":"机器人能送到房间吗","resolvedText":"机器人能送到房间吗","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply","missingAspects":["scope"]}]},
					"evidenceJudge":{"deferredTaskIds":["task-1"],"tasks":[{"taskId":"task-1","disposition":"answer_then_handoff"}]}
				},"output":{"finishReason":"completed"}}
			}`,
			wantState: manualResumeSnapshotUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupManualResumeSourceTestDB(t)
			origin := createManualResumeSourceMessage(t, db, models.Message{
				ID: 231, ConversationID: 9031, SessionNo: 1, ClientMsgID: "handoff-state", SeqNo: 1,
				SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "前台几点有人",
			})
			createManualResumeTrace(t, db, origin, tt.trace)
			task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
			snapshot, state := AIManualResumeTaskService.manualResumeExecutionSnapshotState(task, []models.Message{origin})
			if state != tt.wantState || len(snapshot.FrozenTasks) != tt.wantTasks {
				t.Fatalf("state=%q tasks=%#v, want state=%q task count=%d", state, snapshot.FrozenTasks, tt.wantState, tt.wantTasks)
			}
		})
	}
}

func TestManualResumeSnapshotRejectsDuplicateJudgeDispositions(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 236, ConversationID: 9036, SessionNo: 1, ClientMsgID: "duplicate-dispositions", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "拖鞋去哪里拿",
	})
	createManualResumeTrace(t, db, origin, `{
		"runtime":{"status":"completed","input":{"currentTurnSources":[{"ref":"U1","messageId":236,"messageType":"text","text":"拖鞋去哪里拿"}]},"pipeline":{
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}]},
			"evidenceJudge":{"deferredTaskIds":["task-1"],"tasks":[{"taskId":"task-1","disposition":"no_evidence_handoff"},{"taskId":"task-1","disposition":"knowledge_direct_handoff"}]}
		},"output":{"finishReason":"human_route_dispatched"}}
	}`)

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	snapshot, state := AIManualResumeTaskService.manualResumeExecutionSnapshotState(task, []models.Message{origin})
	if state != manualResumeSnapshotUnavailable || len(snapshot.FrozenTasks) != 0 {
		t.Fatalf("duplicate Judge dispositions must invalidate the snapshot: state=%q snapshot=%#v", state, snapshot)
	}
}

func TestManualResumeAnswerThenHandoffRequiresExactCommittedTask(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 237, ConversationID: 9037, SessionNo: 1, ClientMsgID: "answer-then-handoff-origin", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "机器人能送到房间吗，拖鞋没了",
	})
	task := &models.AIManualResumeTask{
		HandoffToken:           "legacy-answer-then-handoff",
		ConversationID:         origin.ConversationID,
		OriginMessageID:        origin.ID,
		LatestWaitingMessageID: origin.ID,
	}
	requestID := manualResumeLegacyRequestID(task)
	now := time.Now()
	wrongTaskReply := createManualResumeSourceMessage(t, db, models.Message{
		ConversationID: origin.ConversationID,
		SessionNo:      1,
		RequestID:      requestID,
		ClientMsgID:    "answer-then-handoff-wrong-task",
		SeqNo:          2,
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeText,
		Content:        "拖鞋的问题需要同事处理。",
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
	})
	trace := fmt.Sprintf(`{
		"runtime":{"status":"completed","input":{"currentTurnSources":[{"ref":"U1","messageId":%d,"messageType":"text","text":"机器人能送到房间吗，拖鞋没了"}]},"pipeline":{
			"replyPlan":{"taskPlans":[
				{"taskId":"task-1","intent":"hotel_info","subIntent":"delivery_robot","objective":"compound_information","relationToPrevious":"independent","resolutionState":"clear","originalText":"机器人能送到房间吗","resolvedText":"机器人能送到房间吗","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply","missingAspects":["scope"]},
				{"taskId":"task-2","intent":"service_request","subIntent":"supplies_self_help","objective":"method","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋没了","resolvedText":"拖鞋没了","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}
			]},
			"evidenceJudge":{"deferredTaskIds":["task-1","task-2"],"tasks":[{"taskId":"task-1","disposition":"answer_then_handoff"},{"taskId":"task-2","disposition":"no_evidence_handoff"}]}
		},"output":{"finishReason":"completed","commitMessages":[{"messageId":%d,"messageType":"text","content":"拖鞋的问题需要同事处理。","taskIds":["task-2"],"status":"sent"}]}}
	}`, origin.ID, wrongTaskReply.ID)
	if err := db.Create(&models.AgentRunLog{
		ConversationID: origin.ConversationID,
		MessageID:      origin.ID,
		RequestID:      requestID,
		FinalStatus:    "completed",
		TraceData:      trace,
		CreatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create answer-then-handoff RunLog: %v", err)
	}

	snapshot, state := AIManualResumeTaskService.manualResumeExecutionSnapshotState(task, []models.Message{origin})
	if state != manualResumeSnapshotUnavailable || len(snapshot.FrozenTasks) != 0 {
		t.Fatalf("a different Task commit must not settle answer_then_handoff: state=%q snapshot=%#v", state, snapshot)
	}
	if outcome := AIManualResumeTaskService.manualResumeRunOutcome(task, []string{requestID}); outcome.State != manualResumeRunUnavailable {
		t.Fatalf("legacy message-count fallback must not settle answer_then_handoff: %+v", outcome)
	}
}

func TestManualResumeExternalCommitRequiresSentOutbox(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	now := time.Now()
	channel := &models.Channel{
		Name:        "manual-resume-outbox",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "manual-resume-outbox",
		AIAgentID:   aiAgent.ID,
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create external channel: %v", err)
	}
	conversation, err := ConversationService.Create(welcomeTestExternalUser("manual-resume-outbox"), channel.ID, aiAgent.ID)
	if err != nil {
		t.Fatalf("create external conversation: %v", err)
	}
	reply := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      1,
		RequestID:      "manual_resume_outbox",
		ClientMsgID:    "manual-resume-outbox-reply",
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        "门店有外卖机器人。",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(reply).Error; err != nil {
		t.Fatalf("create committed reply: %v", err)
	}
	commit := manualResumeCommitMessage{
		MessageID:   reply.ID,
		MessageType: string(reply.MessageType),
		Content:     reply.Content,
		TaskIDs:     []string{"task-1"},
		Status:      "sent",
	}
	if manualResumeAIMessageMatchesTrace(conversation.ID, reply.RequestID, commit, true) {
		t.Fatal("an external message without an Outbox must not count as customer-visible")
	}
	outbox := &models.ChannelMessageOutbox{
		ChannelType:    channel.ChannelType,
		ConversationID: conversation.ID,
		MessageID:      reply.ID,
		Payload:        `{}`,
		SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create outbound task: %v", err)
	}
	for _, tt := range []struct {
		name        string
		status      enums.ChannelMessageOutboxStatus
		nextRetryAt *time.Time
		lastError   string
		want        bool
		wantState   manualResumeMessageDeliveryState
	}{
		{name: "pending", status: enums.ChannelMessageOutboxStatusPending, wantState: manualResumeMessageDeliveryPending},
		{name: "sending", status: enums.ChannelMessageOutboxStatusSending, wantState: manualResumeMessageDeliveryPending},
		{name: "sent", status: enums.ChannelMessageOutboxStatusSent, want: true, wantState: manualResumeMessageDeliverySent},
		{name: "retryable_failed", status: enums.ChannelMessageOutboxStatusFailed, nextRetryAt: ptrTime(time.Now().Add(time.Minute)), wantState: manualResumeMessageDeliveryPending},
		{name: "terminal_failed", status: enums.ChannelMessageOutboxStatusFailed, wantState: manualResumeMessageDeliveryFailed},
		{name: "cancelled", status: enums.ChannelMessageOutboxStatusCancelled, wantState: manualResumeMessageDeliveryUnavailable},
		{name: "cancelled_after_claim", status: enums.ChannelMessageOutboxStatusCancelled, lastError: channelMessageOutboxDispatchUncertainReasonPrefix + "human takeover", wantState: manualResumeMessageDeliveryUncertain},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{
				"send_status":   string(tt.status),
				"next_retry_at": tt.nextRetryAt,
				"last_error":    tt.lastError,
				"updated_at":    time.Now(),
			}).Error; err != nil {
				t.Fatalf("update Outbox status: %v", err)
			}
			if got := manualResumeAIMessageMatchesTrace(conversation.ID, reply.RequestID, commit, true); got != tt.want {
				t.Fatalf("customer-visible commit for Outbox %q = %v, want %v", tt.status, got, tt.want)
			}
			if got := manualResumeAIMessageTraceDeliveryState(conversation.ID, reply.RequestID, commit, true); got != tt.wantState {
				t.Fatalf("delivery state for Outbox %q = %q, want %q", tt.status, got, tt.wantState)
			}
		})
	}
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{
		"send_status": string(enums.ChannelMessageOutboxStatusSending),
		"last_error":  "",
		"updated_at":  time.Now().Add(-aiManualResumeDeliveryUncertainAfter - time.Minute),
	}).Error; err != nil {
		t.Fatalf("mark stale sending Outbox: %v", err)
	}
	if got := manualResumeAIMessageTraceDeliveryState(conversation.ID, reply.RequestID, commit, true); got != manualResumeMessageDeliveryUncertain {
		t.Fatalf("stale sending delivery state=%q want %q", got, manualResumeMessageDeliveryUncertain)
	}
}

func TestManualResumeUncertainDeliveryStopsPollingAndKeepsHuman(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "delivery-uncertain")
	requestID := manualResumeRequestID(task)
	now := time.Now()
	reply := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      origin.SessionNo,
		RequestID:      requestID,
		ClientMsgID:    "manual-resume-delivery-uncertain-reply",
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        "早餐时间是7:00-9:30。",
		SeqNo:          origin.SeqNo + 1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(reply).Error; err != nil {
		t.Fatalf("create committed reply: %v", err)
	}
	outbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID,
		MessageID:      reply.ID,
		Payload:        `{}`,
		SendStatus:     string(enums.ChannelMessageOutboxStatusSending),
		AuditFields: models.AuditFields{
			CreatedAt: now.Add(-aiManualResumeDeliveryUncertainAfter - time.Minute),
			UpdatedAt: now.Add(-aiManualResumeDeliveryUncertainAfter - time.Minute),
		},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create stale sending Outbox: %v", err)
	}
	createCompletedManualResumeRunLog(t, db, *origin, requestID, reply)

	previousHook := TriggerAIReplySyncHook
	defer func() { TriggerAIReplySyncHook = previousHook }()
	hookCalls := 0
	TriggerAIReplySyncHook = func(context.Context, models.Conversation, models.Message) error {
		hookCalls++
		return nil
	}
	if !AIManualResumeTaskService.processOne(*task, now) {
		t.Fatal("expected uncertain delivery task to be terminally reconciled")
	}
	if hookCalls != 0 {
		t.Fatalf("uncertain external delivery must not rerun the model, hook calls=%d", hookCalls)
	}
	reloadedTask := &models.AIManualResumeTask{}
	if err := db.First(reloadedTask, task.ID).Error; err != nil {
		t.Fatalf("reload uncertain task: %v", err)
	}
	if reloadedTask.TaskStatus != aiManualResumeTaskFailed || reloadedTask.NextRetryAt != nil || reloadedTask.LastError != aiManualResumeDeliveryUncertainMarker {
		t.Fatalf("uncertain delivery must stop polling without becoming retryable: %+v", reloadedTask)
	}
	state := ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || !routeStatusBlocksManualResume(state.RouteStatus) || !state.NeedHumanFollowUp || state.ManualExpireAt != nil {
		t.Fatalf("uncertain delivery must remain in an explicit human-review state: %+v", state)
	}
	if items := ChannelMessageOutboxService.ListPending(enums.ChannelTypeWxWorkProtocol, 20); len(items) != 0 {
		t.Fatalf("uncertain sending delivery must not be replayed: %+v", items)
	}
}

func TestManualResumeTerminalDeliveryFailureStopsModelAndKeepsHuman(t *testing.T) {
	for _, initialRoute := range []string{"manual", "ai"} {
		t.Run(initialRoute, func(t *testing.T) {
			db := setupMessageWelcomeTestDB(t)
			aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "delivery-failed-"+initialRoute)
			requestID := manualResumeRequestID(task)
			now := time.Now()
			reply := &models.Message{
				ConversationID: conversation.ID,
				SessionNo:      origin.SessionNo,
				RequestID:      requestID,
				ClientMsgID:    "manual-resume-delivery-failed-reply-" + initialRoute,
				SenderType:     enums.IMSenderTypeAI,
				SenderID:       aiAgent.ID,
				MessageType:    enums.IMMessageTypeText,
				Content:        "早餐时间是7:00-9:30。",
				SeqNo:          origin.SeqNo + 1,
				SendStatus:     enums.IMMessageStatusSent,
				SentAt:         &now,
				AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
			}
			if err := db.Create(reply).Error; err != nil {
				t.Fatalf("create committed reply: %v", err)
			}
			outbox := &models.ChannelMessageOutbox{
				ChannelType:    enums.ChannelTypeWxWorkProtocol,
				ConversationID: conversation.ID,
				MessageID:      reply.ID,
				Payload:        `{}`,
				SendStatus:     string(enums.ChannelMessageOutboxStatusFailed),
				LastError:      "channel retry limit reached",
				AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
			}
			if err := db.Create(outbox).Error; err != nil {
				t.Fatalf("create terminal failed Outbox: %v", err)
			}
			createCompletedManualResumeRunLog(t, db, *origin, requestID, reply)
			if initialRoute == "ai" {
				state := ConversationRouteService.GetByConversationID(conversation.ID)
				if state == nil {
					t.Fatal("expected conversation route")
				}
				if err := db.Model(&models.ConversationRouteState{}).Where("id = ?", state.ID).Updates(map[string]any{
					"route_status": enums.ConversationRouteStatusAIServing,
					"route_target": "ai",
				}).Error; err != nil {
					t.Fatalf("move route to AI before reconciliation: %v", err)
				}
			}

			previousHook := TriggerAIReplySyncHook
			defer func() { TriggerAIReplySyncHook = previousHook }()
			hookCalls := 0
			TriggerAIReplySyncHook = func(context.Context, models.Conversation, models.Message) error {
				hookCalls++
				return nil
			}
			if !AIManualResumeTaskService.processOne(*task, now) {
				t.Fatal("expected terminal failed delivery to be reconciled")
			}
			if hookCalls != 0 {
				t.Fatalf("terminal failed external delivery must not rerun the model, hook calls=%d", hookCalls)
			}
			reloadedTask := &models.AIManualResumeTask{}
			if err := db.First(reloadedTask, task.ID).Error; err != nil {
				t.Fatalf("reload failed task: %v", err)
			}
			if reloadedTask.TaskStatus != aiManualResumeTaskFailed || reloadedTask.NextRetryAt != nil ||
				reloadedTask.RetryCount != 0 || reloadedTask.LastError != aiManualResumeDeliveryFailedMarker {
				t.Fatalf("terminal delivery failure must stop without consuming model retries: %+v", reloadedTask)
			}
			state := ConversationRouteService.GetByConversationID(conversation.ID)
			if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual ||
				!state.NeedHumanFollowUp || state.ManualExpireAt != nil {
				t.Fatalf("terminal delivery failure must keep or restore an explicit human route: %+v", state)
			}
			reloadedOutbox := &models.ChannelMessageOutbox{}
			if err := db.First(reloadedOutbox, outbox.ID).Error; err != nil {
				t.Fatalf("reload terminal failed Outbox: %v", err)
			}
			if reloadedOutbox.SendStatus != string(enums.ChannelMessageOutboxStatusFailed) || reloadedOutbox.NextRetryAt != nil {
				t.Fatalf("terminal failed Outbox must not be revived: %+v", reloadedOutbox)
			}
			if items := ChannelMessageOutboxService.ListPending(enums.ChannelTypeWxWorkProtocol, 20); len(items) != 0 {
				t.Fatalf("terminal failed delivery must not return to the dispatch queue: %+v", items)
			}
		})
	}
}

func TestManualResumeRequestBoundTerminalFailureWithoutRunLogStopsModel(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "delivery-failed-without-runlog")
	requestID := manualResumeRequestID(task)
	now := time.Now()
	reply := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      origin.SessionNo,
		RequestID:      requestID,
		ClientMsgID:    manualResumeOwnedClientMessageID(requestID, "task", origin.ID),
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        "早餐时间是7:00-9:30。",
		SeqNo:          origin.SeqNo + 1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(reply).Error; err != nil {
		t.Fatalf("create request-bound reply: %v", err)
	}
	outbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID,
		MessageID:      reply.ID,
		Payload:        `{}`,
		SendStatus:     string(enums.ChannelMessageOutboxStatusFailed),
		LastError:      "channel retry limit reached",
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create terminal failed Outbox: %v", err)
	}

	previousHook := TriggerAIReplySyncHook
	defer func() { TriggerAIReplySyncHook = previousHook }()
	hookCalls := 0
	TriggerAIReplySyncHook = func(context.Context, models.Conversation, models.Message) error {
		hookCalls++
		return nil
	}
	if !AIManualResumeTaskService.processOne(*task, now) {
		t.Fatal("expected request-bound terminal failure to be reconciled")
	}
	if hookCalls != 0 {
		t.Fatalf("a committed request with terminal delivery failure must not rerun the model, hook calls=%d", hookCalls)
	}
	reloadedTask := &models.AIManualResumeTask{}
	if err := db.First(reloadedTask, task.ID).Error; err != nil {
		t.Fatalf("reload failed task: %v", err)
	}
	if reloadedTask.TaskStatus != aiManualResumeTaskFailed || reloadedTask.NextRetryAt != nil ||
		reloadedTask.RetryCount != 0 || reloadedTask.LastError != aiManualResumeDeliveryFailedMarker {
		t.Fatalf("terminal request-bound delivery failure must stop without model retries: %+v", reloadedTask)
	}
	state := ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual ||
		!state.NeedHumanFollowUp || state.ManualExpireAt != nil {
		t.Fatalf("terminal request-bound failure must remain in explicit human review: %+v", state)
	}
	reloadedOutbox := &models.ChannelMessageOutbox{}
	if err := db.First(reloadedOutbox, outbox.ID).Error; err != nil {
		t.Fatalf("reload terminal failed Outbox: %v", err)
	}
	if reloadedOutbox.SendStatus != string(enums.ChannelMessageOutboxStatusFailed) || reloadedOutbox.NextRetryAt != nil {
		t.Fatalf("terminal failed Outbox must remain terminal: %+v", reloadedOutbox)
	}
}

func TestManualResumeRequestBoundMessageWithoutRunLogNeverRerunsModel(t *testing.T) {
	for _, tt := range []struct {
		name        string
		outboxState enums.ChannelMessageOutboxStatus
		messageAge  time.Duration
		wantStatus  string
		wantError   string
	}{
		{name: "pending delivery", outboxState: enums.ChannelMessageOutboxStatusPending, wantStatus: aiManualResumeTaskRetry, wantError: aiManualResumeAwaitingDeliveryMarker},
		{name: "sent while RunLog may still be persisting", outboxState: enums.ChannelMessageOutboxStatusSent, wantStatus: aiManualResumeTaskRetry, wantError: aiManualResumeAwaitingDeliveryMarker},
		{name: "sent without authoritative trace after grace", outboxState: enums.ChannelMessageOutboxStatusSent, messageAge: aiManualResumeDeliveryReconcileDelay + time.Second, wantStatus: aiManualResumeTaskFailed, wantError: aiManualResumeDeliveryUncertainMarker},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := setupMessageWelcomeTestDB(t)
			aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "request-bound-without-runlog-"+tt.name)
			requestID := manualResumeRequestID(task)
			now := time.Now()
			messageAt := now.Add(-tt.messageAge)
			reply := &models.Message{
				ConversationID: conversation.ID,
				SessionNo:      origin.SessionNo,
				RequestID:      requestID,
				ClientMsgID:    manualResumeOwnedClientMessageID(requestID, "task", origin.ID),
				SenderType:     enums.IMSenderTypeAI,
				SenderID:       aiAgent.ID,
				MessageType:    enums.IMMessageTypeText,
				Content:        "早餐时间是7:00-9:30。",
				SeqNo:          origin.SeqNo + 1,
				SendStatus:     enums.IMMessageStatusSent,
				SentAt:         &messageAt,
				AuditFields:    models.AuditFields{CreatedAt: messageAt, UpdatedAt: messageAt},
			}
			if err := db.Create(reply).Error; err != nil {
				t.Fatalf("create request-bound reply: %v", err)
			}
			outbox := &models.ChannelMessageOutbox{
				ChannelType:    enums.ChannelTypeWxWorkProtocol,
				ConversationID: conversation.ID,
				MessageID:      reply.ID,
				Payload:        `{}`,
				SendStatus:     string(tt.outboxState),
				AuditFields:    models.AuditFields{CreatedAt: messageAt, UpdatedAt: messageAt},
			}
			if err := db.Create(outbox).Error; err != nil {
				t.Fatalf("create request-bound Outbox: %v", err)
			}

			previousHook := TriggerAIReplySyncHook
			defer func() { TriggerAIReplySyncHook = previousHook }()
			hookCalls := 0
			TriggerAIReplySyncHook = func(context.Context, models.Conversation, models.Message) error {
				hookCalls++
				return nil
			}
			if !AIManualResumeTaskService.processOne(*task, now) {
				t.Fatal("expected request-bound delivery to be reconciled")
			}
			if hookCalls != 0 {
				t.Fatalf("an already committed request must not rerun the model, hook calls=%d", hookCalls)
			}
			reloadedTask := &models.AIManualResumeTask{}
			if err := db.First(reloadedTask, task.ID).Error; err != nil {
				t.Fatalf("reload reconciled task: %v", err)
			}
			if reloadedTask.TaskStatus != tt.wantStatus || reloadedTask.LastError != tt.wantError || reloadedTask.RetryCount != 0 {
				t.Fatalf("unexpected request-bound reconciliation result: %+v", reloadedTask)
			}
			if tt.wantStatus == aiManualResumeTaskRetry && reloadedTask.NextRetryAt == nil {
				t.Fatalf("pending delivery must schedule a delivery-only recheck: %+v", reloadedTask)
			}
			if tt.wantStatus == aiManualResumeTaskFailed && reloadedTask.NextRetryAt != nil {
				t.Fatalf("uncertain delivered request must stop automatic retries: %+v", reloadedTask)
			}
		})
	}
}

func TestManualResumeRequestBoundBarrierRequiresStableBusinessOwnership(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		clientID func(string, int64) string
	}{
		{
			name:    "notice only",
			content: aiManualResumeNotice,
			clientID: func(requestID string, sourceID int64) string {
				return manualResumeNoticeClientMessageID(requestID, sourceID)
			},
		},
		{
			name:    "notice content under task-shaped id",
			content: aiManualResumeNotice,
			clientID: func(requestID string, sourceID int64) string {
				return manualResumeOwnedClientMessageID(requestID, "task", sourceID)
			},
		},
		{
			name:    "business content under notice id",
			content: "早餐时间是7:00-9:30。",
			clientID: func(requestID string, sourceID int64) string {
				return manualResumeNoticeClientMessageID(requestID, sourceID)
			},
		},
		{
			name:    "unowned text id",
			content: "早餐时间是7:00-9:30。",
			clientID: func(requestID string, sourceID int64) string {
				return manualResumeTextClientMessageID(requestID, sourceID)
			},
		},
		{
			name:    "wrong request hash",
			content: "早餐时间是7:00-9:30。",
			clientID: func(requestID string, sourceID int64) string {
				return manualResumeOwnedClientMessageID(requestID+"-other", "task", sourceID)
			},
		},
		{
			name:    "wrong source message",
			content: "早餐时间是7:00-9:30。",
			clientID: func(requestID string, sourceID int64) string {
				return manualResumeOwnedClientMessageID(requestID, "task", sourceID+1)
			},
		},
		{
			name:    "malformed ownership hash",
			content: "早餐时间是7:00-9:30。",
			clientID: func(requestID string, sourceID int64) string {
				sum := sha256.Sum256([]byte(requestID))
				return fmt.Sprintf("ai_manual_resume_%x_task_bad_%d", sum[:24], sourceID)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupMessageWelcomeTestDB(t)
			aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "barrier-"+strings.ReplaceAll(test.name, " ", "-"))
			requestID := manualResumeRequestID(task)
			now := time.Now()
			message := &models.Message{
				ConversationID: conversation.ID,
				SessionNo:      origin.SessionNo,
				RequestID:      requestID,
				ClientMsgID:    test.clientID(requestID, origin.ID),
				SenderType:     enums.IMSenderTypeAI,
				SenderID:       aiAgent.ID,
				MessageType:    enums.IMMessageTypeText,
				Content:        test.content,
				SeqNo:          origin.SeqNo + 1,
				SendStatus:     enums.IMMessageStatusSent,
				SentAt:         &now,
				AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
			}
			if err := db.Create(message).Error; err != nil {
				t.Fatalf("create request-bound non-barrier message: %v", err)
			}

			outcome := AIManualResumeTaskService.manualResumeRequestBoundMessageOutcome(task, requestID)
			if outcome.State != manualResumeRunUnavailable {
				t.Fatalf("notice or unowned message must not block real business recovery: %+v", outcome)
			}
		})
	}
}

func TestManualResumeRequestBoundNoticeAndTaskUsesTaskDeliveryOnly(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "notice-plus-task")
	requestID := manualResumeRequestID(task)
	now := time.Now()
	notice := createManualResumeRequestBoundMessage(t, db, aiAgent.ID, *conversation, *origin, requestID,
		manualResumeNoticeClientMessageID(requestID, origin.ID), enums.IMMessageTypeText, aiManualResumeNotice, now)
	createManualResumeRequestBoundOutbox(t, db, conversation.ID, notice.ID, enums.ChannelMessageOutboxStatusFailed, now)
	business := createManualResumeRequestBoundMessage(t, db, aiAgent.ID, *conversation, *origin, requestID,
		manualResumeOwnedClientMessageID(requestID, "task", origin.ID), enums.IMMessageTypeText, "早餐时间是7:00-9:30。", now)
	createManualResumeRequestBoundOutbox(t, db, conversation.ID, business.ID, enums.ChannelMessageOutboxStatusPending, now)

	outcome := AIManualResumeTaskService.manualResumeRequestBoundMessageOutcome(task, requestID)
	if outcome.State != manualResumeRunDeliveryPending {
		t.Fatalf("failed notice must be ignored while the owned business message remains pending: %+v", outcome)
	}
}

func TestManualResumeRequestBoundResourceTerminalFailureIsImmediate(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "resource-terminal-failure")
	requestID := manualResumeRequestID(task)
	now := time.Now()
	resource := createManualResumeRequestBoundMessage(t, db, aiAgent.ID, *conversation, *origin, requestID,
		manualResumeOwnedClientMessageID(requestID, "resource", origin.ID), enums.IMMessageTypeMiniProgram, "", now)
	createManualResumeRequestBoundOutbox(t, db, conversation.ID, resource.ID, enums.ChannelMessageOutboxStatusFailed, now)

	outcome := AIManualResumeTaskService.manualResumeRequestBoundMessageOutcome(task, requestID)
	if outcome.State != manualResumeRunDeliveryFailed {
		t.Fatalf("terminal resource delivery failure must stop immediately: %+v", outcome)
	}
}

func TestManualResumeRequestBoundMessageWaitsForDelayedRunLogAndOutbox(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent, conversation, origin, task := prepareReadyExternalManualResumeTask(t, db, "persistence-race")
	requestID := manualResumeRequestID(task)
	now := time.Now()
	business := createManualResumeRequestBoundMessage(t, db, aiAgent.ID, *conversation, *origin, requestID,
		manualResumeOwnedClientMessageID(requestID, "task", origin.ID), enums.IMMessageTypeText, "早餐时间是7:00-9:30。", now)

	if outcome := AIManualResumeTaskService.manualResumeRequestBoundMessageOutcome(task, requestID); outcome.State != manualResumeRunDeliveryPending {
		t.Fatalf("a freshly committed message must wait briefly for its Outbox and RunLog: %+v", outcome)
	}
	createManualResumeRequestBoundOutbox(t, db, conversation.ID, business.ID, enums.ChannelMessageOutboxStatusSent, now)
	if outcome := AIManualResumeTaskService.manualResumeRequestBoundMessageOutcome(task, requestID); outcome.State != manualResumeRunDeliveryPending {
		t.Fatalf("a freshly sent message must still allow the matching RunLog to persist: %+v", outcome)
	}

	old := now.Add(-aiManualResumeDeliveryReconcileDelay - time.Second)
	if err := db.Model(&models.Message{}).Where("id = ?", business.ID).UpdateColumns(map[string]any{
		"created_at": old,
		"updated_at": old,
		"sent_at":    old,
	}).Error; err != nil {
		t.Fatalf("age request-bound message beyond persistence grace: %v", err)
	}
	if outcome := AIManualResumeTaskService.manualResumeRequestBoundMessageOutcome(task, requestID); outcome.State != manualResumeRunDeliveryUncertain {
		t.Fatalf("a delivered message still missing its RunLog after grace must stop as uncertain: %+v", outcome)
	}
}

func TestPreferredManualResumeRunResultUsesFixedAuthorityPriority(t *testing.T) {
	tests := []struct {
		name      string
		states    []manualResumeRunState
		wantState manualResumeRunState
		wantLogID int64
	}{
		{name: "newer keeps-manual beats older delivery states", states: []manualResumeRunState{manualResumeRunKeepsManual, manualResumeRunDeliveryFailed, manualResumeRunDeliveryUncertain}, wantState: manualResumeRunKeepsManual, wantLogID: 1},
		{name: "terminal failure beats uncertainty", states: []manualResumeRunState{manualResumeRunDeliveryFailed, manualResumeRunDeliveryUncertain}, wantState: manualResumeRunDeliveryFailed, wantLogID: 1},
		{name: "uncertainty beats pending", states: []manualResumeRunState{manualResumeRunDeliveryUncertain, manualResumeRunDeliveryPending}, wantState: manualResumeRunDeliveryUncertain, wantLogID: 1},
		{name: "older equal state cannot overwrite newer", states: []manualResumeRunState{manualResumeRunDeliveryUncertain, manualResumeRunDeliveryUncertain}, wantState: manualResumeRunDeliveryUncertain, wantLogID: 1},
		{name: "older verified commit remains authoritative", states: []manualResumeRunState{manualResumeRunDeliveryFailed, manualResumeRunCommitted}, wantState: manualResumeRunCommitted, wantLogID: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			best := manualResumeRunResult{State: manualResumeRunUnavailable}
			for index, state := range test.states {
				best = preferredManualResumeRunResult(best, manualResumeRunResult{RunLogID: int64(index + 1), State: state})
			}
			if best.State != test.wantState || best.RunLogID != test.wantLogID {
				t.Fatalf("mixed RunLog states selected %+v, want state=%q runLog=%d", best, test.wantState, test.wantLogID)
			}
		})
	}
}

func manualResumeOwnedClientMessageID(requestID string, kind string, sourceMessageID int64) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(requestID)))
	return fmt.Sprintf("ai_manual_resume_%x_%s_0123456789abcdef_%d", sum[:24], strings.TrimSpace(kind), sourceMessageID)
}

func manualResumeNoticeClientMessageID(requestID string, sourceMessageID int64) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(requestID)))
	return fmt.Sprintf("ai_manual_resume_%x_notice_%d", sum[:24], sourceMessageID)
}

func manualResumeTextClientMessageID(requestID string, sourceMessageID int64) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(requestID)))
	return fmt.Sprintf("ai_manual_resume_%x_text_1_%d", sum[:24], sourceMessageID)
}

func createManualResumeRequestBoundMessage(
	t *testing.T,
	db *gorm.DB,
	aiAgentID int64,
	conversation models.Conversation,
	origin models.Message,
	requestID string,
	clientMessageID string,
	messageType enums.IMMessageType,
	content string,
	at time.Time,
) *models.Message {
	t.Helper()
	var maxSeq int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ?", conversation.ID).
		Select("COALESCE(MAX(seq_no), 0)").Scan(&maxSeq).Error; err != nil {
		t.Fatalf("read request-bound message sequence: %v", err)
	}
	message := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      origin.SessionNo,
		RequestID:      requestID,
		ClientMsgID:    clientMessageID,
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgentID,
		MessageType:    messageType,
		Content:        content,
		SeqNo:          maxSeq + 1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &at,
		AuditFields:    models.AuditFields{CreatedAt: at, UpdatedAt: at},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create request-bound message: %v", err)
	}
	return message
}

func createManualResumeRequestBoundOutbox(
	t *testing.T,
	db *gorm.DB,
	conversationID int64,
	messageID int64,
	status enums.ChannelMessageOutboxStatus,
	at time.Time,
) *models.ChannelMessageOutbox {
	t.Helper()
	outbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversationID,
		MessageID:      messageID,
		Payload:        `{}`,
		SendStatus:     string(status),
		AuditFields:    models.AuditFields{CreatedAt: at, UpdatedAt: at},
	}
	if status == enums.ChannelMessageOutboxStatusSent {
		outbox.SentAt = &at
	}
	if status == enums.ChannelMessageOutboxStatusFailed {
		outbox.LastError = "channel retry limit reached"
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create request-bound Outbox: %v", err)
	}
	return outbox
}

func TestManualResumeCorruptTerminalRunBlocksOlderSnapshot(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 238, ConversationID: 9038, SessionNo: 1, ClientMsgID: "terminal-barrier", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "拖鞋去哪里拿",
	})
	createManualResumeTrace(t, db, origin, `{
		"runtime":{"status":"interrupted","input":{"currentTurnSources":[{"ref":"U1","messageId":238,"messageType":"text","text":"拖鞋去哪里拿"}]},"pipeline":{
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}]},
			"evidenceJudge":{"deferredTaskIds":["task-1"],"tasks":[{"taskId":"task-1","disposition":"no_evidence_handoff"}]}
		},"output":{"finishReason":"human_route_dispatched"}}
	}`)
	if err := db.Create(&models.AgentRunLog{
		ConversationID: origin.ConversationID,
		MessageID:      origin.ID,
		RequestID:      "newer-terminal",
		FinalStatus:    "completed",
		TraceData:      "{corrupt",
		CreatedAt:      time.Now().Add(time.Second),
	}).Error; err != nil {
		t.Fatalf("create corrupt terminal RunLog: %v", err)
	}

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	if snapshot, state := AIManualResumeTaskService.manualResumeExecutionSnapshotState(task, []models.Message{origin}); state != manualResumeSnapshotUnavailable || len(snapshot.FrozenTasks) != 0 {
		t.Fatalf("a newer terminal RunLog must block revival of an older deferred snapshot: state=%q snapshot=%#v", state, snapshot)
	}
}

func TestManualResumeLegacyRequestIDCompatibilityIsSourceBound(t *testing.T) {
	task := &models.AIManualResumeTask{HandoffToken: "token-with-dashes", OriginMessageID: 10, LatestWaitingMessageID: 10}
	compatible := manualResumeCompatibleRequestIDs(task)
	if len(compatible) != 2 || compatible[0] != "manual_resume_tokenwithdashes_10" || compatible[1] != "manual_resume_tokenwithdashes" {
		t.Fatalf("origin source must accept current and legacy request IDs: %#v", compatible)
	}
	task.LatestWaitingMessageID = 11
	compatible = manualResumeCompatibleRequestIDs(task)
	if len(compatible) != 2 || compatible[0] != "manual_resume_tokenwithdashes_11" || compatible[1] != "manual_resume_tokenwithdashes" {
		t.Fatalf("rolling upgrades must accept both IDs while the RunLog query remains source-bound: %#v", compatible)
	}
}

func TestManualResumeStrictTraceRejectsIncompleteV2Task(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 241, ConversationID: 9044, SessionNo: 1, ClientMsgID: "strict-incomplete-v2-origin", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: "拖鞋去哪里拿？",
	})
	createManualResumeTrace(t, db, origin, `{
		"runtime":{"input":{"currentTurnSources":[
			{"ref":"U1","messageId":241,"messageType":"text","text":"拖鞋去哪里拿？"}
		]},"pipeline":{
			"replyPlan":{"taskPlans":[
				{"taskId":"task-1","intent":"hotel_info","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿？","resolvedText":"拖鞋去哪里拿？","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}
			]},
			"evidenceJudge":{"deferredTaskIds":["task-1"]}
		}}
	}`)

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	snapshot, ok := AIManualResumeTaskService.manualResumeExecutionSnapshot(task, []models.Message{origin})
	if ok || snapshot.ContractMode != "" || snapshot.SourcesValidated || len(snapshot.FrozenTasks) != 0 {
		t.Fatalf("a strict Trace with incomplete V2 semantics must not masquerade as legacy: snapshot=%#v ok=%v", snapshot, ok)
	}
	if len(snapshot.Sources) != 1 || snapshot.Sources[0].MessageID != origin.ID {
		t.Fatalf("full Intent fallback must retain the validated physical source: %#v", snapshot.Sources)
	}
}

func TestManualResumeDeferredSnapshotSkipsNewerRunLogWithoutMatchingTaskPlans(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 251, ConversationID: 9025, SessionNo: 1, ClientMsgID: "snapshot-origin", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "拖鞋去哪里拿？",
	})
	createManualResumeTrace(t, db, origin, `{
		"runtime":{"input":{"currentTurnSources":[{"ref":"U1","messageId":251,"messageType":"text","text":"拖鞋去哪里拿？"}]},"pipeline":{
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","text":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}]},
			"evidenceJudge":{"deferredTaskIds":["task-1"]}
		}}
	}`)
	validLog := AgentRunLogService.FindOne(sqls.NewCnd().Eq("conversation_id", origin.ConversationID).Eq("message_id", origin.ID).Desc("id"))
	createManualResumeTrace(t, db, origin, `{"runtime":{"pipeline":{"replyPlan":{"taskPlans":[]},"evidenceJudge":{}}}}`)

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	runLogID, frozen, _, ok := AIManualResumeTaskService.manualResumeDeferredSnapshot(task)
	if !ok || validLog == nil || runLogID != validLog.ID || len(frozen) != 1 || frozen[0].TaskID != "task-1" {
		t.Fatalf("a retry log without a snapshot must not hide the newest older valid snapshot: runLog=%d frozen=%#v ok=%v", runLogID, frozen, ok)
	}
}

func TestManualResumeDeferredSnapshotStopsAtNewerAuthoritativeRunWithoutDeferredTasks(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 261, ConversationID: 9026, SessionNo: 1, ClientMsgID: "snapshot-authoritative-origin", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "拖鞋去哪里拿？",
	})
	createManualResumeTrace(t, db, origin, `{
		"runtime":{"input":{"currentTurnSources":[{"ref":"U1","messageId":261,"messageType":"text","text":"拖鞋去哪里拿？"}]},"pipeline":{
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","text":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"]}]},
			"evidenceJudge":{"deferredTaskIds":["task-1"]}
		}}
	}`)
	createManualResumeTrace(t, db, origin, `{
		"runtime":{"status":"completed","pipeline":{
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","text":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"]}]},
			"evidenceJudge":{"deferredTaskIds":[]}
		},"output":{"finishReason":"completed"}}
	}`)

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	if runLogID, frozen, sources, ok := AIManualResumeTaskService.manualResumeDeferredSnapshot(task); ok || runLogID != 0 || len(frozen) != 0 || len(sources) != 0 {
		t.Fatalf("a newer authoritative run with no deferred tasks must stop older snapshot revival: runLog=%d frozen=%#v sources=%#v ok=%v", runLogID, frozen, sources, ok)
	}
}

func TestManualResumeDeferredSnapshotSkipsNewerFailedRunAfterReplyPlan(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 266, ConversationID: 9036, SessionNo: 1, ClientMsgID: "snapshot-failed-plan-origin", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "拖鞋去哪里拿？",
	})
	createManualResumeTrace(t, db, origin, `{
		"runtime":{"status":"interrupted","input":{"currentTurnSources":[{"ref":"U1","messageId":266,"messageType":"text","text":"拖鞋去哪里拿？"}]},"pipeline":{
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}]},
			"evidenceJudge":{"deferredTaskIds":["task-1"]}
		},"output":{"finishReason":"human_route_dispatched"}}
	}`)
	validLog := AgentRunLogService.FindOne(sqls.NewCnd().Eq("conversation_id", origin.ConversationID).Eq("message_id", origin.ID).Desc("id"))
	createManualResumeTrace(t, db, origin, `{
		"runtime":{"status":"error","pipeline":{
			"replyPlan":{"taskPlans":[{"taskId":"task-retry","intent":"hotel_info","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"]}]},
			"evidenceJudge":{"deferredTaskIds":[]}
		},"error":{"stage":"tool_knowledge"}}
	}`)

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	runLogID, frozen, _, ok := AIManualResumeTaskService.manualResumeDeferredSnapshot(task)
	if !ok || validLog == nil || runLogID != validLog.ID || len(frozen) != 1 || frozen[0].TaskID != "task-1" {
		t.Fatalf("a failed run that never reached a deferred decision must not hide the older authoritative snapshot: runLog=%d frozen=%#v ok=%v", runLogID, frozen, ok)
	}
}

func TestManualResumeTraceWithoutMessageIDsIsExplicitLegacy(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 271, ConversationID: 9027, SessionNo: 1, ClientMsgID: "snapshot-legacy-origin", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "拖鞋去哪里拿？",
	})
	createManualResumeTrace(t, db, origin, `{
		"runtime":{"input":{"currentTurnSources":[{"ref":"U1","messageType":"text","text":"拖鞋去哪里拿？"}]},"pipeline":{
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿？","resolvedText":"拖鞋去哪里拿？","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}]},
			"evidenceJudge":{"deferredTaskIds":["task-1"]}
		}}
	}`)

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	snapshot, ok := AIManualResumeTaskService.manualResumeExecutionSnapshot(task, []models.Message{origin})
	if !ok || snapshot.ContractMode != replyruntime.ManualResumeContractLegacy || snapshot.SourcesValidated {
		t.Fatalf("a source backfilled from a real message may be reused only as explicit legacy, got %#v", snapshot)
	}
	if len(snapshot.Sources) != 1 || snapshot.Sources[0].MessageID != origin.ID {
		t.Fatalf("legacy source must still be rebound to the real physical message: %#v", snapshot.Sources)
	}
}

func TestManualResumeTraceWithInvalidMessageIDFallsBackToFullIntent(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 281, ConversationID: 9028, SessionNo: 1, ClientMsgID: "snapshot-invalid-source-origin", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "拖鞋去哪里拿？",
	})
	createManualResumeTrace(t, db, origin, `{
		"runtime":{"input":{"currentTurnSources":[{"ref":"U1","messageId":999999,"messageType":"text","text":"拖鞋去哪里拿？"}]},"pipeline":{
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿？","resolvedText":"拖鞋去哪里拿？","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}]},
			"evidenceJudge":{"deferredTaskIds":["task-1"]}
		}}
	}`)

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	snapshot, ok := AIManualResumeTaskService.manualResumeExecutionSnapshot(task, []models.Message{origin})
	if ok || snapshot.ContractMode != "" || len(snapshot.FrozenTasks) != 0 {
		t.Fatalf("an explicit source ID that cannot be validated must not be silently rebound to another message: %#v ok=%v", snapshot, ok)
	}
	if len(snapshot.Sources) != 1 || snapshot.Sources[0].MessageID != origin.ID {
		t.Fatalf("the compatibility full-Intent request may still use the real current message source: %#v", snapshot.Sources)
	}
}

func TestManualResumeTraceRequiresSequentialLiveSourcesEndingAtOrigin(t *testing.T) {
	for _, tt := range []struct {
		name         string
		traceSources string
		recallFirst  bool
	}{
		{name: "reversed refs", traceSources: `[{"ref":"U2","messageId":291,"messageType":"text","text":"有早餐吗"},{"ref":"U1","messageId":292,"messageType":"text","text":"几点"}]`},
		{name: "missing origin", traceSources: `[{"ref":"U1","messageId":291,"messageType":"text","text":"有早餐吗"}]`},
		{name: "recalled source", traceSources: `[{"ref":"U1","messageId":291,"messageType":"text","text":"有早餐吗"},{"ref":"U2","messageId":292,"messageType":"text","text":"几点"}]`, recallFirst: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := setupManualResumeSourceTestDB(t)
			first := createManualResumeSourceMessage(t, db, models.Message{ID: 291, ConversationID: 9041, SessionNo: 1, ClientMsgID: "trace-boundary-first", SeqNo: 1, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "有早餐吗"})
			origin := createManualResumeSourceMessage(t, db, models.Message{ID: 292, ConversationID: 9041, SessionNo: 1, ClientMsgID: "trace-boundary-origin", SeqNo: 2, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "几点"})
			if tt.recallFirst {
				if err := db.Model(&models.Message{}).Where("id = ?", first.ID).Update("send_status", enums.IMMessageStatusRecalled).Error; err != nil {
					t.Fatalf("recall source: %v", err)
				}
			}
			createManualResumeTrace(t, db, origin, `{"runtime":{"input":{"currentTurnSources":`+tt.traceSources+`},"pipeline":{"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","objective":"time","relationToPrevious":"independent","resolutionState":"resolved_from_context","originalText":"几点","resolvedText":"早餐几点","sourceRefs":["U2","U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}]},"evidenceJudge":{"deferredTaskIds":["task-1"]}}}}`)
			task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
			snapshot, ok := AIManualResumeTaskService.manualResumeExecutionSnapshot(task, []models.Message{first, origin})
			if ok || snapshot.ContractMode != "" || len(snapshot.FrozenTasks) != 0 {
				t.Fatalf("invalid physical URef boundary must fall back to a fresh Intent without frozen tasks: %#v ok=%v", snapshot, ok)
			}
		})
	}
}

func TestManualResumeSourcesUseTraceMessageIDsBeyondLegacyEightSecondWindow(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	firstAt := time.Now().Add(-30 * time.Second)
	first := createManualResumeSourceMessage(t, db, models.Message{
		ID: 501, ConversationID: 9005, SessionNo: 1, ClientMsgID: "trace-first", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "有早餐吗？", SentAt: &firstAt,
	})
	originAt := firstAt.Add(20 * time.Second)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 502, ConversationID: 9005, SessionNo: 1, ClientMsgID: "trace-origin", SeqNo: 2,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "几点？", SentAt: &originAt,
	})
	createManualResumeTrace(t, db, origin, `{
		"runtime":{"input":{"currentTurnSources":[
			{"ref":"U1","messageId":501,"messageType":"text","text":"有早餐吗？"},
			{"ref":"U2","messageId":502,"messageType":"text","text":"几点？"}
		]},"pipeline":{
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","text":"早餐几点","originalText":"几点？","resolvedText":"早餐几点","sourceRefs":["U2","U1"],"needsKnowledge":true,"outputKind":"handoff","replyRequired":false,"output":"deferred_knowledge_handoff"}]},
			"evidenceJudge":{"deferredTaskIds":["task-1"]}
		}}
	}`)

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	sources := AIManualResumeTaskService.manualResumeSources(task, []models.Message{first, origin})
	if len(sources) != 2 || sources[0].MessageID != first.ID || sources[1].MessageID != origin.ID {
		t.Fatalf("trace message IDs, not the legacy eight-second window, must reconstruct the original turn: %#v", sources)
	}
}

func TestManualResumeSourcesFallsBackToOriginalBurstWithoutTrace(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	firstAt := time.Now()
	first := createManualResumeSourceMessage(t, db, models.Message{
		ID: 301, ConversationID: 9003, SessionNo: 1, ClientMsgID: "legacy-first", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: "有早餐吗？", SentAt: &firstAt,
	})
	secondAt := firstAt.Add(2 * time.Second)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 302, ConversationID: 9003, SessionNo: 1, ClientMsgID: "legacy-origin", SeqNo: 2,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: "几点？", SentAt: &secondAt,
	})

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	sources := AIManualResumeTaskService.manualResumeSources(task, []models.Message{first, origin})
	if len(sources) != 2 || sources[0].MessageID != first.ID || sources[1].MessageID != origin.ID {
		t.Fatalf("missing legacy Trace must safely reconstruct the original physical burst, got %#v", sources)
	}
	if sources[0].Text != "有早餐吗？" || sources[1].Text != "几点？" {
		t.Fatalf("legacy fallback changed original message order or content: %#v", sources)
	}
}

func TestManualResumeSourcesAddsLaterCustomerMessageAndUsesVoiceTranscript(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	originAt := time.Now()
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 401, ConversationID: 9004, SessionNo: 1, ClientMsgID: "voice-origin", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice,
		Content: "voice.amr", Payload: `{"mediaText":"早餐几点？","mediaSummary":"咨询早餐","mediaUnderstandingStatus":"understood"}`, SentAt: &originAt,
	})
	laterAt := originAt.Add(20 * time.Second)
	later := createManualResumeSourceMessage(t, db, models.Message{
		ID: 402, ConversationID: 9004, SessionNo: 1, ClientMsgID: "later-question", SeqNo: 2,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: "停车免费吗？", SentAt: &laterAt,
	})

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: later.ID}
	sources := AIManualResumeTaskService.manualResumeSources(task, []models.Message{origin, later})
	if len(sources) != 2 || sources[0].MessageID != origin.ID || sources[0].Text != "早餐几点？" || sources[1].MessageID != later.ID || sources[1].Text != "停车免费吗？" {
		t.Fatalf("resume fallback must preserve voice transcript and later waiting messages: %#v", sources)
	}
}

func setupManualResumeSourceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := "manual_resume_source_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open sql db: %v", err)
	}
	t.Cleanup(func() {
		sqls.SetDB(nil)
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&models.Message{}, &models.AgentRunLog{}); err != nil {
		t.Fatalf("migrate manual resume fixtures: %v", err)
	}
	sqls.SetDB(db)
	return db
}

func createManualResumeSourceMessage(t *testing.T, db *gorm.DB, message models.Message) models.Message {
	t.Helper()
	if message.SentAt == nil {
		now := time.Now()
		message.SentAt = &now
	}
	if err := db.Create(&message).Error; err != nil {
		t.Fatalf("create message %d: %v", message.ID, err)
	}
	return message
}

func createManualResumeTrace(t *testing.T, db *gorm.DB, origin models.Message, trace string) {
	t.Helper()
	if err := db.Create(&models.AgentRunLog{
		ConversationID: origin.ConversationID,
		MessageID:      origin.ID,
		RequestID:      "trace",
		TraceData:      trace,
		CreatedAt:      time.Now(),
	}).Error; err != nil {
		t.Fatalf("create AgentRunLog: %v", err)
	}
}
