package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
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

func TestRetrieveRuntimeTaskKnowledgeGroupsTasksUsingSameTopKnowledgeRecord(t *testing.T) {
	plans := []callbacks.ReplyTaskPlanTraceData{
		{TaskKey: "task-coffee", Intent: "hotel_info", Text: "想喝咖啡了", Output: "knowledge_text_reply"},
		{TaskKey: "task-coffee-location", Intent: "hotel_info", Text: "咖啡在哪", Output: "knowledge_text_reply"},
		{TaskKey: "task-parking", Intent: "hotel_info", Text: "停车场在哪", Output: "knowledge_text_reply"},
	}
	coffeeHit := rag.RetrieveResult{KnowledgeBaseID: 99, SourceRecordID: "coffee-record", Content: "咖啡在洗衣房", Score: 0.9}
	parkingHit := rag.RetrieveResult{KnowledgeBaseID: 99, SourceRecordID: "parking-record", Content: "停车入口在昭潭路", Score: 0.9}
	retriever := &testRuntimeTaskKnowledgeRetriever{
		results: map[string]*retrievers.KnowledgeRetrieveResult{
			plans[0].Text: {Hits: []rag.RetrieveResult{coffeeHit}, ContextResults: []rag.RetrieveResult{coffeeHit}, ContextText: coffeeHit.Content},
			plans[1].Text: {Hits: []rag.RetrieveResult{coffeeHit}, ContextResults: []rag.RetrieveResult{coffeeHit}, ContextText: coffeeHit.Content},
			plans[2].Text: {Hits: []rag.RetrieveResult{parkingHit}, ContextResults: []rag.RetrieveResult{parkingHit}, ContextText: parkingHit.Content},
		},
		errors: map[string]error{},
	}
	outcome, err := retrieveRuntimeTaskKnowledgeWithRetriever(context.Background(), RunInput{}, plans, nil, runtimeTaskBatchState{}, retriever)
	if err != nil {
		t.Fatalf("retrieve grouped task knowledge: %v", err)
	}
	if len(outcome.ActiveTaskPlans) != 3 {
		t.Fatalf("active plans=%#v", outcome.ActiveTaskPlans)
	}
	coffeeGroup := outcome.ActiveTaskPlans[0].AnswerGroup
	if coffeeGroup == "" || outcome.ActiveTaskPlans[1].AnswerGroup != coffeeGroup {
		t.Fatalf("coffee tasks did not share an answer group: %#v", outcome.ActiveTaskPlans)
	}
	if outcome.ActiveTaskPlans[2].AnswerGroup != "" {
		t.Fatalf("unrelated parking task was grouped: %#v", outcome.ActiveTaskPlans)
	}
}

func TestRuntimeKnowledgeAnswerGroupRequiresFullEvidenceSet(t *testing.T) {
	// 契约 4.13：首条命中相同但完整命中集合不同，不得合并成一组。
	shared := rag.RetrieveResult{KnowledgeBaseID: 99, SourceRecordID: "meta-question", Content: "元问题", Score: 0.95}
	parking := rag.RetrieveResult{KnowledgeBaseID: 99, SourceRecordID: "parking-exact", Content: "停车入口", Score: 0.8}
	quiet := rag.RetrieveResult{KnowledgeBaseID: 99, SourceRecordID: "quiet-room", Content: "安静房规则", Score: 0.8}
	groupParking := runtimeKnowledgeAnswerGroup(&retrievers.KnowledgeRetrieveResult{Hits: []rag.RetrieveResult{shared, parking}, ContextText: "x"})
	groupQuiet := runtimeKnowledgeAnswerGroup(&retrievers.KnowledgeRetrieveResult{Hits: []rag.RetrieveResult{shared, quiet}, ContextText: "x"})
	if groupParking == "" || groupQuiet == "" {
		t.Fatal("groups must be derivable")
	}
	if groupParking == groupQuiet {
		t.Fatalf("first-hit overlap must not merge different evidence sets: %s", groupParking)
	}

	// 完整命中集合一致（顺序不同也一致）时才允许同组。
	groupParkingAgain := runtimeKnowledgeAnswerGroup(&retrievers.KnowledgeRetrieveResult{Hits: []rag.RetrieveResult{parking, shared}, ContextText: "x"})
	if groupParking != groupParkingAgain {
		t.Fatalf("identical evidence sets must produce identical group keys: %s vs %s", groupParking, groupParkingAgain)
	}
}

func TestBuildRuntimeEvidenceBundleServiceRequestNoContextDoesNotAutoHandoff(t *testing.T) {
	// 知识无命中不是人工业务决定；只能由能力路由明确拒绝、澄清或执行。
	items := []runtimeTaskKnowledgeItem{
		{TaskKey: "t-change-room", Intent: "service_request", Query: "换1203", Status: enums.AIReplyTurnTaskKnowledgeStatusNoHit},
	}
	_, _, actionCodes := buildRuntimeEvidenceBundle(RunInput{}, items, nil)
	if got := actionCodes["t-change-room"]; got != "" {
		t.Fatalf("knowledge no-context must not auto hand off, got %q", got)
	}
}

func TestBuildRuntimeEvidenceBundleInfoNoContextDoesNotHandOff(t *testing.T) {
	// 要信息（hotel_info）且知识库无答案 → 不转人工，继续追问澄清。
	items := []runtimeTaskKnowledgeItem{
		{TaskKey: "t-wifi", Intent: "hotel_info", Query: "wifi怎么连", Status: enums.AIReplyTurnTaskKnowledgeStatusNoHit},
	}
	_, _, actionCodes := buildRuntimeEvidenceBundle(RunInput{}, items, nil)
	if got := actionCodes["t-wifi"]; got != "" {
		t.Fatalf("expected hotel_info no_context not to hand off, got %q", got)
	}
}

func TestBuildRuntimeEvidenceBundleServiceRequestHitWithoutHandoffKeywordAnswersDirectly(t *testing.T) {
	// 要动作 + 知识库命中但 top-1 不含"转接" → 直接按知识回答，不转人工。
	hit := rag.RetrieveResult{KnowledgeBaseID: 99, SourceRecordID: "bed", Content: "本店不提供加床", Score: 0.9}
	items := []runtimeTaskKnowledgeItem{
		{
			TaskKey: "t-bed", Intent: "service_request", Query: "加床",
			Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
			Result: &retrievers.KnowledgeRetrieveResult{
				KnowledgeBaseIDs: []int64{99}, Hits: []rag.RetrieveResult{hit},
				ContextResults: []rag.RetrieveResult{hit}, ContextText: hit.Content,
			},
		},
	}
	_, _, actionCodes := buildRuntimeEvidenceBundle(RunInput{}, items, nil)
	if got := actionCodes["t-bed"]; got != "" {
		t.Fatalf("expected service_request with concrete answer not to hand off, got %q", got)
	}
}

func TestBuildRuntimeEvidenceArtifactsReservesAddressFactSlot(t *testing.T) {
	db := setupRuntimeIntentConfigTestDB(t)
	store := &models.Store{
		ID: 77, TenantID: 1, StoreCode: "evidence-address-store", Name: "证据边界酒店",
		Address: "安徽省合肥市蜀山区测试路88号", Status: enums.StatusOk,
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	instance := &models.WxWorkProtocolInstance{
		ID: 88, TenantID: 1, Guid: "evidence-address-instance", StoreID: store.ID,
		AIReplyEnabled: true, Status: enums.StatusOk,
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ConversationRouteState{
		TenantID: 1, ConversationID: 7, StoreID: store.ID, WxWorkInstanceID: instance.ID,
	}).Error; err != nil {
		t.Fatal(err)
	}

	hits := make([]rag.RetrieveResult, 0, runtimeEvidenceMaxItems)
	for index := 1; index <= runtimeEvidenceMaxItems; index++ {
		hits = append(hits, rag.RetrieveResult{
			SourceRecordID: fmt.Sprintf("address-source-%02d", index),
			Title:          "外卖地址说明", Content: fmt.Sprintf("地址相关知识片段 %02d", index), Score: 0.9,
		})
	}
	artifacts := buildRuntimeEvidenceArtifacts(RunInput{
		Conversation: models.Conversation{ID: 7, TenantID: 1, StoreID: store.ID},
	}, []runtimeTaskKnowledgeItem{{
		TaskKey: "t-address", Intent: "hotel_info", SubIntent: "address_for_delivery", Query: "点外卖地址填哪里",
		Status: enums.AIReplyTurnTaskKnowledgeStatusHit,
		Result: &retrievers.KnowledgeRetrieveResult{
			Hits: hits, ContextResults: hits,
		},
	}}, nil)
	if artifacts.Quality == nil {
		t.Fatal("expected evidence_bundle.v2")
	}
	if got := len(artifacts.Quality.Items); got != runtimeEvidenceMaxItems {
		t.Fatalf("knowledge evidence plus store fact must stay within schema max: got %d", got)
	}
	knowledgeCount := 0
	storeFactCount := 0
	for _, item := range artifacts.Quality.Items {
		switch item.SourceType {
		case "fastgpt":
			knowledgeCount++
		case "store_fact":
			storeFactCount++
			if item.Ref != "S1" || item.FactKey != "store.address" || item.Content != store.Address {
				t.Fatalf("unexpected authoritative store fact: %+v", item)
			}
		}
	}
	if knowledgeCount != runtimeEvidenceMaxItems-1 || storeFactCount != 1 {
		t.Fatalf("expected 23 FastGPT items plus one store fact, got knowledge=%d storeFacts=%d", knowledgeCount, storeFactCount)
	}

	plan := contracts.ReplyPlanV4{
		SchemaVersion: contracts.ReplyPlanV4SchemaVersion, TurnVersion: 1,
		PlanFingerprint: strings.Repeat("a", 64), ShouldGenerate: false,
		Tasks: []contracts.ReplyPlanTaskV4{}, ReplyGroups: []contracts.ReplyPlanGroupV4{},
		GlobalConstraints: contracts.ReplyPlanGlobalV4{
			MaxReplyParts: 0, MaxQuestionsPerPart: 1, ForbiddenClaims: []string{},
		},
	}
	if err := validateRuntimeReplyPlanV4Contract(plan, *artifacts.Quality, contracts.ActionLedgerV1{}, nil); err != nil {
		t.Fatalf("bounded evidence must satisfy embedded schema: %v", err)
	}

	overflow := *artifacts.Quality
	overflow.Items = append(append([]contracts.EvidenceItemV2(nil), artifacts.Quality.Items...), artifacts.Quality.Items[0])
	overflow.Items[len(overflow.Items)-1].Ref = "K24"
	if err := validateRuntimeReplyPlanV4Contract(plan, overflow, contracts.ActionLedgerV1{}, nil); err == nil {
		t.Fatal("oversized evidence bundle must be rejected before Generate")
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

func TestRuntimeTaskKnowledgeQueryUsesTaskTextDirectly(t *testing.T) {
	// 检索 query 直接用 task.text 原话，不再用 subIntent 锚点改写。
	plan := callbacks.ReplyTaskPlanTraceData{Text: "怎么把门打开", SubIntent: "door_access"}
	if got := runtimeTaskKnowledgeQuery(plan); got != "怎么把门打开" {
		t.Fatalf("expected task text unchanged, got %q", got)
	}
}

func TestRuntimeTaskKnowledgeQueryFallsBackToSubIntent(t *testing.T) {
	plan := callbacks.ReplyTaskPlanTraceData{SubIntent: "door_access"}
	if got := runtimeTaskKnowledgeQuery(plan); got != "door_access" {
		t.Fatalf("expected subIntent fallback, got %q", got)
	}
}

func TestSplitMultiTopicClauses(t *testing.T) {
	got := splitMultiTopicClauses("我饿了 有啥吃的推荐没 以及明天要去附近玩 你知道哪里好玩吗 还有啊 我怎么把门打开啊")
	if len(got) != 3 {
		t.Fatalf("expected 3 clauses, got %d: %#v", len(got), got)
	}
	if got[0] != "我饿了 有啥吃的推荐没" || got[2] != "我怎么把门打开啊" {
		t.Fatalf("unexpected clauses: %#v", got)
	}
}

func TestRedistributeMultiTopicClauses(t *testing.T) {
	full := "我饿了 有啥吃的推荐没 以及明天要去附近玩 你知道哪里好玩吗 还有啊 我怎么把门打开啊"
	plans := []callbacks.ReplyTaskPlanTraceData{
		{TaskKey: "t1", Sequence: 1, Text: full},
		{TaskKey: "t2", Sequence: 2, Text: full},
		{TaskKey: "t3", Sequence: 3, Text: full},
	}
	got := redistributeMultiTopicClauses(plans)
	if got[0].Text != "我饿了 有啥吃的推荐没" || got[2].Text != "我怎么把门打开啊" {
		t.Fatalf("redistribute failed: %#v", got)
	}
}

func TestRedistributeKeepsAlreadySplitPlans(t *testing.T) {
	plans := []callbacks.ReplyTaskPlanTraceData{
		{TaskKey: "t1", Sequence: 1, Text: "附近有什么好吃的"},
		{TaskKey: "t2", Sequence: 2, Text: "怎么把门打开"},
	}
	got := redistributeMultiTopicClauses(plans)
	if got[0].Text != "附近有什么好吃的" || got[1].Text != "怎么把门打开" {
		t.Fatalf("should keep already split plans: %#v", got)
	}
}
