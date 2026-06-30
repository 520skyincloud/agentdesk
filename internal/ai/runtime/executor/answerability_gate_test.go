package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/cloudwego/eino/schema"
)

type fakeKnowledgeContextRetriever struct {
	knowledgeBaseIDs []int64
	result           *retrievers.KnowledgeRetrieveResult
	err              error
	called           bool
}

func (r *fakeKnowledgeContextRetriever) KnowledgeBaseIDs() []int64 {
	return append([]int64(nil), r.knowledgeBaseIDs...)
}

func (r *fakeKnowledgeContextRetriever) RetrieveContextByOptions(ctx context.Context, opts retrievers.KnowledgeRetrieveOptions, query string) (*retrievers.KnowledgeRetrieveResult, error) {
	r.called = true
	if r.err != nil {
		return nil, r.err
	}
	if r.result != nil {
		return r.result, nil
	}
	return &retrievers.KnowledgeRetrieveResult{
		KnowledgeBaseIDs: append([]int64(nil), r.knowledgeBaseIDs...),
		Query:            query,
	}, nil
}

func newTestKnowledgePolicyGate(retriever knowledgeContextRetriever) *KnowledgeAnswerabilityGate {
	return &KnowledgeAnswerabilityGate{
		newRetriever: func(aiAgent models.AIAgent) knowledgeContextRetriever {
			return retriever
		},
	}
}

func newKnowledgePolicyRunInput(content string, knowledgeIDs string) RunInput {
	return RunInput{
		UserMessage: models.Message{Content: content},
		AIAgent: models.AIAgent{
			KnowledgeIDs:    knowledgeIDs,
			FallbackMode:    enums.AIAgentFallbackModeSuggestRetry,
			FallbackMessage: "我暂时没有找到足够准确的信息。你可以补充更具体的问题，我再继续帮你查。",
			AllowedMCPTools: "[]",
		},
		AIConfig: models.AIConfig{ModelName: "fake-model"},
	}
}

func messagesContainContent(messages []*schema.Message, needle string) bool {
	for _, message := range messages {
		if message != nil && strings.Contains(message.Content, needle) {
			return true
		}
	}
	return false
}

func TestKnowledgePolicyEvaluateInjectsNoContextInstructionWithoutFallback(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
		},
	})

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("你好", "1"),
		Summary:   &RunResult{},
		Collector: collector,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if state.FallbackReply != "" {
		t.Fatalf("expected no direct fallback, got %q", state.FallbackReply)
	}
	if state.SkipGate {
		t.Fatal("expected configured knowledge to inject policy, not skip")
	}
	if len(state.Decision.Instructions) != 1 {
		t.Fatalf("expected one no-context instruction, got %d", len(state.Decision.Instructions))
	}
	if !strings.Contains(state.Decision.Instructions[0].Content, "当前没有从知识库检索到可用资料") {
		t.Fatalf("unexpected no-context instruction: %q", state.Decision.Instructions[0].Content)
	}
	if !strings.Contains(state.Decision.Instructions[0].Content, "不得编造") {
		t.Fatalf("expected anti-hallucination policy, got %q", state.Decision.Instructions[0].Content)
	}
	if !strings.Contains(state.Decision.Instructions[0].Content, "图片/语音/文件等媒体理解结果") {
		t.Fatalf("expected media-aware no-context policy, got %q", state.Decision.Instructions[0].Content)
	}
	if !strings.Contains(state.Decision.Instructions[0].Content, "不要因为知识库未命中就直接输出固定兜底话术") {
		t.Fatalf("expected policy to avoid robotic fallback, got %q", state.Decision.Instructions[0].Content)
	}
	if collector.Data.Answerability.Status != answerabilityStatusNoContext {
		t.Fatalf("unexpected policy status: %q", collector.Data.Answerability.Status)
	}
}

func TestBuildRunMessagesContinuesAgentFlowWhenNoContext(t *testing.T) {
	summary := &RunResult{}
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
		},
	})

	messages := buildRunMessages(context.Background(), newKnowledgePolicyRunInput("你好", "1"), summary, nil, gate)

	if summary.ReplyText != "" {
		t.Fatalf("expected no early fallback reply, got %q", summary.ReplyText)
	}
	if !messagesContainContent(messages, "当前没有从知识库检索到可用资料") {
		t.Fatalf("expected no-context instruction in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "你好") {
		t.Fatalf("expected current user message to remain in messages: %#v", messages)
	}
}

func TestKnowledgePolicyEvaluateDoesNotFallbackWhenHitsHaveNoContextText(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, DocumentID: 10, ChunkID: 101, Content: "入住办理在小程序里。", Score: 0.2},
			},
		},
	})

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("你能干啥啊", "1"),
		Summary:   &RunResult{},
		Collector: collector,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.FallbackReply != "" {
		t.Fatalf("expected no direct fallback for weak hit, got %q", state.FallbackReply)
	}
	if len(state.Decision.Instructions) != 1 {
		t.Fatalf("expected one no-context instruction, got %d", len(state.Decision.Instructions))
	}
	if !strings.Contains(state.Decision.Instructions[0].Content, "先判断用户意图") {
		t.Fatalf("expected intent-first no-context policy, got %q", state.Decision.Instructions[0].Content)
	}
}

func TestKnowledgePolicyEvaluateInjectsGroundedInstructionAndContext(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, DocumentID: 10, ChunkID: 101, Content: "退款规则：订单发货前可以申请退款。", Score: 0.91},
			},
			ContextResults: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, DocumentID: 10, ChunkID: 101, Content: "退款规则：订单发货前可以申请退款。", Score: 0.91},
			},
			ContextText: "知识库片段：退款规则：订单发货前可以申请退款。",
			AnswerMode:  enums.KnowledgeAnswerModeStrict,
		},
	})

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("怎么退款", "1"),
		Summary:   &RunResult{},
		Collector: collector,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if state.FallbackReply != "" {
		t.Fatalf("expected no direct fallback, got %q", state.FallbackReply)
	}
	if len(state.Decision.Instructions) != 1 {
		t.Fatalf("expected one grounded instruction, got %d", len(state.Decision.Instructions))
	}
	if !strings.Contains(state.Decision.Instructions[0].Content, "知识库回答约束") {
		t.Fatalf("unexpected grounded instruction: %q", state.Decision.Instructions[0].Content)
	}
	if collector.Data.Answerability.Status != answerabilityStatusHasContext {
		t.Fatalf("unexpected policy status: %q", collector.Data.Answerability.Status)
	}
}

func TestBuildRunMessagesInjectsRetrievedContextWhenHasContext(t *testing.T) {
	summary := &RunResult{}
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, DocumentID: 10, ChunkID: 101, Content: "退款规则：订单发货前可以申请退款。", Score: 0.91},
			},
			ContextText: "知识库片段：退款规则：订单发货前可以申请退款。",
			AnswerMode:  enums.KnowledgeAnswerModeStrict,
		},
	})

	messages := buildRunMessages(context.Background(), newKnowledgePolicyRunInput("怎么退款", "1"), summary, nil, gate)

	if summary.ReplyText != "" {
		t.Fatalf("expected no fallback, got %q", summary.ReplyText)
	}
	if !messagesContainContent(messages, "知识库回答约束") {
		t.Fatalf("expected knowledge instruction in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "退款规则") {
		t.Fatalf("expected retrieved context in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "怎么退款") {
		t.Fatalf("expected current user message in messages: %#v", messages)
	}
}

func TestKnowledgePolicyEvaluateSkipsWhenNoKnowledgeConfigured(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{}
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(retriever)

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("你好", ""),
		Collector: collector,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if !state.SkipGate {
		t.Fatal("expected skip without knowledge")
	}
	if retriever.called {
		t.Fatal("expected retriever not to run without configured knowledge")
	}
	if collector.Data.Answerability.Status != answerabilityStatusSkipped {
		t.Fatalf("unexpected status: %q", collector.Data.Answerability.Status)
	}
}

func TestKnowledgePolicyEvaluateSkipsRuntimeActionIntent(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(retriever)

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("帮我转人工", "1"),
		Collector: collector,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if !state.SkipGate {
		t.Fatal("expected runtime action to skip knowledge policy")
	}
	if retriever.called {
		t.Fatal("expected retriever not to run for runtime action")
	}
	if collector.Data.Answerability.Status != answerabilityStatusSkipped {
		t.Fatalf("unexpected status: %q", collector.Data.Answerability.Status)
	}
}

func TestKnowledgePolicyEvaluateInjectsRetrievalErrorInstructionWithoutFallback(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		err:              errors.New("vector store unavailable"),
	})

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("怎么退款", "1"),
		Collector: collector,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if state.FallbackReply != "" {
		t.Fatalf("expected no direct fallback on retrieval error, got %q", state.FallbackReply)
	}
	if len(state.Decision.Instructions) != 1 {
		t.Fatalf("expected one retrieval-error instruction, got %d", len(state.Decision.Instructions))
	}
	if !strings.Contains(state.Decision.Instructions[0].Content, "知识库检索暂时不可用") {
		t.Fatalf("unexpected retrieval-error instruction: %q", state.Decision.Instructions[0].Content)
	}
	if collector.Data.Answerability.Status != answerabilityStatusUnanswerable {
		t.Fatalf("unexpected status: %q", collector.Data.Answerability.Status)
	}
	if collector.Data.Answerability.Reason != "knowledge retrieval failed" {
		t.Fatalf("unexpected reason: %q", collector.Data.Answerability.Reason)
	}
}

func TestBuildRunMessagesContinuesAgentFlowWhenRetrievalFails(t *testing.T) {
	summary := &RunResult{}
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		err:              errors.New("vector store unavailable"),
	})

	messages := buildRunMessages(context.Background(), newKnowledgePolicyRunInput("你好", "1"), summary, nil, gate)

	if summary.ReplyText != "" {
		t.Fatalf("expected no early fallback reply, got %q", summary.ReplyText)
	}
	if !messagesContainContent(messages, "知识库检索暂时不可用") {
		t.Fatalf("expected retrieval-error instruction in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "你好") {
		t.Fatalf("expected current user message to remain in messages: %#v", messages)
	}
}
