package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestScoreRecordRequiresEveryMultiQuestionOutcome(t *testing.T) {
	tests := []struct {
		id       string
		partial  record
		complete record
		missing  string
	}{
		{
			id: "C02",
			partial: record{
				Status:       "completed",
				ReplyText:    "早餐供应时间是7:00-9:30。",
				Intent:       "hotel_info",
				KnowledgeHit: true,
			},
			complete: record{
				Status:       "completed",
				ReplyText:    "早餐供应时间是7:00-9:30；停车免费；剃须刀可在自助区领取。",
				Intent:       "hotel_info",
				KnowledgeHit: true,
			},
			missing: "停车问题",
		},
		{
			id: "X03",
			partial: record{
				Status:       "completed",
				ReplyText:    "早餐供应时间是7:00-9:30。",
				Intent:       "service_request",
				KnowledgeHit: true,
			},
			complete: record{
				Status:                "completed",
				ReplyText:             "早餐供应时间是7:00-9:30。",
				Intent:                "service_request",
				KnowledgeHit:          true,
				DeferredHandoff:       true,
				DeferredHandoffReason: "部分酒店业务问题需要门店同事接手；待处理问题：空调坏了",
			},
			missing: "空调故障处理",
		},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			sc := findEvalScenario(t, tt.id)
			partialScore, partialIssues := scoreRecord(sc, tt.partial)
			if partialScore >= 80 {
				t.Fatalf("partial multi-question reply must fail, score=%d issues=%v", partialScore, partialIssues)
			}
			if !issuesContain(partialIssues, tt.missing) {
				t.Fatalf("expected missing outcome %q, got %v", tt.missing, partialIssues)
			}

			completeScore, completeIssues := scoreRecord(sc, tt.complete)
			if completeScore < 80 {
				t.Fatalf("complete multi-question outcome must pass, score=%d issues=%v", completeScore, completeIssues)
			}
		})
	}
}

func TestFillRecordFromRunLogCapturesDeferredHandoffAction(t *testing.T) {
	logItem := &models.AgentRunLog{
		FinalStatus: "completed",
		ReplyText:   "早餐供应时间是7:00-9:30。",
		TraceData:   `{"runtime":{"pipeline":{"evidenceJudge":{"deferredHandoff":true,"deferredHandoffReason":"部分酒店业务问题需要门店同事接手；待处理问题：空调坏了"}}}}`,
	}
	rec := (&runner{}).fillRecordFromRunLog(record{}, scenario{}, logItem)
	if !rec.DeferredHandoff || !strings.Contains(rec.DeferredHandoffReason, "空调坏了") {
		t.Fatalf("expected deferred handoff action in eval record, got %#v", rec)
	}
}

func TestSelectActiveAnswerSuite(t *testing.T) {
	selected, err := selectScenarioSuite(buildScenarios(1), "active-answer")
	if err != nil {
		t.Fatalf("select active-answer suite: %v", err)
	}
	wantIDs := []string{"AA01", "AA02", "AA03", "AA04", "AA05", "AA06", "AA07", "AA08"}
	if len(selected) != len(wantIDs) {
		t.Fatalf("active-answer suite size=%d want=%d", len(selected), len(wantIDs))
	}
	for i, want := range wantIDs {
		if selected[i].ID != want {
			t.Fatalf("active-answer suite[%d]=%s want=%s", i, selected[i].ID, want)
		}
	}

	alias, err := selectScenarioSuite(buildScenarios(1), "active-answer-focused")
	if err != nil || len(alias) != len(selected) {
		t.Fatalf("select active-answer alias: len=%d err=%v", len(alias), err)
	}
}

func TestActiveAnswerScenariosCoverFocusedAcceptance(t *testing.T) {
	eight := findEvalScenario(t, "AA01")
	if len(eight.Turns) != 1 || len(eight.RequiredOutcomes) < 16 || eight.MaxReplyMessages != 3 || eight.NeedsResource != nil {
		t.Fatalf("eight-question scenario is incomplete: turns=%d outcomes=%d maxMessages=%d", len(eight.Turns), len(eight.RequiredOutcomes), eight.MaxReplyMessages)
	}
	for _, label := range []string{"WiFi账号", "入住机", "人脸开门", "矿泉水数量", "外卖机器人存在", "南七店外卖地址", "停车场充电桩", "发票下载时间"} {
		if !hasOutcomeLabel(eight.RequiredOutcomes, label) {
			t.Fatalf("eight-question scenario missing outcome %q", label)
		}
	}

	for _, id := range []string{"AA02", "AA03"} {
		sc := findEvalScenario(t, id)
		if len(sc.Turns) != 1 || sc.Turns[0].Type != enums.IMMessageTypeVoice || sc.MaxReplyMessages != 3 {
			t.Fatalf("%s must be a bounded single voice turn: %#v", id, sc)
		}
		if id == "AA02" && sc.NeedsResource != nil {
			t.Fatalf("%s must allow the existing check-in mini-program action without treating it as a failure", id)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(sc.Turns[0].Payload), &payload); err != nil {
			t.Fatalf("decode %s voice payload: %v", id, err)
		}
		mediaText, _ := payload["mediaText"].(string)
		mediaSummary, _ := payload["mediaSummary"].(string)
		if strings.TrimSpace(mediaText) == "" || mediaText == mediaSummary {
			t.Fatalf("%s must keep a complete mediaText distinct from its short summary", id)
		}
	}

	backref := findEvalScenario(t, "AA04")
	if len(backref.Turns) != 2 || !strings.Contains(backref.Turns[1].Content, "麦田") || !hasOutcomeLabel(backref.RequiredOutcomes, "麦田办公桌") {
		t.Fatalf("room-type back-reference scenario is incomplete: %#v", backref)
	}
	address := findEvalScenario(t, "AA05")
	if len(address.Turns) != 2 || !strings.Contains(address.Turns[1].Content, "再说一遍") || !hasOutcomeLabel(address.RequiredOutcomes, "房间号格式") {
		t.Fatalf("address repeat scenario is incomplete: %#v", address)
	}
	water := findEvalScenario(t, "AA06")
	if !hasOutcomeLabel(water.RequiredOutcomes, "矿泉水数量") || !hasOutcomeLabel(water.RequiredOutcomes, "矿泉水费用") {
		t.Fatalf("water follow-up must require quantity and price: %#v", water.RequiredOutcomes)
	}
	robot := findEvalScenario(t, "AA07")
	if !containsExact(robot.Banned, "送到房间") || !containsExact(robot.Banned, "送上来") {
		t.Fatalf("robot scenario must ban unsupported delivery scope: %v", robot.Banned)
	}
	protocol := findEvalScenario(t, "AA08")
	if !hasOutcomeLabel(protocol.RequiredOutcomes, "麦田办公桌") || !containsExact(defaultBannedPhrases(), "replyParts") || !containsExact(defaultBannedPhrases(), "[历史消息]") {
		t.Fatalf("protocol leakage scenario or global guards are incomplete")
	}
}

func TestFocusedScoringRejectsMissingFactsAndUnsafeOutput(t *testing.T) {
	water := findEvalScenario(t, "AA06")
	partialScore, partialIssues := scoreRecord(water, record{
		Status:       "completed",
		ReplyText:    "是的，房间矿泉水免费。",
		Intent:       "hotel_info",
		KnowledgeHit: true,
	})
	if partialScore >= 80 || !issuesContain(partialIssues, "矿泉水数量") {
		t.Fatalf("missing quantity must fail: score=%d issues=%v", partialScore, partialIssues)
	}
	completeScore, completeIssues := scoreRecord(water, record{
		Status:       "completed",
		ReplyText:    "房间内有两瓶矿泉水，两瓶都是免费的。",
		Intent:       "hotel_info",
		KnowledgeHit: true,
	})
	if completeScore < 80 {
		t.Fatalf("complete water facts must pass: score=%d issues=%v", completeScore, completeIssues)
	}

	robot := findEvalScenario(t, "AA07")
	unsafeScore, unsafeIssues := scoreRecord(robot, record{
		Status:       "completed",
		ReplyText:    "有外卖机器人，可以送到房间。",
		Intent:       "hotel_info",
		KnowledgeHit: true,
	})
	if unsafeScore >= 80 || !issuesContain(unsafeIssues, "banned phrase") {
		t.Fatalf("unsupported robot delivery must fail: score=%d issues=%v", unsafeScore, unsafeIssues)
	}
	cautiousScore, cautiousIssues := scoreRecord(robot, record{
		Status:       "completed",
		ReplyText:    "有外卖机器人，具体能送到哪里需要以门店实际能力为准。",
		Intent:       "hotel_info",
		KnowledgeHit: true,
	})
	if cautiousScore < 80 {
		t.Fatalf("a cautious boundary statement must not be mistaken for a delivery promise: score=%d issues=%v", cautiousScore, cautiousIssues)
	}

	address := findEvalScenario(t, "AA05")
	addressScore, addressIssues := scoreRecord(address, record{
		Status:       "completed",
		ReplyText:    "外卖地址填丽斯未来酒店合肥南七店+房间号，比如305。",
		Intent:       "hotel_info",
		KnowledgeHit: true,
	})
	if addressScore < 80 {
		t.Fatalf("equivalent room-number format must pass: score=%d issues=%v", addressScore, addressIssues)
	}

	protocol := findEvalScenario(t, "AA08")
	leakedScore, leakedIssues := scoreRecord(protocol, record{
		Status:       "completed",
		ReplyText:    "[历史消息][AI客服] replyParts taskId：办公桌有合柴、麦田、艺林，同时有沙发的是合柴、艺林。",
		Intent:       "hotel_info",
		KnowledgeHit: true,
	})
	if leakedScore >= 80 || !issuesContain(leakedIssues, "[历史消息]") {
		t.Fatalf("internal markers must fail: score=%d issues=%v", leakedScore, leakedIssues)
	}
}

func TestContinuous50SafeSuiteHasFiftyAIRounds(t *testing.T) {
	selected, err := selectScenarioSuite(buildScenarios(1), "continuous50-safe")
	if err != nil {
		t.Fatalf("select continuous50-safe suite: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != "Q50S" || !selected[0].RecordEachTurn {
		t.Fatalf("unexpected continuous50-safe suite: %#v", selected)
	}
	sc := selected[0]
	aiTurns := 0
	voiceTurns := 0
	foundEightQuestions := false
	foundRoomTypeBackref := false
	foundAddressRepeat := false
	for i, item := range sc.Turns {
		if !item.WaitForAI {
			continue
		}
		aiTurns++
		if item.Type == enums.IMMessageTypeVoice {
			voiceTurns++
		}
		if item.NeedsHumanRoute == nil || *item.NeedsHumanRoute {
			t.Fatalf("turn %d is not safe for a continuous AI conversation: %#v", i+1, item)
		}
		if strings.Contains(item.Content, "八项") {
			foundEightQuestions = len(item.RequiredOutcomes) >= 13 && item.MaxReplyMessages == 3
		}
		if strings.Contains(item.Content, "那麦田呢") {
			foundRoomTypeBackref = true
		}
		if strings.Contains(item.Content, "外卖地址再说一遍") || strings.Contains(item.Content, "外卖地址再复述") {
			foundAddressRepeat = true
		}
	}
	if aiTurns != 50 {
		t.Fatalf("continuous50-safe AI rounds=%d want=50", aiTurns)
	}
	if voiceTurns < 3 || !foundEightQuestions || !foundRoomTypeBackref || !foundAddressRepeat {
		t.Fatalf("continuous50-safe coverage incomplete: voice=%d eight=%t roomBackref=%t addressRepeat=%t", voiceTurns, foundEightQuestions, foundRoomTypeBackref, foundAddressRepeat)
	}
	if got := countFactSlots(sc); got != 117 {
		t.Fatalf("continuous50-safe fact slots=%d want=117", got)
	}

	alias, err := selectScenarioSuite(buildScenarios(1), "continuous50")
	if err != nil || len(alias) != 1 || alias[0].ID != "Q50S" {
		t.Fatalf("select continuous50 alias: %#v err=%v", alias, err)
	}
}

func TestScoreRecordEnforcesMaximumReplyMessages(t *testing.T) {
	sc := scenario{MaxReplyMessages: 3}
	rec := record{
		Status:    "completed",
		ReplyText: "第一条<<NEXT_MESSAGE>>第二条<<NEXT_MESSAGE>>第三条<<NEXT_MESSAGE>>第四条",
	}
	score, issues := scoreRecord(sc, rec)
	if score >= 80 || !issuesContain(issues, "too many reply messages") {
		t.Fatalf("four messages must fail a three-message bound: score=%d issues=%v", score, issues)
	}
}

func TestScoreRecordAppliesLengthLimitPerCustomerMessage(t *testing.T) {
	shortPart := strings.Repeat("短", 150)
	withinLimit := record{
		Status: "completed",
		CommitMessages: []commitRecord{
			{Content: shortPart, Status: "sent"},
			{Content: shortPart, Status: "sent"},
		},
	}
	if score, issues := scoreRecord(scenario{}, withinLimit); issuesContain(issues, "too long") {
		t.Fatalf("combined reply length must not penalize individually short messages: score=%d issues=%v", score, issues)
	}

	tooLong := record{Status: "completed", ReplyText: strings.Repeat("长", 221)}
	if score, issues := scoreRecord(scenario{}, tooLong); !issuesContain(issues, "reply message 1 too long") {
		t.Fatalf("single oversized message must be reported: score=%d issues=%v", score, issues)
	}
}

func TestFactSlotStatsExcludeResourceAndCoverageRequirements(t *testing.T) {
	sc := scenario{RequiredOutcomes: []outcomeRequirement{
		textOutcome("停车入口", "昭潭路"),
		coverageOutcome("回答需说明入口", "入口"),
		resourceOutcome("定位和小程序", "location", "mini_program"),
	}}
	rec := record{
		ReplyText: "停车场入口在昭潭路。",
		CommitMessages: []commitRecord{
			{MessageType: "location", ResourceType: "location", Content: "[位置] 丽斯未来酒店", Status: "sent"},
			{MessageType: "mini_program", ResourceType: "mini_program", Content: "[小程序] e秒安心住", Status: "sent"},
		},
	}
	satisfied, expected := factSlotStats(sc, rec)
	if satisfied != 1 || expected != 1 {
		t.Fatalf("fact slot stats=%d/%d want=1/1", satisfied, expected)
	}
	for _, requirement := range sc.RequiredOutcomes {
		if !requiredOutcomeSatisfied(rec, requirement) {
			t.Fatalf("scoring requirement %q should still be enforced", requirement.Label)
		}
	}
}

func TestEquivalentProductionPhrasingsDoNotCreateFalseFailures(t *testing.T) {
	tests := []struct {
		name        string
		reply       string
		requirement outcomeRequirement
	}{
		{name: "air conditioner", reply: "所有房间都配了空调。", requirement: textOutcome("空调", "有空调", "配有空调", "配了空调", "都有空调")},
		{name: "face unlock", reply: "登记后刷脸开门，不用房卡。", requirement: textOutcome("人脸开门", "扫脸", "刷脸", "人脸")},
		{name: "free parking", reply: "停车是免费的。", requirement: textOutcome("停车费用", "停车免费", "免费停车", "停车是免费", "停车不收费")},
		{name: "room number format", reply: "丽斯未来酒店合肥南七店+房间号，比如305。", requirement: textOutcome("房间号格式", "楼层房间号", "对应楼层房间号", "房间号")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !requiredOutcomeSatisfied(record{ReplyText: tt.reply}, tt.requirement) {
				t.Fatalf("equivalent production phrasing should satisfy %q: %s", tt.requirement.Label, tt.reply)
			}
		})
	}
}

func TestAA03ScoringAcceptsNaturalEquivalentPhrasesAndRejectsRobotDeliveryPromises(t *testing.T) {
	sc := findEvalScenario(t, "AA03")
	base := "外卖地址填丽斯未来酒店合肥南七店+对应楼层房间号。布草是一客一换的。携程、抖音、美团各平台权益不同，%s"
	tests := []struct {
		name  string
		robot string
		price string
	}{
		{name: "provide robot service and compare before choosing", robot: "我们酒店提供外卖机器人服务。", price: "建议您对比后选择合适的。"},
		{name: "compare then choose", robot: "酒店有外卖机器人。", price: "可以对比后再选。"},
		{name: "compare and choose a suitable option", robot: "酒店有外卖机器人的。", price: "建议您对比后选合适的。"},
		{name: "compare price colloquially", robot: "酒店配有外卖机器人。", price: "建议您对比下价格再选。"},
		{name: "compare once before choosing", robot: "酒店有外卖机器人。", price: "建议您对比一下再选。"},
		{name: "compare once before selecting", robot: "酒店有外卖机器人。", price: "建议您比较一下再选择。"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := record{
				Status:       "completed",
				ReplyText:    tt.robot + fmt.Sprintf(base, tt.price),
				Intent:       "hotel_info",
				KnowledgeHit: true,
			}
			score, issues := scoreRecord(sc, rec)
			if score < 80 {
				t.Fatalf("equivalent AA03 reply must pass: score=%d issues=%v reply=%s", score, issues, rec.ReplyText)
			}
		})
	}

	unsafe := record{
		Status:       "completed",
		ReplyText:    "酒店提供外卖机器人服务，可以送到房间。" + fmt.Sprintf(base, "建议您对比后选择合适的。"),
		Intent:       "hotel_info",
		KnowledgeHit: true,
	}
	score, issues := scoreRecord(sc, unsafe)
	if score >= 80 || !issuesContain(issues, "banned phrase: 送到房间") {
		t.Fatalf("unsupported robot delivery promise must still fail: score=%d issues=%v", score, issues)
	}
}

func TestPriceComparisonOutcomeUsesCategorySemanticsAndRejectsNegation(t *testing.T) {
	requirement := priceComparisonOutcome()
	for _, reply := range []string{
		"建议对比各平台价格后再选合适的下单。",
		"您可以对比一下再选择。",
		"最好比较各个平台的权益后再决定。",
	} {
		if !requiredOutcomeSatisfied(record{ReplyText: reply}, requirement) {
			t.Fatalf("positive price comparison advice must pass: %s", reply)
		}
	}

	for _, prefix := range []string{"无需", "不用", "不必", "不能", "不要", "没必要", "没有必要", "不需要"} {
		for _, verb := range []string{"对比", "比较"} {
			reply := prefix + verb + "各平台价格，直接下单就行。"
			if requiredOutcomeSatisfied(record{ReplyText: reply}, requirement) {
				t.Fatalf("negated price comparison advice must fail: %s", reply)
			}
		}
	}
}

func TestRemainingFactSlotsKeepsContinuousSuiteDenominatorStable(t *testing.T) {
	sc := continuous50SafeScenario()
	remaining := remainingFactSlots(sc, []record{
		{ScenarioID: "Q50S-T01", FactSlotsExpected: 0},
		{ScenarioID: "Q50S-T02", FactSlotsExpected: 2},
		{ScenarioID: "Q50S-T03", FactSlotsExpected: 1},
	})
	if remaining != 114 {
		t.Fatalf("remaining fact slots=%d want=114", remaining)
	}
}

func TestPreparedMediaPayloadTriggersAIOnlyWhenMessageServiceCanConsumeIt(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{name: "understood text", payload: `{"mediaText":"早餐几点","mediaUnderstandingStatus":"understood"}`, want: true},
		{name: "understood summary", payload: `{"mediaSummary":"客户询问早餐","mediaUnderstandingStatus":"understood"}`, want: true},
		{name: "pending", payload: `{"mediaText":"早餐几点","mediaUnderstandingStatus":"pending"}`, want: false},
		{name: "empty", payload: `{"mediaUnderstandingStatus":"understood"}`, want: false},
		{name: "invalid", payload: `{`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := preparedMediaPayloadTriggersAI(tt.payload); got != tt.want {
				t.Fatalf("preparedMediaPayloadTriggersAI()=%v want=%v", got, tt.want)
			}
		})
	}
}

func findEvalScenario(t *testing.T, id string) scenario {
	t.Helper()
	for _, sc := range buildScenarios(1) {
		if sc.ID == id {
			return sc
		}
	}
	t.Fatalf("scenario %s not found", id)
	return scenario{}
}

func issuesContain(issues []string, want string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, want) {
			return true
		}
	}
	return false
}

func hasOutcomeLabel(requirements []outcomeRequirement, want string) bool {
	for _, requirement := range requirements {
		if requirement.Label == want {
			return true
		}
	}
	return false
}
