package services

import (
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestBuildKnowledgeBaseModelUsesLowerDefaultScoreThreshold(t *testing.T) {
	item, err := KnowledgeBaseService.buildKnowledgeBaseModel(request.CreateKnowledgeBaseRequest{}, 101)
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

func TestCreateKnowledgeBaseRejectsFastGPTOutsideProvisionFlow(t *testing.T) {
	operator := &dto.AuthPrincipal{UserID: 9, Username: "admin", ActiveTenantID: 101, Roles: []string{constants.RoleCodeAdmin}}
	_, err := KnowledgeBaseService.CreateKnowledgeBase(request.CreateKnowledgeBaseRequest{
		Name: "绕过开通流程", KnowledgeType: string(enums.KnowledgeBaseTypeFastGPTCloud),
	}, operator)
	if err == nil || !strings.Contains(err.Error(), "开通流程") {
		t.Fatalf("expected provision-only error, got %v", err)
	}
}

func TestUpdateFastGPTKnowledgeBasePreservesStoreAuthority(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Store{}, &models.KnowledgeBase{}); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })

	store := &models.Store{TenantID: 101, StoreCode: "fastgpt-authority", Name: "南七店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	knowledgeBase := &models.KnowledgeBase{
		TenantID: 101, StoreID: store.ID, Name: "门店知识库", KnowledgeType: string(enums.KnowledgeBaseTypeFastGPTCloud),
		DatasetID: "dataset-stable", DatasetName: "远端数据集", ConnectionID: fastgptapi.ManagedConnectionID,
		RetrievalMode: enums.KnowledgeRetrievalModeFastGPT, ChunkProvider: string(enums.KnowledgeChunkProviderFastGPT),
		DefaultTopK: 5, DefaultScoreThreshold: 0.2, DefaultRerankLimit: 10, AnswerMode: int(enums.KnowledgeAnswerModeStrict),
		Remark: `{"baseUrl":"legacy","resourceAllowedHosts":["old.example.com"]}`, Status: enums.StatusOk,
	}
	if err := db.Create(knowledgeBase).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(store).Update("knowledge_base_id", knowledgeBase.ID).Error; err != nil {
		t.Fatal(err)
	}
	operator := &dto.AuthPrincipal{UserID: 9, Username: "admin", ActiveTenantID: 101, Roles: []string{constants.RoleCodeAdmin}}
	err = KnowledgeBaseService.UpdateKnowledgeBase(request.UpdateKnowledgeBaseRequest{
		ID: knowledgeBase.ID,
		CreateKnowledgeBaseRequest: request.CreateKnowledgeBaseRequest{
			Name: "更新后的知识库", Description: "仅更新展示和检索参数",
			KnowledgeType: string(enums.KnowledgeBaseTypeFastGPTCloud), DefaultTopK: 8,
			DefaultScoreThreshold: 0.3, DefaultRerankLimit: 6, AnswerMode: int(enums.KnowledgeAnswerModeStrict),
			ResourceAllowedHosts: []string{"https://CDN.example.com/", "cdn.example.com"},
		},
	}, operator)
	if err != nil {
		t.Fatal(err)
	}
	var updated models.KnowledgeBase
	if err := db.First(&updated, knowledgeBase.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.StoreID != store.ID || updated.DatasetID != "dataset-stable" || updated.DatasetName != "远端数据集" ||
		updated.ConnectionID != fastgptapi.ManagedConnectionID || updated.ChunkProvider != string(enums.KnowledgeChunkProviderFastGPT) {
		t.Fatalf("FastGPT authority was overwritten: %#v", updated)
	}
	if strings.Contains(updated.Remark, "baseUrl") || updated.Remark != `{"resourceAllowedHosts":["cdn.example.com"]}` {
		t.Fatalf("unexpected safe FastGPT remark: %s", updated.Remark)
	}
}
