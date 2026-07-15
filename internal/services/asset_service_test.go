package services

import (
	"fmt"
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func setupWelcomeAssetTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	config.SetCurrent(&config.Config{Storage: config.StorageConfig{
		Default: enums.AssetProviderLocal,
		Local: config.LocalStorageConfig{
			Root:    t.TempDir(),
			BaseURL: "/api/asset/file",
		},
	}})
	dbName := "welcome_asset_test_" + strings.NewReplacer("/", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Asset{}, &models.WxWorkProtocolInstance{}, &models.KnowledgeResourceGroup{}, &models.KnowledgeResourceItem{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	sqls.SetDB(db)
	return db
}

func TestDeleteWelcomeAssetRejectsReferencedImageAndCleansOrphan(t *testing.T) {
	db := setupWelcomeAssetTestDB(t)
	asset := &models.Asset{
		TenantID:   101,
		AssetID:    "welcome-asset-test",
		Provider:   enums.AssetProviderLocal,
		StorageKey: "wxwork-welcome/nonexistent.jpg",
		Filename:   "welcome.jpg",
		MimeType:   "image/jpeg",
		Status:     enums.AssetStatusSuccess,
	}
	if err := db.Create(asset).Error; err != nil {
		t.Fatalf("create asset: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID:            101,
		Guid:                "welcome-asset-guid",
		WelcomeImageAssetID: asset.AssetID,
		Status:              enums.StatusOk,
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	principal := &dto.AuthPrincipal{UserID: 1, Username: "tester", TenantID: 101, ActiveTenantID: 101}
	if err := AssetService.DeleteAsset(asset.ID, principal); err == nil {
		t.Fatal("expected referenced welcome image deletion to be rejected")
	}
	if stored := repositories.AssetRepository.Get(db, asset.ID); stored == nil || stored.Status != enums.AssetStatusSuccess {
		t.Fatalf("referenced image status changed unexpectedly: %#v", stored)
	}

	if err := repositories.WxWorkProtocolInstanceRepository.Updates(db, instance.ID, map[string]any{"welcome_image_asset_id": ""}); err != nil {
		t.Fatalf("clear image reference: %v", err)
	}
	if err := AssetService.DeleteAsset(asset.ID, principal); err != nil {
		t.Fatalf("delete orphan welcome image: %v", err)
	}
	if stored := repositories.AssetRepository.Get(db, asset.ID); stored == nil || stored.Status != enums.AssetStatusDeleted {
		t.Fatalf("expected deleted asset status, got %#v", stored)
	}
}
