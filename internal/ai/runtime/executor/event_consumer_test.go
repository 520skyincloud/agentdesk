package executor

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/tooling"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/services"

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

	consumeAgentEvents(events, summary, nil, map[string]string{
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

	consumeAgentEvents(events, summary, nil, map[string]string{
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

	consumeAgentEvents(events, summary, collector, nil)

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

func TestConsumeAgentEventsSuppressesBareHandoffToolCallText(t *testing.T) {
	summary := &RunResult{Status: "started", InvokedToolCodes: make([]string, 0)}
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{Role: schema.Assistant, Message: &schema.Message{Content: `handoff_to_human(reason: "客人在109摔倒")`}}}})
	gen.Close()

	consumeAgentEvents(events, summary, nil, nil)

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

	consumeAgentEvents(events, summary, nil, map[string]string{
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

	consumeAgentEvents(events, summary, nil, map[string]string{
		toolx.GraphHandoffConversation.Name: toolx.GraphHandoffConversation.Code,
	})

	if summary.ReplyText != "" {
		t.Fatalf("expected no reply text, got %q", summary.ReplyText)
	}
	if summary.Status != "completed" {
		t.Fatalf("unexpected summary status: %q", summary.Status)
	}
}

func TestFinishRuntimeGenerationReturnsControlledModelError(t *testing.T) {
	summary := &RunResult{Status: "started", InvokedToolCodes: []string{}}
	collector := callbacks.NewRuntimeTraceCollector()
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{Err: errors.New("upstream unavailable")})
	gen.Close()

	err := finishRuntimeGeneration(events, summary, collector, nil, "reply-model", time.Now())
	if code, ok := services.AIReplyExecutionErrorCodeOf(err); !ok || code != services.AIReplyExecutionErrorGenerationFailed {
		t.Fatalf("expected controlled generation error, got %v", err)
	}
	if summary.Status != "error" || collector.Data.Error.Stage != "generate" {
		t.Fatalf("expected failed generation trace, summary=%+v trace=%+v", summary, collector.Data.Error)
	}
}

func TestFinishRuntimeGenerationRejectsEmptyOutput(t *testing.T) {
	summary := &RunResult{Status: "started", InvokedToolCodes: []string{}}
	collector := callbacks.NewRuntimeTraceCollector()
	events, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Close()

	err := finishRuntimeGeneration(events, summary, collector, nil, "reply-model", time.Now())
	if code, ok := services.AIReplyExecutionErrorCodeOf(err); !ok || code != services.AIReplyExecutionErrorEmptyOutput {
		t.Fatalf("expected controlled empty output error, got %v", err)
	}
	if summary.Status != "error" || collector.Data.Error.Stage != "validate" {
		t.Fatalf("expected failed validation trace, summary=%+v trace=%+v", summary, collector.Data.Error)
	}
}
