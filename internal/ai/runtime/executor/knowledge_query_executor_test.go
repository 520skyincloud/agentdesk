package executor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type checkpointTestRetriever struct {
	calls atomic.Int32
	delay time.Duration
	noHit bool
	err   error
}

func (r *checkpointTestRetriever) RetrieveContextByOptions(_ context.Context, opts retrievers.KnowledgeRetrieveOptions, query string) (*retrievers.KnowledgeRetrieveResult, error) {
	r.calls.Add(1)
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.noHit {
		return &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: []int64{11}, Query: query, Options: opts,
		}, nil
	}
	hit := rag.RetrieveResult{
		KnowledgeBaseID: 11,
		SourceRecordID:  "source-parking",
		DocumentTitle:   "停车说明",
		Title:           "停车入口",
		Content:         "停车场入口位于酒店东侧。",
		Score:           0.91,
	}
	return &retrievers.KnowledgeRetrieveResult{
		KnowledgeBaseIDs: []int64{11},
		Query:            query,
		Options:          opts,
		Hits:             []rag.RetrieveResult{hit},
		ContextResults:   []rag.RetrieveResult{hit},
		ContextText:      hit.Content,
		TopScore:         float64(hit.Score),
		RetrieveMs:       25,
	}, nil
}

func TestExecuteKnowledgeQueryReusesNoHitCheckpoint(t *testing.T) {
	db := setupKnowledgeCheckpointTestDB(t)
	retriever := &checkpointTestRetriever{noHit: true}
	plan := BuildKnowledgeQueryPlan(101, 77, 1, 1, "有直升机停机坪吗", "answer", 9, 2, 17, "task-helicopter")
	options := retrievers.DefaultKnowledgeRetrieveOptions()

	first, err := ExecuteKnowledgeQuery(context.Background(), plan, retriever, options, db)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	second, err := ExecuteKnowledgeQuery(context.Background(), plan, retriever, options, db)
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if retriever.calls.Load() != 1 || first == nil || second == nil || first.RetrieveLogID != second.RetrieveLogID {
		t.Fatalf("no-hit checkpoint was not reused: calls=%d first=%+v second=%+v", retriever.calls.Load(), first, second)
	}
	var item models.KnowledgeRetrieveLog
	if err := db.First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.ExecutionStatus != "no_hit" || item.CompletedAt == nil || item.HitCount != 0 {
		t.Fatalf("unexpected no-hit checkpoint: %+v", item)
	}
}

func TestExecuteKnowledgeQueryDoesNotRetryFailedCheckpoint(t *testing.T) {
	db := setupKnowledgeCheckpointTestDB(t)
	upstreamErr := errors.New("upstream exhausted")
	retriever := &checkpointTestRetriever{err: upstreamErr}
	plan := BuildKnowledgeQueryPlan(101, 77, 1, 1, "停车场入口在哪里", "answer", 9, 2, 17, "task-parking")
	options := retrievers.DefaultKnowledgeRetrieveOptions()

	if _, err := ExecuteKnowledgeQuery(context.Background(), plan, retriever, options, db); !errors.Is(err, upstreamErr) {
		t.Fatalf("first error=%v want upstream error", err)
	}
	if _, err := ExecuteKnowledgeQuery(context.Background(), plan, retriever, options, db); !errors.Is(err, errKnowledgeCheckpointFailed) {
		t.Fatalf("second error=%v want terminal checkpoint error", err)
	}
	if retriever.calls.Load() != 1 {
		t.Fatalf("failed checkpoint amplified upstream calls: %d", retriever.calls.Load())
	}
	var item models.KnowledgeRetrieveLog
	if err := db.First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.ExecutionStatus != "failed" || item.CompletedAt == nil || item.LeaseOwner != "" || item.LeaseExpiresAt != nil {
		t.Fatalf("failed checkpoint did not reach a clean terminal state: %+v", item)
	}
}

func TestExecuteKnowledgeQueryWritesOneAuthoritativeCheckpoint(t *testing.T) {
	db := setupKnowledgeCheckpointTestDB(t)
	retriever := &checkpointTestRetriever{}
	plan := BuildKnowledgeQueryPlan(101, 77, 1, 1, "停车场入口在哪里", "answer", 9, 2, 17, "task-parking")
	options := retrievers.DefaultKnowledgeRetrieveOptions()

	first, err := ExecuteKnowledgeQuery(context.Background(), plan, retriever, options, db)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	second, err := ExecuteKnowledgeQuery(context.Background(), plan, retriever, options, db)
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if retriever.calls.Load() != 1 {
		t.Fatalf("expected one upstream retrieval, got %d", retriever.calls.Load())
	}
	if first.RetrieveLogID <= 0 || second.RetrieveLogID != first.RetrieveLogID {
		t.Fatalf("expected reused retrieve checkpoint, first=%d second=%d", first.RetrieveLogID, second.RetrieveLogID)
	}

	var logs []models.KnowledgeRetrieveLog
	if err := db.Find(&logs).Error; err != nil {
		t.Fatalf("find retrieve logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected one authoritative retrieve log, got %d", len(logs))
	}
	logItem := logs[0]
	if logItem.ExecutionStatus != "succeeded" || logItem.TurnID != 9 || logItem.TurnVersion != 2 ||
		logItem.TaskID != 17 || logItem.TaskKey != "task-parking" || logItem.QueryPurpose != "answer" {
		t.Fatalf("unexpected retrieve checkpoint: %+v", logItem)
	}
	if logItem.CheckpointKey == nil || *logItem.CheckpointKey != plan.CheckpointKey || logItem.HitCount != 1 || logItem.UsedChunkCount != 1 {
		t.Fatalf("checkpoint evidence was not persisted: %+v", logItem)
	}
	var hits []models.KnowledgeRetrieveHit
	if err := db.Where("retrieve_log_id = ?", logItem.ID).Find(&hits).Error; err != nil {
		t.Fatalf("find retrieve hits: %v", err)
	}
	if len(hits) != 1 || !hits[0].UsedInAnswer || hits[0].SourceRecordID != "source-parking" {
		t.Fatalf("unexpected persisted hits: %+v", hits)
	}
}

func TestConditionalProbeCheckpointPromotesIntoFormalTaskWithoutSecondFastGPTCall(t *testing.T) {
	db := setupKnowledgeCheckpointTestDB(t)
	retriever := &checkpointTestRetriever{}
	query := "酒店有速溶咖啡吗"
	probePlan := BuildKnowledgeQueryPlan(101, 77, 1, 1, query, "conditional_probe", 9, 2, 0, "conditional_probe_coffee")
	options := retrievers.DefaultKnowledgeRetrieveOptions()
	probe, err := ExecuteKnowledgeQuery(context.Background(), probePlan, retriever, options, db)
	if err != nil {
		t.Fatalf("execute conditional probe: %v", err)
	}
	if probe == nil || probe.RetrieveLogID <= 0 || retriever.calls.Load() != 1 {
		t.Fatalf("conditional probe did not create one checkpoint: probe=%+v calls=%d", probe, retriever.calls.Load())
	}

	req := RunInput{
		Conversation: models.Conversation{ID: 1, TenantID: 101, StoreID: 77},
		UserMessage:  models.Message{SessionNo: 1},
	}
	state := runtimeTaskBatchState{
		Enabled: true, TurnID: 9, TurnVersion: 2,
		TaskIDByTaskKey: map[string]int64{"task-coffee": 17},
	}
	item := runtimeTaskKnowledgeItem{TaskKey: "task-coffee", Query: query}
	promoted, err := promoteConditionalProbeCheckpoint(req, state, item, probe)
	if err != nil {
		t.Fatalf("promote conditional checkpoint: %v", err)
	}
	if promoted == nil || promoted.RetrieveLogID != probe.RetrieveLogID {
		t.Fatalf("probe checkpoint identity changed: probe=%+v promoted=%+v", probe, promoted)
	}

	formalPlan := BuildKnowledgeQueryPlan(101, 77, 1, 1, query, "answer", 9, 2, 17, "task-coffee")
	formal, err := ExecuteKnowledgeQuery(context.Background(), formalPlan, retriever, options, db)
	if err != nil {
		t.Fatalf("reuse promoted formal checkpoint: %v", err)
	}
	if retriever.calls.Load() != 1 || formal == nil || formal.RetrieveLogID != probe.RetrieveLogID {
		t.Fatalf("formal phase queried FastGPT again: calls=%d probe=%+v formal=%+v", retriever.calls.Load(), probe, formal)
	}
}

func TestExecuteKnowledgeQueryConcurrentWorkersShareLease(t *testing.T) {
	db := setupKnowledgeCheckpointTestDB(t)
	retriever := &checkpointTestRetriever{delay: 120 * time.Millisecond}
	plan := BuildKnowledgeQueryPlan(101, 77, 1, 1, "停车场入口在哪里", "answer", 9, 2, 17, "task-parking")
	options := retrievers.DefaultKnowledgeRetrieveOptions()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := make(chan struct{})
	results := make([]*retrievers.KnowledgeRetrieveResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for index := range results {
		wg.Add(1)
		go func(resultIndex int) {
			defer wg.Done()
			<-start
			results[resultIndex], errs[resultIndex] = ExecuteKnowledgeQuery(ctx, plan, retriever, options, db)
		}(index)
	}
	close(start)
	wg.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("worker %d failed: %v", index, err)
		}
	}
	if retriever.calls.Load() != 1 {
		t.Fatalf("concurrent workers must share one upstream retrieval, got %d", retriever.calls.Load())
	}
	if results[0] == nil || results[1] == nil || results[0].RetrieveLogID <= 0 || results[0].RetrieveLogID != results[1].RetrieveLogID {
		t.Fatalf("workers did not reuse one checkpoint: first=%+v second=%+v", results[0], results[1])
	}
	var logCount int64
	if err := db.Model(&models.KnowledgeRetrieveLog{}).Count(&logCount).Error; err != nil {
		t.Fatalf("count retrieve logs: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("expected one retrieve log under concurrency, got %d", logCount)
	}
}

func setupKnowledgeCheckpointTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := "knowledge_checkpoint_" + strings.NewReplacer("/", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(
		&models.KnowledgeBase{},
		&models.Conversation{},
		&models.KnowledgeRetrieveLog{},
		&models.KnowledgeRetrieveHit{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	now := time.Now()
	if err := db.Create(&models.KnowledgeBase{
		ID: 11, TenantID: 101, StoreID: 77, DatasetID: "dataset-parking", Name: "测试知识库",
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create knowledge base: %v", err)
	}
	if err := db.Create(&models.Conversation{
		ID: 1, TenantID: 101, StoreID: 77, Status: enums.IMConversationStatusAIServing,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	sqls.SetDB(db)
	return db
}
