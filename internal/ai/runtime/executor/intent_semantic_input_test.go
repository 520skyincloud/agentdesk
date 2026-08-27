package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
)

func TestCurrentRuntimeIntentSemanticTextUsesCompleteVoiceTranscript(t *testing.T) {
	req := RunInput{UserMessage: models.Message{
		MessageType: enums.IMMessageTypeVoice,
		Content:     "voice.amr",
		Payload:     `{"mediaText":"早餐几点，停车免费吗，房间有几瓶矿泉水？","mediaSummary":"客户咨询早餐。","mediaUnderstandingStatus":"understood"}`,
	}}
	got := currentRuntimeIntentSemanticText(req)
	if got != "早餐几点，停车免费吗，房间有几瓶矿泉水？" {
		t.Fatalf("expected complete voice transcript, got %q", got)
	}
	if strings.Contains(got, "客户咨询早餐") {
		t.Fatalf("expected non-empty transcript to take precedence over summary, got %q", got)
	}
}

func TestCurrentRuntimeIntentSemanticTextFallsBackToVoiceSummaryWhenTranscriptEmpty(t *testing.T) {
	req := RunInput{UserMessage: models.Message{
		MessageType: enums.IMMessageTypeVoice,
		Content:     "voice.amr",
		Payload:     `{"mediaText":"  ","mediaSummary":"客户询问停车是否免费。","mediaUnderstandingStatus":"understood"}`,
	}}
	if got := currentRuntimeIntentSemanticText(req); got != "客户询问停车是否免费。" {
		t.Fatalf("expected voice summary only when transcript is empty, got %q", got)
	}
}

func TestCurrentRuntimeIntentSemanticTextKeepsMergedTurnWhenLastMessageIsVoice(t *testing.T) {
	content := utils.BuildRuntimeCustomerBurstEnvelope([]string{"1. [消息] 早餐几点", "2. [语音] 停车免费吗"})
	req := RunInput{UserMessage: models.Message{
		MessageType: enums.IMMessageTypeVoice,
		Content:     content,
		Payload:     `{"mediaText":"停车免费吗","mediaUnderstandingStatus":"understood"}`,
	}}
	got := currentRuntimeIntentSemanticText(req)
	if !strings.Contains(got, "早餐几点") || !strings.Contains(got, "停车免费吗") {
		t.Fatalf("expected the complete merged turn, got %q", got)
	}
}

func TestCurrentRuntimeIntentSemanticTextRejectsUnfinishedVoiceText(t *testing.T) {
	for _, status := range []string{"", "pending", "failed", "empty"} {
		req := RunInput{UserMessage: models.Message{
			MessageType: enums.IMMessageTypeVoice,
			Content:     "[语音] voice.amr\n语音内容是：早餐几点",
			Payload:     `{"mediaText":"早餐几点","mediaUnderstandingStatus":"` + status + `"}`,
		}}
		if got := currentRuntimeIntentSemanticText(req); got != "" {
			t.Fatalf("status %q must not enter intent, got %q", status, got)
		}
	}
}

func TestCurrentTurnTaskCandidatesDetectSingleParagraphQuestions(t *testing.T) {
	text := "早餐几点，停车免费吗，房间有几瓶矿泉水？"
	got := currentTurnTaskCandidates(text)
	if len(got) != 3 {
		t.Fatalf("expected three task candidates, got %#v", got)
	}
	if !isMultiQuestionCurrentTurn(text) {
		t.Fatal("expected single-paragraph questions to trigger multi-task coverage")
	}
}

func TestCurrentTurnTaskCandidatesKeepDependentAspectWithPreviousQuestion(t *testing.T) {
	for _, text := range []string{
		"房间有几瓶矿泉水，免费吗？",
		"房间里有几瓶矿泉水，都是免费的吗？",
		"房间有几瓶矿泉水，另外收费吗？",
	} {
		got := currentTurnTaskCandidates(text)
		if len(got) != 1 {
			t.Fatalf("expected one compound task candidate for %q, got %#v", text, got)
		}
	}
}

func TestCurrentTurnTaskCandidatesSkipsInstructionLeadButKeepsVoiceQuestions(t *testing.T) {
	text := "麻烦分别告诉我，房间空调有没有，矿泉水配几瓶收不收费，入住要怎么操作。"
	got := currentTurnTaskCandidates(text)
	want := []string{"房间空调有没有", "矿泉水配几瓶收不收费", "入住要怎么操作"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected three business questions without the instruction lead, got %#v", got)
	}
}

func TestRuntimeIntentRetrievalQueryOnlyTrimsConversationalLead(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "还有怎么办理入住", want: "怎么办理入住"},
		{input: "另外停车免费吗", want: "停车免费吗"},
		{input: "顺便问下外卖地址怎么填", want: "外卖地址怎么填"},
		{input: "顺便问早餐几点", want: "早餐几点"},
		{input: "再问一下房间有几瓶矿泉水", want: "房间有几瓶矿泉水"},
		{input: "还有没有空调", want: "还有没有空调"},
		{input: "还有多少间房", want: "还有多少间房"},
		{input: "还有两瓶矿泉水收费吗", want: "还有两瓶矿泉水收费吗"},
		{input: "另外收费吗", want: "另外收费吗"},
		{input: "刚才的入住方式再完整说一遍", want: "入住方式"},
		{input: "刚才那个开门方式再说一遍", want: "开门方式"},
		{input: "顺便问下开门方式，分别说，不要混在一起", want: "开门方式"},
		{input: "外卖地址，只要地址不要解释", want: "外卖地址"},
		{input: "外卖地址再说一遍，只要正确地址", want: "外卖地址"},
		{input: "还有开门方式", want: "开门方式"},
		{input: "另外发票流程", want: "发票流程"},
		{input: "再说一遍开门方式", want: "开门方式"},
		{input: "刚才那个再说一遍开门方式", want: "开门方式"},
		{input: "开门方式再说下", want: "开门方式"},
		{input: "开门方式重新说一下", want: "开门方式"},
		{input: "开门方式，分别说清楚，不要混在一起", want: "开门方式"},
		{input: "外卖地址，只回复地址", want: "外卖地址"},
		{input: "上面说的开门方式再说一遍", want: "开门方式"},
	}
	for _, tt := range tests {
		if got := runtimeIntentRetrievalQuery(tt.input); got != tt.want {
			t.Fatalf("runtimeIntentRetrievalQuery(%q)=%q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCurrentTurnTaskCandidatesSplitsExplicitSeparateLabels(t *testing.T) {
	got := currentTurnTaskCandidates("入住方式和开门方式分别说，不要混在一起。")
	want := []string{"入住方式", "开门方式"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected explicit separate labels to become atomic candidates, got %#v", got)
	}
}

func TestCurrentTurnTaskCandidatesSplitsIndependentLabelsButKeepsSharedSubjectFacts(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  []string
	}{
		{input: "入住方式、开门方式", want: []string{"入住方式", "开门方式"}},
		{input: "办公桌、沙发都有吗", want: []string{"办公桌、沙发都有吗"}},
		{input: "矿泉水数量和费用分别说", want: []string{"矿泉水数量和费用分别说"}},
		{input: "WiFi账号和密码分别说", want: []string{"WiFi账号和密码分别说"}},
		{input: "和平饭店地址和停车位置分别说", want: []string{"和平饭店地址", "停车位置"}},
		{input: "早餐时间和和平广场位置分别说", want: []string{"早餐时间", "和平广场位置"}},
	} {
		got := currentTurnTaskCandidates(tt.input)
		if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
			t.Fatalf("currentTurnTaskCandidates(%q)=%#v, want %#v", tt.input, got, tt.want)
		}
	}
}

func TestCurrentTurnTaskCandidatesKeepsStandaloneReplayForContextResolution(t *testing.T) {
	got := currentTurnTaskCandidates("刚才那个再说一遍")
	if len(got) != 1 || got[0] != "刚才那个再说一遍" {
		t.Fatalf("standalone replay must reach Intent for context resolution, got %#v", got)
	}
}

func TestCurrentTurnTaskCandidatesAttachOutputConstraintsToPreviousQuestion(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  string
	}{
		{input: "外卖地址再说一遍，只要正确地址", want: "外卖地址再说一遍，只要正确地址"},
		{input: "外卖地址怎么填？只说正确地址", want: "外卖地址怎么填，只说正确地址"},
		{input: "再说一遍，只回复地址", want: "再说一遍，只回复地址"},
		{input: "外卖地址怎么填？仅回复地址", want: "外卖地址怎么填，仅回复地址"},
		{input: "外卖地址怎么填？仅正确地址", want: "外卖地址怎么填，仅正确地址"},
	} {
		got := currentTurnTaskCandidates(tt.input)
		if len(got) != 1 || got[0] != tt.want {
			t.Fatalf("currentTurnTaskCandidates(%q)=%#v, want one constrained task %q", tt.input, got, tt.want)
		}
	}
}

func TestCurrentTurnTaskCandidatesKeepSelfContainedOnlyQuestionsIndependent(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  []string
	}{
		{input: "早餐几点？只要身份证可以入住吗？", want: []string{"早餐几点", "只要身份证可以入住吗"}},
		{input: "早餐几点？只要身份证就可以入住？", want: []string{"早餐几点", "只要身份证就可以入住"}},
		{input: "早餐几点？只需要身份证是否可以入住？", want: []string{"早餐几点", "只需要身份证是否可以入住"}},
		{input: "早餐几点？仅身份证能不能入住？", want: []string{"早餐几点", "仅身份证能不能入住"}},
	} {
		got := currentTurnTaskCandidates(tt.input)
		if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
			t.Fatalf("currentTurnTaskCandidates(%q)=%#v, want %#v", tt.input, got, tt.want)
		}
		if !isMultiQuestionCurrentTurn(tt.input) {
			t.Fatalf("self-contained only-question must keep multi-question coverage: %q", tt.input)
		}
	}
}

func TestCurrentTurnTaskCandidatesKeepsEllipticalFollowUpsWithTheirSubject(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  string
	}{
		{input: "酒店有早餐吗？几点开始？", want: "酒店有早餐吗，几点开始"},
		{input: "发票怎么开？多久能下载？", want: "发票怎么开，多久能下载"},
		{input: "有早餐吗？在哪里吃？", want: "有早餐吗，在哪里吃"},
		{input: "停车收费吗？怎么收费？", want: "停车收费吗，怎么收费"},
	} {
		got := currentTurnTaskCandidates(tt.input)
		if len(got) != 1 || got[0] != tt.want {
			t.Fatalf("currentTurnTaskCandidates(%q)=%#v, want one combined subject task %q", tt.input, got, tt.want)
		}
	}
}

func TestRuntimeIntentGenericFollowUpDoesNotHideSelfContainedQuestions(t *testing.T) {
	for _, input := range []string{"几点退房", "怎么投屏", "如何开门", "早餐在哪里吃", "发票多久能下载", "只要身份证可以入住吗"} {
		if isDependentIntentTaskClause(input) {
			t.Fatalf("self-contained question must not be treated as an elliptical follow-up: %q", input)
		}
		if isRuntimeIntentOutputConstraintClause(input) {
			t.Fatalf("self-contained question must not be treated as an output constraint: %q", input)
		}
	}
}

func TestCurrentTurnTaskCandidatesSplitTaskLikeEnumerationOnly(t *testing.T) {
	got := currentTurnTaskCandidates("早餐几点、停车免费吗、发票怎么开")
	if len(got) != 3 {
		t.Fatalf("expected three enumerated tasks, got %#v", got)
	}
	combined := currentTurnTaskCandidates("办公桌、沙发都有吗")
	if len(combined) != 1 || combined[0] != "办公桌、沙发都有吗" {
		t.Fatalf("expected one combined facilities task, got %#v", combined)
	}
}

func TestCurrentTurnIntentSourceTextsKeepPhysicalMultilineBoundaries(t *testing.T) {
	envelope := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息] 第一行问题\n第二行仍属于同一条消息",
		"2. [语音] 早餐几点\n停车免费吗",
	})
	sources := currentTurnIntentSourceTexts(currentTurnDisplayText(envelope))
	if len(sources) != 2 {
		t.Fatalf("expected two physical sources, got %#v", sources)
	}
	if sources[0] != "第一行问题\n第二行仍属于同一条消息" || sources[1] != "早餐几点\n停车免费吗" {
		t.Fatalf("expected multiline content preserved inside each source, got %#v", sources)
	}
}

func TestRuntimeIntentPromptDoesNotRepeatBurstSourcesFromMediaHistory(t *testing.T) {
	testCases := []struct {
		name    string
		current models.Message
		history adapter.HistoryBuildResult
		texts   []string
	}{
		{
			name: "text_then_voice",
			current: models.Message{
				MessageType: enums.IMMessageTypeVoice,
				Content: utils.BuildRuntimeCustomerBurstEnvelope([]string{
					"1. [消息] 早餐几点",
					"2. [语音] 停车免费吗",
				}),
				Payload: `{"mediaText":"停车免费吗","mediaUnderstandingStatus":"understood"}`,
			},
			history: adapter.HistoryBuildResult{RawItems: []models.Message{{
				SenderType:  enums.IMSenderTypeCustomer,
				MessageType: enums.IMMessageTypeText,
				Content:     "早餐几点",
			}}},
			texts: []string{"早餐几点", "停车免费吗"},
		},
		{
			name: "voice_then_text",
			current: models.Message{
				MessageType: enums.IMMessageTypeText,
				Content: utils.BuildRuntimeCustomerBurstEnvelope([]string{
					"1. [语音] 房间有几瓶矿泉水",
					"2. [消息] 这两瓶免费吗",
				}),
			},
			history: adapter.HistoryBuildResult{RawItems: []models.Message{{
				SenderType:  enums.IMSenderTypeCustomer,
				MessageType: enums.IMMessageTypeVoice,
				Content:     "voice.amr",
				Payload:     `{"mediaText":"房间有几瓶矿泉水","mediaUnderstandingStatus":"understood"}`,
			}}},
			texts: []string{"房间有几瓶矿泉水", "这两瓶免费吗"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			prompt := buildRuntimeIntentDetectUserPrompt(RunInput{UserMessage: testCase.current}, testCase.history, nil)
			for _, expected := range testCase.texts {
				if count := strings.Count(prompt, expected); count != 1 {
					t.Fatalf("expected %q exactly once, got %d occurrences in %q", expected, count, prompt)
				}
			}
			if strings.Contains(prompt, "上下文中的媒体理解") {
				t.Fatalf("burst-contained media must not be repeated as media context: %q", prompt)
			}
		})
	}
}

func TestPlainMultilineSingleQuestionDoesNotForceMultiTaskCoverage(t *testing.T) {
	text := "房间里有没有空调\n只是想确认一下设施"
	if isMultiQuestionCurrentTurn(text) {
		t.Fatalf("one physical multiline question must not be treated as multiple tasks: %q", text)
	}
}

func TestSingleQuestionVoiceDoesNotTriggerMultiQuestionCoverage(t *testing.T) {
	req := RunInput{UserMessage: models.Message{
		MessageType: enums.IMMessageTypeVoice,
		Content:     "voice.amr",
		Payload:     `{"mediaText":"早餐几点？","mediaSummary":"客户询问早餐。","mediaUnderstandingStatus":"understood"}`,
	}}
	semanticText := currentRuntimeIntentSemanticText(req)
	if got := currentTurnTaskCandidates(semanticText); len(got) != 1 || got[0] != "早餐几点" {
		t.Fatalf("expected one voice task candidate, got %#v", got)
	}
	if isMultiQuestionCurrentTurn(semanticText) {
		t.Fatal("expected a single voice question not to trigger multi-question coverage")
	}
	prompt := buildRuntimeIntentDetectUserPrompt(req, adapter.HistoryBuildResult{}, nil)
	if strings.Contains(prompt, "【当前轮多任务覆盖】") {
		t.Fatalf("single voice question must not receive multi-question instructions: %q", prompt)
	}
}

func TestRuntimeIntentPromptTreatsVoiceLikeText(t *testing.T) {
	transcript := "早餐几点，停车免费吗，房间有几瓶矿泉水？"
	req := RunInput{UserMessage: models.Message{
		MessageType: enums.IMMessageTypeVoice,
		Content:     "voice.amr",
		Payload:     `{"mediaText":"` + transcript + `","mediaSummary":"客户咨询早餐。","mediaUnderstandingStatus":"understood"}`,
	}}
	prompt := buildRuntimeIntentDetectUserPrompt(req, adapter.HistoryBuildResult{}, nil)
	for _, expected := range []string{
		"U1: 早餐几点，停车免费吗，房间有几瓶矿泉水？",
		"【当前轮多任务覆盖】",
		"C1: 早餐几点",
		"C2: 停车免费吗",
		"C3: 房间有几瓶矿泉水",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("expected voice intent prompt to contain %q, got %q", expected, prompt)
		}
	}
	if strings.Contains(prompt, "U1: voice.amr") || strings.Contains(prompt, "语音摘要是") {
		t.Fatalf("voice filename or lossy summary must not replace the transcript: %q", prompt)
	}
	if count := strings.Count(prompt, transcript); count != 1 {
		t.Fatalf("expected the current voice transcript exactly once in prompt, got %d occurrences: %q", count, prompt)
	}
}
