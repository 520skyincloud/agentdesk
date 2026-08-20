package executor

import (
	"testing"
	"time"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
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

func TestKnowledgeEvidenceJudgeKeepsSupplyStoredByLaundry(t *testing.T) {
	item := runtimeTaskKnowledgeItem{Query: "有熨斗吗，在哪里取", SubIntent: "supplies_self_help"}
	result := rag.RetrieveResult{
		Title:   "洗衣房客用品取用",
		Content: "没有传统熨斗时可使用挂烫机，位于12楼洗衣房旁的百宝箱，可自行取用。",
	}
	if knowledgeEvidenceMismatchesTask(item, result) {
		t.Fatal("a matched supply entity must not be rejected because its location is the laundry room")
	}
	if !knowledgeEvidenceHasPositiveRelevance(item, result, models.KnowledgeEvidenceMetadata{}, false) {
		t.Fatal("a matched supply alias must be positive evidence")
	}
}

func TestKnowledgeEvidenceJudgeRejectsDifferentSpecificSupply(t *testing.T) {
	item := runtimeTaskKnowledgeItem{Query: "有熨斗吗", SubIntent: "supplies_self_help"}
	result := rag.RetrieveResult{Title: "牙具取用", Content: "牙刷在一楼客用品柜自取。"}
	if !knowledgeEvidenceMismatchesTask(item, result) {
		t.Fatal("a broad supplies topic must not admit a different explicitly named item")
	}
}

func TestKnowledgeEvidenceJudgeRecordsPerCandidateDecision(t *testing.T) {
	good := rag.RetrieveResult{KnowledgeBaseID: 1, SourceRecordID: "iron", Title: "用品取用", Content: "挂烫机在12楼百宝箱自取。", Score: 0.8}
	bad := rag.RetrieveResult{KnowledgeBaseID: 1, SourceRecordID: "breakfast", Title: "早餐时间", Content: "早餐七点开始。", Score: 0.9}
	item := runtimeTaskKnowledgeItem{
		Query: "熨斗在哪里", SubIntent: "supplies_self_help", Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
		Result: &retrievers.KnowledgeRetrieveResult{
			Hits: []rag.RetrieveResult{good, bad}, ContextResults: []rag.RetrieveResult{good, bad},
			TraceItems: []callbacks.RetrieverTraceItem{
				{KnowledgeBaseID: 1, SourceRecordID: "iron", UsedInContext: true, ContextRankNo: 1},
				{KnowledgeBaseID: 1, SourceRecordID: "breakfast", UsedInContext: true, ContextRankNo: 2},
			},
		},
	}
	judgeRuntimeTaskKnowledgeEvidence(RunInput{}, &item)
	if len(item.Result.TraceItems) != 2 || item.Result.TraceItems[0].JudgeDecision != "accepted" || item.Result.TraceItems[0].JudgeReason == "" {
		t.Fatalf("accepted candidate must carry a judge reason: %+v", item.Result.TraceItems)
	}
	if item.Result.TraceItems[1].JudgeDecision != "rejected" || item.Result.TraceItems[1].JudgeReason == "" || item.Result.TraceItems[1].UsedInContext {
		t.Fatalf("rejected candidate must carry its drop reason: %+v", item.Result.TraceItems)
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

func TestKnowledgeEvidenceJudgeRequiresPositiveRelevanceEvenAtHighScore(t *testing.T) {
	item := runtimeTaskKnowledgeItem{Query: "老客户可以享受优惠吗", SubIntent: "discount"}
	kept, stats := filterKnowledgeEvidenceForTask(RunInput{}, item, []rag.RetrieveResult{{
		Title: "其他服务", Content: "可以开水单。", Score: 0.95,
	}})
	if len(kept) != 0 || stats.droppedWeak != 1 {
		t.Fatalf("retrieval score alone must not admit unrelated evidence: kept=%+v stats=%+v", kept, stats)
	}

	relevant, _ := filterKnowledgeEvidenceForTask(RunInput{}, item, []rag.RetrieveResult{{
		Title: "会员优惠", Content: "老客户可按门店当前活动享受对应优惠。", Score: 0.58,
	}})
	if len(relevant) != 1 {
		t.Fatalf("topic-relevant evidence must remain available: %+v", relevant)
	}
}

func TestKnowledgeEvidenceJudgeDoesNotTreatRelationshipContextAsDiscountEvidence(t *testing.T) {
	item := runtimeTaskKnowledgeItem{Query: "我是老客户", SubIntent: "discount"}
	kept, stats := filterKnowledgeEvidenceForTask(RunInput{}, item, []rag.RetrieveResult{{
		Title: "客服介绍", Content: "我是24小时客服，有什么可以帮您？", Score: 0.96,
	}})
	if len(kept) != 0 || stats.droppedWeak+stats.droppedMismatch != 1 {
		t.Fatalf("relationship context must not admit unrelated customer-service evidence: kept=%+v stats=%+v", kept, stats)
	}
}

func TestKnowledgeEvidenceJudgeEnforcesStoreScopeAndMetadataAcrossKnowledgeBases(t *testing.T) {
	db := setupRuntimeIntentConfigTestDB(t)
	if err := db.AutoMigrate(&models.KnowledgeBase{}, &models.KnowledgeEvidenceMetadata{}); err != nil {
		t.Fatalf("migrate knowledge evidence fixture: %v", err)
	}
	now := time.Now()
	store := &models.Store{TenantID: 1, StoreCode: "evidence-store", Name: "证据门店", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
	foreignStore := &models.Store{TenantID: 1, StoreCode: "foreign-evidence-store", Name: "其他门店", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := db.Create(foreignStore).Error; err != nil {
		t.Fatalf("create foreign store: %v", err)
	}
	createKnowledge := func(storeID int64, name string) *models.KnowledgeBase {
		item := &models.KnowledgeBase{TenantID: 1, StoreID: storeID, DatasetID: name, Name: name,
			KnowledgeType: string(enums.KnowledgeBaseTypeFastGPTCloud), Status: enums.StatusOk,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create knowledge base %s: %v", name, err)
		}
		return item
	}
	allowed := createKnowledge(store.ID, "allowed")
	blockedByMetadata := createKnowledge(store.ID, "blocked-metadata")
	foreign := createKnowledge(foreignStore.ID, "foreign")
	if err := db.Create(&models.KnowledgeEvidenceMetadata{
		TenantID: 1, StoreID: store.ID, KnowledgeBaseID: blockedByMetadata.ID, SourceRecordID: "blocked-record",
		SourceClass: "customer_content", FactScope: "store", ClaimType: "fact", TrustLevel: "supported",
		Freshness: "current", ReviewStatus: "approved", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create blocked metadata: %v", err)
	}

	req := RunInput{Conversation: models.Conversation{TenantID: 1, StoreID: store.ID}}
	item := runtimeTaskKnowledgeItem{Query: "早餐几点开始", SubIntent: "breakfast"}
	kept, stats := filterKnowledgeEvidenceForTask(req, item, []rag.RetrieveResult{
		{KnowledgeBaseID: allowed.ID, SourceRecordID: "allowed-record", Title: "早餐时间", Content: "早餐七点开始。", Score: 0.8},
		{KnowledgeBaseID: blockedByMetadata.ID, SourceRecordID: "blocked-record", Title: "早餐时间", Content: "早餐八点开始。", Score: 0.9},
		{KnowledgeBaseID: foreign.ID, SourceRecordID: "foreign-record", Title: "早餐时间", Content: "早餐九点开始。", Score: 0.99},
	})
	if len(kept) != 1 || kept[0].KnowledgeBaseID != allowed.ID {
		t.Fatalf("only in-scope answerable evidence may remain: %+v", kept)
	}
	if stats.droppedPolicy != 1 || stats.droppedScope != 1 {
		t.Fatalf("unexpected evidence rejection stats: %+v", stats)
	}
}

func TestStoreIdentityUsesStructuredModelTask(t *testing.T) {
	req := RunInput{UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "这里是哪家酒店"}}
	intent := normalizeModelIntentTrace(callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info", SubIntent: "store_identity", IntentConfidence: 0.7, ShouldReply: true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Sequence: 1, Intent: "hotel_info", SubIntent: "store_identity", Text: req.UserMessage.Content,
			RequestMode: "answer", Confidence: 0.7,
		}},
	}, req, adapter.HistoryBuildResult{}, []models.ReplyIntentConfig{{Code: "hotel_info", Status: enums.StatusOk}})
	if len(intent.IntentTasks) != 1 || intent.IntentTasks[0].Intent != "hotel_info" || intent.IntentTasks[0].SubIntent != "store_identity" || !intent.NeedsKnowledge {
		t.Fatalf("structured store identity task was not preserved: %#v", intent)
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
