package executor

import (
	"testing"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
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
