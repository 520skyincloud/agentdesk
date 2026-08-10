package executor

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
)

type testRuntimeTaskKnowledgeRetriever struct {
	results map[string]*retrievers.KnowledgeRetrieveResult
	errors  map[string]error
	started chan string
	release <-chan struct{}
	calls   atomic.Int32
}

func (r *testRuntimeTaskKnowledgeRetriever) KnowledgeBaseIDs() []int64 {
	return []int64{99}
}

func (r *testRuntimeTaskKnowledgeRetriever) RetrieveContextByOptions(_ context.Context, _ retrievers.KnowledgeRetrieveOptions, query string) (*retrievers.KnowledgeRetrieveResult, error) {
	r.calls.Add(1)
	if r.started != nil {
		r.started <- query
	}
	if r.release != nil {
		<-r.release
	}
	if err := r.errors[query]; err != nil {
		return nil, err
	}
	return r.results[query], nil
}

func TestRetrieveRuntimeTaskKnowledgeRunsIndependentQueriesConcurrently(t *testing.T) {
	plans := testKnowledgeTaskPlans()
	started := make(chan string, len(plans))
	release := make(chan struct{})
	retriever := &testRuntimeTaskKnowledgeRetriever{
		results: testKnowledgeTaskResults(plans),
		errors:  map[string]error{},
		started: started,
		release: release,
	}
	type result struct {
		outcome runtimeTaskKnowledgeOutcome
		err     error
	}
	done := make(chan result, 1)
	go func() {
		outcome, err := retrieveRuntimeTaskKnowledgeWithRetriever(context.Background(), RunInput{}, plans, nil, runtimeTaskBatchState{}, retriever)
		done <- result{outcome: outcome, err: err}
	}()

	seen := map[string]bool{}
	for range plans {
		select {
		case query := <-started:
			seen[query] = true
		case <-time.After(time.Second):
			t.Fatal("knowledge tasks did not start concurrently")
		}
	}
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatalf("retrieve task knowledge: %v", got.err)
	}
	if retriever.calls.Load() != int32(len(plans)) || len(seen) != len(plans) {
		t.Fatalf("knowledge calls=%d seen=%v", retriever.calls.Load(), seen)
	}
	if len(got.outcome.ActiveTaskPlans) != len(plans) || len(got.outcome.FailedTaskKeys) != 0 || got.outcome.Prefetched == nil {
		t.Fatalf("unexpected knowledge outcome: %#v", got.outcome)
	}
	for _, plan := range plans {
		if !strings.Contains(got.outcome.Prefetched.ContextText, plan.TaskKey) || !strings.Contains(got.outcome.Prefetched.ContextText, plan.Text) {
			t.Fatalf("merged context does not preserve task boundary for %s: %q", plan.TaskKey, got.outcome.Prefetched.ContextText)
		}
	}
}

func TestRetrieveRuntimeTaskKnowledgeKeepsSuccessfulTasksWhenOneFails(t *testing.T) {
	plans := testKnowledgeTaskPlans()
	retriever := &testRuntimeTaskKnowledgeRetriever{
		results: testKnowledgeTaskResults(plans),
		errors:  map[string]error{plans[1].Text: errors.New("fastgpt unavailable")},
	}
	outcome, err := retrieveRuntimeTaskKnowledgeWithRetriever(context.Background(), RunInput{}, plans, nil, runtimeTaskBatchState{}, retriever)
	if err != nil {
		t.Fatalf("retrieve task knowledge: %v", err)
	}
	if len(outcome.FailedTaskKeys) != 1 || outcome.FailedTaskKeys[0] != plans[1].TaskKey {
		t.Fatalf("failed task keys=%v", outcome.FailedTaskKeys)
	}
	if len(outcome.ActiveTaskPlans) != 2 || outcome.ActiveTaskPlans[0].TaskKey != plans[0].TaskKey || outcome.ActiveTaskPlans[1].TaskKey != plans[2].TaskKey {
		t.Fatalf("successful task plans were not preserved: %#v", outcome.ActiveTaskPlans)
	}
	if outcome.Prefetched == nil || strings.Contains(outcome.Prefetched.ContextText, plans[1].TaskKey) {
		t.Fatalf("failed task leaked into merged knowledge context: %#v", outcome.Prefetched)
	}
}

func testKnowledgeTaskPlans() []callbacks.ReplyTaskPlanTraceData {
	return []callbacks.ReplyTaskPlanTraceData{
		{TaskKey: "task-checkin", Intent: "hotel_info", SubIntent: "checkin_process", Text: "怎么办理入住", Output: "knowledge_text_reply"},
		{TaskKey: "task-coffee", Intent: "hotel_info", SubIntent: "service_facility", Text: "有咖啡吗", Output: "knowledge_text_reply"},
		{TaskKey: "task-parking", Intent: "hotel_info", SubIntent: "parking", Text: "停车场在哪里", Output: "knowledge_text_reply"},
	}
}

func testKnowledgeTaskResults(plans []callbacks.ReplyTaskPlanTraceData) map[string]*retrievers.KnowledgeRetrieveResult {
	results := make(map[string]*retrievers.KnowledgeRetrieveResult, len(plans))
	for _, plan := range plans {
		hit := rag.RetrieveResult{
			KnowledgeBaseID: 99,
			SourceRecordID:  plan.TaskKey,
			Content:         plan.Text + "的知识答案",
			Score:           0.9,
		}
		results[plan.Text] = &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{99},
			Query:            plan.Text,
			Hits:             []rag.RetrieveResult{hit},
			ContextResults:   []rag.RetrieveResult{hit},
			ContextText:      hit.Content,
		}
	}
	return results
}
