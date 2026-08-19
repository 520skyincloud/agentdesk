package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func TestRuntimeAnswerBriefSeparatesRequiredAndSupportingFacts(t *testing.T) {
	plan := contracts.ReplyPlanV2{Tasks: []contracts.ReplyPlanTaskV2{{
		TaskKey: "checkin", SubIntent: "checkin_process", Objective: "给我办入住", OutputMode: "text",
		EvidenceRefs: []string{"K1", "K2", "S1"},
	}}}
	evidence := contracts.EvidenceBundleV1{Items: []contracts.EvidenceItemV1{
		{Ref: "K1", SourceType: "fastgpt", Content: "酒店入口在停车场旁边，进大厅乘电梯。", Answerability: "supporting"},
		{Ref: "K2", SourceType: "fastgpt", Content: "登记入住后刷脸开门。", Answerability: "supporting"},
		{Ref: "S1", SourceType: "store_fact", Content: "使用入住小程序登记，登记后刷脸开门。", Answerability: "supporting"},
	}}
	got := buildRuntimeAnswerBriefInstruction(plan, evidence)
	if !strings.Contains(got, "必答=K2,S1") || !strings.Contains(got, "补充=K1") {
		t.Fatalf("answer brief did not separate process facts: %q", got)
	}
	if !strings.Contains(got, "不能只摘第一条证据") {
		t.Fatalf("process coverage instruction missing: %q", got)
	}
}
