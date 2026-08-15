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
