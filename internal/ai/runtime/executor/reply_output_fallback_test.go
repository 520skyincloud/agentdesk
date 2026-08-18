package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func TestControlledRuntimeFallbackUsesAuthoritativeStoreFacts(t *testing.T) {
	plan := contracts.ReplyPlanV2{
		GlobalConstraints: contracts.ReplyPlanGlobalConstraints{MaxReplyParts: 3},
		Tasks: []contracts.ReplyPlanTaskV2{
			{TaskKey: "address", SubIntent: "address_for_delivery", Objective: "外卖地址填哪里", OutputMode: "text", EvidenceRefs: []string{"S1"}},
			{TaskKey: "identity", SubIntent: "store_identity", Objective: "这里是什么酒店", OutputMode: "text", EvidenceRefs: []string{"S2"}},
		},
	}
	evidence := contracts.EvidenceBundleV1{Items: []contracts.EvidenceItemV1{
		{Ref: "S1", SourceType: "store_fact", Content: "合肥市包河区水阳江路392号", TaskKeys: []string{"address"}},
		{Ref: "S2", SourceType: "store_fact", Content: "合肥南七店", TaskKeys: []string{"identity"}},
	}}
	parts := buildControlledRuntimeFallbackParts(plan, evidence)
	if len(parts) != 2 || !strings.Contains(parts[0].Content, "水阳江路392号") || parts[1].Content != "这里是合肥南七店。" {
		t.Fatalf("authoritative fallback mismatch: %#v", parts)
	}
}

func TestControlledRuntimeFallbackKeepsProcessCoverage(t *testing.T) {
	task := contracts.ReplyPlanTaskV2{
		TaskKey: "checkin", SubIntent: "checkin_process", Objective: "办理入住流程", OutputMode: "text",
		Knowledge: contracts.ReplyPlanKnowledge{Policy: "required", Status: "has_context"}, EvidenceRefs: []string{"K1"},
	}
	evidence := contracts.EvidenceBundleV1{Items: []contracts.EvidenceItemV1{{
		Ref: "K1", SourceType: "fastgpt", Content: "第一步填写订单信息。第二步完成住客实名认证。第三步核对入住日期。第四步获取房间信息。第五步按提示开门。",
	}}}
	got := controlledRuntimeTaskFallbackText(task, evidence)
	if !strings.Contains(got, "第一步") || !strings.Contains(got, "第五步") {
		t.Fatalf("process fallback must keep the complete short procedure, got %q", got)
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
