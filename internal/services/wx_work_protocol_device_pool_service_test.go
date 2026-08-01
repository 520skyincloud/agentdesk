package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestProtocolProfileResponseShowsOffline(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "provider offline response", raw: `{"err_code":1002,"err_msg":"-102 user if offline"}`, want: true},
		{name: "successful profile", raw: `{"err_code":0,"err_msg":"success","data":{"persons":[]}}`, want: false},
		{name: "unrelated provider error", raw: `{"err_code":1002,"err_msg":"invalid guid"}`, want: false},
		{name: "invalid json", raw: `not-json`, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := protocolProfileResponseShowsOffline(test.raw); got != test.want {
				t.Fatalf("protocolProfileResponseShowsOffline() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestClaimAvailableGUIDUsesDirectQRCodeProbeWhenProfileRuntimeIsUnavailable(t *testing.T) {
	db := setupWxWorkProtocolDevicePoolTestDB(t)
	const (
		onlineGUID  = "11111111-1111-1111-1111-111111111111"
		offlineGUID = "22222222-2222-2222-2222-222222222222"
	)

	var mu sync.Mutex
	calls := make([]struct {
		Path string
		GUID string
	}, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Path string `json:"path"`
			Data struct {
				GUID string `json:"guid"`
			} `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode protocol request: %v", err)
			http.Error(w, "invalid protocol request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		calls = append(calls, struct {
			Path string
			GUID string
		}{Path: payload.Path, GUID: payload.Data.GUID})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case payload.Path == "/user/get_profile" && payload.Data.GUID == onlineGUID:
			_, _ = w.Write([]byte(`{"err_code":0,"err_msg":"success","data":{"persons":[{"vid":"online-user"}]}}`))
		case payload.Path == "/user/get_profile" && payload.Data.GUID == offlineGUID:
			_, _ = w.Write([]byte(`{"err_code":1014,"err_msg":"runtime not started","data":null}`))
		case payload.Path == "/login/get_login_qrcode" && payload.Data.GUID == offlineGUID:
			_, _ = w.Write([]byte(`{"err_code":0,"err_msg":"success","data":{"qrcode_base64":"test-qrcode"}}`))
		default:
			http.Error(w, "unexpected protocol request", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	configJSON, err := json.Marshal(map[string]any{
		"baseUrl":   server.URL,
		"appKey":    "test-app-key",
		"appSecret": "test-app-secret",
	})
	if err != nil {
		t.Fatalf("marshal channel config: %v", err)
	}
	channel := &models.Channel{
		TenantID:    101,
		Name:        "企微协议测试",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-device-pool-test",
		ConfigJSON:  string(configJSON),
		Status:      enums.StatusOk,
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	now := time.Now()
	expiredAt := now.Add(24 * time.Hour)
	for _, item := range []models.SystemConfig{
		{
			ConfigKey: wxWorkDevicePoolConfigUsername, ConfigValue: "test-admin",
			GroupCode: wxWorkDevicePoolConfigGroup, Status: enums.StatusOk,
		},
		{
			ConfigKey: wxWorkDevicePoolConfigPassword, ConfigValue: "test-password",
			GroupCode: wxWorkDevicePoolConfigGroup, Status: enums.StatusOk,
		},
	} {
		item.CreatedAt = now
		item.UpdatedAt = now
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create device pool setting: %v", err)
		}
	}
	for _, item := range []models.WxWorkProtocolDevicePoolInstance{
		{
			Guid: onlineGUID, Uin: "stale-online-uin", SyncStatus: "online",
			ExpiredAt: &expiredAt, Status: enums.StatusOk,
		},
		{
			Guid: offlineGUID, Uin: "stale-offline-uin", SyncStatus: "online",
			ExpiredAt: &expiredAt, Status: enums.StatusOk,
		},
	} {
		item.CreatedAt = now
		item.UpdatedAt = now
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create device pool item: %v", err)
		}
	}

	guid, err := WxWorkProtocolDevicePoolService.ClaimAvailableGUID(channel)
	if err != nil {
		t.Fatalf("ClaimAvailableGUID() error = %v", err)
	}
	if guid != offlineGUID {
		t.Fatalf("ClaimAvailableGUID() = %q, want %q", guid, offlineGUID)
	}

	online := repositories.WxWorkProtocolDevicePoolRepository.Take(db, "guid = ?", onlineGUID)
	offline := repositories.WxWorkProtocolDevicePoolRepository.Take(db, "guid = ?", offlineGUID)
	if online == nil || online.SyncStatus != "online" {
		t.Fatalf("online instance should remain unavailable for扫码: %#v", online)
	}
	if offline == nil || offline.SyncStatus != "idle" {
		t.Fatalf("qrcode-verified instance should become idle: %#v", offline)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("expected two profile probes and one qrcode call, got %#v", calls)
	}
	if calls[2].Path != "/login/get_login_qrcode" || calls[2].GUID != offlineGUID {
		t.Fatalf("qrcode must directly validate the profile-unavailable instance: %#v", calls)
	}
}

func TestAdoptOnlineDevicePoolInstanceCreatesTenantBindingAndHTTPSCallback(t *testing.T) {
	db := setupWxWorkProtocolDevicePoolTestDB(t)
	if err := db.AutoMigrate(&models.Tenant{}, &models.User{}, &models.Store{}, &models.StoreStaffBinding{}); err != nil {
		t.Fatalf("auto migrate adoption models: %v", err)
	}
	const (
		appKey    = "provider-app-key"
		appSecret = "provider-app-secret"
		guid      = "33333333-3333-3333-3333-333333333333"
	)
	var notifyURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/admin/GetOpenApp" {
			_, _ = w.Write([]byte(`{"code":200,"data":{"app":{"app_key":"` + appKey + `","app_secret":"` + appSecret + `"}}}`))
			return
		}
		var payload struct {
			Path string         `json:"path"`
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid protocol request", http.StatusBadRequest)
			return
		}
		switch payload.Path {
		case "/user/get_profile":
			_, _ = w.Write([]byte(`{"err_code":0,"err_msg":"success","data":{"persons":[{"vid":"employee-vid","info":{"name":"真实员工号"}}]}}`))
		case "/client/set_notify_url":
			notifyURL, _ = payload.Data["notify_url"].(string)
			_, _ = w.Write([]byte(`{"err_code":0,"err_msg":"success","data":{}}`))
		default:
			http.Error(w, "unexpected protocol request", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	previousGatewayURL := wxWorkProtocolGatewayURL
	wxWorkProtocolGatewayURL = server.URL
	t.Cleanup(func() { wxWorkProtocolGatewayURL = previousGatewayURL })

	now := time.Now()
	tokenExpire := now.Add(time.Hour)
	for _, item := range []models.SystemConfig{
		{ConfigKey: wxWorkDevicePoolConfigAdminBaseURL, ConfigValue: server.URL, GroupCode: wxWorkDevicePoolConfigGroup, Status: enums.StatusOk},
		{ConfigKey: wxWorkDevicePoolConfigCallbackBaseURL, ConfigValue: "https://agentdesk.example.test", GroupCode: wxWorkDevicePoolConfigGroup, Status: enums.StatusOk},
		{ConfigKey: wxWorkDevicePoolConfigUsername, ConfigValue: "provider-admin", GroupCode: wxWorkDevicePoolConfigGroup, Status: enums.StatusOk},
		{ConfigKey: wxWorkDevicePoolConfigPassword, ConfigValue: "provider-password", GroupCode: wxWorkDevicePoolConfigGroup, Status: enums.StatusOk},
		{ConfigKey: wxWorkDevicePoolConfigToken, ConfigValue: "provider-token", GroupCode: wxWorkDevicePoolConfigGroup, Status: enums.StatusOk},
		{ConfigKey: wxWorkDevicePoolConfigTokenExpire, ConfigValue: tokenExpire.Format(time.RFC3339), GroupCode: wxWorkDevicePoolConfigGroup, Status: enums.StatusOk},
	} {
		item.CreatedAt = now
		item.UpdatedAt = now
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create setting %s: %v", item.ConfigKey, err)
		}
	}
	tenant := &models.Tenant{TenantCode: "adoption-tenant", ShortName: "测试租户", Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user := &models.User{TenantID: tenant.ID, Username: "store-user", Nickname: "门店员工", Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	store := &models.Store{TenantID: tenant.ID, StoreCode: "S001", Name: "南七店", Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	activeUserID := user.ID
	binding := &models.StoreStaffBinding{
		TenantID: tenant.ID, UserID: user.ID, ActiveUserID: &activeUserID, StoreID: store.ID,
		FallbackToHQ: true, ManualTimeoutMinutes: 10, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create store staff binding: %v", err)
	}
	expiredAt := now.Add(24 * time.Hour)
	pool := &models.WxWorkProtocolDevicePoolInstance{
		Guid: guid, Uin: "online-uin", SyncStatus: "online", ExpiredAt: &expiredAt, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(pool).Error; err != nil {
		t.Fatalf("create device pool row: %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: 9001, Username: "platform-admin", IsPlatformAccount: true}
	result, err := WxWorkProtocolDevicePoolService.Adopt(request.AdoptWxWorkProtocolDevicePoolRequest{
		DevicePoolID: pool.ID, TenantID: tenant.ID, StoreStaffBindingID: binding.ID,
	}, operator)
	if err != nil {
		t.Fatalf("adopt online instance: %v", err)
	}
	if result == nil || !result.NotifyConfigured || result.TenantID != tenant.ID || result.StoreID != store.ID || result.EmployeeName != "真实员工号" {
		t.Fatalf("unexpected adoption result: %+v", result)
	}
	if !strings.HasPrefix(notifyURL, "https://agentdesk.example.test/api/third/wxp?t=") {
		t.Fatalf("unexpected notify URL: %q", notifyURL)
	}
	currentPool := repositories.WxWorkProtocolDevicePoolRepository.Get(db, pool.ID)
	if currentPool == nil || currentPool.BoundWxWorkProtocolInstanceID <= 0 || currentPool.SyncStatus != "bound" {
		t.Fatalf("device pool not bound: %+v", currentPool)
	}
	instance := repositories.WxWorkProtocolInstanceRepository.Get(db, currentPool.BoundWxWorkProtocolInstanceID)
	if instance == nil || instance.TenantID != tenant.ID || instance.StoreStaffBindingID != binding.ID || instance.EmployeeUserID != "employee-vid" {
		t.Fatalf("unexpected adopted instance: %+v", instance)
	}
	channel := repositories.ChannelRepository.GetInTenant(db, instance.ChannelID, tenant.ID)
	if channel == nil {
		t.Fatal("tenant protocol channel was not created")
	}
	cfg, err := ChannelService.ParseWxWorkProtocolChannelConfig(channel.ConfigJSON)
	if err != nil || cfg.CallbackToken == "" || cfg.AppKey != appKey || cfg.AppSecret != appSecret {
		t.Fatalf("unexpected tenant protocol channel config")
	}
	if got := len(cfg.CallbackToken); got != 43 {
		t.Fatalf("callback token length=%d want=43", got)
	}
	if got := len(notifyURL); got > 100 {
		t.Fatalf("notify URL length=%d exceeds provider callback_url limit", got)
	}
	resultJSON, _ := json.Marshal(result)
	if strings.Contains(string(resultJSON), appKey) || strings.Contains(string(resultJSON), appSecret) || strings.Contains(string(resultJSON), cfg.CallbackToken) {
		t.Fatalf("adoption response exposed provider credentials")
	}
	if _, err := WxWorkProtocolDevicePoolService.Adopt(request.AdoptWxWorkProtocolDevicePoolRequest{
		DevicePoolID: pool.ID, TenantID: tenant.ID, StoreStaffBindingID: binding.ID,
	}, operator); err != nil {
		t.Fatalf("idempotent adoption failed: %v", err)
	}
	var instanceCount int64
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("guid = ?", guid).Count(&instanceCount).Error; err != nil {
		t.Fatalf("count adopted instances: %v", err)
	}
	if instanceCount != 1 {
		t.Fatalf("idempotent adoption created %d instances", instanceCount)
	}
	secondUser := &models.User{TenantID: tenant.ID, Username: "store-user-2", Nickname: "门店员工二", Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(secondUser).Error; err != nil {
		t.Fatalf("create second store user: %v", err)
	}
	secondActiveUserID := secondUser.ID
	secondBinding := &models.StoreStaffBinding{
		TenantID: tenant.ID, UserID: secondUser.ID, ActiveUserID: &secondActiveUserID, StoreID: store.ID,
		FallbackToHQ: true, ManualTimeoutMinutes: 10, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(secondBinding).Error; err != nil {
		t.Fatalf("create second binding in same store: %v", err)
	}
	secondPool := &models.WxWorkProtocolDevicePoolInstance{
		Guid: "44444444-4444-4444-4444-444444444444", Uin: "second-online-uin", SyncStatus: "online",
		ExpiredAt: &expiredAt, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(secondPool).Error; err != nil {
		t.Fatalf("create second device pool row: %v", err)
	}
	secondResult, err := WxWorkProtocolDevicePoolService.Adopt(request.AdoptWxWorkProtocolDevicePoolRequest{
		DevicePoolID: secondPool.ID, TenantID: tenant.ID, StoreStaffBindingID: secondBinding.ID,
	}, operator)
	if err != nil {
		t.Fatalf("adopt second binding in same store: %v", err)
	}
	if secondResult == nil || secondResult.StoreID != store.ID || secondResult.StoreStaffBindingID != secondBinding.ID {
		t.Fatalf("unexpected second adoption result: %+v", secondResult)
	}
	var currentStoreInstances int64
	if err := db.Model(&models.WxWorkProtocolInstance{}).
		Where("tenant_id = ? AND store_id = ? AND replaced_by_instance_id = 0 AND status <> ?", tenant.ID, store.ID, enums.StatusDeleted).
		Count(&currentStoreInstances).Error; err != nil {
		t.Fatalf("count current Store instances: %v", err)
	}
	if currentStoreInstances != 2 {
		t.Fatalf("same Store current instances=%d, want 2 for two bindings", currentStoreInstances)
	}
	if _, err := WxWorkProtocolDevicePoolService.Adopt(request.AdoptWxWorkProtocolDevicePoolRequest{
		DevicePoolID: pool.ID, TenantID: tenant.ID + 1, StoreStaffBindingID: binding.ID,
	}, operator); err == nil {
		t.Fatal("cross-tenant adoption must be rejected")
	}
}

func TestValidateAdoptableDevicePoolInstanceRejectsUnavailableStates(t *testing.T) {
	now := time.Now()
	expired := now.Add(-time.Minute)
	tests := []struct {
		name string
		item *models.WxWorkProtocolDevicePoolInstance
	}{
		{name: "missing", item: nil},
		{name: "expired", item: &models.WxWorkProtocolDevicePoolInstance{Guid: "expired", Uin: "uin", SyncStatus: "online", ExpiredAt: &expired, Status: enums.StatusOk}},
		{name: "offline", item: &models.WxWorkProtocolDevicePoolInstance{Guid: "offline", Uin: "uin", SyncStatus: "offline", Status: enums.StatusOk}},
		{name: "not logged in", item: &models.WxWorkProtocolDevicePoolInstance{Guid: "idle", SyncStatus: "idle", Status: enums.StatusOk}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateAdoptableDevicePoolInstance(test.item, now); err == nil {
				t.Fatalf("expected %s instance to be rejected", test.name)
			}
		})
	}
}

func TestRepairMessagesUsesDocumentedBoundedSyncRequest(t *testing.T) {
	db := setupWxWorkProtocolDevicePoolTestDB(t)
	var captured struct {
		Path string         `json:"path"`
		Data map[string]any `json:"data"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"err_code":0,"err_msg":"success","data":{}}`))
	}))
	t.Cleanup(server.Close)
	configJSON, _ := json.Marshal(map[string]any{"baseUrl": server.URL, "appKey": "app", "appSecret": "secret"})
	now := time.Now()
	channel := &models.Channel{
		TenantID: 101, ChannelType: enums.ChannelTypeWxWorkProtocol, ChannelID: "repair-channel",
		Name: "repair", ConfigJSON: string(configJSON), Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "repair-guid", ChannelID: channel.ID, MessageGapFromSeq: "321", MessageGapToSeq: "400",
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	pool := &models.WxWorkProtocolDevicePoolInstance{
		Guid: instance.Guid, BoundWxWorkProtocolInstanceID: instance.ID, SyncStatus: "bound", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(pool).Error; err != nil {
		t.Fatalf("create pool row: %v", err)
	}
	result, err := WxWorkProtocolDevicePoolService.RepairMessages(
		request.RepairWxWorkProtocolMessagesRequest{ID: pool.ID, Limit: 999},
		&dto.AuthPrincipal{UserID: 1, Username: "platform", IsPlatformAccount: true},
	)
	if err != nil {
		t.Fatalf("repair messages: %v", err)
	}
	if result == nil || result.SyncKey != "321" || result.Limit != 100 {
		t.Fatalf("unexpected repair result: %+v", result)
	}
	if captured.Path != "/sync/sync_msg" || captured.Data["guid"] != instance.Guid || captured.Data["sync_key"] != "321" || captured.Data["limit"] != float64(100) {
		t.Fatalf("unexpected repair protocol request: %+v", captured)
	}
}

func setupWxWorkProtocolDevicePoolTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Channel{},
		&models.SystemConfig{},
		&models.WxWorkProtocolDevicePoolInstance{},
		&models.WxWorkProtocolInstance{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
