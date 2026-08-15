package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

// 契约 12.1/13.2 回放：模型漏回显 evidenceRefs（生产 missing_task_evidence
// 大量失败的直接原因）必须由服务端 deterministic_autofix 派生，不得 rejected。
func TestValidatorAutofillsMissingEvidenceRefsFromPlan(t *testing.T) {
	plan := contracts.ReplyPlanV2{
		SchemaVersion: contracts.ReplyPlanV2SchemaVersion, TurnVersion: 1,
		Tasks: []contracts.ReplyPlanTaskV2{{
			TaskKey: "t1", Intent: "hotel_info", Objective: "answer", OutputMode: "text",
			Knowledge:    contracts.ReplyPlanKnowledge{Policy: "required", Status: "has_context"},
			EvidenceRefs: []string{"K1", "K2"}, ActionRefs: []string{},
		}},
	}
	evidence := contracts.EvidenceBundleV1{ScopeFingerprint: "scope", Items: []contracts.EvidenceItemV1{
		{Ref: "K1", TaskKeys: []string{"t1"}, SourceType: "knowledge", Content: "酒店提供速溶咖啡"},
		{Ref: "K2", TaskKeys: []string{"t1"}, SourceType: "knowledge", Content: "酒店提供一次性剃须刀"},
	}}
	// 模型只输出 taskKeys + content，漏写 evidenceRefs。
	output := contracts.ReplyOutputV2{Parts: []contracts.ReplyPartV2{{
		TaskKeys: []string{"t1"}, Content: "有咖啡和一次性剃须刀，可以到洗衣房自取。", EvidenceRefs: nil, ActionRefs: nil,
	}}}
	plan.GlobalConstraints = contracts.ReplyPlanGlobalConstraints{MaxReplyParts: 3, MaxQuestionsPerPart: 4}
	validator := NewReplyValidator()
	result := validator.Validate(ReplyValidationInput{Output: output, Plan: plan, Evidence: evidence, ActionLedger: contracts.ActionLedgerV1{TurnVersion: 1}, Gates: DefaultReplyValidationGates()})
	if result.Status != "passed" {
		t.Fatalf("autofixed output must pass, got %s errors=%+v", result.Status, result.Errors)
	}
	if len(result.NormalizedParts) != 1 {
		t.Fatalf("normalized parts: %+v", result.NormalizedParts)
	}
	refs := result.NormalizedParts[0].EvidenceRefs
	if !autofixTestContains("K1", refs) || !autofixTestContains("K2", refs) {
		t.Fatalf("server must derive grounding evidence refs: %v", refs)
	}
}

// 模型回显的未知 EvidenceRef 仍然必须拒绝（防止越权引用）。
func TestValidatorStillRejectsUnknownEvidenceRef(t *testing.T) {
	plan := contracts.ReplyPlanV2{
		SchemaVersion: contracts.ReplyPlanV2SchemaVersion,
		Tasks:         []contracts.ReplyPlanTaskV2{{TaskKey: "t1", Intent: "hotel_info", OutputMode: "text", Knowledge: contracts.ReplyPlanKnowledge{Policy: "optional"}}},
	}
	evidence := contracts.EvidenceBundleV1{Items: []contracts.EvidenceItemV1{
		{Ref: "K1", TaskKeys: []string{"t-other"}},
	}}
	output := contracts.ReplyOutputV2{Parts: []contracts.ReplyPartV2{{
		TaskKeys: []string{"t1"}, Content: "好的。", EvidenceRefs: []string{"K9"},
	}}}
	result := NewReplyValidator().Validate(ReplyValidationInput{Output: output, Plan: plan, Evidence: evidence, ActionLedger: contracts.ActionLedgerV1{TurnVersion: 1}, Gates: DefaultReplyValidationGates()})
	if result.Status != "rejected" {
		t.Fatalf("unknown evidence ref must stay rejected: %+v", result)
	}
}

func autofixTestContains(value string, items []string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
