package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestNormalizeRuntimeIntentTasksRoutesAddressSubIntentToLocation(t *testing.T) {
	tasks := normalizeRuntimeIntentTasks([]callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", SubIntent: "address_for_delivery", Text: "外卖地址填哪里"},
	})
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	got := tasks[0]
	if got.Intent != "hotel_variable" || got.SubIntent != "location" || got.ResourceAction != "provide_location" {
		t.Fatalf("expected address_for_delivery -> hotel_variable/location/provide_location, got %+v", got)
	}
	if got.NeedsKnowledge {
		t.Fatalf("address request must not require knowledge, got NeedsKnowledge=true")
	}
}

func TestNormalizeRuntimeIntentTasksKeepsNormalHotelInfo(t *testing.T) {
	tasks := normalizeRuntimeIntentTasks([]callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", SubIntent: "network_wifi", Text: "wifi密码多少"},
	})
	if len(tasks) != 1 || tasks[0].Intent != "hotel_info" || !tasks[0].NeedsKnowledge {
		t.Fatalf("expected normal hotel_info kept, got %+v", tasks)
	}
}
