package executor

import (
	"testing"
	"time"

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
