package executor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
)

func TestMultiReplyInstructionPreservesSubjectScopeAndCertainty(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{TaskID: "task-1", Text: "能步行吗", OutputKind: "text", ReplyRequired: true}}}
	instruction := buildMultiReplyOutputInstruction(plan, true)
	for _, expected := range []string{"权益不同不等于价格不同", "需要或建议驾车不等于不能步行", "已确认包括某些房型不等于只有这些房型", "不得把未知属性写成肯定或否定结论"} {
		if !strings.Contains(instruction, expected) {
			t.Errorf("missing generation boundary %q", expected)
		}
	}
}

func TestLockedJudgeFactsSurviveGenerateRewritingAndFallback(t *testing.T) {
	for _, tt := range []struct {
		fact, generated string
	}{
		{"每个平台享受的平台权益不同，建议对比价格后选择。", "各个平台价格不一样。"},
		{"需要驾车前往小丁小吃。", "小丁小吃步行不方便。"},
		{"房间内有两瓶矿泉水，都是免费的。", "水免费。"},
	} {
		plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
			TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true,
			SelectedLayer: "store", SelectedCandidateIDs: []string{"T1C1"},
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "F1", Aspect: "other", Statement: tt.fact}},
		}}}
		raw, _ := json.Marshal(generatedReplyPartsEnvelope{ReplyParts: []generatedReplyPart{{TaskID: "task-1", Content: tt.generated, CoveredFactIDs: []string{"F1"}}}})
		got, err := normalizeGeneratedReplyPartsResult(string(raw), plan, true)
		if err != nil || got != tt.fact {
			t.Fatalf("Judge fact was rewritten: got=%q err=%v want=%q", got, err, tt.fact)
		}
		collector := callbacks.NewRuntimeTraceCollector()
		collector.Data.Pipeline.ReplyPlan = plan
		if fallback := deterministicGeneratedReplyFallback(collector); fallback != tt.fact {
			t.Fatalf("fallback changed locked facts: %q", fallback)
		}
	}
}

func TestExternalProxyDoesNotRepeatExplicitTaskFact(t *testing.T) {
	address := "收货地址是测试路18号。"
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "proxy", Intent: "service_request", SubIntent: "external_proxy_action", Objective: "action_request",
			OutputKind: "text", ReplyRequired: true, SelectedLayer: "store", SelectedCandidateIDs: []string{"T1C1"},
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "P1", Aspect: "location", Statement: address}}},
		{TaskID: "address", Intent: "hotel_info", SubIntent: "delivery_address", Objective: "location",
			OutputKind: "text", ReplyRequired: true, SelectedLayer: "store", SelectedCandidateIDs: []string{"T2C1"},
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "A1", Aspect: "location", Statement: address}}},
	}}
	raw := `{"replyParts":[{"taskId":"proxy","content":""},{"taskId":"address","content":"","coveredFactIds":["A1"]}]}`
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, true)
	if err != nil || strings.Count(got, address) != 1 || !strings.Contains(got, externalProxyActionCapabilityBoundaryReply) {
		t.Fatalf("proxy and explicit answer responsibilities overlap: %q, %v", got, err)
	}
}
func TestBuildMultiReplyOutputInstructionUsesTextTasksOnly(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "停车在哪里", Output: "knowledge_text_reply"},
		{Intent: "hotel_variable", Text: "定位发我", Output: "structured_resource_commit", ResourceAction: "provide_location"},
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	instruction := buildMultiReplyOutputInstruction(plan, false)
	if !strings.Contains(instruction, `"taskId":"task-1"`) || !strings.Contains(instruction, "停车在哪里") || !strings.Contains(instruction, "早餐几点") {
		t.Fatalf("unexpected instruction: %s", instruction)
	}
	if strings.Contains(instruction, "定位发我") {
		t.Fatalf("structured variable task must stay out of generated text contract: %s", instruction)
	}
}

func TestLockedEvidenceKeepsAllTasksInOrderAndRejectsUnsafeFacts(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{}
	envelope := generatedReplyPartsEnvelope{}
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("T%d", i)
		plan.TaskPlans = append(plan.TaskPlans, callbacks.ReplyTaskPlanTraceData{
			TaskID: id, Intent: "hotel_info", OutputKind: "text", ReplyRequired: true,
			SelectedLayer: "store", SelectedCandidateIDs: []string{id + "C1"},
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
				FactID: id + "F1", Statement: "答案" + id + "。", CriticalValues: []string{id},
			}},
		})
		envelope.ReplyParts = append(envelope.ReplyParts, generatedReplyPart{
			TaskID: id, CoveredFactIDs: []string{id + "F1"},
		})
	}
	raw, _ := json.Marshal(envelope)
	got, err := normalizeGeneratedReplyPartsResult(string(raw), plan, true)
	if err != nil || strings.Count(got, "<<NEXT_MESSAGE>>") > 2 {
		t.Fatalf("six tasks must compose into at most three messages: %q, %v", got, err)
	}
	last := -1
	for i := 1; i <= 6; i++ {
		answer := fmt.Sprintf("答案T%d。", i)
		if at := strings.Index(got, answer); at <= last || strings.Count(got, answer) != 1 {
			t.Fatalf("lost, repeated or reordered answer %q: %q", answer, got)
		} else {
			last = at
		}
	}
	plan.TaskPlans[0].SupportedFacts[0].Statement = `{"replyParts":[{"taskId":"T1","content":"internal"}]}`
	if _, err := normalizeGeneratedReplyPartsResult(string(raw), plan, true); err == nil {
		t.Fatal("locked facts must still pass the final protocol-leak safety check")
	}
}
func TestSinglePhysicalSourceWithMultipleModelTasksRequiresReplyParts(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{
			TaskID: "task-1", Intent: "hotel_info", SubIntent: "breakfast", OutputKind: "text", ReplyRequired: true,
			Text: "早餐几点", ResolvedText: "早餐几点", SourceRefs: []string{"U1"},
		},
		{
			TaskID: "task-2", Intent: "hotel_info", SubIntent: "parking", OutputKind: "text", ReplyRequired: true,
			Text: "停车免费吗", ResolvedText: "停车免费吗", SourceRefs: []string{"U1"},
		},
	}}

	if !replyPlanRequiresStructuredOutput(plan, false) {
		t.Fatal("two model-owned tasks from one physical message must require per-task replyParts")
	}
	instruction := buildMultiReplyOutputInstruction(plan, false)
	for _, expected := range []string{`"taskId":"task-1"`, `"taskId":"task-2"`, "早餐几点", "停车免费吗"} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("single-source multi-task contract missing %q: %s", expected, instruction)
		}
	}
}

func TestNormalizeGeneratedReplyPartsOrdersPartsByTask(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "停车在哪里", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-2","content":"早餐在一楼。"},{"taskId":"task-1","content":"停车从辅路入口进。"}]}`
	got := normalizeGeneratedReplyParts(raw, plan, false)
	want := "停车从辅路入口进。\n<<NEXT_MESSAGE>>\n早餐在一楼。"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNormalizeGeneratedReplyPartsUnwrapsSingleTaskProtocol(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"早餐供应到9:30。"}]}`
	if got := normalizeGeneratedReplyParts(raw, plan, false); got != "早餐供应到9:30。" {
		t.Fatalf("single-task protocol must be unwrapped, got %q", got)
	}
}

func TestNormalizeGeneratedReplyPartsCompactsRepeatedCustomerSentences(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true,
	}}}
	for name, test := range map[string]struct {
		content string
		want    string
	}{
		"contained": {
			content: "完成登记扫人脸就可以开门，无需房卡。无需房卡。",
			want:    "完成登记扫人脸就可以开门，无需房卡。无需房卡。",
		},
		"identical": {
			content: "无需房卡。无需房卡。",
			want:    "无需房卡。",
		},
		"mixed_capability": {
			content: "酒店没有传统前台，可以通过入住机或小程序办理入住。可以通过入住机或小程序办理入住。",
			want:    "酒店没有传统前台，可以通过入住机或小程序办理入住。可以通过入住机或小程序办理入住。",
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw := fmt.Sprintf(`{"replyParts":[{"taskId":"task-1","content":%q}]}`, test.content)
			got, err := normalizeGeneratedReplyPartsResult(raw, plan, true)
			if err != nil {
				t.Fatalf("compact repeated content: %v", err)
			}
			if got != test.want {
				t.Fatalf("only exactly repeated sentences may be removed: got %q want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeGeneratedReplyPartsUnwrapsMarkdownAndJSONStrings(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "停车在哪里", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	payload := `{"replyParts":[{"taskId":"task-1","content":"停车从辅路入口进。"},{"taskId":"task-2","content":"早餐供应到9:30。"}]}`
	quoted, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal quoted reply protocol: %v", err)
	}
	wrapped, err := json.Marshal(string(quoted))
	if err != nil {
		t.Fatalf("marshal double-quoted reply protocol: %v", err)
	}

	want := "停车从辅路入口进。\n<<NEXT_MESSAGE>>\n早餐供应到9:30。"
	for name, raw := range map[string]string{
		"markdown":       "模型输出如下：\n```json\n" + payload + "\n```",
		"quoted":         string(quoted),
		"double_quoted":  string(wrapped),
		"common_wrapper": `{"result":` + string(quoted) + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := normalizeGeneratedReplyParts(raw, plan, false); got != want {
				t.Fatalf("expected %q, got %q", want, got)
			}
		})
	}
}

func TestNormalizeGeneratedReplyPartsRejectsUnstructuredMultiTaskReply(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "停车在哪里", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := "停车从辅路入口进，早餐在一楼。"
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
	if got != "" || !errors.Is(err, errGeneratedReplyProtocol) {
		t.Fatalf("multi-task output must preserve task-level validation, got=%q err=%v", got, err)
	}
}

func TestNormalizeGeneratedReplyPartsSuppressesMalformedProtocolWithoutDeferredHandoff(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"早餐供应到9:30。"}]`
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
	if got != "" {
		t.Fatalf("malformed internal protocol must not leak, got %q", got)
	}
	if !errors.Is(err, errGeneratedReplyProtocol) {
		t.Fatalf("malformed internal protocol must request a retry, got %v", err)
	}
}

func TestNormalizeGeneratedReplyPartsRejectsBareTaskProtocolShapes(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	for name, raw := range map[string]string{
		"bare_object": `{"taskId":"task-1","content":"早餐供应到9:30。"}`,
		"bare_array":  `[{"taskId":"task-1","content":"早餐供应到9:30。"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
			if got != "" || !errors.Is(err, errGeneratedReplyProtocol) {
				t.Fatalf("bare internal protocol shape must fail without leaking output, got=%q err=%v", got, err)
			}
		})
	}
}

func TestNormalizeGeneratedReplyPartsRejectsInvalidTaskIDs(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "停车在哪里", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	for name, raw := range map[string]string{
		"missing":   `{"replyParts":[{"content":"停车从辅路入口进。"},{"taskId":"task-2","content":"早餐供应到9:30。"}]}`,
		"unknown":   `{"replyParts":[{"taskId":"task-1","content":"停车从辅路入口进。"},{"taskId":"task-3","content":"早餐供应到9:30。"}]}`,
		"duplicate": `{"replyParts":[{"taskId":"task-1","content":"停车从辅路入口进。"},{"taskId":"task-1","content":"早餐供应到9:30。"}]}`,
		"extra":     `{"replyParts":[{"taskId":"task-1","content":"停车从辅路入口进。"},{"taskId":"task-2","content":"早餐供应到9:30。"},{"taskId":"task-3","content":"多余内容"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
			if got != "" || !errors.Is(err, errGeneratedReplyProtocol) {
				t.Fatalf("invalid task IDs must fail without leaking output, got=%q err=%v", got, err)
			}
		})
	}
}

func TestBuildMultiReplyOutputInstructionRequiresStructuredSingleTaskForDeferredHandoff(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
	}}
	instruction := buildMultiReplyOutputInstruction(plan, true)
	if !strings.Contains(instruction, `"taskId":"task-1"`) || !strings.Contains(instruction, "顺便问早餐几点") {
		t.Fatalf("expected a structured contract for the single active task, got %q", instruction)
	}
}

func TestNormalizeGeneratedReplyPartsKeepsActiveTaskForDeferredHandoff(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"酒店暂不提供早餐。"}]}`

	got := normalizeGeneratedReplyParts(raw, plan, true)

	if got != "酒店暂不提供早餐。" {
		t.Fatalf("expected only the answerable task content, got %q", got)
	}
}

func TestNormalizeGeneratedReplyPartsFailsClosedOnMalformedDeferredOutput(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"酒店暂不提供早餐。"}`
	if got := normalizeGeneratedReplyParts(raw, plan, true); got != "" {
		t.Fatalf("malformed deferred output must fail closed, got %q", got)
	}
}

func TestNormalizeGeneratedReplyPartsFailsClosedWhenActivePartIsMissing(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "停车免费吗", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"酒店暂不提供早餐。"}]}`
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, true)
	if got != "" {
		t.Fatalf("missing active task content must fail closed, got %q", got)
	}
	if !errors.Is(err, errGeneratedReplyProtocol) {
		t.Fatalf("missing active task content must request a retry, got %v", err)
	}
}

func TestBuildActiveGenerationUserMessageTextExcludesDeferredQuestion(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:  "service_request",
		NeedsKnowledge: true,
	}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
	}}
	current := "空调坏了，我住1302\n顺便问早餐几点"
	got := buildActiveGenerationUserMessageText(current, intent, plan, true)
	if got != "顺便问早餐几点" {
		t.Fatalf("expected only the active answerable question, got %q", got)
	}
}

func TestBuildActiveGenerationUserMessageTextDoesNotRestoreDeferredQuestionForResourceOnlyPlan(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:   "hotel_variable",
		NeedsKnowledge:  true,
		NeedsResource:   true,
		ResourceActions: []string{"provide_mini_program"},
	}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_variable", SubIntent: "mini_program", Output: "structured_resource_commit", ResourceAction: "provide_mini_program"},
	}}
	current := "空调坏了，入住小程序发我"
	got := buildActiveGenerationUserMessageText(current, intent, plan, true)
	if strings.Contains(got, "空调坏了") || strings.Contains(got, "入住小程序发我") {
		t.Fatalf("resource-only active plan must not restore deferred customer questions, got %q", got)
	}
	if !strings.Contains(got, "没有需要 Generate 输出的文本任务") {
		t.Fatalf("expected an explicit no-text-task placeholder, got %q", got)
	}
}

func TestBuildTextReplyTaskGroupsKeepsEveryAtomicTask(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{}
	for _, text := range []string{"一", "二", "三", "四"} {
		plan.TaskPlans = append(plan.TaskPlans, callbacks.ReplyTaskPlanTraceData{Intent: "hotel_info", Text: text, Output: "knowledge_text_reply"})
	}
	groups := buildTextReplyTaskGroups(plan)
	if len(groups) != 4 {
		t.Fatalf("expected every atomic task to remain independently verifiable, got %#v", groups)
	}
	for index, want := range []string{"一", "二", "三", "四"} {
		if groups[index].TaskID != "task-"+string(rune('1'+index)) || strings.Join(groups[index].Texts, "") != want {
			t.Fatalf("unexpected atomic task at %d: %#v", index, groups[index])
		}
	}
}

func TestNormalizeGeneratedReplyPartsBalancesAtomicTasksIntoThreeMessages(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{}
	parts := make([]string, 0, 8)
	for index := 1; index <= 8; index++ {
		plan.TaskPlans = append(plan.TaskPlans, callbacks.ReplyTaskPlanTraceData{Intent: "hotel_info", Text: "问题", Output: "knowledge_text_reply"})
		parts = append(parts, `{"taskId":"task-`+string(rune('0'+index))+`","content":"回答`+string(rune('0'+index))+`"}`)
	}
	raw := `{"replyParts":[` + strings.Join(parts, ",") + `]}`
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
	if err != nil {
		t.Fatalf("normalize eight task parts: %v", err)
	}
	messages := strings.Split(got, "\n<<NEXT_MESSAGE>>\n")
	if len(messages) != 3 || messages[0] != "回答1\n\n回答2\n\n回答3" || messages[1] != "回答4\n\n回答5\n\n回答6" || messages[2] != "回答7\n\n回答8" {
		t.Fatalf("expected balanced consecutive 3/3/2 composition, got %#v", messages)
	}
}

func TestValidateCoveredFactsRequiresKnownCompleteFactsAndCriticalValues(t *testing.T) {
	group := textReplyTaskGroup{TaskID: "task-1", Facts: []replyFactRequirement{
		{FactID: "F1", Statement: "房间内有两瓶矿泉水", CriticalValues: []string{"两瓶"}},
		{FactID: "F2", Statement: "矿泉水免费", CriticalValues: []string{"免费"}},
	}}
	valid := generatedReplyPart{TaskID: "task-1", Content: "房间内有两瓶矿泉水，都是免费的。", CoveredFactIDs: []string{"F2", "F1"}}
	if err := validateCoveredFacts(valid, group); err != nil {
		t.Fatalf("expected complete facts to pass: %v", err)
	}

	tests := map[string]generatedReplyPart{
		"missing_fact":     {TaskID: "task-1", Content: "房间内有两瓶矿泉水。", CoveredFactIDs: []string{"F1"}},
		"unknown_fact":     {TaskID: "task-1", Content: "有两瓶，免费。", CoveredFactIDs: []string{"F1", "F3"}},
		"duplicate_fact":   {TaskID: "task-1", Content: "有两瓶，免费。", CoveredFactIDs: []string{"F1", "F1", "F2"}},
		"missing_critical": {TaskID: "task-1", Content: "房间内有矿泉水，都是免费的。", CoveredFactIDs: []string{"F1", "F2"}},
	}
	for name, part := range tests {
		t.Run(name, func(t *testing.T) {
			if err := validateCoveredFacts(part, group); !errors.Is(err, errGeneratedReplyProtocol) {
				t.Fatalf("expected protocol failure, got %v", err)
			}
		})
	}
	polarity := generatedReplyPart{TaskID: "task-1", Content: "房间内有两瓶矿泉水，但矿泉水不免费。", CoveredFactIDs: []string{"F1", "F2"}}
	if err := validateCoveredFacts(polarity, group); err != nil {
		t.Fatalf("local protocol validation must not interpret fact polarity: %v", err)
	}
}

func TestValidateCoveredFactsAllowsLosslessCriticalValueFormatting(t *testing.T) {
	group := textReplyTaskGroup{TaskID: "task-1", Facts: []replyFactRequirement{
		{FactID: "F1", Statement: "发票会在申请后1至3个工作日上传。", CriticalValues: []string{"1至3个工作日"}},
	}}
	part := generatedReplyPart{
		TaskID:         "task-1",
		Content:        "发票会在申请后１～３个工作日上传。",
		CoveredFactIDs: []string{"F1"},
	}
	if err := validateCoveredFacts(part, group); err != nil {
		t.Fatalf("lossless full-width and numeric-range formatting must pass: %v", err)
	}
}

func TestValidateCoveredFactsDoesNotTreatParaphrasesAsCriticalValues(t *testing.T) {
	group := textReplyTaskGroup{TaskID: "task-1", Facts: []replyFactRequirement{
		{FactID: "F1", Statement: "完成登记后扫人脸开门。", CriticalValues: []string{"扫人脸"}},
	}}
	part := generatedReplyPart{
		TaskID:         "task-1",
		Content:        "完成登记后刷脸开门。",
		CoveredFactIDs: []string{"F1"},
	}
	if err := validateCoveredFacts(part, group); err != nil {
		t.Fatalf("equivalent face-recognition wording must pass: %v", err)
	}
	part.Content = "完成登记后使用房卡开门。"
	if err := validateCoveredFacts(part, group); !errors.Is(err, errGeneratedReplyProtocol) {
		t.Fatalf("the required face-recognition method must not be omitted, got %v", err)
	}
}

func TestValidateCoveredFactsIgnoresGenericWordingButKeepsLiteralValues(t *testing.T) {
	generic := textReplyTaskGroup{TaskID: "task-1", Facts: []replyFactRequirement{{
		FactID: "F1", Statement: "建议对比价格后选择合适的平台。", CriticalValues: []string{"建议", "对比", "选择"},
	}}}
	part := generatedReplyPart{TaskID: "task-1", Content: "各平台价格可能不同，请按实际价格决定。", CoveredFactIDs: []string{"F1"}}
	if err := validateCoveredFacts(part, generic); err != nil {
		t.Fatalf("generic wording must not cause a protocol retry: %v", err)
	}

	for name, fact := range map[string]replyFactRequirement{
		"phone":    {FactID: "F1", Statement: "门店管家电话是18256022128。", CriticalValues: []string{"18256022128"}},
		"quantity": {FactID: "F1", Statement: "房间内有两瓶矿泉水。", CriticalValues: []string{"两瓶"}},
		"amount":   {FactID: "F1", Statement: "延迟退房费用是30元。", CriticalValues: []string{"30元"}},
		"time":     {FactID: "F1", Statement: "早餐时间是7:00-9:30。", CriticalValues: []string{"7:00-9:30"}},
		"address":  {FactID: "F1", Statement: "外卖地址填写丽斯未来酒店合肥南七店。", CriticalValues: []string{"丽斯未来酒店合肥南七店"}},
	} {
		t.Run(name, func(t *testing.T) {
			group := textReplyTaskGroup{TaskID: "task-1", Facts: []replyFactRequirement{fact}}
			missing := generatedReplyPart{TaskID: "task-1", Content: "相关信息已经确认。", CoveredFactIDs: []string{"F1"}}
			if err := validateCoveredFacts(missing, group); !errors.Is(err, errGeneratedReplyProtocol) {
				t.Fatalf("missing literal value must fail, got %v", err)
			}
		})
	}
}

func TestValidateCoveredFactsKeepsBusinessActionTargets(t *testing.T) {
	group := textReplyTaskGroup{TaskID: "task-1", Facts: []replyFactRequirement{{
		FactID: "F1", Statement: "完成登记后可以开门。", CriticalValues: []string{"登记", "开门"},
	}}}
	missing := generatedReplyPart{TaskID: "task-1", Content: "好的。", CoveredFactIDs: []string{"F1"}}
	if err := validateCoveredFacts(missing, group); !errors.Is(err, errGeneratedReplyProtocol) {
		t.Fatalf("business action targets must not be sanitized away, got %v", err)
	}
	complete := generatedReplyPart{TaskID: "task-1", Content: "完成登记后可以开门。", CoveredFactIDs: []string{"F1"}}
	if err := validateCoveredFacts(complete, group); err != nil {
		t.Fatalf("complete business action must pass: %v", err)
	}
}

func TestValidateCoveredFactsNormalizesAddressComponentWording(t *testing.T) {
	group := textReplyTaskGroup{TaskID: "task-1", Facts: []replyFactRequirement{{
		FactID: "F1", Aspect: "location", Statement: "外卖地址需要填写酒店名称、对应楼层和房间号。", CriticalValues: []string{"酒店名", "楼层", "房号"},
	}}}
	complete := generatedReplyPart{
		TaskID: "task-1", Content: "外卖地址填写酒店名、所在楼层和房号。", CoveredFactIDs: []string{"F1"},
	}
	if err := validateCoveredFacts(complete, group); err != nil {
		t.Fatalf("natural address component aliases must pass: %v", err)
	}
	for name, content := range map[string]string{
		"missing_floor": "外卖地址填写酒店名和房号。",
		"missing_room":  "外卖地址填写酒店名和所在楼层。",
	} {
		t.Run(name, func(t *testing.T) {
			part := generatedReplyPart{TaskID: "task-1", Content: content, CoveredFactIDs: []string{"F1"}}
			if err := validateCoveredFacts(part, group); !errors.Is(err, errGeneratedReplyProtocol) {
				t.Fatalf("missing address component must fail, got %v", err)
			}
		})
	}
}

func TestNormalizeGeneratedReplyPartsKeepsDifferentScopeAndPolarity(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true,
	}}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"儿童早餐免费。早餐免费。并非所有房间都可以开门。房间都可以开门。"}]}`
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, true)
	if err != nil {
		t.Fatalf("normalize scoped facts: %v", err)
	}
	want := "儿童早餐免费。早餐免费。并非所有房间都可以开门。房间都可以开门。"
	if got != want {
		t.Fatalf("different scope or polarity must remain separate, got %q want %q", got, want)
	}
}

func TestGeneratedReplyAIConfigUsesConfiguredBudgetOnlyForMultipleTasks(t *testing.T) {
	config := models.AIConfig{MaxOutputTokens: 1024}
	singlePlan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
	}}
	multiPlan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
		{TaskID: "task-2", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
	}}

	if got := generatedReplyAIConfigForPlan(config, singlePlan).MaxOutputTokens; got != generatedReplySingleTaskMaxOutputTokens {
		t.Fatalf("single task must keep the normal %d-token budget, got %d", generatedReplySingleTaskMaxOutputTokens, got)
	}
	if got := generatedReplyAIConfigForPlan(config, multiPlan).MaxOutputTokens; got != 1024 {
		t.Fatalf("multiple tasks must retain the configured 1024-token budget, got %d", got)
	}
}

func TestStructuredReplyPromptRemovesPlainTextOutputConflicts(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{
		Intent: "hotel_info",
		Style:  "自然微信口吻，1-3句",
		TaskPlans: []callbacks.ReplyTaskPlanTraceData{
			{TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
			{TaskID: "task-2", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
		},
	}
	prompt := buildIntentStagePrompt(callbacks.IntentPromptTraceData{Instructions: []string{
		"回复像微信真人，通常 1-3 句。",
		"最终回复只输出给客人的话，不输出内部分析。",
	}}, plan)

	for _, forbidden := range []string{"1-3句", "1-3 句", "最终回复只输出给客人的话"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("structured prompt must remove conflicting instruction %q: %s", forbidden, prompt)
		}
	}
	for _, required := range []string{"replyParts.content", "replyParts 的 content", "普通问题用 1-2 句", "流程问题可用 2-3 个简短步骤"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("structured prompt must scope customer-facing text to %q: %s", required, prompt)
		}
	}
}

func TestNormalizeGeneratedReplyPartsUsesActiveTaskFacts(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{
			TaskID:        "T1",
			Intent:        "hotel_info",
			ResolvedText:  "房间内有几瓶矿泉水，是否免费？",
			OutputKind:    "text",
			ReplyRequired: true,
			Output:        "knowledge_text_reply",
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
				{FactID: "T1F1", Statement: "房间内有两瓶矿泉水。", CriticalValues: []string{"两瓶"}},
				{FactID: "T1F2", Statement: "矿泉水都是免费的。", CriticalValues: []string{"免费"}},
			},
		},
	}}
	instruction := buildMultiReplyOutputInstruction(plan, false)
	for _, want := range []string{"T1", "T1F1", "T1F2", "两瓶", "免费", "coveredFactIds"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("active fact contract is missing %q: %s", want, instruction)
		}
	}
	raw := `{"replyParts":[{"taskId":"T1","content":"房间内有两瓶矿泉水，都是免费的。","coveredFactIds":["T1F1","T1F2"]}]}`
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
	if err != nil || got != "房间内有两瓶矿泉水，都是免费的。" {
		t.Fatalf("expected active task facts to validate, got=%q err=%v", got, err)
	}
}

func TestNormalizeGeneratedReplyPartsAddsExternalProxyBoundaryBeforeSelectedSelfServiceFacts(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "service_request", SubIntent: "external_proxy_action", Objective: "action_request",
		ResolvedText: "帮我点个外卖", OutputKind: "text", ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
			FactID: "task-1F1", Aspect: "location",
			Statement:      "外卖地址填写丽斯未来酒店合肥南七店加楼层房间号。",
			CriticalValues: []string{"丽斯未来酒店合肥南七店", "房间号"},
		}},
	}}}
	want := externalProxyActionCapabilityBoundaryReply + "外卖地址填写丽斯未来酒店合肥南七店加楼层房间号。"
	for _, raw := range []string{
		`{"replyParts":[{"taskId":"task-1","content":"","coveredFactIds":[]}]}`,
		`{"replyParts":[{"taskId":"task-1","content":"可以帮您下单，稍后会送到房间。"}]}`,
	} {
		got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
		if err != nil || got != want {
			t.Fatalf("external proxy reply must use only the fixed boundary and Judge facts, raw=%s got=%q err=%v", raw, got, err)
		}
	}
}

func TestNormalizeGeneratedReplyPartsReplacesFactlessExternalProxyContentWithBoundary(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "service_request", SubIntent: "external_proxy_action", Objective: "action_request",
		ResolvedText: "帮我叫辆车", OutputKind: "text", ReplyRequired: true,
	}}}
	for _, raw := range []string{
		`{"replyParts":[{"taskId":"task-1","content":"已经帮您叫车了。"}]}`,
		`{"replyParts":[{"taskId":"task-1","content":""}]}`,
	} {
		got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
		if err != nil || got != externalProxyActionCapabilityBoundaryReply {
			t.Fatalf("factless external proxy output must be deterministic, raw=%s got=%q err=%v", raw, got, err)
		}
	}
}

func TestNormalizeGeneratedReplyPartsDoesNotAddExternalBoundaryToInternalService(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "service_request", SubIntent: "room_supplies", Objective: "action_request",
		ResolvedText: "送双拖鞋", OutputKind: "text", ReplyRequired: true,
	}}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"方便说下是哪个房间吗？"}]}`
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
	if err != nil || got != "方便说下是哪个房间吗？" {
		t.Fatalf("internal hotel service must keep its original reply, got=%q err=%v", got, err)
	}
}

func TestNormalizeGeneratedReplyPartsDoesNotApplyLocalAspectVeto(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID:        "task-1",
		Intent:        "hotel_info",
		ResolvedText:  "有外卖机器人吗",
		OutputKind:    "text",
		ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
			FactID: "task-1F1", Aspect: "existence", Statement: "酒店有外卖机器人。",
		}},
	}}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"酒店有外卖机器人，可以送到房间。","coveredFactIds":["task-1F1"]}]}`

	got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
	if err != nil || got != "酒店有外卖机器人，可以送到房间。" {
		t.Fatalf("local protocol validation must leave semantic scope decisions to Judge and Generate, got=%q err=%v", got, err)
	}
}

func TestValidateGeneratedReplyFactAspectBoundariesRejectsUnsupportedDimensions(t *testing.T) {
	facts := []replyFactRequirement{{
		FactID: "task-1F1", Aspect: "existence", Statement: "酒店有外卖机器人。",
	}}
	for name, content := range map[string]string{
		"method":              "酒店有外卖机器人，可以通过小程序操作。",
		"location":            "酒店有外卖机器人，机器人位于一楼。",
		"time":                "酒店有外卖机器人，服务时间是10:00。",
		"delivery_commitment": "酒店有外卖机器人，已经安排给您送过去。",
		"no_comma_delivery":   "酒店有外卖机器人可以送到房间。",
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateGeneratedReplyFactAspectBoundaries(content, facts); err == nil {
				t.Fatalf("unsupported %s expansion must be rejected: %q", name, content)
			}
		})
	}
}

func TestNormalizeGeneratedReplyPartsAllowsNaturalExistenceReply(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID:        "task-1",
		Intent:        "hotel_info",
		OutputKind:    "text",
		ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
			FactID: "task-1F1", Aspect: "existence", Statement: "酒店有外卖机器人。",
		}},
	}}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"有的，我们酒店有外卖机器人。","coveredFactIds":["task-1F1"]}]}`

	got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
	if err != nil || got != "有的，我们酒店有外卖机器人。" {
		t.Fatalf("natural existence-only reply should pass, got=%q err=%v", got, err)
	}
}

func TestNormalizeGeneratedReplyPartsAllowsDeliveryScopeWhenFactSupportsIt(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID:        "task-1",
		Intent:        "hotel_info",
		OutputKind:    "text",
		ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
			{FactID: "task-1F1", Aspect: "existence", Statement: "酒店有外卖机器人。"},
			{FactID: "task-1F2", Aspect: "scope", Statement: "外卖机器人可以送到房门口。", CriticalValues: []string{"房门口"}},
		},
	}}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"酒店有外卖机器人，可以送到房门口。","coveredFactIds":["task-1F1","task-1F2"]}]}`

	got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
	if err != nil || got != "酒店有外卖机器人，可以送到房门口。" {
		t.Fatalf("explicitly supported delivery scope should pass, got=%q err=%v", got, err)
	}
}

func TestValidateGeneratedReplyFactAspectBoundariesRejectsSameAspectExpansion(t *testing.T) {
	for name, test := range map[string]struct {
		content string
		facts   []replyFactRequirement
	}{
		"quantity": {
			content: "房间内有两瓶矿泉水，另外还有四瓶收费的。",
			facts: []replyFactRequirement{
				{FactID: "F1", Aspect: "quantity", Statement: "房间内有两瓶矿泉水。", CriticalValues: []string{"两瓶"}},
				{FactID: "F2", Aspect: "price", Statement: "房间内矿泉水免费。", CriticalValues: []string{"免费"}},
			},
		},
		"location": {
			content: "外卖地址填写丽斯未来酒店合肥南七店加楼层房间号，也可以写壹间公寓。",
			facts: []replyFactRequirement{{
				FactID: "F1", Aspect: "location", Statement: "外卖地址填写丽斯未来酒店合肥南七店加楼层房间号。", CriticalValues: []string{"丽斯未来酒店合肥南七店", "房间号"},
			}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateGeneratedReplyFactAspectBoundaries(test.content, test.facts); err == nil {
				t.Fatalf("same-aspect unsupported expansion must be rejected: %q", test.content)
			}
		})
	}
}

func TestValidateGeneratedReplyFactAspectBoundariesRejectsUnsupportedExistenceCapabilityAndPolicy(t *testing.T) {
	facts := []replyFactRequirement{{
		FactID: "F1", Aspect: "existence", Statement: "酒店提供早餐。",
	}}
	for name, content := range map[string]string{
		"existence":  "酒店提供早餐，还配备健身房。",
		"capability": "酒店提供早餐，也支持代客泊车。",
		"policy":     "酒店提供早餐，儿童必须单独购买。",
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateGeneratedReplyFactAspectBoundaries(content, facts); err == nil || !strings.Contains(err.Error(), "unsupported "+name+" claim") {
				t.Fatalf("unsupported %s expansion must be rejected: content=%q err=%v", name, content, err)
			}
		})
	}
}

func TestValidateGeneratedReplyFactAspectBoundariesAllowsGroundedCapabilityAndPolicyParaphrase(t *testing.T) {
	facts := []replyFactRequirement{
		{FactID: "F1", Aspect: "scope", Statement: "酒店允许携带宠物。"},
		{FactID: "F2", Aspect: "condition", Statement: "办理入住需要身份证。"},
	}
	if err := validateGeneratedReplyFactAspectBoundaries("可以携带宠物，入住必须带身份证。", facts); err != nil {
		t.Fatalf("grounded capability and policy paraphrases should pass, got %v", err)
	}
	if err := validateGeneratedReplyFactAspectBoundaries("宠物可入住，入住要带身份证。", facts); err != nil {
		t.Fatalf("common grounded capability and policy word forms should pass, got %v", err)
	}
}

func TestValidateGeneratedReplyFactAspectBoundariesRejectsFactsHiddenBehindUncertainty(t *testing.T) {
	facts := []replyFactRequirement{{
		FactID: "F1", Aspect: "other", Statement: ungroundedKnowledgeSafeReply, CriticalValues: []string{"暂时没法准确回答"},
	}}
	content := "这个我暂时没法准确回答，酒店有健身房但其他细节无法确认。"
	if err := validateGeneratedReplyFactAspectBoundaries(content, facts); err == nil || !strings.Contains(err.Error(), "unsupported existence claim") {
		t.Fatalf("uncertainty wording must not hide an invented hotel fact, got %v", err)
	}
}

func TestValidateGeneratedReplyFactAspectBoundariesRecognizesCommonClaimWordForms(t *testing.T) {
	facts := []replyFactRequirement{{FactID: "F1", Aspect: "other", Statement: "不好意思，暂时无法确认。"}}
	for name, content := range map[string]string{
		"existence":  "房费含早餐。",
		"capability": "宠物可入住。",
		"policy":     "入住要带身份证。",
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateGeneratedReplyFactAspectBoundaries(content, facts); err == nil || !strings.Contains(err.Error(), "unsupported "+name+" claim") {
				t.Fatalf("common %s claim form must be checked, content=%q err=%v", name, content, err)
			}
		})
	}
}

func TestValidateGeneratedReplyFactAspectBoundariesAllowsNaturalServiceOffer(t *testing.T) {
	facts := []replyFactRequirement{{FactID: "F1", Aspect: "existence", Statement: "酒店提供早餐。"}}
	if err := validateGeneratedReplyFactAspectBoundaries("酒店提供早餐，有需要随时说。", facts); err != nil {
		t.Fatalf("non-factual service offer should not trigger the existence or policy guard, got %v", err)
	}
}

func TestNormalizeGeneratedReplyPartsAcceptsUniqueTaskLocalFactID(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{
			TaskID:        "task-1",
			Intent:        "hotel_info",
			OutputKind:    "text",
			ReplyRequired: true,
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
				{FactID: "task-1F1", Statement: "房间内有两瓶矿泉水。", CriticalValues: []string{"两瓶"}},
			},
		},
	}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"房间内有两瓶矿泉水。","coveredFactIds":["F1"]}]}`
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
	if err != nil || got != "房间内有两瓶矿泉水。" {
		t.Fatalf("unique task-local fact ID must normalize to its scoped ID, got=%q err=%v", got, err)
	}
}

func TestNormalizeGeneratedReplyPartsAcceptsEquivalentTaskScopedFactID(t *testing.T) {
	for _, tt := range []struct {
		name    string
		taskID  string
		factID  string
		modelID string
	}{
		{name: "compact model id", taskID: "task-1", factID: "task-1F1", modelID: "T1F1"},
		{name: "verbose model id", taskID: "T1", factID: "T1F1", modelID: "task-1F1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
				TaskID: tt.taskID, Intent: "hotel_info", OutputKind: "text", ReplyRequired: true,
				SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
					FactID: tt.factID, Statement: "房间内有两瓶矿泉水。", CriticalValues: []string{"两瓶"},
				}},
			}}}
			raw := fmt.Sprintf(`{"replyParts":[{"taskId":%q,"content":"房间内有两瓶矿泉水。","coveredFactIds":[%q]}]}`, tt.taskID, tt.modelID)
			got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
			if err != nil || got != "房间内有两瓶矿泉水。" {
				t.Fatalf("equivalent task-scoped fact ID must normalize, got=%q err=%v", got, err)
			}
		})
	}
}

func TestNormalizeGeneratedReplyPartsScopesLocalFactIDsPerTask(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{
			TaskID:        "task-1",
			Intent:        "hotel_info",
			OutputKind:    "text",
			ReplyRequired: true,
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
				{FactID: "task-1F1", Statement: "房间内有两瓶矿泉水。", CriticalValues: []string{"两瓶"}},
			},
		},
		{
			TaskID:        "task-2",
			Intent:        "hotel_info",
			OutputKind:    "text",
			ReplyRequired: true,
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
				{FactID: "task-2F1", Statement: "矿泉水免费。", CriticalValues: []string{"免费"}},
			},
		},
	}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"房间内有两瓶矿泉水。","coveredFactIds":["F1"]},{"taskId":"task-2","content":"矿泉水免费。","coveredFactIds":["F1"]}]}`
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
	if err != nil || got != "房间内有两瓶矿泉水。\n<<NEXT_MESSAGE>>\n矿泉水免费。" {
		t.Fatalf("the same local fact ID must resolve independently inside each task, got=%q err=%v", got, err)
	}
}

func TestNormalizeCoveredFactIDRejectsUnsafeAliases(t *testing.T) {
	group := textReplyTaskGroup{TaskID: "task-1", Facts: []replyFactRequirement{
		{FactID: "task-1F1", Statement: "房间内有两瓶矿泉水。"},
	}}
	for name, coveredFactIDs := range map[string][]string{
		"cross_task":           {"task-2F1"},
		"unknown_local":        {"F2"},
		"unsupported_hyphen":   {"task-1-F1"},
		"normalized_duplicate": {"F1", "task-1F1"},
	} {
		t.Run(name, func(t *testing.T) {
			part := generatedReplyPart{TaskID: "task-1", Content: "房间内有两瓶矿泉水。", CoveredFactIDs: coveredFactIDs}
			if err := validateCoveredFacts(part, group); !errors.Is(err, errGeneratedReplyProtocol) {
				t.Fatalf("unsafe fact alias must fail closed, got %v", err)
			}
		})
	}
}

func TestNormalizeCoveredFactIDRejectsAmbiguousLocalSuffix(t *testing.T) {
	group := textReplyTaskGroup{TaskID: "task-1", Facts: []replyFactRequirement{
		{FactID: "task-1F1", Statement: "事实一。"},
		{FactID: "task-1F1", Statement: "重复且歧义的事实一。"},
	}}
	part := generatedReplyPart{TaskID: "task-1", Content: "事实一。", CoveredFactIDs: []string{"F1"}}
	if err := validateCoveredFacts(part, group); !errors.Is(err, errGeneratedReplyProtocol) || !strings.Contains(err.Error(), "ambiguous coveredFactId") {
		t.Fatalf("ambiguous task-local fact suffix must fail closed, got %v", err)
	}
}

func TestBuildMultiReplyOutputInstructionUsesScopedFactIDExample(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{
			TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true,
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "task-1F1", Statement: "房间有空调。"}},
		},
	}}
	instruction := buildMultiReplyOutputInstruction(plan, true)
	if !strings.Contains(instruction, `"coveredFactIds":["task-1F1"]`) || strings.Contains(instruction, `"coveredFactIds":["F1"]`) {
		t.Fatalf("reply protocol example must teach task-scoped fact IDs: %s", instruction)
	}
}

func TestBuildMultiReplyOutputInstructionExampleCoversAllTasksAndFacts(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{}
	for index := 1; index <= 8; index++ {
		facts := []callbacks.KnowledgeEvidenceFactTraceData{{
			FactID:    fmt.Sprintf("task-%dF1", index),
			Statement: fmt.Sprintf("第%d项已确认。", index),
		}}
		if index == 1 || index == 8 {
			facts = append(facts, callbacks.KnowledgeEvidenceFactTraceData{
				FactID:    fmt.Sprintf("task-%dF2", index),
				Statement: fmt.Sprintf("第%d项的第二个事实已确认。", index),
			})
		}
		plan.TaskPlans = append(plan.TaskPlans, callbacks.ReplyTaskPlanTraceData{
			TaskID:         fmt.Sprintf("task-%d", index),
			Intent:         "hotel_info",
			ResolvedText:   fmt.Sprintf("问题%d", index),
			OutputKind:     "text",
			ReplyRequired:  true,
			SupportedFacts: facts,
		})
	}

	instruction := buildMultiReplyOutputInstruction(plan, true)
	const prefix = "格式为："
	const suffix = "。JSON 外层"
	start := strings.Index(instruction, prefix)
	end := strings.Index(instruction, suffix)
	if start < 0 || end <= start+len(prefix) {
		t.Fatalf("could not locate protocol example: %s", instruction)
	}
	var example generatedReplyPartsEnvelope
	if err := json.Unmarshal([]byte(instruction[start+len(prefix):end]), &example); err != nil {
		t.Fatalf("decode protocol example: %v", err)
	}
	if len(example.ReplyParts) != 8 {
		t.Fatalf("protocol example must demonstrate every active task, got %#v", example.ReplyParts)
	}
	if got := example.ReplyParts[0].CoveredFactIDs; len(got) != 2 || got[0] != "task-1F1" || got[1] != "task-1F2" {
		t.Fatalf("WiFi-like first task must demonstrate all facts: %#v", got)
	}
	if got := example.ReplyParts[7].CoveredFactIDs; len(got) != 2 || got[0] != "task-8F1" || got[1] != "task-8F2" {
		t.Fatalf("invoice-like last task must demonstrate all facts: %#v", got)
	}
}

func TestBuildMultiReplyOutputInstructionGroupsSameStatementFactIDs(t *testing.T) {
	statement := "酒店没有传统前台，可以通过入住机或小程序线上智能化方式办理入住。"
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID:        "task-1",
		Intent:        "hotel_info",
		ResolvedText:  "怎么办理入住",
		OutputKind:    "text",
		ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
			{FactID: "task-1F1", Aspect: "method", Statement: statement, CriticalValues: []string{"入住机", "小程序"}},
			{FactID: "task-1F2", Aspect: "existence", Statement: statement, CriticalValues: []string{"传统前台"}},
		},
	}}}

	instruction := buildMultiReplyOutputInstruction(plan, true)
	if strings.Count(instruction, statement) != 1 || !strings.Contains(instruction, "task-1F1、task-1F2（同一事实，content 只表达一次）") {
		t.Fatalf("equivalent fact IDs must share one customer-facing statement: %s", instruction)
	}
	raw := `{"replyParts":[{"taskId":"task-1","content":"酒店没有传统前台，可以通过入住机或小程序线上智能化方式办理入住。","coveredFactIds":["task-1F1","task-1F2"]}]}`
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, true)
	if err != nil || got != statement {
		t.Fatalf("one natural sentence must be allowed to cover equivalent fact IDs, got=%q err=%v", got, err)
	}
}

func TestBuildMultiReplyOutputInstructionGroupsContainedFactIDs(t *testing.T) {
	complete := "房间内有两瓶矿泉水，都是免费的。"
	contained := "房间内有两瓶矿泉水。"
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "hotel_info", ResolvedText: "房间有几瓶矿泉水，收费吗", OutputKind: "text", ReplyRequired: true,
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
			{FactID: "task-1F1", Aspect: "price", Statement: complete, CriticalValues: []string{"免费"}},
			{FactID: "task-1F2", Aspect: "quantity", Statement: contained, CriticalValues: []string{"两瓶"}},
		},
	}}}

	instruction := buildMultiReplyOutputInstruction(plan, true)
	if strings.Count(instruction, complete) != 1 || strings.Contains(instruction, "："+contained) {
		t.Fatalf("contained customer-facing fact must be displayed only through the complete sentence: %s", instruction)
	}
	if !strings.Contains(instruction, "task-1F1") || !strings.Contains(instruction, "task-1F2") {
		t.Fatalf("all covered fact IDs must remain in the grouped instruction: %s", instruction)
	}
}

func TestBuildMultiReplyOutputInstructionDoesNotInventFactIDForFactlessTask(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "interaction", OutputKind: "text", ReplyRequired: true},
	}}
	instruction := buildMultiReplyOutputInstruction(plan, true)
	if strings.Contains(instruction, `"coveredFactIds"`) || strings.Contains(instruction, "task-1F1") {
		t.Fatalf("factless task example must not teach a nonexistent fact ID: %s", instruction)
	}
}

func TestNormalizeGeneratedReplyPartsRejectsModelControlledMessageMarker(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"早餐七点开始。<<NEXT_MESSAGE>>额外内容"}]}`
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, true)
	if got != "" || !errors.Is(err, errGeneratedReplyProtocol) {
		t.Fatalf("model-controlled composition marker must be rejected, got=%q err=%v", got, err)
	}
}
