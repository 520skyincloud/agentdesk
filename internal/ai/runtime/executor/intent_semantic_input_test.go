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
	got := currentTurnTaskCandidates("房间有几瓶矿泉水，免费吗？")
	if len(got) != 1 || got[0] != "房间有几瓶矿泉水，免费吗" {
		t.Fatalf("expected one compound task candidate, got %#v", got)
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
