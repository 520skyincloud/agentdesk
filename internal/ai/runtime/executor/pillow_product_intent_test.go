package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestRuntimePillowPurchaseIntentMapsToStructuredProduct(t *testing.T) {
	for _, text := range []string{
		"你们酒店同款枕头怎么买",
		"我想购买你们的枕头",
		"有这个枕头的链接吗",
		"这个枕头怎么下单",
		"这个枕头多少钱",
	} {
		intent := postprocessRuntimeModelIntent(runtimePillowIntentFixture(text, "hotel_info", "store_knowledge", ""), RunInput{
			UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: text},
		}, adapter.HistoryBuildResult{}, nil)
		if intent.PrimaryIntent != "hotel_variable" || intent.SubIntent != "pillow_product" || !intent.NeedsResource ||
			intent.ResourceAction != "provide_pillow_product" || len(intent.IntentTasks) != 1 ||
			intent.IntentTasks[0].ResourceAction != "provide_pillow_product" {
			t.Fatalf("purchase text %q did not map to pillow product: %#v", text, intent)
		}
		plans := buildReplyTaskPlans(intent)
		if len(plans) != 1 || plans[0].OutputKind != "resource" || plans[0].Output != "structured_resource_commit" || plans[0].ResourceAction != "provide_pillow_product" {
			t.Fatalf("purchase text %q did not create a resource commit task: %#v", text, plans)
		}
	}
}

func TestRuntimePillowRoomServiceNeverMapsToProduct(t *testing.T) {
	for _, text := range []string{
		"送两个枕头到房间",
		"帮我换个枕头",
		"再加个枕头",
		"枕头脏了",
		"枕头坏了",
		"这个枕头不舒服",
	} {
		intent := postprocessRuntimeModelIntent(runtimePillowIntentFixture(text, "hotel_variable", "pillow_product", "provide_pillow_product"), RunInput{
			UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: text},
		}, adapter.HistoryBuildResult{}, nil)
		if intent.PrimaryIntent != "service_request" || intent.NeedsResource || intent.ResourceAction != "" || len(intent.ResourceActions) != 0 ||
			len(intent.IntentTasks) != 1 || intent.IntentTasks[0].Intent != "service_request" || intent.IntentTasks[0].ResourceAction != "" {
			t.Fatalf("room-service text %q still carried product action: %#v", text, intent)
		}
	}
}

func TestJevPillowRouteIsStructuredResource(t *testing.T) {
	if _, ok := jevIntentRouteCriteria()["provide_pillow_product"]; !ok {
		t.Fatal("JEV route criteria must expose provide_pillow_product")
	}
	if !semanticGateAllowedResourceAction("provide_pillow_product") {
		t.Fatal("semantic gate must allow provide_pillow_product")
	}
	resourceType, action := normalizeHotelVariableResourceAction("provide_pillow_product", "", "")
	if resourceType != "pillow_product" || action != "provide_pillow_product" {
		t.Fatalf("unexpected pillow resource normalization: type=%q action=%q", resourceType, action)
	}
}

func runtimePillowIntentFixture(text string, intent string, subIntent string, resourceAction string) callbacks.IntentTraceData {
	return callbacks.IntentTraceData{
		PrimaryIntent: intent, IntentConfidence: 0.95, ShouldReply: true,
		SemanticContractExpected: true, SourceRefsValidated: true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: intent, SubIntent: subIntent, Objective: "action_request",
			RelationToPrevious: "independent", ResolutionState: runtimeIntentResolutionClear,
			Text: text, ResolvedText: text, SourceRefs: []string{"U1"},
			NeedsKnowledge: intent == "hotel_info", NeedsResource: resourceAction != "", ResourceAction: resourceAction,
		}},
	}
}
