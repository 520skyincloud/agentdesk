package dashboard

import (
	"encoding/json"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestStorageSettingPlatformPermissionsAllowReadAndUpdate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatalf("migrate system config: %v", err)
	}
	sqls.SetDB(db)
	config.SetCurrent(&config.Config{Storage: config.StorageConfig{
		Default: enums.AssetProviderLocal,
		Local:   config.LocalStorageConfig{Root: "data/storage", BaseURL: "/storage"},
	}})

	viewCtx, viewRecorder := newAuthzHandlerTestContext(t, "", &dto.AuthPrincipal{
		UserID: 201, Username: "platform_storage_viewer", IsPlatformAccount: true,
		Permissions: []string{constants.PermissionStorageSettingView.Code},
	})
	StorageSettingGet(viewCtx)
	assertStorageSettingSuccess(t, viewRecorder.Body.Bytes())

	updateCtx, updateRecorder := newAuthzHandlerTestContext(t, `{
		"defaultProvider":"local",
		"maxUploadSizeMb":42,
		"localRoot":"data/private-storage",
		"localBaseUrl":"/files",
		"ossPrivate":true,
		"ossSignedUrlExpireSeconds":900
	}`, &dto.AuthPrincipal{
		UserID: 202, Username: "platform_storage_editor", IsPlatformAccount: true,
		Permissions: []string{constants.PermissionStorageSettingUpdate.Code},
	})
	StorageSettingPostUpdate(updateCtx)
	assertStorageSettingSuccess(t, updateRecorder.Body.Bytes())
	setting := services.GetStorageSetting()
	if setting.MaxUploadSizeMB != 42 || setting.LocalRoot != "data/private-storage" || setting.LocalBaseURL != "/files" {
		t.Fatalf("stored setting = %+v", setting)
	}
}

func TestWxWorkDevicePoolPlatformPermissionsAllowSettingsReadAndUpdate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatalf("migrate system config: %v", err)
	}
	sqls.SetDB(db)

	viewCtx, viewRecorder := newAuthzHandlerTestContext(t, "", &dto.AuthPrincipal{
		UserID: 211, Username: "platform_device_pool_viewer", IsPlatformAccount: true,
		Permissions: []string{constants.PermissionWxWorkDevicePoolView.Code},
	})
	WxWorkProtocolDevicePoolGetSettings(viewCtx)
	assertStorageSettingSuccess(t, viewRecorder.Body.Bytes())

	updateCtx, updateRecorder := newAuthzHandlerTestContext(t, `{
		"adminBaseUrl":"https://device-pool.example.com",
		"username":"pool-admin",
		"password":"pool-password"
	}`, &dto.AuthPrincipal{
		UserID: 212, Username: "platform_device_pool_editor", IsPlatformAccount: true,
		Permissions: []string{constants.PermissionWxWorkDevicePoolUpdate.Code},
	})
	WxWorkProtocolDevicePoolPostUpdate_settings(updateCtx)
	assertStorageSettingSuccess(t, updateRecorder.Body.Bytes())
	settings := services.WxWorkProtocolDevicePoolService.Settings()
	if settings.AdminBaseURL != "https://device-pool.example.com" || settings.Username != "pool-admin" || !settings.PasswordSet {
		t.Fatalf("stored device pool settings = %+v", settings)
	}
}

func assertStorageSettingSuccess(t *testing.T, body []byte) {
	t.Helper()
	var result struct {
		Success   bool `json:"success"`
		ErrorCode int  `json:"errorCode"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode response %q: %v", string(body), err)
	}
	if !result.Success || result.ErrorCode != 0 {
		t.Fatalf("unexpected response: %s", string(body))
	}
}
