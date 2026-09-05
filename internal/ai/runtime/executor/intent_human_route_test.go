package executor

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"
)

func TestExecuteRuntimeHandoffDirectiveCollectsRoomBeforeDirectDispatch(t *testing.T) {
	db := setupRuntimeIntentConfigTestDB(t)
	if err := db.AutoMigrate(&models.ConversationReadState{}, &models.ConversationEventLog{}); err != nil {
		t.Fatalf("auto migrate handoff message tables: %v", err)
	}
	conversation := models.Conversation{ID: 82, CustomerID: 902, AIAgentID: 17, Status: enums.IMConversationStatusAIServing}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		ConversationID:   conversation.ID,
		WxWorkInstanceID: 772,
		RouteStatus:      enums.ConversationRouteStatusAIServing,
		RouteTarget:      "ai",
		SessionNo:        1,
	}).Error; err != nil {
		t.Fatalf("create route state: %v", err)
	}
	message := models.Message{
		ID:             8201,
		ConversationID: conversation.ID,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeText,
		Content:        "马桶堵了",
		RequestID:      "req-toilet",
	}
	if err := db.Create(&message).Error; err != nil {
		t.Fatalf("create customer message: %v", err)
	}
	summary := &RunResult{
		handoffDirective:       true,
		handoffDirectiveReason: "知识库规则要求门店同事接手；客户消息：马桶堵了",
		handoffDirectiveSource: "knowledge_top_answer",
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetActionLedger(callbacks.ActionLedgerTraceData{})

	handled, err := executeRuntimeHandoffDirective(RunInput{
		Conversation: conversation,
		UserMessage:  message,
		AIAgent:      models.AIAgent{ID: 17, Name: "AI", Status: enums.StatusOk},
	}, summary, collector)
	if err != nil || !handled {
		t.Fatalf("expected room collection before direct handoff, handled=%v err=%v", handled, err)
	}
	if summary.handoffDispatchStatus != string(services.HandoffDispatchStatusAwaitingRoomNumber) {
		t.Fatalf("expected awaiting_room_number status, got %q", summary.handoffDispatchStatus)
	}
	state := services.ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.PendingAction != string(enums.ConversationPendingActionHumanHandoff) {
		t.Fatalf("expected pending room-number collection, got %+v", state)
	}
	var reply models.Message
	if err := db.Where("conversation_id = ? AND sender_type = ?", conversation.ID, enums.IMSenderTypeAI).Order("id DESC").First(&reply).Error; err != nil {
		t.Fatalf("load room-number reply: %v", err)
	}
	if reply.Content != "方便说下是哪个房间吗？" {
		t.Fatalf("expected exact room collection prompt, got %q", reply.Content)
	}
	if !strings.Contains(state.PendingActionPayload, `"awaitingField":"room_number"`) {
		t.Fatalf("expected room-number pending field, got %+v", state)
	}
	if !actionLedgerContainsActionWithStatus(collector.Data.ActionLedger.CommittedActions, "human_route", string(services.HandoffDispatchStatusAwaitingRoomNumber)) {
		t.Fatalf("expected committed handoff action, got %#v", collector.Data.ActionLedger)
	}
	if len(collector.Data.GraphTools.Items) != 1 || collector.Data.GraphTools.Items[0].RecommendedAction != "collect_handoff_room_number" {
		t.Fatalf("expected room collection trace, got %+v", collector.Data.GraphTools.Items)
	}
	assertNoHandoffConfirmationProtocol(t, reply.Content+"\n"+collector.Marshal())
}

func TestExecuteIntentHumanRouteDispatchesExplicitAndRejectedAnswersDirectly(t *testing.T) {
	tests := []struct {
		name      string
		subIntent string
		message   string
		inquiry   bool
	}{
		{name: "explicit handoff", subIntent: "explicit_handoff", message: "别机器人了，帮我转人工"},
		{name: "answer rejected", subIntent: "answer_rejected", message: "你刚才答非所问，找同事来处理"},
		{name: "knowledge inquiry skips room collection", message: "外卖机器人能送到房间门口吗？", inquiry: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupRuntimeIntentConfigTestDB(t)
			if err := db.AutoMigrate(
				&models.AIAgent{},
				&models.AgentTeam{},
				&models.AgentTeamSchedule{},
				&models.AIManualResumeTask{},
				&models.ConversationReadState{},
				&models.ConversationEventLog{},
			); err != nil {
				t.Fatalf("auto migrate direct handoff tables: %v", err)
			}

			const teamID int64 = 9401
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
				ID:          9402,
				Name:        "AI",
				TeamIDs:     "9401",
				ServiceMode: enums.IMConversationServiceModeAIFirst,
				Status:      enums.StatusOk,
			}
			if err := db.Create(&aiAgent).Error; err != nil {
				t.Fatalf("create ai agent: %v", err)
			}
			conversation := models.Conversation{
				ID:          9403,
				CustomerID:  9404,
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
			message := models.Message{
				ID:             9405,
				ConversationID: conversation.ID,
				ClientMsgID:    "customer-direct-handoff",
				SeqNo:          1,
				SenderType:     enums.IMSenderTypeCustomer,
				MessageType:    enums.IMMessageTypeText,
				Content:        tt.message,
				RequestID:      "req-direct-handoff",
				SendStatus:     enums.IMMessageStatusSent,
				SentAt:         &now,
			}
			if err := db.Create(&message).Error; err != nil {
				t.Fatalf("create customer message: %v", err)
			}

			collector := callbacks.NewRuntimeTraceCollector()
			collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
				PrimaryIntent:   "human_complaint_risk",
				SubIntent:       tt.subIntent,
				NeedsHumanRoute: true,
			}
			summary := &RunResult{}
			req := RunInput{
				Conversation: conversation,
				UserMessage:  message,
				AIAgent:      aiAgent,
			}
			var handled bool
			var err error
			if tt.inquiry {
				collector.Data.Pipeline.EvidenceJudge.DeferredTaskIDs = []string{"task-1"}
				collector.Data.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{{
					TaskID: "task-1", Intent: "hotel_info", Objective: "policy", ResolvedText: tt.message,
				}}
				summary.handoffDirective = true
				summary.handoffDirectiveReason = "完整待处理问题：" + tt.message
				handled, err = executeRuntimeHandoffDirective(req, summary, collector)
			} else {
				handled, err = executeIntentHumanRoute(t.Context(), req, summary, collector)
			}
			if err != nil || !handled {
				t.Fatalf("expected direct handoff, handled=%v err=%v", handled, err)
			}
			if summary.handoffDispatchStatus != string(services.HandoffDispatchStatusDispatched) {
				t.Fatalf("expected dispatched status, got %q", summary.handoffDispatchStatus)
			}
			state := services.ConversationRouteService.GetByConversationID(conversation.ID)
			if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.PendingAction != "" {
				t.Fatalf("expected direct manual route without a pending action, got %+v", state)
			}
			var replies []models.Message
			if err := db.Where("conversation_id = ? AND sender_type = ?", conversation.ID, enums.IMSenderTypeAI).Order("seq_no ASC, id ASC").Find(&replies).Error; err != nil {
				t.Fatalf("load direct handoff replies: %v", err)
			}
			if len(replies) != 1 || replies[0].Content != services.DirectHandoffSuccessMessage {
				t.Fatalf("expected one exact success message, got %+v", replies)
			}
			if len(collector.Data.GraphTools.Items) != 1 || collector.Data.GraphTools.Items[0].RecommendedAction != "dispatch_human_route" {
				t.Fatalf("expected direct dispatch trace, got %+v", collector.Data.GraphTools.Items)
			}
			assertNoHandoffConfirmationProtocol(t, replies[0].Content+"\n"+collector.Marshal())
		})
	}
}

func TestRuntimeHandoffRoomNumberPolicyUsesOnlyPendingTaskSemantics(t *testing.T) {
	inquiry := callbacks.ReplyTaskPlanTraceData{TaskID: "inquiry", Intent: "hotel_info", Objective: "policy", ResolvedText: "机器人能送到房间吗"}
	service := callbacks.ReplyTaskPlanTraceData{TaskID: "service", Intent: "service_request", Objective: "action_request", ResolvedText: "帮我处理马桶堵塞"}
	unknown := callbacks.ReplyTaskPlanTraceData{TaskID: "unknown", Intent: "hotel_info", ResolvedText: "房间问题"}
	for _, tt := range []struct {
		name    string
		pending []string
		want    bool
		text    string
	}{
		{"inquiry only", []string{"inquiry"}, false, ""},
		{"service only", []string{"service"}, true, service.ResolvedText},
		{"mixed pending tasks", []string{"inquiry", "service"}, true, service.ResolvedText},
		{"legacy", nil, true, ""},
		{"missing task metadata", []string{"missing"}, true, ""},
		{"unknown objective", []string{"unknown"}, true, unknown.ResolvedText},
	} {
		t.Run(tt.name, func(t *testing.T) {
			collector := callbacks.NewRuntimeTraceCollector()
			collector.Data.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{inquiry, service, unknown}
			collector.Data.Pipeline.EvidenceJudge.DeferredTaskIDs = tt.pending
			got, text := runtimeHandoffRoomNumberPolicy(collector)
			if got != tt.want || text != tt.text {
				t.Fatalf("room policy must use only pending action tasks: got=(%v,%q), want=(%v,%q)", got, text, tt.want, tt.text)
			}
			deferred, deferredText := HandoffRoomNumberPolicyFromTrace(collector.Marshal())
			if deferred != got || deferredText != text {
				t.Fatalf("deferred and direct policy differ: direct=(%v,%q), deferred=(%v,%q)", got, text, deferred, deferredText)
			}
		})
	}
}

func TestApplyHandoffDispatchResultMapsDirectStatuses(t *testing.T) {
	tests := []struct {
		name   string
		status services.HandoffDispatchStatus
		action string
	}{
		{name: "awaiting room", status: services.HandoffDispatchStatusAwaitingRoomNumber, action: "collect_handoff_room_number"},
		{name: "dispatched", status: services.HandoffDispatchStatusDispatched, action: "dispatch_human_route"},
		{name: "already active", status: services.HandoffDispatchStatusAlreadyActive, action: "human_route_already_active"},
		{name: "off hours", status: services.HandoffDispatchStatusOffHours, action: "handoff_off_hours"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := &RunResult{}
			item := &callbacks.GraphToolTraceItem{}
			err := applyHandoffDispatchResult(summary, item, &services.HandoffDispatchResult{Status: tt.status}, false)
			if err != nil {
				t.Fatalf("applyHandoffDispatchResult() error = %v", err)
			}
			if summary.handoffDispatchStatus != string(tt.status) || item.Status != "success" || item.RecommendedAction != tt.action {
				t.Fatalf("unexpected mapped result: summary=%+v item=%+v", summary, item)
			}
			assertNoHandoffConfirmationProtocol(t, item.ResultPreview)
		})
	}
}

func TestIsEmergencySafetyHandoff(t *testing.T) {
	tests := []struct {
		name   string
		intent callbacks.IntentTraceData
		want   bool
	}{
		{
			name: "emergency safety",
			intent: callbacks.IntentTraceData{
				PrimaryIntent:   "human_complaint_risk",
				SubIntent:       "emergency_safety",
				NeedsHumanRoute: true,
			},
			want: true,
		},
		{
			name: "ordinary direct handoff",
			intent: callbacks.IntentTraceData{
				PrimaryIntent:   "human_complaint_risk",
				SubIntent:       "explicit_handoff",
				NeedsHumanRoute: true,
			},
			want: false,
		},
		{
			name: "service request is not handoff",
			intent: callbacks.IntentTraceData{
				PrimaryIntent:   "service_request",
				SubIntent:       "emergency_safety",
				NeedsHumanRoute: true,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEmergencySafetyHandoff(tt.intent); got != tt.want {
				t.Fatalf("isEmergencySafetyHandoff() = %v, want %v", got, tt.want)
			}
		})
	}
}

func actionLedgerContainsActionWithStatus(items []callbacks.ActionLedgerItem, action, status string) bool {
	for _, item := range items {
		if item.Action == action && item.Status == status {
			return true
		}
	}
	return false
}

func assertNoHandoffConfirmationProtocol(t *testing.T, value string) {
	t.Helper()
	lower := strings.ToLower(value)
	for _, forbidden := range []string{"confirmation", "回复“确认”", "确认或取消", "接待确认", "二次确认"} {
		if strings.Contains(lower, strings.ToLower(forbidden)) {
			t.Fatalf("direct handoff output still contains confirmation protocol %q: %s", forbidden, value)
		}
	}
}

func TestExecuteIntentHumanRouteSkipsCustomerWithAutoHandoffDisabled(t *testing.T) {
	db := setupRuntimeIntentConfigTestDB(t)
	conversation := models.Conversation{ID: 81, CustomerID: 901, Status: enums.IMConversationStatusAIServing}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		ConversationID:   conversation.ID,
		WxWorkInstanceID: 771,
		RouteStatus:      enums.ConversationRouteStatusAIServing,
		RouteTarget:      "ai",
		SessionNo:        1,
	}).Error; err != nil {
		t.Fatalf("create route: %v", err)
	}
	now := time.Now()
	if err := db.Model(&models.WxWorkCustomerHandoffSetting{}).Create(map[string]any{
		"customer_id":          conversation.CustomerID,
		"wx_work_instance_id":  int64(771),
		"auto_handoff_enabled": false,
		"created_at":           now,
		"updated_at":           now,
	}).Error; err != nil {
		t.Fatalf("create customer handoff setting: %v", err)
	}

	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:   "human_complaint_risk",
		SubIntent:       "explicit_handoff",
		NeedsHumanRoute: true,
	}
	handled, err := executeIntentHumanRoute(t.Context(), RunInput{Conversation: conversation}, &RunResult{}, collector)
	if err != nil || handled {
		t.Fatalf("expected disabled customer handoff to continue AI reply, handled=%v err=%v", handled, err)
	}
	if len(collector.Data.GraphTools.Items) != 1 || collector.Data.GraphTools.Items[0].RecommendedAction != "customer_auto_handoff_disabled" {
		t.Fatalf("expected trace to record disabled customer handoff, got %+v", collector.Data.GraphTools.Items)
	}
	if services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabled(conversation.CustomerID, 771) {
		t.Fatal("expected account-scoped setting to remain disabled")
	}
}

func TestDeferMixedExplicitIntentHumanRouteKeepsAnswerableTasks(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:   "human_complaint_risk",
		SubIntent:       "explicit_handoff",
		NeedsHumanRoute: true,
	}
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
		{TaskID: "task-2", Intent: "human_complaint_risk", SubIntent: "explicit_handoff", OutputKind: "handoff"},
	}}

	deferred := deferMixedExplicitIntentHumanRoute(RunInput{UserMessage: models.Message{Content: "早餐几点，另外转人工"}}, collector)
	if !deferred {
		t.Fatal("expected explicit handoff to wait until the answerable task is committed")
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if !trace.DeferredHandoff || len(trace.DeferredTaskIDs) != 1 || trace.DeferredTaskIDs[0] != "task-2" {
		t.Fatalf("unexpected deferred handoff trace: %#v", trace)
	}
	if !actionLedgerContainsAction(collector.Data.ActionLedger.RequestedActions, "human_route") {
		t.Fatalf("expected human route in the action ledger: %#v", collector.Data.ActionLedger)
	}
}

func TestDeferMixedExplicitIntentHumanRouteIgnoresDeferredKnowledgeTask(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:   "human_complaint_risk",
		SubIntent:       "explicit_handoff",
		NeedsHumanRoute: true,
	}
	collector.Data.Pipeline.EvidenceJudge = callbacks.KnowledgeEvidenceJudgeTraceData{
		DeferredHandoff:       true,
		DeferredHandoffReason: "部分知识问题需要同事处理",
		DeferredTaskIDs:       []string{"task-2"},
	}
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", Text: "早餐几点", OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
		{TaskID: "task-2", Intent: "hotel_info", Text: "汤东强是谁", OutputKind: "handoff", ReplyRequired: false, Output: runtimeKnowledgeDeferredHandoffOutput},
		{TaskID: "task-3", Intent: "human_complaint_risk", SubIntent: "explicit_handoff", Text: "转人工", OutputKind: "handoff", Output: "human_route_confirmation_or_dispatch"},
	}}

	deferred := deferMixedExplicitIntentHumanRoute(RunInput{UserMessage: models.Message{Content: "早餐几点，汤东强是谁，转人工"}}, collector)
	if !deferred {
		t.Fatal("explicit handoff must wait for the answerable sibling even when another knowledge Task is already deferred")
	}
	trace := collector.Data.Pipeline.EvidenceJudge
	if !trace.DeferredHandoff || strings.Join(trace.DeferredTaskIDs, ",") != "task-2,task-3" {
		t.Fatalf("both deferred knowledge and explicit handoff identities must survive: %#v", trace)
	}
}

func TestDeferMixedExplicitIntentHumanRouteKeepsPureAndRejectedRoutesImmediate(t *testing.T) {
	tests := []struct {
		name   string
		intent callbacks.IntentTraceData
		tasks  []callbacks.ReplyTaskPlanTraceData
	}{
		{
			name:   "pure explicit handoff",
			intent: callbacks.IntentTraceData{PrimaryIntent: "human_complaint_risk", SubIntent: "explicit_handoff", NeedsHumanRoute: true},
			tasks:  []callbacks.ReplyTaskPlanTraceData{{TaskID: "task-1", Intent: "human_complaint_risk", SubIntent: "explicit_handoff", OutputKind: "handoff"}},
		},
		{
			name:   "answer rejected remains immediate",
			intent: callbacks.IntentTraceData{PrimaryIntent: "human_complaint_risk", SubIntent: "answer_rejected", NeedsHumanRoute: true},
			tasks: []callbacks.ReplyTaskPlanTraceData{
				{TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
				{TaskID: "task-2", Intent: "human_complaint_risk", SubIntent: "answer_rejected", OutputKind: "handoff"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := callbacks.NewRuntimeTraceCollector()
			collector.Data.Pipeline.Intent = tt.intent
			collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: tt.tasks}
			if deferMixedExplicitIntentHumanRoute(RunInput{}, collector) {
				t.Fatal("expected the route to remain immediate")
			}
		})
	}
}
