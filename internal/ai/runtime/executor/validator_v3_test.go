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

func TestValidatorV3RejectsInternalMessageBoundaryMarker(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk("有速溶咖啡。<<NEXT_MESSAGE>>请去洗衣房取。"),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "前台可以借充电线。"},
	}}
	result := NewReplyValidatorV3().Validate(input)
	if result.Status != "repairable_protocol_error" || !validationV3HasCode(result, "internal_control_marker") {
		t.Fatalf("internal marker must be repaired before commit: %+v", result)
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

func TestValidatorV3ExactDuplicateAcrossDifferentIntentsIsRetryable(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Plan.Tasks[1].Intent = "interaction"
	input.Plan.Tasks[1].SubIntent = "social"
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk("ＡＢＣ，咖啡。"),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "abc咖啡"},
	}}
	result := NewReplyValidatorV3().Validate(input)
	if result.Status != "retryable_content_error" || !validationV3HasCode(result, "retryable_content_error") {
		t.Fatalf("exact duplicate must be rejected regardless of intent: %+v", result)
	}
}

func TestValidatorV3HighSimilarityOnlyWarns(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk("咖啡机在二楼，24小时开放。"),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "咖啡机在二楼，24小时开放的。"},
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

func TestValidatorV3AddressTaskDetectedByRequiredFactRef(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Plan.Tasks[0].SubIntent = "general_service"
	input.Plan.Tasks[0].RequiredFactRefs = []string{"S1"}
	input.Facts = contracts.RuntimeContextSnapshotV2{Facts: []contracts.RuntimeContextFactV2{
		{Ref: "S1", Key: "store.address", Value: "科技园区88号"},
	}}
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk("酒店地址是幸福路66号。"),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "前台可以借充电线。"},
	}}
	result := NewReplyValidatorV3().Validate(input)
	if result.Status != "rejected" || !validationV3HasCode(result, "protected_fact_source_violation") {
		t.Fatalf("required store.address fact must activate address boundary: %+v", result)
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

func TestValidatorV3StoreNameMustMatchAuthoritativeStore(t *testing.T) {
	parts := []contracts.ReplyPartV3{{
		GroupKey: "grp_a", TaskKeys: []string{"t1"}, Content: "对，填壹间公寓就行。",
	}}
	issues := validateStoreNamesAgainstAuthoritative(parts, []string{"丽斯未来酒店南七店"})
	if len(issues) == 0 || issues[0].Code != "protected_fact_source_violation" {
		t.Fatalf("customer-injected store name was not rejected: %+v", issues)
	}
	parts[0].Content = "丽斯未来酒店。"
	if issues := validateStoreNamesAgainstAuthoritative(parts, []string{"丽斯未来酒店南七店"}); len(issues) != 0 {
		t.Fatalf("authoritative store name was rejected: %+v", issues)
	}
	parts[0].Content = "我们酒店有速溶咖啡。"
	if issues := validateStoreNamesAgainstAuthoritative(parts, []string{"丽斯未来酒店南七店"}); len(issues) != 0 {
		t.Fatalf("generic store category phrase must not be treated as a store name: %+v", issues)
	}
}

func TestValidatorV3RecommendationPlaceNameDoesNotBecomeStoreIdentity(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Plan.Tasks[0].ClaimType = "recommendation"
	input.Plan.Tasks[0].Knowledge = contracts.ReplyPlanKnowledgeV4{Policy: "required", Status: "has_context"}
	input.Evidence = contracts.EvidenceBundleV2{SchemaVersion: contracts.EvidenceBundleV2SchemaVersion, Items: []contracts.EvidenceItemV2{{
		Ref: "K1", TaskKeys: []string{"t1"}, Title: "附近推荐", Content: "云顶公寓",
		ClaimType: "recommendation", TopicMatch: "exact", Answerability: "supporting", AllowedUses: []string{"answer_text", "recommend"},
	}}}
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk("附近可以去云顶公寓。"),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "前台可以借充电线。"},
	}}
	result := NewReplyValidatorV3().Validate(input)
	if validationV3HasCode(result, "protected_fact_source_violation") {
		t.Fatalf("evidence-backed recommendation place must not be treated as current store identity: %+v", result)
	}
}

func TestValidatorV3StoreIdentityScopeUsesOnlyAuthoritativeStoreNames(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Plan.Tasks[0].SubIntent = "general_service"
	input.Plan.Tasks[0].RequiredFactRefs = []string{"S1"}
	input.Facts = contracts.RuntimeContextSnapshotV2{Facts: []contracts.RuntimeContextFactV2{
		{Ref: "S1", Key: "store.name", Value: "丽斯未来酒店南七店"},
	}}
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk("外卖填壹间公寓就行。"),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "前台可以借充电线。"},
	}}
	authoritative := []string{"丽斯未来酒店南七店", "丽斯未来酒店"}
	issues := validateV3StoreIdentityAssertionsAgainstAuthoritative(input, authoritative)
	if len(issues) == 0 || issues[0].Code != "protected_fact_source_violation" {
		t.Fatalf("customer-supplied store identity must be rejected: %+v", issues)
	}

	input.Output.Parts[0].Content = "外卖填丽斯未来酒店南七店就行。"
	if issues := validateV3StoreIdentityAssertionsAgainstAuthoritative(input, authoritative); len(issues) != 0 {
		t.Fatalf("authoritative store identity must pass: %+v", issues)
	}
}

func TestValidatorV3StoreIdentityScopeDoesNotBlockRecommendationPlaces(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Plan.Tasks[0].Intent = "recommendation"
	input.Plan.Tasks[0].SubIntent = "nearby_places"
	input.Plan.Tasks[0].ClaimType = "recommendation"
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk("附近可以去云顶公寓逛逛。"),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "前台可以借充电线。"},
	}}
	if issues := validateV3StoreIdentityAssertionsAgainstAuthoritative(input, []string{"丽斯未来酒店南七店"}); len(issues) != 0 {
		t.Fatalf("recommendation place must not be treated as the current store identity: %+v", issues)
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

func TestValidatorV3RecommendationEntitiesMustComeFromEvidence(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Plan.Tasks[0].ClaimType = "recommendation"
	input.Plan.Tasks[0].Knowledge = contracts.ReplyPlanKnowledgeV4{Policy: "required", Status: "has_context"}
	input.Evidence = contracts.EvidenceBundleV2{SchemaVersion: contracts.EvidenceBundleV2SchemaVersion, Items: []contracts.EvidenceItemV2{
		{Ref: "K1", TaskKeys: []string{"t1"}, Title: "附近游玩推荐", Content: "南七天地商业中心、骆岗中央公园、罍街",
			ClaimType: "recommendation", TopicMatch: "exact", Answerability: "supporting", AllowedUses: []string{"answer_text", "recommend"}},
	}}
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk("附近可以去南七天地商业中心、骆岗中央公园、罍街。"),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "前台可以借充电线。"},
	}}
	result := NewReplyValidatorV3().Validate(input)
	if result.Status != "passed" {
		t.Fatalf("grounded recommendations must pass: %+v", result)
	}

	input.Output.Parts[0].Content = "附近可以去南七天地商业中心、淮河路步行街。"
	result = NewReplyValidatorV3().Validate(input)
	if result.Status == "passed" || result.NormalizedParts[0].Content != "附近可以去南七天地商业中心、淮河路步行街。" {
		t.Fatalf("unsupported recommendation must be rejected without replacing model prose: %+v", result)
	}
}

func TestValidatorV3RecommendationNoContextMayOnlyStateUncertainty(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Plan.Tasks[0].ClaimType = "recommendation"
	input.Plan.Tasks[0].Knowledge = contracts.ReplyPlanKnowledgeV4{Policy: "required", Status: "no_context"}
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk("当前资料没有写明附近推荐，你想找吃的还是玩的？"),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "前台可以借充电线。"},
	}}
	result := NewReplyValidatorV3().Validate(input)
	if result.Status != "warning" || result.NormalizedParts[0].Content != "当前资料没有写明可推荐的具体地点，我暂时不能可靠推荐。" {
		t.Fatalf("no-context recommendation must use server fallback: %+v", result)
	}
	input.Output.Parts[0].Content = "附近可以去一个知识库没有写过的新景点。"
	result = NewReplyValidatorV3().Validate(input)
	if result.Status != "warning" || result.NormalizedParts[0].Content != "当前资料没有写明可推荐的具体地点，我暂时不能可靠推荐。" {
		t.Fatalf("invented recommendation must be replaced before commit: %+v", result)
	}
	input.Output.Parts[0].Content = "当前资料没有写明附近推荐；不过可以去一个知识库没有写过的新景点。"
	result = NewReplyValidatorV3().Validate(input)
	if result.Status != "warning" || result.NormalizedParts[0].Content != "当前资料没有写明可推荐的具体地点，我暂时不能可靠推荐。" {
		t.Fatalf("uncertainty prefix must not preserve an unsupported recommendation: %+v", result)
	}
}

func TestValidatorV3ReplacesNoContextHallucinationDeterministically(t *testing.T) {
	input, mk := validatorV3Fixture()
	input.Plan.Tasks[0].Knowledge = contracts.ReplyPlanKnowledgeV4{Policy: "required", Status: "no_context"}
	input.Plan.Tasks[1].Knowledge = contracts.ReplyPlanKnowledgeV4{Policy: "required", Status: "has_context"}
	input.Output = contracts.ReplyOutputV3{SchemaVersion: contracts.ReplyOutputV3SchemaVersion, Parts: []contracts.ReplyPartV3{
		mk("天花板绿光应该是烟雾报警器，属于正常现象。"),
		{GroupKey: "grp_b", TaskKeys: []string{"t2"}, Content: "前台可以借充电线。"},
	}}
	result := NewReplyValidatorV3().Validate(input)
	if result.Status != "warning" || len(result.NormalizedParts) != 2 {
		t.Fatalf("no-context fallback must remain committable: %+v", result)
	}
	if result.NormalizedParts[0].Content != "当前资料没有写明这项信息，我暂时不能确认。" {
		t.Fatalf("hallucinated no-context answer survived: %+v", result.NormalizedParts[0])
	}
	if !validationV3HasWarning(result, "server_fallback_knowledge_no_context") {
		t.Fatalf("server fallback was not audited: %+v", result.Warnings)
	}
}

func TestDeterministicGroundedKnowledgeContentMergesRequiredFactsAndKnowledgeEvidence(t *testing.T) {
	plan := contracts.ReplyPlanV4{
		Tasks: []contracts.ReplyPlanTaskV4{{
			TaskKey: "delivery", Sequence: 1, Intent: "hotel_info", SubIntent: "address_for_delivery",
			Knowledge:        contracts.ReplyPlanKnowledgeV4{Policy: "required", Status: "has_context"},
			RequiredFactRefs: []string{"S1"},
			EvidenceRefs:     []string{"K1", "S1"},
		}},
	}
	evidence := contracts.EvidenceBundleV2{Items: []contracts.EvidenceItemV2{
		{
			Ref: "S1", TaskKeys: []string{"delivery"}, Content: "包河区水阳江路392号职工之家12至15层",
			TopicMatch: "exact", Answerability: "supporting", AllowedUses: []string{"answer_text"},
		},
		{
			Ref: "K1", TaskKeys: []string{"delivery"}, Content: "外卖员不能上楼，请到一楼领取。",
			TopicMatch: "exact", Answerability: "supporting", AllowedUses: []string{"answer_text"},
		},
	}}

	content, ok := deterministicGroundedKnowledgeContent(plan, evidence, []string{"delivery"})
	if !ok {
		t.Fatal("expected grounded address and delivery policy")
	}
	if content != "酒店地址是：包河区水阳江路392号职工之家12至15层\n外卖员不能上楼，请到一楼领取。" {
		t.Fatalf("required fact replaced ordinary evidence: %q", content)
	}
}

func TestDeterministicGroundedKnowledgeContentCoversAllKnowledgeClaimClasses(t *testing.T) {
	cases := []struct {
		name      string
		claimType string
		content   string
	}{
		{name: "fact", claimType: "fact", content: "洗衣房位于1313房间对面。"},
		{name: "procedure", claimType: "procedure", content: "办理入住请先打开小程序并完成人脸登记。"},
		{name: "policy", claimType: "policy", content: "外卖员不能上楼，请到一楼领取。"},
		{name: "recommendation", claimType: "recommendation", content: "附近可以去骆岗中央公园。"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := contracts.ReplyPlanV4{Tasks: []contracts.ReplyPlanTaskV4{{
				TaskKey: "task", Sequence: 1, Intent: "hotel_info", SubIntent: tc.name,
				ClaimType: tc.claimType, EvidenceRefs: []string{"K1"},
				Knowledge: contracts.ReplyPlanKnowledgeV4{Policy: "required", Status: "has_context"},
			}}}
			evidence := contracts.EvidenceBundleV2{Items: []contracts.EvidenceItemV2{{
				Ref: "K1", TaskKeys: []string{"task"}, ClaimType: tc.claimType, Content: tc.content,
				TopicMatch: "exact", Answerability: "supporting", AllowedUses: []string{"answer_text"},
			}}}
			content, ok := deterministicGroundedKnowledgeContent(plan, evidence, []string{"task"})
			if !ok || content != tc.content {
				t.Fatalf("%s knowledge was not projected from evidence: ok=%t content=%q", tc.claimType, ok, content)
			}
		})
	}
}

func validationV3HasCode(result contracts.ValidationResultV3, code string) bool {
	for _, issue := range result.Errors {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func validationV3HasWarning(result contracts.ValidationResultV3, code string) bool {
	for _, issue := range result.Warnings {
		if issue.Code == code {
			return true
		}
	}
	return false
}
