package services

import (
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

func TestManualResumeSourcesSelectsOnlyDeferredTaskFromSinglePhysicalMessage(t *testing.T) {
	db := setupManualResumeSourceTestDB(t)
	origin := createManualResumeSourceMessage(t, db, models.Message{
		ID: 101, ConversationID: 9001, SessionNo: 1, ClientMsgID: "single-source", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: "早餐几点，拖鞋去哪里拿？",
	})
	createManualResumeTrace(t, db, origin, `{
		"runtime":{"input":{"currentTurnSources":[
			{"ref":"U1","messageId":101,"messageType":"text","text":"早餐几点，拖鞋去哪里拿？"}
		]},"pipeline":{
			"replyPlan":{"taskPlans":[
				{"taskId":"task-1","intent":"hotel_info","subIntent":"breakfast","objective":"time","relationToPrevious":"independent","resolutionState":"clear","originalText":"早餐几点","resolvedText":"早餐几点","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"},
				{"taskId":"task-2","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","entities":[{"text":"拖鞋","type":"supply"}],"originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply","missingAspects":["location"]}
			]},
			"evidenceJudge":{"deferredTaskIds":["task-2"]}
		}}
	}`)

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
		frozen.OutputKind != "text" || !frozen.ReplyRequired || frozen.Output != "knowledge_text_reply" ||
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
				{"taskId":"task-1","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"},
				{"taskId":"task-2","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"牙刷去哪里拿","resolvedText":"牙刷去哪里拿","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"}
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
				{"taskId":"task-1","intent":"hotel_info","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿？","resolvedText":"拖鞋去哪里拿？","sourceRefs":["U1"]}
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
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","text":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"]}]},
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
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"]}]},
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
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿？","resolvedText":"拖鞋去哪里拿？","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"}]},
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
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","subIntent":"supplies_self_help","objective":"location","relationToPrevious":"independent","resolutionState":"clear","originalText":"拖鞋去哪里拿？","resolvedText":"拖鞋去哪里拿？","sourceRefs":["U1"],"needsKnowledge":true,"outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"}]},
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
			createManualResumeTrace(t, db, origin, `{"runtime":{"input":{"currentTurnSources":`+tt.traceSources+`},"pipeline":{"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","objective":"time","relationToPrevious":"independent","resolutionState":"resolved_from_context","originalText":"几点","resolvedText":"早餐几点","sourceRefs":["U2","U1"]}]},"evidenceJudge":{"deferredTaskIds":["task-1"]}}}}`)
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
			"replyPlan":{"taskPlans":[{"taskId":"task-1","intent":"hotel_info","text":"早餐几点","originalText":"几点？","resolvedText":"早餐几点","sourceRefs":["U2","U1"]}]},
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
