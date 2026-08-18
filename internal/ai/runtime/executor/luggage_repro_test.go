package executor

import (
	"fmt"
	"testing"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/pkg/enums"
)

func TestReproLuggageNoHit(t *testing.T) {
	hits := []rag.RetrieveResult{
		{KnowledgeBaseID: 1, SourceRecordID: "r1", Title: "酒店提供行李寄存服务吗？", Content: "问题：酒店提供行李寄存服务吗？ 答案：我们酒店提供行李寄存服务，住客可以在入住前或退房后免费寄存行李。如需寄存请自行前往1楼丽斯酒店前台处的寄存柜，根据提示完成。", Score: 0.79},
		{KnowledgeBaseID: 1, SourceRecordID: "r2", Title: "洗衣房在哪里？", Content: "问题：洗衣房在哪里？ 答案：洗衣房位于1313房间对面，提供洗衣机、烘干机、挂烫机等。", Score: 0.63},
		{KnowledgeBaseID: 1, SourceRecordID: "r3", Title: "酒店提供行李搬运服务吗？", Content: "问题：酒店提供行李搬运服务吗？ 答案：很抱歉，酒店暂不提供行李搬运服务。", Score: 0.61},
	}
	items := []runtimeTaskKnowledgeItem{{
		TaskKey: "t-luggage", Intent: "hotel_info", SubIntent: "", Query: "行李放哪里",
		Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
		Result: &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: []int64{1}, Hits: hits, ContextResults: hits},
	}}
	_, byTask, _ := buildRuntimeEvidenceBundle(RunInput{}, items, nil)
	out := byTask["t-luggage"]
	fmt.Printf("OUTCOME status=%s reason=%s refs=%v\n", out.Status, out.ReasonCode, out.SupportingRefs)
	if out.Status != "has_context" {
		t.Fatalf("luggage hit was dropped: %+v", out)
	}
}

func TestRuntimeKnowledgeStatusHitsSufficient(t *testing.T) {
	result := &retrievers.KnowledgeRetrieveResult{
		Hits: []rag.RetrieveResult{{Title: "酒店提供行李寄存服务吗？", Content: "问题：… 答案：我们酒店提供行李寄存服务…", Score: 0.79}},
	}
	if got := runtimeKnowledgeStatus(result, nil); got != enums.AIReplyTurnTaskKnowledgeStatusHit {
		t.Fatalf("hits present must count as hit even without ContextText, got %s", got)
	}
}
