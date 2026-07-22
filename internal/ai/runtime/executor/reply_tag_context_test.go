package executor

import (
	"encoding/json"
	"reflect"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"

	"github.com/cloudwego/eino/schema"
	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestReplyTagContextSchemaAndRender(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(replyTagContextSchemaV1), &schema); err != nil {
		t.Fatalf("schema must be valid JSON: %v", err)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("root additionalProperties=%#v", schema["additionalProperties"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) != 3 {
		t.Fatalf("root properties=%#v", schema["properties"])
	}
	value := replyTagContextV1{
		SchemaVersion: replyTagContextSchemaVersion,
		Scenes:        []string{"room_assignment"},
		Tags: []replyTagContextItemV1{
			{TagID: 1, SemanticKey: "room.quiet", Name: "喜静"},
			{TagID: 2, SemanticKey: "room.king_bed", Name: "大床"},
		},
	}
	if err := validateReplyTagContextV1(value); err != nil {
		t.Fatal(err)
	}
	const expected = "低优先偏好：喜静、大床；仅在与当前问题相关时自然参考，不得复述标签、提及来源或覆盖客户当前表达。"
	if rendered := renderReplyTagContext(value); rendered != expected {
		t.Fatalf("rendered=%q", rendered)
	}
}

func TestSelectReplyTagScenesUsesOnlyCurrentTask(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info", SubIntent: "network_wifi", ShouldReply: true, NeedsKnowledge: true,
	}
	scenes, reason := selectReplyTagScenes("WiFi 密码是多少", intent, callbacks.ReplyPlanTraceData{})
	if len(scenes) != 0 || reason != "no_matching_scene" {
		t.Fatalf("wifi scenes=%#v reason=%q", scenes, reason)
	}

	intent.SubIntent = "hotel_knowledge"
	scenes, _ = selectReplyTagScenes("想要安静一点的大床房", intent, callbacks.ReplyPlanTraceData{})
	if !reflect.DeepEqual(scenes, []string{"room_assignment"}) {
		t.Fatalf("room assignment scenes=%#v", scenes)
	}

	intent.PrimaryIntent = "interaction"
	scenes, reason = selectReplyTagScenes("谢谢，房间挺安静", intent, callbacks.ReplyPlanTraceData{})
	if len(scenes) != 0 || reason != "interaction" {
		t.Fatalf("interaction scenes=%#v reason=%q", scenes, reason)
	}

	intent.PrimaryIntent = "hotel_info"
	intent.NeedsHumanRoute = true
	scenes, reason = selectReplyTagScenes("我要一个安静房间", intent, callbacks.ReplyPlanTraceData{})
	if len(scenes) != 0 || reason != "human_route" {
		t.Fatalf("human route scenes=%#v reason=%q", scenes, reason)
	}
}

func TestSelectReplyTagScenesIgnoresStructuredResourceTasks(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "hotel_variable", ShouldReply: true, NeedsKnowledge: true, NeedsResource: true,
	}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_variable", SubIntent: "mini_program", Text: "发送入住小程序", Output: "structured_resource_commit", ResourceAction: "provide_mini_program"},
		{Intent: "hotel_info", SubIntent: "network_wifi", Text: "WiFi 密码是多少", Output: "knowledge_text_reply"},
	}}
	scenes, reason := selectReplyTagScenes("发入住小程序，再告诉我 WiFi 密码", intent, plan)
	if len(scenes) != 0 || reason != "no_matching_scene" {
		t.Fatalf("resource task leaked into Generate tag scenes: scenes=%#v reason=%q", scenes, reason)
	}

	plan.TaskPlans = plan.TaskPlans[:1]
	scenes, reason = selectReplyTagScenes("发入住小程序", intent, plan)
	if len(scenes) != 0 || reason != "resource_only" {
		t.Fatalf("resource-only task scenes=%#v reason=%q", scenes, reason)
	}
}

func TestReplyTagContextRejectsLimits(t *testing.T) {
	value := replyTagContextV1{
		SchemaVersion: replyTagContextSchemaVersion,
		Scenes:        []string{"room_assignment", "room_selection", "room_service", "pet_service"},
	}
	if err := validateReplyTagContextV1(value); err == nil {
		t.Fatal("expected scene limit validation failure")
	}
}

func TestAppendReplyTagContextCandidateFailureLeavesMessagesUnchanged(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })

	original := schema.SystemMessage("existing generation context")
	messages := []*schema.Message{original}
	collector := callbacks.NewRuntimeTraceCollector()
	appendReplyTagContext(
		RunInput{
			Conversation: models.Conversation{ID: 88},
			UserMessage:  models.Message{Content: "帮我安排安静一点的房间"},
		},
		callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "hotel_knowledge", NeedsKnowledge: true, ShouldReply: true},
		callbacks.ReplyPlanTraceData{},
		answerabilityStatusHasContext,
		collector,
		&messages,
	)
	if len(messages) != 1 || messages[0] != original {
		t.Fatalf("messages changed after candidate failure: %#v", messages)
	}
	trace := collector.Data.Pipeline.Generate.TagContext
	if trace.Status != "failed" || trace.Reason != "candidate_query_failed" || !reflect.DeepEqual(trace.Scenes, []string{"room_assignment"}) {
		t.Fatalf("unexpected failed tag trace: %#v", trace)
	}
}

func TestAppendReplyTagContextNonKnowledgeDoesNotRequireAnswerability(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })

	original := schema.SystemMessage("existing generation context")
	messages := []*schema.Message{original}
	collector := callbacks.NewRuntimeTraceCollector()
	appendReplyTagContext(
		RunInput{
			Conversation: models.Conversation{ID: 88},
			UserMessage:  models.Message{Content: "帮我安排安静一点的房间"},
		},
		callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "room_assignment", ShouldReply: true},
		callbacks.ReplyPlanTraceData{},
		"",
		collector,
		&messages,
	)
	if len(messages) != 1 || messages[0] != original {
		t.Fatalf("messages changed after candidate failure: %#v", messages)
	}
	trace := collector.Data.Pipeline.Generate.TagContext
	if trace.Status != "failed" || trace.Reason != "candidate_query_failed" || !reflect.DeepEqual(trace.Scenes, []string{"room_assignment"}) {
		t.Fatalf("non-knowledge reply did not reach candidate selection: %#v", trace)
	}
}

func TestAppendReplyTagContextKnowledgeGateLeavesMessagesUnchanged(t *testing.T) {
	for _, status := range []string{
		answerabilityStatusNoContext,
		answerabilityStatusUnanswerable,
		answerabilityStatusSkipped,
		"",
		"unknown",
	} {
		t.Run(status, func(t *testing.T) {
			original := schema.SystemMessage("existing generation context")
			messages := []*schema.Message{original}
			collector := callbacks.NewRuntimeTraceCollector()
			appendReplyTagContext(
				RunInput{
					Conversation: models.Conversation{ID: 88},
					UserMessage:  models.Message{Content: "帮我安排安静一点的房间"},
				},
				callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "hotel_knowledge", NeedsKnowledge: true, ShouldReply: true},
				callbacks.ReplyPlanTraceData{},
				status,
				collector,
				&messages,
			)
			if len(messages) != 1 || messages[0] != original {
				t.Fatalf("messages changed for answerability status %q: %#v", status, messages)
			}
			trace := collector.Data.Pipeline.Generate.TagContext
			if trace.Status != "skipped" || trace.Reason != "knowledge_not_answerable" || !reflect.DeepEqual(trace.Scenes, []string{"room_assignment"}) {
				t.Fatalf("unexpected skipped tag trace for status %q: %#v", status, trace)
			}
		})
	}
}
