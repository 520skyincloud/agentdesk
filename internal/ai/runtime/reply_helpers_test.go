package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	applicationruntime "agent-desk/internal/ai/application/runtime"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/toolx"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestSummaryPrimaryToolCodePrefersToolSearchTarget(t *testing.T) {
	summary := &applicationruntime.Summary{
		InvokedToolCodes: []string{toolx.BuiltinToolSearch.Code},
		TraceData: `{
			"toolSearch": {
				"items": [
					{"targetToolCode":"mcp/server/tool_a"}
				]
			}
		}`,
	}

	if got := summaryPrimaryToolCode(summary); got != "mcp/server/tool_a" {
		t.Fatalf("unexpected primary tool code: %q", got)
	}
}

func TestToRunLogFinalAction(t *testing.T) {
	if got := toRunLogFinalAction(&applicationruntime.Summary{PlannedSkillCode: "refund", ReplyText: "ok"}); got != "skill" {
		t.Fatalf("expected skill final action, got %q", got)
	}

	graphSummary := &applicationruntime.Summary{
		ReplyText: "ok",
		TraceData: `{
			"graphTools": {
				"items": [
					{"toolCode":"` + toolx.GraphAnalyzeConversation.Code + `"}
				]
			}
		}`,
	}
	if got := toRunLogFinalAction(graphSummary); got != "graph" {
		t.Fatalf("expected graph final action, got %q", got)
	}

	if got := toRunLogFinalAction(&applicationruntime.Summary{Status: "fallback"}); got != "fallback" {
		t.Fatalf("expected fallback final action, got %q", got)
	}
}

func TestRunLogFinalActionUsesStructuredResourceTrace(t *testing.T) {
	summary := &applicationruntime.Summary{Status: "completed", ReplyText: "[位置] 丽斯未来酒店"}
	trace := &aiReplyTraceData{FinalAction: "resource"}
	if got := runLogFinalAction(summary, trace); got != "resource" {
		t.Fatalf("expected resource final action, got %q", got)
	}
}

func TestStructuredVariableResourceTypeFromTrace(t *testing.T) {
	locationTrace := &aiReplyTraceData{Runtime: json.RawMessage(`{
		"pipeline": {
			"intent": {
				"primaryIntent": "hotel_variable",
				"needsResource": true,
				"resourceAction": "provide_location"
			}
		}
	}`)}
	if got := structuredVariableResourceTypeFromTrace(locationTrace); got != "location" {
		t.Fatalf("expected location resource, got %q", got)
	}

	miniProgramTrace := &aiReplyTraceData{Runtime: json.RawMessage(`{
		"pipeline": {
			"intent": {
				"primaryIntent": "hotel_variable",
				"needsResource": true,
				"resourceAction": "send_miniprogram"
			}
		}
	}`)}
	if got := structuredVariableResourceTypeFromTrace(miniProgramTrace); got != "mini_program" {
		t.Fatalf("expected mini_program resource, got %q", got)
	}

	hotelInfoTrace := &aiReplyTraceData{Runtime: json.RawMessage(`{
		"pipeline": {
			"intent": {
				"primaryIntent": "hotel_info",
				"needsResource": true,
				"resourceAction": "provide_location"
			}
		}
	}`)}
	if got := structuredVariableResourceTypeFromTrace(hotelInfoTrace); got != "location" {
		t.Fatalf("expected mixed hotel_info resource send, got %q", got)
	}
}

func TestStructuredVariableResourceTypesFromTraceUsesResourceActions(t *testing.T) {
	trace := &aiReplyTraceData{Runtime: json.RawMessage(`{
		"pipeline": {
			"intent": {
				"primaryIntent": "hotel_variable",
				"needsResource": true,
				"resourceAction": "provide_location",
				"resourceActions": ["provide_location", "provide_mini_program", "provide_phone"]
			}
		}
	}`)}
	got := structuredVariableResourceTypesFromTrace(trace)
	if len(got) != 3 || got[0] != "location" || got[1] != "mini_program" || got[2] != "phone" {
		t.Fatalf("expected ordered structured location, mini_program and phone resources, got %#v", got)
	}
}

func TestStructuredVariableResourceTypesFromTraceIncludesPhone(t *testing.T) {
	trace := &aiReplyTraceData{Runtime: json.RawMessage(`{
		"pipeline": {
			"intent": {
				"primaryIntent": "hotel_variable",
				"needsResource": true,
				"resourceAction": "provide_phone"
			}
		}
	}`)}
	got := structuredVariableResourceTypesFromTrace(trace)
	if len(got) != 1 || got[0] != "phone" {
		t.Fatalf("expected phone structured resource, got %#v", got)
	}
}

func TestKnowledgeResourceTraceBuildsOrderedImageCommitMessages(t *testing.T) {
	dbName := "runtime_knowledge_resource_commit_" + strings.NewReplacer("/", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Asset{}, &models.WxWorkProtocolInstance{}, &models.ConversationRouteState{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	sqls.SetDB(db)
	instance := &models.WxWorkProtocolInstance{ID: 7, Guid: "knowledge-resource-guid", Status: enums.StatusOk}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{ConversationID: 99, WxWorkInstanceID: instance.ID}).Error; err != nil {
		t.Fatalf("create route: %v", err)
	}
	for _, asset := range []*models.Asset{
		{AssetID: "knowledge-image-1", Provider: enums.AssetProviderLocal, StorageKey: "knowledge-resources/3/7/one.png", Filename: "one.png", MimeType: "image/png", Status: enums.AssetStatusSuccess},
		{AssetID: "knowledge-image-2", Provider: enums.AssetProviderLocal, StorageKey: "knowledge-resources/3/7/two.png", Filename: "two.png", MimeType: "image/png", Status: enums.AssetStatusSuccess},
	} {
		if err := db.Create(asset).Error; err != nil {
			t.Fatalf("create asset: %v", err)
		}
	}

	trace := &aiReplyTraceData{Runtime: json.RawMessage(`{
		"knowledgeResources":[
			{"assetId":"knowledge-image-1","title":"入口图","sortNo":1},
			{"assetId":"knowledge-image-2","title":"路线图","sortNo":2}
		]
	}`)}
	service := newReplyCommitService()
	if !service.HasStructuredVariableReply(trace) {
		t.Fatal("knowledge image resources must be treated as structured commit actions")
	}
	replies := service.buildKnowledgeResourceReplies(replyCommitInput{
		Conversation: models.Conversation{ID: 99},
		Trace:        trace,
	})
	if len(replies) != 2 {
		t.Fatalf("expected two image commit messages, got %#v", replies)
	}
	for index, reply := range replies {
		if reply.ResourceType != "knowledge_image" || reply.MessageType != enums.IMMessageTypeImage {
			t.Fatalf("expected image resource reply, got %#v", reply)
		}
		if !strings.Contains(reply.Payload, `"assetId":"knowledge-image-`) {
			t.Fatalf("expected canonical asset payload, got %q", reply.Payload)
		}
		if index == 0 && reply.Content != "入口图" {
			t.Fatalf("expected first image title to retain order, got %q", reply.Content)
		}
	}
}

func TestUpdateRuntimeTraceOutputForStructuredResource(t *testing.T) {
	trace := &aiReplyTraceData{Runtime: json.RawMessage(`{"output":{"replyText":"旧文本链接","finishReason":"completed"}}`)}
	updateRuntimeTraceOutput(trace, "[小程序] e秒安心住", "committed_structured_mini_program")
	var data struct {
		Output struct {
			ReplyText    string `json:"replyText"`
			FinishReason string `json:"finishReason"`
		} `json:"output"`
	}
	if err := json.Unmarshal(trace.Runtime, &data); err != nil {
		t.Fatalf("unmarshal trace: %v", err)
	}
	if data.Output.ReplyText != "[小程序] e秒安心住" {
		t.Fatalf("unexpected reply text: %q", data.Output.ReplyText)
	}
	if data.Output.FinishReason != "committed_structured_mini_program" {
		t.Fatalf("unexpected finish reason: %q", data.Output.FinishReason)
	}
}

func TestUpdateRuntimeTraceOutputRecordsCommittedActionLedger(t *testing.T) {
	trace := &aiReplyTraceData{Runtime: json.RawMessage(`{
		"output":{"replyText":"","finishReason":"completed"},
		"actionLedger":{"requestedActions":[{"action":"provide_phone","resourceType":"phone","status":"requested"}]}
	}`)}
	updateRuntimeTraceCommitOutput(trace, "酒店电话：0551-88886666", "committed_structured_resources", []map[string]any{
		{"messageId": int64(99), "messageType": "text", "resourceType": "phone", "content": "酒店电话：0551-88886666", "status": "sent"},
	})
	var data struct {
		ActionLedger struct {
			CommittedActions []struct {
				Action       string `json:"action"`
				ResourceType string `json:"resourceType"`
				MessageID    int64  `json:"messageId"`
				Status       string `json:"status"`
			} `json:"committedActions"`
		} `json:"actionLedger"`
	}
	if err := json.Unmarshal(trace.Runtime, &data); err != nil {
		t.Fatalf("unmarshal trace: %v", err)
	}
	if len(data.ActionLedger.CommittedActions) != 1 {
		t.Fatalf("expected one committed action, got %#v", data.ActionLedger.CommittedActions)
	}
	item := data.ActionLedger.CommittedActions[0]
	if item.Action != "provide_phone" || item.ResourceType != "phone" || item.MessageID != 99 || item.Status != "committed" {
		t.Fatalf("unexpected committed action: %#v", item)
	}
}

func TestSplitReplyTextForCommitUsesExplicitMultiMessageMarker(t *testing.T) {
	trace := &aiReplyTraceData{Runtime: json.RawMessage(`{
		"pipeline":{
			"replyPlan":{
				"taskPlans":[
					{"intent":"hotel_info","output":"knowledge_text_reply"},
					{"intent":"hotel_info","output":"knowledge_text_reply"},
					{"intent":"hotel_variable","output":"structured_resource_commit"}
				]
			}
		}
	}`)}
	parts := splitReplyTextForCommit(trace, "停车从繁华大道辅路进。\n<<NEXT_MESSAGE>>\n发票退房后在小程序申请。")
	if len(parts) != 2 || parts[0] != "停车从繁华大道辅路进。" || parts[1] != "发票退房后在小程序申请。" {
		t.Fatalf("expected two commit text messages, got %#v", parts)
	}
}

func TestSplitReplyTextForCommitKeepsSingleTaskReplyTogether(t *testing.T) {
	trace := &aiReplyTraceData{Runtime: json.RawMessage(`{
		"pipeline":{
			"replyPlan":{
				"taskPlans":[
					{"intent":"hotel_info","output":"knowledge_text_reply"}
				]
			}
		}
	}`)}
	parts := splitReplyTextForCommit(trace, "第一句。\n\n第二句。")
	if len(parts) != 1 || parts[0] != "第一句。\n\n第二句。" {
		t.Fatalf("expected single-task reply to remain one message, got %#v", parts)
	}
}

func TestExtractInterruptMessageAndCheckpointError(t *testing.T) {
	if got := extractInterruptMessage(`{"message":"请补充订单号"}`); got != "请补充订单号" {
		t.Fatalf("unexpected interrupt message: %q", got)
	}
	if got := extractInterruptMessage("not-json"); got != "" {
		t.Fatalf("expected empty message for invalid json, got %q", got)
	}

	err := fakeErr("Failed to load from checkpoint: record does not exist")
	if !isCheckpointMissingError(err) {
		t.Fatalf("expected checkpoint missing error to be detected")
	}
	if isCheckpointMissingError(fakeErr("other error")) {
		t.Fatalf("expected unrelated error to be ignored")
	}
}

func TestGraphPlanReason(t *testing.T) {
	summary := &applicationruntime.Summary{
		TraceData: `{
			"graphTools": {
				"items": [
					{
						"toolCode":"` + toolx.GraphTriageServiceRequest.Code + `",
						"recommendedAction":"create_ticket",
						"ticketDraftReady": true
					}
				]
			}
		}`,
	}
	got := graphPlanReason(summary)
	if !strings.Contains(got, "create_ticket") || !strings.Contains(got, "ready ticket draft") {
		t.Fatalf("unexpected graph plan reason: %q", got)
	}
}

func TestExtractHandoffReason(t *testing.T) {
	summary := &applicationruntime.Summary{
		TraceData: `{
			"graphTools": {
				"items": [
					{
						"toolCode":"` + toolx.GraphHandoffConversation.Code + `",
						"arguments":{"reason":"  用户明确要求人工处理  "}
					}
				]
			}
		}`,
	}
	if got := extractHandoffReason(summary); got != "用户明确要求人工处理" {
		t.Fatalf("unexpected handoff reason: %q", got)
	}
}

type fakeErr string

func (e fakeErr) Error() string {
	return string(e)
}
