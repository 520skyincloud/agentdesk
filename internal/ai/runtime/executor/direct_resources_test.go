package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func directResourcesConfigs() []models.ReplyIntentConfig {
	return []models.ReplyIntentConfig{
		{Code: "hotel_info", Status: enums.StatusOk},
		{Code: "hotel_variable", Status: enums.StatusOk, NeedsResource: true, ResourceType: "store_variable"},
		{Code: "service_request", Status: enums.StatusOk},
	}
}

func normalizeForDirectResources(text string, primary string, subIntent string) callbacks.IntentTraceData {
	return normalizeModelIntentTrace(callbacks.IntentTraceData{
		PrimaryIntent: primary, SubIntent: subIntent, IntentConfidence: 0.9, ShouldReply: true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Sequence: 1, Intent: primary, SubIntent: subIntent, Text: text, RequestMode: "request_action", Confidence: 0.9,
		}},
	}, RunInput{UserMessage: models.Message{Content: text}}, adapter.HistoryBuildResult{}, directResourcesConfigs())
}

func TestHotelLocationRequestRoutesToLocationCard(t *testing.T) {
	for _, text := range []string{"酒店地址发我", "发个定位", "定位发我", "发我定位", "发一下酒店定位"} {
		intent := normalizeForDirectResources(text, "hotel_variable", "location")
		if intent.PrimaryIntent != "hotel_variable" || intent.SubIntent != "location" ||
			!intent.NeedsResource || intent.NeedsKnowledge {
			t.Fatalf("%s must route to location card direct: %#v", text, intent)
		}
		if len(intent.ResourceActions) != 1 || intent.ResourceActions[0] != "provide_location" {
			t.Fatalf("%s unexpected resource actions: %#v", text, intent.ResourceActions)
		}
	}
}

func TestExternalLocationAndAddressQuestionsStayKnowledge(t *testing.T) {
	for _, text := range []string{"附近有什么商场", "外卖地址填哪里", "地铁站怎么去", "周边有什么好玩的", "酒店在哪", "酒店在哪里", "定位发我，小程序也发一下，停车在哪"} {
		intent := normalizeForDirectResources(text, "hotel_info", "surrounding_facilities")
		if intent.PrimaryIntent == "hotel_variable" && intent.SubIntent == "location" {
			t.Fatalf("%s must not send hotel location card: %#v", text, intent)
		}
	}
}
