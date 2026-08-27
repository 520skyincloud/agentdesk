package executor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/tooling"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/pkg/usagex"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestConsumeAgentEventsIgnoresPlainGraphToolText(t *testing.T) {
	summary := &RunResult{
		Status:           "started",
		InvokedToolCodes: make([]string, 0),
	}
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				Role:     schema.Tool,
				ToolName: toolx.GraphHandoffConversation.Name,
				Message: &schema.Message{
					Content: "已为你转接人工客服，请稍候。，请稍候。",
				},
			},
		},
	})
	gen.Close()

	consumeAgentEvents(context.Background(), events, summary, nil, map[string]string{
		toolx.GraphHandoffConversation.Name: toolx.GraphHandoffConversation.Code,
	})

	if summary.ReplyText != "" {
		t.Fatalf("unexpected reply text: %q", summary.ReplyText)
	}
	if summary.Status != "completed" {
		t.Fatalf("unexpected summary status: %q", summary.Status)
	}
}

func TestConsumeAgentEventsUsesGraphToolResultReplyText(t *testing.T) {
	summary := &RunResult{
		Status:           "started",
		InvokedToolCodes: make([]string, 0),
	}
	payload, err := json.Marshal(tooling.ToolResult{
		Handled:     true,
		Terminal:    true,
		Action:      "off_hours_handoff",
		ReplyText:   "当前暂不在人工客服服务时间内，你可以先继续描述问题。",
		ShouldRetry: false,
	})
	if err != nil {
		t.Fatalf("marshal graph tool result: %v", err)
	}
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				Role:     schema.Tool,
				ToolName: toolx.GraphHandoffConversation.Name,
				Message: &schema.Message{
					Content: string(payload),
				},
			},
		},
	})
	gen.Send(&adk.AgentEvent{
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				Role: schema.Assistant,
				Message: &schema.Message{
					Content: "我再试一次转人工。",
				},
			},
		},
	})
	gen.Close()

	consumeAgentEvents(context.Background(), events, summary, nil, map[string]string{
		toolx.GraphHandoffConversation.Name: toolx.GraphHandoffConversation.Code,
	})

	if summary.ReplyText != "当前暂不在人工客服服务时间内，你可以先继续描述问题。" {
		t.Fatalf("unexpected reply text: %q", summary.ReplyText)
	}
	if summary.Status != "completed" {
		t.Fatalf("unexpected summary status: %q", summary.Status)
	}
}

func TestConsumeAgentEventsCollectsTokenUsage(t *testing.T) {
	summary := &RunResult{
		Status:           "started",
		InvokedToolCodes: make([]string, 0),
	}
	collector := callbacks.NewRuntimeTraceCollector()
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				Role: schema.Assistant,
				Message: &schema.Message{
					Content: "好的",
					ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{
						PromptTokens:     120,
						CompletionTokens: 8,
						TotalTokens:      128,
						PromptTokenDetails: schema.PromptTokenDetails{
							CachedTokens: 80,
						},
						CompletionTokensDetails: schema.CompletionTokensDetails{
							ReasoningTokens: 3,
						},
					}},
				},
			},
		},
	})
	gen.Close()

	consumeAgentEvents(context.Background(), events, summary, collector, nil)

	if summary.PromptTokens != 120 || summary.CompletionTokens != 8 || summary.TotalTokens != 128 || summary.CachedPromptTokens != 80 || summary.ReasoningTokens != 3 {
		t.Fatalf("unexpected usage summary: %+v", summary)
	}
	if collector.Data.Model.Usage.CachedPromptTokens != 80 {
		t.Fatalf("expected cached tokens in trace, got %d", collector.Data.Model.Usage.CachedPromptTokens)
	}
	if len(summary.ModelUsageCalls) != 1 || summary.ModelUsageCalls[0].PromptTokens != 120 || summary.ModelUsageCalls[0].CachedPromptTokens != 80 {
		t.Fatalf("expected one billable upstream usage call, got %+v", summary.ModelUsageCalls)
	}
}

func TestConsumeAgentEventsBindsUsageToCurrentGatewayReceipt(t *testing.T) {
	ctx, _ := usagex.WithCapture(context.Background())
	client := &http.Client{Transport: usagex.TrackingTransport{Base: generatedReplyTestRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				usagex.NewAPIRequestIDHeader: []string{"successful-request"},
			},
			Body: http.NoBody,
		}, nil
	})}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://model.invalid/generate", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("capture receipt: %v", err)
	}
	_ = resp.Body.Close()

	summary := &RunResult{Status: "started"}
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role: schema.Assistant,
		Message: &schema.Message{Content: "好的", ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{
			PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12,
		}}},
	}}})
	gen.Close()

	consumeAgentEvents(ctx, events, summary, nil, nil)

	if len(summary.ModelUsageCalls) != 1 || !summary.ModelUsageCalls[0].HasUsage || summary.ModelUsageCalls[0].GatewayReceiptOrdinal != 1 {
		t.Fatalf("usage must bind to the receipt visible at response time: %+v", summary.ModelUsageCalls)
	}
}

func TestConsumeAgentEventsReturnsGenerateExecutionErrorAfterCollectingUsage(t *testing.T) {
	summary := &RunResult{Status: "started", InvokedToolCodes: make([]string, 0)}
	collector := callbacks.NewRuntimeTraceCollector()
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{
		Err: errors.New("upstream connection reset"),
		Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
			Role: schema.Assistant,
			Message: &schema.Message{
				Content: "未完成的回复",
				ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{
					PromptTokens: 40, CompletionTokens: 3, TotalTokens: 43,
				}},
			},
		}},
	})
	gen.Close()

	err := consumeAgentEvents(context.Background(), events, summary, collector, nil)

	if !IsGeneratedReplyExecutionError(err) || errors.Is(err, ErrGeneratedReplyProtocol) {
		t.Fatalf("expected a distinguishable Generate execution error, got %v", err)
	}
	if summary.Status != "error" || !strings.Contains(summary.ErrorMessage, "connection reset") {
		t.Fatalf("expected the run to retain the upstream error, got %#v", summary)
	}
	if summary.TotalTokens != 43 || collector.Data.Model.Usage.TotalTokens != 43 {
		t.Fatalf("event errors must not discard measured usage, summary=%#v trace=%#v", summary, collector.Data.Model.Usage)
	}
}

func TestConsumeAgentEventsCleansExactInternalHistoryPrefix(t *testing.T) {
	summary := &RunResult{Status: "started", InvokedToolCodes: make([]string, 0)}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "矿泉水有几瓶", Output: "knowledge_text_reply"},
	}}
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role:    schema.Assistant,
		Message: &schema.Message{Content: "[历史消息][人工作答][2026-08-26 13:00:24] 房间内有两瓶矿泉水。"},
	}}})
	gen.Close()

	err := consumeAgentEvents(context.Background(), events, summary, collector, nil)

	if err != nil || summary.ReplyText != "房间内有两瓶矿泉水。" {
		t.Fatalf("exact internal history prefix must be removed safely, reply=%q err=%v", summary.ReplyText, err)
	}
}

func TestConsumeAgentEventsRejectsEmbeddedInternalHistoryMarker(t *testing.T) {
	summary := &RunResult{Status: "started", InvokedToolCodes: make([]string, 0)}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "矿泉水有几瓶", Output: "knowledge_text_reply"},
	}}
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role:    schema.Assistant,
		Message: &schema.Message{Content: "房间内有两瓶矿泉水。[历史消息][AI客服]停车免费。"},
	}}})
	gen.Close()

	err := consumeAgentEvents(context.Background(), events, summary, collector, nil)

	if summary.ReplyText != "" || !errors.Is(err, ErrGeneratedReplyProtocol) {
		t.Fatalf("embedded internal history marker must fail closed, reply=%q err=%v", summary.ReplyText, err)
	}
	if !collector.Data.Pipeline.Generate.BlockedInternalMarker {
		t.Fatalf("expected internal marker block to be recorded in trace")
	}
}

func TestConsumeAgentEventsKeepsEveryMultiQuestionGeneratePart(t *testing.T) {
	summary := &RunResult{Status: "started", InvokedToolCodes: make([]string, 0)}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "早餐有吗", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "停车免费吗", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "剃须刀在哪", Output: "knowledge_text_reply"},
	}}
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role:    schema.Assistant,
		Message: &schema.Message{Content: `{"replyParts":[{"taskId":"task-1","content":"早餐供应到9:30。"},{"taskId":"task-2","content":"停车免费。"},{"taskId":"task-3","content":"剃须刀可在自助区领取。"}]}`},
	}}})
	gen.Close()

	consumeAgentEvents(context.Background(), events, summary, collector, nil)

	for _, want := range []string{"早餐", "停车", "剃须刀"} {
		if !strings.Contains(summary.ReplyText, want) {
			t.Fatalf("multi-question Generate output lost %q: %q", want, summary.ReplyText)
		}
	}
	if strings.Count(summary.ReplyText, "<<NEXT_MESSAGE>>") != 2 {
		t.Fatalf("expected three ordered reply parts, got %q", summary.ReplyText)
	}
}

func TestConsumeAgentEventsUnwrapsSingleQuestionGenerateProtocol(t *testing.T) {
	summary := &RunResult{Status: "started", InvokedToolCodes: make([]string, 0)}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "有没有空调", Output: "knowledge_text_reply"},
	}}
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role: schema.Assistant,
		Message: &schema.Message{Content: "```json\n" +
			`{"replyParts":[{"taskId":"task-1","content":"房间配有空调。"}]}` + "\n```"},
	}}})
	gen.Close()

	consumeAgentEvents(context.Background(), events, summary, collector, nil)

	if summary.ReplyText != "房间配有空调。" {
		t.Fatalf("single-question protocol must be unwrapped before commit, got %q", summary.ReplyText)
	}
	if summary.Status != "completed" {
		t.Fatalf("unexpected summary status: %q", summary.Status)
	}
}

func TestConsumeAgentEventsSuppressesMalformedReplyProtocol(t *testing.T) {
	summary := &RunResult{Status: "started", InvokedToolCodes: make([]string, 0)}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "有没有空调", Output: "knowledge_text_reply"},
	}}
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role:    schema.Assistant,
		Message: &schema.Message{Content: `{"replyParts":[{"taskId":"task-1","content":"房间配有空调。"}]`},
	}}})
	gen.Close()

	err := consumeAgentEvents(context.Background(), events, summary, collector, nil)

	if summary.ReplyText != "" {
		t.Fatalf("malformed internal protocol must not reach the final reply, got %q", summary.ReplyText)
	}
	if !errors.Is(err, errGeneratedReplyProtocol) {
		t.Fatalf("malformed internal protocol must return a retryable executor error, got %v", err)
	}
	if summary.Status != "error" {
		t.Fatalf("malformed internal protocol must fail the executor run, got %q", summary.Status)
	}
}

func TestConsumeAgentEventsSuppressesBareHandoffToolCallText(t *testing.T) {
	summary := &RunResult{Status: "started", InvokedToolCodes: make([]string, 0)}
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{Role: schema.Assistant, Message: &schema.Message{Content: `handoff_to_human(reason: "客人在109摔倒")`}}}})
	gen.Close()

	consumeAgentEvents(context.Background(), events, summary, nil, nil)

	if summary.ReplyText != "" {
		t.Fatalf("expected bare tool text to be suppressed, got %q", summary.ReplyText)
	}
	if summary.Status != "fallback" {
		t.Fatalf("expected fallback status after suppressing bare tool text, got %q", summary.Status)
	}
}

func TestConsumeAgentEventsSuppressesGraphToolResultWhenReplyAlreadySent(t *testing.T) {
	summary := &RunResult{
		Status:           "started",
		InvokedToolCodes: make([]string, 0),
	}
	payload, err := json.Marshal(tooling.ToolResult{
		Handled:     true,
		Terminal:    true,
		Action:      "off_hours_handoff",
		ReplyText:   "当前暂不在人工客服服务时间内，你可以先继续描述问题。",
		ReplySent:   true,
		ShouldRetry: false,
	})
	if err != nil {
		t.Fatalf("marshal graph tool result: %v", err)
	}
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				Role:     schema.Tool,
				ToolName: toolx.GraphHandoffConversation.Name,
				Message: &schema.Message{
					Content: string(payload),
				},
			},
		},
	})
	gen.Send(&adk.AgentEvent{
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				Role: schema.Assistant,
				Message: &schema.Message{
					Content: "我再试一次转人工。",
				},
			},
		},
	})
	gen.Close()

	consumeAgentEvents(context.Background(), events, summary, nil, map[string]string{
		toolx.GraphHandoffConversation.Name: toolx.GraphHandoffConversation.Code,
	})

	if summary.ReplyText != "" {
		t.Fatalf("expected no committed reply because graph already sent it, got %q", summary.ReplyText)
	}
	if summary.Status != "completed" {
		t.Fatalf("unexpected summary status: %q", summary.Status)
	}
}

func TestConsumeAgentEventsCompletesGraphToolWithNoVisibleReply(t *testing.T) {
	summary := &RunResult{
		Status:           "started",
		InvokedToolCodes: make([]string, 0),
	}
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				Role:     schema.Tool,
				ToolName: toolx.GraphHandoffConversation.Name,
				Message: &schema.Message{
					Content: "",
				},
			},
		},
	})
	gen.Close()

	consumeAgentEvents(context.Background(), events, summary, nil, map[string]string{
		toolx.GraphHandoffConversation.Name: toolx.GraphHandoffConversation.Code,
	})

	if summary.ReplyText != "" {
		t.Fatalf("expected no reply text, got %q", summary.ReplyText)
	}
	if summary.Status != "completed" {
		t.Fatalf("unexpected summary status: %q", summary.Status)
	}
}
