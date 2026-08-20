package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestEnforceGeneratedReplyActionLedgerRemovesResourceCommitText(t *testing.T) {
	summary := &RunResult{
		ReplyText: "停车从繁华大道辅路进，地上停车场免费。\n\n定位和小程序我这边发你。",
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:   "hotel_variable",
		NeedsKnowledge:  true,
		NeedsResource:   true,
		ResourceActions: []string{"provide_location", "provide_mini_program"},
	}

	enforceGeneratedReplyActionLedger(summary, collector)

	if strings.Contains(summary.ReplyText, "发你") || strings.Contains(summary.ReplyText, "小程序") || strings.Contains(summary.ReplyText, "定位") {
		t.Fatalf("expected generated resource commit text to be removed, got %q", summary.ReplyText)
	}
	if !strings.Contains(summary.ReplyText, "停车") {
		t.Fatalf("expected knowledge answer to remain, got %q", summary.ReplyText)
	}
	if !strings.Contains(collector.Data.Pipeline.Validate.Reason, "action ledger removed") {
		t.Fatalf("expected validate reason to record cleanup, got %q", collector.Data.Pipeline.Validate.Reason)
	}
}

func TestEnforceGeneratedReplyActionLedgerScopesCorrectionToFirstSentence(t *testing.T) {
	summary := &RunResult{
		ReplyText: "哈哈，是我看岔了，抱歉。你说电视打不开，是现在到房间了还是过几天才住？",
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent: "interaction",
		SubIntent:     "correction",
	}

	enforceGeneratedReplyActionLedger(summary, collector)

	if summary.ReplyText != "哈哈，是我看岔了，抱歉。" {
		t.Fatalf("expected correction to stop after the current acknowledgement, got %q", summary.ReplyText)
	}
	if !strings.Contains(collector.Data.Pipeline.Validate.Reason, "current correction") {
		t.Fatalf("expected correction scoping to be recorded, got %q", collector.Data.Pipeline.Validate.Reason)
	}
}

func TestEnforceGeneratedReplyActionLedgerNormalizesIncompleteEnding(t *testing.T) {
	summary := &RunResult{ReplyText: "门锁电量低了，稍等，"}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_info",
		SubIntent:      "door_lock",
		NeedsKnowledge: true,
	}

	enforceGeneratedReplyActionLedger(summary, collector)

	if summary.ReplyText != "门锁电量低了。" {
		t.Fatalf("expected incomplete trailing clause to be removed, got %q", summary.ReplyText)
	}
	if !strings.Contains(collector.Data.Pipeline.Validate.Reason, "incomplete reply ending") {
		t.Fatalf("expected incomplete ending normalization to be recorded, got %q", collector.Data.Pipeline.Validate.Reason)
	}
}

func TestEnforceGeneratedReplyActionLedgerRemovesUnsupportedStaffAction(t *testing.T) {
	summary := &RunResult{
		ReplyText: "目前不能直接处理这项服务。你到时候入住了跟我说下房号。我这边需要找同事过去看看。",
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_info",
		NeedsKnowledge: true,
	}

	enforceGeneratedReplyActionLedger(summary, collector)

	if strings.Contains(summary.ReplyText, "找同事") || strings.Contains(summary.ReplyText, "过去看看") {
		t.Fatalf("expected unsupported staff action sentence to be removed, got %q", summary.ReplyText)
	}
	if strings.Contains(summary.ReplyText, "房号") {
		t.Fatalf("expected useless customer field request to be removed, got %q", summary.ReplyText)
	}
	if !strings.Contains(summary.ReplyText, "不能直接处理") {
		t.Fatalf("expected direct capability boundary to remain, got %q", summary.ReplyText)
	}
}

func TestEnforceGeneratedReplyActionLedgerRemovesNeutralStaffAction(t *testing.T) {
	summary := &RunResult{
		ReplyText: "电视投屏要先连同一个 WiFi。得让同事去房间看一下。我看看怎么能帮上你。",
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_info",
		NeedsKnowledge: true,
	}

	enforceGeneratedReplyActionLedger(summary, collector)

	if strings.Contains(summary.ReplyText, "同事去") || strings.Contains(summary.ReplyText, "我看看") {
		t.Fatalf("expected neutral unsupported staff action to be removed, got %q", summary.ReplyText)
	}
	if !strings.Contains(summary.ReplyText, "电视投屏") {
		t.Fatalf("expected knowledge sentence to remain, got %q", summary.ReplyText)
	}
}

func TestEnforceGeneratedReplyActionLedgerRemovesFrontDeskTransferPromise(t *testing.T) {
	summary := &RunResult{
		ReplyText: "目前无法直接处理房间设备故障。空调不制冷这个问题需要现场看一下。你现在在哪个房间？我帮你转前台同事来跟进。",
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_info",
		NeedsKnowledge: true,
	}

	enforceGeneratedReplyActionLedger(summary, collector)

	if strings.Contains(summary.ReplyText, "现场看") || strings.Contains(summary.ReplyText, "前台同事") || strings.Contains(summary.ReplyText, "跟进") {
		t.Fatalf("expected unsupported front desk transfer promise to be removed, got %q", summary.ReplyText)
	}
	if strings.Contains(summary.ReplyText, "哪个房间") {
		t.Fatalf("expected unavailable service not to collect a room number, got %q", summary.ReplyText)
	}
	if !strings.Contains(summary.ReplyText, "无法直接处理") {
		t.Fatalf("expected direct capability boundary to remain, got %q", summary.ReplyText)
	}
}

func TestEnforceGeneratedReplyActionLedgerRemovesObservedFrontDeskTransferVariant(t *testing.T) {
	summary := &RunResult{
		ReplyText: "目前无法直接处理房间设备故障。空调的事情需要现场看看，你现在在哪个房间？我帮你转达给前台同事处理。",
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_info",
		NeedsKnowledge: true,
	}

	enforceGeneratedReplyActionLedger(summary, collector)

	for _, forbidden := range []string{"现场", "转达", "前台同事"} {
		if strings.Contains(summary.ReplyText, forbidden) {
			t.Fatalf("expected unsupported observed transfer variant %q to be removed, got %q", forbidden, summary.ReplyText)
		}
	}
	if strings.Contains(summary.ReplyText, "哪个房间") {
		t.Fatalf("expected unavailable service not to collect a room number, got %q", summary.ReplyText)
	}
	if !strings.Contains(summary.ReplyText, "无法直接处理") {
		t.Fatalf("expected direct capability boundary to remain, got %q", summary.ReplyText)
	}
}

func TestEnforceGeneratedReplyActionLedgerRemovesFindSomeoneAndAskColleague(t *testing.T) {
	tests := []struct {
		name     string
		reply    string
		expected string
	}{
		{
			name:     "find someone",
			reply:    "空调不制冷的话，先把模式调到制冷，温度设低一点试试。如果调完还是不行，我再帮你找人来处理，你在哪个房间？",
			expected: "先把模式调到制冷",
		},
		{
			name:     "front desk worker handoff suggestion",
			reply:    "空调不制冷这个事得联系前台工作人员帮你处理，我这边没法直接操作房间设备。你现在方便去前台说一下吗，或者我帮你转人工？",
			expected: "我这边没法直接操作房间设备",
		},
		{
			name:     "transfer it over",
			reply:    "目前不能直接处理这项服务。你现在在哪个房间？我帮你转过去。",
			expected: "不能直接处理",
		},
		{
			name:     "ask colleague",
			reply:    "洗衣房的具体位置当前资料没写明，你到前台问一下同事就好，109离前台不远。",
			expected: "洗衣房的具体位置当前资料没写明",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := &RunResult{ReplyText: tt.reply}
			collector := callbacks.NewRuntimeTraceCollector()
			collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
				PrimaryIntent:  "hotel_info",
				NeedsKnowledge: true,
			}

			enforceGeneratedReplyActionLedger(summary, collector)

			for _, forbidden := range []string{"找人", "问一下同事", "问下同事", "转人工", "转过去", "前台工作人员", "去前台说一下", "房号", "哪个房间"} {
				if strings.Contains(summary.ReplyText, forbidden) {
					t.Fatalf("expected unsupported staff wording %q to be removed, got %q", forbidden, summary.ReplyText)
				}
			}
			if !strings.Contains(summary.ReplyText, tt.expected) {
				t.Fatalf("expected useful text %q to remain, got %q", tt.expected, summary.ReplyText)
			}
		})
	}
}

func TestGeneratedReplyBoundaryRemovesUnavailableDiscountCustomerFields(t *testing.T) {
	plan := contracts.ReplyPlanV2{Tasks: []contracts.ReplyPlanTaskV2{{
		TaskKey: "discount", Intent: "hotel_info", SubIntent: "discount", OutputMode: "text",
		Knowledge:   contracts.ReplyPlanKnowledge{Policy: "required", Status: "unavailable"},
		Constraints: []string{"state_knowledge_boundary_only", "do_not_collect_customer_fields"},
	}}}
	intent := callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "discount", NeedsKnowledge: true}

	got, _, _ := cleanGeneratedReplyTextForTasks(
		"当前资料无法确认是否有优惠，请提供订单号、姓名和手机号，我帮你联系前台确认。",
		intent,
		&plan,
		[]string{"discount"},
	)

	if got != "当前资料无法确认是否有优惠。" {
		t.Fatalf("expected direct knowledge boundary only, got %q", got)
	}
}

func TestGeneratedReplyBoundaryRemovesUnsupportedRoomChangeCustomerFields(t *testing.T) {
	plan := contracts.ReplyPlanV2{Tasks: []contracts.ReplyPlanTaskV2{{
		TaskKey: "upgrade", Intent: "service_request", SubIntent: "room_change", OutputMode: "text",
		Knowledge: contracts.ReplyPlanKnowledge{Policy: "optional", Status: "not_needed"},
	}}}
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "service_request", SubIntent: "room_change",
		IntentTasks: []callbacks.IntentTaskTraceData{{Intent: "service_request", SubIntent: "room_change"}},
	}

	got, _, _ := cleanGeneratedReplyTextForTasks(
		"目前不能办理大床房升级。请把房号、订单号、姓名和手机号发我，后续会有人联系你。",
		intent,
		&plan,
		[]string{"upgrade"},
	)

	if got != "目前不能办理大床房升级。" {
		t.Fatalf("expected unsupported capability boundary only, got %q", got)
	}
}

func TestGeneratedReplyBoundaryKeepsToolBackedRequiredFieldQuestion(t *testing.T) {
	plan := contracts.ReplyPlanV2{Tasks: []contracts.ReplyPlanTaskV2{{
		TaskKey: "upgrade", Intent: "service_request", SubIntent: "room_change", OutputMode: "clarification",
		Knowledge: contracts.ReplyPlanKnowledge{Policy: "forbidden", Status: "not_needed"},
	}}}
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "service_request", SubIntent: "room_change", NeedsTool: true, ToolCodes: []string{"room_change"},
		IntentTasks: []callbacks.IntentTaskTraceData{{Intent: "service_request", SubIntent: "room_change", NeedsTool: true}},
	}
	original := "可以办理，请提供房号和订单号。"

	got, _, _ := cleanGeneratedReplyTextForTasks(original, intent, &plan, []string{"upgrade"})

	if got != original {
		t.Fatalf("expected a real tool-backed required-field question to remain, got %q", got)
	}
}

func TestGeneratedReplyBoundaryKeepsGenuineAmbiguityClarification(t *testing.T) {
	plan := contracts.ReplyPlanV2{Tasks: []contracts.ReplyPlanTaskV2{{
		TaskKey: "ambiguous", Intent: "interaction", SubIntent: "clarify", OutputMode: "clarification",
		Knowledge:   contracts.ReplyPlanKnowledge{Policy: "forbidden", Status: "not_needed"},
		Constraints: []string{"clarify_ambiguous_expression_only"},
	}}}
	intent := callbacks.IntentTraceData{PrimaryIntent: "interaction", SubIntent: "clarify", NeedsClarification: true}
	original := "你说的“那个”是指停车还是早餐？"

	got, _, _ := cleanGeneratedReplyTextForTasks(original, intent, &plan, []string{"ambiguous"})

	if got != original {
		t.Fatalf("expected genuine ambiguity clarification to remain, got %q", got)
	}
}

func TestGeneratedReplyBoundaryKeepsNegatedCustomerFieldStatement(t *testing.T) {
	plan := contracts.ReplyPlanV2{Tasks: []contracts.ReplyPlanTaskV2{{
		TaskKey: "upgrade", Intent: "hotel_info", SubIntent: "room_change", OutputMode: "text",
		Knowledge:   contracts.ReplyPlanKnowledge{Policy: "required", Status: "no_context"},
		Constraints: []string{"do_not_collect_customer_fields"},
	}}}
	intent := callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "room_change", NeedsKnowledge: true}
	original := "目前无法确认能否升级，也无需提供房号、订单号、姓名或手机号。"

	got, _, _ := cleanGeneratedReplyTextForTasks(original, intent, &plan, []string{"upgrade"})

	if got != original {
		t.Fatalf("expected explicit no-collection statement to remain, got %q", got)
	}
}

func TestGeneratedReplyBoundaryKeepsSelfServiceFieldInstruction(t *testing.T) {
	plan := contracts.ReplyPlanV2{Tasks: []contracts.ReplyPlanTaskV2{{
		TaskKey: "checkin", Intent: "hotel_info", SubIntent: "checkin_process", OutputMode: "text",
		Knowledge: contracts.ReplyPlanKnowledge{Policy: "required", Status: "has_context"},
	}}}
	intent := callbacks.IntentTraceData{PrimaryIntent: "hotel_info", SubIntent: "checkin_process", NeedsKnowledge: true}
	original := "请在入住小程序页面填写姓名和手机号，然后提交。"

	got, _, _ := cleanGeneratedReplyTextForTasks(original, intent, &plan, []string{"checkin"})

	if got != original {
		t.Fatalf("expected self-service field instructions to remain, got %q", got)
	}
}

func TestGeneratedReplyBoundaryRequiresPublishedHumanRoute(t *testing.T) {
	intent := callbacks.IntentTraceData{PrimaryIntent: "service_request", NeedsHumanRoute: true}
	got, _, _ := cleanGeneratedReplyText("我帮你联系前台处理。", intent)
	if got != "" {
		t.Fatalf("expected an unconfigured human route claim to be removed, got %q", got)
	}

	intent.HumanRoutePolicy = "managed_mode"
	got, _, _ = cleanGeneratedReplyText("我帮你联系前台处理。", intent)
	if got != "" {
		t.Fatalf("a published route authorizes the server confirmation, not a model promise: %q", got)
	}
}
