package executor

import (
	"encoding/json"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestRuntimeCustomerScenarioIntentCorrections(t *testing.T) {
	t.Run("explicit external order request overrides delivery information context", func(t *testing.T) {
		intent := postprocessRuntimeModelIntent(runtimeScenarioTestIntent("那你直接帮我下单吧", "food_delivery", false), RunInput{
			UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "那你直接帮我下单吧"},
		}, adapter.HistoryBuildResult{}, nil)
		if len(intent.IntentTasks) != 1 || intent.IntentTasks[0].Intent != "service_request" ||
			intent.IntentTasks[0].SubIntent != "external_proxy_action" || intent.IntentTasks[0].Objective != "action_request" ||
			intent.IntentTasks[0].NeedsKnowledge || intent.IntentTasks[0].NeedsHumanRoute {
			t.Fatalf("external order request did not override the historical delivery-information route: %#v", intent)
		}
	})

	t.Run("personal checkout becomes order detail before locator preflight", func(t *testing.T) {
		intent := postprocessRuntimeModelIntent(runtimeScenarioTestIntent("我几点退房", "checkout_process", false), RunInput{
			UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "我几点退房"},
		}, adapter.HistoryBuildResult{}, nil)
		if len(intent.IntentTasks) != 1 || intent.IntentTasks[0].SubIntent != "order_detail" || intent.IntentTasks[0].NeedsTool ||
			intent.PrimaryIntent != "interaction" || !intent.NeedsClarification || intent.IntentTasks[0].ResolvedText != runtimePMSOrderPhoneClarification {
			t.Fatalf("personal checkout did not enter order_detail preflight: %#v", intent)
		}
	})

	t.Run("general checkout remains hotel policy", func(t *testing.T) {
		intent := postprocessRuntimeModelIntent(runtimeScenarioTestIntent("你们酒店几点退房", "checkout_process", false), RunInput{
			UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "你们酒店几点退房"},
		}, adapter.HistoryBuildResult{}, nil)
		if len(intent.IntentTasks) != 1 || intent.IntentTasks[0].SubIntent != "checkout_process" || intent.IntentTasks[0].NeedsTool ||
			!intent.IntentTasks[0].NeedsKnowledge || intent.NeedsClarification {
			t.Fatalf("general checkout was personalized: %#v", intent)
		}
	})

	t.Run("general membership program does not request a phone", func(t *testing.T) {
		intent := postprocessRuntimeModelIntent(runtimeScenarioTestIntent("你们有会员吗", "member_benefits", true), RunInput{
			UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "你们有会员吗"},
		}, adapter.HistoryBuildResult{}, nil)
		if len(intent.IntentTasks) != 1 || intent.IntentTasks[0].SubIntent != "store_knowledge" || intent.IntentTasks[0].NeedsTool ||
			!intent.IntentTasks[0].NeedsKnowledge || intent.NeedsClarification {
			t.Fatalf("general membership program incorrectly required customer identity: %#v", intent)
		}
	})

	t.Run("personal member benefits reuse a confirmed phone", func(t *testing.T) {
		history := adapter.HistoryBuildResult{RawItems: []models.Message{{
			ID: 1, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
			Content: "会员绑定手机号是13800138000",
		}}}
		intent := postprocessRuntimeModelIntent(runtimeScenarioTestIntent("我是会员有啥优惠", "store_knowledge", false), RunInput{
			UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "我是会员有啥优惠"},
		}, history, nil)
		if len(intent.IntentTasks) != 1 || intent.IntentTasks[0].SubIntent != "member_benefits" || !intent.IntentTasks[0].NeedsTool ||
			intent.NeedsClarification || runtimeIntentEntityValue(intent.IntentTasks[0].Entities, runtimeIntentEntityCustomerPhone) != "13800138000" ||
			strings.Contains(intent.IntentTasks[0].ResolvedText, "13800138000") {
			t.Fatalf("personal member benefits did not reuse the confirmed phone: %#v", intent)
		}
	})
}

func TestRuntimePMSSessionLocatorRetainsSuccessfulTargetRoomType(t *testing.T) {
	for _, test := range []struct {
		name     string
		original string
		resolved string
		entities []callbacks.IntentEntityTraceData
	}{
		{
			name: "structured target entity", original: "那换沐阳吧", resolved: "换到沐阳房型",
			entities: []callbacks.IntentEntityTraceData{{Type: runtimeIntentEntityTargetRoomType, Text: "沐阳"}},
		},
		{
			name: "selection retained only in resolved customer context", original: "房号我也不懂，你随便帮我选一间",
			resolved: "我想换个房间，手机号18569300806\n当前客户补充（以本次为准）：那换沐阳吧\n当前客户补充（以本次为准）：房号我也不懂，你随便帮我选一间",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			trace := callbacks.RuntimeTraceData{}
			trace.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{{
				TaskID: "task-room", Intent: "hotel_info", SubIntent: "room_change", Objective: "selection",
				OriginalText: test.original, Text: test.resolved, ResolvedText: test.resolved, Entities: test.entities,
				SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{Aspect: "pms_room_inventory", Statement: "沐阳有房"}},
			}}
			trace.Output.CommitMessages = []callbacks.CommitMessageTraceData{{Status: "sent", TaskIDs: []string{"task-room"}}}
			locator := runtimePMSSessionLocatorFromTrace(trace)
			if locator.TargetRoomTypeText != "沐阳" {
				t.Fatalf("successful room choice was not retained: %#v", locator)
			}
		})
	}
}

func TestRuntimePMSSessionLocatorRestoresTargetFromCustomerHistory(t *testing.T) {
	history := adapter.HistoryBuildResult{RawItems: []models.Message{
		{SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "我想换个房间，手机号18569300806"},
		{SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "当前可以换橙意、沐阳，您更想选哪一种？"},
		{SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "那换沐阳吧"},
		{SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "房号我不懂，你随便选一间"},
	}}
	locator := runtimePMSSessionLocatorFromHistory(history)
	if locator.Phone != "18569300806" || locator.TargetRoomTypeText != "沐阳" {
		t.Fatalf("rapid customer turns lost the selected room type: %#v", locator)
	}

	history.RawItems = append(history.RawItems,
		models.Message{SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "不换沐阳了，换橙意"},
	)
	locator = runtimePMSSessionLocatorFromHistory(history)
	if locator.TargetRoomTypeText != "橙意" {
		t.Fatalf("corrected room choice did not replace the old target: %#v", locator)
	}
}

func TestJevStateUsesRecentUniqueBusinessTaskFromSameSession(t *testing.T) {
	db := setupRuntimeIntentConfigTestDB(t)
	conversation := models.Conversation{ID: 8101}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	source := models.Message{
		ID: 8102, ConversationID: conversation.ID, SessionNo: 7, ClientMsgID: "recent-business-source", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "帮我查这个订单的退房时间",
	}
	if err := db.Create(&source).Error; err != nil {
		t.Fatalf("create source message: %v", err)
	}
	if err := db.Create(&models.AgentRunLog{
		ConversationID: conversation.ID, MessageID: source.ID, AIAgentID: 91,
		FinalStatus: "completed", TraceData: runtimeProductionPMSOrderTraceFixture(),
	}).Error; err != nil {
		t.Fatalf("create run log: %v", err)
	}

	req := RunInput{
		Conversation: conversation,
		UserMessage:  models.Message{ID: 8200, ConversationID: conversation.ID, SessionNo: 7, MessageType: enums.IMMessageTypeText, Content: "那你回答啊"},
		AIAgent:      models.AIAgent{ID: 91},
	}
	sources := adapter.BuildCurrentTurnSources(req.UserMessage)
	state := buildJevIntentState(req, adapter.HistoryBuildResult{}, sources)
	if state.RecentBusinessTask == nil || state.RecentBusinessTask.Ref != "R1" || state.RecentBusinessTask.SubIntent != "order_detail" ||
		!strings.Contains(state.RecentBusinessTask.ResolvedText, "本次查询订单定位：接待单ID:REC-8102") {
		t.Fatalf("same-session business task was not exposed to JEV: %#v", state.RecentBusinessTask)
	}
	spans := []jevIntentSpan{{Ref: "T1", SourceRef: "U1", Text: "那你回答啊"}}
	questions, contexts := buildJevClassificationQuestions(spans, state)
	if _, exists := jevTestCriteria(questions["T1_context"].Criteria)["R1"]; !exists || !strings.Contains(contexts["R1"].Text, "退房时间") {
		t.Fatalf("recent business task is not a selectable JEV context: questions=%#v contexts=%#v", questions["T1_context"], contexts)
	}
	result, err := buildIntentTraceFromJev(jevTestResponse(questions, map[string]string{
		"T1_route": "order_detail", "T1_objective": "time", "T1_relation": "reference_previous",
		"T1_resolution": "resolved_from_context", "T1_context": "R1",
	}, nil), spans, contexts)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.IntentTasks) != 1 || result.IntentTasks[0].SubIntent != "order_detail" || !result.IntentTasks[0].NeedsTool ||
		!strings.Contains(result.IntentTasks[0].ResolvedText, "帮我查这个订单的退房时间") {
		t.Fatalf("elliptical continuation did not inherit the unique business task: %#v", result.IntentTasks)
	}

	otherSession := req
	otherSession.UserMessage.SessionNo = 8
	otherState := buildJevIntentState(otherSession, adapter.HistoryBuildResult{}, sources)
	if otherState.RecentBusinessTask != nil {
		t.Fatalf("business task crossed session boundary: %#v", otherState.RecentBusinessTask)
	}
}

func TestRuntimeIndependentInteractionBlocksStaleBusinessContext(t *testing.T) {
	trace := callbacks.RuntimeTraceData{}
	trace.Pipeline.Intent.IntentTasks = []callbacks.IntentTaskTraceData{{
		Intent: "interaction", SubIntent: "acknowledgement", Text: "你好",
		RelationToPrevious: "independent", ResolutionState: runtimeIntentResolutionClear,
	}}
	if !runtimeTraceBlocksEarlierBusinessContext(trace) {
		t.Fatal("an independent greeting must stop stale business context lookup")
	}

	trace.Pipeline.Intent.IntentTasks[0].SubIntent = "frustration"
	trace.Pipeline.Intent.IntentTasks[0].Text = "你没回答我的问题"
	trace.Pipeline.Intent.IntentTasks[0].RelationToPrevious = "answer_rejected"
	trace.Pipeline.Intent.IntentTasks[0].ResolutionState = runtimeIntentResolutionResolvedFromContext
	if runtimeTraceBlocksEarlierBusinessContext(trace) {
		t.Fatal("a contextual rejection must preserve the active business goal")
	}
}

func TestRuntimePMSSessionLocatorUsesProductionTraceAcrossIntentAndReplyPlan(t *testing.T) {
	db := setupRuntimeIntentConfigTestDB(t)
	conversation := models.Conversation{ID: 8301}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	source := models.Message{
		ID: 8302, ConversationID: conversation.ID, SessionNo: 4, ClientMsgID: "production-trace-source", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "手机号13800138000，查我的退房时间",
	}
	if err := db.Create(&source).Error; err != nil {
		t.Fatalf("create source message: %v", err)
	}
	if err := db.Create(&models.AgentRunLog{
		ConversationID: conversation.ID, MessageID: source.ID, AIAgentID: 92,
		FinalStatus: "runtime_prepared", TraceData: runtimeProductionPMSOrderTraceFixture(),
	}).Error; err != nil {
		t.Fatalf("create production-style run log: %v", err)
	}

	locator := runtimePMSSessionLocatorForRequest(RunInput{
		Conversation: conversation,
		UserMessage:  models.Message{ID: 8400, ConversationID: conversation.ID, SessionNo: 4},
		AIAgent:      models.AIAgent{ID: 92},
	}, adapter.HistoryBuildResult{})
	if locator.Phone != "13800138000" || locator.OrderLocator != "接待单ID:REC-8102" {
		t.Fatalf("production trace did not restore both intent phone and resolved order: %#v", locator)
	}
}

func TestRuntimePMSSessionLocatorRestoresTargetFromResolvedRunContext(t *testing.T) {
	db := setupRuntimeIntentConfigTestDB(t)
	conversation := models.Conversation{ID: 8501}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	source := models.Message{
		ID: 8502, ConversationID: conversation.ID, SessionNo: 5, ClientMsgID: "room-choice-source", SeqNo: 1,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "房号我也不懂，你随便帮我选一间",
	}
	if err := db.Create(&source).Error; err != nil {
		t.Fatalf("create source message: %v", err)
	}
	trace := callbacks.RuntimeTraceData{Status: "completed"}
	trace.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-room", Intent: "hotel_info", SubIntent: "room_change", Objective: "recommendation",
		OriginalText: source.Content, Text: "我想换个房间，手机号18569300806\n当前客户补充（以本次为准）：那换沐阳吧\n当前客户补充（以本次为准）：房号我也不懂，你随便帮我选一间",
		ResolvedText:   "我想换个房间，手机号18569300806\n当前客户补充（以本次为准）：那换沐阳吧\n当前客户补充（以本次为准）：房号我也不懂，你随便帮我选一间",
		Entities:       []callbacks.IntentEntityTraceData{{Type: runtimeIntentEntityCustomerPhone, Text: "18569300806"}},
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{Aspect: "pms_room_inventory", Statement: "沐阳可售1间"}},
	}}
	trace.Tools.Items = []callbacks.ToolTraceItem{{ToolCode: "builtin/pms_query", ToolName: "pms_query", Status: "ok"}}
	trace.Output.CommitMessages = []callbacks.CommitMessageTraceData{{Status: "sent", TaskIDs: []string{"task-room"}}}
	raw, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}
	if err := db.Create(&models.AgentRunLog{
		ConversationID: conversation.ID, MessageID: source.ID, AIAgentID: 93,
		FinalStatus: "runtime_prepared", TraceData: string(raw),
	}).Error; err != nil {
		t.Fatalf("create run log: %v", err)
	}

	locator := runtimePMSSessionLocatorForRequest(RunInput{
		Conversation: conversation,
		UserMessage:  models.Message{ID: 8600, ConversationID: conversation.ID, SessionNo: 5},
		AIAgent:      models.AIAgent{ID: 93},
	}, adapter.HistoryBuildResult{})
	if locator.Phone != "18569300806" || locator.TargetRoomTypeText != "沐阳" {
		t.Fatalf("resolved room choice was not restored from recent run: %#v", locator)
	}
}

func runtimeScenarioTestIntent(text string, subIntent string, needsTool bool) callbacks.IntentTraceData {
	return callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info", IntentConfidence: 0.95, ShouldReply: true,
		SemanticContractExpected: true, SourceRefsValidated: true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: "hotel_info", SubIntent: subIntent, Objective: "availability",
			RelationToPrevious: "independent", ResolutionState: runtimeIntentResolutionClear,
			Text: text, ResolvedText: text, SourceRefs: []string{"U1"}, NeedsTool: needsTool, NeedsKnowledge: !needsTool,
		}},
	}
}

func runtimeProductionPMSOrderTraceFixture() string {
	return `{
  "status": "completed",
  "aiConfigId": 7,
  "modelSource": "store_model_setting",
  "replySent": true,
  "runtime": {
    "version": "v1",
    "status": "completed",
    "input": {
      "currentUserMessagePreview": "手机号13800138000，查我的退房时间",
      "currentTurnSources": [{"ref":"U1","messageId":8102,"messageType":"text","text":"手机号13800138000，查我的退房时间"}]
    },
    "pipeline": {
      "intent": {
        "primaryIntent": "hotel_info",
        "subIntent": "order_detail",
        "needsTool": true,
        "intentTasks": [{
          "intent": "hotel_info",
          "subIntent": "order_detail",
          "objective": "time",
          "relationToPrevious": "independent",
          "resolutionState": "clear",
          "text": "查我的退房时间",
          "resolvedText": "查我的退房时间\n本次查询手机号：13800138000",
          "sourceRefs": ["U1"],
          "needsTool": true
        }]
      },
      "replyPlan": {
        "intent": "hotel_info",
        "activeTaskCount": 1,
        "replyRequiredTaskCount": 1,
        "taskPlans": [{
          "taskId": "task-order-detail",
          "intent": "hotel_info",
          "subIntent": "order_detail",
          "objective": "time",
          "relationToPrevious": "independent",
          "resolutionState": "clear",
          "text": "查我的退房时间",
          "originalText": "查我的退房时间",
          "resolvedText": "帮我查这个订单的退房时间\n本次查询订单定位：接待单ID:REC-8102",
          "sourceRefs": ["U1"],
          "outputKind": "text",
          "replyRequired": true,
          "output": "text_reply",
          "supportedFacts": [{"factId":"P1F1","aspect":"pms_order_recept","statement":"PMS 当前接待单离店时间为2026-09-24 12:00:00。","criticalValues":["2026-09-24 12:00:00"]}]
        }]
      }
    },
    "tools": {
      "count": 1,
      "items": [{"toolCode":"builtin/pms_query","toolName":"pms_query","arguments":{"action":"recept_order_by_phone"},"status":"ok","latencyMs":32}]
    },
    "output": {
      "replyText": "您这笔订单的退房时间是9月24日12:00。",
      "finishReason": "committed_reply",
      "commitMessages": [{"messageId":9001,"messageType":"text","content":"您这笔订单的退房时间是9月24日12:00。","status":"sent","taskIds":["task-order-detail"]}]
    }
  }
}`
}
