package executor

import (
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"
)

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
			name: "ordinary handoff confirmation",
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

func TestExecuteIntentHumanRouteSkipsCustomerWithAutoHandoffDisabled(t *testing.T) {
	db := setupRuntimeIntentConfigTestDB(t)
	if err := db.Create(&models.Customer{ID: 901, TenantID: 101, Name: "测试客户", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if err := db.Create(&models.Store{ID: 701, TenantID: 101, StoreCode: "intent-route", Name: "意图测试门店", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create Store: %v", err)
	}
	if err := db.Create(&models.StoreStaffBinding{ID: 711, TenantID: 101, StoreID: 701, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create Store staff binding: %v", err)
	}
	if err := db.Create(&models.WxWorkProtocolInstance{ID: 771, TenantID: 101, Guid: "intent-route", StoreID: 701, StoreStaffBindingID: 711, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create WeCom instance: %v", err)
	}
	conversation := models.Conversation{ID: 81, TenantID: 101, StoreID: 701, StoreStaffBindingID: 711, CustomerID: 901, Status: enums.IMConversationStatusAIServing}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		TenantID:       101,
		ConversationID: conversation.ID,
		StoreID:        701, StoreStaffBindingID: 711, WxWorkInstanceID: 771,
		RouteStatus: enums.ConversationRouteStatusAIServing,
		RouteTarget: "ai",
		SessionNo:   1,
	}).Error; err != nil {
		t.Fatalf("create route: %v", err)
	}
	now := time.Now()
	if err := db.Model(&models.WxWorkCustomerHandoffSetting{}).Create(map[string]any{
		"tenant_id":              int64(101),
		"customer_id":            conversation.CustomerID,
		"store_staff_binding_id": int64(711),
		"auto_handoff_enabled":   false,
		"created_at":             now,
		"updated_at":             now,
	}).Error; err != nil {
		t.Fatalf("create customer handoff setting: %v", err)
	}

	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:   "human_complaint_risk",
		SubIntent:       "explicit_handoff",
		NeedsHumanRoute: true,
	}
	handled, err := executeIntentHumanRoute(t.Context(), RunInput{
		Conversation: conversation,
		UserMessage:  models.Message{MessageType: enums.IMMessageTypeText, Content: "帮我转人工"},
	}, &RunResult{}, collector)
	if err != nil || handled {
		t.Fatalf("expected disabled customer handoff to continue AI reply, handled=%v err=%v", handled, err)
	}
	if len(collector.Data.GraphTools.Items) != 1 || collector.Data.GraphTools.Items[0].RecommendedAction != "customer_auto_handoff_disabled" {
		t.Fatalf("expected trace to record disabled customer handoff, got %+v", collector.Data.GraphTools.Items)
	}
	if services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabled(conversation.CustomerID, 711) {
		t.Fatal("expected account-scoped setting to remain disabled")
	}
}

func TestDecideRuntimeIntentHandoffScopesTasksAndRejectsFalseExplicitRoute(t *testing.T) {
	req := RunInput{
		Conversation: models.Conversation{ID: 8, TenantID: 1, StoreID: 2, StoreStaffBindingID: 3},
		UserMessage: models.Message{
			ID: 11, SessionNo: 1, AIReplyTurnID: 21, AIReplyTurnVersion: 4,
			MessageType: enums.IMMessageTypeText, Content: "别机器人了，帮我转人工",
		},
	}
	summary := &RunResult{ReplyPlanV2: &contracts.ReplyPlanV2{TurnVersion: 4, Tasks: []contracts.ReplyPlanTaskV2{
		{TaskKey: "handoff-a", OutputMode: "handoff"},
		{TaskKey: "handoff-b", OutputMode: "handoff"},
	}}}
	intent := callbacks.IntentTraceData{PrimaryIntent: "human_complaint_risk", SubIntent: "explicit_handoff", NeedsHumanRoute: true}
	decision := decideRuntimeIntentHandoff(req, summary, intent, true)
	if decision.Mode != contracts.HandoffModeConfirm || decision.TurnID != 21 || len(decision.TaskKeys) != 2 {
		t.Fatalf("handoff decision lost turn/task scope: %+v", decision)
	}

	req.UserMessage.Content = "你说话真难听"
	decision = decideRuntimeIntentHandoff(req, summary, intent, true)
	if decision.Mode != contracts.HandoffModeNone || decision.ReasonCode != "explicit_handoff_not_present" {
		t.Fatalf("an explicit_handoff label without an explicit customer request must be blocked: %+v", decision)
	}

	intent.SubIntent = "insult_complaint"
	decision = decideRuntimeIntentHandoff(req, summary, intent, true)
	if decision.Mode != contracts.HandoffModeNone || decision.ReasonCode != "handoff_category_not_eligible" {
		t.Fatalf("ordinary insult must not enter handoff: %+v", decision)
	}
}
