package executor

import (
	"context"
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

type multiTopicStubRetriever struct {
	queries  []string
	byQuery  map[string]*retrievers.KnowledgeRetrieveResult
	kbIDs    []int64
	retrieve func(query string) *retrievers.KnowledgeRetrieveResult
}

func (s *multiTopicStubRetriever) KnowledgeBaseIDs() []int64 { return s.kbIDs }

func (s *multiTopicStubRetriever) RetrieveContextByOptions(ctx context.Context, opts retrievers.KnowledgeRetrieveOptions, query string) (*retrievers.KnowledgeRetrieveResult, error) {
	s.queries = append(s.queries, query)
	if s.retrieve != nil {
		return s.retrieve(query), nil
	}
	if r, ok := s.byQuery[query]; ok {
		return r, nil
	}
	return &retrievers.KnowledgeRetrieveResult{KnowledgeBaseIDs: s.kbIDs, Hits: []rag.RetrieveResult{}, ContextResults: []rag.RetrieveResult{}}, nil
}

func TestSingleMultiTopicTaskSplitsIntoPerClauseQueries(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.KnowledgeBase{}, &models.KnowledgeRetrieveLog{}, &models.KnowledgeRetrieveHit{}, &models.AIReplyTurnTask{}, &models.AIReplyTurn{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })
	if err := db.Create(&models.KnowledgeBase{ID: 1, TenantID: 2, StoreID: 1, Name: "test knowledge", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("seed knowledge base: %v", err)
	}
	stub := &multiTopicStubRetriever{
		kbIDs: []int64{1},
		byQuery: map[string]*retrievers.KnowledgeRetrieveResult{
			"我饿了 有啥吃的推荐没": {
				KnowledgeBaseIDs: []int64{1},
				Hits:             []rag.RetrieveResult{{KnowledgeBaseID: 1, Title: "附近有哪些餐饮推荐？", Content: "问题：附近有哪些餐饮推荐？ 答案：罍街小吃街。", Score: 0.8}},
				ContextResults:   []rag.RetrieveResult{{KnowledgeBaseID: 1, Title: "附近有哪些餐饮推荐？", Content: "答案：罍街小吃街", Score: 0.8}},
			},
			"明天要去附近玩 你知道哪里好玩吗": {
				KnowledgeBaseIDs: []int64{1},
				Hits:             []rag.RetrieveResult{{KnowledgeBaseID: 1, Title: "酒店周边推荐的景点？", Content: "答案：南七天地、骆岗中央公园。", Score: 0.82}},
				ContextResults:   []rag.RetrieveResult{{KnowledgeBaseID: 1, Title: "酒店周边推荐的景点？", Content: "答案：南七天地", Score: 0.82}},
			},
			"我怎么把门打开啊": {
				KnowledgeBaseIDs: []int64{1},
				Hits:             []rag.RetrieveResult{{KnowledgeBaseID: 1, Title: "怎么开门？", Content: "答案：入住后直接刷脸开门，不需要密码。", Score: 0.86}},
				ContextResults:   []rag.RetrieveResult{{KnowledgeBaseID: 1, Title: "怎么开门？", Content: "答案：刷脸开门", Score: 0.86}},
			},
		},
	}
	plans := []callbacks.ReplyTaskPlanTraceData{{
		TaskKey: "t-multi", Sequence: 1, Intent: "hotel_info", Text: "我饿了 有啥吃的推荐没 以及明天要去附近玩 你知道哪里好玩吗  还有啊 我怎么把门打开啊",
	}}
	state := runtimeTaskBatchState{Enabled: false, TurnID: 500, TaskIDByTaskKey: map[string]int64{"t-multi": 1}}
	outcome, err := retrieveRuntimeTaskKnowledgeWithRetriever(context.Background(), RunInput{
		Conversation: models.Conversation{ID: 3, TenantID: 2, StoreID: 1},
		UserMessage:  models.Message{SessionNo: 10},
	}, plans, nil, state, stub)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	fmt.Printf("queries=%v\n", stub.queries)
	if len(stub.queries) < 3 {
		t.Fatalf("multi-topic single task must issue per-clause queries, got %v", stub.queries)
	}
	status := outcome.KnowledgeByTask["t-multi"]
	if status.Status != "has_context" {
		t.Fatalf("merged clause evidence must be has_context, got %#v", status)
	}
}
