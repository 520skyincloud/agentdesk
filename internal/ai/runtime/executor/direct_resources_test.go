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

func normalizeForDirectResources(text string, primary string) callbacks.IntentTraceData {
	return normalizeModelIntentTrace(callbacks.IntentTraceData{
		PrimaryIntent: primary, IntentConfidence: 0.9, ShouldReply: true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Sequence: 1, Intent: primary, Text: text, RequestMode: "request_action", Confidence: 0.9,
		}},
	}, RunInput{UserMessage: models.Message{Content: text}}, adapter.HistoryBuildResult{}, directResourcesConfigs())
}

func TestCheckinExecutionRoutesToMiniProgramDirect(t *testing.T) {
	for _, text := range []string{"给我办入住", "帮我办个入住", "我要入住", "办入住", "入住", "给我办入组"} {
		intent := normalizeForDirectResources(text, "service_request")
		if intent.PrimaryIntent != "hotel_variable" || intent.SubIntent != "mini_program" ||
			!intent.NeedsResource || intent.NeedsKnowledge || intent.NeedsHumanRoute {
			t.Fatalf("%s must route to mini program direct resource: %#v", text, intent)
		}
		if len(intent.ResourceActions) != 1 || intent.ResourceActions[0] != "provide_mini_program" {
			t.Fatalf("%s unexpected resource actions: %#v", text, intent.ResourceActions)
		}
	}
}

func TestCheckinExceptionsAndConsultationStayKnowledge(t *testing.T) {
	for _, text := range []string{
		"另一间房怎么办入住", "我有两间房，另一间房办不了入住", "手机不能用，怎么办入住",
		"怎么办理入住", "入住流程是什么", "入住需要什么", "几点可以入住",
	} {
		intent := normalizeForDirectResources(text, "hotel_info")
		if intent.PrimaryIntent == "hotel_variable" && intent.SubIntent == "mini_program" {
			t.Fatalf("%s must stay on knowledge path: %#v", text, intent)
		}
		if !intent.NeedsKnowledge {
			t.Fatalf("%s must keep knowledge capability: %#v", text, intent)
		}
	}
}

func TestHotelLocationRequestRoutesToLocationCard(t *testing.T) {
	for _, text := range []string{"酒店地址发我", "发个定位", "定位发我", "发我定位", "发一下酒店定位"} {
		intent := normalizeForDirectResources(text, "hotel_info")
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
		intent := normalizeForDirectResources(text, "hotel_info")
		if intent.PrimaryIntent == "hotel_variable" && intent.SubIntent == "location" {
			t.Fatalf("%s must not send hotel location card: %#v", text, intent)
		}
	}
}
