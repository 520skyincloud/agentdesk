package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

// 契约 3.9.4 回放：13 秒语音四题（游玩/入住/安静房/停车）必须按真实停顿
// 拆成四个原子 Query，而不是四次共用同一整段语音包装。
func TestRedistributeMultiTopicClausesSplitsVoicePunctuation(t *testing.T) {
	voice := "附近有什么好玩的呀？我想办个入住，能不能教一下我怎么办呀？这里有没有安静的房间？停车场又在哪里啊？"
	plans := make([]callbacks.ReplyTaskPlanTraceData, 4)
	for i := range plans {
		plans[i] = callbacks.ReplyTaskPlanTraceData{TaskKey: "t", Sequence: i + 1, Text: voice}
	}
	ret := redistributeMultiTopicClauses(plans)
	queries := make([]string, 0, len(ret))
	for _, plan := range ret {
		queries = append(queries, runtimeTaskKnowledgeQuery(plan))
	}
	if len(ret) != 4 {
		t.Fatalf("expected 4 split clauses, got %d: %v", len(ret), queries)
	}
	joined := strings.Join(queries, "|")
	for _, want := range []string{"好玩的", "入住", "安静的房间", "停车场"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("atomic query missing %q: %v", want, queries)
		}
	}
	if queries[0] == queries[1] || queries[0] == queries[2] || queries[0] == queries[3] {
		t.Fatalf("queries must not be the duplicated full text: %v", queries)
	}
}

// 契约 22.12 回放：检索文本禁止 [语音] 文件名包装。
func TestKnowledgeQueryStripsVoiceTransportWrapper(t *testing.T) {
	raw := "[语音] wx_protocol_1006150.mp3 语音内容是：附近有什么好玩的呀？"
	query := runtimeTaskKnowledgeQuery(callbacks.ReplyTaskPlanTraceData{Text: raw})
	if strings.Contains(query, "[语音]") || strings.Contains(query, ".mp3") || strings.Contains(query, "语音内容是") {
		t.Fatalf("transport wrapper leaked into query: %q", query)
	}
	if !strings.Contains(query, "好玩的") {
		t.Fatalf("query lost business text: %q", query)
	}
}

func TestKnowledgeQueryNormalizesSpokenNearbyPlayTopic(t *testing.T) {
	query := runtimeTaskKnowledgeQuery(callbacks.ReplyTaskPlanTraceData{
		Intent: "hotel_info", SubIntent: "surrounding_facilities", Text: "这个附近有什么可以玩的呀",
	})
	if query != "这个附近有什么可以玩的呀 附近游玩" {
		t.Fatalf("spoken nearby-play query was not normalized: %q", query)
	}
}

func TestKnowledgeQueryKeepsSpecificSupplyName(t *testing.T) {
	tests := map[string]string{
		"草稿纸有没有":        "草稿纸",
		"可以给我拿点草稿纸什么的吗": "草稿纸",
		"你们酒店有没有牙刷":     "牙刷",
		"能不能给我拿点拖鞋啊":    "拖鞋",
	}
	for text, want := range tests {
		query := runtimeTaskKnowledgeQuery(callbacks.ReplyTaskPlanTraceData{
			Intent: "hotel_info", SubIntent: "supplies_self_help", Text: text,
		})
		if query != want {
			t.Fatalf("specific supply query %q = %q, want %q", text, query, want)
		}
	}
}

func TestKnowledgeQueryNormalizesSupplyObjectAfterGenericTaskSplit(t *testing.T) {
	query := runtimeTaskKnowledgeQuery(callbacks.ReplyTaskPlanTraceData{
		Intent: "hotel_info", SubIntent: "service_facility", Text: "草稿纸有没有",
	})
	if query != "草稿纸" {
		t.Fatalf("supply object must be normalized even when the model kept a generic subIntent: %q", query)
	}
}

func TestKnowledgeQueryUsesStableNormalCheckinProcedure(t *testing.T) {
	normal := runtimeTaskKnowledgeQuery(callbacks.ReplyTaskPlanTraceData{
		Intent: "hotel_info", SubIntent: "checkin_process", Text: "给我办入住",
	})
	if normal != runtimeNormalCheckinKnowledgeQueryText {
		t.Fatalf("normal checkin query mismatch: %q", normal)
	}
	exception := runtimeTaskKnowledgeQuery(callbacks.ReplyTaskPlanTraceData{
		Intent: "hotel_info", SubIntent: "checkin_process", Text: "手机不能用怎么办入住",
	})
	if exception != "手机不能用怎么办入住 入住流程" {
		t.Fatalf("exception checkin query must preserve the customer's failure context: %q", exception)
	}
	route := runtimeTaskKnowledgeQuery(callbacks.ReplyTaskPlanTraceData{
		Intent: "hotel_info", SubIntent: "checkin_process", Text: "我到楼下了，入住入口怎么走",
	})
	if route == runtimeNormalCheckinKnowledgeQueryText || !strings.Contains(route, "入口怎么走") {
		t.Fatalf("explicit entrance question must preserve route wording: %q", route)
	}
	foreignClause := runtimeTaskKnowledgeQuery(callbacks.ReplyTaskPlanTraceData{
		Intent: "hotel_info", SubIntent: "checkin_process", Text: "停车场在哪里",
	})
	if foreignClause != "停车场在哪里" {
		t.Fatalf("a split non-checkin clause must not inherit the checkin anchor: %q", foreignClause)
	}
}

func TestExpandRuntimeAtomicReplyTaskPlansSplitsVoiceQuestions(t *testing.T) {
	plans := []callbacks.ReplyTaskPlanTraceData{{
		Sequence: 1, Intent: "hotel_info", SubIntent: "service_facility", Output: "knowledge_text_reply",
		Text: "可以给我拿点咖啡吗？或者草稿纸什么的？",
	}}
	got := expandRuntimeAtomicReplyTaskPlans(plans)
	if len(got) != 2 {
		t.Fatalf("expected coffee and paper as two tasks, got %#v", got)
	}
	if !strings.Contains(got[0].Text, "咖啡") || !strings.Contains(got[1].Text, "草稿纸") {
		t.Fatalf("atomic task text mismatch: %#v", got)
	}
	if got[0].Sequence != 1 || got[1].Sequence != 2 {
		t.Fatalf("atomic task sequence mismatch: %#v", got)
	}
}

func TestExpandRuntimeAtomicReplyTaskPlansSplitsDistinctConjunctionObjects(t *testing.T) {
	plans := []callbacks.ReplyTaskPlanTraceData{{
		Sequence: 1, Intent: "hotel_info", SubIntent: "service_facility", Output: "knowledge_text_reply",
		Text: "咖啡和草稿纸有没有",
	}}
	got := expandRuntimeAtomicReplyTaskPlans(plans)
	if len(got) != 2 {
		t.Fatalf("expected conjunction objects as two tasks, got %#v", got)
	}
	if got[0].Text != "咖啡" || got[1].Text != "草稿纸有没有" {
		t.Fatalf("conjunction task text mismatch: %#v", got)
	}
}

func TestExpandRuntimeAtomicReplyTaskPlansKeepsCompoundDimensionsTogether(t *testing.T) {
	plans := []callbacks.ReplyTaskPlanTraceData{{
		Sequence: 1, Intent: "hotel_info", SubIntent: "breakfast", Output: "knowledge_text_reply",
		Text: "早餐时间以及地点",
	}}
	got := expandRuntimeAtomicReplyTaskPlans(plans)
	if len(got) != 1 || got[0].Text != "早餐时间以及地点" {
		t.Fatalf("same-topic dimensions must stay one task: %#v", got)
	}
}

// 契约 3.9.2 回放（语音 1362）：口语重复“都可以”不得拆出重复子句。
func TestSplitMultiTopicClausesDedupesRepeatedPhrases(t *testing.T) {
	voice := "我要吃本地菜。都可以，都可以介绍一下呀。还有什么推荐？还有什么推荐？"
	clauses := dedupeAdjacentClauses(splitMultiTopicClauses(voice))
	for i := 1; i < len(clauses); i++ {
		if clauses[i] == clauses[i-1] {
			t.Fatalf("adjacent duplicate clauses leaked: %v", clauses)
		}
	}
}
