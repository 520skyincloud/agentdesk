package executor

import (
	"context"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"
	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type stubRuntimeIntentModelDetector struct {
	intent callbacks.IntentTraceData
	err    error
}

func (s stubRuntimeIntentModelDetector) DetectRuntimeIntent(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, error) {
	return s.intent, s.err
}

func TestRuntimePipelineBuildsIntentPromptAndReplyPlan(t *testing.T) {
	req := RunInput{
		Conversation: models.Conversation{ID: 7},
		UserMessage:  models.Message{ID: 10, ConversationID: 7, MessageType: enums.IMMessageTypeText, Content: "你看我吃得怎么样"},
		AIAgent:      models.AIAgent{KnowledgeIDs: "1,2"},
	}
	history := adapter.HistoryBuildResult{
		Messages: nil,
		RawItems: []models.Message{{
			ID:             9,
			ConversationID: 7,
			MessageType:    enums.IMMessageTypeImage,
			Content:        "food.jpg",
			Payload:        `{"mediaText":"图片中有米饭、鸡肉、青菜和一杯饮品。","mediaUnderstandingStatus":"understood"}`,
		}},
		MemorySource:    "message_digest",
		MemoryItemCount: 3,
	}
	plan := buildRuntimePipelinePlan(req, history)
	if plan.Normalize.CurrentUserText == "" {
		t.Fatal("expected normalize current user text")
	}
	if plan.Intent.PrimaryIntent == "" {
		t.Fatal("expected intent")
	}
	if plan.Intent.PrimaryIntent != "social_confirm" || plan.Intent.SubIntent != "media_context_follow_up" {
		t.Fatalf("expected context follow-up under social_confirm, got %#v", plan.Intent)
	}
	if plan.PromptSelect.PackName == "" || len(plan.PromptSelect.Instructions) == 0 {
		t.Fatal("expected intent prompt pack")
	}
	if plan.Context.CompressedMemorySource != "message_digest" {
		t.Fatalf("expected compressed memory source, got %q", plan.Context.CompressedMemorySource)
	}
	if plan.ReplyPlan.AnswerGoal == "" {
		t.Fatal("expected reply plan")
	}
	if len(plan.Validate.Rules) == 0 {
		t.Fatal("expected validate rules")
	}
	if plan.Prompt == "" {
		t.Fatal("expected staged prompt")
	}
}

func TestRuntimePipelineNoReplyForPlainMediaOnly(t *testing.T) {
	req := RunInput{
		Conversation: models.Conversation{ID: 7},
		UserMessage: models.Message{
			ID:             10,
			ConversationID: 7,
			MessageType:    enums.IMMessageTypeImage,
			Content:        "selfie.jpg",
			Payload:        `{"mediaText":"图片为客人自拍，无清晰文字、报错或明确服务诉求信息。","mediaUnderstandingStatus":"understood"}`,
		},
	}
	plan := buildRuntimePipelinePlan(req, adapter.HistoryBuildResult{})
	if plan.Intent.PrimaryIntent != "context_media" || plan.Intent.SubIntent != "media_only_no_question" {
		t.Fatalf("expected context media gate, got %#v", plan.Intent)
	}
	if plan.Intent.ShouldReply {
		t.Fatal("expected no reply for plain media-only message")
	}
}

func TestRuntimePipelinePlainMediaAfterTextQuestionShouldReply(t *testing.T) {
	req := RunInput{
		Conversation: models.Conversation{ID: 7},
		UserMessage: models.Message{
			ID:             11,
			ConversationID: 7,
			SenderType:     enums.IMSenderTypeCustomer,
			MessageType:    enums.IMMessageTypeImage,
			Content:        "funny.jpg",
			Payload:        `{"mediaText":"图片为一只戴着黑色猫耳头套、手持玩具手枪并抱着印有美元符号麻袋的仓鼠，属幽默摆拍，无实际酒店服务相关信息。","mediaUnderstandingStatus":"understood"}`,
		},
	}
	history := adapter.HistoryBuildResult{RawItems: []models.Message{{ID: 10, ConversationID: 7, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "这是干嘛的"}}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "social_confirm", SubIntent: "media_context_follow_up", IntentConfidence: 0.88, ShouldReply: true, Reason: "图片前一条文本在追问图片"}})
	if !plan.Intent.ShouldReply || plan.Intent.PrimaryIntent != "social_confirm" {
		t.Fatalf("expected adjacent text+media to reply as context follow-up, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineTextQuestionAfterPlainMediaUsesMediaContext(t *testing.T) {
	req := RunInput{
		Conversation: models.Conversation{ID: 7},
		UserMessage: models.Message{
			ID:             12,
			ConversationID: 7,
			SenderType:     enums.IMSenderTypeCustomer,
			MessageType:    enums.IMMessageTypeText,
			Content:        "这是干嘛的",
		},
	}
	history := adapter.HistoryBuildResult{RawItems: []models.Message{{
		ID:             11,
		ConversationID: 7,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeImage,
		Content:        "funny.jpg",
		Payload:        `{"mediaText":"图片为一只戴着黑色猫耳头套、手持玩具手枪并抱着印有美元符号麻袋的仓鼠，属幽默摆拍，无实际酒店服务相关信息。","mediaUnderstandingStatus":"understood"}`,
	}}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "store_knowledge", IntentConfidence: 0.82, ShouldReply: true, NeedsKnowledge: true, Reason: "模型误判为酒店信息"}})
	if plan.Intent.PrimaryIntent != "social_confirm" || plan.Intent.SubIntent != "media_context_follow_up" {
		t.Fatalf("expected text after parsed media to use context follow-up, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsKnowledge {
		t.Fatal("context follow-up should not be forced through hotel knowledge")
	}
	if !strings.Contains(plan.Prompt, "图片/文件上下文追问") {
		t.Fatalf("expected image/file context prompt, got %q", plan.Prompt)
	}
}

func TestRuntimePipelineMediaFollowUpBeatsOldHumanRiskIntent(t *testing.T) {
	req := RunInput{
		Conversation: models.Conversation{ID: 7},
		UserMessage: models.Message{
			ID:             13,
			ConversationID: 7,
			SenderType:     enums.IMSenderTypeCustomer,
			MessageType:    enums.IMMessageTypeText,
			Content:        "你看我吃得怎么样",
		},
	}
	history := adapter.HistoryBuildResult{
		RawItems: []models.Message{{
			ID:             12,
			ConversationID: 7,
			SenderType:     enums.IMSenderTypeCustomer,
			MessageType:    enums.IMMessageTypeImage,
			Content:        "food.jpg",
			Payload:        `{"mediaText":"图片中有米饭、青菜、肉类和饮料，看起来餐食较丰富。","mediaUnderstandingStatus":"understood"}`,
		}},
		MemorySource: "上一轮客户说摔倒了，已进入人工风险判断。",
	}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "human_complaint_risk", SubIntent: "complaint", IntentConfidence: 0.86, ShouldReply: true, NeedsHumanRoute: true, Reason: "模型被旧风险上下文带偏"}})
	if plan.Intent.PrimaryIntent != "social_confirm" || plan.Intent.SubIntent != "media_context_follow_up" {
		t.Fatalf("expected media follow-up to beat stale human risk context, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsHumanRoute || plan.ToolKnowledge.ToolTriggered {
		t.Fatalf("media follow-up must not trigger handoff, intent=%#v tool=%#v", plan.Intent, plan.ToolKnowledge)
	}
}

func TestRuntimePipelineTextAfterMediaDoesNotHijackHotelInfo(t *testing.T) {
	req := RunInput{
		Conversation: models.Conversation{ID: 7},
		UserMessage: models.Message{
			ID:             12,
			ConversationID: 7,
			SenderType:     enums.IMSenderTypeCustomer,
			MessageType:    enums.IMMessageTypeText,
			Content:        "WiFi密码是多少",
		},
	}
	history := adapter.HistoryBuildResult{RawItems: []models.Message{{
		ID:             11,
		ConversationID: 7,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeImage,
		Content:        "room.jpg",
		Payload:        `{"mediaText":"图片为酒店房间一角。","mediaUnderstandingStatus":"understood"}`,
	}}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "network_wifi", IntentConfidence: 0.9, ShouldReply: true, NeedsKnowledge: true, Reason: "用户询问 WiFi 信息"}})
	if plan.Intent.PrimaryIntent != "hotel_info" {
		t.Fatalf("expected independent hotel question to remain hotel_info, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge {
		t.Fatal("hotel_info should still use knowledge")
	}
}

func TestRuntimePipelineCurrentHotelInfoSubIntentBeatsModelStaleSubIntent(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 100, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "停车免费吗，车停哪里"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "breakfast", IntentConfidence: 0.9, ShouldReply: true, NeedsKnowledge: true, Reason: "模型沿用了上一轮早餐子意图"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.SubIntent != "parking" {
		t.Fatalf("expected current parking subIntent override, got %#v", plan.Intent)
	}
	if !strings.Contains(plan.Intent.Reason, "current-turn subIntent override") {
		t.Fatalf("expected current-turn override reason, got %q", plan.Intent.Reason)
	}
}

func TestRuntimePipelineConfiguredHotelInfoSubIntentIgnoresConfigHints(t *testing.T) {
	primary, subIntent, _, _ := canonicalRuntimeIntent("hotel_info", "", "停车免费吗，车停哪里", "WiFi,网络,网连不上")
	if primary != "hotel_info" || subIntent != "parking" {
		t.Fatalf("expected current text to decide hotel_info subIntent, got primary=%q subIntent=%q", primary, subIntent)
	}
}

func TestRuntimePipelineBurstMediaFollowUpBeatsOldHumanRiskIntent(t *testing.T) {
	req := RunInput{
		Conversation: models.Conversation{ID: 7},
		UserMessage: models.Message{
			ID:             14,
			ConversationID: 7,
			SenderType:     enums.IMSenderTypeCustomer,
			MessageType:    enums.IMMessageTypeText,
			Content:        "客人刚才连续发了几条消息：\n[10:00] 我摔倒了，厕所太滑了\n[10:01] food.jpg\n[10:02] 你看我吃得怎么样",
		},
	}
	history := adapter.HistoryBuildResult{
		RawItems: []models.Message{{
			ID:             13,
			ConversationID: 7,
			SenderType:     enums.IMSenderTypeCustomer,
			MessageType:    enums.IMMessageTypeImage,
			Content:        "food.jpg",
			Payload:        `{"mediaText":"图片中有米饭、青菜、肉类和饮料，看起来餐食较丰富。","mediaUnderstandingStatus":"understood"}`,
		}},
	}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "human_complaint_risk", SubIntent: "emergency_safety", IntentConfidence: 0.9, ShouldReply: true, NeedsHumanRoute: true, Reason: "模型被合并消息里的旧风险带偏"}})
	if plan.Intent.PrimaryIntent != "social_confirm" || plan.Intent.SubIntent != "media_context_follow_up" {
		t.Fatalf("expected latest burst media follow-up to beat stale risk intent, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsHumanRoute || plan.ToolKnowledge.ToolTriggered {
		t.Fatalf("media follow-up must not trigger handoff, intent=%#v tool=%#v", plan.Intent, plan.ToolKnowledge)
	}
}

func TestRuntimePipelineHotelVariableCurrentTextOverridesModelResourceSubIntent(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "发一下酒店定位"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_variable", SubIntent: "phone", IntentConfidence: 0.91, ShouldReply: true, NeedsResource: true, ResourceAction: "provide_phone", Reason: "模型沿用了电话变量"}})
	if plan.Intent.PrimaryIntent != "hotel_variable" || plan.Intent.SubIntent != "location" {
		t.Fatalf("expected current location resource subIntent, got %#v", plan.Intent)
	}
	if plan.Intent.ResourceType != "location" || plan.Intent.ResourceAction != "provide_location" {
		t.Fatalf("expected current location resource action, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineHotelVariableMixedHotelInfoRequiresKnowledge(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "定位和入住小程序都发我，顺便问下停车"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_variable", SubIntent: "location", IntentConfidence: 0.9, ShouldReply: true, NeedsResource: true, ResourceAction: "provide_location", Reason: "用户索要门店变量"}})
	if plan.Intent.PrimaryIntent != "hotel_variable" || !plan.Intent.NeedsResource {
		t.Fatalf("expected hotel_variable resource intent, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge || !plan.ToolKnowledge.KnowledgeTriggered {
		t.Fatalf("expected mixed parking question to trigger knowledge too, intent=%#v tool=%#v", plan.Intent, plan.ToolKnowledge)
	}
	if !strings.Contains(plan.Intent.Reason, "mixed hotel_info") {
		t.Fatalf("expected mixed knowledge reason, got %q", plan.Intent.Reason)
	}
}

func TestRuntimePipelineExplicitVariableBeatsModelHotelInfoInMixedQuestion(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "定位和入住小程序都发我，顺便问下停车"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "parking", IntentConfidence: 0.9, ShouldReply: true, NeedsKnowledge: true, Reason: "模型只看到了停车问题"}})
	if plan.Intent.PrimaryIntent != "hotel_variable" || !plan.Intent.NeedsResource {
		t.Fatalf("expected explicit variable request to beat model hotel_info, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge || !plan.ToolKnowledge.KnowledgeTriggered {
		t.Fatalf("expected mixed hotel_info knowledge to remain, intent=%#v tool=%#v", plan.Intent, plan.ToolKnowledge)
	}
}

func TestRuntimePipelineActionableMediaShouldReply(t *testing.T) {
	req := RunInput{
		Conversation: models.Conversation{ID: 7},
		UserMessage: models.Message{
			ID:             10,
			ConversationID: 7,
			MessageType:    enums.IMMessageTypeImage,
			Content:        "error.png",
			Payload:        `{"mediaText":"截图里显示小程序打不开，并有文字提示：怎么处理？","mediaUnderstandingStatus":"understood"}`,
		},
	}
	plan := buildRuntimePipelinePlan(req, adapter.HistoryBuildResult{})
	if !plan.Intent.ShouldReply {
		t.Fatal("expected actionable media to reply")
	}
	if plan.Intent.PrimaryIntent != "unknown_clarify" || plan.Intent.SubIntent != "actionable_media_context" {
		t.Fatalf("expected actionable media context to fall back to clarify, got %#v", plan.Intent)
	}
}

func TestHotelVariableResourceContextForbidsCoworkerFallback(t *testing.T) {
	location := buildLocationResourceContext(&models.WxWorkProtocolInstance{EmployeeName: "测试门店", StoreLatitude: "31.824097", StoreLongitude: "117.263908"})
	if !strings.Contains(location, "31.824097") || !strings.Contains(location, "117.263908") || !strings.Contains(location, "uri.amap.com") || !strings.Contains(location, "不能说发不了链接") {
		t.Fatalf("expected direct location variable instruction, got %q", location)
	}
	miniProgram := buildMiniProgramResourceContext(&models.WxWorkProtocolInstance{DefaultMiniProgramPayload: `{"title":"入住小程序"}`})
	if !strings.Contains(miniProgram, "已绑定入住小程序变量") || !strings.Contains(miniProgram, "不能说让同事") {
		t.Fatalf("expected direct mini program variable instruction, got %q", miniProgram)
	}
	mixed := buildHotelVariableInstructionFromInstance(
		&models.WxWorkProtocolInstance{EmployeeName: "测试门店", StoreLatitude: "31.824097", StoreLongitude: "117.263908", DefaultMiniProgramPayload: `{"title":"入住小程序"}`},
		"定位和入住小程序都发我",
		callbacks.IntentTraceData{PrimaryIntent: "hotel_variable", ResourceAction: "provide_location", ResourceType: "location"},
	)
	if !strings.Contains(mixed, "酒店变量-定位/地址") || !strings.Contains(mixed, "酒店变量-入住小程序") || !strings.Contains(mixed, "不能说小程序未配置") {
		t.Fatalf("expected mixed variable instruction for location and mini program, got %q", mixed)
	}
	phone := buildPhoneResourceContext(&models.WxWorkProtocolInstance{})
	if !strings.Contains(phone, "暂未配置联系电话") || !strings.Contains(phone, "不能说让同事") {
		t.Fatalf("expected missing phone variable instruction, got %q", phone)
	}
}

func TestRuntimePipelineWifiIntentUsesKnowledgePrompt(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 200, MatchMode: "keyword", Keywords: "WiFi,网连不上", NeedsKnowledge: true, PromptPack: "这是酒店信息大分类；需要知识库回答网络问题；禁止承诺排查。", Status: enums.StatusOk})
	req := RunInput{
		Conversation: models.Conversation{ID: 7},
		UserMessage:  models.Message{ID: 10, ConversationID: 7, MessageType: enums.IMMessageTypeText, Content: "房间网连不上，WiFi 密码是多少"},
		AIAgent:      models.AIAgent{KnowledgeIDs: "1,2"},
	}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "network_wifi", IntentConfidence: 0.91, ShouldReply: true, NeedsKnowledge: true, Reason: "信息类酒店问题"}})
	if plan.Intent.PrimaryIntent != "hotel_info" {
		t.Fatalf("expected hotel_info, got %q", plan.Intent.PrimaryIntent)
	}
	if !plan.Intent.NeedsKnowledge {
		t.Fatal("expected WiFi intent to need knowledge")
	}
	if plan.Prompt == "" || !strings.Contains(plan.Prompt, "网络问题") || !strings.Contains(plan.Prompt, "禁止承诺排查") {
		t.Fatalf("expected WiFi prompt pack, got %q", plan.Prompt)
	}
}

func TestRuntimePipelineStoreResourceIntents(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 200, MatchMode: "keyword", Keywords: "入住,小程序,酒店在哪里,定位,电话", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	cases := []struct {
		text           string
		resourceAction string
	}{
		{text: "我要办理入住，发下小程序", resourceAction: "send_miniprogram"},
		{text: "酒店在哪里，定位发我", resourceAction: "provide_location"},
		{text: "你们电话多少", resourceAction: "provide_phone"},
	}
	for _, tc := range cases {
		req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: tc.text}}
		plan := buildRuntimePipelinePlan(req, adapter.HistoryBuildResult{})
		if plan.Intent.PrimaryIntent != "hotel_variable" {
			t.Fatalf("%q expected hotel_variable, got %s", tc.text, plan.Intent.PrimaryIntent)
		}
		if plan.Intent.ResourceAction != tc.resourceAction {
			t.Fatalf("%q expected action %s, got %s", tc.text, tc.resourceAction, plan.Intent.ResourceAction)
		}
		if !plan.Intent.NeedsResource {
			t.Fatalf("%q expected resource", tc.text)
		}
	}
}

func TestRuntimePipelineUsesConfiguredIntent(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{
		Code:              "hotel_info",
		Name:              "网络自助引导",
		Priority:          200,
		MatchMode:         "keyword",
		Keywords:          "网连不上,WiFi断了",
		NeedsKnowledge:    true,
		PromptPack:        "先查门店网络知识；禁止承诺帮客人排查网络。",
		ValidationRules:   "不得出现我帮你排查。",
		Status:            enums.StatusOk,
		ReplyPlanTemplate: "给出自助连接步骤，不做假动作。",
	})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "房间网连不上"}}
	plan := buildRuntimePipelinePlan(req, adapter.HistoryBuildResult{})
	if plan.Intent.PrimaryIntent != "hotel_info" {
		t.Fatalf("expected hotel_info, got %q", plan.Intent.PrimaryIntent)
	}
	if plan.Intent.MatchedConfigID == 0 || plan.Intent.MatchedConfig != "网络自助引导" {
		t.Fatalf("expected matched config trace, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge {
		t.Fatal("expected configured intent to need knowledge")
	}
	if !strings.Contains(plan.Prompt, "禁止承诺帮客人排查网络") || !strings.Contains(plan.Prompt, "不得出现我帮你排查") {
		t.Fatalf("expected configured prompt pack, got %q", plan.Prompt)
	}
}

func TestRuntimePipelineCurrentFacilityQuestionBeatsOldRiskContext(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "human_complaint_risk", Name: "人工风险", Priority: 300, MatchMode: "keyword", Keywords: "摔倒,受伤,人工,现场看", NeedsHumanRoute: true, Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 100, MatchMode: "keyword", Keywords: "", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "小爱同学黑屏了"}}
	history := adapter.HistoryBuildResult{MemorySource: "上次：客人在109摔倒，需要现场看。"}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "store_knowledge", IntentConfidence: 0.88, ShouldReply: true, NeedsKnowledge: true, Reason: "当前消息是设施设备信息类问题"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || !plan.Intent.NeedsKnowledge || plan.Intent.NeedsHumanRoute {
		t.Fatalf("expected current facility issue to use hotel_info knowledge, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineEmergencySafetyIntent(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 100, MatchMode: "keyword", Keywords: "厕所,地滑", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "我摔倒了，厕所太滑了，我在109"}}
	plan := buildRuntimePipelinePlan(req, adapter.HistoryBuildResult{})
	if plan.Intent.PrimaryIntent != "human_complaint_risk" || plan.Intent.SubIntent != "emergency_safety" || !plan.Intent.NeedsHumanRoute {
		t.Fatalf("expected emergency safety handoff intent, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineRoomNumberMemoryDoesNotDriveIntent(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "human_complaint_risk", Name: "人工风险", Priority: 300, MatchMode: "keyword", Keywords: "109,摔倒,人工", NeedsHumanRoute: true, Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 100, MatchMode: "keyword", Keywords: "", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "有浴帽吗"}}
	history := adapter.HistoryBuildResult{MemorySource: "上次住109，曾经摔倒。"}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "supplies_self_help", IntentConfidence: 0.9, ShouldReply: true, NeedsKnowledge: true, Reason: "用品信息类问题"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.SubIntent != "supplies_self_help" {
		t.Fatalf("expected current hotel info intent without old room contamination, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineModelHotelInfoTriggersKnowledgeWithoutKeywordOverride(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 100, MatchMode: "hybrid", NeedsKnowledge: true, PromptPack: "按当前门店知识库回答。", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "这边房间里的设备怎么弄"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", IntentConfidence: 0.86, ShouldReply: true, Reason: "用户询问酒店设备使用说明"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || !plan.Intent.NeedsKnowledge || plan.Intent.NeedsHumanRoute {
		t.Fatalf("expected model hotel_info to trigger knowledge, got %#v", plan.Intent)
	}
	if plan.Intent.MatchMode != "model" {
		t.Fatalf("expected model match mode, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineModelHotelVariableUsesResourceNotKnowledge(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "发一下酒店电话"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_variable", IntentConfidence: 0.92, ShouldReply: true, NeedsResource: true, ResourceAction: "provide_phone", Reason: "用户索要门店电话变量"}})
	if plan.Intent.PrimaryIntent != "hotel_variable" || !plan.Intent.NeedsResource || plan.Intent.NeedsKnowledge {
		t.Fatalf("expected hotel_variable resource only, got %#v", plan.Intent)
	}
	if plan.Intent.ResourceAction != "provide_phone" || plan.Intent.ResourceType != "phone" {
		t.Fatalf("expected phone resource action, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineModelServiceRequestPrechecksKnowledge(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "service_request", Name: "服务请求", Priority: 100, MatchMode: "hybrid", PromptPack: "服务请求先看当前门店知识库。", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "门锁不上"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "service_request", IntentConfidence: 0.86, ShouldReply: true, Reason: "设备故障服务请求"}})
	if plan.Intent.PrimaryIntent != "service_request" || !plan.Intent.NeedsKnowledge || plan.Intent.NeedsHumanRoute {
		t.Fatalf("expected service_request to precheck knowledge, got %#v", plan.Intent)
	}
	if !plan.ToolKnowledge.KnowledgeTriggered {
		t.Fatalf("expected tool knowledge trace to trigger knowledge, got %#v", plan.ToolKnowledge)
	}
}

func TestRuntimePipelineServiceRequestCannotRequestHandoffConfirmation(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "service_request", Name: "服务请求", Priority: 100, MatchMode: "hybrid", NeedsHumanRoute: true, HumanRoutePolicy: "managed_mode", PromptPack: "服务请求先看当前门店知识库。", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "门锁不上，帮我看看怎么处理"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "service_request", IntentConfidence: 0.86, ShouldReply: true, NeedsHumanRoute: true, HumanRoutePolicy: "managed_mode", Reason: "模型错误携带人工路由标记"}})
	if plan.Intent.PrimaryIntent != "service_request" {
		t.Fatalf("expected service_request, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsHumanRoute || plan.Intent.HumanRoutePolicy != "" {
		t.Fatalf("service_request must not request handoff confirmation, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge || !plan.ToolKnowledge.KnowledgeTriggered || plan.ToolKnowledge.ToolTriggered {
		t.Fatalf("expected service_request to stay on knowledge/service path, got intent=%#v tool=%#v", plan.Intent, plan.ToolKnowledge)
	}
	if !strings.Contains(plan.Intent.Reason, "handoff confirmation only belongs") {
		t.Fatalf("expected downgrade reason in trace, got %q", plan.Intent.Reason)
	}
}

func TestRuntimePipelineModelExplicitHandoffUsesHumanComplaintRiskRoute(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "human_complaint_risk", Name: "人工/投诉/风险", Priority: 100, MatchMode: "hybrid", NeedsHumanRoute: true, HumanRoutePolicy: "managed_mode", PromptPack: "按当前门店托管模式和排班路由。", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "别机器人了，帮我转人工"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "human_complaint_risk", IntentConfidence: 0.91, ShouldReply: true, Reason: "用户明确要求人工"}})
	if plan.Intent.PrimaryIntent != "human_complaint_risk" || plan.Intent.MatchedIntentCode != "human_complaint_risk" {
		t.Fatalf("expected explicit handoff to stay in human_complaint_risk, got %#v", plan.Intent)
	}
	if plan.Intent.SubIntent != "explicit_handoff" || !plan.Intent.NeedsHumanRoute || plan.Intent.NeedsKnowledge || plan.Intent.HumanRoutePolicy != "managed_mode" {
		t.Fatalf("expected managed-mode human route intent, got %#v", plan.Intent)
	}
	if !plan.ToolKnowledge.ToolTriggered || len(plan.ToolKnowledge.ExpectedResources) == 0 || plan.ToolKnowledge.ExpectedResources[0] != "handoff confirmation policy" {
		t.Fatalf("expected human route tool trace, got %#v", plan.ToolKnowledge)
	}
}

func TestRuntimePipelineLegacyHandoffIntentCanonicalizesToSevenCategory(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "human_complaint_risk", Name: "人工/投诉/风险", Priority: 100, MatchMode: "hybrid", NeedsHumanRoute: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "我要找真人客服"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "handoff", IntentConfidence: 0.88, ShouldReply: true, Reason: "legacy label from model"}})
	if plan.Intent.PrimaryIntent != "human_complaint_risk" || plan.Intent.MatchedIntentCode == "handoff" {
		t.Fatalf("expected legacy handoff to canonicalize to human_complaint_risk, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsHumanRoute {
		t.Fatalf("expected canonicalized handoff to need human route, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineWeatherQueryStaysSocialConfirmAndUsesTool(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 100, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "social_confirm", Name: "轻互动/确认", Priority: 90, MatchMode: "hybrid", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "帮我查一下合肥今天的天气"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", IntentConfidence: 0.8, ShouldReply: true, NeedsKnowledge: true, Reason: "模型误判天气为酒店信息"}})
	if plan.Intent.PrimaryIntent != "social_confirm" || plan.Intent.SubIntent != "weather_query" {
		t.Fatalf("expected weather query under social_confirm, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsTool || plan.Intent.NeedsKnowledge || plan.Intent.ResourceAction != "get_weather" {
		t.Fatalf("expected weather tool only, got %#v", plan.Intent)
	}
	if !plan.ToolKnowledge.ToolTriggered || plan.ToolKnowledge.KnowledgeTriggered {
		t.Fatalf("expected tool trace without knowledge, got %#v", plan.ToolKnowledge)
	}
}

func TestResolveRuntimeIntentDetectAIConfigPrefersAccountOverride(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	fallback := models.AIConfig{ID: 1, Name: "reply", Provider: enums.AIProviderOpenAI, ModelType: enums.AIModelTypeLLM, ModelName: "reply-model"}
	globalIntent := models.AIConfig{ID: 3, Name: "global intent", Provider: enums.AIProviderOpenAI, BaseURL: "https://api.example.com/v1", APIKey: "sk", ModelType: enums.AIModelTypeLLM, ModelName: "global-intent-model", Status: enums.StatusOk, IntentDetectEnabled: true, SortNo: 2}
	if err := sqls.DB().Create(&globalIntent).Error; err != nil {
		t.Fatalf("create global ai config: %v", err)
	}
	if err := sqls.DB().Create(&models.Store{ID: 5, CompanyID: 2, Name: "store", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := sqls.DB().Create(&models.ConversationRouteState{ConversationID: 7, StoreID: 5, WxWorkInstanceID: 11}).Error; err != nil {
		t.Fatalf("create route state: %v", err)
	}
	if err := sqls.DB().Create(&models.StoreAIModelSetting{
		CompanyID:        2,
		StoreID:          5,
		WxWorkInstanceID: 11,
		UsageCode:        services.StoreAIModelUsageIntentDetectLLM,
		Provider:         enums.AIProviderOpenAI,
		BaseURL:          "https://account.example.com/v1",
		APIKey:           "sk-account",
		ModelType:        enums.AIModelTypeLLM,
		ModelName:        "account-intent-model",
		Status:           enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create account model setting: %v", err)
	}
	got := resolveRuntimeIntentDetectAIConfig(RunInput{Conversation: models.Conversation{ID: 7}, AIConfig: fallback})
	if got.ModelName != "account-intent-model" {
		t.Fatalf("expected account override intent model, got %#v", got)
	}
}

func TestResolveRuntimeIntentDetectAIConfigFallsBack(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	fallback := models.AIConfig{ID: 1, Name: "reply", Provider: enums.AIProviderOpenAI, ModelType: enums.AIModelTypeLLM, ModelName: "reply-model"}
	got := resolveRuntimeIntentDetectAIConfig(RunInput{Conversation: models.Conversation{ID: 7}, AIConfig: fallback})
	if got.ID != fallback.ID || got.ModelName != "reply-model" {
		t.Fatalf("expected fallback model, got %#v", got)
	}
}

func seedRuntimeIntentConfig(t *testing.T, item models.ReplyIntentConfig) {
	t.Helper()
	if item.ScopeType == "" {
		item.ScopeType = "global"
	}
	if err := sqls.DB().Create(&item).Error; err != nil {
		t.Fatalf("create intent config: %v", err)
	}
}

func setupRuntimeIntentConfigTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite error = %v", err)
	}
	if err := db.AutoMigrate(&models.ReplyIntentConfig{}, &models.AIConfig{}, &models.Store{}, &models.StoreAIModelSetting{}, &models.ConversationRouteState{}); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
