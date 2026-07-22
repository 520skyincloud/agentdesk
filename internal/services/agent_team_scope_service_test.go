package services

import (
	"fmt"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestStoreStaffScopeIncludesEveryKnowledgeBaseOwnedByStore(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Store{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{}, &models.KnowledgeBase{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})

	store := &models.Store{Name: "测试门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := db.Create(&models.StoreStaffBinding{UserID: 77, StoreID: store.ID, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create binding: %v", err)
	}
	first := &models.KnowledgeBase{StoreID: store.ID, DatasetID: "dataset-1", Name: "当前库", Status: enums.StatusOk}
	second := &models.KnowledgeBase{StoreID: store.ID, DatasetID: "dataset-2", Name: "备用库", Status: enums.StatusOk}
	if err := db.Create(first).Error; err != nil {
		t.Fatalf("create first knowledge base: %v", err)
	}
	if err := db.Create(second).Error; err != nil {
		t.Fatalf("create second knowledge base: %v", err)
	}
	if err := db.Create(&models.WxWorkProtocolInstance{Guid: "scope-instance", StoreID: store.ID, KnowledgeBaseID: first.ID, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	scope := AgentTeamScopeService.Resolve(&dto.AuthPrincipal{UserID: 77, Roles: []string{constants.RoleCodeStoreStaff}})
	if !testContainsInt64(scope.KnowledgeBaseIDs, first.ID) || !testContainsInt64(scope.KnowledgeBaseIDs, second.ID) {
		t.Fatalf("store staff cannot see every store knowledge base: %#v", scope.KnowledgeBaseIDs)
	}
}

func TestStoreStaffScopeDoesNotExpandToSiblingStoresInCompany(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Store{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{}, &models.KnowledgeBase{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})

	first := &models.Store{StoreCode: "scope-store-1", Name: "南七店", CompanyID: 9, Status: enums.StatusOk}
	second := &models.Store{StoreCode: "scope-store-2", Name: "高铁南站店", CompanyID: 9, Status: enums.StatusOk}
	if err := db.Create(first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(second).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.StoreStaffBinding{
		UserID: 88, CompanyID: 9, StoreID: first.ID, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatal(err)
	}

	scope := AgentTeamScopeService.Resolve(&dto.AuthPrincipal{
		UserID: 88, Roles: []string{constants.RoleCodeStoreStaff},
	})
	if !testContainsInt64(scope.StoreIDs, first.ID) {
		t.Fatalf("bound store missing from scope: %#v", scope.StoreIDs)
	}
	if testContainsInt64(scope.StoreIDs, second.ID) {
		t.Fatalf("sibling store leaked into store staff scope: %#v", scope.StoreIDs)
	}
}

func testContainsInt64(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
