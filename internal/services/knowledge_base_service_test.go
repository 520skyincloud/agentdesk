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

func TestBuildKnowledgeBaseModelUsesLowerDefaultScoreThreshold(t *testing.T) {
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

	item, err := KnowledgeBaseService.buildKnowledgeBaseModel(request.CreateKnowledgeBaseRequest{IntentProfileID: profile.ID}, 101)
	if err != nil {
		t.Fatalf("build knowledge base model failed: %v", err)
	}
	if item.DefaultScoreThreshold != 0.2 {
		t.Fatalf("expected default score threshold 0.2, got %v", item.DefaultScoreThreshold)
	}
}

func TestBuildKnowledgeBaseModelDoesNotRequireIntentProfile(t *testing.T) {
	item, err := KnowledgeBaseService.buildKnowledgeBaseModel(request.CreateKnowledgeBaseRequest{Name: "独立门店 FastGPT"}, 101)
	if err != nil {
		t.Fatalf("knowledge base without industry profile should be valid: %v", err)
	}
	if item.IntentProfileID != 0 {
		t.Fatalf("intent profile should remain optional, got %d", item.IntentProfileID)
	}
}
