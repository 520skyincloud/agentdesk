package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func validatorV3Fixture() (ReplyValidationInputV3, func(content string) contracts.ReplyPartV3) {
	plan := contracts.ReplyPlanV4{
		SchemaVersion: contracts.ReplyPlanV4SchemaVersion, TurnVersion: 1, ShouldGenerate: true,
		Tasks: []contracts.ReplyPlanTaskV4{
			{TaskKey: "t1", Sequence: 1, Intent: "hotel_info", SubIntent: "facility", AnswerGroupKey: "grp_a", OutputMode: "text",
				EvidenceRefs: []string{"K1"}},
			{TaskKey: "t2", Sequence: 2, Intent: "hotel_info", SubIntent: "facility", AnswerGroupKey: "grp_b", OutputMode: "text",
				EvidenceRefs: []string{"K2"}},
		},
		ReplyGroups: []contracts.ReplyPlanGroupV4{
			{GroupKey: "grp_a", TaskKeys: []string{"t1"}, Sequence: 1, OutputMode: "text", MaxParts: 1, Required: true},
			{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Sequence: 2, OutputMode: "text", MaxParts: 1, Required: true},
		},
	}
	input := ReplyValidationInputV3{Plan: plan}
	mk := func(content string) contracts.ReplyPartV3 {
		return contracts.ReplyPartV3{GroupKey: "grp_a", TaskKeys: []string{"t1"}, Content: content}
	}
	return input, mk
}

func TestValidatorV3PassesWithServerResolvedRefs(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion,
		Parts: []contracts.ReplyPartV3{mk("咖啡机在二楼，24小时开放。"), {GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "前台可以借充电线。"}}}
	result := NewReplyValidatorV3().Validate(input)
	if result.Status != "passed" {
		t.Fatalf("expected passed, got %s errors=%v", result.Status, result.Errors)
	}
	if len(result.NormalizedParts) != 2 {
		t.Fatalf("normalized parts: %+v", result.NormalizedParts)
	}
	if len(result.NormalizedParts[0].GroundingEvidenceRefs) == 0 {
		t.Fatal("grounding refs must be resolved server-side")
	}
}

func TestValidatorV3MissingRequiredGroupIsRepairable(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{mk("咖啡机在二楼。")}}
	result := NewReplyValidatorV3().Validate(input)
	if result.Status != "repairable_protocol_error" || result.RecoveryStage != "generate" {
		t.Fatalf("missing required group must be repairable: %+v", result)
	}
}

func TestValidatorV3DuplicateContentAcrossGroupsIsRetryable(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk(" 咖啡机在二楼，24小时开放。 "),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "咖啡机在二楼，24小时开放。"},
	}}
	result := NewReplyValidatorV3().Validate(input)
	if result.Status != "retryable_content_error" {
		t.Fatalf("exact duplicate across same-intent groups must be retryable: %+v", result)
	}
}

func TestValidatorV3HighSimilarityOnlyWarns(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk("咖啡机在二楼，24小时开放。"),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "咖啡机在二楼，24小时开放"},
	}}
	result := NewReplyValidatorV3().Validate(input)
	if result.Status != "warning" && result.Status != "passed" {
		t.Fatalf("high similarity must only warn: %+v", result)
	}
	found := false
	for _, warning := range result.Warnings {
		if warning.Code == "high_similarity_content" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected high_similarity_content warning: %+v", result.Warnings)
	}
}

func TestValidatorV3AddressFabricationRejected(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Plan.Tasks[0].SubIntent = "address_for_delivery"
	input.Facts = contracts.RuntimeContextSnapshotV2{Facts: []contracts.RuntimeContextFactV2{
		{Ref: "S1", Key: "store.address", Value: "科技园区88号"},
	}}
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk("酒店地址是幸福路66号壹间公寓3楼。"),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "前台可以借充电线。"},
	}}
	result := NewReplyValidatorV3().Validate(input)
	if result.Status != "rejected" {
		t.Fatalf("address fabrication must be rejected: %+v", result)
	}
}

func TestValidatorV3UncommittedActionClaimRejected(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk("已为您办好入住。"),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "前台可以借充电线。"},
	}}
	result := NewReplyValidatorV3().Validate(input)
	if result.Checks.ActionClaims != "failed" {
		t.Fatalf("uncommitted action claim must fail: %+v", result)
	}
}
