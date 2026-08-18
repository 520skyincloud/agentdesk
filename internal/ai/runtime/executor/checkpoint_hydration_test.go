package executor

import (
	"testing"

	"agent-desk/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestFindSucceededCheckpointHydratesFromRetrieverRow(t *testing.T) {
	db := setupCheckpointHydrationTestDB(t)
	plan := BuildKnowledgeQueryPlan(2, 1, 3, 10, "行李放哪", "answer", 500, 0, "tk1")
	// 执行器 checkpoint 行：succeeded 但不携带命中（检索器另行记日志）。
	executorRow := &models.KnowledgeRetrieveLog{
		TenantID: plan.TenantID, ScopeFingerprint: plan.ScopeFingerprint,
		QueryFingerprint: plan.QueryFingerprint, QueryKey: plan.QueryKey,
		QueryPurpose: plan.Purpose, ExecutionStatus: "succeeded",
		TurnID: plan.TurnID, TaskKey: plan.TaskKey, Question: plan.Query,
	}
	if err := db.Create(executorRow).Error; err != nil {
		t.Fatalf("seed executor row: %v", err)
	}
	// 检索器行：同一 query_key，带命中。
	retrieverRow := &models.KnowledgeRetrieveLog{
		TenantID: plan.TenantID, QueryKey: plan.QueryKey,
		QueryFingerprint: plan.QueryFingerprint, ScopeFingerprint: plan.ScopeFingerprint,
		ExecutionStatus: "pending", HitCount: 5, TopScore: 0.81, Question: plan.Query,
	}
	if err := db.Create(retrieverRow).Error; err != nil {
		t.Fatalf("seed retriever row: %v", err)
	}
	for i := 0; i < 3; i++ {
		hit := models.KnowledgeRetrieveHit{
			RetrieveLogID: retrieverRow.ID, KnowledgeBaseID: 1,
			RankNo: i + 1, Score: 0.8, Title: "酒店提供行李寄存服务吗？",
			Snippet: "问题：酒店提供行李寄存服务吗？ 答案：我们酒店提供行李寄存服务。",
		}
		if err := db.Create(&hit).Error; err != nil {
			t.Fatalf("seed hit: %v", err)
		}
	}
	cached := findSucceededCheckpoint(db, plan)
	if cached == nil || len(cached.Hits) == 0 {
		t.Fatalf("must hydrate hits from retriever row, got %#v", cached)
	}
}

func TestFindSucceededCheckpointReturnsNilWhenNoHitsAnywhere(t *testing.T) {
	db := setupCheckpointHydrationTestDB(t)
	plan := BuildKnowledgeQueryPlan(2, 1, 3, 10, "空查询", "answer", 501, 0, "tk2")
	row := &models.KnowledgeRetrieveLog{
		TenantID: plan.TenantID, ScopeFingerprint: plan.ScopeFingerprint,
		QueryFingerprint: plan.QueryFingerprint, QueryKey: plan.QueryKey,
		ExecutionStatus: "succeeded", Question: plan.Query,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if cached := findSucceededCheckpoint(db, plan); cached != nil {
		t.Fatalf("empty succeeded checkpoint must not be reused, got %#v", cached)
	}
}

func setupCheckpointHydrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.KnowledgeRetrieveLog{}, &models.KnowledgeRetrieveHit{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}
