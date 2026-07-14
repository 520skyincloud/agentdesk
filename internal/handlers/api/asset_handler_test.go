package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/assetaccess"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestAssetGetFileRequiresTenantBoundSignature(t *testing.T) {
	root := t.TempDir()
	config.SetCurrent(&config.Config{Storage: config.StorageConfig{
		AssetURLSigningSecret: "asset-handler-signing-secret",
		AssetURLTTLSeconds:    60,
		Local:                 config.LocalStorageConfig{Root: root, BaseURL: "/storage"},
	}})
	db, err := gorm.Open(sqlite.Open("file:"+filepath.Join(t.TempDir(), "asset-handler.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Asset{}); err != nil {
		t.Fatalf("migrate asset: %v", err)
	}
	sqls.SetDB(db)

	storageKey := "tenants/101/images/asset-a.txt"
	fullPath := filepath.Join(root, filepath.FromSlash(storageKey))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatalf("mkdir asset directory: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("tenant-a-file"), 0600); err != nil {
		t.Fatalf("write asset file: %v", err)
	}
	asset := &models.Asset{
		TenantID: 101, AssetID: "asset-a", Provider: enums.AssetProviderLocal,
		StorageKey: storageKey, Filename: "asset-a.txt", FileSize: int64(len("tenant-a-file")),
		MimeType: "text/plain", Status: enums.AssetStatusSuccess,
	}
	if err := db.Create(asset).Error; err != nil {
		t.Fatalf("create asset: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/asset/file/:assetId", AssetGetFile)
	validURL, err := assetaccess.BuildRelativeURL(asset.AssetID, asset.TenantID, assetaccess.PurposeInline)
	if err != nil {
		t.Fatalf("build valid URL: %v", err)
	}

	assertAssetRequest(t, router, validURL, http.StatusOK, "tenant-a-file")
	assertAssetRequest(t, router, strings.Replace(validURL, "tenantId=101", "tenantId=202", 1), http.StatusForbidden, "")
	assertAssetRequest(t, router, validURL+"x", http.StatusForbidden, "")

	crossTenantURL, err := assetaccess.BuildRelativeURL(asset.AssetID, 202, assetaccess.PurposeInline)
	if err != nil {
		t.Fatalf("build cross-tenant URL: %v", err)
	}
	assertAssetRequest(t, router, crossTenantURL, http.StatusNotFound, "")

	expiredURL, err := assetaccess.BuildRelativeURLAt(asset.AssetID, asset.TenantID, assetaccess.PurposeInline, time.Now().Add(-2*time.Minute))
	if err != nil {
		t.Fatalf("build expired URL: %v", err)
	}
	assertAssetRequest(t, router, expiredURL, http.StatusGone, "")
	assertAssetRequest(t, router, "/api/asset/file/asset-a", http.StatusForbidden, "")
}

func TestAssetGetFileRedirectsSignedExternalAsset(t *testing.T) {
	config.SetCurrent(&config.Config{Storage: config.StorageConfig{
		AssetURLSigningSecret: "asset-handler-signing-secret",
	}})
	db, err := gorm.Open(sqlite.Open("file:"+filepath.Join(t.TempDir(), "asset-external.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Asset{}); err != nil {
		t.Fatalf("migrate asset: %v", err)
	}
	sqls.SetDB(db)
	asset := &models.Asset{
		TenantID: 101, AssetID: "asset-external", Provider: enums.AssetProviderLocal,
		StorageKey: "https://media.example.com/file.png", Filename: "file.png",
		MimeType: "image/png", Status: enums.AssetStatusSuccess,
	}
	if err := db.Create(asset).Error; err != nil {
		t.Fatalf("create external asset: %v", err)
	}
	router := gin.New()
	router.GET("/api/asset/file/:assetId", AssetGetFile)
	signedURL, err := assetaccess.BuildRelativeURL(asset.AssetID, asset.TenantID, assetaccess.PurposeInline)
	if err != nil {
		t.Fatalf("build signed URL: %v", err)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, signedURL, nil))
	if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != asset.StorageKey {
		t.Fatalf("external response status=%d location=%q", recorder.Code, recorder.Header().Get("Location"))
	}
	if recorder.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("Referrer-Policy=%q", recorder.Header().Get("Referrer-Policy"))
	}
}

func assertAssetRequest(t *testing.T, router http.Handler, rawURL string, wantStatus int, wantBody string) {
	t.Helper()
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		t.Fatalf("invalid request URL %q: %v", rawURL, err)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, rawURL, nil))
	if recorder.Code != wantStatus {
		t.Fatalf("GET %s status=%d want=%d body=%s", rawURL, recorder.Code, wantStatus, recorder.Body.String())
	}
	if wantBody != "" && recorder.Body.String() != wantBody {
		t.Fatalf("GET %s body=%q want=%q", rawURL, recorder.Body.String(), wantBody)
	}
}
