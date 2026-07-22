package services

import (
	"fmt"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestBuildKnowledgeBaseModelUsesSouthSevenDefaults(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.ReplyIntentProfile{}); err != nil {
		t.Fatalf("migrate intent profile: %v", err)
	}
	profile := &models.ReplyIntentProfile{Code: "hotel", Name: "测试酒店行业", Status: enums.StatusOk}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("seed intent profile: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
	})

	item, err := KnowledgeBaseService.buildKnowledgeBaseModel(request.CreateKnowledgeBaseRequest{IntentProfileID: profile.ID})
	if err != nil {
		t.Fatalf("build knowledge base model failed: %v", err)
	}
	if item.DefaultTopK != 5 || item.DefaultScoreThreshold != 0.2 || item.DefaultRerankLimit != 10 {
		t.Fatalf("unexpected retrieval defaults: topK=%d threshold=%v rerank=%d", item.DefaultTopK, item.DefaultScoreThreshold, item.DefaultRerankLimit)
	}
	if item.ChunkTargetTokens != 300 || item.ChunkMaxTokens != 400 || item.ChunkOverlapTokens != 40 {
		t.Fatalf("unexpected chunk defaults: target=%d max=%d overlap=%d", item.ChunkTargetTokens, item.ChunkMaxTokens, item.ChunkOverlapTokens)
	}
	if item.AnswerMode != int(enums.KnowledgeAnswerModeStrict) {
		t.Fatalf("expected strict answer mode, got %d", item.AnswerMode)
	}
}

func TestBuildFastGPTKnowledgeBaseModelUsesSouthSevenDefaults(t *testing.T) {
	item, err := KnowledgeBaseService.buildKnowledgeBaseModel(request.CreateKnowledgeBaseRequest{
		Name:          "新门店知识库",
		KnowledgeType: string(enums.KnowledgeBaseTypeFastGPTCloud),
	})
	if err != nil {
		t.Fatalf("build FastGPT knowledge base model failed: %v", err)
	}
	if item.DefaultTopK != 5 || item.DefaultScoreThreshold != 0.2 || item.DefaultRerankLimit != 10 {
		t.Fatalf("unexpected FastGPT retrieval defaults: topK=%d threshold=%v rerank=%d", item.DefaultTopK, item.DefaultScoreThreshold, item.DefaultRerankLimit)
	}
	if item.ChunkProvider != string(enums.KnowledgeChunkProviderFastGPT) {
		t.Fatalf("expected FastGPT chunk provider, got %q", item.ChunkProvider)
	}
	if item.ChunkTargetTokens != 300 || item.ChunkMaxTokens != 400 || item.ChunkOverlapTokens != 40 {
		t.Fatalf("unexpected FastGPT chunk defaults: target=%d max=%d overlap=%d", item.ChunkTargetTokens, item.ChunkMaxTokens, item.ChunkOverlapTokens)
	}
	if item.AnswerMode != int(enums.KnowledgeAnswerModeStrict) {
		t.Fatalf("expected strict answer mode, got %d", item.AnswerMode)
	}
}

func TestBuildKnowledgeBaseModelDoesNotRequireIntentProfile(t *testing.T) {
	item, err := KnowledgeBaseService.buildKnowledgeBaseModel(request.CreateKnowledgeBaseRequest{Name: "独立门店 FastGPT"})
	if err != nil {
		t.Fatalf("knowledge base without industry profile should be valid: %v", err)
	}
	if item.IntentProfileID != 0 {
		t.Fatalf("intent profile should remain optional, got %d", item.IntentProfileID)
	}
}
