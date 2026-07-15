package services

import (
	"fmt"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func setupKnowledgeResourceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := "knowledge_resource_" + t.Name()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Asset{}, &models.KnowledgeResourceGroup{}, &models.KnowledgeResourceItem{}, &models.WxWorkProtocolInstance{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	sqls.SetDB(db)
	return db
}

func TestResolveKnowledgeResourcesStaysWithinStoreAndKnowledgeScopeAcrossAccountReplacement(t *testing.T) {
	db := setupKnowledgeResourceTestDB(t)
	for _, instance := range []models.WxWorkProtocolInstance{
		{ID: 41, TenantID: 101, Guid: "old-account", CompanyID: 11, StoreID: 7, Status: enums.StatusOk},
		{ID: 42, TenantID: 101, Guid: "replacement-account", CompanyID: 11, StoreID: 7, Status: enums.StatusOk},
		{ID: 43, TenantID: 101, Guid: "other-store", CompanyID: 11, StoreID: 8, Status: enums.StatusOk},
	} {
		if err := db.Create(&instance).Error; err != nil {
			t.Fatalf("create instance: %v", err)
		}
	}
	asset := &models.Asset{
		TenantID:   101,
		AssetID:    "knowledge-image-001",
		Provider:   enums.AssetProviderLocal,
		StorageKey: "knowledge-resources/3/7/entrance.png",
		Filename:   "entrance.png",
		MimeType:   "image/png",
		Status:     enums.AssetStatusSuccess,
	}
	if err := db.Create(asset).Error; err != nil {
		t.Fatalf("create asset: %v", err)
	}
	group := &models.KnowledgeResourceGroup{
		TenantID:         101,
		CompanyID:        11,
		StoreID:          7,
		IntentProfileID:  0,
		KnowledgeBaseID:  31,
		WxWorkInstanceID: 0,
		SourceProvider:   knowledgeResourceProviderFastGPT,
		SourceRecordID:   "fastgpt-record-001",
		Status:           enums.StatusOk,
	}
	if err := db.Create(group).Error; err != nil {
		t.Fatalf("create resource group: %v", err)
	}
	item := &models.KnowledgeResourceItem{
		TenantID:                 101,
		KnowledgeResourceGroupID: group.ID,
		AssetID:                  asset.AssetID,
		SourceURL:                "https://cdn.example.test/entrance.png",
		SourceChecksum:           "abc",
		Title:                    "停车场入口图",
		SortNo:                   1,
		Status:                   enums.StatusOk,
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create resource item: %v", err)
	}

	sources := []KnowledgeResourceSourceRef{{KnowledgeBaseID: 31, SourceRecordID: "fastgpt-record-001"}}
	resources := KnowledgeResourceService.ResolveForRuntime(41, 11, 101, sources)
	if len(resources) != 1 || resources[0].AssetID != asset.AssetID {
		t.Fatalf("expected exactly scoped resource, got %+v", resources)
	}
	resources = KnowledgeResourceService.ResolveForRuntime(42, 11, 101, sources)
	if len(resources) != 1 || resources[0].AssetID != asset.AssetID {
		t.Fatalf("replacement account should reuse store resource, got %+v", resources)
	}

	for _, scope := range []struct {
		name      string
		instance  int64
		company   int64
		tenant    int64
		sourceRef []KnowledgeResourceSourceRef
	}{
		{name: "other store", instance: 43, company: 11, tenant: 101, sourceRef: sources},
		{name: "other company", instance: 41, company: 12, tenant: 101, sourceRef: sources},
		{name: "other tenant", instance: 41, company: 11, tenant: 202, sourceRef: sources},
		{name: "other source", instance: 41, company: 11, tenant: 101, sourceRef: []KnowledgeResourceSourceRef{{KnowledgeBaseID: 31, SourceRecordID: "fastgpt-record-002"}}},
	} {
		t.Run(scope.name, func(t *testing.T) {
			if got := KnowledgeResourceService.ResolveForRuntime(scope.instance, scope.company, scope.tenant, scope.sourceRef); len(got) != 0 {
				t.Fatalf("resource crossed isolation boundary: %+v", got)
			}
		})
	}
}

func TestKnowledgeResourceAllowedHostsNormalizesExactHosts(t *testing.T) {
	hosts := knowledgeResourceAllowedHosts(`{"resourceAllowedHosts":["https://cdn.example.test/","CDN.example.test","cdn.example.test/path","", "cdn.example.test"]}`)
	if len(hosts) != 1 || hosts[0] != "cdn.example.test" {
		t.Fatalf("unexpected allowed hosts: %#v", hosts)
	}
}
