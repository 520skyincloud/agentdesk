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
	resultsByQuery   map[string]*retrievers.KnowledgeRetrieveResult
	err              error
	called           bool
	queries          []string
}

func (r *fakeKnowledgeContextRetriever) KnowledgeBaseIDs() []int64 {
	return append([]int64(nil), r.knowledgeBaseIDs...)
}

func (r *fakeKnowledgeContextRetriever) RetrieveContextByOptions(ctx context.Context, opts retrievers.KnowledgeRetrieveOptions, query string) (*retrievers.KnowledgeRetrieveResult, error) {
	r.called = true
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	if r.resultsByQuery != nil {
		if result, ok := r.resultsByQuery[query]; ok {
			return result, nil
		}
	}
	if r.result != nil {
		return r.result, nil
	}
	return &retrievers.KnowledgeRetrieveResult{
		KnowledgeBaseIDs: append([]int64(nil), r.knowledgeBaseIDs...),
		Query:            query,
	}, nil
}

func TestKnowledgePolicyRetrievesEachBurstQuestion(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		resultsByQuery: map[string]*retrievers.KnowledgeRetrieveResult{
			"能开专票不": {
				KnowledgeBaseIDs: []int64{1},
				RawHits: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 1001, Title: "发票原始候选", Content: "发票原始候选", Score: 0.99},
				},
				Hits: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 101, Title: "发票", Content: "可以开电子专票，退房后在小程序申请。", Score: 0.95},
				},
				ContextResults: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 101, Title: "发票", Content: "可以开电子专票，退房后在小程序申请。", Score: 0.95},
				},
				ContextText: "可以开电子专票，退房后在小程序申请。",
				AnswerMode:  enums.KnowledgeAnswerModeStrict,
			},
			"WiFi是哪个": {
				KnowledgeBaseIDs: []int64{1},
				RawHits: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 1002, Title: "WiFi原始候选", Content: "WiFi原始候选", Score: 0.98},
				},
				Hits: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 102, Title: "WiFi", Content: "WiFi 名称是 LISI，密码看房间桌牌。", Score: 0.93},
				},
				ContextResults: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 102, Title: "WiFi", Content: "WiFi 名称是 LISI，密码看房间桌牌。", Score: 0.93},
				},
				ContextText: "WiFi 名称是 LISI，密码看房间桌牌。",
				AnswerMode:  enums.KnowledgeAnswerModeStrict,
			},
		},
	}
	gate := newTestKnowledgePolicyGate(retriever)
	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("客人刚才连续发了几条消息，请一起理解，不要只回复最后一句：\n能开专票不\nWiFi是哪个", "1"),
		Summary: &RunResult{},
		Intent:  hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !stringSliceSetEqual(retriever.queries, []string{"能开专票不", "WiFi是哪个"}) {
		t.Fatalf("expected retrieval for each current burst question, got %#v", retriever.queries)
	}
	if state.RetrieveResult == nil {
		t.Fatalf("expected retrieval result, got nil")
	}
	if len(state.RetrieveResult.RawHits) != 2 {
		t.Fatalf("expected merged raw hits for both burst questions, got %#v", state.RetrieveResult.RawHits)
	}
	if !strings.Contains(state.RetrieveResult.ContextText, "能开专票不") || !strings.Contains(state.RetrieveResult.ContextText, "WiFi是哪个") {
		t.Fatalf("expected merged context to label each question, got %q", state.RetrieveResult.ContextText)
	}
}

func TestMergeRuntimeKnowledgeQueriesBackfillsIntentMissedBurstQuestions(t *testing.T) {
	query := "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 早餐有吗\n2. [消息] 停车免费吗\n3. [消息] 剃须刀在哪"
	got := mergeRuntimeKnowledgeQueries(query, []string{"剃须刀在哪"}, nil)
	want := []string{"早餐有吗", "停车免费吗", "剃须刀在哪"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected uncovered burst questions to be restored in customer order, got %#v", got)
	}
}

func TestMergeRuntimeKnowledgeQueriesDoesNotTurnPureContextIntoTask(t *testing.T) {
	query := "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 好困啊\n2. [消息] 有没有咖啡"
	got := mergeRuntimeKnowledgeQueries(query, []string{"有没有咖啡"}, nil)
	want := []string{"有没有咖啡"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected pure context to remain attached to the actual question, got %#v", got)
	}
}

func TestMergeRuntimeKnowledgeQueriesSkipsExplicitResourceTasks(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		knowledgeTask string
		resourceTask  string
	}{
		{
			name:          "location resource before breakfast knowledge",
			query:         "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 定位发我\n2. [消息] 早餐几点",
			knowledgeTask: "早餐几点",
			resourceTask:  "定位发我",
		},
		{
			name:          "knowledge before repeated location resource",
			query:         "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [语音] 洗衣房在哪\n2. [消息] 定位再发我",
			knowledgeTask: "洗衣房在哪",
			resourceTask:  "定位再发我",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{
				{Intent: "hotel_info", Text: tt.knowledgeTask, NeedsKnowledge: true},
				{Intent: "hotel_variable", Text: tt.resourceTask, NeedsResource: true, ResourceAction: "provide_location"},
			}}
			got := mergeRuntimeKnowledgeQueries(
				tt.query,
				knowledgeQueriesFromIntentTasks(intent),
				nonKnowledgeQueriesFromIntentTasks(intent),
			)
			want := []string{tt.knowledgeTask}
			if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("expected only the knowledge task to be retrieved, got %#v", got)
			}
		})
	}
}

func TestMergeRuntimeKnowledgeQueriesFiltersResourceWhenIntentMissedKnowledgeTask(t *testing.T) {
	query := "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 定位发我\n2. [消息] 早餐几点"
	got := mergeRuntimeKnowledgeQueries(query, nil, []string{"定位发我"})
	want := []string{"早餐几点"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected the missed knowledge question to be restored without retrieving the resource task, got %#v", got)
	}
}

func TestMergeRuntimeKnowledgeQueriesKeepsSingleLineKnowledgeResidual(t *testing.T) {
	tests := []struct {
		query       string
		resource    string
		wantQueries []string
	}{
		{query: "定位发我，早餐几点", resource: "定位发我", wantQueries: []string{"早餐几点"}},
		{query: "定位发我，早餐几点", resource: "定位", wantQueries: []string{"早餐几点"}},
		{query: "把入住小程序发我，空调坏了怎么办", resource: "把入住小程序发我", wantQueries: []string{"空调坏了怎么办"}},
		{query: "把入住小程序发我，空调坏了怎么办", resource: "入住小程序", wantQueries: []string{"空调坏了怎么办"}},
	}
	for _, tt := range tests {
		got := mergeRuntimeKnowledgeQueries(tt.query, nil, []string{tt.resource})
		if strings.Join(got, "\x00") != strings.Join(tt.wantQueries, "\x00") {
			t.Fatalf("expected residual knowledge queries %#v for %q, got %#v", tt.wantQueries, tt.query, got)
		}
	}
}

func TestRebuildRuntimeKnowledgeReplyPlanUsesActualQuestionOrder(t *testing.T) {
	tests := []struct {
		name      string
		plan      callbacks.ReplyPlanTraceData
		questions []runtimeKnowledgeQuestionResult
		pending   []runtimeKnowledgeQuestionDisposition
		want      []string
	}{
		{
			name: "restored deferred question appears before planned answer",
			plan: callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
				{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
			}},
			questions: []runtimeKnowledgeQuestionResult{
				{TaskID: "T1", Query: "空调坏了，我住1302"},
				{TaskID: "T2", Query: "顺便问早餐几点"},
			},
			pending: []runtimeKnowledgeQuestionDisposition{{TaskID: "T1", Query: "空调坏了，我住1302", NeedsHandoff: true}},
			want:    []string{"顺便问早餐几点"},
		},
		{
			name: "restored answer appears before planned deferred question",
			plan: callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
				{Intent: "service_request", SubIntent: "air_conditioner", Text: "空调坏了，我住1302", Output: "knowledge_text_reply"},
			}},
			questions: []runtimeKnowledgeQuestionResult{
				{TaskID: "T1", Query: "顺便问早餐几点"},
				{TaskID: "T2", Query: "空调坏了，我住1302"},
			},
			pending: []runtimeKnowledgeQuestionDisposition{{TaskID: "T2", Query: "空调坏了，我住1302", NeedsHandoff: true}},
			want:    []string{"顺便问早餐几点"},
		},
		{
			name: "intent task order differs from customer question order",
			plan: callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
				{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
				{Intent: "service_request", SubIntent: "air_conditioner", Text: "空调坏了，我住1302", Output: "knowledge_text_reply"},
			}},
			questions: []runtimeKnowledgeQuestionResult{
				{TaskID: "T1", Query: "空调坏了，我住1302"},
				{TaskID: "T2", Query: "顺便问早餐几点"},
			},
			pending: []runtimeKnowledgeQuestionDisposition{{TaskID: "T1", Query: "空调坏了，我住1302", NeedsHandoff: true}},
			want:    []string{"顺便问早餐几点"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rebuildRuntimeKnowledgeReplyPlan(tt.plan, tt.questions, tt.pending, true)
			texts := make([]string, 0, len(got.TaskPlans))
			for _, task := range got.TaskPlans {
				if runtimeReplyTaskUsesKnowledge(task) {
					texts = append(texts, task.Text)
				}
			}
			if strings.Join(texts, "\x00") != strings.Join(tt.want, "\x00") {
				t.Fatalf("expected active knowledge tasks %#v, got %#v", tt.want, texts)
			}
		})
	}
}

func TestRebuildRuntimeKnowledgeReplyPlanRestoresEveryAnswerableBurstQuestion(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", SubIntent: "supplies_self_help", Text: "剃须刀在哪", Output: "knowledge_text_reply"},
	}}
	questions := []runtimeKnowledgeQuestionResult{
		{TaskID: "T1", Query: "早餐有吗"},
		{TaskID: "T2", Query: "停车免费吗"},
		{TaskID: "T3", Query: "剃须刀在哪"},
	}
	got := rebuildRuntimeKnowledgeReplyPlan(plan, questions, nil, false)
	texts := make([]string, 0, len(got.TaskPlans))
	for _, task := range got.TaskPlans {
		if runtimeReplyTaskUsesKnowledge(task) {
			texts = append(texts, task.Text)
		}
	}
	want := []string{"早餐有吗", "停车免费吗", "剃须刀在哪"}
	if strings.Join(texts, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected restored tasks in customer order %#v, got %#v", want, texts)
	}
}

func TestRebuildRuntimeKnowledgeReplyPlanPreservesBlankServiceRequestTask(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "service_request", SubIntent: "air_conditioner", Output: "knowledge_text_reply"},
	}}
	questions := []runtimeKnowledgeQuestionResult{{TaskID: "T1", Query: "空调坏了"}}
	got := rebuildRuntimeKnowledgeReplyPlan(plan, questions, nil, false)
	if len(got.TaskPlans) != 1 {
		t.Fatalf("expected one rebuilt task, got %#v", got.TaskPlans)
	}
	task := got.TaskPlans[0]
	if task.Intent != "service_request" || task.SubIntent != "air_conditioner" || task.Text != "空调坏了" {
		t.Fatalf("expected the original service request semantics to survive query binding, got %#v", task)
	}
}

func TestKnowledgePolicyPromotesTopExactHandoffDirective(t *testing.T) {
	top := rag.RetrieveResult{
		KnowledgeBaseID: 1,
		SourceRecordID:  "toilet-blocked",
		Title:           "马桶堵了怎么办",
		Content:         "问题：马桶堵了怎么办\n答案：转接",
		Score:           0.98,
	}
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits:             []rag.RetrieveResult{top},
			ContextResults:   []rag.RetrieveResult{top},
			ContextText:      top.Content,
			AnswerMode:       enums.KnowledgeAnswerModeStrict,
		},
	}
	summary := &RunResult{}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetActionLedger(buildInitialActionLedger(hotelInfoIntent()))
	state, err := newTestKnowledgePolicyGate(retriever).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("马桶堵了", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !summary.handoffDirective || summary.handoffDirectiveSource != "knowledge_top_answer" {
		t.Fatalf("expected exact top answer to request handoff, got %+v", summary)
	}
	if state.RetrieveResult == nil || strings.Contains(state.RetrieveResult.ContextText, "转接") {
		t.Fatalf("expected internal directive to stay out of Generate context, got %#v", state.RetrieveResult)
	}
	if !actionLedgerContainsAction(collector.Data.ActionLedger.RequestedActions, "human_route") {
		t.Fatalf("expected handoff request in action ledger, got %#v", collector.Data.ActionLedger)
	}
}

func TestKnowledgePolicyKeepsRoomNumberAnswerAndIgnoresLowerRankedDirective(t *testing.T) {
	top := rag.RetrieveResult{
		KnowledgeBaseID: 1,
		SourceRecordID:  "room-number",
		Title:           "设备故障",
		Content:         "问题：空调坏了怎么办\n答案：请告诉我房间号，我先确认是哪一间房。",
		Score:           0.98,
	}
	lower := rag.RetrieveResult{
		KnowledgeBaseID: 1,
		SourceRecordID:  "legacy-transfer",
		Title:           "其他服务",
		Content:         "问题：其他服务\n答案：转接",
		Score:           0.80,
	}
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits:             []rag.RetrieveResult{top, lower},
			ContextResults:   []rag.RetrieveResult{top, lower},
			ContextText:      top.Content + "\n" + lower.Content,
			AnswerMode:       enums.KnowledgeAnswerModeStrict,
		},
	}
	summary := &RunResult{}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.SetActionLedger(buildInitialActionLedger(hotelInfoIntent()))
	state, err := newTestKnowledgePolicyGate(retriever).Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("空调坏了", "1"),
		Summary:   summary,
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if summary.handoffDirective {
		t.Fatal("lower-ranked transfer directive must not bypass the room-number reply")
	}
	if state.RetrieveResult == nil || !strings.Contains(state.RetrieveResult.ContextText, "房间号") {
		t.Fatalf("expected normal room-number answer to remain available, got %#v", state.RetrieveResult)
	}
	if strings.Contains(state.RetrieveResult.ContextText, "转接") {
		t.Fatalf("expected lower-ranked internal directive to stay out of Generate context, got %q", state.RetrieveResult.ContextText)
	}
	if actionLedgerContainsAction(collector.Data.ActionLedger.RequestedActions, "human_route") {
		t.Fatalf("ordinary room-number clarification must not request handoff, got %#v", collector.Data.ActionLedger)
	}
}

func TestKnowledgePolicyDoesNotUseHardcodedSupplyFallback(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	gate := newTestKnowledgePolicyGate(retriever)
	_, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("能不能送两瓶水到房间", "1"),
		Summary: &RunResult{},
		Intent:  hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !retriever.called {
		t.Fatal("expected supply request to go through knowledge/runtime path")
	}
}

func TestKnowledgePolicyDoesNotLetSupplyFastPathHideMixedKnowledgeQuestions(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		resultsByQuery: map[string]*retrievers.KnowledgeRetrieveResult{
			"wifi和停车都发我一下": {
				KnowledgeBaseIDs: []int64{1},
				Hits: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 199, Title: "错域", Content: "在公司介绍模式里，如果用户问早餐、停车这类门店服务，可以先说明这里主要回答公司、品牌、展厅、加盟和AI方案；具体门店服务再由现场工作人员确认。", Score: 0.98},
					{KnowledgeBaseID: 1, ChunkID: 200, Title: "停车异常", Content: "车辆出场或闸口问题需要门店工作人员协助处理，请联系门店管家或前台。", Score: 0.96},
					{KnowledgeBaseID: 1, ChunkID: 201, Title: "WiFi", Content: "WiFi 名称是 LISI，密码看房间桌牌。", Score: 0.94},
					{KnowledgeBaseID: 1, ChunkID: 202, Title: "停车", Content: "门店有免费地上停车场。", Score: 0.91},
				},
				ContextResults: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 199, Title: "错域", Content: "在公司介绍模式里，如果用户问早餐、停车这类门店服务，可以先说明这里主要回答公司、品牌、展厅、加盟和AI方案；具体门店服务再由现场工作人员确认。", Score: 0.98},
					{KnowledgeBaseID: 1, ChunkID: 200, Title: "停车异常", Content: "车辆出场或闸口问题需要门店工作人员协助处理，请联系门店管家或前台。", Score: 0.96},
					{KnowledgeBaseID: 1, ChunkID: 201, Title: "WiFi", Content: "WiFi 名称是 LISI，密码看房间桌牌。", Score: 0.94},
					{KnowledgeBaseID: 1, ChunkID: 202, Title: "停车", Content: "门店有免费地上停车场。", Score: 0.91},
				},
				ContextText: "WiFi 名称是 LISI，密码看房间桌牌。\n门店有免费地上停车场。",
				AnswerMode:  enums.KnowledgeAnswerModeStrict,
			},
			"房间没纸巾": {
				KnowledgeBaseIDs: []int64{1},
				Hits: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 203, Title: "用品", Content: "纸巾在1020对面的洗衣房，可以自取。", Score: 0.95},
				},
				ContextResults: []rag.RetrieveResult{
					{KnowledgeBaseID: 1, ChunkID: 203, Title: "用品", Content: "纸巾在1020对面的洗衣房，可以自取。", Score: 0.95},
				},
				ContextText: "纸巾在1020对面的洗衣房，可以自取。",
				AnswerMode:  enums.KnowledgeAnswerModeStrict,
			},
		},
	}
	gate := newTestKnowledgePolicyGate(retriever)
	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("客人刚才连续发了几条消息，请一起理解，不要只回复最后一句：\nwifi和停车都发我一下\n房间没纸巾", "1"),
		Summary: &RunResult{},
		Intent:  hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !stringSliceSetEqual(retriever.queries, []string{"wifi和停车都发我一下", "房间没纸巾"}) {
		t.Fatalf("expected retrieval for each current burst question, got %#v", retriever.queries)
	}
	if state.RetrieveResult == nil {
		t.Fatalf("expected retrieval result, got nil")
	}
	if !strings.Contains(state.RetrieveResult.ContextText, "WiFi 名称是 LISI") || !strings.Contains(state.RetrieveResult.ContextText, "纸巾在1020") {
		t.Fatalf("expected merged context from each burst question, got %q", state.RetrieveResult.ContextText)
	}
}

func TestKnowledgePolicyUsesFastPathWhenMediaFollowUpHasNoUnderstanding(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	gate := newTestKnowledgePolicyGate(retriever)
	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("刚才发的语音你听懂了吗", "1"),
		Summary: &RunResult{},
		Intent:  mediaUnderstandingIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !state.SkipGate {
		t.Fatal("missing media context should skip knowledge and let the model respond from instruction")
	}
	if len(state.Decision.Instructions) == 0 || !strings.Contains(state.Decision.Instructions[0].Content, "媒体上下文状态") {
		t.Fatalf("expected missing media instruction, got %#v", state.Decision.Instructions)
	}
	if retriever.called {
		t.Fatal("missing media context should not call knowledge retriever")
	}
}

func TestKnowledgePolicyKeepsModelPathWhenMediaUnderstandingExists(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	gate := newTestKnowledgePolicyGate(retriever)
	_, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request: newKnowledgePolicyRunInput("刚才发的语音你听懂了吗", "1"),
		Summary: &RunResult{},
		Intent:  mediaUnderstandingIntent(),
		Messages: []*schema.Message{
			schema.UserMessage("[语音]\n语音内容是：确认确认"),
		},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
}

func TestKnowledgePolicyKeepsModelPathWhenImageUnderstandingExists(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	gate := newTestKnowledgePolicyGate(retriever)
	req := newKnowledgePolicyRunInput("你看不到我的照片吗", "1")
	_, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request: req,
		Summary: &RunResult{},
		Intent:  mediaUnderstandingIntent(),
		Messages: []*schema.Message{
			schema.UserMessage("[图片]\n图片内容是：图片中可见一桌菜、果粒橙和保温桶。"),
		},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
}

func newTestKnowledgePolicyGate(retriever knowledgeContextRetriever) *KnowledgeAnswerabilityGate {
	return &KnowledgeAnswerabilityGate{
		newRetriever: func(aiAgent models.AIAgent) knowledgeContextRetriever {
			return retriever
		},
	}
}

func stringSliceSetEqual(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[string]int, len(want))
	for _, item := range want {
		counts[item]++
	}
	for _, item := range got {
		if counts[item] == 0 {
			return false
		}
		counts[item]--
	}
	return true
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

func hotelInfoIntent() callbacks.IntentTraceData {
	return callbacks.IntentTraceData{PrimaryIntent: "hotel_info", MatchedIntentCode: "hotel_info", NeedsKnowledge: true, ShouldReply: true}
}

func socialConfirmIntent() callbacks.IntentTraceData {
	return callbacks.IntentTraceData{PrimaryIntent: "interaction", MatchedIntentCode: "interaction", ShouldReply: true}
}

func mediaUnderstandingIntent() callbacks.IntentTraceData {
	return callbacks.IntentTraceData{PrimaryIntent: "interaction", MatchedIntentCode: "interaction", SubIntent: "media_context_follow_up", ShouldReply: true}
}

func humanRiskIntent() callbacks.IntentTraceData {
	return callbacks.IntentTraceData{PrimaryIntent: "human_complaint_risk", MatchedIntentCode: "human_complaint_risk", NeedsHumanRoute: true, ShouldReply: true}
}

func messagesContainContent(messages []*schema.Message, needle string) bool {
	for _, message := range messages {
		if message != nil && strings.Contains(message.Content, needle) {
			return true
		}
	}
	return false
}

func TestKnowledgePolicyEvaluateUsesDeterministicConversationalReply(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(retriever)

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("你好", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent:    socialConfirmIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !state.SkipGate {
		t.Fatal("expected conversational intent to skip knowledge and continue to model")
	}
	if retriever.called {
		t.Fatal("expected retriever not to run for conversational intent")
	}
	if collector.Data.Answerability.Status != answerabilityStatusSkipped {
		t.Fatalf("unexpected policy status: %q", collector.Data.Answerability.Status)
	}
	if collector.Data.Answerability.Reason != "intent does not require knowledge" {
		t.Fatalf("unexpected policy reason: %q", collector.Data.Answerability.Reason)
	}
}

func TestKnowledgePolicyEvaluateInjectsNoContextInstructionForKnowledgeQuestion(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
		},
	})

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点", "1"),
		Summary:   &RunResult{},
		Intent:    hotelInfoIntent(),
		Collector: collector,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
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
	if !strings.Contains(state.Decision.Instructions[0].Content, "不要因为知识库未命中就输出固定兜底话术") {
		t.Fatalf("expected policy to avoid robotic fallback, got %q", state.Decision.Instructions[0].Content)
	}
	assertNoFixedFallbackSource(t, state.Decision.Instructions[0].Content)
	if !strings.Contains(state.Decision.Instructions[0].Content, "否则进入接待路由") {
		t.Fatalf("expected actionable no-context policy, got %q", state.Decision.Instructions[0].Content)
	}
	if state.Input.Summary == nil || !state.Input.Summary.handoffDirective || state.Input.Summary.handoffDirectiveSource != "knowledge_no_context" {
		t.Fatalf("expected no-context business question to request handoff, got %#v", state.Input.Summary)
	}
	if collector.Data.Answerability.Status != answerabilityStatusNoContext {
		t.Fatalf("unexpected policy status: %q", collector.Data.Answerability.Status)
	}
}

func TestKnowledgePolicyRetrievesWifiInsteadOfSkippingAsAction(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
		},
	}
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(retriever)

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("房间网连不上", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.SkipGate {
		t.Fatal("wifi/network question should retrieve configured knowledge, not skip as an action")
	}
	if !retriever.called {
		t.Fatal("expected wifi/network question to call knowledge retriever")
	}
	if got := strings.Join(retriever.queries, "|"); !strings.Contains(got, "房间网连不上") {
		t.Fatalf("expected original wifi query to be retrieved, got %q", got)
	}
	if collector.Data.Answerability.Status != answerabilityStatusNoContext {
		t.Fatalf("unexpected policy status: %q", collector.Data.Answerability.Status)
	}
	if len(state.Decision.Instructions) != 1 || !strings.Contains(state.Decision.Instructions[0].Content, "WiFi") {
		t.Fatalf("expected wifi-aware no-context instruction, got %#v", state.Decision.Instructions)
	}
}

func assertNoFixedFallbackSource(t *testing.T, content string) {
	t.Helper()
	badFragments := []string{
		"我先记下",
		"帮你确认下",
		"帮您确认下",
		"让同事确认",
		"稍后让同事跟进",
		"可参考但不要死抄",
		"确实无法确认时才短句回复",
	}
	for _, bad := range badFragments {
		if strings.Contains(content, bad) {
			t.Fatalf("knowledge instruction leaks fixed fallback source %q: %q", bad, content)
		}
	}
}

func TestKnowledgeGuardInstructionsDoNotLeakFixedFallbackSources(t *testing.T) {
	for name, instruction := range map[string]string{
		"no_context":      buildKnowledgeNoContextInstruction(),
		"retrieval_error": buildKnowledgeRetrievalErrorInstruction(),
		"strict":          buildKnowledgeRuntimeInstruction(enums.KnowledgeAnswerModeStrict),
		"assist":          buildKnowledgeRuntimeInstruction(enums.KnowledgeAnswerModeAssist),
	} {
		t.Run(name, func(t *testing.T) {
			assertNoFixedFallbackSource(t, instruction)
		})
	}
}

func TestBuildRunMessagesMarksHandoffWhenNoContext(t *testing.T) {
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 200, MatchMode: "keyword", Keywords: "早餐", NeedsKnowledge: true, Status: enums.StatusOk})
	summary := &RunResult{}
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
		},
	})

	messages := make([]*schema.Message, 0)
	req := newKnowledgePolicyRunInput("早餐几点", "1")
	outcome := appendRetrievedContext(context.Background(), req, hotelInfoIntent(), summary, nil, gate, &messages)
	messages = append(messages, schema.UserMessage(req.UserMessage.Content))
	if outcome.AnswerabilityStatus != answerabilityStatusNoContext {
		t.Fatalf("unexpected answerability status: %q", outcome.AnswerabilityStatus)
	}

	if summary.ReplyText != "" {
		t.Fatalf("expected no early fallback reply, got %q", summary.ReplyText)
	}
	if !summary.handoffDirective || summary.handoffDirectiveSource != "knowledge_no_context" {
		t.Fatalf("expected no-context business question to enter handoff flow, got %#v", summary)
	}
	if !messagesContainContent(messages, "当前没有从知识库检索到可用资料") {
		t.Fatalf("expected no-context instruction in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "早餐几点") {
		t.Fatalf("expected current user message to remain in messages: %#v", messages)
	}
}

func TestAppendRetrievedContextKeepsSkippedRuntimeActionInstruction(t *testing.T) {
	messages := []*schema.Message{schema.SystemMessage("base instruction")}
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}})
	intent := callbacks.IntentTraceData{
		PrimaryIntent:     "hotel_variable",
		MatchedIntentCode: "hotel_variable",
		SubIntent:         "location",
		ResourceAction:    "provide_location",
		NeedsResource:     true,
		ShouldReply:       true,
	}

	outcome := appendRetrievedContext(context.Background(), newKnowledgePolicyRunInput("发一下酒店定位", ""), intent, &RunResult{}, nil, gate, &messages)
	if outcome.AnswerabilityStatus != answerabilityStatusSkipped {
		t.Fatalf("unexpected answerability status: %q", outcome.AnswerabilityStatus)
	}

	if !messagesContainContent(messages, "酒店变量-定位/地址") {
		t.Fatalf("expected hotel variable instruction in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "不能说让同事发送") {
		t.Fatalf("expected anti fake coworker fallback instruction in messages: %#v", messages)
	}
}

func TestBuildLocationDirectReplyUsesCurrentAccountVariable(t *testing.T) {
	reply := buildLocationDirectReply(&models.WxWorkProtocolInstance{
		StoreNavigationName: "丽斯未来酒店合肥包河店",
		StoreAddress:        "安徽省合肥市包河大道100号",
		StoreLongitude:      "117.263908",
		StoreLatitude:       "31.824097",
	})
	if !strings.Contains(reply, "酒店") || !strings.Contains(reply, "地址") || !strings.Contains(reply, "安徽省合肥市包河大道100号") || !strings.Contains(reply, "uri.amap.com") {
		t.Fatalf("expected address and map uri in direct location reply, got %q", reply)
	}
	if strings.Contains(reply, "发你了") || strings.Contains(reply, "点开就能") {
		t.Fatalf("direct location reply must not pretend a resource was sent, got %q", reply)
	}
}

func TestBuildHotelVariableDirectReplyRoutesLocationIntent(t *testing.T) {
	reply := buildHotelVariableDirectReply(&models.WxWorkProtocolInstance{
		EmployeeName:   "丽斯未来酒店",
		StoreAddress:   "安徽省合肥市包河大道100号",
		StoreLongitude: "117.263908",
		StoreLatitude:  "31.824097",
	}, callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_variable",
		SubIntent:      "location",
		ResourceType:   "location",
		ResourceAction: "provide_location",
	}, "发一下酒店定位")
	if !strings.Contains(reply, "安徽省合肥市包河大道100号") || !strings.Contains(reply, "酒店定位") {
		t.Fatalf("expected location direct reply for location intent, got %q", reply)
	}
}

func TestBuildHotelVariableDirectReplyDoesNotInferResourcesFromMergedText(t *testing.T) {
	reply := buildHotelVariableDirectReply(&models.WxWorkProtocolInstance{
		EmployeeName:              "丽斯未来酒店",
		StoreAddress:              "安徽省合肥市包河大道100号",
		StoreLongitude:            "117.263908",
		StoreLatitude:             "31.824097",
		DefaultMiniProgramPayload: `{"title":"安心宿入住小程序"}`,
	}, callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_variable",
		SubIntent:      "location",
		ResourceType:   "location",
		ResourceAction: "provide_location",
	}, "定位发我一个\n我要办理入住")
	if !strings.Contains(reply, "酒店定位") {
		t.Fatalf("expected location direct reply, got %q", reply)
	}
	if strings.Contains(reply, "入住小程序入口") {
		t.Fatalf("must not infer mini program from merged text when intent action is location only, got %q", reply)
	}
}

func TestKnowledgePolicyKeepsHotelVariableInstructionWhenMixedKnowledgeRetrieves(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, ChunkID: 101, Title: "停车", Content: "停车免费，地上停车场从繁华大道辅路进。", Score: 0.91},
			},
			ContextResults: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, ChunkID: 101, Title: "停车", Content: "停车免费，地上停车场从繁华大道辅路进。", Score: 0.91},
			},
			ContextText: "停车免费，地上停车场从繁华大道辅路进。",
			AnswerMode:  enums.KnowledgeAnswerModeStrict,
		},
	})
	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("定位和入住小程序都发我，顺便问下停车", "1"),
		Summary:   &RunResult{},
		Collector: collector,
		Intent: callbacks.IntentTraceData{
			PrimaryIntent:     "hotel_variable",
			MatchedIntentCode: "hotel_variable",
			SubIntent:         "location",
			ResourceAction:    "provide_location",
			NeedsResource:     true,
			NeedsKnowledge:    true,
			ShouldReply:       true,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if state.SkipGate {
		t.Fatalf("expected mixed variable+knowledge to retrieve, got skip")
	}
	if len(state.Decision.Instructions) == 0 {
		t.Fatalf("expected mixed variable+knowledge instruction, got %#v", state.Decision.Instructions)
	}
	first := state.Decision.Instructions[0].Content
	if !strings.Contains(first, "Commit 阶段") || !strings.Contains(first, "本阶段只回答停车") {
		t.Fatalf("expected mixed variable+knowledge instruction to leave variables for commit, got %#v", state.Decision.Instructions)
	}
	if strings.Contains(first, "酒店变量-定位/地址") || strings.Contains(first, "酒店变量-入住小程序") {
		t.Fatalf("mixed knowledge generation must not expose variable details to Generate, got %#v", state.Decision.Instructions)
	}
}

func TestBuildRunMessagesUsesDeterministicReplyForAmbiguousConfirm(t *testing.T) {
	summary := &RunResult{}
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	gate := newTestKnowledgePolicyGate(retriever)

	_ = buildRunMessages(context.Background(), newKnowledgePolicyRunInput("确认确认", "1"), summary, nil, gate)

	if summary.ReplyText != "" {
		t.Fatalf("expected model-generated confirm reply, got fixed reply %q", summary.ReplyText)
	}
	if retriever.called {
		t.Fatal("expected retriever not to run")
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
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if len(state.Decision.Instructions) != 1 {
		t.Fatalf("expected one no-context instruction, got %d", len(state.Decision.Instructions))
	}
	assertNoFixedFallbackSource(t, state.Decision.Instructions[0].Content)
	if !strings.Contains(state.Decision.Instructions[0].Content, "回复运行时决策") {
		t.Fatalf("expected runtime-engine no-context policy, got %q", state.Decision.Instructions[0].Content)
	}
}

func TestKnowledgePolicyEvaluateInjectsGroundedInstructionAndContext(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, DocumentID: 10, ChunkID: 101, Content: "早餐时间是 7:00-9:30。", Score: 0.91},
			},
			ContextResults: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, DocumentID: 10, ChunkID: 101, Content: "早餐时间是 7:00-9:30。", Score: 0.91},
			},
			ContextText: "知识库片段：早餐时间是 7:00-9:30。",
			AnswerMode:  enums.KnowledgeAnswerModeStrict,
		},
	})

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("早餐几点", "1"),
		Summary:   &RunResult{},
		Intent:    hotelInfoIntent(),
		Collector: collector,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
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
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 200, MatchMode: "keyword", Keywords: "早餐", NeedsKnowledge: true, Status: enums.StatusOk})
	summary := &RunResult{}
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		result: &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{1},
			Hits: []rag.RetrieveResult{
				{KnowledgeBaseID: 1, DocumentID: 10, ChunkID: 101, Content: "早餐时间是 7:00-9:30。", Score: 0.91},
			},
			ContextText: "知识库片段：早餐时间是 7:00-9:30。",
			AnswerMode:  enums.KnowledgeAnswerModeStrict,
		},
	})

	messages := make([]*schema.Message, 0)
	req := newKnowledgePolicyRunInput("早餐几点", "1")
	outcome := appendRetrievedContext(context.Background(), req, hotelInfoIntent(), summary, nil, gate, &messages)
	messages = append(messages, schema.UserMessage(req.UserMessage.Content))
	if outcome.AnswerabilityStatus != answerabilityStatusHasContext {
		t.Fatalf("unexpected answerability status: %q", outcome.AnswerabilityStatus)
	}

	if summary.ReplyText != "" {
		t.Fatalf("expected no fallback, got %q", summary.ReplyText)
	}
	if !messagesContainContent(messages, "知识库回答约束") {
		t.Fatalf("expected knowledge instruction in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "早餐时间") {
		t.Fatalf("expected retrieved context in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "早餐几点") {
		t.Fatalf("expected current user message in messages: %#v", messages)
	}
}

func TestKnowledgePolicyUsesRuntimeFallbackForHumanDecision(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(retriever)

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("怎么退款", "1"),
		Collector: collector,
		Intent:    humanRiskIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !state.SkipGate {
		t.Fatal("refund should skip knowledge and let the model respond from action instruction")
	}
	if len(state.Decision.Instructions) == 0 || !strings.Contains(state.Decision.Instructions[0].Content, "人工/投诉/风险") {
		t.Fatalf("expected human decision instruction, got %#v", state.Decision.Instructions)
	}
	if retriever.called {
		t.Fatal("refund should not let the model answer from knowledge directly")
	}
	if collector.Data.Answerability.Reason != "intent does not require knowledge" {
		t.Fatalf("unexpected reason: %q", collector.Data.Answerability.Reason)
	}
}

func TestKnowledgePolicyEvaluateSkipsWhenNoKnowledgeConfigured(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{}
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(retriever)

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("你好", ""),
		Collector: collector,
		Intent:    socialConfirmIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if !state.SkipGate {
		t.Fatal("expected conversational intent to skip knowledge and continue to model")
	}
	if retriever.called {
		t.Fatal("expected retriever not to run without configured knowledge")
	}
	if collector.Data.Answerability.Status != answerabilityStatusSkipped {
		t.Fatalf("unexpected status: %q", collector.Data.Answerability.Status)
	}
}

func TestKnowledgePolicyEvaluateUsesRuntimeActionFallback(t *testing.T) {
	retriever := &fakeKnowledgeContextRetriever{knowledgeBaseIDs: []int64{1}}
	collector := callbacks.NewRuntimeTraceCollector()
	gate := newTestKnowledgePolicyGate(retriever)

	state, err := gate.Evaluate(context.Background(), answerabilityGateInput{
		Request:   newKnowledgePolicyRunInput("帮我转人工", "1"),
		Collector: collector,
		Intent:    humanRiskIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if !state.SkipGate {
		t.Fatal("expected runtime action to skip knowledge and continue to model")
	}
	if len(state.Decision.Instructions) == 0 || !strings.Contains(state.Decision.Instructions[0].Content, "人工/投诉/风险") {
		t.Fatalf("expected handoff instruction, got %#v", state.Decision.Instructions)
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
		Request:   newKnowledgePolicyRunInput("早餐几点", "1"),
		Collector: collector,
		Intent:    hotelInfoIntent(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
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
	setupRuntimeIntentConfigTestDB(t)
	seedRuntimeIntentConfig(t, models.ReplyIntentConfig{Code: "hotel_info", Name: "酒店信息", Priority: 200, MatchMode: "keyword", Keywords: "早餐", NeedsKnowledge: true, Status: enums.StatusOk})
	summary := &RunResult{}
	gate := newTestKnowledgePolicyGate(&fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		err:              errors.New("vector store unavailable"),
	})

	messages := make([]*schema.Message, 0)
	req := newKnowledgePolicyRunInput("早餐几点", "1")
	outcome := appendRetrievedContext(context.Background(), req, hotelInfoIntent(), summary, nil, gate, &messages)
	messages = append(messages, schema.UserMessage(req.UserMessage.Content))
	if outcome.AnswerabilityStatus != answerabilityStatusUnanswerable {
		t.Fatalf("unexpected answerability status: %q", outcome.AnswerabilityStatus)
	}

	if summary.ReplyText != "" {
		t.Fatalf("expected no early fallback reply, got %q", summary.ReplyText)
	}
	if !messagesContainContent(messages, "知识库检索暂时不可用") {
		t.Fatalf("expected retrieval-error instruction in messages: %#v", messages)
	}
	if !messagesContainContent(messages, "早餐几点") {
		t.Fatalf("expected current user message to remain in messages: %#v", messages)
	}
}
