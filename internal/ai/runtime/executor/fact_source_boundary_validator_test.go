package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func addressBoundaryInput(subIntent, content, authoritative string) ReplyValidationInput {
	evidenceItems := []contracts.EvidenceItemV1{}
	if authoritative != "" {
		evidenceItems = append(evidenceItems, contracts.EvidenceItemV1{
			Ref: "S1", SourceType: "store_fact", TaskKeys: []string{"t1"},
			Title: "当前门店地址（系统权威）", Content: authoritative, Score: 1,
			Answerability: "supporting", ResourceRefs: []string{},
		})
	}
	return ReplyValidationInput{
		Plan: contracts.ReplyPlanV2{
			TurnVersion:       1,
			Tasks:             []contracts.ReplyPlanTaskV2{{TaskKey: "t1", SubIntent: subIntent, OutputMode: "text"}},
			GlobalConstraints: contracts.ReplyPlanGlobalConstraints{MaxReplyParts: 3},
		},
		ActionLedger: contracts.ActionLedgerV1{TurnVersion: 1},
		Evidence:     contracts.EvidenceBundleV1{ScopeFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Items: evidenceItems},
		Output: contracts.ReplyOutputV2{
			Parts: []contracts.ReplyPartV2{{TaskKeys: []string{"t1"}, Content: content}},
		},
	}
}

func TestAddressMatchesAuthoritativeAllowsFormatVariants(t *testing.T) {
	auth := "合肥市包河区水阳江路392号职工之家12-15整层"
	cases := []string{
		"地址是包河区水阳江路392号职工之家12至15层。",
		"水阳江路 392 号职工之家 12-15 层。",
		auth,
	}
	for _, content := range cases {
		if !addressMatchesAuthoritative(content, auth) {
			t.Fatalf("expected format variant to match: %q", content)
		}
	}
}

func TestFactSourceBoundaryRejectsForeignAddress(t *testing.T) {
	// 生产故障复现：点外卖回复了客户 OCR 里的“壹间公寓高新社区”。
	input := addressBoundaryInput("order_food_delivery", "外卖得你自己下单，你填壹间公寓高新社区这个地址。", "合肥市包河区水阳江路392号职工之家12-15整层")
	result := NewReplyValidator().Validate(input)
	if result.Status != "repairable_protocol_error" {
		t.Fatalf("expected one repair opportunity for foreign address when authoritative fact exists, got %s", result.Status)
	}
	found := false
	for _, issue := range result.Errors {
		if issue.Code == "protected_fact_source_violation" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected protected_fact_source_violation, got %+v", result.Errors)
	}
}

func TestFactSourceBoundaryAllowsQuestionAndNegativeCorrection(t *testing.T) {
	for _, content := range []string{
		"您问的是壹间公寓吗？这里是合肥南七店。",
		"这里不是壹间公寓，这里是合肥南七店。",
	} {
		if placeNameMentionIsNonAssertive(content, "壹间公寓") != true {
			t.Fatalf("question or correction must not be treated as a store assertion: %q", content)
		}
	}
	if placeNameMentionIsNonAssertive("外卖地址填壹间公寓就行。", "壹间公寓") {
		t.Fatal("positive instruction must remain an asserted foreign place name")
	}
}

func TestFactSourceBoundaryPassesCorrectAddress(t *testing.T) {
	input := addressBoundaryInput("address", "地址是包河区水阳江路392号职工之家12至15层。", "合肥市包河区水阳江路392号职工之家12-15整层")
	if issues := validateReplyFactSourceBoundary(input); len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
}

func TestFactSourceBoundaryRejectsAssertionWhenUnconfigured(t *testing.T) {
	// 权威地址未配置时，地址任务不得下确定性地址断言（禁止从 OCR/历史猜）。
	input := addressBoundaryInput("address", "填壹间公寓高新社区就行。", "")
	if issues := validateReplyFactSourceBoundary(input); len(issues) == 0 {
		t.Fatal("expected violation when authoritative address unconfigured")
	}
}

func TestFactSourceBoundaryIgnoresNonAddressTasks(t *testing.T) {
	// 非地址任务（如早餐时间）不触发地址比对，避免误伤普通回复。
	input := addressBoundaryInput("breakfast", "早餐7点到10点，在二楼。", "合肥市包河区水阳江路392号职工之家12-15整层")
	if issues := validateReplyFactSourceBoundary(input); len(issues) != 0 {
		t.Fatalf("unexpected issues for non-address task: %+v", issues)
	}
}

func TestRuntimeActionInputsForbidImageForAddressTasks(t *testing.T) {
	// 文档 19.4：地址文字 + 任意图片 -> 不创建 Action（问地址不再发洗衣房图）。
	assetID := "asset-1"
	evidence := &contracts.EvidenceBundleV1{
		Resources: []contracts.EvidenceResourceV1{{
			Ref: "R1", Type: "image", AssetID: &assetID, Title: "洗衣房照片", TaskKeys: []string{"t1"},
		}},
	}
	plans := []callbacks.ReplyTaskPlanTraceData{
		{TaskKey: "t1", Intent: "hotel_info", SubIntent: "address", Text: "你们店地址在哪"},
	}
	inputs := runtimeActionInputs(plans, evidence, true)
	for _, in := range inputs {
		if in.ActionType == "send_knowledge_image" {
			t.Fatalf("address task must not auto-send images: %+v", in)
		}
	}
}

func TestRuntimeActionInputsAllowImageForFacilityTasks(t *testing.T) {
	// 问洗衣房（设施类任务）图片照常可发，不被地址门禁误伤。
	assetID := "asset-2"
	evidence := &contracts.EvidenceBundleV1{
		Resources: []contracts.EvidenceResourceV1{{
			Ref: "R1", Type: "image", AssetID: &assetID, Title: "洗衣房照片", TaskKeys: []string{"t1"},
		}},
	}
	plans := []callbacks.ReplyTaskPlanTraceData{
		{TaskKey: "t1", Intent: "hotel_info", SubIntent: "laundry", Text: "洗衣房在哪"},
	}
	inputs := runtimeActionInputs(plans, evidence, true)
	found := false
	for _, in := range inputs {
		if in.ActionType == "send_knowledge_image" {
			found = true
		}
	}
	if !found {
		t.Fatal("facility task should keep image eligibility")
	}
}

func TestKnowledgeEvidencePlaceNameIsAllowed(t *testing.T) {
	input := ReplyValidationInput{
		Req: RunInput{},
		Evidence: contracts.EvidenceBundleV1{Items: []contracts.EvidenceItemV1{{
			Title:   "酒店提供行李寄存服务吗？",
			Content: "问题：酒店提供行李寄存服务吗？ 答案：我们酒店提供行李寄存服务，如需寄存请自行前往1楼丽斯酒店前台处的寄存柜。",
		}}},
		Output: contracts.ReplyOutputV2{Parts: []contracts.ReplyPartV2{{
			TaskKeys: []string{"t1"}, Content: "行李可以免费寄存，去1楼丽斯酒店前台处的寄存柜放就行。",
		}}},
	}
	issues := validateStoreNameAssertions(input)
	for _, issue := range issues {
		if issue.Code == "protected_fact_source_violation" {
			t.Fatalf("KB-sourced place name must not be rejected: %#v", issue)
		}
	}
}
