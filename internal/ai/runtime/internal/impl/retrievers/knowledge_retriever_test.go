package retrievers

import (
	"fmt"
	"testing"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestInferRuntimeRetrieveSourceTypeRecognizesFastGPTEngineResults(t *testing.T) {
	hits := []response.KnowledgeSearchResult{{SectionPath: "FastGPT知识库/3/collection-1"}}
	if got := inferRuntimeRetrieveSourceType(hits); got != "fastgpt" {
		t.Fatalf("source type=%q", got)
	}
}

func TestBuildRetrieverTraceItemsRecordsRawAndContextOrder(t *testing.T) {
	hits := []rag.RetrieveResult{
		{KnowledgeBaseID: 3, SourceRecordID: "data-1", DocumentTitle: "南七.xlsx", Score: 0.9},
		{KnowledgeBaseID: 3, SourceRecordID: "data-2", DocumentTitle: "南七.xlsx", Score: 0.8},
	}
	items := buildRetrieverTraceItems("附近餐饮", hits, hits[:1], nil)
	if len(items) != 2 || items[0].RawRankNo != 1 || items[0].ContextRankNo != 1 || !items[0].UsedInContext {
		t.Fatalf("unexpected first trace item: %#v", items)
	}
	if items[1].RawRankNo != 2 || items[1].ContextRankNo != 0 || items[1].DiscardReason == "" {
		t.Fatalf("unexpected discarded trace item: %#v", items[1])
	}
}

func TestKnowledgeRetrieverFiltersKnowledgeBasesByAgentTenant(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.KnowledgeBase{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	for _, item := range []models.KnowledgeBase{
		{ID: 11, TenantID: 101, Name: "tenant-a", Status: enums.StatusOk},
		{ID: 22, TenantID: 202, Name: "tenant-b", Status: enums.StatusOk},
	} {
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create knowledge base: %v", err)
		}
	}

	retriever := NewKnowledgeRetriever(models.AIAgent{TenantID: 101, KnowledgeIDs: "11,22"})
	ids := retriever.KnowledgeBaseIDs()
	if len(ids) != 1 || ids[0] != 11 {
		t.Fatalf("knowledge base ids crossed tenant boundary: %#v", ids)
	}
}
