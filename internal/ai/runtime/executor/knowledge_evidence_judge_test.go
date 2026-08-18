package executor

import (
	"testing"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/ai/runtime/knowledgepolicy"
	"agent-desk/internal/pkg/enums"
)

func TestFilterKnowledgeMetaEvidenceDropsDerivedMetaQuestions(t *testing.T) {
	results := []rag.RetrieveResult{
		{KnowledgeBaseID: 99, SourceRecordID: "meta-1", Title: "用户可能通过哪些不同的方式向助手询问附近的游玩推荐？", Content: "问题：…", Score: 0.9},
		{KnowledgeBaseID: 99, SourceRecordID: "good-1", Title: "附近有哪些餐饮推荐？", Content: "罍街小吃街、小丁小吃", Score: 0.88},
		{KnowledgeBaseID: 99, SourceRecordID: "meta-2", Title: "这个表格是否包含任何地理位置信息？", Content: "不包含", Score: 0.8},
	}
	kept, dropped := filterKnowledgeMetaEvidence(RunInput{}, results)
	if dropped != 2 || len(kept) != 1 {
		t.Fatalf("kept=%d dropped=%d, want kept=1 dropped=2", len(kept), dropped)
	}
	if kept[0].SourceRecordID != "good-1" {
		t.Fatalf("expected only real answer kept, got %s", kept[0].SourceRecordID)
	}
}

func TestBuildRuntimeEvidenceBundleDowngradesAllMetaToNoContext(t *testing.T) {
	// 生产故障 3.3：吃喝玩乐检索命中的全是派生元问题，不得成为推荐证据。
	metaHit := rag.RetrieveResult{KnowledgeBaseID: 99, SourceRecordID: "m", Title: "美食推荐分为哪两个类别？", Content: "问题：美食推荐分为哪两个类别？", Score: 0.9}
	items := []runtimeTaskKnowledgeItem{
		{
			TaskKey: "t-food", Intent: "hotel_info", SubIntent: "surrounding_facilities", Query: "附近吃的",
			Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
			Result: &retrievers.KnowledgeRetrieveResult{
				KnowledgeBaseIDs: []int64{99}, Hits: []rag.RetrieveResult{metaHit},
				ContextResults: []rag.RetrieveResult{metaHit}, ContextText: metaHit.Content,
			},
		},
	}
	_, byTask, _ := buildRuntimeEvidenceBundle(RunInput{}, items, nil)
	if byTask["t-food"].Status != "no_context" {
		t.Fatalf("expected all-meta task downgrade to no_context, got %+v", byTask["t-food"])
	}
	if byTask["t-food"].ReasonCode != "knowledge_meta_content" {
		t.Fatalf("expected reasonCode=knowledge_meta_content, got %s", byTask["t-food"].ReasonCode)
	}
}

func TestRealAnswersStillSupportingAfterJudge(t *testing.T) {
	// 正常知识（非元问题）不受 Judge 影响，仍为 supporting。
	hit := rag.RetrieveResult{KnowledgeBaseID: 99, SourceRecordID: "real", Title: "附近有哪些餐饮推荐？", Content: "答案：罍街小吃街、小丁小吃。", Score: 0.9}
	items := []runtimeTaskKnowledgeItem{
		{
			TaskKey: "t-food", Intent: "hotel_info", SubIntent: "surrounding_facilities", Query: "附近吃的",
			Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
			Result: &retrievers.KnowledgeRetrieveResult{
				KnowledgeBaseIDs: []int64{99}, Hits: []rag.RetrieveResult{hit},
				ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content,
			},
		},
	}
	bundle, byTask, _ := buildRuntimeEvidenceBundle(RunInput{}, items, nil)
	if byTask["t-food"].Status != "has_context" {
		t.Fatalf("real answer must stay has_context, got %+v", byTask["t-food"])
	}
	if len(bundle.Items) == 0 {
		t.Fatal("real answer must enter evidence items")
	}
	_ = contracts.EvidenceBundleV1SchemaVersion
}

func TestHighVectorScoreAloneCannotBecomeExactEvidence(t *testing.T) {
	hit := rag.RetrieveResult{KnowledgeBaseID: 99, SourceRecordID: "unrelated", Title: "早餐供应时间", Content: "早餐七点开始。", Score: 0.99}
	items := []runtimeTaskKnowledgeItem{{
		TaskKey: "t-play", Intent: "hotel_info", SubIntent: "nearby_play", Query: "附近有什么好玩的",
		Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
		Result: &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: []int64{99}, Hits: []rag.RetrieveResult{hit}, ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content},
	}}
	_, byTask, _ := buildRuntimeEvidenceBundle(RunInput{}, items, nil)
	if byTask["t-play"].Status != "no_context" {
		t.Fatalf("vector score alone must not authorize an answer: %+v", byTask["t-play"])
	}
}

func TestColloquialProcedureQuestionUsesMatchingFAQEvidence(t *testing.T) {
	hit := rag.RetrieveResult{
		KnowledgeBaseID: 99, SourceRecordID: "invoice", Title: "如何开具发票？",
		Content: "问题：如何开具发票？ 答案：退房后在酒店小程序申请电子发票。", Score: 0.7847,
	}
	items := []runtimeTaskKnowledgeItem{{
		TaskKey: "t-invoice", Intent: "hotel_info", SubIntent: "invoice_issuance", Query: "发票咋开",
		Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
		Result: &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: []int64{99}, Hits: []rag.RetrieveResult{hit}, ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content},
	}}
	artifacts := buildRuntimeEvidenceArtifacts(RunInput{}, items, nil)
	if artifacts.ByTask["t-invoice"].Status != "has_context" {
		t.Fatalf("matching colloquial FAQ must be answerable: %+v", artifacts.ByTask["t-invoice"])
	}
	if len(artifacts.Quality.Items) != 1 || artifacts.Quality.Items[0].TopicMatch != "exact" {
		t.Fatalf("matching FAQ must become exact evidence: %+v", artifacts.Quality.Items)
	}
}

func TestPromptContextResidueCannotBecomeHotelEvidence(t *testing.T) {
	hit := rag.RetrieveResult{
		KnowledgeBaseID: 99, SourceRecordID: "polluted", Title: "问题。",
		Content: "问题：问题。 答案：答案。 Okay, I will replicate the Context block first because the instruction explicitly requires it.", Score: 0.91,
	}
	items := []runtimeTaskKnowledgeItem{{
		TaskKey: "t-razor", Intent: "service_request", SubIntent: "amenity_delivery_razor", Query: "有没有刮胡刀",
		Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
		Result: &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: []int64{99}, Hits: []rag.RetrieveResult{hit}, ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content},
	}}
	artifacts := buildRuntimeEvidenceArtifacts(RunInput{}, items, nil)
	if artifacts.ByTask["t-razor"].Status != "no_context" || artifacts.ByTask["t-razor"].ReasonCode != "knowledge_meta_content" {
		t.Fatalf("prompt residue must be blocked: %+v", artifacts.ByTask["t-razor"])
	}
}

func TestShortColloquialQuestionsUseSameTopicFAQEvidence(t *testing.T) {
	cases := []struct {
		name, query, title, content string
	}{
		{
			name:    "checkin",
			query:   "入住都不会？",
			title:   "在哪办入住？",
			content: "问题：在哪办入住？ 答案：我们是非24小时前台的智能化运作模式。",
		},
		{
			name:    "food",
			query:   "干嘛吃的？",
			title:   "边上有什么吃的？",
			content: "问题：边上有什么吃的？ 答案：酒店周边有丰富的餐饮选择。",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hit := rag.RetrieveResult{
				KnowledgeBaseID: 99, SourceRecordID: tc.name, Title: tc.title,
				Content: tc.content, Score: 0.75,
			}
			items := []runtimeTaskKnowledgeItem{{
				TaskKey: "t-" + tc.name, Intent: "hotel_info", SubIntent: "store_knowledge",
				Query: tc.query, EvidenceQuery: tc.query,
				Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
				Result: &retrievers.KnowledgeRetrieveResult{
					KnowledgeBaseIDs: []int64{99}, Hits: []rag.RetrieveResult{hit},
					ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content,
				},
			}}
			artifacts := buildRuntimeEvidenceArtifacts(RunInput{}, items, nil)
			if artifacts.ByTask["t-"+tc.name].Status != "has_context" {
				t.Fatalf("short colloquial question lost same-topic FAQ evidence: %+v", artifacts.ByTask["t-"+tc.name])
			}
			if len(artifacts.Quality.Items) != 1 || artifacts.Quality.Items[0].TopicMatch != "exact" {
				t.Fatalf("same-topic FAQ was not exact evidence: %+v", artifacts.Quality.Items)
			}
		})
	}
}

func TestDirectQASharedGenericTimeWordDoesNotCrossTopics(t *testing.T) {
	hit := rag.RetrieveResult{
		KnowledgeBaseID: 99, SourceRecordID: "checkout", Title: "退房时间是什么时候？",
		Content: "问题：退房时间是什么时候？ 答案：中午十二点。", Score: 0.92,
	}
	items := []runtimeTaskKnowledgeItem{{
		TaskKey: "t-breakfast", Intent: "hotel_info", SubIntent: "breakfast_time", Query: "早餐时间",
		Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
		Result: &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: []int64{99}, Hits: []rag.RetrieveResult{hit}, ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content},
	}}
	artifacts := buildRuntimeEvidenceArtifacts(RunInput{}, items, nil)
	if artifacts.ByTask["t-breakfast"].Status != "no_context" {
		t.Fatalf("generic time wording must not cross business topics: %+v", artifacts.ByTask["t-breakfast"])
	}
}

func TestDirectQAPolicyCanAnswerProcedureWordingOnSameTopic(t *testing.T) {
	hit := rag.RetrieveResult{
		KnowledgeBaseID: 99, SourceRecordID: "delivery", Title: "酒店可以点外卖吗？",
		Content: "问题：酒店可以点外卖吗？ 答案：可以自行点外卖，外卖员不能送到房门，需要前往一楼领取。", Score: 0.6991,
	}
	items := []runtimeTaskKnowledgeItem{{
		TaskKey: "t-delivery", Intent: "hotel_info", SubIntent: "delivery_order", Query: "外卖怎么点",
		Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
		Result: &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: []int64{99}, Hits: []rag.RetrieveResult{hit}, ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content},
	}}
	artifacts := buildRuntimeEvidenceArtifacts(RunInput{}, items, nil)
	if artifacts.ByTask["t-delivery"].Status != "has_context" {
		t.Fatalf("same-topic policy FAQ must answer procedure wording: %+v", artifacts.ByTask["t-delivery"])
	}
	if len(artifacts.Quality.Items) != 1 || artifacts.Quality.Items[0].TopicMatch != "exact" {
		t.Fatalf("same-topic FAQ must become exact evidence: %+v", artifacts.Quality.Items)
	}
}

func TestNormalProcedureFAQWithConditionalContactAdviceRemainsUsable(t *testing.T) {
	// 生产链路里 FastGPT 命中会被包装成“问题：/答案：”形态（见
	// buildFastGPTRetrieveResult），无侧车元数据时按该标记识别为 imported_faq。
	hit := rag.RetrieveResult{
		KnowledgeBaseID: 99, SourceRecordID: "checkin-normal", Title: "如何办理入住？",
		Content: "问题：如何办理入住？ 答案：到店后按订单提示办理入住；如无法办理，请联系客服。", Score: 0.88,
	}
	result := knowledgepolicy.Judge(knowledgepolicy.EvidenceJudgeInput{
		TenantID: 1, StoreID: 2,
		Task:      knowledgepolicy.Task{TaskKey: "t-checkin", Intent: "hotel_info", SubIntent: "checkin_process", EvidenceQuery: "办理入住"},
		Candidate: hit,
	})
	if result.Answerability != "supporting" || containsString(result.BlockedReasons, "exception_evidence_for_normal_question") {
		t.Fatalf("normal procedure FAQ was incorrectly treated as exception evidence: %+v", result)
	}
}

func TestExceptionDecisionUsesSourceBoundQuestionNotParentRetrievalHint(t *testing.T) {
	hit := rag.RetrieveResult{
		KnowledgeBaseID: 99, SourceRecordID: "checkin-failure", Title: "入住失败怎么办？",
		Content: "如果手机无法办理入住，请联系前台。", Score: 0.9,
	}
	result := knowledgepolicy.Judge(knowledgepolicy.EvidenceJudgeInput{
		TenantID: 1, StoreID: 2,
		Task: knowledgepolicy.Task{
			TaskKey: "t-checkin", Intent: "hotel_info", SubIntent: "checkin_process",
			EvidenceQuery: "办理入住", Query: "上一条问题是入住失败，现在办理入住流程是什么",
		},
		Candidate: hit,
	})
	if !containsString(result.BlockedReasons, "exception_evidence_for_normal_question") {
		t.Fatalf("exception FAQ was allowed by a contaminated parent query: %+v", result)
	}
}

func TestDirectQASharedGenericHotelWordsDoNotCrossTopics(t *testing.T) {
	hit := rag.RetrieveResult{
		KnowledgeBaseID: 99, SourceRecordID: "desk", Title: "房间有办公桌吗？",
		Content: "问题：房间有办公桌吗？ 答案：部分房型配备办公桌。", Score: 0.91,
	}
	items := []runtimeTaskKnowledgeItem{{
		TaskKey: "t-address", Intent: "hotel_info", SubIntent: "store_address", Query: "酒店房间地址在哪里",
		Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
		Result: &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: []int64{99}, Hits: []rag.RetrieveResult{hit}, ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content},
	}}
	artifacts := buildRuntimeEvidenceArtifacts(RunInput{}, items, nil)
	if artifacts.ByTask["t-address"].Status != "no_context" {
		t.Fatalf("generic hotel words must not authorize cross-topic evidence: %+v", artifacts.ByTask["t-address"])
	}
}
