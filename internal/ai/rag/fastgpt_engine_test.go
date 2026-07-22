package rag

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestRetrieveFastGPTMapsQAToRetrieveResultWithoutFAQFields(t *testing.T) {
	requestedTokenLimit := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/integration/agent-desk/dataset/search" || r.Header.Get("X-Agent-Desk-Token") != "integration-secret" {
			t.Fatalf("unexpected managed request: %s headers=%#v", r.URL.Path, r.Header)
		}
		payload := struct {
			ExternalStoreID string `json:"externalStoreId"`
			TokenLimit      int    `json:"tokenLimit"`
		}{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.ExternalStoreID != "31" {
			t.Fatalf("externalStoreId=%q", payload.ExternalStoreID)
		}
		requestedTokenLimit = payload.TokenLimit
		_, _ = io.WriteString(w, `{"code":200,"data":[{"id":"data-1","datasetId":"dataset-1","collectionId":"collection-1","sourceName":"南七.xlsx","q":"有剃须刀吗","a":"前台自助柜可领取","score":0.88}]}`)
	}))
	defer server.Close()
	knowledgeBase := prepareManagedFastGPTRetrieveTest(t)
	config.SetCurrent(&config.Config{FastGPT: config.FastGPTConfig{
		Enabled: true, BaseURL: server.URL, IntegrationToken: "integration-secret", TimeoutMS: 1000,
	}})
	t.Cleanup(func() {
		config.SetCurrent(&config.Config{})
		sqls.SetDB(nil)
	})

	results, _, err := Retrieve.retrieveFastGPTKnowledge(context.Background(), RetrieveRequest{Query: "有剃须刀吗", ContextMaxTokens: 2000}, []models.KnowledgeBase{knowledgeBase})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results=%#v", results)
	}
	if requestedTokenLimit != fastGPTDefaultContextTokens {
		t.Fatalf("requested token limit=%d want %d", requestedTokenLimit, fastGPTDefaultContextTokens)
	}
	result := results[0]
	if result.SourceRecordID != "data-1" || result.DocumentTitle != "南七.xlsx" || result.Title != "有剃须刀吗" {
		t.Fatalf("result=%#v", result)
	}
	if result.FaqID != 0 || result.FaqQuestion != "" || result.Content != "问题：有剃须刀吗\n答案：前台自助柜可领取" {
		t.Fatalf("unexpected mapping=%#v", result)
	}
}

func prepareManagedFastGPTRetrieveTest(t *testing.T) models.KnowledgeBase {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.Store{}, &models.KnowledgeBase{}, &models.StoreModelProfileAssignment{},
		&models.StoreModelCredential{}, &models.FastGPTStoreTenant{},
	); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	store := &models.Store{ID: 31, TenantID: 21, StoreCode: "managed-rag", Name: "南七店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	knowledgeBase := models.KnowledgeBase{
		ID: 41, TenantID: store.TenantID, StoreID: store.ID, Name: "南七知识库",
		KnowledgeType: string(enums.KnowledgeBaseTypeFastGPTCloud), ChunkProvider: string(enums.KnowledgeChunkProviderFastGPT),
		ConnectionID: fastgptapi.ManagedConnectionID, DatasetID: "dataset-1", Status: enums.StatusOk,
		DefaultTopK: 5, DefaultScoreThreshold: 0.2, DefaultRerankLimit: 10, FastGPTProfileStatus: "ready",
		FastGPTAppliedProfileID: 51, FastGPTAppliedProfileRevision: 2, FastGPTAppliedCredentialRevision: 3,
	}
	if err := db.Create(&knowledgeBase).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(store).Update("knowledge_base_id", knowledgeBase.ID).Error; err != nil {
		t.Fatal(err)
	}
	assignment := &models.StoreModelProfileAssignment{
		TenantID: store.TenantID, StoreID: store.ID, TemplateID: 51, TemplateRevision: 2,
		Status: enums.StoreModelAssignmentStatusReady, ReadinessStatus: "ready",
	}
	credential := &models.StoreModelCredential{
		TenantID: store.TenantID, StoreID: store.ID, CredentialRevision: 3,
		Status: enums.StoreCredentialStatusActive,
	}
	binding := &models.FastGPTStoreTenant{
		TenantID: store.TenantID, StoreID: store.ID, TenantTeamID: "team-31", Status: "active", ReadinessStatus: "ready",
		AppliedProfileID: assignment.TemplateID, AppliedProfileRevision: assignment.TemplateRevision,
		AppliedCredentialRevision: credential.CredentialRevision,
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(credential).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatal(err)
	}
	return knowledgeBase
}

func TestFastGPTEngineExtractsImagesWithoutLeakingURLsIntoModelContext(t *testing.T) {
	answer := "从北门进入。\n![入口一](https://assets.example.com/one.png)\nhttps://assets.example.com/two.jpg"
	resources := extractFastGPTImageResources(answer)
	if len(resources) != 2 || resources[0].SourceURL != "https://assets.example.com/one.png" || resources[1].SourceURL != "https://assets.example.com/two.jpg" {
		t.Fatalf("resources=%#v", resources)
	}
	if got := stripFastGPTImageURLs(answer); got != "从北门进入。" {
		t.Fatalf("cleaned answer=%q", got)
	}
}
