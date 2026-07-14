package builders

import (
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
)

func TestBuildAssetReturnsApplicationSignedURLWithoutStorageKey(t *testing.T) {
	config.SetCurrent(&config.Config{Storage: config.StorageConfig{
		AssetURLSigningSecret: "asset-builder-signing-secret",
	}})
	result := BuildAsset(&models.Asset{
		TenantID: 101, AssetID: "builder-asset", Provider: enums.AssetProviderOSS,
		StorageKey: "tenants/101/private/file.png", Status: enums.AssetStatusSuccess,
	})
	if result.StorageKey != "" {
		t.Fatalf("StorageKey=%q want hidden", result.StorageKey)
	}
	if !strings.HasPrefix(result.URL, "/api/asset/file/builder-asset?") || !strings.Contains(result.URL, "tenantId=101") {
		t.Fatalf("URL=%q want tenant-bound application URL", result.URL)
	}
}
