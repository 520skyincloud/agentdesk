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
		ID: knowledgeBase.ID, Name: "更新后的知识库", Description: "仅更新展示和检索参数",
		DefaultTopK: 8, DefaultScoreThreshold: 0.3, DefaultRerankLimit: 6,
		AnswerMode:           int(enums.KnowledgeAnswerModeStrict),
		ResourceAllowedHosts: []string{"https://CDN.example.com/", "cdn.example.com"},
	}, operator)
	if err != nil {
		t.Fatal(err)
	}
	var updated models.KnowledgeBase
	if err := db.First(&updated, knowledgeBase.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.StoreID != store.ID || updated.DatasetID != "dataset-stable" || updated.DatasetName != "远端数据集" ||
		updated.ConnectionID != fastgptapi.ManagedConnectionID {
		t.Fatalf("FastGPT authority was overwritten: %#v", updated)
	}
	if strings.Contains(updated.Remark, "baseUrl") || updated.Remark != `{"resourceAllowedHosts":["cdn.example.com"]}` {
		t.Fatalf("unexpected safe FastGPT remark: %s", updated.Remark)
	}
}
