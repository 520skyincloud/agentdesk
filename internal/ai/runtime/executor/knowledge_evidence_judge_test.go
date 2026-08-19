package executor

import (
	"testing"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestKnowledgeEvidenceJudgeSeparatesNormalFlowFromExceptionFAQ(t *testing.T) {
	normal := runtimeTaskKnowledgeItem{Query: "我要办入住 入住流程", SubIntent: "checkin_process"}
	exception := rag.RetrieveResult{Content: "入住小程序打不开或手机不能使用时，请联系工作人员处理。"}
	if !knowledgeEvidenceMismatchesTask(normal, exception) {
		t.Fatal("normal check-in flow must not consume an exception-handling FAQ")
	}

	exceptionTask := runtimeTaskKnowledgeItem{Query: "入住小程序打不开怎么办 入住流程", SubIntent: "checkin_process"}
	if knowledgeEvidenceMismatchesTask(exceptionTask, exception) {
		t.Fatal("an exception question must retain the matching exception FAQ")
	}

	normalWithFallback := rag.RetrieveResult{
		Title:   "办理入住流程",
		Content: "先打开入住小程序，填写订单和住客信息，完成实名认证后获取房间信息。如遇小程序打不开，请联系工作人员。",
	}
	if knowledgeEvidenceMismatchesTask(normal, normalWithFallback) {
		t.Fatal("a normal process document must survive when only its final sentence contains exception guidance")
	}

	entryStep := rag.RetrieveResult{
		Title:   "酒店入口路线",
		Content: "从昭潭路停车场入口右手边大楼进入，大厅左转乘电梯。",
	}
	if !knowledgeEvidenceMismatchesTask(normal, entryStep) {
		t.Fatal("a normal check-in request must not consume an unasked entrance route")
	}
	doorStep := rag.RetrieveResult{
		Title:   "怎么开门",
		Content: "完成入住登记后到店刷脸开门，不需要密码。",
	}
	if knowledgeEvidenceMismatchesTask(normal, doorStep) {
		t.Fatal("registration followed by face access belongs to the normal check-in procedure")
	}

	routeTask := runtimeTaskKnowledgeItem{Query: "酒店入口怎么走", SubIntent: "entrance_navigation"}
	if knowledgeEvidenceMismatchesTask(routeTask, entryStep) {
		t.Fatal("an explicit entrance question must retain the matching route")
	}
}

func TestKnowledgeEvidenceJudgeRejectsCrossTopicResourceSource(t *testing.T) {
	addressTask := runtimeTaskKnowledgeItem{Query: "外卖地址填哪里 门店地址", SubIntent: "address_for_delivery"}
	laundry := rag.RetrieveResult{Title: "洗衣房位置和图片", Content: "洗衣房位于十二楼。"}
	if !knowledgeEvidenceMismatchesTask(addressTask, laundry) {
		t.Fatal("address task must reject laundry evidence and its bound resources")
	}
	laundryWithAddress := rag.RetrieveResult{Title: "洗衣房地址和图片", Content: "洗衣房位于十二楼。"}
	if !knowledgeEvidenceMismatchesTask(addressTask, laundryWithAddress) {
		t.Fatal("a generic address word must not make a foreign facility record eligible for a store-address task")
	}
	laundryTask := runtimeTaskKnowledgeItem{Query: "洗衣房在哪里 洗衣", SubIntent: "laundry"}
	if knowledgeEvidenceMismatchesTask(laundryTask, laundryWithAddress) {
		t.Fatal("the same location record must remain eligible for its own facility task")
	}
}

func TestKnowledgeEvidenceJudgeRejectsUnboundActionMarker(t *testing.T) {
	item := runtimeTaskKnowledgeItem{Query: "随便问一个门店服务问题", SubIntent: "store_knowledge"}
	kept, stats := filterKnowledgeEvidenceForTask(RunInput{}, item, []rag.RetrieveResult{{
		KnowledgeBaseID: 1, SourceRecordID: "action-only", Content: "转接", Score: 0.9,
	}})
	if len(kept) != 0 || stats.droppedAction != 1 {
		t.Fatalf("unbound action marker must not become customer-visible evidence: kept=%+v stats=%+v", kept, stats)
	}
}

func TestKnowledgeEvidenceJudgeRejectsWeakUnrelatedCandidate(t *testing.T) {
	item := runtimeTaskKnowledgeItem{Query: "老客户可以享受优惠吗", SubIntent: "discount"}
	kept, stats := filterKnowledgeEvidenceForTask(RunInput{}, item, []rag.RetrieveResult{{
		Title: "其他服务", Content: "可以开水单。", Score: 0.58,
	}})
	if len(kept) != 0 || stats.droppedWeak != 1 {
		t.Fatalf("weak unrelated evidence must be rejected: kept=%+v stats=%+v", kept, stats)
	}

	relevant, _ := filterKnowledgeEvidenceForTask(RunInput{}, item, []rag.RetrieveResult{{
		Title: "会员优惠", Content: "老客户可按门店当前活动享受对应优惠。", Score: 0.58,
	}})
	if len(relevant) != 1 {
		t.Fatalf("topic-relevant evidence must remain available: %+v", relevant)
	}
}

func TestStoreIdentityQuestionNormalizationIsGeneric(t *testing.T) {
	req := RunInput{UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "这里是壹间公寓吗"}}
	intent := normalizeStoreIdentityQuestionIntent(callbacks.IntentTraceData{
		PrimaryIntent: "interaction", IntentConfidence: 0.7,
		IntentTasks: []callbacks.IntentTaskTraceData{{Intent: "interaction", SubIntent: "clarify", Text: req.UserMessage.Content}},
	}, req)
	if len(intent.IntentTasks) != 1 || intent.IntentTasks[0].Intent != "hotel_info" || intent.IntentTasks[0].SubIntent != "store_identity" {
		t.Fatalf("store identity question was not normalized: %#v", intent)
	}
	if explicitStoreIdentityQuestion("外卖地址填壹间公寓就行") {
		t.Fatal("a positive address instruction must not be reclassified as a store identity question")
	}
}

func TestAuthoritativeStoreFactDoesNotSkipDeliveryKnowledge(t *testing.T) {
	if !runtimeTaskUsesOnlyAuthoritativeStoreAddress("address_for_delivery") {
		t.Fatal("a pure delivery-address task may use the authoritative store address directly")
	}
	for _, subIntent := range []string{"order_food_delivery", "food_delivery", "takeaway"} {
		if runtimeTaskUsesOnlyAuthoritativeStoreAddress(subIntent) {
			t.Fatalf("%s must still retrieve delivery instructions before adding the store address", subIntent)
		}
	}
}
