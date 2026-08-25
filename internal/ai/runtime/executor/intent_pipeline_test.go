package executor

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/securex"
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

type recordingRuntimeIntentModelDetector struct {
	called bool
	intent callbacks.IntentTraceData
	err    error
}

func (s *recordingRuntimeIntentModelDetector) DetectRuntimeIntent(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, error) {
	s.called = true
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
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{
		PrimaryIntent:    "interaction",
		SubIntent:        "media_context_follow_up",
		IntentConfidence: 0.87,
		ShouldReply:      true,
		Reason:           "模型识别为媒体上下文追问",
	}})
	if plan.Normalize.CurrentUserText == "" {
		t.Fatal("expected normalize current user text")
	}
	if plan.Intent.PrimaryIntent == "" {
		t.Fatal("expected intent")
	}
	if plan.Intent.PrimaryIntent != "interaction" || plan.Intent.SubIntent != "media_context_follow_up" {
		t.Fatalf("expected context follow-up under interaction, got %#v", plan.Intent)
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

func TestRuntimePipelineMediaFollowUpKeepsModelIntentAfterIntentDetect(t *testing.T) {
	detector := &recordingRuntimeIntentModelDetector{
		intent: callbacks.IntentTraceData{
			PrimaryIntent:    "hotel_info",
			SubIntent:        "dining_feedback",
			IntentConfidence: 0.82,
			ShouldReply:      true,
			NeedsKnowledge:   true,
			Reason:           "model incorrectly treated image follow-up as hotel dining info",
		},
	}
	req := RunInput{
		Conversation: models.Conversation{ID: 7},
		UserMessage:  models.Message{ID: 12, ConversationID: 7, MessageType: enums.IMMessageTypeText, Content: "我吃得怎么样"},
	}
	history := adapter.HistoryBuildResult{RawItems: []models.Message{{
		ID:             11,
		ConversationID: 7,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeImage,
		Content:        "meal.jpg",
		Payload:        `{"mediaText":"图片里是一份盖浇饭、饮料和水果，餐食比较完整。","mediaUnderstandingStatus":"understood"}`,
	}}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, detector)
	if !detector.called {
		t.Fatal("expected IntentDetect model stage to run")
	}
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.SubIntent != "dining_feedback" || !plan.Intent.NeedsKnowledge {
		t.Fatalf("expected media context to remain context while model intent is preserved, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineIntentDetectFailureStillReplies(t *testing.T) {
	req := RunInput{
		Conversation: models.Conversation{ID: 7},
		UserMessage:  models.Message{ID: 13, ConversationID: 7, MessageType: enums.IMMessageTypeText, Content: "WiFi 密码多少"},
	}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{err: context.DeadlineExceeded})
	if !plan.Intent.ShouldReply {
		t.Fatalf("IntentDetect failure must continue to a safe reply instead of going silent, got %#v", plan.Intent)
	}
	if plan.Intent.PrimaryIntent != "" || plan.Intent.DetectedIntent != "intent_detect_unavailable" {
		t.Fatalf("expected explicit IntentDetect failure trace, got %#v", plan.Intent)
	}
}

func TestRuntimeIntentDetectPromptRequiresSpecificHotelInfoSubIntent(t *testing.T) {
	prompt := runtimeIntentDetectSystemPrompt()
	for _, expected := range []string{
		"subIntent 字段纪律",
		"checkin_process",
		"“我要办理入住/怎么入住/入住怎么弄”必须按顺序输出 hotel_info/checkin_process",
		"只有用户只说“办理入住的小程序发我/入住小程序发我”且没有问步骤时",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("intent detect prompt missing %q: %s", expected, prompt)
		}
	}
}

func TestRuntimeIntentDetectSystemPromptUsesFiveCleanTopLevelIntents(t *testing.T) {
	prompt := runtimeIntentDetectSystemPrompt()
	for _, intentCode := range []string{
		"hotel_info",
		"service_request",
		"human_complaint_risk",
		"interaction",
		"hotel_variable",
	} {
		if !strings.Contains(prompt, intentCode) {
			t.Fatalf("intent prompt missing %s: %s", intentCode, prompt)
		}
	}
	for _, legacy := range []string{
		"hotel_faq",
		"media_question",
		"media_understanding",
		"no_reply_media_only",
		"social_confirm",
		"unknown_clarify",
		"FAQ",
	} {
		if strings.Contains(prompt, legacy) {
			t.Fatalf("intent prompt must not expose legacy intent %s: %s", legacy, prompt)
		}
	}
}

func TestRuntimeIntentDetectSystemPromptDefinesHotelInfoServiceRequestBoundary(t *testing.T) {
	prompt := runtimeIntentDetectSystemPrompt()
	for _, expected := range []string{
		"hotel_info 与 service_request 的硬边界",
		"空调不制冷怎么办",
		"我要办理入住",
		"只有客户明确要人或动作",
		"才归 service_request",
		"hotel_info 与 hotel_variable 的硬边界",
		"WiFi密码多少",
		"都不能输出 hotel_variable",
		"resourceActions 字段纪律",
		"禁止把电话、定位、小程序作为默认兜底一起输出",
		"客户明确要其他地点的定位或导航时，不是 hotel_variable",
		"都不能输出 provide_location 或发送门店定位",
		"定位、地址、导航先判断对象",
		"最近一轮仍在讨论的外部地点",
		"都以该外部地点为准，优先于默认的酒店身份",
		"只追问要哪个地点，不取变量",
		"纠错与业务问题边界",
		"纠错语气本身不是独立业务任务",
		"我问的是停车，不是早餐，停车入口在哪",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("intent prompt missing boundary phrase %q: %s", expected, prompt)
		}
	}
}

func TestRuntimeIntentDetectSystemPromptRoutesPublicCompanyIdentityToKnowledge(t *testing.T) {
	prompt := runtimeIntentDetectSystemPrompt()
	for _, expected := range []string{
		"hotel_info/company_profile",
		"老板、创始人、董事长",
		"公开身份",
		"公开职务",
		"你是谁",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("expected company-profile boundary %q in prompt", expected)
		}
	}
	if strings.Contains(prompt, "汤东强") {
		t.Fatalf("company-profile routing must remain generic, got hard-coded person name")
	}
}

func TestRuntimeIntentDetectPromptCarriesImmediateBusinessClarification(t *testing.T) {
	prompt := runtimeIntentDetectSystemPrompt()
	for _, expected := range []string{
		"紧邻的上一条 AI 客服消息正在就一个业务问题追问偏好、条件、范围或选项",
		"附近餐饮推荐，偏好麻辣口味",
		"不能从更早历史里挑一个旧主题强行续接",
		"answer_rejected 只有本轮用户提示明确启用",
		"answer_rejected 不是关键词命中",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("intent prompt missing follow-up rule %q: %s", expected, prompt)
		}
	}
}

func TestBuildRuntimeIntentDetectUserPromptMarksShortBusinessFollowUp(t *testing.T) {
	history := adapter.HistoryBuildResult{RawItems: []models.Message{
		{ID: 1, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "附近有什么好吃的"},
		{ID: 2, SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "附近餐饮想吃什么口味？"},
	}}
	req := RunInput{UserMessage: models.Message{ID: 3, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "麻辣口味的"}}
	prompt := buildRuntimeIntentDetectUserPrompt(req, history, nil)
	for _, expected := range []string{"麻辣口味的", "附近餐饮想吃什么口味", "hotel_info/surrounding_facilities", "完整检索问题"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("user prompt missing follow-up context %q: %s", expected, prompt)
		}
	}
}

func TestBuildRuntimeIntentDetectUserPromptDisclosesAnswerRelationOnlyAfterAIReply(t *testing.T) {
	req := RunInput{UserMessage: models.Message{ID: 3, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "你刚才不是说要开车吗？"}}
	history := adapter.HistoryBuildResult{RawItems: []models.Message{
		{ID: 1, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "小丁小吃能走过去吗"},
		{ID: 2, SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "走路几分钟就能到。"},
	}}
	prompt := buildRuntimeIntentDetectUserPrompt(req, history, nil)
	for _, expected := range []string{
		"上一答复关系判断（仅本轮启用）",
		"此前客户原问题",
		"紧邻 AI 客服答复",
		"answer_rejected",
		"answer_contradicted",
		"答非所问",
		"引用真人客服说法或现场事实",
		"你刚才不是说要开车吗",
		"我问的是房间里有没有",
		"客服说可以微信转账",
		"不能按‘不是、为什么、真的吗’等单个词机械匹配",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("answer relation prompt missing %q: %s", expected, prompt)
		}
	}
}

func TestBuildRuntimeIntentDetectUserPromptDoesNotDiscloseAnswerRelationWithoutAdjacentAIReply(t *testing.T) {
	req := RunInput{UserMessage: models.Message{ID: 4, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "为什么"}}
	tests := []struct {
		name    string
		history adapter.HistoryBuildResult
	}{
		{name: "no history"},
		{name: "previous customer", history: adapter.HistoryBuildResult{RawItems: []models.Message{{ID: 3, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "早餐几点"}}}},
		{name: "previous human agent", history: adapter.HistoryBuildResult{RawItems: []models.Message{{ID: 3, SenderType: enums.IMSenderTypeAgent, MessageType: enums.IMMessageTypeText, Content: "早餐到十点"}}}},
		{name: "older ai but adjacent customer", history: adapter.HistoryBuildResult{RawItems: []models.Message{
			{ID: 2, SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "早餐到十点"},
			{ID: 3, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "好的"},
		}}},
		{name: "standalone one reply is the physical latest message", history: adapter.HistoryBuildResult{
			RawItems: []models.Message{{ID: 1, SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "早餐到十点"}},
			LatestRawItem: &models.Message{
				ID:          3,
				SenderType:  enums.IMSenderTypeAI,
				MessageType: enums.IMMessageTypeText,
				Content:     "欢迎入住",
				ClientMsgID: "ai_reply_faq_one_fixed",
			},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := buildRuntimeIntentDetectUserPrompt(req, tc.history, nil)
			if strings.Contains(prompt, "上一答复关系判断（仅本轮启用）") {
				t.Fatalf("answer relation prompt must stay hidden without an adjacent AI reply: %s", prompt)
			}
		})
	}
}

func TestNormalizeAnswerRejectedRequiresAdjacentAIReply(t *testing.T) {
	base := callbacks.IntentTraceData{
		PrimaryIntent:    "human_complaint_risk",
		SubIntent:        "answer_rejected",
		IntentConfidence: 0.9,
		ShouldReply:      true,
		NeedsHumanRoute:  true,
		Reason:           "客户指出上一答复矛盾",
	}
	withAI := normalizeModelIntentTrace(base, RunInput{}, adapter.HistoryBuildResult{RawItems: []models.Message{{
		ID:          1,
		SenderType:  enums.IMSenderTypeAI,
		MessageType: enums.IMMessageTypeText,
		Content:     "可以走路过去。",
	}}}, nil)
	if withAI.PrimaryIntent != "human_complaint_risk" || withAI.SubIntent != "answer_rejected" || !withAI.NeedsHumanRoute || withAI.HumanRoutePolicy != "managed_mode" {
		t.Fatalf("expected adjacent AI answer rejection to use existing human route, got %#v", withAI)
	}

	withoutAI := normalizeModelIntentTrace(base, RunInput{}, adapter.HistoryBuildResult{RawItems: []models.Message{{
		ID:          1,
		SenderType:  enums.IMSenderTypeCustomer,
		MessageType: enums.IMMessageTypeText,
		Content:     "真的吗",
	}}}, nil)
	if withoutAI.PrimaryIntent != "interaction" || withoutAI.SubIntent != "frustration" || withoutAI.NeedsHumanRoute || withoutAI.HumanRoutePolicy != "" {
		t.Fatalf("expected detached answer_rejected to be downgraded safely, got %#v", withoutAI)
	}
}

func TestIntentPromptPackDisclosesSpatialFactsOnlyForSpatialTasks(t *testing.T) {
	for _, intent := range []callbacks.IntentTraceData{
		{PrimaryIntent: "hotel_info", SubIntent: "surrounding_facilities"},
		{PrimaryIntent: "hotel_info", SubIntent: "location_info"},
		{PrimaryIntent: "hotel_variable", SubIntent: "mini_program", IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "hotel_variable", SubIntent: "mini_program"},
			{Intent: "hotel_info", SubIntent: "surrounding_facilities", Text: "附近有什么吃的"},
		}},
	} {
		joined := strings.Join(selectIntentPromptPack(intent).Instructions, "\n")
		for _, expected := range []string{"仅适用于本轮周边/位置任务", "地点是否存在", "具体地址", "交通方式", "预计时间", "完整路线", "不能跨维度推断"} {
			if !strings.Contains(joined, expected) {
				t.Fatalf("spatial task prompt missing %q: %s", expected, joined)
			}
		}
	}

	configured := promptForModelDetectedIntent(callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info",
		SubIntent:     "surrounding_facilities",
	}, []models.ReplyIntentConfig{{
		Code:       "hotel_info",
		PromptPack: "使用当前门店知识回答。",
	}})
	configuredText := strings.Join(configured.Instructions, "\n")
	if !strings.Contains(configuredText, "仅适用于本轮周边/位置任务") || !strings.Contains(configuredText, "使用当前门店知识回答") {
		t.Fatalf("configured prompt must retain its rules and append spatial facts: %s", configuredText)
	}

	for _, intent := range []callbacks.IntentTraceData{
		{PrimaryIntent: "hotel_info", SubIntent: "breakfast"},
		{PrimaryIntent: "hotel_info", SubIntent: "parking"},
		{PrimaryIntent: "interaction", SubIntent: "chat"},
		{PrimaryIntent: "hotel_variable", SubIntent: "location_info"},
	} {
		joined := strings.Join(selectIntentPromptPack(intent).Instructions, "\n")
		if strings.Contains(joined, "仅适用于本轮周边/位置任务") {
			t.Fatalf("spatial fact prompt leaked into unrelated intent %#v: %s", intent, joined)
		}
	}
}

func TestDeriveModelIntentFromTasksKeepsCheckinKnowledgePrimary(t *testing.T) {
	intent := deriveModelIntentFromTasks(callbacks.IntentTraceData{
		PrimaryIntent: "hotel_variable",
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "hotel_info", SubIntent: "checkin_process", Text: "我要办理入住", NeedsKnowledge: true},
			{Intent: "hotel_variable", SubIntent: "mini_program", Text: "发送入住小程序", NeedsResource: true, ResourceAction: "provide_mini_program"},
		},
	})
	if intent.PrimaryIntent != "hotel_info" {
		t.Fatalf("expected first business task to remain primary, got %#v", intent)
	}
	if !intent.NeedsKnowledge || !intent.NeedsResource || len(intent.ResourceActions) != 1 || intent.ResourceActions[0] != "provide_mini_program" {
		t.Fatalf("expected checkin knowledge and mini program action to be aggregated, got %#v", intent)
	}
	if len(intent.SecondaryIntents) != 1 || intent.SecondaryIntents[0] != "hotel_variable" {
		t.Fatalf("expected hotel variable to remain a secondary task, got %#v", intent.SecondaryIntents)
	}
}

func TestDeriveModelIntentFromTasksDoesNotLetCorrectionToneHideBusinessTask(t *testing.T) {
	intent := deriveModelIntentFromTasks(callbacks.IntentTraceData{
		PrimaryIntent: "interaction",
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "interaction", SubIntent: "correction", Text: "我问的不是早餐"},
			{Intent: "hotel_info", SubIntent: "parking", Text: "停车入口在哪", NeedsKnowledge: true},
		},
	})
	if intent.PrimaryIntent != "hotel_info" || !intent.NeedsKnowledge {
		t.Fatalf("expected the corrected hotel question to be primary and retrieve knowledge, got %#v", intent)
	}
	if len(intent.SecondaryIntents) != 1 || intent.SecondaryIntents[0] != "interaction" {
		t.Fatalf("expected correction tone to remain secondary context only, got %#v", intent.SecondaryIntents)
	}
}

func TestIntentPromptPackBlocksCoworkerFakeCommitment(t *testing.T) {
	prompt := selectIntentPromptPack(callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "store_knowledge"})
	joined := strings.Join(prompt.Instructions, "\n")
	for _, expected := range []string{
		"没有真实工具、资源提交或接待路由结果",
		"内部核实、通知转告、登记安排、现场查看、后续跟进",
		"只能如实引导客户联系",
		"不能改写成系统已经替客户执行了真实动作",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("intent prompt pack missing fake commitment guard %q: %s", expected, joined)
		}
	}
}

func TestParseRuntimeIntentDetectJSONToleratesLooseListFields(t *testing.T) {
	parsed, err := parseRuntimeIntentDetectJSON(`{
		"primaryIntent":"hotel_variable",
		"subIntent":"location",
		"confidence":0.91,
		"needsKnowledge":true,
		"needsTool":false,
		"needsResource":true,
		"needsHumanRoute":false,
		"resourceType":"location",
		"resourceAction":"provide_location",
		"resourceActions":"provide_location",
		"secondaryIntents":null,
		"mixedSubTasks":false,
		"intentTasks":false,
		"reason":"用户同时索要定位并询问停车"
	}`)
	if err != nil {
		t.Fatalf("expected loose list fields to parse, got %v", err)
	}
	if parsed.PrimaryIntent != "hotel_variable" || parsed.ResourceAction != "provide_location" {
		t.Fatalf("unexpected parsed intent: %#v", parsed)
	}
	if len(parsed.ResourceActions) != 1 || parsed.ResourceActions[0] != "provide_location" {
		t.Fatalf("expected single resource action to be coerced into list, got %#v", parsed.ResourceActions)
	}
	if len(parsed.MixedSubTasks) != 0 || len(parsed.IntentTasks) != 0 {
		t.Fatalf("expected false mixedSubTasks/intentTasks to become empty lists, got %#v %#v", parsed.MixedSubTasks, parsed.IntentTasks)
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
	if plan.Intent.PrimaryIntent != "" || plan.Intent.DetectedIntent != "media_gate" || plan.Intent.SubIntent != "media_only_no_question" {
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
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "interaction", SubIntent: "media_context_follow_up", IntentConfidence: 0.88, ShouldReply: true, Reason: "图片前一条文本在追问图片"}})
	if !plan.Intent.ShouldReply || plan.Intent.PrimaryIntent != "interaction" {
		t.Fatalf("expected adjacent text+media to reply as context follow-up, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineTextQuestionAfterPlainMediaUsesModelMediaIntent(t *testing.T) {
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
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "interaction", SubIntent: "media_context_follow_up", IntentConfidence: 0.82, ShouldReply: true, Reason: "模型识别为最近媒体上下文追问"}})
	if plan.Intent.PrimaryIntent != "interaction" || plan.Intent.SubIntent != "media_context_follow_up" {
		t.Fatalf("expected text after parsed media to use context follow-up, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsKnowledge {
		t.Fatal("context follow-up should not be forced through hotel knowledge")
	}
	if !strings.Contains(plan.Prompt, "图片/文件上下文追问") {
		t.Fatalf("expected image/file context prompt, got %q", plan.Prompt)
	}
}

func TestCurrentTurnDisplayTextStripsBurstInstruction(t *testing.T) {
	content := "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n[消息1] WiFi连不上\n[消息2] 发票怎么开"
	display := currentTurnDisplayText(content)
	if strings.Contains(display, "客人刚才连续发") || strings.Contains(display, "请按顺序合并理解") {
		t.Fatalf("expected burst instruction stripped, got %q", display)
	}
	if !strings.Contains(display, "WiFi连不上") || !strings.Contains(display, "发票怎么开") {
		t.Fatalf("expected customer burst messages retained, got %q", display)
	}
}

func TestRuntimePipelineDoesNotBypassIntentModelWithKeywordGuard(t *testing.T) {
	detector := &recordingRuntimeIntentModelDetector{
		intent: callbacks.IntentTraceData{
			PrimaryIntent:    "hotel_info",
			SubIntent:        "store_knowledge",
			IntentConfidence: 0.72,
			ShouldReply:      true,
			NeedsKnowledge:   true,
			Reason:           "model classified first",
		},
	}
	req := RunInput{
		Conversation: models.Conversation{ID: 7},
		UserMessage: models.Message{
			MessageType: enums.IMMessageTypeText,
			Content:     "定位发我一个",
		},
	}

	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, detector)
	if !detector.called {
		t.Fatal("expected IntentDetect model stage to run")
	}
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.ResourceAction != "" {
		t.Fatalf("expected model intent to remain unchanged by keyword guard, got %#v", plan.Intent)
	}
}

func TestIsMultiQuestionCurrentTurnDetectsContinuousHotelQuestions(t *testing.T) {
	if !isMultiQuestionCurrentTurn("早餐有吗\n停车免费吗\n剃须刀在哪") {
		t.Fatal("expected continuous hotel questions to be treated as multi-question current turn")
	}
}

func TestRuntimeIntentPromptRequiresEveryBurstQuestionToBecomeTask(t *testing.T) {
	req := RunInput{UserMessage: models.Message{
		MessageType: enums.IMMessageTypeText,
		Content:     "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 早餐有吗\n2. [消息] 停车免费吗\n3. [消息] 剃须刀在哪",
	}}
	prompt := buildRuntimeIntentDetectUserPrompt(req, adapter.HistoryBuildResult{}, nil)
	if !strings.Contains(prompt, "每个独立问题或动作都要在 intentTasks 中有对应任务") || !strings.Contains(prompt, "不能只分类最后一条") {
		t.Fatalf("expected burst task coverage contract in Intent prompt, got %q", prompt)
	}
}

func TestCurrentTurnBoundaryUsesActualBurstWhenIntentMissesTasks(t *testing.T) {
	req := RunInput{UserMessage: models.Message{
		MessageType: enums.IMMessageTypeText,
		Content:     "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 早餐有吗\n2. [消息] 停车免费吗\n3. [消息] 剃须刀在哪",
	}}
	intent := callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_info",
		NeedsKnowledge: true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:         "hotel_info",
			SubIntent:      "supplies_self_help",
			Text:           "剃须刀在哪",
			NeedsKnowledge: true,
		}},
	}
	instruction := buildCurrentTurnBoundaryInstruction(req, adapter.HistoryBuildResult{}, intent)
	if !strings.Contains(instruction, "当前轮包含连续多问") || !strings.Contains(instruction, "不要只回答主意图或最后一个问题") {
		t.Fatalf("expected actual burst to enforce multi-question generation coverage, got %q", instruction)
	}
}

func TestRuntimePipelineSeenQuestionAfterPlainMediaUsesMediaContext(t *testing.T) {
	req := RunInput{
		Conversation: models.Conversation{ID: 7},
		UserMessage: models.Message{
			ID:             12,
			ConversationID: 7,
			SenderType:     enums.IMSenderTypeCustomer,
			MessageType:    enums.IMMessageTypeText,
			Content:        "看到了吗",
		},
	}
	history := adapter.HistoryBuildResult{RawItems: []models.Message{{
		ID:             11,
		ConversationID: 7,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeImage,
		Content:        "selfie.jpg",
		Payload:        `{"mediaText":"图片为客人自拍，无清晰文字、报错或明确服务诉求。","mediaUnderstandingStatus":"understood"}`,
	}}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "interaction", SubIntent: "media_context_follow_up", IntentConfidence: 0.76, ShouldReply: true, Reason: "模型识别为媒体追问"}})
	if plan.Intent.PrimaryIntent != "interaction" || plan.Intent.SubIntent != "media_context_follow_up" {
		t.Fatalf("expected seen-question after parsed media to use social media follow-up, got %#v", plan.Intent)
	}
}

func TestRuntimeHistoryMessageContentIncludesSpeakerAndTime(t *testing.T) {
	at := time.Date(2026, 7, 8, 9, 10, 11, 0, time.Local)
	item := models.Message{
		SenderType:  enums.IMSenderTypeCustomer,
		MessageType: enums.IMMessageTypeText,
		Content:     "早餐几点",
		AuditFields: models.AuditFields{CreatedAt: at},
	}
	text := adapter.RuntimeHistoryMessageContent(&item)
	if !strings.Contains(text, "[历史消息][客户][2026-07-08 09:10:11]") || !strings.Contains(text, "早餐几点") {
		t.Fatalf("expected structured history content with speaker/time, got %q", text)
	}
}

func TestRuntimePipelineMediaFollowUpUsesModelIntent(t *testing.T) {
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
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "interaction", SubIntent: "media_context_follow_up", IntentConfidence: 0.86, ShouldReply: true, Reason: "模型识别为图片追问"}})
	if plan.Intent.PrimaryIntent != "interaction" || plan.Intent.SubIntent != "media_context_follow_up" {
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

func TestRuntimePipelineKeepsModelHotelInfoSubIntent(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 100, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "停车免费吗，车停哪里"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "breakfast", IntentConfidence: 0.9, ShouldReply: true, NeedsKnowledge: true, Reason: "模型沿用了上一轮早餐子意图"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.SubIntent != "breakfast" {
		t.Fatalf("expected model subIntent to remain unchanged by keyword override, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineBurstMediaFollowUpUsesModelIntent(t *testing.T) {
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
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "interaction", SubIntent: "media_context_follow_up", IntentConfidence: 0.9, ShouldReply: true, Reason: "模型识别为最新图片追问"}})
	if plan.Intent.PrimaryIntent != "interaction" || plan.Intent.SubIntent != "media_context_follow_up" {
		t.Fatalf("expected latest burst media follow-up to beat stale risk intent, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsHumanRoute || plan.ToolKnowledge.ToolTriggered {
		t.Fatalf("media follow-up must not trigger handoff, intent=%#v tool=%#v", plan.Intent, plan.ToolKnowledge)
	}
}

func TestRuntimePipelineHotelVariableKeepsModelResourceSubIntent(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "发一下酒店定位"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_variable", SubIntent: "phone", IntentConfidence: 0.91, ShouldReply: true, NeedsResource: true, ResourceAction: "provide_phone", Reason: "模型沿用了电话变量"}})
	if plan.Intent.PrimaryIntent != "hotel_variable" || plan.Intent.SubIntent != "phone" {
		t.Fatalf("expected model resource subIntent to remain unchanged by keyword override, got %#v", plan.Intent)
	}
	if plan.Intent.ResourceType != "phone" || plan.Intent.ResourceAction != "provide_phone" {
		t.Fatalf("expected model phone resource action, got %#v", plan.Intent)
	}
}

func TestRuntimePipelinePureHotelVariableClearsModelKnowledgeFlag(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "电话多少"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{
		PrimaryIntent:    "hotel_variable",
		SubIntent:        "phone",
		IntentConfidence: 0.91,
		ShouldReply:      true,
		NeedsKnowledge:   true,
		NeedsResource:    true,
		ResourceAction:   "provide_phone",
		Reason:           "模型误带 needsKnowledge，但没有酒店信息子任务",
	}})
	if plan.Intent.NeedsKnowledge {
		t.Fatalf("pure hotel_variable must not query knowledge, got %#v", plan.Intent)
	}
	if plan.Intent.ResourceType != "phone" || plan.Intent.ResourceAction != "provide_phone" || !plan.Intent.NeedsResource {
		t.Fatalf("expected phone variable action to remain, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineHotelVariableMixedHotelInfoRequiresKnowledge(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "定位和入住小程序都发我，顺便问下停车"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{
		PrimaryIntent:    "hotel_variable",
		SubIntent:        "location",
		IntentConfidence: 0.9,
		ShouldReply:      true,
		NeedsResource:    true,
		NeedsKnowledge:   true,
		ResourceAction:   "provide_location",
		ResourceActions:  []string{"provide_location", "provide_mini_program"},
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "hotel_variable", SubIntent: "location", Text: "定位", NeedsResource: true, ResourceAction: "provide_location"},
			{Intent: "hotel_variable", SubIntent: "mini_program", Text: "入住小程序", NeedsResource: true, ResourceAction: "provide_mini_program"},
			{Intent: "hotel_info", SubIntent: "parking", Text: "停车", NeedsKnowledge: true},
		},
		Reason: "模型识别为变量请求，同时包含停车知识问题",
	}})
	if plan.Intent.PrimaryIntent != "hotel_variable" || !plan.Intent.NeedsResource {
		t.Fatalf("expected hotel_variable resource intent, got %#v", plan.Intent)
	}
	if len(plan.Intent.ResourceActions) != 2 || plan.Intent.ResourceActions[0] != "provide_location" || plan.Intent.ResourceActions[1] != "provide_mini_program" {
		t.Fatalf("expected two ordered resource actions, got %#v", plan.Intent.ResourceActions)
	}
	if !plan.Intent.NeedsKnowledge || !plan.ToolKnowledge.KnowledgeTriggered {
		t.Fatalf("expected mixed parking question to trigger knowledge too, intent=%#v tool=%#v", plan.Intent, plan.ToolKnowledge)
	}
	if !containsString(plan.ToolKnowledge.ExpectedResources, "intent task:hotel_info/parking=停车") {
		t.Fatalf("expected intent task trace, got %#v", plan.ToolKnowledge.ExpectedResources)
	}
	if len(plan.ReplyPlan.TaskPlans) != 3 {
		t.Fatalf("expected three reply task plans, got %#v", plan.ReplyPlan.TaskPlans)
	}
	if plan.ReplyPlan.TaskPlans[0].Output != "structured_resource_commit" || plan.ReplyPlan.TaskPlans[0].ResourceAction != "provide_location" {
		t.Fatalf("expected first task to commit location, got %#v", plan.ReplyPlan.TaskPlans)
	}
	if plan.ReplyPlan.TaskPlans[1].Output != "structured_resource_commit" || plan.ReplyPlan.TaskPlans[1].ResourceAction != "provide_mini_program" {
		t.Fatalf("expected second task to commit mini program, got %#v", plan.ReplyPlan.TaskPlans)
	}
	if plan.ReplyPlan.TaskPlans[2].Output != "knowledge_text_reply" || plan.ReplyPlan.TaskPlans[2].Intent != "hotel_info" {
		t.Fatalf("expected third task to answer parking with knowledge, got %#v", plan.ReplyPlan.TaskPlans)
	}
	if !strings.Contains(plan.Prompt, "【多任务回复计划】") || !strings.Contains(plan.Prompt, "不要承诺“发你/已经发/后续发”") {
		t.Fatalf("expected multi-task prompt to separate text and commit actions, got %q", plan.Prompt)
	}
	for _, forbidden := range []string{"provide_location", "provide_mini_program", "定位和入住小程序", "小程序发你", "定位发你"} {
		if strings.Contains(plan.Prompt, forbidden) {
			t.Fatalf("generate prompt must not expose variable task names %q: %s", forbidden, plan.Prompt)
		}
	}
	scope := buildGenerationScopeInstruction(plan.Intent, plan.ReplyPlan)
	if !strings.Contains(scope, "本阶段只输出酒店信息任务的文本答案") || !strings.Contains(scope, "停车") || !strings.Contains(scope, "Commit 阶段") {
		t.Fatalf("expected generation scope to isolate knowledge text from variable commits, got %q", scope)
	}
	for _, forbidden := range []string{"provide_location", "provide_mini_program", "定位和入住小程序", "小程序发你", "定位发你"} {
		if strings.Contains(scope, forbidden) {
			t.Fatalf("generation scope must not expose variable task names %q: %s", forbidden, scope)
		}
	}
	boundary := buildCurrentTurnBoundaryInstruction(req, adapter.HistoryBuildResult{}, plan.Intent)
	if !strings.Contains(boundary, "停车") {
		t.Fatalf("expected boundary to keep knowledge task, got %q", boundary)
	}
	for _, forbidden := range []string{"定位和入住小程序", "定位发我", "小程序也发一下"} {
		if strings.Contains(boundary, forbidden) {
			t.Fatalf("boundary must not expose variable request %q: %s", forbidden, boundary)
		}
	}
	generationText := buildGenerationUserMessageText(req.UserMessage.Content, plan.Intent)
	if generationText != "停车" {
		t.Fatalf("expected Generate user text to contain only knowledge task, got %q", generationText)
	}
	ledger := buildInitialActionLedger(plan.Intent)
	if !actionLedgerHas(ledger.RequestedActions, "provide_location", "location") || !actionLedgerHas(ledger.RequestedActions, "provide_mini_program", "mini_program") || !actionLedgerHas(ledger.RequestedActions, "knowledge_lookup", "") {
		t.Fatalf("expected action ledger to request variables and knowledge, got %#v", ledger.RequestedActions)
	}
}

func TestDeferredKnowledgeGenerationInstructionsUseActiveReplyPlan(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:   "service_request",
		SubIntent:       "air_conditioner",
		NeedsKnowledge:  true,
		NeedsResource:   true,
		ResourceActions: []string{"provide_mini_program"},
	}
	activePlan := callbacks.ReplyPlanTraceData{
		Intent:     "service_request",
		AnswerGoal: "回答仍有直接证据的当前问题",
		Style:      "自然微信口吻，1-3句",
		TaskPlans: []callbacks.ReplyTaskPlanTraceData{
			{Intent: "hotel_variable", SubIntent: "mini_program", Output: "structured_resource_commit", ResourceAction: "provide_mini_program"},
			{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
		},
	}
	prompt := buildIntentStagePrompt(selectIntentPromptPack(intent), activePlan)
	scope := buildGenerationScopeInstruction(intent, activePlan)
	for name, value := range map[string]string{"prompt": prompt, "scope": scope} {
		if !strings.Contains(value, "顺便问早餐几点") {
			t.Fatalf("%s must contain the active breakfast task, got %q", name, value)
		}
		if strings.Contains(value, "空调坏了") {
			t.Fatalf("%s must not reintroduce the deferred air-conditioner task, got %q", name, value)
		}
	}
}

func TestRuntimePipelineDoesNotInferTasksFromModelReason(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "定位发我，小程序也发一下，停车在哪"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{
		PrimaryIntent:    "hotel_variable",
		SubIntent:        "location",
		IntentConfidence: 0.9,
		ShouldReply:      true,
		NeedsResource:    true,
		NeedsKnowledge:   false,
		ResourceAction:   "provide_location",
		ResourceActions:  []string{"provide_location", "provide_mini_program"},
		SecondaryIntents: nil,
		MixedSubTasks:    nil,
		IntentTasks:      nil,
		Reason:           "用户明确索要定位和小程序，属于hotel_variable；同时询问停车位置，属于hotel_info。根据多任务规则，primaryIntent选hotel_variable，同时通过intentTasks保留hotel_info子任务并设置needsKnowledge=true。",
	}})
	if plan.Intent.NeedsKnowledge || plan.ToolKnowledge.KnowledgeTriggered {
		t.Fatalf("reason text must not create hidden knowledge task, intent=%#v tool=%#v", plan.Intent, plan.ToolKnowledge)
	}
	if len(plan.Intent.IntentTasks) != 0 {
		t.Fatalf("reason text must not infer intentTasks, got %#v", plan.Intent.IntentTasks)
	}
}

func TestRuntimePipelineDerivesSummaryFromIntentTasks(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "定位发我，停车在哪"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{
		PrimaryIntent:    "hotel_info",
		SubIntent:        "parking",
		IntentConfidence: 0.9,
		ShouldReply:      true,
		NeedsKnowledge:   true,
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "hotel_variable", Text: "定位发我", NeedsResource: true, ResourceAction: "provide_location"},
			{Intent: "hotel_info", SubIntent: "parking", Text: "停车在哪", NeedsKnowledge: true},
		},
		Reason: "模型识别出酒店信息主问题，同时有定位变量子任务",
	}})
	if plan.Intent.PrimaryIntent != "hotel_variable" {
		t.Fatalf("expected primary intent to be derived from variable task for commit closure, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge || !plan.Intent.NeedsResource {
		t.Fatalf("expected mixed hotel_info to need both knowledge and resource, got %#v", plan.Intent)
	}
	if len(plan.Intent.ResourceActions) != 1 || plan.Intent.ResourceActions[0] != "provide_location" {
		t.Fatalf("expected variable task resource action to survive, got %#v", plan.Intent.ResourceActions)
	}
	if !containsString(plan.ToolKnowledge.ExpectedResources, "intent task:hotel_variable:provide_location=定位发我") {
		t.Fatalf("expected variable intent task trace, got %#v", plan.ToolKnowledge.ExpectedResources)
	}
	if len(plan.ReplyPlan.TaskPlans) != 2 {
		t.Fatalf("expected two task plans, got %#v", plan.ReplyPlan.TaskPlans)
	}
	if plan.ReplyPlan.TaskPlans[0].Output != "structured_resource_commit" || plan.ReplyPlan.TaskPlans[1].Output != "knowledge_text_reply" {
		t.Fatalf("expected variable commit then knowledge reply plans, got %#v", plan.ReplyPlan.TaskPlans)
	}
}

func actionLedgerHas(items []callbacks.ActionLedgerItem, action string, resourceType string) bool {
	for _, item := range items {
		if item.Action == action && item.ResourceType == resourceType {
			return true
		}
	}
	return false
}

func TestRuntimePipelineCheckinProcessAttachesMiniProgramTask(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 100, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 90, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "我要办理入住"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{
		PrimaryIntent:    "hotel_info",
		SubIntent:        "check_in",
		IntentConfidence: 0.82,
		ShouldReply:      true,
		NeedsKnowledge:   true,
		Reason:           "模型识别为办理入住流程",
	}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.SubIntent != "checkin_process" {
		t.Fatalf("expected checkin to remain hotel_info/checkin_process, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge || !plan.Intent.NeedsResource || plan.Intent.NeedsHumanRoute {
		t.Fatalf("expected checkin to need knowledge and mini program resource only, got %#v", plan.Intent)
	}
	if len(plan.Intent.ResourceActions) != 1 || plan.Intent.ResourceActions[0] != "provide_mini_program" {
		t.Fatalf("expected checkin to attach mini program resource action, got %#v", plan.Intent.ResourceActions)
	}
	if len(plan.ReplyPlan.TaskPlans) != 2 {
		t.Fatalf("expected checkin knowledge task and mini program commit task, got %#v", plan.ReplyPlan.TaskPlans)
	}
	if plan.ReplyPlan.TaskPlans[0].Output != "knowledge_text_reply" || plan.ReplyPlan.TaskPlans[0].SubIntent != "checkin_process" {
		t.Fatalf("expected first task to answer checkin steps with knowledge, got %#v", plan.ReplyPlan.TaskPlans)
	}
	if plan.ReplyPlan.TaskPlans[1].Output != "structured_resource_commit" || plan.ReplyPlan.TaskPlans[1].ResourceAction != "provide_mini_program" {
		t.Fatalf("expected second task to commit mini program, got %#v", plan.ReplyPlan.TaskPlans)
	}
	if !strings.Contains(plan.Prompt, "结构化变量任务只由 Commit 阶段发送") {
		t.Fatalf("expected checkin plan to preserve the commit boundary, got %q", plan.Prompt)
	}
}

func TestRuntimePipelineDoesNotNormalizeDeviceServiceRequestByKeyword(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "过几天我又来了，电视打不开"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{
		PrimaryIntent:    "service_request",
		SubIntent:        "tv_malfunction",
		IntentConfidence: 0.84,
		ShouldReply:      true,
		NeedsKnowledge:   true,
		Reason:           "模型识别为电视故障服务请求",
	}})
	if plan.Intent.PrimaryIntent != "service_request" || plan.Intent.SubIntent != "tv_malfunction" {
		t.Fatalf("expected model service_request to survive without keyword normalization, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge || plan.Intent.NeedsResource || plan.Intent.NeedsHumanRoute {
		t.Fatalf("expected device service request to use knowledge without resource or handoff, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineDoesNotNormalizeAirConditionerRepairByKeyword(t *testing.T) {
	for _, subIntent := range []string{"air_conditioner_repair", "hvac_issue", "ac_not_cooling"} {
		req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "空调不制冷怎么办"}}
		plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{
			PrimaryIntent:    "service_request",
			SubIntent:        subIntent,
			IntentConfidence: 0.84,
			ShouldReply:      true,
			NeedsKnowledge:   true,
			Reason:           "模型识别为空调维修服务请求",
		}})
		if plan.Intent.PrimaryIntent != "service_request" || plan.Intent.SubIntent != subIntent {
			t.Fatalf("expected %s model intent to survive without keyword normalization, got %#v", subIntent, plan.Intent)
		}
		if !plan.Intent.NeedsKnowledge || plan.Intent.NeedsResource || plan.Intent.NeedsHumanRoute {
			t.Fatalf("expected air conditioner service request to use knowledge without resource or handoff, got %#v", plan.Intent)
		}
	}
}

func TestRuntimePipelineDoesNotLetKeywordVariableBeatModelHotelInfo(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "定位和入住小程序都发我，顺便问下停车"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "parking", IntentConfidence: 0.9, ShouldReply: true, NeedsKnowledge: true, Reason: "模型只看到了停车问题"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.NeedsResource {
		t.Fatalf("expected model hotel_info to remain unchanged by keyword variable guard, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge || !plan.ToolKnowledge.KnowledgeTriggered {
		t.Fatalf("expected hotel_info knowledge to remain, intent=%#v tool=%#v", plan.Intent, plan.ToolKnowledge)
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
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "interaction", SubIntent: "actionable_media_context", IntentConfidence: 0.55, ShouldReply: true, NeedsClarification: true, Reason: "模型识别为媒体内容中有问题但业务意图不明确"}})
	if !plan.Intent.ShouldReply {
		t.Fatal("expected actionable media to reply")
	}
	if plan.Intent.PrimaryIntent != "interaction" || plan.Intent.SubIntent != "actionable_media_context" {
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
	if !strings.Contains(mixed, "酒店变量-定位/地址") {
		t.Fatalf("expected location variable instruction, got %q", mixed)
	}
	if strings.Contains(mixed, "酒店变量-入住小程序") {
		t.Fatalf("must not infer mini program from merged text when intent action is location only, got %q", mixed)
	}
	multi := buildHotelVariableInstructionFromInstance(
		&models.WxWorkProtocolInstance{EmployeeName: "测试门店", StoreLatitude: "31.824097", StoreLongitude: "117.263908", DefaultMiniProgramPayload: `{"title":"入住小程序"}`},
		"定位和入住小程序都发我",
		callbacks.IntentTraceData{PrimaryIntent: "hotel_variable", ResourceAction: "provide_location", ResourceActions: []string{"provide_location", "provide_mini_program"}},
	)
	if !strings.Contains(multi, "酒店变量-定位/地址") || !strings.Contains(multi, "酒店变量-入住小程序") {
		t.Fatalf("expected both requested variables from resourceActions, got %q", multi)
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
		{text: "我要办理入住，发下小程序", resourceAction: "provide_mini_program"},
		{text: "酒店在哪里，定位发我", resourceAction: "provide_location"},
		{text: "你们电话多少", resourceAction: "provide_phone"},
	}
	for _, tc := range cases {
		req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: tc.text}}
		resourceType := "mini_program"
		if tc.resourceAction == "provide_location" {
			resourceType = "location"
		}
		if tc.resourceAction == "provide_phone" {
			resourceType = "phone"
		}
		plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_variable", SubIntent: resourceType, IntentConfidence: 0.9, ShouldReply: true, NeedsResource: true, ResourceAction: tc.resourceAction, Reason: "模型识别为酒店变量"}})
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

func TestRuntimePipelineUsesConfiguredPromptForModelIntent(t *testing.T) {
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
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "network_wifi", IntentConfidence: 0.9, ShouldReply: true, NeedsKnowledge: true, Reason: "模型识别为酒店信息"}})
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

func TestRuntimePipelineDoesNotLetNetworkKeywordsBeatServiceRequestIntent(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{
		Code:           "service_request",
		Name:           "服务请求",
		Priority:       300,
		MatchMode:      "keyword",
		Keywords:       "网连不上,连不上网,网络不好",
		NeedsKnowledge: true,
		Status:         enums.StatusOk,
	})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{
		Code:           "hotel_info",
		Name:           "酒店信息",
		Priority:       100,
		MatchMode:      "keyword",
		Keywords:       "WiFi,无线网,网络",
		NeedsKnowledge: true,
		Status:         enums.StatusOk,
	})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "房间网连不上"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "service_request", SubIntent: "wifi_connectivity", IntentConfidence: 0.86, ShouldReply: true, NeedsKnowledge: true, Reason: "模型认为是网络服务请求"}})
	if plan.Intent.PrimaryIntent != "service_request" || plan.Intent.SubIntent != "wifi_connectivity" {
		t.Fatalf("expected model service_request to remain unchanged by keyword override, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge || plan.Intent.NeedsHumanRoute {
		t.Fatalf("expected knowledge path without handoff, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineDoesNotLetEquipmentKeywordsBeatModelHotelInfo(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "service_request", Name: "服务请求", Priority: 100, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "空调不制冷怎么办"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "store_knowledge", IntentConfidence: 0.87, ShouldReply: true, NeedsKnowledge: true, Reason: "模型误判为空调使用说明"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.SubIntent != "store_knowledge" {
		t.Fatalf("expected model hotel_info to remain unchanged by equipment keywords, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsHumanRoute {
		t.Fatalf("equipment service request must not directly trigger handoff, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineDoesNotLetMiniProgramIssueKeywordsBeatVariableIntent(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "小程序打不开"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_variable", SubIntent: "mini_program", IntentConfidence: 0.9, ShouldReply: true, NeedsResource: true, ResourceAction: "send_miniprogram", Reason: "模型只看到了小程序"}})
	if plan.Intent.PrimaryIntent != "hotel_variable" || plan.Intent.SubIntent != "mini_program" {
		t.Fatalf("expected model hotel_variable to remain unchanged by issue keywords, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsKnowledge || !plan.Intent.NeedsResource {
		t.Fatalf("model variable intent should use resource injection only, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineDoesNotLetMiniProgramIssueKeywordsBeatServiceRequestIntent(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "service_request", Name: "服务请求", Priority: 100, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "小程序打不开"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "service_request", SubIntent: "technical_issue", IntentConfidence: 0.9, ShouldReply: true, NeedsKnowledge: true, Reason: "模型误判为技术服务请求"}})
	if plan.Intent.PrimaryIntent != "service_request" || plan.Intent.SubIntent != "technical_issue" {
		t.Fatalf("expected model service_request to remain unchanged by keyword override, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineDoesNotLetExplicitLocationKeywordsBeatModelHotelInfo(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "酒店在哪里"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "store_knowledge", IntentConfidence: 0.9, ShouldReply: true, NeedsKnowledge: true, Reason: "模型按地址知识回答"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.ResourceAction != "" {
		t.Fatalf("expected model hotel_info to remain unchanged by location keywords, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineLocationVariableWithEntranceQuestionUsesKnowledge(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "我到楼下了，入口怎么走，定位也发我"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{
		PrimaryIntent:    "hotel_variable",
		SubIntent:        "location",
		IntentConfidence: 0.9,
		ShouldReply:      true,
		NeedsResource:    true,
		NeedsKnowledge:   true,
		ResourceAction:   "provide_location",
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "hotel_info", SubIntent: "entrance_navigation", Text: "入口怎么走", NeedsKnowledge: true},
			{Intent: "hotel_variable", SubIntent: "location", Text: "定位也发我", NeedsResource: true, ResourceAction: "provide_location"},
		},
		Reason: "模型识别为定位变量，同时入口问题需要知识库",
	}})
	if plan.Intent.PrimaryIntent != "hotel_variable" || plan.Intent.ResourceAction != "provide_location" {
		t.Fatalf("expected location variable, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge {
		t.Fatalf("expected entrance question mixed with location variable to need knowledge, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineDoesNotLetMixedPhoneKeywordBeatNetworkInfo(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "WiFi连不上，发票怎么开，顺便给电话"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "network_wifi", IntentConfidence: 0.9, ShouldReply: true, NeedsKnowledge: true}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.ResourceAction != "" {
		t.Fatalf("expected model hotel_info to remain unchanged by phone keyword, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge {
		t.Fatalf("expected hotel_info knowledge to remain, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineDoesNotSupplementSiblingResourceActionsByKeyword(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "小程序和电话都给我，顺便问下早餐在哪吃"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "surrounding_facilities", IntentConfidence: 0.9, ShouldReply: true, NeedsKnowledge: true, NeedsResource: true, ResourceAction: "provide_mini_program", ResourceActions: []string{"provide_mini_program"}, Reason: "模型识别到小程序变量和早餐知识，但漏掉电话变量"}})
	if plan.Intent.PrimaryIntent != "hotel_info" {
		t.Fatalf("expected primary hotel_info to remain model-led, got %#v", plan.Intent)
	}
	if !containsString(plan.Intent.ResourceActions, "provide_mini_program") || containsString(plan.Intent.ResourceActions, "provide_phone") {
		t.Fatalf("expected only model-provided mini program action, got %#v", plan.Intent.ResourceActions)
	}
	if !plan.Intent.NeedsKnowledge || !plan.Intent.NeedsResource {
		t.Fatalf("expected mixed knowledge and resources, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineDoesNotTurnMiniProgramPhoneMixedBreakfastIntoHotelInfo(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_variable", Name: "酒店变量", Priority: 100, MatchMode: "hybrid", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "小程序和电话都给我，顺便问下早餐在哪吃"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{
		PrimaryIntent:    "hotel_variable",
		SubIntent:        "mini_program",
		IntentConfidence: 0.9,
		ShouldReply:      true,
		NeedsKnowledge:   true,
		NeedsResource:    true,
		ResourceAction:   "provide_mini_program",
		ResourceActions:  []string{"provide_mini_program", "provide_phone"},
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "hotel_variable", SubIntent: "mini_program", Text: "小程序", NeedsResource: true, ResourceAction: "provide_mini_program"},
			{Intent: "hotel_variable", SubIntent: "phone", Text: "电话", NeedsResource: true, ResourceAction: "provide_phone"},
			{Intent: "hotel_info", SubIntent: "breakfast", Text: "早餐在哪吃", NeedsKnowledge: true},
		},
		Reason: "模型识别为变量加早餐知识混合意图",
	}})
	if plan.Intent.PrimaryIntent != "hotel_variable" {
		t.Fatalf("expected variable primary to survive breakfast facility wording, got %#v", plan.Intent)
	}
	if !containsString(plan.Intent.ResourceActions, "provide_mini_program") || !containsString(plan.Intent.ResourceActions, "provide_phone") || !plan.Intent.NeedsKnowledge {
		t.Fatalf("expected mini program, phone and breakfast knowledge, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineDoesNotLetSimpleConfirmKeywordsBeatModelVariable(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "好的，第100次确认"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_variable", SubIntent: "location", IntentConfidence: 0.82, ShouldReply: true, NeedsResource: true, ResourceAction: "provide_location", Reason: "模型沿用了上一轮定位"}})
	if plan.Intent.PrimaryIntent != "hotel_variable" {
		t.Fatalf("expected model variable intent to remain unchanged by simple confirm keywords, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineDoesNotLetLatestThanksKeywordsBeatModelServiceRequest(t *testing.T) {
	content := "客人刚才连续发了几条消息：\n[10:00] 房间纸巾没了\n[10:01] 谢谢"
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: content}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "service_request", SubIntent: "room_supply_request", IntentConfidence: 0.82, ShouldReply: true, NeedsKnowledge: true, Reason: "模型沿用了上一条用品请求"}})
	if plan.Intent.PrimaryIntent != "service_request" {
		t.Fatalf("expected model service_request to remain unchanged by thanks keyword, got %#v", plan.Intent)
	}
}

func TestCurrentTurnBoundaryInstructionForSocialThanksBlocksOldServicePromise(t *testing.T) {
	content := "客人刚才连续发了几条消息：\n[10:00] 房间纸巾没了\n[10:01] 谢谢"
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: content}}
	instruction := buildCurrentTurnBoundaryInstruction(req, adapter.HistoryBuildResult{}, callbacks.IntentTraceData{PrimaryIntent: "interaction", SubIntent: "social"})
	if !strings.Contains(instruction, "不要继续承诺上一条送物") {
		t.Fatalf("expected social boundary to block stale service promise, got %q", instruction)
	}
}

func TestCurrentTurnBoundaryInstructionBlocksUnsupportedActionPromises(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "停车和剃须刀怎么办"}}
	instruction := buildCurrentTurnBoundaryInstruction(req, adapter.HistoryBuildResult{}, callbacks.IntentTraceData{PrimaryIntent: "service_request", SubIntent: "room_supply_request"})
	if !strings.Contains(instruction, "动作安全") {
		t.Fatalf("expected current-turn boundary to include action safety, got %q", instruction)
	}
	for _, expected := range []string{"真实动作", "内部核实", "通知转告", "现场查看", "接待路由"} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("expected action safety to include %q, got %q", expected, instruction)
		}
	}
	if !strings.Contains(instruction, "店助补充") {
		t.Fatalf("expected current-turn boundary to block internal knowledge notes, got %q", instruction)
	}
}

func TestCurrentTurnBoundaryInstructionForEmojiBlocksInternalReasoning(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "😅"}}
	instruction := buildCurrentTurnBoundaryInstruction(req, adapter.HistoryBuildResult{}, callbacks.IntentTraceData{PrimaryIntent: "interaction", SubIntent: "social"})
	if !strings.Contains(instruction, "不得输出思考过程") || !strings.Contains(instruction, "我在，有事发我") {
		t.Fatalf("expected emoji social boundary to block analysis and guide lightweight reply, got %q", instruction)
	}
}

func TestCurrentTurnBoundaryInstructionForCorrectionRequiresFriendlyAck(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "我没给你发语音大哥"}}
	instruction := buildCurrentTurnBoundaryInstruction(req, adapter.HistoryBuildResult{}, callbacks.IntentTraceData{PrimaryIntent: "interaction", SubIntent: "correction"})
	if !strings.Contains(instruction, "纠错/误会") || !strings.Contains(instruction, "只输出一句完整短句") || !strings.Contains(instruction, "不要补答或追问任何旧业务主题") {
		t.Fatalf("expected correction boundary to scope the reply to the current correction, got %q", instruction)
	}
}

func TestRuntimePipelineInvoiceAttachmentFollowUpUsesKnowledge(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "资料够了吗"}}
	history := adapter.HistoryBuildResult{RawItems: []models.Message{{
		ID:             11,
		ConversationID: 7,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeAttachment,
		Content:        "invoice-info.pdf",
		Payload:        `{"mediaText":"文件是一份公司开票资料，包含抬头、税号、邮箱。","mediaUnderstandingStatus":"understood"}`,
	}}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "invoice", IntentConfidence: 0.86, ShouldReply: true, NeedsKnowledge: true, Reason: "模型结合附件识别为发票资料问题"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.SubIntent != "invoice" {
		t.Fatalf("expected invoice attachment follow-up to use hotel_info/invoice, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge {
		t.Fatal("invoice attachment follow-up should use knowledge")
	}
	instruction := buildCurrentTurnBoundaryInstruction(req, history, plan.Intent)
	if !strings.Contains(instruction, "动作安全") || !strings.Contains(instruction, "进入接待路由") || strings.Contains(instruction, "只能说当前资料没写明") {
		t.Fatalf("expected invoice follow-up to retain category-level action safety, got %q", instruction)
	}
}

func TestRuntimePipelineOrderAttachmentFollowUpUsesCheckInKnowledge(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "这个能入住吗"}}
	history := adapter.HistoryBuildResult{RawItems: []models.Message{{
		ID:             11,
		ConversationID: 7,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeAttachment,
		Content:        "order.pdf",
		Payload:        `{"mediaText":"文件是一张订单确认单，显示入住人为张先生，入住日期为今天。","mediaUnderstandingStatus":"understood"}`,
	}}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "check_in", IntentConfidence: 0.86, ShouldReply: true, NeedsKnowledge: true, Reason: "模型结合订单附件识别为入住问题"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.SubIntent != "checkin_process" {
		t.Fatalf("expected order attachment follow-up to use hotel_info/checkin_process, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge || !plan.Intent.NeedsResource || !containsString(plan.Intent.ResourceActions, "provide_mini_program") {
		t.Fatalf("order attachment follow-up should use knowledge plus mini program resource injection, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineReturningCustomerBoundaryRejectsOldRoom(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "三天后我又来了，空调不制冷"}}
	intent := callbacks.IntentTraceData{PrimaryIntent: "service_request", SubIntent: "maintenance"}
	instruction := buildCurrentTurnBoundaryInstruction(req, adapter.HistoryBuildResult{}, intent)
	if !strings.Contains(instruction, "旧消息或长期记忆里的房号") || !strings.Contains(instruction, "不能沿用旧房号") {
		t.Fatalf("expected returning customer boundary to reject old room, got %q", instruction)
	}
}

func TestRuntimePipelineWifiMediaFollowUpUsesKnowledge(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "这个怎么弄"}}
	history := adapter.HistoryBuildResult{RawItems: []models.Message{{
		ID:             11,
		ConversationID: 7,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeImage,
		Content:        "wifi-error.png",
		Payload:        `{"mediaText":"截图显示手机 WiFi 连接失败，提示无法加入网络。","mediaUnderstandingStatus":"understood"}`,
	}}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "network_wifi", IntentConfidence: 0.82, ShouldReply: true, NeedsKnowledge: true, Reason: "模型结合截图识别为网络问题"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.SubIntent != "network_wifi" || !plan.Intent.NeedsKnowledge {
		t.Fatalf("expected WiFi media follow-up to use hotel_info/network_wifi, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineWaterMediaFollowUpUsesKnowledge(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "这个水收费吗"}}
	history := adapter.HistoryBuildResult{RawItems: []models.Message{{
		ID:             11,
		ConversationID: 7,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeImage,
		Content:        "room.jpg",
		Payload:        `{"mediaText":"图片为酒店房间桌面，有两瓶矿泉水和一张 WiFi 牌。","mediaUnderstandingStatus":"understood"}`,
	}}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, history, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "supplies_self_help", IntentConfidence: 0.82, ShouldReply: true, NeedsKnowledge: true, Reason: "模型结合图片识别为矿泉水收费问题"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.SubIntent != "supplies_self_help" || !plan.Intent.NeedsKnowledge {
		t.Fatalf("expected water media follow-up to use hotel_info/supplies_self_help, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineVoiceCorrectionUsesSocialConfirm(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "我没给你发语音大哥"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "interaction", SubIntent: "correction", IntentConfidence: 0.84, ShouldReply: true, Reason: "模型识别为纠正误会"}})
	if plan.Intent.PrimaryIntent != "interaction" || plan.Intent.SubIntent != "correction" {
		t.Fatalf("expected voice correction to use interaction/correction, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineBusinessCorrectionUsesHotelInfo(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "我说的是停车场在哪里"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "parking", IntentConfidence: 0.84, ShouldReply: true, NeedsKnowledge: true, Reason: "模型结合当前纠正识别为停车场位置问题"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.SubIntent != "parking" || !plan.Intent.NeedsKnowledge {
		t.Fatalf("expected model business correction to use hotel_info knowledge, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsResource || plan.Intent.ResourceAction != "" {
		t.Fatalf("parking correction must not send store location variable, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineFacilityWhereDoesNotBecomeStoreLocationVariable(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "洗衣房在哪里"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "laundry_location", IntentConfidence: 0.82, ShouldReply: true, NeedsKnowledge: true, Reason: "模型识别为酒店设施位置问题"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.SubIntent != "laundry_location" || !plan.Intent.NeedsKnowledge {
		t.Fatalf("expected facility location question to use hotel_info knowledge, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsResource || len(plan.Intent.ResourceActions) != 0 {
		t.Fatalf("facility location must not commit hotel location variable, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineVoiceMixedResourceUsesMediaTextForActionLedger(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{
		MessageType: enums.IMMessageTypeVoice,
		Content:     "voice-mixed.amr",
		Payload:     `{"mediaText":"我想问洗衣房在哪里，顺便把定位再发我一下。","mediaUnderstandingStatus":"understood"}`,
	}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{
		PrimaryIntent:    "hotel_variable",
		SubIntent:        "location",
		IntentConfidence: 0.82,
		ShouldReply:      true,
		NeedsKnowledge:   true,
		NeedsResource:    true,
		ResourceAction:   "provide_location",
		ResourceActions:  []string{"provide_location"},
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "hotel_info", SubIntent: "laundry_location", Text: "洗衣房在哪里", NeedsKnowledge: true},
			{Intent: "hotel_variable", SubIntent: "location", Text: "定位再发我一下", NeedsResource: true, ResourceAction: "provide_location"},
		},
		Reason: "模型从语音转写文本识别出洗衣房知识和定位变量两个任务",
	}})
	if plan.Intent.PrimaryIntent != "hotel_variable" || !plan.Intent.NeedsResource || !containsString(plan.Intent.ResourceActions, "provide_location") {
		t.Fatalf("expected voice media text to add location resource action, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge {
		t.Fatalf("expected voice mixed request to keep laundry knowledge, got %#v", plan.Intent)
	}
	if len(plan.ReplyPlan.TaskPlans) < 2 {
		t.Fatalf("expected mixed task plans for knowledge and resource, got %#v", plan.ReplyPlan.TaskPlans)
	}
}

func TestRuntimePipelineUnknownHotelInfoFallsBackToKnowledge(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "电视投屏怎么弄"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "interaction", IntentConfidence: 0.62, ShouldReply: true, NeedsClarification: true, Reason: "模型没有识别出业务分类"}})
	if plan.Intent.PrimaryIntent != "interaction" || plan.Intent.NeedsKnowledge {
		t.Fatalf("expected interaction to remain model-led without keyword fallback, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineAbuseWithoutHandoffDoesNotRequestHuman(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "你是个蠢猪"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "interaction", SubIntent: "frustration", IntentConfidence: 0.75, ShouldReply: true, Reason: "模型识别为不满情绪但没有明确人工或投诉升级"}})
	if plan.Intent.PrimaryIntent != "interaction" || plan.Intent.SubIntent != "frustration" {
		t.Fatalf("expected plain abuse to stay out of human handoff, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsHumanRoute || plan.ToolKnowledge.ToolTriggered {
		t.Fatalf("plain abuse must not trigger direct handoff, got intent=%#v tool=%#v", plan.Intent, plan.ToolKnowledge)
	}
}

func TestCurrentAndRecentMediaTextSkipsFailedVoiceUnderstanding(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "我吃的啥"}}
	history := adapter.HistoryBuildResult{RawItems: []models.Message{
		{
			ID:          11,
			SenderType:  enums.IMSenderTypeCustomer,
			MessageType: enums.IMMessageTypeImage,
			Content:     "meal.jpg",
			Payload:     `{"mediaText":"图片里是米饭、炒菜和汤。","mediaUnderstandingStatus":"understood"}`,
		},
		{
			ID:          12,
			SenderType:  enums.IMSenderTypeCustomer,
			MessageType: enums.IMMessageTypeVoice,
			Content:     "voice.amr",
			Payload:     `{"mediaText":"这条语音我没听清，方便打字说一下吗？","mediaUnderstandingStatus":"failed"}`,
		},
	}}
	got := currentAndRecentMediaText(req, history)
	if strings.Contains(got, "语音") || strings.Contains(got, "没听清") {
		t.Fatalf("failed voice understanding must not enter media context, got %q", got)
	}
	if !strings.Contains(got, "米饭") || !strings.Contains(got, "炒菜") {
		t.Fatalf("expected recent understood image context, got %q", got)
	}
}

func TestRuntimePipelineAmbiguousServiceWithoutMediaDowngradesClarify(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 100, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "这是什么服务"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "interaction", IntentConfidence: 0.8, ShouldReply: true, NeedsClarification: true, Reason: "模型识别为缺少上下文的含糊追问"}})
	if plan.Intent.PrimaryIntent != "interaction" {
		t.Fatalf("expected ambiguous service question without media to clarify, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsKnowledge {
		t.Fatal("ambiguous service question should not trigger knowledge without context")
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
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "human_complaint_risk", SubIntent: "emergency_safety", IntentConfidence: 0.96, ShouldReply: true, NeedsHumanRoute: true, Reason: "模型识别为突发安全风险"}})
	if plan.Intent.PrimaryIntent != "human_complaint_risk" || plan.Intent.SubIntent != "emergency_safety" || !plan.Intent.NeedsHumanRoute {
		t.Fatalf("expected emergency safety handoff intent, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineRoomNumber1208DoesNotTriggerEmergencySafety(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "service_request", Name: "服务请求", Priority: 100, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "纸巾没了，牙刷也没有，我住1208"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "service_request", IntentConfidence: 0.86, ShouldReply: true, NeedsKnowledge: true, Reason: "用品补充服务请求"}})
	if plan.Intent.PrimaryIntent == "human_complaint_risk" || plan.Intent.NeedsHumanRoute {
		t.Fatalf("room number containing 120 must not trigger emergency safety intent, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineDoesNotLetPriceKeywordsBeatModelHotelInfo(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "human_complaint_risk", Name: "人工/投诉/风险", Priority: 100, MatchMode: "hybrid", NeedsHumanRoute: true, Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "为什么我朋友比我订得便宜"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "store_knowledge", IntentConfidence: 0.86, ShouldReply: true, NeedsKnowledge: true, Reason: "模型误判为价格规则咨询"}})
	if plan.Intent.PrimaryIntent != "hotel_info" || plan.Intent.SubIntent != "store_knowledge" {
		t.Fatalf("expected model hotel_info to remain unchanged by price keywords, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsHumanRoute || !plan.Intent.NeedsKnowledge {
		t.Fatalf("expected model hotel_info knowledge path, got %#v", plan.Intent)
	}
}

func TestRuntimePipelineDoesNotLetLateCheckoutKeywordsBeatServiceRequestIntent(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "service_request", Name: "服务请求", Priority: 100, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 90, MatchMode: "hybrid", NeedsKnowledge: true, Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "我想延迟退房"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "service_request", IntentConfidence: 0.86, ShouldReply: true, NeedsKnowledge: true, Reason: "模型误判为执行请求"}})
	if plan.Intent.PrimaryIntent != "service_request" {
		t.Fatalf("expected model service_request to remain unchanged by checkout keywords, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge || plan.Intent.NeedsHumanRoute {
		t.Fatalf("expected model service_request knowledge path without direct handoff, got %#v", plan.Intent)
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

func TestRuntimePipelineServiceRequestCannotRequestDirectHandoff(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "service_request", Name: "服务请求", Priority: 100, MatchMode: "hybrid", NeedsHumanRoute: true, HumanRoutePolicy: "managed_mode", PromptPack: "服务请求先看当前门店知识库。", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "门锁不上，帮我看看怎么处理"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "service_request", IntentConfidence: 0.86, ShouldReply: true, NeedsHumanRoute: true, HumanRoutePolicy: "managed_mode", Reason: "模型错误携带人工路由标记"}})
	if plan.Intent.PrimaryIntent != "service_request" {
		t.Fatalf("expected service_request, got %#v", plan.Intent)
	}
	if plan.Intent.NeedsHumanRoute || plan.Intent.HumanRoutePolicy != "" {
		t.Fatalf("service_request must not request direct handoff, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsKnowledge || !plan.ToolKnowledge.KnowledgeTriggered || plan.ToolKnowledge.ToolTriggered {
		t.Fatalf("expected service_request to stay on knowledge/service path, got intent=%#v tool=%#v", plan.Intent, plan.ToolKnowledge)
	}
	if !strings.Contains(plan.Intent.Reason, "direct handoff only belongs") {
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
	if !plan.ToolKnowledge.ToolTriggered || len(plan.ToolKnowledge.ExpectedResources) == 0 || plan.ToolKnowledge.ExpectedResources[0] != "direct handoff policy" {
		t.Fatalf("expected human route tool trace, got %#v", plan.ToolKnowledge)
	}
}

func TestRuntimePipelineKeepsModelHumanRouteWithoutKeywordRewrite(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "human_complaint_risk", Name: "人工/投诉/风险", Priority: 100, MatchMode: "hybrid", NeedsHumanRoute: true, HumanRoutePolicy: "managed_mode", PromptPack: "按当前门店托管模式和排班路由。", Status: enums.StatusOk})
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "interaction", Name: "互动", Priority: 90, MatchMode: "hybrid", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "你是个蠢猪"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{
		PrimaryIntent:    "human_complaint_risk",
		SubIntent:        "insult_complaint",
		IntentConfidence: 0.91,
		ShouldReply:      true,
		NeedsHumanRoute:  true,
		Reason:           "模型把单纯辱骂误判为投诉风险",
	}})
	if plan.Intent.PrimaryIntent != "human_complaint_risk" || plan.Intent.SubIntent != "insult_complaint" {
		t.Fatalf("expected model human intent to stay intact without keyword rewrite, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsHumanRoute || !plan.ToolKnowledge.ToolTriggered {
		t.Fatalf("expected human route to follow the model result, intent=%#v tool=%#v", plan.Intent, plan.ToolKnowledge)
	}
	if strings.Contains(plan.Intent.Reason, "downgraded") {
		t.Fatalf("unexpected keyword downgrade reason, got %q", plan.Intent.Reason)
	}
}

func TestRuntimePipelineLegacyHandoffIntentCanonicalizesToFiveCategory(t *testing.T) {
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
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "interaction", Name: "互动", Priority: 90, MatchMode: "hybrid", Status: enums.StatusOk})
	req := RunInput{Conversation: models.Conversation{ID: 7}, UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "帮我查一下合肥今天的天气"}}
	plan := buildRuntimePipelinePlanWithModel(context.Background(), req, adapter.HistoryBuildResult{}, stubRuntimeIntentModelDetector{intent: callbacks.IntentTraceData{PrimaryIntent: "interaction", SubIntent: "weather_query", IntentConfidence: 0.88, ShouldReply: true, NeedsTool: true, ResourceAction: "get_weather", Reason: "模型识别为天气闲聊查询"}})
	if plan.Intent.PrimaryIntent != "interaction" || plan.Intent.SubIntent != "weather_query" {
		t.Fatalf("expected weather query under interaction, got %#v", plan.Intent)
	}
	if !plan.Intent.NeedsTool || plan.Intent.NeedsKnowledge || plan.Intent.ResourceAction != "get_weather" {
		t.Fatalf("expected weather tool only, got %#v", plan.Intent)
	}
	if !plan.ToolKnowledge.ToolTriggered || plan.ToolKnowledge.KnowledgeTriggered {
		t.Fatalf("expected tool trace without knowledge, got %#v", plan.ToolKnowledge)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestResolveRuntimeIntentDetectAIConfigUsesStoreCredentialAndIgnoresLegacyOverrides(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	fallback := models.AIConfig{ID: 1, Name: "reply", Provider: enums.AIProviderOpenAI, ModelType: enums.AIModelTypeLLM, ModelName: "reply-model"}
	globalIntent := models.AIConfig{ID: 3, Name: "global intent", Provider: enums.AIProviderOpenAI, BaseURL: "https://api.example.com/v1", APIKey: "sk", ModelType: enums.AIModelTypeLLM, ModelName: "global-intent-model", Status: enums.StatusOk, IntentDetectEnabled: true, SortNo: 2}
	if err := sqls.DB().Create(&globalIntent).Error; err != nil {
		t.Fatalf("create global ai config: %v", err)
	}
	if err := sqls.DB().Create(&models.Store{ID: 5, CompanyID: 2, StoreCode: "intent-store-5", Name: "store", Status: enums.StatusOk}).Error; err != nil {
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
	if err := sqls.DB().Create(&models.ModelProfileTemplate{
		ID: 1, Name: "runtime template", Revision: 8,
		GatewayBaseURL: "https://store-gateway.example.com/v1", Status: "active",
	}).Error; err != nil {
		t.Fatalf("create model profile template: %v", err)
	}
	if err := sqls.DB().Create(&models.ModelProfileSlot{
		TemplateID: 1, UsageCode: services.ModelProfileUsageIntentDetectLLM,
		DisplayName: "intent", ModelType: enums.AIModelTypeLLM,
		Provider: string(enums.AIProviderOpenAI), ModelName: "store-intent-model",
		APIMode: "chat_completions", TimeoutMS: 30000, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("create intent model slot: %v", err)
	}
	const masterKeyRaw = "0123456789abcdef0123456789abcdef"
	masterKey := base64.StdEncoding.EncodeToString([]byte(masterKeyRaw))
	config.SetCurrent(&config.Config{StoreCredential: config.StoreCredentialConfig{MasterKey: masterKey}})
	t.Cleanup(func() {
		config.SetCurrent(&config.Config{})
	})
	cipher, err := securex.NewAESGCM(masterKey)
	if err != nil {
		t.Fatalf("create credential cipher: %v", err)
	}
	const revision int64 = 4
	encryptedKey, nonce, err := cipher.Encrypt("sk-store-bound", []byte(fmt.Sprintf("store:%d:revision:%d", 5, revision)))
	if err != nil {
		t.Fatalf("encrypt credential: %v", err)
	}
	if err := sqls.DB().Create(&models.StoreModelCredential{
		CompanyID: 2, StoreID: 5, EncryptedKey: encryptedKey, KeyNonce: nonce,
		KeyFingerprint:     securex.Fingerprint("sk-store-bound"),
		CredentialRevision: revision, Status: "active",
	}).Error; err != nil {
		t.Fatalf("create store credential: %v", err)
	}
	got, credentialRevision, err := resolveRuntimeIntentDetectAIConfigWithRevision(RunInput{
		Conversation: models.Conversation{ID: 7},
		AIConfig:     fallback,
	})
	if err != nil {
		t.Fatalf("resolve intent model: %v", err)
	}
	if got.APIKey != "sk-store-bound" || got.ModelName != "store-intent-model" || got.BaseURL != "https://store-gateway.example.com/v1" {
		t.Fatalf("expected store credential and template slot, got %#v", got)
	}
	if credentialRevision != revision {
		t.Fatalf("expected credential revision %d, got %d", revision, credentialRevision)
	}
}

func TestResolveRuntimeIntentDetectAIConfigFailsClosedWithoutStoreRoute(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	fallback := models.AIConfig{ID: 1, Name: "reply", Provider: enums.AIProviderOpenAI, ModelType: enums.AIModelTypeLLM, ModelName: "reply-model"}
	got, _, err := resolveRuntimeIntentDetectAIConfigWithRevision(RunInput{Conversation: models.Conversation{ID: 7}, AIConfig: fallback})
	if err == nil {
		t.Fatalf("expected missing store route to fail closed, got %#v", got)
	}
	if got.ModelName != "" || got.APIKey != "" {
		t.Fatalf("legacy fallback must not be selected, got %#v", got)
	}
}

func seedRuntimeIntentConfig(t *testing.T, item models.ReplyIntentConfig) {
	t.Helper()
	if item.ScopeType == "" {
		item.ScopeType = "global"
	}
	if item.IntentProfileID == 0 {
		profile := &models.ReplyIntentProfile{}
		if err := sqls.DB().Where("code = ?", "hotel").First(profile).Error; err != nil {
			t.Fatalf("load test hotel intent profile: %v", err)
		}
		item.IntentProfileID = profile.ID
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
	if err := db.AutoMigrate(
		&models.ReplyIntentProfile{},
		&models.ReplyIntentConfig{},
		&models.AIConfig{},
		&models.Asset{},
		&models.Store{},
		&models.StoreAIModelSetting{},
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.Message{},
		&models.WxWorkProtocolInstance{},
		&models.StoreModelCredential{},
		&models.ModelProfileTemplate{},
		&models.ModelProfileSlot{},
		&models.WxWorkCustomerHandoffSetting{},
		&models.KnowledgeResourceGroup{},
		&models.KnowledgeResourceItem{},
	); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	if err := db.Create(&models.ReplyIntentProfile{Code: "hotel", Name: "测试酒店行业", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("seed hotel intent profile: %v", err)
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
