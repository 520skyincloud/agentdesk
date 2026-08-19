package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func TestSafeRuntimeDegradedUsesOnlyAuthoritativeScalarFacts(t *testing.T) {
	plan := contracts.ReplyPlanV2{
		GlobalConstraints: contracts.ReplyPlanGlobalConstraints{MaxReplyParts: 3},
		Tasks: []contracts.ReplyPlanTaskV2{
			{TaskKey: "address", SubIntent: "address_for_delivery", OutputMode: "text", EvidenceRefs: []string{"S1"}},
			{TaskKey: "identity", SubIntent: "store_identity", OutputMode: "text", EvidenceRefs: []string{"S2"}},
		},
	}
	evidence := contracts.EvidenceBundleV1{Items: []contracts.EvidenceItemV1{
		{Ref: "S1", SourceType: "store_fact", Title: authoritativeStoreAddressEvidenceTitle, Content: "合肥市包河区水阳江路392号", TaskKeys: []string{"address"}, Answerability: "supporting"},
		{Ref: "S2", SourceType: "store_fact", Title: authoritativeStoreNameEvidenceTitle, Content: "合肥南七店", TaskKeys: []string{"identity"}, Answerability: "supporting"},
	}}
	parts := buildSafeRuntimeDegradedParts(plan, evidence)
	if len(parts) != 2 || !strings.Contains(parts[0].Content, "水阳江路392号") || parts[1].Content != "这里是合肥南七店。" {
		t.Fatalf("authoritative safe degraded output mismatch: %#v", parts)
	}
}

func TestSafeRuntimeDegradedNeverUsesKnowledgeOrProcessTemplates(t *testing.T) {
	plan := contracts.ReplyPlanV2{
		GlobalConstraints: contracts.ReplyPlanGlobalConstraints{MaxReplyParts: 3},
		Tasks: []contracts.ReplyPlanTaskV2{{
			TaskKey: "checkin", SubIntent: "checkin_process", OutputMode: "text",
			Knowledge: contracts.ReplyPlanKnowledge{Policy: "required", Status: "has_context"}, EvidenceRefs: []string{"K1", "S1"},
		}},
	}
	evidence := contracts.EvidenceBundleV1{Items: []contracts.EvidenceItemV1{
		{Ref: "K1", SourceType: "fastgpt", Title: "入住流程", Content: "第一步登记，第二步刷脸开门。", TaskKeys: []string{"checkin"}, Answerability: "supporting"},
		{Ref: "S1", SourceType: "store_fact", Title: "当前门店入住方式（系统权威）", Content: "使用入住小程序登记后刷脸开门。", TaskKeys: []string{"checkin"}, Answerability: "supporting"},
	}}
	if parts := buildSafeRuntimeDegradedParts(plan, evidence); len(parts) != 0 {
		t.Fatalf("safe degraded mode must not become a process answer engine: %#v", parts)
	}
}

func TestSafeRuntimeDegradedDoesNotInventAcknowledgementOrNoHitReply(t *testing.T) {
	plan := contracts.ReplyPlanV2{
		GlobalConstraints: contracts.ReplyPlanGlobalConstraints{MaxReplyParts: 3},
		Tasks: []contracts.ReplyPlanTaskV2{
			{TaskKey: "social", Intent: "interaction", SubIntent: "smalltalk", OutputMode: "text"},
			{TaskKey: "unknown", Intent: "hotel_info", SubIntent: "facility", OutputMode: "clarification", Knowledge: contracts.ReplyPlanKnowledge{Policy: "required", Status: "no_context"}},
		},
	}
	if parts := buildSafeRuntimeDegradedParts(plan, contracts.EvidenceBundleV1{}); len(parts) != 0 {
		t.Fatalf("safe degraded mode must not emit generic fallback text: %#v", parts)
	}
}

func TestReplyRepairInstructionDoesNotReplayRawModelOutput(t *testing.T) {
	instruction := buildRuntimeReplyOutputRepairInstruction(&replyOutputProtocolError{
		Reason: "protected_fact_source_violation", RawResponse: "不应再次进入 Prompt 的完整原文",
	})
	if strings.Contains(instruction, "完整原文") || strings.Contains(instruction, "第一次输出") {
		t.Fatalf("repair instruction replayed raw model output: %q", instruction)
	}
}
