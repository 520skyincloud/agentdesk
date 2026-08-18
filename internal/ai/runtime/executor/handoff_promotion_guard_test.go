package executor

import (
	"testing"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/pkg/enums"
)

func TestNormalCheckinNotPromotedByExceptionFAQ(t *testing.T) {
	hits := []rag.RetrieveResult{
		{KnowledgeBaseID: 1, SourceRecordID: "x1", Title: "把跟我一起入住的人信息删掉",
			Content: "问题：把跟我一起入住的人信息删掉 答案：转接", Score: 0.71},
		{KnowledgeBaseID: 1, SourceRecordID: "x2", Title: "酒店是否设有传统的前台服务？",
			Content: "问题：酒店是否设有传统的前台服务？ 答案：酒店采用非24小时前台的智能化运作模式，客人可通过智能化方式办理入住。", Score: 0.70},
	}
	items := []runtimeTaskKnowledgeItem{{
		TaskKey: "t-checkin", Intent: "hotel_info", SubIntent: "checkin_process", Query: "我要办理入住",
		Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
		Result: &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: []int64{1}, Hits: hits, ContextResults: hits},
	}}
	_, byTask, taskActions := buildRuntimeEvidenceBundle(RunInput{}, items, nil)
	if code := taskActions["t-checkin"]; code == "human_handoff" {
		t.Fatalf("normal checkin must not be promoted by exception FAQ mismatch, got %s (outcome=%+v)", code, byTask["t-checkin"])
	}
	if byTask["t-checkin"].Status != "has_context" {
		t.Fatalf("checkin must stay has_context, got %#v", byTask["t-checkin"])
	}
}

func TestKnowledgeTextNeverPromotesHandoffWithoutExplicitBinding(t *testing.T) {
	hits := []rag.RetrieveResult{
		{KnowledgeBaseID: 1, SourceRecordID: "x1", Title: "我有两间房，另一间房办不了入住",
			Content: "问题：我有两间房，另一间房办不了入住 答案：转接", Score: 0.79},
	}
	items := []runtimeTaskKnowledgeItem{{
		TaskKey: "t-exc", Intent: "hotel_info", SubIntent: "checkin_process", Query: "我有两间房，另一间房办不了入住",
		Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
		Result: &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: []int64{1}, Hits: hits, ContextResults: hits},
	}}
	_, _, taskActions := buildRuntimeEvidenceBundle(RunInput{}, items, nil)
	if code := taskActions["t-exc"]; code != "" {
		t.Fatalf("knowledge text must not control routing without an explicit binding, got %q", code)
	}
}
