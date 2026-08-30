package services

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

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
		"runtime":{"pipeline":{
			"replyPlan":{"taskPlans":[
				{"taskId":"task-1","text":"早餐几点","resolvedText":"早餐几点","sourceRefs":["U1"]},
				{"taskId":"task-2","text":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"]}
			]},
			"evidenceJudge":{"deferredTaskIds":["task-2"]}
		}}
	}`)

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	sources := AIManualResumeTaskService.manualResumeSources(task, []models.Message{origin})
	if len(sources) != 1 || sources[0].MessageID != origin.ID {
		t.Fatalf("expected one source bound to the original physical message, got %#v", sources)
	}
	if sources[0].Text != "拖鞋去哪里拿" || strings.Contains(sources[0].Text, "早餐") {
		t.Fatalf("resume must contain only the deferred sibling task, got %#v", sources[0])
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
		"runtime":{"pipeline":{
			"replyPlan":{"taskPlans":[
				{"taskId":"task-1","text":"拖鞋去哪里拿","resolvedText":"拖鞋去哪里拿","sourceRefs":["U1"]},
				{"taskId":"task-2","text":"牙刷去哪里拿","resolvedText":"牙刷去哪里拿","sourceRefs":["U1"]}
			]},
			"evidenceJudge":{"deferredTaskIds":["task-1","task-2"]}
		}}
	}`)

	task := &models.AIManualResumeTask{ConversationID: origin.ConversationID, OriginMessageID: origin.ID, LatestWaitingMessageID: origin.ID}
	sources := AIManualResumeTaskService.manualResumeSources(task, []models.Message{origin})
	if len(sources) != 1 || sources[0].MessageID != origin.ID {
		t.Fatalf("two deferred tasks from one physical message must share one source, got %#v", sources)
	}
	if sources[0].Text != "拖鞋去哪里拿\n牙刷去哪里拿" {
		t.Fatalf("grouped deferred tasks must preserve ReplyPlan order, got %q", sources[0].Text)
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
