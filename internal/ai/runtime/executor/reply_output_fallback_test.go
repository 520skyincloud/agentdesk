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

func TestControlledRuntimeFallbackCombinesCheckinEvidenceByStep(t *testing.T) {
	task := contracts.ReplyPlanTaskV2{
		TaskKey: "checkin", SubIntent: "checkin_process", Objective: "办理入住流程", OutputMode: "text",
		Knowledge:    contracts.ReplyPlanKnowledge{Policy: "required", Status: "has_context"},
		EvidenceRefs: []string{"K1", "K2", "S1"},
	}
	evidence := contracts.EvidenceBundleV1{Items: []contracts.EvidenceItemV1{
		{Ref: "K1", SourceType: "fastgpt", Content: "答案：酒店入口在昭潭路停车场入口右手边大楼，进入大厅后左转乘电梯上楼。"},
		{Ref: "K2", SourceType: "fastgpt", Content: "问题：怎么开门？ 答案：登记入住后直接刷脸开门，不需要密码。"},
		{Ref: "S1", SourceType: "store_fact", Content: "当前门店为无人值守智能化酒店，没有传统常驻前台和房卡；客户通过当前门店已配置的入住小程序完成入住登记，登记完成后到店刷脸开门。"},
	}}
	got := controlledRuntimeTaskFallbackText(task, evidence)
	for _, expected := range []string{"入住小程序", "刷脸开门"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("process fallback missed %q: %q", expected, got)
		}
	}
	if !strings.Contains(got, "我们这边是无人值守自助入住") || !strings.Contains(got, "不需要密码") {
		t.Fatalf("check-in fallback is not a natural complete customer reply: %q", got)
	}
	if strings.Contains(got, "答案：") || strings.Contains(got, "昭潭路") || strings.Contains(got, "电梯") || strings.Count(got, "登记入住后直接刷脸开门") > 0 {
		t.Fatalf("process fallback leaked FAQ labels, unrelated route, or repeated access evidence: %q", got)
	}
}

func TestControlledRuntimeFallbackIncludesRouteWhenCustomerAsks(t *testing.T) {
	task := contracts.ReplyPlanTaskV2{
		TaskKey: "route", SubIntent: "entrance_navigation", Objective: "酒店入口怎么走", OutputMode: "text",
		Knowledge: contracts.ReplyPlanKnowledge{Policy: "required", Status: "has_context"}, EvidenceRefs: []string{"K1"},
	}
	evidence := contracts.EvidenceBundleV1{Items: []contracts.EvidenceItemV1{{
		Ref: "K1", SourceType: "fastgpt", Content: "答案：入口在昭潭路停车场右侧大楼，进入大厅后乘电梯上楼。",
	}}}
	got := controlledRuntimeTaskFallbackText(task, evidence)
	if !strings.Contains(got, "昭潭路") || !strings.Contains(got, "电梯") {
		t.Fatalf("explicit route question lost route evidence: %q", got)
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
