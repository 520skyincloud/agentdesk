package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
)

func TestGenerateHistoryIsExcludedForIndependentTask(t *testing.T) {
	history := []models.Message{{ID: 1, Content: "上一轮错误酒店答案"}}
	plans := []callbacks.ReplyTaskPlanTraceData{{Intent: "hotel_info", SubIntent: "checkin_process", Text: "我要办入住"}}
	if got := runtimeGenerateRelevantHistory(history, plans); len(got) != 0 {
		t.Fatalf("independent task inherited old answer history: %#v", got)
	}
}

func TestGenerateHistoryIsKeptForAdjacentFollowUp(t *testing.T) {
	history := []models.Message{{ID: 1, Content: "您想吃什么口味？"}}
	plans := []callbacks.ReplyTaskPlanTraceData{{Intent: "hotel_info", SubIntent: "surrounding_facilities", Text: "麻辣口味的", RelationType: "follow_up"}}
	if got := runtimeGenerateRelevantHistory(history, plans); len(got) != 1 {
		t.Fatalf("follow-up lost required adjacent history: %#v", got)
	}
}
