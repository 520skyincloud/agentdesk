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
			{"assetId":"knowledge-image-1","title":"入口图","sortNo":1,"taskIds":["task-1"]},
			{"assetId":"knowledge-image-2","title":"路线图","sortNo":2,"taskIds":["task-2"]}
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
		wantTaskID := fmt.Sprintf("task-%d", index+1)
		if len(reply.TaskIDs) != 1 || reply.TaskIDs[0] != wantTaskID {
			t.Fatalf("knowledge image must retain Task ownership: got %#v want %q", reply.TaskIDs, wantTaskID)
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

func TestTextCommitTaskIDsFromTracePreservesAllTextTasks(t *testing.T) {
	trace := &aiReplyTraceData{Runtime: json.RawMessage(`{
		"pipeline":{"replyPlan":{"taskPlans":[
			{"taskId":"task-1","intent":"hotel_info","output":"knowledge_text_reply"},
			{"intent":"hotel_variable","output":"structured_resource_commit"},
			{"taskId":"task-2","intent":"hotel_info","output":"knowledge_text_reply"},
			{"taskId":"task-3","intent":"interaction","output":"text_reply"},
			{"taskId":"task-4","intent":"hotel_info","output":"knowledge_text_reply"}
		]}}
	}`)}
	ids := textCommitTaskIDsFromTrace(trace)
	if strings.Join(ids, ",") != "task-1,task-2,task-3,task-4" {
		t.Fatalf("all ordered text Task IDs must survive message-count capping, got %#v", ids)
	}
}

func TestBuildTextCommitPartsBalancesAllTasksAcrossThreeMessages(t *testing.T) {
	trace := &aiReplyTraceData{Runtime: json.RawMessage(`{
		"pipeline":{"replyPlan":{"taskPlans":[
			{"taskId":"task-1","intent":"hotel_info","outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"},
			{"taskId":"task-2","intent":"hotel_info","outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"},
			{"taskId":"task-3","intent":"hotel_info","outputKind":"text","replyRequired":true,"output":"knowledge_text_reply"},
			{"taskId":"task-4","intent":"interaction","outputKind":"text","replyRequired":true,"output":"text_reply"}
		]}}
	}`)}
	parts := buildTextCommitParts(trace, "一\n<<NEXT_MESSAGE>>\n二\n<<NEXT_MESSAGE>>\n三\n<<NEXT_MESSAGE>>\n四")
	if len(parts) != 3 {
		t.Fatalf("customer messages must still be capped at three, got %#v", parts)
	}
	allTaskIDs := make([]string, 0, 4)
	for _, part := range parts {
		allTaskIDs = append(allTaskIDs, part.TaskIDs...)
	}
	if strings.Join(allTaskIDs, ",") != "task-1,task-2,task-3,task-4" {
		t.Fatalf("message merging must not lose Task ownership: %#v", parts)
	}
}

func TestBindFallbackResourceTextPartsPreservesResourceTaskOwnership(t *testing.T) {
	trace := &aiReplyTraceData{Runtime: json.RawMessage(`{
		"pipeline":{
			"intent":{"primaryIntent":"hotel_variable","needsResource":true,"resourceActions":["provide_location","send_miniprogram"]},
			"replyPlan":{"taskPlans":[
				{"taskId":"task-location","intent":"hotel_variable","subIntent":"location","outputKind":"resource","needsResource":true,"resourceAction":"provide_location","output":"structured_resource_commit"},
				{"taskId":"task-mini","intent":"hotel_variable","subIntent":"mini_program","outputKind":"resource","needsResource":true,"resourceAction":"send_miniprogram","output":"structured_resource_commit"}
			]}
		}
	}`)}
	parts := buildTextCommitParts(trace, "酒店地址：测试路1号。\n<<NEXT_MESSAGE>>\n入住小程序入口：智慧入住。")
	parts = bindFallbackResourceTextParts(trace, nil, parts)
	if len(parts) != 2 {
		t.Fatalf("two resource fallbacks must stay independently attributable, got %#v", parts)
	}
	if parts[0].FallbackResourceType != "location" || strings.Join(parts[0].TaskIDs, ",") != "task-location" {
		t.Fatalf("location fallback lost Task ownership: %#v", parts[0])
	}
	if parts[1].FallbackResourceType != "mini_program" || strings.Join(parts[1].TaskIDs, ",") != "task-mini" {
		t.Fatalf("mini-program fallback lost Task ownership: %#v", parts[1])
	}
}

func TestCapTextCommitPartsPreservesAllFallbackResourceTypes(t *testing.T) {
	parts := []textCommitPart{
		{Content: manualResumeCustomerNotice},
		{Content: "酒店地址：测试路1号。", TaskIDs: []string{"task-location"}, FallbackResourceType: "location"},
		{Content: "入住小程序入口：智慧入住。", TaskIDs: []string{"task-mini"}, FallbackResourceType: "mini_program"},
		{Content: "门店电话：12345678。", TaskIDs: []string{"task-phone"}, FallbackResourceType: "phone"},
		{Content: "房型图片如下。", TaskIDs: []string{"task-image"}, FallbackResourceType: "knowledge_image"},
	}

	capped := capTextCommitParts(parts, 3)
	if len(capped) != 3 {
		t.Fatalf("manual resume text parts must be capped at three, got %#v", capped)
	}
	allResourceTypes := make([]string, 0, 4)
	allTaskIDs := make([]string, 0, 4)
	for _, part := range capped {
		allResourceTypes = append(allResourceTypes, textCommitPartFallbackResourceTypes(part)...)
		allTaskIDs = append(allTaskIDs, part.TaskIDs...)
	}
	if strings.Join(allResourceTypes, ",") != "location,mini_program,phone,knowledge_image" {
		t.Fatalf("message capping must preserve every fallback resource type: %#v", capped)
	}
	if strings.Join(allTaskIDs, ",") != "task-location,task-mini,task-phone,task-image" {
		t.Fatalf("message capping must preserve every fallback resource Task: %#v", capped)
	}
	if capped[0].FallbackResourceType != "location" {
		t.Fatalf("the legacy singular fallback resource type must remain populated: %#v", capped[0])
	}
	if strings.Join(capped[1].FallbackResourceTypes, ",") != "mini_program,phone" {
		t.Fatalf("a merged fallback message must retain all resource types: %#v", capped[1])
	}
	traceItem := map[string]any{}
	addFallbackResourceTypesToCommitTrace(traceItem, capped[1])
	if traceItem["fallbackResourceType"] != "mini_program" {
		t.Fatalf("Commit Trace must retain the legacy singular resource type: %#v", traceItem)
	}
	resourceTypes, ok := traceItem["fallbackResourceTypes"].([]string)
	if !ok || strings.Join(resourceTypes, ",") != "mini_program,phone" {
		t.Fatalf("Commit Trace must persist every merged fallback resource type: %#v", traceItem)
	}
}

func TestValidateCommittedMessageMatchesRequestRejectsStaleIdempotentText(t *testing.T) {
	message := &models.Message{
		RequestID:   "manual_resume_token_101",
		MessageType: enums.IMMessageTypeText,
		Content:     "早餐时间是7:00-9:30。",
		Payload:     `{"source":"knowledge","version":1}`,
	}
	if err := validateCommittedMessageMatchesRequest(
		message,
		enums.IMMessageTypeText,
		"早餐时间是7:00-9:30。",
		`{"version":1,"source":"knowledge"}`,
		"manual_resume_token_101",
	); err != nil {
		t.Fatalf("the persisted message for the same request must remain idempotent: %v", err)
	}
	if err := validateCommittedMessageMatchesRequest(
		message,
		enums.IMMessageTypeText,
		"停车从辅路入口进入。",
		message.Payload,
		message.RequestID,
	); err == nil {
		t.Fatal("an old message returned by a stable ClientMsgID must not be rebound to new Task content")
	}
	if err := validateCommittedMessageMatchesRequest(
		message,
		enums.IMMessageTypeText,
		message.Content,
		message.Payload,
		"manual_resume_token_102",
	); err == nil {
		t.Fatal("an old message from another source-bound request must not satisfy the current request")
	}
}

func TestValidateCommittedMessageMatchesRequestAcceptsStableTaskBoundManualResumeMessage(t *testing.T) {
	message := &models.Message{
		RequestID:   "manual_resume_token_101",
		ClientMsgID: "ai_manual_resume_0123456789abcdef_task_abcdef0123456789_101",
		MessageType: enums.IMMessageTypeText,
		Content:     "第一次已提交的早餐答复。",
	}
	if err := validateCommittedMessageMatchesRequest(
		message,
		enums.IMMessageTypeText,
		"重试时模型换了一种早餐说法。",
		"",
		message.RequestID,
		message.ClientMsgID,
	); err != nil {
		t.Fatalf("stable Task-bound manual resume Commit must reuse the persisted message: %v", err)
	}
	if err := validateCommittedMessageMatchesRequest(
		message,
		enums.IMMessageTypeText,
		message.Content,
		"",
		"manual_resume_token_102",
		message.ClientMsgID,
	); err == nil {
		t.Fatal("stable Task ownership must not cross request boundaries")
	}
}

func TestManualResumeTextCommitIDsFollowTaskOwnership(t *testing.T) {
	input := replyCommitInput{Message: models.Message{ID: 101, RequestID: "manual_resume_token_101"}}
	prefix := replyCommitClientPrefix(input)
	first := textCommitClientMessageID(input, prefix, textCommitPart{Content: "早餐答复", TaskIDs: []string{"task-1", "task-2"}}, 0, 2)
	retry := textCommitClientMessageID(input, prefix, textCommitPart{Content: "换一种早餐答复", TaskIDs: []string{"task-1", "task-2"}}, 1, 3)
	other := textCommitClientMessageID(input, prefix, textCommitPart{Content: "停车答复", TaskIDs: []string{"task-3"}}, 0, 1)
	if first != retry {
		t.Fatalf("same Task ownership must keep one ClientMsgID across wording/index changes: %q != %q", first, retry)
	}
	if first == other {
		t.Fatalf("different Task ownership must not share ClientMsgID %q", first)
	}
	if len(first) > 128 {
		t.Fatalf("stable ClientMsgID exceeds database limit: %d %q", len(first), first)
	}
}

func TestValidateCommittedMessageMatchesRequestUsesMediaAssetIdentity(t *testing.T) {
	message := &models.Message{
		RequestID:   "manual_resume_token_201",
		MessageType: enums.IMMessageTypeImage,
		Content:     "persisted-filename.png",
		Payload:     `{"assetId":"asset-1","wxMedia":{"mediaId":"media-1"}}`,
	}
	if err := validateCommittedMessageMatchesRequest(
		message,
		enums.IMMessageTypeImage,
		"客户可见标题",
		`{"assetId":"asset-1"}`,
		message.RequestID,
	); err != nil {
		t.Fatalf("canonical media normalization may change content and payload details while retaining the same asset: %v", err)
	}
	if err := validateCommittedMessageMatchesRequest(
		message,
		enums.IMMessageTypeImage,
		"客户可见标题",
		`{"assetId":"asset-2"}`,
		message.RequestID,
	); err == nil {
		t.Fatal("a different persisted media asset must not satisfy the current resource Task")
	}
}

func TestBuildCommitMessageTraceRecordsPersistedContent(t *testing.T) {
	message := &models.Message{ID: 77, MessageType: enums.IMMessageTypeText, Content: "数据库中的真实回复"}
	trace := buildCommitMessageTrace(message, enums.IMMessageTypeText, "", "本次模型生成但未落库的文本", []string{"task-1"}, nil)
	if got := strings.TrimSpace(fmt.Sprint(trace["content"])); got != message.Content {
		t.Fatalf("Commit Trace must describe the persisted customer-visible message, got %q", got)
	}
}

func TestSplitReplyTextForCommitCapsMessagesAtThree(t *testing.T) {
	trace := &aiReplyTraceData{Runtime: json.RawMessage(`{
		"pipeline":{"replyPlan":{"taskPlans":[
			{"intent":"hotel_info","output":"knowledge_text_reply"},
			{"intent":"hotel_info","output":"knowledge_text_reply"},
			{"intent":"hotel_info","output":"knowledge_text_reply"},
			{"intent":"hotel_info","output":"knowledge_text_reply"}
		]}}
	}`)}
	parts := splitReplyTextForCommit(trace, "一\n<<NEXT_MESSAGE>>\n二\n<<NEXT_MESSAGE>>\n三\n<<NEXT_MESSAGE>>\n四")
	if len(parts) != 3 || parts[0] != "一\n\n二" || parts[1] != "三" || parts[2] != "四" {
		t.Fatalf("expected replies to be balanced across three messages, got %#v", parts)
	}
}

func TestContainsInternalReplyProtocolShape(t *testing.T) {
	for _, text := range []string{
		`{"replyParts":[{"taskId":"task-1","content":"房间有空调。"}]}`,
		"```json\n{\"replyParts\":[{\"taskId\":\"task-1\",\"content\":\"房间有空调。\"}]}\n```",
		`{"taskId":"task-1","content":"房间有空调。"}`,
		`[{"taskId":"task-1","content":"房间有空调。"}]`,
		`{"replyParts":[]}`,
		`{"coveredFactIds":["F1"],"content":"房间有两瓶水。"}`,
	} {
		if !containsInternalReplyProtocolShape(text) {
			t.Fatalf("expected internal protocol payload to be rejected: %s", text)
		}
	}
	if containsInternalReplyProtocolShape("房间有空调，可以正常使用。") {
		t.Fatal("ordinary customer reply must not be rejected")
	}
	if containsInternalReplyProtocolShape("不好意思，这个需要同事处理，已经帮您转接。") {
		t.Fatal("ordinary human-service wording must not be rejected")
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
