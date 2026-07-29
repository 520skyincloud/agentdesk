package third

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestWxWorkProtocolCallbackHTTPContract(t *testing.T) {
	const (
		guid  = "callback-guid"
		token = "callback-token-for-test"
	)
	db := setupWxWorkProtocolCallbackHandlerTest(t, guid, token)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Any("/callback", WxWorkProtocolAnyCallback)

	t.Run("valid callback returns 200", func(t *testing.T) {
		recorder := serveWxWorkProtocolCallback(router, http.MethodPost, "/callback?t="+token, `{"guid":"`+guid+`","notify_type":1999,"data":{}}`)
		if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "success" {
			t.Fatalf("status=%d body=%q, want 200 success", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("missing token returns 401", func(t *testing.T) {
		recorder := serveWxWorkProtocolCallback(router, http.MethodPost, "/callback", `{"guid":"`+guid+`","notify_type":1999,"data":{}}`)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%q, want 401", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("wrong token returns 401", func(t *testing.T) {
		recorder := serveWxWorkProtocolCallback(router, http.MethodPost, "/callback?token=wrong", `{"guid":"`+guid+`","notify_type":1999,"data":{}}`)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%q, want 401", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("unknown guid returns 404", func(t *testing.T) {
		recorder := serveWxWorkProtocolCallback(router, http.MethodPost, "/callback?token="+token, `{"guid":"unknown-guid","notify_type":1999,"data":{}}`)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%q, want 404", recorder.Code, recorder.Body.String())
		}
		var pendingCount int64
		if err := db.Model(&models.WxWorkProtocolInstance{}).Where("tenant_id = ?", 0).Count(&pendingCount).Error; err != nil {
			t.Fatalf("count tenant-zero instances: %v", err)
		}
		if pendingCount != 0 {
			t.Fatalf("unknown callback created %d tenant-zero instances", pendingCount)
		}
	})

	t.Run("malformed body returns 400", func(t *testing.T) {
		recorder := serveWxWorkProtocolCallback(router, http.MethodPost, "/callback?token="+token, `{`)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%q, want 400", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("processing failure returns 500", func(t *testing.T) {
		recorder := serveWxWorkProtocolCallback(router, http.MethodPost, "/callback?token="+token, `{"guid":"`+guid+`","notify_type":11010,"data":"invalid-message"}`)
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%q, want 500", recorder.Code, recorder.Body.String())
		}
	})
}

func setupWxWorkProtocolCallbackHandlerTest(t *testing.T, guid, token string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Channel{}, &models.WxWorkProtocolInstance{}, &models.MessageSyncLog{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	configJSON, err := json.Marshal(map[string]any{
		"appKey":        "test-app",
		"appSecret":     "test-secret",
		"callbackToken": token,
	})
	if err != nil {
		t.Fatalf("marshal channel config: %v", err)
	}
	now := time.Now()
	channel := &models.Channel{
		TenantID:    101,
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "callback-channel",
		Name:        "callback channel",
		ConfigJSON:  string(configJSON),
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID:     101,
		Guid:         guid,
		ChannelID:    channel.ID,
		HealthStatus: "online",
		Status:       enums.StatusOk,
		AuditFields:  models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	return db
}

func serveWxWorkProtocolCallback(router http.Handler, method, target, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}
