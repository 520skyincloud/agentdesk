package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/models"
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

func TestClaimAvailableGUIDReclaimsProviderOfflineInstanceWithStaleUIN(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"err_code":1002,"err_msg":"-102 user if offline","data":null}`))
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
		t.Fatalf("provider-offline instance should become idle: %#v", offline)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("expected two profile probes and one qrcode call, got %#v", calls)
	}
	if calls[2].Path != "/login/get_login_qrcode" || calls[2].GUID != offlineGUID {
		t.Fatalf("qrcode must only be requested for the confirmed offline instance: %#v", calls)
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
