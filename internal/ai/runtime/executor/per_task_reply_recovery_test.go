package executor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/cloudwego/eino/schema"
)

func TestBurstClarificationUsesWholeTurnAndKeepsIndependentAmbiguity(t *testing.T) {
	prompt := buildRuntimeIntentDetectUserPrompt(RunInput{
		UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "早餐几点"},
	}, adapter.HistoryBuildResult{}, nil)
	for _, rule := range []string{"先读完本轮全部 URef", "不再建立 interaction/clarify", "独立且仍有歧义的业务问题必须保留澄清"} {
		if !strings.Contains(prompt, rule) {
			t.Fatalf("missing whole-turn clarification rule %q", rule)
		}
	}
	plan := buildReplyPlan(callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: "hotel_info", SubIntent: "breakfast", Text: "早餐几点",
			ResolvedText: "早餐几点", SourceRefs: []string{"U2", "U1"}, NeedsKnowledge: true,
		}},
	}, callbacks.IntentPromptTraceData{})
	if plan.ReplyRequiredTaskCount != 1 || len(plan.TaskPlans[0].SourceRefs) != 2 {
		t.Fatalf("context source must not create another answer: %#v", plan)
	}
}

func TestJudgeBuildsFactsFromTaskScopedAnswer(t *testing.T) {
	prompt := knowledgeEvidenceJudgeSystemPrompt()
	for _, rule := range []string{"先形成当前任务的 answerText", "同一候选可以被多个 Task 使用", "不能把其他 Task 的答案复制进当前答案", "从该 answerText 提取 supportedFacts"} {
		if !strings.Contains(prompt, rule) {
			t.Fatalf("missing task-local answer contract %q", rule)
		}
	}
}

func TestLockedAnswerRecoversMalformedAnnotationWithoutChangingFact(t *testing.T) {
	for _, tc := range []struct {
		name, answer, statement, annotation string
	}{
		{"invoice", "可以开增值税电子普票或专票。", "可以开增值税电子普票或专票。", "增值税电子普或专票"},
		{"quantity", "房间有矿泉水。", "房间内有两瓶矿泉水，都是免费的。", "二瓶"},
		{"amount", "可以办理延迟退房。", "延迟退房收费30元。", "300元"},
		{"credential", "可以连接客房WiFi。", "客房WiFi密码是ab!12。", "ab12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := textReplyTaskGroup{
				TaskID: "T1", EvidenceLocked: true, AnswerText: &tc.answer,
				Facts: []replyFactRequirement{{FactID: "F1", Statement: tc.statement, CriticalValues: []string{tc.annotation}}},
			}
			got, err := renderLockedReplyContent(group)
			if err != nil || got != tc.statement {
				t.Fatalf("must retain complete Judge fact, not annotation or generic fallback: %q %v", got, err)
			}
			if tc.answer != tc.statement {
				part := generatedReplyPart{TaskID: "T1", Content: tc.answer, CoveredFactIDs: []string{"F1"}}
				if err := validateCoveredFacts(part, group); err == nil {
					t.Fatal("invalid annotation must not silently remove the full-fact requirement")
				}
			}
		})
	}
}

func TestSixAnswersRecoverOnlyInconsistentTasks(t *testing.T) {
	answers := []string{
		"附近可以去甲店吃饭。", "游玩可以去乙园。", "完成登记后刷脸开门。",
		"酒店可以免费停车。", "停车场有充电桩。", "可以开增值税电子普票或专票。",
	}
	statements := append([]string(nil), answers...)
	statements[3] = "酒店可以免费停车，入口在测试路。"
	values := []string{"甲店", "乙园", "刷脸", "测试路", "充电桩", "增值税电子普或专票"}
	plan := callbacks.ReplyPlanTraceData{}
	envelope := generatedReplyPartsEnvelope{}
	for i := range answers {
		id := string(rune('A' + i))
		plan.TaskPlans = append(plan.TaskPlans, callbacks.ReplyTaskPlanTraceData{
			TaskID: id, Intent: "hotel_info", OutputKind: "text", ReplyRequired: true,
			SelectedLayer: "store", SelectedCandidateIDs: []string{id + "C1"}, AnswerText: &answers[i],
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
				FactID: id + "F1", Statement: statements[i], CriticalValues: []string{values[i]},
			}},
		})
		envelope.ReplyParts = append(envelope.ReplyParts, generatedReplyPart{TaskID: id, CoveredFactIDs: []string{id + "F1"}})
	}
	raw, _ := json.Marshal(envelope)
	got, err := normalizeGeneratedReplyPartsResult(string(raw), plan, true)
	if err != nil || strings.Count(got, "<<NEXT_MESSAGE>>") != 2 {
		t.Fatalf("six tasks must complete without batch fallback: %q %v", got, err)
	}
	last := -1
	for _, expected := range statements {
		at := strings.Index(got, expected)
		if at <= last || strings.Count(got, expected) != 1 {
			t.Fatalf("missing, repeated or reordered answer %q: %q", expected, got)
		}
		last = at
	}
}

func TestGeneratedReplyFallbackPreservesValidatedSibling(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "social", Intent: "interaction", SubIntent: "thanks", OutputKind: "text", ReplyRequired: true},
		{TaskID: "water", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true,
			SelectedLayer: "store", SelectedCandidateIDs: []string{"WC1"},
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "WF1", Statement: "房间有两瓶矿泉水。"}}},
	}}
	summary := &RunResult{}
	attempts := 0
	result, err := runGeneratedReplyWithRecovery(context.Background(), nil, summary, collector, func() bool { return true },
		func(context.Context, []*schema.Message) error {
			attempts++
			raw := `{"replyParts":[{"taskId":"social","content":"别客气，祝您今天玩得开心。"}]}`
			if attempts == 2 {
				raw = `{"replyParts":[{"taskId":"water","content":"","coveredFactIds":["WF1"]}]}`
			}
			_, err := normalizeGeneratedReplyPartsResult(raw, collector.Data.Pipeline.ReplyPlan, true)
			return err
		})
	if err != nil || attempts > 2 || result.FallbackMode == "" {
		t.Fatalf("unexpected recovery: %+v attempts=%d err=%v", result, attempts, err)
	}
	for _, text := range []string{"别客气，祝您今天玩得开心。", "房间有两瓶矿泉水。"} {
		if strings.Count(summary.ReplyText, text) != 1 {
			t.Fatalf("validated answer must survive sibling recovery: %q", summary.ReplyText)
		}
	}
}

func TestMalformedTaskEnvelopeCannotAuthorizePartialReplies(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "interaction", OutputKind: "text", ReplyRequired: true,
	}}}
	for _, raw := range []string{
		`{"replyParts":[{"taskId":"T1","content":"您好"},{"taskId":"T1","content":"重复"}]}`,
		`{"replyParts":[{"taskId":"T1","content":"您好"},{"taskId":"unknown","content":"未知"}]}`,
	} {
		_, err := normalizeGeneratedReplyPartsResult(raw, plan, true)
		if !errors.Is(err, ErrGeneratedReplyProtocol) {
			t.Fatalf("unsafe envelope must remain a protocol error: %v", err)
		}
		var partial *generatedReplyTaskError
		if errors.As(err, &partial) {
			t.Fatal("ambiguous task ownership must not preserve partial replies")
		}
	}
}

func TestUnrecoverableLockedFactDoesNotExposeProtocol(t *testing.T) {
	answer := `{"replyParts":[{"taskId":"T1","content":"internal"}]}`
	group := textReplyTaskGroup{
		TaskID: "T1", EvidenceLocked: true, AnswerText: &answer,
		Facts: []replyFactRequirement{{FactID: "F1", Statement: answer}},
	}
	if text, err := renderLockedReplyContent(group); text != "" || !errors.Is(err, errLockedReplyEvidence) {
		t.Fatalf("invalid answer and facts must fail closed: %q %v", text, err)
	}
}
