package executor

import (
	"strings"
	"testing"

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
		ReplyText: "你到时候入住了跟我说下房号。我这边需要找同事过去看看。",
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
	if !strings.Contains(summary.ReplyText, "房号") {
		t.Fatalf("expected non-action sentence to remain, got %q", summary.ReplyText)
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

func TestEnforceGeneratedReplyActionLedgerRemovesUnsupportedRecordPromiseFromMixedReply(t *testing.T) {
	summary := &RunResult{
		ReplyText: "房间空调出问题确实挺影响休息的，我先记下你在1302。早餐时间是早上7点到10点。",
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_info",
		NeedsKnowledge: true,
	}

	enforceGeneratedReplyActionLedger(summary, collector)

	for _, forbidden := range []string{"我先记下", "记下你在1302"} {
		if strings.Contains(summary.ReplyText, forbidden) {
			t.Fatalf("expected unsupported record promise %q to be removed, got %q", forbidden, summary.ReplyText)
		}
	}
	if !strings.Contains(summary.ReplyText, "早餐时间是早上7点到10点") {
		t.Fatalf("expected the supported breakfast answer to remain, got %q", summary.ReplyText)
	}
}

func TestEnforceGeneratedReplyActionLedgerRemovesUnsupportedRecordPromiseVariants(t *testing.T) {
	for _, phrase := range []string{"我已经记录", "我已登记", "我已经受理"} {
		t.Run(phrase, func(t *testing.T) {
			summary := &RunResult{ReplyText: "早餐时间是早上7点到10点。" + phrase + "你的情况。"}
			collector := callbacks.NewRuntimeTraceCollector()
			collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
				PrimaryIntent:  "hotel_info",
				NeedsKnowledge: true,
			}

			enforceGeneratedReplyActionLedger(summary, collector)

			if strings.Contains(summary.ReplyText, phrase) {
				t.Fatalf("expected unsupported action promise %q to be removed, got %q", phrase, summary.ReplyText)
			}
			if !strings.Contains(summary.ReplyText, "早餐时间是早上7点到10点") {
				t.Fatalf("expected the supported breakfast answer to remain, got %q", summary.ReplyText)
			}
		})
	}
}

func TestEnforceGeneratedReplyActionLedgerKeepsAnswerBeforeUnpunctuatedRecordPromise(t *testing.T) {
	summary := &RunResult{ReplyText: "早餐时间是早上7点到10点同时我先记下您在1302。"}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_info",
		NeedsKnowledge: true,
	}

	enforceGeneratedReplyActionLedger(summary, collector)

	if strings.Contains(summary.ReplyText, "我先记下") {
		t.Fatalf("expected unsupported record promise to be removed, got %q", summary.ReplyText)
	}
	if !strings.Contains(summary.ReplyText, "早餐时间是早上7点到10点") {
		t.Fatalf("expected the supported answer in the same sentence to remain, got %q", summary.ReplyText)
	}
}

func TestEnforceGeneratedReplyActionLedgerKeepsNeutralRecordKnowledge(t *testing.T) {
	summary := &RunResult{ReplyText: "入住时需要在小程序登记身份证信息，系统会记录入住信息。"}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_info",
		NeedsKnowledge: true,
	}

	enforceGeneratedReplyActionLedger(summary, collector)

	if summary.ReplyText != "入住时需要在小程序登记身份证信息，系统会记录入住信息。" {
		t.Fatalf("expected neutral record knowledge to remain unchanged, got %q", summary.ReplyText)
	}
}

func TestEnforceGeneratedReplyActionLedgerRemovesFrontDeskTransferPromise(t *testing.T) {
	summary := &RunResult{
		ReplyText: "空调不制冷这个问题需要现场看一下。你现在在哪个房间？我帮你转前台同事来跟进。",
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
	if !strings.Contains(summary.ReplyText, "你现在在哪个房间") {
		t.Fatalf("expected key clarification sentence to remain, got %q", summary.ReplyText)
	}
}

func TestEnforceGeneratedReplyActionLedgerRemovesObservedFrontDeskTransferVariant(t *testing.T) {
	summary := &RunResult{
		ReplyText: "空调的事情需要现场看看，你现在在哪个房间？我帮你转达给前台同事处理。",
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_info",
		NeedsKnowledge: true,
	}

	enforceGeneratedReplyActionLedger(summary, collector)

	for _, forbidden := range []string{"现场", "转达", "前台同事", "处理"} {
		if strings.Contains(summary.ReplyText, forbidden) {
			t.Fatalf("expected unsupported observed transfer variant %q to be removed, got %q", forbidden, summary.ReplyText)
		}
	}
	if !strings.Contains(summary.ReplyText, "你现在在哪个房间") {
		t.Fatalf("expected key clarification sentence to remain, got %q", summary.ReplyText)
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
			expected: "你在哪个房间",
		},
		{
			name:     "front desk worker handoff suggestion",
			reply:    "空调不制冷这个事得联系前台工作人员帮你处理，我这边没法直接操作房间设备。你现在方便去前台说一下吗，或者我帮你转人工？",
			expected: "我这边没法直接操作房间设备",
		},
		{
			name:     "transfer it over",
			reply:    "你现在在哪个房间？我帮你转过去。",
			expected: "你现在在哪个房间",
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

			for _, forbidden := range []string{"找人", "处理", "问一下同事", "问下同事", "转人工", "转过去", "前台工作人员", "去前台说一下"} {
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

func TestEnforceGeneratedReplyActionLedgerRequestsRealHandoffForPromiseOnlyReply(t *testing.T) {
	summary := &RunResult{ReplyText: "稍等，我先帮你把信息转给人工对接处理。"}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:  "service_request",
		NeedsKnowledge: true,
	}
	collector.SetActionLedger(buildInitialActionLedger(collector.Data.Pipeline.Intent))

	outcome := enforceGeneratedReplyActionLedger(summary, collector)

	if !outcome.RequestHandoffConfirmation {
		t.Fatalf("expected unsupported promise-only reply to request persisted handoff confirmation, got %#v", outcome)
	}
	if summary.ReplyText != "" {
		t.Fatalf("expected unsupported promise not to be committed as text, got %q", summary.ReplyText)
	}
}

func TestEnforceGeneratedReplyActionLedgerKeepsRoomNumberClarification(t *testing.T) {
	summary := &RunResult{ReplyText: "请告诉我房间号，我先确认是哪一间房。"}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent:  "service_request",
		NeedsKnowledge: true,
	}
	collector.SetActionLedger(buildInitialActionLedger(collector.Data.Pipeline.Intent))

	outcome := enforceGeneratedReplyActionLedger(summary, collector)

	if outcome.RequestHandoffConfirmation {
		t.Fatalf("room-number clarification must not request handoff, got %#v", outcome)
	}
	if summary.ReplyText != "请告诉我房间号，我先确认是哪一间房。" {
		t.Fatalf("expected room-number clarification to remain unchanged, got %q", summary.ReplyText)
	}
}
